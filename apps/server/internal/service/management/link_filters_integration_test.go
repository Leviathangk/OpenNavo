//go:build integration

package management

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/links"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

type linkFilterGithub struct {
	body  string
	calls int
}

func (g *linkFilterGithub) Get(_ context.Context, _ string, _ int64, _ http.Header) ([]byte, int, http.Header, error) {
	g.calls++
	return []byte(g.body), http.StatusOK, http.Header{}, nil
}

func TestLinkReleaseFiltersPersistPreviewSyncAndRejectStaleResults(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	svc := Service{Store: st}
	input := Input{Body: map[string]any{
		"token": "desktop-filter", "name": "Desktop Filter", "githubRepo": "owner/app", "pricing": "open_source",
		"githubTagRegex": `^v1\.0\.0-beta\.[0-9]+$`, "githubAssetPattern": "*.dmg", "includePrereleases": true,
	}}
	preview := input
	preview.Params = map[string]any{"dryrun": true}
	result, err := svc.Execute(ctx, "AddLinkPackage", preview)
	require.NoError(t, err)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"githubAssetPattern":"*.dmg"`)
	for _, table := range []string{"packages", "link_apps", "job_outbox", "audit_logs"} {
		var n int64
		require.NoError(t, st.DB.Table(table).Count(&n).Error)
		require.Zero(t, n, table)
	}
	result, err = svc.Execute(ctx, "AddLinkPackage", input)
	require.NoError(t, err)
	id := valueID(result.(map[string]any)["id"])
	row, err := st.LinkApp(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "*.dmg", *row.GithubAssetPattern)
	require.Equal(t, input.Body["githubTagRegex"], *row.GithubTagRegex)
	require.True(t, row.IncludePrereleases)
	github := &linkFilterGithub{body: `[
	 {"tag_name":"v1.1.8","published_at":"2026-10-28T07:52:42Z","html_url":"https://github.com/owner/app","assets":[{"name":"engine.tar.gz","browser_download_url":"https://example.com/engine.tar.gz"}]},
	 {"tag_name":"v1.0.0-beta.9","prerelease":true,"published_at":"2026-10-22T20:40:19Z","html_url":"https://github.com/owner/app","body":"App notes","assets":[{"name":"TensorFold-1.0.0-beta.9-arm64.dmg","browser_download_url":"https://example.com/app.dmg"}]}
	]`}
	sync := links.Service{Store: st, GitHub: github}
	stats, err := sync.Sync(ctx, id)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"checked": 1, "failed": 0}, stats)
	row, err = st.LinkApp(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "1.0.0-beta.9", row.Latest.Version)
	// The later sync uses the saved filters even though the engine is newer.
	github.body = `[{"tag_name":"v1.0.0-beta.10","prerelease":true,"published_at":"2026-10-25T19:41:46Z","html_url":"https://github.com/owner/app","assets":[{"name":"TensorFold-1.0.0-beta.10-arm64.dmg","browser_download_url":"https://example.com/app.dmg"}]}]`
	_, err = sync.Sync(ctx, id)
	require.NoError(t, err)
	row, err = st.LinkApp(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "1.0.0-beta.10", row.Latest.Version)
	require.NotNil(t, row.ETag)
	var versions int64
	require.NoError(t, st.DB.Table("package_versions").Where("package_id=?", id).Count(&versions).Error)
	require.Equal(t, int64(2), versions)
	before := *row
	edit := Input{ID: id, Body: map[string]any{"includePrereleases": false}, Params: map[string]any{"dryrun": true}}
	_, err = svc.Execute(ctx, "UpdateLinkPackage", edit)
	require.NoError(t, err)
	row, err = st.LinkApp(ctx, id)
	require.NoError(t, err)
	require.True(t, row.IncludePrereleases)
	require.NotNil(t, row.Latest)
	edit.Params = nil
	_, err = svc.Execute(ctx, "UpdateLinkPackage", edit)
	require.NoError(t, err)
	row, err = st.LinkApp(ctx, id)
	require.NoError(t, err)
	require.False(t, row.IncludePrereleases)
	require.Equal(t, before.GithubTagRegex, row.GithubTagRegex)
	require.Nil(t, row.Latest)
	require.Nil(t, row.ETag)
	// Neither a late successful response nor a late failure may overwrite new config.
	require.NoError(t, st.LinkSyncResult(ctx, before, before.Latest, "github_release", "late", "late", time.Now(), nil))
	require.NoError(t, st.LinkSyncResult(ctx, before, nil, "", "", "", time.Now(), links.ErrNoMatchingRelease))
	row, err = st.LinkApp(ctx, id)
	require.NoError(t, err)
	require.Nil(t, row.Latest)
	require.Nil(t, row.LastError)
	stats, err = sync.Sync(ctx, id)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"checked": 1, "failed": 1}, stats)
	row, err = st.LinkApp(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "github_release_no_match", *row.LastError)
	require.Nil(t, row.Latest)
	var version string
	require.NoError(t, st.DB.Raw("SELECT version FROM packages WHERE id=?", id).Scan(&version).Error)
	require.Equal(t, "1.0.0-beta.10", version)
	var count int64
	require.NoError(t, st.DB.Table("package_versions").Where("package_id=?", id).Count(&count).Error)
	require.Equal(t, versions, count)
	// Invalid patterns fail during dry-run as well as writes, without replacing config.
	for _, body := range []map[string]any{
		{"githubTagRegex": "(?=app)"}, {"githubAssetPattern": "["},
		{"githubTagPrefix": "app-"}, {"githubRepo": nil},
		{"includePrereleases": nil},
	} {
		for _, dry := range []bool{true, false} {
			_, err = svc.Execute(ctx, "UpdateLinkPackage", Input{ID: id, Body: body, Params: map[string]any{"dryrun": dry}})
			require.Error(t, err)
			var app *domain.AppError
			require.ErrorAs(t, err, &app)
			require.Equal(t, domain.CodeValidation, app.Code)
		}
	}
	// Switch from regex to prefix atomically, then clear all optional selection.
	_, err = svc.Execute(ctx, "UpdateLinkPackage", Input{ID: id, Body: map[string]any{"githubTagRegex": nil, "githubTagPrefix": "rapid-mac-v"}})
	require.NoError(t, err)
	row, err = st.LinkApp(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "rapid-mac-v", *row.GithubTagPrefix)
	require.Nil(t, row.GithubTagRegex)
	_, err = svc.Execute(ctx, "UpdateLinkPackage", Input{ID: id, Body: map[string]any{"githubTagPrefix": nil, "githubAssetPattern": nil, "includePrereleases": false}})
	require.NoError(t, err)
	row, err = st.LinkApp(ctx, id)
	require.NoError(t, err)
	require.False(t, row.HasGithubFilters())
	require.Equal(t, 3, github.calls)
	// Each selector participates in cache invalidation and in-flight source identity.
	for _, edit := range []map[string]any{
		{"githubTagPrefix": "app-"}, {"githubTagRegex": "^app-"},
		{"githubAssetPattern": "*.pkg"}, {"includePrereleases": true},
	} {
		_, err = svc.Execute(ctx, "UpdateLinkPackage", Input{ID: id, Body: map[string]any{
			"githubTagPrefix": nil, "githubTagRegex": nil, "githubAssetPattern": nil, "includePrereleases": false,
		}})
		require.NoError(t, err)
		old, err := st.LinkApp(ctx, id)
		require.NoError(t, err)
		require.NoError(t, st.LinkSyncResult(ctx, *old, before.Latest, "github_release", "", "cached", time.Now(), nil))
		old, err = st.LinkApp(ctx, id)
		require.NoError(t, err)
		require.NotNil(t, old.Latest)
		require.NotNil(t, old.ETag)
		_, err = svc.Execute(ctx, "UpdateLinkPackage", Input{ID: id, Body: edit})
		require.NoError(t, err)
		require.NoError(t, st.LinkSyncResult(ctx, *old, old.Latest, "github_release", "", "stale", time.Now(), nil))
		current, err := st.LinkApp(ctx, id)
		require.NoError(t, err)
		require.Nil(t, current.Latest, edit)
		require.Nil(t, current.ETag, edit)
	}
}
