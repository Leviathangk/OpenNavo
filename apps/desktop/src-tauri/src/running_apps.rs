//! Running-app protection before updates: request normal quit only; never start brew writes unless exit is confirmed.
use crate::{
    AppError,
    brew::{
        args::{ReadOp, validate_token},
        json::Info,
        locate,
    },
    db::Database,
    model::*,
};
use std::{
    path::{Path, PathBuf},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};
use tokio::sync::Notify;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Process {
    pub pid: i32,
    pub path: PathBuf,
    pub bundle_id: String,
}

pub trait AppControl: Send + Sync {
    fn list(&self) -> Result<Vec<Process>, AppError>;
    fn quit(&self, process: &Process) -> Result<bool, AppError>;
    fn reopen(&self, path: &Path) -> Result<(), AppError>;
    /// Bring an app forward, e.g. so its "save changes?" sheet is seen while OpenNavo waits for it to quit.
    fn activate(&self, _process: &Process) -> Result<(), AppError> {
        Ok(())
    }
}

pub struct NativeApps;
#[cfg(target_os = "macos")]
impl AppControl for NativeApps {
    fn list(&self) -> Result<Vec<Process>, AppError> {
        use objc2_app_kit::NSWorkspace;
        objc2::rc::autoreleasepool(|_| {
            Ok(NSWorkspace::sharedWorkspace()
                .runningApplications()
                .iter()
                .map(|app| {
                    // Retain identifiers even without bundleURL so recognizable running apps are not mistaken for exited apps.
                    let path = app
                        .bundleURL()
                        .and_then(|url| url.path())
                        .map(|p| p.to_string())
                        .unwrap_or_default();
                    Process {
                        pid: app.processIdentifier(),
                        path: path.into(),
                        bundle_id: app
                            .bundleIdentifier()
                            .map(|s| s.to_string())
                            .unwrap_or_default(),
                    }
                })
                .collect())
        })
    }
    fn quit(&self, process: &Process) -> Result<bool, AppError> {
        use objc2_app_kit::NSRunningApplication;
        // Quitting ourselves would interrupt the queue; PID reuse must not close unrelated apps.
        if process.pid == std::process::id() as i32 {
            return Ok(false);
        }
        objc2::rc::autoreleasepool(|_| {
            let Some(app) =
                NSRunningApplication::runningApplicationWithProcessIdentifier(process.pid)
            else {
                return Ok(true);
            };
            let same = app
                .bundleURL()
                .and_then(|url| url.path())
                .is_some_and(|p| canonical(Path::new(&p.to_string())) == canonical(&process.path));
            Ok(same && app.terminate())
        })
    }
    fn activate(&self, process: &Process) -> Result<(), AppError> {
        use objc2_app_kit::{NSApplicationActivationOptions, NSRunningApplication};
        if process.pid == std::process::id() as i32 {
            return Ok(());
        }
        objc2::rc::autoreleasepool(|_| {
            if let Some(app) =
                NSRunningApplication::runningApplicationWithProcessIdentifier(process.pid)
            {
                app.activateWithOptions(NSApplicationActivationOptions::ActivateAllWindows);
            }
            Ok(())
        })
    }
    fn reopen(&self, path: &Path) -> Result<(), AppError> {
        use objc2_app_kit::NSWorkspace;
        use objc2_foundation::{NSString, NSURL};
        objc2::rc::autoreleasepool(|_| {
            let url = NSURL::fileURLWithPath(&NSString::from_str(&path.to_string_lossy()));
            if NSWorkspace::sharedWorkspace().openURL(&url) {
                Ok(())
            } else {
                Err(check_failed())
            }
        })
    }
}
#[cfg(not(target_os = "macos"))]
impl AppControl for NativeApps {
    fn list(&self) -> Result<Vec<Process>, AppError> {
        Err(check_failed())
    }
    fn quit(&self, _: &Process) -> Result<bool, AppError> {
        Err(check_failed())
    }
    fn reopen(&self, _: &Path) -> Result<(), AppError> {
        Err(check_failed())
    }
}

