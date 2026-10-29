use super::gate::{self, Inspector};
use crate::{
    AppError,
    uninstall::{
        identity::command,
        rules::{self, Snapshot},
    },
};
use serde::{Deserialize, Serialize};
use std::{
    fs,
    path::{Path, PathBuf},
    time::Duration,
};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SwapRequest {
    #[serde(default)]
    pub source: crate::model::UpdateSource,
    pub installed: PathBuf,
    pub prepared: PathBuf,
    pub installed_snapshot: Snapshot,
    pub prepared_snapshot: Snapshot,
    pub expected_team: Option<String>,
    #[serde(default)]
    pub unsigned: Option<super::unsigned::Approval>,
    #[serde(default)]
    pub package: Option<super::package::Artifact>,
}
impl SwapRequest {
    pub fn validate(
        &self,
        inspector: &dyn Inspector,
        prepared: &Path,
        platform: gate::Platform<'_>,
    ) -> Result<(), AppError> {
        if let Some(approval) = &self.unsigned {
            approval.validate(self, prepared, platform)
        } else {
            gate::bundles_for_source(
                inspector,
                &self.installed,
                prepared,
                &self.installed_snapshot,
                platform,
                self.expected_team.as_deref(),
                self.source,
            )
        }
    }
}
pub struct Context<'a> {
    pub inspector: &'a dyn Inspector,
    pub files: &'a dyn Files,
    pub os: &'a str,
    pub arch: &'a str,
}
pub trait Files: Send + Sync {
    fn copy_bundle(&self, from: &Path, to: &Path) -> Result<(), AppError>;
    fn exchange(&self, a: &Path, b: &Path) -> Result<(), AppError>;
    fn register(&self, path: &Path) -> Result<(), AppError>;
    fn clear_quarantine(&self, _path: &Path) -> Result<(), AppError> {
        Ok(())
    }
}
pub struct NativeFiles;
impl Files for NativeFiles {
    fn copy_bundle(&self, from: &Path, to: &Path) -> Result<(), AppError> {
        if fs::symlink_metadata(to).is_ok() {
            return Err(AppError::new("E_PROTECTED", "copy_target_exists"));
        }
        super::archive::validate_bundle_links(from)?;
        command(
            "/usr/bin/ditto",
            &[
                "--rsrc",
                "--extattr",
                from.to_str()
                    .ok_or_else(|| AppError::new("E_UPDATE_CHANGED", "bundle_path"))?,
                to.to_str()
                    .ok_or_else(|| AppError::new("E_UPDATE_CHANGED", "bundle_path"))?,
            ],
            Duration::from_secs(300),
        )
        .map(|_| ())
    }
    fn exchange(&self, a: &Path, b: &Path) -> Result<(), AppError> {
        if !rules::parents_safe(a) || !rules::parents_safe(b) {
            return Err(AppError::new("E_PROTECTED", "swap_parent"));
        }
        #[cfg(target_os = "macos")]
        {
            use std::{ffi::CString, os::unix::ffi::OsStrExt};
            let a = CString::new(a.as_os_str().as_bytes())
                .map_err(|_| AppError::new("E_PROTECTED", "swap_path"))?;
            let b = CString::new(b.as_os_str().as_bytes())
                .map_err(|_| AppError::new("E_PROTECTED", "swap_path"))?;
            if unsafe { nix::libc::renamex_np(a.as_ptr(), b.as_ptr(), nix::libc::RENAME_SWAP) } != 0
            {
                return Err(std::io::Error::last_os_error().into());
            }
            Ok(())
        }
        #[cfg(not(target_os = "macos"))]
        {
            let _ = (a, b);
            Err(AppError::new("E_NOT_IMPLEMENTED", "atomic_swap_platform"))
        }
    }
    fn register(&self, path: &Path) -> Result<(), AppError> {
        register(path)
    }
    fn clear_quarantine(&self, path: &Path) -> Result<(), AppError> {
        #[cfg(target_os = "macos")]
        {
            use std::{ffi::CString, os::unix::ffi::OsStrExt};
            let mut pending = vec![path.to_owned()];
            while let Some(path) = pending.pop() {
                let meta = fs::symlink_metadata(&path)?;
                let name = CString::new(path.as_os_str().as_bytes())
                    .map_err(|_| AppError::new("E_PROTECTED", "quarantine_path"))?;
                let code = unsafe {
                    nix::libc::removexattr(
                        name.as_ptr(),
                        c"com.apple.quarantine".as_ptr(),
                        nix::libc::XATTR_NOFOLLOW,
                    )
                };
                if code != 0 {
                    let error = std::io::Error::last_os_error();
                    if error.raw_os_error() != Some(nix::libc::ENOATTR) {
                        return Err(error.into());
                    }
                }
                if meta.is_dir() && !meta.is_symlink() {
                    for entry in fs::read_dir(&path)? {
                        pending.push(entry?.path());
                    }
                }
            }
        }
        Ok(())
    }
}
pub fn register(path: &Path) -> Result<(), AppError> {
    #[cfg(target_os = "macos")]
    {
        use objc2_foundation::{NSString, NSURL};
        #[link(name = "CoreServices", kind = "framework")]
        unsafe extern "C" {
            fn LSRegisterURL(url: *const std::ffi::c_void, update: bool) -> i32;
        }
        let code = objc2::rc::autoreleasepool(|_| {
            let url = NSURL::fileURLWithPath(&NSString::from_str(&path.to_string_lossy()));
            unsafe { LSRegisterURL((&*url as *const NSURL).cast(), true) }
        });
        if code != 0 {
            return Err(
                AppError::new("E_UPDATE_FEED", "launch_services_registration")
                    .with_detail(code.to_string()),
            );
        }
        Ok(())
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = path;
        Err(AppError::new(
            "E_NOT_IMPLEMENTED",
            "launch_services_platform",
        ))
    }
}
pub(crate) fn private_directory(path: &Path) -> Result<(), AppError> {
    use std::os::unix::fs::{DirBuilderExt, MetadataExt};
    if !rules::parents_safe(path) {
        return Err(AppError::new("E_PROTECTED", "private_directory_parent"));
    }
    match fs::DirBuilder::new().mode(0o700).create(path) {
        Ok(()) => {}
        Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => {}
        Err(e) => return Err(e.into()),
    }
    let meta = fs::symlink_metadata(path)?;
    if !meta.is_dir()
        || meta.is_symlink()
        || meta.uid() != unsafe { nix::libc::geteuid() }
        || meta.mode() & 0o077 != 0
    {
        return Err(AppError::new("E_PROTECTED", "private_directory_owner"));
    }
    Ok(())
}
/// Old version is copied to a private, same-volume backup BEFORE the atomic
/// exchange. Registration errors exchange the old bundle back automatically.
pub fn apply(request: &SwapRequest, context: &Context<'_>) -> Result<PathBuf, AppError> {
    if request.installed.extension().is_none_or(|e| e != "app")
        || request.prepared.extension().is_none_or(|e| e != "app")
        || request.installed == request.prepared
        || !request.prepared_snapshot.matches(&request.prepared)
    {
        return Err(AppError::new("E_UPDATE_CHANGED", "swap_plan_changed"));
    }
    request.validate(
        context.inspector,
        &request.prepared,
        gate::Platform {
            os: context.os,
            arch: context.arch,
        },
    )?;
    let mut recovery = super::backup::Transaction::new(&request.installed)?;
    let work = &recovery.work;
    let prepared = work.join("replacement.app");
    let backup = work.join("previous.app");
    context.files.copy_bundle(&request.prepared, &prepared)?;
    // Check the private copy. Validation of a mutable staging pathname alone is
    // insufficient when this action runs as root.
    request.validate(
        context.inspector,
        &prepared,
        gate::Platform {
            os: context.os,
            arch: context.arch,
        },
    )?;
    if request.unsigned.is_none() {
        context.files.clear_quarantine(&prepared)?;
    }
    context.files.copy_bundle(&request.installed, &backup)?;
    if let Some(approval) = &request.unsigned {
        approval.verify_backup(&request.installed, &backup)?;
    } else {
        let before = context.inspector.inspect(&backup)?;
        let installed = context.inspector.inspect(&request.installed)?;
        if before.team_id != installed.team_id
            || before.version != installed.version
            || before.requirement != installed.requirement
            || !before.valid
        {
            return Err(AppError::new("E_UPDATE_CHANGED", "backup_verification"));
        }
    }
    if !request.installed_snapshot.still(&request.installed) {
        return Err(AppError::new("E_UPDATE_CHANGED", "installed_app_changed"));
    }
    // Commit the verified backup before replacing the live bundle. Housekeeping
    // failures cannot be reported as a failed update after a successful exchange.
    recovery.publish()?;
    context.files.exchange(&request.installed, &prepared)?;
    if let Err(error) = context.files.register(&request.installed) {
        if let Err(rollback) = context.files.exchange(&request.installed, &prepared) {
            recovery.preserve_recovery();
            return Err(
                AppError::new("E_UNKNOWN", "update_rollback_failed").with_detail(
                    serde_json::json!({"failure": error, "rollback": rollback, "recovery": recovery.previous}).to_string()
                ),
            );
        }
        if !Snapshot::capture(&request.installed)
            .is_ok_and(|restored| request.installed_snapshot.same_object(&restored))
        {
            recovery.preserve_recovery();
            return Err(AppError::new("E_UNKNOWN", "rollback_identity_changed")
                .with_detail(recovery.previous.display().to_string()));
        }
        return Err(error.context("E_UPDATE_SWAP", "update_reverted"));
    }
    Ok(recovery.commit())
}
