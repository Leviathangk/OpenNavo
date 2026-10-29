//! Regression fixtures use only temporary applications, SQLite, and the existing loopback TLS server.
use super::*;
use crate::{
    running_apps::{AppControl, Process},
    scheduler,
    uninstall::{
        Engine, EngineConfig,
        identity::{Identity, MetadataProbe, Receipt},
        rules::Roots,
    },
};
use std::{fs, process::Stdio};
use tokio::io::{AsyncBufReadExt, BufReader};

struct Probe;
impl MetadataProbe for Probe {
    fn identity(&self, path: &Path) -> Identity {
        let info = plist::Value::from_file(path.join("Contents/Info.plist")).unwrap();
        let dict = info.as_dictionary().unwrap();
        Identity {
            name: Some("Fixture".into()),
            bundle_id: dict
                .get("CFBundleIdentifier")
                .and_then(|v| v.as_string())
                .map(str::to_owned),
            version: Some("1.0".into()),
            ..Default::default()
        }
    }
    fn receipts(&self, _: &Path) -> Vec<Receipt> {
        vec![]
    }
}
#[derive(Default)]
struct Apps(RwLock<Vec<Process>>);
impl AppControl for Apps {
    fn list(&self) -> Result<Vec<Process>, AppError> {
        Ok(self.0.read().unwrap().clone())
    }
    fn quit(&self, _: &Process) -> Result<bool, AppError> {
        panic!("no fixture quits")
    }
    fn reopen(&self, _: &Path) -> Result<(), AppError> {
        panic!("no fixture launches")
    }
}
struct Fixture {
    _temporary: tempfile::TempDir,
    root: PathBuf,
    path: PathBuf,
    database: Database,
    settings: Arc<RwLock<Settings>>,
    service: Service,
    app: LocalApp,
    brew_version: String,
    control: Arc<Apps>,
}
impl Fixture {
    fn engine(
        root: &Path,
        database: &Database,
        settings: &Arc<RwLock<Settings>>,
        control: Arc<Apps>,
    ) -> Arc<Engine> {
        Engine::new(EngineConfig {
            database: database.clone(),
            settings: settings.clone(),
            sink: Arc::new(|_| {}),
            roots: Roots {
                home: root.into(),
                applications: vec![root.join("Applications")],
                system_library: root.join("Library"),
                bins: vec![],
                receipts: root.join("receipts"),
                temporary: vec![],
                caskrooms: vec![],
            },
            control,
            probe: Arc::new(Probe),
            icon_cache: root.join("icons"),
            askpass: root.join("askpass"),
        })
    }
    fn new(source: UpdateSource, url: &str) -> Self {
        let temporary = tempfile::tempdir().unwrap();
        let root = temporary.path().canonicalize().unwrap();
        let path = root.join("Applications/Fixture.app");
        fs::create_dir_all(path.join("Contents/Resources")).unwrap();
        let mut info = plist::Dictionary::new();
        info.insert("CFBundleIdentifier".into(), "org.fixture.App".into());
        info.insert("CFBundleVersion".into(), "100".into());
        info.insert("CFBundleShortVersionString".into(), "1.0.0".into());
        if source == UpdateSource::Sparkle {
            info.insert("SUFeedURL".into(), url.into());
            // RFC 8032 test public key: routing requires a supported signing configuration.
            info.insert(
                "SUPublicEDKey".into(),
                "11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo=".into(),
            );
        } else if source == UpdateSource::Electron {
            fs::write(
                path.join("Contents/Resources/app-update.yml"),
                format!("provider: generic\nurl: {url}\n"),
            )
            .unwrap();
        }
        plist::Value::Dictionary(info)
            .to_file_xml(path.join("Contents/Info.plist"))
            .unwrap();
        let database = Database::open(&root.join("db")).unwrap();
        let settings = Arc::new(RwLock::new(Settings {
            auto_upgrade_casks: true,
            ..Settings::default()
        }));
        let installed: InstalledItem = serde_json::from_value(serde_json::json!({
            "kind":"cask", "token":"fixture", "name":"Fixture", "installedVersion":"1.0",
            "actualVersion":"1.0", "latestVersion":"2.0", "status":"outdated", "onRequest":true,
            "requiredBy":[], "appPaths":[path], "autoUpdates":true, "pinned":false
        }))
        .unwrap();
        database
            .set_meta(
                "installed_cache",
                &serde_json::to_string(&vec![installed]).unwrap(),
            )
            .unwrap();
        let mut catalog = crate::catalog::links::tests::item();
        catalog.kind = Kind::Cask;
        catalog.token = "fixture".into();
        catalog.auto_updates = true;
        database
            .with(|c| crate::catalog::upsert(c, &catalog))
            .unwrap();
        let control = Arc::new(Apps::default());
        let engine = Self::engine(&root, &database, &settings, control.clone());
        let app = engine.list().unwrap().remove(0);
        assert_eq!(app.cask.as_deref(), Some("fixture"));
        let service =
            Service::new(database.clone(), engine, settings.clone(), Arc::new(|_| {})).unwrap();
        Self {
            _temporary: temporary,
            root,
            path,
            database,
            settings,
            service,
            app,
            brew_version: "2.0".into(),
            control,
        }
    }
    fn brew(&self, kind: Kind, token: &str) -> OutdatedItem {
        serde_json::from_value(serde_json::json!({
            "source":"homebrew", "kind":kind, "token":token, "name":"Fixture", "installedVersion":"1.0",
            "currentVersion":self.brew_version, "autoUpdates":true, "pinned":false, "ignored":false,
            "dependents":0, "major":false, "requiresAdmin":false, "running":false,
            "relaunchPending":false, "criticalUpdate":false
        })).unwrap()
    }
    fn cache_candidate(&self, source: UpdateSource) {
        let mut item = self.brew(
            Kind::Cask,
            self.app.catalog_token.as_deref().unwrap_or("fixture"),
        );
        item.source = source;
        item.app_id = Some(self.app.id.clone());
        item.bundle_id = self.app.bundle_id.clone();
        let candidate = Candidate {
            item,
            app_path: self.path.clone(),
            snapshot: Snapshot::capture(&self.path).unwrap(),
            feed_item: None,
            public_key: None,
            artifact: None,
            expected_team: None,
            checked_at: 0,
        };
        self.database
            .set_meta(
                "native_update_candidates",
                &serde_json::to_string(&vec![candidate]).unwrap(),
            )
            .unwrap();
    }
    fn skip(&self, version: Option<&str>) {
        self.service
            .skip(&format!("app:{}", self.app.id), version)
            .unwrap();
    }
    fn merged(&self) -> Vec<OutdatedItem> {
        self.service
            .merge(vec![self.brew(Kind::Cask, "fixture")])
            .unwrap()
    }
    fn assert_hidden(&self) {
        let merged = self.merged();
        assert!(
            scheduler::eligible(&merged, &self.settings.read().unwrap()).is_empty(),
            "skipped native update entered scheduler: {merged:?}"
        );
        assert!(
            merged.is_empty(),
            "skipped native update fell back to Homebrew: {merged:?}"
        );
    }
    fn restart(&mut self) {
        // Recreate the app engine as well: no in-memory candidate or route survives.
        let engine = Self::engine(
            &self.root,
            &self.database,
            &self.settings,
            self.control.clone(),
        );
        self.service = Service::new(
            self.database.clone(),
            engine,
            self.settings.clone(),
            Arc::new(|_| {}),
        )
        .unwrap();
    }
    async fn refresh(&self, source: UpdateSource) {
        self.refresh_mode(source, true).await;
    }
    async fn refresh_mode(&self, source: UpdateSource, manual: bool) {
        let apps = self.service.apps.list().unwrap();
        assert_eq!(apps[0].updater, Some(source));
        let mut candidates = self.service.candidates().unwrap();
        let outcome = self
            .service
            .check_native_with_rules(&[source], &apps, &mut candidates, manual, Ok(vec![]))
            .await
            .unwrap();
        outcome.result().unwrap();
        assert!(outcome.failed_sources().is_empty());
        self.database
            .set_meta(
                "native_update_candidates",
                &serde_json::to_string(&candidates).unwrap(),
            )
            .unwrap();
    }
}

