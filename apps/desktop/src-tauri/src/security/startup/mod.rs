// SPDX-License-Identifier: GPL-3.0-only
// 移植自 KnockKnock，Objective-See, LLC，GPL-3.0。
// Upstream 914f2c0c2d3f058f40b6d98ed303e8a1a6715eb3.
// Changes: Rust, bounded/no-follow reads, BTM v18, persistent review state,
// injectable roots/commands, no cloud upload or product branding. See UPSTREAM.md.
mod actions;
pub mod btm;
#[cfg(test)]
mod concurrency;
pub(crate) mod plugins;
use super::*;
use crate::uninstall::{identity, rules::Snapshot};
use plist::Value;
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, BTreeSet};
#[derive(Clone)]
pub struct Roots {
    pub home: PathBuf,
    pub root: PathBuf,
    pub applications: Vec<PathBuf>,
}
impl Roots {
    pub fn native(home: PathBuf) -> Self {
        Self {
            applications: vec![
                PathBuf::from("/Applications"),
                home.join("Applications"),
                PathBuf::from("/System/Applications"),
            ],
            home,
            root: PathBuf::from("/"),
        }
    }
    pub fn path(&self, s: &str) -> PathBuf {
        if let Some(p) = s.strip_prefix("~/") {
            self.home.join(p)
        } else {
            self.root.join(s.trim_start_matches('/'))
        }
    }
}
pub(crate) struct Scanner<'a> {
    roots: &'a Roots,
    commands: &'a dyn Commands,
    own_data: Option<&'a own_data::OwnData>,
    items: BTreeMap<String, StartupItem>,
    missing: BTreeSet<StartupCategory>,
    btm_format: Option<BtmFormat>,
    hints: BTreeMap<String, Hint>,
    /// `launchctl print-disabled` for the user's GUI domain and for the system domain.
    user_overrides: Option<String>,
    system_overrides: Option<String>,
    user_uuid: Option<String>,
}
/// The app an item belongs to when its own path does not say: what BTM, launchd plists
/// (AssociatedBundleIdentifiers) or the scanner itself (browser extensions) record.
#[derive(Default)]
struct Hint {
    app: Option<PathBuf>,
    bundle_ids: Vec<String>,
    /// The plist BTM says a launch agent or daemon came from; its item path is the program.
    plist: Option<PathBuf>,
    /// In the user's login items list, which System Events can take out and put back.
    legacy_login: bool,
    /// Registered through ServiceManagement from inside an app, so launchctl can switch it.
    service: bool,
}
/// Part of macOS: on the sealed system volume, or one of its own files in /private/etc.
/// /System/Volumes holds the writable Data volume, reachable under that prefix too.
fn macos_owned(roots: &Roots, path: &Path) -> bool {
    let under = |p: &str| path.starts_with(roots.path(p));
    under("/System") && !under("/System/Volumes")
        || under("/usr") && !under("/usr/local")
        || under("/bin")
        || under("/sbin")
        || MACOS_FILES.iter().any(|p| path == roots.path(p))
}
/// Raise when the scan starts finding items earlier versions missed, or gives items new IDs.
/// 2: helpers registered from inside an app bundle (SMAppService), whose BTM records give
/// paths relative to it. 3: an app's own Open at Login is a login item.
const SCANNER_VERSION: &str = "3";
/// macOS's own default shell and startup files. They live on the writable Data volume, so
/// anything else there (an added /etc/zshenv, launchd.conf) is still listed.
const MACOS_FILES: [&str; 6] = [
    "/private/etc/bashrc",
    "/private/etc/profile",
    "/private/etc/zprofile",
    "/private/etc/zshrc",
    "/private/etc/rc.common",
    "/private/etc/defaults/periodic.conf",
];
fn outermost_app(path: &Path) -> Option<&Path> {
    path.ancestors()
        .filter(|p| p.extension().is_some_and(|e| e.eq_ignore_ascii_case("app")))
        .last()
}
/// The name Finder shows: the app's localized name where it has one ("UU远程"), otherwise
/// its file name ("Visual Studio Code", where the bundle itself only says "Code").
fn display_name(app: &Path) -> Option<String> {
    #[cfg(target_os = "macos")]
    {
        use objc2_foundation::{NSFileManager, NSString};
        let name = objc2::rc::autoreleasepool(|_| {
            NSFileManager::defaultManager()
                .displayNameAtPath(&NSString::from_str(&app.to_string_lossy()))
                .to_string()
        });
        let name = name.strip_suffix(".app").unwrap_or(&name).trim();
        (!name.is_empty()).then(|| name.to_owned())
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = app;
        None
    }
}
impl<'a> Scanner<'a> {
    fn plist(&mut self, path: &Path, category: StartupCategory) -> Option<Value> {
        if files::app_update_backup(path) {
            return None;
        }
        let resolved = if path.starts_with(self.roots.path("/System")) {
            path.canonicalize()
                .ok()
                .filter(|p| p.starts_with(self.roots.path("/System")))
        } else {
            None
        };
        match files::read_guarded(
            resolved.as_deref().unwrap_or(path),
            16 * 1024 * 1024,
            self.own_data,
        ) {
            Ok(b) => match plist::from_bytes(&b) {
                Ok(v) => Some(v),
                Err(_) => {
                    self.missing.insert(category);
                    None
                }
            },
            Err(e) => {
                if path.exists()
                    || e.message != "io"
                    || std::fs::symlink_metadata(path)
                        .is_err_and(|e| e.kind() != std::io::ErrorKind::NotFound)
                {
                    self.missing.insert(category);
                }
                None
            }
        }
    }
    fn entries(&mut self, path: &Path, category: StartupCategory) -> Vec<PathBuf> {
        if files::app_update_backup(path) {
            return vec![];
        }
        if std::fs::symlink_metadata(path).is_ok()
            && self
                .own_data
                .is_some_and(|guard| guard.check(path).is_err())
        {
            self.missing.insert(category);
            return vec![];
        }
        match files::safe_metadata(path) {
            Ok(m) if m.is_dir() => {}
            Ok(_) => return vec![],
            Err(_) => {
                if std::fs::symlink_metadata(path)
                    .is_err_and(|e| e.kind() != std::io::ErrorKind::NotFound)
                {
                    self.missing.insert(category);
                }
                return vec![];
            }
        }
        match std::fs::read_dir(path) {
            Ok(e) => e
                .filter_map(|e| e.ok().map(|e| e.path()))
                .filter(|p| !files::app_update_backup(p))
                .collect(),
            Err(_) => {
                self.missing.insert(category);
                vec![]
            }
        }
    }
    fn add(
        &mut self,
        category: StartupCategory,
        path: &Path,
        name: Option<String>,
        program: Option<PathBuf>,
        label: Option<String>,
    ) -> Option<String> {
        if files::app_update_backup(path) {
            return None;
        }
        let meta = std::fs::symlink_metadata(path).ok()?;
        if files::dataless(&meta) {
            self.missing.insert(category);
            return None;
        }
        let program = program.or_else(|| {
            if !meta.is_dir() {
                return None;
            }
            let info = files::read_guarded(
                &path.join("Contents/Info.plist"),
                4 * 1024 * 1024,
                self.own_data,
            )
            .ok()
            .and_then(|b| plist::from_bytes::<Value>(&b).ok())?;
            let executable = info
                .as_dictionary()?
                .get("CFBundleExecutable")
                .and_then(Value::as_string)
                .or_else(|| path.file_stem()?.to_str())?;
            (!executable.contains('/')).then(|| path.join("Contents/MacOS").join(executable))
        });
        if category == StartupCategory::Kexts && program.as_ref().is_none_or(|p| !p.is_file()) {
            return None;
        }
        if matches!(category, StartupCategory::Quicklook)
            && program.as_ref().is_some_and(|p| !p.exists())
        {
            return None;
        }

        // Validate unique subjects after enumeration, with bounded concurrency; owners
        // likewise come after enumeration, once BTM has said which app each item is for.
        let signature = signature::empty(SignatureStatus::Unknown);
        let trust = StartupTrust::Unknown;
        let scope = if path.starts_with(&self.roots.home) {
            StartupScope::User
        } else {
            StartupScope::System
        };
        let id = format!(
            "{:x}",
            Sha256::digest(format!(
                "{category:?}:{}:{}",
                path.display(),
                label.as_deref().unwrap_or_default()
            ))
        );
        let protected = path.starts_with(self.roots.path("/System"));
        let actions = item_actions(category, protected, StartupState::Enabled);
        self.items.insert(
            id.clone(),
            StartupItem {
                id: id.clone(),
                category,
                name: name.filter(|s| !s.is_empty()).unwrap_or_else(|| {
                    path.file_name()
                        .unwrap_or_default()
                        .to_string_lossy()
                        .into_owned()
                }),
                label,
                path: path.to_string_lossy().into_owned(),
                program: program.map(|p| p.to_string_lossy().into_owned()),
                args: vec![],
                scope,
                owner: None,
                signature,
                trust,
                state: StartupState::Enabled,
                triggers: vec![],
                first_seen_at: 0,
                is_new: false,
                risk: if trust == StartupTrust::Untrusted {
                    StartupRisk::High
                } else {
                    StartupRisk::None
                },
                actions,
                requires_admin: !protected && scope == StartupScope::System,
                av: None,
            },
        );
        Some(id)
    }
    /// Where an item is registered: the plist BTM names for a launch agent or daemon, whose
    /// item path is its program, otherwise the item path.
    fn registered<'p>(&'p self, item: &'p StartupItem) -> &'p Path {
        self.hints
            .get(&item.id)
            .and_then(|h| h.plist.as_deref())
            .unwrap_or(Path::new(&item.path))
    }
    fn signatures(&mut self) {
        // macOS's own items are Apple's by where they are registered, and never listed:
        // checking their signatures was most of a scan (about 0.3 s each, a thousand of them).
        let subjects: BTreeSet<_> = self
            .items
            .values()
            .filter(|i| {
                i.category != StartupCategory::Cron && !macos_owned(self.roots, self.registered(i))
            })
            .map(|i| {
                let path = Path::new(i.program.as_deref().unwrap_or(&i.path));
                path.canonicalize().unwrap_or_else(|_| path.to_owned())
            })
            .collect();
        let paths: Vec<_> = subjects.into_iter().collect();
        let next = std::sync::atomic::AtomicUsize::new(0);
        let signatures = Mutex::new(BTreeMap::new());
        // Each check runs in a helper process and mostly waits on it.
        let workers = std::thread::available_parallelism().map_or(4, |n| n.get().clamp(2, 8));
        std::thread::scope(|scope| {
            for _ in 0..paths.len().min(workers) {
                scope.spawn(|| {
                    // Cache hits still walk entire bundles. Let interactive work
                    // take precedence while these filesystem checks run.
                    #[cfg(target_os = "macos")]
                    unsafe {
                        nix::libc::pthread_set_qos_class_self_np(
                            nix::libc::qos_class_t::QOS_CLASS_UTILITY,
                            0,
                        );
                    }
                    loop {
                        let index = next.fetch_add(1, Ordering::Relaxed);
                        let Some(path) = paths.get(index) else {
                            break;
                        };
                        let start = std::time::Instant::now();
                        // Unchanged files keep their verdict for hours: scans run every few
                        // minutes and on every visit.
                        let info = signature::inspect_recent(
                            path,
                            self.own_data,
                            std::time::Duration::from_secs(6 * 3600),
                        )
                        .unwrap_or_else(|error| {
                            log::warn!(
                                "Startup signature unavailable for {}: {}",
                                path.display(),
                                error.message
                            );
                            signature::empty(SignatureStatus::Unknown)
                        });
                        if std::env::var_os("OPENNAVO_STARTUP_PROFILE").is_some() {
                            eprintln!(
                                "startup-profile signature {:.3}ms {}",
                                start.elapsed().as_secs_f64() * 1000.0,
                                path.display()
                            );
                        }
                        signatures
                            .lock()
                            .unwrap_or_else(|e| e.into_inner())
                            .insert(path.clone(), info);
                    }
                });
            }
        });
        let signatures = signatures.into_inner().unwrap_or_else(|e| e.into_inner());
        let roots = self.roots;
        for item in self.items.values_mut() {
            if item.category == StartupCategory::Cron {
                continue;
            }
            // Where it is registered decides whether it is part of macOS: a script that
            // /System/Library/LaunchDaemons runs is, a user agent that runs /bin/sh is not.
            // Apple's interpreter does not vouch for a third-party launch plist.
            let registered = self
                .hints
                .get(&item.id)
                .and_then(|h| h.plist.as_deref())
                .unwrap_or(Path::new(&item.path));
            let system = macos_owned(roots, registered);
            let subject = Path::new(item.program.as_deref().unwrap_or(&item.path));
            let path = subject
                .canonicalize()
                .unwrap_or_else(|_| subject.to_owned());
            item.signature = if system {
                signature::empty(SignatureStatus::Apple)
            } else {
                signatures
                    .get(&path)
                    .cloned()
                    .unwrap_or_else(|| signature::empty(SignatureStatus::Unknown))
            };
            let apple = system
                || item.signature.status == SignatureStatus::Apple
                    && !matches!(
                        item.category,
                        StartupCategory::LaunchItems | StartupCategory::Btm
                    );
            item.trust = if apple {
                StartupTrust::Apple
            } else if item.signature.status == SignatureStatus::Invalid {
                StartupTrust::Untrusted
            } else if matches!(
                item.signature.status,
                SignatureStatus::DeveloperId | SignatureStatus::AppStore
            ) {
                StartupTrust::Known
            } else {
                StartupTrust::Unknown
            };
            item.risk = match item.signature.status {
                SignatureStatus::Invalid => StartupRisk::High,
                SignatureStatus::Modified => StartupRisk::Notice,
                _ => StartupRisk::None,
            };
            let protected = apple;
            item.actions = item_actions(item.category, protected, item.state);
            item.requires_admin = !protected && item.scope == StartupScope::System;
        }
    }
    /// The app each item belongs to, as people know it: the outermost app around its program
    /// (O+Connect.app, not the helper app inside it), else the app BTM or its plist names.
    /// macOS's own items are never listed, so they get none.
    fn owners(&mut self) {
        let mut found: BTreeMap<String, Option<PathBuf>> = BTreeMap::new();
        let mut owners: BTreeMap<PathBuf, Option<StartupOwner>> = BTreeMap::new();
        let ids: Vec<String> = self
            .items
            .values()
            .filter(|i| i.trust != StartupTrust::Apple)
            .map(|i| i.id.clone())
            .collect();
        for id in ids {
            let item = &self.items[&id];
            let hint = self.hints.get(&id);
            let app = [item.program.as_deref(), Some(item.path.as_str())]
                .into_iter()
                .flatten()
                .filter_map(|p| outermost_app(Path::new(p)))
                .chain(hint.and_then(|h| h.app.as_deref()))
                // BTM stores app URLs with a trailing slash.
                .map(|p| p.components().collect::<PathBuf>())
                .chain(
                    hint.into_iter()
                        .flat_map(|h| &h.bundle_ids)
                        .filter_map(|b| {
                            found
                                .entry(b.clone())
                                .or_insert_with(|| self.commands.app_for_bundle(b))
                                .clone()
                        }),
                )
                .find(|app| self.app_readable(app));
            let owner = app.and_then(|app| {
                owners
                    .entry(app.clone())
                    .or_insert_with(|| {
                        let identity = identity::bundle(&app);
                        Some(StartupOwner {
                            name: display_name(&app).or(identity.name).unwrap_or_else(|| {
                                app.file_stem()
                                    .unwrap_or_default()
                                    .to_string_lossy()
                                    .into_owned()
                            }),
                            bundle_id: identity.bundle_id,
                            path: app.to_string_lossy().into_owned(),
                            icon_path: None,
                        })
                    })
                    .clone()
            });
            if let Some(item) = self.items.get_mut(&id) {
                item.owner = owner;
            }
        }
    }
    /// What OpenNavo can switch beyond launchd items in the Launch folders: an app's Open at
    /// Login through System Events, a ServiceManagement job through launchctl. An app that
    /// registered itself to open at login, and a job System Settings switched off, stay with
    /// System Settings.
    fn controls(&mut self) {
        for item in self.items.values_mut() {
            if item.trust == StartupTrust::Apple || item.state == StartupState::Removed {
                continue;
            }
            let hint = self.hints.get(&item.id);
            let on = match item.category {
                StartupCategory::LoginItems => {
                    let listed = hint.is_some_and(|h| h.legacy_login);
                    (listed || item.state == StartupState::Disabled).then_some(listed)
                }
                StartupCategory::Btm
                    if hint.is_some_and(|h| h.service) && item.state != StartupState::Disabled =>
                {
                    let Some((label, system)) = service_label(item.label.as_deref()) else {
                        continue;
                    };
                    let overrides = if system {
                        self.system_overrides.as_deref()
                    } else {
                        self.user_overrides.as_deref()
                    };
                    let off = override_disabled(overrides, label) == Some(true);
                    if off {
                        item.state = StartupState::Disabled;
                    }
                    // Login items and agents run in the user's session, daemons for the whole Mac.
                    item.scope = if system {
                        StartupScope::System
                    } else {
                        StartupScope::User
                    };
                    item.requires_admin = system;
                    Some(!off)
                }
                _ => continue,
            };
            item.actions.retain(|a| {
                !matches!(
                    a,
                    StartupAction::Enable | StartupAction::Disable | StartupAction::Remove
                )
            });
            if let Some(on) = on {
                item.actions.insert(
                    0,
                    if on {
                        StartupAction::Disable
                    } else {
                        StartupAction::Enable
                    },
                );
            }
        }
    }
    /// An app bundle that can be opened without following symlinks, downloading iCloud
    /// placeholders or touching OpenNavo's own data.
    fn app_readable(&self, app: &Path) -> bool {
        files::safe_metadata(app).is_ok_and(|m| m.is_dir())
            && self.own_data.is_none_or(|guard| guard.check(app).is_ok())
    }
    fn launch(&mut self) {
        let user_domain = identity::user_domain();
        self.user_overrides = self
            .commands
            .run("/bin/launchctl", &["print-disabled", &user_domain])
            .ok();
        self.system_overrides = self
            .commands
            .run("/bin/launchctl", &["print-disabled", "system"])
            .ok();
        for location in [
            "/System/Library/LaunchAgents",
            "/System/Library/LaunchDaemons",
            "/Library/LaunchAgents",
            "/Library/LaunchDaemons",
            "~/Library/LaunchAgents",
        ] {
            for path in self.entries(&self.roots.path(location), StartupCategory::LaunchItems) {
                let Some(v) = self.plist(&path, StartupCategory::LaunchItems) else {
                    continue;
                };
                let Some(d) = v.as_dictionary() else {
                    continue;
                };
                let lower: BTreeMap<_, _> = d.iter().map(|(k, v)| (k.to_lowercase(), v)).collect();
                let Some(label) = lower
                    .get("label")
                    .and_then(|v| v.as_string())
                    .filter(|v| valid_launch_label(v))
                else {
                    continue;
                };
                let mut triggers = vec![];
                for (key, t) in [
                    ("runatload", StartupTrigger::AtLoad),
                    ("keepalive", StartupTrigger::KeepAlive),
                    ("startinterval", StartupTrigger::Interval),
                    ("startcalendarinterval", StartupTrigger::Calendar),
                    ("watchpaths", StartupTrigger::WatchPaths),
                    ("queuedirectories", StartupTrigger::WatchPaths),
                    ("launchevents", StartupTrigger::Events),
                    ("sockets", StartupTrigger::Events),
                    ("machservices", StartupTrigger::Events),
                    ("startonmount", StartupTrigger::Events),
                ] {
                    if lower
                        .get(key)
                        .is_some_and(|v| v.as_boolean() != Some(false))
                    {
                        triggers.push(t);
                    }
                }
                if triggers.is_empty()
                    && !lower.contains_key("runatload")
                    && !lower.contains_key("keepalive")
                    && lower.get("ondemand").and_then(|v| v.as_boolean()) == Some(false)
                {
                    triggers.push(StartupTrigger::AtLoad);
                }
                if triggers.is_empty() {
                    continue;
                }
                let args = lower
                    .get("programarguments")
                    .map(|v| strings(v))
                    .unwrap_or_default();
                let program = lower
                    .get("program")
                    .and_then(|v| strings(v).into_iter().next())
                    .or_else(|| args.first().cloned());
                let Some(program) = program else {
                    continue;
                };
                // The plist was already read through OwnData. Do not reopen it
                // through the uninstall parser in this SQLite-owning process.
                let mut program = PathBuf::from(&program);
                if !program.is_absolute() {
                    let env = lower
                        .get("environmentvariables")
                        .and_then(|v| v.as_dictionary())
                        .and_then(|v| v.get("PATH"))
                        .and_then(Value::as_string)
                        .unwrap_or("/usr/bin:/bin:/usr/sbin:/sbin");
                    if let Some(p) = env
                        .split(':')
                        .filter(|p| p.starts_with('/'))
                        .map(|p| Path::new(p).join(&program))
                        .find(|p| files::safe_metadata(p).is_ok())
                    {
                        program = p;
                    }
                }
                if !program.is_absolute() {
                    continue;
                }
                // Match upstream's executable existence check without opening it.
                if !program.is_file() {
                    continue;
                }
                let overrides = if location.contains("LaunchDaemons") {
                    self.system_overrides.as_deref()
                } else {
                    self.user_overrides.as_deref()
                };
                let disabled = override_disabled(overrides, label).unwrap_or_else(|| {
                    lower
                        .get("disabled")
                        .and_then(|v| v.as_boolean())
                        .unwrap_or(false)
                });
                if let Some(id) = self.add(
                    StartupCategory::LaunchItems,
                    &path,
                    Some(label.into()),
                    Some(program),
                    Some(label.into()),
                ) {
                    let i = self.items.get_mut(&id).unwrap();
                    i.args = args.into_iter().skip(1).collect();
                    i.triggers = triggers;
                    if disabled {
                        i.state = StartupState::Disabled;
                        for action in &mut i.actions {
                            if *action == StartupAction::Disable {
                                *action = StartupAction::Enable;
                            }
                        }
                    }
                    // What System Settings uses to show a helper under its app (macOS 13+).
                    self.hints.entry(id).or_default().bundle_ids = lower
                        .get("associatedbundleidentifiers")
                        .map(|v| strings(v))
                        .unwrap_or_default();
                }
                self.inserts(&path, d.get("EnvironmentVariables"));
            }
        }
    }
    fn btm(&mut self) {
        let category = StartupCategory::Btm;
        let dir = self
            .roots
            .path("/private/var/db/com.apple.backgroundtaskmanagement");
        let mut parsed_archives = vec![];
        for path in self
            .entries(&dir, category)
            .into_iter()
            .filter(|p| p.extension().is_some_and(|e| e == "btm"))
        {
            let parsed = files::read_guarded(&path, 64 * 1024 * 1024, self.own_data)
                .and_then(|b| btm::Archive::parse(&b))
                .and_then(|a| a.records());
            // v18 keeps one store per account, named by its UUID; root and the system have
            // stores of their own.
            let owner = path
                .file_stem()
                .and_then(|s| s.to_str())
                .and_then(|s| s.strip_prefix("BackgroundItems-v18-"))
                .map(str::to_owned);
            let own = owner.is_none_or(|owner| {
                self.user_uuid
                    .as_deref()
                    .is_none_or(|me| owner.eq_ignore_ascii_case(me))
            });
            parsed_archives.push((own, parsed));
        }
        let modern = parsed_archives
            .iter()
            .any(|(_, r)| r.as_ref().is_ok_and(|(f, _)| *f == BtmFormat::V18));
        let launchd: BTreeMap<PathBuf, String> = self
            .items
            .values()
            .filter(|i| i.category == StartupCategory::LaunchItems)
            .map(|i| (PathBuf::from(&i.path), i.id.clone()))
            .collect();
        for (own, parsed) in parsed_archives {
            match parsed {
                Ok((format, records)) => {
                    if modern && format == BtmFormat::V16 {
                        continue;
                    }
                    self.btm_format = Some(format);
                    for r in records {
                        // Another account's or the system's own app records are not this
                        // user's Open at Login; they only contain helpers listed separately.
                        let opens_at_login = r.opens_at_login();
                        if opens_at_login && !own {
                            continue;
                        }
                        let service = r.service_managed();
                        let hint = Hint {
                            app: r.app.map(PathBuf::from),
                            bundle_ids: r.bundle_ids,
                            plist: r.plist.map(PathBuf::from),
                            legacy_login: false,
                            service,
                        };
                        // BTM lists every launch agent and daemon again. Keep only the
                        // launchd item, which can be switched off here, unless System
                        // Settings has switched it off: only System Settings can undo that.
                        if r.kind & (8 | 16) != 0
                            && r.disposition & 1 == 1
                            && let Some(id) = hint.plist.as_ref().and_then(|p| launchd.get(p))
                        {
                            let known = self.hints.entry(id.clone()).or_default();
                            known.app = known.app.take().or(hint.app);
                            known.bundle_ids.extend(hint.bundle_ids);
                            continue;
                        }
                        // An app opening at login is a login item, whichever way it was
                        // registered: the same ID as its entry in the legacy list, if any.
                        let (kind, path, label) = if opens_at_login {
                            (
                                StartupCategory::LoginItems,
                                PathBuf::from(r.path.trim_end_matches('/')),
                                None,
                            )
                        } else {
                            (category, PathBuf::from(r.path), r.identifier)
                        };
                        let active_in_another_user = self.items.values().any(|i| {
                            i.category == kind
                                && Path::new(&i.path) == path
                                && i.label == label
                                && i.state == StartupState::Enabled
                        });
                        if let Some(id) = self.add(kind, &path, Some(r.name), None, label) {
                            let item = self.items.get_mut(&id).unwrap();
                            item.state = if r.disposition & 1 == 1 || active_in_another_user {
                                StartupState::Enabled
                            } else {
                                StartupState::Disabled
                            };
                            item.triggers = vec![StartupTrigger::AtLoad];
                            if opens_at_login {
                                item.scope = StartupScope::User;
                            }
                            self.hints.insert(id, hint);
                        }
                    }
                }
                Err(_) => {
                    self.missing.insert(category);
                    self.btm_format = Some(BtmFormat::Unknown);
                }
            }
        }
    }
}
/// What a BTM store says about startup items: names, identifiers, paths and whether
/// each is enabled, the parts the scan reads. macOS rewrites these stores every few
/// seconds for its own bookkeeping, so only a different fingerprint is a change.
/// `None` when the store cannot be read or parsed.
pub(super) fn btm_fingerprint(path: &Path, guard: Option<&own_data::OwnData>) -> Option<u64> {
    use std::hash::{Hash, Hasher};
    let bytes = files::read_guarded(path, 64 * 1024 * 1024, guard).ok()?;
    let (format, records) = btm::Archive::parse(&bytes).ok()?.records().ok()?;
    let mut items: Vec<_> = records
        .iter()
        .map(|r| (&r.name, &r.identifier, &r.path, r.disposition & 1))
        .collect();
    items.sort();
    let mut hasher = std::collections::hash_map::DefaultHasher::new();
    format!("{format:?}").hash(&mut hasher);
    items.hash(&mut hasher);
    Some(hasher.finish())
}
fn strings(v: &Value) -> Vec<String> {
    v.as_string()
        .map(|s| vec![s.to_owned()])
        .or_else(|| {
            v.as_array().map(|v| {
                v.iter()
                    .filter_map(Value::as_string)
                    .map(str::to_owned)
                    .collect()
            })
        })
        .unwrap_or_default()
}

