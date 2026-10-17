// SPDX-License-Identifier: GPL-3.0-only
use super::*;
use tauri::Manager;
use tauri_plugin_notification::NotificationExt;
use tauri_specta::Event;
fn localized<'a>(app: &tauri::AppHandle, texts: [&'a str; 6]) -> &'a str {
    let locale = app
        .try_state::<Arc<crate::core::DesktopCore>>()
        .and_then(|c| c.current_settings().ok())
        .map(|s| s.locale)
        .unwrap_or(crate::model::Locale::EnUs);
    texts[match locale {
        crate::model::Locale::EnUs => 0,
        crate::model::Locale::ZhCn => 1,
        crate::model::Locale::JaJp => 2,
        crate::model::Locale::EsEs => 3,
        crate::model::Locale::PtBr => 4,
        crate::model::Locale::RuRu => 5,
    }]
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
            let title = localized(
                &handle,
                [
                    "Suspicious paste paused",
                    "已暂停可疑命令粘贴",
                    "不審な貼り付けを停止しました",
                    "Pegado sospechoso detenido",
                    "Colagem suspeita pausada",
                    "Подозрительная вставка приостановлена",
                ],
            );
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
        localized(
            app,
            [
                "Virus scan finished",
                "病毒扫描已完成",
                "ウイルススキャンが完了しました",
                "Análisis antivirus finalizado",
                "Verificação de vírus concluída",
                "Проверка на вирусы завершена",
            ],
        )
    } else {
        localized(
            app,
            [
                "Threats found",
                "发现威胁",
                "脅威が見つかりました",
                "Se encontraron amenazas",
                "Ameaças encontradas",
                "Обнаружены угрозы",
            ],
        )
    };
    let _ = app.notification().builder().title(title).show();
}
pub fn signatures(app: &tauri::AppHandle, data: &AppSignaturesChanged) {
    let title = localized(
        app,
        [
            "App signature changed",
            "App 签名发生变化",
            "App の署名が変更されました",
            "La firma de la app ha cambiado",
            "A assinatura do app mudou",
            "Подпись приложения изменилась",
        ],
    );
    let body = data
        .apps
        .iter()
        .take(3)
        .map(|a| a.name.as_str())
        .collect::<Vec<_>>()
        .join(", ");
    let _ = app.notification().builder().title(title).body(body).show();
}
