//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestURLProbeNullDeadlineUsesActualBackoff(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, tc := range []struct {
		name       string
		overloaded bool
		retryAfter time.Duration
		want       time.Time
	}{
		{"success", false, 0, time.Unix(0, 0).UTC()},
		{"first overload", true, time.Second, now.Add(30 * time.Second)},
		{"longer retry", true, 40 * time.Second, now.Add(40 * time.Second)},
		{"capped retry", true, time.Hour, now.Add(45 * time.Second)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, st.DB.Exec("UPDATE url_notify_state SET probe_ready_at=NULL,probe_failures=0").Error)
			require.NoError(t, st.RecordURLProbe(ctx, now, now.Add(tc.retryAfter), tc.overloaded, 45*time.Second))
			var saved struct {
				ProbeReadyAt  time.Time
				ProbeFailures int
			}
			require.NoError(t, st.DB.Raw("SELECT probe_ready_at,probe_failures FROM url_notify_state").Scan(&saved).Error)
			require.Equal(t, tc.want, saved.ProbeReadyAt.UTC(), "a NULL deadline must not become the full retry cap")
			if tc.overloaded {
				require.Equal(t, 1, saved.ProbeFailures)
			} else {
				require.Zero(t, saved.ProbeFailures)
			}
		})
	}
}

func TestURLPurgeNullDeadlineUsesRequestedDelay(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, retryAfter := range []time.Duration{time.Second, 20 * time.Second, time.Hour} {
		t.Run(retryAfter.String(), func(t *testing.T) {
			require.NoError(t, st.DB.Exec("UPDATE url_notify_state SET next_purge_at=NULL").Error)
			require.NoError(t, st.DelayURLPurge(ctx, now, now.Add(retryAfter), 45*time.Second))
			var saved time.Time
			require.NoError(t, st.DB.Raw("SELECT next_purge_at FROM url_notify_state").Scan(&saved).Error)
			require.Equal(t, now.Add(min(retryAfter, 45*time.Second)), saved.UTC())
		})
	}
}

func TestURLProbeWriteCapsExistingDeadlinesAndExponentialBackoff(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, overloaded := range []bool{true, false} {
		for _, cap := range []time.Duration{5 * time.Second, 45 * time.Second} {
			require.NoError(t, st.DB.Exec("UPDATE url_notify_state SET probe_ready_at='9999-12-31',probe_failures=7").Error)
			require.NoError(t, st.RecordURLProbe(ctx, now, now.Add(365*24*time.Hour), overloaded, cap))
			var saved time.Time
			require.NoError(t, st.DB.Raw("SELECT probe_ready_at FROM url_notify_state").Scan(&saved).Error)
			require.Equal(t, now.Add(cap), saved.UTC(), "the write itself must repair old values and cap every new deadline")
			due, err := st.URLProbeReadyAt(ctx, now.Add(cap), cap)
			require.NoError(t, err)
			require.Equal(t, saved, due)
		}
	}
	// A shorter retry must still honor another worker's valid, bounded deadline.
	require.NoError(t, st.DB.Exec("UPDATE url_notify_state SET probe_ready_at=?,probe_failures=0", now.Add(40*time.Second)).Error)
	require.NoError(t, st.RecordURLProbe(ctx, now, now.Add(time.Second), true, time.Minute))
	due, err := st.URLProbeReadyAt(ctx, now, time.Minute)
	require.NoError(t, err)
	require.Equal(t, now.Add(40*time.Second), due.UTC())
}

func TestURLPurgeRepairsLegacyDeadlineOnReadAndWrite(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, maxRetryAfter := range []time.Duration{5 * time.Second, 45 * time.Second} {
		cap := max(maxRetryAfter, store.URLPurgeInterval)
		for _, write := range []bool{false, true} {
			require.NoError(t, st.DB.Exec("UPDATE url_notify_state SET next_purge_at='9999-12-31'").Error)
			if write {
				require.NoError(t, st.DelayURLPurge(ctx, now, now.Add(365*24*time.Hour), maxRetryAfter))
				var saved time.Time
				require.NoError(t, st.DB.Raw("SELECT next_purge_at FROM url_notify_state").Scan(&saved).Error)
				require.Equal(t, now.Add(cap), saved.UTC())
			}
			due, err := st.URLPurgeReadyAt(ctx, now, maxRetryAfter)
			require.NoError(t, err)
			require.Equal(t, now.Add(cap), due.UTC())
			later, err := st.URLPurgeReadyAt(ctx, now.Add(cap/2), maxRetryAfter)
			require.NoError(t, err)
			require.Equal(t, due, later)
			allowed, err := st.ReserveURLPurge(ctx, now.Add(cap), time.Second)
			require.NoError(t, err)
			require.True(t, allowed)
		}
	}
}

