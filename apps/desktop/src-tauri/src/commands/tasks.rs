use crate::{AppError, core::DesktopCore, model::*};
use std::{io::Write, path::Path, sync::Arc};
use tauri::State;

fn validate_ipc_trigger(trigger: TaskTrigger) -> Result<(), AppError> {
    // App-internal origins retain the queue's retry, unattended-run and pause policies.
    // External origins must enter through Rust-owned paths that enforce native confirmation.
    if matches!(trigger, TaskTrigger::Deeplink | TaskTrigger::Mcp) {
        return Err(AppError::new(
            "E_INVALID_ARG",
            "external_trigger_requires_rust",
        ));
    }
    Ok(())
}

#[tauri::command]
#[specta::specta]
pub async fn task_enqueue(
    state: State<'_, Arc<DesktopCore>>,
    op: TaskOp,
    target: Option<TaskTarget>,
    options: TaskOptions,
    trigger: TaskTrigger,
) -> Result<Task, AppError> {
    validate_ipc_trigger(trigger)?;
    if op == TaskOp::Clean {
        return Err(AppError::new("E_INVALID_ARG", "cleanup_execute_required"));
    }
    let queue = state.queue.clone();
    crate::blocking(move || queue.enqueue(op, target, options, trigger)).await
}
#[tauri::command]
#[specta::specta]
pub async fn task_enqueue_many(
    state: State<'_, Arc<DesktopCore>>,
    op: TaskOp,
    targets: Vec<TaskTarget>,
    options: TaskOptions,
    trigger: TaskTrigger,
) -> Result<Vec<Task>, AppError> {
    validate_ipc_trigger(trigger)?;
    let queue = state.queue.clone();
    crate::blocking(move || queue.enqueue_many(op, targets, options, trigger)).await
}

#[cfg(test)]
mod tests {
    use super::*;
    use tauri::Manager;

    const TRIGGERS: [TaskTrigger; 6] = [
        TaskTrigger::Manual,
        TaskTrigger::Bundle,
        TaskTrigger::Retry,
        TaskTrigger::Schedule,
        TaskTrigger::Deeplink,
        TaskTrigger::Mcp,
    ];

    fn open_core(tmp: &Path) -> Arc<DesktopCore> {
        DesktopCore::open(
            tmp.join("data"),
            tmp.join("cache"),
            tmp.join("askpass"),
            Arc::new(|_| {}),
        )
        .unwrap()
    }

    async fn enqueue(
        app: &tauri::App<tauri::test::MockRuntime>,
        op: TaskOp,
        many: bool,
        trigger: TaskTrigger,
        options: TaskOptions,
    ) -> Result<Vec<Task>, AppError> {
        let targets = (0..if many { 2 } else { 1 })
            .map(|index| TaskTarget {
                kind: TaskKind::Cask,
                token: format!("fixture-{index}"),
            })
            .collect::<Vec<_>>();
        if many {
            task_enqueue_many(app.state(), op, targets, options, trigger).await
        } else {
            task_enqueue(
                app.state(),
                op,
                targets.into_iter().next(),
                options,
                trigger,
            )
            .await
            .map(|task| vec![task])
        }
    }

