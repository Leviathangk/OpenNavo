use crate::{AppError, model::*, uninstall::CachedApp};
use std::{
    collections::BTreeSet,
    path::{Path, PathBuf},
    process::Command,
};

#[derive(Debug, Clone, Default)]
pub struct Owner {
    pub bundle: Option<String>,
    pub app_id: Option<String>,
    pub app_path: Option<PathBuf>,
    pub identifiers: Vec<String>,
    pub processes: Vec<String>,
    pub tool_only: bool,
    pub tool_key: Option<String>,
    pub arguments: Option<super::rules::ArgumentMatch>,
    pub group_key: Option<String>,
    pub group_subtitle: Option<String>,
    pub external_cache: Option<PathBuf>,
    pub known: bool,
    pub protected: bool,
}
impl Owner {
    pub fn app(bundle: &str, apps: &[CachedApp]) -> Self {
        let found = apps.iter().find(|a| {
            a.app.bundle_id.as_deref() == Some(bundle)
                || a.identity.component_ids.iter().any(|id| id == bundle)
        });
        Self {
            bundle: Some(bundle.into()),
            app_id: found.map(|a| a.app.id.clone()),
            app_path: found.map(|a| PathBuf::from(&a.app.path)),
            identifiers: found
                .map(|a| a.identity.component_ids.iter().cloned().collect())
                .unwrap_or_default(),
            processes: match bundle {
                "com.google.Chrome" => vec![
                    "Google Chrome",
                    "Google Chrome Helper",
                    "Google Chrome Helper (Renderer)",
                    "Google Chrome Helper (GPU)",
                ],
                "com.microsoft.edgemac" => vec![
                    "Microsoft Edge",
                    "Microsoft Edge Helper",
                    "Microsoft Edge Helper (Renderer)",
                    "Microsoft Edge Helper (GPU)",
                ],
                "company.thebrowser.Browser" => vec![
                    "Arc",
                    "Arc Helper",
                    "Arc Helper (Renderer)",
                    "Arc Helper (GPU)",
                ],
                "com.brave.Browser" => vec![
                    "Brave Browser",
                    "Brave Browser Helper",
                    "Brave Browser Helper (Renderer)",
                    "Brave Browser Helper (GPU)",
                ],
                "org.mozilla.firefox" => vec!["firefox", "plugin-container"],
                "com.apple.Safari" => vec![
                    "Safari",
                    "com.apple.WebKit.Networking",
                    "com.apple.WebKit.WebContent",
                    "com.apple.WebKit.GPU",
                ],
                _ => vec![],
            }
            .into_iter()
            .map(str::to_owned)
            .collect(),
            known: true,
            protected: super::rules::sensitive_name(bundle)
                || found.is_some_and(|a| {
                    super::rules::sensitive_name(&a.app.name)
                        || a.app
                            .bundle_id
                            .as_deref()
                            .is_some_and(super::rules::sensitive_name)
                }),
            tool_only: false,
            tool_key: None,
            arguments: None,
            group_key: None,
            group_subtitle: None,
            external_cache: None,
        }
    }
    pub fn tool(processes: &[&str]) -> Self {
        Self {
            processes: processes.iter().map(|s| (*s).into()).collect(),
            known: true,
            tool_only: true,
            ..Self::default()
        }
    }
    pub fn status(&self, engine: &super::Engine) -> Option<SkippedGroupReason> {
        self.status_in(&Activity::read(engine))
    }

    fn matches_bundle(&self, id: &str) -> bool {
        if self.tool_only {
            return false;
        }
        self.bundle
            .as_ref()
            .is_some_and(|b| id == b || id.starts_with(&format!("{b}.")))
            || self.identifiers.iter().any(|b| id == b)
    }
    fn matches_path(&self, path: &Path) -> bool {
        (!self.tool_only
            && (self
                .app_path
                .as_ref()
                .is_some_and(|root| path.starts_with(root))
                || path.components().any(|c| {
                    self.matches_bundle(c.as_os_str().to_string_lossy().trim_end_matches(".app"))
                })))
            || path.file_name().is_some_and(|name| {
                let name = name.to_string_lossy();
                let name = name.strip_suffix(".app").unwrap_or(&name);
                self.processes
                    .iter()
                    .any(|p| name == p || (p == "pip3" && numbered(name, "pip3.")))
            })
    }
    pub fn display_title(&self, activity: &Result<Activity, AppError>, fallback: &str) -> String {
        if self.app_id.is_some() || self.tool_only {
            return fallback.into();
        }
        activity
            .as_ref()
            .ok()
            .and_then(|activity| {
                activity
                    .apps
                    .iter()
                    .filter(|p| self.matches_bundle(&p.bundle_id) || self.matches_path(&p.path))
                    .find_map(|p| app_name(&p.path))
                    .or_else(|| {
                        activity
                            .processes
                            .iter()
                            .filter(|p| self.matches_path(&p.executable))
                            .find_map(|p| app_name(&p.executable))
                    })
            })
            .unwrap_or_else(|| fallback.into())
    }
}
fn app_name(path: &Path) -> Option<String> {
    path.ancestors()
        .filter(|p| p.extension().is_some_and(|e| e == "app"))
        .last()
        .and_then(|p| p.file_stem())
        .map(|s| s.to_string_lossy().into_owned())
}

