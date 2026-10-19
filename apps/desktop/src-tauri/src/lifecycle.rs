use crate::{AppError, core::DesktopCore, model::*, queue::TaskQueue};
use std::{
    sync::{
        Arc, Mutex,
        atomic::{AtomicU8, Ordering},
    },
    time::Duration,
};
use tauri::Manager;

const ALIVE: u8 = 0;
const INSPECTING: u8 = 1;
const STOPPING: u8 = 2;
const READY: u8 = 3;
const CONFIRMING: u8 = 4;
type SystemReply = Box<dyn FnOnce(bool) + Send>;
pub const LOGIN_ARG: &str = "--hidden";

#[derive(Default)]
pub struct Lifecycle {
    phase: AtomicU8,
    system_reply: Mutex<Option<SystemReply>>,
}
#[derive(Debug, PartialEq)]
pub enum ExitDecision {
    Confirm(QuitRequested),
    Cleanup,
    Pending,
}
/// Answer to a macOS quit request (NSApplicationTerminateReply).
#[derive(Debug, PartialEq)]
pub enum TerminateReply {
    /// Another native request already owns the deferred reply.
    Cancel,
    /// Cleanup already finished: let macOS quit.
    Now,
    /// Tasks are being inspected; the answer follows once known.
    Later,
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
                if active { CONFIRMING } else { STOPPING },
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
    /// Retain the native request until the user decides and cleanup completes.
    /// The boolean says whether this caller must inspect tasks; otherwise the existing quit owns that work.
    pub fn defer_termination(
        &self,
        reply: impl FnOnce(bool) + Send + 'static,
    ) -> (TerminateReply, bool) {
        let mut pending = self.system_reply.lock().unwrap();
        loop {
            if self.exit_ready() {
                return (TerminateReply::Now, false);
            }
            if pending.is_some() {
                return (TerminateReply::Cancel, false);
            }
            let inspect = match self.phase.load(Ordering::Acquire) {
                ALIVE => {
                    if !self.begin_request() {
                        continue;
                    }
                    true
                }
                INSPECTING | CONFIRMING | STOPPING => false,
                _ => continue, // Cleanup finished between the reads above.
            };
            *pending = Some(Box::new(reply));
            return (TerminateReply::Later, inspect);
        }
    }
    pub fn confirmation_pending(&self) -> bool {
        self.phase.load(Ordering::Acquire) == CONFIRMING
    }
    pub fn restore_confirmation(&self, show: impl FnOnce()) {
        if self.confirmation_pending() {
            show();
        }
    }

