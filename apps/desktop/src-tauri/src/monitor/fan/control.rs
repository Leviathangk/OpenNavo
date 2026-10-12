//! The five-command helper protocol and fail-safe state machine, driven by an injected monotonic clock.
use super::{super::types::*, hardware::Hardware};
use crate::AppError;
use serde::{Deserialize, Serialize};
use std::collections::VecDeque;
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(
    tag = "command",
    rename_all = "camelCase",
    rename_all_fields = "camelCase",
    deny_unknown_fields
)]
pub enum Request {
    Status { after: u64, epoch: Option<u64> },
    SetMode { fan_id: Option<u32>, mode: FanMode },
    SetSpeed { fan_id: Option<u32>, rpm: u32 },
    Reset { reason: FanReason },
    Uninstall,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Change {
    pub sequence: u64,
    pub reason: FanReason,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Reply {
    pub epoch: u64,
    pub sequence: u64,
    pub fans: Vec<FanState>,
    pub changes: Vec<Change>,
    pub support: FanControlSupport,
}
pub struct Controller<H: Hardware> {
    pub hardware: H,
    pub support: FanControlSupport,
    epoch: u64,
    sequence: u64,
    journal: VecDeque<Change>,
    last_heartbeat: u64,
    hot_since: Option<u64>,
    temperature_at: Option<u64>,
    temperature: Option<f64>,
    restore_pending: bool,
    manual_active: bool,
}
impl<H: Hardware> Controller<H> {
    pub fn new(hardware: H, support: FanControlSupport, now: u64, epoch: u64) -> Self {
        let mut control = Self {
            hardware,
            support,
            epoch,
            sequence: 0,
            journal: VecDeque::new(),
            last_heartbeat: now,
            hot_since: None,
            temperature_at: None,
            temperature: None,
            restore_pending: false,
            manual_active: false,
        };
        control.recover(FanReason::HelperStarted);
        control
    }
    fn record(&mut self, reason: FanReason) {
        self.sequence = self.sequence.wrapping_add(1);
        if self.journal.len() == 64 {
            // Prefer dropping old user operations or redundant reasons, preserving every safety category.
            let index = self
                .journal
                .iter()
                .position(|e| e.reason == FanReason::User)
                .or_else(|| {
                    self.journal.iter().enumerate().find_map(|(i, e)| {
                        self.journal
                            .iter()
                            .skip(i + 1)
                            .any(|other| other.reason == e.reason)
                            .then_some(i)
                    })
                })
                .unwrap_or(0);
            self.journal.remove(index);
        }
        self.journal.push_back(Change {
            sequence: self.sequence,
            reason,
        });
    }
    pub fn recover(&mut self, reason: FanReason) {
        let redundant_sleep = reason == FanReason::Sleep
            && !self.manual_active
            && self
                .hardware
                .fans()
                .is_ok_and(|fans| fans.iter().all(|f| f.mode == FanMode::Auto));
        self.restore_pending = self.hardware.reset().is_err();
        self.manual_active = false;
        self.hot_since = None;
        if !redundant_sleep || self.restore_pending {
            self.record(reason);
        }
        if self.restore_pending && reason != FanReason::WriteFailed {
            self.record(FanReason::WriteFailed);
        }
    }
    fn targets(&mut self, id: Option<u32>) -> Result<Vec<FanState>, AppError> {
        let fans = self.hardware.fans()?;
        if let Some(id) = id {
            let fan = fans
                .into_iter()
                .find(|f| f.id == id)
                .ok_or_else(|| AppError::new("E_INVALID_ARG", "fan_id"))?;
            Ok(vec![fan])
        } else if fans.is_empty() {
            Err(AppError::new("E_INVALID_ARG", "no_fans"))
        } else {
            Ok(fans)
        }
    }
    pub fn request(
        &mut self,
        request: Request,
        now: u64,
        authorized: bool,
    ) -> Result<Reply, AppError> {
        if !authorized {
            return Err(AppError::new("E_FAN_UNAUTHORIZED", "audit_token"));
        }
        self.last_heartbeat = now;
        let mut after = 0;
        match request {
            Request::Status {
                after: cursor,
                epoch,
            } => {
                after = if epoch == Some(self.epoch) { cursor } else { 0 };
            }
            Request::SetMode { fan_id, mode } => {
                if mode == FanMode::Manual && !super::super::manual_allowed(self.support) {
                    return Err(AppError::new("E_FAN_UNSUPPORTED", "fan_control"));
                }
                if self.restore_pending {
                    return Err(super::super::smc::error());
                }
                let targets = self.targets(fan_id)?;
                if mode == FanMode::Manual {
                    self.validate_thermal(now)?;
                }
                for fan in targets {
                    let safe = fan
                        .target_rpm
                        .unwrap_or(fan.rpm)
                        .clamp(fan.min_rpm.max(1), fan.max_rpm);
                    if let Err(e) = self.hardware.set_mode(fan.id, mode).and_then(|_| {
                        if mode == FanMode::Manual {
                            self.hardware.set_speed(fan.id, safe)
                        } else {
                            Ok(())
                        }
                    }) {
                        self.recover(FanReason::WriteFailed);
                        return Err(e);
                    }
                }
                self.manual_active = match self.hardware.fans() {
                    Ok(fans) => fans.iter().any(|f| f.mode == FanMode::Manual),
                    Err(error) => {
                        self.recover(FanReason::WriteFailed);
                        return Err(error);
                    }
                };
                self.record(FanReason::User);
            }
            Request::SetSpeed { fan_id, rpm } => {
                if !super::super::manual_allowed(self.support) {
                    return Err(AppError::new("E_FAN_UNSUPPORTED", "fan_control"));
                }
                if self.restore_pending {
                    return Err(super::super::smc::error());
                }
                let targets = self.targets(fan_id)?;
                // Reject off; clamp a synchronized positive target independently to each system range.
                if rpm == 0
                    || targets
                        .iter()
                        .any(|f| f.max_rpm == 0 || f.min_rpm > f.max_rpm)
                {
                    return Err(AppError::new("E_INVALID_ARG", "fan_rpm"));
                }
                self.validate_thermal(now)?;
                self.manual_active = true;
                for fan in targets {
                    let target = rpm.clamp(fan.min_rpm.max(1), fan.max_rpm);
                    if let Err(e) = self
                        .hardware
                        .set_mode(fan.id, FanMode::Manual)
                        .and_then(|_| self.hardware.set_speed(fan.id, target))
                    {
                        self.recover(FanReason::WriteFailed);
                        return Err(e);
                    }
                }
                self.manual_active = true;
                self.record(FanReason::User);
            }
            Request::Reset { reason } => {
                if !matches!(
                    reason,
                    FanReason::User | FanReason::AppQuit | FanReason::Sleep
                ) {
                    return Err(AppError::new("E_INVALID_ARG", "fan_reset_reason"));
                }
                self.recover(reason);
                if self.restore_pending {
                    return Err(super::super::smc::error());
                }
            }
            Request::Uninstall => {
                self.recover(FanReason::HelperUninstalled);
                if self.restore_pending {
                    return Err(super::super::smc::error());
                }
            }
        }
        let reply = self.reply(after);
        if reply.is_err() && self.manual_active {
            self.recover(FanReason::WriteFailed);
        }
        reply
    }
    /// Heat never blocks manual control: boosting the fans is what it is for when the chip runs hot, and the watchdog
    /// keeps manual fans at full speed while it stays hot. Only an unreadable temperature leaves nothing to protect by.
    fn validate_thermal(&mut self, now: u64) -> Result<(), AppError> {
        let temperature = self.hardware.temperature().filter(|t| t.is_finite());
        self.temperature = temperature;
        self.temperature_at = Some(now);
        if temperature.is_none() {
            return Err(AppError::new("E_FAN_SENSOR_UNAVAILABLE", "thermal_safety"));
        }
        Ok(())
    }
    fn reply(&mut self, after: u64) -> Result<Reply, AppError> {
        Ok(Reply {
            epoch: self.epoch,
            sequence: self.sequence,
            fans: self.hardware.fans()?,
            changes: self
                .journal
                .iter()
                .filter(|c| c.sequence > after)
                .cloned()
                .collect(),
            support: self.support,
        })
    }
    pub fn watchdog(&mut self, now: u64) {
        if self.restore_pending {
            self.restore_pending = self.hardware.reset().is_err();
            return;
        }
        let manual = match self.hardware.fans() {
            Ok(fans) => fans.iter().any(|f| f.mode == FanMode::Manual),
            Err(_) => {
                if self.manual_active {
                    self.recover(FanReason::WriteFailed);
                }
                return;
            }
        };
        if !manual {
            self.hot_since = None;
            self.temperature_at = None;
            self.temperature = None;
            return;
        }
        if now.saturating_sub(self.last_heartbeat) >= 15000 {
            self.recover(FanReason::HeartbeatLost);
            return;
        }
        if self
            .temperature_at
            .is_none_or(|at| now.saturating_sub(at) >= 5000)
        {
            self.temperature = self.hardware.temperature().filter(|t| t.is_finite());
            self.temperature_at = Some(now);
            if self.temperature.is_none() {
                self.recover(FanReason::WriteFailed);
                return;
            }
        }
        if self.temperature.is_some_and(|t| t >= 95.0) {
            let start = *self.hot_since.get_or_insert(now);
            if now.saturating_sub(start) >= 10000 {
                self.overheat();
                return;
            }
        } else {
            self.hot_since = None;
        }
        if self.hardware.serious_pressure() {
            self.overheat();
        }
    }
    /// Too hot for a manual speed: manual fans below their maximum go to full speed and stay manual, so cooling only
    /// goes up and a boost the user chose is kept. A failed write hands the fans back to the system instead.
    fn overheat(&mut self) {
        let Ok(fans) = self.hardware.fans() else {
            self.recover(FanReason::WriteFailed);
            return;
        };
        let mut raised = false;
        // A target read back within 100 rpm of the maximum counts as full speed (no rewrite every second).
        for fan in fans.iter().filter(|f| {
            f.mode == FanMode::Manual
                && f.target_rpm.unwrap_or(f.rpm) < f.max_rpm.saturating_sub(100)
        }) {
            if self.hardware.set_speed(fan.id, fan.max_rpm).is_err() {
                self.recover(FanReason::WriteFailed);
                return;
            }
            raised = true;
        }
        if raised {
            self.record(FanReason::Overheat);
        }
    }
}
