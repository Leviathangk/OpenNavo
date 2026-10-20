-- +goose Up
CREATE TABLE seo_link_crawls(
 url_id BIGINT NOT NULL REFERENCES seo_urls(id) ON DELETE CASCADE,
 layer TEXT NOT NULL CHECK(layer IN ('origin','edge')),
 checked_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(url_id,layer)
);
CREATE TABLE seo_internal_links(
 from_url_id BIGINT NOT NULL REFERENCES seo_urls(id) ON DELETE CASCADE,
 target_url TEXT NOT NULL,
 layer TEXT NOT NULL CHECK(layer IN ('origin','edge')),
 PRIMARY KEY(from_url_id,layer,target_url)
);
CREATE INDEX seo_internal_links_target ON seo_internal_links(target_url,layer);
-- +goose Down
DROP TABLE seo_internal_links,seo_link_crawls;
