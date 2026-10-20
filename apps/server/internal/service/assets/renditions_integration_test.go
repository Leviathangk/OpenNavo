//go:build integration

package assets

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/maintenance"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestStoredRenditionsPublicFallbackAndCleanup(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	objects := &fakeObjects{files: map[string][]byte{}}
	service := &Service{Store: st, Objects: objects}
	input := fixturePNG(t)
	icon, err := service.Upload(ctx, "icon", input, nil, store.AuditActor{})
	require.NoError(t, err)
	screenshot, err := service.Upload(ctx, "screenshot", input, nil, store.AuditActor{})
	require.NoError(t, err)
	for _, id := range []int64{icon.Id, screenshot.Id} {
		asset, err := st.AssetByID(ctx, id)
		require.NoError(t, err)
		var files []store.AssetFile
		require.NoError(t, json.Unmarshal(asset.Files, &files))
		expected := []string{}
		for _, f := range files {
			require.NotEmpty(t, objects.files[f.StorageKey])
			expected = append(expected, f.StorageKey)
			if f.Role == "original" {
				require.Equal(t, input, objects.files[f.StorageKey])
			}
		}
		require.ElementsMatch(t, expected, maintenance.VariantKeys(asset))
	}
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"renditions-fixture","name":["Fixture"],"version":"1"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	row, err := st.CatalogRow(ctx, "cask", p.Token)
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("INSERT INTO package_meta(package_id,icon_asset_id) VALUES(?,?)", row.ID, icon.Id).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO package_screenshots(package_id,asset_id) VALUES(?,?)", row.ID, screenshot.Id).Error)
	public, err := st.PublicPackage(ctx, "cask", p.Token, "en-US")
	require.NoError(t, err)
	summaries := catalog.Summaries([]store.PublicPackage{public})
	require.Len(t, *summaries[0].IconVariants, 3)
	raw, err := st.PublicScreenshots(ctx, row.ID, "en-US")
	require.NoError(t, err)
	var shots []struct{ Variants, ThumbnailVariants []store.ImageVariant }
	require.NoError(t, json.Unmarshal(raw, &shots))
	require.Len(t, shots[0].Variants, 3)
	require.Len(t, shots[0].ThumbnailVariants, 2)
	for i, variant := range shots[0].ThumbnailVariants {
		require.Equal(t, 318*(i+1), variant.Width)
		require.Equal(t, 200*(i+1), variant.Height)
		require.Equal(t, "image/webp", variant.Type)
		key := strings.TrimPrefix(variant.URL, "https://cdn.example.test/")
		require.NotEmpty(t, objects.files[key], "public URL must name an uploaded object")
	}
	// A referenced image cannot lose either originals or variants during pruning.
	require.NoError(t, st.DB.Exec("UPDATE assets SET created_at=now()-interval '60 days'").Error)
	removed := []string{}
	n, err := st.PruneAssets(ctx, time.Now().AddDate(0, 0, -30), func(_ context.Context, a store.Asset) error {
		removed = append(removed, maintenance.VariantKeys(a)...)
		return nil
	})
	require.NoError(t, err)
	require.Zero(t, n)
	require.Empty(t, removed)
	require.NoError(t, st.DB.Exec("UPDATE assets SET files='[]'").Error)
	public, err = st.PublicPackage(ctx, "cask", p.Token, "en-US")
	require.NoError(t, err)
	require.Empty(t, public.IconVariants)
	require.NotNil(t, public.IconURL)
	raw, err = st.PublicScreenshots(ctx, row.ID, "en-US")
	require.NoError(t, err)
	require.Contains(t, string(raw), `"variants": []`)
	require.Contains(t, string(raw), `"thumbnailVariants": []`)
	// Object failure must not persist a partial manifest.
	count := func() int64 { var n int64; require.NoError(t, st.DB.Table("assets").Count(&n).Error); return n }
	before := count()
	objects.err = errors.New("fixture upload failure")
	_, err = service.Upload(ctx, "icon", input, nil, store.AuditActor{})
	require.Error(t, err)
	require.Equal(t, before, count())
	objects.err = nil
	service.Objects = failSecondThumbnail{Objects: objects}
	_, err = service.Upload(ctx, "screenshot", input, nil, store.AuditActor{})
	require.ErrorContains(t, err, "fixture second thumbnail failure")
	require.Equal(t, before, count(), "a failed 2x upload must not publish a partial manifest")
	require.True(t, strings.HasPrefix(*public.IconURL, "https://cdn.example.test/"))
}

type failSecondThumbnail struct{ Objects }

func (f failSecondThumbnail) Put(ctx context.Context, key string, data []byte, mime string) error {
	if strings.Contains(key, "-thumbnail-") && strings.HasSuffix(key, "-636.webp") {
		return errors.New("fixture second thumbnail failure")
	}
	return f.Objects.Put(ctx, key, data, mime)
}
