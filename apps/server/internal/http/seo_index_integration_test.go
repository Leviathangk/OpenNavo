//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"slices"
	"strconv"
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
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

func TestIndexPolicySitemapOverridesAndReport(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("isolated-test-password")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "index_tester", hash))
	mutations := []store.CatalogMutation{}
	for _, raw := range []string{
		`{"token":"good-app","name":["Good App"],"version":"2.0","desc":"Edits code"}`,
		`{"token":"no-summary","name":["No Summary"],"version":"latest"}`,
		`{"token":"gone-upstream","name":["Gone"],"version":"1","desc":"Old tool","disabled":true,"disable_date":"2026-01-01"}`,
		`{"token":"font-good","name":["Good Font"],"version":"3","desc":"Typeface"}`,
		`{"token":"hidden-app","name":["Hidden"],"version":"1","desc":"Hidden tool"}`,
	} {
		p, err := homebrew.Normalize("cask", json.RawMessage(raw))
		require.NoError(t, err)
		mutations = append(mutations, store.CatalogMutation{Package: p})
	}
	require.NoError(t, st.ApplyCatalogBatch(ctx, mutations))
	for _, sql := range []string{
		`UPDATE packages SET is_font=true WHERE token='font-good'`,
		`INSERT INTO package_meta(package_id,hidden) SELECT id,true FROM packages WHERE token='hidden-app'`,
		// Sync touched the rows recently; only content times may reach lastmod.
		`UPDATE packages SET updated_at='2026-10-07T00:00:00Z',version_changed_at='2026-10-07T00:00:00Z'`,
		`UPDATE package_i18n SET updated_at='2025-01-01T00:00:00Z'`,
		`INSERT INTO package_versions(package_id,version,version_base,brew_committed_at) SELECT id,'2.0','2.0','2025-03-18T10:00:00Z' FROM packages WHERE token='good-app' ON CONFLICT(package_id,version) DO UPDATE SET brew_committed_at=EXCLUDED.brew_committed_at`,
		`INSERT INTO package_i18n(package_id,locale,summary,status,source,source_locale,updated_at) SELECT id,'zh-CN','编辑代码','machine','llm','en-US','2025-04-01T00:00:00Z' FROM packages WHERE token='good-app'`,
		`INSERT INTO releases(package_id,source,source_key,version,body_markdown) SELECT id,'github_release','v'||n,'1.'||n,'Notes' FROM packages, generate_series(1,3) n WHERE token='good-app'`,
	} {
		require.NoError(t, st.DB.WithContext(ctx).Exec(sql).Error, sql)
	}
	cfg := config.Config{AppEnv: "dev", JWTSecret: strings.Repeat("a", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour, WebBaseURL: "http://localhost:3000"}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "index_tester", "isolated-test-password", store.AuditActor{})
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
	adminCall := func(method, path string, body any, code string) map[string]any {
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
		require.Equal(t, code, envelope.Code, rec.Body.String())
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
	sitemap := func(group string) map[string][]publicapi.SitemapLocale {
		t.Helper()
		var page struct{ Records []publicapi.SitemapEntry }
		publicGet("/sitemap/packages?group="+group, &page)
		out := map[string][]publicapi.SitemapLocale{}
		for _, record := range page.Records {
			out[record.Token] = *record.Locales
		}
		return out
	}
	detail := func(token string) publicapi.PackageIndexing {
		t.Helper()
		var d publicapi.PackageDetail
		publicGet("/packages/cask/"+token, &d)
		return *d.Indexing
	}
	setPolicy := func(mode, scope string, since string) map[string]any {
		t.Helper()
		settings := adminCall("GET", "/system/seo-settings", nil, "0000")["settings"].(map[string]any)
		policy := settings["pages"].(map[string]any)["indexPolicy"].(map[string]any)
		policy["mode"], policy["scope"], policy["since"] = mode, scope, since
		return adminCall("PUT", "/system/seo-settings", map[string]any{"settings": settings}, "0000")["settings"].(map[string]any)["pages"].(map[string]any)["indexPolicy"].(map[string]any)
	}

	// Shadow (the default) keeps today's sitemap: listed, not disabled, all six languages.
	apps := sitemap("apps")
	require.ElementsMatch(t, []string{"good-app", "no-summary"}, keys(apps))
	require.Len(t, apps["good-app"], 6)
	lastmod := map[string]string{}
	for _, entry := range apps["good-app"] {
		lastmod[string(entry.Locale)] = entry.Lastmod.UTC().Format(time.RFC3339)
	}
	require.Equal(t, "2025-03-18T10:00:00Z", lastmod["en-US"], "Homebrew commit, not the 2026-10-07 sync time")
	require.Equal(t, "2025-04-01T00:00:00Z", lastmod["zh-CN"], "the Chinese text change")
	require.Equal(t, []string{"font-good"}, keys(sitemap("fonts")))
	var good publicapi.PackageDetail
	publicGet("/packages/cask/good-app", &good)
	require.Equal(t, "2025-03-18T10:00:00Z", good.VersionCommittedAt.UTC().Format(time.RFC3339))
	require.True(t, detail("no-summary").Indexable)

	// Enforcing everything applies the rule and limits languages to published text.
	var policyCursor int64
	require.NoError(t, st.DB.Raw("SELECT COALESCE(max(seq),0) FROM catalog_changes").Scan(&policyCursor).Error)
	require.Equal(t, "", setPolicy("enforce", "all", "")["since"])
	var policyChanges []string
	require.NoError(t, st.DB.Raw("SELECT token FROM catalog_changes WHERE seq>? AND reason='content'", policyCursor).Scan(&policyChanges).Error)
	require.ElementsMatch(t, []string{"good-app", "no-summary", "gone-upstream", "font-good", "hidden-app"}, policyChanges)

	apps = sitemap("apps")
	require.Equal(t, []string{"good-app"}, keys(apps))
	require.Equal(t, []publicapi.Locale{"en-US", "zh-CN"}, locales(apps["good-app"]))
	noSummary := detail("no-summary")
	require.False(t, noSummary.Indexable)
	require.Equal(t, "missing_summary", *noSummary.Reason)
	require.Empty(t, noSummary.Locales)

	// Overrides need a reason, show in the report and only refresh that app's cached pages.
	var noSummaryID int64
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='no-summary'").Scan(&noSummaryID).Error)
	var goodID int64
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='good-app'").Scan(&goodID).Error)
	path := func(id int64) string { return "/packages/" + strconv.FormatInt(id, 10) + "/index-status" }
	adminCall("PUT", path(noSummaryID), map[string]any{"override": "index"}, "1001")
	adminCall("PUT", path(noSummaryID), map[string]any{"override": "index", "reason": " "}, "1001")
	var changeCursor int64
	require.NoError(t, st.DB.Raw("SELECT COALESCE(max(seq),0) FROM catalog_changes").Scan(&changeCursor).Error)
	forced := adminCall("PUT", path(noSummaryID), map[string]any{"override": "index", "reason": "Placeholder text is being written"}, "0000")
	require.Equal(t, true, forced["eligible"])
	require.Equal(t, true, forced["indexable"])
	require.Equal(t, "Placeholder text is being written", forced["overrideReason"])
	var queued int
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM job_outbox WHERE unique_key='cache:invalidate:seo-index:no-summary' AND payload::text LIKE '%c:pkg:c4:cask:no-summary:*%'").Scan(&queued).Error)
	require.Equal(t, 1, queued)
	var changes []store.URLChange
	require.NoError(t, st.DB.Raw("SELECT * FROM catalog_changes WHERE seq>?", changeCursor).Scan(&changes).Error)
	require.Len(t, changes, 1)
	require.Equal(t, noSummaryID, changes[0].PackageID)
	require.Equal(t, "content", changes[0].Reason)
	require.Equal(t, "upsert", changes[0].Op)
	adminCall("PUT", path(goodID), map[string]any{"override": "noindex", "reason": "Duplicate of another entry"}, "0000")
	apps = sitemap("apps")
	require.Equal(t, []string{"no-summary"}, keys(apps))
	require.Equal(t, []publicapi.Locale{"en-US"}, locales(apps["no-summary"]))
	status := adminCall("GET", path(goodID), nil, "0000")
	require.Equal(t, "forced_noindex", status["reason"])
	require.Equal(t, false, status["indexable"])

	report := adminCall("GET", "/seo/index-report?size=10", nil, "0000")
	require.Equal(t, map[string]any{"hidden": 1.0, "forcedNoindex": 1.0, "unavailable": 1.0, "missingSummary": 0.0, "thin": 0.0, "forcedIndex": 1.0}, report["counts"])
	require.EqualValues(t, 4, report["total"])
	full := report["records"].([]any)
	for current := 1; current <= 5; current++ {
		paged := adminCall("GET", "/seo/index-report?size=1&current="+strconv.Itoa(current), nil, "0000")
		require.Equal(t, report["counts"], paged["counts"])
		require.Equal(t, report["total"], paged["total"])
		if current <= len(full) {
			require.Equal(t, []any{full[current-1]}, paged["records"])
		} else {
			require.Empty(t, paged["records"])
		}
	}
	filtered := adminCall("GET", "/seo/index-report?reason=forced_index", nil, "0000")
	require.Len(t, filtered["records"], 1)
	require.Equal(t, "no-summary", filtered["records"].([]any)[0].(map[string]any)["token"])
	adminCall("GET", "/seo/index-report?reason=bogus", nil, "1001")

	// Scope "new": the server stamps since once and ignores client values; older apps fall outside,
	// except where an override is an explicit decision.
	stamped := setPolicy("enforce", "new", "2000-01-01T00:00:00Z")["since"].(string)
	_, err = time.Parse(time.RFC3339, stamped)
	require.NoError(t, err)
	require.Equal(t, stamped, setPolicy("enforce", "new", "")["since"])
	apps = sitemap("apps")
	require.Equal(t, []string{"no-summary"}, keys(apps), "good-app stays forced out; nothing else is in scope")
	require.Len(t, apps["no-summary"], 1)
	adminCall("PUT", path(goodID), map[string]any{"override": "auto"}, "0000")
	require.Len(t, sitemap("apps")["good-app"], 6, "outside the new scope the long-standing rule applies")
	var policyQueued int
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM job_outbox WHERE unique_key='cache:invalidate:seo-policy'").Scan(&policyQueued).Error)
	require.Equal(t, 1, policyQueued)
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

func locales(entries []publicapi.SitemapLocale) []publicapi.Locale {
	out := []publicapi.Locale{}
	for _, entry := range entries {
		out = append(out, entry.Locale)
	}
	return out
}
