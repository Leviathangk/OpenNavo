#[cfg(target_os = "macos")]
pub fn install(app: &tauri::AppHandle) {
    super::paste::native::install(app);
    use super::*;
    use std::{
        collections::{BTreeMap, BTreeSet},
        sync::mpsc,
        time::{Duration, Instant},
    };
    use tauri::Manager;
    use tauri_plugin_notification::NotificationExt;
    let Some(core) = app.try_state::<Arc<crate::core::DesktopCore>>() else {
        return;
    };
    let service = core.queue.security.clone();
    let own_data = match service.own_data() {
        Ok(guard) => guard,
        Err(error) => {
            log::warn!("Security watch protection unavailable: {error}");
            return;
        }
    };
    let weak = Arc::downgrade(&core.queue);
    let home = service.home.clone();
    let mut paths = startup_watch_paths(&home);
    paths = paths
        .into_iter()
        .filter_map(|p| {
            p.ancestors()
                .find(|p| p.is_dir() && *p != Path::new("/"))
                .map(Path::to_owned)
        })
        .collect();
    let (sender, receiver) = mpsc::sync_channel(1);
    // BTM stores as they are now; the system's own rewrites leave these unchanged.
    let stores: BTreeMap<PathBuf, u64> = btm_roots(&home)
        .iter()
        .filter_map(|root| std::fs::read_dir(root).ok())
        .flatten()
        .flatten()
        .map(|entry| entry.path())
        .filter(|path| path.extension().is_some_and(|e| e == "btm"))
        .filter_map(|path| {
            super::startup::btm_fingerprint(&path, Some(&own_data)).map(|print| (path, print))
        })
        .collect();
    let stores = std::sync::Mutex::new(stores);
    let relevant_home = home.clone();
    let stream = crate::uninstall::fsevents::watch_paths(
        paths,
        sender,
        Box::new(move |path| match startup_relevant(path, &relevant_home) {
            StartupChange::No => false,
            StartupChange::Yes => true,
            // An unreadable store is one the scan cannot see either.
            StartupChange::IfBtmChanged => super::startup::btm_fingerprint(path, Some(&own_data))
                .is_some_and(|print| {
                    stores
                        .lock()
                        .map(|mut stores| stores.insert(path.to_owned(), print) != Some(print))
                        .unwrap_or(true)
                }),
        }),
    );
    let stop = Arc::new(AtomicBool::new(false));
    let stopped = stop.clone();
    struct Guard {
        stop: Arc<AtomicBool>,
        _stream: crate::uninstall::fsevents::Guard,
    }
    impl Drop for Guard {
        fn drop(&mut self) {
            self.stop.store(true, Ordering::Release);
        }
    }
    app.manage(Guard {
        stop,
        _stream: stream,
    });
    let app = app.clone();
    // Downloads that change after this point are new, even if the first startup read
    // or a Downloads permission prompt holds up the listing of what was already there.
    let watch_started = std::time::SystemTime::now();
    std::thread::spawn(move || {
        let mut seen: BTreeSet<String> = service
            .saved_startup()
            .unwrap_or_default()
            .into_iter()
            .map(|i| i.id)
            .collect();
        // A threat in a finished download: the app may be in the background, so the
        // system notification is what tells the user; the window offers the scan.
        let notify_threat = |path: &Path, signature: &str, settings: &Settings| {
            let title = match settings.locale {
                crate::model::Locale::ZhCn => "下载的文件有威胁",
                crate::model::Locale::JaJp => "ダウンロードしたファイルに脅威",
                crate::model::Locale::EsEs => "Amenaza en una descarga",
                crate::model::Locale::PtBr => "Ameaça em um download",
                crate::model::Locale::RuRu => "Угроза в загруженном файле",
                _ => "Threat in a download",
            };
            let name = path
                .file_name()
                .map(|n| n.to_string_lossy())
                .unwrap_or_default();
            let _ = app
                .notification()
                .builder()
                .title(title)
                .body(format!("{name} · {signature}"))
                .show();
        };
        let notify_startup =
            |snapshot: &StartupSnapshot, seen: &BTreeSet<String>, settings: &Settings| {
                let added: Vec<_> = snapshot
                    .items
                    .iter()
                    .filter(|i| i.is_new && !seen.contains(&i.id))
                    .collect();
                if !added.is_empty() {
                    let title = match settings.locale {
                        crate::model::Locale::ZhCn => "发现新启动项",
                        crate::model::Locale::JaJp => "新しい起動項目",
                        crate::model::Locale::EsEs => "Nuevo elemento de inicio",
                        crate::model::Locale::PtBr => "Novo item de inicialização",
                        crate::model::Locale::RuRu => "Новый элемент автозапуска",
                        _ => "New startup item",
                    };
                    let body = added
                        .iter()
                        .take(3)
                        .map(|i| i.name.as_str())
                        .collect::<Vec<_>>()
                        .join(", ");
                    let _ = app.notification().builder().title(title).body(body).show();
                }
            };
        if service.settings.read().is_ok_and(|s| s.startup_alerts)
            && let Ok(snapshot) = service.startup_scan()
        {
            if let Ok(settings) = service.settings.read()
                && settings.startup_alerts
            {
                notify_startup(&snapshot, &seen, &settings);
            }
            seen.extend(snapshot.items.into_iter().map(|i| i.id));
        }
        let mut downloads: BTreeMap<PathBuf, (u64, std::time::SystemTime, Instant, bool)> = service
            .downloads()
            .into_iter()
            .filter_map(|p| {
                let m = files::safe_metadata(&p).ok()?;
                let modified = m.modified().ok()?;
                Some((
                    p,
                    (m.len(), modified, Instant::now(), modified < watch_started),
                ))
            })
            .collect();
        let mut last_scan = Instant::now();
        let mut pending = false;
        let mut alerts_were_enabled = service
            .settings
            .read()
            .map(|s| s.startup_alerts)
            .unwrap_or(false);
        let mut last_schedule = Instant::now() - Duration::from_secs(60);
        let mut last_db = Instant::now() - Duration::from_secs(60);
        while !stopped.load(Ordering::Acquire) {
            if receiver.recv_timeout(Duration::from_secs(1)).is_ok() {
                pending = true;
                while receiver.try_recv().is_ok() {}
            }
            let Ok(settings) = service.settings.read().map(|s| s.clone()) else {
                continue;
            };
            if settings.startup_alerts && !alerts_were_enabled {
                pending = true;
            }
            alerts_were_enabled = settings.startup_alerts;
            if startup_scan_due(settings.startup_alerts, pending, last_scan.elapsed()) {
                pending = false;
                if settings.startup_alerts
                    && let Ok(snapshot) = service.startup_scan()
                {
                    notify_startup(&snapshot, &seen, &settings);
                    seen.extend(snapshot.items.into_iter().map(|i| i.id));
                }
                // Measure from completion: a slow inventory must not make the next
                // fallback immediately due. Failed scans also retry at this interval.
                last_scan = Instant::now();
            }
            {
                for path in service.downloads() {
                    let Ok(meta) = files::safe_metadata(&path) else {
                        continue;
                    };
                    let Ok(modified) = meta.modified() else {
                        continue;
                    };
                    let entry = downloads.entry(path.clone()).or_insert((
                        meta.len(),
                        modified,
                        Instant::now(),
                        false,
                    ));
                    if entry.0 != meta.len() || entry.1 != modified {
                        *entry = (meta.len(), modified, Instant::now(), false);
                    }
                    if entry.3 || entry.2.elapsed() < Duration::from_secs(3) {
                        continue;
                    }
                    if let Ok(Some(token)) = service.recent_download_token(&path) {
                        if service.download_detected(&path, token.clone()).is_ok() {
                            entry.3 = service.download_verify(path.clone(), Some(token)).is_ok();
                        }
                    } else if settings.av_consent && settings.watch_downloads {
                        entry.3 = service
                            .download_verify(path.clone(), None)
                            .map(|verdict| {
                                if let Some(signature) =
                                    verdict.av.as_ref().and_then(|av| av.signature.as_deref())
                                {
                                    notify_threat(&path, signature, &settings);
                                }
                            })
                            .is_ok();
                    }
                    if !entry.3 {
                        entry.2 = Instant::now();
                    }
                }
            }
            if last_schedule.elapsed() >= Duration::from_secs(30) {
                last_schedule = Instant::now();
                if settings.av_schedule != AvSchedule::Off {
                    let _ = service.schedule_tick(
                        chrono::Local::now(),
                        super::schedule::on_battery(service.commands.as_ref()),
                    );
                }
            }
            if settings.av_consent && last_db.elapsed() >= Duration::from_secs(60) {
                last_db = Instant::now();
                if service.av.check_update().is_ok()
                    && let Some(queue) = weak.upgrade()
                {
                    let _ = queue.enqueue(
                        crate::model::TaskOp::AvDbUpdate,
                        None,
                        Default::default(),
                        crate::model::TaskTrigger::Schedule,
                    );
                }
            }
        }
    });
}
#[cfg(not(target_os = "macos"))]
pub fn install(_: &tauri::AppHandle) {}

