use crate::{
    AppError,
    model::SkippedLocationReason,
    uninstall::rules::{Snapshot, parents_safe},
};
use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    path::{Path, PathBuf},
    sync::atomic::{AtomicBool, Ordering},
    time::Instant,
};

#[derive(Debug, Clone)]
pub struct Tree {
    pub snapshots: BTreeMap<PathBuf, Snapshot>,
    pub bytes: u64,
    pub modified_at: i64,
    pub incomplete: Option<SkippedLocationReason>,
    pub compact: bool,
    pub kept_open_files: u64,
}
pub fn check(flag: &AtomicBool, deadline: Instant) -> Result<(), AppError> {
    if flag.load(Ordering::Acquire) {
        return Err(AppError::new("E_CANCELED", "cleanup_canceled"));
    }
    if Instant::now() >= deadline {
        return Err(AppError::new("E_TIMEOUT", "cleanup_category_timeout"));
    }
    Ok(())
}
pub fn capture(path: &Path, flag: &AtomicBool, deadline: Instant) -> Result<Tree, AppError> {
    let mut tree = Tree {
        snapshots: BTreeMap::new(),
        bytes: 0,
        modified_at: 0,
        incomplete: None,
        compact: false,
        kept_open_files: 0,
    };
    let device = Snapshot::capture(path)?.device;
    let mut pending = vec![path.to_owned()];
    let mut seen = BTreeSet::new();
    while let Some(path) = pending.pop() {
        check(flag, deadline)?;
        if tree.snapshots.len() >= 200_000 {
            return Err(AppError::new("E_TIMEOUT", "cleanup_tree_limit"));
        }
        let snapshot = Snapshot::capture(&path)?;
        let metadata = fs::symlink_metadata(&path)?;
        if snapshot.device != device {
            return Err(AppError::new("E_PROTECTED", "cleanup_mount_boundary"));
        }
        if snapshot.kind == 2 {
            return Err(AppError::new("E_PROTECTED", "cleanup_symlink"));
        }
        #[cfg(unix)]
        {
            use std::os::unix::fs::MetadataExt;
            if metadata.uid() != unsafe { nix::libc::geteuid() } {
                return Err(AppError::new("E_PROTECTED", "cleanup_foreign_owner"));
            }
            if !metadata.is_dir()
                && metadata.nlink() == 1
                && seen.insert((metadata.dev(), metadata.ino()))
            {
                tree.bytes = tree
                    .bytes
                    .saturating_add(metadata.blocks().saturating_mul(512));
            }
        }
        #[cfg(not(unix))]
        {
            let _ = &mut seen;
            if metadata.is_file() {
                tree.bytes = tree.bytes.saturating_add(metadata.len());
            }
        }
        tree.modified_at = tree
            .modified_at
            .max((snapshot.modified / 1_000_000).min(i64::MAX as u128) as i64);
        if metadata.is_dir() {
            for entry in fs::read_dir(&path)? {
                check(flag, deadline)?;
                pending.push(entry?.path());
            }
        }
        tree.snapshots.insert(path, snapshot);
    }
    // Capture directory snapshots after traversal, then reject concurrent changes.
    if !tree.snapshots.iter().all(|(p, s)| s.matches(p)) {
        return Err(AppError::new("E_PLAN_EXPIRED", "cleanup_tree_changed"));
    }
    Ok(tree)
}

