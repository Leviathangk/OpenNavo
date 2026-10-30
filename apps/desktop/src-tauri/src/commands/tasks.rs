use crate::{AppError, core::DesktopCore, model::*};
use std::{io::Write, path::Path, sync::Arc};
use tauri::State;

#[tauri::command]
#[specta::specta]
pub async fn task_enqueue(
    state: State<'_, Arc<DesktopCore>>,
    op: TaskOp,
    target: Option<TaskTarget>,
    options: TaskOptions,
    trigger: TaskTrigger,
) -> Result<Task, AppError> {
    // IPC callers cannot assert an external or unattended origin. Internal Rust callers use the queue directly.
    let _ = trigger;
    let trigger = TaskTrigger::Manual;
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
    let _ = trigger;
    let trigger = TaskTrigger::Manual;
    let queue = state.queue.clone();
    crate::blocking(move || queue.enqueue_many(op, targets, options, trigger)).await
}

#[cfg(test)]
mod tests {
    use super::*;
    use tauri::Manager;

    #[tokio::test]
    async fn generic_task_commands_always_persist_manual_origin() {
        let tmp = tempfile::tempdir().unwrap();
        let core = DesktopCore::open(
            tmp.path().join("data"),
            tmp.path().join("cache"),
            tmp.path().join("askpass"),
            Arc::new(|_| {}),
        )
        .unwrap();
        let app = tauri::test::mock_app();
        app.manage(core.clone());
        for (index, trigger) in [
            TaskTrigger::Manual,
            TaskTrigger::Deeplink,
            TaskTrigger::Mcp,
            TaskTrigger::Schedule,
            TaskTrigger::Retry,
            TaskTrigger::Bundle,
        ]
        .into_iter()
        .enumerate()
        {
            let target = |suffix| TaskTarget {
                kind: TaskKind::Cask,
                token: format!("fixture-{index}-{suffix}"),
            };
            let single = task_enqueue(
                app.state(),
                TaskOp::Install,
                Some(target("one")),
                TaskOptions::default(),
                trigger,
            )
            .await
            .unwrap();
            let batch = task_enqueue_many(
                app.state(),
                TaskOp::Install,
                vec![target("two"), target("three")],
                TaskOptions::default(),
                trigger,
            )
            .await
            .unwrap();
            for task in std::iter::once(single).chain(batch) {
                assert_eq!(task.trigger, TaskTrigger::Manual);
                assert_eq!(
                    core.database.task(&task.id).unwrap().unwrap().trigger,
                    TaskTrigger::Manual
                );
            }
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
