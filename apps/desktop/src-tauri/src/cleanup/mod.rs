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
        Ok(entry)
    }
    pub fn enqueue(
        &self,
        queue: &TaskQueue,
        report_id: &str,
        ids: Vec<String>,
    ) -> Result<Task, AppError> {
        let _operation = self.operation.lock().map_err(poison)?;
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
fn summary(report: &CleanupReport) -> CleanupSummary {
    CleanupSummary {
        scanned_at: Some(report.scanned_at),
        volume: None,
        reclaimable_bytes: report
            .categories
            .iter()
            .filter(|c| c.risk != CleanupRisk::Personal)
            .map(|c| c.bytes)
            .sum(),
        suggested_bytes: report
            .categories
            .iter()
            .flat_map(|c| &c.groups)
            .flat_map(|g| &g.items)
            .filter(|i| i.selected)
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
pub(crate) fn reconcile(entry: &mut Entry) {
    for category in &mut entry.report.categories {
        for group in &mut category.groups {
            group.items.retain(|i| entry.items.contains_key(&i.id));
        }
        category.groups.retain(|g| !g.items.is_empty());
        category.bytes = category
            .groups
            .iter()
            .flat_map(|g| &g.items)
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
}
