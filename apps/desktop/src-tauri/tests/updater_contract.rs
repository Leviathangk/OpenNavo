use opennavo_desktop_lib::{appstore, db::Database, model::*, queue::TaskQueue};
use serde_json::json;
use std::sync::{Arc, RwLock};

#[test]
fn legacy_settings_and_sparse_source_overrides_preserve_defaults() {
    let legacy: Settings = serde_json::from_value(json!({})).unwrap();
    let sparse: Settings =
        serde_json::from_value(json!({"updateSources":{"sparkle":false,"msupdate":true}})).unwrap();
    for source in UpdateSource::ALL {
        assert_eq!(
            legacy.update_sources.enabled(source),
            source != UpdateSource::Msupdate
        );
    }
    assert!(sparse.update_sources.enabled(UpdateSource::Homebrew));
    assert!(!sparse.update_sources.enabled(UpdateSource::Sparkle));
    assert!(sparse.update_sources.enabled(UpdateSource::Msupdate));
    assert!(serde_json::from_value::<Settings>(json!({"updateSources":{"unknown":true}})).is_err());
}

fn item(source: UpdateSource) -> OutdatedItem {
    serde_json::from_value(json!({
        "kind":"link","token":"catalog-navigation-only","name":"App",
        "installedVersion":"1","currentVersion":"2","autoUpdates":false,
        "pinned":false,"ignored":false,"dependents":0,"downloadSize":null,
        "source":source,"appId":"local-stable-id","bundleId":"test.app",
        "channel":"stable","major":true,"notes":null,"integrity":"eddsa",
        "requiresAdmin":false,"running":false,"relaunchPending":false,
        "skippedVersion":null,"criticalUpdate":false,"informationalUrl":null
    }))
    .unwrap()
}

#[test]
fn native_task_identity_never_uses_catalog_navigation_identity() {
    for source in [
        UpdateSource::Sparkle,
        UpdateSource::Electron,
        UpdateSource::Github,
        UpdateSource::Vendor,
        UpdateSource::Msupdate,
    ] {
        let mut update = item(source);
        assert_eq!(
            update.task_target().unwrap(),
            TaskTarget {
                kind: TaskKind::App,
                token: "local-stable-id".into()
            }
        );
        update.token.clear();
        assert_eq!(update.task_target().unwrap().token, "local-stable-id");
        assert_eq!(update.app_key().unwrap(), "app:local-stable-id");
        update.informational_url = Some("https://example.org/update".into());
        assert_eq!(update.task_target().unwrap_err().code, "E_NOT_INSTALLABLE");
        update.informational_url = None;
        update.app_id = None;
        assert_eq!(update.task_target().unwrap_err().code, "E_UPDATE_FEED");
    }
    let mut mas = item(UpdateSource::Mas);
    mas.token = "12345".into();
    assert_eq!(
        mas.task_target().unwrap(),
        TaskTarget {
            kind: TaskKind::Mas,
            token: "12345".into()
        }
    );
}

#[test]
fn mas_stage_mapping_does_not_invent_administrator_install_progress() {
    let purchasing = appstore::progress::stage("purchasing", Some(0.0)).unwrap();
    assert_eq!(purchasing.phase, Some(TaskPhase::Preparing));
    assert_eq!(purchasing.percent, None);
    let downloading = appstore::progress::stage("downloading", Some(37.5)).unwrap();
    assert_eq!(downloading.phase, Some(TaskPhase::Downloading));
    assert_eq!(downloading.percent, Some(37.5));
    let installing = appstore::progress::stage("installing", None).unwrap();
    assert_eq!(installing.phase, Some(TaskPhase::Installing));
    assert_eq!(installing.percent, None);
    assert_eq!(installing.step_index, Some(2));
    assert!(appstore::progress::stage("downloading", Some(f32::NAN)).is_err());
    assert!(appstore::progress::stage("unknown", None).is_err());
}

#[test]
fn invalid_native_install_targets_never_fall_through_to_brew() {
    let dir = tempfile::tempdir().unwrap();
    let db = Database::open(&dir.path().join("app.db")).unwrap();
    let queue = TaskQueue::new(
        db.clone(),
        Arc::new(RwLock::new(Settings::default())),
        dir.path().join("logs"),
        dir.path().join("askpass"),
        Arc::new(|_| panic!("stub emitted a task")),
    )
    .unwrap();
    for (kind, op) in [
        (TaskKind::Mas, TaskOp::Install),
        (TaskKind::Mas, TaskOp::Upgrade),
        (TaskKind::App, TaskOp::Upgrade),
    ] {
        let error = queue
            .enqueue(
                op,
                Some(TaskTarget {
                    kind,
                    token: "12345".into(),
                }),
                TaskOptions::default(),
                TaskTrigger::Manual,
            )
            .unwrap_err();
        assert_eq!(
            error.code,
            if kind == TaskKind::Mas {
                "E_MAS_ENGINE"
            } else {
                "E_INVALID_ARG"
            }
        );
        assert!(db.tasks().unwrap().is_empty());
    }
    assert!(
        db.meta("schema_version")
            .unwrap()
            .unwrap()
            .parse::<u32>()
            .unwrap()
            >= 6
    );
    db.with(|c| {
        c.execute("INSERT INTO update_prefs(app_key) VALUES('app:test')", [])?;
        let values: (String, i64) =
            c.query_row("SELECT channel,ignored FROM update_prefs", [], |r| {
                Ok((r.get(0)?, r.get(1)?))
            })?;
        assert_eq!(values, ("stable".into(), 0));
        Ok(())
    })
    .unwrap();
}

