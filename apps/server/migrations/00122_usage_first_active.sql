-- +goose Up
ALTER TABLE usage_daily ADD COLUMN first_active_at TIMESTAMPTZ;
-- Earlier first observations cannot be recovered; preserve the available observation once.
UPDATE usage_daily SET first_active_at=last_active_at;

-- Accept inserts from the previous server during a rolling upgrade, and retain
-- the earliest observation when later versions or offline retries arrive.
-- +goose StatementBegin
CREATE FUNCTION usage_daily_first_active() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    NEW.first_active_at := COALESCE(NEW.first_active_at, NEW.last_active_at);
  ELSE
    NEW.first_active_at := LEAST(COALESCE(OLD.first_active_at, NEW.first_active_at),
                                 COALESCE(NEW.first_active_at, OLD.first_active_at));
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER usage_daily_first_active BEFORE INSERT OR UPDATE ON usage_daily
  FOR EACH ROW EXECUTE FUNCTION usage_daily_first_active();
ALTER TABLE usage_daily ALTER COLUMN first_active_at SET NOT NULL;

-- +goose Down
DROP TRIGGER usage_daily_first_active ON usage_daily;
DROP FUNCTION usage_daily_first_active();
ALTER TABLE usage_daily DROP COLUMN first_active_at;
