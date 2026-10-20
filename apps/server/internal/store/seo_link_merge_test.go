package store

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMergePackageReferencesAreTypedAndBounded(t *testing.T) {
	for _, value := range []string{"pkg:12", "translate:package:12:en-US:hash", "collection_item:75:12"} {
		require.NotEqual(t, value, mergedEntity(value, 12, 4))
	}
	for _, value := range []string{"pkg:123", "notpackage:12", "package:12abc", "collection_item:12:75"} {
		require.Equal(t, value, mergedEntity(value, 12, 4))
	}
	raw := json.RawMessage(`{"packageId":12,"ref":{"entity":"collection_item","id":75,"secondaryId":12},"url":"https://example.org/12","id":12,"nested":[{"entity":"package","id":12}]}`)
	result, changed, err := mergedPackageJSON(raw, 12, 4)
	require.NoError(t, err)
	require.True(t, changed)
	require.JSONEq(t, `{"packageId":4,"ref":{"entity":"collection_item","id":75,"secondaryId":4},"url":"https://example.org/12","id":12,"nested":[{"entity":"package","id":4}]}`, string(result))
}
