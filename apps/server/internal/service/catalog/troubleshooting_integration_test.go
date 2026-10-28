//go:build integration

package catalog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/urlnotify"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestTroubleshootingDetailSitemapAndNotificationsAgree(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en,installs_30d,rank_30d)
 VALUES(901,'cask','issue-fixture','issue-fixture','homebrew/cask','Issue Fixture','1','1','{}','fixture','Useful fixture application',50,1)`).Error)
	// Every language open for indexing (no list), so the issue rules alone decide until the list closes some.
	openLocales := func(list string) {
		t.Helper()
		require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}')||jsonb_build_object('openLocales',?::jsonb))`, list).Error)
	}
	openLocales("null")
	for _, locale := range seo.Locales {
		require.NoError(t, st.DB.Exec(`INSERT INTO package_i18n(package_id,locale,source_locale,summary,description,status,source_hash,source)
 VALUES(901,?,'en-US','Useful fixture application','Complete fixture description','machine',repeat('a',64),'rule')`, locale).Error)
		count := 3
		if locale == "de-DE" {
			count = 2
		}
		if locale == "ja-JP" {
			count = 0
		} // Public display falls back to English; indexing must not.
		for i := 0; i < count; i++ {
			issue := facts.Issue{Locale: locale, Title: fmt.Sprintf("Issue %d", i), Symptom: "Import stops", AppVersions: []string{"1"}, MacOSVersions: []string{"15.0"}, Steps: []string{"Restart and retry"}, CommunityLinks: []string{}}
			proof := facts.NewProof("https://vendor.example.test/help", now.Add(-time.Minute), time.Hour, "verified", now)
			switch locale {
			case "it-IT":
				proof.Status = "needs_review"
			case "pt-BR":
				proof = facts.NewProof(proof.SourceURL, now.Add(-2*time.Hour), time.Hour, "verified", now)
				proof.Status = "verified"
			case "zh-CN":
				issue.Steps = []string{" "}
			case "ru-RU":
				proof.ValidUntil = nil
			case "es-ES":
				proof.CheckedAt = nil
			case "zh-Hant":
				issue.Symptom = " "
			}
			value, err := json.Marshal(issue)
			require.NoError(t, err)
			evidence, err := json.Marshal(proof)
			require.NoError(t, err)
			require.NoError(t, st.DB.Exec(`INSERT INTO package_fact_records(package_id,kind,key,value,evidence) VALUES(901,'issue',?,?::jsonb,?::jsonb)`, fmt.Sprintf("%s-%d", locale, i), string(value), string(evidence)).Error)
		}
	}
	svc := &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}, Now: func() time.Time { return now }}
	sitemap, err := svc.Sitemap(ctx, "apps", 1, 100)
	require.NoError(t, err)
	require.Len(t, sitemap.Records, 1)
	for _, locale := range *sitemap.Records[0].Locales {
		detail, err := svc.Detail(ctx, "cask", "issue-fixture", string(locale.Locale))
		require.NoError(t, err)
		d := detail.Indexing.Pages.Troubleshooting
		require.NotNil(t, d)
		require.Equal(t, locale.Pages.Troubleshooting, d)
		admitted := locale.Locale == "en-US" || locale.Locale == "fr-FR"
		require.Equal(t, admitted, d.InSitemap)
		require.Equal(t, admitted, d.Announce)
		if admitted {
			require.Len(t, d.Hreflang, 2)
		}
		if locale.Locale == "ja-JP" {
			require.Len(t, *detail.Facts.Issues, 3)
			require.False(t, d.InSitemap)
		}
	}
	row, found, err := st.IndexRowByID(ctx, 901)
	require.NoError(t, err)
	require.True(t, found)
	policy, err := st.IndexPolicy(ctx)
	require.NoError(t, err)
	plan := urlnotify.BuildPlan([]store.URLChange{{PackageID: 901, Token: row.Token, Reason: "content"}}, map[int64]store.URLChange{901: {Token: row.Token}}, map[int64]store.IndexRow{901: row}, policy, "https://site.example.test")
	require.Contains(t, plan.IndexNowURLs, "https://site.example.test/apps/issue-fixture/troubleshooting")
	require.Contains(t, plan.IndexNowURLs, "https://site.example.test/fr/apps/issue-fixture/troubleshooting")
	require.NotContains(t, plan.IndexNowURLs, "https://site.example.test/ja/apps/issue-fixture/troubleshooting")

	// Closing French keeps its qualified page out of the index, the sitemap, IndexNow and English's hreflang.
	openLocales(`["en-US","zh-CN","ja-JP","ru-RU","es-ES"]`)
	uncached := &catalog.PublicService{Store: st, Now: func() time.Time { return now }}
	french, err := uncached.Detail(ctx, "cask", "issue-fixture", "fr-FR")
	require.NoError(t, err)
	require.Equal(t, seo.ReasonLocaleClosed, french.Indexing.Pages.Troubleshooting.Reason)
	require.False(t, french.Indexing.Pages.Troubleshooting.InSitemap)
	english, err := uncached.Detail(ctx, "cask", "issue-fixture", "en-US")
	require.NoError(t, err)
	require.Equal(t, []publicapi.Locale{"en-US"}, english.Indexing.Pages.Troubleshooting.Hreflang)
	sitemap, err = uncached.Sitemap(ctx, "apps", 1, 100)
	require.NoError(t, err)
	listed := []publicapi.Locale{}
	for _, locale := range *sitemap.Records[0].Locales {
		listed = append(listed, locale.Locale)
	}
	require.NotContains(t, listed, publicapi.Locale("fr-FR"))
	policy, err = st.IndexPolicy(ctx)
	require.NoError(t, err)
	plan = urlnotify.BuildPlan([]store.URLChange{{PackageID: 901, Token: row.Token, Reason: "content"}}, map[int64]store.URLChange{901: {Token: row.Token}}, map[int64]store.IndexRow{901: row}, policy, "https://site.example.test")
	require.Contains(t, plan.IndexNowURLs, "https://site.example.test/apps/issue-fixture/troubleshooting")
	require.NotContains(t, plan.IndexNowURLs, "https://site.example.test/fr/apps/issue-fixture/troubleshooting")
	openLocales("null")
	// Cached detail crosses an expiry boundary without a cache purge.
	now = now.Add(2 * time.Hour)
	detail, err := svc.Detail(ctx, "cask", "issue-fixture", "en-US")
	require.NoError(t, err)
	require.Empty(t, *detail.Facts.Issues)
	require.False(t, detail.Indexing.Pages.Troubleshooting.Announce)
	require.Equal(t, "/apps/issue-fixture", detail.Indexing.Pages.Troubleshooting.CanonicalPath)
}
