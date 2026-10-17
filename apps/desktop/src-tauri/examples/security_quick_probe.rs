//! Explicit read-only quick scan with cached definitions and a cached startup
//! inventory. All results, runtime files and logs belong to a temporary fixture.
//! No freshclam, startup enumeration, quarantine or system notifications.
use opennavo_desktop_lib::{
    db::Database,
    events::CoreEvent,
    model::Settings,
    security::{self, AvScanKind, AvScanState, Service, StartupSnapshot},
};
use std::{
    path::PathBuf,
    sync::{Arc, RwLock},
    time::{Duration, Instant},
};
struct Logger;
impl log::Log for Logger {
    fn enabled(&self, _: &log::Metadata<'_>) -> bool {
        true
    }
    fn log(&self, record: &log::Record<'_>) {
        eprintln!("{}: {}", record.level(), record.args());
    }
    fn flush(&self) {}
}
static LOGGER: Logger = Logger;
fn main() -> Result<(), Box<dyn std::error::Error>> {
    if let Some(code) = opennavo_desktop_lib::security::signature::helper_entry() {
        std::process::exit(code);
    }
    let args = std::env::args().skip(1).collect::<Vec<_>>();
    if args.len() != 5 || args[0] != "--cached-read-only" {
        return Err(
            "usage: security_quick_probe --cached-read-only HOME HELPERS DB_DIR STARTUP_JSON"
                .into(),
        );
    }
    log::set_logger(&LOGGER).map_err(|e| e.to_string())?;
    log::set_max_level(log::LevelFilter::Info);
    let temp = tempfile::tempdir()?;
    let root = temp.path().canonicalize()?;
    let data = root.join("app-data");
    let database = Database::open(&data.join("opennavo.db"))?;
    let startup: StartupSnapshot = serde_json::from_slice(&std::fs::read(&args[4])?)?;
    database.with(|c| {
        for item in &startup.items {
            c.execute(
                "INSERT INTO security_startup(id,value) VALUES(?1,?2)",
                rusqlite::params![item.id, serde_json::to_string(item)?],
            )?;
        }
        Ok(())
    })?;
    let started = Instant::now();
    let service = Service::new(
        database,
        Arc::new(RwLock::new(Settings {
            av_consent: true,
            ..Default::default()
        })),
        Arc::new(move |event| {
            if let CoreEvent::AvProgress(progress) = event {
                println!(
                    "{}",
                    serde_json::json!({"elapsedSeconds":started.elapsed().as_secs_f64(), "progress":progress})
                );
            }
        }),
        PathBuf::from(&args[1]).canonicalize()?,
        data.join("logs/security"),
        security::av::Config {
            helpers: PathBuf::from(&args[2]).canonicalize()?,
            database: PathBuf::from(&args[3]).canonicalize()?,
            runtime: data.join("logs/security/av/run"),
            idle: Duration::from_secs(1),
        },
        Arc::new(security::NativeCommands),
    );
    let scan = service.scan_start(AvScanKind::Quick, None)?;
    let mut peak = 0;
    loop {
        let result = service.scan_get(&scan.scan_id)?;
        peak = peak.max(service.av.status()?.resident_bytes.unwrap_or(0));
        if result.state != AvScanState::Running {
            println!(
                "{}",
                serde_json::json!({"seconds":started.elapsed().as_secs_f64(),"peakResidentBytes":peak,"cachedStartupItems":startup.items.len(),"result":result})
            );
            if result.state != AvScanState::Done {
                return Err("quick scan did not complete".into());
            }
            break;
        }
        if started.elapsed() > Duration::from_secs(900) {
            service.scan_cancel(&scan.scan_id)?;
            // Keep the fixture until its worker has stopped and released files.
            while service.scan_get(&scan.scan_id)?.state == AvScanState::Running {
                std::thread::sleep(Duration::from_millis(100));
            }
            return Err("quick scan exceeded measurement window (canceled)".into());
        }
        std::thread::sleep(Duration::from_millis(500));
    }
    drop(service);
    Ok(())
}
