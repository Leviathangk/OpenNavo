use super::*;
use std::{
    fs,
    os::unix::fs::PermissionsExt,
    time::{Duration, Instant},
};
#[derive(Default)]
pub(super) struct FakeCommands(Mutex<Vec<Vec<String>>>);
impl Commands for FakeCommands {
    fn run(&self, p: &str, args: &[&str]) -> Result<String, AppError> {
        self.0.lock().unwrap().push(
            std::iter::once(p.to_owned())
                .chain(args.iter().map(|s| (*s).to_owned()))
                .collect(),
        );
        if args.first() == Some(&"print") {
            Err(AppError::new("E_UNKNOWN", "not_loaded"))
        } else {
            Ok(String::new())
        }
    }
}
pub(super) struct Fixture {
    temp: tempfile::TempDir,
    engine: Arc<av::Engine>,
}
impl Fixture {
    pub fn path(&self) -> &Path {
        self.temp.path()
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        self.engine.shutdown();
    }
}
pub(super) fn fixture() -> (Fixture, Arc<Service>, Arc<FakeCommands>) {
    fixture_at("home", "run")
}
pub(super) fn fixture_at(
    home_name: &str,
    runtime_name: &str,
) -> (Fixture, Arc<Service>, Arc<FakeCommands>) {
    let temp = tempfile::tempdir().unwrap();
    let root = temp.path().canonicalize().unwrap();
    let home = root.join(home_name);
    fs::create_dir_all(&home).unwrap();
    let app_data = home.join("Library/Application Support/com.opennavo.fixture");
    let data = app_data.join("logs/security");
    let db = Database::open(&app_data.join("opennavo.db")).unwrap();
    let commands = Arc::new(FakeCommands::default());
    let settings = Arc::new(RwLock::new(Settings::default()));
    let service = Service::new(
        db,
        settings,
        Arc::new(|_| {}),
        home,
        data.clone(),
        av::Config {
            helpers: root.join("helpers"),
            database: data.join("av/db"),
            runtime: root.join(runtime_name),
            idle: Duration::from_millis(200),
        },
        commands.clone(),
    );
    (
        Fixture {
            temp,
            engine: service.av.clone(),
        },
        service,
        commands,
    )
}
fn executable(path: &Path, body: &str) {
    fs::write(path, body).unwrap();
    fs::set_permissions(path, fs::Permissions::from_mode(0o700)).unwrap();
}
pub(super) fn fake_engine(s: &Service) {
    let dir = &s.av.config.helpers;
    fs::create_dir_all(dir).unwrap();
    fs::write(dir.join("clamav.crt"), "fixture cert").unwrap();
    fs::create_dir_all(&s.av.config.database).unwrap();
    for name in ["main", "daily"] {
        let mut header = b"ClamAV-VDB:date:123:0".to_vec();
        header.resize(512, b' ');
        fs::write(s.av.config.database.join(format!("{name}.cvd")), header).unwrap();
    }
    executable(
        &dir.join("clamd"),
        r#"#!/usr/bin/env python3
import socket,os,sys,array,threading,time
config=dict(line.strip().split(' ',1) for line in open(sys.argv[1].split('=',1)[1]) if line.strip())
assert os.environ['CVD_CERTS_DIR']==config['CVDCertsDirectory'].strip('"')
assert config['SelfCheck']=='0' and config['ConcurrentDatabaseReload']=='no'
assert config['HeuristicScanPrecedence']=='no' and config['AlertExceedsMax']=='yes' and config['AlertEncrypted']=='yes'
p=config['LocalSocket'].strip('"')
s=socket.socket(socket.AF_UNIX);s.bind(p);os.chmod(p,0o600);s.listen()
def client(c):
 try:
  buf=b''
  while not buf.endswith(b'\0'):buf+=c.recv(1)
  if buf==b'zPING\0':c.sendall(b'PONG\0');return
  if buf==b'zSHUTDOWN\0':os._exit(0)
  assert buf==b'zFILDES\0'
  _,anc,_,_=c.recvmsg(1,socket.CMSG_SPACE(4))
  fds=array.array('i');fds.frombytes(anc[0][2][:4]);fd=fds[0]
  data=os.read(fd,1000000);os.close(fd)
  if b'close-after-idle' in data:
   marker=os.path.join(os.path.dirname(sys.argv[1].split('=',1)[1]),'closed-count')
   count=int(open(marker).read()) if os.path.exists(marker) else 0
   with open(marker,'w') as f:f.write(str(count+1))
   if count==0 or b'always-close' in data:
    time.sleep(0.05)
    return
  if b'wait-for-test-cancel' in data:
   with open(os.path.join(config['TemporaryDirectory'].strip(chr(34)),'partial-extraction'),'w') as f:f.write('fixture')
   # The test controls cancellation; do not race it against a fixed sleep.
   while c.recv(1):pass
   return
  if b'wait-for-cancel' in data:
   open(os.path.join(config['TemporaryDirectory'].strip(chr(34)),'partial-extraction'),'w').write('fixture')
   time.sleep(5)
  if b'serial-scan' in data:
   lock=os.path.join(config['TemporaryDirectory'].strip(chr(34)),'serial-active')
   with open(lock,'x') as f:f.write('active')
   time.sleep(0.1)
   os.unlink(lock)
  result=b'Fixture.Test FOUND' if b'fixture-threat' in data else b'OK'
  if data.startswith(b'heuristic:'):result=data.split(b':',1)[1]+b' FOUND'
  c.sendall(b'fd[1]: '+result+b'\0')
 except (BrokenPipeError,ConnectionResetError):pass
 finally:c.close()
while True:
 c,_=s.accept();threading.Thread(target=client,args=(c,),daemon=True).start()
"#,
    );
}
pub(super) fn wait_scan(s: &Service, id: &str) -> AvScanResult {
    let start = Instant::now();
    loop {
        let result = s.scan_get(id).unwrap();
        if result.state != AvScanState::Running {
            return result;
        }
        // The engine allows up to 90 seconds to start. Concurrent fixture
        // processes on CI must get that full budget before the test times out.
        assert!(
            start.elapsed() < Duration::from_secs(120),
            "fixture scan did not settle: {id}"
        );
        std::thread::sleep(Duration::from_millis(20));
    }
}
#[test]
fn missing_updater_reports_engine_error_without_starting_cooldown() {
    let (_temp, s, _) = fixture();
    s.settings.write().unwrap().av_consent = true;
    let error = s.av.check_update().unwrap_err();
    assert_eq!(error.code, "E_AV_ENGINE");
    assert_eq!(error.message, "freshclam_missing");
    assert!(s.database.meta("av_db_next_check").unwrap().is_none());
}

