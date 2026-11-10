//! Hermes Agent sidecar discovery, configuration, and one-shot chat execution.
//!
//! Release builds bundle the runtime under `Resources/resources/hermes/`; development
//! builds can point `HERMES_RUNTIME_BIN` at a prepared runtime or use the checked-in
//! resources directory after `pnpm --filter @opennavo/desktop hermes:prepare`.

use serde::{Deserialize, Serialize};
use std::io::Write;
#[cfg(unix)]
use std::os::fd::{AsRawFd, OwnedFd};
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

use crate::AppError;

pub(crate) mod gateway;
pub(crate) mod install;
pub(crate) mod login;
pub(crate) mod login_session;
pub(crate) mod permissions;
pub(crate) mod test_connection;
use gateway::GatewayServer;

#[allow(dead_code)]
const CHAT_TIMEOUT: Duration = Duration::from_secs(180);
const VERSION_TIMEOUT: Duration = Duration::from_secs(15);
const DATA_ROOT_DIR: &str = "OpenNavoData";
const RUNTIME_DIR: &str = "runtime/hermes";
const HERMES_HOME_DIR: &str = "hermes";
const AUDIT_FILE: &str = "audit.jsonl";
const KEYCHAIN_SERVICE: &str = "com.opennavo.desktop.hermes";

#[derive(Debug, Clone, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesRuntimeStatus {
    pub ready: bool,
    pub installed: bool,
    pub version: Option<String>,
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
pub struct HermesChatReply {
    pub text: String,
    pub session_id: Option<String>,
    pub live_session_id: Option<String>,
    pub usage: Option<HermesUsage>,
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
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct HermesChunk {
    pub request_id: String,
    pub delta: String,
}

#[derive(Default)]
pub struct HermesRuntimeState {
    pid: Mutex<Option<u32>>,
    gateway: tokio::sync::Mutex<Option<std::sync::Arc<GatewayServer>>>,
    pub(crate) approval: tokio::sync::Mutex<Option<tokio::sync::mpsc::UnboundedSender<String>>>,
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

fn env_line_defines_key(line: &str, key: &str) -> bool {
    let stripped = line.trim().trim_start_matches('\u{feff}');
    let stripped = stripped
        .strip_prefix("export ")
        .unwrap_or(stripped)
        .trim_start();
    stripped
        .split_once('=')
        .map(|(name, _)| name.trim() == key)
        .unwrap_or(false)
}

const OPENNAVO_CREDENTIAL_SOURCE: &str = "manual:opennavo";
const OPENNAVO_CREDENTIAL_ID: &str = "opennavo";
const AUTH_STORE_VERSION: u64 = 1;
const AUTH_LOCK_TIMEOUT: Duration = Duration::from_secs(15);

/// Environment variables that may carry a provider secret. `.env` is loaded into the
/// gateway's `os.environ` by Hermes (`override=True`), so app-managed key lines must be
/// removed from it; the current build injects the value from an inherited descriptor
/// instead. Exactly the variables `provider_key_env` can write, plus the custom
/// endpoint key. Anything else in `.env` belongs to Hermes or the user and is left alone.
const MANAGED_KEY_ENV_VARS: &[&str] = &[
    "OPENAI_API_KEY",
    "ANTHROPIC_API_KEY",
    "GOOGLE_API_KEY",
    "OPENROUTER_API_KEY",
    "NOUS_API_KEY",
    "GITHUB_TOKEN",
    "OPENCODE_ZEN_API_KEY",
    "OPENCODE_GO_API_KEY",
    "OPENNAVO_CUSTOM_API_KEY",
];

fn read_auth_store(home: &Path) -> Result<serde_json::Value, AppError> {
    let path = home.join("auth.json");
    let bytes = match std::fs::read(&path) {
        Ok(bytes) => bytes,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            return Ok(serde_json::json!({
                "version": AUTH_STORE_VERSION,
                "providers": {},
            }));
        }
        Err(error) => return Err(error.into()),
    };
    match serde_json::from_slice::<serde_json::Value>(&bytes) {
        Ok(value) if value.is_object() => Ok(value),
        _ => {
            // Match Hermes' own corruption handling: preserve the unreadable store and
            // start from an empty one instead of overwriting it silently.
            let _ = std::fs::copy(&path, home.join("auth.json.corrupt"));
            Ok(serde_json::json!({
                "version": AUTH_STORE_VERSION,
                "providers": {},
            }))
        }
    }
}

fn save_auth_store(home: &Path, store: &serde_json::Value) -> Result<(), AppError> {
    std::fs::create_dir_all(home)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        // Hermes keeps credential files under an owner-only directory; match it.
        let _ = std::fs::set_permissions(home, std::fs::Permissions::from_mode(0o700));
    }
    let path = home.join("auth.json");
    let tmp = home.join(".auth.json.opennavo.tmp");
    let mut bytes = serde_json::to_vec_pretty(store)?;
    bytes.push(b'\n');
    {
        let mut options = std::fs::OpenOptions::new();
        options.write(true).create(true).truncate(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let mut file = options.open(&tmp)?;
        file.write_all(&bytes)?;
        file.sync_all()?;
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let _ = std::fs::set_permissions(&tmp, std::fs::Permissions::from_mode(0o600));
    }
    #[cfg(windows)]
    {
        // std::fs::rename does not replace an existing file on Windows.
        let _ = std::fs::remove_file(&path);
    }
    std::fs::rename(&tmp, &path)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let _ = std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600));
    }
    Ok(())
}

