//! Hermes Agent sidecar discovery, configuration, and one-shot chat execution.
//!
//! Release builds bundle the runtime under `Resources/resources/hermes/`; development
//! builds can point `HERMES_RUNTIME_BIN` at a prepared runtime or use the checked-in
//! resources directory after `pnpm --filter @opennavo/desktop hermes:prepare`.

use serde::{Deserialize, Serialize};
use std::ffi::OsString;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::sync::Mutex;
use std::sync::atomic::AtomicBool;
use std::time::Duration;
use tauri::{AppHandle, Emitter, Manager};
use tokio::io::{AsyncRead, AsyncReadExt};
use tokio::process::Command;
use tokio::sync::mpsc::UnboundedSender;
use tokio::time::timeout;
use yaml_rust2::{Yaml, YamlEmitter, YamlLoader, yaml::Hash};

use crate::AppError;

pub(crate) mod env_store;
pub(crate) mod gateway;
pub(crate) mod install;
pub(crate) mod legacy_migration;
pub(crate) mod login;
pub(crate) mod login_session;
pub mod panel;
pub(crate) mod permissions;
pub mod profiles;
pub(crate) mod test_connection;
use gateway::GatewayServer;

#[allow(dead_code)]
const CHAT_TIMEOUT: Duration = Duration::from_secs(180);
const VERSION_TIMEOUT: Duration = Duration::from_secs(15);
const DATA_ROOT_DIR: &str = "OpenNavoData";
const RUNTIME_DIR: &str = "runtime/hermes";
const HERMES_HOME_DIR: &str = "hermes";
const AUDIT_FILE: &str = "audit.jsonl";
const RUNTIME_MANIFEST_JSON: &str = include_str!("../../../hermes/runtime.json");

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct RuntimeManifest {
    hermes: RuntimeHermesPin,
    #[serde(default)]
    plugins: Vec<RuntimePluginPin>,
    release: RuntimeReleasePin,
}

#[derive(Debug, Deserialize)]
struct RuntimeHermesPin {
    version: String,
}

#[derive(Debug, Deserialize)]
struct RuntimePluginPin {
    name: String,
    repository: String,
    #[serde(rename = "ref")]
    sha: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct RuntimeReleasePin {
    repository: String,
    tag: String,
    asset: String,
    sha256: String,
}

/// App data root, cached so panel-session helpers work without an `AppHandle`.
static DATA_ROOT: std::sync::OnceLock<PathBuf> = std::sync::OnceLock::new();

#[derive(Debug, Clone, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesRuntimeStatus {
    pub ready: bool,
    pub installed: bool,
    pub version: Option<String>,
    pub expected_version: String,
    pub path: Option<String>,
    pub data_path: Option<String>,
    pub computer_use: bool,
    pub outdated: bool,
    pub message: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, Default)]
#[serde(default)]
pub struct HermesAuxAssignment {
    pub task: String,
    pub provider: String,
    pub model: String,
}

#[derive(Debug, Clone, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesChatInput {
    pub prompt: String,
    pub request_id: String,
    pub session_id: Option<String>,
    pub provider: String,
    pub model: String,
    pub effort: String,
    pub fast: bool,
    pub api_key: Option<String>,
    pub endpoint: Option<String>,
    pub cwd: Option<String>,
    pub attachments: Vec<String>,
    pub auxiliary: Vec<HermesAuxAssignment>,
    pub force_aux_vision: bool,
    pub gateway_url: Option<String>,
    pub profile_id: Option<String>,
    pub profile: Option<String>,
    pub panel_auth: bool,
    pub mac_enabled: bool,
    pub control_level: String,
    pub mac_terminal: bool,
    pub mac_screen: bool,
    pub mac_files: bool,
    pub mac_cron: bool,
    pub confirm_writes: bool,
}

#[derive(Debug, Clone, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesAuditEntry {
    pub at: String,
    pub provider: String,
    pub model: String,
    pub toolsets: String,
    pub control_level: String,
    pub prompt_chars: u64,
    pub outcome: String,
    pub session_id: Option<String>,
    pub detail: Option<String>,
}

#[derive(Debug, Clone, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesAttachmentInfo {
    pub path: String,
    pub name: String,
    pub size: u64,
    pub is_image: bool,
}

#[derive(Debug, Clone, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesRemoteTest {
    pub ok: bool,
    pub latency_ms: u64,
    pub models: u32,
    pub message: String,
}

#[derive(Debug, Clone, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesChatReply {
    pub text: String,
    pub session_id: Option<String>,
    pub live_session_id: Option<String>,
    pub usage: Option<HermesUsage>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, specta::Type)]
#[serde(rename_all = "snake_case")]
pub enum HermesSteerStatus {
    Queued,
    Redirected,
    Rejected,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesSteerResult {
    pub status: HermesSteerStatus,
    /// Only set to `false` when Hermes confirms no live agent saw the correction. A plain
    /// `rejected` may follow delivery (for example, a Codex steer whose ACK timed out), so it
    /// carries no `delivered` field and must never be re-sent as the next prompt.
    pub delivered: Option<bool>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, Default)]
#[serde(default)]
pub struct HermesUsage {
    pub input: Option<u64>,
    pub output: Option<u64>,
    pub total: Option<u64>,
    pub context_used: Option<u64>,
    pub context_max: Option<u64>,
    pub context_percent: Option<u8>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, Default)]
#[serde(default)]
pub struct HermesModelOptions {
    pub providers: Vec<HermesModelProvider>,
    pub model: Option<String>,
    pub provider: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, Default)]
#[serde(default)]
pub struct HermesModelProvider {
    pub slug: String,
    pub name: String,
    pub is_current: bool,
    pub is_user_defined: bool,
    pub models: Vec<String>,
    pub total_models: u32,
    pub source: Option<String>,
    pub authenticated: bool,
    pub auth_type: Option<String>,
    pub key_env: Option<String>,
    pub warning: Option<String>,
    pub base_url: Option<String>,
    pub capabilities: std::collections::BTreeMap<String, HermesModelCapability>,
    pub pricing: std::collections::BTreeMap<String, HermesModelPricing>,
    pub free_tier: Option<bool>,
    pub unavailable_models: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, Default)]
#[serde(default)]
pub struct HermesModelCapability {
    pub fast: bool,
    pub reasoning: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, Default)]
#[serde(default)]
pub struct HermesModelPricing {
    pub input: Option<String>,
    pub output: Option<String>,
    pub cache: Option<String>,
    pub free: Option<bool>,
}

#[derive(Debug, Clone, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesModelOptionsInput {
    pub provider: String,
    pub model: String,
    pub effort: String,
    pub fast: bool,
    pub api_key: Option<String>,
    pub endpoint: Option<String>,
    pub refresh: bool,
    pub include_unconfigured: bool,
    pub gateway_url: Option<String>,
    pub profile_id: Option<String>,
    pub profile: Option<String>,
    pub panel_auth: bool,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct HermesChunk {
    pub request_id: String,
    pub delta: String,
}

/// One answer the renderer gave to a server→client request (`approval`, `clarify`, `sudo`/`secret`).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(tag = "kind", rename_all = "camelCase")]
pub enum HermesAskReply {
    /// `scope` is one of the `choices` the runtime offered: once / session / always / deny.
    Approval { choice: String },
    /// `cancelled` answers "the user dismissed the card", which the runtime reads as cancel-all.
    Clarify {
        answers: Vec<HermesClarifyAnswer>,
        cancelled: bool,
    },
    /// One masked string (sudo password, env secret, vault unlock). `""` means skipped.
    Secret { value: String },
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesClarifyAnswer {
    pub qid: String,
    /// `None` is a skipped question (`clarify`'s `null` answer).
    pub answer: Option<String>,
}

/// A reply routed to the turn that is waiting on it, matched by the request id the card carried.
#[derive(Debug, Clone)]
pub(crate) struct AskAnswer {
    pub request_id: String,
    pub reply: HermesAskReply,
}

#[derive(Default)]
pub struct HermesRuntimeState {
    pid: Mutex<Option<u32>>,
    gateway: tokio::sync::Mutex<Option<std::sync::Arc<GatewayServer>>>,
    /// Answers from the renderer for the requests the running turn is parked on. One channel per turn:
    /// the id inside the answer picks the request, so a clarify and an approval can be open together.
    pub(crate) asks: tokio::sync::Mutex<Option<tokio::sync::mpsc::UnboundedSender<AskAnswer>>>,
    pub(crate) approval_cancel: tokio::sync::Notify,
    installing: AtomicBool,
    pub(crate) install_cancel: AtomicBool,
}

fn data_root(app: &AppHandle) -> Result<PathBuf, AppError> {
    let root = app
        .path()
        .app_data_dir()
        .map_err(|error| {
            AppError::new("E_HERMES_HOME", "hermes_home").with_detail(error.to_string())
        })?
        .join(DATA_ROOT_DIR);
    std::fs::create_dir_all(&root)?;
    let _ = DATA_ROOT.set(root.clone());
    Ok(root)
}

fn runtime_root(app: &AppHandle) -> Result<PathBuf, AppError> {
    Ok(data_root(app)?.join(RUNTIME_DIR))
}

pub(crate) fn runtime_bin(app: &AppHandle) -> Option<PathBuf> {
    if let Ok(path) = std::env::var("HERMES_RUNTIME_BIN") {
        let path = PathBuf::from(path);
        if path.is_file() {
            return Some(path);
        }
    }
    let installed = runtime_root(app).ok()?.join("bin").join("hermes");
    installed.is_file().then_some(installed)
}

pub(crate) fn hermes_home(app: &AppHandle) -> Result<PathBuf, AppError> {
    let home = data_root(app)?.join(HERMES_HOME_DIR);
    // Consolidate the pre-OpenNavoData home from earlier builds.
    if !home.exists()
        && let Ok(app_data) = app.path().app_data_dir()
    {
        let legacy = app_data.join(HERMES_HOME_DIR);
        if legacy.is_dir() {
            if let Some(parent) = home.parent() {
                std::fs::create_dir_all(parent)?;
            }
            let _ = std::fs::rename(legacy, &home);
        }
    }
    std::fs::create_dir_all(&home)?;
    Ok(home)
}

fn clean_output(value: &str) -> String {
    let ansi = regex::Regex::new(r"\x1b\[[0-9;]*m").expect("static ANSI regex");
    ansi.replace_all(value, "").trim().to_owned()
}

#[allow(dead_code)]
fn clean_response(value: &str) -> String {
    clean_output(value)
        .lines()
        .filter(|line| !line.contains("tirith security scanner enabled but not available"))
        .collect::<Vec<_>>()
        .join("\n")
        .trim()
        .to_owned()
}

#[allow(dead_code)]
fn session_id(stderr: &str) -> Option<String> {
    stderr
        .lines()
        .find_map(|line| {
            line.trim()
                .strip_prefix("session_id:")
                .map(|value| value.trim().to_owned())
        })
        .filter(|value| !value.is_empty())
}

fn provider_id(provider: &str) -> &str {
    match provider {
        "openai" => "openai-api",
        "anthropic" => "anthropic",
        "google" => "gemini",
        "openrouter" => "openrouter",
        "nous" => "nous",
        "codex" => "openai-codex",
        "claude-code" => "anthropic",
        "copilot" => "copilot",
        "opencode" => "opencode-zen",
        "opencode-go" => "opencode-go",
        "custom" | "local" => "openavoo-custom",
        other => other,
    }
}

fn provider_key_env(provider: &str) -> &str {
    match provider {
        "openai" | "codex" => "OPENAI_API_KEY",
        "anthropic" | "claude-code" => "ANTHROPIC_API_KEY",
        "google" => "GOOGLE_API_KEY",
        "openrouter" => "OPENROUTER_API_KEY",
        "nous" => "NOUS_API_KEY",
        "copilot" => "GITHUB_TOKEN",
        "opencode" => "OPENCODE_ZEN_API_KEY",
        "opencode-go" => "OPENCODE_GO_API_KEY",
        "custom" | "local" => "OPENNAVO_CUSTOM_API_KEY",
        _ => "OPENAI_API_KEY",
    }
}

fn toolsets(input: &HermesChatInput) -> String {
    if !input.mac_enabled || input.control_level == "open" {
        return "safe".to_owned();
    }
    let mut selected = Vec::new();
    if input.mac_terminal {
        selected.push("terminal");
    }
    if input.mac_files {
        selected.push("file");
    }
    if input.mac_screen {
        selected.push("computer_use");
    }
    if input.mac_cron {
        selected.push("cronjob");
    }
    if selected.is_empty() {
        "safe".to_owned()
    } else {
        selected.join(",")
    }
}

fn runtime_manifest() -> Result<RuntimeManifest, AppError> {
    serde_json::from_str(RUNTIME_MANIFEST_JSON).map_err(|error| {
        AppError::new(
            "E_HERMES_RUNTIME_MANIFEST",
            "The pinned Hermes runtime metadata is invalid.",
        )
        .with_detail(error.to_string())
    })
}

fn expected_runtime_version() -> Result<String, AppError> {
    let version = runtime_manifest()?.hermes.version;
    let valid = regex::Regex::new(r"^\d+\.\d+\.\d+$")
        .expect("static Hermes version regex")
        .is_match(&version);
    if !valid {
        return Err(AppError::new(
            "E_HERMES_RUNTIME_MANIFEST",
            "The pinned Hermes runtime version is invalid.",
        ));
    }
    Ok(version)
}

fn runtime_version_from_output(value: &str) -> Option<String> {
    let pattern = regex::Regex::new(r"(?m)^Hermes Agent v?(\d+\.\d+\.\d+)\b")
        .expect("static Hermes version output regex");
    pattern
        .captures(value)
        .and_then(|captures| captures.get(1))
        .map(|value| value.as_str().to_owned())
}

#[cfg(test)]
fn runtime_version_matches(version: &str, expected: &str) -> bool {
    runtime_version_from_output(version).as_deref() == Some(expected)
}

fn reasoning_effort(effort: &str) -> Option<&str> {
    match effort {
        "none" | "minimal" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra" => Some(effort),
        _ => None,
    }
}

fn history_path(app: &AppHandle, profile: Option<&str>) -> Result<PathBuf, AppError> {
    let home = hermes_home(app)?;
    let profile = profile
        .map(str::trim)
        .filter(|name| !name.is_empty() && *name != "default");
    Ok(match profile {
        Some(name)
            if name
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_') =>
        {
            home.join(format!("history-{name}.json"))
        }
        Some(_) => {
            return Err(AppError::new(
                "E_HERMES_HISTORY",
                "Invalid profile name for history.",
            ));
        }
        None => home.join("history.json"),
    })
}

pub fn history_load(app: &AppHandle, profile: Option<&str>) -> Result<Option<String>, AppError> {
    let path = history_path(app, profile)?;
    if !path.is_file() {
        return Ok(None);
    }
    let raw = std::fs::read_to_string(&path)?;
    if raw.len() > 20 * 1024 * 1024 {
        return Err(AppError::new(
            "E_HERMES_HISTORY",
            "Hermes history is too large.",
        ));
    }
    Ok(Some(raw))
}

pub fn history_save(app: &AppHandle, json: &str, profile: Option<&str>) -> Result<(), AppError> {
    if json.len() > 20 * 1024 * 1024 {
        return Err(AppError::new(
            "E_HERMES_HISTORY",
            "Hermes history is too large.",
        ));
    }
    serde_json::from_str::<serde_json::Value>(json).map_err(|error| {
        AppError::new("E_HERMES_HISTORY", "Hermes history is not valid JSON.")
            .with_detail(error.to_string())
    })?;
    let path = history_path(app, profile)?;
    let tmp = path.with_extension("json.tmp");
    std::fs::write(&tmp, json)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let _ = std::fs::set_permissions(&tmp, std::fs::Permissions::from_mode(0o600));
    }
    std::fs::rename(&tmp, &path)?;
    Ok(())
}

