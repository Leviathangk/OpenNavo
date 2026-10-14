-- +goose Up
-- Editorial notes can belong to one exact Homebrew build (2026.4,237457). Casks such as Amore keep
-- the version before the comma unchanged while every build ships its own notes, so a note keyed only
-- by version_base could not be written for the newest build without overwriting the older builds'
-- note. NULL keeps the existing shared note of the base version; builds without their own note still
-- show it, so no existing association changes. The composite key keeps a build in its own package.
ALTER TABLE package_versions ADD CONSTRAINT package_versions_id_package_key UNIQUE (id, package_id);
ALTER TABLE releases ADD COLUMN package_version_id BIGINT;
ALTER TABLE releases ADD CONSTRAINT releases_package_version_fkey FOREIGN KEY (package_version_id, package_id) REFERENCES package_versions(id, package_id) ON DELETE CASCADE;
ALTER TABLE releases ADD CONSTRAINT releases_build_notes_editorial CHECK (package_version_id IS NULL OR source='editorial');
-- One visible note per build; shared base-version notes keep their existing per-package lock rule.
CREATE UNIQUE INDEX releases_build_editorial ON releases(package_version_id) WHERE package_version_id IS NOT NULL AND source='editorial' AND NOT hidden;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM releases WHERE package_version_id IS NOT NULL) THEN
    RAISE EXCEPTION 'Build release notes exist; downgrading would merge them into shared base-version notes';
  END IF;
END $$;
-- +goose StatementEnd
DROP INDEX releases_build_editorial;
ALTER TABLE releases DROP CONSTRAINT releases_build_notes_editorial;
ALTER TABLE releases DROP CONSTRAINT releases_package_version_fkey;
ALTER TABLE releases DROP COLUMN package_version_id;
ALTER TABLE package_versions DROP CONSTRAINT package_versions_id_package_key;
