// SPDX-License-Identifier: GPL-3.0-only
//! Install-only read-only DMG fallback. No conversion, launch, copy or shell.
use super::install_progress::Meter;
use super::*;
use std::{
    collections::BTreeSet,
    fs,
    io::{Read, Write},
    os::unix::{fs::MetadataExt, process::CommandExt},
    process::{Command, Stdio},
    time::{Duration, Instant},
};

#[derive(Default, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct InstallerFileScan {
    pub files_scanned: u64,
    pub threats: BTreeSet<String>,
    pub skipped: Option<AvSkipReason>,
    pub logs: Vec<String>,
}
pub trait MountCommands {
    fn trace(&self, line: &str) {
        log::info!("{line}");
    }
    fn run(
        &self,
        args: &[String],
        input: &[u8],
        cancel: &AtomicBool,
    ) -> Result<(bool, Vec<u8>), AppError>;
}
pub struct NativeMountCommands;
pub(crate) fn task_log(path: &Path, line: &str) {
    TaskMountCommands(path).trace(line);
}
struct TaskMountCommands<'a>(&'a Path);
impl MountCommands for TaskMountCommands<'_> {
    fn trace(&self, line: &str) {
        use std::os::unix::fs::OpenOptionsExt;
        log::info!("{line}");
        if let Ok(mut file) = fs::OpenOptions::new()
            .append(true)
            .create(true)
            .mode(0o600)
            .custom_flags(nix::libc::O_NOFOLLOW)
            .open(self.0)
        {
            let _ = writeln!(file, "{line}");
        }
    }
    fn run(
        &self,
        args: &[String],
        input: &[u8],
        cancel: &AtomicBool,
    ) -> Result<(bool, Vec<u8>), AppError> {
        NativeMountCommands.run(args, input, cancel)
    }
}
impl MountCommands for NativeMountCommands {
    fn run(
        &self,
        args: &[String],
        input: &[u8],
        cancel: &AtomicBool,
    ) -> Result<(bool, Vec<u8>), AppError> {
        let mut child = Command::new("/usr/bin/hdiutil")
            .args(args)
            .env("LC_ALL", "C")
            .process_group(0)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .spawn()?;
        let output = child.stdout.take().unwrap();
        let reader = std::thread::spawn(move || {
            let mut bytes = vec![];
            output
                .take(8 * 1024 * 1024 + 1)
                .read_to_end(&mut bytes)
                .map(|_| bytes)
        });
        if let Some(mut stdin) = child.stdin.take() {
            let _ = stdin.write_all(input);
        }
        let start = Instant::now();
        let mut interrupted = false;
        let status = loop {
            if let Some(status) = child.try_wait()? {
                break status;
            }
            if cancel.load(Ordering::Acquire) || start.elapsed() > Duration::from_secs(60) {
                interrupted = true;
                unsafe {
                    nix::libc::kill(-(child.id() as i32), nix::libc::SIGKILL);
                }
                let _ = child.kill();
                break child.wait()?;
            }
            std::thread::sleep(Duration::from_millis(25));
        };
        let bytes = reader
            .join()
            .map_err(|_| AppError::new("E_IO", "dmg_output"))??;
        if cancel.load(Ordering::Acquire) {
            return Err(AppError::new("E_SCAN_CANCELED", "scan_canceled"));
        }
        if interrupted || bytes.len() > 8 * 1024 * 1024 {
            return Err(AppError::new("E_IO", "dmg_command_timeout_or_output_limit"));
        }
        Ok((status.success(), bytes))
    }
}
#[derive(Default)]
struct Image {
    path: PathBuf,
    devices: BTreeSet<String>,
    mounts: Vec<PathBuf>,
}
fn images(bytes: &[u8]) -> Result<Vec<Image>, AppError> {
    let invalid = || AppError::new("E_INSTALL_SCAN", "dmg_info_invalid");
    let value = plist::Value::from_reader(std::io::Cursor::new(bytes)).map_err(|_| invalid())?;
    let array = value
        .as_dictionary()
        .and_then(|d| d.get("images"))
        .and_then(plist::Value::as_array)
        .ok_or_else(invalid)?;
    let mut images = vec![];
    for value in array {
        let Some(d) = value.as_dictionary() else {
            continue;
        };
        let Some(path) = d.get("image-path").and_then(plist::Value::as_string) else {
            continue;
        };
        let mut image = Image {
            path: PathBuf::from(path),
            ..Default::default()
        };
        if let Some(entries) = d.get("system-entities").and_then(plist::Value::as_array) {
            for entry in entries.iter().filter_map(plist::Value::as_dictionary) {
                if let Some(device) = entry.get("dev-entry").and_then(plist::Value::as_string)
                    && device.strip_prefix("/dev/disk").is_some_and(|s| {
                        !s.is_empty() && s.chars().all(|c| c.is_ascii_digit() || c == 's')
                    })
                {
                    image.devices.insert(device.into());
                }
                if let Some(path) = entry.get("mount-point").and_then(plist::Value::as_string) {
                    image.mounts.push(path.into());
                }
            }
        }
        images.push(image);
    }
    Ok(images)
}
struct Mounted<'a> {
    commands: &'a dyn MountCommands,
    image: PathBuf,
    root: PathBuf,
    baseline: BTreeSet<String>,
    devices: BTreeSet<String>,
    logs: Vec<String>,
    closed: bool,
    armed: bool,
}
impl Mounted<'_> {
    fn log(&mut self, line: String) {
        self.commands.trace(&line);
        self.logs.push(line);
    }
    fn info(&self) -> Result<Vec<Image>, AppError> {
        let (ok, bytes) = self.commands.run(
            &["info".into(), "-plist".into()],
            b"",
            &AtomicBool::new(false),
        )?;
        if !ok {
            return Err(AppError::new("E_INSTALL_SCAN", "dmg_info_failed"));
        }
        images(&bytes)
    }
    fn discover(&mut self) -> Result<Vec<PathBuf>, AppError> {
        let mut mounts = vec![];
        for image in self.info()? {
            let same = image.path == self.image
                || image.path.canonicalize().ok().as_ref() == Some(&self.image);
            if !same {
                continue;
            }
            let added: BTreeSet<_> = image.devices.difference(&self.baseline).cloned().collect();
            if added.is_empty() {
                continue;
            }
            self.devices.extend(added);
            for mount in image.mounts {
                if !mount.starts_with(&self.root) {
                    return Err(AppError::new(
                        "E_INSTALL_SCAN",
                        "dmg_mount_outside_private_directory",
                    ));
                }
                mounts.push(mount);
            }
        }
        Ok(mounts)
    }
    fn cleanup(&mut self) -> Result<(), AppError> {
        if !self.armed {
            self.closed = true;
            fs::remove_dir_all(&self.root)?;
            return Ok(());
        }
        let discovered = self.discover();
        // Prefer whole devices, then partitions still reported after detaching.
        let devices = self.devices.iter().cloned().collect::<Vec<_>>();
        for device in devices {
            if !self.info()?.iter().any(|i| i.devices.contains(&device)) {
                continue;
            }
            let mut detached = false;
            for force in [false, true] {
                let mut args = vec!["detach".into()];
                if force {
                    args.push("-force".into());
                }
                args.push(device.clone());
                self.log(format!("[security] hdiutil {}", args.join(" ")));
                match self.commands.run(&args, b"", &AtomicBool::new(false)) {
                    Ok((true, _)) => {
                        detached = true;
                        break;
                    }
                    _ => self.log(format!(
                        "[security] detach failed: {device} (force={force})"
                    )),
                }
            }
            if !detached {
                self.log(format!(
                    "[security] detach could not be confirmed: {device}"
                ));
            }
        }
        let remaining = self.info()?.iter().any(|i| {
            !i.devices.is_disjoint(&self.devices)
                || i.mounts.iter().any(|p| p.starts_with(&self.root))
        });
        if remaining {
            return Err(AppError::new("E_INSTALL_SCAN", "dmg_detach_unconfirmed")
                .with_detail(self.root.to_string_lossy()));
        }
        discovered?;
        self.log("[security] hdiutil info confirmed no remaining mount for this scan".into());
        self.closed = true;
        fs::remove_dir_all(&self.root)?;
        Ok(())
    }
}
impl Drop for Mounted<'_> {
    fn drop(&mut self) {
        if !self.closed
            && let Err(error) = self.cleanup()
        {
            log::error!("DMG cleanup failed: {} {:?}", error.message, error.detail);
        }
    }
}
impl Service {
    pub fn scan_installer_file(
        &self,
        path: &Path,
        cancel: &AtomicBool,
    ) -> Result<InstallerFileScan, AppError> {
        self.scan_installer_with(path, cancel, &NativeMountCommands, &Meter::default())
    }
    pub(crate) fn scan_installer_logged(
        &self,
        path: &Path,
        cancel: &AtomicBool,
        log: &Path,
        meter: &Meter,
    ) -> Result<InstallerFileScan, AppError> {
        self.scan_installer_with(path, cancel, &TaskMountCommands(log), meter)
    }
    fn scan_installer_with(
        &self,
        path: &Path,
        cancel: &AtomicBool,
        commands: &dyn MountCommands,
        meter: &Meter,
    ) -> Result<InstallerFileScan, AppError> {
        let sqlite_files = self.sqlite_files()?;
        let mut file = sqlite_files.open_read(path, sqlite_files.scan_limit(path)?)?;
        files::canceled(cancel)?;
        let coverage = av::container::inspect(&file).ok();
        if coverage.as_ref().is_some_and(|c| {
            c.expanded_bytes.is_some_and(|n| n > files::MAX_SCAN_SIZE)
                || c.incomplete == Some("av_scan_too_large")
        }) {
            let line = format!(
                "[security] Installer skipped before scanning: metadata expanded bytes {:?}, budget {}",
                coverage.and_then(|c| c.expanded_bytes),
                files::MAX_SCAN_SIZE
            );
            commands.trace(&line);
            return Ok(InstallerFileScan {
                skipped: Some(AvSkipReason::TooLarge),
                logs: vec![line],
                ..Default::default()
            });
        }
        match self.av.scan(&mut file, cancel) {
            Ok(threat) => Ok(InstallerFileScan {
                files_scanned: 1,
                threats: threat.into_iter().collect(),
                ..Default::default()
            }),
            Err(error) if error.code == "E_SCAN_CANCELED" => Err(error),
            Err(error) => {
                if is_dmg(path, &file)? && error.code == "E_AV_SCAN_FAILED" {
                    self.scan_mounted_progress(path, cancel, commands, meter)
                } else {
                    Ok(InstallerFileScan {
                        skipped: Some(if error.message == "av_scan_too_large" {
                            AvSkipReason::TooLarge
                        } else {
                            AvSkipReason::Unreadable
                        }),
                        ..Default::default()
                    })
                }
            }
        }
    }
    pub fn scan_mounted(
        &self,
        path: &Path,
        cancel: &AtomicBool,
        commands: &dyn MountCommands,
    ) -> Result<InstallerFileScan, AppError> {
        self.scan_mounted_progress(path, cancel, commands, &Meter::default())
    }
    fn scan_mounted_progress(
        &self,
        path: &Path,
        cancel: &AtomicBool,
        commands: &dyn MountCommands,
        meter: &Meter,
    ) -> Result<InstallerFileScan, AppError> {
        self.sqlite_files()?.check(path)?;
        let snapshot = crate::uninstall::rules::Snapshot::capture(path)?;
        files::safe_metadata(path)?;
        let root = self
            .data
            .join("mount-tmp")
            .join(uuid::Uuid::now_v7().to_string());
        files::private_dir(&root)?;
        let mut mount = Mounted {
            commands,
            image: path.to_owned(),
            root,
            baseline: BTreeSet::new(),
            devices: BTreeSet::new(),
            logs: vec![],
            closed: false,
            armed: false,
        };
        mount.baseline = mount.info()?.into_iter().flat_map(|i| i.devices).collect();
        let args = vec![
            "attach".into(),
            "-plist".into(),
            "-nobrowse".into(),
            "-readonly".into(),
            "-noautoopen".into(),
            "-mountrandom".into(),
            mount.root.to_string_lossy().into_owned(),
            path.to_string_lossy().into_owned(),
        ];
        mount.log(format!(
            "[security] hdiutil attach -plist -nobrowse -readonly -noautoopen -mountrandom {} {}",
            mount.root.display(),
            path.display()
        ));
        mount.armed = true;
        let attached = commands.run(&args, b"qn\n", cancel);
        let scanned = (|| {
            let (ok, _) = match attached {
                Ok(result) => result,
                Err(error) if error.code == "E_SCAN_CANCELED" => return Err(error),
                Err(error) => {
                    mount.log(format!(
                        "[security] DMG attach failed: {}; scan skipped",
                        error.code
                    ));
                    return Ok(InstallerFileScan {
                        skipped: Some(AvSkipReason::Unreadable),
                        ..Default::default()
                    });
                }
            };
            let roots = mount.discover()?;
            if !ok || roots.is_empty() {
                mount.log("[security] DMG attach unavailable (SLA, encryption, password or unsupported format); scan skipped".into());
                return Ok(InstallerFileScan {
                    skipped: Some(AvSkipReason::Unreadable),
                    ..Default::default()
                });
            }
            files::same_file(&snapshot, path)?;
            self.scan_volumes(roots, cancel, meter, commands)
        })();
        let cleanup = mount.cleanup();
        let logs = std::mem::take(&mut mount.logs);
        cleanup.map_err(|error| {
            AppError::new("E_INSTALL_SCAN", "dmg_cleanup_unconfirmed")
                .with_detail(format!("{}: {}", error.code, error.message))
        })?;
        let mut result = scanned?;
        result.logs = logs;
        Ok(result)
    }
    fn scan_volumes(
        &self,
        mut stack: Vec<PathBuf>,
        cancel: &AtomicBool,
        meter: &Meter,
        commands: &dyn MountCommands,
    ) -> Result<InstallerFileScan, AppError> {
        let sqlite_files = self.sqlite_files()?;
        let mut result = InstallerFileScan::default();
        let mut seen = BTreeSet::new();
        let mut total = 0u64;
        let mut too_large = false;
        let mut planned = vec![];
        while let Some(path) = stack.pop() {
            files::canceled(cancel)?;
            let meta = match fs::symlink_metadata(&path) {
                Ok(m) => m,
                Err(_) => {
                    result.skipped = Some(AvSkipReason::Unreadable);
                    continue;
                }
            };
            if meta.is_symlink() || (!meta.is_file() && !meta.is_dir()) {
                continue;
            }
            if files::dataless(&meta) || files::safe_metadata(&path).is_err() {
                result.skipped = Some(AvSkipReason::Unreadable);
                continue;
            }
            if let Err(error) = sqlite_files.check(&path) {
                result.skipped = Some(if error.message == "security_own_data" {
                    AvSkipReason::Excluded
                } else {
                    AvSkipReason::Unreadable
                });
                continue;
            }
            if !seen.insert((meta.dev(), meta.ino())) {
                continue;
            }
            if seen.len() > 50_000 {
                too_large = true;
                result.skipped = Some(AvSkipReason::TooLarge);
                break;
            }
            if meta.is_dir() {
                match fs::read_dir(&path) {
                    Ok(entries) => {
                        for entry in entries {
                            match entry {
                                Ok(e) => stack.push(e.path()),
                                Err(_) => result.skipped = Some(AvSkipReason::Unreadable),
                            }
                        }
                    }
                    Err(_) => result.skipped = Some(AvSkipReason::Unreadable),
                }
                continue;
            }
            total = total.saturating_add(meta.len());
            if meta.len() > files::MAX_INSTALLER_FILE || total > files::MAX_SCAN_SIZE {
                too_large = true;
                result.skipped = Some(AvSkipReason::TooLarge);
                continue;
            }
            planned.push((path, meta.len()));
        }
        meter.volume(total);
        if too_large {
            commands.trace(&format!("[security] Mounted scan skipped before reading content: {total} bytes / {} objects exceeds budget", seen.len()));
            result.skipped = Some(AvSkipReason::TooLarge);
            return Ok(result);
        }
        for (path, bytes) in planned {
            files::canceled(cancel)?;
            let mut file = match sqlite_files.open_read(&path, files::MAX_INSTALLER_FILE) {
                Ok(f) => f,
                Err(_) => {
                    result.skipped = Some(AvSkipReason::Unreadable);
                    continue;
                }
            };
            meter.file_started();
            match self.av.scan(&mut file, cancel) {
                Ok(threat) => {
                    result.files_scanned += 1;
                    result.threats.extend(threat);
                    meter.file_finished(bytes);
                }
                Err(e) if e.code == "E_SCAN_CANCELED" => return Err(e),
                Err(e) => {
                    meter.file_finished(0);
                    too_large |= e.message == "av_scan_too_large";
                    result.skipped = Some(if e.message == "av_scan_too_large" {
                        AvSkipReason::TooLarge
                    } else {
                        AvSkipReason::Unreadable
                    })
                }
            }
        }
        if too_large {
            result.skipped = Some(AvSkipReason::TooLarge);
        } else if result.files_scanned == 0 && result.skipped.is_none() {
            result.skipped = Some(AvSkipReason::Unreadable);
        }
        Ok(result)
    }
}
fn is_dmg(path: &Path, file: &fs::File) -> Result<bool, AppError> {
    use std::os::unix::fs::FileExt;
    if path
        .extension()
        .is_some_and(|e| e.eq_ignore_ascii_case("dmg"))
    {
        return Ok(true);
    }
    let size = file.metadata()?.len();
    let mut magic = [0; 4];
    Ok(size >= 512 && file.read_at(&mut magic, size - 512)? == 4 && &magic == b"koly")
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn mounted_total_is_checked_before_any_file_content_scan() {
        let (_temp, s, _) = super::super::tests::fixture();
        let root = s.home.join("volume");
        fs::create_dir(&root).unwrap();
        for name in ["a", "b", "c"] {
            fs::File::create(root.join(name))
                .unwrap()
                .set_len(900 * 1024 * 1024)
                .unwrap();
        }
        let fake = Fake {
            image: s.home.join("image.dmg"),
            mount: Mutex::new(None),
            calls: Mutex::new(vec![]),
            mode: "clean",
        };
        let result = s
            .scan_volumes(
                vec![root],
                &AtomicBool::new(false),
                &Meter::default(),
                &fake,
            )
            .unwrap();
        assert_eq!(result.skipped, Some(AvSkipReason::TooLarge));
        assert_eq!(result.files_scanned, 0);
        assert!(!s.av.config.runtime.exists()); // No engine exists for this fixture.
    }
    struct Fake {
        image: PathBuf,
        mount: Mutex<Option<PathBuf>>,
        calls: Mutex<Vec<Vec<String>>>,
        mode: &'static str,
    }
    impl MountCommands for Fake {
        fn run(
            &self,
            args: &[String],
            input: &[u8],
            cancel: &AtomicBool,
        ) -> Result<(bool, Vec<u8>), AppError> {
            self.calls.lock().unwrap().push(args.to_vec());
            if args[0] == "attach" {
                assert_eq!(input, b"qn\n");
                for flag in ["-nobrowse", "-readonly", "-noautoopen"] {
                    assert!(args.iter().any(|a| a == flag));
                }
                if self.mode == "denied" {
                    return Ok((false, vec![]));
                }
                let root = PathBuf::from(
                    &args[args.iter().position(|a| a == "-mountrandom").unwrap() + 1],
                )
                .join("volume");
                fs::create_dir(&root).unwrap();
                fs::write(
                    root.join("file"),
                    if self.mode == "threat" {
                        "fixture-threat"
                    } else {
                        "safe"
                    },
                )
                .unwrap();
                std::os::unix::fs::symlink(&self.image, root.join("outside-link")).unwrap();
                *self.mount.lock().unwrap() = Some(root);
                if self.mode == "cancel" {
                    cancel.store(true, Ordering::Release);
                    return Err(AppError::new("E_SCAN_CANCELED", "scan_canceled"));
                }
                return Ok((true, vec![]));
            }
            if args[0] == "detach" {
                assert_ne!(args.last().unwrap(), "/dev/disk999");
                if self.mode == "detach-failure" || !args.contains(&"-force".into()) {
                    return Ok((false, vec![]));
                }
                *self.mount.lock().unwrap() = None;
                return Ok((true, vec![]));
            }
            assert_eq!(args, ["info", "-plist"]);
            let entity = |device: &str, path: &str| serde_json::json!({"dev-entry":device,"mount-point":path});
            let mut list = vec![
                serde_json::json!({"image-path":self.image,"system-entities":[entity("/dev/disk999","/Volumes/Existing")]}),
            ];
            if let Some(path) = self.mount.lock().unwrap().as_ref() {
                list.push(serde_json::json!({"image-path":self.image,"system-entities":[entity("/dev/disk998",&path.to_string_lossy())]}));
            }
            let value = serde_json::json!({"images":list});
            let mut bytes = vec![];
            plist::to_writer_xml(&mut bytes, &value).unwrap();
            Ok((true, bytes))
        }
    }
    #[test]
    fn unconfirmed_detach_blocks_install_and_keeps_the_private_path() {
        let (_temp, s, _) = super::super::tests::fixture();
        super::super::tests::fake_engine(&s);
        s.settings.write().unwrap().av_consent = true;
        let path = s.home.join("fixture.dmg");
        fs::write(&path, "image").unwrap();
        let fake = Fake {
            image: path.clone(),
            mount: Mutex::new(None),
            calls: Mutex::new(vec![]),
            mode: "detach-failure",
        };
        let error = s
            .scan_mounted(&path, &AtomicBool::new(false), &fake)
            .err()
            .unwrap();
        assert_eq!(error.code, "E_INSTALL_SCAN");
        assert_eq!(error.message, "dmg_cleanup_unconfirmed");
        assert!(fake.mount.lock().unwrap().as_ref().unwrap().exists());
    }
    #[test]
    fn mounted_fallback_detaches_on_success_threat_denial_and_cancellation() {
        let (_temp, s, _) = super::super::tests::fixture();
        super::super::tests::fake_engine(&s);
        s.settings.write().unwrap().av_consent = true;
        let path = s.home.join("fixture.dmg");
        fs::write(&path, "safe image").unwrap();
        for mode in ["clean", "threat", "denied", "cancel"] {
            let fake = Fake {
                image: path.clone(),
                mount: Mutex::new(None),
                calls: Mutex::new(vec![]),
                mode,
            };
            let result = s.scan_mounted(&path, &AtomicBool::new(false), &fake);
            if mode == "cancel" {
                assert_eq!(result.err().unwrap().code, "E_SCAN_CANCELED");
            } else {
                let result = result.unwrap();
                assert_eq!(result.files_scanned, if mode == "denied" { 0 } else { 1 });
                assert_eq!(!result.threats.is_empty(), mode == "threat");
                assert_eq!(
                    result.skipped,
                    if mode == "denied" {
                        Some(AvSkipReason::Unreadable)
                    } else {
                        None
                    }
                );
                assert!(result.logs.iter().any(|l| l.contains("no remaining mount")));
            }
            assert!(fake.mount.lock().unwrap().is_none());
            assert_eq!(fs::read_dir(s.data.join("mount-tmp")).unwrap().count(), 0);
            if mode != "denied" {
                assert!(
                    fake.calls
                        .lock()
                        .unwrap()
                        .iter()
                        .any(|args| args[0] == "detach" && args.contains(&"-force".into()))
                );
            }
        }
    }
}
