//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestTranslationHistoricalWriterCorrection(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	sql, err := st.DB.DB()
	require.NoError(t, err)
	require.NoError(t, goose.DownToContext(ctx, sql, ".", 90))
	// Rows written at schema 90 are fixtures in that schema's columns. Today's
	// store writers know columns added later, so they cannot stand in for them.
	require.NoError(t, st.DB.Exec("INSERT INTO content_revision_requests(request_id) VALUES('fixture-historical-writers')").Error)
	for i, writer := range []string{"agent", "admin", "unknown", "later-edit"} {
		id := int64(850 + i)
		require.NoError(t, st.DB.Exec(`INSERT INTO categories(id,slug,icon,source_locale) VALUES(?,'historical-writer-'||?::bigint,'lucide:box','en-US')`, id, id).Error)
		require.NoError(t, st.DB.Exec(`INSERT INTO category_i18n(category_id,locale,source_locale,status,name) VALUES(?,'zh-CN','en-US','manual','文件管理工具')`, id).Error)
		if writer == "unknown" {
			continue
		}
		actor := "agent"
		if writer == "admin" {
			actor = "admin"
		}
		require.NoError(t, st.DB.Exec(`INSERT INTO content_revisions(entity,object_key,version,locale,action,operation_id,required_permissions,actor_type,request_id,before_data,after_data)
 SELECT 'category',category_id::text,1,locale,'update','FixTranslation',ARRAY['translation:review'],?,'fixture-historical-writers','{}'::jsonb,jsonb_build_object('category_i18n',jsonb_build_array(to_jsonb(t)))
 FROM category_i18n t WHERE category_id=? AND locale='zh-CN'`, actor, id).Error)
		if writer == "later-edit" {
			require.NoError(t, st.DB.Exec("UPDATE category_i18n SET name='文档管理工具' WHERE category_id=? AND locale='zh-CN'", id).Error)
		}
	}
	require.NoError(t, goose.UpContext(ctx, sql, "."))
	for i, status := range []string{"machine", "manual", "manual", "manual"} {
		var row struct {
			Status            string
			MachineTranslated bool
		}
		require.NoError(t, st.DB.Table("category_i18n").Where("category_id=? AND locale='zh-CN'", 850+i).Take(&row).Error)
		require.Equal(t, status, row.Status)
		require.Equal(t, i == 0, row.MachineTranslated)
	}
	var n int64
	require.NoError(t, st.DB.Table("seo_translation_corrections").Count(&n).Error)
	require.EqualValues(t, 1, n)
	require.NoError(t, goose.DownToContext(ctx, sql, ".", 90))
	var status string
	require.NoError(t, st.DB.Raw("SELECT status FROM category_i18n WHERE category_id=850 AND locale='zh-CN'").Scan(&status).Error)
	require.Equal(t, "manual", status)
	require.NoError(t, st.DB.Table("seo_translation_corrections").Where("reverted_at IS NOT NULL AND restored").Count(&n).Error)
	require.EqualValues(t, 1, n, "rollback keeps the original correction and its compensation audit")
}

