use opennavo_desktop_lib::{
    catalog::{self, Changes, Snapshot, SnapshotInfo},
    db::Database,
    model::*,
};
use std::{
    io::{Read, Write},
    path::PathBuf,
    time::Instant,
};
fn fixtures() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../apps/server/testdata/desktop")
}
fn fixture<T: serde::de::DeserializeOwned>(name: &str) -> T {
    serde_json::from_slice(&std::fs::read(fixtures().join(name)).unwrap()).unwrap()
}
fn snapshot() -> Snapshot {
    serde_json::from_reader(flate2::read::GzDecoder::new(
        std::fs::File::open(fixtures().join("catalog.json.gz")).unwrap(),
    ))
    .unwrap()
}
fn search(q: &str) -> SearchQuery {
    SearchQuery {
        q: q.into(),
        kind: None,
        include_links: false,
        category: None,
        include_disabled: false,
        limit: 20,
        offset: 0,
    }
}
fn list() -> ListQuery {
    ListQuery {
        kind: None,
        include_links: false,
        category: None,
        sort: ListSort::Popular,
        include_fonts: false,
        include_libraries: false,
        include_disabled: false,
        limit: 200,
        offset: 0,
    }
}
#[test]
fn fixtures_import_delta_search_and_atomic_failure() {
    let temp = tempfile::tempdir().unwrap();
    let db = Database::open(&temp.path().join("catalog.db")).unwrap();
    let info: SnapshotInfo = fixture("latest.json");
    assert_eq!(
        catalog::import_file(&db, &fixtures().join("catalog.json.gz"), &info, 1).unwrap(),
        40
    );
    for (q, token) in [
        ("微信", "wechat"),
        ("rg", "ripgrep"),
        ("vscode", "visual-studio-code"),
    ] {
        let hits = catalog::search(&db, &search(q), 2).unwrap();
        if q == "rg" {
            assert!(hits.items.is_empty());
        } else {
            assert_eq!(hits.items[0].item.token, token);
        }
    }
    assert_eq!(
        catalog::apply_changes(
            &db,
            &fixture::<catalog::Response<Changes>>("changes-page.json")
                .data
                .unwrap(),
            3
        )
        .unwrap(),
        3
    );
    assert_eq!(
        catalog::get(&db, Kind::Cask, "visual-studio-code")
            .unwrap()
            .unwrap()
            .version,
        "1.140.1"
    );
    assert!(
        catalog::get(&db, Kind::Cask, "font-jetbrains-mono")
            .unwrap()
            .is_none()
    );
    assert_eq!(
        catalog::apply_changes(
            &db,
            &fixture::<catalog::Response<Changes>>("changes-page.json")
                .data
                .unwrap(),
            3
        )
        .unwrap(),
        0
    );
    catalog::apply_changes(
        &db,
        &fixture::<catalog::Response<Changes>>("changes-final.json")
            .data
            .unwrap(),
        4,
    )
    .unwrap();
    assert_eq!(catalog::status(&db, false).unwrap().cursor, 64);
    assert_eq!(catalog::status(&db, false).unwrap().item_count, 39);
    let mut broken = snapshot();
    broken.items[10].token = "../../invalid".into();
    assert!(catalog::import_snapshot(&db, &broken, &info, 5).is_err());
    assert_eq!(catalog::status(&db, false).unwrap().cursor, 64);
    let mut bad_info = info.clone();
    bad_info.sha256 = "0".repeat(64);
    assert_eq!(
        catalog::import_file(&db, &fixtures().join("catalog.json.gz"), &bad_info, 6)
            .unwrap_err()
            .code,
        "E_CHECKSUM"
    );
    assert_eq!(catalog::status(&db, false).unwrap().item_count, 39);
    let cats = catalog::categories(&db, Locale::ZhCn).unwrap();
    let english = catalog::categories(&db, Locale::EnUs).unwrap();
    assert_eq!(cats.len(), 20);
    assert!(cats.iter().zip(english).any(|(a, b)| a.name != b.name));
    assert!(
        catalog::list(&db, &list())
            .unwrap()
            .items
            .iter()
            .all(|i| !i.is_font && !i.is_library && !i.disabled && !i.hidden)
    );
    for q in ["' OR 1=1", "\"", "*", "%", "_", "-", "'", "AND"] {
        let _ = catalog::search(&db, &search(q), 7).unwrap();
    }
    assert!(catalog::search(&db, &search("a\nb"), 7).is_err());
    for i in 0..25 {
        let _ = catalog::search(&db, &search(&format!("query{i}")), i + 10).unwrap();
    }
    db.with(|c| {
        assert_eq!(
            c.query_row("SELECT count(*) FROM search_history", [], |r| r
                .get::<_, i64>(0))?,
            20
        );
        Ok(())
    })
    .unwrap();
}
#[test]
fn sixteen_thousand_import_and_short_queries() {
    use sha2::{Digest, Sha256};
    let temp = tempfile::tempdir().unwrap();
    let db = Database::open(&temp.path().join("catalog.db")).unwrap();
    let mut snap = snapshot();
    let mut template = snap.items[0].clone();
    template.names = vec!["Benchmark Application".into()];
    template
        .display_name
        .0
        .insert(Locale::ZhCn, "性能测试应用".into());
    template.display_name.0.remove(&Locale::EnUs);
    template.name = "Benchmark Application".into();
    template
        .summary
        .0
        .insert(Locale::ZhCn, "确定的性能测试数据".into());
    template.summary.0.remove(&Locale::EnUs);
    template.binaries.clear();
    template.pinyin = None;
    for i in snap.items.len()..16_000 {
        let mut item = template.clone();
        item.token = format!("bench_{i}");
        snap.items.push(item);
    }
    let mut encoder = flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::fast());
    serde_json::to_writer(&mut encoder, &snap).unwrap();
    let bytes = encoder.finish().unwrap();
    let path = temp.path().join("catalog.gz");
    std::fs::write(&path, &bytes).unwrap();
    let mut info: SnapshotInfo = fixture("latest.json");
    info.item_count = 16000;
    info.bytes = bytes.len() as u64;
    info.sha256 = format!("{:x}", Sha256::digest(&bytes));
    let started = Instant::now();
    catalog::import_file(&db, &path, &info, 10).unwrap();
    let elapsed = started.elapsed();
    println!("16k gzip import: {} ms", elapsed.as_millis());
    assert!(elapsed.as_secs_f64() < 3.0, "{elapsed:?}");
    for (q, token) in [
        ("微信", "wechat"),
        ("rg", "ripgrep"),
        ("vscode", "visual-studio-code"),
    ] {
        let start = Instant::now();
        let result = catalog::search(&db, &search(q), 20).unwrap();
        println!("query {q}: {} us", start.elapsed().as_micros());
        if q == "rg" {
            assert!(result.items.is_empty());
        } else {
            assert_eq!(result.items[0].item.token, token);
        }
        assert!(start.elapsed().as_millis() < 50);
    }
    let mut query = list();
    query.kind = Some(Kind::Formula);
    query.sort = ListSort::Installs90d;
    query.include_libraries = true;
    let page = catalog::list(&db, &query).unwrap();
    assert!(page.items.is_empty());
    query.kind = Some(Kind::Cask);
    let page = catalog::list(&db, &query).unwrap();
    assert!(!page.items.is_empty());
    let stats: Vec<_> = page
        .items
        .iter()
        .map(|i| i.on_request.as_ref().map(|r| r.d90).or(i.installs_90d))
        .collect();
    assert!(stats.windows(2).all(|w| w[0] >= w[1]));
}
struct HttpServer {
    url: String,
    stop: std::sync::Arc<std::sync::atomic::AtomicBool>,
    thread: Option<std::thread::JoinHandle<()>>,
}
impl HttpServer {
    fn new(handler: impl Fn(&str, &str) -> (u16, Vec<u8>) + Send + 'static) -> Self {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let address = listener.local_addr().unwrap();
        let url = format!("http://{address}");
        let thread_url = url.clone();
        let stop = std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false));
        let thread_stop = stop.clone();
        let thread = std::thread::spawn(move || {
            for stream in listener.incoming() {
                if thread_stop.load(std::sync::atomic::Ordering::Acquire) {
                    break;
                }
                let mut stream = stream.unwrap();
                stream
                    .set_read_timeout(Some(std::time::Duration::from_secs(2)))
                    .unwrap();
                let mut buf = [0; 8192];
                let n = stream.read(&mut buf).unwrap_or(0);
                let text = String::from_utf8_lossy(&buf[..n]);
                let (code, body) = handler(&text, &thread_url);
                let header = format!(
                    "HTTP/1.1 {code} Test\r\nContent-Length: {}\r\nConnection: close\r\nContent-Type: application/json\r\n\r\n",
                    body.len()
                );
                let _ = stream.write_all(header.as_bytes());
                let _ = stream.write_all(&body);
            }
        });
        Self {
            url,
            stop,
            thread: Some(thread),
        }
    }
}
impl Drop for HttpServer {
    fn drop(&mut self) {
        self.stop.store(true, std::sync::atomic::Ordering::Release);
        let _ = std::net::TcpStream::connect(self.url.trim_start_matches("http://"));
        if let Some(thread) = self.thread.take() {
            thread.join().unwrap();
        }
    }
}
#[tokio::test]
async fn sync_http_snapshot_delta_expiry_cdn_and_headers() {
    use std::sync::{
        Arc, RwLock,
        atomic::{AtomicU8, Ordering},
    };
    let mode = Arc::new(AtomicU8::new(0));
    let control = mode.clone();
    let requests = Arc::new(std::sync::Mutex::new(Vec::<String>::new()));
    let recorded = requests.clone();
    let server = HttpServer::new(move |request, url| {
        assert!(
            request
                .to_lowercase()
                .contains("x-client-platform: desktop")
        );
        let language = if control.load(Ordering::Relaxed) >= 3 {
            "ja-jp"
        } else {
            "en-us"
        };
        assert!(
            request
                .to_lowercase()
                .contains(&format!("accept-language: {language}"))
        );
        assert!(request.to_lowercase().contains("x-client-version:"));
        let path = request.split_whitespace().nth(1).unwrap();
        recorded.lock().unwrap().push(path.to_owned());
        if path == "/api/catalog/snapshot" && matches!(control.load(Ordering::Relaxed), 2 | 6) {
            return (503, b"{}".to_vec());
        }
        if path == "/api/catalog/snapshot" || path == "/latest.json" {
            let mut info: serde_json::Value =
                serde_json::from_slice(&std::fs::read(fixtures().join("v2/latest.json")).unwrap())
                    .unwrap();
            info["url"] = format!("{url}/catalog.gz").into();
            for locale in ["en-US", "zh-CN", "ja-JP", "es-ES", "pt-BR", "ru-RU"] {
                info["textPacks"][locale]["url"] = format!("{url}/text-{locale}.json.gz").into();
            }
            if control.load(Ordering::Relaxed) == 4 {
                info["textPacks"]["ja-JP"]["sha256"] = "0".repeat(64).into();
                info["textPacks"]["ja-JP"]["version"] =
                    (info["textPacks"]["ja-JP"]["version"].as_i64().unwrap() + 1).into();
            }
            if path.contains("/api/") {
                return (
                    200,
                    serde_json::to_vec(&serde_json::json!({"code":"0000","data":info})).unwrap(),
                );
            }
            return (200, serde_json::to_vec(&info).unwrap());
        }
        if path == "/catalog.gz" {
            return (
                200,
                std::fs::read(fixtures().join("v2/catalog.json.gz")).unwrap(),
            );
        }
        if path.starts_with("/text-") {
            return (
                200,
                std::fs::read(fixtures().join("v2").join(path.trim_start_matches('/'))).unwrap(),
            );
        }
        if path.starts_with("/api/catalog/changes") {
            if control.load(Ordering::Relaxed) == 6 {
                return (503, b"{}".to_vec());
            }
            if (control.load(Ordering::Relaxed) == 1 && path.contains("since=1&"))
                || control.load(Ordering::Relaxed) == 5
            {
                return (
                    410,
                    std::fs::read(fixtures().join("changes-expired.json")).unwrap(),
                );
            }
            let name = if path.contains("since=60&") {
                "changes-page.json"
            } else if path.contains("since=63&") {
                "changes-final.json"
            } else {
                "changes-empty.json"
            };
            let mut delta: serde_json::Value =
                serde_json::from_slice(&std::fs::read(fixtures().join("v2").join(name)).unwrap())
                    .unwrap();
            if name == "changes-page.json" {
                delta["data"]["changes"][0]["item"]["categories"] = serde_json::json!(["ai"]);
            }
            return (200, serde_json::to_vec(&delta).unwrap());
        }
        (404, b"{}".to_vec())
    });
    let temp = tempfile::tempdir().unwrap();
    let db = Database::open(&temp.path().join("db")).unwrap();
    let settings = Arc::new(RwLock::new(Settings {
        locale: Locale::EnUs,
        ..Settings::default()
    }));
    let events = Arc::new(std::sync::Mutex::new(Vec::new()));
    let saved = events.clone();
    let service = catalog::sync::CatalogService::new(
        db.clone(),
        format!("{}/api", server.url),
        format!("{}/latest.json", server.url),
        temp.path().join("cache"),
        settings.clone(),
        Arc::new(move |event| saved.lock().unwrap().push(event)),
    );
    let result = service.sync(false).await.unwrap();
    assert_eq!(result.mode, SyncMode::Snapshot);
    assert_eq!(result.cursor, 64);
    assert_eq!(result.applied, 44);
    assert_eq!(service.sync(false).await.unwrap().mode, SyncMode::None);
    assert_eq!(service.sync(false).await.unwrap().mode, SyncMode::None);
    mode.store(1, Ordering::Relaxed);
    db.set_meta("catalog_cursor", "1").unwrap();
    let recovered = service.sync(false).await.unwrap();
    assert_eq!(recovered.mode, SyncMode::Snapshot);
    assert_eq!(recovered.cursor, 64);
    mode.store(2, Ordering::Relaxed);
    requests.lock().unwrap().clear();
    let refreshed = service.sync(true).await.unwrap();
    assert_eq!(refreshed.mode, SyncMode::Snapshot);
    assert_eq!(refreshed.cursor, 64);
    assert_eq!(refreshed.applied, 44);
    assert_eq!(
        catalog::get(&db, Kind::Cask, "visual-studio-code")
            .unwrap()
            .unwrap()
            .categories,
        vec!["ai"]
    );
    let mut category_query = list();
    category_query.category = Some("ai".into());
    assert!(
        catalog::list(&db, &category_query)
            .unwrap()
            .items
            .iter()
            .any(|item| item.token == "visual-studio-code")
    );
    let paths = requests.lock().unwrap().clone();
    assert!(
        paths
            .iter()
            .any(|p| p == "/api/catalog/changes?since=60&limit=500")
    );
    assert!(
        paths
            .iter()
            .any(|p| p == "/api/catalog/changes?since=63&limit=500")
    );
    assert!(!service.is_syncing());
    assert_eq!(events.lock().unwrap().len(), 5);
    assert_eq!(
        std::fs::read_dir(temp.path().join("cache"))
            .unwrap()
            .count(),
        3
    );
    mode.store(3, Ordering::Relaxed);
    settings.write().unwrap().locale = Locale::JaJp;
    requests.lock().unwrap().clear();
    let old_hash = db.meta("snapshot_sha256").unwrap();
    let old_cursor = db.meta("catalog_cursor").unwrap();
    service.sync_text().await.unwrap();
    assert_eq!(
        *requests.lock().unwrap(),
        vec!["/api/catalog/snapshot", "/text-ja-JP.json.gz"]
    );
    assert_eq!(db.meta("snapshot_sha256").unwrap(), old_hash);
    assert_eq!(db.meta("catalog_cursor").unwrap(), old_cursor);
    let before = catalog::search(&db, &search("日本語"), 1).unwrap().total;
    assert_eq!(before, 1);
    requests.lock().unwrap().clear();
    mode.store(4, Ordering::Relaxed);
    assert_eq!(service.sync_text().await.unwrap_err().code, "E_CHECKSUM");
    assert_eq!(db.meta("snapshot_sha256").unwrap(), old_hash);
    assert_eq!(db.meta("catalog_cursor").unwrap(), old_cursor);
    assert_eq!(
        catalog::search(&db, &search("日本語"), 1).unwrap().total,
        before
    );
    mode.store(5, Ordering::Relaxed);
    requests.lock().unwrap().clear();
    let completed_events = events.lock().unwrap().len();
    assert!(service.sync(true).await.is_err());
    assert_eq!(events.lock().unwrap().len(), completed_events + 1);
    assert_eq!(catalog::status(&db, false).unwrap().item_count, 40);
    assert_eq!(
        requests
            .lock()
            .unwrap()
            .iter()
            .filter(|path| path.starts_with("/api/catalog/changes"))
            .count(),
        2
    );
    assert!(!service.is_syncing());
    mode.store(6, Ordering::Relaxed);
    events.lock().unwrap().clear();
    assert!(service.sync(true).await.is_err());
    let saved = events.lock().unwrap();
    assert_eq!(saved.len(), 1);
    let opennavo_desktop_lib::events::CoreEvent::Catalog(status) = &saved[0] else {
        panic!("expected catalog refresh");
    };
    assert_eq!(status.item_count, 40);
    assert!(!status.syncing);
    assert!(!service.is_syncing());
}
