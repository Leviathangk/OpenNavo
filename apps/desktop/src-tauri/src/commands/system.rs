use crate::{AppError, core::DesktopCore, model::*};
use std::sync::Arc;
use tauri::State;
async fn open(
    state: State<'_, Arc<DesktopCore>>,
    target: TaskTarget,
    reveal: bool,
) -> Result<(), AppError> {
    let library = state.library_list()?;
    let path = crate::blocking(move || crate::system::app_path(&target, &library)).await?;
    let mut args = vec![];
    if reveal {
        args.push("-R".into());
    }
    args.push(path.to_string_lossy().into());
    crate::system::open(&args).await
}
#[tauri::command]
#[specta::specta]
pub async fn app_open(
    state: State<'_, Arc<DesktopCore>>,
    target: TaskTarget,
) -> Result<(), AppError> {
    open(state, target, false).await
}
#[tauri::command]
#[specta::specta]
pub async fn app_reveal(
    state: State<'_, Arc<DesktopCore>>,
    target: TaskTarget,
) -> Result<(), AppError> {
    open(state, target, true).await
}
#[tauri::command]
#[specta::specta]
pub async fn system_open_privacy(app: tauri::AppHandle, pane: PrivacyPane) -> Result<(), AppError> {
    let url = crate::system::privacy_url(pane, &app.config().identifier)?;
    crate::system::open(&[url]).await
}
#[tauri::command]
#[specta::specta]
pub async fn app_quit(
    app: tauri::AppHandle,
    state: State<'_, Arc<DesktopCore>>,
) -> Result<(), AppError> {
    crate::lifecycle::confirmed_quit(app, state.inner().clone()).await
}

#[tauri::command]
#[specta::specta]
pub fn app_cancel_quit(app: tauri::AppHandle) {
    use tauri::Manager;
    if let Some(lifecycle) = app.try_state::<Arc<crate::lifecycle::Lifecycle>>() {
        lifecycle.cancel();
    }
}
