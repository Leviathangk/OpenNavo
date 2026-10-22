-- +goose Up
ALTER TABLE usage_daily ADD COLUMN country VARCHAR(2) NOT NULL DEFAULT 'ZZ' CHECK (country ~ '^[A-Z]{2}$');
-- Enable first-party web usage by default, including previously saved settings.
UPDATE seo_settings SET settings=jsonb_set(settings,'{analytics}',COALESCE(settings->'analytics','{}'::jsonb)||'{"usageEnabled":true}'::jsonb) WHERE id=true;

-- +goose Down
ALTER TABLE usage_daily DROP COLUMN country;
-- Keep the administrator's current collection setting on rollback.
