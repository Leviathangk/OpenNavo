use crate::model::{DeleteMode, SkippedLocation};
use serde::{Deserialize, Serialize};

#[derive(
    Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq, Hash, PartialOrd, Ord,
)]
#[serde(rename_all = "snake_case")]
pub enum CleanupCategory {
    AppCaches,
    Logs,
    BrowserCaches,
    DevCaches,
    /// Caches, logs and abandoned staging of AI coding tools (Claude Code, Codex, Cursor, Gemini CLI).
    AiCaches,
    /// Session transcripts, memories and project history of AI coding tools: shown for information, never removed
    /// here. People delete these in the tool that owns them.
    AiHistory,
    Homebrew,
    Temp,
    Leftovers,
    Installers,
    ProjectArtifacts,
    Trash,
    DeviceBackups,
    /// Docker's own reclaimable space, removed only by Docker's prune commands.
    Docker,
    LargeFiles,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum CleanupRisk {
    Rebuildable,
    Review,
    Personal,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CleanupVolume {
    pub name: String,
    pub total_bytes: u64,
    pub free_bytes: u64,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CleanupReport {
    pub report_id: String,
    pub scanned_at: i64,
    pub expires_at: i64,
    pub volume: CleanupVolume,
    pub categories: Vec<CategoryReport>,
    pub incomplete: Vec<SkippedLocation>,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CategoryReport {
    pub key: CleanupCategory,
    pub risk: CleanupRisk,
    pub bytes: u64,
    pub complete: bool,
    pub groups: Vec<CleanupGroup>,
    pub skipped: Vec<SkippedGroup>,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CleanupGroup {
    pub id: String,
    pub title: String,
    pub subtitle: Option<String>,
    pub app_id: Option<String>,
    pub bundle_id: Option<String>,
    pub items: Vec<CleanupItem>,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CleanupItem {
    pub id: String,
    pub path: String,
    pub kind: CleanupItemKind,
    pub command: Option<String>,
    pub size_bytes: Option<u64>,
    /// False when the scan stopped measuring early (time or entry limit, unreadable folders): `size_bytes` is then
    /// only a lower bound, so the item is shown as "at least" and can never be selected or deleted.
    pub size_complete: bool,
    pub kept_open_files: u64,
    pub modified_at: Option<i64>,
    pub reason: CleanupReason,
    /// How the item comes back after removal: rebuilt automatically, rebuilt at a cost, or not at all.
    /// Shown next to the item and used by the Local MCP allowlist; selection defaults still come from
    /// `rules::default_selected`.
    pub restore: CleanupRestore,
    pub selected: bool,
    pub delete_mode: DeleteMode,
    pub needs_admin: bool,
    pub note: Option<String>,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum CleanupItemKind {
    File,
    Dir,
    Command,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum CleanupRestore {
    /// Rebuilt on the next build or launch, at negligible cost.
    Auto,
    /// Rebuilt, but the next build recompiles everything or re-downloads it: shown with that cost. Selection
    /// defaults come from `rules::default_selected`, or from the collector for commands that free space now.
    Costly,
    /// Gone for good once removed: user data and files nothing regenerates.
    Irreversible,
    /// Shown for information only. Nothing in this app removes it, and the executor refuses it even if a client asks.
    Never,
}
impl CleanupRestore {
    /// Restore cost follows what removing the item destroys, not the folder's name.
    pub fn for_reason(reason: CleanupReason) -> Self {
        match reason {
            CleanupReason::CacheDir
            | CleanupReason::LogFile
            | CleanupReason::BrowserCache
            | CleanupReason::ToolCache
            | CleanupReason::TempFile
            | CleanupReason::AiCache
            | CleanupReason::BuildLeaf => Self::Auto,
            CleanupReason::ToolDownloadCache
            | CleanupReason::DeviceSupport
            | CleanupReason::DeviceSupportLatest
            | CleanupReason::BuildArtifact
            | CleanupReason::BrewCleanup
            | CleanupReason::ToolCommand => Self::Costly,
            CleanupReason::Orphan
            | CleanupReason::OrphanService
            | CleanupReason::Installer
            | CleanupReason::InstallerInstalled
            | CleanupReason::Trash
            | CleanupReason::DeviceBackup
            | CleanupReason::XcodeArchive
            | CleanupReason::DockerImages
            | CleanupReason::DockerBuildCache
            | CleanupReason::DockerContainers
            | CleanupReason::LargeFile => Self::Irreversible,
            CleanupReason::AiHistory => Self::Never,
        }
    }
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum CleanupReason {
    CacheDir,
    LogFile,
    BrowserCache,
    ToolCache,
    /// Rebuildable, but the next build downloads it again (Gradle, Maven, Go modules, browsers for tests).
    ToolDownloadCache,
    DeviceSupport,
    DeviceSupportLatest,
    ToolCommand,
    BrewCleanup,
    TempFile,
    Orphan,
    OrphanService,
    Installer,
    InstallerInstalled,
    BuildArtifact,
    /// A generated leaf inside an active project (Rust incremental state, bundler caches): safe to remove without a
    /// full rebuild, unlike the whole artifact directory above.
    BuildLeaf,
    /// A cache, log or abandoned staging folder owned by an AI coding tool.
    AiCache,
    /// Session transcripts and project history of an AI coding tool, listed read-only.
    AiHistory,
    Trash,
    DeviceBackup,
    /// An Xcode archive: the shipped build and the dSYMs needed to symbolicate its crash logs.
    XcodeArchive,
    /// Images no container uses; pulled or built again when needed.
    DockerImages,
    DockerBuildCache,
    /// Stopped containers; their own changes are lost, volumes stay.
    DockerContainers,
    LargeFile,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct SkippedGroup {
    pub title: String,
    pub app_id: Option<String>,
    pub bytes: Option<u64>,
    pub reason: SkippedGroupReason,
    pub processes: Vec<String>,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum SkippedGroupReason {
    Running,
    InUse,
    Busy,
    Recent,
    GitTracked,
    Protected,
    Permission,
    /// The tool that owns the space is not running (Docker); open it and scan again.
    ToolStopped,
    /// Docker volumes: measured and shown, never offered; they can hold databases.
    Volumes,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CleanupProgress {
    pub request_id: String,
    pub category: CleanupCategory,
    pub location: Option<String>,
    pub checked: u64,
    pub total: u64,
    pub found_bytes: u64,
}
#[derive(Debug, Clone, Default, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CleanupSummary {
    pub scanned_at: Option<i64>,
    pub volume: Option<CleanupVolume>,
    pub reclaimable_bytes: u64,
    pub suggested_bytes: u64,
    pub active_background_items: u64,
    pub categories: Vec<CleanupCategoryBytes>,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CleanupCategoryBytes {
    pub key: CleanupCategory,
    pub bytes: u64,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ProtectRule {
    pub id: String,
    pub kind: ProtectKind,
    pub value: String,
    pub label: String,
    pub created_at: i64,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum ProtectKind {
    Path,
    BundleId,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(untagged, deny_unknown_fields)]
pub enum ProtectInput {
    #[serde(rename_all = "camelCase")]
    Item {
        report_id: String,
        item_id: String,
    },
    Manual {
        kind: ProtectKind,
        value: String,
    },
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum CleanupReminder {
    Off,
    Weekly,
    Monthly,
}