#[tokio::test]
async fn startup_rechecks_remounted_candidates_without_trusting_old_snapshots() {
    let source = UpdateSource::Sparkle;
    let (_feeds, mut process, url, client) = server(&feed(source, "200.0.0")).await;
    let mut fixture = Fixture::new(source, &url);
    fixture.service.client = client.clone();
    fixture.refresh(source).await;
    assert!(!fixture.service.needs_startup_check().unwrap());

    // Model the persisted pre-reboot device numbers, including ancestor devices.
    fixture
        .service
        .update_candidates(|candidates| {
            for candidate in candidates {
                candidate.snapshot.device += 1;
                for (_, device, _) in &mut candidate.snapshot.parents {
                    *device += 1;
                }
            }
            Ok(())
        })
        .unwrap();
    fixture.restart();
    assert!(fixture.service.needs_startup_check().unwrap());
    assert!(
        fixture.merged().is_empty(),
        "stale snapshots must still fail closed"
    );
    assert!(
        !fixture.service.candidates().unwrap()[0]
            .snapshot
            .still(&fixture.path)
    );

    fixture.service.client = client;
    fixture.refresh_mode(source, false).await;
    assert!(!fixture.service.needs_startup_check().unwrap());
    assert!(
        fixture.service.candidates().unwrap()[0]
            .snapshot
            .still(&fixture.path)
    );
    let merged = fixture
        .service
        .merge(vec![fixture.brew(Kind::Cask, "unrelated")])
        .unwrap();
    assert!(
        merged
            .iter()
            .any(|item| item.source == UpdateSource::Homebrew)
    );
    assert!(merged.iter().any(|item| item.source == source));
    process.kill().await.unwrap();
    process.wait().await.unwrap();
}

#[test]
fn startup_recovery_respects_preferences_and_pending_relaunches() {
    let source = UpdateSource::Sparkle;
    let fixture = Fixture::new(source, "https://example.org/feed.xml");
    assert!(!fixture.service.needs_startup_check().unwrap());
    fixture.cache_candidate(source);
    assert!(!fixture.service.needs_startup_check().unwrap());
    fixture
        .service
        .update_candidates(|candidates| {
            candidates[0].snapshot.device += 1;
            Ok(())
        })
        .unwrap();
    assert!(fixture.service.needs_startup_check().unwrap());
    fixture.settings.write().unwrap().auto_check = false;
    assert!(!fixture.service.needs_startup_check().unwrap());
    fixture.settings.write().unwrap().auto_check = true;
    fixture
        .settings
        .write()
        .unwrap()
        .update_sources
        .0
        .insert(source, false);
    assert!(!fixture.service.needs_startup_check().unwrap());
    fixture
        .settings
        .write()
        .unwrap()
        .update_sources
        .0
        .insert(source, true);
    fixture
        .service
        .update_candidates(|candidates| {
            candidates[0].item.relaunch_pending = true;
            Ok(())
        })
        .unwrap();
    assert!(!fixture.service.needs_startup_check().unwrap());
}

async fn server(
    body: &str,
) -> (
    tempfile::TempDir,
    tokio::process::Child,
    String,
    reqwest::Client,
) {
    server_mode(body, "normal").await
}

async fn server_mode(
    body: &str,
    mode: &str,
) -> (
    tempfile::TempDir,
    tokio::process::Child,
    String,
    reqwest::Client,
) {
    let temporary = tempfile::tempdir().unwrap();
    let payload = temporary.path().join("feed");
    fs::write(&payload, body).unwrap();
    let fixtures = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("tests/fixtures/updater");
    let mut process = tokio::process::Command::new("/usr/bin/python3")
        .args(["-I", "-u"])
        .arg(fixtures.join("https.py"))
        .arg(&payload)
        .arg(mode)
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .kill_on_drop(true)
        .spawn()
        .unwrap();
    let mut line = String::new();
    tokio::time::timeout(
        Duration::from_secs(5),
        BufReader::new(process.stdout.take().unwrap()).read_line(&mut line),
    )
    .await
    .unwrap()
    .unwrap();
    let port: u16 = line.trim().parse().unwrap();
    let cert = reqwest::Certificate::from_pem(
        &fs::read(PathBuf::from(format!("{}.cert.pem", payload.display()))).unwrap(),
    )
    .unwrap();
    let client = reqwest::Client::builder()
        .no_proxy()
        .tls_certs_only([cert])
        .timeout(Duration::from_secs(5))
        .build()
        .unwrap();
    (
        temporary,
        process,
        format!("https://127.0.0.1:{port}/"),
        client,
    )
}
fn feed(source: UpdateSource, version: &str) -> String {
    if source == UpdateSource::Sparkle {
        format!(
            r#"<rss xmlns:s="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><s:version>{version}</s:version><s:shortVersionString>{version}</s:shortVersionString><enclosure url="https://fixture.example/App.zip" s:edSignature="AA=="/></item></channel></rss>"#
        )
    } else {
        use base64::{Engine as _, engine::general_purpose::STANDARD};
        format!(
            "version: {version}\nfiles:\n  - url: App.zip\n    sha512: {}\n",
            STANDARD.encode([1; 64])
        )
    }
}
async fn skipped_native_survives_refresh_and_restart(source: UpdateSource) {
    // Sparkle build and marketing versions are both greater than the installed build 100.
    let (_feeds, mut process, url, client) = server(&feed(source, "200.0.0")).await;
    let mut fixture = Fixture::new(source, &url);
    fixture.brew_version = "200.0.0".into();
    fixture.service.client = client.clone();
    fixture.refresh(source).await;
    assert_eq!(
        fixture
            .merged()
            .iter()
            .map(|i| i.source)
            .collect::<Vec<_>>(),
        [source]
    );
    fixture.skip(Some("200.0.0"));
    fixture.assert_hidden();
    fixture.refresh(source).await;
    assert!(
        fixture.service.candidates().unwrap().is_empty(),
        "real source check must remove the skipped candidate"
    );
    fixture.assert_hidden();
    fixture.restart();
    fixture.assert_hidden();
    fixture.service.client = client;
    fixture.skip(None);
    fixture.refresh(source).await;
    assert_eq!(
        fixture
            .merged()
            .iter()
            .map(|i| i.source)
            .collect::<Vec<_>>(),
        [source]
    );
    fixture.skip(Some("200.0.0"));
    process.kill().await.unwrap();
    process.wait().await.unwrap();
    let (_new_feed, mut process, url, client) = server(&feed(source, "201.0.0")).await;
    if source == UpdateSource::Sparkle {
        let mut info = plist::Value::from_file(fixture.path.join("Contents/Info.plist")).unwrap();
        info.as_dictionary_mut()
            .unwrap()
            .insert("SUFeedURL".into(), url.into());
        info.to_file_xml(fixture.path.join("Contents/Info.plist"))
            .unwrap();
    } else {
        fs::write(
            fixture.path.join("Contents/Resources/app-update.yml"),
            format!("provider: generic\nurl: {url}\n"),
        )
        .unwrap();
    }
    fixture
        .service
        .apps
        .refresh(LocalAppsChangedReason::Refresh)
        .unwrap();
    fixture.service.client = client;
    fixture.brew_version = "201.0.0".into();
    fixture.refresh(source).await;
    let merged = fixture.merged();
    assert_eq!(merged.len(), 1);
    assert_eq!(
        (merged[0].source, merged[0].current_version.as_str()),
        (source, "201.0.0")
    );
    assert_eq!(merged[0].skipped_version.as_deref(), Some("200.0.0"));
    assert!(scheduler::eligible(&merged, &fixture.settings.read().unwrap()).is_empty());
    process.kill().await.unwrap();
    process.wait().await.unwrap();
}
#[tokio::test]
async fn sparkle_skip_survives_empty_refresh_restart_and_unskip() {
    skipped_native_survives_refresh_and_restart(UpdateSource::Sparkle).await;
}
#[tokio::test]
async fn electron_skip_survives_empty_refresh_restart_and_unskip() {
    skipped_native_survives_refresh_and_restart(UpdateSource::Electron).await;
}
#[test]
fn routing_deduplication_keeps_other_apps_and_same_named_formula() {
    for source in [UpdateSource::Sparkle, UpdateSource::Electron] {
        let fixture = Fixture::new(source, "https://fixture.example/releases/");
        fixture.cache_candidate(source);
        let merged = fixture
            .service
            .merge(vec![
                fixture.brew(Kind::Cask, "fixture"),
                fixture.brew(Kind::Formula, "fixture"),
                fixture.brew(Kind::Cask, "other"),
            ])
            .unwrap();
        assert_eq!(merged.len(), 3);
        assert!(merged.iter().any(|i| i.source == source));
        assert!(merged.iter().any(|i| i.source == UpdateSource::Homebrew
            && i.kind == Kind::Formula
            && i.token == "fixture"));
        assert!(merged.iter().any(|i| i.source == UpdateSource::Homebrew
            && i.kind == Kind::Cask
            && i.token == "other"));
        fixture.skip(Some("2.0"));
        assert!(fixture.service.candidates().unwrap().len() == 1);
        fixture
            .settings
            .write()
            .unwrap()
            .update_sources
            .0
            .insert(source, false);
        let merged = fixture.merged();
        assert_eq!(merged.len(), 1);
        assert_eq!(merged[0].source, UpdateSource::Homebrew);
        assert_eq!(
            scheduler::eligible(&merged, &fixture.settings.read().unwrap()).len(),
            1
        );
    }
}
#[test]
fn skipped_route_does_not_hide_brew_after_uninstall_or_identity_replacement() {
    for replacement in ["missing", "directory", "metadata"] {
        let fixture = Fixture::new(UpdateSource::Electron, "https://fixture.example/releases/");
        fixture.cache_candidate(UpdateSource::Electron);
        fixture.skip(Some("2.0"));
        if replacement != "metadata" {
            fs::rename(&fixture.path, fixture.root.join("previous.app")).unwrap();
        }
        if replacement != "missing" {
            fs::create_dir_all(fixture.path.join("Contents")).unwrap();
            let mut info = plist::Dictionary::new();
            info.insert("CFBundleIdentifier".into(), "org.other.App".into());
            plist::Value::Dictionary(info)
                .to_file_xml(fixture.path.join("Contents/Info.plist"))
                .unwrap();
        }
        let merged = fixture.merged();
        assert_eq!(merged.len(), 1);
        assert_eq!(merged[0].source, UpdateSource::Homebrew);
    }
}
#[test]
fn homebrew_owned_app_with_native_configuration_keeps_brew_candidate() {
    let fixture = Fixture::new(UpdateSource::Electron, "https://fixture.example/releases/");
    let mut catalog = crate::catalog::get(&fixture.database, Kind::Cask, "fixture")
        .unwrap()
        .unwrap();
    catalog.auto_updates = false;
    fixture
        .database
        .with(|c| crate::catalog::upsert(c, &catalog))
        .unwrap();
    fixture.skip(Some("2.0"));
    assert_eq!(
        fixture.service.apps.list().unwrap()[0].updater,
        Some(UpdateSource::Homebrew)
    );
    assert_eq!(fixture.merged()[0].source, UpdateSource::Homebrew);
}

