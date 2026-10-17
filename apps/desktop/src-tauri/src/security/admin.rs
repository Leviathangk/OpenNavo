//! Closed privileged action protocol. Every object is revalidated immediately before use.
use super::{Commands, NativeCommands, files};
use crate::{AppError, uninstall::rules::Snapshot};
use serde::{Deserialize, Serialize};
use std::{
    fs,
    os::unix::fs::{MetadataExt, PermissionsExt},
    path::{Path, PathBuf},
};
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "action", rename_all = "snake_case", deny_unknown_fields)]
pub enum Action {
    QuarantineMove {
        source: PathBuf,
        destination: PathBuf,
        snapshot: Snapshot,
    },
    QuarantineRestore {
        source: PathBuf,
        destination: PathBuf,
        snapshot: Snapshot,
        mode: u32,
    },
    LaunchdDisableSystem {
        path: PathBuf,
        snapshot: Snapshot,
        label: String,
    },
    LaunchdEnableSystem {
        path: PathBuf,
        snapshot: Snapshot,
        label: String,
    },
    /// A ServiceManagement daemon, named by its BTM identifier ("16.<label>"): it has no plist
    /// in a Launch folder, so BTM vouches for it instead.
    LaunchdService { identifier: String, enable: bool },
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Request {
    pub id: String,
    pub action: Action,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ResultItem {
    pub id: String,
    pub error: Option<AppError>,
}
pub trait Actions: Send + Sync {
    fn run(&self, requests: &[Request]) -> Result<Vec<ResultItem>, AppError>;
}
pub struct NativeActions(pub std::sync::Arc<crate::uninstall::Engine>);
impl Actions for NativeActions {
    fn run(&self, r: &[Request]) -> Result<Vec<ResultItem>, AppError> {
        crate::uninstall::execute::admin_batch_security(&self.0, r)
    }
}
fn protected(path: &Path) -> bool {
    path.starts_with("/System")
        || path.starts_with("/usr")
        || path.starts_with("/bin")
        || path.starts_with("/sbin")
        || path.starts_with("/Library/Apple")
        || path
            .file_name()
            .is_some_and(|n| n.to_string_lossy().starts_with("com.apple."))
}
fn original_allowed(path: &Path, home: &Path) -> bool {
    !protected(path)
        && path.file_name().is_some()
        && (path.starts_with(home)
            || path.starts_with("/Applications")
            || path.starts_with("/Library"))
}
fn vault_path(path: &Path, data: &Path, uid: u32) -> Result<(), AppError> {
    let vault = data.join("security/quarantine");
    let meta = files::safe_metadata(&vault)?;
    if path.parent() != Some(vault.as_path())
        || path
            .file_name()
            .and_then(|n| n.to_str())
            .is_none_or(|n| uuid::Uuid::parse_str(n).is_err())
        || meta.uid() != uid
        || meta.mode() & 0o077 != 0
        || !meta.is_dir()
    {
        return Err(AppError::new("E_PROTECTED", "security_quarantine_path"));
    }
    Ok(())
}
pub fn execute(
    requests: &[Request],
    home: &Path,
    data: &Path,
    uid: u32,
) -> Result<Vec<ResultItem>, AppError> {
    if requests.len() > 256 {
        return Err(AppError::new("E_INVALID_ARG", "security_batch_limit"));
    }
    let mut failed_launch = std::collections::BTreeSet::new();
    let mut results = vec![];
    for r in requests {
        let error = if matches!(&r.action,Action::QuarantineMove{source,..} if failed_launch.contains(source))
        {
            Some(AppError::new("E_PROTECTED", "startup_disable_failed"))
        } else {
            execute_one(&r.action, home, data, uid, &NativeCommands).err()
        };
        if error.is_some()
            && let Action::LaunchdDisableSystem { path, .. } = &r.action
        {
            failed_launch.insert(path.clone());
        }
        results.push(ResultItem {
            id: r.id.clone(),
            error,
        });
    }
    Ok(results)
}
fn execute_one(
    action: &Action,
    home: &Path,
    data: &Path,
    uid: u32,
    commands: &dyn Commands,
) -> Result<(), AppError> {
    match action {
        Action::QuarantineMove {
            source,
            destination,
            snapshot,
        } => {
            vault_path(destination, data, uid)?;
            if !original_allowed(source, home) {
                return Err(AppError::new("E_PROTECTED", "security_system_path"));
            }
            files::same_file(snapshot, source)?;
            let metadata = files::safe_metadata(source)?;
            if !metadata.is_file() || metadata.nlink() != 1 {
                return Err(AppError::new("E_PROTECTED", "security_hardlink_or_type"));
            }
            quarantine_move(source, destination, snapshot)?;
        }
        Action::QuarantineRestore {
            source,
            destination,
            snapshot,
            mode,
        } => {
            vault_path(source, data, uid)?;
            if !original_allowed(destination, home) || mode & !0o777 != 0 {
                return Err(AppError::new("E_PROTECTED", "security_restore_path"));
            }
            files::same_file(snapshot, source)?;
            // Restore permissions before rename: a failure leaves the file in the private vault.
            fs::set_permissions(source, fs::Permissions::from_mode(*mode))?;
            if let Err(e) = move_exclusive(source, destination) {
                let _ = fs::set_permissions(source, fs::Permissions::from_mode(0o0));
                return Err(e);
            }
        }
        Action::LaunchdDisableSystem {
            path,
            snapshot,
            label,
        }
        | Action::LaunchdEnableSystem {
            path,
            snapshot,
            label,
        } => {
            if ![
                Path::new("/Library/LaunchAgents"),
                Path::new("/Library/LaunchDaemons"),
            ]
            .contains(&path.parent().unwrap_or(Path::new("")))
                || protected(path)
                || label.starts_with("com.apple.")
            {
                return Err(AppError::new("E_STARTUP_PROTECTED", "startup_system_item"));
            }
            launch_action(
                commands,
                path,
                snapshot,
                label,
                &system_domain(path, uid),
                matches!(action, Action::LaunchdEnableSystem { .. }),
                None,
            )?;
        }
        Action::LaunchdService { identifier, enable } => {
            let Some((label, true)) = super::startup::service_label(Some(identifier)) else {
                return Err(AppError::new(
                    "E_STARTUP_PROTECTED",
                    "startup_service_label",
                ));
            };
            super::startup::registered_service(
                &super::startup::Roots::native(home.to_owned()),
                identifier,
                None,
            )?;
            commands.run(
                "/bin/launchctl",
                &[
                    if *enable { "enable" } else { "disable" },
                    &format!("system/{label}"),
                ],
            )?;
        }
    }
    Ok(())
}
pub(super) fn launch_action(
    commands: &dyn Commands,
    path: &Path,
    snapshot: &Snapshot,
    label: &str,
    domain: &str,
    enable: bool,
    guard: Option<&super::own_data::OwnData>,
) -> Result<(), AppError> {
    if !super::startup::valid_launch_label(label) || label.starts_with("com.apple.") {
        return Err(AppError::new("E_STARTUP_PROTECTED", "startup_label"));
    }
    files::same_file(snapshot, path)?;
    if super::startup::launch_label(path, guard)? != label {
        return Err(AppError::new("E_PROTECTED", "startup_label_changed"));
    }
    let target = format!("{domain}/{label}");
    files::same_file(snapshot, path)?;
    if enable {
        commands.run("/bin/launchctl", &["enable", &target])?;
        if commands.run("/bin/launchctl", &["print", &target]).is_err() {
            files::same_file(snapshot, path)?;
            commands.run(
                "/bin/launchctl",
                &["bootstrap", domain, &path.to_string_lossy()],
            )?;
        }
    } else {
        commands.run("/bin/launchctl", &["disable", &target])?;
        files::same_file(snapshot, path)?;
        commands.bootout(label, domain, &path.to_string_lossy())?;
    }
    Ok(())
}
fn system_domain(path: &Path, uid: u32) -> String {
    if path.parent() == Some(Path::new("/Library/LaunchAgents")) {
        format!("gui/{uid}")
    } else {
        "system".into()
    }
}
pub fn move_exclusive(source: &Path, destination: &Path) -> Result<(), AppError> {
    files::safe_metadata(
        destination
            .parent()
            .ok_or_else(|| AppError::new("E_PROTECTED", "security_parent"))?,
    )?;
    #[cfg(target_os = "macos")]
    {
        use std::{ffi::CString, os::unix::ffi::OsStrExt};
        let a = CString::new(source.as_os_str().as_bytes())
            .map_err(|_| AppError::new("E_INVALID_ARG", "security_path"))?;
        let b = CString::new(destination.as_os_str().as_bytes())
            .map_err(|_| AppError::new("E_INVALID_ARG", "security_path"))?;
        // RENAME_EXCL: do not overwrite even if a competing writer wins after our check.
        if unsafe { nix::libc::renamex_np(a.as_ptr(), b.as_ptr(), nix::libc::RENAME_EXCL) } != 0 {
            let e = std::io::Error::last_os_error();
            if e.kind() == std::io::ErrorKind::AlreadyExists {
                return Err(AppError::new(
                    "E_RESTORE_CONFLICT",
                    "restore_destination_exists",
                ));
            }
            return Err(e.into());
        }
        Ok(())
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = (source, destination);
        Err(AppError::new("E_NOT_IMPLEMENTED", "security_platform"))
    }
}

/// Keep a descriptor to the verified inode so chmod cannot follow a replaced path.
pub(super) fn quarantine_move(
    source: &Path,
    destination: &Path,
    snapshot: &Snapshot,
) -> Result<(), AppError> {
    quarantine_move_with(source, destination, snapshot, |file| {
        file.set_permissions(fs::Permissions::from_mode(0o0))
    })
}

pub(super) fn quarantine_move_with(
    source: &Path,
    destination: &Path,
    snapshot: &Snapshot,
    restrict: impl FnOnce(&fs::File) -> std::io::Result<()>,
) -> Result<(), AppError> {
    files::same_file(snapshot, source)?;
    let file = files::open_read(source, files::MAX_INSTALLER_FILE)?;
    let metadata = file.metadata()?;
    if metadata.dev() != snapshot.device
        || metadata.ino() != snapshot.inode
        || metadata.nlink() != 1
    {
        return Err(AppError::new("E_PROTECTED", "quarantine_identity"));
    }
    files::same_file(snapshot, source)?;
    let moved = snapshot.moved_to(destination)?;
    move_exclusive(source, destination)?;
    files::same_file(&moved, destination)?;
    if let Err(error) = restrict(&file) {
        // Never overwrite a new original-path occupant. If rollback cannot complete,
        // the pending database record still identifies this inode in the private vault.
        files::same_file(&moved, destination)?;
        move_exclusive(destination, source)?;
        return Err(error.into());
    }
    Ok(())
}
