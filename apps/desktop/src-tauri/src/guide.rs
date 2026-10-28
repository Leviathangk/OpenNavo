//! Permission overlay (06 §12.7): after opening System Settings, show a small overlay beneath its window,
//! with a draggable OpenNavo icon and instructions; poll permissions, then show completion and close automatically.
//! The overlay never activates OpenNavo, so System Settings stays the front app.
use crate::{AppError, core::DesktopCore, events::PermissionsChanged, model::*};
use std::{
    path::{Path, PathBuf},
    sync::{
        Arc,
        atomic::{AtomicU64, Ordering},
    },
    time::{Duration, Instant},
};
use tauri::{LogicalPosition, LogicalSize, Manager, WebviewUrl, WebviewWindowBuilder};
use tauri_specta::Event;

pub const LABEL: &str = "permission-guide";
/// Window size includes 12 px transparent margins around the 376 px wide panel; frontend draws corners/shadows.
pub const WIDTH: f64 = 400.;
/// Height for three single-line rows; longer translations wrap, and the page reports the height it needs (`fit`).
pub const HEIGHT: f64 = 122.;
const MIN_HEIGHT: f64 = 100.;
const MAX_HEIGHT: f64 = 240.;
const POLL: Duration = Duration::from_millis(250);
/// Check permissions every two iterations (about 0.5 seconds).
const STATUS_EVERY: u32 = 2;
/// Delay before closing after showing Enabled.
const DONE_DELAY: Duration = Duration::from_millis(1600);
/// Full Disk Access applies only after OpenNavo reopens, and macOS asks to Quit & Reopen right away: leave the hint
/// that says to click it up for longer.
const RELAUNCH_DONE_DELAY: Duration = Duration::from_secs(6);
/// Treat System Settings as closed if its window is absent longer than this.
const SETTINGS_GONE: Duration = Duration::from_millis(1500);
/// Show at screen bottom if System Settings cannot be found.
const FALLBACK_AFTER: Duration = Duration::from_millis(1500);
/// Show the window once the page has reported its height, or after this long without it.
const FIT_WAIT: Duration = Duration::from_millis(1000);
const MAX_LIFETIME: Duration = Duration::from_secs(600);

/// Increment generation on every open/close; old polling threads exit when the generation changes.
#[derive(Default)]
pub struct Guide {
    generation: AtomicU64,
    /// Height the page asked for (`f64` bits); 0 until the current window reports one.
    height: AtomicU64,
}

impl Guide {
    fn height(&self) -> Option<f64> {
        let height = f64::from_bits(self.height.load(Ordering::Acquire));
        (height > 0.).then_some(height)
    }
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Frame {
    pub x: f64,
    pub y: f64,
    pub width: f64,
    pub height: f64,
}

impl Frame {
    fn contains_center_of(&self, other: Frame) -> bool {
        let (x, y) = (other.x + other.width / 2., other.y + other.height / 2.);
        x >= self.x && x < self.x + self.width && y >= self.y && y < self.y + self.height
    }
}

fn slug(pane: PrivacyPane) -> Result<&'static str, AppError> {
    match pane {
        PrivacyPane::AppManagement => Ok("app_management"),
        PrivacyPane::FullDiskAccess => Ok("full_disk_access"),
        PrivacyPane::Notifications => Err(AppError::new("E_INVALID_ARG", "guide_pane")),
    }
}

pub fn granted(pane: PrivacyPane, permissions: &Permissions) -> bool {
    match pane {
        PrivacyPane::AppManagement => permissions.app_management_switch == PermissionState::Granted,
        PrivacyPane::FullDiskAccess => permissions.full_disk_access == PermissionState::Granted,
        PrivacyPane::Notifications => false,
    }
}

/// On in System Settings, but this process gets it only after reopening.
pub fn pending(pane: PrivacyPane, permissions: &Permissions) -> bool {
    match pane {
        PrivacyPane::AppManagement => permissions.app_management_pending,
        PrivacyPane::FullDiskAccess => permissions.full_disk_access_pending,
        PrivacyPane::Notifications => false,
    }
}

