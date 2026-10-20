use super::{feed, prefs, version};
use crate::{
    AppError,
    db::Database,
    events::{CoreEvent, EventSink},
    model::*,
    uninstall::rules::Snapshot,
};
use rusqlite::OptionalExtension;
use serde::{Deserialize, Serialize};
use std::{
    path::{Path, PathBuf},
    sync::{Arc, RwLock},
    time::Duration,
};
use tokio::sync::Mutex;

#[cfg(test)]
#[path = "skip_tests.rs"]
mod skip_tests;

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FeedRule {
    pub bundle_id: String,
    pub source: UpdateSource,
    pub feed_url: Option<String>,
    pub team_id: Option<String>,
    pub channel: Option<UpdateChannel>,
    pub token: Option<String>,
    pub kind: Option<Kind>,
    pub release: Option<super::release::Release>,
}
#[derive(Serialize, Deserialize)]
struct RulesResponse {
    items: Vec<FeedRule>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Candidate {
    pub item: OutdatedItem,
    pub app_path: PathBuf,
    pub snapshot: Snapshot,
    pub feed_item: Option<feed::Item>,
    pub public_key: Option<String>,
    pub artifact: Option<super::release::Artifact>,
    pub expected_team: Option<String>,
    pub checked_at: i64,
}
impl Candidate {
    pub fn validate_identity(&self) -> Result<(), AppError> {
        if self.item.expected_team_match == Some(false)
            || (matches!(
                self.item.source,
                UpdateSource::Github | UpdateSource::Vendor
            ) && self.expected_team.as_ref().is_none_or(|t| t.is_empty()))
        {
            return Err(AppError::new(
                "E_UPDATE_TEAM_MISMATCH",
                "rule_team_mismatch",
            ));
        }
        Ok(())
    }
}
#[derive(Serialize, Deserialize)]
struct CachedFeed {
    url: String,
    etag: Option<String>,
    body: String,
}
#[derive(Serialize, Deserialize)]
struct FeedRetry {
    url: String,
    until: i64,
}
fn retry_after(value: Option<&str>, now: i64) -> i64 {
    value
        .and_then(|value| {
            value
                .parse::<i64>()
                .ok()
                .filter(|seconds| *seconds >= 0)
                .map(|seconds| now.saturating_add(seconds))
                .or_else(|| {
                    chrono::DateTime::parse_from_rfc2822(value)
                        .ok()
                        .map(|date| date.timestamp())
                })
        })
        .filter(|until| *until > now)
        .unwrap_or_else(|| now.saturating_add(60))
}

pub struct Service {
    pub database: Database,
    pub apps: Arc<crate::uninstall::Engine>,
    pub settings: Arc<RwLock<Settings>>,
    sink: EventSink,
    client: reqwest::Client,
    check_lock: Mutex<()>,
}
fn poisoned<T>(_: T) -> AppError {
    AppError::new("E_UPDATE_FEED", "updater_lock")
}
impl Service {
    pub fn new(
        database: Database,
        apps: Arc<crate::uninstall::Engine>,
        settings: Arc<RwLock<Settings>>,
        sink: EventSink,
    ) -> Result<Self, AppError> {
        let client = reqwest::Client::builder()
            .timeout(Duration::from_secs(30))
            .redirect(reqwest::redirect::Policy::custom(|attempt| {
                if attempt.previous().len() >= 5
                    || attempt.url().scheme() != "https"
                    || !attempt.url().username().is_empty()
                    || attempt.url().password().is_some()
                {
                    attempt.error("unsafe update redirect")
                } else {
                    attempt.follow()
                }
            }))
            .build()?;
        Ok(Self {
            database,
            apps,
            settings,
            sink,
            client,
            check_lock: Mutex::new(()),
        })
    }
    pub fn settings(&self) -> Result<Settings, AppError> {
        Ok(self.settings.read().map_err(poisoned)?.clone())
    }
    pub fn candidates(&self) -> Result<Vec<Candidate>, AppError> {
        Ok(self
            .database
            .meta("native_update_candidates")?
            .and_then(|s| serde_json::from_str(&s).ok())
            .unwrap_or_default())
    }
    // All lifecycle read/modify/write operations share the database lock with
    // check commits, so neither writer can restore a stale whole-cache snapshot.
    fn update_candidates<T>(
        &self,
        update: impl FnOnce(&mut Vec<Candidate>) -> Result<T, AppError>,
    ) -> Result<T, AppError> {
        self.database.with(|db| {
            let value: Option<String> = db.query_row(
                "SELECT value FROM meta WHERE key='native_update_candidates'", [], |row| row.get(0),
            ).optional()?;
            let mut candidates = value.and_then(|s| serde_json::from_str(&s).ok()).unwrap_or_default();
            let result = update(&mut candidates)?;
            db.execute(
                "INSERT INTO meta(key,value) VALUES('native_update_candidates',?1) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
                [serde_json::to_string(&candidates)?],
            )?;
            Ok(result)
        })
    }
    // Resolve ownership once for both sides of merge. Keep invalid cached app records
    // without a route: an installed update may still need the old process restarted.
    fn routed_apps(&self, settings: &Settings) -> Result<Vec<LocalApp>, AppError> {
        let apps = self.apps.list()?;
        let keys = self.apps.keys.lock().map_err(poisoned)?;
        let valid: Vec<_> = apps
            .into_iter()
            .map(|app| {
                let valid = crate::uninstall::apps::BundleKey::read(Path::new(&app.path))
                    .is_some_and(|key| keys.get(&app.id) == Some(&key));
                (app, valid)
            })
            .collect();
        drop(keys);
        let rules = cached_rules(&self.database);
        valid
            .into_iter()
            .map(|(mut app, valid)| {
                app.updater = if valid {
                    super::routing::select(&self.database, settings, &app, &rules)?
                } else {
                    None
                };
                Ok(app)
            })
            .collect()
    }
    fn current_candidate(
        &self,
        mut c: Candidate,
        settings: &Settings,
        apps: &[LocalApp],
        running: &[crate::running_apps::Process],
    ) -> Result<Option<Candidate>, AppError> {
        let Some(app) = apps.iter().find(|app| {
            c.item.app_id.as_deref() == Some(&app.id) && Path::new(&app.path) == c.app_path
        }) else {
            return Ok(None);
        };
        if !c.app_path.is_dir() {
            return Ok(None);
        }
        let is_running = running.iter().any(|p| p.path.starts_with(&c.app_path));
        if c.item.relaunch_pending && !is_running {
            let applied = c.artifact.as_ref().is_none_or(|a| {
                super::check::disk_version(&c.app_path, &a.version_key).is_some_and(|v| {
                    version::compare_source(c.item.source, &v, &a.version).is_ok_and(|o| !o.is_lt())
                })
            });
            if !self.finish_pending(&c, applied)? || applied {
                return Ok(None);
            }
            c.item.relaunch_pending = false;
        }
        let pref = prefs::load(&self.database, &c.item.app_key()?)?;
        c.item.skipped_version = pref.skipped_version;
        c.item.ignored = pref.ignored;
        c.item.running = is_running;
        // A running pending update is only a restart action, even after source changes
        // or a committed swap invalidated the pre-install snapshot. Any retry is a
        // normal candidate again and must pass the current route and snapshot gates.
        if !c.item.relaunch_pending
            && (!settings.update_sources.enabled(c.item.source)
                || app.updater != Some(c.item.source)
                || c.item.channel.unwrap_or_default() != pref.channel
                || !c.snapshot.still(&c.app_path)
                || c.item.skipped_version.as_deref() == Some(c.item.current_version.as_str()))
        {
            return Ok(None);
        }
        Ok(Some(c))
    }
    // Observing the old process stopped completes its pending lifecycle. Persist
    // that transition so a later launch cannot resurrect it. Match the raw record
    // under the DB lock to leave concurrently refreshed candidates untouched.
    fn finish_pending(&self, original: &Candidate, applied: bool) -> Result<bool, AppError> {
        let original = serde_json::to_value(original)?;
        self.database.with(|db| {
            let value: Option<String> = db
                .query_row(
                    "SELECT value FROM meta WHERE key='native_update_candidates'",
                    [],
                    |row| row.get(0),
                )
                .optional()?;
            let Some(value) = value else { return Ok(false) };
            let mut candidates: Vec<Candidate> = serde_json::from_str(&value)?;
            let mut found = None;
            for (index, candidate) in candidates.iter().enumerate() {
                if serde_json::to_value(candidate)? == original {
                    found = Some(index);
                    break;
                }
            }
            let Some(index) = found else { return Ok(false) };
            if applied {
                candidates.remove(index);
            } else {
                candidates[index].item.relaunch_pending = false;
            }
            db.execute(
                "UPDATE meta SET value=?1 WHERE key='native_update_candidates'",
                [serde_json::to_string(&candidates)?],
            )?;
            Ok(true)
        })
    }
    fn list_with_context(
        &self,
        settings: &Settings,
        apps: &[LocalApp],
        running: &[crate::running_apps::Process],
    ) -> Result<Vec<OutdatedItem>, AppError> {
        self.candidates()?
            .into_iter()
            .map(|c| self.current_candidate(c, settings, apps, running))
            .filter_map(|r| r.transpose())
            .map(|r| r.map(|c| c.item))
            .collect()
    }
    pub fn list(&self) -> Result<Vec<OutdatedItem>, AppError> {
        let settings = self.settings()?;
        let apps = self.routed_apps(&settings)?;
        let running = self.apps.control.list()?;
        self.list_with_context(&settings, &apps, &running)
    }
    pub fn merge(&self, mut brew: Vec<OutdatedItem>) -> Result<Vec<OutdatedItem>, AppError> {
        // An empty update notification needs no app ownership or process lookup.
        // Enumerating every installed app here stalls queue completion on first use.
        if brew.is_empty() && self.candidates()?.is_empty() {
            return Ok(brew);
        }
        let settings = self.settings()?;
        let apps = self.routed_apps(&settings)?;
        let running = self.apps.control.list()?;
        let native = self.list_with_context(&settings, &apps, &running)?;
        // Ownership survives an empty check (including skipped releases) and a restart.
        // Pending restarts also prevent a second installation through Homebrew.
        let native_casks: std::collections::HashSet<_> = apps
            .iter()
            .filter(|app| {
                app.updater
                    .is_some_and(|source| source != UpdateSource::Homebrew)
                    || native.iter().any(|item| {
                        item.relaunch_pending && item.app_id.as_deref() == Some(&app.id)
                    })
            })
            .filter_map(|app| app.cask.as_deref())
            .collect();
        brew.retain(|item| {
            settings.update_sources.enabled(UpdateSource::Homebrew)
                && !(item.kind == Kind::Cask && native_casks.contains(item.token.as_str()))
        });
        let installed: Vec<InstalledItem> = self
            .database
            .meta("installed_cache")?
            .and_then(|s| serde_json::from_str(&s).ok())
            .unwrap_or_default();
        for item in &mut brew {
            if let Some(local) = installed
                .iter()
                .find(|i| i.kind == item.kind && i.token == item.token)
            {
                item.running = running.iter().any(|p| {
                    local
                        .app_paths
                        .iter()
                        .any(|path| p.path.starts_with(Path::new(path)))
                });
            }
            item.major = version::major(&item.installed_version, &item.current_version);
            let pref = prefs::load(&self.database, &item.app_key()?)?;
            item.skipped_version = pref.skipped_version;
            item.ignored |= pref.ignored;
        }
        brew.retain(|i| i.skipped_version.as_deref() != Some(i.current_version.as_str()));
        brew.extend(native);
        brew.sort_by(|a, b| {
            a.name
                .to_lowercase()
                .cmp(&b.name.to_lowercase())
                .then_with(|| a.source.cmp(&b.source))
                .then_with(|| a.app_id.cmp(&b.app_id))
                .then_with(|| a.token.cmp(&b.token))
        });
        Ok(brew)
    }
    pub fn installed(&self, app_id: &str, relaunch_pending: bool) -> Result<(), AppError> {
        self.update_candidates(|candidates| {
            candidates.retain_mut(|c| {
                if c.item.app_id.as_deref() == Some(app_id) {
                    c.item.relaunch_pending = relaunch_pending;
                    c.item.running = relaunch_pending;
                    relaunch_pending
                } else {
                    true
                }
            });
            Ok(())
        })?;
        self.changed()
    }
    pub fn relaunch(&self, app_id: &str) -> Result<(), AppError> {
        let record = self.apps.app(app_id)?;
        if record.app.protected_reason.is_some() {
            return Err(AppError::new("E_PROTECTED", "protected_update_target"));
        }
        let path = Path::new(&record.app.path);
        let snapshot = Snapshot::capture(path)?;
        let pending = self
            .candidates()?
            .into_iter()
            .find(|c| c.item.app_id.as_deref() == Some(app_id) && c.item.relaunch_pending);
        let before = if pending
            .as_ref()
            .is_some_and(|c| c.item.source == UpdateSource::Electron)
        {
            Some(super::gate::Inspector::inspect(
                &super::gate::NativeInspector,
                path,
            )?)
        } else {
            None
        };
        let control = &self.apps.control;
        let matches = |process: &crate::running_apps::Process| {
            process.path == path || process.path.starts_with(path)
        };
        for process in control.list()?.iter().filter(|p| matches(p)) {
            if !control.quit(process)? {
                return Err(AppError::new("E_APP_RUNNING", "normal_quit_refused"));
            }
        }
        let started = std::time::Instant::now();
        while control.list()?.iter().any(&matches) {
            if started.elapsed() > Duration::from_secs(15) {
                return Err(AppError::new("E_APP_RUNNING", "normal_quit_timeout"));
            }
            std::thread::sleep(Duration::from_millis(100));
        }
        if let Some(before) = before.as_ref() {
            let after = super::gate::Inspector::inspect(&super::gate::NativeInspector, path)?;
            let (os, arch) = system()?;
            super::gate::compatible_identities(
                before,
                &after,
                super::gate::Inspector::satisfies(
                    &super::gate::NativeInspector,
                    path,
                    &before.requirement,
                )?,
                &os,
                &arch,
                pending.as_ref().and_then(|c| c.expected_team.as_deref()),
            )?;
            if after.version != before.version
                || after.marketing_version != before.marketing_version
            {
                super::gate::electron_versions(before, &after)?;
            }
        } else if !snapshot.still(path) {
            return Err(AppError::new(
                "E_UPDATE_INTEGRITY",
                "relaunch_target_changed",
            ));
        }
        control.reopen(path)?;
        self.update_candidates(|candidates| {
            candidates.retain_mut(|c| {
                if c.item.app_id.as_deref() != Some(app_id) || !c.item.relaunch_pending {
                    return true;
                }
                let applied = c.artifact.as_ref().is_none_or(|a| {
                    super::check::disk_version(path, &a.version_key).is_some_and(|v| {
                        version::compare_source(c.item.source, &v, &a.version)
                            .is_ok_and(|o| !o.is_lt())
                    })
                });
                if !applied {
                    c.item.relaunch_pending = false
                }
                !applied
            });
            Ok(())
        })?;
        self.changed()
    }
    pub fn candidate(&self, app_id: &str, source: UpdateSource) -> Result<Candidate, AppError> {
        let candidate = self
            .candidates()?
            .into_iter()
            .find(|c| c.item.app_id.as_deref() == Some(app_id) && c.item.source == source)
            .ok_or_else(|| AppError::new("E_UPDATE_FEED", "update_candidate_missing"))?;
        candidate.validate_identity()?;
        let settings = self.settings()?;
        let apps = self.routed_apps(&settings)?;
        let running = self.apps.control.list()?;
        let candidate = self
            .current_candidate(candidate, &settings, &apps, &running)?
            .ok_or_else(|| AppError::new("E_UPDATE_FEED", "update_candidate_no_longer_current"))?;
        if candidate.item.relaunch_pending {
            return Err(AppError::new(
                "E_NOT_INSTALLABLE",
                "informational_or_pending_update",
            ));
        }
        Ok(candidate)
    }
    pub fn changed(&self) -> Result<(), AppError> {
        (self.sink)(CoreEvent::Updates(UpdatesChanged {
            count: self
                .merge(
                    self.database
                        .meta("updates_cache")?
                        .and_then(|s| serde_json::from_str(&s).ok())
                        .unwrap_or_default(),
                )?
                .len() as u32,
        }));
        Ok(())
    }
    pub fn ignore(&self, key: &str, ignored: bool) -> Result<(), AppError> {
        prefs::ignore(&self.database, key, ignored)?;
        self.changed()
    }
    pub fn skip(&self, key: &str, version: Option<&str>) -> Result<(), AppError> {
        prefs::skip(&self.database, key, version)?;
        self.changed()
    }
    pub fn channel(&self, key: &str, channel: UpdateChannel) -> Result<(), AppError> {
        prefs::channel(&self.database, key, channel)?;
        self.changed()
    }
    pub(super) async fn read_url(
        &self,
        url: &str,
        cache_key: &str,
        max: usize,
    ) -> Result<Vec<u8>, AppError> {
        let parsed = reqwest::Url::parse(url)
            .map_err(|_| AppError::new("E_UPDATE_FEED", "invalid_update_url"))?;
        if parsed.host_str().is_some_and(|host| {
            let host = host.trim_end_matches('.');
            host == "invalid" || host.ends_with(".invalid")
        }) {
            return Err(AppError::new("E_UPDATE_FEED", "invalid_update_url"));
        }
        let retry_key = format!("{cache_key}:retry_after");
        let now = chrono::Utc::now().timestamp();
        if self
            .database
            .meta(&retry_key)?
            .and_then(|value| serde_json::from_str::<FeedRetry>(&value).ok())
            .is_some_and(|retry| retry.url == url && retry.until > now)
        {
            return Err(AppError::new("E_UPDATE_FEED", "feed_rate_limited"));
        }
        let cached = self
            .database
            .meta(cache_key)?
            .and_then(|s| serde_json::from_str::<CachedFeed>(&s).ok())
            .filter(|c| c.url == url);
        let mut request = self.client.get(url);
        if let Some(tag) = cached.as_ref().and_then(|c| c.etag.as_ref()) {
            request = request.header(reqwest::header::IF_NONE_MATCH, tag);
        }
        let mut response = request.send().await.map_err(|e| {
            AppError::new("E_UPDATE_FEED", "feed_request").with_detail(e.without_url().to_string())
        })?;
        if response.status() == reqwest::StatusCode::NOT_MODIFIED {
            return cached
                .map(|c| c.body.into_bytes())
                .ok_or_else(|| AppError::new("E_UPDATE_FEED", "feed_304_without_cache"));
        }
        if response.status() == reqwest::StatusCode::TOO_MANY_REQUESTS {
            let until = retry_after(
                response
                    .headers()
                    .get(reqwest::header::RETRY_AFTER)
                    .and_then(|value| value.to_str().ok()),
                chrono::Utc::now().timestamp(),
            );
            self.database.set_meta(
                &retry_key,
                &serde_json::to_string(&FeedRetry {
                    url: url.into(),
                    until,
                })?,
            )?;
            return Err(AppError::new("E_UPDATE_FEED", "feed_rate_limited"));
        }
        if !response.status().is_success()
            || response.content_length().is_some_and(|n| n > max as u64)
        {
            // Keep useful diagnostics in backend logs without exposing query credentials.
            if let Ok(mut endpoint) = reqwest::Url::parse(url) {
                endpoint.set_query(None);
                endpoint.set_fragment(None);
                log::warn!(
                    "Update feed rejected: endpoint={endpoint} status={} contentLength={:?} limit={max}",
                    response.status(),
                    response.content_length()
                );
            }
            return Err(AppError::new("E_UPDATE_FEED", "feed_response"));
        }
        let etag = response
            .headers()
            .get(reqwest::header::ETAG)
            .and_then(|h| h.to_str().ok())
            .map(str::to_owned);
        let mut data = vec![];
        while let Some(chunk) = response.chunk().await? {
            if data.len() + chunk.len() > max {
                return Err(AppError::new("E_UPDATE_FEED", "feed_size"));
            }
            data.extend_from_slice(&chunk);
        }
        let body = String::from_utf8(data.clone())
            .map_err(|_| AppError::new("E_UPDATE_FEED", "feed_encoding"))?;
        self.database.set_meta(
            cache_key,
            &serde_json::to_string(&CachedFeed {
                url: url.into(),
                etag,
                body,
            })?,
        )?;
        Ok(data)
    }
    pub async fn rules(&self) -> Result<Vec<FeedRule>, AppError> {
        let base = crate::catalog::sync::configured_base();
        let data = self
            .read_url(
                &format!("{}/catalog/update-feeds", base.trim_end_matches('/')),
                "update_feed_rules",
                4 * 1024 * 1024,
            )
            .await?;
        parse_rules(&data)
    }
    pub async fn check(
        &self,
        sources: &[UpdateSource],
        store: &crate::appstore::Service,
        manual: bool,
    ) -> Result<super::outcome::CheckOutcome, AppError> {
        let _lock = self.check_lock.lock().await;
        let app_engine = self.apps.clone();
        let apps = crate::blocking(move || app_engine.list()).await?;
        let mut candidates = self.candidates()?;
        let initial_candidates = candidates.clone();
        let settings = self.settings()?;
        let mut outcome = super::outcome::CheckOutcome::default();
        if sources.contains(&UpdateSource::Mas)
            && settings.update_sources.enabled(UpdateSource::Mas)
        {
            match self.mas_candidates(&apps, store).await {
                Ok(new) => {
                    outcome.succeeded();
                    candidates.retain(|c| c.item.source != UpdateSource::Mas);
                    candidates.extend(new);
                }
                Err(e) => outcome.failed(UpdateSource::Mas, "installed App Store applications", e),
            }
        }
        match self
            .check_native(sources, &apps, &mut candidates, manual)
            .await
        {
            Ok(native) => outcome.extend(native),
            Err(error) => {
                for source in sources.iter().copied().filter(|s| {
                    matches!(
                        s,
                        UpdateSource::Sparkle
                            | UpdateSource::Electron
                            | UpdateSource::Github
                            | UpdateSource::Vendor
                    )
                }) {
                    outcome.failed(source, "native source inspection", error.clone());
                }
            }
        }
        candidates.retain(|c| apps.iter().any(|a| Some(&a.id) == c.item.app_id.as_ref()));
        self.persist_checked_candidates(candidates, &initial_candidates)?;
        self.changed()?;
        Ok(outcome)
    }
    fn persist_checked_candidates(
        &self,
        mut candidates: Vec<Candidate>,
        initial: &[Candidate],
    ) -> Result<(), AppError> {
        let states = |items: &[Candidate]| -> Result<_, AppError> {
            let mut apps = std::collections::BTreeMap::<_, Vec<_>>::new();
            for candidate in items {
                apps.entry(candidate.item.app_id.clone())
                    .or_default()
                    .push(serde_json::to_value(candidate)?);
            }
            Ok(apps)
        };
        let initial = states(initial)?;
        self.update_candidates(|current| {
            let latest = states(current)?;
            // Preserve complete app state committed while this check awaited I/O,
            // including newly pending installs, removals and source transitions.
            // Unchanged apps still receive their freshly checked results.
            let changed: std::collections::BTreeSet<_> = initial
                .keys()
                .chain(latest.keys())
                .filter(|id| initial.get(*id) != latest.get(*id))
                .cloned()
                .collect();
            candidates.retain(|c| !changed.contains(&c.item.app_id));
            candidates.extend(
                current
                    .iter()
                    .filter(|c| changed.contains(&c.item.app_id))
                    .cloned(),
            );
            *current = candidates;
            Ok(())
        })
    }
    pub(super) fn catalog_identity(
        &self,
        app: &LocalApp,
        rule: Option<&FeedRule>,
    ) -> Result<(Kind, String), AppError> {
        // Public rules use token:"" when no catalog package is associated.
        // This is an absent navigation hint, not a token for catalog::get.
        let token = [
            app.cask.as_ref(),
            app.catalog_token.as_ref(),
            rule.and_then(|r| r.token.as_ref()),
        ]
        .into_iter()
        .flatten()
        .find(|token| !token.is_empty());
        if let Some(token) = token {
            for kind in [Kind::Cask, Kind::Link] {
                if crate::catalog::get(&self.database, kind, token)?.is_some() {
                    return Ok((kind, token.clone()));
                }
            }
        }
        if let Some(bundle) = &app.bundle_id {
            let matched = self.database.with(|c| {
                let mut stmt =
                    c.prepare("SELECT data FROM catalog_items WHERE kind IN ('cask','link')")?;
                let rows = stmt
                    .query_map([], |r| r.get::<_, String>(0))?
                    .collect::<rusqlite::Result<Vec<_>>>()?;
                Ok(rows
                    .into_iter()
                    .filter_map(|s| serde_json::from_str::<CatalogItem>(&s).ok())
                    .find(|i| {
                        i.bundle_ids
                            .as_ref()
                            .is_some_and(|ids| ids.contains(bundle))
                    }))
            })?;
            if let Some(item) = matched {
                return Ok((item.kind, item.token));
            }
        }
        Ok((Kind::Cask, String::new()))
    }
    async fn mas_candidates(
        &self,
        apps: &[LocalApp],
        store: &crate::appstore::Service,
    ) -> Result<Vec<Candidate>, AppError> {
        let status = store.status().await?;
        let installed = store.installed().await?;
        let updates = store.updates().await?;
        let mut out = vec![];
        for update in updates {
            let Some(local) = installed.iter().find(|a| a.adam_id == update.adam_id) else {
                continue;
            };
            let Some(app) = apps.iter().find(|a| {
                a.path == local.path && a.bundle_id.as_deref() == Some(update.bundle_id.as_str())
            }) else {
                continue;
            };
            let pref = prefs::load(&self.database, &format!("app:{}", app.id))?;
            let item = OutdatedItem {
                kind: Kind::Cask,
                token: update.adam_id.to_string(),
                name: update.name,
                installed_version: update.installed_version.clone(),
                current_version: update.store_version.clone(),
                auto_updates: false,
                pinned: false,
                ignored: pref.ignored,
                dependents: 0,
                download_size: update.size_bytes,
                source: UpdateSource::Mas,
                app_id: Some(app.id.clone()),
                bundle_id: Some(update.bundle_id),
                channel: None,
                major: version::major(&update.installed_version, &update.store_version),
                notes: update.release_notes.map(|body| UpdateNotes::Inline {
                    format: UpdateNotesFormat::Text,
                    body,
                }),
                integrity: None,
                requires_admin: status.admin_install,
                running: app.running,
                relaunch_pending: false,
                skipped_version: pref.skipped_version,
                critical_update: false,
                informational_url: None,
                download_host: None,
                expected_team_match: None,
            };
            out.push(Candidate {
                item,
                app_path: app.path.clone().into(),
                snapshot: Snapshot::capture(Path::new(&app.path))?,
                feed_item: None,
                public_key: None,
                artifact: None,
                expected_team: None,
                checked_at: chrono::Utc::now().timestamp_millis(),
            });
        }
        Ok(out)
    }
}
pub(super) fn rollout_group(id: &str) -> u8 {
    use sha2::{Digest, Sha256};
    Sha256::digest(id.as_bytes())[0] % 7
}
pub fn system() -> Result<(String, String), AppError> {
    let info = plist::Value::from_file("/System/Library/CoreServices/SystemVersion.plist")
        .map_err(|_| AppError::new("E_UPDATE_FEED", "system_version"))?;
    let os = info
        .as_dictionary()
        .and_then(|d| d.get("ProductVersion"))
        .and_then(|v| v.as_string())
        .ok_or_else(|| AppError::new("E_UPDATE_FEED", "system_version"))?
        .to_owned();
    // sysctl reports the hardware even when this process is running under Rosetta.
    let arm = crate::uninstall::identity::command(
        "/usr/sbin/sysctl",
        &["-n", "hw.optional.arm64"],
        std::time::Duration::from_secs(5),
    )
    .is_ok_and(|v| v.trim() == "1");
    let arch = if arm { "arm64" } else { "x86_64" };
    Ok((os, arch.into()))
}
pub fn requires_admin(path: &Path) -> bool {
    #[cfg(unix)]
    {
        use std::os::unix::ffi::OsStrExt;
        let Some(parent) = path.parent() else {
            return true;
        };
        let Ok(parent) = std::ffi::CString::new(parent.as_os_str().as_bytes()) else {
            return true;
        };
        unsafe { nix::libc::access(parent.as_ptr(), nix::libc::W_OK) != 0 }
    }
    #[cfg(not(unix))]
    {
        let _ = path;
        true
    }
}

fn parse_rules(data: &[u8]) -> Result<Vec<FeedRule>, AppError> {
    let value: serde_json::Value =
        serde_json::from_slice(data).map_err(|_| AppError::new("E_UPDATE_FEED", "update_rules"))?;
    if value.get("code").is_some_and(|v| v != "0000") {
        return Err(AppError::new("E_UPDATE_FEED", "update_rules_response"));
    }
    let rules: RulesResponse = serde_json::from_value(value.get("data").cloned().unwrap_or(value))
        .map_err(|_| AppError::new("E_UPDATE_FEED", "update_rules"))?;
    if rules.items.len() > 20000 {
        return Err(AppError::new("E_UPDATE_FEED", "update_rules_size"));
    }
    for rule in &rules.items {
        if let Some(url) = &rule.feed_url {
            feed::https(url)?;
        }
        if rule.team_id.as_ref().is_some_and(|t| {
            t.len() != 10
                || !t
                    .bytes()
                    .all(|b| b.is_ascii_uppercase() || b.is_ascii_digit())
        }) || matches!(rule.source, UpdateSource::Github | UpdateSource::Vendor)
            && rule.team_id.is_none()
        {
            return Err(AppError::new(
                "E_UPDATE_TEAM_MISMATCH",
                "rule_team_required",
            ));
        }
        if let Some(release) = &rule.release {
            release.validate(rule.source)?;
        }
    }
    Ok(rules.items)
}
pub fn cached_rules(db: &Database) -> Vec<FeedRule> {
    db.meta("update_feed_rules")
        .ok()
        .flatten()
        .and_then(|s| serde_json::from_str::<CachedFeed>(&s).ok())
        .and_then(|s| parse_rules(s.body.as_bytes()).ok())
        .unwrap_or_default()
}
