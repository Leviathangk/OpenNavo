// SPDX-License-Identifier: GPL-3.0-only
use crate::types::*;
use std::{collections::HashSet, net::IpAddr};

pub const MAX_LIST_BYTES: usize = 50 * 1024 * 1024;
pub const MAX_LIST_ENTRIES: usize = 1_000_000;

fn parse_literal(value: &str) -> Option<IpAddr> {
    let value = value.trim();
    let value = value
        .strip_prefix('[')
        .and_then(|v| v.strip_suffix(']'))
        .unwrap_or(value);
    // A zone (fe80::1%en0) names the local interface, not a different peer.
    let value = match value.split_once('%') {
        Some((ip, _)) if ip.contains(':') => ip,
        _ => value,
    };
    value.parse().ok()
}

/// One spelling per peer: brackets and IPv6 zone dropped, IPv4-mapped IPv6 as IPv4.
pub fn parse_ip(value: &str) -> Option<IpAddr> {
    parse_literal(value).map(|ip| ip.to_canonical())
}

/// An IP or CIDR as (network, prefix); a bare IP has a full-length prefix. A network
/// inside ::ffff:0:0/96 is its IPv4 equivalent, matching canonical flow addresses.
pub fn parse_network(value: &str) -> Option<(IpAddr, u8)> {
    let (ip, prefix) = match value.split_once('/') {
        Some((ip, prefix)) => (parse_literal(ip)?, Some(prefix.trim().parse::<u8>().ok()?)),
        None => (parse_literal(value)?, None),
    };
    let bits = if ip.is_ipv4() { 32 } else { 128 };
    let prefix = prefix.unwrap_or(bits);
    if prefix > bits {
        return None;
    }
    Some(match ip.to_canonical() {
        IpAddr::V4(v4) if ip.is_ipv6() && prefix >= 96 => (IpAddr::V4(v4), prefix - 96),
        _ => (ip, prefix),
    })
}

/// Address text for a flow: canonical when it is an IP, otherwise trimmed as received.
pub fn normalize_address(value: &str) -> String {
    parse_ip(value).map_or_else(|| value.trim().to_owned(), |ip| ip.to_string())
}

pub fn normalize_remote(value: &str) -> Result<String, FirewallErrorCode> {
    let value = value.trim().trim_end_matches('.').to_ascii_lowercase();
    if value.contains('/') || parse_literal(&value).is_some() {
        // Canonical text, so equivalent spellings compare equal and dedupe.
        let (ip, prefix) = parse_network(&value).ok_or(FirewallErrorCode::RuleInvalid)?;
        return Ok(if value.contains('/') {
            format!("{ip}/{prefix}")
        } else {
            ip.to_string()
        });
    }
    let host = value.strip_prefix("*.").unwrap_or(&value);
    if host.is_empty() || host.len() > 253 || parse_literal(host).is_some() {
        return Err(FirewallErrorCode::RuleInvalid);
    }
    if host.split('.').all(|label| {
        !label.is_empty()
            && label.len() <= 63
            && !label.starts_with('-')
            && !label.ends_with('-')
            && label
                .bytes()
                .all(|c| c.is_ascii_alphanumeric() || c == b'-')
    }) {
        Ok(value)
    } else {
        Err(FirewallErrorCode::RuleInvalid)
    }
}

fn address(ip: IpAddr) -> (u128, u8) {
    match ip {
        IpAddr::V4(ip) => (u32::from(ip) as u128, 32),
        IpAddr::V6(ip) => (u128::from(ip), 128),
    }
}
fn masked(ip: u128, bits: u8, prefix: u8) -> u128 {
    if prefix == 0 {
        0
    } else {
        ip >> (bits - prefix) << (bits - prefix)
    }
}

