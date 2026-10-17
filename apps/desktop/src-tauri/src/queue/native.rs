use super::*;
use crate::{
    appstore::install::Runner,
    updater::{admin, download, gate, rollback, service, swap},
};

/// Downloads and extracted bundles wait here. `.noindex` keeps Spotlight, and so
/// LaunchServices, from registering an extracted app as a second copy.
pub(crate) const NATIVE_STAGING: &str = "native-staging.noindex";
#[derive(Default)]
pub(super) struct Runtime {
    pub store: Option<Runner>,
    pub admin: Option<Arc<dyn admin::Actions>>,
}
fn completed() -> crate::brew::pty::Outcome {
    crate::brew::pty::Outcome {
        code: 0,
        canceled: false,
        #[cfg(test)]
        signal: None,
        parser: Default::default(),
    }
}
impl TaskQueue {
    pub(super) fn validate_native(
        &self,
        op: TaskOp,
        target: &TaskTarget,
        options: &TaskOptions,
    ) -> Result<bool, AppError> {
        let native = target.kind == TaskKind::Mas
            || (target.kind == TaskKind::App && matches!(op, TaskOp::Upgrade | TaskOp::Rollback));
        if !native {
            return Ok(false);
        }
        if options.plan_id.is_some()
            || options.item_ids.is_some()
            || options.zap
            || options.adopt
            || options.force
            || options.delete_mode.is_some()
        {
            return Err(AppError::new("E_INVALID_ARG", "native_install_options"));
        }
        if op == TaskOp::Rollback {
            let history = options
                .rollback_of
                .as_deref()
                .ok_or_else(|| AppError::new("E_INVALID_ARG", "rollback_options"))?;
            let plan = self.rollback_plan(history)?;
            if plan.app_id != target.token || options.source != Some(plan.source) {
                return Err(AppError::new("E_INVALID_ARG", "rollback_options"));
            }
            return Ok(true);
        }
        if target.kind == TaskKind::Mas {
            if !matches!(op, TaskOp::Install | TaskOp::Upgrade)
                || options.source.is_some_and(|s| s != UpdateSource::Mas)
            {
                return Err(AppError::new("E_INVALID_ARG", "mas_task_operation"));
            }
            let id = target
                .token
                .parse::<u64>()
                .ok()
                .filter(|id| crate::appstore::valid_adam_id(*id) && id.to_string() == target.token)
                .ok_or_else(|| AppError::new("E_INVALID_ARG", "adam_id"))?;
            let _ = id;
            if self.native_runtime.lock().map_err(poison)?.store.is_none() {
                Runner::native()?;
            }
        } else {
            let source = options
                .source
                .ok_or_else(|| AppError::new("E_INVALID_ARG", "update_source_required"))?;
            if !matches!(
                source,
                UpdateSource::Sparkle
                    | UpdateSource::Electron
                    | UpdateSource::Github
                    | UpdateSource::Vendor
            ) {
                return Err(crate::updater::not_implemented(
                    "update_source_not_available",
                ));
            }
            let candidate = self.updater.candidate(&target.token, source)?;
            candidate.item.task_target()?;
            candidate.validate_identity()?;
            let app = self.uninstall_engine.app(&target.token)?;
            if candidate
                .expected_team
                .as_ref()
                .is_some_and(|t| app.app.team_id.as_ref() != Some(t))
            {
                return Err(AppError::new(
                    "E_UPDATE_TEAM_MISMATCH",
                    "rule_team_mismatch",
                ));
            }
            if app.app.protected_reason.is_some() {
                return Err(AppError::new("E_PROTECTED", "protected_update_target"));
            }
        }
        Ok(true)
    }
    fn stage(&self) -> Result<download::Stage, AppError> {
        download::Stage::create(
            &self
                .uninstall_engine
                .admin_root()
                .with_file_name(NATIVE_STAGING),
        )
    }
    pub(super) async fn execute_app_update(
        self: &Arc<Self>,
        task: &Task,
        cancel: Arc<AtomicBool>,
        notify: Arc<Notify>,
    ) -> Result<crate::brew::pty::Outcome, AppError> {
        let _operation = self.read_gate.write().await;
        let target = task
            .target
            .as_ref()
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "update_target"))?;
        let source = task
            .options
            .source
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "update_source_required"))?;
        let candidate = self.updater.candidate(&target.token, source)?;
        let identity = Arc::new(crate::running_apps::Identity {
            paths: vec![candidate.app_path.clone()],
            bundle_ids: vec![],
            ..Default::default()
        });
        let reopen = crate::running_apps::prepare(
            identity.clone(),
            self.app_control.clone(),
            task.options.quit_running
                && !matches!(task.trigger, TaskTrigger::Schedule | TaskTrigger::Bundle),
            cancel.clone(),
            notify.clone(),
            Duration::from_secs(15),
        )
        .await
        .map_err(|mut error| {
            // Native updates must report refusal and timeout as an app still running.
            // Keep cancellation/check failures intact and preserve the diagnostic message.
            if error.code == "E_APP_QUIT_FAILED" {
                error.code = "E_APP_RUNNING".into();
            }
            error
        })?;
        let stage = self.stage()?;
        let queue = self.clone();
        let id = task.id.clone();
        let request = download::prepare(
            &candidate,
            &stage,
            cancel.clone(),
            Arc::new(move |progress| queue.progress(&id, &[], progress, true)),
        )
        .await?;
        crate::running_apps::prepare(
            identity,
            self.app_control.clone(),
            false,
            cancel.clone(),
            notify,
            Duration::ZERO,
        )
        .await?;
        if cancel.load(Ordering::Acquire) {
            return Err(AppError::new("E_INTERRUPTED", "update_canceled"));
        }
        self.progress(
            &task.id,
            &[],
            &Progress {
                phase: Some(TaskPhase::Installing),
                percent: None,
                step_index: Some(2),
                from: Some(candidate.item.installed_version.clone()),
                to: Some(candidate.item.current_version.clone()),
                bytes: None,
                total: None,
                speed: None,
            },
            true,
        )?;
        let (os, arch) = service::system()?;
        let validation = request.clone();
        crate::blocking(move || {
            gate::bundles_for_source(
                &gate::NativeInspector,
                &validation.installed,
                &validation.prepared,
                &validation.installed_snapshot,
                gate::Platform {
                    os: &os,
                    arch: &arch,
                },
                validation.expected_team.as_deref(),
                validation.source,
            )
        })
        .await?;
        let backup = if request.package.is_some() || service::requires_admin(&request.installed) {
            let requests = vec![admin::Request {
                id: task.id.clone(),
                action: if request.package.is_some() {
                    admin::Action::PkgInstall {
                        request: Box::new(request.clone()),
                    }
                } else {
                    admin::Action::AppSwap {
                        request: Box::new(request.clone()),
                    }
                },
            }];
            let results = self.run_native_admin(requests).await?;
            let result = results
                .into_iter()
                .find(|r| r.id == task.id)
                .ok_or_else(|| AppError::new("E_UNKNOWN", "admin_result_missing"))?;
            if let Some(error) = result.error {
                return Err(error);
            }
            result.backup
        } else {
            let request = request.clone();
            let (os, arch) = service::system()?;
            Some(
                crate::blocking(move || {
                    swap::apply(
                        &request,
                        &swap::Context {
                            inspector: &gate::NativeInspector,
                            files: &swap::NativeFiles,
                            os: &os,
                            arch: &arch,
                        },
                    )
                })
                .await?,
            )
        };
        // Disk replacement is already committed. Auxiliary persistence or reopen
        // errors must not report an unrolled-back update as failed.
        if let Some(backup) = backup
            && let Err(error) = self.database.set_meta(&format!("update_backup:{}",task.id),&serde_json::json!({"appId":target.token,"path":backup,"fromVersion":candidate.item.installed_version,"toVersion":candidate.item.current_version}).to_string())
        { log::warn!("Update backup history could not be saved: {}", error.code); }
        let old_running = self
            .app_control
            .list()
            .map(|processes| {
                processes
                    .iter()
                    .any(|p| p.path.starts_with(&request.installed))
            })
            .unwrap_or(true);
        if let Err(error) = self.updater.installed(&target.token, old_running) {
            log::warn!("Update list refresh deferred: {}", error.code);
        }
        if task.options.reopen && !old_running {
            for path in reopen {
                if let Err(error) = self.app_control.reopen(&path) {
                    log::warn!("Updated app could not reopen: {}", error.code);
                }
            }
        }
        Ok(completed())
    }
    /// A local app's bundle path for a rollback; protected apps are refused.
    fn rollback_app(&self, id: &str) -> Result<PathBuf, AppError> {
        let record = self.uninstall_engine.app(id)?;
        if record.app.protected_reason.is_some() {
            return Err(AppError::new("E_PROTECTED", "protected_update_target"));
        }
        Ok(PathBuf::from(record.app.path))
    }
    pub(crate) fn rollback_plan(&self, history_id: &str) -> Result<rollback::Plan, AppError> {
        rollback::plan(&self.database, history_id, &|id| self.rollback_app(id))
    }
    /// History ids among `ids` whose update can be rolled back now.
    pub fn rollback_targets(&self, ids: &[String]) -> Result<Vec<String>, AppError> {
        rollback::available(&self.database, ids, &|id| self.rollback_app(id))
    }
    /// The Updates history's one-click rollback. Target and source come from the
    /// recorded update; a running app is asked to quit normally and reopened.
    pub fn enqueue_rollback(&self, history_id: &str) -> Result<String, AppError> {
        let plan = self.rollback_plan(history_id)?;
        let task = self.enqueue(
            TaskOp::Rollback,
            Some(TaskTarget {
                kind: TaskKind::App,
                token: plan.app_id,
            }),
            TaskOptions {
                source: Some(plan.source),
                rollback_of: Some(plan.history_id),
                quit_running: true,
                reopen: true,
                ..Default::default()
            },
            TaskTrigger::Manual,
        )?;
        // The queue returns an existing task for the same app instead of a second one.
        if task.op != TaskOp::Rollback {
            return Err(AppError::new("E_ROLLBACK_UNAVAILABLE", "task_active"));
        }
        Ok(task.id)
    }
    pub(super) async fn execute_app_rollback(
        self: &Arc<Self>,
        task: &Task,
        cancel: Arc<AtomicBool>,
        notify: Arc<Notify>,
    ) -> Result<crate::brew::pty::Outcome, AppError> {
        let _operation = self.read_gate.write().await;
        let history = task
            .options
            .rollback_of
            .as_deref()
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "rollback_options"))?;
        let plan = self.rollback_plan(history)?;
        if task.target.as_ref().is_none_or(|t| t.token != plan.app_id) {
            return Err(AppError::new("E_INVALID_ARG", "rollback_options"));
        }
        let step = |phase, index| Progress {
            phase: Some(phase),
            percent: None,
            step_index: Some(index),
            from: Some(plan.current_version.clone()),
            to: Some(plan.restored_version.clone()),
            bytes: None,
            total: None,
            speed: None,
        };
        self.progress(&task.id, &[], &step(TaskPhase::Preparing, 1), true)?;
        let identity = Arc::new(crate::running_apps::Identity {
            paths: vec![plan.installed.clone()],
            bundle_ids: vec![],
            ..Default::default()
        });
        let reopen = crate::running_apps::prepare(
            identity,
            self.app_control.clone(),
            task.options.quit_running
                && !matches!(task.trigger, TaskTrigger::Schedule | TaskTrigger::Bundle),
            cancel.clone(),
            notify,
            Duration::from_secs(15),
        )
        .await
        .map_err(|mut error| {
            if error.code == "E_APP_QUIT_FAILED" {
                error.code = "E_APP_RUNNING".into();
            }
            error
        })?;
        if cancel.load(Ordering::Acquire) {
            return Err(AppError::new("E_INTERRUPTED", "rollback_canceled"));
        }
        self.progress(&task.id, &[], &step(TaskPhase::Installing, 2), true)?;
        let (os, arch) = service::system()?;
        let applied = plan.clone();
        crate::blocking(move || {
            rollback::apply(
                &applied,
                &gate::NativeInspector,
                &swap::NativeFiles,
                &os,
                &arch,
            )
        })
        .await?;
        // The bundle is already restored; bookkeeping errors must not report failure.
        if let Err(error) = rollback::finish(&self.database, &plan) {
            log::warn!("Rollback record could not be settled: {}", error.code);
        }
        if let Err(error) = self.updater.changed() {
            log::warn!("Update list refresh deferred: {}", error.code);
        }
        if task.options.reopen {
            for path in reopen {
                if let Err(error) = self.app_control.reopen(&path) {
                    log::warn!("Restored app could not reopen: {}", error.code);
                }
            }
        }
        Ok(completed())
    }
    async fn run_native_admin(
        &self,
        requests: Vec<admin::Request>,
    ) -> Result<Vec<admin::ResultItem>, AppError> {
        let fake = self.native_runtime.lock().map_err(poison)?.admin.clone();
        let engine = self.uninstall_engine.clone();
        crate::blocking(move || {
            if let Some(fake) = fake {
                fake.run(&requests)
            } else {
                crate::uninstall::execute::admin_batch_native(&engine, &requests)
            }
        })
        .await
    }
    pub(super) fn mas_batch(&self, first: &Task) -> Result<Vec<Task>, AppError> {
        let group = self.database.meta(&format!("mas_batch:{}", first.id))?;
        let Some(group) = group else {
            return Ok(vec![first.clone()]);
        };
        let mut tasks = self.tasks.lock().map_err(poison)?;
        let mut out = vec![first.clone()];
        for task in tasks.iter_mut().filter(|t| {
            t.state == TaskState::Queued
                && t.target.as_ref().is_some_and(|t| t.kind == TaskKind::Mas)
        }) {
            if self
                .database
                .meta(&format!("mas_batch:{}", task.id))?
                .as_deref()
                != Some(&group)
            {
                continue;
            }
            task.state = TaskState::Running;
            task.phase = Some(TaskPhase::Preparing);
            task.started_at = first.started_at;
            self.controls.lock().map_err(poison)?.insert(
                task.id.clone(),
                Control {
                    cancel: Arc::new(AtomicBool::new(false)),
                    notify: Arc::new(Notify::new()),
                },
            );
            self.database.save_task(task)?;
            (self.sink)(CoreEvent::Task(Box::new(task.clone())));
            out.push(task.clone());
        }
        Ok(out)
    }
    pub(super) async fn execute_mas_batch(
        self: &Arc<Self>,
        tasks: &[Task],
    ) -> Vec<Result<crate::brew::pty::Outcome, AppError>> {
        match self.download_mas_batch(tasks).await {
            Ok(results) => results,
            Err(error) => tasks.iter().map(|_| Err(error.clone())).collect(),
        }
    }
    async fn download_mas_batch(
        self: &Arc<Self>,
        tasks: &[Task],
    ) -> Result<Vec<Result<crate::brew::pty::Outcome, AppError>>, AppError> {
        let _operation = self.read_gate.write().await;
        let ids: Vec<u64> = tasks
            .iter()
            .map(|t| {
                t.target
                    .as_ref()
                    .and_then(|t| t.token.parse().ok())
                    .ok_or_else(|| AppError::new("E_INVALID_ARG", "adam_id"))
            })
            .collect::<Result<_, _>>()?;
        let products = self.appstore.lookup(ids.clone()).await?;
        for (task, id) in tasks.iter().zip(&ids) {
            let product = products
                .iter()
                .find(|a| a.adam_id == *id)
                .ok_or_else(|| AppError::new("E_MAS_REGION", "app_unavailable_in_region"))?;
            if task.op == TaskOp::Install && f64::from(product.price) > 0.0 {
                return Err(AppError::new("E_MAS_PAID", "paid_app_requires_store"));
            }
        }
        let mut reopen = BTreeMap::new();
        for (task, product) in tasks.iter().zip(ids.iter().map(|id| {
            products
                .iter()
                .find(|p| p.adam_id == *id)
                .expect("validated product")
        })) {
            let (cancel, notify) = self
                .controls
                .lock()
                .map_err(poison)?
                .get(&task.id)
                .map(|c| (c.cancel.clone(), c.notify.clone()))
                .ok_or_else(|| AppError::new("E_INTERRUPTED", "mas_task_canceled"))?;
            let paths = crate::running_apps::prepare(
                Arc::new(crate::running_apps::Identity {
                    bundle_ids: vec![product.bundle_id.clone()],
                    ..Default::default()
                }),
                self.app_control.clone(),
                task.options.quit_running && task.trigger == TaskTrigger::Manual,
                cancel,
                notify,
                Duration::from_secs(15),
            )
            .await?;
            if task.options.reopen {
                reopen.insert(task.id.clone(), paths);
            }
        }
        let runner = self
            .native_runtime
            .lock()
            .map_err(poison)?
            .store
            .clone()
            .map(Ok)
            .unwrap_or_else(Runner::native)?;
        let stage = self.stage()?;
        let canceled = Arc::new(AtomicBool::new(false));
        let monitor_cancel = canceled.clone();
        let queue = self.clone();
        let task_ids: Vec<_> = tasks.iter().map(|t| t.id.clone()).collect();
        let monitor = tokio::spawn(async move {
            loop {
                if queue.controls.lock().is_ok_and(|controls| {
                    task_ids.iter().any(|id| {
                        controls
                            .get(id)
                            .is_none_or(|c| c.cancel.load(Ordering::Acquire))
                    })
                }) {
                    monitor_cancel.store(true, Ordering::Release);
                    break;
                }
                tokio::time::sleep(Duration::from_millis(40)).await;
            }
        });
        let queue = self.clone();
        let mapping: BTreeMap<_, _> = tasks
            .iter()
            .zip(&ids)
            .map(|(t, id)| (*id, t.id.clone()))
            .collect();
        let result = runner
            .download(
                tasks[0].op,
                &ids,
                &stage.path,
                canceled.clone(),
                Arc::new(move |id, phase, percent| {
                    queue.progress(
                        &mapping[&id],
                        &[],
                        &crate::appstore::progress::stage(phase, percent)?,
                        true,
                    )
                }),
            )
            .await;
        monitor.abort();
        let results = result?;
        let mut requests = vec![];
        let mut outcomes = vec![];
        for (task, result) in tasks.iter().zip(results) {
            if let Some(error) = result.error {
                if error.code == "E_INTERRUPTED"
                    && let Some(control) = self.controls.lock().map_err(poison)?.get(&task.id)
                {
                    control.cancel.store(true, Ordering::Release);
                }
                outcomes.push(Err(error));
                continue;
            }
            if let Some(mut request) = result.privileged {
                request.bundle_id = products
                    .iter()
                    .find(|p| p.adam_id == request.adam_id)
                    .map(|p| p.bundle_id.clone());
                requests.push(admin::Request {
                    id: task.id.clone(),
                    action: admin::Action::MasInstallPkg {
                        request: Box::new(request),
                    },
                });
            }
            outcomes.push(Ok(completed()));
        }
        if !requests.is_empty() {
            let batch_canceled = self.controls.lock().map_err(poison).map(|controls| {
                tasks.iter().any(|t| {
                    controls
                        .get(&t.id)
                        .is_none_or(|c| c.cancel.load(Ordering::Acquire))
                })
            })?;
            let results = if batch_canceled {
                for request in &requests {
                    if let Some(control) = self.controls.lock().map_err(poison)?.get(&request.id) {
                        control.cancel.store(true, Ordering::Release);
                    }
                }
                Err(AppError::new("E_INTERRUPTED", "mas_batch_canceled"))
            } else {
                self.run_native_admin(requests.clone()).await
            };
            for (task, outcome) in tasks
                .iter()
                .zip(&mut outcomes)
                .filter(|(t, _)| requests.iter().any(|r| r.id == t.id))
            {
                *outcome = match &results {
                    Err(error) => Err(error.clone()),
                    Ok(results) => results
                        .iter()
                        .find(|r| r.id == task.id)
                        .map(|r| r.error.clone().map_or_else(|| Ok(completed()), Err))
                        .unwrap_or_else(|| {
                            Err(AppError::new("E_MAS_ENGINE", "admin_result_missing"))
                        }),
                };
            }
        }
        for (task, outcome) in tasks.iter().zip(&outcomes) {
            if outcome.is_ok() {
                if let Some(paths) = reopen.get(&task.id) {
                    for path in paths {
                        if let Err(error) = self.app_control.reopen(path) {
                            log::warn!("Store app reopen deferred: {}", error.code);
                        }
                    }
                }
                if let Err(error) = self.progress(
                    &task.id,
                    &[],
                    &crate::appstore::progress::stage("done", Some(100.0))?,
                    true,
                ) {
                    log::warn!("Store task progress persistence deferred: {}", error.code);
                }
            }
        }
        Ok(outcomes)
    }
}

