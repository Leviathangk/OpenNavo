// SPDX-License-Identifier: GPL-3.0-only
use crate::{AppError, firewall::*};
use serde_json::json;
use std::sync::Arc;
use tauri::State;

#[tauri::command]
#[specta::specta]
pub async fn firewall_status(state: State<'_, Arc<Service>>) -> Result<FirewallStatus, AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.status()).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_enable(
    state: State<'_, Arc<Service>>,
    options: Option<FirewallEnableOptions>,
) -> Result<FirewallStatus, AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.enable(options)).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_disable(state: State<'_, Arc<Service>>) -> Result<FirewallStatus, AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.disable(false)).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_uninstall(
    state: State<'_, Arc<Service>>,
) -> Result<FirewallStatus, AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.disable(true)).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_open_approval(state: State<'_, Arc<Service>>) -> Result<(), AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.open_approval()).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_app_identify(
    state: State<'_, Arc<Service>>,
    path: String,
) -> Result<FirewallApp, AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.identify(path)).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_rules(state: State<'_, Arc<Service>>) -> Result<Vec<FirewallRule>, AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.rules()).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_rule_save(
    state: State<'_, Arc<Service>>,
    input: FirewallRuleInput,
) -> Result<FirewallRule, AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.mutate(json!({"op":"rule_save","input":input}))).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_rule_delete(
    state: State<'_, Arc<Service>>,
    ids: Vec<String>,
) -> Result<(), AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.mutate(json!({"op":"rule_delete","ids":ids}))).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_alerts(
    state: State<'_, Arc<Service>>,
) -> Result<Vec<FirewallAlert>, AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.alerts()).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_alert_reply(
    window: tauri::WebviewWindow,
    state: State<'_, Arc<Service>>,
    reply: FirewallReply,
) -> Result<(), AppError> {
    answer_alert(window, state, reply).await
}

/// The command body, runtime-generic so the command-level test can drive it through Tauri's mock
/// runtime. The command itself fixes the runtime to the app's real one (`Wry`).
async fn answer_alert<R: tauri::Runtime>(
    window: tauri::WebviewWindow<R>,
    state: State<'_, Arc<Service>>,
    reply: FirewallReply,
) -> Result<(), AppError> {
    require_alert_window(window.label())?;
    let service = state.inner().clone();
    crate::blocking(move || service.reply(reply)).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_alert_show(state: State<'_, Arc<Service>>) -> Result<(), AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.show()).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_events(
    state: State<'_, Arc<Service>>,
    query: FirewallEventsQuery,
) -> Result<Vec<FirewallEvent>, AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.events(query)).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_events_clear(state: State<'_, Arc<Service>>) -> Result<(), AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.mutate(json!({"op":"events_clear"}))).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_blocklists(
    state: State<'_, Arc<Service>>,
) -> Result<Vec<FirewallBlocklist>, AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.list(json!({"op":"blocklists"}))).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_blocklist_add(
    state: State<'_, Arc<Service>>,
    source: String,
) -> Result<FirewallBlocklist, AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.blocklist_load(Some(source), None)).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_blocklist_set(
    state: State<'_, Arc<Service>>,
    id: String,
    enabled: bool,
) -> Result<FirewallBlocklist, AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.mutate(json!({"op":"blocklist_set","id":id,"enabled":enabled})))
        .await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_blocklist_refresh(
    state: State<'_, Arc<Service>>,
    id: String,
) -> Result<FirewallBlocklist, AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.blocklist_load(None, Some(id))).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_blocklist_remove(
    state: State<'_, Arc<Service>>,
    id: String,
) -> Result<(), AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.mutate(json!({"op":"blocklist_remove","id":id}))).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_settings(
    state: State<'_, Arc<Service>>,
) -> Result<FirewallSettings, AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.settings()).await
}

#[tauri::command]
#[specta::specta]
pub async fn firewall_settings_set(
    state: State<'_, Arc<Service>>,
    settings: FirewallSettings,
) -> Result<FirewallSettings, AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.mutate(json!({"op":"settings_set","settings":settings}))).await
}

