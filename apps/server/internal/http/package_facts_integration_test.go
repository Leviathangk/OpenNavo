//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
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

func TestPackageFactsEvidenceRevisionsAndPublicRead(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("isolated-test-password")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "facts_tester", hash))
	mutations := []store.CatalogMutation{}
	for _, raw := range []string{
		`{"token":"editor","name":["Editor"],"version":"5.2","desc":"Edits text","depends_on":{"macos":{">=":["13"]},"arch":[{"type":"arm","bits":64},{"type":"intel","bits":64}]}}`,
		`{"token":"lite-editor","name":["Lite Editor"],"version":"1.0","desc":"Edits text quickly"}`,
		`{"token":"hidden-editor","name":["Hidden Editor"],"version":"1.0","desc":"Edits text privately"}`,
	} {
		p, err := homebrew.Normalize("cask", json.RawMessage(raw))
		require.NoError(t, err)
		mutations = append(mutations, store.CatalogMutation{Package: p})
	}
	require.NoError(t, st.ApplyCatalogBatch(ctx, mutations))
	ids := map[string]int64{}
	for _, token := range []string{"editor", "lite-editor", "hidden-editor"} {
		var id int64
		require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token=?", token).Scan(&id).Error)
		ids[token] = id
	}
	require.NoError(t, st.DB.Exec("INSERT INTO package_meta(package_id,hidden) VALUES(?,true)", ids["hidden-editor"]).Error)

	cfg := config.Config{AppEnv: "dev", JWTSecret: strings.Repeat("a", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour, WebBaseURL: "http://localhost:3000"}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "facts_tester", "isolated-test-password", store.AuditActor{})
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
	call := func(method, path string, body any, code string) (map[string]any, string) {
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
		return envelope.Data, rec.Header().Get("X-Request-ID")
	}
	detail := func() publicapi.PackageDetail {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/packages/cask/editor?locale=en-US", nil)
		rec := httptest.NewRecorder()
		app.Engine.ServeHTTP(rec, req)
		require.Equal(t, 200, rec.Code, rec.Body.String())
		testutil.ValidateResponse(t, publicSpec, "/api/v1", req, rec)
		var envelope struct{ Data publicapi.PackageDetail }
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
		return envelope.Data
	}
	base := "/packages/" + strconv.FormatInt(ids["editor"], 10)

	// Nothing recorded: price unknown, Homebrew's requirements, no alternatives.
	facts, _ := call("GET", base+"/facts", nil, "0000")
	require.Nil(t, facts["price"])
	require.Equal(t, []any{"price.unknown"}, facts["gaps"])
	require.Equal(t, map[string]any{"supportStatus": "known", "arm64": true, "x86_64": true, "requiresRosetta": false, "minimum": "13", "maximum": nil}, facts["homebrew"])
	empty := detail()
	require.Nil(t, empty.Price)
	require.Nil(t, empty.MacosSupplement)
	require.Empty(t, *empty.Alternatives)

	// Claims without evidence, or "unknown" carrying data, are rejected.
	call("PUT", base+"/price", map[string]any{"type": "free"}, "1001")
	call("PUT", base+"/price", map[string]any{"type": "unknown", "plan": "Pro"}, "1001")
	call("PUT", base+"/price", map[string]any{"type": "paid", "amount": 5, "currency": "USD", "period": "year", "sourceUrl": "https://vendor.example/pricing", "method": "vendor_site"}, "1001")

	// A dry run reports the evidence still missing and writes nothing.
	subscription := map[string]any{"type": "subscription", "amount": 9.99, "currency": "USD", "period": "month", "sourceUrl": "https://vendor.example/pricing", "method": "vendor_site"}
	preview, _ := call("PUT", base+"/price?dryRun=true", subscription, "0000")
	require.Equal(t, true, preview["dryRun"])
	require.Equal(t, []any{"price.offer", "price.version"}, preview["sourceGaps"])
	require.Equal(t, []any{"http://localhost:3000/apps/editor"}, preview["affectedUrls"])
	var rows int
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM package_facts").Scan(&rows).Error)
	require.Zero(t, rows)

	saved, _ := call("PUT", base+"/price", subscription, "0000")
	price := saved["price"].(map[string]any)
	require.Equal(t, "facts_tester", price["verifiedBy"])
	require.Equal(t, "admin", price["verifier"])
	require.Equal(t, false, saved["offerReady"], "no region or plan yet")
	public := detail()
	require.Equal(t, publicapi.AppPriceType("subscription"), public.Price.Type)
	require.Equal(t, 9.99, *public.Price.Amount)
	require.False(t, public.Price.Offer)

	subscription["region"], subscription["plan"], subscription["appVersion"] = "US", "Pro", "5.2"
	saved, _ = call("PUT", base+"/price", subscription, "0000")
	require.Equal(t, true, saved["offerReady"])
	require.True(t, detail().Price.Offer)

	// Evidence marked for review keeps its record but leaves pages; old evidence is flagged.
	subscription["status"] = "needs_review"
	saved, _ = call("PUT", base+"/price", subscription, "0000")
	require.Contains(t, saved["gaps"], "price.review")
	require.Nil(t, detail().Price)
	delete(subscription, "status")
	subscription["verifiedAt"] = time.Now().Add(-400 * 24 * time.Hour).UTC().Format(time.RFC3339)
	saved, _ = call("PUT", base+"/price", subscription, "0000")
	require.Equal(t, true, saved["price"].(map[string]any)["stale"])
	stale := detail().Price
	require.True(t, stale.Stale)
	require.False(t, stale.Offer)

	// The supplement keeps Homebrew's side and both are shown where they disagree.
	call("PUT", base+"/macos-support", map[string]any{"ranges": []any{map[string]any{"arch": "x86_64", "support": "rosetta"}}, "sourceUrl": "https://vendor.example/requirements", "method": "vendor_docs"}, "1001")
	saved, _ = call("PUT", base+"/macos-support", map[string]any{
		"ranges":    []any{map[string]any{"arch": "arm64", "support": "native", "minimum": "12"}, map[string]any{"arch": "x86_64", "support": "native", "minimum": "12"}},
		"sourceUrl": "https://vendor.example/requirements", "method": "vendor_docs", "appVersion": "5.2",
	}, "0000")
	require.Equal(t, []any{"minimum"}, saved["conflicts"])
	require.Contains(t, saved["gaps"], "macos.conflict")
	supplement := detail().MacosSupplement
	require.Equal(t, []publicapi.MacosSupplementConflicts{"minimum"}, supplement.Conflicts)
	require.Equal(t, "12", *supplement.Ranges[0].Minimum)
	require.Equal(t, "13", *detail().MinMacos, "Homebrew's requirement is unchanged")

	// Alternatives: directional, ordered, hidden ones stored but not shown.
	alternatives := "/alternatives"
	call("PUT", base+alternatives, map[string]any{"items": []any{map[string]any{"packageId": ids["editor"], "reasons": []any{"free"}}}}, "1001")
	call("PUT", base+alternatives, map[string]any{"items": []any{map[string]any{"packageId": 999999, "reasons": []any{"free"}}}}, "1001")
	call("PUT", base+alternatives, map[string]any{"items": []any{map[string]any{"packageId": ids["lite-editor"], "reasons": []any{}}}}, "1001")
	saved, requestID := call("PUT", base+alternatives, map[string]any{"items": []any{
		map[string]any{"packageId": ids["hidden-editor"], "reasons": []any{"privacy"}},
		map[string]any{"packageId": ids["lite-editor"], "reasons": []any{"open_source", "lighter"}, "limits": []any{"fewer_features"}, "bestFor": "developers", "note": "internal"},
	}}, "0000")
	listed := saved["alternatives"].([]any)
	require.Len(t, listed, 2)
	require.Equal(t, false, listed[0].(map[string]any)["available"])
	require.Contains(t, saved["gaps"], "alternatives.unavailable")
	shown := *detail().Alternatives
	require.Len(t, shown, 1)
	require.Equal(t, "lite-editor", shown[0].Package.Token)
	require.Equal(t, []publicapi.PackageAlternativeReasons{"open_source", "lighter"}, shown[0].Reasons)
	require.Equal(t, publicapi.PackageAlternativeBestFor("developers"), *shown[0].BestFor)
	var reverse int
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM package_alternatives WHERE package_id=?", ids["lite-editor"]).Scan(&reverse).Error)
	require.Zero(t, reverse, "the relation is directional")

	// Every write is a package revision; undoing the request restores the previous list.
	var revisions int
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM content_revisions WHERE entity='package' AND object_key=?", strconv.FormatInt(ids["editor"], 10)).Scan(&revisions).Error)
	require.Equal(t, 6, revisions, "four price writes, one supplement and one list")
	call("POST", "/content/requests/"+requestID+"/revert", nil, "0000")
	facts, _ = call("GET", base+"/facts", nil, "0000")
	require.Empty(t, facts["alternatives"])
	require.NotNil(t, facts["macos"], "undo touches only that request")

	// Clearing removes the row once both documents are empty; the price gap lists the app again.
	call("PUT", base+"/macos-support", map[string]any{"ranges": []any{}}, "0000")
	page, _ := call("GET", "/packages?gaps=price&size=50", nil, "0000")
	require.True(t, containsToken(page, "editor"), "stale price counts as a gap")
	saved, _ = call("PUT", base+"/price", map[string]any{"type": "unknown"}, "0000")
	require.Nil(t, saved["price"])
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM package_facts").Scan(&rows).Error)
	require.Zero(t, rows)
	call("PUT", base+"/price", map[string]any{"type": "free", "sourceUrl": "https://vendor.example/download", "method": "vendor_site"}, "0000")
	page, _ = call("GET", "/packages?gaps=price&size=50", nil, "0000")
	require.False(t, containsToken(page, "editor"))
	require.True(t, containsToken(page, "lite-editor"))
}

func containsToken(page map[string]any, token string) bool {
	for _, record := range page["records"].([]any) {
		if record.(map[string]any)["token"] == token {
			return true
		}
	}
	return false
}
