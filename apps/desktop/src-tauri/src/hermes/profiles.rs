//! Hermes agent profiles: isolated HERMES_HOME identities inside a connection.

use std::path::{Path, PathBuf};
use std::process::Stdio;

use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use tauri::AppHandle;

use super::{AppError, hermes_home, runtime_bin, runtime_command};

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, Default)]
#[serde(rename_all = "camelCase")]
pub struct HermesAgentProfile {
    pub name: String,
    pub display_name: Option<String>,
    pub path: String,
    pub is_default: bool,
    pub model: Option<String>,
    pub provider: Option<String>,
    pub skill_count: u32,
    pub has_env: bool,
    pub description: Option<String>,
    pub bot_title: Option<String>,
    pub distribution_name: Option<String>,
    pub distribution_version: Option<String>,
}

pub(crate) fn name_ok(name: &str) -> bool {
    let mut chars = name.chars();
    let Some(first) = chars.next() else {
        return false;
    };
    (first.is_ascii_lowercase() || first.is_ascii_digit())
        && name.len() <= 64
        && chars.all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '_' || c == '-')
}

fn profile_path(home: &Path, name: &str) -> PathBuf {
    if name == "default" {
        home.to_path_buf()
    } else {
        home.join("profiles").join(name)
    }
}

fn checked_profile_path(home: &Path, name: &str) -> Result<PathBuf, AppError> {
    if !name_ok(name) {
        return Err(AppError::new(
            "E_HERMES_PROFILE_NAME",
            "Profile names must be lowercase letters, digits, dashes or underscores.",
        ));
    }
    Ok(profile_path(home, name))
}

/// HOME a named profile's config and `.env` actually live in.
///
/// Passing `--profile <name>` (optionally `--isolated`) makes the runtime resolve `HERMES_HOME`
/// to `<root>/profiles/<name>` before it reads `config.yaml` or `.env`. Writing those files to
/// the root instead would overwrite the default profile's model and leave the named profile
/// without the key the user just saved.
pub(crate) fn resolve_home(home: &Path, profile: Option<&str>) -> Result<PathBuf, AppError> {
    match profile
        .map(str::trim)
        .filter(|name| !name.is_empty() && *name != "default")
    {
        Some(name) => checked_profile_path(home, name),
        None => Ok(home.to_path_buf()),
    }
}

/// Files that make a directory under `profiles/` a profile the runtime will serve.
///
/// Mirrors the marker list in `hermes_constants._PROFILE_IDENTITY_MARKERS`: without one of these
/// the runtime refuses `--profile <name>`, so a home that has none of them still needs a
/// `config.yaml` before it can be started.
const PROFILE_IDENTITY_MARKERS: [&str; 6] = [
    "config.yaml",
    ".env",
    "SOUL.md",
    "profile.yaml",
    "auth.json",
    "state.db",
];

pub(crate) fn has_identity(home: &Path) -> bool {
    PROFILE_IDENTITY_MARKERS.iter().any(|marker| {
        let path = home.join(marker);
        path.is_file() || path.is_symlink()
    })
}

/// Whether a profile argument names the root home.
pub(crate) fn is_default_profile(profile: Option<&str>) -> bool {
    matches!(
        profile.map(str::trim).filter(|name| !name.is_empty()),
        None | Some("default")
    )
}

/// Pin a CLI call to the default profile.
///
/// The runtime follows the sticky `<root>/active_profile` file whenever `HERMES_HOME` names the
/// root and no explicit `--profile` was passed, so a `hermes profile use work` in the CLI would
/// silently retarget the default profile's logout, MCP servers, skills, toolsets, cron jobs and
/// OAuth logins. A named profile needs no flag: its `HERMES_HOME` already points inside
/// `profiles/`, which the runtime trusts as-is (and which `--profile` would have to re-resolve).
/// The flag must come before the subcommand, where the runtime's own argv scan finds it.
pub(crate) fn pin_default_profile(command: &mut tokio::process::Command, profile: Option<&str>) {
    if is_default_profile(profile) {
        command.arg("--profile").arg("default");
    }
}

