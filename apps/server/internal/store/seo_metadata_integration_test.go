//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLocalizedSEOMetadataWritesTranslationsAndPublicReads(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	for _, entity := range []string{"category", "collection"} {
		t.Run(entity, func(t *testing.T) {
			var id int64
			query, label := "INSERT INTO categories(slug,visible,icon) VALUES('seo-fixture',true,'lucide:box') RETURNING id", "name"
			if entity == "collection" {
				query, label = "INSERT INTO collections(slug,status) VALUES('seo-fixture','published') RETURNING id", "title"
			}
			require.NoError(t, st.DB.Raw(query).Scan(&id).Error)
			ref := content.Ref{Entity: entity, ID: id}
			write := func(title, description any) {
				require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
					return (&store.Store{DB: tx}).WriteLocalized(ctx, entity, id, 0, map[string]any{
						"sourceLocale": "en-US", "i18n": map[string]any{"en-US": map[string]any{
							label: "Fixture", "seoTitle": title, "seoDescription": description,
						}},
					}, true)
				}))
			}
			write(nil, nil)
			legacyFields := map[string]any{label: "Fixture", "description": nil}
			if entity == "collection" {
				legacyFields = map[string]any{label: "Fixture", "subtitle": nil, "body": nil}
			}
			before, err := st.ReadContentSource(ctx, ref)
			require.NoError(t, err)
			require.Equal(t, content.Hash("en-US", legacyFields), before.Hash, "optional fields preserve existing translation provenance")
			write("English search title", "English search description")
			source, err := st.ReadContentSource(ctx, ref)
			require.NoError(t, err)
			require.NotEqual(t, before.Hash, source.Hash)
			require.Equal(t, "English search title", source.Fields["seo_title"])
			require.Equal(t, "English search description", source.Fields["seo_description"])
			require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
				return (&store.Store{DB: tx}).WriteContentText(ctx, ref, "ja-JP", map[string]any{
					label: "見出し", "seoTitle": "検索用タイトル", "seoDescription": "検索用の説明",
				}, false)
			}))
			read := func(locale string) (*string, *string) {
				if entity == "category" {
					rows, err := st.PublicCategories(ctx, locale)
					require.NoError(t, err)
					require.Len(t, rows, 1)
					return rows[0].SeoTitle, rows[0].SeoDescription
				}
				raw, err := st.PublicCollection(ctx, "seo-fixture", locale, time.Now())
				require.NoError(t, err)
				var detail publicapi.CollectionSummary
				require.NoError(t, json.Unmarshal(raw, &detail))
				return detail.SeoTitle, detail.SeoDescription
			}
			title, description := read("ja-JP")
			require.Equal(t, "検索用タイトル", *title)
			require.Equal(t, "検索用の説明", *description)
			title, description = read("de-DE")
			require.Equal(t, "English search title", *title)
			require.Equal(t, "English search description", *description)
			write(nil, nil)
			title, description = read("en-US")
			require.Nil(t, title)
			require.Nil(t, description)
			cleared, err := st.ReadContentSource(ctx, ref)
			require.NoError(t, err)
			require.Equal(t, before.Hash, cleared.Hash)
			require.Contains(t, cleared.Fields, "seo_title", "translation snapshots must carry cleared fields")
		})
	}
}
