-- +goose Up
ALTER TABLE category_i18n
 ADD COLUMN seo_title TEXT CHECK (length(seo_title)<=120),
 ADD COLUMN seo_description TEXT CHECK (length(seo_description)<=300);
ALTER TABLE collection_i18n
 ADD COLUMN seo_title TEXT CHECK (length(seo_title)<=120),
 ADD COLUMN seo_description TEXT CHECK (length(seo_description)<=300);

-- Retry only failures addressed by this release. Newer active work already
-- represents the URL; preserve its lease and leave the older receipt intact.
UPDATE url_notify_outbox old SET state='pending',attempts=0,next_attempt_at=now(),
 lease_token=NULL,lease_until=NULL,updated_at=now()
WHERE old.state='dead' AND (
 old.last_error='edge purge incomplete: status 429'
 OR (old.path ~ '/apps/[^/]+/dependencies$'
     AND old.last_error='public canonical or robots differs from page decision'))
AND NOT EXISTS (SELECT 1 FROM url_notify_outbox active WHERE active.url=old.url
 AND active.state IN ('pending','invalidated','probed'))
AND NOT EXISTS (SELECT 1 FROM url_notify_outbox newer WHERE newer.url=old.url AND newer.id>old.id);

-- Cached API decisions predate the visibility rule. Invalidate them before the
-- recovered jobs render/probe HTML; the web release purge refreshes edge pages.
INSERT INTO job_outbox(unique_key,job_type,payload)
 SELECT 'cache:invalidate:seo-delivery-metadata','cache:invalidate','["c:pkg:*","c:dep:*"]'::jsonb
 WHERE EXISTS(SELECT 1 FROM packages)
 ON CONFLICT(unique_key) DO UPDATE SET payload=EXCLUDED.payload,available_at=now();

-- +goose Down
-- Delivery receipts are not rolled back: doing so could repeat announcements.
ALTER TABLE collection_i18n DROP COLUMN seo_description, DROP COLUMN seo_title;
ALTER TABLE category_i18n DROP COLUMN seo_description, DROP COLUMN seo_title;
