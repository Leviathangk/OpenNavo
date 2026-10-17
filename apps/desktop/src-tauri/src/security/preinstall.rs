// SPDX-License-Identifier: GPL-3.0-only
//! Fetch into a task-private Homebrew cache, scan, then allow installation.
use super::install_progress::Meter;
use super::*;
use crate::{
    brew::pty::{Execution, Progress, execute},
    model::TaskOp,
    uninstall::rules::Snapshot,
};
use std::{
    collections::BTreeSet,
    fs,
    io::{Seek, SeekFrom},
};

pub struct Prepared {
    root: PathBuf,
    files: Vec<(PathBuf, Snapshot, String)>,
    known: Mutex<BTreeSet<PathBuf>>,
}
impl Prepared {
    /// Informational only: Homebrew may download more during build/install.
    /// Those bytes are outside the pre-download check and must not abort install.
    pub fn download_logs(lines: &[String]) -> Vec<String> {
        lines
            .iter()
            .filter(|line| {
                let line = line.trim_start();
                line.starts_with("==> Downloading") || line.starts_with("==> Fetching")
            })
            .map(|line| format!("[security] Download after pre-install scan (not scanned): {line}"))
            .collect()
    }
    pub fn additional_file_logs(&self) -> Vec<String> {
        let (current, unreadable) = cache_entries(&self.root);
        let Ok(mut known) = self.known.lock() else {
            return vec![];
        };
        let mut logs = current
            .difference(&known)
            .map(|path| {
                format!(
                    "[security] New cache file after pre-install scan (not scanned): {}",
                    path.strip_prefix(&self.root)
                        .unwrap_or(path)
                        .display()
                        .to_string()
                        .escape_debug()
                )
            })
            .collect::<Vec<_>>();
        known.extend(current);
        if unreadable {
            logs.push("[security] Unable to enumerate all additional cache files".into());
        }
        logs
    }
    pub fn revalidate(&self, cancel: &AtomicBool) -> Result<(), AppError> {
        for (path, snapshot, expected) in &self.files {
            files::same_file(snapshot, path)
                .map_err(|_| AppError::new("E_INSTALL_SCAN", "install_download_changed"))?;
            let actual = files::hash(
                &mut files::open_read(path, files::scan_limit(path)?)?,
                cancel,
            )?;
            if &actual != expected {
                return Err(AppError::new("E_INSTALL_SCAN", "install_download_changed"));
            }
        }
        Ok(())
    }
}
impl Drop for Prepared {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.root);
    }
}
impl Service {
    pub fn install_scan_get(&self, id: &str) -> Result<Option<InstallScanResult>, AppError> {
        if uuid::Uuid::parse_str(id).is_err() {
            return Err(AppError::new("E_INVALID_ARG", "task_id"));
        }
        self.s2_get(&format!("security_s2_install:{id}"))
    }
    fn save_install_scan(&self, result: &InstallScanResult) -> Result<(), AppError> {
        self.s2_set(&format!("security_s2_install:{}", result.task_id), result)?;
        (self.sink)(CoreEvent::InstallScanned(result.clone()));
        Ok(())
    }
    pub fn prepare_install(
        &self,
        id: &str,
        request: &mut Execution,
        mut progress: impl FnMut(&[String], &Progress),
    ) -> Result<Option<Prepared>, AppError> {
        if !matches!(
            request.op,
            TaskOp::Install | TaskOp::Upgrade | TaskOp::Reinstall
        ) {
            return Ok(None);
        }
        let settings = self.settings.read().map_err(poison)?.clone();
        let mut result = InstallScanResult {
            task_id: id.into(),
            state: InstallScanState::Skipped,
            files_scanned: 0,
            av_skipped: None,
            threats: vec![],
            checked_at: now(),
        };
        if !settings.av_scan_before_install {
            result.state = InstallScanState::Disabled;
        } else if !settings.av_consent {
            result.av_skipped = Some(AvSkipReason::NoConsent);
        } else if self.av.ready().is_err() {
            result.av_skipped = Some(AvSkipReason::NoDatabase);
        } else {
            let root = self
                .data
                .join("install-cache")
                .join(uuid::Uuid::now_v7().to_string());
            files::private_dir(&root)?;
            let mut prepared = Prepared {
                root: root.clone(),
                files: vec![],
                known: Mutex::new(BTreeSet::new()),
            };
            let kind = request
                .args
                .get(1)
                .filter(|s| matches!(s.as_str(), "--cask" | "--formula"))
                .ok_or_else(|| AppError::new("E_INSTALL_SCAN", "install_kind"))?
                .clone();
            let target = request
                .args
                .last()
                .ok_or_else(|| AppError::new("E_INSTALL_SCAN", "install_target"))?
                .clone();
            request
                .env
                .insert("HOMEBREW_CACHE".into(), root.to_string_lossy().into_owned());
            request
                .env
                .insert("HOMEBREW_NO_AUTO_UPDATE".into(), "1".into());
            request
                .env
                .insert("HOMEBREW_API_AUTO_UPDATE_SECS".into(), "604800".into());
            request.cache_dir = Some(root.clone());
            let mut fetch = request.clone();
            fetch.args = vec!["fetch".into(), kind.clone()];
            if kind == "--formula" {
                fetch.args.push("--deps".into());
            }
            fetch.args.push(target);
            let fetched = execute(fetch, |lines, p| fetch_progress(&mut progress, lines, p))?;
            if fetched.canceled || request.cancel.load(Ordering::Acquire) {
                return Err(AppError::new("E_INTERRUPTED", "task_canceled"));
            }
            if fetched.code != 0 {
                return Err(AppError::new("E_INSTALL_SCAN", "install_fetch_failed"));
            }
            if kind == "--cask" {
                // Cask fetch rejects --deps. Ask Homebrew for its recursive
                // dependency list, then fetch those names with normal type inference.
                let mut deps = request.clone();
                deps.args = vec![
                    "deps".into(),
                    "--include-build".into(),
                    "--include-implicit".into(),
                    "--full-name".into(),
                    kind,
                    request.args.last().unwrap().clone(),
                ];
                let mut names = vec![];
                let outcome = execute(deps, |lines, _| names.extend_from_slice(lines))?;
                if outcome.canceled {
                    return Err(AppError::new("E_INTERRUPTED", "task_canceled"));
                }
                if outcome.code != 0 {
                    return Err(AppError::new("E_INSTALL_SCAN", "install_dependencies"));
                }
                let names = names
                    .into_iter()
                    .filter(|s| !s.trim().is_empty())
                    .collect::<Vec<_>>();
                if names.len() > 256 {
                    return Err(AppError::new(
                        "E_INSTALL_SCAN",
                        "install_dependencies_limit",
                    ));
                }
                for name in names {
                    // These are names, never command flags or shell fragments.
                    let parts: Vec<_> = name.split('/').collect();
                    if !matches!(parts.len(), 1 | 3)
                        || parts
                            .iter()
                            .any(|p| crate::brew::args::validate_token(p).is_err())
                    {
                        return Err(AppError::new("E_INSTALL_SCAN", "install_dependency_name"));
                    }
                    let mut fetch = request.clone();
                    fetch.args = vec!["fetch".into(), name];
                    let outcome =
                        execute(fetch, |lines, p| fetch_progress(&mut progress, lines, p))?;
                    if outcome.canceled {
                        return Err(AppError::new("E_INTERRUPTED", "task_canceled"));
                    }
                    if outcome.code != 0 {
                        return Err(AppError::new("E_INSTALL_SCAN", "install_dependency_fetch"));
                    }
                }
            }
            let meter = Meter::default();
            progress(&[], &meter.progress());
            let (finished, receiver) = std::sync::mpsc::channel();
            let scanned = std::thread::scope(|scope| {
                let worker = scope.spawn(|| {
                    let result = self.scan_install_cache(
                        &root,
                        &settings,
                        &request.cancel,
                        &mut prepared,
                        &mut result,
                        &request.log_path,
                        &meter,
                    );
                    let _ = finished.send(());
                    result
                });
                while !worker.is_finished()
                    && receiver.recv_timeout(std::time::Duration::from_millis(250))
                        == Err(std::sync::mpsc::RecvTimeoutError::Timeout)
                {
                    progress(&[], &meter.progress());
                }
                let result = worker
                    .join()
                    .map_err(|_| AppError::new("E_INSTALL_SCAN", "install_scan_worker"))?;
                progress(&[], &meter.progress());
                result
            });
            scanned.map_err(|e| {
                if e.code == "E_SCAN_CANCELED" {
                    AppError::new("E_INTERRUPTED", "task_canceled")
                } else {
                    e
                }
            })?;
            *prepared.known.lock().map_err(poison)? = cache_entries(&root).0;
            self.save_install_scan(&result)?;
            if result.state == InstallScanState::Threat {
                self.record_event(ProtectionEventKind::InstallThreat, id, None, false)?;
                return Err(
                    AppError::new("E_INSTALL_THREAT", "install_threat").with_detail(
                        serde_json::json!({"taskId":id,"signatures":result.threats}).to_string(),
                    ),
                );
            }
            prepared.revalidate(&request.cancel)?;
            return Ok(Some(prepared));
        }
        self.save_install_scan(&result)?;
        Ok(None)
    }
    #[allow(clippy::too_many_arguments)]
    fn scan_install_cache(
        &self,
        root: &Path,
        settings: &Settings,
        cancel: &AtomicBool,
        prepared: &mut Prepared,
        result: &mut InstallScanResult,
        log_path: &Path,
        meter: &Meter,
    ) -> Result<(), AppError> {
        // Only configured exclusions apply to this explicitly requested private cache.
        let excluded: Vec<_> = settings
            .av_exclusions
            .iter()
            .map(|p| {
                p.strip_prefix("~/")
                    .map(|p| self.home.join(p))
                    .unwrap_or_else(|| PathBuf::from(p))
            })
            .collect();
        let mut skipped = AvSkipped::default();
        let mut planned = vec![];
        let mut total = 0u64;
        let mut over_budget = false;
        // Metadata first. No clamd startup, content hashing or mount is needed
        // to reject an already oversized ZIP/ZIP64/DMG workload.
        scan::walk(
            vec![root.to_owned()],
            &excluded,
            cancel,
            &mut skipped,
            &|_, m| files::dataless(m),
            &mut |path| {
                let snapshot = Snapshot::capture(path)?;
                let file = files::open_read(path, files::scan_limit(path)?)?;
                let coverage = av::container::inspect(&file).ok();
                let bytes = coverage
                    .as_ref()
                    .and_then(|c| c.expanded_bytes)
                    .unwrap_or(snapshot.length);
                over_budget |= coverage
                    .as_ref()
                    .is_some_and(|c| c.incomplete == Some("av_scan_too_large"));
                total = total.saturating_add(bytes);
                planned.push((path.to_owned(), snapshot, bytes));
                Ok(())
            },
        )?;
        meter.plan(total);
        if over_budget || total > files::MAX_SCAN_SIZE {
            let line = format!(
                "[security] Pre-install scan skipped: metadata size {total} exceeds container budget (2 GiB total / 1 GiB member)"
            );
            mounted::task_log(log_path, &line);
            result.av_skipped = Some(AvSkipReason::TooLarge);
            return Ok(());
        }
        let _lease = self.av.lease(cancel).map_err(|e| {
            if e.code == "E_SCAN_CANCELED" {
                e
            } else {
                AppError::new("E_INSTALL_SCAN", "install_engine").with_detail(e.code)
            }
        })?;
        let mut threats = BTreeSet::new();
        let mut scan_unreadable = false;
        for (path, snapshot, bytes) in planned {
            files::canceled(cancel)?;
            files::same_file(&snapshot, &path)?;
            meter.begin(bytes);
            let mut file = files::open_read(&path, files::scan_limit(&path)?)?;
            let hash = files::hash(&mut file, cancel)?;
            file.seek(SeekFrom::Start(0))?;
            let scanned = self.scan_installer_logged(&path, cancel, log_path, meter)?;
            threats.extend(scanned.threats);
            files::same_file(&snapshot, &path)?;
            prepared.files.push((path, snapshot, hash));
            result.files_scanned += scanned.files_scanned;
            if let Some(reason) = scanned.skipped {
                if reason == AvSkipReason::TooLarge {
                    skipped.too_large += 1;
                } else {
                    scan_unreadable = true;
                }
            }
            meter.finish();
        }
        // Homebrew creates symlink aliases alongside its actual downloads. We
        // never follow them for scanning; an alias of an already scanned object
        // inside this private cache is not a separate unscanned download.
        let aliases = cache_entries(root)
            .0
            .iter()
            .filter(|path| {
                !excluded.iter().any(|p| path.starts_with(p))
                    && fs::symlink_metadata(path).is_ok_and(|m| m.is_symlink())
                    && path.canonicalize().is_ok_and(|target| {
                        target.starts_with(root)
                            && prepared.files.iter().any(|(p, _, _)| *p == target)
                    })
            })
            .count() as u64;
        skipped.excluded = skipped.excluded.saturating_sub(aliases);
        result.threats = threats.into_iter().collect();
        result.av_skipped = if skipped.unreadable > 0 || skipped.dataless > 0 || scan_unreadable {
            Some(AvSkipReason::Unreadable)
        } else if skipped.too_large > 0 {
            Some(AvSkipReason::TooLarge)
        } else if skipped.excluded > 0 {
            Some(AvSkipReason::Excluded)
        } else {
            None
        };
        result.state = if !result.threats.is_empty() {
            InstallScanState::Threat
        } else if result.av_skipped.is_some() {
            InstallScanState::Skipped
        } else {
            InstallScanState::Clean
        };
        if result.files_scanned == 0 && result.av_skipped.is_none() {
            return Err(AppError::new("E_INSTALL_SCAN", "install_download_missing"));
        }
        Ok(())
    }
}

