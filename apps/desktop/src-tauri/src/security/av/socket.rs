//! A per-daemon private socket directory, independent of HOME and bundle ID length.
use crate::AppError;
use std::{
    fs,
    os::unix::fs::{DirBuilderExt, MetadataExt},
    path::{Path, PathBuf},
};

pub(super) struct SocketDir {
    directory: PathBuf,
    device: u64,
    inode: u64,
}

fn error(detail: impl ToString) -> AppError {
    AppError::new("E_AV_ENGINE", "av_socket_directory").with_detail(detail.to_string())
}

impl SocketDir {
    pub fn new() -> Result<Self, AppError> {
        // Do not trust TMPDIR or derive this path from HOME: either can exceed
        // sockaddr_un.sun_path. /private/tmp avoids macOS's /tmp symlink.
        let base = if cfg!(target_os = "macos") {
            "/private/tmp"
        } else {
            "/tmp"
        };
        let base = Path::new(base);
        let meta = super::files::safe_metadata(base).map_err(|e| error(e.to_string()))?;
        if !meta.is_dir() || meta.uid() != 0 || meta.mode() & 0o1000 == 0 {
            return Err(error("temporary_parent_not_root_owned_sticky_directory"));
        }
        let directory = base.join(format!(
            "opennavo-av-{}-{}",
            unsafe { nix::libc::geteuid() },
            uuid::Uuid::now_v7().simple()
        ));
        Self::create_at(directory)
    }

    fn create_at(directory: PathBuf) -> Result<Self, AppError> {
        // Atomic mkdir; never reuse, chmod or remove a pre-existing entry.
        fs::DirBuilder::new()
            .mode(0o700)
            .create(&directory)
            .map_err(error)?;
        let meta = fs::symlink_metadata(&directory).map_err(error)?;
        let owned = Self {
            directory,
            device: meta.dev(),
            inode: meta.ino(),
        };
        owned.verify()?;
        if owned.path().as_os_str().as_encoded_bytes().len() >= 104 {
            return Err(error("socket_path_too_long"));
        }
        Ok(owned)
    }

    pub fn path(&self) -> PathBuf {
        self.directory.join("s.sock")
    }

    pub fn verify(&self) -> Result<(), AppError> {
        let meta = fs::symlink_metadata(&self.directory).map_err(error)?;
        if !meta.is_dir()
            || meta.uid() != unsafe { nix::libc::geteuid() }
            || meta.mode() & 0o777 != 0o700
            || meta.dev() != self.device
            || meta.ino() != self.inode
        {
            return Err(error("socket_directory_owner_mode_or_identity_changed"));
        }
        Ok(())
    }
}

impl Drop for SocketDir {
    fn drop(&mut self) {
        // Called after the owned child was killed and reaped. Never recursively
        // remove a substituted path, or follow a socket replaced with a symlink.
        if self.verify().is_ok() {
            let _ = fs::remove_file(self.path());
            if let Err(e) = fs::remove_dir(&self.directory) {
                log::warn!(
                    "Unable to remove AV socket directory {}: {e}",
                    self.directory.display()
                );
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::{fs::PermissionsExt, net::UnixListener};

    #[test]
    fn private_socket_is_unique_short_and_removed_on_drop() {
        let first = SocketDir::new().unwrap();
        let second = SocketDir::new().unwrap();
        let path = first.path();
        assert_ne!(path, second.path());
        assert!(path.as_os_str().as_encoded_bytes().len() < 104);
        first.verify().unwrap();
        let listener = UnixListener::bind(&path).unwrap();
        drop(listener);
        drop(first);
        assert!(!path.parent().unwrap().exists());
        second.verify().unwrap();
    }

    #[test]
    fn socket_directory_rejects_existing_entries_and_changed_permissions() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let target = root.join("existing");
        fs::create_dir(&target).unwrap();
        let link = root.join("link");
        std::os::unix::fs::symlink(&target, &link).unwrap();
        assert!(SocketDir::create_at(target.clone()).is_err());
        assert!(SocketDir::create_at(link.clone()).is_err());
        assert!(link.is_symlink());
        let owned = SocketDir::create_at(root.join("owned")).unwrap();
        fs::set_permissions(&owned.directory, fs::Permissions::from_mode(0o755)).unwrap();
        assert!(owned.verify().is_err());
        fs::set_permissions(&owned.directory, fs::Permissions::from_mode(0o700)).unwrap();
        owned.verify().unwrap();
        let replaced = owned.directory.clone();
        fs::rename(&replaced, root.join("original")).unwrap();
        fs::create_dir(&replaced).unwrap();
        fs::set_permissions(&replaced, fs::Permissions::from_mode(0o700)).unwrap();
        fs::write(replaced.join("keep"), "replacement").unwrap();
        assert!(owned.verify().is_err());
        drop(owned);
        assert_eq!(
            fs::read_to_string(replaced.join("keep")).unwrap(),
            "replacement"
        );
    }
}
