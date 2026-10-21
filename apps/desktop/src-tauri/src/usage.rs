//! Opt-in running-installation DAU, including background/menu-bar operation.
//! Owned by Rust so hidden/suspended WebViews cannot stop the daily tick.
use crate::{AppError, core::DesktopCore};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use std::{
    collections::BTreeMap,
    sync::{Arc, atomic::Ordering},
    time::Duration,
};

pub const HISTORY_KEY: &str = "usage_history_v1";
const WEEK: i64 = 7 * 86_400_000;

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct Activity {
    anonymous_id: String,
    platform: String,
    occurred_at: DateTime<Utc>,
    version: String,
    locale: String,
}
#[derive(Debug, Serialize, Deserialize)]
struct History {
    id: String,
    sent: BTreeMap<String, String>,
    pending: Vec<Activity>,
}
fn day(now: DateTime<Utc>) -> String {
    (now + chrono::Duration::hours(8))
        .format("%Y-%m-%d")
        .to_string()
}
impl History {
    fn load(core: &DesktopCore) -> Result<Self, AppError> {
        Ok(core
            .database
            .meta(HISTORY_KEY)?
            .and_then(|value| serde_json::from_str(&value).ok())
            .unwrap_or_else(|| Self {
                id: uuid::Uuid::new_v4().to_string(),
                sent: BTreeMap::new(),
                pending: Vec::new(),
            }))
    }
    fn record(&mut self, now: DateTime<Utc>, version: &str, locale: &str) {
        let cutoff = now - chrono::Duration::milliseconds(WEEK);
        self.pending
            .retain(|a| a.occurred_at >= cutoff && a.occurred_at <= now);
        self.sent
            .retain(|date, _| date >= &day(cutoff) && date <= &day(now));
        let date = day(now);
        if self.sent.get(&date).is_some_and(|v| v == version) {
            return;
        }
        if self
            .pending
            .iter()
            .any(|a| day(a.occurred_at) == date && a.version == version)
        {
            return;
        }
        self.pending.retain(|a| day(a.occurred_at) != date);
        self.pending.push(Activity {
            anonymous_id: self.id.clone(),
            platform: "desktop".into(),
            occurred_at: now,
            version: version.into(),
            locale: locale.into(),
        });
    }
    fn acknowledge(&mut self, activity: &Activity, reporting_day: &str) {
        self.sent
            .insert(reporting_day.to_owned(), activity.version.clone());
        self.pending
            .retain(|a| a.occurred_at != activity.occurred_at || a.version != activity.version);
    }
    fn save(&self, core: &DesktopCore) -> Result<(), AppError> {
        core.database
            .set_meta(HISTORY_KEY, &serde_json::to_string(self)?)
    }
}

async fn tick(
    core: &DesktopCore,
    client: &reqwest::Client,
    url: &str,
    now: DateTime<Utc>,
    version: &str,
) -> Result<(), AppError> {
    let epoch;
    let mut history;
    {
        let settings = core
            .settings
            .read()
            .map_err(|_| AppError::new("E_UNKNOWN", "settings_poisoned"))?;
        if !settings.usage_analytics {
            // Also finish clearing history after a crash between saving opt-out and removing the outbox.
            if core
                .database
                .meta(HISTORY_KEY)?
                .is_some_and(|value| value != "null")
            {
                core.database.set_meta(HISTORY_KEY, "null")?;
            }
            return Ok(());
        }
        epoch = core.usage_epoch.load(Ordering::Acquire);
        history = History::load(core)?;
        history.record(now, version, settings.locale.as_str());
        history.save(core)?;
    }
    for activity in history.pending.clone() {
        if core.usage_epoch.load(Ordering::Acquire) != epoch {
            break;
        }
        let request = client.post(url).json(&activity).send();
        let response = tokio::select! {
            result = request => result?,
            _ = core.usage_changed.notified() => break,
        };
        if response.status() != reqwest::StatusCode::NO_CONTENT {
            break;
        }
        let Some(acknowledged_day) = response
            .headers()
            .get("X-Usage-Day")
            .and_then(|value| value.to_str().ok())
            .filter(|value| {
                value.len() == 10 && chrono::NaiveDate::parse_from_str(value, "%Y-%m-%d").is_ok()
            })
        else {
            // Older/malformed responses cannot safely acknowledge a client-clock date.
            break;
        };
        let settings = core
            .settings
            .read()
            .map_err(|_| AppError::new("E_UNKNOWN", "settings_poisoned"))?;
        if !settings.usage_analytics || core.usage_epoch.load(Ordering::Acquire) != epoch {
            break;
        }
        history.acknowledge(&activity, acknowledged_day);
        history.save(core)?;
    }
    Ok(())
}

