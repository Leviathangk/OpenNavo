//! Native behavior shared by the existing updates and monitor popovers.
use crate::{AppError, monitor};
use objc2::{MainThreadMarker, rc::Retained, runtime::AnyObject};
use objc2_app_kit::{NSEvent, NSEventMask, NSEventType, NSStatusBarButton};
use objc2_foundation::NSPoint;
use std::{
    cell::RefCell,
    ptr::NonNull,
    time::{Duration, Instant},
};
use tauri::Manager;

const REOPEN_DELAY: Duration = Duration::from_secs(1);

#[derive(Default)]
struct ReopenGuard {
    last_status_click: Option<Instant>,
}
impl ReopenGuard {
    fn pointer_down(&mut self, status_item: bool, now: Instant) {
        // A subsequent click elsewhere (including the Dock) is new user intent.
        self.last_status_click = status_item.then_some(now);
    }
    fn suppress(&self, now: Instant, from_dock: bool) -> bool {
        !from_dock
            && self
                .last_status_click
                .is_some_and(|last| now.saturating_duration_since(last) < REOPEN_DELAY)
    }
}

thread_local! {
    static REOPEN: RefCell<ReopenGuard> = RefCell::default();
    // App-lifetime monitors also see the first status-item mouse-down, before
    // clicked:/mouseUp and any Reopen event, even while both panels are hidden.
    static MONITORS: RefCell<Vec<Retained<AnyObject>>> = const { RefCell::new(Vec::new()) };
}

pub(crate) fn status_click() {
    REOPEN.with(|guard| guard.borrow_mut().pointer_down(true, Instant::now()));
}

pub(crate) fn suppress_reopen() -> bool {
    let from_dock = unsafe { on_menubar_reopen_from_dock() } != 0;
    REOPEN.with(|guard| guard.borrow().suppress(Instant::now(), from_dock))
}

unsafe extern "C" {
    fn on_menubar_panel_configure(window: *mut std::ffi::c_void) -> i32;
    fn on_menubar_panel_show(window: *mut std::ffi::c_void) -> i32;
    fn on_menubar_reopen_from_dock() -> i32;
}

fn error() -> AppError {
    AppError::new("E_UNKNOWN", "menubar_panel")
}

pub(crate) fn configure(window: &tauri::WebviewWindow) {
    let pointer = match window.ns_window() {
        Ok(pointer) => pointer,
        Err(error) => {
            log::warn!(
                "Menu-bar panel {} has no native window: {error}",
                window.label()
            );
            return;
        }
    };
    match unsafe { on_menubar_panel_configure(pointer) } {
        1 => log::info!(
            "Menu-bar panel {} configured without activation",
            window.label()
        ),
        2 => log::warn!(
            "Menu-bar panel {} uses the nonactivating mouse-only fallback",
            window.label()
        ),
        _ => log::warn!(
            "Menu-bar panel {} is unavailable; keeping it hidden without failing startup",
            window.label()
        ),
    }
}

pub(crate) fn show(window: &tauri::WebviewWindow) -> Result<(), AppError> {
    if window.label() == super::LABEL {
        position_updates(window)?;
    }
    let pointer = window.ns_window().map_err(|_| error())?;
    if unsafe { on_menubar_panel_show(pointer) } == 0 {
        return Err(error());
    }
    Ok(())
}

pub(crate) fn position_updates(window: &tauri::WebviewWindow) -> Result<(), AppError> {
    let tray = window
        .app_handle()
        .tray_by_id("opennavo")
        .ok_or_else(error)?;
    let anchor = tray
        .with_inner_tray_icon(|tray| {
            let mtm = MainThreadMarker::new()?;
            let button = tray.ns_status_item()?.button(mtm)?;
            let native = button.window()?;
            let screen = native.screen()?;
            Some(monitor::popover::Anchor::new(
                native.convertRectToScreen(button.convertRect_toView(button.bounds(), None)),
                screen.visibleFrame(),
            ))
        })
        .map_err(|_| error())?
        .ok_or_else(error)?;
    let pointer = window.ns_window().map_err(|_| error())?;
    let native = unsafe { &*pointer.cast::<objc2_app_kit::NSWindow>() };
    let size = native.frame().size;
    // Modern status items may use a remote 500x500 host window. Its frame isn't
    // the button's screen rect, which can put the popover over the menu bar.
    let (x, y) = monitor::popover::position(anchor, size.width, size.height);
    native.setFrameOrigin(NSPoint::new(x, y));
    Ok(())
}

pub(crate) fn contains_button(button: &NSStatusBarButton, point: NSPoint) -> bool {
    let Some(window) = button.window() else {
        return false;
    };
    // Stay in AppKit points, including negative origins and mixed-DPI screens.
    let rect = window.convertRectToScreen(button.convertRect_toView(button.bounds(), None));
    monitor::popover::Rect {
        x: rect.origin.x,
        y: rect.origin.y,
        width: rect.size.width,
        height: rect.size.height,
    }
    .contains(point.x, point.y)
}

