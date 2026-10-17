use super::{
    CachedApp, Engine, ItemAction, PlanEntry, identity, now, poison,
    rules::{self, Snapshot},
};
use crate::{AppError, events::CoreEvent, model::*};
use serde_json::Value;
use sha1::{Digest, Sha1};
use std::{
    collections::{BTreeMap, BTreeSet, HashSet},
    fs,
    path::{Path, PathBuf},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};

pub fn rule_bundle_matches(raw: &Value, id: Option<&str>) -> bool {
    let Some(id) = id else {
        return false;
    };
    declarations(raw, "uninstall")
        .into_iter()
        .chain(declarations(raw, "zap"))
        .any(|v| {
            ["quit", "launchctl"]
                .iter()
                .any(|key| strings(v.get(*key)).iter().any(|s| s == id))
        })
}
pub fn declarations(raw: &Value, phase: &str) -> Vec<Value> {
    if let Some(artifacts) = raw.get("artifacts").and_then(Value::as_array) {
        return artifacts
            .iter()
            .filter_map(|a| a.get(phase))
            .flat_map(|v| match v {
                Value::Array(v) => v.clone(),
                Value::Object(_) => vec![v.clone()],
                _ => vec![],
            })
            .collect();
    }
    raw.pointer("/artifacts/entries")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .filter(|e| {
            e.get("phase").and_then(Value::as_str)
                == Some(if phase == "zap" { "cleanup" } else { phase })
        })
        .filter_map(|e| e.get("declaration").and_then(|d| d.get(phase)))
        .flat_map(|v| match v {
            Value::Array(v) => v.clone(),
            Value::Object(_) => vec![v.clone()],
            _ => vec![],
        })
        .collect()
}
pub fn strings(value: Option<&Value>) -> Vec<String> {
    match value {
        Some(Value::String(s)) => vec![s.clone()],
        Some(Value::Array(v)) => v
            .iter()
            .filter_map(Value::as_str)
            .map(str::to_owned)
            .collect(),
        _ => vec![],
    }
}

