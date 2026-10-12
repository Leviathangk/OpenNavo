use super::walk::{Directory, Node};
use crate::{
    AppError,
    model::SkippedLocationReason,
    uninstall::rules::{Snapshot, parents_safe},
};
use std::{
    collections::{BTreeMap, BTreeSet},
    ffi::OsString,
    fs,
    path::{Path, PathBuf},
    sync::{
        Arc, Condvar, Mutex, PoisonError,
        atomic::{AtomicBool, AtomicI64, AtomicU64, AtomicUsize, Ordering},
    },
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
    /// Entries seen, the root included: removing takes time per entry, so this weighs the item's share of progress.
    pub entries: u64,
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
        entries: 0,
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
    tree.entries = tree.snapshots.len() as u64;
    Ok(tree)
}

/// Read-only estimate for a busy cache. Concurrent removals are harmless; no executable plan
/// or stable snapshot is produced. Return measured bytes even if the traversal is incomplete.
pub fn measure_filtered(
    path: &Path,
    flag: &AtomicBool,
    deadline: Instant,
    excluded: impl Fn(&Path) -> bool + Sync,
) -> Result<(u64, Option<SkippedLocationReason>), AppError> {
    measure_filtered_with(path, flag, deadline, excluded, parallel_workers(true))
}
fn measure_filtered_with(
    path: &Path,
    flag: &AtomicBool,
    deadline: Instant,
    excluded: impl Fn(&Path) -> bool + Sync,
    workers: usize,
) -> Result<(u64, Option<SkippedLocationReason>), AppError> {
    check_cancel(flag)?;
    if Instant::now() >= deadline {
        return Ok((0, Some(SkippedLocationReason::Timeout)));
    }
    if excluded(path) || !parents_safe(path) {
        return Ok((0, None));
    }
    let dir = match Directory::open(path) {
        Ok(dir) => Arc::new(dir),
        Err(_) => return Ok((0, Some(SkippedLocationReason::Permission))),
    };
    let device = dir.identity()?.device;
    let mut bytes = 0u64;
    let mut incomplete = None;
    let mut pending = vec![(None, PathBuf::new())];
    let mut hardlinks = BTreeSet::new();
    let mut count = 0;
    let mut listed = 0;
    while let Some((parent, relative)) = pending.pop() {
        check_cancel(flag)?;
        if Instant::now() >= deadline {
            return Ok((bytes, Some(SkippedLocationReason::Timeout)));
        }
        if workers > 1 && listed >= SEQUENTIAL_FOLDERS {
            // A large tree: the rest is measured on several threads.
            pending.push((parent, relative));
            let walk = Walk::new(flag, deadline, MEASURE_LIMIT, count);
            let hardlinks = Mutex::new(hardlinks);
            drain(pending, workers, |(parent, relative): Pending| {
                walk.step(|| {
                    let directory = match open_pending(&dir, parent.as_ref(), &relative) {
                        Ok(dir) => dir,
                        Err(_) => return Ok(Visit::incomplete(SkippedLocationReason::Permission)),
                    };
                    if directory.identity()?.device != device {
                        return Ok(Visit::default());
                    }
                    let Ok(entries) = directory.entries() else {
                        return Ok(Visit::incomplete(SkippedLocationReason::Permission));
                    };
                    let mut visit = Visit::default();
                    for entry in entries {
                        if !walk.admit()? {
                            visit.timeout = true;
                            break;
                        }
                        let relative = relative.join(&entry.name);
                        if entry.kind == 2
                            || entry.device != device
                            || excluded(&path.join(&relative))
                        {
                            continue;
                        }
                        if entry.kind == 1 {
                            visit.folders.push((Some(directory.clone()), relative));
                        } else if entry.links == 1
                            || hardlinks
                                .lock()
                                .unwrap_or_else(PoisonError::into_inner)
                                .insert((entry.device, entry.inode))
                        {
                            visit.bytes = visit.bytes.saturating_add(entry.bytes);
                        }
                    }
                    Ok(visit)
                })
            });
            let total = walk.finish()?;
            return Ok((
                bytes.saturating_add(total.bytes),
                total.incomplete.or(incomplete),
            ));
        }
        listed += 1;
        let directory = match open_pending(&dir, parent.as_ref(), &relative) {
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
            if Instant::now() >= deadline || count > MEASURE_LIMIT {
                return Ok((bytes, Some(SkippedLocationReason::Timeout)));
            }
            let relative = relative.join(&entry.name);
            if entry.kind == 2 || entry.device != device || excluded(&path.join(&relative)) {
                continue;
            }
            if entry.kind == 1 {
                pending.push((Some(directory.clone()), relative));
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
    protected: impl Fn(&Path) -> bool + Sync,
    open: &[PathBuf],
) -> Result<Tree, AppError> {
    estimate_with(
        path,
        flag,
        deadline,
        max_nodes,
        protected,
        open,
        parallel_workers(true),
    )
}
fn estimate_with(
    path: &Path,
    flag: &AtomicBool,
    deadline: Instant,
    max_nodes: usize,
    protected: impl Fn(&Path) -> bool + Sync,
    open: &[PathBuf],
    workers: usize,
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
        entries: 1,
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
    let dir = Arc::new(Directory::open(path)?);
    let identity = dir.identity()?;
    if identity.device != root.device || identity.inode != root.inode {
        return Err(AppError::new("E_PLAN_EXPIRED", "cleanup_root_changed"));
    }
    let mut pending = vec![(None, PathBuf::new())];
    let mut count = 0usize;
    // Allocated bytes are only counted for single-link regular files, so no per-file inode set is needed.
    'scan: while let Some((parent, relative)) = pending.pop() {
        if check(flag, deadline).is_err() {
            check_cancel(flag)?;
            result.incomplete = Some(SkippedLocationReason::Timeout);
            break;
        }
        let directory = match open_pending(&dir, parent.as_ref(), &relative) {
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
                pending.push((Some(directory.clone()), relative));
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
        // A large tree keeps only its root's identity, so the order of the rest no longer matters: it is measured on
        // several threads.
        if result.compact && workers > 1 && !pending.is_empty() {
            let uid = unsafe { nix::libc::geteuid() };
            let walk = Walk::new(flag, deadline, max_nodes, count);
            drain(
                std::mem::take(&mut pending),
                workers,
                |(parent, relative): Pending| {
                    walk.step(|| {
                        let directory = match open_pending(&dir, parent.as_ref(), &relative) {
                            Ok(d) => d,
                            Err(_) => {
                                return Ok(Visit::incomplete(SkippedLocationReason::Permission));
                            }
                        };
                        let identity = directory.identity()?;
                        if identity.device != root.device || identity.uid != uid {
                            return Ok(Visit::incomplete(SkippedLocationReason::Permission));
                        }
                        let Ok(entries) = directory.entries() else {
                            return Ok(Visit::incomplete(SkippedLocationReason::Permission));
                        };
                        let mut visit = Visit::default();
                        for entry in entries {
                            if !walk.admit()? {
                                visit.timeout = true;
                                break;
                            }
                            let relative = relative.join(&entry.name);
                            let child = path.join(&relative);
                            if protected(&child) {
                                return Err(AppError::new(
                                    "E_PROTECTED",
                                    "cleanup_protected_descendant",
                                ));
                            }
                            if entry.kind == 2 {
                                continue;
                            }
                            if entry.device != root.device || entry.uid != uid {
                                visit.incomplete = Some(SkippedLocationReason::Permission);
                                continue;
                            }
                            visit.modified = visit.modified.max(entry.modified);
                            if open.iter().any(|p| child.starts_with(p)) {
                                continue;
                            }
                            if entry.kind == 1 {
                                visit.folders.push((Some(directory.clone()), relative));
                            } else if entry.links == 1 {
                                visit.bytes = visit.bytes.saturating_add(entry.bytes);
                            }
                        }
                        Ok(visit)
                    })
                },
            );
            let total = walk.finish()?;
            result.bytes = result.bytes.saturating_add(total.bytes);
            result.modified_at = result.modified_at.max(total.modified);
            if total.incomplete.is_some() {
                result.incomplete = total.incomplete;
            }
            count = total.count;
        }
    }
    if result.incomplete.is_some() {
        result.compact = true;
    }
    if result.compact {
        result.snapshots.retain(|p, _| p == path);
    }
    result.entries = count as u64 + 1;
    Ok(result)
}
/// Threads for a large walk or removal. Measured on APFS: four parallel removers are 2.3x one; six and eight are
/// slower than four.
const WORKERS: usize = 4;
/// Folders a busy-cache measurement lists on one thread before it uses several.
const SEQUENTIAL_FOLDERS: usize = 64;
/// Entries a busy-cache measurement looks at before it reports a lower bound.
const MEASURE_LIMIT: usize = 2_000_000;

/// Threads for a tree: several only when it is large and the process may keep a descriptor open per folder level of
/// every thread (a Finder-launched app starts with 256).
pub fn parallel_workers(large: bool) -> usize {
    let mut limit = nix::libc::rlimit {
        rlim_cur: 0,
        rlim_max: 0,
    };
    let room = unsafe { nix::libc::getrlimit(nix::libc::RLIMIT_NOFILE, &mut limit) } == 0
        && limit.rlim_cur >= 1024;
    if large && room { WORKERS } else { 1 }
}

/// Runs `step` for every item of a last-in-first-out stack on `workers` threads, the caller's included. `step` returns
/// the items it found to do next, or None to stop every thread; whatever is left is then dropped.
fn drain<T: Send>(initial: Vec<T>, workers: usize, step: impl Fn(T) -> Option<Vec<T>> + Sync) {
    struct Stack<T> {
        items: Vec<T>,
        busy: usize,
        stopped: bool,
    }
    let stack = Mutex::new(Stack {
        items: initial,
        busy: 0,
        stopped: false,
    });
    let ready = Condvar::new();
    let work = || {
        loop {
            let item = {
                let mut state = stack.lock().unwrap_or_else(PoisonError::into_inner);
                loop {
                    if state.stopped {
                        return;
                    }
                    if let Some(item) = state.items.pop() {
                        state.busy += 1;
                        break item;
                    }
                    // Nothing to take and nobody left who could add more.
                    if state.busy == 0 {
                        return;
                    }
                    state = ready.wait(state).unwrap_or_else(PoisonError::into_inner);
                }
            };
            let next = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| step(item)));
            let mut state = stack.lock().unwrap_or_else(PoisonError::into_inner);
            state.busy -= 1;
            match next {
                Ok(Some(items)) => state.items.extend(items),
                Ok(None) => state.stopped = true,
                // The others must not wait for this thread forever.
                Err(panic) => {
                    state.stopped = true;
                    drop(state);
                    ready.notify_all();
                    std::panic::resume_unwind(panic);
                }
            }
            drop(state);
            ready.notify_all();
        }
    };
    if workers > 1 {
        std::thread::scope(|scope| {
            for _ in 1..workers {
                scope.spawn(work);
            }
            work();
        });
    } else {
        work();
    }
}

/// A folder still to be listed, opened from its parent's descriptor when its turn comes (the root has none).
type Pending = (Option<Arc<Directory>>, PathBuf);

/// What one folder of a parallel walk added.
struct Visit {
    folders: Vec<Pending>,
    bytes: u64,
    modified: i64,
    incomplete: Option<SkippedLocationReason>,
    /// The entry limit or the deadline was reached: the walk stops with a lower bound.
    timeout: bool,
}
impl Default for Visit {
    fn default() -> Self {
        Self {
            folders: vec![],
            bytes: 0,
            modified: i64::MIN,
            incomplete: None,
            timeout: false,
        }
    }
}
impl Visit {
    fn incomplete(reason: SkippedLocationReason) -> Self {
        Self {
            incomplete: Some(reason),
            ..Self::default()
        }
    }
}
/// Totals of a parallel walk.
struct Walked {
    count: usize,
    bytes: u64,
    modified: i64,
    incomplete: Option<SkippedLocationReason>,
}
/// The shared state of the parallel part of a walk: entries admitted so far, totals and why it stopped.
struct Walk<'a> {
    flag: &'a AtomicBool,
    deadline: Instant,
    limit: usize,
    count: AtomicUsize,
    bytes: AtomicU64,
    modified: AtomicI64,
    incomplete: Mutex<Option<SkippedLocationReason>>,
    failure: Mutex<Option<AppError>>,
}
impl<'a> Walk<'a> {
    fn new(flag: &'a AtomicBool, deadline: Instant, limit: usize, count: usize) -> Self {
        Self {
            flag,
            deadline,
            limit,
            count: AtomicUsize::new(count),
            bytes: AtomicU64::new(0),
            modified: AtomicI64::new(i64::MIN),
            incomplete: Mutex::new(None),
            failure: Mutex::new(None),
        }
    }
    fn mark(&self, reason: SkippedLocationReason) {
        *self
            .incomplete
            .lock()
            .unwrap_or_else(PoisonError::into_inner) = Some(reason);
    }
    /// Counts one more entry. False once the entry limit or the deadline is reached; an error once canceled.
    fn admit(&self) -> Result<bool, AppError> {
        check_cancel(self.flag)?;
        Ok(
            Instant::now() < self.deadline
                && self.count.fetch_add(1, Ordering::AcqRel) < self.limit,
        )
    }
    /// Lists one folder with `visit` and returns the folders it found, or None to stop the walk.
    fn step(&self, visit: impl FnOnce() -> Result<Visit, AppError>) -> Option<Vec<Pending>> {
        let visit = check_cancel(self.flag).and_then(|()| {
            if Instant::now() >= self.deadline {
                Ok(Visit {
                    timeout: true,
                    ..Visit::default()
                })
            } else {
                visit()
            }
        });
        let visit = match visit {
            Ok(visit) => visit,
            Err(error) => {
                self.failure
                    .lock()
                    .unwrap_or_else(PoisonError::into_inner)
                    .get_or_insert(error);
                return None;
            }
        };
        self.bytes.fetch_add(visit.bytes, Ordering::AcqRel);
        self.modified.fetch_max(visit.modified, Ordering::AcqRel);
        if let Some(reason) = visit.incomplete {
            self.mark(reason);
        }
        if visit.timeout {
            self.mark(SkippedLocationReason::Timeout);
            return None;
        }
        Some(visit.folders)
    }
    fn finish(self) -> Result<Walked, AppError> {
        if let Some(error) = self
            .failure
            .into_inner()
            .unwrap_or_else(PoisonError::into_inner)
        {
            return Err(error);
        }
        Ok(Walked {
            count: self.count.into_inner().min(self.limit),
            bytes: self.bytes.into_inner(),
            modified: self.modified.into_inner(),
            incomplete: self
                .incomplete
                .into_inner()
                .unwrap_or_else(PoisonError::into_inner),
        })
    }
}

