-- +goose Up
-- Manual override of the index rule per app (WP11), set in admin with a reason that the impact
-- report shows. auto follows the computed rule.
ALTER TABLE package_meta
  ADD COLUMN index_override TEXT NOT NULL DEFAULT 'auto' CHECK (index_override IN ('auto','index','noindex')),
  ADD COLUMN index_override_reason TEXT,
  ADD COLUMN index_override_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE package_meta
  DROP COLUMN index_override_at,
  DROP COLUMN index_override_reason,
  DROP COLUMN index_override;
