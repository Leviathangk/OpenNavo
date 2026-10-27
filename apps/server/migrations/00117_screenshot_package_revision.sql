-- +goose Up
-- Package revision reads include screenshot history, including retained deletions.
-- Screenshots keep one package identity across their revisions.
CREATE INDEX content_revisions_screenshot_package ON content_revisions
 ((COALESCE(after_data#>>'{package_screenshots,0,package_id}',before_data#>>'{package_screenshots,0,package_id}')),id DESC)
 WHERE entity='screenshot';

-- +goose Down
DROP INDEX content_revisions_screenshot_package;
