use crate::{AppError, brew::args::split_cask_token, model::*};
use std::path::{Path, PathBuf};

pub fn app_path(target: &TaskTarget, library: &[InstalledItem]) -> Result<PathBuf, AppError> {
    split_cask_token(&target.token)?;
    if target.kind != Kind::Cask {
        return Err(AppError::new("E_INVALID_ARG", "formula_has_no_app"));
    }
    let item = library
        .iter()
        .find(|i| i.kind == target.kind && i.matches_token(&target.token))
        .ok_or_else(|| AppError::new("E_NOT_FOUND", "installed_app"))?;
    for path in &item.app_paths {
        let path = Path::new(path);
        if path.is_absolute()
            && path.extension().is_some_and(|extension| extension == "app")
            && path.is_dir()
        {
            return Ok(path.to_owned());
        }
    }
    Err(AppError::new("E_NOT_FOUND", "installed_app_path"))
}
pub fn privacy_url(pane: PrivacyPane, identifier: &str) -> Result<String, AppError> {
    if identifier.is_empty()
        || !identifier
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b".-".contains(&b))
    {
        return Err(AppError::new("E_INVALID_ARG", "bundle_identifier"));
    }
    Ok(match pane {
        PrivacyPane::AppManagement => {
            "x-apple.systempreferences:com.apple.preference.security?Privacy_AppBundles".into()
        }
        PrivacyPane::Notifications => {
            format!("x-apple.systempreferences:com.apple.preference.notifications?id={identifier}")
        }
        PrivacyPane::FullDiskAccess => {
            "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles".into()
        }
    })
}
pub async fn open(args: &[String]) -> Result<(), AppError> {
    let status = tokio::process::Command::new("/usr/bin/open")
        .args(args)
        .stdin(std::process::Stdio::null())
        .kill_on_drop(true)
        .status();
    let status = tokio::time::timeout(std::time::Duration::from_secs(10), status)
        .await
        .map_err(|_| AppError::new("E_TIMEOUT", "system_open"))??;
    if status.success() {
        Ok(())
    } else {
        Err(AppError::new("E_UNKNOWN", "system_open"))
    }
}
