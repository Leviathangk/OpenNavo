-- +goose Up
SET LOCAL lock_timeout = '5s';
-- No default/backfill: old writers and old rows retain all-language notifications.
ALTER TABLE catalog_changes ADD COLUMN locales text[];
ALTER TABLE content_url_changes ADD COLUMN locales text[];
COMMENT ON COLUMN catalog_changes.locales IS 'NULL means all languages; otherwise the changed content locales';
COMMENT ON COLUMN content_url_changes.locales IS 'NULL means all languages; otherwise the changed content locales';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION collection_url_invalidation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE row_data jsonb; slug_value text; cid bigint; changed_paths text[] := ARRAY['/collections','/discover']; package_ids bigint[] := '{}'; locale_scope text[];
BEGIN
 IF TG_OP='UPDATE' AND (to_jsonb(OLD)-'updated_at') IS NOT DISTINCT FROM (to_jsonb(NEW)-'updated_at') THEN RETURN NULL; END IF;
 FOR row_data IN SELECT v FROM (VALUES (to_jsonb(OLD)),(to_jsonb(NEW))) d(v) WHERE v IS NOT NULL LOOP
  IF TG_TABLE_NAME='collections' THEN
   slug_value:=row_data->>'slug';
   cid:=(row_data->>'id')::bigint;
  ELSE
   cid:=(row_data->>'collection_id')::bigint;
   SELECT slug INTO slug_value FROM collections WHERE id=cid;
  END IF;
  package_ids:=array_append(package_ids,(row_data->>'package_id')::bigint);
  package_ids:=package_ids || ARRAY(SELECT package_id FROM collection_items WHERE collection_id=cid);
  IF slug_value IS NOT NULL THEN changed_paths:=array_append(changed_paths,'/collections/'||slug_value); END IF;
 END LOOP;
 -- A source-language correction can affect every fallback; pure translations only affect their locale.
 IF TG_TABLE_NAME IN ('collection_i18n','collection_item_i18n','collection_setup_criteria')
 AND COALESCE(to_jsonb(NEW)->>'locale',to_jsonb(OLD)->>'locale') IS DISTINCT FROM
     COALESCE(to_jsonb(NEW)->>'source_locale',to_jsonb(OLD)->>'source_locale',(SELECT source_locale FROM collections WHERE id=cid))
 AND COALESCE(to_jsonb(NEW)->>'locale',to_jsonb(OLD)->>'locale') <> 'en-US' THEN
  SELECT array_agg(DISTINCT v) INTO locale_scope FROM (VALUES(to_jsonb(OLD)->>'locale'),(to_jsonb(NEW)->>'locale')) x(v) WHERE v IS NOT NULL;
 END IF;
 INSERT INTO content_url_changes(paths,announce,locales) SELECT array_agg(DISTINCT path),false,locale_scope FROM unnest(changed_paths) path;
 INSERT INTO job_outbox(unique_key,job_type,payload) VALUES('cache:invalidate:collection-seo','cache:invalidate','["c:coll:*","c:home:*"]')
 ON CONFLICT(unique_key) DO UPDATE SET payload=EXCLUDED.payload,available_at=now();
 -- Each member has an independent outbox key, so edits of two collections do
 -- not replace one another's package patterns. Escape Redis glob metacharacters.
 INSERT INTO job_outbox(unique_key,job_type,payload)
 SELECT 'cache:invalidate:collection-package:'||p.id,'cache:invalidate',
  jsonb_build_array('c:pkg:*:'||p.kind||':'||replace(replace(replace(replace(replace(p.token,chr(92),chr(92)||chr(92)),'*',chr(92)||'*'),'?',chr(92)||'?'),'[',chr(92)||'['),']',chr(92)||']')||':*')
 FROM packages p WHERE p.id=ANY(package_ids)
 ON CONFLICT(unique_key) DO UPDATE SET payload=EXCLUDED.payload,available_at=now();
 RETURN NULL;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION collection_url_invalidation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE row_data jsonb; slug_value text; cid bigint; changed_paths text[] := ARRAY['/collections','/discover']; package_ids bigint[] := '{}';
BEGIN
 IF TG_OP='UPDATE' AND (to_jsonb(OLD)-'updated_at') IS NOT DISTINCT FROM (to_jsonb(NEW)-'updated_at') THEN RETURN NULL; END IF;
 FOR row_data IN SELECT v FROM (VALUES (to_jsonb(OLD)),(to_jsonb(NEW))) d(v) WHERE v IS NOT NULL LOOP
  IF TG_TABLE_NAME='collections' THEN
   slug_value:=row_data->>'slug';
   cid:=(row_data->>'id')::bigint;
  ELSE
   cid:=(row_data->>'collection_id')::bigint;
   SELECT slug INTO slug_value FROM collections WHERE id=cid;
  END IF;
  package_ids:=array_append(package_ids,(row_data->>'package_id')::bigint);
  package_ids:=package_ids || ARRAY(SELECT package_id FROM collection_items WHERE collection_id=cid);
  IF slug_value IS NOT NULL THEN changed_paths:=array_append(changed_paths,'/collections/'||slug_value); END IF;
 END LOOP;
 INSERT INTO content_url_changes(paths,announce) SELECT array_agg(DISTINCT path),false FROM unnest(changed_paths) path;
 INSERT INTO job_outbox(unique_key,job_type,payload) VALUES('cache:invalidate:collection-seo','cache:invalidate','["c:coll:*","c:home:*"]')
 ON CONFLICT(unique_key) DO UPDATE SET payload=EXCLUDED.payload,available_at=now();
 -- Each member has an independent outbox key, so edits of two collections do
 -- not replace one another's package patterns. Escape Redis glob metacharacters.
 INSERT INTO job_outbox(unique_key,job_type,payload)
 SELECT 'cache:invalidate:collection-package:'||p.id,'cache:invalidate',
  jsonb_build_array('c:pkg:*:'||p.kind||':'||replace(replace(replace(replace(replace(p.token,chr(92),chr(92)||chr(92)),'*',chr(92)||'*'),'?',chr(92)||'?'),'[',chr(92)||'['),']',chr(92)||']')||':*')
 FROM packages p WHERE p.id=ANY(package_ids)
 ON CONFLICT(unique_key) DO UPDATE SET payload=EXCLUDED.payload,available_at=now();
 RETURN NULL;
END $$;
-- +goose StatementEnd
ALTER TABLE content_url_changes DROP COLUMN locales;
ALTER TABLE catalog_changes DROP COLUMN locales;