/// Opens a queued folder from its parent's descriptor, which the queue keeps open: one open per folder instead of one
/// per path component from the root, and still no symlink is followed at any level. The root has no parent.
fn open_pending(
    root: &Arc<Directory>,
    parent: Option<&Arc<Directory>>,
    relative: &Path,
) -> Result<Arc<Directory>, AppError> {
    match (parent, relative.file_name()) {
        (Some(parent), Some(name)) => Ok(Arc::new(parent.child(Path::new(name))?)),
        _ => Ok(root.clone()),
    }
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

/// What a removal has taken away so far; read by the run while the removal works.
#[derive(Debug, Default)]
pub struct Meter {
    pub entries: AtomicU64,
    pub bytes: AtomicU64,
}

/// Only deletes descriptor-relative, freshly checked regular files / directories.
/// Ancestors of open files remain; new links, mounts, foreign ownership and protected
/// descendants are retained. No cached list of half a million files grants deletion rights.
/// Large trees are emptied by `workers` threads; every folder is still opened from its verified parent and removed
/// from it only once everything below it is gone.
pub fn remove_cache(
    path: &Path,
    before: &Snapshot,
    flag: &AtomicBool,
    protected: impl Fn(&Path) -> bool + Sync,
    open: &[PathBuf],
    workers: usize,
    meter: &Meter,
) -> Result<u64, AppError> {
    let current = Snapshot::capture(path)?;
    if !same_root(before, &current) {
        return Err(AppError::new("E_PLAN_EXPIRED", "cleanup_root_changed"));
    }
    let parent = Directory::verified_parent(path, before)?;
    let name = Path::new(path.file_name().ok_or_else(super::cached_error)?);
    let dir = parent.child(name)?;
    let identity = dir.identity()?;
    let uid = unsafe { nix::libc::geteuid() };
    if identity.device != before.device || identity.inode != before.inode || identity.uid != uid {
        return Err(AppError::new("E_PLAN_EXPIRED", "cleanup_root_changed"));
    }
    let root = Arc::new(Folder::new(dir, path.to_owned(), None, 0)?);
    let removal = Removal {
        device: before.device,
        uid,
        flag,
        protected: &protected,
        open,
        meter,
        bytes: AtomicU64::new(0),
        failure: Mutex::new(None),
        failed: AtomicBool::new(false),
        // Kept unless the root's last piece of work says otherwise.
        root_retained: AtomicBool::new(true),
    };
    let children = removal.list(&root);
    removal.finish(root);
    drain(children, workers, |job| Some(removal.run(job)));
    if let Some(error) = removal
        .failure
        .into_inner()
        .unwrap_or_else(PoisonError::into_inner)
    {
        return Err(error);
    }
    let retained = removal.root_retained.load(Ordering::Acquire);
    if !retained && !flag.load(Ordering::Acquire) && same_root(before, &Snapshot::capture(path)?) {
        parent.unlink(name, true)?;
    }
    Ok(removal.bytes.load(Ordering::Acquire))
}

/// A folder being emptied. It stays open while anything below it is pending: its children are opened from it, and it
/// is removed through its parent's descriptor once it is empty.
struct Folder {
    dir: Directory,
    path: PathBuf,
    /// Parent, this folder's name in it and its identity when the parent listed it; None for the root.
    parent: Option<(Arc<Folder>, OsString, Node)>,
    depth: usize,
    mode: u16,
    locked: bool,
    /// This folder's own listing plus every child folder not finished yet.
    pending: AtomicUsize,
    retained: AtomicBool,
}
impl Folder {
    fn new(
        dir: Directory,
        path: PathBuf,
        parent: Option<(Arc<Folder>, OsString, Node)>,
        depth: usize,
    ) -> Result<Self, AppError> {
        if depth > 256 {
            return Err(AppError::new("E_PROTECTED", "cleanup_tree_depth"));
        }
        // Go's module cache is read-only by design (0555 directories). The caller checked that we own this directory
        // and it is being removed anyway: make it writable while emptying it, and put the mode back if anything has
        // to stay.
        let mode = dir.mode()?;
        let locked = mode & 0o200 == 0;
        if locked {
            dir.set_mode(mode | 0o200)?;
        }
        Ok(Self {
            dir,
            path,
            parent,
            depth,
            mode,
            locked,
            pending: AtomicUsize::new(1),
            retained: AtomicBool::new(false),
        })
    }
    fn keep(&self) {
        self.retained.store(true, Ordering::Release);
    }
}
/// A listed child folder waiting to be opened, checked again and emptied.
struct Job {
    parent: Arc<Folder>,
    name: OsString,
    node: Node,
    path: PathBuf,
}
struct Removal<'a, P> {
    device: u64,
    uid: u32,
    flag: &'a AtomicBool,
    protected: &'a P,
    open: &'a [PathBuf],
    meter: &'a Meter,
    bytes: AtomicU64,
    failure: Mutex<Option<AppError>>,
    failed: AtomicBool,
    root_retained: AtomicBool,
}
impl<P: Fn(&Path) -> bool + Sync> Removal<'_, P> {
    fn stopped(&self) -> bool {
        self.flag.load(Ordering::Acquire) || self.failed.load(Ordering::Acquire)
    }
    /// The first error ends the removal, as it did when one thread walked the tree.
    fn fail(&self, error: AppError) {
        self.failure
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .get_or_insert(error);
        self.failed.store(true, Ordering::Release);
    }
    fn counted(&self, bytes: u64) {
        self.bytes.fetch_add(bytes, Ordering::AcqRel);
        self.meter.entries.fetch_add(1, Ordering::Relaxed);
        self.meter.bytes.fetch_add(bytes, Ordering::Relaxed);
    }
    /// Unlinks the regular files of a folder and returns its child folders. Anything skipped keeps the folder, and so
    /// every folder above it.
    fn list(&self, folder: &Arc<Folder>) -> Vec<Job> {
        let entries = match folder.dir.entries() {
            Ok(entries) => entries,
            Err(error) => {
                self.fail(error);
                folder.keep();
                return vec![];
            }
        };
        let mut children = vec![];
        for node in entries {
            if self.stopped() {
                folder.keep();
                break;
            }
            let child = folder.path.join(&node.name);
            if (self.protected)(&child)
                || self.open.iter().any(|p| child.starts_with(p))
                || node.kind == 2
                || node.device != self.device
                || node.uid != self.uid
                || (node.kind == 0 && node.links != 1)
            {
                folder.keep();
                continue;
            }
            if node.kind == 1 {
                children.push(Job {
                    parent: folder.clone(),
                    name: node.name.clone(),
                    node,
                    path: child,
                });
                continue;
            }
            let name = Path::new(&node.name);
            // The file is checked again right before it is unlinked.
            let now = match folder.dir.stat(name) {
                Ok(now) => now,
                Err(error) => {
                    self.fail(error);
                    folder.keep();
                    break;
                }
            };
            if now.device != node.device
                || now.inode != node.inode
                || now.kind != node.kind
                || now.uid != self.uid
                || now.links != 1
            {
                folder.keep();
                continue;
            }
            match folder.dir.unlink(name, false) {
                Ok(()) => self.counted(now.bytes),
                Err(_) => folder.keep(),
            }
        }
        folder.pending.fetch_add(children.len(), Ordering::AcqRel);
        // Taken last in, first out: workers go deep before wide, so few folders are open at a time.
        children.reverse();
        children
    }
    /// Opens a queued folder from its parent after checking it is still the folder that was listed.
    fn open(&self, job: &Job) -> Result<Option<Folder>, AppError> {
        let name = Path::new(&job.name);
        let now = job.parent.dir.stat(name)?;
        if now.device != job.node.device
            || now.inode != job.node.inode
            || now.kind != job.node.kind
            || now.uid != self.uid
        {
            return Ok(None);
        }
        let dir = job.parent.dir.child(name)?;
        let identity = dir.identity()?;
        if identity.inode != job.node.inode
            || identity.device != self.device
            || identity.uid != self.uid
        {
            return Ok(None);
        }
        Folder::new(
            dir,
            job.path.clone(),
            Some((job.parent.clone(), job.name.clone(), job.node.clone())),
            job.parent.depth + 1,
        )
        .map(Some)
    }
    fn run(&self, job: Job) -> Vec<Job> {
        if self.stopped() {
            job.parent.keep();
            self.finish(job.parent);
            return vec![];
        }
        match self.open(&job) {
            Ok(Some(folder)) => {
                let folder = Arc::new(folder);
                let children = self.list(&folder);
                self.finish(folder);
                children
            }
            Ok(None) => {
                job.parent.keep();
                self.finish(job.parent);
                vec![]
            }
            Err(error) => {
                self.fail(error);
                job.parent.keep();
                self.finish(job.parent);
                vec![]
            }
        }
    }
    /// One piece of a folder's work is done. After the last one, a folder that has to stay gets its mode back and an
    /// empty one is removed from its parent (checked again first), which then counts as one piece done for the parent.
    fn finish(&self, folder: Arc<Folder>) {
        let mut folder = folder;
        loop {
            if folder.pending.fetch_sub(1, Ordering::AcqRel) != 1 {
                return;
            }
            let retained = folder.retained.load(Ordering::Acquire);
            // Restore before deciding whether to unlink: failure/cancellation in another worker,
            // identity changes, or an unlink error can all leave an otherwise empty folder behind.
            // Removing a directory needs write permission on its parent, not on the directory itself.
            if folder.locked {
                let _ = folder.dir.set_mode(folder.mode);
            }
            let Some((parent, name, node)) = &folder.parent else {
                self.root_retained.store(retained, Ordering::Release);
                return;
            };
            let parent = parent.clone();
            if retained || self.failed.load(Ordering::Acquire) {
                parent.keep();
            } else {
                let name = Path::new(name);
                match parent.dir.stat(name) {
                    Ok(now)
                        if now.inode == node.inode
                            && now.device == self.device
                            && now.kind == node.kind
                            && now.uid == self.uid =>
                    {
                        match parent.dir.unlink(name, true) {
                            Ok(()) => self.counted(0),
                            Err(_) => parent.keep(),
                        }
                    }
                    Ok(_) => parent.keep(),
                    Err(error) => {
                        self.fail(error);
                        parent.keep();
                    }
                }
            }
            folder = parent;
        }
    }
}

