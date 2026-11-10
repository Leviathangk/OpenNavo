//! Detect local Codex / Claude Code / Copilot logins. Native login processes
//! are managed by `login_session`; detection never returns token values.

use std::path::PathBuf;
use std::process::Command;

use serde::Serialize;
use tauri::AppHandle;

use super::{AppError, hermes_home};

#[derive(Debug, Clone, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesLoginStatus {
    pub codex: bool,
    pub claude_code: bool,
    pub copilot: bool,
    pub opencode: bool,
    pub opencode_go: bool,
    pub nous: bool,
    pub codex_source: Option<String>,
    pub claude_code_source: Option<String>,
    pub copilot_source: Option<String>,
    pub opencode_source: Option<String>,
    pub opencode_go_source: Option<String>,
    pub nous_source: Option<String>,
}

fn home() -> PathBuf {
    std::env::var_os("HOME")
        .map(PathBuf::from)
        .unwrap_or_default()
}

fn codex_file() -> PathBuf {
    std::env::var_os("CODEX_HOME")
        .map(PathBuf::from)
        .unwrap_or_else(|| home().join(".codex"))
        .join("auth.json")
}

fn claude_file() -> PathBuf {
    home().join(".claude").join(".credentials.json")
}

fn copilot_file() -> PathBuf {
    home()
        .join(".config")
        .join("github-copilot")
        .join("hosts.json")
}

fn opencode_auth_path() -> PathBuf {
    home()
        .join(".local")
        .join("share")
        .join("opencode")
        .join("auth.json")
}

fn opencode_key(provider: &str) -> Option<String> {
    let raw = std::fs::read_to_string(opencode_auth_path()).ok()?;
    let value = serde_json::from_str::<serde_json::Value>(&raw).ok()?;
    value
        .get(provider)
        .and_then(|entry| entry.get("key"))
        .and_then(|key| key.as_str())
        .map(str::trim)
        .filter(|key| !key.is_empty())
        .map(str::to_owned)
}

fn has_codex_tokens() -> bool {
    let path = codex_file();
    let Ok(raw) = std::fs::read_to_string(&path) else {
        return false;
    };
    let Ok(value) = serde_json::from_str::<serde_json::Value>(&raw) else {
        return false;
    };
    let tokens = value.get("tokens");
    tokens
        .and_then(|tokens| tokens.get("access_token"))
        .and_then(|token| token.as_str())
        .map(|token| !token.trim().is_empty())
        .unwrap_or(false)
}

fn has_hermes_provider_at(home: &std::path::Path, provider: &str) -> bool {
    let path = home.join("auth.json");
    let Ok(raw) = std::fs::read_to_string(path) else {
        return false;
    };
    let Ok(value) = serde_json::from_str::<serde_json::Value>(&raw) else {
        return false;
    };
    let active = value
        .get("active_provider")
        .and_then(|value| value.as_str())
        == Some(provider);
    let legacy = value
        .get("providers")
        .and_then(|providers| providers.get(provider))
        .is_some_and(|entry| !entry.is_null());
    // Hermes 0.23 stores OAuth logins (Codex, Claude Code, Copilot) in
    // `credential_pool.<provider>`; `providers` only holds the legacy Nous singleton.
    // Checking only `providers` reported a successful `hermes auth add` as failed.
    let pooled = value
        .get("credential_pool")
        .and_then(|pool| pool.get(provider))
        .and_then(|entries| entries.as_array())
        .is_some_and(|entries| {
            entries.iter().any(|entry| {
                entry
                    .get("access_token")
                    .and_then(|token| token.as_str())
                    .is_some_and(|token| !token.trim().is_empty())
            })
        });
    active || legacy || pooled
}

fn claude_keychain_exists() -> bool {
    #[cfg(target_os = "macos")]
    {
        Command::new("security")
            .args(["find-generic-password", "-s", "Claude Code-credentials"])
            .stdout(std::process::Stdio::null())
            .stderr(std::process::Stdio::null())
            .status()
            .map(|status| status.success())
            .unwrap_or(false)
    }
    #[cfg(not(target_os = "macos"))]
    {
        false
    }
}

