-- +goose Up
-- Casks from third-party taps that an editor added explicitly. The packages row is the catalog entry
-- (tap and full_token come from the cask); this table records where the cask file lives and how the
-- scheduled tap sync last read it. The official cask sync leaves packages whose tap is not homebrew/*
-- alone, so these rows are only written by the tap sync and the admin tap operations.
CREATE TABLE tap_casks (
 package_id BIGINT PRIMARY KEY REFERENCES packages(id) ON DELETE CASCADE,
 tap TEXT NOT NULL CHECK (tap ~ '^[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9_.-]*$' AND tap NOT LIKE 'homebrew/%'),
 source_path TEXT NOT NULL CHECK (source_path ~ '^Casks/([0-9a-z_.-]+/)?[0-9a-z@+_.-]+[.]rb$'),
 etag TEXT,
 head_sha TEXT,
 checked_at TIMESTAMPTZ,
 last_error TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE tap_casks;
