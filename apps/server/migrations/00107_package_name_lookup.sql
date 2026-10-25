-- +goose NO TRANSACTION
-- +goose Up
-- A failed concurrent build can leave an invalid index. Drop it before retrying.
DROP INDEX CONCURRENTLY IF EXISTS packages_active_name_lookup;
CREATE INDEX CONCURRENTLY packages_active_name_lookup ON packages(lower(trim(name))) WHERE removed_at IS NULL AND kind IN ('cask','link');

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS packages_active_name_lookup;
