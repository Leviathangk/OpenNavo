-- +goose Up
CREATE TABLE seo_experiments (
 id BIGSERIAL PRIMARY KEY,
 variant TEXT NOT NULL CHECK(variant IN ('verified_free','current_version','uninstall_alternatives')),
 page_kind TEXT NOT NULL,
 locale TEXT NOT NULL CHECK(locale IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU','de-DE','fr-FR','it-IT','zh-Hant')),
 salt TEXT NOT NULL,
 baseline_ctr DOUBLE PRECISION NOT NULL CHECK(baseline_ctr>0 AND baseline_ctr<1),
 target_ctr DOUBLE PRECISION NOT NULL CHECK(target_ctr>baseline_ctr AND target_ctr<1),
 required_impressions BIGINT NOT NULL CHECK(required_impressions>0),
 state TEXT NOT NULL CHECK(state IN ('running','won','lost','inconclusive')),
 started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 ended_at TIMESTAMPTZ,
 cooldown_until TIMESTAMPTZ,
 template_version TEXT NOT NULL
);
CREATE UNIQUE INDEX seo_experiment_running ON seo_experiments(page_kind,locale) WHERE state='running';
CREATE TABLE seo_experiment_pages (
 experiment_id BIGINT NOT NULL REFERENCES seo_experiments(id),
 url_id BIGINT NOT NULL REFERENCES seo_urls(id),
 stratum TEXT NOT NULL,
 arm SMALLINT NOT NULL CHECK(arm IN (0,1)),
 baseline_impressions BIGINT NOT NULL CHECK(baseline_impressions>=0),
 recrawled_at TIMESTAMPTZ,
 active BOOLEAN NOT NULL DEFAULT true,
 PRIMARY KEY(experiment_id,url_id)
);
CREATE UNIQUE INDEX seo_experiment_page_active ON seo_experiment_pages(url_id) WHERE active;
CREATE TABLE seo_experiment_checkpoints (
 experiment_id BIGINT NOT NULL REFERENCES seo_experiments(id),
 checkpoint INTEGER NOT NULL CHECK(checkpoint IN (14,28,42)),
 complete_through DATE,
 metrics JSONB NOT NULL,
 outcome TEXT NOT NULL CHECK(outcome IN ('continue','won','lost','inconclusive')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(experiment_id,checkpoint)
);

-- +goose Down
DROP TABLE seo_experiment_checkpoints;
DROP TABLE seo_experiment_pages;
DROP TABLE seo_experiments;
