//! Keep untrusted scans away from the live application's data, before open(2).
//! In particular, closing ANY fd for a SQLite inode drops this process's POSIX
//! locks. Checking the header or rejecting the fd after opening is already too late.
use super::{Service, files};
use crate::AppError;
use std::{
    collections::BTreeSet,
    fs::{self, File},
    os::unix::fs::MetadataExt,
    path::{Path, PathBuf},
};

pub(super) struct OwnData {
    roots: Vec<PathBuf>,
    directories: BTreeSet<(u64, u64)>,
    sqlite: Vec<PathBuf>,
    sqlite_inodes: BTreeSet<(u64, u64)>,
}

impl Service {
    pub(super) fn own_data(&self) -> Result<OwnData, AppError> {
        // sqlite3_db_filename only reads connection metadata. Copy while holding
        // the existing connection lock; never open a second fd to inspect SQLite.
        let database = self.database.with(|c| Ok(c.path().map(PathBuf::from)))?;
        OwnData::new(database.as_deref(), &self.data)
    }

    /// Internal installer caches and mounted payloads live inside app data and
    /// must remain readable, but never through an alias of a live SQLite file.
    pub(super) fn sqlite_files(&self) -> Result<OwnData, AppError> {
        let database = self.database.with(|c| Ok(c.path().map(PathBuf::from)))?;
        OwnData::sqlite(database.as_deref())
    }
}

fn protected() -> AppError {
    AppError::new("E_PROTECTED", "security_own_data")
}

impl OwnData {
    fn new(database: Option<&Path>, security: &Path) -> Result<Self, AppError> {
        let mut result = Self::sqlite(database)?;
        result.roots.push(security.to_owned());
        if let Some(database) = result.sqlite.first() {
            result
                .roots
                .push(database.parent().ok_or_else(protected)?.to_owned());
        }
        for root in &result.roots {
            if let Some(m) = metadata_if_present(root)? {
                // Follow only the trusted root's directory alias for its identity;
                // scan candidates still reject every symlink component.
                let m = if m.is_symlink() {
                    fs::metadata(root)?
                } else {
                    m
                };
                if !m.is_dir() {
                    return Err(protected());
                }
                result.directories.insert((m.dev(), m.ino()));
            }
        }
        Ok(result)
    }

    fn sqlite(database: Option<&Path>) -> Result<Self, AppError> {
        let mut sqlite = vec![];
        if let Some(database) = database.filter(|p| !p.as_os_str().is_empty()) {
            if !database.is_absolute() {
                return Err(AppError::new("E_PROTECTED", "security_database_path"));
            }
            for suffix in ["", "-wal", "-shm", "-journal"] {
                let mut path = database.as_os_str().to_owned();
                path.push(suffix);
                sqlite.push(PathBuf::from(path));
            }
        }
        let mut result = Self {
            roots: vec![],
            directories: BTreeSet::new(),
            sqlite,
            sqlite_inodes: BTreeSet::new(),
        };
        for path in &result.sqlite {
            if let Some(m) = metadata_if_present(path)? {
                result.sqlite_inodes.insert((m.dev(), m.ino()));
            }
        }
        Ok(result)
    }

    pub(super) fn roots(&self) -> &[PathBuf] {
        &self.roots
    }

    /// Only stat/lstat: no file headers, canonicalize, or file descriptors.
    pub(super) fn check(&self, path: &Path) -> Result<(), AppError> {
        self.checked_metadata(path).map(|_| ())
    }

    pub(super) fn checked_metadata(&self, path: &Path) -> Result<fs::Metadata, AppError> {
        if self.roots.iter().any(|root| path.starts_with(root)) {
            return Err(protected());
        }
        // One ancestry pass applies both filesystem and own-data guards.
        files::safe_metadata_with(path, &|m| {
            self.check_inode(m.dev(), m.ino(), m.is_dir(), m.nlink())
        })
    }

    fn check_directory(&self, device: u64, inode: u64) -> Result<(), AppError> {
        if self.directories.contains(&(device, inode)) {
            return Err(protected());
        }
        // A previously absent security directory may have been created meanwhile.
        for root in &self.roots {
            if let Some(m) = metadata_if_present(root)?
                && (m.dev(), m.ino()) == (device, inode)
            {
                return Err(protected());
            }
        }
        Ok(())
    }

