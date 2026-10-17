//! Independently implemented rules. Never traverse links while expanding patterns or measuring files.
use crate::{AppError, model::*};
use serde::{Deserialize, Serialize};
use std::{
    fs,
    path::{Component, Path, PathBuf},
    sync::atomic::AtomicBool,
    time::{Duration, Instant},
};

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Roots {
    pub home: PathBuf,
    pub applications: Vec<PathBuf>,
    pub system_library: PathBuf,
    pub bins: Vec<PathBuf>,
    pub receipts: PathBuf,
    pub temporary: Vec<PathBuf>,
    pub caskrooms: Vec<PathBuf>,
}
impl Roots {
    pub fn native() -> Self {
        let home = PathBuf::from(std::env::var_os("HOME").unwrap_or_default());
        let mut temporary: Vec<_> = std::env::var_os("TMPDIR")
            .and_then(|p| PathBuf::from(p).canonicalize().ok())
            .into_iter()
            .collect();
        if let Some(cache) = temporary
            .first()
            .and_then(|p| p.parent())
            .map(|p| p.join("C"))
        {
            temporary.push(cache);
        }
        Self {
            applications: vec![PathBuf::from("/Applications"), home.join("Applications")],
            home,
            system_library: "/Library".into(),
            bins: vec!["/usr/local/bin".into(), "/opt/homebrew/bin".into()],
            receipts: "/private/var/db/receipts".into(),
            temporary,
            caskrooms: vec![
                "/opt/homebrew/Caskroom".into(),
                "/usr/local/Caskroom".into(),
            ],
        }
    }
    pub fn locations(&self) -> Vec<(PathBuf, ItemCategory)> {
        let mut result = Vec::new();
        for library in [self.home.join("Library"), self.system_library.clone()] {
            for (relative, category) in [
                ("Application Support", ItemCategory::AppData),
                ("Containers", ItemCategory::AppData),
                ("Group Containers", ItemCategory::AppData),
                ("Application Scripts", ItemCategory::AppData),
                ("Caches", ItemCategory::Caches),
                ("HTTPStorages", ItemCategory::Caches),
                ("WebKit", ItemCategory::Caches),
                ("Preferences", ItemCategory::Preferences),
                ("Preferences/ByHost", ItemCategory::Preferences),
                ("Saved Application State", ItemCategory::Preferences),
                ("Cookies", ItemCategory::Preferences),
                ("Logs", ItemCategory::Logs),
                ("Logs/DiagnosticReports", ItemCategory::Logs),
                ("LaunchAgents", ItemCategory::Background),
                ("LaunchDaemons", ItemCategory::Background),
                ("PrivilegedHelperTools", ItemCategory::Background),
                ("Internet Plug-Ins", ItemCategory::Plugins),
                ("Audio/Plug-Ins", ItemCategory::Plugins),
                ("Audio/Plug-Ins/Components", ItemCategory::Plugins),
                ("Audio/Plug-Ins/VST", ItemCategory::Plugins),
                ("Audio/Plug-Ins/VST3", ItemCategory::Plugins),
                ("QuickLook", ItemCategory::Plugins),
                ("PreferencePanes", ItemCategory::Plugins),
                ("Services", ItemCategory::Plugins),
                ("Spotlight", ItemCategory::Plugins),
                ("Screen Savers", ItemCategory::Plugins),
                ("Input Methods", ItemCategory::Plugins),
            ] {
                result.push((library.join(relative), category));
            }
        }
        result.extend(
            self.temporary
                .iter()
                .cloned()
                .map(|p| (p, ItemCategory::Caches)),
        );
        result.extend(self.bins.iter().cloned().map(|p| (p, ItemCategory::Cli)));
        result
    }
    pub fn allowed(&self, path: &Path, reason: MatchReason, app: Option<&Path>) -> bool {
        if !path.is_absolute()
            || path
                .components()
                .any(|c| matches!(c, Component::ParentDir | Component::CurDir))
            || !parents_safe(path)
        {
            return false;
        }
        if reason == MatchReason::AppBundle {
            return app == Some(path)
                && self.applications.iter().any(|root| {
                    path.strip_prefix(root).is_ok_and(|relative| {
                        let count = relative.components().count();
                        (1..=2).contains(&count) && path.extension().is_some_and(|e| e == "app")
                    })
                });
        }
        // Only this application's exact recovery roots, never the shared root
        // or arbitrary descendants of Applications. Rechecked by admin_batch.
        if reason == MatchReason::NameExact
            && let Some(app) = app
            && self.allowed(app, MatchReason::AppBundle, Some(app))
            && crate::updater::backup::is_directory(app, path)
        {
            return true;
        }
        // Never remove scan roots (including nested scan locations), other applications or protected user data.
        let locations = self.locations();
        if locations.iter().any(|(root, _)| root == path) {
            return false;
        }
        if self.applications.iter().any(|root| path.starts_with(root)) {
            return false;
        }
        if path.starts_with(&self.home) && !path.starts_with(self.home.join("Library")) {
            if reason != MatchReason::ZapRule {
                return false;
            }
            let Some(first) = path
                .strip_prefix(&self.home)
                .ok()
                .and_then(|r| r.components().next())
            else {
                return false;
            };
            let name = first.as_os_str().to_string_lossy();
            return name.starts_with('.')
                && name.len() > 1
                && ![
                    ".Trash", ".ssh", ".gnupg", ".aws", ".kube", ".config", ".local",
                ]
                .contains(&name.as_ref());
        }
        if path.starts_with(&self.receipts) {
            return reason == MatchReason::PkgReceipt
                && path.parent() == Some(self.receipts.as_path())
                && path.extension().is_some_and(|e| e == "bom" || e == "plist");
        }
        if self.bins.iter().any(|root| path.starts_with(root)) {
            return reason == MatchReason::Symlink
                && fs::symlink_metadata(path).is_ok_and(|m| m.is_symlink());
        }
        // TMPDIR is per-user; accept its own C cache sibling, never arbitrary /private/var/folders.
        locations
            .iter()
            .any(|(root, _)| path.starts_with(root) && path != root)
    }
    pub fn category(&self, path: &Path) -> ItemCategory {
        self.locations()
            .into_iter()
            .filter(|(root, _)| path.starts_with(root))
            .max_by_key(|(root, _)| root.components().count())
            .map(|(_, c)| c)
            .unwrap_or(ItemCategory::AppData)
    }
    pub fn admin(&self, path: &Path) -> bool {
        if path.starts_with(&self.system_library)
            || path.starts_with(&self.receipts)
            || self
                .bins
                .iter()
                .any(|p| path.starts_with(p) && p.starts_with("/usr/local"))
        {
            return true;
        }
        #[cfg(unix)]
        {
            use std::os::unix::fs::MetadataExt;
            if fs::symlink_metadata(path).is_ok_and(|m| m.uid() == 0) {
                return true;
            }
        }
        false
    }
}

