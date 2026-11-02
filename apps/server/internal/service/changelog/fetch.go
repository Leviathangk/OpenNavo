package changelog

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	pipeline "github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/jobs"
)

// fetchLease is how long a source enqueued by the schedule stays claimed. Its next_fetch_at moves this far
// ahead at enqueue, so later ticks cannot enqueue it again while the task waits in a backlog; the fetch
// itself then sets the real next_fetch_at (interval on success, backoff on failure). Only a task that is
// lost without running waits out the lease.
const fetchLease = 2 * time.Hour

// identityLookupTimeout bounds the stats-only source lookup that runs after a failed fetch, when the run
// context may already be over. It only fills in observability fields, so it must never hold the job open.
const identityLookupTimeout = 5 * time.Second

type Enqueuer interface {
	Enqueue(context.Context, string, jobs.Payload, jobs.Metadata) (string, error)
}

func interval(rank *int) time.Duration {
	if rank != nil && *rank <= 200 {
		return 3 * time.Hour
	}
	if rank != nil && *rank <= 1000 {
		return 12 * time.Hour
	}
	return 48 * time.Hour
}
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) release(ctx context.Context, ids []int64) error {
	if s.Release != nil {
		return s.Release(ctx, ids)
	}
	return s.Store.ReleaseChangelogClaim(ctx, ids, s.now())
}

// Fetch reads one changelog source. Every outcome (success, failure, timeout, cancellation) reports which
// source it was: source_id, package_token, source_url (scheme, host and path only: no query, no
// credentials) and attempt, the 1-based consecutive attempt counted from changelog_sources.fail_count.
func (s *Service) Fetch(ctx context.Context, id int64) (map[string]any, error) {
	observed := map[string]any{"source_id": id}
	stats, err := s.fetch(ctx, id, observed)
	if _, known := observed["package_token"]; !known {
		// Early exits (a source outside the fetchable scope such as sparkle/webpage, a missing package,
		// a store error) still say which source and package it was when the row exists. The run context is
		// usually already canceled or past its deadline here, so this lookup gets its own short deadline:
		// WithoutCancel alone would let a stuck database hold the job open for as long as the pool waits.
		lookup, cancel := context.WithTimeout(context.WithoutCancel(ctx), identityLookupTimeout)
		identity, lookupErr := s.Store.ChangelogSourceIdentity(lookup, id)
		cancel()
		if lookupErr == nil {
			var settings map[string]any
			_ = json.Unmarshal(identity.Config, &settings)
			observed["attempt"], observed["source"], observed["package_token"] = identity.FailCount+1, identity.Type, identity.Token
			observed["source_url"] = pipeline.SourceURL(identity.Type, settings)
		}
	}
	for _, key := range []string{"package_token", "source_url", "attempt"} {
		if _, set := observed[key]; !set {
			observed[key] = nil
		}
	}
	if stats == nil {
		stats = map[string]any{}
	}
	for key, value := range observed {
		if _, set := stats[key]; !set {
			stats[key] = value
		}
	}
	if err != nil && stats["errorCategory"] == nil {
		category, httpStatus := pipeline.Failure(err)
		stats["errorCategory"] = category
		if httpStatus != 0 {
			stats["httpStatus"] = httpStatus
		}
	}
	return stats, err
}

