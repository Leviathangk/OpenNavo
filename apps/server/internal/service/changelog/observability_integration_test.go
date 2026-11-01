//go:build integration

package changelog_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	pipeline "github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/service/changelog"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func requireSourceFields(t *testing.T, stats map[string]any, sid int64, attempt int) {
	t.Helper()
	require.Equal(t, sid, stats["source_id"])
	require.Equal(t, "observed", stats["package_token"])
	require.Equal(t, attempt, stats["attempt"])
	target, _ := stats["source_url"].(string)
	require.Equal(t, "https://api.github.com/repos/Homebrew/homebrew-cask/commits", target)
	require.NotContains(t, target, "?")
	require.NotContains(t, target, "secret")
}

func TestFetchStatsIdentifySourceAndBackoffReenqueues(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"observed","version":"1.0.0"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	var pid, sid int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id FROM packages WHERE token='observed'").Scan(&pid).Error)
	require.NoError(t, st.DB.WithContext(ctx).Raw("INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by) VALUES(?,'homebrew_commits','{\"repo\":\"Homebrew/homebrew-cask\",\"path\":\"Casks/o/observed.rb\"}',90,'auto') RETURNING id", pid).Scan(&sid).Error)

	var respond func(*http.Request) (*http.Response, error)
	fetcher := pipeline.NewFetcher("test")
	fetcher.HTTP.TokenProvider = func(context.Context) (string, error) { return "ghp_secret", nil }
	fetcher.HTTP.Client = &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) { return respond(r) })}
	now := time.Now().UTC()
	svc := &changelog.Service{Store: st, Fetcher: fetcher, Now: func() time.Time { return now }}
	fetch := svc.Fetch

	// Failure: the stats name the source and the attempt; the source backs off via next_fetch_at.
	respond = func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	stats, err := fetch(ctx, sid)
	require.Error(t, err)
	requireSourceFields(t, stats, sid, 1)
	require.Equal(t, "upstream_not_found", stats["errorCategory"])
	stats, err = fetch(ctx, sid)
	require.Error(t, err)
	requireSourceFields(t, stats, sid, 2)
	var state struct {
		FailCount   int
		NextFetchAt time.Time
	}
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT fail_count,next_fetch_at FROM changelog_sources WHERE id=?", sid).Scan(&state).Error)
	require.Equal(t, 2, state.FailCount)
	require.WithinDuration(t, now.Add(4*time.Hour), state.NextFetchAt, time.Second)
	due, err := st.ChangelogDue(ctx, now)
	require.NoError(t, err)
	require.NotContains(t, due, sid, "backing off")
	due, err = st.ChangelogDue(ctx, state.NextFetchAt)
	require.NoError(t, err)
	require.Contains(t, due, sid, "due again once next_fetch_at passes")
	queue := &queuedSources{}
	svc.Queue = queue
	svc.Now = func() time.Time { return state.NextFetchAt }
	scheduled, err := svc.Schedule(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, scheduled["enqueued"])
	svc.Now = func() time.Time { return now }

	// A real timeout while reading upstream reports the source and records the failure after the run
	// context has ended.
	t.Run("timeout", func(t *testing.T) {
		runCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()
		respond = func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		var before int
		require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT fail_count FROM changelog_sources WHERE id=?", sid).Scan(&before).Error)
		stats, err := fetch(runCtx, sid)
		require.Error(t, err)
		require.ErrorIs(t, runCtx.Err(), context.DeadlineExceeded)
		requireSourceFields(t, stats, sid, before+1)
		require.Equal(t, "deadline_exceeded", stats["errorCategory"])
		var after int
		require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT fail_count FROM changelog_sources WHERE id=?", sid).Scan(&after).Error)
		require.Equal(t, before+1, after, "the failure is recorded even after the deadline")
	})
	// Cancellation (worker shutdown) reports the source but is not a failure: fail_count and the backoff
	// stay, and so does next_fetch_at. The canceled task is requeued by asynq itself; making the source due
	// now would add a second queue entry once the uniqueness lock expires.
	t.Run("canceled", func(t *testing.T) {
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		respond = func(*http.Request) (*http.Response, error) {
			cancel()
			return nil, context.Canceled
		}
		var before int
		require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT fail_count FROM changelog_sources WHERE id=?", sid).Scan(&before).Error)
		var beforeNext time.Time
		require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT next_fetch_at FROM changelog_sources WHERE id=?", sid).Scan(&beforeNext).Error)
		stats, err := fetch(runCtx, sid)
		require.Error(t, err)
		requireSourceFields(t, stats, sid, before+1)
		require.Equal(t, "canceled", stats["errorCategory"])
		var after struct {
			FailCount   int
			NextFetchAt time.Time
		}
		require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT fail_count,next_fetch_at FROM changelog_sources WHERE id=?", sid).Scan(&after).Error)
		require.Equal(t, before, after.FailCount, "cancellation does not count as a failure")
		require.Equal(t, beforeNext.UTC(), after.NextFetchAt.UTC(), "cancellation leaves the schedule to the requeued task")
	})

	// Success carries the same fields; attempt resets with fail_count.
	respond = func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`[]`))}, nil
	}
	stats, err = fetch(ctx, sid)
	require.NoError(t, err)
	requireSourceFields(t, stats, sid, 4)
	stats, err = fetch(ctx, sid)
	require.NoError(t, err)
	requireSourceFields(t, stats, sid, 1)

	// Early exits keep the fields: excluded (skipped), an unsupported type outside the fetch scope, and a
	// config that is not an object.
	_, err = st.SetPackageChangelogSettings(ctx, pid, true, store.AuditActor{})
	require.NoError(t, err)
	stats, err = fetch(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, true, stats["skipped"])
	requireSourceFields(t, stats, sid, 1)
	_, err = st.SetPackageChangelogSettings(ctx, pid, false, store.AuditActor{})
	require.NoError(t, err)
	var sparkle int64
	require.NoError(t, st.DB.WithContext(ctx).Raw(`INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by) VALUES(?,'sparkle','{"url":"https://example.com/appcast.xml?token=secret"}',30,'manual') RETURNING id`, pid).Scan(&sparkle).Error)
	stats, err = fetch(ctx, sparkle)
	require.Error(t, err)
	require.Equal(t, sparkle, stats["source_id"])
	require.Equal(t, "observed", stats["package_token"])
	require.Equal(t, "sparkle", stats["source"])
	require.Equal(t, 1, stats["attempt"])
	require.Equal(t, "", stats["source_url"], "unsupported types expose no URL, never the configured query")
	require.NoError(t, st.DB.WithContext(ctx).Exec(`UPDATE changelog_sources SET config='[]' WHERE id=?`, sid).Error)
	stats, err = fetch(ctx, sid)
	require.Error(t, err)
	require.Equal(t, sid, stats["source_id"])
	require.Equal(t, "observed", stats["package_token"])
	require.Equal(t, 1, stats["attempt"])
	require.Contains(t, stats, "source_url")
	missing, err := svc.Fetch(ctx, 999999)
	require.Error(t, err)
	require.Equal(t, int64(999999), missing["source_id"])
	require.Contains(t, missing, "package_token")
}

