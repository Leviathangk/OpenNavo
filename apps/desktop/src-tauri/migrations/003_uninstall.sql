-- Local paths never leave this database. No foreign key: history cleanup must not destroy undo records.
ALTER TABLE tasks ADD COLUMN target_name TEXT;
ALTER TABLE tasks ADD COLUMN uninstall TEXT;
CREATE TABLE uninstall_manifest (
  task_id TEXT NOT NULL,
  item_id TEXT NOT NULL,
  original_path TEXT NOT NULL,
  trash_path TEXT,
  bytes INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL,
  snapshot TEXT NOT NULL,
  PRIMARY KEY (task_id, item_id)
);
CREATE INDEX uninstall_manifest_task ON uninstall_manifest(task_id);
