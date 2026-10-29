pub mod types;
pub use types::*;
pub mod apps;
pub mod execute;
mod footprint;
#[cfg(target_os = "macos")]
pub(crate) mod fsevents;
pub mod identity;
pub(crate) mod leftovers;
pub mod manifest;
pub mod rules;
pub mod scan;
mod watch;

use crate::{AppError, db::Database, events::EventSink, model::*, running_apps::AppControl};
use identity::{Identity, MetadataProbe, Receipt};
use rules::{Roots, Snapshot};
use std::{
    collections::{BTreeMap, HashMap},
    path::PathBuf,
    sync::{
        Arc, Mutex, RwLock,
        atomic::{AtomicBool, AtomicU64},
    },
};

#[derive(Debug, Clone)]
pub struct CachedApp {
    pub app: LocalApp,
    pub identity: Identity,
    pub receipts: Vec<Receipt>,
}
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub enum ItemAction {
    Path {
        empty_only: bool,
        bootout: Option<(String, bool)>,
    },
    Forget {
        package: String,
    },
    RestoreTrash {
        original: PathBuf,
        reason: MatchReason,
    },
    PurgeTrash,
    BrewStep,
    /// A fixed prune command of a developer tool (Docker); the reason selects the arguments.
    ToolCommand,
    LoginItem,
}
/// Cask rules by token.
pub(crate) type Rules = Arc<HashMap<String, serde_json::Value>>;
#[derive(Debug, Clone)]
pub struct PlanEntry {
    pub plan: UninstallPlan,
    pub snapshots: BTreeMap<String, Snapshot>,
    pub actions: BTreeMap<String, ItemAction>,
    pub app_records: BTreeMap<String, CachedApp>,
    /// Items whose size leaves out something that could not be read.
    pub partial: std::collections::BTreeSet<String>,
}
pub struct Engine {
    pub roots: Roots,
    pub database: Database,
    pub sink: EventSink,
    pub control: Arc<dyn AppControl>,
    pub probe: Arc<dyn MetadataProbe>,
    pub settings: Arc<RwLock<Settings>>,
    pub askpass: PathBuf,
    pub(crate) file_actions: Mutex<Arc<dyn execute::FileActions>>,
    pub(crate) admin_actions: Mutex<Option<Arc<dyn execute::AdminActions>>>,
    pub(crate) reserved: Mutex<std::collections::BTreeSet<(String, String)>>,
    icon_cache: PathBuf,
    pub(crate) apps: Mutex<BTreeMap<String, CachedApp>>,
    pub(crate) plans: Mutex<HashMap<String, PlanEntry>>,
    pub(crate) requests: Mutex<HashMap<String, Arc<AtomicBool>>>,
    pub(crate) rules_cache: Mutex<HashMap<String, serde_json::Value>>,
    pub(crate) catalog_attempts: Mutex<HashMap<String, i64>>,
    /// Probe results by bundle path (see `MetadataProbe::cacheable`).
    pub(crate) probe_cache: Mutex<HashMap<PathBuf, apps::Probed>>,
    /// Bundle key of every listed app, by id, and the bundle sizes measured for those keys.
    pub(crate) keys: Mutex<HashMap<String, apps::BundleKey>>,
    pub(crate) sizes: Mutex<HashMap<String, (apps::BundleKey, u64)>>,
    pub(crate) seen: Mutex<apps::Seen>,
    pub(crate) catalog: Mutex<Option<Arc<apps::CatalogIndex>>>,
    /// Rules of installed casks parsed from `brew info --installed`, keyed by a hash of that output.
    installed_rules: Mutex<Option<(u64, Rules)>>,
    /// Apps generation and catalog revision of the last catalog hydration, and when it ran.
    pub(crate) hydrated: Mutex<Option<(u64, String, i64)>>,
    pub(crate) hydrate_lock: tokio::sync::Mutex<()>,
    pub(crate) footprints: Mutex<HashMap<String, footprint::CachedFootprint>>,
    footprint_jobs: Mutex<footprint::FootprintJobs>,
    footprint_sequence: AtomicU64,
    footprint_slots: Arc<tokio::sync::Semaphore>,
    footprint_locks: Mutex<HashMap<String, Arc<tokio::sync::Mutex<()>>>>,
    refresh_lock: Mutex<()>,
    operation_lock: Mutex<()>,
    enumerated: AtomicBool,
    library_refresh_pending: AtomicBool,
    library_refresh_dirty: AtomicBool,
    generation: AtomicU64,
}
pub struct EngineConfig {
    pub database: Database,
    pub settings: Arc<RwLock<Settings>>,
    pub sink: EventSink,
    pub roots: Roots,
    pub control: Arc<dyn AppControl>,
    pub probe: Arc<dyn MetadataProbe>,
    pub icon_cache: PathBuf,
    pub askpass: PathBuf,
}
impl Engine {
    pub fn new(config: EngineConfig) -> Arc<Self> {
        let EngineConfig {
            database,
            settings,
            sink,
            roots,
            control,
            probe,
            icon_cache,
            askpass,
        } = config;
        Arc::new(Self {
            roots,
            database: database.clone(),
            settings,
            sink,
            control,
            probe,
            icon_cache,
            askpass,
            file_actions: Mutex::new(Arc::new(execute::NativeFiles)),
            admin_actions: Mutex::new(None),
            reserved: Mutex::new(Default::default()),
            apps: Mutex::new(BTreeMap::new()),
            plans: Mutex::new(HashMap::new()),
            requests: Mutex::new(HashMap::new()),
            rules_cache: Mutex::new(HashMap::new()),
            probe_cache: Mutex::new(HashMap::new()),
            keys: Mutex::new(HashMap::new()),
            sizes: Mutex::new(HashMap::new()),
            seen: Mutex::new(apps::Seen::load(&database)),
            catalog: Mutex::new(None),
            installed_rules: Mutex::new(None),
            hydrated: Mutex::new(None),
            hydrate_lock: tokio::sync::Mutex::new(()),
            footprints: Mutex::new(
                database
                    .meta("footprints_v1")
                    .ok()
                    .flatten()
                    .and_then(|raw| serde_json::from_str(&raw).ok())
                    .unwrap_or_default(),
            ),
            footprint_jobs: Mutex::new(Default::default()),
            footprint_sequence: AtomicU64::new(0),
            footprint_slots: Arc::new(tokio::sync::Semaphore::new(2)),
            footprint_locks: Mutex::new(HashMap::new()),
            catalog_attempts: Mutex::new(Default::default()),
            refresh_lock: Mutex::new(()),
            operation_lock: Mutex::new(()),
            enumerated: AtomicBool::new(false),
            library_refresh_pending: AtomicBool::new(false),
            library_refresh_dirty: AtomicBool::new(false),
            generation: AtomicU64::new(0),
        })
    }
    pub fn seed_rule(&self, token: &str, value: serde_json::Value) -> Result<(), AppError> {
        crate::brew::args::split_cask_token(token)?;
        self.rules_cache
            .lock()
            .map_err(poison)?
            .insert(token.to_owned(), value);
        Ok(())
    }
    pub fn local_rule(&self, token: &str) -> Option<serde_json::Value> {
        self.local_rule_with(token, &self.installed_rules())
    }
    /// Rule reads go to Homebrew with its installed identity and to the site with its alias.
    fn rule_identity(&self, token: &str) -> Result<(String, Option<String>), AppError> {
        let (tap, upstream) = crate::brew::args::split_cask_token(token)?;
        let catalog = if tap.is_some() {
            crate::catalog::get_brew_cask(&self.database, upstream, tap)?
        } else {
            crate::catalog::get(&self.database, Kind::Cask, token)?
        };
        let identity = match catalog.as_ref() {
            Some(item) if tap.is_none() => crate::brew::args::qualified_token(
                item.tap.as_deref(),
                item.brew_token.as_deref().unwrap_or(&item.token),
            )?,
            _ => token.to_owned(),
        };
        Ok((identity, catalog.map(|item| item.token)))
    }
    /// `local_rule` with the installed casks' rules already parsed, for callers that look up many tokens.
    pub(crate) fn local_rule_with(
        &self,
        token: &str,
        installed: &HashMap<String, serde_json::Value>,
    ) -> Option<serde_json::Value> {
        let (identity, catalog_token) = self.rule_identity(token).ok()?;
        for key in [
            Some(token),
            Some(identity.as_str()),
            catalog_token.as_deref(),
        ]
        .into_iter()
        .flatten()
        {
            if let Some(value) = self.rules_cache.lock().ok()?.get(key) {
                return Some(value.clone());
            }
            if let Some(value) = self
                .database
                .meta(&format!("uninstall_rules:{key}"))
                .ok()
                .flatten()
                .and_then(|s| serde_json::from_str(&s).ok())
            {
                return Some(value);
            }
        }
        if let Some(value) = installed.get(&identity) {
            return Some(value.clone());
        }
        let (tap, upstream) = crate::brew::args::split_cask_token(&identity).ok()?;
        // Homebrew sometimes omits its default tap. Both spellings refer to the official cask.
        if tap.is_none_or(|tap| tap == "homebrew/cask")
            && let Some(value) = installed
                .get(&format!("homebrew/cask/{upstream}"))
                .or_else(|| installed.get(upstream))
        {
            return Some(value.clone());
        }
        // Local Homebrew API or Caskroom metadata. No app paths are uploaded.
        let mut files = Vec::new();
        if tap.is_none_or(|tap| tap == "homebrew/cask") {
            files.push(
                self.roots
                    .home
                    .join("Library/Caches/Homebrew/api/cask")
                    .join(format!("{upstream}.json")),
            );
        }
        for root in &self.roots.caskrooms {
            collect_json(&root.join(upstream).join(".metadata"), 4, &mut files);
        }
        files.into_iter().find_map(|p| {
            let m = std::fs::symlink_metadata(&p).ok()?;
            if m.is_symlink() || m.len() > 16 * 1024 * 1024 || !rules::parents_safe(&p) {
                return None;
            }
            let v: serde_json::Value = serde_json::from_slice(&std::fs::read(p).ok()?).ok()?;
            (v.get("token").and_then(|t| t.as_str()) == Some(upstream)
                && tap.is_none_or(|tap| v.get("tap").and_then(|t| t.as_str()) == Some(tap)))
            .then_some(v)
        })
    }
    /// Casks of the last `brew info --installed` output by token, parsed again only when that output changes.
    pub(crate) fn installed_rules(&self) -> Rules {
        use std::hash::{Hash, Hasher};
        let Some(raw) = self.database.meta("cask_rules_cache").ok().flatten() else {
            return Arc::default();
        };
        let mut hasher = std::collections::hash_map::DefaultHasher::new();
        raw.hash(&mut hasher);
        let hash = hasher.finish();
        if let Ok(cached) = self.installed_rules.lock()
            && let Some((key, rules)) = cached.as_ref()
            && *key == hash
        {
            return rules.clone();
        }
        let casks = serde_json::from_str::<serde_json::Value>(&raw)
            .ok()
            .and_then(|value| value.get("casks")?.as_array().cloned())
            .unwrap_or_default();
        let mut rules = HashMap::new();
        for cask in casks {
            let Some(token) = cask.get("token").and_then(|v| v.as_str()) else {
                continue;
            };
            let identity = match cask.get("tap").and_then(|v| v.as_str()) {
                Some(tap) => format!("{tap}/{token}"),
                None => token.to_owned(),
            };
            rules.insert(identity, cask);
        }
        let rules = Arc::new(rules);
        if let Ok(mut cached) = self.installed_rules.lock() {
            *cached = Some((hash, rules.clone()));
        }
        rules
    }
    pub async fn fetch_rule(&self, token: &str) -> Result<Option<serde_json::Value>, AppError> {
        let (identity, catalog_token) = self.rule_identity(token)?;
        if let Some(value) = self.rules_cache.lock().map_err(poison)?.get(token).cloned() {
            return Ok(Some(value));
        }
        let settings = self.settings.read().map_err(poison)?.clone();
        // Prefer live brew; it knows current platform-specific declarations.
        if let Ok(brew) = crate::brew::locate::locate(&settings).await
            && let Ok(raw) = crate::brew::locate::read(
                &brew,
                &settings,
                crate::brew::args::ReadOp::Info(Kind::Cask),
                Some(&identity),
            )
            .await
            && raw.len() <= 16 * 1024 * 1024
            && let Ok(value) = serde_json::from_str::<serde_json::Value>(&raw)
            && let Some(cask) = value
                .get("casks")
                .and_then(|a| a.as_array())
                .and_then(|a| a.first())
        {
            self.seed_rule(token, cask.clone())?;
            self.database
                .set_meta(&format!("uninstall_rules:{token}"), &cask.to_string())?;
            return Ok(Some(cask.clone()));
        }
        if let Some(value) = self.local_rule(token) {
            return Ok(Some(value));
        }
        let Some(catalog_token) = catalog_token else {
            return Ok(None);
        };
        let client = reqwest::Client::builder()
            .timeout(std::time::Duration::from_secs(5))
            .redirect(reqwest::redirect::Policy::none())
            .build()?;
        let url = format!(
            "{}/packages/cask/{catalog_token}",
            crate::catalog::sync::configured_base().trim_end_matches('/')
        );
        let Ok(mut response) = client
            .get(url)
            .send()
            .await
            .and_then(reqwest::Response::error_for_status)
        else {
            return Ok(None);
        };
        let mut bytes = Vec::new();
        while let Some(chunk) = response.chunk().await? {
            if bytes.len() + chunk.len() > 16 * 1024 * 1024 {
                return Ok(None);
            }
            bytes.extend_from_slice(&chunk);
        }
        let value: serde_json::Value = serde_json::from_slice(&bytes)?;
        let value = value.get("data").unwrap_or(&value).clone();
        self.seed_rule(token, value.clone())?;
        self.database
            .set_meta(&format!("uninstall_rules:{token}"), &value.to_string())?;
        Ok(Some(value))
    }
    pub fn cached_plan(&self, id: &str, now: i64) -> Result<PlanEntry, AppError> {
        let mut plans = self.plans.lock().map_err(poison)?;
        plans.retain(|_, p| p.plan.expires_at > now);
        self.reserved
            .lock()
            .map_err(poison)?
            .retain(|(plan, _)| plans.contains_key(plan));
        plans
            .get(id)
            .cloned()
            .ok_or_else(|| AppError::new("E_PLAN_EXPIRED", "uninstall_plan_expired"))
    }
    pub fn cancel(&self, id: &str) -> Result<(), AppError> {
        if let Some(flag) = self.requests.lock().map_err(poison)?.get(id) {
            flag.store(true, std::sync::atomic::Ordering::Release);
        }
        self.plans.lock().map_err(poison)?.remove(id);
        Ok(())
    }
    pub(crate) fn begin(&self, id: &str) -> Result<Arc<AtomicBool>, AppError> {
        if id.is_empty() || id.len() > 128 || id.chars().any(char::is_control) {
            return Err(AppError::new("E_INVALID_ARG", "request_id"));
        }
        let mut requests = self.requests.lock().map_err(poison)?;
        self.plans
            .lock()
            .map_err(poison)?
            .retain(|_, p| p.plan.expires_at > now());
        if requests.contains_key(id) || self.plans.lock().map_err(poison)?.contains_key(id) {
            return Err(AppError::new("E_INVALID_ARG", "duplicate_request_id"));
        }
        let flag = Arc::new(AtomicBool::new(false));
        requests.insert(id.to_owned(), flag.clone());
        Ok(flag)
    }
}
fn collect_json(root: &std::path::Path, depth: u8, files: &mut Vec<PathBuf>) {
    if depth == 0 || files.len() > 1000 {
        return;
    }
    if let Ok(entries) = std::fs::read_dir(root) {
        for e in entries.flatten() {
            if e.file_type().is_ok_and(|t| t.is_dir() && !t.is_symlink()) {
                collect_json(&e.path(), depth - 1, files);
            } else if e.path().extension().is_some_and(|e| e == "json") {
                files.push(e.path());
            }
        }
    }
}
pub fn now() -> i64 {
    chrono::Utc::now().timestamp_millis()
}
pub(crate) fn poison<T>(_: T) -> AppError {
    AppError::new("E_UNKNOWN", "uninstall_lock_poisoned")
}
pub fn install_watch(app: &tauri::AppHandle) {
    watch::install(app);
}
