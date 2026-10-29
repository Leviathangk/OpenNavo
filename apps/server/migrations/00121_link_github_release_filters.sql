-- +goose Up
ALTER TABLE link_apps
  ADD COLUMN github_tag_prefix TEXT CHECK (github_tag_prefix IS NULL OR octet_length(github_tag_prefix) BETWEEN 1 AND 256),
  ADD COLUMN github_tag_regex TEXT CHECK (github_tag_regex IS NULL OR octet_length(github_tag_regex) BETWEEN 1 AND 1024),
  ADD COLUMN github_asset_pattern TEXT CHECK (github_asset_pattern IS NULL OR octet_length(github_asset_pattern) BETWEEN 1 AND 512),
  ADD COLUMN include_prereleases BOOLEAN NOT NULL DEFAULT false,
  ADD CONSTRAINT link_github_tag_filters_exclusive CHECK (github_tag_prefix IS NULL OR github_tag_regex IS NULL),
  ADD CONSTRAINT link_github_filters_require_repo CHECK (github_repo IS NOT NULL OR (github_tag_prefix IS NULL AND github_tag_regex IS NULL AND github_asset_pattern IS NULL AND NOT include_prereleases));

-- +goose Down
ALTER TABLE link_apps
  DROP CONSTRAINT link_github_filters_require_repo,
  DROP CONSTRAINT link_github_tag_filters_exclusive,
  DROP COLUMN include_prereleases,
  DROP COLUMN github_asset_pattern,
  DROP COLUMN github_tag_regex,
  DROP COLUMN github_tag_prefix;
