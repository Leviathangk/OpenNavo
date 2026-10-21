-- +goose Up
-- The first live runs of the relaxed article gate gave up on legacy byline
-- refreshes for reasons fixed since (reading source pages, 14 vs 14.0, key
-- combinations rendered with spaces). Give those refreshes one more try.
UPDATE seo_tasks SET state='open',next_retry_at=NULL,updated_at=now() WHERE kind='article_refresh' AND state IN ('skipped','quarantined');

-- +goose Down
SELECT 1;
