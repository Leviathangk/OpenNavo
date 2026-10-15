use super::{
    Engine, now, poison,
    rules::{Snapshot, allocated_size_detail},
    scan::scan_plan,
};
use crate::{AppError, model::*};
use std::{
    collections::{BTreeMap, HashMap},
    path::Path,
    sync::{Arc, atomic::AtomicBool},
    time::Duration,
};
impl Engine {
    /// One app's footprint for the info sheet: a still valid cached value is returned as is.
    pub async fn footprint(
        self: &Arc<Self>,
        id: String,
        permissions: Permissions,
    ) -> Result<Footprint, AppError> {
        let revision = self.revision(&id)?;
        self.footprint_revision(id, permissions, revision, false)
            .await
    }
    /// `fresh` recomputes even when the cached value still matches the bundle: data and caches change while the
    /// bundle stays the same, which is what scheduled scans are for.
    async fn footprint_revision(
        self: &Arc<Self>,
        id: String,
        permissions: Permissions,
        revision: u64,
        fresh: bool,
    ) -> Result<Footprint, AppError> {
        let lock = self
            .footprint_locks
            .lock()
            .map_err(poison)?
            .entry(id.clone())
            .or_insert_with(|| Arc::new(tokio::sync::Mutex::new(())))
            .clone();
        let _lock = lock.lock().await;
        if self.revision(&id)? != revision {
            return Err(AppError::new("E_SCAN_CANCELED", "superseded"));
        }
        let engine = self.clone();
        crate::blocking(move || engine.list()).await?;
        let app = self.app(&id)?;
        let snapshot = Snapshot::capture(Path::new(&app.app.path))?;
        if !fresh
            && let Some(cached) = self.footprints.lock().map_err(poison)?.get(&id)
            && cached.value.complete
            && cached.snapshot == snapshot
            && cached.access == permissions.full_disk_access
        {
            return Ok(cached.value.clone());
        }
        let _slot = self.footprint_slots.acquire().await.map_err(poison)?;
        let access = permissions.full_disk_access;
        let mut rules = BTreeMap::new();
        if let Some(token) = app.app.cask.as_ref().or(app.app.catalog_token.as_ref())
            && let Ok(Some(raw)) = self.fetch_rule(token).await
        {
            rules.insert(id.clone(), raw);
        }
        let engine = self.clone();
        let cache_id = id.clone();
        let app_path = app.app.path.clone();
        let result = crate::blocking(move || {
            let cancel = AtomicBool::new(false);
            let entry = scan_plan(
                &engine,
                &format!("footprint-{id}"),
                vec![app.clone()],
                rules,
                permissions,
                &cancel,
            )?;
            // No time limit: large apps (Xcode) are measured completely in the background.
            let bundle = allocated_size_detail(Path::new(&app.app.path), None, Some(&cancel)).ok();
            let mut result = Footprint {
                app_bytes: bundle.map_or(0, |b| b.bytes),
                data_bytes: 0,
                cache_bytes: 0,
                complete: bundle.is_some_and(|b| !b.partial) && entry.plan.incomplete.is_empty(),
            };
            let items: Vec<_> = entry.plan.apps[0]
                .items
                .iter()
                .filter(|i| {
                    !i.required
                        && i.kind != PlanItemKind::Launchd
                        && i.confidence != PlanItemConfidence::Low
                        && i.shared_with.is_empty()
                })
                .collect();
            for item in &items {
                // Count a recursive directory once even when a zap rule also listed its children.
                if items.iter().any(|outer| {
                    outer.id != item.id
                        && outer.kind == PlanItemKind::Dir
                        && Path::new(&item.path).starts_with(&outer.path)
                }) {
                    continue;
                }
                result.complete &= item.size_bytes.is_some() && !entry.partial.contains(&item.id);
                if item.category == ItemCategory::Caches {
                    result.cache_bytes = result
                        .cache_bytes
                        .saturating_add(item.size_bytes.unwrap_or(0));
                } else {
                    result.data_bytes = result
                        .data_bytes
                        .saturating_add(item.size_bytes.unwrap_or(0));
                }
            }
            Ok::<_, AppError>(result)
        })
        .await?;
        // Discard results if an app was replaced/removed while its scan ran.
        if !snapshot.matches(Path::new(&app_path)) || self.app(&cache_id).is_err() {
            return Err(AppError::new("E_PLAN_EXPIRED", "app_changed"));
        }
        {
            let jobs = self.footprint_jobs.lock().map_err(poison)?;
            if jobs.revisions.get(&cache_id).copied().unwrap_or(0) != revision {
                return Err(AppError::new("E_SCAN_CANCELED", "superseded"));
            }
            let mut cache = self.footprints.lock().map_err(poison)?;
            cache.insert(
                cache_id,
                CachedFootprint {
                    time: now(),
                    value: result.clone(),
                    snapshot,
                    access,
                },
            );
            self.database
                .set_meta("footprints_v1", &serde_json::to_string(&*cache)?)?;
        }
        self.footprint_changed();
        Ok(result)
    }
}

