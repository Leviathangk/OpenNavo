//! Fixed native commands with separate, bounded output streams.
use crate::AppError;
use std::{
    io::Read,
    process::{Child, Command, Stdio},
    time::{Duration, Instant},
};

const LIMIT: u64 = 4 * 1024 * 1024;

#[derive(Debug)]
pub struct Output {
    pub code: Option<i32>,
    pub stdout: String,
    pub stderr: String,
}
impl Output {
    /// Only for human-readable diagnostics, including tools such as codesign
    /// and spctl whose result is written to stderr. Do not use this for XML,
    /// file lists, numbers or other structured stdout formats.
    pub fn text(&self) -> String {
        [self.stdout.trim_end(), self.stderr.trim_end()]
            .into_iter()
            .filter(|s| !s.is_empty())
            .collect::<Vec<_>>()
            .join("\n")
    }
    pub fn success(self) -> Result<Self, AppError> {
        if self.code == Some(0) {
            Ok(self)
        } else {
            Err(AppError::new("E_UNKNOWN", "command_exit").with_detail(
                serde_json::json!({
                    "exitCode": self.code,
                    "stdout": self.stdout.chars().take(4096).collect::<String>(),
                    "stderr": self.stderr.chars().take(4096).collect::<String>()
                })
                .to_string(),
            ))
        }
    }
}
struct OwnedChild(Child);
impl Drop for OwnedChild {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}
pub fn output(program: &str, args: &[&str], timeout: Duration) -> Result<Output, AppError> {
    let mut child = OwnedChild(
        Command::new(program)
            .args(args)
            // Keep diagnostics predictable without escaping UTF-8 archive paths.
            .env("LC_ALL", "en_US.UTF-8")
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()?,
    );
    let read = |pipe: Box<dyn Read + Send>| {
        std::thread::spawn(move || {
            let mut bytes = Vec::new();
            pipe.take(LIMIT + 1).read_to_end(&mut bytes)?;
            Ok::<_, std::io::Error>(bytes)
        })
    };
    let out =
        read(Box::new(child.0.stdout.take().ok_or_else(|| {
            AppError::new("E_UNKNOWN", "command_stdout")
        })?));
    let err =
        read(Box::new(child.0.stderr.take().ok_or_else(|| {
            AppError::new("E_UNKNOWN", "command_stderr")
        })?));
    let start = Instant::now();
    let status = loop {
        if let Some(status) = child.0.try_wait()?
            && out.is_finished()
            && err.is_finished()
        {
            break status;
        }
        if start.elapsed() >= timeout {
            return Err(AppError::new("E_TIMEOUT", "command_timeout"));
        }
        std::thread::sleep(Duration::from_millis(10));
    };
    let bytes = out
        .join()
        .map_err(|_| AppError::new("E_UNKNOWN", "command_reader"))??;
    let error = err
        .join()
        .map_err(|_| AppError::new("E_UNKNOWN", "command_reader"))??;
    if bytes.len() as u64 > LIMIT || error.len() as u64 > LIMIT {
        return Err(AppError::new("E_UNKNOWN", "command_output_limit"));
    }
    Ok(Output {
        code: status.code(),
        stdout: String::from_utf8_lossy(&bytes).into_owned(),
        stderr: String::from_utf8_lossy(&error).into_owned(),
    })
}
