-- +goose Up
-- Immutable control/import receipts are audit documents, not live package relations.
CREATE TABLE seo_control_receipts (
 id BIGSERIAL PRIMARY KEY,
 owner TEXT NOT NULL,
 operation TEXT NOT NULL,
 idempotency_key TEXT NOT NULL CHECK(length(idempotency_key) BETWEEN 8 AND 200),
 request_hash TEXT NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 before_data JSONB NOT NULL,
 after_data JSONB NOT NULL,
 result JSONB NOT NULL,
 reason TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(owner,operation,idempotency_key)
);
CREATE TABLE seo_import_cursors (
 owner TEXT NOT NULL,
 profile TEXT NOT NULL,
 cursor JSONB NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(owner,profile)
);
INSERT INTO admin_permissions(code,name,group_name) VALUES ('seo:automation:tune','Tune SEO automation','seo'),('seo:task:handle','Handle SEO tasks','seo') ON CONFLICT(code) DO NOTHING;
INSERT INTO admin_role_permissions(role_id,permission_code)
 SELECT id,p.code FROM admin_roles CROSS JOIN (VALUES('seo:automation:tune'),('seo:task:handle')) p(code)
 WHERE role_code IN ('R_ADMIN','R_AGENT') ON CONFLICT DO NOTHING;

-- Preserve H5's execution whitelist; add the import tool permission and isolated tuner set.
CREATE TABLE seo_token_policy_backup (id BOOLEAN PRIMARY KEY DEFAULT true CHECK(id), expression TEXT NOT NULL);
INSERT INTO seo_token_policy_backup(expression) SELECT pg_get_expr(conbin,conrelid) FROM pg_constraint WHERE conrelid='agent_tokens'::regclass AND conname='agent_tokens_permissions_check';
-- +goose StatementBegin
DO $$
DECLARE prior TEXT;
BEGIN
 SELECT expression INTO STRICT prior FROM seo_token_policy_backup;
 ALTER TABLE agent_tokens DROP CONSTRAINT agent_tokens_permissions_check;
 EXECUTE 'ALTER TABLE agent_tokens ADD CONSTRAINT agent_tokens_permissions_check CHECK ((
''seo:automation:tune''=ANY(permissions) AND permissions <@ ARRAY[''seo:automation:tune'',''seo:insight:view'',''agent:log:view'',''dashboard:view'',''catalog:package:view'',''ops:job:view'',''ops:search:view'']::text[] AND cardinality(permissions)<=7) OR (NOT(''seo:automation:tune''=ANY(permissions)) AND ('||replace(prior,'permissions','array_remove(permissions,''seo:task:handle'')')||')))';
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 LOCK TABLE seo_control_receipts,seo_import_cursors IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM seo_control_receipts) OR EXISTS(SELECT 1 FROM seo_import_cursors) THEN
  RAISE EXCEPTION 'Archive and explicitly resolve control receipts and import cursors before Down 00090';
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
DECLARE prior TEXT;
BEGIN
 SELECT expression INTO STRICT prior FROM seo_token_policy_backup;
 ALTER TABLE agent_tokens DROP CONSTRAINT agent_tokens_permissions_check;
 -- Remove the import permission only when it was absent before this migration.
 IF position('seo:task:handle' IN prior)=0 THEN
  UPDATE agent_tokens SET permissions=array_remove(permissions,'seo:task:handle');
 END IF;
 -- Revoke tuner credentials on rollback; do not turn them into executors.
 UPDATE agent_tokens SET permissions='{}',revoked_at=coalesce(revoked_at,now()) WHERE 'seo:automation:tune'=ANY(permissions);
 EXECUTE 'ALTER TABLE agent_tokens ADD CONSTRAINT agent_tokens_permissions_check CHECK ('||prior||')';
END $$;
-- +goose StatementEnd
DROP TABLE seo_token_policy_backup;
DROP TABLE seo_import_cursors;
DROP TABLE seo_control_receipts;
DELETE FROM admin_role_permissions WHERE permission_code='seo:automation:tune';
DELETE FROM admin_permissions WHERE code='seo:automation:tune';
