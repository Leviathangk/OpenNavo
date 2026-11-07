pub mod types;
pub use types::*;
mod collect;
pub mod docker;
pub mod execute;
pub mod owner;
mod progress;
mod protect;
pub mod rules;
mod tree;
mod walk;
use crate::{
    AppError,
    events::CoreEvent,
    model::*,
    queue::TaskQueue,
    uninstall::{self, ItemAction},
};
use std::{
    collections::{BTreeMap, HashMap},
    path::Path,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};

#[derive(Debug, Clone)]
pub struct EntryItem {
    pub item: CleanupItem,
    pub owner: owner::Owner,
    pub title: String,
    pub tree: Option<tree::Tree>,
    pub action: ItemAction,
    pub docker_endpoint: Option<docker::Endpoint>,
}
#[derive(Debug, Clone)]
pub struct Entry {
    pub category_millis: BTreeMap<CleanupCategory, u64>,
    pub report: CleanupReport,
    pub items: BTreeMap<String, EntryItem>,
    /// A short-lived plan derived from a scan (the Trash-only retry of skipped items) rather than a scan of the
    /// disk. It must never stand in for the latest scan when the stored summary is written.
    pub derived: bool,
}
pub struct Engine {
    pub uninstall: Arc<uninstall::Engine>,
    pub processes: Arc<dyn owner::ProcessProbe>,
    pub category_timeout: Duration,
    pub max_tree_nodes: usize,
    pub(crate) scan_slots: std::sync::Mutex<usize>,
    pub(crate) scan_ready: std::sync::Condvar,
    pub(crate) reports: Mutex<HashMap<String, Entry>>,
    requests: Mutex<HashMap<String, (Arc<AtomicBool>, String)>>,
    pub(crate) operation: Mutex<()>,
    consumed: Mutex<std::collections::HashSet<String>>,
}
impl Engine {
    /// IPC-facing scan boundary; internal collector cancellation may still use E_CANCELED.
    pub async fn scan_for_command(
        self: &Arc<Self>,
        request: String,
        categories: Option<Vec<CleanupCategory>>,
        permissions: Permissions,
        core: Option<Arc<crate::core::DesktopCore>>,
    ) -> Result<CleanupReport, AppError> {
        self.scan(request, categories, permissions, core)
            .await
            .map_err(|mut error| {
                if error.code == "E_CANCELED" {
                    error.code = "E_SCAN_CANCELED".into();
                }
                error
            })
    }
    pub fn new(uninstall: Arc<uninstall::Engine>) -> Arc<Self> {
        Self::with_processes(
            uninstall,
            Arc::new(owner::NativeProcesses),
            Duration::from_secs(100),
        )
    }
    pub fn with_processes(
        uninstall: Arc<uninstall::Engine>,
        processes: Arc<dyn owner::ProcessProbe>,
        category_timeout: Duration,
    ) -> Arc<Self> {
        Self::with_limits(uninstall, processes, category_timeout, 2_000_000)
    }
    /// Injectable traversal ceiling for deterministic synthetic limit tests.
    pub fn with_limits(
        uninstall: Arc<uninstall::Engine>,
        processes: Arc<dyn owner::ProcessProbe>,
        category_timeout: Duration,
        max_tree_nodes: usize,
    ) -> Arc<Self> {
        Arc::new(Self {
            max_tree_nodes,
            scan_slots: Mutex::new(4),
            scan_ready: std::sync::Condvar::new(),
            uninstall,
            processes,
            category_timeout,
            reports: Mutex::new(HashMap::new()),
            requests: Mutex::new(HashMap::new()),
            operation: Mutex::new(()),
            consumed: Mutex::new(Default::default()),
        })
    }
    pub fn cached(&self, id: &str, time: i64) -> Result<Entry, AppError> {
        self.reports
            .lock()
            .map_err(poison)?
            .get(id)
            .filter(|entry| entry.report.expires_at > time)
            .cloned()
            .ok_or_else(cached_error)
    }
    pub fn cancel(&self, request: &str) -> Result<(), AppError> {
        let request = self.requests.lock().map_err(poison)?.get(request).cloned();
        if let Some((flag, private)) = request {
            flag.store(true, Ordering::Release);
            self.uninstall.cancel(&private)?;
        }
        Ok(())
    }
    pub(crate) fn leftover_request(&self, request: &str) -> Result<String, AppError> {
        self.requests
            .lock()
            .map_err(poison)?
            .get(request)
            .map(|(_, private)| private.clone())
            .ok_or_else(cached_error)
    }

