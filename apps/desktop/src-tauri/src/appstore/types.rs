use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct MasApp {
    pub adam_id: u64,
    pub bundle_id: String,
    pub name: String,
    pub seller: String,
    pub version: String,
    pub release_notes: Option<String>,
    pub released_at: Option<String>,
    pub minimum_os: Option<String>,
    pub size_bytes: Option<u64>,
    pub price: MasPrice,
    pub formatted_price: String,
    pub currency: String,
    pub rating: Option<f64>,
    pub rating_count: Option<u64>,
    pub icon_url: Option<String>,
    pub screenshots: Vec<String>,
    pub description: String,
    pub genres: Vec<String>,
    pub store_url: String,
    pub country: String,
}

/// A price cannot serialize as null (serde_json maps non-finite floats to null).
#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq)]
#[serde(try_from = "f64", into = "f64")]
pub struct MasPrice(f64);
impl TryFrom<f64> for MasPrice {
    type Error = &'static str;
    fn try_from(value: f64) -> Result<Self, Self::Error> {
        if value.is_finite() && value >= 0.0 {
            Ok(Self(value))
        } else {
            Err("invalid App Store price")
        }
    }
}
impl From<MasPrice> for f64 {
    fn from(value: MasPrice) -> Self {
        value.0
    }
}
impl specta::Type for MasPrice {
    fn definition(_: &mut specta::Types) -> specta::datatype::DataType {
        specta_typescript::define("number").into()
    }
}

/// Missing bundle IDs are absent, never fabricated or serialized as null.
#[derive(Debug, Clone, Serialize, Deserialize, Default, PartialEq)]
#[serde(transparent)]
pub struct MasBundleLookup(pub BTreeMap<String, MasApp>);
impl specta::Type for MasBundleLookup {
    fn definition(types: &mut specta::Types) -> specta::datatype::DataType {
        let _ = MasApp::definition(types);
        specta::datatype::DataType::Reference(specta_typescript::define(
            "Partial<Record<string, MasApp>>",
        ))
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct MasInstalled {
    pub adam_id: u64,
    pub bundle_id: String,
    pub name: String,
    pub version: String,
    pub path: String,
    pub installed_at: Option<i64>,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct MasUpdate {
    pub adam_id: u64,
    pub bundle_id: String,
    pub name: String,
    pub installed_version: String,
    pub store_version: String,
    pub released_at: Option<String>,
    pub release_notes: Option<String>,
    pub size_bytes: Option<u64>,
    pub confidence: MasConfidence,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum MasConfidence {
    High,
    Low,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct MasStatus {
    pub country: String,
    pub country_source: MasCountrySource,
    pub engine: MasEngineStatus,
    pub admin_install: bool,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum MasCountrySource {
    Account,
    System,
    Setting,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum MasEngineStatus {
    Ok,
    Missing,
    Unsupported,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
pub struct AppStoreChange {
    pub reason: AppStoreChangeReason,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum AppStoreChangeReason {
    Installed,
    Updates,
    Region,
}

/// Exported vocabulary for AppError.code; detail carries serialized structured
/// context (retryAfterSecs, or domain/code) where available.
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
pub enum MasErrorCode {
    #[serde(rename = "E_MAS_NOT_SIGNED_IN")]
    NotSignedIn,
    #[serde(rename = "E_MAS_NOT_OWNED")]
    NotOwned,
    #[serde(rename = "E_MAS_PAID")]
    Paid,
    #[serde(rename = "E_MAS_OTHER_ACCOUNT")]
    OtherAccount,
    #[serde(rename = "E_MAS_REGION")]
    Region,
    #[serde(rename = "E_MAS_RATE_LIMITED")]
    RateLimited,
    #[serde(rename = "E_MAS_ENGINE")]
    Engine,
    #[serde(rename = "E_MAS_HELPER_MISSING")]
    HelperMissing,
    #[serde(rename = "E_ADMIN_CANCELED")]
    AdminCanceled,
}
