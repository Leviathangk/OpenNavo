//! Docker's own reclaimable space. Images, build cache and containers live inside Docker's virtual disk, so only
//! Docker may remove them: `docker system df` measures them read-only and a fixed prune command removes them once the
//! user picks it. Volumes can hold databases; they are measured and shown, never offered.
use super::types::CleanupReason;
use crate::AppError;
use std::{
    io::Read,
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::atomic::{AtomicBool, Ordering},
    time::{Duration, Instant},
};

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Usage {
    pub reclaimable: u64,
    pub count: u64,
    pub active: u64,
}
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Report {
    pub images: Usage,
    pub containers: Usage,
    pub volumes: Usage,
    pub build_cache: Usage,
}
impl Report {
    pub fn get(&self, reason: CleanupReason) -> Option<Usage> {
        match reason {
            CleanupReason::DockerImages => Some(self.images),
            CleanupReason::DockerBuildCache => Some(self.build_cache),
            CleanupReason::DockerContainers => Some(self.containers),
            _ => None,
        }
    }
}

/// What a scan offers, in report order. The argument lists are fixed; nothing from the scan or the user reaches them.
pub const PRUNES: &[(CleanupReason, &[&str])] = &[
    (
        CleanupReason::DockerImages,
        &["image", "prune", "--all", "--force"],
    ),
    (
        CleanupReason::DockerBuildCache,
        &["builder", "prune", "--all", "--force"],
    ),
    (
        CleanupReason::DockerContainers,
        &["container", "prune", "--force"],
    ),
];
pub fn prune_args(reason: CleanupReason) -> Option<&'static [&'static str]> {
    PRUNES
        .iter()
        .find(|(candidate, _)| *candidate == reason)
        .map(|(_, args)| *args)
}

/// Engines whose daemon answers the CLI. The CLI is only called while one of them runs: some engines start themselves
/// on the first `docker` command, and a read-only scan must never launch a virtual machine.
pub const ENGINES: &[&str] = &[
    "com.docker.backend",
    "OrbStack",
    "OrbStack Helper",
    "limactl",
    "colima",
];
/// Unknown (the process table could not be read) counts as not running.
pub fn engine_running(processes: Option<&[super::owner::CommandProcess]>) -> bool {
    processes.is_some_and(|processes| {
        processes.iter().any(|p| {
            p.executable
                .file_name()
                .is_some_and(|name| ENGINES.iter().any(|engine| name == *engine))
        })
    })
}

/// The CLI of Docker Desktop, OrbStack, Rancher Desktop or Homebrew. It must resolve to a regular executable that
/// belongs to root or the user and that nobody else can replace (the file and its folder are not group/world-writable).
pub fn locate(home: &Path) -> Option<PathBuf> {
    [
        PathBuf::from("/usr/local/bin/docker"),
        PathBuf::from("/opt/homebrew/bin/docker"),
        home.join(".docker/bin/docker"),
        home.join(".orbstack/bin/docker"),
        home.join(".rd/bin/docker"),
        PathBuf::from("/Applications/Docker.app/Contents/Resources/bin/docker"),
        PathBuf::from("/Applications/OrbStack.app/Contents/MacOS/xbin/docker"),
    ]
    .iter()
    .find_map(|path| trusted(path))
}
pub fn trusted(path: &Path) -> Option<PathBuf> {
    use std::os::unix::fs::MetadataExt;
    let resolved = path.canonicalize().ok()?;
    let me = unsafe { nix::libc::geteuid() };
    let safe = |path: &Path| {
        std::fs::metadata(path)
            .is_ok_and(|m| (m.uid() == 0 || m.uid() == me) && m.mode() & 0o022 == 0)
    };
    let metadata = std::fs::metadata(&resolved).ok()?;
    (metadata.is_file()
        && metadata.mode() & 0o111 != 0
        && safe(&resolved)
        && resolved.parent().is_some_and(safe))
    .then_some(resolved)
}

/// A fixed environment: the CLI reads its contexts from HOME and needs nothing else from the user's shell.
pub fn env(home: &Path) -> Vec<(String, String)> {
    vec![
        ("HOME".into(), home.to_string_lossy().into_owned()),
        ("PATH".into(), "/usr/bin:/bin:/usr/sbin:/sbin".into()),
        ("DOCKER_CLI_HINTS".into(), "false".into()),
    ]
}

