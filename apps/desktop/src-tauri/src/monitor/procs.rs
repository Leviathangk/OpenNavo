use super::{
    collect::{Group, RawProcess, number},
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
/// The app bundle a process belongs to (the outermost `.app`, so helpers join their app) or else its executable name.
pub fn group_for(path: Option<&str>, name: &str) -> Group {
    if let Some(path) = path
        && let Some(end) = path.find(".app/").map(|index| index + 4)
    {
        let bundle = &path[..end];
        let label = std::path::Path::new(bundle)
            .file_stem()
            .and_then(|stem| stem.to_str())
            .filter(|stem| !stem.is_empty())
            .unwrap_or(name);
        return Group {
            key: format!("app:{bundle}").into(),
            label: label.into(),
        };
    }
    Group {
        key: format!("exe:{name}").into(),
        label: name.into(),
    }
}
#[cfg(target_os = "macos")]
fn executable_path(pid: i32) -> Option<String> {
    // PROC_PIDPATHINFO_MAXSIZE is 4 * MAXPATHLEN.
    let mut buffer = [0u8; 4096];
    let length =
        unsafe { nix::libc::proc_pidpath(pid, buffer.as_mut_ptr().cast(), buffer.len() as u32) };
    (length > 0).then(|| String::from_utf8_lossy(&buffer[..length as usize]).into_owned())
}
#[cfg(target_os = "macos")]
struct CachedGroup {
    started: u64,
    own_user: bool,
    name: Box<str>,
    executable_uuid: Option<[u8; 16]>,
    checked_at: std::time::Instant,
    group: Group,
}
// A copied executable can keep its Mach-O UUID when exec moves it to another bundle.
#[cfg(target_os = "macos")]
const GROUP_PATH_MAX_AGE: std::time::Duration = std::time::Duration::from_secs(30);
/// Exec preserves (pid, start), so also check the executable identity already read with rusage.
#[cfg(target_os = "macos")]
fn cached_group(
    cache: &mut std::collections::BTreeMap<u32, CachedGroup>,
    pid: i32,
    started: u64,
    own_user: bool,
    name: &str,
    executable_uuid: Option<[u8; 16]>,
    read_path: impl FnOnce(i32) -> Option<String>,
) -> Group {
    let executable_uuid = executable_uuid.filter(|uuid| *uuid != [0; 16]);
    let now = std::time::Instant::now();
    if let Some(cached) = cache.get(&(pid as u32))
        && cached.started == started
        && cached.own_user == own_user
        && cached.name.as_ref() == name
        && cached.executable_uuid == executable_uuid
        && (!own_user
            || (executable_uuid.is_some()
                && now.duration_since(cached.checked_at) < GROUP_PATH_MAX_AGE))
    {
        return cached.group.clone();
    }
    // Other users' executables are not looked at: they are grouped by name like the rest of their entry.
    let path = if own_user { read_path(pid) } else { None };
    let group = group_for(path.as_deref(), name);
    if !own_user || (executable_uuid.is_some() && path.is_some()) {
        cache.insert(
            pid as u32,
            CachedGroup {
                started,
                own_user,
                name: name.into(),
                executable_uuid,
                checked_at: now,
                group: group.clone(),
            },
        );
    } else {
        // An unreadable identity cannot validate an old group; retry transient path failures too.
        cache.remove(&(pid as u32));
    }
    group
}
#[cfg(target_os = "macos")]
static GROUPS: std::sync::Mutex<std::collections::BTreeMap<u32, CachedGroup>> =
    std::sync::Mutex::new(std::collections::BTreeMap::new());
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
    let mut groups = GROUPS
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    for &pid in &pids {
        // OpenNavo's own process stays in: its CPU is part of the machine's, and the list says whose it is.
        if pid <= 0 {
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
        let mut child_cpu_ns = None;
        let mut disk_bytes = None;
        let mut executable_uuid = None;
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
                executable_uuid = Some(usage.ri_uuid);
                memory = usage.ri_phys_footprint;
                disk_bytes = Some((usage.ri_diskio_bytesread, usage.ri_diskio_byteswritten));
                cpu_ns = cpu_time_ns(usage.ri_user_time.saturating_add(usage.ri_system_time));
                child_cpu_ns = Some(cpu_time_ns(
                    usage
                        .ri_child_user_time
                        .saturating_add(usage.ri_child_system_time),
                ));
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
        let started = started(&bsd);
        let group = cached_group(
            &mut groups,
            pid,
            started,
            own_user,
            &name,
            executable_uuid,
            executable_path,
        );
        output.push(RawProcess {
            started,
            cpu_ns,
            child_cpu_ns,
            ppid: bsd.pbi_ppid,
            group,
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
                count: 1,
                is_self: pid as u32 == own_pid,
            },
        });
    }
    // Forget processes that are gone, so a reused pid never inherits another process's group.
    let live: std::collections::BTreeSet<u32> = pids
        .iter()
        .filter(|&&pid| pid > 0)
        .map(|&pid| pid as u32)
        .collect();
    groups.retain(|pid, _| live.contains(pid));
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

#[cfg(test)]
mod tests {
    use super::group_for;

    #[cfg(target_os = "macos")]
    #[test]
    fn cached_group_refreshes_after_exec_changes_the_name() {
        let mut cache = std::collections::BTreeMap::new();
        let before = super::cached_group(&mut cache, 42, 100, false, "bash", None, |_| {
            panic!("must not read another user's executable")
        });
        let after = super::cached_group(&mut cache, 42, 100, false, "sleep", None, |_| {
            panic!("must not read another user's executable")
        });

        assert_eq!(&*before.key, "exe:bash");
        assert_eq!(&*after.key, "exe:sleep");
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn cached_group_refreshes_after_exec_with_the_same_name() {
        let mut cache = std::collections::BTreeMap::new();
        let before =
            super::cached_group(&mut cache, 42, 100, true, "helper", Some([1; 16]), |_| {
                Some("/Applications/First.app/Contents/MacOS/helper".into())
            });
        let after = super::cached_group(&mut cache, 42, 100, true, "helper", Some([2; 16]), |_| {
            Some("/Applications/Second.app/Contents/MacOS/helper".into())
        });

        assert_eq!(&*before.key, "app:/Applications/First.app");
        assert_eq!(&*after.key, "app:/Applications/Second.app");
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn cached_group_reuses_a_known_identity_without_reading_the_path() {
        let mut cache = std::collections::BTreeMap::new();
        let before =
            super::cached_group(&mut cache, 42, 100, true, "helper", Some([1; 16]), |_| {
                Some("/Applications/First.app/Contents/MacOS/helper".into())
            });
        let checked_at = cache[&42].checked_at;
        for _ in 0..10 {
            let after =
                super::cached_group(&mut cache, 42, 100, true, "helper", Some([1; 16]), |_| {
                    panic!("a cache hit must not read the executable path")
                });
            assert_eq!(after, before);
        }
        // Hits do not postpone the periodic check for a copied binary with the same UUID.
        assert_eq!(cache[&42].checked_at, checked_at);
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn cached_group_rechecks_same_uuid_copies_in_another_bundle() {
        let mut cache = std::collections::BTreeMap::new();
        super::cached_group(&mut cache, 42, 100, true, "helper", Some([1; 16]), |_| {
            Some("/Applications/First.app/Contents/MacOS/helper".into())
        });
        cache.get_mut(&42).unwrap().checked_at -= super::GROUP_PATH_MAX_AGE;
        let after = super::cached_group(&mut cache, 42, 100, true, "helper", Some([1; 16]), |_| {
            Some("/Applications/Second.app/Contents/MacOS/helper".into())
        });

        assert_eq!(&*after.key, "app:/Applications/Second.app");
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn cached_group_rechecks_unknown_or_zero_uuids_instead_of_reusing_a_bundle() {
        for uuid in [None, Some([0; 16])] {
            let mut cache = std::collections::BTreeMap::new();
            super::cached_group(&mut cache, 42, 100, true, "helper", Some([1; 16]), |_| {
                Some("/Applications/First.app/Contents/MacOS/helper".into())
            });
            for bundle in ["Second", "Third"] {
                let after = super::cached_group(&mut cache, 42, 100, true, "helper", uuid, |_| {
                    Some(format!("/Applications/{bundle}.app/Contents/MacOS/helper"))
                });
                assert_eq!(&*after.key, format!("app:/Applications/{bundle}.app"));
                assert!(cache.is_empty());
            }
        }
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn cached_group_retries_an_unreadable_path() {
        let mut cache = std::collections::BTreeMap::new();
        let fallback =
            super::cached_group(&mut cache, 42, 100, true, "helper", Some([1; 16]), |_| None);
        assert_eq!(&*fallback.key, "exe:helper");
        let recovered =
            super::cached_group(&mut cache, 42, 100, true, "helper", Some([1; 16]), |_| {
                Some("/Applications/First.app/Contents/MacOS/helper".into())
            });
        assert_eq!(&*recovered.key, "app:/Applications/First.app");
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn cached_group_refreshes_on_pid_reuse_and_ownership_changes() {
        let mut cache = std::collections::BTreeMap::new();
        super::cached_group(&mut cache, 42, 100, true, "helper", Some([1; 16]), |_| {
            Some("/Applications/First.app/Contents/MacOS/helper".into())
        });
        let reused =
            super::cached_group(&mut cache, 42, 200, true, "helper", Some([1; 16]), |_| {
                Some("/Applications/Second.app/Contents/MacOS/helper".into())
            });
        assert_eq!(&*reused.key, "app:/Applications/Second.app");

        let other_user =
            super::cached_group(&mut cache, 42, 200, false, "helper", Some([1; 16]), |_| {
                panic!("must not read another user's executable")
            });
        assert_eq!(&*other_user.key, "exe:helper");
        let own_user =
            super::cached_group(&mut cache, 42, 200, true, "helper", Some([1; 16]), |_| {
                Some("/Applications/Second.app/Contents/MacOS/helper".into())
            });
        assert_eq!(&*own_user.key, "app:/Applications/Second.app");
    }

    #[test]
    fn helpers_join_the_outermost_app_and_other_executables_group_by_name() {
        let helper = "/Applications/Google Chrome.app/Contents/Frameworks/Google Chrome Framework.framework/Versions/1/Helpers/Google Chrome Helper (Renderer).app/Contents/MacOS/Google Chrome Helper (Renderer)";
        let main = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
        let group = group_for(Some(helper), "Google Chrome Helper (Renderer)");
        assert_eq!(&*group.key, "app:/Applications/Google Chrome.app");
        assert_eq!(&*group.label, "Google Chrome");
        assert_eq!(group_for(Some(main), "Google Chrome"), group);
        let tool = group_for(
            Some("/opt/homebrew/Cellar/go/1.26/libexec/pkg/tool/darwin_arm64/compile"),
            "compile",
        );
        assert_eq!((&*tool.key, &*tool.label), ("exe:compile", "compile"));
        // No readable path (another user's process, or the read failed): the name alone.
        assert_eq!(&*group_for(None, "kernel_task").key, "exe:kernel_task");
        // A path that merely mentions ".app" without a bundle directory is not a bundle.
        assert_eq!(&*group_for(Some("/tmp/x.app"), "x.app").key, "exe:x.app");
    }
}
