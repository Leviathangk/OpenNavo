//! The menu bar monitor outlives a normal Quit as a hidden instance of the app.
//!
//! Keeping the monitor in the app process avoids a second bundle, a second settings store, duplicate alerts and an
//! IPC protocol. The hidden instance owns the same tray and monitor panel; reopening OpenNavo attaches to it through
//! the single-instance plugin and restores the Dock icon. The trade-off is the app's memory cost while resident, not
//! a second resident runtime.

use crate::{AppError, core::DesktopCore, monitor::sampler::Sampler};
use serde::{Deserialize, Serialize};
use std::sync::{
    Arc,
    atomic::{AtomicBool, Ordering},
};
use tauri::Manager;

/// The exact name macOS shows under Login Items & Extensions; the notice uses the same string.
pub const LOGIN_ITEM_NAME: &str = "OpenNavo Monitor";

/// Set only after `monitor::install` succeeds; a managed sampler alone can survive a failed install.
#[derive(Default)]
pub struct ResidentRuntime {
    active: AtomicBool,
}

pub fn install<R: tauri::Runtime>(app: &tauri::AppHandle<R>) {
    app.manage(ResidentRuntime::default());
}

pub fn mark_active<R: tauri::Runtime>(app: &tauri::AppHandle<R>) {
    if let Some(state) = app.try_state::<ResidentRuntime>() {
        state.active.store(true, Ordering::Release);
    }
}
/// Before this feature the login item used the app name; remove that old LaunchAgent after registering the new name.
const LEGACY_LOGIN_ITEM_NAME: &str = "OpenNavo";

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct MonitorResidentStatus {
    /// The user's switch. False means a Quit ends the monitor too.
    pub enabled: bool,
    /// Whether macOS currently has the login item switched on.
    pub registered: bool,
    /// This process currently has a working sampler and menu bar items.
    pub active: bool,
    /// The one-time notice has not been acknowledged and registration succeeded.
    pub notice_pending: bool,
    /// The name macOS lists for the login item, so the notice can match it exactly.
    pub monitor_name: String,
    /// macOS 15 renamed the Login Items pane; the frontend picks the matching localized name.
    pub login_items_pane_new: bool,
    /// Debug builds do not touch the user's login items.
    pub can_register: bool,
}

pub fn set_autostart<R: tauri::Runtime>(
    app: &tauri::AppHandle<R>,
    enabled: bool,
) -> Result<(), AppError> {
    let manager = app
        .try_state::<tauri_plugin_autostart::AutoLaunchManager>()
        .ok_or_else(|| AppError::new("E_UNKNOWN", "autostart_unavailable"))?;
    if enabled {
        manager
            .enable()
            .map_err(|e| AppError::new("E_UNKNOWN", "autostart").with_detail(e.to_string()))?;
        // Upgrading from the old app-named item must not leave two entries that both start OpenNavo.
        remove_legacy_login_item();
        Ok(())
    } else {
        manager
            .disable()
            .map_err(|e| AppError::new("E_UNKNOWN", "autostart").with_detail(e.to_string()))?;
        // A failed migration must not leave the old agent starting the app after the switch is off.
        remove_legacy_login_item();
        Ok(())
    }
}

/// Remove only the previous OpenNavo LaunchAgent, recognized by its label and app path. A user's unrelated
/// `OpenNavo.plist` is left alone.
fn remove_legacy_login_item() {
    if LOGIN_ITEM_NAME == LEGACY_LOGIN_ITEM_NAME {
        return;
    }
    let Some(home) = std::env::var_os("HOME") else {
        return;
    };
    let path = std::path::PathBuf::from(home)
        .join("Library/LaunchAgents")
        .join(format!("{LEGACY_LOGIN_ITEM_NAME}.plist"));
    remove_legacy_login_item_at(&path);
}

