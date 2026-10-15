//! macOS privacy permissions (06 §12.3, §12.6): App Management and Full Disk Access.
//!
//! No public query API exists: use the private TCC framework's TCCAccessPreflight for read-only preflight without prompts or side effects.
//! Confirm effective Full Disk Access by reading protected directories; Apple recommends probing locations actually needed.
use crate::model::{PermissionState, Permissions};
use std::path::Path;

pub const APP_BUNDLES: &str = "kTCCServiceSystemPolicyAppBundles";
pub const ALL_FILES: &str = "kTCCServiceSystemPolicyAllFiles";

/// Protected directories in order: check the first existing one; the first is Recent Documents, also touched by zap.
const FULL_DISK_PROBES: [&str; 2] = [
    "Library/Application Support/com.apple.sharedfilelist",
    "Library/Safari",
];

/// TCCAccessPreflight results: 0 allowed, 1 denied, 2 never requested.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Preflight {
    Granted,
    Denied,
    NotDetermined,
}

impl Preflight {
    pub fn from_code(code: i32) -> Option<Self> {
        match code {
            0 => Some(Self::Granted),
            1 => Some(Self::Denied),
            2 => Some(Self::NotDetermined),
            _ => None,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Probe {
    Readable,
    Blocked,
    Missing,
}

pub fn probe_dir(path: &Path) -> Probe {
    match std::fs::read_dir(path) {
        Ok(_) => Probe::Readable,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Probe::Missing,
        // TCC denial returns EPERM; any unreadable directory cannot count as effective access, regardless of cause.
        Err(_) => Probe::Blocked,
    }
}

/// Whether this process can read protected directories; None if no probe directory exists.
pub fn probe_full_disk(home: &Path) -> Option<bool> {
    FULL_DISK_PROBES
        .iter()
        .find_map(|relative| match probe_dir(&home.join(relative)) {
            Probe::Readable => Some(true),
            Probe::Blocked => Some(false),
            Probe::Missing => None,
        })
}

/// Inputs: both preflights, protected-directory probe, task-inferred App Management, and records of tasks blocked despite system permission.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Signals {
    pub app_bundles: Option<Preflight>,
    pub all_files: Option<Preflight>,
    pub full_disk_readable: Option<bool>,
    pub inferred_app_management: PermissionState,
    pub app_management_blocked_after_grant: bool,
}

pub fn combine(signals: Signals) -> Permissions {
    let full_disk_access = match (signals.full_disk_readable, signals.all_files) {
        (Some(true), _) => PermissionState::Granted,
        (Some(false), _) => PermissionState::Denied,
        (None, Some(Preflight::Granted)) => PermissionState::Granted,
        (None, Some(_)) => PermissionState::Denied,
        (None, None) => PermissionState::Unknown,
    };
    // Full Disk Access also permits modifying other apps (Homebrew uses the same rule).
    let app_management = if full_disk_access == PermissionState::Granted {
        PermissionState::Granted
    } else {
        match signals.app_bundles {
            Some(Preflight::Granted) => PermissionState::Granted,
            Some(_) => PermissionState::Denied,
            None => signals.inferred_app_management,
        }
    };
    // Enabled in System Settings but not this process: both permissions take effect only after relaunch.
    let full_disk_pending =
        signals.full_disk_readable == Some(false) && signals.all_files == Some(Preflight::Granted);
    let app_management_pending = signals.app_management_blocked_after_grant
        && full_disk_access != PermissionState::Granted
        && signals.app_bundles == Some(Preflight::Granted);
    Permissions {
        app_management,
        full_disk_access,
        relaunch_required: full_disk_pending || app_management_pending,
        app_management_pending,
        full_disk_access_pending: full_disk_pending,
    }
}

/// Read current system signals (preflight uses XPC; run on a blocking thread).
pub fn signals(
    inferred_app_management: PermissionState,
    app_management_blocked_after_grant: bool,
) -> Signals {
    let home = std::env::var_os("HOME").map(std::path::PathBuf::from);
    Signals {
        app_bundles: preflight(APP_BUNDLES),
        all_files: preflight(ALL_FILES),
        full_disk_readable: home.as_deref().and_then(probe_full_disk),
        inferred_app_management,
        app_management_blocked_after_grant,
    }
}

#[cfg(target_os = "macos")]
pub fn preflight(service: &str) -> Option<Preflight> {
    use std::ffi::{CString, c_char, c_int, c_void};
    use std::sync::OnceLock;

    type PreflightFn = unsafe extern "C" fn(*const c_void, *const c_void) -> c_int;
    unsafe extern "C" {
        fn dlopen(path: *const c_char, mode: c_int) -> *mut c_void;
        fn dlsym(handle: *mut c_void, symbol: *const c_char) -> *mut c_void;
    }
    #[link(name = "CoreFoundation", kind = "framework")]
    unsafe extern "C" {
        fn CFStringCreateWithCString(
            allocator: *const c_void,
            text: *const c_char,
            encoding: u32,
        ) -> *const c_void;
        fn CFRelease(value: *const c_void);
    }
    const RTLD_LAZY: c_int = 0x1;
    const UTF8: u32 = 0x0800_0100;

    static FUNCTION: OnceLock<Option<PreflightFn>> = OnceLock::new();
    let function = (*FUNCTION.get_or_init(|| {
        // SAFETY: fixed system framework path/symbol names; keep the handle for process lifetime without dlclose.
        unsafe {
            let handle = dlopen(
                c"/System/Library/PrivateFrameworks/TCC.framework/TCC".as_ptr(),
                RTLD_LAZY,
            );
            if handle.is_null() {
                return None;
            }
            let symbol = dlsym(handle, c"TCCAccessPreflight".as_ptr());
            if symbol.is_null() {
                None
            } else {
                Some(std::mem::transmute::<*mut c_void, PreflightFn>(symbol))
            }
        }
    }))?;
    let name = CString::new(service).ok()?;
    // SAFETY: convert service name to CFString, call read-only preflight with empty options, then release the created string.
    let code = unsafe {
        let value = CFStringCreateWithCString(std::ptr::null(), name.as_ptr(), UTF8);
        if value.is_null() {
            return None;
        }
        let code = function(value, std::ptr::null());
        CFRelease(value);
        code
    };
    Preflight::from_code(code)
}

#[cfg(not(target_os = "macos"))]
pub fn preflight(_service: &str) -> Option<Preflight> {
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    fn base() -> Signals {
        Signals {
            app_bundles: Some(Preflight::NotDetermined),
            all_files: Some(Preflight::NotDetermined),
            full_disk_readable: Some(false),
            inferred_app_management: PermissionState::Unknown,
            app_management_blocked_after_grant: false,
        }
    }

    #[test]
    fn full_disk_access_follows_the_effective_probe() {
        let readable = combine(Signals {
            full_disk_readable: Some(true),
            ..base()
        });
        assert_eq!(readable.full_disk_access, PermissionState::Granted);
        // Effective Full Disk Access also permits modifying other apps.
        assert_eq!(readable.app_management, PermissionState::Granted);
        assert!(!readable.relaunch_required);
        assert!(!readable.full_disk_access_pending);

        // Turned on in System Settings while running: not effective yet, but the guide is done and only a reopen remains.
        let pending = combine(Signals {
            all_files: Some(Preflight::Granted),
            ..base()
        });
        assert_eq!(pending.full_disk_access, PermissionState::Denied);
        assert!(pending.relaunch_required);
        assert!(pending.full_disk_access_pending);
        assert!(!pending.app_management_pending);

        let no_probe = combine(Signals {
            full_disk_readable: None,
            all_files: Some(Preflight::Granted),
            ..base()
        });
        assert_eq!(no_probe.full_disk_access, PermissionState::Granted);
        let unknown = combine(Signals {
            full_disk_readable: None,
            all_files: None,
            ..base()
        });
        assert_eq!(unknown.full_disk_access, PermissionState::Unknown);
    }

    #[test]
    fn app_management_prefers_preflight_and_falls_back_to_inference() {
        let granted = combine(Signals {
            app_bundles: Some(Preflight::Granted),
            ..base()
        });
        assert_eq!(granted.app_management, PermissionState::Granted);
        for answer in [Preflight::Denied, Preflight::NotDetermined] {
            let denied = combine(Signals {
                app_bundles: Some(answer),
                inferred_app_management: PermissionState::Granted,
                ..base()
            });
            assert_eq!(denied.app_management, PermissionState::Denied);
        }
        let inferred = combine(Signals {
            app_bundles: None,
            inferred_app_management: PermissionState::Granted,
            ..base()
        });
        assert_eq!(inferred.app_management, PermissionState::Granted);
    }

    #[test]
    fn blocked_after_grant_asks_for_relaunch() {
        let blocked = combine(Signals {
            app_bundles: Some(Preflight::Granted),
            app_management_blocked_after_grant: true,
            ..base()
        });
        assert_eq!(blocked.app_management, PermissionState::Granted);
        assert!(blocked.relaunch_required);
        assert!(blocked.app_management_pending);
        assert!(!blocked.full_disk_access_pending);
        // If preflight does not allow access, failure means missing authorization, not a relaunch requirement.
        let not_granted = combine(Signals {
            app_bundles: Some(Preflight::Denied),
            app_management_blocked_after_grant: true,
            ..base()
        });
        assert!(!not_granted.relaunch_required);
        assert!(!not_granted.app_management_pending);
    }

    #[test]
    fn probes_use_the_first_existing_directory() {
        let home = tempfile::tempdir().expect("temporary directory");
        assert_eq!(probe_full_disk(home.path()), None);
        std::fs::create_dir_all(home.path().join("Library/Safari")).expect("directory");
        assert_eq!(probe_full_disk(home.path()), Some(true));
        assert_eq!(probe_dir(&home.path().join("missing")), Probe::Missing);
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn preflight_answers_known_services() {
        // Check only that the private framework loads and returns a recognized result, without assuming local permission state.
        assert!(preflight(ALL_FILES).is_some());
        assert!(preflight(APP_BUNDLES).is_some());
    }
}