pub fn attachment_preview(path: &str) -> Result<Option<String>, AppError> {
    let path_ref = std::path::Path::new(path);
    let ext = path_ref
        .extension()
        .and_then(|value| value.to_str())
        .map(|value| value.to_ascii_lowercase())
        .unwrap_or_default();
    let mime = match ext.as_str() {
        "png" => "image/png",
        "jpg" | "jpeg" => "image/jpeg",
        "gif" => "image/gif",
        "webp" => "image/webp",
        "heic" | "heif" => "image/heic",
        "bmp" => "image/bmp",
        "tiff" => "image/tiff",
        _ => return Ok(None),
    };
    let metadata = match std::fs::metadata(path_ref) {
        Ok(metadata) => metadata,
        Err(_) => return Ok(None),
    };
    if metadata.len() > 8 * 1024 * 1024 {
        return Ok(None);
    }
    let bytes = std::fs::read(path_ref)?;
    use base64::Engine;
    Ok(Some(format!(
        "data:{mime};base64,{}",
        base64::engine::general_purpose::STANDARD.encode(bytes)
    )))
}

pub fn clipboard_image(app: &AppHandle) -> Result<Option<String>, AppError> {
    let mut clipboard = arboard::Clipboard::new().map_err(|error| {
        AppError::new("E_HERMES_CLIPBOARD", "Could not open the clipboard.")
            .with_detail(error.to_string())
    })?;
    let image = match clipboard.get_image() {
        Ok(image) => image,
        Err(_) => return Ok(None),
    };
    let dir = data_root(app)?.join("clipboard");
    std::fs::create_dir_all(&dir)?;
    if let Ok(entries) = std::fs::read_dir(&dir) {
        let cutoff = std::time::SystemTime::now() - std::time::Duration::from_secs(86_400);
        for entry in entries.flatten() {
            if entry
                .metadata()
                .and_then(|meta| meta.modified())
                .map(|modified| modified < cutoff)
                .unwrap_or(false)
            {
                let _ = std::fs::remove_file(entry.path());
            }
        }
    }
    let path = dir.join(format!("clipboard-{}.png", uuid::Uuid::new_v4()));
    image::save_buffer(
        &path,
        &image.bytes,
        image.width as u32,
        image.height as u32,
        image::ColorType::Rgba8,
    )
    .map_err(|error| {
        AppError::new("E_HERMES_CLIPBOARD", "Could not save the clipboard image.")
            .with_detail(error.to_string())
    })?;
    Ok(Some(path.display().to_string()))
}

pub fn attachment_info(paths: Vec<String>) -> Vec<HermesAttachmentInfo> {
    paths
        .into_iter()
        .map(|path| {
            let name = std::path::Path::new(&path)
                .file_name()
                .map(|value| value.to_string_lossy().into_owned())
                .unwrap_or_else(|| path.clone());
            let size = std::fs::metadata(&path).map(|meta| meta.len()).unwrap_or(0);
            let is_image = std::path::Path::new(&path)
                .extension()
                .and_then(|ext| ext.to_str())
                .map(|ext| {
                    matches!(
                        ext.to_ascii_lowercase().as_str(),
                        "png" | "jpg" | "jpeg" | "gif" | "webp" | "heic" | "heif" | "bmp" | "tiff"
                    )
                })
                .unwrap_or(false);
            HermesAttachmentInfo {
                path,
                name,
                size,
                is_image,
            }
        })
        .collect()
}

fn append_audit(
    home: &Path,
    input: &HermesChatInput,
    outcome: &str,
    session: Option<&str>,
    detail: Option<&str>,
) {
    let entry = serde_json::json!({
        "at": chrono::Utc::now().to_rfc3339(),
        "provider": input.provider,
        "model": input.model,
        "effort": input.effort,
        "toolsets": toolsets(input),
        "controlLevel": input.control_level,
        "promptChars": input.prompt.chars().count(),
        "outcome": outcome,
        "sessionId": session,
        "detail": detail
    });
    if let Ok(mut file) = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(home.join(AUDIT_FILE))
    {
        let _ = writeln!(file, "{entry}");
    }
}

fn audit_path(app: &AppHandle) -> Result<PathBuf, AppError> {
    Ok(hermes_home(app)?.join(AUDIT_FILE))
}

pub async fn logout(
    app: &AppHandle,
    provider: &str,
    profile: Option<&str>,
) -> Result<(), AppError> {
    let Some(bin) = runtime_bin(app) else {
        return Err(AppError::new(
            "E_HERMES_RUNTIME",
            "Hermes runtime is not installed.",
        ));
    };
    // Log out of the profile whose `auth.json` actually holds the login.
    let home = profiles::resolve_home(&hermes_home(app)?, profile)?;
    let hermes_provider = provider_id(provider);
    let mut command = runtime_command(&bin)?;
    profiles::pin_default_profile(&mut command, profile);
    command
        .arg("auth")
        .arg("logout")
        .arg(hermes_provider)
        .env("HERMES_HOME", &home)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped());
    let output = timeout(VERSION_TIMEOUT, command.output())
        .await
        .map_err(|_| AppError::new("E_HERMES_TIMEOUT", "Hermes logout timed out."))?
        .map_err(|error| {
            AppError::new("E_HERMES_RUNTIME", "Hermes logout could not start.")
                .with_detail(error.to_string())
        })?;
    if !output.status.success() {
        let detail = String::from_utf8_lossy(&output.stderr)
            .chars()
            .take(300)
            .collect::<String>();
        return Err(AppError::new("E_HERMES_RUNTIME", "Hermes logout failed.").with_detail(detail));
    }
    Ok(())
}

pub async fn mcp_action(
    app: &AppHandle,
    action: &str,
    name: &str,
    url: &str,
    profile: Option<&str>,
) -> Result<String, AppError> {
    let name = name.trim();
    if !matches!(action, "list" | "add" | "remove") {
        return Err(AppError::new("E_HERMES_INPUT", "Unknown MCP action."));
    }
    if action != "list" && (name.is_empty() || name.starts_with('-') || name.len() > 128) {
        return Err(AppError::new(
            "E_HERMES_INPUT",
            "A valid MCP server name is required.",
        ));
    }
    let Some(bin) = runtime_bin(app) else {
        return Err(AppError::new(
            "E_HERMES_RUNTIME",
            "Hermes runtime is not installed.",
        ));
    };
    let home = profiles::resolve_home(&hermes_home(app)?, profile)?;
    let mut command = runtime_command(&bin)?;
    profiles::pin_default_profile(&mut command, profile);
    command.arg("mcp");
    match action {
        "list" => {
            command.arg("list");
        }
        "remove" => {
            command.arg("remove").arg(name);
        }
        "add" => {
            let url = url.trim();
            if !(url.starts_with("https://") || url.starts_with("http://")) {
                return Err(AppError::new(
                    "E_HERMES_INPUT",
                    "MCP URL must start with http:// or https://.",
                ));
            }
            command.arg("add").arg(name).arg("--url").arg(url);
        }
        _ => {}
    }
    command
        .env("HERMES_HOME", home)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped());
    let output = timeout(VERSION_TIMEOUT, command.output())
        .await
        .map_err(|_| AppError::new("E_HERMES_TIMEOUT", "Hermes MCP action timed out."))?
        .map_err(|error| {
            AppError::new("E_HERMES_RUNTIME", "Hermes MCP action could not start.")
                .with_detail(error.to_string())
        })?;
    if !output.status.success() {
        let detail = String::from_utf8_lossy(&output.stderr)
            .chars()
            .take(400)
            .collect::<String>();
        return Err(
            AppError::new("E_HERMES_RUNTIME", "Hermes MCP action failed.").with_detail(detail),
        );
    }
    Ok(String::from_utf8_lossy(&output.stdout).into_owned())
}

pub async fn skill_action(
    app: &AppHandle,
    action: &str,
    identifier: &str,
    profile: Option<&str>,
) -> Result<String, AppError> {
    let action = match action {
        "install" | "uninstall" => action,
        _ => return Err(AppError::new("E_HERMES_INPUT", "Unknown skill action.")),
    };
    let identifier = identifier.trim();
    if identifier.is_empty() || identifier.starts_with('-') || identifier.len() > 512 {
        return Err(AppError::new(
            "E_HERMES_INPUT",
            "A valid skill identifier is required.",
        ));
    }
    let Some(bin) = runtime_bin(app) else {
        return Err(AppError::new(
            "E_HERMES_RUNTIME",
            "Hermes runtime is not installed.",
        ));
    };
    let home = profiles::resolve_home(&hermes_home(app)?, profile)?;
    let mut command = runtime_command(&bin)?;
    profiles::pin_default_profile(&mut command, profile);
    command
        .arg("skills")
        .arg(action)
        .arg(identifier)
        .arg("--yes")
        .env("HERMES_HOME", home)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped());
    let output = timeout(VERSION_TIMEOUT, command.output())
        .await
        .map_err(|_| AppError::new("E_HERMES_TIMEOUT", "Hermes skill action timed out."))?
        .map_err(|error| {
            AppError::new("E_HERMES_RUNTIME", "Hermes skill action could not start.")
                .with_detail(error.to_string())
        })?;
    if !output.status.success() {
        let detail = String::from_utf8_lossy(&output.stderr)
            .chars()
            .take(400)
            .collect::<String>();
        return Err(
            AppError::new("E_HERMES_RUNTIME", "Hermes skill action failed.").with_detail(detail),
        );
    }
    Ok(String::from_utf8_lossy(&output.stdout).into_owned())
}

