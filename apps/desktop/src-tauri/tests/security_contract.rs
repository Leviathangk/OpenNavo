use opennavo_desktop_lib::{db::Database, model::Settings, security::*, settings};
use serde_json::json;

#[test]
fn security_ipc_is_registered_only_for_main_and_events_are_collected() {
    assert_eq!(
        serde_json::to_value(PastePattern::TooLong).unwrap(),
        "too_long"
    );
    let registration = include_str!("../src/lib.rs");
    let events = include_str!("../src/events.rs");
    let acl = include_str!("../permissions/native.toml");
    let main = acl
        .split("[[permission]]")
        .find(|s| s.contains("identifier = \"native-main\""))
        .unwrap();
    for command in [
        "security_overview",
        "posture_check",
        "av_engine_status",
        "av_db_update",
        "av_scan_start",
        "av_scan_cancel",
        "av_scan_get",
        "av_findings_act",
        "quarantine_list",
        "quarantine_restore",
        "quarantine_delete",
        "startup_scan",
        "startup_cached",
        "startup_login_add",
        "startup_item_act",
        "protection_events",
        "download_verify",
        "download_act",
        "av_schedule_status",
        "install_scan_get",
        "app_signature_check",
        "app_signature_get",
        "paste_protection_status",
        "paste_protection_request_access",
        "paste_protection_act",
        "virus_total_status",
        "virus_total_key_set",
        "virus_total_lookup",
    ] {
        assert!(registration.contains(&format!("commands::security::{command},")));
        assert!(main.contains(&format!("\"{command}\"")));
        for other in acl
            .split("[[permission]]")
            .filter(|s| !s.contains("identifier = \"native-main\""))
        {
            assert!(!other.contains(&format!("\"{command}\"")));
        }
    }
    for (wire, name) in [
        ("security:changed", "SecurityChangedEvent"),
        ("av:progress", "AvProgressEvent"),
        ("av:finished", "AvFinishedEvent"),
        ("startup:added", "StartupAddedEvent"),
        ("download:detected", "DownloadDetectedEvent"),
        ("download:verified", "DownloadVerifiedEvent"),
        ("av:schedule", "AvScheduleRunEvent"),
        ("install:scanned", "InstallScannedEvent"),
        ("app-signatures:changed", "AppSignaturesChangedEvent"),
        ("paste:blocked", "PasteBlockedEvent"),
    ] {
        assert!(registration.contains(&format!("events::{name}")));
        assert!(events.contains(&format!("event_name = \"{wire}\"")));
    }
}

#[test]
fn legacy_settings_default_to_no_av_consent_and_keep_explicit_preferences() {
    let legacy: Settings = serde_json::from_value(json!({})).unwrap();
    let value = serde_json::to_value(legacy).unwrap();
    let expected = serde_json::to_value(SecuritySettings::default()).unwrap();
    for (key, default) in expected.as_object().unwrap() {
        assert_eq!(&value[key], default);
    }
    let changed = json!({"avConsent":true,"avDbCheckHours":8,"avKeepResident":true,"watchDownloads":false,"startupAlerts":false,"avExclusions":["~/Private"]});
    let settings: Settings = serde_json::from_value(changed.clone()).unwrap();
    let value = serde_json::to_value(settings::validate(settings).unwrap()).unwrap();
    for (key, wanted) in changed.as_object().unwrap() {
        assert_eq!(&value[key], wanted);
    }
    for invalid in [
        json!({"avDbCheckHours":0}),
        json!({"avScheduleTime":"24:00"}),
        json!({"avScheduleTime":"０3:00"}),
        json!({"avScheduleWeekday":0}),
        json!({"avExclusions":["relative"]}),
        json!({"avExclusions":["/tmp/../etc"]}),
    ] {
        assert!(settings::validate(serde_json::from_value(invalid).unwrap()).is_err());
    }
}

#[test]
fn security_migration_preserves_m2_and_reopens_without_data_loss() {
    let tmp = tempfile::tempdir().unwrap();
    let path = tmp.path().join("test.sqlite");
    let db = Database::open(&path).unwrap();
    assert_eq!(db.meta("schema_version").unwrap().as_deref(), Some("7"));
    db.set_meta("security_test", "kept").unwrap();
    db.with(|c| {
        c.execute(
            "INSERT INTO protection_events(id,at,value) VALUES('e',1,'{}')",
            [],
        )?;
        Ok(())
    })
    .unwrap();
    drop(db);
    let db = Database::open(&path).unwrap();
    assert_eq!(db.meta("security_test").unwrap().as_deref(), Some("kept"));
    let count: i64 = db
        .with(|c| Ok(c.query_row("SELECT count(*) FROM protection_events", [], |r| r.get(0))?))
        .unwrap();
    assert_eq!(count, 1);
}

#[test]
fn security_json_shapes_keep_nullable_fields_and_tagged_quarantine_reasons() {
    let mut startup: StartupSnapshot =
        serde_json::from_value(json!({"scannedAt":1,"items":[],"missing":[],"btmFormat":null}))
            .unwrap();
    assert!(startup.cron_error.is_none());
    assert!(
        serde_json::to_value(&startup)
            .unwrap()
            .get("cronError")
            .is_none()
    );
    startup.cron_error = Some("E_STARTUP_CHANGED".into());
    assert_eq!(
        serde_json::to_value(startup).unwrap()["cronError"],
        "E_STARTUP_CHANGED"
    );

    let legacy: AvScanResult = serde_json::from_value(json!({
        "scanId":"legacy", "kind":"quick", "state":"done", "startedAt":0,
        "finishedAt":1, "filesScanned":0, "bytesScanned":0,
        "skipped":{"dataless":0,"tooLarge":0,"unreadable":1,"excluded":0}, "findings":[]
    }))
    .unwrap();
    assert!(legacy.error_code.is_none());
    assert!(legacy.denied_roots.is_empty());
    assert_eq!(
        serde_json::to_value(&legacy).unwrap()["deniedRoots"],
        json!([])
    );
    assert_eq!(
        serde_json::to_value(legacy).unwrap()["errorCode"],
        serde_json::Value::Null
    );
    for status in ["not_code", "unknown", "modified"] {
        let signature: SignatureInfo = serde_json::from_value(
            json!({"status":status,"teamId":null,"signer":null,"notarized":null}),
        )
        .unwrap();
        assert_eq!(serde_json::to_value(signature).unwrap()["status"], status);
    }
    assert_eq!(
        serde_json::to_value(AppSignatureIssue::Modified).unwrap(),
        "modified"
    );
    assert_eq!(
        serde_json::to_value(opennavo_desktop_lib::model::TaskPhase::Scanning).unwrap(),
        "scanning"
    );
    let reason: QuarantineReason =
        serde_json::from_value(json!({"kind":"threat","signature":"Eicar"})).unwrap();
    assert_eq!(
        serde_json::to_value(reason).unwrap(),
        json!({"kind":"threat","signature":"Eicar"})
    );
    let signature: SignatureInfo = serde_json::from_value(
        json!({"status":"developer_id","teamId":null,"signer":null,"notarized":null}),
    )
    .unwrap();
    assert_eq!(
        serde_json::to_value(signature).unwrap()["notarized"],
        serde_json::Value::Null
    );
    assert_eq!(serde_json::to_value(BtmFormat::V18).unwrap(), "v18");
    assert_eq!(
        serde_json::to_value(StartupItemAction::MarkReviewed).unwrap(),
        "mark_reviewed"
    );
}
