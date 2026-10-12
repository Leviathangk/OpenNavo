use crate::{AppError, model::*, monitor};
use monitor::sampler::Sampler;
use std::sync::Arc;
use tauri::{Manager, WebviewWindow};
fn sampler(app: &tauri::AppHandle) -> Result<Arc<Sampler>, AppError> {
    app.try_state::<Arc<Sampler>>()
        .map(|state| state.inner().clone())
        .ok_or_else(|| AppError::new("E_MONITOR_UNAVAILABLE", "monitor_system_info"))
}
#[tauri::command]
#[specta::specta]
pub async fn monitor_subscribe(
    window: WebviewWindow,
    view: MonitorView,
    modules: Vec<MonitorModule>,
) -> Result<MonitorSnapshot, AppError> {
    let sampler = sampler(window.app_handle())?;
    let label = window.label().to_owned();
    let instance = monitor::window_instance(&window)?;
    let refresh =
        sampler.prepare_subscription(&label, instance, view, modules.into_iter().collect())?;
    crate::blocking(refresh).await
}
#[tauri::command]
#[specta::specta]
pub fn monitor_unsubscribe(window: WebviewWindow, view: MonitorView) -> Result<(), AppError> {
    sampler(window.app_handle())?.unsubscribe_instance(
        window.label(),
        monitor::window_instance(&window)?,
        view,
    )
}
/// Shared in-memory one-hour trend, oldest first.
#[tauri::command]
#[specta::specta]
pub fn monitor_trend(app: tauri::AppHandle) -> Result<Vec<MonitorTrendPoint>, AppError> {
    sampler(&app)?.trend()
}
#[tauri::command]
#[specta::specta]
pub async fn monitor_quit_app(app: tauri::AppHandle, pid: u32) -> Result<(), AppError> {
    let target = sampler(&app)?.quit_target(pid)?;
    crate::blocking(move || monitor::procs::quit(&target)).await
}
#[tauri::command]
#[specta::specta]
pub async fn monitor_open_activity_monitor() -> Result<(), AppError> {
    crate::system::open(&["-b".into(), "com.apple.ActivityMonitor".into()]).await
}
#[tauri::command]
#[specta::specta]
pub async fn monitor_menu_bar_status(
    app: tauri::AppHandle,
) -> Result<Vec<MenuBarItemStatus>, AppError> {
    let menu = sampler(&app)?.presentation()?.menu;
    monitor::menubar::status(app, menu).await
}
#[tauri::command]
#[specta::specta]
pub async fn monitor_open_menu_bar_settings() -> Result<(), AppError> {
    crate::system::open(&[
        "x-apple.systempreferences:com.apple.ControlCenter-Settings.extension?MenuBar".into(),
    ])
    .await
}
#[tauri::command]
#[specta::specta]
pub fn monitor_open_in_main(
    app: tauri::AppHandle,
    target: MonitorMainTarget,
    module: Option<MonitorModule>,
) -> Result<(), AppError> {
    let ui = app.clone();
    app.run_on_main_thread(move || monitor::popover::open_main(&ui, target, module))
        .map_err(|_| AppError::new("E_UNKNOWN", "monitor_main_thread"))
}