pub async fn tool_action(
    app: &AppHandle,
    action: &str,
    name: &str,
    profile: Option<&str>,
) -> Result<String, AppError> {
    let action = match action {
        "enable" | "disable" => action,
        _ => return Err(AppError::new("E_HERMES_INPUT", "Unknown tool action.")),
    };
    let name = name.trim();
    if name.is_empty() || name.starts_with('-') || name.len() > 512 {
        return Err(AppError::new(
            "E_HERMES_INPUT",
            "A valid toolset or MCP tool name is required.",
        ));
    }
    let Some(bin) = runtime_bin(app) else {
        return Err(AppError::new(
            "E_HERMES_RUNTIME",
            "Hermes runtime is not installed.",
        ));
    };
    let home = profiles::resolve_home(&hermes_home(app)?, profile)?;
    let mut command = runtime_command(&bin)?;
    profiles::pin_default_profile(&mut command, profile);
    command
        .arg("tools")
        .arg(action)
        .arg(name)
        .env("HERMES_HOME", home)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped());
    let output = timeout(VERSION_TIMEOUT, command.output())
        .await
        .map_err(|_| AppError::new("E_HERMES_TIMEOUT", "Hermes tool action timed out."))?
        .map_err(|error| {
            AppError::new("E_HERMES_RUNTIME", "Hermes tool action could not start.")
                .with_detail(error.to_string())
        })?;
    if !output.status.success() {
        let detail = String::from_utf8_lossy(&output.stderr)
            .chars()
            .take(400)
            .collect::<String>();
        return Err(
            AppError::new("E_HERMES_RUNTIME", "Hermes tool action failed.").with_detail(detail),
        );
    }
    Ok(String::from_utf8_lossy(&output.stdout).into_owned())
}

pub async fn cron_action(
    app: &AppHandle,
    action: &str,
    job_id: &str,
    profile: Option<&str>,
) -> Result<String, AppError> {
    let action = match action {
        "pause" | "resume" | "remove" => action,
        _ => return Err(AppError::new("E_HERMES_INPUT", "Unknown cron action.")),
    };
    let job_id = job_id.trim();
    if job_id.is_empty() || job_id.starts_with('-') {
        return Err(AppError::new(
            "E_HERMES_INPUT",
            "A valid cron job id is required.",
        ));
    }
    let Some(bin) = runtime_bin(app) else {
        return Err(AppError::new(
            "E_HERMES_RUNTIME",
            "Hermes runtime is not installed.",
        ));
    };
    let home = profiles::resolve_home(&hermes_home(app)?, profile)?;
    let mut command = runtime_command(&bin)?;
    profiles::pin_default_profile(&mut command, profile);
    command
        .arg("cron")
        .arg(action)
        .arg(job_id)
        .env("HERMES_HOME", home)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped());
    let output = timeout(VERSION_TIMEOUT, command.output())
        .await
        .map_err(|_| AppError::new("E_HERMES_TIMEOUT", "Hermes cron action timed out."))?
        .map_err(|error| {
            AppError::new("E_HERMES_RUNTIME", "Hermes cron action could not start.")
                .with_detail(error.to_string())
        })?;
    if !output.status.success() {
        let detail = String::from_utf8_lossy(&output.stderr)
            .chars()
            .take(400)
            .collect::<String>();
        return Err(
            AppError::new("E_HERMES_RUNTIME", "Hermes cron action failed.").with_detail(detail),
        );
    }
    Ok(String::from_utf8_lossy(&output.stdout).into_owned())
}

pub async fn cli_read(
    app: &AppHandle,
    kind: &str,
    profile: Option<&str>,
) -> Result<String, AppError> {
    let Some(bin) = runtime_bin(app) else {
        return Err(AppError::new(
            "E_HERMES_RUNTIME",
            "Hermes runtime is not installed.",
        ));
    };
    let home = profiles::resolve_home(&hermes_home(app)?, profile)?;
    let args: &[&str] = match kind {
        "tools" => &["tools", "list"],
        "skills" => &["skills", "list"],
        "cron" => &["cron", "list", "--all"],
        _ => {
            return Err(AppError::new(
                "E_HERMES_INPUT",
                "Unknown Hermes diagnostics section.",
            ));
        }
    };
    let mut command = runtime_command(&bin)?;
    profiles::pin_default_profile(&mut command, profile);
    command
        .args(args)
        .env("HERMES_HOME", home)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped());
    let output = timeout(VERSION_TIMEOUT, command.output())
        .await
        .map_err(|_| AppError::new("E_HERMES_TIMEOUT", "Hermes diagnostics timed out."))?
        .map_err(|error| {
            AppError::new("E_HERMES_RUNTIME", "Hermes diagnostics could not start.")
                .with_detail(error.to_string())
        })?;
    if !output.status.success() {
        let detail = String::from_utf8_lossy(&output.stderr)
            .chars()
            .take(400)
            .collect::<String>();
        return Err(
            AppError::new("E_HERMES_RUNTIME", "Hermes diagnostics failed.").with_detail(detail),
        );
    }
    Ok(String::from_utf8_lossy(&output.stdout).into_owned())
}

pub fn audit_entries(app: &AppHandle, limit: usize) -> Result<Vec<HermesAuditEntry>, AppError> {
    let path = audit_path(app)?;
    let Ok(raw) = std::fs::read_to_string(path) else {
        return Ok(Vec::new());
    };
    let mut entries = raw
        .lines()
        .filter_map(|line| serde_json::from_str::<serde_json::Value>(line).ok())
        .map(|value| HermesAuditEntry {
            at: value
                .get("at")
                .and_then(|v| v.as_str())
                .unwrap_or_default()
                .to_owned(),
            provider: value
                .get("provider")
                .and_then(|v| v.as_str())
                .unwrap_or_default()
                .to_owned(),
            model: value
                .get("model")
                .and_then(|v| v.as_str())
                .unwrap_or_default()
                .to_owned(),
            toolsets: value
                .get("toolsets")
                .and_then(|v| v.as_str())
                .unwrap_or_default()
                .to_owned(),
            control_level: value
                .get("controlLevel")
                .and_then(|v| v.as_str())
                .unwrap_or_default()
                .to_owned(),
            prompt_chars: value
                .get("promptChars")
                .and_then(|v| v.as_u64())
                .unwrap_or(0),
            outcome: value
                .get("outcome")
                .and_then(|v| v.as_str())
                .unwrap_or_default()
                .to_owned(),
            session_id: value
                .get("sessionId")
                .and_then(|v| v.as_str())
                .map(str::to_owned),
            detail: value
                .get("detail")
                .and_then(|v| v.as_str())
                .map(str::to_owned),
        })
        .collect::<Vec<_>>();
    if entries.len() > limit {
        entries = entries.split_off(entries.len() - limit);
    }
    entries.reverse();
    Ok(entries)
}

pub fn open_logs(app: &AppHandle) -> Result<(), AppError> {
    let path = hermes_home(app)?.join("logs");
    if !path.is_dir() {
        return Err(AppError::new("E_HERMES_LOGS", "No Hermes logs exist yet."));
    }
    std::process::Command::new("open")
        .arg(&path)
        .spawn()
        .map_err(|error| {
            AppError::new("E_HERMES_LOGS", "Could not open the Hermes logs.")
                .with_detail(error.to_string())
        })?;
    Ok(())
}

pub fn open_audit(app: &AppHandle) -> Result<(), AppError> {
    let path = audit_path(app)?;
    if !path.is_file() {
        return Err(AppError::new(
            "E_HERMES_AUDIT",
            "No Hermes audit log exists yet.",
        ));
    }
    std::process::Command::new("open")
        .arg(&path)
        .spawn()
        .map_err(|error| {
            AppError::new("E_HERMES_AUDIT", "Could not open the audit log.")
                .with_detail(error.to_string())
        })?;
    Ok(())
}

fn runtime_python(bin: &Path) -> Option<PathBuf> {
    let root = bin.parent()?.parent()?;
    let link = root.join("python").join("bin").join("python3");
    if link.is_file() {
        return Some(link);
    }
    let mut found: Vec<PathBuf> = std::fs::read_dir(root.join("python").join("bin"))
        .ok()?
        .flatten()
        .map(|entry| entry.path())
        .filter(|path| {
            path.file_name()
                .and_then(|name| name.to_str())
                .and_then(|name| name.strip_prefix("python3."))
                .is_some_and(|rest| !rest.is_empty() && rest.chars().all(|ch| ch.is_ascii_digit()))
        })
        .collect();
    found.sort();
    found.pop()
}

/// Entry point the generated runtime wrapper (`bin/hermes`) also uses.
const HERMES_ENTRYPOINT: &str = "from hermes_cli.main import main; main()";

/// Host variables the runtime needs. Everything else is dropped before spawning so a key that
/// happened to be present when the app was launched from a shell cannot reach terminal/screen
/// tools. Provider keys are loaded from `<HERMES_HOME>/.env` by the runtime itself.
const RUNTIME_ENV_ALLOWLIST: &[&str] = &[
    "PATH",
    "HOME",
    "USER",
    "LOGNAME",
    "SHELL",
    "TMPDIR",
    "TMP",
    "TEMP",
    "LANG",
    "LC_ALL",
    "LC_CTYPE",
    "LC_MESSAGES",
    "SSL_CERT_FILE",
    "SSL_CERT_DIR",
    "REQUESTS_CA_BUNDLE",
    "CURL_CA_BUNDLE",
    "NO_PROXY",
    "no_proxy",
    "SYSTEMROOT",
    "WINDIR",
    "COMSPEC",
    "PATHEXT",
    "PYTHONIOENCODING",
    "PYTHONUTF8",
    "TERM",
];

fn inherited_runtime_env() -> Vec<(&'static str, OsString)> {
    RUNTIME_ENV_ALLOWLIST
        .iter()
        .filter_map(|name| std::env::var_os(name).map(|value| (*name, value)))
        .collect()
}

fn apply_runtime_environment(command: &mut Command) {
    command.env_clear().envs(inherited_runtime_env());
}

/// Build the runtime process directly instead of going through `bin/hermes` so the
/// interpreter is discovered rather than hard-coded by the generated wrapper. The
/// layout matches that wrapper (`<root>/python/bin/python3`, `<root>/site`).
///
/// No credential is injected here: provider keys live in `<HERMES_HOME>/.env`, which
/// the runtime loads itself (see [`env_store`]).
fn runtime_command(bin: &Path) -> Result<Command, AppError> {
    let python = runtime_python(bin).ok_or_else(|| {
        AppError::new(
            "E_HERMES_RUNTIME",
            "The Hermes runtime is incomplete. Reinstall it from the assistant.",
        )
    })?;
    let root = bin
        .parent()
        .and_then(Path::parent)
        .ok_or_else(|| AppError::new("E_HERMES_RUNTIME", "The Hermes runtime path is invalid."))?;
    let mut pythonpath = root.join("site").display().to_string();
    if let Some(existing) = std::env::var_os("PYTHONPATH")
        && !existing.is_empty()
    {
        pythonpath.push(':');
        pythonpath.push_str(&existing.to_string_lossy());
    }
    let mut command = Command::new(python);
    apply_runtime_environment(&mut command);
    command
        .arg("-c")
        .arg(HERMES_ENTRYPOINT)
        .env("PYTHONPATH", pythonpath);
    Ok(command)
}

/// Resolve the provider key the runtime will authenticate with. Used for the gateway
/// fingerprint and for the custom-endpoint `key_env` decision.
///
/// The key is never injected into the child process: it lives in `<HERMES_HOME>/.env`, which the
/// runtime loads itself (see [`env_store`]). Inherited shell variables are deliberately ignored:
/// the runtime is launched with those credentials scrubbed, so counting one here would claim a
/// key exists while the runtime cannot read it.
fn resolve_provider_secret(home: &Path, input: &HermesChatInput) -> Option<String> {
    if let Some(key) = env_store::env_value(home, provider_key_env(&input.provider)) {
        return Some(key);
    }
    if matches!(input.provider.as_str(), "opencode" | "opencode-go")
        && let Some(key) = local_opencode_key(&input.provider)
    {
        return Some(key);
    }
    None
}