func TestURLPurgeShortRetryCapPreservesReservedInterval(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, maxRetryAfter := range []time.Duration{time.Second, 5 * time.Second, store.URLPurgeInterval} {
		require.NoError(t, st.DB.Exec("UPDATE url_notify_state SET next_purge_at=NULL").Error)
		allowed, err := st.ReserveURLPurge(ctx, now, store.URLPurgeInterval)
		require.NoError(t, err)
		require.True(t, allowed)
		// Read and write through new stores, as other workers and restarts do.
		for _, write := range []bool{false, true} {
			worker := &store.Store{DB: st.DB}
			if write {
				require.NoError(t, worker.DelayURLPurge(ctx, now, now.Add(time.Second), maxRetryAfter))
			}
			var saved time.Time
			require.NoError(t, st.DB.Raw("SELECT next_purge_at FROM url_notify_state").Scan(&saved).Error)
			require.Equal(t, now.Add(store.URLPurgeInterval), saved.UTC())
			due, err := worker.URLPurgeReadyAt(ctx, now, maxRetryAfter)
			require.NoError(t, err)
			require.Equal(t, now.Add(store.URLPurgeInterval), due.UTC())
		}
		allowed, err = st.ReserveURLPurge(ctx, now.Add(store.URLPurgeInterval-time.Microsecond), store.URLPurgeInterval)
		require.NoError(t, err)
		require.False(t, allowed, "a small probe cap must not permit an early purge")
		allowed, err = st.ReserveURLPurge(ctx, now.Add(store.URLPurgeInterval), store.URLPurgeInterval)
		require.NoError(t, err)
		require.True(t, allowed)
	}
}

func TestURLOutboxRepairsLegacyDeadlinesWithoutAdvancingDailyBudget(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	cap := 45 * time.Second
	for _, state := range []string{"pending", "invalidated", "probed"} {
		path := "/articles/" + state
		require.NoError(t, store.QueueURLNotification(ctx, st.DB, store.URLNotification{URL: "https://site.example" + path, Path: path, Locale: "en-US", Announce: true}))
		require.NoError(t, st.DB.Exec("UPDATE url_notify_outbox SET state=?,next_attempt_at='9999-12-31',updated_at=? WHERE path=?", state, now, path).Error)
	}
	require.NoError(t, store.QueueURLNotification(ctx, st.DB, store.URLNotification{URL: "https://site.example/budget", Path: "/budget", Locale: "en-US", Announce: true}))
	require.NoError(t, st.DB.Exec("UPDATE url_notify_outbox SET state='probed',next_attempt_at=?,updated_at=? WHERE path='/budget'", now.Add(24*time.Hour), now).Error)
	for _, offset := range []time.Duration{0, cap / 2} {
		rows, err := st.ClaimURLNotifications(ctx, "waiting", now.Add(offset), 10, cap)
		require.NoError(t, err)
		require.Empty(t, rows)
	}
	rows, err := st.ClaimURLNotifications(ctx, "recovered", now.Add(cap), 10, cap)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	// Scheduling cannot create another unbounded outbox value.
	require.NoError(t, st.DeferURLNotification(ctx, rows[0], now.Add(cap), now.Add(365*24*time.Hour)))
	var due time.Time
	require.NoError(t, st.DB.Raw("SELECT next_attempt_at FROM url_notify_outbox WHERE id=?", rows[0].ID).Scan(&due).Error)
	require.Equal(t, now.Add(cap+24*time.Hour), due.UTC())
}
