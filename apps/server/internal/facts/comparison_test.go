package facts

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrationDocumentsStayWithinOfficialSource(t *testing.T) {
	bases := []string{"https://vendor.example", "https://github.com/example/editor"}
	require.True(t, OfficialDocument("https://docs.vendor.example/import", bases))
	require.True(t, OfficialDocument("https://github.com/example/editor/wiki/import", bases))
	for _, u := range []string{"https://vendor.example.evil.test/import", "https://github.com/attacker/editor/wiki/import", "https://github.com/example/editor-fork/wiki/import", "https://attacker@vendor.example/import", "javascript:alert(1)"} {
		require.False(t, OfficialDocument(u, bases), u)
	}
}
func TestComparisonAttributesDoNotMixTypes(t *testing.T) {
	value := true
	require.NoError(t, (Attribute{Name: "offline", BooleanValue: &value}).Validate())
	require.ErrorIs(t, (Attribute{Name: "offline", Formats: []string{"Markdown"}}).Validate(), ErrInvalid)
	require.ErrorIs(t, (Attribute{Name: "price", BooleanValue: &value}).Validate(), ErrInvalid)
	require.NoError(t, (Attribute{Name: "export_formats", Formats: []string{"Markdown"}}).Validate())
}
