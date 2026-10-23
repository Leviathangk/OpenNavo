package httpserver_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/middleware"
	"github.com/stretchr/testify/require"
)

type countedBody struct {
	io.Reader
	read int
}

func (r *countedBody) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += n
	return n, err
}

func TestPublicRequestResourceAndContractBounds(t *testing.T) {
	server, err := httpserver.New(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	require.NoError(t, err)
	for _, test := range []struct{ path, body string }{
		{"/search?q=" + url.QueryEscape(strings.Repeat("中", 65)), ""},
		{"/search/suggest?q=test&limit=11", ""},
		{"/packages?size=101", ""},
		{"/packages/cask/" + strings.Repeat("a", 129), ""},
		{"/packages/cask/test/dependencies?depth=4", ""},
		{"/packages/cask/test/related?limit=13", ""},
		{"/catalog/changes?since=0&limit=1001", ""},
		{"/sitemap/packages?size=501", ""},
		{"/feedback", `{"type":"other","platform":"web","content":"` + strings.Repeat("中", 2001) + `"}`},
		{"/feedback", `{"type":"other","platform":"web","content":"有效反馈内容","contact":"` + strings.Repeat("a", 201) + `"}`},
		{"/packages/lookup", `{"items":[` + strings.Repeat(`{"kind":"cask","token":"test"},`, 500) + `{"kind":"cask","token":"test"}]}`},
		{"/home?" + strings.Repeat("x=1&", middleware.QueryPairLimit), ""},
		{"/home?x=" + strings.Repeat("a", middleware.RequestTargetLimit), ""},
	} {
		t.Run(test.path[:min(70, len(test.path))], func(t *testing.T) {
			method := "GET"
			if test.body != "" {
				method = "POST"
			}
			request := httptest.NewRequest(method, "/api/v1"+test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			reply := httptest.NewRecorder()
			server.Engine.ServeHTTP(reply, request)
			require.Equal(t, 400, reply.Code)
			var envelope struct{ Code string }
			require.NoError(t, json.Unmarshal(reply.Body.Bytes(), &envelope))
			require.Equal(t, domain.CodeValidation, envelope.Code)
		})
	}
	for _, declared := range []int64{2 * middleware.JSONBodyLimit, -1, 20} {
		body := &countedBody{Reader: strings.NewReader(`{"type":"other","platform":"web","content":"有效反馈内容","website":"` + strings.Repeat("a", 2*middleware.JSONBodyLimit) + `"}`)}
		request := httptest.NewRequest("POST", "/api/v1/feedback", nil)
		request.Header.Set("Content-Type", "application/json")
		request.Body, request.ContentLength = io.NopCloser(body), declared
		reply := httptest.NewRecorder()
		server.Engine.ServeHTTP(reply, request)
		require.Equal(t, 400, reply.Code)
		require.LessOrEqual(t, body.read, middleware.JSONBodyLimit+1)
		if declared > middleware.JSONBodyLimit {
			require.Zero(t, body.read)
		}
	}
}
