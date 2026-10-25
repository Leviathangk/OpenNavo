use crate::{AppError, core::DesktopCore, model::*};
use std::sync::Arc;
use tauri::State;
#[tauri::command]
#[specta::specta]
pub async fn library_list(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<Vec<InstalledItem>, AppError> {
    let core = state.inner().clone();
    crate::blocking(move || core.library_list()).await
}
#[tauri::command]
#[specta::specta]
pub async fn library_refresh(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<Vec<InstalledItem>, AppError> {
    state
        .inner()
        .refresh_library(LibraryChangeReason::Refresh, None)
        .await
}
#[tauri::command]
#[specta::specta]
pub async fn library_storage(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<StorageSummary, AppError> {
    let core = state.inner().clone();
    crate::blocking(move || core.storage_summary()).await
}
