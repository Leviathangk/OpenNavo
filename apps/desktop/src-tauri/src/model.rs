use crate::AppError;
pub use crate::cleanup::types::*;
pub use crate::monitor::types::*;
pub use crate::uninstall::types::*;
pub use crate::updater::types::*;
use serde::{Deserialize, Serialize};

/// Sparse maps accept legacy nulls; IPC emits only text actually present.
#[derive(Debug, Clone, Serialize, specta::Type, PartialEq, Default)]
#[serde(transparent)]
pub struct LocalizedText(pub std::collections::HashMap<Locale, String>);
impl<'de> Deserialize<'de> for LocalizedText {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        let values = app_locale_map::<_, Option<String>>(deserializer)?;
        Ok(Self(
            values
                .into_iter()
                .filter_map(|(key, value)| value.filter(|s| !s.is_empty()).map(|s| (key, s)))
                .collect(),
        ))
    }
}

// The content API can add website languages independently of the desktop UI. Ignore unsupported
// map keys before deserializing their values; known app languages still receive full validation.
pub(crate) fn app_locale_map<'de, D, T>(
    deserializer: D,
) -> Result<std::collections::HashMap<Locale, T>, D::Error>
where
    D: serde::Deserializer<'de>,
    T: serde::de::DeserializeOwned,
{
    let values = std::collections::HashMap::<String, serde_json::Value>::deserialize(deserializer)?;
    values
        .into_iter()
        .filter_map(|(code, value)| {
            serde_json::from_value::<Locale>(serde_json::Value::String(code))
                .ok()
                .map(|locale| {
                    serde_json::from_value(value)
                        .map(|value| (locale, value))
                        .map_err(serde::de::Error::custom)
                })
        })
        .collect()
}

// A language saved by a newer build that this one does not know falls back to the system language, so every other
// setting still loads (for example after a downgrade).
fn tolerant_locale<'de, D: serde::Deserializer<'de>>(deserializer: D) -> Result<Locale, D::Error> {
    let code = String::deserialize(deserializer)?;
    Ok(serde_json::from_value(serde_json::Value::String(code))
        .unwrap_or_else(|_| crate::settings::system_locale()))
}

// An appearance this build does not know (saved by a newer one) or any non-string value defaults to dark, so every
// other setting still loads.
fn tolerant_appearance<'de, D: serde::Deserializer<'de>>(
    deserializer: D,
) -> Result<Appearance, D::Error> {
    let value = serde_json::Value::deserialize(deserializer)?;
    Ok(serde_json::from_value(value).unwrap_or_default())
}

