pub mod files;
use crate::{
    AppError,
    brew::{json::Info, version::compare},
    db::Database,
    model::*,
};
use std::{
    cmp::Ordering,
    path::{Component, Path, PathBuf},
};
pub struct Scan {
    pub items: Vec<InstalledItem>,
    pub formula_paths: std::collections::HashMap<String, Vec<PathBuf>>,
}
pub fn display_name(item: Option<&CatalogItem>, fallback: &str, locale: Locale) -> String {
    item.and_then(|i| i.display_name.fallback(locale, i.source_locale))
        .filter(|s| !s.is_empty())
        .cloned()
        .unwrap_or_else(|| fallback.into())
}
fn seconds_ms(value: Option<i64>) -> Option<i64> {
    value.and_then(|s| s.checked_mul(1000))
}
pub(crate) fn app_paths(cask: &crate::brew::json::Cask) -> Vec<String> {
    let mut paths = Vec::new();
    for artifact in cask.artifacts.iter().flatten() {
        if let Some(app) = artifact.get("app").and_then(serde_json::Value::as_array) {
            let target = artifact
                .get("target")
                .and_then(serde_json::Value::as_str)
                .or_else(|| {
                    app.iter()
                        .find_map(|v| v.get("target").and_then(serde_json::Value::as_str))
                });
            let path = target.map(PathBuf::from).or_else(|| {
                app.first().and_then(serde_json::Value::as_str).map(|s| {
                    if Path::new(s).is_absolute() {
                        s.into()
                    } else {
                        Path::new("/Applications").join(s)
                    }
                })
            });
            if let Some(path) = path
                && path.is_absolute()
                && path.extension().is_some_and(|s| s == "app")
                && !path.components().any(|c| matches!(c, Component::ParentDir))
            {
                paths.push(path.to_string_lossy().into());
            }
        }
    }
    paths.sort();
    paths.dedup();
    paths
}
pub fn scan(
    database: &Database,
    info: &Info,
    prefix: &str,
    locale: Locale,
) -> Result<Scan, AppError> {
    let mut items = Vec::new();
    let mut paths = std::collections::HashMap::new();
    for formula in &info.formulae {
        crate::brew::args::validate_token(&formula.name)?;
        let Some(installed) = formula
            .installed
            .iter()
            .max_by(|a, b| compare(&a.version, &b.version).unwrap_or(Ordering::Equal))
        else {
            continue;
        };
        let catalog = crate::catalog::get(database, Kind::Formula, &formula.name)?;
        let latest = catalog.as_ref().map(|i| i.version.clone());
        let status = if formula.pinned {
            InstalledStatus::Pinned
        } else if formula.outdated {
            InstalledStatus::Outdated
        } else {
            InstalledStatus::UpToDate
        };
        let required_by = info
            .formulae
            .iter()
            .filter(|other| {
                other.name != formula.name
                    && !other.installed.is_empty()
                    && other.installed.iter().any(|installation| {
                        installation.runtime_dependencies.iter().any(|dependency| {
                            dependency.full_name == formula.name
                                || formula.full_name.as_ref() == Some(&dependency.full_name)
                        })
                    })
            })
            .map(|f| f.name.clone())
            .collect();
        let installed_paths: Vec<_> = formula
            .installed
            .iter()
            .filter_map(|v| files::formula_path(prefix, &formula.name, &v.version))
            .collect();
        let size = cached_total(database, &installed_paths);
        paths.insert(formula.name.clone(), installed_paths);
        items.push(InstalledItem {
            kind: Kind::Formula,
            token: formula.name.clone(),
            name: display_name(catalog.as_ref(), &formula.name, locale),
            installed_version: installed.version.clone(),
            actual_version: None,
            latest_version: latest,
            status,
            on_request: installed.installed_on_request,
            required_by,
            installed_at: seconds_ms(installed.time),
            size_bytes: size,
            app_paths: Vec::new(),
            icon_path: None,
            auto_updates: false,
            pinned: formula.pinned,
        });
    }
    for cask in &info.casks {
        crate::brew::args::validate_token(&cask.token)?;
        let installed_version = match &cask.installed {
            serde_json::Value::String(s) => s.clone(),
            serde_json::Value::Array(values) => values
                .iter()
                .filter_map(|v| v.as_str())
                .max_by(|a, b| compare(a, b).unwrap_or(Ordering::Equal))
                .unwrap_or_default()
                .into(),
            _ => String::new(),
        };
        if installed_version.is_empty() {
            continue;
        }
        // A cask installed from another tap under the same token is a different app.
        let catalog = crate::catalog::get(database, Kind::Cask, &cask.token)?.filter(|item| {
            cask.tap
                .as_deref()
                .is_none_or(|installed| installed == item.tap.as_deref().unwrap_or("homebrew/cask"))
        });
        let latest = catalog.as_ref().map(|i| i.version.clone());
        let app_paths = app_paths(cask);
        let actual = app_paths
            .iter()
            .find_map(|p| files::actual_version(Path::new(p)));
        let status = if cask.pinned {
            InstalledStatus::Pinned
        } else if actual.as_ref().is_some_and(|actual| {
            compare(actual, latest.as_deref().unwrap_or(&cask.version))
                .is_some_and(|order| order != Ordering::Less)
        }) {
            InstalledStatus::SelfUpdated
        } else if cask.outdated {
            InstalledStatus::Outdated
        } else {
            InstalledStatus::UpToDate
        };
        let app_files: Vec<_> = app_paths.iter().map(PathBuf::from).collect();
        let size = cached_total(database, &app_files);
        let icon = app_files
            .iter()
            .find_map(|p| files::cached_icon(database, p).ok().flatten());
        let fallback = cask.name.first().unwrap_or(&cask.token);
        items.push(InstalledItem {
            kind: Kind::Cask,
            token: cask.token.clone(),
            name: display_name(catalog.as_ref(), fallback, locale),
            installed_version,
            actual_version: actual,
            latest_version: latest,
            status,
            on_request: true,
            required_by: Vec::new(),
            installed_at: seconds_ms(cask.installed_time),
            size_bytes: size,
            app_paths,
            icon_path: icon,
            auto_updates: cask.auto_updates,
            pinned: cask.pinned,
        });
    }
    items.sort_by(|a, b| {
        a.kind
            .as_str()
            .cmp(b.kind.as_str())
            .then_with(|| a.name.to_lowercase().cmp(&b.name.to_lowercase()))
            .then_with(|| a.token.cmp(&b.token))
    });
    Ok(Scan {
        items,
        formula_paths: paths,
    })
}
fn cached_total(database: &Database, paths: &[PathBuf]) -> Option<u64> {
    if paths.is_empty() {
        return None;
    }
    paths
        .iter()
        .map(|p| files::cached_size(database, p).ok().flatten())
        .try_fold(0u64, |sum, size| size.map(|n| sum.saturating_add(n)))
}
pub fn enrich(
    database: &Database,
    scan: &mut Scan,
    icons: &Path,
    mut changed: impl FnMut(&[InstalledItem]),
) {
    let mut last = std::time::Instant::now();
    for index in 0..scan.items.len() {
        let item = &mut scan.items[index];
        let paths = if item.kind == Kind::Cask {
            item.app_paths.iter().map(PathBuf::from).collect()
        } else {
            scan.formula_paths
                .get(&item.token)
                .cloned()
                .unwrap_or_default()
        };
        if !paths.is_empty() {
            item.size_bytes = paths
                .iter()
                .map(|p| files::size(database, p).ok())
                .try_fold(0u64, |sum, n| n.map(|n| sum.saturating_add(n)));
        }
        if item.kind == Kind::Cask {
            item.icon_path = paths
                .iter()
                .find_map(|p| files::icon(database, p, icons).ok());
        }
        if last.elapsed().as_millis() >= 250 {
            changed(&scan.items);
            last = std::time::Instant::now();
        }
    }
    changed(&scan.items);
}
pub fn storage(items: &[InstalledItem], cache_bytes: u64, complete: bool) -> StorageSummary {
    StorageSummary {
        apps_bytes: items
            .iter()
            .filter(|i| i.kind == Kind::Cask)
            .filter_map(|i| i.size_bytes)
            .sum(),
        app_count: items
            .iter()
            .filter(|i| i.kind == Kind::Cask)
            .map(|i| i.app_paths.len() as u32)
            .sum(),
        formulae_bytes: items
            .iter()
            .filter(|i| i.kind == Kind::Formula)
            .filter_map(|i| i.size_bytes)
            .sum(),
        formula_count: items.iter().filter(|i| i.kind == Kind::Formula).count() as u32,
        cache_bytes,
        complete,
        measured_at: chrono::Utc::now().timestamp_millis(),
    }
}