fn canonical(path: &Path) -> PathBuf {
    path.canonicalize().unwrap_or_else(|_| path.to_owned())
}
fn check_failed() -> AppError {
    AppError::new("E_APP_CHECK_FAILED", "app_check_failed")
}
pub fn deferred(error: &AppError) -> bool {
    matches!(
        error.code.as_str(),
        "E_APP_RUNNING" | "E_APP_QUIT_FAILED" | "E_APP_CHECK_FAILED"
    )
}

#[derive(Default)]
pub struct Identity {
    pub paths: Vec<PathBuf>,
    pub bundle_ids: Vec<String>,
    /// Other installed apps: a process running from one of them is never matched by bundle ID alone.
    pub exclude: Vec<PathBuf>,
    /// Bring apps that have not quit after this long to the front, so a "save changes?" sheet gets noticed.
    pub reveal_after: Option<Duration>,
}
impl Identity {
    fn matches(&self, process: &Process) -> bool {
        let path = canonical(&process.path);
        self.paths
            .iter()
            .any(|root| path.starts_with(canonical(root)))
            || (self.bundle_ids.iter().any(|id| {
                if let Some(prefix) = id.strip_suffix('*') {
                    !prefix.is_empty() && process.bundle_id.starts_with(prefix)
                } else {
                    process.bundle_id == *id
                }
            }) && !self
                .exclude
                .iter()
                .any(|other| path.starts_with(canonical(other))))
    }
    fn running(&self, control: &dyn AppControl) -> Result<Vec<Process>, AppError> {
        control
            .list()
            .map(|list| list.into_iter().filter(|p| self.matches(p)).collect())
            .map_err(|_| check_failed())
    }
}

pub async fn identity(
    database: &Database,
    settings: &Settings,
    target: &TaskTarget,
) -> Result<Identity, AppError> {
    validate_token(&target.token)?;
    if target.kind != Kind::Cask {
        return Err(AppError::new("E_INVALID_ARG", "cask_only"));
    }
    let cache = database.clone();
    let cached: Vec<InstalledItem> = crate::blocking(move || {
        cache
            .meta("installed_cache")?
            .map(|s| serde_json::from_str(&s).map_err(AppError::from))
            .transpose()
            .map(|v| v.unwrap_or_default())
    })
    .await
    .map_err(|_| check_failed())?;
    let mut paths: Vec<PathBuf> = cached
        .iter()
        .find(|i| i.kind == target.kind && i.token == target.token)
        .map(|i| i.app_paths.iter().map(PathBuf::from).collect())
        .unwrap_or_default();
    let mut bundle_ids = Vec::new();
    if paths.is_empty() {
        // pkg apps may lack app artifacts; read Cask quit identifiers. Fonts and other non-app artifacts need no blocking.
        let brew = locate::locate(settings).await.map_err(|_| check_failed())?;
        let json = locate::read(
            &brew,
            settings,
            ReadOp::Info(Kind::Cask),
            Some(&target.token),
        )
        .await
        .map_err(|_| check_failed())?;
        let info: Info = serde_json::from_str(&json).map_err(|_| check_failed())?;
        let cask = info
            .casks
            .iter()
            .find(|c| c.token == target.token)
            .ok_or_else(check_failed)?;
        let artifacts = cask.artifacts.as_ref().ok_or_else(check_failed)?;
        paths = crate::library::app_paths(cask)
            .into_iter()
            .map(PathBuf::from)
            .collect();
        for artifact in artifacts {
            for directive in artifact
                .get("uninstall")
                .and_then(|v| v.as_array())
                .into_iter()
                .flatten()
            {
                if let Some(quit) = directive.get("quit") {
                    if let Some(id) = quit.as_str() {
                        bundle_ids.push(id.to_owned());
                    }
                    if let Some(ids) = quit.as_array() {
                        bundle_ids
                            .extend(ids.iter().filter_map(|id| id.as_str().map(str::to_owned)));
                    }
                }
            }
        }
        if paths.is_empty()
            && bundle_ids.is_empty()
            && artifacts
                .iter()
                .any(|a| a.get("pkg").is_some() || a.get("installer").is_some())
        {
            return Err(check_failed());
        }
    }
    crate::blocking(move || {
        for path in &paths {
            if !path.is_absolute()
                || path.extension().is_none_or(|e| e != "app")
                || path
                    .components()
                    .any(|c| c == std::path::Component::ParentDir)
            {
                return Err(check_failed());
            }
            // A removed bundle (or missing plist) does not prevent checking the
            // destination path. Keep it in Identity: a process can still be
            // running from that path after its on-disk bundle was removed.
            // Permission and parsing failures remain errors, not "not running".
            let file = match std::fs::File::open(path.join("Contents/Info.plist")) {
                Ok(file) => file,
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => continue,
                Err(_) => return Err(check_failed()),
            };
            let value = plist::Value::from_reader(file).map_err(|_| check_failed())?;
            let id = value
                .as_dictionary()
                .and_then(|d| d.get("CFBundleIdentifier"))
                .and_then(|v| v.as_string())
                .filter(|s| !s.is_empty())
                .ok_or_else(check_failed)?;
            bundle_ids.push(id.to_owned());
        }
        // Interpret only exact identifiers and trailing wildcards; defer unrecognized rules instead of assuming no app is running.
        if bundle_ids.iter().any(|id| {
            let prefix = id.strip_suffix('*').unwrap_or(id);
            prefix.is_empty()
                || !prefix.contains('.')
                || !prefix
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b"._-".contains(&b))
        }) {
            return Err(check_failed());
        }
        Ok(Identity {
            paths,
            bundle_ids,
            ..Default::default()
        })
    })
    .await
}

