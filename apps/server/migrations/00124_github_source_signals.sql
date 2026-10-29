-- +goose Up
-- GitHub signal refreshes have their own daily schedule and backoff. The existing
-- checked_at/next_check_at/fail_count columns continue to govern the 30-day license
-- and repository identity check.
ALTER TABLE package_source_facts
  ADD COLUMN signals_checked_at TIMESTAMPTZ,
  ADD COLUMN signals_next_check_at TIMESTAMPTZ,
  ADD COLUMN signals_fail_count INTEGER NOT NULL DEFAULT 0 CHECK (signals_fail_count >= 0);

-- +goose Down
ALTER TABLE package_source_facts
  DROP COLUMN signals_fail_count,
  DROP COLUMN signals_next_check_at,
  DROP COLUMN signals_checked_at;