pub(crate) fn settings_url(category: StartupCategory, major: Option<u32>) -> Option<&'static str> {
    match category {
        StartupCategory::Btm | StartupCategory::LoginItems => {
            Some("x-apple.systempreferences:com.apple.LoginItems-Settings.extension")
        }
        StartupCategory::SystemExtensions | StartupCategory::AppExtensions
            if major.is_some_and(|v| v >= 15) =>
        {
            Some("x-apple.systempreferences:com.apple.LoginItems-Settings.extension")
        }
        // The older Extensions pane route has not been verified on macOS 13–14.
        _ => None,
    }
}
pub(crate) fn macos_major() -> Option<u32> {
    static MAJOR: std::sync::OnceLock<Option<u32>> = std::sync::OnceLock::new();
    *MAJOR.get_or_init(|| {
        let data = files::read(
            Path::new("/System/Library/CoreServices/SystemVersion.plist"),
            1024 * 1024,
        )
        .ok()?;
        let plist: Value = plist::from_bytes(&data).ok()?;
        plist
            .as_dictionary()?
            .get("ProductVersion")?
            .as_string()?
            .split('.')
            .next()?
            .parse()
            .ok()
    })
}
fn item_actions(
    category: StartupCategory,
    protected: bool,
    state: StartupState,
) -> Vec<StartupAction> {
    if protected {
        return vec![StartupAction::Reveal];
    }
    let mut actions = vec![StartupAction::Reveal, StartupAction::Scan];
    if category == StartupCategory::LaunchItems {
        actions.insert(
            0,
            if state == StartupState::Disabled {
                StartupAction::Enable
            } else {
                StartupAction::Disable
            },
        );
        actions.insert(1, StartupAction::Remove);
    } else if category == StartupCategory::LoginItems {
        actions.insert(0, StartupAction::Remove);
    }
    if settings_url(category, macos_major()).is_some() {
        actions.push(StartupAction::OpenSettings);
    }
    actions
}

