use super::{Engine, Entry, EntryItem, owner::Owner, poison, protect, reconcile, rules, tree};
use crate::{
    AppError,
    core::DesktopCore,
    model::*,
    uninstall::{self, CachedApp, ItemAction},
};
use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    path::{Path, PathBuf},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};

struct RequestGuard {
    engine: Arc<Engine>,
    id: String,
    flag: Arc<AtomicBool>,
}
impl Drop for RequestGuard {
    fn drop(&mut self) {
        self.flag.store(true, Ordering::Release);
        let _ = self.engine.cancel(&self.id);
        if let Ok(mut requests) = self.engine.requests.lock() {
            requests.remove(&self.id);
        }
    }
}
struct Collected {
    category: CategoryReport,
    incomplete: Vec<SkippedLocation>,
    items: BTreeMap<String, EntryItem>,
    skipped_keys: Vec<(String, SkippedGroupReason)>,
}
impl Collected {
    fn new(key: CleanupCategory) -> Self {
        Self {
            category: CategoryReport {
                key,
                risk: match key {
                    CleanupCategory::Leftovers
                    | CleanupCategory::Installers
                    | CleanupCategory::ProjectArtifacts
                    | CleanupCategory::Docker
                    | CleanupCategory::Trash => CleanupRisk::Review,
                    CleanupCategory::DeviceBackups => CleanupRisk::Personal,
                    _ => CleanupRisk::Rebuildable,
                },
                bytes: 0,
                complete: true,
                groups: vec![],
                skipped: vec![],
            },
            incomplete: vec![],
            items: BTreeMap::new(),
            skipped_keys: vec![],
        }
    }
    fn skip(
        &mut self,
        owner: &Owner,
        title: String,
        bytes: Option<u64>,
        reason: SkippedGroupReason,
        processes: Vec<String>,
    ) {
        let key = owner
            .app_id
            .as_ref()
            .or(owner.bundle.as_ref())
            .or(owner.tool_key.as_ref())
            .unwrap_or(&title)
            .clone();
        if let Some(index) = self
            .skipped_keys
            .iter()
            .position(|(id, why)| *id == key && *why == reason)
        {
            let previous = &mut self.category.skipped[index];
            previous.bytes = match (previous.bytes, bytes) {
                (Some(a), Some(b)) => Some(a.saturating_add(b)),
                _ => None,
            };
            previous.processes.extend(processes);
            previous.processes.sort();
            previous.processes.dedup();
        } else {
            self.skipped_keys.push((key, reason));
            self.category.skipped.push(SkippedGroup {
                title,
                app_id: owner.app_id.clone(),
                bytes,
                reason,
                processes,
            });
        }
    }
    fn incomplete(&mut self, path: &Path, reason: SkippedLocationReason) {
        self.category.complete = false;
        let location = SkippedLocation {
            path: path.to_string_lossy().into_owned(),
            reason,
        };
        if !self.incomplete.contains(&location) {
            self.incomplete.push(location);
        }
    }
}
#[derive(Clone)]
struct Candidate {
    path: PathBuf,
    owner: Owner,
    title: String,
    reason: CleanupReason,
    age_days: Option<i64>,
    /// Listed with its measured size but never offered (a project touched recently).
    skip: Option<SkippedGroupReason>,
}
struct Collector<'a> {
    engine: &'a Engine,
    apps: &'a [CachedApp],
    rules: Vec<ProtectRule>,
    permissions: Permissions,
    progress: Arc<super::progress::Progress>,
    flag: &'a AtomicBool,
    deadline: Instant,
    result: Collected,
    activity: &'a Result<super::owner::Activity, AppError>,
    pending: Vec<Candidate>,
    /// Folders searched for project build output: the configured roots plus the usual places that exist.
    project_roots: Vec<PathBuf>,
}
impl Collector<'_> {
    fn progress(&self, path: Option<&Path>) {
        let _ = self
            .progress
            .update(self.result.category.key, path, self.result.category.bytes);
    }
    fn check(&self) -> Result<(), AppError> {
        // Candidate discovery is cancellation-bound, never measurement-deadline-bound.
        if self.flag.load(Ordering::Acquire) {
            return Err(AppError::new("E_CANCELED", "cleanup_canceled"));
        }
        Ok(())
    }
    fn children(&mut self, path: &Path) -> Result<Vec<PathBuf>, AppError> {
        self.check()?;
        if !uninstall::rules::parents_safe(path) {
            return Ok(vec![]);
        }
        match fs::symlink_metadata(path) {
            Ok(m) if m.is_symlink() || !m.is_dir() => return Ok(vec![]),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(vec![]),
            Err(_) => {
                self.result
                    .incomplete(path, SkippedLocationReason::Permission);
                return Ok(vec![]);
            }
            _ => {}
        }
        match fs::read_dir(path) {
            Ok(entries) => {
                let mut paths = vec![];
                for entry in entries {
                    self.check()?;
                    match entry {
                        Ok(entry) => paths.push(entry.path()),
                        Err(_) => self
                            .result
                            .incomplete(path, SkippedLocationReason::Permission),
                    }
                }
                paths.sort();
                Ok(paths)
            }
            Err(_) => {
                self.result
                    .incomplete(path, SkippedLocationReason::Permission);
                Ok(vec![])
            }
        }
    }
    fn fda(&mut self, path: &Path) -> bool {
        if self.permissions.full_disk_access != PermissionState::Granted {
            self.result
                .incomplete(path, SkippedLocationReason::FullDiskAccess);
            true
        } else {
            false
        }
    }
    fn add(
        &mut self,
        path: PathBuf,
        owner: Owner,
        title: String,
        reason: CleanupReason,
        age_days: Option<i64>,
    ) -> Result<(), AppError> {
        self.check()?;
        self.pending.push(Candidate {
            path,
            owner,
            title,
            reason,
            age_days,
            skip: None,
        });
        Ok(())
    }
    /// A candidate that is measured and shown as skipped, never offered.
    fn add_skipped(
        &mut self,
        path: PathBuf,
        owner: Owner,
        title: String,
        reason: CleanupReason,
        skip: SkippedGroupReason,
    ) -> Result<(), AppError> {
        self.check()?;
        self.pending.push(Candidate {
            path,
            owner,
            title,
            reason,
            age_days: None,
            skip: Some(skip),
        });
        Ok(())
    }
    fn measure_candidates(&mut self) -> Result<(), AppError> {
        let pending =
            std::sync::Mutex::new(std::mem::take(&mut self.pending).into_iter().enumerate());
        let results = std::sync::Mutex::new(Vec::new());
        std::thread::scope(|scope| {
            for _ in 0..4 {
                let pending = &pending;
                let results = &results;
                let collector = &*self;
                scope.spawn(move || {
                    loop {
                        let candidate = pending.lock().unwrap().next();
                        let Some((index, candidate)) = candidate else {
                            break;
                        };
                        // Only folder walks share the slots. A missing path or a single file is answered at once
                        // instead of queueing behind other categories' large trees.
                        let walk = fs::symlink_metadata(&candidate.path).is_ok_and(|m| m.is_dir());
                        let mut acquired = false;
                        if walk {
                            let mut slots = collector.engine.scan_slots.lock().unwrap();
                            while *slots == 0
                                && Instant::now() < collector.deadline
                                && !collector.flag.load(Ordering::Acquire)
                            {
                                slots = collector
                                    .engine
                                    .scan_ready
                                    .wait_timeout(slots, Duration::from_millis(25))
                                    .unwrap()
                                    .0;
                            }
                            acquired = *slots > 0;
                            if acquired {
                                *slots -= 1;
                            }
                        }
                        let mut worker = Collector {
                            engine: collector.engine,
                            apps: collector.apps,
                            rules: collector.rules.clone(),
                            permissions: collector.permissions,
                            progress: collector.progress.clone(),
                            flag: collector.flag,
                            deadline: collector.deadline,
                            result: Collected::new(collector.result.category.key),
                            activity: collector.activity,
                            pending: vec![],
                            project_roots: vec![],
                        };
                        let result = worker.add_measured(
                            candidate.path,
                            candidate.owner,
                            candidate.title,
                            candidate.reason,
                            candidate.age_days,
                            candidate.skip,
                        );
                        if acquired {
                            *collector.engine.scan_slots.lock().unwrap() += 1;
                            collector.engine.scan_ready.notify_one();
                        }
                        results.lock().unwrap().push((index, result, worker.result));
                    }
                });
            }
        });
        let mut results = results.into_inner().map_err(poison)?;
        // Apply in the collector's order, whichever worker finished first: merged skipped rows keep the title of the
        // first location and groups keep their order from scan to scan.
        results.sort_by_key(|(index, ..)| *index);
        for (_, result, collected) in results {
            result?;
            for location in collected.incomplete {
                self.result
                    .incomplete(Path::new(&location.path), location.reason);
            }
            for (key, skip) in collected
                .skipped_keys
                .into_iter()
                .zip(collected.category.skipped)
            {
                let owner = Owner {
                    tool_key: Some(key.0),
                    app_id: skip.app_id.clone(),
                    ..Default::default()
                };
                self.result
                    .skip(&owner, skip.title, skip.bytes, skip.reason, skip.processes);
            }
            for item in collected.items.into_values() {
                self.insert(item.item, item.owner, item.title, item.tree, item.action);
            }
            self.progress(None);
        }
        for group in &mut self.result.category.groups {
            group.items.sort_by(|a, b| a.path.cmp(&b.path));
        }
        Ok(())
    }
    fn add_measured(
        &mut self,
        path: PathBuf,
        owner: Owner,
        title: String,
        reason: CleanupReason,
        age_days: Option<i64>,
        skip: Option<SkippedGroupReason>,
    ) -> Result<(), AppError> {
        self.check()?;
        if fs::symlink_metadata(&path).is_err() {
            return Ok(());
        }
        self.progress(Some(&path));
        let home = &self.engine.uninstall.roots.home;
        if owner.protected
            || rules::root_blocked(home, &path, owner.external_cache.as_deref(), reason)
            || protect::is_protected(&self.rules, &path, owner.bundle.as_deref())
        {
            // Visible as protected instead of vanishing from the report without a word.
            self.result
                .skip(&owner, title, None, SkippedGroupReason::Protected, vec![]);
            return Ok(());
        }
        let names_apply = rules::names_apply(reason);
        let blocked = |p: &Path| {
            (names_apply && rules::never_touch_scoped(home, p, owner.external_cache.as_deref()))
                || protect::is_protected(&self.rules, p, owner.bundle.as_deref())
        };
        let partial = self.result.category.key == CleanupCategory::DevCaches
            && (reason == CleanupReason::ToolCache
                || rules::partial_cache(reason, owner.tool_key.as_deref()));
        let open: Vec<_> = if partial {
            self.activity
                .as_ref()
                .map(|a| a.open_under(&path).map(|(p, _)| p.clone()).collect())
                .unwrap_or_default()
        } else {
            vec![]
        };
        let activity_reason = owner
            .status_in(self.activity)
            .or_else(|| match self.activity {
                Ok(activity) if !partial && activity.in_use(&path) => {
                    Some(SkippedGroupReason::InUse)
                }
                _ => None,
            });
        if let Some(reason) = skip.or(activity_reason.filter(|_| age_days.is_none())) {
            // A live cache may change while it is measured. It needs a read-only estimate,
            // not the stable snapshot required for entries that can actually be executed.
            let (bytes, incomplete) =
                tree::measure_filtered(&path, self.flag, self.deadline, blocked)?;
            if let Some(reason) = incomplete {
                self.result.incomplete(&path, reason);
            }
            // A recent project with nothing generated yet is not worth a line.
            if skip.is_some() && bytes == 0 && incomplete.is_none() {
                return Ok(());
            }
            let processes = if skip.is_some() {
                vec![]
            } else {
                owner.blocking_processes(self.activity, &path, reason)
            };
            self.result
                .skip(&owner, title, Some(bytes), reason, processes);
            return Ok(());
        }
        let captured = match tree::estimate(
            &path,
            self.flag,
            self.deadline,
            self.engine.max_tree_nodes,
            blocked,
            &open,
        ) {
            Ok(tree) => tree,
            Err(error) if error.code == "E_CANCELED" || error.code == "E_TIMEOUT" => {
                return Err(error);
            }
            // Refused for safety (protected descendant, symlink, another owner): list it as protected rather than
            // letting tens of gigabytes vanish from the report without a word.
            Err(error) if error.code == "E_PROTECTED" => {
                self.result
                    .skip(&owner, title, None, SkippedGroupReason::Protected, vec![]);
                return Ok(());
            }
            Err(_) => {
                self.result
                    .incomplete(&path, SkippedLocationReason::Permission);
                return Ok(());
            }
        };
        if let Some(reason) = captured.incomplete {
            self.result.incomplete(&path, reason);
        }
        if captured.snapshots.keys().any(|p| blocked(p)) {
            self.result.skip(
                &owner,
                title,
                Some(captured.bytes),
                SkippedGroupReason::Protected,
                vec![],
            );
            return Ok(());
        }
        if age_days.is_some_and(|days| captured.modified_at > uninstall::now() - days * 86_400_000)
        {
            return Ok(());
        }
        // The temp folder and the Trash hold hundreds of empty leftovers; they free nothing and only add rows.
        if captured.bytes == 0
            && captured.incomplete.is_none()
            && matches!(reason, CleanupReason::TempFile | CleanupReason::Trash)
        {
            return Ok(());
        }
        if let Some(reason) = activity_reason {
            let processes = owner.blocking_processes(self.activity, &path, reason);
            self.result
                .skip(&owner, title, Some(captured.bytes), reason, processes);
            return Ok(());
        }
        let root = captured
            .snapshots
            .get(&path)
            .ok_or_else(super::cached_error)?;
        let item = CleanupItem {
            id: uuid::Uuid::now_v7().to_string(),
            path: path.to_string_lossy().into_owned(),
            kind: if root.kind == 1 {
                CleanupItemKind::Dir
            } else {
                CleanupItemKind::File
            },
            command: None,
            size_bytes: Some(captured.bytes),
            kept_open_files: captured.kept_open_files,
            modified_at: Some(
                if matches!(
                    reason,
                    CleanupReason::DeviceSupport | CleanupReason::DeviceSupportLatest
                ) {
                    (root.modified / 1_000_000).min(i64::MAX as u128) as i64
                } else {
                    captured.modified_at
                },
            ),
            reason,
            // Unchecked by default: the newest device support version, anything downloaded again, installers whose
            // app is not installed, the Trash (emptying it cannot be undone) and personal backups and archives.
            selected: !matches!(
                reason,
                CleanupReason::DeviceSupportLatest
                    | CleanupReason::ToolDownloadCache
                    | CleanupReason::Installer
                    | CleanupReason::Trash
                    | CleanupReason::DeviceBackup
                    | CleanupReason::XcodeArchive
            ),
            // Installers, backups and archives go to the Trash so a wrong pick can be put back.
            delete_mode: if matches!(
                reason,
                CleanupReason::Installer
                    | CleanupReason::InstallerInstalled
                    | CleanupReason::DeviceBackup
                    | CleanupReason::XcodeArchive
            ) {
                DeleteMode::Trash
            } else {
                DeleteMode::Delete
            },
            needs_admin: false,
            note: None,
        };
        self.insert(
            item,
            owner,
            title,
            Some(captured),
            ItemAction::Path {
                empty_only: false,
                bootout: None,
            },
        );
        Ok(())
    }
    fn insert(
        &mut self,
        item: CleanupItem,
        owner: Owner,
        title: String,
        tree: Option<tree::Tree>,
        action: ItemAction,
    ) {
        let key = owner
            .group_key
            .clone()
            .or(owner.bundle.clone())
            .unwrap_or_else(|| title.clone());
        let position = self.result.category.groups.iter().position(|g| g.id == key);
        let position = position.unwrap_or_else(|| {
            self.result.category.groups.push(CleanupGroup {
                id: key,
                title: title.clone(),
                subtitle: owner.group_subtitle.clone(),
                app_id: owner.app_id.clone(),
                bundle_id: owner.bundle.clone(),
                items: vec![],
            });
            self.result.category.groups.len() - 1
        });
        self.result.category.bytes = self
            .result
            .category
            .bytes
            .saturating_add(item.size_bytes.unwrap_or(0));
        self.result.category.groups[position]
            .items
            .push(item.clone());
        self.result.items.insert(
            item.id.clone(),
            EntryItem {
                docker_endpoint: None,
                item,
                owner,
                title,
                tree,
                action,
            },
        );
    }
    fn app_caches(&mut self) -> Result<(), AppError> {
        let home = self.engine.uninstall.roots.home.clone();
        for path in self.children(&home.join("Library/Caches"))? {
            let name = path.file_name().unwrap_or_default().to_string_lossy();
            if name == "Homebrew"
                || rules::browser_bundle(&name)
                || rules::TOOLS.iter().any(|t| home.join(t.path) == path)
                || rules::DOWNLOAD_TOOLS
                    .iter()
                    .any(|t| home.join(t.path) == path)
                || [
                    "Google",
                    "Microsoft Edge",
                    "Arc",
                    "BraveSoftware",
                    "Firefox",
                ]
                .contains(&name.as_ref())
            {
                continue;
            }
            if name.starts_with("com.apple.")
                && !rules::APPLE_CACHE_ALLOWLIST.contains(&name.as_ref())
            {
                continue;
            }
            let owner = self.app_owner(&name);
            let title = self.title(&owner, &name);
            self.add(path, owner, title, CleanupReason::CacheDir, None)?;
        }
        self.electron_caches()?;
        let containers = home.join("Library/Containers");
        if self.fda(&containers) {
            return Ok(());
        }
        for container in self.children(&containers)? {
            let bundle = container.file_name().unwrap_or_default().to_string_lossy();
            if rules::browser_bundle(&bundle)
                || (bundle.starts_with("com.apple.")
                    && !rules::APPLE_CACHE_ALLOWLIST.contains(&bundle.as_ref()))
            {
                continue;
            }
            let owner = self.app_owner(&bundle);
            self.add(
                container.join("Data/Library/Caches"),
                owner.clone(),
                self.title(&owner, &bundle),
                CleanupReason::CacheDir,
                None,
            )?;
        }
        Ok(())
    }
    fn electron_caches(&mut self) -> Result<(), AppError> {
        let support = self
            .engine
            .uninstall
            .roots
            .home
            .join("Library/Application Support");
        for directory in self.children(&support)? {
            let name = directory.file_name().unwrap_or_default().to_string_lossy();
            let candidates: Vec<_> = self
                .apps
                .iter()
                .filter(|app| {
                    app.app.name.eq_ignore_ascii_case(&name)
                        || app
                            .app
                            .bundle_id
                            .as_deref()
                            .is_some_and(|id| id.eq_ignore_ascii_case(&name))
                        || Path::new(&app.app.path)
                            .file_stem()
                            .is_some_and(|n| n.to_string_lossy().eq_ignore_ascii_case(&name))
                        || (app.app.bundle_id.as_deref() == Some("com.microsoft.VSCode")
                            && name == "Code")
                        || (app.app.bundle_id.as_deref() == Some("com.microsoft.VSCodeInsiders")
                            && name == "Code - Insiders")
                })
                .collect();
            // Ambiguous ownership is not permission to remove application data.
            if candidates.len() != 1 {
                continue;
            }
            let app = candidates[0];
            let Some(bundle) = &app.app.bundle_id else {
                continue;
            };
            let chromium = ["Local Storage", "Preferences"].iter().any(|marker| {
                fs::symlink_metadata(directory.join(marker))
                    .is_ok_and(|m| !m.is_symlink() && (m.is_file() || m.is_dir()))
            });
            if !chromium {
                continue;
            }
            let owner = self.app_owner(bundle);
            let mut leaves = rules::ELECTRON_LEAVES.to_vec();
            if matches!(
                bundle.as_str(),
                "com.microsoft.VSCode" | "com.microsoft.VSCodeInsiders"
            ) {
                leaves.extend(["CachedData", "CachedExtensionVSIXs"]);
            }
            for leaf in leaves {
                let path = directory.join(leaf);
                if !fs::symlink_metadata(&path).is_ok_and(|m| m.is_dir() && !m.is_symlink()) {
                    continue;
                }
                self.add(
                    path,
                    owner.clone(),
                    app.app.name.clone(),
                    CleanupReason::CacheDir,
                    None,
                )?;
            }
        }
        Ok(())
    }
    fn app_owner(&self, bundle: &str) -> Owner {
        let mut owner = Owner::app(bundle, self.apps);
        owner.known = owner.app_id.is_some() || rules::browser_bundle(bundle);
        if bundle == "com.apple.dt.Xcode" {
            owner.processes = rules::TOOLS[0]
                .processes
                .iter()
                .map(|s| (*s).into())
                .collect();
        }
        owner
    }
    fn title(&self, owner: &Owner, fallback: &str) -> String {
        owner
            .app_id
            .as_ref()
            .and_then(|id| self.apps.iter().find(|a| a.app.id == *id))
            .map(|a| a.app.name.clone())
            .unwrap_or_else(|| owner.display_title(self.activity, fallback))
    }
    fn logs(&mut self) -> Result<(), AppError> {
        let root = self.engine.uninstall.roots.home.join("Library/Logs");
        let mut pending = self.children(&root)?;
        while let Some(path) = pending.pop() {
            self.check()?;
            if rules::never_touch(&self.engine.uninstall.roots.home, &path)
                || protect::is_protected(&self.rules, &path, None)
            {
                continue;
            }
            let Ok(meta) = fs::symlink_metadata(&path) else {
                self.result
                    .incomplete(&path, SkippedLocationReason::Permission);
                continue;
            };
            if meta.is_symlink() {
                continue;
            }
            if meta.is_dir() {
                pending.extend(self.children(&path)?);
            } else if meta.is_file() {
                let parent = path
                    .strip_prefix(&root)
                    .ok()
                    .and_then(|r| r.components().next())
                    .map(|s| s.as_os_str().to_string_lossy().into_owned())
                    .unwrap_or_default();
                let owner = if self
                    .apps
                    .iter()
                    .any(|a| a.app.bundle_id.as_deref() == Some(&parent))
                {
                    self.app_owner(&parent)
                } else {
                    Owner {
                        known: true,
                        ..Default::default()
                    }
                };
                self.add(path, owner, "Logs".into(), CleanupReason::LogFile, Some(7))?;
            }
        }
        let root = self.engine.uninstall.roots.home.join(".npm/_logs");
        let mut owner = Owner::tool(&["npm", "npx"]);
        owner.arguments = Some(rules::ArgumentMatch::Npm);
        owner.tool_key = Some("npm".into());
        for path in self.children(&root)? {
            if fs::symlink_metadata(&path).is_ok_and(|m| m.is_file()) {
                self.add(
                    path,
                    owner.clone(),
                    "npm".into(),
                    CleanupReason::LogFile,
                    Some(7),
                )?;
            }
        }
        Ok(())
    }
    fn browser_caches(&mut self) -> Result<(), AppError> {
        let home = self.engine.uninstall.roots.home.clone();
        let mut updater = Owner::app("com.google.Chrome", self.apps);
        updater.processes.push("GoogleUpdater".into());
        self.add(
            home.join("Library/Application Support/Google/GoogleUpdater/crx_cache"),
            updater,
            "Chrome".into(),
            CleanupReason::BrowserCache,
            None,
        )?;
        for browser in rules::BROWSERS {
            let owner = Owner::app(browser.bundle, self.apps);
            for base in [
                home.join("Library/Application Support")
                    .join(browser.product),
                home.join("Library/Caches").join(browser.product),
            ] {
                let mut profiles = vec![base.clone()];
                profiles.extend(self.children(&base)?.into_iter().filter(|p| {
                    p.file_name().is_some_and(|n| {
                        let n = n.to_string_lossy();
                        n == "Default"
                            || n.starts_with("Profile ")
                            || n == "Guest Profile"
                            || n == "System Profile"
                    })
                }));
                for profile in profiles {
                    for leaf in rules::BROWSER_LEAVES {
                        self.add(
                            profile.join(leaf),
                            owner.clone(),
                            browser.title.into(),
                            CleanupReason::BrowserCache,
                            None,
                        )?;
                    }
                }
            }
        }
        for base in [
            home.join("Library/Caches/Firefox/Profiles"),
            home.join("Library/Application Support/Firefox/Profiles"),
        ] {
            for profile in self.children(&base)? {
                self.add(
                    profile.join("cache2"),
                    Owner::app("org.mozilla.firefox", self.apps),
                    "Firefox".into(),
                    CleanupReason::BrowserCache,
                    None,
                )?;
            }
        }
        let safari = home.join("Library/Caches/com.apple.Safari");
        if !self.fda(&safari) {
            self.add(
                safari,
                Owner::app("com.apple.Safari", self.apps),
                "Safari".into(),
                CleanupReason::BrowserCache,
                None,
            )?;
        }
        let sandbox = home.join("Library/Containers/com.apple.Safari/Data/Library/Caches");
        if !self.fda(&sandbox) {
            for relative in [
                "com.apple.Safari",
                "com.apple.WebKit.Networking",
                "WebKit/NetworkCache",
            ] {
                self.add(
                    sandbox.join(relative),
                    Owner::app("com.apple.Safari", self.apps),
                    "Safari".into(),
                    CleanupReason::BrowserCache,
                    None,
                )?;
            }
        }
        Ok(())
    }
    fn dev_caches(&mut self) -> Result<(), AppError> {
        let home = self.engine.uninstall.roots.home.clone();
        for rule in rules::TOOLS {
            self.add(
                home.join(rule.path),
                if ["Xcode", "CoreSimulator"].contains(&rule.title) {
                    let mut owner = Owner::app("com.apple.dt.Xcode", self.apps);
                    owner.processes = rule.processes.iter().map(|s| (*s).into()).collect();
                    owner.tool_only = true;
                    owner.tool_key = Some(rule.title.into());
                    owner
                } else {
                    let mut owner = Owner::tool(rule.processes);
                    owner.tool_key = Some(rule.title.into());
                    owner.arguments = rule.arguments;
                    owner
                },
                rule.title.into(),
                CleanupReason::ToolCache,
                None,
            )?;
        }
        for rule in rules::DOWNLOAD_TOOLS {
            let mut owner = Owner::tool(rule.processes);
            owner.tool_key = Some(rule.title.into());
            owner.arguments = rule.arguments;
            self.add(
                home.join(rule.path),
                owner,
                rule.title.into(),
                CleanupReason::ToolDownloadCache,
                None,
            )?;
        }
        self.device_support()?;
        if let Some(root) = self.engine.processes.user_cache_dir()? {
            let mut owner = Owner::tool(rules::TOOLS[0].processes);
            owner.processes.push("clang".into());
            owner.tool_key = Some("Clang".into());
            for leaf in rules::CLANG_LEAVES {
                let path = root.join(leaf);
                let mut owner = owner.clone();
                // Judged inside clang's own folder: the rest of the OS cache folder stays out of reach.
                owner.external_cache = path.parent().map(Path::to_path_buf);
                self.add(path, owner, "Clang".into(), CleanupReason::ToolCache, None)?;
            }
        }
        Ok(())
    }
    fn device_support(&mut self) -> Result<(), AppError> {
        for platform in rules::DEVICE_PLATFORMS {
            let root = self
                .engine
                .uninstall
                .roots
                .home
                .join(format!("Library/Developer/Xcode/{platform} DeviceSupport"));
            let mut groups = BTreeMap::<String, Vec<_>>::new();
            for path in self.children(&root)? {
                let Ok(metadata) = fs::symlink_metadata(&path) else {
                    continue;
                };
                if !metadata.is_dir() || metadata.is_symlink() {
                    continue;
                }
                let Some((model, version)) = path
                    .file_name()
                    .and_then(|n| n.to_str())
                    .and_then(|n| rules::device_version(n, platform))
                else {
                    continue;
                };
                let modified = metadata
                    .modified()?
                    .duration_since(std::time::UNIX_EPOCH)
                    .unwrap_or_default();
                groups
                    .entry(model)
                    .or_default()
                    .push((modified, version, path));
            }
            for (model, mut versions) in groups {
                versions.sort();
                let latest = versions.last().map(|(_, _, p)| p.clone());
                let mut owner = Owner::app("com.apple.dt.Xcode", self.apps);
                owner.processes = rules::TOOLS[0]
                    .processes
                    .iter()
                    .map(|s| (*s).into())
                    .collect();
                owner.tool_only = true;
                owner.tool_key = Some("Xcode".into());
                owner.group_key = Some(format!("device-support:{platform}:{model}"));
                owner.group_subtitle = Some((*platform).into());
                for (_, _, path) in versions {
                    let reason = if Some(&path) == latest.as_ref() {
                        CleanupReason::DeviceSupportLatest
                    } else {
                        CleanupReason::DeviceSupport
                    };
                    self.add(path, owner.clone(), model.clone(), reason, None)?;
                }
            }
        }
        Ok(())
    }
}
impl Collector<'_> {
    /// Folders inside Documents, Desktop or Downloads are protected by macOS: reading them without Full Disk Access
    /// would prompt the user mid-scan. They are skipped and reported instead.
    fn guarded(&mut self, path: &Path) -> bool {
        let home = self.engine.uninstall.roots.home.clone();
        ["Documents", "Desktop", "Downloads"]
            .iter()
            .any(|folder| path.starts_with(home.join(folder)))
            && self.fda(path)
    }
    /// Plain entries in the per-user temp folder that nobody has touched for three days and nothing holds open.
    fn temp(&mut self) -> Result<(), AppError> {
        let Some(root) = self.engine.processes.user_temp_dir()? else {
            return Ok(());
        };
        for path in self.children(&root)? {
            let name = path.file_name().unwrap_or_default().to_string_lossy();
            // System services keep live state here; only ordinary leftovers are offered.
            if name.starts_with("com.apple.")
                || name.starts_with('.')
                || name == "TemporaryItems"
                || name.starts_with("launchd")
            {
                continue;
            }
            let mut owner = Owner::tool(&[]);
            owner.group_key = Some("temp".into());
            owner.external_cache = Some(root.clone());
            self.add(path, owner, "temp".into(), CleanupReason::TempFile, Some(3))?;
        }
        Ok(())
    }
    /// Disk images and installer packages in Downloads and on the Desktop; those whose app is installed are picked.
    fn installers(&mut self) -> Result<(), AppError> {
        let home = self.engine.uninstall.roots.home.clone();
        let names: Vec<String> = self.apps.iter().map(|a| a.app.name.clone()).collect();
        let names: Vec<&str> = names.iter().map(String::as_str).collect();
        for folder in ["Downloads", "Desktop"] {
            let root = home.join(folder);
            if self.guarded(&root) {
                continue;
            }
            for path in self.children(&root)? {
                let Some(extension) = path
                    .extension()
                    .and_then(|e| e.to_str())
                    .map(str::to_ascii_lowercase)
                else {
                    continue;
                };
                if !rules::INSTALLER_EXTENSIONS.contains(&extension.as_str()) {
                    continue;
                }
                let stem = path.file_stem().unwrap_or_default().to_string_lossy();
                let reason = if rules::installer_matches(&stem, &names) {
                    CleanupReason::InstallerInstalled
                } else {
                    CleanupReason::Installer
                };
                let mut owner = Owner::tool(&[]);
                owner.group_key = Some("installers".into());
                self.add(path, owner, "installers".into(), reason, None)?;
            }
        }
        Ok(())
    }
    /// Generated folders of projects under the project roots. Projects touched within the last month keep theirs:
    /// listed with the size, never offered.
    fn project_artifacts(&mut self) -> Result<(), AppError> {
        for root in self.project_roots.clone() {
            if self.guarded(&root) {
                continue;
            }
            self.walk_projects(&root, 0)?;
        }
        Ok(())
    }
    fn walk_projects(&mut self, dir: &Path, depth: usize) -> Result<(), AppError> {
        if depth > rules::PROJECT_MAX_DEPTH {
            return Ok(());
        }
        let children = self.children(dir)?;
        let names: BTreeSet<String> = children
            .iter()
            .filter_map(|p| p.file_name().map(|n| n.to_string_lossy().into_owned()))
            .collect();
        let artifacts: BTreeSet<&str> = rules::PROJECT_ARTIFACTS
            .iter()
            .filter(|(marker, _)| names.contains(*marker))
            .flat_map(|(_, dirs)| dirs.iter().copied())
            .collect();
        if !artifacts.is_empty() {
            let home = self.engine.uninstall.roots.home.clone();
            let recent = project_touched(dir, &children, &artifacts)
                > uninstall::now() - rules::PROJECT_RECENT_DAYS * 86_400_000;
            let title = dir
                .file_name()
                .unwrap_or_default()
                .to_string_lossy()
                .into_owned();
            for name in &artifacts {
                let path = dir.join(name);
                if !fs::symlink_metadata(&path).is_ok_and(|m| m.is_dir() && !m.is_symlink()) {
                    continue;
                }
                let mut owner = Owner::tool(&[]);
                owner.group_key = Some(format!("project:{}", dir.display()));
                owner.group_subtitle = Some(match dir.strip_prefix(&home) {
                    Ok(relative) => format!("~/{}", relative.display()),
                    Err(_) => dir.display().to_string(),
                });
                if recent {
                    self.add_skipped(
                        path,
                        owner,
                        title.clone(),
                        CleanupReason::BuildArtifact,
                        SkippedGroupReason::Recent,
                    )?;
                } else {
                    self.add(
                        path,
                        owner,
                        title.clone(),
                        CleanupReason::BuildArtifact,
                        None,
                    )?;
                }
            }
        }
        for child in children {
            let name = child
                .file_name()
                .unwrap_or_default()
                .to_string_lossy()
                .into_owned();
            // Plain folders only: not hidden ones, not generated output, not app or framework bundles.
            if name.starts_with('.')
                || artifacts.contains(name.as_str())
                || name == "node_modules"
                || name.ends_with(".app")
                || name.ends_with(".framework")
            {
                continue;
            }
            if fs::symlink_metadata(&child).is_ok_and(|m| m.is_dir() && !m.is_symlink()) {
                self.walk_projects(&child, depth + 1)?;
            }
        }
        Ok(())
    }
    /// Each top-level item in the Trash. Unchecked by default: emptying it cannot be undone.
    fn trash(&mut self) -> Result<(), AppError> {
        let root = self.engine.uninstall.roots.home.join(".Trash");
        if self.fda(&root) {
            return Ok(());
        }
        for path in self.children(&root)? {
            // Finder's own .DS_Store keeps the Trash window's layout.
            if path
                .file_name()
                .is_some_and(|n| n.to_string_lossy().starts_with('.'))
            {
                continue;
            }
            let mut owner = Owner::tool(&[]);
            owner.group_key = Some("trash".into());
            owner.external_cache = Some(root.clone());
            self.add(path, owner, "trash".into(), CleanupReason::Trash, None)?;
        }
        Ok(())
    }
    /// iPhone and iPad backups (named by device), and Xcode archives (named by app). Personal, never preselected.
    fn device_backups(&mut self) -> Result<(), AppError> {
        let home = self.engine.uninstall.roots.home.clone();
        let backups = home.join("Library/Application Support/MobileSync/Backup");
        if !self.fda(&backups) {
            for path in self.children(&backups)? {
                if !fs::symlink_metadata(&path).is_ok_and(|m| m.is_dir() && !m.is_symlink()) {
                    continue;
                }
                let info = plist_dictionary(&path.join("Info.plist"));
                let device = plist_text(&info, "Device Name")
                    .or_else(|| plist_text(&info, "Display Name"))
                    .unwrap_or_else(|| {
                        path.file_name()
                            .unwrap_or_default()
                            .to_string_lossy()
                            .into_owned()
                    });
                let mut owner = Owner::tool(&[]);
                owner.group_key = Some(format!("backup:{device}"));
                owner.group_subtitle = plist_text(&info, "Product Type");
                owner.external_cache = Some(backups.clone());
                self.add(path, owner, device, CleanupReason::DeviceBackup, None)?;
            }
        }
        let archives = home.join("Library/Developer/Xcode/Archives");
        for day in self.children(&archives)? {
            for path in self.children(&day)? {
                if path.extension().and_then(|e| e.to_str()) != Some("xcarchive") {
                    continue;
                }
                let info = plist_dictionary(&path.join("Info.plist"));
                let app = plist_text(&info, "Name").unwrap_or_else(|| {
                    path.file_stem()
                        .unwrap_or_default()
                        .to_string_lossy()
                        .into_owned()
                });
                let mut owner = Owner::app("com.apple.dt.Xcode", self.apps);
                owner.processes = rules::TOOLS[0]
                    .processes
                    .iter()
                    .map(|s| (*s).into())
                    .collect();
                owner.tool_only = true;
                owner.tool_key = Some("Xcode".into());
                owner.group_key = Some(format!("archive:{app}"));
                owner.group_subtitle = Some("Xcode".into());
                self.add(path, owner, app, CleanupReason::XcodeArchive, None)?;
            }
        }
        Ok(())
    }
}
/// Configured project roots (`~/...` or absolute, inside home) plus the usual places that exist, without duplicates
/// (APFS is case-insensitive, so ~/code and ~/Code can be the same folder) and without roots inside other roots.
fn project_roots(home: &Path, configured: &[String]) -> Vec<PathBuf> {
    let configured = configured.iter().filter_map(|root| {
        let root = root.trim();
        if let Some(rest) = root.strip_prefix("~/") {
            Some(home.join(rest))
        } else if Path::new(root).is_absolute() {
            Some(PathBuf::from(root))
        } else {
            None
        }
    });
    let mut seen = BTreeSet::new();
    let mut roots: Vec<PathBuf> = configured
        .chain(rules::PROJECT_ROOTS.iter().map(|root| home.join(root)))
        .filter(|root| root.starts_with(home) && root != home)
        .filter(|root| {
            use std::os::unix::fs::MetadataExt;
            fs::symlink_metadata(root)
                .ok()
                .filter(|m| m.is_dir() && !m.is_symlink())
                .is_some_and(|m| seen.insert((m.dev(), m.ino())))
        })
        .collect();
    roots.sort();
    let nested: Vec<PathBuf> = roots.clone();
    roots.retain(|root| {
        !nested
            .iter()
            .any(|other| other != root && root.starts_with(other))
    });
    roots
}
/// When a project was last worked on: its top-level files and folders (not generated output) and Git's index and
/// HEAD log, in milliseconds since the epoch.
fn project_touched(dir: &Path, children: &[PathBuf], artifacts: &BTreeSet<&str>) -> i64 {
    let millis = |path: &Path| {
        fs::symlink_metadata(path)
            .and_then(|m| m.modified())
            .ok()
            .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
            .map_or(0, |d| d.as_millis().min(i64::MAX as u128) as i64)
    };
    children
        .iter()
        .filter(|p| {
            !p.file_name()
                .is_some_and(|n| artifacts.contains(n.to_string_lossy().as_ref()))
        })
        .map(|p| millis(p))
        .chain(
            [".git/index", ".git/logs/HEAD", ".git/FETCH_HEAD"]
                .iter()
                .map(|f| millis(&dir.join(f))),
        )
        .max()
        .unwrap_or(0)
}
fn plist_dictionary(path: &Path) -> plist::Dictionary {
    plist::Value::from_file(path)
        .ok()
        .and_then(|v| v.into_dictionary())
        .unwrap_or_default()
}
fn plist_text(info: &plist::Dictionary, key: &str) -> Option<String> {
    info.get(key)
        .and_then(|v| v.as_string())
        .map(str::trim)
        .filter(|s| !s.is_empty() && !s.chars().any(char::is_control))
        .map(String::from)
}

