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

/// Whether the updates page lists this row at all. The mirror of `shown()` in
/// `apps/desktop/src/stores/updates.ts`, which is the product rule behind it: the catalog is
/// cask-only (ADR-018), so a Homebrew formula or link is never listed, while a pinned, ignored,
/// skipped or non-auto-upgraded row still is.
///
/// This is deliberately not `eligible()`. That one answers "may the scheduler upgrade this on its
/// own" and therefore also drops pinned, ignored, skipped and non-auto-upgraded rows. A notification
/// says what appeared on the page, so filtering it by `eligible()` would leave a row visible and
/// silent, which is the same page-says-one-thing/notice-says-another defect this module is being
/// fixed for, only mirrored.
pub fn visible(item: &OutdatedItem) -> bool {
    item.source != UpdateSource::Homebrew || item.kind == Kind::Cask
}

/// One row's identity across two checks: the source, the app, and the version on offer. `app_key()`
/// fails only for a non-Homebrew row without a local app id, which `check_sources` already drops, so
/// such a row has no stable identity to compare and is left out rather than collapsed to a shared
/// empty key.
fn identity(item: &OutdatedItem) -> Option<(UpdateSource, String, String)> {
    item.app_key()
        .ok()
        .map(|key| (item.source, key, item.current_version.clone()))
}

