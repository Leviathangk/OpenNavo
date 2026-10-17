use crate::AppError;
use std::path::Path;
pub fn reveal(path: &Path) -> Result<(), AppError> {
    #[cfg(target_os = "macos")]
    {
        use objc2_app_kit::NSWorkspace;
        use objc2_foundation::{NSArray, NSString, NSURL};
        objc2::rc::autoreleasepool(|_| {
            let url = NSURL::fileURLWithPath(&NSString::from_str(&path.to_string_lossy()));
            NSWorkspace::sharedWorkspace()
                .activateFileViewerSelectingURLs(&NSArray::from_slice(&[&*url]));
        });
        Ok(())
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = path;
        Err(AppError::new("E_UNKNOWN", "unsupported_platform"))
    }
}

use super::{
    Engine, ItemAction, PlanEntry, identity, manifest, now, poison,
    rules::{self, Snapshot},
};
use crate::{model::*, queue::TaskQueue};
use serde::{Deserialize, Serialize};
use std::{
    collections::{BTreeSet, HashMap},
    fs,
    io::{Read, Write},
    path::PathBuf,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};

pub trait FileActions: Send + Sync {
    fn trash(&self, path: &Path, trash_root: &Path) -> Result<PathBuf, AppError>;
    fn bootout(&self, label: &str, system: bool, path: &str) -> Result<(), AppError> {
        bootout(label, system, path)
    }
}
pub struct NativeFiles;
impl FileActions for NativeFiles {
    fn trash(&self, path: &Path, _: &Path) -> Result<PathBuf, AppError> {
        #[cfg(target_os = "macos")]
        {
            use objc2_foundation::{NSFileManager, NSString, NSURL};
            objc2::rc::autoreleasepool(|_| {
                let mut destination = None;
                let url = NSURL::fileURLWithPath(&NSString::from_str(&path.to_string_lossy()));
                NSFileManager::defaultManager()
                    .trashItemAtURL_resultingItemURL_error(&url, Some(&mut destination))
                    .map_err(|error| trash_error(path, &error))?;
                destination
                    .and_then(|u| u.path())
                    .map(|p| PathBuf::from(p.to_string()))
                    .ok_or_else(|| AppError::new("E_UNKNOWN", "trash_destination"))
            })
        }
        #[cfg(not(target_os = "macos"))]
        {
            let _ = path;
            Err(AppError::new("E_UNKNOWN", "unsupported_platform"))
        }
    }
}

/// Name the real cause of a failed move to the Trash. Calling every failure "App Management" sent users to a
/// permission they already had and, once granted, to a pointless relaunch.
#[cfg(target_os = "macos")]
fn trash_error(path: &Path, error: &objc2_foundation::NSError) -> AppError {
    use objc2_foundation::{NSError, NSUnderlyingErrorKey};
    let posix = |error: &NSError| {
        (error.domain().to_string() == "NSPOSIXErrorDomain").then(|| error.code())
    };
    let underlying = error
        .userInfo()
        .objectForKey(unsafe { NSUnderlyingErrorKey })
        .and_then(|value| value.downcast_ref::<NSError>().and_then(posix));
    let errno = posix(error).or(underlying);
    let cocoa = (error.domain().to_string() == "NSCocoaErrorDomain").then(|| error.code());
    let bundle = path.extension().is_some_and(|e| e == "app");
    // NSFileNoSuchFileError 4, NSFileWriteNoPermissionError 513, NSFileWriteVolumeReadOnlyError 642.
    let (code, message) = match (errno, cocoa) {
        (Some(1), _) if bundle => ("E_APP_MANAGEMENT", "trash_app_management"),
        (Some(1), _) => ("E_FULL_DISK_ACCESS", "trash_privacy"),
        (Some(2), _) | (_, Some(4)) => ("E_NOT_FOUND", "trash_missing"),
        (None, Some(513)) if bundle => ("E_APP_MANAGEMENT", "trash_app_management"),
        (_, Some(642)) | (Some(30), _) => ("E_TRASH_FAILED", "read_only_volume"),
        _ => ("E_TRASH_FAILED", "trash_failed"),
    };
    AppError::new(code, message).with_detail(error.localizedDescription().to_string())
}

