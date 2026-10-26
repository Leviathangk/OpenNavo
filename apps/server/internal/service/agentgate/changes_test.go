package agentgate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChangedFieldsReportsActualPaths(t *testing.T) {
	before := map[string]any{"summary": "before", "price": 10, "tags": []any{"a", "b"}}
	after := map[string]any{"summary": "after", "price": 10, "tags": []any{"a", "c"}, "a/b": "new"}
	require.Equal(t, []string{"/a~1b", "/summary", "/tags/1"}, ChangedFields(before, after))
	require.Empty(t, ChangedFields(after, after))
}
