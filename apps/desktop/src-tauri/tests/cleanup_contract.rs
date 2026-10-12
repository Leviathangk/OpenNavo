use opennavo_desktop_lib::{db::Database, model::*, settings};
#[test]
fn cleanup_settings_defaults_and_legacy_false_values() {
    let defaults = Settings::default();
    assert_eq!(defaults.cleanup_delete_mode, DeleteMode::Trash);
    assert_eq!(defaults.cleanup_reminder, CleanupReminder::Off);
    assert_eq!(defaults.cleanup_reminder_bytes, 10_000_000_000);
    assert_eq!(
        defaults.cleanup_project_roots,
        ["~/Projects", "~/Developer", "~/code", "~/GitHub", "~/dev"]
    );
    let old: Settings =
        serde_json::from_str(r#"{"zapByDefault":false,"autoCheck":false}"#).unwrap();
    assert!(!old.zap_by_default);
    assert!(!old.auto_check);
    assert_eq!(old.cleanup_reminder, CleanupReminder::Off);
    assert_eq!(old.cleanup_project_roots, defaults.cleanup_project_roots);
    let changed: Settings = serde_json::from_str(r#"{"cleanupDeleteMode":"delete","cleanupReminder":"monthly","cleanupReminderBytes":123,"cleanupProjectRoots":["~/Custom"]}"#).unwrap();
    assert_eq!(changed.cleanup_delete_mode, DeleteMode::Delete);
    assert_eq!(changed.cleanup_reminder, CleanupReminder::Monthly);
    assert_eq!(changed.cleanup_reminder_bytes, 123);
    let mut invalid = defaults;
    invalid.cleanup_project_roots = vec!["~/Projects/../../Documents".into()];
    assert!(settings::validate(invalid).is_err());
}
#[test]
fn protect_input_rejects_mixed_raw_paths_and_ids_and_permissions_are_main_only() {
    assert!(
        serde_json::from_str::<ProtectInput>(
            r#"{"reportId":"report","itemId":"item","value":"/etc","kind":"path"}"#
        )
        .is_err()
    );
    let acl = include_str!("../permissions/native.toml");
    for section in acl.split("[[permission]]").skip(1) {
        for command in [
            "cleanup_scan",
            "cleanup_cancel",
            "cleanup_execute",
            "cleanup_reveal",
            "cleanup_summary",
            "cleanup_protect",
            "cleanup_unprotect",
            "cleanup_protected_list",
        ] {
            assert_eq!(
                section.contains(&format!("\"{command}\"")),
                section.contains("identifier = \"native-main\"")
            );
        }
    }
}
#[test]
fn migration_v3_preserves_uninstall_outcomes_under_new_name() {
    let temp = tempfile::tempdir().unwrap();
    let path = temp.path().join("old.db");
    let c = rusqlite::Connection::open(&path).unwrap();
    c.execute_batch(include_str!("../migrations/001_initial.sql"))
        .unwrap();
    c.execute_batch(include_str!("../migrations/002_catalog_texts.sql"))
        .unwrap();
    c.execute_batch(include_str!("../migrations/003_uninstall.sql"))
        .unwrap();
    c.execute(
        "INSERT INTO meta(key,value) VALUES('schema_version','3')",
        [],
    )
    .unwrap();
    c.execute("INSERT INTO tasks(id,op,options,trigger,state,created_at,target_name,uninstall) VALUES('old','uninstall','{}','manual','succeeded',1,'Example',?1)", [r#"{"freedBytes":0,"trashed":1,"deleted":0,"skipped":[],"restorable":true}"#]).unwrap();
    drop(c);
    let db = Database::open(&path).unwrap();
    let task = db.tasks().unwrap().remove(0);
    assert!(task.outcome.as_ref().unwrap().restorable);
    assert_eq!(task.outcome.as_ref().unwrap().trashed_bytes, 0);
    let json = serde_json::to_value(&task).unwrap();
    assert!(json.get("outcome").is_some());
    assert!(json.get("uninstall").is_none());
    db.save_task(&task).unwrap();
    assert_eq!(db.tasks().unwrap()[0].outcome, task.outcome);
}

#[test]
fn removed_apis_have_no_registration_permissions_or_generated_types() {
    let commands = include_str!("../src/lib.rs");
    let acl = include_str!("../permissions/native.toml");
    for command in ["leftovers_scan", "leftovers_remove", "cleanup_estimate"] {
        assert!(!commands.contains(&format!("::{command},")));
        assert!(!acl.contains(&format!("\"{command}\"")));
    }
    for op in ["remove_leftovers", "cleanup"] {
        assert!(serde_json::from_value::<TaskOp>(serde_json::json!(op)).is_ok());
    }
}

#[test]
fn c2_device_support_reasons_and_process_names_are_required_output_fields() {
    for (reason, value) in [
        (CleanupReason::DeviceSupport, "device_support"),
        (CleanupReason::DeviceSupportLatest, "device_support_latest"),
    ] {
        assert_eq!(serde_json::to_value(reason).unwrap(), value);
    }
    let skipped = SkippedGroup {
        title: "Go".into(),
        app_id: None,
        bytes: Some(10),
        reason: SkippedGroupReason::Running,
        processes: vec!["compile".into(), "go".into()],
    };
    let json = serde_json::to_value(skipped).unwrap();
    assert_eq!(json["processes"], serde_json::json!(["compile", "go"]));
    let other = SkippedGroup {
        title: "Protected".into(),
        app_id: None,
        bytes: None,
        reason: SkippedGroupReason::Protected,
        processes: vec![],
    };
    assert_eq!(
        serde_json::to_value(other).unwrap()["processes"],
        serde_json::json!([])
    );
}

#[test]
fn c21_kept_open_files_is_a_required_numeric_output_field() {
    let item = CleanupItem {
        id: "synthetic".into(),
        path: "/synthetic/cache".into(),
        kind: CleanupItemKind::Dir,
        command: None,
        size_bytes: Some(100),
        kept_open_files: 2,
        modified_at: None,
        reason: CleanupReason::ToolCache,
        selected: true,
        delete_mode: DeleteMode::Delete,
        needs_admin: false,
        note: None,
    };
    assert_eq!(serde_json::to_value(item).unwrap()["keptOpenFiles"], 2);
}
