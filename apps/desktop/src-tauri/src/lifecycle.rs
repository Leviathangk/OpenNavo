use crate::{AppError, core::DesktopCore, model::*, queue::TaskQueue};
use std::{
    sync::{
        Arc,
        atomic::{AtomicU8, Ordering},
    },
    time::Duration,
};
use tauri::Manager;

const ALIVE: u8 = 0;
const INSPECTING: u8 = 1;
const STOPPING: u8 = 2;
const READY: u8 = 3;
pub const LOGIN_ARG: &str = "--hidden";

#[derive(Default)]
pub struct Lifecycle {
    phase: AtomicU8,
}
#[derive(Debug, PartialEq)]
pub enum ExitDecision {
    Confirm(QuitRequested),
    Cleanup,
    Pending,
}

#[derive(Debug, serde::Serialize, thiserror::Error)]
#[serde(rename_all = "snake_case")]
enum ProcessError {
    #[error("invalid_exit_code")]
    InvalidExitCode,
    #[error("core_unavailable")]
    CoreUnavailable,
    #[error("lifecycle_unavailable")]
    LifecycleUnavailable,
    #[error("active_tasks")]
    ActiveTasks,
    #[error("exit_in_progress")]
    ExitInProgress,
    #[error(transparent)]
    #[serde(untagged)]
    Core(#[from] AppError),
}

impl Lifecycle {
    pub fn begin_request(&self) -> bool {
        self.phase
            .compare_exchange(ALIVE, INSPECTING, Ordering::AcqRel, Ordering::Acquire)
            .is_ok()
    }
    pub fn inspected(&self, counts: QuitRequested) -> ExitDecision {
        let active = counts.running > 0 || counts.queued > 0;
        if self
            .phase
            .compare_exchange(
                INSPECTING,
                if active { ALIVE } else { STOPPING },
                Ordering::AcqRel,
                Ordering::Acquire,
            )
            .is_err()
        {
            return ExitDecision::Pending;
        }
        if active {
            ExitDecision::Confirm(counts)
        } else {
            ExitDecision::Cleanup
        }
    }
    pub fn confirm(&self) -> bool {
        let mut current = self.phase.load(Ordering::Acquire);
        loop {
            if matches!(current, STOPPING | READY) {
                return false;
            }
            match self.phase.compare_exchange(
                current,
                STOPPING,
                Ordering::AcqRel,
                Ordering::Acquire,
            ) {
                Ok(_) => return true,
                Err(actual) => current = actual,
            }
        }
    }
    pub fn ready(&self) {
        self.phase.store(READY, Ordering::Release);
    }
    pub fn exit_ready(&self) -> bool {
        self.phase.load(Ordering::Acquire) == READY
    }
    fn inspection_failed(&self) {
        let _ = self
            .phase
            .compare_exchange(INSPECTING, ALIVE, Ordering::AcqRel, Ordering::Acquire);
    }
}

pub fn counts(tasks: &[Task]) -> QuitRequested {
    QuitRequested {
        running: tasks
            .iter()
            .filter(|t| t.state == TaskState::Running)
            .count() as u32,
        queued: tasks
            .iter()
            .filter(|t| t.state == TaskState::Queued)
            .count() as u32,
    }
}
pub fn hidden_launch(args: &[String]) -> bool {
    args.iter().any(|arg| arg == LOGIN_ARG)
}
pub fn window_state_flags(hidden: bool) -> tauri_plugin_window_state::StateFlags {
    use tauri_plugin_window_state::StateFlags;
    if hidden {
        StateFlags::SIZE | StateFlags::POSITION
    } else {
        StateFlags::all().difference(StateFlags::VISIBLE)
    }
}
#[derive(Debug, PartialEq)]
pub enum CloseAction {
    Hide,
    RequestExit,
}
pub fn close_action(settings: &Settings) -> CloseAction {
    if settings.keep_in_menu_bar_on_close {
        CloseAction::Hide
    } else {
        CloseAction::RequestExit
    }
}

pub async fn interrupt_and_wait(queue: Arc<TaskQueue>, limit: Duration) -> Result<bool, AppError> {
    let shutdown = queue.clone();
    crate::blocking(move || {
        shutdown.shutdown();
        Ok(())
    })
    .await?;
    Ok(tokio::time::timeout(limit, async {
        loop {
            let active = queue.clone();
            let tasks = crate::blocking(move || active.active()).await?;
            if !tasks.iter().any(|t| t.state == TaskState::Running) {
                break;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
        Ok::<(), AppError>(())
    })
    .await
    .is_ok_and(|result| result.is_ok()))
}

pub async fn prepare_restart(queue: Arc<TaskQueue>) -> Result<bool, AppError> {
    let counts = crate::blocking(move || queue.prepare_exit()).await?;
    Ok(counts.running == 0 && counts.queued == 0)
}

async fn finish_quit<R: tauri::Runtime>(
    app: tauri::AppHandle<R>,
    core: Arc<DesktopCore>,
    code: i32,
) {
    crate::monitor::fan::client::shutdown(&app).await;
    let native = app
        .try_state::<Arc<crate::notifications::NativeNotifications>>()
        .map(|n| n.inner().clone());
    let _ = tokio::time::timeout(Duration::from_secs(10), async {
        let queue = interrupt_and_wait(core.queue.clone(), Duration::from_secs(10));
        let notices = async {
            if let Some(native) = native {
                native.shutdown().await;
            }
        };
        let (result, ()) = tokio::join!(queue, notices);
        if !matches!(result, Ok(true)) {
            log::warn!("Failed to interrupt tasks during quit");
        }
    })
    .await;
    if let Some(lifecycle) = app.try_state::<Arc<Lifecycle>>() {
        lifecycle.ready();
    }
    if code == tauri::RESTART_EXIT_CODE {
        app.request_restart();
    } else {
        app.exit(code);
    }
}

// Preserve process plugin IPC; Tauri's original restart cannot prevent_exit, so seal the queue before cleanup.
pub fn guarded_process<R: tauri::Runtime>() -> tauri::plugin::TauriPlugin<R> {
    tauri::plugin::Builder::new("process")
        .invoke_handler(tauri::generate_handler![process_exit, process_restart])
        .build()
}

#[tauri::command(rename = "exit")]
fn process_exit<R: tauri::Runtime>(
    app: tauri::AppHandle<R>,
    code: i32,
) -> Result<(), ProcessError> {
    if code == tauri::RESTART_EXIT_CODE {
        return Err(ProcessError::InvalidExitCode);
    }
    app.exit(code);
    Ok(())
}

#[tauri::command(rename = "restart")]
async fn process_restart<R: tauri::Runtime>(app: tauri::AppHandle<R>) -> Result<(), ProcessError> {
    let core = app
        .try_state::<Arc<DesktopCore>>()
        .ok_or(ProcessError::CoreUnavailable)?
        .inner()
        .clone();
    if !prepare_restart(core.queue.clone()).await? {
        // Do not begin exiting with active tasks; the frontend updater can suggest restarting after completion.
        return Err(ProcessError::ActiveTasks);
    }
    let lifecycle = app
        .try_state::<Arc<Lifecycle>>()
        .ok_or(ProcessError::LifecycleUnavailable)?;
    if !lifecycle.confirm() {
        return Err(ProcessError::ExitInProgress);
    }
    finish_quit(app.clone(), core, tauri::RESTART_EXIT_CODE).await;
    Ok(())
}

pub async fn confirmed_quit(app: tauri::AppHandle, core: Arc<DesktopCore>) -> Result<(), AppError> {
    let lifecycle = app
        .try_state::<Arc<Lifecycle>>()
        .ok_or_else(|| AppError::new("E_UNKNOWN", "lifecycle"))?
        .inner()
        .clone();
    if lifecycle.confirm() {
        finish_quit(app, core, 0).await;
    }
    Ok(())
}
pub fn request_exit(app: &tauri::AppHandle, code: i32) {
    let Some(lifecycle) = app.try_state::<Arc<Lifecycle>>() else {
        return;
    };
    if !lifecycle.begin_request() {
        return;
    }
    let lifecycle = lifecycle.inner().clone();
    let Some(core) = app.try_state::<Arc<DesktopCore>>() else {
        lifecycle.inspection_failed();
        return;
    };
    let core = core.inner().clone();
    let handle = app.clone();
    tauri::async_runtime::spawn(async move {
        let queue = core.queue.clone();
        let counts = crate::blocking(move || queue.prepare_exit()).await;
        match counts {
            Ok(counts) => match lifecycle.inspected(counts) {
                ExitDecision::Confirm(counts) => {
                    crate::deeplink::show_main(&handle);
                    (core.sink)(crate::events::CoreEvent::Quit(counts));
                }
                ExitDecision::Cleanup => finish_quit(handle, core, code).await,
                ExitDecision::Pending => {}
            },
            Err(error) => {
                lifecycle.inspection_failed();
                crate::deeplink::show_main(&handle);
                log::warn!("Quit confirmation could not read tasks: {}", error.code);
            }
        }
    });
}
pub fn install(app: &tauri::AppHandle, hidden: bool) {
    app.manage(Arc::new(Lifecycle::default()));
    if !hidden {
        crate::deeplink::show_main(app);
    }
}
pub fn run_event(app: &tauri::AppHandle, event: tauri::RunEvent) {
    match event {
        tauri::RunEvent::ExitRequested { api, code, .. } => {
            if !app
                .try_state::<Arc<Lifecycle>>()
                .is_some_and(|l| l.exit_ready())
            {
                api.prevent_exit();
                request_exit(app, code.unwrap_or(0));
            }
        }
        #[cfg(target_os = "macos")]
        tauri::RunEvent::Reopen { .. } => crate::deeplink::show_main(app),
        _ => {}
    }
}
