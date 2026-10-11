package admin

import (
	"crypto/subtle"
	"slices"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/http/middleware"
	"github.com/opennavo/opennavo/server/internal/http/respond"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/store"
)

func (h *Handler) Middleware(limiter *middleware.RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		key := c.Request.Method + " " + c.FullPath()
		// The generator shares the introspection and ticket-issuing paths, but they must never be accessible through the admin entry point.
		if key == "POST /admin-api/introspect" || key == "POST /admin-api/assets/upload-tickets" {
			respond.Error(c, &domain.AppError{Code: domain.CodeForbidden, HTTPStatus: 403}, true)
			return
		}
		// Ticket uploads authenticate only by the ticket; admin JWTs and CI tokens never apply to this route.
		if key == "POST /admin-api/assets/upload-tickets/:ticket" {
			h.agentUpload(c)
			return
		}
		if key == "POST /admin-api/auth/login" || key == "POST /admin-api/auth/refreshToken" {
			if limiter != nil {
				allowed, retry, err := limiter.Allow(c.Request.Context(), "admin:login:"+c.ClientIP(), 10, time.Minute)
				if err != nil {
					respond.Error(c, err, true)
					return
				}
				if !allowed {
					c.Header("Retry-After", strconv.Itoa(max(1, retry)))
					respond.Error(c, &domain.AppError{Code: domain.CodeRateLimited, HTTPStatus: 429}, true)
					return
				}
			}
			c.Next()
			return
		}
		token := auth.Bearer(c.GetHeader("Authorization"))
		if h.CIToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(h.CIToken)) == 1 {
			if key == "POST /admin-api/assets" || key == "POST /admin-api/desktop-releases" {
				c.Set("adminActor", store.AuditActor{IP: c.ClientIP(), UserAgent: c.GetHeader("User-Agent")})
				c.Set("adminCI", true)
				c.Next()
				return
			}
			respond.Error(c, &domain.AppError{Code: domain.CodeForbidden, HTTPStatus: 403}, true)
			return
		}
		i, err := h.Auth.Authenticate(c.Request.Context(), token)
		if err != nil {
			respond.Error(c, err, true)
			return
		}
		permission, registered := Permissions[key]
		allowed := i.Allowed(permission)
		if key == "GET /admin-api/desktop-releases" || key == "GET /admin-api/desktop-releases/:id" {
			allowed = allowed || i.Allowed("release:desktop:notes")
		}
		if !registered || !allowed {
			respond.Error(c, &domain.AppError{Code: domain.CodeForbidden, HTTPStatus: 403}, true)
			return
		}
		if limiter != nil {
			allowed, retry, err := limiter.Allow(c.Request.Context(), "admin:user:"+strconv.FormatInt(i.ID, 10), 600, time.Minute)
			if err != nil {
				respond.Error(c, err, true)
				return
			}
			if !allowed {
				c.Header("Retry-After", strconv.Itoa(max(1, retry)))
				respond.Error(c, &domain.AppError{Code: domain.CodeRateLimited, HTTPStatus: 429}, true)
				return
			}
		}
		permissions := append([]string{}, i.Permissions.Buttons...)
		if slices.Contains(i.Permissions.Roles, "R_SUPER") {
			permissions = append(permissions, "*")
		}
		op := &domain.Operation{RequestID: c.GetString("requestId"), ActorID: &i.ID, ActorName: i.Name, Permissions: permissions, RequiredPermissions: []string{permission}, AllowDelete: true, DryRun: c.Query("dryRun") == "true"}
		c.Request = c.Request.WithContext(domain.WithOperation(c.Request.Context(), op))
		c.Set("adminIdentity", i)
		c.Set("adminActor", store.AuditActor{ID: &i.ID, IP: c.ClientIP(), UserAgent: c.GetHeader("User-Agent")})
		c.Set("userId", i.ID)
		c.Next()
	}
}