/// Which part of `config.yaml` a write owns.
///
/// A chat turn applies the whole desktop projection: the model, the agent/display/terminal
/// switches and the auxiliary slots the GUI shows. Reading the model catalog must not. The
/// settings sheet asks for a profile's catalog just by selecting it, and the draft it passes
/// is that profile's *saved* state — or the built-in default when this window never opened the
/// profile — so writing it back replaced the profile's own model with the default one. A
/// catalog read therefore only maintains the user-defined provider block it needs in order to
/// discover models from a custom endpoint.
#[derive(Clone, Copy, PartialEq, Eq)]
enum ConfigScope {
    Turn,
    Catalog,
}

/// Write the runtime's `config.yaml`.
///
/// A key supplied with the request is persisted to `<HERMES_HOME>/.env`; otherwise the
/// value already in `.env` is used as-is. That is the same store the official CLI and
/// dashboard read and write, so both see one set of credentials.
///
/// The document is merged, not replaced: keys this app does not own (tools, skills, browser,
/// cron, other providers, anything the user added by hand) survive the write. Only the keys
/// listed in [`ConfigScope`] are removed and rewritten. The runtime's own `save_config` rewrites
/// the file the same way, so YAML comments in it were never durable.
///
/// Returns the key the runtime will authenticate with, so the caller can fingerprint it.
fn write_config(home: &Path, input: &HermesChatInput) -> Result<Option<String>, AppError> {
    write_config_scoped(home, input, ConfigScope::Turn)
}

/// Catalog-only write; see [`ConfigScope`].
fn write_catalog_config(home: &Path, input: &HermesChatInput) -> Result<Option<String>, AppError> {
    write_config_scoped(home, input, ConfigScope::Catalog)
}

fn write_config_scoped(
    home: &Path,
    input: &HermesChatInput,
    scope: ConfigScope,
) -> Result<Option<String>, AppError> {
    // Read before anything is written: saving a key creates `.env`, which is itself enough for the
    // runtime to accept `--profile <name>`.
    let was_live = profiles::has_identity(home);
    if let Some(key) = input
        .api_key
        .as_deref()
        .map(str::trim)
        .filter(|key| !key.is_empty())
    {
        env_store::set_env_value(home, provider_key_env(&input.provider), key)?;
    }
    let api_key = resolve_provider_secret(home, input);
    let config_path = home.join("config.yaml");
    let existing = std::fs::read_to_string(&config_path).ok();
    let mut doc = config_doc(existing.as_deref());
    let before = doc.clone();
    // A catalog read of a profile directory that holds none of the markers the runtime uses to
    // recognise a profile would leave `--profile <name>` unresolvable, so that case still gets
    // the full projection. A profile that merely lost its `config.yaml` stays live through its
    // `.env` or `auth.json` and keeps the runtime's own defaults.
    let bootstrap = scope == ConfigScope::Catalog && !was_live;
    if scope == ConfigScope::Turn || bootstrap {
        apply_turn_config(&mut doc, input);
    }
    apply_custom_provider(&mut doc, input, api_key.is_some());
    // Nothing this write owns changed, so leave the file byte-for-byte alone. That keeps a
    // catalog read from reformatting a profile's hand-written `config.yaml`.
    if doc == before {
        return Ok(api_key);
    }
    let body = render_config(&doc)?;
    if existing.as_deref() == Some(body.as_str()) {
        return Ok(api_key);
    }
    // A named profile's home may not exist yet; creating it here is what makes the directory a
    // live `--profile` target for the gateway.
    env_store::ensure_dir(home)?;
    std::fs::write(&config_path, body)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let _ = std::fs::set_permissions(&config_path, std::fs::Permissions::from_mode(0o600));
    }
    Ok(api_key)
}

fn endpoint_of(input: &HermesChatInput) -> Option<&str> {
    input
        .endpoint
        .as_deref()
        .map(str::trim)
        .filter(|value| !value.is_empty())
}

/// Apply the keys a chat turn owns.
fn apply_turn_config(doc: &mut Yaml, input: &HermesChatInput) {
    remove_path(doc, &["agent", "image_input_mode"]);
    if input.force_aux_vision {
        set_path(
            doc,
            &["agent", "image_input_mode"],
            Yaml::String("text".into()),
        );
    }
    set_path(
        doc,
        &["agent", "reasoning_effort"],
        Yaml::String(reasoning_effort(&input.effort).unwrap_or("").to_owned()),
    );
    set_path(doc, &["display", "show_reasoning"], Yaml::Boolean(false));
    set_path(doc, &["display", "streaming"], Yaml::Boolean(true));
    // No credential is written to this file: keys stay in `.env`, and `env_passthrough`
    // stays empty so no tool can opt a credential into its own child environment.
    set_path(
        doc,
        &["terminal", "env_passthrough"],
        Yaml::Array(Vec::new()),
    );
    set_path(
        doc,
        &["terminal", "client_env_passthrough"],
        Yaml::Array(Vec::new()),
    );
    set_path(
        doc,
        &["model", "provider"],
        Yaml::String(provider_id(&input.provider).to_owned()),
    );
    set_path(
        doc,
        &["model", "default"],
        Yaml::String(input.model.clone()),
    );
    remove_path(doc, &["model", "base_url"]);
    if let Some(endpoint) = endpoint_of(input) {
        set_path(
            doc,
            &["model", "base_url"],
            Yaml::String(endpoint.to_owned()),
        );
    }
    // Every auxiliary slot the request mentions belongs to the desktop store, so a slot the
    // user put back on "auto" has to disappear instead of keeping its previous override.
    // Slots this build does not know about are left alone.
    for entry in &input.auxiliary {
        remove_path(doc, &["auxiliary", entry.task.as_str()]);
    }
    for entry in input
        .auxiliary
        .iter()
        .filter(|entry| entry.provider != "auto" && !entry.model.is_empty())
    {
        set_path(
            doc,
            &["auxiliary", entry.task.as_str(), "provider"],
            Yaml::String(entry.provider.clone()),
        );
        set_path(
            doc,
            &["auxiliary", entry.task.as_str(), "model"],
            Yaml::String(entry.model.clone()),
        );
    }
}

/// Apply the user-defined provider block the custom and local cards are served by.
fn apply_custom_provider(doc: &mut Yaml, input: &HermesChatInput, has_key: bool) {
    remove_path(doc, &["providers", "openavoo-custom"]);
    if !matches!(input.provider.as_str(), "custom" | "local") {
        return;
    }
    // `local` means an Ollama-style server on this Mac, so an empty URL has an obvious
    // default. `custom` means "some OpenAI-compatible endpoint the user names" —
    // defaulting it to 127.0.0.1:11434 would silently probe Ollama and hide the fact
    // that no endpoint is configured, so that block is simply omitted until a URL exists.
    let endpoint = endpoint_of(input)
        .or_else(|| (input.provider == "local").then_some("http://127.0.0.1:11434/v1"));
    let Some(endpoint) = endpoint else { return };
    set_path(
        doc,
        &["providers", "openavoo-custom", "api"],
        Yaml::String(endpoint.to_owned()),
    );
    set_path(
        doc,
        &["providers", "openavoo-custom", "transport"],
        Yaml::String("chat_completions".into()),
    );
    // `key_env` names the `.env` variable holding the custom endpoint's key; the value itself
    // never reaches this file. `discover_models: true` lets the runtime list the endpoint's own
    // models (`GET <api>/models`); with it off the picker could only ever offer the single model
    // written below.
    if has_key {
        set_path(
            doc,
            &["providers", "openavoo-custom", "key_env"],
            Yaml::String("OPENNAVO_CUSTOM_API_KEY".into()),
        );
    }
    set_path(
        doc,
        &["providers", "openavoo-custom", "default_model"],
        Yaml::String(input.model.clone()),
    );
    set_path(
        doc,
        &["providers", "openavoo-custom", "models"],
        Yaml::Array(vec![Yaml::String(input.model.clone())]),
    );
    set_path(
        doc,
        &["providers", "openavoo-custom", "discover_models"],
        Yaml::Boolean(true),
    );
}

/// Parse `config.yaml` into the document a write merges onto.
///
/// Anything this crate cannot round-trip (invalid YAML, a non-mapping document, anchors) counts
/// as absent: the caller then writes a complete document, which is what the app did before it
/// merged.
fn config_doc(raw: Option<&str>) -> Yaml {
    let Some(raw) = raw else {
        return Yaml::Hash(Hash::new());
    };
    let Ok(docs) = YamlLoader::load_from_str(raw) else {
        return Yaml::Hash(Hash::new());
    };
    match docs.into_iter().next() {
        Some(doc @ Yaml::Hash(_)) if !has_alias(&doc) => doc,
        _ => Yaml::Hash(Hash::new()),
    }
}

fn has_alias(value: &Yaml) -> bool {
    match value {
        Yaml::Alias(_) | Yaml::BadValue => true,
        Yaml::Array(items) => items.iter().any(has_alias),
        Yaml::Hash(map) => map
            .iter()
            .any(|(key, value)| has_alias(key) || has_alias(value)),
        _ => false,
    }
}

fn hash_mut(value: &mut Yaml) -> &mut Hash {
    if !matches!(value, Yaml::Hash(_)) {
        *value = Yaml::Hash(Hash::new());
    }
    let Yaml::Hash(map) = value else {
        unreachable!("a hash was just inserted")
    };
    map
}

fn lookup_mut<'a>(doc: &'a mut Yaml, path: &[&str]) -> Option<&'a mut Yaml> {
    let mut node = doc;
    for key in path {
        let Yaml::Hash(map) = node else {
            return None;
        };
        node = map.get_mut(&Yaml::String((*key).to_owned()))?;
    }
    Some(node)
}

fn set_path(doc: &mut Yaml, path: &[&str], value: Yaml) {
    let Some((last, parents)) = path.split_last() else {
        return;
    };
    let mut node = doc;
    for key in parents {
        node = hash_mut(node)
            .entry(Yaml::String((*key).to_owned()))
            .or_insert_with(|| Yaml::Hash(Hash::new()));
    }
    hash_mut(node).insert(Yaml::String((*last).to_owned()), value);
}

/// Remove one key, and any section the removal leaves empty, without touching its siblings.
fn remove_path(doc: &mut Yaml, path: &[&str]) {
    let Some((last, parents)) = path.split_last() else {
        return;
    };
    if let Some(Yaml::Hash(map)) = lookup_mut(doc, parents) {
        map.remove(&Yaml::String((*last).to_owned()));
    }
    for depth in (1..=parents.len()).rev() {
        let empty =
            matches!(lookup_mut(doc, &path[..depth]), Some(Yaml::Hash(map)) if map.is_empty());
        if !empty {
            break;
        }
        let Some((key, grand)) = path[..depth].split_last() else {
            break;
        };
        if let Some(Yaml::Hash(map)) = lookup_mut(doc, grand) {
            map.remove(&Yaml::String((*key).to_owned()));
        }
    }
}

fn render_config(doc: &Yaml) -> Result<String, AppError> {
    if matches!(doc, Yaml::Hash(map) if map.is_empty()) {
        return Ok(String::new());
    }
    let mut out = String::new();
    YamlEmitter::new(&mut out).dump(doc).map_err(|error| {
        AppError::new(
            "E_HERMES_CONFIG",
            "Could not serialize the Hermes configuration.",
        )
        .with_detail(error.to_string())
    })?;
    let body = out.strip_prefix("---\n").unwrap_or(&out).to_owned();
    // Keep a POSIX trailing newline so re-writing an unchanged document stays a no-op.
    let body = if body.ends_with('\n') {
        body
    } else {
        format!("{body}\n")
    };
    // `YamlEmitter` only quotes what YAML requires, and a bare `4` or `~` is legal but reads back
    // as an integer and a null. When the emitted text no longer parses back to the same document,
    // fall back to quoting every string so the file means exactly what was written.
    let reparsed = YamlLoader::load_from_str(&body)
        .ok()
        .and_then(|docs| docs.into_iter().next());
    if reparsed.as_ref() == Some(doc) {
        return Ok(body);
    }
    Ok(quoted_config(doc))
}

