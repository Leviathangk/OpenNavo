package httpserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/http/middleware"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/http/respond"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Readiness interface{ Ready(context.Context) error }
type Server struct {
	Engine  *gin.Engine
	Metrics http.Handler
}
type Dependencies struct {
	Collectors []prometheus.Collector
	Public     *public.Handler
	Rate       *middleware.RateLimiter
	Admin      *admin.Handler
}

//nolint:contextcheck // Gin callbacks inherit each request's context; construction performs no I/O and must not replace request contexts with the process context.
func New(cfg config.Config, logger *slog.Logger, ready Readiness, deps ...Dependencies) (*Server, error) {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	// Accept forwarded client addresses only from configured proxies; direct connections still ignore forged headers.
	engine.RemoteIPHeaders = []string{"X-Forwarded-For"}
	if err := engine.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		return nil, err
	}
	registry := prometheus.NewRegistry()
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "http_requests_total", Help: "HTTP requests by route, method and status."}, []string{"route", "method", "status"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "http_request_duration_seconds", Help: "HTTP request duration."}, []string{"route", "method"})
	registry.MustRegister(requests, duration)
	if len(deps) > 0 {
		for _, collector := range deps[0].Collectors {
			registry.MustRegister(collector)
		}
	}
	engine.Use(middleware.RequestID(), middleware.Logger(logger), middleware.Recovery(logger), func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		requests.WithLabelValues(route, c.Request.Method, strconv.Itoa(c.Writer.Status())).Inc()
		duration.WithLabelValues(route, c.Request.Method).Observe(time.Since(start).Seconds())
	}, middleware.CORS(cfg.PublicOrigins(), cfg.AdminOrigins()...), middleware.UsageCountry(cfg.UsageCountryTrustedProxies))
	engine.GET("/healthz", func(c *gin.Context) { respond.OK(c, gin.H{"status": "ok"}) })
	engine.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if ready == nil || ready.Ready(ctx) != nil {
			respond.Error(c, &domain.AppError{Code: domain.CodeInternal, HTTPStatus: http.StatusServiceUnavailable}, false)
			return
		}
		respond.OK(c, gin.H{"status": "ready"})
	})
	engine.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/agent-api/") {
			respond.Error(c, &domain.AppError{Code: domain.CodeForbidden, HTTPStatus: http.StatusForbidden}, true)
			return
		}
		respond.Error(c, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: http.StatusNotFound}, false)
	})
	fail := func(admin bool) func(*gin.Context, error) {
		return func(c *gin.Context, err error) {
			var appErr *domain.AppError
			if !errors.As(err, &appErr) || appErr.Code == domain.CodeInternal {
				attrs := []any{"requestId", c.GetString("requestId"), "code", domain.CodeInternal, "route", c.FullPath()}
				logger.ErrorContext(c.Request.Context(), "request failed", append(attrs, failureDetails(err)...)...)
			}
			respond.Error(c, err, admin)
		}
	}
	invalid := func(admin bool) func(*gin.Context, error) {
		return func(c *gin.Context, _ error) { respond.Error(c, domain.Validation(), admin) }
	}
	publicSpec, err := publicapi.GetSpec()
	if err != nil {
		return nil, err
	}
	publicValidation, err := middleware.Validate(publicSpec, "/api/v1", false)
	if err != nil {
		return nil, err
	}
	publicService := &public.Handler{}
	publicMiddleware := []gin.HandlerFunc{middleware.RequestBounds(false), publicValidation, middleware.PublicCacheHeaders()}
	if len(deps) > 0 {
		if deps[0].Public != nil {
			publicService = deps[0].Public
		}
		if deps[0].Rate != nil {
			publicMiddleware = append([]gin.HandlerFunc{middleware.RateLimit(*deps[0].Rate, logger)}, publicMiddleware...)
		}
	}
	publicMiddleware = append(publicMiddleware, func(c *gin.Context) {
		if c.Param("kind") == "formula" || c.Query("kind") == "formula" {
			if strings.HasPrefix(c.Request.URL.Path, "/agent-api/") {
				respond.Error(c, &domain.AppError{Code: domain.CodeForbidden, HTTPStatus: http.StatusForbidden}, true)
				return
			}
			respond.Error(c, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: http.StatusNotFound}, false)
			c.Abort()
		}
	})
	publicGroup := engine.Group("/api/v1", publicMiddleware...)
	publicHandler := publicapi.NewStrictHandlerWithOptions(publicService, nil, publicapi.StrictGinServerOptions{RequestErrorHandlerFunc: invalid(false), HandlerErrorFunc: fail(false), ResponseErrorHandlerFunc: fail(false)})
	publicapi.RegisterHandlersWithOptions(publicGroup, publicHandler, publicapi.GinServerOptions{ErrorHandler: func(c *gin.Context, _ error, _ int) { respond.Error(c, domain.Validation(), false) }})
	adminSpec, err := adminapi.GetSpec()
	if err != nil {
		return nil, err
	}
	adminValidation, err := middleware.Validate(adminSpec, "/admin-api", true)
	if err != nil {
		return nil, err
	}
	adminGroup := engine.Group("/admin-api", func(c *gin.Context) {
		if c.Request.Method == "POST" && (c.Request.URL.Path == "/admin-api/assets" || c.FullPath() == "/admin-api/assets/upload-tickets/:ticket") {
			if c.Request.ContentLength > 6*1024*1024 {
				respond.Error(c, &domain.AppError{Code: domain.CodeInvalidUpload, HTTPStatus: 400}, true)
				c.Abort()
				return
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 6*1024*1024)
		}
		c.Next()
	})
	adminService := &admin.Handler{}
	if len(deps) > 0 && deps[0].Admin != nil {
		adminService = deps[0].Admin
	}
	if adminService.Auth != nil {
		var limiter *middleware.RateLimiter
		if len(deps) > 0 {
			limiter = deps[0].Rate
		}
		adminGroup.Use(adminService.Middleware(limiter))
	}
	adminGroup.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
			data, err := io.ReadAll(io.LimitReader(c.Request.Body, 1024*1024+1))
			if err != nil || len(data) > 1024*1024 {
				respond.Error(c, domain.Validation(), true)
				return
			}
			c.Set("adminBody", data)
			c.Request.Body = io.NopCloser(bytes.NewReader(data))
		}
		c.Next()
	}, adminValidation)
	adminHandler := adminapi.NewStrictHandlerWithOptions(adminService, nil, adminapi.StrictGinServerOptions{RequestErrorHandlerFunc: invalid(true), HandlerErrorFunc: fail(true), ResponseErrorHandlerFunc: fail(true)})
	adminapi.RegisterHandlersWithOptions(adminGroup, adminHandler, adminapi.GinServerOptions{ErrorHandler: func(c *gin.Context, _ error, _ int) { respond.Error(c, domain.Validation(), true) }})
	if adminService.Management != nil {
		agentSpec, err := adminapi.GetSpec()
		if err != nil {
			return nil, err
		}
		policies := admin.AgentPolicies(agentSpec)
		validation, err := middleware.Validate(agentSpec, "/agent-api", true)
		if err != nil {
			return nil, err
		}
		group := engine.Group("/agent-api", adminService.AgentMiddleware(policies), middleware.RequestBounds(true), validation)
		adminapi.RegisterHandlersWithOptions(admin.AgentRouter{IRouter: group, Policies: policies}, adminHandler, adminapi.GinServerOptions{ErrorHandler: func(c *gin.Context, _ error, _ int) { respond.Error(c, domain.Validation(), true) }})
	}
	return &Server{Engine: engine, Metrics: promhttp.HandlerFor(registry, promhttp.HandlerOpts{})}, nil
}
