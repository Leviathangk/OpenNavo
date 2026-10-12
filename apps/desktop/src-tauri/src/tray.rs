use crate::{AppError, core::DesktopCore, deeplink::Router, model::*};
use std::sync::Arc;
use tauri::{
    Manager, WebviewUrl, WebviewWindowBuilder,
    menu::{Menu, MenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
};

pub const LABEL: &str = "tray";
pub const ROUTE: &str = "index.html#/tray";
pub const WIDTH: f64 = 330.;
pub const HEIGHT: f64 = 200.;
// Match frontend updates.actionable; legacy cached Formula entries must not be counted or batch-updated.
fn actionable(item: &OutdatedItem) -> bool {
    item.kind == Kind::Cask && !item.pinned && !item.ignored
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
pub fn refresh_now(app: &tauri::AppHandle) -> Result<(), AppError> {
    let core = app.state::<Arc<DesktopCore>>();
    let settings = core.current_settings()?;
    if let Some(tray) = app.tray_by_id("opennavo") {
        let count = core
            .updates_list()?
            .iter()
            .filter(|i| actionable(i))
            .count();
        tray.set_title(title(count, settings.tray_show_count))
            .map_err(error)?;
        tray.set_menu(Some(menu(app, settings.locale)?))
            .map_err(error)?;
    }
    Ok(())
}
pub fn refresh(app: &tauri::AppHandle) {
    let handle = app.clone();
    let _ = app.run_on_main_thread(move || {
        if let Err(error) = refresh_now(&handle) {
            log::warn!("Tray menu refresh failed: {}", error.code);
        }
    });
}

pub(crate) fn navigate(app: &tauri::AppHandle, route: &str) {
    if let Some(window) = app.get_webview_window(LABEL) {
        let _ = window.hide();
    }
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
        .resizable(false)
        .skip_taskbar(true)
        .build()
        .map_err(error)?;
    let hidden = window.clone();
    window.on_window_event(move |event| {
        if matches!(event, tauri::WindowEvent::Focused(false)) {
            let _ = hidden.hide();
        }
    });
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
            if matches!(
                event,
                TrayIconEvent::Click {
                    button: MouseButton::Left,
                    button_state: MouseButtonState::Up,
                    ..
                }
            ) && let Some(window) = app.get_webview_window(LABEL)
            {
                if window.is_visible().unwrap_or(false) {
                    let _ = window.hide();
                } else {
                    use tauri_plugin_positioner::{Position, WindowExt};
                    let _ = window.move_window_constrained(Position::TrayBottomCenter);
                    let _ = window.show();
                    let _ = window.set_focus();
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
                            .map(|i| TaskTarget {
                                kind: i.kind.into(),
                                token: i.token,
                            })
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
                        .check_updates(
                            core.current_settings()
                                .map(|s| s.run_brew_update_on_check)
                                .unwrap_or(false),
                        )
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
    refresh(app);
    Ok(())
}
