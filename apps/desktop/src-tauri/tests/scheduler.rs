use chrono::{Local, TimeZone, Timelike, Utc};
use opennavo_desktop_lib::{
    core::DesktopCore,
    model::*,
    notifications::{self, Workers},
    scheduler::{self, Notice},
    tray,
};
use std::{
    os::unix::fs::PermissionsExt,
    path::Path,
    sync::{Arc, Mutex, atomic::Ordering},
    time::Duration,
};

fn item(kind: Kind, token: &str, pinned: bool, ignored: bool) -> OutdatedItem {
    OutdatedItem {
        source: UpdateSource::Homebrew,
        app_id: None,
        bundle_id: None,
        channel: None,
        major: false,
        notes: None,
        integrity: None,
        requires_admin: false,
        running: false,
        relaunch_pending: false,
        skipped_version: None,
        critical_update: false,
        informational_url: None,
        download_host: None,
        expected_team_match: None,
        kind,
        token: token.into(),
        name: token.into(),
        installed_version: "1".into(),
        current_version: "2".into(),
        auto_updates: false,
        pinned,
        ignored,
        dependents: 0,
        download_size: None,
    }
}

#[test]
fn daily_clock_two_minutes_sleep_catch_up_and_disabled() {
    let now = Utc.with_ymd_and_hms(2026, 10, 5, 12, 0, 0).unwrap();
    let mut settings = Settings {
        check_time: "12:02".into(),
        ..Settings::default()
    };
    let due = now + chrono::Duration::minutes(2);
    assert_eq!(
        scheduler::next_check_in(&settings, None, now),
        Some(due.timestamp_millis())
    );
    assert_eq!(
        scheduler::next_check_in(&settings, None, due),
        Some(due.timestamp_millis())
    );
    let missed = now + chrono::Duration::hours(3);
    assert!(
        scheduler::next_check_in(&settings, None, missed).unwrap() <= missed.timestamp_millis()
    );
    let yesterday = now - chrono::Duration::hours(25);
    assert_eq!(
        scheduler::next_check_in(&settings, Some(yesterday.timestamp_millis()), now),
        Some(now.timestamp_millis())
    );
    let next_day = due + chrono::Duration::days(1);
    assert_eq!(
        scheduler::next_check_in(&settings, Some(due.timestamp_millis()), due),
        Some(next_day.timestamp_millis())
    );
    settings.auto_check = false;
    assert_eq!(scheduler::next_check_in(&settings, None, due), None);
}

#[test]
fn auto_updates_ignore_legacy_formula_flag_and_respect_cask_pins_and_ignores() {
    let items = [
        item(Kind::Cask, "ghostty", false, false),
        item(Kind::Formula, "node", false, false),
        item(Kind::Formula, "pcre2", true, false),
        item(Kind::Cask, "visual-studio-code", false, true),
    ];
    let mut settings = Settings::default();
    assert!(scheduler::eligible(&items, &settings).is_empty());
    settings.auto_upgrade_formulae = true;
    assert_eq!(
        scheduler::eligible(&items, &settings)
            .iter()
            .map(|t| t.token.as_str())
            .collect::<Vec<_>>(),
        [] as [&str; 0]
    );
    settings.auto_upgrade_casks = true;
    assert_eq!(scheduler::eligible(&items, &settings).len(), 1);
    settings.auto_upgrade_formulae = false;
    assert_eq!(
        scheduler::eligible(&items, &settings)
            .iter()
            .map(|t| t.token.as_str())
            .collect::<Vec<_>>(),
        ["ghostty"]
    );
}

