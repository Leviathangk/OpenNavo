//! Fake-clock friendly transition tracking. Cooldown applies to each health key, including upgrades.
use super::types::*;
use crate::{AppError, model::Locale};
use std::collections::BTreeMap;
#[derive(Default)]
pub struct AlertTracker {
    enabled: bool,
    active: BTreeMap<String, u8>,
    sent: BTreeMap<String, u64>,
}
fn key(issue: &HealthIssue) -> String {
    serde_json::to_value(issue.key)
        .unwrap()
        .as_str()
        .unwrap()
        .to_owned()
}
impl AlertTracker {
    pub fn enabled(&self) -> bool {
        self.enabled
    }
    pub fn set_enabled(&mut self, enabled: bool) {
        if enabled != self.enabled {
            self.active.clear();
        }
        self.enabled = enabled;
    }
    pub fn evaluate(&mut self, health: &Health, now: u64) -> Vec<HealthIssue> {
        if !self.enabled {
            return vec![];
        }
        let mut active = BTreeMap::new();
        let mut emitted = vec![];
        for issue in &health.issues {
            let level = match issue.level {
                HealthIssueLevel::Info => 0,
                HealthIssueLevel::Attention => 1,
                HealthIssueLevel::Critical => 2,
            };
            let key = key(issue);
            active
                .entry(key.clone())
                .and_modify(|v: &mut u8| *v = (*v).max(level))
                .or_insert(level);
            if level > 0
                && level > self.active.get(&key).copied().unwrap_or(0)
                && self
                    .sent
                    .get(&key)
                    .is_none_or(|last| now.saturating_sub(*last) >= 3_600_000)
            {
                self.sent.insert(key, now);
                emitted.push(issue.clone());
            }
        }
        self.active = active;
        emitted
    }
}
pub fn route(key: HealthIssueKey) -> &'static str {
    if key == HealthIssueKey::DiskLow {
        "/cleanup"
    } else {
        "/monitor"
    }
}
pub fn content(issue: &HealthIssue, locale: Locale) -> Result<(String, String), AppError> {
    let key = if issue.key == HealthIssueKey::MemoryPressure
        && issue.level == HealthIssueLevel::Critical
    {
        "memory_critical".to_owned()
    } else {
        key(issue)
    };
    let title = crate::native_i18n::text(locale, &format!("monitorAlert.{key}.title"))?;
    let body = crate::native_i18n::text(locale, &format!("monitorAlert.{key}.body"))?;
    let value = f64::from(issue.value);
    let size = localized(locale, value, 0);
    let percent = localized(locale, value, 1);
    let temp = localized(locale, value, 2);
    Ok((
        title,
        body.replace("{size}", &size)
            .replace("{percent}", &percent)
            .replace("{temp}", &temp)
            .replace("{name}", issue.subject.as_deref().unwrap_or("")),
    ))
}
fn localized(locale: Locale, value: f64, kind: u32) -> String {
    #[cfg(target_os = "macos")]
    {
        unsafe extern "C" {
            fn on_monitor_format(
                locale: *const std::ffi::c_char,
                value: f64,
                kind: u32,
            ) -> *mut std::ffi::c_char;
        }
        let locale = match locale {
            Locale::ZhCn => c"zh-CN",
            Locale::EnUs => c"en-US",
            Locale::JaJp => c"ja-JP",
            Locale::EsEs => c"es-ES",
            Locale::PtBr => c"pt-BR",
            Locale::RuRu => c"ru-RU",
            Locale::DeDe => c"de-DE",
            Locale::FrFr => c"fr-FR",
            Locale::ItIt => c"it-IT",
            Locale::ZhHant => c"zh-TW",
        };
        let raw = unsafe { on_monitor_format(locale.as_ptr(), value, kind) };
        if !raw.is_null() {
            let s = unsafe { std::ffi::CStr::from_ptr(raw).to_string_lossy().into_owned() };
            unsafe { nix::libc::free(raw.cast()) };
            return s;
        }
    }
    let _ = locale;
    match kind {
        0 => format!("{value:.0} B"),
        1 => format!("{:.0}%", value * 100.0),
        _ => format!("{value:.0}\u{a0}°C"),
    }
}
