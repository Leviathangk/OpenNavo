//! Reopen through LaunchServices so macOS assigns the new process its own TCC identity.
use serde::{Deserialize, Serialize};
use std::{
    path::Path,
    time::{SystemTime, UNIX_EPOCH},
};

#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Reason {
    User,
    SelfHeal,
}

#[derive(Serialize, Deserialize)]
struct Marker {
    at: u64,
    reason: Reason,
}

fn now_ms() -> std::io::Result<u64> {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(std::io::Error::other)?
        .as_millis()
        .try_into()
        .map_err(std::io::Error::other)
}

fn fresh(bytes: &[u8], now: u64) -> bool {
    serde_json::from_slice::<Marker>(bytes)
        .is_ok_and(|marker| now.checked_sub(marker.at).is_some_and(|age| age <= 120_000))
}

fn consume_at(data_dir: &Path, now: Option<u64>) -> bool {
    let path = data_dir.join("relaunch.json");
    let bytes = std::fs::read(&path);
    if let Err(error) = std::fs::remove_file(&path)
        && error.kind() != std::io::ErrorKind::NotFound
    {
        log::warn!("Could not remove relaunch marker: {error}");
    }
    bytes
        .ok()
        .zip(now)
        .is_some_and(|(bytes, now)| fresh(&bytes, now))
}

pub fn consume(data_dir: &Path) -> bool {
    consume_at(data_dir, now_ms().ok())
}

#[cfg(any(target_os = "macos", test))]
fn write_at(data_dir: &Path, reason: Reason, at: u64) -> std::io::Result<()> {
    let bytes = serde_json::to_vec(&Marker { at, reason }).map_err(std::io::Error::other)?;
    std::fs::write(data_dir.join("relaunch.json"), bytes)
}

#[cfg(target_os = "macos")]
fn write(data_dir: &Path, reason: Reason) -> std::io::Result<()> {
    write_at(data_dir, reason, now_ms()?)
}

#[cfg(target_os = "macos")]
use std::{
    os::unix::process::CommandExt,
    process::{Command, Stdio},
};

#[cfg(target_os = "macos")]
const WAIT_SCRIPT: &str = r#"i=0
while kill -0 "$1" 2>/dev/null && [ "$i" -lt 300 ]; do
    sleep 0.1
    i=$((i + 1))
done
exec /usr/bin/open -n "$2""#;

#[cfg(target_os = "macos")]
fn helper(pid: u32, bundle: &Path) -> Command {
    let mut command = Command::new("/bin/sh");
    // Keep paths out of shell source and never forward argv (including login's --hidden).
    command
        .args(["-c", WAIT_SCRIPT, "sh"])
        .arg(pid.to_string())
        .arg(bundle)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .process_group(0);
    command
}

#[cfg(target_os = "macos")]
pub fn reopen(data_dir: &Path, reason: Reason) -> std::io::Result<bool> {
    let Some(bundle) = crate::guide::app_bundle(&std::env::current_exe()?) else {
        return Ok(false);
    };
    write(data_dir, reason)?;
    helper(std::process::id(), &bundle).spawn()?;
    Ok(true)
}

#[cfg(any(target_os = "macos", test))]
fn should_self_heal(
    release: bool,
    bundled: bool,
    hidden: bool,
    pending: bool,
    fresh: bool,
) -> bool {
    release && bundled && !hidden && pending && !fresh
}

/// Return before any window or background service starts when a one-time repair is requested.
#[cfg(target_os = "macos")]
pub fn self_heal(app: &tauri::AppHandle, core: &crate::core::DesktopCore, hidden: bool) -> bool {
    use tauri::Manager;
    let bundle = std::env::current_exe()
        .ok()
        .and_then(|exe| crate::guide::app_bundle(&exe));
    if !should_self_heal(
        cfg!(all(not(debug_assertions), not(test))),
        bundle.is_some(),
        hidden,
        core.startup_full_disk_pending,
        core.relaunch_fresh,
    ) {
        return false;
    }
    // A durable marker is required before automatic restart, including its fallback, to prevent a loop.
    if let Err(error) = write(&core.data_dir, Reason::SelfHeal) {
        log::warn!("Startup relaunch marker could not be written: {error}");
        return false;
    }
    let lifecycle = std::sync::Arc::new(crate::lifecycle::Lifecycle::default());
    lifecycle.ready();
    app.manage(lifecycle);
    match helper(std::process::id(), &bundle.unwrap()).spawn() {
        Ok(_) => app.exit(0),
        Err(error) => {
            log::warn!("LaunchServices startup relaunch failed: {error}");
            app.request_restart();
        }
    }
    true
}

