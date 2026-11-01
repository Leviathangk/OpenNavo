package snapshot

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBundleContainsTokenChecksDecompressedFiles(t *testing.T) {
	pack, err := compress(map[string]any{"items": []map[string]any{{"token": "snapshot-private"}}})
	require.NoError(t, err)
	bundle := Bundle{Files: []File{{Key: "snapshots/text-v2-en-US.json.gz", Data: pack}}}
	token, found := bundleContainsToken(bundle, []string{"snapshot-public", "snapshot-private"})
	require.True(t, found)
	require.Equal(t, "snapshot-private", token)
	_, found = bundleContainsToken(bundle, []string{"snapshot-public"})
	require.False(t, found)
	_, found = bundleContainsToken(bundle, nil)
	require.False(t, found)
	// A corrupt pack fails closed rather than acknowledging a revocation without checking it.
	_, found = bundleContainsToken(Bundle{Files: []File{{Key: "broken.gz", Data: []byte{0x1f, 0x8b, 0x00}}}}, []string{"snapshot-private"})
	require.True(t, found)
}