    pub fn summary(&self) -> Result<CleanupSummary, AppError> {
        let mut summary = self
            .uninstall
            .database
            .meta("cleanup_summary_v1")?
            .map(|raw| serde_json::from_str(&raw).map_err(AppError::from))
            .transpose()?
            .unwrap_or_else(CleanupSummary::default);
        summary.volume = collect::volume(Path::new("/")).ok();
        Ok(summary)
    }
    pub(crate) fn save_summary(&self, report: &CleanupReport) -> Result<(), AppError> {
        let mut summary = summary(report);
        let mut stored = serde_json::to_value(&summary)?;
        stored
            .as_object_mut()
            .expect("summary object")
            .remove("volume");
        self.uninstall
            .database
            .set_meta("cleanup_summary_v1", &serde_json::to_string(&stored)?)?;
        summary.volume = collect::volume(Path::new("/")).ok();
        (self.uninstall.sink)(CoreEvent::CleanupChanged(summary));
        Ok(())
    }
    pub fn validate_task(
        &self,
        target: Option<&TaskTarget>,
        options: &TaskOptions,
    ) -> Result<Entry, AppError> {
        let id = options.plan_id.as_deref().ok_or_else(cached_error)?;
        if target
            != Some(&TaskTarget {
                kind: TaskKind::App,
                token: id.into(),
            })
            || options.item_ids.as_ref().is_none_or(|ids| ids.is_empty())
            || *options
                != (TaskOptions {
                    plan_id: Some(id.into()),
                    item_ids: options.item_ids.clone(),
                    plan_digest: options.plan_digest.clone(),
                    ..Default::default()
                })
        {
            return Err(AppError::new("E_INVALID_ARG", "cleanup_options"));
        }
        let entry = self.cached(id, uninstall::now())?;
        let ids = options.item_ids.as_ref().ok_or_else(cached_error)?;
        let unique: std::collections::BTreeSet<_> = ids.iter().collect();
        if unique.len() != ids.len() || ids.iter().any(|id| !entry.items.contains_key(id)) {
            return Err(AppError::new("E_INVALID_ARG", "item_ids"));
        }
        // A size that is only a lower bound is never offered for removal (#116).
        if ids.iter().any(|id| !entry.items[id].item.size_complete) {
            return Err(AppError::new(
                "E_CLEANUP_SIZE_UNKNOWN",
                "cleanup_size_unknown",
            ));
        }
        // Checked when the task is queued and again when it starts, before anything is removed.
        if let Some(expected) = &options.plan_digest
            && *expected != selection_digest(&entry, ids)
        {
            return Err(crate::destructive::plan_changed());
        }
        Ok(entry)
    }
    /// Digest and window content of a cleanup the user is about to confirm: every selected item,
    /// in order, with its path, kind, action and size.
    pub fn confirm_plan(
        &self,
        report_id: &str,
        ids: &[String],
    ) -> Result<(String, crate::confirmation::McpConfirmView), AppError> {
        let entry = self.cached(report_id, uninstall::now())?;
        let unique: std::collections::BTreeSet<_> = ids.iter().collect();
        if ids.is_empty()
            || unique.len() != ids.len()
            || ids.iter().any(|id| !entry.items.contains_key(id))
        {
            return Err(AppError::new("E_INVALID_ARG", "item_ids"));
        }
        let mut bytes = 0u64;
        let mut permanent = false;
        let mut rows = Vec::new();
        for id in ids {
            let item = &entry.items[id];
            bytes = bytes.saturating_add(item.item.size_bytes.unwrap_or(0));
            permanent |= item.item.delete_mode == DeleteMode::Delete;
            if rows.len() < crate::destructive::MAX_LISTED_ITEMS {
                let target = match (&item.item.kind, &item.item.command) {
                    (CleanupItemKind::Command, Some(command)) => command.clone(),
                    _ => item.item.path.clone(),
                };
                let mut row = crate::confirmation::ConfirmItemView::row(&item.title, &target);
                // Groups Rust names by key (see collect.rs); the window shows them translated.
                if matches!(
                    item.title.as_str(),
                    "temp" | "trash" | "installers" | "logs"
                ) {
                    row.note = Some("cleanupGroup".into());
                }
                rows.push(row);
            }
        }
        let mut view = crate::confirmation::McpConfirmView::app_action(
            "cleanup",
            "",
            rows,
            ids.len(),
            Some(bytes),
        );
        // Items cleaned with "delete" never reach the Trash; the window says so.
        view.permanent = permanent;
        Ok((selection_digest(&entry, ids), view))
    }

