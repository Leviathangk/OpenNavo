package httpserver_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/stretchr/testify/require"
)

func TestHermesContractExamplesAndAgentBoundary(t *testing.T) {
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	require.NoError(t, spec.Validate(context.Background()))
	locales := []string{"en-US", "zh-CN", "ja-JP", "es-ES", "pt-BR", "ru-RU"}
	newOperations := strings.Fields("updatePackageText upsertReleaseNotes listTranslations getTranslationStatus retranslateContent fixTranslation listGlossary upsertGlossaryTerm deleteGlossaryTerm getAnnouncement updateAnnouncement updateDesktopReleaseNotes listAdminCatalogChanges getAgentSettings updateAgentSettings listAgentClients createAgentClient getAgentClient updateAgentClient listAgentTokens createAgentToken updateAgentToken revokeAgentToken introspectAgentToken listContentRevisions getContentRevision restoreRevision listTrash getTrashItem restoreTrash revertRequest listAgentCalls getAgentCall listTranslationLogs getTranslationLog listAdminVersions")
	var covered int
	for path, item := range spec.Paths.Map() {
		for method, operation := range item.Operations() {
			if slices.ContainsFunc(newOperations, func(name string) bool { return strings.EqualFold(name, operation.OperationID) }) {
				covered++
				t.Run(operation.OperationID, func(t *testing.T) {
					media := operation.Responses.Status(200).Value.Content.Get("application/json")
					// EmptyResponse deletions reuse existing responses; other new endpoints must provide six usable static examples.
					if operation.OperationID != "deleteGlossaryTerm" {
						for _, locale := range locales {
							example, ok := media.Examples[locale]
							require.True(t, ok, locale)
							require.NoError(t, media.Schema.Value.VisitJSON(example.Value.Value), locale)
							verifyNestedLocales(t, example.Value.Value, locales)
						}
					}
					if operation.RequestBody != nil {
						request := operation.RequestBody.Value.Content.Get("application/json")
						require.NotNil(t, request.Example)
						require.NoError(t, request.Schema.Value.VisitJSON(request.Example))
					}
				})
			}
			raw, ok := operation.Extensions["x-agent-access"]
			if !ok || raw == false {
				continue
			}
			encoded, err := json.Marshal(raw)
			require.NoError(t, err)
			var policy struct {
				Permissions   []string `json:"permissions"`
				Introspection bool     `json:"introspection"`
			}
			require.NoError(t, json.Unmarshal(encoded, &policy))
			if policy.Introspection {
				require.Equal(t, "/introspect", path)
				continue
			}
			require.NotEmpty(t, policy.Permissions, operation.OperationID)
			for _, permission := range policy.Permissions {
				require.Contains(t, spec.Components.Schemas["AgentPermission"].Value.Enum, permission)
			}
			require.False(t, strings.HasPrefix(path, "/system/") || strings.HasPrefix(path, "/mirrors") || strings.HasPrefix(path, "/app-config"), path)
			require.NotContains(t, operation.OperationID, "publishDesktop")
			require.NotContains(t, operation.OperationID, "rollbackDesktop")
			if method != "GET" {
				media := operation.Responses.Status(200).Value.Content.Get("application/json")
				example, ok := media.Examples["dry-run"]
				require.True(t, ok, operation.OperationID)
				require.NoError(t, media.Schema.Value.VisitJSON(example.Value.Value), operation.OperationID)
			}
		}
	}
	require.Equal(t, 36, covered)
	require.Len(t, spec.Components.Schemas["AgentPermission"].Value.Enum, 23)
	// Reject invalid permissions, mixed clear payloads and oversized batches at the contract layer.
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"AgentTokenCreate", map[string]any{"name": "invalid", "permissions": []any{"system:config:edit"}}},
		{"EditorialNotesUpdate", map[string]any{"clear": true, "sourceLocale": "zh-CN", "summary": "cannot mix"}},
		{"PackageTextUpdate", map[string]any{"sourceLocale": "zh-CN"}},
		{"AgentLimits", map[string]any{"callsPerMinute": 120, "writesPerDay": 3000, "llmOpsPerDay": 1000, "batchLimit": 101}},
	} {
		require.Error(t, spec.Components.Schemas[tc.name].Value.VisitJSON(tc.value), tc.name)
	}
	sections := make([]any, 8)
	for i := range sections {
		sections[i] = map[string]any{"area": "改进", "items": []any{"改善窗口体验。"}}
	}
	write := map[string]any{"sourceLocale": "zh-CN", "summary": "改善体验", "sections": sections}
	require.NoError(t, spec.Components.Schemas["EditorialNotesUpdate"].Value.VisitJSON(write))
	write["sections"] = append(sections, sections[0])
	require.Error(t, spec.Components.Schemas["EditorialNotesUpdate"].Value.VisitJSON(write))

	require.NoError(t, spec.Components.Schemas["EditorialNotesUpdate"].Value.VisitJSON(map[string]any{"clear": true}))
	require.Equal(t, openapi3.SecurityRequirements{{"agentToken": {}, "agentGateway": {}}}, *spec.Paths.Value("/introspect").Post.Security)
}

func verifyNestedLocales(t *testing.T, value any, locales []string) {
	t.Helper()
	switch v := value.(type) {
	case map[string]any:
		if translations, ok := v["i18n"].(map[string]any); ok {
			for _, locale := range locales {
				require.Contains(t, translations, locale)
			}
		}
		for _, child := range v {
			verifyNestedLocales(t, child, locales)
		}
	case []any:
		for _, child := range v {
			verifyNestedLocales(t, child, locales)
		}
	}
}