#[test]
fn disabled_source_falls_back_to_another_native_route_before_brew() {
    let fixture = Fixture::new(UpdateSource::Sparkle, "https://fixture.example/releases/");
    fs::write(
        fixture.path.join("Contents/Resources/app-update.yml"),
        "provider: generic\nurl: https://fixture.example/releases/\n",
    )
    .unwrap();
    fixture
        .service
        .apps
        .refresh(LocalAppsChangedReason::Refresh)
        .unwrap();
    fixture.cache_candidate(UpdateSource::Sparkle);
    fixture.skip(Some("2.0"));
    fixture
        .settings
        .write()
        .unwrap()
        .update_sources
        .0
        .insert(UpdateSource::Sparkle, false);
    assert_eq!(
        fixture.service.apps.list().unwrap()[0].updater,
        Some(UpdateSource::Electron)
    );
    fixture.assert_hidden();
    fixture
        .settings
        .write()
        .unwrap()
        .update_sources
        .0
        .insert(UpdateSource::Electron, false);
    assert_eq!(fixture.merged()[0].source, UpdateSource::Homebrew);
    fixture
        .settings
        .write()
        .unwrap()
        .update_sources
        .0
        .insert(UpdateSource::Sparkle, true);
    fixture.assert_hidden();
}
#[test]
fn direct_app_navigation_token_is_not_a_homebrew_installation_identity() {
    let fixture = Fixture::new(UpdateSource::Sparkle, "https://fixture.example/releases/");
    fixture.database.set_meta("installed_cache", "[]").unwrap();
    fixture
        .service
        .apps
        .refresh(LocalAppsChangedReason::Refresh)
        .unwrap();
    {
        let mut apps = fixture.service.apps.apps.lock().unwrap();
        let app = &mut apps.get_mut(&fixture.app.id).unwrap().app;
        assert!(app.cask.is_none());
        app.catalog_token = Some("fixture".into());
    }
    fixture.cache_candidate(UpdateSource::Sparkle);
    let merged = fixture.merged();
    assert_eq!(merged.len(), 2);
    assert!(merged.iter().any(|i| i.source == UpdateSource::Homebrew));
    fixture.skip(Some("2.0"));
    assert_eq!(fixture.merged()[0].source, UpdateSource::Homebrew);
}