func (s *Service) fetch(ctx context.Context, id int64, observed map[string]any) (map[string]any, error) {
	if err := s.Store.DispatchCacheInvalidations(ctx); err != nil {
		return nil, err
	}
	state, err := s.Store.ChangelogState(ctx, id)
	if err != nil {
		return nil, err
	}
	observed["attempt"] = state.FailCount + 1
	observed["source"] = state.Type
	p, err := s.Store.ChangelogCandidate(ctx, state.PackageID)
	if err != nil {
		return nil, err
	}
	observed["package_token"] = p.Token
	var settings map[string]any
	if err := json.Unmarshal(state.Config, &settings); err != nil {
		return nil, err
	}
	observed["source_url"] = pipeline.SourceURL(state.Type, settings)
	if state.Excluded || !state.Enabled {
		return map[string]any{"skipped": true, "reason": "excluded_or_disabled"}, nil
	}
	source := pipeline.FetchSource{ID: state.ID, Type: state.Type, Config: settings}
	if state.ETag != nil {
		source.ETag = *state.ETag
	}
	if state.LastModified != nil {
		source.LastModified = *state.LastModified
	}
	// An incremental window can miss a version published before first import.
	// Until that exact version's date is known, fetch the bounded full history.
	// The "latest" marker has no version commit date to backfill.
	if state.Type == "homebrew_commits" && state.CurrentVersion != "latest" && (state.CurrentVersionCommittedAt == nil || state.CurrentVersionCommittedAt.IsZero() || state.CurrentVersionCommittedAt.After(s.now())) {
		source.ETag, source.LastModified = "", ""
	} else if state.Type == "homebrew_commits" && state.LastFetchedAt != nil && (state.LastStatus == "ok" || state.LastStatus == "not_modified") {
		since := state.LastFetchedAt.UTC().Add(-24 * time.Hour)
		source.Since = &since
	}
	result, err := s.Fetcher.Fetch(ctx, p, source)
	if err != nil && errors.Is(err, context.Canceled) {
		// A shutdown cancels the run and asynq archives the task instead of requeueing it (Retries: 0 makes
		// `msg.Retried >= msg.Retry` select the archive branch); an archived task still holds the 20-minute
		// uniqueness lock for its type. So this source is not scheduled again right away: it waits out the
		// two-hour fetchLease. Keeping the lease is what stops the recovery delay from growing past 2h; the
		// price is that a canceled source can sit unscheduled for up to 2h. A run timeout takes a different
		// path below — it is recorded as an upstream failure and backs off, which is the existing design.
		category, _ := pipeline.Failure(err)
		return map[string]any{"status": "canceled", "source": state.Type, "errorCategory": category}, err
	}
	if err != nil {
		status, next := "error", s.now().Add(time.Duration(math.Pow(2, float64(min(state.FailCount+1, 8))))*time.Hour)
		if next.After(s.now().Add(7 * 24 * time.Hour)) {
			next = s.now().Add(7 * 24 * time.Hour)
		}
		var rate *pipeline.RateLimitError
		var httpError *pipeline.StatusError
		if errors.As(err, &rate) {
			status, next = "rate_limited", rate.Reset
		} else if errors.As(err, &httpError) && httpError.Status == 404 {
			status = "not_found"
		}
		// Record the failure even after a timeout ended the run context, within its own deadline.
		write, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if saveErr := s.Store.ChangelogFailure(write, id, status, pipeline.FailureMessage(err), next); saveErr != nil {
			return nil, saveErr
		}
		category, httpStatus := pipeline.Failure(err)
		return map[string]any{"status": status, "source": state.Type, "errorCategory": category, "httpStatus": httpStatus}, err
	}
	changed, lifecycle, err := s.Store.SaveFetchedChangelogWithLifecycle(ctx, state, result, s.now().Add(interval(state.Rank30d)), s.now())
	if err != nil {
		return nil, err
	}
	if err := s.Store.DispatchCacheInvalidations(ctx); err != nil {
		return nil, err
	}
	if s.Queue != nil {
		if _, err := s.Store.DispatchOutbox(ctx, func(ctx context.Context, typ string, raw json.RawMessage) error {
			payload, meta, err := jobs.DecodeOutbox(raw)
			if err != nil {
				return err
			}
			_, err = s.Queue.Enqueue(ctx, typ, payload, meta)
			return jobs.OutboxDeliveryError(typ, err)
		}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"versions": len(result.Versions), "releases": len(result.Releases), "notModified": result.NotModified, "source": state.Type, "changed": changed, "autoDisabled": lifecycle.AutoDisabled, "reactivated": lifecycle.Reactivated, "missingVersionDate": lifecycle.MissingVersionDate}, nil
}
func (s *Service) Schedule(ctx context.Context) (map[string]any, error) {
	candidates, err := s.Store.UnresolvedHomebrewSources(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range candidates {
		if _, err := s.resolve(ctx, p); err != nil {
			return nil, err
		}
	}
	// Claim due sources by moving next_fetch_at past the lease before enqueueing, so a backlog longer
	// than the task's uniqueness lock cannot enqueue the same source on the next tick.
	ids, err := s.Store.ClaimChangelogDue(ctx, s.now(), s.now().Add(fetchLease))
	if err != nil {
		return nil, err
	}
	queued := 0
	unqueued := []int64{}
	var queueErr error
	for i, id := range ids {
		_, err := s.Queue.Enqueue(ctx, "changelog:fetch", jobs.Payload{SourceID: id}, jobs.Metadata{Trigger: "event"})
		if err != nil {
			// The lease was taken for this source, but nothing will run: give its next_fetch_at back, or it
			// waits two hours for a task that never existed.
			unqueued = append(unqueued, id)
			var app *domain.AppError
			if errors.As(err, &app) && app.Code == domain.CodeInvalidState {
				continue
			}
			// The queue itself is failing; the rest of this batch is not attempted either and must not
			// keep the lease. The tick still reports the failure so an operator sees the queue problem.
			unqueued = append(unqueued, ids[i+1:]...)
			queueErr = err
			break
		}
		queued++
	}
	var releaseErr error
	if len(unqueued) > 0 {
		// A shutdown cancels the tick's context mid-batch, which is exactly when this release matters: the
		// lease must go back or every source in the batch waits out fetchLease for a task that was never
		// enqueued. Give it its own deadline, like the failure write above.
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		releaseErr = s.release(release, unqueued)
		cancel()
	}
	if queueErr != nil || releaseErr != nil {
		// Join instead of replacing: the caller records a category from the returned error, and a bare
		// release failure classifies as unknown — a queue outage would read as an unexplained error. The
		// queue error keeps its own category (`net.OpError` → network_error) inside the chain.
		return nil, errors.Join(releaseErr, queueErr)
	}
	return map[string]any{"sources": len(ids), "enqueued": queued}, nil
}
