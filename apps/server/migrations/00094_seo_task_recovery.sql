-- +goose Up
-- Evidence that changes while an agent holds a task's lease waits in pending_evidence
-- and replaces the task evidence when the lease ends; opportunity delivery no longer
-- rolls back on a held task. reopen_count/reopened_at bound how often an agent may
-- reopen a quarantined or skipped task without a person.
ALTER TABLE seo_tasks
  ADD COLUMN pending_evidence JSONB,
  ADD COLUMN pending_evidence_hash TEXT,
  ADD COLUMN reopen_count INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN reopened_at TIMESTAMPTZ;

-- The catalog cursor started at 0, so the first runs replayed two weeks of catalog
-- history at 500 rows a run and never caught up. The tasks that replay created were
-- reviewed and skipped as a backlog; maintenance follows changes since the job first
-- ran. Resume after the last change older than the first maintenance task. A database
-- without maintenance tasks keeps its cursor.
UPDATE seo_opportunity_cursors c SET cursor=greatest(c.cursor,coalesce((
  SELECT max(seq) FROM catalog_changes WHERE created_at<(SELECT min(created_at) FROM seo_tasks WHERE kind='maintenance')),c.cursor)),updated_at=now()
WHERE source='catalog_changes';

-- +goose Down
ALTER TABLE seo_tasks
  DROP COLUMN reopened_at,
  DROP COLUMN reopen_count,
  DROP COLUMN pending_evidence_hash,
  DROP COLUMN pending_evidence;
