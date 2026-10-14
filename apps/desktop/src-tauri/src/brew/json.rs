use serde::Deserialize;
use serde_json::Value;
#[derive(Debug, Clone, Deserialize, Default)]
pub struct Info {
    #[serde(default)]
    pub formulae: Vec<Formula>,
    #[serde(default)]
    pub casks: Vec<Cask>,
}
#[derive(Debug, Clone, Deserialize)]
pub struct Formula {
    pub name: String,
    #[serde(default)]
    pub full_name: Option<String>,
    #[serde(default)]
    pub installed: Vec<Installation>,
    #[serde(default, deserialize_with = "bool_or_null")]
    pub pinned: bool,
    #[serde(default, deserialize_with = "bool_or_null")]
    pub outdated: bool,
    #[serde(default)]
    pub dependencies: Vec<String>,
    #[serde(default)]
    pub versions: Versions,
    #[serde(default)]
    pub revision: u32,
}
#[derive(Debug, Clone, Deserialize, Default)]
pub struct Versions {
    pub stable: Option<String>,
}
#[derive(Debug, Clone, Deserialize)]
pub struct Installation {
    pub version: String,
    #[serde(default, deserialize_with = "bool_or_null")]
    pub installed_on_request: bool,
    #[serde(default, deserialize_with = "bool_or_null")]
    pub installed_as_dependency: bool,
    pub time: Option<i64>,
    #[serde(default)]
    pub runtime_dependencies: Vec<RuntimeDependency>,
}
#[derive(Debug, Clone, Deserialize)]
pub struct RuntimeDependency {
    pub full_name: String,
    #[serde(default, deserialize_with = "bool_or_null")]
    pub declared_directly: bool,
}
#[derive(Debug, Clone, Deserialize)]
pub struct Cask {
    pub token: String,
    /// homebrew/cask for official casks; null for casks brew no longer finds in any tap.
    #[serde(default)]
    pub tap: Option<String>,
    #[serde(default)]
    pub name: Vec<String>,
    pub version: String,
    #[serde(default)]
    pub installed: Value,
    pub installed_time: Option<i64>,
    #[serde(default, deserialize_with = "bool_or_null")]
    pub pinned: bool,
    #[serde(default, deserialize_with = "bool_or_null")]
    pub outdated: bool,
    #[serde(default, deserialize_with = "bool_or_null")]
    pub auto_updates: bool,
    #[serde(default)]
    pub artifacts: Option<Vec<Value>>, // None means artifact metadata unavailable; Some([]) means known to have no artifacts.
}

pub(crate) fn bool_or_null<'de, D: serde::Deserializer<'de>>(
    deserializer: D,
) -> Result<bool, D::Error> {
    Ok(Option::<bool>::deserialize(deserializer)?.unwrap_or(false))
}