fn gh_token_exists() -> bool {
    Command::new("gh")
        .args(["auth", "token"])
        .output()
        .map(|output| output.status.success() && !output.stdout.is_empty())
        .unwrap_or(false)
}

pub fn hermes_authenticated(app: &AppHandle, provider: &str) -> bool {
    hermes_home(app)
        .ok()
        .map(|home| has_hermes_provider_at(&home, provider))
        .unwrap_or(false)
}

pub fn status(app: &AppHandle) -> Result<HermesLoginStatus, AppError> {
    let hermes = hermes_home(app).ok();
    let has = |provider: &str| {
        hermes
            .as_ref()
            .map(|home| has_hermes_provider_at(home, provider))
            .unwrap_or(false)
    };
    let codex = has_codex_tokens() || has("openai-codex");
    let claude_code = claude_file().is_file() || claude_keychain_exists() || has("anthropic");
    let copilot = copilot_file().is_file() || gh_token_exists() || has("copilot");
    let opencode = opencode_key("opencode").is_some() || opencode_key("opencode-zen").is_some();
    let opencode_go = opencode_key("opencode-go").is_some();
    let nous = has("nous");
    Ok(HermesLoginStatus {
        codex,
        claude_code,
        copilot,
        opencode,
        opencode_go,
        nous,
        codex_source: codex.then(|| codex_file().display().to_string()),
        claude_code_source: claude_code.then(|| {
            if claude_file().is_file() {
                claude_file().display().to_string()
            } else {
                "macOS Keychain · Claude Code-credentials".into()
            }
        }),
        copilot_source: copilot.then(|| "GitHub CLI / Copilot hosts.json".into()),
        opencode_source: opencode.then(|| opencode_auth_path().display().to_string()),
        opencode_go_source: opencode_go.then(|| opencode_auth_path().display().to_string()),
        nous_source: nous.then(|| {
            hermes_home(app)
                .map(|path| path.display().to_string())
                .unwrap_or_default()
        }),
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn detects_credentials_in_the_pool_and_the_legacy_providers_block() {
        let dir = tempfile::tempdir().unwrap();
        let write = |value: serde_json::Value| {
            std::fs::write(dir.path().join("auth.json"), value.to_string()).unwrap();
        };

        write(serde_json::json!({
            "version": 1,
            "providers": { "nous": { "tokens": {} } },
            "credential_pool": {}
        }));
        assert!(has_hermes_provider_at(dir.path(), "nous"));
        assert!(!has_hermes_provider_at(dir.path(), "copilot"));

        // `hermes auth add copilot` writes a `manual:device_code` pool row, not
        // `providers.copilot`; empty or tokenless rows must not count.
        write(serde_json::json!({
            "version": 1,
            "providers": {},
            "credential_pool": {
                "copilot": [
                    { "id": "abc", "source": "manual:device_code", "access_token": "ghu_token" }
                ],
                "openai-codex": [
                    { "id": "def", "source": "manual:device_code", "access_token": "   " }
                ]
            }
        }));
        assert!(has_hermes_provider_at(dir.path(), "copilot"));
        assert!(!has_hermes_provider_at(dir.path(), "openai-codex"));
    }

    #[test]
    #[ignore = "depends on this Mac's Codex/Claude/Copilot logins"]
    fn detects_local_cli_credentials() {
        assert!(has_codex_tokens(), "Codex CLI auth not detected");
        assert!(
            claude_keychain_exists() || claude_file().is_file(),
            "Claude Code auth not detected"
        );
        assert!(gh_token_exists(), "GitHub CLI auth not detected");
        assert!(
            opencode_key("opencode-go").is_some(),
            "OpenCode Go auth not detected"
        );
    }
}