/// The rows the user can now see that the previous list did not have: the only thing a scheduled
/// check's "updates available" notice may describe.
fn newly_visible<'a>(
    items: &'a [OutdatedItem],
    before: &HashSet<(UpdateSource, String, String)>,
) -> Vec<&'a OutdatedItem> {
    items
        .iter()
        .filter(|item| visible(item))
        .filter(|item| identity(item).is_some_and(|key| !before.contains(&key)))
        .collect()
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
        .filter_map(|i| identity(&i))
        .collect();
    let items = core
        .check_update_sources(None, TaskTrigger::Schedule, Some(now))
        .await?;
    if settings.notify_updates {
        // The count and the names both come from the same new rows, so the notice describes what
        // appeared rather than the whole outdated list: ten known updates plus one new one said
        // "eleven" before, because the diff only decided whether to notify.
        let added = newly_visible(&items, &before);
        if !added.is_empty() {
            core.notice(Notice::Available {
                names: added.iter().map(|i| i.name.clone()).collect(),
            });
        }
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

#[cfg(test)]
mod tests {
    use super::*;

    fn item(source: UpdateSource, kind: Kind, token: &str) -> OutdatedItem {
        OutdatedItem {
            source,
            app_id: (source != UpdateSource::Homebrew).then(|| format!("{token}-id")),
            bundle_id: None,
            channel: None,
            major: false,
            notes: None,
            integrity: None,
            requires_admin: false,
            running: false,
            relaunch_pending: false,
            skipped_version: None,
            critical_update: false,
            informational_url: None,
            download_host: None,
            unsigned_update: None,
            expected_team_match: None,
            kind,
            token: token.into(),
            name: token.into(),
            installed_version: "1".into(),
            current_version: "2".into(),
            auto_updates: false,
            pinned: false,
            ignored: false,
            dependents: 0,
            download_size: None,
        }
    }

    /// The rows the page lists. Formula and link are never listed (cask-only catalog), every other
    /// state the page still shows, which is what separates this from `eligible()`.
    #[test]
    fn visible_is_the_pages_rule_not_the_schedulers() {
        assert!(!visible(&item(
            UpdateSource::Homebrew,
            Kind::Formula,
            "node"
        )));
        assert!(!visible(&item(UpdateSource::Homebrew, Kind::Link, "node")));
        assert!(visible(&item(
            UpdateSource::Homebrew,
            Kind::Cask,
            "ghostty"
        )));
        assert!(visible(&item(UpdateSource::Mas, Kind::Cask, "mas-app")));
        // States `eligible()` drops but the page still shows. A notice that used `eligible()`
        // would leave each of these visible and silent.
        let mut pinned = item(UpdateSource::Homebrew, Kind::Cask, "pinned");
        pinned.pinned = true;
        let mut ignored = item(UpdateSource::Homebrew, Kind::Cask, "ignored");
        ignored.ignored = true;
        let mut skipped = item(UpdateSource::Homebrew, Kind::Cask, "skipped");
        skipped.skipped_version = Some(skipped.current_version.clone());
        for (label, row) in [
            ("pinned", pinned),
            ("ignored", ignored),
            ("skipped", skipped),
        ] {
            assert!(
                visible(&row),
                "{label} still appears on the page, so a notice must be able to name it"
            );
        }
    }

    /// The notice describes what appeared, not the whole list. Ten known rows plus one new one used
    /// to say eleven.
    #[test]
    fn the_notice_names_only_the_rows_that_appeared() {
        let known = item(UpdateSource::Homebrew, Kind::Cask, "known");
        let fresh = item(UpdateSource::Homebrew, Kind::Cask, "fresh");
        let before: HashSet<_> = [identity(&known).unwrap()].into_iter().collect();
        let items = vec![known, fresh];
        let added = newly_visible(&items, &before);
        assert_eq!(
            added.iter().map(|i| i.name.as_str()).collect::<Vec<_>>(),
            ["fresh"],
            "one new row out of two is one name, not the whole list"
        );
    }

    /// A new Homebrew formula must stay silent: the user cannot see it on the page, so a notice
    /// naming it would report something the app does not show.
    #[test]
    fn a_new_formula_alone_does_not_notify() {
        let items = vec![item(UpdateSource::Homebrew, Kind::Formula, "node")];
        assert!(newly_visible(&items, &HashSet::new()).is_empty());
    }

    /// A version bump on a row already listed counts as new: the page shows a different version.
    #[test]
    fn a_new_version_of_a_known_row_is_new() {
        let old = item(UpdateSource::Homebrew, Kind::Cask, "ghostty");
        let before: HashSet<_> = [identity(&old).unwrap()].into_iter().collect();
        let mut bumped = item(UpdateSource::Homebrew, Kind::Cask, "ghostty");
        bumped.current_version = "3".into();
        assert_eq!(
            newly_visible(&[bumped], &before)
                .iter()
                .map(|i| i.current_version.as_str())
                .collect::<Vec<_>>(),
            ["3"]
        );
    }

    /// The same app, the same version on offer, but a different source: the page now says the
    /// update comes from somewhere else, so it is a row the user has not seen before and the
    /// notice must name it. Without the source in the identity the two rows compare equal and the
    /// notice stays silent about a change the page shows.
    #[test]
    fn a_source_switch_announces_the_row_again() {
        let sparkle = item(UpdateSource::Sparkle, Kind::Cask, "editor");
        let github = item(UpdateSource::Github, Kind::Cask, "editor");
        // Same app, same version: only the source differs, which is the whole point.
        assert_eq!(sparkle.app_id, github.app_id);
        assert_eq!(sparkle.current_version, github.current_version);
        assert!(visible(&sparkle) && visible(&github));

        let before: HashSet<_> = [identity(&sparkle).unwrap()].into_iter().collect();
        let switched = [github.clone()];
        let added = newly_visible(&switched, &before);
        assert_eq!(added.len(), 1, "a source switch is a new row on the page");
        assert_eq!(added[0].source, UpdateSource::Github);

        // And the reverse direction too: the identity is not anchored on one source.
        let back: HashSet<_> = [identity(&github).unwrap()].into_iter().collect();
        let switched_back = [sparkle];
        assert_eq!(newly_visible(&switched_back, &back).len(), 1);
    }

    /// A row whose update is skipped at the version on offer still appears on the page, but the
    /// scheduler must not upgrade it on its own. `eligible()` is the only place that decides that,
    /// and this pins the skip arm of the decision.
    #[test]
    fn a_version_the_user_skipped_is_not_upgraded_by_the_scheduler() {
        let mut settings = Settings {
            auto_upgrade_casks: true,
            ..Settings::default()
        };
        settings
            .update_sources
            .0
            .insert(UpdateSource::Homebrew, true);
        let mut skipped = item(UpdateSource::Homebrew, Kind::Cask, "skipped");
        skipped.skipped_version = Some(skipped.current_version.clone());
        let other = item(UpdateSource::Homebrew, Kind::Cask, "other");
        // Still listed by the page: this is about the scheduler, not about visibility.
        assert!(visible(&skipped));
        assert_eq!(
            eligible(&[skipped, other], &settings)
                .iter()
                .map(|t| t.token.as_str())
                .collect::<Vec<_>>(),
            ["other"],
            "the skipped version is not queued, every other row still is"
        );
    }

    /// A skip recorded for some *other* version must not block this one: the page offers a version
    /// the user never skipped. Deleting the arm would not catch this, only over-broadening it.
    #[test]
    fn a_skip_of_an_older_version_does_not_block_a_newer_one() {
        let mut settings = Settings {
            auto_upgrade_casks: true,
            ..Settings::default()
        };
        settings
            .update_sources
            .0
            .insert(UpdateSource::Homebrew, true);
        let mut earlier = item(UpdateSource::Homebrew, Kind::Cask, "ghostty");
        earlier.current_version = "3".into();
        earlier.skipped_version = Some("2".into());
        assert_eq!(
            eligible(&[earlier], &settings)
                .iter()
                .map(|t| t.token.as_str())
                .collect::<Vec<_>>(),
            ["ghostty"],
            "a skip of 2 does not skip 3"
        );
    }

    /// A non-Homebrew row without an app id has no identity to compare, so it is left out instead of
    /// collapsing onto a shared empty key that would make every such row look like every other.
    #[test]
    fn a_row_without_an_identity_is_not_reported() {
        let mut orphan = item(UpdateSource::Mas, Kind::Cask, "orphan");
        orphan.app_id = None;
        assert!(orphan.app_key().is_err());
        assert!(newly_visible(&[orphan], &HashSet::new()).is_empty());
    }

    /// `eligible` is unchanged: it still refuses what the scheduler must not upgrade on its own.
    #[test]
    fn eligible_still_refuses_what_the_scheduler_must_not_touch() {
        let mut settings = Settings {
            auto_upgrade_casks: true,
            ..Settings::default()
        };
        settings
            .update_sources
            .0
            .insert(UpdateSource::Homebrew, true);
        let mut pinned = item(UpdateSource::Homebrew, Kind::Cask, "pinned");
        pinned.pinned = true;
        let mut ignored = item(UpdateSource::Homebrew, Kind::Cask, "ignored");
        ignored.ignored = true;
        let items = vec![
            pinned,
            ignored,
            item(UpdateSource::Homebrew, Kind::Formula, "node"),
            item(UpdateSource::Homebrew, Kind::Cask, "ok"),
        ];
        assert_eq!(
            eligible(&items, &settings)
                .iter()
                .map(|t| t.token.as_str())
                .collect::<Vec<_>>(),
            ["ok"]
        );
    }
}
