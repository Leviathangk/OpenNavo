//! A canceled/failed transfer keeps only a private partial file. Range replies are
//! bound to a validator; the caller still verifies the complete EdDSA signature.
use super::download::ProgressSink;
use crate::{AppError, brew::pty::Progress, model::TaskPhase};
use reqwest::{Client, StatusCode, header};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    fs,
    path::Path,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};
use tokio::io::{AsyncSeekExt, AsyncWriteExt};

const MAXIMUM: u64 = 1024 * 1024 * 1024;
fn error(message: &str) -> AppError {
    AppError::new("E_UPDATE_INTEGRITY", message)
}
#[derive(Default, Serialize, Deserialize)]
struct Metadata {
    validator: Option<String>,
    total: Option<u64>,
}

fn directory(path: &Path) -> Result<(), AppError> {
    use std::os::unix::fs::DirBuilderExt;
    match fs::DirBuilder::new().mode(0o700).create(path) {
        Ok(()) => {}
        Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => {}
        Err(e) => return Err(e.into()),
    }
    crate::appstore::install::validate_staging(path, unsafe { nix::libc::getuid() })
}
fn range(value: &str) -> Option<(u64, u64, u64)> {
    let (span, total) = value.strip_prefix("bytes ")?.split_once('/')?;
    let (start, end) = span.split_once('-')?;
    Some((start.parse().ok()?, end.parse().ok()?, total.parse().ok()?))
}
fn validator(headers: &header::HeaderMap) -> Option<String> {
    headers
        .get(header::ETAG)
        .and_then(|v| v.to_str().ok())
        .filter(|v| v.starts_with('"') && v.ends_with('"'))
        .or_else(|| {
            headers
                .get(header::LAST_MODIFIED)
                .and_then(|v| v.to_str().ok())
        })
        .filter(|v| v.len() <= 1024)
        .map(str::to_owned)
}
#[allow(clippy::too_many_arguments)]
pub async fn fetch(
    client: &Client,
    url: &str,
    identity: &str,
    expected: Option<u64>,
    root: &Path,
    destination: &Path,
    cancel: Arc<AtomicBool>,
    progress: ProgressSink,
) -> Result<(), AppError> {
    use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
    if expected.is_some_and(|n| n > MAXIMUM) {
        return Err(error("download_size_limit"));
    }
    let root = root.join("downloads");
    directory(&root)?;
    let cache = root.join(format!("{:x}", Sha256::digest(identity.as_bytes())));
    directory(&cache)?;
    let path = cache.join("partial");
    let file = fs::OpenOptions::new()
        .read(true)
        .write(true)
        .create(true)
        .truncate(false)
        .mode(0o600)
        .custom_flags(nix::libc::O_NOFOLLOW)
        .open(&path)?;
    let info = file.metadata()?;
    if !info.is_file()
        || info.uid() != unsafe { nix::libc::getuid() }
        || info.mode() & 0o077 != 0
        || info.nlink() != 1
    {
        return Err(error("download_cache_file"));
    }
    // The lock's lifetime includes the final copy, including across task/process restarts.
    file.try_lock()
        .map_err(|_| AppError::new("E_BUSY", "download_in_progress"))?;
    let meta_path = cache.join("metadata.json");
    let mut meta: Metadata = fs::symlink_metadata(&meta_path)
        .ok()
        .filter(|m| m.is_file() && !m.is_symlink() && m.len() <= 4096)
        .and_then(|_| fs::read(&meta_path).ok())
        .and_then(|v| serde_json::from_slice(&v).ok())
        .unwrap_or_default();
    let mut offset = info.len();
    if offset > MAXIMUM
        || meta.validator.is_none()
        || meta.total.is_none_or(|n| offset >= n)
        || expected.zip(meta.total).is_some_and(|(a, b)| a != b)
    {
        file.set_len(0)?;
        offset = 0;
        meta = Metadata::default();
    }
    let mut output = tokio::fs::File::from_std(file.try_clone()?);
    output.seek(std::io::SeekFrom::Start(offset)).await?;
    let result = async {
    let mut request = client.get(url).header(header::ACCEPT_ENCODING, "identity");
    if offset > 0 {
        request = request
            .header(header::RANGE, format!("bytes={offset}-"))
            .header(header::IF_RANGE, meta.validator.as_deref().unwrap());
    }
    let sending = request.send();
    tokio::pin!(sending);
    let mut response = loop {
        if cancel.load(Ordering::Acquire) {
            return Err(AppError::new("E_INTERRUPTED", "update_canceled"));
        }
        tokio::select! { value = &mut sending => break value?, _ = tokio::time::sleep(Duration::from_millis(100)) => {} }
    };
    if response.status() == StatusCode::PARTIAL_CONTENT {
        let span = response
            .headers()
            .get(header::CONTENT_RANGE)
            .and_then(|v| v.to_str().ok())
            .and_then(range);
        if offset == 0
            || span.is_none_or(|(start, end, total)| {
                start != offset
                    || end.checked_add(1) != Some(total)
                    || total > MAXIMUM
                    || total <= offset
                    || meta.total != Some(total)
                    || response
                        .content_length()
                        .is_some_and(|n| n != total - offset)
            })
            || validator(response.headers()) != meta.validator
        {
            output.set_len(0).await?;
            return Err(error("download_resume_range"));
        }
    } else if response.status() == StatusCode::OK {
        output.set_len(0).await?;
        output.seek(std::io::SeekFrom::Start(0)).await?;
        offset = 0;
        meta = Metadata {
            validator: validator(response.headers()),
            total: expected.or(response.content_length()),
        };
        if expected
            .zip(response.content_length())
            .is_some_and(|(a, b)| a != b)
        {
            return Err(error("download_length"));
        }
    } else {
        // A stale/unsatisfiable cached range must not poison the next retry.
        if response.status() == StatusCode::RANGE_NOT_SATISFIABLE {
            output.set_len(0).await?;
        }
        response.error_for_status()?;
        return Err(error("download_http_status"));
    }
    if meta.total.is_some_and(|n| n > MAXIMUM) {
        return Err(error("download_size_limit"));
    }
    let pending = cache.join(format!("{}.json", uuid::Uuid::now_v7()));
    let mut metadata = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(&pending)?;
    serde_json::to_writer(&mut metadata, &meta).map_err(|_| error("download_cache_metadata"))?;
    metadata.sync_all()?;
    fs::rename(pending, &meta_path)?;
    let started = Instant::now();
    let mut done = offset;
    loop {
        if cancel.load(Ordering::Acquire) {
            return Err(AppError::new("E_INTERRUPTED", "update_canceled"));
        }
        let chunk = tokio::select! { value = response.chunk() => value?, _ = tokio::time::sleep(Duration::from_millis(100)) => continue };
        let Some(chunk) = chunk else { break };
        done += chunk.len() as u64;
        if done > MAXIMUM || meta.total.is_some_and(|n| done > n) {
            output.set_len(0).await?;
            return Err(error("download_size_limit"));
        }
        output.write_all(&chunk).await?;
        progress(&Progress {
            phase: Some(TaskPhase::Downloading),
            percent: meta
                .total
                .filter(|n| *n > 0)
                .map(|n| (done as f64 / n as f64 * 100.0) as f32),
            step_index: Some(1),
            bytes: Some(done),
            total: meta.total,
            speed: Some(
                ((done - offset) as f64 / started.elapsed().as_secs_f64().max(0.001)) as u64,
            ),
            from: None,
            to: None,
        })?;
    }
    output.sync_all().await?;
    if meta.total.is_some_and(|n| n != done) {
        return Err(error("download_length"));
    }
    // A partial never becomes an install input. It first moves into the unique task stage.
    let mut target = tokio::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(destination)
        .await?;
    output.seek(std::io::SeekFrom::Start(0)).await?;
    tokio::io::copy(&mut output, &mut target).await?;
    target.sync_all().await?;
    output.set_len(0).await?; // Keep the locked inode; unlinking it would let another process bypass the lock.
    Ok(())
    }.await;
    let drained = output.flush().await;
    drop(output.into_std().await);
    result.and(drained.map_err(AppError::from))
}
