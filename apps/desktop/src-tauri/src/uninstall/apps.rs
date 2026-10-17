use super::{
    CachedApp, Engine,
    identity::{Identity, Receipt, generic_bundle_id},
    rules,
    scan::{declarations, strings},
};
use crate::{AppError, model::*, running_apps::Process};
use serde_json::Value;
use sha1::{Digest, Sha1};
use std::{
    collections::{BTreeMap, BTreeSet, HashMap, HashSet},
    ffi::OsString,
    fs,
    path::{Path, PathBuf},
    sync::{Arc, MutexGuard, atomic::Ordering},
    time::{Duration, Instant},
};

pub fn stable_id(path: &Path) -> String {
    format!("{:x}", Sha1::digest(path.to_string_lossy().as_bytes()))
}

/// The bundle directory and its Info.plist. Updates replace one or both, so an unchanged key means cached probe
/// results and the measured size still describe the app.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct BundleKey {
    device: u64,
    inode: u64,
    info: Option<(u64, u64, i128)>,
}
impl BundleKey {
    pub(crate) fn read(path: &Path) -> Option<Self> {
        #[cfg(unix)]
        {
            use std::os::unix::fs::MetadataExt;
            let bundle = fs::symlink_metadata(path).ok()?;
            let info = fs::symlink_metadata(path.join("Contents/Info.plist"))
                .ok()
                .map(|m| {
                    (
                        m.ino(),
                        m.len(),
                        i128::from(m.mtime()) * 1_000_000_000 + i128::from(m.mtime_nsec()),
                    )
                });
            Some(Self {
                device: bundle.dev(),
                inode: bundle.ino(),
                info,
            })
        }
        #[cfg(not(unix))]
        {
            let _ = path;
            None
        }
    }
}

/// Probe results of one bundle, reused while its key stays the same.
#[derive(Clone)]
pub(crate) struct Probed {
    key: BundleKey,
    identity: Identity,
    receipts: Vec<Receipt>,
}

/// Catalog casks by the file names of their apps; rebuilt only when the catalog changes.
pub(crate) struct CatalogIndex {
    revision: String,
    by_file: HashMap<OsString, Vec<String>>,
    files: HashMap<String, Vec<OsString>>,
}

/// When each app was last seen running, kept across launches: login items and menu bar apps often never update
/// Spotlight's last-used date, yet they are in use every day.
#[derive(Default)]
pub(crate) struct Seen {
    values: HashMap<String, i64>,
    saved_at: i64,
}
impl Seen {
    pub(crate) fn load(database: &crate::db::Database) -> Self {
        Self {
            values: database
                .meta("local_apps_seen_v1")
                .ok()
                .flatten()
                .and_then(|raw| serde_json::from_str(&raw).ok())
                .unwrap_or_default(),
            saved_at: 0,
        }
    }
}

/// What ties an installed cask to an app found on disk.
struct CaskEvidence {
    token: String,
    directory: PathBuf,
    apps: Vec<PathBuf>,
    ids: BTreeSet<String>,
    files: BTreeSet<OsString>,
}

/// A catalog cask that may match local apps, with the file names of its apps.
type Candidate = (String, Vec<OsString>);

/// One enumerated bundle before it becomes a `LocalApp`.
struct Found {
    path: PathBuf,
    key: Option<BundleKey>,
    identity: Identity,
    receipts: Vec<Receipt>,
}

/// A process belongs to an app when it runs from the bundle. A matching bundle ID counts only when no other
/// installed app has the same one (Xcode and Xcode-beta do) and it is not a framework default; a process inside
/// another installed app never counts. Quitting by bundle ID alone used to close the wrong copy.
pub(crate) fn running_at(
    path: &Path,
    bundle_id: Option<&str>,
    processes: &[Process],
    unique: &HashSet<String>,
    installed: &[PathBuf],
) -> bool {
    processes.iter().any(|p| {
        p.path == path
            || bundle_id.is_some_and(|id| {
                p.bundle_id == id
                    && unique.contains(id)
                    && !generic_bundle_id(id)
                    && !installed
                        .iter()
                        .any(|other| other != path && p.path.starts_with(other))
            })
    })
}
/// Bundle IDs that name exactly one installed app.
pub(crate) fn unique_bundle_ids<'a>(ids: impl Iterator<Item = &'a str>) -> HashSet<String> {
    let mut counts: HashMap<&str, usize> = HashMap::new();
    for id in ids {
        *counts.entry(id).or_default() += 1;
    }
    counts
        .into_iter()
        .filter(|(_, count)| *count == 1)
        .map(|(id, _)| id.to_owned())
        .collect()
}

