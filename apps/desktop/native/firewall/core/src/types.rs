// SPDX-License-Identifier: GPL-3.0-only
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "snake_case")]
pub enum FirewallAvailability {
    Available,
    NotBundled,
    NotInApplications,
    UnsupportedOs,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "snake_case")]
pub enum FirewallExtensionState {
    NotInstalled,
    Activating,
    NeedsApproval,
    NeedsReboot,
    Installed,
    Uninstalling,
    /// Removed and no longer running; macOS deletes it at the next restart.
    RemovalPending,
    Failed,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "snake_case")]
pub enum FirewallFilterState {
    Off,
    NeedsConsent,
    On,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "snake_case")]
pub enum FirewallMode {
    Ask,
    Allow,
    Block,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
pub enum FirewallErrorCode {
    #[serde(rename = "E_FW_NOT_BUNDLED")]
    NotBundled,
    #[serde(rename = "E_FW_NOT_IN_APPLICATIONS")]
    NotInApplications,
    #[serde(rename = "E_FW_ACTIVATION")]
    Activation,
    #[serde(rename = "E_FW_REMOVAL")]
    Removal,
    /// macOS refused to load, save or remove the network filter configuration.
    #[serde(rename = "E_FW_FILTER")]
    Filter,
    #[serde(rename = "E_FW_CONSENT_DENIED")]
    ConsentDenied,
    #[serde(rename = "E_FW_DISCONNECTED")]
    Disconnected,
    #[serde(rename = "E_FW_RULE_INVALID")]
    RuleInvalid,
    #[serde(rename = "E_FW_UNKNOWN_ALERT")]
    UnknownAlert,
    #[serde(rename = "E_FW_BLOCKLIST_URL")]
    BlocklistUrl,
    #[serde(rename = "E_FW_BLOCKLIST_FETCH")]
    BlocklistFetch,
    #[serde(rename = "E_FW_BLOCKLIST_FORMAT")]
    BlocklistFormat,
    #[serde(rename = "E_FW_BLOCKLIST_TOO_LARGE")]
    BlocklistTooLarge,
    /// 32 lists, or 256 MB across all lists, are already stored.
    #[serde(rename = "E_FW_BLOCKLIST_LIMIT")]
    BlocklistLimit,
    /// A local list file is missing or can't be read.
    #[serde(rename = "E_FW_BLOCKLIST_FILE")]
    BlocklistFile,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "camelCase")]
pub struct FirewallFailure {
    pub code: FirewallErrorCode,
    pub detail: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "camelCase")]
pub struct FirewallStatus {
    pub availability: FirewallAvailability,
    pub extension: FirewallExtensionState,
    pub extension_version: Option<String>,
    pub filter: FirewallFilterState,
    pub connected: bool,
    /// Installed-app baseline is being collected; filtering remains active.
    #[serde(default)]
    pub preparing: bool,
    pub mode: FirewallMode,
    pub rule_count: u64,
    pub pending_alerts: u64,
    pub blocked_today: u64,
    pub error: Option<FirewallFailure>,
}

impl FirewallStatus {
    pub fn not_bundled() -> Self {
        Self {
            availability: FirewallAvailability::NotBundled,
            extension: FirewallExtensionState::NotInstalled,
            extension_version: None,
            filter: FirewallFilterState::Off,
            connected: false,
            preparing: false,
            mode: FirewallMode::Ask,
            rule_count: 0,
            pending_alerts: 0,
            blocked_today: 0,
            error: None,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "camelCase")]
pub struct FirewallEnableOptions {
    pub allow_apple: bool,
    pub allow_installed: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "snake_case")]
pub enum FirewallSigner {
    Apple,
    AppStore,
    DeveloperId,
    Adhoc,
    Unsigned,
    Invalid,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "camelCase")]
pub struct FirewallApp {
    pub key: String,
    pub name: String,
    pub path: String,
    pub bundle_path: Option<String>,
    pub bundle_id: Option<String>,
    pub signing_id: Option<String>,
    pub team_id: Option<String>,
    pub team_name: Option<String>,
    pub signer: FirewallSigner,
    pub icon_path: Option<String>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "snake_case")]
pub enum FirewallAction {
    Allow,
    Block,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "snake_case")]
pub enum FirewallRuleSource {
    User,
    Apple,
    Installed,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "snake_case")]
pub enum FirewallRuleUntil {
    Always,
    AppQuits,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "camelCase")]
pub struct FirewallRule {
    pub id: String,
    pub app: Option<FirewallApp>,
    pub action: FirewallAction,
    pub remote: Option<String>,
    pub port: Option<u16>,
    pub until: FirewallRuleUntil,
    pub pid: Option<u32>,
    pub source: FirewallRuleSource,
    pub created_at: i64,
    pub last_match_at: Option<i64>,
    pub match_count: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "camelCase")]
pub struct FirewallRuleInput {
    pub id: Option<String>,
    pub app_key: Option<String>,
    pub app_path: Option<String>,
    pub action: FirewallAction,
    pub remote: Option<String>,
    // Accept out-of-range integers so validation can return E_FW_RULE_INVALID.
    pub port: Option<i64>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "camelCase")]
pub struct FirewallProcess {
    pub pid: u32,
    pub name: String,
    pub path: Option<String>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "snake_case")]
pub enum FirewallProtocol {
    Tcp,
    Udp,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "camelCase")]
pub struct FirewallAlert {
    pub id: String,
    pub app: FirewallApp,
    pub pid: u32,
    pub args: Vec<String>,
    pub ancestors: Vec<FirewallProcess>,
    pub remote_address: String,
    pub remote_host: Option<String>,
    pub port: u16,
    pub protocol: FirewallProtocol,
    pub at: i64,
    pub waiting: u64,
    pub released: bool,
    pub deadline: Option<i64>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "snake_case")]
pub enum FirewallReplyScope {
    App,
    Destination,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "camelCase")]
pub struct FirewallReply {
    pub alert_id: String,
    pub action: FirewallAction,
    pub scope: FirewallReplyScope,
    pub until: FirewallRuleUntil,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "snake_case")]
pub enum FirewallVerdictReason {
    Rule,
    Blocklist,
    Apple,
    Installed,
    Mode,
    Answer,
    Timeout,
    NoClient,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "camelCase")]
pub struct FirewallEvent {
    pub id: String,
    pub at: i64,
    pub app: FirewallApp,
    pub remote_address: String,
    pub remote_host: Option<String>,
    pub port: u16,
    pub protocol: FirewallProtocol,
    pub action: FirewallAction,
    pub reason: FirewallVerdictReason,
    pub rule_id: Option<String>,
    pub blocklist_id: Option<String>,
    pub count: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "camelCase")]
pub struct FirewallEventsQuery {
    pub before: Option<i64>,
    pub before_id: Option<String>,
    pub limit: u32,
    pub blocked_only: bool,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "camelCase")]
pub struct FirewallBlocklist {
    pub id: String,
    pub name: String,
    pub source: String,
    pub enabled: bool,
    pub entries: u64,
    pub updated_at: Option<i64>,
    pub error: Option<FirewallErrorCode>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "camelCase")]
pub struct FirewallSettings {
    pub mode: FirewallMode,
    pub allow_apple: bool,
    pub allow_installed: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[cfg_attr(feature = "specta", derive(specta::Type))]
#[serde(rename_all = "snake_case")]
pub enum FirewallChangeKind {
    Rules,
    Settings,
    Blocklists,
}