#[test]
fn freshclam_requires_consent_and_cooldown_survives_reopen() {
    let (_temp, s, _) = fixture();
    fake_engine(&s);
    executable(
        &s.av.config.helpers.join("freshclam"),
        r#"#!/usr/bin/env python3
import sys,os
config=dict(line.strip().split(' ',1) for line in open(sys.argv[1].split('=',1)[1]) if line.strip())
assert os.environ['CVD_CERTS_DIR']==config['CVDCertsDirectory'].strip('"')
print('HTTP 429\nRetry-After: 7200')
sys.exit(17)
"#,
    );
    assert_eq!(
        s.av.update(&AtomicBool::new(false)).unwrap_err().code,
        "E_AV_CONSENT"
    );
    assert!(s.database.meta("av_db_next_check").unwrap().is_none());
    s.settings.write().unwrap().av_consent = true;
    let error = s.av.update(&AtomicBool::new(false)).unwrap_err();
    assert_eq!(error.code, "E_AV_DB_RATE_LIMITED");
    assert!(error.detail.unwrap().contains("7200"));
    let engine = av::Engine::new(
        s.av.config.clone(),
        s.database.clone(),
        s.settings.clone(),
        s.sink.clone(),
    );
    assert_eq!(
        engine.check_update().unwrap_err().code,
        "E_AV_DB_RATE_LIMITED"
    );
}
#[test]
fn descriptor_scan_quarantine_conflict_restore_cancel_and_idle() {
    let (_temp, s, _) = fixture();
    fake_engine(&s);
    s.settings.write().unwrap().av_consent = true;
    let corpus = s.home.join("sample");
    fs::write(&corpus, b"fixture-threat").unwrap();
    let scan = s
        .scan_start(
            AvScanKind::Custom,
            Some(vec![corpus.to_string_lossy().into_owned()]),
        )
        .unwrap();
    let result = wait_scan(&s, &scan.scan_id);
    assert_eq!(result.state, AvScanState::Done);
    assert_eq!(result.findings.len(), 1);
    let id = result.findings[0].id.clone();
    let outcome = s
        .findings_act(&scan.scan_id, vec![id.clone()], FindingAction::Quarantine)
        .unwrap();
    assert_eq!(outcome.done, vec![id]);
    assert!(
        s.events(20)
            .unwrap()
            .iter()
            .any(|e| e.kind == ProtectionEventKind::ScanThreat && e.handled)
    );
    assert!(!corpus.exists());
    let entries = s.quarantine_list().unwrap();
    assert_eq!(entries.len(), 1);
    fs::write(&corpus, b"occupied").unwrap();
    let blocked = s.quarantine_restore(vec![entries[0].id.clone()]).unwrap();
    assert_eq!(blocked.failed[0].code, "E_RESTORE_CONFLICT");
    assert_eq!(fs::read(&corpus).unwrap(), b"occupied");
    fs::remove_file(&corpus).unwrap();
    let restored = s.quarantine_restore(vec![entries[0].id.clone()]).unwrap();
    assert_eq!(restored.done.len(), 1);
    assert_eq!(fs::read(&corpus).unwrap(), b"fixture-threat");
    assert!(
        s.events(20)
            .unwrap()
            .iter()
            .any(|e| e.kind == ProtectionEventKind::ScanThreat && !e.handled)
    );
    assert!(s.quarantine_list().unwrap().is_empty());
    fs::write(&corpus, b"wait-for-cancel").unwrap();
    let scan = s
        .scan_start(
            AvScanKind::Custom,
            Some(vec![corpus.to_string_lossy().into_owned()]),
        )
        .unwrap();
    std::thread::sleep(Duration::from_millis(100));
    s.scan_cancel(&scan.scan_id).unwrap();
    let canceled = wait_scan(&s, &scan.scan_id);
    assert_eq!(canceled.state, AvScanState::Canceled);
    assert!(canceled.error_code.is_none());
    assert_eq!(
        fs::read_dir(s.av.config.runtime.join("scan-tmp"))
            .unwrap()
            .count(),
        0
    );
    let start = Instant::now();
    while s.av.status().unwrap().running {
        assert!(start.elapsed() < Duration::from_secs(3));
        std::thread::sleep(Duration::from_millis(50));
    }
    s.settings.write().unwrap().av_keep_resident = true;
    let lease = s.av.lease(&AtomicBool::new(false)).unwrap();
    drop(lease);
    std::thread::sleep(Duration::from_millis(600));
    assert!(s.av.status().unwrap().running);
}
fn launch_fixture(s: &Service, root: &Path) -> StartupItem {
    let dir = s.home.join("Library/LaunchAgents");
    fs::create_dir_all(&dir).unwrap();
    let program = s.home.join("program");
    fs::write(&program, b"fixture").unwrap();
    let path = dir.join("example.fixture.plist");
    fs::write(&path,format!(r#"<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>example.fixture</string><key>Program</key><string>{}</string><key>RunAtLoad</key><true/></dict></plist>"#,program.display())).unwrap();
    let roots = startup::Roots {
        home: s.home.clone(),
        root: root.to_owned(),
        applications: vec![],
    };
    s.startup_scan_at(&roots)
        .unwrap()
        .items
        .into_iter()
        .find(|i| i.category == StartupCategory::LaunchItems)
        .unwrap()
}
#[test]
fn fixture_launch_agent_disable_enable_remove_restore() {
    let (temp, s, c) = fixture();
    let root = temp.path().canonicalize().unwrap();
    let item = launch_fixture(&s, &root);
    let id = item.id.clone();
    assert_eq!(
        s.startup_act(vec![id.clone()], StartupItemAction::Disable)
            .unwrap()
            .done
            .len(),
        1
    );
    assert_eq!(
        s.startup_act(vec![id.clone()], StartupItemAction::Enable)
            .unwrap()
            .done
            .len(),
        1
    );
    assert!(
        c.0.lock()
            .unwrap()
            .iter()
            .any(|v| v.get(1).is_some_and(|s| s == "bootstrap"))
    );
    assert_eq!(
        s.startup_act(vec![id], StartupItemAction::Remove)
            .unwrap()
            .done
            .len(),
        1
    );
    assert!(!Path::new(&item.path).exists());
    let q = s.quarantine_list().unwrap();
    assert_eq!(
        s.quarantine_restore(vec![q[0].id.clone()])
            .unwrap()
            .done
            .len(),
        1
    );
    assert!(Path::new(&item.path).exists());
    assert_eq!(
        s.saved_startup()
            .unwrap()
            .iter()
            .find(|i| i.id == item.id)
            .unwrap()
            .state,
        StartupState::Disabled
    );
}

#[test]
fn startup_owners_carry_the_icon_installed_apps_use() {
    let (temp, s, _) = fixture();
    let root = temp.path().canonicalize().unwrap();
    let app = root.join("Applications/Fixture.app");
    let program = app.join("Contents/MacOS/Fixture");
    fs::create_dir_all(program.parent().unwrap()).unwrap();
    fs::write(app.join("Contents/Info.plist"), r#"<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.fixture</string><key>CFBundleExecutable</key><string>Fixture</string></dict></plist>"#).unwrap();
    fs::write(&program, b"fixture").unwrap();
    let dir = s.home.join("Library/LaunchAgents");
    fs::create_dir_all(&dir).unwrap();
    fs::write(dir.join("com.example.fixture.helper.plist"), format!(r#"<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>com.example.fixture.helper</string><key>Program</key><string>{}</string><key>RunAtLoad</key><true/></dict></plist>"#, program.display())).unwrap();
    let roots = startup::Roots {
        home: s.home.clone(),
        root,
        applications: vec![],
    };
    let item = s
        .startup_scan_at(&roots)
        .unwrap()
        .items
        .into_iter()
        .find(|i| i.label.as_deref() == Some("com.example.fixture.helper"))
        .unwrap();
    let owner = item.owner.unwrap();
    assert_eq!(owner.path, app.to_string_lossy());
    assert_eq!(owner.bundle_id.as_deref(), Some("com.example.fixture"));
    // The same cache Installed fills, which the asset protocol may read.
    let icon = PathBuf::from(owner.icon_path.unwrap());
    assert!(icon.is_file());
    assert_eq!(
        icon.parent(),
        Some(s.data.with_file_name("local-app-icons").as_path())
    );
    // Saved rows keep it, so the overview and actions see the same owner.
    let saved = s.saved_startup().unwrap();
    let saved = saved.iter().find(|i| i.id == item.id).unwrap();
    assert_eq!(
        saved.owner.as_ref().unwrap().icon_path.as_deref(),
        Some(icon.to_str().unwrap())
    );
}

#[test]
fn service_management_jobs_switch_through_launchctl_and_daemons_ask_once() {
    #[derive(Default)]
    struct Admin(Mutex<Vec<admin::Request>>);
    impl admin::Actions for Admin {
        fn run(&self, requests: &[admin::Request]) -> Result<Vec<admin::ResultItem>, AppError> {
            self.0.lock().unwrap().extend(requests.iter().cloned());
            Ok(requests
                .iter()
                .map(|r| admin::ResultItem {
                    id: r.id.clone(),
                    error: None,
                })
                .collect())
        }
    }
    let (temp, s, commands) = fixture();
    let root = temp.path().canonicalize().unwrap();
    let app = root.join("Applications/Example.app");
    for (bundle, executable) in [
        (app.clone(), "Example"),
        (app.join("Contents/Library/LoginItems/Helper.app"), "Helper"),
    ] {
        fs::create_dir_all(bundle.join("Contents/MacOS")).unwrap();
        fs::write(bundle.join("Contents/Info.plist"), format!(r#"<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleExecutable</key><string>{executable}</string></dict></plist>"#)).unwrap();
        fs::write(bundle.join("Contents/MacOS").join(executable), b"fixture").unwrap();
    }
    fs::write(app.join("Contents/MacOS/Daemon"), b"fixture").unwrap();
    let text = |s: &str| plist::Value::String(s.into());
    let number = |n: u64| plist::Value::Integer(n.into());
    let relative = |path: &str| {
        plist::Value::Dictionary(
            [
                ("NS.base".to_owned(), text("file:///")),
                ("NS.relative".to_owned(), text(path)),
            ]
            .into_iter()
            .collect(),
        )
    };
    let store = startup::btm::test_archive(vec![
        vec![
            ("type", number(2)),
            ("identifier", text("2.com.example.app")),
            ("name", text("Example")),
            (
                "url",
                text(reqwest::Url::from_file_path(&app).unwrap().as_str()),
            ),
            ("disposition", number(2)),
        ],
        vec![
            ("type", number(4)),
            ("identifier", text("4.com.example.helper")),
            ("container", text("2.com.example.app")),
            ("name", text("Helper")),
            ("url", relative("Contents/Library/LoginItems/Helper.app")),
            ("disposition", number(11)),
        ],
        vec![
            ("type", number(16)),
            ("identifier", text("16.com.example.daemon")),
            ("container", text("2.com.example.app")),
            ("name", text("Daemon")),
            ("executablePath", text("Contents/MacOS/Daemon")),
            (
                "url",
                relative("Contents/Library/LaunchDaemons/com.example.daemon.plist"),
            ),
            ("disposition", number(11)),
        ],
    ]);
    let dir = root.join("private/var/db/com.apple.backgroundtaskmanagement");
    fs::create_dir_all(&dir).unwrap();
    fs::write(dir.join("BackgroundItems-v18.btm"), store).unwrap();
    let admin = Arc::new(Admin::default());
    *s.admin.write().unwrap() = Some(admin.clone());
    let roots = startup::Roots {
        home: s.home.clone(),
        root,
        applications: vec![],
    };
    let items = s.startup_scan_at(&roots).unwrap().items;
    let id = |label: &str| {
        items
            .iter()
            .find(|i| i.label.as_deref() == Some(label))
            .unwrap()
            .id
            .clone()
    };
    let (helper, daemon) = (id("4.com.example.helper"), id("16.com.example.daemon"));

    let outcome = s
        .startup_act(vec![helper.clone()], StartupItemAction::Disable)
        .unwrap();
    assert_eq!(outcome.done, vec![helper.clone()]);
    let target = format!(
        "{}/com.example.helper",
        crate::uninstall::identity::user_domain()
    );
    assert!(commands.0.lock().unwrap().iter().any(|c| {
        c == &["/bin/launchctl", "disable", &target]
            .map(String::from)
            .to_vec()
    }));
    // Nothing is stopped or loaded: it no longer starts at the next login.
    assert!(
        !commands
            .0
            .lock()
            .unwrap()
            .iter()
            .any(|c| c.iter().any(|a| a == "bootout" || a == "bootstrap"))
    );
    let saved = |id: &str| {
        s.saved_startup()
            .unwrap()
            .into_iter()
            .find(|i| i.id == id)
            .unwrap()
    };
    assert_eq!(saved(&helper).state, StartupState::Disabled);
    assert!(saved(&helper).actions.contains(&StartupAction::Enable));

    let outcome = s
        .startup_act(vec![daemon.clone()], StartupItemAction::Disable)
        .unwrap();
    assert_eq!(outcome.done, vec![daemon.clone()]);
    let requests = admin.0.lock().unwrap().clone();
    assert_eq!(requests.len(), 1);
    assert!(matches!(
        &requests[0].action,
        admin::Action::LaunchdService { identifier, enable: false }
            if identifier == "16.com.example.daemon"
    ));
    assert_eq!(saved(&daemon).state, StartupState::Disabled);

    // A job BTM no longer lists is never switched by label alone.
    fs::write(
        dir.join("BackgroundItems-v18.btm"),
        startup::btm::test_archive(vec![]),
    )
    .unwrap();
    let outcome = s
        .startup_act(vec![helper], StartupItemAction::Enable)
        .unwrap();
    assert_eq!(outcome.failed[0].code, "E_STARTUP_CHANGED");
}

#[test]
fn a_better_scanner_rebaselines_instead_of_reporting_what_it_newly_sees() {
    let (temp, s, _) = fixture();
    let root = temp.path().canonicalize().unwrap();
    launch_fixture(&s, &root);
    let roots = startup::Roots {
        home: s.home.clone(),
        root,
        applications: vec![],
    };
    let agent = |label: &str| {
        let program = s.home.join(label);
        fs::write(&program, b"fixture").unwrap();
        fs::write(s.home.join("Library/LaunchAgents").join(format!("{label}.plist")), format!(r#"<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>{label}</string><key>Program</key><string>{}</string><key>RunAtLoad</key><true/></dict></plist>"#, program.display())).unwrap();
    };
    let is_new = |label: &str| {
        s.startup_scan_at(&roots)
            .unwrap()
            .items
            .into_iter()
            .find(|i| i.label.as_deref() == Some(label))
            .unwrap()
            .is_new
    };
    // Items an older scanner could not see were there before this version looked.
    s.database.set_meta("startup_scanner", "1").unwrap();
    agent("example.seen.late");
    assert!(!is_new("example.seen.late"));
    agent("example.added");
    assert!(is_new("example.added"));
}

#[test]
fn unchanged_startup_scan_does_not_emit_another_startup_event() {
    use std::sync::atomic::AtomicUsize;
    let (temp, mut service, _) = fixture();
    let events = Arc::new(AtomicUsize::new(0));
    let sink_events = events.clone();
    Arc::get_mut(&mut service).unwrap().sink = Arc::new(move |event| {
        if matches!(
            event,
            CoreEvent::SecurityChanged(SecurityChanged {
                reason: SecurityChangeReason::Startup
            })
        ) {
            sink_events.fetch_add(1, Ordering::SeqCst);
        }
    });
    let root = temp.path().canonicalize().unwrap();
    let item = launch_fixture(&service, &root);
    assert_eq!(events.swap(0, Ordering::SeqCst), 1);
    let roots = startup::Roots {
        home: service.home.clone(),
        root,
        applications: vec![],
    };
    let again = service.startup_scan_at(&roots).unwrap();
    assert!(again.items.iter().any(|i| i.id == item.id));
    assert_eq!(events.swap(0, Ordering::SeqCst), 0);

    // Action notifications still trigger a refresh; its unchanged scan must stop there.
    service
        .startup_act(vec![item.id.clone()], StartupItemAction::MarkReviewed)
        .unwrap();
    assert_eq!(events.swap(0, Ordering::SeqCst), 1);
    service.startup_scan_at(&roots).unwrap();
    assert_eq!(events.swap(0, Ordering::SeqCst), 0);

    fs::remove_file(&item.path).unwrap();
    assert!(
        !service
            .startup_scan_at(&roots)
            .unwrap()
            .items
            .iter()
            .any(|i| i.id == item.id)
    );
    assert_eq!(events.swap(0, Ordering::SeqCst), 1);
    service.startup_scan_at(&roots).unwrap();
    assert_eq!(events.load(Ordering::SeqCst), 0);
}

#[test]
fn launch_labels_and_overrides_cover_versioned_brew_and_macos27() {
    assert!(startup::valid_launch_label("homebrew.mxcl.postgresql@18"));
    assert!(startup::valid_launch_label("com.oplus.O+Connect.uninstall"));
    assert!(!startup::valid_launch_label("../com.example"));
    assert!(!startup::valid_launch_label("-option"));
    for value in ["true", "disabled"] {
        assert_eq!(
            startup::override_disabled(Some(&format!("\"example.app\" => {value}")), "example.app"),
            Some(true)
        );
    }
    for value in ["false", "enabled"] {
        assert_eq!(
            startup::override_disabled(Some(&format!("\"example.app\" => {value}")), "example.app"),
            Some(false)
        );
    }
}
#[test]
fn replacement_after_scan_is_never_quarantined() {
    let (_temp, s, _) = fixture();
    let path = s.home.join("scan");
    fs::write(&path, b"old").unwrap();
    let snap = crate::uninstall::rules::Snapshot::capture(&path).unwrap();
    fs::remove_file(&path).unwrap();
    fs::write(&path, b"replacement").unwrap();
    assert_eq!(
        s.prepare_quarantine(&path, &snap, QuarantineReason::User, None, None)
            .unwrap_err()
            .code,
        "E_PROTECTED"
    );
    assert_eq!(fs::read(path).unwrap(), b"replacement");
}

#[test]
fn download_actions_require_detection_verification_and_unchanged_safe_content() {
    let (_temp, s, c) = fixture();
    let path = s.home.join("download.zip");
    fs::write(&path, b"local download fixture").unwrap();
    let invalid = |action| {
        assert_eq!(
            s.download_act(path.clone(), action).unwrap_err().code,
            "E_INVALID_ARG"
        );
    };
    invalid(DownloadAction::Reveal);
    // A manual verification result alone must not confer open permission.
    s.download_verify(path.clone(), None).unwrap();
    invalid(DownloadAction::Open);
    s.download_detected(&path, "example.app".into()).unwrap();
    invalid(DownloadAction::Open);
    let verdict = s.download_verify(path.clone(), None).unwrap();
    use sha2::{Digest, Sha256};
    assert_eq!(
        verdict.sha256,
        format!("{:x}", Sha256::digest(b"local download fixture"))
    );
    s.download_act(path.clone(), DownloadAction::Open).unwrap();
    s.download_act(path.clone(), DownloadAction::Reveal)
        .unwrap();
    assert_eq!(c.0.lock().unwrap().len(), 2);
    fs::remove_file(&path).unwrap();
    fs::write(&path, b"replacement").unwrap();
    invalid(DownloadAction::Open);
    invalid(DownloadAction::Reveal);
    assert_eq!(c.0.lock().unwrap().len(), 2);
}

#[test]
fn oversized_download_is_hashed_without_claiming_an_av_verdict() {
    let (_temp, s, _) = fixture();
    fake_engine(&s);
    s.settings.write().unwrap().av_consent = true;
    let path = s.home.join("large.bin");
    fs::File::create(&path)
        .unwrap()
        .set_len(files::MAX_REGULAR_FILE + 1)
        .unwrap();
    let result = s.download_verify(path, None).unwrap();
    assert_eq!(result.size, files::MAX_REGULAR_FILE + 1);
    assert_eq!(result.sha256.len(), 64);
    assert!(result.av.is_none());
    assert!(!s.av.status().unwrap().running);
}

#[test]
fn unsafe_downloads_can_be_revealed_but_never_opened_even_after_quarantine() {
    let (_temp, s, c) = fixture();
    fake_engine(&s);
    s.settings.write().unwrap().av_consent = true;
    let path = s.home.join("download.dmg");
    fs::write(&path, b"fixture-threat").unwrap();
    s.download_detected(&path, "example.app".into()).unwrap();
    assert!(
        !s.download_verify(path.clone(), None)
            .unwrap()
            .av
            .unwrap()
            .clean
    );
    assert_eq!(
        s.download_act(path.clone(), DownloadAction::Open)
            .unwrap_err()
            .code,
        "E_DOWNLOAD_UNSAFE"
    );
    s.download_act(path.clone(), DownloadAction::Reveal)
        .unwrap();
    let snap = crate::uninstall::rules::Snapshot::capture(&path).unwrap();
    let (entry, meta, request) = s
        .prepare_quarantine(&path, &snap, QuarantineReason::User, None, None)
        .unwrap();
    s.move_user(&request).unwrap();
    s.finish_quarantine(&entry, meta).unwrap();
    assert_eq!(
        s.download_act(path.clone(), DownloadAction::Open)
            .unwrap_err()
            .code,
        "E_DOWNLOAD_UNSAFE"
    );
    s.download_act(path, DownloadAction::Reveal).unwrap();
    let calls = c.0.lock().unwrap();
    assert_eq!(calls.len(), 2);
    assert_eq!(calls[1][1], "-R");
    assert_eq!(calls[1][3], s.vault().join(entry.id).to_string_lossy());
}

#[test]
fn system_actions_use_one_batch_and_apple_items_are_protected() {
    struct FakeAdmin {
        calls: AtomicUsize,
    }
    use std::sync::atomic::AtomicUsize;
    impl admin::Actions for FakeAdmin {
        fn run(&self, requests: &[admin::Request]) -> Result<Vec<admin::ResultItem>, AppError> {
            self.calls.fetch_add(1, Ordering::SeqCst);
            for request in requests {
                if let admin::Action::LaunchdDisableSystem { path, snapshot, .. } = &request.action
                {
                    files::same_file(snapshot, path)?;
                } else {
                    return Err(AppError::new("E_PROTECTED", "unexpected_action"));
                }
            }
            Ok(requests
                .iter()
                .map(|r| admin::ResultItem {
                    id: r.id.clone(),
                    error: None,
                })
                .collect())
        }
    }
    let (temp, s, _) = fixture();
    let root = temp.path().canonicalize().unwrap();
    let item = launch_fixture(&s, &root);
    let mut ids = vec![];
    for suffix in ["one", "two"] {
        let mut i = item.clone();
        i.id = format!("{}-{suffix}", item.id);
        i.requires_admin = true;
        i.scope = StartupScope::System;
        ids.push(i.id.clone());
        let snapshot = crate::uninstall::rules::Snapshot::capture(Path::new(&i.path)).unwrap();
        s.database
            .with(|c| {
                c.execute(
                    "INSERT INTO security_startup(id,value,snapshot) VALUES(?1,?2,?3)",
                    params![
                        i.id,
                        serde_json::to_string(&i)?,
                        serde_json::to_string(&snapshot)?
                    ],
                )?;
                Ok(())
            })
            .unwrap();
    }
    let admin = Arc::new(FakeAdmin {
        calls: AtomicUsize::new(0),
    });
    *s.admin.write().unwrap() = Some(admin.clone());
    let outcome = s.startup_act(ids, StartupItemAction::Disable).unwrap();
    assert_eq!(outcome.done.len(), 2);
    assert_eq!(admin.calls.load(Ordering::SeqCst), 1);
    let mut protected = item.clone();
    protected.trust = StartupTrust::Apple;
    s.database
        .with(|c| {
            c.execute(
                "UPDATE security_startup SET value=?2 WHERE id=?1",
                params![protected.id, serde_json::to_string(&protected)?],
            )?;
            Ok(())
        })
        .unwrap();
    let outcome = s
        .startup_act(vec![protected.id], StartupItemAction::Remove)
        .unwrap();
    assert_eq!(outcome.failed[0].code, "E_STARTUP_PROTECTED");
    assert!(Path::new(&item.path).exists());
    assert_eq!(admin.calls.load(Ordering::SeqCst), 1);
}

#[test]
fn quarantine_permission_decision_includes_file_owner_and_directory_access() {
    // Model a root-owned 0644 single-link file in a user-owned Downloads directory.
    // No privileged fixture or mutation of real user files is necessary.
    assert!(mutation_requires_admin(0, 501, 0o40755, 501));
    assert!(!mutation_requires_admin(501, 501, 0o40755, 501));
    assert!(mutation_requires_admin(501, 0, 0o40755, 501));
    assert!(mutation_requires_admin(501, 501, 0o40500, 501));
}

#[test]
fn quarantine_chmod_failure_rolls_back_and_conflict_remains_recoverable() {
    use crate::uninstall::rules::Snapshot;
    let (_root, service, _) = fixture();
    let source = service.home.join("file");
    fs::write(&source, b"fixture").unwrap();
    fs::set_permissions(&source, fs::Permissions::from_mode(0o644)).unwrap();
    let snapshot = Snapshot::capture(&source).unwrap();
    let (entry, meta, request) = service
        .prepare_quarantine(
            &source,
            &snapshot,
            QuarantineReason::Threat {
                signature: "fixture".into(),
            },
            None,
            None,
        )
        .unwrap();
    let destination = service.vault().join(&entry.id);
    let denied = || std::io::Error::from(std::io::ErrorKind::PermissionDenied);
    assert!(
        admin::quarantine_move_with(&source, &destination, &snapshot, |_| Err(denied())).is_err()
    );
    assert_eq!(fs::read(&source).unwrap(), b"fixture");
    assert!(!destination.exists());
    assert!(service.quarantine_list().unwrap().is_empty());
    assert_eq!(
        fs::metadata(&source).unwrap().permissions().mode() & 0o777,
        0o644
    );
    // A concurrent writer takes the original path before rollback. Keep its bytes and the pending record.
    assert!(
        admin::quarantine_move_with(&source, &destination, &snapshot, |_| {
            fs::write(&source, b"new occupant")?;
            Err(denied())
        })
        .is_err()
    );
    assert_eq!(fs::read(&source).unwrap(), b"new occupant");
    assert_eq!(fs::read(&destination).unwrap(), b"fixture");
    assert_eq!(service.quarantine_list().unwrap().len(), 1);
    assert!(
        !service
            .quarantine_restore(vec![entry.id.clone()])
            .unwrap()
            .failed
            .is_empty()
    );
    fs::remove_file(&source).unwrap();
    assert_eq!(
        service
            .quarantine_restore(vec![entry.id.clone()])
            .unwrap()
            .done,
        vec![entry.id]
    );
    assert_eq!(fs::read(&source).unwrap(), b"fixture");
    let _ = (meta, request);
}

#[test]
fn user_launch_agent_remove_uses_helper_when_file_mutation_requires_admin() {
    use std::sync::atomic::AtomicUsize;
    struct Admin {
        calls: AtomicUsize,
    }
    impl admin::Actions for Admin {
        fn run(&self, requests: &[admin::Request]) -> Result<Vec<admin::ResultItem>, AppError> {
            self.calls.fetch_add(1, Ordering::SeqCst);
            assert_eq!(requests.len(), 1);
            let admin::Action::QuarantineMove {
                source,
                destination,
                snapshot,
            } = &requests[0].action
            else {
                panic!("user launchd disable must not be dispatched as a system action")
            };
            // Simulate granting directory write access, without sudo or changing a file's owner.
            fs::set_permissions(source.parent().unwrap(), fs::Permissions::from_mode(0o700))?;
            admin::quarantine_move(source, destination, snapshot)?;
            Ok(vec![admin::ResultItem {
                id: requests[0].id.clone(),
                error: None,
            }])
        }
    }
    let (root, service, commands) = fixture();
    let item = launch_fixture(&service, &root.path().canonicalize().unwrap());
    assert!(!item.requires_admin);
    let path = Path::new(&item.path);
    fs::set_permissions(path.parent().unwrap(), fs::Permissions::from_mode(0o500)).unwrap();
    let helper = Arc::new(Admin {
        calls: AtomicUsize::new(0),
    });
    *service.admin.write().unwrap() = Some(helper.clone());
    let result = service
        .startup_act(vec![item.id.clone()], StartupItemAction::Remove)
        .unwrap();
    // Restore fixture permissions even if the assertion fails.
    fs::set_permissions(path.parent().unwrap(), fs::Permissions::from_mode(0o700)).unwrap();
    assert_eq!(result.done, vec![item.id]);
    assert_eq!(helper.calls.load(Ordering::SeqCst), 1);
    assert!(
        commands
            .0
            .lock()
            .unwrap()
            .iter()
            .any(|v| v.get(1).is_some_and(|s| s == "disable"))
    );
    let entries = service.quarantine_list().unwrap();
    assert_eq!(entries.len(), 1);
    assert!(service.quarantine_record(&entries[0].id).unwrap().1.system);
}

fn scan_threat(s: &Arc<Service>, path: &Path) -> AvScanResult {
    let scan = s
        .scan_start(
            AvScanKind::Custom,
            Some(vec![path.to_string_lossy().into_owned()]),
        )
        .unwrap();
    let result = wait_scan(s, &scan.scan_id);
    assert_eq!(result.state, AvScanState::Done);
    assert_eq!(result.findings.len(), 1);
    result
}

#[test]
fn threat_history_closes_together_and_restore_can_be_processed_again() {
    let (_temp, s, _) = fixture();
    fake_engine(&s);
    s.settings.write().unwrap().av_consent = true;
    let path = s.home.join("repeated-threat");
    fs::write(&path, b"fixture-threat").unwrap();
    let first = scan_threat(&s, &path);
    let second = scan_threat(&s, &path);
    let outcome = s
        .findings_act(
            &second.scan_id,
            vec![second.findings[0].id.clone()],
            FindingAction::Quarantine,
        )
        .unwrap();
    assert!(outcome.failed.is_empty());
    for scan in [&first, &second] {
        assert_eq!(
            s.scan_get(&scan.scan_id).unwrap().findings[0].status,
            FindingStatus::Quarantined
        );
    }
    assert_eq!(s.overview().unwrap().attention, 0);
    assert!(s.events(20).unwrap().iter().all(|e| e.handled));
    let entry = s.quarantine_list().unwrap().remove(0);
    assert!(
        s.quarantine_restore(vec![entry.id])
            .unwrap()
            .failed
            .is_empty()
    );
    for scan in [&first, &second] {
        assert_eq!(
            s.scan_get(&scan.scan_id).unwrap().findings[0].status,
            FindingStatus::Open
        );
    }
    assert!(s.events(20).unwrap().iter().all(|e| !e.handled));
    let outcome = s
        .findings_act(
            &first.scan_id,
            vec![first.findings[0].id.clone()],
            FindingAction::Delete,
        )
        .unwrap();
    assert!(outcome.failed.is_empty(), "{:?}", outcome.failed);
    assert!(!path.exists());
    assert_eq!(s.overview().unwrap().attention, 0);
    for scan in [&first, &second] {
        assert_eq!(
            s.scan_get(&scan.scan_id).unwrap().findings[0].status,
            FindingStatus::Deleted
        );
    }
}

fn assert_finding(s: &Service, scan: &AvScanResult, status: FindingStatus) {
    let current = s.scan_get(&scan.scan_id).unwrap();
    assert!(
        current.findings.iter().all(|f| f.status == status),
        "{current:?}"
    );
    for event in s.events(500).unwrap().iter().filter(|e| {
        e.item_id
            .as_ref()
            .is_some_and(|id| current.findings.iter().any(|f| &f.id == id))
    }) {
        assert_eq!(event.handled, status != FindingStatus::Open);
    }
}

#[test]
fn threat_history_trust_delete_and_vault_delete_notify_events_after_commit() {
    for action in [
        FindingAction::Trust,
        FindingAction::Delete,
        FindingAction::Quarantine,
    ] {
        let (_temp, mut s, _) = fixture();
        let notifications = Arc::new(Mutex::new(Vec::new()));
        let events = notifications.clone();
        Arc::get_mut(&mut s).unwrap().sink = Arc::new(move |event| {
            if let CoreEvent::SecurityChanged(change) = event {
                events.lock().unwrap().push(change.reason);
            }
        });
        fake_engine(&s);
        s.settings.write().unwrap().av_consent = true;
        let path = s.home.join("repeated");
        fs::write(&path, b"fixture-threat").unwrap();
        let first = scan_threat(&s, &path);
        let second = scan_threat(&s, &path);
        notifications.lock().unwrap().clear();
        assert!(
            s.findings_act(&second.scan_id, vec![second.findings[0].id.clone()], action)
                .unwrap()
                .failed
                .is_empty()
        );
        assert!(
            notifications
                .lock()
                .unwrap()
                .contains(&SecurityChangeReason::Events)
        );
        let status = match action {
            FindingAction::Trust => FindingStatus::Trusted,
            FindingAction::Delete => FindingStatus::Deleted,
            _ => FindingStatus::Quarantined,
        };
        for scan in [&first, &second] {
            assert_finding(&s, scan, status);
        }
        assert_eq!(s.overview().unwrap().attention, 0);
        if action == FindingAction::Quarantine {
            notifications.lock().unwrap().clear();
            let entry = s.quarantine_list().unwrap().remove(0);
            assert!(
                s.quarantine_delete(vec![entry.id])
                    .unwrap()
                    .failed
                    .is_empty()
            );
            for scan in [&first, &second] {
                assert_finding(&s, scan, FindingStatus::Deleted);
            }
            assert!(
                notifications
                    .lock()
                    .unwrap()
                    .contains(&SecurityChangeReason::Events)
            );
        }
    }
}

#[test]
fn threat_association_requires_path_hash_and_exact_file_identity() {
    for action in [
        FindingAction::Trust,
        FindingAction::Delete,
        FindingAction::Quarantine,
    ] {
        let (_temp, s, _) = fixture();
        fake_engine(&s);
        s.settings.write().unwrap().av_consent = true;
        let path = s.home.join("target");
        let other = s.home.join("other");
        fs::write(&path, b"fixture-threat").unwrap();
        fs::write(&other, b"fixture-threat").unwrap();
        let other_scan = scan_threat(&s, &other);
        let old_identity = scan_threat(&s, &path);
        // Keep the old inode alive, so replacement cannot reuse it.
        fs::rename(&path, s.home.join("previous")).unwrap();
        fs::write(&path, b"fixture-threat").unwrap();
        let old_content = scan_threat(&s, &path);
        fs::write(&path, b"fixture-threat changed").unwrap();
        let latest = scan_threat(&s, &path);
        let latest_again = scan_threat(&s, &path);
        assert!(
            s.findings_act(&latest.scan_id, vec![latest.findings[0].id.clone()], action)
                .unwrap()
                .failed
                .is_empty()
        );
        for scan in [&other_scan, &old_identity, &old_content] {
            assert_finding(&s, scan, FindingStatus::Open);
        }
        assert_eq!(s.overview().unwrap().attention, 3);
        if action == FindingAction::Quarantine {
            let entry = s.quarantine_list().unwrap().remove(0);
            // A new occupant at the same path stays independent from the stored IDs.
            fs::write(&path, b"fixture-threat changed").unwrap();
            let new_occupant = scan_threat(&s, &path);
            assert!(
                s.quarantine_delete(vec![entry.id])
                    .unwrap()
                    .failed
                    .is_empty()
            );
            assert_finding(&s, &new_occupant, FindingStatus::Open);
            for scan in [&latest, &latest_again] {
                assert_finding(&s, scan, FindingStatus::Deleted);
            }
        }
    }
}

#[test]
fn threat_restore_refreshes_parent_snapshot_and_reads_legacy_metadata() {
    let (_temp, s, _) = fixture();
    fake_engine(&s);
    s.settings.write().unwrap().av_consent = true;
    let dir = s.home.join("parent");
    fs::create_dir(&dir).unwrap();
    let path = dir.join("threat");
    fs::write(&path, b"fixture-threat").unwrap();
    let scan = scan_threat(&s, &path);
    assert!(
        s.findings_act(
            &scan.scan_id,
            vec![scan.findings[0].id.clone()],
            FindingAction::Quarantine
        )
        .unwrap()
        .failed
        .is_empty()
    );
    let entry = s.quarantine_list().unwrap().remove(0);
    s.database.with(|c| {
        c.execute("UPDATE security_quarantine SET metadata=json_remove(metadata,'$.findings') WHERE id=?1", [&entry.id])?;
        Ok(())
    }).unwrap();
    fs::rename(&dir, s.home.join("old-parent")).unwrap();
    fs::create_dir(&dir).unwrap();
    assert!(
        s.quarantine_restore(vec![entry.id])
            .unwrap()
            .failed
            .is_empty()
    );
    assert_finding(&s, &scan, FindingStatus::Open);
    assert!(
        s.findings_act(
            &scan.scan_id,
            vec![scan.findings[0].id.clone()],
            FindingAction::Quarantine
        )
        .unwrap()
        .failed
        .is_empty()
    );
    assert_finding(&s, &scan, FindingStatus::Quarantined);
}

#[test]
fn threat_trust_transaction_rolls_back_findings_events_and_trust_on_database_failure() {
    let (_temp, s, _) = fixture();
    fake_engine(&s);
    s.settings.write().unwrap().av_consent = true;
    let path = s.home.join("threat");
    fs::write(&path, b"fixture-threat").unwrap();
    let first = scan_threat(&s, &path);
    let second = scan_threat(&s, &path);
    s.database.with(|c| { c.execute_batch("CREATE TEMP TRIGGER fail_events BEFORE UPDATE ON protection_events BEGIN SELECT RAISE(ABORT,'fixture failure'); END;")?; Ok(()) }).unwrap();
    let result = s
        .findings_act(
            &second.scan_id,
            vec![second.findings[0].id.clone()],
            FindingAction::Trust,
        )
        .unwrap();
    assert_eq!(result.failed.len(), 1);
    for scan in [&first, &second] {
        assert_finding(&s, scan, FindingStatus::Open);
    }
    assert_eq!(
        s.database
            .with(|c| Ok(
                c.query_row("SELECT count(*) FROM security_trusted", [], |r| r
                    .get::<_, i64>(0))?
            ))
            .unwrap(),
        0
    );
}

#[test]
fn threat_pending_scan_cannot_insert_after_the_file_was_quarantined() {
    use crate::uninstall::rules::Snapshot;
    let (_temp, s, _) = fixture();
    fake_engine(&s);
    s.settings.write().unwrap().av_consent = true;
    let path = s.home.join("threat");
    fs::write(&path, b"fixture-threat").unwrap();
    let scan = scan_threat(&s, &path);
    let snapshot = Snapshot::capture(&path).unwrap();
    let mut late = scan.findings[0].clone();
    late.id = uuid::Uuid::now_v7().to_string();
    let guard = s.mutation.lock().unwrap();
    let service = s.clone();
    let scan_id = scan.scan_id.clone();
    let late_snapshot = snapshot.clone();
    let (tx, rx) = std::sync::mpsc::channel();
    let thread = std::thread::spawn(move || {
        tx.send(service.record_scan_finding(&scan_id, &late, &late_snapshot))
            .unwrap();
    });
    assert!(rx.recv_timeout(Duration::from_millis(40)).is_err());
    let (entry, meta, request) = s
        .prepare_quarantine(&path, &snapshot, QuarantineReason::User, None, None)
        .unwrap();
    s.move_user(&request).unwrap();
    s.finish_quarantine(&entry, meta).unwrap();
    drop(guard);
    assert!(rx.recv_timeout(Duration::from_secs(3)).unwrap().is_err());
    thread.join().unwrap();
    assert_finding(&s, &scan, FindingStatus::Quarantined);
    assert_eq!(s.scan_get(&scan.scan_id).unwrap().findings.len(), 1);
    assert_eq!(s.overview().unwrap().attention, 0);
}

#[test]
fn threat_batch_deduplicates_instances_and_preserves_admin_failure_states() {
    use crate::uninstall::rules::Snapshot;
    struct Admin {
        mode: &'static str,
        calls: Mutex<Vec<usize>>,
    }
    impl admin::Actions for Admin {
        fn run(&self, requests: &[admin::Request]) -> Result<Vec<admin::ResultItem>, AppError> {
            self.calls.lock().unwrap().push(requests.len());
            let mut results = Vec::new();
            for request in requests {
                let error = if self.mode == "denied" {
                    Some(AppError::new("E_ADMIN_CANCELED", "fixture refused"))
                } else if self.mode == "move-failed" {
                    Some(AppError::new("E_PERMISSION", "fixture move failed"))
                } else {
                    match &request.action {
                        admin::Action::QuarantineMove {
                            source,
                            destination,
                            snapshot,
                        } => {
                            fs::set_permissions(
                                source.parent().unwrap(),
                                fs::Permissions::from_mode(0o700),
                            )?;
                            admin::quarantine_move(source, destination, snapshot)?;
                            if self.mode == "delete-failed" {
                                fs::set_permissions(
                                    destination.parent().unwrap(),
                                    fs::Permissions::from_mode(0o500),
                                )?;
                            }
                        }
                        admin::Action::QuarantineRestore {
                            source,
                            destination,
                            snapshot,
                            mode,
                        } => {
                            files::same_file(snapshot, source)?;
                            fs::set_permissions(source, fs::Permissions::from_mode(*mode))?;
                            admin::move_exclusive(source, destination)?;
                        }
                        _ => panic!("unexpected action"),
                    }
                    None
                };
                results.push(admin::ResultItem {
                    id: request.id.clone(),
                    error,
                });
            }
            Ok(results)
        }
    }
    for mode in ["moved", "denied", "move-failed", "delete-failed"] {
        let (_temp, s, _) = fixture();
        fake_engine(&s);
        s.settings.write().unwrap().av_consent = true;
        let dir = s.home.join("protected");
        fs::create_dir(&dir).unwrap();
        let path = dir.join("threat");
        fs::write(&path, b"fixture-threat").unwrap();
        let first = scan_threat(&s, &path);
        let second = scan_threat(&s, &path);
        let mut duplicate = second.findings[0].clone();
        duplicate.id = uuid::Uuid::now_v7().to_string();
        assert!(
            s.record_scan_finding(
                &second.scan_id,
                &duplicate,
                &Snapshot::capture(&path).unwrap()
            )
            .unwrap()
        );
        let admin = Arc::new(Admin {
            mode,
            calls: Mutex::new(Vec::new()),
        });
        *s.admin.write().unwrap() = Some(admin.clone());
        fs::set_permissions(&dir, fs::Permissions::from_mode(0o500)).unwrap();
        let result = s
            .findings_act(
                &second.scan_id,
                vec![
                    second.findings[0].id.clone(),
                    duplicate.id.clone(),
                    first.findings[0].id.clone(),
                    "missing".into(),
                ],
                if mode == "delete-failed" {
                    FindingAction::Delete
                } else {
                    FindingAction::Quarantine
                },
            )
            .unwrap();
        fs::set_permissions(&dir, fs::Permissions::from_mode(0o700)).unwrap();
        fs::set_permissions(s.vault(), fs::Permissions::from_mode(0o700)).unwrap();
        assert_eq!(*admin.calls.lock().unwrap(), vec![1]);
        // The historical ID belongs to a different scan and is never accepted as a request.
        assert!(
            result
                .failed
                .iter()
                .any(|f| f.id == first.findings[0].id && f.code == "E_NOT_FOUND")
        );
        assert!(
            result
                .failed
                .iter()
                .any(|f| f.id == "missing" && f.code == "E_NOT_FOUND")
        );
        if mode == "moved" {
            assert_eq!(
                result.done,
                vec![second.findings[0].id.clone(), duplicate.id]
            );
            for scan in [&first, &second] {
                assert_finding(&s, scan, FindingStatus::Quarantined);
            }
            let entry = s.quarantine_list().unwrap().remove(0);
            *s.admin.write().unwrap() = Some(Arc::new(Admin {
                mode: "denied",
                calls: Mutex::new(Vec::new()),
            }));
            assert_eq!(
                s.quarantine_restore(vec![entry.id.clone()]).unwrap().failed[0].code,
                "E_ADMIN_CANCELED"
            );
            for scan in [&first, &second] {
                assert_finding(&s, scan, FindingStatus::Quarantined);
            }
            *s.admin.write().unwrap() = Some(admin.clone());
            assert!(
                s.quarantine_restore(vec![entry.id])
                    .unwrap()
                    .failed
                    .is_empty()
            );
            assert_eq!(*admin.calls.lock().unwrap(), vec![1, 1]);
            for scan in [&first, &second] {
                assert_finding(&s, scan, FindingStatus::Open);
            }
        } else if mode == "delete-failed" {
            assert_eq!(result.failed.len(), 4);
            for scan in [&first, &second] {
                assert_finding(&s, scan, FindingStatus::Quarantined);
            }
            let entry = s.quarantine_list().unwrap().remove(0);
            assert!(
                s.quarantine_restore(vec![entry.id])
                    .unwrap()
                    .failed
                    .is_empty()
            );
            for scan in [&first, &second] {
                assert_finding(&s, scan, FindingStatus::Open);
            }
        } else {
            assert_eq!(result.failed.len(), 4);
            for scan in [&first, &second] {
                assert_finding(&s, scan, FindingStatus::Open);
            }
            assert_eq!(fs::read(&path).unwrap(), b"fixture-threat");
            assert!(s.quarantine_list().unwrap().is_empty());
        }
    }
}

#[test]
fn threat_batch_user_duplicates_and_changed_file_keep_partial_results() {
    use crate::uninstall::rules::Snapshot;
    for action in [
        FindingAction::Quarantine,
        FindingAction::Delete,
        FindingAction::Trust,
    ] {
        let (_temp, s, _) = fixture();
        fake_engine(&s);
        s.settings.write().unwrap().av_consent = true;
        let path = s.home.join("good");
        let changed = s.home.join("changed");
        fs::write(&path, b"fixture-threat").unwrap();
        fs::write(&changed, b"fixture-threat").unwrap();
        let scan = s
            .scan_start(
                AvScanKind::Custom,
                Some(vec![
                    path.to_string_lossy().into_owned(),
                    changed.to_string_lossy().into_owned(),
                ]),
            )
            .unwrap();
        let scan = wait_scan(&s, &scan.scan_id);
        assert_eq!(scan.findings.len(), 2);
        let good = scan
            .findings
            .iter()
            .find(|f| f.path == path.to_string_lossy())
            .unwrap();
        let bad = scan
            .findings
            .iter()
            .find(|f| f.path == changed.to_string_lossy())
            .unwrap();
        let mut duplicate = good.clone();
        duplicate.id = uuid::Uuid::now_v7().to_string();
        assert!(
            s.record_scan_finding(
                &scan.scan_id,
                &duplicate,
                &Snapshot::capture(&path).unwrap()
            )
            .unwrap()
        );
        fs::write(&changed, b"replacement").unwrap();
        let result = s
            .findings_act(
                &scan.scan_id,
                vec![good.id.clone(), bad.id.clone(), duplicate.id.clone()],
                action,
            )
            .unwrap();
        assert_eq!(result.done, vec![good.id.clone(), duplicate.id]);
        assert_eq!(result.failed.len(), 1);
        assert_eq!(result.failed[0].id, bad.id);
        assert_eq!(result.failed[0].code, "E_PROTECTED");
        assert_eq!(fs::read(&changed).unwrap(), b"replacement");
        assert_eq!(s.overview().unwrap().attention, 1);
    }
}

#[test]
fn threat_handled_during_scan_stays_handled_in_finished_event() {
    let (_temp, mut s, _) = fixture();
    let (tx, rx) = std::sync::mpsc::channel();
    Arc::get_mut(&mut s).unwrap().sink = Arc::new(move |event| {
        if let CoreEvent::AvFinished(result) = event {
            tx.send(result).unwrap();
        }
    });
    fake_engine(&s);
    s.settings.write().unwrap().av_consent = true;
    let threat = s.home.join("first-threat");
    let slow = s.home.join("second-slow");
    fs::write(&threat, b"fixture-threat").unwrap();
    fs::write(&slow, b"wait-for-test-cancel").unwrap();
    let scan = s
        .scan_start(
            AvScanKind::Custom,
            Some(vec![
                threat.to_string_lossy().into_owned(),
                slow.to_string_lossy().into_owned(),
            ]),
        )
        .unwrap();
    let started = Instant::now();
    let current = loop {
        let current = s.scan_get(&scan.scan_id).unwrap();
        if current.findings.len() == 1
            && s.av
                .config
                .runtime
                .join("scan-tmp/partial-extraction")
                .exists()
        {
            break current;
        }
        assert_eq!(
            current.state,
            AvScanState::Running,
            "scan ended before the cancellation handshake: {current:?}"
        );
        // Match wait_scan's engine-start budget under parallel CI load. Once
        // the marker appears, the fixture waits for our cancellation/EOF.
        assert!(
            started.elapsed() < Duration::from_secs(120),
            "scan never reached the second file: {current:?}"
        );
        std::thread::sleep(Duration::from_millis(10));
    };
    assert!(
        s.findings_act(
            &scan.scan_id,
            vec![current.findings[0].id.clone()],
            FindingAction::Quarantine
        )
        .unwrap()
        .failed
        .is_empty()
    );
    s.scan_cancel(&scan.scan_id).unwrap();
    let finished = rx.recv_timeout(Duration::from_secs(10)).unwrap();
    assert_eq!(finished.state, AvScanState::Canceled);
    assert_eq!(finished.findings[0].status, FindingStatus::Quarantined);
    assert_eq!(
        finished.findings,
        s.scan_get(&scan.scan_id).unwrap().findings
    );
}

#[test]
fn threat_legacy_single_finding_metadata_can_be_deleted() {
    let (_temp, s, _) = fixture();
    fake_engine(&s);
    s.settings.write().unwrap().av_consent = true;
    let path = s.home.join("legacy");
    fs::write(&path, b"fixture-threat").unwrap();
    let scan = scan_threat(&s, &path);
    assert!(
        s.findings_act(
            &scan.scan_id,
            vec![scan.findings[0].id.clone()],
            FindingAction::Quarantine
        )
        .unwrap()
        .failed
        .is_empty()
    );
    let entry = s.quarantine_list().unwrap().remove(0);
    s.database.with(|c| {
        c.execute("UPDATE security_quarantine SET metadata=json_remove(metadata,'$.findings') WHERE id=?1", [&entry.id])?;
        Ok(())
    }).unwrap();
    assert!(
        s.quarantine_delete(vec![entry.id])
            .unwrap()
            .failed
            .is_empty()
    );
    assert_finding(&s, &scan, FindingStatus::Deleted);
    assert!(s.quarantine_list().unwrap().is_empty());
}
