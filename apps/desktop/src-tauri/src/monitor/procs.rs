use super::{
    collect::{RawProcess, number},
    types::*,
};
use crate::AppError;
/// Darwin proc rusage/task CPU clocks use Mach absolute units, including on Apple silicon.
pub fn cpu_ticks_to_ns(ticks: u64, numer: u32, denom: u32) -> u64 {
    if denom == 0 {
        return 0;
    }
    ((u128::from(ticks) * u128::from(numer)) / u128::from(denom)).min(u128::from(u64::MAX)) as u64
}
#[cfg(target_os = "macos")]
fn cpu_time_ns(ticks: u64) -> u64 {
    #[repr(C)]
    struct Timebase {
        numer: u32,
        denom: u32,
    }
    unsafe extern "C" {
        fn mach_timebase_info(info: *mut Timebase) -> i32;
    }
    static TIMEBASE: std::sync::OnceLock<(u32, u32)> = std::sync::OnceLock::new();
    let &(numer, denom) = TIMEBASE.get_or_init(|| {
        let mut timebase = Timebase { numer: 0, denom: 0 };
        unsafe { mach_timebase_info(&mut timebase) };
        (timebase.numer, timebase.denom)
    });
    cpu_ticks_to_ns(ticks, numer, denom)
}
#[derive(Clone, Debug)]
pub struct AppIdentity {
    pub pid: u32,
    pub own_user: bool,
    pub regular: bool,
    pub bundle_id: Option<String>,
    pub path: String,
}
pub fn may_quit(app: &AppIdentity, own_pid: u32) -> bool {
    app.pid > 1
        && app.pid != own_pid
        && app.own_user
        && app.regular
        && app.bundle_id.is_some()
        && !app.path.starts_with("/System/Library/")
        && !matches!(
            app.bundle_id.as_deref(),
            Some(
                "com.apple.finder"
                    | "com.apple.dock"
                    | "com.apple.loginwindow"
                    | "com.apple.SystemUIServer"
            )
        )
}
#[cfg(target_os = "macos")]
fn bsd(pid: i32) -> Option<nix::libc::proc_bsdinfo> {
    let mut info = unsafe { std::mem::zeroed::<nix::libc::proc_bsdinfo>() };
    let size = std::mem::size_of_val(&info);
    (unsafe {
        nix::libc::proc_pidinfo(
            pid,
            nix::libc::PROC_PIDTBSDINFO,
            0,
            (&mut info as *mut nix::libc::proc_bsdinfo).cast(),
            size as i32,
        )
    } == size as i32)
        .then_some(info)
}
#[cfg(target_os = "macos")]
fn started(bsd: &nix::libc::proc_bsdinfo) -> u64 {
    bsd.pbi_start_tvsec
        .saturating_mul(1_000_000)
        .saturating_add(bsd.pbi_start_tvusec)
}
#[cfg(target_os = "macos")]
fn identity(app: &objc2_app_kit::NSRunningApplication, own_user: bool) -> AppIdentity {
    AppIdentity {
        pid: app.processIdentifier() as u32,
        own_user,
        regular: app.activationPolicy() == objc2_app_kit::NSApplicationActivationPolicy::Regular,
        bundle_id: app.bundleIdentifier().map(|v| v.to_string()),
        path: app
            .bundleURL()
            .and_then(|u| u.path())
            .map(|p| p.to_string())
            .unwrap_or_default(),
    }
}
#[cfg(target_os = "macos")]
pub fn list() -> Option<Vec<RawProcess>> {
    use nix::libc;
    let count = unsafe { libc::proc_listallpids(std::ptr::null_mut(), 0) };
    if count <= 0 {
        return None;
    }
    let mut pids = vec![0i32; count as usize + 256];
    let capacity = std::mem::size_of_val(pids.as_slice());
    let count = unsafe { libc::proc_listallpids(pids.as_mut_ptr().cast(), capacity as i32) };
    if count < 0 || count as usize > pids.len() {
        return None;
    }
    pids.truncate(count as usize);
    let apps = objc2_app_kit::NSWorkspace::sharedWorkspace().runningApplications();
    let by_pid: std::collections::BTreeMap<_, _> =
        apps.iter().map(|a| (a.processIdentifier(), a)).collect();
    let own = unsafe { libc::geteuid() };
    let own_pid = std::process::id();
    let mut output = Vec::new();
    for pid in pids {
        if pid <= 0 || pid as u32 == own_pid {
            continue;
        }
        let Some(bsd) = bsd(pid) else {
            continue;
        };
        let own_user = bsd.pbi_uid == own;
        let app = by_pid.get(&pid);
        let ident = app.map(|app| identity(app, own_user));
        let name = app
            .and_then(|a| a.localizedName())
            .map(|s| s.to_string())
            .unwrap_or_else(|| {
                let chars = if bsd.pbi_name[0] != 0 {
                    &bsd.pbi_name[..]
                } else {
                    &bsd.pbi_comm[..]
                };
                String::from_utf8_lossy(
                    &chars
                        .iter()
                        .take_while(|&&v| v != 0)
                        .map(|&v| v as u8)
                        .collect::<Vec<_>>(),
                )
                .into_owned()
            });
        let mut memory = 0;
        let mut cpu_ns = 0;
        let mut disk_bytes = None;
        // Other users' processes are names only. Never inspect argv, environment or executable arguments.
        if own_user {
            let mut usage = unsafe { std::mem::zeroed::<libc::rusage_info_v2>() };
            if unsafe {
                libc::proc_pid_rusage(
                    pid,
                    libc::RUSAGE_INFO_V2,
                    (&mut usage as *mut libc::rusage_info_v2).cast(),
                )
            } == 0
            {
                memory = usage.ri_phys_footprint;
                disk_bytes = Some((usage.ri_diskio_bytesread, usage.ri_diskio_byteswritten));
                cpu_ns = cpu_time_ns(usage.ri_user_time.saturating_add(usage.ri_system_time));
            } else {
                let mut task = unsafe { std::mem::zeroed::<libc::proc_taskinfo>() };
                let size = std::mem::size_of_val(&task);
                if unsafe {
                    libc::proc_pidinfo(
                        pid,
                        libc::PROC_PIDTASKINFO,
                        0,
                        (&mut task as *mut libc::proc_taskinfo).cast(),
                        size as i32,
                    )
                } == size as i32
                {
                    cpu_ns = cpu_time_ns(task.pti_total_user.saturating_add(task.pti_total_system));
                }
            }
        }
        let quittable = ident.as_ref().is_some_and(|a| may_quit(a, own_pid));
        let system = !own_user
            || pid <= 1
            || ident
                .as_ref()
                .is_some_and(|a| a.path.starts_with("/System/Library/"));
        output.push(RawProcess {
            started: started(&bsd),
            cpu_ns,
            disk_bytes,
            net_bytes: None,
            net_at: None,
            info: ProcessInfo {
                pid: pid as u32,
                name,
                bundle_id: if own_user {
                    ident.and_then(|a| a.bundle_id)
                } else {
                    None
                },
                icon_path: None,
                cpu: number(0.0),
                memory_bytes: memory,
                disk_read_bytes_per_sec: None,
                disk_write_bytes_per_sec: None,
                net_down_bytes_per_sec: None,
                net_up_bytes_per_sec: None,
                kind: if system {
                    ProcessKind::System
                } else if app.is_some_and(|a| {
                    a.activationPolicy() == objc2_app_kit::NSApplicationActivationPolicy::Regular
                }) {
                    ProcessKind::App
                } else {
                    ProcessKind::Background
                },
                quittable,
            },
        });
    }
    Some(output)
}
#[cfg(target_os = "macos")]
pub fn same_user_identity(pid: u32, expected_start: u64) -> bool {
    bsd(pid as i32).is_some_and(|b| {
        b.pbi_uid == unsafe { nix::libc::geteuid() } && started(&b) == expected_start
    })
}
#[cfg(target_os = "macos")]
pub fn quit(target: &RawProcess) -> Result<(), AppError> {
    use objc2_app_kit::NSRunningApplication;
    let reject = || AppError::new("E_PROCESS_NOT_QUITTABLE", "monitor_quit_app");
    objc2::rc::autoreleasepool(|_| {
        if !target.info.quittable || target.info.kind != ProcessKind::App {
            return Err(reject());
        }
        let current = bsd(target.info.pid as i32).ok_or_else(reject)?;
        if started(&current) != target.started {
            return Err(reject());
        }
        let app =
            NSRunningApplication::runningApplicationWithProcessIdentifier(target.info.pid as i32)
                .ok_or_else(reject)?;
        let ident = identity(&app, current.pbi_uid == unsafe { nix::libc::geteuid() });
        if !may_quit(&ident, std::process::id())
            || ident.bundle_id != target.info.bundle_id
            || app.isTerminated()
        {
            return Err(reject());
        }
        if app.terminate() {
            Ok(())
        } else {
            Err(AppError::new("E_APP_QUIT_FAILED", "monitor_quit_app"))
        }
    })
}
#[cfg(not(target_os = "macos"))]
pub fn quit(_: &RawProcess) -> Result<(), AppError> {
    Err(AppError::new("E_MONITOR_UNAVAILABLE", "monitor_quit_app"))
}