fn fan_client(app: &tauri::AppHandle) -> Result<monitor::fan::client::FanClient, AppError> {
    app.try_state::<monitor::fan::client::FanClient>()
        .map(|s| s.inner().clone())
        .ok_or_else(monitor::fan::client::unavailable)
}
async fn with_fans<R: Send + 'static>(
    app: tauri::AppHandle,
    operation: impl FnOnce(
        &mut monitor::fan::client::Client<monitor::fan::client::NativeTransport>,
    ) -> Result<R, AppError>
    + Send
    + 'static,
) -> Result<R, AppError> {
    let client = fan_client(&app)?;
    crate::blocking(move || {
        let mut client = client
            .lock()
            .map_err(|_| monitor::fan::client::unavailable())?;
        operation(&mut client)
    })
    .await
}
#[tauri::command]
#[specta::specta]
pub async fn fan_helper_status(app: tauri::AppHandle) -> Result<FanHelperStatus, AppError> {
    with_fans(app, |client| client.status()).await
}
#[tauri::command]
#[specta::specta]
pub async fn fan_helper_install(app: tauri::AppHandle) -> Result<FanHelperStatus, AppError> {
    with_fans(app, |client| client.install()).await
}
#[tauri::command]
#[specta::specta]
pub async fn fan_helper_uninstall(app: tauri::AppHandle) -> Result<(), AppError> {
    with_fans(app, |client| client.uninstall()).await
}
#[tauri::command]
#[specta::specta]
pub fn fan_helper_open_approval(app: tauri::AppHandle) -> Result<(), AppError> {
    #[cfg(target_os = "macos")]
    {
        app.run_on_main_thread(|| unsafe { monitor::fan::bridge::on_service_approval() })
            .map_err(|_| monitor::fan::client::unavailable())
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = app;
        Err(monitor::fan::client::unavailable())
    }
}
#[tauri::command]
#[specta::specta]
pub async fn fan_set_mode(
    app: tauri::AppHandle,
    fan_id: Option<u32>,
    mode: FanMode,
) -> Result<Vec<FanState>, AppError> {
    with_fans(app, move |client| client.mode(fan_id, mode)).await
}
#[tauri::command]
#[specta::specta]
pub async fn fan_set_speed(
    app: tauri::AppHandle,
    fan_id: Option<u32>,
    rpm: u32,
) -> Result<Vec<FanState>, AppError> {
    let fans = with_fans(app.clone(), move |client| client.speed(fan_id, rpm)).await?;
    let speeds = requested_manual_speeds(&fans, fan_id);
    app.state::<Arc<crate::core::DesktopCore>>()
        .save_fan_manual(speeds)
        .await?;
    Ok(fans)
}
#[tauri::command]
#[specta::specta]
pub async fn fan_restore_last(app: tauri::AppHandle) -> Result<Vec<FanState>, AppError> {
    let last = app
        .state::<Arc<crate::core::DesktopCore>>()
        .current_settings()?
        .fan_last_manual
        .filter(|m| !m.is_empty())
        .ok_or_else(|| AppError::new("E_FAN_NO_MANUAL_SPEED", "restore_last"))?;
    with_fans(app, move |client| {
        let reply = client.poll()?;
        if last.iter().any(|(id, rpm)| {
            !reply
                .fans
                .iter()
                .any(|f| f.id == *id && *rpm > 0 && *rpm >= f.min_rpm && *rpm <= f.max_rpm)
        }) {
            return Err(AppError::new("E_INVALID_ARG", "saved_fan_rpm"));
        }
        let mut result = reply.fans;
        for (id, rpm) in last {
            result = client.speed(Some(id), rpm)?;
        }
        Ok(result)
    })
    .await
}

// A speed reply contains every fan, including untouched automatic fans.
fn requested_manual_speeds(
    fans: &[FanState],
    fan_id: Option<u32>,
) -> std::collections::BTreeMap<u32, u32> {
    fans.iter()
        .filter(|f| fan_id.is_none_or(|id| f.id == id) && f.mode == FanMode::Manual)
        .map(|f| {
            (
                f.id,
                f.target_rpm
                    .unwrap_or(f.rpm)
                    .clamp(f.min_rpm.max(1), f.max_rpm),
            )
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn saved_speeds_only_include_requested_manual_fans() {
        let mut fans = vec![
            FanState {
                id: 0,
                name: "Left".into(),
                rpm: 2000,
                min_rpm: 1200,
                max_rpm: 6000,
                target_rpm: Some(3000),
                mode: FanMode::Manual,
            },
            FanState {
                id: 1,
                name: "Right".into(),
                rpm: 2200,
                min_rpm: 1200,
                max_rpm: 6000,
                target_rpm: Some(2200),
                mode: FanMode::Auto,
            },
        ];
        assert_eq!(requested_manual_speeds(&fans, Some(0)), [(0, 3000)].into());
        assert_eq!(requested_manual_speeds(&fans, None), [(0, 3000)].into());
        fans[1].mode = FanMode::Manual;
        fans[1].target_rpm = Some(7000);
        assert_eq!(requested_manual_speeds(&fans, Some(0)), [(0, 3000)].into());
        assert_eq!(
            requested_manual_speeds(&fans, None),
            [(0, 3000), (1, 6000)].into()
        );
    }
}
