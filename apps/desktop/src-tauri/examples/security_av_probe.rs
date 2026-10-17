//! Explicit cached-database probe. Writes only a temporary fixture directory;
//! never invokes freshclam, installs an app, or changes a launch item.
use opennavo_desktop_lib::{
    db::Database,
    model::Settings,
    security::{self, AvScanKind, AvScanState, FindingAction, Service},
};
use std::{
    path::PathBuf,
    sync::{Arc, RwLock},
    time::{Duration, Instant},
};
fn main() -> Result<(), Box<dyn std::error::Error>> {
    if let Some(code) = opennavo_desktop_lib::security::signature::helper_entry() {
        std::process::exit(code);
    }
    let args: Vec<_> = std::env::args().skip(1).collect();
    if args.len() != 3 || args[0] != "--cached-temp-only" {
        return Err("usage: security_av_probe --cached-temp-only HELPER_DIR DB_DIR".into());
    }
    let temp = tempfile::tempdir()?;
    let root = temp.path().canonicalize()?;
    let home = root.join("home");
    std::fs::create_dir(&home)?;
    let data = root.join("security");
    let db = Database::open(&root.join("app-data/db"))?;
    let settings = Arc::new(RwLock::new(Settings {
        av_consent: true,
        ..Default::default()
    }));
    let config = security::av::Config {
        helpers: PathBuf::from(&args[1]).canonicalize()?,
        database: PathBuf::from(&args[2]).canonicalize()?,
        runtime: root.join("run"),
        idle: Duration::from_secs(300),
    };
    let service = Service::new(
        db,
        settings,
        Arc::new(|_| {}),
        home.clone(),
        data,
        config,
        Arc::new(security::NativeCommands),
    );
    // Construct the harmless standard test signature only in the temporary file.
    let test = [
        "X5O!P%@AP[4\\PZX54(P^)7CC)7}",
        "$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*",
    ]
    .concat();
    let path = home.join("sample.txt");
    std::fs::write(&path, test)?;
    let started = Instant::now();
    let scan = service.scan_start(
        AvScanKind::Custom,
        Some(vec![path.to_string_lossy().into_owned()]),
    )?;
    let result = loop {
        let r = service.scan_get(&scan.scan_id)?;
        if r.state != AvScanState::Running {
            break r;
        }
        if started.elapsed() > Duration::from_secs(100) {
            return Err("scan timeout".into());
        }
        std::thread::sleep(Duration::from_millis(100));
    };
    let load_scan_secs = started.elapsed().as_secs_f64();
    if result.state != AvScanState::Done || result.findings.len() != 1 {
        return Err(format!("EICAR failed: {:?}", result.state).into());
    }
    let memory = service.av.status()?.resident_bytes;
    let action = service.findings_act(
        &scan.scan_id,
        vec![result.findings[0].id.clone()],
        FindingAction::Quarantine,
    )?;
    if action.done.len() != 1 || path.exists() {
        return Err(format!("quarantine: {:?}", action.failed).into());
    }
    let q = service.quarantine_list()?;
    let restore = service.quarantine_restore(vec![q[0].id.clone()])?;
    if restore.done.len() != 1 || !path.exists() {
        return Err("restore failed".into());
    }
    std::fs::remove_file(&path)?;
    let clean = home.join("clean.bin");
    std::fs::write(&clean, vec![42_u8; 16 * 1024 * 1024])?;
    let start = Instant::now();
    let scan = service.scan_start(
        AvScanKind::Custom,
        Some(vec![clean.to_string_lossy().into_owned()]),
    )?;
    loop {
        let result = service.scan_get(&scan.scan_id)?;
        if result.state != AvScanState::Running {
            if result.state != AvScanState::Done {
                return Err("clean scan failed".into());
            }
            break;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    let scan_secs = start.elapsed().as_secs_f64();
    let idle = Instant::now();
    println!(
        "{}",
        serde_json::json!({"eicar":true,"quarantineRestore":true,"loadScanSecs":load_scan_secs,"residentBytes":memory,"corpusBytes":16*1024*1024,"scanSecs":scan_secs,"mibPerSec":16.0/scan_secs,"waitingForIdleSeconds":300})
    );
    while service.av.status()?.running {
        if idle.elapsed() > Duration::from_secs(315) {
            return Err("idle shutdown failed".into());
        }
        std::thread::sleep(Duration::from_secs(1));
    }
    println!(
        "{}",
        serde_json::json!({"idleExited":true,"idleSeconds":idle.elapsed().as_secs_f64(),"cdnDownloads":0})
    );
    Ok(())
}
