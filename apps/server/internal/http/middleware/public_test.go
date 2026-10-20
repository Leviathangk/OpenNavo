package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPublicCacheHeadersConditionalResponsesAndVary(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(PublicCacheHeaders())
	router.GET("/api/v1/home", func(c *gin.Context) { c.Header("Vary", "Origin"); c.JSON(200, gin.H{"data": "test"}) })
	router.GET("/api/v1/search", func(c *gin.Context) { c.JSON(200, gin.H{}) })
	router.GET("/api/v1/catalog/changes", func(c *gin.Context) { c.JSON(200, gin.H{}) })
	router.GET("/api/v1/config/client", func(c *gin.Context) { c.JSON(200, gin.H{}) })
	router.GET("/api/v1/catalog/links", func(c *gin.Context) { c.JSON(200, gin.H{}) })
	router.GET("/error", func(c *gin.Context) { c.JSON(500, gin.H{}) })
	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest("GET", "/api/v1/home", nil))
	require.Equal(t, 200, first.Code)
	require.Equal(t, "Origin, Accept-Language", first.Header().Get("Vary"))
	etag := first.Header().Get("ETag")
	require.NotEmpty(t, etag)
	for _, value := range []string{etag, etag[2:], `"unmatched", ` + etag, "*"} {
		request := httptest.NewRequest("GET", "/api/v1/home", nil)
		request.Header.Set("If-None-Match", value)
		reply := httptest.NewRecorder()
		router.ServeHTTP(reply, request)
		require.Equal(t, http.StatusNotModified, reply.Code)
		require.Empty(t, reply.Body.String())
	}
	for _, example := range []struct{ path, policy string }{{"/api/v1/search", "public, max-age=30"}, {"/api/v1/catalog/changes", "public, max-age=60"}, {"/api/v1/config/client", "public, max-age=300"}, {"/error", "no-store"}, {"/api/v1/catalog/links", "public, max-age=60, s-maxage=300, stale-while-revalidate=600, no-transform"}} {
		reply := httptest.NewRecorder()
		router.ServeHTTP(reply, httptest.NewRequest("GET", example.path, nil))
		require.Equal(t, example.policy, reply.Header().Get("Cache-Control"))
	}
}
