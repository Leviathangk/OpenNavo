package agentgate

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOfficialSourcesStayWithinTheRegisteredProject(t *testing.T) {
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
	for _, op := range []string{"UpdateSeoSettings", "SetPackageIndexStatus", "UpdateAgentSettings", "ImaginaryWrite", "UpsertSeoKeywords", "CompensateSeoAction", "ProposeSnippetTemplate", "UpsertSeoListingCandidate"} {
		_, ok := RuleFor(op)
		require.False(t, ok, op)
	}
	for _, op := range []string{"FixTranslation", "PublishArticle", "SetPackagePrice", "SetPackageFactRecords", "AddLinkPackage", "RestoreRevision", "UploadAssetWithTicket"} {
		_, ok := RuleFor(op)
		require.True(t, ok, op)
	}
}