pub fn start(core: &Arc<DesktopCore>, version: String) {
    // Development and test processes are not product usage.
    if cfg!(debug_assertions) {
        return;
    }
    let weak = Arc::downgrade(core);
    tauri::async_runtime::spawn(async move {
        let Ok(client) = reqwest::Client::builder()
            .timeout(Duration::from_secs(15))
            .redirect(reqwest::redirect::Policy::none())
            .build()
        else {
            return;
        };
        let url = format!(
            "{}/usage/activity",
            crate::catalog::sync::configured_base().trim_end_matches('/')
        );
        loop {
            let Some(core) = weak.upgrade() else {
                break;
            };
            if core.queue.is_stopping() {
                break;
            }
            // Errors are deliberately silent; the bounded outbox retries next minute.
            let _ = tick(&core, &client, &url, Utc::now(), &version).await;
            tokio::select! {
                _ = tokio::time::sleep(Duration::from_secs(60)) => {},
                _ = core.usage_changed.notified() => {},
            }
        }
    });
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::TimeZone;

    #[test]
    fn background_daily_dedup_midnight_version_and_bounded_offline_history() {
        let mut h = History {
            id: uuid::Uuid::new_v4().to_string(),
            sent: BTreeMap::new(),
            pending: vec![],
        };
        let start = Utc.with_ymd_and_hms(2026, 10, 22, 15, 59, 0).unwrap();
        h.record(start, "0.1.3", "en-US");
        h.record(start + chrono::Duration::seconds(30), "0.1.3", "en-US");
        assert_eq!(h.pending.len(), 1);
        h.record(start + chrono::Duration::minutes(1), "0.1.3", "en-US");
        assert_eq!(h.pending.len(), 2);
        h.record(start + chrono::Duration::minutes(2), "0.1.4", "en-US");
        assert_eq!(h.pending.len(), 2);
        assert_eq!(h.pending[1].version, "0.1.4");
        h.sent.insert(day(start), "0.1.3".into());
        h.pending.remove(0);
        h.record(start, "0.1.3", "en-US");
        assert!(h.pending.is_empty()); // future-dated outbox entries are discarded after clock rollback
        for n in 0..20 {
            h.record(start + chrono::Duration::days(n), "0.1.4", "en-US");
        }
        assert!(h.pending.len() <= 8);
    }

    #[test]
    fn server_day_acknowledgement_does_not_suppress_real_midnight() {
        let mut h = History {
            id: uuid::Uuid::new_v4().to_string(),
            sent: BTreeMap::new(),
            pending: vec![],
        };
        // Actual Shanghai 23:58; the installation clock reads 00:01.
        let client = Utc.with_ymd_and_hms(2026, 10, 22, 16, 1, 0).unwrap();
        h.record(client, "0.1.3", "en-US");
        h.acknowledge(&h.pending[0].clone(), "2026-10-22");
        assert!(!h.sent.contains_key("2026-10-23"));
        h.record(client + chrono::Duration::minutes(2), "0.1.3", "en-US");
        assert_eq!(h.pending.len(), 1);
        h.acknowledge(&h.pending[0].clone(), "2026-10-23");
        h.record(client + chrono::Duration::minutes(3), "0.1.3", "en-US");
        assert!(h.pending.is_empty());
    }

    #[tokio::test]
    async fn opt_out_never_creates_an_identity_or_connects() {
        let tmp = tempfile::tempdir().unwrap();
        let core = DesktopCore::open(
            tmp.path().join("data"),
            tmp.path().join("cache"),
            tmp.path().join("askpass"),
            Arc::new(|_| {}),
        )
        .unwrap();
        assert!(!core.current_settings().unwrap().usage_analytics);
        tick(
            &core,
            &reqwest::Client::new(),
            "http://127.0.0.1:1/usage/activity",
            Utc::now(),
            "0.1.3",
        )
        .await
        .unwrap();
        assert!(core.database.meta(HISTORY_KEY).unwrap().is_none());
    }
    #[tokio::test]
    async fn background_report_survives_restart_and_opt_out_clears_pending_data() {
        use std::io::{Read, Write};
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!("http://{}/usage/activity", listener.local_addr().unwrap());
        let server = std::thread::spawn(move || {
            let (mut stream, _) = listener.accept().unwrap();
            stream
                .set_read_timeout(Some(Duration::from_secs(5)))
                .unwrap();
            let mut bytes = Vec::new();
            let mut buf = [0u8; 4096];
            loop {
                let n = stream.read(&mut buf).unwrap();
                assert!(n > 0);
                bytes.extend_from_slice(&buf[..n]);
                if let Some(end) = bytes.windows(4).position(|w| w == b"\r\n\r\n") {
                    let header = String::from_utf8_lossy(&bytes[..end]).to_lowercase();
                    let length: usize = header
                        .lines()
                        .find_map(|line| line.strip_prefix("content-length:"))
                        .unwrap()
                        .trim()
                        .parse()
                        .unwrap();
                    if bytes.len() >= end + 4 + length {
                        let body: serde_json::Value =
                            serde_json::from_slice(&bytes[end + 4..end + 4 + length]).unwrap();
                        assert_eq!(body["platform"], "desktop");
                        assert_eq!(body.as_object().unwrap().len(), 5);
                        let at: DateTime<Utc> =
                            body["occurredAt"].as_str().unwrap().parse().unwrap();
                        stream.write_all(format!("HTTP/1.1 204 No Content\r\nX-Usage-Day: {}\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", day(at)).as_bytes()).unwrap();
                        return body["anonymousId"].as_str().unwrap().to_owned();
                    }
                }
            }
        });
        let tmp = tempfile::tempdir().unwrap();
        let open = || {
            DesktopCore::open(
                tmp.path().join("data"),
                tmp.path().join("cache"),
                tmp.path().join("askpass"),
                Arc::new(|_| {}),
            )
            .unwrap()
        };
        let core = open();
        let mut settings = core.current_settings().unwrap();
        settings.usage_analytics = true;
        core.replace_settings(settings).await.unwrap();
        let now = Utc::now();
        let client = reqwest::Client::new();
        tick(&core, &client, &url, now, "0.1.3").await.unwrap();
        let id = server.join().unwrap();
        let restarted = open();
        assert_eq!(History::load(&restarted).unwrap().id, id);
        // The fixture server has closed. A duplicate network attempt would fail this tick.
        tick(&restarted, &client, &url, now, "0.1.3").await.unwrap();
        assert!(History::load(&restarted).unwrap().pending.is_empty());
        let mut settings = restarted.current_settings().unwrap();
        settings.usage_analytics = false;
        restarted.replace_settings(settings).await.unwrap();
        assert_eq!(
            restarted.database.meta(HISTORY_KEY).unwrap().as_deref(),
            Some("null")
        );
        tick(&restarted, &client, &url, now, "0.1.3").await.unwrap();
        assert_eq!(
            restarted.database.meta(HISTORY_KEY).unwrap().as_deref(),
            Some("null")
        );
    }
}