pub fn parents_safe(path: &Path) -> bool {
    path.parent().is_some_and(|parent| {
        parent
            .ancestors()
            .all(|p| fs::symlink_metadata(p).is_ok_and(|m| m.is_dir() && !m.is_symlink()))
    })
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Snapshot {
    pub kind: u8,
    pub length: u64,
    #[serde(deserialize_with = "snapshot_nanoseconds")]
    pub modified: u128,
    pub device: u64,
    pub inode: u64,
    pub link: Option<PathBuf>,
    pub parents: Vec<(PathBuf, u64, u64)>,
}
// Internally tagged admin actions buffer their contents, whose deserializer does not support
// deserialize_u128. Number uses deserialize_any instead; arbitrary_precision preserves all digits
// even above u64::MAX. Keep the existing numeric wire format and reject fractions/negatives/overflow.
fn snapshot_nanoseconds<'de, D: serde::Deserializer<'de>>(
    deserializer: D,
) -> Result<u128, D::Error> {
    serde_json::Number::deserialize(deserializer)?
        .as_u128()
        .ok_or_else(|| {
            serde::de::Error::custom("snapshot modified must be an unsigned 128-bit integer")
        })
}
impl Snapshot {
    pub fn capture(path: &Path) -> Result<Self, AppError> {
        if !parents_safe(path) {
            return Err(AppError::new("E_PROTECTED", "symlink_parent"));
        }
        let m = fs::symlink_metadata(path)?;
        let kind = if m.is_symlink() {
            2
        } else if m.is_dir() {
            1
        } else if m.is_file() {
            0
        } else {
            return Err(AppError::new("E_PROTECTED", "special_file"));
        };
        #[cfg(unix)]
        let (device, inode) = {
            use std::os::unix::fs::MetadataExt;
            (m.dev(), m.ino())
        };
        #[cfg(not(unix))]
        let (device, inode) = (0, 0);
        let parents = path
            .parent()
            .into_iter()
            .flat_map(Path::ancestors)
            .map(|p| {
                let m = fs::symlink_metadata(p)?;
                #[cfg(unix)]
                let (d, i) = {
                    use std::os::unix::fs::MetadataExt;
                    (m.dev(), m.ino())
                };
                #[cfg(not(unix))]
                let (d, i) = (0, 0);
                Ok((p.to_owned(), d, i))
            })
            .collect::<Result<Vec<_>, std::io::Error>>()?;
        Ok(Self {
            kind,
            length: m.len(),
            modified: m
                .modified()?
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap_or_default()
                .as_nanos(),
            device,
            inode,
            link: if kind == 2 {
                Some(fs::read_link(path)?)
            } else {
                None
            },
            parents,
        })
    }
    pub fn matches(&self, path: &Path) -> bool {
        Self::capture(path).is_ok_and(|now| now == *self)
    }
    /// The path still names the object the plan saw, although its contents may have changed since: same kind,
    /// link target and parent chain, and a directory or link keeps its inode. A regular file may have been replaced
    /// atomically (preferences are rewritten that way when an app quits); removing it only unlinks an entry of a
    /// verified directory, so the action cannot be redirected elsewhere.
    pub fn same_object(&self, now: &Snapshot) -> bool {
        self.kind == now.kind
            && self.parents == now.parents
            && self.link == now.link
            && (self.kind == 0 || (self.device == now.device && self.inode == now.inode))
    }
    /// `same_object` against the current state of `path`.
    pub fn still(&self, path: &Path) -> bool {
        Self::capture(path).is_ok_and(|now| self.same_object(&now))
    }
    /// This object after a same-volume rename to `destination`: inode, size and times stay, only the parent chain
    /// changes. Used when the moved item itself cannot be examined (the Trash is privacy-protected without Full Disk
    /// Access), so a later undo still recognises it.
    pub fn moved_to(&self, destination: &Path) -> Result<Self, AppError> {
        let parents = destination
            .parent()
            .into_iter()
            .flat_map(Path::ancestors)
            .map(|p| {
                let m = fs::symlink_metadata(p)?;
                #[cfg(unix)]
                let (d, i) = {
                    use std::os::unix::fs::MetadataExt;
                    (m.dev(), m.ino())
                };
                #[cfg(not(unix))]
                let (d, i) = (0, 0);
                Ok((p.to_owned(), d, i))
            })
            .collect::<Result<Vec<_>, std::io::Error>>()?;
        Ok(Self {
            parents,
            ..self.clone()
        })
    }
}

pub fn normalize(name: &str) -> String {
    name.to_lowercase()
        .chars()
        .filter(|c| !c.is_whitespace() && !"-_.".contains(*c))
        .collect()
}
pub fn meaningful(name: &str) -> bool {
    name.chars().count() >= 4
        && ![
            "app",
            "apps",
            "application",
            "applications",
            "helper",
            "helpers",
            "update",
            "updater",
            "updates",
            "mac",
            "macos",
            "pro",
            "studio",
            "service",
            "services",
            "support",
            "cache",
            "caches",
            "data",
            "shared",
            "common",
            "installer",
            "system",
            "desktop",
        ]
        .contains(&name)
}
pub fn bundle_matches(name: &str, id: &str) -> bool {
    !id.is_empty()
        && (name == id
            || name
                .strip_prefix(id)
                .is_some_and(|suffix| suffix.starts_with('.')))
}
pub fn name_match(name: &str, app: &str) -> Option<(MatchReason, PlanItemConfidence)> {
    let n = normalize(name);
    let a = normalize(app);
    if !meaningful(&n) || !meaningful(&a) {
        return None;
    }
    if n == a {
        Some((MatchReason::NameExact, PlanItemConfidence::Medium))
    } else if n.contains(&a) || a.contains(&n) {
        Some((MatchReason::NameSimilar, PlanItemConfidence::Low))
    } else {
        None
    }
}

/// Bound expansion and never ask a shell to interpret a declaration.
pub fn expand(pattern: &str, home: &Path, team: Option<&str>) -> Result<Vec<PathBuf>, AppError> {
    let mut value = if let Some(rest) = pattern.strip_prefix("~/") {
        home.join(rest).to_string_lossy().into_owned()
    } else {
        pattern.to_owned()
    };
    if value.contains("$(TeamIdentifierPrefix)") {
        let Some(team) =
            team.filter(|t| !t.is_empty() && t.chars().all(|c| c.is_ascii_alphanumeric()))
        else {
            return Ok(vec![]);
        };
        value = value.replace("$(TeamIdentifierPrefix)", &format!("{team}."));
    }
    if value.contains('$')
        || value.contains('`')
        || value.contains('\0')
        || !Path::new(&value).is_absolute()
        || Path::new(&value)
            .components()
            .any(|c| matches!(c, Component::ParentDir | Component::CurDir))
    {
        return Ok(vec![]);
    }
    let mut paths = vec![PathBuf::from("/")];
    for component in Path::new(&value).components().filter_map(|c| match c {
        Component::Normal(c) => Some(c.to_string_lossy().into_owned()),
        _ => None,
    }) {
        let mut next = Vec::new();
        let pattern = glob::Pattern::new(&component)
            .map_err(|_| AppError::new("E_INVALID_ARG", "zap_pattern"))?;
        for parent in paths {
            if fs::symlink_metadata(&parent).is_ok_and(|m| m.is_dir() && !m.is_symlink()) {
                if component.contains(['*', '?', '[']) {
                    if let Ok(entries) = fs::read_dir(&parent) {
                        for entry in entries.flatten() {
                            if pattern.matches(&entry.file_name().to_string_lossy()) {
                                next.push(entry.path());
                            }
                            if next.len() > 10_000 {
                                return Err(AppError::new("E_INVALID_ARG", "zap_expansion_limit"));
                            }
                        }
                    }
                } else {
                    let child = parent.join(&component);
                    if fs::symlink_metadata(&child).is_ok() {
                        next.push(child);
                    }
                }
            }
        }
        paths = next;
    }
    Ok(paths)
}

/// Allocated bytes, excluding symlink targets; budget prevents blocking scans on huge data trees.
pub fn allocated_size(path: &Path, budget: Duration) -> Result<u64, AppError> {
    measure_size(path, Some(budget), None).map(|m| m.bytes)
}

/// Finish measuring a leftover directory, allowing the user to cancel the scan.
pub fn allocated_size_complete(path: &Path, cancel: &AtomicBool) -> Result<u64, AppError> {
    measure_size(path, None, Some(cancel)).map(|m| m.bytes)
}

/// Size of a tree and whether part of it could not be read.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Measured {
    pub bytes: u64,
    /// Something below the root was unreadable and is not counted.
    pub partial: bool,
}

