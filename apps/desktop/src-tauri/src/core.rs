use crate::{
    AppError,
    brew::{args::ReadOp, json::Info},
    db::Database,
    events::{CoreEvent, EventSink},
    library,
    model::*,
    queue::TaskQueue,
};
use std::{
    path::PathBuf,
    sync::{
        Arc, RwLock,
        atomic::{AtomicBool, AtomicI64, AtomicU64, Ordering},
    },
};
pub struct DesktopCore {
    pub appstore: Arc<crate::appstore::Service>,
    pub database: Database,
    pub catalog: Arc<crate::catalog::sync::CatalogService>,
    pub settings: Arc<RwLock<Settings>>,
    settings_save: RwLock<crate::settings::Save>,
    settings_lock: tokio::sync::Mutex<()>,
    pub settings_changed: tokio::sync::Notify,
    pub queue: Arc<TaskQueue>,
    pub data_dir: PathBuf,
    pub cache_dir: PathBuf,
    pub relaunch_fresh: bool,
    pub startup_full_disk_pending: bool,
    relaunch_ineffective: AtomicBool,
    pub sink: EventSink,
    pub library: RwLock<Vec<InstalledItem>>,
    pub updates: RwLock<Vec<OutdatedItem>>,
    pub storage: RwLock<StorageSummary>,
    pub brew: RwLock<Option<BrewInfo>>,
    installed_info: RwLock<Info>,
    app_management: RwLock<PermissionState>,
    /// System preflight allows App Management but tasks are blocked: permission requires relaunch (06 §12.3).
    app_management_blocked: AtomicBool,
    /// Privacy signal source; tests substitute fixed values independent of local authorization.
    permission_signals: RwLock<PermissionSignals>,
    permission_lock: tokio::sync::Mutex<()>,
    refresh_lock: tokio::sync::Mutex<()>,
    library_generation: AtomicU64,
    outdated_source: RwLock<crate::updates::Outdated>,
    updates_lock: tokio::sync::Mutex<()>,
    source_checks_lock: tokio::sync::Mutex<()>,
    source_checks_active: AtomicBool,
    updates_checking: AtomicBool,
    checked_at: RwLock<Option<i64>>,
    schedule_plan_at: AtomicI64,
    pub schedule_lock: tokio::sync::Mutex<()>,
    notice_sink: RwLock<Option<crate::scheduler::NoticeSink>>,
}
fn poisoned<T>(_: T) -> AppError {
    AppError::new("E_UNKNOWN", "core_poisoned")
}
pub type PermissionSignals = fn(PermissionState, bool) -> crate::permissions::Signals;
impl DesktopCore {
    pub fn open(
        data_dir: PathBuf,
        cache_dir: PathBuf,
        askpass: PathBuf,
        sink: EventSink,
    ) -> Result<Arc<Self>, AppError> {
        let relaunch_fresh = crate::relaunch::consume(&data_dir);
        let startup_full_disk_pending = crate::permissions::combine(crate::permissions::signals(
            PermissionState::Unknown,
            false,
        ))
        .full_disk_access_pending;
        let database = Database::open(&data_dir.join("opennavo.db"))?;
        let settings_path = data_dir.join("settings.json");
        let settings = Arc::new(RwLock::new(crate::settings::load(&settings_path)?));
        let settings_save: crate::settings::Save =
            Arc::new(move |settings| crate::settings::save_file(&settings_path, settings));
        let queue = TaskQueue::new(
            database.clone(),
            settings.clone(),
            data_dir.join("logs/tasks"),
            askpass,
            sink.clone(),
        )?;
        let cached = database
            .meta("installed_cache")?
            .and_then(|s| serde_json::from_str::<Vec<InstalledItem>>(&s).ok())
            .unwrap_or_default();
        let storage = database
            .meta("storage_cache")?
            .and_then(|s| serde_json::from_str(&s).ok())
            .unwrap_or_else(|| library::storage(&cached, 0, false));
        let catalog = Arc::new(crate::catalog::sync::CatalogService::new(
            database.clone(),
            crate::catalog::sync::configured_base(),
            "https://cdn.opennavo.com/snapshots/latest.json".into(),
            cache_dir.join("snapshots"),
            settings.clone(),
            sink.clone(),
        ));
        let appstore = queue.appstore.clone();
        let core = Arc::new(Self {
            appstore,
            database: database.clone(),
            catalog,
            settings,
            settings_save: RwLock::new(settings_save),
            settings_lock: tokio::sync::Mutex::new(()),
            settings_changed: tokio::sync::Notify::new(),
            queue: queue.clone(),
            data_dir,
            cache_dir,
            relaunch_fresh,
            startup_full_disk_pending,
            relaunch_ineffective: AtomicBool::new(relaunch_fresh && startup_full_disk_pending),
            sink,
            library: RwLock::new(cached),
            updates: RwLock::new(
                database
                    .meta("updates_cache")?
                    .and_then(|s| serde_json::from_str(&s).ok())
                    .unwrap_or_default(),
            ),
            outdated_source: RwLock::new(
                database
                    .meta("outdated_source")?
                    .and_then(|s| serde_json::from_str(&s).ok())
                    .unwrap_or_default(),
            ),
            updates_lock: tokio::sync::Mutex::new(()),
            source_checks_lock: tokio::sync::Mutex::new(()),
            source_checks_active: AtomicBool::new(false),
            updates_checking: AtomicBool::new(false),
            checked_at: RwLock::new(
                database
                    .meta("updates_checked_at")?
                    .and_then(|s| s.parse().ok()),
            ),
            schedule_plan_at: AtomicI64::new(
                database
                    .meta("schedule_plan_at")?
                    .and_then(|s| s.parse().ok())
                    .unwrap_or(0),
            ),
            schedule_lock: tokio::sync::Mutex::new(()),
            notice_sink: RwLock::new(None),
            storage: RwLock::new(storage),
            brew: RwLock::new(None),
            installed_info: RwLock::new(Info::default()),
            app_management: RwLock::new(match database.meta("app_management")?.as_deref() {
                Some("granted") => PermissionState::Granted,
                Some("denied") => PermissionState::Denied,
                _ => PermissionState::Unknown,
            }),
            app_management_blocked: AtomicBool::new(false),
            permission_signals: RwLock::new(crate::permissions::signals),
            permission_lock: tokio::sync::Mutex::new(()),
            refresh_lock: tokio::sync::Mutex::new(()),
            library_generation: AtomicU64::new(0),
        });
        let weak = Arc::downgrade(&core);
        queue.set_completion(Arc::new(move |task| {
            let weak = weak.clone();
            Box::pin(async move {
                if let Some(core) = weak.upgrade() {
                    core.after_task(&task).await;
                }
            })
        }))?;
        Ok(core)
    }
    pub fn start_catalog_sync(self: &Arc<Self>) {
        let weak = Arc::downgrade(self);
        tauri::async_runtime::spawn(async move {
            tokio::time::sleep(std::time::Duration::from_secs(5)).await;
            loop {
                let Some(core) = weak.upgrade() else {
                    break;
                };
                let result = core.sync_catalog(false).await;
                if let Err(error) = &result {
                    log::info!("Catalog sync temporarily unavailable: {}", error.code);
                }
                drop(core);
                tokio::time::sleep(std::time::Duration::from_secs(if result.is_err() {
                    60
                } else {
                    3600
                }))
                .await;
            }
        });
    }
    pub fn start_library_scan(self: &Arc<Self>) {
        let core = self.clone();
        tauri::async_runtime::spawn(async move {
            if let Err(e) = core
                .refresh_library(LibraryChangeReason::Startup, None)
                .await
            {
                log::info!("Installed-package scan temporarily unavailable: {}", e.code);
            }
            let updater = core.queue.updater.clone();
            let recover = crate::blocking(move || updater.needs_startup_check()).await;
            let result = match recover {
                // Recovery does not perform the scheduler's notifications or upgrades,
                // so it must not consume today's scheduled run.
                Ok(true) => core.recover_update_sources(None).await,
                Ok(false) => core.check_updates(false).await,
                Err(error) => Err(error),
            };
            if let Err(e) = result {
                log::info!("Startup update check temporarily unavailable: {}", e.code);
            }
        });
    }
    pub fn current_settings(&self) -> Result<Settings, AppError> {
        self.queue.settings()
    }

