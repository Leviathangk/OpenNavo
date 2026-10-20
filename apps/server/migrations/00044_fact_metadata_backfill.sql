-- +goose NO TRANSACTION
-- +goose Up
-- Only migration cursors live here, never business evidence. Each cursor commits
-- with its batch; a failed CALL resumes at the last committed primary key.
CREATE TABLE IF NOT EXISTS fact_metadata_backfill_progress (
 name TEXT PRIMARY KEY,
 cursor JSONB NOT NULL DEFAULT '{}',
 completed BOOLEAN NOT NULL DEFAULT false
);
-- +goose StatementBegin
CREATE OR REPLACE PROCEDURE backfill_fact_metadata() LANGUAGE plpgsql AS $$
DECLARE p packages; r record; progress jsonb; done boolean; last_id bigint; n integer;
BEGIN
 INSERT INTO fact_metadata_backfill_progress(name) VALUES('00044:packages'),('00044:cover-alt') ON CONFLICT DO NOTHING;
 COMMIT;
 LOOP
  -- Same ordering as sync writers; released after at most 256 catalog rows.
  PERFORM pg_advisory_xact_lock(hashtextextended('catalog:changes',0));
  SELECT cursor,completed INTO progress,done FROM fact_metadata_backfill_progress WHERE name='00044:packages' FOR UPDATE;
  EXIT WHEN done;
  last_id:=COALESCE((progress->>'id')::bigint,0); n:=0;
  FOR p IN SELECT * FROM packages WHERE id>last_id AND kind='cask' ORDER BY id LIMIT 256 FOR UPDATE LOOP
   PERFORM reconcile_background_declarations(p);
   PERFORM reconcile_version_requirements(p);
   -- Preserve only prior observations; migration time is not a successful sync.
   UPDATE packages SET homebrew_checked_at=updated_at WHERE id=p.id AND homebrew_checked_at IS NULL;
   last_id:=p.id; n:=n+1;
  END LOOP;
  UPDATE fact_metadata_backfill_progress SET cursor=jsonb_build_object('id',last_id),completed=(n<256) WHERE name='00044:packages';
  COMMIT;
 END LOOP;
 COMMIT;
 LOOP
  SELECT cursor,completed INTO progress,done FROM fact_metadata_backfill_progress WHERE name='00044:cover-alt' FOR UPDATE;
  EXIT WHEN done;
  last_id:=COALESCE((progress->>'id')::bigint,0); n:=0;
  FOR r IN SELECT id,source_locale,cover_alt FROM article_revisions WHERE id>last_id ORDER BY id LIMIT 256 FOR UPDATE LOOP
   UPDATE article_revision_i18n SET cover_alt=r.cover_alt
   WHERE revision_id=r.id AND locale=r.source_locale AND cover_alt IS NULL AND r.cover_alt IS NOT NULL;
   last_id:=r.id; n:=n+1;
  END LOOP;
  UPDATE fact_metadata_backfill_progress SET cursor=jsonb_build_object('id',last_id),completed=(n<256) WHERE name='00044:cover-alt';
  COMMIT;
 END LOOP;
 COMMIT;
END $$;
-- +goose StatementEnd
CALL backfill_fact_metadata();
DROP PROCEDURE backfill_fact_metadata();

-- +goose Down
-- Data is retained; lower migrations refuse dropping populated evidence.
DROP PROCEDURE IF EXISTS backfill_fact_metadata();
DROP TABLE IF EXISTS fact_metadata_backfill_progress;
