//go:build integration

package httpserver_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/middleware"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestUsageEndpointValidationAndRateLimit(t *testing.T) {
	st := testutil.NewStore(t)
	server, err := httpserver.New(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Public: &public.Handler{Catalog: &catalog.PublicService{Store: st}}, Rate: &middleware.RateLimiter{Client: st.Redis}})
	require.NoError(t, err)
	valid := map[string]any{"anonymousId": "2a77e45e-1ac2-4d72-b9c2-aaf2a42f8234", "platform": "desktop", "occurredAt": time.Now().UTC().Format(time.RFC3339), "version": "0.1.3", "locale": "en-US"}
	call := func(body map[string]any) *httptest.ResponseRecorder {
		raw, e := json.Marshal(body)
		require.NoError(t, e)
		req := httptest.NewRequest("POST", "/api/v1/usage/activity", strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		reply := httptest.NewRecorder()
		server.Engine.ServeHTTP(reply, req)
		return reply
	}
	reply := call(valid)
	require.Equal(t, 204, reply.Code)
	require.Equal(t, "no-store", reply.Header().Get("Cache-Control"))
	requested, err := time.Parse(time.RFC3339, valid["occurredAt"].(string))
	require.NoError(t, err)
	require.Equal(t, requested.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02"), reply.Header().Get("X-Usage-Day"))

	// The website's collection switch is independent of GA4 and enforced server-side.
	valid["platform"] = "web"
	require.Equal(t, 204, call(valid).Code)
	var webRows int64
	require.NoError(t, st.DB.Table("usage_daily").Where("platform='web'").Count(&webRows).Error)
	require.Zero(t, webRows)
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{analytics}',COALESCE(settings->'analytics','{}'::jsonb)||'{"usageEnabled":true}'::jsonb) WHERE id=true`).Error)
	require.Equal(t, 204, call(valid).Code)
	require.NoError(t, st.DB.Table("usage_daily").Where("platform='web'").Count(&webRows).Error)
	require.EqualValues(t, 1, webRows)
	valid["platform"] = "desktop"
	valid["installedApps"] = []string{"must-not-be-collected"}
	require.Equal(t, 400, call(valid).Code)
	delete(valid, "installedApps")
	valid["occurredAt"] = time.Now().Add(-8 * 24 * time.Hour).Format(time.RFC3339)
	require.Equal(t, 400, call(valid).Code)
	valid["occurredAt"] = time.Now().UTC().Format(time.RFC3339)
	for range 60 {
		reply = call(valid)
	}
	require.Equal(t, 429, reply.Code)
	var count int64
	require.NoError(t, st.DB.Table("usage_daily").Count(&count).Error)
	require.EqualValues(t, 2, count)
}
