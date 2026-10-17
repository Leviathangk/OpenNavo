//! One-click rollback (P2) to the copy the last successful update kept. Only the
//! newest OpenNavo update of an app qualifies: the live bundle must still be the
//! version that update installed, and the recovery copy must be the exact version
//! it replaced, with the same signing identity. The swap is the same same-volume
//! atomic exchange the update used, reverted when registration fails.
use super::{
    UpdateSource, backup,
    gate::{self, Inspector},
    prefs, service,
    swap::Files,
    version,
};
use crate::{
    AppError,
    db::Database,
    model::{Task, TaskKind, TaskOp, TaskState},
    uninstall::rules::{self, Snapshot},
};
use serde::{Deserialize, Serialize};
use std::{
    fs,
    path::{Path, PathBuf},
};

/// What a successful update saved under [`record_key`].
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Record {
    pub app_id: String,
    pub path: PathBuf,
    pub from_version: String,
    pub to_version: String,
}
pub fn record_key(history_id: &str) -> String {
    format!("update_backup:{history_id}")
}

/// A rollback that passed every check short of signature verification.
#[derive(Debug, Clone)]
pub struct Plan {
    pub history_id: String,
    pub source: UpdateSource,
    pub app_id: String,
    pub installed: PathBuf,
    pub backup: PathBuf,
    /// Installed by the update; the rollback replaces it and skips it.
    pub current_version: String,
    /// Replaced by the update; the rollback restores it.
    pub restored_version: String,
}

/// Maps a local app id to its bundle path. `E_PROTECTED` marks a protected app.
pub type Resolve<'a> = &'a dyn Fn(&str) -> Result<PathBuf, AppError>;

fn unavailable(reason: &str) -> AppError {
    AppError::new("E_ROLLBACK_UNAVAILABLE", reason)
}
fn integrity(reason: &str) -> AppError {
    AppError::new("E_ROLLBACK_INTEGRITY", reason)
}
/// Keep the gate's reason for the log while reporting a failed rollback check.
fn verification(error: AppError) -> AppError {
    integrity(&error.message).with_detail(error.code)
}

/// Recorded versions come from the update source or from the local app list
/// (short version, else build). Either Info.plist key may match, compared the way
/// the update source compares versions.
fn same_version(source: UpdateSource, a: &str, b: &str) -> bool {
    a == b
        || (!a.is_empty()
            && !b.is_empty()
            && version::compare_source(source, a, b).is_ok_and(|o| o.is_eq()))
}
fn bundle_is(source: UpdateSource, bundle: &Path, expected: &str) -> bool {
    let Ok(info) = plist::Value::from_file(bundle.join("Contents/Info.plist")) else {
        return false;
    };
    info.as_dictionary().is_some_and(|dict| {
        ["CFBundleShortVersionString", "CFBundleVersion"]
            .into_iter()
            .filter_map(|key| dict.get(key).and_then(|v| v.as_string()))
            .any(|v| same_version(source, v, expected))
    })
}
fn identity_is(source: UpdateSource, identity: &gate::Identity, expected: &str) -> bool {
    same_version(source, &identity.marketing_version, expected)
        || same_version(source, &identity.version, expected)
}

/// The slot and its storage root must be the ones the update made: directories
/// owned by this user that nobody else can list, never links.
fn private(path: &Path, mask: u32) -> bool {
    use std::os::unix::fs::MetadataExt;
    fs::symlink_metadata(path).is_ok_and(|m| {
        m.is_dir()
            && !m.is_symlink()
            && m.uid() == unsafe { nix::libc::geteuid() }
            && m.mode() & mask == 0
    })
}

pub fn plan(db: &Database, history_id: &str, resolve: Resolve<'_>) -> Result<Plan, AppError> {
    plan_in(db, &db.tasks()?, history_id, resolve)
}

/// The subset of `ids` whose update can be rolled back now.
pub fn available(
    db: &Database,
    ids: &[String],
    resolve: Resolve<'_>,
) -> Result<Vec<String>, AppError> {
    if ids.len() > 200 {
        return Err(AppError::new("E_INVALID_ARG", "too_many_history_ids"));
    }
    let tasks = db.tasks()?;
    Ok(ids
        .iter()
        .filter(|id| plan_in(db, &tasks, id, resolve).is_ok())
        .cloned()
        .collect())
}

