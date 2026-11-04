//! What a Local MCP cleanup may remove.
//!
//! The app's own cleanup page can offer anything the scanner found; an AI client
//! must not. This module holds the one rule that decides which of a finished
//! scan's items a tool call may select, so the rule is testable on its own and
//! cannot drift from the confirmation the user sees.
//!
//! Two things are excluded, for different reasons:
//!
//! * Anything that cannot be rebuilt. `Trash` holds files the user deleted,
//!   `DeviceBackups` are the last copy of a phone, `Installers`, `LargeFiles`,
//!   `ProjectArtifacts` and Docker images are review items that a person should
//!   look at, not caches. Removing them through a tool would turn "reclaim space"
//!   into "destroy data".
//! * Anything the scan could not measure completely (`size_complete == false`).
//!   The app already refuses to select those; a tool that could choose them would
//!   be the only path around that guard.
//!
//! Everything else — app caches, logs, browser caches, dev caches, Homebrew's own
//! cache, temp files, orphans — is regenerable, so a tool may offer it.

use crate::cleanup::types::{
    CategoryReport, CleanupCategory, CleanupItem, CleanupItemKind, CleanupReason, CleanupReport,
    CleanupRestore, CleanupRisk,
};

/// Categories whose items are regenerated on demand, so a tool may select them.
const REGENERABLE: &[CleanupCategory] = &[
    CleanupCategory::AppCaches,
    CleanupCategory::Logs,
    CleanupCategory::BrowserCaches,
    CleanupCategory::DevCaches,
    CleanupCategory::AiCaches,
    CleanupCategory::Homebrew,
    CleanupCategory::Temp,
];

/// Reasons that are regenerable even inside a review category.
fn regenerable_reason(reason: CleanupReason) -> bool {
    match reason {
        // Orphans are leftovers whose app is gone; nothing rebuilds them, and
        // nothing needs them either.
        CleanupReason::CacheDir
        | CleanupReason::LogFile
        | CleanupReason::BrowserCache
        | CleanupReason::ToolCache
        | CleanupReason::ToolDownloadCache
        | CleanupReason::DeviceSupport
        | CleanupReason::DeviceSupportLatest
        | CleanupReason::ToolCommand
        | CleanupReason::BrewCleanup
        | CleanupReason::TempFile
        | CleanupReason::Orphan
        | CleanupReason::BuildLeaf
        | CleanupReason::AiCache => true,
        CleanupReason::OrphanService
        | CleanupReason::Installer
        | CleanupReason::InstallerInstalled
        | CleanupReason::BuildArtifact
        | CleanupReason::Trash
        | CleanupReason::DeviceBackup
        | CleanupReason::XcodeArchive
        | CleanupReason::DockerImages
        | CleanupReason::DockerBuildCache
        | CleanupReason::DockerContainers
        | CleanupReason::LargeFile
        | CleanupReason::AiHistory => false,
    }
}

/// Whether one scanned item may be removed by a Local MCP cleanup.
///
/// The delete mode is read from the item, which the scanner filled from
/// `settings.cleanup_delete_mode`; this module never chooses a mode itself.
pub fn removable(category: &CategoryReport, item: &CleanupItem) -> bool {
    // Commands are not paths: they run a tool (`brew cleanup`, docker prune) and
    // the confirmation would show a command line where the user expects a file
    // list. `candidates` applies this too, but the rule has to hold on its own:
    // the write path re-checks a selection against the report and must refuse a
    // command even if some other caller ever offers one.
    if !kind_allowed(item.kind) {
        return false;
    }
    // A size the scan could not finish is a lower bound, and the app already
    // refuses to act on one.
    if !item.size_complete {
        return false;
    }
    // Only what the tool rebuilds for free. `Costly` items (a full Rust rebuild, a
    // re-downloaded toolchain) are choices for a person, not for a tool call.
    if item.restore != CleanupRestore::Auto {
        return false;
    }
    // "Clean" removes for good; only caches are safe to remove for good. The
    // user's setting is respected, not overridden: a Trash setting still shows
    // as Trash, but a permanent one is not extended to data that is not a cache.
    if item.delete_mode == crate::model::DeleteMode::Delete && !regenerable_reason(item.reason) {
        return false;
    }
    // Review and personal categories are never caches, whatever the reason says.
    if matches!(category.risk, CleanupRisk::Review | CleanupRisk::Personal) {
        return regenerable_reason(item.reason) && REGENERABLE.contains(&category.key);
    }
    REGENERABLE.contains(&category.key) && regenerable_reason(item.reason)
}

/// Every item a tool call may select, with the category it came from.
pub fn candidates(report: &CleanupReport) -> Vec<(&CategoryReport, &CleanupItem)> {
    report
        .categories
        .iter()
        .flat_map(|category| {
            category
                .groups
                .iter()
                .flat_map(move |group| group.items.iter().map(move |item| (category, item)))
        })
        .filter(|(category, item)| removable(category, item))
        .collect()
}

