ALTER TABLE tasks RENAME COLUMN uninstall TO outcome;
CREATE TABLE cleanup_protected (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK(kind IN ('path', 'bundle_id')),
    value TEXT NOT NULL,
    label TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(kind, value)
);
