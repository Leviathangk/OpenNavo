// SPDX-License-Identifier: GPL-3.0-only
use super::SignatureInfo;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "snake_case")]
pub enum AvSchedule {
    #[default]
    Off,
    Daily,
    Weekly,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "snake_case")]
pub enum AvSkipReason {
    NoDatabase,
    NoConsent,
    Excluded,
    TooLarge,
    Unreadable,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "snake_case")]
pub enum ScheduleSkipReason {
    NoDatabase,
    NoConsent,
    OnBattery,
    Busy,
    Error,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct ScheduledScanRun {
    pub scheduled_at: i64,
    pub scan_id: Option<String>,
    pub skipped: Option<ScheduleSkipReason>,
    pub error_code: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct AvScheduleStatus {
    pub next_run_at: Option<i64>,
    pub last_run: Option<ScheduledScanRun>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "snake_case")]
pub enum InstallScanState {
    Disabled,
    Skipped,
    Clean,
    Threat,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct InstallScanResult {
    pub task_id: String,
    pub state: InstallScanState,
    pub files_scanned: u64,
    pub av_skipped: Option<AvSkipReason>,
    pub threats: Vec<String>,
    pub checked_at: i64,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "snake_case")]
pub enum AppSignatureIssue {
    Modified,
    Invalid,
    Unsigned,
    Adhoc,
    NotNotarized,
    TeamChanged,
    Unreadable,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct AppSignatureEntry {
    pub path: String,
    pub name: String,
    pub bundle_id: Option<String>,
    pub signature: Option<SignatureInfo>,
    pub previous_team_id: Option<String>,
    pub issues: Vec<AppSignatureIssue>,
    pub changed: bool,
    pub error_code: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct AppSignatureReport {
    pub checked_at: i64,
    pub apps: Vec<AppSignatureEntry>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct AppSignaturesChanged {
    pub checked_at: i64,
    pub apps: Vec<AppSignatureEntry>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "snake_case")]
pub enum PastePattern {
    PipeToInterpreter,
    DownloadExecute,
    EncodedCommand,
    RemoveQuarantine,
    TooLong,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct PasteBlocked {
    pub id: String,
    pub app_name: String,
    pub bundle_id: String,
    pub patterns: Vec<PastePattern>,
    /// Bounded in-memory preview, never persisted. Only sent to the main window.
    pub preview: String,
    pub expires_at: i64,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "snake_case")]
pub enum PasteAction {
    AllowOnce,
    Discard,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct PasteProtectionStatus {
    pub enabled: bool,
    pub accessibility_granted: bool,
    pub listening: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct VirusTotalStatus {
    pub enabled: bool,
    pub has_key: bool,
    pub retry_after_secs: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct VirusTotalVerdict {
    pub sha256: String,
    pub found: bool,
    pub malicious: u32,
    pub suspicious: u32,
    pub harmless: u32,
    pub undetected: u32,
    pub last_analysis_at: Option<i64>,
    pub permalink: String,
}
