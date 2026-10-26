-- +goose Up
-- One-time repair of sitemap dates. The deploy of 2026-10-24 raised the page hash version while every app had a
-- dirty refresh job (a migration had touched tables with dirty triggers), and the old rule took the job's time,
-- so pages that had not changed got a date in that hour (about 213,000 rows, 64% of all). A page there now
-- takes the time of the last real change it shows, read from columns no check or sync rewrites:
--   every page: packages.version_changed_at;
--   overview and details: updated_at of the package_i18n row the page shows (the public language fallback);
--   overview: screenshots (created_at, and caption updated_at in the shown languages);
--   overview and versions: releases shown on the timeline (updated_at, with release_i18n updated_at in the
--   shown languages) and package_versions.created_at.
-- Fact and source tables are left out: their updated_at and checked_at move with every re-verification. This
-- evidence covers at least what the worker's pageChanges reads for the same page, so the following hash-v3
-- refresh finds nothing newer and keeps the repaired date. A page without evidence, or whose evidence is later
-- than its date, keeps its date; the v3 refresh handles a later change. Only content_modified_at changes:
-- hashes, hash versions and check times stay, so the v3 refresh runs as planned. package_page_state has no
-- triggers, so this queues no refresh job, URL notification, purge or IndexNow announcement.
--
-- One statement suffices: it locks only the rows it changes, which only the worker writes, and sitemap reads
-- do not wait for row locks. It waits at most 5 s for a lock, then fails without changes; retry the deploy.
SET LOCAL lock_timeout = '5s';
UPDATE package_page_state s SET content_modified_at=e.at
FROM (
 SELECT x.package_id,x.locale,x.page_kind,x.page_number,GREATEST(
  p.version_changed_at,
  CASE WHEN x.page_kind IN ('overview','details') THEN (SELECT t.updated_at FROM package_i18n t
   WHERE t.package_id=p.id AND t.locale IN (x.locale,p.source_locale,'en-US') AND COALESCE(NULLIF(t.display_name,''),NULLIF(t.summary,''),NULLIF(t.description,'')) IS NOT NULL
   ORDER BY CASE t.locale WHEN x.locale THEN 0 WHEN 'en-US' THEN 1 ELSE 2 END LIMIT 1) END,
  CASE WHEN x.page_kind='overview' THEN (SELECT GREATEST(max(c.created_at),max(t.updated_at)) FROM package_screenshots c
   LEFT JOIN screenshot_i18n t ON t.screenshot_id=c.id AND t.locale IN (x.locale,c.source_locale,'en-US') WHERE c.package_id=p.id) END,
  CASE WHEN x.page_kind IN ('overview','versions') THEN (SELECT max(GREATEST(r.updated_at,t.updated_at)) FROM releases r
   LEFT JOIN release_i18n t ON t.release_id=r.id AND t.locale IN (x.locale,r.source_locale,'en-US') WHERE r.package_id=p.id AND NOT r.hidden) END,
  CASE WHEN x.page_kind IN ('overview','versions') THEN (SELECT max(v.created_at) FROM package_versions v WHERE v.package_id=p.id) END
 ) AS at
 FROM package_page_state x JOIN packages p ON p.id=x.package_id
 WHERE x.content_modified_at>='2026-10-24 12:00:00+00' AND x.content_modified_at<'2026-10-24 13:00:00+00'
) e
WHERE s.package_id=e.package_id AND s.locale=e.locale AND s.page_kind=e.page_kind AND s.page_number=e.page_number
 AND e.at<s.content_modified_at;

-- +goose Down
-- Nothing to undo: the dates replaced were wrong, and restoring them would announce changes that did not happen.
SELECT 1;