fn remove_legacy_login_item_at(path: &std::path::Path) {
    let Ok(contents) = std::fs::read_to_string(path) else {
        return;
    };
    let label = format!("<string>{LEGACY_LOGIN_ITEM_NAME}</string>");
    if !contents.contains(&label) || !contents.contains("/OpenNavo.app/") {
        return;
    }
    if let Err(error) = std::fs::remove_file(path) {
        log::warn!("Could not remove the old login item: {error}");
    }
}

/// Test apps and early setup do not run the autostart plugin. Treat that as "not registered" instead of panicking.
pub fn is_registered<R: tauri::Runtime>(app: &tauri::AppHandle<R>) -> bool {
    let file_exists = app
        .try_state::<tauri_plugin_autostart::AutoLaunchManager>()
        .and_then(|manager| manager.is_enabled().ok())
        .unwrap_or(false);
    // The plugin only checks that the plist exists. Login Items can switch the agent off while leaving the plist
    // behind, so ask launchd for its disabled override too. A stale plist that points at a deleted bundle is not
    // a working registration either.
    file_exists
        && !login_item_disabled(LOGIN_ITEM_NAME)
        && login_item_points_to_current() != Some(false)
}

fn login_item_plist_path() -> Option<std::path::PathBuf> {
    std::env::var_os("HOME").map(|home| {
        std::path::PathBuf::from(home)
            .join("Library/LaunchAgents")
            .join(format!("{LOGIN_ITEM_NAME}.plist"))
    })
}

fn current_bundle_path() -> Option<std::path::PathBuf> {
    // The autostart plugin canonicalizes the executable before writing the agent, so compare canonical paths.
    let exe = std::env::current_exe().ok()?.canonicalize().ok()?;
    let bundle = exe.ancestors().nth(3)?;
    (bundle.extension().and_then(|value| value.to_str()) == Some("app"))
        .then(|| bundle.to_path_buf())
}

fn login_item_points_to_current() -> Option<bool> {
    let path = login_item_plist_path()?;
    let contents = std::fs::read_to_string(path).ok()?;
    let bundle = current_bundle_path()?;
    Some(plist_points_to_bundle(&contents, &bundle))
}

fn plist_points_to_bundle(contents: &str, bundle: &std::path::Path) -> bool {
    bundle
        .to_str()
        .is_some_and(|bundle| contents.contains(&format!("{bundle}/Contents/")))
}

fn login_item_disabled(label: &str) -> bool {
    let uid = std::process::Command::new("/usr/bin/id").arg("-u").output();
    let Ok(uid) = uid else {
        return false;
    };
    let Ok(uid) = String::from_utf8(uid.stdout) else {
        return false;
    };
    let Ok(output) = std::process::Command::new("/bin/launchctl")
        .args(["print-disabled", &format!("gui/{}", uid.trim())])
        .output()
    else {
        return false;
    };
    login_item_disabled_in(&String::from_utf8_lossy(&output.stdout), label)
}

fn login_item_disabled_in(output: &str, label: &str) -> bool {
    output.contains(&format!("\"{label}\" => disabled"))
}

/// First launch after installing or upgrading: register once, then remember that the attempt happened. A login item
/// the user later switches off in System Settings is left off; the status says so instead of silently re-enabling it.
pub fn ensure_configured<R: tauri::Runtime>(
    app: &tauri::AppHandle<R>,
    core: &Arc<DesktopCore>,
) -> Result<(), AppError> {
    let settings = core.current_settings()?;
    if !settings.monitor_resident || cfg!(debug_assertions) {
        return Ok(());
    }
    if settings.monitor_resident_configured {
        // The app can be moved between launches. Refresh a stale path, but leave an item the user switched off
        // alone: that is an explicit choice, not a stale registration.
        if login_item_points_to_current() == Some(false)
            && !login_item_disabled(LOGIN_ITEM_NAME)
            && let Err(error) = set_autostart(app, true)
        {
            log::warn!("Resident monitor re-registration failed: {}", error.code);
        }
        return Ok(());
    }
    let registered = set_autostart(app, true).is_ok();
    core.mark_monitor_resident_configured()?;
    if !registered {
        log::warn!("Resident monitor login item could not be registered");
    }
    Ok(())
}

