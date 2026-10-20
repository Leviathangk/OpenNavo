-- +goose Up
-- Explicit manifests: old assets remain [], without claiming nonexistent variants.
ALTER TABLE assets ADD COLUMN files JSONB NOT NULL DEFAULT '[]',
 ADD CONSTRAINT assets_files_check CHECK(jsonb_typeof(files)='array') NOT VALID;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 LOCK TABLE assets IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM assets WHERE files<>'[]'::jsonb) THEN
  RAISE EXCEPTION 'Archive and explicitly resolve asset manifests before Down 00041';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE assets DROP COLUMN files;