/// Read-only estimate for a busy cache. Concurrent removals are harmless; no executable plan
/// or stable snapshot is produced. Return measured bytes even if the traversal is incomplete.
pub fn measure_filtered(
    path: &Path,
    flag: &AtomicBool,
    deadline: Instant,
    excluded: impl Fn(&Path) -> bool,
) -> Result<(u64, Option<SkippedLocationReason>), AppError> {
    check_cancel(flag)?;
    if Instant::now() >= deadline {
        return Ok((0, Some(SkippedLocationReason::Timeout)));
    }
    if excluded(path) || !parents_safe(path) {
        return Ok((0, None));
    }
    let dir = match super::walk::Directory::open(path) {
        Ok(dir) => dir,
        Err(_) => return Ok((0, Some(SkippedLocationReason::Permission))),
    };
    let device = dir.identity()?.device;
    let mut bytes = 0u64;
    let mut incomplete = None;
    let mut pending = vec![PathBuf::new()];
    let mut hardlinks = BTreeSet::new();
    let mut count = 0;
    while let Some(relative) = pending.pop() {
        check_cancel(flag)?;
        if Instant::now() >= deadline {
            return Ok((bytes, Some(SkippedLocationReason::Timeout)));
        }
        let directory = match dir.relative(&relative) {
            Ok(dir) => dir,
            Err(_) => {
                incomplete = Some(SkippedLocationReason::Permission);
                continue;
            }
        };
        if directory.identity()?.device != device {
            continue;
        }
        let entries = match directory.entries() {
            Ok(entries) => entries,
            Err(_) => {
                incomplete = Some(SkippedLocationReason::Permission);
                continue;
            }
        };
        for entry in entries {
            check_cancel(flag)?;
            count += 1;
            if Instant::now() >= deadline || count > 2_000_000 {
                return Ok((bytes, Some(SkippedLocationReason::Timeout)));
            }
            let relative = relative.join(&entry.name);
            if entry.kind == 2 || entry.device != device || excluded(&path.join(&relative)) {
                continue;
            }
            if entry.kind == 1 {
                pending.push(relative);
            } else if entry.links == 1 || hardlinks.insert((entry.device, entry.inode)) {
                bytes = bytes.saturating_add(entry.bytes);
            }
        }
    }
    Ok((bytes, incomplete))
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use std::{
        os::unix::fs::{MetadataExt, symlink},
        time::Duration,
    };

    #[test]
    fn busy_measure_is_bounded_cancellable_and_counts_hardlinks_once() {
        let temporary = tempfile::tempdir().unwrap();
        let root = temporary.path().canonicalize().unwrap();
        let cache = root.join("cache");
        fs::create_dir(&cache).unwrap();
        let data = cache.join("data");
        fs::write(&data, [0x55; 65536]).unwrap();
        fs::hard_link(&data, cache.join("duplicate")).unwrap();
        let outside = root.join("outside");
        fs::write(&outside, [0x66; 65536]).unwrap();
        symlink(&outside, cache.join("symlink")).unwrap();
        let flag = AtomicBool::new(false);
        let result = measure_filtered(
            &cache,
            &flag,
            Instant::now() + Duration::from_secs(5),
            |_| false,
        )
        .unwrap();
        assert_eq!(result, (fs::metadata(data).unwrap().blocks() * 512, None));
        let result = measure_filtered(&cache, &flag, Instant::now(), |_| false).unwrap();
        assert_eq!(result, (0, Some(SkippedLocationReason::Timeout)));
        flag.store(true, Ordering::Release);
        assert_eq!(
            measure_filtered(&cache, &flag, Instant::now(), |_| false)
                .unwrap_err()
                .code,
            "E_CANCELED"
        );
        assert!(outside.exists());
    }
}

