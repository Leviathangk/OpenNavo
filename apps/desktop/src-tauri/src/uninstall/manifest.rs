use super::{
    Engine, poison,
    rules::{self, Snapshot},
};
use crate::{
    AppError,
    brew::env::{Ask, Reason},
    db::Database,
    model::*,
};
use rusqlite::params;
use std::{
    fs,
    path::{Path, PathBuf},
};

#[derive(Debug, Clone)]
pub struct Entry {
    pub task: String,
    pub item: String,
    pub original: PathBuf,
    pub trash: Option<PathBuf>,
    pub bytes: u64,
    pub state: String,
    pub snapshot: Snapshot,
}
pub fn pending(
    db: &Database,
    task: &str,
    item: &PlanItem,
    snapshot: &Snapshot,
    destination: Option<&Path>,
) -> Result<(), AppError> {
    db.with(|c| { c.execute("INSERT INTO uninstall_manifest(task_id,item_id,original_path,trash_path,bytes,state,snapshot) VALUES(?1,?2,?3,?4,?5,'pending',?6)",
        params![task, item.id, item.path, destination.map(|p| p.to_string_lossy().into_owned()), item.size_bytes.unwrap_or(0).min(i64::MAX as u64) as i64, serde_json::to_string(snapshot)?])?; Ok(()) })
}
pub fn finish(
    db: &Database,
    task: &str,
    item: &str,
    state: &str,
    destination: Option<&Path>,
    snapshot: &Snapshot,
) -> Result<(), AppError> {
    db.with(|c| { c.execute("UPDATE uninstall_manifest SET state=?3,trash_path=?4,snapshot=?5 WHERE task_id=?1 AND item_id=?2",
        params![task, item, state, destination.map(|p| p.to_string_lossy().into_owned()), serde_json::to_string(snapshot)?])?; Ok(()) })
}
pub fn entries(db: &Database, task: &str) -> Result<Vec<Entry>, AppError> {
    db.with(|c| {
        let mut stmt = c.prepare("SELECT item_id,original_path,trash_path,bytes,state,snapshot FROM uninstall_manifest WHERE task_id=?1 ORDER BY original_path")?;
        let rows = stmt.query_map([task], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?, r.get::<_, Option<String>>(2)?, r.get::<_, i64>(3)?, r.get::<_, String>(4)?, r.get::<_, String>(5)?)))?.collect::<rusqlite::Result<Vec<_>>>()?;
        rows.into_iter().map(|(item, original, trash, bytes, state, snapshot)| Ok(Entry { task: task.to_owned(), item, original: original.into(), trash: trash.map(PathBuf::from), bytes: bytes.max(0) as u64, state, snapshot: serde_json::from_str(&snapshot)? })).collect()
    })
}
impl Engine {
    /// Private working folder for administrator requests, next to OpenNavo's other data.
    pub(crate) fn admin_root(&self) -> PathBuf {
        self.icon_cache.with_file_name("uninstall-admin")
    }
    pub fn trash_root(&self) -> Result<PathBuf, AppError> {
        let root = self.roots.home.join(".Trash");
        if !rules::parents_safe(&root) {
            return Err(AppError::new("E_PROTECTED", "trash_parent"));
        }
        if !root.exists() {
            fs::create_dir(&root)?;
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                fs::set_permissions(&root, fs::Permissions::from_mode(0o700))?;
            }
        }
        if !fs::symlink_metadata(&root)?.is_dir() || fs::symlink_metadata(&root)?.is_symlink() {
            return Err(AppError::new("E_PROTECTED", "trash_symlink"));
        }
        Ok(root)
    }
    pub(crate) fn resolve_pending(&self, entry: &mut Entry) -> Result<(), AppError> {
        if entry.state != "pending" {
            return Ok(());
        }
        let root = self.trash_root()?;
        let candidates: Vec<_> = match &entry.trash {
            Some(path) => vec![path.clone()],
            // Without Full Disk Access the Trash cannot be listed; the entry stays pending.
            None => match fs::read_dir(&root) {
                Ok(entries) => entries.flatten().map(|e| e.path()).collect(),
                Err(_) => return Ok(()),
            },
        };
        for path in candidates {
            if path.parent() == Some(root.as_path())
                && let Ok(snapshot) = Snapshot::capture(&path)
                && moved_identity(&entry.snapshot, &snapshot)
            {
                finish(
                    &self.database,
                    &entry.task,
                    &entry.item,
                    "trash",
                    Some(&path),
                    &snapshot,
                )?;
                entry.trash = Some(path);
                entry.state = "trash".into();
                entry.snapshot = snapshot;
                return Ok(());
            }
        }
        Ok(())
    }
    pub fn restore(&self, task_id: &str) -> Result<RestoreResult, AppError> {
        let _guard = self.operation_lock.lock().map_err(poison)?;
        let mut values = entries(&self.database, task_id)?;
        if values.is_empty() {
            return Err(unavailable());
        }
        values.sort_by_key(|e| e.original.components().count());
        let app_paths: Vec<_> = values
            .iter()
            .filter(|e| {
                self.roots
                    .allowed(&e.original, MatchReason::AppBundle, Some(&e.original))
            })
            .map(|e| e.original.clone())
            .collect();
        let root = self.trash_root()?;
        let mut admins = vec![];
        let mut result = RestoreResult {
            restored: 0,
            missing: vec![],
        };
        let mut denied = false;
        for mut e in values {
            self.resolve_pending(&mut e)?;
            if matches!(e.state.as_str(), "restored" | "purged" | "failed" | "kept") {
                continue;
            }
            let Some(path) = &e.trash else {
                result
                    .missing
                    .push(e.original.to_string_lossy().into_owned());
                continue;
            };
            // macOS hides the Trash's contents from apps without Full Disk Access; an administrator password
            // would not help, so say what is needed instead.
            if privacy_denied(path) {
                denied = true;
                result
                    .missing
                    .push(e.original.to_string_lossy().into_owned());
                continue;
            }
            if e.state != "trash"
                || path.parent() != Some(root.as_path())
                || !e.snapshot.matches(path)
                || fs::symlink_metadata(&e.original).is_ok()
                || !rules::parents_safe(&e.original)
            {
                result
                    .missing
                    .push(e.original.to_string_lossy().into_owned());
                continue;
            }
            if self.roots.admin(&e.original) {
                admins.push((e.clone(), trash_request(&e, true, &app_paths)?));
                continue;
            }
            match super::execute::move_exclusive(path, &e.original) {
                Ok(()) => {
                    finish(
                        &self.database,
                        task_id,
                        &e.item,
                        "restored",
                        Some(path),
                        &e.snapshot,
                    )?;
                    result.restored += 1;
                    self.audit(task_id, &format!("restored {}", e.original.display()))?;
                }
                Err(_) => {
                    admins.push((e.clone(), trash_request(&e, true, &app_paths)?));
                }
            }
        }
        if !admins.is_empty() {
            let requests: Vec<_> = admins.iter().map(|(_, r)| r.clone()).collect();
            self.audit(
                task_id,
                &format!(
                    "==> OpenNavo: requesting administrator access for {} items",
                    requests.len()
                ),
            )?;
            let results = super::execute::admin_batch(self, &requests, Ask::new(Reason::Restore))
                .unwrap_or_default();
            for (e, _) in admins {
                if results
                    .iter()
                    .any(|r| r.item_id == e.item && r.error.is_none())
                {
                    finish(
                        &self.database,
                        task_id,
                        &e.item,
                        "restored",
                        e.trash.as_deref(),
                        &e.snapshot,
                    )?;
                    result.restored += 1;
                    self.audit(task_id, &format!("restored {}", e.original.display()))?;
                } else {
                    result
                        .missing
                        .push(e.original.to_string_lossy().into_owned());
                }
            }
        }
        self.refresh_outcome(task_id)?;
        (self.sink)(crate::events::CoreEvent::LocalApps(LocalAppsChanged {
            reason: LocalAppsChangedReason::Task,
        }));
        if result.restored == 0 {
            let error = if denied {
                AppError::new("E_FULL_DISK_ACCESS", "trash_privacy")
            } else {
                unavailable()
            };
            return Err(error.with_detail(serde_json::to_string(&result)?));
        }
        Ok(result)
    }
    pub fn purge(&self, task_id: &str) -> Result<u64, AppError> {
        let _guard = self.operation_lock.lock().map_err(poison)?;
        let root = self.trash_root()?;
        let mut bytes = 0;
        let mut missing = vec![];
        let mut admins = vec![];
        let values = entries(&self.database, task_id)?;
        if values.is_empty() {
            return Err(unavailable());
        }
        let mut denied = false;
        for mut e in values {
            self.resolve_pending(&mut e)?;
            if e.state != "trash" {
                continue;
            }
            let Some(path) = &e.trash else {
                missing.push(e.original.to_string_lossy().into_owned());
                continue;
            };
            if privacy_denied(path) {
                denied = true;
                missing.push(e.original.to_string_lossy().into_owned());
                continue;
            }
            if path.parent() != Some(root.as_path()) || !e.snapshot.matches(path) {
                missing.push(e.original.to_string_lossy().into_owned());
                self.audit(task_id, &format!("skipped changed {}", path.display()))?;
                continue;
            }
            if self.roots.admin(&e.original) || super::execute::remove(path, false).is_err() {
                admins.push((e.clone(), trash_request(&e, false, &[])?));
                continue;
            }
            finish(
                &self.database,
                task_id,
                &e.item,
                "purged",
                Some(path),
                &e.snapshot,
            )?;
            bytes += e.bytes;
            self.audit(task_id, &format!("purged {}", path.display()))?;
        }
        if !admins.is_empty() {
            let requests: Vec<_> = admins.iter().map(|(_, r)| r.clone()).collect();
            self.audit(
                task_id,
                &format!(
                    "==> OpenNavo: requesting administrator access for {} items",
                    requests.len()
                ),
            )?;
            let results = super::execute::admin_batch(self, &requests, Ask::new(Reason::Purge))?;
            for (e, _) in admins {
                if results
                    .iter()
                    .any(|r| r.item_id == e.item && r.error.is_none())
                {
                    finish(
                        &self.database,
                        task_id,
                        &e.item,
                        "purged",
                        e.trash.as_deref(),
                        &e.snapshot,
                    )?;
                    bytes += e.bytes;
                    self.audit(task_id, &format!("purged {}", e.original.display()))?;
                } else {
                    missing.push(e.original.to_string_lossy().into_owned());
                }
            }
        }
        self.refresh_outcome(task_id)?;
        if !missing.is_empty() {
            let error = if denied && bytes == 0 {
                AppError::new("E_FULL_DISK_ACCESS", "trash_privacy")
            } else {
                unavailable()
            };
            return Err(error.with_detail(
                serde_json::json!({ "freedBytes": bytes, "missing": missing }).to_string(),
            ));
        }
        Ok(bytes)
    }
    pub(crate) fn audit(&self, task: &str, line: &str) -> Result<(), AppError> {
        use std::io::Write;
        if let Some(path) = self
            .database
            .tasks()?
            .into_iter()
            .find(|t| t.id == task)
            .and_then(|t| t.log_path)
        {
            let mut options = fs::OpenOptions::new();
            options.append(true).create(true);
            #[cfg(unix)]
            {
                use std::os::unix::fs::OpenOptionsExt;
                options.mode(0o600).custom_flags(nix::libc::O_NOFOLLOW);
            }
            let mut file = options.open(path)?;
            writeln!(file, "{line}")?;
        }
        (self.sink)(crate::events::CoreEvent::Log(TaskLogEvent {
            id: task.to_owned(),
            lines: vec![line.to_owned()],
        }));
        Ok(())
    }
    /// Whether anything this run moved to the Trash can still be put back: it is there as it was left. An item that the
    /// system's privacy protection keeps us from examining counts as there, so nothing is given up on a guess.
    pub(crate) fn undoable(&self, task: &str) -> Result<bool, AppError> {
        Ok(entries(&self.database, task)?.iter().any(|e| {
            e.state == "trash"
                && e.trash
                    .as_ref()
                    .is_some_and(|path| e.snapshot.matches(path) || privacy_denied(path))
        }))
    }
    fn refresh_outcome(&self, task: &str) -> Result<(), AppError> {
        if let Some(mut record) = self.database.tasks()?.into_iter().find(|t| t.id == task) {
            if let Some(outcome) = &mut record.outcome {
                outcome.restorable = entries(&self.database, task)?.iter().any(|e| {
                    e.state == "trash" && e.trash.as_ref().is_some_and(|p| e.snapshot.matches(p))
                });
            }
            self.database.save_task(&record)?;
            (self.sink)(crate::events::CoreEvent::Task(Box::new(record)));
        }
        Ok(())
    }
}
/// The item exists but privacy protection (no Full Disk Access) keeps us from examining it.
fn privacy_denied(path: &Path) -> bool {
    matches!(fs::symlink_metadata(path), Err(error) if error.kind() == std::io::ErrorKind::PermissionDenied && error.raw_os_error() == Some(1))
}
pub(super) fn moved_identity(a: &Snapshot, b: &Snapshot) -> bool {
    a.device == b.device
        && a.inode == b.inode
        && a.kind == b.kind
        && a.link == b.link
        && a.length == b.length
        && a.modified == b.modified
}
fn unavailable() -> AppError {
    AppError::new("E_RESTORE_UNAVAILABLE", "uninstall_restore_unavailable")
}

