use crate::{AppError, model::*, scheduler::Notice};
use std::{
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    thread::JoinHandle,
    time::{Duration, Instant},
};
use tauri::Manager;

pub fn content(notice: &Notice, locale: Locale) -> Result<Option<(String, String)>, AppError> {
    use crate::native_i18n::text;
    Ok(match notice {
        Notice::Available { names } => Some((
            crate::native_i18n::plural(
                locale,
                "available",
                u32::try_from(names.len()).unwrap_or(u32::MAX),
            )?,
            names
                .iter()
                .take(2)
                .cloned()
                .collect::<Vec<_>>()
                .join(&text(locale, "separator")?),
        )),
        Notice::Completed {
            succeeded,
            failed,
            canceled,
        } => Some((
            text(locale, "completed")?,
            text(locale, "summary")?
                .replace(
                    "{succeeded}",
                    &crate::native_i18n::plural(locale, "completion.succeeded", *succeeded)?,
                )
                .replace(
                    "{failed}",
                    &crate::native_i18n::plural(locale, "completion.failed", *failed)?,
                )
                .replace(
                    "{canceled}",
                    &crate::native_i18n::plural(locale, "completion.canceled", *canceled)?,
                ),
        )),
        Notice::Failed { name } => Some((
            text(locale, "failed")?,
            text(locale, "failureBody")?.replace("{name}", name),
        )),
        Notice::Settings | Notice::TaskFinished(_) => None,
    })
}
#[derive(Default)]
pub struct TaskNotices {
    succeeded: Mutex<u32>,
}
impl TaskNotices {
    pub fn completed(
        &self,
        task: &Task,
        visible: bool,
        more: bool,
    ) -> Result<Vec<Notice>, AppError> {
        if task.trigger == TaskTrigger::Schedule || task.op == TaskOp::Update {
            return Ok(vec![]);
        }
        let mut succeeded = self
            .succeeded
            .lock()
            .map_err(|_| AppError::new("E_UNKNOWN", "task_notices"))?;
        if visible {
            *succeeded = 0;
            return Ok(vec![]);
        }
        let mut notices = vec![];
        match task.state {
            TaskState::Succeeded => *succeeded += 1,
            TaskState::Failed => notices.push(Notice::Failed {
                name: task
                    .target
                    .as_ref()
                    .map(|t| t.token.clone())
                    .unwrap_or_else(|| "Homebrew".into()),
            }),
            _ => {}
        }
        if !more && *succeeded > 0 {
            notices.push(Notice::Completed {
                succeeded: *succeeded,
                failed: 0,
                canceled: 0,
            });
            *succeeded = 0;
        }
        Ok(notices)
    }
}
#[derive(Default)]
pub struct Workers {
    stopped: Arc<AtomicBool>,
    threads: Mutex<Vec<JoinHandle<()>>>,
}
impl Workers {
    pub fn spawn(
        &self,
        work: impl FnOnce(Arc<AtomicBool>) + Send + 'static,
    ) -> Result<(), AppError> {
        if self.stopped.load(Ordering::Acquire) {
            return Ok(());
        }
        let mut threads = self
            .threads
            .lock()
            .map_err(|_| AppError::new("E_UNKNOWN", "notification_workers"))?;
        if self.stopped.load(Ordering::Acquire) {
            return Ok(());
        }
        let mut waiting = vec![];
        for handle in threads.drain(..) {
            if handle.is_finished() {
                let _ = handle.join();
            } else {
                waiting.push(handle);
            }
        }
        *threads = waiting;
        if threads.len() >= 8 {
            return Err(AppError::new("E_UNKNOWN", "notification_workers_limit"));
        }
        let stopped = self.stopped.clone();
        threads.push(
            std::thread::Builder::new()
                .name("opennavo_notification".into())
                .spawn(move || work(stopped))?,
        );
        Ok(())
    }
    pub fn stop(&self) {
        self.stopped.store(true, Ordering::Release);
    }
    pub fn pending(&self) -> usize {
        self.threads
            .lock()
            .ok()
            .map(|handles| {
                handles
                    .iter()
                    .filter(|handle| !handle.is_finished())
                    .count()
            })
            .unwrap_or(0)
    }
    pub async fn drain(&self, limit: Duration) {
        let deadline = Instant::now() + limit;
        while self.pending() > 0 && Instant::now() < deadline {
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
        if let Ok(mut threads) = self.threads.lock() {
            let mut waiting = vec![];
            for handle in threads.drain(..) {
                if handle.is_finished() {
                    let _ = handle.join();
                } else {
                    waiting.push(handle);
                }
            }
            *threads = waiting;
        }
    }
}
pub struct NativeNotifications {
    app: tauri::AppHandle,
    pub workers: Workers,
    tasks: TaskNotices,
    permission_requested: AtomicBool,
}
impl NativeNotifications {
    pub fn new(app: tauri::AppHandle) -> Arc<Self> {
        Arc::new(Self {
            app,
            workers: Workers::default(),
            tasks: TaskNotices::default(),
            permission_requested: AtomicBool::new(false),
        })
    }
    pub fn send(&self, notice: Notice, locale: Locale) -> Result<(), AppError> {
        if let Notice::TaskFinished(task) = notice {
            let Some(core) = self.app.try_state::<Arc<crate::core::DesktopCore>>() else {
                return Ok(());
            };
            if core.queue.is_stopping() {
                return Ok(());
            }
            let visible = self
                .app
                .get_webview_window("main")
                .is_some_and(|w| w.is_visible().unwrap_or(true))
                || !core.current_settings()?.notify_updates;
            let more = core
                .queue
                .active()?
                .iter()
                .any(|t| t.trigger != TaskTrigger::Schedule && t.op != TaskOp::Update);
            for notice in self.tasks.completed(&task, visible, more)? {
                self.send(notice, locale)?;
            }
            return Ok(());
        }
        let Some((title, body)) = content(&notice, locale)? else {
            crate::tray::refresh(&self.app);
            return Ok(());
        };
        self.deliver(title, body, "/updates")
    }
    pub fn monitor(
        &self,
        issue: &crate::monitor::types::HealthIssue,
        locale: Locale,
    ) -> Result<(), AppError> {
        let (title, body) = crate::monitor::alerts::content(issue, locale)?;
        self.deliver(title, body, crate::monitor::alerts::route(issue.key))
    }
    fn deliver(&self, title: String, body: String, route: &'static str) -> Result<(), AppError> {
        use tauri_plugin_notification::NotificationExt;
        if !self.permission_requested.swap(true, Ordering::AcqRel) {
            self.app
                .notification()
                .request_permission()
                .map_err(|_| AppError::new("E_UNKNOWN", "notification_permission"))?;
        }
        let app = self.app.clone();
        self.workers.spawn(move |stopped| {
            #[cfg(target_os = "macos")]
            {
                if stopped.load(Ordering::Acquire) {
                    return;
                }
                static READY: std::sync::OnceLock<bool> = std::sync::OnceLock::new();
                if !*READY.get_or_init(|| {
                    mac_notification_sys::set_application(&app.config().identifier).is_ok()
                }) {
                    log::info!("System notifications temporarily unavailable");
                    return;
                }
                let response = mac_notification_sys::Notification::new()
                    .title(&title)
                    .message(&body)
                    .wait_for_click(true)
                    .send();
                if !stopped.load(Ordering::Acquire)
                    && matches!(
                        response,
                        Ok(mac_notification_sys::NotificationResponse::Click
                            | mac_notification_sys::NotificationResponse::ActionButton(_))
                    )
                {
                    crate::tray::navigate(&app, route);
                }
            }
            #[cfg(not(target_os = "macos"))]
            {
                let _ = (stopped, route);
                let _ = app.notification().builder().title(title).body(body).show();
            }
        })
    }
    pub async fn shutdown(&self) {
        self.workers.stop();
        // Remove this app's delivered notifications; native removal polling releases threads waiting for clicks.
        let deadline = Instant::now() + Duration::from_secs(3);
        while self.workers.pending() > 0 && Instant::now() < deadline {
            // Repeat cleanup to cover threads created but not yet delivering notifications concurrently with exit.
            let _ = self.app.run_on_main_thread(clear_delivered);
            self.workers.drain(Duration::from_millis(100)).await;
        }
    }
}
#[cfg(target_os = "macos")]
fn clear_delivered() {
    use objc2::{
        msg_send,
        rc::Retained,
        runtime::{AnyClass, AnyObject},
    };
    if let Some(class) = AnyClass::get(c"NSUserNotificationCenter") {
        unsafe {
            let center: Retained<AnyObject> = msg_send![class, defaultUserNotificationCenter];
            let _: () = msg_send![&*center, removeAllDeliveredNotifications];
        }
    }
}
#[cfg(not(target_os = "macos"))]
fn clear_delivered() {}
