// SPDX-License-Identifier: GPL-3.0-only
// Rule evaluation and paused-flow lifecycle adapted from LuLu v4.5.1's
// FilterDataProvider / Rules, with OpenNavo's explicit precedence and fail-open policy.
use crate::{matching::*, types::*};
use serde::{Deserialize, Serialize};
use std::collections::{BTreeSet, HashMap, HashSet, VecDeque};

pub const TCP_TIMEOUT_MS: u64 = 120_000;
pub const UDP_TIMEOUT_MS: u64 = 5_000;
pub const HEARTBEAT_TIMEOUT_MS: u64 = 5_000;
pub const MAX_PENDING: usize = 4096;
pub const MAX_ALERTS: usize = 64;
pub const BASELINE_TIMEOUT_MS: u64 = 60_000;
// Leave 64 MiB below the native 320 MiB restore envelope for rules, events and RPC metadata.
const MAX_BLOCKLIST_STATE_BYTES: usize = 256 * 1024 * 1024;

fn serialized_size(value: &impl Serialize) -> usize {
    struct Counter(usize);
    impl std::io::Write for Counter {
        fn write(&mut self, bytes: &[u8]) -> std::io::Result<usize> {
            self.0 = self.0.saturating_add(bytes.len());
            Ok(bytes.len())
        }
        fn flush(&mut self) -> std::io::Result<()> {
            Ok(())
        }
    }
    let mut counter = Counter(0);
    if serde_json::to_writer(&mut counter, value).is_err() {
        return usize::MAX;
    }
    counter.0
}

fn blocklists_fit<'a>(lists: impl Iterator<Item = &'a StoredBlocklist>, limit: usize) -> bool {
    // Include JSON array delimiters, commas, escaping and metadata, not just entry text.
    lists.fold(2usize, |size, list| {
        size.saturating_add(serialized_size(list)).saturating_add(1)
    }) <= limit
}