/// The user's part is done once the pane is on: in effect, or waiting for a reopen. Full Disk Access is always the latter,
/// since macOS applies it to new processes only.
pub fn done(pane: PrivacyPane, permissions: &Permissions) -> bool {
    granted(pane, permissions) || pending(pane, permissions)
}

/// Center below System Settings' content area (right of sidebar); if insufficient space, align inside its bottom edge, staying within usable screen bounds.
pub fn anchor(settings: Frame, screen: Frame, height: f64) -> (f64, f64) {
    const GAP: f64 = 2.;
    const INSET: f64 = 8.;
    let sidebar = (settings.width * 0.3).clamp(180., 260.);
    let column_width = (settings.width - sidebar).max(0.);
    let x = settings.x + sidebar + (column_width - WIDTH) / 2.;
    let below = settings.y + settings.height + GAP;
    let y = if below + height <= screen.y + screen.height {
        below
    } else {
        settings.y + settings.height - height - INSET
    };
    let max_x = (screen.x + screen.width - WIDTH).max(screen.x);
    let max_y = (screen.y + screen.height - height).max(screen.y);
    (x.clamp(screen.x, max_x), y.clamp(screen.y, max_y))
}

/// Center at screen bottom if System Settings cannot be found.
pub fn fallback(screen: Frame, height: f64) -> (f64, f64) {
    (
        screen.x + (screen.width - WIDTH) / 2.,
        (screen.y + screen.height - height - 72.).max(screen.y),
    )
}

/// Height the page needs, kept within bounds.
pub fn fitted_height(height: u32) -> f64 {
    f64::from(height).clamp(MIN_HEIGHT, MAX_HEIGHT)
}

/// Innermost .app bundle containing an executable.
pub fn app_bundle(executable: &Path) -> Option<PathBuf> {
    executable
        .ancestors()
        .find(|path| path.extension().is_some_and(|extension| extension == "app"))
        .map(Path::to_path_buf)
}

/// The app that belongs in the list: macOS checks privacy permissions against the process responsible for this one.
/// That is OpenNavo.app when it was opened normally. A development build is a bare binary started from a terminal,
/// so macOS checks the app it was started from (Paseo, Terminal…) instead. `None` if that is not an app bundle.
pub fn owner_bundle() -> Option<PathBuf> {
    responsible_executable()
        .or_else(|| std::env::current_exe().ok())
        .and_then(|executable| app_bundle(&executable))
}

/// Name of the owner app when it is not OpenNavo itself, as for a development build; the overlay then names that app.
pub fn foreign_owner() -> Option<(String, PathBuf)> {
    let owner = owner_bundle()?;
    let own = std::env::current_exe()
        .ok()
        .and_then(|executable| app_bundle(&executable));
    (own.as_deref() != Some(owner.as_path())).then(|| (display_name(&owner), owner))
}

fn error(error: impl std::fmt::Display) -> AppError {
    AppError::new("E_UNKNOWN", "permission_guide").with_detail(error.to_string())
}

pub async fn open(app: &tauri::AppHandle, pane: PrivacyPane) -> Result<(), AppError> {
    let slug = slug(pane)?;
    let url = crate::system::privacy_url(pane, &app.config().identifier)?;
    // Create the overlay before System Settings opens: wry activates OpenNavo whenever it creates a webview, and doing that
    // right after the open cancels the activation macOS is handing to System Settings, leaving OpenNavo's window on top.
    let generation = prepare(app, slug)?;
    if let Err(error) = crate::system::open(&[url]).await {
        close(app);
        return Err(error);
    }
    watch(app.clone(), pane, generation);
    Ok(())
}

