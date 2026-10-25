-- +goose Up
ALTER TABLE url_notify_state ADD COLUMN probe_ready_at timestamptz;
ALTER TABLE url_notify_state ADD COLUMN probe_failures integer NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE url_notify_state DROP COLUMN probe_ready_at;
ALTER TABLE url_notify_state DROP COLUMN probe_failures;
