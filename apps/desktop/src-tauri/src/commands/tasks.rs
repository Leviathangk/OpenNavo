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
    let queue = state.queue.clone();
    crate::blocking(move || queue.enqueue_many(op, targets, options, trigger)).await
}
#[tauri::command]
#[specta::specta]
pub async fn task_cancel(state: State<'_, Arc<DesktopCore>>, id: String) -> Result<(), AppError> {
    let queue = state.queue.clone();
    crate::blocking(move || queue.cancel(&id)).await
}
#[tauri::command]
#[specta::specta]
pub fn task_list_active(state: State<'_, Arc<DesktopCore>>) -> Result<Vec<Task>, AppError> {
    state.queue.active()
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
pub async fn history_clear(state: State<'_, Arc<DesktopCore>>) -> Result<u32, AppError> {
    let database = state.database.clone();
    crate::blocking(move || database.clear_history()).await
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
    crate::install_preflight::inspect(&state.queue.settings()?, &target).await
}
