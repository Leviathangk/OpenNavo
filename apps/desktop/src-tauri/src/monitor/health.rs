use super::{
    collect::{Modules, number},
    types::*,
};
#[derive(Default)]
pub struct HealthTracker {
    busy_since: Option<u64>,
    hot_since: Option<u64>,
    protection: bool,
}
impl HealthTracker {
    pub fn reset(&mut self) {
        self.busy_since = None;
        self.hot_since = None;
    }
    pub fn fan_reason(&mut self, reason: FanReason) {
        self.protection = match reason {
            FanReason::Overheat => true,
            // These are helper/control events ending the forced maximum-RPM mode.
            // A sampler pause (screen/sleep/lock) only calls reset(), not fan_reason().
            FanReason::User
            | FanReason::WakeReapplied
            | FanReason::HeartbeatLost
            | FanReason::WriteFailed
            | FanReason::HelperStarted
            | FanReason::HelperUninstalled
            | FanReason::AppQuit
            | FanReason::Sleep => false,
        };
    }
    pub fn evaluate(
        &mut self,
        sample: &MonitorSample,
        available: &Modules,
        elapsed_ms: u64,
        booted_at: i64,
    ) -> Health {
        let mut issues = Vec::new();
        let top = |cpu: bool| {
            sample
                .processes
                .as_ref()
                .and_then(|items| {
                    items.iter().filter(|p| !p.is_self).max_by(|a, b| {
                        if cpu {
                            f64::from(a.cpu).total_cmp(&f64::from(b.cpu))
                        } else {
                            a.memory_bytes.cmp(&b.memory_bytes)
                        }
                    })
                })
                .map(|p| p.pid)
        };
        let mut add = |key, level, value, top_pid| {
            issues.push(HealthIssue {
                key,
                level,
                value: number(value),
                top_pid,
                subject: None,
            })
        };
        if available.contains(&MonitorModule::Cpu) && f64::from(sample.cpu.usage) > 0.85 {
            let since = *self.busy_since.get_or_insert(elapsed_ms);
            if elapsed_ms.saturating_sub(since) >= 180_000 {
                add(
                    HealthIssueKey::CpuBusy,
                    HealthIssueLevel::Attention,
                    f64::from(sample.cpu.usage),
                    top(true),
                );
            }
        } else {
            self.busy_since = None;
        }
        let temperature = available
            .contains(&MonitorModule::Sensors)
            .then(|| {
                sample.sensors.as_ref().and_then(|s| {
                    s.iter()
                        .filter(|s| s.group == SensorGroup::Cpu)
                        .map(|s| f64::from(s.celsius))
                        .reduce(f64::max)
                })
            })
            .flatten();
        if self.protection {
            add(
                HealthIssueKey::Temperature,
                HealthIssueLevel::Critical,
                temperature.unwrap_or(95.0),
                top(true),
            );
        }
        if let Some(t) = temperature.filter(|t| *t > 95.0) {
            let since = *self.hot_since.get_or_insert(elapsed_ms);
            if !self.protection && elapsed_ms.saturating_sub(since) >= 10_000 {
                add(
                    HealthIssueKey::Temperature,
                    HealthIssueLevel::Attention,
                    t,
                    top(true),
                );
            }
        } else {
            self.hot_since = None;
        }
        if available.contains(&MonitorModule::Memory) {
            match sample.memory.pressure {
                MemoryPressure::Warning => add(
                    HealthIssueKey::MemoryPressure,
                    HealthIssueLevel::Attention,
                    1.0,
                    top(false),
                ),
                MemoryPressure::Critical => add(
                    HealthIssueKey::MemoryPressure,
                    HealthIssueLevel::Critical,
                    1.0,
                    top(false),
                ),
                MemoryPressure::Normal => {}
            }
        }
        if available.contains(&MonitorModule::Disk)
            && let Some(vol) = sample.disk.volumes.iter().find(|v| v.mount_point == "/")
        {
            if vol.free_bytes < 5_000_000_000 {
                add(
                    HealthIssueKey::DiskLow,
                    HealthIssueLevel::Critical,
                    vol.free_bytes as f64,
                    None,
                );
            } else if vol.free_bytes < 20_000_000_000
                || (vol.total_bytes > 0 && vol.free_bytes as f64 / (vol.total_bytes as f64) < 0.1)
            {
                add(
                    HealthIssueKey::DiskLow,
                    HealthIssueLevel::Attention,
                    vol.free_bytes as f64,
                    None,
                );
            }
        }
        if available.contains(&MonitorModule::Battery)
            && let Some(b) = &sample.battery
        {
            if let Some(h) = b.health_percent
                && f64::from(h) < 0.8
            {
                add(
                    HealthIssueKey::BatteryHealth,
                    HealthIssueLevel::Attention,
                    f64::from(h),
                    None,
                );
            }
            if let Some(t) = b.temperature_c
                && f64::from(t) > 40.0
            {
                add(
                    HealthIssueKey::BatteryHot,
                    HealthIssueLevel::Attention,
                    f64::from(t),
                    None,
                );
            }
        }
        let days = (sample.at.saturating_sub(booted_at).max(0) as f64) / 86_400_000.0;
        if days > 30.0 {
            add(HealthIssueKey::Uptime, HealthIssueLevel::Info, days, None);
        }
        if available.contains(&MonitorModule::Disk)
            && let Some(devices) = &sample.disk.devices
        {
            for d in devices
                .iter()
                .filter(|d| d.smart == DiskSmartStatus::Failing)
            {
                issues.push(HealthIssue {
                    key: HealthIssueKey::DiskSmart,
                    level: HealthIssueLevel::Critical,
                    value: number(1.0),
                    top_pid: None,
                    subject: Some(d.name.clone()),
                });
            }
        }
        let level = if issues.iter().any(|i| i.level == HealthIssueLevel::Critical) {
            HealthLevel::Critical
        } else if issues
            .iter()
            .any(|i| i.level == HealthIssueLevel::Attention)
        {
            HealthLevel::Attention
        } else {
            HealthLevel::Good
        };
        Health { level, issues }
    }
}
