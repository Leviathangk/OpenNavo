//! Bounded, local file reads. Reject cloud placeholders before opening or descending.
use crate::{AppError, uninstall::rules::Snapshot};
use sha2::{Digest, Sha256};
#[cfg(unix)]
use std::os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt};
use std::{
    fs::{self, File, Metadata, OpenOptions},
    io::Read,
    path::{Component, Path},
    sync::atomic::{AtomicBool, Ordering},
};

pub const MAX_REGULAR_FILE: u64 = 100 * 1024 * 1024;
pub const MAX_INSTALLER_FILE: u64 = 1024 * 1024 * 1024;
pub const MAX_SCAN_SIZE: u64 = 2 * 1024 * 1024 * 1024;
pub const SF_DATALESS: u32 = 0x4000_0000;

/// Internal update copies are not installed Apps or discovered startup plugins.
/// Lexical only: this also works for fixture roots and the Data volume alias.
pub(super) fn app_update_backup(path: &Path) -> bool {
    crate::updater::backup::contains(path)
}

/// Determine the scan budget without following links or hydrating placeholders.
/// Extensions cover Homebrew's named downloads; bounded header/trailer reads
/// also recognize its extensionless cache objects.
pub fn scan_limit(path: &Path) -> Result<u64, AppError> {
    scan_limit_guarded(path, None)
}

pub(super) fn scan_limit_guarded(
    path: &Path,
    guard: Option<&super::own_data::OwnData>,
) -> Result<u64, AppError> {
    let meta = match guard {
        Some(guard) => guard.checked_metadata(path)?,
        None => safe_metadata(path)?,
    };
    if installer_extension(path) {
        return Ok(MAX_INSTALLER_FILE);
    }
    if !meta.is_file() || meta.len() > MAX_INSTALLER_FILE {
        return Ok(MAX_REGULAR_FILE);
    }
    // An unreadable small file is still handled as unreadable by the caller;
    // do not infer that it is ordinary from a failed header read.
    let file = open_read_guarded(path, MAX_INSTALLER_FILE, guard)?;
    scan_limit_from_file(path, &file)
}

/// Classify the already verified descriptor without opening its path again.
pub(super) fn scan_limit_from_file(path: &Path, file: &File) -> Result<u64, AppError> {
    if installer_extension(path) {
        return Ok(MAX_INSTALLER_FILE);
    }
    let size = file.metadata()?.len();
    use std::os::unix::fs::FileExt;
    let mut head = [0; 512];
    let n = file.read_at(&mut head, 0)?;
    let head = &head[..n];
    let archive = [
        b"PK\x03\x04".as_slice(),
        b"PK\x05\x06",
        b"PK\x07\x08",
        b"xar!",
        b"\x1f\x8b",
        b"BZh",
        b"\xfd7zXZ\0",
        b"\x28\xb5\x2f\xfd",
    ]
    .iter()
    .any(|magic| head.starts_with(magic))
        || head.get(257..262) == Some(b"ustar");
    let mut trailer = [0; 4];
    let dmg = size >= 512 && file.read_at(&mut trailer, size - 512)? == 4 && &trailer == b"koly";
    Ok(if archive || dmg {
        MAX_INSTALLER_FILE
    } else {
        MAX_REGULAR_FILE
    })
}

pub(super) fn installer_extension(path: &Path) -> bool {
    let name = path
        .file_name()
        .unwrap_or_default()
        .to_string_lossy()
        .to_ascii_lowercase();
    [
        ".dmg", ".pkg", ".xar", ".zip", ".tar", ".tgz", ".tbz", ".tbz2", ".txz", ".tzst",
        ".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst", ".bottle",
    ]
    .iter()
    .any(|ext| name.ends_with(ext))
}

pub fn dataless(meta: &Metadata) -> bool {
    #[cfg(target_os = "macos")]
    {
        use std::os::macos::fs::MetadataExt;
        meta.st_flags() & SF_DATALESS != 0
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = meta;
        false
    }
}

pub fn safe_metadata(path: &Path) -> Result<Metadata, AppError> {
    safe_metadata_with(path, &|_| Ok(()))
}

pub(super) fn safe_metadata_with(
    path: &Path,
    check: &dyn Fn(&Metadata) -> Result<(), AppError>,
) -> Result<Metadata, AppError> {
    if !path.is_absolute() || path.components().any(|p| matches!(p, Component::ParentDir)) {
        return Err(AppError::new("E_INVALID_ARG", "security_absolute_path"));
    }
    let mut last = None;
    for part in path.ancestors().collect::<Vec<_>>().into_iter().rev() {
        let meta = fs::symlink_metadata(part)?;
        if meta.is_symlink() {
            return Err(AppError::new("E_PROTECTED", "security_symlink"));
        }
        if dataless(&meta) {
            return Err(AppError::new("E_PROTECTED", "security_dataless"));
        }
        check(&meta)?;
        last = Some(meta);
    }
    last.ok_or_else(|| AppError::new("E_INVALID_ARG", "security_absolute_path"))
}

pub fn open_read(path: &Path, limit: u64) -> Result<File, AppError> {
    open_read_guarded(path, limit, None)
}

