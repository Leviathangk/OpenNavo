-- +goose Up
-- Progress of the URL change feed (WP4): the last catalog_changes row whose pages were refreshed and
-- announced, plus the outcome of the latest run for the admin SEO page. Starts at the current
-- position so the first run does not replay history.
CREATE TABLE url_notify_state (
  id                   BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
  cursor               BIGINT NOT NULL DEFAULT 0,
  last_run_at          TIMESTAMPTZ,
  last_urls            INTEGER NOT NULL DEFAULT 0,
  last_indexnow_status TEXT NOT NULL DEFAULT '',
  last_purge_status    TEXT NOT NULL DEFAULT '',
  last_error           TEXT NOT NULL DEFAULT ''
);
INSERT INTO url_notify_state(id,cursor) SELECT TRUE,COALESCE(max(seq),0) FROM catalog_changes;

-- +goose Down
DROP TABLE url_notify_state;
