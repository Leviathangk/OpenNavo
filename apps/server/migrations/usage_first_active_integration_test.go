//go:build integration

package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestUsageFirstActiveMigration(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 121)
	ctx := context.Background()
	db, err := st.DB.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	id := "00000000-0000-4000-8000-000000000001"
	at := time.Date(2026, 10, 29, 1, 0, 0, 0, time.UTC)
	require.NoError(t, st.DB.Exec(`INSERT INTO usage_identities(platform,anonymous_id,first_seen_at,last_seen_at) VALUES('web',?,?,?)`, id, at, at).Error)
	insert := `INSERT INTO usage_daily(day,platform,anonymous_id,last_active_at,version,locale) VALUES(?,'web',?,?,'','en-US')`
	require.NoError(t, st.DB.Exec(insert, "2026-10-29", id, at).Error)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 122))
	var first time.Time
	read := func(day string) time.Time {
		require.NoError(t, st.DB.Table("usage_daily").Where("day=?", day).Pluck("first_active_at", &first).Error)
		return first
	}
	require.True(t, read("2026-10-29").Equal(at))
	require.NoError(t, st.DB.Exec("UPDATE usage_daily SET last_active_at=?,first_active_at=?", at.Add(time.Hour), at.Add(time.Hour)).Error)
	require.True(t, read("2026-10-29").Equal(at))
	// An old server can still insert without knowing about the new column.
	require.NoError(t, st.DB.Exec(insert, "2026-10-30", id, at.Add(24*time.Hour)).Error)
	require.True(t, read("2026-10-30").Equal(at.Add(24*time.Hour)))
	require.NoError(t, goose.DownToContext(ctx, db, ".", 121))
	require.NoError(t, goose.UpToContext(ctx, db, ".", 122))
	require.True(t, read("2026-10-29").Equal(at.Add(time.Hour)))
}
