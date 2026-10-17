use super::types::*;
use std::{cmp::Ordering, collections::BTreeMap};

pub(crate) fn version_numbers(value: &str) -> Option<Vec<u64>> {
    let mut parts = value
        .trim()
        .split('.')
        .map(str::parse)
        .collect::<Result<Vec<u64>, _>>()
        .ok()?;
    if parts.is_empty() {
        return None;
    }
    while parts.len() > 1 && parts.last() == Some(&0) {
        parts.pop();
    }
    Some(parts)
}

pub fn detect(
    installed: &[MasInstalled],
    records: &BTreeMap<String, MasApp>,
    os: &str,
) -> Vec<MasUpdate> {
    let Some(os) = version_numbers(os) else {
        return vec![];
    };
    installed
        .iter()
        .filter_map(|local| {
            let app = records.get(&local.bundle_id)?;
            if local.adam_id != app.adam_id || local.version.trim() == app.version.trim() {
                return None;
            }
            if let Some(minimum) = &app.minimum_os
                && version_numbers(minimum).is_none_or(|minimum| minimum > os)
            {
                return None;
            }
            // Dates are mandatory evidence. Unknown metadata must not become an update suggestion.
            let installed_at = local.installed_at.filter(|at| *at > 0)?;
            let release = chrono::DateTime::parse_from_rfc3339(app.released_at.as_deref()?)
                .ok()?
                .timestamp_millis();
            if release <= installed_at || release > chrono::Utc::now().timestamp_millis() {
                return None;
            }
            let confidence = match (
                version_numbers(&local.version),
                version_numbers(&app.version),
            ) {
                (Some(local), Some(store)) => match store.cmp(&local) {
                    Ordering::Greater => MasConfidence::High,
                    _ => return None,
                },
                _ => MasConfidence::Low,
            };
            Some(MasUpdate {
                adam_id: app.adam_id,
                bundle_id: local.bundle_id.clone(),
                name: local.name.clone(),
                installed_version: local.version.clone(),
                store_version: app.version.clone(),
                released_at: app.released_at.clone(),
                release_notes: app.release_notes.clone(),
                size_bytes: app.size_bytes,
                confidence,
            })
        })
        .collect()
}
