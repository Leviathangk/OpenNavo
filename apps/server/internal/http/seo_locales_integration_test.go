//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

func TestClosedLocalesAndDependencyTabsStayOutOfTheIndex(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("isolated-test-password")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "locale_tester", hash))
	mutations := []store.CatalogMutation{}
	for _, raw := range []string{
		`{"token":"open-app","name":["Open App"],"version":"2.0","desc":"Edits code"}`,
		`{"token":"helper-app","name":["Helper App"],"version":"1.0","desc":"Helps other apps"}`,
	} {
		p, err := homebrew.Normalize("cask", json.RawMessage(raw))
		require.NoError(t, err)
		mutations = append(mutations, store.CatalogMutation{Package: p})
	}
	require.NoError(t, st.ApplyCatalogBatch(ctx, mutations))
	languages, err := json.Marshal(seo.Locales)
	require.NoError(t, err)
	for _, sql := range []string{
		`UPDATE packages SET dependencies='{"dependsOn":{"cask":["helper-app"]}}' WHERE token='open-app'`,
		// Ready text in every language, so only the open list keeps a language out.
		`INSERT INTO package_i18n(package_id,locale,display_name,summary,description,status,source,source_locale,source_hash)
 SELECT p.id,l.locale,'Open App','Edits code','Longer description','machine','llm','en-US',repeat('a',64) FROM packages p
 CROSS JOIN jsonb_array_elements_text('` + string(languages) + `'::jsonb) l(locale) WHERE p.token='open-app' AND l.locale<>'en-US'`,
		// One statement batch: the article and its revision reference each other.
		`INSERT INTO articles(id,slug,status,working_revision_id,published_revision_id,first_published_at) OVERRIDING SYSTEM VALUE VALUES(9301,'locale-article','published',9301,9301,now());
 INSERT INTO article_revisions(id,article_id,number,source_locale,type) OVERRIDING SYSTEM VALUE VALUES(9301,9301,1,'en-US','news');
 INSERT INTO article_revision_i18n(revision_id,locale,title,source_locale,status) VALUES(9301,'en-US','Locale article','en-US','source'),(9301,'de-DE','Artikel','en-US','machine'),(9301,'ja-JP','記事','en-US','machine')`,
	} {
		require.NoError(t, st.DB.WithContext(ctx).Exec(sql).Error, sql)
	}
	cfg := config.Config{AppEnv: "dev", JWTSecret: strings.Repeat("a", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour, WebBaseURL: "http://localhost:3000"}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "locale_tester", "isolated-test-password", store.AuditActor{})
	require.NoError(t, err)
	app, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{
		Admin:  &admin.Handler{Auth: authentication, Management: &management.Service{Store: st, Config: cfg}},
		Public: &public.Handler{Catalog: &catalog.PublicService{Store: st, WebBaseURL: cfg.WebBaseURL, Secrets: cfg}},
	})
	require.NoError(t, err)
	adminSpec, err := adminapi.GetSpec()
	require.NoError(t, err)
	publicSpec, err := publicapi.GetSpec()
	require.NoError(t, err)
	adminCall := func(method, path string, body any) map[string]any {
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
		rec := httptest.NewRecorder()
		app.Engine.ServeHTTP(rec, req)
		require.Equal(t, 200, rec.Code, rec.Body.String())
		var envelope struct {
			Code string         `json:"code"`
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
		require.Equal(t, "0000", envelope.Code, rec.Body.String())
		testutil.ValidateResponse(t, adminSpec, "/admin-api", req, rec)
		return envelope.Data
	}
	publicGet := func(path string, target any) {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1"+path, nil)
		rec := httptest.NewRecorder()
		app.Engine.ServeHTTP(rec, req)
		require.Equal(t, 200, rec.Code, rec.Body.String())
		testutil.ValidateResponse(t, publicSpec, "/api/v1", req, rec)
		var envelope struct{ Data json.RawMessage }
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
		require.NoError(t, json.Unmarshal(envelope.Data, target))
	}
	pages := func(locale string) publicapi.PackagePageDecisions {
		t.Helper()
		var d publicapi.PackageDetail
		publicGet("/packages/cask/open-app?locale="+locale, &d)
		return d.Indexing.Pages
	}
	open := []publicapi.Locale{"en-US", "zh-CN", "ja-JP", "es-ES", "ru-RU"}

	settings := adminCall("GET", "/system/seo-settings", nil)["settings"].(map[string]any)
	settings["pages"].(map[string]any)["openLocales"] = []string{"en-US", "zh-CN", "ja-JP", "ru-RU", "es-ES"}
	adminCall("PUT", "/system/seo-settings", map[string]any{"settings": settings})

	english := pages("en-US")
	require.Equal(t, publicapi.PageDecisionRobots("index, follow"), english.Overview.Robots)
	require.Equal(t, open, english.Overview.Hreflang)
	// Dependency content keeps the tab's own canonical, but it is noindex, unlisted and unannounced.
	require.Equal(t, publicapi.PageDecision{Robots: "noindex, follow", Hreflang: []publicapi.Locale{}, Reason: seo.ReasonSupportingPage}, english.Dependencies)
	for _, locale := range []string{"pt-BR", "de-DE", "fr-FR", "it-IT", "zh-Hant"} {
		closed := pages(locale)
		require.Equal(t, publicapi.PageDecision{Robots: "noindex, follow", Hreflang: []publicapi.Locale{}, Reason: seo.ReasonLocaleClosed}, closed.Overview, locale)
		require.Equal(t, seo.ReasonLocaleClosed, closed.Versions.Reason, locale)
		require.Equal(t, seo.ReasonSupportingPage, closed.Dependencies.Reason, locale)
	}

	var sitemap struct{ Records []publicapi.SitemapEntry }
	publicGet("/sitemap/packages?group=apps", &sitemap)
	listed := map[string][]publicapi.Locale{}
	for _, record := range sitemap.Records {
		listed[record.Token] = locales(*record.Locales)
	}
	require.Equal(t, open, listed["open-app"])
	var articles publicapi.ArticleSitemap
	publicGet("/sitemap/articles", &articles)
	require.Len(t, articles.Articles, 1)
	require.Equal(t, []publicapi.Locale{"en-US", "ja-JP"}, locales(articles.Articles[0].Locales), "German is ready but closed")
}
