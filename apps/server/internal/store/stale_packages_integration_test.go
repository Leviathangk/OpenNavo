//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestStaleTransitionRollsBackStatusRanksCursorAndNotification(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, token := range []string{"old", "recent"} {
		p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"`+token+`","version":"1"}`))
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	}
	rows, err := st.CatalogRows(ctx, "cask")
	require.NoError(t, err)
	require.NoError(t, st.ApplyAnalytics(ctx, []domain.AnalyticsRow{{ID: rows["old"].ID, Installs30d: 100}, {ID: rows["recent"].ID, Installs30d: 80}}, "2026-10-10"))
	require.NoError(t, st.DB.Exec(`UPDATE package_versions SET brew_committed_at=? WHERE package_id=?`, now.Add(-store.StalePackageAge-time.Second), rows["old"].ID).Error)
	require.NoError(t, st.DB.Exec(`DELETE FROM job_outbox`).Error)
	_, cursor, err := st.CatalogBounds(ctx)
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec(`ALTER TABLE job_outbox ADD CONSTRAINT reject_cache CHECK(job_type<>'cache:invalidate')`).Error)
	stats, err := st.RefreshStalePackages(ctx, now)
	require.Error(t, err)
	require.Zero(t, stats.AutoDisabled)
	p, err := st.PublicPackage(ctx, "cask", "old", "en-US")
	require.NoError(t, err)
	require.False(t, p.Disabled)
	require.Equal(t, 1, *p.Rank30d)
	p, err = st.PublicPackage(ctx, "cask", "recent", "en-US")
	require.NoError(t, err)
	require.Equal(t, 2, *p.Rank30d)
	_, after, err := st.CatalogBounds(ctx)
	require.NoError(t, err)
	require.Equal(t, cursor, after)
	var outbox int64
	require.NoError(t, st.DB.Table("job_outbox").Count(&outbox).Error)
	require.Zero(t, outbox)
	require.NoError(t, st.DB.Exec(`ALTER TABLE job_outbox DROP CONSTRAINT reject_cache`).Error)
	stats, err = st.RefreshStalePackages(ctx, now)
	require.NoError(t, err)
	require.Equal(t, 1, stats.AutoDisabled)
	p, err = st.PublicPackage(ctx, "cask", "old", "en-US")
	require.NoError(t, err)
	require.True(t, p.Disabled)
	require.Nil(t, p.Rank30d)
	p, err = st.PublicPackage(ctx, "cask", "recent", "en-US")
	require.NoError(t, err)
	require.Equal(t, 1, *p.Rank30d)
	// Only the app that flipped is a change; "recent" moved up a rank and gets it with the next snapshot.
	changes, err := st.CatalogChanges(ctx, cursor, 100)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Equal(t, "old", changes[0].Token)
	require.NoError(t, st.DB.Table("job_outbox").Where("job_type='cache:invalidate'").Count(&outbox).Error)
	require.EqualValues(t, 1, outbox)
}
