// SPDX-License-Identifier: GPL-3.0-only
pub mod engine;
mod ffi;
pub mod matching;
mod rpc;
pub mod types;

use types::*;
impl Default for FirewallSettings {
    fn default() -> Self {
        Self {
            mode: FirewallMode::Ask,
            allow_apple: true,
            allow_installed: true,
        }
    }
}

impl FirewallErrorCode {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::NotBundled => "E_FW_NOT_BUNDLED",
            Self::NotInApplications => "E_FW_NOT_IN_APPLICATIONS",
            Self::Activation => "E_FW_ACTIVATION",
            Self::Removal => "E_FW_REMOVAL",
            Self::Filter => "E_FW_FILTER",
            Self::ConsentDenied => "E_FW_CONSENT_DENIED",
            Self::Disconnected => "E_FW_DISCONNECTED",
            Self::RuleInvalid => "E_FW_RULE_INVALID",
            Self::UnknownAlert => "E_FW_UNKNOWN_ALERT",
            Self::BlocklistUrl => "E_FW_BLOCKLIST_URL",
            Self::BlocklistFetch => "E_FW_BLOCKLIST_FETCH",
            Self::BlocklistFormat => "E_FW_BLOCKLIST_FORMAT",
            Self::BlocklistTooLarge => "E_FW_BLOCKLIST_TOO_LARGE",
            Self::BlocklistLimit => "E_FW_BLOCKLIST_LIMIT",
            Self::BlocklistFile => "E_FW_BLOCKLIST_FILE",
        }
    }
}
