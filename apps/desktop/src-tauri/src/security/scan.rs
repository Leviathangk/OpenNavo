use super::{Service, files, types::*};
use crate::{AppError, events::CoreEvent, uninstall::rules::Snapshot};
use rusqlite::params;
use std::{
    collections::BTreeSet,
    fs,
    io::{Seek, SeekFrom},
    path::{Path, PathBuf},
    sync::{Arc, atomic::AtomicBool},
    time::{Duration, Instant},
};

#[path = "scan_worker.rs"]
mod worker;

// One heartbeat per scan, not per file. Stop and join before publishing the
// terminal result, so late progress can never resurrect a finished scan.
struct ScanHeartbeat {
    progress: Arc<std::sync::Mutex<AvProgress>>,
    active: Arc<std::sync::Mutex<std::collections::BTreeMap<usize, String>>>,
    stop: std::sync::mpsc::Sender<()>,
    thread: Option<std::thread::JoinHandle<()>>,
}
impl ScanHeartbeat {
    fn new(progress: AvProgress, sink: crate::events::EventSink) -> Self {
        let progress = Arc::new(std::sync::Mutex::new(progress));
        let shared = progress.clone();
        let active = Arc::new(std::sync::Mutex::new(std::collections::BTreeMap::<
            usize,
            String,
        >::new()));
        let active_paths = active.clone();
        let (stop, receiver) = std::sync::mpsc::channel();
        let thread = std::thread::spawn(move || {
            while matches!(
                receiver.recv_timeout(Duration::from_millis(500)),
                Err(std::sync::mpsc::RecvTimeoutError::Timeout)
            ) {
                let mut value = shared.lock().unwrap_or_else(|e| e.into_inner()).clone();
                value.current_path = active_paths
                    .lock()
                    .unwrap_or_else(|e| e.into_inner())
                    .values()
                    .next()
                    .cloned()
                    .unwrap_or_default();
                sink(CoreEvent::AvProgress(value));
            }
        });
        Self {
            progress,
            active,
            stop,
            thread: Some(thread),
        }
    }
    fn begin(&self, slot: usize, path: &Path) {
        self.active
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .insert(slot, path.to_string_lossy().into_owned());
    }
    fn end(&self, slot: usize) {
        self.active
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .remove(&slot);
    }
    fn update(&self, result: &AvScanResult) {
        let mut progress = self.progress.lock().unwrap_or_else(|e| e.into_inner());
        progress.cached_files = result.metrics.cached_files;
        progress.files_scanned = result.files_scanned;
        progress.bytes_scanned = result.bytes_scanned;
        progress.findings = result.findings.len() as u64;
    }
}
impl Drop for ScanHeartbeat {
    fn drop(&mut self) {
        let _ = self.stop.send(());
        if let Some(thread) = self.thread.take() {
            let _ = thread.join();
        }
    }
}

fn retain_scan_error(current: &mut Option<AppError>, error: AppError) {
    // Producer cancellation can arrive before the worker's actual failure.
    // Preserve the cause instead of reporting an internal abort as user cancel.
    if current.as_ref().is_none_or(|previous| {
        previous.code == "E_SCAN_CANCELED" && error.code != "E_SCAN_CANCELED"
    }) {
        *current = Some(error);
    }
}

pub fn roots(
    kind: AvScanKind,
    home: &Path,
    paths: Option<Vec<String>>,
    startup: &[StartupItem],
) -> Result<Vec<PathBuf>, AppError> {
    let mut temporary = super::scan_policy::temporary_roots().into_iter();
    let applications = vec![PathBuf::from("/Applications"), home.join("Applications")];
    let full = [
        "/Applications",
        "/Library",
        "/Users",
        "/usr/local",
        "/opt",
        "/private/var",
    ]
    .into_iter()
    .map(PathBuf::from)
    .collect::<Vec<_>>();
    let mut roots = roots_at(
        kind,
        home,
        paths,
        startup,
        &applications,
        &full,
        temporary.next(),
    )?;
    if kind == AvScanKind::Quick {
        for path in temporary {
            roots.insert(2, path);
        }
    }
    Ok(roots)
}
fn roots_at(
    kind: AvScanKind,
    home: &Path,
    paths: Option<Vec<String>>,
    startup: &[StartupItem],
    applications: &[PathBuf],
    full: &[PathBuf],
    temporary: Option<PathBuf>,
) -> Result<Vec<PathBuf>, AppError> {
    let roots = match kind {
        AvScanKind::Custom => {
            let paths =
                paths.ok_or_else(|| AppError::new("E_INVALID_ARG", "scan_paths_required"))?;
            if paths.is_empty() || paths.len() > 128 {
                return Err(AppError::new("E_INVALID_ARG", "scan_paths_count"));
            }
            paths.into_iter().map(PathBuf::from).collect()
        }
        AvScanKind::Quick => {
            let mut r = vec![home.join("Downloads"), home.join("Desktop")];
            r.extend(temporary);
            r.extend(
                startup
                    .iter()
                    .filter_map(|i| i.program.as_ref().map(PathBuf::from)),
            );
            for root in applications {
                if files::safe_metadata(root).is_ok()
                    && let Ok(entries) = fs::read_dir(root)
                {
                    r.extend(entries.flatten().filter_map(|e| {
                        if !e
                            .path()
                            .extension()
                            .is_some_and(|x| x.eq_ignore_ascii_case("app"))
                        {
                            return None;
                        }
                        let m = fs::symlink_metadata(e.path()).ok()?;
                        let recent = m.created().or_else(|_| m.modified()).ok()?.elapsed().ok()?
                            < Duration::from_secs(7 * 86400);
                        (recent && m.is_dir() && !files::dataless(&m)).then(|| e.path())
                    }));
                }
            }
            // A cached startup record can point at an updater backup even when
            // the recent-App enumeration itself correctly ignores that folder.
            r.retain(|path| quick_root_allowed(path));
            r
        }
        AvScanKind::Full => full.to_vec(),
    };
    for path in &roots {
        if !path.is_absolute()
            || path
                .components()
                .any(|p| matches!(p, std::path::Component::ParentDir))
        {
            return Err(AppError::new("E_INVALID_ARG", "scan_absolute_paths"));
        }
    }
    Ok(roots)
}

fn quick_root_allowed(path: &Path) -> bool {
    !files::app_update_backup(path)
}

pub fn exclusions(home: &Path, configured: &[String], data: &Path) -> Vec<PathBuf> {
    let mut paths: Vec<_> = configured
        .iter()
        .map(|p| {
            p.strip_prefix("~/")
                .map(|p| home.join(p))
                .unwrap_or_else(|| PathBuf::from(p))
        })
        .collect();
    paths.push(data.to_owned());
    paths.extend(
        ["/System", "/Volumes", "/Network", "/dev", "/private/var/vm"]
            .into_iter()
            .map(PathBuf::from),
    );
    paths
}

pub fn walk(
    roots: Vec<PathBuf>,
    excluded: &[PathBuf],
    cancel: &AtomicBool,
    skipped: &mut AvSkipped,
    flags: &dyn Fn(&Path, &fs::Metadata) -> bool,
    visit: &mut dyn FnMut(&Path) -> Result<(), AppError>,
) -> Result<(), AppError> {
    walk_guarded(roots, excluded, cancel, skipped, flags, visit, None)
}

