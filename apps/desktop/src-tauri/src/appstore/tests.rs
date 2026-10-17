use super::*;
use serde_json::{Value, json};
use std::{
    collections::BTreeMap,
    sync::atomic::{AtomicI64, Ordering},
};
use tokio::io::{AsyncReadExt, AsyncWriteExt};

fn app(id: u64, bundle: &str) -> Value {
    json!({"kind":"software", "trackId":id, "bundleId":bundle, "trackName":"Fixture App", "sellerName":"Fixture",
        "version":"2.0", "price":0, "formattedPrice":"Free", "currency":"USD", "fileSizeBytes":"1234",
        "currentVersionReleaseDate":"2024-01-01T00:00:00Z", "minimumOsVersion":"13.0", "releaseNotes":"Changes",
        "artworkUrl512":"https://is1-ssl.mzstatic.com/fixture.png", "screenshotUrls":["https://is1-ssl.mzstatic.com/screen.png"]})
}
struct Server {
    base: reqwest::Url,
    calls: Arc<Mutex<Vec<reqwest::Url>>>,
    task: tokio::task::JoinHandle<()>,
}
impl Drop for Server {
    fn drop(&mut self) {
        self.task.abort();
    }
}
impl Server {
    async fn new(
        handler: impl Fn(&reqwest::Url) -> (u16, String, String) + Send + Sync + 'static,
    ) -> Self {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let base =
            reqwest::Url::parse(&format!("http://{}/", listener.local_addr().unwrap())).unwrap();
        let calls = Arc::new(Mutex::new(Vec::new()));
        let captured = calls.clone();
        let base_copy = base.clone();
        let task = tokio::spawn(async move {
            loop {
                let (mut stream, _) = listener.accept().await.unwrap();
                let mut bytes = Vec::new();
                loop {
                    let mut chunk = [0; 1024];
                    let count = stream.read(&mut chunk).await.unwrap();
                    if count == 0 {
                        break;
                    }
                    bytes.extend_from_slice(&chunk[..count]);
                    if bytes.windows(4).any(|w| w == b"\r\n\r\n") {
                        break;
                    }
                }
                let request = String::from_utf8(bytes).unwrap();
                let url = base_copy
                    .join(request.split_whitespace().nth(1).unwrap())
                    .unwrap();
                captured.lock().unwrap().push(url.clone());
                let (status, headers, body) = handler(&url);
                let response = format!(
                    "HTTP/1.1 {status} Response\r\nContent-Length: {}\r\nContent-Type: application/json\r\nConnection: close\r\n{headers}\r\n{body}",
                    body.len()
                );
                stream.write_all(response.as_bytes()).await.unwrap();
            }
        });
        Self { base, calls, task }
    }
    async fn success() -> Self {
        Self::new(|url| {
            let params: BTreeMap<_, _> = url.query_pairs().into_owned().collect();
            assert_eq!(params["entity"], "desktopSoftware");
            assert!(params.contains_key("country") && params.contains_key("lang"));
            let apps = if params
                .get("bundleId")
                .is_some_and(|id| id == "test.missing")
            {
                vec![]
            } else if let Some(ids) = params.get("id") {
                ids.split(',')
                    .map(|id| app(id.parse().unwrap(), "test.app"))
                    .collect()
            } else {
                vec![app(42, "test.app")]
            };
            (
                200,
                String::new(),
                json!({"resultCount":apps.len(),"results":apps}).to_string(),
            )
        })
        .await
    }
    fn count(&self) -> usize {
        self.calls.lock().unwrap().len()
    }
}
#[derive(Default)]
struct FakeHost {
    opened: Mutex<Vec<String>>,
    apps: Mutex<Vec<native::Candidate>>,
}
impl Host for FakeHost {
    fn hints(&self) -> Result<Hints, AppError> {
        Ok(Hints {
            storefront: Some("143465-19,29".into()),
            locale: "en_US".into(),
            os_version: "27.0.1".into(),
            engine: MasEngineStatus::Missing,
        })
    }
    fn installed(&self) -> Result<Vec<native::Candidate>, AppError> {
        Ok(self.apps.lock().unwrap().clone())
    }
    fn open_store(&self, url: &str) -> Result<(), AppError> {
        self.opened.lock().unwrap().push(url.into());
        Ok(())
    }
}
struct Fixture {
    _dir: tempfile::TempDir,
    db: Database,
    settings: Arc<RwLock<Settings>>,
    clock: Arc<AtomicI64>,
    host: Arc<FakeHost>,
    events: Arc<Mutex<Vec<CoreEvent>>>,
}
impl Fixture {
    fn new() -> Self {
        let dir = tempfile::tempdir().unwrap();
        Self {
            db: Database::open(&dir.path().join("test.db")).unwrap(),
            _dir: dir,
            settings: Arc::new(RwLock::new(Settings::default())),
            clock: Arc::new(AtomicI64::new(1_700_000_000_000)),
            host: Arc::new(FakeHost::default()),
            events: Arc::new(Mutex::new(vec![])),
        }
    }
    fn service(&self, server: &Server) -> Arc<Service> {
        let clock = self.clock.clone();
        let events = self.events.clone();
        Arc::new(
            Service::with_host(
                self.db.clone(),
                self.settings.clone(),
                Arc::new(move |e| events.lock().unwrap().push(e)),
                self.host.clone(),
                server.base.clone(),
                Arc::new(move || clock.load(Ordering::Relaxed)),
            )
            .unwrap(),
        )
    }
}