fn fetch_progress(callback: &mut impl FnMut(&[String], &Progress), lines: &[String], p: &Progress) {
    callback(
        lines,
        &Progress {
            phase: p.phase,
            percent: p.percent.map(|v| v.min(70.0) * (60.0 / 70.0)),
            step_index: p.step_index,
            from: p.from.clone(),
            to: p.to.clone(),
            bytes: p.bytes,
            total: p.total,
            speed: p.speed,
        },
    );
}

/// Metadata only; do not follow links or descend into cloud placeholders.
fn cache_entries(root: &Path) -> (BTreeSet<PathBuf>, bool) {
    let mut stack = vec![root.to_owned()];
    let mut found = BTreeSet::new();
    let mut unreadable = false;
    while let Some(path) = stack.pop() {
        match fs::symlink_metadata(&path) {
            Ok(m) if m.is_dir() && !files::dataless(&m) => {
                if files::safe_metadata(&path).is_err() {
                    unreadable = true;
                    continue;
                }
                match fs::read_dir(&path) {
                    Ok(entries) => {
                        for entry in entries {
                            match entry {
                                Ok(entry) => stack.push(entry.path()),
                                Err(_) => unreadable = true,
                            }
                        }
                    }
                    Err(_) => unreadable = true,
                }
            }
            Ok(_) => {
                found.insert(path);
            }
            Err(_) => unreadable = true,
        }
    }
    (found, unreadable)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{os::unix::fs::PermissionsExt, time::Duration};
    #[test]
    fn aggregate_metadata_budget_skips_before_starting_the_engine() {
        use std::io::{Cursor, Write};
        let (_temp, s, _) = super::super::tests::fixture();
        let root = s.home.join("cache");
        fs::create_dir(&root).unwrap();
        for name in ["first.zip", "second.zip"] {
            let mut writer = zip::ZipWriter::new(Cursor::new(Vec::new()));
            for member in ["one", "two"] {
                writer
                    .start_file(member, zip::write::SimpleFileOptions::default())
                    .unwrap();
                writer.write_all(b"x").unwrap();
            }
            let mut bytes = writer.finish().unwrap().into_inner();
            let entries: Vec<_> = bytes
                .windows(4)
                .enumerate()
                .filter_map(|(i, v)| (v == b"PK\x01\x02").then_some(i))
                .collect();
            for at in entries {
                bytes[at + 24..at + 28].copy_from_slice(&(600u32 * 1024 * 1024).to_le_bytes());
            }
            fs::write(root.join(name), bytes).unwrap();
        }
        let mut prepared = Prepared {
            root: root.clone(),
            files: vec![],
            known: Mutex::new(BTreeSet::new()),
        };
        let mut result = InstallScanResult {
            task_id: uuid::Uuid::now_v7().to_string(),
            state: InstallScanState::Skipped,
            files_scanned: 0,
            av_skipped: None,
            threats: vec![],
            checked_at: now(),
        };
        s.scan_install_cache(
            &root,
            &Settings::default(),
            &AtomicBool::new(false),
            &mut prepared,
            &mut result,
            &s.home.join("task.log"),
            &Meter::default(),
        )
        .unwrap();
        assert_eq!(result.av_skipped, Some(AvSkipReason::TooLarge));
        assert_eq!(result.files_scanned, 0);
        assert!(!s.av.config.runtime.exists());
        assert!(
            fs::read_to_string(s.home.join("task.log"))
                .unwrap()
                .contains("metadata size")
        );
    }
    #[test]
    fn threat_blocks_before_install_and_clean_cache_is_revalidated() {
        let (_tmp, s, _) = super::super::tests::fixture();
        let brew = s.home.join("fake-brew");
        fs::write(
            &brew,
            r#"#!/usr/bin/env python3
import sys,os,pathlib,time
root=pathlib.Path(os.environ['HOMEBREW_CACHE'])
if sys.argv[1]=='fetch':
 (root/'archive.zip').write_text(os.environ.get('FIXTURE_PAYLOAD','fixture-threat'))
 if os.environ.get('FIXTURE_ALIAS'): (root/'alias.zip').symlink_to(root/'archive.zip')
 if os.environ.get('FIXTURE_LARGE'):
  with (root/'large.dmg').open('wb') as f: f.truncate(1024*1024*1024+1)
 if os.environ.get('FIXTURE_UNREADABLE'): (root/'archive.zip').chmod(0)
 if os.environ.get('FIXTURE_FAIL'): raise SystemExit(17)
 if os.environ.get('FIXTURE_WAIT'):
  pathlib.Path(os.environ['FIXTURE_WAIT']).write_text('ready')
  time.sleep(30)
elif sys.argv[1]=='deps': pass
else:
 pathlib.Path(os.environ['INSTALL_MARKER']).write_text('installed')
 print('==> Downloading additional-fixture')
 (root/'additional.pkg').write_text('unscanned')
"#,
        )
        .unwrap();
        fs::set_permissions(&brew, fs::Permissions::from_mode(0o700)).unwrap();
        let marker = s.home.join("installed");
        let request = Execution {
            executable: brew,
            args: vec!["install".into(), "--cask".into(), "fixture".into()],
            env: std::collections::BTreeMap::from([
                ("PATH".into(), "/usr/bin:/bin".into()),
                ("INSTALL_MARKER".into(), marker.to_string_lossy().into()),
            ]),
            log_path: s.home.join("task.log"),
            op: TaskOp::Install,
            cancel: Arc::new(AtomicBool::new(false)),
            grace: Duration::from_millis(100),
            cache_dir: None,
            download_total: None,
        };
        let id = uuid::Uuid::now_v7().to_string();
        assert!(
            s.prepare_install(&id, &mut request.clone(), |_, _| {})
                .unwrap()
                .is_none()
        );
        assert_eq!(
            s.install_scan_get(&id).unwrap().unwrap().av_skipped,
            Some(AvSkipReason::NoConsent)
        );
        assert!(!marker.exists());
        assert!(!s.data.join("install-cache").exists());
        s.settings.write().unwrap().av_consent = true;
        let mut missing_db = request.clone();
        assert!(
            s.prepare_install(&id, &mut missing_db, |_, _| {})
                .unwrap()
                .is_none()
        );
        assert_eq!(
            s.install_scan_get(&id).unwrap().unwrap().av_skipped,
            Some(AvSkipReason::NoDatabase)
        );
        assert_eq!(missing_db.env, request.env);
        assert!(missing_db.cache_dir.is_none());
        assert!(!s.data.join("install-cache").exists());
        super::super::tests::fake_engine(&s);
        let error = s
            .prepare_install(&id, &mut request.clone(), |_, _| {})
            .err()
            .unwrap();
        assert_eq!(error.code, "E_INSTALL_THREAT");
        assert!(!marker.exists());
        assert_eq!(
            s.install_scan_get(&id).unwrap().unwrap().state,
            InstallScanState::Threat
        );
        assert_eq!(
            fs::read_dir(s.data.join("install-cache")).unwrap().count(),
            0
        );
        let mut clean = request.clone();
        clean.env.insert("FIXTURE_PAYLOAD".into(), "safe".into());
        clean.env.insert("FIXTURE_ALIAS".into(), "1".into());
        let mut phases = vec![];
        let guard = s
            .prepare_install(&id, &mut clean, |_, progress| phases.extend(progress.phase))
            .unwrap()
            .unwrap();
        assert_eq!(phases.last(), Some(&crate::model::TaskPhase::Scanning));
        guard.revalidate(&clean.cancel).unwrap();
        assert_eq!(
            s.install_scan_get(&id).unwrap().unwrap().state,
            InstallScanState::Clean
        );
        let mut logs = vec![];
        let outcome = execute(clean.clone(), |lines, _| {
            logs.extend(Prepared::download_logs(lines))
        })
        .unwrap();
        assert_eq!(outcome.code, 0);
        assert!(logs.iter().any(|s| s.contains("additional-fixture")));
        assert!(
            guard
                .additional_file_logs()
                .iter()
                .any(|s| s.contains("additional.pkg"))
        );
        assert!(guard.additional_file_logs().is_empty());
        guard.revalidate(&clean.cancel).unwrap();
        assert!(marker.exists());
        fs::write(guard.root.join("archive.zip"), "changed").unwrap();
        assert!(guard.revalidate(&clean.cancel).is_err());
        let root = guard.root.clone();
        drop(guard);
        assert!(!root.exists());
        for (flag, reason, count) in [
            ("FIXTURE_LARGE", AvSkipReason::TooLarge, 1),
            ("FIXTURE_UNREADABLE", AvSkipReason::Unreadable, 0),
        ] {
            let mut partial = request.clone();
            partial.env.insert("FIXTURE_PAYLOAD".into(), "safe".into());
            partial.env.insert(flag.into(), "1".into());
            let guard = s
                .prepare_install(&id, &mut partial, |_, _| {})
                .unwrap()
                .unwrap();
            let result = s.install_scan_get(&id).unwrap().unwrap();
            assert_eq!(result.state, InstallScanState::Skipped);
            assert_eq!(result.av_skipped, Some(reason));
            assert_eq!(result.files_scanned, count);
            let root = guard.root.clone();
            drop(guard);
            assert!(!root.exists());
        }
        for suffix in [
            "Limits.Exceeded.MaxFileSize",
            "Limits.Exceeded.MaxScanSize",
            "Limits.Exceeded.MaxFiles",
            "Limits.Exceeded.MaxRecursion",
            "Limits.Exceeded.MaxScanTime",
            "Encrypted.Zip",
            "Encrypted.PDF",
        ] {
            let mut partial = request.clone();
            partial.env.insert(
                "FIXTURE_PAYLOAD".into(),
                format!("heuristic:Heuristics.{suffix}"),
            );
            let guard = s
                .prepare_install(&id, &mut partial, |_, _| {})
                .unwrap()
                .unwrap();
            let result = s.install_scan_get(&id).unwrap().unwrap();
            assert_eq!(result.state, InstallScanState::Skipped);
            assert_eq!(result.files_scanned, 0);
            assert!(result.threats.is_empty());
            assert_eq!(
                result.av_skipped,
                Some(if suffix.starts_with("Limits") {
                    AvSkipReason::TooLarge
                } else {
                    AvSkipReason::Unreadable
                })
            );
            drop(guard);
        }
        s.settings.write().unwrap().av_exclusions =
            vec![s.data.join("install-cache").to_string_lossy().into_owned()];
        let mut excluded = request.clone();
        let guard = s
            .prepare_install(&id, &mut excluded, |_, _| {})
            .unwrap()
            .unwrap();
        let result = s.install_scan_get(&id).unwrap().unwrap();
        assert_eq!(result.state, InstallScanState::Skipped);
        assert_eq!(result.av_skipped, Some(AvSkipReason::Excluded));
        assert_eq!(result.files_scanned, 0);
        drop(guard);
        s.settings.write().unwrap().av_exclusions.clear();
        let mut failed = request.clone();
        failed.env.insert("FIXTURE_FAIL".into(), "1".into());
        assert_eq!(
            s.prepare_install(&id, &mut failed, |_, _| {})
                .err()
                .unwrap()
                .code,
            "E_INSTALL_SCAN"
        );
        assert_eq!(
            fs::read_dir(s.data.join("install-cache")).unwrap().count(),
            0
        );
        // Fetch and clamd scans are both interruptible; a canceled task leaves no cache.
        for wait_fetch in [true, false] {
            let mut waiting = request.clone();
            waiting.cancel = Arc::new(AtomicBool::new(false));
            waiting
                .env
                .insert("FIXTURE_PAYLOAD".into(), "wait-for-cancel".into());
            let ready = s.home.join("fetch-ready");
            if wait_fetch {
                waiting
                    .env
                    .insert("FIXTURE_WAIT".into(), ready.to_string_lossy().into_owned());
            }
            let cancel = waiting.cancel.clone();
            let worker_service = s.clone();
            let worker_id = id.clone();
            let start = std::time::Instant::now();
            let worker = std::thread::spawn(move || {
                worker_service.prepare_install(&worker_id, &mut waiting, |_, _| {})
            });
            if wait_fetch {
                while !ready.exists() {
                    assert!(start.elapsed() < Duration::from_secs(4));
                    std::thread::sleep(Duration::from_millis(10));
                }
            } else {
                std::thread::sleep(Duration::from_millis(500));
            }
            cancel.store(true, Ordering::Release);
            assert_eq!(worker.join().unwrap().err().unwrap().code, "E_INTERRUPTED");
            assert!(start.elapsed() < Duration::from_secs(4));
            assert_eq!(
                fs::read_dir(s.data.join("install-cache")).unwrap().count(),
                0
            );
        }
        s.settings.write().unwrap().av_scan_before_install = false;
        assert!(
            s.prepare_install(&id, &mut request.clone(), |_, _| {})
                .unwrap()
                .is_none()
        );
        assert_eq!(
            s.install_scan_get(&id).unwrap().unwrap().state,
            InstallScanState::Disabled
        );
    }
}