func TestTranslationCorrectionResumesAndPreservesLaterEdits(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	db, err := st.DB.DB()
	require.NoError(t, err)
	require.NoError(t, goose.DownToContext(ctx, db, ".", 90))
	require.NoError(t, st.DB.Exec(`INSERT INTO categories(id,slug,icon,source_locale) SELECT i,'correction-'||i,'lucide:box','en-US' FROM generate_series(1,130) i;
 INSERT INTO category_i18n(category_id,locale,source_locale,status,name) SELECT i,'zh-CN','en-US','manual','Fixture name' FROM generate_series(1,130) i;
 INSERT INTO content_revision_requests(request_id) VALUES('fixture-provenance-batch');
 INSERT INTO content_revisions(entity,object_key,version,locale,action,operation_id,required_permissions,actor_type,request_id,before_data,after_data)
 SELECT 'category',category_id::text,1,locale,'update','FixTranslation',ARRAY['translation:review'],'agent','fixture-provenance-batch','{}',jsonb_build_object('category_i18n',jsonb_build_array(to_jsonb(t))) FROM category_i18n t;
 CREATE FUNCTION stop_translation_correction() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.category_id=129 THEN RAISE EXCEPTION 'fixture correction interruption'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER stop_translation_correction BEFORE UPDATE OF status ON category_i18n FOR EACH ROW EXECUTE FUNCTION stop_translation_correction()`).Error)
	require.ErrorContains(t, goose.UpContext(ctx, db, "."), "fixture correction interruption")
	var n int64
	require.NoError(t, st.DB.Table("seo_translation_corrections").Count(&n).Error)
	require.EqualValues(t, 128, n, "first bounded batch and its receipts committed before interruption")
	require.NoError(t, st.DB.Exec(`DROP TRIGGER stop_translation_correction ON category_i18n; DROP FUNCTION stop_translation_correction();
 UPDATE category_i18n SET name='Later human edit',status='manual' WHERE category_id=1 AND locale='zh-CN'`).Error)
	require.NoError(t, goose.UpContext(ctx, db, "."))
	require.NoError(t, st.DB.Table("seo_translation_corrections").Count(&n).Error)
	require.EqualValues(t, 130, n, "resume neither duplicates receipts nor revisits completed keys")
	require.NoError(t, goose.DownToContext(ctx, db, ".", 90))
	require.NoError(t, st.DB.Table("seo_translation_corrections").Where("restored").Count(&n).Error)
	require.EqualValues(t, 129, n)
	require.NoError(t, st.DB.Table("seo_translation_corrections").Where("NOT restored AND reverted_at IS NOT NULL").Count(&n).Error)
	require.EqualValues(t, 1, n)
	var name string
	require.NoError(t, st.DB.Raw(`SELECT name FROM category_i18n WHERE category_id=1 AND locale='zh-CN'`).Scan(&name).Error)
	require.Equal(t, "Later human edit", name)
	require.NoError(t, st.DB.Table("category_i18n").Where("status='manual' AND NOT machine_translated").Count(&n).Error)
	require.EqualValues(t, 130, n)
}

func TestTranslationCorrectionCompositeAndTextKeys(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	db, err := st.DB.DB()
	require.NoError(t, err)
	require.NoError(t, goose.DownToContext(ctx, db, ".", 90))
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 SELECT i,'cask','provenance-'||i,'provenance-'||i,'homebrew/cask','Fixture','1','1','{}','fixture' FROM generate_series(1,2) i;
 INSERT INTO collections(id,slug) VALUES(1,'provenance');
 INSERT INTO collection_items(collection_id,package_id) VALUES(1,1),(1,2);
 INSERT INTO collection_item_i18n(collection_id,package_id,locale,source_locale,status,note) VALUES(1,1,'zh-CN','en-US','manual','Fixture note'),(1,2,'zh-CN','en-US','manual','Fixture note');
 INSERT INTO app_config(key,value) VALUES('announcement.fixture','{}');
 INSERT INTO announcement_i18n(config_key,locale,source_locale,status,body) VALUES('announcement.fixture','zh-CN','en-US','manual','Fixture body');
 INSERT INTO content_revision_requests(request_id) VALUES('fixture-provenance-keys');
 INSERT INTO content_revisions(entity,object_key,version,locale,action,operation_id,required_permissions,actor_type,request_id,before_data,after_data)
 SELECT 'collection_item',collection_id||':'||package_id,1,locale,'update','FixTranslation',ARRAY['translation:review'],'agent','fixture-provenance-keys','{}'::jsonb,jsonb_build_object('collection_item_i18n',jsonb_build_array(to_jsonb(t))) FROM collection_item_i18n t
 UNION ALL SELECT 'announcement',config_key,1,locale,'update','FixTranslation',ARRAY['translation:review'],'agent','fixture-provenance-keys','{}'::jsonb,jsonb_build_object('announcement_i18n',jsonb_build_array(to_jsonb(t))) FROM announcement_i18n t WHERE config_key='announcement.fixture'`).Error)
	require.NoError(t, goose.UpContext(ctx, db, "."))
	var n int64
	require.NoError(t, st.DB.Table("seo_translation_corrections").Count(&n).Error)
	require.EqualValues(t, 3, n)
	require.NoError(t, st.DB.Exec(`UPDATE collection_item_i18n SET note='Later note' WHERE collection_id=1 AND package_id=2 AND locale='zh-CN'`).Error)
	require.NoError(t, goose.DownToContext(ctx, db, ".", 90))
	require.NoError(t, st.DB.Table("seo_translation_corrections").Where("restored").Count(&n).Error)
	require.EqualValues(t, 2, n)
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM collection_item_i18n WHERE collection_id=1 AND package_id=2 AND locale='zh-CN' AND note='Later note' AND status='machine'`).Scan(&n).Error)
	require.EqualValues(t, 1, n)
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM announcement_i18n WHERE config_key='announcement.fixture' AND locale='zh-CN' AND status='manual'`).Scan(&n).Error)
	require.EqualValues(t, 1, n)
}