/// A BTM identifier is "<type>.<launchd label>": 4 login item and 8 agent in the user's
/// domain, 16 daemon in the system domain (`true`).
pub(crate) fn service_label(identifier: Option<&str>) -> Option<(&str, bool)> {
    let (kind, label) = identifier?.split_once('.')?;
    let kind: u64 = kind.parse().ok()?;
    (kind & (4 | 8 | 16) != 0 && valid_launch_label(label) && !label.starts_with("com.apple."))
        .then_some((label, kind & 16 != 0))
}
/// BTM still lists this identifier as a ServiceManagement job the user allows. Checked again
/// right before launchctl switches it, by the privileged helper too, so a label alone never
/// reaches launchctl.
pub(in crate::security) fn registered_service(
    roots: &Roots,
    identifier: &str,
    guard: Option<&own_data::OwnData>,
) -> Result<(), AppError> {
    let dir = roots.path("/private/var/db/com.apple.backgroundtaskmanagement");
    for entry in std::fs::read_dir(dir)? {
        let path = entry?.path();
        if path.extension().is_none_or(|e| e != "btm") {
            continue;
        }
        let Ok((_, records)) = files::read_guarded(&path, 64 * 1024 * 1024, guard)
            .and_then(|b| btm::Archive::parse(&b))
            .and_then(|a| a.records())
        else {
            continue;
        };
        if records.iter().any(|r| {
            r.identifier.as_deref() == Some(identifier)
                && r.service_managed()
                && r.disposition & 1 == 1
        }) {
            return Ok(());
        }
    }
    Err(AppError::new(
        "E_STARTUP_CHANGED",
        "startup_service_missing",
    ))
}
/// One login item, read the way a full scan reads it: owner, signature and switch.
pub(super) fn login_item(
    roots: &Roots,
    commands: &dyn Commands,
    own_data: Option<&own_data::OwnData>,
    app: &Path,
) -> Option<StartupItem> {
    let mut scanner = Scanner {
        roots,
        commands,
        own_data,
        items: BTreeMap::new(),
        missing: BTreeSet::new(),
        btm_format: None,
        hints: BTreeMap::new(),
        user_overrides: None,
        system_overrides: None,
        user_uuid: None,
    };
    let name = app.file_stem().map(|s| s.to_string_lossy().into_owned());
    let id = scanner.add(StartupCategory::LoginItems, app, name, None, None)?;
    scanner.hints.entry(id.clone()).or_default().legacy_login = true;
    if let Some(item) = scanner.items.get_mut(&id) {
        item.scope = StartupScope::User;
    }
    scanner.signatures();
    scanner.controls();
    scanner.owners();
    scanner.items.remove(&id)
}
/// A switchable item's actions with the switch set: what a scan would give it.
pub(crate) fn switch_actions(category: StartupCategory, on: bool) -> Vec<StartupAction> {
    let mut actions = item_actions(category, false, StartupState::Enabled);
    actions.retain(|a| {
        !matches!(
            a,
            StartupAction::Enable | StartupAction::Disable | StartupAction::Remove
        )
    });
    actions.insert(
        0,
        if on {
            StartupAction::Disable
        } else {
            StartupAction::Enable
        },
    );
    actions
}
pub(crate) fn valid_launch_label(label: &str) -> bool {
    !label.is_empty()
        && label.len() <= 256
        && !label.starts_with('-')
        && label
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"._-@+".contains(&b))
}

