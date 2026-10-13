-- +goose Up
-- Single-row SEO and analytics settings edited in admin (System > SEO & analytics).
-- settings holds only non-secret values; credentials are stored as AES-GCM ciphertexts.
CREATE TABLE seo_settings (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    settings JSONB NOT NULL DEFAULT '{}'::jsonb,
    indexnow_key_ciphertext TEXT NOT NULL DEFAULT '',
    cloudflare_token_ciphertext TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0)
);
INSERT INTO seo_settings(id) VALUES(TRUE);
INSERT INTO admin_permissions(code,name,group_name) VALUES('system:seo:edit','SEO and analytics settings','system') ON CONFLICT(code) DO NOTHING;
INSERT INTO admin_role_permissions(role_id,permission_code) SELECT id,'system:seo:edit' FROM admin_roles WHERE role_code='R_ADMIN' ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM admin_role_permissions WHERE permission_code='system:seo:edit';
DELETE FROM admin_permissions WHERE code='system:seo:edit';
DROP TABLE seo_settings;