#[tokio::test]
async fn concurrent_cache_persistence_misses_and_country_language_isolation() {
    let server = Server::success().await;
    let fixture = Fixture::new();
    let service = fixture.service(&server);
    let mut tasks = tokio::task::JoinSet::new();
    for _ in 0..20 {
        let service = service.clone();
        tasks.spawn(async move { service.lookup(vec![42]).await.unwrap() });
    }
    while let Some(result) = tasks.join_next().await {
        assert_eq!(result.unwrap()[0].adam_id, 42);
    }
    assert_eq!(server.count(), 1);
    drop(service);
    let service = fixture.service(&server);
    service.lookup(vec![42]).await.unwrap();
    assert_eq!(server.count(), 1);
    let bundles = service
        .lookup_bundles(vec!["test.app".into(), "test.missing".into()])
        .await
        .unwrap();
    assert_eq!(bundles.0.len(), 1);
    assert_eq!(server.count(), 3);
    service
        .lookup_bundles(vec!["test.missing".into()])
        .await
        .unwrap();
    assert_eq!(server.count(), 3);
    fixture.settings.write().unwrap().app_store_region = "us".into();
    assert_eq!(service.lookup(vec![42]).await.unwrap()[0].country, "us");
    assert_eq!(server.count(), 4);
    fixture.settings.write().unwrap().locale = crate::model::Locale::ZhCn;
    service.lookup(vec![42]).await.unwrap();
    assert_eq!(server.count(), 5);
    fixture
        .clock
        .fetch_add(6 * 60 * 60 * 1000, Ordering::Relaxed);
    service.lookup(vec![42]).await.unwrap();
    assert_eq!(server.count(), 6);
    assert!(fixture.events.lock().unwrap().iter().any(|e| matches!(
        e,
        CoreEvent::AppStore(AppStoreChange {
            reason: AppStoreChangeReason::Region
        })
    )));
}

#[tokio::test]
async fn search_ttl_and_one_shared_rolling_budget() {
    let server = Server::success().await;
    let fixture = Fixture::new();
    let service = fixture.service(&server);
    for _ in 0..2 {
        service.search("fixture".into(), 3).await.unwrap();
    }
    for id in 1..=7 {
        service.lookup(vec![id]).await.unwrap();
    }
    for id in 1..=7 {
        service
            .lookup_bundles(vec![format!("test.item{id}")])
            .await
            .unwrap();
    }
    assert_eq!(server.count(), 15);
    let error = service.search("over-limit".into(), 3).await.unwrap_err();
    assert_eq!(error.code, "E_MAS_RATE_LIMITED");
    assert_eq!(
        serde_json::from_str::<Value>(&error.detail.unwrap()).unwrap()["retryAfterSecs"],
        60
    );
    let restarted = fixture.service(&server); // Restart cannot reset the request budget.
    assert!(restarted.lookup(vec![99]).await.is_err());
    assert_eq!(server.count(), 15);
    fixture.clock.fetch_add(60_000, Ordering::Relaxed);
    service.search("fixture".into(), 3).await.unwrap();
    assert_eq!(server.count(), 15);
    service.lookup(vec![99]).await.unwrap();
    assert_eq!(server.count(), 16);
    fixture.clock.fetch_add(540_000, Ordering::Relaxed);
    service.search("fixture".into(), 3).await.unwrap();
    assert_eq!(server.count(), 17);
}

