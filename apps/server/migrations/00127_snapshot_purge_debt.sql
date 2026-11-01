-- +goose Up
-- When the oldest durable CDN purge debt was queued; NULL while the queue is empty.
ALTER TABLE url_notify_state ADD COLUMN pending_purges_since TIMESTAMPTZ;

-- A deployment that already holds queued purges would otherwise report a debt age of zero until
-- the next purge is queued. The true queue time is unknown, so the migration starts the clock now.
-- +goose StatementBegin
UPDATE url_notify_state SET pending_purges_since=now()
WHERE jsonb_array_length(pending_purges)>0 AND pending_purges_since IS NULL;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE url_notify_state DROP COLUMN pending_purges_since;