/// Fast allocated-size estimate. Small trees retain strict snapshots; large/incomplete
/// rebuildable caches retain only the root identity and are traversed again at execution.
pub fn estimate(
    path: &Path,
    flag: &AtomicBool,
    deadline: Instant,
    max_nodes: usize,
    protected: impl Fn(&Path) -> bool,
    open: &[PathBuf],
) -> Result<Tree, AppError> {
    let root = Snapshot::capture(path)?;
    if root.kind == 2 {
        return Err(AppError::new("E_PROTECTED", "cleanup_symlink"));
    }
    let mut result = Tree {
        snapshots: BTreeMap::from([(path.to_owned(), root.clone())]),
        bytes: 0,
        modified_at: (root.modified / 1_000_000).min(i64::MAX as u128) as i64,
        incomplete: None,
        compact: false,
        kept_open_files: open
            .iter()
            .filter(|p| p.starts_with(path))
            .collect::<BTreeSet<_>>()
            .len() as u64,
    };
    let metadata = fs::symlink_metadata(path)?;
    {
        use std::os::unix::fs::MetadataExt;
        if metadata.uid() != unsafe { nix::libc::geteuid() } {
            return Err(AppError::new("E_PROTECTED", "cleanup_foreign_owner"));
        }
    }
    if metadata.is_file() {
        if !open.iter().any(|p| p == path) {
            use std::os::unix::fs::MetadataExt;
            result.bytes = if metadata.nlink() == 1 {
                metadata.blocks().saturating_mul(512)
            } else {
                0
            };
        }
        if check(flag, deadline).is_err() {
            check_cancel(flag)?;
            result.incomplete = Some(SkippedLocationReason::Timeout);
            result.compact = true;
        }
        return Ok(result);
    }
    let dir = super::walk::Directory::open(path)?;
    let identity = dir.identity()?;
    if identity.device != root.device || identity.inode != root.inode {
        return Err(AppError::new("E_PLAN_EXPIRED", "cleanup_root_changed"));
    }
    let mut pending = vec![PathBuf::new()];
    let mut count = 0usize;
    // Allocated bytes are only counted for single-link regular files, so no per-file inode set is needed.
    'scan: while let Some(relative) = pending.pop() {
        if check(flag, deadline).is_err() {
            check_cancel(flag)?;
            result.incomplete = Some(SkippedLocationReason::Timeout);
            break;
        }
        let directory = match dir.relative(&relative) {
            Ok(d) => d,
            Err(_) => {
                result.incomplete = Some(SkippedLocationReason::Permission);
                continue;
            }
        };
        let directory_identity = directory.identity()?;
        if directory_identity.device != root.device
            || directory_identity.uid != unsafe { nix::libc::geteuid() }
        {
            result.incomplete = Some(SkippedLocationReason::Permission);
            continue;
        }
        let entries = match directory.entries() {
            Ok(e) => e,
            Err(_) => {
                result.incomplete = Some(SkippedLocationReason::Permission);
                continue;
            }
        };
        for entry in entries {
            if check(flag, deadline).is_err() || count >= max_nodes {
                check_cancel(flag)?;
                result.incomplete = Some(SkippedLocationReason::Timeout);
                break 'scan;
            }
            count += 1;
            let relative = relative.join(&entry.name);
            let child = path.join(&relative);
            if protected(&child) {
                return Err(AppError::new("E_PROTECTED", "cleanup_protected_descendant"));
            }
            if entry.kind == 2 {
                result.compact = true;
                continue;
            }
            if entry.device != root.device || entry.uid != unsafe { nix::libc::geteuid() } {
                result.incomplete = Some(SkippedLocationReason::Permission);
                result.compact = true;
                continue;
            }
            result.modified_at = result.modified_at.max(entry.modified);
            if open.iter().any(|p| child.starts_with(p)) {
                continue;
            }
            if entry.kind == 1 {
                pending.push(relative);
            } else if entry.links == 1 {
                result.bytes = result.bytes.saturating_add(entry.bytes);
            }
            if count <= 256 && !result.compact {
                match Snapshot::capture(&child) {
                    Ok(s) => {
                        result.snapshots.insert(child, s);
                    }
                    Err(_) => {
                        result.compact = true;
                    }
                }
            } else if !result.compact {
                result.compact = true;
                result.snapshots.retain(|p, _| p == path);
            }
        }
    }
    if result.incomplete.is_some() {
        result.compact = true;
    }
    if result.compact {
        result.snapshots.retain(|p, _| p == path);
    }
    Ok(result)
}
fn check_cancel(flag: &AtomicBool) -> Result<(), AppError> {
    if flag.load(Ordering::Acquire) {
        Err(AppError::new("E_CANCELED", "cleanup_canceled"))
    } else {
        Ok(())
    }
}

/// Identity check deliberately ignores size/mtime for a rebuildable directory whose
/// contents may grow after scanning, but never ignores parent identities, device or inode.
pub fn same_root(before: &Snapshot, after: &Snapshot) -> bool {
    before.kind == after.kind
        && before.device == after.device
        && before.inode == after.inode
        && before.parents == after.parents
        && before.link == after.link
}