pub(super) fn open_read_guarded(
    path: &Path,
    limit: u64,
    guard: Option<&super::own_data::OwnData>,
) -> Result<File, AppError> {
    let before = match guard {
        Some(guard) => guard.checked_metadata(path)?,
        None => safe_metadata(path)?,
    };
    if !before.is_file() || before.len() > limit {
        return Err(AppError::new("E_PROTECTED", "security_file_size_or_type"));
    }
    let file = open_no_follow(path, guard)?;
    let after = file.metadata()?;
    if !after.is_file()
        || dataless(&after)
        || after.len() > limit
        || before.dev() != after.dev()
        || before.ino() != after.ino()
    {
        return Err(AppError::new("E_PROTECTED", "security_file_changed"));
    }
    Ok(file)
}

/// Resolve each component relative to an already opened directory. O_NOFOLLOW
/// on only the final path is insufficient when an ancestor is swapped for a link.
fn open_no_follow(path: &Path, guard: Option<&super::own_data::OwnData>) -> Result<File, AppError> {
    use std::{
        ffi::CString,
        os::{
            fd::{AsRawFd, FromRawFd},
            unix::ffi::OsStrExt,
        },
    };
    let mut directory = File::open("/")?;
    let parts = path
        .components()
        .filter_map(|c| {
            if let Component::Normal(v) = c {
                Some(v)
            } else {
                None
            }
        })
        .collect::<Vec<_>>();
    for (index, part) in parts.iter().enumerate() {
        let name = CString::new(part.as_bytes())
            .map_err(|_| AppError::new("E_INVALID_ARG", "security_path"))?;
        let mut stat: nix::libc::stat = unsafe { std::mem::zeroed() };
        if unsafe {
            nix::libc::fstatat(
                directory.as_raw_fd(),
                name.as_ptr(),
                &mut stat,
                nix::libc::AT_SYMLINK_NOFOLLOW,
            )
        } != 0
        {
            return Err(std::io::Error::last_os_error().into());
        }
        #[cfg(target_os = "macos")]
        if stat.st_flags & SF_DATALESS != 0 {
            return Err(AppError::new("E_PROTECTED", "security_dataless"));
        }
        let last = index + 1 == parts.len();
        let expected = if last {
            nix::libc::S_IFREG
        } else {
            nix::libc::S_IFDIR
        };
        if stat.st_mode & nix::libc::S_IFMT != expected {
            return Err(AppError::new("E_PROTECTED", "security_symlink_or_type"));
        }
        if let Some(guard) = guard {
            guard.check_inode(stat.st_dev as u64, stat.st_ino, !last, stat.st_nlink as u64)?;
        }
        let fd = unsafe {
            nix::libc::openat(
                directory.as_raw_fd(),
                name.as_ptr(),
                nix::libc::O_RDONLY
                    | nix::libc::O_NOFOLLOW
                    | nix::libc::O_NONBLOCK
                    | nix::libc::O_CLOEXEC
                    | if last { 0 } else { nix::libc::O_DIRECTORY },
            )
        };
        if fd < 0 {
            return Err(std::io::Error::last_os_error().into());
        }
        let opened = unsafe { File::from_raw_fd(fd) };
        let meta = opened.metadata()?;
        if dataless(&meta) || meta.dev() != stat.st_dev as u64 || meta.ino() != stat.st_ino {
            return Err(AppError::new("E_PROTECTED", "security_file_changed"));
        }
        directory = opened;
    }
    Ok(directory)
}

pub fn read(path: &Path, limit: u64) -> Result<Vec<u8>, AppError> {
    read_guarded(path, limit, None)
}

pub(super) fn read_guarded(
    path: &Path,
    limit: u64,
    guard: Option<&super::own_data::OwnData>,
) -> Result<Vec<u8>, AppError> {
    let file = open_read_guarded(path, limit, guard)?;
    let mut bytes = Vec::new();
    file.take(limit + 1).read_to_end(&mut bytes)?;
    if bytes.len() as u64 > limit {
        return Err(AppError::new("E_PROTECTED", "security_file_grew"));
    }
    Ok(bytes)
}

pub fn hash(file: &mut File, cancel: &AtomicBool) -> Result<String, AppError> {
    // Callers have already applied their file-class policy before opening.
    hash_up_to(file, cancel, MAX_INSTALLER_FILE)
}

pub fn hash_up_to(file: &mut File, cancel: &AtomicBool, limit: u64) -> Result<String, AppError> {
    let mut digest = Sha256::new();
    let mut bytes = 0_u64;
    let mut buffer = [0; 65536];
    loop {
        canceled(cancel)?;
        let n = file.read(&mut buffer)?;
        if n == 0 {
            break;
        }
        bytes += n as u64;
        if bytes > limit {
            return Err(AppError::new("E_PROTECTED", "security_file_grew"));
        }
        digest.update(&buffer[..n]);
    }
    Ok(format!("{:x}", digest.finalize()))
}

