use std::sync::Arc;
use tauri::State;

use crate::AppError;
use crate::hermes::{
    self, HermesChatInput, HermesChatReply, HermesRuntimeState, HermesRuntimeStatus,
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
pub async fn hermes_stop(state: State<'_, Arc<HermesRuntimeState>>) -> Result<(), AppError> {
    hermes::stop(state.inner()).await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_approval_respond(
    state: State<'_, Arc<HermesRuntimeState>>,
    choice: String,
) -> Result<(), AppError> {
    let sender = state.approval.lock().await.clone();
    let Some(sender) = sender else {
        return Err(AppError::new(
            "E_HERMES_APPROVAL",
            "No approval request is pending.",
        ));
    };
    sender.send(choice).map_err(|_| {
        AppError::new(
            "E_HERMES_APPROVAL",
            "The approval request is no longer active.",
        )
    })
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_interrupt(
    state: State<'_, Arc<HermesRuntimeState>>,
    live_session_id: String,
) -> Result<(), AppError> {
    hermes::gateway::interrupt(state.inner(), &live_session_id).await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_steer(
    state: State<'_, Arc<HermesRuntimeState>>,
    live_session_id: String,
    text: String,
) -> Result<(), AppError> {
    hermes::gateway::steer(state.inner(), &live_session_id, &text).await
}

#[tauri::command]
#[specta::specta]
pub fn hermes_secret_set(provider: String, key: String) -> Result<(), AppError> {
    hermes::secret_set(&provider, &key)
}

#[tauri::command]
#[specta::specta]
pub fn hermes_secret_status(provider: String) -> Result<bool, AppError> {
    hermes::secret_status(&provider)
}

#[tauri::command]
#[specta::specta]
pub fn hermes_secret_delete(provider: String) -> Result<(), AppError> {
    hermes::secret_delete(&provider)
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
) -> Result<String, AppError> {
    crate::hermes::mcp_action(&app, &action, &name, &url).await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_skill_action(
    app: tauri::AppHandle,
    action: String,
    identifier: String,
) -> Result<String, AppError> {
    crate::hermes::skill_action(&app, &action, &identifier).await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_tool_action(
    app: tauri::AppHandle,
    action: String,
    name: String,
) -> Result<String, AppError> {
    crate::hermes::tool_action(&app, &action, &name).await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_cron_action(
    app: tauri::AppHandle,
    action: String,
    job_id: String,
) -> Result<String, AppError> {
    crate::hermes::cron_action(&app, &action, &job_id).await
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_cli_read(app: tauri::AppHandle, kind: String) -> Result<String, AppError> {
    crate::hermes::cli_read(&app, &kind).await
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
pub async fn hermes_logout(app: tauri::AppHandle, provider: String) -> Result<(), AppError> {
    crate::hermes::logout(&app, &provider).await
}

#[tauri::command]
#[specta::specta]
pub fn hermes_history_load(app: tauri::AppHandle) -> Result<Option<String>, AppError> {
    crate::hermes::history_load(&app)
}

#[tauri::command]
#[specta::specta]
pub fn hermes_history_save(app: tauri::AppHandle, json: String) -> Result<(), AppError> {
    crate::hermes::history_save(&app, &json)
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
) -> Result<crate::hermes::login::HermesLoginStatus, AppError> {
    crate::hermes::login::status(&app)
}

#[tauri::command]
#[specta::specta]
pub async fn hermes_model_options(
    app: tauri::AppHandle,
    state: State<'_, Arc<HermesRuntimeState>>,
    input: crate::hermes::HermesModelOptionsInput,
) -> Result<crate::hermes::HermesModelOptions, AppError> {
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
) -> Result<(), AppError> {
    crate::hermes::login_session::start(&app, state.inner().clone(), &provider)
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
