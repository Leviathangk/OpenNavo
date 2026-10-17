//go:build integration

package httpserver_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/http/respond"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

// Expectations are independent of runtime Permissions and database roles, so incorrect configuration cannot also define test expectations.
func documentedPermissionPolicy() (map[string]string, map[string][]string) {
	groups := map[string]string{
		"":                           "getUserInfo logout getProfile updateProfile changePassword",
		"dashboard:view":             "getDashboardOverview getLlmUsage",
		"catalog:package:view":       "listAdminPackages getAdminPackage listPackageScreenshots listPackageVersions listChangelogSources listAdminReleases getAdminRelease getCategoryTree",
		"catalog:package:edit":       "updatePackageMeta setPackageCategories",
		"catalog:asset:upload":       "setPackageIcon deletePackageIcon addPackageScreenshot reorderPackageScreenshots updatePackageScreenshot deletePackageScreenshot listAssets uploadAsset",
		"catalog:package:resync":     "resyncPackage",
		"catalog:category:edit":      "createCategory reorderCategories updateCategory deleteCategory",
		"content:collection:edit":    "listAdminCollections createCollection getAdminCollection updateCollection deleteCollection setCollectionItems",
		"content:collection:publish": "publishCollection unpublishCollection",
		"content:feature:edit":       "listFeatures createFeature getFeature updateFeature deleteFeature",
		"changelog:release:edit":     "updateRelease",
		"translation:review":         "updatePackageI18n updateReleaseI18n retranslateRelease listTranslationQueue approveTranslations",
		"ops:job:view":               "listJobRuns getJobRun listQueues",
		"ops:job:trigger":            "triggerJob",
		"ops:search:view":            "listTopQueries listZeroResultQueries listSynonyms",
		"ops:search:edit":            "createSynonym updateSynonym deleteSynonym",
		"ops:feedback:handle":        "listFeedback getFeedback updateFeedback",
		"release:desktop:edit":       "listDesktopReleases createDesktopRelease getDesktopRelease updateDesktopRelease",
		"release:desktop:publish":    "publishDesktopRelease rollbackDesktopRelease",
		"system:config:edit":         "listMirrors createMirror updateMirror deleteMirror listAppConfig updateAppConfig",
		"system:user:edit":           "listAdminUsers createAdminUser updateAdminUser deleteAdminUser resetAdminUserPassword forceLogoutAdminUser",
		"system:role:edit":           "listRoles createRole updateRole deleteRole setRolePermissions listPermissions",
		"system:audit:view":          "listAuditLogs",
		"i18n:settings":              "getTranslationSettings updateTranslationSettings listTranslationModels",
		"system:github:edit":         "getGitHubSettings updateGitHubSettings",
		"system:seo:edit":            "getSeoSettings updateSeoSettings getPackageIndexStatus setPackageIndexStatus listIndexReport",
	}
	// B3 extends the independent expectation list; retain all original route and five-role assertions.
	groups["catalog:package:view"] += " listAdminVersions listAdminCatalogChanges getPackageChangelogSettings getPackageFacts listTapPackages"
	groups["catalog:package:edit"] += " updatePackageText updatePackageChangelogSettings setPackagePrice setPackageMacosSupport setPackageAlternatives addTapPackage removeTapPackage"
	groups["catalog:package:edit"] += " listLinkPackages addLinkPackage updateLinkPackage removeLinkPackage upsertUpdateFeed disableUpdateFeed"
	groups["changelog:release:edit"] += " upsertReleaseNotes"
	groups["translation:review"] += " listTranslations getTranslationStatus retranslateContent fixTranslation"
	groups["content:glossary:edit"] = "listGlossary upsertGlossaryTerm deleteGlossaryTerm"
	groups["content:announcement:edit"] = "getAnnouncement updateAnnouncement"
	groups["release:desktop:notes"] = "updateDesktopReleaseNotes"
	groups["agent:manage"] = "getAgentSettings updateAgentSettings listAgentClients createAgentClient getAgentClient updateAgentClient listAgentTokens createAgentToken updateAgentToken revokeAgentToken introspectAgentToken"
	groups["agent:log:view"] = "listContentRevisions getContentRevision listTrash getTrashItem listAgentCalls getAgentCall listTranslationLogs getTranslationLog"
	groups["content:revision:restore"] = "restoreRevision restoreTrash revertRequest"
	groups["content:article:edit"] = "listAdminArticles createArticle getAdminArticle updateArticle setArticlePackages submitArticleReview revertArticle listArticleAuthors"
	groups["content:article:publish"] = "publishArticle withdrawArticle restoreArticle setArticleSlug setArticleLocale createArticleAuthor updateArticleAuthor"
	// Upload tickets are issued only to Agents and redeemed only with the ticket itself.
	groups["catalog:asset:upload"] += " createAssetUploadTicket uploadAssetWithTicket"
	operations := map[string]string{}
	for permission, names := range groups {
		for _, name := range strings.Fields(names) {
			operations[strings.ToLower(name)] = permission
		}
	}
	all := []string{"R_ADMIN", "R_EDITOR", "R_REVIEWER", "R_OPS"}
	edit := []string{"R_ADMIN", "R_EDITOR"}
	ops := []string{"R_ADMIN", "R_OPS"}
	editorialOps := []string{"R_ADMIN", "R_EDITOR", "R_OPS"}
	roles := map[string][]string{
		"": all, "dashboard:view": all, "catalog:package:view": all,
		"catalog:package:edit": edit, "catalog:asset:upload": edit, "catalog:category:edit": edit,
		"catalog:package:resync": editorialOps, "content:collection:edit": edit,
		"content:collection:publish": {"R_ADMIN"}, "content:feature:edit": edit,
		"changelog:release:edit": edit,
		"translation:review":     {"R_ADMIN", "R_EDITOR", "R_REVIEWER"},
		"ops:job:view":           ops, "ops:job:trigger": ops, "ops:search:view": editorialOps,
		"ops:search:edit": edit, "ops:feedback:handle": editorialOps,
		"release:desktop:edit": ops, "release:desktop:publish": {"R_ADMIN"}, "system:config:edit": ops,
		"i18n:settings":        {"R_ADMIN"},
		"system:github:edit":   {"R_ADMIN"},
		"system:seo:edit":      {"R_ADMIN"},
		"content:article:edit": edit, "content:article:publish": {"R_ADMIN"},
		"system:user:edit": {}, "system:role:edit": {}, "system:audit:view": {"R_ADMIN"},
	}
	for _, p := range []string{"content:glossary:edit", "content:announcement:edit", "release:desktop:notes", "agent:log:view", "content:revision:restore"} {
		roles[p] = []string{"R_ADMIN"}
	}
	roles["agent:manage"] = []string{}
	return operations, roles
}

