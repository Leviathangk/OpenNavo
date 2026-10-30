package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHistoryPatchPreservesFieldsOutsideOriginalMutation(t *testing.T) {
	before := HistoryState{"package_meta": {}}
	after := HistoryState{"package_meta": {{"package_id": float64(1), "icon_asset_id": float64(4), "notes": nil, "hidden": false, "private": false, "visibility_reason": "", "editor_choice": false, "tags": []any{}}}}
	current := HistoryState{"package_meta": {{"package_id": float64(1), "icon_asset_id": float64(8), "notes": "later private editorial notes", "hidden": true, "private": true, "visibility_reason": "Later privacy decision", "editor_choice": false, "tags": []any{"later"}}}}
	patched := HistoryPatch(current, before, after, after)
	require.Equal(t, float64(4), patched["package_meta"][0]["icon_asset_id"])
	require.Equal(t, "later private editorial notes", patched["package_meta"][0]["notes"])
	require.Equal(t, true, patched["package_meta"][0]["hidden"])
	require.Equal(t, true, patched["package_meta"][0]["private"])
	require.Equal(t, "Later privacy decision", patched["package_meta"][0]["visibility_reason"])
	require.Equal(t, []any{"later"}, patched["package_meta"][0]["tags"])
	require.Equal(t, float64(8), current["package_meta"][0]["icon_asset_id"])
}

func TestHistoryPatchCreationPreservesPublicationAndLaterRows(t *testing.T) {
	created := HistoryState{
		"collections":          {{"id": float64(1), "slug": "original", "status": "draft", "publish_at": nil, "unpublish_at": nil, "updated_by": float64(1)}},
		"collection_i18n":      {{"collection_id": float64(1), "locale": "zh-CN", "title": "原始标题"}},
		"collection_items":     {},
		"collection_item_i18n": {},
	}
	for _, status := range []string{"published", "scheduled", "archived"} {
		t.Run(status, func(t *testing.T) {
			current := HistoryState{
				"collections":          {{"id": float64(1), "slug": "later", "status": status, "publish_at": "2026-10-07T00:00:00Z", "unpublish_at": "2026-10-10T00:00:00Z", "updated_by": float64(2)}},
				"collection_i18n":      {{"collection_id": float64(1), "locale": "zh-CN", "title": "后来的标题"}, {"collection_id": float64(1), "locale": "en-US", "title": "Later translation"}},
				"collection_items":     {{"collection_id": float64(1), "package_id": float64(2), "sort": float64(0)}},
				"collection_item_i18n": {{"collection_id": float64(1), "package_id": float64(2), "locale": "zh-CN", "note": "后来添加的推荐语"}},
			}
			patched := HistoryPatch(current, nil, created, created)
			require.Equal(t, "original", patched["collections"][0]["slug"])
			for _, field := range []string{"status", "publish_at", "unpublish_at", "updated_by"} {
				require.Equal(t, current["collections"][0][field], patched["collections"][0][field])
			}
			require.Equal(t, current["collection_items"], patched["collection_items"])
			require.Equal(t, current["collection_item_i18n"], patched["collection_item_i18n"])
			translations := historyRows(patched["collection_i18n"])
			for key, row := range historyRows(created["collection_i18n"]) {
				require.Equal(t, row["title"], translations[key]["title"])
			}
			require.Len(t, translations, 2)
			require.Equal(t, "later", current["collections"][0]["slug"])
			require.Equal(t, "draft", created["collections"][0]["status"])
		})
	}
	// Object recreation and undoing creation retain the original full-snapshot/deletion semantics.
	require.Equal(t, created, HistoryPatch(nil, nil, created, created))
	require.Nil(t, HistoryPatch(created, nil, created, nil))
}
