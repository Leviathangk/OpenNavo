//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestGitHubReleaseSnapshotMigration(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 121)
	db, err := st.DB.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	ctx := context.Background()
	require.NoError(t, goose.UpToContext(ctx, db, ".", 123))
	version, err := goose.GetDBVersionContext(ctx, db)
	require.NoError(t, err)
	require.EqualValues(t, 123, version)
	insert := `INSERT INTO github_release_snapshots(day,release_tag,asset_id,asset_name,download_count,captured_at) VALUES(?::date,?,1,'asset',?,?::timestamptz)`
	require.NoError(t, st.DB.Exec(insert, "2026-10-30", "desktop-v0.1.3", 10, "2026-10-29T16:00:00Z").Error)
	require.Error(t, st.DB.Exec(insert, "2026-10-30", "desktop-v0.1.3", 11, "2026-10-29T17:00:00Z").Error, "day and asset form the primary key")
	require.Error(t, st.DB.Exec(insert, "2026-10-29", "desktop-v0.1.3", 10, "2026-10-29T16:00:00Z").Error, "day must be Beijing date")
	require.Error(t, st.DB.Exec(insert, "2026-10-31", "server-v1", 10, "2026-10-31T00:00:00Z").Error)
	require.Error(t, st.DB.Exec(insert, "2026-10-31", "desktop-v0.1.3", -1, "2026-10-31T00:00:00Z").Error)
	require.NoError(t, goose.DownContext(ctx, db, "."))
	require.False(t, st.DB.Migrator().HasTable("github_release_snapshots"))
	require.NoError(t, goose.UpToContext(ctx, db, ".", 123))
	require.True(t, st.DB.Migrator().HasTable("github_release_snapshots"))
}
