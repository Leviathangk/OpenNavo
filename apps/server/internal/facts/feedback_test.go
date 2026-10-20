package facts

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFeedbackScrubsSecretsAndExecutableHTML(t *testing.T) {
	raw := `<script>alert('unsafe')</script><p>Crash on startup</p><img src=x onerror=evil()>
Authorization: Bearer fixture-auth-value
Cookie: session=fixture-cookie-value
api_key="fixture-key-value"
{"password":"fixture-password-value"}
/Users/example-person/Library/Logs/app.log
/home/example-person/.config
person@example.test
-----BEGIN PRIVATE KEY-----
fixture-key-body
-----END PRIVATE KEY-----
https://example-user:example-password@vendor.example/path`
	cleaned := CleanFeedback(raw)
	for _, secret := range []string{"unsafe", "evil", "fixture-", "example-person", "person@example", "example-user", "example-password", "<script", "<img"} {
		require.NotContains(t, cleaned, secret)
	}
	require.Contains(t, cleaned, "Crash on startup")
	require.Contains(t, cleaned, "[REDACTED]/Library/Logs/app.log")
	require.Equal(t, "a &lt; b &amp; c &gt; d", CleanFeedback("a < b & c > d"))
	require.NotContains(t, CleanFeedback(`api_<b>key</b>=fixture-split-secret`), "fixture-split-secret")
}
func TestFeedbackLinkLimits(t *testing.T) {
	require.NoError(t, FeedbackLinks([]string{"https://vendor.example/support"}))
	for _, links := range [][]string{{"http://vendor.example"}, {"https://user:pass@vendor.example"}, {"https://localhost/path"}, {"https://127.0.0.1"}, {"https://vendor.example/?token=fixture"}, {"https://vendor.example/#secret"}, {strings.Repeat("a", 2049)}, {"https://a.example", "https://b.example", "https://c.example", "https://d.example"}} {
		require.Error(t, FeedbackLinks(links))
	}
	require.NoError(t, FeedbackLinks([]string{"https://a.example", "https://b.example", "https://c.example"}))
	text := "Downloads fail: http://vendor.example/file?build=2#arm64 https://vendor.example/file?build=3#intel http://localhost/test https://a.example https://b.example"
	require.Equal(t, text, CleanFeedback(text))
}
