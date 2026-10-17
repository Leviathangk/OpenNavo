use opennavo_desktop_lib::{appstore::install::Runner, model::TaskOp, updater::download::Stage};
use std::{
    collections::BTreeMap,
    path::PathBuf,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};
fn runner(scenario: &str) -> Runner {
    Runner {
        executable: PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("tests/fake-store/store"),
        environment: BTreeMap::from([
            ("OPENNAVO_FAKE_STORE_SCENARIO".into(), scenario.into()),
            ("OPENNAVO_FAKE_STORE_FIXTURES".into(), "1".into()),
        ]),
    }
}
#[tokio::test]
async fn fake_store_success_and_201_retain_snapshots_without_root_actions() {
    for scenario in ["success", "201"] {
        let dir = tempfile::tempdir().unwrap();
        let stage = Stage::create(&dir.path().join("stages")).unwrap();
        let results = runner(scenario)
            .download(
                TaskOp::Install,
                &[42, 43],
                &stage.path,
                Arc::new(AtomicBool::new(false)),
                Arc::new(|_, _, _| Ok(())),
            )
            .await
            .unwrap();
        assert_eq!(results.len(), 2);
        for result in results {
            assert!(result.error.is_none());
            assert_eq!(result.privileged.is_some(), scenario == "201");
            if let Some(request) = result.privileged {
                request.validate(unsafe { nix::libc::getuid() }).unwrap();
                std::fs::write(&request.pkg, b"tampered").unwrap();
                assert!(request.validate(unsafe { nix::libc::getuid() }).is_err());
            }
        }
    }
}
#[tokio::test]
async fn fake_store_reports_typed_errors_and_bounds_unterminated_lines() {
    let dir = tempfile::tempdir().unwrap();
    let stage = Stage::create(&dir.path().join("stages")).unwrap();
    for code in [
        "E_MAS_NOT_SIGNED_IN",
        "E_MAS_NOT_OWNED",
        "E_MAS_PAID",
        "E_MAS_OTHER_ACCOUNT",
        "E_MAS_REGION",
        "E_MAS_RATE_LIMITED",
        "E_MAS_ENGINE",
    ] {
        let results = runner(&format!("error:{code}"))
            .download(
                TaskOp::Upgrade,
                &[42],
                &stage.path,
                Arc::new(AtomicBool::new(false)),
                Arc::new(|_, _, _| Ok(())),
            )
            .await
            .unwrap();
        assert_eq!(results[0].error.as_ref().unwrap().code, code);
    }
    let error = runner("oversized")
        .download(
            TaskOp::Install,
            &[42],
            &stage.path,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_, _, _| Ok(())),
        )
        .await
        .unwrap_err();
    assert_eq!(error.message, "store_protocol_line_size");
}
#[tokio::test]
async fn closing_stdin_cancels_owned_store_child_promptly() {
    let dir = tempfile::tempdir().unwrap();
    let stage = Stage::create(&dir.path().join("stages")).unwrap();
    let cancel = Arc::new(AtomicBool::new(false));
    let signal = cancel.clone();
    tokio::spawn(async move {
        tokio::time::sleep(Duration::from_millis(100)).await;
        signal.store(true, Ordering::Release);
    });
    let result = tokio::time::timeout(
        Duration::from_secs(5),
        runner("cancel").download(
            TaskOp::Install,
            &[42],
            &stage.path,
            cancel,
            Arc::new(|_, _, _| Ok(())),
        ),
    )
    .await
    .unwrap();
    let items = result.unwrap();
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].adam_id, 42);
    assert!(items[0].privileged.is_none());
    assert_eq!(items[0].error.as_ref().unwrap().code, "E_INTERRUPTED");
}

#[test]
fn apple_signed_packages_are_still_scoped_to_the_requested_app() {
    use opennavo_desktop_lib::updater::admin::store_payload_scope;
    let info = br#"<pkg-info install-location="/Applications"><bundle-version><bundle id="test.opennavo.Fixture" path="Fixture.app"/></bundle-version></pkg-info>"#;
    let app = std::path::Path::new("/Applications/Fixture.app");
    let payload = "./Fixture.app\n./Fixture.app/Contents\n./Fixture.app/Contents/MacOS/fixture\n";
    assert!(store_payload_scope(info, payload, app, "test.opennavo.Fixture").is_ok());
    for bad in [
        "./Other.app/Contents/file",
        "./Fixture.app/../Other.app/Contents/file",
        "/Applications/Fixture.app/Contents/file",
        "./Fixture.app",
    ] {
        assert!(store_payload_scope(info, bad, app, "test.opennavo.Fixture").is_err());
    }
    assert!(store_payload_scope(info, payload, app, "test.other.App").is_err());
    let scripts = br#"<pkg-info install-location="/Applications"><scripts/><bundle id="test.opennavo.Fixture"/></pkg-info>"#;
    assert!(store_payload_scope(scripts, payload, app, "test.opennavo.Fixture").is_err());
}

