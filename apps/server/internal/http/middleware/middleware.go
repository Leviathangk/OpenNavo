package middleware

import (
	"crypto/rand"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/http/respond"
)

var requestIDPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if !requestIDPattern.MatchString(id) {
			id = rand.Text()
		}
		c.Set("requestId", id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}
func Logger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		logger.InfoContext(c.Request.Context(), "http request", "requestId", c.GetString("requestId"), "method", c.Request.Method, "route", route, "status", c.Writer.Status(), "latencyMs", float64(time.Since(start).Microseconds())/1000, "userId", c.GetInt64("userId"))
	}
}
func Recovery(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recover() != nil {
				logger.ErrorContext(c.Request.Context(), "http panic recovered", "requestId", c.GetString("requestId"))
				respond.Error(c, domain.Internal(nil), (strings.HasPrefix(c.Request.URL.Path, "/admin-api/") || strings.HasPrefix(c.Request.URL.Path, "/agent-api/")))
			}
		}()
		c.Next()
	}
}
func CORS(publicOrigins []string, adminOrigins ...string) gin.HandlerFunc {
	allowedPublic := make(map[string]bool, len(publicOrigins))
	for _, origin := range publicOrigins {
		allowedPublic[origin] = true
	}
	allowedAdmin := make(map[string]bool, len(adminOrigins))
	for _, origin := range adminOrigins {
		allowedAdmin[origin] = true
	}
	return func(c *gin.Context) {
		// Declare this dimension even for SSR requests without Origin, preventing CDN reuse of cached responses lacking CORS headers.
		appendVary(c.Writer.Header(), "Origin")
		appendVary(c.Writer.Header(), "Accept-Language")
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}
		allowed := allowedAdmin[origin]
		admin := (strings.HasPrefix(c.Request.URL.Path, "/admin-api/") || strings.HasPrefix(c.Request.URL.Path, "/agent-api/"))
		if !admin {
			allowed = allowedPublic[origin]
		}
		if !allowed {
			respond.Error(c, &domain.AppError{Code: domain.CodeForbidden, HTTPStatus: http.StatusForbidden}, admin)
			return
		}
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, Accept-Language, X-Request-Id, X-Client-Platform, X-Client-Version, If-None-Match, X-OpenNavo-Caps")
		c.Header("Access-Control-Expose-Headers", "X-Request-Id, ETag, Retry-After, X-RateLimit-Limit, X-RateLimit-Remaining")
		if c.Request.Method == http.MethodOptions {
			// Browsers reuse an allowed preflight up to their own cap: a day in Firefox, 2 hours in Chromium,
			// 10 minutes in WebKit.
			c.Header("Access-Control-Max-Age", "86400")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func Validate(spec *openapi3.T, basePath string, admin bool) (gin.HandlerFunc, error) {
	spec.Servers = openapi3.Servers{&openapi3.Server{URL: basePath}}
	router, err := legacy.NewRouter(spec)
	if err != nil {
		return nil, err
	}
	return func(c *gin.Context) {
		route, params, err := router.FindRoute(c.Request)
		// The legacy OpenAPI router matches URL.String(), so its path values
		// are escaped. Decode once to validate the same values Gin dispatches.
		if err == nil {
			for name, value := range params {
				params[name], err = url.PathUnescape(value)
				if err != nil {
					break
				}
			}
		}
		if err == nil {
			err = openapi3filter.ValidateRequest(c.Request.Context(), &openapi3filter.RequestValidationInput{Request: c.Request, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}})
		}
		if err != nil {
			respond.Error(c, domain.Validation(), admin)
			return
		}
		c.Next()
	}, nil
}
