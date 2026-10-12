use super::super::types::*;
use super::{
    bridge,
    control::{Reply, Request},
    hardware::Family,
};
use crate::AppError;
use std::sync::{Arc, Mutex};
pub trait Transport: Send {
    fn state(&self) -> FanHelperState;
    fn install(&mut self) -> Result<(), AppError>;
    fn uninstall(&mut self) -> Result<(), AppError>;
    fn exchange(&mut self, request: Request) -> Result<Reply, AppError>;
}
pub struct NativeTransport;
impl Transport for NativeTransport {
    fn state(&self) -> FanHelperState {
        #[cfg(target_os = "macos")]
        {
            match unsafe { bridge::on_service_status() } {
                0 | 3 => FanHelperState::NotInstalled,
                1 => FanHelperState::Enabled,
                2 => FanHelperState::RequiresApproval,
                _ => FanHelperState::Error,
            }
        }
        #[cfg(not(target_os = "macos"))]
        {
            FanHelperState::Unsupported
        }
    }
    fn install(&mut self) -> Result<(), AppError> {
        #[cfg(target_os = "macos")]
        {
            if unsafe { bridge::on_helper_bundled() } != 1 {
                return Err(AppError::new("E_FAN_HELPER_UNSIGNED", "helper_bundle"));
            }
            let code = unsafe { bridge::on_service_change(1) };
            // A daemon's first registration reports an error while macOS waits for the user's approval.
            if code == 0
                || matches!(
                    self.state(),
                    FanHelperState::RequiresApproval | FanHelperState::Enabled
                )
            {
                Ok(())
            } else {
                log::warn!("Fan helper registration failed: SMAppService error {code}");
                Err(AppError::new("E_FAN_HELPER_NOT_ENABLED", "register"))
            }
        }
        #[cfg(not(target_os = "macos"))]
        {
            Err(unavailable())
        }
    }
    fn uninstall(&mut self) -> Result<(), AppError> {
        #[cfg(target_os = "macos")]
        {
            let code = unsafe { bridge::on_service_change(0) };
            if code == 0 {
                Ok(())
            } else {
                log::warn!("Fan helper unregistration failed: SMAppService error {code}");
                Err(AppError::new("E_FAN_HELPER_NOT_ENABLED", "unregister"))
            }
        }
        #[cfg(not(target_os = "macos"))]
        {
            Err(unavailable())
        }
    }
    fn exchange(&mut self, request: Request) -> Result<Reply, AppError> {
        #[cfg(target_os = "macos")]
        unsafe {
            let json =
                std::ffi::CString::new(serde_json::to_string(&request).map_err(|_| unavailable())?)
                    .map_err(|_| unavailable())?;
            let response = bridge::on_xpc_exchange(json.as_ptr());
            if response.is_null() {
                return Err(unavailable());
            }
            let value = serde_json::from_slice::<Result<Reply, AppError>>(
                std::ffi::CStr::from_ptr(response).to_bytes(),
            )
            .map_err(|_| unavailable());
            bridge::on_bridge_free(response);
            value?
        }
        #[cfg(not(target_os = "macos"))]
        {
            let _ = request;
            Err(unavailable())
        }
    }
}
pub fn unavailable() -> AppError {
    AppError::new("E_FAN_HELPER_UNAVAILABLE", "xpc")
}
pub type Changed = Arc<dyn Fn(FanChange) + Send + Sync>;
pub struct Client<T: Transport> {
    pub transport: T,
    pub support: FanControlSupport,
    epoch: Option<u64>,
    sequence: u64,
    sink: Changed,
    sleep_manual: Vec<(u32, u32)>,
    session_started: bool,
    silent: bool,
    desired_manual: std::collections::BTreeMap<u32, u32>,
}
impl<T: Transport> Client<T> {
    pub fn new(transport: T, support: FanControlSupport, sink: Changed) -> Self {
        Self {
            transport,
            support,
            epoch: None,
            sequence: 0,
            sink,
            sleep_manual: vec![],
            session_started: false,
            silent: false,
            desired_manual: std::collections::BTreeMap::new(),
        }
    }
    pub fn allowed(&self) -> bool {
        super::super::manual_allowed(self.support)
    }
    fn enabled(&self) -> Result<(), AppError> {
        if self.transport.state() != FanHelperState::Enabled {
            Err(AppError::new("E_FAN_HELPER_NOT_ENABLED", "helper"))
        } else {
            Ok(())
        }
    }
    fn consume(&mut self, reply: &Reply) {
        if self.epoch != Some(reply.epoch) {
            self.sequence = 0;
            self.epoch = Some(reply.epoch);
        }
        for event in &reply.changes {
            if event.sequence > self.sequence {
                if matches!(
                    event.reason,
                    FanReason::HeartbeatLost
                        | FanReason::Overheat
                        | FanReason::WriteFailed
                        | FanReason::HelperStarted
                        | FanReason::HelperUninstalled
                        | FanReason::AppQuit
                ) {
                    self.desired_manual.clear();
                    self.sleep_manual.clear();
                }
                (self.sink)(FanChange {
                    fans: reply.fans.clone(),
                    reason: event.reason,
                });
                self.sequence = event.sequence;
            }
        }
        self.sequence = self.sequence.max(reply.sequence);
    }
    fn send(&mut self, request: Request) -> Result<Reply, AppError> {
        self.enabled()?;
        let reply = self.transport.exchange(request)?;
        self.consume(&reply);
        Ok(reply)
    }
    pub fn status(&mut self) -> Result<FanHelperStatus, AppError> {
        let mut status = self.transport.state();
        if self.support == FanControlSupport::Unsupported {
            status = FanHelperState::Unsupported;
        }
        if status == FanHelperState::Enabled {
            // Approved but not answering (it failed to start, or was replaced): say so instead of failing the query.
            let answer = self.start_session();
            if let Err(error) = &answer
                && !self.silent
            {
                log::warn!("Fan helper is enabled but not answering: {}", error.code);
            }
            self.silent = answer.is_err();
            if self.silent {
                status = FanHelperState::Error;
            }
        }
        Ok(FanHelperStatus {
            status,
            support: self.support,
            manual_allowed: self.allowed(),
        })
    }
    fn start_session(&mut self) -> Result<(), AppError> {
        self.poll()?;
        // A fresh App session always starts in automatic, including a still-running helper.
        if !self.session_started {
            self.send(Request::Reset {
                reason: FanReason::User,
            })?;
            self.session_started = true;
        }
        Ok(())
    }
    pub fn poll(&mut self) -> Result<Reply, AppError> {
        self.send(Request::Status {
            after: self.sequence,
            epoch: self.epoch,
        })
    }
    fn manual_gate(&self) -> Result<(), AppError> {
        if !self.allowed() {
            Err(AppError::new("E_FAN_UNSUPPORTED", "fan_control"))
        } else {
            Ok(())
        }
    }
    pub fn install(&mut self) -> Result<FanHelperStatus, AppError> {
        self.manual_gate()?;
        self.transport.install()?;
        self.status()
    }
    pub fn uninstall(&mut self) -> Result<(), AppError> {
        match self.transport.state() {
            FanHelperState::NotInstalled => return Ok(()),
            FanHelperState::Enabled => {
                match self.poll().and_then(|_| self.send(Request::Uninstall)) {
                    Ok(_) => {}
                    // Not answering: launchd stops it on unregister, and it hands the fans back on SIGTERM.
                    Err(error) if error.code == "E_FAN_HELPER_UNAVAILABLE" => {
                        log::warn!(
                            "Fan helper did not confirm the uninstall; unregistering anyway"
                        );
                    }
                    // It answered but could not restore automatic control: keep it, its watchdog keeps retrying.
                    Err(error) => return Err(error),
                }
            }
            _ => {}
        }
        self.transport.uninstall()?;
        self.session_started = false;
        Ok(())
    }
    pub fn mode(&mut self, id: Option<u32>, mode: FanMode) -> Result<Vec<FanState>, AppError> {
        if mode == FanMode::Manual {
            self.manual_gate()?;
        }
        self.status()?;
        self.poll()?;
        let reply = self.send(Request::SetMode { fan_id: id, mode })?;
        self.remember(&reply.fans);
        Ok(reply.fans)
    }
    pub fn speed(&mut self, id: Option<u32>, rpm: u32) -> Result<Vec<FanState>, AppError> {
        self.manual_gate()?;
        self.status()?;
        self.poll()?;
        let reply = self.send(Request::SetSpeed { fan_id: id, rpm })?;
        self.remember(&reply.fans);
        Ok(reply.fans)
    }
    fn remember(&mut self, fans: &[FanState]) {
        self.desired_manual = fans
            .iter()
            .filter(|f| f.mode == FanMode::Manual)
            .map(|f| {
                (
                    f.id,
                    f.target_rpm
                        .unwrap_or(f.rpm)
                        .clamp(f.min_rpm.max(1), f.max_rpm),
                )
            })
            .collect();
    }
    pub fn reset(&mut self, reason: FanReason) -> Result<(), AppError> {
        self.poll()?;
        self.send(Request::Reset { reason })?;
        Ok(())
    }
    pub fn sleep(&mut self) {
        if let Ok(reply) = self.poll() {
            if self.desired_manual.is_empty() {
                self.remember(&reply.fans);
            }
            self.sleep_manual = self
                .desired_manual
                .iter()
                .map(|(id, rpm)| (*id, *rpm))
                .collect();
            let _ = self.reset(FanReason::Sleep);
        }
    }
    pub fn wake(&mut self) {
        if self.poll().is_err() {
            return;
        }
        let targets = std::mem::take(&mut self.sleep_manual);
        if !self.allowed() || targets.is_empty() {
            return;
        }
        let mut fans = vec![];
        for (id, rpm) in targets {
            match self.speed(Some(id), rpm) {
                Ok(updated) => fans = updated,
                Err(_) => return,
            }
        }
        (self.sink)(FanChange {
            fans,
            reason: FanReason::WakeReapplied,
        });
    }
}
pub type FanClient = Arc<Mutex<Client<NativeTransport>>>;
pub fn install(app: &tauri::AppHandle, chip: &str, fan_count: usize) {
    use tauri::Manager;
    let ui = app.clone();
    let support = Family::from_chip(chip).support(fan_count);
    let client = Arc::new(Mutex::new(Client::new(
        NativeTransport,
        support,
        Arc::new(move |change| {
            use tauri_specta::Event;
            if let Some(sampler) = ui.try_state::<Arc<super::super::sampler::Sampler>>() {
                sampler.fan_reason(change.reason);
            }
            let _ = crate::events::FanChanged(change).emit(&ui);
        }),
    )));
    app.manage(client.clone());
    tauri::async_runtime::spawn(async move {
        loop {
            let handle = client.clone();
            let _ = crate::blocking(move || {
                let mut client = handle.lock().map_err(|_| unavailable())?;
                if client.transport.state() == FanHelperState::Enabled {
                    client.status()?;
                }
                Ok(())
            })
            .await;
            tokio::time::sleep(std::time::Duration::from_secs(5)).await;
        }
    });
}
pub fn power(app: &tauri::AppHandle, sleep: bool) {
    use tauri::Manager;
    if let Some(client) = app.try_state::<FanClient>() {
        let client = client.inner().clone();
        tauri::async_runtime::spawn_blocking(move || {
            if let Ok(mut client) = client.lock() {
                if sleep {
                    client.sleep();
                } else {
                    client.wake();
                }
            }
        });
    }
}
pub async fn shutdown<R: tauri::Runtime>(app: &tauri::AppHandle<R>) {
    use tauri::Manager;
    if let Some(client) = app.try_state::<FanClient>() {
        let client = client.inner().clone();
        let _ = crate::blocking(move || {
            let mut client = client.lock().map_err(|_| unavailable())?;
            if client.transport.state() == FanHelperState::Enabled {
                client.reset(FanReason::AppQuit)?;
            }
            Ok(())
        })
        .await;
    }
}