    async fn assert_trigger_policy(many: bool) {
        for trigger in TRIGGERS {
            let tmp = tempfile::tempdir().unwrap();
            let core = open_core(tmp.path());
            let mut parents = Vec::new();
            for index in 0..if many { 2 } else { 1 } {
                let mut parent = core
                    .queue
                    .enqueue(
                        TaskOp::Install,
                        Some(TaskTarget {
                            kind: TaskKind::Cask,
                            token: format!("fixture-{index}"),
                        }),
                        TaskOptions::default(),
                        TaskTrigger::Manual,
                    )
                    .unwrap();
                parent.state = TaskState::Failed;
                core.database.save_task(&parent).unwrap();
                parents.push(parent);
            }
            // Recover an unrelated pending task so enqueue must preserve or resume a paused queue.
            core.queue
                .enqueue(
                    TaskOp::Install,
                    Some(TaskTarget {
                        kind: TaskKind::Cask,
                        token: "pending-fixture".into(),
                    }),
                    TaskOptions::default(),
                    TaskTrigger::Manual,
                )
                .unwrap();
            drop(core);
            let core = open_core(tmp.path());
            assert!(core.queue.is_paused());
            let app = tauri::test::mock_app();
            app.manage(core.clone());
            let before = core.database.tasks().unwrap().len();
            let result =
                enqueue(&app, TaskOp::Install, many, trigger, TaskOptions::default()).await;
            if matches!(trigger, TaskTrigger::Deeplink | TaskTrigger::Mcp) {
                let error = result.unwrap_err();
                assert_eq!(error.code, "E_INVALID_ARG", "{trigger:?}");
                assert_eq!(error.message, "external_trigger_requires_rust");
                assert_eq!(core.database.tasks().unwrap().len(), before);
                assert!(core.queue.is_paused());
                continue;
            }
            let tasks = result.unwrap();
            assert_eq!(tasks.len(), parents.len());
            for (task, parent) in tasks.iter().zip(parents) {
                assert_eq!(task.trigger, trigger);
                assert_eq!(
                    core.database.task(&task.id).unwrap().unwrap().trigger,
                    trigger
                );
                assert_eq!(
                    core.database
                        .meta(&format!("task_retry_parent:{}", task.id))
                        .unwrap(),
                    (trigger == TaskTrigger::Retry).then_some(parent.id)
                );
            }
            assert_eq!(
                core.queue.is_paused(),
                matches!(trigger, TaskTrigger::Bundle | TaskTrigger::Schedule),
                "{trigger:?}"
            );
        }
    }

    #[tokio::test]
    async fn task_enqueue_preserves_internal_triggers_and_rejects_external_origins() {
        assert_trigger_policy(false).await;
    }

    #[tokio::test]
    async fn task_enqueue_many_preserves_internal_triggers_and_rejects_external_origins() {
        assert_trigger_policy(true).await;
    }

    #[tokio::test]
    async fn renderer_restore_replay_does_not_require_a_native_grant() {
        let tmp = tempfile::tempdir().unwrap();
        let core = open_core(tmp.path());
        // RestorePrompt replays tasks through the renderer's generic IPC. No confirmation service
        // or grant is installed here: that renderer trust is an accepted boundary of this PR.
        let app = tauri::test::mock_app();
        app.manage(core.clone());
        let tasks = enqueue(
            &app,
            TaskOp::Install,
            true,
            TaskTrigger::Manual,
            TaskOptions::default(),
        )
        .await
        .unwrap();
        assert_eq!(tasks.len(), 2);
        assert!(tasks.iter().all(|task| task.trigger == TaskTrigger::Manual));
        assert_eq!(core.database.tasks().unwrap().len(), 2);
    }

    #[tokio::test]
    async fn generic_task_commands_enforce_unattended_options() {
        let tmp = tempfile::tempdir().unwrap();
        let core = open_core(tmp.path());
        let app = tauri::test::mock_app();
        app.manage(core.clone());
        for many in [false, true] {
            for trigger in [TaskTrigger::Bundle, TaskTrigger::Schedule] {
                for options in [
                    TaskOptions {
                        quit_running: true,
                        ..TaskOptions::default()
                    },
                    TaskOptions {
                        quit_running: true,
                        reopen: true,
                        ..TaskOptions::default()
                    },
                ] {
                    let error = enqueue(&app, TaskOp::Upgrade, many, trigger, options)
                        .await
                        .unwrap_err();
                    assert_eq!(error.code, "E_INVALID_ARG");
                    assert_eq!(error.message, "unattended_quit");
                }
            }
            let error = enqueue(
                &app,
                TaskOp::Install,
                many,
                TaskTrigger::Schedule,
                TaskOptions {
                    adopt: true,
                    ..TaskOptions::default()
                },
            )
            .await
            .unwrap_err();
            assert_eq!(error.code, "E_INVALID_ARG");
            assert_eq!(error.message, "unattended_adopt");
            assert!(core.database.tasks().unwrap().is_empty());
        }
    }
}
#[tauri::command]
#[specta::specta]
pub async fn task_cancel(state: State<'_, Arc<DesktopCore>>, id: String) -> Result<(), AppError> {
    let queue = state.queue.clone();
    crate::blocking(move || queue.cancel(&id)).await
}
#[tauri::command]
#[specta::specta]
pub async fn task_list_active(state: State<'_, Arc<DesktopCore>>) -> Result<Vec<Task>, AppError> {
    let queue = state.queue.clone();
    crate::blocking(move || queue.active()).await
}
#[tauri::command]
#[specta::specta]
pub async fn task_log(
    state: State<'_, Arc<DesktopCore>>,
    id: String,
    tail: Option<u32>,
) -> Result<String, AppError> {
    let queue = state.queue.clone();
    crate::blocking(move || queue.log(&id, tail)).await
}
#[tauri::command]
#[specta::specta]
pub async fn history_list(
    state: State<'_, Arc<DesktopCore>>,
    query: HistoryQuery,
) -> Result<Page<Task>, AppError> {
    let database = state.database.clone();
    crate::blocking(move || database.history(&query)).await
}

