//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/search"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSixLanguageHTTPNegotiationFallbackSearchAndCache(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	// Start at the old schema before inserting provenance-bearing data. Modern
	// Down migrations intentionally refuse to discard confirmations or evidence.
	sqlDB, err := st.DB.DB()
	require.NoError(t, err)
	require.NoError(t, goose.DownToContext(ctx, sqlDB, ".", 12))
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"six-language","name":["Sample"],"version":"1","desc":"Original English"}`))
	require.NoError(t, err)
	var id int64
	// Only columns present in v12: current catalog writes require newer schema.
	require.NoError(t, st.DB.Raw(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,desc_en,raw,raw_hash,artifacts,dependencies,conflicts_with)
 VALUES(?,?,?,?,?,?,?,?,?::jsonb,?,?::jsonb,?::jsonb,?::jsonb) RETURNING id`, p.Kind, p.Token, p.FullToken, p.Tap, p.Name, p.Version, p.VersionBase, p.Desc, string(p.Raw), p.RawHash, string(p.Artifacts), string(p.Dependencies), string(p.ConflictsWith)).Scan(&id).Error)
	names := map[string]string{"en-US": "English name", "zh-CN": "中文名称", "ja-JP": "日本語アプリ", "es-ES": "Nombre español", "pt-BR": "Nome brasileiro", "ru-RU": "Русское название"}
	summaries := map[string]string{"en-US": "Unique sketching", "zh-CN": "独特绘图工具", "ja-JP": "独自の描画ツール", "es-ES": "Dibujo exclusivo", "pt-BR": "Desenho exclusivo", "ru-RU": "Уникальное рисование"}
	descriptions := map[string]string{"en-US": "Collaborative workflow", "zh-CN": "支持团队协作", "ja-JP": "共同作業に対応", "es-ES": "Colaboración simultánea", "pt-BR": "Colaboração simultânea", "ru-RU": "Совместная работа"}
	for code, name := range names {
		status := "machine"
		if code == "en-US" {
			status = "source"
		}
		require.NoError(t, st.DB.Exec("INSERT INTO package_i18n(package_id,locale,display_name,summary,description,status,source,source_locale) VALUES(?,?,?,?,?,?,'llm','en-US')", id, code, name, summaries[code], descriptions[code], status).Error)
	}
	// Exercise upgrade backfill with translations already present, before the
	// application gets a chance to rebuild their search index.
	require.NoError(t, goose.UpContext(ctx, sqlDB, "."))
	var indexed string
	require.NoError(t, st.DB.Raw("SELECT search_content FROM packages WHERE id=?", id).Scan(&indexed).Error)
	for code := range names {
		require.Contains(t, indexed, strings.ToLower(summaries[code]))
		require.Contains(t, indexed, strings.ToLower(descriptions[code]))
	}
	data, err := seeds.Load()
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "", ""))
	require.NoError(t, store.UpdateSearchIndex(ctx, st.DB, []int64{id}))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	backend := cache.Redis{Client: st.Redis}
	searchSvc := search.New(ctx, st, backend, logger)
	t.Cleanup(searchSvc.Close)
	svc := &catalog.PublicService{Store: st, Cache: backend, WebBaseURL: "https://example.com"}
	server, err := httpserver.New(config.Config{}, logger, st, httpserver.Dependencies{Public: &public.Handler{Catalog: svc, Search: searchSvc}})
	require.NoError(t, err)
	spec, err := publicapi.GetSpec()
	require.NoError(t, err)
	call := func(path, accept, etag string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/v1"+path, nil)
		r.Header.Set("Accept-Language", accept)
		if etag != "" {
			r.Header.Set("If-None-Match", etag)
		}
		w := httptest.NewRecorder()
		server.Engine.ServeHTTP(w, r)
		testutil.ValidateResponse(t, spec, "/api/v1", r, w)
		require.Contains(t, w.Header().Get("Vary"), "Accept-Language")
		require.Contains(t, w.Header().Get("Vary"), "Origin")
		return w
	}
	tags := map[string]string{}
	for code, name := range names {
		w := call("/packages/cask/six-language", code, "")
		require.Equal(t, 200, w.Code, w.Body.String())
		var body publicapi.PackageDetailResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Equal(t, name, body.Data.DisplayName)
		require.Equal(t, publicapi.Locale(code), *body.Data.SummaryTranslation.Locale)
		require.Equal(t, publicapi.Locale("en-US"), *body.Data.SourceLocale)
		require.Equal(t, code != "en-US", *body.Data.MachineTranslated)
		tags[code] = w.Header().Get("ETag")
		require.Equal(t, 304, call("/packages/cask/six-language", code, tags[code]).Code)
		bad := call("/packages/cask/missing", code, "")
		require.Equal(t, 404, bad.Code)
		require.Contains(t, bad.Body.String(), domain.Message(domain.CodeNotFound, code))
	}
	require.NotEqual(t, tags["ja-JP"], tags["en-US"])
	require.Equal(t, 200, call("/packages/cask/six-language", "ja-JP", tags["en-US"]).Code)
	require.Contains(t, call("/packages/cask/six-language?locale=ja-JP", "en-US", "").Body.String(), names["ja-JP"])
	require.Contains(t, call("/packages/cask/six-language", "ru;q=0.3,es-MX;q=0.9", "").Body.String(), names["es-ES"])
	require.Contains(t, call("/packages/cask/six-language", "", "").Body.String(), names["en-US"])
	for code, name := range names {
		for _, query := range []string{name, summaries[code], descriptions[code]} {
			w := call("/search?q="+url.QueryEscape(query)+"&locale="+code, "", "")
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), `"token":"six-language"`, "%s: %s", code, query)
			require.Contains(t, w.Body.String(), name)
		}
	}
	// Rebuilding after translation removal must remove stale prose hits.
	require.NoError(t, st.DB.Exec("UPDATE package_i18n SET description=NULL WHERE package_id=? AND locale='ja-JP'", id).Error)
	require.NoError(t, store.UpdateSearchIndex(ctx, st.DB, []int64{id}))
	require.NotContains(t, call("/search?q="+url.QueryEscape(descriptions["ja-JP"])+"&locale=ja-JP", "", "").Body.String(), `"token":"six-language"`)
	require.NoError(t, st.DB.Exec("UPDATE packages SET source_locale='zh-CN' WHERE id=?;", id).Error)
	require.NoError(t, st.DB.Exec("UPDATE package_i18n SET source_locale='zh-CN',status=CASE WHEN locale='zh-CN' THEN 'source' ELSE 'manual' END WHERE package_id=?", id).Error)
	require.NoError(t, st.DB.Exec("DELETE FROM package_i18n WHERE package_id=? AND locale='ja-JP'", id).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO package_i18n(package_id,locale,status,source,source_locale) VALUES(?,'ja-JP','pending','llm','zh-CN')", id).Error)
	row, err := st.PublicPackage(ctx, "cask", "six-language", "ja-JP")
	require.NoError(t, err)
	// Fall back to English when the requested language is missing, then to the source language if English is also missing.
	require.Equal(t, names["en-US"], *row.I18n.DisplayName)
	require.NoError(t, st.DB.Exec("DELETE FROM package_i18n WHERE package_id=? AND locale='en-US'", id).Error)
	row, err = st.PublicPackage(ctx, "cask", "six-language", "ja-JP")
	require.NoError(t, err)
	require.Equal(t, names["zh-CN"], *row.I18n.DisplayName)
	require.NoError(t, st.DB.Exec("DELETE FROM package_i18n WHERE package_id=? AND locale='zh-CN'", id).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO package_i18n(package_id,locale,display_name,summary,status,source,source_locale) VALUES(?,'en-US',?,?,'manual','llm','zh-CN')", id, names["en-US"], names["en-US"]).Error)
	row, err = st.PublicPackage(ctx, "cask", "six-language", "ja-JP")
	require.NoError(t, err)
	require.Equal(t, names["en-US"], *row.I18n.DisplayName)
	require.NoError(t, st.DB.Exec("INSERT INTO releases(package_id,source,source_key,version,body_markdown) VALUES(?,'manual','upstream','1','Upstream'),(?,'editorial','edited','1','Editorial')", id, id).Error)
	require.NoError(t, st.Redis.Del(ctx, "c:rel:c4:cask:six-language:en-US").Err())
	timeline, err := svc.ReleasePage(ctx, "cask", "six-language", "en-US", 1, 10, false)
	require.NoError(t, err)
	require.Len(t, timeline.Records, 1)
	require.Equal(t, publicapi.ReleaseEntrySource("editorial"), timeline.Records[0].Source)
	require.Equal(t, "Editorial", *timeline.Records[0].BodyMarkdown)
	for _, code := range i18n.SiteLocales {
		var categoryID int64
		slug := "source-" + strings.ToLower(string(code))
		require.NoError(t, st.DB.Raw("INSERT INTO categories(slug,icon) VALUES(?,'lucide:box') RETURNING id", slug).Scan(&categoryID).Error)
		name := names[string(code)]
		if name == "" {
			name = "Fixture " + string(code)
		}
		body := map[string]any{"sourceLocale": string(code), "i18n": map[string]any{string(code): map[string]any{"name": name}}}
		if !i18n.Writable(string(code)) {
			// Traditional Chinese is derived from Simplified Chinese and is never a source language.
			require.ErrorContains(t, st.WithTx(ctx, func(tx *gorm.DB) error {
				return (&store.Store{DB: tx}).WriteLocalized(ctx, "category", categoryID, 0, body, true)
			}), "source_locale_invalid")
			continue
		}
		require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
			return (&store.Store{DB: tx}).WriteLocalized(ctx, "category", categoryID, 0, body, true)
		}))
		categories, err := st.PublicCategories(ctx, "en-US")
		require.NoError(t, err)
		found := false
		for _, v := range categories {
			if v.Slug == slug {
				found = true
				require.Equal(t, name, v.Name)
			}
		}
		require.True(t, found)
		body["sourceLocale"] = "unknown"
		require.Error(t, st.WriteLocalized(ctx, "category", categoryID, 0, body, true))
	}
}
