//! Strict, bounded electron-builder metadata. No provider code is executed.
use super::{
    feed,
    release::{self, File, Release},
    types::*,
};
use crate::AppError;
use std::os::darwin::fs::MetadataExt;
use std::{fs, path::Path};
use yaml_rust2::{
    Yaml, YamlLoader,
    scanner::{Scanner, TokenType},
};
pub const MAX_MANIFEST: usize = 1024 * 1024;
fn error(message: &str) -> AppError {
    AppError::new("E_UPDATE_FEED", message)
}
pub fn yaml(bytes: &[u8]) -> Result<Yaml, AppError> {
    if bytes.len() > MAX_MANIFEST {
        return Err(error("electron_yaml_size"));
    }
    let text = std::str::from_utf8(bytes).map_err(|_| error("electron_yaml_encoding"))?;
    let mut scanner = Scanner::new(text.chars());
    let mut depth: i32 = 0;
    for (nodes, token) in scanner.by_ref().enumerate() {
        if nodes > 20000 {
            return Err(error("electron_yaml_nodes"));
        }
        match token.1 {
            TokenType::Anchor(_)
            | TokenType::Alias(_)
            | TokenType::Tag(_, _)
            | TokenType::TagDirective(_, _) => return Err(error("electron_yaml_alias_or_tag")),
            TokenType::BlockMappingStart
            | TokenType::BlockSequenceStart
            | TokenType::FlowMappingStart
            | TokenType::FlowSequenceStart => depth += 1,
            TokenType::BlockEnd | TokenType::FlowMappingEnd | TokenType::FlowSequenceEnd => {
                depth -= 1
            }
            _ => {}
        }
        if depth > 16 {
            return Err(error("electron_yaml_depth"));
        }
    }
    if scanner.get_error().is_some() {
        return Err(error("electron_yaml_syntax"));
    }
    let mut docs = YamlLoader::load_from_str(text).map_err(|_| error("electron_yaml_syntax"))?;
    if docs.len() != 1 || docs[0].as_hash().is_none() {
        return Err(error("electron_yaml_document"));
    }
    Ok(docs.remove(0))
}
fn scalar(v: &Yaml) -> Option<String> {
    v.as_str()
        .map(str::to_owned)
        .or_else(|| v.as_i64().map(|n| n.to_string()))
        .or_else(|| v.as_f64().map(|n| n.to_string()))
}
#[derive(Debug, Clone)]
pub struct Config {
    pub base: String,
    pub cache_name: Option<String>,
}
impl Config {
    pub fn parse(bytes: &[u8]) -> Result<Self, AppError> {
        let doc = yaml(bytes)?;
        let base = match doc["provider"].as_str() {
            Some("generic") => feed::https(
                doc["url"]
                    .as_str()
                    .ok_or_else(|| error("electron_generic_url"))?,
            )?,
            Some("github") => {
                if doc["private"].as_bool() == Some(true)
                    || !doc["token"].is_badvalue()
                    || doc["host"].as_str().is_some_and(|s| s != "github.com")
                {
                    return Err(error("electron_private_provider"));
                }
                let valid = |v: &Yaml| {
                    v.as_str()
                        .filter(|s| {
                            !s.is_empty()
                                && s.len() <= 100
                                && *s != "."
                                && *s != ".."
                                && s.bytes()
                                    .all(|b| b.is_ascii_alphanumeric() || b"._-".contains(&b))
                        })
                        .map(str::to_owned)
                };
                let owner = valid(&doc["owner"]).ok_or_else(|| error("electron_github_owner"))?;
                let repo = valid(&doc["repo"]).ok_or_else(|| error("electron_github_repo"))?;
                format!("https://github.com/{owner}/{repo}/releases/latest/download/")
            }
            _ => return Err(error("electron_provider")),
        };
        Ok(Self {
            base,
            cache_name: doc["updaterCacheDirName"]
                .as_str()
                .filter(|n| {
                    !n.is_empty()
                        && n.len() < 200
                        && !n.contains('/')
                        && !n.contains('\\')
                        && *n != "."
                        && *n != ".."
                })
                .map(str::to_owned),
        })
    }
    pub fn local(app: &Path) -> Result<Self, AppError> {
        let p = app.join("Contents/Resources/app-update.yml");
        let meta = fs::symlink_metadata(&p)?;
        if !meta.is_file()
            || meta.len() > MAX_MANIFEST as u64
            || meta.st_flags() & 0x40000000 != 0
            || !p.canonicalize()?.starts_with(app.canonicalize()?)
        {
            return Err(error("electron_local_config"));
        }
        Self::parse(&fs::read(p)?)
    }
    pub fn manifest(&self, channel: UpdateChannel) -> Result<String, AppError> {
        let mut base = reqwest::Url::parse(&self.base).map_err(|_| error("electron_url"))?;
        if !base.path().ends_with('/') {
            base.set_path(&format!("{}/", base.path()));
        }
        Ok(base
            .join(if channel == UpdateChannel::Beta {
                "beta-mac.yml"
            } else {
                "latest-mac.yml"
            })
            .map_err(|_| error("electron_manifest_url"))?
            .into())
    }
}
pub fn parse(bytes: &[u8], url: &str) -> Result<Release, AppError> {
    let doc = yaml(bytes)?;
    let base = reqwest::Url::parse(&feed::https(url)?).map_err(|_| error("electron_url"))?;
    let mut files = vec![];
    let raw = doc["files"]
        .as_vec()
        .cloned()
        .unwrap_or_else(|| vec![doc.clone()]);
    for f in raw {
        let Some(path) = f["url"].as_str().or_else(|| f["path"].as_str()) else {
            continue;
        };
        let file_url = base.join(path).map_err(|_| error("electron_file_url"))?;
        let name = file_url
            .path_segments()
            .and_then(|mut s| s.next_back())
            .unwrap_or("")
            .to_string();
        if !release::supported_file(&name) {
            continue;
        }
        let declared = f["arch"]
            .as_str()
            .map(|s| if s == "x64" { "x86_64" } else { s });
        files.push(File {
            url: feed::https(file_url.as_str())?,
            architecture: declared
                .map(str::to_owned)
                .or_else(|| release::inferred_arch(&name)),
            name,
            size: f["size"].as_i64().filter(|n| *n > 0).map(|n| n as u64),
            sha512: f["sha512"].as_str().map(str::to_owned),
            sha256: None,
        });
    }
    let notes = doc["releaseNotes"].as_str().map(|s| UpdateNotes::Inline {
        format: UpdateNotesFormat::Markdown,
        body: s.into(),
    });
    let result = Release {
        version: scalar(&doc["version"]).ok_or_else(|| error("electron_version"))?,
        version_key: release::default_version_key(),
        notes,
        minimum_os: scalar(&doc["minimumSystemVersion"]),
        files,
    };
    result.validate(UpdateSource::Electron)?;
    Ok(result)
}

