//go:build integration

package contenttranslate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/llm"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var translatedText = map[string]string{"en-US": "The application manages your files", "zh-CN": "这款应用可以管理文件", "ja-JP": "ファイルを管理するアプリです", "es-ES": "La aplicación permite gestionar tus archivos", "pt-BR": "O aplicativo permite gerenciar seus arquivos", "ru-RU": "Приложение для управления файлами"}

func validFake(in llm.ContentInput) (llm.ContentOutput, error) {
	out := llm.ContentOutput{}
	for _, locale := range in.Targets {
		out[locale] = map[string]string{}
		for field, source := range in.Fields {
			value := translatedText[locale]
			for _, term := range in.Protected {
				if strings.Contains(source, term) {
					value += " " + term
				}
			}
			out[locale][field] = value
		}
	}
	return out, nil
}
func seedContents(t *testing.T, st *store.Store) {
	t.Helper()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(100,'cask','orbit','orbit','homebrew/cask','Orbit','1','1','{}','hash');
INSERT INTO categories(id,slug,icon,source_locale) VALUES(100,'tools','lucide:box','en-US');
INSERT INTO collections(id,slug,source_locale) VALUES(100,'tools','en-US');
INSERT INTO features(id,placement,target_type,url,source_locale) VALUES(100,'home_hero','url','https://example.com','en-US');
INSERT INTO releases(id,package_id,source,source_key,version) VALUES(100,100,'editorial','one','1');
INSERT INTO assets(id,kind,storage_key,url,width,height,bytes,sha256,mime) VALUES(100,'screenshot','test-image','https://example.com/a.png',100,100,1,'hash','image/png');
INSERT INTO package_screenshots(id,package_id,asset_id,source_locale) VALUES(100,100,100,'en-US');
INSERT INTO collection_items(collection_id,package_id,source_locale) VALUES(100,100,'en-US');
INSERT INTO desktop_releases(id,version,artifacts,source_locale) VALUES(100,'1','[]','en-US');
INSERT INTO mirrors(id,key,probe_url,source_locale) VALUES(100,'test','https://example.com','en-US');
INSERT INTO app_config(key,value) VALUES('desktop.announcement','{"sourceLocale":"en-US","en-US":{"title":"The application manages files"}}');
INSERT INTO article_authors(id,slug,name,source_locale) VALUES(100,'files-author','Files Author','en-US');
INSERT INTO articles(id,slug,status,working_revision_id,published_revision_id,first_published_at,content_updated_at) VALUES(100,'files-article','published',100,100,now(),now());
INSERT INTO article_revisions(id,article_id,number,source_locale,type,published_at) VALUES(100,100,1,'en-US','tutorial',now());`).Error)
	for _, entity := range content.Names() {
		spec := content.Registry[entity]
		row := map[string]any{spec.Key: 100, "locale": "en-US", "source_locale": "en-US", "status": "source"}
		if entity == "announcement" {
			row[spec.Key] = "desktop.announcement"
		}
		if entity == "package" {
			row["source"] = "human"
		}
		if entity == "collection_item" {
			row["package_id"] = 100
		}
		for field := range spec.Fields {
			if field == "sections" {
				row[field] = gorm.Expr(`'[{"area":"Files","items":["Manage your files"]}]'::jsonb`)
			} else {
				row[field] = "The application manages files"
			}
		}
		require.NoError(t, st.DB.Table(spec.Table).Create(row).Error, entity)
	}
}
func schedule(t *testing.T, ctx context.Context, st *store.Store, ref content.Ref) store.ContentSource {
	t.Helper()
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error { return (&store.Store{DB: tx}).ScheduleContentTranslation(ctx, ref) }))
	src, err := st.ReadContentSource(ctx, ref)
	require.NoError(t, err)
	return src
}
func TestAllContentTypesTranslateDeduplicateAndProtectConcurrentEdits(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	seedContents(t, st)
	fake := &fakeTranslator{Run: validFake}
	svc := Service{Store: st, LLM: fake, Model: "fake"}
	for _, entity := range content.Names() {
		t.Run(entity, func(t *testing.T) {
			ref := content.Ref{Entity: entity, ID: 100}
			if entity == "collection_item" {
				ref.SecondaryID = 100
			}
			source := schedule(t, ctx, st, ref)
			require.Len(t, source.Targets, 5)
			var due time.Time
			require.NoError(t, st.DB.Raw("SELECT available_at FROM job_outbox WHERE unique_key=?", "translate:content:"+entity+":"+ref.Key()).Scan(&due).Error)
			require.WithinDuration(t, time.Now().Add(30*time.Second), due, 2*time.Second)
			before := fake.Calls
			stats, err := svc.Run(ctx, ref, source.Hash)
			require.NoError(t, err)
			require.Equal(t, 5, stats["translated"])
			require.Greater(t, fake.Calls, before)
			current, err := st.ReadContentSource(ctx, ref)
			require.NoError(t, err)
			require.Equal(t, source.Fields, current.Fields)
			require.Empty(t, current.Targets)
			var rows []struct {
				Locale, Status, SourceHash, Model string
				MachineTranslated                 bool
				TranslatedAt                      *time.Time
			}
			require.NoError(t, st.DB.Table(content.Registry[entity].Table).Where("locale<>'en-US'").Find(&rows).Error)
			require.Len(t, rows, 5)
			for _, row := range rows {
				require.Equal(t, "machine", row.Status)
				require.Equal(t, source.Hash, row.SourceHash)
				require.Equal(t, "fake", row.Model)
				require.True(t, row.MachineTranslated)
				require.NotNil(t, row.TranslatedAt)
			}
			calls := fake.Calls
			schedule(t, ctx, st, ref)
			_, err = svc.Run(ctx, ref, source.Hash)
			require.NoError(t, err)
			require.Equal(t, calls, fake.Calls)
		})
	}
	ref := content.Ref{Entity: "category", ID: 100}
	require.NoError(t, st.DB.Exec("UPDATE category_i18n SET name='The new application manages files' WHERE category_id=100 AND locale='en-US'").Error)
	source := schedule(t, ctx, st, ref)
	fake.Run = func(in llm.ContentInput) (llm.ContentOutput, error) {
		require.NoError(t, st.DB.Exec("UPDATE category_i18n SET name='手工修正',status='manual',updated_at=clock_timestamp() WHERE category_id=100 AND locale='zh-CN'").Error)
		return validFake(in)
	}
	stats, err := svc.Run(ctx, ref, source.Hash)
	require.NoError(t, err)
	require.Equal(t, 4, stats["translated"])
	var manual string
	require.NoError(t, st.DB.Raw("SELECT name FROM category_i18n WHERE category_id=100 AND locale='zh-CN'").Scan(&manual).Error)
	require.Equal(t, "手工修正", manual)
	require.NoError(t, st.DB.Exec("UPDATE category_i18n SET name='The next application manages files' WHERE category_id=100 AND locale='en-US'").Error)
	source = schedule(t, ctx, st, ref)
	fake.Run = func(in llm.ContentInput) (llm.ContentOutput, error) {
		require.NoError(t, st.DB.Exec("UPDATE category_i18n SET name='The newest application manages files' WHERE category_id=100 AND locale='en-US'").Error)
		schedule(t, ctx, st, ref)
		return validFake(in)
	}
	stats, err = svc.Run(ctx, ref, source.Hash)
	require.NoError(t, err)
	require.Equal(t, 0, stats["translated"])
	require.Equal(t, 5, stats["stale"])
	// Old outbox hashes never call the model; consecutive saves retain only the latest payload.
	calls := fake.Calls
	stats, err = svc.Run(ctx, ref, source.Hash)
	require.NoError(t, err)
	require.Equal(t, calls, fake.Calls)
	require.Equal(t, "fresh_or_stale", stats["skipped"])
	var total int64
	require.NoError(t, st.DB.Table("job_outbox").Where("unique_key=?", "translate:content:category:100").Count(&total).Error)
	require.EqualValues(t, 1, total)
	require.NoError(t, st.DB.Table("translation_logs").Count(&total).Error)
	require.Positive(t, total)
}
func TestContentTransactionRollbackFailuresAndLease(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	seedContents(t, st)
	ref := content.Ref{Entity: "category", ID: 100}
	sentinel := errors.New("rollback")
	require.ErrorIs(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		require.NoError(t, (&store.Store{DB: tx}).ScheduleContentTranslation(ctx, ref))
		return sentinel
	}), sentinel)
	var count int64
	require.NoError(t, st.DB.Table("job_outbox").Count(&count).Error)
	require.Zero(t, count)
	source := schedule(t, ctx, st, ref)
	fake := &fakeTranslator{Run: func(llm.ContentInput) (llm.ContentOutput, error) { return nil, &llm.Failure{Reason: "wrong_language"} }}
	svc := Service{Store: st, LLM: fake, Model: "fake"}
	require.NoError(t, st.Redis.Set(ctx, "translation:lease:category:100:"+source.Hash, "another-worker", time.Minute).Err())
	stats, err := svc.Run(ctx, ref, source.Hash)
	require.NoError(t, err)
	require.Equal(t, "running", stats["skipped"])
	require.Zero(t, fake.Calls)
	require.NoError(t, st.Redis.Del(ctx, "translation:lease:category:100:"+source.Hash).Err())
	_, err = svc.Run(ctx, ref, source.Hash)
	require.Error(t, err)
	require.Equal(t, 2, fake.Calls)
	require.NoError(t, st.DB.Table("category_i18n").Where("status='failed' AND fail_reason='wrong_language'").Count(&count).Error)
	require.EqualValues(t, 5, count)
	require.NoError(t, st.DB.Exec("UPDATE i18n_settings SET enabled=false").Error)
	calls := fake.Calls
	stats, err = svc.Run(ctx, ref, source.Hash)
	require.NoError(t, err)
	require.Equal(t, "disabled", stats["skipped"])
	require.Equal(t, calls, fake.Calls)
	var usage struct{ Prompt, Completion int64 }
	require.NoError(t, st.DB.Raw("SELECT sum(prompt_tokens) prompt,sum(completion_tokens) completion FROM translation_logs").Scan(&usage).Error)
	require.EqualValues(t, 20, usage.Prompt)
	require.EqualValues(t, 10, usage.Completion)
	var payload json.RawMessage
	require.NoError(t, st.DB.Raw("SELECT payload FROM job_outbox LIMIT 1").Row().Scan(&payload))
	require.Contains(t, string(payload), source.Hash)
}

func TestTranslationDoesNotInventHomebrewSourcesAndResumeDoesNotLoseChanges(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	seedContents(t, st)
	require.NoError(t, st.DB.Exec(`DELETE FROM package_i18n WHERE package_id=100; UPDATE packages SET desc_en='The application manages files' WHERE id=100; UPDATE releases SET source='github_release',body_markdown='The application improves file management',body_hash='hash' WHERE id=100; DELETE FROM release_i18n WHERE release_id=100`).Error)
	for _, entity := range []string{"package", "release"} {
		require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
			return (&store.Store{DB: tx}).ScheduleContentTranslation(ctx, content.Ref{Entity: entity, ID: 100})
		}))
	}
	var count int64
	require.NoError(t, st.DB.Table("package_i18n").Where("package_id=100").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, st.DB.Table("release_i18n").Where("release_id=100").Count(&count).Error)
	require.Zero(t, count)
	ref := content.Ref{Entity: "category", ID: 100}
	first := schedule(t, ctx, st, ref)
	require.NoError(t, st.DB.Exec(`UPDATE i18n_settings SET enabled=false; UPDATE category_i18n SET name='The application supports your files' WHERE category_id=100 AND locale='en-US'`).Error)
	changed := schedule(t, ctx, st, ref)
	require.NotEqual(t, first.Hash, changed.Hash)
	var queued string
	require.NoError(t, st.DB.Raw(`SELECT payload->>'sourceHash' FROM job_outbox WHERE unique_key='translate:content:category:100'`).Scan(&queued).Error)
	require.Equal(t, first.Hash, queued)
	require.NoError(t, st.DB.Exec(`UPDATE i18n_settings SET enabled=true`).Error)
	resumed := schedule(t, ctx, st, ref)
	require.Len(t, resumed.Targets, 5)
	require.NoError(t, st.DB.Raw(`SELECT payload->>'sourceHash' FROM job_outbox WHERE unique_key='translate:content:category:100'`).Scan(&queued).Error)
	require.Equal(t, changed.Hash, queued)
	require.NoError(t, st.DB.Exec(`UPDATE i18n_settings SET enabled_locales='["en-US","zh-CN","es-ES","pt-BR","ru-RU"]'`).Error)
	fake := &fakeTranslator{Run: validFake}
	svc := Service{Store: st, LLM: fake, Model: "fake"}
	stats, err := svc.Run(ctx, ref, resumed.Hash)
	require.NoError(t, err)
	require.Equal(t, 4, stats["translated"])
	require.NoError(t, st.DB.Table("category_i18n").Where("category_id=100 AND locale='ja-JP' AND status='pending'").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestFormulaContentNeverQueuesOrCallsModel(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	seedContents(t, st)
	require.NoError(t, st.DB.Exec("UPDATE packages SET kind='formula' WHERE id=100").Error)
	for _, entity := range []string{"package", "release", "screenshot", "collection_item"} {
		ref := content.Ref{Entity: entity, ID: 100}
		if entity == "collection_item" {
			ref.SecondaryID = 100
		}
		require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error { return (&store.Store{DB: tx}).ScheduleContentTranslation(ctx, ref) }))
		fake := &fakeTranslator{Run: validFake}
		svc := Service{Store: st, LLM: fake, Model: "fake"}
		stats, err := svc.Run(ctx, ref, strings.Repeat("a", 64))
		require.NoError(t, err)
		require.Equal(t, "deleted", stats["skipped"])
		require.Zero(t, fake.Calls)
	}
	var count int64
	require.NoError(t, st.DB.Table("job_outbox").Count(&count).Error)
	require.Zero(t, count)
}
