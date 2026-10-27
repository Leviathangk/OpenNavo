-- +goose Up
-- Authoring limits apply to writable locales. System-derived Traditional text
-- can expand (for example, 内存 -> 記憶體) without rolling back a valid source edit.
-- Keep all non-length checks and all authoring-locale length constraints intact.
-- +goose StatementBegin
DO $$
DECLARE
 c RECORD;
BEGIN
 FOR c IN
  SELECT conrelid::regclass AS relation, conname, pg_get_expr(conbin, conrelid) AS expression
  FROM pg_constraint
  WHERE contype='c'
   AND conrelid IN ('category_i18n'::regclass, 'collection_i18n'::regclass,
                   'article_revision_i18n'::regclass, 'article_author_i18n'::regclass)
   AND pg_get_expr(conbin, conrelid) ~ '\mlength\('
 LOOP
  EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', c.relation, c.conname);
  EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I CHECK (locale=''zh-Hant'' OR (%s)) NOT VALID',
                 c.relation, c.conname, c.expression);
 END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Retain expanded storage: valid derived rows may already exceed the old limits.