fn numbered(name: &str, prefix: &str) -> bool {
    name.strip_prefix(prefix).is_some_and(|v| {
        !v.is_empty()
            && v.bytes().all(|b| b.is_ascii_digit() || b == b'.')
            && v.bytes().any(|b| b.is_ascii_digit())
    })
}
fn python(name: &str) -> bool {
    matches!(name, "python" | "python3") || numbered(name, "python3.")
}
fn pip(name: &str) -> bool {
    matches!(name, "pip" | "pip3") || numbered(name, "pip3.")
}
fn argument_name(arg: &str) -> Option<&str> {
    if arg.starts_with('-') || arg.contains('=') || arg.contains("://") {
        return None;
    }
    Path::new(arg).file_name()?.to_str()
}
fn argument_runtime(name: &str) -> bool {
    matches!(
        name,
        "node" | "nodejs" | "ruby" | "go" | "java" | "cargo" | "bun" | "uv"
    ) || python(name)
}
/// Skip options and their values without treating a redacted value as a subcommand.
fn subcommand(argv: &[String]) -> Option<&str> {
    let mut args = argv.iter().skip(1).map(String::as_str);
    while let Some(arg) = args.next() {
        if arg == "--cwd" {
            args.next()?;
        } else if arg == "--" {
            return args.next();
        } else if !arg.starts_with('-') && !arg.starts_with('+') {
            return Some(arg);
        }
    }
    None
}
/// Long-running `run` commands keep the process alive after the tool is done with its caches.
const CARGO_IDLE: &[&str] = &["run"];
const BUN_ACTIVE: &[&str] = &[
    "install", "ci", "i", "add", "a", "update", "remove", "rm", "pm", "link", "x",
];
const UV_IDLE: &[&str] = &["run"];
const GO_COMMANDS: &[&str] = &[
    "build", "install", "test", "vet", "generate", "get", "mod", "work", "clean", "tool", "list",
];
const GO_TOOLS: &[&str] = &[
    "compile", "link", "asm", "cgo", "vet", "cover", "buildid", "pack",
];
fn go_subcommand(argv: &[String]) -> Option<&str> {
    let mut i = 1;
    while let Some(arg) = argv.get(i) {
        if arg == "-C" {
            i += 2;
        } else if arg.starts_with("-C=") {
            i += 1;
        } else {
            return Some(arg);
        }
    }
    None
}
fn go_children<'a>(process: &CommandProcess, all: &'a [CommandProcess]) -> Vec<&'a CommandProcess> {
    if process.pid <= 0 {
        return vec![];
    }
    let mut ancestors = BTreeSet::from([process.pid]);
    loop {
        let before = ancestors.len();
        for child in all {
            if child.pid > 0 && ancestors.contains(&child.parent_pid) {
                ancestors.insert(child.pid);
            }
        }
        if before == ancestors.len() {
            break;
        }
    }
    all.iter()
        .filter(|p| {
            p.pid != process.pid && ancestors.contains(&p.pid) && GO_TOOLS.contains(&p.comm())
        })
        .collect()
}
impl super::rules::ArgumentMatch {
    fn runtime(self, path: &Path) -> bool {
        let name = path.file_name().unwrap_or_default().to_string_lossy();
        match self {
            Self::Npm | Self::Yarn => matches!(name.as_ref(), "node" | "nodejs"),
            Self::Pip => python(&name),
            Self::Pod | Self::Brew => name == "ruby",
            Self::Go => name == "go",
            Self::Gradle | Self::Maven => name == "java",
            Self::Cargo => matches!(name.as_ref(), "cargo" | "rustc"),
            Self::Bun => name == "bun",
            Self::Uv => name == "uv",
        }
    }
    fn matches(self, process: &CommandProcess, all: &[CommandProcess]) -> bool {
        let name = process
            .executable
            .file_name()
            .unwrap_or_default()
            .to_string_lossy();
        if matches!(self, Self::Pip) && pip(&name) {
            return true;
        }
        if matches!(self, Self::Go) {
            return name == "go"
                && (!go_children(process, all).is_empty()
                    || process
                        .argv
                        .as_ref()
                        .and_then(|args| go_subcommand(args))
                        .is_some_and(|cmd| GO_COMMANDS.contains(&cmd)));
        }
        // rustc only runs while something is being compiled; the Rust cache is busy whatever started it.
        if matches!(self, Self::Cargo) && name == "rustc" {
            return true;
        }
        let Some(argv) = &process.argv else {
            return false;
        };
        let java = |classes: &[&str]| {
            name == "java"
                && argv
                    .iter()
                    .any(|arg| classes.iter().any(|c| arg.contains(c)))
        };
        match self {
            Self::Gradle => {
                return java(&[
                    "org.gradle.launcher",
                    "org.gradle.wrapper.GradleWrapperMain",
                ]);
            }
            Self::Maven => {
                return java(&[
                    "org.codehaus.plexus.classworlds.launcher.Launcher",
                    "-Dmaven.home=",
                ]);
            }
            Self::Cargo => {
                return name == "cargo"
                    && subcommand(argv).is_some_and(|cmd| !CARGO_IDLE.contains(&cmd));
            }
            Self::Bun => {
                return (name == "bun"
                    && subcommand(argv).is_some_and(|cmd| BUN_ACTIVE.contains(&cmd)))
                    || name == "bunx";
            }
            Self::Uv => {
                return name == "uv" && subcommand(argv).is_some_and(|cmd| !UV_IDLE.contains(&cmd));
            }
            _ => {}
        }
        match self {
            Self::Pip => {
                (python(&name) && argv.windows(2).any(|a| a[0] == "-m" && a[1] == "pip"))
                    || argv.iter().enumerate().any(|(i, arg)| {
                        (i == 0 || arg.contains('/')) && argument_name(arg).is_some_and(pip)
                    })
            }
            rule => {
                let names: &[&str] = match rule {
                    Self::Npm => &["npm", "npm-cli.js", "npx", "npx-cli.js"],
                    Self::Yarn => &["yarn", "yarn.js", "yarn.cjs"],
                    Self::Pod => &["pod"],
                    Self::Brew => &["brew", "brew.rb"],
                    Self::Pip
                    | Self::Go
                    | Self::Gradle
                    | Self::Maven
                    | Self::Cargo
                    | Self::Bun
                    | Self::Uv => unreachable!(),
                };
                argv.iter()
                    .any(|arg| argument_name(arg).is_some_and(|name| names.contains(&name)))
            }
        }
    }
}

