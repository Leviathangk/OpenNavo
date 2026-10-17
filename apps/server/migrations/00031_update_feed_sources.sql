-- +goose Up
-- Additional metadata stays outside legacy catalog serialization and revisions.
ALTER TABLE update_feeds DROP CONSTRAINT update_feeds_origin_check;
ALTER TABLE update_feeds ADD CONSTRAINT update_feeds_origin_check CHECK (origin IN ('manual','homebrew','link','seed'));
ALTER TABLE update_feeds ADD COLUMN revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE update_feeds ADD COLUMN github_repo TEXT;
ALTER TABLE update_feeds ADD COLUMN asset_pattern TEXT;
ALTER TABLE update_feeds ADD COLUMN resolver JSONB;
ALTER TABLE update_feeds ADD COLUMN release JSONB;
ALTER TABLE update_feeds ADD COLUMN last_modified TEXT;
ALTER TABLE update_feeds ADD CONSTRAINT update_feeds_expected_team CHECK (source NOT IN ('github','vendor') OR team_id IS NOT NULL);
-- last_error is an automatic suspension, independent of manual enabled=false.
-- +goose Down
ALTER TABLE update_feeds DROP CONSTRAINT update_feeds_expected_team;
ALTER TABLE update_feeds DROP COLUMN last_modified;
ALTER TABLE update_feeds DROP COLUMN release;
ALTER TABLE update_feeds DROP COLUMN resolver;
ALTER TABLE update_feeds DROP COLUMN asset_pattern;
ALTER TABLE update_feeds DROP COLUMN github_repo;
ALTER TABLE update_feeds DROP COLUMN revision;
DELETE FROM update_feeds WHERE origin='seed';
ALTER TABLE update_feeds DROP CONSTRAINT update_feeds_origin_check;
ALTER TABLE update_feeds ADD CONSTRAINT update_feeds_origin_check CHECK (origin IN ('manual','homebrew','link'));
