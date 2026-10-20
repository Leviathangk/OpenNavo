pub(crate) mod container;
pub mod protocol;
mod socket;
use super::{files, types::*};
use crate::{
    AppError,
    db::Database,
    events::{CoreEvent, EventSink},
    model::Settings,
};
use std::{
    fs::{self, File},
    io::Read,
    os::unix::fs::MetadataExt,
    path::{Path, PathBuf},
    process::{Child, Command, Stdio},
    sync::{
        Arc, Mutex, RwLock,
        atomic::{AtomicBool, AtomicUsize, Ordering},
    },
    time::{Duration, Instant},
};

#[derive(Clone)]
pub struct Config {
    pub helpers: PathBuf,
    pub database: PathBuf,
    pub runtime: PathBuf,
    pub idle: Duration,
}
impl Config {
    pub fn native(data: &Path) -> Self {
        let helpers = helper_directory(
            std::env::current_exe().ok().as_deref(),
            cfg!(debug_assertions),
        );
        Self {
            helpers,
            database: data.join("av/db"),
            runtime: data.join("av/run"),
            idle: Duration::from_secs(300),
        }
    }
}
fn helper_directory(executable: Option<&Path>, development: bool) -> PathBuf {
    // `tauri dev` runs target/debug directly, without an app bundle's Contents.
    let contents = executable
        .and_then(Path::parent)
        .filter(|parent| parent.file_name().is_some_and(|name| name == "MacOS"))
        .and_then(Path::parent)
        .filter(|parent| parent.file_name().is_some_and(|name| name == "Contents"));
    if let Some(contents) = contents {
        return contents.join("Library/Helpers/clamav");
    }
    if development {
        return Path::new(env!("CARGO_MANIFEST_DIR")).join("../native/staged/clamav");
    }
    // Release builds never fall back to executables in the source checkout.
    executable
        .and_then(Path::parent)
        .and_then(Path::parent)
        .map(|parent| parent.join("Library/Helpers/clamav"))
        .unwrap_or_default()
}

struct Daemon {
    child: Child,
    socket: socket::SocketDir,
    generation: Option<String>,
    fingerprint: String,
    last_used: Instant,
}
impl Drop for Daemon {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}
struct UpdaterChild(Child);
impl Drop for UpdaterChild {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}
pub struct Engine {
    pub config: Config,
    db: Database,
    settings: Arc<RwLock<Settings>>,
    sink: EventSink,
    daemon: Mutex<Option<Daemon>>,
    scan_serial: Mutex<()>,
    active: AtomicUsize,
    stopped: AtomicBool,
    updating: AtomicBool,
    last_error: Mutex<Option<AvEngineError>>,
    disk: Arc<Mutex<DiskBudget>>,
    children: Mutex<Vec<std::sync::Weak<Engine>>>,
}
fn poison<T>(_: T) -> AppError {
    AppError::new("E_AV_ENGINE", "av_lock")
}
fn now() -> i64 {
    chrono::Utc::now().timestamp_millis()
}
fn disk_budget(available: u64) -> Result<(), AppError> {
    if available < files::MAX_SCAN_SIZE + 128 * 1024 * 1024 {
        log::warn!("Skipping AV scan: insufficient free space for the 2 GiB extraction budget");
        Err(AppError::new("E_AV_SCAN_FAILED", "av_scan_too_large")
            .with_detail("insufficient_temporary_space"))
    } else {
        Ok(())
    }
}
#[derive(Default)]
pub(super) struct ScanTiming {
    pub queue_us: u64,
    pub inspection_us: u64,
    pub engine_us: u64,
    pub cleanup_us: u64,
}

#[derive(Default)]
struct DiskBudget {
    sampled: Option<Instant>,
    available: u64,
    reserved: u64,
}
struct DiskReservation(Arc<Mutex<DiskBudget>>);
impl Drop for DiskReservation {
    fn drop(&mut self) {
        let mut state = self.0.lock().unwrap_or_else(|e| e.into_inner());
        state.reserved = state.reserved.saturating_sub(files::MAX_SCAN_SIZE);
    }
}

pub(super) struct ScanSession {
    lease: Lease,
    pub fingerprint: String,
    budget: Duration,
}
impl ScanSession {
    pub fn validate(&self) -> Result<(), AppError> {
        let engine = &self.lease.engine;
        if engine.stopped.load(Ordering::Acquire)
            || !engine.settings.read().map_err(poison)?.av_consent
        {
            return Err(AppError::new("E_SCAN_CANCELED", "av_stopped"));
        }
        if engine
            .daemon
            .lock()
            .map_err(poison)?
            .as_ref()
            .is_some_and(|daemon| daemon.fingerprint != self.fingerprint)
        {
            return Err(AppError::new("E_AV_ENGINE", "av_database_changed"));
        }
        Ok(())
    }
    pub fn scan(
        &self,
        file: &mut File,
        cancel: &AtomicBool,
        timing: &mut ScanTiming,
    ) -> Result<Option<String>, AppError> {
        self.validate()?;
        let result = self
            .lease
            .engine
            .scan_loaded(file, cancel, self.budget, timing);
        self.validate()?;
        result
    }
}

