-- +goose Up
CREATE TABLE seo_tasks (
 id BIGSERIAL PRIMARY KEY,
 kind TEXT NOT NULL,
 entity_key TEXT NOT NULL,
 locale TEXT CHECK(locale IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU','de-DE','fr-FR','it-IT','zh-Hant')),
 dedupe_key TEXT NOT NULL UNIQUE,
 priority REAL NOT NULL DEFAULT 0,
 evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
 evidence_hash TEXT NOT NULL DEFAULT '',
 rule_version TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'open' CHECK(state IN ('open','leased','done','deferred','skipped','quarantined','expired')),
 lease_owner TEXT,
 lease_until TIMESTAMPTZ,
 fencing BIGINT NOT NULL DEFAULT 0,
 result JSONB,
 attempts INTEGER NOT NULL DEFAULT 0,
 next_retry_at TIMESTAMPTZ,
 last_attempt_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 expires_at TIMESTAMPTZ
);
CREATE INDEX seo_tasks_claim ON seo_tasks(state,next_retry_at,priority DESC,created_at);
CREATE INDEX seo_tasks_entity ON seo_tasks(entity_key);

-- Retain historical settings/logs for one release, but retire executable jobs and secrets.
UPDATE i18n_settings SET enabled=false,api_key_ciphertext='',base_url='',model='',updated_at=now();
DELETE FROM job_outbox WHERE job_type='translate:content';
DELETE FROM admin_role_permissions WHERE permission_code='i18n:settings';
DELETE FROM admin_permissions WHERE code='i18n:settings';

-- +goose Down
-- Credentials and old executable jobs are intentionally not restored.
DROP TABLE seo_tasks;
