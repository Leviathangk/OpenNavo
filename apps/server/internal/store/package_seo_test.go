package store

import (
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestNormalizeSEOTextKeepsOneLineOfPlainText(t *testing.T) {
	text := func(value string) *string { return &value }
	for _, tc := range []struct {
		name, value, want, reason string
		limit                     int
	}{
		{name: "trimmed", value: "  Visual Studio Code for Mac  ", want: "Visual Studio Code for Mac", limit: 70},
		{name: "empty clears", value: "   ", limit: 70},
		{name: "punctuation", value: "Tom & Jerry: “quotes” — 100% (beta)", want: "Tom & Jerry: “quotes” — 100% (beta)", limit: 70},
		{name: "cjk at limit", value: strings.Repeat("码", 70), want: strings.Repeat("码", 70), limit: 70},
		{name: "too long", value: strings.Repeat("a", 71), reason: "field_too_long:title", limit: 70},
		{name: "markup", value: "<b>Code</b>", reason: "seo_text_invalid:title", limit: 70},
		{name: "angle", value: "a > b", reason: "seo_text_invalid:title", limit: 70},
		{name: "line break", value: "two\nlines", reason: "seo_text_invalid:title", limit: 70},
		{name: "carriage return", value: "two\rlines", reason: "seo_text_invalid:title", limit: 70},
		{name: "tab", value: "tab\tinside", reason: "seo_text_invalid:title", limit: 70},
		{name: "next line", value: "two\u0085lines", reason: "seo_text_invalid:title", limit: 70},
		{name: "line separator", value: "two\u2028lines", reason: "seo_text_invalid:title", limit: 70},
		{name: "paragraph separator", value: "two\u2029lines", reason: "seo_text_invalid:title", limit: 70},
		{name: "non-breaking space", value: "Visual\u00a0Studio", want: "Visual\u00a0Studio", limit: 70},
		{name: "invalid utf8", value: "bad \xff byte", reason: "seo_text_invalid:title", limit: 70},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeSEOText(text(tc.value), tc.limit, "title")
			if tc.reason != "" {
				var app *domain.AppError
				require.ErrorAs(t, err, &app)
				require.Equal(t, domain.CodeValidation, app.Code)
				require.Equal(t, "rejected: "+tc.reason, app.Msg)
				return
			}
			require.NoError(t, err)
			if tc.want == "" {
				require.Nil(t, got)
				return
			}
			require.Equal(t, tc.want, *got)
		})
	}
	got, err := NormalizeSEOText(nil, 70, "title")
	require.NoError(t, err)
	require.Nil(t, got)
}
