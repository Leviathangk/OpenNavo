package middleware

import (
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
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
		// OAuth endpoints apply their own public-client CORS and consent-origin policy.
		if strings.HasPrefix(c.Request.URL.Path, "/oauth/") || strings.HasPrefix(c.Request.URL.Path, "/.well-known/oauth-") {
			c.Next()
			return
		}
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
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, Accept-Language, X-Request-Id, X-Client-Platform, X-Client-Version, If-None-Match, X-OpenNavo-Caps, X-Expected-Revision, Idempotency-Key")
		c.Header("Access-Control-Expose-Headers", "X-Request-Id, ETag, Retry-After, X-RateLimit-Limit, X-RateLimit-Remaining, X-Usage-Day")
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
			if c.FullPath() == basePath+"/catalog/changes" {
				field, rule := "request", "invalid"
				var requestErr *openapi3filter.RequestError
				var schemaErr *openapi3.SchemaError
				if errors.As(err, &requestErr) && requestErr.Parameter != nil {
					field = requestErr.Parameter.Name // contract name, never caller input
					rule = "parameter_invalid"
					if values, ok := c.Request.URL.Query()[field]; ok && len(values) == 1 && values[0] == "" {
						rule = "empty_parameter"
					}
				}
				if errors.As(err, &schemaErr) {
					rule = schemaErr.SchemaField
				}
				slog.WarnContext(c.Request.Context(), "catalog request validation failed",
					"requestId", c.GetString("requestId"), "route", c.FullPath(), "field", field, "rule", rule)
				respond.Error(c, domain.Rejected("parameter_invalid:"+field), admin)
				return
			}
			if !admin {
				respond.Error(c, domain.Validation(), admin)
				return
			}
			respond.Error(c, requestReason(err), admin)
			return
		}
		c.Next()
	}, nil
}

// requestReason names what the contract refused for admin and Agent callers:
// the parameter, or the body field as a dotted path of contract names and array
// indices, with the failed rule. Property names are echoed only when safe.
func requestReason(err error) error {
	var requestErr *openapi3filter.RequestError
	if errors.As(err, &requestErr) && requestErr.Parameter != nil {
		return domain.RejectedDetail("parameter_invalid", requestErr.Parameter.Name)
	}
	var schemaErr *openapi3.SchemaError
	if !errors.As(err, &schemaErr) {
		if requestErr != nil && requestErr.RequestBody != nil {
			return domain.Rejected("body_invalid")
		}
		return domain.Rejected("request_invalid")
	}
	path := schemaErr.JSONPointer()
	// A missing property's pointer already ends with it; an unsupported one's does not.
	field := func(name string) string {
		if len(path) > 0 && path[len(path)-1] == name {
			return strings.Join(path, ".")
		}
		return strings.Join(append(append([]string{}, path...), name), ".")
	}
	if name, ok := quotedProperty(schemaErr.Reason, " is missing"); ok {
		return domain.RejectedDetail("field_required", field(name))
	}
	if name, ok := quotedProperty(schemaErr.Reason, " is unsupported"); ok {
		return domain.RejectedDetail("unknown_field", field(name))
	}
	if len(path) == 0 {
		return domain.Rejected("body_invalid")
	}
	return domain.RejectedDetail("field_invalid", strings.Join(path, "."), schemaErr.SchemaField)
}

// quotedProperty reads name from a kin-openapi reason such as `property "name" is missing`.
func quotedProperty(reason, suffix string) (string, bool) {
	quoted, ok := strings.CutPrefix(reason, "property ")
	if !ok {
		return "", false
	}
	quoted, ok = strings.CutSuffix(quoted, suffix)
	if !ok {
		return "", false
	}
	name, err := strconv.Unquote(quoted)
	return name, err == nil
}
