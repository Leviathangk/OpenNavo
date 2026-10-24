-- +goose Up
-- Versions pages count how many releases on the page carry notes (at least three) instead of requiring a
-- source URL on each note: editorial notes never store one, so no versions page could qualify. NULL until the
-- page-state backfill recomputes a package (about 200 per seo:pages run); the API reads live facts meanwhile.
-- notes_has_source stays for a rollback and is no longer written.
ALTER TABLE package_page_state ADD COLUMN notes_entries INTEGER;

-- +goose Down
ALTER TABLE package_page_state DROP COLUMN notes_entries;
