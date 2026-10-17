//! Same-volume recovery storage. Never register anything inside these directories.
use crate::{
    AppError,
    uninstall::{execute::move_exclusive, rules::Snapshot},
};
use sha2::{Digest, Sha256};
use std::{
    fs,
    path::{Path, PathBuf},
};

const ROOT: &str = ".opennavo-updates.noindex";
const LEGACY_ROOT: &str = ".opennavo-updates";

/// Root names are recognized independently of the caller's identity so an
/// unprivileged uninstall plan remains valid when a root helper rechecks it.
pub fn is_root_name(name: &str) -> bool {
    if name == ROOT || name == LEGACY_ROOT {
        return true;
    }
    name.strip_prefix(".opennavo-updates-")
        .and_then(|s| s.strip_suffix(".noindex"))
        .and_then(|s| s.parse::<u32>().ok().map(|uid| (s, uid)))
        .is_some_and(|(s, uid)| s == uid.to_string())
}

/// Recovery contents must never appear as installed applications, including
/// explicit catalog artifact paths or aliases into a recovery tree.
pub fn contains(path: &Path) -> bool {
    path.ancestors().any(|p| {
        p.file_name()
            .is_some_and(|n| is_root_name(&n.to_string_lossy().to_ascii_lowercase()))
    })
}

fn key(app: &Path) -> Option<String> {
    if app.extension().is_none_or(|ext| ext != "app") {
        return None;
    }
    Some(format!("{:x}", Sha256::digest(app.as_os_str().as_encoded_bytes()))[..24].to_owned())
}

/// Known write/legacy paths. Discovery of other identities is separate.
pub fn directories(app: &Path) -> Option<[PathBuf; 3]> {
    let parent = app.parent()?;
    let key = key(app)?;
    Some([
        parent
            .join(format!(".opennavo-updates-{}.noindex", unsafe {
                nix::libc::geteuid()
            }))
            .join(&key),
        parent.join(LEGACY_ROOT).join(&key),
        parent.join(ROOT).join(&key),
    ])
}

pub fn is_directory(app: &Path, path: &Path) -> bool {
    let Some(key) = key(app) else {
        return false;
    };
    let Some(root) = path.parent() else {
        return false;
    };
    root.parent() == app.parent()
        && path.file_name().is_some_and(|n| n == key.as_str())
        && root
            .file_name()
            .and_then(|n| n.to_str())
            .is_some_and(is_root_name)
}

pub fn existing(app: &Path) -> Result<Vec<PathBuf>, AppError> {
    let Some(parent) = app.parent() else {
        return Ok(vec![]);
    };
    let Some(key) = key(app) else {
        return Ok(vec![]);
    };
    let mut found = Vec::new();
    for entry in fs::read_dir(parent)? {
        let root = entry?.path();
        if !root
            .file_name()
            .and_then(|n| n.to_str())
            .is_some_and(is_root_name)
        {
            continue;
        }
        let path = root.join(&key);
        match fs::symlink_metadata(&path) {
            Ok(meta) => {
                if !meta.is_dir() || meta.is_symlink() || Snapshot::capture(&path)?.kind != 1 {
                    return Err(AppError::new("E_PROTECTED", "update_backup_type"));
                }
                found.push(path);
            }
            // A private root owned by another identity cannot be inspected. It
            // is neither an uninstall target nor a reason to block an update.
            Err(e)
                if matches!(
                    e.kind(),
                    std::io::ErrorKind::NotFound | std::io::ErrorKind::PermissionDenied
                ) => {}
            Err(e) => return Err(e.into()),
        }
    }
    found.sort();
    Ok(found)
}

