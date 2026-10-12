//! Root daemon entry point. Never launched by tests or cargo gen.
#[cfg(target_os = "macos")]
mod mac {
    use super::super::{
        bridge,
        control::{Controller, Reply, Request},
        hardware::{Family, SmcHardware, ThreadDelay},
    };
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
        std::thread::spawn(|| {
            loop {
                if let Some(control) = CONTROL.get()
                    && let Ok(mut control) = control.lock()
                {
                    if TERMINATE.load(std::sync::atomic::Ordering::Acquire) {
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
