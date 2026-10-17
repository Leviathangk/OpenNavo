CREATE TABLE IF NOT EXISTS update_prefs (
    app_key TEXT PRIMARY KEY NOT NULL,
    skipped_version TEXT,
    channel TEXT NOT NULL DEFAULT 'stable' CHECK(channel IN ('stable', 'beta')),
    ignored INTEGER NOT NULL DEFAULT 0 CHECK(ignored IN (0, 1))
);