    /// Create a short-lived cleanup plan for items that were skipped at execution time.
    /// These retries are deliberately Trash-only: the result page must never turn a skipped
    /// item into an irreversible delete. The normal confirmation window and execution checks
    /// still apply to the derived plan.
    pub fn prepare_trash_retry(&self, report_id: &str, ids: &[String]) -> Result<String, AppError> {
        let current = self.cached(report_id, uninstall::now())?;
        let unique: std::collections::BTreeSet<_> = ids.iter().collect();
        if ids.is_empty() || unique.len() != ids.len() {
            return Err(AppError::new("E_INVALID_ARG", "item_ids"));
        }
        // Tool commands have no path, and a worktree is removed by Git itself, which ignores the delete mode and
        // cannot be undone from the Trash.
        if ids.iter().any(|id| {
            current.items.get(id).is_none_or(|entry| {
                entry.item.kind == CleanupItemKind::Command
                    || entry.item.path.is_empty()
                    || entry.item.reason == CleanupReason::Worktree
            })
        }) {
            return Err(AppError::new("E_INVALID_ARG", "trash_retry_item"));
        }

        let retry_id = uuid::Uuid::now_v7().to_string();
        let wanted: std::collections::BTreeSet<_> = ids.iter().cloned().collect();
        let mut derived = current.clone();
        derived.report.report_id = retry_id.clone();
        derived.items.retain(|id, _| wanted.contains(id));
        if derived.items.len() != ids.len() {
            return Err(AppError::new("E_INVALID_ARG", "trash_retry_item"));
        }
        // The confirmation window, the digest and the executor all read `items`; the report's copy is only for
        // display, so both carry the Trash-only decision.
        derived
            .items
            .values_mut()
            .for_each(|entry| trash_only(&mut entry.item));
        for category in &mut derived.report.categories {
            for group in &mut category.groups {
                group.items.retain(|item| wanted.contains(&item.id));
                group.items.iter_mut().for_each(trash_only);
            }
        }
        // Drops the rows that are not part of this retry and recounts the category bytes.
        reconcile(&mut derived);
        derived.derived = true;
        self.reports
            .lock()
            .map_err(poison)?
            .insert(retry_id.clone(), derived);
        Ok(retry_id)
    }
    /// Queue a confirmed cleanup: under the same lock as [`Self::enqueue`], the selection must still
    /// have the digest the user allowed.
    pub fn enqueue_confirmed(
        &self,
        queue: &TaskQueue,
        report_id: &str,
        ids: Vec<String>,
        approved: &str,
    ) -> Result<Task, AppError> {
        let _operation = self.operation.lock().map_err(poison)?;
        let entry = self.cached(report_id, uninstall::now())?;
        if ids.iter().any(|id| !entry.items.contains_key(id))
            || selection_digest(&entry, &ids) != approved
        {
            return Err(crate::destructive::plan_changed());
        }
        self.enqueue_locked(queue, report_id, ids, approved.to_owned())
    }
    /// Unconfirmed entry kept `pub` only for the integration tests in `tests/` (a separate crate);
    /// no IPC command calls it. App commands use the `*_confirmed` path.
    #[doc(hidden)]
    pub fn enqueue(
        &self,
        queue: &TaskQueue,
        report_id: &str,
        ids: Vec<String>,
    ) -> Result<Task, AppError> {
        let _operation = self.operation.lock().map_err(poison)?;
        let entry = self.cached(report_id, uninstall::now())?;
        if ids.iter().any(|id| !entry.items.contains_key(id)) {
            return Err(AppError::new("E_INVALID_ARG", "item_ids"));
        }
        let digest = selection_digest(&entry, &ids);
        self.enqueue_locked(queue, report_id, ids, digest)
    }
    fn enqueue_locked(
        &self,
        queue: &TaskQueue,
        report_id: &str,
        ids: Vec<String>,
        digest: String,
    ) -> Result<Task, AppError> {
        let mut consumed = self.consumed.lock().map_err(poison)?;
        if consumed.contains(report_id) {
            return Err(cached_error());
        }
        let task = queue.enqueue(
            TaskOp::Clean,
            Some(TaskTarget {
                kind: TaskKind::App,
                token: report_id.into(),
            }),
            TaskOptions {
                plan_id: Some(report_id.into()),
                item_ids: Some(ids),
                plan_digest: Some(digest),
                ..Default::default()
            },
            TaskTrigger::Manual,
        )?;
        consumed.insert(report_id.into());
        Ok(task)
    }
    pub fn reveal(&self, report_id: &str, item_id: &str) -> Result<(), AppError> {
        let entry = self.cached(report_id, uninstall::now())?;
        let item = entry
            .items
            .get(item_id)
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "item_id"))?;
        if item.item.kind == CleanupItemKind::Command {
            return Err(AppError::new("E_INVALID_ARG", "command_reveal"));
        }
        uninstall::execute::reveal(Path::new(&item.item.path))
    }
    pub fn active_requests(&self) -> usize {
        self.requests.lock().map(|r| r.len()).unwrap_or(0)
    }
}
/// The cached scan the stored summary is rebuilt from: the newest one taken from the disk. A plan derived from a
/// scan (the Trash-only retry of skipped items) is ignored — it holds the same `scanned_at` and only a handful of
/// rows, so it would win the tie and write a summary that reports almost nothing as reclaimable.
pub(crate) fn latest_scan(reports: &HashMap<String, Entry>) -> Option<&Entry> {
    reports
        .values()
        .filter(|entry| !entry.derived)
        .max_by_key(|entry| entry.report.scanned_at)
}
/// A retry from the result page moves the item to the Trash whatever the scan had chosen for it, so the
/// confirmation window ("not through the Trash") and the executor agree.
fn trash_only(item: &mut CleanupItem) {
    item.delete_mode = DeleteMode::Trash;
    item.selected = true;
}
/// A middle-tier project lists a whole `target` and its leaves. Only one side can be selected, so totals must not
/// add a child to its parent.
fn disjoint<'a>(items: impl Iterator<Item = &'a CleanupItem>) -> Vec<&'a CleanupItem> {
    let mut sorted: Vec<&CleanupItem> = items.collect();
    sorted.sort_by_key(|item| item.path.len());
    let mut kept: Vec<&CleanupItem> = vec![];
    for item in sorted {
        // Tool commands (`brew cleanup`, Docker prune) have no path; treating "" as a parent would swallow every
        // other row.
        if item.kind == CleanupItemKind::Command || item.path.is_empty() {
            kept.push(item);
            continue;
        }
        let path = std::path::Path::new(&item.path);
        if kept.iter().any(|parent| {
            parent.kind != CleanupItemKind::Command
                && !parent.path.is_empty()
                && path != std::path::Path::new(&parent.path)
                && path.starts_with(&parent.path)
        }) {
            continue;
        }
        kept.push(item);
    }
    kept
}
fn summary(report: &CleanupReport) -> CleanupSummary {
    CleanupSummary {
        scanned_at: Some(report.scanned_at),
        volume: None,
        // Read-only rows (AI history) are shown in their category but are not space this app can reclaim.
        reclaimable_bytes: disjoint(
            report
                .categories
                .iter()
                .filter(|c| c.risk != CleanupRisk::Personal)
                .flat_map(|c| &c.groups)
                .flat_map(|g| &g.items)
                .filter(|i| i.restore != CleanupRestore::Never),
        )
        .iter()
        .filter_map(|i| i.size_bytes)
        .sum(),
        suggested_bytes: disjoint(
            report
                .categories
                .iter()
                .flat_map(|c| &c.groups)
                .flat_map(|g| &g.items)
                .filter(|i| i.selected),
        )
        .iter()
        .filter_map(|i| i.size_bytes)
        .sum(),
        active_background_items: report
            .categories
            .iter()
            .flat_map(|c| &c.groups)
            .flat_map(|g| &g.items)
            .filter(|i| i.reason == CleanupReason::OrphanService && i.selected)
            .count() as u64,
        categories: report
            .categories
            .iter()
            .map(|c| CleanupCategoryBytes {
                key: c.key,
                bytes: c.bytes,
            })
            .collect(),
    }
}
/// Digest of the items a cleanup removes, in order: the cached scan result of each (path, kind,
/// size, reason, action) and how it is removed.
pub(crate) fn selection_digest(entry: &Entry, ids: &[String]) -> String {
    let items: Vec<_> = ids
        .iter()
        .map(|id| {
            entry.items.get(id).map(|item| {
                (
                    id,
                    &item.item,
                    &item.title,
                    format!("{:?}", item.action),
                    format!("{:?}", item.docker_endpoint),
                )
            })
        })
        .collect();
    crate::destructive::digest(&("cleanup", &entry.report.report_id, items))
}
pub(crate) fn reconcile(entry: &mut Entry) {
    for category in &mut entry.report.categories {
        for group in &mut category.groups {
            group.items.retain(|i| entry.items.contains_key(&i.id));
        }
        category.groups.retain(|g| !g.items.is_empty());
        // A middle-tier project lists both `target` and its leaves; only the outermost row may count.
        category.bytes = disjoint(category.groups.iter().flat_map(|g| &g.items))
            .iter()
            .filter_map(|i| i.size_bytes)
            .sum();
    }
}
pub(crate) fn poison<T>(_: std::sync::PoisonError<T>) -> AppError {
    AppError::new("E_UNKNOWN", "cleanup_lock")
}
pub(crate) fn cached_error() -> AppError {
    AppError::new("E_PLAN_EXPIRED", "cleanup_report_expired")
}

