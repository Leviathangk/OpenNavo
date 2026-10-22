// SPDX-License-Identifier: GPL-3.0-only
// 移植自 KnockKnock，Objective-See, LLC，GPL-3.0。
// Copyright (c) 2015 Objective-See. All rights reserved. (Cronjobs)
// Copyright (c) 2018 Objective-See. All rights reserved. (EventRules)
// Rust adaptation of 914f2c0c2d3f058f40b6d98ed303e8a1a6715eb3; see UPSTREAM.md.
use super::*;
impl Scanner<'_> {
    pub(super) fn plugins(&mut self) {
        macro_rules! timed {
            ($method:ident) => {{
                let start = std::time::Instant::now();
                self.$method();
                if std::env::var_os("OPENNAVO_STARTUP_PROFILE").is_some() {
                    eprintln!(
                        "startup-profile plugin {} {:?}",
                        stringify!($method),
                        start.elapsed()
                    );
                }
            }};
        }
        timed!(directory_plugins);
        timed!(scripts);
        timed!(hooks_events);
        timed!(browser);
        timed!(extensions);
        timed!(application_plugins);
        timed!(proxies);
    }
    fn directory_plugins(&mut self) {
        for (category, locations, extension) in [
            (
                StartupCategory::Kexts,
                vec!["/System/Library/Extensions", "/Library/Extensions"],
                Some("kext"),
            ),
            (
                StartupCategory::AuthPlugins,
                vec![
                    "/System/Library/CoreServices/SecurityAgentPlugins",
                    "/Library/Security/SecurityAgentPlugins",
                ],
                None,
            ),
            (
                StartupCategory::DirServicesPlugins,
                vec![
                    "/System/Library/Frameworks/DirectoryService.framework/Versions/A/Resources/Plugins",
                    "/Library/DirectoryServices/PlugIns",
                ],
                Some("dsplug"),
            ),
            (
                StartupCategory::Spotlight,
                vec![
                    "/System/Library/Spotlight",
                    "/Library/Spotlight",
                    "~/Library/Spotlight",
                ],
                None,
            ),
        ] {
            for location in locations {
                for path in self.entries(&self.roots.path(location), category) {
                    if extension.is_some_and(|e| path.extension().is_none_or(|p| p != e)) {
                        continue;
                    }
                    if extension.is_none()
                        && files::safe_metadata(&path.join("Contents/Info.plist")).is_err()
                    {
                        continue;
                    }
                    self.add(category, &path, None, None, None);
                }
            }
        }
        let path = self.roots.path("/Library/SystemExtensions/db.plist");
        if let Some(v) = self.plist(&path, StartupCategory::SystemExtensions)
            && let Some(exts) = v
                .as_dictionary()
                .and_then(|d| d.get("extensions"))
                .and_then(Value::as_array)
        {
            for e in exts {
                if let Some(d) = e.as_dictionary()
                    && d.get("state").and_then(Value::as_string) == Some("activated_enabled")
                    && let Some(p) = d.get("originPath").and_then(Value::as_string)
                {
                    self.add(
                        StartupCategory::SystemExtensions,
                        Path::new(p),
                        None,
                        None,
                        None,
                    );
                }
            }
        }
    }
    fn scripts(&mut self) {
        for (category, locations) in [
            (
                StartupCategory::StartupScripts,
                vec![
                    "/private/etc/rc.cleanup",
                    "/private/etc/rc.common",
                    "/private/etc/rc.installer_cleanup",
                    "/private/etc/rc.server",
                    "/private/etc/launchd.conf",
                ],
            ),
            (
                StartupCategory::ShellConfig,
                vec![
                    "~/.zshenv",
                    "~/.zprofile",
                    "~/.zshrc",
                    "~/.zlogin",
                    "~/.zlogout",
                    "~/.bash_profile",
                    "~/.bashrc",
                    "~/.profile",
                    "/private/etc/zshenv",
                    "/private/etc/zprofile",
                    "/private/etc/zshrc",
                    "/private/etc/zlogin",
                    "/private/etc/zlogout",
                    "/private/etc/bashrc",
                    "/private/etc/profile",
                ],
            ),
            (
                StartupCategory::Periodic,
                vec!["/private/etc/defaults/periodic.conf"],
            ),
        ] {
            for location in locations {
                self.add(category, &self.roots.path(location), None, None, None);
            }
        }
        for location in [
            "/private/etc/periodic/daily",
            "/private/etc/periodic/weekly",
            "/private/etc/periodic/monthly",
        ] {
            for p in self.entries(&self.roots.path(location), StartupCategory::Periodic) {
                self.add(StartupCategory::Periodic, &p, None, None, None);
            }
        }
        // Never evaluate a cron command or shell configuration.
        let cron = self.commands.crontab_read();
        if cron.is_err() {
            self.missing.insert(StartupCategory::Cron);
        }
        if let Ok(Some(output)) = cron {
            let path = self
                .roots
                .path("/private/var/at/tabs")
                .join(self.roots.home.file_name().unwrap_or_default());
            for (index, line) in output
                .lines()
                .enumerate()
                .filter(|(_, l)| cron_job(l.trim()))
            {
                let line = line.trim();
                let label = super::super::legacy_startup::fingerprint(&output, index);
                // crontab -l may be readable although the backing spool file is not.
                let id = format!("cron:{:x}", Sha256::digest(format!("{label}:{line}")));
                self.items.insert(
                    id.clone(),
                    StartupItem {
                        id,
                        category: StartupCategory::Cron,
                        name: line.into(),
                        label: Some(label),
                        path: path.to_string_lossy().into_owned(),
                        program: None,
                        args: vec![],
                        scope: StartupScope::User,
                        owner: None,
                        signature: signature::empty(SignatureStatus::NotCode),
                        trust: StartupTrust::Unknown,
                        state: StartupState::Enabled,
                        triggers: vec![StartupTrigger::Calendar],
                        first_seen_at: 0,
                        is_new: false,
                        risk: StartupRisk::Notice,
                        actions: vec![StartupAction::Remove, StartupAction::Reveal],
                        requires_admin: false,
                        av: None,
                    },
                );
            }
        }
    }
    pub(super) fn inserts(&mut self, path: &Path, value: Option<&Value>) {
        if let Some(d) = value.and_then(Value::as_dictionary)
            && let Some(libs) = d
                .get("DYLD_INSERT_LIBRARIES")
                .or_else(|| d.get("__XPC_DYLD_INSERT_LIBRARIES"))
                .and_then(Value::as_string)
        {
            for lib in libs.split(':').filter(|s| s.starts_with('/')) {
                self.add(
                    StartupCategory::LibraryInserts,
                    Path::new(lib),
                    None,
                    None,
                    Some(path.to_string_lossy().into_owned()),
                );
            }
        }
    }
    fn hooks_events(&mut self) {
        for location in [
            "/Library/Preferences/com.apple.loginwindow.plist",
            "~/Library/Preferences/com.apple.loginwindow.plist",
        ] {
            let path = self.roots.path(location);
            if let Some(v) = self.plist(&path, StartupCategory::LoginHooks)
                && let Some(d) = v.as_dictionary()
            {
                for key in ["LoginHook", "LogoutHook"] {
                    if let Some(command) = d.get(key).and_then(Value::as_string) {
                        self.add(
                            StartupCategory::LoginHooks,
                            &path,
                            Some(command.into()),
                            command.starts_with('/').then(|| PathBuf::from(command)),
                            Some(key.into()),
                        );
                    }
                }
            }
        }
        let mut dirs = vec![self.roots.path("/private/etc/emond.d/rules")];
        if let Some(v) = self.plist(
            &self.roots.path("/private/etc/emond.d/emond.plist"),
            StartupCategory::EventRules,
        ) && let Some(paths) = v
            .as_dictionary()
            .and_then(|d| d.get("config"))
            .and_then(Value::as_dictionary)
            .and_then(|d| d.get("additionalRulesPaths"))
            .and_then(Value::as_array)
        {
            dirs.extend(
                paths
                    .iter()
                    .filter_map(Value::as_string)
                    .filter(|s| s.starts_with('/'))
                    .map(PathBuf::from),
            );
        }
        for dir in dirs {
            for path in self.entries(&dir, StartupCategory::EventRules) {
                if let Some(v) = self.plist(&path, StartupCategory::EventRules)
                    && let Some(rules) = v.as_array()
                {
                    for rule in rules {
                        if let Some(actions) = rule
                            .as_dictionary()
                            .and_then(|d| d.get("actions"))
                            .and_then(Value::as_array)
                        {
                            for (index, action) in actions.iter().enumerate() {
                                if let Some(command) = action
                                    .as_dictionary()
                                    .and_then(|d| d.get("command"))
                                    .and_then(Value::as_string)
                                {
                                    self.add(
                                        StartupCategory::EventRules,
                                        &path,
                                        Some(command.into()),
                                        None,
                                        Some(format!("{index}:{command}")),
                                    );
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    fn extensions(&mut self) {
        let cat = StartupCategory::AppExtensions;
        match self.commands.run("/usr/bin/pluginkit", &["-vmA"]) {
            Ok(text) => {
                for path in extension_paths(&text) {
                    self.add(cat, Path::new(path), None, None, None);
                }
            }
            Err(_) => {
                self.missing.insert(cat);
            }
        }
        if let Some(v) = self.plist(
            &self
                .roots
                .path("~/Library/Preferences/com.apple.preferences.extensions.FinderSync.plist"),
            cat,
        ) && let Some(ids) = v
            .as_dictionary()
            .and_then(|d| d.get("displayOrder"))
            .and_then(Value::as_array)
        {
            for id in ids
                .iter()
                .filter_map(Value::as_string)
                .filter(|s| identity::valid_identifier(s))
            {
                if let Ok(text) = self.commands.run("/usr/bin/pluginkit", &["-mi", id, "-v"]) {
                    for path in extension_paths(&text) {
                        self.add(cat, Path::new(path), None, None, None);
                    }
                }
            }
        }
        // qlmanage exposes the same registered-plugin statistics without loading private frameworks.
        match self.commands.run("/usr/bin/qlmanage", &["-m", "plugins"]) {
            Ok(text) => {
                for line in text.lines() {
                    if let Some(start) = line.find('/') {
                        let p = line[start..]
                            .rsplit_once(" (")
                            .map(|(p, _)| p)
                            .unwrap_or(line[start..].trim());
                        self.add(StartupCategory::Quicklook, Path::new(p), None, None, None);
                    }
                }
            }
            Err(_) => {
                self.missing.insert(StartupCategory::Quicklook);
            }
        }
    }
    fn application_plugins(&mut self) {
        let mut apps = vec![];
        for root in &self.roots.applications.clone() {
            let mut dirs = vec![(root.clone(), 0)];
            while let Some((dir, depth)) = dirs.pop() {
                for path in self.entries(&dir, StartupCategory::DockTiles) {
                    // An .app suffix also occurs on Finder aliases and symbolic links.
                    // They are not bundles to traverse under the scanner's no-follow policy.
                    match std::fs::symlink_metadata(&path) {
                        Ok(meta) if meta.is_dir() => {}
                        Ok(_) => continue,
                        Err(error) => {
                            if error.kind() != std::io::ErrorKind::NotFound {
                                self.missing.insert(StartupCategory::DockTiles);
                            }
                            continue;
                        }
                    }
                    if path.extension().is_some_and(|p| p == "app") {
                        apps.push(path);
                    } else if depth < 3 && files::safe_metadata(&path).is_ok_and(|m| m.is_dir()) {
                        dirs.push((path, depth + 1));
                    }
                }
            }
        }
        for app in apps {
            let path = app.join("Contents/Info.plist");
            if let Some(v) = self.plist(&path, StartupCategory::DockTiles)
                && let Some(d) = v.as_dictionary()
            {
                if let Some(plugin) = d
                    .get("NSDockTilePlugIn")
                    .and_then(Value::as_string)
                    .filter(|p| !p.starts_with('/') && !p.split('/').any(|c| c == ".."))
                {
                    self.add(
                        StartupCategory::DockTiles,
                        &app.join("Contents/PlugIns").join(plugin),
                        None,
                        None,
                        None,
                    );
                }
                self.inserts(&path, d.get("LSEnvironment"));
            }
        }
        // Legacy login-items enumeration uses the native read-only shared file list API.
        match self.commands.login_items() {
            Ok(paths) => {
                for path in paths {
                    // Same ID as the app's BTM record, which this entry replaces; named like it.
                    let name = path.file_stem().map(|s| s.to_string_lossy().into_owned());
                    if let Some(id) = self.add(StartupCategory::LoginItems, &path, name, None, None)
                    {
                        self.hints.entry(id.clone()).or_default().legacy_login = true;
                        let item = self.items.get_mut(&id).unwrap();
                        // The shared-file-list is the current user's, even for /Applications apps.
                        item.scope = StartupScope::User;
                        item.requires_admin = false;
                        if item.trust != StartupTrust::Apple
                            && !path.starts_with(self.roots.path("/System"))
                        {
                            item.actions = vec![
                                StartupAction::Remove,
                                StartupAction::Reveal,
                                StartupAction::Scan,
                            ];
                        }
                    }
                }
            }
            Err(_) => {
                self.missing.insert(StartupCategory::LoginItems);
            }
        }
    }
    fn json(&mut self, path: &Path) -> Option<serde_json::Value> {
        match files::read_guarded(path, 32 * 1024 * 1024, self.own_data) {
            Ok(bytes) => serde_json::from_slice(&bytes).ok(),
            Err(_) => None,
        }
    }
    fn browser(&mut self) {
        let category = StartupCategory::BrowserExtensions;
        // Extensions belong to their browser, wherever LaunchServices finds it installed.
        for (base, browser) in [
            (
                "~/Library/Application Support/Google/Chrome",
                "com.google.Chrome",
            ),
            (
                "~/Library/Application Support/com.operasoftware.Opera",
                "com.operasoftware.Opera",
            ),
        ] {
            let base = self.roots.path(base);
            let mut profiles = vec![base.clone(), base.join("Default")];
            profiles.extend(self.entries(&base, category).into_iter().filter(|p| {
                p.file_name()
                    .is_some_and(|n| n.to_string_lossy().starts_with("Profile"))
            }));
            for profile in profiles {
                for name in ["Preferences", "Secure Preferences"] {
                    if let Some(v) = self.json(&profile.join(name))
                        && let Some(exts) = v
                            .pointer("/extensions/settings")
                            .and_then(|v| v.as_object())
                    {
                        for (id, extension) in exts {
                            let truth = |key: &str| {
                                extension.get(key).is_some_and(|v| {
                                    v.as_bool() == Some(true) || v.as_i64().is_some_and(|v| v != 0)
                                })
                            };
                            if !truth("state")
                                || truth("was_installed_by_default")
                                || truth("blacklist")
                            {
                                continue;
                            }
                            let Some(raw) = extension.get("path").and_then(|v| v.as_str()) else {
                                continue;
                            };
                            let path = if raw.starts_with('/') {
                                PathBuf::from(raw)
                            } else {
                                profile.join("Extensions").join(raw)
                            };
                            let name = extension
                                .pointer("/manifest/name")
                                .and_then(|v| v.as_str())
                                .map(str::to_owned);
                            if let Some(item) =
                                self.add(category, &path, name, None, Some(id.clone()))
                            {
                                self.hints.entry(item).or_default().bundle_ids =
                                    vec![browser.into()];
                            }
                        }
                    }
                }
            }
        }
        for profile in self.entries(
            &self
                .roots
                .path("~/Library/Application Support/Firefox/Profiles"),
            category,
        ) {
            let mut seen = BTreeSet::new();
            for file in ["addons.json", "extensions.json"] {
                if let Some(v) = self.json(&profile.join(file))
                    && let Some(addons) = v.get("addons").and_then(|v| v.as_array())
                {
                    for addon in addons {
                        let Some(id) = addon
                            .get("id")
                            .and_then(|v| v.as_str())
                            .filter(|s| !s.contains('/'))
                        else {
                            continue;
                        };
                        if seen.contains(id) {
                            continue;
                        }
                        let mut path = profile.join("extensions").join(id);
                        if !path.exists() {
                            path = profile.join("extensions").join(format!("{id}.xpi"));
                        }
                        let name = addon
                            .get("name")
                            .or_else(|| addon.pointer("/defaultLocale/name"))
                            .and_then(|v| v.as_str())
                            .map(str::to_owned);
                        if let Some(item) = self.add(category, &path, name, None, Some(id.into())) {
                            self.hints.entry(item).or_default().bundle_ids =
                                vec!["org.mozilla.firefox".into()];
                            seen.insert(id.to_owned());
                        }
                    }
                }
            }
        }
        for kind in [
            "com.apple.Safari.extension",
            "com.apple.Safari.content-blocker",
        ] {
            if let Ok(text) = self
                .commands
                .run("/usr/bin/pluginkit", &["-mAvv", "-p", kind])
            {
                for line in text.lines() {
                    if let Some((key, path)) = line.split_once('=')
                        && key.trim() == "Path"
                    {
                        self.add(category, Path::new(path.trim()), None, None, None);
                    }
                }
            }
        }
    }
    fn proxies(&mut self) {
        let category = StartupCategory::LibraryProxies;
        let own_data = self.own_data;
        let mut cache = PROBED
            .get_or_init(Default::default)
            .lock()
            .unwrap_or_else(|e| e.into_inner());
        // Paths no longer listed fall out with `previous`.
        let mut previous = std::mem::take(&mut *cache);
        let mut current = Probes::default();
        // Upstream's lsof set plus direct/weak Mach-O dependencies of process images.
        let mut paths = BTreeSet::new();
        if let Ok(output) = self.commands.run("/usr/sbin/lsof", &["-Fn", "/"]) {
            paths.extend(
                output
                    .lines()
                    .filter_map(|l| l.strip_prefix('n'))
                    .filter(|p| p.starts_with('/'))
                    .map(PathBuf::from),
            );
        } else {
            self.missing.insert(category);
        }
        if let Ok(output) = self.commands.run("/bin/ps", &["-axo", "comm="]) {
            for executable in output.lines().map(str::trim).filter(|p| p.starts_with('/')) {
                let executable = Path::new(executable);
                if proxy_probe_allowed(&self.roots.home, executable)
                    && let Some(dependencies) = probe(
                        executable,
                        &mut previous.images,
                        &mut current.images,
                        || {
                            Ok(macho_file(executable, own_data)?
                                .into_iter()
                                .flat_map(|(_, dependencies)| dependencies)
                                .collect())
                        },
                    )
                {
                    paths.extend(
                        dependencies
                            .into_iter()
                            .filter(|p| p.starts_with('/'))
                            .map(PathBuf::from),
                    );
                }
            }
        }
        for path in paths {
            if !proxy_probe_allowed(&self.roots.home, &path) {
                continue;
            }
            if probe(&path, &mut previous.files, &mut current.files, || {
                Ok(macho_file(&path, own_data)?.iter().any(|(proxy, _)| *proxy))
            }) == Some(true)
            {
                self.add(category, &path, None, None, None);
            }
        }
        *cache = current;
    }
}
/// What the last scan read from a file that has not changed since. lsof lists every file
/// open on the volume (about 13,000 on a developer's Mac) and ps every process image; a
/// guarded open of each, on every fallback scan, was most of this probe although nearly
/// all of them were the same files as last time. A hit opens nothing, so it cannot touch
/// protected data; new, changed or previously rejected files get the full guarded open.
struct Probed<T> {
    identity: [i64; 7],
    /// None: rejected in this scan only; parent paths or protection state may change.
    found: Option<T>,
}
/// Keyed by a hash of the path, not the path: the identity already names the file.
type ProbeMap<T> = std::collections::HashMap<u64, Probed<T>>;
#[derive(Default)]
struct Probes {
    images: ProbeMap<Vec<String>>,
    files: ProbeMap<bool>,
}
static PROBED: std::sync::OnceLock<Mutex<Probes>> = std::sync::OnceLock::new();
fn probe<T: Clone>(
    path: &Path,
    previous: &mut ProbeMap<T>,
    current: &mut ProbeMap<T>,
    read: impl FnOnce() -> Result<T, AppError>,
) -> Option<T> {
    use std::{
        hash::{Hash, Hasher},
        os::unix::fs::MetadataExt,
    };
    let mut hasher = std::collections::hash_map::DefaultHasher::new();
    path.hash(&mut hasher);
    let key = hasher.finish();
    if let Some(done) = current.get(&key) {
        return done.found.clone();
    }
    // Content changes move ctime, which no unprivileged process can set back.
    let m = std::fs::symlink_metadata(path).ok()?;
    let identity = [
        m.dev() as i64,
        m.ino() as i64,
        m.size() as i64,
        m.mtime(),
        m.mtime_nsec(),
        m.ctime(),
        m.ctime_nsec(),
    ];
    let found = match previous.remove(&key) {
        Some(old) if old.identity == identity && old.found.is_some() => old.found,
        _ => match read() {
            Ok(found) => Some(found),
            Err(error) if error.code == "E_PROTECTED" => None,
            // Not remembered: an I/O failure may be transient.
            Err(_) => return None,
        },
    };
    current.insert(
        key,
        Probed {
            identity,
            found: found.clone(),
        },
    );
    found
}
// lsof lists documents and Photos databases as well as code. Reject private data
// lexically BEFORE metadata/header reads, including the Data volume alias and
// photo libraries moved outside ~/Pictures. Do not canonicalize protected paths.
fn proxy_probe_allowed(home: &Path, path: &Path) -> bool {
    if files::app_update_backup(path) {
        return false;
    }
    let visible = |p: &Path| {
        p.strip_prefix("/System/Volumes/Data")
            .map(|p| Path::new("/").join(p))
            .unwrap_or_else(|_| p.to_owned())
    };
    let path = visible(path);
    let home = visible(home);
    !path.ancestors().any(|p| {
        p.extension()
            .is_some_and(|e| e.eq_ignore_ascii_case("photoslibrary"))
    }) && ![
        "Downloads",
        "Documents",
        "Desktop",
        "Pictures",
        "Library/Photos",
        "Library/AddressBook",
        "Library/Calendars",
        "Library/Mail",
        "Library/Messages",
        "Library/Safari",
        "Library/Cookies",
        "Library/HomeKit",
        "Library/IdentityServices",
        "Library/Metadata",
        "Library/Suggestions",
    ]
    .iter()
    .any(|s| path.starts_with(home.join(s)))
}
fn cron_job(s: &str) -> bool {
    if s.starts_with('#') || s.is_empty() {
        return false;
    }
    if s.starts_with('*') || s.starts_with('@') || s.as_bytes()[0].is_ascii_digit() {
        return true;
    }
    s.split_once('=').is_some_and(|(k, _)| {
        let k = k.trim();
        !k.is_empty()
            && (k.as_bytes()[0].is_ascii_alphabetic() || k.starts_with('_'))
            && k.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'_')
    })
}
fn extension_paths(text: &str) -> impl Iterator<Item = &str> {
    text.lines()
        .filter(|s| s.starts_with('+'))
        .filter_map(|s| s.find('/').map(|i| s[i..].trim()))
}
/// Read just load-command metadata, never the executable's multi-megabyte body.
fn macho_file(
    path: &Path,
    own_data: Option<&own_data::OwnData>,
) -> Result<Vec<(bool, Vec<String>)>, AppError> {
    use std::os::unix::fs::FileExt;
    let file = files::open_read_guarded(path, files::MAX_REGULAR_FILE, own_data)?;
    let length = file.metadata()?.len();
    let read = |offset: u64, count: usize| -> Result<Vec<u8>, AppError> {
        if offset
            .checked_add(count as u64)
            .is_none_or(|end| end > length)
            || count > 4 * 1024 * 1024
        {
            return Err(AppError::new("E_PROTECTED", "startup_macho_bounds"));
        }
        let mut bytes = vec![0; count];
        file.read_exact_at(&mut bytes, offset)?;
        Ok(bytes)
    };
    let head = read(0, 8)?;
    let magic = u32::from_be_bytes(head[..4].try_into().unwrap());
    let fat = matches!(magic, 0xcafebabe | 0xbebafeca | 0xcafebabf | 0xbfbafeca);
    let mut slices = vec![(0, length)];
    if fat {
        let little = matches!(magic, 0xbebafeca | 0xbfbafeca);
        let count = if little {
            u32::from_le_bytes(head[4..8].try_into().unwrap())
        } else {
            u32::from_be_bytes(head[4..8].try_into().unwrap())
        }
        .min(32) as usize;
        let wide = matches!(magic, 0xcafebabf | 0xbfbafeca);
        let size = if wide { 32 } else { 20 };
        let table = read(8, count * size)?;
        slices.clear();
        for row in table.chunks_exact(size) {
            let word = |at: usize| -> u64 {
                if wide {
                    let bytes = row[at..at + 8].try_into().unwrap();
                    if little {
                        u64::from_le_bytes(bytes)
                    } else {
                        u64::from_be_bytes(bytes)
                    }
                } else {
                    let bytes = row[at..at + 4].try_into().unwrap();
                    u64::from(if little {
                        u32::from_le_bytes(bytes)
                    } else {
                        u32::from_be_bytes(bytes)
                    })
                }
            };
            slices.push((word(8), word(if wide { 16 } else { 12 })));
        }
    }
    let mut all = vec![];
    for (offset, size) in slices {
        if size < 28 || offset.checked_add(size).is_none_or(|end| end > length) {
            continue;
        }
        let head = read(offset, 28)?;
        let magic = u32::from_be_bytes(head[..4].try_into().unwrap());
        let (little, header) = match magic {
            0xcffaedfe => (true, 32),
            0xcefaedfe => (true, 28),
            0xfeedfacf => (false, 32),
            0xfeedface => (false, 28),
            _ => continue,
        };
        let bytes = head[20..24].try_into().unwrap();
        let commands = if little {
            u32::from_le_bytes(bytes)
        } else {
            u32::from_be_bytes(bytes)
        } as usize;
        if (header + commands) as u64 > size {
            continue;
        }
        all.extend(macho(&read(offset, header + commands)?));
    }
    Ok(all)
}

/// Mach-O and universal Mach-O parser: checked offsets, bounded load commands.
fn macho(bytes: &[u8]) -> Vec<(bool, Vec<String>)> {
    fn word(b: &[u8], o: usize, le: bool) -> Option<u32> {
        let a = b.get(o..o + 4)?.try_into().ok()?;
        Some(if le {
            u32::from_le_bytes(a)
        } else {
            u32::from_be_bytes(a)
        })
    }
    let Some(magic) = word(bytes, 0, false) else {
        return vec![];
    };
    if matches!(magic, 0xcafebabe | 0xbebafeca) {
        let le = magic == 0xbebafeca;
        let count = word(bytes, 4, le).unwrap_or(0).min(32);
        let mut all = vec![];
        for i in 0..count as usize {
            let start = word(bytes, 8 + i * 20 + 8, le).unwrap_or(u32::MAX) as usize;
            let length = word(bytes, 8 + i * 20 + 12, le).unwrap_or(0) as usize;
            if let Some(b) = start
                .checked_add(length)
                .and_then(|end| bytes.get(start..end))
                && b.len() >= 4
                && !matches!(word(b, 0, false), Some(0xcafebabe | 0xbebafeca))
            {
                all.extend(macho(b));
            }
        }
        return all;
    }
    let (le, header) = match magic {
        0xcffaedfe => (true, 32),
        0xcefaedfe => (true, 28),
        0xfeedfacf => (false, 32),
        0xfeedface => (false, 28),
        _ => return vec![],
    };
    let dylib = word(bytes, 12, le) == Some(6);
    let n = word(bytes, 16, le).unwrap_or(0).min(65536);
    let limit = word(bytes, 20, le).unwrap_or(0) as usize + header;
    let mut offset = header;
    let mut reexport = false;
    let mut dependencies = vec![];
    for _ in 0..n {
        let Some(cmd) = word(bytes, offset, le) else {
            break;
        };
        let size = word(bytes, offset + 4, le).unwrap_or(0) as usize;
        if size < 8
            || offset
                .checked_add(size)
                .is_none_or(|e| e > bytes.len() || e > limit)
        {
            break;
        }
        if cmd == 0x8000001f {
            reexport = true;
        }
        if matches!(cmd, 0xc | 0x80000018 | 0x8000001f) {
            let name = word(bytes, offset + 8, le).unwrap_or(u32::MAX) as usize;
            if name < size
                && let Some(raw) = bytes.get(offset + name..offset + size)
            {
                let end = raw.iter().position(|b| *b == 0).unwrap_or(raw.len());
                if let Ok(s) = std::str::from_utf8(&raw[..end]) {
                    dependencies.push(s.to_owned());
                }
            }
        }
        offset += size;
    }
    vec![(dylib && reexport, dependencies)]
}
#[cfg(target_os = "macos")]
pub(crate) fn login_items() -> Result<Vec<PathBuf>, AppError> {
    use std::ffi::c_void;
    type Ref = *const c_void;
    #[link(name = "CoreServices", kind = "framework")]
    unsafe extern "C" {
        static kLSSharedFileListSessionLoginItems: Ref;
        fn LSSharedFileListCreate(a: Ref, t: Ref, o: Ref) -> Ref;
        fn LSSharedFileListCopySnapshot(l: Ref, s: *mut u32) -> Ref;
        fn LSSharedFileListItemResolve(i: Ref, f: u32, u: *mut Ref, r: *mut c_void) -> i32;
    }
    #[link(name = "CoreFoundation", kind = "framework")]
    unsafe extern "C" {
        fn CFRelease(r: Ref);
        fn CFArrayGetCount(r: Ref) -> isize;
        fn CFArrayGetValueAtIndex(r: Ref, i: isize) -> Ref;
        fn CFURLGetFileSystemRepresentation(r: Ref, resolve: u8, b: *mut u8, n: isize) -> u8;
    }
    unsafe {
        let list = LSSharedFileListCreate(
            std::ptr::null(),
            kLSSharedFileListSessionLoginItems,
            std::ptr::null(),
        );
        if list.is_null() {
            return Err(AppError::new(
                "E_FULL_DISK_ACCESS",
                "login_items_unavailable",
            ));
        }
        let mut seed = 0;
        let items = LSSharedFileListCopySnapshot(list, &mut seed);
        CFRelease(list);
        if items.is_null() {
            return Ok(vec![]);
        }
        let mut paths = vec![];
        for i in 0..CFArrayGetCount(items).min(10000) {
            let mut url = std::ptr::null();
            if LSSharedFileListItemResolve(
                CFArrayGetValueAtIndex(items, i),
                3,
                &mut url,
                std::ptr::null_mut(),
            ) == 0
                && !url.is_null()
            {
                let mut b = [0; 4096];
                if CFURLGetFileSystemRepresentation(url, 0, b.as_mut_ptr(), 4096) != 0 {
                    let n = b.iter().position(|b| *b == 0).unwrap_or(b.len());
                    if let Ok(s) = std::str::from_utf8(&b[..n]) {
                        paths.push(PathBuf::from(s));
                    }
                }
                CFRelease(url);
            }
        }
        CFRelease(items);
        Ok(paths)
    }
}
#[cfg(not(target_os = "macos"))]
pub(crate) fn login_items() -> Result<Vec<PathBuf>, AppError> {
    Ok(vec![])
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn application_plugins_skip_aliases_but_report_unreadable_real_bundles() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let applications = root.join("Applications");
        let app = applications.join("Utilities/Current.app");
        std::fs::create_dir_all(app.join("Contents/PlugIns/Test.bundle")).unwrap();
        std::fs::write(app.join("Contents/Info.plist"),
            b"<?xml version=\"1.0\"?><plist version=\"1.0\"><dict><key>NSDockTilePlugIn</key><string>Test.bundle</string></dict></plist>").unwrap();
        // Finder aliases are files; system apps can also be symbolic links.
        std::fs::write(applications.join("Alias.app"), b"alias fixture").unwrap();
        std::os::unix::fs::symlink(&app, applications.join("Linked.app")).unwrap();
        std::os::unix::fs::symlink(root.join("Absent.app"), applications.join("Broken.app"))
            .unwrap();
        let roots = Roots {
            home: root.join("home"),
            root,
            applications: vec![applications.clone()],
        };
        let commands = crate::security::tests::FakeCommands::default();
        let mut scanner = Scanner {
            roots: &roots,
            commands: &commands,
            own_data: None,
            items: BTreeMap::new(),
            missing: BTreeSet::new(),
            btm_format: None,
            hints: BTreeMap::new(),
            user_overrides: None,
            system_overrides: None,
            user_uuid: None,
        };
        scanner.application_plugins();
        assert_eq!(scanner.items.len(), 1);
        assert_eq!(
            scanner.items.values().next().unwrap().path,
            app.join("Contents/PlugIns/Test.bundle").to_string_lossy()
        );
        assert!(scanner.missing.is_empty());

        let invalid = applications.join("Invalid.app/Contents");
        std::fs::create_dir_all(&invalid).unwrap();
        std::fs::write(invalid.join("Info.plist"), b"not a plist").unwrap();
        scanner.application_plugins();
        assert!(scanner.missing.contains(&StartupCategory::DockTiles));
    }
    #[test]
    fn application_plugins_and_registered_candidates_ignore_both_backup_roots() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let applications = root.join("Applications");
        let current = applications.join("Current.app");
        let backups = [
            applications.join(".opennavo-updates/id/previous.app"),
            applications.join(".opennavo-updates.noindex/id/previous.app"),
        ];
        for app in std::iter::once(&current).chain(&backups) {
            std::fs::create_dir_all(app.join("Contents/PlugIns/Test.bundle")).unwrap();
            std::fs::write(app.join("Contents/Info.plist"),
                b"<?xml version=\"1.0\"?><plist version=\"1.0\"><dict><key>NSDockTilePlugIn</key><string>Test.bundle</string></dict></plist>").unwrap();
        }
        let roots = Roots {
            home: root.join("home"),
            root: root.clone(),
            applications: vec![applications],
        };
        let commands = crate::security::tests::FakeCommands::default();
        let mut scanner = Scanner {
            roots: &roots,
            commands: &commands,
            own_data: None,
            items: BTreeMap::new(),
            missing: BTreeSet::new(),
            btm_format: None,
            hints: BTreeMap::new(),
            user_overrides: None,
            system_overrides: None,
            user_uuid: None,
        };
        scanner.application_plugins();
        assert_eq!(scanner.items.len(), 1);
        assert_eq!(
            scanner.items.values().next().unwrap().path,
            current
                .join("Contents/PlugIns/Test.bundle")
                .to_string_lossy()
        );
        for backup in backups {
            assert!(
                scanner
                    .entries(&backup, StartupCategory::DockTiles)
                    .is_empty()
            );
            assert!(
                scanner
                    .plist(
                        &backup.join("Contents/Info.plist"),
                        StartupCategory::DockTiles
                    )
                    .is_none()
            );
            assert!(
                scanner
                    .add(StartupCategory::AppExtensions, &backup, None, None, None)
                    .is_none()
            );
            assert!(!proxy_probe_allowed(
                &roots.home,
                &backup.join("Contents/MacOS/Previous")
            ));
        }
        assert!(scanner.missing.is_empty());
    }
    #[test]
    fn proxy_candidates_exclude_private_data_before_file_probes() {
        let home = Path::new("/Users/fixture");
        for private in [
            "Downloads/install.dmg",
            "Documents/code/main",
            "Desktop/report.pdf",
            "Pictures/Photos Library.photoslibrary/database/Photos.sqlite",
            "Library/Photos/Libraries/Syndication.photoslibrary/database/Photos.sqlite",
        ] {
            let path = home.join(private);
            assert!(!proxy_probe_allowed(home, &path));
            assert!(!proxy_probe_allowed(
                home,
                &Path::new("/System/Volumes/Data").join(path.strip_prefix("/").unwrap())
            ));
        }
        assert!(!proxy_probe_allowed(
            home,
            Path::new("/Volumes/External/Moved.PHOTOSLIBRARY/database/Photos.sqlite")
        ));
        assert!(proxy_probe_allowed(
            home,
            Path::new("/Applications/Fixture.app/Contents/MacOS/main")
        ));
        assert!(proxy_probe_allowed(
            home,
            &home.join("Library/Frameworks/Fixture.framework/main")
        ));
    }
    #[test]
    fn proxy_detection_reads_thin_and_universal_load_commands_without_the_body() {
        use std::os::unix::fs::FileExt;
        let mut thin = vec![];
        for word in [
            0xfeedfacf_u32,
            0x100000c,
            0,
            6,
            1,
            32,
            0,
            0,
            0x8000001f,
            32,
            24,
            0,
            0,
            0,
        ] {
            thin.extend(word.to_le_bytes());
        }
        thin.extend(b"/abc\0\0\0\0");
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        for wide in [false, true] {
            let path = root.join(if wide { "fat" } else { "thin" });
            let file = std::fs::File::create(&path).unwrap();
            file.set_len(90 * 1024 * 1024).unwrap();
            if wide {
                let mut header = vec![];
                for word in [0xcafebabe_u32, 1, 0x100000c, 0, 4096, thin.len() as u32, 12] {
                    header.extend(word.to_be_bytes());
                }
                file.write_all_at(&header, 0).unwrap();
                file.write_all_at(&thin, 4096).unwrap();
            } else {
                file.write_all_at(&thin, 0).unwrap();
            }
            assert_eq!(
                macho_file(&path, None).unwrap(),
                vec![(true, vec!["/abc".into()])]
            );
        }
    }
    /// A thin arm64 Mach-O whose only load command names `library`.
    fn macho_bytes(filetype: u32, command: u32, library: &Path) -> Vec<u8> {
        let mut name = library.as_os_str().as_encoded_bytes().to_vec();
        name.resize((name.len() / 8 + 1) * 8, 0);
        let size = 24 + name.len() as u32;
        let mut bytes = vec![];
        for word in [
            0xfeedfacf_u32,
            0x100000c,
            0,
            filetype,
            1,
            size,
            0,
            0,
            command,
            size,
            24,
            0,
            0,
            0,
        ] {
            bytes.extend(word.to_le_bytes());
        }
        bytes.extend(name);
        bytes
    }
    #[test]
    fn probes_reread_only_new_or_changed_files() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().canonicalize().unwrap().join("file");
        std::fs::write(&path, "first").unwrap();
        let reads = std::cell::Cell::new(0);
        let mut previous = std::collections::HashMap::new();
        let round = |previous: &mut std::collections::HashMap<_, _>,
                     result: Result<u32, AppError>| {
            let mut current = std::collections::HashMap::new();
            let read = || {
                reads.set(reads.get() + 1);
                result.clone()
            };
            let found = probe(&path, previous, &mut current, read);
            // The same path again in one scan (ps lists an image once per process).
            assert_eq!(probe(&path, previous, &mut current, read), found);
            *previous = current;
            found
        };
        assert_eq!(round(&mut previous, Ok(1)), Some(1));
        assert_eq!(round(&mut previous, Ok(2)), Some(1));
        assert_eq!(reads.get(), 1);
        std::fs::write(&path, "second").unwrap();
        assert_eq!(round(&mut previous, Ok(2)), Some(2));
        assert_eq!(reads.get(), 2);
        // A rejection is retried next scan even without file metadata changes.
        std::fs::write(&path, "third").unwrap();
        let rejected = AppError::new("E_PROTECTED", "security_own_data");
        assert_eq!(round(&mut previous, Err(rejected)), None);
        assert_eq!(reads.get(), 3);
        assert_eq!(round(&mut previous, Ok(3)), Some(3));
        assert_eq!(reads.get(), 4);
        std::fs::write(&path, "fourth").unwrap();
        let failed = AppError::new("E_UNKNOWN", "io");
        assert_eq!(round(&mut previous, Err(failed)), None);
        // Not remembered even within the scan: its repeat was read again.
        assert_eq!(reads.get(), 6);
        assert_eq!(round(&mut previous, Ok(4)), Some(4));
        assert_eq!(reads.get(), 7);
        std::fs::remove_file(&path).unwrap();
        assert_eq!(round(&mut previous, Ok(5)), None);
        assert!(previous.is_empty());
    }
    #[test]
    fn probes_retry_after_a_symlinked_parent_is_restored() {
        use std::os::unix::fs::MetadataExt;
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let parent = root.join("libraries");
        let moved = root.join("moved");
        std::fs::create_dir(&moved).unwrap();
        let path = parent.join("proxy.dylib");
        std::fs::write(
            moved.join("proxy.dylib"),
            macho_bytes(6, 0x8000001f, Path::new("/usr/lib/libSystem.B.dylib")),
        )
        .unwrap();
        std::os::unix::fs::symlink(&moved, &parent).unwrap();
        let identity = || {
            let m = std::fs::symlink_metadata(&path).unwrap();
            (
                m.dev(),
                m.ino(),
                m.size(),
                m.mtime(),
                m.mtime_nsec(),
                m.ctime(),
                m.ctime_nsec(),
            )
        };
        let before = identity();
        assert_eq!(macho_file(&path, None).unwrap_err().code, "E_PROTECTED");
        let mut previous = ProbeMap::default();
        let round = |previous: &mut ProbeMap<_>| {
            let mut current = ProbeMap::default();
            let found = probe(&path, previous, &mut current, || macho_file(&path, None));
            *previous = current;
            found
        };
        assert_eq!(round(&mut previous), None);
        assert_eq!(round(&mut previous), None);
        std::fs::remove_file(&parent).unwrap();
        std::fs::rename(&moved, &parent).unwrap();
        assert_eq!(identity(), before);
        let expected = Some(vec![(true, vec!["/usr/lib/libSystem.B.dylib".into()])]);
        assert_eq!(round(&mut previous), expected);
        assert_eq!(round(&mut previous), expected);
    }
    #[test]
    fn repeated_scans_report_the_same_proxies_and_follow_changes() {
        use super::super::Commands;
        struct Listed(PathBuf, PathBuf);
        impl Commands for Listed {
            fn run(&self, program: &str, _: &[&str]) -> Result<String, AppError> {
                Ok(match program {
                    "/usr/sbin/lsof" => format!("p1\nn{}\nn/dev/null\n", self.0.display()),
                    "/bin/ps" => format!("{0}\n{0}\n", self.1.display()),
                    _ => String::new(),
                })
            }
        }
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let open = root.join("open.dylib");
        let linked = root.join("linked.dylib");
        let image = root.join("image");
        let proxy = macho_bytes(6, 0x8000001f, Path::new("/usr/lib/libSystem.B.dylib"));
        std::fs::write(&open, &proxy).unwrap();
        std::fs::write(&linked, &proxy).unwrap();
        std::fs::write(&image, macho_bytes(2, 0xc, &linked)).unwrap();
        let roots = Roots {
            home: root.join("home"),
            root: root.join("system"),
            applications: vec![],
        };
        let commands = Listed(open.clone(), image);
        let proxies = || {
            scan(&roots, &commands)
                .items
                .into_iter()
                .filter(|item| item.category == StartupCategory::LibraryProxies)
                .map(|item| PathBuf::from(item.path))
                .collect::<BTreeSet<_>>()
        };
        let both = BTreeSet::from([open.clone(), linked.clone()]);
        assert_eq!(proxies(), both);
        assert_eq!(proxies(), both);
        std::fs::write(&open, b"no longer a library").unwrap();
        assert_eq!(proxies(), BTreeSet::from([linked.clone()]));
        std::fs::write(&open, &proxy).unwrap();
        assert_eq!(proxies(), both);
    }
    #[test]
    fn cron_assignments_are_persistence_too() {
        assert!(cron_job("SHELL = /tmp/evil"));
        assert!(cron_job("@reboot /tmp/a"));
        assert!(!cron_job("# ignored"));
    }
    #[test]
    fn malformed_macho_never_panics() {
        for n in 0..100 {
            assert!(macho(&vec![255; n]).is_empty());
        }
    }
}
