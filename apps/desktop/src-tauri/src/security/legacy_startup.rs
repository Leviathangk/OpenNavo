// SPDX-License-Identifier: GPL-3.0-only
//! Current-user, fixed operations only. Cron contents are data, never a shell script.
use super::*;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    io::{Read, Write},
    process::{Command, Stdio},
    time::{Duration, Instant},
};

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct LoginItem {
    pub name: String,
    pub path: String,
    pub hidden: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub(super) enum Undo {
    CronJournal {
        epoch: String,
        occurrence: usize,
    },
    Cron {
        before: Option<String>,
        after: String,
    },
    Login {
        entry: LoginItem,
    },
}

pub(crate) fn fingerprint(text: &str, index: usize) -> String {
    format!("{}:{index}", digest(text))
}
pub(super) fn digest(text: &str) -> String {
    format!("{:x}", Sha256::digest(text))
}
pub(super) fn backup_key(id: &str) -> String {
    format!("security_s2_startup_undo:{id}")
}

impl Service {
    /// Cron lines are removed and restored. A login item is also switched: off takes it out
    /// of the login items list with a backup, on puts that entry back, or adds the app when
    /// it was never in the list (it had registered itself, then been switched off).
    pub(crate) fn legacy_act(
        &self,
        item: &mut StartupItem,
        action: StartupItemAction,
    ) -> Result<(), AppError> {
        let switch = matches!(
            action,
            StartupItemAction::Enable | StartupItemAction::Disable
        );
        if item.scope != StartupScope::User
            || !switch
                && !matches!(
                    action,
                    StartupItemAction::Remove | StartupItemAction::Restore
                )
            || switch && item.category != StartupCategory::LoginItems
        {
            return Err(AppError::new("E_STARTUP_PROTECTED", "startup_action"));
        }
        if item.category == StartupCategory::Cron {
            return self.cron_act(item, action);
        }
        let key = backup_key(&item.id);
        if matches!(
            action,
            StartupItemAction::Restore | StartupItemAction::Enable
        ) {
            let off = match action {
                StartupItemAction::Restore => item.state == StartupState::Removed,
                _ => matches!(item.state, StartupState::Disabled | StartupState::Removed),
            };
            if !off {
                return Err(AppError::new("E_INVALID_ARG", "startup_not_removed"));
            }
            match self.s2_get::<Undo>(&key)? {
                Some(Undo::Login { entry }) => self.commands.login_restore(&entry)?,
                Some(Undo::Cron { .. } | Undo::CronJournal { .. }) => {
                    return Err(AppError::new(
                        "E_STARTUP_CHANGED",
                        "startup_backup_category",
                    ));
                }
                None if action == StartupItemAction::Enable => {
                    self.commands.login_restore(&LoginItem {
                        name: Path::new(&item.path)
                            .file_stem()
                            .unwrap_or_default()
                            .to_string_lossy()
                            .into_owned(),
                        path: item.path.clone(),
                        hidden: false,
                    })?
                }
                None => return Err(AppError::new("E_NOT_FOUND", "startup_backup")),
            }
            item.state = StartupState::Enabled;
            item.actions = if action == StartupItemAction::Enable {
                startup::switch_actions(StartupCategory::LoginItems, true)
            } else {
                vec![StartupAction::Remove, StartupAction::Reveal]
            };
            item.is_new = false;
            self.database.with(|c| {
                c.execute("DELETE FROM meta WHERE key=?1", [&key])?;
                Ok(())
            })?;
        } else {
            if matches!(item.state, StartupState::Removed | StartupState::Disabled) {
                return Err(AppError::new("E_INVALID_ARG", "startup_already_removed"));
            }
            match item.category {
                StartupCategory::LoginItems => {
                    let entry = self.commands.login_read(&item.path)?;
                    self.s2_set(
                        &key,
                        &Undo::Login {
                            entry: entry.clone(),
                        },
                    )?;
                    self.commands.login_remove(&entry)?;
                }
                _ => return Err(AppError::new("E_STARTUP_PROTECTED", "startup_category")),
            }
            item.is_new = false;
            if action == StartupItemAction::Disable {
                item.state = StartupState::Disabled;
                item.actions = startup::switch_actions(StartupCategory::LoginItems, false);
            } else {
                item.state = StartupState::Removed;
                item.actions = vec![StartupAction::Restore];
                self.record_event(
                    ProtectionEventKind::StartupRemoved,
                    &item.path,
                    Some(item.id.clone()),
                    true,
                )?;
            }
        }
        self.persist_legacy_item(item)
    }
    fn persist_legacy_item(&self, item: &StartupItem) -> Result<(), AppError> {
        self.database.with(|c| {
            c.execute(
                "UPDATE security_startup SET value=?2 WHERE id=?1",
                params![item.id, serde_json::to_string(item)?],
            )?;
            Ok(())
        })
    }
}

// No caller-supplied code, shell, username or command flags. The timeout applies
// to automation approval too. stdout/stderr are bounded and never logged.
fn native(
    program: &str,
    args: &[&str],
    input: Option<&str>,
) -> Result<(i32, String, String), AppError> {
    let mut child = Command::new(program)
        .args(args)
        .env("LC_ALL", "C")
        .stdin(if input.is_some() {
            Stdio::piped()
        } else {
            Stdio::null()
        })
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()?;
    let out = child.stdout.take().unwrap();
    let err = child.stderr.take().unwrap();
    let read = |pipe: Box<dyn Read + Send>| {
        std::thread::spawn(move || {
            let mut b = vec![];
            pipe.take(1024 * 1024 + 1).read_to_end(&mut b).map(|_| b)
        })
    };
    let out = read(Box::new(out));
    let err = read(Box::new(err));
    let writer = input.map(|s| {
        let value = s.as_bytes().to_vec();
        let mut pipe = child.stdin.take().unwrap();
        std::thread::spawn(move || pipe.write_all(&value))
    });
    let start = Instant::now();
    let status = loop {
        match child.try_wait() {
            Ok(Some(s)) => break s,
            Ok(None) if start.elapsed() < Duration::from_secs(60) => {
                std::thread::sleep(Duration::from_millis(20))
            }
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                return Err(AppError::new("E_UNKNOWN", "startup_command_timeout"));
            }
        }
    };
    if let Some(writer) = writer {
        writer
            .join()
            .map_err(|_| AppError::new("E_UNKNOWN", "cron_writer"))??;
    }
    let out = out
        .join()
        .map_err(|_| AppError::new("E_UNKNOWN", "startup_stdout"))??;
    let err = err
        .join()
        .map_err(|_| AppError::new("E_UNKNOWN", "startup_stderr"))??;
    if out.len() > 1024 * 1024 || err.len() > 1024 * 1024 {
        return Err(AppError::new("E_PROTECTED", "startup_output_limit"));
    }
    Ok((
        status.code().unwrap_or(-1),
        String::from_utf8_lossy(&out).into(),
        String::from_utf8_lossy(&err).into(),
    ))
}
pub(crate) fn crontab_read() -> Result<Option<String>, AppError> {
    let (status, text, err) = native("/usr/bin/crontab", &["-l"], None)?;
    if status == 0 {
        Ok(Some(text))
    } else if status == 1 && err.contains("no crontab for ") {
        Ok(None)
    } else {
        Err(AppError::new("E_STARTUP_PROTECTED", "cron_read"))
    }
}
pub(crate) fn crontab_write(text: &str) -> Result<(), AppError> {
    if text.len() > 1024 * 1024 || text.contains('\0') {
        return Err(AppError::new("E_INVALID_ARG", "cron_size"));
    }
    let (status, _, _) = native("/usr/bin/crontab", &["-"], Some(text))?;
    if status == 0 {
        Ok(())
    } else {
        Err(AppError::new("E_STARTUP_PROTECTED", "cron_write"))
    }
}
const LOGIN_SCRIPT: &str = r#"function run(argv) {
 const action=argv[0], expected=JSON.parse(argv[1]);
 const se=Application('System Events');
 const matches=se.loginItems().filter(x=>x.path()===expected.path);
 if(action==='restore') {
   if(matches.length) throw Error('E_RESTORE_CONFLICT');
   se.loginItems.push(se.LoginItem({name:expected.name,path:expected.path,hidden:expected.hidden}));
   return '{}';
 }
 if(matches.length!==1) throw Error('E_STARTUP_CHANGED');
 const item=matches[0], actual={name:item.name(),path:item.path(),hidden:item.hidden()};
 if(action==='remove') {
   if(actual.name!==expected.name || actual.hidden!==expected.hidden) throw Error('E_STARTUP_CHANGED');
   se.delete(item);
 }
 return JSON.stringify(actual);
}"#;
pub(crate) fn login(action: &str, item: &LoginItem) -> Result<LoginItem, AppError> {
    if !matches!(action, "read" | "remove" | "restore") || !Path::new(&item.path).is_absolute() {
        return Err(AppError::new("E_INVALID_ARG", "login_action"));
    }
    let json = serde_json::to_string(item)?;
    let (status, out, err) = native(
        "/usr/bin/osascript",
        &["-l", "JavaScript", "-e", LOGIN_SCRIPT, "--", action, &json],
        None,
    )?;
    if status != 0 {
        let code = if err.contains("-1743") || err.contains("not authorized") {
            "E_AUTOMATION_DENIED"
        } else if err.contains("E_RESTORE_CONFLICT") {
            "E_RESTORE_CONFLICT"
        } else {
            "E_STARTUP_CHANGED"
        };
        return Err(AppError::new(code, "legacy_login_item"));
    }
    if action == "restore" {
        Ok(item.clone())
    } else {
        serde_json::from_str(out.trim()).map_err(Into::into)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[derive(Default)]
    struct Fake {
        cron: Mutex<Option<String>>,
        logins: Mutex<Vec<LoginItem>>,
        denied: AtomicBool,
        fail_cron: AtomicBool,
        fail_after_write: AtomicBool,
        change_on_read: std::sync::atomic::AtomicUsize,
        fail_on_read: std::sync::atomic::AtomicUsize,
        writes: std::sync::atomic::AtomicUsize,
    }
    impl Commands for Fake {
        fn run(&self, _: &str, _: &[&str]) -> Result<String, AppError> {
            Ok(String::new())
        }
        fn crontab_read(&self) -> Result<Option<String>, AppError> {
            if self
                .fail_on_read
                .fetch_update(Ordering::SeqCst, Ordering::SeqCst, |v| v.checked_sub(1))
                == Ok(1)
            {
                return Err(AppError::new("E_STARTUP_PROTECTED", "fixture_read"));
            }
            if self
                .change_on_read
                .fetch_update(Ordering::SeqCst, Ordering::SeqCst, |v| v.checked_sub(1))
                == Ok(1)
            {
                self.cron
                    .lock()
                    .unwrap()
                    .as_mut()
                    .unwrap()
                    .push_str("# external\n");
            }
            Ok(self.cron.lock().unwrap().clone())
        }
        fn crontab_write(&self, text: &str) -> Result<(), AppError> {
            self.writes.fetch_add(1, Ordering::SeqCst);
            if self.fail_cron.load(Ordering::SeqCst) {
                return Err(AppError::new("E_STARTUP_PROTECTED", "fixture_write"));
            }
            *self.cron.lock().unwrap() = Some(text.into());
            if self.fail_after_write.load(Ordering::SeqCst) {
                return Err(AppError::new("E_UNKNOWN", "fixture_after_write"));
            }
            Ok(())
        }
        fn login_items(&self) -> Result<Vec<PathBuf>, AppError> {
            Ok(self
                .logins
                .lock()
                .unwrap()
                .iter()
                .map(|i| PathBuf::from(&i.path))
                .collect())
        }
        fn login_read(&self, path: &str) -> Result<LoginItem, AppError> {
            if self.denied.load(Ordering::Relaxed) {
                return Err(AppError::new("E_AUTOMATION_DENIED", "fixture"));
            }
            self.logins
                .lock()
                .unwrap()
                .iter()
                .find(|i| i.path == path)
                .cloned()
                .ok_or_else(|| AppError::new("E_STARTUP_CHANGED", "missing"))
        }
        fn login_remove(&self, entry: &LoginItem) -> Result<(), AppError> {
            let mut entries = self.logins.lock().unwrap();
            let i = entries
                .iter()
                .position(|i| i == entry)
                .ok_or_else(|| AppError::new("E_STARTUP_CHANGED", "changed"))?;
            entries.remove(i);
            Ok(())
        }
        fn login_restore(&self, entry: &LoginItem) -> Result<(), AppError> {
            if self
                .logins
                .lock()
                .unwrap()
                .iter()
                .any(|i| i.path == entry.path)
            {
                return Err(AppError::new("E_RESTORE_CONFLICT", "exists"));
            }
            self.logins.lock().unwrap().push(entry.clone());
            Ok(())
        }
    }
    #[test]
    fn cron_removes_only_selected_line_preserves_environment_and_rejects_external_edits() {
        let (_tmp, mut s, _) = super::super::tests::fixture();
        let fake = Arc::new(Fake::default());
        Arc::get_mut(&mut s).unwrap().commands = fake.clone();
        let original = "# comment\nSHELL=/bin/sh\n0 1 * * * printf first\nPATH=/usr/bin\n0 1 * * * printf second\n";
        *fake.cron.lock().unwrap() = Some(original.into());
        let roots = startup::Roots {
            home: s.home.clone(),
            root: s.home.clone(),
            applications: vec![],
        };
        let scan = s.startup_scan_at(&roots).unwrap();
        let item = scan
            .items
            .iter()
            .find(|i| i.name.ends_with("first"))
            .unwrap();
        let outcome = s
            .startup_act(vec![item.id.clone()], StartupItemAction::Remove)
            .unwrap();
        assert_eq!(outcome.done, vec![item.id.clone()]);
        assert!(outcome.restorable);
        assert_eq!(
            fake.crontab_read().unwrap().unwrap(),
            original.replace("0 1 * * * printf first\n", "")
        );
        assert!(
            s.startup_scan_at(&roots)
                .unwrap()
                .items
                .iter()
                .any(|i| i.id == item.id && i.state == StartupState::Removed)
        );
        let outcome = s
            .startup_act(vec![item.id.clone()], StartupItemAction::Restore)
            .unwrap();
        assert_eq!(outcome.done.len(), 1);
        assert_eq!(fake.crontab_read().unwrap().unwrap(), original);
        let scan = s.startup_scan_at(&roots).unwrap();
        let item = scan
            .items
            .iter()
            .find(|i| i.name.ends_with("first") && i.state != StartupState::Removed)
            .unwrap();
        *fake.cron.lock().unwrap() = Some(format!("{original}# external\n"));
        let outcome = s
            .startup_act(vec![item.id.clone()], StartupItemAction::Remove)
            .unwrap();
        assert_eq!(outcome.failed[0].code, "E_STARTUP_CHANGED");
    }
    #[test]
    fn login_automation_denial_remove_restore_and_conflicts_use_only_fixed_entries() {
        let (_tmp, mut s, _) = super::super::tests::fixture();
        let fake = Arc::new(Fake::default());
        Arc::get_mut(&mut s).unwrap().commands = fake.clone();
        let path = s.home.join("fixture.app");
        std::fs::create_dir(&path).unwrap();
        let login = LoginItem {
            name: "Fixture ' quoted".into(),
            path: path.to_string_lossy().into(),
            hidden: true,
        };
        fake.logins.lock().unwrap().push(login.clone());
        let roots = startup::Roots {
            home: s.home.clone(),
            root: s.home.clone(),
            applications: vec![],
        };
        let scan = s.startup_scan_at(&roots).unwrap();
        let id = scan
            .items
            .iter()
            .find(|i| i.category == StartupCategory::LoginItems)
            .unwrap()
            .id
            .clone();
        fake.denied.store(true, Ordering::Relaxed);
        assert_eq!(
            s.startup_act(vec![id.clone()], StartupItemAction::Remove)
                .unwrap()
                .failed[0]
                .code,
            "E_AUTOMATION_DENIED"
        );
        assert_eq!(fake.logins.lock().unwrap().len(), 1);
        fake.denied.store(false, Ordering::Relaxed);
        assert_eq!(
            s.startup_act(vec![id.clone()], StartupItemAction::Remove)
                .unwrap()
                .done
                .len(),
            1
        );
        assert!(fake.logins.lock().unwrap().is_empty());
        assert_eq!(
            s.startup_act(vec![id.clone()], StartupItemAction::Restore)
                .unwrap()
                .done
                .len(),
            1
        );
        assert_eq!(*fake.logins.lock().unwrap(), vec![login.clone()]);
        s.startup_act(vec![id.clone()], StartupItemAction::Remove)
            .unwrap();
        fake.logins.lock().unwrap().push(login);
        assert_eq!(
            s.startup_act(vec![id], StartupItemAction::Restore)
                .unwrap()
                .failed[0]
                .code,
            "E_RESTORE_CONFLICT"
        );
    }
    #[test]
    fn an_app_added_to_open_at_login_is_listed_at_once_and_never_reported_as_new() {
        let (_tmp, mut s, _) = super::super::tests::fixture();
        let fake = Arc::new(Fake::default());
        Arc::get_mut(&mut s).unwrap().commands = fake.clone();
        let app = s.home.join("Applications/Added.app");
        std::fs::create_dir_all(app.join("Contents/MacOS")).unwrap();
        std::fs::write(app.join("Contents/Info.plist"), r#"<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.added</string><key>CFBundleExecutable</key><string>Added</string></dict></plist>"#).unwrap();
        std::fs::write(app.join("Contents/MacOS/Added"), "fixture").unwrap();
        let roots = startup::Roots {
            home: s.home.clone(),
            root: s.home.clone(),
            applications: vec![],
        };
        assert!(s.startup_cached().unwrap().is_none());
        s.startup_scan_at(&roots).unwrap();
        let item = s.startup_login_add(app.clone()).unwrap();
        assert_eq!(item.category, StartupCategory::LoginItems);
        assert_eq!(item.state, StartupState::Enabled);
        assert!(item.actions.contains(&StartupAction::Disable));
        assert!(!item.is_new);
        assert_eq!(
            item.owner.as_ref().unwrap().path,
            app.to_string_lossy().as_ref()
        );
        assert_eq!(
            *fake.logins.lock().unwrap(),
            vec![LoginItem {
                name: "Added".into(),
                path: app.to_string_lossy().into(),
                hidden: false,
            }]
        );
        // On the page at once, and still the same quiet row after the next scan.
        let cached = s.startup_cached().unwrap().unwrap();
        assert!(cached.items.iter().any(|i| i.id == item.id));
        let scanned = s.startup_scan_at(&roots).unwrap();
        let again = scanned.items.iter().find(|i| i.id == item.id).unwrap();
        assert!(!again.is_new);
        assert_eq!(
            s.startup_login_add(app).unwrap_err().code,
            "E_LOGIN_ITEM_EXISTS"
        );
        let note = s.home.join("note.txt");
        std::fs::write(&note, "not an app").unwrap();
        assert_eq!(s.startup_login_add(note).unwrap_err().code, "E_INVALID_ARG");
    }
    #[test]
    fn open_at_login_switches_off_with_a_backup_and_back_on_even_when_btm_lags() {
        let (_tmp, mut s, _) = super::super::tests::fixture();
        let fake = Arc::new(Fake::default());
        Arc::get_mut(&mut s).unwrap().commands = fake.clone();
        let app = s.home.join("Applications/Fixture.app");
        std::fs::create_dir_all(app.join("Contents/MacOS")).unwrap();
        std::fs::write(app.join("Contents/Info.plist"), r#"<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleExecutable</key><string>Fixture</string></dict></plist>"#).unwrap();
        std::fs::write(app.join("Contents/MacOS/Fixture"), "fixture").unwrap();
        let store = s
            .home
            .join("private/var/db/com.apple.backgroundtaskmanagement");
        std::fs::create_dir_all(&store).unwrap();
        let registered = |disposition: u64| {
            let bytes = startup::btm::test_archive(vec![vec![
                ("type", plist::Value::Integer(2.into())),
                (
                    "identifier",
                    plist::Value::String("2.com.example.fixture".into()),
                ),
                ("name", plist::Value::String("Fixture".into())),
                (
                    "url",
                    plist::Value::String(reqwest::Url::from_file_path(&app).unwrap().to_string()),
                ),
                ("disposition", plist::Value::Integer(disposition.into())),
            ]]);
            std::fs::write(store.join("BackgroundItems-v18.btm"), bytes).unwrap();
        };
        let roots = startup::Roots {
            home: s.home.clone(),
            root: s.home.clone(),
            applications: vec![],
        };
        let item = || {
            s.startup_scan_at(&roots)
                .unwrap()
                .items
                .into_iter()
                .find(|i| i.category == StartupCategory::LoginItems)
                .unwrap()
        };
        // Registered once, switched off: never in the login items list.
        registered(2);
        let first = item();
        assert_eq!(first.state, StartupState::Disabled);
        assert!(first.actions.contains(&StartupAction::Enable));
        let entry = LoginItem {
            name: "Fixture".into(),
            path: app.to_string_lossy().into(),
            hidden: false,
        };
        assert_eq!(
            s.startup_act(vec![first.id.clone()], StartupItemAction::Enable)
                .unwrap()
                .done
                .len(),
            1
        );
        assert_eq!(*fake.logins.lock().unwrap(), vec![entry.clone()]);
        // The list entry is now the switch, under the same ID.
        let on = item();
        assert_eq!(on.id, first.id);
        assert_eq!(on.state, StartupState::Enabled);
        assert!(on.actions.contains(&StartupAction::Disable));
        assert_eq!(
            s.startup_act(vec![on.id.clone()], StartupItemAction::Disable)
                .unwrap()
                .done
                .len(),
            1
        );
        assert!(fake.logins.lock().unwrap().is_empty());
        // BTM still lists it as opening at login; the backup says it is off.
        registered(11);
        let off = item();
        assert_eq!(off.state, StartupState::Disabled);
        assert!(off.actions.contains(&StartupAction::Enable));
        s.startup_act(vec![off.id.clone()], StartupItemAction::Enable)
            .unwrap();
        assert_eq!(*fake.logins.lock().unwrap(), vec![entry]);
        assert_eq!(item().state, StartupState::Enabled);
    }
    #[test]
    fn cron_occurrences_survive_every_remove_restore_order_and_rescan() {
        let orders = [
            [0, 1, 2],
            [0, 2, 1],
            [1, 0, 2],
            [1, 2, 0],
            [2, 0, 1],
            [2, 1, 0],
        ];
        for deletion in orders {
            for restoration in orders {
                let (_tmp, mut s, _) = super::super::tests::fixture();
                let fake = Arc::new(Fake::default());
                Arc::get_mut(&mut s).unwrap().commands = fake.clone();
                let lines = [
                    "# comment\n",
                    "PATH=/usr/bin\n",
                    "0 1 * * * echo same\n",
                    "0 1 * * * echo same\n",
                    "PATH=/bin\n",
                    " 0 1 * * * echo same",
                ];
                let original = lines.concat();
                *fake.cron.lock().unwrap() = Some(original.clone());
                let roots = startup::Roots {
                    home: s.home.clone(),
                    root: s.home.clone(),
                    applications: vec![],
                };
                let scan = s.startup_scan_at(&roots).unwrap();
                let mut jobs: Vec<_> = scan
                    .items
                    .iter()
                    .filter(|i| i.category == StartupCategory::Cron && i.name.ends_with("same"))
                    .collect();
                jobs.sort_by_key(|i| {
                    i.label
                        .as_ref()
                        .unwrap()
                        .rsplit(':')
                        .next()
                        .unwrap()
                        .parse::<usize>()
                        .unwrap()
                });
                let ids: Vec<_> = jobs.iter().map(|i| i.id.clone()).collect();
                let mut removed = std::collections::BTreeSet::new();
                for (order, action) in [
                    (deletion, StartupItemAction::Remove),
                    (restoration, StartupItemAction::Restore),
                ] {
                    for index in order {
                        let outcome = s.startup_act(vec![ids[index].clone()], action).unwrap();
                        assert_eq!(
                            outcome.done,
                            vec![ids[index].clone()],
                            "{:?}",
                            outcome.failed
                        );
                        if action == StartupItemAction::Remove {
                            removed.insert(index);
                        } else {
                            removed.remove(&index);
                        }
                        let expected: String = lines
                            .iter()
                            .enumerate()
                            .filter(|(i, _)| {
                                ![2, 3, 5]
                                    .iter()
                                    .enumerate()
                                    .any(|(job, line)| line == i && removed.contains(&job))
                            })
                            .map(|(_, s)| *s)
                            .collect();
                        assert_eq!(fake.crontab_read().unwrap(), Some(expected));
                        let scan = s.startup_scan_at(&roots).unwrap();
                        assert_eq!(
                            scan.items
                                .iter()
                                .filter(|i| i.category == StartupCategory::Cron)
                                .count(),
                            5
                        );
                        for (i, id) in ids.iter().enumerate() {
                            let item = scan.items.iter().find(|item| &item.id == id).unwrap();
                            assert_eq!(item.state == StartupState::Removed, removed.contains(&i));
                        }
                    }
                }
                assert_eq!(fake.crontab_read().unwrap(), Some(original));
            }
        }
    }

    #[test]
    fn cron_pending_survives_os_success_database_failure_and_service_restart() {
        let (_tmp, mut s, _) = super::super::tests::fixture();
        let fake = Arc::new(Fake::default());
        Arc::get_mut(&mut s).unwrap().commands = fake.clone();
        *fake.cron.lock().unwrap() = Some("* * * * * first\n* * * * * second".into());
        let roots = startup::Roots {
            home: s.home.clone(),
            root: s.home.clone(),
            applications: vec![],
        };
        let scan = s.startup_scan_at(&roots).unwrap();
        let id = scan
            .items
            .iter()
            .find(|i| i.name.ends_with("first"))
            .unwrap()
            .id
            .clone();
        s.database.with(|c| { c.execute_batch("CREATE TRIGGER fail_cron_commit BEFORE UPDATE ON security_startup WHEN json_extract(NEW.value,'$.state')='removed' BEGIN SELECT RAISE(FAIL,'fixture commit failure'); END;")?; Ok(()) }).unwrap();
        let outcome = s
            .startup_act(vec![id.clone()], StartupItemAction::Remove)
            .unwrap();
        assert!(outcome.done.is_empty());
        assert_eq!(fake.crontab_read().unwrap().unwrap(), "* * * * * second");
        s.database
            .with(|c| {
                c.execute_batch("DROP TRIGGER fail_cron_commit")?;
                Ok(())
            })
            .unwrap();
        let restarted = Service::new(
            s.database.clone(),
            s.settings.clone(),
            Arc::new(|_| {}),
            s.home.clone(),
            s.data.clone(),
            s.av.config.clone(),
            fake.clone(),
        );
        let scan = restarted.startup_scan_at(&roots).unwrap();
        assert_eq!(
            scan.items.iter().find(|i| i.id == id).unwrap().state,
            StartupState::Removed
        );
        let outcome = restarted
            .startup_act(vec![id], StartupItemAction::Restore)
            .unwrap();
        assert!(outcome.failed.is_empty());
        assert_eq!(
            fake.crontab_read().unwrap().unwrap(),
            "* * * * * first\n* * * * * second"
        );
        restarted.av.shutdown();
    }

    #[test]
    fn exact_legacy_chain_migrates_duplicates_without_lifo_restore() {
        let (_tmp, mut s, _) = super::super::tests::fixture();
        let fake = Arc::new(Fake::default());
        Arc::get_mut(&mut s).unwrap().commands = fake.clone();
        let original = "SHELL=/bin/sh\n* * * * * same\n* * * * * same\n";
        *fake.cron.lock().unwrap() = Some(original.into());
        let roots = startup::Roots {
            home: s.home.clone(),
            root: s.home.clone(),
            applications: vec![],
        };
        let scan = s.startup_scan_at(&roots).unwrap();
        let mut jobs: Vec<_> = scan
            .items
            .into_iter()
            .filter(|i| i.category == StartupCategory::Cron && i.name.ends_with("same"))
            .collect();
        jobs.sort_by(|a, b| a.label.cmp(&b.label));
        let middle = "SHELL=/bin/sh\n* * * * * same\n";
        {
            let (item, before, after) = (&mut jobs[0], original, middle);
            item.state = StartupState::Removed;
            item.actions = vec![StartupAction::Restore];
            s.persist_legacy_item(item).unwrap();
            s.s2_set(
                &backup_key(&item.id),
                &Undo::Cron {
                    before: Some(before.into()),
                    after: after.into(),
                },
            )
            .unwrap();
        }
        jobs[1].label = Some(fingerprint(middle, 1));
        jobs[1].state = StartupState::Removed;
        s.persist_legacy_item(&jobs[1]).unwrap();
        s.s2_set(
            &backup_key(&jobs[1].id),
            &Undo::Cron {
                before: Some(middle.into()),
                after: "SHELL=/bin/sh\n".into(),
            },
        )
        .unwrap();
        *fake.cron.lock().unwrap() = Some("SHELL=/bin/sh\n".into());
        for item in jobs {
            assert!(
                s.startup_act(vec![item.id], StartupItemAction::Restore)
                    .unwrap()
                    .failed
                    .is_empty()
            );
            s.startup_scan_at(&roots).unwrap();
        }
        assert_eq!(fake.crontab_read().unwrap().unwrap(), original);
    }

    #[test]
    fn cron_write_failures_and_external_changes_keep_exact_recoverable_state() {
        let (_tmp, mut s, _) = super::super::tests::fixture();
        let fake = Arc::new(Fake::default());
        Arc::get_mut(&mut s).unwrap().commands = fake.clone();
        let original = "* * * * * first\n";
        *fake.cron.lock().unwrap() = Some(original.into());
        let roots = startup::Roots {
            home: s.home.clone(),
            root: s.home.clone(),
            applications: vec![],
        };
        let id = s
            .startup_scan_at(&roots)
            .unwrap()
            .items
            .into_iter()
            .find(|i| i.category == StartupCategory::Cron)
            .unwrap()
            .id;
        fake.fail_cron.store(true, Ordering::SeqCst);
        assert!(
            s.startup_act(vec![id.clone()], StartupItemAction::Remove)
                .unwrap()
                .done
                .is_empty()
        );
        assert_eq!(fake.crontab_read().unwrap(), Some(original.into()));
        fake.fail_cron.store(false, Ordering::SeqCst);
        fake.change_on_read.store(2, Ordering::SeqCst);
        assert!(
            s.startup_act(vec![id.clone()], StartupItemAction::Remove)
                .unwrap()
                .done
                .is_empty()
        );
        assert_eq!(
            fake.crontab_read().unwrap(),
            Some(format!("{original}# external\n"))
        );
        // This action never attempted an OS write, so its prepared intent is
        // canceled even though external text no longer matches the old journal.
        assert!(s.reconcile_cron().is_ok());
        *fake.cron.lock().unwrap() = Some(original.into());
        fake.fail_after_write.store(true, Ordering::SeqCst);
        assert_eq!(
            s.startup_act(vec![id.clone()], StartupItemAction::Remove)
                .unwrap()
                .done,
            vec![id.clone()]
        );
        assert_eq!(fake.crontab_read().unwrap(), Some(String::new()));
        *fake.cron.lock().unwrap() = Some("# external environment\nPATH=/bin\n".into());
        let before = fake.crontab_read().unwrap();
        assert!(
            s.startup_act(vec![id.clone()], StartupItemAction::Restore)
                .unwrap()
                .done
                .is_empty()
        );
        assert_eq!(fake.crontab_read().unwrap(), before);
        *fake.cron.lock().unwrap() = Some(String::new());
        assert_eq!(
            s.startup_act(vec![id], StartupItemAction::Restore)
                .unwrap()
                .done
                .len(),
            1
        );
        assert_eq!(fake.crontab_read().unwrap(), Some(original.into()));
    }
    #[test]
    fn cron_batch_restore_preserves_bytes_and_independent_occurrences() {
        let (_tmp, mut s, _) = super::super::tests::fixture();
        let fake = Arc::new(Fake::default());
        Arc::get_mut(&mut s).unwrap().commands = fake.clone();
        let original = "# comment\r\nPATH=/bin\n* * * * * same\n* * * * * same";
        *fake.cron.lock().unwrap() = Some(original.into());
        let roots = startup::Roots {
            home: s.home.clone(),
            root: s.home.clone(),
            applications: vec![],
        };
        let scan = s.startup_scan_at(&roots).unwrap();
        let mut ids: Vec<_> = scan
            .items
            .iter()
            .filter(|i| i.category == StartupCategory::Cron && i.name.ends_with("same"))
            .map(|i| i.id.clone())
            .collect();
        assert_eq!(ids.len(), 2);
        let removed = s
            .startup_act(ids.clone(), StartupItemAction::Remove)
            .unwrap();
        assert!(removed.failed.is_empty());
        assert_eq!(removed.done.len(), 2);
        assert_eq!(
            fake.crontab_read().unwrap(),
            Some("# comment\r\nPATH=/bin\n".into())
        );
        s.startup_scan_at(&roots).unwrap();
        ids.reverse();
        let restored = s.startup_act(ids, StartupItemAction::Restore).unwrap();
        assert!(restored.failed.is_empty());
        assert_eq!(restored.done.len(), 2);
        assert_eq!(fake.crontab_read().unwrap(), Some(original.into()));
    }

    #[test]
    fn legacy_cron_conflict_retains_original_undo_and_external_text() {
        let (_tmp, mut s, _) = super::super::tests::fixture();
        let fake = Arc::new(Fake::default());
        Arc::get_mut(&mut s).unwrap().commands = fake.clone();
        let original = "* * * * * original\n";
        *fake.cron.lock().unwrap() = Some(original.into());
        let roots = startup::Roots {
            home: s.home.clone(),
            root: s.home.clone(),
            applications: vec![],
        };
        let mut item = s
            .startup_scan_at(&roots)
            .unwrap()
            .items
            .into_iter()
            .find(|i| i.category == StartupCategory::Cron)
            .unwrap();
        item.state = StartupState::Removed;
        item.actions = vec![StartupAction::Restore];
        s.persist_legacy_item(&item).unwrap();
        s.s2_set(
            &backup_key(&item.id),
            &Undo::Cron {
                before: Some(original.into()),
                after: String::new(),
            },
        )
        .unwrap();
        let external = "# externally changed\n";
        *fake.cron.lock().unwrap() = Some(external.into());
        let result = s
            .startup_act(vec![item.id.clone()], StartupItemAction::Restore)
            .unwrap();
        assert!(result.done.is_empty());
        assert_eq!(result.failed[0].code, "E_STARTUP_CHANGED");
        assert_eq!(fake.crontab_read().unwrap(), Some(external.into()));
        let undo: Undo = s.s2_get(&backup_key(&item.id)).unwrap().unwrap();
        assert!(
            matches!(undo, Undo::Cron { before, after } if before.as_deref()==Some(original) && after.is_empty())
        );
    }
    fn cron_and_launch_fixture() -> (
        super::super::tests::Fixture,
        Arc<Service>,
        Arc<Fake>,
        startup::Roots,
        String,
        String,
    ) {
        let (temp, mut service, _) = super::super::tests::fixture();
        let commands = Arc::new(Fake::default());
        Arc::get_mut(&mut service).unwrap().commands = commands.clone();
        *commands.cron.lock().unwrap() = Some("* * * * * first\n* * * * * second\n".into());
        let path = service.home.join("Library/LaunchAgents/good.plist");
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(path, r#"<plist><dict><key>Label</key><string>org.fixture.good</string><key>Program</key><string>/usr/bin/true</string><key>RunAtLoad</key><true/></dict></plist>"#).unwrap();
        let roots = startup::Roots {
            home: service.home.clone(),
            root: temp.path().join("system"),
            applications: vec![],
        };
        let scan = service.startup_scan_at(&roots).unwrap();
        let cron = scan
            .items
            .iter()
            .find(|i| i.category == StartupCategory::Cron && i.name.ends_with("first"))
            .unwrap()
            .id
            .clone();
        let launch = scan
            .items
            .iter()
            .find(|i| i.label.as_deref() == Some("org.fixture.good"))
            .unwrap()
            .id
            .clone();
        (temp, service, commands, roots, cron, launch)
    }
    fn restart(service: &Service, commands: Arc<Fake>) -> Arc<Service> {
        Service::new(
            service.database.clone(),
            service.settings.clone(),
            Arc::new(|_| {}),
            service.home.clone(),
            service.data.clone(),
            service.av.config.clone(),
            commands,
        )
    }
    fn journal(service: &Service) -> serde_json::Value {
        service.s2_get("security_cron_journal_v2").unwrap().unwrap()
    }

    #[test]
    fn canceled_cron_prewrite_is_local_and_new_text_is_manageable_after_restart() {
        for read_fails in [false, true] {
            let (_tmp, service, commands, roots, cron, launch) = cron_and_launch_fixture();
            if read_fails {
                commands.fail_on_read.store(2, Ordering::SeqCst);
            } else {
                commands.change_on_read.store(2, Ordering::SeqCst);
            }
            let result = service
                .startup_act(vec![cron], StartupItemAction::Remove)
                .unwrap();
            assert!(result.done.is_empty());
            assert_eq!(
                result.failed[0].code,
                if read_fails {
                    "E_STARTUP_PROTECTED"
                } else {
                    "E_STARTUP_CHANGED"
                }
            );
            assert_eq!(commands.writes.load(Ordering::SeqCst), 0);
            assert!(journal(&service)["pending"].is_null());
            let external = commands.crontab_read().unwrap();
            let restarted = restart(&service, commands.clone());
            let scan = restarted.startup_scan_at(&roots).unwrap();
            assert!(scan.cron_error.is_none());
            assert!(scan.items.iter().any(|i| i.id == launch));
            for action in [StartupItemAction::MarkReviewed, StartupItemAction::Reveal] {
                assert_eq!(
                    restarted
                        .startup_act(vec![launch.clone()], action)
                        .unwrap()
                        .done,
                    vec![launch.clone()]
                );
            }
            assert_eq!(commands.crontab_read().unwrap(), external);
            let job = scan
                .items
                .iter()
                .find(|i| i.category == StartupCategory::Cron && i.name.ends_with("first"))
                .unwrap();
            assert_eq!(
                restarted
                    .startup_act(vec![job.id.clone()], StartupItemAction::Remove)
                    .unwrap()
                    .done,
                vec![job.id.clone()]
            );
            assert_eq!(
                restarted
                    .startup_act(vec![job.id.clone()], StartupItemAction::Restore)
                    .unwrap()
                    .done,
                vec![job.id.clone()]
            );
            assert_eq!(commands.crontab_read().unwrap(), external);
            restarted.av.shutdown();
        }
    }

    #[test]
    fn canceled_cron_intent_keeps_prior_undo_and_mixed_batch_progress() {
        let (_tmp, service, commands, roots, first, launch) = cron_and_launch_fixture();
        assert_eq!(
            service
                .startup_act(vec![first.clone()], StartupItemAction::Remove)
                .unwrap()
                .done,
            vec![first.clone()]
        );
        let undo = service.database.meta(&backup_key(&first)).unwrap();
        let scan = service.startup_scan_at(&roots).unwrap();
        let second = scan
            .items
            .iter()
            .find(|i| i.category == StartupCategory::Cron && i.state != StartupState::Removed)
            .unwrap()
            .id
            .clone();
        commands.change_on_read.store(2, Ordering::SeqCst);
        let result = service
            .startup_act(vec![second], StartupItemAction::Remove)
            .unwrap();
        assert_eq!(result.failed[0].code, "E_STARTUP_CHANGED");
        assert_eq!(commands.writes.load(Ordering::SeqCst), 1);
        assert!(journal(&service)["pending"].is_null());
        assert_eq!(service.database.meta(&backup_key(&first)).unwrap(), undo);
        let external = commands.crontab_read().unwrap();
        let restarted = restart(&service, commands.clone());
        let scan = restarted.startup_scan_at(&roots).unwrap();
        assert_eq!(scan.cron_error.as_deref(), Some("E_STARTUP_CHANGED"));
        assert!(scan.missing.contains(&StartupCategory::Cron));
        assert!(scan.items.iter().any(|i| i.id == launch));
        assert!(
            scan.items
                .iter()
                .any(|i| i.id == first && i.state == StartupState::Removed)
        );
        // Both orders of the same action batch must still process unrelated rows.
        for ids in [
            vec![first.clone(), launch.clone()],
            vec![launch.clone(), first.clone()],
        ] {
            let result = restarted
                .startup_act(ids, StartupItemAction::Disable)
                .unwrap();
            assert_eq!(result.done, vec![launch.clone()]);
            assert_eq!(result.failed.len(), 1);
            assert_eq!(result.failed[0].id, first);
        }
        let result = restarted
            .startup_act(vec![first.clone()], StartupItemAction::Restore)
            .unwrap();
        assert_eq!(result.failed[0].code, "E_STARTUP_CHANGED");
        assert_eq!(commands.crontab_read().unwrap(), external);
        assert_eq!(service.database.meta(&backup_key(&first)).unwrap(), undo);
        *commands.cron.lock().unwrap() = Some("* * * * * second\n".into());
        assert_eq!(
            restarted
                .startup_act(vec![first.clone()], StartupItemAction::Restore)
                .unwrap()
                .done,
            vec![first]
        );
        assert!(
            restarted
                .startup_scan_at(&roots)
                .unwrap()
                .cron_error
                .is_none()
        );
        restarted.av.shutdown();
    }

    #[test]
    fn uncertain_cron_write_and_old_pending_stay_local_and_durable_after_restart() {
        for legacy_pending in [false, true] {
            let (_tmp, service, commands, roots, cron, launch) = cron_and_launch_fixture();
            service.database.with(|c| { c.execute_batch("CREATE TRIGGER fail_cron_commit BEFORE UPDATE ON security_startup WHEN json_extract(NEW.value,'$.state')='removed' BEGIN SELECT RAISE(FAIL,'fixture commit failure'); END;")?; Ok(()) }).unwrap();
            assert!(
                service
                    .startup_act(vec![cron.clone()], StartupItemAction::Remove)
                    .unwrap()
                    .done
                    .is_empty()
            );
            assert_eq!(commands.writes.load(Ordering::SeqCst), 1);
            service
                .database
                .with(|c| {
                    c.execute_batch("DROP TRIGGER fail_cron_commit")?;
                    Ok(())
                })
                .unwrap();
            let mut saved = journal(&service);
            assert_eq!(saved["pending"]["phase"], "write_unknown");
            if legacy_pending {
                saved["pending"].as_object_mut().unwrap().remove("phase");
                service.s2_set("security_cron_journal_v2", &saved).unwrap();
            }
            let applied = commands.crontab_read().unwrap();
            commands.fail_on_read.store(1, Ordering::SeqCst);
            let unreadable = service.startup_scan_at(&roots).unwrap();
            assert_eq!(
                unreadable.cron_error.as_deref(),
                Some("E_STARTUP_PROTECTED")
            );
            assert!(unreadable.items.iter().any(|i| i.id == launch));
            assert_eq!(journal(&service), saved);
            commands
                .cron
                .lock()
                .unwrap()
                .as_mut()
                .unwrap()
                .push_str("# external\n");
            let external = commands.crontab_read().unwrap();
            let restarted = restart(&service, commands.clone());
            let scan = restarted.startup_scan_at(&roots).unwrap();
            assert_eq!(scan.cron_error.as_deref(), Some("E_STARTUP_CHANGED"));
            assert!(scan.items.iter().any(|i| i.id == launch));
            assert_eq!(journal(&restarted), saved);
            assert_eq!(
                restarted
                    .startup_act(vec![launch.clone()], StartupItemAction::MarkReviewed)
                    .unwrap()
                    .done,
                vec![launch.clone()]
            );
            let result = restarted
                .startup_act(
                    vec![cron.clone(), launch.clone()],
                    StartupItemAction::Remove,
                )
                .unwrap();
            assert_eq!(result.done, vec![launch]);
            assert_eq!(result.failed[0].id, cron);
            assert_eq!(result.failed[0].code, "E_STARTUP_CHANGED");
            assert_eq!(journal(&restarted), saved);
            assert_eq!(commands.crontab_read().unwrap(), external);
            assert_eq!(commands.writes.load(Ordering::SeqCst), 1);
            // Once the exact post-write text is observed, finish the pending
            // commit and restore the original occurrence without another remove.
            *commands.cron.lock().unwrap() = applied;
            let scan = restarted.startup_scan_at(&roots).unwrap();
            assert!(
                scan.items
                    .iter()
                    .any(|i| i.id == cron && i.state == StartupState::Removed)
            );
            assert_eq!(
                restarted
                    .startup_act(vec![cron.clone()], StartupItemAction::Restore)
                    .unwrap()
                    .done,
                vec![cron]
            );
            restarted.av.shutdown();
        }
    }

    #[test]
    fn prepared_cron_survives_failed_cancel_without_becoming_an_uncertain_write() {
        let (_tmp, service, commands, roots, cron, launch) = cron_and_launch_fixture();
        // Reject only clearing the intent, simulating a crash/DB failure after
        // the second read. Prepared remains durable and can be discarded later.
        service.database.with(|c| { c.execute_batch("CREATE TRIGGER fail_cancel BEFORE UPDATE ON meta WHEN NEW.key='security_cron_journal_v2' AND json_type(NEW.value,'$.pending')='null' BEGIN SELECT RAISE(FAIL,'fixture cancel failure'); END;")?; Ok(()) }).unwrap();
        commands.change_on_read.store(2, Ordering::SeqCst);
        assert!(
            service
                .startup_act(vec![cron], StartupItemAction::Remove)
                .unwrap()
                .done
                .is_empty()
        );
        assert_eq!(commands.writes.load(Ordering::SeqCst), 0);
        assert_eq!(journal(&service)["pending"]["phase"], "prepared");
        // A real storage failure is still observable; unrelated actions do not
        // consult this journal, while a scan reports the DB failure to its caller.
        assert!(service.startup_scan_at(&roots).is_err());
        assert_eq!(
            service
                .startup_act(vec![launch.clone()], StartupItemAction::MarkReviewed)
                .unwrap()
                .done,
            vec![launch]
        );
        service
            .database
            .with(|c| {
                c.execute_batch("DROP TRIGGER fail_cancel")?;
                Ok(())
            })
            .unwrap();
        let external = commands.crontab_read().unwrap();
        let restarted = restart(&service, commands.clone());
        assert!(
            restarted
                .startup_scan_at(&roots)
                .unwrap()
                .cron_error
                .is_none()
        );
        assert!(journal(&restarted)["pending"].is_null());
        assert_eq!(commands.crontab_read().unwrap(), external);
        assert_eq!(commands.writes.load(Ordering::SeqCst), 0);
        restarted.av.shutdown();
    }
}
