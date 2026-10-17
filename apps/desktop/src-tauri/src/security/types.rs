// SPDX-License-Identifier: GPL-3.0-only
// Security IPC: store/security plan sections 11.2 and 11.5.
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum SignatureStatus {
    NotCode,
    Unknown,
    Modified,
    Apple,
    AppStore,
    DeveloperId,
    Adhoc,
    Unsigned,
    Invalid,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum PostureId {
    Sip,
    Ssv,
    Gatekeeper,
    Filevault,
    Firewall,
    Xprotect,
    Updates,
    Screenlock,
    Autologin,
    Guest,
    Sharing,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum PostureState {
    Ok,
    Warn,
    Bad,
    Info,
    Unknown,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum AvScanKind {
    Quick,
    Full,
    Custom,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum AvScanState {
    Running,
    Done,
    Canceled,
    Failed,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum FindingStatus {
    Open,
    Quarantined,
    Deleted,
    Trusted,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum FindingAction {
    Quarantine,
    Delete,
    Trust,
    Reveal,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum StartupCategory {
    LaunchItems,
    Btm,
    LoginItems,
    Cron,
    BrowserExtensions,
    Kexts,
    SystemExtensions,
    AppExtensions,
    AuthPlugins,
    DirServicesPlugins,
    Spotlight,
    Quicklook,
    DockTiles,
    LibraryInserts,
    LibraryProxies,
    LoginHooks,
    Periodic,
    StartupScripts,
    ShellConfig,
    EventRules,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum StartupAction {
    Restore,
    Disable,
    Enable,
    Remove,
    Reveal,
    Scan,
    OpenSettings,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum StartupItemAction {
    Restore,
    Disable,
    Enable,
    Remove,
    Reveal,
    Scan,
    MarkReviewed,
    OpenSettings,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum StartupScope {
    User,
    System,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum StartupTrust {
    Apple,
    Known,
    Unknown,
    Untrusted,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum StartupState {
    Removed,
    Running,
    Loaded,
    Enabled,
    Disabled,
    Unknown,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum StartupTrigger {
    AtLoad,
    KeepAlive,
    Interval,
    Calendar,
    WatchPaths,
    Events,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum StartupRisk {
    None,
    Notice,
    High,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum BtmFormat {
    V16,
    V18,
    Unknown,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum ProtectionEventKind {
    ScheduledScanSkipped,
    SignatureChanged,
    InstallThreat,
    PasteBlocked,
    StartupAdded,
    StartupRemoved,
    DownloadThreat,
    DownloadUnsigned,
    ScanThreat,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum ProtectionState {
    Unavailable,
    Off,
    On,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum SecurityChangeReason {
    Schedule,
    Signatures,
    Posture,
    Engine,
    Startup,
    Quarantine,
    Events,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, specta::Type,
)]
#[serde(rename_all = "snake_case")]
pub enum DownloadFormat {
    Dmg,
    Zip,
    Pkg,
    App,
    Other,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct SignatureInfo {
    pub status: SignatureStatus,
    pub team_id: Option<String>,
    pub signer: Option<String>,
    pub notarized: Option<bool>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct PostureCheck {
    pub id: PostureId,
    pub state: PostureState,
    pub facts: BTreeMap<String, PostureFact>,
    pub settings_url: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct AvDatabaseStatus {
    pub daily: Option<u64>,
    pub main: Option<u64>,
    pub bytecode: Option<u64>,
    pub updated_at: Option<i64>,
    pub bytes: u64,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct AvEngineError {
    pub code: String,
    pub at: i64,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct AvEngineStatus {
    pub version: String,
    pub running: bool,
    pub resident_bytes: Option<u64>,
    pub updating: bool,
    pub db: Option<AvDatabaseStatus>,
    pub last_error: Option<AvEngineError>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct AvVerdict {
    pub clean: bool,
    pub signature: Option<String>,
    pub scanned_at: i64,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct FindingOwner {
    pub name: String,
    pub path: String,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct AvFinding {
    pub id: String,
    pub path: String,
    pub signature: String,
    pub size_bytes: u64,
    pub sha256: String,
    pub owner_app: Option<FindingOwner>,
    pub requires_admin: bool,
    pub status: FindingStatus,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type, Default)]
#[serde(rename_all = "camelCase")]
pub struct AvSkipped {
    pub dataless: u64,
    pub too_large: u64,
    pub unreadable: u64,
    pub excluded: u64,
}

#[derive(Debug, Clone, PartialEq, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct AvScanResult {
    pub scan_id: String,
    pub kind: AvScanKind,
    pub state: AvScanState,
    pub error_code: Option<String>,
    pub started_at: i64,
    pub finished_at: Option<i64>,
    pub files_scanned: u64,
    pub bytes_scanned: u64,
    pub skipped: AvSkipped,
    pub denied_roots: Vec<String>,
    pub findings: Vec<AvFinding>,
}

// Defaults belong to stored input only: IPC always includes deniedRoots, including
// old scan records. Do not generate an optional property for a required output.
impl<'de> Deserialize<'de> for AvScanResult {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        #[derive(Deserialize)]
        #[serde(rename_all = "camelCase")]
        struct Stored {
            scan_id: String,
            kind: AvScanKind,
            state: AvScanState,
            error_code: Option<String>,
            started_at: i64,
            finished_at: Option<i64>,
            files_scanned: u64,
            bytes_scanned: u64,
            skipped: AvSkipped,
            #[serde(default)]
            denied_roots: Vec<String>,
            findings: Vec<AvFinding>,
        }
        let v = Stored::deserialize(deserializer)?;
        Ok(Self {
            scan_id: v.scan_id,
            kind: v.kind,
            state: v.state,
            error_code: v.error_code,
            started_at: v.started_at,
            finished_at: v.finished_at,
            files_scanned: v.files_scanned,
            bytes_scanned: v.bytes_scanned,
            skipped: v.skipped,
            denied_roots: v.denied_roots,
            findings: v.findings,
        })
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct AvScanSummary {
    pub scan_id: String,
    pub kind: AvScanKind,
    pub state: AvScanState,
    pub finished_at: Option<i64>,
    pub files_scanned: u64,
    pub threats: u64,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct AvScanStarted {
    pub scan_id: String,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct StartupOwner {
    pub name: String,
    pub bundle_id: Option<String>,
    pub path: String,
    /// Locally extracted PNG shown through the asset protocol; absent from rows saved
    /// before startup items had icons.
    pub icon_path: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct StartupItem {
    pub id: String,
    pub category: StartupCategory,
    pub name: String,
    pub label: Option<String>,
    pub path: String,
    pub program: Option<String>,
    pub args: Vec<String>,
    pub scope: StartupScope,
    pub owner: Option<StartupOwner>,
    pub signature: SignatureInfo,
    pub trust: StartupTrust,
    pub state: StartupState,
    pub triggers: Vec<StartupTrigger>,
    pub first_seen_at: i64,
    pub is_new: bool,
    pub risk: StartupRisk,
    pub actions: Vec<StartupAction>,
    pub requires_admin: bool,
    pub av: Option<AvVerdict>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct StartupSnapshot {
    /// Category-local Cron recovery/read error. Older snapshots omit it.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cron_error: Option<String>,
    pub scanned_at: i64,
    pub items: Vec<StartupItem>,
    pub missing: Vec<StartupCategory>,
    pub btm_format: Option<BtmFormat>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct ActFailure {
    pub id: String,
    pub code: String,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type, Default)]
#[serde(rename_all = "camelCase")]
pub struct ActOutcome {
    pub done: Vec<String>,
    pub failed: Vec<ActFailure>,
    pub restorable: bool,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct QuarantineEntry {
    pub id: String,
    pub name: String,
    pub original_path: String,
    pub size_bytes: u64,
    pub sha256: String,
    pub reason: QuarantineReason,
    pub quarantined_at: i64,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct ProtectionEvent {
    pub id: String,
    pub at: i64,
    pub kind: ProtectionEventKind,
    pub name: String,
    pub path: Option<String>,
    pub item_id: Option<String>,
    pub handled: bool,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type, Default)]
#[serde(rename_all = "camelCase")]
pub struct StartupOverview {
    pub total: u64,
    pub third_party: u64,
    pub untrusted: u64,
    pub pending_review: u64,
    pub changed_at: Option<i64>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct ProtectionOverview {
    pub startup_alerts: bool,
    pub watch_downloads: bool,
    pub endpoint_security: ProtectionState,
    pub firewall: ProtectionState,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type, Default)]
#[serde(rename_all = "camelCase")]
pub struct QuarantineOverview {
    pub count: u64,
    pub bytes: u64,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct SecurityOverview {
    pub posture: Vec<PostureCheck>,
    pub engine: AvEngineStatus,
    pub last_scan: Option<AvScanSummary>,
    pub startup: StartupOverview,
    pub protection: ProtectionOverview,
    pub quarantine: QuarantineOverview,
    pub attention: u64,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct SecuritySettings {
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
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct AvProgress {
    pub scan_id: String,
    pub files_scanned: u64,
    pub bytes_scanned: u64,
    pub current_path: String,
    pub findings: u64,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct SecurityChanged {
    pub reason: SecurityChangeReason,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct StartupAdded {
    pub items: Vec<StartupItem>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct DownloadDetected {
    pub token: String,
    pub path: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "snake_case")]
pub enum DownloadAction {
    Open,
    Reveal,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct DownloadVerdict {
    pub path: String,
    pub sha256: String,
    pub size: u64,
    pub format: DownloadFormat,
    pub signature: SignatureInfo,
    pub team_matches: Option<bool>,
    pub av: Option<AvVerdict>,
    pub quarantined: bool,
    #[serde(default)]
    pub av_skipped: Option<super::AvSkipReason>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(untagged)]
pub enum PostureFact {
    Text(String),
    Number(i64),
    Boolean(bool),
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum QuarantineReason {
    Threat { signature: String },
    Startup,
    User,
}

impl Default for SecuritySettings {
    fn default() -> Self {
        Self {
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
        }
    }
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type)]
pub enum SecurityErrorCode {
    #[serde(rename = "E_INSTALL_THREAT")]
    EInstallThreat,
    #[serde(rename = "E_INSTALL_SCAN")]
    EInstallScan,
    #[serde(rename = "E_AUTOMATION_DENIED")]
    EAutomationDenied,
    #[serde(rename = "E_ACCESSIBILITY_DENIED")]
    EAccessibilityDenied,
    #[serde(rename = "E_PASTE_EXPIRED")]
    EPasteExpired,
    #[serde(rename = "E_STARTUP_CHANGED")]
    EStartupChanged,
    #[serde(rename = "E_VT_DISABLED")]
    EVtDisabled,
    #[serde(rename = "E_VT_KEY_REQUIRED")]
    EVtKeyRequired,
    #[serde(rename = "E_VT_KEY_INVALID")]
    EVtKeyInvalid,
    #[serde(rename = "E_VT_RATE_LIMITED")]
    EVtRateLimited,
    #[serde(rename = "E_VT_UNAVAILABLE")]
    EVtUnavailable,
    #[serde(rename = "E_KEYCHAIN")]
    EKeychain,

    #[serde(rename = "E_DOWNLOAD_UNSAFE")]
    EDownloadUnsafe,
    #[serde(rename = "E_AV_CONSENT")]
    EAvConsent,
    #[serde(rename = "E_AV_NOT_READY")]
    EAvNotReady,
    #[serde(rename = "E_AV_ENGINE")]
    EAvEngine,
    #[serde(rename = "E_AV_DB_RATE_LIMITED")]
    EAvDbRateLimited,
    #[serde(rename = "E_AV_DB_UPDATE")]
    EAvDbUpdate,
    #[serde(rename = "E_STARTUP_PROTECTED")]
    EStartupProtected,
    #[serde(rename = "E_RESTORE_CONFLICT")]
    ERestoreConflict,
    #[serde(rename = "E_SCAN_CANCELED")]
    EScanCanceled,
    #[serde(rename = "E_FULL_DISK_ACCESS")]
    EFullDiskAccess,
    #[serde(rename = "E_ADMIN_CANCELED")]
    EAdminCanceled,
    #[serde(rename = "E_NOT_IMPLEMENTED")]
    ENotImplemented,
}
