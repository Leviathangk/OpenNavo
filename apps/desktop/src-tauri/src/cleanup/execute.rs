use super::{
    Engine, EntryItem,
    owner::{Activities, Activity},
    poison, protect, reconcile, rules, tree,
};
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

/// Progress events while an item runs (a large folder takes a while to empty), and at most this often in general.
const PROGRESS_INTERVAL: Duration = Duration::from_millis(200);
/// Progress weight of a tool command (Homebrew, Docker): roughly a few seconds of removing files.
const COMMAND_WEIGHT: u64 = 50_000;
/// Slack on top of 110% of what one item showed before its removal stops (#116).
pub const ITEM_SLACK_BYTES: u64 = 64 << 20;
/// Slack on top of 110% of the selected total before the whole run stops (#116).
pub const TOTAL_SLACK_BYTES: u64 = 256 << 20;
/// The most one item may free: shown × 1.1 + 64 MB. Shown size, re-measured size and freed bytes all use the same
/// metric (allocated blocks × 512 of single-link files), so APFS clones, whose shared blocks are counted once per
/// file, inflate all three alike and cannot trip the limit on their own.
pub fn item_limit(shown: u64) -> u64 {
    shown
        .saturating_add(shown / 10)
        .saturating_add(ITEM_SLACK_BYTES)
}
/// The most a whole run may free: selected total × 1.1 + 256 MB.
pub fn total_limit(shown: u64) -> u64 {
    shown
        .saturating_add(shown / 10)
        .saturating_add(TOTAL_SLACK_BYTES)
}
fn oversize(detail: String) -> AppError {
    AppError::new("E_CLEANUP_OVERSIZE", "cleanup_oversize").with_detail(detail)
}
fn size_unknown(detail: String) -> AppError {
    AppError::new("E_CLEANUP_SIZE_UNKNOWN", "cleanup_size_unknown").with_detail(detail)
}
/// The middle tier offers a `target` and its leaves. The UI keeps them mutually exclusive, but a crafted task could
/// contain both; keep the parent and drop the children so bytes are never counted twice.
fn prune_nested(entry: &super::Entry, ids: &[String]) -> Vec<String> {
    let selected: Vec<(&EntryItem, &String)> = ids
        .iter()
        .filter_map(|id| entry.items.get(id).map(|item| (item, id)))
        .collect();
    let mut kept = Vec::with_capacity(selected.len());
    for (item, id) in &selected {
        let path = item.item.path.as_str();
        if item.item.kind == CleanupItemKind::Command || path.is_empty() {
            kept.push((*id).clone());
            continue;
        }
        // Look at the whole selection, not only what came before: `[leaf, parent]` must drop the leaf too. The
        // caller's order is kept because removal order is observable (the abort test relies on it).
        let nested = selected.iter().any(|(parent, other)| {
            other != id
                && parent.item.kind != CleanupItemKind::Command
                && !parent.item.path.is_empty()
                && path != parent.item.path
                && Path::new(path).starts_with(&parent.item.path)
        });
        if !nested {
            kept.push((*id).clone());
        }
    }
    kept
}

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
    let ids = prune_nested(&entry, ids);
    let mut outcome = UninstallOutcome {
        freed_bytes: 0,
        trashed_bytes: 0,
        trashed: 0,
        deleted: 0,
        skipped: vec![],
        restorable: false,
        planned: ids
            .iter()
            .filter_map(|id| entry.items.get(id))
            .map(|item| CleanupPlannedItem {
                item_id: item.item.id.clone(),
                path: display(item).to_owned(),
                size_bytes: item.item.size_bytes,
                size_complete: item.item.size_complete,
            })
            .collect(),
    };
    // What the user saw is stored before anything is touched, so the task can be audited afterwards (#116).
    queue.store_uninstall_outcome(&task.id, &outcome)?;
    queue.uninstall_progress(
        &task.id,
        TaskPhase::Preparing,
        0,
        0.0,
        Some("==> OpenNavo: cleanup".into()),
    )?;
    // One reading of what runs and what is open serves the items of the next two seconds.
    let activities = Arc::new(Activities::default());
    let mut report = Report::new(&entry, &ids);
    let mut removed = vec![];
    let mut command_failure = None;
    // Re-measure the whole selection before the first removal: an item that is no longer fully measurable, or larger
    // than shown beyond the tolerance, stops the run with nothing removed.
    let shown_total = report.selected_bytes;
    let mut abort = {
        let (engine, entry, ids, flag, activities) = (
            engine.clone(),
            entry.clone(),
            ids.clone(),
            flag.clone(),
            activities.clone(),
        );
        let (lines, abort) =
            crate::blocking(move || engine.preflight(&entry, &ids, &flag, &activities)).await?;
        for line in lines {
            report.line(line);
        }
        abort
    };
    for id in &ids {
        if flag.load(Ordering::Acquire) || abort.is_some() {
            break;
        }
        // Each item may free its own limit, and never more than what is left of the run's limit.
        let budget = {
            let item = entry.items.get(id).ok_or_else(super::cached_error)?;
            item_limit(item.item.size_bytes.unwrap_or(0))
                .min(total_limit(shown_total).saturating_sub(outcome.freed_bytes))
        };
        let item = entry.items.get(id).ok_or_else(super::cached_error)?.clone();
        report.start(&item);
        let meter = Arc::new(tree::Meter::default());
        let work = async {
            if item.item.kind == CleanupItemKind::Command
                && super::docker::prune_args(item.item.reason).is_some()
            {
                let engine = engine.clone();
                let log_path = task.log_path.clone().ok_or_else(super::cached_error)?;
                let flag = flag.clone();
                let item = item.clone();
                crate::blocking(move || engine.docker(Path::new(&log_path), &item, flag)).await
            } else if item.item.kind == CleanupItemKind::Command {
                run_brew(&queue, task, &engine, &item, flag.clone(), &activities).await
            } else {
                let engine = engine.clone();
                let task_id = task.id.clone();
                let flag = flag.clone();
                let item = item.clone();
                let activities = activities.clone();
                let meter = meter.clone();
                crate::blocking(move || {
                    engine.act(&task_id, &item, &flag, &activities, &meter, budget)
                })
                .await
            }
        };
        let result = await_with_progress(work, &flag, || {
            report.emit(&queue, &task.id, &outcome, Some(&meter), false)
        })
        .await?;
        match result {
            Ok(Act::Deleted(bytes)) => {
                outcome.deleted += 1;
                outcome.freed_bytes = outcome.freed_bytes.saturating_add(bytes);
                removed.push(id.clone());
                report.line(format!(
                    "deleted {} ({bytes} bytes, shown {} bytes)",
                    display(&item),
                    item.item.size_bytes.unwrap_or(0)
                ));
            }
            Ok(Act::Trashed) => {
                let bytes = item.item.size_bytes.unwrap_or(0);
                outcome.freed_bytes = outcome.freed_bytes.saturating_add(bytes);
                outcome.trashed_bytes = outcome.trashed_bytes.saturating_add(bytes);
                outcome.trashed += 1;
                outcome.restorable = true;
                removed.push(id.clone());
                report.line(format!("trashed {} ({bytes} bytes)", display(&item)));
            }
            Ok(Act::Skipped(reason)) => skip(&mut report, &mut outcome, &item, reason),
            Err(error) => {
                // A removal that stops part-way has already unlinked files: they are counted in the outcome, the log
                // and the result page like any other freed byte, so an aborted run reports what was really removed
                // instead of under-reporting it (#117).
                let freed = meter.bytes.load(Ordering::Relaxed);
                if freed > 0 {
                    outcome.freed_bytes = outcome.freed_bytes.saturating_add(freed);
                    report.line(format!(
                        "freed {freed} bytes before {} stopped ({})",
                        display(&item),
                        error.code
                    ));
                }
                if error.code == "E_BREW_CLEANUP" {
                    command_failure = Some(error.clone());
                }
                if error.code == "E_CLEANUP_OVERSIZE" {
                    report.line(format!(
                        "abort: {} reached its limit of {budget} bytes (shown {} bytes, freed {freed} bytes); cleanup stopped",
                        display(&item),
                        item.item.size_bytes.unwrap_or(0)
                    ));
                    abort = Some(error.clone());
                }
                let reason = match error.code.as_str() {
                    "E_PROTECTED" => SkippedItemReason::Protected,
                    "E_DOCKER_PRUNE" | "E_DOCKER_UNAVAILABLE" => SkippedItemReason::ToolUnavailable,
                    "E_PLAN_EXPIRED" | "E_CLEANUP_OVERSIZE" | "E_CLEANUP_SIZE_UNKNOWN" => {
                        SkippedItemReason::Changed
                    }
                    "E_NOT_FOUND" => SkippedItemReason::Missing,
                    "E_APP_RUNNING" | "E_CANCELED" => SkippedItemReason::InUse,
                    _ => SkippedItemReason::Permission,
                };
                skip(&mut report, &mut outcome, &item, reason);
            }
        }
        report.done();
        if abort.is_none() && outcome.freed_bytes > total_limit(shown_total) {
            report.line(format!(
                "abort: {} bytes freed, over the limit of {} bytes for {shown_total} bytes shown; cleanup stopped",
                outcome.freed_bytes,
                total_limit(shown_total)
            ));
            abort = Some(oversize(format!(
                "freed {} of {shown_total} shown",
                outcome.freed_bytes
            )));
        }
        // Persisted per item; the next progress event carries it to the window.
        queue.store_uninstall_outcome(&task.id, &outcome)?;
        report.emit(&queue, &task.id, &outcome, None, false)?;
    }
    // Include the unattempted selection when a task is canceled so the result accounts for every selected item.
    for id in &ids {
        if !removed.contains(id) && !outcome.skipped.iter().any(|s| &s.item_id == id) {
            let item = &entry.items[id];
            let reason = if abort.is_some() {
                SkippedItemReason::Changed
            } else {
                SkippedItemReason::InUse
            };
            skip(&mut report, &mut outcome, item, reason);
        }
    }
    outcome.restorable = manifest::entries(&engine.uninstall.database, &task.id)?
        .iter()
        .any(|e| e.state == "trash" || e.state == "pending");
    report.emit(&queue, &task.id, &outcome, None, true)?;
    queue.set_uninstall_outcome(&task.id, outcome.clone())?;
    engine.executed(&removed, &entry)?;
    report.finish(&queue, &task.id, &outcome)?;
    if let Some(error) = abort {
        return Err(error);
    }
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
/// Reporting must not detach blocking deletion work from the queue's cancellation control.
async fn await_with_progress<T>(
    work: impl std::future::Future<Output = T>,
    flag: &AtomicBool,
    mut emit: impl FnMut() -> Result<(), AppError>,
) -> Result<T, AppError> {
    tokio::pin!(work);
    let mut ticker = tokio::time::interval_at(
        tokio::time::Instant::now() + PROGRESS_INTERVAL,
        PROGRESS_INTERVAL,
    );
    loop {
        tokio::select! {
            result = &mut work => return Ok(result),
            _ = ticker.tick() => {
                if let Err(error) = emit() {
                    flag.store(true, Ordering::Release);
                    let _ = work.await;
                    return Err(error);
                }
            }
        }
    }
}

