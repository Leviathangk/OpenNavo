-- Local-only security state. Never read or migrate the production app DB in tests.
CREATE TABLE security_scans (id TEXT PRIMARY KEY, started_at INTEGER NOT NULL, result TEXT NOT NULL);
CREATE TABLE security_findings (id TEXT PRIMARY KEY, scan_id TEXT NOT NULL REFERENCES security_scans(id), value TEXT NOT NULL, snapshot TEXT NOT NULL);
CREATE TABLE security_quarantine (id TEXT PRIMARY KEY, value TEXT NOT NULL, metadata TEXT NOT NULL);
CREATE TABLE security_startup (id TEXT PRIMARY KEY, value TEXT NOT NULL, snapshot TEXT);
CREATE TABLE security_trusted (sha256 TEXT PRIMARY KEY, at INTEGER NOT NULL);
CREATE TABLE protection_events (id TEXT PRIMARY KEY, at INTEGER NOT NULL, value TEXT NOT NULL);
CREATE INDEX protection_events_time ON protection_events(at DESC);
