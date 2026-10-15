#![cfg(unix)]
use opennavo_desktop_lib::{
    AppError,
    cleanup::{
        Engine as CleanupEngine,
        owner::{CommandProcess, ProcessProbe},
    },
    db::Database,
    events::CoreEvent,
    model::*,
    queue::TaskQueue,
    running_apps::{AppControl, Process},
    uninstall::{
        Engine, EngineConfig,
        execute::FileActions,
        identity::{Identity, MetadataProbe, Receipt, bundle},
        rules::Roots,
    },
};
use std::{
    fs,
    os::unix::fs::symlink,
    path::{Path, PathBuf},
    sync::{
        Arc, Mutex, RwLock,
        atomic::{AtomicBool, AtomicUsize, Ordering},
    },
    time::{Duration, SystemTime},
};
#[derive(Default)]
struct Apps {
    running: Mutex<Vec<Process>>,
    unknown: AtomicBool,
}
impl AppControl for Apps {
    fn list(&self) -> Result<Vec<Process>, AppError> {
        if self.unknown.load(Ordering::Acquire) {
            Err(AppError::new("E_UNKNOWN", "fake_probe"))
        } else {
            Ok(self.running.lock().unwrap().clone())
        }
    }
    fn quit(&self, _: &Process) -> Result<bool, AppError> {
        panic!("cleanup must not quit apps")
    }
    fn reopen(&self, _: &Path) -> Result<(), AppError> {
        panic!("cleanup must not reopen apps")
    }
}
#[derive(Default)]
struct Processes {
    paths: Mutex<Vec<PathBuf>>,
    open: Mutex<Vec<PathBuf>>,
    commands: Mutex<Vec<(PathBuf, Option<Vec<String>>)>>,
    table: Mutex<Vec<CommandProcess>>,
    openers: Mutex<Vec<(PathBuf, Option<i32>)>>,
    user_cache: Mutex<Option<PathBuf>>,
    user_temp: Mutex<Option<PathBuf>>,
    docker: Mutex<Option<PathBuf>>,
    delay: AtomicBool,
    open_error: AtomicBool,
    open_reads: AtomicUsize,
}
impl ProcessProbe for Processes {
    fn list(&self) -> Result<Vec<CommandProcess>, AppError> {
        if self.delay.load(Ordering::Acquire) {
            std::thread::sleep(Duration::from_millis(100));
        }
        let mut processes: Vec<_> = self
            .paths
            .lock()
            .unwrap()
            .iter()
            .map(|path| CommandProcess {
                pid: 0,
                parent_pid: 0,
                executable: path.clone(),
                argv: Some(vec![path.to_string_lossy().into_owned()]),
            })
            .collect();
        processes.extend(
            self.commands
                .lock()
                .unwrap()
                .iter()
                .map(|(path, argv)| CommandProcess {
                    pid: 0,
                    parent_pid: 0,
                    executable: path.clone(),
                    argv: argv.clone(),
                }),
        );
        processes.extend(self.table.lock().unwrap().clone());
        Ok(processes)
    }
    fn open_paths(&self) -> Result<Vec<PathBuf>, AppError> {
        Ok(self.open.lock().unwrap().clone())
    }
    fn open_files(&self) -> Result<Vec<(PathBuf, Option<i32>)>, AppError> {
        self.open_reads.fetch_add(1, Ordering::AcqRel);
        if self.open_error.load(Ordering::Acquire) {
            return Err(AppError::new("E_TIMEOUT", "fake_open_probe"));
        }
        let mut files: Vec<_> = self.open_paths()?.into_iter().map(|p| (p, None)).collect();
        files.extend(self.openers.lock().unwrap().clone());
        Ok(files)
    }
    fn user_cache_dir(&self) -> Result<Option<PathBuf>, AppError> {
        Ok(self.user_cache.lock().unwrap().clone())
    }
    fn user_temp_dir(&self) -> Result<Option<PathBuf>, AppError> {
        Ok(self.user_temp.lock().unwrap().clone())
    }
    fn docker(&self, _: &Path) -> Option<PathBuf> {
        self.docker.lock().unwrap().clone()
    }
}
#[derive(Default)]
struct Probe {
    loaded: AtomicBool,
}
impl MetadataProbe for Probe {
    fn identity(&self, p: &Path) -> Identity {
        bundle(p)
    }
    fn receipts(&self, _: &Path) -> Vec<Receipt> {
        vec![]
    }
    fn loaded(&self, _: &str, _: bool) -> bool {
        self.loaded.load(Ordering::Acquire)
    }
}
struct Files {
    root: PathBuf,
    trash_calls: AtomicUsize,
    bootouts: AtomicUsize,
}
impl FileActions for Files {
    fn trash(&self, path: &Path, trash: &Path) -> Result<PathBuf, AppError> {
        assert!(path.starts_with(&self.root));
        assert!(trash.starts_with(&self.root));
        self.trash_calls.fetch_add(1, Ordering::AcqRel);
        let destination = trash.join(uuid::Uuid::now_v7().to_string());
        fs::rename(path, &destination)?;
        Ok(destination)
    }
    fn bootout(&self, _: &str, system: bool, path: &str) -> Result<(), AppError> {
        assert!(!system);
        assert!(Path::new(path).starts_with(&self.root));
        self.bootouts.fetch_add(1, Ordering::AcqRel);
        Ok(())
    }
}
struct Fixture {
    _temp: tempfile::TempDir,
    home: PathBuf,
    apps: Arc<Apps>,
    processes: Arc<Processes>,
    probe: Arc<Probe>,
    engine: Arc<CleanupEngine>,
    queue: Arc<TaskQueue>,
    files: Arc<Files>,
    events: Arc<Mutex<Vec<CoreEvent>>>,
}
fn permissions(granted: bool) -> Permissions {
    Permissions {
        full_disk_access: if granted {
            PermissionState::Granted
        } else {
            PermissionState::Denied
        },
        app_management: PermissionState::Granted,
        relaunch_required: false,
        app_management_pending: false,
        full_disk_access_pending: false,
    }
}
impl Fixture {
    fn new() -> Self {
        // Generous: a busy machine running the suite in parallel must not turn a slow scan into a partial report.
        // Timeout behavior itself is tested with an explicit zero deadline.
        Self::timeout(Duration::from_secs(30))
    }
    fn timeout(timeout: Duration) -> Self {
        Self::limits(timeout, 2_000_000)
    }
    fn limits(timeout: Duration, nodes: usize) -> Self {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let home = root.join("User");
        let roots = Roots {
            home: home.clone(),
            applications: vec![home.join("Applications")],
            system_library: root.join("SystemLibrary"),
            bins: vec![],
            receipts: root.join("receipts"),
            temporary: vec![],
            caskrooms: vec![],
        };
        for path in [
            &home,
            &roots.applications[0],
            &roots.system_library,
            &roots.receipts,
        ] {
            fs::create_dir_all(path).unwrap();
        }
        for (path, _) in roots.locations() {
            fs::create_dir_all(path).unwrap();
        }
        let db = Database::open(&root.join("db.sqlite")).unwrap();
        let settings = Arc::new(RwLock::new(Settings::default()));
        let events = Arc::new(Mutex::new(vec![]));
        let captured = events.clone();
        let sink = Arc::new(move |event| captured.lock().unwrap().push(event));
        let apps = Arc::new(Apps::default());
        let probe = Arc::new(Probe::default());
        let processes = Arc::new(Processes::default());
        let uninstall = Engine::new(EngineConfig {
            database: db.clone(),
            settings: settings.clone(),
            sink: sink.clone(),
            roots,
            control: apps.clone(),
            probe: probe.clone(),
            icon_cache: root.join("icons"),
            askpass: root.join("askpass"),
        });
        let files = Arc::new(Files {
            root: root.clone(),
            trash_calls: AtomicUsize::new(0),
            bootouts: AtomicUsize::new(0),
        });
        uninstall.set_file_actions(files.clone()).unwrap();
        let engine =
            CleanupEngine::with_limits(uninstall.clone(), processes.clone(), timeout, nodes);
        let mut queue = TaskQueue::new(
            db,
            settings,
            root.join("task-logs"),
            root.join("askpass"),
            sink,
        )
        .unwrap();
        let unique = Arc::get_mut(&mut queue).unwrap();
        unique.uninstall_engine = uninstall;
        unique.cleanup_engine = engine.clone();
        unique.app_control = apps.clone();
        Self {
            _temp: temp,
            home,
            apps,
            processes,
            probe,
            engine,
            queue,
            files,
            events,
        }
    }
    fn app(&self, name: &str, id: &str) -> PathBuf {
        let path = self.home.join("Applications").join(format!("{name}.app"));
        fs::create_dir_all(path.join("Contents")).unwrap();
        let dictionary = plist::Dictionary::from_iter([
            ("CFBundleIdentifier", plist::Value::String(id.into())),
            ("CFBundleName", plist::Value::String(name.into())),
        ]);
        plist::Value::Dictionary(dictionary)
            .to_file_xml(path.join("Contents/Info.plist"))
            .unwrap();
        path
    }
    fn file(&self, relative: &str) -> PathBuf {
        let path = self.home.join(relative);
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(
            &path,
            (0..65536).map(|n| (n % 251) as u8).collect::<Vec<_>>(),
        )
        .unwrap();
        path
    }
    async fn scan(&self, keys: &[CleanupCategory]) -> CleanupReport {
        self.engine
            .scan(
                uuid::Uuid::now_v7().to_string(),
                Some(keys.to_vec()),
                permissions(true),
                None,
            )
            .await
            .unwrap()
    }
    fn run(&self, report: &CleanupReport, ids: Vec<String>) -> Task {
        self.engine
            .enqueue(&self.queue, &report.report_id, ids)
            .unwrap()
    }
    async fn execute(&self, report: &CleanupReport, ids: Vec<String>) -> Task {
        let task = self.run(report, ids);
        self.queue.start();
        tokio::time::timeout(Duration::from_secs(10), async {
            loop {
                let done = self
                    .queue
                    .database
                    .tasks()
                    .unwrap()
                    .into_iter()
                    .find(|t| t.id == task.id)
                    .unwrap();
                if !matches!(done.state, TaskState::Queued | TaskState::Running) {
                    break done;
                }
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap()
    }
}
fn items(report: &CleanupReport) -> Vec<&CleanupItem> {
    report
        .categories
        .iter()
        .flat_map(|c| &c.groups)
        .flat_map(|g| &g.items)
        .collect()
}
fn age(path: &Path, days: u64) {
    let time = SystemTime::now() - Duration::from_secs(days * 86400);
    fs::File::open(path)
        .unwrap()
        .set_times(fs::FileTimes::new().set_modified(time))
        .unwrap();
}

#[test]
fn summary_before_scan_is_non_null_and_volume_is_live_not_persisted() {
    let f = Fixture::new();
    let summary = f.engine.summary().unwrap();
    assert_eq!(summary.scanned_at, None);
    assert_eq!(summary.reclaimable_bytes, 0);
    assert_eq!(summary.suggested_bytes, 0);
    assert_eq!(summary.active_background_items, 0);
    assert!(summary.categories.is_empty());
    let volume = summary.volume.unwrap();
    let stat = nix::sys::statfs::statfs(Path::new("/")).unwrap();
    assert_eq!(
        volume.total_bytes,
        stat.blocks() * u64::from(stat.block_size())
    );
    assert!(!volume.name.is_empty());
    assert!(volume.free_bytes <= volume.total_bytes);
    // Existing summaries lacking volume remain readable; a stale injected volume is ignored.
    f.queue.database.set_meta("cleanup_summary_v1", r#"{"scannedAt":123,"reclaimableBytes":7,"suggestedBytes":5,"activeBackgroundItems":0,"categories":[],"volume":{"name":"stale","totalBytes":1,"freeBytes":1}}"#).unwrap();
    let summary = f.engine.summary().unwrap();
    assert_eq!(summary.scanned_at, Some(123));
    assert_eq!(summary.reclaimable_bytes, 7);
    assert_eq!(summary.volume.unwrap().total_bytes, volume.total_bytes);
}

#[tokio::test]
async fn restart_expires_queued_clean_and_preserves_interrupted_outcome_and_manifest() {
    let f = Fixture::new();
    f.app("Example", "org.example.test");
    let cache = f.file("Library/Caches/org.example.test/data");
    let report = f.scan(&[CleanupCategory::AppCaches]).await;
    let queued = f.run(
        &report,
        items(&report).iter().map(|i| i.id.clone()).collect(),
    );
    let restart = |fixture: &Fixture| {
        TaskQueue::new(
            fixture.queue.database.clone(),
            fixture.queue.settings.clone(),
            fixture.home.join("restart-logs"),
            fixture.home.join("askpass"),
            Arc::new(|_| {}),
        )
        .unwrap()
    };
    let recovered = restart(&f);
    assert!(recovered.active().unwrap().is_empty());
    assert!(!recovered.is_paused());
    let expired = recovered.database.task(&queued.id).unwrap().unwrap();
    assert_eq!(expired.state, TaskState::Failed);
    assert_eq!(expired.error.unwrap().code, "E_PLAN_EXPIRED");
    assert!(expired.finished_at.is_some());
    assert!(expired.started_at.is_none());
    assert!(expired.outcome.is_none());
    assert!(cache.exists());
    assert!(
        opennavo_desktop_lib::uninstall::manifest::entries(&f.queue.database, &queued.id)
            .unwrap()
            .is_empty()
    );
    // Ordinary queued maintenance retains the existing manual-resume behavior.
    let maintenance = recovered
        .enqueue(
            TaskOp::Cleanup,
            None,
            TaskOptions::default(),
            TaskTrigger::Manual,
        )
        .unwrap();
    let recovered_again = restart(&f);
    assert!(recovered_again.is_paused());
    assert_eq!(
        recovered_again
            .active()
            .unwrap()
            .iter()
            .map(|t| &t.id)
            .collect::<Vec<_>>(),
        [&maintenance.id]
    );

    let g = Fixture::new();
    let data = g.file("Library/Application Support/org.example.orphan/data");
    let report = g.scan(&[CleanupCategory::Leftovers]).await;
    let mut interrupted = g
        .execute(
            &report,
            items(&report).iter().map(|i| i.id.clone()).collect(),
        )
        .await;
    assert!(interrupted.outcome.as_ref().unwrap().restorable);
    assert!(!data.exists());
    let manifest_before =
        opennavo_desktop_lib::uninstall::manifest::entries(&g.queue.database, &interrupted.id)
            .unwrap();
    assert!(manifest_before.iter().all(|e| e.state == "trash"));
    // Simulate the process stopping after a trash move and persisted outcome, before task completion.
    interrupted.state = TaskState::Running;
    interrupted.finished_at = None;
    g.queue.database.save_task(&interrupted).unwrap();
    let recovered = restart(&g);
    assert!(recovered.active().unwrap().is_empty());
    let failed = recovered.database.task(&interrupted.id).unwrap().unwrap();
    assert_eq!(failed.state, TaskState::Failed);
    assert_eq!(failed.error.unwrap().code, "E_INTERRUPTED");
    assert_eq!(failed.outcome, interrupted.outcome);
    let manifest_after =
        opennavo_desktop_lib::uninstall::manifest::entries(&g.queue.database, &interrupted.id)
            .unwrap();
    assert_eq!(manifest_after.len(), manifest_before.len());
    for (before, after) in manifest_before.iter().zip(&manifest_after) {
        assert_eq!(after.state, before.state);
        assert_eq!(after.trash, before.trash);
        assert_eq!(after.snapshot, before.snapshot);
    }
    assert!(
        g.engine
            .uninstall
            .restore(&interrupted.id)
            .unwrap()
            .restored
            > 0
    );
    assert!(data.exists());
    g.queue.shutdown();
}

#[tokio::test]
async fn scan_progress_is_global_cumulative_monotonic_and_finishes() {
    let f = Fixture::new();
    f.app("Example", "org.example.test");
    f.app("Chrome", "com.google.Chrome");
    f.file("Library/Caches/org.example.test/data");
    f.file("Library/Application Support/Google/Chrome/Default/Cache/data");
    f.file(".npm/_cacache/data");
    let log = f.file("Library/Logs/old.log");
    age(&log, 8);
    let report = f
        .engine
        .scan("global".into(), None, permissions(true), None)
        .await
        .unwrap();
    {
        let events = f.events.lock().unwrap();
        let progress: Vec<_> = events
            .iter()
            .filter_map(|event| match event {
                CoreEvent::CleanupScan(p) if p.request_id == "global" => Some(p),
                _ => None,
            })
            .collect();
        assert!(progress.len() >= 2);
        assert!(
            progress
                .iter()
                // Six P0 categories, five P1 categories and Docker.
                .all(|p| p.total == 12 && p.checked <= p.total)
        );
        assert_eq!(progress.first().unwrap().checked, 0);
        assert!(
            progress
                .windows(2)
                .all(|p| p[0].checked <= p[1].checked && p[0].found_bytes <= p[1].found_bytes)
        );
        let last = progress.last().unwrap();
        assert_eq!(last.checked, last.total);
        let bytes: u64 = report.categories.iter().map(|c| c.bytes).sum();
        assert!(bytes > 0);
        assert_eq!(last.found_bytes, bytes);
        assert!(events.iter().any(|e| matches!(e, CoreEvent::CleanupChanged(s) if s.volume.is_some() && s.scanned_at.is_some())));
        let stored: serde_json::Value = serde_json::from_str(
            &f.queue
                .database
                .meta("cleanup_summary_v1")
                .unwrap()
                .unwrap(),
        )
        .unwrap();
        assert!(stored.get("volume").is_none());
    }

    let no_tools = Fixture::new();
    no_tools
        .engine
        .scan("no-dev".into(), None, permissions(true), None)
        .await
        .unwrap();
    let events = no_tools.events.lock().unwrap();
    let progress: Vec<_> = events
        .iter()
        .filter_map(|e| match e {
            CoreEvent::CleanupScan(p) => Some(p),
            _ => None,
        })
        .collect();
    assert!(
        progress
            .iter()
            // Every available category except developer caches, which this Mac has no tools for.
            .all(|p| p.total == 11 && p.category != CleanupCategory::DevCaches)
    );
    assert_eq!(progress.last().unwrap().checked, 11);
}

#[tokio::test]
async fn browser_running_helpers_unknown_and_execution_recheck() {
    let f = Fixture::new();
    let app = f.app("Chrome", "com.google.Chrome");
    let cache = f.file("Library/Caches/Google/Chrome/Default/Cache/data");
    f.apps.running.lock().unwrap().push(Process {
        pid: 1,
        path: app.clone(),
        bundle_id: "com.google.Chrome".into(),
    });
    let report = f.scan(&[CleanupCategory::BrowserCaches]).await;
    assert!(items(&report).is_empty());
    assert!(
        report.categories[0]
            .skipped
            .iter()
            .any(|s| s.reason == SkippedGroupReason::Running)
    );
    f.apps.running.lock().unwrap().clear();
    f.processes
        .paths
        .lock()
        .unwrap()
        .push(app.join("Contents/Frameworks/Chrome Helper.app/Contents/MacOS/Chrome Helper"));
    assert!(items(&f.scan(&[CleanupCategory::BrowserCaches]).await).is_empty());
    f.processes.paths.lock().unwrap().clear();
    f.apps.unknown.store(true, Ordering::Release);
    let report = f.scan(&[CleanupCategory::BrowserCaches]).await;
    assert_eq!(
        report.categories[0].skipped[0].reason,
        SkippedGroupReason::InUse
    );
    f.apps.unknown.store(false, Ordering::Release);
    let report = f.scan(&[CleanupCategory::BrowserCaches]).await;
    assert_eq!(items(&report).len(), 1);
    f.apps.running.lock().unwrap().push(Process {
        pid: 1,
        path: app,
        bundle_id: "com.google.Chrome".into(),
    });
    let task = f.execute(&report, vec![items(&report)[0].id.clone()]).await;
    assert_eq!(
        task.outcome.unwrap().skipped[0].reason,
        SkippedItemReason::Running
    );
    assert!(cache.exists());
}
#[tokio::test]
async fn rebuildable_direct_delete_measures_blocks_and_updates_persistent_summary() {
    let f = Fixture::new();
    f.app("测试 App", "org.example.test");
    let cache = f.file("Library/Caches/org.example.test/data");
    let logs = f.file("Library/Logs/old.log");
    age(&logs, 8);
    let report = f
        .scan(&[CleanupCategory::AppCaches, CleanupCategory::Logs])
        .await;
    assert_eq!(items(&report).len(), 2);
    assert!(
        items(&report)
            .iter()
            .all(|i| i.selected && i.delete_mode == DeleteMode::Delete)
    );
    let bytes: u64 = items(&report).iter().filter_map(|i| i.size_bytes).sum();
    let ids = items(&report).iter().map(|i| i.id.clone()).collect();
    let task = f.execute(&report, ids).await;
    assert_eq!(task.state, TaskState::Succeeded);
    let outcome = task.outcome.unwrap();
    assert_eq!(outcome.freed_bytes, bytes);
    assert_eq!(outcome.trashed_bytes, 0);
    assert_eq!(outcome.deleted, 2);
    assert!(!outcome.restorable);
    assert_eq!(f.files.trash_calls.load(Ordering::Acquire), 0);
    assert!(!cache.exists());
    assert!(!logs.exists());
    assert_eq!(f.engine.summary().unwrap().reclaimable_bytes, 0);
    assert!(
        f.events
            .lock()
            .unwrap()
            .iter()
            .any(|e| matches!(e, CoreEvent::CleanupChanged(s) if s.reclaimable_bytes == 0))
    );
    let restored = CleanupEngine::with_processes(
        f.engine.uninstall.clone(),
        f.processes.clone(),
        Duration::from_secs(5),
    );
    let restored_summary = restored.summary().unwrap();
    let current = f.engine.summary().unwrap();
    assert_eq!(restored_summary.scanned_at, current.scanned_at);
    assert_eq!(restored_summary.categories, current.categories);
    assert_eq!(
        restored_summary.reclaimable_bytes,
        current.reclaimable_bytes
    );
    assert!(restored_summary.volume.is_some());
}
#[tokio::test]
async fn many_items_share_activity_readings_and_progress_reports_freed_bytes() {
    let f = Fixture::new();
    let logs: Vec<_> = (0..40)
        .map(|i| {
            let path = f.file(&format!("Library/Logs/Tool/run-{i}.log"));
            age(&path, 8);
            path
        })
        .collect();
    let report = f.scan(&[CleanupCategory::Logs]).await;
    let selected: u64 = items(&report).iter().filter_map(|i| i.size_bytes).sum();
    assert_eq!(items(&report).len(), logs.len());
    let reads = f.processes.open_reads.load(Ordering::Acquire);
    let ids = items(&report).iter().map(|i| i.id.clone()).collect();
    let task = f.execute(&report, ids).await;
    let outcome = task.outcome.clone().unwrap();
    assert_eq!(outcome.deleted, logs.len() as i64);
    assert!(logs.iter().all(|path| !path.exists()));
    // Forty items read open files once or twice (a reading serves two seconds), not twice per item.
    assert!(f.processes.open_reads.load(Ordering::Acquire) - reads <= 2);
    let progress: Vec<_> = f
        .events
        .lock()
        .unwrap()
        .iter()
        .filter_map(|e| match e {
            CoreEvent::Task(t) if t.id == task.id && t.bytes_total.is_some() => Some((
                t.phase,
                t.percent.unwrap(),
                t.bytes_done.unwrap(),
                t.bytes_total.unwrap(),
            )),
            _ => None,
        })
        .collect();
    assert!(progress.iter().all(|(_, _, _, total)| *total == selected));
    assert!(
        progress
            .windows(2)
            .all(|w| w[0].1 <= w[1].1 && w[0].2 <= w[1].2)
    );
    assert_eq!(
        progress.last(),
        Some(&(
            Some(TaskPhase::Finishing),
            100.0,
            outcome.freed_bytes,
            selected
        ))
    );
}

#[tokio::test]
async fn an_incomplete_activity_reading_is_not_reused() {
    let f = Fixture::new();
    for i in 0..5 {
        age(&f.file(&format!("Library/Logs/Tool/run-{i}.log")), 8);
    }
    let report = f.scan(&[CleanupCategory::Logs]).await;
    f.processes.open_error.store(true, Ordering::Release);
    let reads = f.processes.open_reads.load(Ordering::Acquire);
    let ids = items(&report).iter().map(|i| i.id.clone()).collect();
    let task = f.execute(&report, ids).await;
    assert_eq!(task.outcome.unwrap().deleted, 5);
    // Checked when each item starts and again right before it is removed.
    assert_eq!(f.processes.open_reads.load(Ordering::Acquire) - reads, 10);
}

#[tokio::test]
async fn protect_result_bundle_manual_child_persistence_and_unprotect() {
    let f = Fixture::new();
    f.app("Example", "org.example.test");
    f.file("Library/Caches/org.example.test/cache/child");
    let report = f.scan(&[CleanupCategory::AppCaches]).await;
    let id = items(&report)[0].id.clone();
    let rule = f
        .engine
        .protect(ProtectInput::Item {
            report_id: report.report_id.clone(),
            item_id: id.clone(),
        })
        .unwrap();
    assert_eq!(rule.kind, ProtectKind::BundleId);
    assert_eq!(rule.value, "org.example.test");
    assert!(items(&f.engine.cached(&report.report_id, 0).unwrap().report).is_empty());
    assert_eq!(f.engine.summary().unwrap().reclaimable_bytes, 0);
    assert!(items(&f.scan(&[CleanupCategory::AppCaches]).await).is_empty());
    let recreated = CleanupEngine::with_processes(
        f.engine.uninstall.clone(),
        f.processes.clone(),
        Duration::from_secs(5),
    );
    assert_eq!(recreated.protected_list().unwrap(), vec![rule.clone()]);
    recreated.unprotect(&rule.id).unwrap();
    assert_eq!(items(&f.scan(&[CleanupCategory::AppCaches]).await).len(), 1);
    let child = f.home.join("Library/Caches/org.example.test/cache");
    let manual = f
        .engine
        .protect(ProtectInput::Manual {
            kind: ProtectKind::Path,
            value: child.to_string_lossy().into(),
        })
        .unwrap();
    assert!(items(&f.scan(&[CleanupCategory::AppCaches]).await).is_empty());
    f.engine.unprotect(&manual.id).unwrap();
    assert_eq!(items(&f.scan(&[CleanupCategory::AppCaches]).await).len(), 1);
}
#[tokio::test]
async fn protection_added_after_enqueue_prevents_delete() {
    let f = Fixture::new();
    let path = f.file(".npm/_cacache/data");
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    let task = f.run(&report, vec![items(&report)[0].id.clone()]);
    // Enqueued task pins the report, but the live protection list remains authoritative.
    f.engine
        .protect(ProtectInput::Manual {
            kind: ProtectKind::Path,
            value: path.parent().unwrap().to_string_lossy().into(),
        })
        .unwrap();
    let result = opennavo_desktop_lib::cleanup::execute::run(
        f.queue.clone(),
        &task,
        Arc::new(AtomicBool::new(false)),
        Arc::new(tokio::sync::Notify::new()),
    )
    .await;
    assert!(result.is_ok());
    let outcome = f
        .queue
        .database
        .tasks()
        .unwrap()
        .into_iter()
        .find(|t| t.id == task.id)
        .unwrap()
        .outcome
        .unwrap();
    assert_eq!(outcome.skipped[0].reason, SkippedItemReason::Protected);
    assert!(path.exists());
}
#[tokio::test]
async fn inode_and_nested_content_change_are_skipped() {
    for nested in [false, true] {
        let f = Fixture::new();
        let path = f.file(".npm/_cacache/data");
        let report = f.scan(&[CleanupCategory::DevCaches]).await;
        if nested {
            fs::write(&path, b"changed data").unwrap();
        } else {
            let parent = path.parent().unwrap();
            fs::rename(parent, parent.with_extension("original")).unwrap();
            f.file(".npm/_cacache/data");
        }
        let task = f.execute(&report, vec![items(&report)[0].id.clone()]).await;
        assert_eq!(
            task.outcome.unwrap().skipped[0].reason,
            SkippedItemReason::Changed
        );
        assert!(path.exists());
    }
}
#[tokio::test]
async fn symlink_target_and_replaced_parent_preserve_documents() {
    for parent in [false, true] {
        let f = Fixture::new();
        f.app("Example", "org.example.test");
        let cache = f.file("Library/Caches/org.example.test/data");
        let documents = f.file("Documents/sentinel");
        let report = f.scan(&[CleanupCategory::AppCaches]).await;
        let replace = if parent {
            f.home.join("Library/Caches")
        } else {
            cache.parent().unwrap().to_owned()
        };
        fs::rename(&replace, replace.with_extension("original")).unwrap();
        symlink(documents.parent().unwrap(), &replace).unwrap();
        let task = f.execute(&report, vec![items(&report)[0].id.clone()]).await;
        assert_eq!(
            task.outcome.unwrap().skipped[0].reason,
            SkippedItemReason::Changed
        );
        assert_eq!(fs::read(&documents).unwrap().len(), 65536);
    }
}
#[tokio::test]
async fn fda_missing_marks_locations_and_other_collectors_continue() {
    let f = Fixture::new();
    let log = f.file("Library/Logs/old.log");
    age(&log, 8);
    f.file("Library/Containers/com.apple.Safari/Data/Library/Caches/com.apple.Safari/data");
    let report = f
        .engine
        .scan(
            "no-fda".into(),
            Some(vec![
                CleanupCategory::AppCaches,
                CleanupCategory::BrowserCaches,
                CleanupCategory::Logs,
            ]),
            permissions(false),
            None,
        )
        .await
        .unwrap();
    assert!(report.incomplete.iter().any(
        |i| i.path.contains("Containers") && i.reason == SkippedLocationReason::FullDiskAccess
    ));
    assert!(report.incomplete.iter().any(|i| i.path.contains("Safari")));
    assert!(
        report
            .categories
            .iter()
            .filter(|c| c.key != CleanupCategory::Logs)
            .all(|c| !c.complete)
    );
    assert_eq!(items(&report).len(), 1);
    assert!(
        items(&f.scan(&[CleanupCategory::BrowserCaches]).await)
            .iter()
            .any(|i| i.path.contains("Safari"))
    );
}
#[tokio::test]
async fn timeout_partial_cancel_drains_and_rejects_invalid_requests() {
    let f = Fixture::timeout(Duration::ZERO);
    f.file(".npm/_cacache/data");
    let report = f
        .scan(&[CleanupCategory::DevCaches, CleanupCategory::Logs])
        .await;
    assert!(report.categories.iter().all(|c| !c.complete));
    assert!(
        report
            .incomplete
            .iter()
            .all(|i| i.reason == SkippedLocationReason::Timeout)
    );
    assert_eq!(f.engine.active_requests(), 0);
    let f = Fixture::new();
    f.file(".npm/_cacache/data");
    f.processes.delay.store(true, Ordering::Release);
    let engine = f.engine.clone();
    let job = tokio::spawn(async move {
        engine
            .scan_for_command(
                "cancel-me".into(),
                Some(vec![CleanupCategory::DevCaches, CleanupCategory::Leftovers]),
                permissions(true),
                None,
            )
            .await
    });
    while f.engine.active_requests() == 0 {
        tokio::task::yield_now().await;
    }
    f.engine.cancel("cancel-me").unwrap();
    let error = job.await.unwrap().unwrap_err();
    assert_eq!(error.code, "E_SCAN_CANCELED");
    assert_eq!(f.engine.active_requests(), 0);
    assert!(
        f.engine
            .scan("".into(), None, permissions(true), None)
            .await
            .is_err()
    );
    assert!(
        f.engine
            .scan(
                "p2".into(),
                // P1 categories are available now; large files (P2) are not yet.
                Some(vec![CleanupCategory::LargeFiles]),
                permissions(true),
                None
            )
            .await
            .is_err()
    );
}
#[tokio::test]
async fn browser_profiles_keep_cookies_history_passwords_extensions() {
    let f = Fixture::new();
    f.app("Chrome", "com.google.Chrome");
    f.file("Library/Application Support/Google/Chrome/Default/Cache/data");
    let sentinels: Vec<_> = [
        "History",
        "Cookies",
        "Login Data",
        "Extensions/data",
        "Local Storage/data",
    ]
    .iter()
    .map(|leaf| {
        f.file(&format!(
            "Library/Application Support/Google/Chrome/Default/{leaf}"
        ))
    })
    .collect();
    let report = f
        .scan(&[CleanupCategory::BrowserCaches, CleanupCategory::Leftovers])
        .await;
    assert_eq!(items(&report).len(), 1);
    let ids = items(&report).iter().map(|i| i.id.clone()).collect();
    f.execute(&report, ids).await;
    assert!(sentinels.iter().all(|p| p.exists()));
}
#[tokio::test]
async fn developer_allowlist_retains_latest_support_runtimes_and_binaries() {
    let f = Fixture::new();
    for path in [
        ".npm/_cacache/data",
        "Library/Caches/Yarn/v6/data",
        "Library/Caches/pip/wheels/data",
        "Library/Caches/go-build/data",
        "Library/Caches/CocoaPods/Pods/data",
        "Library/Developer/Xcode/DerivedData/project/data",
        "Library/Developer/CoreSimulator/Caches/data",
        "Library/Developer/Xcode/iOS DeviceSupport/9.3 (a)/data",
        "Library/Developer/Xcode/iOS DeviceSupport/17.5 (a)/data",
    ] {
        f.file(path);
    }
    let latest = f.file("Library/Developer/Xcode/iOS DeviceSupport/18.0 (b)/data");
    age(
        &f.home
            .join("Library/Developer/Xcode/iOS DeviceSupport/9.3 (a)"),
        10,
    );
    age(
        &f.home
            .join("Library/Developer/Xcode/iOS DeviceSupport/17.5 (a)"),
        5,
    );
    let runtime = f.file("Library/Developer/CoreSimulator/Devices/device/data");
    let binary = f.file(".cargo/bin/cargo");
    let rustup = f.file(".rustup/toolchains/data");
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    assert_eq!(items(&report).len(), 10);
    assert!(
        items(&report)
            .iter()
            .all(|i| i.selected == (i.reason != CleanupReason::DeviceSupportLatest))
    );
    f.execute(
        &report,
        items(&report)
            .iter()
            .filter(|i| i.selected)
            .map(|i| i.id.clone())
            .collect(),
    )
    .await;
    assert!(latest.exists());
    assert!(runtime.exists());
    assert!(binary.exists());
    assert!(rustup.exists());
    let f = Fixture::new();
    assert!(
        f.scan(&[CleanupCategory::DevCaches])
            .await
            .categories
            .is_empty()
    );
}
#[tokio::test]
async fn logs_use_seven_days_and_protected_apps_are_never_candidates() {
    let f = Fixture::new();
    let old = f.file("Library/Logs/DiagnosticReports/old.ips");
    age(&old, 8);
    let recent = f.file("Library/Logs/recent.log");
    let mut protected = vec![];
    for (name, bundle) in [
        ("Cursor", "com.todesktop.cursor"),
        ("Claude", "com.anthropic.claude"),
        ("Docker", "com.docker.docker"),
        ("Password", "com.1password.1password"),
        ("Surge", "com.nssurge.surge-mac"),
        ("Apple", "com.apple.protected-test"),
    ] {
        f.app(name, bundle);
        protected.push(f.file(&format!("Library/Caches/{bundle}/data")));
    }
    let report = f
        .scan(&[
            CleanupCategory::AppCaches,
            CleanupCategory::Logs,
            CleanupCategory::Leftovers,
        ])
        .await;
    assert_eq!(items(&report).len(), 1);
    assert_eq!(items(&report)[0].path, old.to_string_lossy());
    f.execute(&report, vec![items(&report)[0].id.clone()]).await;
    assert!(recent.exists());
    assert!(protected.iter().all(|p| p.exists()));
}
#[tokio::test]
async fn open_cache_files_and_running_developer_tools_are_skipped() {
    let f = Fixture::new();
    let path = f.file(".npm/_cacache/data");
    f.processes.open.lock().unwrap().push(path.clone());
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    assert!(report.categories[0].skipped.is_empty());
    assert_eq!(items(&report)[0].kept_open_files, 1);
    assert_eq!(items(&report)[0].size_bytes, Some(0));
    f.processes.open.lock().unwrap().clear();
    f.processes
        .paths
        .lock()
        .unwrap()
        .push("/synthetic/bin/npm".into());
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    assert_eq!(
        report.categories[0].skipped[0].reason,
        SkippedGroupReason::Running
    );
    f.processes.paths.lock().unwrap().clear();
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    f.processes.open.lock().unwrap().push(path.clone());
    let task = f.execute(&report, vec![items(&report)[0].id.clone()]).await;
    assert_eq!(
        task.outcome.unwrap().skipped[0].reason,
        SkippedItemReason::InUse
    );
    assert!(path.exists());
}

fn allocated(path: &Path) -> u64 {
    use std::os::unix::fs::MetadataExt;
    fs::symlink_metadata(path).unwrap().blocks() * 512
}

fn command(pid: i32, parent_pid: i32, name: &str, argv: &[&str]) -> CommandProcess {
    CommandProcess {
        pid,
        parent_pid,
        executable: PathBuf::from("/synthetic/bin").join(name),
        argv: Some(argv.iter().map(|s| (*s).into()).collect()),
    }
}

#[tokio::test]
async fn go_cache_only_blocks_compilation_not_long_running_services_or_gopls() {
    for (processes, expected) in [
        (
            vec![
                command(100, 1, "go", &["go", "run", "./cmd/api"]),
                command(101, 100, "api", &["api"]),
                command(200, 1, "go", &["go", "run", "./cmd/worker"]),
                command(201, 200, "worker", &["worker"]),
            ],
            vec![],
        ),
        (
            vec![
                command(100, 1, "go", &["go", "run", "./cmd/api"]),
                command(101, 100, "wrapper", &["wrapper"]),
                command(102, 101, "compile", &["compile"]),
            ],
            vec!["compile", "go"],
        ),
        (
            vec![command(100, 1, "go", &["go", "build", "./..."])],
            vec!["go"],
        ),
        (vec![command(100, 1, "gopls", &["gopls"])], vec![]),
        (
            vec![
                command(100, 1, "go", &["go", "run", "./cmd/api"]),
                command(102, 1, "compile", &["compile"]),
            ],
            vec![],
        ),
    ] {
        let f = Fixture::new();
        let data = f.file("Library/Caches/go-build/data");
        *f.processes.table.lock().unwrap() = processes;
        let report = f.scan(&[CleanupCategory::DevCaches]).await;
        if expected.is_empty() {
            assert_eq!(items(&report).len(), 1);
            assert!(report.categories[0].skipped.is_empty());
        } else {
            assert!(items(&report).is_empty());
            let group = &report.categories[0].skipped[0];
            assert_eq!(group.reason, SkippedGroupReason::Running);
            assert_eq!(group.processes, expected);
            assert_eq!(group.bytes, Some(allocated(&data)));
        }
        assert!(data.exists());
    }
}

#[tokio::test]
async fn go_subcommands_open_files_and_execution_recheck_remain_protected() {
    let f = Fixture::new();
    let data = f.file("Library/Caches/go-build/data");
    for subcommand in [
        "build", "install", "test", "vet", "generate", "get", "mod", "work", "clean", "tool",
        "list",
    ] {
        *f.processes.table.lock().unwrap() = vec![command(
            100,
            1,
            "go",
            &["go", "-C", "/synthetic/project", subcommand],
        )];
        let report = f.scan(&[CleanupCategory::DevCaches]).await;
        assert_eq!(
            report.categories[0].skipped[0].processes,
            ["go"],
            "{subcommand}"
        );
    }
    *f.processes.table.lock().unwrap() = vec![
        command(100, 1, "go", &["go", "run", "./cmd/api"]),
        command(101, 100, "api", &["api"]),
    ];
    f.processes
        .openers
        .lock()
        .unwrap()
        .push((data.clone(), Some(101)));
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    assert!(report.categories[0].skipped.is_empty());
    assert_eq!(items(&report)[0].kept_open_files, 1);
    assert_eq!(items(&report)[0].size_bytes, Some(0));
    f.processes.openers.lock().unwrap().clear();
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    f.processes
        .table
        .lock()
        .unwrap()
        .push(command(102, 100, "compile", &["compile"]));
    let task = f.execute(&report, vec![items(&report)[0].id.clone()]).await;
    assert_eq!(
        task.outcome.unwrap().skipped[0].reason,
        SkippedItemReason::Running
    );
    assert!(data.exists());
}

#[tokio::test]
async fn device_support_groups_all_platforms_and_models_and_uses_directory_mtime() {
    let f = Fixture::new();
    for platform in ["iOS", "watchOS", "tvOS", "xrOS", "visionOS", "macOS"] {
        for (model, versions) in [
            ("Device1,1", vec!["18.10", "18.9"]),
            ("Device2,1", vec!["17.0"]),
            ("", vec!["9.3", "17.0"]),
        ] {
            for version in &versions {
                let name = format!(
                    "{}{} (B)",
                    if model.is_empty() {
                        String::new()
                    } else {
                        format!("{model} ")
                    },
                    version
                );
                let data = f.file(&format!(
                    "Library/Developer/Xcode/{platform} DeviceSupport/{name}/data"
                ));
                age(
                    data.parent().unwrap(),
                    if *version == "18.9" { 1 } else { 2 },
                );
            }
        }
        f.file(&format!(
            "Library/Developer/Xcode/{platform} DeviceSupport/unrecognized/readme"
        ));
    }
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    let category = &report.categories[0];
    assert_eq!(category.risk, CleanupRisk::Rebuildable);
    assert_eq!(category.groups.len(), 18);
    assert_eq!(items(&report).len(), 30);
    for group in &category.groups {
        let latest: Vec<_> = group
            .items
            .iter()
            .filter(|i| i.reason == CleanupReason::DeviceSupportLatest)
            .collect();
        assert_eq!(latest.len(), 1);
        assert!(!latest[0].selected);
        assert!(
            group
                .items
                .iter()
                .filter(|i| i.reason == CleanupReason::DeviceSupport)
                .all(|i| i.selected)
        );
        assert!(latest[0].size_bytes.unwrap() > 0);
        if group.title == "Device1,1" {
            assert!(latest[0].path.contains("18.9"));
        }
        assert!(group.subtitle.is_some());
    }
    assert_eq!(items(&report).iter().filter(|i| i.selected).count(), 12);
}

#[tokio::test]
async fn device_support_mtime_ties_use_numeric_version_and_latest_stays_by_default() {
    let f = Fixture::new();
    let old = f.file("Library/Developer/Xcode/iOS DeviceSupport/iPhone16,2 18.9 (A)/data");
    let latest = f.file("Library/Developer/Xcode/iOS DeviceSupport/iPhone16,2 18.10 (B)/data");
    let modified = SystemTime::now() - Duration::from_secs(86400);
    for path in [&old, &latest] {
        fs::File::open(path.parent().unwrap())
            .unwrap()
            .set_times(fs::FileTimes::new().set_modified(modified))
            .unwrap();
    }
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    let group = &report.categories[0].groups[0];
    assert_eq!(group.title, "iPhone16,2");
    assert_eq!(group.subtitle.as_deref(), Some("iOS"));
    assert_eq!(
        group.items.iter().find(|i| !i.selected).unwrap().path,
        latest.parent().unwrap().to_string_lossy()
    );
    f.execute(
        &report,
        items(&report)
            .iter()
            .filter(|i| i.selected)
            .map(|i| i.id.clone())
            .collect(),
    )
    .await;
    assert!(!old.exists());
    assert!(latest.exists());
}

#[tokio::test]
async fn device_support_running_xcode_and_protection_rules_exclude_versions() {
    let f = Fixture::new();
    let a = f.file("Library/Developer/Xcode/watchOS DeviceSupport/Watch7,11 10.0 (A)/data");
    let b = f.file("Library/Developer/Xcode/watchOS DeviceSupport/Watch7,11 11.0 (B)/data");
    f.processes
        .paths
        .lock()
        .unwrap()
        .push("/synthetic/bin/XCBBuildService".into());
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    assert!(items(&report).is_empty());
    assert_eq!(
        report.categories[0].skipped[0].processes,
        ["XCBBuildService"]
    );
    assert_eq!(
        report.categories[0].skipped[0].bytes,
        Some(allocated(&a) + allocated(&b))
    );
    f.processes.paths.lock().unwrap().clear();
    f.engine
        .protect(ProtectInput::Manual {
            kind: ProtectKind::BundleId,
            value: "com.apple.dt.Xcode".into(),
        })
        .unwrap();
    assert!(items(&f.scan(&[CleanupCategory::DevCaches]).await).is_empty());
}

#[tokio::test]
async fn npm_logs_keep_recent_files_and_skip_active_npm() {
    let f = Fixture::new();
    let old = f.file(".npm/_logs/old.log");
    age(&old, 8);
    let recent = f.file(".npm/_logs/recent.log");
    let report = f.scan(&[CleanupCategory::Logs]).await;
    assert_eq!(items(&report).len(), 1);
    assert_eq!(report.categories[0].groups[0].title, "npm");
    assert_eq!(items(&report)[0].reason, CleanupReason::LogFile);
    *f.processes.table.lock().unwrap() = vec![command(
        100,
        1,
        "node",
        &["node", "/opt/npm-cli.js", "--token=secret"],
    )];
    let report = f.scan(&[CleanupCategory::Logs]).await;
    assert_eq!(report.categories[0].skipped[0].processes, ["node"]);
    assert!(recent.exists());
    let protected = f
        .engine
        .protect(ProtectInput::Manual {
            kind: ProtectKind::Path,
            value: old.to_string_lossy().into_owned(),
        })
        .unwrap();
    assert!(items(&f.scan(&[CleanupCategory::Logs]).await).is_empty());
    f.engine.unprotect(&protected.id).unwrap();
}

#[tokio::test]
async fn clang_module_caches_outside_home_are_scoped_protected_and_activity_checked() {
    let f = Fixture::new();
    let root = f.home.parent().unwrap().join("DarwinCache");
    *f.processes.user_cache.lock().unwrap() = Some(root.clone());
    for leaf in ["clang/ModuleCache", "org.llvm.clang/ModuleCache"] {
        fs::create_dir_all(root.join(leaf)).unwrap();
        fs::write(root.join(leaf).join("module"), [0x55; 65536]).unwrap();
    }
    let sentinel = root.join("clang/keep");
    fs::write(&sentinel, "keep").unwrap();
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    assert_eq!(items(&report).len(), 2);
    assert_eq!(report.categories[0].groups[0].title, "Clang");
    assert!(
        items(&report)
            .iter()
            .all(|i| i.path.starts_with(root.to_str().unwrap()))
    );
    for name in ["clang", "swift-frontend", "Xcode", "xcodebuild"] {
        *f.processes.table.lock().unwrap() = vec![command(100, 1, name, &[name])];
        let skipped = f.scan(&[CleanupCategory::DevCaches]).await;
        assert_eq!(skipped.categories[0].skipped.len(), 1);
        assert_eq!(skipped.categories[0].skipped[0].processes, [name]);
    }
    f.processes.table.lock().unwrap().clear();
    f.engine
        .protect(ProtectInput::Manual {
            kind: ProtectKind::Path,
            value: root
                .join("clang/ModuleCache")
                .to_string_lossy()
                .into_owned(),
        })
        .unwrap();
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    assert_eq!(items(&report).len(), 1);
    f.execute(
        &report,
        items(&report).iter().map(|i| i.id.clone()).collect(),
    )
    .await;
    assert!(root.join("clang/ModuleCache/module").exists());
    assert!(!root.join("org.llvm.clang/ModuleCache").exists());
    assert!(sentinel.exists());
}

#[tokio::test]
async fn google_updater_crx_cache_is_owned_by_chrome_and_skips_updater() {
    let f = Fixture::new();
    let cache = f.file("Library/Application Support/Google/GoogleUpdater/crx_cache/data");
    let sentinel = f.file("Library/Application Support/Google/GoogleUpdater/keep");
    let report = f.scan(&[CleanupCategory::BrowserCaches]).await;
    assert_eq!(items(&report).len(), 1);
    assert_eq!(report.categories[0].groups[0].title, "Chrome");
    f.processes
        .paths
        .lock()
        .unwrap()
        .push("/synthetic/bin/GoogleUpdater".into());
    let report = f.scan(&[CleanupCategory::BrowserCaches]).await;
    assert_eq!(report.categories[0].skipped[0].processes, ["GoogleUpdater"]);
    assert_eq!(
        report.categories[0].skipped[0].bytes,
        Some(allocated(&cache))
    );
    f.processes.paths.lock().unwrap().clear();
    f.engine
        .protect(ProtectInput::Manual {
            kind: ProtectKind::Path,
            value: cache.to_string_lossy().into_owned(),
        })
        .unwrap();
    assert!(items(&f.scan(&[CleanupCategory::BrowserCaches]).await).is_empty());
    assert!(sentinel.exists());
}

#[tokio::test]
async fn electron_allowlist_requires_installed_owner_and_keeps_offline_and_private_data() {
    let f = Fixture::new();
    f.app("ElectronTest", "org.example.electron");
    f.app("Visual Studio Code", "com.microsoft.VSCode");
    f.app("PlainApp", "org.example.plain");
    f.app("Cursor", "com.todesktop.cursor");
    f.app("Opaque", "com.1password.1password");
    let mut sentinel = vec![];
    for app in ["ElectronTest", "Code", "Cursor"] {
        sentinel.push(f.file(&format!("Library/Application Support/{app}/Preferences")));
        for leaf in [
            "Cache",
            "Code Cache",
            "GPUCache",
            "DawnGraphiteCache",
            "DawnWebGPUCache",
            "GrShaderCache",
            "ShaderCache",
        ] {
            f.file(&format!("Library/Application Support/{app}/{leaf}/data"));
        }
        for leaf in [
            "Service Worker/CacheStorage/offline",
            "Local Storage/data",
            "Cookies",
            "History",
            "Login Data",
            "Extensions/data",
        ] {
            sentinel.push(f.file(&format!("Library/Application Support/{app}/{leaf}")));
        }
    }
    f.file("Library/Application Support/Code/CachedData/data");
    f.file("Library/Application Support/Code/CachedExtensionVSIXs/data");
    sentinel.push(f.file("Library/Application Support/PlainApp/Cache/data"));
    sentinel.push(f.file("Library/Application Support/Unknown/Preferences"));
    sentinel.push(f.file("Library/Application Support/Unknown/Cache/data"));
    sentinel.push(f.file("Library/Application Support/org.example.electron/Preferences"));
    f.file("Library/Application Support/org.example.electron/Cache/data");
    sentinel.push(f.file("Library/Application Support/Opaque/Preferences"));
    sentinel.push(f.file("Library/Application Support/Opaque/Cache/data"));
    let protected = f
        .engine
        .protect(ProtectInput::Manual {
            kind: ProtectKind::Path,
            value: f
                .home
                .join("Library/Application Support/ElectronTest/GPUCache")
                .to_string_lossy()
                .into_owned(),
        })
        .unwrap();
    let report = f.scan(&[CleanupCategory::AppCaches]).await;
    assert_eq!(items(&report).len(), 16);
    assert_eq!(report.categories[0].groups.len(), 2);
    assert!(
        items(&report)
            .iter()
            .all(|i| !i.path.contains("Service Worker") && !i.path.contains("Cursor"))
    );
    f.engine.unprotect(&protected.id).unwrap();
    let app = f.home.join("Applications/ElectronTest.app");
    f.apps.running.lock().unwrap().push(Process {
        pid: 100,
        path: app.join("Contents/MacOS/ElectronTest"),
        bundle_id: "org.example.electron".into(),
    });
    *f.processes.table.lock().unwrap() = vec![command(
        100,
        1,
        "ElectronTest",
        &["ElectronTest", "--secret=sensitive"],
    )];
    let report = f.scan(&[CleanupCategory::AppCaches]).await;
    // Protected roots (the Cursor cache) are listed too now, so look only at what the running app holds.
    let running: Vec<_> = report.categories[0]
        .skipped
        .iter()
        .filter(|s| s.reason == SkippedGroupReason::Running)
        .collect();
    assert_eq!(running.len(), 1);
    assert_eq!(running[0].processes, ["ElectronTest"]);
    f.apps.running.lock().unwrap().clear();
    f.processes.table.lock().unwrap().clear();
    let report = f.scan(&[CleanupCategory::AppCaches]).await;
    f.execute(
        &report,
        items(&report).iter().map(|i| i.id.clone()).collect(),
    )
    .await;
    assert!(sentinel.iter().all(|p| p.exists()));
}

#[tokio::test]
async fn generic_runtimes_and_xcode_embedded_services_do_not_block_dev_caches() {
    let f = Fixture::new();
    let xcode = f.app("Xcode", "com.apple.dt.Xcode");
    for path in [
        "Library/Developer/Xcode/DerivedData/data",
        "Library/Developer/CoreSimulator/Caches/data",
        ".npm/_cacache/data",
        "Library/Caches/Yarn/data",
        "Library/Caches/pip/data",
        "Library/Caches/go-build/data",
        "Library/Caches/CocoaPods/data",
    ] {
        f.file(path);
    }
    let clang = xcode.join("Contents/Developer/Toolchains/XcodeDefault.xctoolchain/usr/bin/clang");
    f.apps.running.lock().unwrap().push(Process {
        pid: 1,
        bundle_id: "com.apple.dt.Xcode.clang".into(),
        path: clang.clone(),
    });
    {
        let mut paths = f.processes.paths.lock().unwrap();
        paths.push(clang);
        for name in [
            "node",
            "python",
            "python3",
            "python3.12",
            "ruby",
            "clang",
            "swift",
            "sourcekit-lsp",
            "CoreSimulatorService",
            "com.apple.CoreSimulator.CoreSimulatorService",
            "compile",
            "link",
        ] {
            paths.push(PathBuf::from("/synthetic/bin").join(name));
        }
    }
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    assert_eq!(items(&report).len(), 7);
    assert!(report.categories[0].skipped.is_empty());
    assert!(report.categories[0].complete);
}

#[tokio::test]
async fn real_tool_argv_and_direct_executables_still_block_their_caches() {
    for (cache, executable, argv) in [
        (
            ".npm/_cacache",
            "node",
            vec!["node", "/opt/npm/bin/npm-cli.js"],
        ),
        (
            ".npm/_cacache",
            "node",
            vec!["node", "/opt/npm/bin/npx-cli.js"],
        ),
        (
            "Library/Caches/Yarn",
            "node",
            vec!["node", "/path with spaces/yarn.js"],
        ),
        (
            "Library/Caches/Yarn",
            "node",
            vec!["node", "/opt/yarn/yarn.cjs"],
        ),
        (
            "Library/Caches/pip",
            "python3.12",
            vec!["python3.12", "-m", "pip", "install"],
        ),
        (
            "Library/Caches/pip",
            "python",
            vec!["python", "/opt/bin/pip3.12"],
        ),
        ("Library/Caches/pip", "pip3.12", vec!["pip3.12"]),
        (
            "Library/Caches/CocoaPods",
            "ruby",
            vec!["ruby", "/opt/bin/pod", "install"],
        ),
        ("Library/Caches/go-build", "go", vec!["go", "build"]),
        (
            "Library/Developer/Xcode/DerivedData",
            "XCBBuildService",
            vec!["XCBBuildService"],
        ),
        (
            "Library/Developer/Xcode/DerivedData",
            "SWBBuildService",
            vec!["SWBBuildService"],
        ),
        (
            "Library/Developer/Xcode/DerivedData",
            "swift-frontend",
            vec!["swift-frontend"],
        ),
        (
            "Library/Developer/CoreSimulator/Caches",
            "simctl",
            vec!["simctl"],
        ),
    ] {
        let f = Fixture::new();
        let file = f.file(&format!("{cache}/data"));
        f.processes.commands.lock().unwrap().push((
            PathBuf::from("/synthetic/bin").join(executable),
            Some(argv.into_iter().map(str::to_owned).collect()),
        ));
        let report = f.scan(&[CleanupCategory::DevCaches]).await;
        assert!(items(&report).is_empty(), "{executable}: {cache}");
        let skipped = &report.categories[0].skipped;
        assert_eq!(skipped.len(), 1, "{executable}: {cache}");
        assert_eq!(skipped[0].reason, SkippedGroupReason::Running);
        assert_eq!(skipped[0].bytes, Some(allocated(&file)));
        assert!(file.exists());
    }
}

#[tokio::test]
async fn argv_substrings_and_directory_components_do_not_match_tools() {
    let f = Fixture::new();
    for cache in [
        ".npm/_cacache",
        "Library/Caches/Yarn",
        "Library/Caches/pip",
        "Library/Caches/CocoaPods",
    ] {
        f.file(&format!("{cache}/data"));
    }
    for (name, argv) in [
        (
            "node",
            vec![
                "node",
                "/opt/pnpm-lock.yaml",
                "/opt/npm-global/server.js",
                "/opt/yarn/index.js",
                "--input=/opt/npm-cli.js",
            ],
        ),
        (
            "python3",
            vec!["python3", "/opt/pip/script.py", "pip", "pip3"],
        ),
        (
            "ruby",
            vec!["ruby", "/opt/pod/server.rb", "cocoapods", "podspec"],
        ),
    ] {
        f.processes.commands.lock().unwrap().push((
            PathBuf::from("/synthetic/bin").join(name),
            Some(argv.into_iter().map(str::to_owned).collect()),
        ));
    }
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    assert_eq!(items(&report).len(), 4);
    assert!(report.categories[0].skipped.is_empty());
}

#[tokio::test]
async fn cocoa_pods_cache_is_available_while_xcode_runs() {
    let f = Fixture::new();
    f.file("Library/Caches/CocoaPods/data");
    f.processes.paths.lock().unwrap().extend([
        PathBuf::from("/synthetic/bin/Xcode"),
        PathBuf::from("/synthetic/bin/xcodebuild"),
    ]);
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    assert_eq!(items(&report).len(), 1);
    assert!(report.categories[0].skipped.is_empty());
}

#[tokio::test]
async fn busy_homebrew_has_allocated_bytes_without_invoking_brew_or_following_links() {
    let f = Fixture::new();
    let cache = f.file("Library/Caches/Homebrew/downloads/archive");
    let outside = f.file("Documents/keep");
    symlink(&outside, cache.parent().unwrap().join("outside-link")).unwrap();
    f.processes
        .paths
        .lock()
        .unwrap()
        .push("/synthetic/bin/brew".into());
    // scan passes no DesktopCore: invoking any brew is impossible in this fixture.
    let report = f.scan(&[CleanupCategory::Homebrew]).await;
    assert!(items(&report).is_empty());
    assert!(report.categories[0].complete);
    assert_eq!(report.categories[0].skipped.len(), 1);
    let skipped = &report.categories[0].skipped[0];
    assert_eq!(skipped.title, "Homebrew");
    assert_eq!(skipped.reason, SkippedGroupReason::Running);
    assert_eq!(skipped.bytes, Some(allocated(&cache)));
    assert!(outside.exists());
    f.engine
        .protect(ProtectInput::Manual {
            kind: ProtectKind::Path,
            value: cache.to_string_lossy().into_owned(),
        })
        .unwrap();
    let report = f.scan(&[CleanupCategory::Homebrew]).await;
    assert!(report.categories.is_empty());
}

#[tokio::test]
async fn skipped_directories_merge_by_owner_and_reason_and_sum_bytes() {
    let f = Fixture::new();
    let xcode = f.app("Xcode", "com.apple.dt.Xcode");
    let mut bytes = 0;
    for cache in ["Library/Caches/Yarn", ".cache/yarn", ".yarn/berry/cache"] {
        bytes += allocated(&f.file(&format!("{cache}/data")));
    }
    f.processes.commands.lock().unwrap().push((
        "/synthetic/bin/node".into(),
        Some(vec!["node".into(), "/opt/yarn.js".into()]),
    ));
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    assert_eq!(report.categories[0].skipped.len(), 1);
    assert_eq!(report.categories[0].skipped[0].title, "Yarn");
    assert_eq!(report.categories[0].skipped[0].bytes, Some(bytes));
    f.processes.commands.lock().unwrap().clear();
    f.processes.open.lock().unwrap().extend([
        f.home.join("Library/Caches/Yarn/data"),
        f.home.join(".cache/yarn/data"),
    ]);
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    assert!(report.categories[0].skipped.is_empty());
    assert_eq!(items(&report).len(), 3);
    assert_eq!(
        items(&report)
            .iter()
            .map(|i| i.kept_open_files)
            .sum::<u64>(),
        2
    );
    assert_eq!(
        items(&report)
            .iter()
            .map(|i| i.size_bytes.unwrap())
            .sum::<u64>(),
        bytes / 3
    );
    let mut expected = 0;
    for cache in [
        "Library/Developer/Xcode/DerivedData",
        "Library/Developer/CoreSimulator/Caches",
        "Library/Developer/Xcode/iOS DeviceSupport/17.0",
    ] {
        expected += allocated(&f.file(&format!("{cache}/data")));
    }
    expected += allocated(&f.file("Library/Developer/Xcode/iOS DeviceSupport/18.0/data"));
    f.processes
        .paths
        .lock()
        .unwrap()
        .push(xcode.join("Contents/MacOS/Xcode"));
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    let xcode_skips: Vec<_> = report.categories[0]
        .skipped
        .iter()
        .filter(|g| g.reason == SkippedGroupReason::Running)
        .collect();
    assert_eq!(xcode_skips.len(), 1);
    assert_eq!(xcode_skips[0].title, "Xcode");
    assert!(xcode_skips[0].app_id.is_some());
    assert_eq!(xcode_skips[0].bytes, Some(expected));
}

#[tokio::test]
async fn tool_argv_is_checked_again_when_a_queued_cleanup_executes() {
    let f = Fixture::new();
    let file = f.file(".npm/_cacache/data");
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    f.processes.commands.lock().unwrap().push((
        "/synthetic/bin/node".into(),
        Some(vec!["node".into(), "/opt/bin/npm-cli.js".into()]),
    ));
    let task = f.execute(&report, vec![items(&report)[0].id.clone()]).await;
    assert_eq!(
        task.outcome.unwrap().skipped[0].reason,
        SkippedItemReason::Running
    );
    assert!(file.exists());
}

#[tokio::test]
async fn unknown_running_apps_use_outer_app_name_and_merge_container_caches() {
    let f = Fixture::new();
    let id = "com.logi.cp-dev-mgr";
    let a = f.file(&format!("Library/Caches/{id}/data"));
    let b = f.file(&format!("Library/Containers/{id}/Data/Library/Caches/data"));
    f.apps.running.lock().unwrap().push(Process {
        pid: 1,
        bundle_id: id.into(),
        path: f
            .home
            .join("Helpers/Logi Options+.app/Contents/Helpers/Agent.app/Contents/MacOS/agent"),
    });
    let report = f.scan(&[CleanupCategory::AppCaches]).await;
    assert!(items(&report).is_empty());
    assert_eq!(report.categories[0].skipped.len(), 1);
    let skipped = &report.categories[0].skipped[0];
    assert_eq!(skipped.title, "Logi Options+");
    assert_eq!(skipped.app_id, None);
    assert_eq!(skipped.reason, SkippedGroupReason::Running);
    assert_eq!(skipped.bytes, Some(allocated(&a) + allocated(&b)));
}
#[tokio::test]
async fn leftovers_review_old_high_confidence_active_service_and_shared_trash_restore_purge() {
    let f = Fixture::new();
    f.probe.loaded.store(true, Ordering::Release);
    let data = f.file("Library/Application Support/org.example.orphan/data");
    age(&data, 40);
    age(data.parent().unwrap(), 40);
    let service = f.home.join("Library/LaunchAgents/org.example.orphan.plist");
    let dict = plist::Dictionary::from_iter([
        ("Label", plist::Value::String("org.example.orphan".into())),
        (
            "Program",
            plist::Value::String(
                f.home
                    .join("Applications/Deleted.app/Contents/MacOS/gone")
                    .to_string_lossy()
                    .into(),
            ),
        ),
    ]);
    plist::Value::Dictionary(dict)
        .to_file_xml(&service)
        .unwrap();
    let system = f
        .engine
        .uninstall
        .roots
        .system_library
        .join("LaunchDaemons/org.example.orphan.plist");
    fs::create_dir_all(system.parent().unwrap()).unwrap();
    fs::copy(&service, &system).unwrap();
    let report = f.scan(&[CleanupCategory::Leftovers]).await;
    assert!(
        items(&report)
            .iter()
            .any(|i| i.reason == CleanupReason::OrphanService && i.selected)
    );
    assert!(
        items(&report)
            .iter()
            .all(|i| !i.needs_admin && i.delete_mode == DeleteMode::Trash)
    );
    assert!(
        items(&report)
            .iter()
            .any(|i| i.path == data.parent().unwrap().to_string_lossy() && i.selected)
    );
    let expected_bytes: u64 = items(&report).iter().filter_map(|i| i.size_bytes).sum();
    let task = f
        .execute(
            &report,
            items(&report).iter().map(|i| i.id.clone()).collect(),
        )
        .await;
    let outcome = task.outcome.unwrap();
    assert!(outcome.restorable);
    assert!(expected_bytes > 0);
    assert_eq!(outcome.freed_bytes, expected_bytes);
    assert_eq!(outcome.trashed_bytes, expected_bytes);
    assert_eq!(outcome.freed_bytes - outcome.trashed_bytes, 0);
    assert_eq!(f.files.bootouts.load(Ordering::Acquire), 1);
    assert!(system.exists());
    assert!(!data.exists());
    let sentinel = f.file(".Trash/unrelated");
    f.engine.uninstall.restore(&task.id).unwrap();
    assert!(data.exists());
    assert!(service.exists());
    let report = f.scan(&[CleanupCategory::Leftovers]).await;
    let task = f
        .execute(
            &report,
            items(&report).iter().map(|i| i.id.clone()).collect(),
        )
        .await;
    assert!(f.engine.uninstall.purge(&task.id).unwrap() > 0);
    assert!(sentinel.exists());
    assert!(system.exists());
}
#[tokio::test]
async fn duplicate_inode_and_selection_ids_cannot_delete_twice() {
    let f = Fixture::new();
    let path = f.file("Library/Logs/old.log");
    age(&path, 8);
    fs::hard_link(&path, path.with_extension("alias")).unwrap();
    let report = f.scan(&[CleanupCategory::Logs]).await;
    assert_eq!(items(&report).len(), 1);
    let id = items(&report)[0].id.clone();
    assert!(
        f.engine
            .enqueue(&f.queue, &report.report_id, vec![id.clone(), id.clone()])
            .is_err()
    );
    assert!(
        f.engine
            .enqueue(&f.queue, &report.report_id, vec!["../Documents".into()])
            .is_err()
    );
    assert_eq!(
        f.engine
            .cached(&report.report_id, report.expires_at)
            .unwrap_err()
            .code,
        "E_PLAN_EXPIRED"
    );
    assert!(
        f.engine
            .cached(&report.report_id, report.expires_at - 1)
            .is_ok()
    );
    f.execute(&report, vec![id.clone()]).await;
    assert!(
        f.engine
            .enqueue(&f.queue, &report.report_id, vec![id])
            .is_err()
    );
}

#[tokio::test]
async fn homebrew_command_uses_existing_fake_estimate_fixed_execution_and_report_shape() {
    let f = Fixture::new();
    let fake = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("tests/fake-brew/brew")
        .to_string_lossy()
        .into_owned();
    f.queue.settings.write().unwrap().brew_path = Some(fake.clone());
    let core = opennavo_desktop_lib::core::DesktopCore::open(
        f.home.join("core-data"),
        f.home.join("core-cache"),
        f.home.join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    core.settings.write().unwrap().brew_path = Some(fake);
    let report = f
        .engine
        .scan(
            "fake-brew".into(),
            Some(vec![CleanupCategory::Homebrew]),
            permissions(true),
            Some(core),
        )
        .await
        .unwrap();
    assert_eq!(items(&report).len(), 1);
    let item = items(&report)[0];
    assert_eq!(item.kind, CleanupItemKind::Command);
    assert!(item.path.is_empty());
    assert_eq!(item.command.as_deref(), Some("brew cleanup --prune=all"));
    assert_eq!(item.reason, CleanupReason::BrewCleanup);
    let task = f.execute(&report, vec![item.id.clone()]).await;
    assert_eq!(task.state, TaskState::Succeeded);
    assert!(task.outcome.as_ref().unwrap().deleted > 0);
    assert!(!task.outcome.unwrap().restorable);
    let log = fs::read_to_string(task.log_path.unwrap()).unwrap();
    assert!(log.contains("OPENNAVO_FAKE_BREW_CLEANUP_ARGS=[\"cleanup\", \"--prune=all\"]"));
    assert!(log.contains("deleted brew cleanup --prune=all"));
}

#[tokio::test]
async fn leftovers_latest_descendant_age_and_review_delete_setting() {
    let f = Fixture::new();
    f.probe.loaded.store(true, Ordering::Release);
    let data = f.file("Library/Application Support/org.example.orphan/data");
    age(data.parent().unwrap(), 40);
    let service = f.home.join("Library/LaunchAgents/org.example.orphan.plist");
    plist::Value::Dictionary(plist::Dictionary::from_iter([
        ("Label", plist::Value::String("org.example.orphan".into())),
        (
            "Program",
            plist::Value::String(
                f.home
                    .join("Applications/gone.app/Contents/MacOS/gone")
                    .to_string_lossy()
                    .into(),
            ),
        ),
    ]))
    .to_file_xml(&service)
    .unwrap();
    f.queue.settings.write().unwrap().cleanup_delete_mode = DeleteMode::Delete;
    let report = f.scan(&[CleanupCategory::Leftovers]).await;
    let data_item = items(&report)
        .into_iter()
        .find(|i| i.path == data.parent().unwrap().to_string_lossy())
        .unwrap();
    assert!(!data_item.selected);
    assert_eq!(data_item.delete_mode, DeleteMode::Delete);
    assert!(data_item.modified_at.unwrap() > opennavo_desktop_lib::uninstall::now() - 86_400_000);
    let task = f.execute(&report, vec![data_item.id.clone()]).await;
    assert_eq!(task.outcome.as_ref().unwrap().deleted, 1);
    assert!(!task.outcome.unwrap().restorable);
    assert_eq!(f.files.trash_calls.load(Ordering::Acquire), 0);
}
#[tokio::test]
async fn opaque_sensitive_app_bundle_and_bundle_protected_developer_are_excluded() {
    let f = Fixture::new();
    f.app("Cursor", "com.todesktop.230313mzl4w4u92");
    f.file("Library/Caches/com.todesktop.230313mzl4w4u92/data");
    assert!(items(&f.scan(&[CleanupCategory::AppCaches]).await).is_empty());
    f.file("Library/Developer/Xcode/DerivedData/project/data");
    f.engine
        .protect(ProtectInput::Manual {
            kind: ProtectKind::BundleId,
            value: "com.apple.dt.Xcode".into(),
        })
        .unwrap();
    assert!(items(&f.scan(&[CleanupCategory::DevCaches]).await).is_empty());
}

#[tokio::test]
async fn homebrew_protection_is_derived_and_suppresses_fixed_command() {
    let f = Fixture::new();
    let fake = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("tests/fake-brew/brew")
        .to_string_lossy()
        .into_owned();
    f.queue.settings.write().unwrap().brew_path = Some(fake.clone());
    let core = opennavo_desktop_lib::core::DesktopCore::open(
        f.home.join("core-data"),
        f.home.join("core-cache"),
        f.home.join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    core.settings.write().unwrap().brew_path = Some(fake);
    let report = f
        .engine
        .scan(
            "protect-brew".into(),
            Some(vec![CleanupCategory::Homebrew]),
            permissions(true),
            Some(core.clone()),
        )
        .await
        .unwrap();
    let id = items(&report)[0].id.clone();
    let task = f.run(&report, vec![id.clone()]);
    let rule = f
        .engine
        .protect(ProtectInput::Item {
            report_id: report.report_id.clone(),
            item_id: id,
        })
        .unwrap();
    assert_eq!(rule.kind, ProtectKind::Path);
    assert_eq!(
        rule.value,
        f.home.join("Library/Caches/Homebrew").to_string_lossy()
    );
    assert!(items(&f.engine.cached(&report.report_id, 0).unwrap().report).is_empty());
    opennavo_desktop_lib::cleanup::execute::run(
        f.queue.clone(),
        &task,
        Arc::new(AtomicBool::new(false)),
        Arc::new(tokio::sync::Notify::new()),
    )
    .await
    .unwrap();
    let task = f
        .queue
        .database
        .tasks()
        .unwrap()
        .into_iter()
        .find(|t| t.id == task.id)
        .unwrap();
    assert_eq!(
        task.outcome.unwrap().skipped[0].reason,
        SkippedItemReason::Protected
    );
    assert!(
        !fs::read_to_string(task.log_path.unwrap())
            .unwrap()
            .contains("OPENNAVO_FAKE_BREW_CLEANUP_ARGS=")
    );
    let report = f
        .engine
        .scan(
            "protected-brew".into(),
            Some(vec![CleanupCategory::Homebrew]),
            permissions(true),
            Some(core),
        )
        .await
        .unwrap();
    assert!(report.categories.is_empty());
}

#[tokio::test]
async fn incomplete_measurements_keep_all_candidates_and_latest_selection() {
    for (timeout, limit) in [(Duration::ZERO, 2_000_000), (Duration::from_secs(5), 1)] {
        let f = Fixture::limits(timeout, limit);
        for cache in [
            "Library/Developer/Xcode/DerivedData",
            "Library/Caches/go-build",
        ] {
            f.file(&format!("{cache}/one"));
            f.file(&format!("{cache}/two"));
        }
        for version in 1..=18 {
            for leaf in ["one", "two"] {
                f.file(&format!(
                    "Library/Developer/Xcode/iOS DeviceSupport/iPhone16,2 18.{version} (B)/{leaf}"
                ));
            }
        }
        let report = f.scan(&[CleanupCategory::DevCaches]).await;
        assert_eq!(items(&report).len(), 20);
        assert!(!report.categories[0].complete);
        let entry = f.engine.cached(&report.report_id, 0).unwrap();
        assert!(
            entry
                .items
                .values()
                .all(|i| i.tree.as_ref().unwrap().incomplete.is_some())
        );
        assert_eq!(
            items(&report)
                .iter()
                .filter(|i| i.reason == CleanupReason::DeviceSupportLatest && !i.selected)
                .count(),
            1
        );
        if limit == 1 {
            assert!(items(&report).iter().all(|i| i.size_bytes.unwrap() > 0));
        }
    }
}

#[tokio::test]
async fn unknown_cache_uses_open_files_and_probe_failure_keeps_known_apps_actionable() {
    let f = Fixture::new();
    f.app("Known", "org.example.known");
    let known = f.file("Library/Caches/org.example.known/data");
    let unknown = f.file("Library/Caches/vendor-cache/data");
    let report = f.scan(&[CleanupCategory::AppCaches]).await;
    assert_eq!(items(&report).len(), 2);
    f.processes.open.lock().unwrap().push(unknown.clone());
    let report = f.scan(&[CleanupCategory::AppCaches]).await;
    assert_eq!(items(&report).len(), 1);
    assert_eq!(
        report.categories[0].skipped[0].reason,
        SkippedGroupReason::InUse
    );
    f.processes.open_error.store(true, Ordering::Release);
    let report = f.scan(&[CleanupCategory::AppCaches]).await;
    assert_eq!(items(&report).len(), 1);
    assert!(items(&report)[0].path.contains("org.example.known"));
    assert_eq!(report.categories[0].skipped[0].title, "vendor-cache");
    assert!(report.categories[0].skipped[0].processes.is_empty());
    let known_task = f.execute(&report, vec![items(&report)[0].id.clone()]).await;
    assert_eq!(known_task.outcome.unwrap().deleted, 1);
    assert!(!known.exists());
    assert!(unknown.exists());
}

#[tokio::test]
async fn tool_cache_keeps_open_files_and_rechecks_newly_open_files_at_execution() {
    use std::os::unix::fs::MetadataExt;
    for tool in [
        "Library/Caches/go-build",
        ".npm/_cacache",
        "Library/Developer/Xcode/DerivedData",
    ] {
        let f = Fixture::new();
        let active = f.file(&format!("{tool}/00/executable/api"));
        let later = f.file(&format!("{tool}/00/other"));
        let free = f.file(&format!("{tool}/01/free"));
        *f.processes.table.lock().unwrap() = vec![
            command(10, 1, "go", &["go", "run", "./cmd/api"]),
            command(11, 10, "api", &["api"]),
        ];
        f.processes
            .openers
            .lock()
            .unwrap()
            .push((active.clone(), Some(11)));
        let report = f.scan(&[CleanupCategory::DevCaches]).await;
        assert!(report.categories[0].skipped.is_empty());
        let item = items(&report)[0];
        assert_eq!(item.kept_open_files, 1);
        assert_eq!(
            item.size_bytes,
            Some(
                fs::metadata(&later).unwrap().blocks() * 512
                    + fs::metadata(&free).unwrap().blocks() * 512
            )
        );
        f.processes.open.lock().unwrap().push(later.clone());
        // A newly opened file may also have changed; preserve it without losing the other candidates.
        fs::write(&later, [2u8; 131072]).unwrap();
        let expected = fs::metadata(&free).unwrap().blocks() * 512;
        let task = f.execute(&report, vec![item.id.clone()]).await;
        let outcome = task.outcome.unwrap();
        assert_eq!(outcome.freed_bytes, expected);
        assert_eq!(outcome.deleted, 1);
        assert!(active.exists());
        assert!(later.exists());
        assert!(!free.exists());
    }
}

#[tokio::test]
async fn large_cache_compacts_evidence_and_removes_only_safe_rebuildable_contents() {
    let f = Fixture::new();
    for i in 0..300 {
        f.file(&format!("Library/Caches/go-build/{i}"));
    }
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    let item = items(&report)[0];
    let entry = f.engine.cached(&report.report_id, 0).unwrap();
    assert_eq!(
        entry.items[&item.id].tree.as_ref().unwrap().snapshots.len(),
        1
    );
    let outside = f.file("Documents/sentinel");
    let link = f.home.join("Library/Caches/go-build/new-link");
    symlink(&outside, &link).unwrap();
    let active = f.home.join("Library/Caches/go-build/0");
    f.processes.open.lock().unwrap().push(active.clone());
    let task = f.execute(&report, vec![item.id.clone()]).await;
    assert_eq!(task.outcome.unwrap().deleted, 1);
    assert!(outside.exists());
    assert!(link.symlink_metadata().unwrap().is_symlink());
    assert!(active.exists());
    assert!(!f.home.join("Library/Caches/go-build/299").exists());
}

#[tokio::test]
async fn dev_caches_ignore_sensitive_names_inside_fixed_locations() {
    // Real symbol sets contain NetworkExtension and VPN frameworks; DerivedData may build a module called Cursor.
    // Those names must not hide a whole rebuildable location (C2.2).
    let f = Fixture::new();
    f.file(
        "Library/Developer/Xcode/iOS DeviceSupport/iPhone16,2 27.0 (A)/Symbols/System/Library/Frameworks/NetworkExtension.framework/NetworkExtension",
    );
    f.file(
        "Library/Developer/Xcode/iOS DeviceSupport/iPhone16,2 26.5 (B)/Symbols/System/Library/PrivateFrameworks/VPNUtilities.framework/VPNUtilities",
    );
    f.file("Library/Developer/Xcode/DerivedData/App-abc/Build/Products/Debug/CursorKit.framework/CursorKit");
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    let paths: Vec<_> = items(&report).iter().map(|i| i.path.clone()).collect();
    assert!(
        paths.iter().any(|p| p.ends_with("iPhone16,2 27.0 (A)")),
        "{paths:?}"
    );
    assert!(
        paths.iter().any(|p| p.ends_with("iPhone16,2 26.5 (B)")),
        "{paths:?}"
    );
    assert!(
        paths.iter().any(|p| p.ends_with("Xcode/DerivedData")),
        "{paths:?}"
    );
    assert!(report.categories[0].skipped.is_empty());
}

#[tokio::test]
async fn app_cache_with_a_protected_descendant_is_listed_as_protected_not_dropped() {
    let f = Fixture::new();
    f.file("Library/Caches/com.example.tool/History/data");
    let report = f.scan(&[CleanupCategory::AppCaches]).await;
    assert!(
        items(&report)
            .iter()
            .all(|i| !i.path.contains("com.example.tool"))
    );
    let category = &report.categories[0];
    let skipped = category
        .skipped
        .iter()
        .find(|s| s.title.contains("com.example.tool"))
        .expect("protected cache must stay visible in the report");
    assert_eq!(skipped.reason, SkippedGroupReason::Protected);
}

#[tokio::test]
async fn download_tool_caches_are_listed_unchecked_and_not_twice() {
    let f = Fixture::new();
    for path in [
        ".gradle/caches/modules-2/files-2.1/a.jar",
        ".m2/repository/org/example/a.jar",
        ".cargo/registry/cache/index/a.crate",
        "Library/Caches/ms-playwright/chromium-1/chrome",
    ] {
        f.file(path);
    }
    let report = f
        .scan(&[CleanupCategory::DevCaches, CleanupCategory::AppCaches])
        .await;
    let downloads: Vec<_> = items(&report)
        .into_iter()
        .filter(|i| i.reason == CleanupReason::ToolDownloadCache)
        .collect();
    assert_eq!(downloads.len(), 4, "{downloads:?}");
    assert!(downloads.iter().all(|i| !i.selected));
    // Playwright's browsers sit under Library/Caches but belong to the download group only.
    let app_caches = report
        .categories
        .iter()
        .find(|c| c.key == CleanupCategory::AppCaches);
    assert!(app_caches.is_none_or(|c| {
        c.groups
            .iter()
            .all(|g| g.items.iter().all(|i| !i.path.contains("ms-playwright")))
    }));
}

#[tokio::test]
async fn only_a_gradle_daemon_holds_the_gradle_cache() {
    let f = Fixture::new();
    f.file(".gradle/caches/modules-2/a.jar");
    f.processes.commands.lock().unwrap().push((
        "/usr/bin/java".into(),
        Some(vec![
            "java".into(),
            "-jar".into(),
            "/Applications/Some.app/app.jar".into(),
        ]),
    ));
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    assert!(
        items(&report)
            .iter()
            .any(|i| i.path.ends_with(".gradle/caches"))
    );
    f.processes.commands.lock().unwrap().push((
        "/usr/bin/java".into(),
        Some(vec![
            "java".into(),
            "-cp".into(),
            "/Users/me/.gradle/wrapper/dists/gradle-8.7/lib/gradle-launcher-8.7.jar".into(),
            "org.gradle.launcher.daemon.bootstrap.GradleDaemon".into(),
            "8.7".into(),
        ]),
    ));
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    assert!(
        items(&report)
            .iter()
            .all(|i| !i.path.ends_with(".gradle/caches"))
    );
    assert!(
        report.categories[0]
            .skipped
            .iter()
            .any(|s| s.title == "Gradle" && s.reason == SkippedGroupReason::Running)
    );
}

#[tokio::test]
async fn read_only_go_module_directories_are_removed() {
    use std::os::unix::fs::PermissionsExt;
    let f = Fixture::new();
    let file = f.file("go/pkg/mod/example.com/m@v1.0.0/go.mod");
    // Go marks extracted modules read-only.
    fs::set_permissions(&file, fs::Permissions::from_mode(0o444)).unwrap();
    for dir in [
        file.parent().unwrap(),
        file.parent().unwrap().parent().unwrap(),
    ] {
        fs::set_permissions(dir, fs::Permissions::from_mode(0o555)).unwrap();
    }
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    let item = items(&report)
        .into_iter()
        .find(|i| i.path.ends_with("go/pkg/mod"))
        .expect("go module cache listed");
    let task = f.execute(&report, vec![item.id.clone()]).await;
    assert!(task.outcome.unwrap().skipped.is_empty());
    assert!(!f.home.join("go/pkg/mod").exists());
}

#[tokio::test]
async fn an_open_lock_keeps_only_itself_in_the_go_module_cache_but_holds_a_whole_test_browser() {
    let f = Fixture::new();
    f.file("go/pkg/mod/example.com/m@v1.0.0/go.mod");
    let lock = f.file("go/pkg/mod/cache/lock");
    let chrome = f.file("Library/Caches/ms-playwright/chromium-1/chrome");
    f.processes
        .open
        .lock()
        .unwrap()
        .extend([lock.clone(), chrome.clone()]);
    let report = f.scan(&[CleanupCategory::DevCaches]).await;
    let modules = items(&report)
        .into_iter()
        .find(|i| i.path.ends_with("go/pkg/mod"))
        .expect("module cache stays selectable around its open lock");
    assert_eq!(modules.kept_open_files, 1);
    assert!(
        items(&report)
            .iter()
            .all(|i| !i.path.contains("ms-playwright"))
    );
    assert!(
        report.categories[0]
            .skipped
            .iter()
            .any(|s| s.title == "Playwright" && s.reason == SkippedGroupReason::InUse)
    );
}

/// Ages a file and every folder from it up to (and including) `top`, like a project nobody opened for a while.
fn age_up_to(file: &Path, top: &Path, days: u64) {
    let mut current = Some(file);
    while let Some(path) = current {
        age(path, days);
        if path == top {
            break;
        }
        current = path.parent();
    }
}
fn write_plist(path: &Path, entries: &[(&str, &str)]) {
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    plist::Value::Dictionary(plist::Dictionary::from_iter(
        entries
            .iter()
            .map(|(k, v)| (k.to_string(), plist::Value::String(v.to_string()))),
    ))
    .to_file_xml(path)
    .unwrap();
}

#[tokio::test]
async fn temp_offers_only_old_plain_entries() {
    let f = Fixture::new();
    let root = f.home.parent().unwrap().join("T");
    *f.processes.user_temp.lock().unwrap() = Some(root.clone());
    for (name, days) in [("old-profile", 10), ("fresh", 0), ("com.apple.service", 10)] {
        let file = root.join(name).join("data");
        fs::create_dir_all(file.parent().unwrap()).unwrap();
        fs::write(&file, b"x").unwrap();
        age_up_to(&file, &root.join(name), days);
    }
    // Empty leftovers free nothing and are not listed.
    let empty = root.join("old-empty");
    fs::create_dir_all(&empty).unwrap();
    age(&empty, 10);
    let report = f.scan(&[CleanupCategory::Temp]).await;
    let found: Vec<_> = items(&report);
    assert_eq!(found.len(), 1, "{found:?}");
    assert!(found[0].path.ends_with("old-profile"));
    assert_eq!(found[0].reason, CleanupReason::TempFile);
    assert!(found[0].selected);
}

#[tokio::test]
async fn installers_are_picked_when_their_app_is_installed_and_go_to_the_trash() {
    let f = Fixture::new();
    f.app("Docker", "com.docker.docker");
    let docker = f.file("Downloads/Docker-arm64-v4.30.dmg");
    let other = f.file("Downloads/Mystery-2.0.pkg");
    f.file("Downloads/notes.txt");
    let report = f.scan(&[CleanupCategory::Installers]).await;
    assert_eq!(report.categories[0].risk, CleanupRisk::Review);
    let found = items(&report);
    assert_eq!(found.len(), 2, "{found:?}");
    let pick = |path: &Path| {
        found
            .iter()
            .find(|i| i.path == path.to_string_lossy())
            .unwrap()
    };
    assert_eq!(pick(&docker).reason, CleanupReason::InstallerInstalled);
    assert!(pick(&docker).selected);
    assert_eq!(pick(&other).reason, CleanupReason::Installer);
    assert!(!pick(&other).selected);
    assert!(found.iter().all(|i| i.delete_mode == DeleteMode::Trash));
}

#[tokio::test]
async fn project_output_of_old_projects_is_offered_and_recent_projects_keep_theirs() {
    let f = Fixture::new();
    let old = f.file("code/old-app/package.json");
    // A package name like this must not hide the whole node_modules folder.
    let module = f.file("code/old-app/node_modules/cursor-kit/index.js");
    age_up_to(&module, &f.home.join("code/old-app"), 60);
    age(&old, 60);
    f.file("code/new-app/package.json");
    f.file("code/new-app/node_modules/a/index.js");
    f.file("code/new-lib/package.json");
    fs::create_dir_all(f.home.join("code/new-lib/node_modules")).unwrap();
    let report = f.scan(&[CleanupCategory::ProjectArtifacts]).await;
    let category = &report.categories[0];
    assert_eq!(category.risk, CleanupRisk::Review);
    let offered: Vec<_> = items(&report);
    assert_eq!(offered.len(), 1, "{offered:?}");
    assert!(offered[0].path.ends_with("code/old-app/node_modules"));
    assert!(offered[0].selected);
    assert_eq!(offered[0].delete_mode, DeleteMode::Delete);
    assert_eq!(
        category.groups[0].subtitle.as_deref(),
        Some("~/code/old-app")
    );
    let recent = category
        .skipped
        .iter()
        .find(|s| s.title == "new-app")
        .expect("recent project listed");
    assert_eq!(recent.reason, SkippedGroupReason::Recent);
    assert!(recent.bytes.is_some_and(|b| b > 0));
    assert!(category.skipped.iter().all(|s| s.title != "new-lib"));
}

#[tokio::test]
async fn trash_items_are_listed_unchecked() {
    let f = Fixture::new();
    f.file(".Trash/old.zip");
    f.file(".Trash/Folder/file");
    f.file(".Trash/.DS_Store");
    fs::write(f.home.join(".Trash/empty.txt"), b"").unwrap();
    let report = f.scan(&[CleanupCategory::Trash]).await;
    let found = items(&report);
    assert_eq!(found.len(), 2);
    assert!(
        found
            .iter()
            .all(|i| i.reason == CleanupReason::Trash && !i.selected)
    );
}

#[tokio::test]
async fn backups_and_archives_are_personal_unchecked_and_go_to_the_trash() {
    let f = Fixture::new();
    let backup = f
        .home
        .join("Library/Application Support/MobileSync/Backup/ABC123");
    write_plist(
        &backup.join("Info.plist"),
        &[
            ("Device Name", "Test iPhone"),
            ("Product Type", "iPhone16,2"),
        ],
    );
    f.file("Library/Application Support/MobileSync/Backup/ABC123/00/data");
    let archive = f
        .home
        .join("Library/Developer/Xcode/Archives/2026-01-01/App 1-1-26.xcarchive");
    write_plist(&archive.join("Info.plist"), &[("Name", "App")]);
    f.file("Library/Developer/Xcode/Archives/2026-01-01/App 1-1-26.xcarchive/Products/Applications/App.app/VPNKit");
    let report = f.scan(&[CleanupCategory::DeviceBackups]).await;
    let category = &report.categories[0];
    assert_eq!(category.risk, CleanupRisk::Personal);
    let titles: Vec<_> = category.groups.iter().map(|g| g.title.clone()).collect();
    assert!(titles.contains(&"Test iPhone".to_string()), "{titles:?}");
    assert!(titles.contains(&"App".to_string()), "{titles:?}");
    let found = items(&report);
    assert_eq!(found.len(), 2);
    assert!(
        found
            .iter()
            .all(|i| !i.selected && i.delete_mode == DeleteMode::Trash)
    );
    assert!(
        found
            .iter()
            .any(|i| i.reason == CleanupReason::XcodeArchive)
    );
}

#[tokio::test]
async fn a_protected_cache_root_is_listed_instead_of_vanishing() {
    let f = Fixture::new();
    f.file("Library/Caches/com.nssurge.surge-mac/data");
    let report = f.scan(&[CleanupCategory::AppCaches]).await;
    assert!(items(&report).iter().all(|i| !i.path.contains("surge")));
    assert!(
        report.categories[0]
            .skipped
            .iter()
            .any(|s| s.title.contains("surge") && s.reason == SkippedGroupReason::Protected)
    );
}

#[tokio::test]
async fn large_project_output_download_caches_and_trash_are_removed_not_reported_changed() {
    // Past 256 entries the scan keeps only the root identity; execution walks the tree again with the careful remover.
    let f = Fixture::new();
    let project = f.file("code/old-app/package.json");
    age(&project, 60);
    for i in 0..300 {
        f.file(&format!("code/old-app/node_modules/pkg-{i}/index.js"));
        f.file(&format!(".gradle/caches/modules-2/files-2.1/lib-{i}.jar"));
        f.file(&format!(".Trash/Old Folder/{i}"));
    }
    // Names like these say nothing about user secrets inside generated or discarded trees.
    f.file("code/old-app/node_modules/@anthropic-ai/claude-code/cli.js");
    f.file("go/pkg/mod/github.com/docker/cli@v1.0.0/go.mod");
    f.file(".Trash/Old Folder/Cookies");
    let report = f
        .scan(&[
            CleanupCategory::DevCaches,
            CleanupCategory::ProjectArtifacts,
            CleanupCategory::Trash,
        ])
        .await;
    let ids: Vec<_> = items(&report).iter().map(|i| i.id.clone()).collect();
    assert_eq!(ids.len(), 4, "{:?}", items(&report));
    let outcome = f.execute(&report, ids).await.outcome.unwrap();
    assert!(outcome.skipped.is_empty(), "{:?}", outcome.skipped);
    assert_eq!(outcome.deleted, 4);
    for path in [
        "code/old-app/node_modules",
        ".gradle/caches",
        "go/pkg/mod",
        ".Trash/Old Folder",
    ] {
        assert!(!f.home.join(path).exists(), "{path}");
    }
    assert!(project.exists());
    assert!(f.home.join(".Trash").is_dir());
}

#[tokio::test]
async fn installers_backups_and_archives_go_to_the_trash_even_when_large_or_named_like_an_app() {
    let f = Fixture::new();
    f.app("Docker", "com.docker.docker");
    let installer = f.file("Downloads/Docker-arm64-v4.30.dmg");
    let backup = f
        .home
        .join("Library/Application Support/MobileSync/Backup/ABC123");
    write_plist(
        &backup.join("Info.plist"),
        &[("Device Name", "Test iPhone")],
    );
    for i in 0..300 {
        f.file(&format!(
            "Library/Application Support/MobileSync/Backup/ABC123/{:02x}/{i}",
            i % 16
        ));
    }
    let archive = f
        .home
        .join("Library/Developer/Xcode/Archives/2026-01-01/App 1-1-26.xcarchive");
    write_plist(&archive.join("Info.plist"), &[("Name", "App")]);
    f.file("Library/Developer/Xcode/Archives/2026-01-01/App 1-1-26.xcarchive/Products/Applications/App.app/VPNKit");
    let report = f
        .scan(&[CleanupCategory::Installers, CleanupCategory::DeviceBackups])
        .await;
    let ids: Vec<_> = items(&report).iter().map(|i| i.id.clone()).collect();
    assert_eq!(ids.len(), 3, "{:?}", items(&report));
    let entry = f.engine.cached(&report.report_id, 0).unwrap();
    assert!(
        entry
            .items
            .values()
            .any(|i| i.tree.as_ref().unwrap().compact)
    );
    let outcome = f.execute(&report, ids).await.outcome.unwrap();
    assert!(outcome.skipped.is_empty(), "{:?}", outcome.skipped);
    assert_eq!(outcome.trashed, 3);
    assert!(outcome.restorable);
    for path in [&installer, &backup, &archive] {
        assert!(!path.exists(), "{}", path.display());
    }
    assert_eq!(f.files.trash_calls.load(Ordering::Acquire), 3);
}

/// A stand-in for the Docker CLI: `system df` prints `~/.fake-docker/df` (or fails while `~/.fake-docker/down` exists),
/// and a prune records its arguments and zeroes that type, as Docker would report afterwards.
fn fake_docker(f: &Fixture) -> PathBuf {
    let state = f.home.join(".fake-docker");
    fs::create_dir_all(&state).unwrap();
    fs::write(
        state.join("df"),
        r#"{"Active":"30","Reclaimable":"87.33GB (77%)","Size":"112.1GB","TotalCount":"325","Type":"Images"}
{"Active":"4","Reclaimable":"300.1MB (99%)","Size":"300.1MB","TotalCount":"47","Type":"Containers"}
{"Active":"41","Reclaimable":"24.95GB (65%)","Size":"38.05GB","TotalCount":"135","Type":"Local Volumes"}
{"Active":"22","Reclaimable":"21.1GB","Size":"117.1GB","TotalCount":"2849","Type":"Build Cache"}
"#,
    )
    .unwrap();
    std::os::unix::net::UnixListener::bind(state.join("daemon.sock")).unwrap();
    fs::write(
        state.join("endpoint"),
        format!("\"unix://{}\"", state.join("daemon.sock").display()),
    )
    .unwrap();
    let script = state.join("docker");
    fs::write(
        &script,
        r#"#!/bin/sh
state="$HOME/.fake-docker"
echo "$*" >> "$state/invocations"
if [ "$1 $2" = "context inspect" ]; then cat "$state/endpoint"; exit 0; fi
[ "$1" = "--host" ] || exit 3
[ "$2" = "unix://$state/daemon.sock" ] || exit 4
shift 2
case "$1 $2" in
  "system df")
    [ -e "$state/down" ] && { echo "Cannot connect to the Docker daemon" >&2; exit 1; }
    cat "$state/df" ;;
  "image prune") type=Images ;;
  "builder prune") type="Build Cache" ;;
  "container prune") type=Containers ;;
  *) exit 2 ;;
esac
if [ -n "$type" ]; then
  echo "$*" >> "$state/calls"
  sed -i '' "/\"Type\":\"$type\"/s/\"Reclaimable\":\"[^\"]*\"/\"Reclaimable\":\"0B\"/" "$state/df"
  echo "Total reclaimed space: 1GB"
fi
"#,
    )
    .unwrap();
    use std::os::unix::fs::PermissionsExt;
    fs::set_permissions(&script, fs::Permissions::from_mode(0o755)).unwrap();
    *f.processes.docker.lock().unwrap() = Some(script.clone());
    f.processes
        .paths
        .lock()
        .unwrap()
        .push(PathBuf::from(DOCKER_ENGINE));
    script
}
const DOCKER_ENGINE: &str = "/Applications/Docker.app/Contents/MacOS/com.docker.backend";

#[tokio::test]
async fn docker_space_is_listed_unchecked_and_volumes_are_only_shown() {
    let f = Fixture::new();
    f.app("Docker", "com.docker.docker");
    // Without a Docker CLI the category does not appear at all.
    let report = f.scan(&[CleanupCategory::Docker]).await;
    assert!(report.categories.is_empty());
    fake_docker(&f);
    let report = f.scan(&[CleanupCategory::Docker]).await;
    let category = &report.categories[0];
    assert_eq!(category.key, CleanupCategory::Docker);
    assert_eq!(category.risk, CleanupRisk::Review);
    let found = items(&report);
    let commands: Vec<_> = found
        .iter()
        .map(|i| (i.reason, i.command.clone().unwrap(), i.size_bytes))
        .collect();
    assert_eq!(
        commands,
        vec![
            (
                CleanupReason::DockerImages,
                "docker image prune --all --force".to_string(),
                Some(87_330_000_000)
            ),
            (
                CleanupReason::DockerBuildCache,
                "docker builder prune --all --force".to_string(),
                Some(21_100_000_000)
            ),
            (
                CleanupReason::DockerContainers,
                "docker container prune --force".to_string(),
                Some(300_100_000)
            ),
        ]
    );
    assert!(
        found
            .iter()
            .all(|i| !i.selected && i.kind == CleanupItemKind::Command)
    );
    assert_eq!(category.bytes, 108_730_100_000);
    // Volumes can hold databases: measured and shown, never offered.
    assert_eq!(category.skipped.len(), 1);
    assert_eq!(category.skipped[0].reason, SkippedGroupReason::Volumes);
    assert_eq!(category.skipped[0].bytes, Some(24_950_000_000));

    // A Docker that is installed but not running is one line, not a silent gap.
    fs::write(f.home.join(".fake-docker/down"), b"").unwrap();
    let report = f.scan(&[CleanupCategory::Docker]).await;
    let category = &report.categories[0];
    assert!(category.groups.is_empty());
    assert_eq!(category.skipped[0].reason, SkippedGroupReason::ToolStopped);
    fs::remove_file(f.home.join(".fake-docker/down")).unwrap();

    // With no engine running the CLI is never called: some engines would start a virtual machine for it.
    f.processes.paths.lock().unwrap().clear();
    fs::remove_file(f.home.join(".fake-docker/invocations")).unwrap();
    // Docker Desktop is installed: one line asks to open it.
    let report = f.scan(&[CleanupCategory::Docker]).await;
    assert_eq!(
        report.categories[0].skipped[0].reason,
        SkippedGroupReason::ToolStopped
    );
    assert!(!f.home.join(".fake-docker/invocations").exists());
    f.processes
        .paths
        .lock()
        .unwrap()
        .push(PathBuf::from(DOCKER_ENGINE));

    // Always Keep on a Docker line keeps all of Docker off the report.
    let report = f.scan(&[CleanupCategory::Docker]).await;
    f.engine
        .protect(ProtectInput::Item {
            report_id: report.report_id.clone(),
            item_id: items(&report)[0].id.clone(),
        })
        .unwrap();
    let report = f.scan(&[CleanupCategory::Docker]).await;
    assert!(report.categories.is_empty());

    // A bare CLI with no engine running (remote contexts, Colima stopped) has nothing to open and stays quiet.
    let bare = Fixture::new();
    fake_docker(&bare);
    bare.processes.paths.lock().unwrap().clear();
    let report = bare.scan(&[CleanupCategory::Docker]).await;
    assert!(report.categories.is_empty());
    assert!(!bare.home.join(".fake-docker/invocations").exists());
}

#[tokio::test]
async fn docker_prunes_run_fixed_commands_and_report_what_docker_freed() {
    let f = Fixture::new();
    fake_docker(&f);
    let report = f.scan(&[CleanupCategory::Docker]).await;
    let pick = |reason| {
        items(&report)
            .iter()
            .find(|i| i.reason == reason)
            .unwrap()
            .id
            .clone()
    };
    let ids = vec![
        pick(CleanupReason::DockerImages),
        pick(CleanupReason::DockerBuildCache),
    ];
    // Switching the active context after confirmation must not redirect df or prune.
    fs::write(f.home.join(".fake-docker/endpoint"), "\"ssh://production\"").unwrap();
    let outcome = f.execute(&report, ids).await.outcome.unwrap();
    fs::write(
        f.home.join(".fake-docker/endpoint"),
        format!(
            "\"unix://{}\"",
            f.home.join(".fake-docker/daemon.sock").display()
        ),
    )
    .unwrap();
    assert!(outcome.skipped.is_empty(), "{:?}", outcome.skipped);
    assert_eq!(outcome.deleted, 2);
    assert_eq!(outcome.freed_bytes, 87_330_000_000 + 21_100_000_000);
    assert_eq!(
        fs::read_to_string(f.home.join(".fake-docker/calls")).unwrap(),
        "image prune --all --force\nbuilder prune --all --force\n"
    );
    // Stopped containers were not picked and stay.
    let report = f.scan(&[CleanupCategory::Docker]).await;
    let left: Vec<_> = items(&report).iter().map(|i| i.reason).collect();
    assert_eq!(left, vec![CleanupReason::DockerContainers]);

    // Docker quit after the scan: the prune is skipped, nothing runs.
    f.processes.paths.lock().unwrap().clear();
    let outcome = f
        .execute(&report, vec![items(&report)[0].id.clone()])
        .await
        .outcome
        .unwrap();
    assert_eq!(outcome.deleted, 0);
    assert_eq!(
        outcome.skipped[0].reason,
        SkippedItemReason::ToolUnavailable
    );
    assert!(
        !fs::read_to_string(f.home.join(".fake-docker/calls"))
            .unwrap()
            .contains("container")
    );
}

#[tokio::test]
async fn docker_remote_context_is_never_measured_or_pruned() {
    let f = Fixture::new();
    fake_docker(&f);
    for host in [
        "ssh://production",
        "tcp://127.0.0.1:2375",
        "unix://relative.sock",
    ] {
        fs::write(
            f.home.join(".fake-docker/endpoint"),
            serde_json::to_string(host).unwrap(),
        )
        .unwrap();
        let report = f.scan(&[CleanupCategory::Docker]).await;
        assert!(items(&report).is_empty());
    }
    let calls = fs::read_to_string(f.home.join(".fake-docker/invocations")).unwrap();
    assert!(
        calls
            .lines()
            .all(|line| line.starts_with("context inspect "))
    );
    assert!(!f.home.join(".fake-docker/calls").exists());
}

#[tokio::test]
async fn docker_socket_replaced_after_scan_is_not_pruned() {
    let f = Fixture::new();
    fake_docker(&f);
    let report = f.scan(&[CleanupCategory::Docker]).await;
    let socket = f.home.join(".fake-docker/daemon.sock");
    fs::rename(&socket, socket.with_extension("old")).unwrap();
    let _listener = std::os::unix::net::UnixListener::bind(socket).unwrap();
    let outcome = f
        .execute(&report, vec![items(&report)[0].id.clone()])
        .await
        .outcome
        .unwrap();
    assert_eq!(outcome.deleted, 0);
    assert!(!outcome.skipped.is_empty());
    assert!(!f.home.join(".fake-docker/calls").exists());
}
