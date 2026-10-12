use super::parse::{LineDecoder, Parser};
use crate::{AppError, model::TaskOp};
use nix::{
    sys::signal::{Signal, killpg},
    unistd::Pid,
};
use portable_pty::{CommandBuilder, PtySize, native_pty_system};
use std::{
    collections::BTreeMap,
    fs::OpenOptions,
    io::{Read, Write},
    path::{Path, PathBuf},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
        mpsc,
    },
    thread,
    time::{Duration, Instant, SystemTime},
};

#[derive(Clone)]
pub struct Execution {
    pub executable: PathBuf,
    pub args: Vec<String>,
    pub env: BTreeMap<String, String>,
    pub log_path: PathBuf,
    pub op: TaskOp,
    pub cancel: Arc<AtomicBool>,
    pub grace: Duration,
    pub cache_dir: Option<PathBuf>,
    pub download_total: Option<u64>,
}
#[derive(Debug)]
pub struct Outcome {
    pub code: i32,
    pub canceled: bool,
    #[cfg(test)]
    pub signal: Option<String>,
    pub parser: Parser,
}
#[derive(Debug)]
pub struct Progress {
    pub phase: Option<crate::model::TaskPhase>,
    pub percent: Option<f32>,
    pub step_index: Option<u8>,
    pub from: Option<String>,
    pub to: Option<String>,
    pub bytes: Option<u64>,
    pub total: Option<u64>,
    pub speed: Option<u64>,
}

struct OwnedChild(Box<dyn portable_pty::Child + Send + Sync>);
impl Drop for OwnedChild {
    fn drop(&mut self) {
        if matches!(self.0.try_wait(), Ok(None)) {
            if let Some(pid) = self.0.process_id() {
                let _ = signal_owned_group(pid, Signal::SIGKILL);
            }
            let _ = self.0.kill();
            let _ = self.0.wait();
        }
    }
}

fn failed(error: impl std::fmt::Display) -> AppError {
    AppError::new("E_UNKNOWN", "pty").with_detail(error.to_string())
}

fn signal_owned_group(pid: u32, signal: Signal) -> Result<(), AppError> {
    let pid = i32::try_from(pid).map_err(failed)?;
    if pid <= 1 {
        return Err(AppError::new("E_UNKNOWN", "invalid_child_pid"));
    }
    let group = nix::unistd::getpgid(Some(Pid::from_raw(pid))).map_err(failed)?;
    // portable-pty creates a separate process group with setsid; never signal a group unless verified.
    if group.as_raw() != pid {
        return Err(AppError::new("E_UNKNOWN", "unexpected_child_group"));
    }
    killpg(group, signal).map_err(failed)
}

fn incomplete_bytes(cache: &Path, since: SystemTime, observed: &mut BTreeMap<PathBuf, u64>) -> u64 {
    for dir in [cache.to_path_buf(), cache.join("downloads")] {
        if let Ok(entries) = std::fs::read_dir(dir) {
            for entry in entries.flatten() {
                let path = entry.path();
                if entry.file_name().to_string_lossy().ends_with(".incomplete")
                    && let Ok(meta) = entry.metadata()
                    && meta.is_file()
                    // Canceled runs leave partial files behind; count one only once this run writes to it (resuming).
                    // Keep tracking observed files: curl --remote-time backdates them when the download completes.
                    && (observed.contains_key(&path)
                        || meta.modified().is_ok_and(|modified| modified >= since))
                {
                    let size = observed.entry(path).or_default();
                    *size = (*size).max(meta.len());
                }
            }
        }
    }
    // Homebrew removes .incomplete after download; retain observed files so install byte counts do not reset to zero.
    for (path, size) in observed.iter_mut() {
        if let Ok(meta) = std::fs::metadata(path.with_extension(""))
            && meta.is_file()
        {
            *size = (*size).max(meta.len());
        }
    }
    observed.values().copied().sum()
}

