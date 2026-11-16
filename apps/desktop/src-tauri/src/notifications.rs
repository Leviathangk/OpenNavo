use crate::{AppError, model::*, scheduler::Notice};
use serde::{Deserialize, Serialize};
use std::{
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    thread::JoinHandle,
    time::{Duration, Instant},
};
use tauri::{Emitter, Manager};
use tauri_plugin_notification::NotificationExt;

const HERMES_NOTIFICATION_TARGET: &str = "opennavo.hermes";

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
struct HermesNotificationTarget {
    window: String,
    conversation_id: String,
    #[serde(default)]
    connection_id: Option<String>,
    #[serde(default)]
    agent_profile: Option<String>,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct HermesNotificationOpen {
    conversation_id: String,
}

fn hermes_window_allowed(label: &str) -> bool {
    label == "main" || label.starts_with(crate::hermes::window::PROFILE_WINDOW_PREFIX)
}

fn clip_notification_text(value: String, max: usize) -> String {
    if value.chars().count() <= max {
        value
    } else {
        value.chars().take(max).collect()
    }
}

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
        bind_notification_application(&app);
        let click_app = app.clone();
        if let Err(error) = app.notification().on_action(move |action| {
            if action.action_id() != "tap" {
                return;
            }
            let Some(notification) = action.notification() else {
                return;
            };
            let Some(value) = notification.extra().get(HERMES_NOTIFICATION_TARGET) else {
                return;
            };
            let Ok(target) = serde_json::from_value::<HermesNotificationTarget>(value.clone())
            else {
                return;
            };
            if !hermes_window_allowed(&target.window) {
                return;
            }
            let main_app = click_app.clone();
            if let Err(error) = click_app.run_on_main_thread(move || {
                if let Some(window) = main_app.get_webview_window(&target.window) {
                    focus_and_open_conversation(window, target.conversation_id);
                    return;
                }
                if target
                    .window
                    .starts_with(crate::hermes::window::PROFILE_WINDOW_PREFIX)
                    && let Some(profile) = target
                        .agent_profile
                        .as_deref()
                        .filter(|name| crate::hermes::window::valid_profile_name(name))
                    && let Ok(window) = crate::hermes::window::open_profile_window(
                        &main_app,
                        target.connection_id.as_deref(),
                        profile,
                        Some(&target.conversation_id),
                    )
                {
                    focus_and_open_conversation(window, target.conversation_id);
                    return;
                }
                if let Some(window) = main_app.get_webview_window("main") {
                    focus_and_open_conversation(window, target.conversation_id);
                }
            }) {
                log::warn!("Could not open the Hermes notification target: {error}");
            }
        }) {
            log::warn!("Could not register notification click handler: {error}");
        }
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
    pub fn hermes_reply(
        &self,
        window: &tauri::WebviewWindow,
        conversation_id: &str,
        title: String,
        body: String,
    ) -> Result<(), AppError> {
        let conversation_id = conversation_id.trim();
        if !hermes_window_allowed(window.label()) || conversation_id.is_empty() {
            return Ok(());
        }
        let (connection_id, mut agent_profile) = hermes_window_context(window);
        if agent_profile.is_none() {
            agent_profile = window
                .label()
                .strip_prefix(crate::hermes::window::PROFILE_WINDOW_PREFIX)
                .map(str::to_owned);
        }
        let target = HermesNotificationTarget {
            window: window.label().to_owned(),
            conversation_id: conversation_id.to_owned(),
            connection_id,
            agent_profile,
        };
        self.app
            .notification()
            .builder()
            .title(clip_notification_text(title, 120))
            .body(clip_notification_text(body, 320))
            .extra(HERMES_NOTIFICATION_TARGET, target)
            .show()
            .map_err(|_| AppError::new("E_UNKNOWN", "notification_delivery"))?;
        Ok(())
    }
    fn deliver(&self, title: String, body: String, route: &'static str) -> Result<(), AppError> {
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
                match mac_notification_sys::Notification::new()
                    .title(&title)
                    .message(&body)
                    .wait_for_click(true)
                    .send()
                {
                    Ok(response) => {
                        if !stopped.load(Ordering::Acquire)
                            && matches!(
                                response,
                                mac_notification_sys::NotificationResponse::Click
                                    | mac_notification_sys::NotificationResponse::ActionButton(_)
                            )
                        {
                            crate::tray::navigate(&app, route);
                        }
                    }
                    Err(error) => {
                        log::info!("System notification could not be delivered: {error}");
                    }
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

fn focus_and_open_conversation(window: tauri::WebviewWindow, conversation_id: String) {
    let _ = window.unminimize();
    let _ = window.show();
    let _ = window.set_focus();
    let _ = window.emit(
        "hermes-notification-open",
        HermesNotificationOpen { conversation_id },
    );
}

fn hermes_window_context(window: &tauri::WebviewWindow) -> (Option<String>, Option<String>) {
    let Ok(url) = window.url() else {
        return (None, None);
    };
    let mut connection_id = None;
    let mut agent_profile = None;
    for (key, value) in url.query_pairs() {
        match key.as_ref() {
            "connection" => connection_id = Some(value.into_owned()),
            "profile" => agent_profile = Some(value.into_owned()),
            _ => {}
        }
    }
    (connection_id, agent_profile)
}

#[cfg(target_os = "macos")]
fn bind_notification_application(app: &tauri::AppHandle) {
    // `mac-notification-sys` can bind one application per process. Bind it here, before the
    // plugin sends its first notification, so the plugin and our click-waiting notifications
    // share the same identity. A later `set_application` would fail with `AlreadySet` and used
    // to disable every update and monitor alert after the first Hermes reply.
    let identifier = if tauri::is_dev() {
        "com.apple.Terminal".to_owned()
    } else {
        app.config().identifier.clone()
    };
    match mac_notification_sys::set_application(&identifier) {
        Ok(())
        | Err(mac_notification_sys::error::Error::Application(
            mac_notification_sys::error::ApplicationError::AlreadySet(_),
        )) => {}
        Err(error) => {
            log::warn!("Could not bind system notifications to {identifier}: {error}");
        }
    }
}

#[cfg(not(target_os = "macos"))]
fn bind_notification_application(_app: &tauri::AppHandle) {}
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn limits_notification_targets_to_hermes_windows() {
        assert!(hermes_window_allowed("main"));
        assert!(hermes_window_allowed("hermes-profile-work"));
        assert!(!hermes_window_allowed("tray"));
        assert!(!hermes_window_allowed("hermes-profile"));
        assert!(!hermes_window_allowed("other-window"));
    }

    #[test]
    fn clips_notification_text_without_splitting_characters() {
        assert_eq!(clip_notification_text("hello".into(), 3), "hel");
        assert_eq!(clip_notification_text("你好世界".into(), 2), "你好");
        assert_eq!(clip_notification_text("a😀b".into(), 2), "a😀");
    }

    #[test]
    fn the_extra_payload_round_trips_the_profile_window_and_conversation() {
        let target = HermesNotificationTarget {
            window: "hermes-profile-work".into(),
            conversation_id: "chat-42".into(),
            connection_id: Some("panel-1".into()),
            agent_profile: Some("work".into()),
        };
        let value = serde_json::to_value(&target).unwrap();

        assert_eq!(value["window"], "hermes-profile-work");
        assert_eq!(value["conversationId"], "chat-42");
        assert_eq!(value["connectionId"], "panel-1");
        assert_eq!(value["agentProfile"], "work");
        let decoded: HermesNotificationTarget = serde_json::from_value(value).unwrap();
        assert_eq!(decoded.window, target.window);
        assert_eq!(decoded.conversation_id, target.conversation_id);
        assert_eq!(decoded.connection_id, target.connection_id);
        assert_eq!(decoded.agent_profile, target.agent_profile);
    }
}