#[cfg(test)]
mod expiry_tests {
    use super::*;
    #[test]
    fn execute_rejects_expired_report_before_enqueue() {
        let temp = tempfile::tempdir().unwrap();
        let db = crate::db::Database::open(&temp.path().join("db.sqlite")).unwrap();
        let queue = TaskQueue::new(
            db,
            Arc::new(std::sync::RwLock::new(Settings::default())),
            temp.path().join("logs"),
            temp.path().join("askpass"),
            Arc::new(|_| {}),
        )
        .unwrap();
        let engine = &queue.cleanup_engine;
        let report = CleanupReport {
            report_id: "expired".into(),
            scanned_at: uninstall::now() - 600_001,
            expires_at: uninstall::now() - 1,
            volume: CleanupVolume {
                name: "synthetic".into(),
                total_bytes: 0,
                free_bytes: 0,
            },
            categories: vec![],
            incomplete: vec![],
        };
        engine.reports.lock().unwrap().insert(
            report.report_id.clone(),
            Entry {
                category_millis: BTreeMap::new(),
                report,
                items: BTreeMap::new(),
                derived: false,
            },
        );
        assert_eq!(
            engine
                .enqueue(&queue, "expired", vec!["item".into()])
                .unwrap_err()
                .code,
            "E_PLAN_EXPIRED"
        );
        assert!(queue.active().unwrap().is_empty());
        assert!(queue.database.tasks().unwrap().is_empty());
    }