    pub fn install_settings_save(&self, save: crate::settings::Save) -> Result<(), AppError> {
        *self.settings_save.write().map_err(poisoned)? = save;
        Ok(())
    }
    /// Merge while holding the existing settings transaction lock; unrelated concurrent settings survive.
    pub async fn save_fan_manual(
        self: &Arc<Self>,
        speeds: std::collections::BTreeMap<u32, u32>,
    ) -> Result<(), AppError> {
        let _update = self.settings_lock.lock().await;
        let mut settings = self.current_settings()?;
        settings
            .fan_last_manual
            .get_or_insert_default()
            .extend(speeds);
        let core = self.clone();
        crate::blocking(move || {
            let writer = core.settings_save.read().map_err(poisoned)?.clone();
            writer(&settings)?;
            *core.settings.write().map_err(poisoned)? = settings;
            Ok(())
        })
        .await?;
        self.settings_changed.notify_one();
        Ok(())
    }
    pub async fn replace_settings(
        self: &Arc<Self>,
        settings: Settings,
    ) -> Result<Settings, AppError> {
        self.replace_settings_with(settings, None).await
    }
    pub async fn replace_settings_with(
        self: &Arc<Self>,
        settings: Settings,
        autostart: Option<crate::settings::Autostart>,
    ) -> Result<Settings, AppError> {
        let _update = self.settings_lock.lock().await;
        let settings = crate::settings::resolve(
            crate::settings::validate(settings)?,
            crate::settings::system_locale(),
        );
        let old = self.current_settings()?;
        let login_changed = old.launch_at_login != settings.launch_at_login;
        if login_changed && let Some(change) = autostart.clone() {
            let enabled = settings.launch_at_login;
            crate::blocking(move || change(enabled)).await?;
        }
        let saved = settings.clone();
        let core = self.clone();
        let persisted = crate::blocking(move || {
            let _schedule = core.queue.security.scheduled.lock().map_err(poisoned)?;
            let writer = core.settings_save.read().map_err(poisoned)?.clone();
            writer(&saved)?;
            core.queue
                .security
                .record_schedule_settings(&saved, chrono::Utc::now().timestamp_millis())?;
            *core.settings.write().map_err(poisoned)? = saved;
            Ok(())
        })
        .await;
        if let Err(error) = persisted {
            if login_changed && let Some(change) = autostart {
                let previous = old.launch_at_login;
                let _ = crate::blocking(move || change(previous)).await;
            }
            return Err(error);
        }
        if old.app_store_region != settings.app_store_region
            || old.app_store_updates != settings.app_store_updates
        {
            self.appstore.settings_changed()?;
        }
        if old.check_time != settings.check_time || old.auto_check != settings.auto_check {
            let now = chrono::Utc::now().timestamp_millis();
            self.schedule_plan_at.store(now, Ordering::Release);
            let database = self.database.clone();
            if crate::blocking(move || database.set_meta("schedule_plan_at", &now.to_string()))
                .await
                .is_err()
            {
                log::warn!("Failed to save scheduled check time");
            }
        }
        self.settings_changed.notify_one();
        // Menu/window locale events must not wait for brew probes or language-pack network requests.
        if old.locale != settings.locale {
            (self.sink)(CoreEvent::Locale(LocaleChanged {
                locale: settings.locale,
            }));
        }
        if old.brew_path != settings.brew_path
            || old.mirror != settings.mirror
            || old.homebrew_analytics != settings.homebrew_analytics
            || old.locale != settings.locale
        {
            *self.brew.write().map_err(poisoned)? = None;
            let info = self.detect_environment().await?;
            (self.sink)(CoreEvent::Env(info));
        }
        if old.locale != settings.locale {
            let core = self.clone();
            tauri::async_runtime::spawn(async move {
                match core.catalog.sync_text().await {
                    Ok(_) => {
                        if let Err(error) = core.reclassify_library().await {
                            log::info!("Local name refresh failed: {}", error.code);
                        }
                        if let Err(error) = core.rebuild_updates().await {
                            log::info!("Update name refresh failed: {}", error.code);
                        }
                    }
                    Err(error) => {
                        log::info!("Language pack temporarily unavailable: {}", error.code)
                    }
                }
            });
            self.reclassify_library().await?;
            self.rebuild_updates().await?;
        }
        if old.include_greedy != settings.include_greedy
            && let Err(error) = self.check_updates(false).await
        {
            log::info!(
                "Automatic-update settings saved; check temporarily unavailable: {}",
                error.code
            );
        }
        self.notice(crate::scheduler::Notice::Settings);
        Ok(settings)
    }
    pub fn install_notice_sink(&self, sink: crate::scheduler::NoticeSink) -> Result<(), AppError> {
        *self.notice_sink.write().map_err(poisoned)? = Some(sink);
        Ok(())
    }
    pub fn notice(&self, notice: crate::scheduler::Notice) {
        if let Some(sink) = self.notice_sink.read().ok().and_then(|s| s.clone()) {
            sink(notice);
        }
    }
    pub async fn brew_info(&self) -> Result<BrewInfo, AppError> {
        if let Some(info) = self.brew.read().map_err(poisoned)?.clone() {
            return Ok(info);
        }
        let info = crate::brew::locate::locate(&self.current_settings()?).await?;
        *self.brew.write().map_err(poisoned)? = Some(info.clone());
        Ok(info)
    }
    pub fn library_list(&self) -> Result<Vec<InstalledItem>, AppError> {
        Ok(self.library.read().map_err(poisoned)?.clone())
    }
    pub fn storage_summary(&self) -> Result<StorageSummary, AppError> {
        Ok(self.storage.read().map_err(poisoned)?.clone())
    }
    fn publish_library(
        &self,
        items: &[InstalledItem],
        cache: u64,
        complete: bool,
        reason: LibraryChangeReason,
        generation: u64,
    ) -> Result<(), AppError> {
        let mut library = self.library.write().map_err(poisoned)?;
        if self.library_generation.load(Ordering::Acquire) != generation {
            return Ok(());
        }
        *library = items.to_vec();
        *self.storage.write().map_err(poisoned)? = library::storage(items, cache, complete);
        drop(library);
        (self.sink)(CoreEvent::Library(LibraryChanged { reason }));
        Ok(())
    }

