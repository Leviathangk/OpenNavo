-- +goose Up
-- SEO automation leaves the server (docs/design/seo-removal). Its write layer stays for ordinary
-- Agent and admin writes under neutral names. The engine's tables move to seo_archive with their
-- data. For one release the previous code keeps working against this schema, so a deploy can run
-- old and new replicas side by side and roll back by image: every moved or renamed table keeps a
-- same-name view in public, the write log keeps task_id, seo_settings keeps its automation columns
-- and agent_tokens accepts both permission sets. New code uses none of these; the next release
-- drops them (docs/design/seo-removal/be-a.md, 「下一版清理」). Down needs the archive, so DROP
-- SCHEMA seo_archive CASCADE only once this migration will not be rolled back. Provider
-- credentials are erased, not archived. 00109 must run first: its backfill reads seo_targets and
-- the keyword ledger.
--
-- Statements that lock tables the site reads run last, so those locks are held only until the
-- commit right after them. Requests queue behind a lock the migration waits for, so it waits at
-- most 5 s and then fails; nothing has changed then, and the deploy can simply be retried.
SET LOCAL lock_timeout = '5s';
CREATE SCHEMA seo_archive;

-- What moved, so Down moves back exactly these. Tables a database never created are skipped.
CREATE TABLE seo_archive.retired_tables(name TEXT PRIMARY KEY);
INSERT INTO seo_archive.retired_tables
SELECT name FROM (VALUES ('seo_tasks'),('seo_entity_leases'),('seo_actions'),('seo_action_guards'),
 ('seo_experiments'),('seo_experiment_pages'),('seo_experiment_checkpoints'),('seo_geo_snapshots'),
 ('seo_gsc_page_daily'),('seo_gsc_query_daily'),('seo_bing_daily'),('seo_bing_crawl_daily'),('seo_audit_issues'),
 ('seo_urls'),('seo_url_status'),('seo_internal_links'),('seo_link_crawls'),('seo_inspection_quota'),
 ('seo_data_watermarks'),('seo_watermark_events'),('seo_ingest_days'),
 ('seo_breakers'),('seo_cost_receipts'),('seo_control_receipts'),('seo_daily_entities'),
 ('seo_opportunity_cursors'),('seo_import_cursors'),('seo_keywords'),('seo_keyword_links'),('seo_targets'),
 ('seo_listing_candidates'),('seo_listing_baselines'),('seo_translation_corrections'),
 ('seo_execution_policy_backup'),('seo_token_policy_backup'),('seo_provider_settings')) v(name)
WHERE to_regclass('public.'||name) IS NOT NULL;

-- Permissions: SEO reads and task handling become the index read view; the other SEO grants go.
-- Rewritten rows keep their before/after values so Down can restore rows nobody changed since.
CREATE TABLE seo_archive.retired_permission_rows(tbl TEXT NOT NULL, id TEXT NOT NULL, before TEXT[] NOT NULL, after TEXT[] NOT NULL, PRIMARY KEY(tbl,id));

-- Revisions recorded by SEO endpoints must stay restorable; restoring always requires
-- content:revision:restore and the caller's current access to the object. This reads the whole
-- table, so it runs first, before the migration holds any lock that blocks requests.
INSERT INTO seo_archive.retired_permission_rows
SELECT 'content_revisions',id::text,required_permissions,coalesce(nullif(ARRAY(SELECT p FROM unnest(required_permissions) WITH ORDINALITY u(p,ord)
 WHERE p NOT IN ('seo:insight:view','seo:task:handle','seo:snippet:propose','seo:automation:tune','seo:keyword:edit') ORDER BY ord),'{}'),ARRAY['content:revision:restore'])
FROM content_revisions WHERE required_permissions && ARRAY['seo:insight:view','seo:task:handle','seo:snippet:propose','seo:automation:tune','seo:keyword:edit'];
UPDATE content_revisions c SET required_permissions=r.after FROM seo_archive.retired_permission_rows r WHERE r.tbl='content_revisions' AND r.id=c.id::text;