/// Create the hidden overlay, or point an open one at another pane; returns the generation its polling thread runs under.
fn prepare(app: &tauri::AppHandle, slug: &str) -> Result<u64, AppError> {
    let guide = app.state::<Arc<Guide>>();
    let generation = guide.generation.fetch_add(1, Ordering::AcqRel) + 1;
    match app.get_webview_window(LABEL) {
        // If already open, change only content; the page reports its new height and the new polling thread continues positioning.
        Some(window) => window
            .eval(format!("location.hash = '#/permission-guide/{slug}'"))
            .map_err(error)?,
        None => {
            guide.height.store(0, Ordering::Release);
            let window = WebviewWindowBuilder::new(
                app,
                LABEL,
                WebviewUrl::App(format!("index.html#/permission-guide/{slug}").into()),
            )
            .title("OpenNavo")
            .inner_size(WIDTH, HEIGHT)
            .decorations(false)
            .transparent(true)
            // Frontend draws corners/shadows; native shadows would expose rectangular transparent window bounds.
            .shadow(false)
            .always_on_top(true)
            .visible_on_all_workspaces(true)
            .visible(false)
            .resizable(false)
            .skip_taskbar(true)
            // Do not steal focus from System Settings; the first click should begin dragging the icon.
            .focused(false)
            .focusable(false)
            .accept_first_mouse(true)
            .build()
            .map_err(error)?;
            // Queued before the polling thread can show it, so the first click already leaves System Settings in front.
            #[cfg(target_os = "macos")]
            {
                let overlay = window.clone();
                window
                    .run_on_main_thread(move || native::prevent_activation(&overlay))
                    .map_err(error)?;
            }
        }
    }
    Ok(generation)
}

/// The page's text wraps differently in each language: it reports the height it needs and polling resizes the window.
pub fn fit(app: &tauri::AppHandle, height: u32) {
    if let Some(guide) = app.try_state::<Arc<Guide>>() {
        guide
            .height
            .store(fitted_height(height).to_bits(), Ordering::Release);
    }
}

pub fn close(app: &tauri::AppHandle) {
    if let Some(guide) = app.try_state::<Arc<Guide>>() {
        guide.generation.fetch_add(1, Ordering::AcqRel);
    }
    if let Some(window) = app.get_webview_window(LABEL) {
        let _ = window.close();
    }
}

fn screens(app: &tauri::AppHandle) -> Vec<Frame> {
    app.available_monitors()
        .unwrap_or_default()
        .iter()
        .map(|monitor| {
            let scale = monitor.scale_factor();
            let area = monitor.work_area();
            Frame {
                x: f64::from(area.position.x) / scale,
                y: f64::from(area.position.y) / scale,
                width: f64::from(area.size.width) / scale,
                height: f64::from(area.size.height) / scale,
            }
        })
        .collect()
}

/// Polling: follow System Settings, size the window to the page, push permission changes, close shortly after the pane is
/// on or when Settings closes/times out.
fn watch(app: tauri::AppHandle, pane: PrivacyPane, generation: u64) {
    std::thread::spawn(move || {
        let started = Instant::now();
        let mut tick: u32 = 0;
        let mut last: Option<Permissions> = None;
        let mut position: Option<(f64, f64)> = None;
        let mut size: Option<f64> = None;
        let mut shown = false;
        let mut seen_settings = false;
        let mut settings_missing: Option<Instant> = None;
        let mut done_at: Option<(Instant, Duration)> = None;
        while let Some(guide) = app.try_state::<Arc<Guide>>() {
            if guide.generation.load(Ordering::Acquire) != generation {
                break;
            }
            let Some(window) = app.get_webview_window(LABEL) else {
                break;
            };
            let now = Instant::now();
            let fitted = guide.height();
            let height = fitted.unwrap_or(HEIGHT);
            if size != Some(height) {
                let _ = window.set_size(LogicalSize::new(WIDTH, height));
                size = Some(height);
                // macOS keeps the bottom edge when resizing; place the top edge again right away.
                position = None;
            }
            let next = match settings_frame() {
                Some(frame) => {
                    seen_settings = true;
                    settings_missing = None;
                    let screens = screens(&app);
                    screens
                        .iter()
                        .find(|screen| screen.contains_center_of(frame))
                        .or(screens.first())
                        .map(|screen| anchor(frame, *screen, height))
                }
                None if seen_settings => {
                    if now.duration_since(*settings_missing.get_or_insert(now)) > SETTINGS_GONE {
                        close(&app);
                        break;
                    }
                    None
                }
                // Not placed yet, or resized since: bottom center of the screen.
                None if position.is_none() && now.duration_since(started) > FALLBACK_AFTER => {
                    screens(&app)
                        .first()
                        .map(|screen| fallback(*screen, height))
                }
                None => None,
            };
            if let Some(next) = next
                && position != Some(next)
            {
                let _ = window.set_position(LogicalPosition::new(next.0, next.1));
                position = Some(next);
            }
            // Wait for the page's height so long translations never show clipped first.
            if position.is_some()
                && !shown
                && (fitted.is_some() || now.duration_since(started) > FIT_WAIT)
            {
                present(&window);
                shown = true;
            }
            if tick.is_multiple_of(STATUS_EVERY)
                && let Some(core) = app.try_state::<Arc<DesktopCore>>()
                && let Ok(status) = core.permissions_blocking()
            {
                if last != Some(status) {
                    let _ = PermissionsChanged(status).emit(&app);
                    last = Some(status);
                }
                if done_at.is_none() && done(pane, &status) {
                    let delay = if granted(pane, &status) {
                        DONE_DELAY
                    } else {
                        RELAUNCH_DONE_DELAY
                    };
                    done_at = Some((now, delay));
                }
            }
            if done_at.is_some_and(|(at, delay)| now.duration_since(at) > delay)
                || now.duration_since(started) > MAX_LIFETIME
            {
                close(&app);
                break;
            }
            tick = tick.wrapping_add(1);
            std::thread::sleep(POLL);
        }
    });
}