fn with_auth_lock<T>(
    home: &Path,
    body: impl FnOnce() -> Result<T, AppError>,
) -> Result<T, AppError> {
    #[cfg(unix)]
    {
        use nix::errno::Errno;
        use nix::fcntl::{Flock, FlockArg};

        std::fs::create_dir_all(home)?;
        let lock_path = home.join("auth.lock");
        let mut options = std::fs::OpenOptions::new();
        options.read(true).write(true).create(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let mut file = options.open(&lock_path)?;
        let deadline = std::time::Instant::now() + AUTH_LOCK_TIMEOUT;
        let guard = loop {
            match Flock::lock(file, FlockArg::LockExclusiveNonblock) {
                Ok(guard) => break guard,
                Err((returned, error)) if error == Errno::EWOULDBLOCK || error == Errno::EAGAIN => {
                    if std::time::Instant::now() >= deadline {
                        return Err(AppError::new(
                            "E_HERMES_CREDENTIAL",
                            "The Hermes credential store is busy. Try again in a moment.",
                        ));
                    }
                    std::thread::sleep(Duration::from_millis(50));
                    file = returned;
                }
                Err((_, error)) => {
                    return Err(AppError::new(
                        "E_HERMES_CREDENTIAL",
                        "Could not lock the Hermes credential store.",
                    )
                    .with_detail(error.to_string()));
                }
            }
        };
        let result = body();
        drop(guard);
        result
    }
    #[cfg(not(unix))]
    {
        body()
    }
}

/// Merge or remove an app-managed credential in `$HERMES_HOME/auth.json`. The app
/// only calls this with `None` now, to scrub rows older builds wrote: every previous
/// app-managed copy and every `env:` copy of a managed key variable is removed so a
/// provider switch cannot leave an old secret behind. Returns true when the store changed.
fn apply_provider_credential(
    store: &mut serde_json::Value,
    provider: &str,
    api_key: Option<&str>,
) -> bool {
    let mut changed = false;
    let root = store
        .as_object_mut()
        .expect("auth store is always an object");
    if root
        .get("version")
        .and_then(serde_json::Value::as_u64)
        .is_none()
    {
        root.insert("version".into(), serde_json::json!(AUTH_STORE_VERSION));
        changed = true;
    }
    if !root
        .get("providers")
        .is_some_and(serde_json::Value::is_object)
    {
        root.insert("providers".into(), serde_json::json!({}));
        changed = true;
    }
    let pool = root
        .entry("credential_pool")
        .or_insert_with(|| serde_json::json!({}));
    if !pool.is_object() {
        *pool = serde_json::json!({});
        changed = true;
    }
    let pool = pool.as_object_mut().expect("credential_pool is an object");

    for (owner, entries) in pool.iter_mut() {
        let Some(entries) = entries.as_array_mut() else {
            continue;
        };
        let before = entries.len();
        entries.retain(|entry| {
            let source = entry
                .get("source")
                .and_then(serde_json::Value::as_str)
                .unwrap_or_default();
            if source == OPENNAVO_CREDENTIAL_SOURCE {
                return false;
            }
            // Hermes can seed a pool row from `.env` or the shell. Those rows are
            // snapshots of a secret that may since have been rotated.
            if let Some(variable) = source.strip_prefix("env:")
                && MANAGED_KEY_ENV_VARS.contains(&variable)
            {
                return false;
            }
            // Older builds could write a literal into config.yaml (always for the
            // custom endpoint); the current provider config never does.
            let stale_config =
                source.starts_with("config:") && (owner == provider || owner == "openavoo-custom");
            !stale_config
        });
        if entries.len() != before {
            changed = true;
        }
    }
    pool.retain(|_, entries| {
        entries
            .as_array()
            .map(|entries| !entries.is_empty())
            .unwrap_or(true)
    });

    if let Some(key) = api_key.map(str::trim).filter(|key| !key.is_empty()) {
        let key = key.replace(['\n', '\r'], "");
        let entry = serde_json::json!({
            "id": OPENNAVO_CREDENTIAL_ID,
            "label": "OpenNavo",
            "auth_type": "api_key",
            "priority": 0,
            "source": OPENNAVO_CREDENTIAL_SOURCE,
            "access_token": key,
        });
        let entries = pool
            .entry(provider.to_owned())
            .or_insert_with(|| serde_json::json!([]))
            .as_array_mut()
            .expect("credential pool entries are arrays");
        entries.push(entry);
        changed = true;
    }
    changed
}

fn write_provider_credential(
    home: &Path,
    provider: &str,
    api_key: Option<&str>,
) -> Result<(), AppError> {
    let mut store = read_auth_store(home)?;
    let before = store.clone();
    apply_provider_credential(&mut store, provider, api_key);
    if store == before {
        return Ok(());
    }
    with_auth_lock(home, || {
        // Re-read under the cross-process lock: Hermes may have refreshed an OAuth
        // grant while we were waiting.
        let mut store = read_auth_store(home)?;
        apply_provider_credential(&mut store, provider, api_key);
        store["updated_at"] = serde_json::json!(chrono::Utc::now().to_rfc3339());
        save_auth_store(home, &store)
    })
}

/// Remove app-managed credential lines from `.env`, which Hermes loads into its
/// process environment on startup. Returns true when the file changed.
fn remove_provider_env(home: &Path) -> Result<bool, AppError> {
    let path = home.join(".env");
    let existing = match std::fs::read_to_string(&path) {
        Ok(existing) => existing,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(false),
        Err(error) => return Err(error.into()),
    };
    let mut kept = String::new();
    let mut removed = false;
    for line in existing.lines() {
        if MANAGED_KEY_ENV_VARS
            .iter()
            .any(|variable| env_line_defines_key(line, variable))
        {
            removed = true;
            continue;
        }
        kept.push_str(line);
        kept.push('\n');
    }
    if !removed {
        return Ok(false);
    }
    if kept.trim().is_empty() {
        match std::fs::remove_file(&path) {
            Ok(()) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(error) => return Err(error.into()),
        }
        return Ok(true);
    }
    let tmp = home.join(".env.opennavo.tmp");
    {
        let mut options = std::fs::OpenOptions::new();
        options.write(true).create(true).truncate(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let mut file = options.open(&tmp)?;
        file.write_all(kept.as_bytes())?;
        file.sync_all()?;
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let _ = std::fs::set_permissions(&tmp, std::fs::Permissions::from_mode(0o600));
    }
    #[cfg(windows)]
    {
        let _ = std::fs::remove_file(&path);
    }
    std::fs::rename(&tmp, &path)?;
    Ok(true)
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

fn runtime_is_current(version: &str) -> bool {
    let Ok(pattern) = regex::Regex::new(r"(\d+)\.(\d+)\.(\d+)") else {
        return false;
    };
    let Some(captures) = pattern.captures(version) else {
        return false;
    };
    let parse = |index: usize| {
        captures
            .get(index)
            .and_then(|value| value.as_str().parse::<u64>().ok())
    };
    matches!((parse(1), parse(2), parse(3)), (Some(0), Some(23), Some(0)))
}

fn reasoning_effort(effort: &str) -> Option<&str> {
    match effort {
        "none" | "minimal" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra" => Some(effort),
        _ => None,
    }
}

fn history_path(app: &AppHandle) -> Result<PathBuf, AppError> {
    Ok(hermes_home(app)?.join("history.json"))
}

pub fn history_load(app: &AppHandle) -> Result<Option<String>, AppError> {
    let path = history_path(app)?;
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

pub fn history_save(app: &AppHandle, json: &str) -> Result<(), AppError> {
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
    let path = history_path(app)?;
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

pub async fn logout(app: &AppHandle, provider: &str) -> Result<(), AppError> {
    let Some(bin) = runtime_bin(app) else {
        return Err(AppError::new(
            "E_HERMES_RUNTIME",
            "Hermes runtime is not installed.",
        ));
    };
    let home = hermes_home(app)?;
    let hermes_provider = provider_id(provider);
    let mut command = runtime_command(&bin)?;
    command
        .arg("auth")
        .arg("logout")
        .arg(hermes_provider)
        .env("HERMES_HOME", &home)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped());
    scrub_credential_env(&mut command);
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
    let home = hermes_home(app)?;
    let mut command = runtime_command(&bin)?;
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
    scrub_credential_env(&mut command);
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
    let home = hermes_home(app)?;
    let mut command = runtime_command(&bin)?;
    command
        .arg("skills")
        .arg(action)
        .arg(identifier)
        .arg("--yes")
        .env("HERMES_HOME", home)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped());
    scrub_credential_env(&mut command);
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

pub async fn tool_action(app: &AppHandle, action: &str, name: &str) -> Result<String, AppError> {
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
    let home = hermes_home(app)?;
    let mut command = runtime_command(&bin)?;
    command
        .arg("tools")
        .arg(action)
        .arg(name)
        .env("HERMES_HOME", home)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped());
    scrub_credential_env(&mut command);
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

pub async fn cron_action(app: &AppHandle, action: &str, job_id: &str) -> Result<String, AppError> {
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
    let home = hermes_home(app)?;
    let mut command = runtime_command(&bin)?;
    command
        .arg("cron")
        .arg(action)
        .arg(job_id)
        .env("HERMES_HOME", home)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped());
    scrub_credential_env(&mut command);
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

pub async fn cli_read(app: &AppHandle, kind: &str) -> Result<String, AppError> {
    let Some(bin) = runtime_bin(app) else {
        return Err(AppError::new(
            "E_HERMES_RUNTIME",
            "Hermes runtime is not installed.",
        ));
    };
    let home = hermes_home(app)?;
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
    command
        .args(args)
        .env("HERMES_HOME", home)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped());
    scrub_credential_env(&mut command);
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

/// Credential-bearing environment variables Hermes or its tools may pick up. The
/// sidecar receives none of them: provider keys are written to the Hermes credential
/// store instead, so a terminal tool cannot read them back from `ps -E` or its own env.
const CREDENTIAL_ENV_VARS: &[&str] = &[
    "OPENAI_API_KEY",
    "OPENAI_API_BASE",
    "OPENAI_BASE_URL",
    "OPENAI_ORG_ID",
    "OPENAI_ORGANIZATION",
    "ANTHROPIC_API_KEY",
    "ANTHROPIC_TOKEN",
    "ANTHROPIC_BASE_URL",
    "CLAUDE_CODE_OAUTH_TOKEN",
    "GOOGLE_API_KEY",
    "GEMINI_API_KEY",
    "OPENROUTER_API_KEY",
    "OPENROUTER_BASE_URL",
    "NOUS_API_KEY",
    "QWEN_API_KEY",
    "DEEPSEEK_API_KEY",
    "MISTRAL_API_KEY",
    "GROQ_API_KEY",
    "TOGETHER_API_KEY",
    "PERPLEXITY_API_KEY",
    "COHERE_API_KEY",
    "FIREWORKS_API_KEY",
    "XAI_API_KEY",
    "HELICONE_API_KEY",
    "PARALLEL_API_KEY",
    "OPENCODE_ZEN_API_KEY",
    "OPENCODE_GO_API_KEY",
    "GITHUB_TOKEN",
    "GH_TOKEN",
    "COPILOT_GITHUB_TOKEN",
    "COPILOT_API_BASE_URL",
    "OPENNAVO_CUSTOM_API_KEY",
];

fn scrub_credential_env(command: &mut Command) {
    for name in CREDENTIAL_ENV_VARS {
        command.env_remove(name);
    }
}

const SECRET_ENV_MARKER: &str = "HERMES_OPENNAVO_SECRET_ENV";
const SECRET_FD_MARKER: &str = "HERMES_OPENNAVO_SECRET_FD";

/// Runs before `hermes_cli` is imported. It reads the provider key from an
/// inherited descriptor and installs an `os.environ` overlay: lookups by name see
/// the key, but iteration, `copy()` and child environments do not. The key never
/// enters the exec environment (so `ps -E` cannot show it) and never touches disk.
const SECRET_BOOTSTRAP: &str = r##"
import os

_name = os.environ.pop("HERMES_OPENNAVO_SECRET_ENV", "")
_fd_text = os.environ.pop("HERMES_OPENNAVO_SECRET_FD", "")
_secret = ""
_fd = -1
try:
    _fd = int(_fd_text)
except (TypeError, ValueError):
    _fd = -1
if _name and _fd >= 0:
    try:
        _parts = []
        while True:
            _chunk = os.read(_fd, 65536)
            if not _chunk:
                break
            _parts.append(_chunk)
        _secret = b"".join(_parts).decode("utf-8", "replace").strip()
    except OSError:
        _secret = ""
    finally:
        try:
            os.close(_fd)
        except OSError:
            pass

if _name and _secret:
    _base = os.environ

    class _OpenNavoSecretEnv:
        """Environment overlay that answers the provider key by name but hides it
        from iteration, copy(), and every child process environment."""

        def __init__(self, base, extra):
            object.__setattr__(self, "_base", base)
            object.__setattr__(self, "_extra", extra)

        def __getitem__(self, key):
            if key in self._extra:
                return self._extra[key]
            return self._base[key]

        def __setitem__(self, key, value):
            if key in self._extra:
                object.__setattr__(self, "_extra", {**self._extra, key: value})
            else:
                self._base[key] = value

        def __delitem__(self, key):
            if key in self._extra:
                object.__setattr__(self, "_extra", {k: v for k, v in self._extra.items() if k != key})
            else:
                del self._base[key]

        def __contains__(self, key):
            return key in self._extra or key in self._base

        def __iter__(self):
            return iter(self.keys())

        def __len__(self):
            return len(self.keys())

        def __repr__(self):
            return repr(self.copy())

        def get(self, key, default=None):
            try:
                return self[key]
            except KeyError:
                return default

        def keys(self):
            return [key for key in self._base.keys() if key not in self._extra]

        def items(self):
            return [(key, self._base[key]) for key in self.keys()]

        def values(self):
            return [self._base[key] for key in self.keys()]

        def copy(self):
            return {key: self._base[key] for key in self.keys()}

        def setdefault(self, key, default=None):
            if key not in self:
                self[key] = default
            return self[key]

        def pop(self, key, *default):
            if key in self._extra:
                value = self._extra[key]
                object.__setattr__(self, "_extra", {k: v for k, v in self._extra.items() if k != key})
                return value
            return self._base.pop(key, *default)

        def popitem(self):
            return self._base.popitem()

        def clear(self):
            object.__setattr__(self, "_extra", {})
            self._base.clear()

        def update(self, *args, **kwargs):
            for key, value in dict(*args, **kwargs).items():
                self[key] = value

        def __ior__(self, other):
            self.update(other)
            return self

    os.environ = _OpenNavoSecretEnv(_base, {_name: _secret})

from hermes_cli.main import main
main()
"##;

/// Provider env var to inject, or `None` for providers that sign in through
/// Hermes' OAuth store (a key would preempt the stored login).
fn secret_env_var(provider: &str) -> Option<&str> {
    if matches!(provider, "nous" | "codex" | "claude-code") {
        return None;
    }
    Some(provider_key_env(provider))
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

/// Build the runtime process directly instead of going through `bin/hermes`, so
/// [`SECRET_BOOTSTRAP`] runs before Hermes imports. The layout matches the
/// generated wrapper (`<root>/python/bin/python3`, `<root>/site`).
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
    command
        .arg("-c")
        .arg(SECRET_BOOTSTRAP)
        .env("PYTHONPATH", pythonpath);
    Ok(command)
}

#[cfg(unix)]
type SecretGuard = OwnedFd;
#[cfg(not(unix))]
type SecretGuard = ();

/// Put `value` on an inherited fd 3 and close the write end. The bootstrap reads
/// it and installs the overlay. The returned read end must stay alive until after
/// `spawn()` so `pre_exec` can duplicate it into the child.
#[cfg(unix)]
fn attach_secret(command: &mut Command, name: &str, value: &str) -> Result<SecretGuard, AppError> {
    let (read, write) = nix::unistd::pipe().map_err(|error| {
        AppError::new(
            "E_HERMES_CREDENTIAL",
            "Could not prepare the provider credential.",
        )
        .with_detail(error.to_string())
    })?;
    let mut write = std::fs::File::from(write);
    write.write_all(value.as_bytes())?;
    drop(write);
    let raw = read.as_raw_fd();
    // Safety: the closure only calls async-signal-safe syscalls and captures an
    // integer; the read end stays open in the parent until after `spawn`.
    unsafe {
        command.pre_exec(move || {
            if raw != 3 && libc::dup2(raw, 3) < 0 {
                return Err(std::io::Error::last_os_error());
            }
            if libc::fcntl(3, libc::F_SETFD, 0) < 0 {
                return Err(std::io::Error::last_os_error());
            }
            Ok(())
        });
    }
    command.env(SECRET_ENV_MARKER, name);
    command.env(SECRET_FD_MARKER, "3");
    Ok(read)
}

#[cfg(not(unix))]
fn attach_secret(command: &mut Command, name: &str, value: &str) -> Result<SecretGuard, AppError> {
    let _ = (command, name, value);
    Err(AppError::new(
        "E_HERMES_CREDENTIAL",
        "Secure credential injection requires macOS.",
    ))
}

fn resolve_provider_secret(input: &HermesChatInput) -> Option<String> {
    if let Some(key) = input
        .api_key
        .as_deref()
        .map(str::trim)
        .filter(|key| !key.is_empty())
    {
        return Some(key.to_owned());
    }
    if let Some(key) = stored_secret(&input.provider) {
        return Some(key);
    }
    if matches!(input.provider.as_str(), "opencode" | "opencode-go")
        && let Some(key) = local_opencode_key(&input.provider)
    {
        return Some(key);
    }
    // These providers sign in through Hermes' OAuth flow; an inherited shell key
    // must not preempt the stored login.
    if !matches!(input.provider.as_str(), "nous" | "codex" | "claude-code")
        && let Ok(value) = std::env::var(provider_key_env(&input.provider))
    {
        let value = value.trim();
        if !value.is_empty() {
            return Some(value.to_owned());
        }
    }
    if input.provider == "copilot"
        && let Ok(output) = std::process::Command::new("gh")
            .args(["auth", "token"])
            .output()
        && output.status.success()
    {
        let token = String::from_utf8_lossy(&output.stdout).trim().to_owned();
        if !token.is_empty() {
            return Some(token);
        }
    }
    None
}

/// Returns the resolved key (used for the gateway fingerprint) and whether an old
/// `.env` was cleaned up. The latter forces a gateway restart so a previously loaded
/// `.env` secret does not stay in the running process environment.
fn write_config(home: &Path, input: &HermesChatInput) -> Result<(Option<String>, bool), AppError> {
    let api_key = resolve_provider_secret(input);
    let provider = provider_id(&input.provider);
    // The key is injected into the runtime through an inherited descriptor and an
    // in-process overlay (see `attach_secret`); it must never be persisted. Scrub
    // app-managed rows older builds wrote to the credential pool.
    write_provider_credential(home, provider, None)?;
    let env_cleaned = remove_provider_env(home)?;
    let mut yaml = String::from("agent:\n");
    if input.force_aux_vision {
        yaml.push_str("  image_input_mode: text\n");
    }
    if let Some(effort) = reasoning_effort(&input.effort) {
        yaml.push_str(&format!("  reasoning_effort: {effort}\n"));
    } else {
        yaml.push_str("  reasoning_effort: \"\"\n");
    }
    yaml.push_str("display:\n  show_reasoning: false\n  streaming: true\n");
    // No credential is written here or to `auth.json`. `env_passthrough` stays
    // empty so no tool can opt a credential into its child environment.
    yaml.push_str("terminal:\n  env_passthrough: []\n  client_env_passthrough: []\n");
    let provider_value =
        serde_json::to_string(provider).unwrap_or_else(|_| "\"openai-api\"".into());
    let model_value = serde_json::to_string(&input.model).unwrap_or_else(|_| "\"\"".into());
    yaml.push_str(&format!(
        "model:\n  provider: {provider_value}\n  default: {model_value}\n"
    ));
    if let Some(endpoint) = input
        .endpoint
        .as_deref()
        .map(str::trim)
        .filter(|value| !value.is_empty())
    {
        let endpoint_value = serde_json::to_string(endpoint).unwrap_or_else(|_| "\"\"".into());
        yaml.push_str(&format!("  base_url: {endpoint_value}\n"));
    }
    let custom_aux: Vec<_> = input
        .auxiliary
        .iter()
        .filter(|entry| entry.provider != "auto" && !entry.model.is_empty())
        .collect();
    if !custom_aux.is_empty() {
        yaml.push_str("auxiliary:\n");
        for entry in custom_aux {
            let provider =
                serde_json::to_string(&entry.provider).unwrap_or_else(|_| "\"auto\"".into());
            let model = serde_json::to_string(&entry.model).unwrap_or_else(|_| "\"\"".into());
            yaml.push_str(&format!(
                "  {}:\n    provider: {}\n    model: {}\n",
                entry.task, provider, model
            ));
        }
    }
    if matches!(input.provider.as_str(), "custom" | "local") {
        let endpoint = input
            .endpoint
            .as_deref()
            .map(str::trim)
            .filter(|value| !value.is_empty())
            .unwrap_or("http://127.0.0.1:11434/v1");
        let endpoint = serde_json::to_string(endpoint).unwrap_or_else(|_| "\"\"".into());
        let model = serde_json::to_string(&input.model).unwrap_or_else(|_| "\"\"".into());
        // `key_env` names the variable the bootstrap overlay answers; the value
        // itself never reaches this file, the process environment, or a tool.
        let key_env = if api_key.is_some() {
            "    key_env: OPENNAVO_CUSTOM_API_KEY\n"
        } else {
            ""
        };
        yaml.push_str(&format!(
            "providers:\n  openavoo-custom:\n    api: {endpoint}\n    transport: chat_completions\n{key_env}    default_model: {model}\n    models:\n      - {model}\n    discover_models: false\n"
        ));
    }
    let config_path = home.join("config.yaml");
    std::fs::write(&config_path, yaml)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let _ = std::fs::set_permissions(&config_path, std::fs::Permissions::from_mode(0o600));
    }
    Ok((api_key, env_cleaned))
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

fn keychain_entry(provider: &str) -> Result<keyring::Entry, AppError> {
    keyring::Entry::new(KEYCHAIN_SERVICE, provider).map_err(|error| {
        AppError::new("E_HERMES_KEYCHAIN", "Could not open the macOS Keychain.")
            .with_detail(error.to_string())
    })
}

pub fn secret_set(provider: &str, key: &str) -> Result<(), AppError> {
    keychain_entry(provider)?
        .set_password(key)
        .map_err(|error| {
            AppError::new("E_HERMES_KEYCHAIN", "Could not save the API key.")
                .with_detail(error.to_string())
        })
}

pub fn secret_status(provider: &str) -> Result<bool, AppError> {
    match keychain_entry(provider)?.get_password() {
        Ok(value) => Ok(!value.is_empty()),
        Err(keyring::Error::NoEntry) => Ok(false),
        Err(error) => Err(
            AppError::new("E_HERMES_KEYCHAIN", "Could not read the API key.")
                .with_detail(error.to_string()),
        ),
    }
}

pub fn secret_delete(provider: &str) -> Result<(), AppError> {
    match keychain_entry(provider)?.delete_credential() {
        Ok(()) => Ok(()),
        Err(keyring::Error::NoEntry) => Ok(()),
        Err(error) => Err(
            AppError::new("E_HERMES_KEYCHAIN", "Could not remove the API key.")
                .with_detail(error.to_string()),
        ),
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

fn stored_secret(provider: &str) -> Option<String> {
    keychain_entry(provider)
        .ok()
        .and_then(|entry| entry.get_password().ok())
        .filter(|value| !value.is_empty())
}

/// Remove provider keys that older builds persisted to `auth.json`/`.env`. The
/// current build injects the key in memory, so a stale on-disk copy must not
/// outlive an upgrade even if the user never sends another message.
async fn scrub_legacy_credentials(app: &AppHandle) {
    let Ok(home) = hermes_home(app) else {
        return;
    };
    let _ = tokio::task::spawn_blocking(move || {
        let _ = write_provider_credential(&home, "", None);
        let _ = remove_provider_env(&home);
    })
    .await;
}

pub async fn status(app: AppHandle) -> Result<HermesRuntimeStatus, AppError> {
    scrub_legacy_credentials(&app).await;
    let Some(bin) = runtime_bin(&app) else {
        log::warn!("Hermes runtime status: no runtime binary found");
        return Ok(HermesRuntimeStatus {
            ready: false,
            installed: false,
            version: None,
            path: None,
            data_path: data_root(&app).ok().map(|path| path.display().to_string()),
            computer_use: false,
            outdated: false,
            message: Some("Hermes runtime is not installed. Install it from the assistant.".into()),
        });
    };
    log::info!("Hermes runtime status: checking {}", bin.display());
    let output = timeout(
        VERSION_TIMEOUT,
        Command::new("/bin/sh").arg(&bin).arg("--version").output(),
    )
    .await
    .map_err(|_| {
        log::warn!("Hermes runtime status: version check timed out");
        AppError::new("E_HERMES_TIMEOUT", "Hermes version check timed out.")
    })?
    .map_err(|error| {
        log::warn!("Hermes runtime status: spawn failed: {error}");
        AppError::new("E_HERMES_RUNTIME", "Hermes runtime could not start.")
            .with_detail(error.to_string())
    })?;
    if !output.status.success() {
        log::warn!(
            "Hermes runtime status: version failed status={:?} stderr={}",
            output.status.code(),
            String::from_utf8_lossy(&output.stderr)
        );
        return Ok(HermesRuntimeStatus {
            ready: false,
            installed: true,
            version: None,
            path: Some(bin.display().to_string()),
            data_path: data_root(&app).ok().map(|path| path.display().to_string()),
            computer_use: false,
            outdated: false,
            message: Some(clean_output(&String::from_utf8_lossy(&output.stderr))),
        });
    }
    let version = clean_output(&String::from_utf8_lossy(&output.stdout))
        .lines()
        .next()
        .unwrap_or_default()
        .to_owned();
    let computer_use = bin
        .parent()
        .and_then(|dir| dir.parent())
        .map(|root| {
            root.join("CuaDriver.app/Contents/MacOS/cua-driver")
                .is_file()
        })
        .unwrap_or(false);
    let outdated = !runtime_is_current(&version);
    log::info!("Hermes runtime status: ready {version} (computer_use={computer_use})");
    Ok(HermesRuntimeStatus {
        ready: true,
        installed: true,
        version: Some(version),
        path: Some(bin.display().to_string()),
        data_path: data_root(&app).ok().map(|path| path.display().to_string()),
        computer_use,
        outdated,
        message: None,
    })
}

pub async fn chat(
    app: AppHandle,
    state: &HermesRuntimeState,
    input: HermesChatInput,
) -> Result<HermesChatReply, AppError> {
    let Some(bin) = runtime_bin(&app) else {
        return Err(AppError::new(
            "E_HERMES_RUNTIME",
            "Hermes runtime is not installed. Install it from the assistant.",
        ));
    };
    let home = hermes_home(&app)?;
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
    let (api_key, _) = write_config(home, &input)?;

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
    scrub_credential_env(&mut command);
    let secret = match (api_key.as_deref(), secret_env_var(&input.provider)) {
        (Some(key), Some(name)) => Some(attach_secret(&mut command, name, key)?),
        _ => None,
    };

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
    drop(secret);
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
    let _ = state.approval.lock().await.take();
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
        assert!(config.contains("provider: \"openai-api\""));
        assert!(config.contains("default: \"gpt-5.4\""));
        assert!(config.contains("auxiliary:"));
        assert!(config.contains("title_generation:"));
        assert!(config.contains("gpt-5.4-mini"));
        assert!(config.contains("image_input_mode: text"));
        assert!(!config.contains("vision:"));
    }

    fn pool_entry<'a>(
        store: &'a serde_json::Value,
        provider: &str,
    ) -> Option<&'a serde_json::Value> {
        store
            .get("credential_pool")?
            .get(provider)?
            .as_array()?
            .iter()
            .find(|entry| {
                entry.get("source").and_then(serde_json::Value::as_str)
                    == Some(OPENNAVO_CREDENTIAL_SOURCE)
            })
    }

    #[test]
    fn keeps_provider_keys_out_of_the_credential_pool() {
        let dir = tempfile::tempdir().unwrap();
        let input = HermesChatInput {
            prompt: "hello".into(),
            request_id: "key-test".into(),
            session_id: None,
            provider: "openai".into(),
            model: "gpt-5.4".into(),
            effort: "medium".into(),
            fast: false,
            api_key: Some("sk-not-on-disk".into()),
            endpoint: None,
            cwd: None,
            attachments: Vec::new(),
            auxiliary: Vec::new(),
            force_aux_vision: false,
            mac_enabled: false,
            control_level: "open".into(),
            mac_terminal: false,
            mac_screen: false,
            mac_files: false,
            mac_cron: false,
            confirm_writes: true,
        };
        let (key, env_cleaned) = write_config(dir.path(), &input).unwrap();
        assert_eq!(key.as_deref(), Some("sk-not-on-disk"));
        assert!(!env_cleaned);
        let config = std::fs::read_to_string(dir.path().join("config.yaml")).unwrap();
        assert!(!config.contains("sk-not-on-disk"));
        assert!(!config.contains("OPENAI_API_KEY"));
        assert!(!dir.path().join(".env").exists());
        if let Ok(bytes) = std::fs::read(dir.path().join("auth.json")) {
            let store: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
            assert!(pool_entry(&store, "openai-api").is_none());
        }
    }

    #[test]
    fn configures_custom_endpoints_without_persisting_the_key() {
        let dir = tempfile::tempdir().unwrap();
        let input = HermesChatInput {
            prompt: "hello".into(),
            request_id: "custom-test".into(),
            session_id: None,
            provider: "custom".into(),
            model: "my-model".into(),
            effort: "medium".into(),
            fast: false,
            api_key: Some("sk-custom-not-on-disk".into()),
            endpoint: Some("https://example.test/v1".into()),
            cwd: None,
            attachments: Vec::new(),
            auxiliary: Vec::new(),
            force_aux_vision: false,
            mac_enabled: false,
            control_level: "open".into(),
            mac_terminal: false,
            mac_screen: false,
            mac_files: false,
            mac_cron: false,
            confirm_writes: true,
        };
        let (key, _) = write_config(dir.path(), &input).unwrap();
        assert_eq!(key.as_deref(), Some("sk-custom-not-on-disk"));
        let config = std::fs::read_to_string(dir.path().join("config.yaml")).unwrap();
        assert!(config.contains("key_env: OPENNAVO_CUSTOM_API_KEY"));
        assert!(!config.contains("sk-custom-not-on-disk"));
        assert!(!config.contains("api_key: \"local\""));
        assert!(!dir.path().join(".env").exists());
        if let Ok(bytes) = std::fs::read(dir.path().join("auth.json")) {
            let store: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
            assert!(pool_entry(&store, "openavoo-custom").is_none());
        }
    }

    #[test]
    fn removes_a_stale_provider_key_from_the_pool() {
        let dir = tempfile::tempdir().unwrap();
        // Call the store writer directly: `write_config` with no key would also consult
        // the real Keychain, which is not available in a unit test.
        write_provider_credential(dir.path(), "openavoo-custom", Some("sk-stale")).unwrap();
        let store: serde_json::Value =
            serde_json::from_slice(&std::fs::read(dir.path().join("auth.json")).unwrap()).unwrap();
        assert!(pool_entry(&store, "openavoo-custom").is_some());

        write_provider_credential(dir.path(), "openavoo-custom", None).unwrap();
        let store: serde_json::Value =
            serde_json::from_slice(&std::fs::read(dir.path().join("auth.json")).unwrap()).unwrap();
        assert!(pool_entry(&store, "openavoo-custom").is_none());
    }

    #[test]
    fn removes_managed_env_keys_without_persisting_them() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(
            dir.path().join(".env"),
            "OPENAI_API_KEY=sk-old\nSOME_SETTING=keep\n",
        )
        .unwrap();
        let input = test_input();
        let (_, env_cleaned) = write_config(dir.path(), &input).unwrap();
        assert!(env_cleaned);
        let env = std::fs::read_to_string(dir.path().join(".env")).unwrap_or_default();
        assert!(!env.contains("OPENAI_API_KEY"));
        assert!(env.contains("SOME_SETTING=keep"));
        if let Ok(bytes) = std::fs::read(dir.path().join("auth.json")) {
            let store: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
            assert!(pool_entry(&store, "openai-api").is_none());
        }
    }

    #[test]
    fn preserves_oauth_state_while_cleaning_stale_secrets() {
        let dir = tempfile::tempdir().unwrap();
        let original = serde_json::json!({
            "version": 1,
            "active_provider": "nous",
            "providers": {
                "nous": {"access_token": "oauth-token"}
            },
            "credential_pool": {
                "nous": [
                    {"id": "nous1", "source": "device_code", "access_token": "oauth-token"}
                ],
                "openai-api": [
                    {"id": "env1", "source": "env:OPENAI_API_KEY", "access_token": "sk-old-env"}
                ],
                "openavoo-custom": [
                    {"id": "cfg1", "source": "config:openavoo-custom", "access_token": "sk-old-config"}
                ]
            }
        });
        std::fs::write(
            dir.path().join("auth.json"),
            serde_json::to_vec_pretty(&original).unwrap(),
        )
        .unwrap();

        write_provider_credential(dir.path(), "openai-api", Some("sk-new")).unwrap();
        let store: serde_json::Value =
            serde_json::from_slice(&std::fs::read(dir.path().join("auth.json")).unwrap()).unwrap();
        assert_eq!(store["providers"]["nous"]["access_token"], "oauth-token");
        assert_eq!(store["credential_pool"]["nous"][0]["source"], "device_code");
        assert_eq!(
            pool_entry(&store, "openai-api")
                .and_then(|entry| entry.get("access_token"))
                .and_then(serde_json::Value::as_str),
            Some("sk-new")
        );
        assert!(store["credential_pool"].get("openavoo-custom").is_none());
        assert_eq!(store["active_provider"], "nous");
    }

    #[test]
    fn removes_app_managed_keys_from_other_providers() {
        let dir = tempfile::tempdir().unwrap();
        let mut input = test_input();
        input.api_key = Some("sk-openai".into());
        write_config(dir.path(), &input).unwrap();

        input.provider = "custom".into();
        input.endpoint = Some("https://example.test/v1".into());
        input.api_key = Some("sk-custom".into());
        write_config(dir.path(), &input).unwrap();

        if let Ok(bytes) = std::fs::read(dir.path().join("auth.json")) {
            let store: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
            assert!(pool_entry(&store, "openai-api").is_none());
            assert!(pool_entry(&store, "openavoo-custom").is_none());
        }
    }

    #[test]
    fn oauth_providers_do_not_receive_a_secret_env_var() {
        assert_eq!(secret_env_var("nous"), None);
        assert_eq!(secret_env_var("codex"), None);
        assert_eq!(secret_env_var("claude-code"), None);
        assert_eq!(secret_env_var("openai"), Some("OPENAI_API_KEY"));
        assert_eq!(secret_env_var("custom"), Some("OPENNAVO_CUSTOM_API_KEY"));
        assert_eq!(secret_env_var("copilot"), Some("GITHUB_TOKEN"));
    }

    #[test]
    fn secret_bootstrap_is_valid_python() {
        let Ok(output) = std::process::Command::new("python3")
            .arg("-c")
            .arg(format!(
                "compile({:?}, '<bootstrap>', 'exec')",
                SECRET_BOOTSTRAP
            ))
            .output()
        else {
            return;
        };
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn passes_the_secret_on_fd_three() {
        let mut command = Command::new("/bin/sh");
        command.args(["-c", "cat <&3"]);
        let guard = attach_secret(&mut command, "OPENNAVO_TEST_KEY", "sk-fd-secret").unwrap();
        let output = command.output().await.unwrap();
        drop(guard);
        assert!(output.status.success());
        assert_eq!(String::from_utf8_lossy(&output.stdout), "sk-fd-secret");
    }

    #[test]
    fn maps_reasoning_effort() {
        assert_eq!(reasoning_effort("auto"), None);
        assert_eq!(reasoning_effort("medium"), Some("medium"));
        assert_eq!(reasoning_effort("high"), Some("high"));
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