#[cfg(all(test, target_os = "macos"))]
mod tests {
    use super::*;
    use std::ffi::OsStr;

    #[test]
    fn helper_passes_only_pid_and_bundle_as_positional_arguments() {
        let path = Path::new("/tmp/Space ' quote \" $(touch nope) `whoami` 中文.app");
        let command = helper(1234, path);
        assert_eq!(command.get_program(), OsStr::new("/bin/sh"));
        assert_eq!(
            command.get_args().collect::<Vec<_>>(),
            vec![
                OsStr::new("-c"),
                OsStr::new(WAIT_SCRIPT),
                OsStr::new("sh"),
                OsStr::new("1234"),
                path.as_os_str(),
            ]
        );
        assert!(!WAIT_SCRIPT.contains(path.to_str().unwrap()));
        assert!(!WAIT_SCRIPT.contains("--args"));
        assert!(
            !command
                .get_args()
                .any(|arg| arg == crate::lifecycle::LOGIN_ARG)
        );
    }
}

#[cfg(test)]
mod marker_tests {
    use super::*;

    #[test]
    fn self_heal_requires_every_guard_and_a_fresh_marker_stops_the_next_launch() {
        assert!(should_self_heal(true, true, false, true, false));
        for (release, bundled, hidden, pending, fresh) in [
            (false, true, false, true, false),
            (true, false, false, true, false),
            (true, true, true, true, false),
            (true, true, false, false, false),
            (true, true, false, true, true),
        ] {
            assert!(!should_self_heal(release, bundled, hidden, pending, fresh));
        }
        let dir = tempfile::tempdir().unwrap();
        write_at(dir.path(), Reason::SelfHeal, 1000).unwrap();
        let fresh = consume_at(dir.path(), Some(1001));
        assert!(!should_self_heal(true, true, false, true, fresh));
    }

    #[test]
    fn freshness_rejects_expiry_clock_rollback_and_malformed_markers() {
        let bytes = br#"{"at":1000,"reason":"user"}"#;
        assert!(fresh(bytes, 1000));
        assert!(fresh(bytes, 121_000));
        assert!(!fresh(bytes, 121_001));
        assert!(!fresh(bytes, 999));
        for bytes in [
            b"not json".as_slice(),
            br#"{"at":1000}"#,
            br#"{"at":1000,"reason":"unknown"}"#,
            br#"{"at":-1,"reason":"user"}"#,
            br#"{"at":"1000","reason":"user"}"#,
        ] {
            assert!(!fresh(bytes, 1000));
        }
    }

    #[test]
    fn markers_are_consumed_once_even_when_invalid_or_expired() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("relaunch.json");
        assert!(!consume_at(dir.path(), Some(1000)));
        for reason in [Reason::User, Reason::SelfHeal] {
            write_at(dir.path(), reason, 1000).unwrap();
            assert!(consume_at(dir.path(), Some(1000)));
            assert!(!path.exists());
            assert!(!consume_at(dir.path(), Some(1000)));
        }
        for (bytes, now) in [
            (br#"{"at":1000,"reason":"user"}"#.as_slice(), Some(121_001)),
            (br#"{"at":1000,"reason":"self_heal"}"#.as_slice(), Some(999)),
            (b"invalid".as_slice(), Some(1000)),
            (br#"{"at":1000,"reason":"user"}"#.as_slice(), None),
        ] {
            std::fs::write(&path, bytes).unwrap();
            assert!(!consume_at(dir.path(), now));
            assert!(!path.exists());
        }
    }
}