fn legacy_cleanup(path: PathBuf) -> Option<(PathBuf, Snapshot)> {
    use std::os::unix::fs::MetadataExt;
    let parent = path.parent()?;
    if !crate::uninstall::rules::parents_safe(&path) {
        return None;
    }
    let uid = unsafe { nix::libc::geteuid() };
    for p in [parent, path.as_path()] {
        let m = fs::symlink_metadata(p).ok()?;
        if !m.is_dir() || m.is_symlink() || m.uid() != uid || m.mode() & 0o066 != 0 {
            return None;
        }
    }
    let snapshot = Snapshot::capture(&path).ok()?;
    Some((path, snapshot))
}

fn storage_root(path: &Path) -> Result<(), AppError> {
    use std::os::unix::fs::{DirBuilderExt, MetadataExt, PermissionsExt};
    if !crate::uninstall::rules::parents_safe(path) {
        return Err(AppError::new("E_PROTECTED", "backup_parent"));
    }
    match fs::DirBuilder::new().mode(0o711).create(path) {
        Ok(()) => {}
        Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => {}
        Err(e) => return Err(e.into()),
    }
    let meta = fs::symlink_metadata(path)?;
    if !meta.is_dir()
        || meta.is_symlink()
        || meta.uid() != unsafe { nix::libc::geteuid() }
        || meta.mode() & 0o066 != 0
    {
        return Err(AppError::new("E_PROTECTED", "backup_root_owner"));
    }
    // Traversal, not listing/content access: the user can stat a known per-app
    // root to plan a privileged uninstall. Each app root remains private 0700.
    fs::set_permissions(path, fs::Permissions::from_mode(0o711))?;
    Ok(())
}

/// Failure before commit restores the previous recovery slot. If the application
/// rollback itself fails, retain all recovery material for manual recovery.
pub struct Transaction {
    pub work: PathBuf,
    pub previous: PathBuf,
    snapshot: Snapshot,
    legacy: Vec<(PathBuf, Snapshot)>,
    rotated: bool,
    published: bool,
    committed: bool,
    preserve: bool,
}
impl Transaction {
    pub fn new(app: &Path) -> Result<Self, AppError> {
        let [root, legacy, old_noindex] =
            directories(app).ok_or_else(|| AppError::new("E_PROTECTED", "update_backup_app"))?;
        storage_root(root.parent().expect("backup parent"))?;
        super::swap::private_directory(&root)?;
        // Optional compatibility cleanup must never chmod/chown old shared
        // roots or prevent a new identity-specific transaction from proceeding.
        let legacy = [legacy, old_noindex]
            .into_iter()
            .filter_map(legacy_cleanup)
            .collect();
        let work = root.join(uuid::Uuid::now_v7().to_string());
        super::swap::private_directory(&work)?;
        Ok(Self {
            previous: root.join("previous.app"),
            snapshot: Snapshot::capture(&work)?,
            work,
            legacy,
            rotated: false,
            published: false,
            committed: false,
            preserve: false,
        })
    }

    pub fn publish(&mut self) -> Result<(), AppError> {
        match fs::symlink_metadata(&self.previous) {
            Ok(_) => {
                if Snapshot::capture(&self.previous)?.kind != 1 {
                    return Err(AppError::new("E_PROTECTED", "backup_type"));
                }
                move_exclusive(&self.previous, &self.work.join("older.app"))?;
                self.rotated = true;
            }
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
            Err(e) => return Err(e.into()),
        }
        move_exclusive(&self.work.join("previous.app"), &self.previous)?;
        self.published = true;
        Ok(())
    }

    pub fn preserve_recovery(&mut self) {
        self.preserve = true;
    }

