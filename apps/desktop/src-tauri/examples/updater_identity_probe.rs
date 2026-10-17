//! Inspect two existing bundles without launching, downloading, registering or
//! replacing either one. Uses the same live observations and gate as installation.
use opennavo_desktop_lib::{
    uninstall::rules::Snapshot,
    updater::{
        gate::{self, Inspector, NativeInspector},
        service,
    },
};
use std::path::Path;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<_> = std::env::args().skip(1).collect();
    if args.len() != 3 || args[0] != "--real-read-only" {
        return Err(
            "usage: updater_identity_probe --real-read-only <installed.app> <candidate.app>".into(),
        );
    }
    let installed = Path::new(&args[1]);
    let candidate = Path::new(&args[2]);
    let (os, arch) = service::system()?;
    let old = NativeInspector.inspect(installed)?;
    let new = NativeInspector.inspect(candidate)?;
    let satisfied = NativeInspector.satisfies(candidate, &old.requirement)?;
    let result = gate::bundles(
        &NativeInspector,
        installed,
        candidate,
        &Snapshot::capture(installed)?,
        &os,
        &arch,
        None,
    );
    println!(
        "{}",
        serde_json::to_string_pretty(&serde_json::json!({
            "readOnly": true, "installed": old, "candidate": new,
            "teamEqual": old.team_id == new.team_id,
            "identifierEqual": old.identifier == new.identifier,
            "requirementEqual": old.requirement == new.requirement,
            "requirementSatisfied": satisfied,
            "os": os, "arch": arch, "gate": result,
        }))?
    );
    result?;
    Ok(())
}