#[cfg(all(test, unix))]
mod removal_tests {
    use super::*;
    use std::os::unix::fs::{MetadataExt, PermissionsExt, symlink};

    struct Fixture {
        _temp: tempfile::TempDir,
        root: PathBuf,
        outside: PathBuf,
    }
    /// Removable folders and files, plus one of everything a removal has to leave in place.
    fn fixture() -> Fixture {
        let temp = tempfile::tempdir().unwrap();
        let base = temp.path().canonicalize().unwrap();
        let root = base.join("cache");
        for a in 0..12 {
            for b in 0..4 {
                let dir = root.join(format!("a{a}/b{b}"));
                fs::create_dir_all(&dir).unwrap();
                for f in 0..6 {
                    fs::write(dir.join(format!("f{f}")), [7u8; 5000]).unwrap();
                }
            }
        }
        for (dir, file) in [
            ("keep/protected", "secret"),
            ("open", "held"),
            ("links", "one"),
            ("locked", "data"),
            ("locked_keep/protected", "x"),
        ] {
            fs::create_dir_all(root.join(dir)).unwrap();
            fs::write(root.join(dir).join(file), [1u8; 3000]).unwrap();
        }
        fs::hard_link(root.join("links/one"), root.join("links/two")).unwrap();
        let outside = base.join("outside");
        fs::write(&outside, [2u8; 100]).unwrap();
        symlink(&outside, root.join("sym")).unwrap();
        // Go's module cache: read-only folders that are emptied anyway, and keep their mode if something stays.
        for dir in ["locked", "locked_keep"] {
            fs::set_permissions(root.join(dir), fs::Permissions::from_mode(0o555)).unwrap();
        }
        Fixture {
            _temp: temp,
            root,
            outside,
        }
    }
    fn remaining(root: &Path) -> BTreeSet<PathBuf> {
        let mut found = BTreeSet::new();
        let mut pending = vec![root.to_owned()];
        while let Some(dir) = pending.pop() {
            for entry in fs::read_dir(&dir).unwrap() {
                let path = entry.unwrap().path();
                if fs::symlink_metadata(&path).unwrap().is_dir() {
                    pending.push(path.clone());
                }
                found.insert(path.strip_prefix(root).unwrap().to_owned());
            }
        }
        found
    }
    fn remove(f: &Fixture, workers: usize, flag: &AtomicBool) -> (Result<u64, AppError>, Meter) {
        let before = Snapshot::capture(&f.root).unwrap();
        let meter = Meter::default();
        let protected_root = f.root.join("keep/protected");
        let protected_child = f.root.join("locked_keep/protected");
        let open = [f.root.join("open/held")];
        let result = remove_cache(
            &f.root,
            &before,
            flag,
            |p| p == protected_root || p == protected_child,
            &open,
            workers,
            &meter,
        );
        (result, meter)
    }

