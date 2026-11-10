//! Native login runner. Hermes/CLI auth commands are started in a hidden PTY
//! so the app can surface prompts, device codes and OAuth URLs without ever
//! opening Terminal.app.

use std::collections::HashSet;
use std::io::{Read, Write};
use std::path::Path;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::thread;

use portable_pty::{CommandBuilder, PtySize, native_pty_system};
use regex::Regex;
use serde::Serialize;
use tauri::{AppHandle, Emitter};

use super::{AppError, data_root, hermes_home, login, runtime_bin};

#[derive(Default)]
pub struct HermesLoginState {
    inner: Mutex<Option<LoginHandle>>,
}

struct LoginHandle {
    pid: u32,
    writer: Box<dyn Write + Send>,
    cancelled: Arc<AtomicBool>,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct HermesLoginEvent {
    pub provider: String,
    pub kind: String,
    pub line: Option<String>,
    pub url: Option<String>,
    pub code: Option<String>,
    pub ok: Option<bool>,
    pub message: Option<String>,
}

fn emit(app: &AppHandle, event: HermesLoginEvent) {
    let _ = app.emit("hermes-login", event);
}

fn event(provider: &str, kind: &str) -> HermesLoginEvent {
    HermesLoginEvent {
        provider: provider.to_owned(),
        kind: kind.to_owned(),
        line: None,
        url: None,
        code: None,
        ok: None,
        message: None,
    }
}

fn open_url(url: &str) {
    let _ = std::process::Command::new("/usr/bin/open").arg(url).spawn();
}

fn login_command(bin: &Path, provider: &str) -> Result<Vec<String>, AppError> {
    let bin = bin.display().to_string();
    Ok(match provider {
        "codex" => vec![
            bin,
            "auth".into(),
            "add".into(),
            "openai-codex".into(),
            "--type".into(),
            "oauth".into(),
            "--no-browser".into(),
        ],
        "claude-code" => vec![
            bin,
            "auth".into(),
            "add".into(),
            "anthropic".into(),
            "--type".into(),
            "oauth".into(),
            "--no-browser".into(),
        ],
        "copilot" => vec![
            bin,
            "auth".into(),
            "add".into(),
            "copilot".into(),
            "--type".into(),
            "oauth".into(),
            "--no-browser".into(),
        ],
        "nous" => vec![
            bin,
            "auth".into(),
            "add".into(),
            "nous".into(),
            "--type".into(),
            "oauth".into(),
        ],
        other => {
            return Err(AppError::new(
                "E_HERMES_LOGIN",
                format!("Unknown login provider: {other}"),
            ));
        }
    })
}

pub fn start(
    app: &AppHandle,
    state: Arc<HermesLoginState>,
    provider: &str,
) -> Result<(), AppError> {
    let bin = runtime_bin(app).ok_or_else(|| {
        AppError::new(
            "E_HERMES_RUNTIME",
            "Install the Hermes runtime before logging in.",
        )
    })?;
    let home = hermes_home(app)?;
    let empty_codex = data_root(app)?.join("codex-login-empty");
    std::fs::create_dir_all(&empty_codex)?;
    let args = login_command(&bin, provider)?;

    let mut guard = state
        .inner
        .lock()
        .map_err(|_| AppError::new("E_HERMES_LOGIN", "Login state is unavailable."))?;
    if let Some(previous) = guard.take() {
        previous.cancelled.store(true, Ordering::SeqCst);
        kill(previous.pid);
    }

    let pty_system = native_pty_system();
    let pair = pty_system
        .openpty(PtySize {
            rows: 40,
            cols: 140,
            pixel_width: 0,
            pixel_height: 0,
        })
        .map_err(|error| {
            AppError::new("E_HERMES_LOGIN", "Could not allocate a login terminal.")
                .with_detail(error.to_string())
        })?;
    let mut command = CommandBuilder::new(&args[0]);
    command.args(&args[1..]);
    command.env("HERMES_HOME", home.display().to_string());
    command.env("NO_COLOR", "1");
    command.env("TERM", "xterm-256color");
    command.cwd(home.display().to_string());
    if provider == "codex" {
        command.env("CODEX_HOME", empty_codex.display().to_string());
    }
    if matches!(provider, "codex" | "claude-code" | "copilot") {
        // The CLI uses --no-browser, but older builds may still call the
        // default browser. Make OpenNavo the only opener for these flows.
        command.env("BROWSER", "/usr/bin/true");
    }
    if let Some(dir) = bin.parent() {
        let path = std::env::var_os("PATH").unwrap_or_default();
        let mut paths = vec![dir.to_path_buf()];
        paths.extend(std::env::split_paths(&path));
        if let Ok(joined) = std::env::join_paths(paths) {
            command.env("PATH", joined);
        }
    }
    let child = pair.slave.spawn_command(command).map_err(|error| {
        AppError::new("E_HERMES_LOGIN", "Could not start the login command.")
            .with_detail(error.to_string())
    })?;
    let pid = child.process_id().unwrap_or(0);
    let reader = pair.master.try_clone_reader().map_err(|error| {
        AppError::new("E_HERMES_LOGIN", "Could not read login output.")
            .with_detail(error.to_string())
    })?;
    let writer = pair.master.take_writer().map_err(|error| {
        AppError::new("E_HERMES_LOGIN", "Could not write to the login command.")
            .with_detail(error.to_string())
    })?;
    let cancelled = Arc::new(AtomicBool::new(false));
    let failure = Arc::new(Mutex::new(None::<String>));
    *guard = Some(LoginHandle {
        pid,
        writer,
        cancelled: cancelled.clone(),
    });
    drop(guard);

    emit(app, event(provider, "started"));
    spawn_reader(app.clone(), provider.to_owned(), reader, failure.clone());
    spawn_waiter(
        app.clone(),
        state,
        child,
        pair.master,
        pid,
        cancelled,
        provider.to_owned(),
        failure,
    );
    Ok(())
}

pub fn input(state: &HermesLoginState, value: &str) -> Result<(), AppError> {
    let mut guard = state
        .inner
        .lock()
        .map_err(|_| AppError::new("E_HERMES_LOGIN", "Login state is unavailable."))?;
    let handle = guard
        .as_mut()
        .ok_or_else(|| AppError::new("E_HERMES_LOGIN", "No login is running."))?;
    handle
        .writer
        .write_all(value.as_bytes())
        .and_then(|_| handle.writer.write_all(b"\n"))
        .and_then(|_| handle.writer.flush())
        .map_err(|error| {
            AppError::new("E_HERMES_LOGIN", "Could not send login input.")
                .with_detail(error.to_string())
        })
}

pub fn cancel(state: &HermesLoginState) {
    if let Ok(mut guard) = state.inner.lock()
        && let Some(handle) = guard.take()
    {
        handle.cancelled.store(true, Ordering::SeqCst);
        kill(handle.pid);
    }
}

fn kill(pid: u32) {
    if pid == 0 {
        return;
    }
    #[cfg(unix)]
    {
        use nix::sys::signal::{Signal, kill};
        use nix::unistd::Pid;
        let _ = kill(Pid::from_raw(pid as i32), Signal::SIGTERM);
    }
}

fn is_failure_line(line: &str) -> bool {
    let lower = line.to_ascii_lowercase();
    [
        "command has been removed",
        "traceback",
        "error:",
        "not found",
        "invalid_grant",
        "invalid_client",
    ]
    .iter()
    .any(|needle| lower.contains(needle))
}

fn spawn_reader(
    app: AppHandle,
    provider: String,
    mut reader: Box<dyn Read + Send>,
    failure: Arc<Mutex<Option<String>>>,
) {
    thread::spawn(move || {
        let ansi = Regex::new(r"\x1b\[[0-9;?]*[ -/]*[@-~]").expect("ansi regex");
        let urls = Regex::new(r#"https?://[^\s<>"']+"#).expect("url regex");
        let codes = Regex::new(r"\b[A-Z0-9]{4,10}-[A-Z0-9]{4,10}\b").expect("code regex");
        let mut pending = String::new();
        let mut opened = HashSet::new();
        let mut browser_opened = false;
        let mut buffer = [0u8; 4096];
        while let Ok(size) = reader.read(&mut buffer) {
            if size == 0 {
                break;
            }
            pending.push_str(&String::from_utf8_lossy(&buffer[..size]));
            while let Some(newline) = pending.find('\n') {
                let raw = pending[..newline].to_owned();
                pending.drain(..=newline);
                let line = ansi
                    .replace_all(&raw, "")
                    .replace('\r', "")
                    .trim()
                    .to_owned();
                if line.is_empty() {
                    continue;
                }
                let mut line_event = event(&provider, "line");
                line_event.line = Some(line.clone());
                emit(&app, line_event);
                if is_failure_line(&line)
                    && let Ok(mut stored) = failure.lock()
                    && stored.is_none()
                {
                    *stored = Some(line.clone());
                }
                for found in urls.find_iter(&line) {
                    let url = found
                        .as_str()
                        .trim_end_matches([')', ',', '.', ';', '"', '\''])
                        .to_owned();
                    if url.contains("127.0.0.1") || url.contains("localhost") {
                        continue;
                    }
                    if !opened.insert(url.clone()) {
                        continue;
                    }
                    let mut url_event = event(&provider, "url");
                    url_event.url = Some(url.clone());
                    emit(&app, url_event);
                    if matches!(provider.as_str(), "codex" | "claude-code" | "copilot")
                        && !browser_opened
                    {
                        open_url(&url);
                        browser_opened = true;
                    }
                }
                if let Some(found) = codes.find(&line) {
                    let mut code_event = event(&provider, "code");
                    code_event.code = Some(found.as_str().to_owned());
                    emit(&app, code_event);
                }
            }
        }
    });
}

#[allow(clippy::too_many_arguments)]
fn spawn_waiter(
    app: AppHandle,
    state: Arc<HermesLoginState>,
    mut child: Box<dyn portable_pty::Child + Send + Sync>,
    _master: Box<dyn portable_pty::MasterPty + Send>,
    pid: u32,
    cancelled: Arc<AtomicBool>,
    provider: String,
    failure: Arc<Mutex<Option<String>>>,
) {
    thread::spawn(move || {
        let status = child.wait();
        let failure_line = failure.lock().ok().and_then(|value| value.clone());
        let was_cancelled = cancelled.load(Ordering::SeqCst);
        if let Ok(mut guard) = state.inner.lock()
            && guard.as_ref().map(|handle| handle.pid) == Some(pid)
        {
            *guard = None;
        }
        let exit_ok = status.map(|value| value.success()).unwrap_or(false);
        let authenticated = match provider.as_str() {
            "codex" => login::hermes_authenticated(&app, "openai-codex"),
            "claude-code" => login::hermes_authenticated(&app, "anthropic"),
            "copilot" => login::hermes_authenticated(&app, "copilot"),
            "nous" => login::hermes_authenticated(&app, "nous"),
            _ => false,
        };
        let ok = !was_cancelled && exit_ok && authenticated && failure_line.is_none();
        let mut done = event(&provider, if was_cancelled { "cancelled" } else { "done" });
        done.ok = Some(ok);
        done.message = Some(if was_cancelled {
            "登录已取消。".into()
        } else if ok {
            "登录完成。".into()
        } else if let Some(line) = failure_line {
            format!("登录失败：{line}")
        } else if exit_ok && !authenticated {
            "登录命令结束，但 Hermes 没有写入有效认证；请查看输出。".into()
        } else {
            "登录未完成，请查看输出或重试。".into()
        });
        emit(&app, done);
    });
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    #[test]
    fn extracts_login_urls_and_codes() {
        let urls = Regex::new(r#"https?://[^\s<>"']+"#).unwrap();
        let codes = Regex::new(r"\b[A-Z0-9]{4,10}-[A-Z0-9]{4,10}\b").unwrap();
        let line = "Open https://auth.example.com/oauth?x=1, or enter ABCD-1234";
        assert!(urls.is_match(line));
        assert_eq!(codes.find(line).unwrap().as_str(), "ABCD-1234");
        assert_eq!(
            codes.find("code 820X-9P2LA").unwrap().as_str(),
            "820X-9P2LA"
        );
        assert!(is_failure_line(
            "The 'hermes login' command has been removed."
        ));
        assert!(!is_failure_line("Waiting for sign-in..."));
    }

    #[test]
    fn builds_provider_commands() {
        let bin = PathBuf::from("/tmp/hermes");
        assert_eq!(login_command(&bin, "codex").unwrap()[1], "auth");
        assert_eq!(login_command(&bin, "copilot").unwrap()[1], "auth");
        assert_eq!(login_command(&bin, "nous").unwrap()[1], "auth");
        assert!(login_command(&bin, "unknown").is_err());
    }
}
