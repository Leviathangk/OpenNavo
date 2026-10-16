//! Root daemon entry point. Never launched by tests or cargo gen.
/// The file the helper was started from. An app update swaps the whole bundle, so the path then names the new
/// helper while this process keeps running the deleted one, and launchd never restarts a KeepAlive daemon for
/// that. Such a process can no longer prove its own signature, so every app connection fails.
pub struct Executable {
    path: std::path::PathBuf,
    id: (u64, u64),
}
impl Executable {
    pub fn at(path: std::path::PathBuf) -> std::io::Result<Self> {
        let id = identity(&std::fs::metadata(&path)?);
        Ok(Self { path, id })
    }
    /// Another file has taken the path. A missing file is no replacement (yet): launchd would have nothing to start.
    pub fn replaced(&self) -> bool {
        std::fs::metadata(&self.path).is_ok_and(|now| identity(&now) != self.id)
    }
}
fn identity(metadata: &std::fs::Metadata) -> (u64, u64) {
    use std::os::unix::fs::MetadataExt;
    (metadata.dev(), metadata.ino())
}
#[cfg(target_os = "macos")]
mod mac {
    use super::super::{
        bridge,
        control::{Controller, Reply, Request},
        hardware::{Family, SmcHardware, ThreadDelay},
    };
    use super::Executable;
    use crate::{
        AppError,
        monitor::{
            smc::NativeSmc,
            system::{self, NativeSystemSource},
        },
    };
    use std::sync::{Mutex, OnceLock};
    type Control = Controller<SmcHardware<NativeSmc, ThreadDelay>>;
    static CONTROL: OnceLock<Mutex<Control>> = OnceLock::new();
    static TERMINATE: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);
    extern "C" fn terminate(_: i32) {
        TERMINATE.store(true, std::sync::atomic::Ordering::Release);
    }
    static ORIGIN: OnceLock<std::time::Instant> = OnceLock::new();
    fn now() -> u64 {
        ORIGIN
            .get_or_init(std::time::Instant::now)
            .elapsed()
            .as_millis() as u64
    }
    unsafe extern "C" fn request(
        text: *const std::ffi::c_char,
        authorized: i32,
    ) -> *mut std::ffi::c_char {
        // No Rust unwinding crosses XPC's C block. Returned allocation is freed by the bridge.
        let result = std::panic::catch_unwind(|| -> Result<Reply, AppError> {
            if authorized != 1 {
                return Err(AppError::new("E_FAN_UNAUTHORIZED", "audit_token"));
            }
            let input = unsafe { std::ffi::CStr::from_ptr(text) }.to_bytes();
            let request: Request = serde_json::from_slice(input)
                .map_err(|_| AppError::new("E_INVALID_ARG", "helper_request"))?;
            CONTROL
                .get()
                .ok_or_else(super::super::client::unavailable)?
                .lock()
                .map_err(|_| super::super::client::unavailable())?
                .request(request, now(), true)
        })
        .unwrap_or_else(|_| Err(super::super::client::unavailable()));
        let json=serde_json::to_string(&result).unwrap_or_else(|_|"{\"Err\":{\"code\":\"E_FAN_HELPER_UNAVAILABLE\",\"messageKey\":\"helper\",\"detail\":null}}".into());
        let Ok(text) = std::ffi::CString::new(json) else {
            return std::ptr::null_mut();
        };
        unsafe { nix::libc::strdup(text.as_ptr()) }
    }
    extern "C" fn sleep() {
        if let Some(control) = CONTROL.get()
            && let Ok(mut control) = control.lock()
        {
            control.recover(crate::monitor::types::FanReason::Sleep);
        }
    }
    /// Forwards `log` records (SMC write failures, startup errors) to the unified log.
    struct UnifiedLog;
    impl log::Log for UnifiedLog {
        fn enabled(&self, metadata: &log::Metadata) -> bool {
            metadata.level() <= log::Level::Info
        }
        fn log(&self, record: &log::Record) {
            if let Ok(text) = std::ffi::CString::new(record.args().to_string()) {
                unsafe { bridge::on_helper_log(text.as_ptr()) }
            }
        }
        fn flush(&self) {}
    }
    static LOG: UnifiedLog = UnifiedLog;
    /// Our file as the kernel resolves it; launchd passes only the bundle-relative path as argv[0].
    fn own_path() -> Option<std::path::PathBuf> {
        use std::os::unix::ffi::OsStrExt;
        let mut buffer = [0u8; nix::libc::PROC_PIDPATHINFO_MAXSIZE as usize];
        // SAFETY: buffer size is PROC_PIDPATHINFO_MAXSIZE; read only the returned length.
        let length = unsafe {
            nix::libc::proc_pidpath(
                nix::libc::getpid(),
                buffer.as_mut_ptr().cast(),
                buffer.len() as u32,
            )
        };
        let length = usize::try_from(length).ok().filter(|length| *length > 0)?;
        Some(std::ffi::OsStr::from_bytes(&buffer[..length]).into())
    }
    pub fn run() -> Result<(), AppError> {
        if log::set_logger(&LOG).is_ok() {
            log::set_max_level(log::LevelFilter::Info);
        }
        let result = start();
        if let Err(error) = &result {
            log::error!("OpenNavo fan helper stopped: {}", error.code);
        }
        result
    }
    fn start() -> Result<(), AppError> {
        // Production must be launchd/root, with a developer signature. No sudo, registration or launchctl here.
        if unsafe { nix::libc::geteuid() } != 0 {
            return Err(AppError::new(
                "E_FAN_HELPER_NOT_ENABLED",
                "root_daemon_only",
            ));
        }
        if unsafe { bridge::on_helper_identity() } != 1 {
            return Err(AppError::new("E_FAN_UNAUTHORIZED", "helper_signature"));
        }
        let executable = own_path().and_then(|path| Executable::at(path).ok());
        if executable.is_none() {
            log::warn!("OpenNavo fan helper cannot watch its own file for app updates");
        }
        let chip = system::string(&NativeSystemSource, "machdep.cpu.brand_string")?;
        let family = Family::from_chip(&chip);
        let mut hardware = SmcHardware::new(
            NativeSmc::open().ok_or_else(super::super::client::unavailable)?,
            family,
            ThreadDelay,
        )
        .with_thermal_chip(&chip);
        use super::super::hardware::Hardware;
        let support = family.support(hardware.fans()?.len());
        if support == crate::monitor::types::FanControlSupport::Unsupported {
            return Err(AppError::new("E_FAN_UNSUPPORTED", "chip"));
        }
        let epoch = chrono::Utc::now().timestamp_millis() as u64;
        let _ = CONTROL.set(Mutex::new(Controller::new(hardware, support, now(), epoch)));
        unsafe {
            if bridge::on_helper_sleep_observer(sleep) != 0 {
                return Err(AppError::new("E_FAN_HELPER_UNAVAILABLE", "power_observer"));
            }
            nix::sys::signal::sigaction(
                nix::sys::signal::Signal::SIGTERM,
                &nix::sys::signal::SigAction::new(
                    nix::sys::signal::SigHandler::Handler(terminate),
                    nix::sys::signal::SaFlags::empty(),
                    nix::sys::signal::SigSet::empty(),
                ),
            )
            .map_err(|_| super::super::client::unavailable())?;
            bridge::on_xpc_serve(request);
        }
        std::thread::spawn(move || {
            loop {
                // After an app update, exiting lets KeepAlive start the new helper now at our path.
                let replaced = executable.as_ref().is_some_and(Executable::replaced);
                if let Some(control) = CONTROL.get()
                    && let Ok(mut control) = control.lock()
                {
                    if replaced || TERMINATE.load(std::sync::atomic::Ordering::Acquire) {
                        if replaced {
                            log::info!(
                                "OpenNavo fan helper was replaced by an app update; restarting"
                            );
                        }
                        control.recover(crate::monitor::types::FanReason::Sleep);
                        std::process::exit(0);
                    }
                    control.watchdog(now());
                }
                std::thread::sleep(std::time::Duration::from_secs(1));
            }
        });
        unsafe {
            bridge::on_run_loop();
        }
        Ok(())
    }
}
pub fn run() -> Result<(), crate::AppError> {
    #[cfg(target_os = "macos")]
    {
        mac::run()
    }
    #[cfg(not(target_os = "macos"))]
    {
        Err(super::client::unavailable())
    }
}
