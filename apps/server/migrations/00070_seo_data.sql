-- +goose Up
CREATE TABLE seo_urls (
  id BIGSERIAL PRIMARY KEY, url TEXT NOT NULL UNIQUE,
  page_kind TEXT NOT NULL, locale TEXT NOT NULL CHECK (locale IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU','de-DE','fr-FR','it-IT','zh-Hant')),
  package_id BIGINT REFERENCES packages(id),
  expected JSONB NOT NULL DEFAULT '{}',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE seo_gsc_page_daily (
  day DATE NOT NULL, url_id BIGINT NOT NULL REFERENCES seo_urls(id), country TEXT NOT NULL, device TEXT NOT NULL,
  clicks BIGINT NOT NULL CHECK(clicks>=0), impressions BIGINT NOT NULL CHECK(impressions>=clicks),
  position_sum DOUBLE PRECISION NOT NULL CHECK(position_sum>=0),
  PRIMARY KEY(day,url_id,country,device)
);
CREATE TABLE seo_gsc_query_daily (
  day DATE NOT NULL, url_id BIGINT NOT NULL REFERENCES seo_urls(id), query TEXT NOT NULL,
  clicks BIGINT NOT NULL CHECK(clicks>=0), impressions BIGINT NOT NULL CHECK(impressions>=clicks),
  position_sum DOUBLE PRECISION NOT NULL CHECK(position_sum>=0), anomaly TEXT,
  PRIMARY KEY(day,url_id,query)
);
CREATE TABLE seo_data_watermarks (
  source TEXT PRIMARY KEY CHECK(source IN ('gsc_page','gsc_query','inspection','bing','audit','edge_probe')),
  complete_through DATE, fresh_through DATE, updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE seo_ingest_days (
  source TEXT NOT NULL REFERENCES seo_data_watermarks(source), day DATE NOT NULL,
  temporal_complete BOOLEAN NOT NULL, retrieval_complete BOOLEAN NOT NULL,
  first_incomplete_date DATE, row_count INT NOT NULL CHECK(row_count>=0),
  coverage TEXT NOT NULL, fetched_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY(source,day)
);
-- Durable watermark events are consumed by the opportunity engine, independently of the ingestion clock.
CREATE TABLE seo_watermark_events (
  id BIGSERIAL PRIMARY KEY, source TEXT NOT NULL, complete_through DATE NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), consumed_at TIMESTAMPTZ,
  UNIQUE(source,complete_through)
);
CREATE TABLE seo_url_status (
  url_id BIGINT PRIMARY KEY REFERENCES seo_urls(id), coverage_state TEXT, indexing_state TEXT,
  google_canonical TEXT, last_crawl_at TIMESTAMPTZ, checked_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE seo_inspection_quota (
  day DATE PRIMARY KEY, used INT NOT NULL CHECK(used BETWEEN 0 AND 1500)
);
CREATE TABLE seo_audit_issues (
  id BIGSERIAL PRIMARY KEY, rule TEXT NOT NULL, url_id BIGINT NOT NULL REFERENCES seo_urls(id),
  layer TEXT NOT NULL CHECK(layer IN ('origin','edge')), severity TEXT NOT NULL CHECK(severity IN ('p0','p1','p2')),
  detail TEXT NOT NULL DEFAULT '', first_seen TIMESTAMPTZ NOT NULL, last_seen TIMESTAMPTZ NOT NULL, resolved_at TIMESTAMPTZ,
  UNIQUE(rule,url_id,layer)
);
CREATE TABLE seo_bing_daily (
  day DATE NOT NULL, dimension TEXT NOT NULL CHECK(dimension IN ('page','query')), value TEXT NOT NULL,
  clicks BIGINT NOT NULL CHECK(clicks>=0), impressions BIGINT NOT NULL CHECK(impressions>=clicks),
  position_sum DOUBLE PRECISION NOT NULL CHECK(position_sum>=0),
  PRIMARY KEY(day,dimension,value)
);
CREATE TABLE seo_bing_crawl_daily (day DATE PRIMARY KEY, stats JSONB NOT NULL);
CREATE TABLE seo_provider_settings (
  source TEXT PRIMARY KEY CHECK(source IN ('gsc','bing','audit')), enabled BOOLEAN NOT NULL DEFAULT false,
  credentials_ciphertext TEXT NOT NULL DEFAULT '', updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO seo_data_watermarks(source) VALUES ('gsc_page'),('gsc_query'),('inspection'),('bing'),('audit'),('edge_probe');
INSERT INTO seo_provider_settings(source) VALUES ('gsc'),('bing'),('audit');
CREATE INDEX seo_audit_open ON seo_audit_issues(url_id,layer) WHERE resolved_at IS NULL;
CREATE INDEX seo_watermark_pending ON seo_watermark_events(id) WHERE consumed_at IS NULL;

-- +goose Down
DROP TABLE seo_provider_settings,seo_bing_crawl_daily,seo_bing_daily,seo_audit_issues,seo_inspection_quota,seo_url_status,
  seo_watermark_events,seo_ingest_days,seo_data_watermarks,seo_gsc_query_daily,seo_gsc_page_daily,seo_urls;