#[tokio::test]
async fn remote_cooldown_survives_restart_and_bad_payload_is_not_cached() {
    for status in [403, 429] {
        let server =
            Server::new(move |_| (status, "Retry-After: 120\r\n".into(), String::new())).await;
        let fixture = Fixture::new();
        let service = fixture.service(&server);
        assert_eq!(
            service.lookup(vec![42]).await.unwrap_err().code,
            "E_MAS_RATE_LIMITED"
        );
        fixture.clock.fetch_add(60_000, Ordering::Relaxed);
        let service = fixture.service(&server);
        assert!(service.search("fixture".into(), 3).await.is_err());
        assert_eq!(server.count(), 1);
        fixture.clock.fetch_add(60_000, Ordering::Relaxed);
        assert!(service.search("fixture".into(), 3).await.is_err());
        assert_eq!(server.count(), 2);
    }
    let server =
        Server::new(|_| (200, String::new(), "{\"errorMessage\":\"invalid\"}".into())).await;
    let fixture = Fixture::new();
    let service = fixture.service(&server);
    for _ in 0..2 {
        assert_eq!(
            service.lookup(vec![42]).await.unwrap_err().code,
            "E_MAS_ENGINE"
        );
    }
    assert_eq!(server.count(), 2);
}

#[tokio::test]
async fn settings_validation_and_open_store_use_fixed_scheme_and_host_only() {
    let server = Server::success().await;
    let fixture = Fixture::new();
    let service = fixture.service(&server);
    let status = service.status().await.unwrap();
    assert_eq!(status.country, "cn");
    assert!(status.admin_install);
    assert_eq!(status.engine, MasEngineStatus::Missing);
    service.open_store(42).await.unwrap();
    assert_eq!(
        *fixture.host.opened.lock().unwrap(),
        ["macappstore://apps.apple.com/cn/app/id42?mt=12"]
    );
    assert!(service.open_store(0).await.is_err());
    assert!(service.lookup(vec![u64::MAX]).await.is_err());
    assert!(service.lookup(vec![1; 101]).await.is_err());
    assert!(
        service
            .lookup_bundles(vec!["bad&country=us".into()])
            .await
            .is_err()
    );
    assert!(service.search("fixture".into(), 51).await.is_err());
    assert_eq!(server.count(), 0);
    fixture.settings.write().unwrap().app_store_search = false;
    fixture
        .settings
        .write()
        .unwrap()
        .update_sources
        .0
        .insert(crate::model::UpdateSource::Mas, false);
    assert!(
        service
            .search("fixture".into(), 20)
            .await
            .unwrap()
            .is_empty()
    );
    assert!(service.updates().await.unwrap().is_empty());
    let legacy: Settings = serde_json::from_value(json!({"checkTime":"10:30"})).unwrap();
    assert_eq!(legacy.app_store_region, "auto");
    assert!(legacy.app_store_search && legacy.app_store_updates);
    let mut settings = legacy;
    settings.app_store_region = "IC".into();
    assert_eq!(
        crate::settings::validate(settings)
            .unwrap()
            .app_store_region,
        "es"
    );
    assert!(region::normalize("https://example.com").is_err());
}