/// Only read electron-updater's own pending metadata and its exact SHA-512 file.
/// The selected manifest binds that digest to the selected version and URL; a
/// random file in the cache (or just a newer manifest) is never readiness proof.
pub fn pending(config: &Config, artifact: &release::Artifact) -> Result<bool, AppError> {
    let Some(home) = std::env::var_os("HOME") else {
        return Ok(false);
    };
    pending_in(
        Path::new(&home).join("Library/Caches").as_path(),
        config,
        artifact,
    )
}
pub fn pending_in(
    cache: &Path,
    config: &Config,
    artifact: &release::Artifact,
) -> Result<bool, AppError> {
    use base64::{Engine, engine::general_purpose::STANDARD};
    use sha2::{Digest, Sha512};
    use std::{
        io::Read,
        os::unix::fs::{MetadataExt as UnixMetadataExt, OpenOptionsExt},
    };
    let Some(name) = &config.cache_name else {
        return Ok(false);
    };
    let root = cache.join(name).join("pending");
    if !crate::uninstall::rules::parents_safe(&root) {
        return Ok(false);
    }
    let open = |path: &Path, max: u64| -> Result<fs::File, AppError> {
        let file = fs::OpenOptions::new()
            .read(true)
            .custom_flags(nix::libc::O_NOFOLLOW | nix::libc::O_NONBLOCK)
            .open(path)?;
        let m = file.metadata()?;
        if !m.is_file()
            || m.len() > max
            || m.st_flags() & 0x40000000 != 0
            || m.uid() != unsafe { nix::libc::getuid() }
        {
            return Err(error("electron_pending_file"));
        }
        Ok(file)
    };
    let mut info = String::new();
    let Ok(metadata) = open(&root.join("update-info.json"), 16384) else {
        return Ok(false);
    };
    metadata.take(16385).read_to_string(&mut info)?;
    if info.len() > 16384 {
        return Ok(false);
    }
    let info: serde_json::Value =
        serde_json::from_str(&info).map_err(|_| error("electron_pending_json"))?;
    let Some(file) = info["fileName"].as_str().filter(|f| {
        !f.is_empty() && !f.contains('/') && !f.contains('\\') && *f != "." && *f != ".."
    }) else {
        return Ok(false);
    };
    let Some(expected) = artifact.file.sha512.as_deref() else {
        return Ok(false);
    };
    if info["sha512"].as_str() != Some(expected) {
        return Ok(false);
    }
    let Ok(mut downloaded) = open(&root.join(file), 1024 * 1024 * 1024) else {
        return Ok(false);
    };
    let before = downloaded.metadata()?;
    if artifact.file.size.is_some_and(|n| n != before.len()) {
        return Ok(false);
    }
    let mut hash = Sha512::new();
    let mut buffer = [0u8; 65536];
    let mut total = 0u64;
    loop {
        let n = downloaded.read(&mut buffer)?;
        if n == 0 {
            break;
        };
        total += n as u64;
        if total > 1024 * 1024 * 1024 {
            return Ok(false);
        }
        hash.update(&buffer[..n]);
    }
    let after = downloaded.metadata()?;
    Ok(before.len() == after.len()
        && before.mtime() == after.mtime()
        && before.mtime_nsec() == after.mtime_nsec()
        && STANDARD.encode(hash.finalize()) == expected)
}