impl Engine {
    pub fn list(self: &Arc<Self>) -> Result<Vec<LocalApp>, AppError> {
        if !self.enumerated.load(Ordering::Acquire) {
            // Wait for an enumeration that is already running instead of starting another one after it.
            let guard = self.refresh_lock.lock().map_err(super::poison)?;
            if !self.enumerated.load(Ordering::Acquire) {
                return self.scan_apps(guard, LocalAppsChangedReason::Startup);
            }
        }
        let settings = self.settings.read().map_err(super::poison)?.clone();
        self.apps
            .lock()
            .map_err(super::poison)?
            .values()
            .map(|a| crate::updater::routing::decorate(&self.database, &settings, a.app.clone()))
            .collect()
    }
    pub fn app(&self, id: &str) -> Result<CachedApp, AppError> {
        self.apps
            .lock()
            .map_err(super::poison)?
            .get(id)
            .cloned()
            .ok_or_else(|| AppError::new("E_NOT_FOUND", "local_app"))
    }
    pub fn refresh(
        self: &Arc<Self>,
        reason: LocalAppsChangedReason,
    ) -> Result<Vec<LocalApp>, AppError> {
        let guard = self.refresh_lock.lock().map_err(super::poison)?;
        self.scan_apps(guard, reason)
    }
    fn scan_apps(
        self: &Arc<Self>,
        _guard: MutexGuard<'_, ()>,
        reason: LocalAppsChangedReason,
    ) -> Result<Vec<LocalApp>, AppError> {
        let installed = self
            .database
            .meta("installed_cache")?
            .and_then(|s| serde_json::from_str::<Vec<InstalledItem>>(&s).ok())
            .unwrap_or_default();
        let mut casks: BTreeMap<String, (PathBuf, Option<InstalledItem>)> = BTreeMap::new();
        for root in &self.roots.caskrooms {
            if let Ok(entries) = fs::read_dir(root) {
                for e in entries.flatten() {
                    let token = e.file_name().to_string_lossy().into_owned();
                    if e.file_type().is_ok_and(|t| t.is_dir() && !t.is_symlink())
                        && crate::brew::args::validate_token(&token).is_ok()
                    {
                        let has_install = fs::read_dir(e.path()).is_ok_and(|mut d| {
                            d.any(|v| v.is_ok_and(|v| v.file_name() != ".metadata"))
                        });
                        if has_install {
                            casks.insert(token, (e.path(), None));
                        }
                    }
                }
            }
        }
        for item in installed.iter().filter(|i| i.kind == Kind::Cask) {
            // Cached brew records can outlive an uninstall. Only enrich packages
            // with physical installation evidence; never resurrect a cached row.
            if !casks.contains_key(&item.token)
                && !item.app_paths.iter().any(|p| Path::new(p).is_dir())
            {
                continue;
            }
            let path = self
                .roots
                .caskrooms
                .iter()
                .map(|r| r.join(&item.token))
                .find(|p| p.is_dir())
                .unwrap_or_else(|| {
                    self.roots
                        .caskrooms
                        .first()
                        .cloned()
                        .unwrap_or_else(|| self.roots.home.join("Caskroom"))
                        .join(&item.token)
                });
            casks.insert(item.token.clone(), (path, Some(item.clone())));
        }
        // Read each installed cask's rule once per enumeration instead of once per app and cask.
        let rules = self.installed_rules();
        let evidence: Vec<CaskEvidence> = casks
            .iter()
            .map(|(token, (directory, record))| {
                let mut ids = BTreeSet::new();
                let mut files = BTreeSet::new();
                if let Some(raw) = self.local_rule_with(token, &rules) {
                    for declaration in declarations(&raw, "uninstall")
                        .into_iter()
                        .chain(declarations(&raw, "zap"))
                    {
                        for key in ["quit", "launchctl"] {
                            ids.extend(strings(declaration.get(key)));
                        }
                    }
                    for artifact in raw
                        .get("artifacts")
                        .and_then(Value::as_array)
                        .into_iter()
                        .flatten()
                    {
                        for name in strings(artifact.get("app")) {
                            if let Some(file) = Path::new(&name).file_name() {
                                files.insert(file.to_owned());
                            }
                        }
                    }
                }
                CaskEvidence {
                    token: token.clone(),
                    directory: directory.clone(),
                    apps: record
                        .iter()
                        .flat_map(|r| &r.app_paths)
                        .filter_map(|p| Path::new(p).canonicalize().ok())
                        .collect(),
                    ids,
                    files,
                }
            })
            .collect();
        let catalog = self.catalog_index()?;
        let mut paths = BTreeMap::new();
        for root in &self.roots.applications {
            enumerate(root, 1, &mut paths);
        }
        // Cask app artifacts may be installed outside /Applications; only permitted local roots are listed.
        for item in &installed {
            for path in &item.app_paths {
                let path = PathBuf::from(path);
                if self.allowed_bundle(&path) && path.is_dir() {
                    paths.insert(path.clone(), path);
                }
            }
        }
        let path_values: Vec<_> = paths.into_values().collect();
        let cacheable = self.probe.cacheable();
        let cached: HashMap<PathBuf, Probed> = if cacheable {
            self.probe_cache.lock().map_err(super::poison)?.clone()
        } else {
            HashMap::new()
        };
        let mut found = Vec::new();
        std::thread::scope(|scope| {
            let cached = &cached;
            let jobs: Vec<_> = path_values
                .chunks(path_values.len().div_ceil(4).max(1))
                .map(|chunk| {
                    scope.spawn(move || {
                        chunk
                            .iter()
                            .filter_map(|path| {
                                let real = path.canonicalize().ok()?;
                                if !self.allowed_bundle(&real)
                                    || real.starts_with("/Volumes")
                                    || real.starts_with("/System")
                                {
                                    return None;
                                }
                                let key = BundleKey::read(&real);
                                let hit = key
                                    .as_ref()
                                    .and_then(|key| cached.get(&real).filter(|p| p.key == *key));
                                let (identity, receipts) = match hit {
                                    Some(hit) => {
                                        let mut identity = hit.identity.clone();
                                        (identity.last_used, identity.date_added) =
                                            self.probe.dates(&real);
                                        (identity, hit.receipts.clone())
                                    }
                                    None => {
                                        let identity = self.probe.identity(&real);
                                        if real.file_name().is_some_and(|n| n == "Safari.app")
                                            || identity.bundle_id.as_deref()
                                                == Some("com.apple.Safari")
                                        {
                                            return None;
                                        }
                                        let receipts = self.probe.receipts(&real);
                                        (identity, receipts)
                                    }
                                };
                                Some(Found {
                                    path: real,
                                    key,
                                    identity,
                                    receipts,
                                })
                            })
                            .collect::<Vec<_>>()
                    })
                })
                .collect();
            for job in jobs {
                if let Ok(result) = job.join() {
                    found.extend(result);
                }
            }
        });
        let processes = self.control.list().unwrap_or_default();
        let installed_paths: Vec<PathBuf> = found.iter().map(|f| f.path.clone()).collect();
        let unique =
            unique_bundle_ids(found.iter().filter_map(|f| f.identity.bundle_id.as_deref()));
        let previous_sizes = self.sizes.lock().map_err(super::poison)?.clone();
        let seen = self.seen.lock().map_err(super::poison)?.values.clone();
        let now = super::now();
        let mut running_ids = Vec::new();
        let mut probed = HashMap::new();
        let mut keys = HashMap::new();
        let mut records = BTreeMap::new();
        let mut seen_paths = HashSet::new();
        let mut seen_casks = HashSet::new();
        for Found {
            path,
            key,
            identity,
            receipts,
        } in found
        {
            if !seen_paths.insert(path.clone()) {
                continue;
            }
            if cacheable && let Some(key) = &key {
                probed.insert(
                    path.clone(),
                    Probed {
                        key: key.clone(),
                        identity: identity.clone(),
                        receipts: receipts.clone(),
                    },
                );
            }
            let token = evidence
                .iter()
                .find(|e| {
                    path.starts_with(&e.directory)
                        || e.apps.contains(&path)
                        || (identity
                            .bundle_id
                            .as_ref()
                            .is_some_and(|id| e.ids.contains(id))
                            && path.file_name().is_some_and(|f| e.files.contains(f)))
                })
                .map(|e| e.token.clone());
            let source = if token.is_some() {
                AppSource::Homebrew
            } else if path.join("Contents/_MASReceipt/receipt").is_file() {
                AppSource::AppStore
            } else if !receipts.is_empty() {
                AppSource::Pkg
            } else {
                AppSource::Direct
            };
            let protected_reason = if identity.bundle_id.as_deref() == Some("com.opennavo.desktop")
                || std::env::current_exe().is_ok_and(|p| p.starts_with(&path))
            {
                Some(ProtectedReason::SelfApp)
            } else if identity.managed {
                Some(ProtectedReason::Managed)
            } else {
                None
            };
            let record = token
                .as_ref()
                .and_then(|t| casks.get(t))
                .and_then(|(_, r)| r.as_ref());
            if let Some(t) = &token {
                seen_casks.insert(t.clone());
            }
            // A file-name match alone is insufficient: confirm bundle identity from locally cached cask metadata.
            let catalog_token = if token.is_none() {
                path.file_name()
                    .and_then(|file| catalog.by_file.get(file))
                    .into_iter()
                    .flatten()
                    .find_map(|candidate| {
                        self.local_rule_with(candidate, &rules)
                            .filter(|raw| {
                                super::scan::rule_bundle_matches(raw, identity.bundle_id.as_deref())
                            })
                            .map(|_| candidate.clone())
                    })
            } else {
                None
            };
            let name = identity.name.clone().unwrap_or_else(|| {
                path.file_stem()
                    .unwrap_or_default()
                    .to_string_lossy()
                    .into_owned()
            });
            let id = stable_id(&path);
            let icon_path =
                crate::library::files::icon(&self.database, &path, &self.icon_cache).ok();
            let running = running_at(
                &path,
                identity.bundle_id.as_deref(),
                &processes,
                &unique,
                &installed_paths,
            );
            if running {
                running_ids.push(id.clone());
            }
            let last_used_at = identity
                .last_used
                .max(seen.get(&id).copied())
                .max(running.then_some(now));
            let size_bytes = key.as_ref().and_then(|key| {
                previous_sizes
                    .get(&id)
                    .filter(|(previous, _)| previous == key)
                    .map(|(_, bytes)| *bytes)
            });
            if let Some(key) = key {
                keys.insert(id.clone(), key);
            }
            let app = LocalApp {
                id: id.clone(),
                path: path.to_string_lossy().into_owned(),
                name,
                bundle_id: identity.bundle_id.clone(),
                version: identity.version.clone(),
                source,
                updater: None,
                homebrew_cask: None,
                cask: token,
                catalog_token,
                team_id: identity.team_id.clone(),
                vendor: identity.vendor.clone(),
                arch: identity.arch,
                size_bytes,
                // Homebrew records the install; otherwise Spotlight's date added. A copied bundle keeps the
                // birth date it had in the disk image, often months before it was installed.
                installed_at: record
                    .and_then(|r| r.installed_at)
                    .or(identity.date_added)
                    .or_else(|| created_millis(&path)),
                last_used_at,
                icon_path,
                running,
                removable: protected_reason.is_none(),
                protected_reason,
                uninstaller: None,
            };
            records.insert(
                id,
                CachedApp {
                    app,
                    identity,
                    receipts,
                },
            );
        }
        for (token, (path, installed)) in casks {
            if !seen_casks.contains(&token) {
                let id = stable_id(&path);
                let key = BundleKey::read(&path);
                let size_bytes = key.as_ref().and_then(|key| {
                    previous_sizes
                        .get(&id)
                        .filter(|(previous, _)| previous == key)
                        .map(|(_, bytes)| *bytes)
                });
                if let Some(key) = key {
                    keys.insert(id.clone(), key);
                }
                let name = installed
                    .as_ref()
                    .map(|i| i.name.clone())
                    .unwrap_or_else(|| token.clone());
                let app = LocalApp {
                    id: id.clone(),
                    path: path.to_string_lossy().into_owned(),
                    name,
                    bundle_id: None,
                    version: installed.as_ref().map(|i| i.installed_version.clone()),
                    source: AppSource::Homebrew,
                    updater: None,
                    homebrew_cask: None,
                    cask: Some(token),
                    catalog_token: None,
                    team_id: None,
                    vendor: None,
                    arch: None,
                    size_bytes,
                    installed_at: installed
                        .as_ref()
                        .and_then(|i| i.installed_at)
                        .or_else(|| created_millis(&path)),
                    last_used_at: None,
                    icon_path: installed.as_ref().and_then(|i| i.icon_path.clone()),
                    running: false,
                    removable: true,
                    protected_reason: None,
                    uninstaller: None,
                };
                records.insert(
                    id,
                    CachedApp {
                        app,
                        identity: Identity::default(),
                        receipts: Vec::<Receipt>::new(),
                    },
                );
            }
        }
        let generation = self.generation.fetch_add(1, Ordering::AcqRel) + 1;
        let settings = self.settings.read().map_err(super::poison)?.clone();
        for record in records.values_mut() {
            record.app =
                crate::updater::routing::decorate(&self.database, &settings, record.app.clone())?;
        }
        let result = records.values().map(|c| c.app.clone()).collect();
        *self.keys.lock().map_err(super::poison)? = keys;
        *self.apps.lock().map_err(super::poison)? = records;
        self.enumerated.store(true, Ordering::Release);
        if cacheable {
            *self.probe_cache.lock().map_err(super::poison)? = probed;
        }
        {
            let mut cache = self.footprints.lock().map_err(super::poison)?;
            let apps = self.apps.lock().map_err(super::poison)?;
            cache.retain(|id, cached| {
                let Some(app) = apps.get(id) else {
                    return false;
                };
                if !cached.snapshot.matches(Path::new(&app.app.path)) {
                    cached.time = 0;
                }
                true
            });
            self.footprint_locks
                .lock()
                .map_err(super::poison)?
                .retain(|id, _| apps.contains_key(id));
            self.sizes
                .lock()
                .map_err(super::poison)?
                .retain(|id, _| apps.contains_key(id));
            self.seen
                .lock()
                .map_err(super::poison)?
                .values
                .retain(|id, _| apps.contains_key(id));
        }
        {
            let mut jobs = self.footprint_jobs.lock().map_err(super::poison)?;
            let apps = self.apps.lock().map_err(super::poison)?;
            jobs.revisions.retain(|id, _| apps.contains_key(id));
            jobs.errors.retain(|id, _| apps.contains_key(id));
            jobs.states.retain(|id, _| apps.contains_key(id));
        }
        self.note_running(&running_ids);
        (self.sink)(crate::events::CoreEvent::LocalApps(LocalAppsChanged {
            reason,
        }));
        let engine = self.clone();
        std::thread::spawn(move || {
            // Sizes fill in on their own; at utility priority this walk does not slow a cleanup scan running meanwhile.
            #[cfg(target_os = "macos")]
            unsafe {
                nix::libc::pthread_set_qos_class_self_np(
                    nix::libc::qos_class_t::QOS_CLASS_UTILITY,
                    0,
                );
            }
            engine.measure(generation)
        });
        Ok(result)
    }
    fn allowed_bundle(&self, path: &Path) -> bool {
        !crate::updater::backup::contains(path)
            && self
                .roots
                .applications
                .iter()
                .chain(self.roots.caskrooms.iter())
                .any(|r| path.starts_with(r))
            && path.extension().is_some_and(|e| e == "app")
    }
    /// Measure bundles whose size is not known yet; unchanged bundles keep the size measured before. Results are
    /// announced at most once a second so the totals fill in while large apps are still being walked.
    fn measure(&self, generation: u64) {
        let targets: Vec<_> = match self.apps.lock() {
            Ok(cache) => cache
                .values()
                .filter(|c| c.app.size_bytes.is_none())
                .map(|c| (c.app.id.clone(), c.app.path.clone()))
                .collect(),
            Err(_) => return,
        };
        let mut announced = Instant::now();
        let mut pending = false;
        for (id, path) in targets {
            if self.generation.load(Ordering::Acquire) != generation {
                return;
            }
            let bytes = rules::allocated_size(Path::new(&path), Duration::from_secs(30)).ok();
            if let Ok(mut apps) = self.apps.lock()
                && self.generation.load(Ordering::Acquire) == generation
                && let Some(record) = apps.get_mut(&id)
            {
                record.app.size_bytes = bytes;
                pending = true;
            }
            if let Some(bytes) = bytes
                && let Some(key) = self
                    .keys
                    .lock()
                    .ok()
                    .and_then(|keys| keys.get(&id).cloned())
                && let Ok(mut sizes) = self.sizes.lock()
            {
                sizes.insert(id, (key, bytes));
            }
            if pending && announced.elapsed() >= Duration::from_secs(1) {
                announced = Instant::now();
                pending = false;
                (self.sink)(crate::events::CoreEvent::LocalApps(LocalAppsChanged {
                    reason: LocalAppsChangedReason::Measured,
                }));
            }
        }
        if pending {
            (self.sink)(crate::events::CoreEvent::LocalApps(LocalAppsChanged {
                reason: LocalAppsChangedReason::Measured,
            }));
        }
    }
    pub fn update_running(&self) -> Result<(), AppError> {
        let processes = self.control.list()?;
        let now = super::now();
        let mut changed = false;
        let mut running_ids = Vec::new();
        {
            let mut apps = self.apps.lock().map_err(super::poison)?;
            let installed: Vec<PathBuf> =
                apps.values().map(|r| PathBuf::from(&r.app.path)).collect();
            let unique =
                unique_bundle_ids(apps.values().filter_map(|r| r.app.bundle_id.as_deref()));
            for record in apps.values_mut() {
                let running = running_at(
                    Path::new(&record.app.path),
                    record.app.bundle_id.as_deref(),
                    &processes,
                    &unique,
                    &installed,
                );
                if running {
                    running_ids.push(record.app.id.clone());
                    record.app.last_used_at = record.app.last_used_at.max(Some(now));
                }
                if running != record.app.running {
                    record.app.running = running;
                    changed = true;
                }
            }
        }
        self.note_running(&running_ids);
        if changed {
            (self.sink)(crate::events::CoreEvent::LocalApps(LocalAppsChanged {
                reason: LocalAppsChangedReason::Running,
            }));
        }
        Ok(())
    }
    /// Remember running apps; written at most every five minutes unless a new app shows up.
    fn note_running(&self, ids: &[String]) {
        if ids.is_empty() {
            return;
        }
        let now = super::now();
        let Ok(mut seen) = self.seen.lock() else {
            return;
        };
        let mut fresh = false;
        for id in ids {
            fresh |= seen.values.insert(id.clone(), now).is_none();
        }
        if fresh || now - seen.saved_at >= 300_000 {
            seen.saved_at = now;
            if let Ok(raw) = serde_json::to_string(&seen.values) {
                let _ = self.database.set_meta("local_apps_seen_v1", &raw);
            }
        }
    }
    /// Catalog casks by app file name. The catalog holds thousands of entries, so parse it again only when a sync
    /// changed it.
    pub(crate) fn catalog_index(&self) -> Result<Arc<CatalogIndex>, AppError> {
        // Cheap enough for every list request: indexed aggregates plus the sync cursor.
        let revision = self.database.with(|c| {
            let (count, changed): (i64, Option<i64>) = c.query_row(
                "SELECT count(*), max(version_changed_at) FROM catalog_items WHERE kind='cask'",
                [],
                |r| Ok((r.get(0)?, r.get(1)?)),
            )?;
            Ok(format!("{count}:{}", changed.unwrap_or(0)))
        })?;
        let revision = format!(
            "{}:{}:{revision}",
            self.database.meta("catalog_cursor")?.unwrap_or_default(),
            self.database.meta("snapshot_sha256")?.unwrap_or_default()
        );
        if let Some(index) = self
            .catalog
            .lock()
            .map_err(super::poison)?
            .as_ref()
            .filter(|index| index.revision == revision)
        {
            return Ok(index.clone());
        }
        #[derive(serde::Deserialize)]
        struct Apps {
            #[serde(default)]
            apps: Vec<String>,
        }
        let rows: Vec<(String, String)> = self.database.with(|c| {
            let mut stmt = c.prepare("SELECT token, data FROM catalog_items WHERE kind='cask'")?;
            let rows = stmt
                .query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))?
                .collect::<rusqlite::Result<Vec<_>>>()?;
            Ok(rows)
        })?;
        let mut by_file: HashMap<OsString, Vec<String>> = HashMap::new();
        let mut files: HashMap<String, Vec<OsString>> = HashMap::new();
        for (token, data) in rows {
            let Ok(Apps { apps }) = serde_json::from_str::<Apps>(&data) else {
                continue;
            };
            for app in apps {
                if let Some(file) = Path::new(&app).file_name() {
                    by_file
                        .entry(file.to_owned())
                        .or_default()
                        .push(token.clone());
                    files
                        .entry(token.clone())
                        .or_default()
                        .push(file.to_owned());
                }
            }
        }
        let index = Arc::new(CatalogIndex {
            revision,
            by_file,
            files,
        });
        *self.catalog.lock().map_err(super::poison)? = Some(index.clone());
        Ok(index)
    }
}
fn enumerate(root: &Path, depth: u8, paths: &mut BTreeMap<PathBuf, PathBuf>) {
    if crate::updater::backup::contains(root) || !rules::parents_safe(&root.join("entry")) {
        return;
    }
    if let Ok(entries) = fs::read_dir(root) {
        for e in entries.flatten() {
            let path = e.path();
            if path.extension().is_some_and(|e| e == "app") {
                paths.insert(path.clone(), path);
            } else if depth > 0 && e.file_type().is_ok_and(|t| t.is_dir() && !t.is_symlink()) {
                enumerate(&path, depth - 1, paths);
            }
        }
    }
}
pub fn created_millis(path: &Path) -> Option<i64> {
    fs::symlink_metadata(path)
        .ok()?
        .created()
        .ok()?
        .duration_since(std::time::UNIX_EPOCH)
        .ok()
        .map(|d| d.as_millis().min(i64::MAX as u128) as i64)
}