#[test]
fn storefronts_aliases_locale_and_os_thresholds() {
    assert_eq!(countries::STOREFRONTS.len(), 175);
    for &(id, country) in countries::STOREFRONTS {
        assert_eq!(
            region::resolve("auto", Some(&format!("{id}-1,29")), "en_US").unwrap(),
            (country.into(), MasCountrySource::Account)
        );
    }
    assert_eq!(
        region::resolve("auto", Some("999999"), "zh-Hant-TW")
            .unwrap()
            .0,
        "tw"
    );
    assert_eq!(
        region::resolve("auto", None, "es_IC@calendar=gregorian")
            .unwrap()
            .0,
        "es"
    );
    assert_eq!(
        region::resolve("jp", Some("143465"), "en_US").unwrap().1,
        MasCountrySource::Setting
    );
    for version in ["13.7", "14.8.1", "15.7.1", "26.0"] {
        assert!(!region::admin_install(version));
    }
    for version in ["14.8.2", "15.7.2", "26.1", "27.0.1"] {
        assert!(region::admin_install(version));
    }
}

#[tokio::test]
async fn receipt_fallback_preserves_local_version_and_update_evidence() {
    let server = Server::success().await;
    let fixture = Fixture::new();
    fixture.host.apps.lock().unwrap().push(native::Candidate {
        adam_id: None,
        bundle_id: "test.app".into(),
        name: "Local name".into(),
        version: "1.0".into(),
        path: "/fixture/App.app".into(),
        installed_at: Some(1_600_000_000_000),
    });
    let service = fixture.service(&server);
    let local = service.installed().await.unwrap();
    assert_eq!(local[0].adam_id, 42);
    assert_eq!(local[0].version, "1.0");
    let updates = service.updates().await.unwrap();
    assert_eq!(updates.len(), 1);
    assert_eq!(updates[0].confidence, MasConfidence::High);
    assert_eq!(server.count(), 1);
    let record = service
        .lookup_bundles(vec!["test.app".into()])
        .await
        .unwrap()
        .0;
    assert!(updates::detect(&local, &record, "12.0").is_empty());
    for version in ["2.0.0", "3.0"] {
        let mut rows = local.clone();
        rows[0].version = version.into();
        assert!(updates::detect(&rows, &record, "27").is_empty());
    }
    let mut rows = local.clone();
    rows[0].installed_at = None;
    assert!(updates::detect(&rows, &record, "27").is_empty());
    rows[0].installed_at = Some(1_800_000_000_000);
    assert!(updates::detect(&rows, &record, "27").is_empty());
    rows[0].installed_at = local[0].installed_at;
    rows[0].adam_id = 99;
    assert!(updates::detect(&rows, &record, "27").is_empty());
}

#[test]
fn native_scan_uses_temporary_bundles_and_never_follows_symlinks() {
    let dir = tempfile::tempdir().unwrap();
    for (name, receipt) in [("Known", false), ("Fallback", true), ("NonStore", false)] {
        let contents = dir.path().join(format!("{name}.app/Contents"));
        std::fs::create_dir_all(&contents).unwrap();
        let mut info = plist::Dictionary::new();
        info.insert(
            "CFBundleIdentifier".into(),
            format!("test.{}", name.to_lowercase()).into(),
        );
        info.insert("CFBundleShortVersionString".into(), "1.0".into());
        plist::Value::Dictionary(info)
            .to_file_xml(contents.join("Info.plist"))
            .unwrap();
        if receipt {
            std::fs::create_dir(contents.join("_MASReceipt")).unwrap();
            std::fs::write(contents.join("_MASReceipt/receipt"), b"fixture marker").unwrap();
        }
    }
    #[cfg(unix)]
    std::os::unix::fs::symlink(dir.path().join("Known.app"), dir.path().join("Alias.app")).unwrap();
    let rows = native::scan(&[dir.path().into()], |path| {
        if path.ends_with("Known.app") {
            (Some(42), Some(1234))
        } else {
            (None, None)
        }
    })
    .unwrap();
    assert_eq!(rows.len(), 2);
    assert!(rows.iter().any(|app| app.adam_id == Some(42)));
    assert!(rows.iter().any(|app| app.adam_id.is_none()));
}

