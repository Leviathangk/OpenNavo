package agentgate

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMacScopeAndVersionClaims(t *testing.T) {
	for _, text := range []string{"## Windows installation\nInstructions", "## Android setup", "当前版本 2.0", "Latest 2.0", "We tested this app"} {
		require.Error(t, ValidateText(map[string]any{"description": text}, "1.0"), text)
	}
	for _, text := range []string{"## Mac and Windows comparison\nThe Mac app supports files from Windows.", "Version 1.0", "A subscription costs 2.50."} {
		require.NoError(t, ValidateText(map[string]any{"description": text}, "1.0"), text)
	}
	require.Error(t, ValidateText(map[string]any{"sections": []any{map[string]any{"title": "Android setup"}}}, "1.0"))
}

func TestEvidenceCannotAuthorizeUnrelatedValueOrRepository(t *testing.T) {
	e := Evidence{Excerpt: "The Professional edition costs 19.99 USD in US.", FetchedAt: time.Now()}
	require.NoError(t, bindValues(map[string]any{"amount": "19.99", "currency": "USD", "edition": "Professional", "region": "US"}, e))
	require.Error(t, bindValues(map[string]any{"amount": "9.99"}, e))
	for _, raw := range []string{"https://github.com/other/project/releases", "https://evil.example.test", "https://cdn.example.test/other.dmg"} {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		require.False(t, officialURL(u, "https://vendor.example.test", "owner/project", "https://cdn.example.test/app.dmg"))
	}
	for _, raw := range []string{"https://github.com/owner/project/releases", "https://raw.githubusercontent.com/owner/project/main/README.md", "https://docs.vendor.example.test/help"} {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		require.True(t, officialURL(u, "https://vendor.example.test", "owner/project", ""))
	}
}

func TestEvidenceHTMLIgnoresHiddenInstructions(t *testing.T) {
	text, err := visibleText(`<h1>Mac editor</h1><script>verified free</script><p hidden>unlimited</p><p aria-hidden="true">safe</p><p>Price: USD 20</p>`)
	require.NoError(t, err)
	require.Equal(t, "Mac editor Price: USD 20", normalizeText(text))
}

func TestUnregisteredWritesFailClosed(t *testing.T) {
	for _, op := range []string{"UpdateSeoSettings", "SetPackageIndexStatus", "UpdateAgentSettings", "ImaginaryWrite"} {
		_, ok := RuleFor(op)
		require.False(t, ok)
	}
	for _, op := range []string{"FixTranslation", "PublishArticle", "SetPackagePrice", "SetPackageFactRecords", "AddLinkPackage", "RestoreRevision", "UploadAssetWithTicket"} {
		_, ok := RuleFor(op)
		require.True(t, ok)
	}
}

func TestNestedProofsBindValuesAndKeepServerProvenance(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	e := Evidence{SourceURL: "https://vendor.example/pricing", FinalURL: "https://vendor.example/pricing", Excerpt: "The Professional edition costs 20 USD", FetchedAt: now, ValidUntil: now.Add(time.Hour)}
	proof := func() map[string]any {
		return map[string]any{"sourceUrl": e.SourceURL, "status": "verified", "checkedAt": "2099-01-01T00:00:00Z"}
	}
	bad := map[string]any{"kind": "attribute", "value": map[string]any{"amount": int64(5)}, "evidence": proof()}
	require.Error(t, normalizeNestedProofs(bad, []Evidence{e}))
	good := map[string]any{"kind": "attribute", "key": "price", "value": map[string]any{"amount": int64(20)}, "evidence": proof()}
	require.NoError(t, normalizeNestedProofs(good, []Evidence{e}))
	require.Equal(t, now.Format(time.RFC3339), good["evidence"].(map[string]any)["checkedAt"])
}