/// Only deletes descriptor-relative, freshly checked regular files / directories.
/// Ancestors of open files remain; new links, mounts, foreign ownership and protected
/// descendants are retained. No cached list of half a million files grants deletion rights.
pub fn remove_cache(
    path: &Path,
    before: &Snapshot,
    flag: &AtomicBool,
    protected: impl Fn(&Path) -> bool,
    open: &[PathBuf],
) -> Result<u64, AppError> {
    let current = Snapshot::capture(path)?;
    if !same_root(before, &current) {
        return Err(AppError::new("E_PLAN_EXPIRED", "cleanup_root_changed"));
    }
    let parent = super::walk::Directory::verified_parent(path, before)?;
    let name = Path::new(path.file_name().ok_or_else(super::cached_error)?);
    let dir = parent.child(name)?;
    let identity = dir.identity()?;
    if identity.device != before.device
        || identity.inode != before.inode
        || identity.uid != unsafe { nix::libc::geteuid() }
    {
        return Err(AppError::new("E_PLAN_EXPIRED", "cleanup_root_changed"));
    }
    fn remove(
        dir: &super::walk::Directory,
        path: &Path,
        device: u64,
        flag: &AtomicBool,
        protected: &impl Fn(&Path) -> bool,
        open: &[PathBuf],
        depth: usize,
    ) -> Result<(u64, bool), AppError> {
        if depth > 256 {
            return Err(AppError::new("E_PROTECTED", "cleanup_tree_depth"));
        }
        let mut bytes = 0u64;
        let mut retained = false;
        // Go's module cache is read-only by design (0555 directories). The caller checked that we own this
        // directory and it is being removed anyway: make it writable while emptying it, and put the mode back if
        // anything has to stay.
        let mode = dir.mode()?;
        let locked = mode & 0o200 == 0;
        if locked {
            dir.set_mode(mode | 0o200)?;
        }
        for node in dir.entries()? {
            if flag.load(Ordering::Acquire) {
                retained = true;
                break;
            }
            let child = path.join(&node.name);
            let name = Path::new(&node.name);
            if protected(&child)
                || open.iter().any(|p| child.starts_with(p))
                || node.kind == 2
                || node.device != device
                || node.uid != unsafe { nix::libc::geteuid() }
                || (node.kind == 0 && node.links != 1)
            {
                retained = true;
                continue;
            }
            let now = dir.stat(name)?;
            if now.device != node.device
                || now.inode != node.inode
                || now.kind != node.kind
                || now.uid != unsafe { nix::libc::geteuid() }
                || (now.kind == 0 && now.links != 1)
            {
                retained = true;
                continue;
            }
            if node.kind == 1 {
                let sub = dir.child(name)?;
                let identity = sub.identity()?;
                if identity.inode != node.inode
                    || identity.device != device
                    || identity.uid != unsafe { nix::libc::geteuid() }
                {
                    retained = true;
                    continue;
                }
                let (removed, kept) =
                    remove(&sub, &child, device, flag, protected, open, depth + 1)?;
                bytes = bytes.saturating_add(removed);
                if kept {
                    retained = true;
                    continue;
                }
            }
            let now = dir.stat(name)?;
            if now.inode != node.inode
                || now.device != device
                || now.kind != node.kind
                || now.uid != unsafe { nix::libc::geteuid() }
                || (now.kind == 0 && now.links != 1)
            {
                retained = true;
                continue;
            }
            match dir.unlink(name, node.kind == 1) {
                Ok(()) => {
                    if node.kind == 0 {
                        bytes = bytes.saturating_add(now.bytes);
                    }
                }
                Err(_) => retained = true,
            }
        }
        if locked && retained {
            let _ = dir.set_mode(mode);
        }
        Ok((bytes, retained))
    }
    let (bytes, retained) = remove(&dir, path, before.device, flag, &protected, open, 0)?;
    if !retained && !flag.load(Ordering::Acquire) && same_root(before, &Snapshot::capture(path)?) {
        parent.unlink(name, true)?;
    }
    Ok(bytes)
}