fn core(root: &Path) -> Arc<DesktopCore> {
    let fake = Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/fake-brew/brew");
    let path = root.join("brew_fixture");
    let info = serde_json::json!({"formulae":[{"name":"node","installed":[{"version":"1"}]},{"name":"pcre2","installed":[{"version":"1"}],"pinned":true}],"casks":[{"token":"ghostty","tap":"homebrew/cask","name":["Ghostty"],"installed":"1","version":"2","artifacts":[]},{"token":"visual-studio-code","tap":"homebrew/cask","name":["VS Code"],"installed":"1","version":"2","artifacts":[]}]});
    let outdated = serde_json::json!({"formulae":[{"name":"node","installed_versions":["1"],"current_version":"2"},{"name":"pcre2","installed_versions":["1"],"current_version":"2","pinned":true}],"casks":[{"name":"ghostty","installed_versions":["1"],"current_version":"2"},{"name":"visual-studio-code","installed_versions":["1"],"current_version":"2"}]});
    let python_string = |value: &str| serde_json::to_string(value).unwrap();
    // Local wrapper writes forward only to the public-log simulator, never the development machine's Homebrew.
    std::fs::write(&path, format!("#!/usr/bin/python3 -I\nimport json, os, sys\nargs=sys.argv[1:]\nif args==['--prefix']:\n print({prefix})\n sys.exit(0)\nif args and args[0]=='info':\n print({info})\n sys.exit(0)\nif args and args[0]=='outdated':\n print({outdated})\n sys.exit(0)\nos.execv('/usr/bin/python3', ['/usr/bin/python3', '-I', {fake}] + args)\n", prefix=python_string(root.to_str().unwrap()),info=python_string(&info.to_string()),outdated=python_string(&outdated.to_string()),fake=python_string(fake.to_str().unwrap()))).unwrap();
    std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
    let core = DesktopCore::open(
        root.join("data"),
        root.join("cache"),
        root.join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    core.settings.write().unwrap().brew_path = Some(path.to_string_lossy().into());
    // Scheduler tests exercise Homebrew fixtures only; newly enabled native
    // engines must never inspect real apps or request public feeds here.
    for source in UpdateSource::ALL {
        core.settings
            .write()
            .unwrap()
            .update_sources
            .0
            .insert(source, source == UpdateSource::Homebrew);
    }
    core
}

#[tokio::test]
async fn check_schedule_serial_update_auto_upgrade_and_single_summary() {
    let tmp = tempfile::tempdir().unwrap();
    let core = core(tmp.path());
    {
        let mut settings = core.settings.write().unwrap();
        settings.update_sources.0.insert(UpdateSource::Mas, false);
        settings
            .update_sources
            .0
            .insert(UpdateSource::Sparkle, false);
    }
    let notices = Arc::new(Mutex::new(Vec::new()));
    let received = notices.clone();
    core.install_notice_sink(Arc::new(move |n| received.lock().unwrap().push(n)))
        .unwrap();
    let now = Local::now();
    let due = now
        .with_hour(12)
        .unwrap()
        .with_minute(2)
        .unwrap()
        .with_second(0)
        .unwrap()
        .with_nanosecond(0)
        .unwrap();
    {
        let mut settings = core.settings.write().unwrap();
        settings.check_time = "12:02".into();
        settings.auto_upgrade_formulae = true;
        settings.auto_upgrade_casks = true;
        settings.run_brew_update_on_check = true;
    }
    core.refresh_library(LibraryChangeReason::Refresh, None)
        .await
        .unwrap();
    core.ignore_update(
        TaskTarget {
            kind: Kind::Cask.into(),
            token: "visual-studio-code".into(),
        },
        None,
        None,
    )
    .await
    .unwrap();
    // Isolate wall-clock time for the scan completion hook; scheduling and queue still use real implementations.
    core.queue
        .set_completion(Arc::new(|_| Box::pin(async {})))
        .unwrap();
    core.queue.start();
    assert!(
        !scheduler::tick(core.clone(), due.timestamp_millis() - 1)
            .await
            .unwrap()
    );
    assert!(
        scheduler::tick(core.clone(), due.timestamp_millis())
            .await
            .unwrap()
    );
    tokio::time::timeout(Duration::from_secs(10), async {
        loop {
            if notices
                .lock()
                .unwrap()
                .iter()
                .any(|n| matches!(n, Notice::Completed { .. }))
            {
                break;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .unwrap();
    let tasks = core.database.tasks().unwrap();
    assert_eq!(tasks.len(), 2);
    assert!(
        tasks
            .iter()
            .all(|t| t.trigger == TaskTrigger::Schedule && t.state == TaskState::Succeeded)
    );
    assert_eq!(tasks.iter().filter(|t| t.op == TaskOp::Update).count(), 1);
    assert_eq!(
        tasks
            .iter()
            .filter_map(|t| t.target.as_ref().map(|t| t.token.as_str()))
            .collect::<std::collections::HashSet<_>>(),
        ["ghostty"].into_iter().collect()
    );
    let received = notices.lock().unwrap().clone();
    assert_eq!(received.len(), 2);
    assert!(matches!(&received[0], Notice::Available { names } if names.len()==3));
    assert_eq!(
        received[1],
        Notice::Completed {
            succeeded: 1,
            failed: 0,
            canceled: 0
        }
    );
    assert!(
        !scheduler::tick(core.clone(), due.timestamp_millis() + 60_000)
            .await
            .unwrap()
    );
    assert_eq!(
        core.updates_status_at(due.timestamp_millis())
            .unwrap()
            .checked_at,
        Some(due.timestamp_millis())
    );
    core.queue.shutdown();
    let restarted = DesktopCore::open(
        tmp.path().join("data"),
        tmp.path().join("cache"),
        tmp.path().join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    assert_eq!(
        restarted.updates_status().unwrap().checked_at,
        Some(due.timestamp_millis())
    );
}

#[tokio::test]
async fn changed_time_replans_and_read_only_checks_do_not_repeat_notices() {
    let tmp = tempfile::tempdir().unwrap();
    let core = core(tmp.path());
    {
        let mut settings = core.settings.write().unwrap();
        settings.update_sources.0.insert(UpdateSource::Mas, false);
        settings
            .update_sources
            .0
            .insert(UpdateSource::Sparkle, false);
    }
    core.check_updates(false).await.unwrap();
    let notices = Arc::new(Mutex::new(Vec::new()));
    let received = notices.clone();
    core.install_notice_sink(Arc::new(move |n| received.lock().unwrap().push(n)))
        .unwrap();
    let now = Local::now();
    let due = (now + chrono::Duration::minutes(2))
        .with_second(0)
        .unwrap()
        .with_nanosecond(0)
        .unwrap();
    let mut settings = core.current_settings().unwrap();
    settings.check_time = due.format("%H:%M").to_string();
    settings.run_brew_update_on_check = false;
    core.replace_settings(settings).await.unwrap();
    assert_eq!(
        core.updates_status_at(now.timestamp_millis())
            .unwrap()
            .next_check_at,
        Some(due.timestamp_millis())
    );
    assert!(
        !scheduler::tick(core.clone(), due.timestamp_millis() - 1)
            .await
            .unwrap()
    );
    assert!(
        scheduler::tick(core.clone(), due.timestamp_millis())
            .await
            .unwrap()
    );
    assert!(core.database.tasks().unwrap().is_empty());
    assert!(
        notices
            .lock()
            .unwrap()
            .iter()
            .all(|n| *n == Notice::Settings)
    );
    let restarted = DesktopCore::open(
        tmp.path().join("data"),
        tmp.path().join("cache"),
        tmp.path().join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    assert!(
        restarted
            .updates_status_at(due.timestamp_millis())
            .unwrap()
            .next_check_at
            .unwrap()
            > due.timestamp_millis()
    );
    core.queue.shutdown();
    assert!(
        !scheduler::tick(core.clone(), due.timestamp_millis() + 86_400_000)
            .await
            .unwrap()
    );
}

#[tokio::test]
async fn notification_waits_are_independent_bounded_and_cleaned_on_exit() {
    let workers = Workers::default();
    for _ in 0..8 {
        workers
            .spawn(|stopped| {
                while !stopped.load(Ordering::Acquire) {
                    std::thread::sleep(Duration::from_millis(5));
                }
            })
            .unwrap();
    }
    assert_eq!(workers.pending(), 8);
    assert!(workers.spawn(|_| {}).is_err());
    workers.stop();
    workers.drain(Duration::from_secs(1)).await;
    assert_eq!(workers.pending(), 0);
    workers.spawn(|_| panic!("退出后不得创建等待线程")).unwrap();
    assert_eq!(workers.pending(), 0);
}

#[test]
fn tray_and_notifications_use_template_icons_and_bilingual_resources() {
    let notice = Notice::Available {
        names: vec!["Ghostty".into(), "VS Code".into(), "node".into()],
    };
    assert_eq!(
        notifications::content(&notice, Locale::ZhCn)
            .unwrap()
            .unwrap(),
        ("3 个更新可用".into(), "Ghostty、VS Code".into())
    );
    assert_eq!(
        notifications::content(&notice, Locale::EnUs)
            .unwrap()
            .unwrap()
            .1,
        "Ghostty, VS Code"
    );
    assert!(
        notifications::content(&Notice::Settings, Locale::ZhCn)
            .unwrap()
            .is_none()
    );
    assert_eq!(tray::title(3, true), Some("3".into()));
    assert_eq!(tray::title(0, true), None);
    assert_eq!(tray::title(3, false), None);
    assert_eq!(
        (tray::WIDTH, tray::HEIGHT, tray::ROUTE),
        (330., 200., "index.html#/tray")
    );
    for (bytes, size) in [
        (include_bytes!("../icons/trayTemplate.png").as_slice(), 22),
        (
            include_bytes!("../icons/trayTemplate@2x.png").as_slice(),
            44,
        ),
    ] {
        let image = image::load_from_memory(bytes).unwrap().to_rgba8();
        assert_eq!(image.dimensions(), (size, size));
        assert!(image.pixels().any(|p| p.0[3] == 0));
        assert!(image.pixels().any(|p| p.0[3] > 0));
        assert!(image.pixels().all(|p| p.0[..3] == [0, 0, 0]));
    }
    let capability: serde_json::Value =
        serde_json::from_str(include_str!("../capabilities/tray.json")).unwrap();
    assert_eq!(capability["windows"], serde_json::json!(["tray"]));
    let allowed = capability["permissions"].as_array().unwrap();
    assert!(allowed.contains(&serde_json::json!("native-tray")));
    assert!(
        !allowed
            .iter()
            .any(|p| p.as_str().unwrap().starts_with("opener:"))
    );
}

#[test]
fn compiled_capabilities_isolate_native_commands_between_windows() {
    let mut context: tauri::Context<tauri::Wry> = tauri::generate_context!();
    let authority = context.runtime_authority_mut();
    use tauri::ipc::Origin;
    for command in [
        "updates_list",
        "updates_status",
        "task_enqueue",
        "library_list",
        "plugin:window|set_size",
    ] {
        assert!(
            authority
                .resolve_access(command, "tray", "tray", &Origin::Local)
                .is_some(),
            "{command}"
        );
    }
    for command in [
        "apps_running",
        "settings_set",
        "brewfile_install",
        "cleanup_summary",
        "system_open_privacy",
        "app_quit",
        "app_cancel_quit",
        "plugin:opener|open_url",
    ] {
        assert!(
            authority
                .resolve_access(command, "main", "main", &Origin::Local)
                .is_some(),
            "{command}"
        );
        assert!(
            authority
                .resolve_access(command, "tray", "tray", &Origin::Local)
                .is_none(),
            "{command}"
        );
    }
    for command in [
        "plugin:opener|open_path",
        "plugin:opener|reveal_item_in_dir",
    ] {
        assert!(
            authority
                .resolve_access(command, "main", "main", &Origin::Local)
                .is_none(),
            "{command}"
        );
    }
    assert!(
        authority
            .resolve_access("task_enqueue", "foreign", "foreign", &Origin::Local)
            .is_none()
    );
}

#[tokio::test]
async fn completed_checks_replace_failed_sources_and_empty_selection_preserves_status() {
    let tmp = tempfile::tempdir().unwrap();
    let core = core(tmp.path());
    assert!(core.updates_status().unwrap().failed_sources.is_empty());
    let original = core.settings.read().unwrap().brew_path.clone();
    {
        let mut settings = core.settings.write().unwrap();
        settings.run_brew_update_on_check = false;
    }
    // Bypass discovery so a missing fixture cannot fall back to the real brew.
    *core.brew.write().unwrap() = Some(BrewInfo {
        path: tmp.path().join("missing-brew").to_string_lossy().into(),
        prefix: tmp.path().to_string_lossy().into(),
        version: "7.0.7".into(),
    });
    let failed = core
        .check_update_sources(
            Some(vec![UpdateSource::Homebrew]),
            TaskTrigger::Manual,
            Some(100),
        )
        .await;
    assert!(failed.is_err());
    let status = core.updates_status_at(1000).unwrap();
    assert_eq!(status.failed_sources, [UpdateSource::Homebrew]);
    assert_eq!(status.checked_at, Some(100));
    assert!(!status.checking);
    core.check_update_sources(Some(vec![]), TaskTrigger::Manual, Some(200))
        .await
        .unwrap();
    assert_eq!(core.updates_status_at(1000).unwrap(), status);
    core.settings.write().unwrap().brew_path = original;
    *core.brew.write().unwrap() = None;
    assert!(
        !core
            .check_update_sources(
                Some(vec![UpdateSource::Homebrew]),
                TaskTrigger::Manual,
                Some(300)
            )
            .await
            .unwrap()
            .is_empty()
    );
    let status = core.updates_status().unwrap();
    assert!(status.failed_sources.is_empty());
    assert!(!status.checking);
    assert_eq!(status.checked_at, Some(300));
    assert_eq!(
        serde_json::to_value(status).unwrap()["failedSources"],
        serde_json::json!([])
    );
    assert!(core.database.tasks().unwrap().is_empty());
}
