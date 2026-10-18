use super::types::MasEngineStatus;
use crate::AppError;
use std::{
    collections::BTreeSet,
    fs,
    path::{Path, PathBuf},
    time::Duration,
};

#[derive(Clone, Debug)]
pub struct Hints {
    pub storefront: Option<String>,
    pub locale: String,
    pub os_version: String,
    pub engine: MasEngineStatus,
}
#[derive(Clone, Debug)]
pub struct Candidate {
    pub adam_id: Option<u64>,
    pub bundle_id: String,
    pub name: String,
    pub version: String,
    pub path: String,
    pub installed_at: Option<i64>,
}
/// Every side effect is behind this interface; tests never open the App Store.
pub trait Host: Send + Sync {
    fn hints(&self) -> Result<Hints, AppError>;
    fn installed(&self) -> Result<Vec<Candidate>, AppError>;
    fn open_store(&self, url: &str) -> Result<(), AppError>;
}
pub struct NativeHost;

/// Resolve the same companion binary for both availability and task execution.
/// A bare local binary needs Helpers beside its profile directory; bundled Apps
/// carry the helper inside Contents. Never borrow it from another installation.
pub(super) fn store_helper(executable: &Path) -> Result<PathBuf, AppError> {
    executable
        .parent()
        .and_then(Path::parent)
        .map(|directory| directory.join("Helpers/opennavo-store"))
        .filter(|path| path.is_file())
        .ok_or_else(|| AppError::new("E_MAS_HELPER_MISSING", "store_helper_missing"))
}

impl Host for NativeHost {
    fn hints(&self) -> Result<Hints, AppError> {
        let read = |domain: &str, key: &str| {
            crate::uninstall::identity::command(
                "/usr/bin/defaults",
                &["read", domain, key],
                Duration::from_secs(3),
            )
            .ok()
            .map(|value| value.trim().to_owned())
        };
        let storefront = read(
            "com.apple.itunesstored",
            "AccountsNotificationPlugin.ActiveStorefrontIdentifier",
        );
        let locale = read("-g", "AppleLocale").unwrap_or_else(|| "en_US".into());
        let version = plist::Value::from_file("/System/Library/CoreServices/SystemVersion.plist")
            .ok()
            .and_then(|value| {
                value
                    .as_dictionary()?
                    .get("ProductVersion")?
                    .as_string()
                    .map(str::to_owned)
            })
            .ok_or_else(|| AppError::new("E_MAS_ENGINE", "system_version_unavailable"))?;
        let helper = std::env::current_exe()
            .ok()
            .and_then(|exe| store_helper(&exe).ok());
        // Availability describes packaging, not account authorization or proof of
        // a successful install on the current OS. Runtime errors remain explicit.
        let engine = if helper.is_some() {
            MasEngineStatus::Ok
        } else {
            MasEngineStatus::Missing
        };
        Ok(Hints {
            storefront,
            locale,
            os_version: version,
            engine,
        })
    }
    fn installed(&self) -> Result<Vec<Candidate>, AppError> {
        let mut roots = vec![
            PathBuf::from("/Applications"),
            PathBuf::from("/System/Applications"),
        ];
        if let Some(home) = std::env::var_os("HOME") {
            roots.push(PathBuf::from(home).join("Applications"));
        }
        scan(&roots, crate::uninstall::identity::spotlight_store_metadata)
    }
    fn open_store(&self, url: &str) -> Result<(), AppError> {
        crate::uninstall::identity::command("/usr/bin/open", &[url], Duration::from_secs(10))
            .map(|_| ())
    }
}

