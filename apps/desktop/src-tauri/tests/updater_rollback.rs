use opennavo_desktop_lib::{
    AppError,
    db::Database,
    model::*,
    queue::TaskQueue,
    uninstall::rules::Snapshot,
    updater::{
        backup, gate, prefs,
        rollback::{self, Plan},
        swap,
    },
};
use std::{
    fs,
    path::{Path, PathBuf},
    sync::{Arc, RwLock},
};

const APP_ID: &str = "fixture-app";

fn bundle(path: &Path, version: &str) {
    fs::create_dir_all(path.join("Contents/MacOS")).unwrap();
    let info = plist::Value::Dictionary(
        [
            ("CFBundleIdentifier", "test.opennavo.Fixture"),
            ("CFBundleShortVersionString", version),
            ("CFBundleVersion", "1"),
            ("CFBundleExecutable", "fixture"),
        ]
        .into_iter()
        .map(|(k, v)| (k.to_owned(), plist::Value::String(v.into())))
        .collect(),
    );
    info.to_file_xml(path.join("Contents/Info.plist")).unwrap();
    fs::write(path.join("Contents/MacOS/fixture"), version).unwrap();
}
fn content(path: &Path) -> String {
    fs::read_to_string(path.join("Contents/MacOS/fixture")).unwrap()
}

/// Signature observations without codesign. `other_team` gives one bundle another signer.
struct Observations {
    other_team: Option<PathBuf>,
}
impl gate::Inspector for Observations {
    fn inspect(&self, path: &Path) -> Result<gate::Identity, AppError> {
        let info = plist::Value::from_file(path.join("Contents/Info.plist")).unwrap();
        let dict = info.as_dictionary().unwrap();
        let text = |key: &str| dict[key].as_string().unwrap().to_owned();
        Ok(gate::Identity {
            valid: true,
            team_id: if self.other_team.as_deref() == Some(path) {
                "OTHERTEAM1".into()
            } else {
                "TESTTEAM01".into()
            },
            identifier: "test.opennavo.Fixture".into(),
            requirement: "fixture requirement".into(),
            notarized: true,
            architectures: ["arm64".into(), "x86_64".into()].into(),
            minimum_os: "13.0".into(),
            minimum_os_by_arch: Default::default(),
            version: text("CFBundleVersion"),
            marketing_version: text("CFBundleShortVersionString"),
        })
    }
    fn satisfies(&self, _: &Path, _: &str) -> Result<bool, AppError> {
        Ok(true)
    }
}
const TRUSTED: Observations = Observations { other_team: None };

struct TempFiles {
    fail_register: bool,
}
impl swap::Files for TempFiles {
    fn copy_bundle(&self, from: &Path, to: &Path) -> Result<(), AppError> {
        swap::NativeFiles.copy_bundle(from, to)
    }
    fn exchange(&self, a: &Path, b: &Path) -> Result<(), AppError> {
        swap::NativeFiles.exchange(a, b)
    }
    fn register(&self, path: &Path) -> Result<(), AppError> {
        assert!(!path.to_string_lossy().contains(".opennavo-updates"));
        if self.fail_register {
            Err(AppError::new("E_UNKNOWN", "fixture_registration_failure"))
        } else {
            Ok(())
        }
    }
}

