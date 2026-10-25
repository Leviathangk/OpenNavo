//go:build integration

package management

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/service/seoingest"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestSEOPageAndAuditKeepClosedLocalesAndDependencyTabsNoindex(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	const site = "https://fixture.example"
	s := &Service{Store: st, Config: config.Config{WebBaseURL: site}}
	ingest := &seoingest.Service{DB: st.DB, WebURL: site}
	const id int64 = 719
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,desc_en,source_locale,version,version_base,raw,raw_hash,artifacts,dependencies)
 VALUES(719,'cask','locale-fixture','locale-fixture','homebrew/cask','Locale Fixture','A useful macOS application','en-US','1.0','1.0','{}','fixture',
 '{"entries":[{"phase":"uninstall","declaration":{"uninstall":[{"pkgutil":"com.example.locale"}]}}]}','{"dependsOn":{"cask":["helper-fixture"]}}')`).Error)
	languages, err := json.Marshal(seo.Locales)
	require.NoError(t, err)
	// Ready text in every site language, so only the open list keeps a language out.
	require.NoError(t, st.DB.Exec(`INSERT INTO package_i18n(package_id,locale,display_name,summary,description,status,source,source_locale,source_hash)
 SELECT 719,l.locale,'Locale Fixture','A useful macOS application','Longer description','machine','llm','en-US',repeat('a',64)
 FROM jsonb_array_elements_text(?::jsonb) l(locale) WHERE l.locale<>'en-US'`, string(languages)).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO releases(package_id,source,source_key,version,source_locale,body_markdown,source_url)
 SELECT 719,'manual','locale-fixture-'||n,n::text,'en-US',?,'https://vendor.example/notes' FROM generate_series(1,3) n`, strings.Repeat("Useful macOS release notes. ", 30)).Error)
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}')||'{"intentPagesEnabled":true,"versionsSitemap":true,"openLocales":["en-US","zh-CN","ja-JP","ru-RU","es-ES"]}'::jsonb) WHERE id=true`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_keywords(id,keyword,normalized,source,engine,country,language,device,period,search_volume,volume_state,observed_at)
 VALUES(719,'locale fixture uninstall','locale fixture uninstall','site_search','site','US','en-US','all','2026-10',10,'known',now())`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_keyword_links(keyword_id,package_id,intent,confidence,state) VALUES(719,719,'uninstall',1,'confirmed')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_targets(entity_key,intent,locale,target_url) VALUES('pkg:719','uninstall','en-US',?),('pkg:719','uninstall','de-DE',?)`,
		site+"/apps/locale-fixture/uninstall", site+"/de/apps/locale-fixture/uninstall").Error)
	open := []string{"en-US", "zh-CN", "ja-JP", "es-ES", "ru-RU"}
	check := func(path, kind, locale, robots, reason, canonical string, hreflang []string, status int) {
		t.Helper()
		value, err := s.seoPage(ctx, Input{Params: map[string]any{"url": site + path}})
		require.NoError(t, err)
		decision := value.(map[string]any)["indexing"].(seo.PageDecision)
		require.Equal(t, robots, decision.Robots, path)
		require.Equal(t, reason, decision.Reason, path)
		require.Equal(t, hreflang, decision.Hreflang, path)
		require.Equal(t, robots == "index, follow", decision.Announce, path)
		packageID := id
		expected, err := ingest.ExpectedPage(ctx, seoingest.URL{URL: site + path, PackageID: &packageID, Locale: locale, PageKind: kind})
		require.NoError(t, err)
		require.Equal(t, status, expected.Status, path)
		if status == 404 {
			return
		}
		require.Equal(t, robots, expected.Robots, path)
		require.Equal(t, site+canonical, expected.Canonical, path)
		// The audit expects the decision's alternates (with zh-Hant's aliases) and none of the closed languages.
		alternates := 0
		for _, locale := range hreflang {
			alternates += 1 + len(i18n.Metadata[i18n.LocaleCode(locale)].HreflangAliases)
		}
		require.Len(t, expected.Hreflang, alternates, path)
		for _, locale := range seo.Locales {
			if prefix := i18n.Metadata[i18n.LocaleCode(locale)].Prefix; prefix != "" && !slices.Contains(hreflang, locale) {
				for _, href := range expected.Hreflang {
					require.NotContains(t, href, prefix+"/", path)
				}
			}
		}
	}
	check("/apps/locale-fixture", "overview", "en-US", "index, follow", "", "/apps/locale-fixture", open, 200)
	check("/ja/apps/locale-fixture", "overview", "ja-JP", "index, follow", "", "/ja/apps/locale-fixture", open, 200)
	check("/apps/locale-fixture/uninstall", "uninstall", "en-US", "index, follow", "", "/apps/locale-fixture/uninstall", []string{"en-US"}, 200)
	for _, page := range []struct{ path, kind, locale string }{
		{"/de/apps/locale-fixture", "overview", "de-DE"},
		{"/pt/apps/locale-fixture", "overview", "pt-BR"},
		{"/zh-hant/apps/locale-fixture", "overview", "zh-Hant"},
		{"/fr/apps/locale-fixture/versions", "versions", "fr-FR"},
	} {
		check(page.path, page.kind, page.locale, "noindex, follow", seo.ReasonLocaleClosed, page.path, []string{}, 200)
	}
	// An intent page opens only where indexable, so a closed language answers 404 even with a target.
	check("/de/apps/locale-fixture/uninstall", "uninstall", "de-DE", "noindex, follow", seo.ReasonLocaleClosed, "", []string{}, 404)
	// Dependency tabs keep their own canonical in every language and are never indexed.
	check("/apps/locale-fixture/dependencies", "dependencies", "en-US", "noindex, follow", seo.ReasonSupportingPage, "/apps/locale-fixture/dependencies", []string{}, 200)
	check("/it/apps/locale-fixture/dependencies", "dependencies", "it-IT", "noindex, follow", seo.ReasonSupportingPage, "/it/apps/locale-fixture/dependencies", []string{}, 200)

	// Reopening every language (no list) restores today's decisions.
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{pages,openLocales}','null'::jsonb) WHERE id=true`).Error)
	check("/de/apps/locale-fixture", "overview", "de-DE", "index, follow", "", "/de/apps/locale-fixture", seo.Locales, 200)
}
