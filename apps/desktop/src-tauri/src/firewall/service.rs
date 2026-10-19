// SPDX-License-Identifier: GPL-3.0-only
use super::{
    bridge::{self, Bridge},
    *,
};
use crate::{AppError, db::Database};
use opennavo_firewall_core::{
    engine::Snapshot,
    matching::{MAX_LIST_BYTES, parse_blocklist},
};
use serde::de::DeserializeOwned;
use serde_json::{Value, json};
use std::{
    collections::HashMap,
    io::Read,
    path::PathBuf,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};

#[derive(Debug, Clone)]
pub enum Update {
    Status(FirewallStatus),
    Alerts(Vec<FirewallAlert>),
    Changed(FirewallChangeKind),
    Activity(Vec<FirewallEvent>),
}
pub type Sink = Arc<dyn Fn(Update) + Send + Sync>;
#[derive(Clone)]
struct Configuration {
    id: String,
    options: FirewallEnableOptions,
}
struct Cache {
    status: FirewallStatus,
    snapshot: Option<Snapshot>,
    pending_options: Option<Configuration>,
    activity: HashMap<String, FirewallEvent>,
    last_activity: Instant,
}
impl Default for Cache {
    fn default() -> Self {
        Self {
            status: FirewallStatus::not_bundled(),
            snapshot: None,
            pending_options: None,
            activity: HashMap::new(),
            last_activity: Instant::now(),
        }
    }
}
pub struct Service {
    stopped: AtomicBool,
    bridge: Arc<dyn Bridge>,
    cache: Mutex<Cache>,
    refresh_gate: Mutex<()>,
    sink: Sink,
    icons: Option<(Database, PathBuf)>,
    icon_cache: Mutex<HashMap<String, Option<String>>>,
}
impl Service {
    pub fn new(bridge: Arc<dyn Bridge>, sink: Sink, icons: Option<(Database, PathBuf)>) -> Self {
        Self {
            stopped: AtomicBool::new(false),
            bridge,
            cache: Mutex::new(Cache::default()),
            refresh_gate: Mutex::new(()),
            sink,
            icons,
            icon_cache: Mutex::new(HashMap::new()),
        }
    }
    pub fn shutdown(&self) {
        self.stopped.store(true, Ordering::Release);
        self.bridge.disconnect();
    }
    pub fn is_stopped(&self) -> bool {
        self.stopped.load(Ordering::Acquire)
    }
    fn call<T: DeserializeOwned>(&self, request: Value) -> Result<T, AppError> {
        if self.is_stopped() {
            return Err(failure(FirewallErrorCode::Disconnected));
        }
        let value = self.bridge.request(request)?;
        if self.is_stopped() {
            self.bridge.disconnect();
            return Err(failure(FirewallErrorCode::Disconnected));
        }
        serde_json::from_value(value).map_err(|_| failure(FirewallErrorCode::Disconnected))
    }
    fn available(&self) -> Result<FirewallStatus, AppError> {
        let status = bridge::status(self.bridge.as_ref())?;
        match status.availability {
            FirewallAvailability::Available => Ok(status),
            FirewallAvailability::NotInApplications => {
                Err(failure(FirewallErrorCode::NotInApplications))
            }
            _ => Err(failure(FirewallErrorCode::NotBundled)),
        }
    }
    pub fn refresh(&self, force_alerts: bool) -> Result<FirewallStatus, AppError> {
        let _gate = self
            .refresh_gate
            .lock()
            .map_err(|_| failure(FirewallErrorCode::Disconnected))?;
        if self.is_stopped() {
            return Err(failure(FirewallErrorCode::Disconnected));
        }
        let mut status = bridge::status(self.bridge.as_ref()).unwrap_or_else(|error| {
            let mut status = self
                .cache
                .lock()
                .map(|c| c.status.clone())
                .unwrap_or_else(|_| FirewallStatus::not_bundled());
            status.connected = false;
            status.pending_alerts = 0;
            status.error = Some(FirewallFailure {
                code: FirewallErrorCode::Disconnected,
                detail: error.detail,
            });
            status
        });
        let mut snapshot = None;
        if status.availability == FirewallAvailability::Available
            && status.extension == FirewallExtensionState::Installed
            && status.filter != FirewallFilterState::NeedsConsent
        {
            match self.call::<Snapshot>(json!({"op":"snapshot"})) {
                Ok(mut data) => {
                    // A successful authenticated snapshot is proof of recovery,
                    // including after a stale native status/operation timeout.
                    if status
                        .error
                        .as_ref()
                        .is_some_and(|e| e.code == FirewallErrorCode::Disconnected)
                    {
                        status.error = None;
                    }
                    let options = self
                        .cache
                        .lock()
                        .map_err(|_| failure(FirewallErrorCode::Disconnected))?
                        .pending_options
                        .clone();
                    if let Some(configuration) = options {
                        let acknowledged = data.configuration.as_deref() == Some(&configuration.id);
                        let settings = FirewallSettings {
                            mode: data.settings.mode,
                            allow_apple: configuration.options.allow_apple,
                            allow_installed: configuration.options.allow_installed,
                        };
                        // Do not resend while baseline preparation is in flight.
                        // Lost acknowledgements are reconciled via the stable ID.
                        let accepted = if acknowledged {
                            true
                        } else if data.preparing {
                            false
                        } else {
                            match self.call::<Value>(
                                json!({"op":"configure","id":configuration.id,"settings":settings}),
                            ) {
                                Ok(value) => value["accepted"] == true,
                                Err(error) => {
                                    status.error = Some(FirewallFailure {
                                        code: if error.code == "E_FW_DISCONNECTED" {
                                            FirewallErrorCode::Disconnected
                                        } else {
                                            FirewallErrorCode::Activation
                                        },
                                        detail: error.detail,
                                    });
                                    false
                                }
                            }
                        };
                        if accepted {
                            let mut cache = self
                                .cache
                                .lock()
                                .map_err(|_| failure(FirewallErrorCode::Disconnected))?;
                            if cache
                                .pending_options
                                .as_ref()
                                .is_some_and(|pending| pending.id == configuration.id)
                            {
                                cache.pending_options = None;
                            }
                        }
                        // No scanning occurs in configure, so this stays a short RPC.
                        if accepted
                            && !acknowledged
                            && let Ok(current) = self.call(json!({"op":"snapshot"}))
                        {
                            data = current;
                        }
                    }
                    for alert in &mut data.alerts {
                        self.icon(&mut alert.app);
                    }
                    for event in &mut data.events {
                        self.icon(&mut event.app);
                    }
                    status.connected = true;
                    status.preparing = data.preparing;
                    status.mode = data.settings.mode;
                    status.rule_count = data.rule_count;
                    status.pending_alerts = data.alerts.len() as u64;
                    status.blocked_today = data.blocked_today;
                    snapshot = Some(data);
                }
                Err(error) => {
                    if status.filter == FirewallFilterState::On && status.error.is_none() {
                        status.error = Some(FirewallFailure {
                            code: FirewallErrorCode::Disconnected,
                            detail: error.detail,
                        });
                    }
                }
            }
        }
        let mut updates = Vec::new();
        {
            let mut cache = self
                .cache
                .lock()
                .map_err(|_| failure(FirewallErrorCode::Disconnected))?;
            if status != cache.status {
                updates.push(Update::Status(status.clone()));
            }
            let alerts = snapshot
                .as_ref()
                .map(|s| s.alerts.clone())
                .unwrap_or_default();
            if force_alerts
                || cache.snapshot.as_ref().map(|s| &s.alerts)
                    != snapshot.as_ref().map(|s| &s.alerts)
            {
                updates.push(Update::Alerts(alerts));
            }
            if let Some(current) = &snapshot {
                for (changed, revision, previous) in [
                    (
                        FirewallChangeKind::Rules,
                        current.rules_revision,
                        cache.snapshot.as_ref().map(|s| s.rules_revision),
                    ),
                    (
                        FirewallChangeKind::Settings,
                        current.settings_revision,
                        cache.snapshot.as_ref().map(|s| s.settings_revision),
                    ),
                    (
                        FirewallChangeKind::Blocklists,
                        current.blocklists_revision,
                        cache.snapshot.as_ref().map(|s| s.blocklists_revision),
                    ),
                ] {
                    if previous != Some(revision) {
                        updates.push(Update::Changed(changed));
                    }
                }
                let changed: Vec<_> = current
                    .events
                    .iter()
                    .filter(|event| {
                        !cache
                            .snapshot
                            .as_ref()
                            .is_some_and(|last| last.events.contains(event))
                    })
                    .cloned()
                    .collect();
                for event in changed {
                    cache.activity.insert(event.id.clone(), event);
                }
            }
            if cache.last_activity.elapsed() >= Duration::from_secs(1) && !cache.activity.is_empty()
            {
                let mut activity: Vec<_> = cache.activity.drain().map(|(_, event)| event).collect();
                activity.sort_by_key(|e| std::cmp::Reverse(e.at));
                updates.push(Update::Activity(activity));
                cache.last_activity = Instant::now();
            }
            cache.status = status.clone();
            cache.snapshot = snapshot;
        }
        super::set_protection(&status);
        for update in updates {
            (self.sink)(update);
        }
        Ok(status)
    }
    pub fn status(&self) -> Result<FirewallStatus, AppError> {
        self.refresh(false)
    }
    // None activates the bundled extension without replacing its persisted configuration.
    pub fn enable(
        &self,
        options: impl Into<Option<FirewallEnableOptions>>,
    ) -> Result<FirewallStatus, AppError> {
        self.available()?;
        self.cache
            .lock()
            .map_err(|_| failure(FirewallErrorCode::Disconnected))?
            .pending_options = options.into().map(|options| Configuration {
            id: uuid::Uuid::now_v7().to_string(),
            options,
        });
        let _: FirewallStatus = self.call(json!({"op":"enable"}))?;
        self.refresh(false)
    }
    pub fn disable(&self, uninstall: bool) -> Result<FirewallStatus, AppError> {
        self.available()?;
        {
            let mut cache = self
                .cache
                .lock()
                .map_err(|_| failure(FirewallErrorCode::Disconnected))?;
            cache.pending_options = None;
        }
        // Disabling must remain possible when the extension or its XPC endpoint has failed.
        let _ = self.bridge.request(json!({"op":"flush"}));
        self.bridge.disconnect();
        let _: FirewallStatus =
            self.call(json!({"op":if uninstall {"uninstall"}else{"disable"}}))?;
        self.refresh(true)
    }
    pub fn open_approval(&self) -> Result<(), AppError> {
        self.available()?;
        self.call(json!({"op":"open_approval"}))
    }
    pub fn identify(&self, path: String) -> Result<FirewallApp, AppError> {
        self.available()?;
        let mut app: FirewallApp = self.call(json!({"op":"identify","path":path}))?;
        self.icon(&mut app);
        Ok(app)
    }
    pub fn settings(&self) -> Result<FirewallSettings, AppError> {
        let status = self.available()?;
        if status.extension == FirewallExtensionState::NotInstalled {
            return Ok(FirewallSettings::default());
        }
        self.call(json!({"op":"settings"}))
    }
    pub fn list<T: DeserializeOwned>(&self, request: Value) -> Result<Vec<T>, AppError> {
        let status = bridge::status(self.bridge.as_ref())?;
        if status.availability != FirewallAvailability::Available
            || status.extension == FirewallExtensionState::NotInstalled
        {
            return Ok(vec![]);
        }
        self.call(request)
    }
    pub fn rules(&self) -> Result<Vec<FirewallRule>, AppError> {
        let mut rules: Vec<FirewallRule> = self.list(json!({"op":"rules"}))?;
        for rule in &mut rules {
            if let Some(app) = &mut rule.app {
                self.icon(app);
            }
        }
        Ok(rules)
    }
    pub fn alerts(&self) -> Result<Vec<FirewallAlert>, AppError> {
        let mut alerts: Vec<FirewallAlert> = self.list(json!({"op":"alerts"}))?;
        for alert in &mut alerts {
            self.icon(&mut alert.app);
        }
        Ok(alerts)
    }
    pub fn events(&self, query: FirewallEventsQuery) -> Result<Vec<FirewallEvent>, AppError> {
        let mut events: Vec<FirewallEvent> = self.list(json!({"op":"events","query":query}))?;
        for event in &mut events {
            self.icon(&mut event.app);
        }
        Ok(events)
    }
    pub fn mutate<T: DeserializeOwned>(&self, request: Value) -> Result<T, AppError> {
        self.available()?;
        let reply = request["op"] == "alert_reply";
        let result = self.call(request);
        // Always replay after a reply, including an expired/stale alert, so the UI cannot keep a dead card.
        let refreshed = self.refresh(reply);
        match result {
            Ok(value) => {
                refreshed?;
                Ok(value)
            }
            Err(error) => Err(error),
        }
    }
    pub fn blocklist_load(
        &self,
        source: Option<String>,
        id: Option<String>,
    ) -> Result<FirewallBlocklist, AppError> {
        self.available()?;
        let source = match source {
            Some(source) => source,
            None => self
                .list::<FirewallBlocklist>(json!({"op":"blocklists"}))?
                .into_iter()
                .find(|list| Some(&list.id) == id.as_ref())
                .map(|list| list.source)
                .ok_or_else(|| failure(FirewallErrorCode::BlocklistFormat))?,
        };
        let text = if source.starts_with("https://") {
            let url = reqwest::Url::parse(&source)
                .map_err(|_| failure(FirewallErrorCode::BlocklistUrl))?;
            if url.host_str().is_none() || !url.username().is_empty() || url.password().is_some() {
                return Err(failure(FirewallErrorCode::BlocklistUrl));
            }
            None
        } else {
            let path = std::path::Path::new(&source);
            if !path.is_absolute() || !path.is_file() {
                return Err(failure(FirewallErrorCode::BlocklistUrl));
            }
            let file = std::fs::File::open(path)
                .map_err(|_| failure(FirewallErrorCode::BlocklistFetch))?;
            let mut bytes = Vec::new();
            file.take(MAX_LIST_BYTES as u64 + 1)
                .read_to_end(&mut bytes)
                .map_err(|_| failure(FirewallErrorCode::BlocklistFetch))?;
            parse_blocklist(&bytes).map_err(failure)?;
            Some(
                String::from_utf8(bytes)
                    .map_err(|_| failure(FirewallErrorCode::BlocklistFormat))?,
            )
        };
        self.mutate(json!({"op":if id.is_some(){"blocklist_refresh"}else{"blocklist_add"},"source":source,"id":id,"text":text}))
    }
    pub fn show(&self) -> Result<(), AppError> {
        self.available()?;
        (self.sink)(Update::Alerts(self.alerts()?));
        Ok(())
    }
    fn icon(&self, app: &mut FirewallApp) {
        let Some((database, cache)) = &self.icons else {
            return;
        };
        let path = app.bundle_path.as_deref().unwrap_or(&app.path);
        let Ok(mut icons) = self.icon_cache.lock() else {
            return;
        };
        if icons.len() > 4096 {
            icons.clear();
        }
        app.icon_path = icons
            .entry(path.into())
            .or_insert_with(|| {
                crate::library::files::icon(database, std::path::Path::new(path), cache).ok()
            })
            .clone();
    }
}
