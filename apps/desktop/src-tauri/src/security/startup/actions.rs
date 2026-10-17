use super::*;
use crate::security::{
    admin::{Action, Request},
    quarantine::valid_ids,
};
use rusqlite::OptionalExtension;
impl Service {
    pub fn startup_act(
        self: &Arc<Self>,
        ids: Vec<String>,
        action: StartupItemAction,
    ) -> Result<ActOutcome, AppError> {
        valid_ids(&ids)?;
        let _guard = self.mutation.lock().map_err(poison)?;
        let mut outcome = ActOutcome::default();
        let mut pending = vec![];
        for id in ids {
            let result: Result<(), AppError> = (|| {
                let (mut item, snapshot): (StartupItem, Option<Snapshot>) =
                    self.database.with(|c| {
                        let row = c
                            .query_row(
                                "SELECT value,snapshot FROM security_startup WHERE id=?1",
                                [&id],
                                |r| Ok((r.get::<_, String>(0)?, r.get::<_, Option<String>>(1)?)),
                            )
                            .optional()?
                            .ok_or_else(|| AppError::new("E_NOT_FOUND", "startup_item"))?;
                        Ok((
                            serde_json::from_str(&row.0)?,
                            row.1.map(|s| serde_json::from_str(&s)).transpose()?,
                        ))
                    })?;
                let path = PathBuf::from(&item.path);
                if action == StartupItemAction::MarkReviewed {
                    item.is_new = false;
                    self.save_startup_item(&item)?;
                    self.events_handled(&id, true)?;
                    outcome.done.push(id.clone());
                    return Ok(());
                }
                if action == StartupItemAction::Reveal {
                    self.commands.run("/usr/bin/open", &["-R", &item.path])?;
                    outcome.done.push(id.clone());
                    return Ok(());
                }
                if action == StartupItemAction::OpenSettings {
                    let url = settings_url(item.category, macos_major()).ok_or_else(|| {
                        AppError::new("E_INVALID_ARG", "startup_settings_not_supported")
                    })?;
                    self.commands.run("/usr/bin/open", &[url])?;
                    outcome.done.push(id.clone());
                    return Ok(());
                }
                if item.trust == StartupTrust::Apple
                    || path.starts_with("/System")
                    || item
                        .label
                        .as_deref()
                        .is_some_and(|s| s.starts_with("com.apple."))
                {
                    return Err(AppError::new("E_STARTUP_PROTECTED", "startup_apple_item"));
                }
                if action == StartupItemAction::Scan {
                    self.scan_start(
                        AvScanKind::Custom,
                        Some(vec![item.program.clone().unwrap_or(item.path.clone())]),
                    )?;
                    outcome.done.push(id.clone());
                    return Ok(());
                }
                if matches!(
                    item.category,
                    StartupCategory::Cron | StartupCategory::LoginItems
                ) {
                    if item.category == StartupCategory::LoginItems {
                        files::same_file(
                            snapshot
                                .as_ref()
                                .ok_or_else(|| AppError::new("E_PROTECTED", "startup_snapshot"))?,
                            &path,
                        )?;
                    }
                    self.legacy_act(&mut item, action)?;
                    outcome.done.push(id.clone());
                    return Ok(());
                }
                if action == StartupItemAction::Restore {
                    return Err(AppError::new(
                        "E_INVALID_ARG",
                        "startup_restore_use_quarantine",
                    ));
                }
                // A ServiceManagement job: launchctl switches it by label once BTM confirms it
                // is still the job the scan saw. It stops starting from the next login or boot;
                // the running copy is left alone. Daemons need the administrator.
                if item.category == StartupCategory::Btm
                    && matches!(
                        action,
                        StartupItemAction::Enable | StartupItemAction::Disable
                    )
                {
                    let identifier = item
                        .label
                        .clone()
                        .ok_or_else(|| AppError::new("E_PROTECTED", "startup_label"))?;
                    let (label, system) = service_label(Some(&identifier)).ok_or_else(|| {
                        AppError::new("E_STARTUP_PROTECTED", "startup_service_label")
                    })?;
                    let label = label.to_owned();
                    registered_service(
                        &self.startup_roots(),
                        &identifier,
                        Some(&self.own_data()?),
                    )?;
                    let enable = action == StartupItemAction::Enable;
                    if system {
                        let request = Request {
                            id: id.clone(),
                            action: Action::LaunchdService { identifier, enable },
                        };
                        pending.push((item, Some(request), None));
                        return Ok(());
                    }
                    self.commands.run(
                        "/bin/launchctl",
                        &[
                            if enable { "enable" } else { "disable" },
                            &format!("{}/{label}", identity::user_domain()),
                        ],
                    )?;
                    self.startup_state(&mut item, action)?;
                    outcome.done.push(id.clone());
                    return Ok(());
                }
                if item.category != StartupCategory::LaunchItems {
                    return Err(AppError::new(
                        "E_STARTUP_PROTECTED",
                        "startup_use_system_settings",
                    ));
                }
                let snapshot =
                    snapshot.ok_or_else(|| AppError::new("E_PROTECTED", "startup_snapshot"))?;
                files::same_file(&snapshot, &path)?;
                let label = item
                    .label
                    .as_deref()
                    .ok_or_else(|| AppError::new("E_PROTECTED", "startup_label"))?;
                if item.requires_admin {
                    let request = Request {
                        id: id.clone(),
                        action: if action == StartupItemAction::Enable {
                            Action::LaunchdEnableSystem {
                                path: path.clone(),
                                snapshot: snapshot.clone(),
                                label: label.into(),
                            }
                        } else {
                            Action::LaunchdDisableSystem {
                                path: path.clone(),
                                snapshot: snapshot.clone(),
                                label: label.into(),
                            }
                        },
                    };
                    let quarantine = if action == StartupItemAction::Remove {
                        Some(self.prepare_quarantine(
                            &path,
                            &snapshot,
                            QuarantineReason::Startup,
                            Some(item.clone()),
                            None,
                        )?)
                    } else {
                        None
                    };
                    pending.push((item, Some(request), quarantine));
                } else {
                    admin::launch_action(
                        self.commands.as_ref(),
                        &path,
                        &snapshot,
                        label,
                        &identity::user_domain(),
                        action == StartupItemAction::Enable,
                        Some(&self.own_data()?),
                    )?;
                    if action == StartupItemAction::Remove {
                        let (entry, meta, request) = self.prepare_quarantine(
                            &path,
                            &snapshot,
                            QuarantineReason::Startup,
                            Some(item.clone()),
                            None,
                        )?;
                        if meta.system {
                            // A user-domain agent can still be owned by root. Disabling it
                            // uses the user's launchd domain; moving/chmod needs the helper.
                            pending.push((item, None, Some((entry, meta, request))));
                            return Ok(());
                        }
                        self.move_user(&request)?;
                        self.finish_quarantine(&entry, meta)?;
                        self.removed_startup(&item)?;
                    } else {
                        self.startup_state(&mut item, action)?;
                    }
                    outcome.done.push(id.clone());
                }
                Ok(())
            })();
            if let Err(e) = result {
                outcome.failed.push(ActFailure { id, code: e.code });
            }
        }
        // Disable and quarantine of a system item share one helper invocation.
        // The helper processes in order; no second authentication prompt.
        let requests: Vec<_> = pending
            .iter()
            .flat_map(|(_, r, q)| {
                r.iter()
                    .cloned()
                    .chain(q.as_ref().map(|(_, _, r)| r.clone()))
            })
            .collect();
        let results = self.admin_run(&requests);
        for (mut item, launch, quarantine) in pending {
            let result: Result<(), AppError> = (|| {
                let result_for = |id: &str| -> Result<(), AppError> {
                    match &results {
                        Ok(v) => {
                            let result = v.iter().find(|r| r.id == id).ok_or_else(|| {
                                AppError::new("E_ADMIN_CANCELED", "admin_result_missing")
                            })?;
                            if let Some(e) = &result.error {
                                Err(e.clone())
                            } else {
                                Ok(())
                            }
                        }
                        Err(e) => Err(e.clone()),
                    }
                };
                if launch.is_some() {
                    result_for(&item.id)?;
                }
                if let Some((entry, meta, _)) = quarantine {
                    result_for(&entry.id)?;
                    self.finish_quarantine(&entry, meta)?;
                    self.removed_startup(&item)?;
                } else {
                    self.startup_state(&mut item, action)?;
                }
                Ok(())
            })();
            match result {
                Ok(()) => outcome.done.push(item.id),
                Err(e) => outcome.failed.push(ActFailure {
                    id: item.id,
                    code: e.code,
                }),
            }
        }
        outcome.restorable = action == StartupItemAction::Remove && !outcome.done.is_empty();
        self.changed(SecurityChangeReason::Startup);
        Ok(outcome)
    }
    /// Puts an app in the user's login items so it opens at login, and lists it at once the
    /// way the next scan will: its own row, not reported as a new startup item.
    pub fn startup_login_add(&self, path: PathBuf) -> Result<StartupItem, AppError> {
        let _guard = self.mutation.lock().map_err(poison)?;
        let app: PathBuf = path.components().collect();
        let own_data = self.own_data()?;
        if !app.is_absolute()
            || app.extension().is_none_or(|e| e != "app")
            || !files::safe_metadata(&app).is_ok_and(|m| m.is_dir())
            || own_data.check(&app).is_err()
        {
            return Err(AppError::new("E_INVALID_ARG", "startup_login_app"));
        }
        self.commands
            .login_restore(&legacy_startup::LoginItem {
                name: app
                    .file_stem()
                    .unwrap_or_default()
                    .to_string_lossy()
                    .into_owned(),
                path: app.to_string_lossy().into_owned(),
                hidden: false,
            })
            .map_err(|e| {
                if e.code == "E_RESTORE_CONFLICT" {
                    AppError::new("E_LOGIN_ITEM_EXISTS", "startup_login_exists")
                } else {
                    e
                }
            })?;
        self.added_logins
            .lock()
            .map_err(poison)?
            .insert(app.canonicalize().unwrap_or_else(|_| app.clone()));
        let mut item = login_item(
            &self.startup_roots(),
            self.commands.as_ref(),
            Some(&own_data),
            &app,
        )
        .ok_or_else(|| AppError::new("E_STARTUP_CHANGED", "startup_login_added"))?;
        item.first_seen_at = now();
        self.owner_icons(std::slice::from_mut(&mut item));
        let snapshot = Snapshot::capture(&app)
            .ok()
            .map(|s| serde_json::to_string(&s))
            .transpose()?;
        self.database.with(|c| {
            c.execute(
                "INSERT INTO security_startup(id,value,snapshot) VALUES(?1,?2,?3) ON CONFLICT(id) DO UPDATE SET value=excluded.value,snapshot=excluded.snapshot",
                params![item.id, serde_json::to_string(&item)?, snapshot],
            )?;
            // Switched off here before and now back on: that backup is stale.
            c.execute(
                "DELETE FROM meta WHERE key=?1",
                [&legacy_startup::backup_key(&item.id)],
            )?;
            Ok(())
        })?;
        self.changed(SecurityChangeReason::Startup);
        Ok(item)
    }
    fn save_startup_item(&self, item: &StartupItem) -> Result<(), AppError> {
        self.database.with(|c| {
            c.execute(
                "UPDATE security_startup SET value=?2 WHERE id=?1",
                params![item.id, serde_json::to_string(item)?],
            )?;
            Ok(())
        })
    }
    fn startup_state(
        &self,
        item: &mut StartupItem,
        action: StartupItemAction,
    ) -> Result<(), AppError> {
        item.state = if action == StartupItemAction::Enable {
            StartupState::Enabled
        } else {
            StartupState::Disabled
        };
        item.actions
            .retain(|a| !matches!(a, StartupAction::Enable | StartupAction::Disable));
        item.actions.insert(
            0,
            if action == StartupItemAction::Enable {
                StartupAction::Disable
            } else {
                StartupAction::Enable
            },
        );
        self.save_startup_item(item)
    }
    fn removed_startup(&self, item: &StartupItem) -> Result<(), AppError> {
        self.database.with(|c| {
            c.execute("DELETE FROM security_startup WHERE id=?1", [&item.id])?;
            Ok(())
        })?;
        self.record_event(
            ProtectionEventKind::StartupRemoved,
            &item.path,
            Some(item.id.clone()),
            true,
        )
    }
}