impl Fixture {
    fn alias(source: UpdateSource) -> Self {
        let mut fixture = Self::new(source, "https://fixture.example/releases/");
        let official_path = fixture.root.join("Applications/Plotly.app");
        fs::create_dir_all(official_path.join("Contents/Resources")).unwrap();
        let mut info = plist::Value::from_file(fixture.path.join("Contents/Info.plist")).unwrap();
        info.as_dictionary_mut()
            .unwrap()
            .insert("CFBundleIdentifier".into(), "org.fixture.Official".into());
        info.to_file_xml(official_path.join("Contents/Info.plist"))
            .unwrap();
        if source == UpdateSource::Electron {
            fs::copy(
                fixture.path.join("Contents/Resources/app-update.yml"),
                official_path.join("Contents/Resources/app-update.yml"),
            )
            .unwrap();
        }
        let mut official = crate::catalog::get(&fixture.database, Kind::Cask, "fixture")
            .unwrap()
            .unwrap();
        official.token = "orca".into();
        official.name = "Plotly Orca".into();
        official.display_name =
            serde_json::from_value(serde_json::json!({"en-US":"Plotly Orca"})).unwrap();
        official.auto_updates = false;
        let mut alias = official.clone();
        alias.token = "stablyai-orca".into();
        alias.name = "Stably Orca".into();
        alias.display_name =
            serde_json::from_value(serde_json::json!({"en-US":"Stably Orca"})).unwrap();
        alias.tap = Some("stablyai/orca".into());
        alias.brew_token = Some("orca".into());
        alias.auto_updates = true;
        fixture
            .database
            .with(|c| {
                crate::catalog::upsert(c, &official)?;
                crate::catalog::upsert(c, &alias)
            })
            .unwrap();
        let raw = serde_json::json!({"casks": [
            {"token":"orca", "tap":"stablyai/orca", "version":"1.0", "installed":"1.0", "auto_updates":true,
             "artifacts":[{"app":["Fixture.app", {"target": fixture.path}]}]},
            {"token":"orca", "tap":"homebrew/cask", "version":"1.0", "installed":"1.0", "auto_updates":false,
             "artifacts":[{"app":["Plotly.app", {"target": official_path}]}]}
        ]});
        let info = serde_json::from_value(raw.clone()).unwrap();
        let installed = crate::library::scan(
            &fixture.database,
            &info,
            fixture.root.to_str().unwrap(),
            Locale::EnUs,
        )
        .unwrap()
        .items;
        fixture
            .database
            .set_meta(
                "installed_cache",
                &serde_json::to_string(&installed).unwrap(),
            )
            .unwrap();
        fixture
            .database
            .set_meta("cask_rules_cache", &raw.to_string())
            .unwrap();
        let apps = fixture
            .service
            .apps
            .refresh(LocalAppsChangedReason::Refresh)
            .unwrap();
        fixture.app = apps
            .into_iter()
            .find(|app| app.path == fixture.path.to_str().unwrap())
            .unwrap();
        fixture
    }
    fn alias_brew(&self) -> Vec<OutdatedItem> {
        let mut installed: Vec<InstalledItem> =
            serde_json::from_str(&self.database.meta("installed_cache").unwrap().unwrap()).unwrap();
        let outdated = serde_json::from_value(serde_json::json!({"casks":[
            {"name":"stablyai/orca/orca", "installed_versions":["1.0"], "current_version":"2.0"},
            {"name":"homebrew/cask/orca", "installed_versions":["1.0"], "current_version":"2.0"}
        ]}))
        .unwrap();
        let brew =
            crate::updates::build(&self.database, &outdated, &mut installed, Locale::EnUs, 100)
                .unwrap();
        assert_eq!(brew.len(), 2);
        assert!(brew.iter().any(|item| item.token == "stablyai-orca"));
        assert!(brew.iter().any(|item| item.token == "orca"));
        brew
    }
    fn source_enabled(&self, source: UpdateSource, enabled: bool) {
        self.settings
            .write()
            .unwrap()
            .update_sources
            .0
            .insert(source, enabled);
    }
    fn auto_updates(&self, enabled: bool) {
        let mut catalog = crate::catalog::get(&self.database, Kind::Cask, "fixture")
            .unwrap()
            .unwrap();
        catalog.auto_updates = enabled;
        self.database
            .with(|c| crate::catalog::upsert(c, &catalog))
            .unwrap();
    }
    fn assert_brew_owns_cached_native(&self, previous: UpdateSource) {
        assert!(
            self.service.list().unwrap().is_empty(),
            "old native candidate survived route change"
        );
        let merged = self.merged();
        assert_eq!(merged.len(), 1);
        assert_eq!(merged[0].source, UpdateSource::Homebrew);
        assert_eq!(
            scheduler::eligible(&merged, &self.settings.read().unwrap()).len(),
            1
        );
        assert!(
            self.service.candidate(&self.app.id, previous).is_err(),
            "stale source remains executable"
        );
        assert_eq!(
            self.service.candidates().unwrap().len(),
            1,
            "route filtering must not erase persisted state"
        );
    }
    fn running(&self, on: bool) {
        *self.control.0.write().unwrap() = if on {
            vec![Process {
                pid: 42,
                path: self.path.clone(),
                bundle_id: self.app.bundle_id.clone().unwrap(),
            }]
        } else {
            vec![]
        };
    }
    fn pending(&self, source: UpdateSource) {
        self.cache_candidate(source);
        let mut candidates = self.service.candidates().unwrap();
        let c = &mut candidates[0];
        c.item.relaunch_pending = true;
        c.item.current_version = "2.0.0".into();
        if source == UpdateSource::Electron {
            c.artifact = Some(super::super::release::Artifact {
                version: "2.0.0".into(),
                version_key: "CFBundleShortVersionString".into(),
                file: super::super::release::File {
                    name: "App.zip".into(),
                    url: "https://fixture.example/App.zip".into(),
                    size: None,
                    architecture: None,
                    sha256: None,
                    sha512: None,
                },
            });
        }
        self.database
            .set_meta(
                "native_update_candidates",
                &serde_json::to_string(&candidates).unwrap(),
            )
            .unwrap();
    }
}

#[test]
fn alias_and_official_cask_keep_separate_display_detail_and_auto_update_routes() {
    for source in [UpdateSource::Sparkle, UpdateSource::Electron] {
        let fixture = Fixture::alias(source);
        let apps = fixture.service.apps.list().unwrap();
        assert_eq!(apps.len(), 2);
        for (identity, token, name, route) in [
            ("stablyai/orca/orca", "stablyai-orca", "Stably Orca", source),
            (
                "homebrew/cask/orca",
                "orca",
                "Plotly Orca",
                UpdateSource::Homebrew,
            ),
        ] {
            let app = apps
                .iter()
                .find(|app| app.cask.as_deref() == Some(identity))
                .unwrap();
            assert_eq!(app.catalog_token.as_deref(), Some(token));
            let catalog = crate::catalog::get(
                &fixture.database,
                Kind::Cask,
                app.catalog_token.as_deref().unwrap(),
            )
            .unwrap();
            assert_eq!(
                crate::library::display_name(catalog.as_ref(), &app.name, Locale::EnUs),
                name
            );
            assert_eq!(
                fixture.service.catalog_identity(app, None).unwrap(),
                (Kind::Cask, token.into())
            );
            assert_eq!(app.updater, Some(route));
            assert_eq!(
                fixture.service.apps.local_rule(identity).unwrap()["tap"],
                identity.rsplit_once('/').unwrap().0
            );
        }
        // Opposite flags prove that neither route inherited the other catalog entry's policy.
        let entries: Vec<_> = ["orca", "stablyai-orca"]
            .into_iter()
            .map(|token| {
                crate::catalog::get(&fixture.database, Kind::Cask, token)
                    .unwrap()
                    .unwrap()
            })
            .collect();
        fixture
            .database
            .with(|c| {
                for mut item in entries {
                    item.auto_updates = !item.auto_updates;
                    crate::catalog::upsert(c, &item)?;
                }
                Ok(())
            })
            .unwrap();
        let apps = fixture.service.apps.list().unwrap();
        assert_eq!(
            apps.iter()
                .find(|a| a.catalog_token.as_deref() == Some("stablyai-orca"))
                .unwrap()
                .updater,
            Some(UpdateSource::Homebrew)
        );
        assert_eq!(
            apps.iter()
                .find(|a| a.catalog_token.as_deref() == Some("orca"))
                .unwrap()
                .updater,
            Some(source)
        );
    }
}

#[test]
fn alias_native_ownership_suppresses_only_its_homebrew_update_even_after_restart() {
    for source in [UpdateSource::Sparkle, UpdateSource::Electron] {
        let mut fixture = Fixture::alias(source);
        let merged = fixture.service.merge(fixture.alias_brew()).unwrap();
        assert_eq!(
            merged.len(),
            1,
            "ownership must survive an empty native check"
        );
        assert_eq!(merged[0].token, "orca");
        fixture.cache_candidate(source);
        for restart in [false, true] {
            if restart {
                fixture.restart();
            }
            let merged = fixture.service.merge(fixture.alias_brew()).unwrap();
            assert_eq!(merged.len(), 2);
            assert!(
                merged
                    .iter()
                    .any(|i| i.source == source && i.token == "stablyai-orca")
            );
            assert!(
                merged
                    .iter()
                    .any(|i| i.source == UpdateSource::Homebrew && i.token == "orca")
            );
        }
        fixture.skip(Some("2.0"));
        assert_eq!(
            fixture.service.merge(fixture.alias_brew()).unwrap().len(),
            1
        );
    }
}