/// Serialize a document with every string quoted: the exact fallback for [`render_config`].
fn quoted_config(doc: &Yaml) -> String {
    let mut out = String::new();
    write_block(&mut out, doc, 0);
    out.push('\n');
    out
}

fn write_block(out: &mut String, value: &Yaml, indent: usize) {
    match value {
        Yaml::Hash(map) => {
            for (index, (key, item)) in map.iter().enumerate() {
                if index > 0 {
                    out.push('\n');
                }
                pad(out, indent);
                write_scalar(out, key);
                out.push(':');
                write_child(out, item, indent);
            }
        }
        Yaml::Array(items) => {
            for (index, item) in items.iter().enumerate() {
                if index > 0 {
                    out.push('\n');
                }
                pad(out, indent);
                out.push('-');
                write_child(out, item, indent);
            }
        }
        other => {
            pad(out, indent);
            write_scalar(out, other);
        }
    }
}

fn write_child(out: &mut String, value: &Yaml, indent: usize) {
    match value {
        Yaml::Hash(map) if map.is_empty() => out.push_str(" {}"),
        Yaml::Array(items) if items.is_empty() => out.push_str(" []"),
        Yaml::Hash(_) | Yaml::Array(_) => {
            out.push('\n');
            write_block(out, value, indent + 2);
        }
        other => {
            out.push(' ');
            write_scalar(out, other);
        }
    }
}

fn write_scalar(out: &mut String, value: &Yaml) {
    match value {
        // `serde_json` escaping is a subset of YAML's double-quoted style, so a string is always
        // safe to write this way.
        Yaml::String(text) => match serde_json::to_string(text) {
            Ok(quoted) => out.push_str(&quoted),
            Err(_) => out.push_str("\"\""),
        },
        Yaml::Integer(number) => out.push_str(&number.to_string()),
        Yaml::Real(number) => out.push_str(number),
        Yaml::Boolean(flag) => out.push_str(if *flag { "true" } else { "false" }),
        Yaml::Hash(_) | Yaml::Array(_) | Yaml::Null | Yaml::BadValue | Yaml::Alias(_) => {
            out.push_str("null");
        }
    }
}

fn pad(out: &mut String, indent: usize) {
    for _ in 0..indent {
        out.push(' ');
    }
}

fn kill_pid(pid: u32) {
    if pid == 0 {
        return;
    }
    #[cfg(unix)]
    {
        use nix::sys::signal::{Signal, kill};
        use nix::unistd::Pid;
        let _ = kill(Pid::from_raw(pid as i32), Signal::SIGTERM);
    }
}

/// Store a provider API key in the selected profile's `.env`, the same file the official
/// Hermes CLI and dashboard read and write. `profile` is `None`/`default` for the root home,
/// otherwise the key belongs to `<root>/profiles/<name>/.env`.
pub fn secret_set(
    app: &AppHandle,
    provider: &str,
    key: &str,
    profile: Option<&str>,
) -> Result<(), AppError> {
    let home = profiles::resolve_home(&hermes_home(app)?, profile)?;
    env_store::set_env_value(&home, provider_key_env(provider), key)
}

/// Whether a key is stored for `provider` in the selected profile.
pub fn secret_status(
    app: &AppHandle,
    provider: &str,
    profile: Option<&str>,
) -> Result<bool, AppError> {
    let home = profiles::resolve_home(&hermes_home(app)?, profile)?;
    Ok(env_store::env_has(&home, provider_key_env(provider)))
}

/// Remove a provider's key line from the selected profile's `.env`.
pub fn secret_delete(
    app: &AppHandle,
    provider: &str,
    profile: Option<&str>,
) -> Result<(), AppError> {
    let home = profiles::resolve_home(&hermes_home(app)?, profile)?;
    env_store::remove_env_value(&home, provider_key_env(provider))?;
    Ok(())
}

/// Remote connections are Panel-only: the token-based remote transport was removed with
/// the connection kinds that used it (`stores/hermes.ts` keeps `local` and `panel`).
pub fn remote_auth_required() -> AppError {
    AppError::new(
        "E_HERMES_REMOTE_AUTH",
        "Remote connections sign in through Hermes Panel.",
    )
}

/// App data root, for the stores that are not part of `HERMES_HOME`.
fn session_root() -> Result<&'static Path, AppError> {
    DATA_ROOT
        .get()
        .map(PathBuf::as_path)
        .ok_or_else(|| AppError::new("E_HERMES_HOME", "The app data directory is unavailable."))
}

fn panel_cache() -> &'static Mutex<std::collections::HashMap<String, panel::PanelSession>> {
    static CACHE: std::sync::OnceLock<
        Mutex<std::collections::HashMap<String, panel::PanelSession>>,
    > = std::sync::OnceLock::new();
    CACHE.get_or_init(|| Mutex::new(std::collections::HashMap::new()))
}

fn missing_panel_session() -> AppError {
    AppError::new(
        "E_HERMES_PANEL_AUTH",
        "Hermes Panel session is missing. Sign in again.",
    )
}

/// Persist a panel session and prime the in-process cache.
pub fn panel_session_set(profile_id: &str, session: &panel::PanelSession) -> Result<(), AppError> {
    let value = serde_json::to_value(session).map_err(|error| {
        AppError::new("E_HERMES_CREDENTIAL", "Could not encode the panel session.")
            .with_detail(error.to_string())
    })?;
    env_store::session_set(session_root()?, profile_id, value)?;
    panel_cache()
        .lock()
        .map_err(|_| AppError::new("E_HERMES_CREDENTIAL", "Panel session cache is poisoned."))?
        .insert(profile_id.to_owned(), session.clone());
    Ok(())
}

pub fn panel_session(profile_id: &str) -> Result<panel::PanelSession, AppError> {
    if let Some(session) = panel_cache()
        .lock()
        .map_err(|_| AppError::new("E_HERMES_CREDENTIAL", "Panel session cache is poisoned."))?
        .get(profile_id)
        .cloned()
    {
        return Ok(session);
    }
    let value =
        env_store::session_get(session_root()?, profile_id).ok_or_else(missing_panel_session)?;
    let session: panel::PanelSession = serde_json::from_value(value).map_err(|error| {
        AppError::new("E_HERMES_CREDENTIAL", "The panel session is corrupt.")
            .with_detail(error.to_string())
    })?;
    panel_cache()
        .lock()
        .map_err(|_| AppError::new("E_HERMES_CREDENTIAL", "Panel session cache is poisoned."))?
        .insert(profile_id.to_owned(), session.clone());
    Ok(session)
}

pub fn panel_session_delete(profile_id: &str) -> Result<(), AppError> {
    if let Ok(mut cache) = panel_cache().lock() {
        cache.remove(profile_id);
    }
    env_store::session_delete(session_root()?, profile_id)
}

/// Drop every cached panel session. The stored copies live under the data root, so an
/// uninstall removes them; this keeps the running process from holding them too.
pub fn panel_sessions_forget() {
    if let Ok(mut cache) = panel_cache().lock() {
        cache.clear();
    }
}

fn local_opencode_key(provider: &str) -> Option<String> {
    let key = match provider {
        "opencode" => "opencode",
        "opencode-go" => "opencode-go",
        _ => return None,
    };
    let path = std::env::var_os("HOME")
        .map(PathBuf::from)?
        .join(".local/share/opencode/auth.json");
    let raw = std::fs::read_to_string(path).ok()?;
    let value = serde_json::from_str::<serde_json::Value>(&raw).ok()?;
    value
        .get(key)
        .and_then(|entry| entry.get("key"))
        .and_then(|key| key.as_str())
        .map(str::trim)
        .filter(|key| !key.is_empty())
        .map(str::to_owned)
}

/// Cache the app data root and start the one-time credential migration. Called once
/// from the app setup hook so panel-session helpers never depend on call order.
pub fn init(app: &AppHandle) {
    if data_root(app).is_ok() {
        migrate_legacy_credentials(app);
    }
}

/// Kick off the one-time migration of credentials older builds kept in the Keychain.
/// Runs at most once per process, on a detached thread, and never blocks startup.
fn migrate_legacy_credentials(app: &AppHandle) {
    static ONCE: std::sync::Once = std::sync::Once::new();
    ONCE.call_once(|| {
        let (Ok(root), Ok(home)) = (data_root(app), hermes_home(app)) else {
            return;
        };
        legacy_migration::spawn(root, home);
    });
}

#[derive(Debug)]
struct RuntimeVersionProbe {
    success: bool,
    stderr: String,
    version: Option<String>,
}

/// Read the runtime's own version with an empty, throwaway HERMES_HOME.
///
/// Without this, Hermes falls back to the user's `~/.hermes` checkout (or a
/// source tree) and can report a version that has nothing to do with the
/// packaged runtime OpenNavo installed.
async fn probe_runtime_version(bin: &Path) -> Result<RuntimeVersionProbe, AppError> {
    let check_home =
        std::env::temp_dir().join(format!("opennavo-hermes-check-{}", uuid::Uuid::new_v4()));
    std::fs::create_dir_all(&check_home)?;
    let mut command = Command::new("/bin/sh");
    apply_runtime_environment(&mut command);
    command
        .arg(bin)
        .arg("--version")
        .env("HERMES_HOME", &check_home)
        .env("NO_COLOR", "1")
        .env("TERM", "dumb")
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true);
    let mut child = match command.spawn() {
        Ok(child) => child,
        Err(error) => {
            let _ = std::fs::remove_dir_all(&check_home);
            return Err(
                AppError::new("E_HERMES_RUNTIME", "Hermes runtime could not start.")
                    .with_detail(error.to_string()),
            );
        }
    };
    let mut stdout_pipe = child.stdout.take().expect("version probe stdout is piped");
    let mut stderr_pipe = child.stderr.take().expect("version probe stderr is piped");
    let mut stdout = Vec::new();
    let mut stderr = Vec::new();
    let output = timeout(VERSION_TIMEOUT, async {
        let (stdout_result, stderr_result, status_result) = tokio::join!(
            stdout_pipe.read_to_end(&mut stdout),
            stderr_pipe.read_to_end(&mut stderr),
            child.wait(),
        );
        (stdout_result, stderr_result, status_result)
    })
    .await;
    let (stdout_result, stderr_result, status_result) = match output {
        Ok(output) => output,
        Err(_) => {
            // `kill_on_drop` would reap the child when the future is dropped, but the
            // throwaway HERMES_HOME must outlive the process that is still using it.
            let _ = child.kill().await;
            let _ = child.wait().await;
            let _ = std::fs::remove_dir_all(&check_home);
            return Err(AppError::new(
                "E_HERMES_TIMEOUT",
                "Hermes version check timed out.",
            ));
        }
    };
    let _ = std::fs::remove_dir_all(&check_home);
    stdout_result.map_err(|error| {
        AppError::new(
            "E_HERMES_RUNTIME",
            "Hermes runtime output could not be read.",
        )
        .with_detail(error.to_string())
    })?;
    stderr_result.map_err(|error| {
        AppError::new(
            "E_HERMES_RUNTIME",
            "Hermes runtime output could not be read.",
        )
        .with_detail(error.to_string())
    })?;
    let status = status_result.map_err(|error| {
        AppError::new("E_HERMES_RUNTIME", "Hermes runtime could not start.")
            .with_detail(error.to_string())
    })?;
    let stdout = clean_output(&String::from_utf8_lossy(&stdout));
    let stderr = clean_output(&String::from_utf8_lossy(&stderr));
    Ok(RuntimeVersionProbe {
        success: status.success(),
        version: status
            .success()
            .then(|| runtime_version_from_output(&stdout))
            .flatten(),
        stderr,
    })
}

async fn verify_runtime_bin(bin: &Path, expected: &str) -> Result<(), AppError> {
    let probe = probe_runtime_version(bin).await?;
    if !probe.success {
        return Err(AppError::new(
            "E_HERMES_RUNTIME",
            "The Hermes runtime version check failed.",
        )
        .with_detail(probe.stderr));
    }
    let actual = probe.version.unwrap_or_else(|| "unknown".to_owned());
    if actual != expected {
        return Err(AppError::new(
            "E_HERMES_RUNTIME",
            "The Hermes runtime does not match the pinned version.",
        )
        .with_detail(format!("expected {expected}, got {actual}")));
    }
    Ok(())
}

