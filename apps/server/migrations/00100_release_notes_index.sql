-- +goose NO TRANSACTION
-- +goose Up
-- Index and sitemap rows count each app's releases with non-blank notes. The predicate de-TOASTs every body, so this
-- partial index evaluates it once per write instead of on every count. IF NOT EXISTS keeps the migration idempotent
-- where an operator already built the index concurrently.
CREATE INDEX CONCURRENTLY IF NOT EXISTS releases_with_notes ON releases (package_id) WHERE NOT hidden AND NULLIF(btrim(body_markdown), '') IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS releases_with_notes;