#[cfg(target_os = "macos")]
fn settings_frame() -> Option<Frame> {
    use objc2::{rc::Retained, runtime::AnyObject};
    use objc2_foundation::{NSArray, NSDictionary, NSNumber, NSString};
    use std::collections::HashMap;

    #[link(name = "CoreGraphics", kind = "framework")]
    unsafe extern "C" {
        fn CGWindowListCopyWindowInfo(
            option: u32,
            relative_to_window: u32,
        ) -> *mut NSArray<NSDictionary<NSString, AnyObject>>;
    }
    const ON_SCREEN_ONLY: u32 = 1;
    const EXCLUDE_DESKTOP_ELEMENTS: u32 = 1 << 4;

    // SAFETY: Copy returns a +1 array, toll-free bridged with NSArray; Retained releases it.
    let windows = unsafe {
        Retained::from_raw(CGWindowListCopyWindowInfo(
            ON_SCREEN_ONLY | EXCLUDE_DESKTOP_ELEMENTS,
            0,
        ))
    }?;
    let number = |info: &NSDictionary<NSString, AnyObject>, key: &str| {
        info.objectForKey(&NSString::from_str(key))
            .and_then(|value| value.downcast::<NSNumber>().ok())
    };
    let mut owners: HashMap<i32, bool> = HashMap::new();
    // Windows are ordered front to back; the first match is the foremost System Settings window.
    for info in windows.iter() {
        if number(&info, "kCGWindowLayer").map(|layer| layer.integerValue()) != Some(0) {
            continue;
        }
        let Some(pid) = number(&info, "kCGWindowOwnerPID").map(|pid| pid.intValue()) else {
            continue;
        };
        if !*owners.entry(pid).or_insert_with(|| is_system_settings(pid)) {
            continue;
        }
        let bounds = info
            .objectForKey(&NSString::from_str("kCGWindowBounds"))?
            .downcast::<NSDictionary>()
            .ok()?;
        let value = |key: &str| {
            let key = NSString::from_str(key);
            bounds
                .objectForKey(&key)
                .and_then(|value| value.downcast::<NSNumber>().ok())
                .map(|value| value.doubleValue())
        };
        let frame = Frame {
            x: value("X")?,
            y: value("Y")?,
            width: value("Width")?,
            height: value("Height")?,
        };
        // Skip small windows such as tooltips and menus.
        if frame.width >= 320. && frame.height >= 240. {
            return Some(frame);
        }
    }
    None
}