    fn publish_enrichment(
        &self,
        items: &[InstalledItem],
        cache: u64,
        complete: bool,
        reason: LibraryChangeReason,
        generation: u64,
    ) -> Result<(), AppError> {
        let mut library = self.library.write().map_err(poisoned)?;
        if self.library_generation.load(Ordering::Acquire) != generation {
            return Ok(());
        }
        for current in library.iter_mut() {
            if let Some(item) = items
                .iter()
                .find(|i| i.kind == current.kind && i.install_token() == current.install_token())
            {
                current.size_bytes = item.size_bytes;
                current.icon_path = item.icon_path.clone();
            }
        }
        *self.storage.write().map_err(poisoned)? = library::storage(&library, cache, complete);
        drop(library);
        (self.sink)(CoreEvent::Library(LibraryChanged { reason }));
        Ok(())
    }
    pub async fn sync_catalog(self: &Arc<Self>, force: bool) -> Result<SyncResult, AppError> {
        let result = self.catalog.sync(force).await?;
        self.reclassify_library().await?;
        self.rebuild_updates().await?;
        (self.sink)(CoreEvent::Library(LibraryChanged {
            reason: LibraryChangeReason::Refresh,
        }));
        Ok(result)
    }
    async fn reclassify_library(self: &Arc<Self>) -> Result<(), AppError> {
        let core = self.clone();
        crate::blocking(move || {
            let info = core.installed_info.read().map_err(poisoned)?.clone();
            let brew = core.brew.read().map_err(poisoned)?.clone();
            if let Some(brew) = brew {
                let scan = library::scan(
                    &core.database,
                    &info,
                    &brew.prefix,
                    core.current_settings()?.locale,
                )?;
                let mut cached = core.library.write().map_err(poisoned)?;
                for current in cached.iter_mut() {
                    if let Some(item) = scan.items.iter().find(|i| {
                        i.kind == current.kind && i.install_token() == current.install_token()
                    }) {
                        current.token = item.token.clone();
                        current.name = item.name.clone();
                        current.latest_version = item.latest_version.clone();
                        current.status = item.status;
                    }
                }
                core.database
                    .set_meta("installed_cache", &serde_json::to_string(&*cached)?)?;
            }
            Ok(())
        })
        .await?;
        (self.sink)(CoreEvent::Library(LibraryChanged {
            reason: LibraryChangeReason::Refresh,
        }));
        Ok(())
    }
    pub fn updates_list(&self) -> Result<Vec<OutdatedItem>, AppError> {
        let updates = self.updates.read().map_err(poisoned)?.clone();
        self.queue.updater.merge(updates)
    }
    pub async fn rebuild_updates(self: &Arc<Self>) -> Result<Vec<OutdatedItem>, AppError> {
        let core = self.clone();
        crate::blocking(move || {
            let source = core.outdated_source.read().map_err(poisoned)?.clone();
            let mut library = core.library.write().map_err(poisoned)?;
            let before = library.clone();
            let result = crate::updates::build(
                &core.database,
                &source,
                &mut library,
                core.current_settings()?.locale,
                chrono::Utc::now().timestamp_millis(),
            )?;
            core.database
                .set_meta("installed_cache", &serde_json::to_string(&*library)?)?;
            let changed = before != *library;
            drop(library);
            if changed {
                (core.sink)(CoreEvent::Library(LibraryChanged {
                    reason: LibraryChangeReason::Refresh,
                }));
            }
            core.database
                .set_meta("updates_cache", &serde_json::to_string(&result)?)?;
            *core.updates.write().map_err(poisoned)? = result.clone();
            core.queue.updater.changed()?;
            Ok(result)
        })
        .await
    }
    /// Shared manual/tray/scheduled check. Reserved engines are skipped only for
    /// the default selection; an explicit unsupported source fails before work.
    pub async fn check_update_sources(
        self: &Arc<Self>,
        requested: Option<Vec<UpdateSource>>,
        trigger: TaskTrigger,
        finished_at: Option<i64>,
    ) -> Result<Vec<OutdatedItem>, AppError> {
        self.check_update_sources_inner(requested, trigger, finished_at, false)
            .await
    }
    /// Revalidate startup caches without completing the daily scheduled workflow.
    pub async fn recover_update_sources(
        self: &Arc<Self>,
        finished_at: Option<i64>,
    ) -> Result<Vec<OutdatedItem>, AppError> {
        self.check_update_sources_inner(None, TaskTrigger::Schedule, finished_at, true)
            .await
    }
    async fn check_update_sources_inner(
        self: &Arc<Self>,
        requested: Option<Vec<UpdateSource>>,
        trigger: TaskTrigger,
        finished_at: Option<i64>,
        recovery: bool,
    ) -> Result<Vec<OutdatedItem>, AppError> {
        let settings = self.current_settings()?;
        let sources = crate::updater::check_sources(requested, &settings.update_sources)?;
        if sources.is_empty() {
            return self.updates_list(); // No completed check: retain the previous status.
        }
        let _check = self.source_checks_lock.lock().await;
        self.source_checks_active.store(true, Ordering::Release);
        struct Reset<'a>(&'a AtomicBool);
        impl Drop for Reset<'_> {
            fn drop(&mut self) {
                self.0.store(false, Ordering::Release);
            }
        }
        let reset = Reset(&self.source_checks_active);
        let mut outcome = crate::updater::outcome::CheckOutcome::default();
        if sources.contains(&UpdateSource::Homebrew) {
            match self
                .check_updates_for(settings.run_brew_update_on_check, trigger)
                .await
            {
                Ok(_) => outcome.succeeded(),
                Err(error) => outcome.failed(UpdateSource::Homebrew, "brew outdated", error),
            }
        }
        let native: Vec<_> = sources
            .iter()
            .copied()
            .filter(|s| *s != UpdateSource::Homebrew)
            .collect();
        if !native.is_empty() {
            let _check = self.updates_lock.lock().await;
            match self
                .queue
                .updater
                .check(&native, &self.appstore, trigger == TaskTrigger::Manual)
                .await
            {
                Ok(result) => outcome.extend(result),
                Err(error) => {
                    for source in native {
                        outcome.failed(source, "native check could not complete", error.clone());
                    }
                }
            }
        }
        let now = finished_at.unwrap_or_else(|| chrono::Utc::now().timestamp_millis());
        self.database.set_meta(
            if recovery {
                "updates_recovery_checked_at"
            } else {
                "updates_checked_at"
            },
            &now.to_string(),
        )?;
        self.database.set_meta(
            "updates_failed_sources",
            &serde_json::to_string(&outcome.failed_sources())?,
        )?;
        self.database.set_meta(
            "updates_failures",
            &serde_json::to_string(&outcome.details)?,
        )?;
        if !recovery {
            *self.checked_at.write().map_err(poisoned)? = Some(now);
        }
        drop(reset); // The final event must let the frontend observe completed status.
        self.queue.updater.changed()?;
        outcome.result()?;
        self.updates_list()
    }
    pub async fn check_updates(
        self: &Arc<Self>,
        run_brew_update: bool,
    ) -> Result<Vec<OutdatedItem>, AppError> {
        self.check_updates_for(run_brew_update, TaskTrigger::Manual)
            .await
    }
    pub async fn check_updates_for(
        self: &Arc<Self>,
        run_brew_update: bool,
        trigger: TaskTrigger,
    ) -> Result<Vec<OutdatedItem>, AppError> {
        if run_brew_update {
            let queue = self.queue.clone();
            let task = crate::blocking(move || {
                queue.enqueue(TaskOp::Update, None, TaskOptions::default(), trigger)
            })
            .await?;
            let task = self.queue.wait_terminal(&task.id).await?;
            if task.state != TaskState::Succeeded {
                return Err(task
                    .error
                    .unwrap_or_else(|| AppError::new("E_INTERRUPTED", "update_canceled")));
            }
        }
        // Queue completion callbacks also check updates; never hold their required lock while waiting for the queue.
        let _check = self.updates_lock.lock().await;
        self.updates_checking.store(true, Ordering::Release);
        struct Reset<'a>(&'a AtomicBool);
        impl Drop for Reset<'_> {
            fn drop(&mut self) {
                self.0.store(false, Ordering::Release);
            }
        }
        let _reset = Reset(&self.updates_checking);
        if self.library_list()?.is_empty() {
            self.refresh_library(LibraryChangeReason::Refresh, None)
                .await?;
        }
        let _read = self.queue.read_gate.read().await;
        let settings = self.current_settings()?;
        let brew = self.brew_info().await?;
        let raw = crate::brew::locate::read(
            &brew,
            &settings,
            ReadOp::Outdated {
                greedy: settings.include_greedy,
            },
            None,
        )
        .await?;
        if raw.len() > 32 * 1024 * 1024 {
            return Err(AppError::new("E_UNKNOWN", "outdated_json_size"));
        }
        let core = self.clone();
        crate::blocking(move || {
            let parsed: crate::updates::Outdated = serde_json::from_str(&raw)?;
            core.database
                .set_meta("outdated_source", &serde_json::to_string(&parsed)?)?;
            *core.outdated_source.write().map_err(poisoned)? = parsed;
            Ok(())
        })
        .await?;
        let result = self.rebuild_updates().await?;
        // Startup and task-completion scans refresh Homebrew results only. The
        // shared source check records completion after all selected sources run;
        // recording it here would suppress today's scheduled check.
        self.updates_checking.store(false, Ordering::Release);
        self.queue.updater.changed()?;
        Ok(result)
    }
    pub fn updates_status(&self) -> Result<UpdatesStatus, AppError> {
        self.updates_status_at(chrono::Utc::now().timestamp_millis())
    }
    pub fn updates_status_at(&self, now: i64) -> Result<UpdatesStatus, AppError> {
        let checked_at = *self.checked_at.read().map_err(poisoned)?;
        let settings = self.current_settings()?;
        let planned_at = self.schedule_plan_at.load(Ordering::Acquire);
        let effective = checked_at.filter(|at| *at >= planned_at);
        let mut next = crate::scheduler::next_check(&settings, effective, now);
        if effective.is_none() && planned_at > 0 && next.is_some_and(|at| at <= planned_at) {
            // If the newly configured time has passed (including a two-minute schedule across midnight), schedule the next day.
            next = crate::scheduler::next_check(&settings, Some(planned_at), now);
        }
        Ok(UpdatesStatus {
            checked_at,
            next_check_at: next,
            checking: self.updates_checking.load(Ordering::Acquire)
                || self.source_checks_active.load(Ordering::Acquire),
            failures: self
                .database
                .meta("updates_failures")?
                .and_then(|s| serde_json::from_str(&s).ok())
                .unwrap_or_default(),
            failed_sources: self
                .database
                .meta("updates_failed_sources")?
                .and_then(|s| serde_json::from_str(&s).ok())
                .unwrap_or_default(),
        })
    }
    pub async fn ignore_update(
        self: &Arc<Self>,
        target: TaskTarget,
        version: Option<String>,
        until: Option<i64>,
    ) -> Result<(), AppError> {
        let database = self.database.clone();
        crate::blocking(move || {
            crate::updates::ignore(
                &database,
                &target,
                version.as_deref(),
                until,
                chrono::Utc::now().timestamp_millis(),
            )
        })
        .await?;
        self.rebuild_updates().await?;
        Ok(())
    }
    pub async fn unignore_update(self: &Arc<Self>, target: TaskTarget) -> Result<(), AppError> {
        let database = self.database.clone();
        crate::blocking(move || crate::updates::unignore(&database, &target)).await?;
        self.rebuild_updates().await?;
        Ok(())
    }
    pub async fn refresh_library(
        self: &Arc<Self>,
        reason: LibraryChangeReason,
        target: Option<&TaskTarget>,
    ) -> Result<Vec<InstalledItem>, AppError> {
        let _refresh = self.refresh_lock.lock().await;
        let _read = self.queue.read_gate.read().await;
        let settings = self.current_settings()?;
        let brew = self.brew_info().await?;
        let has_info = {
            let info = self.installed_info.read().map_err(poisoned)?;
            !info.formulae.is_empty() || !info.casks.is_empty()
        };
        let brew_target = target
            .map(|target| {
                let library = self.library.read().map_err(poisoned)?;
                let installed = library
                    .iter()
                    .find(|i| i.kind == target.kind && i.matches_token(&target.token));
                let mut resolved = target.clone();
                let name = if target.kind == TaskKind::Cask {
                    if let Some(installed) = installed {
                        installed.install_token().to_owned()
                    } else if target.token.contains('/') {
                        crate::brew::args::split_cask_token(&target.token)?;
                        target.token.clone()
                    } else {
                        let catalog =
                            crate::catalog::get(&self.database, Kind::Cask, &target.token)?;
                        let upstream = crate::brew::args::catalog_target(target, catalog.as_ref())?;
                        crate::brew::args::qualified_token(
                            catalog.as_ref().and_then(|i| i.tap.as_deref()),
                            &upstream.token,
                        )?
                    }
                } else {
                    target.token.clone()
                };
                if target.kind == TaskKind::Cask {
                    resolved.token = name.clone();
                } else if let Some(installed) = installed {
                    resolved.token = installed.brew_token().to_owned();
                }
                Ok::<_, AppError>((resolved, name))
            })
            .transpose()?;
        let (op, token) = if has_info {
            brew_target
                .as_ref()
                .map(|(t, name)| {
                    t.kind
                        .brew_kind()
                        .map(|kind| (ReadOp::Info(kind), Some(name.as_str())))
                })
                .transpose()?
                .unwrap_or((ReadOp::Installed, None))
        } else {
            (ReadOp::Installed, None)
        };
        let raw = crate::brew::locate::read(&brew, &settings, op, token).await?;
        if raw.len() > 32 * 1024 * 1024 {
            return Err(AppError::new("E_UNKNOWN", "installed_json_size"));
        }
        if target.is_none() {
            self.database.set_meta("cask_rules_cache", &raw)?;
        }
        let core = self.clone();
        let target = brew_target.map(|(target, _)| target);
        let prefix = brew.prefix.clone();
        let mut scan = crate::blocking(move || {
            let incoming: Info = serde_json::from_str(&raw)?;
            let mut info = core.installed_info.write().map_err(poisoned)?;
            if has_info && target.is_some() {
                let target = target
                    .as_ref()
                    .ok_or_else(|| AppError::new("E_UNKNOWN", "scan_target"))?;
                match target.kind.brew_kind()? {
                    Kind::Link => return Err(AppError::new("E_NOT_INSTALLABLE", "link_target")),
                    Kind::Cask => {
                        let (tap, upstream) = crate::brew::args::split_cask_token(&target.token)?;
                        let matches = |i: &crate::brew::json::Cask| {
                            i.token == upstream
                                && i.tap.as_deref().unwrap_or("homebrew/cask")
                                    == tap.unwrap_or("homebrew/cask")
                        };
                        info.casks.retain(|i| !matches(i));
                        info.casks
                            .extend(incoming.casks.into_iter().filter(matches));
                    }
                    Kind::Formula => {
                        info.formulae.retain(|i| i.name != target.token);
                        info.formulae.extend(
                            incoming
                                .formulae
                                .into_iter()
                                .filter(|i| i.name == target.token),
                        );
                    }
                }
            } else {
                *info = incoming;
            }
            library::scan(
                &core.database,
                &info,
                &prefix,
                core.current_settings()?.locale,
            )
        })
        .await?;
        let generation = self.library_generation.fetch_add(1, Ordering::AcqRel) + 1;
        let cache = self.storage_summary()?.cache_bytes;
        self.publish_library(&scan.items, cache, false, reason, generation)?;
        let initial = scan.items.clone();
        let database = self.database.clone();
        let value = serde_json::to_string(&initial)?;
        crate::blocking(move || database.set_meta("installed_cache", &value)).await?;
        let core = self.clone();
        let icons = self.cache_dir.join("icons");
        let cache_path =
            crate::brew::locate::read(&brew, &self.current_settings()?, ReadOp::Cache, None)
                .await
                .ok()
                .map(PathBuf::from)
                .filter(|p| p.is_absolute());
        tauri::async_runtime::spawn(async move {
            if let Err(error) = crate::blocking(move || {
                library::enrich(&core.database, &mut scan, &icons, |items| {
                    if core.library_generation.load(Ordering::Acquire) == generation {
                        let _ = core.publish_enrichment(items, cache, false, reason,generation);
                    }
                });
                let cache_bytes = cache_path
                    .and_then(|p| library::files::size(&core.database, &p).ok())
                    .unwrap_or(cache);
                if core.library_generation.load(Ordering::Acquire) == generation {
                    core.publish_enrichment(&scan.items, cache_bytes, true, reason,generation)?;
                    let installed=serde_json::to_string(&core.library_list()?)?;let storage=serde_json::to_string(&library::storage(&scan.items,cache_bytes,true))?;
                    core.database.with(|connection|{if core.library_generation.load(Ordering::Acquire)==generation{let tx=connection.transaction()?;for (key,value) in [("installed_cache",&installed),("storage_cache",&storage)]{tx.execute("INSERT INTO meta(key,value) VALUES(?1,?2) ON CONFLICT(key) DO UPDATE SET value=excluded.value",rusqlite::params![key,value])?;}tx.commit()?;}Ok(())})?;
                }
                Ok(())
            })
            .await
            {
                log::warn!("Failed to supplement installed sizes or icons: {}", error.code);
            }
        });
        Ok(initial)
    }
    pub async fn detect_environment(&self) -> Result<EnvInfo, AppError> {
        let permission = *self.app_management.read().map_err(poisoned)?;
        let mut info = crate::brew::locate::detect(&self.current_settings()?, permission).await;
        // Tasks may complete during system probing; prefer available system preflight, otherwise infer from task outcomes (06 §12.3).
        info.app_management = self.permissions().await?.app_management;
        *self.brew.write().map_err(poisoned)? = info.brew.clone();
        Ok(info)
    }
    /// Current privacy permissions (06 §12.3, §12.6): run system preflight and protected-directory probes on blocking threads.
    pub async fn permissions(&self) -> Result<Permissions, AppError> {
        let inferred = *self.app_management.read().map_err(poisoned)?;
        let blocked = self.app_management_blocked.load(Ordering::Acquire);
        let source = *self.permission_signals.read().map_err(poisoned)?;
        let signals = crate::blocking(move || Ok(source(inferred, blocked))).await?;
        Ok(self.combine_permissions(signals))
    }
    /// Tests: infer only from task outcomes without system preflight, independent of this Mac's permissions.
    #[cfg(test)]
    pub(crate) fn infer_permissions_only(&self) {
        *self.permission_signals.write().unwrap() =
            |inferred, blocked| crate::permissions::Signals {
                app_bundles: None,
                all_files: None,
                full_disk_readable: None,
                inferred_app_management: inferred,
                app_management_blocked_after_grant: blocked,
                relaunch_ineffective: false,
            };
    }
    /// Keep inference tests independent of installed apps and native post-task metadata scans.
    #[cfg(test)]
    pub(crate) fn install_permission_test_completion(self: &Arc<Self>) {
        let weak = Arc::downgrade(self);
        self.queue
            .set_completion(Arc::new(move |task| {
                let weak = weak.clone();
                Box::pin(async move {
                    if let Some(core) = weak.upgrade() {
                        core.infer_app_management(&task)
                            .await
                            .expect("permission inference");
                    }
                })
            }))
            .expect("test completion");
    }
    /// Same behavior, callable directly from the guidance polling thread.
    pub fn permissions_blocking(&self) -> Result<Permissions, AppError> {
        let inferred = *self.app_management.read().map_err(poisoned)?;
        let blocked = self.app_management_blocked.load(Ordering::Acquire);
        let source = *self.permission_signals.read().map_err(poisoned)?;
        Ok(self.combine_permissions(source(inferred, blocked)))
    }
    fn combine_permissions(&self, mut signals: crate::permissions::Signals) -> Permissions {
        // Once a read stops being pending, this process never raises the relaunch warning again.
        signals.relaunch_ineffective = self
            .relaunch_ineffective
            .fetch_and(signals.full_disk_pending(), Ordering::AcqRel);
        crate::permissions::combine(signals)
    }
    async fn infer_app_management(self: &Arc<Self>, task: &Task) -> Result<(), AppError> {
        let next = if task.state == TaskState::Failed
            && task.error.as_ref().is_some_and(|error| {
                matches!(error.code.as_str(), "E_PERMISSION" | "E_APP_MANAGEMENT")
            }) {
            let source = *self.permission_signals.read().map_err(poisoned)?;
            let signals =
                crate::blocking(move || Ok(source(PermissionState::Unknown, false))).await?;
            if signals.app_bundles == Some(crate::permissions::Preflight::Granted) {
                self.app_management_blocked.store(true, Ordering::Release);
            }
            PermissionState::Denied
        } else if task.state == TaskState::Succeeded
            && matches!(task.op, TaskOp::Upgrade | TaskOp::Uninstall)
            && task.target.as_ref().is_some_and(|target| {
                target.kind == Kind::Cask
                    || (target.kind == TaskKind::App
                        && crate::uninstall::manifest::entries(&self.database, &task.id).is_ok_and(
                            |entries| {
                                entries.iter().any(|e| {
                                    e.original.extension().is_some_and(|x| x == "app")
                                        && matches!(e.state.as_str(), "trash" | "deleted")
                                })
                            },
                        ))
            })
        {
            let info = self.installed_info.read().map_err(poisoned)?;
            let target = task
                .target
                .as_ref()
                .ok_or_else(|| AppError::new("E_UNKNOWN", "missing_task_target"))?;
            let catalog = if target.kind == Kind::Cask && !target.token.contains('/') {
                crate::catalog::get(&self.database, Kind::Cask, &target.token)?
            } else {
                None
            };
            let upstream = crate::brew::args::catalog_target(target, catalog.as_ref())?;
            let (tap, token) = if target.kind == Kind::Cask {
                let (tap, token) = crate::brew::args::split_cask_token(&upstream.token)?;
                (
                    tap.or_else(|| catalog.as_ref().and_then(|i| i.tap.as_deref())),
                    token,
                )
            } else {
                (None, upstream.token.as_str())
            };
            // With cached artifacts, exclude fonts/pkg Casks without app artifacts; without metadata, approximate using Cask kind.
            if let Some(cask) = info.casks.iter().find(|cask| {
                cask.token == token
                    && cask.tap.as_deref().unwrap_or("homebrew/cask")
                        == tap.unwrap_or("homebrew/cask")
            }) && cask.artifacts.as_ref().is_some_and(|artifacts| {
                !artifacts
                    .iter()
                    .any(|artifact| artifact.get("app").is_some())
            }) {
                return Ok(());
            }
            PermissionState::Granted
        } else {
            return Ok(());
        };
        let _guard = self.permission_lock.lock().await;
        if *self.app_management.read().map_err(poisoned)? == next {
            // Even unchanged inferred values may newly require relaunch; push the full permission state.
            (self.sink)(CoreEvent::Permissions(self.permissions().await?));
            return Ok(());
        }
        let core = self.clone();
        crate::blocking(move || {
            core.database.set_meta(
                "app_management",
                match next {
                    PermissionState::Granted => "granted",
                    PermissionState::Denied => "denied",
                    PermissionState::Unknown => "unknown",
                },
            )?;
            *core.app_management.write().map_err(poisoned)? = next;
            Ok(())
        })
        .await?;
        (self.sink)(CoreEvent::Permissions(self.permissions().await?));
        (self.sink)(CoreEvent::Env(self.detect_environment().await?));
        Ok(())
    }
    /// A successful task settles its target in the update list at once: an upgrade, reinstall or uninstall
    /// drops it, a pin or unpin flips its pinned flag. The cached brew outdated output changes with it, so
    /// rebuilds agree, and no window (nor the menu bar count) keeps the old state while the post-task library
    /// scan and brew outdated check below (seconds) rebuild the list.
    async fn settle_updates(self: &Arc<Self>, task: &Task) -> Result<(), AppError> {
        let pinned = match task.op {
            TaskOp::Upgrade | TaskOp::Reinstall | TaskOp::Uninstall => None,
            TaskOp::Pin => Some(true),
            TaskOp::Unpin => Some(false),
            _ => return Ok(()),
        };
        if task.state != TaskState::Succeeded {
            return Ok(());
        }
        let Some(target) = task.target.clone() else {
            return Ok(());
        };
        let Ok(kind) = target.kind.brew_kind() else {
            return Ok(());
        };
        let core = self.clone();
        crate::blocking(move || {
            let library = core.library.read().map_err(poisoned)?;
            let local = library
                .iter()
                .find(|i| i.kind == kind && i.matches_token(&target.token));
            let source_identity = local
                .map(|i| {
                    if kind == Kind::Cask {
                        i.install_token()
                    } else {
                        i.brew_token()
                    }
                })
                .unwrap_or(&target.token)
                .to_owned();
            let ambiguous = local.is_some_and(|local| {
                library
                    .iter()
                    .filter(|other| other.kind == kind && other.brew_token() == local.brew_token())
                    .count()
                    > 1
            });
            let site_token = local
                .map(|i| i.token.as_str())
                .unwrap_or(&target.token)
                .to_owned();
            drop(library);
            let matches = |item: &OutdatedItem| item.kind == kind && item.token == site_token;
            let mut source = core.outdated_source.write().map_err(poisoned)?;
            let entries = match kind {
                Kind::Cask => &mut source.casks,
                Kind::Formula => &mut source.formulae,
                Kind::Link => return Ok(()),
            };
            let Some(index) = entries.iter().position(|entry| {
                if kind != Kind::Cask {
                    return entry.name == source_identity;
                }
                let Ok((tap, upstream)) = crate::brew::args::split_cask_token(&entry.name) else {
                    return false;
                };
                let Ok((source_tap, source_token)) =
                    crate::brew::args::split_cask_token(&source_identity)
                else {
                    return false;
                };
                upstream == source_token
                    && match tap {
                        Some(tap) => Some(tap) == source_tap,
                        None => !ambiguous,
                    }
            }) else {
                return Ok(());
            };
            match pinned {
                None => {
                    entries.remove(index);
                }
                Some(pinned) => entries[index].pinned = pinned,
            }
            core.database
                .set_meta("outdated_source", &serde_json::to_string(&*source)?)?;
            drop(source);
            let mut updates = core.updates.write().map_err(poisoned)?;
            match pinned {
                None => updates.retain(|item| !matches(item)),
                Some(pinned) => updates
                    .iter_mut()
                    .filter(|item| matches(item))
                    .for_each(|item| item.pinned = pinned),
            }
            core.database
                .set_meta("updates_cache", &serde_json::to_string(&*updates)?)?;
            drop(updates);
            core.queue.updater.changed()?;
            Ok(())
        })
        .await
    }
    pub async fn after_task(self: &Arc<Self>, task: &Task) {
        if self.queue.is_stopping() {
            return;
        }
        if let Err(error) = self.settle_updates(task).await {
            log::info!("Could not settle updates after the task: {}", error.code);
        }
        if let Err(error) = self.infer_app_management(task).await {
            log::warn!(
                "Failed to save App Management permission state: {}",
                error.code
            );
        }
        // Apps outside Homebrew need only a new local list; the completion notice below still applies to them.
        if task
            .target
            .as_ref()
            .is_some_and(|t| matches!(t.kind, TaskKind::App | TaskKind::Mas))
        {
            if task
                .target
                .as_ref()
                .is_some_and(|t| t.kind == TaskKind::Mas)
            {
                let _ = self.appstore.installed().await;
            }
            let engine = self.queue.uninstall_engine.clone();
            let _ = crate::blocking(move || engine.refresh(LocalAppsChangedReason::Task)).await;
            if task
                .target
                .as_ref()
                .is_some_and(|t| t.kind == TaskKind::Mas)
                && let Err(error) = self
                    .queue
                    .updater
                    .check(&[UpdateSource::Mas], &self.appstore, false)
                    .await
            {
                log::info!("Store updates refresh deferred: {}", error.code);
            }
            if let Ok(permissions) = self.permissions().await {
                let _ = self
                    .queue
                    .uninstall_engine
                    .task_footprints(task, permissions)
                    .await;
            }
            self.notice(crate::scheduler::Notice::TaskFinished(Box::new(
                task.clone(),
            )));
            return;
        }
        if task.op == TaskOp::InstallHomebrew
            && task.state == TaskState::Succeeded
            && let Ok(info) = self.detect_environment().await
        {
            (self.sink)(CoreEvent::Env(info));
        }
        if task.error.as_ref().is_some_and(|e| e.code == "E_NOT_FOUND") {
            let core = self.clone();
            tauri::async_runtime::spawn(async move {
                let _ = core.sync_catalog(false).await;
            });
        }
        // brew writes may install, upgrade, or remove dependencies; reread the full inventory after completion.
        if let Err(error) = self.refresh_library(LibraryChangeReason::Task, None).await {
            log::info!("Post-task scan temporarily unavailable: {}", error.code);
        }
        // The app list reads the inventory just written, not the update check; refresh it first so Installed
        // shows the change without waiting for brew outdated.
        if task.options.plan_id.is_some()
            || (task
                .target
                .as_ref()
                .is_some_and(|target| target.kind == TaskKind::Cask)
                && matches!(
                    task.op,
                    TaskOp::Install | TaskOp::Upgrade | TaskOp::Reinstall | TaskOp::Uninstall
                ))
        {
            let engine = self.queue.uninstall_engine.clone();
            let _ = crate::blocking(move || engine.refresh(LocalAppsChangedReason::Task)).await;
        }
        if let Err(error) = self.check_updates(false).await {
            log::info!(
                "Post-task update check temporarily unavailable: {}",
                error.code
            );
        }
        if let Ok(permissions) = self.permissions().await {
            let _ = self
                .queue
                .uninstall_engine
                .task_footprints(task, permissions)
                .await;
        }
        self.notice(crate::scheduler::Notice::TaskFinished(Box::new(
            task.clone(),
        )));
    }
}

