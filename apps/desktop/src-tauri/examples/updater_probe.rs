//! Read-only local discovery. No update downloads, purchases, settings, desktop
//! data directory, registration, mounting, or installation are touched.
use opennavo_desktop_lib::updater::{electron, feed, macho, service};
use std::{fs, path::PathBuf};

fn main() -> Result<(), Box<dyn std::error::Error>> {
    if std::env::args().skip(1).collect::<Vec<_>>() != ["--real-read-only"] {
        return Err("Usage: updater_probe --real-read-only".into());
    }
    let (os, arch) = service::system()?;
    let mut roots = vec![PathBuf::from("/Applications")];
    if let Some(home) = std::env::var_os("HOME") {
        roots.push(PathBuf::from(home).join("Applications"));
    }
    let mut apps = 0;
    let mut sparkle = 0;
    let mut eddsa = 0;
    let mut electron = 0;
    let mut inspected = 0;
    for root in roots {
        let Ok(entries) = fs::read_dir(root) else {
            continue;
        };
        for entry in entries.flatten() {
            if !entry.file_type()?.is_dir() || entry.path().extension().is_none_or(|e| e != "app") {
                continue;
            }
            let path = entry.path();
            let Ok(info) = plist::Value::from_file(path.join("Contents/Info.plist")) else {
                continue;
            };
            let Some(info) = info.as_dictionary() else {
                continue;
            };
            apps += 1;
            electron += usize::from(electron::Config::local(&path).is_ok());
            if info
                .get("SUFeedURL")
                .and_then(|v| v.as_string())
                .is_none_or(|u| feed::https(u).is_err())
            {
                continue;
            }
            sparkle += 1;
            eddsa += usize::from(
                info.get("SUPublicEDKey")
                    .and_then(|v| v.as_string())
                    .is_some_and(|s| !s.is_empty()),
            );
            if let Some(binary) = info
                .get("CFBundleExecutable")
                .and_then(|v| v.as_string())
                .filter(|s| !s.contains('/') && *s != "." && *s != "..")
            {
                inspected +=
                    usize::from(macho::inspect(&path.join("Contents/MacOS").join(binary)).is_ok());
            }
        }
    }
    println!(
        "{}",
        serde_json::json!({"readOnly":true,"os":os,"arch":arch,"apps":apps,"sparkle":sparkle,"electron":electron,"eddsaKeys":eddsa,"machOInspected":inspected,"networkRequests":0})
    );
    Ok(())
}
