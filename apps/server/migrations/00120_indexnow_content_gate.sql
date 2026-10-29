-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE indexnow_url_state (
 url TEXT PRIMARY KEY,
 announced_at TIMESTAMPTZ NOT NULL,
 robots TEXT NOT NULL DEFAULT '',
 canonical_path TEXT NOT NULL DEFAULT ''
);

-- Do not backfill from historical outbox receipts: updated_at is the request
-- completion time, not a verified pre-send content snapshot. A concurrent edit
-- may predate that receipt and still need delivery. Start with no baseline and
-- allow a conservative resend; new accepted requests record verified snapshots.

ALTER TABLE url_notify_outbox ADD COLUMN skip_reason TEXT NOT NULL DEFAULT ''
 CHECK (skip_reason IN ('','unchanged'));

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE url_notify_outbox DROP COLUMN skip_reason;
DROP TABLE indexnow_url_state;