pub struct Lease {
    engine: Arc<Engine>,
}
impl Drop for Lease {
    fn drop(&mut self) {
        if let Ok(mut daemon) = self.engine.daemon.lock()
            && let Some(d) = daemon.as_mut()
        {
            d.last_used = Instant::now();
        }
        self.engine.active.fetch_sub(1, Ordering::AcqRel);
    }
}
impl Engine {
    pub fn new(
        config: Config,
        db: Database,
        settings: Arc<RwLock<Settings>>,
        sink: EventSink,
    ) -> Arc<Self> {
        Self::new_with_disk(
            config,
            db,
            settings,
            sink,
            Arc::new(Mutex::new(DiskBudget::default())),
        )
    }
    fn new_with_disk(
        config: Config,
        db: Database,
        settings: Arc<RwLock<Settings>>,
        sink: EventSink,
        disk: Arc<Mutex<DiskBudget>>,
    ) -> Arc<Self> {
        let engine = Arc::new(Self {
            config,
            db,
            settings,
            sink,
            daemon: Mutex::new(None),
            scan_serial: Mutex::new(()),
            active: AtomicUsize::new(0),
            stopped: AtomicBool::new(false),
            updating: AtomicBool::new(false),
            last_error: Mutex::new(None),
            disk,
            children: Mutex::new(Vec::new()),
        });
        let weak = Arc::downgrade(&engine);
        std::thread::spawn(move || {
            loop {
                std::thread::sleep(Duration::from_millis(250));
                let Some(engine) = weak.upgrade() else {
                    break;
                };
                let resident = engine
                    .settings
                    .read()
                    .map(|s| s.av_keep_resident)
                    .unwrap_or(false);
                if let Ok(mut d) = engine.daemon.lock()
                    && let Some(daemon) = d.as_mut()
                {
                    let exited = daemon.child.try_wait().ok().flatten().is_some();
                    if exited
                        || (!resident
                            && engine.active.load(Ordering::Acquire) == 0
                            && daemon.last_used.elapsed() >= engine.config.idle)
                    {
                        protocol::shutdown(&daemon.socket.path());
                        *d = None;
                        (engine.sink)(CoreEvent::SecurityChanged(SecurityChanged {
                            reason: SecurityChangeReason::Engine,
                        }));
                    }
                }
            }
        });
        engine
    }
    /// Synchronous owner teardown, independent of the weak idle worker's lifetime.
    pub(crate) fn shutdown(&self) {
        self.stopped.store(true, Ordering::Release);
        let children: Vec<_> = self
            .children
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .iter()
            .filter_map(std::sync::Weak::upgrade)
            .collect();
        for child in children {
            child.shutdown();
            let _ = fs::remove_dir_all(&child.config.runtime);
        }
        let mut daemon = self.daemon.lock().unwrap_or_else(|e| e.into_inner());
        // Kill/wait before waiting for the request lock: the child may be stuck
        // extracting while its caller owns that lock.
        *daemon = None;
        drop(daemon);
        let _serial = self.scan_serial.lock().unwrap_or_else(|e| e.into_inner());
        let _ = self.clean_temporary();
    }
    /// An isolated daemon owns its socket and extraction directory. Canceling
    /// this worker cannot kill or clean another worker's in-flight request.
    pub(super) fn isolated(self: &Arc<Self>) -> Arc<Self> {
        let mut config = self.config.clone();
        config.runtime = self
            .config
            .runtime
            .join(format!("worker-{}", uuid::Uuid::now_v7()));
        let child = Self::new_with_disk(
            config,
            self.db.clone(),
            self.settings.clone(),
            self.sink.clone(),
            self.disk.clone(),
        );
        let mut children = self.children.lock().unwrap_or_else(|e| e.into_inner());
        children.retain(|child| child.strong_count() > 0);
        children.push(Arc::downgrade(&child));
        drop(children);
        // Covers a shutdown which raced with registration.
        if self.stopped.load(Ordering::Acquire) {
            child.shutdown();
        }
        child
    }

    pub(super) fn session(
        self: &Arc<Self>,
        cancel: &AtomicBool,
        budget: Duration,
    ) -> Result<ScanSession, AppError> {
        let lease = self.lease(cancel)?;
        let fingerprint = self
            .daemon
            .lock()
            .map_err(poison)?
            .as_ref()
            .ok_or_else(|| AppError::new("E_AV_ENGINE", "clamd_stopped"))?
            .fingerprint
            .clone();
        Ok(ScanSession {
            lease,
            fingerprint,
            budget,
        })
    }