    #[test]
    fn failed_removal_restores_unretained_directory_modes() {
        let temp = tempfile::tempdir().unwrap();
        let root_path = temp.path().canonicalize().unwrap().join("cache");
        let child_path = root_path.join("child");
        fs::create_dir_all(&child_path).unwrap();
        for path in [&root_path, &child_path] {
            fs::set_permissions(path, fs::Permissions::from_mode(0o555)).unwrap();
        }
        let before = Snapshot::capture(&root_path).unwrap();
        let parent = Directory::verified_parent(&root_path, &before).unwrap();
        let root = Arc::new(
            Folder::new(
                parent.child(Path::new("cache")).unwrap(),
                root_path.clone(),
                None,
                0,
            )
            .unwrap(),
        );
        let flag = AtomicBool::new(false);
        let meter = Meter::default();
        let removal = Removal {
            device: before.device,
            uid: unsafe { nix::libc::geteuid() },
            flag: &flag,
            protected: &|_: &Path| false,
            open: &[],
            meter: &meter,
            bytes: AtomicU64::new(0),
            failure: Mutex::new(None),
            failed: AtomicBool::new(false),
            root_retained: AtomicBool::new(true),
        };
        let mut jobs = removal.list(&root);
        removal.finish(root);
        let child = Arc::new(removal.open(&jobs.pop().unwrap()).unwrap().unwrap());
        assert!(removal.list(&child).is_empty());
        assert!(!child.retained.load(Ordering::Acquire));
        assert_eq!(fs::metadata(&child_path).unwrap().mode() & 0o777, 0o755);
        // Reproduce another worker failing after this directory's listing has finished.
        removal.fail(AppError::new("E_TEST", "parallel_failure"));
        removal.finish(child);
        for path in [&root_path, &child_path] {
            assert_eq!(fs::metadata(path).unwrap().mode() & 0o777, 0o555);
        }
    }

