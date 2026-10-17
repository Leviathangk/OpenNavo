use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct LocalApp {
    pub id: String,
    pub path: String,
    pub name: String,
    pub bundle_id: Option<String>,
    pub version: Option<String>,
    pub source: AppSource,
    pub updater: Option<crate::updater::UpdateSource>,
    pub homebrew_cask: Option<String>,
    pub cask: Option<String>,
    pub catalog_token: Option<String>,
    pub team_id: Option<String>,
    pub vendor: Option<String>,
    pub arch: Option<LocalAppArch>,
    pub size_bytes: Option<u64>,
    pub installed_at: Option<i64>,
    pub last_used_at: Option<i64>,
    pub icon_path: Option<String>,
    pub running: bool,
    pub removable: bool,
    pub protected_reason: Option<ProtectedReason>,
    pub uninstaller: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct LocalAppsChanged {
    pub reason: LocalAppsChangedReason,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Footprint {
    pub app_bytes: u64,
    pub data_bytes: u64,
    pub cache_bytes: u64,
    pub complete: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct FootprintProgress {
    pub total: u32,
    pub completed: u32,
    pub failed: u32,
    pub retrying: u32,
    pub running: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct FootprintReport {
    pub progress: Option<FootprintProgress>,
    pub values: std::collections::HashMap<String, Footprint>,
    pub errors: std::collections::HashMap<String, crate::AppError>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ScanProgress {
    pub request_id: String,
    pub checked: i64,
    pub total: i64,
    pub location: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct UninstallPlan {
    pub plan_id: String,
    pub apps: Vec<AppPlan>,
    pub incomplete: Vec<SkippedLocation>,
    pub created_at: i64,
    pub expires_at: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct AppPlan {
    pub app_id: String,
    pub name: String,
    pub source: AppSource,
    pub running: bool,
    pub removal: AppPlanRemoval,
    pub brew_command: Option<String>,
    pub blocked_reason: Option<AppPlanBlockedReason>,
    pub uninstaller: Option<String>,
    pub personal_data: Option<String>,
    pub items: Vec<PlanItem>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct PlanItem {
    pub id: String,
    pub path: String,
    pub category: ItemCategory,
    pub kind: PlanItemKind,
    pub count: i64,
    pub size_bytes: Option<u64>,
    pub reason: MatchReason,
    pub confidence: PlanItemConfidence,
    pub selected: bool,
    pub required: bool,
    pub needs_admin: bool,
    pub shared_with: Vec<String>,
    pub note: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct SkippedLocation {
    pub path: String,
    pub reason: SkippedLocationReason,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct UninstallChoice {
    pub app_id: String,
    pub item_ids: Vec<String>,
    pub quit_running: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct RestoreResult {
    pub restored: i64,
    pub missing: Vec<String>,
}

#[derive(Debug, Clone, Serialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct UninstallOutcome {
    pub freed_bytes: u64,
    pub trashed_bytes: u64,
    pub trashed: i64,
    pub deleted: i64,
    pub skipped: Vec<SkippedItem>,
    pub restorable: bool,
}

// Legacy storage may omit trashedBytes. Keep this input default separate from the
// IPC output shape: every serialized outcome always supplies a numeric value.
impl<'de> Deserialize<'de> for UninstallOutcome {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        #[derive(Deserialize)]
        #[serde(rename_all = "camelCase")]
        struct StoredOutcome {
            freed_bytes: u64,
            #[serde(default)]
            trashed_bytes: u64,
            trashed: i64,
            deleted: i64,
            skipped: Vec<SkippedItem>,
            restorable: bool,
        }
        let stored = StoredOutcome::deserialize(deserializer)?;
        Ok(Self {
            freed_bytes: stored.freed_bytes,
            trashed_bytes: stored.trashed_bytes,
            trashed: stored.trashed,
            deleted: stored.deleted,
            skipped: stored.skipped,
            restorable: stored.restorable,
        })
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct SkippedItem {
    pub item_id: String,
    pub path: String,
    pub reason: SkippedItemReason,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum AppSource {
    Homebrew,
    AppStore,
    Direct,
    Pkg,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum ProtectedReason {
    System,
    #[serde(rename = "self")]
    SelfApp,
    Managed,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum ItemCategory {
    App,
    AppData,
    Caches,
    Preferences,
    Logs,
    Background,
    Cli,
    Plugins,
    Receipts,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum MatchReason {
    AppBundle,
    BrewUninstall,
    ZapRule,
    BundleId,
    AppGroup,
    Launchd,
    Symlink,
    PkgReceipt,
    TeamId,
    NameExact,
    NameSimilar,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum DeleteMode {
    Trash,
    Delete,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum LocalAppArch {
    Arm64,
    #[serde(rename = "x86_64")]
    X86_64,
    Universal,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum LocalAppsChangedReason {
    Startup,
    Refresh,
    /// Bundle sizes (`LocalApp.sizeBytes`) were measured.
    Measured,
    Running,
    Task,
    Fs,
    /// Only the footprint report changed (values, errors or progress); the app list did not.
    Footprints,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum AppPlanRemoval {
    Brew,
    Trash,
    Blocked,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum AppPlanBlockedReason {
    System,
    #[serde(rename = "self")]
    SelfApp,
    Managed,
    AppManagement,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum PlanItemKind {
    File,
    Dir,
    Symlink,
    Launchd,
    Glob,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum PlanItemConfidence {
    High,
    Medium,
    Low,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum SkippedLocationReason {
    FullDiskAccess,
    Permission,
    Timeout,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum SkippedItemReason {
    Running,
    InUse,
    Permission,
    AdminCanceled,
    Missing,
    Changed,
    Protected,
    /// The tool that runs the command is no longer available (Docker was quit after the scan).
    ToolUnavailable,
}
