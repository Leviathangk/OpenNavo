-- +goose Up
-- Versions pages record the notes facts the sitemap's thin-page rule reads, so a sitemap page no longer re-reads
-- every release of hundreds of apps in every language. NULL until the page-state job (or its backfill) computes them.
ALTER TABLE package_page_state ADD COLUMN notes_chars INTEGER, ADD COLUMN notes_has_source BOOLEAN;

-- +goose Down
ALTER TABLE package_page_state DROP COLUMN notes_has_source, DROP COLUMN notes_chars;