    fn queue_with_entry(temp: &tempfile::TempDir) -> std::sync::Arc<TaskQueue> {
        let db = crate::db::Database::open(&temp.path().join("db.sqlite")).unwrap();
        let queue = TaskQueue::new(
            db,
            Arc::new(std::sync::RwLock::new(Settings::default())),
            temp.path().join("logs"),
            temp.path().join("askpass"),
            Arc::new(|_| {}),
        )
        .unwrap();
        let item = |id: &str,
                    path: &str,
                    reason: CleanupReason,
                    restore: CleanupRestore,
                    mode: DeleteMode| {
            EntryItem {
                item: CleanupItem {
                    id: id.into(),
                    path: path.into(),
                    kind: CleanupItemKind::File,
                    command: None,
                    size_bytes: Some(10),
                    size_complete: true,
                    kept_open_files: 0,
                    modified_at: None,
                    reason,
                    restore,
                    selected: true,
                    delete_mode: mode,
                    needs_admin: false,
                    note: None,
                },
                owner: owner::Owner::default(),
                title: "Logs".into(),
                tree: None,
                action: ItemAction::Path {
                    empty_only: false,
                    bootout: None,
                },
                docker_endpoint: None,
            }
        };
        let report = CleanupReport {
            report_id: "report".into(),
            scanned_at: uninstall::now(),
            expires_at: uninstall::now() + 600_000,
            volume: CleanupVolume {
                name: "synthetic".into(),
                total_bytes: 0,
                free_bytes: 0,
            },
            categories: vec![],
            incomplete: vec![],
        };
        queue.cleanup_engine.reports.lock().unwrap().insert(
            "report".into(),
            Entry {
                category_millis: BTreeMap::new(),
                report,
                derived: false,
                items: BTreeMap::from([
                    (
                        "a".into(),
                        item(
                            "a",
                            "/Users/me/Library/Logs/a.log",
                            CleanupReason::LogFile,
                            CleanupRestore::Auto,
                            DeleteMode::Trash,
                        ),
                    ),
                    (
                        "b".into(),
                        item(
                            "b",
                            "/Users/me/Library/Logs/b.log",
                            CleanupReason::LogFile,
                            CleanupRestore::Auto,
                            DeleteMode::Trash,
                        ),
                    ),
                    // Removed for good by the scan's own decision; a retry must move it to the Trash instead.
                    (
                        "c".into(),
                        item(
                            "c",
                            "/Users/me/Library/Caches/tool/c",
                            CleanupReason::ToolCache,
                            CleanupRestore::Auto,
                            DeleteMode::Delete,
                        ),
                    ),
                    // A linked worktree is removed by Git: no Trash, nothing to put back.
                    (
                        "w".into(),
                        item(
                            "w",
                            "/Users/me/code/w",
                            CleanupReason::Worktree,
                            CleanupRestore::Irreversible,
                            DeleteMode::Delete,
                        ),
                    ),
                ]),
            },
        );
        queue
    }

