-- +goose Up
-- Intent pages (alternatives, uninstall) open by an editorial switch per app, kind and language. A row is
-- an open switch; pages.intentPagesEnabled and the content gates still decide whether the page is indexed.
-- zh-Hant is derived from zh-CN and follows its switch, so it has none of its own. created_by and
-- updated_by hold the write owner: admin:<user>, agent:<client>:<token> or system.
-- Neither table has a foreign key to packages in this release: the previous release's workers refuse a
-- link-to-cask merge when any foreign key to packages is missing from their own list, and they still run
-- during the rollout or after a rollback. Writes check that the app exists, link merges move the rows,
-- and the daily cleanup deletes rows whose app is gone; the next release adds the keys. The primary keys
-- lead with package_id, so lookups by app stay indexed.
CREATE TABLE package_intent_pages (
 package_id BIGINT NOT NULL,
 page_kind TEXT NOT NULL CHECK(page_kind IN ('alternatives','uninstall')),
 locale TEXT NOT NULL CHECK(locale IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU','de-DE','fr-FR','it-IT')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 created_by TEXT NOT NULL DEFAULT 'system' CHECK(created_by ~ '^(admin:[0-9]+|agent:[0-9]+:[0-9]+|system)$'),
 PRIMARY KEY(package_id,page_kind,locale)
);
CREATE INDEX package_intent_pages_listing ON package_intent_pages(page_kind,locale,package_id);

-- Optional search title and description of an app's overview page in one language; the templates stay the
-- default. Values are single-line plain text. zh-Hant has none: its pages convert zh-CN's override.
CREATE TABLE package_seo_overrides (
 package_id BIGINT NOT NULL,
 locale TEXT NOT NULL CHECK(locale IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU','de-DE','fr-FR','it-IT')),
 title TEXT CHECK(title=btrim(title) AND char_length(title) BETWEEN 1 AND 70 AND title !~ '[[:cntrl:]<>]'),
 description TEXT CHECK(description=btrim(description) AND char_length(description) BETWEEN 1 AND 200 AND description !~ '[[:cntrl:]<>]'),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_by TEXT NOT NULL DEFAULT 'system' CHECK(updated_by ~ '^(admin:[0-9]+|agent:[0-9]+:[0-9]+|system)$'),
 PRIMARY KEY(package_id,locale),
 CHECK(title IS NOT NULL OR description IS NOT NULL)
);

-- Open the switches that confirmed demand opened before, by the rule that admitted pages until now: a
-- seo_targets reservation at the page's own address, joined to a confirmed keyword with known volume or a
-- recent own ranking. The address is the site origin (scheme and host of WEB_BASE_URL, which the migrate
-- command has from its configuration), the language prefix, /apps/<current token> and the page, with no query
-- or fragment, so targets of renamed apps, of another language or of another host stay closed. Without
-- WEB_BASE_URL nothing opens. Tokens need no escaping in a path segment. Uninstall pages exist for casks only;
-- zh-Hant targets are dropped because zh-Hant follows zh-CN.
-- +goose ENVSUB ON
INSERT INTO package_intent_pages(package_id,page_kind,locale,created_at)
SELECT p.id,CASE t.intent WHEN 'alternative' THEN 'alternatives' ELSE 'uninstall' END,t.locale,t.created_at
FROM seo_targets t JOIN packages p ON t.entity_key='pkg:'||p.id::text
WHERE p.kind IN ('cask','link') AND t.intent IN ('alternative','uninstall') AND t.locale<>'zh-Hant'
 AND (t.intent='alternative' OR p.kind='cask')
 AND t.target_url=substring('${WEB_BASE_URL:-}' from '^[a-z]+://[^/?#]+')
  ||CASE t.locale WHEN 'en-US' THEN '' WHEN 'zh-CN' THEN '/zh' WHEN 'ja-JP' THEN '/ja' WHEN 'es-ES' THEN '/es' WHEN 'pt-BR' THEN '/pt'
   WHEN 'ru-RU' THEN '/ru' WHEN 'de-DE' THEN '/de' WHEN 'fr-FR' THEN '/fr' WHEN 'it-IT' THEN '/it' END
  ||'/apps/'||p.token||'/'||CASE t.intent WHEN 'alternative' THEN 'alternatives' ELSE 'uninstall' END
 AND EXISTS(
 SELECT 1 FROM seo_keyword_links l JOIN seo_keywords k ON k.id=l.keyword_id WHERE l.package_id=p.id AND l.intent=t.intent AND l.state='confirmed' AND k.language=t.locale
 AND ((k.volume_state='known' AND k.search_volume>0) OR (k.source IN ('gsc','bing') AND k.anomaly IS NULL AND k.our_position IS NOT NULL AND k.observed_at>=clock_timestamp()-interval '60 days' AND k.observed_at<=clock_timestamp())))
ON CONFLICT DO NOTHING;
-- +goose ENVSUB OFF

-- Switches and titles queue the app's page state like the other app tables (00052). The revision triggers
-- (intent:<id>, pkgseo:<id>) come with entity_revisions in 00110.
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON package_intent_pages FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();
CREATE TRIGGER seo_page_dirty AFTER INSERT OR UPDATE OR DELETE ON package_seo_overrides FOR EACH ROW EXECUTE FUNCTION queue_package_page_dirty();

-- +goose Down
-- seo_targets and the keyword ledger still hold the demand the switches were opened from.
DELETE FROM seo_entity_revisions WHERE entity_key LIKE 'intent:%' OR entity_key LIKE 'pkgseo:%';
DROP TABLE package_seo_overrides;
DROP TABLE package_intent_pages;