impl Engine {
    /// Injection is Rust-only; IPC has no path or filesystem-service argument.
    pub fn set_file_actions(&self, files: Arc<dyn FileActions>) -> Result<(), AppError> {
        *self.file_actions.lock().map_err(poison)? = files;
        Ok(())
    }
    pub fn set_admin_actions(&self, actions: Arc<dyn AdminActions>) -> Result<(), AppError> {
        *self.admin_actions.lock().map_err(poison)? = Some(actions);
        Ok(())
    }
    pub fn task_plan(
        &self,
        op: TaskOp,
        target: Option<&TaskTarget>,
        options: &TaskOptions,
    ) -> Result<(PlanEntry, AppPlan), AppError> {
        if !matches!(op, TaskOp::Uninstall | TaskOp::RemoveLeftovers)
            || options.zap
            || options.force
            || options.adopt
            || options.greedy
            || options.reopen
        {
            return Err(AppError::new("E_INVALID_ARG", "uninstall_options"));
        }
        let plan_id = options
            .plan_id
            .as_deref()
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "plan_id"))?;
        let entry = self.cached_plan(plan_id, now())?;
        let target = target.ok_or_else(|| AppError::new("E_INVALID_ARG", "target"))?;
        let app = entry
            .plan
            .apps
            .iter()
            .find(|a| {
                let record = entry.app_records.get(&a.app_id);
                if target.kind == TaskKind::Cask {
                    record.is_some_and(|r| r.app.cask.as_deref() == Some(&target.token))
                        && options.item_ids.as_ref().is_some_and(|ids| {
                            ids.iter().all(|id| a.items.iter().any(|i| i.id == *id))
                        })
                } else {
                    target.kind == TaskKind::App && a.app_id == target.token
                }
            })
            .cloned()
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "plan_target"))?;
        if op == TaskOp::RemoveLeftovers
            || !app.items.iter().any(|i| i.reason == MatchReason::AppBundle)
        {
            return Err(AppError::new("E_INVALID_ARG", "plan_operation"));
        }
        if app.removal == AppPlanRemoval::Blocked {
            return Err(AppError::new(
                if app.blocked_reason == Some(AppPlanBlockedReason::AppManagement) {
                    "E_APP_MANAGEMENT"
                } else {
                    "E_PROTECTED"
                },
                "uninstall_blocked",
            ));
        }
        let ids = options
            .item_ids
            .as_ref()
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "item_ids"))?;
        if ids.iter().collect::<BTreeSet<_>>().len() != ids.len()
            || ids.iter().any(|id| !app.items.iter().any(|i| i.id == *id))
            || options.delete_mode.is_none()
        {
            return Err(AppError::new("E_INVALID_ARG", "item_ids"));
        }
        Ok((entry, app))
    }
    pub fn execute_choices(
        self: &Arc<Self>,
        queue: &TaskQueue,
        plan_id: &str,
        choices: Vec<UninstallChoice>,
        mode: DeleteMode,
    ) -> Result<Vec<Task>, AppError> {
        let _guard = self.operation_lock.lock().map_err(poison)?;
        if choices.is_empty()
            || choices.len() > 100
            || choices
                .iter()
                .map(|c| &c.app_id)
                .collect::<BTreeSet<_>>()
                .len()
                != choices.len()
        {
            return Err(AppError::new("E_INVALID_ARG", "choices"));
        }
        let entry = self.cached_plan(plan_id, now())?;
        let mut requests = Vec::new();
        let mut casks = BTreeSet::new();
        for choice in choices {
            if !entry.plan.apps.iter().any(|a| a.app_id == choice.app_id) {
                return Err(AppError::new("E_INVALID_ARG", "app_id"));
            }
            let record = entry
                .app_records
                .get(&choice.app_id)
                .ok_or_else(|| AppError::new("E_INVALID_ARG", "app_id"))?;
            let target = TaskTarget {
                kind: if record.app.source == AppSource::Homebrew {
                    TaskKind::Cask
                } else {
                    TaskKind::App
                },
                token: record.app.cask.clone().unwrap_or(choice.app_id),
            };
            if target.kind == TaskKind::Cask && !casks.insert(target.token.clone()) {
                return Err(AppError::new("E_INVALID_ARG", "duplicate_cask"));
            }
            let options = TaskOptions {
                plan_id: Some(plan_id.to_owned()),
                item_ids: Some(choice.item_ids),
                delete_mode: Some(mode),
                quit_running: choice.quit_running,
                ..Default::default()
            };
            self.task_plan(TaskOp::Uninstall, Some(&target), &options)?;
            requests.push((target, options));
        }
        requests
            .into_iter()
            .map(|(target, options)| {
                queue.enqueue(
                    TaskOp::Uninstall,
                    Some(target),
                    options,
                    TaskTrigger::Manual,
                )
            })
            .collect()
    }
    pub(crate) fn reserve(&self, plan: &str, app: &str) -> Result<(), AppError> {
        if !self
            .reserved
            .lock()
            .map_err(poison)?
            .insert((plan.to_owned(), app.to_owned()))
        {
            return Err(AppError::new("E_PLAN_EXPIRED", "uninstall_plan_consumed"));
        }
        Ok(())
    }
    pub(crate) fn release(&self, plan: &str, app: &str) {
        if let Ok(mut reserved) = self.reserved.lock() {
            reserved.remove(&(plan.to_owned(), app.to_owned()));
        }
    }
}

pub fn remove(path: &Path, empty_only: bool) -> Result<(), AppError> {
    if !rules::parents_safe(path) {
        return Err(AppError::new("E_PROTECTED", "symlink_parent"));
    }
    let meta = fs::symlink_metadata(path)?;
    if meta.is_symlink() || meta.is_file() {
        fs::remove_file(path)?;
    } else if meta.is_dir() {
        if empty_only {
            fs::remove_dir(path)?;
        } else {
            fs::remove_dir_all(path)?;
        }
    } else {
        return Err(AppError::new("E_PROTECTED", "special_file"));
    }
    Ok(())
}
/// Atomic no-overwrite rename on macOS, including symlinks themselves.
pub fn move_exclusive(from: &Path, to: &Path) -> Result<(), AppError> {
    if !rules::parents_safe(from) || !rules::parents_safe(to) {
        return Err(AppError::new("E_PROTECTED", "move_parent"));
    }
    #[cfg(target_os = "macos")]
    {
        use std::{ffi::CString, os::unix::ffi::OsStrExt};
        let from = CString::new(from.as_os_str().as_bytes())
            .map_err(|_| AppError::new("E_PROTECTED", "move_path"))?;
        let to = CString::new(to.as_os_str().as_bytes())
            .map_err(|_| AppError::new("E_PROTECTED", "move_path"))?;
        if unsafe { nix::libc::renamex_np(from.as_ptr(), to.as_ptr(), nix::libc::RENAME_EXCL) } != 0
        {
            return Err(std::io::Error::last_os_error().into());
        }
        Ok(())
    }
    #[cfg(not(target_os = "macos"))]
    {
        if fs::symlink_metadata(to).is_ok() {
            return Err(AppError::new("E_PROTECTED", "move_conflict"));
        }
        fs::rename(from, to)?;
        Ok(())
    }
}

