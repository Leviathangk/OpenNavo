package i18n

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNegotiationAndFallback(t *testing.T) {
	for _, code := range SiteLocales {
		require.Equal(t, string(code), Negotiate(string(code), "ru-RU"))
		require.Equal(t, string(code), Negotiate("", string(code)))
	}
	for _, test := range []struct{ query, header, want string }{
		{"", "", "en-US"}, {"", "fr-FR", "fr-FR"}, {"", "ja;q=0.3,pt-PT;q=0.9,en;q=0.8", "pt-BR"},
		{"", "zh-TW", "zh-Hant"}, {"", "zh-HK", "zh-Hant"}, {"", "zh-MO", "zh-Hant"}, {"", "zh-SG", "zh-CN"},
		{"", "ru;q=0,zh-Hans;q=0.5", "zh-CN"}, {"", "es-MX;q=0.8,ja;q=0.8", "es-ES"},
		{"ja-JP", "ru;q=1", "ja-JP"}, {"", "ja;q=invalid,ru;q=0.5", "ru-RU"}, {"", "ja;q=NaN,ru;q=0.5", "ru-RU"},
	} {
		require.Equal(t, test.want, Negotiate(test.query, test.header))
	}
	require.Equal(t, []string{"ja-JP", "en-US", "zh-CN"}, Fallback("ja-JP", "zh-CN"))
	require.Equal(t, []string{"en-US"}, Fallback("en-US", "en-US"))
}
func TestAllErrorResourcesHaveSameNonemptyCodes(t *testing.T) {
	reference := messages[string(DefaultLocale)]
	require.Len(t, reference, 19)
	for _, locale := range Locales {
		data, err := messageFiles.ReadFile("messages/" + string(locale) + ".json")
		require.NoError(t, err)
		var got map[string]string
		require.NoError(t, json.Unmarshal(data, &got))
		require.Len(t, got, len(reference))
		for code := range reference {
			require.NotEmpty(t, got[code])
			require.Equal(t, got[code], Message(code, string(locale)))
		}
	}
	require.Equal(t, messages["en-US"]["5000"], Message("unknown", "unsupported"))
	for _, locale := range []string{"de-DE", "fr-FR", "it-IT", "zh-Hant"} {
		require.True(t, Valid(locale))
		require.False(t, ValidApp(locale))
		require.Equal(t, messages["en-US"]["5000"], Message("5000", locale))
	}
}

func TestSharedLocaleCases(t *testing.T) {
	data, err := os.ReadFile("../../testdata/locale_cases.json")
	require.NoError(t, err)
	var cases []struct {
		Name      string   `json:"name"`
		Languages []string `json:"languages"`
		Expected  string   `json:"expected"`
	}
	require.NoError(t, json.Unmarshal(data, &cases))
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			require.Equal(t, test.Expected, NegotiateApp("", strings.Join(test.Languages, ",")))
		})
	}
}
