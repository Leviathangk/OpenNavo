use super::{Engine, cached_error, poison};
use crate::{AppError, model::*, uninstall::identity::valid_identifier};
use rusqlite::{OptionalExtension, params};
use std::path::{Component, Path, PathBuf};
impl Engine {
    pub fn protected_list(&self) -> Result<Vec<ProtectRule>, AppError> {
        self.uninstall.database.with(|c| {
            let mut statement = c.prepare("SELECT id,kind,value,label,created_at FROM cleanup_protected ORDER BY created_at,id")?;
            Ok(statement.query_map([], |r| Ok(ProtectRule { id: r.get(0)?, kind: if r.get::<_, String>(1)? == "path" { ProtectKind::Path } else { ProtectKind::BundleId }, value: r.get(2)?, label: r.get(3)?, created_at: r.get(4)? }))?.collect::<rusqlite::Result<Vec<_>>>()?)
        })
    }
    pub fn protect(&self, input: ProtectInput) -> Result<ProtectRule, AppError> {
        let _operation = self.operation.lock().map_err(poison)?;
        let (kind, value, label) = match input {
            ProtectInput::Item { report_id, item_id } => {
                let entry = self.cached(&report_id, crate::uninstall::now())?;
                let item = entry
                    .items
                    .get(&item_id)
                    .ok_or_else(|| AppError::new("E_INVALID_ARG", "item_id"))?;
                if item.item.kind == CleanupItemKind::Command {
                    (
                        ProtectKind::Path,
                        command_root(&self.uninstall.roots.home, item.item.reason)
                            .to_string_lossy()
                            .into_owned(),
                        item.title.clone(),
                    )
                } else if let Some(bundle) = &item.owner.bundle {
                    (ProtectKind::BundleId, bundle.clone(), item.title.clone())
                } else {
                    (
                        ProtectKind::Path,
                        item.item.path.clone(),
                        item.title.clone(),
                    )
                }
            }
            ProtectInput::Manual { kind, value } => {
                let value = match kind {
                    ProtectKind::BundleId => {
                        if !valid_identifier(&value) {
                            return Err(AppError::new("E_INVALID_ARG", "bundle_id"));
                        }
                        value
                    }
                    ProtectKind::Path => normalize_path(&self.uninstall.roots.home, &value)?,
                };
                (kind, value.clone(), value)
            }
        };
        let proposed = ProtectRule {
            id: uuid::Uuid::now_v7().to_string(),
            kind,
            value,
            label,
            created_at: crate::uninstall::now(),
        };
        let rule = self.uninstall.database.with(|c| {
            let kind = if kind == ProtectKind::Path { "path" } else { "bundle_id" };
            let existing: Option<String> = c.query_row("SELECT id FROM cleanup_protected WHERE kind=?1 AND value=?2", params![kind, proposed.value], |r| r.get(0)).optional()?;
            if existing.is_none() { c.execute("INSERT INTO cleanup_protected(id,kind,value,label,created_at) VALUES(?1,?2,?3,?4,?5)", params![proposed.id, kind, proposed.value, proposed.label, proposed.created_at])?; }
            Ok(existing)
        })?;
        let rule = if let Some(id) = rule {
            self.protected_list()?
                .into_iter()
                .find(|r| r.id == id)
                .ok_or_else(cached_error)?
        } else {
            proposed
        };
        let protected = self.protected_list()?;
        let mut reports = self.reports.lock().map_err(poison)?;
        for entry in reports.values_mut() {
            // Keep backend execution evidence for already queued tasks; hide protected rows from the report.
            for category in &mut entry.report.categories {
                for group in &mut category.groups {
                    group.items.retain(|row| {
                        entry
                            .items
                            .get(&row.id)
                            .is_some_and(|item| !entry_is_protected(self, &protected, item))
                    });
                }
            }
            super::reconcile(entry);
        }
        if let Some(report) = reports.values().max_by_key(|r| r.report.scanned_at) {
            self.save_summary(&report.report)?;
        }
        Ok(rule)
    }
    pub fn unprotect(&self, id: &str) -> Result<(), AppError> {
        let _operation = self.operation.lock().map_err(poison)?;
        self.uninstall.database.with(|c| {
            c.execute("DELETE FROM cleanup_protected WHERE id=?1", [id])?;
            Ok(())
        })
    }
}
fn normalize_path(home: &Path, value: &str) -> Result<String, AppError> {
    if value.is_empty() || value.len() > 4096 || value.chars().any(char::is_control) {
        return Err(AppError::new("E_INVALID_ARG", "protect_path"));
    }
    let path = if value == "~" {
        home.to_owned()
    } else if let Some(rest) = value.strip_prefix("~/") {
        home.join(rest)
    } else {
        PathBuf::from(value)
    };
    if !path.is_absolute()
        || path
            .components()
            .any(|c| matches!(c, Component::ParentDir | Component::CurDir))
    {
        return Err(AppError::new("E_INVALID_ARG", "protect_path"));
    }
    // Resolve an existing selection so aliases protect the actual directory too. Missing paths remain useful rules.
    Ok(path
        .canonicalize()
        .unwrap_or(path)
        .to_string_lossy()
        .into_owned())
}
pub fn is_protected(rules: &[ProtectRule], path: &Path, bundle: Option<&str>) -> bool {
    rules.iter().any(|rule| match rule.kind {
        ProtectKind::BundleId => {
            bundle.is_some_and(|b| b == rule.value || b.starts_with(&format!("{}.", rule.value)))
        }
        ProtectKind::Path => {
            !path.as_os_str().is_empty()
                && (path.starts_with(&rule.value) || Path::new(&rule.value).starts_with(path))
        }
    })
}

pub fn entry_is_protected(
    engine: &Engine,
    protected: &[ProtectRule],
    item: &super::EntryItem,
) -> bool {
    if item.item.kind == CleanupItemKind::Command {
        is_protected(
            protected,
            &command_root(&engine.uninstall.roots.home, item.item.reason),
            None,
        )
    } else {
        is_protected(
            protected,
            Path::new(&item.item.path),
            item.owner.bundle.as_deref(),
        )
    }
}
/// The folder that stands for a fixed command in the protected list: keeping it keeps the command off the report.
pub fn command_root(home: &Path, reason: CleanupReason) -> std::path::PathBuf {
    if super::docker::prune_args(reason).is_some() {
        home.join(".docker")
    } else {
        home.join("Library/Caches/Homebrew")
    }
}