/// Only the question window Rust creates may answer, whatever the capability files say: the main
/// window (or any other webview) would skip the window's answer delay and click-only rule.
pub(crate) fn require_alert_window(label: &str) -> Result<(), AppError> {
    if label == crate::firewall::ALERT_LABEL {
        Ok(())
    } else {
        Err(AppError::new("E_FORBIDDEN", "firewall_alert_window_only"))
    }
}

#[cfg(test)]
mod alert_window_tests {
    use super::require_alert_window;

    #[test]
    fn only_the_question_window_may_answer() {
        assert!(require_alert_window("firewall-alert").is_ok());
        for label in ["main", "tray", "mcp-confirm", "permission-guide", ""] {
            assert_eq!(
                require_alert_window(label).unwrap_err().code,
                "E_FORBIDDEN",
                "{label}"
            );
        }
    }

    /// The real command fixes the runtime to `Wry`, which Tauri's mock runtime cannot construct.
    /// This wrapper has the same IPC name and calls the real command body, so dispatch and the
    /// window guard are exercised end to end. The extra module keeps the generated command macros
    /// from colliding with the real command's.
    mod mock {
        #[tauri::command(rename = "firewall_alert_reply")]
        pub async fn reply(
            window: tauri::WebviewWindow<tauri::test::MockRuntime>,
            state: tauri::State<'_, std::sync::Arc<crate::firewall::Service>>,
            reply: crate::firewall::FirewallReply,
        ) -> Result<(), crate::AppError> {
            super::super::answer_alert(window, state, reply).await
        }
    }

    #[test]
    fn main_window_gets_e_forbidden_from_the_alert_reply_command() {
        use std::time::Duration;

        let app = tauri::test::mock_builder()
            .manage(std::sync::Arc::new(crate::firewall::Service::new(
                std::sync::Arc::new(crate::firewall::bridge::Native),
                std::sync::Arc::new(|_| {}),
                None,
            )))
            .invoke_handler(tauri::generate_handler![mock::reply])
            .build(tauri::test::mock_context(tauri::test::noop_assets()))
            .unwrap();
        let window = tauri::WebviewWindowBuilder::new(&app, "main", Default::default())
            .build()
            .unwrap();
        let (sender, receiver) = std::sync::mpsc::channel();
        window.as_ref().clone().on_message(
            tauri::webview::InvokeRequest {
                cmd: "firewall_alert_reply".into(),
                callback: tauri::ipc::CallbackFn(0),
                error: tauri::ipc::CallbackFn(1),
                url: "tauri://localhost".parse().unwrap(),
                body: tauri::ipc::InvokeBody::Json(serde_json::json!({
                    "reply": {
                        "alertId": "alert",
                        "action": "block",
                        "scope": "app",
                        "until": "always"
                    }
                })),
                headers: Default::default(),
                invoke_key: tauri::test::INVOKE_KEY.into(),
            },
            Box::new(move |_, _, result, _, _| {
                sender.send(result).unwrap();
            }),
        );
        let response = receiver
            .recv_timeout(Duration::from_secs(5))
            .expect("the command replies");
        let tauri::ipc::InvokeResponse::Err(error) = response else {
            panic!("the main window must not answer a firewall question");
        };
        assert_eq!(error.0["code"], "E_FORBIDDEN");
    }

    #[test]
    fn only_the_question_window_capability_grants_the_reply() {
        let acl = include_str!("../../permissions/native.toml");
        let section = |id: &str| {
            acl.split("[[permission]]")
                .find(|entry| entry.contains(&format!("identifier = \"{id}\"")))
                .unwrap_or_else(|| panic!("{id}"))
                .to_owned()
        };
        assert!(!section("native-main").contains("\"firewall_alert_reply\""));
        assert!(section("native-main").contains("\"firewall_alerts\""));
        assert!(section("native-firewall-alert").contains("\"firewall_alert_reply\""));
        let capability: serde_json::Value =
            serde_json::from_str(include_str!("../../capabilities/firewall-alert.json")).unwrap();
        assert_eq!(capability["windows"], serde_json::json!(["firewall-alert"]));
        assert!(
            capability["permissions"]
                .as_array()
                .unwrap()
                .contains(&serde_json::json!("native-firewall-alert"))
        );
    }
}
