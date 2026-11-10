//! Optional Hermes runtime installer.
//!
//! The app no longer bundles the ~330 MB runtime. A release build points
//! `HERMES_RUNTIME_URL` at the pinned tarball (or the default CDN path);
//! development can point `HERMES_RUNTIME_SOURCE` at the prepared directory
//! or tarball. Everything is installed under the single OpenNavoData root.

use std::io::Read;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};

use flate2::read::GzDecoder;
use futures_util::StreamExt;
use serde::Serialize;
use sha2::{Digest, Sha256};
use tauri::{AppHandle, Emitter};
use tokio::io::AsyncWriteExt;

use super::{
    AppError, HermesRuntimeState, HermesRuntimeStatus, data_root, hermes_home, runtime_root,
    secret_delete, status, stop,
};

const DEFAULT_RUNTIME_URL: &str = "https://github.com/opennavo/OpenNavo/releases/download/hermes-runtime-v0.23.0/runtime-0.23.0-macos-arm64.tar.gz";
const DEFAULT_RUNTIME_SHA256: &str =
    "36601db39c3a20b381192dfa08969b7f0a4fe52d70d8e8a1475a0986d7c244fc";
const PROVIDERS: &[&str] = &[
    "nous",
    "openai",
    "anthropic",
    "google",
    "openrouter",
    "local",
    "custom",
    "codex",
    "claude-code",
    "copilot",
    "opencode",
    "opencode-go",
];

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct HermesInstallProgress {
    pub phase: String,
    pub downloaded: u64,
    pub total: Option<u64>,
    pub percent: u8,
}

fn emit(app: &AppHandle, phase: &str, downloaded: u64, total: Option<u64>, percent: u8) {
    let _ = app.emit(
        "hermes-install-progress",
        HermesInstallProgress {
            phase: phase.to_owned(),
            downloaded,
            total,
            percent,
        },
    );
}

pub async fn install(
    app: AppHandle,
    state: &HermesRuntimeState,
) -> Result<HermesRuntimeStatus, AppError> {
    if state.installing.swap(true, Ordering::SeqCst) {
        return Err(AppError::new(
            "E_HERMES_INSTALLING",
            "Hermes is already being installed.",
        ));
    }
    state.install_cancel.store(false, Ordering::SeqCst);
    let result = install_inner(&app, state).await;
    state.installing.store(false, Ordering::SeqCst);
    result
}

