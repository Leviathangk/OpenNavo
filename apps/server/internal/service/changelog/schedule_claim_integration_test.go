//go:build integration

package changelog_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	pipeline "github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/service/changelog"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// failingQueue fails the first enqueues with the given error, then succeeds; failed lists every id offered.
type failingQueue struct {
	mu     sync.Mutex
	fail   int
	reason error
	failed []int64
	queued []int64
}

func (q *failingQueue) Enqueue(_ context.Context, _ string, payload jobs.Payload, _ jobs.Metadata) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.fail > 0 {
		q.fail--
		q.failed = append(q.failed, payload.SourceID)
		return "", q.reason
	}
	q.queued = append(q.queued, payload.SourceID)
	return "queued", nil
}

// cancelingQueue cancels the tick's context while it enqueues, as a SIGTERM arriving mid-batch does, then
// fails every enqueue: the worker is stopping, so nothing in this batch can become a task.
type cancelingQueue struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

func (q *cancelingQueue) Enqueue(ctx context.Context, _ string, _ jobs.Payload, _ jobs.Metadata) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.cancel()
	return "", ctx.Err()
}

func sourceState(t *testing.T, st *store.Store, id int64) time.Time {
	t.Helper()
	var next time.Time
	require.NoError(t, st.DB.Raw("SELECT next_fetch_at FROM changelog_sources WHERE id=?", id).Scan(&next).Error)
	return next
}