impl Engine {
    /// Fetch only catalog tokens identified by artifact filenames; never transmit native app records or paths.
    /// The app list calls this after every change, so it returns at once while neither the apps nor the catalog
    /// changed (re-checking unresolved candidates every ten minutes).
    pub async fn hydrate_catalog(self: &Arc<Self>) -> Result<(), AppError> {
        let _running = self.hydrate_lock.lock().await;
        let engine = self.clone();
        let Some(candidates) = crate::blocking(move || engine.hydrate_candidates()).await? else {
            return Ok(());
        };
        let mut jobs = tokio::task::JoinSet::new();
        for (token, files) in candidates {
            {
                let mut attempts = self.catalog_attempts.lock().map_err(super::poison)?;
                if attempts
                    .get(&token)
                    .is_some_and(|time| *time + 600_000 > super::now())
                {
                    continue;
                }
                attempts.insert(token.clone(), super::now());
            }
            let engine = self.clone();
            jobs.spawn(async move {
                let raw = engine.fetch_rule(&token).await.ok().flatten();
                (token, files, raw)
            });
            if jobs.len() >= 4
                && let Some(Ok((token, files, raw))) = jobs.join_next().await
            {
                self.apply_catalog_rule(&token, &files, raw.as_ref())?;
            }
        }
        while let Some(result) = jobs.join_next().await {
            if let Ok((token, files, raw)) = result {
                self.apply_catalog_rule(&token, &files, raw.as_ref())?;
            }
        }
        Ok(())
    }
    /// Catalog tokens to look up, each with its app file names; None while nothing changed.
    fn hydrate_candidates(&self) -> Result<Option<Vec<Candidate>>, AppError> {
        let index = self.catalog_index()?;
        let generation = self.generation.load(Ordering::Acquire);
        let now = super::now();
        {
            let mut hydrated = self.hydrated.lock().map_err(super::poison)?;
            if hydrated.as_ref().is_some_and(|(g, revision, at)| {
                *g == generation && *revision == index.revision && now - at < 600_000
            }) {
                return Ok(None);
            }
            *hydrated = Some((generation, index.revision.clone(), now));
        }
        let apps = self.apps.lock().map_err(super::poison)?;
        let mut candidates = BTreeMap::new();
        for app in apps
            .values()
            .filter(|a| a.app.source != AppSource::Homebrew && a.app.catalog_token.is_none())
        {
            let Some(file) = Path::new(&app.app.path).file_name() else {
                continue;
            };
            for token in index.by_file.get(file).into_iter().flatten() {
                candidates
                    .entry(token.clone())
                    .or_insert_with(|| index.files.get(token).cloned().unwrap_or_default());
            }
        }
        Ok(Some(candidates.into_iter().collect()))
    }
    fn apply_catalog_rule(
        &self,
        token: &str,
        files: &[OsString],
        raw: Option<&Value>,
    ) -> Result<(), AppError> {
        let Some(raw) = raw else {
            return Ok(());
        };
        let mut changed = false;
        for app in self.apps.lock().map_err(super::poison)?.values_mut() {
            if app.app.source != AppSource::Homebrew
                && app.app.catalog_token.is_none()
                && Path::new(&app.app.path)
                    .file_name()
                    .is_some_and(|file| files.iter().any(|f| f == file))
                && super::scan::rule_bundle_matches(raw, app.app.bundle_id.as_deref())
            {
                app.app.catalog_token = Some(token.to_owned());
                changed = true;
            }
        }
        if changed {
            (self.sink)(crate::events::CoreEvent::LocalApps(LocalAppsChanged {
                reason: LocalAppsChangedReason::Refresh,
            }));
        }
        Ok(())
    }
}

impl Engine {
    /// Homebrew records arrive in several events; each one re-enumerates once more after the refresh in flight,
    /// so the last record of a burst is never dropped.
    pub fn schedule_library_refresh(self: &Arc<Self>) {
        if self.library_refresh_pending.swap(true, Ordering::AcqRel) {
            self.library_refresh_dirty.store(true, Ordering::Release);
            return;
        }
        let engine = self.clone();
        tauri::async_runtime::spawn(async move {
            loop {
                tokio::time::sleep(Duration::from_millis(500)).await;
                engine.library_refresh_dirty.store(false, Ordering::Release);
                let scan = engine.clone();
                let _ =
                    crate::blocking(move || scan.refresh(LocalAppsChangedReason::Refresh)).await;
                let _ = engine.hydrate_catalog().await;
                if !engine.library_refresh_dirty.swap(false, Ordering::AcqRel) {
                    break;
                }
            }
            engine
                .library_refresh_pending
                .store(false, Ordering::Release);
            // An event that arrived between the last check and clearing `pending` is not lost.
            if engine.library_refresh_dirty.swap(false, Ordering::AcqRel) {
                engine.schedule_library_refresh();
            }
        });
    }
}
