//! Read-only installation preflight; never delete, replace, or automatically adopt existing apps.
use crate::{
    AppError,
    brew::{args::ReadOp, json::Cask, json::Info, locate},
    model::*,
};
use std::path::{Component, Path, PathBuf};

pub async fn inspect(
    settings: &Settings,
    target: &TaskTarget,
) -> Result<InstallPreflight, AppError> {
    inspect_listed(settings, target, None).await
}

/// Preflight with the catalog entry, if any. Casks from a third-party tap are read by their qualified
/// name; before the tap is cloned brew cannot read them, so the catalog must provide complete destination metadata for the fallback.
pub async fn inspect_listed(
    settings: &Settings,
    target: &TaskTarget,
    item: Option<CatalogItem>,
) -> Result<InstallPreflight, AppError> {
    crate::brew::args::validate_token(&target.token)?;
    if target.kind != Kind::Cask {
        return Err(AppError::new("E_INVALID_ARG", "cask_only"));
    }
    let tap = item.as_ref().and_then(|i| i.tap.clone());
    let name = crate::brew::args::qualified_token(tap.as_deref(), &target.token)?;
    let brew = locate::locate(settings).await?;
    let json = match locate::read(&brew, settings, ReadOp::Info(Kind::Cask), Some(&name)).await {
        Ok(json) => json,
        // Before the first install the tap is not cloned (and its casks are not trusted), so brew
        // cannot read the cask; the install itself taps and trusts it.
        Err(error) if tap.is_some() => {
            let item = item.ok_or(error)?;
            let artifacts = item.app_targets.map(|targets| {
                targets
                    .into_iter()
                    .map(|target| serde_json::json!({"app": ["catalog.app", {"target": target}]}))
                    .collect()
            });
            let cask = Cask {
                token: target.token.clone(),
                tap: tap.clone(),
                name: vec![item.name],
                version: item.version,
                installed: serde_json::Value::Null,
                installed_time: None,
                pinned: false,
                outdated: false,
                auto_updates: item.auto_updates,
                artifacts,
            };
            return crate::blocking(move || {
                let mut result = check(&cask, Path::new("/Applications"))?;
                result.tap = tap;
                Ok(result)
            })
            .await;
        }
        Err(error) => return Err(error),
    };
    let info: Info = serde_json::from_str(&json)?;
    let cask = info
        .casks
        .into_iter()
        .find(|c| c.token == target.token)
        .ok_or_else(|| AppError::new("E_INVALID_ARG", "cask_missing"))?;
    crate::blocking(move || {
        let mut result = check(&cask, Path::new("/Applications"))?;
        result.tap = tap;
        Ok(result)
    })
    .await
}

