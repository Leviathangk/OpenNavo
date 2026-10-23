use crate::{AppError, core::DesktopCore, model::*};
use std::sync::Arc;
use tauri::State;
#[tauri::command]
#[specta::specta]
pub fn settings_get(state: State<'_, Arc<DesktopCore>>) -> Result<Settings, AppError> {
    state.current_settings()
}
#[tauri::command]
#[specta::specta]
pub async fn settings_set(
    app: tauri::AppHandle,
    state: State<'_, Arc<DesktopCore>>,
    settings: Settings,
) -> Result<Settings, AppError> {
    let handle = app.clone();
    let change: crate::settings::Autostart = Arc::new(move |enabled| set_autostart(&app, enabled));
    let result = state
        .inner()
        .replace_settings_with(settings, Some(change))
        .await?;
    let persisted = result.clone();
    let ui = handle.clone();
    handle
        .run_on_main_thread(move || {
            crate::settings::apply_apple_languages(&persisted);
            crate::settings::apply_appearance(&ui, persisted.appearance);
        })
        .map_err(|e| AppError::new("E_UNKNOWN", "apple_languages").with_detail(e.to_string()))?;
    crate::tray::refresh(&handle);
    crate::monitor::refresh_settings(&handle);
    Ok(result)
}

fn set_autostart(app: &tauri::AppHandle, enabled: bool) -> Result<(), AppError> {
    use tauri_plugin_autostart::ManagerExt;
    let result = if enabled {
        app.autolaunch().enable()
    } else {
        app.autolaunch().disable()
    };
    result.map_err(|e| AppError::new("E_UNKNOWN", "autostart").with_detail(e.to_string()))
}

#[tauri::command]
#[specta::specta]
pub fn locale_get(state: State<'_, Arc<DesktopCore>>) -> Result<Locale, AppError> {
    Ok(state.current_settings()?.locale)
}
#[tauri::command]
#[specta::specta]
pub fn system_locale_get() -> Result<Locale, AppError> {
    Ok(crate::settings::system_locale())
}
pub fn refresh_system(app: &tauri::AppHandle) {
    use tauri::Manager;
    let Some(core) = app.try_state::<Arc<DesktopCore>>() else {
        return;
    };
    let core = core.inner().clone();
    tauri::async_runtime::spawn(async move {
        if let Ok(settings) = core.current_settings()
            && settings.locale_mode == LocaleMode::System
            && settings.locale != crate::settings::system_locale()
            && let Err(error) = core.replace_settings(settings).await
        {
            log::warn!("System locale refresh failed: {}", error.code);
        }
    });
}
