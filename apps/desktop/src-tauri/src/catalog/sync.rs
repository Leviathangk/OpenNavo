use super::{Changes, Response, SnapshotInfo, protocol};
use crate::{
    AppError,
    db::Database,
    events::{CoreEvent, EventSink},
    model::{CatalogStatus, Settings, SyncMode, SyncResult},
};
use reqwest::header::{HeaderMap, HeaderValue};
use sha2::{Digest, Sha256};
use std::{
    collections::HashSet,
    path::PathBuf,
    sync::{
        Arc, RwLock,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};
use tokio::io::AsyncWriteExt;

pub struct CatalogService {
    database: Database,
    base: String,
    latest_url: String,
    cache: PathBuf,
    settings: Arc<RwLock<Settings>>,
    sink: EventSink,
    syncing: AtomicBool,
    lock: tokio::sync::Mutex<()>,
}
struct Reset<'a>(&'a AtomicBool);
impl Drop for Reset<'_> {
    fn drop(&mut self) {
        self.0.store(false, Ordering::Release);
    }
}
struct Download(PathBuf);
impl Drop for Download {
    fn drop(&mut self) {
        let path = self.0.clone();
        std::thread::spawn(move || {
            let _ = std::fs::remove_file(path);
        });
    }
}
impl CatalogService {
    pub fn new(
        database: Database,
        base: String,
        latest_url: String,
        cache: PathBuf,
        settings: Arc<RwLock<Settings>>,
        sink: EventSink,
    ) -> Self {
        Self {
            database,
            base: base.trim_end_matches('/').into(),
            latest_url,
            cache,
            settings,
            sink,
            syncing: AtomicBool::new(false),
            lock: tokio::sync::Mutex::new(()),
        }
    }
    pub fn is_syncing(&self) -> bool {
        self.syncing.load(Ordering::Acquire)
    }
    fn client(&self) -> Result<reqwest::Client, AppError> {
        let locale = self
            .settings
            .read()
            .map_err(|_| protocol("settings_poisoned"))?
            .locale;
        let mut headers = HeaderMap::new();
        headers.insert("X-Client-Platform", HeaderValue::from_static("desktop"));
        headers.insert(
            "X-Client-Version",
            HeaderValue::from_static(env!("CARGO_PKG_VERSION")),
        );
        headers.insert("Accept-Language", HeaderValue::from_static(locale.as_str()));
        headers.insert(
            "User-Agent",
            HeaderValue::from_str(&format!(
                "OpenNavo/{} (macOS; {})",
                env!("CARGO_PKG_VERSION"),
                std::env::consts::ARCH
            ))
            .map_err(|_| protocol("client_header"))?,
        );
        Ok(reqwest::Client::builder()
            .default_headers(headers)
            .connect_timeout(Duration::from_secs(5))
            .timeout(Duration::from_secs(30))
            .redirect(reqwest::redirect::Policy::limited(5))
            .build()?)
    }
    pub async fn status(&self) -> Result<CatalogStatus, AppError> {
        let database = self.database.clone();
        let syncing = self.is_syncing();
        crate::blocking(move || super::status(&database, syncing)).await
    }
    async fn metadata(&self, client: &reqwest::Client) -> Result<SnapshotInfo, AppError> {
        // locales=all lists a text pack for every language this build knows. The CDN latest.json fallback lists the
        // original six only; the other languages then fall back to English until the API answers again.
        let url = format!("{}/catalog/snapshot?locales=all", self.base);
        validate_url(&url)?;
        let api = async {
            let response = client.get(&url).send().await?;
            let body: Response<SnapshotInfo> = json(response).await?;
            if body.code != "0000" {
                return Err(protocol("snapshot_response"));
            }
            body.data.ok_or_else(|| protocol("snapshot_response"))
        }
        .await;
        if api.is_ok() {
            api
        } else {
            validate_url(&self.latest_url)?;
            json(client.get(&self.latest_url).send().await?).await
        }
    }
    async fn download(&self, url: &str, sha: &str, expected: u64) -> Result<PathBuf, AppError> {
        validate_url(url)?;
        if expected == 0
            || expected > 64 * 1024 * 1024
            || sha.len() != 64
            || !sha.bytes().all(|b| b.is_ascii_hexdigit())
        {
            return Err(super::protocol("snapshot_metadata"));
        }
        tokio::fs::create_dir_all(&self.cache).await?;
        let path = self
            .cache
            .join(format!("{}.json.gz", sha.to_ascii_lowercase()));
        if path.is_file() {
            return Ok(path);
        }
        let temporary = self
            .cache
            .join(format!("download-{}.tmp", uuid::Uuid::now_v7()));
        let _cleanup = Download(temporary.clone());
        let mut response = self.client()?.get(url).send().await?.error_for_status()?;
        let mut options = tokio::fs::OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(unix)]
        options.mode(0o600);
        let mut file = options.open(&temporary).await?;
        let mut hash = Sha256::new();
        let mut bytes = 0u64;
        while let Some(chunk) = response.chunk().await? {
            bytes += chunk.len() as u64;
            if bytes > expected {
                return Err(AppError::new("E_CHECKSUM", "snapshot_checksum"));
            }
            hash.update(&chunk);
            file.write_all(&chunk).await?;
        }
        file.sync_all().await?;
        drop(file);
        if bytes != expected || format!("{:x}", hash.finalize()) != sha.to_ascii_lowercase() {
            return Err(AppError::new("E_CHECKSUM", "snapshot_checksum"));
        }
        tokio::fs::rename(&temporary, &path).await?;
        Ok(path)
    }
    async fn snapshot(&self, metadata: &super::SnapshotInfo) -> Result<u32, AppError> {
        self.prune_cache(metadata).await?;
        let locale = self
            .settings
            .read()
            .map_err(|_| AppError::new("E_UNKNOWN", "settings_lock"))?
            .locale;
        let path = self
            .download(&metadata.url, &metadata.sha256, metadata.bytes)
            .await?;
        let read_path = path.clone();
        let info = metadata.clone();
        let base = crate::blocking(move || super::v2::read_base(&read_path, &info)).await?;
        let mut paths = Vec::new();
        for locale in super::v2::needed_packs(&base, metadata, locale) {
            let pack = &metadata.text_packs[&locale];
            paths.push((
                locale,
                self.download(&pack.url, &pack.sha256, pack.bytes).await?,
            ));
        }
        let database = self.database.clone();
        let info = metadata.clone();
        crate::blocking(move || {
            super::v2::import_files(
                &database,
                &path,
                &info,
                &paths,
                locale,
                chrono::Utc::now().timestamp_millis(),
            )
        })
        .await
    }
    async fn text(&self, metadata: &super::SnapshotInfo) -> Result<u32, AppError> {
        self.prune_cache(metadata).await?;
        let locale = self
            .settings
            .read()
            .map_err(|_| AppError::new("E_UNKNOWN", "settings_lock"))?
            .locale;
        let db = self.database.clone();
        let needed = crate::blocking(move || {
            db.with(|c| {
                let mut needed = std::collections::HashSet::from([locale, super::Locale::EnUs]);
                let data = c
                    .prepare("SELECT data FROM catalog_items")?
                    .query_map([], |r| r.get::<_, String>(0))?
                    .collect::<Result<Vec<_>, _>>()?;
                for row in data {
                    let item: super::CatalogItem = serde_json::from_str(&row)?;
                    needed.insert(item.source_locale);
                }
                for category in super::load_categories(c)? {
                    needed.insert(category.source_locale);
                }
                Ok(needed)
            })
        })
        .await?;
        let mut packs = Vec::new();
        for locale in needed {
            // prune_cache checked every published pack; a language without one falls back to English.
            let Some(info) = metadata.text_packs.get(&locale) else {
                continue;
            };
            let old = self
                .database
                .meta(&format!("text_version:{}", locale.as_str()))?
                .and_then(|s| s.parse::<i64>().ok())
                .unwrap_or(0);
            if old >= info.version {
                continue;
            }
            let path = self.download(&info.url, &info.sha256, info.bytes).await?;
            let info = info.clone();
            packs.push(crate::blocking(move || super::v2::read_pack(&path, &info)).await?);
        }
        if packs.is_empty() {
            return Ok(0);
        }
        let db = self.database.clone();
        crate::blocking(move || {
            super::v2::apply_text(&db, &packs, chrono::Utc::now().timestamp_millis())
        })
        .await
    }
    async fn prune_cache(&self, metadata: &SnapshotInfo) -> Result<(), AppError> {
        if metadata.format_version != 2 || metadata.cursor < 0 {
            return Err(protocol("snapshot_metadata"));
        }
        let mut keep = HashSet::new();
        for (sha, bytes, url) in
            std::iter::once((&metadata.sha256, metadata.bytes, metadata.url.as_str())).chain(
                metadata
                    .text_packs
                    .values()
                    .map(|pack| (&pack.sha256, pack.bytes, pack.url.as_str())),
            )
        {
            if bytes == 0
                || bytes > 64 * 1024 * 1024
                || sha.len() != 64
                || !sha.bytes().all(|b| b.is_ascii_hexdigit())
            {
                return Err(protocol("snapshot_metadata"));
            }
            validate_url(url)?;
            keep.insert(format!("{}.json.gz", sha.to_ascii_lowercase()));
        }
        super::v2::check_packs(metadata)?;
        let mut entries = match tokio::fs::read_dir(&self.cache).await {
            Ok(entries) => entries,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(()),
            Err(error) => return Err(error.into()),
        };
        // Caller holds the sync lock; retain only the current manifest. Offline SQLite data does not depend on compressed cache.
        while let Some(entry) = entries.next_entry().await? {
            let name = entry.file_name();
            let Some(name) = name.to_str() else {
                continue;
            };
            let Some(sha) = name.strip_suffix(".json.gz") else {
                continue;
            };
            if sha.len() == 64
                && sha.bytes().all(|b| b.is_ascii_hexdigit())
                && !keep.contains(name)
                && entry.file_type().await?.is_file()
            {
                tokio::fs::remove_file(entry.path()).await?;
            }
        }
        Ok(())
    }
    pub async fn sync_text(&self) -> Result<SyncResult, AppError> {
        let _lock = self.lock.lock().await;
        self.syncing.store(true, Ordering::Release);
        let _reset = Reset(&self.syncing);
        if self.status().await?.item_count == 0 {
            return self.finish(SyncMode::None, 0).await;
        }
        let metadata = self.metadata(&self.client()?).await?;
        let applied = self.text(&metadata).await?;
        self.finish(
            if applied == 0 {
                SyncMode::None
            } else {
                SyncMode::Delta
            },
            applied,
        )
        .await
    }
    async fn finish(&self, mode: SyncMode, applied: u32) -> Result<SyncResult, AppError> {
        let mut status = self.status().await?;
        status.syncing = false;
        (self.sink)(CoreEvent::Catalog(status.clone()));
        Ok(SyncResult {
            mode,
            applied,
            cursor: status.cursor,
        })
    }
    pub async fn sync(&self, force: bool) -> Result<SyncResult, AppError> {
        let _lock = self.lock.lock().await;
        self.syncing.store(true, Ordering::Release);
        let _reset = Reset(&self.syncing);
        let had_links_error = self
            .database
            .meta("links_last_error")
            .ok()
            .flatten()
            .is_some();
        // Independent sources: a slow snapshot must not delay the small links catalog.
        let (mut result, links) = tokio::join!(self.sync_inner(force), self.sync_links(force));
        // Snapshots/deltas may commit before sync fails; the UI must still read the available offline catalog.
        let mut publish = result.is_err();
        match links {
            Ok(applied) => {
                // `sync_inner` published before the links source answered: say so again if it changed anything,
                // or if it just cleared an error the UI still shows.
                publish |= applied > 0 || had_links_error;
                if let Ok(result) = &mut result {
                    result.applied += applied;
                    if applied > 0 && result.mode == SyncMode::None {
                        result.mode = SyncMode::Delta;
                    }
                }
            }
            Err(error) => {
                // The failure is published as `links_error` and shown by the UI. It must not fail the sync as a whole:
                // casks that arrived still need the library reclassified and the updates rebuilt, and the scheduler
                // would otherwise retry every minute for as long as the links source stays unavailable.
                let _ = self.database.set_meta("links_last_error", &error.code);
                log::warn!("links sync failed: {} ({})", error.code, error.message);
                publish = true;
            }
        }
        if publish {
            let _ = self.finish(SyncMode::None, 0).await;
        }
        result
    }
    async fn sync_links(&self, force: bool) -> Result<u32, AppError> {
        // locales=all: notes in every desktop language (older clients get the original six).
        let url = format!("{}/catalog/links?locales=all", self.base);
        validate_url(&url)?;
        let mut request = self.client()?.get(url);
        // `links_etag` is the catalog's own tag and says which content the local rows hold. `links_validator` is the
        // ETag header of the response that delivered it, as the server and every proxy before it sent it. A proxy that
        // compresses the body suffixes and weakens that header and accepts only its own form back, so that form is what
        // a conditional request has to carry; the two are not required to be equal.
        let cached = self.database.meta("links_etag")?;
        if !force && let Some(etag) = &cached {
            let validator = self.database.meta("links_validator")?;
            request = request.header("If-None-Match", validator.as_ref().unwrap_or(etag));
        }
        let response = request.send().await?;
        if response.status() == reqwest::StatusCode::NOT_MODIFIED {
            if cached.is_none() || force {
                return Err(protocol("links_unexpected_304"));
            }
            self.database.with(|conn| {
                let tx = conn.transaction()?;
                super::set_meta(
                    &tx,
                    "links_synced_at",
                    &chrono::Utc::now().timestamp_millis().to_string(),
                )?;
                tx.execute("DELETE FROM meta WHERE key='links_last_error'", [])?;
                tx.commit()?;
                Ok(())
            })?;
            return Ok(0);
        }
        let validator = response
            .headers()
            .get("ETag")
            .and_then(|h| h.to_str().ok())
            .map(str::to_owned);
        let response = json::<Response<super::links::LinkCatalog>>(response).await?;
        if response.code != "0000" {
            return Err(protocol("links_response"));
        }
        // The body is the authority on its own version. The header is not compared with it: an intermediary that
        // compresses the response rewrites the header, and the body, which `apply` validates, is what gets stored.
        let catalog = response.data.ok_or_else(|| protocol("links_response"))?;
        let database = self.database.clone();
        crate::blocking(move || {
            super::links::apply(
                &database,
                &catalog,
                validator.as_deref(),
                chrono::Utc::now().timestamp_millis(),
            )
        })
        .await
    }
    async fn sync_inner(&self, force: bool) -> Result<SyncResult, AppError> {
        let client = self.client()?;
        let mut metadata = self.metadata(&client).await?;
        let mut status = self.status().await?;
        let database = self.database.clone();
        let imported = crate::blocking(move || database.meta("snapshot_imported_at"))
            .await?
            .and_then(|s| s.parse::<i64>().ok());
        let now = chrono::Utc::now().timestamp_millis();
        let stale = imported.is_none_or(|at| now.saturating_sub(at) >= 86_400_000);
        let must_snapshot = force
            || self.database.meta("snapshot_format")?.as_deref() != Some("2")
            || status.item_count == 0
            || stale
            || metadata.cursor.saturating_sub(status.cursor) > 5000;
        let mut mode = SyncMode::Delta;
        let mut applied = 0u32;
        if must_snapshot {
            applied = self.snapshot(&metadata).await?;
            mode = SyncMode::Snapshot;
            status = self.status().await?;
        }
        // Full snapshots may lag live catalogs; catch up with deltas from the imported cursor.
        let mut finished = false;
        let mut recovered_expiry = false;
        for _ in 0..10_000 {
            let before = status.cursor;
            let url = format!(
                "{}/catalog/changes?since={}&limit=500&locales=all",
                self.base, status.cursor
            );
            validate_url(&url)?;
            let response = client.get(url).send().await?;
            let body = if response.status() == reqwest::StatusCode::GONE {
                None
            } else {
                Some(json::<Response<Changes>>(response).await?)
            };
            if body.as_ref().is_none_or(|body| body.code == "1201") {
                // Do not repeatedly reimport expired snapshots; retain imported data and mark sync failed.
                if recovered_expiry {
                    return Err(protocol("changes_response"));
                }
                recovered_expiry = true;
                metadata = self.metadata(&client).await?;
                applied += self.snapshot(&metadata).await?;
                mode = SyncMode::Snapshot;
                status = self.status().await?;
                continue;
            }
            let body = body.ok_or_else(|| protocol("changes_response"))?;
            if body.code != "0000" {
                return Err(protocol("changes_response"));
            }
            let changes = body.data.ok_or_else(|| protocol("changes_response"))?;
            let more = changes.has_more;
            let database = self.database.clone();
            applied +=
                crate::blocking(move || super::apply_changes(&database, &changes, now)).await?;
            status = self.status().await?;
            if !more {
                finished = true;
                break;
            }
            if status.cursor <= before {
                return Err(protocol("nonprogressing_changes"));
            }
        }
        if !finished {
            return Err(protocol("changes_page_limit"));
        }
        applied += self.text(&metadata).await?;
        if mode == SyncMode::Delta && applied == 0 {
            mode = SyncMode::None;
        }
        let mut status = self.status().await?;
        status.syncing = false;
        (self.sink)(CoreEvent::Catalog(status.clone()));
        Ok(SyncResult {
            mode,
            applied,
            cursor: status.cursor,
        })
    }
}
pub fn configured_base() -> String {
    std::env::var("OPENNAVO_API_BASE").unwrap_or_else(|_| {
        option_env!("VITE_API_BASE")
            .unwrap_or("http://localhost:8080/api/v1")
            .into()
    })
}
fn validate_url(value: &str) -> Result<(), AppError> {
    let url = reqwest::Url::parse(value).map_err(|_| protocol("catalog_url"))?;
    if !matches!(url.scheme(), "http" | "https")
        || !url.username().is_empty()
        || url.password().is_some()
    {
        return Err(protocol("catalog_url"));
    }
    Ok(())
}
async fn json<T: serde::de::DeserializeOwned + Send + 'static>(
    response: reqwest::Response,
) -> Result<T, AppError> {
    let mut response = response.error_for_status()?;
    let mut bytes = Vec::new();
    while let Some(chunk) = response.chunk().await? {
        if bytes.len() + chunk.len() > 16 * 1024 * 1024 {
            return Err(protocol("catalog_response_too_large"));
        }
        bytes.extend_from_slice(&chunk);
    }
    crate::blocking(move || {
        serde_json::from_slice(&bytes)
            .map_err(|e| protocol("catalog_response_json").with_detail(e.to_string()))
    })
    .await
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::{Kind, Locale};
    use std::io::{Read, Write};

    #[tokio::test]
    async fn partial_sync_reports_links_failure_then_recovers_without_losing_casks() {
        let temp = tempfile::tempdir().unwrap();
        let cache = temp.path().join("cache");
        std::fs::create_dir(&cache).unwrap();
        let info = cache_build(&cache, 1);
        let db = Database::open(&temp.path().join("catalog.db")).unwrap();
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let metadata = serde_json::json!({"code":"0000","data":info}).to_string();
        let changes = serde_json::json!({"code":"0000","data":{
            "changes":[], "nextCursor":info.cursor, "hasMore":false
        }})
        .to_string();
        let mut item = super::super::links::tests::item();
        item.token = "ely-office".into();
        let links = serde_json::json!({"code":"0000","data":{
            "etag":"\"recovered\"", "items":[item]
        }})
        .to_string();
        // What the links requests carried as If-None-Match, in order.
        let conditions = Arc::new(std::sync::Mutex::new(Vec::<Option<String>>::new()));
        let seen = conditions.clone();
        let server = std::thread::spawn(move || {
            let mut links_requests = 0;
            for _ in 0..9 {
                let (mut stream, _) = listener.accept().unwrap();
                stream
                    .set_read_timeout(Some(Duration::from_secs(5)))
                    .unwrap();
                let mut request = Vec::new();
                let mut byte = [0];
                while !request.ends_with(b"\r\n\r\n") {
                    stream.read_exact(&mut byte).unwrap();
                    request.push(byte[0]);
                }
                let request = String::from_utf8(request).unwrap();
                // Compressing in front of the API rewrites the tag: the header never equals the catalog's own.
                let (status, headers, body) =
                    if request.starts_with("GET /catalog/links?locales=all ") {
                        links_requests += 1;
                        seen.lock().unwrap().push(header(&request, "if-none-match"));
                        match links_requests {
                            1 => ("503 Unavailable", "", ""),
                            2 => ("200 OK", "ETag: W/\"recovered-gzip\"\r\n", links.as_str()),
                            _ => ("304 Not Modified", "ETag: W/\"recovered-gzip\"\r\n", ""),
                        }
                    } else if request.starts_with("GET /catalog/snapshot?locales=all ") {
                        ("200 OK", "", metadata.as_str())
                    } else {
                        assert!(request.starts_with("GET /catalog/changes?"));
                        ("200 OK", "", changes.as_str())
                    };
                write!(
                    stream,
                    "HTTP/1.1 {status}\r\n{headers}Content-Length: {}\r\nConnection: close\r\n\r\n{body}",
                    body.len()
                )
                .unwrap();
            }
        });
        let emitted = Arc::new(std::sync::Mutex::new(Vec::new()));
        let sink = emitted.clone();
        let service = CatalogService::new(
            db.clone(),
            base,
            String::new(),
            cache,
            Arc::new(RwLock::new(Settings::default())),
            Arc::new(move |event| {
                if let CoreEvent::Catalog(status) = event {
                    sink.lock().unwrap().push(status);
                }
            }),
        );
        service.snapshot(&info).await.unwrap();
        // The links source is down: the casks still sync and the result is Ok, so the library is reclassified and the
        // scheduler does not retry every minute. The failure is published for the UI instead.
        service.sync(false).await.unwrap();
        assert_eq!(service.status().await.unwrap().item_count, 40);
        assert!(
            emitted
                .lock()
                .unwrap()
                .last()
                .unwrap()
                .links_error
                .is_some()
        );
        service.sync(false).await.unwrap();
        assert_eq!(service.status().await.unwrap().item_count, 41);
        assert!(
            emitted
                .lock()
                .unwrap()
                .last()
                .unwrap()
                .links_error
                .is_none()
        );
        let found: i64 = db
            .with(|c| {
                Ok(c.query_row(
                    "SELECT count(*) FROM catalog_fts WHERE catalog_fts MATCH '\"ely-office\"'",
                    [],
                    |r| r.get(0),
                )?)
            })
            .unwrap();
        assert_eq!(found, 1);
        // Unchanged: the tag the proxy sent goes back as it came, and the cached links stay.
        service.sync(false).await.unwrap();
        assert_eq!(service.status().await.unwrap().item_count, 41);
        assert!(
            emitted
                .lock()
                .unwrap()
                .last()
                .unwrap()
                .links_error
                .is_none()
        );
        server.join().unwrap();
        assert_eq!(
            *conditions.lock().unwrap(),
            [None, None, Some("W/\"recovered-gzip\"".to_string())]
        );
        assert_eq!(
            db.meta("links_etag").unwrap().as_deref(),
            Some("\"recovered\"")
        );
    }

    /// Value of a request header (any case), if the request has it.
    fn header(request: &str, name: &str) -> Option<String> {
        request.lines().find_map(|line| {
            let (key, value) = line.split_once(':')?;
            key.trim()
                .eq_ignore_ascii_case(name)
                .then(|| value.trim().to_string())
        })
    }

    /// Serves one scripted response per connection and checks the conditional header of each request. A script entry is
    /// (If-None-Match the client must send, status line, extra response headers, body).
    fn serve_links(
        listener: std::net::TcpListener,
        script: Vec<(Option<&'static str>, &'static str, &'static str, String)>,
    ) -> std::thread::JoinHandle<()> {
        std::thread::spawn(move || {
            for (round, (expected, status, headers, body)) in script.into_iter().enumerate() {
                let (mut stream, _) = listener.accept().unwrap();
                stream
                    .set_read_timeout(Some(Duration::from_secs(5)))
                    .unwrap();
                let mut request = Vec::new();
                let mut byte = [0u8; 1];
                while !request.ends_with(b"\r\n\r\n") {
                    stream.read_exact(&mut byte).unwrap();
                    request.push(byte[0]);
                }
                let request = String::from_utf8(request).unwrap();
                assert!(request.starts_with("GET /catalog/links?locales=all "));
                assert_eq!(
                    header(&request, "if-none-match").as_deref(),
                    expected,
                    "If-None-Match of request {round}"
                );
                write!(
                    stream,
                    "HTTP/1.1 {status}\r\n{headers}Content-Length: {}\r\nConnection: close\r\n\r\n{body}",
                    body.len()
                )
                .unwrap();
            }
        })
    }

    fn links_body(etag: &str, token: &str) -> String {
        let mut item = super::super::links::tests::item();
        item.token = token.into();
        serde_json::json!({"code":"0000","data":{"etag":etag,"items":[item]}}).to_string()
    }

    #[tokio::test]
    async fn links_sync_survives_an_edge_that_rewrites_the_etag_header() {
        let temp = tempfile::tempdir().unwrap();
        let db = Database::open(&temp.path().join("catalog.db")).unwrap();
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let server = serve_links(
            listener,
            vec![
                // Compressed at the edge: the tag gains a suffix and Cloudflare weakens it. The body is intact.
                (
                    None,
                    "200 OK",
                    "ETag: W/\"one-gzip\"\r\n",
                    links_body("\"one\"", "first-link"),
                ),
                // Only the form the edge produced is understood by it, never the catalog's own tag.
                (
                    Some("W/\"one-gzip\""),
                    "304 Not Modified",
                    "ETag: W/\"one-gzip\"\r\n",
                    String::new(),
                ),
                // The catalog changed: a new body with a new header replaces both.
                (
                    Some("W/\"one-gzip\""),
                    "200 OK",
                    "ETag: \"two-gzip\"\r\n",
                    links_body("\"two\"", "second-link"),
                ),
                (
                    Some("\"two-gzip\""),
                    "304 Not Modified",
                    "ETag: \"two-gzip\"\r\n",
                    String::new(),
                ),
                // A forced sync asks for the whole catalog; a response without a tag clears the stored header form.
                (None, "200 OK", "", links_body("\"two\"", "second-link")),
                (Some("\"two\""), "304 Not Modified", "", String::new()),
            ],
        );
        let service = CatalogService::new(
            db.clone(),
            base,
            String::new(),
            temp.path().join("cache"),
            Arc::new(RwLock::new(Settings::default())),
            Arc::new(|_| {}),
        );
        assert_eq!(service.sync_links(false).await.unwrap(), 1);
        assert_eq!(db.meta("links_etag").unwrap().as_deref(), Some("\"one\""));
        assert_eq!(
            db.meta("links_validator").unwrap().as_deref(),
            Some("W/\"one-gzip\"")
        );
        assert_eq!(service.sync_links(false).await.unwrap(), 0);
        assert!(db.meta("links_last_error").unwrap().is_none());
        assert_eq!(service.sync_links(false).await.unwrap(), 1);
        assert_eq!(db.meta("links_etag").unwrap().as_deref(), Some("\"two\""));
        assert_eq!(
            db.meta("links_validator").unwrap().as_deref(),
            Some("\"two-gzip\"")
        );
        assert!(
            super::super::get(&db, Kind::Link, "first-link")
                .unwrap()
                .is_none()
        );
        assert!(
            super::super::get(&db, Kind::Link, "second-link")
                .unwrap()
                .is_some()
        );
        assert_eq!(service.sync_links(false).await.unwrap(), 0);
        assert_eq!(service.sync_links(true).await.unwrap(), 0);
        assert!(db.meta("links_validator").unwrap().is_none());
        assert_eq!(service.sync_links(false).await.unwrap(), 0);
        server.join().unwrap();
    }

    #[tokio::test]
    async fn a_known_good_links_response_clears_an_earlier_failure_even_when_nothing_changed() {
        let temp = tempfile::tempdir().unwrap();
        let db = Database::open(&temp.path().join("catalog.db")).unwrap();
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let server = serve_links(
            listener,
            vec![
                (
                    None,
                    "200 OK",
                    "ETag: \"one\"\r\n",
                    links_body("\"one\"", "stable"),
                ),
                (Some("\"one\""), "503 Unavailable", "", String::new()),
                // A shared cache may answer in full again even though the client presented the tag.
                (
                    Some("\"one\""),
                    "200 OK",
                    "ETag: \"one\"\r\n",
                    links_body("\"one\"", "stable"),
                ),
            ],
        );
        let service = CatalogService::new(
            db.clone(),
            base,
            String::new(),
            temp.path().join("cache"),
            Arc::new(RwLock::new(Settings::default())),
            Arc::new(|_| {}),
        );
        assert_eq!(service.sync_links(false).await.unwrap(), 1);
        assert!(service.sync_links(false).await.is_err());
        db.set_meta("links_last_error", "E_NETWORK").unwrap();
        assert_eq!(service.sync_links(false).await.unwrap(), 0);
        assert!(db.meta("links_last_error").unwrap().is_none());
        assert!(db.meta("links_synced_at").unwrap().is_some());
        server.join().unwrap();
    }

    #[tokio::test]
    async fn links_etag_offline_cache_and_cask_snapshot_are_independent() {
        let temp = tempfile::tempdir().unwrap();
        let db = Database::open(&temp.path().join("catalog.db")).unwrap();
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let catalog = super::super::links::LinkCatalog {
            etag: "\"one\"".into(),
            items: vec![super::super::links::tests::item()],
        };
        let raw = serde_json::json!({"code":"0000","data":catalog}).to_string();
        let server = std::thread::spawn(move || {
            for index in 0..4 {
                let (mut stream, _) = listener.accept().unwrap();
                stream
                    .set_read_timeout(Some(Duration::from_secs(5)))
                    .unwrap();
                let mut request = Vec::new();
                let mut b = [0u8; 1];
                while !request.ends_with(b"\r\n\r\n") {
                    stream.read_exact(&mut b).unwrap();
                    request.push(b[0]);
                }
                let request = String::from_utf8(request).unwrap().to_lowercase();
                assert!(request.starts_with("get /catalog/links?locales=all "));
                if index > 0 {
                    assert!(request.contains("if-none-match: \"one\""));
                }
                let (status, body) = match index {
                    0 => ("200 OK", raw.as_str()),
                    1 => ("304 Not Modified", ""),
                    2 => ("503 Unavailable", ""),
                    // An invalid catalog (its own tag is unusable) is rejected whatever the headers say.
                    _ => (
                        "200 OK",
                        "{\"code\":\"0000\",\"data\":{\"etag\":\"x\",\"items\":[]}}",
                    ),
                };
                write!(stream,"HTTP/1.1 {status}\r\nETag: \"one\"\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",body.len()).unwrap();
            }
        });
        let cache = temp.path().join("cache");
        std::fs::create_dir(&cache).unwrap();
        let service = CatalogService::new(
            db.clone(),
            base,
            String::new(),
            cache.clone(),
            Arc::new(RwLock::new(Settings::default())),
            Arc::new(|_| {}),
        );
        assert_eq!(service.sync_links(false).await.unwrap(), 1);
        assert_eq!(service.sync_links(false).await.unwrap(), 0);
        assert!(service.sync_links(false).await.is_err());
        assert!(service.sync_links(false).await.is_err());
        server.join().unwrap();
        assert_eq!(db.meta("links_etag").unwrap().as_deref(), Some("\"one\""));
        let info = cache_build(&cache, 1);
        service.snapshot(&info).await.unwrap();
        assert_eq!(service.status().await.unwrap().item_count, 41);
        assert!(
            super::super::get(&db, Kind::Link, "external-example")
                .unwrap()
                .is_some()
        );
        let found:i64=db.with(|c|Ok(c.query_row("SELECT count(*) FROM catalog_fts WHERE kind='link' AND catalog_fts MATCH 'External'",[],|r|r.get(0))?)).unwrap();
        assert_eq!(found, 1);
    }

    fn cache_build(cache: &std::path::Path, generation: i64) -> SnapshotInfo {
        let fixtures = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("../../../apps/server/testdata/desktop/v2");
        // What the API returns for locales=all: a pack for every desktop language.
        let response: serde_json::Value =
            serde_json::from_slice(&std::fs::read(fixtures.join("snapshot-all.json")).unwrap())
                .unwrap();
        let mut info: SnapshotInfo = serde_json::from_value(response["data"].clone()).unwrap();
        let encode = |name: &str, version: Option<i64>| {
            let mut raw = String::new();
            flate2::read::GzDecoder::new(std::fs::File::open(fixtures.join(name)).unwrap())
                .read_to_string(&mut raw)
                .unwrap();
            if let Some(version) = version {
                let mut value: serde_json::Value = serde_json::from_str(&raw).unwrap();
                value["version"] = version.into();
                raw = serde_json::to_string(&value).unwrap();
            }
            raw.push_str(&" ".repeat(generation as usize));
            let mut writer =
                flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::default());
            writer.write_all(raw.as_bytes()).unwrap();
            let bytes = writer.finish().unwrap();
            let sha = format!("{:x}", Sha256::digest(&bytes));
            std::fs::write(cache.join(format!("{sha}.json.gz")), &bytes).unwrap();
            (sha, bytes.len() as u64)
        };
        (info.sha256, info.bytes) = encode("catalog.json.gz", None);
        for (locale, pack) in &mut info.text_packs {
            pack.version += generation;
            (pack.sha256, pack.bytes) = encode(
                &format!("text-{}.json.gz", locale.as_str()),
                Some(pack.version),
            );
        }
        info
    }

    #[tokio::test]
    async fn language_pack_reapplies_after_full_import_in_another_locale() {
        let temp = tempfile::tempdir().unwrap();
        let cache = temp.path().join("cache");
        std::fs::create_dir(&cache).unwrap();
        let old_info = cache_build(&cache, 1);
        let db = Database::open(&temp.path().join("catalog.db")).unwrap();
        let settings = Arc::new(RwLock::new(Settings {
            locale: Locale::EnUs,
            ..Settings::default()
        }));
        let service = CatalogService::new(
            db.clone(),
            String::new(),
            String::new(),
            cache.clone(),
            settings.clone(),
            Arc::new(|_| {}),
        );
        let _lock = service.lock.lock().await;
        service.snapshot(&old_info).await.unwrap();
        let read = |sha: &str| -> serde_json::Value {
            serde_json::from_reader(flate2::read::GzDecoder::new(
                std::fs::File::open(cache.join(format!("{sha}.json.gz"))).unwrap(),
            ))
            .unwrap()
        };
        let write = |value: &serde_json::Value| {
            let mut writer =
                flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::default());
            writer
                .write_all(&serde_json::to_vec(value).unwrap())
                .unwrap();
            let bytes = writer.finish().unwrap();
            let sha = format!("{:x}", Sha256::digest(&bytes));
            std::fs::write(cache.join(format!("{sha}.json.gz")), &bytes).unwrap();
            (sha, bytes.len() as u64)
        };
        let mut info = old_info.clone();
        info.cursor += 1;
        info.item_count += 1;
        let mut base = read(&info.sha256);
        let mut added = base["items"][0].clone();
        added["token"] = "new-language-app".into();
        base["items"].as_array_mut().unwrap().push(added);
        base["cursor"] = info.cursor.into();
        (info.sha256, info.bytes) = write(&base);
        for (locale, pack) in &mut info.text_packs {
            let mut data = read(&pack.sha256);
            pack.cursor = info.cursor;
            pack.version += 1;
            data["cursor"] = pack.cursor.into();
            data["version"] = pack.version.into();
            if *locale == Locale::JaJp {
                data["items"].as_array_mut().unwrap().push(serde_json::json!({
                    "kind":"cask", "token":"new-language-app", "displayName":"新しいアプリ", "summary":null
                }));
            }
            (pack.sha256, pack.bytes) = write(&data);
        }
        settings.write().unwrap().locale = Locale::JaJp;
        service.text(&info).await.unwrap();
        assert!(db.meta("text_version:ja-JP").unwrap().is_none());
        settings.write().unwrap().locale = Locale::EnUs;
        service.snapshot(&info).await.unwrap();
        // Invalidate erroneous old completion markers during full sync so they cannot block the next locale switch.
        assert!(db.meta("text_version:ja-JP").unwrap().is_none());
        settings.write().unwrap().locale = Locale::JaJp;
        assert!(service.text(&info).await.unwrap() > 0);
        let item = super::super::get(&db, Kind::Cask, "new-language-app")
            .unwrap()
            .unwrap();
        assert_eq!(item.display_name.get(Locale::JaJp).unwrap(), "新しいアプリ");
        assert_eq!(service.text(&info).await.unwrap(), 0);
    }

    // An older or rolled-back server publishes only the original six packs: German still syncs, with English text.
    #[tokio::test]
    async fn a_language_without_a_published_pack_falls_back_to_english() {
        let temp = tempfile::tempdir().unwrap();
        let cache = temp.path().join("cache");
        std::fs::create_dir(&cache).unwrap();
        let mut info = cache_build(&cache, 1);
        info.text_packs.remove(&Locale::DeDe);
        let db = Database::open(&temp.path().join("catalog.db")).unwrap();
        let settings = Arc::new(RwLock::new(Settings {
            locale: Locale::DeDe,
            ..Settings::default()
        }));
        let service = CatalogService::new(
            db.clone(),
            String::new(),
            String::new(),
            cache,
            settings,
            Arc::new(|_| {}),
        );
        let _lock = service.lock.lock().await;
        assert_eq!(service.snapshot(&info).await.unwrap(), 40);
        assert_eq!(service.text(&info).await.unwrap(), 0);
        assert!(db.meta("text_version:de-DE").unwrap().is_none());
        let item = super::super::get(&db, Kind::Cask, "visual-studio-code")
            .unwrap()
            .unwrap();
        assert_eq!(
            item.display_name
                .fallback(Locale::DeDe, item.source_locale)
                .unwrap(),
            "Visual Studio Code"
        );
        // English is the fallback for every language, so a snapshot without it is still rejected.
        let mut english = info.clone();
        english.text_packs.remove(&Locale::EnUs);
        assert_eq!(
            service.snapshot(&english).await.unwrap_err().message,
            "missing_text_pack"
        );
    }

    #[tokio::test]
    async fn cache_retains_only_current_build_for_snapshot_and_language_sync() {
        let temp = tempfile::tempdir().unwrap();
        let cache = temp.path().join("cache");
        std::fs::create_dir(&cache).unwrap();
        let unrelated = cache.join("notes.json.gz");
        std::fs::write(&unrelated, "keep").unwrap();
        let directory = cache.join(format!("{}.json.gz", "e".repeat(64)));
        std::fs::create_dir(&directory).unwrap();
        let db = Database::open(&temp.path().join("catalog.db")).unwrap();
        let settings = Arc::new(RwLock::new(Settings::default()));
        let service = CatalogService::new(
            db.clone(),
            String::new(),
            String::new(),
            cache.clone(),
            settings.clone(),
            Arc::new(|_| {}),
        );
        let _lock = service.lock.lock().await;
        // The base file, one pack per language, and the unrelated file and directory left alone.
        let files = crate::locales_gen::LOCALES.len() + 3;
        for generation in 1..=12 {
            let old_files = std::fs::read_dir(&cache)
                .unwrap()
                .map(|entry| entry.unwrap().path())
                .filter(|path| path.is_file() && path != &unrelated)
                .collect::<Vec<_>>();
            let info = cache_build(&cache, generation);
            if generation % 2 == 1 {
                assert_eq!(service.snapshot(&info).await.unwrap(), 40);
            } else {
                for locale in crate::locales_gen::LOCALES {
                    settings.write().unwrap().locale = locale;
                    service.text(&info).await.unwrap();
                }
            }
            assert!(old_files.iter().all(|path| !path.exists()));
            assert!(cache.join(format!("{}.json.gz", info.sha256)).is_file());
            for pack in info.text_packs.values() {
                assert!(cache.join(format!("{}.json.gz", pack.sha256)).is_file());
            }
            assert_eq!(std::fs::read_dir(&cache).unwrap().count(), files);
            assert_eq!(std::fs::read_to_string(&unrelated).unwrap(), "keep");
            assert!(directory.is_dir());
            let cursor = db.meta("catalog_cursor").unwrap();
            let hash = db.meta("snapshot_sha256").unwrap();
            let mut invalid = info.clone();
            invalid.sha256 = "invalid".into();
            assert!(service.prune_cache(&invalid).await.is_err());
            assert_eq!(std::fs::read_dir(&cache).unwrap().count(), files);
            std::fs::write(cache.join(format!("{}.json.gz", info.sha256)), "bad").unwrap();
            assert_eq!(
                service.snapshot(&info).await.unwrap_err().code,
                "E_CHECKSUM"
            );
            assert_eq!(db.meta("catalog_cursor").unwrap(), cursor);
            assert_eq!(db.meta("snapshot_sha256").unwrap(), hash);
            assert_eq!(service.status().await.unwrap().item_count, 40);
        }
    }
}