pub fn remote_matches(rule: &str, ip: &str, host: Option<&str>) -> bool {
    if rule.contains('/') || parse_literal(rule).is_some() {
        return parse_network(rule)
            .zip(parse_ip(ip))
            .is_some_and(|((network, prefix), ip)| {
                let (network, bits) = address(network);
                let (ip, other) = address(ip);
                bits == other && masked(network, bits, prefix) == masked(ip, bits, prefix)
            });
    }
    let Some(host) = host else {
        return false;
    };
    let host = host.trim_end_matches('.').to_ascii_lowercase();
    if let Some(suffix) = rule.strip_prefix("*.") {
        host.len() > suffix.len() + 1 && host.ends_with(&format!(".{suffix}"))
    } else {
        rule == host
    }
}

#[derive(Default, Debug, Clone)]
pub struct BlockIndex {
    hosts: HashSet<String>,
    wildcards: HashSet<String>,
    networks: HashSet<(u128, u8, u8)>,
}
impl BlockIndex {
    pub fn new(entries: &[String]) -> Self {
        let mut index = Self::default();
        for item in entries {
            if let Some((ip, prefix)) = parse_network(item) {
                let (ip, bits) = address(ip);
                index
                    .networks
                    .insert((masked(ip, bits, prefix), bits, prefix));
            } else if item.contains('/') {
                continue;
            } else if let Some(suffix) = item.strip_prefix("*.") {
                index.wildcards.insert(suffix.into());
            } else {
                index.hosts.insert(item.clone());
            }
        }
        index
    }
    pub fn matches(&self, ip: &str, host: Option<&str>) -> bool {
        if let Some(ip) = parse_ip(ip) {
            let (ip, bits) = address(ip);
            if (0..=bits).any(|prefix| {
                self.networks
                    .contains(&(masked(ip, bits, prefix), bits, prefix))
            }) {
                return true;
            }
        }
        if let Some(host) = host {
            let host = host.trim_end_matches('.').to_ascii_lowercase();
            if self.hosts.contains(&host) {
                return true;
            }
            for (position, _) in host.match_indices('.') {
                if self.wildcards.contains(&host[position + 1..]) {
                    return true;
                }
            }
        }
        false
    }
}

// The standard hosts header (StevenBlack and most systems) maps these to loopback,
// broadcast or multicast addresses. They name this machine, not hosts to block.
const LOCAL_NAMES: &[&str] = &[
    "localhost",
    "localhost.localdomain",
    "broadcasthost",
    "local",
    "ip6-localhost",
    "ip6-loopback",
    "ip6-localnet",
    "ip6-mcastprefix",
    "ip6-allnodes",
    "ip6-allrouters",
    "ip6-allhosts",
];

pub fn parse_blocklist(bytes: &[u8]) -> Result<Vec<String>, FirewallErrorCode> {
    if bytes.len() > MAX_LIST_BYTES {
        return Err(FirewallErrorCode::BlocklistTooLarge);
    }
    let text = std::str::from_utf8(bytes).map_err(|_| FirewallErrorCode::BlocklistFormat)?;
    let mut entries = HashSet::new();
    let mut count = 0;
    let mut first = true;
    for line in text.trim_start_matches('\u{feff}').lines() {
        let line = line.split('#').next().unwrap_or_default().trim();
        if line.is_empty() {
            continue;
        }
        // Skipping bad lines must not let a captive portal or error page replace a good list.
        if first && line.starts_with('<') {
            return Err(FirewallErrorCode::BlocklistFormat);
        }
        first = false;
        let parts: Vec<_> = line.split_whitespace().collect();
        // A hosts line maps names to an address; any other line is a single entry.
        let hosts = parts.len() > 1 && parse_ip(parts[0]).is_some();
        if parts.len() > 1 && !hosts {
            continue;
        }
        let values = if hosts { &parts[1..] } else { &parts[..] };
        for value in values {
            count += 1;
            if count > MAX_LIST_ENTRIES {
                return Err(FirewallErrorCode::BlocklistTooLarge);
            }
            // Underscores, Unicode and Adblock syntax can't match; skip them, keep the rest.
            let Ok(normalized) = normalize_remote(value) else {
                continue;
            };
            // An address in a hosts name column (0.0.0.0 0.0.0.0) isn't a name to block.
            if hosts && (normalized.contains('/') || parse_ip(&normalized).is_some()) {
                continue;
            }
            if !LOCAL_NAMES.contains(&normalized.as_str()) {
                entries.insert(normalized);
            }
        }
    }
    if entries.is_empty() {
        return Err(FirewallErrorCode::BlocklistFormat);
    }
    let mut entries: Vec<_> = entries.into_iter().collect();
    entries.sort();
    Ok(entries)
}