// FSEvents may miss a protected cron spool or drop events. Keep a bounded
// fallback even when no relevant event arrives; never scan with alerts disabled.
fn startup_scan_due(enabled: bool, pending: bool, elapsed: std::time::Duration) -> bool {
    enabled
        && ((pending && elapsed >= std::time::Duration::from_secs(3))
            || elapsed >= std::time::Duration::from_secs(300))
}

fn startup_watch_paths(home: &std::path::Path) -> Vec<std::path::PathBuf> {
    let mut paths: Vec<_> = [
        "/Library/LaunchAgents",
        "/Library/LaunchDaemons",
        "/Library/SystemExtensions",
        "/Library/Extensions",
        "/Library/Preferences",
        "/Library/Security",
        "/Library/Spotlight",
        "/private/var/at/tabs",
        "/private/etc",
        "/Applications",
    ]
    .into_iter()
    .map(std::path::PathBuf::from)
    .collect();
    paths.extend(btm_roots(home));
    paths.extend([
        // Watch the parent so creation and atomic replacement of shell files work.
        // The event filter excludes unrelated files below the home directory.
        home.to_owned(),
        home.join("Library/LaunchAgents"),
        home.join("Library/Preferences"),
        home.join("Library/Spotlight"),
    ]);
    paths
}