#[derive(Clone, serde::Serialize, serde::Deserialize)]
pub(crate) struct CachedFootprint {
    pub time: i64,
    pub value: Footprint,
    pub snapshot: Snapshot,
    pub access: PermissionState,
}

#[derive(Clone, Copy, PartialEq)]
pub(crate) enum JobState {
    Pending,
    Retrying,
    Complete,
    Failed,
}

#[derive(Default)]
pub(crate) struct FootprintJobs {
    pub(crate) revisions: HashMap<String, u64>,
    pub(crate) errors: HashMap<String, AppError>,
    pub(crate) states: HashMap<String, JobState>,
    last_scan: Option<i64>,
}
impl Engine {
    fn revision(&self, id: &str) -> Result<u64, AppError> {
        Ok(self
            .footprint_jobs
            .lock()
            .map_err(poison)?
            .revisions
            .get(id)
            .copied()
            .unwrap_or(0))
    }
    pub fn footprint_values(&self) -> Result<HashMap<String, Footprint>, AppError> {
        let cache = self.footprints.lock().map_err(poison)?;
        let apps = self.apps.lock().map_err(poison)?;
        Ok(cache
            .iter()
            .filter(|(id, _)| apps.contains_key(*id))
            .map(|(id, cached)| (id.clone(), cached.value.clone()))
            .collect())
    }
    pub fn footprint_report(&self) -> Result<FootprintReport, AppError> {
        let (errors, progress) = {
            let jobs = self.footprint_jobs.lock().map_err(poison)?;
            let total = jobs.states.len() as u32;
            let completed = jobs
                .states
                .values()
                .filter(|s| matches!(s, JobState::Complete | JobState::Failed))
                .count() as u32;
            let progress = (total > 0).then(|| FootprintProgress {
                total,
                completed,
                failed: jobs
                    .states
                    .values()
                    .filter(|s| **s == JobState::Failed)
                    .count() as u32,
                retrying: jobs
                    .states
                    .values()
                    .filter(|s| **s == JobState::Retrying)
                    .count() as u32,
                running: completed < total,
            });
            (jobs.errors.clone(), progress)
        };
        Ok(FootprintReport {
            progress,
            values: self.footprint_values()?,
            errors,
        })
    }
    /// Page entry is the only automatic full-scan trigger; persist its one-hour gate. Between full scans, apps
    /// without a value (newly installed) or whose value was computed under different Full Disk Access are still
    /// measured, so granting access corrects the incomplete numbers right away.
    pub fn scan_footprints(
        self: &Arc<Self>,
        permissions: Permissions,
        force: bool,
    ) -> Result<bool, AppError> {
        let current = now();
        let full = {
            let mut jobs = self.footprint_jobs.lock().map_err(poison)?;
            let last = jobs.last_scan.or_else(|| {
                self.database
                    .meta("footprints_scan_at")
                    .ok()
                    .flatten()
                    .and_then(|s| s.parse().ok())
            });
            let full = force || last.is_none_or(|last| current - last >= 3_600_000);
            if full {
                jobs.last_scan = Some(current);
                self.database
                    .set_meta("footprints_scan_at", &current.to_string())?;
            }
            full
        };
        let ids: Vec<String> = {
            // Shared lock order: jobs -> footprints -> apps (also used by writers/readers).
            let jobs = self.footprint_jobs.lock().map_err(poison)?;
            let cache = self.footprints.lock().map_err(poison)?;
            let apps = self.apps.lock().map_err(poison)?;
            apps.keys()
                .filter(|id| {
                    full || !matches!(
                        jobs.states.get(*id),
                        Some(JobState::Pending | JobState::Retrying)
                    ) && cache
                        .get(*id)
                        .is_none_or(|cached| cached.access != permissions.full_disk_access)
                })
                .cloned()
                .collect()
        };
        if ids.is_empty() {
            return Ok(false);
        }
        // A full scan replaces the previous batch; target rescans merge into an active batch.
        if full {
            self.footprint_jobs.lock().map_err(poison)?.states.clear();
        }
        self.schedule_footprints(ids, permissions)?;
        Ok(true)
    }
    pub fn schedule_footprints(
        self: &Arc<Self>,
        ids: Vec<String>,
        permissions: Permissions,
    ) -> Result<(), AppError> {
        self.schedule_with_delays(
            ids,
            permissions,
            [Duration::from_secs(30), Duration::from_secs(120)],
        )
    }
    /// Values already shown stay until the new ones arrive. Only errors are retried: an incomplete value is a
    /// result (without Full Disk Access retrying cannot help), and an app that is gone is simply dropped.
    fn schedule_with_delays(
        self: &Arc<Self>,
        ids: Vec<String>,
        permissions: Permissions,
        delays: [Duration; 2],
    ) -> Result<(), AppError> {
        let mut jobs = self.footprint_jobs.lock().map_err(poison)?;
        if jobs
            .states
            .values()
            .all(|s| matches!(s, JobState::Complete | JobState::Failed))
        {
            jobs.states.clear();
        }
        for id in ids {
            let revision = self
                .footprint_sequence
                .fetch_add(1, std::sync::atomic::Ordering::AcqRel)
                + 1;
            jobs.revisions.insert(id.clone(), revision);
            jobs.errors.remove(&id);
            jobs.states.insert(id.clone(), JobState::Pending);
            let weak = Arc::downgrade(self);
            tauri::async_runtime::spawn(async move {
                for attempt in 0..3 {
                    if attempt > 0 {
                        tokio::time::sleep(delays[attempt - 1]).await;
                    }
                    let Some(engine) = weak.upgrade() else {
                        return;
                    };
                    if engine.revision(&id).ok() != Some(revision) {
                        return;
                    }
                    if attempt > 0 {
                        engine.set_footprint_state(&id, revision, JobState::Pending);
                    }
                    let error = match engine
                        .footprint_revision(id.clone(), permissions, revision, true)
                        .await
                    {
                        Ok(_) => {
                            engine.set_footprint_state(&id, revision, JobState::Complete);
                            return;
                        }
                        Err(error) => error,
                    };
                    if engine.revision(&id).ok() != Some(revision) {
                        return;
                    }
                    if error.code == "E_NOT_FOUND" {
                        engine.forget_footprint_job(&id, revision);
                        return;
                    }
                    if attempt == 2 {
                        if let Ok(mut jobs) = engine.footprint_jobs.lock() {
                            if jobs.revisions.get(&id).copied() != Some(revision) {
                                return;
                            }
                            jobs.errors.insert(id.clone(), error);
                            jobs.states.insert(id.clone(), JobState::Failed);
                        }
                        engine.footprint_changed();
                    } else {
                        engine.set_footprint_state(&id, revision, JobState::Retrying);
                    }
                }
            });
        }
        drop(jobs);
        self.footprint_changed();
        Ok(())
    }
    fn set_footprint_state(&self, id: &str, revision: u64, state: JobState) {
        if let Ok(mut jobs) = self.footprint_jobs.lock() {
            if jobs.revisions.get(id).copied() != Some(revision) {
                return;
            }
            jobs.states.insert(id.to_owned(), state);
        }
        self.footprint_changed();
    }
    /// The app left the list while its job waited: nothing to show and nothing to retry.
    fn forget_footprint_job(&self, id: &str, revision: u64) {
        if let Ok(mut jobs) = self.footprint_jobs.lock() {
            if jobs.revisions.get(id).copied() != Some(revision) {
                return;
            }
            jobs.revisions.remove(id);
            jobs.states.remove(id);
            jobs.errors.remove(id);
        }
        self.footprint_changed();
    }
    fn footprint_changed(&self) {
        (self.sink)(crate::events::CoreEvent::LocalApps(LocalAppsChanged {
            reason: LocalAppsChangedReason::Footprints,
        }));
    }
    pub async fn task_footprints(
        self: &Arc<Self>,
        task: &Task,
        permissions: Permissions,
    ) -> Result<(), AppError> {
        if !matches!(
            task.op,
            TaskOp::Install | TaskOp::Upgrade | TaskOp::Reinstall | TaskOp::Uninstall
        ) {
            return Ok(());
        }
        let Some(target) = &task.target else {
            return Ok(());
        };
        let ids = self
            .apps
            .lock()
            .map_err(poison)?
            .values()
            .filter(|record| {
                (target.kind == TaskKind::App && record.app.id == target.token)
                    || (target.kind == TaskKind::Cask
                        && record
                            .app
                            .cask
                            .as_ref()
                            .or(record.app.catalog_token.as_ref())
                            == Some(&target.token))
            })
            .map(|record| record.app.id.clone())
            .collect();
        self.schedule_footprints(ids, permissions)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn engine(base: &Path) -> Arc<Engine> {
        let roots = super::super::rules::Roots {
            home: base.join("User"),
            applications: vec![base.join("Applications")],
            system_library: base.join("Library"),
            bins: vec![],
            receipts: base.join("Receipts"),
            temporary: vec![],
            caskrooms: vec![],
        };
        std::fs::create_dir_all(&roots.applications[0]).unwrap();
        Engine::new(super::super::EngineConfig {
            database: crate::db::Database::open(&base.join("db.sqlite")).unwrap(),
            settings: Arc::new(std::sync::RwLock::new(Settings::default())),
            sink: Arc::new(|_| {}),
            roots,
            control: Arc::new(crate::running_apps::NativeApps),
            probe: Arc::new(super::super::identity::NativeProbe),
            icon_cache: base.join("icons"),
            askpass: base.join("askpass"),
        })
    }
    fn granted() -> Permissions {
        Permissions {
            app_management: PermissionState::Granted,
            full_disk_access: PermissionState::Granted,
            relaunch_required: false,
            app_management_pending: false,
            full_disk_access_pending: false,
        }
    }
    #[test]
    fn concurrent_scans_reads_and_cache_writes_finish() {
        let temp = tempfile::tempdir().unwrap();
        let engine = engine(&temp.path().canonicalize().unwrap());
        let barrier = Arc::new(std::sync::Barrier::new(3));
        let (sender, receiver) = std::sync::mpsc::channel();
        let mut workers = vec![];
        for role in 0..3 {
            let engine = engine.clone();
            let barrier = barrier.clone();
            let sender = sender.clone();
            workers.push(std::thread::spawn(move || {
                barrier.wait();
                for _ in 0..500 {
                    match role {
                        0 => {
                            engine.scan_footprints(granted(), false).unwrap();
                        }
                        1 => {
                            engine.footprint_report().unwrap();
                        }
                        _ => {
                            // The revision check and cache publication must stay atomic.
                            let _jobs = engine.footprint_jobs.lock().unwrap();
                            std::thread::yield_now();
                            let _cache = engine.footprints.lock().unwrap();
                        }
                    }
                }
                sender.send(()).unwrap();
            }));
        }
        for _ in 0..3 {
            receiver
                .recv_timeout(Duration::from_secs(10))
                .expect("footprint lock order deadlocked");
        }
        for worker in workers {
            worker.join().unwrap();
        }
    }

    #[tokio::test]
    async fn retries_stop_after_three_attempts_and_publish_error_without_frontend() {
        let temp = tempfile::tempdir().unwrap();
        let base = temp.path().canonicalize().unwrap();
        let engine = engine(&base);
        let app = base.join("Applications/Gone.app");
        std::fs::create_dir_all(app.join("Contents")).unwrap();
        engine.refresh(LocalAppsChangedReason::Startup).unwrap();
        let id = super::super::apps::stable_id(&app);
        // Still listed but unreadable: a real error, retried with short delays for this regression test.
        std::fs::remove_dir_all(&app).unwrap();
        engine
            .schedule_with_delays(
                vec![id.clone()],
                granted(),
                [Duration::from_millis(30), Duration::from_millis(120)],
            )
            .unwrap();
        tokio::time::sleep(Duration::from_millis(60)).await;
        let report = engine.footprint_report().unwrap();
        assert!(report.errors.is_empty());
        let progress = report.progress.unwrap();
        assert_eq!(progress.completed, 0);
        assert_eq!(progress.retrying, 1);
        assert!(progress.running);
        tokio::time::timeout(Duration::from_secs(3), async {
            while engine.footprint_report().unwrap().errors.is_empty() {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        assert_eq!(
            engine.footprint_report().unwrap().errors[&id].code,
            "E_UNKNOWN"
        );
        let progress = engine.footprint_report().unwrap().progress.unwrap();
        assert_eq!(progress.completed, progress.total);
        assert_eq!(progress.failed, 1);
        assert_eq!(progress.retrying, 0);
        assert!(!progress.running);
        // No polling runner remains to clear or retry the final failure.
        tokio::time::sleep(Duration::from_millis(60)).await;
        assert_eq!(engine.footprint_report().unwrap().errors.len(), 1);
    }
    #[tokio::test]
    async fn an_app_that_left_the_list_is_dropped_without_an_error() {
        let temp = tempfile::tempdir().unwrap();
        let base = temp.path().canonicalize().unwrap();
        let engine = engine(&base);
        engine.refresh(LocalAppsChangedReason::Startup).unwrap();
        engine
            .schedule_with_delays(
                vec!["missing".into()],
                granted(),
                [Duration::from_millis(30), Duration::from_millis(120)],
            )
            .unwrap();
        tokio::time::timeout(Duration::from_secs(3), async {
            while engine.footprint_report().unwrap().progress.is_some() {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        assert!(engine.footprint_report().unwrap().errors.is_empty());
    }
}
