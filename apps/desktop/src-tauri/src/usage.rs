//! Automatic running-installation DAU, including background/menu-bar operation.
//! Owned by Rust so hidden/suspended WebViews cannot stop the daily tick.
use crate::{AppError, core::DesktopCore};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use std::{collections::BTreeMap, sync::Arc, time::Duration};

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
        self.forget(activity);
    }
    /// Remove one activity the server refuses to ever accept. Keeping it at the head of the
    /// outbox would stop every later record from being sent.
    fn forget(&mut self, activity: &Activity) {
        self.pending
            .retain(|a| a.occurred_at != activity.occurred_at || a.version != activity.version);
    }
    fn save(&self, core: &DesktopCore) -> Result<(), AppError> {
        core.database
            .set_meta(HISTORY_KEY, &serde_json::to_string(self)?)
    }
}

/// One activity's result, which decides whether the tick keeps going.
enum Attempt {
    /// The server accepted the activity and named the day to acknowledge.
    Acknowledged(String),
    /// The server checked the record and refused it with 400. The same record fails the same way on
    /// every later tick, so keeping it would block everything behind it.
    Rejected,
    /// The service is unavailable, not the record: transport error, 5xx, 429, or a server too old to
    /// name the reporting day. The batch is kept for the next minute.
    Unavailable(AppError),
}

async fn attempt(client: &reqwest::Client, url: &str, activity: &Activity) -> Attempt {
    static REDIRECT_NOTICED: std::sync::Once = std::sync::Once::new();
    let response = match client.post(url).json(activity).send().await {
        Ok(response) => response,
        Err(error) => return Attempt::Unavailable(error.into()),
    };
    if response.url().as_str() != url {
        // A redirect in front of the API is followed, bounded like the catalog sync. Say it once, not
        // once a minute, so a changed endpoint stays visible without filling the log.
        REDIRECT_NOTICED.call_once(|| {
            log::info!("usage endpoint redirected to {}", response.url());
        });
    }
    let status = response.status();
    if status == reqwest::StatusCode::BAD_REQUEST {
        return Attempt::Rejected;
    }
    if status != reqwest::StatusCode::NO_CONTENT {
        return Attempt::Unavailable(
            AppError::new("E_NETWORK", "usage_unavailable")
                .with_detail(format!("status {}", status.as_u16())),
        );
    }
    match response
        .headers()
        .get("X-Usage-Day")
        .and_then(|value| value.to_str().ok())
        .filter(|value| {
            value.len() == 10 && chrono::NaiveDate::parse_from_str(value, "%Y-%m-%d").is_ok()
        }) {
        Some(day) => Attempt::Acknowledged(day.to_owned()),
        None => Attempt::Unavailable(AppError::new("E_NETWORK", "usage_day_missing")),
    }
}

async fn tick(
    core: &DesktopCore,
    client: &reqwest::Client,
    url: &str,
    now: DateTime<Utc>,
    version: &str,
) -> Result<(), AppError> {
    let mut history;
    {
        let settings = core
            .settings
            .read()
            .map_err(|_| AppError::new("E_UNKNOWN", "settings_poisoned"))?;
        history = History::load(core)?;
        history.record(now, version, settings.locale.as_str());
        history.save(core)?;
    }
    for activity in history.pending.clone() {
        match attempt(client, url, &activity).await {
            Attempt::Acknowledged(day) => {
                history.acknowledge(&activity, &day);
                history.save(core)?;
            }
            // Retaining a refused record repeats the same refusal every minute, and while it sits in
            // the outbox nothing behind it is sent: one unsendable record, such as an activity older
            // than the server's seven-day window or newer than its five-minute skew allowance, would
            // end this installation's reporting for good once the local window prunes the rest.
            // Dropping just that record keeps the remaining days and every later tick working.
            Attempt::Rejected => {
                log::warn!(
                    "usage activity rejected: occurred_at={} version={}",
                    activity.occurred_at.to_rfc3339(),
                    activity.version
                );
                history.forget(&activity);
                history.save(core)?;
            }
            Attempt::Unavailable(error) => {
                log::warn!(
                    "usage report unavailable: {} {}",
                    error.code,
                    error.detail.as_deref().unwrap_or(&error.message)
                );
                return Ok(());
            }
        }
    }
    Ok(())
}

/// What the usage client does with one redirect, given the status, the scheme the chain started on,
/// the scheme it is being sent to, and how many requests the chain has already made.
#[derive(Debug, PartialEq, Eq)]
enum Redirect {
    Follow,
    Stop,
    TooMany,
}