/// Join a panel path onto the saved base URL without dropping its path prefix.
///
/// The panel can be mounted under a sub-path (`https://host/hermes`), so `Url::set_path`
/// used to turn `/api/status` into `https://host/api/status` and break every profile call.
fn panel_url(base: &str, path: &str) -> Result<reqwest::Url, AppError> {
    let base = base.trim_end_matches('/');
    let path = path.trim_start_matches('/');
    reqwest::Url::parse(&format!("{base}/{path}"))
        .map_err(|_| AppError::new("E_HERMES_PROFILE_URL", "Invalid Hermes Panel URL."))
}

fn skill_count(path: &Path) -> u32 {
    let dirs = [path.join("skills"), path.join("plugins").join("skills")];
    dirs.iter()
        .filter_map(|dir| std::fs::read_dir(dir).ok())
        .flat_map(|entries| entries.flatten())
        .filter(|entry| entry.path().is_dir())
        .count() as u32
}

fn yaml_scalar(path: &Path, section: Option<&str>, key: &str) -> Option<String> {
    let raw = std::fs::read_to_string(path).ok()?;
    let mut current: Option<String> = None;
    for line in raw.lines() {
        let trimmed = line.trim();
        if trimmed.is_empty() || trimmed.starts_with('#') {
            continue;
        }
        let indented = line.starts_with(' ') || line.starts_with('\t');
        if !indented && let Some(name) = trimmed.strip_suffix(':') {
            current = Some(name.trim().to_owned());
            continue;
        }
        let expected = section.map_or(!indented, |section| {
            current.as_deref() == Some(section) && indented
        });
        if !expected {
            continue;
        }
        let Some(value) = trimmed.strip_prefix(&format!("{key}:")) else {
            continue;
        };
        let value = value.trim().trim_matches('"').trim_matches('\'').to_owned();
        if !value.is_empty() {
            return Some(value);
        }
    }
    None
}

fn profile_meta(path: &Path) -> (Option<String>, Option<String>, Option<String>) {
    let file = path.join("profile.yaml");
    (
        yaml_scalar(&file, None, "display_name"),
        yaml_scalar(&file, None, "description"),
        yaml_scalar(&file, None, "bot_title"),
    )
}

fn model_meta(path: &Path) -> (Option<String>, Option<String>) {
    let file = path.join("config.yaml");
    (
        yaml_scalar(&file, Some("model"), "default"),
        yaml_scalar(&file, Some("model"), "provider"),
    )
}

fn local_profile(home: &Path, name: &str) -> HermesAgentProfile {
    let path = profile_path(home, name);
    let (display_name, description, bot_title) = profile_meta(&path);
    let (model, provider) = model_meta(&path);
    HermesAgentProfile {
        name: name.to_owned(),
        display_name,
        path: path.display().to_string(),
        is_default: name == "default",
        model,
        provider,
        skill_count: skill_count(&path),
        has_env: path.join(".env").is_file(),
        description,
        bot_title,
        distribution_name: None,
        distribution_version: None,
    }
}

async fn cli(app: &AppHandle, args: &[&str]) -> Result<String, AppError> {
    let Some(bin) = runtime_bin(app) else {
        return Err(AppError::new(
            "E_HERMES_RUNTIME",
            "Install the Hermes runtime before managing profiles.",
        ));
    };
    let home = hermes_home(app)?;
    let mut command = runtime_command(&bin)?;
    // Profile management always acts on the root home: without the flag a sticky
    // `active_profile` would re-home the process into the profile being managed.
    pin_default_profile(&mut command, None);
    let output = command
        .arg("profile")
        .args(args)
        .env("HERMES_HOME", &home)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .output()
        .await
        .map_err(|error| {
            AppError::new("E_HERMES_PROFILE", "Could not run the profile command.")
                .with_detail(error.to_string())
        })?;
    if output.status.success() {
        Ok(String::from_utf8_lossy(&output.stdout).trim().to_owned())
    } else {
        let stderr = String::from_utf8_lossy(&output.stderr).trim().to_owned();
        let stdout = String::from_utf8_lossy(&output.stdout).trim().to_owned();
        Err(
            AppError::new("E_HERMES_PROFILE", "The Hermes profile command failed.")
                .with_detail(if stderr.is_empty() { stdout } else { stderr }),
        )
    }
}