struct Fixture {
    _dir: tempfile::TempDir,
    root: PathBuf,
    db: Database,
    installed: PathBuf,
}
impl Fixture {
    /// Installs 1.0, then updates it to 2.0 through the real swap, as a Sparkle
    /// update task would, and records the update the way the queue does.
    fn updated() -> (Self, String) {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().canonicalize().unwrap();
        let db = Database::open(&root.join("db")).unwrap();
        let installed = root.join("Fixture.app");
        bundle(&installed, "1.0");
        let fixture = Self {
            _dir: dir,
            root,
            db,
            installed,
        };
        let id = fixture.update("1.0", "2.0", 1_000);
        (fixture, id)
    }
    fn update(&self, from: &str, to: &str, finished_at: i64) -> String {
        let prepared = self.root.join(format!("Prepared-{to}.app"));
        bundle(&prepared, to);
        let request = swap::SwapRequest {
            source: Default::default(),
            installed: self.installed.clone(),
            prepared: prepared.clone(),
            installed_snapshot: Snapshot::capture(&self.installed).unwrap(),
            prepared_snapshot: Snapshot::capture(&prepared).unwrap(),
            expected_team: None,
            package: None,
        };
        let saved = swap::apply(
            &request,
            &swap::Context {
                inspector: &TRUSTED,
                files: &TempFiles {
                    fail_register: false,
                },
                os: "15.0",
                arch: "arm64",
            },
        )
        .unwrap();
        let id = format!("task-{finished_at}");
        self.db
            .save_task(&task(
                &id,
                TaskOp::Upgrade,
                UpdateSource::Sparkle,
                finished_at,
            ))
            .unwrap();
        self.db
            .set_meta(
                &rollback::record_key(&id),
                &serde_json::json!({"appId":APP_ID,"path":saved,"fromVersion":from,"toVersion":to})
                    .to_string(),
            )
            .unwrap();
        id
    }
    fn resolve(&self) -> impl Fn(&str) -> Result<PathBuf, AppError> + '_ {
        |id| {
            assert_eq!(id, APP_ID);
            Ok(self.installed.clone())
        }
    }
    fn plan(&self, id: &str) -> Result<Plan, AppError> {
        rollback::plan(&self.db, id, &self.resolve())
    }
    fn slot(&self) -> PathBuf {
        backup::directories(&self.installed).unwrap()[0].clone()
    }
}
fn task(id: &str, op: TaskOp, source: UpdateSource, finished_at: i64) -> Task {
    Task {
        target_name: None,
        outcome: None,
        id: id.into(),
        op,
        target: Some(TaskTarget {
            kind: TaskKind::App,
            token: APP_ID.into(),
        }),
        options: TaskOptions {
            source: Some(source),
            ..Default::default()
        },
        trigger: TaskTrigger::Manual,
        state: TaskState::Succeeded,
        phase: None,
        percent: None,
        bytes_done: None,
        bytes_total: None,
        speed_bps: None,
        step_index: None,
        step_count: None,
        from_version: None,
        to_version: None,
        error: None,
        exit_code: None,
        log_path: None,
        created_at: finished_at - 10,
        started_at: Some(finished_at - 5),
        finished_at: Some(finished_at),
    }
}
fn reason(result: Result<Plan, AppError>) -> String {
    let error = result.unwrap_err();
    assert_eq!(error.code, "E_ROLLBACK_UNAVAILABLE", "{error:?}");
    error.message
}

#[test]
fn rollback_restores_the_replaced_version_and_spends_the_record() {
    let (fixture, id) = Fixture::updated();
    assert_eq!(content(&fixture.installed), "2.0");
    assert_eq!(
        rollback::available(
            &fixture.db,
            &[id.clone(), "other".into()],
            &fixture.resolve()
        )
        .unwrap(),
        vec![id.clone()]
    );
    let plan = fixture.plan(&id).unwrap();
    assert_eq!(plan.source, UpdateSource::Sparkle);
    assert_eq!(
        (
            plan.current_version.as_str(),
            plan.restored_version.as_str()
        ),
        ("2.0", "1.0")
    );
    rollback::apply(
        &plan,
        &TRUSTED,
        &TempFiles {
            fail_register: false,
        },
        "15.0",
        "arm64",
    )
    .unwrap();
    assert_eq!(content(&fixture.installed), "1.0");
    // The version rolled back from is not kept, and empty storage goes with it.
    assert!(!plan.backup.exists());
    assert!(!fixture.slot().exists());
    assert!(!fixture.slot().parent().unwrap().exists());
    rollback::finish(&fixture.db, &plan).unwrap();
    assert!(
        fixture
            .db
            .meta(&rollback::record_key(&id))
            .unwrap()
            .is_none()
    );
    assert_eq!(
        prefs::load(&fixture.db, &format!("app:{APP_ID}"))
            .unwrap()
            .skipped_version
            .as_deref(),
        Some("2.0")
    );
    assert_eq!(reason(fixture.plan(&id)), "no_backup");
    assert!(
        rollback::available(&fixture.db, &[id], &fixture.resolve())
            .unwrap()
            .is_empty()
    );
}