/// The usage endpoint carries the activity body on the request, so only a redirect that keeps it can
/// be followed: 307 and 308 preserve the method and body, while 301, 302 and 303 rewrite the POST
/// into a GET and drop the payload (tower-http follow_redirect), leaving a second hop that carries no
/// activity at all. Those are returned as-is, so `attempt` keeps the whole batch for the next tick.
///
/// A redirect may not change the scheme. The initial URL comes from `configured_base()`, which accepts
/// `http` for a self-hosted development server, so requiring `https` outright would break that setup;
/// what must never happen is a server choosing to take an `https` endpoint down to plaintext, which
/// would put the device identifier and the activity records on the wire in clear. Keeping the scheme
/// while changing host or path is fine, which is also what `Policy::limited` allowed.
///
/// An `http` to `https` upgrade is refused as well, and that is deliberate rather than a side effect:
/// "the scheme must not change" is one comparison that needs no knowledge of which scheme is safe, and
/// a local proxy that upgrades would be a configuration error the user should see and fix at
/// `configured_base()`, not one the client silently works around once per user.
///
/// The chain follows five redirects and refuses the sixth request's redirect, matching
/// `Policy::limited(5)`. `previous()` holds the initial URL followed by one entry per request already
/// made, so it is 1 on the first redirect and `Policy::limited(5)`'s `previous().len() > 5` is
/// `requests > 5` here — `>= 5` would allow four. The updater's `>= 5` does exactly that, which is a
/// separate pre-existing bug outside this change.
fn redirect(
    status: reqwest::StatusCode,
    initial: Option<&str>,
    target: &str,
    requests: usize,
) -> Redirect {
    let keeps_body = status == reqwest::StatusCode::TEMPORARY_REDIRECT
        || status == reqwest::StatusCode::PERMANENT_REDIRECT;
    if !keeps_body || initial != Some(target) {
        Redirect::Stop
    } else if requests > 5 {
        Redirect::TooMany
    } else {
        Redirect::Follow
    }
}

/// The redirect policy, shared by the shipped client and the tests; keeping it in one place is what
/// makes those tests cover the policy that actually ships.
fn client_builder() -> reqwest::ClientBuilder {
    reqwest::Client::builder()
        .timeout(Duration::from_secs(15))
        .redirect(reqwest::redirect::Policy::custom(|attempt| {
            // `previous()[0]` is the initial URL, not a redirection, so the first scheme is the one the
            // configured base used.
            let initial = attempt.previous().first().map(|url| url.scheme());
            match redirect(
                attempt.status(),
                initial,
                attempt.url().scheme(),
                attempt.previous().len(),
            ) {
                Redirect::Follow => attempt.follow(),
                Redirect::Stop => attempt.stop(),
                Redirect::TooMany => attempt.error("too many usage redirects"),
            }
        }))
}

fn build_client() -> Result<reqwest::Client, AppError> {
    Ok(client_builder().build()?)
}