pub async fn inspect(
    database: &Database,
    settings: &Settings,
    control: Arc<dyn AppControl>,
    targets: Vec<TaskTarget>,
) -> Result<Vec<RunningApp>, AppError> {
    if targets.len() > 200 {
        return Err(AppError::new("E_INVALID_ARG", "too_many_targets"));
    }
    let mut result = Vec::new();
    for target in targets {
        let identity = match identity(database, settings, &target).await {
            Ok(identity) => identity,
            // Let the queue defer unrecognized targets individually without blocking other apps in a batch.
            Err(error) if deferred(&error) => continue,
            Err(error) => return Err(error),
        };
        let native = control.clone();
        if !crate::blocking(move || identity.running(native.as_ref()))
            .await?
            .is_empty()
        {
            let name = crate::catalog::get(database, target.kind.brew_kind()?, &target.token)?;
            let name = crate::library::display_name(name.as_ref(), &target.token, settings.locale);
            result.push(RunningApp { target, name });
        }
    }
    Ok(result)
}

pub async fn prepare(
    identity: Arc<Identity>,
    control: Arc<dyn AppControl>,
    allow_quit: bool,
    cancel: Arc<AtomicBool>,
    notify: Arc<Notify>,
    timeout: Duration,
) -> Result<Vec<PathBuf>, AppError> {
    let detect = || {
        let identity = identity.clone();
        let control = control.clone();
        crate::blocking(move || identity.running(control.as_ref()))
    };
    let running = detect().await?;
    if running.is_empty() {
        return Ok(vec![]);
    }
    if !allow_quit {
        return Err(AppError::new("E_APP_RUNNING", "app_running"));
    }
    let mut reopen = Vec::new();
    for process in running {
        if cancel.load(Ordering::Acquire) {
            return Err(AppError::new("E_INTERRUPTED", "task_canceled"));
        }
        let path = process.path.clone();
        let native = control.clone();
        let accepted = crate::blocking(move || native.quit(&process))
            .await
            .map_err(|_| AppError::new("E_APP_QUIT_FAILED", "quit_failed"))?;
        if !accepted {
            return Err(AppError::new("E_APP_QUIT_FAILED", "quit_refused"));
        }
        // Do not reopen helper processes as standalone apps.
        if !reopen.contains(&path) && !path.to_string_lossy().contains(".app/Contents/") {
            reopen.push(path);
        }
    }
    let start = Instant::now();
    let mut revealed = false;
    loop {
        if cancel.load(Ordering::Acquire) {
            return Err(AppError::new("E_INTERRUPTED", "task_canceled"));
        }
        let remaining = detect().await?;
        if remaining.is_empty() {
            return Ok(reopen);
        }
        if !revealed
            && identity
                .reveal_after
                .is_some_and(|after| start.elapsed() >= after)
        {
            revealed = true;
            for process in remaining {
                let native = control.clone();
                let _ = crate::blocking(move || native.activate(&process)).await;
            }
        }
        if start.elapsed() >= timeout {
            return Err(AppError::new("E_APP_QUIT_FAILED", "quit_timeout"));
        }
        tokio::select! { _ = tokio::time::sleep(Duration::from_millis(200)) => {}, _ = notify.notified() => {} }
    }
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    use std::sync::Mutex;
    #[derive(Default)]
    pub struct FakeApps {
        pub processes: Mutex<Vec<Process>>,
        pub quits: Mutex<Vec<i32>>,
        pub reopened: Mutex<Vec<PathBuf>>,
        pub refuse: AtomicBool,
        pub stay_open: AtomicBool,
        pub check_fails: AtomicBool,
    }
    impl AppControl for FakeApps {
        fn list(&self) -> Result<Vec<Process>, AppError> {
            if self.check_fails.load(Ordering::Acquire) {
                return Err(check_failed());
            }
            Ok(self.processes.lock().unwrap().clone())
        }
        fn quit(&self, process: &Process) -> Result<bool, AppError> {
            self.quits.lock().unwrap().push(process.pid);
            if self.refuse.load(Ordering::Acquire) {
                return Ok(false);
            }
            if !self.stay_open.load(Ordering::Acquire) {
                self.processes
                    .lock()
                    .unwrap()
                    .retain(|p| p.pid != process.pid);
            }
            Ok(true)
        }
        fn reopen(&self, path: &Path) -> Result<(), AppError> {
            self.reopened.lock().unwrap().push(path.into());
            Ok(())
        }
    }
    pub fn cache_apps(database: &Database, tokens: &[&str]) {
        let db_path: String = database
            .with(|c| {
                Ok(c.query_row(
                    "SELECT file FROM pragma_database_list WHERE name='main'",
                    [],
                    |r| r.get(0),
                )?)
            })
            .unwrap();
        let root = Path::new(&db_path).parent().unwrap().join("apps");
        let items: Vec<_> = tokens.iter().map(|token| {
            let app = root.join(format!("{token}.app"));
            std::fs::create_dir_all(app.join("Contents")).unwrap();
            std::fs::write(app.join("Contents/Info.plist"), format!("<?xml version=\"1.0\"?><plist version=\"1.0\"><dict><key>CFBundleIdentifier</key><string>test.{token}</string></dict></plist>")).unwrap();
            serde_json::json!({
                "kind":"cask", "token":token, "name":token, "installedVersion":"1", "actualVersion":null,
                "latestVersion":"2", "status":"outdated", "onRequest":true, "requiredBy":[],
                "installedAt":null, "sizeBytes":null, "appPaths":[app],
                "iconPath":null, "autoUpdates":false, "pinned":false
            })
        }).collect();
        database
            .set_meta("installed_cache", &serde_json::to_string(&items).unwrap())
            .unwrap();
    }
    pub fn process(name: &str, pid: i32) -> Process {
        Process {
            pid,
            path: format!("/Applications/{name}.app").into(),
            bundle_id: format!("test.{name}"),
        }
    }
    fn identity() -> Arc<Identity> {
        Arc::new(Identity {
            paths: vec!["/Applications/target.app".into()],
            bundle_ids: vec!["test.target".into()],
            ..Default::default()
        })
    }
    #[tokio::test]
    async fn missing_bundle_or_plist_still_checks_running_processes_by_path() {
        for remove_bundle in [false, true] {
            let tmp = tempfile::tempdir().unwrap();
            let database = Database::open(&tmp.path().join("db")).unwrap();
            cache_apps(&database, &["target"]);
            let path = tmp.path().canonicalize().unwrap().join("apps/target.app");
            if remove_bundle {
                std::fs::remove_dir_all(&path).unwrap();
            } else {
                std::fs::remove_file(path.join("Contents/Info.plist")).unwrap();
            }
            let identity = Arc::new(
                super::identity(
                    &database,
                    &Settings::default(),
                    &TaskTarget {
                        kind: TaskKind::Cask,
                        token: "target".into(),
                    },
                )
                .await
                .unwrap(),
            );
            assert_eq!(identity.paths, vec![path.clone()]);
            let apps = Arc::new(FakeApps::default());
            let check = || {
                prepare(
                    identity.clone(),
                    apps.clone(),
                    false,
                    Arc::new(AtomicBool::new(false)),
                    Arc::new(Notify::new()),
                    Duration::ZERO,
                )
            };
            assert!(check().await.unwrap().is_empty());
            // Even a helper with a different identifier must block replacement.
            apps.processes.lock().unwrap().push(Process {
                pid: 42,
                path: path.join("Contents/Helpers/Helper.app"),
                bundle_id: "test.helper".into(),
            });
            assert_eq!(check().await.unwrap_err().code, "E_APP_RUNNING");
            assert!(apps.quits.lock().unwrap().is_empty());
        }
    }

    #[tokio::test]
    async fn malformed_plist_does_not_bypass_running_app_protection() {
        let tmp = tempfile::tempdir().unwrap();
        let database = Database::open(&tmp.path().join("db")).unwrap();
        cache_apps(&database, &["target"]);
        std::fs::write(
            tmp.path().join("apps/target.app/Contents/Info.plist"),
            "bad",
        )
        .unwrap();
        let result = super::identity(
            &database,
            &Settings::default(),
            &TaskTarget {
                kind: TaskKind::Cask,
                token: "target".into(),
            },
        )
        .await;
        assert_eq!(result.err().unwrap().code, "E_APP_CHECK_FAILED");
    }

    async fn prepare_test(apps: Arc<FakeApps>, allow: bool) -> Result<Vec<PathBuf>, AppError> {
        prepare(
            identity(),
            apps,
            allow,
            Arc::new(AtomicBool::new(false)),
            Arc::new(Notify::new()),
            Duration::ZERO,
        )
        .await
    }
    #[tokio::test]
    async fn no_consent_never_quits_and_consent_only_quits_target() {
        let apps = Arc::new(FakeApps::default());
        apps.processes
            .lock()
            .unwrap()
            .extend([process("target", 1), process("other", 2)]);
        assert_eq!(
            prepare_test(apps.clone(), false).await.unwrap_err().code,
            "E_APP_RUNNING"
        );
        assert!(apps.quits.lock().unwrap().is_empty());
        assert_eq!(
            prepare_test(apps.clone(), true).await.unwrap(),
            vec![PathBuf::from("/Applications/target.app")]
        );
        assert_eq!(*apps.quits.lock().unwrap(), vec![1]);
        assert_eq!(*apps.processes.lock().unwrap(), vec![process("other", 2)]);
    }
    #[tokio::test]
    async fn refusal_timeout_and_detection_failure_fail_closed() {
        let apps = Arc::new(FakeApps::default());
        apps.processes.lock().unwrap().push(process("target", 1));
        apps.refuse.store(true, Ordering::Release);
        assert_eq!(
            prepare_test(apps.clone(), true).await.unwrap_err().code,
            "E_APP_QUIT_FAILED"
        );
        apps.refuse.store(false, Ordering::Release);
        apps.stay_open.store(true, Ordering::Release);
        assert_eq!(
            prepare_test(apps.clone(), true).await.unwrap_err().code,
            "E_APP_QUIT_FAILED"
        );
        apps.check_fails.store(true, Ordering::Release);
        assert_eq!(
            prepare_test(apps, true).await.unwrap_err().code,
            "E_APP_CHECK_FAILED"
        );
    }
    #[tokio::test]
    async fn cancellation_never_sends_quit() {
        let apps = Arc::new(FakeApps::default());
        apps.processes.lock().unwrap().push(process("target", 1));
        let result = prepare(
            identity(),
            apps.clone(),
            true,
            Arc::new(AtomicBool::new(true)),
            Arc::new(Notify::new()),
            Duration::from_secs(15),
        )
        .await;
        assert_eq!(result.unwrap_err().code, "E_INTERRUPTED");
        assert!(apps.quits.lock().unwrap().is_empty());
    }
    #[test]
    fn paths_match_helpers_but_not_similarly_named_apps() {
        let identity = identity();
        assert!(identity.matches(&Process {
            pid: 1,
            path: "/Applications/target.app/Contents/Helpers/Helper.app".into(),
            bundle_id: "test.helper".into()
        }));
        assert!(!identity.matches(&process("target-extra", 2)));
        assert!(identity.matches(&Process {
            pid: 3,
            path: "/Users/test/Applications/target.app".into(),
            bundle_id: "test.target".into()
        }));
    }
}