#[cfg(target_os = "macos")]
fn read_arguments(pid: i32) -> Result<Vec<String>, std::io::Error> {
    let mut mib = [nix::libc::CTL_KERN, nix::libc::KERN_PROCARGS2, pid];
    let mut buffer = vec![0u8; 1024 * 1024];
    let mut length = buffer.len();
    // Darwin KERN_PROCARGS2 returns argc, executable, NUL padding, then argv and environment.
    // Parse only argc tokens; neither environment nor unrelated arguments leave this function.
    let result = unsafe {
        nix::libc::sysctl(
            mib.as_mut_ptr(),
            mib.len() as u32,
            buffer.as_mut_ptr().cast(),
            &mut length,
            std::ptr::null_mut(),
            0,
        )
    };
    if result != 0 {
        return Err(std::io::Error::last_os_error());
    }
    if length > buffer.len() {
        return Err(std::io::Error::other("process_arguments_limit"));
    }
    buffer.truncate(length);
    parse_arguments(&buffer)
}
#[cfg(not(target_os = "macos"))]
fn read_arguments(_: i32) -> Result<Vec<String>, std::io::Error> {
    Err(std::io::Error::new(
        std::io::ErrorKind::Unsupported,
        "process_arguments",
    ))
}
#[cfg(any(target_os = "macos", test))]
fn parse_arguments(buffer: &[u8]) -> Result<Vec<String>, std::io::Error> {
    let invalid = || std::io::Error::new(std::io::ErrorKind::InvalidData, "process_arguments");
    let argc = i32::from_ne_bytes(
        buffer
            .get(..4)
            .ok_or_else(invalid)?
            .try_into()
            .map_err(|_| invalid())?,
    );
    if !(0..=65_536).contains(&argc) {
        return Err(invalid());
    }
    let mut offset = 4;
    offset += buffer
        .get(offset..)
        .ok_or_else(invalid)?
        .iter()
        .position(|b| *b == 0)
        .ok_or_else(invalid)?
        + 1;
    while buffer.get(offset) == Some(&0) {
        offset += 1;
    }
    let mut args = vec![];
    for _ in 0..argc {
        let end = buffer
            .get(offset..)
            .ok_or_else(invalid)?
            .iter()
            .position(|b| *b == 0)
            .ok_or_else(invalid)?;
        let arg = String::from_utf8_lossy(&buffer[offset..offset + end]);
        // Retain only matching markers, preserving positions for Python's '-m pip'.
        let marker = if matches!(arg.as_ref(), "-m" | "-C" | "--cwd" | "--") {
            arg.as_ref()
        } else if arg.starts_with("-C=") {
            "-C="
        } else if arg.starts_with("-Dmaven.home=") {
            "-Dmaven.home="
        } else if let Some(class) = [
            "org.gradle.launcher",
            "org.gradle.wrapper.GradleWrapperMain",
            "org.codehaus.plexus.classworlds.launcher.Launcher",
        ]
        .into_iter()
        .find(|class| arg.contains(class))
        {
            class
        } else if GO_COMMANDS.contains(&arg.as_ref())
            || BUN_ACTIVE.contains(&arg.as_ref())
            || arg == "run"
            || arg == "go"
        {
            arg.as_ref()
        } else {
            argument_name(&arg)
                .filter(|name| {
                    pip(name)
                        || [
                            "npm",
                            "npm-cli.js",
                            "npx",
                            "npx-cli.js",
                            "yarn",
                            "yarn.js",
                            "yarn.cjs",
                            "pod",
                            "brew",
                            "brew.rb",
                        ]
                        .contains(name)
                })
                // Preserve flag/toolchain positions without retaining their values.
                .unwrap_or(if arg.starts_with('-') {
                    "-"
                } else if arg.starts_with('+') {
                    "+"
                } else {
                    ""
                })
        };
        args.push(if pip(marker) && arg.contains('/') {
            format!("/{marker}")
        } else {
            marker.into()
        });
        offset += end + 1;
    }
    Ok(args)
}