fn jsonl_runner(root: &std::path::Path, percent: Option<&str>) -> Runner {
    use std::os::unix::fs::PermissionsExt;
    let field = percent
        .map(|raw| format!(",\"percent\":{raw}"))
        .unwrap_or_default();
    // Emit literal JSONL so exponent spelling, null, missing and malformed values reach the real decoder.
    let script = format!(
        "#!/bin/sh\ncat <<'OPENNAVO_PROTOCOL_FIXTURE'\n{{\"event\":\"phase\",\"adamId\":42,\"phase\":\"downloading\"{field}}}\n{{\"event\":\"phase\",\"adamId\":42,\"phase\":\"done\",\"percent\":100}}\nOPENNAVO_PROTOCOL_FIXTURE\n"
    );
    let executable = root.join("jsonl-store");
    std::fs::write(&executable, script).unwrap();
    std::fs::set_permissions(&executable, std::fs::Permissions::from_mode(0o700)).unwrap();
    Runner {
        executable,
        environment: BTreeMap::new(),
    }
}

#[tokio::test]
async fn runner_delivers_integer_decimal_exponent_and_unknown_progress() {
    for (raw, expected) in [
        (Some("50"), Some(50.0)),
        (Some("50.0"), Some(50.0)),
        (Some("37.5"), Some(37.5)),
        (Some("3.75e1"), Some(37.5)),
        (Some("null"), None),
        (None, None),
    ] {
        let dir = tempfile::tempdir().unwrap();
        let stage = Stage::create(&dir.path().join("stages")).unwrap();
        let seen = Arc::new(std::sync::Mutex::new(Vec::new()));
        let progress = seen.clone();
        let results = jsonl_runner(dir.path(), raw)
            .download(
                TaskOp::Install,
                &[42],
                &stage.path,
                Arc::new(AtomicBool::new(false)),
                Arc::new(move |id, phase, percent| {
                    progress
                        .lock()
                        .unwrap()
                        .push((id, phase.to_owned(), percent));
                    Ok(())
                }),
            )
            .await
            .unwrap();
        assert_eq!(results.len(), 1);
        assert_eq!(results[0].adam_id, 42);
        assert!(results[0].error.is_none() && results[0].privileged.is_none());
        assert_eq!(
            *seen.lock().unwrap(),
            vec![
                (42, "downloading".into(), expected),
                (42, "done".into(), Some(100.0))
            ],
            "raw={raw:?}"
        );
    }
}

#[tokio::test]
async fn runner_rejects_invalid_progress_before_publishing_or_finishing() {
    for raw in [
        "-1",
        "100.1",
        "100.000001",
        "1e39",
        "1e999",
        "-1e999",
        "NaN",
        "Infinity",
        "\"NaN\"",
        "\"37.5\"",
        "true",
        "[]",
        "{}",
    ] {
        let dir = tempfile::tempdir().unwrap();
        let stage = Stage::create(&dir.path().join("stages")).unwrap();
        let called = Arc::new(AtomicBool::new(false));
        let progress = called.clone();
        let error = jsonl_runner(dir.path(), Some(raw))
            .download(
                TaskOp::Install,
                &[42],
                &stage.path,
                Arc::new(AtomicBool::new(false)),
                Arc::new(move |_, _, _| {
                    progress.store(true, Ordering::Release);
                    Ok(())
                }),
            )
            .await
            .unwrap_err();
        assert_eq!(error.code, "E_MAS_ENGINE", "raw={raw}");
        assert!(
            !called.load(Ordering::Acquire),
            "published invalid progress {raw}"
        );
    }
}

#[tokio::test]
async fn signal_exit_preserves_completed_items_and_fails_unfinished_items() {
    let dir = tempfile::tempdir().unwrap();
    let stage = Stage::create(&dir.path().join("stages")).unwrap();
    let items = runner("partial_signal")
        .download(
            TaskOp::Install,
            &[42, 43],
            &stage.path,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_, _, _| Ok(())),
        )
        .await
        .unwrap();
    assert_eq!(items.len(), 2);
    assert_eq!(items[0].adam_id, 42);
    assert!(items[0].error.is_none() && items[0].privileged.is_none());
    assert_eq!(items[1].adam_id, 43);
    assert_eq!(
        items[1].error.as_ref().unwrap().message,
        "store_exit_failed"
    );
    assert!(items[1].privileged.is_none());
}

#[tokio::test]
async fn item_failure_preserves_other_privileged_requests_but_abnormal_exit_does_not() {
    for scenario in [
        "201_then_error",
        "201_then_error_signal",
        "201_then_incomplete",
    ] {
        let dir = tempfile::tempdir().unwrap();
        let stage = Stage::create(&dir.path().join("stages")).unwrap();
        let items = runner(scenario)
            .download(
                TaskOp::Install,
                &[42, 43],
                &stage.path,
                Arc::new(AtomicBool::new(false)),
                Arc::new(|_, _, _| Ok(())),
            )
            .await
            .unwrap();
        assert_eq!(items.len(), 2);
        assert_eq!(items[0].adam_id, 42);
        assert_eq!(items[1].adam_id, 43);
        if scenario == "201_then_error" {
            assert!(items[0].error.is_none());
            items[0]
                .privileged
                .as_ref()
                .unwrap()
                .validate(unsafe { nix::libc::getuid() })
                .unwrap();
        } else {
            assert!(items[0].privileged.is_none());
            assert_eq!(
                items[0].error.as_ref().unwrap().message,
                "store_exit_failed"
            );
        }
        if scenario == "201_then_incomplete" {
            assert_eq!(
                items[1].error.as_ref().unwrap().message,
                "store_exit_failed"
            );
        } else {
            assert_eq!(items[1].error.as_ref().unwrap().code, "E_MAS_NOT_OWNED");
        }
    }
}