    #[test]
    fn a_confirmed_cleanup_runs_only_the_allowed_list() {
        let temp = tempfile::tempdir().unwrap();
        let queue = queue_with_entry(&temp);
        let engine = &queue.cleanup_engine;
        let (digest, view) = engine.confirm_plan("report", &["a".into()]).unwrap();
        assert_eq!(view.origin, "app");
        assert_eq!(view.action, "cleanup");
        assert_eq!(view.items.len(), 1);
        assert_eq!(view.items[0].target, "/Users/me/Library/Logs/a.log");
        assert_eq!(
            (view.install_count, view.bytes, view.permanent),
            (1, Some(10), false)
        );
        // A different selection, or a forged digest, is not what the user allowed.
        for (ids, approved) in [
            (vec!["a".to_owned(), "b".to_owned()], digest.as_str()),
            (vec!["b".to_owned()], digest.as_str()),
            (vec!["a".to_owned()], "forged"),
        ] {
            assert_eq!(
                engine
                    .enqueue_confirmed(&queue, "report", ids, approved)
                    .unwrap_err()
                    .code,
                "E_PLAN_CHANGED"
            );
        }
        assert!(queue.database.tasks().unwrap().is_empty());
        let task = engine
            .enqueue_confirmed(&queue, "report", vec!["a".into()], &digest)
            .unwrap();
        assert_eq!(task.options.plan_digest.as_deref(), Some(digest.as_str()));
    }

    #[test]
    fn a_cleanup_whose_plan_changed_after_it_was_queued_does_not_start() {
        let temp = tempfile::tempdir().unwrap();
        let queue = queue_with_entry(&temp);
        let engine = &queue.cleanup_engine;
        let (digest, _) = engine.confirm_plan("report", &["a".into()]).unwrap();
        let task = engine
            .enqueue_confirmed(&queue, "report", vec!["a".into()], &digest)
            .unwrap();
        engine
            .validate_task(task.target.as_ref(), &task.options)
            .unwrap();
        // The cached item now points somewhere else: the task refuses before removing anything.
        engine
            .reports
            .lock()
            .unwrap()
            .get_mut("report")
            .unwrap()
            .items
            .get_mut("a")
            .unwrap()
            .item
            .path = "/Users/me/Documents".into();
        assert_eq!(
            engine
                .validate_task(task.target.as_ref(), &task.options)
                .unwrap_err()
                .code,
            "E_PLAN_CHANGED"
        );
        // A task carrying a digest the user never allowed is refused when queued.
        let mut forged = task.options.clone();
        forged.plan_digest = Some("forged".into());
        assert_eq!(
            engine
                .validate_task(task.target.as_ref(), &forged)
                .unwrap_err()
                .code,
            "E_PLAN_CHANGED"
        );
    }

