//go:build integration

package management

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/snapshot"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestLinksCRUDDryRunCapabilitiesExclusionsAndAdoption(t *testing.T) {
	ctx := context.Background()
	caps := domain.WithLinks(ctx, true)
	st := testutil.NewStore(t)
	svc := &Service{Store: st}
	input := Input{Body: map[string]any{"token": "link-example", "name": "Example Link App", "homepage": "https://example.com", "githubRepo": "owner/example", "pricing": "free", "bundleIds": []string{"com.example.App"}}}
	preview := input
	preview.Params = map[string]any{"dryrun": true}
	result, err := svc.Execute(ctx, "AddLinkPackage", preview)
	require.NoError(t, err)
	require.Equal(t, true, result.(map[string]any)["dryRun"])
	var count int64
	require.NoError(t, st.DB.Table("packages").Count(&count).Error)
	require.Zero(t, count)
	result, err = svc.Execute(ctx, "AddLinkPackage", input)
	require.NoError(t, err)
	id := valueID(result.(map[string]any)["id"])
	_, err = svc.Execute(ctx, "AddLinkPackage", input)
	require.Error(t, err)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_meta(package_id,developer,tags,editor_choice) VALUES(?,'Original developer',ARRAY['curated'],true)`, id).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_i18n(package_id,locale,source_locale,source,status,display_name,summary) VALUES(?,'en-US','en-US','human','manual','Curated name','Curated summary')`, id).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO categories(id,slug,icon,source_locale) VALUES(1,'link-category','lucide:box','en-US');INSERT INTO category_i18n(category_id,locale,name,source_locale,status) VALUES(1,'en-US','Links','en-US','source');INSERT INTO collections(id,slug,status,source_locale) VALUES(1,'links','published','en-US');INSERT INTO collection_i18n(collection_id,locale,title,source_locale,status) VALUES(1,'en-US','Links','en-US','source')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_categories(package_id,category_id,is_primary,source) VALUES(?,1,true,'human')`, id).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO collection_items(collection_id,package_id,source_locale) VALUES(1,?,'en-US')`, id).Error)
	public := &catalog.PublicService{Store: st}
	sitemap, err := public.Sitemap(ctx, "apps", 1, 100)
	require.NoError(t, err)
	require.Len(t, sitemap.Records, 1)
	require.Equal(t, "link", string(sitemap.Records[0].Kind))
	f := store.PublicFilter{Current: 1, Size: 100, Locale: "en-US", IncludeFonts: true, IncludeLibraries: true}
	_, total, err := st.PublicPackages(ctx, f)
	require.NoError(t, err)
	require.Zero(t, total)
	rows, total, err := st.PublicPackages(caps, f)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.NotNil(t, rows[0].Link)
	require.Equal(t, []string{"com.example.App"}, *rows[0].BundleIDs)
	_, n, err := st.Search(ctx, domain.SearchInput{Current: 1, Size: 10}, []string{"example"}, 1, 1)
	require.NoError(t, err)
	require.Zero(t, n)
	_, n, err = st.Search(caps, domain.SearchInput{Current: 1, Size: 10}, []string{"example"}, 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	categories, err := st.PublicCategories(ctx, "en-US")
	require.NoError(t, err)
	require.Len(t, categories, 1)
	require.Zero(t, categories[0].PackageCount)
	categories, err = st.PublicCategories(caps, "en-US")
	require.NoError(t, err)
	require.Len(t, categories, 1)
	require.Equal(t, 1, categories[0].PackageCount)
	collection, err := public.Collection(ctx, "links", "en-US")
	require.NoError(t, err)
	require.Empty(t, collection.Items)
	collection, err = public.Collection(caps, "links", "en-US")
	require.NoError(t, err)
	require.Len(t, collection.Items, 1)
	brewfile, err := public.Brewfile(caps, "links")
	require.NoError(t, err)
	require.Empty(t, brewfile)
	detail, err := public.Detail(ctx, "link", "link-example", "en-US")
	require.NoError(t, err)
	require.NotNil(t, detail.Link)
	require.Empty(t, detail.InstallCommand)
	links, err := (&snapshot.Service{Store: st}).Links(ctx)
	require.NoError(t, err)
	require.Len(t, links.Items, 1)
	firstTag := links.ETag
	updated := Input{ID: id, Body: map[string]any{"downloadUrl": "https://example.com/download"}, Params: map[string]any{"dryrun": true}}
	_, err = svc.Execute(ctx, "UpdateLinkPackage", updated)
	require.NoError(t, err)
	row, err := st.LinkApp(ctx, id)
	require.NoError(t, err)
	require.Nil(t, row.DownloadURL)
	updated.Params = nil
	_, err = svc.Execute(ctx, "UpdateLinkPackage", updated)
	require.NoError(t, err)
	links, err = (&snapshot.Service{Store: st}).Links(ctx)
	require.NoError(t, err)
	require.NotEqual(t, firstTag, links.ETag)
	// Each Homebrew-only pipeline is exercised with an otherwise plausible link.
	require.NoError(t, st.DB.Exec(`UPDATE packages SET version='1',version_base='1',version_changed_at=now()-interval '5 years',raw='{"token":"link-example"}',ruby_source_path='Casks/l/link-example.rb',tap='homebrew/cask' WHERE id=?`, id).Error)
	analytics, err := st.AnalyticsPackages(ctx)
	require.NoError(t, err)
	require.Empty(t, analytics)
	require.NoError(t, st.ApplyAnalytics(ctx, []domain.AnalyticsRow{{ID: id, Installs30d: 99}}, "2026-10-16"))
	var installs int
	require.NoError(t, st.DB.Raw("SELECT installs_30d FROM packages WHERE id=?", id).Scan(&installs).Error)
	require.Zero(t, installs)
	_, err = st.RefreshStalePackages(ctx, time.Now().AddDate(10, 0, 0))
	require.NoError(t, err)
	var stale bool
	require.NoError(t, st.DB.Raw("SELECT stale_disabled FROM packages WHERE id=?", id).Scan(&stale).Error)
	require.False(t, stale)
	unresolved, err := st.UnresolvedHomebrewSources(ctx)
	require.NoError(t, err)
	require.Empty(t, unresolved)
	_, found := changelog.HomebrewSource(changelog.Candidate{Kind: "link", Tap: "homebrew/cask", RubySourcePath: "Casks/l/link-example.rb"})
	require.False(t, found)
	_, err = st.DownloadSizePackage(caps, id)
	require.Error(t, err)
	scheduled, err := st.ScheduleDownloadSizes(ctx)
	require.NoError(t, err)
	require.Zero(t, scheduled)
	visited := 0
	require.NoError(t, st.VisitOutdatedCasks(ctx, "old", func(json.RawMessage) error { visited++; return nil }))
	require.Zero(t, visited)
	facts, err := st.SourceFactPackages(ctx)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.Equal(t, "https://github.com/owner/example", *facts[0].RepoURL)
	_, found, err = st.IndexRowByToken(ctx, "link-example")
	require.NoError(t, err)
	require.True(t, found)
	// Sync writes version history and release metadata without creating brew jobs.
	before, err := st.LinkApp(ctx, id)
	require.NoError(t, err)
	release := &domain.GithubRelease{Tag: "v2", Version: "2", PublishedAt: time.Now().UTC(), URL: "https://github.com/owner/example/releases/tag/v2", Assets: []domain.ReleaseAsset{}}
	assertReleaseInvalidation := func() {
		t.Helper()
		var payload string
		require.NoError(t, st.DB.Raw("SELECT payload::text FROM job_outbox WHERE unique_key='cache:invalidate:link-releases:link-example'").Scan(&payload).Error)
		require.JSONEq(t, `["c:rel:*:link:link-example:*"]`, payload)
		require.NoError(t, st.DB.Exec("DELETE FROM job_outbox WHERE unique_key='cache:invalidate:link-releases:link-example'").Error)
	}
	require.NoError(t, st.LinkSyncResult(ctx, *before, release, "github_release", "Notes", `"v2"`, time.Now(), nil))
	require.NoError(t, st.DB.Table("package_versions").Where("package_id=?", id).Count(&count).Error)
	require.Equal(t, int64(1), count)
	require.NoError(t, st.DB.Table("releases").Where("package_id=?", id).Count(&count).Error)
	require.Equal(t, int64(1), count)
	assertReleaseInvalidation()
	// Editing notes on the same upstream release must refresh history as well.
	require.NoError(t, st.LinkSyncResult(ctx, *before, release, "github_release", "Revised notes", `"v2-edit"`, time.Now(), nil))
	var notes string
	require.NoError(t, st.DB.Raw("SELECT body_markdown FROM releases WHERE package_id=?", id).Scan(&notes).Error)
	require.Equal(t, "Revised notes", notes)
	assertReleaseInvalidation()
	// Official adoption preserves identity, translations, categories and editor metadata.
	cask, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"link-example","name":["Upstream name"],"version":"3","desc":"Upstream summary","artifacts":[{"app":["Example.app"]}]}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: cask}}))
	owner, err := st.CatalogRow(ctx, "cask", "link-example")
	require.NoError(t, err)
	require.Equal(t, id, owner.ID)
	sitemap, err = public.Sitemap(ctx, "apps", 1, 100)
	require.NoError(t, err)
	require.Len(t, sitemap.Records, 1)
	require.Equal(t, "cask", string(sitemap.Records[0].Kind))
	row, err = st.LinkApp(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, row.AdoptedByCaskAt)
	var developer, summary string
	require.NoError(t, st.DB.Raw("SELECT developer FROM package_meta WHERE package_id=?", id).Scan(&developer).Error)
	require.Equal(t, "Original developer", developer)
	require.NoError(t, st.DB.Raw("SELECT summary FROM package_i18n WHERE package_id=? AND locale='en-US'", id).Scan(&summary).Error)
	require.Equal(t, "Curated summary", summary)
	links, err = (&snapshot.Service{Store: st}).Links(ctx)
	require.NoError(t, err)
	require.Empty(t, links.Items)
	_, err = svc.Execute(ctx, "RemoveLinkPackage", Input{ID: id})
	require.Error(t, err)
	// A late pre-adoption response must never turn the cask back into a link.
	require.NoError(t, st.LinkSyncResult(ctx, *before, release, "github_release", "old", `"old"`, time.Now(), nil))
	owner, err = st.CatalogRow(ctx, "cask", "link-example")
	require.NoError(t, err)
	require.Equal(t, "3", owner.Version)
	// Deleting an active link supports preview, hides it from the public feed and
	// prevents an already fetched response from restoring it.
	input.Body["token"] = "link-removable"
	result, err = svc.Execute(ctx, "AddLinkPackage", input)
	require.NoError(t, err)
	removeID := valueID(result.(map[string]any)["id"])
	removable, err := st.LinkApp(ctx, removeID)
	require.NoError(t, err)
	_, err = svc.Execute(ctx, "RemoveLinkPackage", Input{ID: removeID, Params: map[string]any{"dryrun": true}})
	require.NoError(t, err)
	links, err = (&snapshot.Service{Store: st}).Links(ctx)
	require.NoError(t, err)
	require.Len(t, links.Items, 1)
	_, err = svc.Execute(ctx, "RemoveLinkPackage", Input{ID: removeID})
	require.NoError(t, err)
	require.NoError(t, st.LinkSyncResult(ctx, *removable, release, "github_release", "late", `"late"`, time.Now(), nil))
	links, err = (&snapshot.Service{Store: st}).Links(ctx)
	require.NoError(t, err)
	require.Empty(t, links.Items)
}

func TestLinkIndexOverrideInvalidatesLinkDetails(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	svc := &Service{Store: st}
	result, err := svc.Execute(ctx, "AddLinkPackage", Input{Body: map[string]any{
		"token": "link-seo", "name": "Link SEO", "homepage": "https://example.com",
		"githubRepo": "owner/example", "pricing": "free",
	}})
	require.NoError(t, err)
	id := valueID(result.(map[string]any)["id"])
	_, err = svc.packageIndexStatus(ctx, "SetPackageIndexStatus", Input{ID: id, Body: map[string]any{
		"override": "noindex", "reason": "Duplicate",
	}})
	require.NoError(t, err)
	var payload string
	require.NoError(t, st.DB.Raw("SELECT payload::text FROM job_outbox WHERE unique_key='cache:invalidate:seo-index:link-seo'").Scan(&payload).Error)
	require.JSONEq(t, `["c:pkg:c5:link:link-seo:*"]`, payload)
}
