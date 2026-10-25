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
}
// How narrowly a rule names its destination. Wildcard suffixes and CIDR prefixes
// rank only against their own kind; a suffix length and a prefix length don't compare.
#[derive(Clone, Copy)]
enum Remote {
    Any,
    Wildcard(usize),
    Network(u8),
    Exact,
}
impl Remote {
    fn of(rule: &FirewallRule) -> Self {
        let Some(remote) = rule.remote.as_deref() else {
            return Self::Any;
        };
        if let Some(suffix) = remote.strip_prefix("*.") {
            return Self::Wildcard(suffix.len());
        }
        match remote
            .contains('/')
            .then(|| parse_network(remote))
            .flatten()
        {
            Some((ip, prefix)) if prefix < if ip.is_ipv4() { 32 } else { 128 } => {
                Self::Network(prefix)
            }
            _ => Self::Exact,
        }
    }
    fn tier(self) -> u8 {
        match self {
            Self::Any => 0,
            Self::Wildcard(_) | Self::Network(_) => 1,
            Self::Exact => 2,
        }
    }
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
    /// Bumped by match statistics alone (last match, count); the rule list is unchanged.
    #[serde(default)]
    pub rules_stats_revision: u64,
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
    // Until-quit rule ID -> every process (pid, start) that answered with it.
    temporary: HashMap<String, HashSet<(u32, u64)>>,
    heartbeat: Option<u64>,
    now: u64,
    suspended: bool,
    // (configuration ID that started it, monotonic deadline)
    baseline_scan: Option<(String, u64)>,
    configuration: Option<String>,
    pub resolutions: Vec<Resolution>,
    rules_revision: u64,
    rules_stats_revision: u64,
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
        // A process lifetime cannot survive extension restart / reboot. A rule that
        // can never match must not cost all the others: drop it, keep the rest canonical.
        state.rules.retain_mut(|rule| {
            if rule.until != FirewallRuleUntil::Always || rule.port == Some(0) {
                return false;
            }
            if let Some(remote) = &mut rule.remote {
                match normalize_remote(remote) {
                    Ok(normalized) => *remote = normalized,
                    Err(_) => return false,
                }
            }
            // Automatic grants from an older graylist must not bypass today's policy.
            // Explicit user decisions remain valid, including allows for graylisted tools.
            if rule.source == FirewallRuleSource::Apple && rule.app.as_ref().is_some_and(graylisted)
            {
                return false;
            }
            // Earlier builds also granted this to Apple programs, which follow allow_apple alone.
            if rule.source == FirewallRuleSource::Installed
                && rule
                    .app
                    .as_ref()
                    .is_some_and(|app| app.signer == FirewallSigner::Apple)
            {
                return false;
            }
            rule.app.is_some() || rule.remote.is_some() || rule.port.is_some()
        });
        state.events.truncate(1000);
        // Preserve baselines captured by versions predating the readiness flag.
        state.baseline_initialized |= !state.baseline.is_empty();
        for list in &mut state.blocklists {
            if list.entries.len() > MAX_LIST_ENTRIES
                || list.entries.iter().map(String::len).sum::<usize>() > MAX_LIST_BYTES
            {
                return Err(FirewallErrorCode::BlocklistTooLarge);
            }
            let mut changed = false;
            list.entries
                .retain_mut(|entry| match normalize_remote(entry) {
                    Ok(normalized) => {
                        changed |= normalized != *entry;
                        *entry = normalized;
                        true
                    }
                    Err(_) => {
                        changed = true;
                        false
                    }
                });
            if changed {
                list.entries.sort();
                list.entries.dedup();
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
        let existing = self.take_target(
            app.as_ref().map(|a| a.key.as_str()),
            remote.as_deref(),
            port,
            input.id.as_deref(),
        );
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
        self.state.rules.push(rule.clone());
        self.rules_revision += 1;
        self.reevaluate(at, None);
        Ok(rule)
    }
    /// Remove every rule for this exact target, temporary or permanent, plus the one
    /// being edited. Returns the ID to keep: a permanent rule's, so it stays stable.
    fn take_target(
        &mut self,
        key: Option<&str>,
        remote: Option<&str>,
        port: Option<u16>,
        editing: Option<&str>,
    ) -> Option<String> {
        let same = |r: &FirewallRule| {
            r.app.as_ref().map(|a| a.key.as_str()) == key
                && r.remote.as_deref() == remote
                && r.port == port
        };
        let keep = self
            .state
            .rules
            .iter()
            .filter(|r| same(r))
            .min_by_key(|r| r.until != FirewallRuleUntil::Always)
            .map(|r| r.id.clone());
        let temporary = &mut self.temporary;
        self.state.rules.retain(|r| {
            let removed = same(r) || Some(r.id.as_str()) == editing;
            if removed {
                temporary.remove(&r.id);
            }
            !removed
        });
        keep
    }
    pub fn delete_rules(&mut self, ids: &[String], at: i64) {
        self.state.rules.retain(|r| !ids.contains(&r.id));
        self.temporary.retain(|id, _| !ids.contains(id));
        self.rules_revision += 1;
        self.reevaluate(at, None);
    }
    pub fn set_settings(&mut self, settings: FirewallSettings, at: i64) {
        let count = self.state.rules.len();
        self.state.rules.retain(|r| match r.source {
            FirewallRuleSource::Apple => settings.allow_apple,
            FirewallRuleSource::Installed => settings.allow_installed,
            _ => true,
        });
        if self.state.rules.len() != count {
            self.rules_revision += 1;
        }
        self.state.settings = settings;
        self.settings_revision += 1;
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
    /// `fresh` (setup after removal) first forgets everything, then scans again.
    pub fn configure(
        &mut self,
        id: String,
        settings: FirewallSettings,
        fresh: bool,
        at: i64,
    ) -> bool {
        if self.configuration.as_ref() == Some(&id) {
            return false;
        }
        self.configuration = Some(id.clone());
        self.resume();
        if fresh {
            self.reset();
        }
        if self.state.settings != settings {
            self.set_settings(settings, at);
        }
        if self.state.baseline_initialized || self.baseline_scan.is_some() {
            return false;
        }
        self.baseline_scan = Some((id, self.now.saturating_add(BASELINE_TIMEOUT_MS)));
        true
    }
    /// Forget everything the firewall learned, as removal promises: rules, block lists,
    /// activity, the installed-app baseline and answers in progress. Settings stay.
    pub fn reset(&mut self) {
        let state = std::mem::take(&mut self.state);
        self.state = Persistent {
            settings: state.settings,
            day_start: state.day_start,
            ..Persistent::default()
        };
        // A scan still running reports into the wiped baseline under its old ID; ignore it.
        self.baseline_scan = None;
        self.indexes.clear();
        self.temporary.clear();
        self.apps.clear();
        self.questions.clear();
        for flow in std::mem::take(&mut self.pending) {
            self.resolutions.push(Resolution {
                id: flow.id,
                action: FirewallAction::Allow,
            });
        }
        self.rules_revision += 1;
        self.blocklists_revision += 1;
    }
    /// `scan` is the configuration ID that started the scan; results from any other
    /// (cancelled) scan are ignored. None accepts the current scan.
    fn scanning(&self, scan: Option<&str>) -> bool {
        self.baseline_scan
            .as_ref()
            .is_some_and(|(id, _)| scan.is_none_or(|scan| scan == id))
    }
    pub fn baseline_add(&mut self, apps: Vec<FirewallApp>, scan: Option<&str>, at: i64) {
        if !self.scanning(scan) {
            return;
        }
        self.state.baseline.extend(
            apps.into_iter()
                .filter(|a| a.bundle_path.is_some() && a.signer != FirewallSigner::Invalid)
                .map(|a| identity_key(&a)),
        );
        self.reevaluate(at, None);
    }
    pub fn baseline_finish(&mut self, scan: Option<&str>) {
        if self.scanning(scan) {
            // Includes a bounded partial scan. Never restart on polling/reconnect.
            self.baseline_scan = None;
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
                return Err(FirewallErrorCode::BlocklistLimit);
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
            return Err(FirewallErrorCode::BlocklistLimit);
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
        let matches: Vec<_> = self
            .state
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
                        || self.temporary.get(&rule.id).is_some_and(|processes| {
                            processes.contains(&(flow.pid, flow.process_start))
                        }))
                    && rule.port.is_none_or(|port| port == flow.port)
                    && rule.remote.as_deref().is_none_or(|remote| {
                        remote_matches(remote, &flow.remote_address, flow.remote_host.as_deref())
                    })
            })
            .map(|(index, rule)| {
                let block = rule.action == FirewallAction::Block;
                (index, Remote::of(rule), rule.port.is_some(), block)
            })
            .collect();
        let suffix = matches
            .iter()
            .filter_map(|m| match m.1 {
                Remote::Wildcard(length) => Some(length),
                _ => None,
            })
            .max();
        let prefix = matches
            .iter()
            .filter_map(|m| match m.1 {
                Remote::Network(length) => Some(length),
                _ => None,
            })
            .max();
        // The destination decides first: exact host/IP, then the longest wildcard
        // suffix or CIDR prefix, then none. Then a port, then block over allow;
        // max_by_key keeps the latest of equal rules.
        matches
            .into_iter()
            .filter(|m| match m.1 {
                Remote::Wildcard(length) => Some(length) == suffix,
                Remote::Network(length) => Some(length) == prefix,
                _ => true,
            })
            .max_by_key(|m| (m.1.tier(), m.2, m.3))
            .map(|m| m.0)
    }
    fn rule_decision(&mut self, index: usize, at: i64) -> Decision {
        let rule = &mut self.state.rules[index];
        rule.last_match_at = Some(at);
        rule.match_count = rule.match_count.saturating_add(1);
        // Statistics only: clients needn't refetch the rule list for every connection.
        self.rules_stats_revision += 1;
        Decision {
            action: rule.action,
            reason: FirewallVerdictReason::Rule,
            rule: Some(rule.id.clone()),
            blocklist: None,
        }
    }
    /// Rules, block lists and automatic allowances, in precedence order, without
    /// the mode or a question. A program without identity meets destination policy
    /// only: global destination rules and block lists.
    fn policy(&mut self, flow: &Flow, identified: bool) -> Option<Decision> {
        for stage in [1, 2] {
            if (identified || stage == 2)
                && let Some(index) = self.matching_rule(flow, stage)
            {
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
        if !identified {
            return None;
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
            // Apple programs follow allow_apple alone, although the baseline scan
            // also covers /System/Applications.
            && !matches!(flow.app.signer, FirewallSigner::Invalid | FirewallSigner::Apple)
            && flow.baseline_matches(&self.state.baseline)
        {
            Some((
                FirewallRuleSource::Installed,
                FirewallVerdictReason::Installed,
            ))
        } else {
            None
        };
        let (source, reason) = automatic?;
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
        Some(decision)
    }
    pub fn decide(&mut self, flow: &Flow) -> Option<Decision> {
        // The extension owns policy independently of the UI. Losing the client
        // prevents asking, but must not disable explicit or automatic rules.
        if let Some(decision) = self.policy(flow, true) {
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
    // Every flow entering policy, paused or not, takes one canonical shape.
    fn ingest(&mut self, mut flow: Flow) -> Flow {
        flow.app = self.remember(flow.app);
        // Native endpoints can carry an IPv6 zone, an IPv4-mapped form or nothing.
        flow.remote_address = normalize_address(&flow.remote_address);
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
        flow
    }
    pub fn flow(&mut self, flow: Flow) -> Option<FirewallAction> {
        self.now = self.now.max(flow.monotonic_ms);
        // Configuration/disable deliberately suspends policy while the client
        // may still be online. Do not report this as a lost client.
        if self.suspended {
            return Some(FirewallAction::Allow);
        }
        let flow = self.ingest(flow);
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
        if self
            .questions
            .iter()
            .any(|q| q.flow.app.key == flow.app.key)
        {
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
        self.questions.push(Question { flow: flow.clone() });
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
                self.questions.push(Question { flow });
            }
        }
    }
    pub fn alerts(&self) -> Vec<FirewallAlert> {
        let mut alerts = Vec::new();
        for question in &self.questions {
            let flow = &question.flow;
            let held = self.pending.iter().filter(|f| f.app.key == flow.app.key);
            let deadline = held
                .clone()
                .map(|f| f.at.saturating_add(f.timeout_ms() as i64))
                .min();
            // Other connections of this app held right now for this answer.
            let waiting = held.filter(|f| f.id != flow.id).count() as u64;
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
                waiting,
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
        let (remote, port) = if reply.scope == FirewallReplyScope::Destination {
            // Only a destination that can match this flow again may become a rule. An
            // empty or malformed address never matches and would be persisted forever.
            let host = flow
                .remote_host
                .as_deref()
                .and_then(|host| normalize_remote(host).ok())
                .filter(|host| !host.contains('/') && !host.starts_with("*."));
            let remote = host
                .or_else(|| parse_ip(&flow.remote_address).map(|ip| ip.to_string()))
                .ok_or(FirewallErrorCode::RuleInvalid)?;
            (Some(remote), (flow.port > 0).then_some(flow.port))
        } else {
            (None, None)
        };
        let key = flow.app.key.as_str();
        let rule_id = if reply.until == FirewallRuleUntil::AppQuits {
            // Every process of a multi-process app (helpers share its key) answers into
            // one temporary rule. A permanent rule for the target stays untouched.
            let existing = self.state.rules.iter().position(|r| {
                r.until == FirewallRuleUntil::AppQuits
                    && r.app.as_ref().is_some_and(|a| a.key == key)
                    && r.remote == remote
                    && r.port == port
            });
            let rule = match existing {
                Some(index) => {
                    // Re-push: the newest answer is the latest rule for tie-breaks.
                    let mut rule = self.state.rules.remove(index);
                    rule.action = reply.action;
                    rule
                }
                None => FirewallRule {
                    id: id(),
                    app: Some(flow.app.clone()),
                    action: reply.action,
                    remote,
                    port,
                    until: FirewallRuleUntil::AppQuits,
                    pid: Some(flow.pid),
                    source: FirewallRuleSource::User,
                    created_at: at,
                    last_match_at: None,
                    match_count: 0,
                },
            };
            self.temporary
                .entry(rule.id.clone())
                .or_default()
                .insert((flow.pid, flow.process_start));
            let rule_id = rule.id.clone();
            self.state.rules.push(rule);
            rule_id
        } else {
            let existing = self.take_target(Some(key), remote.as_deref(), port, None);
            let rule = FirewallRule {
                id: existing.unwrap_or_else(id),
                app: Some(flow.app.clone()),
                action: reply.action,
                remote,
                port,
                until: FirewallRuleUntil::Always,
                pid: None,
                source: FirewallRuleSource::User,
                created_at: at,
                last_match_at: None,
                match_count: 0,
            };
            let rule_id = rule.id.clone();
            self.state.rules.push(rule);
            rule_id
        };
        self.rules_revision += 1;
        self.questions.retain(|q| q.flow.id != reply.alert_id);
        self.reevaluate(at, Some(&rule_id));
        Ok(())
    }
    pub fn processes(&self) -> Vec<(u32, u64)> {
        self.pending
            .iter()
            .map(|f| (f.pid, f.process_start))
            .chain(self.temporary.values().flatten().copied())
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
    /// A flow that can't wait for an answer: it came too late, has no identity, or
    /// the watchdog already let it through (`released`). Rules, lists and automatic
    /// allowances still apply where they can; without identity only destination
    /// policy does. Nothing is asked; anything else is allowed as no_client.
    pub fn bypass(&mut self, flow: Flow, identified: bool, released: bool) -> FirewallAction {
        if self.suspended {
            return FirewallAction::Allow;
        }
        let flow = self.ingest(flow);
        let decision = if released {
            None
        } else {
            self.policy(&flow, identified)
        }
        .unwrap_or(Decision::allow(FirewallVerdictReason::NoClient));
        self.record(&flow, &decision, flow.at);
        decision.action
    }
    pub fn expire(&mut self, now: u64, at: i64) {
        self.now = now;
        if self
            .baseline_scan
            .as_ref()
            .is_some_and(|(_, deadline)| now >= *deadline)
        {
            self.baseline_finish(None);
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
        // An until-quit rule ends when the last process that answered with it quits.
        for processes in self.temporary.values_mut() {
            processes.retain(|process| alive.contains(process));
        }
        let expired: HashSet<_> = self
            .temporary
            .iter()
            .filter(|(_, processes)| processes.is_empty())
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
            preparing: self.baseline_scan.is_some(),
            configuration: self.configuration.clone(),
            settings: self.state.settings.clone(),
            rule_count: self.state.rules.len() as u64,
            blocked_today: self.state.blocked_today,
            alerts: self.alerts(),
            events: self.state.events.iter().cloned().collect(),
            rules_revision: self.rules_revision,
            rules_stats_revision: self.rules_stats_revision,
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
