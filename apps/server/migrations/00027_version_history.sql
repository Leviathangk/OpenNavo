-- +goose Up
-- The tap sync records every version a tap cask file's recent commits declare. history_sha is the newest
-- commit whose history has been read; NULL (every cask added so far) reads it on the next sync.
ALTER TABLE tap_casks ADD COLUMN history_sha TEXT;

-- Homebrew commit titles with annotations after the version ("token 1.2 (new cask)", "(arm only)",
-- "(#123)") now date versions. Drop the incremental cursor so each history is read in full once more;
-- next_fetch_at is kept, so these reads follow the normal schedule instead of arriving at once.
UPDATE changelog_sources SET etag=NULL,last_modified=NULL,last_fetched_at=NULL,updated_at=now()
WHERE type='homebrew_commits' AND last_fetched_at IS NOT NULL;

-- +goose Down
ALTER TABLE tap_casks DROP COLUMN history_sha;