/// What an event under a watched root means for the startup list.
#[derive(Debug, PartialEq)]
pub(crate) enum StartupChange {
    No,
    Yes,
    /// A BTM store was written: a change only if its items differ (`btm_fingerprint`).
    IfBtmChanged,
}

fn btm_roots(home: &std::path::Path) -> [std::path::PathBuf; 2] {
    [
        "/private/var/db/com.apple.backgroundtaskmanagement".into(),
        home.join("Library/Application Support/com.apple.backgroundtaskmanagementagent"),
    ]
}

/// Event paths that can add, remove or change a startup item. The watched
/// Preferences folders, /private/etc and /Applications also change all the time
/// for other reasons, and macOS rewrites its BTM stores every few seconds;
/// rescanning on each such write kept the startup scan running back to back
/// (about every 40 s, each scan 35–45 s, on the acceptance Mac).
pub(crate) fn startup_relevant(path: &std::path::Path, home: &std::path::Path) -> StartupChange {
    use std::path::Path;
    let path = path
        .strip_prefix("/System/Volumes/Data")
        .map(|p| Path::new("/").join(p))
        .unwrap_or_else(|_| path.to_owned());
    let answer = |yes: bool| {
        if yes {
            StartupChange::Yes
        } else {
            StartupChange::No
        }
    };
    if btm_roots(home).iter().any(|root| path.starts_with(root)) {
        return if path.extension().is_some_and(|e| e == "btm") {
            StartupChange::IfBtmChanged
        } else {
            StartupChange::No
        };
    }
    let roots = [
        Path::new("/Library/LaunchAgents").to_owned(),
        Path::new("/Library/LaunchDaemons").to_owned(),
        Path::new("/Library/SystemExtensions").to_owned(),
        Path::new("/Library/Extensions").to_owned(),
        Path::new("/Library/Security").to_owned(),
        Path::new("/Library/Spotlight").to_owned(),
        home.join("Library/LaunchAgents"),
        home.join("Library/Spotlight"),
        Path::new("/private/var/at/tabs").to_owned(),
    ];
    // A root's own creation is reported on its parent, which is watched instead.
    if roots
        .iter()
        .any(|root| path.starts_with(root) || root.starts_with(&path))
    {
        return StartupChange::Yes;
    }
    let name = path.file_name().and_then(|n| n.to_str()).unwrap_or("");
    let parent = path.parent().unwrap_or(Path::new(""));
    if parent == home {
        return answer(
            [
                ".zshenv",
                ".zprofile",
                ".zshrc",
                ".zlogin",
                ".zlogout",
                ".bash_profile",
                ".bashrc",
                ".profile",
            ]
            .contains(&name),
        );
    }
    if parent == Path::new("/Library/Preferences") || parent == home.join("Library/Preferences") {
        // Saved atomically: a temporary sibling first, then the plist itself.
        return answer(
            name.starts_with("com.apple.loginwindow.")
                || name.starts_with("com.apple.preferences.extensions.FinderSync."),
        );
    }
    if let Ok(relative) = path.strip_prefix("/private/etc") {
        let relative = relative.to_string_lossy();
        return answer(
            [
                "rc.",
                "launchd.conf",
                "zshenv",
                "zprofile",
                "zshrc",
                "zlogin",
                "zlogout",
                "bashrc",
                "profile",
                "periodic",
                "defaults/periodic.conf",
                "emond.d",
            ]
            .iter()
            .any(|prefix| relative.starts_with(prefix)),
        );
    }
    if let Ok(relative) = path.strip_prefix("/Applications") {
        // A bundle appearing, going or being replaced brings or removes its plug-ins
        // and login items; files changing inside one do not. Hidden entries (update
        // backups, .DS_Store) hold none.
        let parts: Vec<_> = relative.components().map(|c| c.as_os_str()).collect();
        if parts
            .first()
            .is_some_and(|first| first.as_encoded_bytes().first() == Some(&b'.'))
        {
            return StartupChange::No;
        }
        let bundle = |part: &std::ffi::OsStr| part.to_string_lossy().ends_with(".app");
        return answer(
            parts.len() <= 1
                || (parts.len() == 2 && bundle(parts[1]))
                || relative
                    .to_string_lossy()
                    .contains("/Contents/Library/LoginItems"),
        );
    }
    StartupChange::No
}

