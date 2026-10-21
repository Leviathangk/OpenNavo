package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/stretchr/testify/require"
)

func headerHas(header http.Header, name, value string) bool {
	for _, token := range strings.Split(header.Get(name), ",") {
		if strings.EqualFold(strings.TrimSpace(token), value) {
			return true
		}
	}
	return false
}
func TestCORSOriginsPreflightVaryAndAdminIsolation(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	for _, environment := range []string{"dev", "staging", "prod"} {
		t.Run(environment, func(t *testing.T) {
			cfg := config.Config{AppEnv: environment, WebBaseURL: "https://opennavo.example", AdminOrigin: "https://admin.opennavo.example", PublicCORSOrigins: []string{"https://preview.opennavo.example"}}
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Header("Vary", "Accept-Encoding"); c.Next() }, RequestID(), CORS(cfg.PublicOrigins(), cfg.AdminOrigin))
			router.Group("/api/v1", PublicCacheHeaders()).GET("/home", func(c *gin.Context) { c.JSON(200, gin.H{"code": domain.CodeOK, "msg": "ok", "data": gin.H{}}) })
			router.GET("/admin-api/auth/getUserInfo", func(c *gin.Context) { c.JSON(200, gin.H{"code": domain.CodeOK}) })
			call := func(method, path, origin, etag string) *httptest.ResponseRecorder {
				request := httptest.NewRequest(method, path, nil)
				request.Header.Set("Origin", origin)
				request.Header.Set("X-Request-Id", "cors-client-1")
				if etag != "" {
					request.Header.Set("If-None-Match", etag)
				}
				if method == http.MethodOptions {
					request.Header.Set("Access-Control-Request-Method", "GET")
					request.Header.Set("Access-Control-Request-Headers", "accept-language,x-client-platform,x-client-version,x-request-id")
				}
				reply := httptest.NewRecorder()
				router.ServeHTTP(reply, request)
				require.True(t, headerHas(reply.Header(), "Vary", "Accept-Encoding"))
				require.True(t, headerHas(reply.Header(), "Vary", "Origin"))
				require.Equal(t, "cors-client-1", reply.Header().Get("X-Request-Id"))
				return reply
			}
			spoofed := []string{"https://foreign.example", "null", "https://opennavo.example.evil.example", "https://opennavo.example:8443", "https://opennavo.example/", "https://opennavo.example@evil.example"}
			for _, origin := range append(append(cfg.PublicOrigins(), spoofed...), "http://localhost:3000", "http://localhost:1420") {
				allowed := !slices.Contains(spoofed, origin) && (environment == "dev" || (origin != "http://localhost:3000" && origin != "http://localhost:1420"))
				preflight := call("OPTIONS", "/api/v1/home", origin, "")
				if !allowed {
					require.Equal(t, http.StatusForbidden, preflight.Code)
					require.Empty(t, preflight.Header().Get("Access-Control-Allow-Origin"))
					require.Empty(t, preflight.Header().Get("Access-Control-Max-Age"))
					continue
				}
				require.Equal(t, http.StatusNoContent, preflight.Code)
				require.Equal(t, origin, preflight.Header().Get("Access-Control-Allow-Origin"))
				// Allowed preflights are cached by the browser, sparing most API calls a round trip.
				require.Equal(t, "86400", preflight.Header().Get("Access-Control-Max-Age"))
				for _, name := range []string{"Accept-Language", "X-Client-Platform", "X-Client-Version", "X-Request-Id", "If-None-Match"} {
					require.True(t, headerHas(preflight.Header(), "Access-Control-Allow-Headers", name), name)
				}
				for _, name := range []string{"X-Request-Id", "Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining", "ETag", "X-Usage-Day"} {
					require.True(t, headerHas(preflight.Header(), "Access-Control-Expose-Headers", name), name)
				}
				first := call("GET", "/api/v1/home", origin, "")
				require.Equal(t, http.StatusOK, first.Code)
				require.True(t, headerHas(first.Header(), "Vary", "Accept-Language"))
				require.Empty(t, first.Header().Get("Access-Control-Max-Age"), "only preflights are cached")
				cached := call("GET", "/api/v1/home", origin, first.Header().Get("ETag"))
				require.Equal(t, http.StatusNotModified, cached.Code)
				require.Empty(t, cached.Body.String())
				require.Equal(t, origin, cached.Header().Get("Access-Control-Allow-Origin"))
				require.True(t, headerHas(cached.Header(), "Access-Control-Expose-Headers", "Retry-After"))
				admin := call("GET", "/admin-api/auth/getUserInfo", origin, "")
				require.Equal(t, http.StatusOK, admin.Code)
				var body map[string]any
				require.NoError(t, json.Unmarshal(admin.Body.Bytes(), &body))
				require.Equal(t, domain.CodeForbidden, body["code"], "public origins must not enter admin APIs")
				require.Empty(t, admin.Header().Get("Access-Control-Allow-Origin"))
			}
			admin := call("OPTIONS", "/admin-api/auth/getUserInfo", cfg.AdminOrigin, "")
			require.Equal(t, http.StatusNoContent, admin.Code)
			require.Equal(t, cfg.AdminOrigin, admin.Header().Get("Access-Control-Allow-Origin"))
			require.Equal(t, "86400", admin.Header().Get("Access-Control-Max-Age"))
			withoutOrigin := call("GET", "/api/v1/home", "", "")
			require.Equal(t, http.StatusOK, withoutOrigin.Code)
			require.Empty(t, withoutOrigin.Header().Get("Access-Control-Allow-Origin"))
		})
	}
}

func TestInvalidRateLimitRejectsBeforeIO(t *testing.T) {
	for _, input := range []struct {
		count  int
		window time.Duration
	}{{0, time.Second}, {1, 0}, {2, time.Microsecond}} {
		_, err := (RateLimiter{}).Check(context.Background(), "invalid", input.count, input.window)
		require.ErrorContains(t, err, "invalid rate limit configuration")
	}
}