#[test]
fn alias_awaiting_restart_suppresses_homebrew_after_route_changes_and_restart() {
    for source in [UpdateSource::Sparkle, UpdateSource::Electron] {
        let mut fixture = Fixture::alias(source);
        fixture.pending(source);
        fixture.running(true);
        fixture.source_enabled(source, false);
        for restart in [false, true] {
            if restart {
                fixture.restart();
            }
            assert_eq!(
                fixture
                    .service
                    .apps
                    .list()
                    .unwrap()
                    .iter()
                    .find(|a| a.id == fixture.app.id)
                    .unwrap()
                    .updater,
                Some(UpdateSource::Homebrew)
            );
            let merged = fixture.service.merge(fixture.alias_brew()).unwrap();
            assert_eq!(merged.len(), 2);
            assert!(
                merged.iter().any(|i| i.source == source
                    && i.token == "stablyai-orca"
                    && i.relaunch_pending)
            );
            assert!(
                merged
                    .iter()
                    .any(|i| i.source == UpdateSource::Homebrew && i.token == "orca")
            );
        }
    }
}

#[tokio::test]
async fn cached_native_is_retired_when_homebrew_is_reenabled() {
    for source in [UpdateSource::Sparkle, UpdateSource::Electron] {
        let (_feed, mut process, url, client) = server(&feed(source, "200.0.0")).await;
        let mut fixture = Fixture::new(source, &url);
        fixture.service.client = client;
        fixture.auto_updates(false);
        fixture.source_enabled(UpdateSource::Homebrew, false);
        fixture.refresh(source).await;
        assert_eq!(fixture.service.list().unwrap()[0].source, source);
        assert!(fixture.service.candidate(&fixture.app.id, source).is_ok());
        fixture.source_enabled(UpdateSource::Homebrew, true);
        fixture.assert_brew_owns_cached_native(source);
        fixture.source_enabled(UpdateSource::Homebrew, false);
        assert_eq!(fixture.service.list().unwrap()[0].source, source);
        process.kill().await.unwrap();
        process.wait().await.unwrap();
    }
}

#[test]
fn cached_native_is_retired_when_catalog_switches_to_homebrew() {
    for source in [UpdateSource::Sparkle, UpdateSource::Electron] {
        let fixture = Fixture::new(source, "https://fixture.example/releases/");
        fixture.cache_candidate(source);
        assert_eq!(fixture.service.list().unwrap()[0].source, source);
        fixture.auto_updates(false);
        fixture.assert_brew_owns_cached_native(source);
    }
}

#[test]
fn cached_native_is_retired_when_its_curated_rule_is_withdrawn() {
    let fixture = Fixture::new(UpdateSource::Vendor, "https://fixture.example/releases/");
    let rules = serde_json::json!({"items": [{"bundleId":"org.fixture.App", "source":"vendor", "feedUrl":"https://fixture.example/feed", "teamId":"TESTTEAM01", "channel":"stable", "token":"fixture", "kind":"cask", "release":null}]});
    fixture
        .database
        .set_meta(
            "update_feed_rules",
            &serde_json::to_string(&CachedFeed {
                url: "https://fixture.example/catalog/update-feeds".into(),
                etag: None,
                body: rules.to_string(),
            })
            .unwrap(),
        )
        .unwrap();
    assert_eq!(cached_rules(&fixture.database).len(), 1);
    assert_eq!(
        fixture.service.apps.list().unwrap()[0].updater,
        Some(UpdateSource::Vendor)
    );
    fixture.cache_candidate(UpdateSource::Vendor);
    let mut candidates = fixture.service.candidates().unwrap();
    candidates[0].expected_team = Some("TESTTEAM01".into());
    fixture
        .database
        .set_meta(
            "native_update_candidates",
            &serde_json::to_string(&candidates).unwrap(),
        )
        .unwrap();
    assert_eq!(
        fixture.service.list().unwrap()[0].source,
        UpdateSource::Vendor
    );
    fixture
        .database
        .set_meta(
            "update_feed_rules",
            &serde_json::to_string(&CachedFeed {
                url: "https://fixture.example/catalog/update-feeds".into(),
                etag: None,
                body: r#"{"items":[]}"#.into(),
            })
            .unwrap(),
        )
        .unwrap();
    fixture.assert_brew_owns_cached_native(UpdateSource::Vendor);
}

#[test]
fn cached_lower_priority_native_cannot_be_displayed_or_executed() {
    let fixture = Fixture::new(UpdateSource::Sparkle, "https://fixture.example/releases/");
    fs::write(
        fixture.path.join("Contents/Resources/app-update.yml"),
        "provider: generic\nurl: https://fixture.example/releases/\n",
    )
    .unwrap();
    fixture
        .service
        .apps
        .refresh(LocalAppsChangedReason::Refresh)
        .unwrap();
    fixture.source_enabled(UpdateSource::Sparkle, false);
    fixture.cache_candidate(UpdateSource::Electron);
    assert_eq!(
        fixture.service.list().unwrap()[0].source,
        UpdateSource::Electron
    );
    fixture.source_enabled(UpdateSource::Sparkle, true);
    assert!(fixture.service.list().unwrap().is_empty());
    assert!(fixture.merged().is_empty());
    assert!(
        fixture
            .service
            .candidate(&fixture.app.id, UpdateSource::Electron)
            .is_err()
    );
    fixture.cache_candidate(UpdateSource::Sparkle);
    assert_eq!(fixture.merged()[0].source, UpdateSource::Sparkle);
}

#[tokio::test]
async fn pending_restart_survives_route_changes_swap_and_refresh_without_reinstalling() {
    for source in [UpdateSource::Sparkle, UpdateSource::Electron] {
        let fixture = Fixture::new(source, "https://fixture.example/releases/");
        fixture.pending(source);
        fixture.running(true);
        // A committed swap replaces the directory while the old process is still running.
        fs::rename(&fixture.path, fixture.root.join("old.app")).unwrap();
        fs::create_dir_all(fixture.path.join("Contents")).unwrap();
        let mut info =
            plist::Value::from_file(fixture.root.join("old.app/Contents/Info.plist")).unwrap();
        info.as_dictionary_mut()
            .unwrap()
            .insert("CFBundleShortVersionString".into(), "2.0.0".into());
        info.to_file_xml(fixture.path.join("Contents/Info.plist"))
            .unwrap();
        fixture.service.installed(&fixture.app.id, true).unwrap();
        fixture.auto_updates(false);
        fixture.source_enabled(source, false);
        let merged = fixture.merged();
        assert_eq!(merged.len(), 1);
        assert_eq!(merged[0].source, source);
        assert!(merged[0].relaunch_pending);
        assert!(scheduler::eligible(&merged, &fixture.settings.read().unwrap()).is_empty());
        assert!(fixture.service.candidate(&fixture.app.id, source).is_err());
        let apps = fixture.service.apps.list().unwrap();
        let mut candidates = fixture.service.candidates().unwrap();
        fixture
            .service
            .check_native_with_rules(&[source], &apps, &mut candidates, true, Ok(vec![]))
            .await
            .unwrap();
        assert_eq!(candidates.len(), 1);
        assert!(candidates[0].item.relaunch_pending);
        fixture
            .database
            .set_meta(
                "native_update_candidates",
                &serde_json::to_string(&candidates).unwrap(),
            )
            .unwrap();
        fixture.running(false);
        assert!(fixture.service.list().unwrap().is_empty());
        assert!(fixture.service.candidate(&fixture.app.id, source).is_err());
        assert_eq!(fixture.merged()[0].source, UpdateSource::Homebrew);
        fixture.running(true);
        assert_eq!(fixture.merged()[0].source, UpdateSource::Homebrew);
        assert!(fixture.service.list().unwrap().is_empty());
        assert!(fixture.service.candidates().unwrap().is_empty());
    }
}