/// Reject a selection that names anything a tool may not remove.
///
/// Selection is by item id only; a caller cannot ask for a path, a category, or
/// "everything". Every requested id must be present in the report and pass
/// [`removable`].
pub fn check_selection<'a>(
    report: &'a CleanupReport,
    ids: &[String],
) -> Result<Vec<&'a CleanupItem>, SelectionError> {
    let candidates = candidates(report);
    let mut chosen = Vec::with_capacity(ids.len());
    for id in ids {
        let Some((_, item)) = candidates.iter().find(|(_, item)| &item.id == id) else {
            return Err(SelectionError::NotRemovable(id.clone()));
        };
        if chosen.iter().any(|chosen: &&CleanupItem| chosen.id == *id) {
            return Err(SelectionError::Duplicate(id.clone()));
        }
        chosen.push(*item);
    }
    Ok(chosen)
}

/// Why a selection was refused. Kept as data so the tool can name the item.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SelectionError {
    /// Not in the report, not regenerable, or measured incompletely.
    NotRemovable(String),
    Duplicate(String),
    Empty,
}

/// Commands are never offered: they run a tool (`brew cleanup`, docker prune)
/// rather than removing a path, and the confirmation would show a command line
/// where the user expects a file list.
pub fn kind_allowed(kind: CleanupItemKind) -> bool {
    matches!(kind, CleanupItemKind::File | CleanupItemKind::Dir)
}

#[cfg(test)]
mod tests {
    use super::super::super::model::DeleteMode;
    use super::*;
    use crate::cleanup::types::{CleanupGroup, CleanupVolume};

    fn item(id: &str, reason: CleanupReason, complete: bool, mode: DeleteMode) -> CleanupItem {
        CleanupItem {
            id: id.into(),
            path: format!("/Users/x/Library/Caches/{id}"),
            kind: CleanupItemKind::Dir,
            command: None,
            size_bytes: Some(1024),
            size_complete: complete,
            kept_open_files: 0,
            modified_at: None,
            reason,
            restore: CleanupRestore::for_reason(reason),
            selected: true,
            delete_mode: mode,
            needs_admin: false,
            note: None,
        }
    }

    fn category(key: CleanupCategory, items: Vec<CleanupItem>) -> CategoryReport {
        CategoryReport {
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
            bytes: 1024,
            complete: true,
            groups: vec![CleanupGroup {
                id: "g".into(),
                title: "g".into(),
                subtitle: None,
                app_id: None,
                bundle_id: None,
                items,
            }],
            skipped: vec![],
        }
    }

    fn report(categories: Vec<CategoryReport>) -> CleanupReport {
        CleanupReport {
            report_id: "r".into(),
            scanned_at: 0,
            expires_at: i64::MAX,
            volume: CleanupVolume {
                name: "Macintosh HD".into(),
                total_bytes: 1,
                free_bytes: 0,
            },
            categories,
            incomplete: vec![],
        }
    }

    fn cache_item(id: &str) -> CleanupItem {
        item(id, CleanupReason::CacheDir, true, DeleteMode::Trash)
    }

    #[test]
    fn regenerable_categories_are_removable() {
        for key in [
            CleanupCategory::AppCaches,
            CleanupCategory::Logs,
            CleanupCategory::BrowserCaches,
            CleanupCategory::DevCaches,
            CleanupCategory::AiCaches,
            CleanupCategory::Homebrew,
            CleanupCategory::Temp,
        ] {
            let category = category(key, vec![cache_item("a")]);
            assert!(
                removable(&category, &category.groups[0].items[0]),
                "{key:?} should be removable"
            );
        }
    }

    #[test]
    fn a_costly_item_is_refused_even_in_a_regenerable_category() {
        // A full rebuild is a person's decision; the allowlist must not turn an
        // AI cache rule into permission to delete one.
        let mut costly = item("a", CleanupReason::AiCache, true, DeleteMode::Trash);
        costly.restore = CleanupRestore::Costly;
        let costly_category = category(CleanupCategory::AiCaches, vec![costly]);
        assert!(!removable(
            &costly_category,
            &costly_category.groups[0].items[0]
        ));

        let free_category = category(
            CleanupCategory::AiCaches,
            vec![item("b", CleanupReason::AiCache, true, DeleteMode::Trash)],
        );
        assert!(removable(&free_category, &free_category.groups[0].items[0]));
    }

    #[test]
    fn irreversible_categories_are_never_removable() {
        // Each of these is either the user's data or the last copy of something.
        for (key, reason) in [
            (CleanupCategory::Trash, CleanupReason::Trash),
            (CleanupCategory::DeviceBackups, CleanupReason::DeviceBackup),
            (CleanupCategory::Installers, CleanupReason::Installer),
            (
                CleanupCategory::ProjectArtifacts,
                CleanupReason::BuildArtifact,
            ),
            (CleanupCategory::Docker, CleanupReason::DockerImages),
            (CleanupCategory::LargeFiles, CleanupReason::LargeFile),
            (CleanupCategory::Leftovers, CleanupReason::XcodeArchive),
        ] {
            let category = category(key, vec![item("a", reason, true, DeleteMode::Trash)]);
            assert!(
                !removable(&category, &category.groups[0].items[0]),
                "{key:?}/{reason:?} must never be removable"
            );
        }
    }