    #[test]
    fn parallel_removal_keeps_exactly_what_one_worker_keeps() {
        let mut kept = vec![];
        for workers in [1, WORKERS] {
            let f = fixture();
            let before = remaining(&f.root);
            let removable: u64 = before
                .iter()
                .map(|p| f.root.join(p))
                .filter(|p| {
                    let m = fs::symlink_metadata(p).unwrap();
                    m.is_file()
                        && m.nlink() == 1
                        && !["keep", "open", "locked_keep"]
                            .iter()
                            .any(|kept| p.starts_with(f.root.join(kept)))
                })
                .map(|p| fs::metadata(p).unwrap().blocks() * 512)
                .sum();
            let (result, meter) = remove(&f, workers, &AtomicBool::new(false));
            let bytes = result.unwrap();
            assert_eq!(bytes, removable, "workers={workers}");
            assert_eq!(meter.bytes.load(Ordering::Acquire), bytes);
            let after = remaining(&f.root);
            assert_eq!(
                meter.entries.load(Ordering::Acquire),
                (before.len() - after.len()) as u64
            );
            for kept_path in [
                "keep/protected/secret",
                "open/held",
                "links/one",
                "links/two",
                "sym",
                "locked_keep/protected/x",
            ] {
                assert!(after.contains(Path::new(kept_path)), "{kept_path}");
            }
            assert!(
                !after
                    .iter()
                    .any(|p| p.starts_with("a0") || p.starts_with("locked/"))
            );
            assert!(!after.contains(Path::new("locked")));
            assert!(f.outside.exists());
            assert_eq!(
                fs::metadata(f.root.join("locked_keep")).unwrap().mode() & 0o777,
                0o555
            );
            kept.push(after);
        }
        assert_eq!(kept[0], kept[1]);
    }

