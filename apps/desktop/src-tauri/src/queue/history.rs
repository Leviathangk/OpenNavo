use super::*;
use std::collections::HashSet;

/// The finished records a clear leaves alone, because they are still the way back: uninstalls and clean-ups whose
/// Trash items can be put back, and updates that can still be rolled back.
fn kept(
    tasks: &[Task],
    undoable: impl Fn(&str) -> Result<bool, AppError>,
    rollbackable: impl Fn(&[String]) -> Result<Vec<String>, AppError>,
) -> Result<HashSet<String>, AppError> {
    let finished = || {
        tasks.iter().filter(|task| {
            matches!(
                task.state,
                TaskState::Succeeded | TaskState::Failed | TaskState::Canceled
            )
        })
    };
    let mut keep = HashSet::new();
    for task in finished() {
        // A partly finished uninstall can be undone as well, whatever state it ended in.
        if matches!(task.op, TaskOp::Uninstall | TaskOp::Clean)
            && task
                .outcome
                .as_ref()
                .is_some_and(|outcome| outcome.restorable)
            && undoable(&task.id)?
        {
            keep.insert(task.id.clone());
        }
    }
    let updates: Vec<String> = finished()
        .filter(|task| {
            task.op == TaskOp::Upgrade
                && task.state == TaskState::Succeeded
                && task
                    .target
                    .as_ref()
                    .is_some_and(|target| target.kind == TaskKind::App)
        })
        .map(|task| task.id.clone())
        .collect();
    // One call answers for at most 200 ids.
    for chunk in updates.chunks(200) {
        keep.extend(rollbackable(chunk)?);
    }
    Ok(keep)
}

