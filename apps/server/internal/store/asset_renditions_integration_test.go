//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestAssetRenditionSelectionAndAppendGuards(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	st := testutil.NewStore(t)
	asset, err := st.SaveAsset(ctx, store.Asset{Kind: "screenshot", StorageKey: "screenshots/fixture/primary.png", URL: "https://cdn.example.test/screenshots/fixture/primary.png", MIME: "image/png", Width: 256, Height: 256, Bytes: 42, SHA256: "fixture"}, nil)
	require.NoError(t, err)
	files := []store.AssetFile{}
	for _, spec := range store.RequiredAssetRenditions("screenshot") {
		files = append(files, store.AssetFile{Role: spec.Role, Type: spec.Type, Width: spec.Width, Height: max(1, spec.Height), StorageKey: "screenshots/fixture/variant.webp", URL: "https://cdn.example.test/variant.webp", Bytes: 12, SHA256: "fixture"})
	}
	// Go and PostgreSQL must agree on partially present sets, format, and crop.
	for _, tc := range []struct {
		name  string
		files []store.AssetFile
	}{
		{"empty", nil},
		{"responsive only", files[:3]},
		{"only 2x missing", files[:4]},
		{"complete", files},
		{"wrong crop", append(append([]store.AssetFile{}, files[:4]...), store.AssetFile{Role: "thumbnail", Type: "image/webp", Width: 636, Height: 399})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := tc.files
			if input == nil {
				input = []store.AssetFile{}
			}
			raw, err := json.Marshal(input)
			require.NoError(t, err)
			require.NoError(t, st.DB.Exec("UPDATE assets SET files=?::jsonb WHERE id=?", string(raw), asset.ID).Error)
			count, upper, err := st.AssetRenditionSnapshot(ctx, "screenshot")
			require.NoError(t, err)
			want := int64(0)
			if len(store.MissingAssetRenditions("screenshot", input)) > 0 {
				want = 1
			}
			require.Equal(t, want, count)
			ids, err := st.AssetRenditionPage(ctx, "screenshot", 0, upper, 10)
			require.NoError(t, err)
			require.Len(t, ids, int(want))
		})
	}
	require.NoError(t, st.DB.Exec("UPDATE assets SET files='[]' WHERE id=?", asset.ID).Error)
	result, err := st.AppendAssetRenditions(ctx, asset, files[:3])
	require.NoError(t, err)
	require.Equal(t, 3, result.Added)
	require.Equal(t, "prepared_renditions_mismatch", result.DeferredReason)
	// A stale snapshot is safe when only responsive entries changed. Existing
	// entries, duplicate prepared files and unrelated roles are all ignored.
	prepared := append(append([]store.AssetFile{}, files...), files[0], store.AssetFile{Role: "original", Type: "image/png", Width: 256})
	result, err = st.AppendAssetRenditions(ctx, asset, prepared)
	require.NoError(t, err)
	require.Equal(t, 2, result.Added)
	require.Empty(t, result.DeferredReason)
	result, err = st.AppendAssetRenditions(ctx, asset, prepared)
	require.NoError(t, err)
	require.Zero(t, result.Added)
	require.Empty(t, result.DeferredReason)
	current, err := st.AssetByID(ctx, asset.ID)
	require.NoError(t, err)
	var actual []store.AssetFile
	require.NoError(t, json.Unmarshal(current.Files, &actual))
	require.Equal(t, files, actual)

	// A changed original can retain the same primary asset and source key.
	// Its manifest hash must still prevent publishing stale prepared files.
	const original = `[{"role":"original","type":"image/png","storageKey":"screenshots/fixture/original.png","sha256":"before"}]`
	require.NoError(t, st.DB.Exec("UPDATE assets SET files=?::jsonb WHERE id=?", original, asset.ID).Error)
	asset, err = st.AssetByID(ctx, asset.ID)
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec(`UPDATE assets SET files=jsonb_set(files,'{0,sha256}','"after"'::jsonb) WHERE id=?`, asset.ID).Error)
	result, err = st.AppendAssetRenditions(ctx, asset, files)
	require.NoError(t, err)
	require.Zero(t, result.Added)
	require.Equal(t, "source_changed", result.DeferredReason)
	current, err = st.AssetByID(ctx, asset.ID)
	require.NoError(t, err)
	require.NotContains(t, string(current.Files), "responsive")
}