// A backlog longer than the uniqueness lock must not enqueue the same source twice, and two tasks for one
// source (schedule and catalog outbox) must not both fetch.
func TestScheduleBacklogDoesNotDuplicateSources(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"backlog","version":"1.0.0"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	var pid, sid int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id FROM packages WHERE token='backlog'").Scan(&pid).Error)
	require.NoError(t, st.DB.WithContext(ctx).Raw("INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by) VALUES(?,'homebrew_commits','{\"repo\":\"Homebrew/homebrew-cask\",\"path\":\"Casks/b/backlog.rb\"}',90,'auto') RETURNING id", pid).Scan(&sid).Error)
	requests := 0
	fetcher := pipeline.NewFetcher("test")
	fetcher.HTTP.Client = &http.Client{Transport: responseTransport(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`[]`))}, nil
	})}
	clock := time.Now().UTC()
	queue := &queuedSources{}
	svc := &changelog.Service{Store: st, Fetcher: fetcher, Queue: queue, Now: func() time.Time { return clock }}

	start := clock
	stats, err := svc.Schedule(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats["enqueued"])
	// The task waits in a backlog: later ticks (past the 20-minute lock) do not enqueue it again.
	for _, wait := range []time.Duration{10 * time.Minute, 30 * time.Minute, 90 * time.Minute} {
		clock = start.Add(wait)
		stats, err = svc.Schedule(ctx)
		require.NoError(t, err)
		require.Equal(t, 0, stats["enqueued"], wait)
	}
	require.Equal(t, 1, queue.Count)

	// The queued task runs and sets the real schedule.
	_, err = svc.Fetch(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, 1, requests)
	stats, err = svc.Schedule(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, stats["enqueued"])

	// A catalog version change enqueues the fetch through the outbox and leases the source too.
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE changelog_sources SET next_fetch_at=now()-interval '1 minute' WHERE id=?", sid).Error)
	rows, err := st.CatalogRows(ctx, "cask")
	require.NoError(t, err)
	next, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"backlog","version":"1.1.0"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: next, Previous: rows["backlog"]}}))
	due, err := st.ChangelogDue(ctx, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.NotContains(t, due, sid, "leased by the catalog outbox enqueue")

	// A task lost without running waits out the lease, then the source is enqueued again.
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE changelog_sources SET next_fetch_at=? WHERE id=?", clock, sid).Error)
	stats, err = svc.Schedule(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats["enqueued"])
	clock = clock.Add(2*time.Hour + time.Second)
	stats, err = svc.Schedule(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats["enqueued"], "lease expired")
}