pub async fn status(app: AppHandle) -> Result<HermesRuntimeStatus, AppError> {
    migrate_legacy_credentials(&app);
    let expected_version = expected_runtime_version()?;
    let Some(bin) = runtime_bin(&app) else {
        log::warn!("Hermes runtime status: no runtime binary found");
        return Ok(HermesRuntimeStatus {
            ready: false,
            installed: false,
            version: None,
            expected_version,
            path: None,
            data_path: data_root(&app).ok().map(|path| path.display().to_string()),
            computer_use: false,
            outdated: false,
            message: Some("Hermes runtime is not installed. Install it from the assistant.".into()),
        });
    };
    log::info!("Hermes runtime status: checking {}", bin.display());
    let probe = probe_runtime_version(&bin).await.map_err(|error| {
        log::warn!("Hermes runtime status: version check failed: {error}");
        error
    })?;
    if !probe.success {
        log::warn!(
            "Hermes runtime status: version failed stderr={}",
            probe.stderr
        );
        return Ok(HermesRuntimeStatus {
            ready: false,
            installed: true,
            version: None,
            expected_version,
            path: Some(bin.display().to_string()),
            data_path: data_root(&app).ok().map(|path| path.display().to_string()),
            computer_use: false,
            outdated: false,
            message: Some(probe.stderr),
        });
    }
    let version = probe.version;
    let computer_use = bin
        .parent()
        .and_then(|dir| dir.parent())
        .map(|root| {
            root.join("CuaDriver.app/Contents/MacOS/cua-driver")
                .is_file()
        })
        .unwrap_or(false);
    let outdated = version.as_deref() != Some(expected_version.as_str());
    let message = version.is_none().then(|| {
        format!(
            "The installed Hermes runtime does not report a packaged version. Update it to {expected_version}."
        )
    });
    log::info!(
        "Hermes runtime status: ready {} (computer_use={computer_use}, outdated={outdated})",
        version.as_deref().unwrap_or("unknown")
    );
    Ok(HermesRuntimeStatus {
        ready: true,
        installed: true,
        version,
        expected_version,
        path: Some(bin.display().to_string()),
        data_path: data_root(&app).ok().map(|path| path.display().to_string()),
        computer_use,
        outdated,
        message,
    })
}

pub async fn chat(
    app: AppHandle,
    state: &HermesRuntimeState,
    input: HermesChatInput,
) -> Result<HermesChatReply, AppError> {
    let home = hermes_home(&app)?;
    if input
        .gateway_url
        .as_deref()
        .is_some_and(|value| !value.trim().is_empty())
    {
        let profile_id = input.profile_id.as_deref().unwrap_or_default().to_owned();
        if input.panel_auth {
            let ticket = panel::ticket_for(&profile_id).await?;
            let query = format!("ticket={}", urlencoding::encode(&ticket));
            let panel_url = panel::base_url_for(&profile_id)?;
            return gateway::chat_remote_auth(
                &panel_url,
                &query,
                &home,
                state,
                input,
                Some(&app),
                None,
            )
            .await;
        }
        return Err(remote_auth_required());
    }
    let Some(bin) = runtime_bin(&app) else {
        return Err(AppError::new(
            "E_HERMES_RUNTIME",
            "Hermes runtime is not installed. Install it from the assistant.",
        ));
    };
    gateway::chat(&bin, &home, state, input, Some(&app), None).await
}

#[allow(dead_code)]
async fn chat_with_bin(
    bin: &Path,
    home: &Path,
    state: &HermesRuntimeState,
    input: HermesChatInput,
    app: Option<&AppHandle>,
    sink: Option<UnboundedSender<String>>,
) -> Result<HermesChatReply, AppError> {
    if input.prompt.trim().is_empty() {
        return Err(AppError::new("E_HERMES_INPUT", "The message is empty."));
    }
    write_config(home, &input)?;

    let prompt = {
        let mut prompt = input.prompt.clone();
        for path in &input.attachments {
            prompt.push_str(&format!("\n[Attached file: {path}]"));
        }
        prompt
    };
    let provider = provider_id(&input.provider);
    let mut command = runtime_command(bin)?;
    if let Some(dir) = bin.parent() {
        let path = std::env::var_os("PATH").unwrap_or_default();
        let mut paths = vec![dir.to_path_buf()];
        paths.extend(std::env::split_paths(&path));
        if let Ok(joined) = std::env::join_paths(paths) {
            command.env("PATH", joined);
        }
        let cua = dir
            .parent()
            .map(|root| root.join("CuaDriver.app/Contents/MacOS/cua-driver"));
        if let Some(cua) = cua.filter(|path| path.is_file()) {
            command.env("HERMES_CUA_DRIVER_CMD", cua);
        }
    }
    command
        .arg("chat")
        .arg("-Q")
        .arg("--provider")
        .arg(provider)
        .arg("--model")
        .arg(&input.model)
        .arg("--toolsets")
        .arg(toolsets(&input))
        .arg("-q")
        .arg(&prompt)
        .env("HERMES_HOME", home)
        .env("PYTHONUNBUFFERED", "1")
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    if let Some(session) = input
        .session_id
        .as_deref()
        .map(str::trim)
        .filter(|value| !value.is_empty())
    {
        command.arg("--resume").arg(session);
    }
    if input.mac_enabled && input.control_level == "full" && !input.confirm_writes {
        command.arg("--yolo");
    }
    if let Some(dir) = input
        .cwd
        .as_deref()
        .map(str::trim)
        .filter(|dir| !dir.is_empty() && Path::new(dir).is_dir())
    {
        command.current_dir(dir);
    }
    let mut child = command.spawn().map_err(|error| {
        AppError::new("E_HERMES_RUNTIME", "Hermes runtime could not start.")
            .with_detail(error.to_string())
    })?;
    let pid = child.id();
    if let Some(pid) = pid
        && let Ok(mut current) = state.pid.lock()
    {
        *current = Some(pid);
    }
    let Some(stdout) = child.stdout.take() else {
        return Err(AppError::new(
            "E_HERMES_CHAT",
            "Hermes stdout was unavailable.",
        ));
    };
    let Some(stderr) = child.stderr.take() else {
        return Err(AppError::new(
            "E_HERMES_CHAT",
            "Hermes stderr was unavailable.",
        ));
    };
    let request_id = input.request_id.clone();
    let emitter = app.cloned();
    let stdout_task =
        tokio::spawn(async move { consume_stdout(stdout, emitter, sink, request_id).await });
    let stderr_task = tokio::spawn(async move {
        let mut buffer = String::new();
        let mut reader = stderr;
        let _ = reader.read_to_string(&mut buffer).await;
        buffer
    });

    let status = timeout(CHAT_TIMEOUT, child.wait()).await;
    if let Ok(mut current) = state.pid.lock() {
        *current = None;
    }
    let status = match status {
        Ok(Ok(status)) => status,
        Ok(Err(error)) => {
            stdout_task.abort();
            stderr_task.abort();
            return Err(AppError::new("E_HERMES_CHAT", "Hermes chat failed.")
                .with_detail(error.to_string()));
        }
        Err(_) => {
            if let Some(pid) = pid {
                kill_pid(pid);
            }
            stdout_task.abort();
            stderr_task.abort();
            return Err(AppError::new(
                "E_HERMES_TIMEOUT",
                "Hermes took too long to answer.",
            ));
        }
    };
    let stdout = stdout_task.await.unwrap_or_default();
    let stderr = clean_output(&stderr_task.await.unwrap_or_default());
    let stdout = clean_response(&stdout);
    if !status.success() {
        let detail = if stderr.is_empty() {
            stdout.clone()
        } else {
            stderr.clone()
        };
        append_audit(
            home,
            &input,
            "failed",
            session_id(&stderr).as_deref(),
            Some(&detail),
        );
        return Err(
            AppError::new("E_HERMES_CHAT", "Hermes could not complete this request.")
                .with_detail(detail),
        );
    }
    let session = session_id(&stderr);
    append_audit(home, &input, "succeeded", session.as_deref(), None);
    Ok(HermesChatReply {
        text: stdout,
        session_id: session,
        live_session_id: None,
        usage: None,
    })
}

#[derive(Default)]
#[allow(dead_code)]
struct StreamFilter {
    initial: String,
    checked: bool,
}

#[allow(dead_code)]
impl StreamFilter {
    /// Hold the first line long enough to drop Hermes' optional scanner warning,
    /// then release tokens immediately so the UI can render them as they arrive.
    /// The warning is sometimes printed without a newline before the answer.
    fn push(&mut self, text: &str) -> String {
        if self.checked {
            return text.to_owned();
        }
        self.initial.push_str(text);
        let trimmed = self.initial.trim_start();
        if trimmed.is_empty() {
            return String::new();
        }
        if !trimmed.starts_with('⚠') {
            self.checked = true;
            return std::mem::take(&mut self.initial);
        }
        if let Some(index) = self.initial.find("pattern matching only") {
            let marker = "pattern matching only";
            let rest = self.initial[index + marker.len()..].to_owned();
            self.initial.clear();
            self.checked = true;
            return rest.trim_start_matches(['\r', '\n']).to_owned();
        }
        if self.initial.len() >= 300 {
            self.checked = true;
            return std::mem::take(&mut self.initial);
        }
        String::new()
    }

    fn finish(&mut self) -> String {
        if self.checked {
            return String::new();
        }
        self.checked = true;
        std::mem::take(&mut self.initial)
    }
}

#[allow(dead_code)]
async fn consume_stdout<R: AsyncRead + Unpin>(
    mut reader: R,
    app: Option<AppHandle>,
    sink: Option<UnboundedSender<String>>,
    request_id: String,
) -> String {
    let mut raw = Vec::new();
    let mut buffer = [0_u8; 1024];
    let mut filter = StreamFilter::default();
    let mut full = String::new();
    loop {
        let read = match reader.read(&mut buffer).await {
            Ok(0) | Err(_) => break,
            Ok(read) => read,
        };
        raw.extend_from_slice(&buffer[..read]);
        let valid = match std::str::from_utf8(&raw) {
            Ok(value) => value.len(),
            Err(error) => error.valid_up_to(),
        };
        if valid == 0 {
            continue;
        }
        let text = String::from_utf8_lossy(&raw[..valid]).into_owned();
        raw.drain(..valid);
        let delta = filter.push(&clean_output(&text));
        if delta.is_empty() {
            continue;
        }
        full.push_str(&delta);
        if let Some(sink) = &sink {
            let _ = sink.send(delta.clone());
        }
        if let Some(app) = &app {
            let _ = app.emit(
                "hermes-chunk",
                HermesChunk {
                    request_id: request_id.clone(),
                    delta,
                },
            );
        }
    }
    let tail = filter.finish();
    if !tail.is_empty() {
        full.push_str(&tail);
        if let Some(sink) = &sink {
            let _ = sink.send(tail.clone());
        }
        if let Some(app) = &app {
            let _ = app.emit(
                "hermes-chunk",
                HermesChunk {
                    request_id,
                    delta: tail,
                },
            );
        }
    }
    full
}

