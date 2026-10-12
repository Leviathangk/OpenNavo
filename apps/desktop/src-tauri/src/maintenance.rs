// Internal Homebrew dry-run data; no longer part of the desktop IPC contract.
#[derive(Debug, Clone, PartialEq)]
pub struct EstimatedPath {
    pub path: String,
    pub bytes: Option<u64>,
}
#[derive(Debug, Clone, PartialEq)]
pub struct Estimate {
    pub bytes: u64,
    pub file_count: u32,
    pub items: Vec<EstimatedPath>,
}

use crate::{
    AppError,
    brew::{
        args::{ReadOp, read_args},
        env::construct,
    },
    core::DesktopCore,
    model::*,
};
use std::{path::Path, process::Stdio, sync::Arc, time::Duration};
use tokio::{io::AsyncReadExt, process::Command};

pub struct Output {
    pub code: i32,
    pub text: String,
}
impl Output {
    pub fn success(&self) -> Result<(), AppError> {
        if self.code == 0 {
            Ok(())
        } else {
            Err(
                crate::brew::parse::map_error(&self.text).unwrap_or_else(|| {
                    AppError::new("E_UNKNOWN", "brew_read_failed").with_detail(self.text.clone())
                }),
            )
        }
    }
}
pub async fn run(
    brew: &BrewInfo,
    settings: &Settings,
    args: Vec<String>,
) -> Result<Output, AppError> {
    let mut child = Command::new(&brew.path)
        .args(args)
        .env_clear()
        .envs(construct(
            settings,
            &brew.prefix,
            Path::new("/nonexistent/opennavo_askpass"),
        ))
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true)
        .spawn()?;
    let stdout = child
        .stdout
        .take()
        .ok_or_else(|| AppError::new("E_UNKNOWN", "stdout_pipe"))?;
    let stderr = child
        .stderr
        .take()
        .ok_or_else(|| AppError::new("E_UNKNOWN", "stderr_pipe"))?;
    async fn text(reader: impl tokio::io::AsyncRead + Unpin) -> std::io::Result<Vec<u8>> {
        let mut bytes = vec![];
        reader
            .take(4 * 1024 * 1024 + 1)
            .read_to_end(&mut bytes)
            .await?;
        Ok(bytes)
    }
    let (out, err, status) = tokio::time::timeout(Duration::from_secs(60), async {
        tokio::try_join!(text(stdout), text(stderr), child.wait())
    })
    .await
    .map_err(|_| AppError::new("E_TIMEOUT", "maintenance_timeout"))??;
    if out.len() > 4 * 1024 * 1024 || err.len() > 4 * 1024 * 1024 {
        return Err(AppError::new("E_UNKNOWN", "maintenance_output_size"));
    }
    Ok(Output {
        code: status.code().unwrap_or(-1),
        text: format!(
            "{}\n{}",
            String::from_utf8_lossy(&out),
            String::from_utf8_lossy(&err)
        ),
    })
}

fn bytes(text: &str) -> Option<u64> {
    let regex = regex::Regex::new(r"(?i)([0-9]+(?:\.[0-9]+)?)\s*(B|KB|MB|GB|TB)\b").ok()?;
    let capture = regex.captures_iter(text).last()?;
    let value = capture[1].parse::<f64>().ok()?;
    let power = match capture[2].to_ascii_uppercase().as_str() {
        "B" => 0,
        "KB" => 1,
        "MB" => 2,
        "GB" => 3,
        "TB" => 4,
        _ => return None,
    };
    Some((value * 1024_f64.powi(power)).round() as u64)
}
pub fn cleanup(text: &str) -> Estimate {
    let mut result = Estimate {
        bytes: 0,
        file_count: 0,
        items: vec![],
    };
    let mut total = None;
    for line in text.lines() {
        if line.contains("This operation would free approximately") {
            total = bytes(line);
        }
        let remainder = line
            .strip_prefix("Would remove: ")
            .or_else(|| line.strip_prefix("Would remove (broken link): "))
            .or_else(|| line.strip_prefix("Would remove (empty directory): "));
        if let Some(remainder) = remainder {
            let (path, size) = remainder
                .rsplit_once(" (")
                .map(|(p, s)| (p, bytes(s)))
                .unwrap_or((remainder, None));
            result.file_count += 1;
            result.bytes = result.bytes.saturating_add(size.unwrap_or(0));
            if result.items.len() < 200 {
                result.items.push(EstimatedPath {
                    path: path.into(),
                    bytes: size,
                });
            }
        }
    }
    result.bytes = total.unwrap_or(result.bytes);
    result
}
pub fn doctor(text: &str, now: i64) -> DoctorReport {
    let mut report = DoctorReport {
        ok: text.contains("Your system is ready to brew."),
        warnings: vec![],
        ran_at: now,
    };
    for line in text.lines() {
        if let Some(title) = line.strip_prefix("Warning: ") {
            report.warnings.push(DoctorWarning {
                title: title.into(),
                body: String::new(),
            });
        } else if let Some(warning) = report.warnings.last_mut() {
            if !warning.body.is_empty() {
                warning.body.push('\n');
            }
            warning.body.push_str(line);
        }
    }
    for warning in &mut report.warnings {
        warning.body = warning.body.trim().into();
    }
    report.ok &= report.warnings.is_empty();
    report
}
pub async fn estimate(core: Arc<DesktopCore>) -> Result<Estimate, AppError> {
    let _read = core.queue.read_gate.read().await;
    let result = run(
        &core.brew_info().await?,
        &core.current_settings()?,
        read_args(ReadOp::CleanupEstimate, None)?,
    )
    .await?;
    result.success()?;
    crate::blocking(move || Ok(cleanup(&result.text))).await
}
pub async fn diagnose(core: Arc<DesktopCore>) -> Result<DoctorReport, AppError> {
    let _read = core.queue.read_gate.read().await;
    let result = run(
        &core.brew_info().await?,
        &core.current_settings()?,
        read_args(ReadOp::Doctor, None)?,
    )
    .await?;
    // doctor warnings normally exit with 1; do not treat them as IPC failures.
    if result.code != 0 && !result.text.contains("Warning: ") {
        result.success()?;
    }
    crate::blocking(move || Ok(doctor(&result.text, chrono::Utc::now().timestamp_millis()))).await
}
