pub mod alerts;
pub mod battery;
#[cfg(target_os = "macos")]
mod cf;
pub mod collect;
pub mod engine;
#[cfg(target_os = "macos")]
pub mod extras;
pub mod fan;
pub mod health;
pub mod menubar;
pub mod native;
mod observers;
pub mod popover;
pub mod procs;
pub mod routes;
pub mod sampler;
pub mod sensors;
pub mod smc;
pub mod system;
pub mod trend;
pub mod types;
pub mod volume_cache;

use crate::AppError;
use types::{FanControlSupport, MonitorSnapshot};

pub const POPOVER_LABEL: &str = "tray-monitor";
pub const POPOVER_ROUTE: &str = "index.html#/tray-monitor";

pub fn not_implemented(command: &str, milestone: &str) -> AppError {
    AppError::new("E_NOT_IMPLEMENTED", command).with_detail(format!(
        "Scheduled for {milestone}; this command currently exports the contract only"
    ))
}

/// Static-only snapshot used by contract validation; runtime subscriptions use Sampler::snapshot.
pub fn snapshot(source: &impl system::SystemSource) -> Result<MonitorSnapshot, AppError> {
    Ok(MonitorSnapshot {
        system: system::read(source)?,
        available: Vec::new(),
        history: Vec::new(),
    })
}

/// Manual control is offered on every chip family that exposes its fans (Intel, Apple M1 and later); per-chip
/// verification is informational, and the helper's fail-safes carry the safety. Unknown chips stay read-only.
/// The helper applies the same gate as the App.
pub fn manual_allowed(support: FanControlSupport) -> bool {
    support != FanControlSupport::Unsupported
}

// The resource table belongs to this webview instance (unlike its reusable label).
struct WindowInstance(u64);
impl tauri::Resource for WindowInstance {}

pub fn window_instance(window: &impl tauri::Manager<tauri::Wry>) -> Result<u64, AppError> {
    let resources = window.resources_table();
    resources
        .names()
        .find_map(|(id, _)| {
            resources
                .get::<WindowInstance>(id)
                .ok()
                .map(|token| token.0)
        })
        .ok_or_else(|| AppError::new("E_MONITOR_UNAVAILABLE", "monitor_window_closed"))
}

/// Called only after native creation, before the window can request monitor data.
pub fn register_window(
    window: &tauri::WebviewWindow,
    sampler: &std::sync::Arc<sampler::Sampler>,
) -> Result<u64, AppError> {
    use tauri::Manager;
    let instance = sampler.register_window(window.label())?;
    window.resources_table().add(WindowInstance(instance));
    let sampler = sampler.clone();
    let label = window.label().to_owned();
    window.on_window_event(move |event| {
        if matches!(event, tauri::WindowEvent::Destroyed) {
            sampler.destroy_window_instance(&label, instance);
        }
    });
    Ok(instance)
}

/// Only static info and a one-off capability probe are read at startup; the loop obeys combined demand.
pub fn install(app: &tauri::AppHandle) -> Result<(), AppError> {
    use std::sync::Arc;
    use tauri::Manager;
    let settings = app
        .state::<Arc<crate::core::DesktopCore>>()
        .current_settings()?;
    let collector = native::NativeCollector::new()?;
    let sampler = sampler::Sampler::new(
        Box::new(collector),
        settings.monitor_menu_bar,
        settings.monitor_interval_ms,
    );
    app.manage(popover::Popover::default());
    app.manage(sampler.clone());
    let presentation = sampler.presentation()?;
    fan::client::install(
        app,
        &sampler.snapshot()?.system.chip,
        presentation.sample.fans.as_ref().map_or(0, Vec::len),
    );
    if let Some(window) = app.get_webview_window("main") {
        register_window(&window, &sampler)?;
        sampler.set_window_visible("main", window.is_visible().unwrap_or(false));
    }
    observers::install(app, sampler.clone());
    let ui = app.clone();
    sampler.start(Arc::new(move |p| {
        use tauri_specta::Event;
        for window in &p.windows {
            if ui
                .get_webview_window(window)
                .is_some_and(|w| w.is_visible().unwrap_or(false))
            {
                let _ = crate::events::MonitorSampleEvent(p.sample.clone()).emit_to(&ui, window);
            }
        }
        if let (Some(notices), Some(core)) = (
            ui.try_state::<Arc<crate::notifications::NativeNotifications>>(),
            ui.try_state::<Arc<crate::core::DesktopCore>>(),
        ) && let Ok(settings) = core.current_settings()
            && settings.monitor_alerts
        {
            for issue in &p.alerts {
                let _ = notices.monitor(issue, settings.locale);
            }
        }
        menubar::refresh(&ui, p);
    }));
    refresh_settings(app);
    Ok(())
}
pub fn refresh_settings(app: &tauri::AppHandle) {
    use std::sync::Arc;
    use tauri::Manager;
    if let (Some(sampler), Some(core)) = (
        app.try_state::<Arc<sampler::Sampler>>(),
        app.try_state::<Arc<crate::core::DesktopCore>>(),
    ) && let Ok(settings) = core.current_settings()
    {
        let _ = sampler.configure_alerts(settings.monitor_alerts);
        let _ = sampler.configure(settings.monitor_menu_bar, settings.monitor_interval_ms);
        if let Ok(p) = sampler.presentation() {
            menubar::refresh(app, p);
        }
    }
}
