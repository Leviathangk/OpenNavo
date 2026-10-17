-- +goose Up
ALTER TABLE url_notify_state ADD COLUMN pending_purges JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE url_notify_state ADD COLUMN next_purge_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE url_notify_state DROP COLUMN next_purge_at;
ALTER TABLE url_notify_state DROP COLUMN pending_purges;
