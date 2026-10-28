//go:build integration

package catalog_test

import (
	"context"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// Sitemap pages read the versions-page notes facts the page-state job stored and never recount releases: an app
// whose facts are missing (not reached yet, or reset by a migration) keeps its versions page out until they are stored.
func TestSitemapUsesStoredNotesFactsAndWaitsForMissingOnes(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	// Meet 72a38e4's popularity gate so this test continues to isolate stored notes readiness.
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en,installs_30d,rank_30d)
 VALUES(902,'cask','notes-sitemap','notes-sitemap','homebrew/cask','Notes Sitemap','3','3','{}','fixture','Useful fixture application',50,1);
 UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}','{"versionsSitemap":true}')`).Error)
	for _, locale := range seo.Locales {
		require.NoError(t, st.DB.Exec(`INSERT INTO package_i18n(package_id,locale,source_locale,summary,description,status,source_hash,source)
 VALUES(902,?,'en-US','Useful fixture application','Complete fixture description','machine',repeat('a',64),'rule')`, locale).Error)
	}
	for _, version := range []string{"1", "2", "3"} {
		require.NoError(t, st.DB.Exec(`INSERT INTO package_versions(package_id,version,version_base) VALUES(902,?,?)`, version, version).Error)
		require.NoError(t, st.DB.Exec(`INSERT INTO releases(package_id,source,source_key,version,body_markdown,source_url,source_locale)
 VALUES(902,'manual',?,?,?,'https://vendor.example/notes','en-US')`, version, version, strings.Repeat("Improved sync reliability for large libraries. ", 4)).Error)
	}
	svc := &catalog.PublicService{Store: st}
	versions := func() publicapi.PageDecision {
		t.Helper()
		page, err := svc.Sitemap(ctx, "apps", 1, 100)
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
		for _, locale := range *page.Records[0].Locales {
			if locale.Locale == "en-US" {
				return locale.Pages.Versions
			}
		}
		t.Fatal("English is not listed")
		return publicapi.PageDecision{}
	}
	missing := versions()
	require.Equal(t, "noindex, follow", string(missing.Robots), "no facts are stored before the job runs")
	require.False(t, missing.InSitemap)
	count, err := st.RefreshPackagePageStates(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	stored := versions()
	require.Equal(t, seo.IndexableRobots, string(stored.Robots), "English notes pass the thin-page rule")
	require.True(t, stored.InSitemap)
	require.True(t, stored.Announce)
	// With facts stored, the sitemap does not recount: blanking the notes changes nothing until the job runs.
	require.NoError(t, st.DB.Exec("UPDATE releases SET body_markdown='' WHERE package_id=902").Error)
	require.Equal(t, stored, versions())
	// Facts reset by a migration leave the page out instead of being counted live.
	require.NoError(t, st.DB.Exec("UPDATE package_page_state SET notes_entries=NULL WHERE package_id=902").Error)
	reset := versions()
	require.Equal(t, "noindex, follow", string(reset.Robots))
	require.False(t, reset.InSitemap)
	// The backfill stores the current facts, which see the blank notes.
	filled, err := st.BackfillPageNotes(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, filled)
	thin := versions()
	require.Equal(t, "noindex, follow", string(thin.Robots))
	require.Equal(t, "thin_notes_page", string(thin.Reason))
	require.False(t, thin.InSitemap)
	require.False(t, thin.Announce)
}

// Pages above SitemapPageSize are rejected rather than computed.
func TestSitemapRejectsPagesAboveTheLimit(t *testing.T) {
	svc := &catalog.PublicService{}
	_, err := svc.Sitemap(context.Background(), "apps", 1, catalog.SitemapPageSize+1)
	require.Error(t, err)
}
