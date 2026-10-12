#[cfg(target_os = "macos")]
pub fn install(app: &tauri::AppHandle) {
    use objc2::rc::Retained;
    use objc2_app_kit::NSWorkspace;
    use objc2_foundation::{NSNotificationCenter, NSOperationQueue, NSString};
    use std::sync::Arc;
    use tauri::Manager;
    let Some(core) = app.try_state::<Arc<crate::core::DesktopCore>>() else {
        return;
    };
    app.manage(super::fsevents::start(&core.queue.uninstall_engine));
    let weak = Arc::downgrade(&core.queue.uninstall_engine);
    let startup = weak.clone();
    tauri::async_runtime::spawn(async move {
        if let Some(engine) = startup.upgrade() {
            let scan = engine.clone();
            let _ = crate::blocking(move || {
                scan.refresh(crate::model::LocalAppsChangedReason::Startup)
            })
            .await;
            let _ = engine.hydrate_catalog().await;
        }
    });
    struct Guard {
        app: tauri::AppHandle,
        tokens: Vec<usize>,
    }
    impl Drop for Guard {
        fn drop(&mut self) {
            let tokens = std::mem::take(&mut self.tokens);
            let _ = self.app.run_on_main_thread(move || {
                for pointer in tokens {
                    if let Some(token) = unsafe {
                        Retained::<objc2::runtime::AnyObject>::from_raw(pointer as *mut _)
                    } {
                        unsafe {
                            NSWorkspace::sharedWorkspace()
                                .notificationCenter()
                                .removeObserver(&token);
                        }
                    }
                }
            });
        }
    }
    let center: Retained<NSNotificationCenter> =
        NSWorkspace::sharedWorkspace().notificationCenter();
    let mut tokens = vec![];
    for name in [
        "NSWorkspaceDidLaunchApplicationNotification",
        "NSWorkspaceDidTerminateApplicationNotification",
    ] {
        let weak = weak.clone();
        let block = block2::RcBlock::new(
            move |_: std::ptr::NonNull<objc2_foundation::NSNotification>| {
                if let Some(engine) = weak.upgrade() {
                    let _ = engine.update_running();
                }
            },
        );
        let token = unsafe {
            center.addObserverForName_object_queue_usingBlock(
                Some(&NSString::from_str(name)),
                None,
                Some(&NSOperationQueue::mainQueue()),
                &block,
            )
        };
        tokens.push(Retained::into_raw(token) as usize);
    }
    app.manage(Guard {
        app: app.clone(),
        tokens,
    });
}
#[cfg(not(target_os = "macos"))]
pub fn install(_: &tauri::AppHandle) {}