/// A local socket pinned to its canonical path and identity for the lifetime of a scan.
#[derive(Debug, Clone)]
pub struct Endpoint {
    host: String,
    device: u64,
    inode: u64,
}
impl Endpoint {
    fn local(host: &str) -> Option<Self> {
        use std::os::unix::fs::{FileTypeExt, MetadataExt};
        let path = Path::new(host.strip_prefix("unix://")?);
        if !path.is_absolute() {
            return None;
        }
        let path = path.canonicalize().ok()?;
        let metadata = std::fs::metadata(&path).ok()?;
        let me = unsafe { nix::libc::geteuid() };
        if !metadata.file_type().is_socket() || (metadata.uid() != 0 && metadata.uid() != me) {
            return None;
        }
        Some(Self {
            host: format!("unix://{}", path.to_str()?),
            device: metadata.dev(),
            inode: metadata.ino(),
        })
    }
    pub fn valid(&self) -> bool {
        Self::local(&self.host)
            .is_some_and(|now| now.device == self.device && now.inode == self.inode)
    }
    pub fn args(&self, args: &[&str]) -> Vec<String> {
        ["--host", &self.host]
            .into_iter()
            .chain(args.iter().copied())
            .map(str::to_owned)
            .collect()
    }
}
pub fn scan(
    docker: &Path,
    home: &Path,
    timeout: Duration,
    flag: &AtomicBool,
) -> Result<(Endpoint, Report), AppError> {
    // Inspect only metadata; never contact an unverified context's daemon.
    let output = output(
        docker,
        home,
        &[
            "context",
            "inspect",
            "--format",
            "{{json .Endpoints.docker.Host}}",
        ],
        timeout,
        flag,
    )?;
    let host: String = serde_json::from_slice(&output)
        .map_err(|_| AppError::new("E_DOCKER_UNAVAILABLE", "docker_endpoint"))?;
    let endpoint = Endpoint::local(&host)
        .ok_or_else(|| AppError::new("E_DOCKER_UNAVAILABLE", "docker_endpoint"))?;
    let report = report(docker, home, &endpoint, timeout, flag)?;
    Ok((endpoint, report))
}
/// Read-only usage, always against the scan's verified local endpoint.
pub fn report(
    docker: &Path,
    home: &Path,
    endpoint: &Endpoint,
    timeout: Duration,
    flag: &AtomicBool,
) -> Result<Report, AppError> {
    if !endpoint.valid() {
        return Err(AppError::new(
            "E_DOCKER_UNAVAILABLE",
            "docker_endpoint_changed",
        ));
    }
    let args = endpoint.args(&["system", "df", "--format", "{{json .}}"]);
    let args: Vec<_> = args.iter().map(String::as_str).collect();
    let output = output(docker, home, &args, timeout, flag)?;
    parse(&String::from_utf8_lossy(&output))
        .ok_or_else(|| AppError::new("E_DOCKER_UNAVAILABLE", "docker_df_format"))
}
fn output(
    docker: &Path,
    home: &Path,
    args: &[&str],
    timeout: Duration,
    flag: &AtomicBool,
) -> Result<Vec<u8>, AppError> {
    let mut child = Command::new(docker)
        .args(args)
        .env_clear()
        .envs(env(home))
        .stdin(Stdio::null())
        .stderr(Stdio::null())
        .stdout(Stdio::piped())
        .spawn()?;
    let stdout = child
        .stdout
        .take()
        .ok_or_else(|| AppError::new("E_UNKNOWN", "process_pipe"))?;
    let reader = std::thread::spawn(move || {
        let mut bytes = vec![];
        stdout
            .take(1024 * 1024)
            .read_to_end(&mut bytes)
            .map(|_| bytes)
    });
    let deadline = Instant::now() + timeout;
    let status = loop {
        if let Some(status) = child.try_wait()? {
            break status;
        }
        let canceled = flag.load(Ordering::Acquire);
        if canceled || Instant::now() >= deadline {
            let _ = child.kill();
            let _ = child.wait();
            return Err(if canceled {
                AppError::new("E_CANCELED", "cleanup_canceled")
            } else {
                AppError::new("E_DOCKER_UNAVAILABLE", "docker_timeout")
            });
        }
        std::thread::sleep(Duration::from_millis(20));
    };
    let output = reader
        .join()
        .map_err(|_| AppError::new("E_UNKNOWN", "process_pipe"))??;
    if !status.success() {
        return Err(AppError::new("E_DOCKER_UNAVAILABLE", "docker_not_running"));
    }
    Ok(output)
}
/// One JSON object per line, e.g. {"Active":"30","Reclaimable":"87.33GB (77%)","TotalCount":"325","Type":"Images"}.
pub fn parse(text: &str) -> Option<Report> {
    let mut report = Report::default();
    let mut seen = false;
    for line in text.lines().filter(|line| !line.trim().is_empty()) {
        let value: serde_json::Value = serde_json::from_str(line).ok()?;
        let field = |key: &str| value.get(key).and_then(serde_json::Value::as_str);
        let usage = Usage {
            reclaimable: size(field("Reclaimable")?)?,
            count: field("TotalCount")?.parse().ok()?,
            active: field("Active")?.parse().ok()?,
        };
        match field("Type")? {
            "Images" => report.images = usage,
            "Containers" => report.containers = usage,
            "Local Volumes" => report.volumes = usage,
            "Build Cache" => report.build_cache = usage,
            _ => continue,
        }
        seen = true;
    }
    seen.then_some(report)
}
/// Docker prints decimal units: "87.33GB (77%)", "300.1MB", "0B".
pub fn size(text: &str) -> Option<u64> {
    let number = text.split_whitespace().next()?;
    let (value, unit) = number.split_at(number.find(|c: char| c.is_ascii_alphabetic())?);
    let value: f64 = value.parse().ok()?;
    let scale = match unit {
        "B" => 1e0,
        "kB" | "KB" => 1e3,
        "MB" => 1e6,
        "GB" => 1e9,
        "TB" => 1e12,
        "PB" => 1e15,
        _ => return None,
    };
    (value.is_finite() && value >= 0.0).then(|| (value * scale).round() as u64)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn reads_docker_system_df_and_rejects_anything_else() {
        let text = r#"{"Active":"30","Reclaimable":"87.33GB (77%)","Size":"112.1GB","TotalCount":"325","Type":"Images"}
{"Active":"4","Reclaimable":"300.1MB (99%)","Size":"300.1MB","TotalCount":"47","Type":"Containers"}
{"Active":"41","Reclaimable":"24.95GB (65%)","Size":"38.05GB","TotalCount":"135","Type":"Local Volumes"}
{"Active":"22","Reclaimable":"21.1GB","Size":"117.1GB","TotalCount":"2849","Type":"Build Cache"}
"#;
        let report = parse(text).unwrap();
        assert_eq!(
            report.images,
            Usage {
                reclaimable: 87_330_000_000,
                count: 325,
                active: 30
            }
        );
        assert_eq!(report.containers.reclaimable, 300_100_000);
        assert_eq!(report.volumes.reclaimable, 24_950_000_000);
        assert_eq!(report.build_cache.reclaimable, 21_100_000_000);
        assert_eq!(size("0B"), Some(0));
        assert_eq!(size("12.5kB (3%)"), Some(12_500));
        for bad in ["", "GB", "-1GB", "1.5XB", "NaNGB"] {
            assert_eq!(size(bad), None, "{bad}");
        }
        assert_eq!(parse(""), None);
        assert_eq!(parse("Cannot connect to the Docker daemon"), None);
        assert_eq!(
            prune_args(CleanupReason::DockerImages),
            Some(&["image", "prune", "--all", "--force"][..])
        );
        assert_eq!(prune_args(CleanupReason::CacheDir), None);
    }

    #[test]
    fn the_cli_is_only_called_while_an_engine_runs() {
        let process = |path: &str| super::super::owner::CommandProcess {
            pid: 1,
            parent_pid: 1,
            executable: PathBuf::from(path),
            argv: None,
        };
        assert!(!engine_running(None));
        assert!(!engine_running(Some(&[])));
        // The CLI itself or Docker Desktop's dashboard alone does not answer `docker` commands.
        assert!(!engine_running(Some(&[
            process("/usr/local/bin/docker"),
            process(
                "/Applications/Docker.app/Contents/MacOS/Docker Desktop.app/Contents/MacOS/Docker Desktop"
            ),
        ])));
        assert!(engine_running(Some(&[process(
            "/Applications/Docker.app/Contents/MacOS/com.docker.backend"
        )])));
        assert!(engine_running(Some(&[process(
            "/Applications/OrbStack.app/Contents/MacOS/OrbStack"
        )])));
    }

    #[test]
    fn only_trusted_executables_are_used() {
        use std::os::unix::fs::PermissionsExt;
        let temporary = tempfile::tempdir().unwrap();
        let root = temporary.path().canonicalize().unwrap();
        let docker = root.join("docker");
        std::fs::write(&docker, "#!/bin/sh\n").unwrap();
        std::fs::set_permissions(&docker, std::fs::Permissions::from_mode(0o755)).unwrap();
        std::fs::set_permissions(&root, std::fs::Permissions::from_mode(0o755)).unwrap();
        assert_eq!(trusted(&docker), Some(docker.clone()));
        let link = root.join("link");
        std::os::unix::fs::symlink(&docker, &link).unwrap();
        assert_eq!(trusted(&link), Some(docker.clone()));
        std::fs::set_permissions(&docker, std::fs::Permissions::from_mode(0o775)).unwrap();
        assert_eq!(trusted(&docker), None);
        std::fs::set_permissions(&docker, std::fs::Permissions::from_mode(0o644)).unwrap();
        assert_eq!(trusted(&docker), None);
        assert_eq!(trusted(&root), None);
        assert_eq!(trusted(&root.join("missing")), None);
    }
}
