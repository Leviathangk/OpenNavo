package api_test

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

func TestImageVariantAndCoverAltContracts(t *testing.T) {
	public, err := openapi3.NewLoader().LoadFromFile("public.openapi.yaml")
	require.NoError(t, err)
	variant := map[string]any{"width": float64(64), "height": float64(64), "url": "https://cdn.example.test/icon-64.webp", "type": "image/webp"}
	require.NoError(t, public.Components.Schemas["ImageVariant"].Value.VisitJSON(variant))
	for _, name := range []string{"PackageSummary", "Screenshot"} {
		schema := public.Components.Schemas[name].Value
		example := map[string]any{}
		for k, v := range schema.Example.(map[string]any) {
			example[k] = v
		}
		field := "variants"
		if name == "PackageSummary" {
			field = "iconVariants"
		}
		require.NoError(t, schema.VisitJSON(example), "old assets remain valid")
		example[field] = []any{variant}
		require.NoError(t, schema.VisitJSON(example))
		example[field] = []any{}
		require.NoError(t, schema.VisitJSON(example))
	}
	variant["width"] = float64(0)
	require.Error(t, public.Components.Schemas["ImageVariant"].Value.VisitJSON(variant))
	admin, err := openapi3.NewLoader().LoadFromFile("admin.openapi.yaml")
	require.NoError(t, err)
	fields := admin.Components.Schemas["ContentFields"].Value
	require.NoError(t, fields.VisitJSON(map[string]any{"coverAlt": "Localized cover description"}))
	require.NoError(t, fields.VisitJSON(map[string]any{"coverAlt": nil}))
	require.Error(t, fields.VisitJSON(map[string]any{"coverAlt": strings.Repeat("x", 301)}))
}
