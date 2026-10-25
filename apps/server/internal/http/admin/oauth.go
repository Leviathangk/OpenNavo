package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/internal/http/middleware"
	"github.com/opennavo/opennavo/server/internal/service/agent"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/mcpoauth"
	"github.com/opennavo/opennavo/server/internal/store"
)

// OAuth is a protocol API, deliberately outside the admin envelope/OpenAPI router.
func (h *Handler) RegisterOAuth(engine *gin.Engine) {
	if h.Auth == nil || h.Management == nil || h.Management.Config.AdminOrigin == "" {
		return
	}
	issuer := strings.TrimRight(h.Management.Config.AdminOrigin, "/")
	svc := &mcpoauth.Service{Store: h.Management.Store, Issuer: issuer}
	fail := func(c *gin.Context, err error) {
		code := mcpoauth.ProtocolError(err)
		status := 400
		if code == "server_error" {
			status = 500
		}
		c.AbortWithStatusJSON(status, gin.H{"error": code})
	}
	boundary := func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Pragma", "no-cache")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
		origin := c.GetHeader("Origin")
		private := c.Request.URL.Path == "/oauth/consent" || strings.HasPrefix(c.Request.URL.Path, "/oauth/grants")
		if origin != "" {
			if private && origin != issuer {
				c.AbortWithStatusJSON(403, gin.H{"error": "access_denied"})
				return
			}
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		}
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		if len(c.Request.URL.RawQuery) > 8192 {
			fail(c, mcpoauth.Error("invalid_request"))
			return
		}
		// A shared quota protects public registration and token endpoints across API replicas.
		if h.Management.Store.Redis == nil {
			c.AbortWithStatusJSON(503, gin.H{"error": "temporarily_unavailable"})
			return
		}
		limiter := middleware.RateLimiter{Client: h.Management.Store.Redis}
		count, window := 120, time.Minute
		if c.Request.URL.Path == "/oauth/register" {
			count, window = 10, time.Hour
		}
		allowed, retry, err := limiter.Allow(c.Request.Context(), "oauth:"+c.Request.URL.Path+":"+agent.Hash(c.ClientIP()), count, window)
		if err != nil {
			c.AbortWithStatusJSON(503, gin.H{"error": "temporarily_unavailable"})
			return
		}
		if !allowed {
			c.Header("Retry-After", strconv.Itoa(retry))
			c.AbortWithStatusJSON(429, gin.H{"error": "temporarily_unavailable"})
			return
		}
		c.Next()
	}
	group := engine.Group("/oauth", boundary)
	group.OPTIONS("/*path", func(c *gin.Context) { c.Status(204) })
	metadata := func(c *gin.Context) {
		c.JSON(200, gin.H{"issuer": issuer, "authorization_endpoint": issuer + "/oauth/authorize", "token_endpoint": issuer + "/oauth/token", "registration_endpoint": issuer + "/oauth/register", "revocation_endpoint": issuer + "/oauth/revoke", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": agent.Grantable, "authorization_response_iss_parameter_supported": true})
	}
	resource := func(c *gin.Context) {
		c.JSON(200, gin.H{"resource": svc.Resource(), "authorization_servers": []string{issuer}, "scopes_supported": agent.Grantable, "bearer_methods_supported": []string{"header"}})
	}
	for path, handler := range map[string]gin.HandlerFunc{"/.well-known/oauth-authorization-server": metadata, "/.well-known/oauth-protected-resource": resource, "/.well-known/oauth-protected-resource/mcp": resource} {
		engine.GET(path, boundary, handler)
		engine.OPTIONS(path, boundary, func(c *gin.Context) { c.Status(204) })
	}
	decode := func(c *gin.Context, v any) bool {
		if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
			fail(c, mcpoauth.Error("invalid_request"))
			return false
		}
		d := json.NewDecoder(c.Request.Body)
		if d.Decode(v) != nil {
			fail(c, mcpoauth.Error("invalid_request"))
			return false
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			fail(c, mcpoauth.Error("invalid_request"))
			return false
		}
		return true
	}
	group.POST("/register", func(c *gin.Context) {
		var in mcpoauth.Registration
		if !decode(c, &in) {
			return
		}
		out, err := svc.Register(c.Request.Context(), in)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(201, gin.H{"client_id": out.ID, "client_name": out.Name, "redirect_uris": out.RedirectURIs, "scope": out.Scopes, "token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}})
	})
	parseAuthorization := func(c *gin.Context) (mcpoauth.Authorization, bool) {
		q, err := url.ParseQuery(c.Request.URL.RawQuery)
		if err != nil {
			fail(c, mcpoauth.Error("invalid_request"))
			return mcpoauth.Authorization{}, false
		}
		for _, v := range q {
			if len(v) != 1 {
				fail(c, mcpoauth.Error("invalid_request"))
				return mcpoauth.Authorization{}, false
			}
		}
		return mcpoauth.Authorization{ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), ResponseType: q.Get("response_type"), Scope: q.Get("scope"), State: q.Get("state"), Challenge: q.Get("code_challenge"), ChallengeMethod: q.Get("code_challenge_method"), Resource: q.Get("resource")}, true
	}
	group.GET("/authorize", func(c *gin.Context) {
		in, ok := parseAuthorization(c)
		if !ok {
			return
		}
		if _, _, err := svc.Validate(c.Request.Context(), in); err != nil {
			fail(c, err)
			return
		}
		c.Redirect(http.StatusSeeOther, issuer+"/oauth-consent?"+c.Request.URL.RawQuery)
	})
	form := func(c *gin.Context) bool {
		if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/x-www-form-urlencoded") || c.Request.ParseForm() != nil {
			fail(c, mcpoauth.Error("invalid_request"))
			return false
		}
		for _, v := range c.Request.PostForm {
			if len(v) != 1 {
				fail(c, mcpoauth.Error("invalid_request"))
				return false
			}
		}
		// This server supports public clients; do not silently accept ignored secrets.
		if c.GetHeader("Authorization") != "" || c.Request.PostForm.Get("client_secret") != "" {
			fail(c, mcpoauth.Error("invalid_client"))
			return false
		}
		return true
	}
	group.POST("/token", func(c *gin.Context) {
		if !form(c) {
			return
		}
		v := c.Request.PostForm
		out, err := svc.Exchange(c.Request.Context(), mcpoauth.TokenRequest{GrantType: v.Get("grant_type"), ClientID: v.Get("client_id"), Code: v.Get("code"), RedirectURI: v.Get("redirect_uri"), Verifier: v.Get("code_verifier"), RefreshToken: v.Get("refresh_token"), Resource: v.Get("resource"), Scope: v.Get("scope")})
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, out)
	})
	group.POST("/revoke", func(c *gin.Context) {
		if !form(c) {
			return
		}
		v := c.Request.PostForm
		if v.Get("client_id") == "" || v.Get("token") == "" {
			fail(c, mcpoauth.Error("invalid_request"))
			return
		}
		if err := svc.Revoke(c.Request.Context(), v.Get("client_id"), v.Get("token")); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{})
	})
	session := func(c *gin.Context) (auth.Identity, bool) {
		id, err := h.Auth.Authenticate(c.Request.Context(), auth.Bearer(c.GetHeader("Authorization")))
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "login_required"})
			return id, false
		}
		c.Set("userId", id.ID)
		c.Set("adminActor", store.AuditActor{ID: &id.ID, IP: c.ClientIP(), UserAgent: c.GetHeader("User-Agent")})
		return id, true
	}
	group.GET("/consent", func(c *gin.Context) {
		id, ok := session(c)
		if !ok {
			return
		}
		in, ok := parseAuthorization(c)
		if !ok {
			return
		}
		client, requested, err := svc.Validate(c.Request.Context(), in)
		if err != nil {
			fail(c, err)
			return
		}
		allowed, err := svc.Allowed(c.Request.Context(), id.ID)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"clientName": client.Name, "clientId": client.ID, "redirectUri": in.RedirectURI, "requestedScopes": requested, "allowedScopes": allowed})
	})
	group.POST("/consent", func(c *gin.Context) {
		id, ok := session(c)
		if !ok {
			return
		}
		var in struct {
			Authorization mcpoauth.Authorization `json:"authorization"`
			Scopes        []string               `json:"scopes"`
			AllowDelete   bool                   `json:"allowDelete"`
			Approve       bool                   `json:"approve"`
		}
		if !decode(c, &in) {
			return
		}
		destination, err := svc.Consent(c.Request.Context(), id, in.Authorization, in.Scopes, in.AllowDelete, in.Approve, actor(c))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"redirect": destination})
	})
	group.GET("/grants", func(c *gin.Context) {
		id, ok := session(c)
		if !ok {
			return
		}
		// ID cursors remain stable when newer grants are created or old ones revoked.
		var before int64
		if values, present := c.Request.URL.Query()["before"]; present {
			var err error
			if len(values) == 1 {
				before, err = strconv.ParseInt(values[0], 10, 64)
			}
			if len(values) != 1 || err != nil || before <= 0 {
				fail(c, mcpoauth.Error("invalid_request"))
				return
			}
		}
		var rows []struct {
			ID          int64      `json:"id"`
			Name        string     `json:"name"`
			Scopes      string     `json:"scopes"`
			AllowDelete bool       `json:"allowDelete"`
			ExpiresAt   time.Time  `json:"expiresAt"`
			RevokedAt   *time.Time `json:"revokedAt"`
		}
		query := `SELECT g.id,c.name,g.scopes,g.allow_delete,g.expires_at,g.revoked_at FROM mcp_oauth_grants g JOIN mcp_oauth_clients c ON c.id=g.client_id WHERE g.user_id=?`
		args := []any{id.ID}
		if before > 0 {
			query += " AND g.id<?"
			args = append(args, before)
		}
		err := svc.Store.DB.WithContext(c.Request.Context()).Raw(query+" ORDER BY g.id DESC LIMIT 101", args...).Scan(&rows).Error
		if err != nil {
			fail(c, err)
			return
		}
		var nextCursor *string
		if len(rows) > 100 {
			rows = rows[:100]
			cursor := strconv.FormatInt(rows[len(rows)-1].ID, 10)
			nextCursor = &cursor
		}
		if rows == nil {
			c.JSON(200, gin.H{"items": []any{}, "nextCursor": nil})
			return
		}
		c.JSON(200, gin.H{"items": rows, "nextCursor": nextCursor})
	})
	group.DELETE("/grants/:id", func(c *gin.Context) {
		id, ok := session(c)
		if !ok {
			return
		}
		grantID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			fail(c, mcpoauth.Error("invalid_request"))
			return
		}
		if err := svc.RevokeOwned(c.Request.Context(), id.ID, grantID, actor(c)); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{})
	})
}
