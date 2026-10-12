use crate::model::*;
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use tauri::Manager;

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "env:changed")]
pub struct EnvChanged(pub EnvInfo);

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "task:updated")]
pub struct TaskUpdated(pub Task);
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "task:log")]
pub struct TaskLog(pub TaskLogEvent);
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "library:changed")]
pub struct LibraryEvent(pub LibraryChanged);
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "updates:changed")]
pub struct UpdatesEvent(pub UpdatesChanged);
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "catalog:synced")]
pub struct CatalogSynced(pub CatalogStatus);
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "deeplink:received")]
pub struct DeeplinkReceived(pub DeepLinkEvent);
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "app:quit-requested")]
pub struct QuitEvent(pub QuitRequested);

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "locale:changed")]
pub struct LocaleEvent(pub LocaleChanged);
/// Permission changes detected by guidance polling (06 §12.7), broadcast to all windows.
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "permissions:changed")]
pub struct PermissionsChanged(pub Permissions);

#[derive(Debug, Clone)]
pub enum CoreEvent {
    CleanupScan(CleanupProgress),
    CleanupChanged(CleanupSummary),
    LocalApps(LocalAppsChanged),
    UninstallScan(ScanProgress),
    Task(Box<Task>),
    Log(TaskLogEvent),
    Library(LibraryChanged),
    Updates(UpdatesChanged),
    Env(EnvInfo),
    Permissions(Permissions),
    Catalog(CatalogStatus),
    Deeplink(DeepLinkEvent),
    Quit(QuitRequested),
    Locale(LocaleChanged),
}
pub type EventSink = Arc<dyn Fn(CoreEvent) + Send + Sync>;
pub fn sink(app: tauri::AppHandle) -> EventSink {
    use tauri_specta::Event;
    Arc::new(move |event| {
        let result = match event {
            CoreEvent::CleanupScan(data) => CleanupScanEvent(data).emit(&app),
            CoreEvent::CleanupChanged(data) => CleanupChangedEvent(data).emit(&app),
            CoreEvent::LocalApps(data) => LocalAppsEvent(data).emit(&app),
            CoreEvent::UninstallScan(data) => UninstallScanEvent(data).emit(&app),
            CoreEvent::Task(task) => TaskUpdated(*task).emit(&app),
            CoreEvent::Log(log) => TaskLog(log).emit(&app),
            CoreEvent::Library(data) => {
                // Homebrew records can arrive after the initial native scan (including Casks without .app artifacts).
                if let Some(core) = app.try_state::<Arc<crate::core::DesktopCore>>() {
                    core.queue.uninstall_engine.schedule_library_refresh();
                }
                LibraryEvent(data).emit(&app)
            }
            CoreEvent::Updates(data) => {
                crate::tray::refresh(&app);
                UpdatesEvent(data).emit(&app)
            }
            CoreEvent::Env(data) => EnvChanged(data).emit(&app),
            CoreEvent::Permissions(data) => PermissionsChanged(data).emit(&app),
            CoreEvent::Catalog(data) => {
                // A changed catalog can identify more local apps (adoptable, zap rules).
                if let Some(core) = app.try_state::<Arc<crate::core::DesktopCore>>() {
                    let engine = core.queue.uninstall_engine.clone();
                    tauri::async_runtime::spawn(async move {
                        let _ = engine.hydrate_catalog().await;
                    });
                }
                CatalogSynced(data).emit(&app)
            }
            CoreEvent::Deeplink(data) => DeeplinkReceived(data).emit_to(&app, "main"),
            CoreEvent::Locale(data) => {
                let handle = app.clone();
                app.run_on_main_thread(move || {
                    if let Some(core) = handle.try_state::<Arc<crate::core::DesktopCore>>()
                        && let Ok(settings) = core.current_settings()
                    {
                        crate::settings::apply_apple_languages(&settings);
                    }
                    if let Err(error) = crate::tray::refresh_now(&handle)
                        .and_then(|()| crate::native_menu::install(&handle, data.locale))
                    {
                        log::warn!("Menu locale refresh failed: {}", error.code);
                    }
                    if LocaleEvent(data).emit(&handle).is_err() {
                        log::warn!("Failed to emit locale event");
                    }
                })
            }
            CoreEvent::Quit(data) => QuitEvent(data).emit_to(&app, "main"),
        };
        if result.is_err() {
            log::warn!("Failed to emit client event");
        }
    })
}

/// The main window centrally confirms tray updates; never close other apps from the small popover.
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "updates:requested")]
pub struct UpdatesRequested(pub Vec<TaskTarget>);

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "local-apps:changed")]
pub struct LocalAppsEvent(pub LocalAppsChanged);
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "uninstall:scan-progress")]
pub struct UninstallScanEvent(pub ScanProgress);

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "cleanup:scan-progress")]
pub struct CleanupScanEvent(pub CleanupProgress);
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "cleanup:changed")]
pub struct CleanupChangedEvent(pub CleanupSummary);

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "monitor:sample")]
pub struct MonitorSampleEvent(pub MonitorSample);

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "monitor:popover")]
pub struct MonitorPopover(pub MonitorModule);

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, tauri_specta::Event)]
#[serde(transparent)]
#[tauri_specta(event_name = "fan:changed")]
pub struct FanChanged(pub FanChange);