    fn fingerprint(&self) -> Result<String, AppError> {
        use sha2::{Digest, Sha256};
        let mut paths = fs::read_dir(&self.config.database)?
            .map(|entry| entry.map(|e| e.path()))
            .collect::<std::io::Result<Vec<_>>>()?;
        paths.sort();
        paths.push(self.config.helpers.join("clamd"));
        let mut hash = Sha256::new();
        hash.update(b"opennavo-av-clean-v1:archives:encrypted:limits");
        hash.update(self.db.meta("av_db_generation")?.unwrap_or_default());
        for path in paths {
            let m = fs::symlink_metadata(&path)?;
            if !m.is_file() {
                continue;
            }
            hash.update(path.file_name().unwrap_or_default().as_encoded_bytes());
            for value in [
                m.dev(),
                m.ino(),
                m.len(),
                m.mtime() as u64,
                m.mtime_nsec() as u64,
                m.ctime() as u64,
                m.ctime_nsec() as u64,
            ] {
                hash.update(value.to_le_bytes());
            }
        }
        Ok(format!("{:x}", hash.finalize()))
    }

    pub fn ready(&self) -> Result<(), AppError> {
        if self.stopped.load(Ordering::Acquire) {
            return Err(AppError::new("E_AV_ENGINE", "av_stopped"));
        }
        if !self.settings.read().map_err(poison)?.av_consent {
            return Err(AppError::new("E_AV_CONSENT", "av_consent_required"));
        }
        if !self.config.helpers.join("clamd").is_file() || self.database_status()?.is_none() {
            return Err(AppError::new(
                "E_AV_NOT_READY",
                "av_database_or_engine_missing",
            ));
        }
        Ok(())
    }
    pub fn lease(self: &Arc<Self>, cancel: &AtomicBool) -> Result<Lease, AppError> {
        self.lease_inner(cancel).map_err(|error| {
            if matches!(
                error.code.as_str(),
                "E_UNKNOWN" | "E_PROTECTED" | "E_INVALID_ARG"
            ) {
                AppError::new("E_AV_ENGINE", "clamd_setup")
                    .with_detail(format!("{error} {:?}", error.detail))
            } else {
                error
            }
        })
    }
    fn lease_inner(self: &Arc<Self>, cancel: &AtomicBool) -> Result<Lease, AppError> {
        self.ready()?;
        let mut holder = self.daemon.lock().map_err(poison)?;
        if self.stopped.load(Ordering::Acquire) {
            return Err(AppError::new("E_AV_ENGINE", "av_stopped"));
        }
        if self.updating.load(Ordering::Acquire) {
            return Err(AppError::new("E_AV_NOT_READY", "av_database_updating"));
        }
        let generation = self.db.meta("av_db_generation")?;
        if let Some(d) = holder.as_mut()
            && (d.child.try_wait()?.is_some() || d.generation != generation)
        {
            // No RELOAD: only replace an idle instance, never interrupt another scan.
            if self.active.load(Ordering::Acquire) != 0 {
                return Err(AppError::new("E_AV_NOT_READY", "av_restart_pending"));
            }
            protocol::shutdown(&d.socket.path());
            *holder = None;
        }
        if holder.is_none() {
            files::private_dir(&self.config.runtime)?;
            let temporary = self.config.runtime.join("scan-tmp");
            files::private_dir(&temporary)?;
            self.clean_temporary()?;
            let socket_dir = socket::SocketDir::new()?;
            let socket = socket_dir.path();
            let config = self
                .config
                .runtime
                .join(format!("clamd-{}.conf", uuid::Uuid::now_v7()));
            let content = format!(
                "Foreground yes\nLocalSocket {}\nLocalSocketMode 600\nDatabaseDirectory {}\nCVDCertsDirectory {}\nTemporaryDirectory {}\nLeaveTemporaryFiles yes\nConcurrentDatabaseReload no\nSelfCheck 0\nMaxThreads 1\nMaxQueue 2\nMaxFileSize 1073741824\nMaxScanSize 2147483648\nStreamMaxLength 1073741824\nMaxFiles 50000\nMaxRecursion 17\nMaxScanTime 300000\nScanArchive yes\nAlertExceedsMax yes\nHeuristicScanPrecedence no\nAlertEncrypted yes\nFollowDirectorySymlinks no\nFollowFileSymlinks no\n",
                files::config_path(&socket)?,
                files::config_path(&self.config.database)?,
                files::config_path(&self.config.helpers)?,
                files::config_path(&temporary)?
            );
            files::write_private(&config, content.as_bytes())?;
            let fingerprint = self.fingerprint()?;
            let spawned = Command::new(self.config.helpers.join("clamd"))
                .arg(format!("--config-file={}", config.display()))
                .env("CVD_CERTS_DIR", &self.config.helpers)
                .env("TZ", "UTC")
                .env("LC_ALL", "C")
                .stdin(Stdio::null())
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .spawn();
            let child = match spawned {
                Ok(c) => c,
                Err(e) => {
                    let _ = fs::remove_file(&config);
                    return Err(
                        AppError::new("E_AV_ENGINE", "clamd_start").with_detail(e.to_string())
                    );
                }
            };
            let mut daemon = Daemon {
                child,
                socket: socket_dir,
                generation,
                fingerprint,
                last_used: Instant::now(),
            };
            let started = Instant::now();
            let loaded = (|| {
                loop {
                    files::canceled(cancel)?;
                    if self.stopped.load(Ordering::Acquire) {
                        return Err(AppError::new("E_SCAN_CANCELED", "av_stopped"));
                    }
                    if protocol::ping(&socket) {
                        break;
                    }
                    if daemon.child.try_wait()?.is_some()
                        || started.elapsed() > Duration::from_secs(90)
                    {
                        return Err(AppError::new("E_AV_ENGINE", "clamd_load"));
                    }
                    std::thread::sleep(Duration::from_millis(100));
                }
                let m = fs::symlink_metadata(&socket)?;
                daemon.socket.verify()?;
                use std::os::unix::fs::FileTypeExt;
                if !m.file_type().is_socket()
                    || m.mode() & 0o777 != 0o600
                    || m.uid() != unsafe { nix::libc::geteuid() }
                {
                    return Err(AppError::new("E_AV_ENGINE", "clamd_socket_permissions"));
                }
                Ok(())
            })();
            let _ = fs::remove_file(config);
            loaded?;
            *holder = Some(daemon);
            (self.sink)(CoreEvent::SecurityChanged(SecurityChanged {
                reason: SecurityChangeReason::Engine,
            }));
        }
        self.active.fetch_add(1, Ordering::AcqRel);
        Ok(Lease {
            engine: self.clone(),
        })
    }
    pub fn scan(
        self: &Arc<Self>,
        file: &mut File,
        cancel: &AtomicBool,
    ) -> Result<Option<String>, AppError> {
        let _lease = self.lease(cancel)?;
        self.scan_loaded(
            file,
            cancel,
            Duration::from_secs(300),
            &mut ScanTiming::default(),
        )
    }
    fn scan_loaded(
        self: &Arc<Self>,
        file: &mut File,
        cancel: &AtomicBool,
        budget: Duration,
        timing: &mut ScanTiming,
    ) -> Result<Option<String>, AppError> {
        let queued_at = Instant::now();
        // Serialize requests, including small compressed inputs which may expand
        // substantially. Cancellation can then terminate the one owned daemon
        // without aborting another caller's in-flight request.
        let _serial = loop {
            files::canceled(cancel)?;
            match self.scan_serial.try_lock() {
                Ok(guard) => break guard,
                Err(std::sync::TryLockError::WouldBlock) => {
                    std::thread::sleep(Duration::from_millis(25))
                }
                Err(e) => return Err(poison(e)),
            }
        };
        timing.queue_us = queued_at.elapsed().as_micros() as u64;
        files::canceled(cancel)?;
        if self.stopped.load(Ordering::Acquire) || !self.settings.read().map_err(poison)?.av_consent
        {
            return Err(AppError::new("E_SCAN_CANCELED", "av_stopped"));
        }
        // A timed-out file kills only this worker. Reload only in that case,
        // rather than reading the signature database headers for every file.
        let absent = self.daemon.lock().map_err(poison)?.is_none();
        let _restart = if absent {
            Some(self.lease(cancel)?)
        } else {
            None
        };
        let inspection_at = Instant::now();
        let _disk = self.reserve_disk()?;
        let coverage = container::inspect(file);
        let socket = {
            let daemon = self.daemon.lock().map_err(poison)?;
            let daemon = daemon
                .as_ref()
                .ok_or_else(|| AppError::new("E_AV_ENGINE", "clamd_stopped"))?;
            daemon.socket.verify()?;
            daemon.socket.path()
        };
        timing.inspection_us = inspection_at.elapsed().as_micros() as u64;
        let engine_at = Instant::now();
        let mut result = protocol::scan_with_budget(&socket, file, cancel, budget);
        timing.engine_us = engine_at.elapsed().as_micros() as u64;
        let cleanup_at = Instant::now();
        // Preserve a real finding even when another member cannot be inspected.
        if matches!(result, Ok(None)) {
            match coverage {
                Err(error) => result = Err(error),
                Ok(coverage) => {
                    let incomplete = coverage.incomplete.or_else(|| {
                        (coverage.dmg
                            && container::extracted_dmg_bytes(
                                &self.config.runtime.join("scan-tmp"),
                            ) == 0
                            && coverage.expected_dmg_bytes > 0)
                            .then_some("av_scan_unreadable")
                    });
                    if let Some(message) = incomplete {
                        result = Err(AppError::new("E_AV_SCAN_FAILED", message)
                            .with_detail("container_incomplete"));
                    }
                }
            }
        }
        if result
            .as_ref()
            .is_err_and(|e| e.code == "E_SCAN_CANCELED" || e.code == "E_AV_ENGINE")
        {
            if let Some(daemon) = self.daemon.lock().map_err(poison)?.as_mut() {
                log::warn!(
                    "ClamAV request failed: {:?}; child status before cleanup: {:?}",
                    result.as_ref().err(),
                    daemon.child.try_wait()
                );
            }
            // Closing FILDES alone does not guarantee the worker stops unpacking.
            // Kill/wait our child before deleting its temporary files. Next scan
            // starts the same engine afresh, even when another caller holds a lease.
            *self.daemon.lock().map_err(poison)? = None;
        }
        let cleanup = self.clean_temporary();
        if cleanup.is_err() {
            self.disk.lock().map_err(poison)?.sampled = None;
        }
        timing.cleanup_us = cleanup_at.elapsed().as_micros() as u64;
        if let Err(e) = &result {
            self.record_failure(e);
        }
        cleanup?;
        result
    }
    fn clean_temporary(&self) -> Result<(), AppError> {
        let root = self.config.runtime.join("scan-tmp");
        files::safe_metadata(&root)?;
        for entry in fs::read_dir(&root)? {
            let entry = entry?;
            if entry.file_type()?.is_dir() {
                fs::remove_dir_all(entry.path())?;
            } else {
                fs::remove_file(entry.path())?;
            }
        }
        Ok(())
    }
    fn reserve_disk(&self) -> Result<DiskReservation, AppError> {
        use std::os::unix::ffi::OsStrExt;
        let mut state = self.disk.lock().map_err(poison)?;
        if state
            .sampled
            .is_none_or(|sampled| sampled.elapsed() >= Duration::from_secs(1))
        {
            let path = std::ffi::CString::new(self.config.runtime.as_os_str().as_bytes())
                .map_err(|_| AppError::new("E_AV_SCAN_FAILED", "av_scan_unreadable"))?;
            let mut stat: nix::libc::statvfs = unsafe { std::mem::zeroed() };
            if unsafe { nix::libc::statvfs(path.as_ptr(), &mut stat) } != 0 {
                return Err(AppError::new("E_AV_SCAN_FAILED", "av_scan_unreadable"));
            }
            state.available = (stat.f_bavail as u64).saturating_mul(stat.f_frsize as u64);
            state.sampled = Some(Instant::now());
        }
        // All workers share reservations, even for tiny compressed inputs.
        // Release only after the child has finished and cleanup has completed.
        disk_budget(state.available.saturating_sub(state.reserved))?;
        state.reserved = state.reserved.saturating_add(files::MAX_SCAN_SIZE);
        Ok(DiskReservation(self.disk.clone()))
    }
    pub(crate) fn record_failure(&self, error: &AppError) {
        if error.code != "E_SCAN_CANCELED"
            && !matches!(
                error.message.as_str(),
                "av_scan_too_large" | "av_scan_unreadable"
            )
            && let Ok(mut last) = self.last_error.lock()
        {
            *last = Some(AvEngineError {
                code: error.code.clone(),
                at: now(),
            });
        }
    }
    pub fn status(&self) -> Result<AvEngineStatus, AppError> {
        let mut d = self.daemon.lock().map_err(poison)?;
        let pid = d.as_mut().and_then(|d| {
            d.child
                .try_wait()
                .ok()
                .flatten()
                .is_none()
                .then(|| d.child.id())
        });
        let resident_bytes = pid
            .and_then(|p| {
                Command::new("/bin/ps")
                    .args(["-o", "rss=", "-p", &p.to_string()])
                    .output()
                    .ok()
            })
            .and_then(|o| {
                String::from_utf8_lossy(&o.stdout)
                    .trim()
                    .parse::<u64>()
                    .ok()
            })
            .map(|v| v * 1024);
        Ok(AvEngineStatus {
            version: "1.5.4".into(),
            running: pid.is_some(),
            resident_bytes,
            updating: self.updating.load(Ordering::Acquire),
            db: self.database_status()?,
            last_error: self.last_error.lock().map_err(poison)?.clone(),
        })
    }
    fn database_status(&self) -> Result<Option<AvDatabaseStatus>, AppError> {
        let mut status = AvDatabaseStatus {
            daily: None,
            main: None,
            bytecode: None,
            updated_at: None,
            bytes: 0,
        };
        for (name, version) in [
            ("daily", &mut status.daily),
            ("main", &mut status.main),
            ("bytecode", &mut status.bytecode),
        ] {
            for ext in ["cld", "cvd"] {
                let path = self.config.database.join(format!("{name}.{ext}"));
                let Ok(mut file) = files::open_read(&path, 512 * 1024 * 1024) else {
                    continue;
                };
                let m = file.metadata()?;
                let mut header = [0u8; 512];
                if file.read_exact(&mut header).is_err() {
                    continue;
                }
                let text = String::from_utf8_lossy(&header);
                if !text.starts_with("ClamAV-VDB:") {
                    continue;
                }
                *version = text.split(':').nth(2).and_then(|v| v.parse().ok());
                if version.is_none() {
                    continue;
                }
                status.bytes += m.len();
                let updated = m
                    .modified()
                    .ok()
                    .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
                    .map(|d| d.as_millis() as i64);
                status.updated_at = status.updated_at.max(updated);
                break;
            }
        }
        Ok((status.daily.is_some() && status.main.is_some()).then_some(status))
    }
    pub fn check_update(&self) -> Result<(), AppError> {
        if !self.settings.read().map_err(poison)?.av_consent {
            return Err(AppError::new("E_AV_CONSENT", "av_consent_required"));
        }
        let until = self
            .db
            .meta("av_db_next_check")?
            .and_then(|v| v.parse::<i64>().ok())
            .unwrap_or(0);
        if until > now() {
            return Err(
                AppError::new("E_AV_DB_RATE_LIMITED", "av_database_cooldown").with_detail(
                    serde_json::json!({"retryAfterSecs":(until-now()+999)/1000}).to_string(),
                ),
            );
        }
        if !self.config.helpers.join("freshclam").is_file() {
            return Err(AppError::new("E_AV_ENGINE", "freshclam_missing"));
        }
        Ok(())
    }
    pub fn update(&self, cancel: &AtomicBool) -> Result<(), AppError> {
        self.check_update()?;
        // Synchronize with lease acquisition so freshclam never writes a DB being loaded.
        let daemon = self.daemon.lock().map_err(poison)?;
        if self.active.load(Ordering::Acquire) > 0 || self.updating.swap(true, Ordering::AcqRel) {
            return Err(AppError::new("E_AV_NOT_READY", "av_busy"));
        }
        drop(daemon);
        let result = self.update_inner(cancel);
        self.updating.store(false, Ordering::Release);
        if let Err(e) = &result {
            *self.last_error.lock().map_err(poison)? = Some(AvEngineError {
                code: e.code.clone(),
                at: now(),
            });
        } else {
            *self.last_error.lock().map_err(poison)? = None;
        }
        (self.sink)(CoreEvent::SecurityChanged(SecurityChanged {
            reason: SecurityChangeReason::Engine,
        }));
        result
    }
    fn update_inner(&self, cancel: &AtomicBool) -> Result<(), AppError> {
        files::canceled(cancel)?;
        self.check_update()?;
        files::private_dir(&self.config.runtime)?;
        files::private_dir(&self.config.database)?;
        let id = uuid::Uuid::now_v7();
        let config = self.config.runtime.join(format!("freshclam-{id}.conf"));
        let log = self.config.runtime.join(format!("freshclam-{id}.log"));
        files::write_private(&config,format!("DatabaseDirectory {}\nCVDCertsDirectory {}\nDatabaseMirror database.clamav.net\nDNSDatabaseInfo current.cvd.clamav.net\nTestDatabases no\nChecks 6\nConnectTimeout 30\nReceiveTimeout 60\n",files::config_path(&self.config.database)?,files::config_path(&self.config.helpers)?).as_bytes())?;
        files::write_private(&log, b"")?;
        let output = fs::OpenOptions::new().write(true).open(&log)?;
        // Persist before spawning: crashes/cancellation cannot bypass the one-hour floor.
        let hours = self
            .settings
            .read()
            .map_err(poison)?
            .av_db_check_hours
            .max(1) as i64;
        let jitter = (id.as_u128() % 601) as i64;
        self.db.set_meta(
            "av_db_next_check",
            &(now() + (hours * 3600 + jitter) * 1000).to_string(),
        )?;
        let result = (|| {
            let child = Command::new(self.config.helpers.join("freshclam"))
                .arg(format!("--config-file={}", config.display()))
                .env("CVD_CERTS_DIR", &self.config.helpers)
                .env("TZ", "UTC")
                .env("LC_ALL", "C")
                .stdin(Stdio::null())
                .stdout(output.try_clone()?)
                .stderr(output)
                .spawn()
                .map_err(|e| {
                    AppError::new("E_AV_DB_UPDATE", "freshclam_start").with_detail(e.to_string())
                })?;
            let mut child = UpdaterChild(child);
            let started = Instant::now();
            let status = loop {
                files::canceled(cancel)?;
                if let Some(s) = child.0.try_wait()? {
                    break s;
                }
                if started.elapsed() > Duration::from_secs(1800) {
                    return Err(AppError::new("E_AV_DB_UPDATE", "freshclam_timeout"));
                }
                std::thread::sleep(Duration::from_millis(100));
            };
            let text = String::from_utf8_lossy(&files::read(&log, 4 * 1024 * 1024)?).into_owned();
            if let Some(secs) = cooldown(&text) {
                self.db.set_meta(
                    "av_db_next_check",
                    &now().saturating_add(secs.saturating_mul(1000)).to_string(),
                )?;
                return Err(
                    AppError::new("E_AV_DB_RATE_LIMITED", "av_database_cdn_cooldown")
                        .with_detail(serde_json::json!({"retryAfterSecs":secs}).to_string()),
                );
            }
            if !status.success() {
                return Err(AppError::new("E_AV_DB_UPDATE", "freshclam_failed")
                    .with_detail(format!("exit={:?}", status.code())));
            }
            self.db.set_meta("av_db_generation", &now().to_string())?;
            Ok(())
        })();
        let _ = fs::remove_file(config);
        let _ = fs::remove_file(log);
        result
    }
}
pub fn cooldown(text: &str) -> Option<i64> {
    cooldown_at(text, now() / 1000)
}
fn cooldown_at(text: &str, now_secs: i64) -> Option<i64> {
    let lower = text.to_ascii_lowercase();
    // freshclam prints a local-time deadline, not the raw HTTP header. The child
    // runs with TZ=UTC; persist the same deadline even on its cached-cooldown exit 0.
    let deadline = text.lines().find_map(|line| {
        let lower = line.to_ascii_lowercase();
        let at = lower.find("cool-down until after:")?;
        let date = line[at + "cool-down until after:".len()..].trim();
        chrono::NaiveDateTime::parse_from_str(date, "%Y-%m-%d %H:%M:%S")
            .ok()
            .map(|d| d.and_utc().timestamp().saturating_sub(now_secs))
    });
    let received_403 = lower.contains("received error code 403") || lower.contains("http 403");
    if received_403 {
        return Some(deadline.unwrap_or(86400).max(86400));
    }
    if let Some(seconds) = deadline {
        return Some(seconds.max(3600));
    }
    if lower.contains("429") || lower.contains("rate limit") {
        let seconds = text.lines().find_map(|line| {
            let at = line.to_ascii_lowercase().find("retry-after:")?;
            let value = line[at + "retry-after:".len()..].trim();
            value.parse::<i64>().ok().or_else(|| {
                chrono::DateTime::parse_from_rfc2822(value)
                    .ok()
                    .map(|d| d.timestamp().saturating_sub(now_secs))
            })
        });
        return Some(seconds.unwrap_or(14400).max(3600));
    }
    None
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn parent_shutdown_reaps_an_isolated_worker_even_with_live_leases() {
        let (_fixture, service, _) = super::super::tests::fixture();
        super::super::tests::fake_engine(&service);
        service.settings.write().unwrap().av_consent = true;
        let child = service.av.isolated();
        let _lease = child.lease(&AtomicBool::new(false)).unwrap();
        let pid = child.daemon.lock().unwrap().as_ref().unwrap().child.id();
        service.av.shutdown();
        assert!(child.stopped.load(Ordering::Acquire));
        assert!(child.daemon.lock().unwrap().is_none());
        assert_eq!(unsafe { nix::libc::kill(pid as i32, 0) }, -1);
        assert_eq!(
            std::io::Error::last_os_error().raw_os_error(),
            Some(nix::libc::ESRCH)
        );
        assert!(!child.config.runtime.exists());
        assert!(service.av.isolated().stopped.load(Ordering::Acquire));
    }

    #[test]
    fn shared_disk_reservations_bound_concurrent_extraction() {
        let (_fixture, service, _) = super::super::tests::fixture();
        let other = service.av.isolated();
        assert!(Arc::ptr_eq(&service.av.disk, &other.disk));
        {
            let mut state = service.av.disk.lock().unwrap();
            state.sampled = Some(Instant::now());
            state.available = 2 * files::MAX_SCAN_SIZE + 128 * 1024 * 1024;
        }
        let first = service.av.reserve_disk().unwrap();
        let second = other.reserve_disk().unwrap();
        assert!(service.av.reserve_disk().is_err());
        drop(first);
        assert!(service.av.reserve_disk().is_ok());
        drop(second);
        other.shutdown();
    }

    #[test]
    fn bounded_session_timeout_restarts_its_worker_and_cleans_extraction() {
        let (_fixture, service, _) = super::super::tests::fixture();
        super::super::tests::fake_engine(&service);
        service.settings.write().unwrap().av_consent = true;
        let cancel = AtomicBool::new(false);
        let session = service
            .av
            .session(&cancel, Duration::from_millis(100))
            .unwrap();
        let path = service.home.join("slow");
        fs::write(&path, b"wait-for-test-cancel").unwrap();
        let mut timing = ScanTiming::default();
        let error = session
            .scan(&mut File::open(&path).unwrap(), &cancel, &mut timing)
            .unwrap_err();
        assert_eq!(error.message, "av_scan_too_large");
        assert!(service.av.daemon.lock().unwrap().is_none());
        assert!(
            !service
                .av
                .config
                .runtime
                .join("scan-tmp/partial-extraction")
                .exists()
        );
        fs::write(&path, b"clean").unwrap();
        assert!(
            session
                .scan(
                    &mut File::open(path).unwrap(),
                    &cancel,
                    &mut ScanTiming::default()
                )
                .unwrap()
                .is_none()
        );
    }

    #[test]
    fn long_home_can_scan_and_shutdown_removes_its_short_private_socket() {
        let home = format!("Users/{}", "long-username-".repeat(8));
        let runtime = format!(
            "{home}/Library/Application Support/com.opennavo.desktop.m5acceptance20261016/security/av/run"
        );
        let (_fixture, service, _) = super::super::tests::fixture_at(&home, &runtime);
        super::super::tests::fake_engine(&service);
        service.settings.write().unwrap().av_consent = true;
        assert!(
            service
                .av
                .config
                .runtime
                .join("s.sock")
                .as_os_str()
                .as_encoded_bytes()
                .len()
                >= 104
        );
        let sample = service.home.join("sample");
        fs::write(&sample, "fixture-threat").unwrap();
        assert_eq!(
            service
                .av
                .scan(&mut File::open(sample).unwrap(), &AtomicBool::new(false))
                .unwrap()
                .as_deref(),
            Some("Fixture.Test")
        );
        let (path, pid) = {
            let daemon = service.av.daemon.lock().unwrap();
            let daemon = daemon.as_ref().unwrap();
            (daemon.socket.path(), daemon.child.id())
        };
        assert!(path.exists());
        assert!(path.as_os_str().as_encoded_bytes().len() < 104);
        service.shutdown();
        assert!(!path.parent().unwrap().exists());
        assert_eq!(unsafe { nix::libc::kill(pid as i32, 0) }, -1);
        assert_eq!(
            std::io::Error::last_os_error().raw_os_error(),
            Some(nix::libc::ESRCH)
        );
    }
    #[test]
    fn fixture_drop_kills_and_reaps_daemon_even_with_an_idle_worker_reference() {
        let (fixture, service, _) = super::super::tests::fixture();
        super::super::tests::fake_engine(&service);
        service.settings.write().unwrap().av_consent = true;
        service.settings.write().unwrap().av_keep_resident = true;
        let engine = service.av.clone();
        let lease = engine.lease(&AtomicBool::new(false)).unwrap();
        let pid = engine.daemon.lock().unwrap().as_ref().unwrap().child.id();
        let runtime = engine.config.runtime.clone();
        // The extra Arc stands in for the maintenance worker between weak.upgrade
        // and the end of its loop. Teardown must not wait for that Arc to drop.
        drop(fixture);
        assert!(!runtime.exists());
        assert_eq!(unsafe { nix::libc::kill(pid as i32, 0) }, -1);
        assert_eq!(
            std::io::Error::last_os_error().raw_os_error(),
            Some(nix::libc::ESRCH)
        );
        assert!(engine.daemon.lock().unwrap().is_none());
        assert!(engine.lease(&AtomicBool::new(false)).is_err());
        drop(lease);
    }
    #[test]
    fn helpers_resolve_for_development_and_bundled_apps() {
        let dev = Path::new("/checkout/target/debug/opennavo-desktop");
        let staged = Path::new(env!("CARGO_MANIFEST_DIR")).join("../native/staged/clamav");
        assert_eq!(helper_directory(Some(dev), true), staged);
        assert_ne!(helper_directory(Some(dev), false), staged);
        let app = Path::new("/Applications/OpenNavo.app/Contents/MacOS/OpenNavo");
        let bundled = Path::new("/Applications/OpenNavo.app/Contents/Library/Helpers/clamav");
        for development in [true, false] {
            assert_eq!(helper_directory(Some(app), development), bundled);
        }
    }

    #[test]
    fn insufficient_temporary_space_is_incomplete_not_clean() {
        let e = disk_budget(files::MAX_SCAN_SIZE - 1).unwrap_err();
        assert_eq!(e.message, "av_scan_too_large");
        assert_eq!(e.detail.as_deref(), Some("insufficient_temporary_space"));
        assert!(disk_budget(files::MAX_SCAN_SIZE + 128 * 1024 * 1024).is_ok());
    }
    #[test]
    fn concurrent_callers_share_one_serial_worker_and_clean_temporary_files() {
        let (_tmp, service, _) = super::super::tests::fixture();
        super::super::tests::fake_engine(&service);
        service.settings.write().unwrap().av_consent = true;
        let path = service.home.join("input.zip");
        fs::write(&path, "serial-scan").unwrap();
        let mut workers = vec![];
        for _ in 0..4 {
            let engine = service.av.clone();
            let path = path.clone();
            workers.push(std::thread::spawn(move || {
                engine
                    .scan(&mut File::open(path).unwrap(), &AtomicBool::new(false))
                    .unwrap()
            }));
        }
        for worker in workers {
            assert!(worker.join().unwrap().is_none());
        }
        assert_eq!(
            fs::read_dir(service.av.config.runtime.join("scan-tmp"))
                .unwrap()
                .count(),
            0
        );
    }
    #[test]
    fn cooldown_obeys_freshclam_deadlines_and_http_dates() {
        let epoch = chrono::DateTime::parse_from_rfc3339("2026-10-16T00:00:00Z")
            .unwrap()
            .timestamp();
        assert_eq!(
            cooldown_at("HTTP 429\nRetry-After: 7200", epoch),
            Some(7200)
        );
        assert_eq!(
            cooldown_at(
                "HTTP 429\nRetry-After: Fri, 16 Oct 2026 03:00:00 GMT",
                epoch
            ),
            Some(10800)
        );
        assert_eq!(
            cooldown_at(
                "FreshClam previously received error code 429 or 403\nWARNING: You are still on cool-down until after: 2026-10-16 01:30:00",
                epoch
            ),
            Some(5400)
        );
        assert_eq!(
            cooldown_at("FreshClam received error code 403", epoch),
            Some(86400)
        );
        assert_eq!(cooldown_at("HTTP 429\nRetry-After: 1", epoch), Some(3600));
        assert_eq!(
            cooldown_at("daily.cvd updated 1403 signatures", epoch),
            None
        );
    }
}