#[cfg(test)]
mod alias_tests {
    use super::*;

    #[tokio::test]
    async fn targeted_refresh_and_settlement_preserve_installed_cask_identity() {
        use std::os::unix::fs::PermissionsExt;
        for (tap, site_token, read_token) in [
            ("stablyai/orca", "stablyai-orca", "stablyai/orca/orca"),
            ("homebrew/cask", "orca", "homebrew/cask/orca"),
        ] {
            let temp = tempfile::tempdir().unwrap();
            let core = DesktopCore::open(
                temp.path().join("data"),
                temp.path().join("cache"),
                temp.path().join("askpass"),
                Arc::new(|_| {}),
            )
            .unwrap();
            let mut official = crate::catalog::links::tests::item();
            official.kind = Kind::Cask;
            official.token = "orca".into();
            official.tap = None;
            official.brew_token = None;
            let mut alias = official.clone();
            alias.token = "stablyai-orca".into();
            alias.tap = Some("stablyai/orca".into());
            alias.brew_token = Some("orca".into());
            core.database
                .with(|c| {
                    crate::catalog::upsert(c, &official)?;
                    crate::catalog::upsert(c, &alias)
                })
                .unwrap();
            let raw = serde_json::json!({"casks": [{"token": "orca", "tap": tap, "version": "1", "installed": "1", "artifacts": []}]}).to_string();
            let mut info: Info = serde_json::from_str(&raw).unwrap();
            let (other_tap, other_token) = if tap == "stablyai/orca" {
                ("homebrew/cask", "orca")
            } else {
                ("stablyai/orca", "stablyai-orca")
            };
            let mut other = info.casks[0].clone();
            other.tap = Some(other_tap.into());
            info.casks.push(other);
            *core.library.write().unwrap() = library::scan(
                &core.database,
                &info,
                temp.path().to_str().unwrap(),
                Locale::EnUs,
            )
            .unwrap()
            .items;
            *core.installed_info.write().unwrap() = info;
            let script = temp.path().join("brew");
            std::fs::write(&script, format!(
                "#!/usr/bin/python3 -I\nimport sys\na=sys.argv[1:]\nif a==['--version']: print('Homebrew 7.0.7')\nelif a in (['--prefix'],['--cache']): print({prefix:?})\nelif a==['info','--json=v2','--cask',{read_token:?}]: print({raw:?})\nelse: sys.exit(2)\n",
                prefix = temp.path().join("cache").to_string_lossy(),
            )).unwrap();
            std::fs::set_permissions(&script, std::fs::Permissions::from_mode(0o700)).unwrap();
            core.settings.write().unwrap().brew_path = Some(script.to_string_lossy().into_owned());
            let target = TaskTarget {
                kind: TaskKind::Cask,
                token: site_token.into(),
            };
            let refreshed = core
                .refresh_library(LibraryChangeReason::Task, Some(&target))
                .await
                .unwrap();
            assert_eq!(refreshed.len(), 2);
            let refreshed = refreshed
                .iter()
                .find(|item| item.token == site_token)
                .unwrap();
            assert_eq!(refreshed.full_token, Some(format!("{tap}/orca")));
            assert!(
                core.library
                    .read()
                    .unwrap()
                    .iter()
                    .any(|item| item.token == other_token)
            );
            let source: crate::updates::Outdated =
                serde_json::from_value(serde_json::json!({"casks": [
                    {"name": format!("{tap}/orca"), "current_version": "2"},
                    {"name": format!("{other_tap}/orca"), "current_version": "2"}
                ]}))
                .unwrap();
            let mut task = core
                .queue
                .enqueue(
                    TaskOp::Upgrade,
                    Some(target),
                    TaskOptions::default(),
                    TaskTrigger::Manual,
                )
                .unwrap();
            task.state = TaskState::Succeeded;
            for op in [TaskOp::Upgrade, TaskOp::Uninstall] {
                *core.outdated_source.write().unwrap() = source.clone();
                // Build the Homebrew list without enumerating this Mac's native apps.
                let updates = crate::updates::build(
                    &core.database,
                    &source,
                    &mut core.library.write().unwrap(),
                    Locale::EnUs,
                    1,
                )
                .unwrap();
                assert_eq!(updates.len(), 2);
                assert!(updates.iter().any(|item| item.token == site_token));
                *core.updates.write().unwrap() = updates;
                task.op = op;
                task.target.as_mut().unwrap().token = if op == TaskOp::Uninstall {
                    read_token
                } else {
                    site_token
                }
                .into();
                core.settle_updates(&task).await.unwrap();
                let source = core.outdated_source.read().unwrap();
                assert_eq!(source.casks.len(), 1);
                assert_eq!(source.casks[0].name, format!("{other_tap}/orca"));
                let updates = core.updates.read().unwrap();
                assert_eq!(updates.len(), 1);
                assert_eq!(updates[0].token, other_token);
            }
        }
    }
}

