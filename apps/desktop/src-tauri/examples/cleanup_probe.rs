//! Opt-in, read-only cleanup diagnostics. All OpenNavo state and icons live in a temporary
//! directory. No task queue is started and no execution API is called. Docker is only asked
//! for `docker system df`.
use opennavo_desktop_lib::{
    AppError,
    cleanup::{Engine as CleanupEngine, rules},
    core::DesktopCore,
    model::*,
    permissions,
    running_apps::NativeApps,
    uninstall::{
        Engine, EngineConfig,
        execute::FileActions,
        identity::{Identity, MetadataProbe, NativeProbe, Receipt},
        rules::Roots,
    },
};
use std::{
    path::{Path, PathBuf},
    sync::Arc,
};

struct ReadOnlyMetadata;
impl MetadataProbe for ReadOnlyMetadata {
    fn identity(&self, path: &Path) -> Identity {
        NativeProbe.identity(path)
    }
    // Receipts and loaded launchd state are unnecessary for this cache comparison.
    // Do not invoke pkgutil or launchctl, even for read-only queries.
    fn receipts(&self, _: &Path) -> Vec<Receipt> {
        vec![]
    }
}
struct NoWrites;
impl FileActions for NoWrites {
    fn trash(&self, _: &Path, _: &Path) -> Result<PathBuf, AppError> {
        Err(AppError::new("E_PROTECTED", "read_only_probe"))
    }
    fn bootout(&self, _: &str, _: bool, _: &str) -> Result<(), AppError> {
        Err(AppError::new("E_PROTECTED", "read_only_probe"))
    }
}

fn opted_in(args: &[String]) -> bool {
    args == ["--real-read-only"]
}
fn selected_bytes(category: &CategoryReport) -> u64 {
    category
        .groups
        .iter()
        .flat_map(|g| &g.items)
        .filter(|i| i.selected)
        .fold(0, |sum, i| sum.saturating_add(i.size_bytes.unwrap_or(0)))
}
fn print_report(report: &CleanupReport, entry: &opennavo_desktop_lib::cleanup::Entry) {
    let mut suggested = 0u64;
    let mut reclaimable = 0u64;
    for key in rules::AVAILABLE {
        let category = report.categories.iter().find(|c| c.key == *key);
        let (bytes, selected) = category
            .map(|c| (c.bytes, selected_bytes(c)))
            .unwrap_or((0, 0));
        suggested = suggested.saturating_add(selected);
        reclaimable = reclaimable.saturating_add(bytes);
        println!(
            "{key:?}: reclaimable_bytes={bytes} default_selected_bytes={selected} complete={}",
            category.is_none_or(|c| c.complete)
        );
        println!(
            "  elapsed_ms={}",
            entry.category_millis.get(key).copied().unwrap_or(0)
        );
        if let Some(category) = category {
            for item in category.groups.iter().flat_map(|g| &g.items) {
                println!(
                    "  item {} reason={:?} selected={} bytes={:?} lower_bound={} kept_open_files={}",
                    item.command.as_deref().unwrap_or(&item.path),
                    item.reason,
                    item.selected,
                    item.size_bytes,
                    entry
                        .items
                        .get(&item.id)
                        .and_then(|i| i.tree.as_ref())
                        .is_some_and(|tree| tree.incomplete.is_some()),
                    item.kept_open_files
                );
            }
            for group in &category.skipped {
                println!(
                    "  skipped {:?}: {} processes={:?} bytes={:?}",
                    group.reason, group.title, group.processes, group.bytes
                );
            }
            for group in &category.groups {
                if group.items.iter().any(|i| {
                    matches!(
                        i.reason,
                        CleanupReason::DeviceSupport | CleanupReason::DeviceSupportLatest
                    )
                }) {
                    println!(
                        "  DeviceSupport {} / {}:",
                        group.subtitle.as_deref().unwrap_or(""),
                        group.title
                    );
                    for item in &group.items {
                        println!(
                            "    {} selected={} reason={:?} bytes={:?}",
                            Path::new(&item.path)
                                .file_name()
                                .unwrap_or_default()
                                .to_string_lossy(),
                            item.selected,
                            item.reason,
                            item.size_bytes
                        );
                    }
                }
            }
        }
    }
    println!(
        "TOTAL reclaimable_bytes={reclaimable} default_selected_bytes={suggested} ({:.2} GB, decimal)",
        suggested as f64 / 1e9
    );
    for location in &report.incomplete {
        println!("incomplete {:?}: {}", location.reason, location.path);
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    if !opted_in(&std::env::args().skip(1).collect::<Vec<_>>()) {
        return Err("require exactly --real-read-only; no cleanup execution is available".into());
    }
    let temporary = tempfile::tempdir()?;
    let core = DesktopCore::open(
        temporary.path().join("data"),
        temporary.path().join("cache"),
        temporary.path().join("unused-askpass"),
        Arc::new(|_| {}),
    )?;
    core.settings
        .write()
        .map_err(|_| "settings")?
        .homebrew_analytics = Some(false);
    let uninstall = Engine::new(EngineConfig {
        database: core.database.clone(),
        settings: core.settings.clone(),
        sink: Arc::new(|_| {}),
        roots: Roots::native(),
        control: Arc::new(NativeApps),
        probe: Arc::new(ReadOnlyMetadata),
        icon_cache: temporary.path().join("icons"),
        askpass: temporary.path().join("unused-askpass"),
    });
    uninstall.set_file_actions(Arc::new(NoWrites))?;
    let cleanup = CleanupEngine::new(uninstall);
    eprintln!(
        "Read-only scan. Homebrew estimate uses the existing --dry-run read operation; no cleanup task runs. Receipt / loaded launchd queries are omitted; OpenNavo state is temporary."
    );
    let started = std::time::Instant::now();
    let report = cleanup
        .scan_for_command(
            uuid::Uuid::now_v7().to_string(),
            None,
            permissions::combine(permissions::signals(PermissionState::Unknown, false)),
            Some(core),
        )
        .await?;
    println!("SCAN elapsed_ms={}", started.elapsed().as_millis());
    print_report(&report, &cleanup.cached(&report.report_id, 0)?);
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn requires_explicit_read_only_flag_and_sums_only_default_selection() {
        assert!(!opted_in(&[]));
        assert!(!opted_in(&["--execute".into()]));
        assert!(!opted_in(&["--real-read-only".into(), "--execute".into()]));
        assert!(opted_in(&["--real-read-only".into()]));
        let mut category = CategoryReport {
            key: CleanupCategory::DevCaches,
            risk: CleanupRisk::Rebuildable,
            bytes: 0,
            complete: true,
            groups: vec![],
            skipped: vec![],
        };
        let mut item = CleanupItem {
            id: "old".into(),
            path: "/synthetic/old".into(),
            kind: CleanupItemKind::Dir,
            command: None,
            size_bytes: Some(10),
            kept_open_files: 0,
            modified_at: None,
            reason: CleanupReason::DeviceSupport,
            selected: true,
            delete_mode: DeleteMode::Delete,
            needs_admin: false,
            note: None,
        };
        let old = item.clone();
        item.selected = false;
        item.reason = CleanupReason::DeviceSupportLatest;
        item.size_bytes = Some(20);
        category.groups.push(CleanupGroup {
            id: "synthetic".into(),
            title: "iPhone16,2".into(),
            subtitle: Some("iOS".into()),
            app_id: None,
            bundle_id: None,
            items: vec![old, item],
        });
        assert_eq!(selected_bytes(&category), 10);
    }
}
