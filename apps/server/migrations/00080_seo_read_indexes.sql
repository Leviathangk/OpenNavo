-- +goose Up
CREATE INDEX seo_urls_package ON seo_urls(package_id) WHERE package_id IS NOT NULL;
CREATE INDEX seo_gsc_page_url_day ON seo_gsc_page_daily(url_id,day);
CREATE INDEX seo_gsc_query_url_day ON seo_gsc_query_daily(url_id,day);
-- Subtracting the creation epoch has exactly the same ordering as adding age.
-- UTC makes the expression immutable. A completed import without a retry time
-- is terminal too; only recurring imports belong alongside the open tasks.
CREATE INDEX seo_tasks_claim_age ON seo_tasks
 ((priority-extract(epoch FROM (created_at AT TIME ZONE 'UTC'))/86400) DESC,id)
 WHERE state IN ('open','deferred','leased') OR (kind='hermes_import' AND state='done' AND next_retry_at IS NOT NULL);
CREATE INDEX seo_tasks_expiry ON seo_tasks(expires_at) WHERE expires_at IS NOT NULL AND state IN ('open','leased','deferred');
-- +goose Down
DROP INDEX seo_tasks_expiry;
DROP INDEX seo_tasks_claim_age;
DROP INDEX seo_gsc_query_url_day;
DROP INDEX seo_gsc_page_url_day;
DROP INDEX seo_urls_package;