const RETENTION_MS: i64 = 7 * 24 * 60 * 60 * 1000;

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Flow {
    pub id: String,
    pub app: FirewallApp,
    // Verified native ownership evidence; never a user-facing app identity or
    // persisted rule key. Old callers/flows have no ownership hint.
    #[serde(default)]
    pub baseline_owner: Option<FirewallApp>,
    pub pid: u32,
    pub process_start: u64,
    pub args: Vec<String>,
    pub ancestors: Vec<FirewallProcess>,
    pub remote_address: String,
    pub remote_host: Option<String>,
    pub port: u16,
    pub protocol: FirewallProtocol,
    pub at: i64,
    pub monotonic_ms: u64,
}
impl Flow {
    fn baseline_matches(&self, baseline: &BTreeSet<String>) -> bool {
        if baseline.contains(&self.app.key) {
            return true;
        }
        self.baseline_owner.as_ref().is_some_and(|owner| {
            matches!(
                self.app.signer,
                FirewallSigner::Adhoc | FirewallSigner::Unsigned
            ) && self.app.signer == owner.signer
                && self.app.team_id.is_none()
                && owner.team_id.is_none()
                && owner.bundle_path.as_ref().is_some_and(|bundle| {
                    std::path::Path::new(bundle)
                        .extension()
                        .is_some_and(|ext| ext == "app")
                        && std::path::Path::new(&self.app.path).starts_with(bundle)
                        && std::path::Path::new(&owner.path).starts_with(bundle)
                })
                && baseline.contains(&identity_key(owner))
        })
    }
    fn timeout_ms(&self) -> u64 {
        match self.protocol {
            FirewallProtocol::Tcp => TCP_TIMEOUT_MS,
            FirewallProtocol::Udp => UDP_TIMEOUT_MS,
        }
    }
}
// An unanswered question outlives the sockets that originally caused it.
// Keep one representative, not every released flow, so retries cannot grow memory.
struct Question {
    flow: Flow,
    waiting: u64,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StoredBlocklist {
    pub info: FirewallBlocklist,
    pub entries: Vec<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Persistent {
    pub version: u32,
    pub settings: FirewallSettings,
    pub rules: Vec<FirewallRule>,
    pub blocklists: Vec<StoredBlocklist>,
    pub baseline: BTreeSet<String>,
    #[serde(default)]
    pub baseline_initialized: bool,
    pub events: VecDeque<FirewallEvent>,
    pub day_start: i64,
    pub blocked_today: u64,
}
impl Default for Persistent {
    fn default() -> Self {
        Self {
            version: 1,
            settings: FirewallSettings::default(),
            rules: vec![],
            blocklists: vec![],
            baseline: BTreeSet::new(),
            baseline_initialized: false,
            events: VecDeque::new(),
            day_start: 0,
            blocked_today: 0,
        }
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Snapshot {
    #[serde(default)]
    pub preparing: bool,
    #[serde(default)]
    pub configuration: Option<String>,
    pub settings: FirewallSettings,
    pub rule_count: u64,
    pub blocked_today: u64,
    pub alerts: Vec<FirewallAlert>,
    pub events: Vec<FirewallEvent>,
    pub rules_revision: u64,
    pub settings_revision: u64,
    pub blocklists_revision: u64,
}
#[derive(Debug, Clone, Serialize)]
pub struct Resolution {
    pub id: String,
    pub action: FirewallAction,
}
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Decision {
    pub action: FirewallAction,
    pub reason: FirewallVerdictReason,
    pub rule: Option<String>,
    pub blocklist: Option<String>,
}
impl Decision {
    fn allow(reason: FirewallVerdictReason) -> Self {
        Self {
            action: FirewallAction::Allow,
            reason,
            rule: None,
            blocklist: None,
        }
    }
}
#[derive(Default)]
pub struct Engine {
    pub state: Persistent,
    pending: Vec<Flow>,
    questions: Vec<Question>,
    apps: HashMap<String, FirewallApp>,
    indexes: HashMap<String, BlockIndex>,
    temporary: HashMap<String, (u32, u64)>,
    heartbeat: Option<u64>,
    now: u64,
    suspended: bool,
    baseline_deadline: Option<u64>,
    configuration: Option<String>,
    pub resolutions: Vec<Resolution>,
    rules_revision: u64,
    settings_revision: u64,
    blocklists_revision: u64,
}
fn id() -> String {
    uuid::Uuid::now_v7().to_string()
}
impl Engine {
    pub fn restore(mut state: Persistent) -> Result<Self, FirewallErrorCode> {
        if state.version != 1 || state.blocklists.len() > 32 || state.rules.len() > 100_000 {
            return Err(FirewallErrorCode::Disconnected);
        }
        if !blocklists_fit(state.blocklists.iter(), MAX_BLOCKLIST_STATE_BYTES) {
            return Err(FirewallErrorCode::BlocklistTooLarge);
        }
        // A process lifetime cannot survive extension restart / reboot.
        state.rules.retain(|r| r.until == FirewallRuleUntil::Always);
        for rule in &state.rules {
            if rule.port == Some(0)
                || (rule.app.is_none() && rule.remote.is_none() && rule.port.is_none())
            {
                return Err(FirewallErrorCode::RuleInvalid);
            }
            if let Some(remote) = &rule.remote {
                normalize_remote(remote)?;
            }
        }
        state.events.truncate(1000);
        // Preserve baselines captured by versions predating the readiness flag.
        state.baseline_initialized |= !state.baseline.is_empty();
        for list in &mut state.blocklists {
            if list.entries.len() > MAX_LIST_ENTRIES
                || list.entries.iter().map(String::len).sum::<usize>() > MAX_LIST_BYTES
            {
                return Err(FirewallErrorCode::BlocklistTooLarge);
            }
            for entry in &mut list.entries {
                *entry = normalize_remote(entry)?;
            }
            list.info.entries = list.entries.len() as u64;
        }
        let mut engine = Self {
            state,
            ..Self::default()
        };
        engine.rebuild_indexes();
        Ok(engine)
    }
    pub fn persistent(&self) -> Persistent {
        let mut state = self.state.clone();
        state.rules.retain(|r| r.until == FirewallRuleUntil::Always);
        state
    }
    fn rebuild_indexes(&mut self) {
        self.indexes = self
            .state
            .blocklists
            .iter()
            .map(|list| (list.info.id.clone(), BlockIndex::new(&list.entries)))
            .collect();
    }
    pub fn connect(&mut self, now: u64) {
        self.now = now;
        self.heartbeat = Some(now);
    }
    pub fn connected(&self, now: u64) -> bool {
        !self.suspended
            && self
                .heartbeat
                .is_some_and(|last| now.saturating_sub(last) < HEARTBEAT_TIMEOUT_MS)
    }
    pub fn disconnect(&mut self, at: i64) {
        self.heartbeat = None;
        self.questions.clear();
        let flows = std::mem::take(&mut self.pending);
        for flow in flows {
            self.resolve(flow, Decision::allow(FirewallVerdictReason::NoClient), at);
        }
    }
    pub fn set_day(&mut self, day_start: i64) {
        if self.state.day_start != day_start {
            self.state.day_start = day_start;
            self.state.blocked_today = 0;
        }
    }
    pub fn known_app(&self, key: &str) -> Option<FirewallApp> {
        self.apps
            .get(key)
            .cloned()
            .or_else(|| {
                self.state
                    .rules
                    .iter()
                    .filter_map(|r| r.app.as_ref())
                    .find(|a| a.key == key)
                    .cloned()
            })
            .or_else(|| {
                self.state
                    .events
                    .iter()
                    .find(|e| e.app.key == key)
                    .map(|e| e.app.clone())
            })
    }
    pub fn remember(&mut self, mut app: FirewallApp) -> FirewallApp {
        app.key = identity_key(&app);
        app.icon_path = None;
        if self.apps.len() >= 4096 {
            self.apps.clear();
        }
        self.apps.insert(app.key.clone(), app.clone());
        app
    }
    pub fn save_rule(
        &mut self,
        input: FirewallRuleInput,
        app: Option<FirewallApp>,
        at: i64,
    ) -> Result<FirewallRule, FirewallErrorCode> {
        let remote = input.remote.as_deref().map(normalize_remote).transpose()?;
        let port = input
            .port
            .map(|port| {
                u16::try_from(port)
                    .ok()
                    .filter(|p| *p > 0)
                    .ok_or(FirewallErrorCode::RuleInvalid)
            })
            .transpose()?;
        if app.is_none() && remote.is_none() && port.is_none() {
            return Err(FirewallErrorCode::RuleInvalid);
        }
        if input
            .id
            .as_ref()
            .is_some_and(|id| !self.state.rules.iter().any(|r| &r.id == id))
        {
            return Err(FirewallErrorCode::RuleInvalid);
        }
        let existing = self
            .state
            .rules
            .iter()
            .find(|r| {
                r.app.as_ref().map(|a| &a.key) == app.as_ref().map(|a| &a.key)
                    && r.remote == remote
                    && r.port == port
            })
            .map(|r| r.id.clone());
        let editing = input.id.clone();
        let rule = FirewallRule {
            id: existing.or(input.id).unwrap_or_else(id),
            app,
            action: input.action,
            remote,
            port,
            until: FirewallRuleUntil::Always,
            pid: None,
            source: FirewallRuleSource::User,
            created_at: at,
            last_match_at: None,
            match_count: 0,
        };
        self.state
            .rules
            .retain(|r| r.id != rule.id && Some(&r.id) != editing.as_ref());
        if let Some(editing) = editing {
            self.temporary.remove(&editing);
        }
        self.temporary.remove(&rule.id);
        self.state.rules.push(rule.clone());
        self.rules_revision += 1;
        self.reevaluate(at, None);
        Ok(rule)
    }
    pub fn delete_rules(&mut self, ids: &[String], at: i64) {
        self.state.rules.retain(|r| !ids.contains(&r.id));
        self.temporary.retain(|id, _| !ids.contains(id));
        self.rules_revision += 1;
        self.reevaluate(at, None);
    }
    pub fn set_settings(&mut self, settings: FirewallSettings, at: i64) {
        self.state.rules.retain(|r| match r.source {
            FirewallRuleSource::Apple => settings.allow_apple,
            FirewallRuleSource::Installed => settings.allow_installed,
            _ => true,
        });
        self.state.settings = settings;
        self.settings_revision += 1;
        self.rules_revision += 1;
        self.reevaluate(at, None);
    }
    pub fn set_baseline(&mut self, apps: Vec<FirewallApp>) {
        self.state.baseline_initialized = true;
        self.state.baseline = apps
            .into_iter()
            .filter(|a| a.bundle_path.is_some() && a.signer != FirewallSigner::Invalid)
            .map(|a| identity_key(&a))
            .collect();
    }
    /// Acknowledge configuration without waiting for disk/signature work. A retry
    /// reuses its ID, and even different options share the one baseline scan.
    pub fn configure(&mut self, id: String, settings: FirewallSettings, at: i64) -> bool {
        if self.configuration.as_ref() == Some(&id) {
            return false;
        }
        self.configuration = Some(id);
        self.resume();
        if self.state.settings != settings {
            self.set_settings(settings, at);
        }
        if self.state.baseline_initialized || self.baseline_deadline.is_some() {
            return false;
        }
        self.baseline_deadline = Some(self.now.saturating_add(BASELINE_TIMEOUT_MS));
        true
    }
    pub fn baseline_add(&mut self, apps: Vec<FirewallApp>, at: i64) {
        if self.baseline_deadline.is_none() {
            return;
        }
        self.state.baseline.extend(
            apps.into_iter()
                .filter(|a| a.bundle_path.is_some() && a.signer != FirewallSigner::Invalid)
                .map(|a| identity_key(&a)),
        );
        self.reevaluate(at, None);
    }
    pub fn baseline_finish(&mut self) {
        if self.baseline_deadline.take().is_some() {
            // Includes a bounded partial scan. Never restart on polling/reconnect.
            self.state.baseline_initialized = true;
        }
    }
    pub fn blocklist_load(
        &mut self,
        existing: Option<&str>,
        source: String,
        name: String,
        bytes: &[u8],
        at: i64,
    ) -> Result<FirewallBlocklist, FirewallErrorCode> {
        let entries = parse_blocklist(bytes)?;
        let info = if let Some(existing) = existing {
            let old = self
                .state
                .blocklists
                .iter()
                .find(|b| b.info.id == existing)
                .ok_or(FirewallErrorCode::BlocklistFormat)?;
            FirewallBlocklist {
                entries: entries.len() as u64,
                updated_at: Some(at),
                error: None,
                ..old.info.clone()
            }
        } else {
            if self.state.blocklists.len() >= 32 {
                return Err(FirewallErrorCode::BlocklistTooLarge);
            }
            if !(source.starts_with("https://") || std::path::Path::new(&source).is_absolute()) {
                return Err(FirewallErrorCode::BlocklistUrl);
            }
            FirewallBlocklist {
                id: id(),
                name,
                source,
                enabled: true,
                entries: entries.len() as u64,
                updated_at: Some(at),
                error: None,
            }
        };
        let candidate = StoredBlocklist {
            info: info.clone(),
            entries,
        };
        if !blocklists_fit(
            self.state
                .blocklists
                .iter()
                .filter(|b| b.info.id != info.id)
                .chain(std::iter::once(&candidate)),
            MAX_BLOCKLIST_STATE_BYTES,
        ) {
            return Err(FirewallErrorCode::BlocklistTooLarge);
        }
        self.state.blocklists.retain(|b| b.info.id != info.id);
        self.indexes
            .insert(info.id.clone(), BlockIndex::new(&candidate.entries));
        self.state.blocklists.push(candidate);
        self.blocklists_revision += 1;
        self.reevaluate(at, None);
        Ok(info)
    }
    pub fn blocklist_error(
        &mut self,
        id: &str,
        error: FirewallErrorCode,
    ) -> Result<(), FirewallErrorCode> {
        self.state
            .blocklists
            .iter_mut()
            .find(|b| b.info.id == id)
            .ok_or(FirewallErrorCode::BlocklistFormat)?
            .info
            .error = Some(error);
        self.blocklists_revision += 1;
        Ok(())
    }
    pub fn blocklist_set(
        &mut self,
        id: &str,
        enabled: bool,
        at: i64,
    ) -> Result<FirewallBlocklist, FirewallErrorCode> {
        let list = self
            .state
            .blocklists
            .iter_mut()
            .find(|b| b.info.id == id)
            .ok_or(FirewallErrorCode::BlocklistFormat)?;
        list.info.enabled = enabled;
        let info = list.info.clone();
        self.blocklists_revision += 1;
        self.reevaluate(at, None);
        Ok(info)
    }
    pub fn blocklist_remove(&mut self, id: &str, at: i64) {
        self.state.blocklists.retain(|b| b.info.id != id);
        self.indexes.remove(id);
        self.blocklists_revision += 1;
        self.reevaluate(at, None);
    }
    fn matching_rule(&self, flow: &Flow, stage: u8) -> Option<usize> {
        self.state
            .rules
            .iter()
            .enumerate()
            .filter(|(_, rule)| {
                let specific = rule.remote.is_some() || rule.port.is_some();
                let app_match = rule.app.as_ref().is_some_and(|app| app.key == flow.app.key);
                let correct = match stage {
                    1 => app_match && specific,
                    2 => rule.app.is_none() && specific,
                    4 => app_match && !specific,
                    _ => false,
                };
                correct
                    && (flow.app.signer != FirewallSigner::Invalid
                        || rule.source == FirewallRuleSource::User)
                    && (rule.until == FirewallRuleUntil::Always
                        || self.temporary.get(&rule.id) == Some(&(flow.pid, flow.process_start)))
                    && rule.port.is_none_or(|port| port == flow.port)
                    && rule.remote.as_deref().is_none_or(|remote| {
                        remote_matches(remote, &flow.remote_address, flow.remote_host.as_deref())
                    })
            })
            .max_by_key(|(_, rule)| {
                (
                    (if rule.remote.is_some() { 2 } else { 0 }) + u8::from(rule.port.is_some()),
                    u8::from(rule.action == FirewallAction::Block),
                )
            })
            .map(|(index, _)| index)
    }
    fn rule_decision(&mut self, index: usize, at: i64) -> Decision {
        let rule = &mut self.state.rules[index];
        rule.last_match_at = Some(at);
        rule.match_count = rule.match_count.saturating_add(1);
        self.rules_revision += 1;
        Decision {
            action: rule.action,
            reason: FirewallVerdictReason::Rule,
            rule: Some(rule.id.clone()),
            blocklist: None,
        }
    }
    pub fn decide(&mut self, flow: &Flow) -> Option<Decision> {
        // The extension owns policy independently of the UI. Losing the client
        // prevents asking, but must not disable explicit or automatic rules.
        for stage in [1, 2] {
            if let Some(index) = self.matching_rule(flow, stage) {
                return Some(self.rule_decision(index, flow.at));
            }
        }
        for list in &self.state.blocklists {
            if list.info.enabled
                && self.indexes.get(&list.info.id).is_some_and(|index| {
                    index.matches(&flow.remote_address, flow.remote_host.as_deref())
                })
            {
                return Some(Decision {
                    action: FirewallAction::Block,
                    reason: FirewallVerdictReason::Blocklist,
                    rule: None,
                    blocklist: Some(list.info.id.clone()),
                });
            }
        }
        if let Some(index) = self.matching_rule(flow, 4) {
            return Some(self.rule_decision(index, flow.at));
        }
        let automatic = if self.state.settings.allow_apple
            && flow.app.signer == FirewallSigner::Apple
            && !graylisted(&flow.app)
        {
            Some((FirewallRuleSource::Apple, FirewallVerdictReason::Apple))
        } else if self.state.settings.allow_installed
            && flow.app.signer != FirewallSigner::Invalid
            && flow.baseline_matches(&self.state.baseline)
            && !graylisted(&flow.app)
        {
            Some((
                FirewallRuleSource::Installed,
                FirewallVerdictReason::Installed,
            ))
        } else {
            None
        };
        if let Some((source, reason)) = automatic {
            let rule = FirewallRule {
                id: id(),
                app: Some(flow.app.clone()),
                action: FirewallAction::Allow,
                remote: None,
                port: None,
                until: FirewallRuleUntil::Always,
                pid: None,
                source,
                created_at: flow.at,
                last_match_at: Some(flow.at),
                match_count: 1,
            };
            let decision = Decision {
                action: FirewallAction::Allow,
                reason,
                rule: Some(rule.id.clone()),
                blocklist: None,
            };
            self.state.rules.push(rule);
            self.rules_revision += 1;
            return Some(decision);
        }
        if !self.connected(self.now.max(flow.monotonic_ms)) {
            return Some(Decision::allow(FirewallVerdictReason::NoClient));
        }
        match self.state.settings.mode {
            FirewallMode::Ask => None,
            FirewallMode::Allow => Some(Decision::allow(FirewallVerdictReason::Mode)),
            FirewallMode::Block => Some(Decision {
                action: FirewallAction::Block,
                reason: FirewallVerdictReason::Mode,
                rule: None,
                blocklist: None,
            }),
        }
    }
    pub fn flow(&mut self, mut flow: Flow) -> Option<FirewallAction> {
        self.now = self.now.max(flow.monotonic_ms);
        // Configuration/disable deliberately suspends policy while the client
        // may still be online. Do not report this as a lost client.
        if self.suspended {
            return Some(FirewallAction::Allow);
        }
        flow.app = self.remember(flow.app);
        flow.remote_host = flow
            .remote_host
            .and_then(|h| normalize_remote(&h).ok())
            .filter(|h| !h.contains('/') && !h.starts_with('*'));
        flow.args = flow
            .args
            .into_iter()
            .take(20)
            .map(|arg| arg.chars().take(200).collect())
            .collect();
        flow.ancestors.truncate(32);
        let decision = self.decide(&flow);
        if let Some(decision) = decision {
            let action = decision.action;
            self.record(&flow, &decision, flow.at);
            Some(action)
        } else {
            if self.pending.len() >= MAX_PENDING || !self.enqueue_question(&flow) {
                // No question can be serviced under overload; fail open without a rule.
                self.record(
                    &flow,
                    &Decision::allow(FirewallVerdictReason::NoClient),
                    flow.at,
                );
                return Some(FirewallAction::Allow);
            }
            self.pending.push(flow);
            None
        }
    }
    fn enqueue_question(&mut self, flow: &Flow) -> bool {
        if let Some(question) = self
            .questions
            .iter_mut()
            .find(|q| q.flow.app.key == flow.app.key)
        {
            question.waiting = question.waiting.saturating_add(1);
            return true;
        }
        if self.questions.len() >= MAX_ALERTS {
            let oldest_released = self
                .questions
                .iter()
                .enumerate()
                .filter(|(_, q)| !self.pending.iter().any(|f| f.app.key == q.flow.app.key))
                .min_by_key(|(_, q)| q.flow.at)
                .map(|(i, _)| i);
            let Some(index) = oldest_released else {
                return false;
            };
            self.questions.remove(index);
        }
        self.questions.push(Question {
            flow: flow.clone(),
            waiting: 0,
        });
        true
    }
    pub fn flow_details(
        &mut self,
        id: &str,
        args: Vec<String>,
        mut ancestors: Vec<FirewallProcess>,
    ) {
        let args: Vec<_> = args
            .into_iter()
            .take(20)
            .map(|arg| arg.chars().take(200).collect::<String>())
            .collect();
        ancestors.truncate(32);
        for flow in self
            .pending
            .iter_mut()
            .chain(self.questions.iter_mut().map(|q| &mut q.flow))
        {
            if flow.id == id {
                flow.args = args.clone();
                flow.ancestors = ancestors.clone();
            }
        }
    }
    fn record(&mut self, flow: &Flow, decision: &Decision, at: i64) {
        if decision.action == FirewallAction::Allow
            && decision.reason == FirewallVerdictReason::Rule
        {
            return;
        }
        if decision.action == FirewallAction::Block {
            self.state.blocked_today = self.state.blocked_today.saturating_add(1);
        }
        self.state
            .events
            .retain(|event| at.saturating_sub(event.at) <= RETENTION_MS);
        let found = self.state.events.iter().position(|e| {
            at.saturating_sub(e.at) < 60_000
                && e.at >= self.state.day_start
                && e.app.key == flow.app.key
                && e.remote_address == flow.remote_address
                && e.remote_host == flow.remote_host
                && e.port == flow.port
                && e.protocol == flow.protocol
                && e.action == decision.action
                && e.reason == decision.reason
                && e.rule_id == decision.rule
                && e.blocklist_id == decision.blocklist
        });
        let event = if let Some(index) = found {
            let mut event = self.state.events.remove(index).expect("existing index");
            event.at = at;
            event.count = event.count.saturating_add(1);
            event
        } else {
            FirewallEvent {
                id: id(),
                at,
                app: flow.app.clone(),
                remote_address: flow.remote_address.clone(),
                remote_host: flow.remote_host.clone(),
                port: flow.port,
                protocol: flow.protocol,
                action: decision.action,
                reason: decision.reason,
                rule_id: decision.rule.clone(),
                blocklist_id: decision.blocklist.clone(),
                count: 1,
            }
        };
        self.state.events.push_front(event);
        self.state.events.truncate(1000);
    }
    fn resolve(&mut self, flow: Flow, decision: Decision, at: i64) {
        self.record(&flow, &decision, at);
        self.resolutions.push(Resolution {
            id: flow.id,
            action: decision.action,
        });
    }
    fn reevaluate(&mut self, at: i64, answer: Option<&str>) {
        let pending = std::mem::take(&mut self.pending);
        let mut resolved = HashSet::new();
        for flow in pending {
            if let Some(mut decision) = self.decide(&flow) {
                if answer.is_some() && decision.rule.as_deref() == answer {
                    decision.reason = FirewallVerdictReason::Answer;
                }
                resolved.insert(flow.id.clone());
                self.resolve(flow, decision, at);
            } else {
                self.pending.push(flow);
            }
        }
        self.questions.retain(|q| !resolved.contains(&q.flow.id));
        // A destination reply can leave a different destination waiting.
        for flow in self.pending.clone() {
            if !self
                .questions
                .iter()
                .any(|q| q.flow.app.key == flow.app.key)
            {
                let waiting = self
                    .pending
                    .iter()
                    .filter(|f| f.app.key == flow.app.key)
                    .count()
                    - 1;
                self.questions.push(Question {
                    flow,
                    waiting: waiting as u64,
                });
            }
        }
    }
    pub fn alerts(&self) -> Vec<FirewallAlert> {
        let mut alerts = Vec::new();
        for question in &self.questions {
            let flow = &question.flow;
            let deadline = self
                .pending
                .iter()
                .filter(|f| f.app.key == flow.app.key)
                .map(|f| f.at.saturating_add(f.timeout_ms() as i64))
                .min();
            alerts.push(FirewallAlert {
                id: flow.id.clone(),
                app: flow.app.clone(),
                pid: flow.pid,
                args: flow.args.clone(),
                ancestors: flow.ancestors.clone(),
                remote_address: flow.remote_address.clone(),
                remote_host: flow.remote_host.clone(),
                port: flow.port,
                protocol: flow.protocol,
                at: flow.at,
                waiting: question.waiting,
                released: deadline.is_none(),
                deadline,
            });
        }
        alerts.sort_by_key(|a| a.at);
        alerts
    }
    pub fn reply(&mut self, reply: FirewallReply, at: i64) -> Result<(), FirewallErrorCode> {
        let flow = self
            .questions
            .iter()
            .find(|q| q.flow.id == reply.alert_id)
            .map(|q| q.flow.clone())
            .ok_or(FirewallErrorCode::UnknownAlert)?;
        let destination = reply.scope == FirewallReplyScope::Destination;
        let rule = FirewallRule {
            id: id(),
            app: Some(flow.app.clone()),
            action: reply.action,
            remote: destination.then(|| {
                flow.remote_host
                    .clone()
                    .unwrap_or_else(|| flow.remote_address.clone())
            }),
            port: destination.then_some(flow.port),
            until: reply.until,
            pid: (reply.until == FirewallRuleUntil::AppQuits).then_some(flow.pid),
            source: FirewallRuleSource::User,
            created_at: at,
            last_match_at: None,
            match_count: 0,
        };
        let mut rule = rule;
        if let Some(existing) = self.state.rules.iter().find(|r| {
            r.app.as_ref().map(|a| &a.key) == rule.app.as_ref().map(|a| &a.key)
                && r.remote == rule.remote
                && r.port == rule.port
        }) {
            rule.id = existing.id.clone();
        }
        self.state.rules.retain(|r| r.id != rule.id);
        self.temporary.remove(&rule.id);
        if reply.until == FirewallRuleUntil::AppQuits {
            self.temporary
                .insert(rule.id.clone(), (flow.pid, flow.process_start));
        }
        let rule_id = rule.id.clone();
        self.state.rules.push(rule);
        self.rules_revision += 1;
        self.questions.retain(|q| q.flow.id != reply.alert_id);
        self.reevaluate(at, Some(&rule_id));
        Ok(())
    }
    pub fn processes(&self) -> Vec<(u32, u64)> {
        self.pending
            .iter()
            .map(|f| (f.pid, f.process_start))
            .chain(self.temporary.values().copied())
            .collect::<HashSet<_>>()
            .into_iter()
            .collect()
    }
    pub fn suspend(&mut self, _at: i64) {
        self.suspended = true;
        self.heartbeat = None;
        self.questions.clear();
        for flow in std::mem::take(&mut self.pending) {
            self.resolutions.push(Resolution {
                id: flow.id,
                action: FirewallAction::Allow,
            });
        }
    }
    pub fn resume(&mut self) {
        self.suspended = false;
    }
    pub fn cancel(&mut self, id: &str, at: i64) {
        if !self.connected(self.now) {
            self.disconnect(at);
            return;
        }
        if let Some(index) = self.pending.iter().position(|f| f.id == id) {
            let flow = self.pending.remove(index);
            // Internal failure to service this flow, not a user-answer timeout.
            // Keep the question available while releasing its socket.
            self.resolve(flow, Decision::allow(FirewallVerdictReason::NoClient), at);
        }
    }
    pub fn timeout(&mut self, id: &str, at: i64) {
        if !self.connected(self.now) {
            self.disconnect(at);
        } else if let Some(index) = self.pending.iter().position(|f| f.id == id) {
            let flow = self.pending.remove(index);
            self.resolve(flow, Decision::allow(FirewallVerdictReason::Timeout), at);
        }
    }
    pub fn bypass(&mut self, mut flow: Flow) {
        flow.app = self.remember(flow.app);
        if !self.suspended {
            self.record(
                &flow,
                &Decision::allow(FirewallVerdictReason::NoClient),
                flow.at,
            );
        }
    }
    pub fn expire(&mut self, now: u64, at: i64) {
        self.now = now;
        if self
            .baseline_deadline
            .is_some_and(|deadline| now >= deadline)
        {
            self.baseline_finish();
        }
        if !self.connected(now) {
            self.disconnect(at);
        }
        let pending = std::mem::take(&mut self.pending);
        for flow in pending {
            if now.saturating_sub(flow.monotonic_ms) >= flow.timeout_ms() {
                self.resolve(flow, Decision::allow(FirewallVerdictReason::Timeout), at);
            } else {
                self.pending.push(flow);
            }
        }
    }
    pub fn tick(&mut self, now: u64, at: i64, alive: &HashSet<(u32, u64)>) {
        self.expire(now, at);
        let expired: HashSet<_> = self
            .temporary
            .iter()
            .filter(|(_, process)| !alive.contains(process))
            .map(|(id, _)| id.clone())
            .collect();
        if !expired.is_empty() {
            self.state.rules.retain(|r| !expired.contains(&r.id));
            self.temporary.retain(|id, _| !expired.contains(id));
            self.rules_revision += 1;
        }
        let pending = std::mem::take(&mut self.pending);
        for flow in pending {
            if !alive.contains(&(flow.pid, flow.process_start)) {
                self.resolutions.push(Resolution {
                    id: flow.id,
                    action: FirewallAction::Allow,
                });
            } else {
                self.pending.push(flow);
            }
        }
        self.state
            .events
            .retain(|event| at.saturating_sub(event.at) <= RETENTION_MS);
    }
    pub fn snapshot(&self) -> Snapshot {
        Snapshot {
            preparing: self.baseline_deadline.is_some(),
            configuration: self.configuration.clone(),
            settings: self.state.settings.clone(),
            rule_count: self.state.rules.len() as u64,
            blocked_today: self.state.blocked_today,
            alerts: self.alerts(),
            events: self.state.events.iter().cloned().collect(),
            rules_revision: self.rules_revision,
            settings_revision: self.settings_revision,
            blocklists_revision: self.blocklists_revision,
        }
    }
}

#[cfg(test)]
mod size_tests {
    use super::*;
    #[test]
    fn aggregate_budget_counts_json_and_replacement() {
        let mut engine = Engine::default();
        let info = engine
            .blocklist_load(
                None,
                "https://example.test/list".into(),
                "quoted \"name\"".into(),
                b"a.test",
                0,
            )
            .unwrap();
        let first = &engine.state.blocklists[0];
        let size = serialized_size(first);
        assert_eq!(size, serde_json::to_vec(first).unwrap().len());
        assert!(blocklists_fit(std::iter::once(first), size + 3));
        assert!(!blocklists_fit([first, first].into_iter(), size + 3));
        engine
            .blocklist_load(Some(&info.id), String::new(), String::new(), b"b.test", 1)
            .unwrap();
        assert_eq!(engine.state.blocklists.len(), 1);
        let restored = Engine::restore(engine.persistent()).unwrap();
        assert_eq!(restored.state.blocklists[0].entries, vec!["b.test"]);
    }
}