pub(super) fn walk_guarded(
    roots: Vec<PathBuf>,
    excluded: &[PathBuf],
    cancel: &AtomicBool,
    skipped: &mut AvSkipped,
    flags: &dyn Fn(&Path, &fs::Metadata) -> bool,
    visit: &mut dyn FnMut(&Path) -> Result<(), AppError>,
    own_data: Option<&super::own_data::OwnData>,
) -> Result<(), AppError> {
    let mut policy = super::scan_policy::Policy {
        own_data,
        ..Default::default()
    };
    walk_policy(
        roots,
        excluded,
        cancel,
        skipped,
        flags,
        &mut |path, _, _| visit(path),
        &mut policy,
    )
}

fn walk_policy(
    roots: Vec<PathBuf>,
    excluded: &[PathBuf],
    cancel: &AtomicBool,
    skipped: &mut AvSkipped,
    flags: &dyn Fn(&Path, &fs::Metadata) -> bool,
    visit: &mut dyn FnMut(&Path, &mut fs::File, &Snapshot) -> Result<(), AppError>,
    policy: &mut super::scan_policy::Policy<'_>,
) -> Result<(), AppError> {
    // Preserve root priority: Downloads must precede temporary directories and
    // recent Apps. Reversing only when creating the LIFO stack avoids the old bug.
    let mut stack = roots
        .iter()
        .cloned()
        .enumerate()
        .rev()
        .map(|(i, path)| (path, i))
        .collect::<Vec<_>>();
    let mut seen = BTreeSet::new();
    while let Some((path, root_index)) = stack.pop() {
        let root = &roots[root_index];
        files::canceled(cancel)?;
        if excluded.iter().any(|p| path.starts_with(p))
            || path
                .components()
                .any(|c| c.as_os_str() == "Backups.backupdb")
        {
            skipped.excluded += 1;
            continue;
        }
        let meta = match fs::symlink_metadata(&path) {
            Ok(m) => m,
            Err(error) => {
                policy.denied(&path, root, &error);
                log::info!("Skipping unreadable AV path {}: {error}", path.display());
                skipped.unreadable += 1;
                continue;
            }
        };
        if meta.is_symlink() {
            skipped.excluded += 1;
            continue;
        }
        if flags(&path, &meta) {
            skipped.dataless += 1;
            continue;
        }
        if !policy.allows(&path, root, &meta) {
            skipped.excluded += 1;
            continue;
        }
        // This must precede directory descent AND scan_limit's header reads.
        if let Some(guard) = policy.own_data
            && let Err(e) = guard.check(&path)
        {
            if e.message == "security_own_data" {
                skipped.excluded += 1;
            } else if e.message == "security_dataless" {
                skipped.dataless += 1;
            } else {
                log::info!("Skipping unreadable AV path {}: {e}", path.display());
                skipped.unreadable += 1;
            }
            continue;
        }
        // No reads or descents before both the object and its ancestors pass.
        if policy.own_data.is_none()
            && let Err(e) = files::safe_metadata(&path)
        {
            if e.message == "security_dataless" {
                skipped.dataless += 1;
            } else {
                log::info!(
                    "Skipping unreadable AV path {}: {e} {:?}",
                    path.display(),
                    e.detail
                );
                skipped.unreadable += 1;
            }
            continue;
        }
        use std::os::unix::fs::MetadataExt;
        if !seen.insert((meta.dev(), meta.ino())) {
            continue;
        }
        if meta.is_dir() {
            match local_volume(&path) {
                Ok(true) => {}
                Ok(false) => {
                    skipped.excluded += 1;
                    continue;
                }
                Err(error) => {
                    policy.denied(&path, root, &error);
                    log::info!("Skipping unreadable AV volume {}: {error}", path.display());
                    skipped.unreadable += 1;
                    continue;
                }
            }
            match fs::read_dir(&path) {
                Ok(entries) => {
                    for entry in entries {
                        match entry {
                            Ok(e) => stack.push((e.path(), root_index)),
                            Err(error) => {
                                policy.denied(&path, root, &error);
                                log::info!(
                                    "Skipping unreadable AV directory entry in {}: {error}",
                                    path.display()
                                );
                                skipped.unreadable += 1;
                            }
                        }
                    }
                }
                Err(error) => {
                    policy.denied(&path, root, &error);
                    log::info!(
                        "Skipping unreadable AV directory {}: {error}",
                        path.display()
                    );
                    skipped.unreadable += 1;
                }
            }
        } else if meta.is_file() {
            if meta.len() > files::MAX_INSTALLER_FILE {
                skipped.too_large += 1;
                continue;
            }
            // Capture before opening, then use the same guarded descriptor for
            // classification and scanning. Never reopen after classification.
            let prepared = (|| {
                let snapshot = Snapshot::capture(&path)?;
                let file =
                    files::open_read_guarded(&path, files::MAX_INSTALLER_FILE, policy.own_data)?;
                let opened = file.metadata()?;
                if opened.dev() != snapshot.device || opened.ino() != snapshot.inode {
                    return Err(AppError::new("E_PROTECTED", "security_object_changed"));
                }
                let limit = files::scan_limit_from_file(&path, &file)?;
                Ok::<_, AppError>((snapshot, file, limit))
            })();
            let (snapshot, mut file, limit) = match prepared {
                Ok(prepared) => prepared,
                Err(error) => {
                    if error.message == "security_own_data" {
                        skipped.excluded += 1;
                    } else if error.message == "security_dataless" {
                        skipped.dataless += 1;
                    } else {
                        skipped.unreadable += 1;
                    }
                    continue;
                }
            };
            if file.metadata()?.len() > limit {
                skipped.too_large += 1;
                continue;
            }
            match policy.allows_file(&path, root, limit, &file) {
                Ok(true) => {}
                Ok(false) => {
                    skipped.excluded += 1;
                    continue;
                }
                Err(error) => {
                    log::info!(
                        "Skipping unreadable AV code candidate {}: {error}",
                        path.display()
                    );
                    skipped.unreadable += 1;
                    continue;
                }
            }
            match visit(&path, &mut file, &snapshot) {
                Err(e) if e.message == "security_own_data" => skipped.excluded += 1,
                Err(e) if e.message == "av_scan_too_large" => skipped.too_large += 1,
                Err(e) if e.message == "av_scan_unreadable" => skipped.unreadable += 1,
                Err(e)
                    if matches!(
                        e.code.as_str(),
                        "E_IO" | "E_PROTECTED" | "E_FULL_DISK_ACCESS"
                    ) || (e.code == "E_UNKNOWN" && e.message == "io") =>
                {
                    log::info!(
                        "Skipping unreadable AV file {}: {e} {:?}",
                        path.display(),
                        e.detail
                    );
                    skipped.unreadable += 1
                }
                result => result?,
            }
        } else {
            skipped.excluded += 1;
        }
    }
    Ok(())
}
fn local_volume(path: &Path) -> std::io::Result<bool> {
    #[cfg(target_os = "macos")]
    {
        use std::os::unix::ffi::OsStrExt;
        let path = std::ffi::CString::new(path.as_os_str().as_bytes())
            .map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidInput, e))?;
        let mut stat: nix::libc::statfs = unsafe { std::mem::zeroed() };
        if unsafe { nix::libc::statfs(path.as_ptr(), &mut stat) } != 0 {
            return Err(std::io::Error::last_os_error());
        }
        Ok(stat.f_flags & nix::libc::MNT_LOCAL as u32 != 0)
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = path;
        Ok(true)
    }
}
impl Service {
    // Serialize the final identity check and persistence with quarantine/trust/restore.
    // A scan that finishes after an action cannot leave a new, unhandled stale finding.
    pub(crate) fn record_scan_finding(
        &self,
        scan_id: &str,
        finding: &AvFinding,
        snapshot: &Snapshot,
    ) -> Result<bool, AppError> {
        let _guard = self.mutation.lock().map_err(super::poison)?;
        files::same_file(snapshot, Path::new(&finding.path))?;
        let recorded = self.database.with(|c| {
            let tx = c.transaction()?;
            let trusted = tx.query_row(
                "SELECT EXISTS(SELECT 1 FROM security_trusted WHERE sha256=?1)",
                [&finding.sha256],
                |r| r.get::<_, bool>(0),
            )?;
            if trusted {
                return Ok(false);
            }
            tx.execute(
                "INSERT INTO security_findings(id,scan_id,value,snapshot) VALUES(?1,?2,?3,?4)",
                params![
                    finding.id,
                    scan_id,
                    serde_json::to_string(finding)?,
                    serde_json::to_string(snapshot)?
                ],
            )?;
            let event = ProtectionEvent {
                id: uuid::Uuid::now_v7().to_string(),
                at: super::now(),
                kind: ProtectionEventKind::ScanThreat,
                name: Path::new(&finding.path)
                    .file_name()
                    .unwrap_or_default()
                    .to_string_lossy()
                    .into_owned(),
                path: Some(finding.path.clone()),
                item_id: Some(finding.id.clone()),
                handled: false,
            };
            tx.execute(
                "INSERT INTO protection_events(id,at,value) VALUES(?1,?2,?3)",
                params![event.id, event.at, serde_json::to_string(&event)?],
            )?;
            tx.commit()?;
            Ok(true)
        })?;
        if recorded {
            self.changed(SecurityChangeReason::Events);
        }
        Ok(recorded)
    }

