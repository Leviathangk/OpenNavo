//go:build integration

package seoingest

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/service/seoopportunities"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestRegistryAndInternalLinksAreIdempotentAndLayered(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	s := &Service{DB: st.DB, WebURL: "https://fixture.example"}
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(715,'cask','graph-fixture','graph-fixture','homebrew/cask','Graph Fixture','1.0','1.0','{}','fixture')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO collections(id,slug,status) VALUES(715,'graph-fixture','published')`).Error)
	require.NoError(t, s.SyncRegistry(ctx))
	var before, after string
	require.NoError(t, st.DB.Raw("SELECT string_agg(xmin::text,',' ORDER BY id) FROM seo_urls").Scan(&before).Error)
	require.NoError(t, s.SyncRegistry(ctx))
	require.NoError(t, st.DB.Raw("SELECT string_agg(xmin::text,',' ORDER BY id) FROM seo_urls").Scan(&after).Error)
	require.Equal(t, before, after, "unchanged catalog rows must not be rewritten")
	var page URL
	require.NoError(t, st.DB.Table("seo_urls").Where("url=?", s.WebURL+"/apps/graph-fixture").Take(&page).Error)
	now := time.Now().UTC()
	doc := Document{Status: 200, HTML: `<a href="/collections/graph-fixture#notes">collection</a><a href="/collections/graph-fixture">duplicate</a><a href="https://external.example/">external</a><a href="#top">self</a><a href="javascript:alert(1)">ignored</a>`}
	require.Equal(t, []string{s.WebURL + "/collections/graph-fixture"}, InternalLinks(page.URL, doc.HTML))
	require.NoError(t, s.recordLinks(ctx, page, "edge", doc, now))
	require.NoError(t, s.recordLinks(ctx, page, "origin", Document{Status: 200, HTML: "<a href='/apps/other'>other</a>"}, now))
	require.NoError(t, s.recordLinks(ctx, page, "edge", Document{Status: 200}, now.Add(-time.Minute)))
	require.NoError(t, s.recordLinks(ctx, page, "edge", Document{Status: 503}, now.Add(time.Minute)))
	var count int64
	require.NoError(t, st.DB.Table("seo_internal_links").Where("layer='edge'").Count(&count).Error)
	require.EqualValues(t, 1, count, "older or failed requests preserve last successful link snapshot")
	require.NoError(t, s.recordLinks(ctx, page, "edge", Document{Status: 200}, now.Add(time.Minute)))
	require.NoError(t, st.DB.Table("seo_internal_links").Where("layer='edge'").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, st.DB.Table("seo_internal_links").Where("layer='origin'").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestRemovedPackageExpectsGoneWithoutStatusAlert(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	s := &Service{DB: st.DB, WebURL: "https://fixture.example"}
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,removed_at) VALUES(716,'cask','removed','removed','homebrew/cask','Removed','1','1','{}','fixture',now())`).Error)
	id := int64(716)
	page := URL{URL: s.WebURL + "/apps/removed", PackageID: &id, PageKind: "overview", Locale: "en-US"}
	expected, err := s.ExpectedPage(ctx, page)
	require.NoError(t, err)
	require.Equal(t, 410, expected.Status)
	page.Expected = `{"status":410}`
	issues, err := CheckDocument(page, Document{Status: 410})
	require.NoError(t, err)
	for _, issue := range issues {
		require.NotEqual(t, "seo.status", issue.Rule)
	}
	require.NoError(t, st.DB.Exec("DELETE FROM packages WHERE id=716").Error)
	expected, err = s.ExpectedPage(ctx, page)
	require.NoError(t, err)
	require.Equal(t, 404, expected.Status)
}