// scheduleFixture makes `count` due Homebrew sources, newest package last, and returns their ids with a
// service whose clock is fixed. Order matters: the claim returns ids in the query's popularity order.
func scheduleFixture(t *testing.T, count int) (*store.Store, *changelog.Service, []int64, time.Time) {
	t.Helper()
	ctx := context.Background()
	st := testutil.NewStore(t)
	now := time.Now().UTC().Add(-time.Hour)
	ids := make([]int64, 0, count)
	for i := range count {
		token := string(rune('a'+i)) + "-source"
		p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"`+token+`","version":"1.0.0"}`))
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
		var pid int64
		require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token=?", token).Scan(&pid).Error)
		var sid int64
		require.NoError(t, st.DB.Raw(`INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by,next_fetch_at)
			VALUES(?,'homebrew_commits','{"repo":"Homebrew/homebrew-cask","path":"Casks/x/x.rb"}',90,'auto',?) RETURNING id`, pid, now).Scan(&sid).Error)
		ids = append(ids, sid)
	}
	svc := &changelog.Service{Store: st, Now: func() time.Time { return now }}
	return st, svc, ids, now
}

// A queue error must not leave the two-hour lease on a source that never became a task, and the rest of
// the batch must not keep it either: the next tick has to offer all of them again.
func TestScheduleReleasesTheLeaseWhenEnqueueFails(t *testing.T) {
	st, svc, ids, now := scheduleFixture(t, 3)
	queue := &failingQueue{fail: 1, reason: errors.New("redis unavailable")}
	svc.Queue = queue
	_, err := svc.Schedule(context.Background())
	require.Error(t, err)

	// The failing enqueue ends the tick, so only the first source was offered; the rest never reached the
	// queue at all, and none of the three may keep the lease.
	require.Equal(t, ids[:1], queue.failed)
	for _, id := range ids {
		require.WithinDuration(t, now, sourceState(t, st, id), time.Second, "source %d is due again", id)
	}
	// The next tick offers all three sources, so nothing was lost to the failed batch.
	queue.fail = 0
	stats, err := svc.Schedule(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, stats["enqueued"])
	require.ElementsMatch(t, ids, queue.queued)
}

// The duplicate-lock rejection (InvalidState) from an archived task is not a queue failure: the rest of the
// batch still enqueues, and only the rejected source goes back to being due.
func TestScheduleReleasesOnlyTheDuplicateRejectedSource(t *testing.T) {
	st, svc, ids, now := scheduleFixture(t, 3)
	queue := &failingQueue{fail: 1, reason: &domain.AppError{Code: domain.CodeInvalidState, HTTPStatus: 409}}
	svc.Queue = queue
	stats, err := svc.Schedule(context.Background())
	require.NoError(t, err)
	require.Equal(t, ids[0], queue.failed[0])
	require.Equal(t, 2, stats["enqueued"])
	require.WithinDuration(t, now, sourceState(t, st, ids[0]), time.Second)
	// The two that were enqueued keep their lease, so the next tick does not enqueue them twice.
	for _, id := range ids[1:] {
		require.WithinDuration(t, now.Add(2*time.Hour), sourceState(t, st, id), time.Second)
	}
}

// A canceled fetch is not made due. asynq archives the task instead of requeueing it (Retries: 0 selects the
// archive branch), and an archived task still holds the 20-minute uniqueness lock. Keeping the lease is what
// stops the recovery delay from growing past fetchLease = 2h; the price is that a canceled source can sit
// unscheduled for up to 2h.
func TestCanceledFetchKeepsNextFetchUnchanged(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"canceled","version":"1.0.0"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	var pid, sid int64
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='canceled'").Scan(&pid).Error)
	require.NoError(t, st.DB.Raw(`INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by)
		VALUES(?,'homebrew_commits','{"repo":"Homebrew/homebrew-cask","path":"Casks/c/canceled.rb"}',90,'auto') RETURNING id`, pid).Scan(&sid).Error)
	// A lease is already held by the schedule that enqueued this run.
	lease := time.Now().UTC().Add(90 * time.Minute).Truncate(time.Second)
	require.NoError(t, st.DB.Exec("UPDATE changelog_sources SET next_fetch_at=? WHERE id=?", lease, sid).Error)

	fetcher := pipeline.NewFetcher("test")
	fetcher.HTTP.Client = &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
		return nil, context.Canceled
	})}
	svc := &changelog.Service{Store: st, Fetcher: fetcher, Now: func() time.Time { return time.Now().UTC() }}
	stats, err := svc.Fetch(ctx, sid)
	require.Error(t, err)
	require.Equal(t, "canceled", stats["status"])
	require.Equal(t, lease.UTC(), sourceState(t, st, sid).UTC(), "cancellation leaves the lease in place")
}

// The archive path, end to end: a scheduled run is canceled by a shutdown, asynq archives it (Retries: 0)
// so it never runs again, and only the claim's lease stands between the source and a duplicate enqueue.
// Until the lease runs out the next ticks must leave it alone; the first tick after it must pick it up.
func TestArchivedCanceledFetchIsNotEnqueuedAgainBeforeTheLeaseEnds(t *testing.T) {
	ctx := context.Background()
	st, svc, ids, now := scheduleFixture(t, 1)
	id := ids[0]
	firstTick := &failingQueue{}
	svc.Queue = firstTick
	stats, err := svc.Schedule(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats["enqueued"])
	require.Equal(t, ids, firstTick.queued)

	// The run reaches the HTTP call and a worker shutdown cancels it. No retry, so asynq archives the task:
	// nothing is waiting to run, and the lease from the claim is the only thing holding the source.
	svc.Fetcher = pipeline.NewFetcher("test")
	svc.Fetcher.HTTP.Client = &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
		return nil, context.Canceled
	})}
	stats, err = svc.Fetch(ctx, id)
	require.Error(t, err)
	require.Equal(t, "canceled", stats["status"])
	lease := now.Add(2 * time.Hour)
	require.WithinDuration(t, lease, sourceState(t, st, id), time.Second, "the canceled run leaves the lease alone")

	// A tick before the lease ends must not enqueue the source again: the archived task cannot run, so a
	// second copy would be the only one that does, and two tasks for one source would race on it.
	early := &failingQueue{}
	next := &changelog.Service{Store: st, Queue: early, Now: func() time.Time { return now.Add(time.Hour) }}
	stats, err = next.Schedule(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, stats["enqueued"])
	require.Empty(t, early.queued)
	require.WithinDuration(t, lease, sourceState(t, st, id), time.Second, "the early tick keeps the lease")

	// The first tick after the lease expires does pick it up, so the canceled run delays the source by the
	// lease and no more.
	late := &failingQueue{}
	after := &changelog.Service{Store: st, Queue: late, Now: func() time.Time { return lease.Add(time.Minute) }}
	stats, err = after.Schedule(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats["enqueued"])
	require.Equal(t, ids, late.queued)
}

// The release has to survive the shutdown that makes it necessary: the tick's context is already over when
// the batch is interrupted, so a plain ctx there would leave every claimed source waiting out fetchLease for
// a task that was never enqueued — the very behaviour the claim and release pair exists to avoid.
func TestScheduleReleasesTheLeaseWhenTheTickIsCanceledMidBatch(t *testing.T) {
	st, _, ids, now := scheduleFixture(t, 3)
	ctx, cancel := context.WithCancel(context.Background())
	svc := &changelog.Service{Store: st, Queue: &cancelingQueue{cancel: cancel}, Now: func() time.Time { return now }}
	_, err := svc.Schedule(ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled, "the tick reports why it stopped")
	// The enqueued source is past the cancel too, so it never became a task: all three go back to due.
	for _, id := range ids {
		require.WithinDuration(t, now, sourceState(t, st, id), time.Second, "source %d is due again after the shutdown", id)
	}
}

// When the queue itself fails and the release fails too, both must reach the caller: replacing one with the
// other would turn a queue outage (network_error) into an unexplained unknown-category failure.
func TestScheduleReportsAQueueFailureWhoseReleaseAlsoFailed(t *testing.T) {
	st, svc, ids, now := scheduleFixture(t, 2)
	queueErr := &net.OpError{Op: "write", Net: "tcp", Err: errors.New("connection reset by peer")}
	releaseErr := &net.OpError{Op: "write", Net: "tcp", Err: errors.New("database is shutting down")}
	svc.Queue = &failingQueue{fail: 1, reason: queueErr}
	svc.Release = func(context.Context, []int64) error { return releaseErr }
	_, err := svc.Schedule(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, queueErr, "the queue failure is not lost")
	require.ErrorIs(t, err, releaseErr, "the release failure is not lost either")
	// The release failed, so the lease it was meant to give back stays: that is the real consequence an
	// operator has to see, and this assertion keeps the test honest about it.
	for _, id := range ids {
		require.WithinDuration(t, now.Add(2*time.Hour), sourceState(t, st, id), time.Second, "source %d keeps the lease", id)
	}
}

// ClaimChangelogDue is what stops two schedule ticks from enqueuing one source twice: across concurrent
// claimants every due source is claimed exactly once, and a later claimant with the same clock sees nothing.
func TestClaimChangelogDueIsExclusiveAcrossConcurrentClaimants(t *testing.T) {
	st, _, ids, now := scheduleFixture(t, 4)
	ctx := context.Background()
	type result struct {
		ids []int64
		err error
	}
	results := make(chan result, 2)
	var start sync.WaitGroup
	start.Add(1)
	for range 2 {
		go func() {
			start.Wait()
			claimed, err := st.ClaimChangelogDue(ctx, now, now.Add(2*time.Hour))
			results <- result{claimed, err}
		}()
	}
	start.Done()
	first, second := <-results, <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	claimed := append(append([]int64{}, first.ids...), second.ids...)
	require.ElementsMatch(t, ids, claimed, "every due source is claimed by one of the two claimants")
	require.Len(t, claimed, len(ids), "no source is claimed twice: %v / %v", first.ids, second.ids)
	// Both claimants used the same clock and lease end; nothing is left for a third.
	again, err := st.ClaimChangelogDue(ctx, now, now.Add(2*time.Hour))
	require.NoError(t, err)
	require.Empty(t, again)
}
