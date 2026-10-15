use opennavo_desktop_lib::{
    brew::json::Info,
    db::Database,
    library,
    model::*,
    updates::{self, Outdated},
};
use std::{path::Path, sync::Arc};
fn fixtures(db: &Database, root: &Path) -> (Vec<InstalledItem>, Outdated) {
    let mut info: Info =
        serde_json::from_str(include_str!("fixtures/installed_subset.json")).unwrap();
    for cask in &mut info.casks {
        let app = root.join(format!("{}.app", cask.token));
        std::fs::create_dir_all(app.join("Contents")).unwrap();
        let mut dict = plist::Dictionary::new();
        dict.insert(
            "CFBundleShortVersionString".into(),
            cask.installed
                .as_str()
                .unwrap()
                .split(',')
                .next()
                .unwrap()
                .into(),
        );
        plist::Value::Dictionary(dict)
            .to_file_xml(app.join("Contents/Info.plist"))
            .unwrap();
        cask.artifacts = Some(vec![
            serde_json::json!({"app":["App.app"],"target":app.to_string_lossy()}),
        ]);
    }
    let library = library::scan(db, &info, root.to_str().unwrap(), Locale::ZhCn)
        .unwrap()
        .items;
    let data: Outdated =
        serde_json::from_str(include_str!("fixtures/outdated_subset.json")).unwrap();
    (library, data)
}
#[test]
fn actual_versions_ignore_expiry_catalog_and_sort() {
    let tmp = tempfile::tempdir().unwrap();
    let db = Database::open(&tmp.path().join("db")).unwrap();
    let path = Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../apps/server/testdata/desktop");
    let meta = serde_json::from_slice(&std::fs::read(path.join("latest.json")).unwrap()).unwrap();
    opennavo_desktop_lib::catalog::import_file(&db, &path.join("catalog.json.gz"), &meta, 1)
        .unwrap();
    let (mut library, data) = fixtures(&db, tmp.path());
    let base = updates::build(&db, &data, &mut library, Locale::ZhCn, 1000).unwrap();
    assert_eq!(base.len(), data.casks.len() + data.formulae.len());
    assert!(
        base.windows(2)
            .all(|items| items[0].kind.as_str() <= items[1].kind.as_str())
    );
    let code = base
        .iter()
        .find(|i| i.token == "visual-studio-code")
        .unwrap();
    assert!(code.download_size.is_some());
    assert!(!code.ignored);
    let current = code.current_version.clone();
    let target = TaskTarget {
        kind: Kind::Cask.into(),
        token: code.token.clone(),
    };
    updates::ignore(&db, &target, Some(&current), Some(2000), 1000).unwrap();
    assert!(
        !updates::build(&db, &data, &mut library, Locale::ZhCn, 1500)
            .unwrap()
            .iter()
            .any(|i| i.token == target.token)
    );
    assert!(
        updates::build(&db, &data, &mut library, Locale::ZhCn, 2000)
            .unwrap()
            .iter()
            .any(|i| i.token == target.token)
    );
    updates::ignore(&db, &target, Some("older"), None, 1000).unwrap();
    assert!(
        updates::build(&db, &data, &mut library, Locale::ZhCn, 1500)
            .unwrap()
            .iter()
            .any(|i| i.token == target.token)
    );
    updates::ignore(&db, &target, None, None, 1000).unwrap();
    assert_eq!(
        updates::build(&db, &data, &mut library, Locale::ZhCn, 1500)
            .unwrap()
            .len(),
        base.len() - 1
    );
    updates::unignore(&db, &target).unwrap();
    let item = library.iter().find(|i| i.token == target.token).unwrap();
    let path = Path::new(&item.app_paths[0]).join("Contents/Info.plist");
    let mut dict = plist::Dictionary::new();
    dict.insert("CFBundleShortVersionString".into(), "99999999.0".into());
    plist::Value::Dictionary(dict)
        .to_file_binary(&path)
        .unwrap();
    assert_eq!(
        updates::build(&db, &data, &mut library, Locale::ZhCn, 3000)
            .unwrap()
            .len(),
        base.len() - 1
    );
    assert_eq!(
        library
            .iter()
            .find(|i| i.token == target.token)
            .unwrap()
            .status,
        InstalledStatus::SelfUpdated
    );
    std::fs::remove_file(path).unwrap();
    assert_eq!(
        updates::build(&db, &data, &mut library, Locale::ZhCn, 3000)
            .unwrap()
            .len(),
        base.len()
    );
    assert!(
        updates::ignore(
            &db,
            &TaskTarget {
                kind: Kind::Formula.into(),
                token: "--all".into()
            },
            None,
            None,
            1
        )
        .is_err()
    );
    assert!(updates::ignore(&db, &target, Some("x\ny"), None, 1).is_err());
    assert!(updates::ignore(&db, &target, None, Some(-1), 1).is_err());
}
#[tokio::test]
async fn cached_ignores_and_unignore_emit_updates_and_survive_restart() {
    let tmp = tempfile::tempdir().unwrap();
    let db = Database::open(&tmp.path().join("data/opennavo.db")).unwrap();
    let (library, data) = fixtures(&db, tmp.path());
    db.set_meta("installed_cache", &serde_json::to_string(&library).unwrap())
        .unwrap();
    db.set_meta("outdated_source", &serde_json::to_string(&data).unwrap())
        .unwrap();
    drop(db);
    let events = Arc::new(std::sync::Mutex::new(Vec::new()));
    let saved = events.clone();
    let core = opennavo_desktop_lib::core::DesktopCore::open(
        tmp.path().join("data"),
        tmp.path().join("cache"),
        tmp.path().join("askpass"),
        Arc::new(move |e| saved.lock().unwrap().push(e)),
    )
    .unwrap();
    let total = core.rebuild_updates().await.unwrap().len();
    let target = TaskTarget {
        kind: Kind::Cask.into(),
        token: "visual-studio-code".into(),
    };
    core.ignore_update(target.clone(), None, None)
        .await
        .unwrap();
    assert_eq!(core.updates_list().unwrap().len(), total - 1);
    core.unignore_update(target).await.unwrap();
    assert_eq!(core.updates_list().unwrap().len(), total);
    assert_eq!(
        events
            .lock()
            .unwrap()
            .iter()
            .filter(|e| matches!(e, opennavo_desktop_lib::events::CoreEvent::Updates(_)))
            .count(),
        3
    );
    let restarted = opennavo_desktop_lib::core::DesktopCore::open(
        tmp.path().join("data"),
        tmp.path().join("cache"),
        tmp.path().join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    assert_eq!(restarted.updates_list().unwrap().len(), total);
}
#[tokio::test]
async fn explicit_index_update_uses_only_simulated_write_queue() {
    let tmp = tempfile::tempdir().unwrap();
    let core = opennavo_desktop_lib::core::DesktopCore::open(
        tmp.path().join("data"),
        tmp.path().join("cache"),
        tmp.path().join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    core.settings.write().unwrap().brew_path = Some(
        Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("tests/fake-brew/brew")
            .to_string_lossy()
            .into(),
    );
    core.queue.start();
    let updates =
        tokio::time::timeout(std::time::Duration::from_secs(10), core.check_updates(true))
            .await
            .unwrap()
            .unwrap();
    assert!(updates.is_empty());
    assert!(
        core.database
            .tasks()
            .unwrap()
            .iter()
            .any(|t| t.op == TaskOp::Update && t.state == TaskState::Succeeded)
    );
    core.queue.shutdown();
}

#[tokio::test]
async fn index_check_during_install_does_not_block_completion_or_next_task() {
    let tmp = tempfile::tempdir().unwrap();
    let (running_tx, mut running_rx) = tokio::sync::mpsc::unbounded_channel();
    let core = opennavo_desktop_lib::core::DesktopCore::open(
        tmp.path().join("data"),
        tmp.path().join("cache"),
        tmp.path().join("askpass"),
        Arc::new(move |event| {
            if let opennavo_desktop_lib::events::CoreEvent::Task(task) = event
                && task.op == TaskOp::Install
                && task.state == TaskState::Running
            {
                let _ = running_tx.send(task.id);
            }
        }),
    )
    .unwrap();
    core.settings.write().unwrap().brew_path = Some(
        Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("tests/fake-brew/brew")
            .to_string_lossy()
            .into(),
    );
    let install = core
        .queue
        .enqueue(
            TaskOp::Install,
            Some(TaskTarget {
                kind: Kind::Formula.into(),
                token: "ripgrep".into(),
            }),
            TaskOptions::default(),
            TaskTrigger::Manual,
        )
        .unwrap();
    core.queue.start();
    tokio::time::timeout(std::time::Duration::from_secs(5), running_rx.recv())
        .await
        .unwrap()
        .unwrap();
    let checked =
        tokio::time::timeout(std::time::Duration::from_secs(10), core.check_updates(true)).await;
    assert!(checked.is_ok(), "安装完成回调与检查更新不能互相等待");
    checked.unwrap().unwrap();
    assert_eq!(
        core.queue.wait_terminal(&install.id).await.unwrap().state,
        TaskState::Succeeded
    );
    let next = core
        .queue
        .enqueue(
            TaskOp::Install,
            Some(TaskTarget {
                kind: Kind::Formula.into(),
                token: "gh".into(),
            }),
            TaskOptions::default(),
            TaskTrigger::Manual,
        )
        .unwrap();
    let task = tokio::time::timeout(
        std::time::Duration::from_secs(10),
        core.queue.wait_terminal(&next.id),
    )
    .await
    .unwrap()
    .unwrap();
    assert_eq!(task.state, TaskState::Succeeded);
    core.queue.shutdown();
}
type Events = Arc<std::sync::Mutex<Vec<opennavo_desktop_lib::events::CoreEvent>>>;

/// A core with the fixture inventory and update list, and a brew that answers detection but fails every read:
/// the post-task library scan and outdated check both fail, so only settling changes the update list (and the
/// Mac's own Homebrew is never consulted).
fn settling_core(root: &Path) -> (Arc<opennavo_desktop_lib::core::DesktopCore>, Events) {
    let db = Database::open(&root.join("data/opennavo.db")).unwrap();
    let (library, data) = fixtures(&db, root);
    db.set_meta("installed_cache", &serde_json::to_string(&library).unwrap())
        .unwrap();
    db.set_meta("outdated_source", &serde_json::to_string(&data).unwrap())
        .unwrap();
    drop(db);
    let events: Events = Arc::new(std::sync::Mutex::new(Vec::new()));
    let saved = events.clone();
    let core = opennavo_desktop_lib::core::DesktopCore::open(
        root.join("data"),
        root.join("cache"),
        root.join("askpass"),
        Arc::new(move |e| saved.lock().unwrap().push(e)),
    )
    .unwrap();
    let brew = root.join("brew");
    std::fs::write(
        &brew,
        format!(
            "#!/usr/bin/python3 -I\nimport sys\nargs = sys.argv[1:]\nif args == ['--version']:\n    print('Homebrew 7.0.7')\nelif args in (['--prefix'], ['--cache']):\n    print({root})\nelse:\n    sys.exit(1)\n",
            root = serde_json::to_string(root.to_str().unwrap()).unwrap()
        ),
    )
    .unwrap();
    std::fs::set_permissions(&brew, std::os::unix::fs::PermissionsExt::from_mode(0o755)).unwrap();
    core.settings.write().unwrap().brew_path = Some(brew.to_string_lossy().into());
    (core, events)
}

fn finished(
    core: &opennavo_desktop_lib::core::DesktopCore,
    op: TaskOp,
    token: &str,
    state: TaskState,
) -> Task {
    let mut task = core
        .queue
        .enqueue(
            op,
            Some(TaskTarget {
                kind: Kind::Formula.into(),
                token: token.into(),
            }),
            TaskOptions::default(),
            TaskTrigger::Manual,
        )
        .unwrap();
    // The queue never runs here, so a task for the same target is still queued and enqueue hands it back.
    task.op = op;
    task.state = state;
    task
}

fn update(core: &opennavo_desktop_lib::core::DesktopCore, token: &str) -> Option<OutdatedItem> {
    core.updates_list()
        .unwrap()
        .into_iter()
        .find(|item| item.token == token)
}

#[tokio::test]
async fn a_successful_upgrade_leaves_the_update_list_before_the_post_task_check() {
    use opennavo_desktop_lib::events::CoreEvent;
    let tmp = tempfile::tempdir().unwrap();
    let (core, events) = settling_core(tmp.path());
    let total = core.rebuild_updates().await.unwrap().len();
    assert!(update(&core, "node").is_some());

    // A failed upgrade keeps offering the update.
    core.after_task(&finished(
        &core,
        TaskOp::Upgrade,
        "pcre2",
        TaskState::Failed,
    ))
    .await;
    assert_eq!(core.updates_list().unwrap().len(), total);

    let upgraded = finished(&core, TaskOp::Upgrade, "node", TaskState::Succeeded);
    events.lock().unwrap().clear();
    core.after_task(&upgraded).await;
    assert!(update(&core, "node").is_none());
    assert_eq!(core.updates_list().unwrap().len(), total - 1);
    // The settled list is the first thing windows hear after the task, before any slower post-task work.
    assert!(matches!(
        events.lock().unwrap().first(),
        Some(CoreEvent::Updates(changed)) if changed.count as usize == total - 1
    ));
    // Rebuilding from the cached brew output, as a catalog sync does, does not bring the update back.
    assert_eq!(core.rebuild_updates().await.unwrap().len(), total - 1);
    let restarted = opennavo_desktop_lib::core::DesktopCore::open(
        tmp.path().join("data"),
        tmp.path().join("cache"),
        tmp.path().join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    assert!(update(&restarted, "node").is_none());
}

#[tokio::test]
async fn a_pin_or_unpin_flips_the_update_at_once() {
    let tmp = tempfile::tempdir().unwrap();
    let (core, _) = settling_core(tmp.path());
    core.rebuild_updates().await.unwrap();
    assert!(!update(&core, "node").unwrap().pinned);

    core.after_task(&finished(&core, TaskOp::Pin, "node", TaskState::Succeeded))
        .await;
    assert!(update(&core, "node").unwrap().pinned);
    // Rebuilds from the cached brew output agree until brew is read again.
    core.rebuild_updates().await.unwrap();
    assert!(update(&core, "node").unwrap().pinned);

    core.after_task(&finished(
        &core,
        TaskOp::Unpin,
        "node",
        TaskState::Succeeded,
    ))
    .await;
    assert!(!update(&core, "node").unwrap().pinned);
}