-- Shared write layer: idempotency ledger, write log, entity revisions and article evidence.
ALTER TABLE seo_task_controls RENAME TO agent_write_controls;
ALTER TABLE seo_gate_events RENAME TO agent_write_events;
ALTER TABLE seo_entity_revisions RENAME TO entity_revisions;
ALTER TABLE seo_evidence_snapshots RENAME TO evidence_snapshots;
ALTER INDEX seo_gate_events_request RENAME TO agent_write_events_request;
ALTER INDEX seo_evidence_entity RENAME TO evidence_snapshots_entity;
-- Constraints (with their indexes) and serial sequences follow the table names.
-- +goose StatementBegin
DO $$
DECLARE t record; o record;
BEGIN
 FOR t IN SELECT * FROM (VALUES ('agent_write_controls','seo_task_controls'),('agent_write_events','seo_gate_events'),
  ('entity_revisions','seo_entity_revisions'),('evidence_snapshots','seo_evidence_snapshots')) v(tbl,old) LOOP
  FOR o IN SELECT conname FROM pg_constraint WHERE conrelid=('public.'||t.tbl)::regclass AND starts_with(conname,t.old||'_') LOOP
   EXECUTE format('ALTER TABLE public.%I RENAME CONSTRAINT %I TO %I',t.tbl,o.conname,t.tbl||substr(o.conname,length(t.old)+1));
  END LOOP;
  FOR o IN SELECT s.relname FROM pg_depend d JOIN pg_class s ON s.oid=d.objid
   WHERE d.classid='pg_class'::regclass AND d.refobjid=('public.'||t.tbl)::regclass AND s.relkind='S' AND starts_with(s.relname,t.old||'_') LOOP
   EXECUTE format('ALTER SEQUENCE public.%I RENAME TO %I',o.relname,t.tbl||substr(o.relname,length(t.old)+1));
  END LOOP;
 END LOOP;
END $$;
-- +goose StatementEnd
-- Expired idempotency records are deleted daily by the cleanup job.
CREATE INDEX agent_write_controls_created ON agent_write_controls(created_at);
-- The previous release writes these under their old names, task_id included; the key from
-- task_id into the archived task queue goes with the other cross-archive keys below.
CREATE VIEW public.seo_task_controls AS SELECT * FROM public.agent_write_controls;
CREATE VIEW public.seo_gate_events AS SELECT * FROM public.agent_write_events;
CREATE VIEW public.seo_entity_revisions AS SELECT * FROM public.entity_revisions;
CREATE VIEW public.seo_evidence_snapshots AS SELECT * FROM public.evidence_snapshots;

-- Revisions of objects without content history. A changed key bumps both entities; a table this
-- function does not know raises instead of writing a wrong key. The archived keyword ledger keeps
-- its triggers for the previous release, which still edits it through the views below.
ALTER FUNCTION seo_bump_entity_revision() RENAME TO bump_entity_revision;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bump_entity_revision() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE data JSONB; entity TEXT; keys TEXT[]:='{}';
BEGIN
 FOREACH data IN ARRAY ARRAY[CASE WHEN TG_OP<>'INSERT' THEN to_jsonb(OLD) END,CASE WHEN TG_OP<>'DELETE' THEN to_jsonb(NEW) END] LOOP
  CONTINUE WHEN data IS NULL;
  CASE TG_TABLE_NAME
  WHEN 'background_item_content' THEN entity:='background:'||(data->>'label');
  WHEN 'update_feeds' THEN entity:='update_feed:'||(data->>'source')||':'||(data->>'bundle_id');
  WHEN 'package_intent_pages' THEN entity:='intent:'||(data->>'package_id');
  WHEN 'package_seo_overrides' THEN entity:='pkgseo:'||(data->>'package_id');
  WHEN 'seo_keywords','seo_targets' THEN entity:='site:keywords';
  END CASE;
  IF NOT entity=ANY(keys) THEN keys:=keys||entity; END IF;
 END LOOP;
 INSERT INTO entity_revisions(entity_key,revision) SELECT k,1 FROM unnest(keys) k
 ON CONFLICT(entity_key) DO UPDATE SET revision=entity_revisions.revision+1;
 RETURN NULL;