pub(super) struct RequestGuard {
    pub engine: Arc<Engine>,
    pub id: String,
}
impl Drop for RequestGuard {
    fn drop(&mut self) {
        if let Ok(mut requests) = self.engine.requests.lock() {
            requests.remove(&self.id);
        }
    }
}
impl Engine {
    pub async fn plan(
        self: &Arc<Self>,
        request_id: String,
        app_ids: Vec<String>,
        permissions: Permissions,
    ) -> Result<UninstallPlan, AppError> {
        if app_ids.is_empty()
            || app_ids.len() > 100
            || app_ids.iter().collect::<BTreeSet<_>>().len() != app_ids.len()
        {
            return Err(AppError::new("E_INVALID_ARG", "app_ids"));
        }
        let cancel = self.begin(&request_id)?;
        let _guard = RequestGuard {
            engine: self.clone(),
            id: request_id.clone(),
        };
        let engine = self.clone();
        crate::blocking(move || engine.list().map(|_| ())).await?;
        self.hydrate_catalog().await?;
        let mut targets = Vec::new();
        let mut source_rules = BTreeMap::new();
        for id in app_ids {
            check_cancel(&cancel)?;
            let app = self.app(&id)?;
            if let Some(token) = app.app.cask.as_ref().or(app.app.catalog_token.as_ref())
                && let Ok(Some(raw)) = self.fetch_rule(token).await
            {
                source_rules.insert(id.clone(), raw);
            }
            targets.push(app);
        }
        check_cancel(&cancel)?;
        let engine = self.clone();
        let cancel_scan = cancel.clone();
        let entry = crate::blocking(move || {
            scan_plan(
                &engine,
                &request_id,
                targets,
                source_rules,
                permissions,
                &cancel_scan,
            )
        })
        .await?;
        check_cancel(&cancel)?;
        let result = entry.plan.clone();
        let mut requests = self.requests.lock().map_err(poison)?;
        check_cancel(&cancel)?;
        let mut plans = self.plans.lock().map_err(poison)?;
        plans.retain(|_, p| p.plan.expires_at > now());
        plans.insert(result.plan_id.clone(), entry);
        requests.remove(&result.plan_id);
        Ok(result)
    }
}
pub(super) fn check_cancel(flag: &AtomicBool) -> Result<(), AppError> {
    if flag.load(Ordering::Acquire) {
        Err(AppError::new("E_SCAN_CANCELED", "uninstall_scan_canceled"))
    } else {
        Ok(())
    }
}
pub(super) struct Builder<'a> {
    pub engine: &'a Engine,
    pub entry: PlanEntry,
    pub all: Vec<CachedApp>,
    pub flag: &'a AtomicBool,
    pub last_progress: Instant,
    pub checked: i64,
    pub total: i64,
    pub references: Vec<(PathBuf, String, String)>,
    /// Item ids already used in this plan.
    pub used: HashSet<String>,
    /// Items whose quick size measurement ran out of time; `finish_sizes` measures them before the plan is shown.
    pub pending_sizes: Vec<(String, PathBuf)>,
}
/// What an item does, as part of its stable id.
fn action_key(action: &ItemAction) -> &'static str {
    match action {
        ItemAction::Path {
            empty_only: true, ..
        } => "rmdir",
        ItemAction::Path {
            bootout: Some(_), ..
        } => "service",
        ItemAction::Path { .. } => "path",
        ItemAction::Forget { .. } => "forget",
        ItemAction::RestoreTrash { .. } => "restore",
        ItemAction::PurgeTrash => "purge",
        ItemAction::BrewStep => "brew",
        ItemAction::ToolCommand => "tool",
        ItemAction::LoginItem => "login",
    }
}
pub(super) struct Candidate {
    pub path: PathBuf,
    pub category: ItemCategory,
    pub reason: MatchReason,
    pub confidence: PlanItemConfidence,
    pub action: ItemAction,
    pub note: Option<String>,
}
impl Builder<'_> {
    /// Stable across rescans of the same files, so the user's choices survive a refreshed plan; unique in the plan.
    pub(super) fn item_id(&mut self, app: &str, parts: &[&str]) -> String {
        let mut hasher = Sha1::new();
        hasher.update(app.as_bytes());
        for part in parts {
            hasher.update([0u8]);
            hasher.update(part.as_bytes());
        }
        let base = format!("{:x}", hasher.finalize())[..24].to_owned();
        let mut id = base.clone();
        let mut n = 1;
        while !self.used.insert(id.clone()) {
            n += 1;
            id = format!("{base}-{n}");
        }
        id
    }
    /// Measure what the quick pass left pending_sizes, all the way (the scan can still be canceled). An unknown size hid
    /// where most of the space went and made the app's footprint incomplete.
    pub(super) fn finish_sizes(&mut self, items: &mut [PlanItem]) -> Result<(), AppError> {
        let pending = std::mem::take(&mut self.pending_sizes);
        self.total += pending.len() as i64;
        for (id, path) in pending {
            self.progress(&path, false)?;
            let Some(item) = items.iter_mut().find(|i| i.id == id) else {
                continue;
            };
            match rules::allocated_size_detail(&path, None, Some(self.flag)) {
                Ok(measured) => {
                    item.size_bytes = Some(measured.bytes);
                    if measured.partial {
                        self.entry.partial.insert(id);
                    }
                }
                Err(error) if error.code == "E_SCAN_CANCELED" => return Err(error),
                Err(_) => {}
            }
        }
        Ok(())
    }
    pub(super) fn progress(&mut self, location: &Path, force: bool) -> Result<(), AppError> {
        check_cancel(self.flag)?;
        self.checked += 1;
        if force || self.last_progress.elapsed() >= Duration::from_millis(100) {
            self.last_progress = Instant::now();
            (self.engine.sink)(CoreEvent::UninstallScan(ScanProgress {
                request_id: self.entry.plan.plan_id.clone(),
                checked: self.checked.min(self.total),
                total: self.total,
                location: location.to_string_lossy().into_owned(),
            }));
        }
        Ok(())
    }
    pub(super) fn skip_location(&mut self, path: &Path, reason: SkippedLocationReason) {
        if !self
            .entry
            .plan
            .incomplete
            .iter()
            .any(|s| s.path == path.to_string_lossy())
        {
            self.entry.plan.incomplete.push(SkippedLocation {
                path: path.to_string_lossy().into_owned(),
                reason,
            });
        }
    }
    pub(super) fn add(
        &mut self,
        app: &CachedApp,
        items: &mut Vec<PlanItem>,
        candidate: Candidate,
    ) -> Result<(), AppError> {
        let Candidate {
            path,
            category,
            reason,
            confidence,
            action,
            note,
        } = candidate;
        check_cancel(self.flag)?;
        if !self
            .engine
            .roots
            .allowed(&path, reason, Some(Path::new(&app.app.path)))
            || !apple_safe(&path, app)
        {
            return Ok(());
        }
        if items
            .iter()
            .any(|i| i.path == path.to_string_lossy() && i.reason != MatchReason::BrewUninstall)
        {
            return Ok(());
        }
        let snapshot = match Snapshot::capture(&path) {
            Ok(s) => s,
            Err(_) => return Ok(()),
        };
        let mut shared: BTreeSet<_> = shared_with(&path, app, &self.all).into_iter().collect();
        for (program, id, name) in &self.references {
            if id != &app.app.id && program.starts_with(&path) {
                shared.insert(name.clone());
            }
        }
        let shared: Vec<_> = shared.into_iter().collect();
        let reason_key = format!("{reason:?}");
        let id = self.item_id(
            &app.app.id,
            &[&path.to_string_lossy(), &reason_key, action_key(&action)],
        );
        let kind = if matches!(&action, ItemAction::Forget { .. })
            || matches!(
                &action,
                ItemAction::Path {
                    bootout: Some(_),
                    ..
                }
            ) {
            PlanItemKind::Launchd
        } else {
            match snapshot.kind {
                2 => PlanItemKind::Symlink,
                1 => PlanItemKind::Dir,
                _ => PlanItemKind::File,
            }
        };
        let size_bytes = if snapshot.kind == 2 {
            Some(0)
        } else {
            match rules::allocated_size_detail(&path, Some(Duration::from_millis(200)), None) {
                Ok(measured) => {
                    if measured.partial {
                        self.entry.partial.insert(id.clone());
                    }
                    Some(measured.bytes)
                }
                Err(error) if error.message == "size_timeout" => {
                    self.pending_sizes.push((id.clone(), path.clone()));
                    None
                }
                Err(_) => None,
            }
        };
        self.entry.snapshots.insert(id.clone(), snapshot);
        self.entry.actions.insert(id.clone(), action);
        let selected = confidence != PlanItemConfidence::Low
            && shared.is_empty()
            && self.engine.settings.read().map_err(poison)?.zap_by_default;
        items.push(PlanItem {
            id,
            path: path.to_string_lossy().into_owned(),
            category,
            kind,
            count: 1,
            size_bytes,
            reason,
            confidence,
            selected,
            required: false,
            needs_admin: self.engine.roots.admin(&path),
            shared_with: shared,
            note,
        });
        Ok(())
    }
}
pub(super) fn scan_plan(
    engine: &Engine,
    request: &str,
    targets: Vec<CachedApp>,
    source_rules: BTreeMap<String, Value>,
    permissions: Permissions,
    flag: &AtomicBool,
) -> Result<PlanEntry, AppError> {
    let locations = engine.roots.locations();
    let time = now();
    let all: Vec<_> = engine
        .apps
        .lock()
        .map_err(poison)?
        .values()
        .cloned()
        .collect();
    let references = launchd_references(engine, &all);
    let mut b = Builder {
        engine,
        all,
        references,
        flag,
        last_progress: Instant::now(),
        checked: 0,
        total: (locations.len() * targets.len()) as i64,
        used: HashSet::new(),
        pending_sizes: vec![],
        entry: PlanEntry {
            plan: UninstallPlan {
                plan_id: request.to_owned(),
                apps: vec![],
                incomplete: vec![],
                created_at: time,
                expires_at: time + 600_000,
            },
            snapshots: BTreeMap::new(),
            actions: BTreeMap::new(),
            app_records: BTreeMap::new(),
            partial: BTreeSet::new(),
        },
    };
    for app in targets {
        check_cancel(flag)?;
        let companions: Vec<_> = b
            .all
            .iter()
            .filter(|other| {
                app.app.source == AppSource::Homebrew
                    && app.app.cask.is_some()
                    && other.app.cask == app.app.cask
                    && other.app.id != app.app.id
                    && Path::new(&other.app.path)
                        .extension()
                        .is_some_and(|e| e == "app")
            })
            .cloned()
            .collect();
        let blocked_reason = app
            .app
            .protected_reason
            .or_else(|| {
                companions
                    .iter()
                    .find_map(|other| other.app.protected_reason)
            })
            .map(|r| match r {
                ProtectedReason::System => AppPlanBlockedReason::System,
                ProtectedReason::SelfApp => AppPlanBlockedReason::SelfApp,
                ProtectedReason::Managed => AppPlanBlockedReason::Managed,
            })
            .or_else(|| {
                (app.app.source != AppSource::Homebrew
                    && permissions.app_management == PermissionState::Denied)
                    .then_some(AppPlanBlockedReason::AppManagement)
            });
        let brew = app.app.source == AppSource::Homebrew;
        let removal = if blocked_reason.is_some() {
            AppPlanRemoval::Blocked
        } else if brew {
            AppPlanRemoval::Brew
        } else {
            AppPlanRemoval::Trash
        };
        let mut items = Vec::new();
        let path = PathBuf::from(&app.app.path);
        let id = b.item_id(&app.app.id, &[&app.app.path, "app"]);
        if app.app.size_bytes.is_none() {
            b.pending_sizes.push((id.clone(), path.clone()));
        }
        if let Ok(snapshot) = Snapshot::capture(&path) {
            b.entry.snapshots.insert(id.clone(), snapshot);
        }
        b.entry.actions.insert(
            id.clone(),
            if brew {
                ItemAction::BrewStep
            } else {
                ItemAction::Path {
                    empty_only: false,
                    bootout: None,
                }
            },
        );
        items.push(PlanItem {
            id,
            path: app.app.path.clone(),
            category: ItemCategory::App,
            kind: PlanItemKind::Dir,
            count: 1,
            size_bytes: app.app.size_bytes,
            reason: MatchReason::AppBundle,
            confidence: PlanItemConfidence::High,
            selected: true,
            required: true,
            needs_admin: !brew && engine.roots.admin(&path),
            shared_with: vec![],
            note: None,
        });
        for other in &companions {
            let id = b.item_id(&app.app.id, &[&other.app.path, "companion"]);
            let path = Path::new(&other.app.path);
            if other.app.size_bytes.is_none() {
                b.pending_sizes.push((id.clone(), path.to_owned()));
            }
            if let Ok(snapshot) = Snapshot::capture(path) {
                b.entry.snapshots.insert(id.clone(), snapshot);
            }
            b.entry.actions.insert(id.clone(), ItemAction::BrewStep);
            b.entry
                .app_records
                .insert(other.app.id.clone(), other.clone());
            items.push(PlanItem {
                id,
                path: other.app.path.clone(),
                category: ItemCategory::App,
                kind: PlanItemKind::Dir,
                count: 1,
                size_bytes: other.app.size_bytes,
                reason: MatchReason::BrewUninstall,
                confidence: PlanItemConfidence::High,
                selected: true,
                required: true,
                needs_admin: false,
                shared_with: vec![],
                note: None,
            });
        }
        if removal != AppPlanRemoval::Blocked {
            // Recovery data belongs to the app, including other bundles removed
            // by the same cask. It must follow removal even when zap is disabled.
            for owner in std::iter::once(&app).chain(companions.iter()) {
                for backup in crate::updater::backup::existing(Path::new(&owner.app.path))? {
                    b.add(
                        owner,
                        &mut items,
                        Candidate {
                            path: backup.clone(),
                            category: ItemCategory::AppData,
                            reason: MatchReason::NameExact,
                            confidence: PlanItemConfidence::High,
                            action: ItemAction::Path {
                                empty_only: false,
                                bootout: None,
                            },
                            note: None,
                        },
                    )?;
                    if let Some(item) = items.iter_mut().find(|i| Path::new(&i.path) == backup) {
                        item.required = true;
                        item.selected = true;
                    }
                }
            }
            // Embedded SMAppService login items disappear with the bundle; display only, no extra command.
            let login_root = path.join("Contents/Library/LoginItems");
            if rules::parents_safe(&login_root.join("entry"))
                && let Ok(entries) = fs::read_dir(&login_root)
            {
                for entry in entries.flatten() {
                    if !entry
                        .file_type()
                        .is_ok_and(|t| t.is_dir() && !t.is_symlink())
                    {
                        continue;
                    }
                    let nested = identity::bundle(&entry.path());
                    if let Some(label) = nested.bundle_id
                        && app.identity.component_ids.contains(&label)
                    {
                        let id = b.item_id(
                            &app.app.id,
                            &[&entry.path().to_string_lossy(), "login", &label],
                        );
                        b.entry.actions.insert(id.clone(), ItemAction::LoginItem);
                        items.push(PlanItem {
                            id,
                            path: entry.path().to_string_lossy().into_owned(),
                            category: ItemCategory::Background,
                            kind: PlanItemKind::Launchd,
                            count: 1,
                            size_bytes: Some(0),
                            reason: MatchReason::AppGroup,
                            confidence: PlanItemConfidence::High,
                            selected: true,
                            required: true,
                            needs_admin: false,
                            shared_with: vec![],
                            note: Some(label),
                        });
                    }
                }
            }
            for (location, category) in &locations {
                b.progress(location, false)?;
                if permissions.full_disk_access != PermissionState::Granted
                    && [
                        engine.roots.home.join("Library/Containers"),
                        engine.roots.home.join("Library/Group Containers"),
                        engine.roots.home.join("Library/Application Scripts"),
                    ]
                    .contains(location)
                {
                    b.skip_location(location, SkippedLocationReason::FullDiskAccess);
                }
                let entries = match fs::read_dir(location) {
                    Ok(e) => e,
                    Err(e) if e.kind() == std::io::ErrorKind::NotFound => continue,
                    Err(_) => {
                        b.skip_location(location, SkippedLocationReason::Permission);
                        continue;
                    }
                };
                for entry in entries {
                    check_cancel(flag)?;
                    let entry = match entry {
                        Ok(e) => e,
                        Err(_) => {
                            b.skip_location(location, SkippedLocationReason::Permission);
                            continue;
                        }
                    };
                    let path = entry.path();
                    let name = entry.file_name().to_string_lossy().into_owned();
                    if category == &ItemCategory::Cli {
                        if entry.file_type().is_ok_and(|t| t.is_symlink())
                            && let Ok(target) = fs::read_link(&path)
                        {
                            let target = if target.is_absolute() {
                                target
                            } else {
                                location.join(target)
                            };
                            // lexical normalization solely for identity; execution always acts on the original link.
                            let target = lexical(&target);
                            if target.starts_with(&app.app.path) {
                                b.add(
                                    &app,
                                    &mut items,
                                    Candidate {
                                        path,
                                        category: *category,
                                        reason: MatchReason::Symlink,
                                        confidence: PlanItemConfidence::High,
                                        action: ItemAction::Path {
                                            empty_only: false,
                                            bootout: None,
                                        },
                                        note: Some(format!("→ {}", target.display())),
                                    },
                                )?;
                            }
                        }
                        continue;
                    }
                    if matches!(category, ItemCategory::Background) && name.ends_with(".plist") {
                        if let Some(service) = service(&path)
                            && service.matches(&app)
                        {
                            let system = path.starts_with(&engine.roots.system_library);
                            let label = service.label.clone();
                            b.add(
                                &app,
                                &mut items,
                                Candidate {
                                    path,
                                    category: *category,
                                    reason: MatchReason::Launchd,
                                    confidence: PlanItemConfidence::High,
                                    action: ItemAction::Path {
                                        empty_only: false,
                                        bootout: Some((label.clone(), system)),
                                    },
                                    note: Some(label),
                                },
                            )?;
                            for program in service.programs {
                                if program.starts_with(
                                    engine.roots.system_library.join("PrivilegedHelperTools"),
                                ) {
                                    b.add(
                                        &app,
                                        &mut items,
                                        Candidate {
                                            path: program,
                                            category: ItemCategory::Background,
                                            reason: MatchReason::Launchd,
                                            confidence: PlanItemConfidence::High,
                                            action: ItemAction::Path {
                                                empty_only: false,
                                                bootout: None,
                                            },
                                            note: None,
                                        },
                                    )?;
                                }
                            }
                        }
                        continue;
                    }
                    let ids = app
                        .app
                        .bundle_id
                        .iter()
                        .chain(app.identity.component_ids.iter());
                    // A framework-default ID (an unbranded Electron app) is shared by unrelated apps: only a suggestion.
                    let exact = ids.clone().any(|id| {
                        !identity::generic_bundle_id(id) && rules::bundle_matches(&name, id)
                    });
                    let generic = !exact
                        && ids.clone().any(|id| {
                            identity::generic_bundle_id(id) && rules::bundle_matches(&name, id)
                        });
                    let group = app
                        .identity
                        .groups
                        .iter()
                        .any(|id| rules::bundle_matches(&name, id));
                    let matched = if exact {
                        Some((MatchReason::BundleId, PlanItemConfidence::High))
                    } else if group {
                        Some((MatchReason::AppGroup, PlanItemConfidence::High))
                    } else if generic {
                        Some((MatchReason::BundleId, PlanItemConfidence::Low))
                    } else if *category == ItemCategory::Logs
                        && location.ends_with("DiagnosticReports")
                        && app
                            .identity
                            .executables
                            .iter()
                            .any(|e| name.starts_with(&format!("{e}_")))
                    {
                        Some((MatchReason::NameExact, PlanItemConfidence::Medium))
                    } else if *category == ItemCategory::Plugins {
                        None
                    } else if *category == ItemCategory::AppData
                        && app.app.team_id.is_some()
                        && app.identity.vendor.as_ref().is_some_and(|vendor| {
                            let vendor = vendor.split(" (").next().unwrap_or(vendor);
                            rules::normalize(vendor) == rules::normalize(&name)
                                && rules::meaningful(&rules::normalize(&name))
                        })
                        && engine.probe.identity(&path).team_id == app.app.team_id
                    {
                        Some((MatchReason::TeamId, PlanItemConfidence::Medium))
                    } else {
                        rules::name_match(&name, &app.app.name)
                    };
                    if let Some((reason, confidence)) = matched {
                        b.add(
                            &app,
                            &mut items,
                            Candidate {
                                path,
                                category: *category,
                                reason,
                                confidence,
                                action: ItemAction::Path {
                                    empty_only: false,
                                    bootout: None,
                                },
                                note: None,
                            },
                        )?;
                    }
                }
            }
            if let Some(raw) = source_rules.get(&app.app.id) {
                for declaration in declarations(raw, "zap") {
                    for key in ["trash", "delete", "rmdir"] {
                        for pattern in strings(declaration.get(key)) {
                            check_cancel(flag)?;
                            for path in rules::expand(
                                &pattern,
                                &engine.roots.home,
                                app.app.team_id.as_deref(),
                            )? {
                                let category = engine.roots.category(&path);
                                b.add(
                                    &app,
                                    &mut items,
                                    Candidate {
                                        path,
                                        category,
                                        reason: MatchReason::ZapRule,
                                        confidence: PlanItemConfidence::High,
                                        action: ItemAction::Path {
                                            empty_only: key == "rmdir",
                                            bootout: None,
                                        },
                                        note: None,
                                    },
                                )?;
                            }
                        }
                    }
                }
                // Never execute script/signal. Launchctl/pkgutil declarations become typed items, never executable source text.
                for declaration in declarations(raw, "zap") {
                    for label in strings(declaration.get("launchctl")) {
                        if identity::valid_identifier(&label) {
                            for root in [
                                engine.roots.home.join("Library/LaunchAgents"),
                                engine.roots.system_library.join("LaunchAgents"),
                                engine.roots.system_library.join("LaunchDaemons"),
                            ] {
                                let file = root.join(format!("{label}.plist"));
                                let system = root.starts_with(&engine.roots.system_library);
                                b.add(
                                    &app,
                                    &mut items,
                                    Candidate {
                                        path: file,
                                        category: ItemCategory::Background,
                                        reason: MatchReason::ZapRule,
                                        confidence: PlanItemConfidence::High,
                                        action: ItemAction::Path {
                                            empty_only: false,
                                            bootout: Some((label.clone(), system)),
                                        },
                                        note: Some(label.clone()),
                                    },
                                )?;
                            }
                        }
                    }
                }
                if brew {
                    for declaration in declarations(raw, "uninstall") {
                        for key in ["launchctl", "pkgutil", "quit", "delete"] {
                            for detail in strings(declaration.get(key)) {
                                let id = b.item_id(&app.app.id, &["brew", key, &detail]);
                                b.entry.actions.insert(id.clone(), ItemAction::BrewStep);
                                items.push(PlanItem {
                                    id,
                                    path: app.app.path.clone(),
                                    category: ItemCategory::Background,
                                    kind: PlanItemKind::Launchd,
                                    count: 1,
                                    size_bytes: Some(0),
                                    reason: MatchReason::BrewUninstall,
                                    confidence: PlanItemConfidence::High,
                                    selected: true,
                                    required: true,
                                    needs_admin: matches!(key, "launchctl" | "pkgutil"),
                                    shared_with: vec![],
                                    note: Some(detail),
                                });
                            }
                        }
                    }
                }
            }
            let mut receipts = app.receipts.clone();
            if let Some(raw) = source_rules.get(&app.app.id) {
                for declaration in declarations(raw, "zap") {
                    for pattern in strings(declaration.get("pkgutil")) {
                        if let Ok(pattern) = glob::Pattern::new(&pattern)
                            && let Ok(entries) = fs::read_dir(&engine.roots.receipts)
                        {
                            for e in entries.flatten() {
                                if e.path().extension().is_some_and(|ext| ext == "plist")
                                    && let Some(id) = e.path().file_stem().and_then(|s| s.to_str())
                                    && identity::valid_identifier(id)
                                    && pattern.matches(id)
                                    && !receipts.iter().any(|r| r.id == id)
                                {
                                    receipts.push(identity::Receipt {
                                        id: id.to_owned(),
                                        files: vec![],
                                    });
                                }
                            }
                        }
                    }
                }
            }
            for receipt in &receipts {
                for file in &receipt.files {
                    if !file.starts_with(&app.app.path) {
                        b.add(
                            &app,
                            &mut items,
                            Candidate {
                                path: file.clone(),
                                category: engine.roots.category(file),
                                reason: MatchReason::PkgReceipt,
                                confidence: PlanItemConfidence::High,
                                action: ItemAction::Path {
                                    empty_only: file.is_dir(),
                                    bootout: None,
                                },
                                note: None,
                            },
                        )?;
                    }
                }
                let mut representative = None;
                for ext in ["bom", "plist"] {
                    let file = engine.roots.receipts.join(format!("{}.{ext}", receipt.id));
                    b.add(
                        &app,
                        &mut items,
                        Candidate {
                            path: file.clone(),
                            category: ItemCategory::Receipts,
                            reason: MatchReason::PkgReceipt,
                            confidence: PlanItemConfidence::High,
                            action: ItemAction::Path {
                                empty_only: false,
                                bootout: None,
                            },
                            note: Some(receipt.id.clone()),
                        },
                    )?;
                    if items.iter().any(|i| {
                        i.category == ItemCategory::Receipts
                            && i.kind != PlanItemKind::Launchd
                            && i.path == file.to_string_lossy()
                    }) {
                        representative = Some(file);
                    }
                }
                if let Some(path) = representative
                    && let Ok(snapshot) = Snapshot::capture(&path)
                {
                    let id = b.item_id(
                        &app.app.id,
                        &[&path.to_string_lossy(), "forget", &receipt.id],
                    );
                    let shared = shared_with(&path, &app, &b.all);
                    b.entry.snapshots.insert(id.clone(), snapshot);
                    b.entry.actions.insert(
                        id.clone(),
                        ItemAction::Forget {
                            package: receipt.id.clone(),
                        },
                    );
                    items.push(PlanItem {
                        id,
                        path: path.to_string_lossy().into_owned(),
                        category: ItemCategory::Receipts,
                        kind: PlanItemKind::Launchd,
                        count: 1,
                        size_bytes: Some(0),
                        reason: MatchReason::PkgReceipt,
                        confidence: PlanItemConfidence::High,
                        selected: shared.is_empty()
                            && engine.settings.read().map_err(poison)?.zap_by_default,
                        required: false,
                        needs_admin: true,
                        shared_with: shared,
                        note: Some(receipt.id.clone()),
                    });
                }
            }
        }
        b.finish_sizes(&mut items)?;
        let personal_data = items
            .iter()
            .any(|i| i.category == ItemCategory::AppData)
            .then(|| personal_data(&app));
        b.entry.plan.apps.push(AppPlan {
            app_id: app.app.id.clone(),
            name: app.app.name.clone(),
            source: app.app.source,
            running: app.app.running || companions.iter().any(|other| other.app.running),
            removal,
            brew_command: app
                .app
                .cask
                .as_ref()
                .map(|t| format!("brew uninstall --cask {t}")),
            blocked_reason,
            uninstaller: None,
            personal_data,
            items,
        });
        b.entry.app_records.insert(app.app.id.clone(), app);
    }
    // Final progress respects the same 100ms gate; return value itself signals completion.
    b.progress(Path::new(""), false)?;
    Ok(b.entry)
}
/// What the app's data most likely holds, by bundle ID. Words inside names were too loose: "word" matched
/// 1Password and "edge" any knowledge base app. An entry ending in "." covers the vendor's whole family.
const PERSONAL_DATA: &[(&str, &[&str])] = &[
    (
        "chat_history",
        &[
            "com.tencent.xinwechat",
            "com.tencent.qq",
            "com.tencent.weworkmac",
            "ru.keepcoder.telegram",
            "org.telegram.desktop",
            "net.whatsapp.whatsapp",
            "desktop.whatsapp",
            "org.whispersystems.signal-desktop",
            "com.hnc.discord",
            "com.tinyspeck.slackmacgap",
            "com.microsoft.teams",
            "com.microsoft.teams2",
            "com.alibaba.dingtalkmac",
            "com.electron.lark",
            "com.bytedance.lark",
            "jp.naver.line.mac",
            "com.viber.osx",
        ],
    ),
    (
        "vm_disk",
        &[
            "com.docker.docker",
            "dev.kdrag0n.macvirt",
            "dev.orbstack.",
            "com.parallels.",
            "com.vmware.fusion",
            "com.utmapp.utm",
            "org.virtualbox.app.virtualbox",
            "io.podman-desktop.podmandesktop",
        ],
    ),
    (
        "browser_profile",
        &[
            "com.google.chrome",
            "org.mozilla.firefox",
            "org.mozilla.firefoxdeveloperedition",
            "com.brave.browser",
            "com.vivaldi.vivaldi",
            "com.microsoft.edgemac",
            "company.thebrowser.browser",
            "com.operasoftware.opera",
            "org.chromium.chromium",
            "app.zen-browser.zen",
        ],
    ),
    (
        "media",
        &[
            "com.apple.finalcut",
            "com.apple.logic10",
            "com.apple.imovieapp",
            "com.apple.garageband10",
            "com.blackmagic-design.davinciresolve",
            "com.adobe.premierepro.",
            "com.adobe.lightroomcc",
        ],
    ),
    (
        "documents",
        &[
            "com.microsoft.word",
            "com.microsoft.excel",
            "com.microsoft.powerpoint",
            "com.apple.iwork.",
            "com.microsoft.vscode",
            "com.microsoft.vscodeinsiders",
            "com.apple.dt.xcode",
            "com.jetbrains.",
            "com.google.android.studio",
            "com.sublimetext.",
            "md.obsidian",
            "notion.id",
            "com.kingsoft.wpsoffice.mac",
            "com.adobe.photoshop",
            "com.adobe.illustrator",
            "com.figma.desktop",
        ],
    ),
];
pub fn personal_data(app: &CachedApp) -> String {
    let id = app
        .app
        .bundle_id
        .as_deref()
        .unwrap_or_default()
        .to_ascii_lowercase();
    PERSONAL_DATA
        .iter()
        .find(|(_, ids)| {
            ids.iter().any(|known| {
                if known.ends_with('.') {
                    id.starts_with(known)
                } else {
                    id == *known
                        || id
                            .strip_prefix(known)
                            .is_some_and(|rest| rest.starts_with('.'))
                }
            })
        })
        .map_or("other", |(kind, _)| kind)
        .into()
}
fn apple_safe(path: &Path, app: &CachedApp) -> bool {
    let name = path.file_name().unwrap_or_default().to_string_lossy();
    if !name.starts_with("com.apple.") {
        return true;
    }
    app.app.bundle_id.as_ref().is_some_and(|id| {
        name == id.as_str()
            || [".plist", ".savedState", ".binarycookies"]
                .iter()
                .any(|s| name.strip_suffix(s) == Some(id.as_str()))
    })
}
pub fn shared_with(path: &Path, target: &CachedApp, all: &[CachedApp]) -> Vec<String> {
    let name = path.file_name().unwrap_or_default().to_string_lossy();
    let text = path.to_string_lossy().to_lowercase();
    let mut result = BTreeSet::new();
    for other in all.iter().filter(|a| a.app.id != target.app.id) {
        let id = other.app.bundle_id.as_deref().unwrap_or_default();
        let entitlement = other
            .app
            .bundle_id
            .iter()
            .chain(other.identity.groups.iter())
            .chain(other.identity.component_ids.iter())
            .any(|g| rules::bundle_matches(&name, g));
        let receipt = other.receipts.iter().any(|r| {
            r.files.iter().any(|f| f == path || f.starts_with(path))
                || path.file_stem().is_some_and(|p| p == r.id.as_str())
        });
        let known = (name == ".docker"
            && (id.starts_with("dev.orbstack") || id.starts_with("com.docker")))
            || ((text.contains("microsoft autoupdate")
                || text.contains("com.microsoft.autoupdate"))
                && id.starts_with("com.microsoft."))
            || ((text.contains("google/keystone") || text.contains("com.google.keystone"))
                && id.starts_with("com.google."))
            || ((text.contains("adobe/coresync") || text.contains("adobe common"))
                && id.starts_with("com.adobe."));
        let service_ref = service(path).is_some_and(|s| s.matches(other));
        if entitlement || receipt || known || service_ref {
            result.insert(other.app.name.clone());
        }
    }
    result.into_iter().collect()
}
#[derive(Clone)]
pub struct Service {
    pub label: String,
    pub programs: Vec<PathBuf>,
}
impl Service {
    pub fn matches(&self, app: &CachedApp) -> bool {
        self.programs.iter().any(|p| p.starts_with(&app.app.path))
            || app
                .app
                .bundle_id
                .iter()
                .chain(app.identity.component_ids.iter())
                .any(|id| rules::bundle_matches(&self.label, id))
    }
}
pub fn service(path: &Path) -> Option<Service> {
    let m = fs::symlink_metadata(path).ok()?;
    if !m.is_file() || m.is_symlink() || m.len() > 4 * 1024 * 1024 || !rules::parents_safe(path) {
        return None;
    }
    let dict = plist::Value::from_file(path).ok()?.into_dictionary()?;
    let label = dict.get("Label")?.as_string()?.to_owned();
    if !identity::valid_identifier(&label) {
        return None;
    }
    let mut programs = vec![];
    if let Some(p) = dict.get("Program").and_then(plist::Value::as_string) {
        programs.push(PathBuf::from(p));
    }
    if let Some(args) = dict
        .get("ProgramArguments")
        .and_then(plist::Value::as_array)
    {
        programs.extend(
            args.iter()
                .filter_map(plist::Value::as_string)
                .filter(|p| Path::new(p).is_absolute())
                .map(PathBuf::from),
        );
    }
    Some(Service { label, programs })
}
pub(super) fn launchd_references(
    engine: &Engine,
    all: &[CachedApp],
) -> Vec<(PathBuf, String, String)> {
    let mut result = vec![];
    for (location, category) in engine.roots.locations() {
        if category != ItemCategory::Background {
            continue;
        }
        if let Ok(entries) = fs::read_dir(location) {
            for entry in entries.flatten() {
                if let Some(s) = service(&entry.path()) {
                    for app in all.iter().filter(|app| s.matches(app)) {
                        for program in &s.programs {
                            result.push((
                                program.clone(),
                                app.app.id.clone(),
                                app.app.name.clone(),
                            ));
                        }
                    }
                }
            }
        }
    }
    result
}
fn lexical(path: &Path) -> PathBuf {
    let mut result = PathBuf::new();
    for c in path.components() {
        match c {
            std::path::Component::ParentDir => {
                result.pop();
            }
            std::path::Component::CurDir => {}
            _ => result.push(c.as_os_str()),
        }
    }
    result
}
