-- +goose Up
CREATE TABLE package_version_requirements (
 package_id BIGINT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
 app_version TEXT NOT NULL,
 platforms JSONB NOT NULL CHECK(jsonb_typeof(platforms)='array'),
 source_url TEXT NOT NULL,
 checked_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(package_id,app_version)
);
-- +goose StatementBegin
CREATE FUNCTION reconcile_version_requirements(p packages) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 IF p.kind <> 'cask' THEN RETURN; END IF;
 INSERT INTO package_version_requirements(package_id,app_version,platforms,source_url,checked_at)
 SELECT p.id,platform->>'version',jsonb_agg(platform ORDER BY platform->>'tag',platform::text),
 'https://github.com/'||split_part(p.tap,'/',1)||'/homebrew-'||split_part(p.tap,'/',2)||'/blob/HEAD/'||
 COALESCE(NULLIF(p.raw->>'ruby_source_path',''),'Casks/'||left(p.token,1)||'/'||p.token||'.rb'),p.updated_at
 FROM (
 SELECT jsonb_strip_nulls(jsonb_build_object(
  'tag',v->'tag','version',v->'version','arch',v->'arch','macos',v->'macos',
  'minMacos',v->'minMacos','requiresRosetta',v->'requiresRosetta',
  'downloadUrl',v->'downloadUrl','downloadSha256',v->'downloadSha256',
  'dependsOn',jsonb_build_object('requirements',jsonb_strip_nulls(jsonb_build_object('macos',v->'dependsOn'->'requirements'->'macos'))))) AS platform
 FROM jsonb_array_elements(COALESCE(p.dependencies->'platforms','[]'::jsonb)) v
 UNION ALL
 SELECT jsonb_build_object('version',p.version,'arch','','minMacos',p.min_macos,
  'dependsOn',jsonb_build_object('requirements',jsonb_strip_nulls(jsonb_build_object('macos',p.dependencies->'dependsOn'->'macos'))))
 WHERE jsonb_array_length(COALESCE(p.dependencies->'platforms','[]'::jsonb))=0
 AND (p.min_macos IS NOT NULL OR p.dependencies->'dependsOn' ? 'macos')
 ) observed
 WHERE COALESCE(platform->>'version','')<>'' GROUP BY platform->>'version'
 ON CONFLICT(package_id,app_version) DO UPDATE SET platforms=EXCLUDED.platforms,source_url=EXCLUDED.source_url,checked_at=EXCLUDED.checked_at
 WHERE (package_version_requirements.platforms,package_version_requirements.source_url)
 IS DISTINCT FROM (EXCLUDED.platforms,EXCLUDED.source_url);
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION capture_version_requirements() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND ROW(NEW.kind,NEW.token,NEW.tap,NEW.raw->>'ruby_source_path',NEW.version,NEW.min_macos,NEW.dependencies->'platforms',NEW.dependencies->'dependsOn'->'macos')
  IS NOT DISTINCT FROM ROW(OLD.kind,OLD.token,OLD.tap,OLD.raw->>'ruby_source_path',OLD.version,OLD.min_macos,OLD.dependencies->'platforms',OLD.dependencies->'dependsOn'->'macos') THEN RETURN NEW; END IF;
 PERFORM reconcile_version_requirements(NEW);
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER package_version_requirements_capture AFTER INSERT OR UPDATE OF kind,token,tap,raw,version,min_macos,dependencies ON packages
 FOR EACH ROW EXECUTE FUNCTION capture_version_requirements();
-- Backfill runs in 00044 without rewriting packages or retaining this DDL lock.
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 LOCK TABLE package_version_requirements IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM package_version_requirements) THEN
  RAISE EXCEPTION 'Archive and explicitly resolve version evidence before Down 00038';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER package_version_requirements_capture ON packages;
DROP FUNCTION capture_version_requirements();
DROP FUNCTION reconcile_version_requirements(packages);
DROP TABLE package_version_requirements;