async fn remote_request(
    connection_id: &str,
    _gateway_url: &str,
    method: reqwest::Method,
    path: &str,
    body: Option<Value>,
) -> Result<Value, AppError> {
    if !super::panel::has_session(connection_id) {
        return Err(super::panel::session_missing());
    }
    let url = panel_url(&super::panel::base_url_for(connection_id)?, path)?;
    let client = reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(25))
        .no_proxy()
        .build()
        .map_err(|error| {
            AppError::new(
                "E_HERMES_PROFILE_HTTP",
                "Could not prepare the profile request.",
            )
            .with_detail(error.to_string())
        })?;
    let mut request = client.request(method, url).header(
        reqwest::header::COOKIE,
        super::panel::cookie_header_for(connection_id).await?,
    );
    if let Some(body) = body {
        request = request.json(&body);
    }
    let response = request.send().await.map_err(|error| {
        AppError::new(
            "E_HERMES_PROFILE_HTTP",
            "Could not reach the remote profile API.",
        )
        .with_detail(error.to_string())
    })?;
    let status = response.status();
    let headers = response.headers().clone();
    super::panel::persist_response_cookies(connection_id, &headers)?;
    let value: Value = response.json().await.unwrap_or(Value::Null);
    if !status.is_success() {
        let detail = value
            .get("detail")
            .and_then(Value::as_str)
            .map(str::to_owned)
            .unwrap_or_else(|| value.to_string());
        return Err(remote_status_error(status, detail));
    }
    Ok(value)
}

/// A 401/403 from the profile API is the panel session, not a malformed profile request.
/// Callers use the code to open the sign-in sheet; a model provider's own 401 must never
/// be classified here.
fn remote_status_error(status: reqwest::StatusCode, detail: String) -> AppError {
    if super::panel::session_rejected(status) {
        return super::panel::session_expired(detail);
    }
    AppError::new(
        "E_HERMES_PROFILE_HTTP",
        format!("Remote profile API returned {status}."),
    )
    .with_detail(detail)
}

fn parse_remote_profiles(value: &Value) -> Vec<HermesAgentProfile> {
    value
        .get("profiles")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default()
        .into_iter()
        .filter_map(|item| {
            let name = item.get("name")?.as_str()?.to_owned();
            Some(HermesAgentProfile {
                display_name: item
                    .get("display_name")
                    .and_then(Value::as_str)
                    .filter(|value| !value.trim().is_empty())
                    .map(str::to_owned),
                path: item
                    .get("path")
                    .and_then(Value::as_str)
                    .unwrap_or_default()
                    .to_owned(),
                is_default: item
                    .get("is_default")
                    .and_then(Value::as_bool)
                    .unwrap_or(false),
                model: item.get("model").and_then(Value::as_str).map(str::to_owned),
                provider: item
                    .get("provider")
                    .and_then(Value::as_str)
                    .map(str::to_owned),
                skill_count: item.get("skill_count").and_then(Value::as_u64).unwrap_or(0) as u32,
                has_env: item
                    .get("has_env")
                    .and_then(Value::as_bool)
                    .unwrap_or(false),
                description: item
                    .get("description")
                    .and_then(Value::as_str)
                    .filter(|value| !value.trim().is_empty())
                    .map(str::to_owned),
                bot_title: item
                    .get("bot_title")
                    .and_then(Value::as_str)
                    .filter(|value| !value.trim().is_empty())
                    .map(str::to_owned),
                distribution_name: item
                    .get("distribution_name")
                    .and_then(Value::as_str)
                    .filter(|value| !value.trim().is_empty())
                    .map(str::to_owned),
                distribution_version: item
                    .get("distribution_version")
                    .and_then(Value::as_str)
                    .filter(|value| !value.trim().is_empty())
                    .map(str::to_owned),
                name,
            })
        })
        .collect()
}

