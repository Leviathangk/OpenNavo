-- +goose Up
ALTER TABLE feedback ADD COLUMN package_version TEXT,
 ADD COLUMN macos_version TEXT,
 ADD COLUMN symptom TEXT,
 ADD COLUMN diagnostic_log TEXT,
 ADD COLUMN links JSONB NOT NULL DEFAULT '[]',
 ADD COLUMN visibility TEXT NOT NULL DEFAULT 'private',
 ADD CONSTRAINT feedback_links_check CHECK(jsonb_typeof(links)='array' AND jsonb_array_length(links)<=3) NOT VALID,
 ADD CONSTRAINT feedback_visibility_check CHECK(visibility='private') NOT VALID;
-- Validated independently in 00045 after the ADD COLUMN lock commits.
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 LOCK TABLE feedback IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM feedback) THEN
  RAISE EXCEPTION 'Archive and explicitly resolve private feedback before Down 00039';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE feedback DROP COLUMN package_version,DROP COLUMN macos_version,DROP COLUMN symptom,DROP COLUMN diagnostic_log,DROP COLUMN links,DROP COLUMN visibility;
