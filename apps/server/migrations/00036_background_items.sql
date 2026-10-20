-- +goose Up
CREATE TABLE background_items (
 label TEXT PRIMARY KEY CHECK(length(label) BETWEEN 1 AND 255)
);
CREATE TABLE background_item_packages (
 label TEXT NOT NULL REFERENCES background_items(label) ON DELETE CASCADE,
 package_id BIGINT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('launchctl','login_item','pkgutil')),
 phase TEXT NOT NULL CHECK(phase IN ('uninstall','zap')),
 source_url TEXT NOT NULL,
 checked_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(label,package_id,kind,phase)
);
CREATE INDEX background_item_packages_package ON background_item_packages(package_id);
CREATE TABLE background_item_content (
 label TEXT NOT NULL REFERENCES background_items(label) ON DELETE CASCADE,
 locale TEXT NOT NULL CHECK(locale IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU','de-DE','fr-FR','it-IT','zh-Hant')),
 description TEXT NOT NULL CHECK(length(description) BETWEEN 1 AND 10000),
 item_type TEXT NOT NULL CHECK(item_type IN ('launch_agent','launch_daemon','login_item','package_receipt','unknown')),
 common_paths JSONB NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(common_paths)='array'),
 evidence JSONB NOT NULL,
 PRIMARY KEY(label,locale)
);

-- +goose StatementBegin
CREATE FUNCTION background_declarations(p packages) RETURNS TABLE(label text,kind text,phase text,source_url text)
LANGUAGE sql STABLE AS $$
 WITH artifact_sets AS (
  SELECT p.artifacts AS a WHERE p.kind='cask'
  UNION ALL SELECT platform->'artifacts' FROM jsonb_array_elements(COALESCE(p.dependencies->'platforms','[]'::jsonb)) platform WHERE p.kind='cask'
 ), entries AS (
  SELECT entry FROM artifact_sets, LATERAL jsonb_array_elements(COALESCE(a->'entries','[]'::jsonb)) entry
  WHERE entry->>'phase' IN ('uninstall','cleanup')
 ), extracted_values AS (
  SELECT entry->>'phase' AS phase,k.kind,v.value FROM entries
  CROSS JOIN (VALUES ('launchctl'),('login_item'),('pkgutil')) k(kind)
  CROSS JOIN LATERAL jsonb_path_query(entry->'declaration',('$.**.'||k.kind)::jsonpath) v(value)
 ), labels AS (
  SELECT phase,kind,label #>> '{}' AS label FROM extracted_values
  CROSS JOIN LATERAL jsonb_array_elements(CASE jsonb_typeof(value) WHEN 'array' THEN value ELSE jsonb_build_array(value) END) label
  WHERE jsonb_typeof(label)='string'
 ) SELECT DISTINCT label,kind,CASE phase WHEN 'cleanup' THEN 'zap' ELSE 'uninstall' END,
  'https://github.com/'||split_part(p.tap,'/',1)||'/homebrew-'||split_part(p.tap,'/',2)||'/blob/HEAD/'||
  COALESCE(NULLIF(p.raw->>'ruby_source_path',''),'Casks/'||left(p.token,1)||'/'||p.token||'.rb')
 FROM labels WHERE octet_length(label) BETWEEN 1 AND 255 AND label=btrim(label) AND label !~ $label$[/\\?#[:cntrl:]]$label$
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION reconcile_background_declarations(p packages) RETURNS void LANGUAGE plpgsql AS $$
DECLARE expected jsonb;
BEGIN
 SELECT COALESCE(jsonb_agg(to_jsonb(d)),'[]') INTO expected FROM background_declarations(p) d;
 DELETE FROM background_item_packages b WHERE b.package_id=p.id AND NOT EXISTS (
  SELECT 1 FROM jsonb_to_recordset(expected) d(label text,kind text,phase text,source_url text)
  WHERE (b.label,b.kind,b.phase)=(d.label,d.kind,d.phase));
 INSERT INTO background_items(label) SELECT DISTINCT d.label FROM jsonb_to_recordset(expected) d(label text) ON CONFLICT DO NOTHING;
 INSERT INTO background_item_packages(label,package_id,kind,phase,source_url,checked_at)
 SELECT d.label,p.id,d.kind,d.phase,d.source_url,p.updated_at
 FROM jsonb_to_recordset(expected) d(label text,kind text,phase text,source_url text)
 ON CONFLICT(label,package_id,kind,phase) DO UPDATE SET source_url=EXCLUDED.source_url,checked_at=EXCLUDED.checked_at
 WHERE background_item_packages.source_url IS DISTINCT FROM EXCLUDED.source_url;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION refresh_background_declarations() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND ROW(NEW.kind,NEW.token,NEW.tap,NEW.raw->>'ruby_source_path',NEW.artifacts,NEW.dependencies->'platforms')
  IS NOT DISTINCT FROM ROW(OLD.kind,OLD.token,OLD.tap,OLD.raw->>'ruby_source_path',OLD.artifacts,OLD.dependencies->'platforms') THEN RETURN NEW; END IF;
 PERFORM reconcile_background_declarations(NEW);
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER package_background_declarations AFTER INSERT OR UPDATE OF kind,token,artifacts,dependencies,raw,tap ON packages
 FOR EACH ROW EXECUTE FUNCTION refresh_background_declarations();
-- Existing catalog data is backfilled in bounded transactions by 00044, after DDL commits.

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 LOCK TABLE background_items,background_item_packages,background_item_content IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM background_items) OR EXISTS(SELECT 1 FROM background_item_packages) OR EXISTS(SELECT 1 FROM background_item_content) THEN
  RAISE EXCEPTION 'Archive and explicitly resolve background evidence before Down 00036';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER package_background_declarations ON packages;
DROP FUNCTION refresh_background_declarations();
DROP FUNCTION reconcile_background_declarations(packages);
DROP FUNCTION background_declarations(packages);
DROP TABLE background_item_content;
DROP TABLE background_item_packages;
DROP TABLE background_items;
