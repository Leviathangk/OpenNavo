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

func TestDisableDeadTelegramBetaSourceMigration(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 130)
	db, err := st.DB.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES
 (1,'cask','telegram@beta','telegram@beta','','Telegram Beta','13.2.0','13.2.0','{}','beta'),
 (2,'cask','telegram','telegram','','Telegram','12.6','12.6','{}','stable'),
 (3,'cask','editor-app','editor-app','','Editor','1','1','{}','editor'),
 (4,'cask','live-app','live-app','','Live','1','1','{}','live')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by) VALUES
 (1,'github_releases','{"repo":"overtake/TelegramSwift"}',20,'auto'),
 (1,'homebrew_commits','{"repo":"Homebrew/homebrew-cask"}',90,'auto'),
 (2,'github_releases','{"repo":"overtake/TelegramSwift"}',20,'auto'),
 (3,'github_releases','{"repo":"overtake/TelegramSwift"}',20,'manual'),
 (4,'github_releases','{"repo":"owner/live"}',20,'auto')`).Error)

	require.NoError(t, goose.UpToContext(ctx, db, ".", 131))
	assertSourceEnabled := func(packageID int64, sourceType string, enabled bool, lastError *string) {
		t.Helper()
		var row struct {
			Enabled   bool
			LastError *string
		}
		require.NoError(t, st.DB.Raw("SELECT enabled, last_error FROM changelog_sources WHERE package_id=? AND type=?", packageID, sourceType).Scan(&row).Error)
		require.Equal(t, enabled, row.Enabled)
		require.Equal(t, lastError, row.LastError)
	}
	gone := "github repository overtake/TelegramSwift is gone"
	assertSourceEnabled(1, "github_releases", false, &gone)
	assertSourceEnabled(1, "homebrew_commits", true, nil)
	assertSourceEnabled(2, "github_releases", true, nil)
	assertSourceEnabled(3, "github_releases", true, nil)
	assertSourceEnabled(4, "github_releases", true, nil)

	require.NoError(t, goose.DownToContext(ctx, db, ".", 130))
	assertSourceEnabled(1, "github_releases", false, &gone)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 131))
	assertSourceEnabled(1, "github_releases", false, &gone)
}
