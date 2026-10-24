//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestTranslationBackfillQueuesMissingSiteLanguagesOncePerLanguageSet(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO categories(id,slug,icon,source_locale) VALUES(901,'backfill-editors','lucide:box','en-US');
 INSERT INTO category_i18n(category_id,locale,name,source_locale,status) VALUES(901,'en-US','Editors','en-US','source');
 INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,source_locale) VALUES
  (902,'cask','backfill-app','backfill-app','homebrew/cask','Backfill App','1','1','{}','hash','en-US'),
  (903,'cask','backfill-hidden','backfill-hidden','homebrew/cask','Backfill Hidden','1','1','{}','hash','en-US');
 INSERT INTO package_meta(package_id,hidden) VALUES(903,true);
 INSERT INTO package_i18n(package_id,locale,source_locale,status,display_name,summary,description,source)
  SELECT id,'en-US','en-US','source','Backfill','A macOS editor','A longer description of a macOS editor.','human' FROM unnest(ARRAY[902,903]) id`).Error)
	// A current machine translation is already done; only the other languages are queued.
	source, err := st.ReadContentSource(ctx, content.Ref{Entity: "category", ID: 901})
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec(`INSERT INTO category_i18n(category_id,locale,name,source_locale,status,source_hash) VALUES(901,'zh-CN','编辑器','en-US','machine',?)`, source.Hash).Error)

	queued, ran, err := st.BackfillTranslationTasksForLocales(ctx)
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, 7, queued["category"])
	require.Equal(t, 8, queued["package"])
	var rows []struct {
		EntityKey, Locale string
		Priority          float64
	}
	require.NoError(t, st.DB.Raw("SELECT entity_key,locale,priority FROM seo_tasks WHERE kind='translate' ORDER BY entity_key,locale").Scan(&rows).Error)
	require.Len(t, rows, 15)
	for _, row := range rows {
		require.NotEqual(t, "package:903", row.EntityKey, "hidden packages are not translated")
		if row.EntityKey == "category:901" {
			require.NotEqual(t, "zh-CN", row.Locale)
			require.EqualValues(t, 100, row.Priority)
		}
	}

	queued, ran, err = st.BackfillTranslationTasksForLocales(ctx)
	require.NoError(t, err)
	require.False(t, ran, "the same language set is scanned once")
	require.Empty(t, queued)
	require.NoError(t, st.DB.Exec("UPDATE seo_opportunity_cursors SET signature='' WHERE source='translation_locales'").Error)
	queued, ran, err = st.BackfillTranslationTasksForLocales(ctx)
	require.NoError(t, err)
	require.True(t, ran)
	require.Zero(t, queued["category"]+queued["package"], "existing tasks are left as they are")
}
