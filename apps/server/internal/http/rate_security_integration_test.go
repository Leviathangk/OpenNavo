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

	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/http/middleware"
	"github.com/opennavo/opennavo/server/internal/http/respond"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestPublicRateScopesAndForwardedIPCannotBypassQuota(t *testing.T) {
	st := testutil.NewStore(t)
	engine := gin.New()
	require.NoError(t, engine.SetTrustedProxies(nil))
	engine.Use(middleware.RateLimit(middleware.RateLimiter{Client: st.Redis}))
	for _, path := range []string{"/api/v1/search", "/api/v1/search/suggest", "/api/v1/feedback", "/api/v1/sitemap/packages"} {
		engine.Any(path, func(c *gin.Context) { respond.OK(c, gin.H{}) })
	}
	for _, sample := range []struct {
		path  string
		quota int
		scope string
	}{{"/api/v1/search", 60, "search"}, {"/api/v1/search/suggest", 120, "suggest"}, {"/api/v1/feedback", 5, "feedback"}, {"/api/v1/sitemap/packages", 30, "sitemap"}} {
		for index := 0; index <= sample.quota; index++ {
			if index == sample.quota {
				// Fix the quota at exhausted to prevent slow CI from replenishing it between iterations and making assertions speed-dependent.
				require.NoError(t, st.Redis.Set(context.Background(), "rate:"+sample.scope+":192.0.2.1", time.Now().Add(time.Hour).UnixMicro(), time.Hour).Err())
			}
			r := httptest.NewRequest("POST", sample.path, nil)
			r.Header.Set("X-Forwarded-For", "198.51.100."+strconv.Itoa(index+1))
			r.Header.Set("X-Real-IP", "203.0.113."+strconv.Itoa(index+1))
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, r)
			require.Equal(t, strconv.Itoa(sample.quota), w.Header().Get("X-RateLimit-Limit"))
			if index < sample.quota {
				require.Equal(t, 200, w.Code)
				continue
			}
			require.Equal(t, 429, w.Code)
			require.Equal(t, "0", w.Header().Get("X-RateLimit-Remaining"))
			require.NotEmpty(t, w.Header().Get("Retry-After"))
			var envelope struct{ Code string }
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
			require.Equal(t, domain.CodeRateLimited, envelope.Code)
		}
	}
}

func TestClientsBehindCaddyHaveIndependentRateQuotas(t *testing.T) {
	st := testutil.NewStore(t)
	cfg := config.Config{TrustedProxies: []string{"172.30.80.2", "172.30.81.0/24"}, JWTSecret: strings.Repeat("a", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour}
	svc, err := auth.New(st, cfg)
	require.NoError(t, err)
	server, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: &admin.Handler{Auth: svc}, Rate: &middleware.RateLimiter{Client: st.Redis}})
	require.NoError(t, err)
	for _, sample := range []struct{ path, method, body, scope string }{
		{"/api/v1/search?q=vscode", "GET", "", "search"},
		{"/api/v1/feedback", "POST", "{}", "feedback"},
		{"/admin-api/auth/login", "POST", `{"userName":"unknown","password":"incorrect-password"}`, "admin:login"},
	} {
		require.NoError(t, st.Redis.Set(context.Background(), "rate:"+sample.scope+":203.0.113.1", time.Now().Add(time.Hour).UnixMicro(), time.Hour).Err())
		for _, peer := range []string{"172.30.80.2:1234", "172.30.81.3:1234"} {
			for _, client := range []string{"203.0.113.1", "203.0.113.2"} {
				r := httptest.NewRequest(sample.method, sample.path, strings.NewReader(sample.body))
				r.RemoteAddr = peer
				r.Header.Set("X-Forwarded-For", "198.51.100.5, "+client)
				if sample.body != "" {
					r.Header.Set("Content-Type", "application/json")
				}
				w := httptest.NewRecorder()
				server.Engine.ServeHTTP(w, r)
				var envelope struct{ Code string }
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
				if client == "203.0.113.1" {
					require.Equal(t, domain.CodeRateLimited, envelope.Code)
				} else {
					require.NotEqual(t, domain.CodeRateLimited, envelope.Code)
				}
			}
		}
	}
}