/// The path still names the object that was scanned: it is its own canonical form (no symbolic link on the way, no
/// `.` or `..`), it is not a link itself, and device and inode are unchanged.
pub(crate) fn same_identity(path: &Path, scanned: &uninstall::rules::Snapshot) -> bool {
    use std::os::unix::fs::MetadataExt;
    let Ok(metadata) = std::fs::symlink_metadata(path) else {
        return false;
    };
    !metadata.file_type().is_symlink()
        && std::fs::canonicalize(path).is_ok_and(|canonical| canonical == path)
        && metadata.dev() == scanned.device
        && metadata.ino() == scanned.inode
}
impl Engine {
    /// Measures every selected path item again, with a fresh time budget per item. Returns log lines and, when the
    /// run must not start, the reason: a size that cannot be fully measured, one item over `item_limit`, or the
    /// total over `total_limit`. Tool commands re-check their own estimate right before they run.
    pub fn preflight(
        &self,
        entry: &super::Entry,
        ids: &[String],
        flag: &AtomicBool,
        activities: &Activities,
    ) -> Result<(Vec<String>, Option<AppError>), AppError> {
        let mut lines = vec![];
        let (mut shown, mut measured) = (0u64, 0u64);
        for id in ids {
            let item = entry.items.get(id).ok_or_else(super::cached_error)?;
            let size = item.item.size_bytes.unwrap_or(0);
            if !item.item.size_complete {
                lines.push(format!(
                    "abort: the size of {} was not fully measured; nothing was removed",
                    display(item)
                ));
                return Ok((lines, Some(size_unknown(display(item).to_owned()))));
            }
            shown = shown.saturating_add(size);
            if item.item.kind == CleanupItemKind::Command
                || item.item.delete_mode != DeleteMode::Delete
            {
                measured = measured.saturating_add(size);
                continue;
            }
            let path = Path::new(&item.item.path);
            if std::fs::symlink_metadata(path).is_err() {
                continue;
            }
            // The removal keeps files that are open somewhere; measuring them here would report a size the removal
            // can never reach and stop the whole run with nothing removed (#117).
            let reading = activities.get(self);
            let open: Vec<_> = reading
                .as_ref()
                .as_ref()
                .ok()
                .map(|activity| {
                    activity
                        .open_under(path)
                        .map(|(open, _)| open.clone())
                        .collect()
                })
                .unwrap_or_default();
            let now = match tree::estimate(
                path,
                flag,
                Instant::now() + self.category_timeout,
                self.max_tree_nodes,
                |_| false,
                &open,
            ) {
                Ok(tree) => tree,
                Err(error) if error.code == "E_CANCELED" => return Err(error),
                // Refused (a link, another owner, a changed root): the item itself is skipped as changed.
                Err(_) => continue,
            };
            if now.incomplete.is_some() {
                lines.push(format!(
                    "abort: {} could not be measured again in full; nothing was removed",
                    display(item)
                ));
                return Ok((lines, Some(size_unknown(display(item).to_owned()))));
            }
            if now.bytes > item_limit(size) {
                lines.push(format!(
                    "abort: {} now measures {} bytes, shown {size} bytes (limit {}); nothing was removed",
                    display(item),
                    now.bytes,
                    item_limit(size)
                ));
                return Ok((
                    lines,
                    Some(oversize(format!(
                        "{} {} > {size}",
                        display(item),
                        now.bytes
                    ))),
                ));
            }
            measured = measured.saturating_add(now.bytes);
        }
        if measured > total_limit(shown) {
            lines.push(format!(
                "abort: the selection now measures {measured} bytes, shown {shown} bytes (limit {}); nothing was removed",
                total_limit(shown)
            ));
            return Ok((lines, Some(oversize(format!("{measured} > {shown}")))));
        }
        lines.push(format!(
            "checked: {shown} bytes shown, {measured} bytes measured again, limit {}",
            total_limit(shown)
        ));
        Ok((lines, None))
    }
}
fn display(item: &EntryItem) -> &str {
    item.item.command.as_deref().unwrap_or(&item.item.path)
}
fn skip(
    report: &mut Report,
    outcome: &mut UninstallOutcome,
    item: &EntryItem,
    reason: SkippedItemReason,
) {
    outcome.skipped.push(SkippedItem {
        item_id: item.item.id.clone(),
        path: item.item.path.clone(),
        reason,
    });
    report.line(format!("skipped {:?} {}", reason, display(item)));
}
/// How much of an item's run is spent: removing takes time per entry, not per byte, and a tool command takes seconds.
fn weight(item: &EntryItem) -> u64 {
    if item.item.kind == CleanupItemKind::Command {
        COMMAND_WEIGHT
    } else {
        item.tree.as_ref().map_or(1, |tree| tree.entries.max(1))
    }
}
/// Progress of one run. The bar follows entries removed; the numbers are bytes freed out of the selected size. Log
/// lines and events are batched to a few per second.
struct Report {
    total: u64,
    done: u64,
    current: u64,
    selected_bytes: u64,
    lines: Vec<String>,
    last: Option<Instant>,
}
impl Report {
    fn new(entry: &super::Entry, ids: &[String]) -> Self {
        let items: Vec<_> = ids.iter().filter_map(|id| entry.items.get(id)).collect();
        Self {
            total: items
                .iter()
                .map(|item| weight(item))
                .fold(0, u64::saturating_add),
            done: 0,
            current: 0,
            selected_bytes: items
                .iter()
                .filter_map(|item| item.item.size_bytes)
                .fold(0, u64::saturating_add),
            lines: vec![],
            last: None,
        }
    }
    fn start(&mut self, item: &EntryItem) {
        self.current = weight(item);
    }
    fn done(&mut self) {
        self.done = self.done.saturating_add(self.current);
        self.current = 0;
    }
    fn line(&mut self, line: String) {
        self.lines.push(line);
    }
    fn emit(
        &mut self,
        queue: &TaskQueue,
        task: &str,
        outcome: &UninstallOutcome,
        meter: Option<&tree::Meter>,
        force: bool,
    ) -> Result<(), AppError> {
        if !force
            && self
                .last
                .is_some_and(|last| last.elapsed() < PROGRESS_INTERVAL)
        {
            return Ok(());
        }
        let removing = meter.map_or(0, |m| m.entries.load(Ordering::Relaxed).min(self.current));
        let percent = (self.done.saturating_add(removing) as f64 / self.total.max(1) as f64
            * 100.0)
            .min(99.9);
        let freed = outcome
            .freed_bytes
            .saturating_add(meter.map_or(0, |m| m.bytes.load(Ordering::Relaxed)));
        queue.cleanup_progress(
            task,
            TaskPhase::Cleaning,
            percent as f32,
            (freed.min(self.selected_bytes), self.selected_bytes),
            std::mem::take(&mut self.lines),
        )?;
        self.last = Some(Instant::now());
        Ok(())
    }
    fn finish(
        &mut self,
        queue: &TaskQueue,
        task: &str,
        outcome: &UninstallOutcome,
    ) -> Result<(), AppError> {
        queue.cleanup_progress(
            task,
            TaskPhase::Finishing,
            100.0,
            (
                outcome.freed_bytes.min(self.selected_bytes),
                self.selected_bytes,
            ),
            std::mem::take(&mut self.lines),
        )
    }
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
        // Docker reports more reclaimable space than was shown: the prune would remove more than the user saw.
        if before > item_limit(item.item.size_bytes.unwrap_or(0)) {
            return Ok(Act::Skipped(SkippedItemReason::Changed));
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
    #[allow(clippy::too_many_arguments)]
    fn delete_cache(
        &self,
        item: &EntryItem,
        snapshot: &uninstall::rules::Snapshot,
        flag: &AtomicBool,
        partial: bool,
        activity: &Result<Activity, AppError>,
        meter: &tree::Meter,
        budget: u64,
    ) -> Result<Act, AppError> {
        let path = Path::new(&item.item.path);
        if let Some(reason) = item.owner.status_in(activity) {
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
            .map(|a| a.open_under(path).map(|(p, _)| p.clone()).collect())
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
            tree::parallel_workers(item.tree.as_ref().is_some_and(|tree| tree.compact)),
            meter,
            budget,
        )?;
        if bytes == 0 && !open.is_empty() {
            return Ok(Act::Skipped(SkippedItemReason::InUse));
        }
        Ok(Act::Deleted(bytes))
    }
    pub fn act(
        &self,
        task_id: &str,
        item: &EntryItem,
        flag: &AtomicBool,
        activities: &Activities,
        meter: &tree::Meter,
        budget: u64,
    ) -> Result<Act, AppError> {
        let _operation = self.operation.lock().map_err(poison)?;
        let path = Path::new(&item.item.path);
        // A size that was only a lower bound is never acted on (#116); the scan does not offer it either.
        if !item.item.size_complete {
            return Err(size_unknown(item.item.path.clone()));
        }
        // Read-only rows (AI history) are information, not candidates: refuse them even if a client selected one.
        if item.item.restore == super::types::CleanupRestore::Never {
            return Ok(Act::Skipped(SkippedItemReason::Protected));
        }
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
        let reading = activities.get(self);
        let activity = &*reading;
        if let Some(reason) = item.owner.status_in(activity) {
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
        // Exactly the folder or file the user saw: same canonical path (no link anywhere on the way) and the same
        // device and inode as at scan time.
        let root_snapshot = before.snapshots.get(path).ok_or_else(super::cached_error)?;
        if !same_identity(path, root_snapshot) {
            return Ok(Act::Skipped(SkippedItemReason::Changed));
        }
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
                for (p, expected) in &before.snapshots {
                    // Open here, below or above: that part stays and may change.
                    if activity
                        .as_ref()
                        .is_ok_and(|a| a.in_use(p) || a.open_at_or_above(p))
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
            return self.delete_cache(item, snapshot, flag, partial, activity, meter, budget);
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
            // What the service ran is looked at again, not as it was before unloading.
            activities.forget();
        }
        let reading = activities.get(self);
        if flag.load(Ordering::Acquire) || item.owner.status_in(&reading).is_some() {
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
                return self.delete_cache(item, snapshot, flag, partial, &reading, meter, budget);
            }
            if current.bytes > budget {
                return Err(oversize(format!(
                    "{} measures {} bytes, limit {budget}",
                    item.item.path, current.bytes
                )));
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
    activities: &Arc<Activities>,
) -> Result<Act, AppError> {
    if item.item.reason != CleanupReason::BrewCleanup
        || !matches!(item.action, ItemAction::BrewStep)
    {
        return Err(AppError::new("E_PROTECTED", "cleanup_command"));
    }
    let status = {
        let (engine, activities, owner) = (engine.clone(), activities.clone(), item.owner.clone());
        crate::blocking(move || Ok(owner.status_in(&activities.get(&engine)))).await?
    };
    if status.is_some() {
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
    if estimate.bytes > item_limit(item.item.size_bytes.unwrap_or(0)) {
        return Ok(Act::Skipped(SkippedItemReason::Changed));
    }
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
    let activities = activities.clone();
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
        if let Some(reason) = item.owner.status_in(&activities.get(&engine)) {
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

#[cfg(test)]
mod progress_tests {
    use super::*;

    #[tokio::test]
    async fn reporting_failure_cancels_and_joins_blocking_work() {
        let flag = Arc::new(AtomicBool::new(false));
        let finished = Arc::new(AtomicBool::new(false));
        let (started_tx, started_rx) = tokio::sync::oneshot::channel();
        let worker_flag = flag.clone();
        let worker_finished = finished.clone();
        let work = tokio::task::spawn_blocking(move || {
            started_tx.send(()).unwrap();
            let deadline = Instant::now() + Duration::from_secs(5);
            while !worker_flag.load(Ordering::Acquire) && Instant::now() < deadline {
                std::thread::sleep(Duration::from_millis(1));
            }
            assert!(worker_flag.load(Ordering::Acquire));
            // Cancellation is observed before the worker has finished unwinding its work.
            std::thread::sleep(Duration::from_millis(50));
            worker_finished.store(true, Ordering::Release);
        });
        started_rx.await.unwrap();
        let error = await_with_progress(work, &flag, || {
            Err(AppError::new("E_TEST_REPORT", "progress_write_failed"))
        })
        .await
        .unwrap_err();
        assert_eq!(error.code, "E_TEST_REPORT");
        assert!(flag.load(Ordering::Acquire));
        assert!(finished.load(Ordering::Acquire));
    }
}

#[cfg(test)]
mod prune_tests {
    use super::*;
    use std::collections::BTreeMap;
    fn entry_item(id: &str, path: &str) -> EntryItem {
        EntryItem {
            item: CleanupItem {
                id: id.into(),
                path: path.into(),
                kind: CleanupItemKind::Dir,
                command: None,
                size_bytes: Some(10),
                size_complete: true,
                kept_open_files: 0,
                modified_at: None,
                reason: CleanupReason::CacheDir,
                restore: CleanupRestore::Auto,
                selected: true,
                delete_mode: DeleteMode::Delete,
                needs_admin: false,
                note: None,
            },
            owner: crate::cleanup::owner::Owner::default(),
            title: id.into(),
            tree: None,
            action: ItemAction::Path {
                empty_only: false,
                bootout: None,
            },
            docker_endpoint: None,
        }
    }
    fn entry(items: Vec<EntryItem>) -> super::super::Entry {
        super::super::Entry {
            category_millis: BTreeMap::new(),
            report: CleanupReport {
                report_id: "prune".into(),
                scanned_at: 0,
                expires_at: 0,
                volume: CleanupVolume {
                    name: String::new(),
                    total_bytes: 0,
                    free_bytes: 0,
                },
                categories: vec![],
                incomplete: vec![],
            },
            items: items
                .into_iter()
                .map(|item| (item.item.id.clone(), item))
                .collect(),
        }
    }
    /// The child must be dropped even when it is listed before its parent.
    #[test]
    fn nested_items_are_pruned_in_either_order() {
        let entry = entry(vec![
            entry_item("parent", "/p/target"),
            entry_item("leaf", "/p/target/debug/incremental"),
        ]);
        for ids in [
            vec!["leaf".to_string(), "parent".to_string()],
            vec!["parent".to_string(), "leaf".to_string()],
        ] {
            assert_eq!(prune_nested(&entry, &ids), vec!["parent".to_string()]);
        }
    }
}