async fn install_inner(
    app: &AppHandle,
    state: &HermesRuntimeState,
) -> Result<HermesRuntimeStatus, AppError> {
    if std::env::consts::ARCH != "aarch64" {
        return Err(AppError::new(
            "E_HERMES_INSTALL",
            "This Hermes runtime build is for Apple Silicon (arm64).",
        ));
    }
    let _ = stop(state).await;
    let root = data_root(app)?;
    let runtime = runtime_root(app)?;
    let temp = root.join("runtime.tmp");
    let _ = std::fs::remove_dir_all(&temp);
    std::fs::create_dir_all(&temp)?;
    emit(app, "preparing", 0, None, 1);

    if let Some(source) = std::env::var_os("HERMES_RUNTIME_SOURCE") {
        let source = PathBuf::from(source);
        if source.is_dir() {
            emit(app, "copying", 0, None, 10);
            copy_directory(&source, &temp)?;
        } else if source.is_file() {
            emit(app, "extracting", 0, None, 35);
            extract_tar(&source, &temp).await?;
        } else {
            return Err(AppError::new(
                "E_HERMES_INSTALL",
                "HERMES_RUNTIME_SOURCE does not exist.",
            ));
        }
    } else {
        let url =
            std::env::var("HERMES_RUNTIME_URL").unwrap_or_else(|_| DEFAULT_RUNTIME_URL.to_owned());
        let archive = root.join("runtime.tar.gz");
        download(app, &url, &archive, &state.install_cancel).await?;
        emit(app, "verifying", 0, None, 80);
        let expected = std::env::var("HERMES_RUNTIME_SHA256")
            .unwrap_or_else(|_| DEFAULT_RUNTIME_SHA256.to_owned());
        let actual = sha256_file(&archive).await?;
        if !actual.eq_ignore_ascii_case(expected.trim()) {
            let _ = std::fs::remove_file(&archive);
            return Err(AppError::new(
                "E_HERMES_INSTALL",
                "Hermes runtime checksum does not match.",
            ));
        }
        emit(app, "extracting", 0, None, 85);
        extract_tar(&archive, &temp).await?;
        let _ = std::fs::remove_file(&archive);
    }

    if state.install_cancel.load(Ordering::SeqCst) {
        let _ = std::fs::remove_dir_all(&temp);
        return Err(AppError::new(
            "E_HERMES_INSTALL_CANCELLED",
            "Hermes install cancelled.",
        ));
    }

    let bundled_plugins = temp.join("plugins");
    if bundled_plugins.is_dir() {
        let home = hermes_home(app)?;
        std::fs::create_dir_all(&home)?;
        copy_directory(&bundled_plugins, &home.join("plugins"))?;
    }

    let wrapper = temp.join("bin").join("hermes");
    if !wrapper.is_file() {
        let _ = std::fs::remove_dir_all(&temp);
        return Err(AppError::new(
            "E_HERMES_INSTALL",
            "The Hermes package does not contain bin/hermes.",
        ));
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let _ = std::fs::set_permissions(&wrapper, std::fs::Permissions::from_mode(0o755));
    }
    if let Some(parent) = runtime.parent() {
        std::fs::create_dir_all(parent)?;
    }
    let backup = root.join("runtime.old");
    let _ = std::fs::remove_dir_all(&backup);
    if runtime.exists() {
        std::fs::rename(&runtime, &backup)?;
    }
    if let Err(error) = std::fs::rename(&temp, &runtime) {
        if backup.exists() {
            let _ = std::fs::rename(&backup, &runtime);
        }
        return Err(
            AppError::new("E_HERMES_INSTALL", "Could not activate the Hermes runtime.")
                .with_detail(error.to_string()),
        );
    }
    let _ = std::fs::remove_dir_all(&backup);
    emit(app, "done", 0, None, 100);
    let _ = state;
    status(app.clone()).await
}

fn copy_directory(source: &Path, destination: &Path) -> Result<(), AppError> {
    let status = std::process::Command::new("ditto")
        .arg(source)
        .arg(destination)
        .status()
        .map_err(|error| {
            AppError::new("E_HERMES_INSTALL", "Could not copy the Hermes runtime.")
                .with_detail(error.to_string())
        })?;
    if !status.success() {
        return Err(AppError::new(
            "E_HERMES_INSTALL",
            "Could not copy the Hermes runtime.",
        ));
    }
    Ok(())
}

async fn download(
    app: &AppHandle,
    url: &str,
    archive: &Path,
    cancel: &AtomicBool,
) -> Result<(), AppError> {
    let response = reqwest::Client::new()
        .get(url)
        .send()
        .await
        .map_err(|error| {
            AppError::new("E_HERMES_INSTALL", "Could not download the Hermes runtime.")
                .with_detail(error.to_string())
        })?;
    if !response.status().is_success() {
        return Err(AppError::new(
            "E_HERMES_INSTALL",
            format!(
                "Hermes runtime download failed with HTTP {}.",
                response.status()
            ),
        ));
    }
    let total = response.content_length();
    let mut file = tokio::fs::File::create(archive).await?;
    let mut stream = response.bytes_stream();
    let mut downloaded = 0_u64;
    while let Some(chunk) = stream.next().await {
        if cancel.load(Ordering::SeqCst) {
            let _ = file.flush().await;
            let _ = std::fs::remove_file(archive);
            return Err(AppError::new(
                "E_HERMES_INSTALL_CANCELLED",
                "Hermes install cancelled.",
            ));
        }
        let chunk = chunk.map_err(|error| {
            AppError::new("E_HERMES_INSTALL", "Hermes download interrupted.")
                .with_detail(error.to_string())
        })?;
        file.write_all(&chunk).await?;
        downloaded += chunk.len() as u64;
        let percent = total
            .filter(|total| *total > 0)
            .map(|total| ((downloaded.saturating_mul(100) / total).min(79)) as u8)
            .unwrap_or(20);
        emit(app, "downloading", downloaded, total, percent);
    }
    file.flush().await?;
    Ok(())
}

