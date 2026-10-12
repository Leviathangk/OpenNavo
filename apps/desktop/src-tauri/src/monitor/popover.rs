use super::{POPOVER_LABEL, POPOVER_ROUTE, sampler::Sampler, types::*};
use crate::AppError;
use std::sync::{Arc, Mutex};
use tauri::{Manager, WebviewUrl, WebviewWindowBuilder};
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Rect {
    pub x: f64,
    pub y: f64,
    pub width: f64,
    pub height: f64,
}
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Anchor {
    pub item: Rect,
    pub visible: Rect,
}
impl Anchor {
    #[cfg(target_os = "macos")]
    pub fn new(item: objc2_foundation::NSRect, visible: objc2_foundation::NSRect) -> Self {
        let rect = |r: objc2_foundation::NSRect| Rect {
            x: r.origin.x,
            y: r.origin.y,
            width: r.size.width,
            height: r.size.height,
        };
        Self {
            item: rect(item),
            visible: rect(visible),
        }
    }
}
pub fn position(anchor: Anchor, width: f64, height: f64) -> (f64, f64) {
    let screen = anchor.visible;
    let x = (anchor.item.x + anchor.item.width / 2.0 - width / 2.0)
        .clamp(screen.x, (screen.x + screen.width - width).max(screen.x));
    let y = (anchor.item.y - height - 4.0)
        .clamp(screen.y, (screen.y + screen.height - height).max(screen.y));
    (x, y)
}
#[derive(Default)]
struct State {
    module: Option<MonitorModule>,
    anchor: Option<Anchor>,
    ready: bool,
    wanted: bool,
    generation: u64,
    shown: bool,
}
#[derive(Default)]
pub struct Popover(Mutex<State>);
fn error() -> AppError {
    AppError::new("E_UNKNOWN", "monitor_popover")
}
fn cancel(app: &tauri::AppHandle) {
    if let Some(sampler) = app.try_state::<Arc<Sampler>>() {
        sampler.remove_window(POPOVER_LABEL);
    }
}
pub fn hide(app: &tauri::AppHandle) {
    if let Some(state) = app.try_state::<Popover>()
        && let Ok(mut state) = state.0.lock()
    {
        state.wanted = false;
        state.shown = false;
        state.generation = state.generation.wrapping_add(1);
    }
    cancel(app);
    if let Some(window) = app.get_webview_window(POPOVER_LABEL) {
        let _ = window.hide();
    }
}
pub fn click(
    app: &tauri::AppHandle,
    module: MonitorModule,
    anchor: Anchor,
) -> Result<(), AppError> {
    let popover = app.state::<Popover>();
    {
        let mut state = popover.0.lock().map_err(|_| error())?;
        if state.wanted && state.module == Some(module) {
            drop(state);
            hide(app);
            return Ok(());
        }
        state.module = Some(module);
        state.anchor = Some(anchor);
        state.wanted = true;
        state.generation = state.generation.wrapping_add(1);
    }
    // Every module shares one webview; switching never navigates or reloads it.
    if app.get_webview_window(POPOVER_LABEL).is_none() {
        let handle = app.clone();
        let window =
            WebviewWindowBuilder::new(app, POPOVER_LABEL, WebviewUrl::App(POPOVER_ROUTE.into()))
                .inner_size(360.0, 400.0)
                .decorations(false)
                .transparent(true)
                .shadow(false)
                .always_on_top(true)
                .visible(false)
                .resizable(false)
                .skip_taskbar(true)
                .on_page_load(move |webview, payload| {
                    if matches!(payload.event(), tauri::webview::PageLoadEvent::Finished) {
                        let app = handle.clone();
                        let source = webview.clone();
                        tauri::async_runtime::spawn(async move {
                            // Same deferred mount boundary as the existing main Router; do not emit into an unmounted Vue app.
                            tokio::time::sleep(std::time::Duration::from_millis(250)).await;
                            let ui = app.clone();
                            let _ = app.run_on_main_thread(move || {
                                let sampler = ui.state::<Arc<Sampler>>();
                                if !super::window_instance(&source).is_ok_and(|token| {
                                    sampler.window_instance_active(POPOVER_LABEL, token)
                                }) {
                                    return;
                                }
                                if let Some(state) = ui.try_state::<Popover>()
                                    && let Ok(mut state) = state.0.lock()
                                {
                                    state.ready = true;
                                }
                                let _ = present(&ui);
                            });
                        });
                    }
                })
                .build()
                .map_err(|_| error())?;
        let sampler = app.state::<Arc<Sampler>>().inner().clone();
        let instance = super::register_window(&window, &sampler)?;
        let handle = app.clone();
        window.on_window_event(move |event| match event {
            tauri::WindowEvent::Focused(false) => {
                if handle
                    .get_webview_window(POPOVER_LABEL)
                    .is_some_and(|w| w.is_visible().unwrap_or(false))
                {
                    hide(&handle);
                }
            }
            tauri::WindowEvent::Destroyed => {
                // A delayed event from an old instance must not reset a replacement popover.
                if !sampler.window_instance_matches(POPOVER_LABEL, instance) {
                    return;
                }
                if let Some(state) = handle.try_state::<Popover>()
                    && let Ok(mut state) = state.0.lock()
                {
                    *state = State::default();
                }
            }
            tauri::WindowEvent::Resized(_) | tauri::WindowEvent::ScaleFactorChanged { .. } => {
                let ui = handle.clone();
                let _ = handle.run_on_main_thread(move || {
                    let _ = reposition(&ui);
                });
            }
            _ => {}
        });
    }
    present(app)
}
fn present(app: &tauri::AppHandle) -> Result<(), AppError> {
    use tauri_specta::Event;
    let (module, generation) = {
        let state = app.state::<Popover>();
        let state = state.0.lock().map_err(|_| error())?;
        if !state.ready || !state.wanted {
            return Ok(());
        }
        (state.module.ok_or_else(error)?, state.generation)
    };
    let window = app.get_webview_window(POPOVER_LABEL).ok_or_else(error)?;
    // The minimal popover capability cannot call localeGet. Replay the existing locale event
    // after mount so its shared locale listener receives the current value before the panel shows.
    if let Some(core) = app.try_state::<Arc<crate::core::DesktopCore>>()
        && let Ok(settings) = core.current_settings()
    {
        let _ = crate::events::LocaleEvent(crate::model::LocaleChanged {
            locale: settings.locale,
        })
        .emit_to(app, POPOVER_LABEL);
    }
    // The content event is strictly emitted before show; a delayed show lets queued JS apply it.
    crate::events::MonitorPopover(module)
        .emit_to(app, POPOVER_LABEL)
        .map_err(|_| error())?;
    reposition(app)?;
    let app = app.clone();
    tauri::async_runtime::spawn(async move {
        tokio::time::sleep(std::time::Duration::from_millis(32)).await;
        let ui = app.clone();
        let _ = app.run_on_main_thread(move || {
            let state = ui.state::<Popover>();
            let Ok(mut state) = state.0.lock() else {
                return;
            };
            if state.wanted && state.generation == generation {
                state.shown = true;
                drop(state);
                let _ = window.show();
                let _ = window.set_focus();
            }
        });
    });
    Ok(())
}
pub fn reposition(app: &tauri::AppHandle) -> Result<(), AppError> {
    let anchor = app.state::<Popover>().0.lock().map_err(|_| error())?.anchor;
    let Some(anchor) = anchor else {
        return Ok(());
    };
    let Some(window) = app.get_webview_window(POPOVER_LABEL) else {
        return Ok(());
    };
    #[cfg(target_os = "macos")]
    {
        // AppKit coordinates are logical points across all screens; avoid global physical-pixel conversion on mixed DPI.
        let pointer = window.ns_window().map_err(|_| error())?;
        let native = unsafe { &*pointer.cast::<objc2_app_kit::NSWindow>() };
        let size = native.frame().size;
        let (x, y) = position(anchor, size.width, size.height);
        native.setFrameOrigin(objc2_foundation::NSPoint::new(x, y));
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = (anchor, window);
    }
    Ok(())
}
pub fn main_route(target: MonitorMainTarget, module: Option<MonitorModule>) -> String {
    match target {
        MonitorMainTarget::Settings => "/settings/monitor".into(),
        MonitorMainTarget::Detail => module
            .map(|m| format!("/monitor?detail={}", m.key()))
            .unwrap_or_else(|| "/monitor".into()),
    }
}
pub fn open_main(app: &tauri::AppHandle, target: MonitorMainTarget, module: Option<MonitorModule>) {
    hide(app);
    crate::tray::navigate(app, &main_route(target, module));
}

/// Native occlusion notification covers direct frontend hide calls that have no Tauri visibility event.
pub fn cancel_if_hidden(app: &tauri::AppHandle) {
    let shown = app
        .try_state::<Popover>()
        .is_some_and(|p| p.0.lock().is_ok_and(|s| s.shown));
    if shown
        && app
            .get_webview_window(POPOVER_LABEL)
            .is_some_and(|w| !w.is_visible().unwrap_or(false))
    {
        hide(app);
    }
}