pub fn start(core: &Arc<DesktopCore>, version: String) {
    // Development and test processes are not product usage.
    if cfg!(debug_assertions) {
        return;
    }
    let weak = Arc::downgrade(core);
    tauri::async_runtime::spawn(async move {
        let Ok(client) = build_client() else {
            log::warn!("usage client could not be created");
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
            // A failed tick keeps the outbox for the next minute; the log line is what makes a run of
            // failures visible, since nothing else in the app depends on this report.
            if let Err(error) = tick(&core, &client, &url, Utc::now(), &version).await {
                log::warn!("usage tick failed: {} ({})", error.code, error.message);
            }
            tokio::time::sleep(Duration::from_secs(60)).await;
        }
    });
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::TimeZone;
    use std::io::{Read, Write};

    /// An HTTP fixture that answers up to `requests` connections and records the request bodies, so
    /// a test can see exactly which activities the client sent. It stops early rather than waiting
    /// forever for a request the client no longer makes.
    fn serve(
        requests: usize,
        answer: impl Fn(usize, &serde_json::Value) -> String + Send + 'static,
    ) -> (String, std::thread::JoinHandle<Vec<serde_json::Value>>) {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        listener.set_nonblocking(true).unwrap();
        let url = format!("http://{}/usage/activity", listener.local_addr().unwrap());
        let server = std::thread::spawn(move || {
            let mut bodies = Vec::new();
            let deadline = std::time::Instant::now() + Duration::from_secs(5);
            while bodies.len() < requests && std::time::Instant::now() < deadline {
                let mut stream = match listener.accept() {
                    Ok((stream, _)) => stream,
                    Err(_) => {
                        std::thread::sleep(Duration::from_millis(5));
                        continue;
                    }
                };
                stream.set_nonblocking(false).unwrap();
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
                            stream
                                .write_all(answer(bodies.len(), &body).as_bytes())
                                .unwrap();
                            bodies.push(body);
                            break;
                        }
                    }
                }
            }
            bodies
        });
        (url, server)
    }

    fn no_content(at: &serde_json::Value) -> String {
        let at: DateTime<Utc> = at["occurredAt"].as_str().unwrap().parse().unwrap();
        format!(
            "HTTP/1.1 204 No Content\r\nX-Usage-Day: {}\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
            day(at)
        )
    }

    fn open_core(tmp: &tempfile::TempDir) -> Arc<DesktopCore> {
        DesktopCore::open(
            tmp.path().join("data"),
            tmp.path().join("cache"),
            tmp.path().join("askpass"),
            Arc::new(|_| {}),
        )
        .unwrap()
    }

    #[tokio::test]
    async fn a_rejected_activity_is_dropped_and_the_batch_continues() {
        // The server refuses a record dated in its future, as it does for a clock running more than
        // five minutes ahead. The same record fails the same way on every later tick, so it must not
        // stand between the day behind it and the server.
        let (url, server) = serve(2, |index, body| {
            assert_eq!(body.as_object().unwrap().len(), 5);
            if index == 0 {
                "HTTP/1.1 400 Bad Request\r\nContent-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}".to_owned()
            } else {
                no_content(body)
            }
        });
        let tmp = tempfile::tempdir().unwrap();
        let core = open_core(&tmp);
        let now = Utc.with_ymd_and_hms(2026, 10, 22, 15, 59, 30).unwrap();
        let id = History::load(&core).unwrap().id;
        // Today's record is the one the server refuses (a clock running ahead of the server), and the
        // record for the day before it is still waiting behind it.
        let pending = vec![
            Activity {
                anonymous_id: id.clone(),
                platform: "desktop".into(),
                occurred_at: a_moment_earlier(now),
                version: "0.1.4".into(),
                locale: "en-US".into(),
            },
            Activity {
                anonymous_id: id.clone(),
                platform: "desktop".into(),
                occurred_at: now - chrono::Duration::days(3),
                version: "0.1.3".into(),
                locale: "en-US".into(),
            },
        ];
        History {
            id,
            sent: BTreeMap::new(),
            pending,
        }
        .save(&core)
        .unwrap();

        let client = build_client().unwrap();
        tick(&core, &client, &url, now, "0.1.4").await.unwrap();
        let delivered = server.join().unwrap();
        assert_eq!(
            delivered.len(),
            2,
            "the refused record did not end the batch"
        );
        assert_eq!(
            delivered[1]["version"], "0.1.3",
            "the record behind the rejection was sent"
        );
        let history = History::load(&core).unwrap();
        assert!(
            !history.pending.iter().any(|a| a.version == "0.1.3"),
            "the delivered record is acknowledged"
        );
        assert!(
            !history.pending.iter().any(|a| a.version == "0.1.4"),
            "the refused record is dropped rather than retried every minute"
        );

        // The next tick records the day again with its current timestamp instead of repeating the same
        // refusal, which is what keeps this installation reporting at all.
        let (url, server) = serve(1, |_, body| no_content(body));
        tick(
            &core,
            &client,
            &url,
            now + chrono::Duration::minutes(1),
            "0.1.4",
        )
        .await
        .unwrap();
        let delivered = server.join().unwrap();
        assert_eq!(
            delivered.len(),
            1,
            "the next tick is not stuck on the refused record"
        );
        assert_eq!(delivered[0]["version"], "0.1.4");
        let next = now + chrono::Duration::minutes(1);
        let at: DateTime<Utc> = delivered[0]["occurredAt"]
            .as_str()
            .unwrap()
            .parse()
            .unwrap();
        assert!(
            at >= next && at <= next + chrono::Duration::minutes(5),
            "the next report carries the current time"
        );
        assert!(History::load(&core).unwrap().pending.is_empty());
    }

    /// A status the retry classifier treats as "the service, not the record". The client must keep the
    /// whole outbox and try again on the next tick instead of dropping anything.
    async fn an_unavailable_status_keeps_the_whole_batch(status: &'static str) {
        let (url, server) = serve(1, move |_, _| {
            format!("HTTP/1.1 {status}\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
        });
        let tmp = tempfile::tempdir().unwrap();
        let core = open_core(&tmp);
        // A fixed clock, not `Utc::now()`, and deliberately one minute before the local day boundary:
        // `day()` is UTC+8, so 15:59:30Z is 23:59:30 local and the second tick a minute later is the
        // first tick of the next reporting day. That makes the midnight case the one this test always
        // covers, instead of a minute a night that only a build landing in that minute would see. The
        // assertion after the second tick therefore allows a record for the new day; what it requires
        // is that nothing that was waiting before it is still queued.
        let now: DateTime<Utc> = "2026-10-22T15:59:30Z".parse().unwrap();
        seed_two_days(&core, now);
        let client = build_client().unwrap();
        tick(&core, &client, &url, now, "0.1.4").await.unwrap();
        let delivered = server.join().unwrap();
        assert_eq!(
            delivered.len(),
            1,
            "an unavailable service stops the batch at the first record"
        );
        let history = History::load(&core).unwrap();
        assert!(
            history.sent.is_empty(),
            "no record is acknowledged by a {status}"
        );
        // Both records are still there: today's, and the one behind it the client never reached.
        for version in ["0.1.4", "0.1.3"] {
            assert!(
                history.pending.iter().any(|a| a.version == version),
                "{version} is kept for the next tick after {status}"
            );
        }

        // The next tick sends both, so nothing was lost by waiting.
        let (url, server) = serve(2, |_, body| no_content(body));
        tick(
            &core,
            &client,
            &url,
            now + chrono::Duration::minutes(1),
            "0.1.4",
        )
        .await
        .unwrap();
        let delivered = server.join().unwrap();
        assert_eq!(
            delivered.len(),
            2,
            "the next tick resends the whole batch after {status}"
        );
        // Nothing that was waiting is still waiting. Asserted as "no activity predates this tick"
        // rather than `pending.is_empty()`: `record()` opens a fresh activity for a new reporting day,
        // so a run that crosses local midnight legitimately leaves one behind, and an empty-outbox
        // assertion would fail for a whole minute each night without anything being wrong.
        let next = now + chrono::Duration::minutes(1);
        let history = History::load(&core).unwrap();
        assert!(
            history.pending.iter().all(|a| a.occurred_at >= next),
            "every record that was waiting is delivered: {:?}",
            history.pending
        );
    }

    #[tokio::test]
    async fn a_server_failure_keeps_the_whole_batch() {
        an_unavailable_status_keeps_the_whole_batch("503 Service Unavailable").await;
    }

    /// A 429 with the header a rate-limiting proxy sends: the batch still waits, and the next tick
    /// sends both records once the server stops asking for less traffic.
    #[tokio::test]
    async fn a_rate_limited_endpoint_keeps_the_batch() {
        let (url, server) = serve(1, |_, _| {
            "HTTP/1.1 429 Too Many Requests\r\nRetry-After: 30\r\nContent-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}".to_owned()
        });
        let tmp = tempfile::tempdir().unwrap();
        let core = open_core(&tmp);
        // The same clock and the same reason as the helper above: this run always crosses the local
        // reporting-day boundary, so the midnight case is covered on every run rather than once a night.
        let now: DateTime<Utc> = "2026-10-22T15:59:30Z".parse().unwrap();
        seed_two_days(&core, now);
        let client = build_client().unwrap();
        // A rate-limited tick is a successful tick: it reports what it could not send and returns.
        tick(&core, &client, &url, now, "0.1.4").await.unwrap();
        server.join().unwrap();
        let history = History::load(&core).unwrap();
        assert!(history.sent.is_empty(), "a 429 acknowledges no record");
        for version in ["0.1.4", "0.1.3"] {
            assert!(
                history.pending.iter().any(|a| a.version == version),
                "{version} is still waiting after a 429"
            );
        }

        let (url, server) = serve(2, |_, body| no_content(body));
        let next = now + chrono::Duration::minutes(1);
        tick(&core, &client, &url, next, "0.1.4").await.unwrap();
        let delivered = server.join().unwrap();
        assert_eq!(
            delivered.len(),
            2,
            "the next tick resends the whole batch after a 429"
        );
        // Same assertion as the helper above, for the same reason: a record for a new reporting day is
        // legitimate, so what must hold is that nothing from before this tick is still queued.
        let history = History::load(&core).unwrap();
        assert!(
            history.pending.iter().all(|a| a.occurred_at >= next),
            "every record that was waiting is delivered: {:?}",
            history.pending
        );
    }

    #[tokio::test]
    async fn a_record_the_server_rejects_is_not_sent_again_on_the_next_tick() {
        // USAGE-039: one record the server will never accept must not become permanent silence. The
        // first tick drops it, and the next tick must not repeat it: it reports the current day only.
        let (url, server) = serve(1, |_, _| {
            "HTTP/1.1 400 Bad Request\r\nContent-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}".to_owned()
        });
        let tmp = tempfile::tempdir().unwrap();
        let core = open_core(&tmp);
        let now = Utc.with_ymd_and_hms(2026, 10, 22, 15, 59, 30).unwrap();
        seed_two_days(&core, now);
        let client = build_client().unwrap();
        tick(&core, &client, &url, now, "0.1.4").await.unwrap();
        server.join().unwrap();
        let after_rejection = History::load(&core).unwrap();
        assert!(
            !after_rejection.pending.iter().any(|a| a.version == "0.1.4"),
            "the rejected record is dropped at once"
        );
        assert!(
            after_rejection.pending.iter().any(|a| a.version == "0.1.3"),
            "the record behind the rejection is still waiting"
        );

        // Reject everything the next tick sends, so a repeated bad record would show up as another 400
        // instead of being hidden by an acknowledgement.
        let (url, server) = serve(2, |_, _| {
            "HTTP/1.1 400 Bad Request\r\nContent-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}".to_owned()
        });
        let next = now + chrono::Duration::minutes(1);
        tick(&core, &client, &url, next, "0.1.4").await.unwrap();
        let delivered = server.join().unwrap();
        let rejected_at =
            a_moment_earlier(now).to_rfc3339_opts(chrono::SecondsFormat::Millis, true);
        // The rejected record is this run's `0.1.4` at `now`; the only `0.1.4` the next tick may send is
        // the fresh record it writes for the new day, dated at `next` or later.
        assert!(
            !delivered.iter().any(|body| {
                body["version"] == "0.1.4"
                    && body["occurredAt"]
                        .as_str()
                        .unwrap()
                        .parse::<DateTime<Utc>>()
                        .unwrap()
                        < next
            }),
            "the rejected 0.1.4 record at {rejected_at} is not sent again"
        );
        assert!(
            delivered.iter().any(|body| body["version"] == "0.1.3"),
            "the record behind the rejected one is reached on the next tick"
        );
        // Reporting resumes with a fresh record for the current day, which is what USAGE-039 protects:
        // dropping the one unaccepted record must not end this installation's reporting.
        assert!(
            delivered.iter().any(|body| body["version"] == "0.1.4"),
            "the next tick still reports the current day"
        );
    }

    /// One second before `now`, except in the first second of a local reporting day, where that lands
    /// on the previous day. Tests seed "today's record" this way, and `record()` replaces pending
    /// entries of its own day, so a seed that crossed the boundary would be counted as a third day and
    /// change what the test asserts. The clamp keeps the seed on `now`'s day at every wall-clock time.
    fn a_moment_earlier(now: DateTime<Utc>) -> DateTime<Utc> {
        let earlier = now - chrono::Duration::seconds(1);
        if day(earlier) == day(now) {
            earlier
        } else {
            now
        }
    }

    /// The seed is "a moment before now" so it can never be mistaken for a fresh record of the same
    /// tick, but one second before the first second of a local day is the *previous* day, and the
    /// caller would then be seeding a third day. Both branches are pinned here because every test
    /// that seeds at `15:59:30Z` exercises only the first one, which would let the clamp rot unseen.
    #[test]
    fn a_seeded_record_stays_on_the_ticks_reporting_day() {
        let midday = Utc.with_ymd_and_hms(2026, 10, 22, 6, 0, 0).unwrap();
        assert_eq!(
            a_moment_earlier(midday),
            midday - chrono::Duration::seconds(1),
            "anywhere but the first second of the day, the seed is one second earlier"
        );
        let boundary = Utc.with_ymd_and_hms(2026, 10, 22, 16, 0, 0).unwrap();
        assert_ne!(
            day(boundary - chrono::Duration::seconds(1)),
            day(boundary),
            "this case only means something if one second earlier really is the previous day"
        );
        assert_eq!(
            day(a_moment_earlier(boundary)),
            day(boundary),
            "the clamp keeps the seed on the tick's own reporting day"
        );
    }

    /// A stored outbox whose first record is today's and whose second is three days old, so a test
    /// can see whether a failure at the head leaves the record behind it alone.
    fn seed_two_days(core: &DesktopCore, now: DateTime<Utc>) -> String {
        let id = History::load(core).unwrap().id;
        let pending = vec![
            Activity {
                anonymous_id: id.clone(),
                platform: "desktop".into(),
                occurred_at: a_moment_earlier(now),
                version: "0.1.4".into(),
                locale: "en-US".into(),
            },
            Activity {
                anonymous_id: id.clone(),
                platform: "desktop".into(),
                occurred_at: now - chrono::Duration::days(3),
                version: "0.1.3".into(),
                locale: "en-US".into(),
            },
        ];
        History {
            id: id.clone(),
            sent: BTreeMap::new(),
            pending,
        }
        .save(core)
        .unwrap();
        id
    }

    /// The second hop a client may or may not make, as the target listener saw it.
    struct Hop {
        method: String,
        content_length: Option<usize>,
        content_type: Option<String>,
        body: Vec<u8>,
    }

    /// Wait for a connection until `deadline`; `None` means nothing arrived in the window. The accepted
    /// stream is switched back to blocking: on macOS it inherits the listener's non-blocking flag.
    fn accept_until(
        listener: &std::net::TcpListener,
        deadline: std::time::Instant,
    ) -> Option<std::net::TcpStream> {
        loop {
            match listener.accept() {
                Ok((stream, _)) => {
                    stream.set_nonblocking(false).unwrap();
                    return Some(stream);
                }
                Err(_) if std::time::Instant::now() < deadline => {
                    std::thread::sleep(Duration::from_millis(5));
                }
                Err(_) => return None,
            }
        }
    }

    /// A one-hop redirect on a POST. The target answers any request with a 204 that names a day, so a
    /// followed hop would be acknowledged; the handle reports what the target saw, or `None` when the
    /// client never connected. Nothing here blocks past `WINDOW`, so a client that refuses to follow
    /// fails an assertion instead of hanging the test.
    ///
    /// `target_scheme` is the scheme written into `Location`. The client only follows when it matches
    /// the scheme the chain started on, so `"http"` exercises the follow path and `"https"` exercises
    /// the refusal: the target here is a plain listener either way, which is the point — a refused
    /// redirect must never reach it.
    fn redirect_once(
        status: &'static str,
        target_scheme: &'static str,
    ) -> (String, std::thread::JoinHandle<Option<Hop>>) {
        let target = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        target.set_nonblocking(true).unwrap();
        let target_url = format!(
            "{target_scheme}://{}/usage/activity",
            target.local_addr().unwrap()
        );
        serve_redirect(status, target_url, target)
    }

    /// Serve one request with a redirect, then report what the target listener saw.
    fn serve_redirect(
        status: &'static str,
        target_url: String,
        target: std::net::TcpListener,
    ) -> (String, std::thread::JoinHandle<Option<Hop>>) {
        const WINDOW: Duration = Duration::from_secs(5);
        let closure_target_url = target_url;
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        listener.set_nonblocking(true).unwrap();
        let url = format!("http://{}/usage/activity", listener.local_addr().unwrap());
        let server = std::thread::spawn(move || {
            let deadline = std::time::Instant::now() + WINDOW;
            let mut stream = accept_until(&listener, deadline)?;
            stream.set_read_timeout(Some(WINDOW)).unwrap();
            let mut buf = [0u8; 4096];
            let _ = stream.read(&mut buf).unwrap();
            stream
                .write_all(
                    format!(
                        "HTTP/1.1 {status}\r\nLocation: {closure_target_url}\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
                    )
                    .as_bytes(),
                )
                .unwrap();
            // Give the client until the window ends to make a second hop it should not make.
            let mut stream = accept_until(&target, deadline)?;
            stream.set_read_timeout(Some(WINDOW)).unwrap();
            let mut bytes = Vec::new();
            loop {
                // A request with no body needs no more reads: a followed 301/302/303 arrives here as a
                // GET with no Content-Length, and waiting for a body it will never send would end in a
                // read timeout rather than the assertion this test is about.
                if let Some(end) = bytes.windows(4).position(|w| w == b"\r\n\r\n") {
                    let header = String::from_utf8_lossy(&bytes[..end]).to_lowercase();
                    let content_length: Option<usize> = header
                        .lines()
                        .find_map(|line| line.strip_prefix("content-length:"))
                        .map(|value| value.trim().parse().unwrap());
                    let content_type = header
                        .lines()
                        .find_map(|line| line.strip_prefix("content-type:"))
                        .map(|value| value.trim().to_owned());
                    let body_starts = end + 4;
                    let complete = match content_length {
                        Some(length) => bytes.len() >= body_starts + length,
                        None => true,
                    };
                    if complete {
                        let body = bytes[body_starts..].to_vec();
                        let day: serde_json::Value = serde_json::from_slice(&body).unwrap_or_else(
                            |_| serde_json::json!({ "occurredAt": "2026-11-02T00:00:00Z" }),
                        );
                        stream.write_all(no_content(&day).as_bytes()).unwrap();
                        return Some(Hop {
                            method: header.split(' ').next().unwrap().to_owned(),
                            content_length,
                            content_type,
                            body,
                        });
                    }
                }
                match stream.read(&mut buf) {
                    Ok(0) => return None,
                    Ok(n) => bytes.extend_from_slice(&buf[..n]),
                    Err(error) => panic!("the target could not read the request: {error}"),
                }
            }
        });
        (url, server)
    }

    /// The redirect decision on its own, so the table is pinned without a socket. `requests` is
    /// `previous().len()`: 1 on the first redirect, because `previous()[0]` is the initial URL.
    #[test]
    fn only_a_307_or_308_that_keeps_the_scheme_is_followed() {
        for (name, status, initial, target, requests, want) in [
            (
                "307 keeps the body and the scheme",
                reqwest::StatusCode::TEMPORARY_REDIRECT,
                "https",
                "https",
                1,
                Redirect::Follow,
            ),
            (
                "308 keeps the body and the scheme",
                reqwest::StatusCode::PERMANENT_REDIRECT,
                "https",
                "https",
                1,
                Redirect::Follow,
            ),
            (
                "a plaintext base follows a plaintext redirect, which is the development setup",
                reqwest::StatusCode::TEMPORARY_REDIRECT,
                "http",
                "http",
                1,
                Redirect::Follow,
            ),
            (
                "the fifth redirect is still followed",
                reqwest::StatusCode::TEMPORARY_REDIRECT,
                "https",
                "https",
                5,
                Redirect::Follow,
            ),
            (
                "the sixth redirect is refused, so there is no seventh request",
                reqwest::StatusCode::TEMPORARY_REDIRECT,
                "https",
                "https",
                6,
                Redirect::TooMany,
            ),
            (
                "301 would rewrite the POST into a GET",
                reqwest::StatusCode::MOVED_PERMANENTLY,
                "https",
                "https",
                1,
                Redirect::Stop,
            ),
            (
                "302 would rewrite the POST into a GET",
                reqwest::StatusCode::FOUND,
                "https",
                "https",
                1,
                Redirect::Stop,
            ),
            (
                "303 would rewrite the POST into a GET",
                reqwest::StatusCode::SEE_OTHER,
                "https",
                "https",
                1,
                Redirect::Stop,
            ),
        ] {
            assert_eq!(
                redirect(status, Some(initial), target, requests),
                want,
                "{name}"
            );
        }
    }

    /// A redirect may not change the scheme. The dangerous direction is `https` down to plaintext,
    /// which puts the device identifier and the records on the wire in clear; `http` up to `https` is
    /// also refused, because the rule is "no change", not "prefer https". Refusing must not be
    /// confused with the loop bound, which reports `TooMany` instead.
    #[test]
    fn a_redirect_that_changes_the_scheme_is_not_followed() {
        for (name, initial, target) in [
            ("a downgrade to plaintext", "https", "http"),
            ("an upgrade to https", "http", "https"),
            ("another scheme entirely", "https", "ftp"),
        ] {
            for status in [
                reqwest::StatusCode::TEMPORARY_REDIRECT,
                reqwest::StatusCode::PERMANENT_REDIRECT,
            ] {
                assert_eq!(
                    redirect(status, Some(initial), target, 1),
                    Redirect::Stop,
                    "{name} is refused ({status})"
                );
            }
        }
    }

    /// 307 keeps the method and the body, so the second hop carries the activity and its 204 is a
    /// true acknowledgement.
    #[tokio::test]
    async fn a_307_redirect_is_followed_as_a_post() {
        let (url, server) = redirect_once("307 Temporary Redirect", "http");
        let client = build_client().unwrap();
        let tmp = tempfile::tempdir().unwrap();
        let core = open_core(&tmp);
        let now = Utc.with_ymd_and_hms(2026, 10, 22, 15, 59, 30).unwrap();
        tick(&core, &client, &url, now, "0.1.4").await.unwrap();
        let hop = server
            .join()
            .unwrap()
            .expect("a 307 keeps the POST, so it is followed");
        assert_eq!(hop.method, "post", "the second hop is still a POST");
        assert_eq!(
            hop.content_length,
            Some(hop.body.len()),
            "the activity body is carried with a matching Content-Length"
        );
        assert_eq!(
            hop.content_type.as_deref(),
            Some("application/json"),
            "the JSON content type survives the redirect"
        );
        let sent: serde_json::Value = serde_json::from_slice(&hop.body).unwrap();
        assert_eq!(
            sent["version"], "0.1.4",
            "the second hop carries the real activity"
        );
        let history = History::load(&core).unwrap();
        assert!(
            history.pending.is_empty(),
            "the redirected 204 acknowledges the record"
        );
        assert_eq!(history.sent.len(), 1, "the day is recorded as reported");
    }

    /// 308 is the permanent form of 307 and behaves the same.
    #[tokio::test]
    async fn a_308_redirect_is_followed_as_a_post() {
        let (url, server) = redirect_once("308 Permanent Redirect", "http");
        let client = build_client().unwrap();
        let tmp = tempfile::tempdir().unwrap();
        let core = open_core(&tmp);
        let now = Utc.with_ymd_and_hms(2026, 10, 22, 15, 59, 30).unwrap();
        tick(&core, &client, &url, now, "0.1.4").await.unwrap();
        let hop = server
            .join()
            .unwrap()
            .expect("a 308 keeps the POST, so it is followed");
        assert_eq!(hop.method, "post", "the second hop is still a POST");
        assert_eq!(
            hop.content_length,
            Some(hop.body.len()),
            "the activity body is carried with a matching Content-Length"
        );
        assert_eq!(
            hop.content_type.as_deref(),
            Some("application/json"),
            "the JSON content type survives the redirect"
        );
        // 308 is the permanent form of 307: the payload itself has to arrive unchanged, not merely
        // some body of the right length.
        let sent: serde_json::Value = serde_json::from_slice(&hop.body).unwrap();
        assert_eq!(
            sent["version"], "0.1.4",
            "the second hop carries the real activity"
        );
        assert!(
            sent["anonymousId"].is_string() && sent["occurredAt"].is_string(),
            "every field of the activity reaches the target, not just the version"
        );
        let history = History::load(&core).unwrap();
        assert!(
            history.pending.is_empty(),
            "the redirected 204 acknowledges the record"
        );
        assert_eq!(history.sent.len(), 1, "the day is recorded as reported");
    }

    /// 301 rewrites the POST into a GET and drops the payload, so following it would let a 204 for
    /// that GET mark the day as reported while the server never saw the record.
    #[tokio::test]
    async fn a_301_redirect_is_not_followed() {
        let (url, server) = redirect_once("301 Moved Permanently", "http");
        let tmp = tempfile::tempdir().unwrap();
        let core = open_core(&tmp);
        let now = Utc.with_ymd_and_hms(2026, 10, 22, 15, 59, 30).unwrap();
        seed_two_days(&core, now);
        let client = build_client().unwrap();
        tick(&core, &client, &url, now, "0.1.4").await.unwrap();
        assert!(
            server.join().unwrap().is_none(),
            "a 301 must not be followed: the target sees zero connections"
        );
        let history = History::load(&core).unwrap();
        assert!(history.sent.is_empty(), "a 301 acknowledges nothing");
        assert_eq!(history.pending.len(), 2, "a 301 keeps the whole batch");
    }

    /// 302 is the temporary form of 301 and is equally lossy for a POST.
    #[tokio::test]
    async fn a_302_redirect_is_not_followed() {
        let (url, server) = redirect_once("302 Found", "http");
        let tmp = tempfile::tempdir().unwrap();
        let core = open_core(&tmp);
        let now = Utc.with_ymd_and_hms(2026, 10, 22, 15, 59, 30).unwrap();
        seed_two_days(&core, now);
        let client = build_client().unwrap();
        tick(&core, &client, &url, now, "0.1.4").await.unwrap();
        assert!(
            server.join().unwrap().is_none(),
            "a 302 must not be followed: the target sees zero connections"
        );
        let history = History::load(&core).unwrap();
        assert!(history.sent.is_empty(), "a 302 acknowledges nothing");
        assert_eq!(history.pending.len(), 2, "a 302 keeps the whole batch");
    }

    /// 303 always becomes a bodyless GET, even for a POST.
    #[tokio::test]
    async fn a_303_redirect_is_not_followed() {
        let (url, server) = redirect_once("303 See Other", "http");
        let tmp = tempfile::tempdir().unwrap();
        let core = open_core(&tmp);
        let now = Utc.with_ymd_and_hms(2026, 10, 22, 15, 59, 30).unwrap();
        seed_two_days(&core, now);
        let client = build_client().unwrap();
        tick(&core, &client, &url, now, "0.1.4").await.unwrap();
        assert!(
            server.join().unwrap().is_none(),
            "a 303 must not be followed: the target sees zero connections"
        );
        let history = History::load(&core).unwrap();
        assert!(history.sent.is_empty(), "a 303 acknowledges nothing");
        assert_eq!(history.pending.len(), 2, "a 303 keeps the whole batch");
    }

    /// The base here is `http` and the redirect names `https`, and a 307 would otherwise keep the
    /// body. Only the scheme rule refuses it, which is why this is the case that proves the rule: a
    /// server must not get to move the endpoint to a different scheme than the configured base used.
    /// The reverse direction, `https` down to plaintext, is the one that leaks; it is covered by the
    /// decision table, because an `https` origin would need a TLS server just to be reached.
    #[tokio::test]
    async fn a_307_redirect_to_a_different_scheme_is_not_followed() {
        let (url, server) = redirect_once("307 Temporary Redirect", "https");
        let tmp = tempfile::tempdir().unwrap();
        let core = open_core(&tmp);
        let now = Utc.with_ymd_and_hms(2026, 10, 22, 15, 59, 30).unwrap();
        seed_two_days(&core, now);
        let client = build_client().unwrap();
        tick(&core, &client, &url, now, "0.1.4").await.unwrap();
        let history = History::load(&core).unwrap();
        assert!(
            history.sent.is_empty(),
            "a redirect that changes the scheme must not acknowledge the records"
        );
        assert_eq!(
            history.pending.len(),
            2,
            "a refused scheme change keeps the whole batch"
        );
        assert!(
            server.join().unwrap().is_none(),
            "the target on the other scheme sees zero connections"
        );
    }

    /// 308 is the permanent form of 307 and is refused for the same reason: the scheme changed from
    /// the base's `http` to `https`. That direction is an upgrade, not a leak — it is refused only
    /// because the rule is "the scheme must not change", which is what makes it checkable without
    /// knowing which scheme is safe. The dangerous direction is the reverse one.
    #[tokio::test]
    async fn a_308_redirect_to_a_different_scheme_is_not_followed() {
        let (url, server) = redirect_once("308 Permanent Redirect", "https");
        let tmp = tempfile::tempdir().unwrap();
        let core = open_core(&tmp);
        let now = Utc.with_ymd_and_hms(2026, 10, 22, 15, 59, 30).unwrap();
        seed_two_days(&core, now);
        let client = build_client().unwrap();
        tick(&core, &client, &url, now, "0.1.4").await.unwrap();
        let history = History::load(&core).unwrap();
        assert!(
            history.sent.is_empty(),
            "a redirect that changes the scheme must not acknowledge the records"
        );
        assert_eq!(
            history.pending.len(),
            2,
            "a refused scheme change keeps the whole batch"
        );
        assert!(
            server.join().unwrap().is_none(),
            "the target on the other scheme sees zero connections"
        );
    }

    /// The regression this guards: the target is willing to answer 204, and a followed 301 would
    /// collect that 204 and mark a record the server never received as sent. Because the client does
    /// not follow, the 204 is never reached and `sent` stays empty.
    #[tokio::test]
    async fn a_301_target_that_would_answer_204_is_never_reached() {
        let (url, server) = redirect_once("301 Moved Permanently", "http");
        let tmp = tempfile::tempdir().unwrap();
        let core = open_core(&tmp);
        let now = Utc.with_ymd_and_hms(2026, 10, 22, 15, 59, 30).unwrap();
        seed_two_days(&core, now);
        let client = build_client().unwrap();
        tick(&core, &client, &url, now, "0.1.4").await.unwrap();
        // Assert the data outcome first: what matters is that no record is marked sent, and a present
        // `Hop` below would otherwise mask that assertion when the client wrongly follows.
        let history = History::load(&core).unwrap();
        assert!(
            history.sent.is_empty(),
            "a 204 behind a 301 must not write sent"
        );
        let hop = server.join().unwrap();
        assert!(
            hop.is_none(),
            "the 204-answering target is never contacted, so no acknowledgement can be forged"
        );
        assert_eq!(
            history.pending.len(),
            2,
            "both records wait for the next tick"
        );
    }

    /// A 307 that points at itself would otherwise be followed until the tick's timeout. The bound
    /// returns an error at the sixth request, records stay put, and nothing panics. The scheme never
    /// changes here, so the loop reaches the bound instead of being stopped by the scheme rule.
    #[tokio::test]
    async fn a_redirect_loop_stops_at_the_bound() {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!("http://{}/usage/activity", listener.local_addr().unwrap());
        let location = url.clone();
        listener.set_nonblocking(true).unwrap();
        let server = std::thread::spawn(move || {
            let mut hops = 0;
            // Stop at the window's end: the client is expected to give up after six requests, so
            // waiting for a seventh would hang the test instead of failing it.
            let deadline = std::time::Instant::now() + Duration::from_secs(5);
            while let Some(mut stream) = accept_until(&listener, deadline) {
                stream
                    .set_read_timeout(Some(Duration::from_secs(5)))
                    .unwrap();
                let mut buf = [0u8; 4096];
                let _ = stream.read(&mut buf).unwrap();
                hops += 1;
                stream
                    .write_all(
                        format!(
                            "HTTP/1.1 307 Temporary Redirect\r\nLocation: {location}\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
                        )
                        .as_bytes(),
                    )
                    .unwrap();
            }
            hops
        });
        let tmp = tempfile::tempdir().unwrap();
        let core = open_core(&tmp);
        let now = Utc.with_ymd_and_hms(2026, 10, 22, 15, 59, 30).unwrap();
        seed_two_days(&core, now);
        let client = build_client().unwrap();
        tick(&core, &client, &url, now, "0.1.4").await.unwrap();
        let hops = server.join().unwrap();
        // `previous()` holds the initial URL plus one entry per request made, so the sixth request is
        // the one whose redirect is refused with `attempt.error("too many usage redirects")`; that is
        // `Policy::limited(5)` exactly, which allows five jumps rather than four.
        assert_eq!(
            hops, 6,
            "the bound allows five redirects and refuses the sixth"
        );
        let history = History::load(&core).unwrap();
        assert!(
            history.sent.is_empty(),
            "a redirect loop acknowledges nothing"
        );
        assert_eq!(
            history.pending.len(),
            2,
            "a redirect loop keeps the whole batch for the next tick"
        );
    }

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
    async fn automatic_background_report_survives_restart_and_legacy_disabled_setting() {
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
        // Old persisted opt-out is no longer a supported setting.
        let mut legacy = serde_json::to_value(core.current_settings().unwrap()).unwrap();
        legacy["usageAnalytics"] = serde_json::json!(false);
        core.replace_settings(serde_json::from_value(legacy).unwrap())
            .await
            .unwrap();
        let now = Utc.with_ymd_and_hms(2026, 10, 22, 15, 59, 30).unwrap();
        let client = reqwest::Client::new();
        tick(&core, &client, &url, now, "0.1.3").await.unwrap();
        let id = server.join().unwrap();
        let restarted = open();
        assert_eq!(History::load(&restarted).unwrap().id, id);
        // The fixture server has closed. A duplicate network attempt would fail this tick.
        tick(&restarted, &client, &url, now, "0.1.3").await.unwrap();
        assert!(History::load(&restarted).unwrap().pending.is_empty());
        assert!(
            serde_json::to_value(restarted.current_settings().unwrap())
                .unwrap()
                .get("usageAnalytics")
                .is_none()
        );
    }
}