END $$;
-- +goose StatementEnd

-- Listing guards read seo_listing_candidates; 00098 already detached them, Down of 00098 reattaches.
ALTER FUNCTION protect_automatic_listing_download() SET SCHEMA seo_archive;
ALTER FUNCTION protect_automatic_listing_package() SET SCHEMA seo_archive;

-- The previous release reads provider settings and skips a provider that is disabled or has no
-- credentials, so erased rows read as unconfigured.
UPDATE seo_provider_settings SET enabled=false,credentials_ciphertext='',updated_at=now();

-- Each archived table keeps a same-name view in public for the previous release. Simple views
-- are updatable: inserts, upserts, row locks and serial defaults reach the archived table.
-- +goose StatementBegin
DO $$
DECLARE t record;
BEGIN
 FOR t IN SELECT name FROM seo_archive.retired_tables ORDER BY name LOOP
  EXECUTE format('ALTER TABLE public.%I SET SCHEMA seo_archive',t.name);
  EXECUTE format('CREATE VIEW public.%I AS SELECT * FROM seo_archive.%I',t.name,t.name);
 END LOOP;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION retire_seo_permissions(granted TEXT[]) RETURNS TEXT[] LANGUAGE sql IMMUTABLE AS $$
 SELECT coalesce(array_agg(code ORDER BY ord),'{}') FROM (
  SELECT code,min(ord) ord FROM (
   SELECT CASE WHEN g IN ('seo:insight:view','seo:task:handle') THEN 'site:index:view' ELSE g END code,ord
   FROM unnest(granted) WITH ORDINALITY u(g,ord)
   WHERE g NOT IN ('seo:snippet:propose','seo:automation:tune','seo:keyword:edit')) mapped
  GROUP BY code) kept
$$;
-- +goose StatementEnd

-- Tokens are rewritten. The check accepts the old codes as well for one release, because the
-- previous admin still creates tokens with them; new code never grants them.
CREATE TABLE seo_archive.agent_token_policy(expression TEXT NOT NULL, defaults TEXT NOT NULL);
INSERT INTO seo_archive.agent_token_policy
SELECT pg_get_expr(c.conbin,c.conrelid),pg_get_expr(d.adbin,d.adrelid)
FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attname='permissions'
JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
WHERE c.conrelid='agent_tokens'::regclass AND c.conname='agent_tokens_permissions_check';
ALTER TABLE agent_tokens DROP CONSTRAINT agent_tokens_permissions_check;
INSERT INTO seo_archive.retired_permission_rows
SELECT 'agent_tokens',id::text,permissions,retire_seo_permissions(permissions) FROM agent_tokens
WHERE permissions && ARRAY['seo:insight:view','seo:task:handle','seo:snippet:propose','seo:automation:tune','seo:keyword:edit'];
UPDATE agent_tokens t SET permissions=r.after FROM seo_archive.retired_permission_rows r WHERE r.tbl='agent_tokens' AND r.id=t.id::text;
ALTER TABLE agent_tokens ALTER COLUMN permissions SET DEFAULT ARRAY['site:index:view','dashboard:view','catalog:package:view','catalog:package:edit','catalog:package:resync','catalog:asset:upload','catalog:category:edit','content:collection:edit','content:collection:publish','content:article:edit','content:article:publish','content:feature:edit','content:glossary:edit','changelog:release:edit','translation:review','ops:job:view','ops:job:trigger','ops:search:view','ops:search:edit','ops:feedback:handle','agent:log:view','content:revision:restore','content:announcement:edit','release:desktop:notes']::text[];
ALTER TABLE agent_tokens ADD CONSTRAINT agent_tokens_permissions_check CHECK(permissions <@ ARRAY['site:index:view','dashboard:view','catalog:package:view','catalog:package:edit','catalog:package:resync','catalog:asset:upload','catalog:category:edit','content:collection:edit','content:collection:publish','content:article:edit','content:article:publish','content:feature:edit','content:glossary:edit','changelog:release:edit','translation:review','ops:job:view','ops:job:trigger','ops:search:view','ops:search:edit','ops:feedback:handle','agent:log:view','content:revision:restore','content:announcement:edit','release:desktop:notes','seo:insight:view','seo:task:handle','seo:snippet:propose','seo:automation:tune','seo:keyword:edit']::text[] AND cardinality(permissions)<=29);

