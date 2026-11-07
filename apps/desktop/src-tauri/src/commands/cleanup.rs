use crate::{AppError, core::DesktopCore, model::*};
use std::sync::Arc;
use tauri::State;
#[tauri::command]
#[specta::specta]
pub async fn cleanup_scan(
    state: State<'_, Arc<DesktopCore>>,
    request_id: String,
    categories: Option<Vec<CleanupCategory>>,
) -> Result<CleanupReport, AppError> {
    state
        .queue
        .cleanup_engine
        .scan_for_command(
            request_id,
            categories,
            state.permissions().await?,
            Some(state.inner().clone()),
        )
        .await
}
#[tauri::command]
#[specta::specta]
pub async fn cleanup_cancel(
    state: State<'_, Arc<DesktopCore>>,
    request_id: String,
) -> Result<(), AppError> {
    state.queue.cleanup_engine.cancel(&request_id)
}
/// Clean the chosen items after the user allows it in OpenNavo's confirmation window; the queued
/// task is bound to the exact list that window showed.
#[tauri::command]
#[specta::specta]
pub async fn cleanup_execute(
    app: tauri::AppHandle,
    state: State<'_, Arc<DesktopCore>>,
    confirmation: State<'_, Arc<crate::confirmation::ConfirmationService>>,
    report_id: String,
    item_ids: Vec<String>,
) -> Result<Task, AppError> {
    let queue = state.queue.clone();
    let engine = queue.cleanup_engine.clone();
    let (digest, view) = {
        let engine = engine.clone();
        let (report_id, item_ids) = (report_id.clone(), item_ids.clone());
        crate::blocking(move || engine.confirm_plan(&report_id, &item_ids)).await?
    };
    let service = confirmation.inner().clone();
    let request = crate::destructive::request("cleanup", &format!("cleanup/{report_id}"), digest);
    crate::destructive::confirmed(
        &service,
        request,
        |request| service.confirm_window(&app, request, view),
        |approved| {
            crate::blocking(move || {
                engine.enqueue_confirmed(&queue, &report_id, item_ids, &approved)
            })
        },
    )
    .await
}

/// Retry skipped filesystem items through the Trash-only cleanup path.
#[tauri::command]
#[specta::specta]
pub async fn cleanup_retry_trash(
    app: tauri::AppHandle,
    state: State<'_, Arc<DesktopCore>>,
    confirmation: State<'_, Arc<crate::confirmation::ConfirmationService>>,
    report_id: String,
    item_ids: Vec<String>,
) -> Result<Task, AppError> {
    let engine = state.queue.cleanup_engine.clone();
    let retry_id = {
        let engine = engine.clone();
        let report_id = report_id.clone();
        let item_ids = item_ids.clone();
        crate::blocking(move || engine.prepare_trash_retry(&report_id, &item_ids)).await?
    };
    let (digest, view) = {
        let engine = engine.clone();
        let retry_id = retry_id.clone();
        let item_ids = item_ids.clone();
        crate::blocking(move || engine.confirm_plan(&retry_id, &item_ids)).await?
    };
    let service = confirmation.inner().clone();
    let request = crate::destructive::request("cleanup", &format!("cleanup/{retry_id}"), digest);
    crate::destructive::confirmed(
        &service,
        request,
        |request| service.confirm_window(&app, request, view),
        |approved| {
            let queue = state.queue.clone();
            let engine = engine.clone();
            let retry_id = retry_id.clone();
            let item_ids = item_ids.clone();
            crate::blocking(move || {
                engine.enqueue_confirmed(&queue, &retry_id, item_ids, &approved)
            })
        },
    )
    .await
}
#[tauri::command]
#[specta::specta]
pub async fn cleanup_reveal(
    state: State<'_, Arc<DesktopCore>>,
    report_id: String,
    item_id: String,
) -> Result<(), AppError> {
    let engine = state.queue.cleanup_engine.clone();
    crate::blocking(move || engine.reveal(&report_id, &item_id)).await
}
#[tauri::command]
#[specta::specta]
pub async fn cleanup_summary(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<CleanupSummary, AppError> {
    let engine = state.queue.cleanup_engine.clone();
    crate::blocking(move || engine.summary()).await
}
#[tauri::command]
#[specta::specta]
pub async fn cleanup_protect(
    state: State<'_, Arc<DesktopCore>>,
    input: ProtectInput,
) -> Result<ProtectRule, AppError> {
    let engine = state.queue.cleanup_engine.clone();
    crate::blocking(move || engine.protect(input)).await
}
#[tauri::command]
#[specta::specta]
pub async fn cleanup_unprotect(
    state: State<'_, Arc<DesktopCore>>,
    rule_id: String,
) -> Result<(), AppError> {
    let engine = state.queue.cleanup_engine.clone();
    crate::blocking(move || engine.unprotect(&rule_id)).await
}
#[tauri::command]
#[specta::specta]
pub async fn cleanup_protected_list(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<Vec<ProtectRule>, AppError> {
    let engine = state.queue.cleanup_engine.clone();
    crate::blocking(move || engine.protected_list()).await
}
