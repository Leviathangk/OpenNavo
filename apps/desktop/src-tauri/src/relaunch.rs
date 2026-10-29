//! Reopen through LaunchServices so macOS assigns the new process its own TCC identity.
use serde::{Deserialize, Serialize};
use std::{
    path::Path,
    time::{SystemTime, UNIX_EPOCH},
};

#[cfg(any(target_os = "macos", test))]
use std::{fs::OpenOptions, io::Write};

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
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    links: Vec<String>,
}

fn now_ms() -> std::io::Result<u64> {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(std::io::Error::other)?
        .as_millis()
        .try_into()
        .map_err(std::io::Error::other)
}

fn fresh(bytes: &[u8], now: u64) -> Option<Marker> {
    serde_json::from_slice::<Marker>(bytes)
        .ok()
        .filter(|marker| now.checked_sub(marker.at).is_some_and(|age| age <= 120_000))
}

fn consume_at(data_dir: &Path, now: Option<u64>) -> (bool, Vec<String>) {
    let path = data_dir.join("relaunch.json");
    let bytes = std::fs::read(&path);
    let removed = match std::fs::remove_file(&path) {
        Ok(()) => true,
        Err(error) => {
            if error.kind() != std::io::ErrorKind::NotFound {
                log::warn!("Could not remove relaunch marker: {error}");
            }
            false
        }
    };
    bytes
        .ok()
        .zip(now)
        .and_then(|(bytes, now)| fresh(&bytes, now))
        .map(|marker| {
            // Retain the restart-loop guard on unlink failure, but only replay links after removal.
            let links = if removed {
                crate::deeplink::validated_links(marker.links)
            } else {
                Vec::new()
            };
            (true, links)
        })
        .unwrap_or_default()
}

pub fn consume(data_dir: &Path) -> bool {
    consume_startup(data_dir).0
}

pub(crate) fn consume_startup(data_dir: &Path) -> (bool, Vec<String>) {
    consume_at(data_dir, now_ms().ok())
}

#[cfg(any(target_os = "macos", test))]
fn write_at(data_dir: &Path, reason: Reason, at: u64, links: &[String]) -> std::io::Result<()> {
    let bytes = serde_json::to_vec(&Marker {
        at,
        reason,
        links: crate::deeplink::validated_links(links),
    })
    .map_err(std::io::Error::other)?;
    let path = data_dir.join("relaunch.json");
    let temporary = data_dir.join(format!(".relaunch-{}.tmp", uuid::Uuid::now_v7()));
    let mut created = false;
    let result = (|| {
        let mut options = OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let mut file = options.open(&temporary)?;
        created = true;
        file.write_all(&bytes)?;
        file.sync_all()?;
        drop(file);
        // Replacing the path itself also replaces a pre-existing symlink, without following it.
        std::fs::rename(&temporary, &path)
    })();
    if result.is_err() && created {
        let _ = std::fs::remove_file(&temporary);
    }
    result
}

