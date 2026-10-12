//! Native FSEvents bridge; callbacks only signal a bounded worker queue.
#![cfg(target_os = "macos")]
use super::Engine;
use objc2_foundation::{NSArray, NSString};
use std::{
    ffi::{CStr, c_void},
    path::PathBuf,
    sync::{
        Arc, Weak,
        atomic::{AtomicBool, Ordering},
        mpsc,
    },
    time::Duration,
};
type Ref = *const c_void;
#[repr(C)]
struct Context {
    version: isize,
    info: *mut c_void,
    retain: Option<unsafe extern "C" fn(Ref) -> Ref>,
    release: Option<unsafe extern "C" fn(Ref)>,
    description: Option<unsafe extern "C" fn(Ref) -> Ref>,
}
#[link(name = "CoreServices", kind = "framework")]
unsafe extern "C" {
    fn FSEventStreamCreate(
        allocator: Ref,
        callback: unsafe extern "C" fn(
            Ref,
            *mut c_void,
            usize,
            *mut c_void,
            *const u32,
            *const u64,
        ),
        context: *mut Context,
        paths: Ref,
        since: u64,
        latency: f64,
        flags: u32,
    ) -> Ref;
    fn FSEventStreamScheduleWithRunLoop(stream: Ref, run_loop: Ref, mode: Ref);
    fn FSEventStreamStart(stream: Ref) -> u8;
    fn FSEventStreamStop(stream: Ref);
    fn FSEventStreamInvalidate(stream: Ref);
    fn FSEventStreamRelease(stream: Ref);
}
#[link(name = "CoreFoundation", kind = "framework")]
unsafe extern "C" {
    fn CFRunLoopGetCurrent() -> Ref;
    fn CFRunLoopRunInMode(mode: Ref, seconds: f64, return_after_source: u8) -> i32;
    static kCFRunLoopDefaultMode: Ref;
}
struct Signals {
    applications: Vec<PathBuf>,
    caskrooms: Vec<PathBuf>,
    sender: mpsc::SyncSender<()>,
}
fn hidden(name: &std::ffi::OsStr) -> bool {
    name.as_encoded_bytes().first() == Some(&b'.')
}
fn app(name: &std::ffi::OsStr) -> bool {
    !hidden(name)
        && std::path::Path::new(name)
            .extension()
            .is_some_and(|e| e == "app")
}
/// Inside a bundle only its replacement or a new Info.plist (an update) matters.
fn bundle_change(rest: &[&std::ffi::OsStr]) -> bool {
    match rest {
        [] => true,
        [contents] => *contents == "Contents",
        [contents, info] => *contents == "Contents" && *info == "Info.plist",
        _ => false,
    }
}
/// Whether a changed path can change the app list. Writes inside bundles (apps updating their frameworks),
/// Finder's .DS_Store and Caskroom internals are ignored; each used to cost a full re-enumeration.
pub(crate) fn relevant(
    path: &std::path::Path,
    applications: &[PathBuf],
    caskrooms: &[PathBuf],
) -> bool {
    for root in applications {
        // The root itself (created, removed, renamed) or an event coalesced at an ancestor.
        if root.starts_with(path) {
            return true;
        }
        let Ok(relative) = path.strip_prefix(root) else {
            continue;
        };
        let parts: Vec<_> = relative.components().map(|c| c.as_os_str()).collect();
        return match parts.as_slice() {
            [] => true,
            [name] => !hidden(name),
            [first, rest @ ..] if app(first) => bundle_change(rest),
            [folder, second, rest @ ..] if !hidden(folder) && app(second) => bundle_change(rest),
            _ => false,
        };
    }
    caskrooms.iter().any(|root| {
        root.starts_with(path)
            || path
                .strip_prefix(root)
                .is_ok_and(|relative| relative.components().count() <= 2)
    })
}
unsafe extern "C" fn changed(
    _: Ref,
    info: *mut c_void,
    count: usize,
    paths: *mut c_void,
    _: *const u32,
    _: *const u64,
) {
    if info.is_null() || paths.is_null() {
        return;
    }
    let signals = unsafe { &*(info as *const Signals) };
    let paths =
        unsafe { std::slice::from_raw_parts(paths as *const *const std::ffi::c_char, count) };
    if paths.iter().filter(|p| !p.is_null()).any(|p| {
        use std::os::unix::ffi::OsStrExt;
        let bytes = unsafe { CStr::from_ptr(*p) }.to_bytes();
        let path = std::path::Path::new(std::ffi::OsStr::from_bytes(bytes));
        relevant(path, &signals.applications, &signals.caskrooms)
    }) {
        let _ = signals.sender.try_send(());
    }
}
pub struct Guard(Arc<AtomicBool>);
impl Drop for Guard {
    fn drop(&mut self) {
        self.0.store(true, Ordering::Release);
    }
}
pub fn start(engine: &Arc<Engine>) -> Guard {
    let stop = Arc::new(AtomicBool::new(false));
    let stream_stop = stop.clone();
    let worker_stop = stop.clone();
    let applications = engine.roots.applications.clone();
    let caskrooms = engine.roots.caskrooms.clone();
    let watched: Vec<_> = applications
        .iter()
        .chain(caskrooms.iter())
        .filter_map(|p| {
            let mut parent = p.as_path();
            while !parent.is_dir() {
                parent = parent.parent()?;
            }
            (parent != std::path::Path::new("/")).then(|| parent.to_owned())
        })
        .collect();
    let (sender, receiver) = mpsc::sync_channel(1);
    let weak: Weak<Engine> = Arc::downgrade(engine);
    std::thread::spawn(move || {
        while !worker_stop.load(Ordering::Acquire) {
            match receiver.recv_timeout(Duration::from_millis(500)) {
                Ok(()) => {}
                Err(mpsc::RecvTimeoutError::Timeout) => continue,
                Err(mpsc::RecvTimeoutError::Disconnected) => break,
            }
            // Coalesce changes while a scan is running; the stream itself has a 500ms latency.
            while receiver.try_recv().is_ok() {}
            if let Some(engine) = weak.upgrade() {
                let _ = engine.refresh(crate::model::LocalAppsChangedReason::Fs);
                // New apps get their catalog match without waiting for the window to ask for the list.
                let hydrate = engine.clone();
                tauri::async_runtime::spawn(async move {
                    let _ = hydrate.hydrate_catalog().await;
                });
            } else {
                break;
            }
        }
    });
    std::thread::spawn(move || {
        objc2::rc::autoreleasepool(|_| {
            let strings: Vec<_> = watched
                .iter()
                .map(|p| NSString::from_str(&p.to_string_lossy()))
                .collect();
            if strings.is_empty() {
                return;
            }
            let references: Vec<_> = strings.iter().map(|s| &**s).collect();
            let array = NSArray::from_slice(&references);
            let mut signals = Signals {
                applications,
                caskrooms,
                sender,
            };
            // FSEvents borrows the context until stream invalidation/release; it stays on this thread's stack.
            let mut context = Context {
                version: 0,
                info: &mut signals as *mut _ as *mut c_void,
                retain: None,
                release: None,
                description: None,
            };
            unsafe {
                // SinceNow; WatchRoot + FileEvents. No path content leaves the process.
                let stream = FSEventStreamCreate(
                    std::ptr::null(),
                    changed,
                    &mut context,
                    &*array as *const _ as Ref,
                    u64::MAX,
                    0.5,
                    0x04 | 0x10,
                );
                if stream.is_null() {
                    return;
                }
                FSEventStreamScheduleWithRunLoop(
                    stream,
                    CFRunLoopGetCurrent(),
                    kCFRunLoopDefaultMode,
                );
                if FSEventStreamStart(stream) != 0 {
                    while !stream_stop.load(Ordering::Acquire) {
                        CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.5, 0);
                    }
                    FSEventStreamStop(stream);
                }
                FSEventStreamInvalidate(stream);
                FSEventStreamRelease(stream);
            }
        })
    });
    Guard(stop)
}