fn trash_request(
    e: &Entry,
    restore: bool,
    apps: &[PathBuf],
) -> Result<super::execute::AdminRequest, AppError> {
    let path = e.trash.as_ref().ok_or_else(unavailable)?;
    let backup_app = apps
        .iter()
        .find(|app| crate::updater::backup::is_directory(app, &e.original));
    let reason = if backup_app.is_some() {
        MatchReason::NameExact
    } else if e.original.extension().is_some_and(|ext| ext == "app") {
        MatchReason::AppBundle
    } else if e.original.starts_with("/private/var/db/receipts") {
        MatchReason::PkgReceipt
    } else if e.snapshot.kind == 2 {
        MatchReason::Symlink
    } else {
        MatchReason::ZapRule
    };
    Ok(super::execute::AdminRequest {
        item: PlanItem {
            id: e.item.clone(),
            path: path.to_string_lossy().into_owned(),
            category: ItemCategory::AppData,
            kind: if e.snapshot.kind == 2 {
                PlanItemKind::Symlink
            } else if e.snapshot.kind == 1 {
                PlanItemKind::Dir
            } else {
                PlanItemKind::File
            },
            count: 1,
            size_bytes: Some(e.bytes),
            reason,
            confidence: PlanItemConfidence::High,
            selected: true,
            required: false,
            needs_admin: true,
            shared_with: vec![],
            note: None,
        },
        snapshot: e.snapshot.clone(),
        action: if restore {
            super::ItemAction::RestoreTrash {
                original: e.original.clone(),
                reason,
            }
        } else {
            super::ItemAction::PurgeTrash
        },
        mode: DeleteMode::Delete,
        destination: None,
        app_path: backup_app
            .cloned()
            .or_else(|| (reason == MatchReason::AppBundle).then(|| e.original.clone())),
    })
}
