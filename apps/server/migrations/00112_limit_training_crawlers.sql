-- +goose Up
-- Training crawlers that fetch the site at scale join the limited group (robots crawl delay, no feeds,
-- share images, query URLs or closed language prefixes; the web rate-limits the group). The admin's mode,
-- delay and other bots stay as set. Names compare case-insensitively, as the settings page does.
--
-- A database without a stored training list keeps none: the server reads absent settings from its
-- defaults (store.DefaultSEOSettings), which already name these bots, so writing them here would only
-- freeze today's defaults. The server refuses more than 20 bots, and the migration does not choose which
-- of the admin's to drop, so a list that would grow beyond 20 is left unchanged. The names match the
-- server's bot name pattern and none is a locked crawler.
--
-- Like an admin save of crawler rules, the change bumps revision and updated_at and clears the cached
-- site settings (c:site:*), which robots.txt reads per request; no page is refreshed.
-- +goose StatementBegin
DO $$
DECLARE
 bots jsonb;
 added jsonb := '[]'::jsonb;
 bot text;
BEGIN
 SELECT settings->'crawlers'->'training'->'bots' INTO bots FROM seo_settings WHERE id=true FOR UPDATE;
 IF bots IS NULL OR jsonb_typeof(bots)<>'array' THEN
  RETURN;
 END IF;
 FOREACH bot IN ARRAY ARRAY['GPTBot','Amazonbot','Bytespider','CCBot','Meta-ExternalAgent','anthropic-ai','MistralAI-Training'] LOOP
  IF NOT EXISTS(SELECT 1 FROM jsonb_array_elements_text(bots) b WHERE lower(b)=lower(bot)) THEN
   added := added||to_jsonb(bot);
  END IF;
 END LOOP;
 IF jsonb_array_length(added)=0 OR jsonb_array_length(bots)+jsonb_array_length(added)>20 THEN
  RETURN;
 END IF;
 UPDATE seo_settings SET settings=jsonb_set(settings,'{crawlers,training,bots}',bots||added),revision=revision+1,updated_at=now() WHERE id=true;
 INSERT INTO job_outbox(unique_key,job_type,payload) VALUES('cache:invalidate:seo-settings','cache:invalidate','["c:site:*"]')
 ON CONFLICT(unique_key) DO UPDATE SET payload=EXCLUDED.payload,available_at=now();
END $$;
-- +goose StatementEnd

-- +goose Down
-- Removes the six bots Up may have added besides GPTBot, which stays: production had it before, set by an admin.
-- +goose StatementBegin
DO $$
DECLARE
 bots jsonb;
 kept jsonb;
BEGIN
 SELECT settings->'crawlers'->'training'->'bots' INTO bots FROM seo_settings WHERE id=true FOR UPDATE;
 IF bots IS NULL OR jsonb_typeof(bots)<>'array' THEN
  RETURN;
 END IF;
 SELECT COALESCE(jsonb_agg(to_jsonb(b) ORDER BY n),'[]'::jsonb) INTO kept FROM jsonb_array_elements_text(bots) WITH ORDINALITY e(b,n)
 WHERE lower(b) NOT IN ('amazonbot','bytespider','ccbot','meta-externalagent','anthropic-ai','mistralai-training');
 IF kept=bots THEN
  RETURN;
 END IF;
 UPDATE seo_settings SET settings=jsonb_set(settings,'{crawlers,training,bots}',kept),revision=revision+1,updated_at=now() WHERE id=true;
 INSERT INTO job_outbox(unique_key,job_type,payload) VALUES('cache:invalidate:seo-settings','cache:invalidate','["c:site:*"]')
 ON CONFLICT(unique_key) DO UPDATE SET payload=EXCLUDED.payload,available_at=now();
END $$;
-- +goose StatementEnd