async fn extract_tar(archive: &Path, destination: &Path) -> Result<(), AppError> {
    let archive = archive.to_path_buf();
    let destination = destination.to_path_buf();
    tokio::task::spawn_blocking(move || -> Result<(), AppError> {
        let file = std::fs::File::open(&archive)?;
        let decoder = GzDecoder::new(file);
        let mut tar = tar::Archive::new(decoder);
        tar.unpack(&destination)?;
        Ok(())
    })
    .await
    .map_err(|error| {
        AppError::new("E_HERMES_INSTALL", "Hermes extraction failed.")
            .with_detail(error.to_string())
    })?
}

async fn sha256_file(path: &Path) -> Result<String, AppError> {
    let path = path.to_path_buf();
    tokio::task::spawn_blocking(move || -> Result<String, AppError> {
        let mut file = std::fs::File::open(path)?;
        let mut hasher = Sha256::new();
        let mut buffer = [0_u8; 64 * 1024];
        loop {
            let read = file.read(&mut buffer)?;
            if read == 0 {
                break;
            }
            hasher.update(&buffer[..read]);
        }
        Ok(format!("{:x}", hasher.finalize()))
    })
    .await
    .map_err(|error| {
        AppError::new("E_HERMES_INSTALL", "Hermes checksum failed.").with_detail(error.to_string())
    })?
}

pub fn cancel(state: &HermesRuntimeState) {
    state.install_cancel.store(true, Ordering::SeqCst);
}

pub async fn uninstall(
    app: AppHandle,
    state: &HermesRuntimeState,
) -> Result<HermesRuntimeStatus, AppError> {
    stop(state).await?;
    let root = data_root(&app)?;
    if root.exists() {
        std::fs::remove_dir_all(&root)?;
    }
    for provider in PROVIDERS {
        let _ = secret_delete(provider);
    }
    status(app).await
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn copies_a_prepared_runtime_directory() {
        let source = tempfile::tempdir().unwrap();
        std::fs::create_dir_all(source.path().join("bin")).unwrap();
        std::fs::write(source.path().join("bin").join("hermes"), "#!/bin/sh\n").unwrap();
        let destination = tempfile::tempdir().unwrap();
        copy_directory(source.path(), destination.path()).unwrap();
        assert!(destination.path().join("bin").join("hermes").is_file());
    }

    #[tokio::test]
    async fn extracts_a_runtime_tarball() {
        let source = tempfile::tempdir().unwrap();
        std::fs::create_dir_all(source.path().join("bin")).unwrap();
        std::fs::write(source.path().join("bin").join("hermes"), "#!/bin/sh\n").unwrap();
        let archive = source.path().join("runtime.tar.gz");
        {
            let file = std::fs::File::create(&archive).unwrap();
            let encoder = flate2::write::GzEncoder::new(file, flate2::Compression::default());
            let mut builder = tar::Builder::new(encoder);
            builder.append_dir_all(".", source.path()).unwrap();
            builder.finish().unwrap();
        }
        let destination = tempfile::tempdir().unwrap();
        extract_tar(&archive, destination.path()).await.unwrap();
        assert!(destination.path().join("bin").join("hermes").is_file());
    }
}
