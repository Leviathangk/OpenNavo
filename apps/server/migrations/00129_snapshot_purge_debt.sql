-- +goose Up
-- When the oldest queued snapshot purge was enqueued; NULL while no snapshot prefix is queued.
-- Page backlog and snapshot debt share one durable queue but are alarmed separately.
ALTER TABLE url_notify_state ADD COLUMN pending_snapshot_purges_since TIMESTAMPTZ;

-- Existing deployments may already hold snapshot prefixes; the queue's clock is the best available
-- lower bound, matching the backfill in 00127.
-- +goose StatementBegin
UPDATE url_notify_state SET pending_snapshot_purges_since=pending_purges_since
WHERE pending_purges_since IS NOT NULL
  AND EXISTS (SELECT 1 FROM jsonb_array_elements_text(pending_purges) AS prefix WHERE prefix LIKE '%/snapshots/%');
-- +goose StatementEnd

-- +goose Down
ALTER TABLE url_notify_state DROP COLUMN pending_snapshot_purges_since;
