//! Conservative orphan discovery: unknown reverse-DNS files are suggestions, never selected automatically.
use super::{
    CachedApp, Engine, ItemAction, PlanEntry,
    identity::{Identity, valid_identifier},
    now, poison, rules,
    scan::{Builder, RequestGuard, check_cancel, service},
};
use crate::{AppError, model::*};
use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    sync::{Arc, atomic::AtomicBool},
    time::Instant,
};
impl Engine {
    pub(crate) async fn scan_residuals(
        self: &Arc<Self>,
        request_id: String,
        permissions: Permissions,
    ) -> Result<(ResidualReport, PlanEntry), AppError> {
        let cancel = self.begin(&request_id)?;
        let _guard = RequestGuard {
            engine: self.clone(),
            id: request_id.clone(),
        };
        let engine = self.clone();
        crate::blocking(move || engine.list()).await?;
        let engine = self.clone();
        let flag = cancel.clone();
        let (report, entry) =
            crate::blocking(move || scan(&engine, &request_id, permissions, &flag)).await?;
        check_cancel(&cancel)?;
        Ok((report, entry))
    }
}
#[derive(Debug, Clone)]
pub(crate) struct ResidualReport {
    pub groups: Vec<ResidualGroup>,
    pub incomplete: Vec<SkippedLocation>,
}
#[derive(Debug, Clone)]
pub(crate) struct ResidualGroup {
    pub id: String,
    pub name: String,
    pub bundle_id: Option<String>,
    pub active_background_items: i64,
    pub items: Vec<PlanItem>,
}