impl Engine {
    pub async fn scan(
        self: &Arc<Self>,
        request: String,
        categories: Option<Vec<CleanupCategory>>,
        permissions: Permissions,
        core: Option<Arc<DesktopCore>>,
    ) -> Result<CleanupReport, AppError> {
        if request.is_empty() || request.len() > 128 || request.chars().any(char::is_control) {
            return Err(AppError::new("E_INVALID_ARG", "request_id"));
        }
        let categories = categories.unwrap_or_else(|| rules::AVAILABLE.to_vec());
        if categories.iter().any(|c| !rules::AVAILABLE.contains(c)) {
            return Err(AppError::new(
                "E_INVALID_ARG",
                "cleanup_category_not_available",
            ));
        }
        let mut categories: BTreeSet<_> = categories.into_iter().collect();
        let flag = Arc::new(AtomicBool::new(false));
        {
            let mut requests = self.requests.lock().map_err(poison)?;
            if requests.contains_key(&request) {
                return Err(AppError::new("E_INVALID_ARG", "request_id_busy"));
            }
            if requests.len() >= 4 {
                return Err(AppError::new("E_BUSY", "cleanup_scan_limit"));
            }
            requests.insert(
                request.clone(),
                (
                    flag.clone(),
                    format!("cleanup-leftovers:{}", uuid::Uuid::now_v7()),
                ),
            );
        }
        let _guard = RequestGuard {
            engine: self.clone(),
            id: request.clone(),
            flag: flag.clone(),
        };
        let engine = self.uninstall.clone();
        let apps = crate::blocking(move || {
            engine.list()?;
            Ok(engine
                .apps
                .lock()
                .map_err(poison)?
                .values()
                .cloned()
                .collect::<Vec<_>>())
        })
        .await?;
        let apps = Arc::new(apps);
        let home = &self.uninstall.roots.home;
        let dev_detected = rules::TOOLS
            .iter()
            .chain(rules::DOWNLOAD_TOOLS)
            .any(|rule| {
                let path = home.join(rule.path);
                uninstall::rules::parents_safe(&path)
                    && fs::symlink_metadata(path).is_ok_and(|m| m.is_dir())
            })
            || rules::DEVICE_PLATFORMS.iter().any(|platform| {
                fs::symlink_metadata(
                    home.join(format!("Library/Developer/Xcode/{platform} DeviceSupport")),
                )
                .is_ok_and(|m| m.is_dir() && !m.is_symlink())
            })
            || self
                .processes
                .user_cache_dir()
                .map(|root| {
                    root.is_some_and(|root| {
                        rules::CLANG_LEAVES.iter().any(|leaf| {
                            fs::symlink_metadata(root.join(leaf))
                                .is_ok_and(|m| m.is_dir() && !m.is_symlink())
                        })
                    })
                })
                .unwrap_or(true); // Let this collector report discovery failures without failing the whole scan.
        if !dev_detected {
            categories.remove(&CleanupCategory::DevCaches);
        }
        let progress = Arc::new(super::progress::Progress::new(
            request.clone(),
            categories.len(),
            self.uninstall.sink.clone(),
        ));
        let time = uninstall::now();
        let volume = volume(&self.uninstall.roots.home)?;
        let activity_engine = self.clone();
        let activity =
            crate::blocking(move || Ok(super::owner::Activity::read(&activity_engine))).await?;
        let mut jobs = tokio::task::JoinSet::new();
        let configured = core
            .as_ref()
            .and_then(|core| core.current_settings().ok())
            .map(|settings| settings.cleanup_project_roots)
            .unwrap_or_default();
        let project_roots = project_roots(&self.uninstall.roots.home, &configured);
        for key in rules::AVAILABLE
            .iter()
            .copied()
            .filter(|c| categories.contains(c))
        {
            let project_roots = project_roots.clone();
            let engine = self.clone();
            let apps = apps.clone();
            let request = request.clone();
            let flag = flag.clone();
            let core = core.clone();
            let progress = progress.clone();
            let activity = activity.clone();
            jobs.spawn(async move {
                let started = Instant::now();
                let result = match key {
                    CleanupCategory::Leftovers => {
                        engine
                            .collect_leftovers(
                                &request,
                                permissions,
                                &apps,
                                flag,
                                progress,
                                activity,
                            )
                            .await
                    }
                    CleanupCategory::Homebrew => {
                        engine.collect_brew(core, flag, progress, activity).await
                    }
                    CleanupCategory::Docker => {
                        crate::blocking(move || {
                            engine.collect_docker(&apps, &flag, progress, &activity)
                        })
                        .await
                    }
                    _ => {
                        crate::blocking(move || {
                            let deadline = Instant::now() + engine.category_timeout;
                            let mut collector = Collector {
                                engine: &engine,
                                apps: &apps,
                                rules: engine.protected_list()?,
                                permissions,
                                progress,
                                flag: &flag,
                                deadline,
                                result: Collected::new(key),
                                activity: &activity,
                                pending: vec![],
                                project_roots,
                            };
                            collector.progress(None);
                            let result = match key {
                                CleanupCategory::AppCaches => collector.app_caches(),
                                CleanupCategory::Logs => collector.logs(),
                                CleanupCategory::BrowserCaches => collector.browser_caches(),
                                CleanupCategory::DevCaches => collector.dev_caches(),
                                CleanupCategory::Temp => collector.temp(),
                                CleanupCategory::Installers => collector.installers(),
                                CleanupCategory::ProjectArtifacts => collector.project_artifacts(),
                                CleanupCategory::Trash => collector.trash(),
                                CleanupCategory::DeviceBackups => collector.device_backups(),
                                _ => unreachable!(),
                            };
                            if let Err(error) = result {
                                if error.code == "E_CANCELED" {
                                    return Err(error);
                                }
                                let home = engine.uninstall.roots.home.clone();
                                collector.result.incomplete(
                                    &home,
                                    if error.code == "E_TIMEOUT" {
                                        SkippedLocationReason::Timeout
                                    } else {
                                        SkippedLocationReason::Permission
                                    },
                                );
                            }
                            collector.measure_candidates()?;
                            collector.progress(None);
                            Ok(collector.result)
                        })
                        .await
                    }
                };
                (
                    key,
                    result,
                    started.elapsed().as_millis().min(u64::MAX as u128) as u64,
                )
            });
        }
        let mut entry = Entry {
            category_millis: BTreeMap::new(),
            report: CleanupReport {
                report_id: uuid::Uuid::now_v7().to_string(),
                scanned_at: time,
                expires_at: 0,
                volume,
                categories: vec![],
                incomplete: vec![],
            },
            items: BTreeMap::new(),
        };
        let mut errors = vec![];
        // Drain all collectors even after cancellation; never leave a detached filesystem scan running.
        while let Some(result) = jobs.join_next().await {
            match result {
                Ok((key, Ok(collected), millis)) => {
                    entry.category_millis.insert(key, millis);
                    progress.finish(key, collected.category.bytes)?;
                    entry.report.categories.push(collected.category);
                    entry.report.incomplete.extend(collected.incomplete);
                    entry.items.extend(collected.items);
                }
                Ok((_, Err(error), _)) => errors.push(error),
                Err(error) => errors.push(
                    AppError::new("E_UNKNOWN", "cleanup_collector_join")
                        .with_detail(error.to_string()),
                ),
            }
        }
        if flag.load(Ordering::Acquire) {
            return Err(AppError::new("E_CANCELED", "cleanup_canceled"));
        }
        if let Some(error) = errors.into_iter().next() {
            return Err(error);
        }
        entry
            .report
            .categories
            .sort_by_key(|c| rules::AVAILABLE.iter().position(|key| key == &c.key));
        deduplicate(&mut entry);
        let _operation = self.operation.lock().map_err(poison)?;
        let protected = self.protected_list()?;
        entry
            .items
            .retain(|_, item| !protect::entry_is_protected(self, &protected, item));
        reconcile(&mut entry);
        entry
            .report
            .categories
            .retain(|c| !c.groups.is_empty() || !c.skipped.is_empty() || !c.complete);
        entry.report.scanned_at = uninstall::now();
        entry.report.expires_at = entry.report.scanned_at + 600_000;
        let mut reports = self.reports.lock().map_err(poison)?;
        reports.retain(|_, r| r.report.expires_at > uninstall::now());
        self.consumed
            .lock()
            .map_err(poison)?
            .retain(|id| reports.contains_key(id));
        if reports.len() >= 16
            && let Some(id) = reports
                .values()
                .min_by_key(|e| e.report.scanned_at)
                .map(|e| e.report.report_id.clone())
        {
            reports.remove(&id);
        }
        self.save_summary(&entry.report)?;
        let report = entry.report.clone();
        reports.insert(report.report_id.clone(), entry);
        Ok(report)
    }
    /// Docker's reclaimable space as Docker itself reports it. Nothing is listed when Docker is not installed or the
    /// user keeps it; when it is installed but not running, one line says so. Every prune starts unchecked.
    fn collect_docker(
        &self,
        apps: &[CachedApp],
        flag: &AtomicBool,
        progress: Arc<super::progress::Progress>,
        activity: &Result<super::owner::Activity, AppError>,
    ) -> Result<Collected, AppError> {
        let mut result = Collected::new(CleanupCategory::Docker);
        progress.update(CleanupCategory::Docker, None, 0)?;
        let home = &self.uninstall.roots.home;
        if protect::is_protected(&self.protected_list()?, &home.join(".docker"), None) {
            return Ok(result);
        }
        let Some(docker) = self.processes.docker(home) else {
            return Ok(result);
        };
        let mut owner = Owner::tool(&[]);
        owner.group_key = Some("docker".into());
        owner.tool_key = Some("Docker".into());
        // Docker Desktop or OrbStack: its icon, and something the user can open.
        let app = apps.iter().find(|a| {
            matches!(
                a.app.bundle_id.as_deref(),
                Some("com.docker.docker" | "dev.kdrag0n.MacVirt")
            )
        });
        let processes = activity
            .as_ref()
            .ok()
            .filter(|a| a.processes_complete)
            .map(|a| a.processes.as_slice());
        if !super::docker::engine_running(processes) {
            // "Open it and scan again" needs an app to open; a bare CLI (remote contexts, Colima stopped) stays quiet.
            if app.is_some() {
                result.skip(
                    &owner,
                    "Docker".into(),
                    None,
                    SkippedGroupReason::ToolStopped,
                    vec![],
                );
            }
            return Ok(result);
        }
        let (endpoint, report) = match super::docker::scan(
            &docker,
            home,
            self.category_timeout.min(Duration::from_secs(30)),
            flag,
        ) {
            Ok(report) => report,
            Err(error) if error.code == "E_CANCELED" => return Err(error),
            Err(_) => {
                result.skip(
                    &owner,
                    "Docker".into(),
                    None,
                    SkippedGroupReason::ToolStopped,
                    vec![],
                );
                return Ok(result);
            }
        };
        let mut items = vec![];
        for (reason, args) in super::docker::PRUNES {
            let bytes = report.get(*reason).unwrap_or_default().reclaimable;
            if bytes == 0 {
                continue;
            }
            let item = CleanupItem {
                id: uuid::Uuid::now_v7().to_string(),
                path: String::new(),
                kind: CleanupItemKind::Command,
                command: Some(format!("docker {}", args.join(" "))),
                size_bytes: Some(bytes),
                kept_open_files: 0,
                modified_at: None,
                reason: *reason,
                selected: false,
                delete_mode: DeleteMode::Delete,
                needs_admin: false,
                note: None,
            };
            result.items.insert(
                item.id.clone(),
                EntryItem {
                    docker_endpoint: Some(endpoint.clone()),
                    item: item.clone(),
                    owner: owner.clone(),
                    title: "Docker".into(),
                    tree: None,
                    action: ItemAction::ToolCommand,
                },
            );
            items.push(item);
        }
        if report.volumes.reclaimable > 0 {
            result.skip(
                &owner,
                "Docker".into(),
                Some(report.volumes.reclaimable),
                SkippedGroupReason::Volumes,
                vec![],
            );
        }
        if !items.is_empty() {
            result.category.bytes = items.iter().filter_map(|i| i.size_bytes).sum();
            result.category.groups.push(CleanupGroup {
                id: "docker".into(),
                title: "Docker".into(),
                subtitle: None,
                app_id: app.map(|a| a.app.id.clone()),
                bundle_id: app.and_then(|a| a.app.bundle_id.clone()),
                items,
            });
        }
        progress.update(CleanupCategory::Docker, None, result.category.bytes)?;
        Ok(result)
    }
    async fn collect_brew(
        &self,
        core: Option<Arc<DesktopCore>>,
        flag: Arc<AtomicBool>,
        progress: Arc<super::progress::Progress>,
        activity: Result<super::owner::Activity, AppError>,
    ) -> Result<Collected, AppError> {
        let mut result = Collected::new(CleanupCategory::Homebrew);
        progress.update(CleanupCategory::Homebrew, None, 0)?;
        // brew itself has no interactive owner; skip while another brew process exists, and fail closed if process enumeration fails.
        let mut owner = Owner::tool(&["brew", "brew.rb"]);
        owner.arguments = Some(rules::ArgumentMatch::Brew);
        if let Some(reason) = owner.status_in(&activity) {
            let path = self.uninstall.roots.home.join("Library/Caches/Homebrew");
            let protected = self.protected_list()?;
            if protect::is_protected(&protected, &path, None) {
                return Ok(result);
            }
            let until = Instant::now() + self.category_timeout;
            let (bytes, incomplete) = crate::blocking(move || {
                tree::measure_filtered(&path, &flag, until, |p| {
                    protect::is_protected(&protected, p, None)
                })
            })
            .await?;
            if let Some(reason) = incomplete {
                result.incomplete(
                    &self.uninstall.roots.home.join("Library/Caches/Homebrew"),
                    reason,
                );
            }
            let processes = owner.blocking_processes(&activity, Path::new(""), reason);
            result.skip(&owner, "Homebrew".into(), Some(bytes), reason, processes);
            return Ok(result);
        }
        let Some(core) = core else {
            return Ok(result);
        };
        let mut interval = tokio::time::interval(Duration::from_millis(25));
        let future = async {
            if core.brew_info().await.is_err() {
                return Ok(None);
            }
            crate::maintenance::estimate(core).await.map(Some)
        };
        tokio::pin!(future);
        let deadline = tokio::time::sleep(self.category_timeout);
        tokio::pin!(deadline);
        let estimate = loop {
            tokio::select! {
                result = &mut future => break Some(result),
                _ = &mut deadline => break None,
                _ = interval.tick() => if flag.load(Ordering::Acquire) { return Err(AppError::new("E_CANCELED", "cleanup_canceled")); },
            }
        };
        match estimate {
            Some(Ok(Some(estimate))) => {
                let rules = self.protected_list()?;
                if protect::is_protected(
                    &rules,
                    &self.uninstall.roots.home.join("Library/Caches/Homebrew"),
                    None,
                ) || (estimate.file_count as usize > estimate.items.len() && !rules.is_empty())
                {
                    return Ok(result);
                }

                // A fixed brew command cannot honor per-path exclusions. Suppress the whole command if any estimated path is protected or forbidden.
                if estimate.items.iter().any(|i| {
                    protect::is_protected(&rules, Path::new(&i.path), None)
                        || (Path::new(&i.path).starts_with(&self.uninstall.roots.home)
                            && rules::never_touch(&self.uninstall.roots.home, Path::new(&i.path)))
                }) {
                    return Ok(result);
                }
                let item = CleanupItem {
                    id: uuid::Uuid::now_v7().to_string(),
                    path: String::new(),
                    kind: CleanupItemKind::Command,
                    command: Some("brew cleanup --prune=all".into()),
                    size_bytes: Some(estimate.bytes),
                    kept_open_files: 0,
                    modified_at: None,
                    reason: CleanupReason::BrewCleanup,
                    selected: true,
                    delete_mode: DeleteMode::Delete,
                    needs_admin: false,
                    note: None,
                };
                result.category.bytes = estimate.bytes;
                result.category.groups.push(CleanupGroup {
                    id: "homebrew".into(),
                    title: "Homebrew".into(),
                    subtitle: None,
                    app_id: None,
                    bundle_id: None,
                    items: vec![item.clone()],
                });
                result.items.insert(
                    item.id.clone(),
                    EntryItem {
                        docker_endpoint: None,
                        item,
                        owner,
                        title: "Homebrew".into(),
                        tree: None,
                        action: ItemAction::BrewStep,
                    },
                );
            }
            Some(Ok(None)) => {}
            Some(Err(_)) => result.incomplete(
                &self.uninstall.roots.home.join("Library/Caches/Homebrew"),
                SkippedLocationReason::Permission,
            ),
            None => result.incomplete(
                &self.uninstall.roots.home.join("Library/Caches/Homebrew"),
                SkippedLocationReason::Timeout,
            ),
        }
        progress.update(CleanupCategory::Homebrew, None, result.category.bytes)?;
        Ok(result)
    }
    async fn collect_leftovers(
        self: &Arc<Self>,
        request: &str,
        permissions: Permissions,
        apps: &[CachedApp],
        flag: Arc<AtomicBool>,
        progress: Arc<super::progress::Progress>,
        activity: Result<super::owner::Activity, AppError>,
    ) -> Result<Collected, AppError> {
        let until = Instant::now() + self.category_timeout;
        let mut result = Collected::new(CleanupCategory::Leftovers);
        let private = self.leftover_request(request)?;
        progress.update(CleanupCategory::Leftovers, None, 0)?;
        let future = self.uninstall.scan_residuals(private.clone(), permissions);
        tokio::pin!(future);
        let deadline = tokio::time::sleep(self.category_timeout);
        tokio::pin!(deadline);
        let mut interval = tokio::time::interval(Duration::from_millis(25));
        let mut stopped = false;
        let report = loop {
            tokio::select! {
                report = &mut future => break report,
                _ = &mut deadline, if !stopped => { stopped = true; self.uninstall.cancel(&private)?; },
                _ = interval.tick() => if flag.load(Ordering::Acquire) { self.uninstall.cancel(&private)?; },
            }
        };
        if flag.load(Ordering::Acquire) {
            return Err(AppError::new("E_CANCELED", "cleanup_canceled"));
        }
        if stopped {
            result.incomplete(
                &self.uninstall.roots.home.join("Library"),
                SkippedLocationReason::Timeout,
            );
            return Ok(result);
        }
        let (report, plan) = report?;
        let engine = self.clone();
        let apps = apps.to_vec();
        crate::blocking(move || {
            engine.convert_leftovers(
                (report, plan),
                result,
                &progress,
                &apps,
                (&flag, &activity),
                until,
            )
        })
        .await
    }
    fn convert_leftovers(
        &self,
        residual: (uninstall::leftovers::ResidualReport, uninstall::PlanEntry),
        mut result: Collected,
        progress: &super::progress::Progress,
        apps: &[CachedApp],
        control: (&AtomicBool, &Result<super::owner::Activity, AppError>),
        until: Instant,
    ) -> Result<Collected, AppError> {
        let (flag, activity) = control;
        let (report, plan) = residual;
        let settings = self.uninstall.settings.read().map_err(poison)?.clone();
        let protected = self.protected_list()?;
        // Only user locations are in P0; do not surface system paths as actionable.
        result.incomplete = report
            .incomplete
            .into_iter()
            .filter(|i| Path::new(&i.path).starts_with(&self.uninstall.roots.home))
            .collect();
        result.category.complete = result.incomplete.is_empty();
        let engine = self.uninstall.clone();
        for group in report.groups {
            let mut owner = group
                .bundle_id
                .as_deref()
                .map(|id| Owner::app(id, apps))
                .unwrap_or_default();
            owner.known = true;
            for old in group.items {
                let path = PathBuf::from(&old.path);
                if owner.protected
                    || old.needs_admin
                    || !path.starts_with(&engine.roots.home)
                    || rules::never_touch(&engine.roots.home, &path)
                    || protect::is_protected(&protected, &path, owner.bundle.as_deref())
                {
                    continue;
                }
                let captured = match tree::estimate(
                    &path,
                    flag,
                    until,
                    self.max_tree_nodes,
                    |p| {
                        rules::never_touch(&engine.roots.home, p)
                            || protect::is_protected(&protected, p, owner.bundle.as_deref())
                    },
                    &[],
                ) {
                    Ok(captured) => captured,
                    Err(error) if error.code == "E_CANCELED" => return Err(error),
                    Err(error) if error.code == "E_TIMEOUT" => {
                        result.incomplete(&path, SkippedLocationReason::Timeout);
                        continue;
                    }
                    Err(error) if error.code == "E_PROTECTED" => continue,
                    Err(_) => {
                        result.incomplete(&path, SkippedLocationReason::Permission);
                        continue;
                    }
                };
                let captured = if captured.compact && captured.incomplete.is_none() {
                    match tree::capture(&path, flag, until) {
                        Ok(strict) => strict,
                        Err(error) if error.code == "E_CANCELED" => return Err(error),
                        Err(_) => {
                            let mut partial = captured;
                            partial.incomplete = Some(SkippedLocationReason::Timeout);
                            partial
                        }
                    }
                } else {
                    captured
                };
                if let Some(reason) = captured.incomplete {
                    result.incomplete(&path, reason);
                }
                // Keep the original scan's identity. A replacement during conversion must not acquire permission to delete.
                if plan
                    .snapshots
                    .get(&old.id)
                    .is_none_or(|s| !s.matches(&path))
                {
                    continue;
                }
                if captured.snapshots.keys().any(|p| {
                    rules::never_touch(&engine.roots.home, p)
                        || protect::is_protected(&protected, p, owner.bundle.as_deref())
                }) {
                    continue;
                }
                if let Some(reason) = owner.status_in(activity).or_else(|| match activity {
                    Ok(activity) if activity.in_use(&path) => Some(SkippedGroupReason::InUse),
                    _ => None,
                }) {
                    result.skip(
                        &owner,
                        owner.display_title(activity, &group.name),
                        Some(captured.bytes),
                        reason,
                        owner.blocking_processes(activity, &path, reason),
                    );
                    continue;
                }
                let action = plan
                    .actions
                    .get(&old.id)
                    .cloned()
                    .ok_or_else(super::cached_error)?;
                let active = matches!(&action, ItemAction::Path { bootout: Some((label, false)), .. } if engine.probe.loaded(label, false));
                let item = CleanupItem {
                    id: old.id.clone(),
                    path: old.path.clone(),
                    kind: if old.kind == PlanItemKind::Dir {
                        CleanupItemKind::Dir
                    } else {
                        CleanupItemKind::File
                    },
                    command: None,
                    size_bytes: Some(captured.bytes),
                    kept_open_files: 0,
                    modified_at: Some(captured.modified_at),
                    reason: if active {
                        CleanupReason::OrphanService
                    } else {
                        CleanupReason::Orphan
                    },
                    selected: active
                        || (captured.incomplete.is_none()
                            && old.confidence == PlanItemConfidence::High
                            && captured.modified_at <= uninstall::now() - 30 * 86_400_000),
                    delete_mode: settings.cleanup_delete_mode,
                    needs_admin: false,
                    note: old.note,
                };
                let position = result
                    .category
                    .groups
                    .iter()
                    .position(|g| g.id == group.id)
                    .unwrap_or_else(|| {
                        result.category.groups.push(CleanupGroup {
                            id: group.id.clone(),
                            title: group.name.clone(),
                            subtitle: None,
                            app_id: owner.app_id.clone(),
                            bundle_id: owner.bundle.clone(),
                            items: vec![],
                        });
                        result.category.groups.len() - 1
                    });
                result.category.groups[position].items.push(item.clone());
                result.items.insert(
                    item.id.clone(),
                    EntryItem {
                        docker_endpoint: None,
                        item,
                        owner: owner.clone(),
                        title: group.name.clone(),
                        tree: Some(captured),
                        action,
                    },
                );
            }
        }
        result.category.bytes = result
            .items
            .values()
            .filter_map(|i| i.item.size_bytes)
            .sum();
        progress.update(CleanupCategory::Leftovers, None, result.category.bytes)?;
        Ok(result)
    }
}
fn deduplicate(entry: &mut Entry) {
    let mut seen = BTreeSet::new();
    let mut paths = BTreeSet::new();
    // Prefer specialized collectors over generic application caches, then residual items.
    for key in [
        CleanupCategory::BrowserCaches,
        CleanupCategory::DevCaches,
        CleanupCategory::Logs,
        CleanupCategory::AppCaches,
        CleanupCategory::Homebrew,
        CleanupCategory::Leftovers,
    ] {
        let ids: Vec<_> = entry
            .report
            .categories
            .iter()
            .filter(|c| c.key == key)
            .flat_map(|c| &c.groups)
            .flat_map(|g| &g.items)
            .map(|i| i.id.clone())
            .collect();
        for id in ids {
            let Some(item) = entry.items.get(&id) else {
                continue;
            };
            let Some(tree) = &item.tree else {
                continue;
            };
            let path = PathBuf::from(&item.item.path);
            if paths
                .iter()
                .any(|p: &PathBuf| path.starts_with(p) || p.starts_with(&path))
                || tree
                    .snapshots
                    .values()
                    .any(|s| seen.contains(&(s.device, s.inode)))
            {
                entry.items.remove(&id);
                continue;
            }
            seen.extend(tree.snapshots.values().map(|s| (s.device, s.inode)));
            paths.insert(path);
        }
    }
    reconcile(entry);
}
pub(super) fn volume(home: &Path) -> Result<CleanupVolume, AppError> {
    #[cfg(unix)]
    {
        let stat = nix::sys::statfs::statfs(home)
            .map_err(|e| AppError::new("E_UNKNOWN", "cleanup_volume").with_detail(e.to_string()))?;
        Ok(CleanupVolume {
            name: volume_name(home),
            total_bytes: stat.blocks().saturating_mul(u64::from(stat.block_size())),
            free_bytes: stat
                .blocks_available()
                .saturating_mul(u64::from(stat.block_size())),
        })
    }
    #[cfg(not(unix))]
    {
        let _ = home;
        Err(AppError::new("E_UNKNOWN", "unsupported_platform"))
    }
}

#[cfg(target_os = "macos")]
fn volume_name(home: &Path) -> String {
    use objc2_foundation::{NSArray, NSString, NSURL};
    objc2::rc::autoreleasepool(|_| {
        let url = NSURL::fileURLWithPath(&NSString::from_str(&home.to_string_lossy()));
        // Foundation's constant is immutable and the returned resource value is checked before use.
        let key = unsafe { objc2_foundation::NSURLVolumeNameKey };
        url.resourceValuesForKeys_error(&NSArray::from_slice(&[key]))
            .ok()
            .and_then(|values| values.objectForKey(key))
            .and_then(|name| name.downcast::<NSString>().ok())
            .map(|name| name.to_string())
            .unwrap_or_else(|| home.to_string_lossy().into_owned())
    })
}
#[cfg(not(target_os = "macos"))]
fn volume_name(home: &Path) -> String {
    home.to_string_lossy().into_owned()
}
