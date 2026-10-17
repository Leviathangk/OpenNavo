//! Multi-source application updates. Installation is never dispatched to brew.
pub mod admin;
pub mod archive;
pub mod backup;
mod check;
pub mod download;
pub mod electron;
pub mod feed;
pub mod gate;
pub mod macho;
pub mod outcome;
pub mod package;
pub mod prefs;
pub mod release;
pub mod rollback;
pub mod routing;
pub mod service;
pub mod swap;
mod transfer;
pub use service::Service;
pub mod types;
pub mod version;
pub use types::*;

pub fn not_implemented(operation: &str) -> crate::AppError {
    crate::AppError::new("E_NOT_IMPLEMENTED", operation)
}

impl crate::model::OutdatedItem {
    /// Catalog identity is for navigation only; a native updater always uses the
    /// stable local application identity, even when the catalog match is a link.
    pub fn task_target(&self) -> Result<crate::model::TaskTarget, crate::AppError> {
        use crate::model::{TaskKind, TaskTarget};
        if self.informational_url.is_some() {
            return Err(crate::AppError::new(
                "E_NOT_INSTALLABLE",
                "informational_update",
            ));
        }
        let kind = match self.source {
            UpdateSource::Homebrew => {
                return Ok(TaskTarget {
                    kind: self.kind.into(),
                    token: self.token.clone(),
                });
            }
            UpdateSource::Mas => TaskKind::Mas,
            _ => TaskKind::App,
        };
        let app_id = self
            .app_id
            .as_ref()
            .filter(|id| !id.is_empty())
            .ok_or_else(|| crate::AppError::new("E_UPDATE_FEED", "missing_local_app_id"))?;
        Ok(TaskTarget {
            kind,
            token: if kind == TaskKind::Mas {
                self.token.clone()
            } else {
                app_id.clone()
            },
        })
    }

    pub fn app_key(&self) -> Result<String, crate::AppError> {
        if self.source == UpdateSource::Homebrew {
            Ok(format!("homebrew:{}:{}", self.kind.as_str(), self.token))
        } else {
            self.app_id
                .as_ref()
                .filter(|id| !id.is_empty())
                .map(|id| format!("app:{id}"))
                .ok_or_else(|| crate::AppError::new("E_UPDATE_FEED", "missing_local_app_id"))
        }
    }
}

/// Default checks omit reserved sources until their engine is implemented. An
/// explicit unsupported source is an error before any network or brew action.
pub fn check_sources(
    requested: Option<Vec<UpdateSource>>,
    enabled: &UpdateSources,
) -> Result<Vec<UpdateSource>, crate::AppError> {
    let implemented = |source: &UpdateSource| {
        matches!(
            source,
            UpdateSource::Homebrew
                | UpdateSource::Mas
                | UpdateSource::Sparkle
                | UpdateSource::Electron
                | UpdateSource::Github
                | UpdateSource::Vendor
        )
    };
    match requested {
        Some(sources) if sources.iter().any(|s| !implemented(s)) => {
            Err(not_implemented("update_source_not_available"))
        }
        Some(sources) => Ok(sources),
        None => Ok(UpdateSource::ALL
            .into_iter()
            .filter(|s| enabled.enabled(*s) && implemented(s))
            .collect()),
    }
}