fn check(cask: &Cask, appdir: &Path) -> Result<InstallPreflight, AppError> {
    let mut result = InstallPreflight {
        name: cask
            .name
            .first()
            .cloned()
            .unwrap_or_else(|| cask.token.clone()),
        paths: Vec::new(),
        status: InstallPreflightStatus::Ready,
        tap: None,
    };
    if cask.installed.as_str().is_some_and(|s| !s.is_empty())
        || cask.installed.as_array().is_some_and(|a| !a.is_empty())
    {
        result.status = InstallPreflightStatus::Managed;
        return Ok(result);
    }
    let Some(artifacts) = &cask.artifacts else {
        result.status = InstallPreflightStatus::Blocked;
        return Ok(result);
    };
    for artifact in artifacts {
        let Some(app_value) = artifact.get("app") else {
            continue;
        };
        let path = app_value.as_array().and_then(|app| {
            let source = app.first()?.as_str()?;
            // The outer absolute target includes Homebrew-resolved custom appdir and renames.
            let destination = artifact
                .get("target")
                .and_then(|v| v.as_str())
                .filter(|target| Path::new(target).is_absolute())
                .or_else(|| app.iter().find_map(|v| v.get("target")?.as_str()))
                .or_else(|| artifact.get("target")?.as_str());
            let path = match destination {
                Some(target) => PathBuf::from(target),
                None => PathBuf::from(Path::new(source).file_name()?),
            };
            if path.to_string_lossy().contains(['$', '~', '#'])
                || path.extension().is_none_or(|e| e != "app")
                || path.components().any(|c| matches!(c, Component::ParentDir))
            {
                return None;
            }
            Some(if path.is_absolute() {
                path
            } else {
                appdir.join(path)
            })
        });
        let Some(path) = path else {
            result.status = InstallPreflightStatus::Blocked;
            continue;
        };
        match std::fs::symlink_metadata(&path) {
            Ok(metadata) => {
                result.paths.push(path.to_string_lossy().into());
                // Matching names alone do not prove identity; do not offer adoption for broken directories, symlinks, or non-app files.
                let valid = metadata.is_dir()
                    && plist::Value::from_file(path.join("Contents/Info.plist"))
                        .ok()
                        .and_then(|v| {
                            v.as_dictionary()?
                                .get("CFBundleIdentifier")?
                                .as_string()
                                .map(str::to_owned)
                        })
                        .is_some_and(|id| !id.is_empty());
                if !valid {
                    result.status = InstallPreflightStatus::Blocked;
                }
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(_) => result.status = InstallPreflightStatus::Blocked,
        }
    }
    result.paths.sort();
    result.paths.dedup();
    if !result.paths.is_empty() && result.status != InstallPreflightStatus::Blocked {
        result.status = InstallPreflightStatus::Conflict;
    }
    Ok(result)
}

#[cfg(test)]
mod tests {
    use super::*;
    fn cask(artifacts: serde_json::Value) -> Cask {
        serde_json::from_value(serde_json::json!({"token":"example","name":["Example"],"version":"1","artifacts":artifacts})).expect("fixture")
    }
    #[test]
    fn checks_destination_without_writing_apps() {
        let dir = tempfile::tempdir().expect("temp");
        let mut cask =
            cask(serde_json::json!([{"app":["nested/Example.app",{"target":"Renamed.app"}]}]));
        assert_eq!(
            check(&cask, dir.path()).expect("check").status,
            InstallPreflightStatus::Ready
        );
        let app = dir.path().join("Renamed.app");
        std::fs::create_dir_all(app.join("Contents")).expect("app");
        assert_eq!(
            check(&cask, dir.path()).expect("check").status,
            InstallPreflightStatus::Blocked
        );
        let mut plist = plist::Dictionary::new();
        plist.insert(
            "CFBundleIdentifier".into(),
            plist::Value::String("org.example.app".into()),
        );
        plist::Value::Dictionary(plist)
            .to_file_xml(app.join("Contents/Info.plist"))
            .expect("plist");
        let result = check(&cask, dir.path()).expect("check");
        assert_eq!(result.status, InstallPreflightStatus::Conflict);
        assert_eq!(result.paths, vec![app.to_string_lossy()]);
        cask.installed = serde_json::json!("1");
        assert_eq!(
            check(&cask, dir.path()).expect("check").status,
            InstallPreflightStatus::Managed
        );
    }
    #[test]
    fn resolved_target_overrides_relative_rename() {
        let dir = tempfile::tempdir().expect("temp");
        let app = dir.path().join("custom/Renamed.app");
        std::fs::create_dir_all(app.join("Contents")).expect("app");
        let mut info = plist::Dictionary::new();
        info.insert(
            "CFBundleIdentifier".into(),
            plist::Value::String("org.example.app".into()),
        );
        plist::Value::Dictionary(info)
            .to_file_xml(app.join("Contents/Info.plist"))
            .expect("plist");
        let cask = cask(serde_json::json!([{
            "app": ["Example.app", {"target": "Renamed.app"}],
            "target": app
        }]));
        let result = check(&cask, &dir.path().join("default")).expect("check");
        assert_eq!(result.status, InstallPreflightStatus::Conflict);
        assert_eq!(result.paths, vec![app.to_string_lossy()]);
    }
    #[test]
    fn missing_and_unsafe_artifacts_are_blocked() {
        let dir = tempfile::tempdir().expect("temp");
        for artifacts in [
            serde_json::Value::Null,
            serde_json::json!([{"app":["Example.app",{"target":"../Escape.app"}]}]),
            serde_json::json!([{"app":null}]),
        ] {
            assert_eq!(
                check(&cask(artifacts), dir.path()).expect("check").status,
                InstallPreflightStatus::Blocked
            );
        }
        assert_eq!(
            check(&cask(serde_json::json!([])), dir.path())
                .expect("check")
                .status,
            InstallPreflightStatus::Ready
        );
    }
}