#[cfg(test)]
mod tests {
    use super::{StartupChange, startup_relevant};
    use std::path::Path;
    #[test]
    fn user_sources_are_watched_and_pass_the_event_filter() {
        let home = Path::new("/Users/u");
        let roots = super::startup_watch_paths(home);
        for path in [
            "/private/var/at/tabs/u",
            "/private/var/at/tabs",
            "/Users/u/.zshenv",
            "/Users/u/.zprofile",
            "/Users/u/.zshrc",
            "/Users/u/.zlogin",
            "/Users/u/.zlogout",
            "/Users/u/.bash_profile",
            "/Users/u/.bashrc",
            "/Users/u/.profile",
            "/Users/u/Library/Spotlight",
            "/Users/u/Library/Spotlight/Example.mdimporter/Contents/Info.plist",
        ] {
            let path = Path::new(path);
            assert!(roots.iter().any(|root| path.starts_with(root)), "{path:?}");
            assert_eq!(startup_relevant(path, home), StartupChange::Yes, "{path:?}");
            let data_path = Path::new("/System/Volumes/Data").join(path.strip_prefix("/").unwrap());
            assert_eq!(startup_relevant(&data_path, home), StartupChange::Yes);
        }
        // A shell file need not exist when the watcher starts.
        assert!(roots.contains(&home.to_owned()));
        for path in [
            "/Users/u/.zsh_history",
            "/Users/u/.bash_history",
            "/Users/u/.zshrc.backup",
            "/Users/u/project/.zshrc",
            "/Users/other/.zshrc",
        ] {
            assert_eq!(
                startup_relevant(Path::new(path), home),
                StartupChange::No,
                "{path}"
            );
        }
    }

    #[test]
    fn fallback_scan_discovers_user_sources_added_after_baseline() {
        use super::super::{Commands, StartupCategory, startup, tests::fixture};
        use crate::AppError;
        use std::{
            fs,
            sync::{Arc, Mutex},
            time::Duration,
        };
        struct Cron(Mutex<String>);
        impl Commands for Cron {
            fn run(&self, _: &str, _: &[&str]) -> Result<String, AppError> {
                Ok(String::new())
            }
            fn crontab_read(&self) -> Result<Option<String>, AppError> {
                Ok(Some(self.0.lock().unwrap().clone()))
            }
        }
        let (temp, mut service, _) = fixture();
        let cron = Arc::new(Cron(Mutex::new(String::new())));
        Arc::get_mut(&mut service).unwrap().commands = cron.clone();
        let roots = startup::Roots {
            home: service.home.clone(),
            root: temp.path().canonicalize().unwrap(),
            applications: vec![],
        };
        service.startup_scan_at(&roots).unwrap();
        *cron.0.lock().unwrap() = "@reboot /usr/bin/true\n".into();
        fs::write(roots.home.join(".zshrc"), "# new shell configuration\n").unwrap();
        let plugin = roots
            .home
            .join("Library/Spotlight/Fixture.mdimporter/Contents");
        fs::create_dir_all(&plugin).unwrap();
        let mut info = plist::Dictionary::new();
        info.insert("CFBundleIdentifier".into(), "org.fixture.importer".into());
        plist::Value::Dictionary(info)
            .to_file_xml(plugin.join("Info.plist"))
            .unwrap();
        // No event was delivered (e.g. the cron spool is protected).
        assert!(super::startup_scan_due(
            true,
            false,
            Duration::from_secs(300)
        ));
        let snapshot = service.startup_scan_at(&roots).unwrap();
        for category in [
            StartupCategory::Cron,
            StartupCategory::ShellConfig,
            StartupCategory::Spotlight,
        ] {
            assert!(
                snapshot
                    .items
                    .iter()
                    .any(|item| item.category == category && item.is_new),
                "{category:?}"
            );
        }
    }

