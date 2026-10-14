-- +goose Up
-- Facts read from public sources by a scheduled job, never typed in: the license of the GitHub
-- repository an app comes from, and the Mac App Store listing that shares a bundle identifier with
-- the Homebrew app. input records what the Homebrew definition pointed at when the row was checked,
-- so a changed definition is checked again. Only 'found' rows reach pages; 'none' and 'error' keep
-- the schedule. These facts are not part of the catalog change feed (the desktop app does not use them).
CREATE TABLE package_source_facts (
 package_id BIGINT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
 source TEXT NOT NULL CHECK (source IN ('github','app_store')),
 input TEXT NOT NULL,
 subject TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK (status IN ('found','none','error')),
 data JSONB NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(data)='object'),
 etag TEXT,
 checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 next_check_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 fail_count INTEGER NOT NULL DEFAULT 0 CHECK (fail_count >= 0),
 PRIMARY KEY (package_id, source),
 CHECK (status <> 'found' OR data <> '{}')
);

-- +goose Down
DROP TABLE package_source_facts;
