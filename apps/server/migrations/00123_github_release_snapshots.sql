-- +goose Up
CREATE TABLE github_release_snapshots (
  day DATE NOT NULL,
  release_tag TEXT NOT NULL CHECK (release_tag LIKE 'desktop-v%'),
  asset_id BIGINT NOT NULL CHECK (asset_id > 0),
  asset_name TEXT NOT NULL CHECK (asset_name <> ''),
  download_count BIGINT NOT NULL CHECK (download_count >= 0),
  captured_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (day, asset_id),
  CHECK (day = (captured_at AT TIME ZONE 'Asia/Shanghai')::date)
);

-- +goose Down
DROP TABLE github_release_snapshots;
