-- +goose Up
-- When the oldest durable CDN purge debt was queued; NULL while the queue is empty.
ALTER TABLE url_notify_state ADD COLUMN pending_purges_since TIMESTAMPTZ;

-- +goose Down
ALTER TABLE url_notify_state DROP COLUMN pending_purges_since;
