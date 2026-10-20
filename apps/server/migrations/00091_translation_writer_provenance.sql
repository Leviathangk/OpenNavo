-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS seo_translation_corrections (
 id BIGSERIAL PRIMARY KEY,
 table_name TEXT NOT NULL,
 revision_id BIGINT NOT NULL,
 before_data JSONB NOT NULL,
 after_data JSONB NOT NULL,
 corrected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 reverted_at TIMESTAMPTZ,
 restored BOOLEAN
);
CREATE INDEX IF NOT EXISTS seo_translation_corrections_pending ON seo_translation_corrections(id DESC) WHERE reverted_at IS NULL;
-- DDL commits before scanning history. Each table is traversed by its primary
-- key, with no more than 128 candidate translations locked per transaction.
-- +goose StatementBegin
CREATE OR REPLACE PROCEDURE correct_translation_writers() LANGUAGE plpgsql AS $$
DECLARE spec record; item record; revision record; progress jsonb; done boolean;
 key_type text; key_expr text; cursor_expr text; target_key text; corrected jsonb; n integer;
BEGIN
 FOR spec IN SELECT * FROM (VALUES
 ('package','package_i18n','package_id'),('release','release_i18n','release_id'),
 ('category','category_i18n','category_id'),('collection','collection_i18n','collection_id'),
 ('feature','feature_i18n','feature_id'),('screenshot','screenshot_i18n','screenshot_id'),
 ('collection_item','collection_item_i18n','collection_id'),('desktop_release','desktop_release_i18n','desktop_release_id'),
 ('mirror','mirror_i18n','mirror_id'),('announcement','announcement_i18n','config_key'),
 ('article','article_revision_i18n','revision_id'),('article_author','article_author_i18n','author_id')) v(entity,tbl,keycol)
 LOOP
  INSERT INTO fact_metadata_backfill_progress(name) VALUES('00091:'||spec.tbl) ON CONFLICT DO NOTHING;
  COMMIT;
  key_type:=CASE WHEN spec.entity='announcement' THEN 'text' ELSE 'bigint' END;
  key_expr:=format('t.%I',spec.keycol);
  cursor_expr:=format('COALESCE(($1->>%L)::%s,%L::%s)',spec.keycol,key_type,CASE WHEN key_type='text' THEN '' ELSE '0' END,key_type);
  IF spec.entity='collection_item' THEN
   key_expr:=key_expr||',t.package_id';
   cursor_expr:=cursor_expr||',COALESCE(($1->>''package_id'')::bigint,0)';
  END IF;
  key_expr:=key_expr||',t.locale';
  cursor_expr:=cursor_expr||',COALESCE($1->>''locale'','''')';
  LOOP
   -- Catalog writes take this lock before row locks. Keeping that ordering
   -- avoids a concurrent editor holding history/catalog while awaiting this row.
   PERFORM pg_advisory_xact_lock(hashtextextended('catalog:changes',0));
   SELECT cursor,completed INTO progress,done FROM fact_metadata_backfill_progress WHERE name='00091:'||spec.tbl FOR UPDATE;
   EXIT WHEN done;
   n:=0;
   FOR item IN EXECUTE format('SELECT to_jsonb(t) original,t.ctid row_id FROM %I t WHERE (%s)>(%s) ORDER BY %s LIMIT 128 FOR UPDATE',spec.tbl,key_expr,cursor_expr,key_expr) USING progress
   LOOP
    n:=n+1;
    progress:=jsonb_build_object(spec.keycol,item.original->spec.keycol,'locale',item.original->'locale');
    IF spec.entity='collection_item' THEN progress:=progress||jsonb_build_object('package_id',item.original->'package_id'); END IF;
    IF item.original->>'status'<>'manual' THEN CONTINUE; END IF;
    target_key:=item.original->>spec.keycol;
    IF spec.entity='collection_item' THEN target_key:=target_key||':'||(item.original->>'package_id'); END IF;
    SELECT r.* INTO revision FROM content_revisions r
    WHERE r.entity=spec.entity AND r.object_key=target_key
    AND EXISTS(SELECT 1 FROM jsonb_array_elements(coalesce(r.after_data->spec.tbl,'[]')) a
      WHERE a->>'locale'=item.original->>'locale' AND a IS DISTINCT FROM
      (SELECT b FROM jsonb_array_elements(coalesce(r.before_data->spec.tbl,'[]')) b WHERE b->>'locale'=item.original->>'locale' LIMIT 1))
    ORDER BY r.version DESC,r.id DESC LIMIT 1;
    IF NOT FOUND OR revision.actor_type<>'agent' OR lower(revision.operation_id)<>'fixtranslation' THEN CONTINUE; END IF;
    IF NOT EXISTS(SELECT 1 FROM jsonb_array_elements(coalesce(revision.after_data->spec.tbl,'[]')) a WHERE a=item.original) THEN CONTINUE; END IF;
    EXECUTE format('UPDATE %I t SET status=''machine'' WHERE ctid=$1 RETURNING to_jsonb(t)',spec.tbl) INTO corrected USING item.row_id;
    INSERT INTO seo_translation_corrections(table_name,revision_id,before_data,after_data) VALUES(spec.tbl,revision.id,item.original,corrected);
   END LOOP;
   UPDATE fact_metadata_backfill_progress SET cursor=progress,completed=(n<128) WHERE name='00091:'||spec.tbl;
   COMMIT;
  END LOOP;
  COMMIT;
 END LOOP;
END $$;
-- +goose StatementEnd
CALL correct_translation_writers();
DROP PROCEDURE correct_translation_writers();

-- +goose Down
-- A rollback is a recorded compensation, not deletion of the correction receipt.
-- Later edits remain untouched; use native primary-key types before JSON equality.
-- +goose StatementBegin
CREATE OR REPLACE PROCEDURE restore_translation_writers() LANGUAGE plpgsql AS $$
DECLARE correction record; keycol text; key_type text; predicate text; n integer; changed bigint;
BEGIN
 IF to_regclass('seo_translation_corrections') IS NULL THEN RETURN; END IF;
 LOOP
  PERFORM pg_advisory_xact_lock(hashtextextended('catalog:changes',0));
  n:=0;
  FOR correction IN SELECT * FROM seo_translation_corrections WHERE reverted_at IS NULL ORDER BY id DESC LIMIT 128 FOR UPDATE LOOP
   SELECT k INTO STRICT keycol FROM (VALUES
    ('package_i18n','package_id'),('release_i18n','release_id'),('category_i18n','category_id'),
    ('collection_i18n','collection_id'),('feature_i18n','feature_id'),('screenshot_i18n','screenshot_id'),
    ('collection_item_i18n','collection_id'),('desktop_release_i18n','desktop_release_id'),
    ('mirror_i18n','mirror_id'),('announcement_i18n','config_key'),('article_revision_i18n','revision_id'),
    ('article_author_i18n','author_id')) v(tbl,k) WHERE tbl=correction.table_name;
   key_type:=CASE WHEN keycol='config_key' THEN 'text' ELSE 'bigint' END;
   predicate:=format('t.%I=($1->>%L)::%s AND t.locale=$1->>''locale''',keycol,keycol,key_type);
   IF correction.table_name='collection_item_i18n' THEN predicate:=predicate||' AND t.package_id=($1->>''package_id'')::bigint'; END IF;
   EXECUTE format('UPDATE %I t SET status=''manual'' WHERE %s AND to_jsonb(t)=$1',correction.table_name,predicate) USING correction.after_data;
   GET DIAGNOSTICS changed=ROW_COUNT;
   UPDATE seo_translation_corrections SET reverted_at=clock_timestamp(),restored=(changed=1) WHERE id=correction.id;
   n:=n+1;
  END LOOP;
  COMMIT;
  EXIT WHEN n<128;
 END LOOP;
 DELETE FROM fact_metadata_backfill_progress WHERE name LIKE '00091:%';
 COMMIT;
END $$;
-- +goose StatementEnd
CALL restore_translation_writers();
DROP PROCEDURE restore_translation_writers();
-- +goose StatementBegin
DO $$ BEGIN
 IF to_regclass('seo_translation_corrections') IS NOT NULL THEN
  LOCK TABLE seo_translation_corrections IN ACCESS EXCLUSIVE MODE;
  IF NOT EXISTS(SELECT 1 FROM seo_translation_corrections) THEN DROP TABLE seo_translation_corrections; END IF;
 END IF;
END $$;
-- +goose StatementEnd
