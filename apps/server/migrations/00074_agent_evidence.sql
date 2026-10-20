-- +goose Up
ALTER TABLE seo_settings ADD COLUMN automation JSONB NOT NULL DEFAULT '{"version":1,"actions":{"maintenance":{"enabled":true,"mode":"live"},"translation":{"enabled":true,"mode":"live"},"fact_fix":{"enabled":true,"mode":"live"},"withdraw":{"enabled":true,"mode":"live"},"compensate":{"enabled":true,"mode":"live"},"fact_fill":{"enabled":true,"mode":"proposal"},"content":{"enabled":true,"mode":"proposal"},"listing":{"enabled":true,"mode":"proposal"},"article":{"enabled":true,"mode":"proposal"},"snippet":{"enabled":true,"mode":"proposal"},"operations":{"enabled":true,"mode":"proposal"},"research":{"enabled":true,"mode":"live"}}}'::jsonb;
ALTER TABLE seo_settings ADD COLUMN automation_revision BIGINT NOT NULL DEFAULT 1;
CREATE TABLE seo_evidence_snapshots (
 id BIGSERIAL PRIMARY KEY,
 entity_key TEXT NOT NULL,
 source_url TEXT NOT NULL,
 final_url TEXT NOT NULL,
 body_text TEXT NOT NULL CHECK(octet_length(body_text)<=1048576),
 excerpt TEXT NOT NULL CHECK(length(excerpt) BETWEEN 1 AND 20000),
 body_hash TEXT NOT NULL CHECK(body_hash ~ '^[0-9a-f]{64}$'),
 fetched_at TIMESTAMPTZ NOT NULL,
 valid_until TIMESTAMPTZ NOT NULL,
 authority TEXT NOT NULL CHECK(authority IN ('official','reference')),
 fetch_status INTEGER NOT NULL CHECK(fetch_status=200),
 created_by TEXT NOT NULL,
 capture_key TEXT NOT NULL UNIQUE,
 request_hash TEXT NOT NULL,
 revoked_at TIMESTAMPTZ,
 UNIQUE(entity_key,source_url,body_hash,excerpt,fetched_at)
);
CREATE INDEX seo_evidence_entity ON seo_evidence_snapshots(entity_key,valid_until DESC);
CREATE TABLE seo_gate_events (
 id BIGSERIAL PRIMARY KEY,
 request_id TEXT NOT NULL,
 task_id BIGINT REFERENCES seo_tasks(id),
 owner TEXT NOT NULL,
 operation TEXT NOT NULL,
 rule_version TEXT NOT NULL,
 result TEXT NOT NULL CHECK(result IN ('rejected','proposal','live','preview')),
 reason TEXT NOT NULL,
 evidence_ids BIGINT[] NOT NULL DEFAULT '{}',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX seo_gate_events_request ON seo_gate_events(request_id,id);

-- +goose Down
DROP TABLE seo_gate_events;
DROP TABLE seo_evidence_snapshots;
ALTER TABLE seo_settings DROP COLUMN automation_revision;
ALTER TABLE seo_settings DROP COLUMN automation;
