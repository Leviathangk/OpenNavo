//! Bounded read-only command output, including diagnostic nonzero exits.
use crate::AppError;
use std::{
    io::Read,
    process::{Child, Command, Stdio},
    time::{Duration, Instant},
};

pub struct Output {
    pub code: Option<i32>,
    pub stdout: String,
    pub stderr: String,
}
impl Output {
    pub fn text(&self) -> String {
        format!("{}\n{}", self.stdout, self.stderr)
    }
}
struct OwnedChild(Child);
impl Drop for OwnedChild {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}
pub fn output(program: &str, args: &[&str]) -> Result<Output, AppError> {
    let mut child = OwnedChild(
        Command::new(program)
            .args(args)
            .env("LC_ALL", "C")
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()?,
    );
    let read = |pipe: Box<dyn Read + Send>| {
        std::thread::spawn(move || {
            let mut bytes = Vec::new();
            pipe.take(4 * 1024 * 1024)
                .read_to_end(&mut bytes)
                .map(|_| bytes)
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
        if let Some(status) = child.0.try_wait()? {
            break status;
        }
        if start.elapsed() >= Duration::from_secs(10) {
            return Err(AppError::new("E_UNKNOWN", "command_timeout"));
        }
        std::thread::sleep(Duration::from_millis(10));
    };
    let bytes = out
        .join()
        .map_err(|_| AppError::new("E_UNKNOWN", "command_reader"))??;
    let error = err
        .join()
        .map_err(|_| AppError::new("E_UNKNOWN", "command_reader"))??;
    Ok(Output {
        code: status.code(),
        stdout: String::from_utf8_lossy(&bytes).into_owned(),
        stderr: String::from_utf8_lossy(&error).into_owned(),
    })
}
