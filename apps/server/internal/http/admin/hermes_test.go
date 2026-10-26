package admin

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agent"
	"github.com/opennavo/opennavo/server/internal/service/assets"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

func TestHermesAdminIntrospectionAlwaysDenied(t *testing.T) {
	h := &Handler{}
	// Always reject the admin path, independently of valid accounts, CI tokens or the Auth service.
	r := gin.New()
	r.Use(h.Middleware(nil))
	r.POST("/admin-api/introspect", func(c *gin.Context) { t.Error("introspection reached handler") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin-api/introspect", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"code":"1004"`)
	_, err := h.IntrospectAgentToken(context.Background(), adminapi.IntrospectAgentTokenRequestObject{})
	var appError *domain.AppError
	require.ErrorAs(t, err, &appError)
	require.Equal(t, domain.CodeForbidden, appError.Code)
}

func TestUploadTicketRoutesNeverAcceptAdminOrCICredentials(t *testing.T) {
	h := &Handler{CIToken: rand.Text()}
	r := gin.New()
	r.Use(h.Middleware(nil))
	for _, path := range []string{"/admin-api/assets/upload-tickets", "/admin-api/assets/upload-tickets/:ticket"} {
		r.POST(path, func(c *gin.Context) { t.Error("ticket route reached handler: " + c.FullPath()) })
	}
	for _, path := range []string{"/admin-api/assets/upload-tickets", "/admin-api/assets/upload-tickets/onvu_0000000000000000000000000000000000000000000"} {
		for _, bearer := range []string{"", h.CIToken} {
			w := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, path, nil)
			request.Header.Set("Authorization", "Bearer "+bearer)
			r.ServeHTTP(w, request)
			require.Equal(t, http.StatusOK, w.Code)
			require.Contains(t, w.Body.String(), `"code":"1004"`, path)
		}
	}
	// Handlers deny independently of Middleware: issuing needs an Agent identity, uploading needs a redeemed ticket.
	h.Management, h.Assets = &management.Service{}, &assets.Service{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/admin-api/assets/upload-tickets", nil).WithContext(domain.WithOperation(context.Background(), &domain.Operation{RequestID: "admin-session"}))
	_, err := h.CreateAssetUploadTicket(c, adminapi.CreateAssetUploadTicketRequestObject{Body: &adminapi.AssetUploadTicketCreate{Kind: adminapi.AssetUploadTicketKindIcon}})
	var appError *domain.AppError
	require.ErrorAs(t, err, &appError)
	require.Equal(t, domain.CodeForbidden, appError.Code)
	_, err = h.UploadAssetWithTicket(c, adminapi.UploadAssetWithTicketRequestObject{})
	require.ErrorAs(t, err, &appError)
	require.Equal(t, domain.CodeUnauthenticated, appError.Code)
}

func TestHermesAgentSeedExactlyMatchesGrantableContract(t *testing.T) {
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	seed, err := seeds.Load()
	require.NoError(t, err)
	var granted []any
	for _, permission := range seed.Permissions {
		for _, role := range permission.Roles {
			if role == "R_AGENT" {
				granted = append(granted, permission.Code)
			}
		}
	}
	require.Len(t, granted, 24)
	require.ElementsMatch(t, spec.Components.Schemas["AgentPermission"].Value.Enum, granted)
	require.ElementsMatch(t, granted, agent.Grantable, "runtime grants must match the contract and seed")
}

func TestAllArticleRoutesAvailableToAgents(t *testing.T) {
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	policies := AgentPolicies(spec)
	for route, permission := range map[string]string{
		"POST /articles/:id/publish":        "content:article:publish",
		"POST /articles/:id/withdraw":       "content:article:publish",
		"POST /articles/:id/restore":        "content:article:publish",
		"POST /articles/:id/revert":         "content:article:edit",
		"PUT /articles/:id/slug":            "content:article:publish",
		"PUT /articles/:id/locales/:locale": "content:article:publish",
		"GET /article-authors":              "content:article:edit",
		"POST /article-authors":             "content:article:publish",
		"PUT /article-authors/:id":          "content:article:publish",
	} {
		require.Contains(t, policies, route)
		require.Equal(t, []string{permission}, policies[route].Permissions, route)
	}
}

func TestAgentRouteWhitelistCountAndForbiddenSurfaces(t *testing.T) {
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	policies := AgentPolicies(spec)
	// 113 routes plus BE-B's page decision, site index settings, intent page and app SEO routes.
	require.Len(t, policies, 121)
	require.Contains(t, policies, "POST /introspect")
	for _, retired := range []string{"POST /seo/tasks/claim", "GET /seo/tasks/:id", "POST /seo/evidence", "GET /seo/automation", "POST /seo/tasks/import", "POST /seo/geo", "GET /seo/keywords", "GET /seo/overview", "PUT /seo/listings/:token"} {
		require.NotContains(t, policies, retired)
	}
	require.NotContains(t, policies, "GET /dashboard/llm-usage")
	require.NotContains(t, policies, "POST /translations/retranslate")
	require.Equal(t, []string{"catalog:asset:upload"}, policies["POST /assets/upload-tickets"].Permissions)
	// Tickets are redeemed only through the public admin path; the internal entry point never accepts uploads by ticket.
	require.NotContains(t, policies, "POST /assets/upload-tickets/:ticket")
	for _, key := range []string{"GET /app-config", "PUT /app-config/:key", "GET /mirrors", "POST /desktop-releases", "PUT /desktop-releases/:id", "POST /desktop-releases/:id/publish", "POST /desktop-releases/:id/rollback", "POST /agent/clients", "GET /system/users"} {
		require.NotContains(t, policies, key)
	}
	require.Equal(t, []string{"release:desktop:notes"}, policies["GET /desktop-releases"].Permissions)
}
