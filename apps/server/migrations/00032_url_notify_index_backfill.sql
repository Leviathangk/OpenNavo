-- +goose Up
-- The last one-time IndexNow backfill the URL change feed finished (urlnotify.indexBackfill): pages an
-- index rule change brought back into search without any catalog change to announce them.
ALTER TABLE url_notify_state ADD COLUMN index_backfill TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE url_notify_state DROP COLUMN index_backfill;
