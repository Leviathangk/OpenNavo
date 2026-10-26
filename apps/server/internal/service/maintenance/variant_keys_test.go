package maintenance

import (
	"encoding/json"
	"testing"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

func TestVariantKeysAfterLegacyBackfill(t *testing.T) {
	for _, tc := range []struct {
		kind             string
		width            int
		primary, sibling string
	}{
		{"icon", 256, "icons/legacy/hash-256.png", "icons/legacy/hash-512.png"},
		{"screenshot", 1280, "screenshots/legacy/hash-1280.png", "screenshots/legacy/hash-640.png"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			asset := store.Asset{Kind: tc.kind, MIME: "image/png", Width: tc.width, StorageKey: tc.primary, Files: json.RawMessage(`[]`)}
			require.ElementsMatch(t, []string{tc.primary, tc.sibling}, VariantKeys(asset))
			asset.Files = json.RawMessage(`[{"role":"responsive","storageKey":"rendition.webp"},{"role":"thumbnail","storageKey":"thumbnail.webp"},{"role":"responsive","storageKey":"rendition.webp"}]`)
			require.ElementsMatch(t, []string{tc.primary, tc.sibling, "rendition.webp", "thumbnail.webp"}, VariantKeys(asset))
		})
	}
}

func TestVariantKeysCompleteManifestDoesNotInventSibling(t *testing.T) {
	asset := store.Asset{Kind: "icon", MIME: "image/png", Width: 256, StorageKey: "icons/new/hash-256.png", Files: json.RawMessage(`[{"role":"normalized","storageKey":"icons/new/hash-256.png"},{"role":"original","storageKey":"source.jpg"}]`)}
	require.ElementsMatch(t, []string{asset.StorageKey, "source.jpg"}, VariantKeys(asset))
}