fn scan(
    engine: &Engine,
    request: &str,
    permissions: Permissions,
    flag: &AtomicBool,
) -> Result<(ResidualReport, PlanEntry), AppError> {
    let time = now();
    let locations = engine.roots.locations();
    let all: Vec<_> = engine
        .apps
        .lock()
        .map_err(poison)?
        .values()
        .cloned()
        .collect();
    let active_ids: BTreeSet<_> = all
        .iter()
        .flat_map(|a| {
            a.app
                .bundle_id
                .iter()
                .chain(a.identity.component_ids.iter())
                .chain(a.identity.groups.iter())
        })
        .cloned()
        .collect();
    let mut candidates = Vec::new();
    let mut orphan_services = BTreeSet::new();
    // A missing launchd executable is stronger evidence than a reverse-DNS filename alone.
    for (location, category) in &locations {
        if *category != ItemCategory::Background {
            continue;
        }
        if let Ok(entries) = fs::read_dir(location) {
            for entry in entries.flatten() {
                check_cancel(flag)?;
                let path = entry.path();
                if let Some(s) = service(&path)
                    && !s.label.starts_with("com.apple.")
                    && !s.programs.is_empty()
                    && all.iter().all(|a| !s.matches(a))
                    && s.programs
                        .iter()
                        .all(|p| p.is_absolute() && fs::symlink_metadata(p).is_err())
                {
                    orphan_services.insert(s.label.clone());
                    candidates.push((path, s));
                }
            }
        }
    }
    let placeholder = CachedApp {
        app: LocalApp {
            id: request.into(),
            path: engine.roots.home.to_string_lossy().into_owned(),
            name: request.into(),
            bundle_id: None,
            version: None,
            source: AppSource::Direct,
            updater: None,
            homebrew_cask: None,
            cask: None,
            catalog_token: None,
            team_id: None,
            vendor: None,
            arch: None,
            size_bytes: None,
            installed_at: None,
            last_used_at: None,
            icon_path: None,
            running: false,
            removable: true,
            protected_reason: None,
            uninstaller: None,
        },
        identity: Identity::default(),
        receipts: vec![],
    };
    let references = super::scan::launchd_references(engine, &all);
    let mut builder = Builder {
        engine,
        all,
        references,
        flag,
        last_progress: Instant::now(),
        used: Default::default(),
        pending_sizes: vec![],
        checked: 0,
        // Reserve the last step for complete sizes and final report assembly.
        total: locations.len() as i64 + 1,
        entry: PlanEntry {
            plan: UninstallPlan {
                plan_id: request.into(),
                apps: vec![],
                incomplete: vec![],
                created_at: time,
                expires_at: time + 600_000,
            },
            snapshots: BTreeMap::new(),
            actions: BTreeMap::new(),
            app_records: BTreeMap::from([(request.into(), placeholder.clone())]),
            partial: BTreeSet::new(),
        },
    };
    let mut groups: BTreeMap<String, ResidualGroup> = BTreeMap::new();
    for (path, s) in candidates {
        let group = groups
            .entry(s.label.clone())
            .or_insert_with(|| group(&s.label));
        builder.add(
            &placeholder,
            &mut group.items,
            super::scan::Candidate {
                path: path.clone(),
                category: ItemCategory::Background,
                reason: MatchReason::Launchd,
                confidence: PlanItemConfidence::High,
                action: ItemAction::Path {
                    empty_only: false,
                    bootout: Some((
                        s.label.clone(),
                        path.starts_with(&engine.roots.system_library),
                    )),
                },
                note: Some(s.label.clone()),
            },
        )?;
        if !group.items.is_empty()
            && engine
                .probe
                .loaded(&s.label, path.starts_with(&engine.roots.system_library))
        {
            group.active_background_items += 1;
        }
    }
    for (location, category) in &locations {
        builder.progress(location, false)?;
        if permissions.full_disk_access != PermissionState::Granted
            && [
                engine.roots.home.join("Library/Containers"),
                engine.roots.home.join("Library/Group Containers"),
                engine.roots.home.join("Library/Application Scripts"),
            ]
            .contains(location)
        {
            builder.skip_location(location, SkippedLocationReason::FullDiskAccess);
        }
        if matches!(
            category,
            ItemCategory::Background | ItemCategory::Cli | ItemCategory::Plugins
        ) {
            continue;
        }
        let entries = match fs::read_dir(location) {
            Ok(e) => e,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => continue,
            Err(_) => {
                builder.skip_location(location, SkippedLocationReason::Permission);
                continue;
            }
        };
        for entry in entries.flatten() {
            check_cancel(flag)?;
            let name = entry.file_name().to_string_lossy().into_owned();
            let id = inferred_id(&name);
            let Some(id) = id.filter(|id| {
                !id.starts_with("com.apple.")
                    && !active_ids
                        .iter()
                        .any(|a| rules::bundle_matches(id, a) || rules::bundle_matches(a, id))
            }) else {
                continue;
            };
            // Keep installed application name/vendor directories out of orphan suggestions too.
            if builder
                .all
                .iter()
                .any(|a| rules::normalize(&name).contains(&rules::normalize(&a.app.name)))
            {
                continue;
            }
            let g = groups.entry(id.to_owned()).or_insert_with(|| group(id));
            let proven = orphan_services
                .iter()
                .any(|label| rules::bundle_matches(label, id));
            builder.add(
                &placeholder,
                &mut g.items,
                super::scan::Candidate {
                    path: entry.path(),
                    category: *category,
                    reason: MatchReason::BundleId,
                    confidence: if proven {
                        PlanItemConfidence::High
                    } else {
                        PlanItemConfidence::Low
                    },
                    action: ItemAction::Path {
                        empty_only: false,
                        bootout: None,
                    },
                    note: None,
                },
            )?;
        }
    }
    for g in groups.values_mut() {
        // The quick per-item measurement may time out on large trees. Finish it
        // before publishing the report rather than presenting pending sizes as zero.
        for item in &mut g.items {
            if item.size_bytes.is_none() {
                item.size_bytes =
                    match rules::allocated_size_complete(std::path::Path::new(&item.path), flag) {
                        Ok(bytes) => Some(bytes),
                        Err(_) => {
                            check_cancel(flag)?;
                            builder.skip_location(
                                std::path::Path::new(&item.path),
                                SkippedLocationReason::Permission,
                            );
                            None
                        }
                    };
            }
        }
    }
    let groups: Vec<_> = groups
        .into_values()
        .filter(|g| !g.items.is_empty())
        .collect();
    let report = ResidualReport {
        groups,
        incomplete: builder.entry.plan.incomplete.clone(),
    };
    // The final event must bypass throttling, including for very fast scans.
    builder.progress(std::path::Path::new(""), true)?;
    Ok((report, builder.entry))
}
fn group(id: &str) -> ResidualGroup {
    ResidualGroup {
        id: uuid::Uuid::now_v7().to_string(),
        name: id.to_owned(),
        bundle_id: Some(id.to_owned()),
        active_background_items: 0,
        items: vec![],
    }
}
fn inferred_id(name: &str) -> Option<&str> {
    let name = name.strip_prefix("group.").unwrap_or(name);
    let name = name
        .split_once('.')
        .filter(|(prefix, suffix)| {
            prefix.len() == 10
                && prefix
                    .bytes()
                    .all(|b| b.is_ascii_uppercase() || b.is_ascii_digit())
                && ["com.", "org.", "net.", "io.", "app.", "dev."]
                    .iter()
                    .any(|p| suffix.starts_with(p))
        })
        .map(|(_, suffix)| suffix)
        .unwrap_or(name);
    let name = [".plist", ".savedState", ".binarycookies"]
        .iter()
        .find_map(|suffix| name.strip_suffix(suffix))
        .unwrap_or(name);
    // Never infer an orphan from a generic product/folder name, UUID or system identifier.
    (valid_identifier(name)
        && name.split('.').count() >= 3
        && ["com.", "org.", "net.", "io.", "app.", "dev."]
            .iter()
            .any(|prefix| name.starts_with(prefix)))
    .then_some(name)
}