pub(super) fn launch_label(
    path: &Path,
    guard: Option<&own_data::OwnData>,
) -> Result<String, AppError> {
    // launchd labels allow @/+ (e.g. versioned Homebrew services), unlike bundle IDs.
    let value: Value = plist::from_bytes(&files::read_guarded(path, 4 * 1024 * 1024, guard)?)
        .map_err(|_| AppError::new("E_PROTECTED", "startup_plist"))?;
    value
        .as_dictionary()
        .and_then(|d| d.get("Label"))
        .and_then(Value::as_string)
        .filter(|s| valid_launch_label(s))
        .map(str::to_owned)
        .ok_or_else(|| AppError::new("E_PROTECTED", "startup_label"))
}
pub(crate) fn override_disabled(output: Option<&str>, label: &str) -> Option<bool> {
    output?
        .lines()
        .find(|l| l.contains(&format!("\"{label}\"")))?
        .split_once("=>")
        .and_then(|(_, s)| match s.trim().trim_end_matches(';') {
            "true" | "disabled" => Some(true),
            "false" | "enabled" => Some(false),
            _ => None,
        })
}
/// Standalone inventory for processes without an open application database.
/// Database-owning callers must use `Service::startup_scan_at`, which excludes
/// live SQLite files and their aliases before any file is opened.
pub fn scan(roots: &Roots, commands: &dyn Commands) -> StartupSnapshot {
    scan_guarded(roots, commands, None)
}

