-- +goose Up
CREATE TABLE seo_entity_leases (
 entity_key TEXT PRIMARY KEY,
 task_id BIGINT REFERENCES seo_tasks(id),
 owner TEXT,
 fencing BIGINT NOT NULL DEFAULT 0,
 lease_until TIMESTAMPTZ
);
CREATE TABLE seo_actions (
 id BIGSERIAL PRIMARY KEY,
 task_id BIGINT NOT NULL REFERENCES seo_tasks(id),
 idempotency_key TEXT NOT NULL UNIQUE CHECK(length(idempotency_key) BETWEEN 8 AND 200),
 request_hash TEXT NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 owner TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('proposal','live')),
 operation TEXT NOT NULL,
 object TEXT NOT NULL,
 object_id TEXT NOT NULL,
 fields TEXT[] NOT NULL,
 before_data JSONB NOT NULL,
 after_data JSONB NOT NULL,
 base_revision BIGINT NOT NULL,
 evidence_ids BIGINT[] NOT NULL DEFAULT '{}',
 rule_version TEXT NOT NULL,
 playbook_version TEXT NOT NULL,
 experiment_id BIGINT,
 result JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 applied_at TIMESTAMPTZ,
 compensated_at TIMESTAMPTZ
);
CREATE INDEX seo_actions_task ON seo_actions(task_id,id);
CREATE INDEX seo_actions_operation ON seo_actions(operation,created_at DESC);

-- +goose Down
DROP TABLE seo_actions;
DROP TABLE seo_entity_leases;
