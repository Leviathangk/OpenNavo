use opennavo_desktop_lib::{db::Database, model::*, queue::TaskQueue};
use std::sync::{Arc, RwLock};

#[test]
fn defaults_preserve_saved_choices_and_target_names_survive_history() {
    assert!(Settings::default().zap_by_default);
    assert_eq!(Settings::default().uninstall_delete_mode, DeleteMode::Trash);
    let legacy: Settings = serde_json::from_str(r#"{"zapByDefault":false}"#).unwrap();
    assert!(!legacy.zap_by_default);
    let tmp = tempfile::tempdir().unwrap();
    let db = Database::open(&tmp.path().join("db.sqlite")).unwrap();
    let queue = TaskQueue::new(
        db.clone(),
        Arc::new(RwLock::new(legacy)),
        tmp.path().join("logs"),
        tmp.path().join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    let mut task = queue
        .enqueue(
            TaskOp::Uninstall,
            Some(TaskTarget {
                kind: TaskKind::Cask,
                token: "sample".into(),
            }),
            Default::default(),
            TaskTrigger::Manual,
        )
        .unwrap();
    task.target_name = Some("Sample editor".into());
    task.state = TaskState::Succeeded;
    task.finished_at = Some(1);
    db.save_task(&task).unwrap();
    let result = db
        .history(&HistoryQuery {
            q: Some("EDITOR".into()),
            ops: vec![],
            states: vec![],
            target: None,
            include_scheduled_updates: true,
            from: None,
            to: None,
            limit: 10,
            offset: 0,
        })
        .unwrap();
    assert_eq!(result.items.len(), 1);
    assert_eq!(
        result.items[0].target_name.as_deref(),
        Some("Sample editor")
    );
}

#[test]
fn new_commands_are_main_only() {
    let permissions = include_str!("../permissions/native.toml");
    let main = permissions
        .split("identifier = \"native-tray\"")
        .next()
        .unwrap();
    let restricted = permissions
        .split("identifier = \"native-tray\"")
        .nth(1)
        .unwrap();
    for command in [
        "local_apps_list",
        "local_apps_refresh",
        "local_app_open",
        "local_app_reveal",
        "local_app_footprint",
        "local_app_footprints",
        "local_apps_scan_footprints",
        "uninstall_plan",
        "uninstall_plan_cancel",
        "uninstall_reveal",
        "uninstall_execute",
        "uninstall_restore",
        "uninstall_purge",
    ] {
        assert!(main.contains(&format!("\"{command}\"")));
        assert!(!restricted.contains(&format!("\"{command}\"")));
    }
}
