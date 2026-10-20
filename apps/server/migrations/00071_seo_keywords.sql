-- +goose Up
CREATE TABLE seo_keywords (
  id BIGSERIAL PRIMARY KEY, keyword TEXT NOT NULL, normalized TEXT NOT NULL,
  source TEXT NOT NULL CHECK(source IN ('openseo:dataforseo','gsc','bing','site_search','homebrew')),
  engine TEXT NOT NULL, country TEXT NOT NULL,
  language TEXT NOT NULL CHECK(language IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU','de-DE','fr-FR','it-IT','zh-Hant')),
  device TEXT NOT NULL, period TEXT NOT NULL,
  search_volume BIGINT CHECK(search_volume>=0),
  volume_state TEXT NOT NULL CHECK(volume_state IN ('known','unsupported','unknown')),
  difficulty REAL CHECK(difficulty BETWEEN 0 AND 100), our_position REAL CHECK(our_position>=0),
  serp_features TEXT[] NOT NULL DEFAULT '{}', vendor_task_id TEXT, cost_usd NUMERIC(10,4) CHECK(cost_usd>=0),
  anomaly TEXT, observed_at TIMESTAMPTZ NOT NULL,
  CHECK((volume_state='known')=(search_volume IS NOT NULL)),
  CHECK(NOT(source='openseo:dataforseo' AND (language='ru-RU' OR country IN ('RU','RUS'))) OR volume_state='unsupported'),
  UNIQUE(normalized,source,engine,country,language,device,period)
);
CREATE TABLE seo_keyword_links (
  keyword_id BIGINT NOT NULL REFERENCES seo_keywords(id) ON DELETE CASCADE,
  package_id BIGINT NOT NULL REFERENCES packages(id), intent TEXT NOT NULL,
  confidence REAL NOT NULL CHECK(confidence BETWEEN 0 AND 1),
  state TEXT NOT NULL CHECK(state IN ('candidate','confirmed','rejected')),
  PRIMARY KEY(keyword_id,package_id,intent)
);
CREATE TABLE seo_targets (
  entity_key TEXT NOT NULL CHECK(entity_key ~ '^(pkg:[1-9][0-9]*|topic:[a-z0-9][a-z0-9-]*)$'),
  intent TEXT NOT NULL CHECK(intent IN ('download','install','brew','uninstall','alternative','price','version','requirements','compare','topic')),
  locale TEXT NOT NULL CHECK(locale IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU','de-DE','fr-FR','it-IT','zh-Hant')),
  target_url TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(entity_key,intent,locale)
);
CREATE INDEX seo_keywords_language ON seo_keywords(language,observed_at DESC,id);
CREATE INDEX seo_keyword_entity ON seo_keyword_links(package_id,intent,state);
INSERT INTO admin_permissions(code,name,group_name) VALUES
 ('seo:insight:view','View SEO measurements and keyword ledger','seo'),
 ('seo:keyword:edit','Update SEO keyword ledger and targets','seo') ON CONFLICT DO NOTHING;
INSERT INTO admin_role_permissions(role_id,permission_code)
 SELECT r.id,p.code FROM admin_roles r CROSS JOIN admin_permissions p
 WHERE r.role_code IN ('R_ADMIN','R_AGENT') AND p.code IN ('seo:insight:view','seo:keyword:edit') ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM admin_role_permissions WHERE permission_code IN ('seo:insight:view','seo:keyword:edit');
DELETE FROM admin_permissions WHERE code IN ('seo:insight:view','seo:keyword:edit');
DROP TABLE seo_targets,seo_keyword_links,seo_keywords;