    pub fn scan_start(
        self: &Arc<Self>,
        kind: AvScanKind,
        paths: Option<Vec<String>>,
    ) -> Result<AvScanStarted, AppError> {
        self.scan_start_with_options(kind, paths, false)
    }
    pub fn scan_start_with_options(
        self: &Arc<Self>,
        kind: AvScanKind,
        paths: Option<Vec<String>>,
        force: bool,
    ) -> Result<AvScanStarted, AppError> {
        self.av.ready()?;
        let startup = self.saved_startup()?;
        let roots = roots(kind, &self.home, paths, &startup)?;
        let mut active = self.active_scan.lock().map_err(super::poison)?;
        if active.is_some() {
            return Err(AppError::new("E_AV_ENGINE", "scan_already_running"));
        }
        let id = uuid::Uuid::now_v7().to_string();
        let cancel = Arc::new(AtomicBool::new(false));
        let result = AvScanResult {
            scan_id: id.clone(),
            kind,
            state: AvScanState::Running,
            error_code: None,
            started_at: super::now(),
            finished_at: None,
            files_scanned: 0,
            bytes_scanned: 0,
            skipped: AvSkipped::default(),
            metrics: AvScanMetrics::default(),
            denied_roots: vec![],
            findings: vec![],
        };
        self.save_scan(&result)?;
        *active = Some((id.clone(), cancel.clone()));
        drop(active);
        let service = self.clone();
        std::thread::spawn(move || service.run_scan_options(result, roots, cancel, force));
        Ok(AvScanStarted { scan_id: id })
    }
    #[cfg(test)]
    fn run_scan(&self, result: AvScanResult, roots: Vec<PathBuf>, cancel: Arc<AtomicBool>) {
        self.run_scan_options(result, roots, cancel, false)
    }
    fn run_scan_options(
        &self,
        mut result: AvScanResult,
        roots: Vec<PathBuf>,
        cancel: Arc<AtomicBool>,
        force: bool,
    ) {
        #[cfg(target_os = "macos")]
        unsafe {
            // QOS_CLASS_BACKGROUND: reduce impact on interactive applications.
            unsafe extern "C" {
                fn pthread_set_qos_class_self_np(class: u32, relative: i32) -> i32;
            }
            let _ = pthread_set_qos_class_self_np(0x09, 0);
        }
        let done = (|| {
            let own_data = self.own_data()?;
            let _lease = self.av.lease(&cancel)?;
            let settings = self.settings.read().map_err(super::poison)?.clone();
            let mut excluded = exclusions(&self.home, &settings.av_exclusions, &self.data);
            excluded.extend_from_slice(own_data.roots());
            let started = Instant::now();
            let heartbeat = ScanHeartbeat::new(
                AvProgress {
                    scan_id: result.scan_id.clone(),
                    files_scanned: 0,
                    bytes_scanned: 0,
                    current_path: String::new(),
                    findings: 0,
                    cached_files: 0,
                },
                self.sink.clone(),
            );
            let cache = std::sync::Mutex::new(super::scan_cache::CleanCache::load(
                &self.database,
                super::now(),
            ));
            // Two isolated daemons bound RAM, descriptors and total extraction
            // budget. Other scan kinds retain their original ordered behavior.
            let worker_count = if result.kind == AvScanKind::Full {
                2
            } else {
                1
            };
            result.metrics.workers = worker_count;
            let auxiliary = (worker_count == 2).then(|| worker::Isolated(self.av.isolated()));
            let engines = std::iter::once(self.av.clone())
                .chain(auxiliary.as_ref().map(|engine| engine.0.clone()))
                .collect::<Vec<_>>();
            let budget = Duration::from_secs(if result.kind == AvScanKind::Full && !force {
                60
            } else {
                300
            });
            enum Message {
                File(worker::Report),
                Walk(AvSkipped, Vec<String>, Result<(), AppError>, u64),
                Failed(AppError),
            }
            let mut failure = None;
            // Sum microseconds before converting, so many sub-millisecond files
            // do not disappear from the persisted timing totals.
            let mut timing_us = [0u64; 5];
            let mut last = Instant::now();
            std::thread::scope(|scope| {
                let (jobs, input) = std::sync::mpsc::sync_channel::<worker::Job>(8);
                let input = Arc::new(std::sync::Mutex::new(input));
                let (messages, output) = std::sync::mpsc::sync_channel(8);
                let kind = result.kind;
                let walker_messages = messages.clone();
                let cancel_ref = &cancel;
                scope.spawn(move || {
                    let mut skipped = AvSkipped::default();
                    let mut policy = super::scan_policy::Policy::for_scan(kind, &roots, &own_data);
                    let mut waiting = Duration::ZERO;
                    let walked_at = Instant::now();
                    let walked = walk_policy(
                        roots,
                        &excluded,
                        cancel_ref,
                        &mut skipped,
                        &|_, m| files::dataless(m),
                        &mut |path, file, snapshot| {
                            let job = worker::Job {
                                path: path.to_owned(),
                                file: file.try_clone()?,
                                snapshot: snapshot.clone(),
                            };
                            let queued_at = Instant::now();
                            let sent = jobs.send(job).map_err(|_| {
                                AppError::new("E_SCAN_CANCELED", "scan_workers_stopped")
                            });
                            waiting += queued_at.elapsed();
                            sent
                        },
                        &mut policy,
                    );
                    drop(jobs);
                    let preparation_ms =
                        walked_at.elapsed().saturating_sub(waiting).as_millis() as u64;
                    let _ = walker_messages.send(Message::Walk(
                        skipped,
                        policy.denied_roots.into_iter().collect(),
                        walked,
                        preparation_ms,
                    ));
                });
                for (slot, engine) in engines.into_iter().enumerate() {
                    let input = input.clone();
                    let messages = messages.clone();
                    let heartbeat = &heartbeat;
                    let cache = &cache;
                    scope.spawn(move || {
                        let session = match engine.session(cancel_ref, budget) {
                            Ok(session) => session,
                            Err(error) => {
                                cancel_ref.store(true, std::sync::atomic::Ordering::Release);
                                let _ = messages.send(Message::Failed(error));
                                return;
                            }
                        };
                        loop {
                            let job = input.lock().unwrap_or_else(|e| e.into_inner()).recv();
                            let Ok(job) = job else {
                                break;
                            };
                            // Drain queued descriptors after cancellation so the
                            // bounded producer can always terminate and be joined.
                            if cancel_ref.load(std::sync::atomic::Ordering::Acquire) {
                                continue;
                            }
                            heartbeat.begin(slot, &job.path);
                            let report = worker::process(&session, job, cache, cancel_ref, force);
                            heartbeat.end(slot);
                            if messages.send(Message::File(report)).is_err() {
                                break;
                            }
                        }
                    });
                }
                drop(input);
                drop(messages);
                for message in output {
                    match message {
                        Message::Failed(error) => {
                            retain_scan_error(&mut failure, error);
                        }
                        Message::Walk(skipped, denied, walked, preparation_ms) => {
                            result.skipped.excluded += skipped.excluded;
                            result.skipped.dataless += skipped.dataless;
                            result.skipped.too_large += skipped.too_large;
                            result.skipped.unreadable += skipped.unreadable;
                            result.denied_roots = denied;
                            result.metrics.preparation_ms = preparation_ms;
                            if let Err(error) = walked {
                                retain_scan_error(&mut failure, error);
                            }
                        }
                        Message::File(report) => {
                            timing_us[0] += report.hash_us;
                            timing_us[1] += report.timing.engine_us;
                            timing_us[2] += report.timing.queue_us;
                            timing_us[3] += report.timing.inspection_us;
                            timing_us[4] += report.timing.cleanup_us;
                            result.metrics.hash_ms = timing_us[0] / 1000;
                            result.metrics.engine_ms = timing_us[1] / 1000;
                            result.metrics.queue_ms = timing_us[2] / 1000;
                            result.metrics.inspection_ms = timing_us[3] / 1000;
                            result.metrics.cleanup_ms = timing_us[4] / 1000;
                            if report.timing.engine_us >= 2_000_000 {
                                result.metrics.slow_files += 1;
                            }
                            let handled = (|| {
                                let verdict = report.verdict?;
                                files::same_file(&report.snapshot, &report.path)?;
                                result.files_scanned += 1;
                                result.bytes_scanned += report.snapshot.length;
                                if verdict.cached {
                                    result.metrics.cached_files += 1;
                                }
                                if let Some(signature) = verdict.signature {
                                    let finding = AvFinding {
                                        id: uuid::Uuid::now_v7().to_string(),
                                        path: report.path.to_string_lossy().into_owned(),
                                        signature,
                                        size_bytes: report.snapshot.length,
                                        sha256: verdict.hash,
                                        owner_app: None,
                                        requires_admin: super::requires_admin(&report.path),
                                        status: FindingStatus::Open,
                                    };
                                    if self.record_scan_finding(
                                        &result.scan_id,
                                        &finding,
                                        &report.snapshot,
                                    )? {
                                        result.findings.push(finding);
                                    }
                                }
                                Ok::<_, AppError>(())
                            })();
                            if let Err(error) = handled {
                                match error.message.as_str() {
                                    "security_own_data" => result.skipped.excluded += 1,
                                    "security_dataless" => result.skipped.dataless += 1,
                                    "av_scan_too_large" => result.skipped.too_large += 1,
                                    "av_scan_unreadable" => result.skipped.unreadable += 1,
                                    _ if matches!(
                                        error.code.as_str(),
                                        "E_IO" | "E_PROTECTED" | "E_FULL_DISK_ACCESS"
                                    ) || (error.code == "E_UNKNOWN"
                                        && error.message == "io") =>
                                    {
                                        result.skipped.unreadable += 1
                                    }
                                    _ => {
                                        retain_scan_error(&mut failure, error);
                                        cancel.store(true, std::sync::atomic::Ordering::Release);
                                    }
                                }
                            }
                            heartbeat.update(&result);
                            if last.elapsed() >= Duration::from_secs(2) {
                                let persist_at = Instant::now();
                                if let Err(error) = self.save_scan(&result) {
                                    retain_scan_error(&mut failure, error);
                                    cancel.store(true, std::sync::atomic::Ordering::Release);
                                }
                                result.metrics.persistence_ms +=
                                    persist_at.elapsed().as_millis() as u64;
                                last = Instant::now();
                            }
                        }
                    }
                }
            });
            let persist_at = Instant::now();
            if let Err(error) = cache.lock().map_err(super::poison)?.save(&self.database) {
                log::warn!("AV clean cache not saved: {}", error.code);
            }
            result.metrics.persistence_ms += persist_at.elapsed().as_millis() as u64;
            log::info!(
                "AV scan timing: kind={:?} files={} total_ms={} metrics={:?}",
                result.kind,
                result.files_scanned,
                started.elapsed().as_millis(),
                result.metrics
            );
            if let Some(error) = failure {
                return Err(error);
            }
            files::canceled(&cancel)?;
            Ok::<_, AppError>(())
        })();
        if let Err(e) = &done {
            self.av.record_failure(e);
            if e.code != "E_SCAN_CANCELED" {
                result.error_code = Some(e.code.clone());
                log::error!(
                    "Security scan {} failed: {} {} {:?}",
                    result.scan_id,
                    e.code,
                    e.message,
                    e.detail
                );
            }
        }
        result.state = match done {
            Ok(()) => AvScanState::Done,
            Err(ref e) if e.code == "E_SCAN_CANCELED" => AvScanState::Canceled,
            Err(_) => AvScanState::Failed,
        };
        result.finished_at = Some(super::now());
        // A reader can start again as soon as it sees the persisted final state.
        // Hold the same lock as scan_start while publishing and releasing the
        // active slot so that reader waits instead of receiving "already running".
        // Actions may have handled a finding while the scan continued on other files.
        // Keep the final persisted result and event ordered with those actions, too.
        let _mutation = self.mutation.lock().unwrap_or_else(|e| e.into_inner());
        let mut active = self.active_scan.lock().ok();
        let saved = self
            .database
            .with(|c| {
                let mut statement =
                    c.prepare("SELECT value FROM security_findings WHERE scan_id=?1 ORDER BY id")?;
                let values = statement
                    .query_map([&result.scan_id], |r| r.get::<_, String>(0))?
                    .collect::<rusqlite::Result<Vec<_>>>()?;
                result.findings = values
                    .into_iter()
                    .map(|value| serde_json::from_str(&value))
                    .collect::<Result<_, _>>()?;
                Ok(())
            })
            .and_then(|()| self.save_scan(&result));
        if let Err(e) = saved {
            result.state = AvScanState::Failed;
            result.error_code.get_or_insert_with(|| e.code.clone());
            log::error!(
                "Unable to save security scan {}: {} {} {:?}",
                result.scan_id,
                e.code,
                e.message,
                e.detail
            );
        }
        if let Some(active) = active.as_mut() {
            **active = None;
        }
        drop(active);
        (self.sink)(CoreEvent::AvFinished(result));
        self.changed(SecurityChangeReason::Engine);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn worker_failure_takes_precedence_over_producer_cancellation() {
        let mut failure = None;
        retain_scan_error(&mut failure, AppError::new("E_SCAN_CANCELED", "canceled"));
        retain_scan_error(&mut failure, AppError::new("E_AV_ENGINE", "worker_start"));
        retain_scan_error(&mut failure, AppError::new("E_SCAN_CANCELED", "canceled"));
        assert_eq!(failure.unwrap().code, "E_AV_ENGINE");
    }

    #[test]
    fn clean_content_cache_reuses_only_matching_generation_and_force_bypasses_it() {
        let (_temp, service, _) = super::super::tests::fixture();
        super::super::tests::fake_engine(&service);
        service.settings.write().unwrap().av_consent = true;
        let path = service.home.join("clean");
        fs::write(&path, b"ordinary clean content").unwrap();
        let run = |force| {
            let scan = service
                .scan_start_with_options(
                    AvScanKind::Custom,
                    Some(vec![path.to_string_lossy().into_owned()]),
                    force,
                )
                .unwrap();
            super::super::tests::wait_scan(&service, &scan.scan_id)
        };
        let first = run(false);
        assert_eq!(first.state, AvScanState::Done);
        assert_eq!(first.metrics.cached_files, 0);
        let cached = run(false);
        assert_eq!(cached.files_scanned, 1);
        assert_eq!(cached.metrics.cached_files, 1);
        assert_eq!(run(true).metrics.cached_files, 0);
        service
            .database
            .set_meta("av_db_generation", "next-generation")
            .unwrap();
        assert_eq!(run(false).metrics.cached_files, 0);
        fs::write(&path, b"fixture-threat").unwrap();
        let threat = run(false);
        assert_eq!(threat.metrics.cached_files, 0);
        assert_eq!(threat.findings.len(), 1);
        assert_eq!(run(false).metrics.cached_files, 0);
        fs::write(&path, b"heuristic:Heuristics.Encrypted.Zip").unwrap();
        for _ in 0..2 {
            let incomplete = run(false);
            assert_eq!(incomplete.metrics.cached_files, 0);
            assert_eq!(incomplete.files_scanned, 0);
            assert_eq!(incomplete.skipped.unreadable, 1);
        }
    }

    #[test]
    fn full_scan_runs_two_isolated_workers_and_cancels_both() {
        let (_temp, service, _) = super::super::tests::fixture();
        super::super::tests::fake_engine(&service);
        service.settings.write().unwrap().av_consent = true;
        // Startup has its own 90-second budget. Hold the primary daemon ready so
        // this test measures scan concurrency/progress, not Python cold startup
        // under the load of the other security fixtures.
        let _lease = service.av.lease(&AtomicBool::new(false)).unwrap();
        let roots = ["one", "two"].map(|name| {
            let path = service.home.join(name);
            fs::write(&path, b"wait-for-test-cancel").unwrap();
            path
        });
        let cancel = Arc::new(AtomicBool::new(false));
        let result = AvScanResult {
            scan_id: "parallel-fixture".into(),
            kind: AvScanKind::Full,
            state: AvScanState::Running,
            error_code: None,
            started_at: super::super::now(),
            finished_at: None,
            files_scanned: 0,
            bytes_scanned: 0,
            skipped: AvSkipped::default(),
            metrics: AvScanMetrics::default(),
            denied_roots: vec![],
            findings: vec![],
        };
        let runtime = &service.av.config.runtime;
        std::thread::scope(|scope| {
            let handle = scope
                .spawn(|| service.run_scan_options(result, roots.to_vec(), cancel.clone(), false));
            // The auxiliary daemon starts inside the scan and must receive its
            // full 90-second startup allowance, plus scheduling/dispatch time.
            let deadline = Instant::now() + Duration::from_secs(120);
            let concurrent = loop {
                let primary = runtime.join("scan-tmp/partial-extraction").exists();
                let auxiliary = fs::read_dir(runtime)
                    .ok()
                    .into_iter()
                    .flatten()
                    .flatten()
                    .any(|entry| {
                        entry.file_name().to_string_lossy().starts_with("worker-")
                            && entry.path().join("scan-tmp/partial-extraction").exists()
                    });
                if primary && auxiliary {
                    break true;
                }
                if Instant::now() >= deadline {
                    break false;
                }
                std::thread::sleep(Duration::from_millis(10));
            };
            cancel.store(true, std::sync::atomic::Ordering::Release);
            handle.join().unwrap();
            assert!(
                concurrent,
                "both workers must reach independent extraction directories"
            );
        });
        let finished = service.scan_get("parallel-fixture").unwrap();
        assert_eq!(finished.state, AvScanState::Canceled);
        assert_eq!(finished.metrics.workers, 2);
        assert!(!runtime.join("scan-tmp/partial-extraction").exists());
        assert!(
            !fs::read_dir(runtime)
                .unwrap()
                .flatten()
                .any(|entry| entry.file_name().to_string_lossy().starts_with("worker-"))
        );
    }

    #[test]
    fn slow_file_emits_live_progress_and_stops_before_finished() {
        let (_temp, mut service, _) = super::super::tests::fixture();
        let (sender, receiver) = std::sync::mpsc::channel();
        Arc::get_mut(&mut service).unwrap().sink = Arc::new(move |event| {
            if matches!(event, CoreEvent::AvProgress(_) | CoreEvent::AvFinished(_)) {
                let _ = sender.send(event);
            }
        });
        super::super::tests::fake_engine(&service);
        service.settings.write().unwrap().av_consent = true;
        // Startup has its own 90-second budget. Hold the primary daemon ready so
        // this test measures scan concurrency/progress, not Python cold startup
        // under the load of the other security fixtures.
        let _lease = service.av.lease(&AtomicBool::new(false)).unwrap();
        let path = service.home.join("slow-file");
        fs::write(&path, b"wait-for-test-cancel").unwrap();
        let scan = service
            .scan_start(
                AvScanKind::Custom,
                Some(vec![path.to_string_lossy().into_owned()]),
            )
            .unwrap();
        let mut heartbeats = 0;
        // The background scan thread still has to be scheduled, acquire its DB
        // locks and dispatch the file. Do not charge this startup wait to the
        // heartbeat interval; measure that only once a file is actually active.
        let mut deadline = Instant::now() + Duration::from_secs(120);
        while heartbeats < 2 {
            let remaining = deadline.saturating_duration_since(Instant::now());
            assert!(!remaining.is_zero(), "scan heartbeat deadline elapsed");
            match receiver.recv_timeout(remaining).expect("scan heartbeat") {
                CoreEvent::AvProgress(progress) => {
                    if progress.current_path == path.to_string_lossy() {
                        assert_eq!(progress.files_scanned, 0);
                        assert_eq!(progress.bytes_scanned, 0);
                        heartbeats += 1;
                        // Once running, another heartbeat must arrive promptly.
                        deadline = Instant::now() + Duration::from_secs(10);
                    }
                }
                _ => panic!("scan finished before the slow file was canceled"),
            }
        }
        service.scan_cancel(&scan.scan_id).unwrap();
        loop {
            if let CoreEvent::AvFinished(result) =
                receiver.recv_timeout(Duration::from_secs(10)).unwrap()
            {
                assert_eq!(result.state, AvScanState::Canceled);
                assert_eq!(result.files_scanned, 0);
                break;
            }
        }
        assert!(receiver.recv_timeout(Duration::from_millis(750)).is_err());
    }

    #[test]
    fn prepared_descriptor_does_not_follow_a_replaced_path() {
        use std::io::Read;
        let (_temp, service, _) = super::super::tests::fixture();
        let path = service.home.join("candidate");
        fs::write(&path, b"original").unwrap();
        let replacement = service.home.join("replacement");
        fs::write(&replacement, b"replacement").unwrap();
        let own_data = service.own_data().unwrap();
        let mut policy = super::super::scan_policy::Policy::for_scan(
            AvScanKind::Full,
            std::slice::from_ref(&path),
            &own_data,
        );
        let mut visits = 0;
        walk_policy(
            vec![path.clone()],
            &[],
            &AtomicBool::new(false),
            &mut AvSkipped::default(),
            &|_, _| false,
            &mut |path, file, snapshot| {
                fs::rename(&replacement, path).unwrap();
                let mut content = String::new();
                file.read_to_string(&mut content).unwrap();
                assert_eq!(content, "original");
                assert!(files::same_file(snapshot, path).is_err());
                visits += 1;
                Ok(())
            },
            &mut policy,
        )
        .unwrap();
        assert_eq!(visits, 1);
    }

    #[test]
    fn idle_connection_eof_retries_same_file_once_and_preserves_the_finding() {
        let (_temp, service, _) = super::super::tests::fixture();
        super::super::tests::fake_engine(&service);
        service.settings.write().unwrap().av_consent = true;
        let path = service.home.join("sample");
        let counter = service.av.config.runtime.join("closed-count");
        for repeated in [false, true] {
            let _ = fs::remove_file(&counter);
            fs::write(
                &path,
                format!(
                    "close-after-idle fixture-threat {}",
                    if repeated { "always-close" } else { "" }
                ),
            )
            .unwrap();
            let started = service
                .scan_start(
                    AvScanKind::Custom,
                    Some(vec![path.to_string_lossy().into_owned()]),
                )
                .unwrap();
            let result = super::super::tests::wait_scan(&service, &started.scan_id);
            assert_eq!(fs::read_to_string(&counter).unwrap(), "2");
            if repeated {
                assert_eq!(result.state, AvScanState::Failed);
                assert_eq!(result.error_code.as_deref(), Some("E_AV_ENGINE"));
                assert_eq!(result.files_scanned, 0);
            } else {
                assert_eq!(result.state, AvScanState::Done);
                assert_eq!(result.files_scanned, 1);
                assert_eq!(result.findings.len(), 1);
                assert_eq!(result.findings[0].signature, "Fixture.Test");
            }
        }
    }
    #[test]
    fn all_scan_kinds_exclude_actual_app_data_and_explicit_database_aliases() {
        let (_temp, service, _) = super::super::tests::fixture();
        super::super::tests::fake_engine(&service);
        service.settings.write().unwrap().av_consent = true;
        let database = service
            .database
            .with(|c| Ok(PathBuf::from(c.path().unwrap())))
            .unwrap();
        let data = database.parent().unwrap();
        // Outside logs/security, where the previous exclusion stopped.
        let private = data.join("private-state");
        fs::write(&private, "fixture-threat").unwrap();
        let good = service.home.join("sample");
        fs::write(&good, "clean fixture").unwrap();
        let mut explicit = vec![data.to_owned(), private, good.clone()];
        for suffix in ["", "-wal", "-shm"] {
            let path = data.join(format!("opennavo.db{suffix}"));
            let alias = service.home.join(format!("database-alias{suffix}"));
            fs::hard_link(&path, &alias).unwrap();
            explicit.extend([path, alias]);
        }
        for kind in [AvScanKind::Quick, AvScanKind::Full, AvScanKind::Custom] {
            let roots = if kind == AvScanKind::Full {
                roots_at(
                    kind,
                    &service.home,
                    None,
                    &[],
                    &[],
                    std::slice::from_ref(&service.home),
                    None,
                )
                .unwrap()
            } else {
                // Exercise explicit roots as well as discovery beneath a parent.
                roots_at(
                    AvScanKind::Custom,
                    &service.home,
                    Some(
                        explicit
                            .iter()
                            .map(|p| p.to_string_lossy().into_owned())
                            .collect(),
                    ),
                    &[],
                    &[],
                    &[],
                    None,
                )
                .unwrap()
            };
            let id = uuid::Uuid::now_v7().to_string();
            service.run_scan(
                AvScanResult {
                    scan_id: id.clone(),
                    kind,
                    state: AvScanState::Running,
                    error_code: None,
                    started_at: 0,
                    finished_at: None,
                    files_scanned: 0,
                    bytes_scanned: 0,
                    skipped: AvSkipped::default(),
                    metrics: AvScanMetrics::default(),
                    denied_roots: vec![],
                    findings: vec![],
                },
                roots,
                Arc::new(AtomicBool::new(false)),
            );
            let result = service.scan_get(&id).unwrap();
            assert_eq!(result.state, AvScanState::Done, "{result:?}");
            assert_eq!(result.files_scanned, 1, "{result:?}");
            assert!(result.skipped.excluded >= 4, "{result:?}");
            assert!(result.findings.is_empty());
            assert!(result.error_code.is_none());
        }
    }
    #[test]
    fn unreadable_roots_and_subdirectories_do_not_abort_any_scan_kind() {
        use std::os::unix::fs::PermissionsExt;
        struct Denied(PathBuf);
        impl Drop for Denied {
            fn drop(&mut self) {
                let _ = fs::set_permissions(&self.0, fs::Permissions::from_mode(0o700));
            }
        }
        let (_temp, service, _) = super::super::tests::fixture();
        super::super::tests::fake_engine(&service);
        service.settings.write().unwrap().av_consent = true;
        let readable = service.home.join("Downloads");
        let denied_root = service.home.join("Desktop");
        let denied_child = readable.join("private");
        fs::create_dir_all(&denied_child).unwrap();
        fs::create_dir(&denied_root).unwrap();
        for directory in [&denied_root, &denied_child] {
            fs::write(directory.join("must-not-read"), "fixture-threat").unwrap();
            fs::set_permissions(directory, fs::Permissions::from_mode(0o000)).unwrap();
        }
        let _restore = [Denied(denied_root.clone()), Denied(denied_child.clone())];
        assert_eq!(
            fs::read_dir(&denied_root).unwrap_err().kind(),
            std::io::ErrorKind::PermissionDenied
        );
        fs::write(readable.join("sample"), "clean fixture").unwrap();
        for kind in [AvScanKind::Quick, AvScanKind::Full, AvScanKind::Custom] {
            let roots = roots_at(
                kind,
                &service.home,
                Some(vec![
                    readable.to_string_lossy().into_owned(),
                    denied_root.to_string_lossy().into_owned(),
                ]),
                &[],
                &[],
                &[readable.clone(), denied_root.clone()],
                None,
            )
            .unwrap();
            let id = uuid::Uuid::now_v7().to_string();
            service.run_scan(
                AvScanResult {
                    scan_id: id.clone(),
                    kind,
                    state: AvScanState::Running,
                    error_code: None,
                    started_at: 0,
                    finished_at: None,
                    files_scanned: 0,
                    bytes_scanned: 0,
                    skipped: AvSkipped::default(),
                    metrics: AvScanMetrics::default(),
                    denied_roots: vec![],
                    findings: vec![],
                },
                roots,
                Arc::new(AtomicBool::new(false)),
            );
            let result = service.scan_get(&id).unwrap();
            assert_eq!(result.state, AvScanState::Done, "{result:?}");
            assert!(result.error_code.is_none());
            assert_eq!(result.skipped.unreadable, 2);
            assert_eq!(
                result.denied_roots,
                vec![denied_root.to_string_lossy().into_owned()]
            );
            assert_eq!(result.files_scanned, 1);
            assert!(result.findings.is_empty());
        }
    }
    #[test]
    fn recent_app_roots_ignore_update_backups_and_ordinary_directories() {
        let (_temp, service, _) = super::super::tests::fixture();
        let applications = service.home.join("Applications");
        let app = applications.join("Recent.app");
        fs::create_dir_all(&app).unwrap();
        fs::create_dir_all(applications.join(".opennavo-updates/previous.app")).unwrap();
        fs::create_dir(applications.join("ordinary-directory")).unwrap();
        fs::write(applications.join("not-a-bundle.app"), "archive").unwrap();
        std::os::unix::fs::symlink(&app, applications.join("alias.app")).unwrap();
        let roots = roots_at(
            AvScanKind::Quick,
            &service.home,
            None,
            &[],
            &[applications],
            &[],
            None,
        )
        .unwrap();
        assert_eq!(
            roots,
            vec![
                service.home.join("Downloads"),
                service.home.join("Desktop"),
                app
            ]
        );
        for path in [
            "/Applications/.opennavo-updates/id/previous.app/Contents/MacOS/Previous",
            "/System/Volumes/Data/Applications/.opennavo-updates/id/previous.app",
            "/Applications/.opennavo-updates.noindex/id/previous.app/Contents/MacOS/Previous",
            "/Users/fixture/Applications/.opennavo-updates.noindex/id/previous.app",
        ] {
            assert!(!quick_root_allowed(Path::new(path)));
        }
        assert!(quick_root_allowed(Path::new("/Applications/Current.app")));
    }
    #[test]
    fn quick_scan_prioritizes_downloads_and_does_not_descend_service_temps_or_scan_app_resources() {
        use std::os::unix::fs::PermissionsExt;
        let (_temp, service, _) = super::super::tests::fixture();
        let downloads = service.home.join("Downloads");
        let desktop = service.home.join("Desktop");
        let temporary = service.home.join("tmp");
        let app = service.home.join("Recent.app");
        for directory in [
            &downloads,
            &desktop,
            &temporary.join("service/TemporaryItems"),
            &app.join("Contents/MacOS"),
            &app.join("Contents/Resources"),
        ] {
            fs::create_dir_all(directory).unwrap();
        }
        let included = [
            downloads.join("first"),
            desktop.join("second"),
            temporary.join("top.zip"),
            app.join("Contents/MacOS/Recent"),
            app.join("Contents/Resources/tool.sh"),
            app.join("Contents/Resources/installer.pkg"),
            app.join("Contents/Resources/native.payload"),
            app.join("Contents/MacOS/script"),
        ];
        let excluded = [
            temporary.join("service/TemporaryItems/secret"),
            app.join("Contents/Resources/large.png"),
            app.join("Contents/Resources/localization.json"),
            app.join("Contents/Resources/locale.pak"),
            app.join("Contents/Resources/README"),
        ];
        for path in included.iter().chain(&excluded) {
            fs::write(path, "fixture").unwrap();
        }
        fs::write(&included[3], b"\xcf\xfa\xed\xfe Mach-O fixture").unwrap();
        fs::write(&included[6], b"\xca\xfe\xba\xbe universal fixture").unwrap();
        fs::write(&included[7], b"#!/bin/sh\ntrue\n").unwrap();
        // Real Electron Apps can mark localisation and other data executable.
        for resource in &excluded[1..] {
            fs::set_permissions(resource, fs::Permissions::from_mode(0o755)).unwrap();
        }
        let guard = service.own_data().unwrap();
        let roots = vec![downloads, desktop, temporary.clone(), app.clone()];
        let mut policy =
            super::super::scan_policy::Policy::for_scan(AvScanKind::Quick, &roots, &guard);
        policy.shallow.insert(temporary);
        let mut visited = vec![];
        walk_policy(
            roots.clone(),
            &[],
            &AtomicBool::new(false),
            &mut AvSkipped::default(),
            &|p, _| {
                assert!(!p.ends_with("TemporaryItems/secret"));
                false
            },
            &mut |p, _, _| {
                visited.push(p.to_owned());
                Ok(())
            },
            &mut policy,
        )
        .unwrap();
        assert_eq!(&visited[..2], &included[..2]);
        assert_eq!(
            visited.into_iter().collect::<BTreeSet<_>>(),
            included.into_iter().collect()
        );
        let mut all = vec![];
        walk(
            roots,
            &[],
            &AtomicBool::new(false),
            &mut AvSkipped::default(),
            &|_, _| false,
            &mut |p| {
                all.push(p.to_owned());
                Ok(())
            },
        )
        .unwrap();
        assert!(excluded.iter().all(|p| all.contains(p)));
    }
    #[test]
    fn engine_start_failure_is_persisted_and_emitted_with_its_error_code() {
        use std::os::unix::fs::PermissionsExt;
        let (_temp, mut service, _) = super::super::tests::fixture();
        let emitted = Arc::new(std::sync::Mutex::new(None));
        let capture = emitted.clone();
        Arc::get_mut(&mut service).unwrap().sink = Arc::new(move |event| {
            if let CoreEvent::AvFinished(result) = event {
                *capture.lock().unwrap() = Some(result);
            }
        });
        super::super::tests::fake_engine(&service);
        service.settings.write().unwrap().av_consent = true;
        fs::set_permissions(
            service.av.config.helpers.join("clamd"),
            fs::Permissions::from_mode(0o600),
        )
        .unwrap();
        let id = "cannot-start";
        service.run_scan(
            AvScanResult {
                scan_id: id.into(),
                kind: AvScanKind::Custom,
                state: AvScanState::Running,
                error_code: None,
                started_at: 0,
                finished_at: None,
                files_scanned: 0,
                bytes_scanned: 0,
                skipped: AvSkipped::default(),
                metrics: AvScanMetrics::default(),
                denied_roots: vec![],
                findings: vec![],
            },
            vec![],
            Arc::new(AtomicBool::new(false)),
        );
        let saved = service.scan_get(id).unwrap();
        assert_eq!(saved.state, AvScanState::Failed);
        assert_eq!(saved.error_code.as_deref(), Some("E_AV_ENGINE"));
        assert_eq!(emitted.lock().unwrap().as_ref(), Some(&saved));
        assert!(!service.av.status().unwrap().running);

        // Storage failure must not emit a successful result either, even when
        // the engine completed normally and there is nowhere to save history.
        fs::set_permissions(
            service.av.config.helpers.join("clamd"),
            fs::Permissions::from_mode(0o700),
        )
        .unwrap();
        service
            .database
            .with(|c| {
                c.execute("DROP TABLE security_scans", [])?;
                Ok(())
            })
            .unwrap();
        service.run_scan(
            AvScanResult {
                state: AvScanState::Running,
                error_code: None,
                ..saved
            },
            vec![],
            Arc::new(AtomicBool::new(false)),
        );
        let finished = emitted.lock().unwrap().clone().unwrap();
        assert_eq!(finished.state, AvScanState::Failed);
        assert_eq!(finished.error_code.as_deref(), Some("E_UNKNOWN"));
    }
    #[test]
    fn incomplete_archive_counts_and_download_reasons_match_for_all_scan_kinds() {
        let (_tmp, service, _) = super::super::tests::fixture();
        super::super::tests::fake_engine(&service);
        service.settings.write().unwrap().av_consent = true;
        let root = service.home.join("Downloads");
        fs::create_dir(&root).unwrap();
        fs::create_dir(service.home.join("Desktop")).unwrap();
        for suffix in [
            "Limits.Exceeded.MaxFileSize",
            "Limits.Exceeded.MaxScanSize",
            "Limits.Exceeded.MaxFiles",
            "Limits.Exceeded.MaxRecursion",
            "Limits.Exceeded.MaxScanTime",
            "Encrypted.Zip",
            "Encrypted.PDF",
        ] {
            let path = root.join(format!("{suffix}.zip"));
            fs::write(&path, format!("heuristic:Heuristics.{suffix}")).unwrap();
            let download = service.download_verify(path, None).unwrap();
            assert!(download.av.is_none());
            assert_eq!(
                download.av_skipped,
                Some(if suffix.starts_with("Limits") {
                    super::super::AvSkipReason::TooLarge
                } else {
                    super::super::AvSkipReason::Unreadable
                })
            );
        }
        for kind in [AvScanKind::Quick, AvScanKind::Full, AvScanKind::Custom] {
            let roots = roots_at(
                kind,
                &service.home,
                Some(vec![root.to_string_lossy().into_owned()]),
                &[],
                &[],
                std::slice::from_ref(&root),
                None,
            )
            .unwrap();
            let id = uuid::Uuid::now_v7().to_string();
            service.run_scan(
                AvScanResult {
                    scan_id: id.clone(),
                    kind,
                    state: AvScanState::Running,
                    error_code: None,
                    started_at: 0,
                    finished_at: None,
                    files_scanned: 0,
                    bytes_scanned: 0,
                    skipped: AvSkipped::default(),
                    metrics: AvScanMetrics::default(),
                    denied_roots: vec![],
                    findings: vec![],
                },
                roots,
                Arc::new(AtomicBool::new(false)),
            );
            let result = service.scan_get(&id).unwrap();
            assert_eq!(result.state, AvScanState::Done);
            assert_eq!(result.files_scanned, 0);
            assert!(result.findings.is_empty());
            assert_eq!(result.skipped.too_large, 5);
            assert_eq!(result.skipped.unreadable, 2);
        }
        assert!(service.quarantine_list().unwrap().is_empty());
    }
    #[test]
    fn quick_and_full_walk_never_open_or_descend_into_cloud_placeholders() {
        let temp = tempfile::tempdir().unwrap();
        let home = temp.path().canonicalize().unwrap();
        let root = home.join("Downloads");
        fs::create_dir(&root).unwrap();
        fs::create_dir(home.join("Desktop")).unwrap();
        fs::create_dir(root.join("cloud-dir")).unwrap();
        fs::write(root.join("cloud-dir/hidden"), b"must not read").unwrap();
        fs::write(root.join("cloud-file"), b"must not read").unwrap();
        fs::write(root.join("local"), b"local").unwrap();
        std::os::unix::fs::symlink(root.join("local"), root.join("link")).unwrap();
        for kind in [AvScanKind::Quick, AvScanKind::Full] {
            let roots = roots_at(
                kind,
                &home,
                None,
                &[],
                &[],
                std::slice::from_ref(&home),
                None,
            )
            .unwrap();
            let mut visited = Vec::new();
            let mut stats = AvSkipped::default();
            walk(
                roots,
                &[],
                &AtomicBool::new(false),
                &mut stats,
                &|p, _| {
                    p.file_name()
                        .is_some_and(|n| n == "cloud-file" || n == "cloud-dir")
                },
                &mut |p| {
                    assert_eq!(fs::read(p).unwrap(), b"local");
                    visited.push(p.to_owned());
                    Ok(())
                },
            )
            .unwrap();
            assert_eq!(visited, vec![root.join("local")]);
            assert_eq!(stats.dataless, 2);
            assert_eq!(stats.excluded, 1);
        }
    }
    #[test]
    fn all_scan_kinds_allow_large_installers_and_skip_large_ordinary_files() {
        let temp = tempfile::tempdir().unwrap();
        let home = temp.path().canonicalize().unwrap();
        let root = home.join("Downloads");
        fs::create_dir(&root).unwrap();
        fs::create_dir(home.join("Desktop")).unwrap();
        for (name, size) in [
            ("app.dmg", files::MAX_REGULAR_FILE + 1),
            ("bottle.tar.gz", files::MAX_REGULAR_FILE + 1),
            ("video.mp4", files::MAX_REGULAR_FILE + 1),
            ("huge.zip", files::MAX_INSTALLER_FILE + 1),
        ] {
            fs::File::create(root.join(name))
                .unwrap()
                .set_len(size)
                .unwrap();
        }
        for kind in [AvScanKind::Quick, AvScanKind::Full, AvScanKind::Custom] {
            let roots = roots_at(
                kind,
                &home,
                Some(vec![root.to_string_lossy().into_owned()]),
                &[],
                &[],
                std::slice::from_ref(&root),
                None,
            )
            .unwrap();
            let mut visited = BTreeSet::new();
            let mut skipped = AvSkipped::default();
            walk(
                roots,
                &[],
                &AtomicBool::new(false),
                &mut skipped,
                &|_, _| false,
                &mut |p| {
                    visited.insert(p.file_name().unwrap().to_string_lossy().into_owned());
                    Ok(())
                },
            )
            .unwrap();
            assert_eq!(
                visited,
                BTreeSet::from(["app.dmg".to_owned(), "bottle.tar.gz".to_owned()])
            );
            assert_eq!(skipped.too_large, 2);
        }
    }
}
