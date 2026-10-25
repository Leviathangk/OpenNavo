//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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
	openLocales := func(list string) {
		t.Helper()
		require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}')||jsonb_build_object('openLocales',?::jsonb))`, list).Error)
	}
	tasks := func() map[string][]string {
		t.Helper()
		var rows []struct {
			EntityKey, Locale string
			Priority          float64
		}
		require.NoError(t, st.DB.Raw("SELECT entity_key,locale,priority FROM seo_tasks WHERE kind='translate' ORDER BY entity_key,locale").Scan(&rows).Error)
		out := map[string][]string{}
		for _, row := range rows {
			require.NotEqual(t, "package:903", row.EntityKey, "hidden packages are not translated")
			if row.EntityKey == "category:901" {
				require.EqualValues(t, 100, row.Priority)
			}
			out[row.EntityKey] = append(out[row.EntityKey], row.Locale)
		}
		return out
	}

	// Languages closed for indexing get no new work.
	openLocales(`["en-US","zh-CN","ja-JP","ru-RU","es-ES"]`)
	queued, ran, err := st.BackfillTranslationTasksForLocales(ctx)
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, 3, queued["category"])
	require.Equal(t, 4, queued["package"])
	require.Equal(t, map[string][]string{"category:901": {"es-ES", "ja-JP", "ru-RU"}, "package:902": {"es-ES", "ja-JP", "ru-RU", "zh-CN"}}, tasks())

	queued, ran, err = st.BackfillTranslationTasksForLocales(ctx)
	require.NoError(t, err)
	require.False(t, ran, "the same language set is scanned once")
	require.Empty(t, queued)
	// Opening every language (no list) is a new set: the next start queues the opened languages.
	openLocales("null")
	queued, ran, err = st.BackfillTranslationTasksForLocales(ctx)
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, 4, queued["category"])
	require.Equal(t, 4, queued["package"])
	all := tasks()
	require.Len(t, all["category:901"], 7)
	require.NotContains(t, all["category:901"], "zh-CN")
	require.NotContains(t, all["category:901"], "zh-Hant", "Traditional Chinese is derived, never queued")
	require.Len(t, all["package:902"], 8)
	require.NoError(t, st.DB.Exec("UPDATE seo_opportunity_cursors SET signature='' WHERE source='translation_locales'").Error)
	queued, ran, err = st.BackfillTranslationTasksForLocales(ctx)
	require.NoError(t, err)
	require.True(t, ran)
	require.Zero(t, queued["category"]+queued["package"], "existing tasks are left as they are")
}

func TestSourceEditsQueueTranslationsOnlyForOpenLocales(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO categories(id,slug,icon,source_locale) VALUES(911,'open-editors','lucide:box','en-US');
 INSERT INTO category_i18n(category_id,locale,name,source_locale,status) VALUES(911,'en-US','Editors','en-US','source');
 INSERT INTO category_i18n(category_id,locale,name,source_locale,status,source_hash) VALUES(911,'de-DE','Editoren','en-US','machine','old');
 INSERT INTO desktop_releases(id,version,artifacts,source_locale) VALUES(912,'1.0','[]','en-US');
 INSERT INTO desktop_release_i18n(desktop_release_id,locale,notes,status,source_locale) VALUES(912,'en-US','Release notes','source','en-US');
 UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}')||'{"openLocales":["en-US","zh-CN","ja-JP","ru-RU","es-ES"]}'::jsonb)`).Error)
	queued := func(entity string) []string {
		t.Helper()
		var locales []string
		require.NoError(t, st.DB.Raw("SELECT locale FROM seo_tasks WHERE kind='translate' AND entity_key=? AND state='open' ORDER BY locale", entity).Scan(&locales).Error)
		return locales
	}
	for _, ref := range []content.Ref{{Entity: "category", ID: 911}, {Entity: "desktop_release", ID: 912}} {
		require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error { return (&store.Store{DB: tx}).ScheduleContentTranslation(ctx, ref) }))
	}
	require.Equal(t, []string{"es-ES", "ja-JP", "ru-RU", "zh-CN"}, queued("category:911"))
	// The existing German text stays published, stale or not.
	var german string
	require.NoError(t, st.DB.Raw("SELECT name FROM category_i18n WHERE category_id=911 AND locale='de-DE'").Scan(&german).Error)
	require.Equal(t, "Editoren", german)
	// Client release notes follow the Mac client's languages, not the website's indexing.
	require.Equal(t, []string{"de-DE", "es-ES", "fr-FR", "it-IT", "ja-JP", "pt-BR", "ru-RU", "zh-CN"}, queued("desktop_release:912"))
}

func TestTraditionalOnlyQueuesSimplifiedDependency(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO categories(id,slug,icon,source_locale) VALUES(921,'traditional-editors','lucide:box','en-US');
 INSERT INTO category_i18n(category_id,locale,name,source_locale,status) VALUES(921,'en-US','Editors','en-US','source');
 UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}')||'{"openLocales":["zh-Hant"]}'::jsonb);
 INSERT INTO seo_opportunity_cursors(source,signature) VALUES('translation_locales','translation-backfill-v1:zh-Hant')`).Error)
	queued, ran, err := st.BackfillTranslationTasksForLocales(ctx)
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, 1, queued["category"])
	var locales []string
	require.NoError(t, st.DB.Raw("SELECT locale FROM seo_tasks WHERE entity_key='category:921'").Scan(&locales).Error)
	require.Equal(t, []string{"zh-CN"}, locales)
	require.NoError(t, st.DB.Exec("DELETE FROM seo_tasks WHERE entity_key='category:921'").Error)
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return (&store.Store{DB: tx}).ScheduleContentTranslation(ctx, content.Ref{Entity: "category", ID: 921})
	}))
	locales = nil
	require.NoError(t, st.DB.Raw("SELECT locale FROM seo_tasks WHERE entity_key='category:921'").Scan(&locales).Error)
	require.Equal(t, []string{"zh-CN"}, locales)
}