/// Like `du`: symlink targets are not followed and a hard-linked file counts once. Entries that disappear during
/// the walk (caches churn constantly) are ignored and unreadable ones below the root are skipped, so one busy or
/// protected subfolder no longer voids the whole measurement. An unreadable root is still an error.
pub fn allocated_size_detail(
    path: &Path,
    budget: Option<Duration>,
    cancel: Option<&AtomicBool>,
) -> Result<Measured, AppError> {
    measure_size(path, budget, cancel)
}

fn measure_size(
    root: &Path,
    budget: Option<Duration>,
    cancel: Option<&AtomicBool>,
) -> Result<Measured, AppError> {
    let start = Instant::now();
    let mut stack = vec![(root.to_owned(), fs::symlink_metadata(root)?)];
    let mut bytes = 0u64;
    let mut partial = false;
    let mut count = 0;
    let mut linked = std::collections::HashSet::new();
    while let Some((path, m)) = stack.pop() {
        count += 1;
        if let Some(cancel) = cancel {
            super::scan::check_cancel(cancel)?;
        }
        if budget.is_some_and(|budget| count > 2_000_000 || start.elapsed() > budget) {
            return Err(AppError::new("E_UNKNOWN", "size_timeout"));
        }
        if m.is_symlink() {
            continue;
        }
        #[cfg(unix)]
        {
            use std::os::unix::fs::MetadataExt;
            if m.is_dir() || m.nlink() < 2 || linked.insert((m.dev(), m.ino())) {
                bytes = bytes.saturating_add(m.blocks().saturating_mul(512));
            }
        }
        #[cfg(not(unix))]
        {
            let _ = &mut linked;
            bytes = bytes.saturating_add(m.len());
        }
        if !m.is_dir() {
            continue;
        }
        let entries = match fs::read_dir(&path) {
            Ok(entries) => entries,
            Err(error) if path == root => return Err(error.into()),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => continue,
            Err(_) => {
                partial = true;
                continue;
            }
        };
        for entry in entries {
            if let Some(cancel) = cancel {
                super::scan::check_cancel(cancel)?;
            }
            let Ok(entry) = entry else {
                partial = true;
                continue;
            };
            let child = entry.path();
            match fs::symlink_metadata(&child) {
                Ok(m) => stack.push((child, m)),
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
                Err(_) => partial = true,
            }
        }
    }
    Ok(Measured { bytes, partial })
}