#[cfg(target_os = "macos")]
fn write(data_dir: &Path, reason: Reason, links: &[String]) -> std::io::Result<()> {
    write_at(data_dir, reason, now_ms()?, links)
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
    write(data_dir, reason, &[])?;
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
    let links = crate::deeplink::startup_links(app);
    if let Err(error) = write(&core.data_dir, Reason::SelfHeal, &links) {
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
        write_at(dir.path(), Reason::SelfHeal, 1000, &[]).unwrap();
        let (fresh, _) = consume_at(dir.path(), Some(1001));
        assert!(!should_self_heal(true, true, false, true, fresh));
    }

    #[test]
    fn freshness_rejects_expiry_clock_rollback_and_malformed_markers() {
        let bytes = br#"{"at":1000,"reason":"user"}"#;
        assert!(fresh(bytes, 1000).is_some());
        assert!(fresh(bytes, 121_000).is_some());
        assert!(fresh(bytes, 121_001).is_none());
        assert!(fresh(bytes, 999).is_none());
        for bytes in [
            b"not json".as_slice(),
            br#"{"at":1000}"#,
            br#"{"at":1000,"reason":"unknown"}"#,
            br#"{"at":-1,"reason":"user"}"#,
            br#"{"at":"1000","reason":"user"}"#,
        ] {
            assert!(fresh(bytes, 1000).is_none());
        }
    }

    #[test]
    fn markers_are_consumed_once_even_when_invalid_or_expired() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("relaunch.json");
        assert_eq!(consume_at(dir.path(), Some(1000)), (false, Vec::new()));
        for reason in [Reason::User, Reason::SelfHeal] {
            write_at(dir.path(), reason, 1000, &[]).unwrap();
            assert_eq!(consume_at(dir.path(), Some(1000)), (true, Vec::new()));
            assert!(!path.exists());
            assert_eq!(consume_at(dir.path(), Some(1000)), (false, Vec::new()));
        }
        for (bytes, now) in [
            (br#"{"at":1000,"reason":"user"}"#.as_slice(), Some(121_001)),
            (br#"{"at":1000,"reason":"self_heal"}"#.as_slice(), Some(999)),
            (b"invalid".as_slice(), Some(1000)),
            (br#"{"at":1000,"reason":"user"}"#.as_slice(), None),
        ] {
            std::fs::write(&path, bytes).unwrap();
            assert_eq!(consume_at(dir.path(), now), (false, Vec::new()));
            assert!(!path.exists());
        }
    }

    #[test]
    fn normal_write_creates_a_marker() {
        let dir = tempfile::tempdir().unwrap();
        write_at(dir.path(), Reason::SelfHeal, 1000, &[]).unwrap();
        let bytes = std::fs::read(dir.path().join("relaunch.json")).unwrap();
        assert_eq!(bytes, br#"{"at":1000,"reason":"self_heal"}"#);
        assert!(fresh(&bytes, 1000).is_some());
    }

    #[test]
    fn existing_regular_marker_is_replaced() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("relaunch.json");
        std::fs::write(&path, br#"{"at":1,"reason":"user"}"#).unwrap();
        write_at(dir.path(), Reason::SelfHeal, 1000, &[]).unwrap();
        let marker = fresh(&std::fs::read(&path).unwrap(), 1000).unwrap();
        assert_eq!(marker.at, 1000);
        assert!(matches!(marker.reason, Reason::SelfHeal));
    }

    #[cfg(unix)]
    #[test]
    fn preexisting_symlink_is_replaced_without_touching_its_target() {
        use std::os::unix::fs::symlink;

        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("relaunch.json");
        let target = dir.path().join("target.json");
        std::fs::write(&target, b"keep me").unwrap();
        symlink(&target, &path).unwrap();

        write_at(dir.path(), Reason::SelfHeal, 1000, &[]).unwrap();

        assert_eq!(std::fs::read(&target).unwrap(), b"keep me");
        assert!(
            !std::fs::symlink_metadata(&path)
                .unwrap()
                .file_type()
                .is_symlink()
        );
        assert!(fresh(&std::fs::read(&path).unwrap(), 1000).is_some());
    }

    #[test]
    fn failed_replacement_cleans_up_the_temporary_file() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("relaunch.json");
        std::fs::create_dir(&path).unwrap();

        assert!(write_at(dir.path(), Reason::SelfHeal, 1000, &[]).is_err());

        assert!(path.is_dir());
        assert_eq!(std::fs::read_dir(dir.path()).unwrap().count(), 1);
    }

    #[tokio::test]
    async fn validated_links_survive_relaunch_and_are_delivered_once() {
        use crate::{
            core::DesktopCore, deeplink::Router, events::CoreEvent, model::DeepLinkAction,
        };
        use std::sync::{Arc, Mutex};

        let dir = tempfile::tempdir().unwrap();
        let now = now_ms().unwrap();
        let link = "opennavo://package/cask/ghostty?action=install";
        let invalid = "opennavo://updates?action=remove";
        write_at(
            dir.path(),
            Reason::SelfHeal,
            now,
            &[link.into(), invalid.into(), link.into(), "--hidden".into()],
        )
        .unwrap();
        let marker: Marker =
            serde_json::from_slice(&std::fs::read(dir.path().join("relaunch.json")).unwrap())
                .unwrap();
        assert_eq!(marker.links, vec![link]);

        let core = DesktopCore::open(
            dir.path().to_owned(),
            dir.path().join("cache"),
            dir.path().join("unused-askpass"),
            Arc::new(|_| {}),
        )
        .unwrap();
        assert!(core.relaunch_fresh);
        let links = core.take_relaunch_links();
        assert_eq!(links, vec![link]);
        assert!(core.take_relaunch_links().is_empty());
        assert_eq!(consume_at(dir.path(), Some(now)), (false, Vec::new()));

        let received = Arc::new(Mutex::new(Vec::new()));
        let saved = received.clone();
        let router = Router::new(
            Arc::new(move |event| saved.lock().unwrap().push(event)),
            Arc::new(|| {}),
        );
        for link in links {
            router.receive(&link);
        }
        // A fallback restart may also retain the original argv; startup sources share router deduplication.
        router.receive_args(&["OpenNavo".into(), link.into()]);
        router.ready();
        router.ready();
        let received = received.lock().unwrap();
        assert_eq!(received.len(), 1);
        assert!(matches!(&received[0], CoreEvent::Deeplink(event)
            if event.url == link && event.action == Some(DeepLinkAction::Install)));
    }

    #[test]
    fn invalid_and_expired_persisted_links_are_never_replayed() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("relaunch.json");
        for (bytes, now, expected_fresh) in [
            (br#"{"at":1000,"reason":"self_heal","links":["opennavo://updates?action=remove","--hidden"]}"#.as_slice(), 1001, true),
            (br#"{"at":1000,"reason":"self_heal","links":["opennavo://updates"]}"#.as_slice(), 121_001, false),
        ] {
            std::fs::write(&path, bytes).unwrap();
            assert_eq!(consume_at(dir.path(), Some(now)), (expected_fresh, Vec::new()));
            assert!(!path.exists());
        }
    }
}