#[test]
fn only_the_newest_update_of_an_unchanged_app_can_roll_back() {
    let (fixture, first) = Fixture::updated();
    // A second update rotates the slot: the first record no longer owns it.
    let second = fixture.update("2.0", "3.0", 2_000);
    assert_eq!(reason(fixture.plan(&first)), "newer_change");
    let plan = fixture.plan(&second).unwrap();
    assert_eq!(plan.restored_version, "2.0");
    // A later rollback of the same app also ends the record's turn.
    fixture
        .db
        .save_task(&task(
            "rolled",
            TaskOp::Rollback,
            UpdateSource::Sparkle,
            3_000,
        ))
        .unwrap();
    assert_eq!(reason(fixture.plan(&second)), "newer_change");
    fixture
        .db
        .with(|c| {
            c.execute("DELETE FROM tasks WHERE id='rolled'", [])?;
            Ok(())
        })
        .unwrap();

    // The app updated itself since: rolling back would skip a version silently.
    bundle(&fixture.installed, "3.1");
    assert_eq!(reason(fixture.plan(&second)), "installed_version_changed");
    bundle(&fixture.installed, "3.0");
    bundle(&plan.backup, "1.5");
    assert_eq!(reason(fixture.plan(&second)), "backup_version_changed");
    bundle(&plan.backup, "2.0");
    fixture.plan(&second).unwrap();

    let protected = |_: &str| -> Result<PathBuf, AppError> {
        Err(AppError::new("E_PROTECTED", "protected_update_target"))
    };
    assert_eq!(
        reason(rollback::plan(&fixture.db, &second, &protected)),
        "protected"
    );
    let missing =
        |_: &str| -> Result<PathBuf, AppError> { Err(AppError::new("E_NOT_FOUND", "local_app")) };
    assert_eq!(
        reason(rollback::plan(&fixture.db, &second, &missing)),
        "app_missing"
    );
    // A moved app hashes to another slot.
    let moved = fixture.root.join("Moved.app");
    let elsewhere = |_: &str| -> Result<PathBuf, AppError> { Ok(moved.clone()) };
    bundle(&moved, "3.0");
    assert_eq!(
        reason(rollback::plan(&fixture.db, &second, &elsewhere)),
        "backup_app_changed"
    );

    for (id, op, source, state) in [
        (
            "brew",
            TaskOp::Upgrade,
            UpdateSource::Homebrew,
            TaskState::Succeeded,
        ),
        (
            "store",
            TaskOp::Upgrade,
            UpdateSource::Mas,
            TaskState::Succeeded,
        ),
        (
            "failed",
            TaskOp::Upgrade,
            UpdateSource::Sparkle,
            TaskState::Failed,
        ),
        (
            "reinstall",
            TaskOp::Reinstall,
            UpdateSource::Sparkle,
            TaskState::Succeeded,
        ),
    ] {
        let mut entry = task(id, op, source, 500);
        entry.state = state;
        fixture.db.save_task(&entry).unwrap();
        assert_eq!(reason(fixture.plan(id)), "not_native_update", "{id}");
    }
    assert_eq!(reason(fixture.plan("unknown")), "history_missing");
}

#[test]
fn a_substituted_slot_or_record_is_refused() {
    let (fixture, id) = Fixture::updated();
    let plan = fixture.plan(&id).unwrap();
    // A record may only point at this app's own previous.app.
    for path in [
        fixture.root.join("Elsewhere.app"),
        fixture.slot().join("other.app"),
        backup::directories(&fixture.root.join("Other.app")).unwrap()[0].join("previous.app"),
    ] {
        fixture
            .db
            .set_meta(
                &rollback::record_key(&id),
                &serde_json::json!({"appId":APP_ID,"path":path,"fromVersion":"1.0","toVersion":"2.0"})
                    .to_string(),
            )
            .unwrap();
        assert_eq!(reason(fixture.plan(&id)), "backup_app_changed", "{path:?}");
    }
    fixture
        .db
        .set_meta(
            &rollback::record_key(&id),
            &serde_json::json!({"appId":"another","path":plan.backup,"fromVersion":"1.0","toVersion":"2.0"})
                .to_string(),
        )
        .unwrap();
    assert_eq!(reason(fixture.plan(&id)), "backup_app_changed");
    fixture
        .db
        .set_meta(
            &rollback::record_key(&id),
            &serde_json::json!({"appId":APP_ID,"path":plan.backup,"fromVersion":"1.0","toVersion":"2.0"})
                .to_string(),
        )
        .unwrap();
    fixture.plan(&id).unwrap();

    // A previous.app replaced by a link to another bundle is not a backup.
    let decoy = fixture.root.join("Decoy.app");
    bundle(&decoy, "1.0");
    let kept = fixture.root.join("kept.app");
    fs::rename(&plan.backup, &kept).unwrap();
    std::os::unix::fs::symlink(&decoy, &plan.backup).unwrap();
    assert_eq!(reason(fixture.plan(&id)), "no_backup");
    fs::remove_file(&plan.backup).unwrap();
    fs::rename(&kept, &plan.backup).unwrap();

    // Storage others can read was not made by the update.
    use std::os::unix::fs::PermissionsExt;
    fs::set_permissions(fixture.slot(), fs::Permissions::from_mode(0o755)).unwrap();
    assert_eq!(reason(fixture.plan(&id)), "backup_unsafe");
    fs::set_permissions(fixture.slot(), fs::Permissions::from_mode(0o700)).unwrap();
    fixture.plan(&id).unwrap();
}

