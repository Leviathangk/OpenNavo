use crate::{AppError, appstore::types::*, core::DesktopCore};
use std::sync::Arc;
use tauri::State;

#[tauri::command]
#[specta::specta]
pub async fn mas_status(state: State<'_, Arc<DesktopCore>>) -> Result<MasStatus, AppError> {
    state.appstore.status().await
}
#[tauri::command]
#[specta::specta]
pub async fn mas_search(
    state: State<'_, Arc<DesktopCore>>,
    term: String,
    limit: u32,
) -> Result<Vec<MasApp>, AppError> {
    state.appstore.search(term, limit).await
}
#[tauri::command]
#[specta::specta]
pub async fn mas_lookup(
    state: State<'_, Arc<DesktopCore>>,
    adam_ids: Vec<u64>,
) -> Result<Vec<MasApp>, AppError> {
    state.appstore.lookup(adam_ids).await
}
#[tauri::command]
#[specta::specta]
pub async fn mas_lookup_bundles(
    state: State<'_, Arc<DesktopCore>>,
    bundle_ids: Vec<String>,
) -> Result<MasBundleLookup, AppError> {
    state.appstore.lookup_bundles(bundle_ids).await
}
#[tauri::command]
#[specta::specta]
pub async fn mas_installed(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<Vec<MasInstalled>, AppError> {
    state.appstore.installed().await
}
#[tauri::command]
#[specta::specta]
pub async fn mas_updates(state: State<'_, Arc<DesktopCore>>) -> Result<Vec<MasUpdate>, AppError> {
    state.appstore.updates().await
}
#[tauri::command]
#[specta::specta]
pub async fn mas_open_store(
    state: State<'_, Arc<DesktopCore>>,
    adam_id: u64,
) -> Result<(), AppError> {
    state.appstore.open_store(adam_id).await
}