-- Grants are rewritten: access tokens are filtered by their grant's scopes. Registered clients
-- keep theirs, which the previous release still authorizes and new code maps when it reads them.
INSERT INTO seo_archive.retired_permission_rows
SELECT 'mcp_oauth_grants',id::text,string_to_array(scopes,' '),retire_seo_permissions(string_to_array(scopes,' ')) FROM mcp_oauth_grants
WHERE string_to_array(scopes,' ') && ARRAY['seo:insight:view','seo:task:handle','seo:snippet:propose','seo:automation:tune','seo:keyword:edit'];
UPDATE mcp_oauth_grants g SET scopes=array_to_string(r.after,' ') FROM seo_archive.retired_permission_rows r WHERE r.tbl='mcp_oauth_grants' AND r.id=g.id::text;
DROP FUNCTION retire_seo_permissions(TEXT[]);

-- Roles keep the index read view where they could read SEO decisions or handle SEO tasks.
CREATE TABLE seo_archive.retired_admin_permissions AS SELECT * FROM admin_permissions
WHERE code IN ('seo:insight:view','seo:task:handle','seo:snippet:propose','seo:automation:tune','seo:keyword:edit');
CREATE TABLE seo_archive.retired_role_permissions AS SELECT * FROM admin_role_permissions
WHERE permission_code IN ('seo:insight:view','seo:task:handle','seo:snippet:propose','seo:automation:tune','seo:keyword:edit');
INSERT INTO admin_permissions(code,name,group_name) VALUES('site:index:view','View page indexing decisions and site index settings','site') ON CONFLICT(code) DO NOTHING;
INSERT INTO admin_role_permissions(role_id,permission_code)
SELECT DISTINCT role_id,'site:index:view' FROM seo_archive.retired_role_permissions WHERE permission_code IN ('seo:insight:view','seo:task:handle')
ON CONFLICT DO NOTHING;
DELETE FROM admin_permissions WHERE code IN ('seo:insight:view','seo:task:handle','seo:snippet:propose','seo:automation:tune','seo:keyword:edit');

-- Renaming a trigger locks its table, so these follow the archive work.
ALTER TRIGGER seo_background_revision ON background_item_content RENAME TO background_revision;
ALTER TRIGGER seo_feed_revision ON update_feeds RENAME TO update_feed_revision;
CREATE TRIGGER intent_page_revision AFTER INSERT OR UPDATE OR DELETE ON package_intent_pages FOR EACH ROW EXECUTE FUNCTION bump_entity_revision();
CREATE TRIGGER package_seo_revision AFTER INSERT OR UPDATE OR DELETE ON package_seo_overrides FOR EACH ROW EXECUTE FUNCTION bump_entity_revision();

-- No foreign key crosses into or out of the archive: archived rows must not constrain live tables
-- (a key into packages would block link merges, which delete the merged package), and the write
-- log no longer points into the archived task queue. Dropping a key locks the referenced table,
-- packages and admin_users among them, so this is the last statement. Down restores these as
-- NOT VALID.
CREATE TABLE seo_archive.retired_foreign_keys(schema_name TEXT NOT NULL, table_name TEXT NOT NULL, name TEXT NOT NULL, definition TEXT NOT NULL, PRIMARY KEY(schema_name,table_name,name));
-- +goose StatementBegin
DO $$
DECLARE fk record;
BEGIN
 INSERT INTO seo_archive.retired_foreign_keys
 SELECT n.nspname,r.relname,c.conname,pg_get_constraintdef(c.oid) FROM pg_constraint c
 JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace JOIN pg_class f ON f.oid=c.confrelid
 WHERE c.contype='f' AND (r.relnamespace='seo_archive'::regnamespace)<>(f.relnamespace='seo_archive'::regnamespace);
 FOR fk IN SELECT * FROM seo_archive.retired_foreign_keys LOOP
  EXECUTE format('ALTER TABLE %I.%I DROP CONSTRAINT %I',fk.schema_name,fk.table_name,fk.name);
 END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- As in Up, the whole-table read comes before any lock that blocks requests. Rows unchanged