#[derive(Debug, Clone, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesConnectionProbe {
    pub ok: bool,
    pub latency_ms: u64,
    pub version: Option<String>,
    pub profiles: u32,
}

fn probe_status_error(status: reqwest::StatusCode) -> AppError {
    if super::panel::session_rejected(status) {
        return super::panel::session_expired(format!("Gateway status returned {status}."));
    }
    AppError::new(
        "E_HERMES_PROBE",
        format!("Gateway status returned {status}."),
    )
}

pub async fn probe(
    connection_id: &str,
    _gateway_url: &str,
) -> Result<HermesConnectionProbe, AppError> {
    let started = std::time::Instant::now();
    if !super::panel::has_session(connection_id) {
        return Err(super::panel::session_missing());
    }
    let url = panel_url(&super::panel::base_url_for(connection_id)?, "/api/status")?;
    let client = reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(12))
        .no_proxy()
        .build()
        .map_err(|error| {
            AppError::new("E_HERMES_PROBE", "Could not prepare the gateway probe.")
                .with_detail(error.to_string())
        })?;
    let response = client
        .get(url)
        .header(
            reqwest::header::COOKIE,
            super::panel::cookie_header_for(connection_id).await?,
        )
        .send()
        .await
        .map_err(|error| {
            AppError::new("E_HERMES_PROBE", "Could not reach the gateway.")
                .with_detail(error.to_string())
        })?;
    let status = response.status();
    let headers = response.headers().clone();
    super::panel::persist_response_cookies(connection_id, &headers)?;
    let value: Value = response.json().await.unwrap_or(Value::Null);
    if !status.is_success() {
        return Err(probe_status_error(status));
    }
    Ok(HermesConnectionProbe {
        ok: true,
        latency_ms: started.elapsed().as_millis() as u64,
        version: value
            .get("version")
            .and_then(Value::as_str)
            .filter(|value| !value.is_empty())
            .map(str::to_owned),
        profiles: value
            .get("gateway_shared_with")
            .and_then(Value::as_array)
            .map(|items| items.len() as u32)
            .unwrap_or(0),
    })
}

pub async fn list(
    app: &AppHandle,
    connection_id: Option<&str>,
    gateway_url: Option<&str>,
) -> Result<Vec<HermesAgentProfile>, AppError> {
    if let (Some(connection_id), Some(gateway_url)) = (connection_id, gateway_url)
        && !gateway_url.trim().is_empty()
    {
        let value = remote_request(
            connection_id,
            gateway_url,
            reqwest::Method::GET,
            "/api/profiles",
            None,
        )
        .await?;
        let mut profiles = parse_remote_profiles(&value);
        profiles.sort_by(|left, right| {
            right
                .is_default
                .cmp(&left.is_default)
                .then_with(|| left.name.cmp(&right.name))
        });
        return Ok(profiles);
    }
    let home = hermes_home(app)?;
    let mut profiles = vec![local_profile(&home, "default")];
    if let Ok(entries) = std::fs::read_dir(home.join("profiles")) {
        profiles.extend(entries.flatten().filter_map(|entry| {
            let name = entry.file_name().to_string_lossy().to_string();
            if entry.path().is_dir() && name_ok(&name) {
                Some(local_profile(&home, &name))
            } else {
                None
            }
        }));
    }
    profiles.sort_by(|left, right| {
        right
            .is_default
            .cmp(&left.is_default)
            .then_with(|| left.name.cmp(&right.name))
    });
    Ok(profiles)
}

