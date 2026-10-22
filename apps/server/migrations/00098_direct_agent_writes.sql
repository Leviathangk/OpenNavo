-- +goose Up
-- Listing candidates are an optional editorial workspace. Published link apps
-- now use the same editable identity and download fields as directly added apps.
DROP TRIGGER automatic_listing_download ON link_apps;
DROP TRIGGER automatic_listing_package ON packages;

-- Link editing has its own concurrency token: content_revisions only covers
-- translated/editorial package content. Track all writers, including candidates
-- and changes to shared package fields, without versioning background syncs.
ALTER TABLE link_apps ADD COLUMN revision BIGINT NOT NULL DEFAULT 0 CHECK(revision>=0);
-- +goose StatementBegin
CREATE FUNCTION bump_link_edit_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='packages' THEN
  UPDATE link_apps SET revision=revision+1 WHERE package_id=NEW.id;
 ELSE
  NEW.revision:=OLD.revision+1;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER link_fields_revision BEFORE UPDATE ON link_apps FOR EACH ROW
 WHEN ((OLD.homepage,OLD.download_url,OLD.github_repo,OLD.bundle_ids,OLD.team_id,OLD.pricing,OLD.sparkle_feed,OLD.asset_rules,OLD.notes)
 IS DISTINCT FROM (NEW.homepage,NEW.download_url,NEW.github_repo,NEW.bundle_ids,NEW.team_id,NEW.pricing,NEW.sparkle_feed,NEW.asset_rules,NEW.notes))
 EXECUTE FUNCTION bump_link_edit_revision();
CREATE TRIGGER link_package_revision AFTER UPDATE ON packages FOR EACH ROW
 WHEN ((OLD.kind='link' OR NEW.kind='link') AND
 (OLD.name,OLD.source_locale,OLD.kind,OLD.removed_at) IS DISTINCT FROM (NEW.name,NEW.source_locale,NEW.kind,NEW.removed_at))
 EXECUTE FUNCTION bump_link_edit_revision();

-- Preserve exact search queries. Normalization is for matching, not identity.
-- +goose StatementBegin
DO $$ DECLARE constraint_name text; BEGIN
 SELECT conname INTO STRICT constraint_name FROM pg_constraint
 WHERE conrelid='seo_keywords'::regclass AND contype='u'
 AND pg_get_constraintdef(oid) LIKE 'UNIQUE (normalized,%';
 EXECUTE format('ALTER TABLE seo_keywords DROP CONSTRAINT %I', constraint_name);
END $$;
-- +goose StatementEnd
ALTER TABLE seo_keywords ADD CONSTRAINT seo_keywords_query_identity
 UNIQUE(keyword,source,engine,country,language,device,period);
CREATE INDEX seo_keywords_normalized ON seo_keywords(normalized);

-- +goose Down
DROP TRIGGER link_package_revision ON packages;
DROP TRIGGER link_fields_revision ON link_apps;
DROP FUNCTION bump_link_edit_revision();
ALTER TABLE link_apps DROP COLUMN revision;
-- This fails without modifying data if distinct queries now normalize alike.
ALTER TABLE seo_keywords ADD CONSTRAINT seo_keywords_normalized_identity
 UNIQUE(normalized,source,engine,country,language,device,period);
ALTER TABLE seo_keywords DROP CONSTRAINT seo_keywords_query_identity;
DROP INDEX seo_keywords_normalized;
CREATE TRIGGER automatic_listing_download BEFORE INSERT OR UPDATE ON link_apps
 FOR EACH ROW EXECUTE FUNCTION protect_automatic_listing_download();
CREATE TRIGGER automatic_listing_package BEFORE UPDATE ON packages
 FOR EACH ROW EXECUTE FUNCTION protect_automatic_listing_package();
