-- +goose Up
-- Translations were skipped because two validation rules rejected faithful text:
-- "desktop/web" read as the path literal "/web" (changed_literal), and 更快 for a
-- source that says "faster" (test_artifact_required). Both were fixed in 2177624.
-- Reopen those translate tasks, found by the skip reason or by the rejection the
-- gate recorded for the task, so the translation job tries them again.
UPDATE seo_tasks t SET state='open',next_retry_at=NULL,updated_at=now()
WHERE t.kind='translate' AND t.state='skipped' AND (
  t.result->>'reason' LIKE '%changed_literal%' OR t.result->>'reason' LIKE '%test_artifact_required%'
  OR EXISTS(SELECT 1 FROM seo_gate_events g WHERE g.task_id=t.id AND g.result='rejected'
    AND (g.reason LIKE '%changed_literal%' OR g.reason LIKE '%test_artifact_required%')));

-- +goose Down
SELECT 1;
