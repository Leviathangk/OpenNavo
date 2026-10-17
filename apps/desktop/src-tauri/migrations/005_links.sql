-- Link navigation is activity, never an install task. Keep tasks free of links.
CREATE TABLE link_activity (
  id INTEGER PRIMARY KEY,
  token TEXT NOT NULL,
  target TEXT NOT NULL,
  url TEXT NOT NULL,
  opened_at INTEGER NOT NULL
);
CREATE INDEX link_activity_opened ON link_activity(opened_at DESC);
CREATE TRIGGER tasks_reject_link_insert BEFORE INSERT ON tasks WHEN NEW.kind='link'
BEGIN SELECT RAISE(ABORT, 'link tasks are not installable'); END;
CREATE TRIGGER tasks_reject_link_update BEFORE UPDATE OF kind ON tasks WHEN NEW.kind='link'
BEGIN SELECT RAISE(ABORT, 'link tasks are not installable'); END;