fn updates_at(app: &tauri::AppHandle, point: NSPoint) -> bool {
    app.tray_by_id("opennavo").is_some_and(|tray| {
        tray.with_inner_tray_icon(move |tray| {
            let Some(mtm) = MainThreadMarker::new() else {
                return false;
            };
            tray.ns_status_item()
                .and_then(|item| item.button(mtm))
                .is_some_and(|button| contains_button(&button, point))
        })
        .unwrap_or(false)
    })
}

fn inside(app: &tauri::AppHandle, label: &str, point: NSPoint) -> bool {
    app.get_webview_window(label)
        .and_then(|window| window.ns_window().ok())
        .is_some_and(|pointer| {
            let window = unsafe { &*pointer.cast::<objc2_app_kit::NSWindow>() };
            let rect = window.frame();
            window.isVisible()
                && monitor::popover::Rect {
                    x: rect.origin.x,
                    y: rect.origin.y,
                    width: rect.size.width,
                    height: rect.size.height,
                }
                .contains(point.x, point.y)
        })
}

fn mouse_down(app: &tauri::AppHandle, event: &NSEvent, mtm: MainThreadMarker) {
    let point = NSEvent::mouseLocation();
    let updates = updates_at(app, point);
    let status = updates || monitor::menubar::contains_point(point, mtm);
    REOPEN.with(|guard| guard.borrow_mut().pointer_down(status, Instant::now()));
    // Both global and local monitors must exclude status buttons. A status click
    // can arrive globally with no local NSWindow while our app is inactive.
    if status && event.r#type() == NSEventType::LeftMouseDown {
        return;
    }
    if monitor::popover::wants_visible(app) && !inside(app, monitor::POPOVER_LABEL, point) {
        monitor::popover::hide(app); // Also cancels a not-yet-mounted/pending show.
    }
    if app
        .get_webview_window(super::LABEL)
        .is_some_and(|window| window.is_visible().unwrap_or(false))
        && !inside(app, super::LABEL, point)
    {
        super::hide(app);
    }
}

fn escape(app: &tauri::AppHandle) -> bool {
    let visible = [super::LABEL, monitor::POPOVER_LABEL].iter().any(|label| {
        app.get_webview_window(label)
            .is_some_and(|window| window.is_visible().unwrap_or(false))
    });
    if visible {
        monitor::popover::hide(app);
        super::hide(app);
    }
    visible
}

pub(crate) fn install(app: &tauri::AppHandle) {
    let Some(mtm) = MainThreadMarker::new() else {
        return;
    };
    MONITORS.with(|monitors| {
        let mut monitors = monitors.borrow_mut();
        if !monitors.is_empty() {
            return;
        }
        let clicks =
            NSEventMask::LeftMouseDown | NSEventMask::RightMouseDown | NSEventMask::OtherMouseDown;
        let handle = app.clone();
        let global = block2::RcBlock::new(move |event: NonNull<NSEvent>| {
            mouse_down(&handle, unsafe { event.as_ref() }, mtm);
        });
        monitors.extend(NSEvent::addGlobalMonitorForEventsMatchingMask_handler(
            clicks, &global,
        ));
        let handle = app.clone();
        let local = block2::RcBlock::new(move |event: NonNull<NSEvent>| {
            let native = unsafe { event.as_ref() };
            if native.r#type() == NSEventType::KeyDown {
                if native.keyCode() == 53 && escape(&handle) {
                    return std::ptr::null_mut();
                }
            } else {
                mouse_down(&handle, native, mtm);
            }
            event.as_ptr()
        });
        monitors.extend(unsafe {
            NSEvent::addLocalMonitorForEventsMatchingMask_handler(
                clicks | NSEventMask::KeyDown,
                &local,
            )
        });
    });
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn reopen_is_suppressed_only_during_the_status_click_window() {
        let now = Instant::now();
        let mut guard = ReopenGuard::default();
        assert!(!guard.suppress(now, false));
        guard.pointer_down(true, now);
        assert!(guard.suppress(now, false));
        assert!(guard.suppress(now + Duration::from_millis(999), false));
        assert!(!guard.suppress(now + REOPEN_DELAY, false));
        // The second click of a double click refreshes the protection.
        guard.pointer_down(true, now + Duration::from_millis(200));
        assert!(guard.suppress(now + REOPEN_DELAY, false));
        assert!(!guard.suppress(now + REOPEN_DELAY + Duration::from_millis(200), false));
    }

    #[test]
    fn subsequent_dock_or_outside_click_allows_immediate_reopen() {
        let now = Instant::now();
        let mut guard = ReopenGuard::default();
        guard.pointer_down(true, now);
        guard.pointer_down(false, now + Duration::from_millis(50));
        assert!(!guard.suppress(now + Duration::from_millis(51), false));
        guard.pointer_down(true, now + Duration::from_millis(100));
        assert!(guard.suppress(now + Duration::from_millis(101), false));
    }

    #[test]
    fn an_explicit_dock_reopen_does_not_need_a_mouse_event() {
        let now = Instant::now();
        let mut guard = ReopenGuard::default();
        guard.pointer_down(true, now);
        assert!(!guard.suppress(now + Duration::from_millis(50), true));
        assert!(guard.suppress(now + Duration::from_millis(50), false));
    }
}
