package store

import (
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestLocalizedFieldsRespectWriteLimits(t *testing.T) {
	for entity, fields := range map[string]map[string]int{
		"collection":      {"title": 40, "subtitle": 80, "body": 10000},
		"collection_item": {"note": 160},
		"category":        {"name": 30, "description": 200},
		"feature":         {"badge": 12, "title": 30, "subtitle": 30, "body": 120, "ctaLabel": 10},
		"package":         {"displayName": 128, "summary": 200, "description": 20000},
		"release":         {"title": 256, "summary": 400, "bodyMarkdown": 200000},
		"article":         {"title": 200, "summary": 400, "bodyMarkdown": 100000, "testNote": 1000, "seoTitle": 120, "seoDescription": 300, "coverAlt": 300},
		"article_author":  {"role": 80, "bio": 1000},
		"screenshot":      {"caption": 120},
		"mirror":          {"name": 40},
		"announcement":    {"title": 200, "body": 2000},
		"desktop_release": {"notes": 20000},
	} {
		for field, limit := range fields {
			t.Run(entity+"/"+field, func(t *testing.T) {
				// OpenAPI maxLength counts characters, not UTF-8 bytes.
				boundary := strings.Repeat("字", limit)
				require.NoError(t, ValidateLocalizedFields(entity, "de-DE", map[string]any{field: boundary}))
				err := ValidateLocalizedFields(entity, "de-DE", map[string]any{field: boundary + "x"})
				var app *domain.AppError
				require.ErrorAs(t, err, &app)
				require.Equal(t, domain.CodeValidation, app.Code)
				require.Equal(t, 400, app.HTTPStatus)
				require.Equal(t, "rejected: field_too_long:"+entity+"."+field+".de-DE", app.Msg)
				require.NoError(t, ValidateLocalizedFields(entity, "de-DE", map[string]any{field: nil}))
			})
		}
	}
	// Internal column aliases must not bypass API limits.
	require.ErrorContains(t, ValidateLocalizedFields("feature", "de-DE", map[string]any{"cta_label": strings.Repeat("a", 11)}), "field_too_long:feature.ctaLabel.de-DE")
	require.ErrorContains(t, ValidateLocalizedFields("collection", "private-value", map[string]any{"title": "x"}), "rejected: locale_invalid")
	err := ValidateLocalizedFields("collection", "de-DE", map[string]any{"private-value": "x"})
	require.ErrorContains(t, err, "rejected: unknown_field")
	require.NotContains(t, err.Error(), "private-value")
}

func TestLocalizedReleaseSectionLimits(t *testing.T) {
	fields := func(area, item string) map[string]any {
		return map[string]any{"sections": []any{map[string]any{"area": area, "items": []any{item}}}}
	}
	require.NoError(t, ValidateLocalizedFields("release", "zh-CN", fields(strings.Repeat("字", 12), strings.Repeat("字", 80))))
	require.ErrorContains(t, ValidateLocalizedFields("release", "zh-CN", fields(strings.Repeat("字", 13), "item")), "field_too_long:release.sections[0].area.zh-CN")
	require.ErrorContains(t, ValidateLocalizedFields("release", "zh-CN", fields("area", strings.Repeat("字", 81))), "field_too_long:release.sections[0].items[0].zh-CN")
}
