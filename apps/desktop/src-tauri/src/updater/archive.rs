use crate::{
    AppError,
    uninstall::{identity::command, rules},
};
use std::{
    collections::{BTreeMap, BTreeSet, VecDeque},
    fs,
    io::Read,
    path::{Component, Path, PathBuf},
    time::Duration,
};
fn fail(message: &str) -> AppError {
    AppError::new("E_UPDATE_INTEGRITY", message)
}
/// Validate every zip entry before delegating extraction to ditto (which preserves
/// macOS resource forks and framework symlinks). Symlinks are allowed only within
/// the archive and cannot be ancestors of another extracted entry.
pub fn validate_zip(path: &Path) -> Result<(), AppError> {
    let mut zip = zip::ZipArchive::new(fs::File::open(path)?).map_err(|_| fail("zip_format"))?;
    if zip.len() > 50000 {
        return Err(fail("archive_entry_limit"));
    }
    let mut paths = BTreeSet::new();
    let mut links = BTreeMap::new();
    let mut spellings = BTreeMap::new();
    let mut non_directories = BTreeSet::new();
    let mut size = 0u64;
    for index in 0..zip.len() {
        let mut file = zip.by_index(index).map_err(|_| fail("zip_entry"))?;
        let name =
            std::str::from_utf8(file.name_raw()).map_err(|_| fail("archive_name_encoding"))?;
        if name.contains(['\\', ':']) || name.chars().any(char::is_control) {
            return Err(fail("archive_name"));
        }
        let relative = PathBuf::from(name);
        if relative.is_absolute()
            || relative
                .components()
                .any(|c| !matches!(c, Component::Normal(_)))
            || relative.as_os_str().is_empty()
            || !paths.insert(relative.clone())
        {
            return Err(fail("archive_path"));
        }
        let mut prefix = PathBuf::new();
        for component in relative.components() {
            prefix.push(component);
            let key = archive_key(&prefix.to_string_lossy());
            if spellings
                .get(&key)
                .is_some_and(|previous| previous != &prefix)
            {
                return Err(fail("archive_alias_collision"));
            }
            spellings.insert(key, prefix.clone());
        }
        size = size
            .checked_add(file.size())
            .ok_or_else(|| fail("archive_size"))?;
        if size > 4 * 1024 * 1024 * 1024 {
            return Err(fail("archive_size"));
        }
        let mode = file.unix_mode().unwrap_or(0) & 0o170000;
        if !file.is_dir() {
            non_directories.insert(relative.clone());
        }
        if mode == 0o120000 {
            if file.size() > 4096 {
                return Err(fail("archive_symlink_size"));
            }
            let mut target = String::new();
            file.read_to_string(&mut target)
                .map_err(|_| fail("archive_symlink"))?;
            if target.is_empty()
                || target.chars().any(char::is_control)
                || Path::new(&target).is_absolute()
                || target.contains(['\\', ':'])
            {
                return Err(fail("archive_symlink_escape"));
            }
            let mut depth = relative.parent().map_or(0, |p| p.components().count());
            for component in Path::new(&target).components() {
                match component {
                    Component::Normal(_) => depth += 1,
                    Component::CurDir => {}
                    Component::ParentDir if depth > 0 => depth -= 1,
                    _ => return Err(fail("archive_symlink_escape")),
                }
            }
            links.insert(archive_key(&relative.to_string_lossy()), target);
        } else if !matches!(mode, 0 | 0o100000 | 0o040000) {
            return Err(fail("archive_special_file"));
        }
    }
    for path in &paths {
        if path
            .ancestors()
            .skip(1)
            .any(|p| non_directories.contains(p))
        {
            return Err(fail("archive_symlink_parent"));
        }
    }
    // Resolve chains in archive space before extraction. Lexical checks alone
    // miss e.g. a link to a sibling symlink followed by ../ components.
    for path in links.keys() {
        resolve_link(path, &links)?;
    }
    Ok(())
}
fn archive_key(value: &str) -> String {
    #[cfg(target_os = "macos")]
    {
        objc2_foundation::NSString::from_str(value)
            .decomposedStringWithCanonicalMapping()
            .lowercaseString()
            .to_string()
    }
    #[cfg(not(target_os = "macos"))]
    {
        value.to_lowercase()
    }
}
fn resolve_link(path: &str, links: &BTreeMap<String, String>) -> Result<(), AppError> {
    let mut remaining: VecDeque<String> = path.split('/').map(str::to_owned).collect();
    let mut resolved = Vec::<String>::new();
    let mut followed = 0;
    while let Some(component) = remaining.pop_front() {
        match component.as_str() {
            "" | "." => continue,
            ".." => {
                if resolved.pop().is_none() {
                    return Err(fail("archive_symlink_escape"));
                }
            }
            _ => {
                resolved.push(component);
                if let Some(target) = links.get(&resolved.join("/")) {
                    followed += 1;
                    if followed > 40 {
                        return Err(fail("archive_symlink_cycle"));
                    }
                    resolved.pop();
                    for part in archive_key(target).split('/').rev() {
                        remaining.push_front(part.to_owned());
                    }
                }
            }
        }
    }
    Ok(())
}
pub fn bundle_in(root: &Path) -> Result<PathBuf, AppError> {
    let mut found = vec![];
    let mut pending = vec![(root.to_owned(), 0)];
    while let Some((path, depth)) = pending.pop() {
        if depth > 3 {
            continue;
        }
        for entry in fs::read_dir(path)? {
            let entry = entry?;
            let meta = entry.file_type()?;
            if !meta.is_dir() || meta.is_symlink() || entry.file_name() == "__MACOSX" {
                continue;
            }
            let path = entry.path();
            if path.extension().is_some_and(|e| e == "app") {
                found.push(path);
            } else {
                pending.push((path, depth + 1));
            }
        }
    }
    if found.len() != 1 {
        return Err(fail("archive_requires_one_app"));
    }
    let app = found.remove(0);
    validate_bundle_links(&app)?;
    Ok(app)
}
pub fn validate_bundle_links(app: &Path) -> Result<(), AppError> {
    let canonical = app.canonicalize()?;
    let mut pending = vec![app.to_owned()];
    let mut count = 0;
    while let Some(path) = pending.pop() {
        for entry in fs::read_dir(path)? {
            let entry = entry?;
            count += 1;
            if count > 100000 {
                return Err(fail("bundle_entry_limit"));
            }
            let meta = entry.file_type()?;
            if meta.is_symlink() {
                if !entry.path().canonicalize()?.starts_with(&canonical) {
                    return Err(fail("bundle_symlink_escape"));
                }
            } else if meta.is_dir() {
                pending.push(entry.path());
            } else if !meta.is_file() {
                return Err(fail("bundle_special_file"));
            }
        }
    }
    Ok(())
}
pub fn extract_zip(archive: &Path, destination: &Path) -> Result<PathBuf, AppError> {
    extract_zip_artifact(archive, destination, false)
}
pub fn extract_zip_artifact(
    archive: &Path,
    destination: &Path,
    package: bool,
) -> Result<PathBuf, AppError> {
    validate_zip(archive)?;
    if !rules::parents_safe(destination) || fs::symlink_metadata(destination).is_ok() {
        return Err(fail("extraction_destination"));
    }
    use std::os::unix::fs::DirBuilderExt;
    fs::DirBuilder::new().mode(0o700).create(destination)?;
    command(
        "/usr/bin/ditto",
        &[
            "-x",
            "-k",
            archive.to_str().ok_or_else(|| fail("archive_path"))?,
            destination.to_str().ok_or_else(|| fail("archive_path"))?,
        ],
        Duration::from_secs(300),
    )?;
    artifact_in(destination, package)
}
pub fn dmg_has_license(info: &plist::Value) -> bool {
    match info {
        plist::Value::Dictionary(d) => d.iter().any(|(key, value)| {
            (key.to_lowercase().contains("license") && value.as_boolean() != Some(false))
                || dmg_has_license(value)
        }),
        plist::Value::Array(a) => a.iter().any(dmg_has_license),
        _ => false,
    }
}
pub fn extract_dmg(archive: &Path, destination: &Path) -> Result<PathBuf, AppError> {
    extract_dmg_artifact(archive, destination, false)
}
pub fn extract_dmg_artifact(
    archive: &Path,
    destination: &Path,
    package: bool,
) -> Result<PathBuf, AppError> {
    let archive = archive.to_str().ok_or_else(|| fail("archive_path"))?;
    let info = command(
        "/usr/bin/hdiutil",
        &["imageinfo", "-plist", archive],
        Duration::from_secs(30),
    )?;
    let info = plist::Value::from_reader_xml(info.as_bytes()).map_err(|_| fail("dmg_info"))?;
    if dmg_has_license(&info) {
        return Err(AppError::new(
            "E_UPDATE_DMG_LICENSE",
            "dmg_requires_license_agreement",
        ));
    }
    use std::os::unix::fs::DirBuilderExt;
    if !rules::parents_safe(destination) || fs::symlink_metadata(destination).is_ok() {
        return Err(fail("extraction_destination"));
    }
    fs::DirBuilder::new().mode(0o700).create(destination)?;
    let mount = destination.join("mount");
    fs::DirBuilder::new().mode(0o700).create(&mount)?;
    struct Detach(PathBuf);
    impl Drop for Detach {
        fn drop(&mut self) {
            let _ = command(
                "/usr/bin/hdiutil",
                &["detach", &self.0.to_string_lossy()],
                Duration::from_secs(30),
            );
        }
    }
    let mounted = command(
        "/usr/bin/hdiutil",
        &[
            "attach",
            "-readonly",
            "-nobrowse",
            "-noautoopen",
            "-mountpoint",
            &mount.to_string_lossy(),
            archive,
        ],
        Duration::from_secs(60),
    );
    if mounted.is_err() {
        return Err(AppError::new("E_UPDATE_DMG_LICENSE", "dmg_mount_refused"));
    }
    let _detach = Detach(mount.clone());
    let app = artifact_in(&mount, package)?;
    let output = destination.join(app.file_name().ok_or_else(|| fail("dmg_app_name"))?);
    command(
        "/usr/bin/ditto",
        &[
            "--rsrc",
            "--extattr",
            &app.to_string_lossy(),
            &output.to_string_lossy(),
        ],
        Duration::from_secs(300),
    )?;
    if !package {
        validate_bundle_links(&output)?;
    }
    Ok(output)
}

fn artifact_in(root: &Path, package: bool) -> Result<PathBuf, AppError> {
    if !package {
        return bundle_in(root);
    }
    let mut found = vec![];
    for entry in fs::read_dir(root)? {
        let entry = entry?;
        let meta = entry.file_type()?;
        let path = entry.path();
        if meta.is_file()
            && !meta.is_symlink()
            && path.extension().is_some_and(|e| e == "pkg" || e == "mpkg")
        {
            found.push(path);
        }
    }
    if found.len() != 1 {
        return Err(fail("archive_requires_one_package"));
    }
    Ok(found.remove(0))
}
