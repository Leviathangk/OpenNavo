-- +goose Up
UPDATE seo_settings SET automation=automation||'{"dailyEntities":{"listing":20,"article":5,"snippet":2,"maintenance":500,"translation":1000,"content":100,"fact_fill":500,"fact_fix":500,"withdraw":500,"compensate":1000,"operations":100,"research":1000},"researchDailyMicros":15000000,"researchMonthlyMicros":100000000,"businessDropRatio":0.5}'::jsonb;
CREATE TABLE seo_daily_entities (
 site TEXT NOT NULL,
 day DATE NOT NULL,
 action TEXT NOT NULL,
 entity_key TEXT NOT NULL,
 first_request_id TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(site,day,action,entity_key)
);
CREATE TABLE seo_cost_receipts (
 id BIGSERIAL PRIMARY KEY,
 site TEXT NOT NULL,
 provider TEXT NOT NULL CHECK(provider='openseo'),
 vendor_task_id TEXT NOT NULL CHECK(length(vendor_task_id) BETWEEN 1 AND 200),
 cost_micros BIGINT NOT NULL CHECK(cost_micros>=0),
 incurred_at TIMESTAMPTZ NOT NULL,
 recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(provider,vendor_task_id)
);
CREATE INDEX seo_cost_site_date ON seo_cost_receipts(site,incurred_at);
CREATE TABLE seo_breakers (
 site TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('technical','business','budget')),
 scope TEXT NOT NULL,
 reason TEXT NOT NULL,
 evidence JSONB NOT NULL,
 opened_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 retry_at TIMESTAMPTZ,
 closed_at TIMESTAMPTZ,
 PRIMARY KEY(site,kind,scope)
);

-- +goose Down
DROP TABLE seo_breakers;
DROP TABLE seo_cost_receipts;
DROP TABLE seo_daily_entities;
UPDATE seo_settings SET automation=automation-ARRAY['dailyEntities','researchDailyMicros','researchMonthlyMicros','businessDropRatio'];
