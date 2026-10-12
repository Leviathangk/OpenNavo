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
#[tauri::command]
#[specta::specta]
pub async fn cleanup_execute(
    state: State<'_, Arc<DesktopCore>>,
    report_id: String,
    item_ids: Vec<String>,
) -> Result<Task, AppError> {
    let queue = state.queue.clone();
    let engine = queue.cleanup_engine.clone();
    crate::blocking(move || engine.enqueue(&queue, &report_id, item_ids)).await
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
