-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION collection_url_invalidation() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER collection_url_invalidation AFTER INSERT OR UPDATE OR DELETE ON collections FOR EACH ROW EXECUTE FUNCTION collection_url_invalidation();
CREATE TRIGGER collection_url_invalidation AFTER INSERT OR UPDATE OR DELETE ON collection_items FOR EACH ROW EXECUTE FUNCTION collection_url_invalidation();
CREATE TRIGGER collection_url_invalidation AFTER INSERT OR UPDATE OR DELETE ON collection_i18n FOR EACH ROW EXECUTE FUNCTION collection_url_invalidation();
CREATE TRIGGER collection_url_invalidation AFTER INSERT OR UPDATE OR DELETE ON collection_item_i18n FOR EACH ROW EXECUTE FUNCTION collection_url_invalidation();
CREATE TRIGGER collection_url_invalidation AFTER INSERT OR UPDATE OR DELETE ON collection_setup_criteria FOR EACH ROW EXECUTE FUNCTION collection_url_invalidation();
-- +goose Down
DROP TRIGGER collection_url_invalidation ON collection_setup_criteria;
DROP TRIGGER collection_url_invalidation ON collection_item_i18n;
DROP TRIGGER collection_url_invalidation ON collection_i18n;
DROP TRIGGER collection_url_invalidation ON collection_items;
DROP TRIGGER collection_url_invalidation ON collections;
DROP FUNCTION collection_url_invalidation();
