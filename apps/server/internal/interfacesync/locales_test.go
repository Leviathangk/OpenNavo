package interfacesync

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTargetLanguagePlanning(t *testing.T) {
	for _, name := range []string{"web", "shared", "desktop", "native", "admin", "server"} {
		t.Run(name, func(t *testing.T) {
			count := 6
			if name == "web" || name == "shared" || name == "desktop" || name == "native" {
				count = 10
			}
			require.Len(t, TargetLocales(name), count)
			target := Target{Source: map[string]string{"open": "打开"}, English: map[string]string{"open": "Open"}}
			batches := Batches(Plan{Targets: map[string]Target{name: target}})
			require.Len(t, batches, 1)
			expected := count - 2
			if count == 10 {
				expected--
			}
			require.Len(t, batches[0].Locales, expected)
			require.NotContains(t, batches[0].Locales, "zh-Hant")
			require.NotContains(t, batches[0].Locales, "en-US")
			require.NotContains(t, batches[0].Locales, "zh-CN")
			target.Locales = []string{"fr-FR", "ja-JP", "unknown"}
			batches = Batches(Plan{Targets: map[string]Target{name: target}})
			require.Len(t, batches, 1)
			if count == 10 {
				require.Equal(t, []string{"ja-JP", "fr-FR"}, batches[0].Locales)
			} else {
				require.Equal(t, []string{"ja-JP"}, batches[0].Locales)
			}
		})
	}
}