pub async fn create(
    app: &AppHandle,
    connection_id: Option<&str>,
    gateway_url: Option<&str>,
    name: &str,
    clone_from: Option<&str>,
    soul: Option<&str>,
) -> Result<(), AppError> {
    let name = name.trim();
    if !name_ok(name) {
        return Err(AppError::new(
            "E_HERMES_PROFILE_NAME",
            "Profile names must be lowercase letters, digits, dashes or underscores.",
        ));
    }
    if let (Some(connection_id), Some(gateway_url)) = (connection_id, gateway_url)
        && !gateway_url.trim().is_empty()
    {
        let mut body = json!({"name": name});
        if let Some(source) = clone_from.filter(|value| !value.is_empty()) {
            body["clone_from"] = Value::String(source.to_owned());
        }
        if let Some(soul) = soul.filter(|value| !value.trim().is_empty()) {
            body["soul"] = Value::String(soul.to_owned());
        }
        remote_request(
            connection_id,
            gateway_url,
            reqwest::Method::POST,
            "/api/profiles",
            Some(body),
        )
        .await?;
        if let Some(soul) = soul.filter(|value| !value.trim().is_empty()) {
            soul_save(app, Some(connection_id), Some(gateway_url), name, soul).await?;
        }
        return Ok(());
    }
    let mut args = vec!["create", name, "--no-alias"];
    if let Some(source) = clone_from.filter(|value| !value.is_empty()) {
        if !name_ok(source) {
            return Err(AppError::new(
                "E_HERMES_PROFILE_NAME",
                "Profile names must be lowercase letters, digits, dashes or underscores.",
            ));
        }
        args.extend(["--clone-from", source]);
    }
    cli(app, &args).await?;
    if let Some(soul) = soul.filter(|value| !value.trim().is_empty()) {
        soul_save(app, connection_id, gateway_url, name, soul).await?;
    }
    Ok(())
}

pub async fn rename(
    app: &AppHandle,
    connection_id: Option<&str>,
    gateway_url: Option<&str>,
    name: &str,
    new_name: &str,
) -> Result<(), AppError> {
    if !name_ok(name) {
        return Err(AppError::new(
            "E_HERMES_PROFILE_NAME",
            "Profile names must be lowercase letters, digits, dashes or underscores.",
        ));
    }
    let new_name = new_name.trim();
    if !name_ok(new_name) {
        return Err(AppError::new(
            "E_HERMES_PROFILE_NAME",
            "Profile names must be lowercase letters, digits, dashes or underscores.",
        ));
    }
    if let (Some(connection_id), Some(gateway_url)) = (connection_id, gateway_url)
        && !gateway_url.trim().is_empty()
    {
        remote_request(
            connection_id,
            gateway_url,
            reqwest::Method::PATCH,
            &format!("/api/profiles/{}", urlencoding::encode(name)),
            Some(json!({"new_name": new_name})),
        )
        .await?;
        return Ok(());
    }
    cli(app, &["rename", name, new_name]).await?;
    Ok(())
}

pub async fn delete(
    app: &AppHandle,
    connection_id: Option<&str>,
    gateway_url: Option<&str>,
    name: &str,
) -> Result<(), AppError> {
    if !name_ok(name) {
        return Err(AppError::new(
            "E_HERMES_PROFILE_NAME",
            "Profile names must be lowercase letters, digits, dashes or underscores.",
        ));
    }
    if name == "default" {
        return Err(AppError::new(
            "E_HERMES_PROFILE_DEFAULT",
            "The default profile cannot be deleted.",
        ));
    }
    if let (Some(connection_id), Some(gateway_url)) = (connection_id, gateway_url)
        && !gateway_url.trim().is_empty()
    {
        remote_request(
            connection_id,
            gateway_url,
            reqwest::Method::DELETE,
            &format!("/api/profiles/{}", urlencoding::encode(name)),
            None,
        )
        .await?;
        return Ok(());
    }
    cli(app, &["delete", "-y", name]).await?;
    Ok(())
}

