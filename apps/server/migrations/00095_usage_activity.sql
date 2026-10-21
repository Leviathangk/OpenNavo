-- +goose Up
CREATE TABLE usage_identities (
  platform TEXT NOT NULL CHECK (platform IN ('web','desktop')),
  anonymous_id UUID NOT NULL,
  first_seen_at TIMESTAMPTZ NOT NULL,
  last_seen_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (platform, anonymous_id)
);
CREATE INDEX usage_identities_first_seen ON usage_identities(first_seen_at);
CREATE INDEX usage_identities_last_seen ON usage_identities(last_seen_at);
CREATE TABLE usage_daily (
  day DATE NOT NULL,
  platform TEXT NOT NULL,
  anonymous_id UUID NOT NULL,
  last_active_at TIMESTAMPTZ NOT NULL,
  version VARCHAR(64) NOT NULL,
  locale VARCHAR(35) NOT NULL,
  PRIMARY KEY (day, platform, anonymous_id),
  FOREIGN KEY (platform, anonymous_id) REFERENCES usage_identities(platform, anonymous_id) ON DELETE CASCADE
);
-- +goose Down
DROP TABLE usage_daily;
DROP TABLE usage_identities;