#[tauri::command]
#[specta::specta]
pub async fn history_clear(state: State<'_, Arc<DesktopCore>>) -> Result<HistoryCleared, AppError> {
    let queue = state.queue.clone();
    crate::blocking(move || queue.clear_history()).await
}

fn cell(value: &str) -> String {
    let safe = if value.starts_with(['=', '+', '-', '@']) {
        format!("'{value}")
    } else {
        value.into()
    };
    format!("\"{}\"", safe.replace('"', "\"\""))
}
pub fn export_csv(database: &crate::db::Database, path: &Path) -> Result<u32, AppError> {
    if !path.is_absolute() {
        return Err(AppError::new("E_INVALID_ARG", "csv_path"));
    }
    let tasks: Vec<_> = database
        .tasks()?
        .into_iter()
        .filter(|t| {
            matches!(
                t.state,
                TaskState::Succeeded | TaskState::Failed | TaskState::Canceled
            )
        })
        .collect();
    let mut file = std::fs::File::create(path)?;
    file.write_all(
        b"\xef\xbb\xbfid,operation,kind,token,state,fromVersion,toVersion,finishedAt,errorCode\r\n",
    )?;
    for task in &tasks {
        let columns = [
            task.id.clone(),
            serde_json::to_value(task.op)?
                .as_str()
                .unwrap_or_default()
                .into(),
            task.target
                .as_ref()
                .map(|t| t.kind.as_str())
                .unwrap_or_default()
                .into(),
            task.target
                .as_ref()
                .map(|t| t.token.clone())
                .unwrap_or_default(),
            serde_json::to_value(task.state)?
                .as_str()
                .unwrap_or_default()
                .into(),
            task.from_version.clone().unwrap_or_default(),
            task.to_version.clone().unwrap_or_default(),
            task.finished_at.unwrap_or_default().to_string(),
            task.error
                .as_ref()
                .map(|e| e.code.clone())
                .unwrap_or_default(),
        ];
        writeln!(
            file,
            "{}\r",
            columns
                .iter()
                .map(|c| cell(c))
                .collect::<Vec<_>>()
                .join(",")
        )?;
    }
    file.sync_all()?;
    Ok(tasks.len() as u32)
}
#[tauri::command]
#[specta::specta]
pub async fn history_export_csv(
    state: State<'_, Arc<DesktopCore>>,
    path: String,
) -> Result<u32, AppError> {
    let database = state.database.clone();
    crate::blocking(move || export_csv(&database, Path::new(&path))).await
}

#[tauri::command]
#[specta::specta]
pub async fn apps_running(
    state: State<'_, Arc<DesktopCore>>,
    targets: Vec<TaskTarget>,
) -> Result<Vec<RunningApp>, AppError> {
    let queue = &state.queue;
    crate::running_apps::inspect(
        &queue.database,
        &queue.settings()?,
        queue.app_control.clone(),
        targets,
    )
    .await
}

#[tauri::command]
#[specta::specta]
pub async fn install_preflight(
    state: State<'_, Arc<DesktopCore>>,
    target: TaskTarget,
) -> Result<InstallPreflight, AppError> {
    let database = state.queue.database.clone();
    let token = target.token.clone();
    let item = if target.kind == Kind::Cask {
        crate::blocking(move || crate::catalog::get(&database, Kind::Cask, &token)).await?
    } else {
        None
    };
    crate::install_preflight::inspect_listed(&state.queue.settings()?, &target, item).await
}
