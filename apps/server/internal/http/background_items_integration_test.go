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
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestBackgroundItemHTTPRoutesAndErrorContracts(t *testing.T) {
	st := testutil.NewFactsStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"background-http","name":["Fixture"],"version":"1","artifacts":[{"uninstall":[{"launchctl":"org.example.background-http"}]}]}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(context.Background(), []store.CatalogMutation{{Package: p}}))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, err := httpserver.New(config.Config{}, logger, st, httpserver.Dependencies{Public: &public.Handler{Catalog: &catalog.PublicService{Store: st}}})
	require.NoError(t, err)
	placeholder, err := httpserver.New(config.Config{}, logger, st)
	require.NoError(t, err)
	spec, err := publicapi.GetSpec()
	require.NoError(t, err)
	for _, tc := range []struct {
		path   string
		status int
		code   string
	}{
		{"/background-items", 200, domain.CodeOK},
		{"/background-items/org.example.background-http", 200, domain.CodeOK},
		{"/background-items/org.example.missing", 404, domain.CodeNotFound},
		{"/background-items?current=0", 400, domain.CodeValidation},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/v1"+tc.path, nil)
			w := httptest.NewRecorder()
			server.Engine.ServeHTTP(w, r)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			var body struct {
				Code string
				Data json.RawMessage
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.Equal(t, tc.code, body.Code)
			if tc.status == 200 {
				require.Contains(t, string(body.Data), "org.example.background-http")
			} else {
				require.JSONEq(t, "null", string(body.Data))
			}
			testutil.ValidateResponse(t, spec, "/api/v1", r, w)
			if tc.status == 200 {
				w = httptest.NewRecorder()
				placeholder.Engine.ServeHTTP(w, r)
				require.Equal(t, 500, w.Code, "registered route without catalog must fail internally, not 404")
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				require.Equal(t, domain.CodeInternal, body.Code)
				require.JSONEq(t, "null", string(body.Data))
				testutil.ValidateResponse(t, spec, "/api/v1", r, w)
			}
		})
	}
}
