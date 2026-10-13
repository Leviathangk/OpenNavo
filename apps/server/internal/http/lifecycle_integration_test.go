//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

func TestPackageLifecycleRenamesRemovalAndRestore(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "", ""))
	apply := func(raw string, previous *store.CatalogRow, renamedFrom string) {
		t.Helper()
		p, err := homebrew.Normalize("cask", json.RawMessage(raw))
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p, Previous: previous, RenamedFrom: renamedFrom}}))
	}
	rows := func() map[string]*store.CatalogRow {
		t.Helper()
		out, err := st.CatalogRows(ctx, "cask")
		require.NoError(t, err)
		return out
	}
	// Homebrew declares a former token; OpenNavo never saw it.
	apply(`{"token":"new-name","name":["Editor"],"version":"1","desc":"Edits text","old_tokens":["old-name"]}`, nil, "")
	apply(`{"token":"other-app","name":["Other"],"version":"3","desc":"Edits text too"}`, nil, "")
	apply(`{"token":"gone-app","name":["Gone"],"version":"2.4.1","desc":"Archived utility","disabled":true,"disable_date":"2026-01-01","disable_replacement_cask":"other-app"}`, nil, "")
	for _, sql := range []string{
		`UPDATE packages SET disable_replacement='cask:other-app' WHERE token='gone-app'`,
		`INSERT INTO package_categories(package_id,category_id,is_primary,source) SELECT p.id,c.id,true,'human' FROM packages p CROSS JOIN categories c WHERE p.token IN ('gone-app','other-app','new-name') AND c.slug='productivity'`,
		`UPDATE packages SET popularity=5 WHERE token='new-name'`,
	} {
		require.NoError(t, st.DB.WithContext(ctx).Exec(sql).Error, sql)
	}
	// A second rename observed by sync: new-name becomes newest-name.
	apply(`{"token":"newest-name","name":["Editor"],"version":"2","desc":"Edits text","old_tokens":["new-name"]}`, rows()["new-name"], "new-name")
	require.NoError(t, st.RemoveCatalogRows(ctx, []*store.CatalogRow{rows()["gone-app"]}))

	app, err := httpserver.New(config.Config{WebBaseURL: "http://localhost:3000"}, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{
		Public: &public.Handler{Catalog: &catalog.PublicService{Store: st, WebBaseURL: "http://localhost:3000"}},
	})
	require.NoError(t, err)
	spec, err := publicapi.GetSpec()
	require.NoError(t, err)
	get := func(path string, status int) publicapi.PackageLifecycle {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1"+path, nil)
		rec := httptest.NewRecorder()
		app.Engine.ServeHTTP(rec, req)
		require.Equal(t, status, rec.Code, rec.Body.String())
		testutil.ValidateResponse(t, spec, "/api/v1", req, rec)
		var envelope struct{ Data publicapi.PackageLifecycle }
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
		return envelope.Data
	}

	// Both former tokens redirect in one step to the current token.
	for _, token := range []string{"old-name", "new-name"} {
		life := get("/packages/cask/"+token+"/lifecycle", 200)
		require.Equal(t, publicapi.Renamed, life.State, token)
		require.Equal(t, "newest-name", life.Token, token)
	}
	require.Equal(t, publicapi.Active, get("/packages/cask/newest-name/lifecycle", 200).State)
	get("/packages/cask/never-existed/lifecycle", 404)

	// Removed apps answer with a summary, the named replacement and alternatives.
	get("/packages/cask/gone-app", 404)
	gone := get("/packages/cask/gone-app/lifecycle?locale=en-US", 200)
	require.Equal(t, publicapi.Removed, gone.State)
	require.Equal(t, "2.4.1", gone.Removed.LastVersion)
	require.Equal(t, "Archived utility", *gone.Removed.Summary)
	require.Equal(t, "other-app", gone.Removed.Replacement.Token)
	require.Len(t, gone.Removed.Alternatives, 1)
	require.Equal(t, "newest-name", gone.Removed.Alternatives[0].Token)
	require.Equal(t, "productivity", gone.Removed.Categories[0].Slug)

	// Homebrew adding it back restores the page; a reused token beats the alias.
	apply(`{"token":"gone-app","name":["Gone"],"version":"2.5","desc":"Archived utility"}`, rows()["gone-app"], "")
	require.Equal(t, publicapi.Active, get("/packages/cask/gone-app/lifecycle", 200).State)
	get("/packages/cask/gone-app", 200)
	apply(`{"token":"old-name","name":["Unrelated"],"version":"1","desc":"A different app"}`, nil, "")
	life := get("/packages/cask/old-name/lifecycle", 200)
	require.Equal(t, publicapi.Active, life.State)
	require.Equal(t, "old-name", life.Token)
}