#[derive(Clone)]
pub struct CommandProcess {
    pub pid: i32,
    pub parent_pid: i32,
    pub executable: PathBuf,
    /// Exact argv tokens; None means unavailable, not an empty argument list.
    pub argv: Option<Vec<String>>,
}
impl CommandProcess {
    pub fn comm(&self) -> &str {
        self.executable
            .file_name()
            .and_then(|n| n.to_str())
            .unwrap_or("")
    }
}
impl std::fmt::Debug for CommandProcess {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("CommandProcess")
            .field("executable", &self.executable)
            .field("argv_available", &self.argv.is_some())
            .finish()
    }
}
pub trait ProcessProbe: Send + Sync {
    fn list(&self) -> Result<Vec<CommandProcess>, AppError>;
    fn open_paths(&self) -> Result<Vec<PathBuf>, AppError> {
        Ok(vec![])
    }
    fn open_files(&self) -> Result<Vec<(PathBuf, Option<i32>)>, AppError> {
        Ok(self.open_paths()?.into_iter().map(|p| (p, None)).collect())
    }
    fn user_cache_dir(&self) -> Result<Option<PathBuf>, AppError> {
        Ok(None)
    }
    fn user_temp_dir(&self) -> Result<Option<PathBuf>, AppError> {
        Ok(None)
    }
    /// A trusted Docker CLI, if one is installed.
    fn docker(&self, _home: &Path) -> Option<PathBuf> {
        None
    }
}
pub struct NativeProcesses;
impl ProcessProbe for NativeProcesses {
    fn list(&self) -> Result<Vec<CommandProcess>, AppError> {
        let (code, output) = read_output("/bin/ps", &["-axww", "-o", "pid=,ppid=,comm="])?;
        if code != 0 {
            return Err(AppError::new("E_UNKNOWN", "cleanup_process_probe"));
        }
        let mut processes = vec![];
        for line in String::from_utf8_lossy(&output).lines() {
            let Some((pid, rest)) = line.trim().split_once(char::is_whitespace) else {
                continue;
            };
            let Some((parent_pid, executable)) = rest.trim().split_once(char::is_whitespace) else {
                continue;
            };
            let (Ok(pid), Ok(parent_pid)) = (pid.parse::<i32>(), parent_pid.parse::<i32>()) else {
                return Err(AppError::new("E_UNKNOWN", "cleanup_process_probe"));
            };
            let executable = PathBuf::from(executable.trim());
            let name = executable.file_name().unwrap_or_default().to_string_lossy();
            // Only interpreters used by these tool rules need argv; never retain arbitrary args or environment values.
            let argv = if argument_runtime(&name) {
                match read_arguments(pid) {
                    Ok(args) => Some(args),
                    Err(error) if error.raw_os_error() == Some(nix::libc::ESRCH) => continue,
                    Err(_) => None,
                }
            } else {
                Some(vec![])
            };
            processes.push(CommandProcess {
                pid,
                parent_pid,
                executable,
                argv,
            });
        }
        Ok(processes)
    }
    fn open_paths(&self) -> Result<Vec<PathBuf>, AppError> {
        Ok(self.open_files()?.into_iter().map(|(p, _)| p).collect())
    }
    fn open_files(&self) -> Result<Vec<(PathBuf, Option<i32>)>, AppError> {
        let user = unsafe { nix::libc::geteuid() }.to_string();
        let (code, output) = read_output("/usr/sbin/lsof", &["-n", "-P", "-F", "pn", "-u", &user])?;
        if code != 0 {
            return Err(AppError::new("E_UNKNOWN", "cleanup_open_files_probe"));
        }
        let mut pid = None;
        let mut paths = vec![];
        for line in String::from_utf8_lossy(&output).lines() {
            if let Some(value) = line.strip_prefix('p') {
                pid = value.parse().ok();
            } else if let Some(path) = line.strip_prefix("n/") {
                paths.push((PathBuf::from(format!("/{path}")), pid));
            }
        }
        Ok(paths)
    }
    fn user_cache_dir(&self) -> Result<Option<PathBuf>, AppError> {
        darwin_user_dir(65538) // _CS_DARWIN_USER_CACHE_DIR, Darwin unistd.h
    }
    fn user_temp_dir(&self) -> Result<Option<PathBuf>, AppError> {
        darwin_user_dir(65537) // _CS_DARWIN_USER_TEMP_DIR, Darwin unistd.h
    }
    fn docker(&self, home: &Path) -> Option<PathBuf> {
        super::docker::locate(home)
    }
}
/// The per-user folder the OS keeps under /var/folders for a confstr name, canonicalized (it is reached via /var).
fn darwin_user_dir(name: i32) -> Result<Option<PathBuf>, AppError> {
    #[cfg(target_os = "macos")]
    {
        let size = unsafe { nix::libc::confstr(name, std::ptr::null_mut(), 0) };
        if size == 0 || size > 65536 {
            return Err(AppError::new("E_UNKNOWN", "darwin_user_dir"));
        }
        let mut path = vec![0; size];
        if unsafe { nix::libc::confstr(name, path.as_mut_ptr().cast(), size) } != size {
            return Err(AppError::new("E_UNKNOWN", "darwin_user_dir"));
        }
        use std::os::unix::ffi::OsStringExt;
        path.pop();
        let path = PathBuf::from(std::ffi::OsString::from_vec(path));
        // Resolve the OS-provided /var alias; fixed leaves below it are never canonicalized.
        Ok(Some(path.canonicalize()?))
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = name;
        Ok(None)
    }
}