fn scan_guarded(
    roots: &Roots,
    commands: &dyn Commands,
    own_data: Option<&own_data::OwnData>,
) -> StartupSnapshot {
    let mut scanner = Scanner {
        roots,
        commands,
        own_data,
        items: BTreeMap::new(),
        missing: BTreeSet::new(),
        btm_format: None,
        hints: BTreeMap::new(),
        user_overrides: None,
        system_overrides: None,
        user_uuid: commands.user_uuid(),
    };
    let started = std::time::Instant::now();
    scanner.launch();
    let launch = started.elapsed();
    scanner.btm();
    let btm = started.elapsed() - launch;
    scanner.plugins();
    let plugins = started.elapsed() - launch - btm;
    scanner.signatures();
    scanner.controls();
    scanner.owners();
    if std::env::var_os("OPENNAVO_STARTUP_PROFILE").is_some() {
        eprintln!(
            "startup-profile phases launch={launch:?} btm={btm:?} plugins={plugins:?} signatures={:?} total={:?}",
            started.elapsed() - launch - btm - plugins,
            started.elapsed()
        );
    }
    StartupSnapshot {
        cron_error: None,
        scanned_at: now(),
        items: scanner.items.into_values().collect(),
        missing: scanner.missing.into_iter().collect(),
        btm_format: scanner.btm_format,
    }
}
fn startup_changed(previous: &BTreeMap<String, StartupItem>, result: &StartupSnapshot) -> bool {
    let current: BTreeMap<_, _> = result.items.iter().map(|item| (&item.id, item)).collect();
    current.values().any(|item| {
        previous.get(&item.id).is_none_or(|old| {
            old.state != item.state
                || old.trust != item.trust
                || old.signature != item.signature
                || old.actions != item.actions
                || old.is_new != item.is_new
        })
    }) || previous.values().any(|old| {
        // An inaccessible category remains in the DB; it is not a deletion.
        // Comparing its absent scan rows on every refresh would recreate the loop.
        !result.missing.contains(&old.category) && !current.contains_key(&old.id)
    })
}
impl Service {
    pub fn startup_scan(&self) -> Result<StartupSnapshot, AppError> {
        self.startup_scan_at(&Roots::native(self.home.clone()))
    }
    /// Owner icons come from the cache Installed fills, so most are extracted already.
    /// macOS's own items are never shown, so their apps need none.
    fn owner_icons(&self, items: &mut [StartupItem]) {
        let cache = self.data.with_file_name("local-app-icons");
        let mut icons = BTreeMap::new();
        for item in items.iter_mut().filter(|i| i.trust != StartupTrust::Apple) {
            if let Some(owner) = &mut item.owner {
                let path = PathBuf::from(&owner.path);
                owner.icon_path = icons
                    .entry(path)
                    .or_insert_with_key(|path| {
                        crate::library::files::icon(&self.database, path, &cache).ok()
                    })
                    .clone();
            }
        }
    }
    /// The last scan as saved, for the page to show at once while it scans again. `None`
    /// before the first scan.
    pub fn startup_cached(&self) -> Result<Option<StartupSnapshot>, AppError> {
        let Some(saved) = self.database.meta("startup_snapshot")? else {
            return Ok(None);
        };
        let mut snapshot: StartupSnapshot = serde_json::from_str(&saved)?;
        snapshot.items = self.saved_startup()?;
        Ok(Some(snapshot))
    }
    /// Where the last scan looked (fixture roots in tests), for actions to revalidate against.
    pub(crate) fn startup_roots(&self) -> Roots {
        self.last_roots
            .lock()
            .ok()
            .and_then(|roots| roots.clone())
            .unwrap_or_else(|| Roots::native(self.home.clone()))
    }
    pub fn startup_scan_at(&self, roots: &Roots) -> Result<StartupSnapshot, AppError> {
        let _inventory = self.startup_inventory.lock().map_err(poison)?;
        loop {
            if let Some(result) = self.startup_scan_attempt(roots)? {
                return Ok(result);
            }
        }
    }
    fn startup_scan_attempt(&self, roots: &Roots) -> Result<Option<StartupSnapshot>, AppError> {
        let guard = self.mutation.lock().map_err(poison)?;
        *self.last_roots.lock().map_err(poison)? = Some(roots.clone());
        let cron_issue = self.cron_scan_issue()?;
        let previous: BTreeMap<_, _> = self
            .saved_startup()?
            .into_iter()
            .map(|i| (i.id.clone(), i))
            .collect();
        // A scan that sees more than the last one did is a new baseline, not a list of
        // additions: what it finds for the first time was there all along.
        let baseline = self.database.meta("startup_baseline")?.is_some()
            && self.database.meta("startup_scanner")?.as_deref() == Some(SCANNER_VERSION);
        let revision = self.startup_revision.load(Ordering::Relaxed);
        drop(guard);
        // Filesystem enumeration, signature checks and icon extraction can take minutes.
        // Only the short reconciliation/persistence phases share the action lock.
        let own_data = self.own_data()?;
        let mut result = scan_guarded(roots, self.commands.as_ref(), Some(&own_data));
        self.owner_icons(&mut result.items);
        let _guard = self.mutation.lock().map_err(poison)?;
        if revision != self.startup_revision.load(Ordering::Relaxed) {
            // Do not publish stale states or recreate removed rows/backups. Read again
            // outside the action lock; unchanged signatures reuse their cached verdicts.
            return Ok(None);
        }
        if let Some(error) = cron_issue {
            log::warn!("Cron scan unavailable: {} {}", error.code, error.message);
            result.cron_error = Some(error.code);
            if !result.missing.contains(&StartupCategory::Cron) {
                result.missing.push(StartupCategory::Cron);
            }
            result
                .items
                .retain(|item| item.category != StartupCategory::Cron);
        } else {
            self.map_cron_scan(&mut result)?;
        }
        // A login item switched off here has a backup. BTM may still list the app as opening
        // at login for a while; the backup says otherwise. Back in the login items list, it
        // was switched on elsewhere (System Settings, the app itself) and the backup is stale.
        let switched_off = |id: &str| -> Result<bool, AppError> {
            Ok(self
                .s2_get::<serde_json::Value>(&legacy_startup::backup_key(id))?
                .is_some())
        };
        for item in &mut result.items {
            if item.category != StartupCategory::LoginItems || !switched_off(&item.id)? {
                continue;
            }
            if item.actions.contains(&StartupAction::Disable) {
                self.database.with(|c| {
                    c.execute(
                        "DELETE FROM meta WHERE key=?1",
                        [&legacy_startup::backup_key(&item.id)],
                    )?;
                    Ok(())
                })?;
            } else {
                item.state = StartupState::Disabled;
                item.actions = switch_actions(StartupCategory::LoginItems, false);
            }
        }
        let mut added = vec![];
        for item in &mut result.items {
            if let Some(old) = previous.get(&item.id) {
                item.first_seen_at = old.first_seen_at;
                item.is_new = old.is_new;
                item.av = old.av.clone();
            } else {
                item.first_seen_at = result.scanned_at;
                // Added to open at login here a moment ago: not news to the user.
                let added_here = item.category == StartupCategory::LoginItems
                    && self.added_logins.lock().map_err(poison)?.remove(
                        &Path::new(&item.path)
                            .canonicalize()
                            .unwrap_or_else(|_| PathBuf::from(&item.path)),
                    );
                item.is_new = baseline && item.trust != StartupTrust::Apple && !added_here;
                if item.is_new {
                    item.risk = StartupRisk::Notice;
                    added.push(item.clone());
                }
            }
        }
        // Keep removed legacy entries, and login items switched off here, visible so their
        // original IDs can be restored or switched back on.
        for old in previous.values() {
            let keep = old.state == StartupState::Removed
                || old.category == StartupCategory::LoginItems
                    && old.state == StartupState::Disabled
                    && Path::new(&old.path).exists()
                    && switched_off(&old.id)?;
            if keep && !result.items.iter().any(|i| i.id == old.id) {
                result.items.push(old.clone());
            }
        }
        // The caller receives the snapshot directly. Only notify other consumers
        // when the observed state changed, never merely because a scan finished.
        let changed = startup_changed(&previous, &result);
        self.database.with(|c|{let tx=c.transaction()?;
            // Write only rows that differ. Deleting and re-inserting all of them (about
            // 1,100 on a typical Mac) on every five-minute fallback rewrote megabytes.
            let stored:BTreeMap<String,(String,Option<String>)>={let mut rows=tx.prepare("SELECT id,value,snapshot FROM security_startup")?;rows.query_map([],|r|Ok((r.get(0)?,(r.get(1)?,r.get(2)?))))?.collect::<Result<_,_>>()?};
            let mut kept=BTreeSet::new();
            for item in &result.items{if item.state == StartupState::Removed { continue; } let snapshot=Snapshot::capture(Path::new(&item.path)).ok().map(|s|serde_json::to_string(&s)).transpose()?;let value=serde_json::to_string(item)?;kept.insert(item.id.as_str());if stored.get(&item.id).is_some_and(|(v,s)|*v==value&&*s==snapshot){continue;}tx.execute("INSERT INTO security_startup(id,value,snapshot) VALUES(?1,?2,?3) ON CONFLICT(id) DO UPDATE SET value=excluded.value,snapshot=excluded.snapshot",params![item.id,value,snapshot])?;}
            // Preserve categories we could not inspect, rather than treating denial as deletion.
            for old in previous.values(){if old.state != StartupState::Removed && !result.missing.contains(&old.category) && !kept.contains(old.id.as_str()){tx.execute("DELETE FROM security_startup WHERE id=?1",[&old.id])?;}}
            tx.commit()?;Ok(())})?;
        self.database
            .set_meta("startup_baseline", &result.scanned_at.to_string())?;
        self.database.set_meta("startup_scanner", SCANNER_VERSION)?;
        // What the page shows at once next time, while its own scan runs; items are the rows.
        self.database.set_meta(
            "startup_snapshot",
            &serde_json::to_string(&StartupSnapshot {
                cron_error: result.cron_error.clone(),
                scanned_at: result.scanned_at,
                items: vec![],
                missing: result.missing.clone(),
                btm_format: result.btm_format,
            })?,
        )?;
        for item in &added {
            self.record_event(
                ProtectionEventKind::StartupAdded,
                &item.path,
                Some(item.id.clone()),
                false,
            )?;
        }
        if !added.is_empty() && self.settings.read().map_err(poison)?.startup_alerts {
            (self.sink)(CoreEvent::StartupAdded(StartupAdded { items: added }));
        }
        if changed {
            self.changed(SecurityChangeReason::Startup);
        }
        Ok(Some(result))
    }
}