-- since Up get their old grants back.
UPDATE content_revisions c SET required_permissions=r.before FROM seo_archive.retired_permission_rows r
WHERE r.tbl='content_revisions' AND r.id=c.id::text AND c.required_permissions=r.after;

-- Then Up in reverse. Foreign keys come back first, while every table still has its 110 name
-- and schema, so the recorded definitions resolve; they then follow their tables back.
-- +goose StatementBegin
DO $$
DECLARE fk record;
BEGIN
 FOR fk IN SELECT * FROM seo_archive.retired_foreign_keys LOOP
  -- A key restored by an earlier Down is already NOT VALID.
  EXECUTE format('ALTER TABLE %I.%I ADD CONSTRAINT %I %s',fk.schema_name,fk.table_name,fk.name,CASE WHEN fk.definition LIKE '% NOT VALID' THEN fk.definition ELSE fk.definition||' NOT VALID' END);
 END LOOP;
END $$;
-- +goose StatementEnd
DROP TABLE seo_archive.retired_foreign_keys;

DROP TRIGGER package_seo_revision ON package_seo_overrides;
DROP TRIGGER intent_page_revision ON package_intent_pages;
ALTER TRIGGER update_feed_revision ON update_feeds RENAME TO seo_feed_revision;
ALTER TRIGGER background_revision ON background_item_content RENAME TO seo_background_revision;

DELETE FROM admin_permissions WHERE code='site:index:view';
INSERT INTO admin_permissions SELECT * FROM seo_archive.retired_admin_permissions ON CONFLICT DO NOTHING;
INSERT INTO admin_role_permissions SELECT * FROM seo_archive.retired_role_permissions r
WHERE EXISTS(SELECT 1 FROM admin_roles WHERE id=r.role_id) ON CONFLICT DO NOTHING;
DROP TABLE seo_archive.retired_role_permissions, seo_archive.retired_admin_permissions;

-- Rows unchanged since Up get their old grants back; others read the index view as SEO insight.
UPDATE mcp_oauth_grants g SET scopes=coalesce(
 (SELECT array_to_string(r.before,' ') FROM seo_archive.retired_permission_rows r WHERE r.tbl='mcp_oauth_grants' AND r.id=g.id::text AND array_to_string(r.after,' ')=g.scopes),
 array_to_string(array_replace(string_to_array(g.scopes,' '),'site:index:view','seo:insight:view'),' '))
WHERE 'site:index:view'=ANY(string_to_array(g.scopes,' ')) OR EXISTS(SELECT 1 FROM seo_archive.retired_permission_rows r WHERE r.tbl='mcp_oauth_grants' AND r.id=g.id::text);
-- Up left clients alone; only those registered since name the index view.
UPDATE mcp_oauth_clients SET scopes=array_to_string(array_replace(string_to_array(scopes,' '),'site:index:view','seo:insight:view'),' ')
WHERE 'site:index:view'=ANY(string_to_array(scopes,' '));
ALTER TABLE agent_tokens DROP CONSTRAINT agent_tokens_permissions_check;
UPDATE agent_tokens t SET permissions=coalesce(
 (SELECT r.before FROM seo_archive.retired_permission_rows r WHERE r.tbl='agent_tokens' AND r.id=t.id::text AND r.after=t.permissions),
 array_replace(t.permissions,'site:index:view','seo:insight:view'))