#[test]
fn stopped_pending_download_only_retries_with_current_route_and_snapshot() {
    let fixture = Fixture::new(UpdateSource::Electron, "https://fixture.example/releases/");
    fixture.pending(UpdateSource::Electron);
    assert!(!fixture.service.list().unwrap()[0].relaunch_pending);
    assert!(
        !fixture
            .service
            .candidate(&fixture.app.id, UpdateSource::Electron)
            .unwrap()
            .item
            .relaunch_pending
    );
    fixture.auto_updates(false);
    fixture.assert_brew_owns_cached_native(UpdateSource::Electron);
    fixture.running(true);
    fixture.assert_brew_owns_cached_native(UpdateSource::Electron);
    assert!(
        !fixture.service.candidates().unwrap()[0]
            .item
            .relaunch_pending
    );
    fixture.running(false);
    fixture.auto_updates(true);
    fs::rename(&fixture.path, fixture.root.join("old.app")).unwrap();
    fs::create_dir_all(fixture.path.join("Contents/Resources")).unwrap();
    fs::copy(
        fixture.root.join("old.app/Contents/Info.plist"),
        fixture.path.join("Contents/Info.plist"),
    )
    .unwrap();
    fs::copy(
        fixture
            .root
            .join("old.app/Contents/Resources/app-update.yml"),
        fixture.path.join("Contents/Resources/app-update.yml"),
    )
    .unwrap();
    fixture
        .service
        .apps
        .refresh(LocalAppsChangedReason::Refresh)
        .unwrap();
    assert!(fixture.service.list().unwrap().is_empty());
    assert!(
        fixture
            .service
            .candidate(&fixture.app.id, UpdateSource::Electron)
            .is_err()
    );
}

#[tokio::test]
async fn retained_pending_does_not_hide_requested_source_failure() {
    let fixture = Fixture::new(UpdateSource::Sparkle, "https://fixture.example/releases/");
    fixture.pending(UpdateSource::Sparkle);
    fixture.running(true);
    let apps = fixture.service.apps.list().unwrap();
    let mut candidates = fixture.service.candidates().unwrap();
    let outcome = fixture
        .service
        .check_native_with_rules(
            &[UpdateSource::Vendor],
            &apps,
            &mut candidates,
            true,
            Err(AppError::new("E_UPDATE_FEED", "rules_unavailable")),
        )
        .await
        .unwrap();
    assert!(outcome.failed_sources().is_empty());
    assert_eq!(outcome.details.len(), 1);
    assert_eq!(
        outcome.details[0].reason,
        UpdateCheckFailureReason::RulesUnavailable
    );
    assert!(
        outcome.result().is_err(),
        "retention is not a successful check"
    );
    assert_eq!(candidates.len(), 1);
    assert!(candidates[0].item.relaunch_pending);
}

#[test]
fn stopped_pending_observation_does_not_overwrite_a_newer_cached_candidate() {
    for source in [UpdateSource::Sparkle, UpdateSource::Electron] {
        let fixture = Fixture::new(source, "https://fixture.example/releases/");
        fixture.pending(source);
        let original = fixture.service.candidates().unwrap().remove(0);
        let mut newer = original.clone();
        newer.checked_at += 1;
        newer.item.current_version = "3.0.0".into();
        let persisted = serde_json::to_string(&vec![newer]).unwrap();
        fixture
            .database
            .set_meta("native_update_candidates", &persisted)
            .unwrap();
        let settings = fixture.settings.read().unwrap().clone();
        let apps = fixture.service.routed_apps(&settings).unwrap();
        assert!(
            fixture
                .service
                .current_candidate(original, &settings, &apps, &[])
                .unwrap()
                .is_none()
        );
        assert_eq!(
            fixture.database.meta("native_update_candidates").unwrap(),
            Some(persisted)
        );
    }
}

#[tokio::test]
async fn in_flight_check_cannot_resurrect_pending_after_stop_and_start() {
    for source in [UpdateSource::Sparkle, UpdateSource::Electron] {
        let fixture = Fixture::new(source, "https://fixture.example/releases/");
        fixture.pending(source);
        fixture.running(true);
        fixture.auto_updates(false);
        fixture.source_enabled(source, false);
        let apps = fixture.service.apps.list().unwrap();
        // Service::check reads candidates before awaiting its rules request.
        let mut candidates = fixture.service.candidates().unwrap();
        let initial = candidates.clone();
        fixture.running(false);
        assert_eq!(fixture.merged()[0].source, UpdateSource::Homebrew);
        fixture.running(true);
        fixture
            .service
            .check_native_with_rules(
                &[UpdateSource::Vendor],
                &apps,
                &mut candidates,
                true,
                Err(AppError::new("E_UPDATE_FEED", "rules_unavailable")),
            )
            .await
            .unwrap();
        // Resume the same check's commit after the process lifecycle changed.
        fixture
            .service
            .persist_checked_candidates(candidates, &initial)
            .unwrap();
        assert_eq!(fixture.merged()[0].source, UpdateSource::Homebrew);
        assert!(
            fixture
                .service
                .candidates()
                .unwrap()
                .iter()
                .all(|c| !c.item.relaunch_pending)
        );
        if source == UpdateSource::Electron {
            assert_eq!(
                fixture.service.candidates().unwrap().len(),
                1,
                "retain the normalized retry"
            );
        }
    }
}

#[test]
fn newly_discovered_electron_pending_is_persisted() {
    use base64::{Engine as _, engine::general_purpose::STANDARD};
    use sha2::{Digest, Sha512};
    for previously_cached in [false, true] {
        let fixture = Fixture::new(UpdateSource::Electron, "https://fixture.example/releases/");
        fixture.pending(UpdateSource::Electron);
        let mut discovered = fixture.service.candidates().unwrap().remove(0);
        let bytes = b"fixture downloaded archive";
        let artifact = discovered.artifact.as_mut().unwrap();
        artifact.file.sha512 = Some(STANDARD.encode(Sha512::digest(bytes)));
        let cache = fixture.root.join("cache");
        let pending = cache.join("fixture-updater/pending");
        fs::create_dir_all(&pending).unwrap();
        fs::write(pending.join("App.zip"), bytes).unwrap();
        fs::write(
            pending.join("update-info.json"),
            serde_json::json!({
                "fileName":"App.zip", "sha512":artifact.file.sha512
            })
            .to_string(),
        )
        .unwrap();
        let config = super::super::electron::Config {
            base: "https://fixture.example/releases/".into(),
            cache_name: Some("fixture-updater".into()),
        };
        discovered.item.relaunch_pending =
            super::super::electron::pending_in(&cache, &config, artifact).unwrap();
        assert!(discovered.item.relaunch_pending);
        if previously_cached {
            fixture.cache_candidate(UpdateSource::Electron);
        } else {
            fixture
                .database
                .set_meta("native_update_candidates", "[]")
                .unwrap();
        }
        fixture.running(true);
        let initial = fixture.service.candidates().unwrap();
        fixture
            .service
            .persist_checked_candidates(vec![discovered], &initial)
            .unwrap();
        let visible = fixture.service.list().unwrap();
        assert_eq!(
            visible.len(),
            1,
            "a newly discovered pending download must persist"
        );
        assert!(visible[0].relaunch_pending);
        assert!(
            fixture
                .service
                .candidate(&fixture.app.id, UpdateSource::Electron)
                .is_err()
        );
    }
}

