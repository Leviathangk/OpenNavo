use crate::{AppError, core::DesktopCore, model::*};
use chrono::{DateTime, Days, Local, NaiveTime, TimeZone};
use std::{collections::HashSet, sync::Arc, time::Duration};

#[derive(Debug, Clone, PartialEq)]
pub enum Notice {
    Available {
        names: Vec<String>,
    },
    Completed {
        succeeded: u32,
        failed: u32,
        canceled: u32,
    },
    Failed {
        name: String,
    },
    TaskFinished(Box<Task>),
    Settings,
}
pub type NoticeSink = Arc<dyn Fn(Notice) + Send + Sync>;

pub fn next_check(settings: &Settings, checked: Option<i64>, now: i64) -> Option<i64> {
    next_check_in(settings, checked, Local.timestamp_millis_opt(now).single()?)
}
pub fn next_check_in<T: TimeZone>(
    settings: &Settings,
    checked: Option<i64>,
    now: DateTime<T>,
) -> Option<i64> {
    if !settings.auto_check {
        return None;
    }
    let time = NaiveTime::parse_from_str(&settings.check_time, "%H:%M").ok()?;
    let zone = now.timezone();
    let mut date = now.date_naive();
    if let Some(at) = checked.and_then(|at| zone.timestamp_millis_opt(at).single()) {
        if at.date_naive() >= date {
            date = date.checked_add_days(Days::new(1))?;
        } else if now.timestamp_millis().saturating_sub(at.timestamp_millis()) >= 86_400_000 {
            return Some(now.timestamp_millis());
        }
    }
    for _ in 0..3 {
        // Advance DST-skipped minutes to the next valid minute; use only the first occurrence of repeated minutes.
        let start = date.and_time(time);
        for offset in 0..=180 {
            let target = zone
                .from_local_datetime(&(start + chrono::Duration::minutes(offset)))
                .earliest();
            if let Some(target) = target {
                if checked.is_none_or(|at| at < target.timestamp_millis()) {
                    return Some(target.timestamp_millis());
                }
                break;
            }
        }
        date = date.checked_add_days(Days::new(1))?;
    }
    None
}

pub fn eligible(items: &[OutdatedItem], settings: &Settings) -> Vec<TaskTarget> {
    items
        .iter()
        .filter(|item| {
            item.source == UpdateSource::Homebrew
                && settings.update_sources.enabled(UpdateSource::Homebrew)
                && !item.pinned
                && !item.ignored
                && item.skipped_version.as_deref() != Some(item.current_version.as_str())
                && match item.kind {
                    Kind::Cask => settings.auto_upgrade_casks,
                    // Legacy settings may still be true, but the product no longer manages Formula packages.
                    Kind::Formula | Kind::Link => false,
                }
        })
        .map(|item| TaskTarget {
            kind: item.kind.into(),
            token: item.token.clone(),
        })
        .collect()
}
pub async fn tick(core: Arc<DesktopCore>, now: i64) -> Result<bool, AppError> {
    let Ok(_tick) = core.schedule_lock.try_lock() else {
        return Ok(false);
    };
    if core.queue.is_stopping() || core.queue.is_paused() {
        return Ok(false);
    }
    let status = core.updates_status_at(now)?;
    if status.checking || status.next_check_at.is_none_or(|at| at > now) {
        return Ok(false);
    }
    let settings = core.current_settings()?;
    let before: HashSet<_> = core
        .updates_list()?
        .into_iter()
        .map(|i| (i.source, i.app_key().unwrap_or_default(), i.current_version))
        .collect();
    let items = core
        .check_update_sources(None, TaskTrigger::Schedule, Some(now))
        .await?;
    if settings.notify_updates
        && items.iter().any(|i| {
            !before.contains(&(
                i.source,
                i.app_key().unwrap_or_default(),
                i.current_version.clone(),
            ))
        })
    {
        core.notice(Notice::Available {
            names: items.iter().map(|i| i.name.clone()).collect(),
        });
    }
    let targets = eligible(&items, &settings);
    if !targets.is_empty() {
        let queue = core.queue.clone();
        let tasks = crate::blocking(move || {
            queue.enqueue_many(
                TaskOp::Upgrade,
                targets,
                TaskOptions::default(),
                TaskTrigger::Schedule,
            )
        })
        .await?;
        let weak = Arc::downgrade(&core);
        tauri::async_runtime::spawn(async move {
            let mut succeeded = 0;
            let mut failed = 0;
            let mut canceled = 0;
            for task in tasks {
                let Some(core) = weak.upgrade() else {
                    return;
                };
                match core.queue.wait_terminal(&task.id).await {
                    Ok(task) => match task.state {
                        TaskState::Succeeded => succeeded += 1,
                        TaskState::Canceled => canceled += 1,
                        _ => failed += 1,
                    },
                    Err(_) => return,
                }
            }
            if let Some(core) = weak.upgrade()
                && !core.queue.is_stopping()
                && core.current_settings().is_ok_and(|s| s.notify_updates)
            {
                core.notice(Notice::Completed {
                    succeeded,
                    failed,
                    canceled,
                });
            }
        });
    }
    Ok(true)
}
pub fn start(core: &Arc<DesktopCore>) {
    let weak = Arc::downgrade(core);
    tauri::async_runtime::spawn(async move {
        loop {
            let Some(core) = weak.upgrade() else {
                break;
            };
            if core.queue.is_stopping() {
                break;
            }
            if let Err(error) = tick(core.clone(), chrono::Utc::now().timestamp_millis()).await {
                log::info!("Scheduled check temporarily unavailable: {}", error.code);
            }
            tokio::select! {_=tokio::time::sleep(Duration::from_secs(60))=>{},_=core.settings_changed.notified()=>{}}
        }
    });
}