#[cfg(test)]
mod permission_tests {
    use super::*;
    fn pending_full_disk(inferred: PermissionState, blocked: bool) -> crate::permissions::Signals {
        crate::permissions::Signals {
            app_bundles: None,
            all_files: Some(crate::permissions::Preflight::Granted),
            full_disk_readable: Some(false),
            inferred_app_management: inferred,
            app_management_blocked_after_grant: blocked,
            relaunch_ineffective: false,
        }
    }

    #[tokio::test]
    async fn ineffective_relaunch_clears_permanently_from_either_permission_reader() {
        for blocking_first in [true, false] {
            let tmp = tempfile::tempdir().unwrap();
            let core = DesktopCore::open(
                tmp.path().join("data"),
                tmp.path().join("cache"),
                tmp.path().join("askpass"),
                Arc::new(|_| {}),
            )
            .unwrap();
            core.relaunch_ineffective.store(true, Ordering::Release);
            *core.permission_signals.write().unwrap() = pending_full_disk;
            assert!(core.permissions().await.unwrap().relaunch_ineffective);
            assert!(core.permissions_blocking().unwrap().relaunch_ineffective);
            core.infer_permissions_only();
            let cleared = if blocking_first {
                core.permissions_blocking().unwrap()
            } else {
                core.permissions().await.unwrap()
            };
            assert!(!cleared.relaunch_ineffective);
            *core.permission_signals.write().unwrap() = pending_full_disk;
            assert!(core.permissions().await.unwrap().full_disk_access_pending);
            assert!(!core.permissions().await.unwrap().relaunch_ineffective);
            assert!(!core.permissions_blocking().unwrap().relaunch_ineffective);
        }
    }

