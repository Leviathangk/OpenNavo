// SPDX-License-Identifier: GPL-3.0-only
use super::*;
use tauri::Manager;
use tauri_plugin_notification::NotificationExt;
use tauri_specta::Event;
// Notification titles live in the native string tables with the other system notifications.
fn localized(app: &tauri::AppHandle, key: &str) -> String {
    let locale = app
        .try_state::<Arc<crate::core::DesktopCore>>()
        .and_then(|c| c.current_settings().ok())
        .map(|s| s.locale)
        .unwrap_or(crate::model::Locale::EnUs);
    crate::native_i18n::text(locale, key)
        .or_else(|_| crate::native_i18n::text(crate::model::Locale::EnUs, key))
        .unwrap_or_default()
}
pub fn paste_blocked(app: &tauri::AppHandle, data: PasteBlocked) -> tauri::Result<()> {
    let app = app.clone();
    // Leave the event-tap callback before showing UI; it has a strict OS deadline.
    tauri::async_runtime::spawn(async move {
        if let Some(core) = app.try_state::<Arc<crate::core::DesktopCore>>() {
            let service = core.queue.security.clone();
            let bundle = data.bundle_id.clone();
            let _ = crate::blocking(move || {
                service.record_event(ProtectionEventKind::PasteBlocked, &bundle, None, true)
            })
            .await;
        }
        let handle = app.clone();
        let _ = app.run_on_main_thread(move || {
            if let Some(window) = handle.get_webview_window("main") {
                let _ = window.show();
                let _ = window.unminimize();
                let _ = window.set_focus();
            }
            let title = localized(&handle, "security.pasteBlocked");
            let _ = handle
                .notification()
                .builder()
                .title(title)
                .body(&data.app_name)
                .show();
            let _ = crate::events::PasteBlockedEvent(data).emit_to(&handle, "main");
        });
    });
    Ok(())
}
pub fn scan_finished(app: &tauri::AppHandle, data: &AvScanResult) {
    if data.state != AvScanState::Done {
        return;
    }
    let title = if data.findings.is_empty() {
        localized(app, "security.scanFinished")
    } else {
        localized(app, "security.threatsFound")
    };
    let _ = app.notification().builder().title(title).show();
}
pub fn signatures(app: &tauri::AppHandle, data: &AppSignaturesChanged) {
    let title = localized(app, "security.signatureChanged");
    let body = data
        .apps
        .iter()
        .take(3)
        .map(|a| a.name.as_str())
        .collect::<Vec<_>>()
        .join(", ");
    let _ = app.notification().builder().title(title).body(body).show();
}
