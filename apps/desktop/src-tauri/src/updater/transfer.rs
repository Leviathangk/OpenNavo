//! Private, locked download cache. Only verified complete downloads are reusable;
//! every task checks their digest and the source signature again before extraction.
use super::download::ProgressSink;
use crate::{AppError, brew::pty::Progress, model::TaskPhase};
use reqwest::{Client, StatusCode, header};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    fs,
    path::{Path, PathBuf},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};
use tokio::io::{AsyncSeekExt, AsyncWriteExt};

const MAXIMUM: u64 = 1024 * 1024 * 1024;
fn error(message: &str) -> AppError {
    AppError::new(
        match message {
            "download_length" => "E_UPDATE_INTEGRITY",
            "download_resume_range" | "download_http_status" | "download_size_limit" => {
                "E_DOWNLOAD"
            }
            _ => "E_UPDATE_PREPARE",
        },
        message,
    )
}
#[derive(Default, Serialize, Deserialize)]
struct Metadata {
    validator: Option<String>,
    total: Option<u64>,
    #[serde(default)]
    verified_sha256: Option<String>,
}

const RETAIN: Duration = Duration::from_secs(7 * 24 * 60 * 60);
const CACHE_BUDGET: u64 = 2 * MAXIMUM;

/// Keeps the inode locked through source verification, extraction and installation.
/// Dropping it releases the lock and retains verified bytes for a later retry.
pub(super) struct Cache {
    file: fs::File,
    meta_path: PathBuf,
    meta: Metadata,
}
impl Drop for Cache {
    fn drop(&mut self) {
        // flock follows the open file description. A duplicated/inherited read
        // descriptor can outlive this attempt, so close alone is insufficient.
        // fetch drains all asynchronous writes before handing the lock to Cache.
        let _ = self.file.unlock();
    }
}
impl Cache {
    pub fn verified(&mut self, digest: String) -> Result<(), AppError> {
        self.meta.total = Some(self.file.metadata()?.len());
        self.meta.verified_sha256 = Some(digest);
        self.save()
    }
    pub fn discard(&mut self) -> Result<(), AppError> {
        // Keep the locked inode: unlinking it could permit a second concurrent writer.
        self.file.set_len(0)?;
        self.meta = Metadata::default();
        self.save()
    }
    fn save(&self) -> Result<(), AppError> {
        use std::os::unix::fs::OpenOptionsExt;
        let pending = self
            .meta_path
            .with_file_name(format!("{}.json", uuid::Uuid::now_v7()));
        let mut file = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(&pending)?;
        let result = (|| {
            serde_json::to_writer(&mut file, &self.meta)?;
            file.sync_all()?;
            fs::rename(&pending, &self.meta_path)?;
            Ok(())
        })();
        if result.is_err() {
            let _ = fs::remove_file(pending);
        }
        result
    }
}
fn safe_file(file: &fs::File) -> Result<fs::Metadata, AppError> {
    use std::os::unix::fs::MetadataExt;
    let info = file.metadata()?;
    if !info.is_file()
        || info.uid() != unsafe { nix::libc::getuid() }
        || info.mode() & 0o077 != 0
        || info.nlink() != 1
    {
        return Err(error("download_cache_file"));
    }
    Ok(info)
}
fn digest(file: &fs::File) -> Result<String, AppError> {
    use std::os::unix::fs::FileExt;
    let mut hash = Sha256::new();
    let mut offset = 0;
    let mut buffer = [0; 65536];
    loop {
        let n = file.read_at(&mut buffer, offset)?;
        if n == 0 {
            break;
        }
        offset += n as u64;
        if offset > MAXIMUM {
            return Err(error("download_size_limit"));
        }
        hash.update(&buffer[..n]);
    }
    Ok(format!("{:x}", hash.finalize()))
}
/// Never remove locked files or follow links. Reserve one maximum-size download
/// within the budget; failed attempts expire after a week.
fn prune(root: &Path, keep: &Path) {
    use std::os::unix::fs::OpenOptionsExt;
    let Ok(entries) = fs::read_dir(root) else {
        return;
    };
    let mut files = vec![];
    for entry in entries.flatten() {
        let path = entry.path();
        if path == keep
            || entry
                .file_name()
                .to_str()
                .is_none_or(|s| s.len() != 64 || !s.bytes().all(|b| b.is_ascii_hexdigit()))
            || crate::appstore::install::validate_staging(&path, unsafe { nix::libc::getuid() })
                .is_err()
        {
            continue;
        }
        let Ok(file) = fs::OpenOptions::new()
            .read(true)
            .write(true)
            .custom_flags(nix::libc::O_NOFOLLOW | nix::libc::O_NONBLOCK)
            .open(path.join("partial"))
        else {
            continue;
        };
        let Ok(info) = safe_file(&file) else {
            continue;
        };
        if info.len() == 0 {
            continue;
        }
        let Ok(modified) = info.modified() else {
            continue;
        };
        files.push((modified, info.len(), file));
    }
    files.sort_by_key(|(modified, _, _)| *modified);
    let mut total = files.iter().map(|(_, len, _)| *len).sum::<u64>();
    for (modified, len, file) in files {
        if (modified.elapsed().is_ok_and(|age| age > RETAIN) || total > CACHE_BUDGET - MAXIMUM)
            && file.try_lock().is_ok()
        {
            if file.set_len(0).is_ok() {
                total = total.saturating_sub(len);
            }
            let _ = file.unlock();
        }
    }
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
pub(super) async fn fetch(
    client: &Client,
    url: &str,
    identity: &str,
    expected: Option<u64>,
    root: &Path,
    destination: &Path,
    cancel: Arc<AtomicBool>,
    progress: ProgressSink,
) -> Result<Cache, AppError> {
    use std::os::unix::fs::OpenOptionsExt;
    if expected.is_some_and(|n| n > MAXIMUM) {
        return Err(error("download_size_limit"));
    }
    let root = root.join("downloads");
    directory(&root)?;
    let cache = root.join(format!("{:x}", Sha256::digest(identity.as_bytes())));
    directory(&cache)?;
    prune(&root, &cache);
    let path = cache.join("partial");
    let file = fs::OpenOptions::new()
        .read(true)
        .write(true)
        .create(true)
        .truncate(false)
        .mode(0o600)
        .custom_flags(nix::libc::O_NOFOLLOW | nix::libc::O_NONBLOCK)
        .open(&path)?;
    // The lock's lifetime includes the final copy, including across task/process restarts.
    loop {
        match file.try_lock() {
            Ok(()) => break,
            Err(fs::TryLockError::WouldBlock) => {
                return Err(AppError::new("E_BUSY", "download_in_progress"));
            }
            Err(fs::TryLockError::Error(e)) if e.kind() == std::io::ErrorKind::Interrupted => {}
            Err(fs::TryLockError::Error(e)) => {
                return Err(AppError::from(e).context("E_UPDATE_PREPARE", "download_cache_lock"));
            }
        }
    }
    let info = safe_file(&file)?;
    let meta_path = cache.join("metadata.json");
    let mut meta: Metadata = fs::symlink_metadata(&meta_path)
        .ok()
        .filter(|m| m.is_file() && !m.is_symlink() && m.len() <= 4096)
        .and_then(|_| fs::read(&meta_path).ok())
        .and_then(|v| serde_json::from_slice(&v).ok())
        .unwrap_or_default();
    if cancel.load(Ordering::Acquire) {
        return Err(AppError::new("E_INTERRUPTED", "update_canceled"));
    }
    let verified = info.len() > 0
        && info.len() <= MAXIMUM
        && meta.total == Some(info.len())
        && expected.is_none_or(|n| n == info.len())
        && info.modified()?.elapsed().is_ok_and(|age| age <= RETAIN)
        && if let Some(expected_digest) = &meta.verified_sha256 {
            let file = file.try_clone()?;
            crate::blocking(move || digest(&file)).await? == *expected_digest
        } else {
            false
        };
    if verified {
        let mut output = tokio::fs::File::from_std(file.try_clone()?);
        let copied = copy(&mut output, destination).await;
        let drained = output.flush().await;
        drop(output.into_std().await);
        let cache = Cache {
            file,
            meta_path,
            meta,
        };
        copied?;
        drained?;
        progress(&Progress {
            phase: Some(TaskPhase::Downloading),
            percent: Some(100.0),
            step_index: Some(1),
            bytes: Some(info.len()),
            total: Some(info.len()),
            speed: None,
            from: None,
            to: None,
        })?;
        return Ok(cache);
    }
    let mut offset = info.len();
    if meta.verified_sha256.is_some()
        || offset > MAXIMUM
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
            verified_sha256: None,
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
    // Only the private task copy is ever extracted or installed. Verification
    // will mark the locked cache reusable; an interrupted verification cannot.
    copy(&mut output, destination).await?;
    Ok(())
    }.await;
    let drained = output.flush().await;
    drop(output.into_std().await);
    let cache = Cache {
        file,
        meta_path,
        meta,
    };
    result?;
    drained?;
    Ok(cache)
}
async fn copy(output: &mut tokio::fs::File, destination: &Path) -> Result<(), AppError> {
    let mut target = tokio::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(destination)
        .await?;
    output.seek(std::io::SeekFrom::Start(0)).await?;
    tokio::io::copy(output, &mut target).await?;
    target.sync_all().await?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt, symlink};

    fn entry(root: &Path, id: u8, size: u64, age: Duration) -> (PathBuf, fs::File) {
        let path = root.join(format!("{id:064x}"));
        directory(&path).unwrap();
        let file = fs::OpenOptions::new()
            .read(true)
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(path.join("partial"))
            .unwrap();
        // Sparse test files exercise the budget without allocating gigabytes.
        file.set_len(size).unwrap();
        file.set_times(fs::FileTimes::new().set_modified(std::time::SystemTime::now() - age))
            .unwrap();
        (path, file)
    }

    #[test]
    fn pruning_expires_old_entries_reserves_space_and_keeps_active_downloads() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().canonicalize().unwrap();
        let (keep, current) = entry(&root, 0, MAXIMUM, Duration::ZERO);
        let (_, expired) = entry(&root, 1, 1024, RETAIN + Duration::from_secs(1));
        let (_, oldest) = entry(&root, 2, MAXIMUM / 2, Duration::from_secs(300));
        let (_, newest) = entry(&root, 3, MAXIMUM / 2, Duration::from_secs(100));
        let (_, active) = entry(&root, 4, MAXIMUM / 2, RETAIN + Duration::from_secs(1));
        active.try_lock().unwrap();
        let inode = oldest.metadata().unwrap().ino();
        prune(&root, &keep);
        assert_eq!(current.metadata().unwrap().len(), MAXIMUM);
        assert_eq!(expired.metadata().unwrap().len(), 0);
        assert_eq!(oldest.metadata().unwrap().len(), 0);
        assert_eq!(oldest.metadata().unwrap().ino(), inode);
        assert_eq!(newest.metadata().unwrap().len(), MAXIMUM / 2);
        assert_eq!(active.metadata().unwrap().len(), MAXIMUM / 2);
        drop(active);
        prune(&root, &keep);
        assert_eq!(
            fs::metadata(root.join(format!("{:064x}/partial", 4)))
                .unwrap()
                .len(),
            0
        );
    }

    #[test]
    fn pruning_never_follows_links_or_truncates_files_outside_its_private_cache() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().canonicalize().unwrap();
        let (target, external) = entry(&root, 1, 123, RETAIN + Duration::from_secs(1));
        let link_directory = root.join(format!("{:064x}", 2));
        directory(&link_directory).unwrap();
        symlink(target.join("partial"), link_directory.join("partial")).unwrap();
        symlink(&target, root.join(format!("{:064x}", 3))).unwrap();
        let (public, public_file) = entry(&root, 4, 123, RETAIN + Duration::from_secs(1));
        fs::set_permissions(public.join("partial"), fs::Permissions::from_mode(0o644)).unwrap();
        let (hardlink, linked_file) = entry(&root, 5, 123, RETAIN + Duration::from_secs(1));
        fs::hard_link(hardlink.join("partial"), root.join("unrelated")).unwrap();
        prune(&root, &target);
        for file in [external, public_file, linked_file] {
            assert_eq!(file.metadata().unwrap().len(), 123);
        }
    }

    #[test]
    fn completed_attempt_releases_lock_even_if_a_read_descriptor_outlives_it() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().canonicalize().unwrap();
        let (path, file) = entry(&root, 0, 123, Duration::ZERO);
        file.try_lock().unwrap();
        // Models a completed reader or a child between fork and exec. Both
        // share the original flock, but neither owns the download attempt.
        let reader = file.try_clone().unwrap();
        let cache = Cache {
            file,
            meta_path: path.join("metadata.json"),
            meta: Metadata::default(),
        };
        let retry = fs::File::options()
            .read(true)
            .write(true)
            .open(path.join("partial"))
            .unwrap();
        assert!(matches!(
            retry.try_lock(),
            Err(fs::TryLockError::WouldBlock)
        ));
        drop(cache);
        retry.try_lock().unwrap();
        assert_eq!(reader.metadata().unwrap().len(), 123);
    }
}
