use super::{Engine, EntryItem, poison, protect, reconcile, rules, tree};
use crate::{
    AppError,
    brew::{self, pty},
    model::*,
    queue::TaskQueue,
    uninstall::{self, ItemAction, manifest},
};
use std::{
    path::Path,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};
use tokio::sync::Notify;

pub async fn run(
    queue: Arc<TaskQueue>,
    task: &Task,
    flag: Arc<AtomicBool>,
    _notify: Arc<Notify>,
) -> Result<pty::Outcome, AppError> {
    let engine = queue.cleanup_engine.clone();
    let entry = engine.validate_task(task.target.as_ref(), &task.options)?;
    let ids = task
        .options
        .item_ids
        .as_ref()
        .ok_or_else(super::cached_error)?;
    let mut outcome = UninstallOutcome {
        freed_bytes: 0,
        trashed_bytes: 0,
        trashed: 0,
        deleted: 0,
        skipped: vec![],
        restorable: false,
    };
    queue.uninstall_progress(
        &task.id,
        TaskPhase::Preparing,
        0,
        0.0,
        Some("==> OpenNavo: cleanup".into()),
    )?;
    let mut removed = vec![];
    let mut command_failure = None;
    for (index, id) in ids.iter().enumerate() {
        if flag.load(Ordering::Acquire) {
            break;
        }
        let item = entry.items.get(id).ok_or_else(super::cached_error)?.clone();
        let result = if item.item.kind == CleanupItemKind::Command
            && super::docker::prune_args(item.item.reason).is_some()
        {
            let engine = engine.clone();
            let log_path = task.log_path.clone().ok_or_else(super::cached_error)?;
            let flag = flag.clone();
            let item = item.clone();
            crate::blocking(move || engine.docker(Path::new(&log_path), &item, flag)).await
        } else if item.item.kind == CleanupItemKind::Command {
            run_brew(&queue, task, &engine, &item, flag.clone()).await
        } else {
            let engine = engine.clone();
            let task_id = task.id.clone();
            let flag = flag.clone();
            let item = item.clone();
            crate::blocking(move || engine.act(&task_id, &item, &flag)).await
        };
        match result {
            Ok(Act::Deleted(bytes)) => {
                outcome.deleted += 1;
                outcome.freed_bytes = outcome.freed_bytes.saturating_add(bytes);
                removed.push(id.clone());
                log(
                    &queue,
                    &task.id,
                    index,
                    ids.len(),
                    format!("deleted {}", display(&item)),
                )?;
            }
            Ok(Act::Trashed) => {
                let bytes = item.item.size_bytes.unwrap_or(0);
                outcome.freed_bytes = outcome.freed_bytes.saturating_add(bytes);
                outcome.trashed_bytes = outcome.trashed_bytes.saturating_add(bytes);
                outcome.trashed += 1;
                outcome.restorable = true;
                removed.push(id.clone());
                log(
                    &queue,
                    &task.id,
                    index,
                    ids.len(),
                    format!("trashed {}", display(&item)),
                )?;
            }
            Ok(Act::Skipped(reason)) => {
                skip(&queue, task, &mut outcome, &item, reason, index, ids.len())?
            }
            Err(error) => {
                if error.code == "E_BREW_CLEANUP" {
                    command_failure = Some(error.clone());
                }
                let reason = match error.code.as_str() {
                    "E_PROTECTED" => SkippedItemReason::Protected,
                    "E_DOCKER_PRUNE" | "E_DOCKER_UNAVAILABLE" => SkippedItemReason::ToolUnavailable,
                    "E_PLAN_EXPIRED" => SkippedItemReason::Changed,
                    "E_NOT_FOUND" => SkippedItemReason::Missing,
                    "E_APP_RUNNING" | "E_CANCELED" => SkippedItemReason::InUse,
                    _ => SkippedItemReason::Permission,
                };
                skip(&queue, task, &mut outcome, &item, reason, index, ids.len())?;
            }
        }
        queue.set_uninstall_outcome(&task.id, outcome.clone())?;
    }
    // Include the unattempted selection when a task is canceled so the result accounts for every selected item.
    for id in ids {
        if !removed.contains(id) && !outcome.skipped.iter().any(|s| &s.item_id == id) {
            let item = &entry.items[id];
            skip(
                &queue,
                task,
                &mut outcome,
                item,
                SkippedItemReason::InUse,
                0,
                ids.len(),
            )?;
        }
    }
    outcome.restorable = manifest::entries(&engine.uninstall.database, &task.id)?
        .iter()
        .any(|e| e.state == "trash" || e.state == "pending");
    queue.set_uninstall_outcome(&task.id, outcome)?;
    engine.executed(&removed, &entry)?;
    queue.uninstall_progress(&task.id, TaskPhase::Finishing, 3, 100.0, None)?;
    if let Some(error) = command_failure {
        return Err(error);
    }
    Ok(pty::Outcome {
        code: 0,
        canceled: flag.load(Ordering::Acquire),
        #[cfg(test)]
        signal: None,
        parser: Default::default(),
    })
}
fn display(item: &EntryItem) -> &str {
    item.item.command.as_deref().unwrap_or(&item.item.path)
}
fn log(
    queue: &TaskQueue,
    task: &str,
    index: usize,
    count: usize,
    line: String,
) -> Result<(), AppError> {
    queue.uninstall_progress(
        task,
        TaskPhase::Cleaning,
        2,
        (index + 1) as f32 / count.max(1) as f32 * 100.0,
        Some(line),
    )
}
fn skip(
    queue: &TaskQueue,
    task: &Task,
    outcome: &mut UninstallOutcome,
    item: &EntryItem,
    reason: SkippedItemReason,
    index: usize,
    count: usize,
) -> Result<(), AppError> {
    outcome.skipped.push(SkippedItem {
        item_id: item.item.id.clone(),
        path: item.item.path.clone(),
        reason,
    });
    log(
        queue,
        &task.id,
        index,
        count,
        format!("skipped {:?} {}", reason, display(item)),
    )
}
pub enum Act {
    Deleted(u64),
    Trashed,
    Skipped(SkippedItemReason),
}
impl Engine {
    /// One of Docker's fixed prune commands. Docker is measured again right before and after; what it no longer reports
    /// as reclaimable is what the prune freed.
    fn docker(
        &self,
        log_path: &Path,
        item: &EntryItem,
        flag: Arc<AtomicBool>,
    ) -> Result<Act, AppError> {
        let Some(args) = super::docker::prune_args(item.item.reason)
            .filter(|_| matches!(item.action, ItemAction::ToolCommand))
        else {
            return Err(AppError::new("E_PROTECTED", "cleanup_command"));
        };
        let _operation = self.operation.lock().map_err(poison)?;
        if protect::entry_is_protected(self, &self.protected_list()?, item) {
            return Ok(Act::Skipped(SkippedItemReason::Protected));
        }
        let home = &self.uninstall.roots.home;
        let Some(docker) = self.processes.docker(home) else {
            return Ok(Act::Skipped(SkippedItemReason::ToolUnavailable));
        };
        if !super::docker::engine_running(self.processes.list().ok().as_deref()) {
            return Ok(Act::Skipped(SkippedItemReason::ToolUnavailable));
        }
        let endpoint = item
            .docker_endpoint
            .as_ref()
            .ok_or_else(super::cached_error)?;
        let measure = |flag: &AtomicBool| {
            super::docker::report(&docker, home, endpoint, Duration::from_secs(30), flag)
                .map(|report| report.get(item.item.reason).unwrap_or_default().reclaimable)
        };
        let before = match measure(&flag) {
            Ok(bytes) => bytes,
            Err(error) if error.code == "E_CANCELED" => {
                return Ok(Act::Skipped(SkippedItemReason::InUse));
            }
            Err(_) => return Ok(Act::Skipped(SkippedItemReason::ToolUnavailable)),
        };
        if before == 0 {
            return Ok(Act::Deleted(0));
        }
        if !endpoint.valid() {
            return Ok(Act::Skipped(SkippedItemReason::Changed));
        }
        let result = pty::execute(
            pty::Execution {
                executable: docker.clone(),
                args: endpoint.args(args),
                env: super::docker::env(home).into_iter().collect(),
                log_path: log_path.to_owned(),
                op: TaskOp::Cleanup,
                cancel: flag,
                grace: Duration::from_secs(3),
                cache_dir: None,
                download_total: None,
            },
            |_, _| {},
        )?;
        if result.canceled {
            return Ok(Act::Skipped(SkippedItemReason::InUse));
        }
        if result.code != 0 {
            return Err(AppError::new("E_DOCKER_PRUNE", "docker_prune_failed"));
        }
        let after = measure(&AtomicBool::new(false)).unwrap_or(0);
        Ok(Act::Deleted(before.saturating_sub(after)))
    }
    fn delete_cache(
        &self,
        item: &EntryItem,
        snapshot: &uninstall::rules::Snapshot,
        flag: &AtomicBool,
        partial: bool,
    ) -> Result<Act, AppError> {
        let path = Path::new(&item.item.path);
        let activity = super::owner::Activity::read(self);
        if let Some(reason) = item.owner.status_in(&activity) {
            return Ok(Act::Skipped(if reason == SkippedGroupReason::Running {
                SkippedItemReason::Running
            } else {
                SkippedItemReason::InUse
            }));
        }
        if !partial && activity.as_ref().is_ok_and(|a| a.in_use(path)) {
            return Ok(Act::Skipped(SkippedItemReason::InUse));
        }
        let open: Vec<_> = activity
            .as_ref()
            .map(|a| {
                a.open_paths
                    .iter()
                    .map(|(p, _)| p.clone())
                    .filter(|p| p.starts_with(path))
                    .collect()
            })
            .unwrap_or_default();
        let protected = self.protected_list()?;
        // Same rule as the scan: inside generated trees, names deep in the tree say nothing about user secrets, so
        // only the user's own protection rules apply below the root.
        let names_apply = rules::names_apply(item.item.reason);
        let bytes = tree::remove_cache(
            path,
            snapshot,
            flag,
            |p| {
                (names_apply
                    && rules::never_touch_scoped(
                        &self.uninstall.roots.home,
                        p,
                        item.owner.external_cache.as_deref(),
                    ))
                    || protect::is_protected(&protected, p, item.owner.bundle.as_deref())
            },
            &open,
        )?;
        if bytes == 0 && !open.is_empty() {
            return Ok(Act::Skipped(SkippedItemReason::InUse));
        }
        Ok(Act::Deleted(bytes))
    }
    pub fn act(&self, task_id: &str, item: &EntryItem, flag: &AtomicBool) -> Result<Act, AppError> {
        let _operation = self.operation.lock().map_err(poison)?;
        let path = Path::new(&item.item.path);
        if item.owner.protected
            || protect::is_protected(&self.protected_list()?, path, item.owner.bundle.as_deref())
            || rules::root_blocked(
                &self.uninstall.roots.home,
                path,
                item.owner.external_cache.as_deref(),
                item.item.reason,
            )
        {
            return Ok(Act::Skipped(SkippedItemReason::Protected));
        }
        let activity = super::owner::Activity::read(self);
        if let Some(reason) = item.owner.status_in(&activity) {
            return Ok(Act::Skipped(if reason == SkippedGroupReason::Running {
                SkippedItemReason::Running
            } else {
                SkippedItemReason::InUse
            }));
        }
        let partial = rules::partial_cache(item.item.reason, item.owner.tool_key.as_deref());
        if !partial
            && activity
                .as_ref()
                .is_ok_and(|activity| activity.in_use(path))
        {
            return Ok(Act::Skipped(SkippedItemReason::InUse));
        }

        if !uninstall::rules::parents_safe(path) {
            return Ok(Act::Skipped(SkippedItemReason::Changed));
        }
        if std::fs::symlink_metadata(path).is_err() {
            return Ok(Act::Skipped(SkippedItemReason::Missing));
        }
        let Some(before) = &item.tree else {
            return Err(super::cached_error());
        };
        if (before.compact || partial)
            && matches!(
                item.item.reason,
                CleanupReason::CacheDir
                    | CleanupReason::BrowserCache
                    | CleanupReason::ToolCache
                    | CleanupReason::ToolDownloadCache
                    | CleanupReason::DeviceSupport
                    | CleanupReason::DeviceSupportLatest
                    | CleanupReason::BuildArtifact
                    | CleanupReason::TempFile
                    | CleanupReason::Trash
            )
            && matches!(
                item.action,
                ItemAction::Path {
                    empty_only: false,
                    bootout: None
                }
            )
            && item.item.delete_mode == DeleteMode::Delete
            && item.item.kind == CleanupItemKind::Dir
        {
            let snapshot = before.snapshots.get(path).ok_or_else(super::cached_error)?;
            if partial && !before.compact {
                let open: Vec<_> = activity
                    .as_ref()
                    .map(|a| a.open_paths.iter().map(|(p, _)| p).collect())
                    .unwrap_or_default();
                for (p, expected) in &before.snapshots {
                    if open
                        .iter()
                        .any(|file| p.starts_with(file) || file.starts_with(p))
                    {
                        continue;
                    }
                    let unchanged = if expected.kind == 1 {
                        uninstall::rules::Snapshot::capture(p)
                            .is_ok_and(|now| tree::same_root(expected, &now))
                    } else {
                        expected.matches(p)
                    };
                    if !unchanged {
                        return Ok(Act::Skipped(SkippedItemReason::Changed));
                    }
                }
            }
            return self.delete_cache(item, snapshot, flag, partial);
        }
        // Moving an installer, a backup or an archive to the Trash is one rename of its root, and a wrong pick can be
        // put back. A large one is not walked again: its root identity is checked below, and a protection rule on
        // anything inside it already matched the root above.
        let current = if before.compact
            && item.item.delete_mode == DeleteMode::Trash
            && matches!(
                item.item.reason,
                CleanupReason::Installer
                    | CleanupReason::InstallerInstalled
                    | CleanupReason::DeviceBackup
                    | CleanupReason::XcodeArchive
            ) {
            before.clone()
        } else {
            match tree::capture(path, flag, Instant::now() + self.category_timeout) {
                Ok(tree) => tree,
                Err(error) if error.code == "E_PROTECTED" => {
                    return Ok(Act::Skipped(SkippedItemReason::Changed));
                }
                Err(error) => return Err(error),
            }
        };
        if if partial && before.kept_open_files > 0 {
            !before
                .snapshots
                .iter()
                .all(|(p, s)| current.snapshots.get(p) == Some(s))
        } else {
            current.snapshots != before.snapshots
        } {
            return Ok(Act::Skipped(SkippedItemReason::Changed));
        }
        let protected = self.protected_list()?;
        // The same rule as the scan: the root was checked above, and the names below it count only where they could
        // be an app's data store.
        let names_apply = rules::names_apply(item.item.reason);
        if current.snapshots.keys().any(|p| {
            (names_apply
                && rules::never_touch_scoped(
                    &self.uninstall.roots.home,
                    p,
                    item.owner.external_cache.as_deref(),
                ))
                || protect::is_protected(&protected, p, item.owner.bundle.as_deref())
        }) {
            return Ok(Act::Skipped(SkippedItemReason::Protected));
        }
        let snapshot = current
            .snapshots
            .get(path)
            .ok_or_else(super::cached_error)?;
        // User launch agents are only admitted by the existing missing-executable orphan rule; never invoke a system action.
        let ItemAction::Path {
            empty_only,
            bootout,
        } = &item.action
        else {
            return Err(AppError::new("E_PROTECTED", "cleanup_action"));
        };
        if let Some((label, system)) = bootout {
            if *system {
                return Err(AppError::new("E_PROTECTED", "cleanup_system_action"));
            }
            self.uninstall
                .file_actions
                .lock()
                .map_err(poison)?
                .bootout(label, false, &item.item.path)?;
        }
        if flag.load(Ordering::Acquire) || item.owner.status(self).is_some() {
            return Ok(Act::Skipped(SkippedItemReason::InUse));
        }
        if !snapshot.matches(path) {
            return Ok(Act::Skipped(SkippedItemReason::Changed));
        }
        if *empty_only && std::fs::read_dir(path)?.next().is_some() {
            return Ok(Act::Skipped(SkippedItemReason::InUse));
        }
        if item.item.delete_mode == DeleteMode::Delete {
            // Directories go through the descriptor-based remover: no symlink traversal, identity checks on every
            // entry, and read-only directories we own (Go's module cache) can still be emptied.
            if snapshot.kind == 1 && !*empty_only {
                return self.delete_cache(item, snapshot, flag, partial);
            }
            uninstall::execute::remove(path, *empty_only)?;
            return Ok(Act::Deleted(current.bytes));
        }
        let root = self.uninstall.trash_root()?;
        let plan_item = PlanItem {
            id: item.item.id.clone(),
            path: item.item.path.clone(),
            category: ItemCategory::AppData,
            kind: if snapshot.kind == 1 {
                PlanItemKind::Dir
            } else {
                PlanItemKind::File
            },
            count: current.snapshots.len() as i64,
            size_bytes: Some(current.bytes),
            reason: MatchReason::BundleId,
            confidence: PlanItemConfidence::High,
            selected: true,
            required: false,
            needs_admin: false,
            shared_with: vec![],
            note: None,
        };
        manifest::pending(
            &self.uninstall.database,
            task_id,
            &plan_item,
            snapshot,
            None,
        )?;
        let result = self
            .uninstall
            .file_actions
            .lock()
            .map_err(poison)?
            .trash(path, &root);
        match result {
            Ok(destination) => {
                if destination.parent() != Some(root.as_path()) {
                    return Err(AppError::new("E_PROTECTED", "cleanup_trash_destination"));
                }
                let moved = uninstall::rules::Snapshot::capture(&destination)?;
                if moved.device != snapshot.device || moved.inode != snapshot.inode {
                    return Err(AppError::new("E_PROTECTED", "cleanup_trash_identity"));
                }
                manifest::finish(
                    &self.uninstall.database,
                    task_id,
                    &item.item.id,
                    "trash",
                    Some(&destination),
                    &moved,
                )?;
                Ok(Act::Trashed)
            }
            Err(error) => {
                if let Some(mut pending) = manifest::entries(&self.uninstall.database, task_id)?
                    .into_iter()
                    .find(|e| e.item == item.item.id)
                {
                    self.uninstall.resolve_pending(&mut pending)?;
                    if pending.state == "trash" {
                        return Ok(Act::Trashed);
                    }
                }
                manifest::finish(
                    &self.uninstall.database,
                    task_id,
                    &item.item.id,
                    "failed",
                    None,
                    snapshot,
                )?;
                Err(error)
            }
        }
    }
    fn executed(&self, removed: &[String], executed: &super::Entry) -> Result<(), AppError> {
        let _operation = self.operation.lock().map_err(poison)?;
        let removed: Vec<_> = removed
            .iter()
            .filter_map(|id| executed.items.get(id))
            .collect();
        let mut reports = self.reports.lock().map_err(poison)?;
        for entry in reports.values_mut() {
            entry.items.retain(|_, item| {
                !removed.iter().any(|old| {
                    if old.item.kind == CleanupItemKind::Command {
                        item.item.reason == old.item.reason
                    } else {
                        Path::new(&item.item.path).starts_with(&old.item.path)
                            || Path::new(&old.item.path).starts_with(&item.item.path)
                    }
                })
            });
            reconcile(entry);
        }
        if let Some(entry) = reports.values().max_by_key(|r| r.report.scanned_at) {
            self.save_summary(&entry.report)?;
        }
        Ok(())
    }
}
async fn run_brew(
    queue: &Arc<TaskQueue>,
    task: &Task,
    engine: &Arc<Engine>,
    item: &EntryItem,
    flag: Arc<AtomicBool>,
) -> Result<Act, AppError> {
    if item.item.reason != CleanupReason::BrewCleanup
        || !matches!(item.action, ItemAction::BrewStep)
    {
        return Err(AppError::new("E_PROTECTED", "cleanup_command"));
    }
    if item.owner.status(engine).is_some() {
        return Ok(Act::Skipped(SkippedItemReason::InUse));
    }
    let settings = queue.settings()?;
    let brew = brew::locate::locate(&settings).await?;
    // Re-estimate immediately before the fixed command. A newly protected path suppresses the entire command.
    let estimate = crate::maintenance::run(
        &brew,
        &settings,
        brew::args::read_args(brew::args::ReadOp::CleanupEstimate, None)?,
    )
    .await?;
    estimate.success()?;
    let estimate = crate::maintenance::cleanup(&estimate.text);
    let args = brew::args::write_args(TaskOp::Cleanup, None, &TaskOptions::default())?;
    let mut env = brew::env::construct(
        &settings,
        &brew.prefix,
        Path::new("/nonexistent/opennavo_cleanup_askpass"),
    );
    env.insert("NONINTERACTIVE".into(), "1".into());
    env.insert(
        "HOME".into(),
        engine.uninstall.roots.home.to_string_lossy().into(),
    );
    let request = pty::Execution {
        executable: brew.path.into(),
        args,
        env,
        log_path: task
            .log_path
            .as_ref()
            .ok_or_else(super::cached_error)?
            .into(),
        op: TaskOp::Cleanup,
        cancel: flag,
        grace: Duration::from_secs(3),
        cache_dir: None,
        download_total: None,
    };
    let engine = engine.clone();
    let item = item.clone();
    crate::blocking(move || {
        let _operation = engine.operation.lock().map_err(poison)?;
        let protected = engine.protected_list()?;
        if protect::entry_is_protected(&engine, &protected, &item)
            || (estimate.file_count as usize > estimate.items.len() && !protected.is_empty())
            || estimate.items.iter().any(|i| {
                protect::is_protected(&protected, Path::new(&i.path), None)
                    || (Path::new(&i.path).starts_with(&engine.uninstall.roots.home)
                        && rules::never_touch(&engine.uninstall.roots.home, Path::new(&i.path)))
            })
        {
            return Ok(Act::Skipped(SkippedItemReason::Protected));
        }
        if let Some(reason) = item.owner.status(&engine) {
            return Ok(Act::Skipped(if reason == SkippedGroupReason::Running {
                SkippedItemReason::Running
            } else {
                SkippedItemReason::InUse
            }));
        }
        let result = pty::execute(request, |_, _| {})?;
        if result.canceled {
            return Ok(Act::Skipped(SkippedItemReason::InUse));
        }
        if result.code != 0 {
            return Err(AppError::new("E_BREW_CLEANUP", "brew_cleanup_failed"));
        }
        Ok(Act::Deleted(estimate.bytes))
    })
    .await
}
