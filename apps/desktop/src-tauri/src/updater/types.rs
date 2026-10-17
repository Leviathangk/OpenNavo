use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

#[derive(
    Debug,
    Clone,
    Copy,
    Serialize,
    Deserialize,
    specta::Type,
    PartialEq,
    Eq,
    PartialOrd,
    Ord,
    Hash,
    Default,
)]
#[serde(rename_all = "snake_case")]
pub enum UpdateSource {
    #[default]
    Homebrew,
    Mas,
    Sparkle,
    Electron,
    Github,
    Vendor,
    Msupdate,
}

impl UpdateSource {
    pub const ALL: [Self; 7] = [
        Self::Homebrew,
        Self::Mas,
        Self::Sparkle,
        Self::Electron,
        Self::Github,
        Self::Vendor,
        Self::Msupdate,
    ];
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Homebrew => "homebrew",
            Self::Mas => "mas",
            Self::Sparkle => "sparkle",
            Self::Electron => "electron",
            Self::Github => "github",
            Self::Vendor => "vendor",
            Self::Msupdate => "msupdate",
        }
    }
}

/// Missing entries use product defaults, including when older settings are loaded.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq, Default)]
#[serde(transparent)]
pub struct UpdateSources(pub BTreeMap<UpdateSource, bool>);
impl UpdateSources {
    pub fn enabled(&self, source: UpdateSource) -> bool {
        self.0
            .get(&source)
            .copied()
            .unwrap_or(source != UpdateSource::Msupdate)
    }
}
impl specta::Type for UpdateSources {
    fn definition(types: &mut specta::Types) -> specta::datatype::DataType {
        let _ = UpdateSource::definition(types);
        specta::datatype::DataType::Reference(specta_typescript::define(
            "Partial<Record<UpdateSource, boolean>>",
        ))
    }
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq, Default)]
#[serde(rename_all = "snake_case")]
pub enum UpdateChannel {
    #[default]
    Stable,
    Beta,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum UpdateNotesFormat {
    Html,
    Markdown,
    Text,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(untagged, deny_unknown_fields)]
pub enum UpdateNotes {
    Inline {
        format: UpdateNotesFormat,
        body: String,
    },
    Link {
        url: String,
    },
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum UpdateIntegrity {
    Eddsa,
    Sha512,
    Digest,
    Sha256,
    TeamId,
}

/// Exported error vocabulary; AppError.code stays a string for existing errors.
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
pub enum UpdateErrorCode {
    #[serde(rename = "E_UPDATE_INTEGRITY")]
    Integrity,
    #[serde(rename = "E_UPDATE_TEAM_MISMATCH")]
    TeamMismatch,
    #[serde(rename = "E_UPDATE_NOT_NOTARIZED")]
    NotNotarized,
    #[serde(rename = "E_UPDATE_ARCH")]
    Arch,
    #[serde(rename = "E_UPDATE_MIN_OS")]
    MinOs,
    #[serde(rename = "E_UPDATE_FEED")]
    Feed,
    #[serde(rename = "E_UPDATE_DMG_LICENSE")]
    DmgLicense,
    #[serde(rename = "E_UPDATE_SWAP")]
    Swap,
    #[serde(rename = "E_APP_RUNNING")]
    AppRunning,
    #[serde(rename = "E_ADMIN_CANCELED")]
    AdminCanceled,
    #[serde(rename = "E_NOT_IMPLEMENTED")]
    NotImplemented,
}
