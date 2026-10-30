-- +goose Up
ALTER TABLE packages
  ADD COLUMN disabled_override_at TIMESTAMPTZ,
  ADD COLUMN disabled_override_date DATE,
  ADD COLUMN disabled_override_reason TEXT,
  ADD COLUMN disabled_override_replacement TEXT,
  ADD COLUMN disabled_override_stale BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN disabled_override_version TEXT;

-- +goose Down
ALTER TABLE packages
  DROP COLUMN disabled_override_replacement,
  DROP COLUMN disabled_override_reason,
  DROP COLUMN disabled_override_date,
  DROP COLUMN disabled_override_at,
  DROP COLUMN disabled_override_stale,
  DROP COLUMN disabled_override_version;
