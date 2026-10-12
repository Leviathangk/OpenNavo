use super::sampler::{PauseReason, Sampler};
use std::sync::Arc;
#[cfg(target_os = "macos")]
pub fn install(app: &tauri::AppHandle, sampler: Arc<Sampler>) {
    use objc2::rc::Retained;
    use objc2_app_kit::NSWorkspace;
    use objc2_foundation::{
        NSDistributedNotificationCenter, NSNotificationCenter, NSOperationQueue, NSString,
    };
    use tauri::Manager;
    struct Guard {
        app: tauri::AppHandle,
        tokens: Vec<(usize, u8)>,
    }
    impl Drop for Guard {
        fn drop(&mut self) {
            let tokens = std::mem::take(&mut self.tokens);
            let _ = self.app.run_on_main_thread(move || {
                for (pointer, distributed) in tokens {
                    if let Some(token) = unsafe {
                        Retained::<objc2::runtime::AnyObject>::from_raw(pointer as *mut _)
                    } {
                        unsafe {
                            if distributed == 2 {
                                NSNotificationCenter::defaultCenter().removeObserver(&token);
                            } else if distributed == 1 {
                                NSDistributedNotificationCenter::defaultCenter()
                                    .removeObserver(&token);
                            } else {
                                NSWorkspace::sharedWorkspace()
                                    .notificationCenter()
                                    .removeObserver(&token);
                            }
                        }
                    }
                }
            });
        }
    }
    #[link(name = "CoreGraphics", kind = "framework")]
    unsafe extern "C" {
        fn CGSessionCopyCurrentDictionary() -> super::cf::Ref;
        fn CGGetOnlineDisplayList(max: u32, displays: *mut u32, count: *mut u32) -> i32;
        fn CGDisplayIsAsleep(id: u32) -> u32;
    }
    if let Some(session) = super::cf::Owned::new(unsafe { CGSessionCopyCurrentDictionary() }) {
        sampler.pause(
            PauseReason::Lock,
            super::cf::boolean(session.0, "CGSSessionScreenIsLocked").unwrap_or(false),
        );
    }
    let mut displays = [0u32; 32];
    let mut count = 0;
    if unsafe { CGGetOnlineDisplayList(32, displays.as_mut_ptr(), &mut count) } == 0 && count > 0 {
        sampler.pause(
            PauseReason::Screen,
            displays[..count.min(32) as usize]
                .iter()
                .all(|id| unsafe { CGDisplayIsAsleep(*id) } != 0),
        );
    }
    let mut tokens = Vec::new();
    for (name, reason, paused, distributed) in [
        (
            "NSWorkspaceWillSleepNotification",
            PauseReason::Sleep,
            true,
            false,
        ),
        (
            "NSWorkspaceDidWakeNotification",
            PauseReason::Sleep,
            false,
            false,
        ),
        (
            "NSWorkspaceScreensDidSleepNotification",
            PauseReason::Screen,
            true,
            false,
        ),
        (
            "NSWorkspaceScreensDidWakeNotification",
            PauseReason::Screen,
            false,
            false,
        ),
        ("com.apple.screenIsLocked", PauseReason::Lock, true, true),
        ("com.apple.screenIsUnlocked", PauseReason::Lock, false, true),
        (
            "NSWorkspaceSessionDidResignActiveNotification",
            PauseReason::Session,
            true,
            false,
        ),
        (
            "NSWorkspaceSessionDidBecomeActiveNotification",
            PauseReason::Session,
            false,
            false,
        ),
    ] {
        let sampler = sampler.clone();
        let app_handle = app.clone();
        let block = block2::RcBlock::new(
            move |_: std::ptr::NonNull<objc2_foundation::NSNotification>| {
                sampler.pause(reason, paused);
                if matches!(reason, PauseReason::Sleep | PauseReason::Session) {
                    super::fan::client::power(&app_handle, paused);
                }
                if paused {
                    super::popover::hide(&app_handle);
                }
            },
        );
        let center = if distributed {
            let c = NSDistributedNotificationCenter::defaultCenter();
            Retained::into_super(c)
        } else {
            NSWorkspace::sharedWorkspace().notificationCenter()
        };
        let token = unsafe {
            center.addObserverForName_object_queue_usingBlock(
                Some(&NSString::from_str(name)),
                None,
                Some(&NSOperationQueue::mainQueue()),
                &block,
            )
        };
        tokens.push((Retained::into_raw(token) as usize, u8::from(distributed)));
    }
    let ui = app.clone();
    let block = block2::RcBlock::new(
        move |notification: std::ptr::NonNull<objc2_foundation::NSNotification>| {
            let notification = unsafe { notification.as_ref() };
            if let Some(window) = ui.get_webview_window("main")
                && let (Ok(pointer), Some(object)) = (window.ns_window(), notification.object())
                && pointer.cast_const() == (&*object as *const objc2::runtime::AnyObject).cast()
            {
                sampler.set_window_visible("main", window.is_visible().unwrap_or(false));
            }
            if let Some(window) = ui.get_webview_window(super::POPOVER_LABEL)
                && let (Ok(pointer), Some(object)) = (window.ns_window(), notification.object())
                && pointer.cast_const() == (&*object as *const objc2::runtime::AnyObject).cast()
            {
                super::popover::cancel_if_hidden(&ui);
            }
        },
    );
    let token = unsafe {
        NSNotificationCenter::defaultCenter().addObserverForName_object_queue_usingBlock(
            Some(&NSString::from_str(
                "NSWindowDidChangeOcclusionStateNotification",
            )),
            None,
            Some(&NSOperationQueue::mainQueue()),
            &block,
        )
    };
    tokens.push((Retained::into_raw(token) as usize, 2));
    app.manage(Guard {
        app: app.clone(),
        tokens,
    });
}
#[cfg(not(target_os = "macos"))]
pub fn install(_: &tauri::AppHandle, _: Arc<Sampler>) {}
