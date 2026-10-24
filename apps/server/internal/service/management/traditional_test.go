package management

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDerivedWritesRejectedBeforeDatabase(t *testing.T) {
	svc := &Service{}
	for _, body := range []map[string]any{
		{"sourceLocale": "zh-Hant"},
		{"i18n": map[string]any{"zh-Hant": nil}},
		{"translations": []any{map[string]any{"locale": "zh-Hant"}}},
		{"items": []any{map[string]any{"i18n": map[string]any{"zh-Hant": map[string]any{"note": "text"}}}}},
	} {
		_, err := svc.Execute(context.Background(), "WriteTranslations", Input{Body: body})
		require.ErrorContains(t, err, "derived_locale_read_only")
	}
	require.False(t, derivedWrite(map[string]any{"sourceLocale": []any{}, "locale": map[string]any{}, "title": "zh-Hant"}))
}
func TestCIOnlyAssertsDerivedNotes(t *testing.T) {
	body := map[string]any{"i18n": map[string]any{"zh-CN": map[string]any{"notes": "软件"}, "zh-Hant": map[string]any{"notes": "軟體"}}}
	require.NoError(t, verifyDerivedReleaseNotes(body))
	require.NotContains(t, body["i18n"], "zh-Hant")
	body["i18n"].(map[string]any)["zh-Hant"] = map[string]any{"notes": "arbitrary"}
	require.Error(t, verifyDerivedReleaseNotes(body))
}