// Source languages the client does not know use the existing English fallback.
pub(crate) fn app_source_locale<'de, D: serde::Deserializer<'de>>(
    deserializer: D,
) -> Result<Locale, D::Error> {
    let code = String::deserialize(deserializer)?;
    Ok(serde_json::from_value(serde_json::Value::String(code)).unwrap_or(Locale::EnUs))
}
impl LocalizedText {
    pub fn get(&self, locale: Locale) -> Option<&String> {
        self.0.get(&locale)
    }
    pub fn fallback(&self, locale: Locale, source: Locale) -> Option<&String> {
        self.get(locale)
            .or_else(|| self.get(Locale::EnUs))
            .or_else(|| self.get(source))
    }
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq, Default)]
#[serde(rename_all = "snake_case")]
pub enum LocaleMode {
    #[default]
    System,
    Manual,
}
/// Window appearance: follow macOS, or keep every OpenNavo window light or dark.
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq, Default)]
#[serde(rename_all = "snake_case")]
pub enum Appearance {
    System,
    Light,
    #[default]
    Dark,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
pub struct LocaleChanged {
    pub locale: Locale,
}
fn default_source_locale() -> Locale {
    Locale::EnUs
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
pub struct OnRequest {
    pub d30: u64,
    pub d90: u64,
    pub d365: u64,
}
/// Date strings retain the HTTP catalog format; local IPC times use Unix milliseconds.
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CatalogItem {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub bundle_ids: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub link: Option<crate::catalog::links::LinkInfo>,
    #[serde(
        default = "default_source_locale",
        deserialize_with = "app_source_locale"
    )]
    #[specta(type = Locale)]
    pub source_locale: Locale,
    pub kind: Kind,
    pub token: String,
    pub name: String,
    pub names: Vec<String>,
    #[serde(default)]
    pub display_name: LocalizedText,
    #[serde(default)]
    pub summary: LocalizedText,
    pub version: String,
    pub homepage: Option<String>,
    pub auto_updates: bool,
    pub deprecated: bool,
    pub disabled: bool,
    pub hidden: bool,
    pub is_font: bool,
    pub is_library: bool,
    pub categories: Vec<String>,
    pub tags: Vec<String>,
    pub installs_30d: u64,
    pub rank_30d: Option<u32>,
    pub popularity: f64,
    pub icon_url: Option<String>,
    pub accent_color: Option<String>,
    pub apps: Vec<String>,
    /// Complete installation destinations; absent in older catalogs.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub app_targets: Option<Vec<String>>,
    pub binaries: Vec<String>,
    pub pinyin: Option<String>,
    pub version_changed_at: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub installs_90d: Option<u64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub installs_365d: Option<u64>,
    pub download_size: Option<u64>,
    pub on_request: Option<OnRequest>,
    /// Third-party tap (owner/name) of casks outside Homebrew's own; installs use `<tap>/<token>`.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub tap: Option<String>,
    /// Upstream Homebrew token when the catalog uses a site alias.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub brew_token: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Copy, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum Kind {
    Cask,
    Formula,
    Link,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct EnvInfo {
    pub macos_version: String, // "27.0.1"
    pub arch: String,          // "arm64" | "x86_64"
    pub rosetta: bool,
    pub clt_installed: bool, // xcode-select -p succeeded.
    pub brew: Option<BrewInfo>,
    pub app_management: PermissionState, // Persisted local inference: denied on task permission failure, granted on app update/uninstall success, unknown without records.
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct BrewInfo {
    pub path: String,
    pub prefix: String,
    pub version: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CatalogStatus {
    pub cursor: i64,
    pub item_count: u32,
    pub synced_at: Option<i64>,
    pub syncing: bool,
    #[serde(default)]
    pub links_error: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct SyncResult {
    pub mode: SyncMode, /* snapshot | delta | none */
    pub applied: u32,
    pub cursor: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct SearchQuery {
    pub q: String,
    pub kind: Option<Kind>,
    #[serde(default)]
    pub include_links: bool,
    pub category: Option<String>,
    pub include_disabled: bool,
    pub limit: u32,
    pub offset: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct SearchHit {
    pub item: CatalogItem,
    pub score: f64,
    pub matched_name: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ListQuery {
    pub kind: Option<Kind>,
    #[serde(default)]
    pub include_links: bool,
    pub category: Option<String>,
    pub sort: ListSort, /* popular | updated | name */
    pub include_fonts: bool,
    pub include_libraries: bool,
    pub include_disabled: bool,
    pub limit: u32,
    pub offset: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Page<T> {
    pub total: u32,
    pub items: Vec<T>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct InstalledItem {
    pub kind: Kind,
    pub token: String,
    /// Installed Homebrew identity, independent of the site's catalog alias.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub full_token: Option<String>,
    pub name: String,
    pub installed_version: String,      // Version recorded by brew.
    pub actual_version: Option<String>, // Cask: actual version from Info.plist.
    pub latest_version: Option<String>, // Latest version in the local catalog.
    pub status: InstalledStatus,        // up_to_date | outdated | pinned | self_updated | unknown
    pub on_request: bool,               // Formula installed_on_request; always true for casks.
    pub required_by: Vec<String>,
    pub installed_at: Option<i64>,
    pub size_bytes: Option<u64>,
    pub app_paths: Vec<String>,
    pub icon_path: Option<String>, // Locally extracted PNG, displayed through the asset protocol.
    pub auto_updates: bool,
    pub pinned: bool,
}

impl InstalledItem {
    pub fn install_token(&self) -> &str {
        self.full_token.as_deref().unwrap_or(&self.token)
    }

    /// Task targets may carry the catalog alias or a complete installed identity.
    pub fn matches_token(&self, token: &str) -> bool {
        self.token == token || self.install_token() == token
    }

    pub fn brew_token(&self) -> &str {
        self.full_token
            .as_deref()
            .and_then(|token| token.rsplit('/').next())
            .unwrap_or(&self.token)
    }

    pub fn brew_tap(&self) -> Option<&str> {
        self.full_token
            .as_deref()?
            .rsplit_once('/')
            .map(|(tap, _)| tap)
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct OutdatedItem {
    pub source: UpdateSource,
    pub app_id: Option<String>,
    pub bundle_id: Option<String>,
    pub channel: Option<UpdateChannel>,
    pub major: bool,
    pub notes: Option<UpdateNotes>,
    pub integrity: Option<UpdateIntegrity>,
    pub requires_admin: bool,
    pub running: bool,
    pub relaunch_pending: bool,
    pub skipped_version: Option<String>,
    pub critical_update: bool,
    pub informational_url: Option<String>,
    pub download_host: Option<String>,
    pub expected_team_match: Option<bool>,
    /// Confirmation fingerprint for this release with unverified developer identity.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub unsigned_update: Option<String>,
    pub kind: Kind,
    pub token: String,
    pub name: String,
    pub installed_version: String,
    pub current_version: String,
    pub auto_updates: bool,
    pub pinned: bool,
    pub ignored: bool,
    pub dependents: u32,
    pub download_size: Option<u64>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Copy, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum TaskOp {
    Install,
    Upgrade,
    Uninstall,
    RemoveLeftovers,
    Reinstall,
    Pin,
    Unpin,
    Cleanup,
    Clean,
    Update,
    InstallHomebrew,
    AvDbUpdate,
    /// Restores the version an OpenNavo update replaced (`TaskOptions.rollback_of`).
    Rollback,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct TaskTarget {
    pub kind: TaskKind,
    pub token: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
#[serde(default)]
#[derive(Default)]
pub struct TaskOptions {
    pub source: Option<UpdateSource>,
    /// Per-release confirmation; never a persistent signature bypass.
    pub unsigned_update: Option<String>,
    pub verification: Option<UpdateConsent>,
    pub plan_id: Option<String>,
    pub item_ids: Option<Vec<String>>,
    pub delete_mode: Option<DeleteMode>,
    pub zap: bool,
    pub adopt: bool,
    pub greedy: bool,
    /// Cask uninstall retry only (§6.3): after partial zap failure the app is moved but Caskroom retains a backup; finish with --force.
    pub force: bool,
    pub quit_running: bool,
    pub reopen: bool,
    /// Rollback only: the history id of the update being undone.
    pub rollback_of: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct RunningApp {
    pub target: TaskTarget,
    pub name: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Copy, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum TaskTrigger {
    Manual,
    Schedule,
    Deeplink,
    Bundle,
    Retry,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Copy, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum TaskState {
    Queued,
    Running,
    Succeeded,
    Failed,
    Canceled,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Copy, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum TaskPhase {
    Scanning,
    Preparing,
    Fetching,
    Downloading,
    Installing,
    Linking,
    Cleaning,
    Uninstalling,
    Updating,
    Finishing,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Task {
    pub target_name: Option<String>,
    pub outcome: Option<UninstallOutcome>,
    pub id: String, // UUID v7
    pub op: TaskOp,
    pub target: Option<TaskTarget>,
    pub options: TaskOptions,
    pub trigger: TaskTrigger,
    pub state: TaskState,
    pub phase: Option<TaskPhase>,
    pub percent: Option<f32>, // 0–100; None if unknown.
    pub bytes_done: Option<u64>,
    pub bytes_total: Option<u64>,
    pub speed_bps: Option<u64>,
    pub step_index: Option<u8>,
    pub step_count: Option<u8>,
    pub from_version: Option<String>,
    pub to_version: Option<String>,
    pub error: Option<AppError>,
    pub exit_code: Option<i32>,
    pub log_path: Option<String>,
    pub created_at: i64,
    pub started_at: Option<i64>,
    pub finished_at: Option<i64>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Copy, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum PermissionState {
    Granted,
    Denied,
    Unknown,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Copy, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum SyncMode {
    Snapshot,
    Delta,
    None,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Copy, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum ListSort {
    Popular,
    Updated,
    Name,
    Installs30d,
    Installs90d,
    Installs365d,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Copy, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum InstalledStatus {
    UpToDate,
    Outdated,
    Pinned,
    SelfUpdated,
    Unknown,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Copy, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum CategoryAppliesTo {
    Cask,
    Formula,
    Both,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CategoryItem {
    pub slug: String,
    pub parent: Option<String>,
    pub name: String, // Snapshot name[locale] using the configured UI language; fall back to English, source language, then slug.
    pub icon: String, // lucide:*
    pub applies_to: CategoryAppliesTo,
    pub hidden_by_default: bool,
    pub sort: i32,
    pub package_count: u32, // Nonhidden local package count including descendants, counting each package once (same meaning as server CategoryNode.packageCount).
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct HistoryQuery {
    pub q: Option<String>,               // Case-insensitive token/name substring.
    pub ops: Vec<TaskOp>,                // Empty means all operations.
    pub states: Vec<TaskState>, // Only succeeded / failed / canceled; empty means all three.
    pub target: Option<TaskTarget>, // Limit to one package, for local annotations in detail version history.
    pub include_scheduled_updates: bool, // Hide scheduled brew update tasks by default (§12.1).
    pub from: Option<i64>,
    pub to: Option<i64>, // finished_at range.
    pub limit: u32,
    pub offset: u32, // limit ≤ 200
}

/// What clearing the history did: records deleted, and finished records kept because the uninstall or clean-up can still
/// be undone or the update can still be rolled back.
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct HistoryCleared {
    pub cleared: u32,
    pub kept: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct StorageSummary {
    pub apps_bytes: u64,
    pub app_count: u32, // Installed Cask apps.
    pub formulae_bytes: u64,
    pub formula_count: u32, // Installed Formula packages in Cellar.
    pub cache_bytes: u64,   // brew --cache directory.
    pub complete: bool, // False while background size calculation continues; UI shows Calculating.
    pub measured_at: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct MirrorInput {
    pub key: String,
    pub name: String,
    pub probe_url: String,
    pub api_domain: Option<String>,
    pub bottle_domain: Option<String>,
    pub brew_git_remote: Option<String>,
    pub core_git_remote: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct MirrorProbe {
    pub key: String,
    pub ok: bool,
    pub latency_ms: Option<u32>, // HEAD round-trip latency; None on failure.
    pub status: Option<u16>,     // HTTP status code.
    pub error: Option<String>,   // Machine-readable: timeout | dns | tls | connect | http_{status}.
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct MirrorChoice {
    pub key: String, // "official" means the official source, with all four URLs None.
    pub api_domain: Option<String>,
    pub bottle_domain: Option<String>,
    pub brew_git_remote: Option<String>,
    pub core_git_remote: Option<String>,
}

pub use crate::locales_gen::LocaleCode as Locale;

#[derive(Debug, Clone, Serialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Settings {
    pub onboarding_completed: bool,
    pub locale_mode: LocaleMode,
    pub locale: Locale,
    pub appearance: Appearance,
    pub launch_at_login: bool,
    pub keep_in_menu_bar_on_close: bool,
    pub tray_show_count: bool,
    pub auto_check: bool,
    pub auto_check_app_updates: bool,
    pub av_consent: bool,
    pub av_db_check_hours: u32,
    pub av_keep_resident: bool,
    pub watch_downloads: bool,
    pub startup_alerts: bool,
    pub av_exclusions: Vec<String>,
    pub av_schedule: crate::security::AvSchedule,
    pub av_schedule_time: String,
    pub av_schedule_weekday: u8,
    pub av_scan_before_install: bool,
    pub paste_protection: bool,
    pub virus_total_enabled: bool,
    pub app_store_region: String,
    pub app_store_search: bool,
    /// Deprecated compatibility mirror; updateSources.mas is authoritative.
    pub app_store_updates: bool,
    pub update_sources: UpdateSources,
    pub check_time: String, // "HH:MM" local time; invalid format returns E_INVALID_ARG.
    pub run_brew_update_on_check: bool,
    pub include_greedy: bool,
    pub auto_upgrade_formulae: bool,
    pub auto_upgrade_casks: bool,
    pub notify_updates: bool,
    pub mirror: MirrorChoice,
    pub custom_mirrors: Vec<MirrorInput>, // Local settings only; never sent to the public API.
    pub brew_path: Option<String>,        // None means automatic discovery (§6.1).
    pub homebrew_analytics: Option<bool>, // None leaves analytics unchanged; Some(false) injects HOMEBREW_NO_ANALYTICS=1.
    pub crash_reports: bool,
    pub uninstall_delete_mode: DeleteMode,
    pub zap_by_default: bool,
    pub cleanup_delete_mode: DeleteMode,
    pub cleanup_reminder: CleanupReminder,
    pub cleanup_reminder_bytes: u64,
    pub cleanup_project_roots: Vec<String>,
    pub monitor_menu_bar: MonitorMenuBar,
    pub monitor_interval_ms: MonitorIntervalMs,
    pub monitor_alerts: bool,
    pub monitor_menu_bar_intro: bool,
    pub fan_last_manual: Option<std::collections::BTreeMap<u32, u32>>,
}
#[derive(Debug, Clone, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
#[serde(default)]
struct SettingsInput {
    pub onboarding_completed: Option<bool>,
    pub locale_mode: Option<LocaleMode>,
    #[serde(deserialize_with = "tolerant_locale")]
    pub locale: Locale,
    #[serde(deserialize_with = "tolerant_appearance")]
    pub appearance: Appearance,
    pub launch_at_login: bool,
    pub keep_in_menu_bar_on_close: bool,
    pub tray_show_count: bool,
    pub auto_check: bool,
    pub auto_check_app_updates: bool,
    pub av_consent: bool,
    pub av_db_check_hours: u32,
    pub av_keep_resident: bool,
    pub watch_downloads: bool,
    pub startup_alerts: bool,
    pub av_exclusions: Vec<String>,
    pub av_schedule: crate::security::AvSchedule,
    pub av_schedule_time: String,
    pub av_schedule_weekday: u8,
    pub av_scan_before_install: bool,
    pub paste_protection: bool,
    pub virus_total_enabled: bool,
    pub app_store_region: String,
    pub app_store_search: bool,
    /// Deprecated compatibility mirror; updateSources.mas is authoritative.
    pub app_store_updates: bool,
    pub update_sources: UpdateSources,
    pub check_time: String, // "HH:MM" local time; invalid format returns E_INVALID_ARG.
    pub run_brew_update_on_check: bool,
    pub include_greedy: bool,
    pub auto_upgrade_formulae: bool,
    pub auto_upgrade_casks: bool,
    pub notify_updates: bool,
    pub mirror: MirrorChoice,
    pub custom_mirrors: Vec<MirrorInput>,
    pub brew_path: Option<String>, // None means automatic discovery (§6.1).
    pub homebrew_analytics: Option<bool>, // None leaves analytics unchanged; Some(false) injects HOMEBREW_NO_ANALYTICS=1.
    pub crash_reports: bool,
    pub uninstall_delete_mode: DeleteMode,
    pub zap_by_default: bool,
    pub cleanup_delete_mode: DeleteMode,
    pub cleanup_reminder: CleanupReminder,
    pub cleanup_reminder_bytes: u64,
    pub cleanup_project_roots: Vec<String>,
    pub monitor_menu_bar: MonitorMenuBar,
    pub monitor_interval_ms: MonitorIntervalMs,
    pub monitor_alerts: bool,
    pub monitor_menu_bar_intro: bool,
    pub fan_last_manual: Option<std::collections::BTreeMap<u32, u32>>,
}

impl Default for SettingsInput {
    fn default() -> Self {
        let value = Settings::default();
        Self {
            locale_mode: None,
            onboarding_completed: None,
            locale: value.locale,
            appearance: value.appearance,
            launch_at_login: value.launch_at_login,
            keep_in_menu_bar_on_close: value.keep_in_menu_bar_on_close,
            tray_show_count: value.tray_show_count,
            auto_check: value.auto_check,
            auto_check_app_updates: value.auto_check_app_updates,
            av_consent: value.av_consent,
            av_db_check_hours: value.av_db_check_hours,
            av_keep_resident: value.av_keep_resident,
            watch_downloads: value.watch_downloads,
            startup_alerts: value.startup_alerts,
            av_exclusions: value.av_exclusions,
            av_schedule: value.av_schedule,
            av_schedule_time: value.av_schedule_time,
            av_schedule_weekday: value.av_schedule_weekday,
            av_scan_before_install: value.av_scan_before_install,
            paste_protection: value.paste_protection,
            virus_total_enabled: value.virus_total_enabled,
            app_store_region: value.app_store_region,
            app_store_search: value.app_store_search,
            app_store_updates: value.app_store_updates,
            update_sources: value.update_sources,
            check_time: value.check_time,
            run_brew_update_on_check: value.run_brew_update_on_check,
            include_greedy: value.include_greedy,
            auto_upgrade_formulae: value.auto_upgrade_formulae,
            auto_upgrade_casks: value.auto_upgrade_casks,
            notify_updates: value.notify_updates,
            mirror: value.mirror,
            custom_mirrors: value.custom_mirrors,
            brew_path: value.brew_path,
            homebrew_analytics: value.homebrew_analytics,
            crash_reports: value.crash_reports,
            uninstall_delete_mode: value.uninstall_delete_mode,
            zap_by_default: value.zap_by_default,
            cleanup_delete_mode: value.cleanup_delete_mode,
            cleanup_reminder: value.cleanup_reminder,
            cleanup_reminder_bytes: value.cleanup_reminder_bytes,
            cleanup_project_roots: value.cleanup_project_roots,
            monitor_menu_bar: value.monitor_menu_bar,
            monitor_interval_ms: value.monitor_interval_ms,
            monitor_alerts: value.monitor_alerts,
            monitor_menu_bar_intro: value.monitor_menu_bar_intro,
            fan_last_manual: value.fan_last_manual,
        }
    }
}
impl From<SettingsInput> for Settings {
    fn from(value: SettingsInput) -> Self {
        let mut update_sources = value.update_sources;
        if !value.app_store_updates && !update_sources.0.contains_key(&UpdateSource::Mas) {
            update_sources.0.insert(UpdateSource::Mas, false);
        }
        let app_store_updates = update_sources.enabled(UpdateSource::Mas);
        let mode = value.locale_mode.unwrap_or_else(|| {
            crate::settings::legacy_mode(value.locale, crate::settings::system_locale())
        });
        Self {
            locale_mode: mode,
            // A missing key means pre-upgrade settings; avoid sending existing users through onboarding again.
            onboarding_completed: value.onboarding_completed.unwrap_or(true),
            locale: value.locale,
            appearance: value.appearance,
            launch_at_login: value.launch_at_login,
            keep_in_menu_bar_on_close: value.keep_in_menu_bar_on_close,
            tray_show_count: value.tray_show_count,
            auto_check: value.auto_check,
            auto_check_app_updates: value.auto_check_app_updates,
            av_consent: value.av_consent,
            av_db_check_hours: value.av_db_check_hours,
            av_keep_resident: value.av_keep_resident,
            watch_downloads: value.watch_downloads,
            startup_alerts: value.startup_alerts,
            av_exclusions: value.av_exclusions,
            av_schedule: value.av_schedule,
            av_schedule_time: value.av_schedule_time,
            av_schedule_weekday: value.av_schedule_weekday,
            av_scan_before_install: value.av_scan_before_install,
            paste_protection: value.paste_protection,
            virus_total_enabled: value.virus_total_enabled,
            app_store_region: value.app_store_region,
            app_store_search: value.app_store_search,
            app_store_updates,
            update_sources,
            check_time: value.check_time,
            run_brew_update_on_check: value.run_brew_update_on_check,
            include_greedy: value.include_greedy,
            auto_upgrade_formulae: value.auto_upgrade_formulae,
            auto_upgrade_casks: value.auto_upgrade_casks,
            notify_updates: value.notify_updates,
            mirror: value.mirror,
            custom_mirrors: value.custom_mirrors,
            brew_path: value.brew_path,
            homebrew_analytics: value.homebrew_analytics,
            crash_reports: value.crash_reports,
            uninstall_delete_mode: value.uninstall_delete_mode,
            zap_by_default: value.zap_by_default,
            cleanup_delete_mode: value.cleanup_delete_mode,
            cleanup_reminder: value.cleanup_reminder,
            cleanup_reminder_bytes: value.cleanup_reminder_bytes,
            cleanup_project_roots: value.cleanup_project_roots,
            monitor_menu_bar: value.monitor_menu_bar,
            monitor_interval_ms: value.monitor_interval_ms,
            monitor_alerts: value.monitor_alerts,
            monitor_menu_bar_intro: value.monitor_menu_bar_intro,
            fan_last_manual: value.fan_last_manual,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Copy, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum BrewfileEntryStatus {
    Ready,
    Installed,
    Skipped,
    Unsupported,
    Invalid,
    NotFound,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct BrewfileEntry {
    pub line: u32,                   // One-based line number.
    pub raw: String,                 // Original line with comments removed.
    pub kind: Option<Kind>,          // brew → formula, cask → cask; None for other entries.
    pub token: Option<String>,       // Populated only if the §6.3 allowlist accepts it.
    pub status: BrewfileEntryStatus, // skipped: no-op entries such as official taps (homebrew/core, homebrew/cask).
    pub reason: Option<String>, // Unsupported category: tap | mas | vscode | whalebrew | other.
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct BrewfilePreview {
    pub entries: Vec<BrewfileEntry>,
    pub ready: u32,
    pub installed: u32,
    pub unsupported: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct DoctorWarning {
    pub title: String, /* First line after Warning:. */
    pub body: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct DoctorReport {
    pub ok: bool, /* "Your system is ready to brew." */
    pub warnings: Vec<DoctorWarning>,
    pub ran_at: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Copy, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum PrivacyPane {
    AppManagement,
    Notifications,
    FullDiskAccess,
}

/// Privacy state (§12.3, §12.6): payload for permissions_get and permissions:changed.
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct Permissions {
    /// App Management: prefer system preflight; effective Full Disk Access implies granted; otherwise infer from task outcomes if preflight is unavailable.
    pub app_management: PermissionState,
    /// System Settings switch only; unlike effective app_management, Full Disk Access never grants it.
    pub app_management_switch: PermissionState,
    /// Full Disk Access: effective only if this process can read protected directories.
    pub full_disk_access: PermissionState,
    /// Enabled in System Settings but not yet effective; quit and reopen OpenNavo.
    pub relaunch_required: bool,
    /// Full Disk Access was still pending after our relaunch; toggle its System Settings switch.
    pub relaunch_ineffective: bool,
    /// App Management is on in System Settings, but this process is still blocked until it reopens.
    pub app_management_pending: bool,
    /// Full Disk Access is on in System Settings, but macOS applies it only after OpenNavo reopens.
    pub full_disk_access_pending: bool,
}

/// Permission overlay (§12.7): the app to drag or choose is the one macOS checks OpenNavo's permissions against.
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct PermissionGuideInfo {
    /// That app is an .app bundle, so its icon can be dragged into the list.
    pub draggable: bool,
    /// Set when that app is not OpenNavo: a development build is checked as the app it was started from, such as a terminal.
    pub owner_name: Option<String>,
    /// PNG of that app's icon, for the asset protocol.
    pub owner_icon: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct TaskLogEvent {
    pub id: String,
    pub lines: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Copy, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum LibraryChangeReason {
    Task,
    Refresh,
    Startup,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct LibraryChanged {
    pub reason: LibraryChangeReason,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct UpdatesChanged {
    pub count: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub enum UpdateCheckFailureReason {
    RulesUnavailable,
    RateLimited,
    InvalidUrl,
    Network,
    Failed,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct UpdateCheckFailure {
    pub source: Option<UpdateSource>,
    pub app_id: Option<String>,
    pub app_name: Option<String>,
    pub reason: UpdateCheckFailureReason,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct UpdatesStatus {
    pub checked_at: Option<i64>,
    pub next_check_at: Option<i64>,
    pub checking: bool,
    pub failed_sources: Vec<UpdateSource>,
    #[serde(default)]
    pub failures: Vec<UpdateCheckFailure>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct QuitRequested {
    pub running: u32,
    pub queued: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Copy, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum DeepLinkAction {
    Install,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct DeepLinkEvent {
    pub url: String,
    pub route: String, // Frontend route, e.g. /package/cask/visual-studio-code, /collection/dev-setup, /search?q=…, /updates.
    pub action: Option<DeepLinkAction>,
}

impl Kind {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Cask => "cask",
            Self::Formula => "formula",
            Self::Link => "link",
        }
    }
    pub fn flag(self) -> &'static str {
        match self {
            Self::Cask => "--cask",
            Self::Formula => "--formula",
            Self::Link => "",
        }
    }
}
impl Default for MirrorChoice {
    fn default() -> Self {
        Self {
            key: "official".into(),
            api_domain: None,
            bottle_domain: None,
            brew_git_remote: None,
            core_git_remote: None,
        }
    }
}
impl Default for Settings {
    fn default() -> Self {
        Self {
            onboarding_completed: false,
            locale_mode: LocaleMode::System,
            locale: Locale::EnUs,
            appearance: Appearance::Dark,
            launch_at_login: false,
            keep_in_menu_bar_on_close: true,
            tray_show_count: true,
            auto_check: true,
            auto_check_app_updates: true,
            av_consent: false,
            av_db_check_hours: 4,
            av_keep_resident: false,
            watch_downloads: true,
            startup_alerts: true,
            av_exclusions: Vec::new(),
            av_schedule: crate::security::AvSchedule::Off,
            av_schedule_time: "03:00".into(),
            av_schedule_weekday: 1,
            av_scan_before_install: true,
            paste_protection: false,
            virus_total_enabled: false,
            app_store_region: "auto".into(),
            app_store_search: true,
            app_store_updates: true,
            update_sources: UpdateSources::default(),
            check_time: "09:00".into(),
            run_brew_update_on_check: true,
            include_greedy: true,
            auto_upgrade_formulae: false,
            auto_upgrade_casks: false,
            notify_updates: true,
            mirror: MirrorChoice::default(),
            custom_mirrors: Vec::new(),
            brew_path: None,
            homebrew_analytics: None,
            crash_reports: false,
            uninstall_delete_mode: DeleteMode::Trash,
            zap_by_default: true,
            cleanup_delete_mode: DeleteMode::Trash,
            cleanup_reminder: CleanupReminder::Off,
            cleanup_reminder_bytes: 10_000_000_000,
            monitor_menu_bar: MonitorMenuBar::default(),
            monitor_interval_ms: MonitorIntervalMs::default(),
            monitor_alerts: false,
            monitor_menu_bar_intro: false,
            fan_last_manual: None,
            cleanup_project_roots: ["~/Projects", "~/Developer", "~/code", "~/GitHub", "~/dev"]
                .into_iter()
                .map(str::to_owned)
                .collect(),
        }
    }
}
impl Locale {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::ZhCn => "zh-CN",
            Self::EnUs => "en-US",
            Self::JaJp => "ja-JP",
            Self::EsEs => "es-ES",
            Self::PtBr => "pt-BR",
            Self::RuRu => "ru-RU",
            Self::DeDe => "de-DE",
            Self::FrFr => "fr-FR",
            Self::ItIt => "it-IT",
            Self::ZhHant => "zh-Hant",
        }
    }
}

impl<'de> Deserialize<'de> for Settings {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        SettingsInput::deserialize(deserializer).map(Into::into)
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum InstallPreflightStatus {
    Ready,
    Managed,
    Conflict,
    Blocked,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct InstallPreflight {
    pub name: String,
    pub paths: Vec<String>,
    pub status: InstallPreflightStatus,
    /// Set for casks from a third-party tap, which brew clones from GitHub on first install.
    pub tap: Option<String>,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum TaskKind {
    Cask,
    Formula,
    App,
    Mas,
    // Accepted at the IPC boundary solely to reject it before queue persistence.
    Link,
}
impl From<Kind> for TaskKind {
    fn from(k: Kind) -> Self {
        match k {
            Kind::Cask => Self::Cask,
            Kind::Formula => Self::Formula,
            Kind::Link => Self::Link,
        }
    }
}
impl PartialEq<Kind> for TaskKind {
    fn eq(&self, k: &Kind) -> bool {
        *self == Self::from(*k)
    }
}
impl PartialEq<TaskKind> for Kind {
    fn eq(&self, k: &TaskKind) -> bool {
        k == self
    }
}
impl TaskKind {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Cask => "cask",
            Self::Formula => "formula",
            Self::App => "app",
            Self::Mas => "mas",
            Self::Link => "link",
        }
    }
    pub fn flag(self) -> &'static str {
        match self {
            Self::Cask => "--cask",
            Self::Formula => "--formula",
            Self::App => "",
            Self::Mas => "",
            Self::Link => "",
        }
    }
    pub fn brew_kind(self) -> Result<Kind, AppError> {
        match self {
            Self::Cask => Ok(Kind::Cask),
            Self::Formula => Ok(Kind::Formula),
            Self::App => Err(AppError::new("E_INVALID_ARG", "local_app_target")),
            Self::Mas => Err(AppError::new("E_INVALID_ARG", "mas_target")),
            Self::Link => Err(AppError::new("E_NOT_INSTALLABLE", "link_target")),
        }
    }
}
