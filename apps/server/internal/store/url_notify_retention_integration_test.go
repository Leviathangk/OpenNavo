//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestURLNotificationRetentionPreservesDeliveryAndSettingsBarrier(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	before := now.Add(-store.URLNotificationRetention)
	insert := func(path, state string, at time.Time) int64 {
		t.Helper()
		var id int64
		require.NoError(t, st.DB.Raw(`INSERT INTO url_notify_outbox(url,path,locale,reason,state,announce,updated_at,next_attempt_at) VALUES(?,?,'en-US','updated',?,true,?,?) RETURNING id`, "https://site.example"+path, path, state, at, now).Scan(&id).Error)
		return id
	}
	staleSuccess := insert("/apps/old", "announced", before.Add(-time.Second))
	staleDead := insert("/apps/dead", "dead", before.Add(-time.Second))
	recent := insert("/apps/recent", "announced", before)
	live := []int64{insert("/apps/pending", "pending", before.Add(-time.Hour)), insert("/apps/invalidated", "invalidated", before.Add(-time.Hour)), insert("/apps/probed", "probed", before.Add(-time.Hour))}
	replaced := insert("/apps/replaced", "dead", now)
	witness := insert("/apps/replaced", "announced", before.Add(-time.Hour))
	barrier := insert("/", "dead", before.Add(-time.Hour))
	require.NoError(t, st.DB.Exec("UPDATE url_notify_state SET probe_ready_at=?,probe_failures=3", now.Add(time.Hour)).Error)
	claim, err := st.ClaimURLNotifications(ctx, "before", now, 100, 0)
	require.NoError(t, err)
	require.Empty(t, claim)
	// A locked receipt does not hold up cleanup or allow deleting its superseding witness.
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, tx.Exec("SELECT id FROM url_notify_outbox WHERE id=? FOR UPDATE", replaced).Error)
		n, err := st.PruneURLNotifications(ctx, before, 1000)
		require.NoError(t, err)
		require.Equal(t, int64(2), n)
		var exists bool
		require.NoError(t, st.DB.Raw("SELECT EXISTS(SELECT 1 FROM url_notify_outbox WHERE id=?)", witness).Scan(&exists).Error)
		require.True(t, exists)
		return nil
	}))
	n, err := st.PruneURLNotifications(ctx, before, 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	n, err = st.PruneURLNotifications(ctx, before, 1000)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	var remaining []int64
	require.NoError(t, st.DB.Raw("SELECT id FROM url_notify_outbox ORDER BY id").Scan(&remaining).Error)
	require.ElementsMatch(t, append(live, recent, barrier), remaining)
	for _, id := range []int64{staleSuccess, staleDead, replaced, witness} {
		require.NotContains(t, remaining, id)
	}
	claim, err = st.ClaimURLNotifications(ctx, "after", now, 100, 0)
	require.NoError(t, err)
	require.Empty(t, claim)
	cooldown, err := st.URLProbeReadyAt(ctx, now, time.Hour)
	require.NoError(t, err)
	require.WithinDuration(t, now.Add(time.Hour), cooldown, time.Second)
	queue, err := st.URLNotifyQueue(ctx, now)
	require.NoError(t, err)
	require.Equal(t, 1, queue.DeadAnnouncements, "the settings barrier stays visible after ordinary retention")
	// A later successful settings refresh releases the barrier, even after retention.
	latest := insert("/", "announced", before.Add(-time.Hour))
	n, err = st.PruneURLNotifications(ctx, before, 1000)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	claim, err = st.ClaimURLNotifications(ctx, "released", now, 100, 0)
	require.NoError(t, err)
	require.Len(t, claim, len(live))
	var exists bool
	require.NoError(t, st.DB.Raw("SELECT EXISTS(SELECT 1 FROM url_notify_outbox WHERE id=?)", latest).Scan(&exists).Error)
	require.True(t, exists)
	// The hard cap applies even when a caller requests a larger batch.
	require.NoError(t, st.DB.Exec(`INSERT INTO url_notify_outbox(url,path,locale,reason,state,updated_at)
 SELECT 'https://site.example/apps/batch-'||n,'/apps/batch-'||n,'en-US','updated','announced',? FROM generate_series(1,1005) n`, before.Add(-time.Hour)).Error)
	n, err = st.PruneURLNotifications(ctx, before, 5000)
	require.NoError(t, err)
	require.Equal(t, int64(store.URLNotificationPruneBatch), n)
	n, err = st.PruneURLNotifications(ctx, before, 5000)
	require.NoError(t, err)
	require.Equal(t, int64(5), n)

}
