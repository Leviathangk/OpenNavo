#![cfg(unix)]
use opennavo_desktop_lib::{
    AppError,
    db::Database,
    events::CoreEvent,
    model::*,
    queue::TaskQueue,
    running_apps::{AppControl, Process},
    uninstall::{
        Engine, EngineConfig, ItemAction,
        apps::stable_id,
        execute::{AdminActions, AdminRequest, AdminResult, FileActions, remove},
        identity::{Identity, MetadataProbe, Receipt, bundle},
        rules::{Roots, Snapshot, allocated_size, expand, meaningful, name_match, normalize},
    },
};
use std::{
    collections::BTreeMap,
    fs,
    os::unix::fs::{PermissionsExt, symlink},
    path::{Path, PathBuf},
    sync::{
        Arc, Mutex, RwLock,
        atomic::{AtomicBool, AtomicUsize, Ordering},
    },
    time::Duration,
};

#[derive(Default)]
struct Probe {
    identities: Mutex<BTreeMap<PathBuf, Identity>>,
    receipts: Mutex<BTreeMap<PathBuf, Vec<Receipt>>>,
    delay: AtomicBool,
    loaded: AtomicBool,
    /// Opt into the engine's probe cache, like the native probe.
    cache: AtomicBool,
    calls: AtomicUsize,
}
impl MetadataProbe for Probe {
    fn identity(&self, path: &Path) -> Identity {
        self.calls.fetch_add(1, Ordering::AcqRel);
        if self.delay.load(Ordering::Acquire) {
            std::thread::sleep(Duration::from_millis(100));
        }
        self.identities
            .lock()
            .unwrap()
            .get(path)
            .cloned()
            .unwrap_or_else(|| bundle(path))
    }
    fn receipts(&self, path: &Path) -> Vec<Receipt> {
        self.receipts
            .lock()
            .unwrap()
            .get(path)
            .cloned()
            .unwrap_or_default()
    }
    fn loaded(&self, _: &str, _: bool) -> bool {
        self.loaded.load(Ordering::Acquire)
    }
    fn cacheable(&self) -> bool {
        self.cache.load(Ordering::Acquire)
    }
}
#[derive(Default)]
struct Apps {
    running: Mutex<Vec<Process>>,
    reject: AtomicBool,
    quits: AtomicUsize,
}
impl AppControl for Apps {
    fn list(&self) -> Result<Vec<Process>, AppError> {
        Ok(self.running.lock().unwrap().clone())
    }
    fn quit(&self, process: &Process) -> Result<bool, AppError> {
        self.quits.fetch_add(1, Ordering::AcqRel);
        if self.reject.load(Ordering::Acquire) {
            return Ok(false);
        }
        self.running
            .lock()
            .unwrap()
            .retain(|p| p.pid != process.pid);
        Ok(true)
    }
    fn reopen(&self, _: &Path) -> Result<(), AppError> {
        Ok(())
    }
}
struct Files {
    sandbox: PathBuf,
    bootouts: Mutex<Vec<String>>,
}
impl FileActions for Files {
    fn trash(&self, path: &Path, root: &Path) -> Result<PathBuf, AppError> {
        assert!(path.starts_with(&self.sandbox));
        assert!(root.starts_with(&self.sandbox));
        let destination = root.join(format!(
            "test-{}-{}",
            uuid::Uuid::now_v7(),
            path.file_name().unwrap().to_string_lossy()
        ));
        fs::rename(path, &destination)?;
        Ok(destination)
    }
    fn bootout(&self, label: &str, _: bool, path: &str) -> Result<(), AppError> {
        assert!(Path::new(path).starts_with(&self.sandbox));
        self.bootouts.lock().unwrap().push(label.into());
        Ok(())
    }
}
struct Admin {
    sandbox: PathBuf,
    root: PathBuf,
    calls: AtomicUsize,
    canceled: AtomicBool,
    labels: Mutex<Vec<String>>,
}
impl AdminActions for Admin {
    fn run(&self, requests: &[AdminRequest]) -> Result<Vec<AdminResult>, AppError> {
        self.calls.fetch_add(1, Ordering::AcqRel);
        if self.canceled.load(Ordering::Acquire) {
            return Err(AppError::new("E_SUDO", "admin_canceled"));
        }
        let mut results = vec![];
        for r in requests {
            let path = Path::new(&r.item.path);
            assert!(path.starts_with(&self.sandbox));
            let mut error = None;
            let mut destination = None;
            let mut kept = false;
            let trash_action = matches!(
                r.action,
                ItemAction::RestoreTrash { .. } | ItemAction::PurgeTrash
            );
            let unchanged = if trash_action {
                r.snapshot.matches(path)
            } else {
                r.snapshot.still(path)
            };
            if !matches!(r.action, ItemAction::Forget { .. }) && !unchanged {
                error = Some(SkippedItemReason::Changed);
            } else {
                match &r.action {
                    ItemAction::Path {
                        empty_only,
                        bootout,
                    } => {
                        if let Some((label, _)) = bootout {
                            self.labels.lock().unwrap().push(label.clone());
                        }
                        if *empty_only && fs::read_dir(path)?.next().is_some() {
                            kept = true;
                        } else if r.mode == DeleteMode::Trash {
                            let to = r.destination.as_ref().unwrap();
                            assert_eq!(to.parent(), Some(self.root.as_path()));
                            fs::rename(path, to)?;
                            destination = Some(to.clone());
                        } else {
                            remove(path, *empty_only)?;
                        }
                    }
                    ItemAction::Forget { package } => {
                        assert!(!package.is_empty());
                    }
                    ItemAction::RestoreTrash { original, reason } => {
                        assert_eq!(path.parent(), Some(self.root.as_path()));
                        assert!(original.starts_with(&self.sandbox));
                        if original
                            .parent()
                            .and_then(|p| p.file_name())
                            .is_some_and(|n| n.to_string_lossy().starts_with(".opennavo-updates"))
                        {
                            assert_eq!(*reason, MatchReason::NameExact);
                            assert!(opennavo_desktop_lib::updater::backup::is_directory(
                                r.app_path.as_deref().expect("backup owner"),
                                original
                            ));
                        }
                        fs::rename(path, original)?;
                    }
                    ItemAction::PurgeTrash => {
                        assert_eq!(path.parent(), Some(self.root.as_path()));
                        remove(path, false)?;
                    }
                    _ => panic!("unexpected test admin action"),
                }
            }
            results.push(AdminResult {
                item_id: r.item.id.clone(),
                destination,
                error,
                kept,
            });
        }
        Ok(results)
    }
}
struct Fixture {
    _temp: tempfile::TempDir,
    base: PathBuf,
    engine: Arc<Engine>,
    queue: Arc<TaskQueue>,
    probe: Arc<Probe>,
    apps: Arc<Apps>,
    files: Arc<Files>,
    admin: Arc<Admin>,
    events: Arc<Mutex<Vec<CoreEvent>>>,
}
impl Fixture {
    fn new() -> Self {
        Self::with_admin_apps(false)
    }
    fn with_admin_apps(admin_apps: bool) -> Self {
        let temp = tempfile::tempdir().unwrap();
        let base = temp.path().canonicalize().unwrap();
        let home = base.join("User");
        let roots = Roots {
            home: home.clone(),
            applications: vec![base.join("Applications"), home.join("Applications")],
            system_library: base.join(if admin_apps {
                "Applications"
            } else {
                "SystemLibrary"
            }),
            bins: vec![base.join("bin")],
            receipts: base.join("receipts"),
            temporary: vec![base.join("Temp")],
            caskrooms: vec![base.join("Caskroom")],
        };
        for path in roots
            .applications
            .iter()
            .chain(roots.bins.iter())
            .chain(roots.temporary.iter())
            .chain(roots.caskrooms.iter())
            .chain([&roots.receipts, &roots.system_library, &roots.home])
        {
            fs::create_dir_all(path).unwrap();
        }
        for (path, _) in roots.locations() {
            fs::create_dir_all(path).unwrap();
        }
        let db = Database::open(&base.join("data/db.sqlite")).unwrap();
        let settings = Arc::new(RwLock::new(Settings::default()));
        let events = Arc::new(Mutex::new(vec![]));
        let output = events.clone();
        let sink = Arc::new(move |e| output.lock().unwrap().push(e));
        let probe = Arc::new(Probe::default());
        let apps = Arc::new(Apps::default());
        let engine = Engine::new(EngineConfig {
            database: db.clone(),
            settings: settings.clone(),
            sink: sink.clone(),
            roots,
            control: apps.clone(),
            probe: probe.clone(),
            icon_cache: base.join("icons"),
            askpass: base.join("askpass"),
        });
        let files = Arc::new(Files {
            sandbox: base.clone(),
            bootouts: Mutex::new(vec![]),
        });
        engine.set_file_actions(files.clone()).unwrap();
        let admin = Arc::new(Admin {
            sandbox: base.clone(),
            root: home.join(".Trash"),
            calls: AtomicUsize::new(0),
            canceled: AtomicBool::new(false),
            labels: Mutex::new(vec![]),
        });
        engine.set_admin_actions(admin.clone()).unwrap();
        let mut queue =
            TaskQueue::new(db, settings, base.join("logs"), base.join("askpass"), sink).unwrap();
        let unique = Arc::get_mut(&mut queue).unwrap();
        unique.app_control = apps.clone();
        unique.uninstall_engine = engine.clone();
        Self {
            _temp: temp,
            base,
            engine,
            queue,
            probe,
            apps,
            files,
            admin,
            events,
        }
    }
    fn app(&self, name: &str, id: &str) -> PathBuf {
        let path = self.engine.roots.applications[0].join(format!("{name}.app"));
        fs::create_dir_all(path.join("Contents")).unwrap();
        let dict = plist::Dictionary::from_iter([
            ("CFBundleIdentifier", plist::Value::String(id.into())),
            ("CFBundleName", plist::Value::String(name.into())),
            (
                "CFBundleShortVersionString",
                plist::Value::String("1.0".into()),
            ),
        ]);
        plist::Value::Dictionary(dict)
            .to_file_xml(path.join("Contents/Info.plist"))
            .unwrap();
        path
    }
    fn dir(&self, relative: &str) -> PathBuf {
        let p = self.engine.roots.home.join(relative);
        fs::create_dir_all(&p).unwrap();
        fs::write(p.join("data"), "synthetic content").unwrap();
        p
    }

    async fn plan(&self, path: &Path) -> UninstallPlan {
        self.engine
            .refresh(LocalAppsChangedReason::Refresh)
            .unwrap();
        self.engine
            .plan(
                uuid::Uuid::now_v7().to_string(),
                vec![stable_id(path)],
                permissions(),
            )
            .await
            .unwrap()
    }
    async fn execute(
        &self,
        plan: &UninstallPlan,
        mode: DeleteMode,
        quit: bool,
        selected: Option<Vec<String>>,
    ) -> Task {
        let app = &plan.apps[0];
        let items = selected.unwrap_or_else(|| {
            app.items
                .iter()
                .filter(|i| i.selected)
                .map(|i| i.id.clone())
                .collect()
        });
        let tasks = self
            .engine
            .execute_choices(
                &self.queue,
                &plan.plan_id,
                vec![UninstallChoice {
                    app_id: app.app_id.clone(),
                    item_ids: items,
                    quit_running: quit,
                }],
                mode,
            )
            .unwrap();
        self.queue.start();
        terminal(&self.queue, &tasks[0].id).await
    }
}
fn permissions() -> Permissions {
    Permissions {
        app_management: PermissionState::Granted,
        app_management_switch: PermissionState::Granted,
        full_disk_access: PermissionState::Granted,
        relaunch_required: false,
        app_management_pending: false,
        full_disk_access_pending: false,
    }
}