    #[test]
    fn parallel_removal_removes_a_fully_removable_root() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap().join("cache");
        for i in 0..400 {
            let dir = root.join(format!("{:02}/{}", i % 17, i % 5));
            fs::create_dir_all(&dir).unwrap();
            fs::write(dir.join(format!("f{i}")), [3u8; 100]).unwrap();
        }
        let before = Snapshot::capture(&root).unwrap();
        let meter = Meter::default();
        let bytes = remove_cache(
            &root,
            &before,
            &AtomicBool::new(false),
            |_| false,
            &[],
            WORKERS,
            &meter,
        )
        .unwrap();
        assert!(!root.exists());
        assert!(bytes > 0);
        // 400 files, 17 folders and 17 * 5 folders below them.
        assert_eq!(meter.entries.load(Ordering::Acquire), 400 + 17 + 85);
    }

    #[test]
    fn canceled_parallel_removal_stops_keeps_the_root_and_restores_modes() {
        let f = fixture();
        let flag = AtomicBool::new(false);
        let calls = AtomicUsize::new(0);
        let before = Snapshot::capture(&f.root).unwrap();
        let meter = Meter::default();
        let result = remove_cache(
            &f.root,
            &before,
            &flag,
            |_| {
                if calls.fetch_add(1, Ordering::AcqRel) == 60 {
                    flag.store(true, Ordering::Release);
                }
                false
            },
            &[],
            WORKERS,
            &meter,
        );
        assert!(result.is_ok());
        assert!(f.root.exists());
        assert!(!remaining(&f.root).is_empty());
        for dir in ["locked", "locked_keep"] {
            if let Ok(metadata) = fs::metadata(f.root.join(dir)) {
                assert_eq!(metadata.mode() & 0o777, 0o555, "{dir}");
            }
        }
    }
}

