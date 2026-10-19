// SPDX-License-Identifier: GPL-3.0-only
use crate::types::*;
use std::{collections::HashSet, net::IpAddr};

pub const MAX_LIST_BYTES: usize = 50 * 1024 * 1024;
pub const MAX_LIST_ENTRIES: usize = 1_000_000;

pub fn normalize_remote(value: &str) -> Result<String, FirewallErrorCode> {
    let value = value.trim().trim_end_matches('.').to_ascii_lowercase();
    if value.parse::<IpAddr>().is_ok() {
        return Ok(value);
    }
    if let Some((ip, prefix)) = value.split_once('/') {
        let ip = ip
            .parse::<IpAddr>()
            .map_err(|_| FirewallErrorCode::RuleInvalid)?;
        let prefix = prefix
            .parse::<u8>()
            .map_err(|_| FirewallErrorCode::RuleInvalid)?;
        if prefix <= if ip.is_ipv4() { 32 } else { 128 } {
            return Ok(format!("{ip}/{prefix}"));
        }
        return Err(FirewallErrorCode::RuleInvalid);
    }
    let host = value.strip_prefix("*.").unwrap_or(&value);
    if host.is_empty() || host.len() > 253 || host.parse::<IpAddr>().is_ok() {
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
    if let Some((network, prefix)) = rule.split_once('/') {
        return network
            .parse::<IpAddr>()
            .ok()
            .zip(prefix.parse::<u8>().ok())
            .zip(ip.parse::<IpAddr>().ok())
            .is_some_and(|((network, prefix), ip)| {
                let (network, bits) = address(network);
                let (ip, other) = address(ip);
                bits == other
                    && prefix <= bits
                    && masked(network, bits, prefix) == masked(ip, bits, prefix)
            });
    }
    if let Ok(rule_ip) = rule.parse::<IpAddr>() {
        return ip.parse::<IpAddr>().is_ok_and(|ip| ip == rule_ip);
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
            if let Some((ip, prefix)) = item.split_once('/') {
                if let (Ok(ip), Ok(prefix)) = (ip.parse::<IpAddr>(), prefix.parse::<u8>()) {
                    let (ip, bits) = address(ip);
                    if prefix <= bits {
                        index
                            .networks
                            .insert((masked(ip, bits, prefix), bits, prefix));
                    }
                }
            } else if let Ok(ip) = item.parse::<IpAddr>() {
                let (ip, bits) = address(ip);
                index.networks.insert((ip, bits, bits));
            } else if let Some(suffix) = item.strip_prefix("*.") {
                index.wildcards.insert(suffix.into());
            } else {
                index.hosts.insert(item.clone());
            }
        }
        index
    }
    pub fn matches(&self, ip: &str, host: Option<&str>) -> bool {
        if let Ok(ip) = ip.parse::<IpAddr>() {
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

pub fn parse_blocklist(bytes: &[u8]) -> Result<Vec<String>, FirewallErrorCode> {
    if bytes.len() > MAX_LIST_BYTES {
        return Err(FirewallErrorCode::BlocklistTooLarge);
    }
    let text = std::str::from_utf8(bytes).map_err(|_| FirewallErrorCode::BlocklistFormat)?;
    let mut entries = HashSet::new();
    let mut count = 0;
    for line in text.trim_start_matches('\u{feff}').lines() {
        let line = line.split('#').next().unwrap_or_default().trim();
        if line.is_empty() {
            continue;
        }
        let parts: Vec<_> = line.split_whitespace().collect();
        let values = if parts.len() > 1 && parts[0].parse::<IpAddr>().is_ok() {
            &parts[1..]
        } else {
            &parts[..]
        };
        if values.len() != 1 && values.len() == parts.len() {
            return Err(FirewallErrorCode::BlocklistFormat);
        }
        for value in values {
            count += 1;
            if count > MAX_LIST_ENTRIES {
                return Err(FirewallErrorCode::BlocklistTooLarge);
            }
            let normalized =
                normalize_remote(value).map_err(|_| FirewallErrorCode::BlocklistFormat)?;
            if !matches!(
                normalized.as_str(),
                "localhost" | "localhost.localdomain" | "broadcasthost"
            ) {
                entries.insert(normalized);
            }
        }
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
