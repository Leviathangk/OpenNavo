-- +goose Up
-- Rebuild for databases that already applied the original 00080 definition.
-- A completed import without a scheduled retry is terminal history too.
-- Keep this expression and predicate aligned with seotasks.Service.Claim.
DROP INDEX seo_tasks_claim_age;
CREATE INDEX seo_tasks_claim_age ON seo_tasks
 ((priority-extract(epoch FROM (created_at AT TIME ZONE 'UTC'))/86400) DESC,id)
 WHERE state IN ('open','deferred','leased')
    OR (kind='hermes_import' AND state='done' AND next_retry_at IS NOT NULL);

-- +goose Down
-- Restore the original, broader 00080 index. Task/lease/action data is untouched.
DROP INDEX seo_tasks_claim_age;
CREATE INDEX seo_tasks_claim_age ON seo_tasks
 ((priority-extract(epoch FROM (created_at AT TIME ZONE 'UTC'))/86400) DESC,id)
 WHERE state IN ('open','deferred','leased') OR (kind='hermes_import' AND state='done');
