use crate::{AppError, brew::version::compare, db::Database, library::display_name, model::*};
use rusqlite::{OptionalExtension, params};
use serde::{Deserialize, Serialize};
use std::cmp::Ordering;
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Outdated {
    #[serde(default)]
    pub formulae: Vec<Entry>,
    #[serde(default)]
    pub casks: Vec<Entry>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Entry {
    pub name: String,
    #[serde(default)]
    pub installed_versions: Vec<String>,
    pub current_version: String,
    #[serde(default, deserialize_with = "crate::brew::json::bool_or_null")]
    pub pinned: bool,
}
pub fn ignore(
    database: &Database,
    target: &TaskTarget,
    version: Option<&str>,
    until: Option<i64>,
    now: i64,
) -> Result<(), AppError> {
    crate::brew::args::validate_token(&target.token)?;
    if version.is_some_and(|v| v.is_empty() || v.len() > 8192 || v.chars().any(char::is_control))
        || until.is_some_and(|v| v < 0)
    {
        return Err(AppError::new("E_INVALID_ARG", "ignore_version"));
    }
    database.with(|c|{c.execute("INSERT INTO ignored_updates(kind,token,version,until,created_at) VALUES(?1,?2,?3,?4,?5) ON CONFLICT(kind,token) DO UPDATE SET version=excluded.version,until=excluded.until,created_at=excluded.created_at",params![target.kind.as_str(),target.token,version,until,now])?;Ok(())})
}
pub fn unignore(database: &Database, target: &TaskTarget) -> Result<(), AppError> {
    crate::brew::args::validate_token(&target.token)?;
    database.with(|c| {
        c.execute(
            "DELETE FROM ignored_updates WHERE kind=?1 AND token=?2",
            params![target.kind.as_str(), target.token],
        )?;
        Ok(())
    })
}
fn ignored(
    database: &Database,
    kind: Kind,
    token: &str,
    version: &str,
    now: i64,
) -> Result<bool, AppError> {
    database.with(|c|{
    Ok(c.query_row("SELECT (version IS NULL OR version=?3) AND (until IS NULL OR until>?4) FROM ignored_updates WHERE kind=?1 AND token=?2",params![kind.as_str(),token,version,now],|r|r.get::<_,bool>(0)).optional()?.unwrap_or(false))
})
}
/// Only original outdated results create candidates; catalog names/statistics cannot invent updates.
pub fn build(
    database: &Database,
    data: &Outdated,
    library: &mut [InstalledItem],
    locale: Locale,
    now: i64,
) -> Result<Vec<OutdatedItem>, AppError> {
    let mut items = Vec::new();
    let mut identities = std::collections::HashSet::new();
    for (kind, entries) in [(Kind::Cask, &data.casks), (Kind::Formula, &data.formulae)] {
        for entry in entries {
            crate::brew::args::validate_token(&entry.name)?;
            if !identities.insert((kind, entry.name.as_str())) {
                continue;
            }
            if ignored(database, kind, &entry.name, &entry.current_version, now)? {
                continue;
            }
            let mut local = library
                .iter_mut()
                .find(|i| i.kind == kind && i.token == entry.name);
            let catalog = crate::catalog::get(database, kind, &entry.name)?;
            let auto = local
                .as_ref()
                .map(|i| i.auto_updates)
                .unwrap_or_else(|| catalog.as_ref().is_some_and(|i| i.auto_updates));
            if kind == Kind::Cask
                && auto
                && let Some(local) = local.as_deref_mut()
            {
                let actual = if local.app_paths.is_empty() {
                    local.actual_version.clone()
                } else {
                    local.app_paths.iter().find_map(|p| {
                        crate::library::files::actual_version(std::path::Path::new(p))
                    })
                };
                local.actual_version = actual.clone();
                if actual.is_some_and(|version| {
                    compare(&version, &entry.current_version).is_some_and(|o| o != Ordering::Less)
                }) {
                    local.status = if local.pinned {
                        InstalledStatus::Pinned
                    } else {
                        InstalledStatus::SelfUpdated
                    };
                    continue;
                }
            }
            let installed = entry
                .installed_versions
                .iter()
                .max_by(|a, b| compare(a, b).unwrap_or(Ordering::Equal))
                .cloned()
                .or_else(|| local.as_ref().map(|i| i.installed_version.clone()))
                .unwrap_or_default();
            let fallback = local
                .as_ref()
                .map(|i| i.name.as_str())
                .unwrap_or(&entry.name);
            items.push(OutdatedItem {
                source: UpdateSource::Homebrew,
                app_id: None,
                bundle_id: None,
                channel: None,
                major: false,
                notes: None,
                integrity: None,
                requires_admin: false,
                running: false,
                relaunch_pending: false,
                skipped_version: None,
                critical_update: false,
                informational_url: None,
                download_host: None,
                unsigned_update: None,
                expected_team_match: None,
                kind,
                token: entry.name.clone(),
                name: display_name(catalog.as_ref(), fallback, locale),
                installed_version: installed,
                current_version: entry.current_version.clone(),
                auto_updates: auto,
                pinned: entry.pinned || local.as_ref().is_some_and(|i| i.pinned),
                ignored: false,
                dependents: local
                    .as_ref()
                    .map(|i| i.required_by.len() as u32)
                    .unwrap_or(0),
                download_size: catalog.and_then(|i| i.download_size),
            });
        }
    }
    items.sort_by(|a, b| {
        a.kind
            .as_str()
            .cmp(b.kind.as_str())
            .then_with(|| a.name.to_lowercase().cmp(&b.name.to_lowercase()))
            .then_with(|| a.token.cmp(&b.token))
    });
    Ok(items)
}