fn plan_in(
    db: &Database,
    tasks: &[Task],
    history_id: &str,
    resolve: Resolve<'_>,
) -> Result<Plan, AppError> {
    let task = tasks
        .iter()
        .find(|t| t.id == history_id)
        .ok_or_else(|| unavailable("history_missing"))?;
    let source = task.options.source.filter(|s| {
        matches!(
            s,
            UpdateSource::Sparkle
                | UpdateSource::Electron
                | UpdateSource::Github
                | UpdateSource::Vendor
        )
    });
    let (Some(target), Some(source)) = (task.target.as_ref(), source) else {
        return Err(unavailable("not_native_update"));
    };
    if task.op != TaskOp::Upgrade
        || task.state != TaskState::Succeeded
        || target.kind != TaskKind::App
    {
        return Err(unavailable("not_native_update"));
    }
    let record: Record = db
        .meta(&record_key(history_id))?
        .and_then(|s| serde_json::from_str(&s).ok())
        .ok_or_else(|| unavailable("no_backup"))?;
    if record.app_id != target.token {
        return Err(unavailable("backup_app_changed"));
    }
    // One recovery slot per app: only the newest OpenNavo change may use it.
    let order = |t: &Task| (t.finished_at.unwrap_or(0), t.id.clone());
    if tasks.iter().any(|t| {
        t.target == task.target
            && t.state == TaskState::Succeeded
            && matches!(t.op, TaskOp::Upgrade | TaskOp::Rollback)
            && order(t) > order(task)
    }) {
        return Err(unavailable("newer_change"));
    }
    let installed = resolve(&record.app_id).map_err(|error| {
        unavailable(if error.code == "E_PROTECTED" {
            "protected"
        } else {
            "app_missing"
        })
    })?;
    let slot = record
        .path
        .parent()
        .filter(|_| record.path.file_name().is_some_and(|n| n == "previous.app"))
        .filter(|dir| backup::is_directory(&installed, dir))
        .ok_or_else(|| unavailable("backup_app_changed"))?;
    if !rules::parents_safe(slot)
        || !private(slot, 0o077)
        || !slot.parent().is_some_and(|root| private(root, 0o066))
    {
        return Err(unavailable("backup_unsafe"));
    }
    if !fs::symlink_metadata(&record.path).is_ok_and(|m| m.is_dir() && !m.is_symlink()) {
        return Err(unavailable("no_backup"));
    }
    if service::requires_admin(&installed) {
        return Err(unavailable("admin_required"));
    }
    if !bundle_is(source, &installed, &record.to_version) {
        return Err(unavailable("installed_version_changed"));
    }
    if !bundle_is(source, &record.path, &record.from_version) {
        return Err(unavailable("backup_version_changed"));
    }
    Ok(Plan {
        history_id: task.id.clone(),
        source,
        app_id: record.app_id,
        installed,
        backup: record.path,
        current_version: record.to_version,
        restored_version: record.from_version,
    })
}

/// Verify both bundles, then exchange them atomically. A registration failure
/// puts the updated bundle back. On success the slot holds the version rolled
/// back from; it is removed, as an update can download it again.
pub fn apply(
    plan: &Plan,
    inspector: &dyn Inspector,
    files: &dyn Files,
    os: &str,
    arch: &str,
) -> Result<(), AppError> {
    let installed = Snapshot::capture(&plan.installed)?;
    let kept = Snapshot::capture(&plan.backup)?;
    if installed.kind != 1 || kept.kind != 1 {
        return Err(integrity("rollback_bundle_type"));
    }
    super::archive::validate_bundle_links(&plan.backup).map_err(verification)?;
    let current = inspector.inspect(&plan.installed).map_err(verification)?;
    let restored = inspector.inspect(&plan.backup).map_err(verification)?;
    let satisfied = inspector
        .satisfies(&plan.backup, &current.requirement)
        .map_err(verification)?;
    gate::compatible_identities(&current, &restored, satisfied, os, arch, None)
        .map_err(verification)?;
    if !identity_is(plan.source, &current, &plan.current_version)
        || !identity_is(plan.source, &restored, &plan.restored_version)
    {
        return Err(integrity("rollback_version_changed"));
    }
    if !installed.still(&plan.installed) || !kept.still(&plan.backup) {
        return Err(integrity("rollback_target_changed"));
    }
    files.exchange(&plan.installed, &plan.backup).map_err(|e| {
        AppError::new("E_UPDATE_SWAP", "rollback_swap_failed").with_detail(e.to_string())
    })?;
    if let Err(error) = files.register(&plan.installed) {
        if let Err(restore) = files.exchange(&plan.installed, &plan.backup) {
            return Err(
                AppError::new("E_UNKNOWN", "rollback_restore_failed").with_detail(format!(
                    "{}; {}; recovery={}",
                    error.code,
                    restore.code,
                    plan.backup.display()
                )),
            );
        }
        return Err(
            AppError::new("E_UPDATE_SWAP", "rollback_reverted").with_detail(error.to_string())
        );
    }
    // The exchange is committed; housekeeping never fails the rollback.
    match fs::remove_dir_all(&plan.backup) {
        Ok(()) => {
            if let Some(slot) = plan.backup.parent() {
                // Only empty directories go; another app's slot keeps the root.
                let _ = fs::remove_dir(slot);
                let _ = slot.parent().map(fs::remove_dir);
            }
        }
        Err(error) => log::warn!(
            "Rolled-back version cleanup deferred: {}: {error}",
            plan.backup.display()
        ),
    }
    Ok(())
}

/// The record is spent, and the version rolled back from is skipped so the
/// Updates list does not offer it straight back.
pub fn finish(db: &Database, plan: &Plan) -> Result<(), AppError> {
    db.with(|c| {
        c.execute(
            "DELETE FROM meta WHERE key=?1",
            [record_key(&plan.history_id)],
        )?;
        Ok(())
    })?;
    prefs::skip(
        db,
        &format!("app:{}", plan.app_id),
        Some(&plan.current_version),
    )
}