func TestPackageLifecycleAuditExpectations(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	s := &Service{DB: st.DB, WebURL: "https://fixture.example"}
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(717,'cask','current-name','current-name','homebrew/cask','Fixture','1','1','{}','fixture')`).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO package_meta(package_id,hidden) VALUES(717,true)").Error)
	id := int64(717)
	stale := `{"status":200,"robots":"index, follow","canonical":"https://fixture.example/apps/old-name","hreflang":{"de":"https://fixture.example/de/apps/old-name"},"requiredText":["old content"]}`
	page := URL{URL: s.WebURL + "/apps/current-name", PackageID: &id, PageKind: "overview", Locale: "en-US", Expected: stale}
	expected, err := s.ExpectedPage(ctx, page)
	require.NoError(t, err)
	require.Equal(t, 200, expected.Status)
	require.Equal(t, "noindex, follow", expected.Robots)
	require.Empty(t, expected.Hreflang)
	require.Equal(t, page.URL, expected.Canonical)
	// Use the actual hidden-page directives, rather than copying the expectation.
	expected.RequiredText = nil
	raw, err := json.Marshal(expected)
	require.NoError(t, err)
	page.Expected = string(raw)
	issues, err := CheckDocument(page, Document{Status: 200, HTML: `<meta name="robots" content="noindex, follow"><link rel="canonical" href="https://fixture.example/apps/current-name">`})
	require.NoError(t, err)
	for _, issue := range issues {
		require.NotEqual(t, "p0", issue.Severity, issue.Rule)
	}

	require.NoError(t, st.DB.Exec("UPDATE packages SET removed_at=now() WHERE id=717").Error)
	for _, kind := range []string{"overview", "versions", "dependencies", "details", "alternatives", "uninstall"} {
		t.Run("removed/"+kind, func(t *testing.T) {
			page.PageKind, page.Expected = kind, stale
			page.URL = s.WebURL + "/apps/current-name"
			if kind != "overview" {
				page.URL += "/" + kind
			}
			expected, err := s.ExpectedPage(ctx, page)
			require.NoError(t, err)
			status := 410
			if kind == "alternatives" || kind == "uninstall" {
				status = 404
			}
			require.Equal(t, AuditExpectation{Status: status}, expected)
			raw, err := json.Marshal(expected)
			require.NoError(t, err)
			page.Expected = string(raw)
			issues, err := CheckDocument(page, Document{Status: status})
			require.NoError(t, err)
			for _, issue := range issues {
				require.NotEqual(t, "p0", issue.Severity, issue.Rule)
			}
		})
	}
}

func TestRenamedPackageAuditFollowsRedirectToCurrentCanonical(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(718,'cask','new-name','new-name','homebrew/cask','Fixture','1','1','{}','fixture')`).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO package_aliases(kind,token,package_id,source) VALUES('cask','old-name',718,'renamed')").Error)
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}'::jsonb)||'{"detailsRedirect":true}'::jsonb)`).Error)
	id := int64(718)
	for _, tc := range []struct{ kind, locale, oldPath, newPath, canonical string }{
		{"overview", "en-US", "/apps/old-name", "/apps/new-name", "/apps/new-name"},
		{"overview", "ja-JP", "/ja/apps/old-name", "/ja/apps/new-name", "/ja/apps/new-name"},
		{"versions", "ja-JP", "/ja/apps/old-name/versions?page=2&notes=1&original=1&utm_source=test", "/ja/apps/new-name/versions", "/ja/apps/new-name/versions"},
		{"details", "en-US", "/apps/old-name/details", "/apps/new-name", "/apps/new-name"},
	} {
		t.Run(tc.locale+"/"+tc.kind, func(t *testing.T) {
			var origin string
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.RequestURI() == tc.oldPath {
					target := tc.newPath
					if tc.kind == "details" {
						target = "/apps/new-name/details"
					}
					http.Redirect(w, r, target, http.StatusMovedPermanently)
					return
				}
				if tc.kind == "details" && r.URL.Path == "/apps/new-name/details" {
					http.Redirect(w, r, tc.newPath, http.StatusMovedPermanently)
					return
				}
				require.Equal(t, tc.newPath, r.URL.RequestURI())
				robots := "noindex, follow"
				if tc.kind == "overview" || tc.kind == "details" {
					robots = "index, follow"
				}
				fmt.Fprintf(w, `<meta name="robots" content="%s"><link rel="canonical" href="%s">`, robots, html.EscapeString(origin+tc.canonical))
			}))
			defer server.Close()
			origin = server.URL
			s := &Service{DB: st.DB, WebURL: origin}
			page := URL{URL: origin + tc.oldPath, PackageID: &id, PageKind: tc.kind, Locale: tc.locale}
			expected, err := s.ExpectedPage(ctx, page)
			require.NoError(t, err)
			require.Equal(t, origin+tc.canonical, expected.Canonical)
			raw, err := json.Marshal(expected)
			require.NoError(t, err)
			page.Expected = string(raw)
			public, err := url.Parse(origin)
			require.NoError(t, err)
			auditor := &httpAuditor{client: server.Client(), public: public, origin: public}
			doc, err := auditor.Fetch(ctx, page, "edge")
			require.NoError(t, err)
			require.Equal(t, 200, doc.Status)
			issues, err := CheckDocument(page, doc)
			require.NoError(t, err)
			for _, issue := range issues {
				require.NotEqual(t, "p0", issue.Severity, issue.Rule)
			}
		})
	}
}

