use crate::{AppError, core::DesktopCore, deeplink::Router, model::*};
use std::sync::{
    Arc,
    atomic::{AtomicBool, AtomicU64, Ordering},
};
use tauri::{
    Manager, WebviewUrl, WebviewWindowBuilder,
    menu::{Menu, MenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
};

#[cfg(target_os = "macos")]
pub(crate) mod panel;

pub const LABEL: &str = "tray";
pub const ROUTE: &str = "index.html#/tray";
pub const WIDTH: f64 = 330.;
pub const HEIGHT: f64 = 200.;
// Match frontend updates.actionable; legacy cached Formula entries must not be counted or batch-updated.
fn actionable(item: &OutdatedItem) -> bool {
    item.kind != Kind::Formula
        && !item.pinned
        && !item.ignored
        && !item.relaunch_pending
        && item.task_target().is_ok()
        && item.skipped_version.as_deref() != Some(item.current_version.as_str())
}
fn error(error: impl std::fmt::Display) -> AppError {
    AppError::new("E_UNKNOWN", "tray").with_detail(error.to_string())
}
pub fn title(count: usize, show: bool) -> Option<String> {
    if show && count > 0 {
        Some(count.to_string())
    } else {
        None
    }
}
fn menu(app: &tauri::AppHandle, locale: Locale) -> Result<Menu<tauri::Wry>, AppError> {
    let mut items = vec![];
    for (id, key) in [
        ("open", "open"),
        ("check", "check"),
        ("upgrade", "upgrade"),
        ("settings", "settings"),
        ("quit", "quit"),
    ] {
        items.push(
            MenuItem::with_id(
                app,
                id,
                crate::native_i18n::text(locale, key)?,
                true,
                None::<&str>,
            )
            .map_err(error)?,
        );
    }
    let items: Vec<&dyn tauri::menu::IsMenuItem<tauri::Wry>> = items
        .iter()
        .map(|item| item as &dyn tauri::menu::IsMenuItem<tauri::Wry>)
        .collect();
    Menu::with_items(app, &items).map_err(error)
}
// One worker coalesces bursts; queued UI callbacks cannot apply stale counts/locales.
#[derive(Default)]
struct RefreshQueue {
    revision: AtomicU64,
    running: AtomicBool,
}
impl RefreshQueue {
    fn request(&self) -> bool {
        self.revision.fetch_add(1, Ordering::SeqCst);
        !self.running.swap(true, Ordering::SeqCst)
    }
    fn again(&self, completed: u64) -> bool {
        self.running.store(false, Ordering::SeqCst);
        self.revision.load(Ordering::SeqCst) != completed
            && !self.running.swap(true, Ordering::SeqCst)
    }
}
static REFRESH: RefreshQueue = RefreshQueue {
    revision: AtomicU64::new(0),
    running: AtomicBool::new(false),
};
pub fn refresh(app: &tauri::AppHandle) {
    if !REFRESH.request() {
        return;
    }
    let handle = app.clone();
    tauri::async_runtime::spawn(async move {
        loop {
            let revision = REFRESH.revision.load(Ordering::SeqCst);
            let core = handle.state::<Arc<DesktopCore>>().inner().clone();
            let snapshot = crate::blocking(move || {
                let settings = core.current_settings()?;
                let count = core
                    .updates_list()?
                    .iter()
                    .filter(|i| actionable(i))
                    .count();
                Ok((title(count, settings.tray_show_count), settings.locale))
            })
            .await;
            match snapshot {
                Ok((title, locale)) => {
                    let ui = handle.clone();
                    let _ = handle.run_on_main_thread(move || {
                        if REFRESH.revision.load(Ordering::SeqCst) != revision {
                            return;
                        }
                        if let Some(tray) = ui.tray_by_id("opennavo") {
                            let result = tray.set_title(title).map_err(error).and_then(|()| {
                                tray.set_menu(Some(menu(&ui, locale)?)).map_err(error)
                            });
                            if let Err(error) = result {
                                log::warn!("Tray menu refresh failed: {}", error.code);
                            }
                        }
                    });
                }
                Err(error) => log::warn!("Tray snapshot failed: {}", error.code),
            }
            if !REFRESH.again(revision) {
                break;
            }
        }
    });
}

pub(crate) fn hide<R: tauri::Runtime>(app: &tauri::AppHandle<R>) {
    if let Some(window) = app.get_webview_window(LABEL) {
        let _ = window.hide();
    }
}

pub(crate) fn navigate(app: &tauri::AppHandle, route: &str) {
    hide(app);
    crate::monitor::popover::hide(app);
    if let Some(router) = app.try_state::<Arc<Router>>() {
        router.navigate(route);
    }
}
pub fn install(app: &tauri::AppHandle) -> Result<(), AppError> {
    let window = WebviewWindowBuilder::new(app, LABEL, WebviewUrl::App(ROUTE.into()))
        .inner_size(WIDTH, HEIGHT)
        .decorations(false)
        .transparent(true)
        // Frontend panels draw corners/shadows; native shadows would reveal the transparent window's rectangular bounds.
        .shadow(false)
        .always_on_top(true)
        .visible(false)
        .focused(false)
        .accept_first_mouse(true)
        .resizable(false)
        .skip_taskbar(true)
        .build()
        .map_err(error)?;
    #[cfg(target_os = "macos")]
    {
        panel::configure(&window);
        let handle = app.clone();
        window.on_window_event(move |event| {
            if matches!(
                event,
                tauri::WindowEvent::Resized(_) | tauri::WindowEvent::ScaleFactorChanged { .. }
            ) {
                let ui = handle.clone();
                let _ = handle.run_on_main_thread(move || {
                    if let Some(window) = ui.get_webview_window(LABEL)
                        && window.is_visible().unwrap_or(false)
                    {
                        let _ = panel::position_updates(&window);
                    }
                });
            }
        });
    }
    #[cfg(not(target_os = "macos"))]
    {
        let hidden = window.clone();
        window.on_window_event(move |event| {
            if matches!(event, tauri::WindowEvent::Focused(false)) {
                let _ = hidden.hide();
            }
        });
    }
    let settings = app.state::<Arc<DesktopCore>>().current_settings()?;
    let icon = image::load_from_memory(include_bytes!("../icons/trayTemplate@2x.png"))
        .map_err(error)?
        .to_rgba8();
    let (width, height) = icon.dimensions();
    TrayIconBuilder::with_id("opennavo")
        .icon(tauri::image::Image::new_owned(
            icon.into_raw(),
            width,
            height,
        ))
        .icon_as_template(true)
        .menu(&menu(app, settings.locale)?)
        .show_menu_on_left_click(false)
        .on_tray_icon_event(|tray, event| {
            let app = tray.app_handle();
            tauri_plugin_positioner::on_tray_event(app, &event);
            #[cfg(target_os = "macos")]
            if matches!(event, TrayIconEvent::Click { .. }) {
                panel::status_click();
            }
            if matches!(
                event,
                TrayIconEvent::Click {
                    button: MouseButton::Left,
                    button_state: MouseButtonState::Up,
                    ..
                }
            ) && let Some(window) = app.get_webview_window(LABEL)
            {
                // Invalidate delayed monitor shows as well as a visible popover.
                crate::monitor::popover::hide(app);
                if window.is_visible().unwrap_or(false) {
                    hide(app);
                } else {
                    #[cfg(target_os = "macos")]
                    if let Err(error) = panel::show(&window) {
                        log::warn!("Tray panel failed: {}", error.code);
                    }
                    #[cfg(not(target_os = "macos"))]
                    {
                        use tauri_plugin_positioner::{Position, WindowExt};
                        let _ = window.move_window_constrained(Position::TrayBottomCenter);
                        let _ = window.show();
                        let _ = window.set_focus();
                    }
                }
            }
        })
        .on_menu_event(|app, event| match event.id().as_ref() {
            "open" => navigate(app, "/"),
            "settings" => navigate(app, "/settings"),
            "quit" => app.exit(0),
            "check" | "upgrade" => {
                let Some(core) = app.try_state::<Arc<DesktopCore>>() else {
                    return;
                };
                let core = core.inner().clone();
                let upgrade = event.id().as_ref() == "upgrade";
                let app = app.clone();
                tauri::async_runtime::spawn(async move {
                    if upgrade {
                        let targets = core
                            .updates_list()
                            .unwrap_or_default()
                            .into_iter()
                            .filter(actionable)
                            .filter_map(|i| i.task_target().ok())
                            .collect();
                        crate::deeplink::show_main(&app);
                        use tauri_specta::Event;
                        if crate::events::UpdatesRequested(targets)
                            .emit_to(&app, "main")
                            .is_err()
                        {
                            log::warn!("Failed to send tray update request");
                        }
                    } else if let Err(error) = core
                        .check_update_sources(None, TaskTrigger::Manual, None)
                        .await
                    {
                        log::info!("Tray check temporarily unavailable: {}", error.code);
                    }
                });
            }
            _ => {}
        })
        .build(app)
        .map_err(error)?;
    #[cfg(target_os = "macos")]
    panel::install(app);
    refresh(app);
    Ok(())
}

#[cfg(test)]
mod refresh_tests {
    use super::*;
    #[test]
    fn burst_requests_share_worker_and_refresh_latest_revision() {
        let queue = RefreshQueue::default();
        assert!(queue.request());
        let first = queue.revision.load(Ordering::SeqCst);
        for _ in 0..100 {
            assert!(!queue.request());
        }
        assert!(queue.again(first));
        let latest = queue.revision.load(Ordering::SeqCst);
        assert_eq!(latest, 101);
        assert!(!queue.again(latest));
        assert!(queue.request());
    }
}