#[cfg(test)]
mod action_tests {
    use super::*;
    #[test]
    fn startup_changes_cover_each_state_field_and_ignore_unreadable_categories() {
        let item: StartupItem = serde_json::from_value(serde_json::json!({
            "id":"fixture", "category":"shell_config", "name":".zshrc", "label":null,
            "path":"/fixture/.zshrc", "program":null, "args":[], "scope":"user", "owner":null,
            "signature":{"status":"not_code", "teamId":null,"signer":null,"notarized":null},
            "trust":"unknown", "state":"enabled", "triggers":[], "firstSeenAt":1,
            "isNew":false, "risk":"none", "actions":["reveal"], "requiresAdmin":false, "av":null
        }))
        .unwrap();
        let previous = BTreeMap::from([(item.id.clone(), item.clone())]);
        let mut result = StartupSnapshot {
            cron_error: None,
            scanned_at: 999,
            items: vec![item.clone()],
            missing: vec![],
            btm_format: None,
        };
        assert!(!startup_changed(&previous, &result));
        let changes: [fn(&mut StartupItem); 5] = [
            |i| i.state = StartupState::Disabled,
            |i| i.trust = StartupTrust::Untrusted,
            |i| i.signature.status = SignatureStatus::Modified,
            |i| i.actions.push(StartupAction::Scan),
            |i| i.is_new = true,
        ];
        for change in changes {
            result.items = vec![item.clone()];
            change(&mut result.items[0]);
            assert!(startup_changed(&previous, &result));
        }
        result.items = vec![item.clone()];
        assert!(startup_changed(&BTreeMap::new(), &result));
        result.items.clear();
        assert!(startup_changed(&previous, &result));
        result.missing.push(item.category);
        assert!(!startup_changed(&previous, &result));
    }
    #[test]
    fn only_login_settings_categories_offer_the_settings_action() {
        for category in [
            StartupCategory::ShellConfig,
            StartupCategory::Cron,
            StartupCategory::Periodic,
            StartupCategory::StartupScripts,
            StartupCategory::LoginHooks,
            StartupCategory::BrowserExtensions,
        ] {
            assert!(
                !item_actions(category, false, StartupState::Enabled)
                    .contains(&StartupAction::OpenSettings)
            );
        }
        for category in [StartupCategory::Btm, StartupCategory::LoginItems] {
            assert!(
                item_actions(category, false, StartupState::Enabled)
                    .contains(&StartupAction::OpenSettings)
            );
        }
        assert_eq!(
            item_actions(StartupCategory::LaunchItems, true, StartupState::Enabled),
            vec![StartupAction::Reveal]
        );
        for category in [
            StartupCategory::SystemExtensions,
            StartupCategory::AppExtensions,
        ] {
            assert!(settings_url(category, Some(15)).is_some());
            assert!(settings_url(category, Some(27)).is_some());
            assert!(settings_url(category, Some(14)).is_none());
            assert!(settings_url(category, None).is_none());
        }
    }
}