pub async fn soul(
    app: &AppHandle,
    connection_id: Option<&str>,
    gateway_url: Option<&str>,
    name: &str,
) -> Result<String, AppError> {
    if !name_ok(name) {
        return Err(AppError::new(
            "E_HERMES_PROFILE_NAME",
            "Profile names must be lowercase letters, digits, dashes or underscores.",
        ));
    }
    if let (Some(connection_id), Some(gateway_url)) = (connection_id, gateway_url)
        && !gateway_url.trim().is_empty()
    {
        let value = remote_request(
            connection_id,
            gateway_url,
            reqwest::Method::GET,
            &format!("/api/profiles/{}/soul", urlencoding::encode(name)),
            None,
        )
        .await?;
        return Ok(value
            .get("content")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_owned());
    }
    let home = hermes_home(app)?;
    let path = checked_profile_path(&home, name)?;
    let canonical_home = home.canonicalize().map_err(|error| {
        AppError::new(
            "E_HERMES_PROFILE_SOUL",
            "Could not resolve the Hermes home.",
        )
        .with_detail(error.to_string())
    })?;
    let canonical_path = path.canonicalize().map_err(|error| {
        AppError::new(
            "E_HERMES_PROFILE_SOUL",
            "Could not resolve the profile folder.",
        )
        .with_detail(error.to_string())
    })?;
    if !canonical_path.starts_with(&canonical_home) {
        return Err(AppError::new(
            "E_HERMES_PROFILE_SOUL",
            "The profile folder escapes the Hermes home.",
        ));
    }
    std::fs::read_to_string(canonical_path.join("SOUL.md")).map_err(|error| {
        AppError::new(
            "E_HERMES_PROFILE_SOUL",
            "Could not read the profile SOUL.md.",
        )
        .with_detail(error.to_string())
    })
}

pub async fn soul_save(
    app: &AppHandle,
    connection_id: Option<&str>,
    gateway_url: Option<&str>,
    name: &str,
    content: &str,
) -> Result<(), AppError> {
    if !name_ok(name) {
        return Err(AppError::new(
            "E_HERMES_PROFILE_NAME",
            "Profile names must be lowercase letters, digits, dashes or underscores.",
        ));
    }
    if let (Some(connection_id), Some(gateway_url)) = (connection_id, gateway_url)
        && !gateway_url.trim().is_empty()
    {
        remote_request(
            connection_id,
            gateway_url,
            reqwest::Method::PUT,
            &format!("/api/profiles/{}/soul", urlencoding::encode(name)),
            Some(json!({"content": content})),
        )
        .await?;
        return Ok(());
    }
    let home = hermes_home(app)?;
    let path = checked_profile_path(&home, name)?;
    std::fs::create_dir_all(&path).map_err(|error| {
        AppError::new(
            "E_HERMES_PROFILE_SOUL",
            "Could not create the profile folder.",
        )
        .with_detail(error.to_string())
    })?;
    let canonical_home = home.canonicalize().map_err(|error| {
        AppError::new(
            "E_HERMES_PROFILE_SOUL",
            "Could not resolve the Hermes home.",
        )
        .with_detail(error.to_string())
    })?;
    let canonical_path = path.canonicalize().map_err(|error| {
        AppError::new(
            "E_HERMES_PROFILE_SOUL",
            "Could not resolve the profile folder.",
        )
        .with_detail(error.to_string())
    })?;
    if !canonical_path.starts_with(&canonical_home) {
        return Err(AppError::new(
            "E_HERMES_PROFILE_SOUL",
            "The profile folder escapes the Hermes home.",
        ));
    }
    std::fs::write(canonical_path.join("SOUL.md"), content).map_err(|error| {
        AppError::new(
            "E_HERMES_PROFILE_SOUL",
            "Could not write the profile SOUL.md.",
        )
        .with_detail(error.to_string())
    })?;
    Ok(())
}

pub async fn export(
    app: &AppHandle,
    connection_id: Option<&str>,
    gateway_url: Option<&str>,
    name: &str,
    output: Option<&str>,
) -> Result<String, AppError> {
    if !name_ok(name) {
        return Err(AppError::new(
            "E_HERMES_PROFILE_NAME",
            "Profile names must be lowercase letters, digits, dashes or underscores.",
        ));
    }
    if let (Some(connection_id), Some(gateway_url)) = (connection_id, gateway_url)
        && !gateway_url.trim().is_empty()
    {
        let value = remote_request(
            connection_id,
            gateway_url,
            reqwest::Method::POST,
            &format!("/api/profiles/{}/export", urlencoding::encode(name)),
            Some(json!({"output": output.unwrap_or_default()})),
        )
        .await?;
        return Ok(value
            .get("archive")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_owned());
    }
    if !name_ok(name) {
        return Err(AppError::new(
            "E_HERMES_PROFILE_NAME",
            "Profile names must be lowercase letters, digits, dashes or underscores.",
        ));
    }
    let mut args = vec!["export", name];
    if let Some(output) = output.filter(|value| !value.trim().is_empty()) {
        args.extend(["-o", output]);
    }
    cli(app, &args).await
}