func TestAuditSubpageExpiryPaginationFlagsAndBindings(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	now := time.Now()
	s := &Service{DB: st.DB, WebURL: "https://fixture.example", Now: func() time.Time { return now }}
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(719,'formula','same','same','homebrew/core','Formula','1','1','{}','fixture'),(720,'cask','same','same','homebrew/cask','App','1','1','{}','fixture')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_versions(package_id,version,version_base) SELECT 720,n::text,n::text FROM generate_series(1,25) n`).Error)
	page := URL{URL: s.WebURL + "/apps/same/versions?page=2", PageKind: "versions", Locale: "en-US"}
	id, err := s.registerURL(st.DB, page.URL)
	require.NoError(t, err)
	var bound int64
	require.NoError(t, st.DB.Raw("SELECT package_id FROM seo_urls WHERE id=?", id).Scan(&bound).Error)
	require.EqualValues(t, 720, bound)
	require.NoError(t, st.DB.Exec("UPDATE seo_urls SET package_id=719 WHERE id=?", id).Error)
	// ExpectedPage also resolves independently of stale registry bindings.
	wrong := int64(719)
	page.PackageID = &wrong
	d, err := s.ExpectedPage(ctx, page)
	require.NoError(t, err)
	require.Equal(t, 200, d.Status)
	require.NoError(t, s.SyncRegistry(ctx))
	require.NoError(t, st.DB.Raw("SELECT package_id FROM seo_urls WHERE id=?", id).Scan(&bound).Error)
	require.EqualValues(t, 720, bound)
	require.NoError(t, st.DB.Exec("DELETE FROM package_versions WHERE package_id=720 AND version='25'").Error)
	d, err = s.ExpectedPage(ctx, page)
	require.NoError(t, err)
	require.Equal(t, AuditExpectation{Status: 404}, d)
	require.NoError(t, st.DB.Exec(`INSERT INTO releases(package_id,source,source_key,version,source_locale,body_markdown,source_url) VALUES(720,'webpage','notes','24','en-US',repeat('Useful release notes ',40),'https://example.com/notes')`).Error)
	for _, query := range []string{"", "?notes=0", "?original=0", "?notes=0&original=0"} {
		page.URL = s.WebURL + "/apps/same/versions" + query
		d, err = s.ExpectedPage(ctx, page)
		require.NoError(t, err)
		require.Equal(t, 200, d.Status)
		require.Equal(t, "index, follow", d.Robots, query)
	}
	page.URL = s.WebURL + "/apps/same/versions?notes=1"
	d, err = s.ExpectedPage(ctx, page)
	require.NoError(t, err)
	require.Equal(t, "noindex, follow", d.Robots)
	// The public timeline groups and filters entries; raw version counts are insufficient.
	page.URL += "&page=2"
	d, err = s.ExpectedPage(ctx, page)
	require.NoError(t, err)
	require.Equal(t, 404, d.Status)
	for i := 0; i < 3; i++ {
		issue := facts.Issue{Locale: "en-US", Title: fmt.Sprintf("Issue %d", i), Symptom: "Import stops", AppVersions: []string{"1"}, MacOSVersions: []string{"15.0"}, Steps: []string{"Restart"}, CommunityLinks: []string{}}
		value, _ := json.Marshal(issue)
		proof, _ := json.Marshal(facts.NewProof("https://example.com/help", now.Add(-time.Minute), time.Hour, "verified", now))
		require.NoError(t, st.DB.Exec(`INSERT INTO package_fact_records(package_id,kind,key,value,evidence) VALUES(720,'issue',?,?::jsonb,?::jsonb)`, fmt.Sprint(i), string(value), string(proof)).Error)
	}
	page.URL, page.PageKind = s.WebURL+"/apps/same/troubleshooting", "troubleshooting"
	d, err = s.ExpectedPage(ctx, page)
	require.NoError(t, err)
	require.Equal(t, 200, d.Status)
	page.Locale, page.URL = "ja-JP", s.WebURL+"/ja/apps/same/troubleshooting"
	d, err = s.ExpectedPage(ctx, page)
	require.NoError(t, err)
	require.Equal(t, 200, d.Status, "English fallback keeps the page available")
	require.Equal(t, "noindex, follow", d.Robots)
	now = now.Add(2 * time.Hour)
	d, err = s.ExpectedPage(ctx, page)
	require.NoError(t, err)
	require.Equal(t, AuditExpectation{Status: 404}, d)
}

func TestVersionQueryAndCaseRedirectExpectations(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	s := &Service{DB: st.DB, WebURL: "https://fixture.example"}
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(721,'cask','ordinary-app','ordinary-app','homebrew/cask','App','25','25','{}','fixture')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_versions(package_id,version,version_base) SELECT 721,n::text,n::text FROM generate_series(1,25) n`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO releases(package_id,source,source_key,version,source_locale,body_markdown,source_url) VALUES(721,'webpage','notes','25','en-US',repeat('Useful release notes ',40),'https://example.com/notes')`).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO package_aliases(kind,token,package_id,source) VALUES('cask','old-app',721,'renamed')").Error)
	for _, tc := range versionQueryCases(t) {
		for _, token := range []string{"ordinary-app", "ORDINARY-APP"} {
			t.Run(token+"/"+tc.Query, func(t *testing.T) {
				page := URL{URL: s.WebURL + "/apps/" + token + "/versions?" + tc.Query, PageKind: "versions", Locale: "en-US"}
				d, err := s.ExpectedPage(ctx, page)
				require.NoError(t, err)
				require.Equal(t, tc.Status, d.Status)
				require.Equal(t, tc.Robots, d.Robots)
				if tc.Status == 200 {
					canonical := s.WebURL + "/apps/ordinary-app/versions"
					if tc.Canonical != "" {
						canonical += "?" + tc.Canonical
					}
					require.Equal(t, canonical, d.Canonical)
				}
			})
		}
	}
	for _, tc := range []struct {
		token     string
		status    int
		canonical string
	}{
		{"ORDINARY-APP", 404, ""},
		{"OLD-APP", 200, s.WebURL + "/apps/ordinary-app/versions"},
	} {
		d, err := s.ExpectedPage(ctx, URL{URL: s.WebURL + "/apps/" + tc.token + "/versions?page=999&notes=true", PageKind: "versions", Locale: "en-US"})
		require.NoError(t, err)
		require.Equal(t, tc.status, d.Status)
		require.Equal(t, tc.canonical, d.Canonical)
	}
	// GSC imports must bind case-normalized URLs to the same public app.
	id, err := s.registerURL(st.DB, s.WebURL+"/apps/ORDINARY-APP/versions?page=2")
	require.NoError(t, err)
	var bound int64
	require.NoError(t, st.DB.Raw("SELECT package_id FROM seo_urls WHERE id=?", id).Scan(&bound).Error)
	require.EqualValues(t, 721, bound)
}

func TestLegacyRegistryMetadataEscapesAndOriginalAlternates(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	s := &Service{DB: st.DB, WebURL: "https://fixture.example"}
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(722,'cask','ordinary-app','ordinary-app','homebrew/cask','App','1','1','{}','fixture')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO releases(package_id,source,source_key,version,source_locale,body_markdown,source_url) VALUES(722,'webpage','notes','1','en-US',repeat('Useful release notes ',40),'https://example.com/notes')`).Error)
	for _, tc := range packagePathCases(t) {
		t.Run(tc.Path, func(t *testing.T) {
			rawURL := s.WebURL + tc.Path
			require.NoError(t, st.DB.Exec("INSERT INTO seo_urls(url,page_kind,locale,package_id) VALUES(?,'VERSIONS','ru-RU',722)", rawURL).Error)
			var legacy URL
			require.NoError(t, st.DB.Table("seo_urls").Where("url=?", rawURL).Take(&legacy).Error)
			// Existing metadata cannot poison audit even before it is repaired.
			d, err := s.ExpectedPage(ctx, legacy)
			require.NoError(t, err)
			status := 200
			if !tc.Valid {
				status = 404
			}
			require.Equal(t, status, d.Status)
			id, err := s.registerURL(st.DB, rawURL)
			require.NoError(t, err)
			require.NoError(t, st.DB.Table("seo_urls").Where("id=?", id).Take(&legacy).Error)
			require.Equal(t, tc.Kind, legacy.PageKind)
			require.Equal(t, tc.Locale, legacy.Locale)
			if tc.Valid {
				require.NotNil(t, legacy.PackageID)
			} else {
				require.Nil(t, legacy.PackageID)
			}
			// Sync also repairs rows that are no longer returned by GSC imports.
			require.NoError(t, st.DB.Exec("UPDATE seo_urls SET page_kind='VERSIONS',locale='ru-RU' WHERE id=?", id).Error)
		})
	}
	require.NoError(t, s.SyncRegistry(ctx))
	for _, tc := range packagePathCases(t) {
		var entry URL
		require.NoError(t, st.DB.Table("seo_urls").Where("url=?", s.WebURL+tc.Path).Take(&entry).Error)
		require.Equal(t, tc.Kind, entry.PageKind)
		require.Equal(t, tc.Locale, entry.Locale)
	}
	for _, query := range []string{"original=1", "original=1&original=0", "original=true"} {
		d, err := s.ExpectedPage(ctx, URL{URL: s.WebURL + "/apps/ordinary-app/versions?" + query, PageKind: "VERSIONS", Locale: "ru-RU"})
		require.NoError(t, err)
		require.NotEmpty(t, d.Hreflang)
		for _, href := range d.Hreflang {
			parsed, err := url.Parse(href)
			require.NoError(t, err)
			want := ""
			if query == "original=1" {
				want = "original=1"
			}
			require.Equal(t, want, parsed.RawQuery)
		}
		// Checking a correct alternate must not produce seo.hreflang.
		raw, err := json.Marshal(d)
		require.NoError(t, err)
		body := fmt.Sprintf(`<meta name="robots" content="%s"><link rel="canonical" href="%s">`, d.Robots, html.EscapeString(d.Canonical))
		for lang, href := range d.Hreflang {
			body += fmt.Sprintf(`<link rel="alternate" hreflang="%s" href="%s">`, lang, html.EscapeString(href))
		}
		issues, err := CheckDocument(URL{Expected: string(raw)}, Document{Status: 200, HTML: body})
		require.NoError(t, err)
		for _, issue := range issues {
			require.NotEqual(t, "seo.hreflang", issue.Rule)
		}
	}
}

