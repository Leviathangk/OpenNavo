use super::{archive, feed, gate, service::Candidate, swap::SwapRequest};
use crate::{AppError, brew::pty::Progress, uninstall::rules::Snapshot};
use std::{
    fs,
    path::{Path, PathBuf},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};

pub struct Stage {
    pub path: PathBuf,
    snapshot: Snapshot,
}
impl Stage {
    pub fn create(base: &Path) -> Result<Self, AppError> {
        use std::os::unix::fs::DirBuilderExt;
        match fs::DirBuilder::new().mode(0o700).create(base) {
            Ok(()) => {}
            Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => {}
            Err(e) => return Err(e.into()),
        }
        if fs::symlink_metadata(base)?.is_symlink() {
            return Err(error("staging_symlink"));
        }
        let base = base.canonicalize()?;
        crate::appstore::install::validate_staging(&base, unsafe { nix::libc::getuid() })?;
        let path = base.join(uuid::Uuid::now_v7().to_string());
        fs::DirBuilder::new().mode(0o700).create(&path)?;
        Ok(Self {
            snapshot: Snapshot::capture(&path)?,
            path,
        })
    }
}
impl Drop for Stage {
    fn drop(&mut self) {
        if self.snapshot.still(&self.path) {
            let _ = fs::remove_dir_all(&self.path);
        }
    }
}
pub type ProgressSink = Arc<dyn Fn(&Progress) -> Result<(), AppError> + Send + Sync>;
fn error(message: &str) -> AppError {
    AppError::new("E_UPDATE_INTEGRITY", message)
}
pub async fn prepare(
    candidate: &Candidate,
    stage: &Stage,
    cancel: Arc<AtomicBool>,
    progress: ProgressSink,
) -> Result<SwapRequest, AppError> {
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(900))
        .redirect(reqwest::redirect::Policy::custom(|attempt| {
            if attempt.previous().len() >= 5 || feed::https(attempt.url().as_str()).is_err() {
                attempt.error("unsafe update redirect")
            } else {
                attempt.follow()
            }
        }))
        .build()?;
    prepare_with_client(candidate, stage, cancel, progress, &client).await
}
/// The client argument permits a local TLS fixture with its own trust root. There
/// is no IPC/environment option to disable production transport verification.
pub async fn prepare_with_client(
    candidate: &Candidate,
    stage: &Stage,
    cancel: Arc<AtomicBool>,
    progress: ProgressSink,
    client: &reqwest::Client,
) -> Result<SwapRequest, AppError> {
    if candidate.item.informational_url.is_some() || candidate.item.relaunch_pending {
        return Err(AppError::new(
            "E_NOT_INSTALLABLE",
            "informational_or_pending_update",
        ));
    }
    candidate.validate_identity()?;
    let (url, length, verification) = if let Some(artifact) = &candidate.artifact {
        (
            feed::https(&artifact.file.url)?,
            artifact.file.size,
            serde_json::to_string(artifact)?,
        )
    } else {
        let item = candidate
            .feed_item
            .as_ref()
            .ok_or_else(|| error("missing_feed_item"))?;
        let url = feed::https(
            item.url
                .as_deref()
                .ok_or_else(|| error("missing_download_url"))?,
        )?;
        let key = candidate
            .public_key
            .as_ref()
            .ok_or_else(|| error("missing_eddsa_key"))?;
        let signature = item
            .ed_signature
            .as_ref()
            .ok_or_else(|| error("missing_eddsa_signature"))?;
        (url, item.length, format!("{key}\n{signature}"))
    };
    let path = stage.path.join("archive");
    super::transfer::fetch(
        client,
        &url,
        &format!("{url}\n{verification}"),
        length,
        stage.path.parent().ok_or_else(|| error("staging_parent"))?,
        &path,
        cancel.clone(),
        progress,
    )
    .await?;
    let root = stage.path.clone();
    let source = candidate.clone();
    crate::blocking(move || {
        let bytes = fs::read(&path)?;
        let digest = if let Some(artifact) = &source.artifact {
            artifact.verify(&bytes, source.item.source)?
        } else {
            gate::verify_archive(
                &bytes,
                source.public_key.as_deref().unwrap_or(""),
                source
                    .feed_item
                    .as_ref()
                    .and_then(|f| f.ed_signature.as_deref())
                    .unwrap_or(""),
                length,
                None,
            )?
        };
        fs::write(root.join("verified.sha256"), &digest)?;
        if cancel.load(Ordering::Acquire) {
            return Err(AppError::new("E_INTERRUPTED", "update_canceled"));
        }
        let destination = root.join("extracted");
        let package = source.feed_item.as_ref().is_some_and(|i| i.package)
            || source
                .artifact
                .as_ref()
                .is_some_and(|a| a.file.name.to_ascii_lowercase().ends_with(".pkg"));
        let artifact = if bytes.starts_with(b"PK\x03\x04") {
            archive::extract_zip_artifact(&path, &destination, package)?
        } else if bytes.len() >= 512 && &bytes[bytes.len() - 512..bytes.len() - 508] == b"koly" {
            archive::extract_dmg_artifact(&path, &destination, package)?
        } else if bytes.starts_with(b"xar!") {
            path.clone()
        } else {
            return Err(AppError::new("E_UPDATE_FEED", "unsupported_update_archive"));
        };
        let (prepared, package) = if package || bytes.starts_with(b"xar!") {
            use gate::Inspector;
            use sha2::{Digest, Sha256};
            let old = gate::NativeInspector.inspect(&source.app_path)?;
            super::package::verify_signature(&artifact, &old.team_id)?;
            let prepared = super::package::payload(
                &artifact,
                &root.join("package"),
                &source.app_path,
                &old.identifier,
            )?;
            let package = super::package::Artifact {
                snapshot: Snapshot::capture(&artifact)?,
                sha256: format!("{:x}", Sha256::digest(fs::read(&artifact)?)),
                path: artifact,
            };
            (prepared, Some(package))
        } else {
            (artifact, None)
        };
        let plist = plist::Value::from_file(prepared.join("Contents/Info.plist"))
            .map_err(|_| error("download_bundle_info"))?;
        let (key, expected) = if let Some(artifact) = &source.artifact {
            (artifact.version_key.as_str(), artifact.version.as_str())
        } else {
            (
                "CFBundleVersion",
                source
                    .feed_item
                    .as_ref()
                    .map(|i| i.version.as_str())
                    .unwrap_or(""),
            )
        };
        let actual = plist
            .as_dictionary()
            .and_then(|d| d.get(key))
            .and_then(|v| v.as_string());
        if actual.is_none_or(|v| {
            !super::version::compare_source(source.item.source, v, expected)
                .is_ok_and(|o| o.is_eq())
        }) {
            return Err(error("feed_bundle_version_mismatch"));
        }
        Ok(SwapRequest {
            source: source.item.source,
            installed: source.app_path,
            installed_snapshot: source.snapshot,
            prepared_snapshot: Snapshot::capture(&prepared)?,
            prepared,
            expected_team: source.expected_team,
            package,
        })
    })
    .await
}