    /// The retry of a skipped item must reach the Trash even where the scan had chosen a permanent delete: the
    /// confirmation window and the executor both read `items`, so the decision has to live there.
    #[test]
    fn a_trash_retry_plan_moves_a_permanent_item_to_the_trash() {
        let temp = tempfile::tempdir().unwrap();
        let queue = queue_with_entry(&temp);
        let engine = &queue.cleanup_engine;
        let retry = engine.prepare_trash_retry("report", &["c".into()]).unwrap();
        let entry = engine.cached(&retry, uninstall::now()).unwrap();
        assert!(entry.derived);
        assert_eq!(entry.items.len(), 1);
        assert_eq!(entry.items["c"].item.delete_mode, DeleteMode::Trash);
        let (digest, view) = engine.confirm_plan(&retry, &["c".into()]).unwrap();
        assert!(!view.permanent);
        assert_eq!((view.install_count, view.bytes), (1, Some(10)));
        // The digest of the derived plan binds the Trash decision, not the scan's permanent one.
        assert_ne!(
            digest,
            engine.confirm_plan("report", &["c".into()]).unwrap().0
        );
        let task = engine
            .enqueue_confirmed(&queue, &retry, vec!["c".into()], &digest)
            .unwrap();
        assert_eq!(task.options.plan_id.as_deref(), Some(retry.as_str()));
        // A derived plan is spent like any other plan.
        assert_eq!(
            engine
                .enqueue_confirmed(&queue, &retry, vec!["c".into()], &digest)
                .unwrap_err()
                .code,
            "E_PLAN_EXPIRED"
        );
    }

    /// Git removes a worktree itself, ignoring the delete mode, and a command has no path: neither can be
    /// retried through the Trash.
    #[test]
    fn a_worktree_or_a_missing_item_cannot_be_retried_to_the_trash() {
        let temp = tempfile::tempdir().unwrap();
        let queue = queue_with_entry(&temp);
        let engine = &queue.cleanup_engine;
        for ids in [
            vec!["w".to_owned()],
            vec!["missing".to_owned()],
            vec!["c".to_owned(), "c".to_owned()],
            vec![],
        ] {
            assert_eq!(
                engine.prepare_trash_retry("report", &ids).unwrap_err().code,
                "E_INVALID_ARG"
            );
        }
        assert_eq!(
            engine
                .prepare_trash_retry("expired", &["c".into()])
                .unwrap_err()
                .code,
            "E_PLAN_EXPIRED"
        );
    }

    /// A retry plan holds the scan's `scanned_at` and a handful of rows; it must not stand in for that scan when
    /// the stored summary is rebuilt.
    #[test]
    fn a_derived_plan_is_not_the_latest_scan() {
        let temp = tempfile::tempdir().unwrap();
        let queue = queue_with_entry(&temp);
        let engine = &queue.cleanup_engine;
        let retry = engine.prepare_trash_retry("report", &["c".into()]).unwrap();
        let reports = engine.reports.lock().unwrap();
        assert_eq!(
            reports[&retry].report.scanned_at,
            reports["report"].report.scanned_at
        );
        assert_eq!(
            super::latest_scan(&reports).map(|entry| entry.report.report_id.as_str()),
            Some("report")
        );
    }
}

#[cfg(test)]
mod disjoint_tests {
    use super::*;
    use std::collections::BTreeSet;
    fn item(id: &str, path: &str, kind: CleanupItemKind, bytes: u64) -> CleanupItem {
        CleanupItem {
            id: id.into(),
            path: path.into(),
            kind,
            command: None,
            size_bytes: Some(bytes),
            size_complete: true,
            kept_open_files: 0,
            modified_at: None,
            reason: CleanupReason::CacheDir,
            restore: CleanupRestore::Auto,
            selected: true,
            delete_mode: DeleteMode::Delete,
            needs_admin: false,
            note: None,
        }
    }
    /// A command item has no path; "" must not be treated as a parent that covers every other row.
    #[test]
    fn command_rows_and_children_do_not_swallow_others() {
        let items = [
            item("brew", "", CleanupItemKind::Command, 10),
            item("target", "/a/target", CleanupItemKind::Dir, 100),
            item(
                "leaf",
                "/a/target/debug/incremental",
                CleanupItemKind::Dir,
                40,
            ),
            item("other", "/b/cache", CleanupItemKind::Dir, 20),
        ];
        let kept: BTreeSet<&str> = disjoint(items.iter())
            .iter()
            .map(|i| i.id.as_str())
            .collect();
        assert_eq!(
            kept,
            BTreeSet::from(["brew", "target", "other"]),
            "keep the command row, drop only the child"
        );
    }
}
