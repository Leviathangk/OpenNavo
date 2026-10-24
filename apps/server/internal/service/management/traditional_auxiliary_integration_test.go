//go:build integration

package management

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestTraditionalAuxiliaryWritesBackfillAndRollback(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(100,'cask','auxiliary-fixture','auxiliary-fixture','homebrew/cask','Fixture','1','1','{}','fixture');
 INSERT INTO background_items(label) VALUES('com.example.auxiliary');
 INSERT INTO background_item_packages(label,package_id,kind,phase,source_url,checked_at) VALUES('com.example.auxiliary',100,'launchctl','uninstall','https://example.com/docs',now());
 INSERT INTO collections(id,slug,source_locale) VALUES(100,'auxiliary-fixture','en-US');`).Error)
	proof := map[string]any{"sourceUrl": "https://example.com/docs", "checkedAt": time.Now().UTC().Truncate(time.Second), "status": "verified"}
	for _, tc := range []struct {
		entity, op, table, key, field string
		input                         Input
	}{
		{"background_item", "SetBackgroundItem", "background_item_content", "label='com.example.auxiliary'", "description", Input{Key: "com.example.auxiliary", Body: map[string]any{"locale": "zh-CN", "description": "软件", "itemType": "launch_agent", "commonPaths": []string{"/Library/软件.plist"}, "evidence": proof}}},
		{"collection_setup", "SetCollectionSetupCriteria", "collection_setup_criteria", "collection_id=100", "criteria", Input{ID: 100, Body: map[string]any{"locale": "zh-CN", "criteria": "软件", "evidence": proof}}},
	} {
		t.Run(tc.entity, func(t *testing.T) {
			read := func() string {
				var value string
				require.NoError(t, st.DB.Raw("SELECT "+tc.field+" FROM "+tc.table+" WHERE "+tc.key+" AND locale='zh-Hant'").Scan(&value).Error)
				return value
			}
			for _, text := range []string{"软件", "鼠标"} {
				tc.input.Body[tc.field] = text
				_, err := svc.Execute(ctx, tc.op, tc.input)
				require.NoError(t, err)
			}
			require.Equal(t, "滑鼠", read())
			var matched bool
			require.NoError(t, st.DB.Raw("SELECT a.evidence=b.evidence FROM "+tc.table+" a JOIN "+tc.table+" b USING ("+map[string]string{"background_item": "label", "collection_setup": "collection_id"}[tc.entity]+") WHERE a.locale='zh-CN' AND b.locale='zh-Hant'").Scan(&matched).Error)
			require.True(t, matched)
			if tc.entity == "background_item" {
				var paths string
				require.NoError(t, st.DB.Raw("SELECT common_paths::text FROM background_item_content WHERE locale='zh-Hant'").Scan(&paths).Error)
				require.JSONEq(t, `["/Library/软件.plist"]`, paths)
			}
			tc.input.Body["locale"] = "zh-Hant"
			_, err := svc.Execute(ctx, tc.op, tc.input)
			require.ErrorContains(t, err, "derived_locale_read_only")
			// Simulate a pre-migration manual row, then verify preview and original backup.
			require.NoError(t, st.DB.Exec("DELETE FROM derived_traditional WHERE entity=?", tc.entity).Error)
			require.NoError(t, st.DB.Exec("UPDATE "+tc.table+" SET "+tc.field+"='舊譯文' WHERE "+tc.key+" AND locale='zh-Hant'").Error)
			counts, err := st.BackfillTraditional(ctx, true)
			require.NoError(t, err)
			require.Equal(t, 1, counts[tc.entity])
			require.Equal(t, "舊譯文", read())
			counts, err = st.BackfillTraditional(ctx, false)
			require.NoError(t, err)
			require.Equal(t, 1, counts[tc.entity])
			require.Equal(t, "滑鼠", read())
			counts, err = st.BackfillTraditional(ctx, false)
			require.NoError(t, err)
			require.Empty(t, counts)
			_, err = st.RollbackTraditional(ctx, false)
			require.NoError(t, err)
			require.Equal(t, "舊譯文", read())
			require.NoError(t, st.DB.Exec("UPDATE "+tc.table+" SET "+tc.field+"='新人工譯文' WHERE "+tc.key+" AND locale='zh-Hant'").Error)
			_, err = st.BackfillTraditional(ctx, false)
			require.NoError(t, err)
			_, err = st.RollbackTraditional(ctx, false)
			require.NoError(t, err)
			require.Equal(t, "新人工譯文", read())
			// Keep the other subtest's backfill from touching this fixture again.
			require.NoError(t, st.DB.Exec("DELETE FROM "+tc.table+" WHERE "+tc.key).Error)
		})
	}
}