#[cfg(target_os = "macos")]
fn is_system_settings(pid: i32) -> bool {
    process_path(pid).is_some_and(|path| {
        path.ends_with("System Settings.app/Contents/MacOS/System Settings")
            || path.ends_with("System Preferences.app/Contents/MacOS/System Preferences")
    })
}

#[cfg(target_os = "macos")]
fn process_path(pid: i32) -> Option<PathBuf> {
    use std::os::unix::ffi::OsStrExt;
    unsafe extern "C" {
        fn proc_pidpath(pid: i32, buffer: *mut std::ffi::c_void, size: u32) -> i32;
    }
    let mut buffer = [0u8; 4096];
    // SAFETY: buffer size is PROC_PIDPATHINFO_MAXSIZE; read only the returned length.
    let length = unsafe { proc_pidpath(pid, buffer.as_mut_ptr().cast(), buffer.len() as u32) };
    let length = usize::try_from(length).ok().filter(|length| *length > 0)?;
    Some(PathBuf::from(std::ffi::OsStr::from_bytes(
        buffer.get(..length)?,
    )))
}

/// Executable of the process macOS holds responsible for this one: this process when opened from Finder or the Dock,
/// otherwise the app it was started from. The libsystem call is private, so it is looked up at run time.
#[cfg(target_os = "macos")]
fn responsible_executable() -> Option<PathBuf> {
    use std::ffi::{c_char, c_void};
    unsafe extern "C" {
        fn dlsym(handle: *mut c_void, symbol: *const c_char) -> *mut c_void;
    }
    const RTLD_DEFAULT: *mut c_void = -2_isize as *mut c_void;
    // SAFETY: dlsym only looks the NUL-terminated name up in the loaded images.
    let symbol = unsafe {
        dlsym(
            RTLD_DEFAULT,
            c"responsibility_get_pid_responsible_for_pid".as_ptr(),
        )
    };
    if symbol.is_null() {
        return None;
    }
    // SAFETY: the symbol is `pid_t responsibility_get_pid_responsible_for_pid(pid_t)`, unchanged since macOS 10.14.
    let responsible: unsafe extern "C" fn(i32) -> i32 = unsafe { std::mem::transmute(symbol) };
    let pid = i32::try_from(std::process::id()).ok()?;
    // SAFETY: takes and returns a plain pid; a failure returns -1.
    let pid = unsafe { responsible(pid) };
    (pid > 0).then(|| process_path(pid)).flatten()
}

/// The name Finder and System Settings show for an app bundle.
#[cfg(target_os = "macos")]
fn display_name(bundle: &Path) -> String {
    let path = objc2_foundation::NSString::from_str(&bundle.to_string_lossy());
    let name = objc2_foundation::NSFileManager::defaultManager()
        .displayNameAtPath(&path)
        .to_string();
    // With "Show all filename extensions" on, Finder's name keeps ".app"; System Settings never shows it.
    match name.strip_suffix(".app") {
        Some(stem) if !stem.is_empty() => stem.to_owned(),
        _ => name,
    }
}

#[cfg(not(target_os = "macos"))]
fn settings_frame() -> Option<Frame> {
    None
}

#[cfg(not(target_os = "macos"))]
fn responsible_executable() -> Option<PathBuf> {
    None
}

#[cfg(not(target_os = "macos"))]
fn display_name(bundle: &Path) -> String {
    bundle
        .file_stem()
        .map(|stem| stem.to_string_lossy().into_owned())
        .unwrap_or_default()
}

/// Show the overlay without making OpenNavo the active app, so System Settings stays in front.
#[cfg(target_os = "macos")]
fn present(window: &tauri::WebviewWindow) {
    let overlay = window.clone();
    if window
        .run_on_main_thread(move || native::present(&overlay))
        .is_err()
    {
        let _ = window.show();
    }
}

#[cfg(not(target_os = "macos"))]
fn present(window: &tauri::WebviewWindow) {
    let _ = window.show();
}

/// Drag the owner app (`owner_bundle`) from the overlay into System Settings privacy lists; call on the main thread while
/// the mouse is down.
#[cfg(target_os = "macos")]
pub fn drag(window: &tauri::WebviewWindow) -> Result<(), AppError> {
    native::begin(window)
}