impl TaskQueue {
    /// Deletes the finished records, except those that can still be acted on: an uninstall or clean-up that can be
    /// undone, an update that can be rolled back. Queued and running tasks are never touched.
    pub fn clear_history(&self) -> Result<HistoryCleared, AppError> {
        let tasks = self.database.tasks()?;
        let keep = kept(
            &tasks,
            |id| self.uninstall_engine.undoable(id),
            |ids| {
                // A rollback is resolved against the list of installed apps, which must have been read.
                self.uninstall_engine.list()?;
                self.rollback_targets(ids)
            },
        )?;
        let doomed: Vec<String> = tasks
            .iter()
            .filter(|task| {
                matches!(
                    task.state,
                    TaskState::Succeeded | TaskState::Failed | TaskState::Canceled
                ) && !keep.contains(&task.id)
            })
            .map(|task| task.id.clone())
            .collect();
        let finished = tasks
            .iter()
            .filter(|task| {
                matches!(
                    task.state,
                    TaskState::Succeeded | TaskState::Failed | TaskState::Canceled
                )
            })
            .count();
        let cleared = self.database.delete_finished(&doomed)?;
        Ok(HistoryCleared {
            cleared,
            kept: (finished - doomed.len()) as u32,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::cell::RefCell;

    fn task(
        id: &str,
        op: TaskOp,
        state: TaskState,
        kind: Option<TaskKind>,
        restorable: bool,
    ) -> Task {
        Task {
            target_name: None,
            outcome: Some(UninstallOutcome {
                freed_bytes: 0,
                trashed_bytes: 0,
                trashed: 0,
                deleted: 0,
                skipped: vec![],
                restorable,
            }),
            id: id.into(),
            op,
            target: kind.map(|kind| TaskTarget {
                kind,
                token: "fixture".into(),
            }),
            options: TaskOptions::default(),
            trigger: TaskTrigger::Manual,
            state,
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
            created_at: 1,
            started_at: None,
            finished_at: Some(2),
        }
    }

    #[test]
    fn undoable_runs_and_rollbackable_updates_are_kept_and_nothing_else() {
        let app = Some(TaskKind::App);
        let tasks = vec![
            // Still in the Trash: kept, also when the run failed half way.
            task(
                "uninstall-in-trash",
                TaskOp::Uninstall,
                TaskState::Succeeded,
                app,
                true,
            ),
            task(
                "clean-in-trash",
                TaskOp::Clean,
                TaskState::Failed,
                None,
                true,
            ),
            // The flag says restorable but the Trash no longer holds the items.
            task(
                "uninstall-emptied",
                TaskOp::Uninstall,
                TaskState::Succeeded,
                app,
                true,
            ),
            // Nothing was moved to the Trash.
            task(
                "uninstall-deleted",
                TaskOp::Uninstall,
                TaskState::Succeeded,
                app,
                false,
            ),
            // Only uninstalls and clean-ups have anything to put back.
            task(
                "leftovers",
                TaskOp::RemoveLeftovers,
                TaskState::Succeeded,
                app,
                true,
            ),
            task(
                "install",
                TaskOp::Install,
                TaskState::Succeeded,
                Some(TaskKind::Cask),
                true,
            ),
            // Updates: asked about once the app is a local app and the update succeeded.
            task(
                "update-rollbackable",
                TaskOp::Upgrade,
                TaskState::Succeeded,
                app,
                false,
            ),
            task(
                "update-spent",
                TaskOp::Upgrade,
                TaskState::Succeeded,
                app,
                false,
            ),
            task(
                "update-failed",
                TaskOp::Upgrade,
                TaskState::Failed,
                app,
                false,
            ),
            task(
                "update-cask",
                TaskOp::Upgrade,
                TaskState::Succeeded,
                Some(TaskKind::Cask),
                false,
            ),
            // Not finished: never part of the history.
            task(
                "uninstall-running",
                TaskOp::Uninstall,
                TaskState::Running,
                app,
                true,
            ),
        ];
        let asked_undo = RefCell::new(vec![]);
        let asked_rollback = RefCell::new(vec![]);
        let keep = kept(
            &tasks,
            |id| {
                asked_undo.borrow_mut().push(id.to_owned());
                Ok(id != "uninstall-emptied")
            },
            |ids| {
                asked_rollback.borrow_mut().extend(ids.iter().cloned());
                Ok(ids
                    .iter()
                    .filter(|id| *id == "update-rollbackable")
                    .cloned()
                    .collect())
            },
        )
        .unwrap();
        let mut keep: Vec<_> = keep.into_iter().collect();
        keep.sort();
        assert_eq!(
            keep,
            [
                "clean-in-trash",
                "uninstall-in-trash",
                "update-rollbackable"
            ]
        );
        // The Trash is examined only for runs that claim to be restorable.
        assert_eq!(
            *asked_undo.borrow(),
            ["uninstall-in-trash", "clean-in-trash", "uninstall-emptied"]
        );
        // Only succeeded updates of local apps are put to the rollback check.
        assert_eq!(
            *asked_rollback.borrow(),
            ["update-rollbackable", "update-spent"]
        );
    }

    #[test]
    fn rollback_questions_are_asked_200_ids_at_a_time() {
        let tasks: Vec<_> = (0..450)
            .map(|i| {
                task(
                    &format!("u{i}"),
                    TaskOp::Upgrade,
                    TaskState::Succeeded,
                    Some(TaskKind::App),
                    false,
                )
            })
            .collect();
        let sizes = RefCell::new(vec![]);
        let keep = kept(
            &tasks,
            |_| Ok(false),
            |ids| {
                sizes.borrow_mut().push(ids.len());
                Ok(ids.iter().step_by(100).cloned().collect())
            },
        )
        .unwrap();
        assert_eq!(*sizes.borrow(), [200, 200, 50]);
        assert_eq!(keep.len(), 2 + 2 + 1);
    }

    #[test]
    fn a_failing_question_stops_the_clear_before_anything_is_deleted() {
        let tasks = vec![task(
            "u",
            TaskOp::Upgrade,
            TaskState::Succeeded,
            Some(TaskKind::App),
            false,
        )];
        let error = kept(
            &tasks,
            |_| Ok(false),
            |_| Err(AppError::new("E_UNKNOWN", "app_list")),
        )
        .unwrap_err();
        assert_eq!(error.message, "app_list");
    }
}