#[cfg(test)]
mod owner_tests {
    use super::*;
    use std::fs;
    struct Fake(BTreeMap<String, PathBuf>);
    impl Commands for Fake {
        fn run(&self, _: &str, args: &[&str]) -> Result<String, AppError> {
            if args.first() == Some(&"print") {
                Err(AppError::new("E_UNKNOWN", "not_loaded"))
            } else {
                Ok(String::new())
            }
        }
        fn app_for_bundle(&self, bundle_id: &str) -> Option<PathBuf> {
            self.0.get(bundle_id).cloned()
        }
    }
    fn write(path: &Path, body: &str) {
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, body).unwrap();
    }
    fn app(path: &Path, bundle_id: &str) {
        let name = path.file_stem().unwrap().to_string_lossy();
        write(
            &path.join("Contents/Info.plist"),
            &format!(
                r#"<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>{bundle_id}</string><key>CFBundleName</key><string>{name}</string><key>CFBundleExecutable</key><string>{name}</string></dict></plist>"#
            ),
        );
        write(&path.join("Contents/MacOS").join(&*name), "fixture");
    }
    fn plist(path: &Path, label: &str, program: &Path, extra: &str) {
        write(
            path,
            &format!(
                r#"<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>{label}</string><key>Program</key><string>{}</string><key>RunAtLoad</key><true/>{extra}</dict></plist>"#,
                program.display()
            ),
        );
    }
    fn url(path: &Path) -> plist::Value {
        plist::Value::String(reqwest::Url::from_file_path(path).unwrap().to_string())
    }
    #[test]
    fn items_belong_to_the_app_people_know_and_macos_items_are_apple() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let example = root.join("Applications/Example.app");
        app(&example, "com.example.app");
        let other = root.join("Applications/Other.app");
        app(&other, "com.example.other");
        // A helper app inside the app still belongs to the app.
        let helper = example.join("Contents/Library/LoginItems/Helper.app");
        app(&helper, "com.example.helper");
        let helper_program = helper.join("Contents/MacOS/Helper");
        let daemon = root.join("Library/PrivilegedHelperTools/com.example.daemon");
        write(&daemon, "fixture");
        let updater = root.join("opt/updater");
        write(&updater, "fixture");
        let daemon_plist = root.join("Library/LaunchDaemons/com.example.daemon.plist");
        plist(&daemon_plist, "com.example.daemon", &daemon, "");
        let helper_plist = root.join("Library/LaunchAgents/com.example.helper.plist");
        plist(&helper_plist, "com.example.helper", &helper_program, "");
        plist(
            &root.join("Library/LaunchAgents/com.example.updater.plist"),
            "com.example.updater",
            &updater,
            "<key>AssociatedBundleIdentifiers</key><string>com.example.other</string>",
        );
        // macOS's own: a script /System runs, and its default shell file. An added one is not.
        let script = root.join("usr/libexec/script");
        write(&script, "#!/bin/sh\n");
        plist(
            &root.join("System/Library/LaunchDaemons/com.apple.script.plist"),
            "com.apple.script",
            &script,
            "",
        );
        write(&root.join("private/etc/zshrc"), "# macOS\n");
        write(&root.join("private/etc/zshenv"), "# added\n");
        write(
            &root.join("private/etc/periodic/daily/999.third-party"),
            "#!/bin/sh\n",
        );
        let number = |n: u64| plist::Value::Integer(n.into());
        let text = |s: &str| plist::Value::String(s.into());
        let store = btm::test_archive(vec![
            vec![
                ("type", number(2)),
                ("identifier", text("2.com.example.app")),
                ("bundleIdentifier", text("com.example.app")),
                ("name", text("Example")),
                ("url", url(&example)),
                ("disposition", number(2)),
            ],
            vec![
                ("type", number(65552)),
                ("identifier", text("16.com.example.daemon")),
                ("name", text("Example")),
                ("container", text("Example Inc")),
                (
                    "associatedBundleIdentifiers",
                    plist::Value::Dictionary(
                        [(
                            "NS.objects".to_owned(),
                            plist::Value::Array(vec![text("com.example.app")]),
                        )]
                        .into_iter()
                        .collect(),
                    ),
                ),
                ("executablePath", text(&daemon.to_string_lossy())),
                ("url", url(&daemon_plist)),
                ("disposition", number(11)),
            ],
            // Switched off in System Settings: kept, since only System Settings can undo it.
            vec![
                ("type", number(65544)),
                ("identifier", text("8.com.example.helper")),
                ("name", text("Helper")),
                ("executablePath", text(&helper_program.to_string_lossy())),
                ("url", url(&helper_plist)),
                ("disposition", number(2)),
            ],
        ]);
        let btm = root.join("private/var/db/com.apple.backgroundtaskmanagement");
        fs::create_dir_all(&btm).unwrap();
        fs::write(btm.join("BackgroundItems-v18.btm"), store).unwrap();
        let roots = Roots {
            home: root.join("home"),
            root: root.clone(),
            applications: vec![],
        };
        let commands = Fake(BTreeMap::from([(
            "com.example.other".to_owned(),
            other.clone(),
        )]));
        let items = scan(&roots, &commands).items;
        let labelled = |suffix: &str| -> Vec<&StartupItem> {
            items
                .iter()
                .filter(|i| i.label.as_deref().is_some_and(|l| l.ends_with(suffix)))
                .collect()
        };
        let owner = |item: &StartupItem| item.owner.as_ref().map(|o| o.path.clone());
        let example_path = Some(example.to_string_lossy().into_owned());

        // BTM's copy of an enabled daemon is dropped; the launchd item learns its app from it.
        let daemon = labelled("com.example.daemon");
        assert_eq!(daemon.len(), 1);
        assert_eq!(daemon[0].category, StartupCategory::LaunchItems);
        let daemon_owner = daemon[0].owner.as_ref().unwrap();
        assert_eq!(daemon_owner.name, "Example");
        assert_eq!(daemon_owner.bundle_id.as_deref(), Some("com.example.app"));
        assert_eq!(owner(daemon[0]), example_path);

        let helper = labelled("com.example.helper");
        assert_eq!(helper.len(), 2);
        assert!(helper.iter().all(|i| owner(i) == example_path));
        assert!(
            helper
                .iter()
                .any(|i| i.category == StartupCategory::Btm && i.state == StartupState::Disabled)
        );

        let updater = labelled("com.example.updater");
        assert_eq!(
            owner(updater[0]),
            Some(other.to_string_lossy().into_owned())
        );
        // The app's own record is its Open at Login, a login item.
        let registered = items
            .iter()
            .find(|i| i.category == StartupCategory::LoginItems)
            .unwrap();
        assert_eq!(registered.path, example.to_string_lossy());
        assert_eq!(owner(registered), example_path);

        assert_eq!(labelled("com.apple.script")[0].trust, StartupTrust::Apple);
        let shell = |name: &str| {
            items
                .iter()
                .find(|i| i.path == root.join("private/etc").join(name).to_string_lossy())
                .unwrap()
                .trust
        };
        assert_eq!(shell("zshrc"), StartupTrust::Apple);
        assert_ne!(shell("zshenv"), StartupTrust::Apple);
        assert_ne!(shell("periodic/daily/999.third-party"), StartupTrust::Apple);
    }
}

#[cfg(test)]
mod switch_tests {
    use super::*;
    use std::fs;
    const ME: &str = "58F37EF8-56EC-4ED0-8E34-A86B62DB5682";
    #[derive(Default)]
    struct Fake {
        user_disabled: String,
        listed: Vec<PathBuf>,
    }
    impl Commands for Fake {
        fn run(&self, program: &str, args: &[&str]) -> Result<String, AppError> {
            match (program, args) {
                ("/bin/launchctl", ["print-disabled", domain]) if domain.starts_with("gui/") => {
                    Ok(self.user_disabled.clone())
                }
                (_, ["print", ..]) => Err(AppError::new("E_UNKNOWN", "not_loaded")),
                _ => Ok(String::new()),
            }
        }
        fn login_items(&self) -> Result<Vec<PathBuf>, AppError> {
            Ok(self.listed.clone())
        }
        fn user_uuid(&self) -> Option<String> {
            Some(ME.into())
        }
    }
    fn app(path: &Path, bundle_id: &str) {
        let name = path.file_stem().unwrap().to_string_lossy().into_owned();
        fs::create_dir_all(path.join("Contents/MacOS")).unwrap();
        fs::write(
            path.join("Contents/Info.plist"),
            format!(
                r#"<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>{bundle_id}</string><key>CFBundleExecutable</key><string>{name}</string></dict></plist>"#
            ),
        )
        .unwrap();
        fs::write(path.join("Contents/MacOS").join(&name), "fixture").unwrap();
    }
    fn text(s: &str) -> plist::Value {
        plist::Value::String(s.into())
    }
    fn number(n: u64) -> plist::Value {
        plist::Value::Integer(n.into())
    }
    fn url(path: &Path) -> plist::Value {
        text(reqwest::Url::from_file_path(path).unwrap().as_str())
    }
    fn relative(path: &str) -> plist::Value {
        plist::Value::Dictionary(
            [
                ("NS.base".to_owned(), text("file:///")),
                ("NS.relative".to_owned(), text(path)),
            ]
            .into_iter()
            .collect(),
        )
    }
    #[test]
    fn open_at_login_and_service_management_jobs_get_a_switch() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let example = root.join("Applications/Example.app");
        app(&example, "com.example.app");
        app(
            &example.join("Contents/Library/LoginItems/Helper.app"),
            "com.example.helper",
        );
        fs::write(example.join("Contents/MacOS/Daemon"), "fixture").unwrap();
        let listed = root.join("Applications/Listed.app");
        app(&listed, "com.example.listed");
        let mainapp = root.join("Applications/Main.app");
        app(&mainapp, "com.example.main");
        let theirs = root.join("Applications/Theirs.app");
        app(&theirs, "com.example.theirs");
        let store = root.join("private/var/db/com.apple.backgroundtaskmanagement");
        fs::create_dir_all(&store).unwrap();
        let mine = btm::test_archive(vec![
            vec![
                ("type", number(2)),
                ("identifier", text("2.com.example.app")),
                ("bundleIdentifier", text("com.example.app")),
                ("name", text("Example")),
                ("url", url(&example)),
                ("disposition", number(2)),
            ],
            vec![
                ("type", number(4)),
                ("identifier", text("4.com.example.helper")),
                ("container", text("2.com.example.app")),
                ("name", text("Helper")),
                ("url", relative("Contents/Library/LoginItems/Helper.app")),
                ("disposition", number(11)),
            ],
            vec![
                ("type", number(16)),
                ("identifier", text("16.com.example.daemon")),
                ("container", text("2.com.example.app")),
                ("name", text("Daemon")),
                ("executablePath", text("Contents/MacOS/Daemon")),
                (
                    "url",
                    relative("Contents/Library/LaunchDaemons/com.example.daemon.plist"),
                ),
                ("disposition", number(11)),
            ],
            // In the legacy list as well: the list entry is the switch.
            vec![
                ("type", number(2)),
                ("identifier", text("2.com.example.listed")),
                ("name", text("Listed")),
                ("url", url(&listed)),
                ("disposition", number(11)),
            ],
            // Opens at login by its own registration: only System Settings turns it off.
            vec![
                ("type", number(2)),
                ("identifier", text("2.com.example.main")),
                ("name", text("Main")),
                ("url", url(&mainapp)),
                ("disposition", number(11)),
            ],
        ]);
        fs::write(store.join(format!("BackgroundItems-v18-{ME}.btm")), mine).unwrap();
        let other = btm::test_archive(vec![vec![
            ("type", number(2)),
            ("identifier", text("2.com.example.theirs")),
            ("name", text("Theirs")),
            ("url", url(&theirs)),
            ("disposition", number(11)),
        ]]);
        fs::write(
            store.join("BackgroundItems-v18-FFFFEEEE-DDDD-CCCC-BBBB-AAAA00000000.btm"),
            other,
        )
        .unwrap();
        let roots = Roots {
            home: root.join("home"),
            root: root.clone(),
            applications: vec![],
        };
        let commands = Fake {
            user_disabled: String::new(),
            listed: vec![listed.clone()],
        };
        let items = scan(&roots, &commands).items;
        let at = |path: &Path| {
            items
                .iter()
                .find(|i| {
                    i.category == StartupCategory::LoginItems && i.path == path.to_string_lossy()
                })
                .unwrap_or_else(|| panic!("{}", path.display()))
        };
        let labelled = |label: &str| {
            items
                .iter()
                .find(|i| i.label.as_deref() == Some(label))
                .unwrap()
        };
        let switch = |item: &StartupItem| {
            item.actions
                .iter()
                .find(|a| matches!(a, StartupAction::Enable | StartupAction::Disable))
                .copied()
        };
        assert_eq!(at(&example).state, StartupState::Disabled);
        assert_eq!(switch(at(&example)), Some(StartupAction::Enable));
        assert_eq!(at(&example).scope, StartupScope::User);
        assert!(!at(&example).requires_admin);
        assert_eq!(switch(at(&listed)), Some(StartupAction::Disable));
        assert_eq!(
            items
                .iter()
                .filter(|i| i.path == listed.to_string_lossy())
                .count(),
            1
        );
        assert_eq!(at(&mainapp).state, StartupState::Enabled);
        assert_eq!(switch(at(&mainapp)), None);
        assert!(at(&mainapp).actions.contains(&StartupAction::OpenSettings));
        assert!(!items.iter().any(|i| i.path == theirs.to_string_lossy()));
        let helper = labelled("4.com.example.helper");
        assert_eq!(switch(helper), Some(StartupAction::Disable));
        assert!(!helper.requires_admin);
        assert_eq!(helper.scope, StartupScope::User);
        let daemon = labelled("16.com.example.daemon");
        assert_eq!(switch(daemon), Some(StartupAction::Disable));
        assert!(daemon.requires_admin);
        assert_eq!(daemon.scope, StartupScope::System);

