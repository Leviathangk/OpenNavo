mod history;
mod native;
use crate::{
    AppError,
    brew::{
        args::{write_args, write_args_in_tap},
        env::{Ask, Reason, construct, explain},
        locate,
        pty::{Execution, Progress, execute},
    },
    db::Database,
    events::{CoreEvent, EventSink},
    model::*,
};
pub(crate) use native::NATIVE_STAGING;
use std::{
    collections::{BTreeMap, HashMap},
    future::Future,
    path::PathBuf,
    pin::Pin,
    sync::{
        Arc, Mutex, RwLock,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};
use tokio::sync::Notify;

type Completion = Arc<dyn Fn(Task) -> Pin<Box<dyn Future<Output = ()> + Send>> + Send + Sync>;

/// The brew operations whose sudo prompt gets a sentence of its own; the rest keep the neutral one.
fn password_reason(op: TaskOp) -> Option<Reason> {
    match op {
        TaskOp::Install | TaskOp::Reinstall => Some(Reason::Install),
        TaskOp::Upgrade => Some(Reason::Update),
        TaskOp::Uninstall => Some(Reason::Uninstall),
        TaskOp::InstallHomebrew => Some(Reason::Homebrew),
        _ => None,
    }
}
struct Control {
    cancel: Arc<AtomicBool>,
    notify: Arc<Notify>,
}
pub struct TaskQueue {
    enqueue_gate: Mutex<()>,
    native_runtime: Mutex<native::Runtime>,
    pub appstore: Arc<crate::appstore::Service>,
    pub updater: Arc<crate::updater::Service>,
    pub security: Arc<crate::security::Service>,
    pub database: Database,
    pub cleanup_engine: Arc<crate::cleanup::Engine>,
    pub uninstall_engine: Arc<crate::uninstall::Engine>,
    pub app_control: Arc<dyn crate::running_apps::AppControl>,
    pub settings: Arc<RwLock<Settings>>,
    tasks: Mutex<Vec<Task>>,
    controls: Mutex<HashMap<String, Control>>,
    logs: PathBuf,
    pub(crate) askpass: PathBuf,
    sink: EventSink,
    wake: Notify,
    paused: AtomicBool,
    stopping: AtomicBool,
    started: AtomicBool,
    pub read_gate: Arc<tokio::sync::RwLock<()>>,
    completion: Mutex<Option<Completion>>,
    retry_delay: Duration,
    extra_env: Mutex<BTreeMap<String, String>>,
    installer_url: Mutex<Option<String>>,
}
impl TaskQueue {
    pub fn new(
        database: Database,
        settings: Arc<RwLock<Settings>>,
        logs: PathBuf,
        askpass: PathBuf,
        sink: EventSink,
    ) -> Result<Arc<Self>, AppError> {
        let tasks = database.recover_tasks(chrono::Utc::now().timestamp_millis())?;
        let paused = !tasks.is_empty();
        let uninstall_engine = crate::uninstall::Engine::new(crate::uninstall::EngineConfig {
            database: database.clone(),
            settings: settings.clone(),
            sink: sink.clone(),
            roots: crate::uninstall::rules::Roots::native(),
            control: Arc::new(crate::running_apps::NativeApps),
            probe: Arc::new(crate::uninstall::identity::NativeProbe),
            icon_cache: logs.parent().unwrap_or(&logs).join("local-app-icons"),
            askpass: askpass.clone(),
        });
        let security_data = uninstall_engine.admin_root().with_file_name("security");
        let security = crate::security::Service::new(
            database.clone(),
            settings.clone(),
            sink.clone(),
            uninstall_engine.roots.home.clone(),
            security_data.clone(),
            crate::security::av::Config::native(&security_data),
            Arc::new(crate::security::NativeCommands),
        );
        *security.admin.write().map_err(poison)? = Some(Arc::new(
            crate::security::admin::NativeActions(uninstall_engine.clone()),
        ));
        Ok(Arc::new(Self {
            security,
            enqueue_gate: Mutex::new(()),
            native_runtime: Mutex::new(native::Runtime::default()),
            appstore: Arc::new(crate::appstore::Service::new(
                database.clone(),
                settings.clone(),
                sink.clone(),
            )?),
            updater: Arc::new(crate::updater::Service::new(
                database.clone(),
                uninstall_engine.clone(),
                settings.clone(),
                sink.clone(),
            )?),
            cleanup_engine: crate::cleanup::Engine::new(uninstall_engine.clone()),
            uninstall_engine,
            database,
            app_control: Arc::new(crate::running_apps::NativeApps),
            settings,
            tasks: Mutex::new(tasks),
            controls: Mutex::new(HashMap::new()),
            logs,
            askpass,
            sink,
            wake: Notify::new(),
            paused: AtomicBool::new(paused),
            stopping: AtomicBool::new(false),
            started: AtomicBool::new(false),
            read_gate: Arc::new(tokio::sync::RwLock::new(())),
            completion: Mutex::new(None),
            retry_delay: Duration::from_secs(30),
            extra_env: Mutex::new(BTreeMap::new()),
            installer_url: Mutex::new(None),
        }))
    }
    pub fn set_completion(&self, hook: Completion) -> Result<(), AppError> {
        *self.completion.lock().map_err(poison)? = Some(hook);
        Ok(())
    }
    pub fn start(self: &Arc<Self>) {
        if self.started.swap(true, Ordering::AcqRel) {
            return;
        }
        let queue = self.clone();
        tauri::async_runtime::spawn(async move {
            queue.run().await;
        });
    }
    pub fn settings(&self) -> Result<Settings, AppError> {
        Ok(self.settings.read().map_err(poison)?.clone())
    }
    pub fn is_stopping(&self) -> bool {
        self.stopping.load(Ordering::Acquire)
    }
    pub fn is_paused(&self) -> bool {
        self.paused.load(Ordering::Acquire)
    }
    pub fn enqueue(
        &self,
        op: TaskOp,
        target: Option<TaskTarget>,
        options: TaskOptions,
        trigger: TaskTrigger,
    ) -> Result<Task, AppError> {
        let _gate = self.enqueue_gate.lock().map_err(poison)?;
        self.enqueue_unlocked(op, target, options, trigger)
    }
    fn enqueue_unlocked(
        &self,
        op: TaskOp,
        target: Option<TaskTarget>,
        options: TaskOptions,
        trigger: TaskTrigger,
    ) -> Result<Task, AppError> {
        // Reject before stopping-state checks, validation, deduplication or any DB
        // write. A link is never a queued task, including with uninstall plans.
        if let Some(target) = target.as_ref() {
            crate::catalog::links::reject_write(&self.database, target)?;
        }
        if self.is_stopping() {
            return Err(AppError::new("E_INTERRUPTED", "queue_stopped"));
        }
        if options.rollback_of.is_some() != (op == TaskOp::Rollback)
            || (op == TaskOp::Rollback && target.as_ref().is_none_or(|t| t.kind != TaskKind::App))
        {
            return Err(AppError::new("E_INVALID_ARG", "rollback_options"));
        }
        let native = target
            .as_ref()
            .map(|t| self.validate_native(op, t, &options))
            .transpose()?
            .unwrap_or(false);
        let planned = if op == TaskOp::AvDbUpdate {
            if target.is_some() || options != TaskOptions::default() {
                return Err(AppError::new("E_INVALID_ARG", "av_update_options"));
            }
            self.security.av.check_update()?;
            None
        } else if native {
            None
        } else if op == TaskOp::Clean {
            self.cleanup_engine
                .validate_task(target.as_ref(), &options)?;
            None
        } else if options.plan_id.is_some() {
            Some(
                self.uninstall_engine
                    .task_plan(op, target.as_ref(), &options)?,
            )
        } else {
            write_args(op, target.as_ref(), &options)?;
            None
        };

        if options.adopt && trigger == TaskTrigger::Schedule {
            return Err(AppError::new("E_INVALID_ARG", "unattended_adopt"));
        }
        if matches!(trigger, TaskTrigger::Schedule | TaskTrigger::Bundle)
            && (options.quit_running || options.reopen)
        {
            return Err(AppError::new("E_INVALID_ARG", "unattended_quit"));
        }
        let mut tasks = self.tasks.lock().map_err(poison)?;
        if self.is_stopping() {
            return Err(AppError::new("E_INTERRUPTED", "queue_stopped"));
        }
        if matches!(
            trigger,
            TaskTrigger::Manual | TaskTrigger::Deeplink | TaskTrigger::Retry
        ) {
            self.paused.store(false, Ordering::Release);
        }
        if let Some(existing) = tasks.iter().find(|t| {
            matches!(t.state, TaskState::Queued | TaskState::Running)
                && if target.is_some() {
                    t.target == target
                } else {
                    t.target.is_none() && t.op == op
                }
        }) {
            let task = existing.clone();
            drop(tasks);
            self.wake.notify_one();
            return Ok(task);
        }
        let now = chrono::Utc::now().timestamp_millis();
        let id = uuid::Uuid::now_v7().to_string();
        let task = Task {
            target_name: if op == TaskOp::Clean {
                Some("cleanup".into())
            } else {
                planned
                    .as_ref()
                    .filter(|_| target.as_ref().is_some_and(|t| t.kind == TaskKind::App))
                    .map(|(_, a)| a.name.clone())
            },
            outcome: None,
            id: id.clone(),
            op,
            target,
            options,
            trigger,
            state: TaskState::Queued,
            phase: None,
            percent: None,
            bytes_done: None,
            bytes_total: None,
            speed_bps: None,
            step_index: Some(0),
            step_count: match op {
                TaskOp::Uninstall | TaskOp::RemoveLeftovers | TaskOp::Clean => Some(4),
                TaskOp::Install | TaskOp::Upgrade | TaskOp::Reinstall => Some(4),
                TaskOp::Rollback => Some(2),
                _ => None,
            },
            from_version: None,
            to_version: None,
            error: None,
            exit_code: None,
            log_path: Some(self.logs.join(format!("{id}.log")).to_string_lossy().into()),
            created_at: now,
            started_at: None,
            finished_at: None,
        };
        if trigger == TaskTrigger::Retry
            && let Some(parent) = self
                .database
                .tasks()?
                .into_iter()
                .rev()
                .find(|t| t.target == task.target && t.op == op && t.state == TaskState::Failed)
        {
            self.database
                .set_meta(&format!("task_retry_parent:{id}"), &parent.id)?;
        }
        if let Some((entry, app)) = &planned {
            self.uninstall_engine
                .reserve(&entry.plan.plan_id, &app.app_id)?;
        }
        if let Err(error) = self.database.save_task(&task) {
            if let Some((entry, app)) = &planned {
                self.uninstall_engine
                    .release(&entry.plan.plan_id, &app.app_id);
            }
            return Err(error);
        }
        tasks.push(task.clone());
        drop(tasks);
        (self.sink)(CoreEvent::Task(Box::new(task.clone())));
        self.wake.notify_one();
        Ok(task)
    }
    pub fn enqueue_many(
        &self,
        op: TaskOp,
        targets: Vec<TaskTarget>,
        options: TaskOptions,
        trigger: TaskTrigger,
    ) -> Result<Vec<Task>, AppError> {
        if targets.len() > 200 || targets.iter().filter(|t| t.kind == TaskKind::Mas).count() > 100 {
            return Err(AppError::new("E_INVALID_ARG", "too_many_targets"));
        }
        // A rollback names one recorded update; it is never a batch.
        if op == TaskOp::Rollback {
            return Err(AppError::new("E_INVALID_ARG", "rollback_options"));
        }
        let _gate = self.enqueue_gate.lock().map_err(poison)?;
        for target in &targets {
            crate::catalog::links::reject_write(&self.database, target)?;
            if !self.validate_native(op, target, &options)? {
                write_args(op, Some(target), &options)?;
            }
        }
        let group = uuid::Uuid::now_v7().to_string();
        let tasks = targets
            .into_iter()
            .map(|target| self.enqueue_unlocked(op, Some(target), options.clone(), trigger))
            .collect::<Result<Vec<_>, _>>()?;
        for task in &tasks {
            if task
                .target
                .as_ref()
                .is_some_and(|t| t.kind == TaskKind::Mas)
            {
                self.database
                    .set_meta(&format!("mas_batch:{}", task.id), &group)?;
            }
        }
        Ok(tasks)
    }

    pub fn active(&self) -> Result<Vec<Task>, AppError> {
        Ok(self
            .tasks
            .lock()
            .map_err(poison)?
            .iter()
            .filter(|t| matches!(t.state, TaskState::Queued | TaskState::Running))
            .cloned()
            .collect())
    }
    pub fn cancel(&self, id: &str) -> Result<(), AppError> {
        let mut tasks = self.tasks.lock().map_err(poison)?;
        let task = tasks
            .iter_mut()
            .find(|t| t.id == id)
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "unknown_task"))?;
        if task.state == TaskState::Queued {
            task.state = TaskState::Canceled;
            task.finished_at = Some(chrono::Utc::now().timestamp_millis());
            self.database.save_task(task)?;
            let task = task.clone();
            tasks.retain(|item| item.id != id);
            drop(tasks);
            (self.sink)(CoreEvent::Task(Box::new(task)));
        } else if task.state == TaskState::Running
            && let Some(control) = self.controls.lock().map_err(poison)?.get(id)
        {
            control.cancel.store(true, Ordering::Release);
            control.notify.notify_one();
        }
        Ok(())
    }
    pub async fn wait_terminal(&self, id: &str) -> Result<Task, AppError> {
        loop {
            let task = self
                .tasks
                .lock()
                .map_err(poison)?
                .iter()
                .find(|t| t.id == id)
                .cloned();
            // Terminal tasks live in SQLite, not the active queue. Drop the queue
            // lock before reading the database, preserving the enqueue lock order.
            let task = match task {
                Some(task) => task,
                None => self
                    .database
                    .task(id)?
                    .ok_or_else(|| AppError::new("E_NOT_FOUND", "task_id"))?,
            };
            if !matches!(task.state, TaskState::Queued | TaskState::Running) {
                return Ok(task);
            }
            if self.stopping.load(Ordering::Acquire) {
                return Err(AppError::new("E_INTERRUPTED", "queue_stopped"));
            }
            tokio::time::sleep(Duration::from_millis(100)).await;
        }
    }
    pub fn shutdown(&self) {
        // Share the next/enqueue lock so selected tasks already have cancellation handles and no child starts after quit.
        let _tasks = self.tasks.lock().ok();
        self.stopping.store(true, Ordering::Release);
        if let Ok(controls) = self.controls.lock() {
            for control in controls.values() {
                control.cancel.store(true, Ordering::Release);
                control.notify.notify_one();
            }
        }
        self.wake.notify_one();
        self.security.shutdown();
    }
    pub fn prepare_exit(&self) -> Result<QuitRequested, AppError> {
        let tasks = self.tasks.lock().map_err(poison)?;
        let counts = crate::lifecycle::counts(&tasks);
        if counts.running == 0 && counts.queued == 0 {
            // Atomically reject new enqueues on idle quit so a task cannot start after the count check.
            self.stopping.store(true, Ordering::Release);
            self.wake.notify_one();
        }
        Ok(counts)
    }
    pub fn log(&self, id: &str, tail: Option<u32>) -> Result<String, AppError> {
        if tail.is_some_and(|n| n > 10_000) {
            return Err(AppError::new("E_INVALID_ARG", "log_tail"));
        }
        let task = self
            .database
            .task(id)?
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "unknown_task"))?;
        let Some(path) = task.log_path else {
            return Ok(String::new());
        };
        let content = match std::fs::read_to_string(path) {
            Ok(s) => s,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => String::new(),
            Err(e) => return Err(e.into()),
        };
        Ok(if let Some(tail) = tail {
            content
                .lines()
                .skip(content.lines().count().saturating_sub(tail as usize))
                .collect::<Vec<_>>()
                .join("\n")
        } else {
            content
        })
    }
    fn next(&self) -> Result<Option<Task>, AppError> {
        let _gate = self.enqueue_gate.lock().map_err(poison)?;
        if self.paused.load(Ordering::Acquire) {
            return Ok(None);
        }
        let mut tasks = self.tasks.lock().map_err(poison)?;
        if self.is_stopping() {
            return Ok(None);
        }
        let selected = tasks
            .iter()
            .enumerate()
            .filter(|(_, t)| t.state == TaskState::Queued)
            .min_by_key(|(_, t)| {
                (
                    if matches!(t.trigger, TaskTrigger::Schedule | TaskTrigger::Bundle) {
                        1
                    } else {
                        0
                    },
                    t.created_at,
                    t.id.clone(),
                )
            })
            .map(|(index, _)| index);
        let Some(index) = selected else {
            return Ok(None);
        };
        let task = &mut tasks[index];
        if let Some(target) = &task.target {
            let cached = self
                .database
                .meta("installed_cache")?
                .and_then(|s| serde_json::from_str::<Vec<InstalledItem>>(&s).ok())
                .unwrap_or_default();
            let installed = cached
                .iter()
                .find(|i| i.kind == target.kind && i.token == target.token);
            task.from_version = installed.map(|i| {
                i.actual_version
                    .clone()
                    .unwrap_or_else(|| i.installed_version.clone())
            });
            task.to_version = if matches!(task.op, TaskOp::Pin | TaskOp::Unpin) {
                task.from_version.clone()
            } else if matches!(
                task.op,
                TaskOp::Uninstall | TaskOp::RemoveLeftovers | TaskOp::Clean
            ) {
                None
            } else {
                match target.kind {
                    TaskKind::Cask | TaskKind::Formula => crate::catalog::get(
                        &self.database,
                        target.kind.brew_kind()?,
                        &target.token,
                    )?
                    .map(|i| i.version),
                    _ => None,
                }
            };
        }
        task.state = TaskState::Running;
        task.phase = Some(TaskPhase::Preparing);
        task.started_at = Some(chrono::Utc::now().timestamp_millis());
        self.controls.lock().map_err(poison)?.insert(
            task.id.clone(),
            Control {
                cancel: Arc::new(AtomicBool::new(false)),
                notify: Arc::new(Notify::new()),
            },
        );
        self.database.save_task(task)?;
        (self.sink)(CoreEvent::Task(Box::new(task.clone())));
        Ok(Some(task.clone()))
    }
    fn progress(
        &self,
        id: &str,
        lines: &[String],
        progress: &Progress,
        emit: bool,
    ) -> Result<(), AppError> {
        if !lines.is_empty() {
            (self.sink)(CoreEvent::Log(TaskLogEvent {
                id: id.into(),
                lines: lines.to_vec(),
            }));
        }
        let mut tasks = self.tasks.lock().map_err(poison)?;
        if let Some(task) = tasks.iter_mut().find(|t| t.id == id) {
            if let Some(phase) = progress.phase {
                task.phase = Some(phase);
            }
            task.percent = progress.percent;
            task.step_index = progress.step_index.or(task.step_index);
            task.bytes_done = progress.bytes;
            task.bytes_total = progress.total;
            task.speed_bps = progress.speed;
            if progress.from.is_some() {
                task.from_version = progress.from.clone();
            }
            if progress.to.is_some() {
                task.to_version = progress.to.clone();
            }
            if emit {
                (self.sink)(CoreEvent::Task(Box::new(task.clone())));
            }
        }
        Ok(())
    }
    /// What the password dialog calls a package: the catalog's name for it, else its token. Only decoration, so a
    /// failed lookup never fails the task.
    async fn dialog_name(&self, target: Option<&TaskTarget>, locale: Locale) -> Option<String> {
        let target = target?;
        let kind = target.kind.brew_kind().ok()?;
        let database = self.database.clone();
        let token = target.token.clone();
        let item = crate::blocking({
            let token = token.clone();
            move || crate::catalog::get(&database, kind, &token)
        })
        .await
        .ok()?;
        Some(crate::library::display_name(item.as_ref(), &token, locale))
    }
    async fn execute_task(
        self: &Arc<Self>,
        task: &Task,
    ) -> Result<crate::brew::pty::Outcome, AppError> {
        // The catalog may have changed while an otherwise valid task was waiting.
        if let Some(target) = &task.target {
            crate::catalog::links::reject_write(&self.database, target)?;
        }
        let settings = self.settings()?;
        let (cancel, notify) = {
            let controls = self.controls.lock().map_err(poison)?;
            let control = controls
                .get(&task.id)
                .ok_or_else(|| AppError::new("E_UNKNOWN", "missing_task_control"))?;
            (control.cancel.clone(), control.notify.clone())
        };
        if task
            .target
            .as_ref()
            .is_some_and(|t| t.kind == TaskKind::App)
            && task.op == TaskOp::Upgrade
        {
            return self.execute_app_update(task, cancel, notify).await;
        }
        if task.op == TaskOp::Rollback {
            return self.execute_app_rollback(task, cancel, notify).await;
        }
        if task.op == TaskOp::AvDbUpdate {
            let engine = self.security.av.clone();
            return crate::blocking(move || {
                engine.update(&cancel)?;
                Ok(crate::brew::pty::Outcome {
                    code: 0,
                    canceled: false,
                    #[cfg(test)]
                    signal: None,
                    parser: Default::default(),
                })
            })
            .await;
        }
        if task.op == TaskOp::Clean {
            return crate::cleanup::execute::run(self.clone(), task, cancel, notify).await;
        }
        if task.options.plan_id.is_some() {
            return crate::uninstall::execute::run(self.clone(), task, cancel, notify).await;
        }
        let mut prepared = None;
        let (executable, args, prefix, cache) = if task.op == TaskOp::InstallHomebrew {
            let sources = self
                .installer_url
                .lock()
                .map_err(poison)?
                .clone()
                .map(|url| vec![url])
                .unwrap_or_else(|| {
                    crate::installer::source(&settings)
                        .iter()
                        .map(|url| (*url).into())
                        .collect()
                });
            let directory = self.logs.join("installers");
            let artifact = crate::installer::download_candidates(
                &directory,
                &sources,
                cancel.clone(),
                notify.clone(),
                std::path::Path::new(
                    task.log_path
                        .as_deref()
                        .ok_or_else(|| AppError::new("E_UNKNOWN", "missing_task_log"))?,
                ),
                |line| {
                    (self.sink)(CoreEvent::Log(TaskLogEvent {
                        id: task.id.clone(),
                        lines: vec![line],
                    }))
                },
            )
            .await?;
            let args = vec![artifact.path.to_string_lossy().into()];
            prepared = Some(artifact);
            (
                PathBuf::from("/bin/bash"),
                args,
                if std::env::consts::ARCH == "aarch64" {
                    "/opt/homebrew"
                } else {
                    "/usr/local"
                }
                .to_owned(),
                None,
            )
        } else {
            let brew = locate::locate(&settings).await?;
            let cache = locate::read(&brew, &settings, crate::brew::args::ReadOp::Cache, None)
                .await
                .ok()
                .map(PathBuf::from);
            // Installs use the catalog; upgrades/reinstalls retain the installed source and reject catalog conflicts.
            let catalog_tap = match (&task.target, task.op) {
                (Some(target), TaskOp::Install | TaskOp::Reinstall | TaskOp::Upgrade)
                    if target.kind == Kind::Cask =>
                {
                    let database = self.database.clone();
                    let token = target.token.clone();
                    crate::blocking(move || {
                        Ok(crate::catalog::get(&database, Kind::Cask, &token)?.map(|i| i.tap))
                    })
                    .await?
                }
                _ => None,
            };
            let mut tap = catalog_tap.clone().flatten();
            if matches!(task.op, TaskOp::Upgrade | TaskOp::Reinstall)
                && let Some(target) = &task.target
                && target.kind == Kind::Cask
            {
                let json =
                    locate::read(&brew, &settings, crate::brew::args::ReadOp::Installed, None)
                        .await?;
                let installed: crate::brew::json::Info = serde_json::from_str(&json)?;
                tap = verify_installed_cask_tap(
                    &installed,
                    &target.token,
                    catalog_tap.as_ref().map(|tap| tap.as_deref()),
                )?;
            }
            (
                PathBuf::from(brew.path),
                write_args_in_tap(task.op, task.target.as_ref(), &task.options, tap.as_deref())?,
                brew.prefix,
                cache,
            )
        };
        if cancel.load(Ordering::Acquire) {
            return Err(AppError::new("E_INTERRUPTED", "task_canceled"));
        }
        let mut env = construct(&settings, &prefix, &self.askpass);
        if task.op == TaskOp::InstallHomebrew {
            env.insert("NONINTERACTIVE".into(), "1".into());
        }
        // sudo may ask for the password mid-task; the dialog should say what this task is doing.
        if let Some(reason) = password_reason(task.op) {
            let name = self
                .dialog_name(task.target.as_ref(), settings.locale)
                .await;
            explain(
                &mut env,
                settings.locale,
                Ask {
                    reason,
                    name: name.as_deref(),
                },
            );
        }
        env.extend(self.extra_env.lock().map_err(poison)?.clone());
        #[cfg(debug_assertions)]
        if std::env::var_os("OPENNAVO_BREW_PATH").is_some() {
            for key in [
                "FAKE_BREW_SCENARIO",
                "FAKE_BREW_PREFIX",
                "FAKE_BREW_CACHE",
                "FAKE_BREW_JSON",
            ] {
                if let Ok(value) = std::env::var(key) {
                    env.insert(key.into(), value);
                }
            }
        }
        let request = Execution {
            executable,
            args,
            env,
            log_path: PathBuf::from(
                task.log_path
                    .as_ref()
                    .ok_or_else(|| AppError::new("E_UNKNOWN", "missing_task_log"))?,
            ),
            op: task.op,
            cancel: cancel.clone(),
            grace: Duration::from_secs(5),
            cache_dir: cache,
            download_total: {
                let database = self.database.clone();
                let target = task.target.clone();
                crate::blocking(move || match target {
                    Some(target) => Ok(crate::catalog::get(
                        &database,
                        target.kind.brew_kind()?,
                        &target.token,
                    )?
                    .and_then(|i| i.download_size)),
                    None => Ok(None),
                })
                .await?
            },
        };
        let (request, install_scan) = {
            let security = self.security.clone();
            let queue = self.clone();
            let id = task.id.clone();
            crate::blocking(move || {
                let mut request = request;
                let prepared = security.prepare_install(&id, &mut request, |lines, progress| {
                    let _ = queue.progress(&id, lines, progress, true);
                })?;
                Ok((request, prepared))
            })
            .await?
        };
        let install_scan = install_scan.map(Arc::new);
        let identity = if task.op == TaskOp::Upgrade {
            let target = task
                .target
                .as_ref()
                .ok_or_else(|| AppError::new("E_INVALID_ARG", "target"))?;
            Some(Arc::new(
                crate::running_apps::identity(&self.database, &settings, target).await?,
            ))
        } else {
            None
        };
        let mut reopen = Vec::new();
        let mut attempts = 0;
        loop {
            if let Some(identity) = &identity {
                let paths = crate::running_apps::prepare(
                    identity.clone(),
                    self.app_control.clone(),
                    task.options.quit_running
                        && attempts == 0
                        && !matches!(task.trigger, TaskTrigger::Schedule | TaskTrigger::Bundle),
                    cancel.clone(),
                    notify.clone(),
                    Duration::from_secs(15),
                )
                .await?;
                reopen.extend(paths);
                // Reopened apps do not inherit quit consent; lock retries must recheck too.
                crate::running_apps::prepare(
                    identity.clone(),
                    self.app_control.clone(),
                    false,
                    cancel.clone(),
                    notify.clone(),
                    Duration::ZERO,
                )
                .await?;
            }
            if let Some(prepared) = &install_scan {
                let prepared = prepared.clone();
                let scan_cancel = cancel.clone();
                crate::blocking(move || prepared.revalidate(&scan_cancel)).await?;
            }
            let queue = self.clone();
            let id = task.id.clone();
            let mut last_emit = Instant::now();
            let request = request.clone();
            let scan_observer = install_scan.clone();
            let result = tokio::task::spawn_blocking(move || {
                let outcome = execute(request, |lines, progress| {
                    let emit = last_emit.elapsed() >= Duration::from_millis(200);
                    if emit {
                        last_emit = Instant::now();
                    }
                    let _ = queue.progress(&id, lines, progress, emit);
                    if scan_observer.is_some() {
                        let extra = crate::security::preinstall::Prepared::download_logs(lines);
                        if !extra.is_empty() {
                            let _ = queue.append_log(&id, &extra);
                            let _ = queue.progress(&id, &extra, progress, true);
                        }
                    }
                });
                // Also record files from hooks which do not print Homebrew's
                // usual download headings. Observation never changes outcome.
                if let Some(prepared) = &scan_observer {
                    let extra = prepared.additional_file_logs();
                    let _ = queue.append_log(&id, &extra);
                    let _ = queue.progress(
                        &id,
                        &extra,
                        &crate::brew::pty::Progress {
                            phase: None,
                            percent: None,
                            step_index: None,
                            from: None,
                            to: None,
                            bytes: None,
                            total: None,
                            speed: None,
                        },
                        true,
                    );
                }
                outcome
            })
            .await
            .map_err(|e| {
                AppError::new("E_UNKNOWN", "executor_panicked").with_detail(e.to_string())
            })??;
            if result
                .parser
                .error
                .as_ref()
                .is_some_and(|e| e.code == "E_LOCKED")
                && attempts < 3
                && !cancel.load(Ordering::Acquire)
            {
                attempts += 1;
                tokio::select! {_=tokio::time::sleep(self.retry_delay)=>{},_=notify.notified()=>{}}
                if cancel.load(Ordering::Acquire) {
                    return Ok(crate::brew::pty::Outcome {
                        canceled: true,
                        ..result
                    });
                }
            } else {
                drop(prepared);
                if result.code == 0
                    && !result.canceled
                    && !cancel.load(Ordering::Acquire)
                    && task.options.reopen
                {
                    for path in &reopen {
                        let native = self.app_control.clone();
                        let path = path.clone();
                        if crate::blocking(move || native.reopen(&path)).await.is_err() {
                            log::warn!("Update succeeded, but reopening the app failed");
                        }
                    }
                }
                return Ok(result);
            }
        }
    }
    fn append_log(&self, id: &str, lines: &[String]) -> Result<(), AppError> {
        if lines.is_empty() {
            return Ok(());
        }
        use std::io::Write;
        std::fs::create_dir_all(&self.logs)?;
        let path = self.logs.join(format!("{id}.log"));
        let mut options = std::fs::OpenOptions::new();
        options.append(true).create(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let mut file = options.open(path)?;
        for line in lines {
            writeln!(file, "{line}")?;
        }
        Ok(())
    }
    pub(crate) fn uninstall_progress(
        &self,
        id: &str,
        phase: TaskPhase,
        step: u8,
        percent: f32,
        line: Option<String>,
    ) -> Result<(), AppError> {
        let lines: Vec<_> = line.into_iter().collect();
        self.append_log(id, &lines)?;
        self.progress(
            id,
            &lines,
            &Progress {
                phase: Some(phase),
                percent: Some(percent),
                step_index: Some(step),
                from: None,
                to: None,
                bytes: None,
                total: None,
                speed: None,
            },
            true,
        )
    }
    /// Cleanup progress with the bytes freed so far out of the selected size; `lines` are logged and announced once.
    pub(crate) fn cleanup_progress(
        &self,
        id: &str,
        phase: TaskPhase,
        percent: f32,
        (freed, selected): (u64, u64),
        lines: Vec<String>,
    ) -> Result<(), AppError> {
        self.append_log(id, &lines)?;
        self.progress(
            id,
            &lines,
            &Progress {
                phase: Some(phase),
                percent: Some(percent),
                step_index: Some(if phase == TaskPhase::Finishing { 3 } else { 2 }),
                from: None,
                to: None,
                bytes: Some(freed),
                total: Some(selected),
                speed: None,
            },
            true,
        )
    }
    /// Persists the outcome so far without an event of its own; the next progress event carries it.
    pub(crate) fn store_uninstall_outcome(
        &self,
        id: &str,
        outcome: &UninstallOutcome,
    ) -> Result<(), AppError> {
        let mut tasks = self.tasks.lock().map_err(poison)?;
        let task = tasks
            .iter_mut()
            .find(|t| t.id == id)
            .ok_or_else(|| AppError::new("E_UNKNOWN", "missing_task"))?;
        task.outcome = Some(outcome.clone());
        self.database.save_task(task)
    }
    pub(crate) fn set_uninstall_outcome(
        &self,
        id: &str,
        outcome: UninstallOutcome,
    ) -> Result<(), AppError> {
        let mut tasks = self.tasks.lock().map_err(poison)?;
        let task = tasks
            .iter_mut()
            .find(|t| t.id == id)
            .ok_or_else(|| AppError::new("E_UNKNOWN", "missing_task"))?;
        task.outcome = Some(outcome);
        self.database.save_task(task)?;
        (self.sink)(CoreEvent::Task(Box::new(task.clone())));
        Ok(())
    }
    fn finish(
        &self,
        id: &str,
        result: Result<crate::brew::pty::Outcome, AppError>,
    ) -> Result<Task, AppError> {
        let mut tasks = self.tasks.lock().map_err(poison)?;
        let task = tasks
            .iter_mut()
            .find(|t| t.id == id)
            .ok_or_else(|| AppError::new("E_UNKNOWN", "missing_task"))?;
        match result {
            Ok(result) => {
                task.exit_code = Some(result.code);
                task.state = if result.canceled {
                    TaskState::Canceled
                } else if result.code == 0 {
                    TaskState::Succeeded
                } else {
                    TaskState::Failed
                };
                if task.state == TaskState::Failed {
                    task.error = Some(result.parser.error.unwrap_or_else(|| {
                        AppError::new("E_UNKNOWN", "brew_exit").with_detail(result.code.to_string())
                    }));
                }
                if task.state == TaskState::Succeeded {
                    task.percent = Some(100.0);
                    task.phase = Some(TaskPhase::Finishing);
                    task.step_index = task.step_count.map(|n| n - 1);
                }
                task.from_version = result.parser.from_version.or(task.from_version.take());
                task.to_version = result.parser.to_version.or(task.to_version.take());
            }
            Err(error) => {
                let canceled = self
                    .controls
                    .lock()
                    .map_err(poison)?
                    .get(id)
                    .is_some_and(|control| control.cancel.load(Ordering::Acquire));
                task.state = if canceled || crate::running_apps::deferred(&error) {
                    TaskState::Canceled
                } else {
                    TaskState::Failed
                };
                if !canceled {
                    task.error = Some(error);
                }
            }
        }
        task.finished_at = Some(chrono::Utc::now().timestamp_millis());
        self.database.save_task(task)?;
        self.controls.lock().map_err(poison)?.remove(id);
        let task = task.clone();
        tasks.retain(|item| item.id != id);
        drop(tasks);
        (self.sink)(CoreEvent::Task(Box::new(task.clone())));
        Ok(task)
    }
    async fn run(self: Arc<Self>) {
        while !self.stopping.load(Ordering::Acquire) {
            let notified = self.wake.notified();
            let queue = self.clone();
            let task = match crate::blocking(move || queue.next()).await {
                Ok(Some(task)) => task,
                Ok(None) => {
                    notified.await;
                    continue;
                }
                Err(error) => {
                    log::error!("Task queue stopped: {}", error.code);
                    break;
                }
            };
            if task
                .target
                .as_ref()
                .is_some_and(|t| t.kind == TaskKind::Mas)
            {
                let batch = match self.mas_batch(&task) {
                    Ok(batch) => batch,
                    Err(error) => {
                        let _ = self.finish(&task.id, Err(error));
                        continue;
                    }
                };
                let outcomes = self.execute_mas_batch(&batch).await;
                for (task, outcome) in batch.into_iter().zip(outcomes) {
                    if let Ok(task) = self.finish(&task.id, outcome) {
                        let completion = self.completion.lock().ok().and_then(|hook| hook.clone());
                        if let Some(completion) = completion {
                            completion(task).await;
                        }
                    }
                }
                continue;
            }
            let gate = if task.op == TaskOp::Update {
                Some(self.read_gate.clone().write_owned().await)
            } else {
                None
            };
            let result = self.execute_task(&task).await;
            drop(gate);
            let queue = self.clone();
            let id = task.id.clone();
            if let Ok(task) = crate::blocking(move || queue.finish(&id, result)).await {
                let completion = self.completion.lock().ok().and_then(|hook| hook.clone());
                if !self.is_stopping()
                    && let Some(completion) = completion
                {
                    completion(task.clone()).await;
                }
                if task.op == TaskOp::Cleanup && task.state == TaskState::Succeeded {
                    let database = self.database.clone();
                    let _ = tokio::task::spawn_blocking(move || {
                        database.prune(chrono::Utc::now().timestamp_millis())
                    })
                    .await;
                }
            }
        }
    }
}
fn poison<T>(_: std::sync::PoisonError<T>) -> AppError {
    AppError::new("E_UNKNOWN", "queue_poisoned")
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::Path;
    fn fixture(
        scenario: &str,
    ) -> (
        tempfile::TempDir,
        Arc<TaskQueue>,
        Arc<Mutex<Vec<CoreEvent>>>,
    ) {
        let directory = tempfile::tempdir().expect("directory");
        let database = Database::open(&directory.path().join("test.db")).expect("database");
        let wrapper = directory.path().join("brew");
        let fake = Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/fake-brew/brew");
        std::fs::write(&wrapper, format!(r#"#!/usr/bin/python3 -I
import json, os, sys
if sys.argv[1:] == ['info', '--json=v2', '--installed']:
    print(json.dumps({{'casks': [{{'token': token, 'tap': 'homebrew/cask', 'version': '1', 'installed': '1'}} for token in ['calibre', 'target', 'other']]}}))
else:
    os.execv({fake:?}, [{fake:?}] + sys.argv[1:])
"#)).unwrap();
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&wrapper, std::fs::Permissions::from_mode(0o700)).unwrap();
        let settings = Settings {
            brew_path: Some(wrapper.to_string_lossy().into()),
            ..Default::default()
        };
        let events = Arc::new(Mutex::new(Vec::new()));
        let output = events.clone();
        let mut queue = TaskQueue::new(
            database,
            Arc::new(RwLock::new(settings)),
            directory.path().join("logs"),
            directory.path().join("askpass"),
            Arc::new(move |event| output.lock().expect("events").push(event)),
        )
        .expect("queue");
        crate::running_apps::tests::cache_apps(&queue.database, &["calibre"]);
        Arc::get_mut(&mut queue).expect("unique queue").app_control =
            Arc::new(crate::running_apps::tests::FakeApps::default());
        Arc::get_mut(&mut queue).expect("unique queue").retry_delay = Duration::from_millis(20);
        queue
            .extra_env
            .lock()
            .expect("environment")
            .insert("FAKE_BREW_SCENARIO".into(), scenario.into());
        (directory, queue, events)
    }
    fn target(token: &str) -> TaskTarget {
        TaskTarget {
            kind: Kind::Cask.into(),
            token: token.into(),
        }
    }
    #[test]
    fn links_are_rejected_before_queue_persistence_deduplication_or_plan_validation() {
        let (_directory, queue, events) = fixture("install_ok");
        let item = crate::catalog::links::tests::item();
        crate::catalog::links::apply(
            &queue.database,
            &crate::catalog::links::LinkCatalog {
                etag: "\"queue\"".into(),
                items: vec![item.clone()],
            },
            None,
            1,
        )
        .unwrap();
        for kind in [TaskKind::Link, TaskKind::Cask] {
            for op in [
                TaskOp::Install,
                TaskOp::Upgrade,
                TaskOp::Reinstall,
                TaskOp::Uninstall,
            ] {
                for options in [
                    TaskOptions::default(),
                    TaskOptions {
                        plan_id: Some("invalid-plan".into()),
                        ..Default::default()
                    },
                ] {
                    let error = queue
                        .enqueue(
                            op,
                            Some(TaskTarget {
                                kind,
                                token: item.token.clone(),
                            }),
                            options,
                            TaskTrigger::Manual,
                        )
                        .unwrap_err();
                    assert_eq!(error.code, "E_NOT_INSTALLABLE");
                    assert!(queue.database.tasks().unwrap().is_empty());
                    assert!(queue.tasks.lock().unwrap().is_empty());
                }
            }
        }
        assert!(events.lock().unwrap().is_empty());
        // Even malformed tokens and a stopping queue must hit the link guard first.
        queue.stopping.store(true, Ordering::Release);
        assert_eq!(
            queue
                .enqueue(
                    TaskOp::Install,
                    Some(TaskTarget {
                        kind: TaskKind::Link,
                        token: "../invalid".into()
                    }),
                    TaskOptions::default(),
                    TaskTrigger::Manual
                )
                .unwrap_err()
                .code,
            "E_NOT_INSTALLABLE"
        );
        assert!(queue.database.tasks().unwrap().is_empty());
    }
    async fn terminal(queue: &TaskQueue, id: &str) -> Task {
        tokio::time::timeout(Duration::from_secs(5), async {
            loop {
                let task = queue
                    .database
                    .tasks()
                    .expect("task")
                    .into_iter()
                    .find(|t| t.id == id)
                    .expect("exists");
                if !matches!(task.state, TaskState::Queued | TaskState::Running) {
                    return task;
                }
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .expect("terminal state timeout")
    }
    #[tokio::test]
    async fn mas_batch_uses_one_admin_prompt_and_preserves_typed_errors() {
        use crate::updater::admin;
        struct FakeAdmin {
            calls: Mutex<Vec<Vec<String>>>,
            cancel: bool,
        }
        impl admin::Actions for FakeAdmin {
            fn run(&self, requests: &[admin::Request]) -> Result<Vec<admin::ResultItem>, AppError> {
                self.calls
                    .lock()
                    .unwrap()
                    .push(requests.iter().map(|r| r.id.clone()).collect());
                if self.cancel {
                    return Err(AppError::new("E_ADMIN_CANCELED", "fixture_cancel"));
                }
                Ok(requests
                    .iter()
                    .map(|r| {
                        let admin::Action::MasInstallPkg { request } = &r.action else {
                            panic!("wrong action")
                        };
                        request.validate(unsafe { nix::libc::getuid() }).unwrap();
                        assert!(
                            request
                                .pkg
                                .starts_with(std::env::temp_dir().canonicalize().unwrap())
                        );
                        admin::ResultItem {
                            id: r.id.clone(),
                            error: None,
                            backup: None,
                        }
                    })
                    .collect())
            }
        }
        for (scenario, admin_cancel, expected) in [
            ("success", false, None),
            ("201", false, None),
            ("201_then_error", false, None),
            ("201", true, Some("E_ADMIN_CANCELED")),
            ("error:E_MAS_NOT_OWNED", false, Some("E_MAS_NOT_OWNED")),
        ] {
            let (_directory, mut queue, events) = fixture("install_ok");
            let (_server, service) = crate::appstore::tests::queue_service(
                queue.database.clone(),
                queue.settings.clone(),
            )
            .await;
            Arc::get_mut(&mut queue).unwrap().appstore = service;
            let admin = Arc::new(FakeAdmin {
                calls: Mutex::new(vec![]),
                cancel: admin_cancel,
            });
            *queue.native_runtime.lock().unwrap() = native::Runtime {
                store: Some(crate::appstore::install::Runner {
                    executable: Path::new(env!("CARGO_MANIFEST_DIR"))
                        .join("tests/fake-store/store"),
                    environment: [
                        ("OPENNAVO_FAKE_STORE_SCENARIO".into(), scenario.into()),
                        ("OPENNAVO_FAKE_STORE_FIXTURES".into(), "1".into()),
                    ]
                    .into(),
                }),
                admin: Some(admin.clone()),
            };
            let tasks = queue
                .enqueue_many(
                    TaskOp::Install,
                    [42, 43]
                        .map(|id| TaskTarget {
                            kind: TaskKind::Mas,
                            token: id.to_string(),
                        })
                        .into(),
                    TaskOptions::default(),
                    TaskTrigger::Manual,
                )
                .unwrap();
            queue.start();
            for (index, task) in tasks.iter().enumerate() {
                let expected = if scenario == "201_then_error" && index == 1 {
                    Some("E_MAS_NOT_OWNED")
                } else {
                    expected
                };
                let result = terminal(&queue, &task.id).await;
                assert_eq!(
                    result.error.as_ref().map(|e| e.code.as_str()),
                    expected,
                    "{scenario}: {result:?}"
                );
                assert_eq!(
                    result.state,
                    if expected.is_some() {
                        TaskState::Failed
                    } else {
                        TaskState::Succeeded
                    }
                );
            }
            let calls = admin.calls.lock().unwrap();
            assert_eq!(
                calls.len(),
                usize::from(matches!(scenario, "201" | "201_then_error"))
            );
            if let Some(batch) = calls.first() {
                assert_eq!(
                    batch.len(),
                    if scenario == "201_then_error" { 1 } else { 2 }
                );
                if scenario == "201_then_error" {
                    assert_eq!(batch, &vec![tasks[0].id.clone()]);
                }
            }
            assert!(
                events.lock().unwrap().iter().any(
                    |e| matches!(e, CoreEvent::Task(t) if t.phase == Some(TaskPhase::Preparing))
                )
            );
            queue.shutdown();
        }
    }
    #[tokio::test]
    async fn mas_batch_cancel_preserves_completed_item() {
        let (_directory, mut queue, _) = fixture("install_ok");
        let (_server, service) =
            crate::appstore::tests::queue_service(queue.database.clone(), queue.settings.clone())
                .await;
        Arc::get_mut(&mut queue).unwrap().appstore = service;
        let apps = Arc::new(crate::running_apps::tests::FakeApps::default());
        apps.processes
            .lock()
            .unwrap()
            .push(crate::running_apps::Process {
                pid: 42,
                path: "/Applications/Fixture.app".into(),
                bundle_id: "test.app".into(),
            });
        Arc::get_mut(&mut queue).unwrap().app_control = apps.clone();
        queue.native_runtime.lock().unwrap().store = Some(crate::appstore::install::Runner {
            executable: Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/fake-store/store"),
            environment: [(
                "OPENNAVO_FAKE_STORE_SCENARIO".into(),
                "partial_cancel".into(),
            )]
            .into(),
        });
        let tasks = queue
            .enqueue_many(
                TaskOp::Install,
                [42, 43]
                    .into_iter()
                    .map(|id| TaskTarget {
                        kind: TaskKind::Mas,
                        token: id.to_string(),
                    })
                    .collect(),
                TaskOptions {
                    quit_running: true,
                    reopen: true,
                    ..Default::default()
                },
                TaskTrigger::Manual,
            )
            .unwrap();
        queue.start();
        tokio::time::timeout(Duration::from_secs(5), async {
            loop {
                let rows = queue.active().unwrap();
                if rows
                    .iter()
                    .any(|t| t.id == tasks[0].id && t.percent == Some(100.0))
                {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        queue.cancel(&tasks[1].id).unwrap();
        assert_eq!(
            terminal(&queue, &tasks[0].id).await.state,
            TaskState::Succeeded
        );
        assert_eq!(
            terminal(&queue, &tasks[1].id).await.state,
            TaskState::Canceled
        );
        assert_eq!(
            *apps.reopened.lock().unwrap(),
            vec![std::path::PathBuf::from("/Applications/Fixture.app")]
        );
        queue.shutdown();
    }

    #[tokio::test]
    async fn mas_batch_signal_exit_preserves_completed_item() {
        let (_directory, mut queue, _) = fixture("install_ok");
        let (_server, service) =
            crate::appstore::tests::queue_service(queue.database.clone(), queue.settings.clone())
                .await;
        Arc::get_mut(&mut queue).unwrap().appstore = service;
        let apps = Arc::new(crate::running_apps::tests::FakeApps::default());
        apps.processes
            .lock()
            .unwrap()
            .push(crate::running_apps::Process {
                pid: 42,
                path: "/Applications/Fixture.app".into(),
                bundle_id: "test.app".into(),
            });
        Arc::get_mut(&mut queue).unwrap().app_control = apps.clone();
        queue.native_runtime.lock().unwrap().store = Some(crate::appstore::install::Runner {
            executable: Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/fake-store/store"),
            environment: [(
                "OPENNAVO_FAKE_STORE_SCENARIO".into(),
                "partial_signal".into(),
            )]
            .into(),
        });
        let tasks = queue
            .enqueue_many(
                TaskOp::Install,
                [42, 43]
                    .into_iter()
                    .map(|id| TaskTarget {
                        kind: TaskKind::Mas,
                        token: id.to_string(),
                    })
                    .collect(),
                TaskOptions {
                    quit_running: true,
                    reopen: true,
                    ..Default::default()
                },
                TaskTrigger::Manual,
            )
            .unwrap();
        queue.start();
        assert_eq!(
            terminal(&queue, &tasks[0].id).await.state,
            TaskState::Succeeded
        );
        assert_eq!(
            terminal(&queue, &tasks[1].id).await.state,
            TaskState::Failed
        );
        assert_eq!(
            *apps.reopened.lock().unwrap(),
            vec![std::path::PathBuf::from("/Applications/Fixture.app")]
        );
        queue.shutdown();
    }

    #[tokio::test]
    async fn mas_cancel_stops_sidecar_and_oversized_batch_never_persists() {
        let (_directory, mut queue, _) = fixture("install_ok");
        let (_server, service) =
            crate::appstore::tests::queue_service(queue.database.clone(), queue.settings.clone())
                .await;
        Arc::get_mut(&mut queue).unwrap().appstore = service;
        queue.native_runtime.lock().unwrap().store = Some(crate::appstore::install::Runner {
            executable: Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/fake-store/store"),
            environment: [("OPENNAVO_FAKE_STORE_SCENARIO".into(), "cancel".into())].into(),
        });
        assert_eq!(
            queue
                .enqueue_many(
                    TaskOp::Install,
                    (1..=101)
                        .map(|id| TaskTarget {
                            kind: TaskKind::Mas,
                            token: id.to_string()
                        })
                        .collect(),
                    TaskOptions::default(),
                    TaskTrigger::Manual
                )
                .unwrap_err()
                .code,
            "E_INVALID_ARG"
        );
        assert!(queue.database.tasks().unwrap().is_empty());
        let task = queue
            .enqueue(
                TaskOp::Install,
                Some(TaskTarget {
                    kind: TaskKind::Mas,
                    token: "42".into(),
                }),
                TaskOptions::default(),
                TaskTrigger::Manual,
            )
            .unwrap();
        queue.start();
        tokio::time::timeout(Duration::from_secs(3), async {
            while queue.database.tasks().unwrap()[0].state != TaskState::Running {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        queue.cancel(&task.id).unwrap();
        assert_eq!(terminal(&queue, &task.id).await.state, TaskState::Canceled);
        queue.shutdown();
    }
    #[tokio::test]
    async fn five_scenarios_cancel_retry_and_logs() {
        for (scenario, code) in [
            ("install_ok", None),
            ("checksum_fail", Some("E_CHECKSUM")),
            ("sudo_cancel", Some("E_SUDO")),
            ("locked", Some("E_LOCKED")),
            ("slow", None),
        ] {
            let (_directory, queue, events) = fixture(scenario);
            let task = queue
                .enqueue(
                    TaskOp::Install,
                    Some(target("inkscape")),
                    TaskOptions::default(),
                    TaskTrigger::Manual,
                )
                .expect("enqueue");
            let duplicate = queue
                .enqueue(
                    TaskOp::Upgrade,
                    Some(target("inkscape")),
                    TaskOptions::default(),
                    TaskTrigger::Manual,
                )
                .expect("deduplicate");
            assert_eq!(task.id, duplicate.id);
            queue.start();
            if scenario == "slow" {
                tokio::time::timeout(Duration::from_secs(2), async {
                    loop {
                        if queue.active().expect("active")[0].state == TaskState::Running {
                            break;
                        }
                        tokio::time::sleep(Duration::from_millis(10)).await;
                    }
                })
                .await
                .expect("start");
                // Wait for actual simulator output to avoid canceling before process startup on busy machines.
                tokio::time::timeout(Duration::from_secs(5), async {
                    loop {
                        if queue
                            .log(&task.id, None)
                            .expect("logs")
                            .contains("==> Downloading")
                        {
                            break;
                        }
                        tokio::time::sleep(Duration::from_millis(10)).await;
                    }
                })
                .await
                .expect("simulator starts downloading");
                queue.cancel(&task.id).expect("cancel");
            }
            let result = terminal(&queue, &task.id).await;
            assert_eq!(
                result.error.as_ref().map(|e| e.code.as_str()),
                code,
                "{scenario}"
            );
            assert_eq!(
                result.state,
                if scenario == "slow" {
                    TaskState::Canceled
                } else if code.is_some() {
                    TaskState::Failed
                } else {
                    TaskState::Succeeded
                }
            );
            let log = queue.log(&task.id, None).expect("logs");
            assert!(log.contains("==> Downloading"));
            if scenario == "locked" {
                assert_eq!(
                    log.matches("Another active Homebrew process").count(),
                    4,
                    "Initial attempt plus three retries"
                );
            }
            {
                let events = events.lock().expect("events");
                assert!(events.iter().any(|e| matches!(e, CoreEvent::Log(_))));
                assert!(
                    events
                        .iter()
                        .any(|e| matches!(e,CoreEvent::Task(t) if t.state==result.state))
                );
            }
            if scenario == "checksum_fail" {
                queue
                    .extra_env
                    .lock()
                    .expect("environment")
                    .insert("FAKE_BREW_SCENARIO".into(), "install_ok".into());
                let retry = queue
                    .enqueue(
                        TaskOp::Install,
                        Some(target("inkscape")),
                        TaskOptions::default(),
                        TaskTrigger::Retry,
                    )
                    .expect("retry");
                assert_ne!(retry.id, task.id);
                assert_eq!(
                    queue
                        .database
                        .meta(&format!("task_retry_parent:{}", retry.id))
                        .expect("relationship"),
                    Some(task.id.clone())
                );
                assert_eq!(
                    terminal(&queue, &retry.id).await.state,
                    TaskState::Succeeded
                );
            }
            queue.shutdown();
        }
    }
    #[tokio::test]
    async fn priorities_serial_execution_and_queued_cancellation() {
        let (_directory, queue, events) = fixture("install_ok");
        let bundle = queue
            .enqueue(
                TaskOp::Install,
                Some(target("bundle")),
                TaskOptions::default(),
                TaskTrigger::Bundle,
            )
            .expect("batch");
        let scheduled = queue
            .enqueue(
                TaskOp::Install,
                Some(target("schedule")),
                TaskOptions::default(),
                TaskTrigger::Schedule,
            )
            .expect("scheduled");
        let manual = queue
            .enqueue(
                TaskOp::Install,
                Some(target("manual")),
                TaskOptions::default(),
                TaskTrigger::Manual,
            )
            .expect("manual");
        queue.cancel(&scheduled.id).expect("cancel queued task");
        queue.start();
        assert_eq!(
            terminal(&queue, &bundle.id).await.state,
            TaskState::Succeeded
        );
        assert_eq!(
            terminal(&queue, &manual.id).await.state,
            TaskState::Succeeded
        );
        let mut running: Vec<_> = events
            .lock()
            .expect("events")
            .iter()
            .filter_map(|e| {
                if let CoreEvent::Task(t) = e {
                    if t.state == TaskState::Running && t.phase == Some(TaskPhase::Preparing) {
                        Some((t.id.clone(), t.started_at))
                    } else {
                        None
                    }
                } else {
                    None
                }
            })
            .collect();
        // Progress heartbeats may repeat Preparing before the simulator produces output.
        // Compare execution transitions, retaining started_at so a second start is not hidden.
        running.dedup();
        assert_eq!(
            running.iter().map(|(id, _)| id.clone()).collect::<Vec<_>>(),
            vec![manual.id.clone(), bundle.id.clone()]
        );
        let ended = queue.database.tasks().expect("persist");
        let manual = ended.iter().find(|t| t.id == manual.id).expect("manual");
        let bundle = ended.iter().find(|t| t.id == bundle.id).expect("batch");
        assert!(bundle.started_at >= manual.finished_at);
        queue.shutdown();
    }
    #[tokio::test]
    async fn startup_marks_interrupted_and_requires_manual_resume() {
        let (directory, queue, _) = fixture("install_ok");
        let mut interrupted = queue
            .enqueue(
                TaskOp::Install,
                Some(target("interrupted")),
                TaskOptions::default(),
                TaskTrigger::Manual,
            )
            .expect("task");
        interrupted.state = TaskState::Running;
        queue.database.save_task(&interrupted).expect("persist");
        let queued = queue
            .enqueue(
                TaskOp::Install,
                Some(target("queued")),
                TaskOptions::default(),
                TaskTrigger::Bundle,
            )
            .expect("queued");
        let recovered = TaskQueue::new(
            queue.database.clone(),
            queue.settings.clone(),
            directory.path().join("logs"),
            directory.path().join("askpass"),
            Arc::new(|_| {}),
        )
        .expect("recover");
        let ended = queue
            .database
            .tasks()
            .expect("record")
            .into_iter()
            .find(|t| t.id == interrupted.id)
            .expect("interrupt");
        assert_eq!(ended.error.expect("error").code, "E_INTERRUPTED");
        assert_eq!(ended.state, TaskState::Failed);
        recovered.start();
        tokio::time::sleep(Duration::from_millis(100)).await;
        assert_eq!(
            recovered.active().expect("paused")[0].state,
            TaskState::Queued
        );
        let resumed = recovered
            .enqueue(
                TaskOp::Install,
                Some(target("queued")),
                TaskOptions::default(),
                TaskTrigger::Manual,
            )
            .expect("continue");
        assert_eq!(resumed.id, queued.id);
        assert_eq!(
            terminal(&recovered, &queued.id).await.state,
            TaskState::Succeeded
        );
        recovered.shutdown();
        queue.shutdown();
    }
    #[test]
    fn history_clear_preserves_active_tasks() {
        let (_directory, queue, _) = fixture("install_ok");
        let task = queue
            .enqueue(
                TaskOp::Upgrade,
                Some(target("calibre")),
                TaskOptions::default(),
                TaskTrigger::Manual,
            )
            .expect("queued");
        for (index, state) in [
            TaskState::Succeeded,
            TaskState::Failed,
            TaskState::Canceled,
            TaskState::Running,
        ]
        .into_iter()
        .enumerate()
        {
            let mut copy = task.clone();
            copy.id = format!("clear-{index}");
            copy.state = state;
            queue.database.save_task(&copy).expect("save");
        }
        assert_eq!(
            queue.clear_history().expect("clear"),
            HistoryCleared {
                cleared: 3,
                kept: 0
            }
        );
        let retained = queue.database.tasks().expect("read");
        assert_eq!(retained.len(), 2);
        assert!(
            retained
                .iter()
                .all(|task| matches!(task.state, TaskState::Queued | TaskState::Running))
        );
        assert_eq!(
            queue.clear_history().expect("clear again"),
            HistoryCleared {
                cleared: 0,
                kept: 0
            }
        );
    }

    #[test]
    fn history_clear_keeps_runs_whose_trash_items_can_still_be_put_back() {
        use crate::uninstall::{manifest, rules::Snapshot};
        let (directory, queue, _) = fixture("install_ok");
        let root = directory.path().canonicalize().expect("root");
        let template = queue
            .enqueue(
                TaskOp::Upgrade,
                Some(target("calibre")),
                TaskOptions::default(),
                TaskTrigger::Manual,
            )
            .expect("queued");
        let moved = |name: &str| {
            let path = root.join(name);
            std::fs::write(&path, name).expect("file");
            path
        };
        let in_trash = moved("in-trash.txt");
        let emptied = moved("emptied.txt");
        let unflagged = moved("unflagged.txt");
        for (id, op, path, restorable) in [
            ("run-in-trash", TaskOp::Uninstall, &in_trash, true),
            ("run-cleanup-in-trash", TaskOp::Clean, &in_trash, true),
            ("run-emptied", TaskOp::Uninstall, &emptied, true),
            ("run-unflagged", TaskOp::Uninstall, &unflagged, false),
        ] {
            let snapshot = Snapshot::capture(path).expect("snapshot");
            let item = PlanItem {
                id: "item".into(),
                path: path.to_string_lossy().into(),
                category: ItemCategory::AppData,
                kind: PlanItemKind::File,
                count: 1,
                size_bytes: Some(4),
                reason: MatchReason::ZapRule,
                confidence: PlanItemConfidence::High,
                selected: true,
                required: false,
                needs_admin: false,
                shared_with: vec![],
                note: None,
            };
            manifest::pending(&queue.database, id, &item, &snapshot, Some(path)).expect("pending");
            manifest::finish(&queue.database, id, "item", "trash", Some(path), &snapshot)
                .expect("moved");
            let mut run = template.clone();
            run.id = id.into();
            run.op = op;
            run.state = TaskState::Succeeded;
            run.target = None;
            run.outcome = Some(UninstallOutcome {
                freed_bytes: 4,
                trashed_bytes: 4,
                trashed: 1,
                deleted: 0,
                skipped: vec![],
                restorable,
            });
            queue.database.save_task(&run).expect("save");
        }
        // The Trash was emptied behind OpenNavo's back, so this run's flag is stale.
        std::fs::remove_file(&emptied).expect("emptied");
        assert_eq!(
            queue.clear_history().expect("clear"),
            HistoryCleared {
                cleared: 2,
                kept: 2
            }
        );
        let mut left: Vec<_> = queue
            .database
            .tasks()
            .expect("read")
            .into_iter()
            .map(|task| task.id)
            .collect();
        left.sort();
        let mut expected = vec![
            template.id.clone(),
            "run-cleanup-in-trash".to_owned(),
            "run-in-trash".to_owned(),
        ];
        expected.sort();
        assert_eq!(left, expected);
        // Putting the items back leaves nothing to keep: the next clear takes the records too.
        std::fs::remove_file(&in_trash).expect("put back");
        assert_eq!(
            queue.clear_history().expect("clear again"),
            HistoryCleared {
                cleared: 2,
                kept: 0
            }
        );
    }

    #[tokio::test]
    async fn terminal_tasks_release_queue_memory_and_remain_queryable() {
        let (_directory, queue, events) = fixture("install_ok");
        for index in 0..100 {
            let task = queue
                .enqueue(
                    TaskOp::Install,
                    Some(target("inkscape")),
                    TaskOptions::default(),
                    TaskTrigger::Manual,
                )
                .expect("enqueue");
            let expected = match index % 3 {
                0 => {
                    queue.cancel(&task.id).expect("cancel queued");
                    TaskState::Canceled
                }
                1 => {
                    queue.next().expect("start task").expect("queued task");
                    queue
                        .finish(&task.id, Err(AppError::new("E_UNKNOWN", "test_failure")))
                        .expect("finish failed");
                    TaskState::Failed
                }
                _ => {
                    queue.next().expect("start task").expect("queued task");
                    queue
                        .finish(
                            &task.id,
                            Ok(crate::brew::pty::Outcome {
                                code: 0,
                                canceled: false,
                                signal: None,
                                parser: crate::brew::parse::Parser::default(),
                            }),
                        )
                        .expect("finish succeeded");
                    TaskState::Succeeded
                }
            };
            assert!(queue.tasks.lock().expect("tasks").is_empty());
            assert!(queue.controls.lock().expect("controls").is_empty());
            let ended = queue
                .wait_terminal(&task.id)
                .await
                .expect("persisted result");
            assert_eq!(ended.state, expected);
            assert!(ended.finished_at.is_some());
            assert!(events.lock().expect("events").iter().any(|event| {
                matches!(event, CoreEvent::Task(ended) if ended.id == task.id && ended.state == expected)
            }));
        }
        assert_eq!(queue.database.tasks().expect("history").len(), 100);
        assert!(queue.database.task("missing").expect("query").is_none());
        assert_eq!(
            queue
                .wait_terminal("missing")
                .await
                .expect_err("unknown")
                .code,
            "E_NOT_FOUND"
        );
    }

    #[tokio::test]
    async fn unknown_installed_source_never_executes_upgrade_or_reinstall() {
        for op in [TaskOp::Upgrade, TaskOp::Reinstall] {
            let (directory, queue, _) = fixture("install_ok");
            let wrapper = directory.path().join("brew");
            let source = std::fs::read_to_string(&wrapper).unwrap();
            std::fs::write(&wrapper, source.replace("'homebrew/cask'", "None")).unwrap();
            let task = queue
                .enqueue(
                    op,
                    Some(target("calibre")),
                    TaskOptions::default(),
                    TaskTrigger::Manual,
                )
                .unwrap();
            queue.start();
            let ended = terminal(&queue, &task.id).await;
            assert_eq!(ended.state, TaskState::Failed);
            assert_eq!(ended.error.unwrap().code, "E_INVALID_ARG");
            assert_eq!(ended.exit_code, None);
            assert!(queue.log(&task.id, None).unwrap().is_empty());
            queue.shutdown();
        }
    }

    #[tokio::test]
    async fn uncatalogued_third_party_cask_keeps_installed_tap() {
        for op in [TaskOp::Upgrade, TaskOp::Reinstall] {
            let (directory, queue, _) = fixture("install_ok");
            let wrapper = directory.path().join("brew");
            let source = std::fs::read_to_string(&wrapper).unwrap();
            let source = source.replace("homebrew/cask", "owner/tap");
            // Make the fake write fail if the selected repository is lost or changed.
            let source = source.replace("    os.execv", "    if sys.argv[1] in ['upgrade', 'reinstall'] and sys.argv[-1] != 'owner/tap/calibre': sys.exit(99)\n    os.execv");
            std::fs::write(&wrapper, source).unwrap();
            assert!(
                crate::catalog::get(&queue.database, Kind::Cask, "calibre")
                    .unwrap()
                    .is_none()
            );
            let task = queue
                .enqueue(
                    op,
                    Some(target("calibre")),
                    TaskOptions::default(),
                    TaskTrigger::Manual,
                )
                .unwrap();
            queue.start();
            assert_eq!(terminal(&queue, &task.id).await.state, TaskState::Succeeded);
            queue.shutdown();
        }
    }

    #[tokio::test]
    async fn history_export_is_persistent_and_filtered() {
        let (directory, queue, _) = fixture("install_ok");
        let task = queue
            .enqueue(
                TaskOp::Upgrade,
                Some(target("calibre")),
                TaskOptions::default(),
                TaskTrigger::Manual,
            )
            .expect("update");
        queue.start();
        terminal(&queue, &task.id).await;
        let query = HistoryQuery {
            q: Some("CALIBRE".into()),
            ops: vec![TaskOp::Upgrade],
            states: vec![TaskState::Succeeded],
            target: None,
            include_scheduled_updates: false,
            from: None,
            to: None,
            limit: 20,
            offset: 0,
        };
        let page = queue.database.history(&query).expect("history");
        assert_eq!(page.total, 1);
        assert_eq!(page.items[0].from_version.as_deref(), Some("5.4.2"));
        assert!(
            queue
                .database
                .history(&HistoryQuery {
                    states: vec![TaskState::Running],
                    ..query
                })
                .is_err()
        );
        let path = directory.path().join("history.csv");
        assert_eq!(
            crate::commands::tasks::export_csv(&queue.database, &path).expect("export"),
            1
        );
        assert!(
            std::fs::read_to_string(path)
                .expect("CSV")
                .contains("5.5.0")
        );
        queue.shutdown();
    }
    #[tokio::test]
    async fn catalog_and_installed_cache_supply_task_versions_and_download_size() {
        let (_tmp, queue, events) = fixture("install_ok");
        let path =
            Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../apps/server/testdata/desktop");
        let info: crate::catalog::SnapshotInfo =
            serde_json::from_slice(&std::fs::read(path.join("latest.json")).unwrap()).unwrap();
        crate::catalog::import_file(&queue.database, &path.join("catalog.json.gz"), &info, 1)
            .unwrap();
        let catalog = crate::catalog::get(&queue.database, Kind::Cask, "visual-studio-code")
            .unwrap()
            .unwrap();
        let task = queue
            .enqueue(
                TaskOp::Install,
                Some(target("visual-studio-code")),
                TaskOptions::default(),
                TaskTrigger::Manual,
            )
            .unwrap();
        queue.start();
        let result = terminal(&queue, &task.id).await;
        assert_eq!(result.to_version, Some(catalog.version));
        assert!(events.lock().unwrap().iter().any(|event|matches!(event,CoreEvent::Task(t) if t.id==task.id && t.bytes_total==catalog.download_size && t.bytes_total.is_some())));
        queue.shutdown();
    }
    #[tokio::test]
    async fn homebrew_installer_downloads_local_file_executes_and_refreshes_environment() {
        use std::io::{Read, Write};
        let tmp = tempfile::tempdir().unwrap();
        let events = Arc::new(Mutex::new(Vec::new()));
        let saved = events.clone();
        let core = crate::core::DesktopCore::open(
            tmp.path().join("data"),
            tmp.path().join("cache"),
            tmp.path().join("askpass"),
            Arc::new(move |event| saved.lock().unwrap().push(event)),
        )
        .unwrap();
        let destination = tmp.path().join("prefix/bin/brew");
        {
            let mut settings = core.settings.write().unwrap();
            settings.brew_path = Some(destination.to_string_lossy().into());
            settings.mirror = MirrorChoice {
                key: "local_test".into(),
                api_domain: Some("https://api.example.test".into()),
                brew_git_remote: Some("https://git.example.test/brew".into()),
                bottle_domain: None,
                core_git_remote: None,
            };
        }
        let server = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!("http://{}/install.sh", server.local_addr().unwrap());
        let worker = std::thread::spawn(move || {
            let (mut socket, _) = server.accept().unwrap();
            socket
                .set_read_timeout(Some(Duration::from_secs(5)))
                .unwrap();
            let mut input = [0; 8192];
            assert!(socket.read(&mut input).unwrap() > 0);
            let body = include_str!("../tests/fixtures/install_homebrew.sh");
            write!(
                socket,
                "HTTP/1.1 200 OK\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
                body.len(),
                body
            )
            .unwrap();
        });
        *core.queue.installer_url.lock().unwrap() = Some(url);
        {
            let mut env = core.queue.extra_env.lock().unwrap();
            env.insert(
                "FAKE_INSTALL_DEST".into(),
                destination.to_string_lossy().into(),
            );
            env.insert(
                "FAKE_INSTALL_SOURCE".into(),
                Path::new(env!("CARGO_MANIFEST_DIR"))
                    .join("tests/fake-brew/brew")
                    .to_string_lossy()
                    .into(),
            );
        }
        core.queue.start();
        let task = core
            .queue
            .enqueue(
                TaskOp::InstallHomebrew,
                None,
                TaskOptions::default(),
                TaskTrigger::Manual,
            )
            .unwrap();
        let result = terminal(&core.queue, &task.id).await;
        assert_eq!(result.state, TaskState::Succeeded, "{:?}", result.error);
        tokio::time::timeout(Duration::from_secs(5),async { while !events.lock().unwrap().iter().any(|e| matches!(e,CoreEvent::Env(info) if info.brew.as_ref().is_some_and(|brew| brew.path==destination.to_string_lossy()))) { tokio::time::sleep(Duration::from_millis(10)).await; } }).await.unwrap();
        assert!(
            core.queue
                .log(&task.id, None)
                .unwrap()
                .contains("Installation successful")
        );
        assert!(
            write_args(
                TaskOp::InstallHomebrew,
                Some(&target("foo")),
                &TaskOptions::default()
            )
            .is_err()
        );
        assert_eq!(
            crate::installer::source(&Settings::default()),
            &[crate::installer::OFFICIAL]
        );
        assert_eq!(
            crate::installer::source(&core.current_settings().unwrap()),
            &[crate::installer::MIRROR, crate::installer::OFFICIAL]
        );
        worker.join().unwrap();
        core.queue.shutdown();
    }
    #[tokio::test]
    async fn installer_download_can_cancel_and_sudo_failure_is_mapped() {
        use std::io::{Read, Write};
        for canceled in [true, false] {
            let (_tmp, queue, _) = fixture("install_ok");
            let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
            let url = format!("http://{}/install.sh", listener.local_addr().unwrap());
            let accepted = Arc::new(AtomicBool::new(false));
            let flag = accepted.clone();
            let server = std::thread::spawn(move || {
                let (mut socket, _) = listener.accept().unwrap();
                socket
                    .set_read_timeout(Some(Duration::from_secs(5)))
                    .unwrap();
                let mut input = [0; 4096];
                assert!(socket.read(&mut input).unwrap() > 0);
                flag.store(true, Ordering::Release);
                if canceled {
                    std::thread::sleep(Duration::from_millis(200));
                }
                let body = "#!/bin/bash\n# Homebrew test installer\nprintf 'sudo: a password is required\\n'\nexit 1\n";
                let _ = write!(
                    socket,
                    "HTTP/1.1 200 OK\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
                    body.len(),
                    body
                );
            });
            *queue.installer_url.lock().unwrap() = Some(url);
            queue.start();
            let task = queue
                .enqueue(
                    TaskOp::InstallHomebrew,
                    None,
                    TaskOptions::default(),
                    TaskTrigger::Manual,
                )
                .unwrap();
            if canceled {
                tokio::time::timeout(Duration::from_secs(3), async {
                    while !accepted.load(Ordering::Acquire) {
                        tokio::time::sleep(Duration::from_millis(10)).await;
                    }
                })
                .await
                .unwrap();
                queue.cancel(&task.id).unwrap();
            }
            let result = terminal(&queue, &task.id).await;
            if canceled {
                assert_eq!(result.state, TaskState::Canceled);
            } else {
                assert_eq!(result.state, TaskState::Failed);
                assert_eq!(result.error.unwrap().code, "E_SUDO");
            }
            server.join().unwrap();
            queue.shutdown();
        }
    }
    #[tokio::test]
    async fn permission_failure_then_upgrade_emits_only_changes_and_survives_restart() {
        let tmp = tempfile::tempdir().unwrap();
        let events = Arc::new(Mutex::new(Vec::new()));
        let output = events.clone();
        // Test only outcome-based inference/persistence, without system preflight.
        let open = || {
            let core = crate::core::DesktopCore::open(
                tmp.path().join("data"),
                tmp.path().join("cache"),
                tmp.path().join("askpass"),
                Arc::new(|_| {}),
            )
            .unwrap();
            core.infer_permissions_only();
            core
        };
        let core = crate::core::DesktopCore::open(
            tmp.path().join("data"),
            tmp.path().join("cache"),
            tmp.path().join("askpass"),
            Arc::new(move |event| output.lock().unwrap().push(event)),
        )
        .unwrap();
        core.infer_permissions_only();
        core.install_permission_test_completion();
        // Sync scans also read simulated app metadata so completion callbacks cannot replace running-state preflight cache with an empty directory.
        let app_path = tmp.path().join("Example.app");
        std::fs::create_dir_all(app_path.join("Contents")).unwrap();
        std::fs::write(app_path.join("Contents/Info.plist"), "<?xml version=\"1.0\"?><plist version=\"1.0\"><dict><key>CFBundleIdentifier</key><string>test.example</string></dict></plist>").unwrap();
        let wrapper = tmp.path().join("brew");
        let fake = Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/fake-brew/brew");
        std::fs::write(&wrapper, format!(r#"#!/usr/bin/python3 -I
import json, os, sys
if sys.argv[1:2] == ['info']:
    print(json.dumps({{'casks':[{{'token':'example','tap':'homebrew/cask','version':'1','installed':'1','artifacts':[{{'app':[{app_path:?}]}}]}}]}}))
else:
    os.execv({fake:?}, [{fake:?}] + sys.argv[1:])
"#)).unwrap();
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&wrapper, std::fs::Permissions::from_mode(0o700)).unwrap();
        core.settings.write().unwrap().brew_path = Some(wrapper.to_string_lossy().into());
        crate::settings::save_file(
            &core.data_dir.join("settings.json"),
            &core.current_settings().unwrap(),
        )
        .unwrap();
        assert_eq!(
            core.detect_environment().await.unwrap().app_management,
            PermissionState::Unknown
        );
        core.queue.start();
        for (scenario, op, state, count) in [
            (
                "permission_denied",
                TaskOp::Upgrade,
                PermissionState::Denied,
                1,
            ),
            (
                "permission_denied",
                TaskOp::Upgrade,
                PermissionState::Denied,
                1,
            ),
            ("install_ok", TaskOp::Install, PermissionState::Denied, 1),
            ("install_ok", TaskOp::Upgrade, PermissionState::Granted, 2),
            ("install_ok", TaskOp::Uninstall, PermissionState::Granted, 2),
        ] {
            crate::running_apps::tests::cache_apps(&core.database, &["example"]);
            core.queue
                .extra_env
                .lock()
                .unwrap()
                .insert("FAKE_BREW_SCENARIO".into(), scenario.into());
            let task = core
                .queue
                .enqueue(
                    op,
                    Some(target("example")),
                    TaskOptions::default(),
                    TaskTrigger::Manual,
                )
                .unwrap();
            let ended = terminal(&core.queue, &task.id).await;
            if scenario == "permission_denied" {
                assert_eq!(ended.error.as_ref().unwrap().code, "E_PERMISSION");
                assert!(
                    core.queue
                        .log(&task.id, None)
                        .unwrap()
                        .contains("/Applications/Example.app: Operation not permitted")
                );
            } else {
                assert_eq!(ended.state, TaskState::Succeeded);
            }
            // Do not start the next task before its predecessor's completion callback; await this callback's final notification.
            tokio::time::timeout(Duration::from_secs(8), async {
                loop {
                    if core.database.meta("app_management").unwrap().as_deref()
                        == Some(if state == PermissionState::Denied {
                            "denied"
                        } else {
                            "granted"
                        })
                        && events
                            .lock()
                            .unwrap()
                            .iter()
                            .filter(|event| matches!(event, CoreEvent::Env(_)))
                            .count()
                            == count
                    {
                        break;
                    }
                    tokio::time::sleep(Duration::from_millis(10)).await;
                }
            })
            .await
            .unwrap();
            assert_eq!(
                core.detect_environment().await.unwrap().app_management,
                state
            );
            assert_eq!(
                open().detect_environment().await.unwrap().app_management,
                state
            );
        }
        core.queue.shutdown();
    }
    #[tokio::test]
    async fn running_update_is_deferred_without_brew_and_next_task_continues() {
        use crate::running_apps::tests::{FakeApps, cache_apps, process};
        let (_tmp, mut queue, _) = fixture("install_ok");
        cache_apps(&queue.database, &["target", "other"]);
        let apps = Arc::new(FakeApps::default());
        apps.processes.lock().unwrap().push(process("target", 1));
        Arc::get_mut(&mut queue).unwrap().app_control = apps.clone();
        let blocked = queue
            .enqueue(
                TaskOp::Upgrade,
                Some(target("target")),
                TaskOptions::default(),
                TaskTrigger::Schedule,
            )
            .unwrap();
        let next = queue
            .enqueue(
                TaskOp::Upgrade,
                Some(target("other")),
                TaskOptions::default(),
                TaskTrigger::Schedule,
            )
            .unwrap();
        queue.start();
        let ended = terminal(&queue, &blocked.id).await;
        assert_eq!(ended.state, TaskState::Canceled);
        assert_eq!(ended.error.unwrap().code, "E_APP_RUNNING");
        assert_eq!(ended.exit_code, None);
        assert!(queue.log(&blocked.id, None).unwrap().is_empty());
        assert_eq!(terminal(&queue, &next.id).await.state, TaskState::Succeeded);
        assert!(apps.quits.lock().unwrap().is_empty());
        queue.shutdown();
    }

    #[tokio::test]
    async fn confirmed_update_reopens_only_on_success_and_refusal_skips_brew() {
        use crate::running_apps::tests::{FakeApps, cache_apps, process};
        for (scenario, refused, expected) in [
            ("install_ok", false, TaskState::Succeeded),
            ("checksum_fail", false, TaskState::Failed),
            ("install_ok", true, TaskState::Canceled),
        ] {
            let (_tmp, mut queue, _) = fixture(scenario);
            cache_apps(&queue.database, &["target"]);
            let apps = Arc::new(FakeApps::default());
            apps.processes.lock().unwrap().push(process("target", 1));
            apps.refuse.store(refused, Ordering::Release);
            Arc::get_mut(&mut queue).unwrap().app_control = apps.clone();
            let options = TaskOptions {
                quit_running: true,
                reopen: true,
                ..Default::default()
            };
            assert!(
                queue
                    .enqueue(
                        TaskOp::Upgrade,
                        Some(target("target")),
                        options.clone(),
                        TaskTrigger::Schedule
                    )
                    .is_err()
            );
            let task = queue
                .enqueue(
                    TaskOp::Upgrade,
                    Some(target("target")),
                    options,
                    TaskTrigger::Manual,
                )
                .unwrap();
            queue.start();
            let ended = terminal(&queue, &task.id).await;
            assert_eq!(ended.state, expected);
            assert_eq!(
                apps.reopened.lock().unwrap().len(),
                usize::from(expected == TaskState::Succeeded)
            );
            if refused {
                assert!(queue.log(&task.id, None).unwrap().is_empty());
            }
            queue.shutdown();
        }
    }

    #[test]
    fn restart_drops_quit_authorization() {
        let (_tmp, queue, _) = fixture("install_ok");
        queue
            .enqueue(
                TaskOp::Upgrade,
                Some(target("target")),
                TaskOptions {
                    quit_running: true,
                    reopen: true,
                    ..Default::default()
                },
                TaskTrigger::Manual,
            )
            .unwrap();
        let recovered = queue.database.recover_tasks(1).unwrap();
        assert!(!recovered[0].options.quit_running);
        assert!(!recovered[0].options.reopen);
    }
}

// Fail closed when brew cannot establish the installed source. Never let catalog metadata
// silently authorize a different repository for an existing installation.
// catalog_tap: None means absent from the catalog; Some(None) means an official cask.
fn verify_installed_cask_tap(
    installed: &crate::brew::json::Info,
    token: &str,
    catalog_tap: Option<Option<&str>>,
) -> Result<Option<String>, AppError> {
    let source = installed
        .casks
        .iter()
        .find(|cask| cask.token == token)
        .and_then(|cask| cask.tap.as_deref());
    if source.is_none()
        || catalog_tap.is_some_and(|tap| source != Some(tap.unwrap_or("homebrew/cask")))
    {
        return Err(AppError::new(
            "E_INVALID_ARG",
            "installed_cask_tap_mismatch",
        ));
    }
    match source {
        Some("homebrew/cask") => Ok(None),
        Some(tap) => {
            crate::brew::args::validate_tap(tap)?;
            Ok(Some(tap.to_owned()))
        }
        None => unreachable!("unknown source rejected above"),
    }
}

#[cfg(test)]
mod tap_source_tests {
    use super::*;
    #[test]
    fn upgrade_and_reinstall_require_known_matching_installed_source() {
        for (source, catalog, allowed) in [
            (Some("owner/tap"), Some(Some("owner/tap")), true),
            (Some("homebrew/cask"), None, true),
            (Some("other/tap"), Some(Some("owner/tap")), false),
            (Some("homebrew/cask"), Some(Some("owner/tap")), false),
            (Some("owner/tap"), Some(None), false),
            (Some("owner/tap"), None, true),
            (Some("homebrew/cask"), Some(None), true),
            (Some("invalid/tap/extra"), None, false),
            (None, None, false),
            (None, Some(Some("owner/tap")), false),
        ] {
            let info = serde_json::from_value(serde_json::json!({
                "casks": [{"token": "example", "tap": source, "version": "1", "installed": "1"}]
            }))
            .unwrap();
            let result = verify_installed_cask_tap(&info, "example", catalog);
            assert_eq!(result.is_ok(), allowed);
            if allowed {
                let tap = result.unwrap();
                assert_eq!(tap.as_deref(), source.filter(|s| *s != "homebrew/cask"));
                for op in [TaskOp::Upgrade, TaskOp::Reinstall] {
                    let target = TaskTarget {
                        kind: Kind::Cask.into(),
                        token: "example".into(),
                    };
                    let args = write_args_in_tap(
                        op,
                        Some(&target),
                        &TaskOptions::default(),
                        tap.as_deref(),
                    )
                    .unwrap();
                    let expected = tap
                        .as_ref()
                        .map(|tap| format!("{tap}/example"))
                        .unwrap_or("example".into());
                    assert_eq!(args.last(), Some(&expected));
                }
            }
        }
        assert!(
            verify_installed_cask_tap(&crate::brew::json::Info::default(), "missing", None)
                .is_err()
        );
    }
}
