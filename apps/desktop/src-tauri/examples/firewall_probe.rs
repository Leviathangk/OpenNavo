// SPDX-License-Identifier: GPL-3.0-only
// Read-only, headless inspection. Never creates an XPC connection, activates an
// extension or changes filter preferences. Use --bundle to inspect signed output.
use std::{error::Error, path::Path};

fn inspect_bundle(app: &Path) -> Result<serde_json::Value, Box<dyn Error>> {
    let host = plist::Value::from_file(app.join("Contents/Info.plist"))?;
    let host_id = host
        .as_dictionary()
        .and_then(|p| p.get("CFBundleIdentifier"))
        .and_then(plist::Value::as_string)
        .ok_or("Missing host identifier")?;
    if host_id.is_empty()
        || !host_id
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b".-".contains(&b))
    {
        return Err("Invalid host identifier".into());
    }
    let extension = app.join(format!(
        "Contents/Library/SystemExtensions/{host_id}.firewall.systemextension/Contents/Info.plist"
    ));
    let info = plist::Value::from_file(extension)?;
    let info = info.as_dictionary().ok_or("Invalid extension Info.plist")?;
    let version = info
        .get("CFBundleVersion")
        .and_then(plist::Value::as_string)
        .ok_or("Missing build version")?;
    if version.is_empty()
        || !version
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b".-".contains(&b))
    {
        return Err("Invalid Mach service build version".into());
    }
    let service = info
        .get("NetworkExtension")
        .and_then(plist::Value::as_dictionary)
        .and_then(|p| p.get("NEMachServiceName"))
        .and_then(plist::Value::as_string)
        .ok_or("Missing Mach service")?;
    let (team, suffix) = service.split_once('.').ok_or("Missing App Group prefix")?;
    if team.len() != 10
        || !team
            .bytes()
            .all(|b| b.is_ascii_uppercase() || b.is_ascii_digit())
        || suffix != format!("{host_id}.firewall.{version}")
        || host
            .as_dictionary()
            .and_then(|p| p.get("CFBundleVersion"))
            .and_then(plist::Value::as_string)
            != Some(version)
    {
        return Err("Mach service does not match the bundle version and App Group prefix".into());
    }
    Ok(
        serde_json::json!({"bundleId": host_id, "bundleVersion": version, "machServiceName": service}),
    )
}

fn main() -> Result<(), Box<dyn Error>> {
    let args: Vec<_> = std::env::args_os().skip(1).collect();
    let value = if args.len() == 2 && args[0] == "--bundle" {
        inspect_bundle(Path::new(&args[1]))?
    } else if args.is_empty() {
        let status = opennavo_desktop_lib::firewall::bridge::status(
            &opennavo_desktop_lib::firewall::bridge::Native,
        )
        .map_err(|_| "native firewall status unavailable")?;
        assert_eq!(
            status.availability,
            opennavo_desktop_lib::firewall::FirewallAvailability::NotBundled
        );
        serde_json::to_value(status)?
    } else {
        return Err("Usage: firewall_probe [--bundle /path/to/App.app]".into());
    };
    println!("{}", serde_json::to_string(&value)?);
    Ok(())
}
