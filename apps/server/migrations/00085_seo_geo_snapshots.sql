-- +goose Up
CREATE TABLE seo_geo_snapshots (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 site text NOT NULL CHECK (length(site) BETWEEN 1 AND 253),
 provider text NOT NULL CHECK (provider='openseo'),
 source_project text NOT NULL CHECK (length(source_project) BETWEEN 1 AND 200),
 provider_run_key text NOT NULL CHECK (length(provider_run_key) BETWEEN 1 AND 200),
 mode text NOT NULL CHECK (mode IN ('summary_only','imported_observation')),
 panel_id text NOT NULL,
 panel_version text NOT NULL,
 period_start timestamptz NOT NULL,
 period_end timestamptz NOT NULL CHECK (period_end>=period_start),
 imported_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 imported_by text NOT NULL,
 payload_hash text NOT NULL CHECK (payload_hash ~ '^[a-f0-9]{64}$'),
 comparison_key text NOT NULL CHECK (comparison_key ~ '^[a-f0-9]{64}$'),
 supersedes bigint UNIQUE REFERENCES seo_geo_snapshots(id),
 snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=600000),
 counts jsonb NOT NULL CHECK (jsonb_typeof(counts)='object'),
 UNIQUE(site,provider,source_project,provider_run_key,payload_hash)
);
CREATE INDEX seo_geo_recent ON seo_geo_snapshots(site,period_end DESC,id DESC);
CREATE INDEX seo_geo_comparable ON seo_geo_snapshots(site,comparison_key,period_end DESC,id DESC) WHERE mode='imported_observation';
-- No paid calls, content changes or cache invalidation triggers belong to this ledger.
-- +goose Down
DROP TABLE seo_geo_snapshots;
