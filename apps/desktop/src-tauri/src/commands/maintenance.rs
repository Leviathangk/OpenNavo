use crate::{AppError, core::DesktopCore, model::*};
use std::sync::Arc;
use tauri::State;
#[tauri::command]
#[specta::specta]
pub async fn brewfile_export(
    state: State<'_, Arc<DesktopCore>>,
    path: String,
    targets: Option<Vec<TaskTarget>>,
) -> Result<u32, AppError> {
    if let Some(targets) = targets {
        let database = state.database.clone();
        return crate::blocking(move || {
            crate::brewfile::export_catalog_list(&database, &path, &targets)
        })
        .await;
    }
    crate::brewfile::export(state.inner().clone(), path).await
}
#[tauri::command]
#[specta::specta]
pub async fn brewfile_preview(
    state: State<'_, Arc<DesktopCore>>,
    path: String,
) -> Result<BrewfilePreview, AppError> {
    crate::brewfile::preview(state.inner().clone(), path).await
}
#[tauri::command]
#[specta::specta]
pub async fn brewfile_install(
    state: State<'_, Arc<DesktopCore>>,
    targets: Vec<TaskTarget>,
) -> Result<Vec<Task>, AppError> {
    if targets.iter().any(|target| target.kind != Kind::Cask) {
        return Err(AppError::new("E_INVALID_ARG", "unsupported_kind"));
    }
    let queue = state.queue.clone();
    crate::blocking(move || {
        queue.enqueue_many(
            TaskOp::Install,
            targets,
            TaskOptions::default(),
            TaskTrigger::Bundle,
        )
    })
    .await
}
#[tauri::command]
#[specta::specta]
pub async fn doctor_run(state: State<'_, Arc<DesktopCore>>) -> Result<DoctorReport, AppError> {
    crate::maintenance::diagnose(state.inner().clone()).await
}