pub fn canceled(cancel: &AtomicBool) -> Result<(), AppError> {
    if cancel.load(Ordering::Acquire) {
        Err(AppError::new("E_SCAN_CANCELED", "scan_canceled"))
    } else {
        Ok(())
    }
}

pub fn same_file(expected: &Snapshot, path: &Path) -> Result<(), AppError> {
    safe_metadata(path)?;
    let now = Snapshot::capture(path)?;
    // same_object intentionally tolerates atomic preference replacement for uninstall.
    // Security actions also require the exact scanned inode/content metadata.
    if !expected.same_object(&now)
        || expected.device != now.device
        || expected.inode != now.inode
        || expected.length != now.length
        || expected.modified != now.modified
    {
        return Err(AppError::new("E_PROTECTED", "security_object_changed"));
    }
    Ok(())
}

pub fn private_dir(path: &Path) -> Result<(), AppError> {
    if let Err(e) = fs::symlink_metadata(path) {
        if e.kind() != std::io::ErrorKind::NotFound {
            return Err(e.into());
        }
        let parent = path
            .parent()
            .ok_or_else(|| AppError::new("E_PROTECTED", "security_private_parent"))?;
        if fs::symlink_metadata(parent).is_err() {
            private_dir(parent)?;
        } else {
            safe_metadata(parent)?;
        }
        fs::create_dir(path)?;
    }
    let m = safe_metadata(path)?;
    if !m.is_dir() || m.uid() != unsafe { nix::libc::geteuid() } {
        return Err(AppError::new("E_PROTECTED", "security_private_directory"));
    }
    fs::set_permissions(path, fs::Permissions::from_mode(0o700))?;
    Ok(())
}

pub fn config_path(path: &Path) -> Result<String, AppError> {
    let value = path
        .to_str()
        .ok_or_else(|| AppError::new("E_INVALID_ARG", "security_path_encoding"))?;
    if value.chars().any(char::is_control) || value.contains('"') || !path.is_absolute() {
        return Err(AppError::new("E_INVALID_ARG", "security_config_path"));
    }
    Ok(format!("\"{value}\""))
}

pub fn write_private(path: &Path, bytes: &[u8]) -> Result<(), AppError> {
    use std::io::Write;
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .custom_flags(nix::libc::O_NOFOLLOW)
        .open(path)?;
    file.write_all(bytes)?;
    file.sync_all()?;
    Ok(())
}

#[cfg(test)]
mod tests {
    #[test]
    fn descriptor_classification_preserves_offset_and_original_identity() {
        use std::io::{Read, Seek, SeekFrom};
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let path = root.join("extensionless");
        fs::write(&path, b"PK\x03\x04payload").unwrap();
        let mut file = open_read(&path, MAX_INSTALLER_FILE).unwrap();
        file.seek(SeekFrom::Start(4)).unwrap();
        let replacement = root.join("replacement");
        fs::write(&replacement, b"ordinary").unwrap();
        fs::rename(replacement, &path).unwrap();
        assert_eq!(
            scan_limit_from_file(&path, &file).unwrap(),
            MAX_INSTALLER_FILE
        );
        assert_eq!(file.stream_position().unwrap(), 4);
        let mut tail = String::new();
        file.read_to_string(&mut tail).unwrap();
        assert_eq!(tail, "payload");
        assert_eq!(scan_limit(&path).unwrap(), MAX_REGULAR_FILE);
    }
    use super::*;
    use std::os::unix::fs::FileExt;
    #[test]
    fn archive_limits_use_extensions_or_bounded_magic_without_following_links() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        for suffix in [
            "dmg",
            "pkg",
            "xar",
            "zip",
            "tar",
            "tar.gz",
            "tar.bz2",
            "tar.xz",
            "tar.zst",
            "bottle.tar.gz",
        ] {
            let path = root.join(format!("fixture.{suffix}"));
            File::create(&path)
                .unwrap()
                .set_len(MAX_REGULAR_FILE + 1)
                .unwrap();
            assert_eq!(scan_limit(&path).unwrap(), MAX_INSTALLER_FILE);
        }
        for magic in [
            b"PK\x03\x04".as_slice(),
            b"xar!",
            b"\x1f\x8b",
            b"BZh",
            b"\xfd7zXZ\0",
            b"\x28\xb5\x2f\xfd",
        ] {
            let path = root.join("extensionless");
            fs::write(&path, magic).unwrap();
            assert_eq!(scan_limit(&path).unwrap(), MAX_INSTALLER_FILE);
        }
        let path = root.join("disk-image");
        let file = File::create(&path).unwrap();
        file.set_len(1024).unwrap();
        file.write_at(b"koly", 512).unwrap();
        assert_eq!(scan_limit(&path).unwrap(), MAX_INSTALLER_FILE);
        let ordinary = root.join("video.mp4");
        File::create(&ordinary)
            .unwrap()
            .set_len(MAX_REGULAR_FILE + 1)
            .unwrap();
        assert_eq!(scan_limit(&ordinary).unwrap(), MAX_REGULAR_FILE);
        let link = root.join("link.zip");
        std::os::unix::fs::symlink(&ordinary, &link).unwrap();
        assert!(scan_limit(&link).is_err());
    }
}
