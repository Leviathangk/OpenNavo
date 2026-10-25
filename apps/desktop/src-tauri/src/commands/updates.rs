use crate::{AppError, core::DesktopCore, model::*};
use std::sync::Arc;
use tauri::State;
#[tauri::command]
#[specta::specta]
pub async fn updates_status(state: State<'_, Arc<DesktopCore>>) -> Result<UpdatesStatus, AppError> {
    let core = state.inner().clone();
    crate::blocking(move || core.updates_status()).await
}
#[tauri::command]
#[specta::specta]
pub async fn updates_list(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<Vec<OutdatedItem>, AppError> {
    // Merging cached updates still reads app bundles and may enumerate local apps.
    // Neither disk access nor waiting for their locks may block the window thread.
    let core = state.inner().clone();
    crate::blocking(move || core.updates_list()).await
}
#[tauri::command]
#[specta::specta]
pub async fn updates_check(
    state: State<'_, Arc<DesktopCore>>,
    sources: Option<Vec<UpdateSource>>,
) -> Result<Vec<OutdatedItem>, AppError> {
    state
        .inner()
        .check_update_sources(sources, TaskTrigger::Manual, None)
        .await
}

#[tauri::command]
#[specta::specta]
pub async fn update_ignore(
    state: State<'_, Arc<DesktopCore>>,
    app_key: String,
    ignored: bool,
) -> Result<(), AppError> {
    let updater = state.queue.updater.clone();
    crate::blocking(move || updater.ignore(&app_key, ignored)).await
}

#[tauri::command]
#[specta::specta]
pub async fn update_skip(
    state: State<'_, Arc<DesktopCore>>,
    app_key: String,
    version: Option<String>,
) -> Result<(), AppError> {
    let updater = state.queue.updater.clone();
    crate::blocking(move || updater.skip(&app_key, version.as_deref())).await
}

#[tauri::command]
#[specta::specta]
pub async fn update_set_channel(
    state: State<'_, Arc<DesktopCore>>,
    app_key: String,
    channel: UpdateChannel,
) -> Result<(), AppError> {
    let updater = state.queue.updater.clone();
    crate::blocking(move || updater.channel(&app_key, channel)).await
}

/// Queues a rollback of a recorded update and returns the task id.
#[tauri::command]
#[specta::specta]
pub async fn update_rollback(
    state: State<'_, Arc<DesktopCore>>,
    history_id: String,
) -> Result<String, AppError> {
    let queue = state.queue.clone();
    crate::blocking(move || queue.enqueue_rollback(&history_id)).await
}

/// The history ids among `history_ids` whose update can be rolled back now.
#[tauri::command]
#[specta::specta]
pub async fn update_rollback_targets(
    state: State<'_, Arc<DesktopCore>>,
    history_ids: Vec<String>,
) -> Result<Vec<String>, AppError> {
    let queue = state.queue.clone();
    crate::blocking(move || queue.rollback_targets(&history_ids)).await
}

#[tauri::command]
#[specta::specta]
pub async fn app_relaunch(
    state: State<'_, Arc<DesktopCore>>,
    app_id: String,
) -> Result<(), AppError> {
    let _operation = state.queue.read_gate.write().await;
    let updater = state.queue.updater.clone();
    crate::blocking(move || updater.relaunch(&app_id)).await
}
#[tauri::command]
#[specta::specta]
pub async fn updates_ignore(
    state: State<'_, Arc<DesktopCore>>,
    target: TaskTarget,
    version: Option<String>,
    until: Option<i64>,
) -> Result<(), AppError> {
    state.inner().ignore_update(target, version, until).await
}
#[tauri::command]
#[specta::specta]
pub async fn updates_unignore(
    state: State<'_, Arc<DesktopCore>>,
    target: TaskTarget,
) -> Result<(), AppError> {
    state.inner().unignore_update(target).await
}
