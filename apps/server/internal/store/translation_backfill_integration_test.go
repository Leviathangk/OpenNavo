//go:build integration

package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTranslationBackfillUpgradeIncludesArticlesAndAuthors(t *testing.T) {
	for _, version := range []string{"v2", "v3"} {
		t.Run(version, func(t *testing.T) {
			st := testutil.NewSEOStore(t)
			ctx := context.Background()
			require.NoError(t, st.DB.Exec(`BEGIN;
 INSERT INTO article_authors(id,slug,name) VALUES(931,'backfill-author','Backfill Author');
 INSERT INTO article_author_i18n(author_id,locale,role,bio,status) VALUES(931,'en-US','Editor','Author biography','source');
 INSERT INTO articles(id,slug,status,working_revision_id,published_revision_id,first_published_at,archived_at) VALUES
 (932,'backfill-published','published',942,941,now(),NULL),
 (933,'backfill-draft','draft',943,NULL,NULL,NULL),
 (934,'backfill-archived','archived',944,944,now(),now());
 INSERT INTO article_revisions(id,article_id,number,source_locale,type,author_id) VALUES
 (941,932,1,'en-US','news',931),(942,932,2,'en-US','news',931),
 (943,933,1,'en-US','news',931),(944,934,1,'en-US','news',931);
 INSERT INTO article_revision_i18n(revision_id,locale,title,source_locale,status)
 SELECT id,'en-US','Backfill article','en-US','source' FROM unnest(ARRAY[941,942,943,944]) id;
 UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}')||'{"openLocales":["en-US"]}'::jsonb);
 COMMIT;`).Error)
			codes := make([]string, 0, len(i18n.SiteLocales))
			for _, code := range i18n.SiteLocales {
				codes = append(codes, string(code))
			}
			require.NoError(t, st.DB.Exec("INSERT INTO seo_opportunity_cursors(source,signature) VALUES('translation_locales',?)", "translation-backfill-"+version+":"+strings.Join(codes, ",")).Error)
			queued, ran, err := st.BackfillTranslationTasksForLocales(ctx)
			require.NoError(t, err)
			require.True(t, ran)
			require.Equal(t, 8, queued["article"])
			require.Equal(t, 8, queued["article_author"])
			var rows []struct{ EntityKey, Locale string }
			require.NoError(t, st.DB.Raw("SELECT entity_key,locale FROM seo_tasks WHERE kind='translate'").Scan(&rows).Error)
			byEntity := map[string][]string{}
			for _, row := range rows {
				byEntity[row.EntityKey] = append(byEntity[row.EntityKey], row.Locale)
			}
			require.Len(t, byEntity, 2, "only the published revision and author are translated")
			for _, entity := range []string{"article:941", "article_author:931"} {
				require.ElementsMatch(t, []string{"de-DE", "es-ES", "fr-FR", "it-IT", "ja-JP", "pt-BR", "ru-RU", "zh-CN"}, byEntity[entity])
			}
			queued, ran, err = st.BackfillTranslationTasksForLocales(ctx)
			require.NoError(t, err)
			require.False(t, ran, "the completed upgrade is skipped on restart")
			require.Empty(t, queued)
			queued, err = st.BackfillTranslationTasks(ctx)
			require.NoError(t, err)
			require.Zero(t, queued["article"]+queued["article_author"], "rescanning does not duplicate tasks")
		})
	}
}

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

	// All supported languages get translation work even when closed for indexing.
	openLocales(`["en-US","zh-CN","ja-JP","ru-RU","es-ES"]`)
	queued, ran, err := st.BackfillTranslationTasksForLocales(ctx)
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, 7, queued["category"])
	require.Equal(t, 8, queued["package"])
	require.Equal(t, map[string][]string{"category:901": {"de-DE", "es-ES", "fr-FR", "it-IT", "ja-JP", "pt-BR", "ru-RU"}, "package:902": {"de-DE", "es-ES", "fr-FR", "it-IT", "ja-JP", "pt-BR", "ru-RU", "zh-CN"}}, tasks())

	queued, ran, err = st.BackfillTranslationTasksForLocales(ctx)
	require.NoError(t, err)
	require.False(t, ran, "the same language set is scanned once")
	require.Empty(t, queued)
	// Changing indexing settings does not trigger a new translation scan.
	openLocales("null")
	queued, ran, err = st.BackfillTranslationTasksForLocales(ctx)
	require.NoError(t, err)
	require.False(t, ran)
	require.Empty(t, queued)
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

func TestSourceEditsQueueTranslationsRegardlessOfOpenLocales(t *testing.T) {
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
	require.Equal(t, []string{"de-DE", "es-ES", "fr-FR", "it-IT", "ja-JP", "pt-BR", "ru-RU", "zh-CN"}, queued("category:911"))
	// The existing German text stays published, stale or not.
	var german string
	require.NoError(t, st.DB.Raw("SELECT name FROM category_i18n WHERE category_id=911 AND locale='de-DE'").Scan(&german).Error)
	require.Equal(t, "Editoren", german)
	// Client release notes follow the Mac client's languages, not the website's indexing.
	require.Equal(t, []string{"de-DE", "es-ES", "fr-FR", "it-IT", "ja-JP", "pt-BR", "ru-RU", "zh-CN"}, queued("desktop_release:912"))
}

func TestClosedLocalesBackfillUpgradeKeepsAllTranslations(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO categories(id,slug,icon,source_locale) VALUES(921,'traditional-editors','lucide:box','en-US');
 INSERT INTO category_i18n(category_id,locale,name,source_locale,status) VALUES(921,'en-US','Editors','en-US','source');
 UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}')||'{"openLocales":["zh-Hant"]}'::jsonb);
 INSERT INTO seo_opportunity_cursors(source,signature) VALUES('translation_locales','translation-backfill-v2:zh-Hant')`).Error)
	queued, ran, err := st.BackfillTranslationTasksForLocales(ctx)
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, 8, queued["category"])
	var locales []string
	require.NoError(t, st.DB.Raw("SELECT locale FROM seo_tasks WHERE entity_key='category:921'").Scan(&locales).Error)
	require.ElementsMatch(t, []string{"de-DE", "es-ES", "fr-FR", "it-IT", "ja-JP", "pt-BR", "ru-RU", "zh-CN"}, locales)
	require.NoError(t, st.DB.Exec("DELETE FROM seo_tasks WHERE entity_key='category:921'").Error)
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return (&store.Store{DB: tx}).ScheduleContentTranslation(ctx, content.Ref{Entity: "category", ID: 921})
	}))
	locales = nil
	require.NoError(t, st.DB.Raw("SELECT locale FROM seo_tasks WHERE entity_key='category:921'").Scan(&locales).Error)
	require.ElementsMatch(t, []string{"de-DE", "es-ES", "fr-FR", "it-IT", "ja-JP", "pt-BR", "ru-RU", "zh-CN"}, locales)
}
