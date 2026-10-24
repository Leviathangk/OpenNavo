-- +goose Up
-- Retain the first pre-conversion row and latest generated row for conditional rollback.
-- Down retains receipts, so repeated upgrades must reuse the table.
CREATE TABLE IF NOT EXISTS derived_traditional (
 entity TEXT NOT NULL,
 object_key TEXT NOT NULL,
 simplified_hash TEXT NOT NULL,
 converter TEXT NOT NULL,
 before_data JSONB,
 after_data JSONB NOT NULL,
 restored_at TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(entity,object_key)
);
-- Fence existing leases before retiring independent Traditional Chinese work.
UPDATE seo_tasks SET state='skipped',fencing=fencing+1,lease_owner=NULL,lease_until=NULL,
 next_retry_at=NULL,result='{"reason":"derived_from_zh_CN"}'::jsonb,updated_at=now()
 WHERE kind='translate' AND locale='zh-Hant' AND state IN ('open','deferred','leased');
DELETE FROM seo_entity_leases WHERE task_id IN (SELECT id FROM seo_tasks WHERE kind='translate' AND locale='zh-Hant');

-- +goose Down
-- Keep conversion receipts for an explicit conditional rollback; never discard backups.
-- Old translation work is not automatically resurrected.
SELECT 1;
