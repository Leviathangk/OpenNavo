package store

import (
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestContentFieldRejections(t *testing.T) {
	for _, tc := range []struct {
		entity string
		fields map[string]any
		reason string
	}{
		{"package", map[string]any{"summary": strings.Repeat("私", 201)}, "field_too_long:package.summary"},
		{"package", map[string]any{"displayName": 123}, "field_type_invalid:package.displayName"},
		{"package", map[string]any{"private-key": "private-value"}, "unknown_field"},
		{"release", map[string]any{"sections": nil}, "sections_invalid:sections"},
		{"release", map[string]any{"sections": []any{map[string]any{"items": []any{123}}}}, "section_item_invalid:sections.items"},
	} {
		err := ValidateContentFields(tc.entity, tc.fields)
		var app *domain.AppError
		require.ErrorAs(t, err, &app)
		require.Equal(t, domain.CodeValidation, app.Code)
		require.Equal(t, "rejected: "+tc.reason, app.Msg)
		require.NotContains(t, app.Msg, "private")
	}
	require.NoError(t, ValidateContentFields("release", map[string]any{"summary": "Fixture"}))
	require.NoError(t, ValidateContentFields("release", map[string]any{"sections": []any{}}))
	require.NoError(t, ValidateContentFields("package", map[string]any{"summary": nil}))
}
