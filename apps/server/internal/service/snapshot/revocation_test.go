package snapshot

import (
	"bytes"
	"compress/gzip"
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

// The self-check compares tokens, not text: a private token that is a prefix of a public one, or that
// appears in ordinary copy, must not block a revocation forever (#88 F-01).
func TestBundleContainsTokenMatchesExactTokensOnly(t *testing.T) {
	for _, test := range []struct {
		name      string
		private   string
		item      map[string]any
		documents []map[string]any
	}{
		{
			name:    "public token extends the private one",
			private: "1password",
			item:    map[string]any{"token": "1password-cli", "homepage": "https://1password.com"},
		},
		{
			name:    "public token starts with the private one",
			private: "ray",
			item:    map[string]any{"token": "raycast", "summary": "Launcher for ray"},
		},
		{
			name:    "private token appears in display copy",
			private: "obs",
			item:    map[string]any{"token": "obsidian", "displayName": map[string]any{"en-US": "Obsidian for obs"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundle := testBundle(t, test.item)
			_, found := bundleContainsToken(bundle, []string{test.private})
			require.False(t, found, "a public item must not be read as its private neighbour")
			// The same bundle still blocks when the public token itself turns private.
			token, found := bundleContainsToken(bundle, []string{test.item["token"].(string)})
			require.True(t, found)
			require.Equal(t, test.item["token"], token)
		})
	}
}

// Privacy is per catalog token: `aerial@beta` is a directory entry of its own, so a private base
// token does not cover it and a private variant does not cover the base entry. The directory filter
// makes the same exact-token comparison, and the self-check must not disagree with it.
func TestBundleContainsTokenTreatsVariantAsSeparateToken(t *testing.T) {
	bundle := testBundle(t, map[string]any{"token": "aerial@beta"}, map[string]any{"token": "aerial@nightly"})
	_, found := bundleContainsToken(bundle, []string{"aerial"})
	require.False(t, found, "a private base token must not block a public variant that is still published")
	token, found := bundleContainsToken(bundle, []string{"aerial@nightly"})
	require.True(t, found)
	require.Equal(t, "aerial@nightly", token)
	_, found = bundleContainsToken(bundle, []string{"aerial@insiders"})
	require.False(t, found)
	// A private variant leaves the public base entry alone in the same way.
	base := testBundle(t, map[string]any{"token": "aerial"})
	_, found = bundleContainsToken(base, []string{"aerial@beta"})
	require.False(t, found, "a private variant must not block the public base entry")
	token, found = bundleContainsToken(base, []string{"aerial"})
	require.True(t, found)
	require.Equal(t, "aerial", token)
}

func TestBundleContainsTokenFailsClosedOnUnreadableDocuments(t *testing.T) {
	valid, err := compress(map[string]any{"items": []map[string]any{{"token": "snapshot-public"}}})
	require.NoError(t, err)
	for name, data := range map[string][]byte{
		"not gzip":       []byte(`{"items":[{"token":"snapshot-public"}]}`),
		"truncated gz":   {0x1f, 0x8b, 0x08, 0x00},
		"gzip, not json": gzipOf(t, []byte("not a document")),
		"wrong shape":    gzipOf(t, []byte(`{"items":[{"token":7}]}`)),
	} {
		t.Run(name, func(t *testing.T) {
			token, found := bundleContainsToken(Bundle{Files: []File{{Key: "snapshots/modern-base-v2.json.gz", Data: data}}}, []string{"snapshot-private"})
			require.True(t, found)
			require.Equal(t, "snapshots/modern-base-v2.json.gz", token)
		})
	}
	bundle := Bundle{Files: []File{{Key: "snapshots/modern-base-v2.json.gz", Data: valid}}}
	_, found := bundleContainsToken(bundle, []string{"snapshot-private"})
	require.False(t, found)
	// A non-gzip file is only accepted when the bundle really has no private token to look for.
	bundle.Files = append(bundle.Files, File{Key: "snapshots/latest.json", Data: []byte(`{}`)})
	_, found = bundleContainsToken(bundle, nil)
	require.False(t, found)
}

// The build writes base, legacy base and ten text packs; every document that lists items must be
// checked, because a token hidden in one pack alone would otherwise survive.
func TestBundleContainsTokenChecksEveryDocument(t *testing.T) {
	clean, err := compress(map[string]any{"items": []map[string]any{{"token": "snapshot-public"}}})
	require.NoError(t, err)
	leaky, err := compress(map[string]any{"items": []map[string]any{{"token": "snapshot-private"}}})
	require.NoError(t, err)
	bundle := Bundle{Files: []File{
		{Key: "snapshots/modern-base-v2-1.json.gz", Data: clean},
		{Key: "snapshots/text-v2-en-US-1.json.gz", Data: clean},
		{Key: "snapshots/text-v2-ja-JP-1.json.gz", Data: leaky},
	}}
	token, found := bundleContainsToken(bundle, []string{"snapshot-private"})
	require.True(t, found)
	require.Equal(t, "snapshot-private", token)

	tokens, err := bundleDocumentTokens(File{Key: "snapshots/text-v2-ja-JP-1.json.gz", Data: leaky})
	require.NoError(t, err)
	require.Equal(t, []string{"snapshot-private"}, tokens)
}

func testBundle(t *testing.T, items ...map[string]any) Bundle {
	t.Helper()
	base, err := compress(map[string]any{"formatVersion": 2, "items": items})
	require.NoError(t, err)
	pack, err := compress(map[string]any{"formatVersion": 2, "locale": "en-US", "items": items})
	require.NoError(t, err)
	return Bundle{Files: []File{
		{Key: "snapshots/modern-base-v2-1.json.gz", Data: base},
		{Key: "snapshots/text-v2-en-US-1.json.gz", Data: pack},
	}}
}

func gzipOf(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	_, err := writer.Write(raw)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}
