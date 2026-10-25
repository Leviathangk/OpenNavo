package middleware

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/stretchr/testify/require"
)

func TestCatalogQueryValidation(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	validate, err := Validate(spec, "/agent-api", true)
	require.NoError(t, err)
	router := gin.New()
	router.Use(RequestID())
	router.GET("/agent-api/catalog/changes", validate, func(c *gin.Context) { c.String(200, "passed") })
	for _, tc := range []struct {
		query string
		pass  bool
	}{
		{"", true},
		{"?from=2026-10-25T00:00:00Z&to=2026-10-25T01:00:00Z", true},
		{"?from=&to=", false},
		{"?from=private-invalid-date", false},
	} {
		logs.Reset()
		req := httptest.NewRequest("GET", "/agent-api/catalog/changes"+tc.query, nil)
		req.Header.Set("X-Request-ID", "catalog-test")
		req.Header.Set("Authorization", "Bearer private-token")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		if tc.pass {
			require.Equal(t, "passed", recorder.Body.String())
			require.Empty(t, logs.String())
		} else {
			require.Contains(t, recorder.Body.String(), `"code":"1001"`)
			require.Contains(t, recorder.Body.String(), "parameter_invalid:from")
			require.Contains(t, logs.String(), `"field":"from"`)
			require.Contains(t, logs.String(), `"requestId":"catalog-test"`)
			require.NotContains(t, logs.String(), "private-")
			require.NotContains(t, recorder.Body.String(), "private-")
		}
	}
}