/// Retry registration from Settings without changing the switch.
pub fn register<R: tauri::Runtime>(
    app: &tauri::AppHandle<R>,
    core: &Arc<DesktopCore>,
) -> Result<MonitorResidentStatus, AppError> {
    if cfg!(debug_assertions) {
        return Err(AppError::new("E_RESIDENT_DEV", "monitor_resident"));
    }
    if !core.current_settings()?.monitor_resident {
        return Err(AppError::new("E_INVALID_ARG", "monitor_resident"));
    }
    set_autostart(app, true)?;
    core.mark_monitor_resident_configured()?;
    status(app)
}

pub fn status<R: tauri::Runtime>(
    app: &tauri::AppHandle<R>,
) -> Result<MonitorResidentStatus, AppError> {
    let core = app
        .try_state::<Arc<DesktopCore>>()
        .ok_or_else(|| AppError::new("E_UNKNOWN", "core_unavailable"))?;
    let settings = core.current_settings()?;
    let registered = is_registered(app);
    let active = runtime_active(app);
    let can_register = !cfg!(debug_assertions);
    Ok(MonitorResidentStatus {
        enabled: settings.monitor_resident,
        registered,
        active,
        notice_pending: settings.monitor_resident
            && registered
            && active
            && !settings.monitor_resident_intro
            && can_register,
        monitor_name: LOGIN_ITEM_NAME.to_owned(),
        login_items_pane_new: login_items_pane_new(),
        can_register,
    })
}

fn login_items_pane_new() -> bool {
    // macOS 15 renamed the pane; the old name stays for macOS 13 and 14.
    std::process::Command::new("sw_vers")
        .arg("-productVersion")
        .output()
        .ok()
        .and_then(|out| String::from_utf8(out.stdout).ok())
        .map(|value| login_items_pane_new_for(&value))
        .unwrap_or(true)
}

fn login_items_pane_new_for(version: &str) -> bool {
    version
        .trim()
        .split('.')
        .next()
        .and_then(|major| major.parse::<u32>().ok())
        .unwrap_or(15)
        >= 15
}

fn runtime_active<R: tauri::Runtime>(app: &tauri::AppHandle<R>) -> bool {
    app.try_state::<ResidentRuntime>()
        .is_some_and(|state| state.active.load(Ordering::Acquire))
        && app.try_state::<Arc<Sampler>>().is_some()
}

/// Whether a normal Quit should hand over to a hidden successor. A login item switched off in System Settings wins
/// over the switch: the monitor must not keep coming back after the user disabled it there.
pub fn should_succeed<R: tauri::Runtime>(app: &tauri::AppHandle<R>) -> bool {
    let Some(core) = app.try_state::<Arc<DesktopCore>>() else {
        return false;
    };
    core.current_settings()
        .map(|settings| settings.monitor_resident && is_registered(app) && runtime_active(app))
        .unwrap_or(false)
}

/// Wait for this process to release the single-instance lock before starting the hidden successor. Opening the
/// bundle directly would race that lock and exit immediately.
const HANDOFF: &str = r#"i=0
while kill -0 "$1" 2>/dev/null && [ "$i" -lt 300 ]; do
    sleep 0.1
    i=$((i + 1))
done
exec /usr/bin/open -n "$2" --args --hidden"#;

pub fn spawn_successor<R: tauri::Runtime>(app: &tauri::AppHandle<R>) -> bool {
    if !should_succeed(app) || cfg!(debug_assertions) {
        return false;
    }
    #[cfg(target_os = "macos")]
    {
        let Ok(exe) = std::env::current_exe() else {
            return false;
        };
        let Some(bundle) = exe.ancestors().nth(3) else {
            return false;
        };
        if bundle.extension().and_then(|value| value.to_str()) != Some("app") {
            return false;
        }
        use std::{os::unix::process::CommandExt, process::Stdio};
        let result = std::process::Command::new("/bin/sh")
            .args(["-c", HANDOFF, "sh"])
            .arg(std::process::id().to_string())
            .arg(bundle)
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .process_group(0)
            .spawn();
        match result {
            Ok(_) => true,
            Err(error) => {
                log::warn!("Resident monitor handoff failed: {error}");
                false
            }
        }
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = app;
        false
    }
}