impl Fixture {
    fn swap_to_updated_bundle(&self) {
        let previous = self.root.join("installed-previous.app");
        fs::rename(&self.path, &previous).unwrap();
        fs::create_dir_all(self.path.join("Contents/Resources")).unwrap();
        let mut info = plist::Value::from_file(previous.join("Contents/Info.plist")).unwrap();
        let dictionary = info.as_dictionary_mut().unwrap();
        dictionary.insert("CFBundleVersion".into(), "200".into());
        dictionary.insert("CFBundleShortVersionString".into(), "2.0.0".into());
        info.to_file_xml(self.path.join("Contents/Info.plist"))
            .unwrap();
        let config = previous.join("Contents/Resources/app-update.yml");
        if config.exists() {
            fs::copy(config, self.path.join("Contents/Resources/app-update.yml")).unwrap();
        }
    }
    fn assert_committed_restart_only(&self, source: UpdateSource) {
        let saved = self.service.candidates().unwrap();
        let pending = saved
            .iter()
            .find(|c| c.item.app_id.as_deref() == Some(&self.app.id))
            .unwrap();
        assert_eq!(pending.item.source, source);
        assert!(pending.item.relaunch_pending);
        let rows = self.merged();
        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].source, source);
        assert!(rows[0].relaunch_pending);
        assert!(
            !rows
                .iter()
                .any(|item| item.source == UpdateSource::Homebrew)
        );
        assert!(scheduler::eligible(&rows, &self.settings.read().unwrap()).is_empty());
        assert!(self.service.candidate(&self.app.id, source).is_err());
    }
}

#[tokio::test]
async fn in_flight_check_preserves_install_committed_from_ordinary_candidate() {
    for source in [UpdateSource::Sparkle, UpdateSource::Electron] {
        for check_removes_candidate in [false, true] {
            let fixture = Fixture::new(source, "https://fixture.example/releases/");
            fixture.cache_candidate(source);
            fixture.running(true);
            let apps = fixture.service.apps.list().unwrap();
            let initial = fixture.service.candidates().unwrap();
            let mut checked = initial.clone();
            // The check has loaded an ordinary candidate and is waiting for rules.
            fixture.swap_to_updated_bundle();
            fixture.service.installed(&fixture.app.id, true).unwrap();
            fixture.assert_committed_restart_only(source);
            if check_removes_candidate {
                fixture.auto_updates(false);
            }
            let outcome = fixture
                .service
                .check_native_with_rules(
                    &[UpdateSource::Vendor],
                    &apps,
                    &mut checked,
                    true,
                    Err(AppError::new("E_UPDATE_FEED", "rules_unavailable")),
                )
                .await
                .unwrap();
            assert!(outcome.failed_sources().is_empty());
            assert_eq!(outcome.details.len(), 1);
            assert_eq!(
                outcome.details[0].reason,
                UpdateCheckFailureReason::RulesUnavailable
            );
            assert!(outcome.result().is_err());
            assert_eq!(checked.is_empty(), check_removes_candidate);
            fixture
                .service
                .persist_checked_candidates(checked, &initial)
                .unwrap();
            fixture.assert_committed_restart_only(source);
            assert!(fixture.database.tasks().unwrap().is_empty());
        }
    }
}

#[tokio::test]
async fn in_flight_check_cannot_restore_candidate_after_install_completion() {
    for source in [UpdateSource::Sparkle, UpdateSource::Electron] {
        for initially_pending in [false, true] {
            let fixture = Fixture::new(source, "https://fixture.example/releases/");
            if initially_pending {
                fixture.pending(source);
            } else {
                fixture.cache_candidate(source);
            }
            fixture.running(true);
            let apps = fixture.service.apps.list().unwrap();
            let initial = fixture.service.candidates().unwrap();
            let mut checked = initial.clone();
            fixture.swap_to_updated_bundle();
            fixture.service.installed(&fixture.app.id, false).unwrap();
            assert!(fixture.service.candidates().unwrap().is_empty());
            let outcome = fixture
                .service
                .check_native_with_rules(
                    &[UpdateSource::Vendor],
                    &apps,
                    &mut checked,
                    true,
                    Err(AppError::new("E_UPDATE_FEED", "rules_unavailable")),
                )
                .await
                .unwrap();
            assert!(outcome.result().is_err());
            assert!(outcome.failed_sources().is_empty());
            assert_eq!(outcome.details.len(), 1);
            assert_eq!(
                outcome.details[0].reason,
                UpdateCheckFailureReason::RulesUnavailable
            );
            fixture
                .service
                .persist_checked_candidates(checked, &initial)
                .unwrap();
            assert!(
                fixture.service.candidates().unwrap().is_empty(),
                "completed install resurrected its old candidate"
            );
            assert!(fixture.service.list().unwrap().is_empty());
            assert!(fixture.service.candidate(&fixture.app.id, source).is_err());
        }
    }
}

#[tokio::test]
async fn install_during_check_preserves_other_apps_fresh_results() {
    let (_feed, mut process, url, client) = server(&feed(UpdateSource::Sparkle, "201.0.0")).await;
    for pending in [false, true] {
        let mut fixture = Fixture::new(UpdateSource::Sparkle, "https://fixture.example/releases/");
        fixture.service.client = client.clone();
        fixture.running(true);
        fixture.cache_candidate(UpdateSource::Sparkle);
        let other_path = fixture.root.join("Applications/Other.app");
        fs::create_dir_all(other_path.join("Contents")).unwrap();
        let mut info = plist::Value::from_file(fixture.path.join("Contents/Info.plist")).unwrap();
        info.as_dictionary_mut()
            .unwrap()
            .insert("CFBundleIdentifier".into(), "org.fixture.Other".into());
        info.as_dictionary_mut()
            .unwrap()
            .insert("SUFeedURL".into(), url.clone().into());
        info.to_file_xml(other_path.join("Contents/Info.plist"))
            .unwrap();
        fixture
            .service
            .apps
            .refresh(LocalAppsChangedReason::Refresh)
            .unwrap();
        let other = fixture
            .service
            .apps
            .list()
            .unwrap()
            .into_iter()
            .find(|app| app.id != fixture.app.id)
            .unwrap();
        let initial = fixture.service.candidates().unwrap();
        let mut checked = initial.clone();
        let outcome = fixture
            .service
            .check_native_with_rules(
                &[UpdateSource::Sparkle],
                std::slice::from_ref(&other),
                &mut checked,
                true,
                Ok(vec![]),
            )
            .await
            .unwrap();
        outcome.result().unwrap();
        assert!(outcome.failed_sources().is_empty());
        fixture.swap_to_updated_bundle();
        fixture.service.installed(&fixture.app.id, pending).unwrap();
        fixture
            .service
            .persist_checked_candidates(checked, &initial)
            .unwrap();
        let cached = fixture.service.candidates().unwrap();
        let fresh = cached
            .iter()
            .find(|c| c.item.app_id.as_deref() == Some(&other.id))
            .unwrap();
        assert_eq!(fresh.item.current_version, "201.0.0");
        let installed = cached
            .iter()
            .find(|c| c.item.app_id.as_deref() == Some(&fixture.app.id));
        assert_eq!(installed.is_some(), pending);
        if let Some(installed) = installed {
            assert!(installed.item.relaunch_pending);
        }
        assert!(
            fixture
                .service
                .list()
                .unwrap()
                .iter()
                .any(|item| item.app_id.as_deref() == Some(&other.id)
                    && item.current_version == "201.0.0")
        );
    }
    process.kill().await.unwrap();
    process.wait().await.unwrap();
}

