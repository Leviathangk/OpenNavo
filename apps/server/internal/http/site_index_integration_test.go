//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The site indexing endpoints that replace the in-server SEO automation, called as the admin console
// does: every response must match the contract MCP and the console generate from.
func TestSiteIndexEndpointsFollowTheContract(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("isolated-test-password")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "index_tester", hash))
	exec := func(query string, args ...any) {
		t.Helper()
		require.NoError(t, st.DB.WithContext(ctx).Exec(query, args...).Error, query)
	}
	mutations := []store.CatalogMutation{}
	for _, raw := range []string{
		`{"token":"lookup-app","name":["Lookup App"],"version":"2.0","desc":"Edits code","artifacts":[{"app":["Lookup.app"]},{"uninstall":[{"launchctl":"com.lookup.helper","pkgutil":"com.lookup.pkg"}]}]}`,
		`{"token":"lookup-gone","name":["Lookup Gone"],"version":"1.0","desc":"Was here"}`,
		`{"token":"tap-app","full_token":"lookup-owner/tap/tap-app","tap":"lookup-owner/tap","name":["Tap App"],"version":"1.0","desc":"From a tap"}`,
		`{"token":"alt-one","name":["Alt One"],"version":"1.0","desc":"Alternative"}`,
		`{"token":"alt-two","name":["Alt Two"],"version":"1.0","desc":"Alternative"}`,
		`{"token":"alt-three","name":["Alt Three"],"version":"1.0","desc":"Alternative"}`,
	} {
		p, err := homebrew.Normalize("cask", json.RawMessage(raw))
		require.NoError(t, err)
		mutations = append(mutations, store.CatalogMutation{Package: p})
	}
	require.NoError(t, st.ApplyCatalogBatch(ctx, mutations))
	ids := map[string]int64{}
	var rows []struct {
		ID    int64
		Token string
	}
	require.NoError(t, st.DB.Raw("SELECT id,token FROM packages").Scan(&rows).Error)
	for _, row := range rows {
		ids[row.Token] = row.ID
	}
	app := ids["lookup-app"]
	exec(`INSERT INTO package_i18n(package_id,locale,display_name,summary,description,status,source,source_locale,source_hash) VALUES
 (?,'zh-CN','查找应用','编辑代码','更长的介绍','machine','llm','en-US',repeat('a',64))`, app)
	for i, token := range []string{"alt-one", "alt-two", "alt-three"} {
		exec(`INSERT INTO package_alternatives(package_id,alternative_id,sort,reasons,source) VALUES(?,?,?,ARRAY['open_source'],'admin')`, app, ids[token], i)
		exec(`INSERT INTO package_fact_records(package_id,kind,key,value,evidence) VALUES(?,'attribute','open-source','{"name":"open_source","booleanValue":true}',
 jsonb_build_object('sourceUrl','https://vendor.example/docs','checkedAt',now()-interval '1 day','validUntil',now()+interval '3 days','status','verified'))`, ids[token])
	}
	exec(`INSERT INTO package_aliases(kind,token,package_id,source) VALUES('cask','lookup-old',?,'renamed')`, app)
	gone, err := st.CatalogRows(ctx, "cask")
	require.NoError(t, err)
	require.NoError(t, st.RemoveCatalogRows(ctx, []*store.CatalogRow{gone["lookup-gone"]}))
	homepage := "https://lookup.example/link"
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		ids["lookup-link"], err = (&store.Store{DB: tx}).WriteLinkApp(ctx, 0, domain.LinkInput{Token: "lookup-link", Name: "Lookup Link", Homepage: &homepage, SourceLocale: "en-US"})
		return err
	}))
	exec(`INSERT INTO articles(id,slug,status,working_revision_id,published_revision_id,first_published_at) OVERRIDING SYSTEM VALUE VALUES(9401,'lookup-article','published',9401,9401,now());
 INSERT INTO article_revisions(id,article_id,number,source_locale,type) OVERRIDING SYSTEM VALUE VALUES(9401,9401,1,'en-US','news');
 INSERT INTO article_revision_i18n(revision_id,locale,title,source_locale,status) VALUES(9401,'en-US','Lookup article','en-US','source'),(9401,'de-DE','Artikel','en-US','machine')`)
	exec(`INSERT INTO article_slug_aliases(slug,article_id) VALUES('lookup-article-old',9401)`)
	now := time.Now()
	require.NoError(t, st.SaveBackgroundContent(ctx, "com.lookup.helper", facts.BackgroundContent{Locale: "en-US", Description: "Lookup App's update helper. Verify its signature, path and version locally.",
		ItemType: "launch_agent", CommonPaths: []string{"~/Library/LaunchAgents/com.lookup.helper.plist"}, Evidence: facts.NewProof("https://lookup.example/helper", now, time.Hour, "verified", now)}))
	exec(`INSERT INTO categories(id,slug,icon,source_locale) VALUES(9501,'lookup-tools','lucide:box','en-US')`)

	cfg := config.Config{AppEnv: "dev", JWTSecret: strings.Repeat("a", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour, WebBaseURL: "https://opennavo.example"}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "index_tester", "isolated-test-password", store.AuditActor{})
	require.NoError(t, err)
	server, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{
		Admin:  &admin.Handler{Auth: authentication, Management: &management.Service{Store: st, Config: cfg}},
		Public: &public.Handler{Catalog: &catalog.PublicService{Store: st, WebBaseURL: cfg.WebBaseURL, Secrets: cfg}},
	})
	require.NoError(t, err)
	adminSpec, err := adminapi.GetSpec()
	require.NoError(t, err)
	publicSpec, err := publicapi.GetSpec()
	require.NoError(t, err)
	type envelope struct {
		Code string          `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	call := func(method, path string, body any, headers map[string]string) (envelope, string) {
		t.Helper()
		var raw io.Reader
		if body != nil {
			encoded, err := json.Marshal(body)
			require.NoError(t, err)
			raw = bytes.NewReader(encoded)
		}
		req := httptest.NewRequest(method, "/admin-api"+path, raw)
		req.Header.Set("Authorization", "Bearer "+pair.Token)
		req.Header.Set("Content-Type", "application/json")
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		rec := httptest.NewRecorder()
		server.Engine.ServeHTTP(rec, req)
		require.Equal(t, 200, rec.Code, rec.Body.String())
		testutil.ValidateResponse(t, adminSpec, "/admin-api", req, rec)
		var out envelope
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out, rec.Body.String()
	}
	ok := func(method, path string, body any, headers map[string]string) map[string]any {
		t.Helper()
		out, text := call(method, path, body, headers)
		require.Equal(t, "0000", out.Code, text)
		var data map[string]any
		require.NoError(t, json.Unmarshal(out.Data, &data))
		return data
	}
	rejected := func(method, path string, body any, headers map[string]string, code, reason string) {
		t.Helper()
		out, text := call(method, path, body, headers)
		require.Equal(t, code, out.Code, text)
		if reason != "" {
			require.Contains(t, out.Msg, "("+reason+")", "the console shows the reason after the localized message")
		}
	}

	settings := ok("GET", "/system/seo-settings", nil, nil)["settings"].(map[string]any)
	settings["pages"].(map[string]any)["openLocales"] = []string{"en-US", "zh-CN"}
	settings["pages"].(map[string]any)["intentPagesEnabled"] = true
	ok("PUT", "/system/seo-settings", map[string]any{"settings": settings}, nil)
	// Credentials and CDN identifiers that no read-only view may show.
	exec(`UPDATE seo_settings SET indexnow_key_ciphertext='ciphertext-of-the-indexnow-key',cloudflare_token_ciphertext='ciphertext-of-the-cloudflare-token',
 settings=jsonb_set(jsonb_set(settings,'{cloudflare}','{"zoneId":"0123456789abcdef0123456789abcdef","purgeEnabled":true}'),'{verification}','{"google":"google-verification-token","bing":"bing-verification-token"}')`)

	// The read-only site settings never contain credentials, their ciphertexts or CDN identifiers.
	view, text := call("GET", "/site-index/settings", nil, nil)
	require.Equal(t, "0000", view.Code)
	for _, secret := range []string{"ciphertext-of-the-indexnow-key", "ciphertext-of-the-cloudflare-token", "0123456789abcdef0123456789abcdef", "google-verification-token", "bing-verification-token", "zoneId", "cloudflare"} {
		require.NotContains(t, text, secret)
	}
	var site map[string]any
	require.NoError(t, json.Unmarshal(view.Data, &site))
	require.Equal(t, "https://opennavo.example", site["siteUrl"])
	require.Equal(t, []any{"en-US", "zh-CN"}, site["pages"].(map[string]any)["openLocales"])
	require.Equal(t, map[string]any{"enabled": false, "longTtlSeconds": float64(172800), "shortTtlSeconds": float64(600)}, site["edgeCache"])
	require.Len(t, site["changeFeed"], 10)

	decision := func(raw string) map[string]any {
		t.Helper()
		return ok("GET", "/page-decision?url="+url.QueryEscape(raw), nil, nil)
	}
	base := "https://opennavo.example"
	overview := decision(base + "/apps/lookup-app")
	require.Equal(t, "app", overview["kind"])
	require.Equal(t, "server", overview["decidedBy"])
	require.Equal(t, true, overview["indexable"])
	require.Equal(t, base+"/apps/lookup-app", overview["canonical"])
	require.Equal(t, []any{map[string]any{"locale": "en-US", "url": base + "/apps/lookup-app"}, map[string]any{"locale": "zh-CN", "url": base + "/zh/apps/lookup-app"}}, overview["hreflang"])
	require.Equal(t, map[string]any{"type": "package", "id": float64(app), "key": "lookup-app", "packageKind": "cask"}, overview["entity"])
	for _, tc := range []struct{ path, kind, reason, canonical string }{
		{"/zh/apps/lookup-app/versions?page=2", "app_versions", "thin_notes_page", "/zh/apps/lookup-app/versions?page=2"},
		{"/apps/lookup-app/versions?notes=1", "app_versions", "filtered_view", "/apps/lookup-app/versions?notes=1"},
		{"/apps/lookup-app/details", "app_details", "consolidated", "/apps/lookup-app"},
		{"/apps/lookup-app/alternatives", "app_alternatives", "intent_not_eligible", "/apps/lookup-app"},
		{"/de/apps/lookup-app", "app", "locale_closed", "/de/apps/lookup-app"},
		{"/apps/lookup-old", "app", "redirect", "/apps/lookup-app"},
		{"/apps/lookup-old/versions", "app_versions", "redirect", "/apps/lookup-app/versions"},
		{"/articles/lookup-article-old", "article", "redirect", "/articles/lookup-article"},
		{"/zh/articles/lookup-article", "article", "locale_not_ready", "/zh/articles/lookup-article"},
		{"/de/articles/lookup-article", "article", "locale_closed", "/de/articles/lookup-article"},
	} {
		got := decision(base + tc.path)
		require.Equal(t, tc.kind, got["kind"], tc.path)
		require.Equal(t, false, got["indexable"], tc.path)
		require.Equal(t, "noindex, follow", got["robots"], tc.path)
		require.Equal(t, tc.reason, got["reason"], tc.path)
		require.Equal(t, base+tc.canonical, got["canonical"], tc.path)
	}
	for _, tc := range []struct{ path, reason string }{{"/apps/lookup-gone", "removed"}, {"/apps/no-such-app", "not_found"}, {"/articles/no-such-article", "not_found"}, {"/background-items/com.nothing.here", "not_found"}} {
		got := decision(tc.path)
		require.Equal(t, tc.reason, got["reason"], tc.path)
		require.Nil(t, got["canonical"], tc.path)
	}
	article := decision(base + "/articles/lookup-article")
	require.Equal(t, true, article["indexable"])
	require.Equal(t, []any{map[string]any{"locale": "en-US", "url": base + "/articles/lookup-article"}}, article["hreflang"], "German is ready but closed")
	helper := decision(base + "/background-items/com.lookup.helper")
	require.Equal(t, "background_item", helper["kind"])
	require.Equal(t, true, helper["indexable"])
	require.Equal(t, base+"/background-items/com.lookup.helper", helper["canonical"])
	require.Equal(t, false, decision(base + "/zh/background-items/com.lookup.helper")["indexable"], "only its verified language is indexed")
	for _, tc := range []struct{ path, kind, locale string }{
		{"/", "home", "en-US"}, {"/zh", "home", "zh-CN"}, {"/zh-hant/categories/lookup-tools", "category", "zh-Hant"},
		{"/collections/new-mac", "collection", "en-US"}, {"/authors/someone", "author", "en-US"}, {"/security/gatekeeper", "security_topic", "en-US"},
		{"/browse/fonts/a", "list", "en-US"}, {"/recent", "list", "en-US"}, {"/download", "static", "en-US"}, {"/articles", "article_list", "en-US"},
		{"/apps/lookup-app/feed.xml", "other", "en-US"},
	} {
		got := decision(tc.path)
		require.Equal(t, tc.kind, got["kind"], tc.path)
		require.Equal(t, tc.locale, got["locale"], tc.path)
		require.Equal(t, "web", got["decidedBy"], tc.path)
		if got["localeOpen"] == true {
			require.Nil(t, got["indexable"], tc.path)
			require.Nil(t, got["robots"], tc.path)
		} else {
			require.Equal(t, false, got["indexable"], tc.path)
			require.Equal(t, "locale_closed", got["reason"], tc.path)
		}
	}
	rejected("GET", "/page-decision?url="+url.QueryEscape("https://elsewhere.example/apps/lookup-app"), nil, nil, "1001", "url_not_on_site:url")
	rejected("GET", "/page-decision?url="+url.QueryEscape(base+"/apps/lookup-app#top"), nil, nil, "1001", "url_invalid:url")

	// Intent pages: the full matrix, editorial switches, admin revision checks.
	intents := ok("GET", fmt.Sprintf("/packages/%d/intent-pages", app), nil, nil)
	items := intents["items"].([]any)
	require.Len(t, items, 20)
	require.Equal(t, true, intents["globalEnabled"])
	first := items[0].(map[string]any)
	require.Equal(t, map[string]any{"pageKind": "alternatives", "locale": "en-US", "enabled": false, "derived": false, "contentEligible": true, "indexable": false,
		"reason": "intent_not_eligible", "url": base + "/apps/lookup-app/alternatives", "enabledAt": nil, "enabledBy": nil}, first)
	require.Equal(t, true, items[9].(map[string]any)["derived"])
	require.Equal(t, "zh-Hant", items[9].(map[string]any)["locale"])
	revision := intents["revision"].(float64)
	path := fmt.Sprintf("/packages/%d/intent-pages", app)
	body := map[string]any{"items": []any{map[string]any{"pageKind": "alternatives", "locale": "en-US", "enabled": true}, map[string]any{"pageKind": "alternatives", "locale": "zh-CN", "enabled": true}}}
	rejected("PUT", path, body, map[string]string{"X-Expected-Revision": fmt.Sprint(revision + 5)}, "1003", fmt.Sprintf("revision_conflict:%v", revision))
	preview := ok("PUT", path+"?dryRun=true", body, nil)
	require.Equal(t, true, preview["dryRun"])
	require.Equal(t, []any{base + "/apps/lookup-app/alternatives", base + "/zh/apps/lookup-app/alternatives", base + "/zh-hant/apps/lookup-app/alternatives"}, preview["affectedUrls"])
	require.Equal(t, revision, ok("GET", path, nil, nil)["revision"], "a preview writes nothing")
	written := ok("PUT", path, body, map[string]string{"X-Expected-Revision": fmt.Sprint(revision)})
	require.Equal(t, revision+2, written["revision"])
	byLocale := map[string]map[string]any{}
	for _, item := range written["items"].([]any) {
		entry := item.(map[string]any)
		if entry["pageKind"] == "alternatives" {
			byLocale[entry["locale"].(string)] = entry
		}
	}
	require.Equal(t, true, byLocale["en-US"]["indexable"])
	require.Regexp(t, `^admin:[0-9]+$`, byLocale["en-US"]["enabledBy"])
	require.Equal(t, true, byLocale["zh-CN"]["indexable"])
	require.Equal(t, true, byLocale["zh-Hant"]["enabled"])
	require.Equal(t, "locale_closed", byLocale["zh-Hant"]["reason"], "zh-Hant follows the switch but is closed for indexing")
	rejected("PUT", path, map[string]any{"items": []any{map[string]any{"pageKind": "alternatives", "locale": "zh-Hant", "enabled": true}}}, nil, "1001", "derived_locale_read_only:zh-Hant:zh-CN")
	rejected("PUT", fmt.Sprintf("/packages/%d/intent-pages", ids["lookup-link"]), map[string]any{"items": []any{map[string]any{"pageKind": "uninstall", "locale": "en-US", "enabled": true}}}, nil, "1001", "intent_page_unsupported:uninstall")
	listed := ok("GET", "/intent-pages?pageKind=alternatives", nil, nil)
	require.EqualValues(t, 2, listed["total"])
	derived := ok("GET", "/intent-pages?locale=zh-Hant", nil, nil)["records"].([]any)
	require.Len(t, derived, 1)
	require.Equal(t, true, derived[0].(map[string]any)["derived"])
	require.Equal(t, base+"/zh-hant/apps/lookup-app/alternatives", derived[0].(map[string]any)["url"])
	require.Equal(t, true, decision(base + "/zh/apps/lookup-app/alternatives")["indexable"], "the page decision agrees with the matrix")

	// Overview title and description overrides, public detail included.
	seoPath := fmt.Sprintf("/packages/%d/seo", app)
	empty := ok("GET", seoPath, nil, nil)
	require.Empty(t, empty["items"])
	rejected("PUT", seoPath, map[string]any{"locale": "en-US", "title": "<b>Lookup</b>"}, nil, "1001", "seo_text_invalid:title")
	rejected("PUT", seoPath, map[string]any{"locale": "en-US", "description": strings.Repeat("a", 201)}, nil, "1001", "field_too_long:description")
	rejected("PUT", seoPath, map[string]any{"locale": "zh-Hant", "title": "查找"}, nil, "1001", "derived_locale_read_only:zh-Hant:zh-CN")
	rejected("PUT", seoPath, map[string]any{"locale": "en-US", "title": "Stale"}, map[string]string{"X-Expected-Revision": "7"}, "1003", "revision_conflict:0")
	saved := ok("PUT", seoPath, map[string]any{"locale": "zh-CN", "title": "  查找应用下载与安装  ", "description": "用 Homebrew 下载查找应用"}, map[string]string{"X-Expected-Revision": "0"})
	require.EqualValues(t, 1, saved["revision"])
	entries := saved["items"].([]any)
	require.Len(t, entries, 2)
	require.Equal(t, "查找应用下载与安装", entries[0].(map[string]any)["title"], "values are trimmed")
	require.Equal(t, "zh-Hant", entries[1].(map[string]any)["locale"])
	require.Equal(t, true, entries[1].(map[string]any)["derived"])
	ok("PUT", seoPath, map[string]any{"locale": "en-US", "title": "Lookup App for Mac"}, nil)
	detail := func(locale string) publicapi.PackageDetail {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/packages/cask/lookup-app?locale="+locale, nil)
		rec := httptest.NewRecorder()
		server.Engine.ServeHTTP(rec, req)
		require.Equal(t, 200, rec.Code, rec.Body.String())
		testutil.ValidateResponse(t, publicSpec, "/api/v1", req, rec)
		var out struct{ Data publicapi.PackageDetail }
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out.Data
	}
	english := detail("en-US")
	require.NotNil(t, english.SeoOverride)
	require.Equal(t, "Lookup App for Mac", *english.SeoOverride.Title)
	require.Nil(t, english.SeoOverride.Description)
	traditionalDetail := detail("zh-Hant")
	require.NotNil(t, traditionalDetail.SeoOverride)
	require.Contains(t, *traditionalDetail.SeoOverride.Description, "Homebrew")
	require.NotEqual(t, "查找应用下载与安装", *traditionalDetail.SeoOverride.Title, "zh-Hant shows the converted zh-CN text")
	require.Nil(t, detail("ja-JP").SeoOverride)

	// Fact records due for a recheck.
	due := ok("GET", "/facts/due?withinDays=7&kind=attribute", nil, nil)
	require.EqualValues(t, 3, due["total"])
	rejected("GET", "/facts/due?kind=unknown", nil, nil, "1001", "") // the contract's enum rejects it before the service

	// Catalog changes: link and tap apps, read by cursor.
	all := ok("GET", "/catalog/changes?afterSeq=0&size=100", nil, nil)
	sources := map[string]string{}
	for _, record := range all["records"].([]any) {
		entry := record.(map[string]any)
		sources[entry["token"].(string)+":"+entry["type"].(string)] = entry["kind"].(string) + "/" + entry["source"].(string)
	}
	require.Equal(t, "cask/homebrew", sources["lookup-app:created"])
	require.Equal(t, "cask/tap", sources["tap-app:created"])
	require.Equal(t, "link/link", sources["lookup-link:created"])
	require.Equal(t, "cask/homebrew", sources["lookup-gone:removed"])
	head := all["nextSeq"].(float64)
	page := ok("GET", "/catalog/changes?afterSeq=0&size=2", nil, nil)
	require.Len(t, page["records"], 2)
	rest := ok("GET", fmt.Sprintf("/catalog/changes?afterSeq=%v&size=100", page["nextSeq"]), nil, nil)
	require.Equal(t, len(all["records"].([]any)), 2+len(rest["records"].([]any)), "the cursor neither skips nor repeats")
	require.Equal(t, head, rest["nextSeq"])
	require.Empty(t, ok("GET", fmt.Sprintf("/catalog/changes?afterSeq=%v", head), nil, nil)["records"])
	require.Equal(t, head, ok("GET", "/catalog/changes", nil, nil)["nextSeq"], "a time-window read also returns the cursor")
	rejected("GET", "/catalog/changes?afterSeq=0&sort=popular", nil, nil, "1001", "parameter_conflict:sort")

	// Reads report the revision their writes check.
	pkg := ok("GET", fmt.Sprintf("/packages/%d", app), nil, nil)
	require.Contains(t, pkg, "revision")
	tree, text := call("GET", "/categories/tree", nil, nil)
	require.Equal(t, "0000", tree.Code)
	require.Contains(t, text, `"revision":`)
}