#[test]
fn another_signer_or_a_failed_registration_leaves_the_update_in_place() {
    let (fixture, id) = Fixture::updated();
    let plan = fixture.plan(&id).unwrap();
    let error = rollback::apply(
        &plan,
        &Observations {
            other_team: Some(plan.backup.clone()),
        },
        &TempFiles {
            fail_register: false,
        },
        "15.0",
        "arm64",
    )
    .unwrap_err();
    assert_eq!(
        (error.code.as_str(), error.message.as_str()),
        ("E_ROLLBACK_INTEGRITY", "signing_identity_changed")
    );
    assert_eq!(content(&fixture.installed), "2.0");
    assert_eq!(content(&plan.backup), "1.0");

    // The backup must not need a newer macOS than this Mac runs.
    let error = rollback::apply(
        &plan,
        &TRUSTED,
        &TempFiles {
            fail_register: false,
        },
        "12.0",
        "arm64",
    )
    .unwrap_err();
    assert_eq!(error.code, "E_ROLLBACK_INTEGRITY");
    assert_eq!(content(&fixture.installed), "2.0");

    let error = rollback::apply(
        &plan,
        &TRUSTED,
        &TempFiles {
            fail_register: true,
        },
        "15.0",
        "arm64",
    )
    .unwrap_err();
    assert_eq!(
        (error.code.as_str(), error.message.as_str()),
        ("E_UPDATE_SWAP", "rollback_reverted")
    );
    assert_eq!(content(&fixture.installed), "2.0");
    assert_eq!(content(&plan.backup), "1.0");
    // Nothing was spent: the same rollback still works afterwards.
    fixture.plan(&id).unwrap();
    rollback::apply(
        &plan,
        &TRUSTED,
        &TempFiles {
            fail_register: false,
        },
        "15.0",
        "arm64",
    )
    .unwrap();
    assert_eq!(content(&fixture.installed), "1.0");
}

#[test]
fn a_backup_from_before_the_noindex_storage_still_rolls_back() {
    use std::os::unix::fs::DirBuilderExt;
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().canonicalize().unwrap();
    let db = Database::open(&root.join("db")).unwrap();
    let installed = root.join("Fixture.app");
    bundle(&installed, "2.0");
    let legacy = backup::directories(&installed).unwrap()[1].clone();
    for path in [legacy.parent().unwrap(), legacy.as_path()] {
        fs::DirBuilder::new().mode(0o700).create(path).unwrap();
    }
    bundle(&legacy.join("previous.app"), "1.0");
    db.save_task(&task("old", TaskOp::Upgrade, UpdateSource::Electron, 1_000))
        .unwrap();
    db.set_meta(
        &rollback::record_key("old"),
        &serde_json::json!({"appId":APP_ID,"path":legacy.join("previous.app"),"fromVersion":"1.0","toVersion":"2.0"})
            .to_string(),
    )
    .unwrap();
    let resolve = |_: &str| -> Result<PathBuf, AppError> { Ok(installed.clone()) };
    let plan = rollback::plan(&db, "old", &resolve).unwrap();
    assert_eq!(plan.source, UpdateSource::Electron);
    rollback::apply(
        &plan,
        &TRUSTED,
        &TempFiles {
            fail_register: false,
        },
        "15.0",
        "arm64",
    )
    .unwrap();
    assert_eq!(content(&installed), "1.0");
    assert!(!legacy.parent().unwrap().exists());
}

