//! Secondary Hermes profile windows.

use crate::AppError;
use tauri::{AppHandle, Manager, WebviewUrl, WebviewWindow, WebviewWindowBuilder};

pub(crate) const PROFILE_WINDOW_PREFIX: &str = "hermes-profile-";

pub(crate) fn valid_profile_name(profile: &str) -> bool {
    !profile.is_empty()
        && profile
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_')
}

/// Open (or focus) the window that owns one Hermes profile. The optional conversation is a
/// notification target: the page selects it once its own history has loaded.
pub(crate) fn open_profile_window(
    app: &AppHandle,
    connection_id: Option<&str>,
    profile: &str,
    conversation_id: Option<&str>,
) -> Result<WebviewWindow, AppError> {
    let profile = profile.trim();
    if !valid_profile_name(profile) {
        return Err(AppError::new(
            "E_HERMES_PROFILE_NAME",
            "Invalid profile name for a new window.",
        ));
    }
    let label = format!("{PROFILE_WINDOW_PREFIX}{profile}");
    if let Some(window) = app.get_webview_window(&label) {
        let _ = window.show();
        let _ = window.set_focus();
        return Ok(window);
    }
    let connection = connection_id
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .unwrap_or("local");
    let mut url = format!(
        "index.html?profile={}&connection={}",
        urlencoding::encode(profile),
        urlencoding::encode(connection)
    );
    if let Some(conversation_id) = conversation_id.map(str::trim).filter(|id| !id.is_empty()) {
        url.push_str("&conversation=");
        url.push_str(&urlencoding::encode(conversation_id));
    }
    url.push_str("#/assistant");
    WebviewWindowBuilder::new(app, label, WebviewUrl::App(url.into()))
        .title(format!("Hermes · {profile}"))
        .inner_size(1200.0, 820.0)
        .min_inner_size(900.0, 640.0)
        .build()
        .map_err(|error| {
            AppError::new("E_HERMES_WINDOW", "Could not open the profile window.")
                .with_detail(error.to_string())
        })
}
