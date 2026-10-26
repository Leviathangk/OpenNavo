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
