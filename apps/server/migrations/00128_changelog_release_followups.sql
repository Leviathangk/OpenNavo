-- +goose Up
-- releases.title_edited: an editor's title must survive upstream renames; without the marker the
-- upsert could not tell "the upstream never changed its title" from "an editor replaced it".
ALTER TABLE releases ADD COLUMN title_edited BOOLEAN NOT NULL DEFAULT FALSE;

-- changelog_sources.archived: an archived repository still gets a source, but every fetch of it
-- costs quota for notes that will not change, so the schedule fetches it less often.
ALTER TABLE changelog_sources ADD COLUMN archived BOOLEAN NOT NULL DEFAULT FALSE;

-- changelog_sources.content_hash: the last release list this source stored, so a 200 that repeats
-- the same list is recognised without spending a second request on the repository visibility check.
ALTER TABLE changelog_sources ADD COLUMN content_hash TEXT;

-- +goose Down
ALTER TABLE changelog_sources DROP COLUMN content_hash;
ALTER TABLE changelog_sources DROP COLUMN archived;
ALTER TABLE releases DROP COLUMN title_edited;
