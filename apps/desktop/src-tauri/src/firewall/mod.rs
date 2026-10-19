// SPDX-License-Identifier: GPL-3.0-only
//! Outbound application firewall, native lifecycle, and alert delivery.

pub mod bridge;
pub mod service;
mod types;
mod window;

pub use service::Service;
use std::sync::{
    Arc,
    atomic::{AtomicU8, Ordering},
};
use tauri::Manager;
pub use types::*;

pub const ALERT_LABEL: &str = "firewall-alert";
static PROTECTION: AtomicU8 = AtomicU8::new(2);
pub fn failure(code: FirewallErrorCode) -> crate::AppError {
    crate::AppError::new(code.as_str(), "firewall_request")
}
pub fn protection_for(status: &FirewallStatus) -> crate::security::ProtectionState {
    match (status.availability, status.filter) {
        (FirewallAvailability::Available, FirewallFilterState::On) => {
            crate::security::ProtectionState::On
        }
        (FirewallAvailability::Available, _) => crate::security::ProtectionState::Off,
        _ => crate::security::ProtectionState::Unavailable,
    }
}
fn set_protection(status: &FirewallStatus) {
    PROTECTION.store(
        match protection_for(status) {
            crate::security::ProtectionState::On => 1,
            crate::security::ProtectionState::Off => 0,
            _ => 2,
        },
        Ordering::Release,
    );
}
pub fn protection() -> crate::security::ProtectionState {
    match PROTECTION.load(Ordering::Acquire) {
        0 => crate::security::ProtectionState::Off,
        1 => crate::security::ProtectionState::On,
        _ => crate::security::ProtectionState::Unavailable,
    }
}
pub fn install(app: &tauri::AppHandle, core: &Arc<crate::core::DesktopCore>) {
    use tauri_specta::Event;
    app.manage(window::WindowState::default());
    let handle = app.clone();
    let sink = Arc::new(move |update| {
        let result = match update {
            service::Update::Status(status) => {
                crate::events::FirewallStatusEvent(status).emit(&handle)
            }
            service::Update::Alerts(alerts) => {
                let visible = !alerts.is_empty();
                let result = crate::events::FirewallAlertsEvent { alerts }.emit(&handle);
                window::sync(&handle, visible);
                result
            }
            service::Update::Changed(what) => {
                crate::events::FirewallChangedEvent { what }.emit(&handle)
            }
            service::Update::Activity(events) => {
                crate::events::FirewallActivityEvent { events }.emit(&handle)
            }
        };
        if result.is_err() {
            log::warn!("Firewall event delivery failed");
        }
    });
    let service = Arc::new(Service::new(
        Arc::new(bridge::Native),
        sink,
        Some((
            core.database.clone(),
            core.data_dir.join("logs/local-app-icons"),
        )),
    ));
    app.manage(service.clone());
    tauri::async_runtime::spawn(async move {
        while !service.is_stopped() {
            let current = service.clone();
            if let Err(error) = crate::blocking(move || current.refresh(false)).await {
                log::debug!("Firewall status unavailable: {}", error.code);
            }
            tokio::time::sleep(std::time::Duration::from_secs(1)).await;
        }
    });
}
