-- +goose Up
-- Independent metadata: never changes packages, catalog cursors or legacy snapshots.
CREATE TABLE update_feeds (
  bundle_id TEXT NOT NULL CHECK (bundle_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]*(\.[A-Za-z0-9_-]+)+$'),
  source TEXT NOT NULL CHECK (source IN ('sparkle','electron','github','vendor','msupdate')),
  feed_url TEXT NOT NULL CHECK (feed_url ~ '^https://'),
  team_id TEXT CHECK (team_id ~ '^[A-Z0-9]{10}$'),
  channel TEXT NOT NULL DEFAULT 'stable' CHECK (channel IN ('stable','beta')),
  package_id BIGINT REFERENCES packages(id) ON DELETE SET NULL,
  origin TEXT NOT NULL CHECK (origin IN ('manual','homebrew','link')),
  enabled BOOLEAN NOT NULL DEFAULT true,
  etag TEXT,
  verified_at TIMESTAMPTZ,
  checked_at TIMESTAMPTZ,
  last_error TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(bundle_id,source)
);
CREATE INDEX update_feeds_package ON update_feeds(package_id);
-- +goose Down
DROP TABLE update_feeds;