WHERE 'site:index:view'=ANY(t.permissions) OR EXISTS(SELECT 1 FROM seo_archive.retired_permission_rows r WHERE r.tbl='agent_tokens' AND r.id=t.id::text);
-- +goose StatementBegin
DO $$
DECLARE prior RECORD;
BEGIN
 SELECT * INTO STRICT prior FROM seo_archive.agent_token_policy;
 EXECUTE 'ALTER TABLE agent_tokens ALTER COLUMN permissions SET DEFAULT '||prior.defaults;
 EXECUTE 'ALTER TABLE agent_tokens ADD CONSTRAINT agent_tokens_permissions_check CHECK ('||prior.expression||')';
END $$;
-- +goose StatementEnd
DROP TABLE seo_archive.agent_token_policy;

-- Provider settings come back erased and disabled; credentials are not restored.
-- +goose StatementBegin
DO $$
DECLARE t record;
BEGIN
 FOR t IN SELECT name FROM seo_archive.retired_tables ORDER BY name LOOP
  EXECUTE format('DROP VIEW public.%I',t.name);
  EXECUTE format('ALTER TABLE seo_archive.%I SET SCHEMA public',t.name);
 END LOOP;
END $$;
-- +goose StatementEnd
ALTER FUNCTION seo_archive.protect_automatic_listing_package() SET SCHEMA public;
ALTER FUNCTION seo_archive.protect_automatic_listing_download() SET SCHEMA public;

ALTER FUNCTION bump_entity_revision() RENAME TO seo_bump_entity_revision;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION seo_bump_entity_revision() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE data JSONB; entity TEXT;
BEGIN
 IF TG_OP='DELETE' THEN data:=to_jsonb(OLD); ELSE data:=to_jsonb(NEW); END IF;
 IF TG_TABLE_NAME='background_item_content' THEN entity:='background:'||(data->>'label');
 ELSIF TG_TABLE_NAME='update_feeds' THEN entity:='update_feed:'||(data->>'source')||':'||(data->>'bundle_id');
 ELSE entity:='site:keywords'; END IF;
 INSERT INTO seo_entity_revisions(entity_key,revision) VALUES(entity,1) ON CONFLICT(entity_key) DO UPDATE SET revision=seo_entity_revisions.revision+1;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

DROP VIEW public.seo_evidence_snapshots, public.seo_entity_revisions, public.seo_gate_events, public.seo_task_controls;
DROP INDEX agent_write_controls_created;
-- +goose StatementBegin
DO $$
DECLARE t record; o record;
BEGIN
 FOR t IN SELECT * FROM (VALUES ('agent_write_controls','seo_task_controls'),('agent_write_events','seo_gate_events'),
  ('entity_revisions','seo_entity_revisions'),('evidence_snapshots','seo_evidence_snapshots')) v(tbl,old) LOOP
  FOR o IN SELECT conname FROM pg_constraint WHERE conrelid=('public.'||t.tbl)::regclass AND starts_with(conname,t.tbl||'_') LOOP
   EXECUTE format('ALTER TABLE public.%I RENAME CONSTRAINT %I TO %I',t.tbl,o.conname,t.old||substr(o.conname,length(t.tbl)+1));
  END LOOP;
  FOR o IN SELECT s.relname FROM pg_depend d JOIN pg_class s ON s.oid=d.objid
   WHERE d.classid='pg_class'::regclass AND d.refobjid=('public.'||t.tbl)::regclass AND s.relkind='S' AND starts_with(s.relname,t.tbl||'_') LOOP
   EXECUTE format('ALTER SEQUENCE public.%I RENAME TO %I',o.relname,t.old||substr(o.relname,length(t.tbl)+1));
  END LOOP;
 END LOOP;
END $$;
-- +goose StatementEnd
ALTER INDEX evidence_snapshots_entity RENAME TO seo_evidence_entity;
ALTER INDEX agent_write_events_request RENAME TO seo_gate_events_request;
ALTER TABLE evidence_snapshots RENAME TO seo_evidence_snapshots;
ALTER TABLE entity_revisions RENAME TO seo_entity_revisions;
ALTER TABLE agent_write_events RENAME TO seo_gate_events;
ALTER TABLE agent_write_controls RENAME TO seo_task_controls;

DROP TABLE seo_archive.retired_permission_rows, seo_archive.retired_tables;
DROP SCHEMA seo_archive;
