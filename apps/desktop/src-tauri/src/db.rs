use crate::{AppError, model::*};
use rusqlite::{Connection, OptionalExtension, Row, params};
use std::{
    path::Path,
    sync::{Arc, Mutex},
};

/// Clones share one serialized SQLite connection, not a connection pool.
/// SQLite alone owns the database/WAL/SHM lifecycle: never open these files for
/// raw reads, truncate, rename or unlink them while the connection is alive.
/// Even a raw read's close() can drop this process's POSIX locks. For the path,
/// inspect Connection::path() under with(), without opening another file.
#[derive(Clone)]
pub struct Database(Arc<Mutex<Connection>>);

/// Meta keys that store a resolved install plan are `<prefix><task id>`. The queue writes and
/// reads them, and task deletion must remove them together with their task, so the prefix lives
/// here where the cleanup SQL can reference it.
pub const TASK_INSTALL_PLAN_PREFIX: &str = "task_install_plan:";
impl Database {
    pub fn open(path: &Path) -> Result<Self, AppError> {
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)?;
        }
        let mut connection = Connection::open(path)?;
        connection.busy_timeout(std::time::Duration::from_secs(5))?;
        connection.execute_batch("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;")?;
        let exists: bool = connection.query_row(
            "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='meta')",
            [],
            |r| r.get(0),
        )?;
        let version = if exists {
            connection
                .query_row(
                    "SELECT value FROM meta WHERE key='schema_version'",
                    [],
                    |r| r.get::<_, String>(0),
                )
                .optional()?
                .and_then(|v| v.parse::<u32>().ok())
                .unwrap_or(0)
        } else {
            0
        };
        if version > 7 {
            return Err(AppError::new("E_UNKNOWN", "newer_database_schema"));
        }
        if version == 0 {
            let transaction = connection.transaction()?;
            transaction.execute_batch(include_str!("../migrations/001_initial.sql"))?;
            transaction.execute(
                "INSERT INTO meta(key,value) VALUES('schema_version','1')",
                [],
            )?;
            transaction.commit()?;
        }
        if version < 2 {
            let tx = connection.transaction()?;
            tx.execute_batch(include_str!("../migrations/002_catalog_texts.sql"))?;
            tx.execute("INSERT INTO meta(key,value) VALUES('schema_version','2') ON CONFLICT(key) DO UPDATE SET value='2'", [])?;
            tx.commit()?;
        }
        if version < 3 {
            let tx = connection.transaction()?;
            tx.execute_batch(include_str!("../migrations/003_uninstall.sql"))?;
            tx.execute("INSERT INTO meta(key,value) VALUES('schema_version','3') ON CONFLICT(key) DO UPDATE SET value='3'", [])?;
            tx.commit()?;
        }
        if version < 4 {
            let tx = connection.transaction()?;
            tx.execute_batch(include_str!("../migrations/004_cleanup.sql"))?;
            tx.execute("INSERT INTO meta(key,value) VALUES('schema_version','4') ON CONFLICT(key) DO UPDATE SET value='4'", [])?;
            tx.commit()?;
        }
        if version < 5 {
            let tx = connection.transaction()?;
            tx.execute_batch(include_str!("../migrations/005_links.sql"))?;
            tx.execute("INSERT INTO meta(key,value) VALUES('schema_version','5') ON CONFLICT(key) DO UPDATE SET value='5'", [])?;
            tx.commit()?;
        }
        if version < 6 {
            let tx = connection.transaction()?;
            tx.execute_batch(include_str!("../migrations/006_update_prefs.sql"))?;
            tx.execute("INSERT INTO meta(key,value) VALUES('schema_version','6') ON CONFLICT(key) DO UPDATE SET value='6'", [])?;
            tx.commit()?;
        }
        if version < 7 {
            let tx = connection.transaction()?;
            tx.execute_batch(include_str!("../migrations/007_security.sql"))?;
            tx.execute("INSERT INTO meta(key,value) VALUES('schema_version','7') ON CONFLICT(key) DO UPDATE SET value='7'", [])?;
            tx.commit()?;
        }
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(path, std::fs::Permissions::from_mode(0o600))?;
        }
        Ok(Self(Arc::new(Mutex::new(connection))))
    }
    pub fn with<T>(
        &self,
        f: impl FnOnce(&mut Connection) -> Result<T, AppError>,
    ) -> Result<T, AppError> {
        let mut connection = self
            .0
            .lock()
            .map_err(|_| AppError::new("E_UNKNOWN", "database_poisoned"))?;
        // Do not replay this closure on BUSY/PROTOCOL: callers may perform writes
        // or other side effects. SQLite's busy handler already waits for locks;
        // a returned database error must remain a recoverable AppError.
        f(&mut connection)
    }
    pub fn meta(&self, key: &str) -> Result<Option<String>, AppError> {
        self.with(|c| {
            Ok(
                c.query_row("SELECT value FROM meta WHERE key=?1", [key], |r| r.get(0))
                    .optional()?,
            )
        })
    }
    pub fn set_meta(&self, key: &str, value: &str) -> Result<(), AppError> {
        self.with(|c|{c.execute("INSERT INTO meta(key,value) VALUES(?1,?2) ON CONFLICT(key) DO UPDATE SET value=excluded.value",params![key,value])?;Ok(())})
    }
    pub fn save_task(&self, task: &Task) -> Result<(), AppError> {
        self.with(|c|{
        c.execute("INSERT INTO tasks(id,op,kind,token,options,trigger,state,from_version,to_version,error_code,error_message,exit_code,log_path,created_at,started_at,finished_at,target_name,outcome) VALUES(?1,?2,?3,?4,?5,?6,?7,?8,?9,?10,?11,?12,?13,?14,?15,?16,?17,?18) ON CONFLICT(id) DO UPDATE SET state=excluded.state,from_version=excluded.from_version,to_version=excluded.to_version,error_code=excluded.error_code,error_message=excluded.error_message,exit_code=excluded.exit_code,log_path=excluded.log_path,started_at=excluded.started_at,finished_at=excluded.finished_at,target_name=excluded.target_name,outcome=excluded.outcome",params![task.id,enum_text(task.op)?,task.target.as_ref().map(|t|t.kind.as_str()),task.target.as_ref().map(|t|t.token.as_str()),serde_json::to_string(&task.options)?,enum_text(task.trigger)?,enum_text(task.state)?,task.from_version,task.to_version,task.error.as_ref().map(|e|&e.code),task.error.as_ref().map(|e|&e.message),task.exit_code,task.log_path,task.created_at,task.started_at,task.finished_at,task.target_name,task.outcome.as_ref().map(serde_json::to_string).transpose()?])?;Ok(())
    })
    }
    pub fn tasks(&self) -> Result<Vec<Task>, AppError> {
        self.with(|c| {
            let mut statement = c.prepare("SELECT * FROM tasks ORDER BY created_at,id")?;
            Ok(statement
                .query_map([], task_row)?
                .collect::<rusqlite::Result<Vec<_>>>()?)
        })
    }
    pub fn task(&self, id: &str) -> Result<Option<Task>, AppError> {
        self.with(|c| {
            Ok(
                c.query_row("SELECT * FROM tasks WHERE id=?1", [id], task_row)
                    .optional()?,
            )
        })
    }
    pub fn recover_tasks(&self, now: i64) -> Result<Vec<Task>, AppError> {
        let mut queued = Vec::new();
        for mut task in self.tasks()? {
            if task.state == TaskState::Running {
                task.state = TaskState::Failed;
                task.error = Some(AppError::new("E_INTERRUPTED", "application_restarted"));
                task.finished_at = Some(now);
                self.save_task(&task)?;
            } else if task.state == TaskState::Queued && task.op == TaskOp::Clean {
                // Cleanup reports exist only in memory and cannot be resumed after restart.
                task.state = TaskState::Failed;
                task.error = Some(AppError::new("E_PLAN_EXPIRED", "cleanup_report_expired"));
                task.finished_at = Some(now);
                self.save_task(&task)?;
            } else if task.state == TaskState::Queued {
                task.options.quit_running = false;
                task.options.reopen = false;
                queued.push(task);
            }
        }
        Ok(queued)
    }
    /// Deletes these finished records. The caller read the history once and chose them, so a task that finishes in the
    /// meantime is never touched, and neither is one that is queued or running.
    pub fn delete_finished(&self, ids: &[String]) -> Result<u32, AppError> {
        self.with(|c| {
            let tx = c.transaction()?;
            let mut deleted = 0;
            for chunk in ids.chunks(500) {
                let marks = vec!["?"; chunk.len()].join(",");
                // Remove plan metadata first, and only for rows the task delete below really
                // removes: a queued or running task must keep its plan.
                tx.execute(
                    &format!(
                        "DELETE FROM meta WHERE key IN (SELECT ?1 || id FROM tasks WHERE state IN ('succeeded','failed','canceled') AND id IN ({marks}))"
                    ),
                    rusqlite::params_from_iter(
                        std::iter::once(TASK_INSTALL_PLAN_PREFIX)
                            .chain(chunk.iter().map(String::as_str)),
                    ),
                )?;
                deleted += tx.execute(
                    &format!(
                        "DELETE FROM tasks WHERE state IN ('succeeded','failed','canceled') AND id IN ({marks})"
                    ),
                    rusqlite::params_from_iter(chunk),
                )? as u32;
            }
            tx.commit()?;
            Ok(deleted)
        })
    }
    pub fn history(&self, query: &HistoryQuery) -> Result<Page<Task>, AppError> {
        if query.limit > 200
            || query.q.as_ref().is_some_and(|q| q.chars().count() > 128)
            || query
                .states
                .iter()
                .any(|s| matches!(s, TaskState::Running | TaskState::Queued))
            || query.from.zip(query.to).is_some_and(|(a, b)| a > b)
        {
            return Err(AppError::new("E_INVALID_ARG", "history_query"));
        }
        if let Some(target) = &query.target {
            if target.kind == TaskKind::Cask {
                crate::brew::args::split_cask_token(&target.token)?;
            } else {
                crate::brew::args::validate_token(&target.token)?;
            }
        }
        let q = query.q.as_ref().map(|q| q.to_lowercase());
        let matching_names: std::collections::HashSet<(String, String)> = if let Some(q) = &q {
            let escaped = q
                .replace('\\', "\\\\")
                .replace('%', "\\%")
                .replace('_', "\\_");
            self.with(|connection|{
                let mut statement=connection.prepare("SELECT kind,token FROM catalog_items WHERE lower(name) LIKE '%'||?1||'%' ESCAPE '\\' OR EXISTS(SELECT 1 FROM catalog_texts n WHERE n.kind=catalog_items.kind AND n.token=catalog_items.token AND lower(n.display_name) LIKE '%'||?1||'%' ESCAPE '\\')")?;
                Ok(statement.query_map([escaped],|row|Ok((row.get(0)?,row.get(1)?)))?.collect::<rusqlite::Result<_>>()?)
            })?
        } else {
            Default::default()
        };
        let mut tasks: Vec<_> = self
            .tasks()?
            .into_iter()
            .filter(|task| {
                let finished = task.finished_at.unwrap_or(0);
                matches!(
                    task.state,
                    TaskState::Succeeded | TaskState::Failed | TaskState::Canceled
                ) && (query.ops.is_empty() || query.ops.contains(&task.op))
                    && (query.states.is_empty() || query.states.contains(&task.state))
                    && query
                        .target
                        .as_ref()
                        .is_none_or(|target| task.target.as_ref() == Some(target))
                    && (query.include_scheduled_updates
                        || task.op != TaskOp::Update
                        || task.trigger != TaskTrigger::Schedule)
                    && query.from.is_none_or(|from| finished >= from)
                    && query.to.is_none_or(|to| finished <= to)
                    && q.as_ref().is_none_or(|q| {
                        task.target_name
                            .as_ref()
                            .is_some_and(|name| name.to_lowercase().contains(q))
                            || task.target.as_ref().is_some_and(|t| {
                                t.token.to_lowercase().contains(q)
                                    || matching_names
                                        .contains(&(t.kind.as_str().to_owned(), t.token.clone()))
                            })
                    })
            })
            .collect();
        tasks.sort_by(|a, b| {
            b.finished_at
                .cmp(&a.finished_at)
                .then_with(|| b.id.cmp(&a.id))
        });
        let total = tasks.len() as u32;
        let items = tasks
            .into_iter()
            .skip(query.offset as usize)
            .take(query.limit as usize)
            .collect();
        Ok(Page { total, items })
    }
    pub fn prune(&self, now: i64) -> Result<(), AppError> {
        let day = 86_400_000;
        let log_cutoff = now - 90 * day;
        let task_cutoff = now - 730 * day;
        for mut task in self.tasks()? {
            if task.finished_at.is_some_and(|at| at < log_cutoff)
                && let Some(path) = task.log_path.take()
            {
                match std::fs::remove_file(path) {
                    Ok(()) => {}
                    Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
                    Err(e) => return Err(e.into()),
                }
                self.save_task(&task)?;
            }
        }
        self.with(|c| {
            let tx = c.transaction()?;
            // Plan metadata must go before the task rows, while the subquery can still find them.
            tx.execute(
                "DELETE FROM meta WHERE key IN (SELECT ?1 || id FROM tasks WHERE finished_at < ?2)",
                params![TASK_INSTALL_PLAN_PREFIX, task_cutoff],
            )?;
            tx.execute("DELETE FROM tasks WHERE finished_at < ?1", [task_cutoff])?;
            tx.commit()?;
            Ok(())
        })
    }
}
fn enum_text<T: serde::Serialize>(value: T) -> Result<String, AppError> {
    serde_json::to_value(value)?
        .as_str()
        .map(str::to_owned)
        .ok_or_else(|| AppError::new("E_UNKNOWN", "enum_serialization"))
}
fn task_row(row: &Row<'_>) -> rusqlite::Result<Task> {
    fn parse<T: serde::de::DeserializeOwned>(text: String) -> rusqlite::Result<T> {
        serde_json::from_str(&text).map_err(|e| {
            rusqlite::Error::FromSqlConversionFailure(0, rusqlite::types::Type::Text, Box::new(e))
        })
    }
    fn variant<T: serde::de::DeserializeOwned>(row: &Row<'_>, key: &str) -> rusqlite::Result<T> {
        parse(serde_json::Value::String(row.get(key)?).to_string())
    }
    let kind: Option<String> = row.get("kind")?;
    let token: Option<String> = row.get("token")?;
    let target = match (kind, token) {
        (Some(kind), Some(token)) => Some(TaskTarget {
            kind: parse(serde_json::Value::String(kind).to_string())?,
            token,
        }),
        _ => None,
    };
    let code: Option<String> = row.get("error_code")?;
    Ok(Task {
        target_name: row.get("target_name")?,
        outcome: row
            .get::<_, Option<String>>("outcome")?
            .map(parse)
            .transpose()?,
        id: row.get("id")?,
        op: variant(row, "op")?,
        target,
        options: parse(row.get("options")?)?,
        trigger: variant(row, "trigger")?,
        state: variant(row, "state")?,
        phase: None,
        percent: None,
        bytes_done: None,
        bytes_total: None,
        speed_bps: None,
        step_index: None,
        step_count: None,
        from_version: row.get("from_version")?,
        to_version: row.get("to_version")?,
        error: code.map(|code| AppError {
            code,
            message: row.get("error_message").unwrap_or_default(),
            detail: None,
        }),
        exit_code: row.get("exit_code")?,
        log_path: row.get("log_path")?,
        created_at: row.get("created_at")?,
        started_at: row.get("started_at")?,
        finished_at: row.get("finished_at")?,
    })
}