/// Homebrew 7 downloads in parallel and, under the TERM=dumb bash defaults to, prints no percentages;
/// map measured bytes onto the same 0–70% download stage the parser uses for curl percentages.
fn download_percent(bytes: Option<u64>, total: Option<u64>) -> Option<f32> {
    let total = total.filter(|total| *total > 0)?;
    Some((bytes? as f64 / total as f64).min(1.0) as f32 * 70.0)
}

/// Run on a blocking thread. Callbacks receive sanitized logs and machine-readable progress, never passwords or environment variables.
pub fn execute(
    mut request: Execution,
    mut callback: impl FnMut(&[String], &Progress),
) -> Result<Outcome, AppError> {
    if request.cancel.load(Ordering::Acquire) {
        return Ok(Outcome {
            code: 130,
            canceled: true,
            #[cfg(test)]
            signal: None,
            parser: Parser::default(),
        });
    }
    if let Some(parent) = request.log_path.parent() {
        std::fs::create_dir_all(parent)?;
    }
    let mut options = OpenOptions::new();
    options.create(true).append(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut log = options.open(&request.log_path)?;
    let pair = native_pty_system()
        .openpty(PtySize {
            rows: 40,
            cols: 120,
            pixel_width: 0,
            pixel_height: 0,
        })
        .map_err(failed)?;
    let mut command = CommandBuilder::new(&request.executable);
    command.args(&request.args);
    command.env_clear();
    for (key, value) in std::mem::take(&mut request.env) {
        command.env(key, value);
    }
    let spawned = SystemTime::now();
    let mut child = OwnedChild(pair.slave.spawn_command(command).map_err(failed)?);
    drop(pair.slave);
    let pid = child
        .0
        .process_id()
        .ok_or_else(|| AppError::new("E_UNKNOWN", "missing_child_pid"))?;
    let mut reader = pair.master.try_clone_reader().map_err(failed)?;
    let (sender, receiver) = mpsc::sync_channel(256);
    let reader_thread = thread::spawn(move || {
        let mut decoder = LineDecoder::default();
        let mut buffer = [0u8; 8192];
        loop {
            match reader.read(&mut buffer) {
                Ok(0) => break,
                Ok(n) => {
                    for line in decoder.push(&buffer[..n]) {
                        if sender.send(line).is_err() {
                            return;
                        }
                    }
                }
                Err(error) if error.kind() == std::io::ErrorKind::Interrupted => continue,
                Err(_) => break,
            }
        }
        for line in decoder.finish() {
            if sender.send(line).is_err() {
                return;
            }
        }
    });
    let mut parser = Parser::default();
    let mut batch = Vec::new();
    let mut last_emit = Instant::now();
    let mut last_sample = Instant::now();
    let mut last_bytes = 0;
    let mut observed_downloads = BTreeMap::new();
    let mut bytes = None;
    let mut speed = None;
    let mut cancel_started = None;
    let mut signal_stage = 0;
    let mut status = None;
    let mut reader_done = false;
    while status.is_none() || !reader_done {
        match receiver.recv_timeout(Duration::from_millis(25)) {
            Ok(line) => {
                writeln!(log, "{line}")?;
                parser.feed(&line, request.op);
                batch.push(line);
            }
            Err(mpsc::RecvTimeoutError::Disconnected) => reader_done = true,
            Err(mpsc::RecvTimeoutError::Timeout) => {}
        }
        if status.is_none() {
            status = child.0.try_wait()?;
        }
        if request.cancel.load(Ordering::Acquire) && status.is_none() {
            let start = *cancel_started.get_or_insert_with(Instant::now);
            let stage = if start.elapsed() >= request.grace * 2 {
                3
            } else if start.elapsed() >= request.grace {
                2
            } else {
                1
            };
            if stage > signal_stage {
                let signal = match stage {
                    1 => Signal::SIGINT,
                    2 => Signal::SIGTERM,
                    _ => Signal::SIGKILL,
                };
                // Exit may race with signaling; the next try_wait determines the final state.
                let _ = signal_owned_group(pid, signal);
                signal_stage = stage;
            }
        }
        if last_sample.elapsed() >= Duration::from_millis(500) {
            if let Some(cache) = &request.cache_dir {
                let measured = incomplete_bytes(cache, spawned, &mut observed_downloads);
                // Unobserved files may mean cache hits or downloads faster than sampling; never display unknown size as 0 B.
                bytes = (measured > 0).then_some(measured);
                speed = Some(
                    ((measured.saturating_sub(last_bytes)) as f64
                        / last_sample.elapsed().as_secs_f64()) as u64,
                );
                last_bytes = measured;
            }
            last_sample = Instant::now();
        }
        if last_emit.elapsed() >= Duration::from_millis(100) || (reader_done && status.is_some()) {
            callback(
                &batch,
                &Progress {
                    phase: if bytes.is_some()
                        && parser.phase == Some(crate::model::TaskPhase::Fetching)
                    {
                        Some(crate::model::TaskPhase::Downloading)
                    } else {
                        parser.phase
                    },
                    percent: parser
                        .percent
                        .or_else(|| download_percent(bytes, request.download_total)),
                    step_index: parser.step_index,
                    from: parser.from_version.clone(),
                    to: parser.to_version.clone(),
                    bytes,
                    total: request.download_total,
                    speed,
                },
            );
            batch.clear();
            last_emit = Instant::now();
            log.flush()?;
        }
    }
    drop(pair.master);
    reader_thread
        .join()
        .map_err(|_| AppError::new("E_UNKNOWN", "pty_reader_panicked"))?;
    #[cfg(test)]
    let signal = status
        .as_ref()
        .and_then(|status| status.signal().map(str::to_owned));
    let code = status.map(|s| s.exit_code() as i32).unwrap_or(1);
    Ok(Outcome {
        code,
        #[cfg(test)]
        signal,
        canceled: request.cancel.load(Ordering::Acquire),
        parser,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{
        brew::{args::write_args, env::construct},
        model::{Kind, Settings, TaskOptions, TaskTarget},
    };
    #[test]
    fn downloaded_bytes_survive_completion_and_multiple_downloads() {
        let dir = tempfile::tempdir().expect("temporary directory");
        let downloads = dir.path().join("downloads");
        std::fs::create_dir(&downloads).expect("cache directory");
        let partial = downloads.join("app.dmg.incomplete");
        let since = SystemTime::now() - Duration::from_secs(1);
        let mut observed = BTreeMap::new();
        assert_eq!(incomplete_bytes(dir.path(), since, &mut observed), 0);
        std::fs::write(&partial, [0; 40]).expect("downloading");
        assert_eq!(incomplete_bytes(dir.path(), since, &mut observed), 40);
        std::fs::write(&partial, [0; 100]).expect("download end");
        let complete = downloads.join("app.dmg");
        std::fs::rename(&partial, &complete).expect("rename completed download");
        assert_eq!(incomplete_bytes(dir.path(), since, &mut observed), 100);
        assert_eq!(incomplete_bytes(dir.path(), since, &mut observed), 100);
        std::fs::remove_file(complete).expect("clean cache");
        assert_eq!(incomplete_bytes(dir.path(), since, &mut observed), 100);
        std::fs::write(dir.path().join("other.zip.incomplete"), [0; 20]).expect("next download");
        assert_eq!(incomplete_bytes(dir.path(), since, &mut observed), 120);
        // Track each execution independently; retries and later tasks do not inherit earlier byte counts.
        assert_eq!(
            incomplete_bytes(dir.path(), since, &mut BTreeMap::new()),
            20
        );
    }

    #[test]
    fn earlier_partial_downloads_count_only_once_resumed() {
        let dir = tempfile::tempdir().expect("temporary directory");
        let downloads = dir.path().join("downloads");
        std::fs::create_dir(&downloads).expect("cache directory");
        let since = SystemTime::now() - Duration::from_secs(1);
        let backdate = |path: &Path, modified: SystemTime| {
            std::fs::File::options()
                .write(true)
                .open(path)
                .and_then(|file| file.set_modified(modified))
                .expect("set modification time");
        };
        let stale = downloads.join("other.dmg.incomplete");
        std::fs::write(&stale, [0; 300]).expect("canceled download");
        backdate(&stale, since - Duration::from_secs(60));
        let mut observed = BTreeMap::new();
        assert_eq!(incomplete_bytes(dir.path(), since, &mut observed), 0);
        let partial = downloads.join("app.dmg.incomplete");
        std::fs::write(&partial, [0; 40]).expect("downloading");
        assert_eq!(incomplete_bytes(dir.path(), since, &mut observed), 40);
        // curl --remote-time backdates a finished download before Homebrew renames it.
        std::fs::write(&partial, [0; 100]).expect("download end");
        backdate(&partial, since - Duration::from_secs(3600));
        assert_eq!(incomplete_bytes(dir.path(), since, &mut observed), 100);
        std::fs::OpenOptions::new()
            .append(true)
            .open(&stale)
            .and_then(|mut file| file.write_all(&[0; 20]))
            .expect("resume canceled download");
        assert_eq!(incomplete_bytes(dir.path(), since, &mut observed), 420);
    }

    #[test]
    fn measured_bytes_fill_the_download_stage() {
        assert_eq!(download_percent(Some(40), Some(100)), Some(28.0));
        assert_eq!(download_percent(Some(0), Some(100)), Some(0.0));
        // Dependencies or a stale catalog size can exceed the target's size; never pass the download stage.
        assert_eq!(download_percent(Some(150), Some(100)), Some(70.0));
        assert_eq!(download_percent(None, Some(100)), None);
        assert_eq!(download_percent(Some(40), None), None);
        assert_eq!(download_percent(Some(40), Some(0)), None);
    }

    #[test]
    fn homebrew7_pty_does_not_reset_downloads_while_installing() {
        use crate::model::TaskPhase;
        let dir = tempfile::tempdir().expect("temporary directory");
        let cache = dir.path().join("cache");
        let request = Execution {
            executable: Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/fake-brew/brew"),
            args: vec!["upgrade".into(), "--cask".into(), "sourcegit".into()],
            env: BTreeMap::from([
                ("FAKE_BREW_SCENARIO".into(), "homebrew7_upgrade".into()),
                (
                    "FAKE_BREW_CACHE".into(),
                    cache.to_string_lossy().into_owned(),
                ),
            ]),
            log_path: dir.path().join("run.log"),
            op: TaskOp::Upgrade,
            cancel: Arc::new(AtomicBool::new(false)),
            grace: Duration::from_millis(100),
            cache_dir: Some(cache),
            download_total: Some(100),
        };
        let mut previous = None;
        let mut installing_seen = false;
        let result = execute(request, |_, progress| {
            assert_ne!(progress.bytes, Some(0));
            if let Some(bytes) = progress.bytes {
                assert!(previous.is_none_or(|last| bytes >= last));
                previous = Some(bytes);
                // Homebrew 7 prints no percentage mid-download; measured bytes fill the ring until it reports completion.
                assert!(
                    progress
                        .percent
                        .is_some_and(|percent| percent >= bytes as f32 * 0.7),
                    "{progress:?}"
                );
            }
            if progress.phase == Some(TaskPhase::Installing) {
                installing_seen = true;
                assert_eq!(progress.bytes, Some(100));
                assert_eq!(progress.percent, Some(70.0));
            }
        })
        .expect("simulated PTY");
        assert_eq!(result.code, 0);
        assert!(installing_seen);
        assert_eq!(previous, Some(100));
    }

    #[test]
    fn simulated_pty_progress_logs_and_failures() {
        for (scenario, expected) in [
            ("install_ok", None),
            ("checksum_fail", Some("E_CHECKSUM")),
            ("sudo_cancel", Some("E_SUDO")),
            ("locked", Some("E_LOCKED")),
        ] {
            let dir = tempfile::tempdir().expect("temporary directory");
            let mut env = construct(
                &Settings::default(),
                &dir.path().to_string_lossy(),
                Path::new("/unused"),
            );
            env.insert("FAKE_BREW_SCENARIO".into(), scenario.into());
            let request = Execution {
                executable: Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/fake-brew/brew"),
                args: write_args(
                    TaskOp::Install,
                    Some(&TaskTarget {
                        kind: Kind::Cask.into(),
                        token: "inkscape".into(),
                    }),
                    &TaskOptions::default(),
                )
                .expect("arguments"),
                env,
                log_path: dir.path().join("run.log"),
                op: TaskOp::Install,
                cancel: Arc::new(AtomicBool::new(false)),
                grace: Duration::from_millis(100),
                cache_dir: None,
                download_total: None,
            };
            let log_path = request.log_path.clone();
            let mut lines = Vec::new();
            let result = execute(request, |batch, _| lines.extend_from_slice(batch)).expect("PTY");
            assert_eq!(
                result.parser.error.as_ref().map(|e| e.code.as_str()),
                expected,
                "{scenario}"
            );
            assert_eq!(result.code == 0, expected.is_none());
            assert!(!lines.is_empty());
            assert!(
                std::fs::read_to_string(log_path)
                    .expect("logs")
                    .contains("==>")
            );
        }
    }
    #[test]
    fn cancels_only_own_group_and_flushes_last_line() {
        let dir = tempfile::tempdir().expect("directory");
        let cancel = Arc::new(AtomicBool::new(false));
        let mut env = construct(
            &Settings::default(),
            &dir.path().to_string_lossy(),
            Path::new("/unused"),
        );
        env.insert("FAKE_BREW_SCENARIO".into(), "slow".into());
        let request = Execution {
            executable: Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/fake-brew/brew"),
            args: vec!["install".into(), "--formula".into(), "ripgrep".into()],
            env,
            log_path: dir.path().join("run.log"),
            op: TaskOp::Install,
            cancel: cancel.clone(),
            grace: Duration::from_millis(50),
            cache_dir: None,
            download_total: None,
        };
        let mark = cancel.clone();
        thread::spawn(move || {
            thread::sleep(Duration::from_millis(200));
            mark.store(true, Ordering::Release);
        });
        let start = Instant::now();
        let result = execute(request, |_, _| {}).expect("cancel");
        assert!(result.canceled);
        assert!(start.elapsed() < Duration::from_secs(2));
    }
    #[test]
    fn cancellation_escalates_to_kill_for_unresponsive_simulator() {
        let dir = tempfile::tempdir().unwrap();
        let cancel = Arc::new(AtomicBool::new(false));
        let mut env = construct(
            &Settings::default(),
            &dir.path().to_string_lossy(),
            Path::new("/unused"),
        );
        env.insert("FAKE_BREW_SCENARIO".into(), "slow".into());
        env.insert("FAKE_BREW_IGNORE_SIGNALS".into(), "1".into());
        let request = Execution {
            executable: Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/fake-brew/brew"),
            args: vec!["install".into(), "--formula".into(), "ripgrep".into()],
            env,
            log_path: dir.path().join("run.log"),
            op: TaskOp::Install,
            cancel: cancel.clone(),
            grace: Duration::from_millis(50),
            cache_dir: None,
            download_total: None,
        };
        let mut cancel_started = None;
        let result = execute(request, |lines, _| {
            if cancel_started.is_none() && lines.iter().any(|line| line == "FAKE_BREW_READY") {
                // Measure cancellation latency, excluding PTY and simulator startup on busy runners.
                cancel_started = Some(Instant::now());
                cancel.store(true, Ordering::Release);
            }
        })
        .unwrap();
        let cancel_started = cancel_started.expect("simulator exited before announcing readiness");
        assert!(result.canceled);
        assert_ne!(result.code, 0);
        assert!(
            result
                .signal
                .as_deref()
                .is_some_and(|signal| signal.to_ascii_lowercase().contains("kill")),
            "expected SIGKILL, got {result:?}"
        );
        assert!(
            cancel_started.elapsed() < Duration::from_secs(2),
            "cancellation exceeded its deadline: {:?}",
            cancel_started.elapsed()
        );
    }
}