    #[test]
    fn a_cache_reason_inside_a_review_category_is_still_refused() {
        // Belt and braces: if the scanner ever put a cache-reasoned item in
        // Trash, the category alone must still stop the tool.
        let category = category(CleanupCategory::Trash, vec![cache_item("a")]);
        assert!(!removable(&category, &category.groups[0].items[0]));
    }

    #[test]
    fn an_incompletely_measured_item_is_refused() {
        let category = category(
            CleanupCategory::AppCaches,
            vec![item("a", CleanupReason::CacheDir, false, DeleteMode::Trash)],
        );
        assert!(!removable(&category, &category.groups[0].items[0]));
    }

    #[test]
    fn a_permanent_delete_mode_does_not_extend_to_non_caches() {
        // With "delete" selected in settings every item is Delete; the tool must
        // not thereby gain the right to erase a non-cache for good.
        let folder = category(
            CleanupCategory::Temp,
            vec![item("a", CleanupReason::TempFile, true, DeleteMode::Delete)],
        );
        assert!(
            removable(&folder, &folder.groups[0].items[0]),
            "a temp file is regenerable and may be deleted with the user's mode"
        );
        let orphan_service = category(
            CleanupCategory::Leftovers,
            vec![item(
                "b",
                CleanupReason::OrphanService,
                true,
                DeleteMode::Delete,
            )],
        );
        assert!(
            !removable(&orphan_service, &orphan_service.groups[0].items[0]),
            "a launchd service is not a cache"
        );
    }

    #[test]
    fn selection_refuses_unknown_irreversible_and_duplicate_ids() {
        let report = report(vec![
            category(CleanupCategory::AppCaches, vec![cache_item("ok")]),
            category(
                CleanupCategory::Trash,
                vec![item("trash", CleanupReason::Trash, true, DeleteMode::Trash)],
            ),
        ]);
        assert_eq!(
            check_selection(&report, &["ok".into()]).map(|items| items.len()),
            Ok(1)
        );
        assert_eq!(
            check_selection(&report, &["trash".into()]).unwrap_err(),
            SelectionError::NotRemovable("trash".into())
        );
        assert_eq!(
            check_selection(&report, &["missing".into()]).unwrap_err(),
            SelectionError::NotRemovable("missing".into())
        );
        assert_eq!(
            check_selection(&report, &["ok".into(), "ok".into()]).unwrap_err(),
            SelectionError::Duplicate("ok".into())
        );
    }

    #[test]
    fn commands_are_not_offered() {
        assert!(!kind_allowed(CleanupItemKind::Command));
        assert!(kind_allowed(CleanupItemKind::File));
        assert!(kind_allowed(CleanupItemKind::Dir));
    }

    #[test]
    fn a_command_is_refused_even_where_every_other_rule_would_allow_it() {
        // Homebrew's own cache is a regenerable category, and `BrewCleanup` is a
        // regenerable reason, so both of the other rules say yes. A command is
        // still not something a tool may select: it runs `brew cleanup`, which
        // reaches packages the dialog never names. Only the kind rule stops it,
        // which is why the rule has to live in `removable` and not in the
        // scan-tool filter alone.
        let command = || {
            let mut one = item("brew", CleanupReason::BrewCleanup, true, DeleteMode::Trash);
            one.kind = CleanupItemKind::Command;
            one.command = Some("brew cleanup --prune=all".into());
            one
        };
        let homebrew = category(CleanupCategory::Homebrew, vec![command()]);
        assert_eq!(homebrew.groups[0].items[0].kind, CleanupItemKind::Command);
        assert!(
            !removable(&homebrew, &homebrew.groups[0].items[0]),
            "a command is never removable, whatever its reason and category"
        );
        let report = report(vec![category(CleanupCategory::Homebrew, vec![command()])]);
        assert_eq!(
            check_selection(&report, &["brew".into()]).unwrap_err(),
            SelectionError::NotRemovable("brew".into())
        );
    }

    #[test]
    fn candidates_read_the_category_an_item_was_scanned_under() {
        // Two items with a regenerable reason, one under a cache category and one
        // under Trash. The reason alone would pass both, so the answer can only
        // come from the category the item was scanned under.
        let report = report(vec![
            category(CleanupCategory::AppCaches, vec![cache_item("cache")]),
            category(CleanupCategory::Trash, vec![cache_item("misfiled")]),
        ]);
        let found = candidates(&report);
        assert_eq!(found.len(), 1);
        assert_eq!(found[0].1.id, "cache");
        assert_eq!(found[0].0.key, CleanupCategory::AppCaches);
    }

    #[test]
    fn a_report_with_no_categories_does_not_panic() {
        // Before, the category lookup fell back to `categories[0]`, which panics
        // on a scan that found nothing.
        let nothing = report(vec![]);
        assert!(candidates(&nothing).is_empty());
        assert_eq!(
            check_selection(&nothing, &["a".into()]).unwrap_err(),
            SelectionError::NotRemovable("a".into())
        );
    }
}
