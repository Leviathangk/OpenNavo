package api_test

import (
	"context"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/stretchr/testify/require"
)

func TestHermesBatchContracts(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile("admin.openapi.yaml")
	require.NoError(t, err)
	for _, name := range []string{"BatchTranslationValue", "TranslationsSource", "BatchTranslationInput", "TranslationsBatchWrite", "BatchTranslationResult", "TranslationsBatchResult"} {
		schema := doc.Components.Schemas[name].Value
		require.NoError(t, schema.Validate(context.Background()), name)
		require.NoError(t, schema.VisitJSON(schema.Example), name)
	}
	require.NoError(t, doc.Components.Schemas["TranslationReadFields"].Value.VisitJSON(map[string]any{}))
	result := doc.Components.Schemas["BatchTranslationResult"].Value
	require.NoError(t, result.VisitJSON(map[string]any{"locale": "unsupported", "status": "failed", "reason": "validation_failed", "value": nil}))
	batch := doc.Components.Schemas["TranslationsBatchWrite"].Value
	require.NoError(t, batch.VisitJSON(map[string]any{
		"ref": map[string]any{"entity": "article"}, "articleId": 31,
		"translations": []any{map[string]any{"locale": "de-DE", "fields": map[string]any{"title": "Referenzhandbuch"}}},
	}))
	for _, invalid := range []any{nil, 0, -1, "31"} {
		require.Error(t, batch.VisitJSON(map[string]any{
			"ref": map[string]any{"entity": "article"}, "articleId": invalid,
			"translations": []any{map[string]any{"locale": "de-DE", "fields": map[string]any{"title": "Referenzhandbuch"}}},
		}))
	}
	readParams := doc.Paths.Value("/translations/source").Get.Parameters
	var articleIDFound bool
	for _, param := range readParams {
		if param.Value.Name == "articleId" {
			articleIDFound = true
			require.False(t, param.Value.Required)
			require.NoError(t, param.Value.Schema.Value.VisitJSON(31))
			require.Error(t, param.Value.Schema.Value.VisitJSON(0))
		}
		if param.Value.Name == "id" {
			require.False(t, param.Value.Required, "articleId can replace id")
		}
	}
	require.True(t, articleIDFound)
	fix := doc.Components.Schemas["TranslationFix"].Value
	require.NoError(t, fix.VisitJSON(map[string]any{
		"ref": map[string]any{"entity": "article"}, "articleId": 31,
		"locale": "de-DE", "fields": map[string]any{"title": "Referenzhandbuch"},
	}))
	for _, invalid := range []any{nil, 0, -1, "31"} {
		require.Error(t, fix.VisitJSON(map[string]any{
			"ref": map[string]any{"entity": "article"}, "articleId": invalid,
			"locale": "de-DE", "fields": map[string]any{"title": "Referenzhandbuch"},
		}))
	}
	articleIDFound = false
	for _, param := range doc.Paths.Value("/translations/status").Get.Parameters {
		if param.Value.Name == "articleId" {
			articleIDFound = true
			require.False(t, param.Value.Required)
			require.NoError(t, param.Value.Schema.Value.VisitJSON(31))
			require.Error(t, param.Value.Schema.Value.VisitJSON(0))
		}
		if param.Value.Name == "id" {
			require.False(t, param.Value.Required)
		}
	}
	require.True(t, articleIDFound)
	for _, path := range []string{"/translations/source", "/translations/batch"} {
		for method, op := range doc.Paths.Value(path).Operations() {
			examples := op.Responses.Status(200).Value.Content["application/json"].Examples
			count := len(i18n.SiteLocales)
			if method != "GET" {
				count++
				require.Contains(t, examples, "dry-run")
			}
			require.Len(t, examples, count)
			for _, locale := range i18n.SiteLocales {
				require.Contains(t, examples, string(locale))
			}
			for name, example := range examples {
				require.NoError(t, op.Responses.Status(200).Value.Content["application/json"].Schema.Value.VisitJSON(example.Value.Value), name)
			}
		}
	}
}
