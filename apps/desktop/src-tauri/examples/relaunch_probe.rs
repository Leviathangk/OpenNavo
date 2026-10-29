//! Native relaunch smoke test, run only from a signed, isolated Release .app.
//! Place probe-mode beside the bundle: user, self_heal, or hidden. All state and
//! results stay beside that bundle; no services, installs, or permission changes.
//! self_heal/hidden inject a pending startup signal to exercise the release guard.
//! Build with: cargo build --release --example relaunch_probe.
use opennavo_desktop_lib::{core::DesktopCore, guide, lifecycle, relaunch};
use std::{fs, path::Path, sync::Arc};
use tauri::Manager;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    if cfg!(debug_assertions) {
        return Err("relaunch_probe requires a Release build".into());
    }
    let bundle = guide::app_bundle(&std::env::current_exe()?)
        .ok_or("relaunch_probe must be inside an isolated .app bundle")?;
    let root = bundle.parent().ok_or("missing probe directory")?.to_owned();
    let mode = fs::read_to_string(root.join("probe-mode"))?;
    let mode = mode.trim().to_owned();
    if !matches!(mode.as_str(), "user" | "self_heal" | "hidden") {
        return Err("probe-mode must be user, self_heal, or hidden".into());
    }
    let launches = root.join("launches.jsonl");
    if fs::read_to_string(&launches)
        .unwrap_or_default()
        .lines()
        .count()
        >= 2
    {
        return Err("unexpected third launch".into());
    }
    let core = DesktopCore::open(
        root.join("data"),
        root.join("cache"),
        root.join("unused-askpass"),
        Arc::new(|_| {}),
    )?;
    let mut core = Arc::try_unwrap(core).map_err(|_| "probe core is shared")?;
    let hidden = lifecycle::hidden_launch(&std::env::args().collect::<Vec<_>>());
    let fresh = core.relaunch_fresh;
    let owner = guide::owner_bundle();
    use std::io::Write;
    writeln!(
        fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(launches)?,
        "{}",
        serde_json::json!({
            "pid": std::process::id(), "mode": mode, "fresh": fresh, "hidden": hidden,
            "owner": owner, "bundle": bundle,
        })
    )?;
    if fresh && (hidden || owner.as_deref() != Some(bundle.as_path())) {
        return Err("LaunchServices must restore the bundle's identity without --hidden".into());
    }
    if relaunch::consume(&core.data_dir) {
        return Err("relaunch marker must be consumed exactly once".into());
    }
    if mode == "user" {
        if fresh {
            complete(&root)?;
        } else if !relaunch::reopen(&core.data_dir, relaunch::Reason::User)? {
            return Err("bundled relaunch was not scheduled".into());
        }
        return Ok(());
    }
    core.startup_full_disk_pending = true;
    let result_root = root.clone();
    // Empty assets avoid embedding a second Info.plist alongside the library's.
    // Builder::default still runs the real native Wry runtime, without a WebView.
    let mut context = tauri::test::mock_context(tauri::test::noop_assets());
    context.config_mut().identifier = "com.opennavo.relaunchprobe.restart20261029".into();
    let app = tauri::Builder::default()
        .setup(move |app| {
            let healing = relaunch::self_heal(app.handle(), &core, hidden);
            if healing != (mode == "self_heal" && !fresh && !hidden) {
                return Err("unexpected automatic relaunch decision".into());
            }
            if !healing {
                if mode == "self_heal" && !fresh {
                    return Err("automatic relaunch never reached its second process".into());
                }
                if mode == "hidden" && (!hidden || fresh) {
                    return Err("hidden launch must not automatically restart".into());
                }
                complete(&result_root)?;
                let lifecycle = Arc::new(lifecycle::Lifecycle::default());
                lifecycle.ready();
                app.manage(lifecycle);
                app.handle().exit(0);
            }
            Ok(())
        })
        .build(context)?;
    app.run(lifecycle::run_event);
    Ok(())
}

fn complete(root: &Path) -> std::io::Result<()> {
    fs::write(
        root.join("passed"),
        "LaunchServices relaunch probe passed\n",
    )
}