#[derive(Debug, Clone)]
pub struct Activity {
    pub apps: Vec<crate::running_apps::Process>,
    pub processes: Vec<CommandProcess>,
    pub open_paths: Vec<(PathBuf, Option<i32>)>,
    pub apps_complete: bool,
    pub processes_complete: bool,
    pub open_complete: bool,
}
impl Activity {
    pub fn read(engine: &super::Engine) -> Result<Self, AppError> {
        // A failed lsof must not discard the independently acquired process tables.
        let apps = engine.uninstall.control.list();
        let processes = engine.processes.list();
        let open = engine.processes.open_files();
        Ok(Self {
            apps_complete: apps.is_ok(),
            processes_complete: processes.is_ok(),
            open_complete: open.is_ok(),
            apps: apps.unwrap_or_default(),
            processes: processes.unwrap_or_default(),
            open_paths: open.unwrap_or_default(),
        })
    }
    pub fn in_use(&self, path: &Path) -> bool {
        self.open_paths
            .iter()
            .any(|(open, _)| open.starts_with(path))
    }
}
impl Owner {
    pub fn status_in(&self, activity: &Result<Activity, AppError>) -> Option<SkippedGroupReason> {
        let Ok(activity) = activity else {
            return Some(SkippedGroupReason::InUse);
        };
        if activity
            .apps
            .iter()
            .any(|p| self.matches_bundle(&p.bundle_id) || self.matches_path(&p.path))
            || activity.processes.iter().any(|p| {
                self.matches_path(&p.executable)
                    || self
                        .arguments
                        .is_some_and(|rule| rule.matches(p, &activity.processes))
            })
        {
            Some(SkippedGroupReason::Running)
        } else if (!self.known && !activity.open_complete)
            || (self.known && !activity.processes_complete)
            || (self.app_id.is_some() && !self.tool_only && !activity.apps_complete)
            || activity.processes.iter().any(|p| {
                p.argv.is_none()
                    && self
                        .arguments
                        .is_some_and(|rule| rule.runtime(&p.executable))
            })
        {
            Some(SkippedGroupReason::InUse)
        } else {
            None
        }
    }
    pub fn blocking_processes(
        &self,
        activity: &Result<Activity, AppError>,
        path: &Path,
        reason: SkippedGroupReason,
    ) -> Vec<String> {
        let Ok(activity) = activity else {
            return vec![];
        };
        let mut names = BTreeSet::new();
        if matches!(
            reason,
            SkippedGroupReason::Running | SkippedGroupReason::Busy
        ) {
            for process in &activity.processes {
                if self.matches_path(&process.executable)
                    || self
                        .arguments
                        .is_some_and(|r| r.matches(process, &activity.processes))
                {
                    names.insert(process.comm().to_owned());
                    if matches!(self.arguments, Some(super::rules::ArgumentMatch::Go)) {
                        names.extend(
                            go_children(process, &activity.processes)
                                .into_iter()
                                .map(|p| p.comm().to_owned()),
                        );
                    }
                }
            }
            for app in &activity.apps {
                if self.matches_bundle(&app.bundle_id) || self.matches_path(&app.path) {
                    let name = activity
                        .processes
                        .iter()
                        .find(|p| p.pid == app.pid && p.pid > 0)
                        .map(|p| p.comm().to_owned());
                    if let Some(name) = name {
                        names.insert(name);
                    }
                }
            }
        } else if reason == SkippedGroupReason::InUse {
            for process in &activity.processes {
                if (process.argv.is_none()
                    && self
                        .arguments
                        .is_some_and(|r| r.runtime(&process.executable)))
                    || activity
                        .open_paths
                        .iter()
                        .any(|(open, pid)| open.starts_with(path) && *pid == Some(process.pid))
                {
                    names.insert(process.comm().to_owned());
                }
            }
        }
        names.remove("");
        names.into_iter().collect()
    }
}
fn read_output(program: &str, args: &[&str]) -> Result<(i32, Vec<u8>), AppError> {
    use std::{
        io::Read,
        process::Stdio,
        time::{Duration, Instant},
    };
    let mut child = Command::new(program)
        .args(args)
        .env_clear()
        .stdin(Stdio::null())
        .stderr(Stdio::null())
        .stdout(Stdio::piped())
        .spawn()?;
    let stdout = child
        .stdout
        .take()
        .ok_or_else(|| AppError::new("E_UNKNOWN", "process_pipe"))?;
    let limit = if program == "/usr/sbin/lsof" {
        32 * 1024 * 1024
    } else {
        8 * 1024 * 1024
    };
    let timeout = if program == "/usr/sbin/lsof" { 15 } else { 3 };
    let reader = std::thread::spawn(move || {
        let mut bytes = vec![];
        stdout
            .take(limit as u64 + 1)
            .read_to_end(&mut bytes)
            .map(|_| bytes)
    });
    let deadline = Instant::now() + Duration::from_secs(timeout);
    let result = loop {
        match child.try_wait() {
            Ok(Some(status)) => break Ok(status.code().unwrap_or(-1)),
            Ok(None) if Instant::now() < deadline => std::thread::sleep(Duration::from_millis(10)),
            Ok(None) => break Err(AppError::new("E_TIMEOUT", "cleanup_process_probe")),
            Err(error) => break Err(error.into()),
        }
    };
    if result.is_err() {
        let _ = child.kill();
        let _ = child.wait();
    }
    let bytes = reader
        .join()
        .map_err(|_| AppError::new("E_UNKNOWN", "process_reader"))??;
    if bytes.len() > limit {
        return Err(AppError::new("E_UNKNOWN", "process_output_limit"));
    }
    Ok((result?, bytes))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::cleanup::rules::ArgumentMatch;

    fn arguments(args: &[&str]) -> Vec<String> {
        let mut buffer = (args.len() as i32).to_ne_bytes().to_vec();
        buffer.extend_from_slice(b"/synthetic/executable\0\0\0");
        for arg in args {
            buffer.extend_from_slice(arg.as_bytes());
            buffer.push(0);
        }
        buffer.extend_from_slice(b"SECRET=never-retain\0npm-cli.js\0");
        parse_arguments(&buffer).unwrap()
    }

    #[test]
    fn bun_ci_protects_cache_after_native_argument_redaction() {
        for (args, active) in [
            (vec!["bun", "ci"], true),
            (vec!["bun", "--cwd", "/tmp/private-project", "ci"], true),
            (vec!["bun", "--cwd=/tmp/private-project", "ci"], true),
            (vec!["bun", "run", "ci"], false),
            (vec!["bun", "app.ts", "ci"], false),
        ] {
            let process = CommandProcess {
                pid: 1,
                parent_pid: 0,
                executable: "bun".into(),
                argv: Some(arguments(&args)),
            };
            assert_eq!(
                ArgumentMatch::Bun.matches(&process, &[]),
                active,
                "{args:?}"
            );
        }
    }

    #[test]
    fn bun_cwd_values_are_not_subcommands_after_redaction() {
        for (args, active) in [
            (
                vec!["bun", "--cwd", "/tmp/private-project", "install"],
                true,
            ),
            (vec!["bun", "--cwd", "run", "install"], true),
            (vec!["bun", "--cwd=/tmp/private-project", "install"], true),
            (
                vec!["bun", "--cwd", "/tmp/private-project", "--offline", "add"],
                true,
            ),
            (
                vec!["bun", "--cwd", "/tmp/private-project", "run", "install"],
                false,
            ),
            (vec!["bun", "--cwd", "install", "run"], false),
            (
                vec!["bun", "--cwd", "/tmp/private-project", "app.ts", "install"],
                false,
            ),
            (vec!["bun", "--cwd"], false),
        ] {
            let argv = arguments(&args);
            assert!(!argv.join(" ").contains("private-project"));
            let process = CommandProcess {
                pid: 1,
                parent_pid: 0,
                executable: "bun".into(),
                argv: Some(argv),
            };
            assert_eq!(
                ArgumentMatch::Bun.matches(&process, &[]),
                active,
                "{args:?}"
            );
        }
    }

    #[test]
    fn native_build_markers_survive_redaction() {
        for (rule, args) in [
            (
                ArgumentMatch::Gradle,
                vec!["java", "org.gradle.launcher.daemon.bootstrap.GradleDaemon"],
            ),
            (
                ArgumentMatch::Gradle,
                vec!["java", "org.gradle.wrapper.GradleWrapperMain"],
            ),
            (
                ArgumentMatch::Maven,
                vec!["java", "org.codehaus.plexus.classworlds.launcher.Launcher"],
            ),
            (
                ArgumentMatch::Maven,
                vec!["java", "-Dmaven.home=/private/secret"],
            ),
            (
                ArgumentMatch::Bun,
                vec!["bun", "--cwd=/private/secret", "add"],
            ),
            (ArgumentMatch::Cargo, vec!["cargo", "+nightly", "build"]),
        ] {
            let argv = arguments(&args);
            assert!(!argv.join(" ").contains("secret"));
            let process = CommandProcess {
                pid: 1,
                parent_pid: 0,
                executable: args[0].into(),
                argv: Some(argv),
            };
            assert!(rule.matches(&process, &[]), "{args:?}");
        }
        for command in BUN_ACTIVE {
            let process = CommandProcess {
                pid: 1,
                parent_pid: 0,
                executable: "bun".into(),
                argv: Some(arguments(&["bun", command])),
            };
            assert!(ArgumentMatch::Bun.matches(&process, &[]), "{command}");
        }
        for (rule, name) in [
            (ArgumentMatch::Cargo, "cargo"),
            (ArgumentMatch::Uv, "uv"),
            (ArgumentMatch::Bun, "bun"),
        ] {
            let process = CommandProcess {
                pid: 1,
                parent_pid: 0,
                executable: name.into(),
                argv: Some(arguments(&[name, "--offline", "run", "add"])),
            };
            assert!(!rule.matches(&process, &[]));
        }
    }

    #[test]
    fn go_native_argv_keeps_subcommand_boundaries_and_all_descendant_tool_names() {
        for args in [
            vec!["go", "build", "./cmd/api"],
            vec!["go", "-C", "/project/private", "build"],
            vec!["go", "-C=/project/private", "test"],
        ] {
            let process = CommandProcess {
                pid: 100,
                parent_pid: 1,
                executable: "/synthetic/go".into(),
                argv: Some(arguments(&args)),
            };
            assert!(ArgumentMatch::Go.matches(&process, &[]));
            assert!(!format!("{process:?}").contains("private"));
        }
        let process = CommandProcess {
            pid: 100,
            parent_pid: 1,
            executable: "/synthetic/go".into(),
            argv: Some(arguments(&["go", "run", "./cmd/api", "build"])),
        };
        assert!(!ArgumentMatch::Go.matches(&process, &[]));
        for tool in GO_TOOLS {
            let child = CommandProcess {
                pid: 101,
                parent_pid: 100,
                executable: PathBuf::from("/synthetic/tool").join(tool),
                argv: Some(vec![]),
            };
            assert!(ArgumentMatch::Go.matches(&process, &[child]), "{tool}");
        }
    }

    #[test]
    fn native_arguments_keep_only_exact_markers_not_environment_or_secrets() {
        let argv = arguments(&[
            "node",
            "/path with spaces/npm-cli.js",
            "--token=secret",
            "/opt/npm-global/index.js",
            "pnpm-lock.yaml",
        ]);
        assert_eq!(argv, ["", "npm-cli.js", "-", "", ""]);
        let process = CommandProcess {
            pid: 0,
            parent_pid: 0,
            executable: "/synthetic/node".into(),
            argv: Some(argv),
        };
        assert!(ArgumentMatch::Npm.matches(&process, &[]));
        let debug = format!("{process:?}");
        assert!(!debug.contains("npm-cli.js"));
        assert!(!debug.contains("secret"));
        let process = CommandProcess {
            pid: 0,
            parent_pid: 0,
            executable: "/synthetic/node".into(),
            argv: Some(arguments(&[
                "node",
                "/opt/npm-global/index.js",
                "pnpm-lock.yaml",
            ])),
        };
        assert!(!ArgumentMatch::Npm.matches(&process, &[]));
        assert!(parse_arguments(&[0, 0]).is_err());
    }

    #[test]
    fn native_python_markers_preserve_module_and_executable_boundaries() {
        for argv in [
            vec!["python3", "-m", "pip"],
            vec!["python3", "/path with spaces/pip3.12"],
        ] {
            let process = CommandProcess {
                pid: 0,
                parent_pid: 0,
                executable: "/synthetic/python3".into(),
                argv: Some(arguments(&argv)),
            };
            assert!(ArgumentMatch::Pip.matches(&process, &[]));
        }
        let process = CommandProcess {
            pid: 0,
            parent_pid: 0,
            executable: "/synthetic/python3".into(),
            argv: Some(arguments(&["python3", "script.py", "pip"])),
        };
        assert!(!ArgumentMatch::Pip.matches(&process, &[]));
    }

    #[test]
    fn unavailable_runtime_argv_fails_closed_only_for_relevant_tools() {
        let activity = Ok(Activity {
            apps: vec![],
            open_paths: vec![],
            apps_complete: true,
            processes_complete: true,
            open_complete: true,
            processes: vec![CommandProcess {
                pid: 0,
                parent_pid: 0,
                executable: "/synthetic/node".into(),
                argv: None,
            }],
        });
        let mut npm = Owner::tool(&["npm", "npx"]);
        npm.arguments = Some(ArgumentMatch::Npm);
        assert_eq!(npm.status_in(&activity), Some(SkippedGroupReason::InUse));
        let mut pip = Owner::tool(&["pip", "pip3"]);
        pip.arguments = Some(ArgumentMatch::Pip);
        assert_eq!(pip.status_in(&activity), None);
    }
}