pub async fn import(
    app: &AppHandle,
    connection_id: Option<&str>,
    gateway_url: Option<&str>,
    archive: &str,
    name: Option<&str>,
) -> Result<(), AppError> {
    if let Some(name) = name.filter(|value| !value.trim().is_empty())
        && !name_ok(name)
    {
        return Err(AppError::new(
            "E_HERMES_PROFILE_NAME",
            "Profile names must be lowercase letters, digits, dashes or underscores.",
        ));
    }
    if let (Some(connection_id), Some(gateway_url)) = (connection_id, gateway_url)
        && !gateway_url.trim().is_empty()
    {
        remote_request(
            connection_id,
            gateway_url,
            reqwest::Method::POST,
            "/api/profiles/import",
            Some(json!({"archive": archive, "name": name})),
        )
        .await?;
        return Ok(());
    }
    let mut args = vec!["import", archive];
    if let Some(name) = name.filter(|value| !value.trim().is_empty()) {
        if !name_ok(name) {
            return Err(AppError::new(
                "E_HERMES_PROFILE_NAME",
                "Profile names must be lowercase letters, digits, dashes or underscores.",
            ));
        }
        args.extend(["--name", name]);
    }
    cli(app, &args).await?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn pins_the_default_profile_to_the_root_home() {
        assert!(is_default_profile(None));
        assert!(is_default_profile(Some("  ")));
        assert!(is_default_profile(Some("default")));
        assert!(!is_default_profile(Some("work")));

        // A sticky `active_profile` only applies when no explicit flag was passed, so the
        // default profile has to name itself.
        let mut command = tokio::process::Command::new("hermes");
        pin_default_profile(&mut command, None);
        pin_default_profile(&mut command, Some("default"));
        let args: Vec<_> = command
            .as_std()
            .get_args()
            .map(|arg| arg.to_string_lossy().into_owned())
            .collect();
        assert_eq!(args, ["--profile", "default", "--profile", "default"]);

        let mut named = tokio::process::Command::new("hermes");
        pin_default_profile(&mut named, Some("work"));
        assert_eq!(named.as_std().get_args().count(), 0);
    }

    #[test]
    fn validates_profile_names() {
        assert!(name_ok("default"));
        assert!(name_ok("work-2"));
        assert!(name_ok("a"));
        assert!(!name_ok(""));
        assert!(!name_ok("Work"));
        assert!(!name_ok("-work"));
        assert!(!name_ok("work space"));
        assert!(!name_ok("../work"));
        assert!(!name_ok("/tmp/work"));
        assert!(!name_ok(".."));
    }

    #[test]
    fn rejects_profile_paths_outside_the_home() {
        let root = tempfile::tempdir().unwrap();
        assert!(checked_profile_path(root.path(), "../escape").is_err());
        assert!(checked_profile_path(root.path(), "/tmp/escape").is_err());
        assert!(checked_profile_path(root.path(), "work").is_ok());
    }

    #[test]
    fn resolves_the_effective_home_for_each_profile() {
        let root = tempfile::tempdir().unwrap();
        assert_eq!(resolve_home(root.path(), None).unwrap(), root.path());
        assert_eq!(
            resolve_home(root.path(), Some("default")).unwrap(),
            root.path()
        );
        assert_eq!(
            resolve_home(root.path(), Some(" work ")).unwrap(),
            root.path().join("profiles").join("work")
        );
        assert!(resolve_home(root.path(), Some("../escape")).is_err());
    }

    #[test]
    fn keeps_the_panel_path_prefix_when_building_api_urls() {
        assert_eq!(
            panel_url("https://host/hermes", "/api/profiles")
                .unwrap()
                .as_str(),
            "https://host/hermes/api/profiles"
        );
        assert_eq!(
            panel_url("https://host/hermes/", "api/status")
                .unwrap()
                .as_str(),
            "https://host/hermes/api/status"
        );
        assert_eq!(
            panel_url("https://host", "/api/status").unwrap().as_str(),
            "https://host/api/status"
        );
    }

    #[test]
    fn reads_display_name_and_metadata() {
        let root = tempfile::tempdir().unwrap();
        let profile = root.path().join("profiles").join("work");
        std::fs::create_dir_all(&profile).unwrap();
        std::fs::write(
            profile.join("profile.yaml"),
            "display_name: Research\ndescription: Lab work\nbot_title: Researcher\n",
        )
        .unwrap();
        std::fs::write(
            profile.join("config.yaml"),
            "model:\n  provider: \"nous\"\n  default: \"hermes-4\"\n",
        )
        .unwrap();
        std::fs::write(profile.join(".env"), "OPENAI_API_KEY=x\n").unwrap();
        let info = local_profile(root.path(), "work");
        assert_eq!(info.display_name.as_deref(), Some("Research"));
        assert_eq!(info.description.as_deref(), Some("Lab work"));
        assert_eq!(info.bot_title.as_deref(), Some("Researcher"));
        assert_eq!(info.model.as_deref(), Some("hermes-4"));
        assert_eq!(info.provider.as_deref(), Some("nous"));
        assert!(info.has_env);
        assert!(!info.is_default);
        assert_eq!(info.path, profile.display().to_string());
    }

    #[test]
    fn classifies_expired_panel_credentials_as_auth_errors() {
        let expired = remote_status_error(reqwest::StatusCode::UNAUTHORIZED, "unauthorized".into());
        assert_eq!(expired.code, "E_HERMES_PANEL_AUTH");
        assert_eq!(
            expired.message,
            "Hermes Panel session expired. Sign in again."
        );
        assert_eq!(expired.detail.as_deref(), Some("unauthorized"));

        let failed = remote_status_error(reqwest::StatusCode::INTERNAL_SERVER_ERROR, "boom".into());
        assert_eq!(failed.code, "E_HERMES_PROFILE_HTTP");
        assert_eq!(
            failed.message,
            "Remote profile API returned 500 Internal Server Error."
        );
    }

    #[test]
    fn classifies_probe_auth_failures_with_the_shared_session_error() {
        let expired = probe_status_error(reqwest::StatusCode::FORBIDDEN);
        assert_eq!(expired.code, "E_HERMES_PANEL_AUTH");
        assert_eq!(
            expired.message,
            "Hermes Panel session expired. Sign in again."
        );
        assert_eq!(
            expired.detail.as_deref(),
            Some("Gateway status returned 403 Forbidden.")
        );

        let failed = probe_status_error(reqwest::StatusCode::BAD_GATEWAY);
        assert_eq!(failed.code, "E_HERMES_PROBE");
        assert_eq!(failed.message, "Gateway status returned 502 Bad Gateway.");
        assert!(failed.detail.is_none());
    }

    #[tokio::test]
    #[ignore = "requires HERMES_PANEL_URL, HERMES_PANEL_USER and HERMES_PANEL_PASSWORD"]
    async fn panel_profile_list_uses_the_panel_cookie() {
        let url = std::env::var("HERMES_PANEL_URL").expect("HERMES_PANEL_URL");
        let user = std::env::var("HERMES_PANEL_USER").expect("HERMES_PANEL_USER");
        let password = std::env::var("HERMES_PANEL_PASSWORD").expect("HERMES_PANEL_PASSWORD");
        let session = crate::hermes::panel::login(&url, &user, &password)
            .await
            .unwrap();
        let id = "panel-review-test";
        crate::hermes::panel_session_set(id, &session).unwrap();
        let result = remote_request(
            id,
            &session.base_url,
            reqwest::Method::GET,
            "/api/profiles",
            None,
        )
        .await;
        crate::hermes::panel_session_delete(id).unwrap();
        let value = result.unwrap();
        assert!(
            value
                .get("profiles")
                .and_then(Value::as_array)
                .is_some_and(|profiles| !profiles.is_empty())
        );
    }
}