pub async fn run(
    queue: Arc<TaskQueue>,
    task: &Task,
    cancel: Arc<AtomicBool>,
    notify: Arc<tokio::sync::Notify>,
) -> Result<crate::brew::pty::Outcome, AppError> {
    let engine = queue.uninstall_engine.clone();
    let (entry, app) = engine.task_plan(task.op, task.target.as_ref(), &task.options)?;
    let record = entry
        .app_records
        .get(&app.app_id)
        .ok_or_else(|| AppError::new("E_PLAN_EXPIRED", "plan_app_missing"))?;
    let package_records: Vec<_> = entry
        .app_records
        .values()
        .filter(|r| {
            app.items
                .iter()
                .any(|item| item.category == ItemCategory::App && item.path == r.app.path)
        })
        .collect();
    let package_paths: Vec<PathBuf> = package_records
        .iter()
        .map(|r| r.app.path.clone().into())
        .collect();
    // Processes are matched by path first. A bundle ID counts only when it names one installed app and is not a
    // framework default; otherwise uninstalling Xcode-beta quit the running Xcode.
    let (unique, others) = {
        let apps = engine.apps.lock().map_err(poison)?;
        let unique = super::apps::unique_bundle_ids(
            apps.values()
                .flat_map(|r| {
                    r.app
                        .bundle_id
                        .iter()
                        .chain(r.identity.component_ids.iter())
                })
                .map(String::as_str),
        );
        let others: Vec<PathBuf> = apps
            .values()
            .map(|r| PathBuf::from(&r.app.path))
            .filter(|path| !package_paths.contains(path))
            .collect();
        (unique, others)
    };
    let identity = Arc::new(crate::running_apps::Identity {
        paths: if task.op == TaskOp::RemoveLeftovers {
            vec![]
        } else {
            package_paths.clone()
        },
        bundle_ids: package_records
            .iter()
            .flat_map(|r| {
                r.app
                    .bundle_id
                    .iter()
                    .chain(r.identity.component_ids.iter())
            })
            .filter(|id| unique.contains(*id) && !identity::generic_bundle_id(id))
            .cloned()
            .collect(),
        exclude: others,
        reveal_after: Some(Duration::from_secs(2)),
    });
    queue.uninstall_progress(&task.id, TaskPhase::Preparing, 0, 0.0, None)?;
    if task.op != TaskOp::RemoveLeftovers {
        // An app with unsaved work shows a save sheet; it is brought forward and given time to answer.
        crate::running_apps::prepare(
            identity,
            queue.app_control.clone(),
            task.options.quit_running,
            cancel.clone(),
            notify.clone(),
            Duration::from_secs(30),
        )
        .await
        .map_err(|e| {
            if matches!(e.code.as_str(), "E_APP_RUNNING" | "E_APP_QUIT_FAILED") {
                AppError::new("E_APP_RUNNING", "uninstall_app_running")
            } else {
                e
            }
        })?;
    }
    let mut items: Vec<_> = app
        .items
        .iter()
        .filter(|i| {
            i.required
                || task
                    .options
                    .item_ids
                    .as_ref()
                    .is_some_and(|ids| ids.contains(&i.id))
        })
        .cloned()
        .collect();
    // Keep only outermost recursive paths; a receipt rmdir remains a separate empty-only action.
    let selected_ids: BTreeSet<_> = items.iter().map(|i| i.id.clone()).collect();
    items.retain(|item| {
        if let Some(ItemAction::Forget { package }) = entry.actions.get(&item.id) {
            app.items
                .iter()
                .filter(|i| {
                    i.category == ItemCategory::Receipts
                        && i.kind != PlanItemKind::Launchd
                        && Path::new(&i.path)
                            .file_stem()
                            .is_some_and(|s| s == package.as_str())
                })
                .all(|i| selected_ids.contains(&i.id))
        } else {
            true
        }
    });
    let recursive: Vec<PathBuf> = items
        .iter()
        .filter(|i| {
            matches!(
                entry.actions.get(&i.id),
                Some(ItemAction::Path {
                    empty_only: false,
                    ..
                })
            ) && i.kind == PlanItemKind::Dir
        })
        .map(|i| i.path.clone().into())
        .collect();
    items.retain(|item| {
        !recursive
            .iter()
            .any(|p| p != Path::new(&item.path) && Path::new(&item.path).starts_with(p))
    });
    let mut outcome = UninstallOutcome {
        freed_bytes: 0,
        trashed_bytes: 0,
        trashed: 0,
        deleted: 0,
        skipped: vec![],
        restorable: false,
    };
    let mode = task
        .options
        .delete_mode
        .ok_or_else(|| AppError::new("E_INVALID_ARG", "delete_mode"))?;
    if task.op == TaskOp::Uninstall {
        let body = app
            .items
            .iter()
            .find(|i| i.reason == MatchReason::AppBundle)
            .ok_or_else(|| AppError::new("E_PLAN_EXPIRED", "app_item"))?;
        if !entry
            .snapshots
            .get(&body.id)
            .is_some_and(|s| s.still(Path::new(&body.path)))
        {
            return Err(AppError::new("E_PLAN_EXPIRED", "app_changed"));
        }
        if record.app.bundle_id.is_some()
            && identity::bundle(Path::new(&record.app.path)).bundle_id != record.app.bundle_id
        {
            return Err(AppError::new("E_PLAN_EXPIRED", "app_identity_changed"));
        }
        for companion in app
            .items
            .iter()
            .filter(|i| i.category == ItemCategory::App && i.reason == MatchReason::BrewUninstall)
        {
            if !entry
                .snapshots
                .get(&companion.id)
                .is_some_and(|s| s.still(Path::new(&companion.path)))
            {
                return Err(AppError::new("E_PLAN_EXPIRED", "app_changed"));
            }
            if let Some(other) = package_records
                .iter()
                .find(|r| r.app.path == companion.path)
                && other.app.bundle_id.is_some()
                && identity::bundle(Path::new(&companion.path)).bundle_id != other.app.bundle_id
            {
                return Err(AppError::new("E_PLAN_EXPIRED", "app_identity_changed"));
            }
        }
    }
    let mut brew_outcome = None;
    if app.removal == AppPlanRemoval::Brew {
        let body = items
            .iter()
            .find(|i| i.reason == MatchReason::AppBundle)
            .ok_or_else(|| AppError::new("E_PLAN_EXPIRED", "app_item"))?;
        if !entry
            .snapshots
            .get(&body.id)
            .is_some_and(|s| s.still(Path::new(&body.path)))
        {
            return Err(AppError::new("E_PLAN_EXPIRED", "app_changed"));
        }
        // Homebrew deletes an app for good and skips one that is already gone. Moving the bundles to the Trash
        // first keeps Undo honest: the app comes back, only Homebrew's record of it does not.
        let mut trashed = BTreeSet::new();
        if mode == DeleteMode::Trash {
            let root = engine.trash_root()?;
            let context = ExecutionContext {
                engine: &engine,
                queue: &queue,
                task,
                entry: &entry,
                app: &app,
                mode,
            };
            let bundles = items.iter().filter(|i| {
                i.category == ItemCategory::App
                    && Path::new(&i.path).extension().is_some_and(|e| e == "app")
                    && !engine.roots.admin(Path::new(&i.path))
            });
            for item in bundles {
                let Some(snapshot) = entry.snapshots.get(&item.id) else {
                    continue;
                };
                let action = ItemAction::Path {
                    empty_only: false,
                    bootout: None,
                };
                perform_user(&context, item, &action, snapshot, &root, 10.0, &mut outcome)?;
                if fs::symlink_metadata(&item.path).is_err() {
                    trashed.insert(item.id.clone());
                }
            }
        }
        queue.uninstall_progress(
            &task.id,
            TaskPhase::Uninstalling,
            1,
            10.0,
            Some(format!(
                "==> Homebrew: brew uninstall --cask {}",
                task.target
                    .as_ref()
                    .map(|t| t.token.as_str())
                    .unwrap_or_default()
            )),
        )?;
        let settings = queue.settings()?;
        let brew = crate::brew::locate::locate(&settings).await?;
        let request = crate::brew::pty::Execution {
            executable: brew.path.into(),
            args: crate::brew::args::write_args(
                TaskOp::Uninstall,
                task.target.as_ref(),
                &TaskOptions::default(),
            )?,
            env: crate::brew::env::construct(&settings, &brew.prefix, &queue.askpass),
            log_path: task
                .log_path
                .as_ref()
                .ok_or_else(|| AppError::new("E_UNKNOWN", "task_log"))?
                .into(),
            op: TaskOp::Uninstall,
            cancel: cancel.clone(),
            grace: Duration::from_secs(5),
            cache_dir: None,
            download_total: None,
        };
        let q = queue.clone();
        let id = task.id.clone();
        let result = crate::blocking(move || {
            crate::brew::pty::execute(request, move |lines, _| {
                for line in lines {
                    let _ = q.uninstall_progress(
                        &id,
                        TaskPhase::Uninstalling,
                        1,
                        20.0,
                        Some(line.clone()),
                    );
                }
            })
        })
        .await?;
        if result.code != 0 || result.canceled {
            // Bundles already in the Trash stay restorable.
            if !trashed.is_empty() {
                queue.set_uninstall_outcome(&task.id, outcome.clone())?;
            }
            return Ok(result);
        }
        // A successful process exit is not proof of removal (e.g. a no-op wrapper). Verify every bundle, and
        // Homebrew's own record (bundles may already be in our Trash), before reporting success or removing data.
        let records: Vec<String> = task
            .target
            .as_ref()
            .map(|target| {
                engine
                    .roots
                    .caskrooms
                    .iter()
                    .map(|root| root.join(&target.token).to_string_lossy().into_owned())
                    .collect()
            })
            .unwrap_or_default();
        let remaining = items
            .iter()
            .filter(|i| i.category == ItemCategory::App)
            .map(|i| &i.path)
            .chain(records.iter());
        for path in remaining {
            match std::fs::symlink_metadata(path) {
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
                _ => {
                    queue.uninstall_progress(
                        &task.id,
                        TaskPhase::Uninstalling,
                        1,
                        20.0,
                        Some(format!("==> Homebrew: removal not confirmed: {path}")),
                    )?;
                    if !trashed.is_empty() {
                        queue.set_uninstall_outcome(&task.id, outcome.clone())?;
                    }
                    return Err(AppError::new("E_UNINSTALL_INCOMPLETE", "app_still_exists"));
                }
            }
        }
        queue.uninstall_progress(
            &task.id,
            TaskPhase::Uninstalling,
            1,
            25.0,
            Some("==> Homebrew: uninstall complete".into()),
        )?;
        let deleted: Vec<_> = items
            .iter()
            .filter(|i| i.category == ItemCategory::App && !trashed.contains(&i.id))
            .collect();
        outcome.freed_bytes += deleted
            .iter()
            .map(|i| i.size_bytes.unwrap_or(0))
            .sum::<u64>();
        outcome.deleted += deleted.len() as i64;
        brew_outcome = Some(result);
    }
    let q = queue.clone();
    let task_owned = task.clone();
    let entry_owned = entry.clone();
    let app_owned = app.clone();
    let cancel_result = cancel.clone();
    let result = crate::blocking(move || {
        execute_files(
            &ExecutionContext {
                engine: &engine,
                queue: &q,
                task: &task_owned,
                entry: &entry_owned,
                app: &app_owned,
                mode,
            },
            items,
            cancel,
            outcome,
        )
    })
    .await?;
    queue.set_uninstall_outcome(&task.id, result)?;
    queue.uninstall_progress(&task.id, TaskPhase::Finishing, 3, 100.0, None)?;
    let mut result = brew_outcome.unwrap_or(crate::brew::pty::Outcome {
        code: 0,
        canceled: false,
        #[cfg(test)]
        signal: None,
        parser: Default::default(),
    });
    result.canceled |= cancel_result.load(Ordering::Acquire);
    Ok(result)
}
struct ExecutionContext<'a> {
    engine: &'a Engine,
    queue: &'a TaskQueue,
    task: &'a Task,
    entry: &'a PlanEntry,
    app: &'a AppPlan,
    mode: DeleteMode,
}
// Related data must never be removed until every application bundle is gone.
fn execute_files(
    context: &ExecutionContext<'_>,
    items: Vec<PlanItem>,
    cancel: Arc<AtomicBool>,
    outcome: UninstallOutcome,
) -> Result<UninstallOutcome, AppError> {
    let (bundles, related): (Vec<_>, Vec<_>) = items
        .into_iter()
        .partition(|item| item.category == ItemCategory::App);
    let paths: Vec<_> = bundles.iter().map(|item| item.path.clone()).collect();
    let outcome = execute_items(context, bundles, cancel.clone(), outcome)?;
    if paths.iter().any(|path| {
        !matches!(fs::symlink_metadata(path), Err(error) if error.kind() == std::io::ErrorKind::NotFound)
    }) {
        context.queue.set_uninstall_outcome(&context.task.id, outcome)?;
        return Err(AppError::new("E_UNINSTALL_INCOMPLETE", "app_still_exists"));
    }
    execute_items(context, related, cancel, outcome)
}
fn execute_items(
    context: &ExecutionContext<'_>,
    mut items: Vec<PlanItem>,
    cancel: Arc<AtomicBool>,
    mut outcome: UninstallOutcome,
) -> Result<UninstallOutcome, AppError> {
    let ExecutionContext {
        engine,
        queue,
        task,
        entry,
        app,
        mode,
    } = *context;
    // A folder a rule removes only when empty goes after everything inside it, deepest first.
    items.sort_by_key(|item| match entry.actions.get(&item.id) {
        Some(ItemAction::Path {
            empty_only: true, ..
        }) => (1, usize::MAX - Path::new(&item.path).components().count()),
        _ => (0, 0),
    });
    let root = engine.trash_root()?;
    let total = items.len().max(1);
    let mut admins = vec![];
    let mut users = vec![];
    for (index, item) in items.into_iter().enumerate() {
        if matches!(
            entry.actions.get(&item.id),
            Some(ItemAction::BrewStep | ItemAction::LoginItem)
        ) {
            continue;
        }
        if cancel.load(Ordering::Acquire) {
            break;
        }
        let action = entry
            .actions
            .get(&item.id)
            .ok_or_else(|| AppError::new("E_PLAN_EXPIRED", "item_action"))?;
        let path = Path::new(&item.path);
        let app_path = entry
            .app_records
            .values()
            .find(|r| crate::updater::backup::is_directory(Path::new(&r.app.path), path))
            .or_else(|| entry.app_records.get(&app.app_id))
            .map(|r| Path::new(&r.app.path));
        let reason = if !engine.roots.allowed(path, item.reason, app_path) {
            Some(SkippedItemReason::Protected)
        } else if fs::symlink_metadata(path).is_err() {
            Some(SkippedItemReason::Missing)
        } else if !entry.snapshots.get(&item.id).is_some_and(|s| s.still(path)) {
            Some(SkippedItemReason::Changed)
        } else {
            None
        };
        if let Some(reason) = reason {
            queue.uninstall_progress(
                &task.id,
                TaskPhase::Cleaning,
                2,
                30.0 + 60.0 * index as f32 / total as f32,
                Some(format!("skipped {:?} {}", reason, item.path)),
            )?;
            outcome.skipped.push(SkippedItem {
                item_id: item.id,
                path: item.path,
                reason,
            });
            continue;
        }
        let snapshot = entry
            .snapshots
            .get(&item.id)
            .ok_or_else(|| AppError::new("E_PLAN_EXPIRED", "item_snapshot"))?;
        if item.needs_admin {
            let destination = (mode == DeleteMode::Trash
                && matches!(action, ItemAction::Path { .. }))
            .then(|| {
                root.join(format!(
                    "OpenNavo-{}-{}",
                    uuid::Uuid::now_v7(),
                    path.file_name().unwrap_or_default().to_string_lossy()
                ))
            });
            if !matches!(action, ItemAction::Forget { .. }) {
                manifest::pending(
                    &engine.database,
                    &task.id,
                    &item,
                    snapshot,
                    destination.as_deref(),
                )?;
            }
            admins.push(AdminRequest {
                item,
                snapshot: snapshot.clone(),
                action: action.clone(),
                mode,
                destination,
                app_path: app_path.map(Path::to_owned),
            });
            continue;
        }
        if item.category == ItemCategory::App {
            perform_user(context, &item, action, snapshot, &root, 20.0, &mut outcome)?;
        } else {
            users.push((item, action.clone(), snapshot.clone()));
        }
    }
    if !admins.is_empty() && !cancel.load(Ordering::Acquire) {
        queue.uninstall_progress(&task.id, TaskPhase::Cleaning, 2, 30.0, None)?;
        queue.uninstall_progress(
            &task.id,
            TaskPhase::Cleaning,
            2,
            30.0,
            Some(format!(
                "==> OpenNavo: requesting administrator access for {} items",
                admins.len()
            )),
        )?;
        let results = admin_batch(engine, &admins);
        let mut indexed: HashMap<_, _> = results
            .unwrap_or_default()
            .into_iter()
            .map(|r| (r.item_id.clone(), r))
            .collect();
        for request in admins {
            let mut result = indexed.remove(&request.item.id);
            // A helper interruption must not lose undo for a move already completed before its result was flushed.
            if result.is_none()
                && let Some(destination) = &request.destination
                && fs::symlink_metadata(&request.item.path).is_err()
                && let Ok(after) = Snapshot::capture(destination)
                && manifest::moved_identity(&request.snapshot, &after)
            {
                result = Some(AdminResult {
                    item_id: request.item.id.clone(),
                    destination: Some(destination.clone()),
                    error: None,
                    kept: false,
                });
            }
            if let Some(ref result) = result
                && result.kept
            {
                manifest::finish(
                    &engine.database,
                    &task.id,
                    &request.item.id,
                    "kept",
                    None,
                    &request.snapshot,
                )?;
                queue.uninstall_progress(
                    &task.id,
                    TaskPhase::Cleaning,
                    2,
                    60.0,
                    Some(format!("kept (not empty) {}", request.item.path)),
                )?;
                continue;
            }
            if let Some(ref result) = result
                && result.error.is_none()
            {
                let after = result
                    .destination
                    .as_ref()
                    .and_then(|p| {
                        Snapshot::capture(p)
                            .or_else(|_| request.snapshot.moved_to(p))
                            .ok()
                    })
                    .unwrap_or(request.snapshot.clone());
                manifest::finish(
                    &engine.database,
                    &task.id,
                    &request.item.id,
                    if result.destination.is_some() {
                        outcome.trashed_bytes += request.item.size_bytes.unwrap_or(0);
                        "trash"
                    } else {
                        "deleted"
                    },
                    result.destination.as_deref(),
                    &after,
                )?;
                if !matches!(request.action, ItemAction::Forget { .. }) {
                    outcome.freed_bytes += request.item.size_bytes.unwrap_or(0);
                    if result.destination.is_some() {
                        outcome.trashed += 1;
                        outcome.restorable = true;
                    } else {
                        outcome.deleted += 1;
                    }
                }
                queue.uninstall_progress(
                    &task.id,
                    TaskPhase::Cleaning,
                    2,
                    60.0,
                    Some(format!(
                        "{} {}",
                        if result.destination.is_some() {
                            "trashed"
                        } else {
                            "deleted"
                        },
                        request.item.path
                    )),
                )?;
            } else {
                let reason = result
                    .and_then(|r| r.error)
                    .unwrap_or(SkippedItemReason::AdminCanceled);
                manifest::finish(
                    &engine.database,
                    &task.id,
                    &request.item.id,
                    "failed",
                    request.destination.as_deref(),
                    &request.snapshot,
                )?;
                outcome.skipped.push(SkippedItem {
                    item_id: request.item.id,
                    path: request.item.path.clone(),
                    reason,
                });
                queue.uninstall_progress(
                    &task.id,
                    TaskPhase::Cleaning,
                    2,
                    60.0,
                    Some(format!("skipped {:?} {}", reason, request.item.path)),
                )?;
            }
        }
    }
    for (index, (item, action, snapshot)) in users.into_iter().enumerate() {
        if cancel.load(Ordering::Acquire) {
            break;
        }
        if !snapshot.still(Path::new(&item.path)) {
            let reason = if fs::symlink_metadata(&item.path).is_err() {
                SkippedItemReason::Missing
            } else {
                SkippedItemReason::Changed
            };
            outcome.skipped.push(SkippedItem {
                item_id: item.id.clone(),
                path: item.path.clone(),
                reason,
            });
            queue.uninstall_progress(
                &task.id,
                TaskPhase::Cleaning,
                2,
                60.0 + 30.0 * index as f32 / total as f32,
                Some(format!("skipped {:?} {}", reason, item.path)),
            )?;
            continue;
        }
        perform_user(
            context,
            &item,
            &action,
            &snapshot,
            &root,
            60.0 + 30.0 * index as f32 / total as f32,
            &mut outcome,
        )?;
    }
    Ok(outcome)
}
fn perform_user(
    context: &ExecutionContext<'_>,
    item: &PlanItem,
    action: &ItemAction,
    snapshot: &Snapshot,
    root: &Path,
    percent: f32,
    outcome: &mut UninstallOutcome,
) -> Result<(), AppError> {
    let ExecutionContext {
        engine,
        queue,
        task,
        mode,
        ..
    } = *context;
    let (phase, step) = if item.category == ItemCategory::App {
        (TaskPhase::Uninstalling, 1)
    } else {
        (TaskPhase::Cleaning, 2)
    };
    manifest::pending(&engine.database, &task.id, item, snapshot, None)?;
    match act_user(engine, item, action, snapshot, mode, root) {
        Ok(Acted::Kept) => {
            manifest::finish(&engine.database, &task.id, &item.id, "kept", None, snapshot)?;
            queue.uninstall_progress(
                &task.id,
                phase,
                step,
                percent,
                Some(format!("kept (not empty) {}", item.path)),
            )?;
        }
        Ok(acted) => {
            let destination = match acted {
                Acted::Trashed(destination) => Some(destination),
                _ => None,
            };
            // The Trash itself may be unreadable without Full Disk Access; derive the moved item's identity then.
            let after = destination
                .as_ref()
                .and_then(|p| Snapshot::capture(p).or_else(|_| snapshot.moved_to(p)).ok())
                .unwrap_or_else(|| snapshot.clone());
            manifest::finish(
                &engine.database,
                &task.id,
                &item.id,
                if destination.is_some() {
                    "trash"
                } else {
                    "deleted"
                },
                destination.as_deref(),
                &after,
            )?;
            outcome.freed_bytes += item.size_bytes.unwrap_or(0);
            if destination.is_some() {
                outcome.trashed_bytes += item.size_bytes.unwrap_or(0);
                outcome.trashed += 1;
                outcome.restorable = true;
            } else {
                outcome.deleted += 1;
            }
            queue.uninstall_progress(
                &task.id,
                phase,
                step,
                percent,
                Some(format!(
                    "{} {}",
                    if destination.is_some() {
                        "trashed"
                    } else {
                        "deleted"
                    },
                    item.path
                )),
            )?;
        }
        Err(error) => {
            let reason = if error.message == "path_changed" {
                SkippedItemReason::Changed
            } else if error.code == "E_NOT_FOUND" {
                SkippedItemReason::Missing
            } else if error.code == "E_APP_RUNNING" {
                SkippedItemReason::InUse
            } else {
                SkippedItemReason::Permission
            };
            manifest::finish(
                &engine.database,
                &task.id,
                &item.id,
                "failed",
                None,
                snapshot,
            )?;
            outcome.skipped.push(SkippedItem {
                item_id: item.id.clone(),
                path: item.path.clone(),
                reason,
            });
            queue.uninstall_progress(
                &task.id,
                TaskPhase::Cleaning,
                2,
                percent,
                Some(format!("skipped {:?} {}", reason, item.path)),
            )?;
            if item.required && item.category == ItemCategory::App {
                queue.set_uninstall_outcome(&task.id, outcome.clone())?;
                return Err(error);
            }
        }
    }
    Ok(())
}
enum Acted {
    Trashed(PathBuf),
    Deleted,
    /// A folder that a rule removes only when empty still had something in it, so it stays, as the rule intends.
    Kept,
}
fn act_user(
    engine: &Engine,
    item: &PlanItem,
    action: &ItemAction,
    snapshot: &Snapshot,
    mode: DeleteMode,
    trash_root: &Path,
) -> Result<Acted, AppError> {
    if let ItemAction::Path {
        empty_only,
        bootout: service,
    } = action
    {
        // Check identity before stopping a service, so a replaced plist never boots anything out.
        if !snapshot.still(Path::new(&item.path)) {
            return Err(AppError::new("E_PLAN_EXPIRED", "path_changed"));
        }
        if let Some((label, system)) = service {
            engine
                .file_actions
                .lock()
                .map_err(poison)?
                .bootout(label, *system, &item.path)?;
        }
        if *empty_only && fs::read_dir(&item.path)?.next().is_some() {
            return Ok(Acted::Kept);
        }
        if mode == DeleteMode::Trash {
            return engine
                .file_actions
                .lock()
                .map_err(poison)?
                .trash(Path::new(&item.path), trash_root)
                .map(Acted::Trashed);
        }
        remove(Path::new(&item.path), *empty_only)?;
        return Ok(Acted::Deleted);
    }
    Err(AppError::new("E_PROTECTED", "admin_action_required"))
}
pub(crate) fn bootout(label: &str, system: bool, path: &str) -> Result<(), AppError> {
    if !identity::valid_identifier(label) {
        return Err(AppError::new("E_PROTECTED", "launchd_label"));
    }
    let domain = if system {
        "system".into()
    } else {
        identity::user_domain()
    };
    // An already unloaded service is safe to remove; other bootout failures preserve its plist.
    if identity::command(
        "/bin/launchctl",
        &["print", &format!("{domain}/{label}")],
        Duration::from_secs(2),
    )
    .is_err()
    {
        return Ok(());
    }
    identity::command(
        "/bin/launchctl",
        &["bootout", &domain, path],
        Duration::from_secs(5),
    )
    .map(|_| ())
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AdminRequest {
    pub item: PlanItem,
    pub snapshot: Snapshot,
    pub action: ItemAction,
    pub mode: DeleteMode,
    pub destination: Option<PathBuf>,
    pub app_path: Option<PathBuf>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AdminResult {
    pub item_id: String,
    pub destination: Option<PathBuf>,
    pub error: Option<SkippedItemReason>,
    /// A folder removed only when empty still had something in it and stays.
    #[serde(default)]
    pub kept: bool,
}
#[derive(Serialize, Deserialize)]
struct AdminBatch {
    home: PathBuf,
    requests: Vec<AdminRequest>,
    #[serde(default)]
    native: Vec<crate::updater::admin::Request>,
    #[serde(default)]
    security: Vec<crate::security::admin::Request>,
}
#[cfg(test)]
#[path = "admin_protocol_tests.rs"]
mod admin_protocol_tests;
/// What the helper did with one request.
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
enum AdminDone {
    Moved(PathBuf),
    Removed,
    Kept,
}
pub trait AdminActions: Send + Sync {
    fn run(&self, requests: &[AdminRequest]) -> Result<Vec<AdminResult>, AppError>;
}
pub fn admin_batch(
    engine: &Engine,
    requests: &[AdminRequest],
) -> Result<Vec<AdminResult>, AppError> {
    if let Some(admin) = engine.admin_actions.lock().map_err(poison)?.as_ref() {
        return admin.run(requests);
    }
    run_admin_batch(
        engine,
        AdminBatch {
            home: engine.roots.home.clone(),
            requests: requests.to_vec(),
            native: Vec::new(),
            security: Vec::new(),
        },
    )
}
/// New fixed install/swap actions share the same batch envelope, sudo -A and
/// administrator entry as uninstall; one invocation means one password prompt.
pub fn admin_batch_native(
    engine: &Engine,
    requests: &[crate::updater::admin::Request],
) -> Result<Vec<crate::updater::admin::ResultItem>, AppError> {
    if requests.is_empty() {
        return Ok(Vec::new());
    }
    run_admin_batch(
        engine,
        AdminBatch {
            home: engine.roots.home.clone(),
            requests: Vec::new(),
            native: requests.to_vec(),
            security: Vec::new(),
        },
    )
    .map_err(|error| {
        if error.code == "E_SUDO" {
            AppError::new("E_ADMIN_CANCELED", "admin_canceled")
        } else {
            error
        }
    })
}
pub fn admin_batch_security(
    engine: &Engine,
    requests: &[crate::security::admin::Request],
) -> Result<Vec<crate::security::admin::ResultItem>, AppError> {
    if requests.is_empty() {
        return Ok(vec![]);
    }
    run_admin_batch(
        engine,
        AdminBatch {
            home: engine.roots.home.clone(),
            requests: vec![],
            native: vec![],
            security: requests.to_vec(),
        },
    )
    .map_err(|e| {
        if e.code == "E_SUDO" {
            AppError::new("E_ADMIN_CANCELED", "admin_canceled")
        } else {
            e
        }
    })
}
fn run_admin_batch<T: serde::de::DeserializeOwned>(
    engine: &Engine,
    batch: AdminBatch,
) -> Result<Vec<T>, AppError> {
    #[cfg(target_os = "macos")]
    {
        // The request lives in OpenNavo's own data folder, not in the Trash: without Full Disk Access the Trash
        // cannot be written, and every system item was then reported as a canceled password prompt.
        use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
        let base = engine.admin_root();
        fs::create_dir_all(&base)?;
        fs::set_permissions(&base, fs::Permissions::from_mode(0o700))?;
        let directory = base.join(format!("batch-{}", uuid::Uuid::now_v7()));
        fs::create_dir(&directory)?;
        fs::set_permissions(&directory, fs::Permissions::from_mode(0o700))?;
        let input = directory.join("request.json");
        let output = directory.join("results.json");
        let mut file = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(&input)?;
        file.write_all(&serde_json::to_vec(&batch)?)?;
        file.sync_all()?;
        let mut result_file = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(&output)?;
        result_file.write_all(b"[]")?;
        let settings = engine.settings.read().map_err(poison)?.clone();
        let env = crate::brew::env::construct(&settings, "/usr/local", &engine.askpass);
        let status = std::process::Command::new("/usr/bin/sudo")
            .args(["-A", "--"])
            .arg(std::env::current_exe()?)
            .arg("--opennavo-uninstall-admin")
            .arg(&input)
            .arg(&output)
            .envs(env)
            .stdin(std::process::Stdio::null())
            .stdout(std::process::Stdio::null())
            .stderr(std::process::Stdio::null())
            .status();
        let results = fs::read(&output)
            .ok()
            .and_then(|v| serde_json::from_slice::<Vec<T>>(&v).ok())
            .unwrap_or_default();
        let _ = fs::remove_file(&input);
        let _ = fs::remove_file(&output);
        let _ = fs::remove_dir(&directory);
        if status.is_ok_and(|s| s.success()) || !results.is_empty() {
            Ok(results)
        } else {
            Err(AppError::new("E_SUDO", "admin_canceled"))
        }
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = (engine, batch);
        Err(AppError::new("E_SUDO", "unsupported_platform"))
    }
}
/// Invoked before Tauri initialization. Only fixed actions with backend snapshots are accepted; never a script.
pub fn admin_entry() -> Option<i32> {
    let args: Vec<_> = std::env::args_os().collect();
    if args
        .get(1)
        .is_none_or(|a| a != "--opennavo-uninstall-admin")
    {
        return None;
    }
    #[cfg(target_os = "macos")]
    {
        let result = (|| -> Result<(), AppError> {
            use std::os::unix::fs::MetadataExt;
            if args.len() != 4
                || std::env::var("SUDO_UID")
                    .ok()
                    .and_then(|v| v.parse::<u32>().ok())
                    .is_none_or(|uid| uid == 0)
            {
                return Err(AppError::new("E_PROTECTED", "admin_context"));
            }
            let uid = std::env::var("SUDO_UID")
                .ok()
                .and_then(|v| v.parse::<u32>().ok())
                .ok_or_else(|| AppError::new("E_PROTECTED", "admin_uid"))?;
            let input = PathBuf::from(&args[2]);
            let output = PathBuf::from(&args[3]);
            if !rules::parents_safe(&input)
                || !rules::parents_safe(&output)
                || input.parent() != output.parent()
            {
                return Err(AppError::new("E_PROTECTED", "admin_paths"));
            }
            use std::os::unix::fs::OpenOptionsExt;
            let directory = input
                .parent()
                .ok_or_else(|| AppError::new("E_PROTECTED", "admin_directory"))?;
            let dm = fs::symlink_metadata(directory)?;
            if dm.uid() != uid || dm.mode() & 0o077 != 0 || !dm.is_dir() || dm.is_symlink() {
                return Err(AppError::new("E_PROTECTED", "admin_directory"));
            }
            let mut file = fs::OpenOptions::new()
                .read(true)
                .custom_flags(nix::libc::O_NOFOLLOW)
                .open(&input)?;
            let metadata = file.metadata()?;
            if metadata.uid() != uid
                || metadata.mode() & 0o077 != 0
                || !metadata.is_file()
                || metadata.len() > 4 * 1024 * 1024
            {
                return Err(AppError::new("E_PROTECTED", "admin_request"));
            }
            let mut bytes = vec![];
            file.read_to_end(&mut bytes)?;
            let batch: AdminBatch = serde_json::from_slice(&bytes)?;
            if !batch.home.starts_with("/Users")
                || fs::metadata(&batch.home)?.uid() != uid
                || batch.requests.len() > 10_000
            {
                return Err(AppError::new("E_PROTECTED", "admin_home"));
            }
            let trash = batch.home.join(".Trash");
            if !input.starts_with(&batch.home)
                || (batch.native.is_empty()
                    && batch.security.is_empty()
                    && (!fs::symlink_metadata(&trash)?.is_dir()
                        || fs::symlink_metadata(&trash)?.is_symlink()))
            {
                return Err(AppError::new("E_PROTECTED", "admin_trash"));
            }
            let mut roots = rules::Roots::native();
            roots.home = batch.home.clone();
            roots.applications = vec!["/Applications".into(), batch.home.join("Applications")];
            let out_meta = fs::symlink_metadata(&output)?;
            if !out_meta.is_file()
                || out_meta.is_symlink()
                || out_meta.uid() != uid
                || out_meta.mode() & 0o077 != 0
            {
                return Err(AppError::new("E_PROTECTED", "admin_output"));
            }
            let mut out = fs::OpenOptions::new()
                .write(true)
                .custom_flags(nix::libc::O_NOFOLLOW)
                .open(&output)?;
            let opened = out.metadata()?;
            if opened.dev() != out_meta.dev() || opened.ino() != out_meta.ino() {
                return Err(AppError::new("E_PROTECTED", "admin_output_changed"));
            }
            if !batch.native.is_empty() {
                if !batch.requests.is_empty() || !batch.security.is_empty() {
                    return Err(AppError::new("E_PROTECTED", "mixed_admin_batch"));
                }
                let data = directory
                    .parent()
                    .and_then(|p| p.parent())
                    .ok_or_else(|| AppError::new("E_PROTECTED", "native_data_root"))?;
                let results =
                    crate::updater::admin::execute(&batch.native, &batch.home, data, uid)?;
                out.set_len(0)?;
                out.write_all(&serde_json::to_vec(&results)?)?;
                out.sync_all()?;
                return Ok(());
            }
            if !batch.security.is_empty() {
                if !batch.requests.is_empty() {
                    return Err(AppError::new("E_PROTECTED", "mixed_admin_batch"));
                }
                let data = directory
                    .parent()
                    .and_then(|p| p.parent())
                    .ok_or_else(|| AppError::new("E_PROTECTED", "security_data_root"))?;
                let results =
                    crate::security::admin::execute(&batch.security, &batch.home, data, uid)?;
                out.set_len(0)?;
                out.write_all(&serde_json::to_vec(&results)?)?;
                out.sync_all()?;
                return Ok(());
            }
            let mut results = vec![];
            let mut removed_receipts = std::collections::BTreeSet::new();
            for request in batch.requests {
                let path = Path::new(&request.item.path);
                let result = (|| -> Result<AdminDone, SkippedItemReason> {
                    let trash_action = matches!(
                        request.action,
                        ItemAction::RestoreTrash { .. } | ItemAction::PurgeTrash
                    );
                    if (trash_action && path.parent() != Some(trash.as_path()))
                        || (!trash_action
                            && !roots.allowed(
                                path,
                                request.item.reason,
                                request.app_path.as_deref(),
                            ))
                        || std::env::current_exe().is_ok_and(|p| p.starts_with(path))
                    {
                        return Err(SkippedItemReason::Protected);
                    }
                    let forgotten_by_moves = matches!(&request.action, ItemAction::Forget { .. })
                        && removed_receipts.contains(path)
                        && fs::symlink_metadata(path).is_err();
                    // Moving out of the Trash is checked strictly; removing a planned item only needs the same object.
                    let unchanged = if trash_action {
                        request.snapshot.matches(path)
                    } else {
                        request.snapshot.still(path)
                    };
                    if !unchanged && !forgotten_by_moves {
                        return Err(SkippedItemReason::Changed);
                    }
                    match &request.action {
                        ItemAction::Path {
                            empty_only,
                            bootout: service,
                        } => {
                            if let Some((label, system)) = service {
                                bootout(label, *system, &request.item.path)
                                    .map_err(|_| SkippedItemReason::InUse)?;
                            }
                            if *empty_only
                                && fs::read_dir(path)
                                    .map_err(|_| SkippedItemReason::Permission)?
                                    .next()
                                    .is_some()
                            {
                                return Ok(AdminDone::Kept);
                            }
                            if request.mode == DeleteMode::Trash {
                                let target = request
                                    .destination
                                    .as_ref()
                                    .ok_or(SkippedItemReason::Protected)?;
                                if target.parent() != Some(trash.as_path())
                                    || fs::symlink_metadata(target).is_ok()
                                {
                                    return Err(SkippedItemReason::Protected);
                                }
                                move_exclusive(path, target)
                                    .map_err(|_| SkippedItemReason::Permission)?;
                                Ok(AdminDone::Moved(target.clone()))
                            } else {
                                remove(path, *empty_only)
                                    .map_err(|_| SkippedItemReason::Permission)?;
                                Ok(AdminDone::Removed)
                            }
                        }
                        ItemAction::RestoreTrash { original, reason } => {
                            if !roots.allowed(original, *reason, request.app_path.as_deref())
                                || fs::symlink_metadata(original).is_ok()
                            {
                                return Err(SkippedItemReason::Protected);
                            }
                            move_exclusive(path, original)
                                .map_err(|_| SkippedItemReason::Permission)?;
                            Ok(AdminDone::Removed)
                        }
                        ItemAction::PurgeTrash => {
                            remove(path, false).map_err(|_| SkippedItemReason::Permission)?;
                            Ok(AdminDone::Removed)
                        }
                        ItemAction::Forget { package } => {
                            if !identity::valid_identifier(package)
                                || (path != roots.receipts.join(format!("{package}.plist"))
                                    && path != roots.receipts.join(format!("{package}.bom")))
                            {
                                return Err(SkippedItemReason::Protected);
                            }
                            let status = identity::command(
                                "/usr/sbin/pkgutil",
                                &["--forget", package],
                                Duration::from_secs(5),
                            );
                            // Modern pkg receipts are the .bom/.plist pair. After both approved moves/deletes, pkgutil may already report not found.
                            if status.is_err()
                                && !(forgotten_by_moves
                                    && ["bom", "plist"].iter().all(|ext| {
                                        fs::symlink_metadata(
                                            roots.receipts.join(format!("{package}.{ext}")),
                                        )
                                        .is_err()
                                    }))
                            {
                                return Err(SkippedItemReason::Permission);
                            }
                            Ok(AdminDone::Removed)
                        }
                        _ => Err(SkippedItemReason::Protected),
                    }
                })();
                if result.is_ok()
                    && request.item.category == ItemCategory::Receipts
                    && matches!(request.action, ItemAction::Path { .. })
                {
                    removed_receipts.insert(PathBuf::from(&request.item.path));
                }
                results.push(match result {
                    Ok(done) => AdminResult {
                        item_id: request.item.id,
                        kept: matches!(done, AdminDone::Kept),
                        destination: match done {
                            AdminDone::Moved(destination) => Some(destination),
                            _ => None,
                        },
                        error: None,
                    },
                    Err(error) => AdminResult {
                        item_id: request.item.id,
                        destination: None,
                        error: Some(error),
                        kept: false,
                    },
                });
                out.set_len(0)?;
                use std::io::Seek;
                out.rewind()?;
                out.write_all(&serde_json::to_vec(&results)?)?;
                out.sync_all()?;
            }
            Ok(())
        })();
        Some(if result.is_ok() { 0 } else { 1 })
    }
    #[cfg(not(target_os = "macos"))]
    {
        Some(1)
    }
}
