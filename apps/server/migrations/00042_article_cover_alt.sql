-- +goose Up
ALTER TABLE article_revision_i18n ADD COLUMN cover_alt TEXT,
 ADD CONSTRAINT article_revision_i18n_cover_alt_check CHECK(length(cover_alt)<=300) NOT VALID;
-- Missing source-language values are backfilled in bounded transactions in 00044.
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 LOCK TABLE article_revision_i18n IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM article_revision_i18n WHERE cover_alt IS NOT NULL) THEN
  RAISE EXCEPTION 'Archive and explicitly resolve localized cover alt before Down 00042';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE article_revision_i18n DROP COLUMN cover_alt;