    #[tokio::test]
    async fn known_non_app_cask_and_other_operations_do_not_grant_permission() {
        let tmp = tempfile::tempdir().unwrap();
        let core = DesktopCore::open(
            tmp.path().join("data"),
            tmp.path().join("cache"),
            tmp.path().join("askpass"),
            Arc::new(|_| {}),
        )
        .unwrap();
        // Without system preflight, env_detect returns state inferred from task outcomes.
        core.infer_permissions_only();
        core.settings.write().unwrap().brew_path = Some(
            std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
                .join("tests/fake-brew/brew")
                .to_string_lossy()
                .into(),
        );
        *core.installed_info.write().unwrap() =
            serde_json::from_value(serde_json::json!({"casks":[
                {"token":"font-test", "version":"1", "artifacts":[{"font":["test.ttf"]}]},
                {"token":"app-test", "version":"1", "artifacts":[{"app":["Test.app"]}]},
                {"token":"unknown-test", "version":"1"}
            ]}))
            .unwrap();
        for op in [
            TaskOp::Upgrade,
            TaskOp::Uninstall,
            TaskOp::Install,
            TaskOp::Reinstall,
        ] {
            let mut task = core
                .queue
                .enqueue(
                    op,
                    Some(TaskTarget {
                        kind: Kind::Cask.into(),
                        token: "font-test".into(),
                    }),
                    TaskOptions::default(),
                    TaskTrigger::Manual,
                )
                .unwrap();
            task.state = TaskState::Succeeded;
            core.infer_app_management(&task).await.unwrap();
            assert!(core.database.meta("app_management").unwrap().is_none());
        }
        let mut unknown = core
            .queue
            .enqueue(
                TaskOp::Upgrade,
                Some(TaskTarget {
                    kind: Kind::Cask.into(),
                    token: "unknown-test".into(),
                }),
                TaskOptions::default(),
                TaskTrigger::Manual,
            )
            .unwrap();
        unknown.state = TaskState::Succeeded;
        core.infer_app_management(&unknown).await.unwrap();
        assert_eq!(
            core.detect_environment().await.unwrap().app_management,
            PermissionState::Granted
        );
        unknown.state = TaskState::Failed;
        unknown.error = Some(AppError::new("E_PERMISSION", "permission"));
        core.infer_app_management(&unknown).await.unwrap();
        assert_eq!(
            core.detect_environment().await.unwrap().app_management,
            PermissionState::Denied
        );
        let mut task = core
            .queue
            .enqueue(
                TaskOp::Upgrade,
                Some(TaskTarget {
                    kind: Kind::Cask.into(),
                    token: "app-test".into(),
                }),
                TaskOptions::default(),
                TaskTrigger::Manual,
            )
            .unwrap();
        task.state = TaskState::Succeeded;
        core.infer_app_management(&task).await.unwrap();
        assert_eq!(
            core.detect_environment().await.unwrap().app_management,
            PermissionState::Granted
        );
    }

