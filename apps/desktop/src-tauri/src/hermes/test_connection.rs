//! Lightweight provider authentication probe. It only calls model-listing
//! endpoints, so testing never consumes completion tokens.

use reqwest::header::{AUTHORIZATION, HeaderMap, HeaderValue, USER_AGENT};
use serde::{Deserialize, Serialize};
use tauri::AppHandle;

use super::{AppError, local_opencode_key, login, stored_secret};

#[derive(Debug, Clone, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesTestInput {
    pub provider: String,
    pub api_key: Option<String>,
    pub endpoint: Option<String>,
}

#[derive(Debug, Clone, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesTestResult {
    pub ok: bool,
    pub status: Option<u16>,
    pub message: String,
}

fn resolve_key(input: &HermesTestInput) -> Option<String> {
    input
        .api_key
        .as_deref()
        .map(str::trim)
        .filter(|key| !key.is_empty())
        .map(str::to_owned)
        .or_else(|| stored_secret(&input.provider))
        .or_else(|| local_opencode_key(&input.provider))
}

pub async fn test(app: AppHandle, input: HermesTestInput) -> Result<HermesTestResult, AppError> {
    let key = resolve_key(&input);
    let provider = input.provider.as_str();
    if matches!(provider, "codex" | "claude-code" | "copilot" | "nous") {
        let local = login::status(&app).ok();
        let (hermes_provider, local_detected) = match provider {
            "codex" => (
                "openai-codex",
                local.as_ref().map(|status| status.codex).unwrap_or(false),
            ),
            "claude-code" => (
                "anthropic",
                local
                    .as_ref()
                    .map(|status| status.claude_code)
                    .unwrap_or(false),
            ),
            "copilot" => (
                "copilot",
                local.as_ref().map(|status| status.copilot).unwrap_or(false),
            ),
            "nous" => (
                "nous",
                local.as_ref().map(|status| status.nous).unwrap_or(false),
            ),
            _ => (provider, false),
        };
        let authenticated = login::hermes_authenticated(&app, hermes_provider);
        if authenticated {
            return Ok(HermesTestResult {
                ok: true,
                status: None,
                message: "Hermes 认证已存在；实际模型请求会在对话时验证。".into(),
            });
        }
        if local_detected {
            return Ok(HermesTestResult {
                ok: false,
                status: None,
                message: "检测到本机凭据，但尚未连接 Hermes；请点“登录”完成连接。".into(),
            });
        }
        return Ok(HermesTestResult {
            ok: false,
            status: None,
            message: "未检测到该服务商的本机登录。".into(),
        });
    }
    let (url, mode) = match provider {
        "opencode-go" => (
            "https://opencode.ai/zen/go/v1/models".to_owned(),
            "opencode",
        ),
        "opencode" => ("https://opencode.ai/zen/v1/models".to_owned(), "opencode"),
        "openai" => ("https://api.openai.com/v1/models".to_owned(), "openai"),
        "anthropic" | "claude-code" => (
            "https://api.anthropic.com/v1/models".to_owned(),
            "anthropic",
        ),
        "google" => (
            "https://generativelanguage.googleapis.com/v1beta/models".to_owned(),
            "google",
        ),
        "openrouter" => ("https://openrouter.ai/api/v1/models".to_owned(), "openai"),
        "local" | "custom" => {
            let endpoint = input
                .endpoint
                .as_deref()
                .unwrap_or("")
                .trim()
                .trim_end_matches('/');
            if endpoint.is_empty() {
                return Ok(HermesTestResult {
                    ok: false,
                    status: None,
                    message: "请先填写 Base URL。".into(),
                });
            }
            let base = if endpoint.ends_with("/v1") {
                endpoint.to_owned()
            } else {
                format!("{endpoint}/v1")
            };
            (format!("{base}/models"), "openai")
        }
        _ => {
            return Ok(HermesTestResult {
                ok: false,
                status: None,
                message: "当前服务商暂不支持测试连接。".into(),
            });
        }
    };

    let mut headers = HeaderMap::new();
    headers.insert(USER_AGENT, HeaderValue::from_static("OpenNavo/0.1.29"));
    if mode == "opencode" {
        headers.insert(
            "x-opencode-session",
            HeaderValue::from_static("open-navo-test"),
        );
    }
    if let Some(key) = key.as_deref() {
        if mode == "anthropic" {
            if let Ok(value) = HeaderValue::from_str(key) {
                headers.insert("x-api-key", value);
            }
            headers.insert("anthropic-version", HeaderValue::from_static("2023-06-01"));
        } else if mode != "google"
            && let Ok(value) = HeaderValue::from_str(&format!("Bearer {key}"))
        {
            headers.insert(AUTHORIZATION, value);
        }
    }
    let final_url = if mode == "google" {
        if let Some(key) = key.as_deref() {
            format!("{url}?key={}", urlencoding::encode(key))
        } else {
            url
        }
    } else {
        url
    };
    let response = reqwest::Client::new()
        .get(final_url)
        .headers(headers)
        .timeout(std::time::Duration::from_secs(15))
        .send()
        .await
        .map_err(|error| {
            AppError::new("E_HERMES_TEST", "连接测试失败。").with_detail(error.to_string())
        })?;
    let status = response.status();
    let body = response.text().await.unwrap_or_default();
    if status.is_success() {
        Ok(HermesTestResult {
            ok: true,
            status: Some(status.as_u16()),
            message: format!("连接成功 · HTTP {}", status.as_u16()),
        })
    } else {
        let detail = if body.len() > 240 {
            let end = (0..=240)
                .rev()
                .find(|index| body.is_char_boundary(*index))
                .unwrap_or(0);
            &body[..end]
        } else {
            &body
        };
        Ok(HermesTestResult {
            ok: false,
            status: Some(status.as_u16()),
            message: format!("HTTP {} · {}", status.as_u16(), detail.trim()),
        })
    }
}
