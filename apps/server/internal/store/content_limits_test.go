package store

import (
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/traditional"
	"github.com/stretchr/testify/require"
)

func TestContentLimitsMatchLocalizedContract(t *testing.T) {
	doc, err := adminapi.GetSpec()
	require.NoError(t, err)
	for entity, model := range map[string]string{
		"package": "PackageTextUpdate", "category": "CategoryI18n",
		"collection": "CollectionI18n", "feature": "FeatureI18n",
		"screenshot": "ScreenshotI18n", "collection_item": "CollectionItemI18n",
		"mirror": "MirrorI18n", "desktop_release": "DesktopNotesUpdate",
	} {
		t.Run(entity, func(t *testing.T) {
			for column, limit := range content.Registry[entity].Fields {
				field := textAPIKey(column)
				property := doc.Components.Schemas[model].Value.Properties[field]
				require.NotNil(t, property, field)
				require.NotNil(t, property.Value.MaxLength, field)
				require.EqualValues(t, limit, *property.Value.MaxLength, field)
				for _, length := range []int{limit, limit + 1} {
					fields := map[string]any{field: strings.Repeat("字", length)}
					sourceErr := ValidateContentFields(entity, fields)
					translationErr := ValidateLocalizedFields(entity, "de-DE", fields)
					if length == limit {
						require.NoError(t, sourceErr, field)
						require.NoError(t, translationErr, field)
					} else {
						require.Error(t, sourceErr, field)
						require.Error(t, translationErr, field)
					}
				}
			}
		})
	}
}

func TestDerivedFieldsAllowConversionExpansion(t *testing.T) {
	for _, entity := range []string{"category", "collection", "feature", "screenshot", "collection_item", "mirror"} {
		t.Run(entity, func(t *testing.T) {
			for column, limit := range content.Registry[entity].Fields {
				text := strings.Repeat("内存", limit/2)
				fields := map[string]any{column: text}
				require.NoError(t, ValidateContentFields(entity, fields))
				converted, err := traditional.Fields(fields, nil)
				require.NoError(t, err)
				require.Error(t, ValidateContentFields(entity, converted), column)
				require.NoError(t, validateContentFields(entity, "", converted, false), column)
			}
		})
	}
	fields := map[string]any{"sections": []any{map[string]any{
		"area": strings.Repeat("内存", 6), "items": []any{strings.Repeat("内存", 40)},
	}}}
	converted, err := traditional.Fields(fields, nil)
	require.NoError(t, err)
	require.NoError(t, validateContentFields("release", "", converted, false))
	for _, invalid := range []map[string]any{
		{"name": 123}, {"unknown": "text"},
	} {
		require.Error(t, validateContentFields("category", "", invalid, false))
	}
	require.Error(t, validateContentFields("release", "", map[string]any{"sections": make([]any, 9)}, false))
}

func TestReleaseSectionsHaveOnlyPublishedShapeAndTextLimits(t *testing.T) {
	groups := make([]any, 8)
	for i := range groups {
		items := make([]any, 6)
		for j := range items {
			// JSON escaping expands these characters beyond the old total limit.
			items[j] = strings.Repeat("<>&字", 20)
		}
		groups[i] = map[string]any{"area": strings.Repeat("字", 12), "items": items}
	}
	for _, fields := range []map[string]any{{"sections": groups}, {"sections": []any{}}} {
		require.NoError(t, ValidateContentFields("release", fields))
		require.NoError(t, ValidateLocalizedFields("release", "de-DE", fields))
	}
	for _, sections := range []any{
		nil, append(groups, groups[0]),
		[]any{map[string]any{"area": strings.Repeat("字", 13), "items": []any{"Change"}}},
		[]any{map[string]any{"area": "Changes", "items": []any{strings.Repeat("字", 81)}}},
		[]any{map[string]any{"area": "Changes", "items": make([]any, 7)}},
		[]any{map[string]any{"area": "Changes", "items": []any{123}}},
	} {
		require.Error(t, ValidateContentFields("release", map[string]any{"sections": sections}))
		require.Error(t, ValidateLocalizedFields("release", "de-DE", map[string]any{"sections": sections}))
	}
}
