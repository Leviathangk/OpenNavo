//go:build integration

package management

import (
	"context"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/service/seoingest"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestSEOPageDistinguishesUnknownFromObservedLinkCounts(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	s := &Service{Store: st, Config: config.Config{WebBaseURL: "https://fixture.example"}}
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_urls(id,url,page_kind,locale) VALUES(716,'https://fixture.example/collections/fixture','collection','en-US'),(717,'https://fixture.example/','home','en-US')`).Error)
	in := Input{Params: map[string]any{"url": "https://fixture.example/collections/fixture"}}
	value, err := s.seoPage(ctx, in)
	require.NoError(t, err)
	view := value.(map[string]any)
	require.Nil(t, view["indexing"], "no fabricated page policy for a frontend-owned route")
	require.Nil(t, view["inboundLinks"])
	require.Nil(t, view["outboundLinks"])
	require.Equal(t, "unknown", view["linkCoverage"])
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_link_crawls(url_id,layer,checked_at) VALUES(716,'edge',now()),(717,'edge',now())`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_internal_links(from_url_id,target_url,layer) VALUES(717,'https://fixture.example/collections/fixture','edge')`).Error)
	value, err = s.seoPage(ctx, in)
	require.NoError(t, err)
	view = value.(map[string]any)
	require.EqualValues(t, 1, view["inboundLinks"])
	require.EqualValues(t, 0, view["outboundLinks"])
	require.Equal(t, "sampled_html", view["linkCoverage"])
}

func TestSEOPageAndAuditUsePageSpecificEligibility(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	const site = "https://fixture.example"
	s := &Service{Store: st, Config: config.Config{WebBaseURL: site}}
	ingest := &seoingest.Service{DB: st.DB, WebURL: site}
	const id int64 = 718
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,desc_en,source_locale,version,version_base,raw,raw_hash,artifacts)
 VALUES(718,'cask','policy-fixture','policy-fixture','homebrew/cask','Policy Fixture','A useful macOS application','en-US','1.0','1.0','{}','fixture','{"entries":[{"phase":"uninstall","declaration":{"uninstall":[{"pkgutil":"com.example.fixture"}]}}]}')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO releases(package_id,source,source_key,version,source_locale,body_markdown,source_url)
 VALUES(718,'manual','policy-fixture','1.0','en-US',?,'https://vendor.example/notes')`, strings.Repeat("Useful macOS release notes. ", 30)).Error)
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}')||'{"intentPagesEnabled":true,"versionsSitemap":true}'::jsonb) WHERE id=true`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_keywords(id,keyword,normalized,source,engine,country,language,device,period,search_volume,volume_state,observed_at)
 VALUES(718,'policy fixture uninstall','policy fixture uninstall','site_search','site','US','en-US','all','2026-10',10,'known',now())`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_keyword_links(keyword_id,package_id,intent,confidence,state) VALUES(718,718,'uninstall',1,'confirmed')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_targets(entity_key,intent,locale,target_url) VALUES('pkg:718','uninstall','en-US',?)`, site+"/apps/policy-fixture/uninstall").Error)
	check := func(path, kind, robots, reason string, sitemap bool, status int) {
		t.Helper()
		value, err := s.seoPage(ctx, Input{Params: map[string]any{"url": site + path}})
		require.NoError(t, err)
		decision := value.(map[string]any)["indexing"].(seo.PageDecision)
		require.Equal(t, robots, decision.Robots, path)
		require.Equal(t, reason, decision.Reason, path)
		require.Equal(t, sitemap, decision.InSitemap, path)
		packageID := id
		expected, err := ingest.ExpectedPage(ctx, seoingest.URL{URL: site + path, PackageID: &packageID, Locale: "en-US", PageKind: kind})
		require.NoError(t, err)
		require.Equal(t, status, expected.Status, path)
		if status == 404 {
			require.Empty(t, expected.Robots, path)
			require.Empty(t, expected.Canonical, path)
			return
		}
		require.Equal(t, decision.Robots, expected.Robots, path)
		if decision.CanonicalPath != "" {
			require.Equal(t, site+decision.CanonicalPath, expected.Canonical, path)
		}
	}
	check("/apps/policy-fixture/versions", "versions", "index, follow", "", true, 200)
	check("/apps/policy-fixture/versions?page=2", "versions", "noindex, follow", "thin_notes_page", false, 404)
	check("/apps/policy-fixture/uninstall", "uninstall", "index, follow", "", true, 200)
	require.NoError(t, st.DB.Exec(`UPDATE seo_keyword_links SET state='candidate' WHERE keyword_id=718`).Error)
	check("/apps/policy-fixture/uninstall", "uninstall", "noindex, follow", "intent_not_eligible", false, 404)
}
