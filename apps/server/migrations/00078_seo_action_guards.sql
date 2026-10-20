-- +goose Up
ALTER TABLE seo_actions ADD COLUMN after_revision BIGINT;
CREATE TABLE seo_action_guards (
 action_id BIGINT PRIMARY KEY REFERENCES seo_actions(id),
 complete_through DATE NOT NULL,
 outcome TEXT NOT NULL CHECK(outcome IN ('stable','degraded','insufficient')),
 metrics JSONB NOT NULL,
 assessed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose Down
DROP TABLE seo_action_guards;
ALTER TABLE seo_actions DROP COLUMN after_revision;
