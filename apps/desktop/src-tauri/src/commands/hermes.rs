use std::sync::Arc;
use tauri::{Manager, State};

use crate::AppError;
use crate::hermes::{
    self, AskAnswer, HermesAskReply, HermesChatInput, HermesChatReply, HermesRuntimeState,
    HermesRuntimeStatus,
};

#[tauri::command]
#[specta::specta]
pub async fn hermes_runtime_status(app: tauri::AppHandle) -> Result<HermesRuntimeStatus, AppError> {
    hermes::status(app).await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_chat(
    app: tauri::AppHandle,
    state: State<'_, Arc<HermesRuntimeState>>,
    input: HermesChatInput,
) -> Result<HermesChatReply, AppError> {
    hermes::chat(app, state.inner(), input).await
}

#[tauri::command]
#[specta::specta]
pub fn hermes_notify_reply(
    app: tauri::AppHandle,
    window: tauri::WebviewWindow,
    conversation_id: String,
    title: String,
    body: String,
) -> Result<(), AppError> {
    let notifications = app
        .try_state::<Arc<crate::notifications::NativeNotifications>>()
        .ok_or_else(|| AppError::new("E_UNKNOWN", "notification_unavailable"))?;
    notifications.hermes_reply(&window, &conversation_id, title, body)
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_stop(state: State<'_, Arc<HermesRuntimeState>>) -> Result<(), AppError> {
    hermes::stop(state.inner()).await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_ask_respond(
    state: State<'_, Arc<HermesRuntimeState>>,
    request_id: String,
    reply: HermesAskReply,
) -> Result<(), AppError> {
    let sender = state.asks.lock().await.clone();
    let Some(sender) = sender else {
        return Err(AppError::new(
            "E_HERMES_ASK",
            "No Hermes request is waiting on an answer.",
        ));
    };
    sender.send(AskAnswer { request_id, reply }).map_err(|_| {
        AppError::new(
            "E_HERMES_ASK",
            "The Hermes turn that asked is no longer running.",
        )
    })
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_interrupt(
    state: State<'_, Arc<HermesRuntimeState>>,
    live_session_id: String,
    gateway_url: Option<String>,
    profile_id: Option<String>,
) -> Result<(), AppError> {
    if gateway_url
        .as_deref()
        .is_some_and(|value| !value.trim().is_empty())
    {
        let id = profile_id.as_deref().unwrap_or_default();
        if hermes::panel::has_session(id) {
            let ticket = hermes::panel::ticket_for(id).await?;
            let query = format!("ticket={}", urlencoding::encode(&ticket));
            let panel_url = hermes::panel::base_url_for(id)?;
            return hermes::gateway::interrupt_remote_auth(&panel_url, &query, &live_session_id)
                .await;
        }
        return Err(hermes::remote_auth_required());
    }
    hermes::gateway::interrupt(state.inner(), &live_session_id).await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_steer(
    state: State<'_, Arc<HermesRuntimeState>>,
    live_session_id: String,
    text: String,
    gateway_url: Option<String>,
    profile_id: Option<String>,
) -> Result<(), AppError> {
    if gateway_url
        .as_deref()
        .is_some_and(|value| !value.trim().is_empty())
    {
        let id = profile_id.as_deref().unwrap_or_default();
        if hermes::panel::has_session(id) {
            let ticket = hermes::panel::ticket_for(id).await?;
            let query = format!("ticket={}", urlencoding::encode(&ticket));
            let panel_url = hermes::panel::base_url_for(id)?;
            return hermes::gateway::steer_remote_auth(&panel_url, &query, &live_session_id, &text)
                .await;
        }
        return Err(hermes::remote_auth_required());
    }
    hermes::gateway::steer(state.inner(), &live_session_id, &text).await
}

#[tauri::command]
#[specta::specta]
pub fn hermes_secret_set(
    app: tauri::AppHandle,
    provider: String,
    key: String,
    profile: Option<String>,
) -> Result<(), AppError> {
    hermes::secret_set(&app, &provider, &key, profile.as_deref())
}

#[tauri::command]
#[specta::specta]
pub fn hermes_secret_status(
    app: tauri::AppHandle,
    provider: String,
    profile: Option<String>,
) -> Result<bool, AppError> {
    hermes::secret_status(&app, &provider, profile.as_deref())
}

#[tauri::command]
#[specta::specta]
pub fn hermes_secret_delete(
    app: tauri::AppHandle,
    provider: String,
    profile: Option<String>,
) -> Result<(), AppError> {
    hermes::secret_delete(&app, &provider, profile.as_deref())
}

#[tauri::command]
#[specta::specta]
pub fn hermes_audit_read(
    app: tauri::AppHandle,
    limit: usize,
) -> Result<Vec<crate::hermes::HermesAuditEntry>, AppError> {
    crate::hermes::audit_entries(&app, limit)
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_mcp_action(
    app: tauri::AppHandle,
    action: String,
    name: String,
    url: String,
    profile: Option<String>,
) -> Result<String, AppError> {
    crate::hermes::mcp_action(&app, &action, &name, &url, profile.as_deref()).await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_skill_action(
    app: tauri::AppHandle,
    action: String,
    identifier: String,
    profile: Option<String>,
) -> Result<String, AppError> {
    crate::hermes::skill_action(&app, &action, &identifier, profile.as_deref()).await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_tool_action(
    app: tauri::AppHandle,
    action: String,
    name: String,
    profile: Option<String>,
) -> Result<String, AppError> {
    crate::hermes::tool_action(&app, &action, &name, profile.as_deref()).await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_cron_action(
    app: tauri::AppHandle,
    action: String,
    job_id: String,
    profile: Option<String>,
) -> Result<String, AppError> {
    crate::hermes::cron_action(&app, &action, &job_id, profile.as_deref()).await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_cli_read(
    app: tauri::AppHandle,
    kind: String,
    profile: Option<String>,
) -> Result<String, AppError> {
    crate::hermes::cli_read(&app, &kind, profile.as_deref()).await
}

#[tauri::command]
#[specta::specta]
pub fn hermes_logs_open(app: tauri::AppHandle) -> Result<(), AppError> {
    crate::hermes::open_logs(&app)
}

#[tauri::command]
#[specta::specta]
pub fn hermes_audit_open(app: tauri::AppHandle) -> Result<(), AppError> {
    hermes::open_audit(&app)
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_runtime_install(
    app: tauri::AppHandle,
    state: State<'_, Arc<HermesRuntimeState>>,
) -> Result<HermesRuntimeStatus, AppError> {
    hermes::install::install(app, state.inner()).await
}

#[tauri::command]
#[specta::specta]
pub fn hermes_runtime_install_cancel(
    state: State<'_, Arc<HermesRuntimeState>>,
) -> Result<(), AppError> {
    crate::hermes::install::cancel(state.inner());
    Ok(())
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_runtime_uninstall(
    app: tauri::AppHandle,
    state: State<'_, Arc<HermesRuntimeState>>,
) -> Result<HermesRuntimeStatus, AppError> {
    hermes::install::uninstall(app, state.inner()).await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_logout(
    app: tauri::AppHandle,
    provider: String,
    profile: Option<String>,
) -> Result<(), AppError> {
    crate::hermes::logout(&app, &provider, profile.as_deref()).await
}

#[tauri::command]
#[specta::specta]
pub fn hermes_history_load(
    app: tauri::AppHandle,
    profile: Option<String>,
) -> Result<Option<String>, AppError> {
    crate::hermes::history_load(&app, profile.as_deref())
}

#[tauri::command]
#[specta::specta]
pub fn hermes_history_save(
    app: tauri::AppHandle,
    json: String,
    profile: Option<String>,
) -> Result<(), AppError> {
    crate::hermes::history_save(&app, &json, profile.as_deref())
}

#[tauri::command]
#[specta::specta]
pub fn hermes_attachment_preview(path: String) -> Result<Option<String>, AppError> {
    crate::hermes::attachment_preview(&path)
}

#[tauri::command]
#[specta::specta]
pub fn hermes_clipboard_image(app: tauri::AppHandle) -> Result<Option<String>, AppError> {
    crate::hermes::clipboard_image(&app)
}

#[tauri::command]
#[specta::specta]
pub fn hermes_attachment_info(paths: Vec<String>) -> Vec<crate::hermes::HermesAttachmentInfo> {
    crate::hermes::attachment_info(paths)
}

#[tauri::command]
#[specta::specta]
pub fn hermes_permissions_request_screen() -> bool {
    crate::hermes::permissions::request_screen_capture()
}

#[tauri::command]
#[specta::specta]
pub fn hermes_permissions_status(
    app: tauri::AppHandle,
) -> Result<crate::hermes::permissions::HermesPermissions, AppError> {
    crate::hermes::permissions::status(&app)
}

#[tauri::command]
#[specta::specta]
pub fn hermes_login_status(
    app: tauri::AppHandle,
    profile: Option<String>,
) -> Result<crate::hermes::login::HermesLoginStatus, AppError> {
    crate::hermes::login::status(&app, profile.as_deref())
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_agent_profiles(
    app: tauri::AppHandle,
    connection_id: Option<String>,
    gateway_url: Option<String>,
) -> Result<Vec<crate::hermes::profiles::HermesAgentProfile>, AppError> {
    crate::hermes::profiles::list(&app, connection_id.as_deref(), gateway_url.as_deref()).await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_agent_profile_create(
    app: tauri::AppHandle,
    connection_id: Option<String>,
    gateway_url: Option<String>,
    name: String,
    clone_from: Option<String>,
    soul: Option<String>,
) -> Result<(), AppError> {
    crate::hermes::profiles::create(
        &app,
        connection_id.as_deref(),
        gateway_url.as_deref(),
        &name,
        clone_from.as_deref(),
        soul.as_deref(),
    )
    .await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_agent_profile_rename(
    app: tauri::AppHandle,
    connection_id: Option<String>,
    gateway_url: Option<String>,
    name: String,
    new_name: String,
) -> Result<(), AppError> {
    crate::hermes::profiles::rename(
        &app,
        connection_id.as_deref(),
        gateway_url.as_deref(),
        &name,
        &new_name,
    )
    .await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_agent_profile_delete(
    app: tauri::AppHandle,
    connection_id: Option<String>,
    gateway_url: Option<String>,
    name: String,
) -> Result<(), AppError> {
    crate::hermes::profiles::delete(
        &app,
        connection_id.as_deref(),
        gateway_url.as_deref(),
        &name,
    )
    .await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_agent_profile_soul(
    app: tauri::AppHandle,
    connection_id: Option<String>,
    gateway_url: Option<String>,
    name: String,
) -> Result<String, AppError> {
    crate::hermes::profiles::soul(
        &app,
        connection_id.as_deref(),
        gateway_url.as_deref(),
        &name,
    )
    .await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_agent_profile_soul_save(
    app: tauri::AppHandle,
    connection_id: Option<String>,
    gateway_url: Option<String>,
    name: String,
    content: String,
) -> Result<(), AppError> {
    crate::hermes::profiles::soul_save(
        &app,
        connection_id.as_deref(),
        gateway_url.as_deref(),
        &name,
        &content,
    )
    .await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_connection_probe(
    connection_id: String,
    gateway_url: String,
) -> Result<crate::hermes::profiles::HermesConnectionProbe, AppError> {
    crate::hermes::profiles::probe(&connection_id, &gateway_url).await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_open_profile_window(
    app: tauri::AppHandle,
    connection_id: Option<String>,
    profile: String,
) -> Result<(), AppError> {
    crate::hermes::window::open_profile_window(&app, connection_id.as_deref(), &profile, None)?;
    Ok(())
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_agent_profile_export(
    app: tauri::AppHandle,
    connection_id: Option<String>,
    gateway_url: Option<String>,
    name: String,
    output: Option<String>,
) -> Result<String, AppError> {
    crate::hermes::profiles::export(
        &app,
        connection_id.as_deref(),
        gateway_url.as_deref(),
        &name,
        output.as_deref(),
    )
    .await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_agent_profile_import(
    app: tauri::AppHandle,
    connection_id: Option<String>,
    gateway_url: Option<String>,
    archive: String,
    name: Option<String>,
) -> Result<(), AppError> {
    crate::hermes::profiles::import(
        &app,
        connection_id.as_deref(),
        gateway_url.as_deref(),
        &archive,
        name.as_deref(),
    )
    .await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_panel_login(
    profile_id: String,
    base_url: String,
    username: String,
    password: String,
) -> Result<crate::hermes::panel::PanelLoginResult, AppError> {
    let session = crate::hermes::panel::login(&base_url, &username, &password).await?;
    crate::hermes::panel_session_set(&profile_id, &session)?;
    Ok(crate::hermes::panel::PanelLoginResult {
        base_url: session.base_url,
        provider: session.provider,
    })
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_panel_logout(profile_id: String) -> Result<(), AppError> {
    if let Ok(session) = crate::hermes::panel_session(&profile_id) {
        crate::hermes::panel::logout(&session).await?;
    }
    crate::hermes::panel_session_delete(&profile_id)
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_panel_sessions(
    profile_id: String,
    profile: Option<String>,
    limit: usize,
    offset: usize,
) -> Result<String, AppError> {
    crate::hermes::panel::sessions(&profile_id, profile.as_deref(), limit, offset).await
}

/// One history slice (recents, gateways, scheduled, archived), optionally aggregated across
/// every profile on the host.
#[tauri::command]
#[specta::specta]
pub async fn hermes_panel_history(
    profile_id: String,
    profile: Option<String>,
    all_profiles: bool,
    slice: String,
    limit: usize,
    offset: usize,
) -> Result<String, AppError> {
    crate::hermes::panel::history(
        &profile_id,
        profile.as_deref(),
        all_profiles,
        &slice,
        limit,
        offset,
    )
    .await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_panel_session_messages(
    profile_id: String,
    session_id: String,
    profile: Option<String>,
    limit: usize,
    offset: usize,
) -> Result<String, AppError> {
    crate::hermes::panel::messages(&profile_id, &session_id, profile.as_deref(), limit, offset)
        .await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_panel_session_rename(
    profile_id: String,
    session_id: String,
    title: String,
    expected_revision: i64,
    expected_generation: i64,
    profile: Option<String>,
) -> Result<(), AppError> {
    crate::hermes::panel::rename(
        &profile_id,
        profile.as_deref(),
        &session_id,
        &title,
        expected_revision,
        expected_generation,
    )
    .await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_panel_session_delete(
    profile_id: String,
    session_id: String,
    expected_revision: i64,
    expected_generation: i64,
    profile: Option<String>,
) -> Result<(), AppError> {
    crate::hermes::panel::delete(
        &profile_id,
        profile.as_deref(),
        &session_id,
        expected_revision,
        expected_generation,
    )
    .await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_panel_session_archive(
    profile_id: String,
    session_id: String,
    archived: bool,
    expected_revision: i64,
    expected_generation: i64,
    profile: Option<String>,
) -> Result<(), AppError> {
    crate::hermes::panel::set_archived(
        &profile_id,
        profile.as_deref(),
        &session_id,
        archived,
        expected_revision,
        expected_generation,
    )
    .await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_model_options(
    app: tauri::AppHandle,
    state: State<'_, Arc<HermesRuntimeState>>,
    input: crate::hermes::HermesModelOptionsInput,
) -> Result<crate::hermes::HermesModelOptions, AppError> {
    if input
        .gateway_url
        .as_deref()
        .is_some_and(|value| !value.trim().is_empty())
    {
        let profile_id = input.profile_id.as_deref().unwrap_or_default().to_owned();
        if input.panel_auth {
            let ticket = crate::hermes::panel::ticket_for(&profile_id).await?;
            let query = format!("ticket={}", urlencoding::encode(&ticket));
            let panel_url = crate::hermes::panel::base_url_for(&profile_id)?;
            return crate::hermes::gateway::model_options_remote_auth(
                &panel_url,
                &query,
                input.profile.as_deref(),
                input.refresh,
                input.include_unconfigured,
            )
            .await;
        }
        return Err(crate::hermes::remote_auth_required());
    }
    let bin = {
        let home = crate::hermes::hermes_home(&app)?;
        (crate::hermes::runtime_bin(&app), home)
    };
    let (Some(bin), home) = bin else {
        return Err(AppError::new(
            "E_HERMES_RUNTIME",
            "Install the Hermes runtime to load the model catalog.",
        ));
    };
    let chat = crate::hermes::HermesChatInput {
        prompt: String::new(),
        request_id: "model-options".into(),
        session_id: None,
        provider: input.provider,
        model: input.model,
        effort: input.effort,
        fast: input.fast,
        cwd: None,
        attachments: Vec::new(),
        auxiliary: Vec::new(),
        force_aux_vision: false,
        gateway_url: input.gateway_url,
        profile_id: input.profile_id,
        profile: input.profile,
        panel_auth: input.panel_auth,
        api_key: input.api_key,
        endpoint: input.endpoint,
        mac_enabled: false,
        control_level: "open".into(),
        mac_terminal: false,
        mac_screen: false,
        mac_files: false,
        mac_cron: false,
        confirm_writes: false,
    };
    crate::hermes::gateway::model_options(
        &bin,
        &home,
        state.inner(),
        chat,
        input.refresh,
        input.include_unconfigured,
    )
    .await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_test_connection(
    app: tauri::AppHandle,
    input: crate::hermes::test_connection::HermesTestInput,
) -> Result<crate::hermes::test_connection::HermesTestResult, AppError> {
    crate::hermes::test_connection::test(app, input).await
}

#[tauri::command]
#[specta::specta]
pub fn hermes_login_start(
    app: tauri::AppHandle,
    state: State<'_, std::sync::Arc<crate::hermes::login_session::HermesLoginState>>,
    provider: String,
    profile: Option<String>,
) -> Result<(), AppError> {
    crate::hermes::login_session::start(&app, state.inner().clone(), &provider, profile.as_deref())
}

#[tauri::command]
#[specta::specta]
pub fn hermes_login_input(
    state: State<'_, std::sync::Arc<crate::hermes::login_session::HermesLoginState>>,
    text: String,
) -> Result<(), AppError> {
    crate::hermes::login_session::input(state.inner(), &text)
}

#[tauri::command]
#[specta::specta]
pub fn hermes_login_cancel(
    state: State<'_, std::sync::Arc<crate::hermes::login_session::HermesLoginState>>,
) -> Result<(), AppError> {
    crate::hermes::login_session::cancel(state.inner());
    Ok(())
}
