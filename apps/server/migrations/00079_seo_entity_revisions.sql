-- +goose Up
CREATE TABLE seo_entity_revisions(entity_key TEXT PRIMARY KEY,revision BIGINT NOT NULL DEFAULT 0 CHECK(revision>=0));
INSERT INTO seo_entity_revisions VALUES('site:keywords',0);
INSERT INTO seo_entity_revisions SELECT DISTINCT 'background:'||label,1 FROM background_item_content;
INSERT INTO seo_entity_revisions SELECT 'update_feed:'||source||':'||bundle_id,1 FROM update_feeds;
-- +goose StatementBegin
CREATE FUNCTION seo_bump_entity_revision() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE data JSONB; entity TEXT;
BEGIN
 IF TG_OP='DELETE' THEN data:=to_jsonb(OLD); ELSE data:=to_jsonb(NEW); END IF;
 IF TG_TABLE_NAME='background_item_content' THEN entity:='background:'||(data->>'label');
 ELSIF TG_TABLE_NAME='update_feeds' THEN entity:='update_feed:'||(data->>'source')||':'||(data->>'bundle_id');
 ELSE entity:='site:keywords'; END IF;
 INSERT INTO seo_entity_revisions(entity_key,revision) VALUES(entity,1) ON CONFLICT(entity_key) DO UPDATE SET revision=seo_entity_revisions.revision+1;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER seo_background_revision AFTER INSERT OR UPDATE OR DELETE ON background_item_content FOR EACH ROW EXECUTE FUNCTION seo_bump_entity_revision();
CREATE TRIGGER seo_feed_revision AFTER INSERT OR UPDATE OR DELETE ON update_feeds FOR EACH ROW EXECUTE FUNCTION seo_bump_entity_revision();
CREATE TRIGGER seo_keyword_revision AFTER INSERT OR UPDATE OR DELETE ON seo_keywords FOR EACH ROW EXECUTE FUNCTION seo_bump_entity_revision();
CREATE TRIGGER seo_target_revision AFTER INSERT OR UPDATE OR DELETE ON seo_targets FOR EACH ROW EXECUTE FUNCTION seo_bump_entity_revision();
-- +goose Down
DROP TRIGGER seo_background_revision ON background_item_content;
DROP TRIGGER seo_feed_revision ON update_feeds;
DROP TRIGGER seo_keyword_revision ON seo_keywords;
DROP TRIGGER seo_target_revision ON seo_targets;
DROP FUNCTION seo_bump_entity_revision();
DROP TABLE seo_entity_revisions;
