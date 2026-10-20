-- +goose Up
ALTER TABLE packages ADD COLUMN homebrew_checked_at TIMESTAMPTZ;
-- Conservative backfill runs in bounded transactions in 00044 after this DDL commits.

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 LOCK TABLE packages IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM packages WHERE homebrew_checked_at IS NOT NULL) THEN
  RAISE EXCEPTION 'Archive and explicitly resolve sync confirmations before Down 00043';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE packages DROP COLUMN homebrew_checked_at;
