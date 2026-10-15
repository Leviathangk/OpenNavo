use crate::{AppError, core::DesktopCore, model::*};
use std::sync::Arc;
use tauri::State;

/// Current privacy permissions (06 §12.3, §12.6): read-only, without prompts.
#[tauri::command]
#[specta::specta]
pub async fn permissions_get(state: State<'_, Arc<DesktopCore>>) -> Result<Permissions, AppError> {
    state.permissions().await
}

/// Open the corresponding System Settings panel and adjacent guidance overlay (06 §12.7).
#[tauri::command]
#[specta::specta]
pub async fn permission_guide_open(
    app: tauri::AppHandle,
    pane: PrivacyPane,
) -> Result<(), AppError> {
    crate::guide::open(&app, pane).await
}

#[tauri::command]
#[specta::specta]
pub async fn permission_guide_close(app: tauri::AppHandle) -> Result<(), AppError> {
    crate::guide::close(&app);
    Ok(())
}

/// Size the overlay to the height its page needs: longer translations wrap onto more lines.
#[tauri::command]
#[specta::specta]
pub async fn permission_guide_fit(app: tauri::AppHandle, height: u32) -> Result<(), AppError> {
    crate::guide::fit(&app, height);
    Ok(())
}

#[tauri::command]
#[specta::specta]
pub async fn permission_guide_info() -> Result<PermissionGuideInfo, AppError> {
    Ok(PermissionGuideInfo {
        draggable: crate::guide::bundle_path().is_some(),
    })
}

/// Drag OpenNavo's icon from the overlay. This synchronous command runs on the main thread while the current event is still mouse-down.
#[tauri::command]
#[specta::specta]
pub fn permission_guide_drag(window: tauri::WebviewWindow) -> Result<(), AppError> {
    crate::guide::drag(&window)
}
