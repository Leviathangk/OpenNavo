//go:build integration

package httpserver_test

import (
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
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/content"
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

func TestContentContractsScheduleWindowBrewfileAndAudit(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	hash, err := seed.HashPassword("test-password-strong")
	require.NoError(t, err)
	data, err := seeds.Load()
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "tester", hash))
	for _, f := range []struct{ kind, raw string }{{"cask", `{"token":"editor","name":["Editor"],"version":"1"}`}, {"formula", `{"name":"ripgrep","versions":{"stable":"1"}}`}} {
		p, err := homebrew.Normalize(f.kind, json.RawMessage(f.raw))
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	}
	var appID, cliID int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id FROM packages WHERE token='editor'").Scan(&appID).Error)
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id FROM packages WHERE token='ripgrep'").Scan(&cliID).Error)
	now := time.Date(2026, 10, 5, 3, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	cfg := config.Config{JWTSecret: strings.Repeat("a", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: time.Hour}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "tester", "test-password-strong", store.AuditActor{})
	require.NoError(t, err)
	publicSvc := &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}, Now: clock}
	adminSvc := &management.Service{Store: st, Now: clock}
	server, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: &admin.Handler{Auth: authentication, Management: adminSvc}, Public: &public.Handler{Catalog: publicSvc}})
	require.NoError(t, err)
	adminSpec, err := adminapi.GetSpec()
	require.NoError(t, err)
	pubSpec, err := publicapi.GetSpec()
	require.NoError(t, err)
	callAdmin := func(method, path, body, code string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/admin-api"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+pair.Token)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		server.Engine.ServeHTTP(w, r)
		testutil.ValidateResponse(t, adminSpec, "/admin-api", r, w)
		var response struct{ Code string }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, code, response.Code, w.Body.String())
		return w
	}
	callPublic := func(path string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/v1"+path, nil)
		w := httptest.NewRecorder()
		server.Engine.ServeHTTP(w, r)
		testutil.ValidateResponse(t, pubSpec, "/api/v1", r, w)
		require.Equal(t, status, w.Code, w.Body.String())
		return w
	}
	idFrom := func(w *httptest.ResponseRecorder) string {
		var response struct{ Data struct{ ID int64 } }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Positive(t, response.Data.ID)
		return strconv.FormatInt(response.Data.ID, 10)
	}
	id := idFrom(callAdmin("POST", "/collections", `{"slug":"essentials","sort":1,"unpublishAt":"2026-10-05T05:00:00Z","sourceLocale":"zh-CN","i18n":{"zh-CN":{"title":"精选工具","body":"正文"},"en-US":{"title":"Essentials"}}}`, "0000"))
	callAdmin("GET", "/collections?q=精选", "", "0000")
	callAdmin("GET", "/collections/"+id, "", "0000")
	callAdmin("POST", "/collections/"+id+"/publish", `{}`, "1006")
	items := `{"items":[{"packageId":` + strconv.FormatInt(appID, 10) + `,"sourceLocale":"zh-CN","i18n":{"zh-CN":{"note":"推荐"}}},{"packageId":` + strconv.FormatInt(cliID, 10) + `}]}`
	callAdmin("PUT", "/collections/"+id+"/items", items, "0000")
	// Deleting and re-adding the same recommendation must leave no stale deduplication hashes or pending deliveries.
	callAdmin("PUT", "/collections/"+id+"/items", `{"items":[]}`, "0000")
	var residue int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM content_translation_sources WHERE entity='collection_item' AND object_key=?", id+":"+strconv.FormatInt(appID, 10)).Scan(&residue).Error)
	require.Zero(t, residue)
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM job_outbox WHERE unique_key=?", "translate:content:collection_item:"+id+":"+strconv.FormatInt(appID, 10)).Scan(&residue).Error)
	require.Zero(t, residue)
	callAdmin("PUT", "/collections/"+id+"/items", items, "0000")
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM collection_item_i18n WHERE collection_id=? AND package_id=?", id, appID).Scan(&residue).Error)
	require.EqualValues(t, 1, residue)
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM collection_item_i18n WHERE collection_id=? AND package_id=? AND status='pending' AND source_hash IS NOT NULL", id, appID).Scan(&residue).Error)
	require.EqualValues(t, 0, residue)
	collectionID, err := strconv.ParseInt(id, 10, 64)
	require.NoError(t, err)
	itemRef := content.Ref{Entity: "collection_item", ID: collectionID, SecondaryID: appID}
	itemSource, err := st.ReadContentSource(ctx, itemRef)
	require.NoError(t, err)
	require.Len(t, itemSource.Targets, 9)
	// Support orphaned hashes left by older implementations: saving identical source text also repairs missing target rows.
	require.NoError(t, st.DB.Exec("DELETE FROM collection_items WHERE collection_id=? AND package_id=?", id, appID).Error)
	callAdmin("PUT", "/collections/"+id+"/items", items, "0000")
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM collection_item_i18n WHERE collection_id=? AND package_id=?", id, appID).Scan(&residue).Error)
	require.EqualValues(t, 1, residue)
	// Unchanged saves and reordering must not cascade-delete completed translations or reset translation metadata.
	for _, locale := range []string{"en-US", "ja-JP", "es-ES", "pt-BR", "ru-RU"} {
		require.NoError(t, st.DB.Exec(`INSERT INTO collection_item_i18n(note,collection_id,package_id,locale,source_locale,status,model,translated_at) VALUES(?,?,?,?,'zh-CN','machine','fixture',now())`, "Translated "+locale, id, appID, locale).Error)
	}
	var translationsBefore, translationsAfter string
	readTranslations := func(target *string) {
		t.Helper()
		require.NoError(t, st.DB.Raw(`SELECT jsonb_agg(to_jsonb(i) ORDER BY locale)::text FROM collection_item_i18n i WHERE collection_id=? AND package_id=? AND locale<>'zh-CN'`, id, appID).Scan(target).Error)
	}
	readTranslations(&translationsBefore)
	callAdmin("PUT", "/collections/"+id+"/items", items, "0000")
	readTranslations(&translationsAfter)
	require.Equal(t, translationsBefore, translationsAfter)
	var languageCount int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM collection_item_i18n WHERE collection_id=? AND package_id=?", id, appID).Scan(&languageCount).Error)
	require.EqualValues(t, 6, languageCount)
	reordered := `{"items":[{"packageId":` + strconv.FormatInt(cliID, 10) + `},{"packageId":` + strconv.FormatInt(appID, 10) + `}]}`
	callAdmin("PUT", "/collections/"+id+"/items", reordered, "0000")
	readTranslations(&translationsAfter)
	require.Equal(t, translationsBefore, translationsAfter)
	var itemSort int
	require.NoError(t, st.DB.Raw("SELECT sort FROM collection_items WHERE collection_id=? AND package_id=?", id, appID).Scan(&itemSort).Error)
	require.Equal(t, 1, itemSort)
	callAdmin("PUT", "/collections/"+id+"/items", items, "0000")
	callAdmin("PUT", "/collections/"+id+"/items", `{"items":[{"packageId":`+strconv.FormatInt(appID, 10)+`},{"packageId":`+strconv.FormatInt(appID, 10)+`}]}`, "1001")
	callAdmin("POST", "/collections/"+id+"/publish", `{"publishAt":"2026-10-05T04:00:00Z"}`, "0000")
	_, policyErr := publicSvc.ContentCachePolicy(ctx)
	require.NoError(t, policyErr)
	callPublic("/collections/essentials", 404)
	require.Contains(t, callPublic("/collections", 200).Body.String(), `"total":0`)
	featureBody := `{"placement":"home_hero","targetType":"collection","collectionId":` + id + `,"status":"scheduled","sort":1,"startsAt":"2026-10-05T04:00:00Z","endsAt":"2026-10-05T05:00:00Z","sourceLocale":"zh-CN","i18n":{"zh-CN":{"title":"本周精选","body":"实用 **工具**"}}}`
	fid := idFrom(callAdmin("POST", "/features", featureBody, "0000"))
	callAdmin("GET", "/features", "", "0000")
	callAdmin("GET", "/features/"+fid, "", "0000")
	callAdmin("PUT", "/features/"+fid, featureBody, "0000")
	require.NotContains(t, callPublic("/home", 200).Body.String(), "本周精选")
	now = time.Date(2026, 10, 5, 3, 59, 59, 0, time.UTC)
	require.Contains(t, callPublic("/home", 200).Header().Get("Cache-Control"), "max-age=1")
	now = now.Add(time.Second)
	detail := callPublic("/collections/essentials?locale=en-US", 200)
	require.Contains(t, detail.Body.String(), "Essentials")
	require.Contains(t, detail.Body.String(), `"items":[`)
	var collection struct{ Data publicapi.CollectionDetail }
	require.NoError(t, json.Unmarshal(detail.Body.Bytes(), &collection))
	require.NotNil(t, collection.Data.PreviewItems)
	preview := *collection.Data.PreviewItems
	require.Len(t, preview, 1)
	require.Equal(t, "editor", preview[0].Token)
	require.Equal(t, "Editor", preview[0].DisplayName)
	require.Equal(t, publicapi.PackageKindCask, preview[0].Kind)
	require.NotContains(t, detail.Body.String(), "ripgrep")
	for i, item := range preview {
		require.Equal(t, collection.Data.IconUrls[i], item.IconUrl)
		require.Nil(t, item.IconUrl)
		require.Nil(t, item.AccentColor)
	}
	for _, path := range []string{"/collections", "/home"} {
		require.Contains(t, callPublic(path, 200).Body.String(), `"previewItems":[`)
	}
	require.Contains(t, callPublic("/home", 200).Body.String(), "本周精选")
	require.Equal(t, "cask \"editor\"\n", callPublic("/collections/essentials/brewfile", 200).Body.String())
	callAdmin("PUT", "/collections/"+id, `{"slug":"essentials","sort":2,"unpublishAt":"2026-10-05T05:00:00Z","sourceLocale":"zh-CN","i18n":{"zh-CN":{"title":"修改后"},"en-US":null}}`, "0000")
	require.Contains(t, callPublic("/collections/essentials?locale=en-US", 200).Body.String(), "修改后")
	now = time.Date(2026, 10, 5, 5, 0, 0, 0, time.UTC)
	callPublic("/collections/essentials", 404)
	require.NotContains(t, callPublic("/home", 200).Body.String(), "本周精选")
	callAdmin("POST", "/collections/"+id+"/unpublish", "", "0000")
	callAdmin("DELETE", "/features/"+fid, "", "0000")
	callAdmin("DELETE", "/collections/"+id, "", "0000")
	var audited int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT count(*) FROM audit_logs WHERE entity_type IN('collection','feature')").Scan(&audited).Error)
	// Six additional saves cover unchanged content, reordering, restoring order, deletion/re-addition and legacy orphaned-hash repair.
	require.Equal(t, int64(15), audited)
}