#[tokio::test]
async fn uninstall_includes_both_recovery_roots_even_without_optional_data_selection() {
    use opennavo_desktop_lib::updater::backup;
    for admin in [false, true] {
        for mode in [DeleteMode::Trash, DeleteMode::Delete] {
            let f = Fixture::with_admin_apps(admin);
            let app = f.app("Recoverable", "test.recoverable");
            let other = f.app("Other", "test.other");
            let paths = backup::directories(&app).unwrap();
            let sibling = backup::directories(&other).unwrap()[0].clone();
            for p in paths.iter().chain([&sibling]) {
                fs::create_dir_all(p.join("previous.app/Contents")).unwrap();
                fs::write(p.join("previous.app/Contents/keep"), b"backup").unwrap();
            }
            let plan = f.plan(&app).await;
            for p in &paths {
                let item = plan.apps[0]
                    .items
                    .iter()
                    .find(|i| Path::new(&i.path) == p)
                    .unwrap();
                assert!(item.required && item.selected);
                assert_eq!(item.category, ItemCategory::AppData);
                assert_eq!(item.needs_admin, admin);
                assert!(f.engine.roots.allowed(p, item.reason, Some(&app)));
                assert!(!f.engine.roots.allowed(p, item.reason, Some(&other)));
                assert!(
                    !f.engine
                        .roots
                        .allowed(p.parent().unwrap(), item.reason, Some(&app))
                );
            }
            let task = f.execute(&plan, mode, false, Some(vec![])).await;
            assert_eq!(task.state, TaskState::Succeeded, "{:?}", task.error);
            assert!(!app.exists());
            assert!(paths.iter().all(|p| !p.exists()));
            assert!(sibling.join("previous.app/Contents/keep").exists());
            assert!(other.exists());
            if mode == DeleteMode::Trash {
                assert_eq!(task.outcome.unwrap().trashed, 4);
                assert_eq!(f.engine.restore(&task.id).unwrap().restored, 4);
                assert!(app.exists());
                assert!(
                    paths
                        .iter()
                        .all(|p| p.join("previous.app/Contents/keep").exists())
                );
            }
        }
    }
}

