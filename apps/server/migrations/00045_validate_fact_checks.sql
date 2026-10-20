-- +goose NO TRANSACTION
-- +goose Up
-- New writes were protected from 00039/41/42. Validate each check after its
-- metadata transaction has released ACCESS EXCLUSIVE; retries are idempotent.
ALTER TABLE feedback VALIDATE CONSTRAINT feedback_links_check;
ALTER TABLE feedback VALIDATE CONSTRAINT feedback_visibility_check;
ALTER TABLE assets VALIDATE CONSTRAINT assets_files_check;
ALTER TABLE article_revision_i18n VALIDATE CONSTRAINT article_revision_i18n_cover_alt_check;
-- +goose Down
SELECT 1;