pub fn identity_key(app: &FirewallApp) -> String {
    if matches!(
        app.signer,
        FirewallSigner::Apple | FirewallSigner::AppStore | FirewallSigner::DeveloperId
    ) && let Some(id) = app.signing_id.as_deref()
        && let Some(team) = if app.signer == FirewallSigner::Apple {
            Some("apple")
        } else {
            app.team_id.as_deref()
        }
    {
        return format!("signed:{}", serde_json::json!([team, id]));
    }
    format!("path:{}", app.path)
}

pub fn owner_app(child: &FirewallApp, ancestors: &[FirewallApp]) -> FirewallApp {
    let team = if child.signer == FirewallSigner::Apple {
        Some("apple")
    } else {
        child.team_id.as_deref()
    };
    if !matches!(
        child.signer,
        FirewallSigner::Apple | FirewallSigner::AppStore | FirewallSigner::DeveloperId
    ) {
        return child.clone();
    }
    ancestors
        .iter()
        .rev()
        .find(|parent| {
            let parent_team = if parent.signer == FirewallSigner::Apple {
                Some("apple")
            } else {
                parent.team_id.as_deref()
            };
            team.is_some()
                && team == parent_team
                && matches!(
                    parent.signer,
                    FirewallSigner::Apple | FirewallSigner::AppStore | FirewallSigner::DeveloperId
                )
        })
        .cloned()
        .unwrap_or_else(|| child.clone())
}

// Ported from Objective-See LuLu v4.5.1, Extension/GrayList.m.
// Copyright (c) 2017 Objective-See / Patrick Wardle. GPL-3.0.
pub fn graylisted(app: &FirewallApp) -> bool {
    const IDS: &[&str] = &[
        "com.apple.nc",
        "com.apple.ftp",
        "com.apple.zsh",
        "com.apple.ksh",
        "com.apple.php",
        "com.apple.scp",
        "com.apple.ssh",
        "com.apple.bash",
        "com.apple.tcsh",
        "com.apple.curl",
        "com.apple.perl",
        "com.apple.ruby",
        "com.apple.sftp",
        "com.tcltk.tclsh",
        "com.apple.perl5",
        "com.apple.whois",
        "com.apple.python",
        "com.apple.telnet",
        "com.apple.openssh",
        "com.apple.python2",
        "com.apple.python3",
        "org.python.python",
        "com.apple.pythonw",
        "com.apple.osascript",
        // OpenNavo additions: other Apple tools that fetch or send on a user's behalf.
        "com.apple.tftp",
        "com.apple.rsync",
        "com.apple.nscurl",
        "com.apple.openssl",
        "com.apple.ldapsearch",
        "com.apple.git-remote-http",
    ];
    app.signer == FirewallSigner::Apple
        && (app
            .signing_id
            .as_deref()
            .is_some_and(|id| IDS.contains(&id))
            || std::path::Path::new(&app.path)
                .file_name()
                .and_then(|v| v.to_str())
                .is_some_and(|name| {
                    [
                        "curl",
                        "ssh",
                        "scp",
                        "sftp",
                        "nc",
                        "netcat",
                        "osascript",
                        "python",
                        "perl",
                        "ruby",
                        "php",
                        "bash",
                        "zsh",
                        "sh",
                        "nscurl",
                        "openssl",
                        "rsync",
                        "ftp",
                        "tftp",
                        "ldapsearch",
                        // Git's HTTPS transport (CommandLineTools and Xcode git-core).
                        "git-remote-http",
                        "git-remote-https",
                    ]
                    .iter()
                    .any(|tool| {
                        name == *tool
                            || name
                                .strip_prefix(tool)
                                .is_some_and(|tail| tail.starts_with(|c: char| c.is_ascii_digit()))
                    })
                }))
}
