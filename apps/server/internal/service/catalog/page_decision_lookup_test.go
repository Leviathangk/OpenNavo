package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitLocaleTakesTheLongestPrefix(t *testing.T) {
	for _, tc := range []struct{ path, locale, rest string }{
		{"/", "en-US", "/"},
		{"/apps/x", "en-US", "/apps/x"},
		{"/zh", "zh-CN", "/"},
		{"/zh/apps/x", "zh-CN", "/apps/x"},
		{"/zh-hant", "zh-Hant", "/"},
		{"/zh-hant/apps/x", "zh-Hant", "/apps/x"},
		{"/zhx/apps", "en-US", "/zhx/apps"},
		{"/pt/articles/a", "pt-BR", "/articles/a"},
	} {
		locale, rest := splitLocale(tc.path)
		require.Equal(t, tc.locale, locale, tc.path)
		require.Equal(t, tc.rest, rest, tc.path)
	}
	require.Equal(t, "/zh", localePath("zh-CN", "/"))
	require.Equal(t, "/", localePath("en-US", "/"))
	require.Equal(t, "/zh-hant/articles/a", localePath("zh-Hant", "/articles/a"))
}

// Labels encode like the web's labelPath: encodeURIComponent with @, + and : kept.
func TestBackgroundSegmentMatchesTheWebLabelPath(t *testing.T) {
	for label, want := range map[string]string{
		"homebrew.mxcl.postgresql@16": "homebrew.mxcl.postgresql@16",
		"com.example.helper":          "com.example.helper",
		"Example Login":               "Example%20Login",
		"a+b:c":                       "a+b:c",
		"it's (fine)!*~":              "it's%20(fine)!*~",
		"数据":                          "%E6%95%B0%E6%8D%AE",
		"x&y=z%":                      "x%26y%3Dz%25",
	} {
		require.Equal(t, want, backgroundSegment(label), label)
	}
}