#[cfg(not(target_os = "macos"))]
pub fn drag(_window: &tauri::WebviewWindow) -> Result<(), AppError> {
    Err(AppError::new("E_INVALID_ARG", "guide_drag_platform"))
}

#[cfg(target_os = "macos")]
mod native {
    use super::*;
    use objc2::{
        AnyThread, MainThreadMarker, MainThreadOnly, define_class, msg_send,
        rc::Retained,
        runtime::{AnyObject, ProtocolObject},
        sel,
    };
    use objc2_app_kit::{
        NSApplication, NSDragOperation, NSDraggingContext, NSDraggingItem, NSDraggingSession,
        NSDraggingSource, NSEventType, NSView, NSWindow, NSWorkspace,
    };
    use objc2_foundation::{
        NSArray, NSObject, NSObjectProtocol, NSPoint, NSRect, NSSize, NSString, NSURL,
    };

    /// A click on a window of an inactive app makes the window server activate that app, which brought OpenNavo's main
    /// window over System Settings as soon as the icon was pressed. Non-activating NSPanels avoid that with a window-server
    /// tag that `-[NSWindow _setPreventsActivation:]` sets on any window, so set it on Tao's own window: its class, ivars and
    /// overrides stay as Tao built them. (A non-NSPanel window drops the NonactivatingPanel style bit, so that is not used.)
    pub fn prevent_activation(window: &tauri::WebviewWindow) {
        let Ok(pointer) = window.ns_window() else {
            return;
        };
        // SAFETY: Tauri supplies this window's NSWindow pointer, alive as long as the WebviewWindow; this runs on the main thread.
        let Some(ns_window) = (unsafe { pointer.cast::<NSWindow>().as_ref() }) else {
            return;
        };
        let prevents = sel!(_setPreventsActivation:);
        if !ns_window.respondsToSelector(prevents) {
            log::warn!(
                "Permission overlay clicks may activate OpenNavo: no _setPreventsActivation:"
            );
            return;
        }
        // SAFETY: private NSWindow setter taking a BOOL (`v@:B`), checked to exist above.
        unsafe {
            let _: () = msg_send![ns_window, _setPreventsActivation: true];
        }
    }

    /// Tao's `show` is `makeKeyAndOrderFront:`. Like the menu-bar panels and the firewall alert, order the overlay in
    /// without touching the key window or the active app.
    pub fn present(window: &tauri::WebviewWindow) {
        let Ok(pointer) = window.ns_window() else {
            return;
        };
        // SAFETY: Tauri supplies this window's NSWindow pointer, alive as long as the WebviewWindow; this runs on the main thread.
        if let Some(ns_window) = unsafe { pointer.cast::<NSWindow>().as_ref() } {
            ns_window.orderFrontRegardless();
        }
    }

    define_class!(
        // SAFETY: NSObject imposes no subclassing requirements; DragSource does not implement Drop.
        #[unsafe(super = NSObject)]
        #[thread_kind = MainThreadOnly]
        #[name = "OpenNavoPermissionDragSource"]
        struct DragSource;

        // SAFETY: NSObjectProtocol imposes no additional requirements.
        unsafe impl NSObjectProtocol for DragSource {}

        // SAFETY: method signature matches NSDraggingSource.
        unsafe impl NSDraggingSource for DragSource {
            #[unsafe(method(draggingSession:sourceOperationMaskForDraggingContext:))]
            fn source_operation_mask(
                &self,
                _session: &NSDraggingSession,
                _context: NSDraggingContext,
            ) -> NSDragOperation {
                // Do not offer move/delete: dragging to Trash or folders must not move OpenNavo itself.
                NSDragOperation::Copy | NSDragOperation::Link | NSDragOperation::Generic
            }
        }
    );

    impl DragSource {
        fn new(mtm: MainThreadMarker) -> Retained<Self> {
            let this = Self::alloc(mtm).set_ivars(());
            // SAFETY: NSObject init takes no arguments.
            unsafe { msg_send![super(this), init] }
        }
    }