#[test]
fn the_queue_takes_rollbacks_only_for_a_recorded_update() {
    let dir = tempfile::tempdir().unwrap();
    let db = Database::open(&dir.path().join("db")).unwrap();
    let queue = TaskQueue::new(
        db.clone(),
        Arc::new(RwLock::new(Settings::default())),
        dir.path().join("logs"),
        dir.path().join("askpass"),
        Arc::new(|_| panic!("invalid task emitted event")),
    )
    .unwrap();
    let app = TaskTarget {
        kind: TaskKind::App,
        token: APP_ID.into(),
    };
    let rollback_of = |id: &str| TaskOptions {
        source: Some(UpdateSource::Sparkle),
        rollback_of: Some(id.into()),
        ..Default::default()
    };
    let cases = [
        (
            TaskOp::Rollback,
            app.clone(),
            TaskOptions::default(),
            "E_INVALID_ARG",
        ),
        (
            TaskOp::Upgrade,
            app.clone(),
            rollback_of("x"),
            "E_INVALID_ARG",
        ),
        (
            TaskOp::Rollback,
            TaskTarget {
                kind: TaskKind::Cask,
                token: "fixture".into(),
            },
            rollback_of("x"),
            "E_INVALID_ARG",
        ),
        (
            TaskOp::Rollback,
            app.clone(),
            rollback_of("x"),
            "E_ROLLBACK_UNAVAILABLE",
        ),
    ];
    for (op, target, options, code) in cases {
        let error = queue
            .enqueue(op, Some(target), options, TaskTrigger::Manual)
            .unwrap_err();
        assert_eq!(error.code, code, "{op:?}");
    }
    assert_eq!(
        queue
            .enqueue_many(
                TaskOp::Rollback,
                vec![app],
                rollback_of("x"),
                TaskTrigger::Manual
            )
            .unwrap_err()
            .code,
        "E_INVALID_ARG"
    );
    assert_eq!(
        queue.enqueue_rollback("x").unwrap_err().code,
        "E_ROLLBACK_UNAVAILABLE"
    );
    assert!(queue.rollback_targets(&["x".into()]).unwrap().is_empty());
    assert_eq!(
        queue
            .rollback_targets(&vec![String::new(); 201])
            .unwrap_err()
            .code,
        "E_INVALID_ARG"
    );
    assert!(db.tasks().unwrap().is_empty());
}

#[test]
fn electron_rollback_does_not_confuse_prereleases_with_each_other_or_stable() {
    let (fixture, _) = Fixture::updated();
    let id = "electron-history".to_owned();
    fixture
        .db
        .save_task(&task(&id, TaskOp::Upgrade, UpdateSource::Electron, 2_000))
        .unwrap();
    let backup = fixture.slot().join("previous.app");
    bundle(&fixture.installed, "2.0.0-beta.2");
    bundle(&backup, "2.0.0-beta.1");
    fixture.db.set_meta(&rollback::record_key(&id), &serde_json::json!({"appId":APP_ID,"path":backup,"fromVersion":"2.0.0-beta.1","toVersion":"2.0.0-beta.2"}).to_string()).unwrap();
    let plan = fixture.plan(&id).unwrap();
    bundle(&fixture.installed, "2.0.0");
    assert_eq!(reason(fixture.plan(&id)), "installed_version_changed");
    bundle(&fixture.installed, "2.0.0-beta.2");
    bundle(&backup, "2.0.0-beta.0");
    assert_eq!(reason(fixture.plan(&id)), "backup_version_changed");
    assert_eq!(
        rollback::apply(
            &plan,
            &TRUSTED,
            &TempFiles {
                fail_register: false
            },
            "15.0",
            "arm64"
        )
        .unwrap_err()
        .message,
        "rollback_version_changed"
    );
    bundle(&backup, "2.0.0-beta.1");
    rollback::apply(
        &plan,
        &TRUSTED,
        &TempFiles {
            fail_register: false,
        },
        "15.0",
        "arm64",
    )
    .unwrap();
    assert_eq!(content(&fixture.installed), "2.0.0-beta.1");
}