func TestLegacyEnglishRegistryRepairReplaysDemand(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	registry := &Service{DB: st.DB, WebURL: "https://fixture.example"}
	demand := &seoopportunities.Service{DB: st.DB}
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 VALUES(730,'cask','english-fixture','english-fixture','homebrew/cask','English Fixture','1','1','{}','fixture')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_urls(id,url,page_kind,locale) VALUES
 (730,'https://fixture.example/en/apps/english-fixture','en','en-US'),
 (731,'https://fixture.example/articles/baseline','article','en-US'),
 (732,'https://fixture.example/en/apps/english-fixture/versions?page=2','en','en-US')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_data_watermarks(source,complete_through) VALUES('gsc_page',current_date-3)
 ON CONFLICT(source) DO UPDATE SET complete_through=EXCLUDED.complete_through`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_ingest_days(source,day,temporal_complete,retrieval_complete,row_count,coverage,fetched_at)
 SELECT 'gsc_page',current_date-3-n,true,true,2,'fixture',now() FROM generate_series(0,27) n`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_gsc_page_daily(day,url_id,country,device,clicks,impressions,position_sum)
 VALUES(current_date-3,730,'usa','DESKTOP',0,2000,10000),
 (current_date-3,731,'usa','DESKTOP',200,2000,10000)`).Error)
	countSnippets := func(want int64) {
		t.Helper()
		var count int64
		require.NoError(t, st.DB.Table("seo_tasks").Where("kind='snippet_template'").Count(&count).Error)
		require.Equal(t, want, count)
	}

	tx := st.DB.Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()
	_, err := (&seoopportunities.Service{DB: tx}).Run(ctx)
	require.NoError(t, err)
	countSnippets(0)
	synced := make(chan error, 1)
	go func() { synced <- registry.SyncRegistry(ctx) }()
	require.Eventually(t, func() bool {
		var waiting bool
		err := st.DB.Raw("SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))").Scan(&waiting).Error
		return err == nil && waiting
	}, 5*time.Second, 10*time.Millisecond, "registry must wait for first analysis and its uncommitted cursors")
	require.NoError(t, tx.Commit().Error)
	require.NoError(t, <-synced)
	for id, kind := range map[int64]string{730: "overview", 732: "versions"} {
		var row URL
		require.NoError(t, st.DB.Table("seo_urls").Where("id=?", id).Take(&row).Error)
		require.Equal(t, kind, row.PageKind)
		require.Equal(t, "en-US", row.Locale)
		require.NotNil(t, row.PackageID)
		require.EqualValues(t, 730, *row.PackageID)
	}
	// No new GSC data or URL registration: the repair must replay cached demand.
	_, err = demand.Run(ctx)
	require.NoError(t, err)
	countSnippets(1)
	var entity string
	require.NoError(t, st.DB.Raw("SELECT entity_key FROM seo_tasks WHERE kind='snippet_template'").Scan(&entity).Error)
	require.Equal(t, "template:overview:en-US", entity)
	var before, after string
	const snapshot = `SELECT (SELECT string_agg(xmin::text,',' ORDER BY id) FROM seo_urls)
 || ':' || signature || ':' || xmin::text FROM seo_opportunity_cursors WHERE source='demand'`
	require.NoError(t, st.DB.Raw(snapshot).Scan(&before).Error)
	require.NoError(t, registry.SyncRegistry(ctx))
	require.NoError(t, st.DB.Raw(snapshot).Scan(&after).Error)
	require.Equal(t, before, after, "no-op sync must preserve metadata and demand cursor")
	_, err = demand.Run(ctx)
	require.NoError(t, err)
	countSnippets(1)
}

func TestRegistryUpsertReplaysMeasurementsAndPackageDemand(t *testing.T) {
	for _, path := range []string{"/ja/apps/binding-fixture", "/ja/APPS/binding-fixture"} {
		t.Run(path, func(t *testing.T) {
			ctx := context.Background()
			st := testutil.NewSEOStore(t)
			registry := &Service{DB: st.DB, WebURL: "https://fixture.example"}
			demand := &seoopportunities.Service{DB: st.DB}
			require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES
 (740,'formula','binding-fixture','binding-fixture','homebrew/core','Fixture','1','1','{}','fixture'),
 (741,'cask','binding-fixture','binding-fixture','homebrew/cask','Fixture','1','1','{}','fixture')`).Error)
			require.NoError(t, registry.SyncRegistry(ctx))
			require.NoError(t, st.DB.Exec("DELETE FROM seo_urls WHERE url=?", registry.WebURL+path).Error)
			require.NoError(t, st.DB.Exec(`INSERT INTO seo_urls(id,url,page_kind,locale,package_id) VALUES
 (740,?,'overview','en-US',740)`, registry.WebURL+path).Error)
			require.NoError(t, st.DB.Exec(`INSERT INTO seo_data_watermarks(source,complete_through)
 VALUES('gsc_page',current_date-3),('gsc_query',current_date-3)
 ON CONFLICT(source) DO UPDATE SET complete_through=EXCLUDED.complete_through`).Error)
			require.NoError(t, st.DB.Exec(`INSERT INTO seo_ingest_days(source,day,temporal_complete,retrieval_complete,row_count,coverage,fetched_at)
 SELECT source,current_date-3-n,true,true,1,'fixture',now()
 FROM (VALUES('gsc_page'),('gsc_query')) s(source) CROSS JOIN generate_series(0,27) n`).Error)
			require.NoError(t, st.DB.Exec(`INSERT INTO seo_gsc_page_daily(day,url_id,country,device,clicks,impressions,position_sum)
 VALUES(current_date-3,740,'jpn','DESKTOP',100,1000,5000)`).Error)
			require.NoError(t, st.DB.Exec(`INSERT INTO seo_gsc_query_daily(day,url_id,query,clicks,impressions,position_sum)
 VALUES(current_date-3,740,'fixture mac',100,1000,5000)`).Error)
			require.NoError(t, st.DB.Exec(`INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,priority,rule_version)
 VALUES('translate','pkg:741','ja-JP','fixture-translation',0,'fixture')`).Error)
			_, err := demand.Run(ctx)
			require.NoError(t, err)
			var keywords int64
			var priority float64
			require.NoError(t, st.DB.Table("seo_keywords").Where("language='ja-JP'").Count(&keywords).Error)
			require.Zero(t, keywords)
			require.NoError(t, st.DB.Raw("SELECT priority FROM seo_tasks WHERE dedupe_key='fixture-translation'").Scan(&priority).Error)
			require.Zero(t, priority)
			require.NoError(t, registry.SyncRegistry(ctx))
			_, err = demand.Run(ctx)
			require.NoError(t, err)
			require.NoError(t, st.DB.Table("seo_keywords").Where("language='ja-JP'").Count(&keywords).Error)
			require.EqualValues(t, 1, keywords)
			require.NoError(t, st.DB.Raw("SELECT priority FROM seo_tasks WHERE dedupe_key='fixture-translation'").Scan(&priority).Error)
			require.InDelta(t, 26.90875, priority, 0.0001)

		})
	}
}
