//! A resolved artifact is data, never an executable updater recipe.
use super::{feed, types::*, version};
use crate::AppError;
use base64::{Engine, engine::general_purpose::STANDARD};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256, Sha512};

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct File {
    pub url: String,
    pub name: String,
    pub size: Option<u64>,
    pub architecture: Option<String>,
    pub sha256: Option<String>,
    pub sha512: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Release {
    pub version: String,
    #[serde(default = "default_version_key")]
    pub version_key: String,
    pub notes: Option<UpdateNotes>,
    pub minimum_os: Option<String>,
    pub files: Vec<File>,
}
pub fn default_version_key() -> String {
    "CFBundleShortVersionString".into()
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Artifact {
    pub file: File,
    pub version: String,
    pub version_key: String,
}
fn invalid(message: &str) -> AppError {
    AppError::new("E_UPDATE_FEED", message)
}
pub fn version_key(key: &str) -> bool {
    matches!(
        key,
        "CFBundleVersion" | "CFBundleShortVersionString" | "Wave Build Version Number"
    )
}
pub fn inferred_arch(name: &str) -> Option<String> {
    let name = name.to_ascii_lowercase();
    if name.contains("universal") {
        Some("universal".into())
    } else if name.contains("arm64") || name.contains("aarch64") {
        Some("arm64".into())
    } else if name.contains("x64")
        || name.contains("x86_64")
        || name.contains("amd64")
        || name.contains("intel")
    {
        Some("x86_64".into())
    } else {
        None
    }
}
pub fn supported_file(name: &str) -> bool {
    let name = name.to_ascii_lowercase();
    [".zip", ".dmg", ".pkg"]
        .iter()
        .any(|ext| name.ends_with(ext))
}
impl Release {
    pub fn validate(&self, source: UpdateSource) -> Result<(), AppError> {
        if self.version.is_empty()
            || self.version.len() > 128
            || self.version.chars().any(char::is_control)
            || !version_key(&self.version_key)
            || self.files.is_empty()
            || self.files.len() > 100
        {
            return Err(invalid("release_metadata"));
        }
        if let Some(UpdateNotes::Link { url }) = &self.notes {
            feed::https(url)?;
        }
        for file in &self.files {
            feed::https(&file.url)?;
            if !supported_file(&file.name)
                || file.name.len() > 512
                || file.size.is_some_and(|n| n == 0 || n > 1024 * 1024 * 1024)
                || file
                    .architecture
                    .as_deref()
                    .is_some_and(|a| !matches!(a, "arm64" | "x86_64" | "universal"))
            {
                return Err(invalid("release_file"));
            }
            if file
                .sha256
                .as_ref()
                .is_some_and(|s| s.len() != 64 || !s.bytes().all(|b| b.is_ascii_hexdigit()))
                || file
                    .sha512
                    .as_ref()
                    .is_some_and(|s| STANDARD.decode(s).map_or(true, |b| b.len() != 64))
            {
                return Err(invalid("release_digest"));
            }
            if (source == UpdateSource::Github && file.sha256.is_none())
                || (source == UpdateSource::Electron && file.sha512.is_none())
            {
                return Err(invalid("required_release_digest"));
            }
            if source == UpdateSource::Github {
                let u = reqwest::Url::parse(&file.url).map_err(|_| invalid("release_url"))?;
                if u.host_str() != Some("github.com") || !u.path().contains("/releases/download/") {
                    return Err(invalid("github_direct_download_required"));
                }
            }
        }
        Ok(())
    }
    pub fn select(&self, arch: &str, os: &str) -> Option<&File> {
        if self
            .minimum_os
            .as_deref()
            .is_some_and(|min| version::compare(os, min).is_lt())
        {
            return None;
        }
        self.files
            .iter()
            .filter(|f| {
                f.architecture
                    .as_deref()
                    .is_none_or(|a| a == arch || a == "universal")
            })
            .max_by_key(|f| {
                (
                    match f.architecture.as_deref() {
                        Some(a) if a == arch => 3,
                        Some("universal") => 2,
                        _ => 1,
                    },
                    f.name.to_ascii_lowercase().ends_with(".zip"),
                )
            })
    }
}
impl Artifact {
    pub fn integrity(&self, source: UpdateSource) -> UpdateIntegrity {
        if self.file.sha512.is_some() {
            UpdateIntegrity::Sha512
        } else if source == UpdateSource::Github {
            UpdateIntegrity::Digest
        } else if self.file.sha256.is_some() {
            UpdateIntegrity::Sha256
        } else {
            UpdateIntegrity::TeamId
        }
    }
    pub fn verify(&self, bytes: &[u8], source: UpdateSource) -> Result<String, AppError> {
        let fail = || AppError::new("E_UPDATE_DIGEST", "release_download_digest");
        if self.file.size.is_some_and(|n| n != bytes.len() as u64) {
            return Err(fail());
        }
        if source == UpdateSource::Github && self.file.sha256.is_none()
            || source == UpdateSource::Electron && self.file.sha512.is_none()
        {
            return Err(fail());
        }
        let digest = format!("{:x}", Sha256::digest(bytes));
        if self
            .file
            .sha256
            .as_ref()
            .is_some_and(|s| !s.eq_ignore_ascii_case(&digest))
        {
            return Err(fail());
        }
        if let Some(expected) = &self.file.sha512 {
            let expected = STANDARD.decode(expected).map_err(|_| fail())?;
            if expected.as_slice() != Sha512::digest(bytes).as_slice() {
                return Err(fail());
            }
        }
        Ok(digest)
    }
}
