use crate::{AppError, core::DesktopCore, model::*};
use std::sync::Arc;
use tauri::State;

/// Current privacy permissions (06 §12.3, §12.6): read-only, without prompts.
#[tauri::command]
#[specta::specta]
pub async fn permissions_get(state: State<'_, Arc<DesktopCore>>) -> Result<Permissions, AppError> {
    state.permissions().await
}

/// Open System Settings without creating a guidance overlay.
#[tauri::command]
#[specta::specta]
pub async fn permission_settings_open(
    app: tauri::AppHandle,
    pane: PrivacyPane,
) -> Result<(), AppError> {
    let url = crate::system::privacy_url(pane, &app.config().identifier)?;
    crate::system::open(&[url]).await
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
pub async fn permission_guide_info(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<PermissionGuideInfo, AppError> {
    let core = state.inner().clone();
    crate::blocking(move || {
        let foreign = crate::guide::foreign_owner();
        // OpenNavo's own icon ships with the page; another owner's comes from the local app icon cache.
        let owner_icon = foreign.as_ref().and_then(|(_, bundle)| {
            let cache = core.data_dir.join("logs/local-app-icons");
            crate::library::files::icon(&core.database, bundle, &cache).ok()
        });
        Ok(PermissionGuideInfo {
            draggable: crate::guide::owner_bundle().is_some(),
            owner_name: foreign.map(|(name, _)| name),
            owner_icon,
        })
    })
    .await
}

/// Drag the owner app's icon (usually OpenNavo's) from the overlay. This synchronous command runs on the main thread while
/// the current event is still mouse-down.
#[tauri::command]
#[specta::specta]
pub fn permission_guide_drag(window: tauri::WebviewWindow) -> Result<(), AppError> {
    crate::guide::drag(&window)
}
