// SPDX-License-Identifier: GPL-3.0-only
pub mod admin;
pub mod app_signatures;
pub mod av;
pub mod command;
mod cron_journal;
pub mod download;
pub mod files;
mod install_progress;
pub mod legacy_startup;
pub mod mounted;
pub mod notify;
mod overview;
mod own_data;
pub mod paste;
pub mod posture;
pub mod preinstall;
pub mod quarantine;
pub mod s2_types;
pub mod scan;
mod scan_policy;
pub mod schedule;
pub mod signature;
pub mod virustotal;
pub use s2_types::*;
pub mod startup;
pub mod types;
pub mod watch;
use crate::{
    AppError,
    db::Database,
    events::{CoreEvent, EventSink},
    model::Settings,
};
use rusqlite::{OptionalExtension, params};
use std::{
    path::{Path, PathBuf},
    sync::{
        Arc, Mutex, RwLock,
        atomic::{AtomicBool, Ordering},
    },
};
pub use types::*;

pub(crate) fn poison<T>(_: T) -> AppError {
    AppError::new("E_UNKNOWN", "security_lock")
}
pub(crate) fn now() -> i64 {
    chrono::Utc::now().timestamp_millis()
}
pub(crate) fn requires_admin(path: &Path) -> bool {
    requires_admin_to(path, path)
}
pub(crate) fn requires_admin_to(source: &Path, destination: &Path) -> bool {
    use std::os::unix::fs::MetadataExt;
    let uid = unsafe { nix::libc::geteuid() };
    let file = files::safe_metadata(source).ok();
    let parent = destination
        .parent()
        .and_then(|p| files::safe_metadata(p).ok());
    file.zip(parent).is_none_or(|(file, parent)| {
        mutation_requires_admin(file.uid(), parent.uid(), parent.mode(), uid)
    })
}
// chmod requires ownership even when rename is allowed by the containing directory.
fn mutation_requires_admin(file_uid: u32, parent_uid: u32, parent_mode: u32, uid: u32) -> bool {
    file_uid != uid || parent_uid != uid || parent_mode & 0o300 != 0o300
}
/// Fixed program/argument execution is injectable for read-only checks and fixture launchd tests.
pub trait Commands: Send + Sync {
    fn output(&self, program: &str, args: &[&str]) -> Result<command::Output, AppError> {
        self.run(program, args).map(|stdout| command::Output {
            code: Some(0),
            stdout,
            stderr: String::new(),
        })
    }
    fn crontab_read(&self) -> Result<Option<String>, AppError> {
        self.run("/usr/bin/crontab", &["-l"]).map(Some)
    }
    fn crontab_write(&self, _: &str) -> Result<(), AppError> {
        Err(AppError::new("E_NOT_IMPLEMENTED", "cron_write_backend"))
    }
    fn login_read(&self, _: &str) -> Result<legacy_startup::LoginItem, AppError> {
        Err(AppError::new("E_NOT_IMPLEMENTED", "login_backend"))
    }
    fn login_remove(&self, _: &legacy_startup::LoginItem) -> Result<(), AppError> {
        Err(AppError::new("E_NOT_IMPLEMENTED", "login_backend"))
    }
    fn login_restore(&self, _: &legacy_startup::LoginItem) -> Result<(), AppError> {
        Err(AppError::new("E_NOT_IMPLEMENTED", "login_backend"))
    }

    fn login_items(&self) -> Result<Vec<PathBuf>, AppError> {
        Ok(vec![])
    }
    /// The app LaunchServices would open for a bundle identifier.
    fn app_for_bundle(&self, _bundle_id: &str) -> Option<PathBuf> {
        None
    }
    /// The current user's UUID, which names their BTM store. `None` treats every store as
    /// theirs, as with a single-user fixture.
    fn user_uuid(&self) -> Option<String> {
        None
    }
    fn run(&self, program: &str, args: &[&str]) -> Result<String, AppError>;
    fn bootout(&self, label: &str, domain: &str, path: &str) -> Result<(), AppError> {
        if self
            .run("/bin/launchctl", &["print", &format!("{domain}/{label}")])
            .is_ok()
        {
            self.run("/bin/launchctl", &["bootout", domain, path])?;
        }
        Ok(())
    }
}
pub struct NativeCommands;
impl Commands for NativeCommands {
    fn output(&self, program: &str, args: &[&str]) -> Result<command::Output, AppError> {
        command::output(program, args)
    }
    fn crontab_read(&self) -> Result<Option<String>, AppError> {
        legacy_startup::crontab_read()
    }
    fn crontab_write(&self, text: &str) -> Result<(), AppError> {
        legacy_startup::crontab_write(text)
    }
    fn login_read(&self, path: &str) -> Result<legacy_startup::LoginItem, AppError> {
        legacy_startup::login(
            "read",
            &legacy_startup::LoginItem {
                name: String::new(),
                path: path.into(),
                hidden: false,
            },
        )
    }
    fn login_remove(&self, item: &legacy_startup::LoginItem) -> Result<(), AppError> {
        legacy_startup::login("remove", item).map(|_| ())
    }
    fn login_restore(&self, item: &legacy_startup::LoginItem) -> Result<(), AppError> {
        legacy_startup::login("restore", item).map(|_| ())
    }