#[cfg(test)]
mod m4_tests {
    use super::*;
    #[tokio::test]
    async fn running_electron_refuses_before_staging_or_any_network_request() {
        use crate::uninstall::{
            Engine, EngineConfig,
            identity::{Identity, MetadataProbe, Receipt},
            rules::Roots,
        };
        use std::path::Path;
        struct Probe;
        impl MetadataProbe for Probe {
            fn identity(&self, _: &Path) -> Identity {
                Identity {
                    name: Some("Fixture".into()),
                    bundle_id: Some("test.app".into()),
                    version: Some("1.0.0".into()),
                    ..Default::default()
                }
            }
            fn receipts(&self, _: &Path) -> Vec<Receipt> {
                vec![]
            }
        }
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let path = root.join("Applications/Fixture.app");
        std::fs::create_dir_all(path.join("Contents/Resources")).unwrap();
        std::fs::write(path.join("Contents/Info.plist"), r#"<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>test.app</string></dict></plist>"#).unwrap();
        std::fs::write(
            path.join("Contents/Resources/app-update.yml"),
            "provider: generic\nurl: https://fixture.example/releases/\n",
        )
        .unwrap();
        let db = crate::db::Database::open(&root.join("db")).unwrap();
        let settings = Arc::new(RwLock::new(Settings::default()));
        let control = Arc::new(crate::running_apps::tests::FakeApps::default());
        control.refuse.store(true, Ordering::Release);
        control
            .processes
            .lock()
            .unwrap()
            .push(crate::running_apps::Process {
                pid: 42,
                path: path.clone(),
                bundle_id: "test.app".into(),
            });
        let engine = Engine::new(EngineConfig {
            database: db.clone(),
            settings: settings.clone(),
            sink: Arc::new(|_| {}),
            roots: Roots {
                home: root.clone(),
                applications: vec![root.join("Applications")],
                system_library: root.join("Library"),
                bins: vec![],
                receipts: root.join("receipts"),
                temporary: vec![],
                caskrooms: vec![],
            },
            control: control.clone(),
            probe: Arc::new(Probe),
            icon_cache: root.join("icons"),
            askpass: root.join("askpass"),
        });
        let app = engine.list().unwrap().remove(0);
        assert_eq!(app.updater, Some(UpdateSource::Electron));
        let item:OutdatedItem=serde_json::from_value(serde_json::json!({"kind":"cask","token":"","name":"Fixture","source":"electron","appId":app.id,"installedVersion":"1","currentVersion":"2","autoUpdates":true,"pinned":false,"ignored":false,"dependents":0,"major":false,"requiresAdmin":false,"running":true,"relaunchPending":false,"criticalUpdate":false})).unwrap();
        // There is deliberately no downloadable artifact; reaching download would return E_UPDATE_INTEGRITY.
        let candidate = service::Candidate {
            item,
            app_path: path.clone(),
            snapshot: crate::uninstall::rules::Snapshot::capture(&path).unwrap(),
            feed_item: None,
            artifact: None,
            public_key: None,
            expected_team: None,
            checked_at: 0,
        };
        db.set_meta(
            "native_update_candidates",
            &serde_json::to_string(&vec![candidate]).unwrap(),
        )
        .unwrap();
        let mut queue = TaskQueue::new(
            db.clone(),
            settings.clone(),
            root.join("logs"),
            root.join("askpass"),
            Arc::new(|_| {}),
        )
        .unwrap();
        let state = Arc::get_mut(&mut queue).unwrap();
        state.app_control = control.clone();
        state.updater = Arc::new(
            service::Service::new(db, engine.clone(), settings, Arc::new(|_| {})).unwrap(),
        );
        state.uninstall_engine = engine;
        for allow in [false, true] {
            let task:Task=serde_json::from_value(serde_json::json!({"id":"test","op":"upgrade","target":{"kind":"app","token":app.id},"options":{"source":"electron","quitRunning":allow},"trigger":"manual","state":"running","createdAt":0})).unwrap();
            let error = queue
                .execute_app_update(
                    &task,
                    Arc::new(AtomicBool::new(false)),
                    Arc::new(Notify::new()),
                )
                .await
                .err()
                .unwrap();
            assert_eq!(error.code, "E_APP_RUNNING");
            assert!(
                !queue
                    .uninstall_engine
                    .admin_root()
                    .with_file_name(NATIVE_STAGING)
                    .exists()
            );
        }
        assert_eq!(control.quits.lock().unwrap().as_slice(), &[42]);
    }
}
