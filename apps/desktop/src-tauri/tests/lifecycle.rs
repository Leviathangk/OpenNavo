use opennavo_desktop_lib::{
    core::DesktopCore,
    lifecycle::{self, CloseAction, ExitDecision, Lifecycle},
    model::*,
    notifications::TaskNotices,
    scheduler::Notice,
};
use std::{
    os::unix::fs::PermissionsExt,
    path::Path,
    sync::{Arc, Mutex},
    time::Duration,
};

fn core(root: &Path, slow: bool) -> Arc<DesktopCore> {
    let fake = Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/fake-brew/brew");
    let path = root.join("brew_fixture");
    std::fs::write(&path, format!("#!/usr/bin/python3 -I\nimport os, sys\nos.environ['FAKE_BREW_SCENARIO']={}\nos.execv('/usr/bin/python3', ['/usr/bin/python3', '-I', {}] + sys.argv[1:])\n", serde_json::to_string(if slow { "slow" } else { "install_ok" }).unwrap(), serde_json::to_string(fake.to_str().unwrap()).unwrap())).unwrap();
    std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
    let core = DesktopCore::open(
        root.join("data"),
        root.join("cache"),
        root.join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    core.settings.write().unwrap().brew_path = Some(path.to_string_lossy().into());
    core
}
fn enqueue(core: &DesktopCore, token: &str) -> Task {
    core.queue
        .enqueue(
            TaskOp::Install,
            Some(TaskTarget {
                kind: Kind::Cask.into(),
                token: token.into(),
            }),
            TaskOptions::default(),
            TaskTrigger::Manual,
        )
        .unwrap()
}

#[test]
fn exit_confirmation_and_cleanup_are_idempotent() {
    let lifecycle = Lifecycle::default();
    assert!(!lifecycle.exit_ready());
    assert!(lifecycle.begin_request());
    assert!(!lifecycle.begin_request());
    let counts = QuitRequested {
        running: 1,
        queued: 2,
    };
    assert_eq!(
        lifecycle.inspected(counts.clone()),
        ExitDecision::Confirm(counts)
    );
    assert!(!lifecycle.exit_ready());
    // Canceling needs no new IPC; choosing to keep running still requires confirmation on the next quit.
    assert!(lifecycle.begin_request());
    assert!(lifecycle.confirm());
    assert_eq!(
        lifecycle.inspected(QuitRequested {
            running: 0,
            queued: 0
        }),
        ExitDecision::Pending
    );
    assert!(!lifecycle.confirm());
    assert!(!lifecycle.begin_request());
    lifecycle.ready();
    assert!(lifecycle.exit_ready());
    assert!(!lifecycle.confirm());
    let idle = Lifecycle::default();
    assert!(idle.begin_request());
    assert_eq!(
        idle.inspected(QuitRequested {
            running: 0,
            queued: 0
        }),
        ExitDecision::Cleanup
    );
}

#[test]
fn hidden_start_close_policy_and_window_restore_do_not_show_login_window() {
    assert!(lifecycle::hidden_launch(&[
        "OpenNavo".into(),
        "--hidden".into()
    ]));
    assert!(!lifecycle::hidden_launch(&[
        "OpenNavo".into(),
        "--hidden=1".into()
    ]));
    assert_eq!(lifecycle::LOGIN_ARG, "--hidden");
    use tauri_plugin_window_state::StateFlags;
    assert!(
        !lifecycle::window_state_flags(true)
            .intersects(StateFlags::VISIBLE | StateFlags::MAXIMIZED | StateFlags::FULLSCREEN)
    );
    assert!(lifecycle::window_state_flags(false).contains(StateFlags::SIZE | StateFlags::POSITION));
    assert!(!lifecycle::window_state_flags(false).contains(StateFlags::VISIBLE));
    assert_eq!(
        lifecycle::close_action(&Settings::default()),
        CloseAction::Hide
    );
    assert_eq!(
        lifecycle::close_action(&Settings {
            keep_in_menu_bar_on_close: false,
            ..Settings::default()
        }),
        CloseAction::RequestExit
    );
    let config: serde_json::Value =
        serde_json::from_str(include_str!("../tauri.conf.json")).unwrap();
    assert_eq!(config["app"]["windows"][0]["visible"], false);
}

#[tokio::test]
async fn confirmed_exit_interrupts_running_but_preserves_queued_for_paused_recovery() {
    let tmp = tempfile::tempdir().unwrap();
    let core = core(tmp.path(), true);
    let first = enqueue(&core, "ghostty");
    let second = enqueue(&core, "visual-studio-code");
    core.queue.start();
    tokio::time::timeout(Duration::from_secs(5), async {
        loop {
            if !core.queue.log(&first.id, None).unwrap().is_empty() {
                break;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .unwrap();
    assert_eq!(
        core.queue.prepare_exit().unwrap(),
        QuitRequested {
            running: 1,
            queued: 1
        }
    );
    assert!(!core.queue.is_stopping());
    assert!(
        lifecycle::interrupt_and_wait(core.queue.clone(), Duration::from_secs(3))
            .await
            .unwrap()
    );
    let tasks = core.database.tasks().unwrap();
    assert_eq!(
        tasks.iter().find(|t| t.id == first.id).unwrap().state,
        TaskState::Canceled
    );
    assert_eq!(
        tasks.iter().find(|t| t.id == second.id).unwrap().state,
        TaskState::Queued
    );
    assert!(
        core.queue
            .enqueue(
                TaskOp::Update,
                None,
                TaskOptions::default(),
                TaskTrigger::Schedule
            )
            .is_err()
    );
    let restarted = DesktopCore::open(
        tmp.path().join("data"),
        tmp.path().join("cache"),
        tmp.path().join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    assert!(restarted.queue.is_paused());
    assert_eq!(restarted.queue.active().unwrap().len(), 1);
    restarted.queue.shutdown();
}

#[tokio::test]
async fn idle_exit_atomically_rejects_new_work_and_login_changes_persist() {
    let tmp = tempfile::tempdir().unwrap();
    let core = core(tmp.path(), false);
    let calls = Arc::new(Mutex::new(Vec::new()));
    let received = calls.clone();
    let change: opennavo_desktop_lib::settings::Autostart = Arc::new(move |enabled| {
        received.lock().unwrap().push(enabled);
        Ok(())
    });
    let mut settings = core.current_settings().unwrap();
    settings.launch_at_login = true;
    core.replace_settings_with(settings.clone(), Some(change.clone()))
        .await
        .unwrap();
    core.replace_settings_with(settings.clone(), Some(change.clone()))
        .await
        .unwrap();
    settings.launch_at_login = false;
    core.replace_settings_with(settings, Some(change))
        .await
        .unwrap();
    assert_eq!(*calls.lock().unwrap(), [true, false]);
    assert_eq!(
        core.queue.prepare_exit().unwrap(),
        QuitRequested {
            running: 0,
            queued: 0
        }
    );
    assert!(core.queue.is_stopping());
    assert!(
        core.queue
            .enqueue(
                TaskOp::Install,
                Some(TaskTarget {
                    kind: Kind::Cask.into(),
                    token: "ghostty".into()
                }),
                TaskOptions::default(),
                TaskTrigger::Manual
            )
            .is_err()
    );
    assert!(core.database.tasks().unwrap().is_empty());
}

#[test]
fn hidden_manual_tasks_summarize_success_and_report_failures_individually() {
    let tmp = tempfile::tempdir().unwrap();
    let core = core(tmp.path(), false);
    let mut task = enqueue(&core, "ghostty");
    task.state = TaskState::Succeeded;
    let policy = TaskNotices::default();
    assert!(policy.completed(&task, true, false).unwrap().is_empty());
    assert!(policy.completed(&task, false, true).unwrap().is_empty());
    task.state = TaskState::Failed;
    assert_eq!(
        policy.completed(&task, false, true).unwrap(),
        [Notice::Failed {
            name: "ghostty".into()
        }]
    );
    task.state = TaskState::Succeeded;
    assert_eq!(
        policy.completed(&task, false, false).unwrap(),
        [Notice::Completed {
            succeeded: 2,
            failed: 0,
            canceled: 0
        }]
    );
    task.trigger = TaskTrigger::Schedule;
    assert!(policy.completed(&task, false, false).unwrap().is_empty());
    task.trigger = TaskTrigger::Manual;
    task.op = TaskOp::Update;
    assert!(policy.completed(&task, false, false).unwrap().is_empty());
    core.queue.shutdown();
}

#[test]
fn process_plugin_keeps_existing_ipc_and_rejects_restart_while_tasks_are_active() {
    let tmp = tempfile::tempdir().unwrap();
    let core = core(tmp.path(), false);
    let task = enqueue(&core, "ghostty");
    let app = tauri::test::mock_builder()
        .manage(core.clone())
        .manage(Arc::new(Lifecycle::default()))
        .plugin(tauri_plugin_process::init())
        .plugin(lifecycle::guarded_process())
        .build(tauri::generate_context!())
        .unwrap();
    let webview = tauri::WebviewWindowBuilder::new(&app, "main", Default::default())
        .build()
        .unwrap();
    for (command, body, error) in [
        ("restart", serde_json::json!({}), "active_tasks"),
        (
            "exit",
            serde_json::json!({ "code": tauri::RESTART_EXIT_CODE }),
            "invalid_exit_code",
        ),
    ] {
        let result = tauri::test::get_ipc_response(
            &webview,
            tauri::webview::InvokeRequest {
                cmd: format!("plugin:process|{command}"),
                callback: tauri::ipc::CallbackFn(0),
                error: tauri::ipc::CallbackFn(1),
                url: "tauri://localhost".parse().unwrap(),
                body: tauri::ipc::InvokeBody::Json(body),
                headers: Default::default(),
                invoke_key: tauri::test::INVOKE_KEY.into(),
            },
        );
        assert_eq!(result.err(), Some(serde_json::json!(error)));
    }
    assert!(!core.queue.is_stopping());
    assert_eq!(core.queue.active().unwrap().len(), 1);
    core.queue.cancel(&task.id).unwrap();
    assert!(
        tauri::async_runtime::block_on(lifecycle::prepare_restart(core.queue.clone())).unwrap()
    );
    assert!(core.queue.is_stopping());
    assert!(core.queue.active().unwrap().is_empty());
    assert!(
        core.queue
            .enqueue(
                TaskOp::Update,
                None,
                TaskOptions::default(),
                TaskTrigger::Schedule
            )
            .is_err()
    );
}