    /// Repeated against fstatat's result immediately BEFORE each openat, not
    /// fstat on the opened fd. Directory identities cover case/volume aliases.
    pub(super) fn check_inode(
        &self,
        device: u64,
        inode: u64,
        directory: bool,
        links: u64,
    ) -> Result<(), AppError> {
        if directory {
            return self.check_directory(device, inode);
        }
        let identity = (device, inode);
        if self.sqlite_inodes.contains(&identity) {
            return Err(protected());
        }
        // WAL/SHM can be created or recreated after a scan begins. Refresh their
        // identities from metadata, and keep the initial ones for unlinked files.
        for path in &self.sqlite {
            if let Some(m) = metadata_if_present(path)?
                && (m.dev(), m.ino()) == identity
            {
                return Err(protected());
            }
        }
        if links > 1 {
            // Hard links to any other app data also stay excluded. Only this rare
            // case needs a metadata-only search; ordinary files never walk data.
            let mut stack = self.roots.clone();
            let mut seen = BTreeSet::new();
            while let Some(path) = stack.pop() {
                let Some(m) = metadata_if_present(&path)? else {
                    continue;
                };
                if (m.dev(), m.ino()) == identity {
                    return Err(protected());
                }
                if m.is_dir() && !files::dataless(&m) && seen.insert((m.dev(), m.ino())) {
                    for entry in fs::read_dir(path)? {
                        stack.push(entry?.path());
                    }
                }
            }
        }
        Ok(())
    }

    pub(super) fn scan_limit(&self, path: &Path) -> Result<u64, AppError> {
        files::scan_limit_guarded(path, Some(self))
    }

    pub(super) fn open_read(&self, path: &Path, limit: u64) -> Result<File, AppError> {
        files::open_read_guarded(path, limit, Some(self))
    }
}

