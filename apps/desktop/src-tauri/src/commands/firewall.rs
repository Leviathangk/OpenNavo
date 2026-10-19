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
    state: State<'_, Arc<Service>>,
    reply: FirewallReply,
) -> Result<(), AppError> {
    let service = state.inner().clone();
    crate::blocking(move || service.mutate(json!({"op":"alert_reply","reply":reply}))).await
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
