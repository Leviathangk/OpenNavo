-- +goose Up
CREATE TABLE package_page_state (
 package_id BIGINT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
 locale TEXT NOT NULL CHECK(locale IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU','de-DE','fr-FR','it-IT','zh-Hant')),
 page_kind TEXT NOT NULL CHECK(page_kind IN ('overview','versions','dependencies','details')),
 page_number INTEGER NOT NULL DEFAULT 1 CHECK(page_number>0),
 content_hash TEXT NOT NULL CHECK(content_hash ~ '^[0-9a-f]{64}$'),
 content_modified_at TIMESTAMPTZ NOT NULL,
 content_checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(package_id,locale,page_kind,page_number)
);
CREATE TABLE package_page_dirty (
 package_id BIGINT PRIMARY KEY REFERENCES packages(id) ON DELETE CASCADE,
 changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX package_page_dirty_order ON package_page_dirty(changed_at,package_id);
INSERT INTO package_page_dirty(package_id) SELECT id FROM packages WHERE kind IN ('cask','link');

-- Triggers queue work, including deletions. Hashing happens in bounded worker batches.
-- +goose StatementBegin
CREATE FUNCTION queue_package_page_dirty() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE row_data jsonb; pid bigint; package_ids bigint[] := '{}';
BEGIN
 IF TG_OP='UPDATE' THEN
  IF TG_TABLE_NAME='packages' THEN
   -- Analytics, search indexing and sync timestamps do not change page content.
   IF ROW(OLD.kind,OLD.token,OLD.name,OLD.names,OLD.version,OLD.desc_en,OLD.homepage,OLD.download_url,OLD.download_size,OLD.license,OLD.artifacts,OLD.caveats,OLD.dependencies,OLD.conflicts_with,OLD.min_macos,OLD.supports_arm64,OLD.supports_x86_64,OLD.disabled,OLD.deprecated,OLD.stale_disabled,OLD.disable_reason,OLD.deprecation_reason,OLD.removed_at,OLD.source_locale,OLD.raw->'variations',OLD.raw->'sha256') IS NOT DISTINCT FROM ROW(NEW.kind,NEW.token,NEW.name,NEW.names,NEW.version,NEW.desc_en,NEW.homepage,NEW.download_url,NEW.download_size,NEW.license,NEW.artifacts,NEW.caveats,NEW.dependencies,NEW.conflicts_with,NEW.min_macos,NEW.supports_arm64,NEW.supports_x86_64,NEW.disabled,NEW.deprecated,NEW.stale_disabled,NEW.disable_reason,NEW.deprecation_reason,NEW.removed_at,NEW.source_locale,NEW.raw->'variations',NEW.raw->'sha256') THEN RETURN NULL; END IF;
  ELSIF (to_jsonb(OLD)-ARRAY['updated_at','synced_at','fetched_at']) IS NOT DISTINCT FROM
        (to_jsonb(NEW)-ARRAY['updated_at','synced_at','fetched_at']) THEN
   RETURN NULL;
  END IF;
 END IF;
 FOR row_data IN SELECT DISTINCT v FROM (VALUES (to_jsonb(OLD)),(to_jsonb(NEW))) d(v) WHERE v IS NOT NULL LOOP
  IF TG_TABLE_NAME='packages' THEN
   pid := (row_data->>'id')::bigint;
  ELSIF TG_TABLE_NAME='release_i18n' THEN
   SELECT package_id INTO pid FROM releases WHERE id=(row_data->>'release_id')::bigint;
  ELSIF TG_TABLE_NAME='article_revision_i18n' THEN
   package_ids := package_ids || ARRAY(SELECT package_id FROM article_revision_packages WHERE revision_id=(row_data->>'revision_id')::bigint);
   CONTINUE;
  ELSIF TG_TABLE_NAME='screenshot_i18n' THEN
   SELECT package_id INTO pid FROM package_screenshots WHERE id=(row_data->>'screenshot_id')::bigint;
  ELSE
   pid := (row_data->>'package_id')::bigint;
  END IF;
  package_ids := array_append(package_ids,pid);
  IF TG_TABLE_NAME='packages' THEN
   -- Match 00015's partial GIN predicate; token equality uses the catalog B-tree.
   package_ids := package_ids || ARRAY(
    SELECT p.id FROM packages p WHERE p.removed_at IS NULL AND p.dependencies->'dependsOn'->'cask' @> jsonb_build_array(row_data->>'token')
    UNION SELECT p.id FROM jsonb_array_elements_text(COALESCE(row_data->'dependencies'->'dependsOn'->'cask','[]'::jsonb)) dep(token)
     JOIN packages p ON p.kind='cask' AND p.token=dep.token WHERE p.removed_at IS NULL
   );
  END IF;
 END LOOP;
 -- One queue upsert per affected package, also across multiple triggers in the
 -- same transaction. Related cards are found by their existing reverse index.
 INSERT INTO package_page_dirty(package_id)
 SELECT p.id FROM packages p JOIN (
  SELECT DISTINCT unnest(package_ids) id
  UNION SELECT a.package_id FROM package_alternatives a WHERE a.alternative_id=ANY(package_ids)
 ) affected ON affected.id=p.id WHERE p.kind IN ('cask','link')
 ON CONFLICT(package_id) DO UPDATE SET changed_at=EXCLUDED.changed_at
 WHERE package_page_dirty.changed_at<EXCLUDED.changed_at;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON packages FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON package_meta FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON package_i18n FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON package_categories FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON package_screenshots FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON package_versions FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON releases FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON release_i18n FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON package_facts FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON package_alternatives FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON package_fact_records FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON package_source_facts FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON package_aliases FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON collection_items FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON article_revision_packages FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON link_apps FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();

-- Name/visibility changes of a related entity affect the card rendered on its app pages.
-- +goose StatementBegin
CREATE FUNCTION queue_related_page_dirty() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE row_data jsonb; ref_id bigint;
BEGIN
 IF TG_OP='UPDATE' AND (to_jsonb(OLD)-'updated_at') IS NOT DISTINCT FROM (to_jsonb(NEW)-'updated_at') THEN RETURN NULL; END IF;
 FOR row_data IN SELECT v FROM (VALUES (to_jsonb(OLD)),(to_jsonb(NEW))) d(v) WHERE v IS NOT NULL LOOP
  IF TG_TABLE_NAME IN ('categories','category_i18n') THEN
   ref_id := COALESCE((row_data->>'category_id')::bigint,(row_data->>'id')::bigint);
   INSERT INTO package_page_dirty(package_id) SELECT package_id FROM package_categories WHERE category_id=ref_id
   ON CONFLICT(package_id) DO UPDATE SET changed_at=EXCLUDED.changed_at WHERE package_page_dirty.changed_at<EXCLUDED.changed_at;
  ELSIF TG_TABLE_NAME IN ('collections','collection_i18n') THEN
   ref_id := COALESCE((row_data->>'collection_id')::bigint,(row_data->>'id')::bigint);
   INSERT INTO package_page_dirty(package_id) SELECT package_id FROM collection_items WHERE collection_id=ref_id
   ON CONFLICT(package_id) DO UPDATE SET changed_at=EXCLUDED.changed_at WHERE package_page_dirty.changed_at<EXCLUDED.changed_at;
  ELSE
   ref_id := COALESCE((row_data->>'article_id')::bigint,(row_data->>'id')::bigint);
   INSERT INTO package_page_dirty(package_id) SELECT DISTINCT rp.package_id FROM article_revision_packages rp JOIN article_revisions r ON r.id=rp.revision_id WHERE r.article_id=ref_id
   ON CONFLICT(package_id) DO UPDATE SET changed_at=EXCLUDED.changed_at WHERE package_page_dirty.changed_at<EXCLUDED.changed_at;
  END IF;
 END LOOP;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON categories FOR EACH ROW EXECUTE FUNCTION queue_related_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON category_i18n FOR EACH ROW EXECUTE FUNCTION queue_related_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON collections FOR EACH ROW EXECUTE FUNCTION queue_related_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON collection_i18n FOR EACH ROW EXECUTE FUNCTION queue_related_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON articles FOR EACH ROW EXECUTE FUNCTION queue_related_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON article_revisions FOR EACH ROW EXECUTE FUNCTION queue_related_page_dirty();

CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON screenshot_i18n FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();

CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON article_revision_i18n FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();

CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON package_version_requirements FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();

CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON package_cve_checks FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();

-- +goose Down
-- Hashes are derived, but a deletion's true content date cannot be reconstructed.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM package_page_state) THEN
  RAISE EXCEPTION 'Archive and explicitly resolve page content dates before rolling back';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER seo_page_dirty ON package_cve_checks;
DROP TRIGGER seo_page_dirty ON package_version_requirements;
DROP TRIGGER seo_page_dirty ON article_revision_i18n;
DROP TRIGGER seo_page_dirty ON screenshot_i18n;
DROP TRIGGER seo_page_dirty ON packages;
DROP TRIGGER seo_page_dirty ON package_meta;
DROP TRIGGER seo_page_dirty ON package_i18n;
DROP TRIGGER seo_page_dirty ON package_categories;
DROP TRIGGER seo_page_dirty ON package_screenshots;
DROP TRIGGER seo_page_dirty ON package_versions;
DROP TRIGGER seo_page_dirty ON releases;
DROP TRIGGER seo_page_dirty ON release_i18n;
DROP TRIGGER seo_page_dirty ON package_facts;
DROP TRIGGER seo_page_dirty ON package_alternatives;
DROP TRIGGER seo_page_dirty ON package_fact_records;
DROP TRIGGER seo_page_dirty ON package_source_facts;
DROP TRIGGER seo_page_dirty ON package_aliases;
DROP TRIGGER seo_page_dirty ON collection_items;
DROP TRIGGER seo_page_dirty ON article_revision_packages;
DROP TRIGGER seo_page_dirty ON link_apps;
DROP TRIGGER seo_page_dirty ON categories;
DROP TRIGGER seo_page_dirty ON category_i18n;
DROP TRIGGER seo_page_dirty ON collections;
DROP TRIGGER seo_page_dirty ON collection_i18n;
DROP TRIGGER seo_page_dirty ON articles;
DROP TRIGGER seo_page_dirty ON article_revisions;
DROP FUNCTION queue_package_page_dirty();
DROP FUNCTION queue_related_page_dirty();
DROP TABLE package_page_dirty;
DROP TABLE package_page_state;