func TestEveryAdminRouteFiveRolePermissionMatrix(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	password := "permission-matrix-dev-only"
	hash, err := seed.HashPassword(password)
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "e2e-super", hash))
	for _, account := range seeds.E2EAccounts()[1:] {
		require.NoError(t, st.DB.Exec("INSERT INTO admin_users(user_name,password_hash,nick_name) VALUES(?,?,?)", account.Username, hash, account.Username).Error)
		require.NoError(t, st.DB.Exec("INSERT INTO admin_user_roles(user_id,role_id) SELECT u.id,r.id FROM admin_users u CROSS JOIN admin_roles r WHERE u.user_name=? AND r.role_code=?", account.Username, account.Role).Error)
	}
	cfg := config.Config{JWTSecret: strings.Repeat("m", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	handler := &admin.Handler{Auth: authentication, CIToken: rand.Text()}
	full, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: handler})
	require.NoError(t, err)
	probe := gin.New()
	probe.Use(handler.Middleware(nil))
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	policy, roles := documentedPermissionPolicy()
	type endpoint struct {
		method, path, actual, operation, permission string
		anonymous                                   bool
	}
	routes := []endpoint{}
	for path, item := range spec.Paths.Map() {
		parts, actual := strings.Split(path, "/"), strings.Split(path, "/")
		for i, part := range parts {
			if strings.HasPrefix(part, "{") {
				name := strings.Trim(part, "{}")
				parts[i], actual[i] = ":"+name, "1"
				if name == "locale" {
					actual[i] = "zh-CN"
				}
				if name == "key" {
					actual[i] = "desktop.announcement"
				}
				if name == "jobType" {
					actual[i] = "catalog:sync"
				}
			}
		}
		for method, operation := range item.Operations() {
			p := "/admin-api" + strings.Join(parts, "/")
			permission, documented := policy[strings.ToLower(operation.OperationID)]
			anonymous := strings.EqualFold(operation.OperationID, "login") || strings.EqualFold(operation.OperationID, "refreshToken")
			require.True(t, documented || anonymous, operation.OperationID)
			if !anonymous {
				require.Equal(t, permission, admin.Permissions[method+" "+p], operation.OperationID)
			}
			probe.Handle(method, p, func(c *gin.Context) { respond.OK(c, gin.H{}) })
			routes = append(routes, endpoint{method, p, "/admin-api" + strings.Join(actual, "/"), operation.OperationID, permission, anonymous})
		}
	}
	probe.GET("/admin-api/unregistered", func(c *gin.Context) { respond.OK(c, gin.H{}) })
	check := func(engine *gin.Engine, method, path, token, expected string) {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		require.Equal(t, 200, w.Code, method+" "+path)
		var envelope struct{ Code string }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
		require.Equal(t, expected, envelope.Code, method+" "+path)
	}
	// Agent entry points never accept admin JWTs or CI tokens; without configured Agent access they deny like introspection.
	agentOnly := func(operation string) bool {
		return slices.ContainsFunc([]string{"introspectAgentToken", "createAssetUploadTicket", "uploadAssetWithTicket"}, func(name string) bool { return strings.EqualFold(name, operation) })
	}
	allowedCount, deniedCount := 0, 0
	for _, account := range seeds.E2EAccounts() {
		pair, err := authentication.Login(ctx, account.Username, password, store.AuditActor{})
		require.NoError(t, err)
		for _, route := range routes {
			t.Run(account.Role+"/"+route.operation, func(t *testing.T) {
				allowed := route.anonymous || account.Role == "R_SUPER" || slices.Contains(roles[route.permission], account.Role)
				if agentOnly(route.operation) {
					allowed = false
				}
				expected := domain.CodeForbidden
				if allowed {
					expected = domain.CodeOK
					allowedCount++
				} else {
					deniedCount++
				}
				// Allowed paths use a side-effect-free endpoint to isolate authentication/authorization; denied paths use the full production router.
				check(probe, route.method, route.actual, pair.Token, expected)
				if !allowed {
					check(full.Engine, route.method, route.actual, pair.Token, expected)
				}
			})
		}
		check(probe, "GET", "/admin-api/unregistered", pair.Token, domain.CodeForbidden)
	}
	for _, route := range routes {
		if route.anonymous {
			continue
		}
		allowed := route.method+" "+route.path == "POST /admin-api/assets" || route.method+" "+route.path == "POST /admin-api/desktop-releases"
		expected := domain.CodeForbidden
		if allowed {
			expected = domain.CodeOK
		}
		check(probe, route.method, route.actual, handler.CIToken, expected)
		anonymousCode := domain.CodeUnauthenticated
		if agentOnly(route.operation) {
			anonymousCode = domain.CodeForbidden
		}
		check(probe, route.method, route.actual, "", anonymousCode)
	}
	t.Logf("matrix: %d routes × 5 roles = %d cases; allowed=%d denied=%d; denied cases also verified on full server", len(routes), len(routes)*5, allowedCount, deniedCount)
}