    pub fn cancel(&self) {
        let reply = {
            let mut pending = self.system_reply.lock().unwrap();
            if self
                .phase
                .compare_exchange(CONFIRMING, ALIVE, Ordering::AcqRel, Ordering::Acquire)
                .is_err()
            {
                return;
            }
            pending.take()
        };
        if let Some(reply) = reply {
            reply(false);
        }
    }
    pub fn take_system_reply(&self) -> Option<SystemReply> {
        self.system_reply.lock().unwrap().take()
    }
    pub fn ready(&self) {
        self.phase.store(READY, Ordering::Release);
    }
    pub fn exit_ready(&self) -> bool {
        self.phase.load(Ordering::Acquire) == READY
    }
    pub fn inspection_failed(&self) {
        let reply = {
            let mut pending = self.system_reply.lock().unwrap();
            if self
                .phase
                .compare_exchange(INSPECTING, ALIVE, Ordering::AcqRel, Ordering::Acquire)
                .is_err()
            {
                return; // A concurrent confirmed quit owns the reply now.
            }
            pending.take()
        };
        if let Some(reply) = reply {
            reply(false);
        }
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

/// Return fans to automatic control, stop notifications and the task queue, then mark the process ready to exit.
async fn release<R: tauri::Runtime>(app: &tauri::AppHandle<R>, core: &Arc<DesktopCore>) {
    if let Some(firewall) = app.try_state::<Arc<crate::firewall::Service>>() {
        firewall.shutdown();
    }
    crate::monitor::fan::client::shutdown(app).await;
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
}

async fn finish_quit<R: tauri::Runtime>(
    app: tauri::AppHandle<R>,
    core: Arc<DesktopCore>,
    code: i32,
) {
    let lifecycle = app.state::<Arc<Lifecycle>>();
    // Bound native cleanup too: macOS is waiting for our answer.
    if tokio::time::timeout(Duration::from_secs(20), release(&app, &core))
        .await
        .is_err()
    {
        log::warn!("Quit cleanup timed out");
        lifecycle.ready();
    }
    if let Some(reply) = lifecycle.take_system_reply() {
        reply(true);
        return;
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

pub async fn confirmed_quit<R: tauri::Runtime>(
    app: tauri::AppHandle<R>,
    core: Arc<DesktopCore>,
) -> Result<(), AppError> {
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
pub fn request_exit<R: tauri::Runtime>(app: &tauri::AppHandle<R>, code: i32) {
    let Some(lifecycle) = app.try_state::<Arc<Lifecycle>>() else {
        return;
    };
    if !lifecycle.begin_request() {
        lifecycle.restore_confirmation(|| crate::deeplink::show_main(app));
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
/// Keep macOS waiting while tasks are inspected and the user decides. After cleanup, answer the original
/// request so logout, shutdown and "Quit & Reopen" retain their intent. Cancel answers false without exiting.
pub fn terminate_requested<R: tauri::Runtime>(
    app: &tauri::AppHandle<R>,
    reply: impl FnOnce(bool) + Send + 'static,
) -> TerminateReply {
    let (Some(lifecycle), Some(core)) = (
        app.try_state::<Arc<Lifecycle>>(),
        app.try_state::<Arc<DesktopCore>>(),
    ) else {
        return TerminateReply::Now;
    };
    let (answer, inspect) = lifecycle.defer_termination(reply);
    if !inspect {
        lifecycle.restore_confirmation(|| crate::deeplink::show_main(app));
        return answer;
    }
    let lifecycle = lifecycle.inner().clone();
    let core = core.inner().clone();
    let handle = app.clone();
    tauri::async_runtime::spawn(async move {
        let queue = core.queue.clone();
        match crate::blocking(move || queue.prepare_exit()).await {
            Ok(counts) => match lifecycle.inspected(counts) {
                ExitDecision::Confirm(counts) => {
                    crate::deeplink::show_main(&handle);
                    (core.sink)(crate::events::CoreEvent::Quit(counts));
                }
                ExitDecision::Cleanup => finish_quit(handle, core, 0).await,
                // A concurrent confirmed quit owns cleanup and the retained reply.
                ExitDecision::Pending => {}
            },
            Err(error) => {
                lifecycle.inspection_failed();
                crate::deeplink::show_main(&handle);
                log::warn!("Quit confirmation could not read tasks: {}", error.code);
            }
        }
    });
    TerminateReply::Later
}

/// Tao does not implement `applicationShouldTerminate:`, so ⌘Q, the Dock's Quit, System Settings' "Quit & Reopen" and
/// logout used to quit at once, skipping the running-task confirmation and the cleanup ExitRequested gets.
#[cfg(target_os = "macos")]
mod terminate {
    use super::{TerminateReply, terminate_requested};
    use objc2::{
        MainThreadMarker, ffi, msg_send,
        runtime::{AnyObject, Imp, Sel},
        sel,
    };
    use objc2_app_kit::NSApplication;
    use std::sync::OnceLock;

    static APP: OnceLock<tauri::AppHandle> = OnceLock::new();
    // NSApplicationTerminateReply.
    const CANCEL: usize = 0;
    const NOW: usize = 1;
    const LATER: usize = 2;

    type ShouldTerminate =
        unsafe extern "C-unwind" fn(*mut AnyObject, Sel, *mut AnyObject) -> usize;

    unsafe extern "C-unwind" fn should_terminate(
        _delegate: *mut AnyObject,
        _cmd: Sel,
        _sender: *mut AnyObject,
    ) -> usize {
        let Some(app) = APP.get() else {
            return NOW;
        };
        let handle = app.clone();
        let reply = move |quit: bool| {
            let _ = handle.run_on_main_thread(move || {
                if let Some(mtm) = MainThreadMarker::new() {
                    NSApplication::sharedApplication(mtm).replyToApplicationShouldTerminate(quit);
                }
            });
        };
        match terminate_requested(app, reply) {
            TerminateReply::Cancel => CANCEL,
            TerminateReply::Now => NOW,
            TerminateReply::Later => LATER,
        }
    }

    /// Add the method to tao's app delegate class; call on the main thread after the event loop exists.
    pub fn install(app: &tauri::AppHandle) {
        if APP.set(app.clone()).is_err() {
            return;
        }
        let Some(mtm) = MainThreadMarker::new() else {
            return;
        };
        let application = NSApplication::sharedApplication(mtm);
        // SAFETY: reads NSApp's delegate, which tao sets when it creates the event loop.
        let delegate: *mut AnyObject = unsafe { msg_send![&*application, delegate] };
        if delegate.is_null() {
            log::warn!("Quit confirmation unavailable for system quit requests: no app delegate");
            return;
        }
        // SAFETY: `should_terminate` matches `- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)sender`
        // ("Q@:@"); class_addMethod leaves an existing implementation untouched and reports it.
        let added = unsafe {
            let class = ffi::object_getClass(delegate);
            ffi::class_addMethod(
                class.cast_mut(),
                sel!(applicationShouldTerminate:),
                std::mem::transmute::<ShouldTerminate, Imp>(should_terminate),
                c"Q@:@".as_ptr(),
            )
        };
        if !added.as_bool() {
            log::warn!(
                "Quit confirmation unavailable for system quit requests: the delegate already answers them"
            );
        }
    }
}

pub fn install(app: &tauri::AppHandle, hidden: bool) {
    app.manage(Arc::new(Lifecycle::default()));
    #[cfg(target_os = "macos")]
    terminate::install(app);
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
        tauri::RunEvent::Reopen { .. } if !crate::tray::panel::suppress_reopen() => {
            crate::deeplink::show_main(app);
        }
        _ => {}
    }
}