    #[tokio::test]
    async fn permission_failure_after_grant_requires_relaunch() {
        let tmp = tempfile::tempdir().unwrap();
        let events = Arc::new(std::sync::Mutex::new(Vec::new()));
        let captured = events.clone();
        let core = DesktopCore::open(
            tmp.path().join("data"),
            tmp.path().join("cache"),
            tmp.path().join("askpass"),
            Arc::new(move |event| captured.lock().unwrap().push(event)),
        )
        .unwrap();
        // System preflight allows App Management, but it is not effective in this process.
        *core.permission_signals.write().unwrap() =
            |inferred, blocked| crate::permissions::Signals {
                app_bundles: Some(crate::permissions::Preflight::Granted),
                all_files: Some(crate::permissions::Preflight::NotDetermined),
                full_disk_readable: Some(false),
                inferred_app_management: inferred,
                app_management_blocked_after_grant: blocked,
                relaunch_ineffective: false,
            };
        *core.app_management.write().unwrap() = PermissionState::Denied;
        let before = core.permissions().await.unwrap();
        assert_eq!(before.app_management, PermissionState::Granted);
        assert!(!before.relaunch_required);
        let mut task = core
            .queue
            .enqueue(
                TaskOp::Uninstall,
                Some(TaskTarget {
                    kind: Kind::Cask.into(),
                    token: "app-test".into(),
                }),
                TaskOptions::default(),
                TaskTrigger::Manual,
            )
            .unwrap();
        task.state = TaskState::Failed;
        task.error = Some(AppError::new("E_PERMISSION", "permission"));
        core.infer_app_management(&task).await.unwrap();
        assert!(core.permissions().await.unwrap().relaunch_required);
        assert!(events.lock().unwrap().iter().any(|event| matches!(
            event, CoreEvent::Permissions(state) if state.relaunch_required
        )));
    }
}