#[tokio::test]
async fn checked_new_source_cannot_replace_committed_app_lifecycle() {
    let (_feed, mut process, url, client) = server(&feed(UpdateSource::Electron, "201.0.0")).await;
    for pending in [false, true] {
        let mut fixture = Fixture::new(UpdateSource::Sparkle, "https://fixture.example/releases/");
        fixture.service.client = client.clone();
        fixture.cache_candidate(UpdateSource::Sparkle);
        fixture.running(true);
        fs::write(
            fixture.path.join("Contents/Resources/app-update.yml"),
            format!("provider: generic\nurl: {url}\n"),
        )
        .unwrap();
        fixture.source_enabled(UpdateSource::Sparkle, false);
        let apps = fixture.service.apps.list().unwrap();
        let initial = fixture.service.candidates().unwrap();
        let mut checked = initial.clone();
        let outcome = fixture
            .service
            .check_native_with_rules(
                &[UpdateSource::Electron],
                &apps,
                &mut checked,
                true,
                Ok(vec![]),
            )
            .await
            .unwrap();
        outcome.result().unwrap();
        assert_eq!(checked.len(), 1);
        assert_eq!(checked[0].item.source, UpdateSource::Electron);
        assert_eq!(checked[0].item.current_version, "201.0.0");
        fixture.swap_to_updated_bundle();
        fixture.service.installed(&fixture.app.id, pending).unwrap();
        fixture
            .service
            .persist_checked_candidates(checked, &initial)
            .unwrap();
        if pending {
            fixture.assert_committed_restart_only(UpdateSource::Sparkle);
        } else {
            assert!(fixture.service.candidates().unwrap().is_empty());
            assert!(fixture.service.list().unwrap().is_empty());
        }
    }
    process.kill().await.unwrap();
    process.wait().await.unwrap();
}

#[test]
fn check_preserves_app_added_to_cache_after_its_initial_snapshot() {
    let fixture = Fixture::new(UpdateSource::Sparkle, "https://fixture.example/releases/");
    let initial = fixture.service.candidates().unwrap();
    assert!(initial.is_empty());
    fixture.cache_candidate(UpdateSource::Sparkle);
    fixture.running(true);
    fixture.service.installed(&fixture.app.id, true).unwrap();
    fixture
        .service
        .persist_checked_candidates(vec![], &initial)
        .unwrap();
    fixture.assert_committed_restart_only(UpdateSource::Sparkle);
}

#[test]
fn cached_candidate_must_match_current_channel_even_after_restart() {
    for source in [UpdateSource::Sparkle, UpdateSource::Electron] {
        let mut fixture = Fixture::new(source, "https://fixture.example/releases/");
        fixture.cache_candidate(source);
        fixture
            .service
            .update_candidates(|candidates| {
                candidates[0].item.channel = Some(UpdateChannel::Beta);
                Ok(())
            })
            .unwrap();
        let key = format!("app:{}", fixture.app.id);
        fixture.service.channel(&key, UpdateChannel::Beta).unwrap();
        assert!(fixture.service.candidate(&fixture.app.id, source).is_ok());
        fixture
            .service
            .channel(&key, UpdateChannel::Stable)
            .unwrap();
        for restart in [false, true] {
            if restart {
                fixture.restart();
            }
            fixture.assert_hidden();
            assert!(fixture.service.list().unwrap().is_empty());
            assert_eq!(
                fixture
                    .service
                    .candidate(&fixture.app.id, source)
                    .unwrap_err()
                    .message,
                "update_candidate_no_longer_current"
            );
        }
        fixture
            .service
            .update_candidates(|candidates| {
                candidates[0].item.channel = Some(UpdateChannel::Stable);
                Ok(())
            })
            .unwrap();
        assert!(fixture.service.candidate(&fixture.app.id, source).is_ok());
    }
}

#[test]
fn channel_change_preserves_restart_action_for_already_installed_update() {
    let fixture = Fixture::new(UpdateSource::Electron, "https://fixture.example/releases/");
    fixture.pending(UpdateSource::Electron);
    fixture.running(true);
    fixture
        .service
        .update_candidates(|candidates| {
            candidates[0].item.channel = Some(UpdateChannel::Beta);
            Ok(())
        })
        .unwrap();
    fixture
        .service
        .channel(&format!("app:{}", fixture.app.id), UpdateChannel::Stable)
        .unwrap();
    assert!(fixture.service.list().unwrap()[0].relaunch_pending);
    assert!(
        fixture
            .service
            .candidate(&fixture.app.id, UpdateSource::Electron)
            .is_err()
    );
}

#[tokio::test]
async fn rules_outage_does_not_mark_working_local_feeds_failed() {
    for source in [UpdateSource::Sparkle, UpdateSource::Electron] {
        let (_files, mut process, url, client) = server(&feed(source, "200.0.0")).await;
        let mut fixture = Fixture::new(source, &url);
        fixture.service.client = client;
        let mut candidates = vec![];
        let outcome = fixture
            .service
            .check_native_with_rules(
                &[source],
                std::slice::from_ref(&fixture.app),
                &mut candidates,
                true,
                Err(AppError::new("E_UPDATE_FEED", "feed_request")),
            )
            .await
            .unwrap();
        assert!(outcome.result().is_ok());
        assert!(outcome.failed_sources().is_empty());
        assert_eq!(candidates.len(), 1);
        assert_eq!(outcome.details.len(), 1);
        assert_eq!(
            outcome.details[0].reason,
            UpdateCheckFailureReason::RulesUnavailable
        );
        assert_eq!(outcome.details[0].source, None);
        process.kill().await.unwrap();
    }
}

#[tokio::test]
async fn invalid_local_feed_reports_app_and_configuration_error_without_network() {
    for source in [UpdateSource::Sparkle, UpdateSource::Electron] {
        let fixture = Fixture::new(source, "https://updates.invalid./app/");
        let outcome = fixture
            .service
            .check_native_with_rules(
                &[source],
                std::slice::from_ref(&fixture.app),
                &mut vec![],
                true,
                Ok(vec![]),
            )
            .await
            .unwrap();
        assert_eq!(outcome.failed_sources(), [source]);
        assert_eq!(outcome.result().unwrap_err().message, "invalid_update_url");
        assert_eq!(
            outcome.details[0].app_id.as_deref(),
            Some(fixture.app.id.as_str())
        );
        assert_eq!(
            outcome.details[0].app_name.as_deref(),
            Some(fixture.app.name.as_str())
        );
        assert_eq!(
            outcome.details[0].reason,
            UpdateCheckFailureReason::InvalidUrl
        );
    }
}

#[tokio::test]
async fn rate_limited_feed_respects_retry_after_then_recovers() {
    let (files, mut process, url, client) = server_mode("feed", "rate_limit").await;
    let mut fixture = Fixture::new(UpdateSource::Electron, &url);
    fixture.service.client = client;
    for _ in 0..2 {
        let error = fixture
            .service
            .read_url(&url, "test_feed", 1024)
            .await
            .unwrap_err();
        assert_eq!(error.message, "feed_rate_limited");
    }
    let requests: serde_json::Value =
        serde_json::from_slice(&fs::read(files.path().join("feed.requests")).unwrap()).unwrap();
    assert_eq!(requests.as_array().unwrap().len(), 1);
    let retry: FeedRetry = serde_json::from_str(
        &fixture
            .database
            .meta("test_feed:retry_after")
            .unwrap()
            .unwrap(),
    )
    .unwrap();
    assert!(retry.until >= chrono::Utc::now().timestamp() + 100);
    fixture
        .database
        .set_meta(
            "test_feed:retry_after",
            &serde_json::to_string(&FeedRetry {
                url: url.clone(),
                until: 0,
            })
            .unwrap(),
        )
        .unwrap();
    assert_eq!(
        fixture
            .service
            .read_url(&url, "test_feed", 1024)
            .await
            .unwrap(),
        b"feed"
    );
    process.kill().await.unwrap();
}

#[test]
fn retry_after_accepts_seconds_dates_and_defaults() {
    assert_eq!(retry_after(Some("120"), 100), 220);
    assert_eq!(retry_after(Some("Thu, 01 Jan 1970 00:05:00 GMT"), 100), 300);
    assert_eq!(retry_after(None, 100), 160);
    assert_eq!(retry_after(Some("invalid"), 100), 160);
    assert_eq!(retry_after(Some("-1"), 100), 160);
}