#[test]
fn updater_mutations_are_registered_and_main_only() {
    let registration = include_str!("../src/lib.rs");
    let acl = include_str!("../permissions/native.toml");
    for command in [
        "update_skip",
        "update_set_channel",
        "update_rollback",
        "update_rollback_targets",
        "app_relaunch",
    ] {
        assert!(registration.contains(&format!("commands::updates::{command},")));
        for permission in acl.split("[[permission]]").skip(1) {
            assert_eq!(
                permission.contains(&format!("\"{command}\"")),
                permission.contains("identifier = \"native-main\"")
            );
        }
    }
}

#[test]
fn default_check_includes_m4_sources_and_omits_msupdate() {
    use opennavo_desktop_lib::updater::{UpdateSource, UpdateSources, check_sources};
    let mut settings = UpdateSources::default();
    for source in UpdateSource::ALL {
        settings.0.insert(source, false);
    }
    assert!(check_sources(None, &settings).unwrap().is_empty());
    assert_eq!(
        check_sources(Some(vec![UpdateSource::Msupdate]), &settings)
            .unwrap_err()
            .code,
        "E_NOT_IMPLEMENTED"
    );
    assert_eq!(
        check_sources(None, &UpdateSources::default()).unwrap(),
        vec![
            UpdateSource::Homebrew,
            UpdateSource::Mas,
            UpdateSource::Sparkle,
            UpdateSource::Electron,
            UpdateSource::Github,
            UpdateSource::Vendor
        ]
    );
}

#[test]
fn mas_setting_migrates_legacy_false_and_explicit_new_setting_wins() {
    for (value, expected) in [
        (json!({"appStoreUpdates":false}), false),
        (
            json!({"appStoreUpdates":false,"updateSources":{"mas":true}}),
            true,
        ),
        (
            json!({"appStoreUpdates":true,"updateSources":{"mas":false}}),
            false,
        ),
    ] {
        let settings: Settings = serde_json::from_value(value).unwrap();
        assert_eq!(settings.update_sources.enabled(UpdateSource::Mas), expected);
        assert_eq!(settings.app_store_updates, expected);
    }
}
#[test]
fn unified_preferences_keep_skip_channel_and_ignore_independent_across_restart() {
    use opennavo_desktop_lib::updater::prefs;
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("prefs.sqlite");
    for key in [
        "homebrew:cask:example",
        "homebrew:formula:example",
        "app:stable-local-id",
    ] {
        let db = Database::open(&path).unwrap();
        prefs::skip(&db, key, Some("2.0")).unwrap();
        prefs::channel(&db, key, UpdateChannel::Beta).unwrap();
        prefs::ignore(&db, key, true).unwrap();
        drop(db);
        let db = Database::open(&path).unwrap();
        let p = prefs::load(&db, key).unwrap();
        assert!(p.ignored);
        assert_eq!(p.skipped_version.as_deref(), Some("2.0"));
        assert_eq!(p.channel, UpdateChannel::Beta);
        prefs::skip(&db, key, None).unwrap();
        prefs::ignore(&db, key, false).unwrap();
        let p = prefs::load(&db, key).unwrap();
        assert!(!p.ignored);
        assert!(p.skipped_version.is_none());
        assert_eq!(p.channel, UpdateChannel::Beta);
    }
}

#[test]
fn download_identity_contract_is_nullable_and_backwards_readable() {
    let old = item(UpdateSource::Homebrew);
    assert!(old.download_host.is_none());
    assert!(old.expected_team_match.is_none());
    let mut new = item(UpdateSource::Vendor);
    new.download_host = Some("dldir1.qq.com".into());
    new.expected_team_match = Some(false);
    let json = serde_json::to_value(new).unwrap();
    assert_eq!(json["downloadHost"], "dldir1.qq.com");
    assert_eq!(json["expectedTeamMatch"], false);
    let json = serde_json::to_value(old).unwrap();
    assert!(json["downloadHost"].is_null());
    assert!(json["expectedTeamMatch"].is_null());
}