/// Queue tests share the Apple wire fixtures without touching the real account.
pub(crate) async fn queue_service(
    database: Database,
    settings: Arc<RwLock<Settings>>,
) -> (impl Drop, Arc<Service>) {
    let server = Server::success().await;
    let service = Service::with_host(
        database,
        settings,
        Arc::new(|_| {}),
        Arc::new(FakeHost::default()),
        server.base.clone(),
        Arc::new(|| 1_700_000_000_000),
    )
    .unwrap();
    (server, Arc::new(service))
}

#[tokio::test]
async fn updates_batch_known_ids_after_daily_cache_expiry() {
    let server = Server::new(|url| {
        let params: BTreeMap<_, _> = url.query_pairs().into_owned().collect();
        assert!(!params.contains_key("bundleId"));
        let apps: Vec<_> = params["id"]
            .split(',')
            .map(|id| app(id.parse().unwrap(), &format!("test.app{id}")))
            .collect();
        (
            200,
            String::new(),
            json!({"resultCount":apps.len(),"results":apps}).to_string(),
        )
    })
    .await;
    let fixture = Fixture::new();
    *fixture.host.apps.lock().unwrap() = (1..=101)
        .map(|id| native::Candidate {
            adam_id: Some(id),
            bundle_id: format!("test.app{id}"),
            name: format!("App {id}"),
            version: "1.0".into(),
            path: format!("/fixture/App{id}.app"),
            installed_at: Some(1_600_000_000_000),
        })
        .collect();
    let service = fixture.service(&server);
    for day in 1..=2 {
        assert_eq!(service.updates().await.unwrap().len(), 101);
        assert_eq!(server.count(), day * 2);
        fixture
            .clock
            .fetch_add(24 * 60 * 60 * 1000, Ordering::Relaxed);
    }
}

#[tokio::test]
async fn missing_ids_wait_for_budget_and_survive_daily_restart() {
    let server = Server::new(|url| {
        let params: BTreeMap<_, _> = url.query_pairs().into_owned().collect();
        let apps: Vec<_> = if let Some(bundle) = params.get("bundleId") {
            vec![app(
                bundle.trim_start_matches("test.app").parse().unwrap(),
                bundle,
            )]
        } else {
            params["id"]
                .split(',')
                .map(|id| app(id.parse().unwrap(), &format!("test.app{id}")))
                .collect()
        };
        (
            200,
            String::new(),
            json!({"resultCount": apps.len(), "results": apps}).to_string(),
        )
    })
    .await;
    let fixture = Fixture::new();
    *fixture.host.apps.lock().unwrap() = (1..=17)
        .map(|id| native::Candidate {
            adam_id: (id == 17).then_some(id),
            bundle_id: format!("test.app{id}"),
            name: format!("App {id}"),
            version: "1.0".into(),
            path: format!("/fixture/App{id}.app"),
            installed_at: Some(1_600_000_000_000),
        })
        .collect();
    let start = std::time::Instant::now();
    let offset = fixture.clock.clone();
    let clock: Arc<dyn Fn() -> i64 + Send + Sync> =
        Arc::new(move || offset.load(Ordering::Relaxed) + start.elapsed().as_millis() as i64);
    let service = || {
        Service::with_host(
            fixture.db.clone(),
            fixture.settings.clone(),
            Arc::new(|_| {}),
            fixture.host.clone(),
            server.base.clone(),
            clock.clone(),
        )
        .unwrap()
    };
    let first = service();
    let updates = tokio::time::timeout(std::time::Duration::from_secs(90), first.updates())
        .await
        .unwrap()
        .unwrap();
    assert_eq!(updates.len(), 17);
    assert_eq!(server.count(), 17); // 16 identity resolutions + one known-ID lookup.
    assert!(start.elapsed() >= std::time::Duration::from_secs(60));
    drop(first);
    fixture
        .clock
        .fetch_add(24 * 60 * 60 * 1000, Ordering::Relaxed);
    assert_eq!(service().updates().await.unwrap().len(), 17);
    assert_eq!(server.count(), 18); // Restart and metadata expiry still use one batch.
    assert!(
        server
            .calls
            .lock()
            .unwrap()
            .last()
            .unwrap()
            .query_pairs()
            .any(|(key, _)| key == "id")
    );
}