/// A hidden resident has no Dock icon; restoring the main window must bring the regular app back first.
pub fn set_activation<R: tauri::Runtime>(app: &tauri::AppHandle<R>, hidden: bool) {
    #[cfg(target_os = "macos")]
    {
        use tauri::ActivationPolicy;
        let policy = if hidden {
            ActivationPolicy::Accessory
        } else {
            ActivationPolicy::Regular
        };
        if let Err(error) = app.set_activation_policy(policy) {
            log::warn!("Could not set activation policy: {error}");
        }
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = (app, hidden);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn marking_the_monitor_ready_flips_the_runtime_flag() {
        let app = tauri::test::mock_app();
        install(app.handle());
        assert!(
            !app.state::<ResidentRuntime>()
                .active
                .load(Ordering::Acquire)
        );
        mark_active(app.handle());
        assert!(
            app.state::<ResidentRuntime>()
                .active
                .load(Ordering::Acquire)
        );
    }

    #[test]
    fn login_agent_path_must_match_the_current_bundle() {
        let bundle = std::path::Path::new("/Applications/OpenNavo.app");
        assert!(plist_points_to_bundle(
            "<string>/Applications/OpenNavo.app/Contents/MacOS/opennavo-desktop</string>",
            bundle
        ));
        assert!(!plist_points_to_bundle(
            "<string>/Applications/OpenNavo Old.app/Contents/MacOS/opennavo-desktop</string>",
            bundle
        ));
        assert!(!plist_points_to_bundle(
            "<string>/Applications/OpenNavo.app.backup/Contents/MacOS/opennavo-desktop</string>",
            bundle
        ));
    }

    #[test]
    fn launchctl_disabled_state_is_parsed_by_exact_label() {
        let output = "\"OpenNavo Monitor\" => disabled\n\"Other\" => enabled\n";
        assert!(login_item_disabled_in(output, LOGIN_ITEM_NAME));
        assert!(!login_item_disabled_in(output, "OpenNavo"));
        assert!(!login_item_disabled_in(
            "\"OpenNavo Monitor\" => enabled",
            LOGIN_ITEM_NAME
        ));
    }

    #[test]
    fn login_items_pane_name_branches_on_macos_version() {
        assert!(!login_items_pane_new_for("14.7.1"));
        assert!(login_items_pane_new_for("15.0"));
        assert!(login_items_pane_new_for("26.1"));
        assert!(login_items_pane_new_for("unknown"));
    }

    #[test]
    fn removes_only_the_legacy_opennavo_launch_agent() {
        let dir = tempfile::tempdir().unwrap();
        let legacy = dir.path().join("OpenNavo.plist");
        std::fs::write(
            &legacy,
            "<string>OpenNavo</string><string>/Applications/OpenNavo.app/Contents/MacOS/opennavo-desktop</string>",
        )
        .unwrap();
        remove_legacy_login_item_at(&legacy);
        assert!(!legacy.exists());

        let unrelated = dir.path().join("Unrelated.plist");
        std::fs::write(
            &unrelated,
            "<string>Other</string><string>/Applications/OpenNavo.app/Contents/MacOS/opennavo-desktop</string>",
        )
        .unwrap();
        remove_legacy_login_item_at(&unrelated);
        assert!(unrelated.exists());

        let other_app = dir.path().join("Other-app.plist");
        std::fs::write(
            &other_app,
            "<string>OpenNavo</string><string>/Applications/Other.app/Contents/MacOS/other</string>",
        )
        .unwrap();
        remove_legacy_login_item_at(&other_app);
        assert!(other_app.exists());
    }
}
