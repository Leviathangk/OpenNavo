//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestEveryContentFallbackRequestedEnglishSource(t *testing.T) {
	ctx := i18n.WithLocale(context.Background(), "ru-RU")
	st := testutil.NewStore(t)
	pkg, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"fallback","name":["Fallback"],"version":"1"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: pkg}}))
	var pid int64
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='fallback'").Scan(&pid).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO categories(id,slug,icon) VALUES(1,'fallback','code');
 INSERT INTO collections(id,slug,status) VALUES(1,'fallback','published');
 INSERT INTO features(id,placement,target_type,url,status) VALUES(1,'home_hero','url','https://example.com','published');
 INSERT INTO assets(id,kind,storage_key,url,mime,bytes,sha256,width,height) VALUES(1,'screenshot','fallback','https://example.com/f.png','image/png',1,'hash',100,100);
 INSERT INTO desktop_releases(id,version,artifacts) VALUES(1,'1','[]');
 INSERT INTO mirrors(id,key,probe_url) VALUES(1,'fallback','https://example.com');`).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO package_screenshots(id,package_id,asset_id) VALUES(1,?,1);", pid).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO collection_items(collection_id,package_id) VALUES(1,?)", pid).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO package_categories(package_id,category_id,is_primary,source) VALUES(?,1,true,'human')", pid).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO releases(id,package_id,source,source_key,version,source_locale) VALUES(1,?,'editorial','fallback','1','zh-CN')", pid).Error)
	require.NoError(t, st.DB.Exec("UPDATE packages SET source_locale='zh-CN' WHERE id=?", pid).Error)
	// These fixtures deliberately use Chinese source text, independently of creation defaults.
	for _, table := range []string{"categories", "collections", "features", "package_screenshots", "collection_items", "desktop_releases", "mirrors"} {
		require.NoError(t, st.DB.Table(table).Where("1=1").Update("source_locale", "zh-CN").Error)
	}
	tables := []struct {
		table, key, field string
		id                int64
	}{
		{"package_i18n", "package_id", "display_name", pid}, {"category_i18n", "category_id", "name", 1},
		{"collection_i18n", "collection_id", "title", 1}, {"feature_i18n", "feature_id", "title", 1},
		{"screenshot_i18n", "screenshot_id", "caption", 1}, {"collection_item_i18n", "collection_id", "note", 1},
		{"desktop_release_i18n", "desktop_release_id", "notes", 1}, {"mirror_i18n", "mirror_id", "name", 1},
		{"release_i18n", "release_id", "summary", 1},
	}
	for _, table := range tables {
		require.NoError(t, st.DB.Exec("DELETE FROM "+table.table).Error)
		for _, locale := range []string{"ru-RU", "en-US", "zh-CN"} {
			row := map[string]any{table.key: table.id, "locale": locale, table.field: "text-" + locale, "status": "manual", "source_locale": "zh-CN"}
			if table.table == "package_i18n" {
				row["source"] = "human"
			}
			if table.table == "collection_item_i18n" {
				row["package_id"] = pid
			}
			require.NoError(t, st.DB.Table(table.table).Create(row).Error)
		}
	}
	require.NoError(t, st.DB.Exec("INSERT INTO app_config(key,value) VALUES('desktop.announcement','{\"sourceLocale\":\"zh-CN\"}') ON CONFLICT(key) DO NOTHING").Error)
	require.NoError(t, st.DB.Exec("INSERT INTO announcement_i18n(config_key,locale,title,status,source_locale) SELECT 'desktop.announcement',locale,'text-'||locale,'manual','zh-CN' FROM unnest(ARRAY['ru-RU','en-US','zh-CN']) locale ON CONFLICT(config_key,locale) DO UPDATE SET title=excluded.title,source_locale=excluded.source_locale").Error)
	source, err := st.ReadContentSource(ctx, content.Ref{Entity: "release", ID: 1})
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("UPDATE release_i18n SET source_hash=?", source.Hash).Error)
	loaders := map[string]func() (any, error){
		"package":         func() (any, error) { return st.PublicPackage(ctx, "cask", "fallback", "ru-RU") },
		"category":        func() (any, error) { return st.PublicCategories(ctx, "ru-RU") },
		"collection":      func() (any, error) { return st.PublicCollection(ctx, "fallback", "ru-RU", time.Now()) },
		"feature":         func() (any, error) { return st.PublicFeatures(ctx, "ru-RU", time.Now()) },
		"screenshot":      func() (any, error) { return st.PublicScreenshots(ctx, pid, "ru-RU") },
		"collection_item": func() (any, error) { return st.CollectionItems(ctx, "fallback", "ru-RU") },
		"mirror":          func() (any, error) { return st.PublicMirrors(ctx, "ru-RU") },
		"release":         func() (any, error) { return st.TimelineEntries(ctx, pid, "ru-RU") },
		"announcement":    func() (any, error) { return st.LocalizeAnnouncement(ctx, "ru-RU") },
		"desktop_release": func() (any, error) {
			r, err := st.DesktopRelease(ctx, 1)
			text, _ := r.LocalizedNotes("ru-RU")
			return text, err
		},
	}
	for _, expected := range []string{"ru-RU", "en-US", "zh-CN"} {
		for name, load := range loaders {
			t.Run(name+"/"+expected, func(t *testing.T) {
				value, err := load()
				require.NoError(t, err)
				data, err := json.Marshal(value)
				require.NoError(t, err)
				if name == "release" && expected != "ru-RU" {
					require.NotContains(t, string(data), "text-", "editorial notes never fall back to another language")
					require.Contains(t, string(data), `"status":"pending"`)
				} else {
					require.Contains(t, string(data), "text-"+expected)
				}
			})
		}
		t.Run("suggestions/"+expected, func(t *testing.T) {
			items, err := st.Suggest(ctx, "fall", "ru-RU", 8)
			require.NoError(t, err)
			require.Len(t, items, 2)
			require.Equal(t, "package", items[0].Type)
			require.Equal(t, "category", items[1].Type)
			for _, item := range items {
				require.Equal(t, "text-"+expected, item.Name)
			}
		})
		if expected != "zh-CN" {
			for _, table := range tables {
				require.NoError(t, st.DB.Exec("DELETE FROM "+table.table+" WHERE locale=?", expected).Error)
			}
			require.NoError(t, st.DB.Exec("DELETE FROM announcement_i18n WHERE locale=?", expected).Error)
		}
	}
}