#[cfg(test)]
mod tests {
    use super::relevant;
    use std::path::{Path, PathBuf};
    #[test]
    fn only_bundle_level_changes_refresh_the_list() {
        let applications = [
            PathBuf::from("/Applications"),
            PathBuf::from("/Users/u/Applications"),
        ];
        let caskrooms = [PathBuf::from("/opt/homebrew/Caskroom")];
        let check = |path: &str| relevant(Path::new(path), &applications, &caskrooms);
        for path in [
            "/Applications",
            "/Applications/Zed.app",
            "/Applications/Zed.app/Contents",
            "/Applications/Zed.app/Contents/Info.plist",
            "/Applications/Utilities",
            "/Applications/Adobe/Photoshop.app",
            "/Applications/Adobe/Photoshop.app/Contents/Info.plist",
            "/Users/u/Applications",
            "/Users/u",
            "/opt/homebrew/Caskroom/zed",
            "/opt/homebrew/Caskroom/zed/0.200.1",
        ] {
            assert!(check(path), "{path}");
        }
        for path in [
            "/Applications/.DS_Store",
            "/Applications/Zed.app/Contents/Frameworks/Sparkle.framework/Versions/B/Sparkle",
            "/Applications/Zed.app/Contents/MacOS/zed",
            "/Applications/Adobe/readme.txt",
            "/Applications/Adobe/Photoshop.app/Contents/Resources/x.png",
            "/Users/u/Downloads/file.zip",
            "/Users/u/Library/Caches/com.example/Cache.db-wal",
            "/opt/homebrew/Caskroom/zed/.metadata/0.200.1/20261001/Casks/zed.json",
        ] {
            assert!(!check(path), "{path}");
        }
    }
}