    #[test]
    fn startup_fallback_runs_without_events_and_respects_alert_setting() {
        use std::time::Duration;
        let due = |enabled, pending, seconds| {
            super::startup_scan_due(enabled, pending, Duration::from_secs(seconds))
        };
        assert!(!due(true, false, 299));
        assert!(due(true, false, 300));
        assert!(due(true, false, 600));
        assert!(!due(true, true, 2));
        assert!(due(true, true, 3));
        assert!(!due(false, false, 600));
        assert!(!due(false, true, 600));
        // The worker resets elapsed time after a scan, avoiding back-to-back fallback scans.
        assert!(!due(true, false, 0));
    }

    #[test]
    fn only_startup_sources_rescan_startup_items() {
        let home = Path::new("/Users/u");
        let check = |path: &str| startup_relevant(Path::new(path), home) == StartupChange::Yes;
        // BTM stores count only when their items differ; see btm_fingerprint.
        for path in [
            "/private/var/db/com.apple.backgroundtaskmanagement/BackgroundItems-v18.btm",
            "/System/Volumes/Data/private/var/db/com.apple.backgroundtaskmanagement/BackgroundItems-v18-58F37EF8.btm",
            "/Users/u/Library/Application Support/com.apple.backgroundtaskmanagementagent/backgrounditems.btm",
        ] {
            assert_eq!(
                startup_relevant(Path::new(path), home),
                StartupChange::IfBtmChanged,
                "{path}"
            );
        }
        for path in [
            "/private/var/db/com.apple.backgroundtaskmanagement",
            "/private/var/db/com.apple.backgroundtaskmanagement/.tmp-x",
        ] {
            assert_eq!(
                startup_relevant(Path::new(path), home),
                StartupChange::No,
                "{path}"
            );
        }
        for path in [
            "/Library/LaunchAgents/com.example.agent.plist",
            "/System/Volumes/Data/Library/LaunchDaemons/com.example.daemon.plist",
            "/Users/u/Library/LaunchAgents/com.example.agent.plist",
            "/Users/u/Library/LaunchAgents",
            "/Users/u/Library",
            "/Library/Preferences/com.apple.loginwindow.plist",
            "/Users/u/Library/Preferences/com.apple.loginwindow.plist.Xa12b",
            "/Users/u/Library/Preferences/com.apple.preferences.extensions.FinderSync.plist",
            "/private/etc/rc.common",
            "/private/etc/zshrc",
            "/private/etc/periodic/daily/999.local",
            "/private/etc/emond.d/rules/x.plist",
            "/Applications",
            "/Applications/Zed.app",
            "/Applications/Utilities/Tool.app",
            "/Applications/Zed.app/Contents/Library/LoginItems/Helper.app",
            "/Library/SystemExtensions/db.plist",
        ] {
            assert!(check(path), "{path}");
        }
        for path in [
            "/Users/u/Library/Preferences/com.apple.finder.plist",
            "/Users/u/Library/Preferences/com.example.app.plist",
            "/Library/Preferences/com.apple.TimeMachine.plist",
            "/private/etc/hosts",
            "/private/etc/resolv.conf",
            "/Users/u/Downloads/file.zip",
            "/Applications/.DS_Store",
            "/Applications/.opennavo-updates.noindex/0123/previous.app",
            "/Applications/Zed.app/Contents/MacOS/zed",
            "/Applications/Zed.app/Contents/Frameworks/Sparkle.framework/Versions/B/Sparkle",
            "/Users/u/Library/Logs/com.opennavo.desktop/OpenNavo.log",
            "/Users/u/Library/Application Support/com.opennavo.desktop/opennavo.db-wal",
        ] {
            assert!(!check(path), "{path}");
        }
    }
}
