// SPDX-License-Identifier: GPL-3.0-only
use super::ALERT_LABEL;
use std::sync::atomic::{AtomicBool, Ordering};
use tauri::{Manager, WebviewUrl, WebviewWindowBuilder};

#[derive(Default)]
pub struct WindowState {
    wanted: AtomicBool,
    ready: AtomicBool,
}
pub fn sync(app: &tauri::AppHandle, visible: bool) {
    app.state::<WindowState>()
        .wanted
        .store(visible, Ordering::Release);
    let handle = app.clone();
    let _ = app.run_on_main_thread(move || {
        if let Err(error) = update(&handle) {
            log::warn!("Firewall alert window unavailable: {error}");
        }
    });
}
fn update(app: &tauri::AppHandle) -> tauri::Result<()> {
    let state = app.state::<WindowState>();
    if !state.wanted.load(Ordering::Acquire) {
        if let Some(window) = app.get_webview_window(ALERT_LABEL) {
            window.hide()?;
        }
        return Ok(());
    }
    if app.get_webview_window(ALERT_LABEL).is_none() {
        state.ready.store(false, Ordering::Release);
        let handle = app.clone();
        let window = WebviewWindowBuilder::new(
            app,
            ALERT_LABEL,
            WebviewUrl::App("index.html#/firewall-alert".into()),
        )
        .title("OpenNavo")
        .inner_size(440.0, 300.0)
        .decorations(false)
        .resizable(false)
        .closable(false)
        .always_on_top(true)
        .visible_on_all_workspaces(true)
        .skip_taskbar(true)
        .focused(false)
        .accept_first_mouse(true)
        .visible(false)
        .on_page_load(move |_, payload| {
            if matches!(payload.event(), tauri::webview::PageLoadEvent::Finished) {
                handle
                    .state::<WindowState>()
                    .ready
                    .store(true, Ordering::Release);
                let ui = handle.clone();
                let _ = handle.run_on_main_thread(move || {
                    let _ = update(&ui);
                });
            }
        })
        .build()?;
        let handle = app.clone();
        window.on_window_event(move |event| match event {
            tauri::WindowEvent::CloseRequested { api, .. } => api.prevent_close(),
            tauri::WindowEvent::Resized(_) | tauri::WindowEvent::ScaleFactorChanged { .. } => {
                let ui = handle.clone();
                let _ = handle.run_on_main_thread(move || {
                    let _ = position(&ui, false);
                });
            }
            tauri::WindowEvent::Destroyed => handle
                .state::<WindowState>()
                .ready
                .store(false, Ordering::Release),
            _ => {}
        });
    }
    if state.ready.load(Ordering::Acquire) {
        position(app, true)?;
    }
    Ok(())
}
fn position(app: &tauri::AppHandle, show: bool) -> tauri::Result<()> {
    if let Some(window) = app.get_webview_window(ALERT_LABEL) {
        #[cfg(target_os = "macos")]
        {
            unsafe extern "C" {
                fn on_fw_alert_present(window: *mut std::ffi::c_void, show: i32);
            }
            unsafe { on_fw_alert_present(window.ns_window()?, i32::from(show)) };
        }
        #[cfg(not(target_os = "macos"))]
        if show {
            window.show()?;
        }
    }
    Ok(())
}