pub async fn stop(state: &HermesRuntimeState) -> Result<(), AppError> {
    state.approval_cancel.notify_waiters();
    let _ = state.asks.lock().await.take();
    let pid = state.pid.lock().ok().and_then(|current| *current);
    if let Some(pid) = pid {
        kill_pid(pid);
    }
    if let Some(server) = state.gateway.lock().await.take() {
        server.stop();
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn maps_ui_providers_to_hermes_provider_ids() {
        assert_eq!(provider_id("openai"), "openai-api");
        assert_eq!(provider_id("google"), "gemini");
        assert_eq!(provider_id("local"), "openavoo-custom");
        assert_eq!(provider_id("custom"), "openavoo-custom");
        assert_eq!(provider_id("anthropic"), "anthropic");
    }

    #[test]
    fn writes_auxiliary_task_assignments() {
        let dir = tempfile::tempdir().unwrap();
        let input = HermesChatInput {
            prompt: "hello".into(),
            request_id: "aux-test".into(),
            session_id: None,
            provider: "openai".into(),
            model: "gpt-5.4".into(),
            effort: "medium".into(),
            fast: false,
            api_key: Some("test-key".into()),
            endpoint: None,
            cwd: None,
            attachments: Vec::new(),
            auxiliary: vec![
                HermesAuxAssignment {
                    task: "title_generation".into(),
                    provider: "openai-api".into(),
                    model: "gpt-5.4-mini".into(),
                },
                HermesAuxAssignment {
                    task: "vision".into(),
                    provider: "auto".into(),
                    model: String::new(),
                },
            ],
            force_aux_vision: true,
            gateway_url: None,
            profile_id: None,
            profile: None,
            panel_auth: false,
            mac_enabled: false,
            control_level: "open".into(),
            mac_terminal: true,
            mac_screen: true,
            mac_files: true,
            mac_cron: true,
            confirm_writes: false,
        };
        write_config(dir.path(), &input).unwrap();
        let config = std::fs::read_to_string(dir.path().join("config.yaml")).unwrap();
        assert!(config.contains("provider: openai-api"));
        assert!(config.contains("default: gpt-5.4"));
        assert!(config.contains("auxiliary:"));
        assert!(config.contains("title_generation:"));
        assert!(config.contains("gpt-5.4-mini"));
        assert!(config.contains("image_input_mode: text"));
        assert!(!config.contains("vision:"));
    }

    #[test]
    fn maps_providers_to_their_dot_env_variables() {
        assert_eq!(provider_key_env("openai"), "OPENAI_API_KEY");
        assert_eq!(provider_key_env("codex"), "OPENAI_API_KEY");
        assert_eq!(provider_key_env("anthropic"), "ANTHROPIC_API_KEY");
        assert_eq!(provider_key_env("claude-code"), "ANTHROPIC_API_KEY");
        assert_eq!(provider_key_env("google"), "GOOGLE_API_KEY");
        assert_eq!(provider_key_env("openrouter"), "OPENROUTER_API_KEY");
        assert_eq!(provider_key_env("nous"), "NOUS_API_KEY");
        assert_eq!(provider_key_env("copilot"), "GITHUB_TOKEN");
        assert_eq!(provider_key_env("custom"), "OPENNAVO_CUSTOM_API_KEY");
        assert_eq!(provider_key_env("local"), "OPENNAVO_CUSTOM_API_KEY");
    }

    #[tokio::test]
    async fn runtime_commands_drop_the_inherited_environment() {
        let dir = tempfile::tempdir().unwrap();
        let bin = fake_runtime(dir.path(), "#!/bin/sh\nenv\n");
        let inherited = ["CARGO_MANIFEST_DIR", "PWD", "OLDPWD"]
            .into_iter()
            .find(|name| std::env::var_os(name).is_some())
            .expect("the test process should expose at least one non-allowlisted variable");
        let output = runtime_command(&bin).unwrap().output().await.unwrap();
        assert!(output.status.success());
        let stdout = String::from_utf8_lossy(&output.stdout);
        assert!(
            !stdout
                .lines()
                .any(|line| line.starts_with(&format!("{inherited}="))),
            "{inherited} must not reach the Hermes runtime"
        );
        if std::env::var_os("PATH").is_some() {
            assert!(stdout.lines().any(|line| line.starts_with("PATH=")));
        }
        assert!(stdout.lines().any(|line| line.starts_with("PYTHONPATH=")));
    }

    #[test]
    fn writes_a_supplied_key_to_dot_env_and_never_to_config() {
        let dir = tempfile::tempdir().unwrap();
        let mut input = test_input();
        input.api_key = Some("sk-in-dot-env".into());
        let key = write_config(dir.path(), &input).unwrap();
        assert_eq!(key.as_deref(), Some("sk-in-dot-env"));

        let env = std::fs::read_to_string(dir.path().join(".env")).unwrap();
        assert_eq!(env, "OPENAI_API_KEY=sk-in-dot-env\n");
        let config = std::fs::read_to_string(dir.path().join("config.yaml")).unwrap();
        assert!(!config.contains("sk-in-dot-env"));
        // The key lives in `.env`; the app no longer maintains an auth store of its own.
        assert!(!dir.path().join("auth.json").exists());
    }

    #[test]
    fn reads_the_key_back_from_dot_env_when_the_request_has_none() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join(".env"), "OPENAI_API_KEY=sk-stored\n").unwrap();
        let mut input = test_input();
        input.api_key = None;
        let key = write_config(dir.path(), &input).unwrap();
        assert_eq!(key.as_deref(), Some("sk-stored"));
        let env = std::fs::read_to_string(dir.path().join(".env")).unwrap();
        assert_eq!(env, "OPENAI_API_KEY=sk-stored\n");
    }

    #[test]
    fn keeps_the_users_own_dot_env_lines() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(
            dir.path().join(".env"),
            "# added by hand\nMY_SETTING=keep\nOPENAI_API_KEY=sk-old\n",
        )
        .unwrap();
        let mut input = test_input();
        input.api_key = Some("sk-new".into());
        write_config(dir.path(), &input).unwrap();
        let env = std::fs::read_to_string(dir.path().join(".env")).unwrap();
        assert!(env.contains("# added by hand"));
        assert!(env.contains("MY_SETTING=keep"));
        assert!(env.contains("OPENAI_API_KEY=sk-new"));
        assert!(!env.contains("sk-old"));
        assert_eq!(env.matches("OPENAI_API_KEY").count(), 1);
    }

    #[test]
    fn leaves_upstream_oauth_state_untouched() {
        let dir = tempfile::tempdir().unwrap();
        // Hermes owns `auth.json`; the app must not read, rewrite or scrub it.
        let original = serde_json::json!({
            "version": 1,
            "active_provider": "nous",
            "providers": {"nous": {"access_token": "oauth-token"}},
            "credential_pool": {
                "nous": [{"id": "nous1", "source": "device_code", "access_token": "oauth-token"}],
                "openai-api": [{"id": "env1", "source": "env:OPENAI_API_KEY", "access_token": "sk-old"}]
            }
        });
        let path = dir.path().join("auth.json");
        let bytes = serde_json::to_vec_pretty(&original).unwrap();
        std::fs::write(&path, &bytes).unwrap();

        let mut input = test_input();
        input.api_key = Some("sk-new".into());
        write_config(dir.path(), &input).unwrap();

        assert_eq!(std::fs::read(&path).unwrap(), bytes);
    }

    #[test]
    fn custom_endpoints_name_the_dot_env_variable_instead_of_inlining_the_key() {
        let dir = tempfile::tempdir().unwrap();
        let mut input = test_input();
        input.provider = "custom".into();
        input.model = "my-model".into();
        input.endpoint = Some("https://example.test/v1".into());
        input.api_key = Some("sk-custom".into());
        let key = write_config(dir.path(), &input).unwrap();
        assert_eq!(key.as_deref(), Some("sk-custom"));

        let config = std::fs::read_to_string(dir.path().join("config.yaml")).unwrap();
        assert!(config.contains("key_env: OPENNAVO_CUSTOM_API_KEY"));
        assert!(!config.contains("sk-custom"));
        let env = std::fs::read_to_string(dir.path().join(".env")).unwrap();
        assert_eq!(env, "OPENNAVO_CUSTOM_API_KEY=sk-custom\n");
    }

    #[test]
    fn custom_provider_without_a_url_writes_no_provider_block() {
        let dir = tempfile::tempdir().unwrap();
        let mut input = test_input();
        input.provider = "custom".into();
        input.model = "grok-4.7".into();
        input.endpoint = None;
        input.api_key = None;
        write_config(dir.path(), &input).unwrap();
        let config = std::fs::read_to_string(dir.path().join("config.yaml")).unwrap();
        // Defaulting a custom endpoint to the Ollama URL would silently probe a local
        // server the user never asked for.
        assert!(!config.contains("providers:"));
        assert!(!config.contains("11434"));
    }

    #[test]
    fn local_provider_still_defaults_to_the_ollama_url() {
        let dir = tempfile::tempdir().unwrap();
        let mut input = test_input();
        input.provider = "local".into();
        input.model = "llama".into();
        input.endpoint = None;
        input.api_key = None;
        write_config(dir.path(), &input).unwrap();
        let config = std::fs::read_to_string(dir.path().join("config.yaml")).unwrap();
        assert!(config.contains("http://127.0.0.1:11434/v1"));
    }

    #[test]
    fn omits_key_env_when_a_custom_endpoint_has_no_key() {
        let dir = tempfile::tempdir().unwrap();
        let mut input = test_input();
        input.provider = "local".into();
        input.model = "llama".into();
        input.endpoint = Some("http://127.0.0.1:11434/v1".into());
        input.api_key = None;
        write_config(dir.path(), &input).unwrap();
        let config = std::fs::read_to_string(dir.path().join("config.yaml")).unwrap();
        assert!(!config.contains("key_env"));
    }

    /// A profile's `config.yaml` holds settings the desktop never shows (tools, skills, browser,
    /// cron, other providers). A write must merge onto it instead of replacing the file.
    #[test]
    fn keeps_settings_the_desktop_does_not_own() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(
            dir.path().join("config.yaml"),
            "_config_version: 7\ntools:\n  browser:\n    enabled: true\nagent:\n  max_turns: 12\n  reasoning_effort: high\nproviders:\n  mine:\n    api: http://127.0.0.1:8000/v1\nmodel:\n  provider: nous\n  default: hermes-4\n",
        )
        .unwrap();
        let mut input = test_input();
        input.model = "gpt-test".into();
        write_config(dir.path(), &input).unwrap();

        let config = std::fs::read_to_string(dir.path().join("config.yaml")).unwrap();
        assert!(config.contains("_config_version: 7"), "{config}");
        assert!(config.contains("browser:"), "{config}");
        assert!(config.contains("max_turns: 12"), "{config}");
        assert!(config.contains("mine:"), "{config}");
        // The keys the desktop owns are still rewritten.
        assert!(config.contains("reasoning_effort: medium"), "{config}");
        assert!(config.contains("provider: openai-api"), "{config}");
        assert!(config.contains("default: gpt-test"), "{config}");
        assert!(!config.contains("hermes-4"), "{config}");
    }

    /// Selecting a profile in the settings sheet loads its catalog. That read used to write the
    /// draft back, replacing the profile's own model with the built-in default.
    #[test]
    fn a_catalog_read_leaves_the_profile_model_alone() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(
            dir.path().join("config.yaml"),
            "model:\n  provider: nous\n  default: hermes-4\nagent:\n  reasoning_effort: high\n",
        )
        .unwrap();
        let mut input = test_input();
        input.provider = "nous".into();
        input.model = "Hermes 4 · 405B".into();
        write_catalog_config(dir.path(), &input).unwrap();

        let config = std::fs::read_to_string(dir.path().join("config.yaml")).unwrap();
        assert_eq!(
            config,
            "model:\n  provider: nous\n  default: hermes-4\nagent:\n  reasoning_effort: high\n"
        );
    }

    /// A catalog read of a fresh profile still has to bootstrap the file, otherwise the runtime
    /// refuses the `--profile` target it was started with.
    #[test]
    fn a_catalog_read_bootstraps_a_profile_without_a_config() {
        let dir = tempfile::tempdir().unwrap();
        let input = test_input();
        write_catalog_config(dir.path(), &input).unwrap();
        let config = std::fs::read_to_string(dir.path().join("config.yaml")).unwrap();
        assert!(config.contains("provider: openai-api"), "{config}");
        assert!(config.contains("default: gpt-test"), "{config}");
    }

    /// A profile that is already live through another marker keeps the runtime's own defaults;
    /// a catalog read must not invent a model for it.
    #[test]
    fn a_catalog_read_does_not_invent_a_config_for_a_live_profile() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join(".env"), "OPENAI_API_KEY=sk-stored\n").unwrap();
        let mut input = test_input();
        input.api_key = None;
        write_catalog_config(dir.path(), &input).unwrap();
        assert!(!dir.path().join("config.yaml").exists());
    }

    #[test]
    fn a_catalog_read_still_registers_the_custom_endpoint() {
        let dir = tempfile::tempdir().unwrap();
        let mut input = test_input();
        input.provider = "custom".into();
        input.model = "my-model".into();
        input.endpoint = Some("https://example.test/v1".into());
        write_catalog_config(dir.path(), &input).unwrap();
        let config = std::fs::read_to_string(dir.path().join("config.yaml")).unwrap();
        assert!(config.contains("openavoo-custom:"), "{config}");
        assert!(config.contains("https://example.test/v1"), "{config}");
    }

    /// Putting an auxiliary slot back on `auto` has to remove its override, or the runtime keeps
    /// using the model the user just cleared.
    #[test]
    fn a_slot_back_on_auto_drops_its_override() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(
            dir.path().join("config.yaml"),
            "auxiliary:\n  vision:\n    provider: openai-api\n    model: gpt-4o\n  curator:\n    provider: openai-api\n    model: keep-me\n",
        )
        .unwrap();
        let mut input = test_input();
        input.auxiliary = vec![HermesAuxAssignment {
            task: "vision".into(),
            provider: "auto".into(),
            model: String::new(),
        }];
        write_config(dir.path(), &input).unwrap();

        let config = std::fs::read_to_string(dir.path().join("config.yaml")).unwrap();
        assert!(!config.contains("gpt-4o"), "{config}");
        // A slot this build does not send is left alone.
        assert!(config.contains("keep-me"), "{config}");
    }

    #[test]
    fn a_missing_auxiliary_section_does_not_leave_an_empty_shell() {
        let dir = tempfile::tempdir().unwrap();
        let mut input = test_input();
        input.auxiliary = vec![HermesAuxAssignment {
            task: "vision".into(),
            provider: "auto".into(),
            model: String::new(),
        }];
        write_config(dir.path(), &input).unwrap();
        let config = std::fs::read_to_string(dir.path().join("config.yaml")).unwrap();
        assert!(!config.contains("auxiliary"), "{config}");
    }

    /// `YamlEmitter` leaves `4` and `~` bare, which would read back as an integer and a null.
    /// That document takes the fully quoted fallback, so this also covers its nesting.
    #[test]
    fn keeps_scalars_that_would_change_type() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(
            dir.path().join("config.yaml"),
            "tools:\n  browser:\n    enabled: true\n",
        )
        .unwrap();
        let mut input = test_input();
        input.provider = "local".into();
        input.model = "4".into();
        write_config(dir.path(), &input).unwrap();
        let config = std::fs::read_to_string(dir.path().join("config.yaml")).unwrap();
        let docs = YamlLoader::load_from_str(&config).unwrap();
        assert_eq!(docs[0]["model"]["default"].as_str(), Some("4"), "{config}");
        assert_eq!(
            docs[0]["providers"]["openavoo-custom"]["models"]
                .as_vec()
                .map(Vec::len),
            Some(1),
            "{config}"
        );
        assert_eq!(
            docs[0]["tools"]["browser"]["enabled"].as_bool(),
            Some(true),
            "{config}"
        );
    }

    /// An unreadable file is written from scratch rather than merging onto a half-parsed doc.
    #[test]
    fn rewrites_a_config_it_cannot_parse() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("config.yaml"), "model: [unclosed\n").unwrap();
        let input = test_input();
        write_config(dir.path(), &input).unwrap();
        let config = std::fs::read_to_string(dir.path().join("config.yaml")).unwrap();
        assert!(config.contains("provider: openai-api"), "{config}");
        assert!(!config.contains("unclosed"), "{config}");
    }

    #[test]
    fn removing_a_provider_key_leaves_other_variables() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(
            dir.path().join(".env"),
            "OPENAI_API_KEY=sk-x\nANTHROPIC_API_KEY=sk-y\n",
        )
        .unwrap();
        assert!(env_store::remove_env_value(dir.path(), "OPENAI_API_KEY").unwrap());
        let env = std::fs::read_to_string(dir.path().join(".env")).unwrap();
        assert_eq!(env, "ANTHROPIC_API_KEY=sk-y\n");
    }

    #[test]
    fn maps_reasoning_effort() {
        assert_eq!(reasoning_effort("auto"), None);
        assert_eq!(reasoning_effort("medium"), Some("medium"));
        assert_eq!(reasoning_effort("high"), Some("high"));
    }

    #[test]
    fn runtime_manifest_pins_the_expected_version() {
        let manifest = runtime_manifest().unwrap();
        assert_eq!(manifest.hermes.version, "0.23.1");
        assert_eq!(manifest.plugins.len(), 1);
        assert_eq!(manifest.plugins[0].name, "github-copilot");
        assert_eq!(
            manifest.plugins[0].repository,
            "https://github.com/NousResearch/hermes-labs-plugins"
        );
        assert_eq!(
            manifest.plugins[0].sha,
            "4987918a068fcf4b3efbb584db5dd05e5f57c669"
        );
    }

    #[test]
    fn runtime_version_ignores_dates_and_other_numbers() {
        assert_eq!(
            runtime_version_from_output("Hermes Agent v0.23.1 · stable").as_deref(),
            Some("0.23.1")
        );
        assert_eq!(
            runtime_version_from_output("Hermes Agent vunknown (2026.9.24)"),
            None
        );
        assert!(runtime_version_matches(
            "Hermes Agent v0.23.1 · stable",
            "0.23.1"
        ));
        assert!(!runtime_version_matches(
            "Hermes Agent v0.23.0 · stable",
            "0.23.1"
        ));
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn version_probe_sets_an_isolated_hermes_home() {
        use std::os::unix::fs::PermissionsExt;

        let dir = tempfile::tempdir().unwrap();
        let wrapper = dir.path().join("hermes");
        std::fs::write(
            &wrapper,
            "#!/bin/sh\nif [ -n \"${HERMES_HOME:-}\" ]; then printf 'Hermes Agent v0.23.1 · stable\\n'; else printf 'Hermes Agent vunknown (2026.9.24)\\n'; fi\n",
        )
        .unwrap();
        std::fs::set_permissions(&wrapper, std::fs::Permissions::from_mode(0o755)).unwrap();

        let probe = probe_runtime_version(&wrapper).await.unwrap();
        assert!(probe.success);
        assert_eq!(probe.version.as_deref(), Some("0.23.1"));
    }

    fn test_input() -> HermesChatInput {
        HermesChatInput {
            prompt: "hello".into(),
            request_id: "test-request".into(),
            session_id: None,
            provider: "openai".into(),
            model: "gpt-test".into(),
            effort: "medium".into(),
            fast: false,
            cwd: None,
            attachments: Vec::new(),
            auxiliary: Vec::new(),
            force_aux_vision: false,
            gateway_url: None,
            profile_id: None,
            profile: None,
            panel_auth: false,
            api_key: Some("test-key".into()),
            endpoint: None,
            mac_enabled: true,
            control_level: "full".into(),
            mac_terminal: true,
            mac_screen: true,
            mac_files: true,
            mac_cron: true,
            confirm_writes: false,
        }
    }

    #[tokio::test]
    async fn runs_a_sidecar_and_returns_stdout_and_session() {
        let dir = tempfile::tempdir().unwrap();
        let bin = fake_runtime(
            dir.path(),
            "#!/bin/sh\nprintf 'HERMES_OK\\n'\nprintf 'session_id: test-session\\n' >&2\nexit 0\n",
        );
        let state = HermesRuntimeState::default();
        let reply = chat_with_bin(&bin, dir.path(), &state, test_input(), None, None)
            .await
            .unwrap();
        assert_eq!(reply.text, "HERMES_OK");
        assert_eq!(reply.session_id.as_deref(), Some("test-session"));
    }

    #[tokio::test]
    #[ignore = "requires HERMES_RUNTIME_BIN and a reachable OpenAI-compatible test endpoint"]
    async fn runs_a_real_terminal_tool_call_in_full_control() {
        let bin = std::env::var("HERMES_RUNTIME_BIN").expect("HERMES_RUNTIME_BIN");
        let endpoint = std::env::var("HERMES_TEST_ENDPOINT").expect("HERMES_TEST_ENDPOINT");
        let dir = tempfile::tempdir().unwrap();
        let input = HermesChatInput {
            prompt: "Use the terminal to echo HERMES_TERMINAL_OK".into(),
            request_id: "test-request".into(),
            session_id: None,
            provider: "openai".into(),
            model: "gpt-4o-mini".into(),
            effort: "medium".into(),
            fast: false,
            cwd: None,
            attachments: Vec::new(),
            auxiliary: Vec::new(),
            force_aux_vision: false,
            gateway_url: None,
            profile_id: None,
            profile: None,
            panel_auth: false,
            api_key: Some("test".into()),
            endpoint: Some(endpoint),
            mac_enabled: true,
            control_level: "full".into(),
            mac_terminal: true,
            mac_screen: true,
            mac_files: true,
            mac_cron: true,
            confirm_writes: false,
        };
        let state = HermesRuntimeState::default();
        let reply = chat_with_bin(Path::new(&bin), dir.path(), &state, input, None, None)
            .await
            .unwrap();
        assert_eq!(reply.text.trim(), "HERMES_TERMINAL_OK");
    }

    #[test]
    fn stream_filter_drops_the_scanner_warning_and_releases_tokens() {
        let mut filter = StreamFilter::default();
        assert_eq!(
            filter.push("  ⚠ tirith security scanner enabled but not available — command scanning will use pattern matching onlyHel"),
            "Hel"
        );
        assert_eq!(filter.push("lo"), "lo");
    }

    #[tokio::test]
    #[ignore = "requires HERMES_RUNTIME_BIN and a reachable OpenAI-compatible test endpoint"]
    async fn runs_the_real_hermes_runtime_against_a_test_endpoint() {
        let bin = std::env::var("HERMES_RUNTIME_BIN").expect("HERMES_RUNTIME_BIN");
        let endpoint = std::env::var("HERMES_TEST_ENDPOINT").expect("HERMES_TEST_ENDPOINT");
        let dir = tempfile::tempdir().unwrap();
        let input = HermesChatInput {
            prompt: "Reply with HERMES_OK".into(),
            request_id: "test-request".into(),
            session_id: None,
            provider: "openai".into(),
            model: "gpt-4o-mini".into(),
            effort: "medium".into(),
            fast: false,
            cwd: None,
            attachments: Vec::new(),
            auxiliary: Vec::new(),
            force_aux_vision: false,
            gateway_url: None,
            profile_id: None,
            profile: None,
            panel_auth: false,
            api_key: Some("test".into()),
            endpoint: Some(endpoint),
            mac_enabled: false,
            control_level: "open".into(),
            mac_terminal: true,
            mac_screen: true,
            mac_files: true,
            mac_cron: true,
            confirm_writes: false,
        };
        let state = HermesRuntimeState::default();
        let reply = chat_with_bin(Path::new(&bin), dir.path(), &state, input, None, None)
            .await
            .unwrap();
        assert!(!reply.text.trim().is_empty());
        assert!(reply.session_id.is_some());
    }

    #[tokio::test]
    async fn reports_sidecar_failure_with_stderr() {
        let dir = tempfile::tempdir().unwrap();
        let bin = fake_runtime(
            dir.path(),
            "#!/bin/sh\necho 'provider is not configured' >&2\nexit 2\n",
        );
        let state = HermesRuntimeState::default();
        let error = chat_with_bin(&bin, dir.path(), &state, test_input(), None, None)
            .await
            .unwrap_err();
        assert_eq!(error.code, "E_HERMES_CHAT");
        assert!(
            error
                .detail
                .unwrap_or_default()
                .contains("provider is not configured")
        );
    }

    /// A fake runtime with the layout `runtime_command` expects. The interpreter
    /// is a shell script that ignores the bootstrap and emits the test output.
    fn fake_runtime(dir: &Path, script: &str) -> PathBuf {
        let bin = dir.join("bin").join("hermes");
        std::fs::create_dir_all(dir.join("bin")).unwrap();
        std::fs::create_dir_all(dir.join("python").join("bin")).unwrap();
        std::fs::create_dir_all(dir.join("site")).unwrap();
        std::fs::write(&bin, "#!/bin/sh\nexit 1\n").unwrap();
        let python = dir.join("python").join("bin").join("python3");
        std::fs::write(&python, script).unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(&python, std::fs::Permissions::from_mode(0o755)).unwrap();
        }
        bin
    }

    #[test]
    fn open_control_never_exposes_mac_tools() {
        let input = HermesChatInput {
            prompt: "hi".into(),
            request_id: "test-request".into(),
            session_id: None,
            provider: "openai".into(),
            model: "gpt".into(),
            effort: "medium".into(),
            fast: false,
            cwd: None,
            attachments: Vec::new(),
            auxiliary: Vec::new(),
            force_aux_vision: false,
            gateway_url: None,
            profile_id: None,
            profile: None,
            panel_auth: false,
            api_key: None,
            endpoint: None,
            mac_enabled: true,
            control_level: "open".into(),
            mac_terminal: true,
            mac_screen: true,
            mac_files: true,
            mac_cron: true,
            confirm_writes: false,
        };
        assert_eq!(toolsets(&input), "safe");
        assert_eq!(
            toolsets(&HermesChatInput {
                control_level: "full".into(),
                ..input.clone()
            }),
            "terminal,file,computer_use,cronjob"
        );
        assert_eq!(
            toolsets(&HermesChatInput {
                control_level: "full".into(),
                mac_cron: false,
                ..input
            }),
            "terminal,file,computer_use"
        );
    }
}
