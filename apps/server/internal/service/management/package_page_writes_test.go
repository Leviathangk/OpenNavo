package management

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIntentAndSEOBodiesNameWhatIsWrong(t *testing.T) {
	item := func(fields map[string]any) map[string]any { return map[string]any{"items": []any{fields}} }
	for _, tc := range []struct {
		name   string
		body   map[string]any
		reason string
	}{
		{"unknown top-level field", map[string]any{"items": []any{}, "extra": 1}, "unknown_field:extra"},
		{"unknown item field", item(map[string]any{"pageKind": "alternatives", "locale": "en-US", "enabled": true, "bogus": 1}), "unknown_field:bogus"},
		{"unsafe field name", map[string]any{"items": []any{}, "a b": 1}, "unknown_field"},
		{"items not a list", map[string]any{"items": "alternatives"}, "field_invalid:items"},
		{"item not an object", map[string]any{"items": []any{"alternatives"}}, "field_invalid:items.0"},
		{"enabled not a boolean", item(map[string]any{"pageKind": "alternatives", "locale": "en-US", "enabled": "yes"}), "field_invalid:items.0.enabled"},
		{"page kind not a string", item(map[string]any{"pageKind": 1, "locale": "en-US", "enabled": true}), "field_invalid:items.0.pageKind"},
		{"enabled missing", item(map[string]any{"pageKind": "alternatives", "locale": "en-US"}), "field_required:items.enabled"},
		{"no items", map[string]any{"items": []any{}}, "items_invalid:items"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeIntentChanges(tc.body)
			require.EqualError(t, err, "1001: rejected: "+tc.reason)
		})
	}
	changes, err := decodeIntentChanges(item(map[string]any{"pageKind": "alternatives", "locale": "en-US", "enabled": false}))
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.False(t, changes[0].Enabled)

	for _, tc := range []struct {
		name   string
		body   map[string]any
		reason string
	}{
		{"unknown field", map[string]any{"locale": "en-US", "title": "Title", "keywords": "x"}, "unknown_field:keywords"},
		{"title not text", map[string]any{"locale": "en-US", "title": 5}, "field_invalid:title"},
		{"description not text", map[string]any{"locale": "en-US", "description": []any{"x"}}, "field_invalid:description"},
		{"locale not text", map[string]any{"locale": 1, "title": "Title"}, "field_invalid:locale"},
		{"unknown locale", map[string]any{"locale": "xx-XX", "title": "Title"}, "locale_invalid:locale"},
		{"derived locale", map[string]any{"locale": "zh-Hant", "title": "標題"}, "derived_locale_read_only:zh-Hant:zh-CN"},
		{"nothing to set", map[string]any{"locale": "en-US"}, "field_required:title"},
		{"separator", map[string]any{"locale": "en-US", "title": "two lines"}, "seo_text_invalid:title"},
	} {
		t.Run("seo "+tc.name, func(t *testing.T) {
			_, _, _, err := seoRequest(tc.body)
			require.EqualError(t, err, "1001: rejected: "+tc.reason)
		})
	}
	locale, title, description, err := seoRequest(map[string]any{"locale": "en-US", "title": "  Title  ", "description": nil})
	require.NoError(t, err)
	require.Equal(t, "en-US", locale)
	require.Equal(t, "Title", *title.Value)
	require.True(t, description.Set)
	require.Nil(t, description.Value)
}
