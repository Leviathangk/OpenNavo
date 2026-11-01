//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/middleware"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// The public spec was served straight from the base engine, so it alone had no quota and was the
// cheapest route on the API host to spend bandwidth. It now runs the public limiter.
func TestPublicSpecRouteSharesThePublicRateLimit(t *testing.T) {
	st := testutil.NewStore(t)
	server, err := httpserver.New(config.Config{WebBaseURL: "http://localhost:3000"}, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Rate: &middleware.RateLimiter{Client: st.Redis}})
	require.NoError(t, err)
	// Exhaust the public scope for this client so the route's own limiter would let it through.
	require.NoError(t, st.Redis.Set(context.Background(), "rate:public:203.0.113.9", time.Now().Add(time.Hour).UnixMicro(), time.Hour).Err())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.yaml", nil)
	request.RemoteAddr = "203.0.113.9:1234"
	reply := httptest.NewRecorder()
	server.Engine.ServeHTTP(reply, request)
	require.Equal(t, http.StatusTooManyRequests, reply.Code)
	require.Equal(t, "300", reply.Header().Get("X-RateLimit-Limit"))
	require.Equal(t, "0", reply.Header().Get("X-RateLimit-Remaining"))
	require.NotEmpty(t, reply.Header().Get("Retry-After"))
	var envelope struct{ Code string }
	require.NoError(t, json.Unmarshal(reply.Body.Bytes(), &envelope))
	require.Equal(t, domain.CodeRateLimited, envelope.Code)
	require.NotContains(t, reply.Body.String(), "openapi: 3.0.3")

	// Another client still gets the document, with the quota headers and the API's cache policy.
	request = httptest.NewRequest(http.MethodGet, "/api/v1/openapi.yaml", nil)
	request.RemoteAddr = "203.0.113.10:1234"
	reply = httptest.NewRecorder()
	server.Engine.ServeHTTP(reply, request)
	require.Equal(t, http.StatusOK, reply.Code)
	require.Equal(t, "300", reply.Header().Get("X-RateLimit-Limit"))
	require.Equal(t, strconv.Itoa(299), reply.Header().Get("X-RateLimit-Remaining"))
	require.Equal(t, "public, max-age=3600", reply.Header().Get("Cache-Control"))
	require.Equal(t, "application/yaml; charset=utf-8", reply.Header().Get("Content-Type"))
}