fn metadata_if_present(path: &Path) -> Result<Option<fs::Metadata>, AppError> {
    match fs::symlink_metadata(path) {
        Ok(m) => Ok(Some(m)),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(e) => Err(e.into()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{os::fd::AsRawFd, process::Command, sync::atomic::AtomicBool};

    fn lock(file: &File) {
        let mut lock: nix::libc::flock = unsafe { std::mem::zeroed() };
        lock.l_type = nix::libc::F_WRLCK;
        lock.l_whence = nix::libc::SEEK_SET as i16;
        lock.l_len = 1;
        assert_eq!(
            unsafe { nix::libc::fcntl(file.as_raw_fd(), nix::libc::F_SETLK, &lock) },
            0
        );
    }

    // A separate process must still fail to acquire each POSIX record lock.
    // Merely asserting that the AV visitor was not called misses magic reads.
    fn assert_locked(paths: &[PathBuf]) {
        assert_locked_at(paths, 0);
    }

    fn assert_locked_at(paths: &[PathBuf], offset: u64) {
        let output = Command::new("/usr/bin/python3")
            .args([
                "-c",
                r#"
import fcntl, os, sys
for path in sys.argv[1:-1]:
    fd = os.open(path, os.O_RDWR)
    try:
        fcntl.lockf(fd, fcntl.LOCK_EX | fcntl.LOCK_NB, 1, int(sys.argv[-1]))
    except BlockingIOError:
        pass
    else:
        raise AssertionError('Scanner released the POSIX lock: ' + path)
    finally:
        os.close(fd)
"#,
            ])
            .args(paths)
            .arg(offset.to_string())
            .output()
            .unwrap();
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
    }

    #[test]
    fn aliases_are_excluded_before_open_and_preserve_posix_locks() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let data = root.join("Application Support/com.OpenNavo.Independent");
        fs::create_dir_all(&data).unwrap();
        let database = data.join("opennavo.db");
        let main = File::create(&database).unwrap();
        main.set_len(512).unwrap();
        lock(&main);
        let guard = OwnData::new(Some(&database), &data.join("logs/security")).unwrap();

        // Sidecars deliberately appear AFTER the guard is created.
        let mut locked = vec![main];
        let mut originals = vec![database.clone()];
        for name in [
            "opennavo.db-wal",
            "opennavo.db-shm",
            "opennavo.db-journal",
            "settings.json",
        ] {
            let path = data.join(name);
            let file = File::create(&path).unwrap();
            file.set_len(512).unwrap();
            lock(&file);
            originals.push(path);
            locked.push(file);
        }
        assert_locked(&originals);
        let mut paths = originals.clone();
        for (index, path) in originals.iter().enumerate() {
            let alias = root.join(format!("renamed-{index}"));
            fs::hard_link(path, &alias).unwrap();
            paths.push(alias);
        }
        // Case-insensitive APFS and the macOS Data volume expose the same inode
        // through paths that do not have the protected lexical prefix.
        for alias in [
            data.with_file_name("com.opennavo.independent")
                .join("opennavo.db"),
            Path::new("/System/Volumes/Data").join(database.strip_prefix("/").unwrap()),
        ] {
            if fs::symlink_metadata(&alias).is_ok_and(|m| {
                (m.dev(), m.ino())
                    == (
                        locked[0].metadata().unwrap().dev(),
                        locked[0].metadata().unwrap().ino(),
                    )
            }) {
                paths.push(alias);
            }
        }
        for path in &paths {
            assert_eq!(
                guard.scan_limit(path).unwrap_err().message,
                "security_own_data",
                "{}",
                path.display()
            );
            assert_eq!(
                guard.open_read(path, 1024).unwrap_err().message,
                "security_own_data"
            );
        }
        let alias = root.join("directory-alias");
        std::os::unix::fs::symlink(&data, &alias).unwrap();
        paths.push(alias.join("opennavo.db"));
        let alias = root.join("file-alias");
        std::os::unix::fs::symlink(&database, &alias).unwrap();
        paths.push(alias);
        let good = root.join("ordinary");
        fs::write(&good, "safe").unwrap();
        // Unrelated hard links remain scannable, not blanket-excluded.
        fs::hard_link(&good, root.join("ordinary-hardlink")).unwrap();
        paths.push(good.clone());
        let mut skipped = super::super::types::AvSkipped::default();
        let mut visited = vec![];
        super::super::scan::walk_guarded(
            paths,
            &[],
            &AtomicBool::new(false),
            &mut skipped,
            &|_, m| files::dataless(m),
            &mut |path| {
                let _file = guard.open_read(path, 1024)?;
                visited.push(path.to_owned());
                Ok(())
            },
            Some(&guard),
        )
        .unwrap();
        assert_eq!(visited, vec![good]);
        assert!(skipped.excluded >= 11);
        assert_locked(&originals);
        // Prove the canary detects the precise failure: even opening then
        // closing a different hard link drops the parent process's record lock.
        drop(File::open(root.join("renamed-0")).unwrap());
        let result = Command::new("/usr/bin/python3")
            .args(["-c", "import fcntl,sys; f=open(sys.argv[1],'r+'); fcntl.lockf(f,fcntl.LOCK_EX|fcntl.LOCK_NB,1)"])
            .arg(&database).status().unwrap();
        assert!(result.success());
    }

    #[test]
    fn explicit_verification_cannot_read_own_data_even_without_av_consent() {
        let (_temp, service, _) = super::super::tests::fixture();
        let database = service
            .database
            .with(|c| Ok(PathBuf::from(c.path().unwrap())))
            .unwrap();
        let alias = service.home.join("download.zip");
        fs::hard_link(&database, &alias).unwrap();
        for path in [database, alias] {
            assert_eq!(
                service
                    .download_verify(path.clone(), None)
                    .unwrap_err()
                    .message,
                "security_own_data"
            );
            assert_eq!(
                service
                    .download_detected(&path, "fixture".into())
                    .unwrap_err()
                    .message,
                "security_own_data"
            );
        }
    }

    #[test]
    fn installer_reads_preserve_sqlite_locks_inside_the_private_cache() {
        let (_temp, service, _) = super::super::tests::fixture();
        let database = service
            .database
            .with(|c| Ok(PathBuf::from(c.path().unwrap())))
            .unwrap();
        let shm = database.with_file_name("opennavo.db-shm");
        let cache = service.data.join("install-cache/fixture");
        fs::create_dir_all(&cache).unwrap();
        for suffix in ["-shm", "-wal", ""] {
            let source = database.with_file_name(format!("opennavo.db{suffix}"));
            let alias = cache.join("download.zip");
            fs::hard_link(&source, &alias).unwrap();
            assert_locked_at(std::slice::from_ref(&shm), 128);
            let result = service.scan_installer_file(&alias, &AtomicBool::new(false));
            assert_locked_at(std::slice::from_ref(&shm), 128);
            assert_eq!(result.err().unwrap().message, "security_own_data");
            fs::remove_file(alias).unwrap();
        }
    }

    #[test]
    fn preinstall_cache_walk_preserves_sqlite_locks() {
        use crate::{brew::pty::Execution, model::TaskOp};
        use std::{os::unix::fs::PermissionsExt, sync::Arc, time::Duration};
        let (_temp, service, _) = super::super::tests::fixture();
        super::super::tests::fake_engine(&service);
        service.settings.write().unwrap().av_consent = true;
        let database = service
            .database
            .with(|c| Ok(PathBuf::from(c.path().unwrap())))
            .unwrap();
        let shm = database.with_file_name("opennavo.db-shm");
        let brew = service.home.join("fake-brew");
        fs::write(
            &brew,
            r#"#!/usr/bin/env python3
import os, pathlib, sys
if sys.argv[1] == 'fetch':
    root = pathlib.Path(os.environ['HOMEBREW_CACHE'])
    os.link(os.environ['FIXTURE_SHM'], root / 'alias.zip')
    (root / 'ordinary.zip').write_text('safe')
"#,
        )
        .unwrap();
        fs::set_permissions(&brew, fs::Permissions::from_mode(0o700)).unwrap();
        let mut request = Execution {
            executable: brew,
            args: vec!["install".into(), "--formula".into(), "fixture".into()],
            env: std::collections::BTreeMap::from([
                ("PATH".into(), "/usr/bin:/bin".into()),
                ("FIXTURE_SHM".into(), shm.to_string_lossy().into_owned()),
            ]),
            log_path: service.home.join("task.log"),
            op: TaskOp::Install,
            cancel: Arc::new(AtomicBool::new(false)),
            grace: Duration::from_millis(100),
            cache_dir: None,
            download_total: None,
        };
        assert_locked_at(std::slice::from_ref(&shm), 128);
        let id = uuid::Uuid::now_v7().to_string();
        let prepared = service.prepare_install(&id, &mut request, |_, _| {});
        assert_locked_at(std::slice::from_ref(&shm), 128);
        let prepared = prepared.unwrap().unwrap();
        let result = service.install_scan_get(&id).unwrap().unwrap();
        assert_eq!(result.files_scanned, 1);
        assert_eq!(
            result.av_skipped,
            Some(super::super::AvSkipReason::Excluded)
        );
        prepared.revalidate(&AtomicBool::new(false)).unwrap();
        assert_locked_at(&[shm], 128);
    }

    #[test]
    fn startup_lsof_and_ps_probes_preserve_own_data_locks() {
        use super::super::{Commands, startup};
        use std::sync::Arc;
        struct ProcessFiles(Vec<PathBuf>);
        impl Commands for ProcessFiles {
            fn run(&self, program: &str, _: &[&str]) -> Result<String, AppError> {
                let prefix = match program {
                    "/usr/sbin/lsof" => "n",
                    "/bin/ps" => "",
                    _ => return Ok(String::new()),
                };
                Ok(self
                    .0
                    .iter()
                    .map(|p| format!("{prefix}{}\n", p.display()))
                    .collect())
            }
        }
        let (temp, mut service, _) = super::super::tests::fixture();
        let database = service
            .database
            .with(|c| Ok(PathBuf::from(c.path().unwrap())))
            .unwrap();
        let canary = database.parent().unwrap().join("locked-data");
        let sidecar = |suffix: &str| {
            let mut path = database.as_os_str().to_owned();
            path.push(suffix);
            PathBuf::from(path)
        };
        let wal = sidecar("-wal");
        let shm = sidecar("-shm");
        // SQLite holds the WAL dead-man-switch lock at byte 128. Opening and
        // closing SHM for a Mach-O header probe drops that real lock, allowing
        // another SQLite process to truncate our still-mapped WAL index.
        // Only the child opens SHM; the database-owning process must not do so.
        assert_locked_at(std::slice::from_ref(&shm), 128);
        let file = File::create(&canary).unwrap();
        file.set_len(512).unwrap();
        lock(&file);
        let alias = service.home.join("aliased-data");
        fs::hard_link(&canary, &alias).unwrap();
        let mut candidates = vec![
            database.clone(),
            wal.clone(),
            shm.clone(),
            canary.clone(),
            alias,
        ];
        for (index, source) in [&database, &wal, &shm].into_iter().enumerate() {
            let alias = service.home.join(format!("sqlite-alias-{index}"));
            fs::hard_link(source, &alias).unwrap();
            candidates.push(alias);
        }
        Arc::get_mut(&mut service).unwrap().commands = Arc::new(ProcessFiles(candidates));
        let roots = startup::Roots {
            home: service.home.clone(),
            root: temp.path().canonicalize().unwrap().join("system"),
            applications: vec![],
        };
        let before = fs::metadata(&shm).unwrap();
        for round in 0..3 {
            let value = round.to_string();
            service.database.set_meta("lock_probe", &value).unwrap();
            let snapshot = service.startup_scan_at(&roots).unwrap();
            assert!(snapshot.items.is_empty());
            assert_locked(std::slice::from_ref(&canary));
            // Fail on lock loss before launching SQLite: otherwise this regression
            // could deliberately truncate the parent's mapping and crash libtest.
            assert_locked_at(std::slice::from_ref(&shm), 128);
            let reader = Command::new("/usr/bin/sqlite3")
                .args(["-batch", "-readonly"])
                .arg(&database)
                .arg("SELECT count(*) FROM tasks;")
                .output()
                .unwrap();
            assert!(
                reader.status.success(),
                "{}",
                String::from_utf8_lossy(&reader.stderr)
            );
            assert_eq!(String::from_utf8_lossy(&reader.stdout).trim(), "0");
            let after = fs::metadata(&shm).unwrap();
            assert_eq!(
                (after.dev(), after.ino(), after.len()),
                (before.dev(), before.ino(), before.len())
            );
            assert_locked_at(std::slice::from_ref(&shm), 128);
            assert_eq!(
                service.database.meta("lock_probe").unwrap().as_deref(),
                Some(value.as_str())
            );
        }
        assert_eq!(
            service
                .database
                .with(|c| Ok(c.query_row("PRAGMA integrity_check", [], |r| r.get::<_, String>(0))?))
                .unwrap(),
            "ok"
        );
    }
    #[test]
    fn live_startup_download_btm_and_bundle_reads_preserve_data_locks() {
        use super::super::{signature, startup};
        let (temp, service, _) = super::super::tests::fixture();
        let root = temp.path().canonicalize().unwrap();
        let database = service
            .database
            .with(|c| Ok(PathBuf::from(c.path().unwrap())))
            .unwrap();
        let canary = database.parent().unwrap().join("locked-settings");
        let file = File::create(&canary).unwrap();
        file.set_len(512).unwrap();
        lock(&file);
        let db_lock = fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(&database)
            .unwrap();
        lock(&db_lock);
        let guard = service.own_data().unwrap();
        let applications = root.join("Applications");
        let app = applications.join("Fixture.app");
        let candidates = [
            service.home.join("Library/LaunchAgents/hostile.plist"),
            service
                .home
                .join("Library/Application Support/Google/Chrome/Default/Preferences"),
            app.join("Contents/Info.plist"),
            root.join(
                "system/private/var/db/com.apple.backgroundtaskmanagement/BackgroundItems-v18.btm",
            ),
            service.home.join("Downloads/installer.pkg"),
        ];
        for path in &candidates {
            fs::create_dir_all(path.parent().unwrap()).unwrap();
            fs::hard_link(
                if path == &candidates[0] {
                    &database
                } else {
                    &canary
                },
                path,
            )
            .unwrap();
        }
        let good = service.home.join("Library/LaunchAgents/good.plist");
        fs::write(&good, r#"<plist><dict><key>Label</key><string>org.fixture.good</string><key>Program</key><string>/usr/bin/true</string><key>RunAtLoad</key><true/></dict></plist>"#).unwrap();
        let scan = service
            .startup_scan_at(&startup::Roots {
                home: service.home.clone(),
                root: root.join("system"),
                applications: vec![applications],
            })
            .unwrap();
        assert!(
            scan.items
                .iter()
                .any(|i| i.label.as_deref() == Some("org.fixture.good"))
        );
        assert_locked(&[canary.clone(), database.clone()]);
        assert!(service.recent_download_token(&candidates[4]).is_err());
        // Initial watcher and event callback both use this guarded fingerprint.
        for _ in 0..2 {
            assert!(startup::btm_fingerprint(&candidates[3], Some(&guard)).is_none());
        }
        assert_eq!(
            signature::inspect_guarded(&app, Some(&guard))
                .unwrap()
                .status,
            super::super::SignatureStatus::Unknown
        );
        fs::remove_file(&candidates[2]).unwrap();
        fs::write(
            &candidates[2],
            r#"<plist><dict><key>CFBundleExecutable</key><string>main</string></dict></plist>"#,
        )
        .unwrap();
        fs::create_dir_all(app.join("Contents/MacOS")).unwrap();
        fs::hard_link(&canary, app.join("Contents/MacOS/main")).unwrap();
        assert_eq!(
            signature::inspect_guarded(&app, Some(&guard))
                .unwrap()
                .status,
            super::super::SignatureStatus::Unknown
        );
        fs::remove_file(app.join("Contents/MacOS/main")).unwrap();
        fs::copy("/usr/bin/true", app.join("Contents/MacOS/main")).unwrap();
        fs::create_dir_all(app.join("Contents/Resources")).unwrap();
        fs::hard_link(&canary, app.join("Contents/Resources/innocent.txt")).unwrap();
        assert_eq!(
            signature::inspect_guarded(&app, Some(&guard))
                .unwrap()
                .status,
            super::super::SignatureStatus::Unknown
        );
        assert_locked(&[canary, database]);
    }
}
