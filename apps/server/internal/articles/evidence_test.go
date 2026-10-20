package articles

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaimsRequireWholeStatementsAndCompleteCoverage(t *testing.T) {
	for _, tc := range []struct {
		name, claim, source string
		want                bool
	}{
		{"negative", "Safe", "Not Safe.", false},
		{"clipped sentence", "The app is safe", "The app is safe only for offline files.", false},
		{"exact sentence", "The app supports macOS.", "Release 1.0. The app supports macOS. See the guide.", true},
		{"Chinese", "支持 macOS", "软件不支持 macOS。", false},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, CompleteAssertion(tc.claim, tc.source)) })
	}
	require.False(t, ClaimsCover("Not safe", []Claim{{Text: "Not"}, {Text: "safe"}}))
	claims := []Claim{{Text: "Supports macOS."}, {Text: "Supports macOS. Requires macOS 15."}}
	require.True(t, ClaimsCover("# Supports macOS. Requires macOS 15.\n\n::app[example-app]", claims))
	for _, text := range []string{"Supports macOS. It is safer.", "Not Supports macOS.", "Supports macOS.\n| Faster | yes |", "Supports macOS.\n![Unverified result](https://assets.example.test/x.png)"} {
		require.False(t, ClaimsCover(text, claims), text)
	}
	for _, text := range []string{"We tested it.", "Our tests show it is faster.", "我們實測比它更快。", "我们测试了这款应用。"} {
		require.True(t, TestingClaim(text))
	}
}