    thread_local! {
        // The drag source must survive the session; one per process suffices.
        static SOURCE: std::cell::OnceCell<Retained<DragSource>> = const { std::cell::OnceCell::new() };
    }

    pub fn begin(window: &tauri::WebviewWindow) -> Result<(), AppError> {
        let fail = |reason: &str| AppError::new("E_UNKNOWN", reason);
        let mtm = MainThreadMarker::new().ok_or_else(|| fail("guide_drag_thread"))?;
        let bundle = super::owner_bundle().ok_or_else(|| fail("guide_drag_bundle"))?;
        let event = NSApplication::sharedApplication(mtm)
            .currentEvent()
            .ok_or_else(|| fail("guide_drag_event"))?;
        let kind = event.r#type();
        // Start only during mouse-down/drag events, otherwise AppKit throws.
        if kind != NSEventType::LeftMouseDown && kind != NSEventType::LeftMouseDragged {
            return Err(fail("guide_drag_event"));
        }
        let pointer = window.ns_view().map_err(super::error)?.cast::<NSView>();
        // SAFETY: Tauri supplies this window's content-view pointer; use on the main thread while the window remains alive throughout the call.
        let view = unsafe { pointer.as_ref() }.ok_or_else(|| fail("guide_drag_view"))?;
        let path = NSString::from_str(&bundle.to_string_lossy());
        let url = NSURL::fileURLWithPath(&path);
        let item = NSDraggingItem::initWithPasteboardWriter(
            NSDraggingItem::alloc(),
            ProtocolObject::from_ref(&*url),
        );
        let icon = NSWorkspace::sharedWorkspace().iconForFile(&path);
        icon.setSize(NSSize::new(64., 64.));
        let location = view.convertPoint_fromView(event.locationInWindow(), None);
        let frame = NSRect::new(
            NSPoint::new(location.x - 32., location.y - 32.),
            NSSize::new(64., 64.),
        );
        let contents: &AnyObject = &icon;
        // SAFETY: contents is NSImage, satisfying dragging-item content requirements.
        unsafe { item.setDraggingFrame_contents(frame, Some(contents)) };
        let source = SOURCE.with(|cell| cell.get_or_init(|| DragSource::new(mtm)).clone());
        let items = NSArray::from_retained_slice(&[item]);
        view.beginDraggingSessionWithItems_event_source(
            &items,
            &event,
            ProtocolObject::from_ref(&*source),
        );
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const SCREEN: Frame = Frame {
        x: 0.,
        y: 25.,
        width: 1512.,
        height: 920.,
    };

    #[test]
    fn anchors_below_the_settings_content_column() {
        let settings = Frame {
            x: 300.,
            y: 120.,
            width: 715.,
            height: 600.,
        };
        let (x, y) = anchor(settings, SCREEN, HEIGHT);
        // Sidebar about 215; content center = 300 + 215 + 250 = 765; overlay left = 765 - 200.
        assert!((x - 565.).abs() < 1.);
        assert!((y - 722.).abs() < 1.);
    }

    #[test]
    fn falls_inside_the_window_when_there_is_no_room_below() {
        let settings = Frame {
            x: 300.,
            y: 300.,
            width: 715.,
            height: 640.,
        };
        let (_, y) = anchor(settings, SCREEN, HEIGHT);
        assert!((y - (300. + 640. - HEIGHT - 8.)).abs() < 1.);
        assert!(y + HEIGHT <= SCREEN.y + SCREEN.height);
        // A taller window for wrapped translations still keeps its bottom edge on screen.
        let (_, y) = anchor(settings, SCREEN, 180.);
        assert!(y + 180. <= SCREEN.y + SCREEN.height);
    }

    #[test]
    fn stays_on_screen_and_falls_back_to_the_bottom_center() {
        let settings = Frame {
            x: -200.,
            y: 40.,
            width: 400.,
            height: 300.,
        };
        let (x, _) = anchor(settings, SCREEN, HEIGHT);
        assert!(x >= SCREEN.x);
        let (x, y) = fallback(SCREEN, HEIGHT);
        assert!((x - (1512. - WIDTH) / 2.).abs() < 1.);
        assert!(y + HEIGHT < SCREEN.y + SCREEN.height);
    }

    #[test]
    fn page_heights_stay_within_bounds() {
        assert_eq!(fitted_height(141), 141.);
        assert_eq!(fitted_height(24), MIN_HEIGHT);
        assert_eq!(fitted_height(4000), MAX_HEIGHT);
    }

    /// Exercise real window-list/process-path reads: None with Settings closed; valid bounds when open.
    #[cfg(target_os = "macos")]
    #[test]
    fn reads_the_window_list_without_crashing() {
        if let Some(frame) = settings_frame() {
            assert!(frame.width >= 320. && frame.height >= 240.);
        }
        assert!(!is_system_settings(std::process::id() as i32));
    }

    #[test]
    fn the_owner_is_the_innermost_app_bundle() {
        assert_eq!(
            app_bundle(Path::new(
                "/Applications/OpenNavo.app/Contents/MacOS/opennavo-desktop"
            )),
            Some(PathBuf::from("/Applications/OpenNavo.app"))
        );
        assert_eq!(
            app_bundle(Path::new(
                "/Applications/Host.app/Contents/Library/LoginItems/Helper.app/Contents/MacOS/Helper"
            )),
            Some(PathBuf::from(
                "/Applications/Host.app/Contents/Library/LoginItems/Helper.app"
            ))
        );
        // A development build: a bare binary has no bundle of its own.
        assert_eq!(
            app_bundle(Path::new(
                "/src/apps/desktop/src-tauri/target/debug/opennavo-desktop"
            )),
            None
        );
    }

    /// Test binaries are bare executables started from a terminal, like a development build: when the terminal is an app,
    /// it is the owner, and it is named.
    #[cfg(target_os = "macos")]
    #[test]
    fn a_bare_binary_is_owned_by_the_app_it_was_started_from() {
        assert!(responsible_executable().is_some());
        if let Some(bundle) = owner_bundle() {
            assert!(
                bundle
                    .extension()
                    .is_some_and(|extension| extension == "app")
            );
            let (name, foreign) =
                foreign_owner().expect("a test binary is not inside the owner app");
            assert_eq!(foreign, bundle);
            assert!(!name.is_empty() && !name.ends_with(".app"));
        }
    }

    #[test]
    fn only_privacy_panes_have_a_guide() {
        let permissions = Permissions {
            app_management: PermissionState::Granted,
            app_management_switch: PermissionState::Granted,
            full_disk_access: PermissionState::Denied,
            relaunch_required: false,
            app_management_pending: false,
            full_disk_access_pending: false,
        };
        assert!(granted(PrivacyPane::AppManagement, &permissions));
        let effective_only = Permissions {
            app_management_switch: PermissionState::Denied,
            full_disk_access: PermissionState::Granted,
            ..permissions
        };
        assert!(!done(PrivacyPane::AppManagement, &effective_only));
        assert!(!granted(PrivacyPane::FullDiskAccess, &permissions));
        assert!(!done(PrivacyPane::FullDiskAccess, &permissions));
        assert!(!done(PrivacyPane::Notifications, &permissions));
        assert!(slug(PrivacyPane::Notifications).is_err());
        assert_eq!(
            slug(PrivacyPane::FullDiskAccess).ok(),
            Some("full_disk_access")
        );
    }

    /// Full Disk Access turned on while OpenNavo runs stays denied until it reopens: the guide still finishes.
    #[test]
    fn full_disk_access_waiting_for_a_reopen_completes_the_guide() {
        let permissions = Permissions {
            app_management: PermissionState::Denied,
            app_management_switch: PermissionState::Denied,
            full_disk_access: PermissionState::Denied,
            relaunch_required: true,
            app_management_pending: false,
            full_disk_access_pending: true,
        };
        assert!(!granted(PrivacyPane::FullDiskAccess, &permissions));
        assert!(pending(PrivacyPane::FullDiskAccess, &permissions));
        assert!(done(PrivacyPane::FullDiskAccess, &permissions));
        assert!(!done(PrivacyPane::AppManagement, &permissions));
    }
}