        // launchd's override decides whether a job allowed in BTM will start.
        let commands = Fake {
            user_disabled: "\t\"com.example.helper\" => disabled\n".into(),
            listed: vec![listed],
        };
        let items = scan(&roots, &commands).items;
        let helper = items
            .iter()
            .find(|i| i.label.as_deref() == Some("4.com.example.helper"))
            .unwrap();
        assert_eq!(helper.state, StartupState::Disabled);
        assert_eq!(switch(helper), Some(StartupAction::Enable));
        assert!(registered_service(&roots, "4.com.example.helper", None).is_ok());
        assert!(registered_service(&roots, "4.com.example.missing", None).is_err());
    }
}

#[cfg(test)]
mod persistence_tests {
    use super::*;
    type Rows = BTreeMap<String, (String, Option<String>)>;
    fn rows(service: &Service) -> Rows {
        service
            .database
            .with(|c| {
                let mut rows = c.prepare("SELECT id,value,snapshot FROM security_startup")?;
                let rows = rows
                    .query_map([], |r| Ok((r.get(0)?, (r.get(1)?, r.get(2)?))))?
                    .collect::<Result<_, _>>()?;
                Ok(rows)
            })
            .unwrap()
    }
    fn row(rows: &Rows, label: &str) -> Option<(String, (String, Option<String>))> {
        let label = format!(r#""label":"{label}""#);
        rows.iter()
            .find(|(_, (value, _))| value.contains(&label))
            .map(|(id, row)| (id.clone(), row.clone()))
    }
    #[test]
    fn rescans_write_only_the_rows_that_changed() {
        let (temp, service, _) = super::super::tests::fixture();
        let roots = Roots {
            home: service.home.clone(),
            root: temp.path().canonicalize().unwrap(),
            applications: vec![],
        };
        let agents = service.home.join("Library/LaunchAgents");
        std::fs::create_dir_all(&agents).unwrap();
        let agent = |label: &str| {
            std::fs::write(
                agents.join(format!("{label}.plist")),
                format!(r#"<plist><dict><key>Label</key><string>{label}</string><key>Program</key><string>/usr/bin/true</string><key>RunAtLoad</key><true/></dict></plist>"#),
            )
            .unwrap()
        };
        agent("org.fixture.kept");
        agent("org.fixture.removed");
        service.startup_scan_at(&roots).unwrap();
        let first = rows(&service);
        assert!(row(&first, "org.fixture.kept").is_some());
        assert!(row(&first, "org.fixture.removed").is_some());
        let changes = || service.database.with(|c| Ok(c.total_changes())).unwrap();
        let before = changes();
        service.startup_scan_at(&roots).unwrap();
        // Only the scan's own meta keys (baseline, scanner, snapshot) are written.
        assert!(changes() - before <= 3, "{} changes", changes() - before);
        assert_eq!(rows(&service), first);
        std::fs::remove_file(agents.join("org.fixture.removed.plist")).unwrap();
        agent("org.fixture.added");
        service.startup_scan_at(&roots).unwrap();
        let last = rows(&service);
        assert!(row(&last, "org.fixture.removed").is_none());
        assert!(row(&last, "org.fixture.added").is_some());
        assert_eq!(
            row(&last, "org.fixture.kept"),
            row(&first, "org.fixture.kept")
        );
    }
}

#[cfg(all(test, target_os = "macos"))]
mod resident_perf_tests {
    use super::*;
    fn cpu_ms(who: i32) -> f64 {
        let mut usage: nix::libc::rusage = unsafe { std::mem::zeroed() };
        unsafe { nix::libc::getrusage(who, &mut usage) };
        let ms = |t: nix::libc::timeval| t.tv_sec as f64 * 1000.0 + t.tv_usec as f64 / 1000.0;
        ms(usage.ru_utime) + ms(usage.ru_stime)
    }
    /// Run explicitly: this machine's real startup sources with the app's own-data guard,
    /// as the resident watcher scans them. The first pass fills the signature cache; the
    /// following passes are the steady state of the five-minute fallback.
    #[test]
    #[ignore = "real-system resident scan probe"]
    fn resident_scan_perf() {
        let (_temp, service, _) = super::super::tests::fixture();
        let guard = service.own_data().unwrap();
        let home = PathBuf::from(std::env::var_os("HOME").unwrap());
        let roots = Roots::native(home);
        let commands = super::super::NativeCommands;
        let started = std::time::Instant::now();
        let warm = scan_guarded(&roots, &commands, Some(&guard));
        println!(
            "resident-scan warm items={} wall_ms={:.0}",
            warm.items.len(),
            started.elapsed().as_secs_f64() * 1000.0
        );
        for round in 0..3 {
            let (own, children) = (
                cpu_ms(nix::libc::RUSAGE_SELF),
                cpu_ms(nix::libc::RUSAGE_CHILDREN),
            );
            let started = std::time::Instant::now();
            let snapshot = scan_guarded(&roots, &commands, Some(&guard));
            println!(
                "resident-scan round={round} items={} wall_ms={:.0} cpu_ms={:.0} child_cpu_ms={:.0}",
                snapshot.items.len(),
                started.elapsed().as_secs_f64() * 1000.0,
                cpu_ms(nix::libc::RUSAGE_SELF) - own,
                cpu_ms(nix::libc::RUSAGE_CHILDREN) - children,
            );
        }
    }
}
