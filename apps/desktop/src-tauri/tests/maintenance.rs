use opennavo_desktop_lib::{
    brewfile, core::DesktopCore, events::CoreEvent, maintenance, model::*, system,
};
use std::{path::Path, sync::Arc};
fn core(root: &Path) -> Arc<DesktopCore> {
    let core = DesktopCore::open(
        root.join("data"),
        root.join("cache"),
        root.join("askpass"),
        Arc::new(|_: CoreEvent| {}),
    )
    .unwrap();
    // This suite exercises Brewfile / maintenance contracts, not native installed-app rescanning.
    // Match the scheduler fixture: keep completion side effects inside the synthetic test scope.
    core.queue
        .set_completion(Arc::new(|_| Box::pin(async {})))
        .unwrap();
    core.settings.write().unwrap().brew_path = Some(
        Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("tests/fake-brew/brew")
            .to_string_lossy()
            .into(),
    );
    let fixtures =
        Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../apps/server/testdata/desktop");
    let metadata =
        serde_json::from_slice(&std::fs::read(fixtures.join("latest.json")).unwrap()).unwrap();
    opennavo_desktop_lib::catalog::import_file(
        &core.database,
        &fixtures.join("catalog.json.gz"),
        &metadata,
        1,
    )
    .unwrap();
    core
}
#[tokio::test]
async fn simulated_export_preview_and_bundle_queue_round_trip() {
    let tmp = tempfile::tempdir().unwrap();
    let core = core(tmp.path());
    let file = tmp.path().join("Brewfile with spaces");
    assert_eq!(
        brewfile::export(core.clone(), file.to_string_lossy().into())
            .await
            .unwrap(),
        2
    );
    assert!(
        std::fs::read_to_string(&file)
            .unwrap()
            .lines()
            .all(|line| line.starts_with("cask "))
    );
    let preview = brewfile::preview(core.clone(), file.to_string_lossy().into())
        .await
        .unwrap();
    assert_eq!(preview.ready, 2);
    assert_eq!(
        preview
            .entries
            .iter()
            .filter(|e| e.status == BrewfileEntryStatus::Skipped)
            .count(),
        0
    );
    let targets = preview
        .entries
        .iter()
        .filter(|e| e.status == BrewfileEntryStatus::Ready)
        .map(|e| TaskTarget {
            kind: e.kind.unwrap().into(),
            token: e.token.clone().unwrap(),
        })
        .collect();
    core.queue.start();
    let tasks = core
        .queue
        .enqueue_many(
            TaskOp::Install,
            targets,
            TaskOptions::default(),
            TaskTrigger::Bundle,
        )
        .unwrap();
    for task in &tasks {
        assert_eq!(task.trigger, TaskTrigger::Bundle);
        assert_eq!(
            tokio::time::timeout(
                std::time::Duration::from_secs(10),
                core.queue.wait_terminal(&task.id)
            )
            .await
            .unwrap()
            .unwrap()
            .state,
            TaskState::Succeeded
        );
    }
    core.queue.shutdown();
}
#[test]
fn preview_all_states_comments_quotes_and_no_ruby_execution() {
    let tmp = tempfile::tempdir().unwrap();
    let core = core(tmp.path());
    let info = serde_json::from_str(r#"{"formulae":[{"name":"ripgrep","installed":[{"version":"14","installed_on_request":true}]}],"casks":[]}"#).unwrap();
    let installed = opennavo_desktop_lib::library::scan(
        &core.database,
        &info,
        tmp.path().to_str().unwrap(),
        Locale::ZhCn,
    )
    .unwrap()
    .items;
    let content = "# header\nbrew \"ripgrep\" # installed\ncask 'ghostty'\ntap \"homebrew/core\"\ntap 'user/tap'\nmas \"App\", id: 1\nvscode \"x\"\nwhalebrew \"x\"\ncask \"unknown\"\nbrew \"--all\"\nbrew 'node\"\ncask \"foo#bar\"\nsystem(\"touch unsafe\")\ncask \"ghostty\"\n";
    let preview = brewfile::parse(content, &installed, |k, t| {
        opennavo_desktop_lib::catalog::get(&core.database, k, t)
    })
    .unwrap();
    let statuses: Vec<_> = preview.entries.iter().map(|e| e.status).collect();
    assert_eq!(
        statuses,
        [
            BrewfileEntryStatus::Unsupported,
            BrewfileEntryStatus::Ready,
            BrewfileEntryStatus::Unsupported,
            BrewfileEntryStatus::Unsupported,
            BrewfileEntryStatus::Unsupported,
            BrewfileEntryStatus::Unsupported,
            BrewfileEntryStatus::Unsupported,
            BrewfileEntryStatus::NotFound,
            BrewfileEntryStatus::Unsupported,
            BrewfileEntryStatus::Unsupported,
            BrewfileEntryStatus::Invalid,
            BrewfileEntryStatus::Unsupported,
            BrewfileEntryStatus::Skipped
        ]
    );
    assert_eq!(preview.entries[0].line, 2);
    assert_eq!(preview.entries[0].raw, "brew \"ripgrep\"");
    assert!(preview.entries[8].token.is_none());
    assert_eq!(
        (preview.ready, preview.installed, preview.unsupported),
        (1, 0, 9)
    );
    // Retain coverage for installed Casks; legacy Formula rows must be Unsupported even when installed.
    let info = serde_json::from_str(r#"{"formulae":[],"casks":[{"token":"ghostty","installed":"1","version":"1","artifacts":[]}]}"#).unwrap();
    let installed = opennavo_desktop_lib::library::scan(
        &core.database,
        &info,
        tmp.path().to_str().unwrap(),
        Locale::ZhCn,
    )
    .unwrap()
    .items;
    let preview = brewfile::parse("cask \"ghostty\"", &installed, |_, _| {
        panic!("已安装项无需查目录")
    })
    .unwrap();
    assert_eq!(preview.entries[0].status, BrewfileEntryStatus::Installed);
    assert_eq!(preview.installed, 1);
    assert!(brewfile::parse(&"x".repeat(1024 * 1024 + 1), &[], |_, _| Ok(None)).is_err());
}
#[tokio::test]
async fn maintenance_uses_fake_fixed_arrays_and_warning_exit_is_report() {
    let tmp = tempfile::tempdir().unwrap();
    let core = core(tmp.path());
    let estimate = maintenance::estimate(core.clone()).await.unwrap();
    assert_eq!((estimate.bytes, estimate.file_count), (61 * 1024 * 1024, 4));
    assert_eq!(estimate.items[1].bytes, Some(512 * 1024));
    assert!(estimate.items[2].bytes.is_none());
    let report = maintenance::diagnose(core.clone()).await.unwrap();
    assert!(!report.ok);
    assert_eq!(report.warnings.len(), 2);
    assert!(report.warnings[0].body.contains("building Homebrew"));
    assert!(report.ran_at > 0);
    assert!(maintenance::doctor("Your system is ready to brew.", 1).ok);
    let bounded = maintenance::cleanup(&"Would remove: /tmp/archive (1KB)\n".repeat(250));
    assert_eq!(bounded.file_count, 250);
    assert_eq!(bounded.items.len(), 200);
    assert_eq!(bounded.bytes, 250 * 1024);
}
#[test]
fn system_only_uses_installed_app_and_fixed_privacy_urls() {
    let tmp = tempfile::tempdir().unwrap();
    let core = core(tmp.path());
    let app = tmp.path().join("App Name.app");
    std::fs::create_dir(&app).unwrap();
    let info = serde_json::from_value(serde_json::json!({"formulae":[],"casks":[{"token":"ghostty","version":"1","installed":"1","artifacts":[{"app":["App Name.app"],"target":app}]}]})).unwrap();
    let items = opennavo_desktop_lib::library::scan(
        &core.database,
        &info,
        tmp.path().to_str().unwrap(),
        Locale::ZhCn,
    )
    .unwrap()
    .items;
    let target = TaskTarget {
        kind: Kind::Cask.into(),
        token: "ghostty".into(),
    };
    assert_eq!(system::app_path(&target, &items).unwrap(), app);
    assert_eq!(
        system::app_path(
            &TaskTarget {
                kind: Kind::Formula.into(),
                token: "ripgrep".into()
            },
            &items
        )
        .unwrap_err()
        .code,
        "E_INVALID_ARG"
    );
    assert_eq!(
        system::privacy_url(PrivacyPane::Notifications, "com.example.changed").unwrap(),
        "x-apple.systempreferences:com.apple.preference.notifications?id=com.example.changed"
    );
    assert!(system::privacy_url(PrivacyPane::Notifications, "id?inject=true").is_err());
    assert_eq!(
        system::privacy_url(PrivacyPane::AppManagement, "com.example.changed").unwrap(),
        "x-apple.systempreferences:com.apple.preference.security?Privacy_AppBundles"
    );
    std::fs::remove_dir(app).unwrap();
    assert!(system::app_path(&target, &items).is_err());
}
#[tokio::test]
async fn updates_status_persists_success_and_clears_checking() {
    let tmp = tempfile::tempdir().unwrap();
    let core = core(tmp.path());
    assert!(core.updates_status().unwrap().checked_at.is_none());
    core.check_updates(false).await.unwrap();
    let status = core.updates_status().unwrap();
    assert!(!status.checking);
    assert!(status.checked_at.is_some());
    assert!(status.next_check_at.is_some());
    core.settings.write().unwrap().auto_check = false;
    assert!(core.updates_status().unwrap().next_check_at.is_none());
    let reloaded = DesktopCore::open(
        tmp.path().join("data"),
        tmp.path().join("cache"),
        tmp.path().join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    assert_eq!(
        reloaded.updates_status().unwrap().checked_at,
        status.checked_at
    );
}