/// Enumerate bundles without following symlinks or entering cloud placeholders.
/// Receipt presence classifies the source; it is never interpreted as an Adam ID.
pub fn scan(
    roots: &[PathBuf],
    metadata: impl Fn(&Path) -> (Option<u64>, Option<i64>),
) -> Result<Vec<Candidate>, AppError> {
    fn walk(root: &Path, depth: usize, found: &mut BTreeSet<PathBuf>) -> Result<(), AppError> {
        if crate::updater::backup::contains(root)
            || root.file_name().is_some_and(|name| {
                let name = name.to_string_lossy();
                (depth < 4 && name.starts_with('.'))
                    || name.to_ascii_lowercase().ends_with(".noindex")
            })
        {
            return Ok(());
        }
        let local_error = |error: &std::io::Error| {
            matches!(
                error.kind(),
                std::io::ErrorKind::NotFound | std::io::ErrorKind::PermissionDenied
            )
        };
        let meta = match fs::symlink_metadata(root) {
            Ok(meta) => meta,
            Err(error) if local_error(&error) => {
                log::debug!("App Store scan skipped {}: {error}", root.display());
                return Ok(());
            }
            Err(error) => return Err(error.into()),
        };
        if !meta.is_dir() || meta.file_type().is_symlink() {
            return Ok(());
        }
        #[cfg(target_os = "macos")]
        {
            use std::os::macos::fs::MetadataExt;
            if meta.st_flags() & 0x4000_0000 != 0 {
                return Ok(());
            }
        }
        if root.extension().is_some_and(|ext| ext == "app") {
            found.insert(root.to_owned());
        } else if depth > 0 {
            let entries = match fs::read_dir(root) {
                Ok(entries) => entries,
                Err(error) if local_error(&error) => {
                    log::debug!("App Store scan skipped {}: {error}", root.display());
                    return Ok(());
                }
                Err(error) => return Err(error.into()),
            };
            for entry in entries {
                match entry {
                    Ok(entry) => walk(&entry.path(), depth - 1, found)?,
                    Err(error) if local_error(&error) => {
                        log::debug!("App Store entry skipped: {error}")
                    }
                    Err(error) => return Err(error.into()),
                }
            }
        }
        Ok(())
    }
    let mut paths = BTreeSet::new();
    for root in roots {
        walk(root, 4, &mut paths)?;
    }
    let mut apps = Vec::new();
    for path in paths {
        let file = path.join("Contents/Info.plist");
        if !fs::symlink_metadata(&file)
            .is_ok_and(|m| m.is_file() && !m.file_type().is_symlink() && m.len() <= 1_048_576)
        {
            continue;
        }
        let Ok(value) = plist::Value::from_file(&file) else {
            continue;
        };
        let Some(info) = value.as_dictionary() else {
            continue;
        };
        let text = |key: &str| info.get(key).and_then(plist::Value::as_string);
        let Some(bundle_id) = text("CFBundleIdentifier").filter(|id| super::valid_bundle_id(id))
        else {
            continue;
        };
        let (id, date) = metadata(&path);
        let id = id.filter(|id| super::valid_adam_id(*id));
        let receipt = path.join("Contents/_MASReceipt/receipt");
        let has_receipt =
            fs::symlink_metadata(receipt).is_ok_and(|m| m.is_file() && !m.file_type().is_symlink());
        if id.is_none() && !has_receipt {
            continue;
        }
        apps.push(Candidate {
            adam_id: id,
            bundle_id: bundle_id.into(),
            name: text("CFBundleDisplayName")
                .or_else(|| text("CFBundleName"))
                .unwrap_or(bundle_id)
                .into(),
            version: text("CFBundleShortVersionString")
                .or_else(|| text("CFBundleVersion"))
                .unwrap_or_default()
                .into(),
            path: path.to_string_lossy().into_owned(),
            installed_at: date.or_else(|| crate::uninstall::apps::created_millis(&path)),
        });
    }
    Ok(apps)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;
    #[test]
    fn store_helper_reports_missing_bundle_contents_and_accepts_local_staging() {
        let temp = tempfile::tempdir().unwrap();
        for location in ["OpenNavo.app/Contents/MacOS", "target/release"] {
            let directory = temp.path().join(location);
            fs::create_dir_all(&directory).unwrap();
            let executable = directory.join("opennavo-desktop");
            assert_eq!(
                store_helper(&executable).unwrap_err().code,
                "E_MAS_HELPER_MISSING"
            );
            let helper = directory.parent().unwrap().join("Helpers/opennavo-store");
            fs::create_dir_all(&helper).unwrap();
            assert!(store_helper(&executable).is_err());
            fs::remove_dir(&helper).unwrap();
            fs::write(&helper, b"fixture helper").unwrap();
            assert_eq!(store_helper(&executable).unwrap(), helper);
        }
    }

    #[test]
    fn inaccessible_siblings_and_recovery_copies_do_not_hide_store_apps() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap().join("Applications");
        let normal = root.join("Normal.app");
        for path in [
            normal.clone(),
            root.join(".opennavo-updates/no/previous.app"),
            root.join(".opennavo-updates.noindex/no/previous.app"),
            root.join(".opennavo-updates-0.noindex/no/previous.app"),
            root.join("Cache.noindex/Nested.app"),
            root.join(".hidden/Other.app"),
        ] {
            fs::create_dir_all(path.join("Contents")).unwrap();
            fs::write(path.join("Contents/Info.plist"),r#"<plist><dict><key>CFBundleIdentifier</key><string>org.fixture.App</string></dict></plist>"#).unwrap();
        }
        let denied = root.join("Ordinary directory");
        fs::create_dir(&denied).unwrap();
        fs::set_permissions(&denied, fs::Permissions::from_mode(0o0)).unwrap();
        std::os::unix::fs::symlink(&normal, root.join("Linked.app")).unwrap();
        for path in [
            root.join(".opennavo-updates"),
            root.join(".opennavo-updates/no/previous.app"),
        ] {
            assert!(scan(&[path], |_| (Some(42), None)).unwrap().is_empty());
        }
        let result = scan(&[root, temp.path().join("absent")], |_| (Some(42), None));
        fs::set_permissions(denied, fs::Permissions::from_mode(0o700)).unwrap();
        let apps = result.unwrap();
        assert_eq!(apps.len(), 1);
        assert_eq!(apps[0].path, normal.to_string_lossy());
    }
}