    pub fn commit(&mut self) -> PathBuf {
        self.committed = true;
        // Legacy data is pruned only after a successful install and a verified
        // new recovery copy. Existing LaunchServices records are cleaned by the
        // user; this code does not touch any other app's registration.
        for (path, snapshot) in &self.legacy {
            if snapshot.still(path) && legacy_cleanup(path.clone()).is_some() {
                if let Err(error) = fs::remove_dir_all(path) {
                    log::warn!(
                        "Legacy update backup cleanup deferred: {}: {error}",
                        path.display()
                    );
                }
            } else {
                log::warn!(
                    "Legacy update backup changed; cleanup skipped: {}",
                    path.display()
                );
            }
        }
        self.previous.clone()
    }
}
impl Drop for Transaction {
    fn drop(&mut self) {
        if self.preserve || !self.snapshot.still(&self.work) {
            return;
        }
        if !self.committed {
            let restore = (|| -> Result<(), AppError> {
                if self.published {
                    move_exclusive(&self.previous, &self.work.join("unused.app"))?;
                }
                if self.rotated {
                    move_exclusive(&self.work.join("older.app"), &self.previous)?;
                }
                Ok(())
            })();
            if let Err(error) = restore {
                log::error!(
                    "Update backup rollback failed; recovery retained at {}: {error}",
                    self.work.display()
                );
                return;
            }
        }
        if let Err(error) = fs::remove_dir_all(&self.work) {
            log::warn!(
                "Update staging cleanup deferred: {}: {error}",
                self.work.display()
            );
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::{MetadataExt, PermissionsExt};
    #[test]
    fn identities_and_legacy_roots_are_recognized_without_selecting_them_for_writes() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let app = root.join("Fixture.app");
        fs::create_dir(&app).unwrap();
        let key = key(&app).unwrap();
        for uid in [0, unsafe { nix::libc::geteuid() }, 4294967295] {
            let slot = root
                .join(format!(".opennavo-updates-{uid}.noindex"))
                .join(&key);
            assert!(is_directory(&app, &slot));
            assert!(!is_directory(&root.join("Other.app"), &slot));
        }
        for name in [
            ".opennavo-updates-00.noindex",
            ".opennavo-updates-+0.noindex",
            ".opennavo-updates--1.noindex",
            ".opennavo-updates-4294967296.noindex",
        ] {
            assert!(!is_directory(&app, &root.join(name).join(&key)));
        }
        let [current, old, old_noindex] = directories(&app).unwrap();
        let other_uid = if unsafe { nix::libc::geteuid() } == 0 {
            1
        } else {
            0
        };
        let other = root
            .join(format!(".opennavo-updates-{other_uid}.noindex"))
            .join(&key);
        fs::create_dir_all(other.join("previous.app")).unwrap();
        for path in [&old, &old_noindex] {
            fs::create_dir_all(path).unwrap();
            fs::set_permissions(path.parent().unwrap(), fs::Permissions::from_mode(0o0)).unwrap();
        }
        let transaction = Transaction::new(&app).unwrap();
        assert_eq!(transaction.previous, current.join("previous.app"));
        drop(transaction);
        for path in [&old, &old_noindex] {
            fs::set_permissions(path.parent().unwrap(), fs::Permissions::from_mode(0o700)).unwrap();
        }
        assert!(old.exists() && old_noindex.exists());
        assert_eq!(existing(&app).unwrap().len(), 4);
        assert!(other.join("previous.app").exists());
        // Exercise the production ownership rejection against a real foreign
        // owner, without chown/sudo or writing to that directory. The existing
        // directory fails ownership validation before set_permissions.
        let system_tmp = Path::new("/tmp").canonicalize().unwrap();
        let meta = fs::symlink_metadata(&system_tmp).unwrap();
        if meta.uid() != unsafe { nix::libc::geteuid() } {
            assert_eq!(
                storage_root(&system_tmp).unwrap_err().message,
                "backup_root_owner"
            );
            assert_eq!(
                fs::symlink_metadata(&system_tmp).unwrap().mode(),
                meta.mode()
            );
        }
        fs::remove_dir_all(&current).unwrap();
        fs::remove_dir(current.parent().unwrap()).unwrap();
        std::os::unix::fs::symlink(old.parent().unwrap(), current.parent().unwrap()).unwrap();
        assert!(Transaction::new(&app).is_err());
    }
}
