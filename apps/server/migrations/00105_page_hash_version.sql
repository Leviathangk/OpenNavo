-- +goose Up
-- Rows written before versions were recorded are version 1. The worker proactively rebaselines old
-- hashes without changing dates for idle pages. Pending content jobs keep their actual change time
-- even when their old hash cannot be compared with the new version.
ALTER TABLE package_page_state ADD COLUMN content_hash_version SMALLINT NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE package_page_state DROP COLUMN content_hash_version;