    fn login_items(&self) -> Result<Vec<PathBuf>, AppError> {
        startup::plugins::login_items()
    }
    fn app_for_bundle(&self, bundle_id: &str) -> Option<PathBuf> {
        if !crate::uninstall::identity::valid_identifier(bundle_id) {
            return None;
        }
        #[cfg(target_os = "macos")]
        return objc2::rc::autoreleasepool(|_| {
            let url = objc2_app_kit::NSWorkspace::sharedWorkspace()
                .URLForApplicationWithBundleIdentifier(&objc2_foundation::NSString::from_str(
                    bundle_id,
                ))?;
            Some(PathBuf::from(url.path()?.to_string()))
        });
        #[cfg(not(target_os = "macos"))]
        None
    }
    fn user_uuid(&self) -> Option<String> {
        #[cfg(target_os = "macos")]
        {
            unsafe extern "C" {
                fn mbr_uid_to_uuid(uid: nix::libc::uid_t, uu: *mut u8) -> nix::libc::c_int;
            }
            let mut uuid = [0u8; 16];
            if unsafe { mbr_uid_to_uuid(nix::libc::getuid(), uuid.as_mut_ptr()) } != 0 {
                return None;
            }
            Some(
                uuid::Uuid::from_bytes(uuid)
                    .hyphenated()
                    .encode_upper(&mut uuid::Uuid::encode_buffer())
                    .to_owned(),
            )
        }
        #[cfg(not(target_os = "macos"))]
        None
    }
    fn bootout(&self, label: &str, domain: &str, path: &str) -> Result<(), AppError> {
        if crate::uninstall::identity::valid_identifier(label)
            && (domain == "system" || domain == crate::uninstall::identity::user_domain())
        {
            crate::uninstall::execute::bootout(label, domain == "system", path)
        } else {
            // A privileged batch targets the requesting user's GUI, not root's GUI.
            if self
                .run("/bin/launchctl", &["print", &format!("{domain}/{label}")])
                .is_ok()
            {
                self.run("/bin/launchctl", &["bootout", domain, path])?;
            }
            Ok(())
        }
    }
    fn run(&self, program: &str, args: &[&str]) -> Result<String, AppError> {
        crate::uninstall::identity::command(program, args, std::time::Duration::from_secs(10))
    }
}
pub struct Service {
    pub database: Database,
    pub home: PathBuf,
    pub data: PathBuf,
    pub av: Arc<av::Engine>,
    pub settings: Arc<RwLock<Settings>>,
    pub sink: EventSink,
    pub commands: Arc<dyn Commands>,
    pub(crate) active_scan: Mutex<Option<(String, Arc<AtomicBool>)>>,
    pub(crate) mutation: Mutex<()>,
    pub(crate) scheduled: Mutex<()>,
    pub(crate) paste_pending: Mutex<Option<paste::Pending>>,
    pub vt_secrets: Arc<dyn virustotal::Secrets>,
    pub(crate) vt_serial: tokio::sync::Mutex<()>,
    pub(crate) signature_audit: Mutex<()>,
    pub(crate) downloads_session: Mutex<std::collections::BTreeMap<PathBuf, download::Session>>,
    pub admin: RwLock<Option<Arc<dyn admin::Actions>>>,
    /// Where the last startup scan looked; actions revalidate against the same roots.
    pub(crate) last_roots: Mutex<Option<startup::Roots>>,
    /// Apps just added to open at login here, which the next scan must not report as new.
    pub(crate) added_logins: Mutex<std::collections::BTreeSet<PathBuf>>,
}
impl Drop for Service {
    fn drop(&mut self) {
        self.shutdown();
    }
}
impl Service {
    pub(crate) fn shutdown(&self) {
        if let Some((_, cancel)) = self
            .active_scan
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .as_ref()
        {
            cancel.store(true, Ordering::Release);
        }
        self.av.shutdown();
    }
    pub fn new(
        database: Database,
        settings: Arc<RwLock<Settings>>,
        sink: EventSink,
        home: PathBuf,
        data: PathBuf,
        config: av::Config,
        commands: Arc<dyn Commands>,
    ) -> Arc<Self> {
        let service = Arc::new(Self {
            av: av::Engine::new(config, database.clone(), settings.clone(), sink.clone()),
            database,
            settings,
            sink,
            home,
            data,
            commands,
            active_scan: Mutex::new(None),
            mutation: Mutex::new(()),
            scheduled: Mutex::new(()),
            paste_pending: Mutex::new(None),
            vt_secrets: Arc::new(virustotal::Keychain),
            vt_serial: tokio::sync::Mutex::new(()),
            signature_audit: Mutex::new(()),
            downloads_session: Mutex::new(Default::default()),
            admin: RwLock::new(None),
            last_roots: Mutex::new(None),
            added_logins: Mutex::new(Default::default()),
        });
        // Establish the initial schedule boundary before a long startup inventory
        // can delay the watcher's first tick. Existing plans retain their epoch.
        if let Err(error) = service.schedule_status() {
            log::warn!("Unable to initialize AV schedule: {error}");
        }
        service
    }
    pub fn changed(&self, reason: SecurityChangeReason) {
        (self.sink)(CoreEvent::SecurityChanged(SecurityChanged { reason }));
    }
    pub fn record_event(
        &self,
        kind: ProtectionEventKind,
        path: &str,
        item_id: Option<String>,
        handled: bool,
    ) -> Result<(), AppError> {
        let event = ProtectionEvent {
            id: uuid::Uuid::now_v7().to_string(),
            at: now(),
            kind,
            name: Path::new(path)
                .file_name()
                .unwrap_or_default()
                .to_string_lossy()
                .into_owned(),
            path: Some(path.to_owned()),
            item_id,
            handled,
        };
        self.database.with(|c| {
            c.execute(
                "INSERT INTO protection_events(id,at,value) VALUES(?1,?2,?3)",
                params![event.id, event.at, serde_json::to_string(&event)?],
            )?;
            Ok(())
        })?;
        self.changed(SecurityChangeReason::Events);
        Ok(())
    }
    pub fn events(&self, limit: u32) -> Result<Vec<ProtectionEvent>, AppError> {
        if !(1..=500).contains(&limit) {
            return Err(AppError::new("E_INVALID_ARG", "protection_events_limit"));
        }
        self.database.with(|c| {
            let mut s =
                c.prepare("SELECT value FROM protection_events ORDER BY at DESC,id DESC LIMIT ?1")?;
            let values = s
                .query_map([limit], |r| r.get::<_, String>(0))?
                .collect::<rusqlite::Result<Vec<_>>>()?;
            values
                .into_iter()
                .map(|v| Ok(serde_json::from_str(&v)?))
                .collect()
        })
    }
    pub(crate) fn events_handled(&self, item_id: &str, handled: bool) -> Result<(), AppError> {
        self.database.with(|c| {
            c.execute("UPDATE protection_events SET value=json_set(value,'$.handled',json(?2)) WHERE json_extract(value,'$.itemId')=?1",
                params![item_id, if handled { "true" } else { "false" }])?;
            Ok(())
        })
    }
    pub fn saved_startup(&self) -> Result<Vec<StartupItem>, AppError> {
        self.database.with(|c| {
            let mut s = c.prepare("SELECT value FROM security_startup ORDER BY id")?;
            let rows = s
                .query_map([], |r| r.get::<_, String>(0))?
                .collect::<rusqlite::Result<Vec<_>>>()?;
            rows.into_iter()
                .map(|v| Ok(serde_json::from_str(&v)?))
                .collect()
        })
    }
    pub fn save_scan(&self, result: &AvScanResult) -> Result<(), AppError> {
        self.database.with(|c|{c.execute("INSERT INTO security_scans(id,started_at,result) VALUES(?1,?2,?3) ON CONFLICT(id) DO UPDATE SET result=excluded.result",params![result.scan_id,result.started_at,serde_json::to_string(result)?])?;Ok(())})
    }
    pub fn scan_get(&self, id: &str) -> Result<AvScanResult, AppError> {
        let is_active = self
            .active_scan
            .lock()
            .map_err(poison)?
            .as_ref()
            .is_some_and(|(active, _)| active == id);
        self.database.with(|c| {
            let json = c
                .query_row("SELECT result FROM security_scans WHERE id=?1", [id], |r| {
                    r.get::<_, String>(0)
                })
                .optional()?
                .ok_or_else(|| AppError::new("E_NOT_FOUND", "security_scan"))?;
            let mut result: AvScanResult = serde_json::from_str(&json)?;
            // Finding state can change while the scanner is still persisting progress.
            let mut s =
                c.prepare("SELECT value FROM security_findings WHERE scan_id=?1 ORDER BY id")?;
            let values = s
                .query_map([id], |r| r.get::<_, String>(0))?
                .collect::<rusqlite::Result<Vec<_>>>()?;
            result.findings = values
                .into_iter()
                .map(|v| serde_json::from_str(&v))
                .collect::<Result<_, _>>()?;
            if result.state == AvScanState::Running && !is_active {
                result.state = AvScanState::Failed;
                result.error_code = Some("E_AV_SCAN_FAILED".into());
                log::warn!(
                    "Security scan {} interrupted before completion",
                    result.scan_id
                );
                c.execute(
                    "UPDATE security_scans SET result=?2 WHERE id=?1",
                    params![id, serde_json::to_string(&result)?],
                )?;
            }
            Ok(result)
        })
    }
    pub fn scan_cancel(&self, id: &str) -> Result<(), AppError> {
        if let Some((active, cancel)) = self.active_scan.lock().map_err(poison)?.as_ref()
            && active == id
        {
            cancel.store(true, Ordering::Release);
            return Ok(());
        }
        Err(AppError::new("E_NOT_FOUND", "security_active_scan"))
    }
    pub fn posture(&self) -> Vec<PostureCheck> {
        posture::check(self.commands.as_ref(), &self.home)
    }
}

#[cfg(test)]
mod tests;