#[tokio::test]
async fn uninstall_never_follows_recovery_symlinks_or_deletes_a_replaced_recovery_root() {
    use opennavo_desktop_lib::updater::backup;
    let f = Fixture::new();
    let app = f.app("Recoverable", "test.recoverable");
    let path = backup::directories(&app).unwrap()[0].clone();
    fs::create_dir_all(path.join("previous.app")).unwrap();
    let plan = f.plan(&app).await;
    let original = path.with_file_name("old-recovery");
    fs::rename(&path, &original).unwrap();
    let sentinel = f.dir("Library/Application Support/keep");
    symlink(&sentinel, &path).unwrap();
    let task = f
        .execute(&plan, DeleteMode::Delete, false, Some(vec![]))
        .await;
    assert!(!app.exists());
    assert!(sentinel.join("data").exists());
    assert!(original.join("previous.app").exists());
    assert!(
        task.outcome
            .unwrap()
            .skipped
            .iter()
            .any(|item| Path::new(&item.path) == path && item.reason == SkippedItemReason::Changed)
    );
}
async fn terminal(queue: &TaskQueue, id: &str) -> Task {
    tokio::time::timeout(Duration::from_secs(8), async {
        loop {
            let t = queue
                .database
                .tasks()
                .unwrap()
                .into_iter()
                .find(|t| t.id == id)
                .unwrap();
            if !matches!(t.state, TaskState::Queued | TaskState::Running) {
                return t;
            }
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .unwrap()
}

#[test]
fn normalization_generic_words_and_name_confidence() {
    assert_eq!(normalize("Visual Studio-Code.app"), "visualstudiocodeapp");
    assert_eq!(normalize("网易 云_音乐"), "网易云音乐");
    for name in [
        "app", "helper", "update", "mac", "pro", "studio", "common", "cache", "data",
    ] {
        assert!(!meaningful(name));
    }
    assert_eq!(
        name_match("sample-editor", "Sample Editor"),
        Some((MatchReason::NameExact, PlanItemConfidence::Medium))
    );
    assert_eq!(
        name_match("Sample Editor Backup", "Sample Editor"),
        Some((MatchReason::NameSimilar, PlanItemConfidence::Low))
    );
    assert!(name_match("Studio", "Studio").is_none());
}
#[test]
fn zap_expansion_and_never_touch_policy_do_not_follow_links() {
    let f = Fixture::new();
    let app = f.app("Editor", "com.example.editor");
    let group = f.dir("Library/Group Containers/TESTTEAM.com.example.editor");
    let linked = f.base.join("outside");
    fs::create_dir_all(&linked).unwrap();
    fs::write(linked.join("com.example.editor"), "keep").unwrap();
    symlink(&linked, f.engine.roots.home.join("Library/Caches/link")).unwrap();
    let pattern = "~/Library/Group Containers/$(TeamIdentifierPrefix)com.example.*";
    assert_eq!(
        expand(pattern, &f.engine.roots.home, Some("TESTTEAM")).unwrap(),
        vec![group]
    );
    assert!(
        expand(pattern, &f.engine.roots.home, None)
            .unwrap()
            .is_empty()
    );
    assert!(
        expand(
            "~/Library/Caches/*/com.example.editor",
            &f.engine.roots.home,
            None
        )
        .unwrap()
        .is_empty()
    );
    assert!(
        expand("~/Library/../Documents/*", &f.engine.roots.home, None)
            .unwrap()
            .is_empty()
    );
    assert!(
        expand("$(rm -rf /)", &f.engine.roots.home, None)
            .unwrap()
            .is_empty()
    );
    for relative in [
        "Documents/project",
        "Desktop/work",
        "Downloads/file",
        "Pictures/photos",
        "Movies/library",
        "Library/Mobile Documents/file",
        "Library/CloudStorage/file",
        "Library/Keychains/file",
        ".ssh/key",
        ".Trash/unrelated",
        "Library/Application Support",
    ] {
        assert!(
            !f.engine.roots.allowed(
                &f.engine.roots.home.join(relative),
                MatchReason::ZapRule,
                Some(&app)
            ),
            "{relative}"
        );
    }
    for path in [
        "/System/Applications/App.app",
        "/bin/file",
        "/usr/bin/file",
        "/private/var/db/file",
        "/Volumes/External/App.app",
    ] {
        assert!(
            !f.engine
                .roots
                .allowed(Path::new(path), MatchReason::ZapRule, Some(&app))
        );
    }
    assert!(
        !f.engine.roots.allowed(
            &f.engine
                .roots
                .home
                .join("Library/Caches/link/com.example.editor"),
            MatchReason::BundleId,
            Some(&app)
        )
    );
}
#[tokio::test]
async fn site_alias_uninstalls_the_upstream_cask_with_an_official_name_collision() {
    use opennavo_desktop_lib::{brew::json::Info, catalog, library};
    for has_bundle in [true, false] {
        let f = Fixture::new();
        let fixtures =
            Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../apps/server/testdata/desktop");
        let mut snapshot: catalog::Snapshot = serde_json::from_reader(
            flate2::read::GzDecoder::new(fs::File::open(fixtures.join("catalog.json.gz")).unwrap()),
        )
        .unwrap();
        let mut official = snapshot
            .items
            .iter()
            .find(|i| i.kind == Kind::Cask)
            .unwrap()
            .clone();
        official.token = "orca".into();
        official.name = "Plotly Orca".into();
        official.tap = None;
        official.brew_token = None;
        official.apps = vec!["Plotly.app".into()];
        let mut alias = official.clone();
        alias.token = "stablyai-orca".into();
        alias.name = "Stably Orca".into();
        alias.tap = Some("stablyai/orca".into());
        alias.brew_token = Some("orca".into());
        alias.apps = vec!["Orca.app".into()];
        snapshot.items = vec![official, alias];
        let mut meta: catalog::SnapshotInfo =
            serde_json::from_slice(&fs::read(fixtures.join("latest.json")).unwrap()).unwrap();
        meta.item_count = 2;
        catalog::import_snapshot(&f.engine.database, &snapshot, &meta, 1).unwrap();

        let plotly = f.app("Plotly", "com.example.plotly");
        let app = has_bundle.then(|| f.app("Orca", "com.example.stably"));
        let caskroom = f.engine.roots.caskrooms[0].join("orca");
        fs::create_dir_all(caskroom.join("1.0")).unwrap();
        let raw = serde_json::json!({"casks": [{
            "token": "orca", "tap": "stablyai/orca", "name": ["Stably Orca"],
            "version": "1.0", "installed": "1.0", "artifacts": app.as_ref().map(|p| vec![serde_json::json!({"app": ["Orca.app"], "target": p})]).unwrap_or_default()
        }]});
        let info: Info = serde_json::from_value(raw.clone()).unwrap();
        let installed = library::scan(
            &f.engine.database,
            &info,
            f.base.to_str().unwrap(),
            Locale::EnUs,
        )
        .unwrap()
        .items;
        assert_eq!(installed[0].token, "stablyai-orca");
        f.engine
            .database
            .set_meta(
                "installed_cache",
                &serde_json::to_string(&installed).unwrap(),
            )
            .unwrap();
        f.engine
            .database
            .set_meta("cask_rules_cache", &raw.to_string())
            .unwrap();
        f.engine
            .seed_rule("stablyai/orca/orca", raw["casks"][0].clone())
            .unwrap();

        let script = f.base.join("brew-simulator");
        let recorded = f.base.join("brew-args.json");
        fs::write(&script, format!(
            "#!/usr/bin/python3 -I\nimport sys,json,shutil\na=sys.argv[1:]\nif a==['--version']: print('Homebrew 7.0.7')\nelif a in (['--prefix'],['--cache']): print({prefix:?})\nelif a==['uninstall','--cask','stablyai/orca/orca']:\n open({recorded:?},'w').write(json.dumps(a))\n shutil.rmtree({caskroom:?})\nelse: sys.exit(2)\n",
            prefix = f.base.to_string_lossy(), recorded = recorded.to_string_lossy(), caskroom = caskroom.to_string_lossy(),
        )).unwrap();
        fs::set_permissions(&script, fs::Permissions::from_mode(0o700)).unwrap();
        f.engine.settings.write().unwrap().brew_path = Some(script.to_string_lossy().into_owned());

        let apps = f.engine.refresh(LocalAppsChangedReason::Refresh).unwrap();
        let owned: Vec<_> = apps
            .iter()
            .filter(|a| a.source == AppSource::Homebrew)
            .collect();
        assert_eq!(
            owned.len(),
            1,
            "the site alias must not create a second Caskroom record"
        );
        assert_eq!(owned[0].cask.as_deref(), Some("stablyai/orca/orca"));
        assert_eq!(owned[0].catalog_token.as_deref(), Some("stablyai-orca"));
        let path = app.as_ref().unwrap_or(&caskroom);
        assert_eq!(owned[0].path, path.to_string_lossy());
        let plan = f.plan(path).await;
        assert_eq!(
            plan.apps[0].brew_command.as_deref(),
            Some("brew uninstall --cask stablyai/orca/orca")
        );
        let task = f.execute(&plan, DeleteMode::Trash, false, None).await;
        assert_eq!(task.target.as_ref().unwrap().token, "stablyai-orca");
        assert_eq!(task.state, TaskState::Succeeded, "{:?}", task.error);
        let args: Vec<String> = serde_json::from_slice(&fs::read(&recorded).unwrap()).unwrap();
        assert_eq!(args, ["uninstall", "--cask", "stablyai/orca/orca"]);
        assert!(!caskroom.exists());
        assert!(plotly.exists());
    }
}

#[tokio::test]
async fn all_sources_cask_without_bundle_and_protected_apps() {
    let f = Fixture::new();
    let direct = f.app("Direct", "com.example.direct");
    let store = f.app("Store", "com.example.store");
    fs::create_dir_all(store.join("Contents/_MASReceipt")).unwrap();
    fs::write(store.join("Contents/_MASReceipt/receipt"), "receipt").unwrap();
    let pkg = f.app("Pkg", "com.example.pkg");
    f.probe.receipts.lock().unwrap().insert(
        pkg.clone(),
        vec![Receipt {
            id: "com.example.pkg".into(),
            files: vec![pkg.join("Contents/Info.plist")],
        }],
    );
    f.app("OpenNavo", "com.opennavo.desktop");
    f.app("Safari", "com.apple.Safari");
    let nested = f.engine.roots.applications[0].join("Tools");
    fs::create_dir(&nested).unwrap();
    let nested_app = f.app("Nested", "com.example.nested");
    fs::rename(&nested_app, nested.join("Nested.app")).unwrap();
    let cask = f.engine.roots.caskrooms[0].join("font-sample/1.0");
    fs::create_dir_all(&cask).unwrap();
    f.engine
        .seed_rule("font-sample", serde_json::json!({"artifacts":[]}))
        .unwrap();
    let apps = f.engine.refresh(LocalAppsChangedReason::Refresh).unwrap();
    assert_eq!(
        apps.iter()
            .find(|a| a.path == direct.to_string_lossy())
            .unwrap()
            .source,
        AppSource::Direct
    );
    assert_eq!(
        apps.iter()
            .find(|a| a.path == store.to_string_lossy())
            .unwrap()
            .source,
        AppSource::AppStore
    );
    assert_eq!(
        apps.iter()
            .find(|a| a.path == pkg.to_string_lossy())
            .unwrap()
            .source,
        AppSource::Pkg
    );
    assert!(!apps.iter().any(|a| a.name == "Safari"));
    assert!(apps.iter().any(|a| a.name == "Nested"));
    let myself = apps.iter().find(|a| a.name == "OpenNavo").unwrap();
    assert!(!myself.removable);
    assert_eq!(myself.protected_reason, Some(ProtectedReason::SelfApp));
    let cask = apps
        .iter()
        .find(|a| a.cask.as_deref() == Some("font-sample"))
        .unwrap();
    assert_eq!(cask.bundle_id, None);
    assert_eq!(
        cask.path,
        f.engine.roots.caskrooms[0]
            .join("font-sample")
            .to_string_lossy()
    );
    let plan = f
        .engine
        .plan("cask-plan".into(), vec![cask.id.clone()], permissions())
        .await
        .unwrap();
    assert_eq!(plan.apps[0].removal, AppPlanRemoval::Brew);
    assert!(plan.apps[0].items[0].required);
    let blocked = f
        .engine
        .plan("self-plan".into(), vec![myself.id.clone()], permissions())
        .await
        .unwrap();
    assert_eq!(blocked.apps[0].removal, AppPlanRemoval::Blocked);
    let e = f
        .engine
        .execute_choices(
            &f.queue,
            "self-plan",
            vec![UninstallChoice {
                app_id: myself.id.clone(),
                item_ids: vec![],
                quit_running: true,
            }],
            DeleteMode::Trash,
        )
        .unwrap_err();
    assert_eq!(e.code, "E_PROTECTED");
}
#[tokio::test]
async fn exact_component_group_team_launchd_receipt_name_and_shared_evidence() {
    let f = Fixture::new();
    let app = f.app("Sample Editor", "com.example.editor");
    let other = f.app("Other Editor", "com.example.other");
    let mut identity = bundle(&app);
    identity.team_id = Some("TESTTEAM".into());
    identity.vendor = Some("Example Vendor".into());
    identity.groups.insert("TESTTEAM.shared".into());
    identity
        .component_ids
        .insert("com.example.editor.helper".into());
    f.probe
        .identities
        .lock()
        .unwrap()
        .insert(app.clone(), identity.clone());
    let mut other_identity = bundle(&other);
    other_identity.team_id = Some("TESTTEAM".into());
    other_identity.groups.insert("TESTTEAM.shared".into());
    f.probe
        .identities
        .lock()
        .unwrap()
        .insert(other.clone(), other_identity);
    f.dir("Library/Group Containers/TESTTEAM.shared");
    f.dir("Library/Containers/com.example.editor.helper");
    f.dir("Library/Application Support/Sample Editor");
    f.dir("Library/Application Support/Sample Editor Backup");
    let vendor = f.dir("Library/Application Support/Example Vendor");
    f.probe.identities.lock().unwrap().insert(vendor, identity);
    let launch = f
        .engine
        .roots
        .home
        .join("Library/LaunchAgents/com.example.editor.service.plist");
    write_service(
        &launch,
        "com.example.editor.service",
        &app.join("Contents/Resources/helper"),
    );
    let receipt = f.engine.roots.receipts.join("com.example.editor.pkg.plist");
    fs::write(&receipt, "synthetic").unwrap();
    fs::write(
        f.engine.roots.receipts.join("com.example.editor.pkg.bom"),
        "synthetic",
    )
    .unwrap();
    f.probe.receipts.lock().unwrap().insert(
        app.clone(),
        vec![Receipt {
            id: "com.example.editor.pkg".into(),
            files: vec![app.join("Contents/Info.plist")],
        }],
    );
    let plan = f.plan(&app).await;
    let items = &plan.apps[0].items;
    for reason in [
        MatchReason::AppBundle,
        MatchReason::BundleId,
        MatchReason::AppGroup,
        MatchReason::NameExact,
        MatchReason::NameSimilar,
        MatchReason::TeamId,
        MatchReason::Launchd,
        MatchReason::PkgReceipt,
    ] {
        assert!(items.iter().any(|i| i.reason == reason), "{reason:?}");
    }
    let shared = items
        .iter()
        .find(|i| i.reason == MatchReason::AppGroup)
        .unwrap();
    assert_eq!(shared.shared_with, vec!["Other Editor"]);
    assert!(!shared.selected);
    assert!(
        items
            .iter()
            .find(|i| i.reason == MatchReason::TeamId)
            .unwrap()
            .shared_with
            .is_empty()
    );
    assert!(
        !items
            .iter()
            .find(|i| i.reason == MatchReason::NameSimilar)
            .unwrap()
            .selected
    );
    assert_eq!(
        items
            .iter()
            .find(|i| i.reason == MatchReason::Launchd)
            .unwrap()
            .note
            .as_deref(),
        Some("com.example.editor.service")
    );
}
fn write_service(path: &Path, label: &str, program: &Path) {
    let dict = plist::Dictionary::from_iter([
        ("Label", plist::Value::String(label.into())),
        (
            "Program",
            plist::Value::String(program.to_string_lossy().into_owned()),
        ),
    ]);
    plist::Value::Dictionary(dict).to_file_xml(path).unwrap();
}

#[tokio::test]
async fn running_unicode_trash_restore_and_purge_touch_only_manifest() {
    let f = Fixture::new();
    let app = f.app("网易云 音乐 🧪", "com.example.music");
    let data = f.dir("Library/Containers/com.example.music");
    f.apps.running.lock().unwrap().push(Process {
        pid: 123,
        path: app.clone(),
        bundle_id: "com.example.music".into(),
    });
    let plan = f.plan(&app).await;
    let expected_bytes: u64 = plan
        .apps
        .iter()
        .flat_map(|a| &a.items)
        .filter(|i| i.selected)
        .filter_map(|i| i.size_bytes)
        .sum();
    let task = f.execute(&plan, DeleteMode::Trash, true, None).await;
    assert_eq!(task.state, TaskState::Succeeded);
    assert_eq!(task.target_name.as_deref(), Some("网易云 音乐 🧪"));
    let outcome = task.outcome.as_ref().unwrap();
    assert!(expected_bytes > 0);
    assert_eq!(outcome.freed_bytes, expected_bytes);
    assert_eq!(outcome.trashed_bytes, expected_bytes);
    assert_eq!(outcome.freed_bytes - outcome.trashed_bytes, 0);
    assert_eq!(f.apps.quits.load(Ordering::Acquire), 1);
    assert!(!app.exists());
    assert!(!data.exists());
    assert!(task.outcome.as_ref().unwrap().restorable);
    let trash = f.engine.trash_root().unwrap();
    fs::write(trash.join("unrelated"), "keep").unwrap();
    let restored = f.engine.restore(&task.id).unwrap();
    assert_eq!(restored.restored, 2);
    assert!(app.is_dir());
    assert!(data.is_dir());
    assert!(trash.join("unrelated").exists());
    let plan = f.plan(&app).await;
    let task = f.execute(&plan, DeleteMode::Trash, false, None).await;
    let bytes = f.engine.purge(&task.id).unwrap();
    assert!(bytes > 0);
    assert!(trash.join("unrelated").exists());
    assert!(!app.exists());
    assert_eq!(
        f.engine.restore(&task.id).unwrap_err().code,
        "E_RESTORE_UNAVAILABLE"
    );
}
#[tokio::test]
async fn missing_trash_conflicts_changed_paths_expiry_and_cancel_fail_safely() {
    let f = Fixture::new();
    let app = f.app("Editor", "com.example.editor");
    let data = f.dir("Library/Caches/com.example.editor");
    let keep = f.dir("Documents/project");
    let plan = f.plan(&app).await;
    fs::remove_dir_all(&data).unwrap();
    symlink(&keep, &data).unwrap();
    let task = f.execute(&plan, DeleteMode::Trash, false, None).await;
    assert_eq!(task.state, TaskState::Succeeded);
    assert!(keep.join("data").exists());
    assert!(fs::symlink_metadata(&data).unwrap().is_symlink());
    assert!(
        task.outcome
            .as_ref()
            .unwrap()
            .skipped
            .iter()
            .any(|s| s.path == data.to_string_lossy() && s.reason == SkippedItemReason::Changed)
    );
    for e in
        opennavo_desktop_lib::uninstall::manifest::entries(&f.queue.database, &task.id).unwrap()
    {
        if let Some(p) = e.trash {
            remove(&p, false).unwrap();
        }
    }
    assert_eq!(
        f.engine.restore(&task.id).unwrap_err().code,
        "E_RESTORE_UNAVAILABLE"
    );
    let app = f.app("Fresh", "com.example.fresh");
    let plan = f.plan(&app).await;
    assert_eq!(
        f.engine
            .cached_plan(&plan.plan_id, plan.expires_at)
            .unwrap_err()
            .code,
        "E_PLAN_EXPIRED"
    );
    assert!(
        f.engine
            .execute_choices(
                &f.queue,
                &plan.plan_id,
                vec![UninstallChoice {
                    app_id: stable_id(&app),
                    item_ids: vec![],
                    quit_running: false
                }],
                DeleteMode::Delete
            )
            .is_err()
    );
    assert!(app.exists());
    let f = Fixture::new();
    f.app("Slow", "com.example.slow");
    f.probe.delay.store(true, Ordering::Release);
    let engine = f.engine.clone();
    let path = f.engine.roots.applications[0].join("Slow.app");
    let job = tokio::spawn(async move {
        engine
            .plan("cancel-me".into(), vec![stable_id(&path)], permissions())
            .await
    });
    tokio::time::sleep(Duration::from_millis(20)).await;
    f.engine.cancel("cancel-me").unwrap();
    assert_eq!(job.await.unwrap().unwrap_err().code, "E_SCAN_CANCELED");
}
#[tokio::test]
async fn no_consent_or_refused_quit_preserves_app_and_data() {
    for consent in [false, true] {
        let f = Fixture::new();
        let app = f.app("Editor", "com.example.editor");
        let data = f.dir("Library/Caches/com.example.editor");
        f.apps.running.lock().unwrap().push(Process {
            pid: 234,
            path: app.clone(),
            bundle_id: "com.example.editor".into(),
        });
        f.apps.reject.store(true, Ordering::Release);
        let plan = f.plan(&app).await;
        let task = f.execute(&plan, DeleteMode::Delete, consent, None).await;
        assert_eq!(task.error.unwrap().code, "E_APP_RUNNING");
        assert!(app.is_dir());
        assert!(data.is_dir());
    }
}
#[tokio::test]
async fn one_admin_batch_cancellation_continues_user_cleanup_and_receipts_restore() {
    let f = Fixture::new();
    let app = f.app("Pkg Editor", "com.example.pkg");
    let data = f.dir("Library/Caches/com.example.pkg");
    let system = f
        .engine
        .roots
        .system_library
        .join("LaunchDaemons/com.example.pkg.service.plist");
    write_service(
        &system,
        "com.example.pkg.service",
        &app.join("Contents/helper"),
    );
    let receipt = f.engine.roots.receipts.join("com.example.pkg.plist");
    fs::write(&receipt, "receipt").unwrap();
    fs::write(f.engine.roots.receipts.join("com.example.pkg.bom"), "bom").unwrap();
    f.probe.receipts.lock().unwrap().insert(
        app.clone(),
        vec![Receipt {
            id: "com.example.pkg".into(),
            files: vec![app.join("Contents/Info.plist")],
        }],
    );
    f.admin.canceled.store(true, Ordering::Release);
    let plan = f.plan(&app).await;
    let task = f.execute(&plan, DeleteMode::Trash, false, None).await;
    assert_eq!(f.admin.calls.load(Ordering::Acquire), 1);
    assert!(!data.exists());
    assert!(system.exists());
    assert!(
        task.outcome
            .as_ref()
            .unwrap()
            .skipped
            .iter()
            .all(|s| s.reason == SkippedItemReason::AdminCanceled)
    );
    assert!(
        f.queue
            .log(&task.id, None)
            .unwrap()
            .contains("==> OpenNavo: requesting administrator access")
    );
    f.engine.restore(&task.id).unwrap();
    f.admin.canceled.store(false, Ordering::Release);
    let plan = f.plan(&app).await;
    let task = f.execute(&plan, DeleteMode::Trash, false, None).await;
    assert!(!receipt.exists());
    assert!(!system.exists());
    assert_eq!(
        f.admin.labels.lock().unwrap().as_slice(),
        &["com.example.pkg.service"]
    );
    let restored = f.engine.restore(&task.id).unwrap();
    assert!(restored.restored >= 5);
    assert!(receipt.exists());
    assert!(f.engine.roots.receipts.join("com.example.pkg.bom").exists());
    assert!(system.exists());
}
#[tokio::test]
async fn permissions_incomplete_apple_data_and_required_item_cannot_be_bypassed() {
    let f = Fixture::new();
    let app = f.app("Editor", "com.example.editor");
    let data = f.dir("Library/Containers/com.example.editor");
    f.dir("Library/Caches/com.apple.unrelated");
    f.engine.refresh(LocalAppsChangedReason::Refresh).unwrap();
    let mut p = permissions();
    p.full_disk_access = PermissionState::Denied;
    let plan = f
        .engine
        .plan("permissions".into(), vec![stable_id(&app)], p)
        .await
        .unwrap();
    assert!(!plan.incomplete.is_empty());
    assert!(
        !plan.apps[0]
            .items
            .iter()
            .any(|i| i.path.contains("com.apple.unrelated"))
    );
    let task = f
        .execute(&plan, DeleteMode::Trash, false, Some(vec![]))
        .await;
    assert!(!app.exists());
    assert!(data.exists());
    assert_eq!(task.outcome.unwrap().trashed, 1);
}
#[test]
fn complete_size_measurement_finishes_after_budget_timeout_and_honors_cancel() {
    use opennavo_desktop_lib::uninstall::rules::allocated_size_complete;
    let f = Fixture::new();
    let data = f.dir("Library/Caches/com.example.orphan");
    assert!(allocated_size(&data, Duration::ZERO).is_err());
    let cancel = AtomicBool::new(false);
    assert_eq!(
        allocated_size_complete(&data, &cancel).unwrap(),
        allocated_size(&data, Duration::from_secs(1)).unwrap()
    );
    cancel.store(true, Ordering::Release);
    assert_eq!(
        allocated_size_complete(&data, &cancel).unwrap_err().code,
        "E_SCAN_CANCELED"
    );
}

#[test]
fn snapshots_and_allocated_size_ignore_symlink_targets() {
    let f = Fixture::new();
    let data = f.dir("Library/Caches/Sample");
    let linked = f.dir("Documents/Keep");
    symlink(&linked, data.join("link")).unwrap();
    let snapshot = Snapshot::capture(&data).unwrap();
    assert!(snapshot.matches(&data));
    let bytes = allocated_size(&data, Duration::from_secs(1)).unwrap();
    assert!(bytes > 0);
    assert_eq!(
        allocated_size(&data.join("link"), Duration::from_secs(1)).unwrap(),
        0
    );
    fs::remove_dir_all(&data).unwrap();
    fs::create_dir(&data).unwrap();
    assert!(!snapshot.matches(&data));
    assert!(linked.join("data").exists());
    let snapshot = Snapshot::capture(&data).unwrap();
    let parent = data.parent().unwrap();
    let moved = f.base.join("old-caches");
    fs::rename(parent, &moved).unwrap();
    fs::create_dir(parent).unwrap();
    fs::rename(moved.join("Sample"), &data).unwrap();
    assert!(
        !snapshot.matches(&data),
        "the same item inode under a replaced parent must be rejected"
    );
}

#[tokio::test]
async fn historical_remove_leftovers_manifest_restores_and_purges_after_restart() {
    let f = Fixture::new();
    let app = f.app("Legacy", "org.example.legacy");
    let data = f.dir("Library/Caches/org.example.legacy");
    let plan = f.plan(&app).await;
    let task = f.execute(&plan, DeleteMode::Trash, false, None).await;
    assert!(task.outcome.as_ref().unwrap().restorable);
    // Model an already persisted legacy result; no removed API is needed to restore it.
    rusqlite::Connection::open(f.base.join("data/db.sqlite"))
        .unwrap()
        .execute(
            "UPDATE tasks SET op='remove_leftovers' WHERE id=?1",
            [&task.id],
        )
        .unwrap();
    let reopened = Database::open(&f.base.join("data/db.sqlite")).unwrap();
    assert_eq!(
        reopened.task(&task.id).unwrap().unwrap().op,
        TaskOp::RemoveLeftovers
    );
    let historical = Engine::new(EngineConfig {
        database: reopened,
        settings: f.engine.settings.clone(),
        sink: f.engine.sink.clone(),
        roots: f.engine.roots.clone(),
        control: f.apps.clone(),
        probe: f.probe.clone(),
        icon_cache: f.base.join("restarted-icons"),
        askpass: f.base.join("askpass"),
    });
    historical.set_file_actions(f.files.clone()).unwrap();
    historical.set_admin_actions(f.admin.clone()).unwrap();
    let sentinel = f.engine.trash_root().unwrap().join("unrelated");
    fs::write(&sentinel, "keep").unwrap();
    assert_eq!(historical.restore(&task.id).unwrap().restored, 2);
    assert!(app.exists());
    assert!(data.exists());
    let plan = f.plan(&app).await;
    let task = f.execute(&plan, DeleteMode::Trash, false, None).await;
    rusqlite::Connection::open(f.base.join("data/db.sqlite"))
        .unwrap()
        .execute(
            "UPDATE tasks SET op='remove_leftovers' WHERE id=?1",
            [&task.id],
        )
        .unwrap();
    assert!(historical.purge(&task.id).unwrap() > 0);
    assert!(sentinel.exists());
    assert!(!app.exists());
    assert!(!data.exists());
}
#[tokio::test]
async fn footprint_counts_owned_data_and_cache_once_and_flags_incomplete() {
    let f = Fixture::new();
    let app = f.app("Editor", "com.example.editor");
    let data = f.dir("Library/Containers/com.example.editor");
    let cache = f.dir("Library/Caches/com.example.editor");
    let footprint = f
        .engine
        .footprint(stable_id(&app), permissions())
        .await
        .unwrap();
    assert!(footprint.complete);
    assert_eq!(
        footprint.app_bytes,
        allocated_size(&app, Duration::from_secs(1)).unwrap()
    );
    assert_eq!(
        footprint.data_bytes,
        allocated_size(&data, Duration::from_secs(1)).unwrap()
    );
    assert_eq!(
        footprint.cache_bytes,
        allocated_size(&cache, Duration::from_secs(1)).unwrap()
    );
    let mut limited = permissions();
    limited.full_disk_access = PermissionState::Denied;
    assert!(
        !f.engine
            .footprint(stable_id(&app), limited)
            .await
            .unwrap()
            .complete
    );
}
#[tokio::test]
async fn symlink_only_restore_conflict_modified_trash_and_restart_are_safe() {
    let f = Fixture::new();
    let app = f.app("Editor", "com.example.editor");
    let binary = app.join("Contents/Resources/tool");
    fs::create_dir_all(binary.parent().unwrap()).unwrap();
    fs::write(&binary, "keep target").unwrap();
    let link = f.engine.roots.bins[0].join("editor");
    symlink(&binary, &link).unwrap();
    let plan = f.plan(&app).await;
    assert!(
        plan.apps[0]
            .items
            .iter()
            .any(|i| i.reason == MatchReason::Symlink && i.path == link.to_string_lossy())
    );
    let task = f.execute(&plan, DeleteMode::Trash, false, None).await;
    let entries =
        opennavo_desktop_lib::uninstall::manifest::entries(&f.queue.database, &task.id).unwrap();
    let bundle_trash = entries
        .iter()
        .find(|e| e.original == app)
        .unwrap()
        .trash
        .as_ref()
        .unwrap();
    assert!(bundle_trash.join("Contents/Resources/tool").exists());
    // Re-open persistent manifest through a fresh engine (empty plan/app caches).
    let db = Database::open(&f.base.join("data/db.sqlite")).unwrap();
    let fresh = Engine::new(EngineConfig {
        database: db,
        settings: f.engine.settings.clone(),
        sink: Arc::new(|_| {}),
        roots: f.engine.roots.clone(),
        control: f.apps.clone(),
        probe: f.probe.clone(),
        icon_cache: f.base.join("icons"),
        askpass: f.base.join("askpass"),
    });
    fresh.set_file_actions(f.files.clone()).unwrap();
    fresh.set_admin_actions(f.admin.clone()).unwrap();
    fs::create_dir(&app).unwrap();
    fs::write(app.join("replacement"), "keep new app").unwrap();
    let result = fresh.restore(&task.id).unwrap();
    assert_eq!(result.restored, 1);
    assert_eq!(result.missing, [app.to_string_lossy()]);
    assert!(app.join("replacement").exists());
    assert!(fs::symlink_metadata(&link).unwrap().is_symlink());
    // Replacing an item inside Trash also invalidates purge; unrelated replacement remains untouched.
    remove(bundle_trash, false).unwrap();
    fs::write(bundle_trash, "replacement in Trash").unwrap();
    assert_eq!(
        fresh.purge(&task.id).unwrap_err().code,
        "E_RESTORE_UNAVAILABLE"
    );
    assert_eq!(
        fs::read_to_string(bundle_trash).unwrap(),
        "replacement in Trash"
    );
}
#[tokio::test]
async fn unknown_ids_replays_and_changed_required_bundle_do_not_delete_data() {
    let f = Fixture::new();
    let app = f.app("Editor", "com.example.editor");
    let data = f.dir("Library/Caches/com.example.editor");
    let plan = f.plan(&app).await;
    assert!(
        f.engine
            .execute_choices(
                &f.queue,
                &plan.plan_id,
                vec![UninstallChoice {
                    app_id: stable_id(&app),
                    item_ids: vec![data.to_string_lossy().into_owned()],
                    quit_running: false
                }],
                DeleteMode::Delete
            )
            .is_err()
    );
    fs::rename(&app, f.base.join("replaced.app")).unwrap();
    f.app("Editor", "com.example.other");
    let task = f.execute(&plan, DeleteMode::Delete, false, None).await;
    assert_eq!(task.error.unwrap().code, "E_PLAN_EXPIRED");
    assert!(data.exists());
    assert!(app.exists());
    assert_eq!(
        f.engine
            .execute_choices(
                &f.queue,
                &plan.plan_id,
                vec![UninstallChoice {
                    app_id: stable_id(&app),
                    item_ids: vec![],
                    quit_running: false
                }],
                DeleteMode::Delete
            )
            .unwrap_err()
            .code,
        "E_PLAN_EXPIRED"
    );
}
#[tokio::test]
async fn live_uninstall_scan_request_namespace_and_cancel_are_shared() {
    let f = Fixture::new();
    let app = f.app("Slow", "com.example.slow");
    f.probe.delay.store(true, Ordering::Release);
    let engine = f.engine.clone();
    let scan_app = app.clone();
    let job = tokio::spawn(async move {
        engine
            .plan(
                "same-request".into(),
                vec![stable_id(&scan_app)],
                permissions(),
            )
            .await
    });
    tokio::time::sleep(Duration::from_millis(20)).await;
    assert_eq!(
        f.engine
            .plan("same-request".into(), vec![stable_id(&app)], permissions())
            .await
            .unwrap_err()
            .code,
        "E_INVALID_ARG"
    );
    f.engine.cancel("same-request").unwrap();
    assert_eq!(job.await.unwrap().unwrap_err().code, "E_SCAN_CANCELED");
}
#[tokio::test]
async fn brew_plan_invokes_only_simulator_without_zap_and_uses_cached_zap_paths() {
    for removed in [false, true] {
        let f = Fixture::new();
        let app = f.app("Docker", "com.docker.docker");
        let other = f.app("OrbStack", "dev.orbstack.OrbStack");
        let data = f.dir("Library/Caches/com.docker.docker");
        let shared = f.dir(".docker");
        let private = f.dir("Documents/project");
        let cask = f.engine.roots.caskrooms[0].join("docker-desktop/1.0");
        fs::create_dir_all(&cask).unwrap();
        let record = InstalledItem {
            kind: Kind::Cask,
            token: "docker-desktop".into(),
            full_token: None,
            name: "Docker".into(),
            installed_version: "1.0".into(),
            actual_version: None,
            latest_version: None,
            status: InstalledStatus::Unknown,
            on_request: true,
            required_by: vec![],
            installed_at: Some(1_700_000_000_000),
            size_bytes: None,
            app_paths: vec![app.to_string_lossy().into_owned()],
            icon_path: None,
            auto_updates: false,
            pinned: false,
        };
        f.queue
            .database
            .set_meta(
                "installed_cache",
                &serde_json::to_string(&vec![record]).unwrap(),
            )
            .unwrap();
        f.engine.seed_rule("docker-desktop", serde_json::json!({"artifacts":[{"uninstall":[{"quit":"com.docker.docker","launchctl":"com.docker.vmnetd"}]},{"zap":[{"trash":["~/Library/Caches/com.docker.docker","~/.docker","~/Documents/project"],"script":{"executable":"/bin/sh","args":["-c","touch forbidden"]},"signal":["KILL","com.docker.docker"]}]}]})).unwrap();
        let script = f.base.join("brew-simulator");
        let recorded = f.base.join("brew-args.json");
        let code = format!(
            "#!/usr/bin/python3 -I\nimport sys,json,shutil\na=sys.argv[1:]\nif a==['--version']: print('Homebrew 7.0.7')\nelif a==['--prefix']: print({prefix:?})\nelif a==['uninstall','--cask','docker-desktop']:\n open({recorded:?},'w').write(json.dumps(a))\n{removal}\n print('simulated brew authorization output')\nelse: sys.exit(2)\n",
            prefix = f.base.to_string_lossy(),
            recorded = recorded.to_string_lossy(),
            removal = if removed {
                // Like Homebrew, skip an app that is already gone (OpenNavo trashed it first).
                format!(
                    " shutil.rmtree({:?}, ignore_errors=True)\n shutil.rmtree({:?})",
                    app.to_string_lossy(),
                    cask.parent().unwrap().to_string_lossy()
                )
            } else {
                String::new()
            }
        );
        fs::write(&script, code).unwrap();
        fs::set_permissions(&script, fs::Permissions::from_mode(0o700)).unwrap();
        f.engine.settings.write().unwrap().brew_path = Some(script.to_string_lossy().into_owned());
        let plan = f.plan(&app).await;
        assert!(
            plan.apps[0]
                .items
                .iter()
                .any(|i| i.reason == MatchReason::BrewUninstall && i.required)
        );
        let shared_item = plan.apps[0]
            .items
            .iter()
            .find(|i| i.path == shared.to_string_lossy())
            .unwrap();
        assert_eq!(shared_item.shared_with, ["OrbStack"]);
        assert!(!shared_item.selected);
        assert!(
            plan.apps[0]
                .items
                .iter()
                .all(|i| i.path != private.to_string_lossy())
        );
        let task = f.execute(&plan, DeleteMode::Trash, false, None).await;
        if !removed {
            // Homebrew kept its record: not uninstalled. The bundle already went to the Trash and comes back.
            assert_eq!(task.state, TaskState::Failed);
            assert_eq!(task.error.as_ref().unwrap().code, "E_UNINSTALL_INCOMPLETE");
            assert!(!app.exists());
            assert!(task.outcome.as_ref().unwrap().restorable);
            assert!(
                data.exists(),
                "failed bundle removal must preserve application data"
            );
            assert!(
                f.engine
                    .refresh(LocalAppsChangedReason::Task)
                    .unwrap()
                    .iter()
                    .any(|a| a.cask.as_deref() == Some("docker-desktop"))
            );
            assert_eq!(f.engine.restore(&task.id).unwrap().restored, 1);
            assert!(app.exists());
            continue;
        }
        assert_eq!(task.state, TaskState::Succeeded);
        assert!(!app.exists());
        assert!(!data.exists());
        // The bundle went to the Trash first (Homebrew would have deleted it for good) and Undo restores it.
        let outcome = task.outcome.clone().unwrap();
        assert!(outcome.restorable);
        assert!(outcome.trashed >= 2);
        // Even a stale brew cache must not resurrect the successfully removed package.
        fs::create_dir_all(cask.parent().unwrap().join(".metadata")).unwrap();
        assert!(
            !f.engine
                .refresh(LocalAppsChangedReason::Task)
                .unwrap()
                .iter()
                .any(|a| a.cask.as_deref() == Some("docker-desktop"))
        );
        assert!(shared.exists());
        assert!(private.exists());
        assert!(other.exists());
        assert_eq!(
            serde_json::from_str::<Vec<String>>(&fs::read_to_string(recorded).unwrap()).unwrap(),
            ["uninstall", "--cask", "docker-desktop"]
        );
        let log = fs::read_to_string(task.log_path.unwrap()).unwrap();
        assert!(log.contains("==> Homebrew:"));
        assert!(log.contains("simulated brew authorization output"));
        assert!(!f.base.join("forbidden").exists());
    }
}

#[test]
fn real_homebrew_fixture_and_server_cleanup_declarations_are_supported() {
    let path =
        Path::new(env!("CARGO_MANIFEST_DIR")).join("../../server/testdata/homebrew/wechat.json");
    let value: serde_json::Value = serde_json::from_slice(&fs::read(path).unwrap()).unwrap();
    let raw = value.get("casks").and_then(|v| v.get(0)).unwrap_or(&value);
    assert!(!opennavo_desktop_lib::uninstall::scan::declarations(raw, "zap").is_empty());
    let server = serde_json::json!({"artifacts":{"entries":[{"phase":"cleanup","declaration":{"zap":[{"trash":"~/Library/Caches/com.example.editor"}]}},{"phase":"uninstall","declaration":{"uninstall":[{"quit":"com.example.editor"}]}}]}});
    assert!(opennavo_desktop_lib::uninstall::scan::rule_bundle_matches(
        &server,
        Some("com.example.editor")
    ));
    assert_eq!(
        opennavo_desktop_lib::uninstall::scan::declarations(&server, "zap").len(),
        1
    );
}

#[tokio::test]
async fn catalog_filename_requires_bundle_id_and_uses_offline_rules() {
    let f = Fixture::new();
    let app = f.app("WeChat", "com.tencent.xinWeChat");
    let mut identity = bundle(&app);
    identity.team_id = Some("TEST123456".into());
    identity
        .groups
        .insert("TEST123456.com.tencent.xinWeChat".into());
    f.probe
        .identities
        .lock()
        .unwrap()
        .insert(app.clone(), identity);
    let container = f.dir("Library/Containers/com.tencent.xinWeChat");
    let group = f.dir("Library/Group Containers/TEST123456.com.tencent.xinWeChat");
    let scripts = f.dir("Library/Application Scripts/TEST123456.com.tencent.xinWeChat");
    let item = serde_json::json!({"kind":"cask","token":"wechat","name":"WeChat","names":[],"version":"1","homepage":null,"autoUpdates":false,"deprecated":false,"disabled":false,"hidden":false,"isFont":false,"isLibrary":false,"categories":[],"tags":[],"installs30d":0,"rank30d":null,"popularity":0,"iconUrl":null,"accentColor":null,"apps":["WeChat.app"],"binaries":[],"pinyin":null,"versionChangedAt":"2026-10-10","downloadSize":null,"onRequest":null});
    f.queue.database.with(|c| { c.execute("INSERT INTO catalog_items(kind,token,data,name,version,version_changed_at) VALUES('cask','wechat',?1,'WeChat','1',0)", [item.to_string()])?; Ok(()) }).unwrap();
    let raw: serde_json::Value = serde_json::from_slice(
        &fs::read(
            Path::new(env!("CARGO_MANIFEST_DIR"))
                .join("../../server/testdata/homebrew/wechat.json"),
        )
        .unwrap(),
    )
    .unwrap();
    f.engine.seed_rule("wechat", raw).unwrap();
    let plan = f.plan(&app).await;
    assert_eq!(plan.apps[0].personal_data.as_deref(), Some("chat_history"));
    for path in [container, group, scripts] {
        assert!(
            plan.apps[0]
                .items
                .iter()
                .any(|i| i.path == path.to_string_lossy() && i.selected)
        );
    }
    assert_eq!(
        f.engine
            .app(&stable_id(&app))
            .unwrap()
            .app
            .catalog_token
            .as_deref(),
        Some("wechat")
    );
    assert_eq!(plan.apps[0].removal, AppPlanRemoval::Trash);
    let nested = f.engine.roots.applications[1].join("WeChat.app");
    fs::create_dir_all(nested.parent().unwrap()).unwrap();
    fs::rename(&app, &nested).unwrap();
    f.probe.identities.lock().unwrap().remove(&app);
    let wrong = f.app("WeChat", "com.example.impersonator");
    let apps = f.engine.refresh(LocalAppsChangedReason::Refresh).unwrap();
    assert_eq!(
        apps.iter()
            .find(|a| a.path == wrong.to_string_lossy())
            .unwrap()
            .catalog_token,
        None
    );
}
#[tokio::test]
async fn shared_pkg_receipt_and_personal_libraries_are_never_selected_by_default() {
    let f = Fixture::new();
    let word = f.app("Word", "com.microsoft.Word");
    let excel = f.app("Excel", "com.microsoft.Excel");
    let support = f.dir("Library/Application Support/Microsoft AutoUpdate");
    let media = f.dir("Movies/Library");
    let caches = f.dir("Library/Caches/com.microsoft.Word");
    let receipt = f.engine.roots.receipts.join("com.microsoft.Office.plist");
    fs::write(&receipt, "synthetic receipt").unwrap();
    for app in [&word, &excel] {
        f.probe.receipts.lock().unwrap().insert(
            app.to_path_buf(),
            vec![Receipt {
                id: "com.microsoft.Office".into(),
                files: vec![
                    word.join("Contents/Info.plist"),
                    excel.join("Contents/Info.plist"),
                    support.clone(),
                ],
            }],
        );
    }
    let plan = f.plan(&word).await;
    assert!(
        plan.apps[0]
            .items
            .iter()
            .filter(|i| i.path == support.to_string_lossy() || i.path == receipt.to_string_lossy())
            .all(|i| !i.selected && i.shared_with == ["Excel"])
    );
    assert_eq!(plan.apps[0].personal_data.as_deref(), Some("documents"));
    assert!(
        plan.apps[0]
            .items
            .iter()
            .all(|i| i.path != media.to_string_lossy())
    );
    let task = f.execute(&plan, DeleteMode::Trash, false, None).await;
    assert_eq!(task.state, TaskState::Succeeded);
    assert!(excel.exists());
    assert!(media.exists());
    assert!(receipt.exists());
    assert!(support.exists());
    assert!(!caches.exists());
}
#[tokio::test]
async fn thirty_gib_sparse_fixture_reports_progress_and_purge_is_scoped() {
    let f = Fixture::new();
    let app = f.app("Docker", "com.docker.docker");
    let data = f.dir("Library/Containers/com.docker.docker");
    let sparse = fs::File::create(data.join("disk.raw")).unwrap();
    sparse.set_len(31 * 1024 * 1024 * 1024).unwrap();
    let plan = f.plan(&app).await;
    assert_eq!(plan.apps[0].personal_data.as_deref(), Some("vm_disk"));
    let task = f.execute(&plan, DeleteMode::Trash, false, None).await;
    let unrelated = f.engine.trash_root().unwrap().join("unrelated");
    fs::write(&unrelated, "keep").unwrap();
    assert!(f.events.lock().unwrap().iter().any(|e| matches!(e, CoreEvent::Task(t) if t.id == task.id && t.phase == Some(TaskPhase::Cleaning))));
    assert!(f.engine.purge(&task.id).unwrap() > 0);
    assert!(unrelated.exists());
}

#[tokio::test]
async fn launchd_references_protect_shared_helpers_without_using_team_id() {
    let f = Fixture::new();
    let app = f.app("Editor", "com.example.editor");
    f.app("Other", "com.example.other");
    let helper = f
        .engine
        .roots
        .system_library
        .join("PrivilegedHelperTools/com.example.helper");
    fs::write(&helper, "synthetic helper").unwrap();
    write_service(
        &f.engine
            .roots
            .system_library
            .join("LaunchDaemons/com.example.editor.plist"),
        "com.example.editor",
        &helper,
    );
    write_service(
        &f.engine
            .roots
            .system_library
            .join("LaunchDaemons/com.example.other.plist"),
        "com.example.other",
        &helper,
    );
    let plan = f.plan(&app).await;
    let shared = plan.apps[0]
        .items
        .iter()
        .find(|i| i.path == helper.to_string_lossy())
        .unwrap();
    assert_eq!(shared.shared_with, ["Other"]);
    assert!(!shared.selected);
}
#[tokio::test]
async fn batch_tasks_keep_individual_app_names_and_cross_app_choices_are_rejected() {
    let f = Fixture::new();
    let first = f.app("First", "com.example.first");
    let second = f.app("Second", "com.example.second");
    f.dir("Library/Caches/com.example.first");
    f.dir("Library/Caches/com.example.second");
    f.engine.refresh(LocalAppsChangedReason::Refresh).unwrap();
    let plan = f
        .engine
        .plan(
            "batch".into(),
            vec![stable_id(&first), stable_id(&second)],
            permissions(),
        )
        .await
        .unwrap();
    let mut choices: Vec<_> = plan
        .apps
        .iter()
        .map(|a| UninstallChoice {
            app_id: a.app_id.clone(),
            item_ids: a
                .items
                .iter()
                .filter(|i| i.selected)
                .map(|i| i.id.clone())
                .collect(),
            quit_running: false,
        })
        .collect();
    let mut invalid = choices.clone();
    invalid[0].item_ids.push(plan.apps[1].items[0].id.clone());
    assert!(
        f.engine
            .execute_choices(&f.queue, &plan.plan_id, invalid, DeleteMode::Trash)
            .is_err()
    );
    assert!(f.queue.database.tasks().unwrap().is_empty());
    let tasks = f
        .engine
        .execute_choices(
            &f.queue,
            &plan.plan_id,
            std::mem::take(&mut choices),
            DeleteMode::Trash,
        )
        .unwrap();
    assert_eq!(tasks.len(), 2);
    assert_eq!(tasks[0].target_name.as_deref(), Some("First"));
    assert_eq!(tasks[1].target_name.as_deref(), Some("Second"));
    f.queue.start();
    for task in tasks {
        assert_eq!(
            terminal(&f.queue, &task.id).await.state,
            TaskState::Succeeded
        );
    }
    assert!(!first.exists());
    assert!(!second.exists());
}

#[tokio::test]
async fn cask_companions_are_required_sized_quit_and_revalidated() {
    for changed in [false, true] {
        let f = Fixture::new();
        let primary = f.app("Suite Editor", "com.example.suite.editor");
        let companion = f.app("Suite Viewer", "com.example.suite.viewer");
        fs::create_dir_all(f.engine.roots.caskrooms[0].join("suite/1.0")).unwrap();
        let installed = InstalledItem {
            full_token: None,
            kind: Kind::Cask,
            token: "suite".into(),
            name: "Suite".into(),
            installed_version: "1.0".into(),
            actual_version: None,
            latest_version: None,
            status: InstalledStatus::Unknown,
            on_request: true,
            required_by: vec![],
            installed_at: None,
            size_bytes: None,
            app_paths: vec![
                primary.to_string_lossy().into_owned(),
                companion.to_string_lossy().into_owned(),
            ],
            icon_path: None,
            auto_updates: false,
            pinned: false,
        };
        f.queue
            .database
            .set_meta(
                "installed_cache",
                &serde_json::to_string(&vec![installed]).unwrap(),
            )
            .unwrap();
        f.engine.seed_rule("suite", serde_json::json!({"artifacts":[{"uninstall":[{"quit":["com.example.suite.editor","com.example.suite.viewer"]}]}]})).unwrap();
        f.engine.settings.write().unwrap().brew_path = Some(
            Path::new(env!("CARGO_MANIFEST_DIR"))
                .join("tests/fake-brew/brew")
                .to_string_lossy()
                .into_owned(),
        );
        f.apps.running.lock().unwrap().push(Process {
            pid: 789,
            path: companion.clone(),
            bundle_id: "com.example.suite.viewer".into(),
        });
        let plan = f.plan(&primary).await;
        assert!(plan.apps[0].running);
        let item = plan.apps[0]
            .items
            .iter()
            .find(|i| i.path == companion.to_string_lossy())
            .unwrap();
        assert_eq!(item.category, ItemCategory::App);
        assert_eq!(item.reason, MatchReason::BrewUninstall);
        assert!(item.required);
        assert!(item.selected);
        assert_eq!(
            item.size_bytes,
            Some(allocated_size(&companion, Duration::from_secs(1)).unwrap())
        );
        if changed {
            fs::rename(&companion, f.base.join("changed.app")).unwrap();
            f.app("Suite Viewer", "com.example.changed");
        }
        // Omitting the companion's itemId cannot opt out of the brew package removal or its revalidation.
        let task = f
            .execute(&plan, DeleteMode::Trash, true, Some(vec![]))
            .await;
        assert_eq!(f.apps.quits.load(Ordering::Acquire), 1);
        if changed {
            assert_eq!(task.error.unwrap().code, "E_PLAN_EXPIRED");
            assert!(primary.exists());
        } else {
            // The default simulator exits successfully without removing its record. Both bundles were moved to
            // the Trash before brew ran, so the failed uninstall can be undone.
            assert_eq!(task.state, TaskState::Failed);
            assert_eq!(task.error.as_ref().unwrap().code, "E_UNINSTALL_INCOMPLETE");
            assert!(!primary.exists());
            assert!(!companion.exists());
            assert_eq!(f.engine.restore(&task.id).unwrap().restored, 2);
            assert!(primary.exists());
            assert!(companion.exists());
        }
    }
}
#[tokio::test]
async fn cask_with_protected_companion_is_blocked_and_duplicate_cask_stays_rejected() {
    for protected in [false, true] {
        let f = Fixture::new();
        let primary = f.app("Suite Editor", "com.example.suite.editor");
        let companion = f.app(
            "Suite Viewer",
            if protected {
                "com.opennavo.desktop"
            } else {
                "com.example.suite.viewer"
            },
        );
        fs::create_dir_all(f.engine.roots.caskrooms[0].join("suite/1.0")).unwrap();
        let installed = InstalledItem {
            full_token: None,
            kind: Kind::Cask,
            token: "suite".into(),
            name: "Suite".into(),
            installed_version: "1.0".into(),
            actual_version: None,
            latest_version: None,
            status: InstalledStatus::Unknown,
            on_request: true,
            required_by: vec![],
            installed_at: None,
            size_bytes: None,
            app_paths: vec![
                primary.to_string_lossy().into_owned(),
                companion.to_string_lossy().into_owned(),
            ],
            icon_path: None,
            auto_updates: false,
            pinned: false,
        };
        f.queue
            .database
            .set_meta(
                "installed_cache",
                &serde_json::to_string(&vec![installed]).unwrap(),
            )
            .unwrap();
        f.engine
            .seed_rule("suite", serde_json::json!({"artifacts":[]}))
            .unwrap();
        f.engine.refresh(LocalAppsChangedReason::Refresh).unwrap();
        let plan = f
            .engine
            .plan(
                "suite-plan".into(),
                vec![stable_id(&primary), stable_id(&companion)],
                permissions(),
            )
            .await
            .unwrap();
        let choices = plan
            .apps
            .iter()
            .map(|a| UninstallChoice {
                app_id: a.app_id.clone(),
                item_ids: a
                    .items
                    .iter()
                    .filter(|i| i.selected)
                    .map(|i| i.id.clone())
                    .collect(),
                quit_running: false,
            })
            .collect();
        let error = f
            .engine
            .execute_choices(&f.queue, &plan.plan_id, choices, DeleteMode::Trash)
            .unwrap_err();
        if protected {
            assert_eq!(
                plan.apps[0].blocked_reason,
                Some(AppPlanBlockedReason::SelfApp)
            );
            assert_eq!(error.code, "E_PROTECTED");
        } else {
            assert_eq!(error.message, "duplicate_cask");
        }
        assert!(f.queue.database.tasks().unwrap().is_empty());
        assert!(primary.exists());
        assert!(companion.exists());
    }
}

#[tokio::test]
async fn native_footprint_page_gate_persists_and_manual_scan_discards_values() {
    let f = Fixture::new();
    let app = f.app("First", "com.example.first");
    f.engine.refresh(LocalAppsChangedReason::Refresh).unwrap();
    assert!(f.engine.scan_footprints(permissions(), false).unwrap());
    tokio::time::timeout(Duration::from_secs(8), async {
        while !f
            .engine
            .footprint_values()
            .unwrap()
            .contains_key(&stable_id(&app))
        {
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .unwrap();
    let before = f.engine.footprint_values().unwrap();
    assert!(!f.engine.scan_footprints(permissions(), false).unwrap());
    f.engine.refresh(LocalAppsChangedReason::Fs).unwrap();
    assert_eq!(before, f.engine.footprint_values().unwrap());
    let fresh = Engine::new(EngineConfig {
        database: Database::open(&f.base.join("data/db.sqlite")).unwrap(),
        settings: f.engine.settings.clone(),
        sink: Arc::new(|_| {}),
        roots: f.engine.roots.clone(),
        control: f.apps.clone(),
        probe: f.probe.clone(),
        icon_cache: f.base.join("icons"),
        askpass: f.base.join("askpass"),
    });
    fresh.refresh(LocalAppsChangedReason::Startup).unwrap();
    assert_eq!(before, fresh.footprint_values().unwrap());
    assert!(!fresh.scan_footprints(permissions(), false).unwrap());
    f.queue
        .database
        .set_meta("footprints_scan_at", "0")
        .unwrap();
    let reloaded = Engine::new(EngineConfig {
        database: f.queue.database.clone(),
        settings: f.engine.settings.clone(),
        sink: Arc::new(|_| {}),
        roots: f.engine.roots.clone(),
        control: f.apps.clone(),
        probe: f.probe.clone(),
        icon_cache: f.base.join("icons"),
        askpass: f.base.join("askpass"),
    });
    reloaded.refresh(LocalAppsChangedReason::Startup).unwrap();
    assert!(reloaded.scan_footprints(permissions(), false).unwrap());
    assert!(f.engine.scan_footprints(permissions(), true).unwrap());
}

#[tokio::test]
async fn completed_app_task_only_rescans_its_target() {
    let f = Fixture::new();
    let first = f.app("First", "com.example.first");
    let second = f.app("Second", "com.example.second");
    f.engine
        .footprint(stable_id(&first), permissions())
        .await
        .unwrap();
    f.engine
        .footprint(stable_id(&second), permissions())
        .await
        .unwrap();
    let before: serde_json::Value =
        serde_json::from_str(&f.queue.database.meta("footprints_v1").unwrap().unwrap()).unwrap();
    let mut task = f
        .queue
        .enqueue(
            TaskOp::Upgrade,
            Some(TaskTarget {
                kind: TaskKind::Cask,
                token: "first".into(),
            }),
            Default::default(),
            TaskTrigger::Manual,
        )
        .unwrap();
    task.target = Some(TaskTarget {
        kind: TaskKind::App,
        token: stable_id(&first),
    });
    tokio::time::sleep(Duration::from_millis(20)).await;
    f.engine
        .task_footprints(&task, permissions())
        .await
        .unwrap();
    // The old value stays visible until the new one replaces it.
    assert!(
        f.engine
            .footprint_values()
            .unwrap()
            .contains_key(&stable_id(&first))
    );
    let read = || -> serde_json::Value {
        serde_json::from_str(&f.queue.database.meta("footprints_v1").unwrap().unwrap()).unwrap()
    };
    tokio::time::timeout(Duration::from_secs(8), async {
        while read()[stable_id(&first)]["time"] == before[stable_id(&first)]["time"] {
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .unwrap();
    let after = read();
    assert_eq!(before[stable_id(&second)], after[stable_id(&second)]);
    assert_ne!(
        before[stable_id(&first)]["time"],
        after[stable_id(&first)]["time"]
    );
}

#[tokio::test]
async fn canceled_admin_bundle_removal_preserves_data_in_both_modes() {
    for mode in [DeleteMode::Trash, DeleteMode::Delete] {
        let f = Fixture::with_admin_apps(true);
        let app = f.app("Editor", "com.example.editor");
        let data = f.dir("Library/Caches/com.example.editor");
        f.admin.canceled.store(true, Ordering::Release);
        let plan = f.plan(&app).await;
        assert!(
            plan.apps[0]
                .items
                .iter()
                .any(|i| i.category == ItemCategory::App && i.needs_admin)
        );
        let task = f.execute(&plan, mode, false, None).await;
        assert_eq!(task.state, TaskState::Failed);
        assert_eq!(task.error.unwrap().code, "E_UNINSTALL_INCOMPLETE");
        assert_eq!(f.admin.calls.load(Ordering::Acquire), 1);
        assert!(app.exists());
        assert!(data.join("data").exists());
    }
}

#[tokio::test]
async fn restore_and_purge_admin_denial_report_failure_and_allow_retry() {
    let f = Fixture::with_admin_apps(true);
    let app = f.app("Editor", "com.example.editor");
    let plan = f.plan(&app).await;
    let task = f.execute(&plan, DeleteMode::Trash, false, None).await;
    assert_eq!(task.state, TaskState::Succeeded);
    f.admin.canceled.store(true, Ordering::Release);
    let error = f.engine.restore(&task.id).unwrap_err();
    assert_eq!(error.code, "E_RESTORE_UNAVAILABLE");
    assert!(error.detail.unwrap().contains("Editor.app"));
    assert!(f.engine.purge(&task.id).is_err());
    let entries =
        opennavo_desktop_lib::uninstall::manifest::entries(&f.queue.database, &task.id).unwrap();
    assert!(
        entries
            .iter()
            .any(|e| e.trash.as_ref().is_some_and(|path| path.exists()))
    );
    f.admin.canceled.store(false, Ordering::Release);
    // Size estimation is best effort (200 ms), so a successful purge can report zero
    // under load. Assert the persisted byte accounting and actual manifest/path state.
    let expected_bytes: u64 = entries
        .iter()
        .filter(|e| e.state == "trash")
        .map(|e| e.bytes)
        .sum();
    assert_eq!(f.engine.purge(&task.id).unwrap(), expected_bytes);
    assert!(
        entries
            .iter()
            .filter_map(|e| e.trash.as_ref())
            .all(|p| !p.exists())
    );
    let finished =
        opennavo_desktop_lib::uninstall::manifest::entries(&f.queue.database, &task.id).unwrap();
    assert!(finished.iter().all(|e| e.state == "purged"));
}

#[tokio::test]
async fn data_written_while_the_app_quits_is_still_removed() {
    let f = Fixture::new();
    let app = f.app("Editor", "com.example.editor");
    let container = f.dir("Library/Containers/com.example.editor");
    let prefs = f
        .engine
        .roots
        .home
        .join("Library/Preferences/com.example.editor.plist");
    fs::write(&prefs, "v1").unwrap();
    let plan = f.plan(&app).await;
    // What a quitting app does: write to its container, and preferences are replaced atomically.
    fs::write(container.join("written-at-quit"), "x").unwrap();
    fs::write(prefs.with_extension("tmp"), "v2").unwrap();
    fs::rename(prefs.with_extension("tmp"), &prefs).unwrap();
    let task = f.execute(&plan, DeleteMode::Trash, false, None).await;
    assert_eq!(task.state, TaskState::Succeeded);
    let outcome = task.outcome.unwrap();
    assert!(outcome.skipped.is_empty(), "{:?}", outcome.skipped);
    assert!(!app.exists());
    assert!(!container.exists());
    assert!(!prefs.exists());
    assert_eq!(f.engine.restore(&task.id).unwrap().restored, 3);
    assert_eq!(fs::read_to_string(&prefs).unwrap(), "v2");
}

#[tokio::test]
async fn folders_removed_only_when_empty_go_last_and_otherwise_stay_quietly() {
    for other_product in [false, true] {
        let f = Fixture::new();
        let app = f.app("Tool", "com.example.tool");
        let vendor = f
            .engine
            .roots
            .home
            .join("Library/Application Support/Vendor");
        fs::create_dir_all(&vendor).unwrap();
        let settings = vendor.join("settings.json");
        fs::write(&settings, "{}").unwrap();
        if other_product {
            fs::write(vendor.join("other-product.json"), "{}").unwrap();
        }
        // The receipt lists the folder before its file: the folder must still go after it.
        f.probe.receipts.lock().unwrap().insert(
            app.clone(),
            vec![Receipt {
                id: "com.example.tool.pkg".into(),
                files: vec![
                    app.join("Contents/Info.plist"),
                    vendor.clone(),
                    settings.clone(),
                ],
            }],
        );
        let plan = f.plan(&app).await;
        let task = f.execute(&plan, DeleteMode::Trash, false, None).await;
        assert_eq!(task.state, TaskState::Succeeded);
        let outcome = task.outcome.unwrap();
        assert!(outcome.skipped.is_empty(), "{:?}", outcome.skipped);
        assert!(!settings.exists());
        assert_eq!(vendor.exists(), other_product);
    }
}

#[tokio::test]
async fn copies_sharing_a_bundle_id_are_told_apart_by_path() {
    let f = Fixture::new();
    let stable = f.app("Xcode", "com.apple.dt.Xcode");
    let beta = f.app("Xcode-beta", "com.apple.dt.Xcode");
    f.apps.running.lock().unwrap().push(Process {
        pid: 42,
        path: stable.clone(),
        bundle_id: "com.apple.dt.Xcode".into(),
    });
    let apps = f.engine.refresh(LocalAppsChangedReason::Refresh).unwrap();
    let running = |path: &Path| {
        apps.iter()
            .find(|a| a.path == path.to_string_lossy())
            .unwrap()
            .running
    };
    assert!(running(&stable));
    assert!(!running(&beta));
    let plan = f.plan(&beta).await;
    assert!(!plan.apps[0].running);
    let task = f.execute(&plan, DeleteMode::Trash, true, None).await;
    assert_eq!(task.state, TaskState::Succeeded);
    assert_eq!(f.apps.quits.load(Ordering::Acquire), 0);
    assert!(!beta.exists());
    assert!(stable.exists());
}

#[tokio::test]
async fn framework_default_ids_prove_nothing_about_processes_or_files() {
    let f = Fixture::new();
    let app = f.app("DevTools", "com.github.Electron");
    let prefs = f
        .engine
        .roots
        .home
        .join("Library/Preferences/com.github.Electron.plist");
    fs::write(&prefs, "x").unwrap();
    // Another Electron app run from a project folder.
    f.apps.running.lock().unwrap().push(Process {
        pid: 7,
        path: f
            .base
            .join("Projects/demo/node_modules/electron/dist/Electron.app"),
        bundle_id: "com.github.Electron".into(),
    });
    let apps = f.engine.refresh(LocalAppsChangedReason::Refresh).unwrap();
    assert!(
        !apps
            .iter()
            .find(|a| a.path == app.to_string_lossy())
            .unwrap()
            .running
    );
    let plan = f.plan(&app).await;
    let item = plan.apps[0]
        .items
        .iter()
        .find(|i| i.path == prefs.to_string_lossy())
        .unwrap();
    assert_eq!(item.confidence, PlanItemConfidence::Low);
    assert!(!item.selected);
    f.execute(&plan, DeleteMode::Trash, true, None).await;
    assert_eq!(f.apps.quits.load(Ordering::Acquire), 0);
    assert!(prefs.exists());
}

#[tokio::test]
async fn item_ids_stay_the_same_when_the_same_files_are_scanned_again() {
    let f = Fixture::new();
    let app = f.app("Editor", "com.example.editor");
    f.dir("Library/Caches/com.example.editor");
    f.dir("Library/Application Support/Editor");
    let ids = |plan: &UninstallPlan| {
        plan.apps[0]
            .items
            .iter()
            .map(|i| (i.path.clone(), i.id.clone()))
            .collect::<BTreeMap<_, _>>()
    };
    let first = f.plan(&app).await;
    let second = f.plan(&app).await;
    assert_ne!(first.plan_id, second.plan_id);
    assert_eq!(ids(&first), ids(&second));
    let unique: std::collections::BTreeSet<_> = first.apps[0].items.iter().map(|i| &i.id).collect();
    assert_eq!(unique.len(), first.apps[0].items.len());
}

#[test]
fn personal_data_kinds_come_from_known_bundle_ids() {
    let kind = |id: &str| {
        let app = LocalApp {
            id: id.into(),
            path: "/Applications/X.app".into(),
            name: "X".into(),
            bundle_id: Some(id.into()),
            version: None,
            source: AppSource::Direct,
            updater: None,
            homebrew_cask: None,
            cask: None,
            catalog_token: None,
            team_id: None,
            vendor: None,
            arch: None,
            size_bytes: None,
            installed_at: None,
            last_used_at: None,
            icon_path: None,
            running: false,
            removable: true,
            protected_reason: None,
            uninstaller: None,
        };
        opennavo_desktop_lib::uninstall::scan::personal_data(
            &opennavo_desktop_lib::uninstall::CachedApp {
                app,
                identity: Identity::default(),
                receipts: vec![],
            },
        )
    };
    assert_eq!(kind("com.tencent.xinWeChat"), "chat_history");
    assert_eq!(kind("com.google.Chrome.beta"), "browser_profile");
    assert_eq!(kind("com.parallels.desktop.console"), "vm_disk");
    assert_eq!(kind("com.jetbrains.intellij"), "documents");
    // Words inside other IDs no longer count: 1Password is not a word processor.
    assert_eq!(kind("com.1password.1password"), "other");
    assert_eq!(kind("com.example.knowledge-edge"), "other");
    assert_eq!(kind("com.google.chromecast.helper.x"), "other");
}

#[tokio::test]
async fn unchanged_bundles_are_not_probed_again() {
    let f = Fixture::new();
    f.probe.cache.store(true, Ordering::Release);
    let app = f.app("Editor", "com.example.editor");
    f.app("Viewer", "com.example.viewer");
    f.engine.refresh(LocalAppsChangedReason::Refresh).unwrap();
    assert_eq!(f.probe.calls.load(Ordering::Acquire), 2);
    f.engine.refresh(LocalAppsChangedReason::Fs).unwrap();
    assert_eq!(f.probe.calls.load(Ordering::Acquire), 2);
    // An update writes a new Info.plist: only that app is examined again.
    std::thread::sleep(Duration::from_millis(5));
    f.app("Editor", "com.example.editor");
    let apps = f.engine.refresh(LocalAppsChangedReason::Fs).unwrap();
    assert_eq!(f.probe.calls.load(Ordering::Acquire), 3);
    assert!(apps.iter().any(|a| a.path == app.to_string_lossy()));
}

#[tokio::test]
async fn listing_during_the_first_enumeration_waits_for_it() {
    let f = Fixture::new();
    f.app("Editor", "com.example.editor");
    f.probe.delay.store(true, Ordering::Release);
    let engine = f.engine.clone();
    let first = std::thread::spawn(move || engine.list().unwrap());
    std::thread::sleep(Duration::from_millis(30));
    let second = f.engine.list().unwrap();
    assert_eq!(first.join().unwrap().len(), 1);
    assert_eq!(second.len(), 1);
    assert_eq!(f.probe.calls.load(Ordering::Acquire), 1);
}

#[tokio::test]
async fn measured_sizes_survive_a_refresh_of_unchanged_bundles() {
    let f = Fixture::new();
    let app = f.app("Editor", "com.example.editor");
    let id = stable_id(&app);
    f.engine.refresh(LocalAppsChangedReason::Refresh).unwrap();
    tokio::time::timeout(Duration::from_secs(3), async {
        while f.engine.app(&id).unwrap().app.size_bytes.is_none() {
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .unwrap();
    let apps = f.engine.refresh(LocalAppsChangedReason::Fs).unwrap();
    assert!(
        apps.iter()
            .find(|a| a.id == id)
            .unwrap()
            .size_bytes
            .is_some()
    );
}

#[tokio::test]
async fn an_incomplete_footprint_is_a_result_and_access_changes_measure_again() {
    let f = Fixture::new();
    let app = f.app("Editor", "com.example.editor");
    f.dir("Library/Caches/com.example.editor");
    f.engine.refresh(LocalAppsChangedReason::Refresh).unwrap();
    let mut limited = permissions();
    limited.full_disk_access = PermissionState::Denied;
    let settled = || async {
        tokio::time::timeout(Duration::from_secs(5), async {
            while f
                .engine
                .footprint_report()
                .unwrap()
                .progress
                .is_some_and(|p| p.running)
            {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
    };
    assert!(f.engine.scan_footprints(limited, true).unwrap());
    settled().await;
    let report = f.engine.footprint_report().unwrap();
    assert!(report.errors.is_empty());
    assert!(!report.values[&stable_id(&app)].complete);
    assert_eq!(report.progress.unwrap().failed, 0);
    // Within the hourly gate, values computed without Full Disk Access are measured again once it is granted.
    assert!(f.engine.scan_footprints(permissions(), false).unwrap());
    settled().await;
    assert!(f.engine.footprint_report().unwrap().values[&stable_id(&app)].complete);
    assert!(!f.engine.scan_footprints(permissions(), false).unwrap());
}

#[tokio::test]
async fn running_apps_count_as_used_and_spotlight_date_added_is_the_install_date() {
    let f = Fixture::new();
    let app = f.app("Menu", "com.example.menu");
    let mut identity = bundle(&app);
    identity.date_added = Some(1_700_000_000_000);
    f.probe
        .identities
        .lock()
        .unwrap()
        .insert(app.clone(), identity);
    f.apps.running.lock().unwrap().push(Process {
        pid: 9,
        path: app.clone(),
        bundle_id: "com.example.menu".into(),
    });
    let before = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_millis() as i64;
    let apps = f.engine.refresh(LocalAppsChangedReason::Refresh).unwrap();
    assert!(apps[0].last_used_at.unwrap() >= before);
    assert_eq!(apps[0].installed_at, Some(1_700_000_000_000));
    // Remembered after it quits, also by a later enumeration.
    f.apps.running.lock().unwrap().clear();
    f.engine.update_running().unwrap();
    let apps = f.engine.refresh(LocalAppsChangedReason::Refresh).unwrap();
    assert!(!apps[0].running);
    assert!(apps[0].last_used_at.unwrap() >= before);
}

#[test]
fn sizes_skip_vanished_and_unreadable_entries_and_count_hard_links_once() {
    let temp = tempfile::tempdir().unwrap();
    let root = temp.path().join("tree");
    fs::create_dir_all(root.join("private")).unwrap();
    fs::write(root.join("file"), vec![0u8; 64 * 1024]).unwrap();
    fs::hard_link(root.join("file"), root.join("link")).unwrap();
    fs::write(root.join("private/secret"), vec![0u8; 64 * 1024]).unwrap();
    let full =
        opennavo_desktop_lib::uninstall::rules::allocated_size_detail(&root, None, None).unwrap();
    assert!(!full.partial);
    fs::set_permissions(root.join("private"), fs::Permissions::from_mode(0o000)).unwrap();
    let limited =
        opennavo_desktop_lib::uninstall::rules::allocated_size_detail(&root, None, None).unwrap();
    fs::set_permissions(root.join("private"), fs::Permissions::from_mode(0o755)).unwrap();
    assert!(limited.partial);
    assert!(limited.bytes < full.bytes);
    // The hard link adds no second copy.
    fs::remove_file(root.join("link")).unwrap();
    let unlinked =
        opennavo_desktop_lib::uninstall::rules::allocated_size_detail(&root, None, None).unwrap();
    assert_eq!(unlinked.bytes, full.bytes);
}
