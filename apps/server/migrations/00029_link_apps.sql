-- +goose Up
ALTER TABLE packages DROP CONSTRAINT packages_kind_check,
  ADD CONSTRAINT packages_kind_check CHECK (kind IN ('cask','formula','link'));
CREATE UNIQUE INDEX packages_app_token ON packages(token) WHERE kind IN ('cask','link');
ALTER TABLE catalog_changes DROP CONSTRAINT catalog_changes_reason_check,
  ADD CONSTRAINT catalog_changes_reason_check CHECK (reason IN ('homebrew','content','asset','link'));
CREATE TABLE link_apps (
  package_id BIGINT PRIMARY KEY REFERENCES packages(id) ON DELETE CASCADE,
  homepage TEXT,
  download_url TEXT,
  github_repo TEXT,
  bundle_ids TEXT[] NOT NULL DEFAULT '{}',
  team_id TEXT CHECK (team_id ~ '^[A-Z0-9]{10}$'),
  pricing TEXT NOT NULL CHECK (pricing IN ('free','paid','freemium','subscription','open_source')),
  sparkle_feed TEXT,
  asset_rules JSONB,
  notes JSONB,
  latest JSONB,
  etag TEXT,
  synced_at TIMESTAMPTZ,
  last_error TEXT,
  adopted_by_cask_at TIMESTAMPTZ,
  CHECK (COALESCE(homepage,'')<>'' OR COALESCE(download_url,'')<>'' OR COALESCE(github_repo,'')<>''),
  CHECK (homepage IS NULL OR homepage ~ '^https://'),
  CHECK (download_url IS NULL OR download_url ~ '^https://'),
  CHECK (sparkle_feed IS NULL OR sparkle_feed ~ '^https://'),
  CHECK (github_repo IS NULL OR github_repo ~ '^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$')
);

-- +goose Down
-- Refuse to discard user-curated apps silently when rolling back.
ALTER TABLE packages DROP CONSTRAINT packages_kind_check,
  ADD CONSTRAINT packages_kind_check CHECK (kind IN ('cask','formula'));
DROP TABLE link_apps;
DROP INDEX packages_app_token;
DELETE FROM catalog_changes WHERE reason='link';
ALTER TABLE catalog_changes DROP CONSTRAINT catalog_changes_reason_check,
  ADD CONSTRAINT catalog_changes_reason_check CHECK (reason IN ('homebrew','content','asset'));
