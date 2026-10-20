-- +goose Up
CREATE TABLE seo_task_controls (
 owner TEXT NOT NULL,
 idempotency_key TEXT NOT NULL CHECK(length(idempotency_key) BETWEEN 8 AND 200),
 operation TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 result JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(owner,idempotency_key)
);
CREATE TABLE seo_opportunity_cursors (
 source TEXT PRIMARY KEY,
 cursor BIGINT NOT NULL DEFAULT 0,
 signature TEXT NOT NULL DEFAULT '',
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO seo_opportunity_cursors(source) VALUES('catalog_changes');
INSERT INTO admin_permissions(code,name,group_name) VALUES('seo:task:handle','Handle SEO tasks','seo'),('seo:snippet:propose','Propose SEO templates','seo') ON CONFLICT DO NOTHING;
INSERT INTO admin_role_permissions(role_id,permission_code) SELECT id,p.code FROM admin_roles CROSS JOIN (VALUES('seo:task:handle'),('seo:snippet:propose')) p(code) WHERE role_code IN ('R_ADMIN','R_AGENT') ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE seo_task_controls;
DROP TABLE seo_opportunity_cursors;
DELETE FROM admin_role_permissions WHERE permission_code='seo:snippet:propose';
DELETE FROM admin_permissions WHERE code='seo:snippet:propose';
