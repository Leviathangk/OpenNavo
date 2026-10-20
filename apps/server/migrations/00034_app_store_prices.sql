-- +goose Up
-- Factual lookup observations only: no artwork, screenshots, audio or promotional assets.
CREATE TABLE app_store_price_snapshots (
 package_id BIGINT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
 observed_on DATE NOT NULL,
 region TEXT NOT NULL CHECK (region ~ '^[A-Z]{2}$'),
 bundle_id TEXT NOT NULL,
 amount NUMERIC(16,6) NOT NULL CHECK (amount >= 0),
 currency TEXT NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
 purchase_url TEXT NOT NULL CHECK (purchase_url LIKE 'https://apps.apple.com/%'),
 checked_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(package_id,observed_on,region,bundle_id)
);
CREATE INDEX app_store_prices_current ON app_store_price_snapshots(package_id, checked_at DESC);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 LOCK TABLE app_store_price_snapshots IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM app_store_price_snapshots) THEN
  RAISE EXCEPTION 'Archive and explicitly resolve app_store_price_snapshots evidence before Down 00034';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE app_store_price_snapshots;
