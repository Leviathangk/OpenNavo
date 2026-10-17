use crate::{AppError, core::DesktopCore, security::*};
use std::sync::Arc;
use tauri::State;

#[tauri::command]
#[specta::specta]
pub async fn security_overview(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<SecurityOverview, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.overview()).await
}

#[tauri::command]
#[specta::specta]
pub async fn posture_check(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<Vec<PostureCheck>, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || Ok(service.posture())).await
}

#[tauri::command]
#[specta::specta]
pub async fn av_engine_status(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<AvEngineStatus, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.av.status()).await
}

#[tauri::command]
#[specta::specta]
pub async fn av_db_update(state: State<'_, Arc<DesktopCore>>) -> Result<String, AppError> {
    Ok(state
        .queue
        .enqueue(
            crate::model::TaskOp::AvDbUpdate,
            None,
            Default::default(),
            crate::model::TaskTrigger::Manual,
        )?
        .id)
}

#[tauri::command]
#[specta::specta]
pub async fn av_scan_start(
    state: State<'_, Arc<DesktopCore>>,
    kind: AvScanKind,
    paths: Option<Vec<String>>,
) -> Result<AvScanStarted, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.scan_start(kind, paths)).await
}

#[tauri::command]
#[specta::specta]
pub async fn av_scan_cancel(
    state: State<'_, Arc<DesktopCore>>,
    scan_id: String,
) -> Result<(), AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.scan_cancel(&scan_id)).await
}

#[tauri::command]
#[specta::specta]
pub async fn av_scan_get(
    state: State<'_, Arc<DesktopCore>>,
    scan_id: String,
) -> Result<AvScanResult, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.scan_get(&scan_id)).await
}

#[tauri::command]
#[specta::specta]
pub async fn av_findings_act(
    state: State<'_, Arc<DesktopCore>>,
    scan_id: String,
    finding_ids: Vec<String>,
    action: FindingAction,
) -> Result<ActOutcome, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.findings_act(&scan_id, finding_ids, action)).await
}

#[tauri::command]
#[specta::specta]
pub async fn quarantine_list(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<Vec<QuarantineEntry>, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.quarantine_list()).await
}

#[tauri::command]
#[specta::specta]
pub async fn quarantine_restore(
    state: State<'_, Arc<DesktopCore>>,
    ids: Vec<String>,
) -> Result<ActOutcome, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.quarantine_restore(ids)).await
}

#[tauri::command]
#[specta::specta]
pub async fn quarantine_delete(
    state: State<'_, Arc<DesktopCore>>,
    ids: Vec<String>,
) -> Result<ActOutcome, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.quarantine_delete(ids)).await
}

#[tauri::command]
#[specta::specta]
pub async fn startup_scan(state: State<'_, Arc<DesktopCore>>) -> Result<StartupSnapshot, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.startup_scan()).await
}

/// The last startup scan as saved: shown at once while `startup_scan` runs.
#[tauri::command]
#[specta::specta]
pub async fn startup_cached(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<Option<StartupSnapshot>, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.startup_cached()).await
}

/// Opens an app at login (the user's login items list).
#[tauri::command]
#[specta::specta]
pub async fn startup_login_add(
    state: State<'_, Arc<DesktopCore>>,
    path: String,
) -> Result<StartupItem, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.startup_login_add(std::path::PathBuf::from(path))).await
}

#[tauri::command]
#[specta::specta]
pub async fn startup_item_act(
    state: State<'_, Arc<DesktopCore>>,
    ids: Vec<String>,
    action: StartupItemAction,
) -> Result<ActOutcome, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.startup_act(ids, action)).await
}

#[tauri::command]
#[specta::specta]
pub async fn protection_events(
    state: State<'_, Arc<DesktopCore>>,
    limit: u32,
) -> Result<Vec<ProtectionEvent>, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.events(limit)).await
}

#[tauri::command]
#[specta::specta]
pub async fn download_verify(
    state: State<'_, Arc<DesktopCore>>,
    path: String,
    token: Option<String>,
) -> Result<DownloadVerdict, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.download_verify(path.into(), token)).await
}

#[tauri::command]
#[specta::specta]
pub async fn download_act(
    state: State<'_, Arc<DesktopCore>>,
    path: String,
    action: DownloadAction,
) -> Result<(), AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.download_act(path.into(), action)).await
}

#[tauri::command]
#[specta::specta]
pub async fn av_schedule_status(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<AvScheduleStatus, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.schedule_status()).await
}

#[tauri::command]
#[specta::specta]
pub async fn install_scan_get(
    state: State<'_, Arc<DesktopCore>>,
    task_id: String,
) -> Result<Option<InstallScanResult>, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.install_scan_get(&task_id)).await
}

#[tauri::command]
#[specta::specta]
pub async fn app_signature_check(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<AppSignatureReport, AppError> {
    let service = state.queue.security.clone();
    let apps = state.queue.uninstall_engine.clone();
    crate::blocking(move || service.app_signature_check(&apps.list()?)).await
}

#[tauri::command]
#[specta::specta]
pub async fn app_signature_get(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<Option<AppSignatureReport>, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.app_signature_get()).await
}

#[tauri::command]
#[specta::specta]
pub async fn paste_protection_status(
    state: State<'_, Arc<DesktopCore>>,
    app: tauri::AppHandle,
) -> Result<PasteProtectionStatus, AppError> {
    let service = state.queue.security.clone();
    #[cfg(target_os = "macos")]
    {
        crate::security::paste::native::on_main(app, move || {
            crate::security::paste::native::status(&service, false)
        })
        .await
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = (service, app);
        Err(AppError::new("E_ACCESSIBILITY_DENIED", "paste_unavailable"))
    }
}

#[tauri::command]
#[specta::specta]
pub async fn paste_protection_request_access(
    state: State<'_, Arc<DesktopCore>>,
    app: tauri::AppHandle,
) -> Result<PasteProtectionStatus, AppError> {
    let service = state.queue.security.clone();
    #[cfg(target_os = "macos")]
    {
        crate::security::paste::native::on_main(app, move || {
            crate::security::paste::native::status(&service, true)
        })
        .await
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = (service, app);
        Err(AppError::new("E_ACCESSIBILITY_DENIED", "paste_unavailable"))
    }
}

#[tauri::command]
#[specta::specta]
pub async fn paste_protection_act(
    state: State<'_, Arc<DesktopCore>>,
    app: tauri::AppHandle,
    id: String,
    action: PasteAction,
) -> Result<(), AppError> {
    let service = state.queue.security.clone();
    #[cfg(target_os = "macos")]
    {
        crate::security::paste::native::on_main(app, move || {
            service.paste_act(
                &id,
                action,
                chrono::Utc::now().timestamp_millis(),
                &crate::security::paste::native::Native,
            )
        })
        .await
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = (service, app, id, action);
        Err(AppError::new("E_ACCESSIBILITY_DENIED", "paste_unavailable"))
    }
}

#[tauri::command]
#[specta::specta]
pub async fn virus_total_status(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<VirusTotalStatus, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.vt_status()).await
}

#[tauri::command]
#[specta::specta]
pub async fn virus_total_key_set(
    state: State<'_, Arc<DesktopCore>>,
    key: Option<String>,
) -> Result<VirusTotalStatus, AppError> {
    let service = state.queue.security.clone();
    crate::blocking(move || service.vt_set_key(key)).await
}

#[tauri::command]
#[specta::specta]
pub async fn virus_total_lookup(
    state: State<'_, Arc<DesktopCore>>,
    sha256: String,
) -> Result<VirusTotalVerdict, AppError> {
    state.queue.security.vt_lookup(sha256).await
}