#[cfg(all(test, unix))]
mod walk_tests {
    use super::*;
    use std::{os::unix::fs::symlink, time::Duration};

    /// A tree well past the 256 entries a strict snapshot keeps, with a link, a hard link and an open file.
    fn tree() -> (tempfile::TempDir, PathBuf) {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap().join("cache");
        for a in 0..30 {
            for b in 0..5 {
                let dir = root.join(format!("{a}/{b}"));
                fs::create_dir_all(&dir).unwrap();
                for f in 0..4 {
                    fs::write(dir.join(format!("{f}")), vec![9u8; 1000 * (f + 1)]).unwrap();
                }
            }
        }
        fs::write(root.join("7/1/held"), [1u8; 50_000]).unwrap();
        fs::hard_link(root.join("3/3/2"), root.join("3/4/linked")).unwrap();
        symlink(root.join("1"), root.join("12/link")).unwrap();
        (temp, root)
    }
    fn later() -> Instant {
        Instant::now() + Duration::from_secs(60)
    }

    #[test]
    fn parallel_estimate_matches_one_thread() {
        let (_temp, root) = tree();
        let open = [root.join("7/1/held")];
        let flag = AtomicBool::new(false);
        let one = estimate_with(&root, &flag, later(), 2_000_000, |_| false, &open, 1).unwrap();
        let many =
            estimate_with(&root, &flag, later(), 2_000_000, |_| false, &open, WORKERS).unwrap();
        assert!(one.compact && many.compact);
        assert_eq!(
            (
                one.bytes,
                one.modified_at,
                one.entries,
                one.incomplete,
                one.snapshots.len()
            ),
            (
                many.bytes,
                many.modified_at,
                many.entries,
                many.incomplete,
                many.snapshots.len()
            )
        );
        assert_eq!(one.snapshots.len(), 1);
        // A protected name deep in the tree refuses the whole folder either way.
        let deep = root.join("29/4/3");
        for workers in [1, WORKERS] {
            let error = estimate_with(
                &root,
                &flag,
                later(),
                2_000_000,
                |p| p == deep,
                &[],
                workers,
            )
            .unwrap_err();
            assert_eq!(error.code, "E_PROTECTED");
        }
        // The entry limit leaves a lower bound marked as incomplete.
        for workers in [1, WORKERS] {
            let limited =
                estimate_with(&root, &flag, later(), 400, |_| false, &[], workers).unwrap();
            assert_eq!(limited.incomplete, Some(SkippedLocationReason::Timeout));
            assert!(limited.bytes < one.bytes);
            assert!(limited.entries <= 401);
        }
        flag.store(true, Ordering::Release);
        for workers in [1, WORKERS] {
            let error = estimate_with(&root, &flag, later(), 2_000_000, |_| false, &[], workers)
                .unwrap_err();
            assert_eq!(error.code, "E_CANCELED");
        }
    }

    #[test]
    fn parallel_busy_measure_matches_one_thread() {
        let (_temp, root) = tree();
        let flag = AtomicBool::new(false);
        let skip = root.join("5");
        let one = measure_filtered_with(&root, &flag, later(), |p| p == skip, 1).unwrap();
        let many = measure_filtered_with(&root, &flag, later(), |p| p == skip, WORKERS).unwrap();
        assert_eq!(one, many);
        assert_eq!(one.1, None);
        let all = measure_filtered_with(&root, &flag, later(), |_| false, WORKERS).unwrap();
        assert!(all.0 > one.0);
        let late = measure_filtered_with(&root, &flag, Instant::now(), |_| false, WORKERS).unwrap();
        assert_eq!(late.1, Some(SkippedLocationReason::Timeout));
    }
}
