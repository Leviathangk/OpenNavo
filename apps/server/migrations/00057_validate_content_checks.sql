-- +goose NO TRANSACTION
-- +goose Up
-- 00050/00053/00056 install CHECKs immediately for new writes without scanning
-- existing content under ACCESS EXCLUSIVE. Each validation commits independently
-- with SHARE UPDATE EXCLUSIVE; retries can safely validate completed checks again.
ALTER TABLE packages VALIDATE CONSTRAINT packages_source_locale_check;
ALTER TABLE releases VALIDATE CONSTRAINT releases_source_locale_check;
ALTER TABLE categories VALIDATE CONSTRAINT categories_source_locale_check;
ALTER TABLE collections VALIDATE CONSTRAINT collections_source_locale_check;
ALTER TABLE features VALIDATE CONSTRAINT features_source_locale_check;
ALTER TABLE package_screenshots VALIDATE CONSTRAINT package_screenshots_source_locale_check;
ALTER TABLE collection_items VALIDATE CONSTRAINT collection_items_source_locale_check;
ALTER TABLE mirrors VALIDATE CONSTRAINT mirrors_source_locale_check;
ALTER TABLE package_i18n VALIDATE CONSTRAINT package_i18n_locale_check;
ALTER TABLE package_i18n VALIDATE CONSTRAINT package_i18n_source_locale_check;
ALTER TABLE release_i18n VALIDATE CONSTRAINT release_i18n_locale_check;
ALTER TABLE release_i18n VALIDATE CONSTRAINT release_i18n_source_locale_check;
ALTER TABLE category_i18n VALIDATE CONSTRAINT category_i18n_locale_check;
ALTER TABLE category_i18n VALIDATE CONSTRAINT category_i18n_source_locale_check;
ALTER TABLE collection_i18n VALIDATE CONSTRAINT collection_i18n_locale_check;
ALTER TABLE collection_i18n VALIDATE CONSTRAINT collection_i18n_source_locale_check;
ALTER TABLE feature_i18n VALIDATE CONSTRAINT feature_i18n_locale_check;
ALTER TABLE feature_i18n VALIDATE CONSTRAINT feature_i18n_source_locale_check;
ALTER TABLE screenshot_i18n VALIDATE CONSTRAINT screenshot_i18n_locale_check;
ALTER TABLE screenshot_i18n VALIDATE CONSTRAINT screenshot_i18n_source_locale_check;
ALTER TABLE collection_item_i18n VALIDATE CONSTRAINT collection_item_i18n_locale_check;
ALTER TABLE collection_item_i18n VALIDATE CONSTRAINT collection_item_i18n_source_locale_check;
ALTER TABLE mirror_i18n VALIDATE CONSTRAINT mirror_i18n_locale_check;
ALTER TABLE mirror_i18n VALIDATE CONSTRAINT mirror_i18n_source_locale_check;
ALTER TABLE announcement_i18n VALIDATE CONSTRAINT announcement_i18n_locale_check;
ALTER TABLE content_revisions VALIDATE CONSTRAINT content_revisions_locale_check;
ALTER TABLE article_authors VALIDATE CONSTRAINT article_authors_source_locale_check;
ALTER TABLE article_author_i18n VALIDATE CONSTRAINT article_author_i18n_locale_check;
ALTER TABLE article_author_i18n VALIDATE CONSTRAINT article_author_i18n_source_locale_check;
ALTER TABLE article_revisions VALIDATE CONSTRAINT article_revisions_source_locale_check;
ALTER TABLE article_revision_i18n VALIDATE CONSTRAINT article_revision_i18n_locale_check;
ALTER TABLE article_revision_i18n VALIDATE CONSTRAINT article_revision_i18n_source_locale_check;
ALTER TABLE link_apps VALIDATE CONSTRAINT link_apps_pricing_check;
ALTER TABLE article_revisions VALIDATE CONSTRAINT article_revisions_claims_check;
ALTER TABLE article_revisions VALIDATE CONSTRAINT article_revisions_claim_evidence_check;
ALTER TABLE article_revisions VALIDATE CONSTRAINT article_revisions_seo_target_check;

-- +goose Down
-- Validation does not change accepted data. Earlier Down migrations restore their
-- constraints transactionally and refuse incompatible content without deleting it.
SELECT 1;
