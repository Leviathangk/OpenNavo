//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestLinkGithubReleaseFiltersMigration(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 120)
	db, err := st.DB.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 VALUES(1,'link','migration-app','migration-app','','Migration App','1','1','{}','fixture');
 INSERT INTO link_apps(package_id,homepage,github_repo,pricing,latest,etag)
 VALUES(1,'https://example.com','owner/app','free','{"version":"1"}','cached')`).Error)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 121))
	var defaults bool
	require.NoError(t, st.DB.Raw(`SELECT NOT include_prereleases AND github_tag_prefix IS NULL AND github_tag_regex IS NULL AND github_asset_pattern IS NULL AND latest->>'version'='1' AND etag='cached' FROM link_apps WHERE package_id=1`).Scan(&defaults).Error)
	require.True(t, defaults)
	require.NoError(t, st.DB.Exec(`UPDATE link_apps SET github_tag_prefix='app-',github_asset_pattern='*.dmg',include_prereleases=true WHERE package_id=1`).Error)
	require.Error(t, st.DB.Exec(`UPDATE link_apps SET github_tag_regex='^app-' WHERE package_id=1`).Error)
	require.Error(t, st.DB.Exec(`UPDATE link_apps SET github_repo=NULL WHERE package_id=1`).Error)
	for _, field := range []struct {
		column string
		limit  int
	}{
		{"github_tag_prefix", 256}, {"github_tag_regex", 1024}, {"github_asset_pattern", 512},
	} {
		// Column names above are fixed test constants; values stay parameterized.
		require.NoError(t, st.DB.Exec("UPDATE link_apps SET github_tag_prefix=NULL,github_tag_regex=NULL,github_asset_pattern=NULL WHERE package_id=1").Error)
		require.Error(t, st.DB.Exec("UPDATE link_apps SET "+field.column+"=? WHERE package_id=1", strings.Repeat("x", field.limit+1)).Error)
	}
	require.NoError(t, goose.DownToContext(ctx, db, ".", 120))
	var version string
	require.NoError(t, st.DB.Raw("SELECT version FROM packages WHERE id=1").Scan(&version).Error)
	require.Equal(t, "1", version)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 121))
}
