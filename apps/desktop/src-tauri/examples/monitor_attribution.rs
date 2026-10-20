//! Real-machine accuracy check for the process CPU attribution. It runs the real collector and engine while it starts
//! short-lived, low-priority busy children (the way `go test` or cargo do) and reaps them promptly, and prints for each
//! window how the host's busy share splits: processes that could be listed, children that ended before they could be
//! read, and what no process can be blamed for. Without the attribution, the listed part alone explains far less of
//! a busy machine than the host reports.
//!
//! Read-only apart from the child processes it starts and ends itself; it writes nothing and talks to no one.
//! Usage: cargo run --example monitor_attribution [windows] [live children]
#[cfg(target_os = "macos")]
fn main() {
    use opennavo_desktop_lib::monitor::{
        collect::{Collector, Modules},
        engine::Engine,
        native::NativeCollector,
        types::*,
    };
    use std::{
        process::{Child, Command, Stdio},
        sync::{
            Arc,
            atomic::{AtomicBool, Ordering},
        },
        time::{Duration, Instant},
    };
    let windows: u32 = std::env::args()
        .nth(1)
        .and_then(|v| v.parse().ok())
        .unwrap_or(6);
    let live: u32 = std::env::args()
        .nth(2)
        .and_then(|v| v.parse().ok())
        .unwrap_or(10);
    const LIFETIME_SECONDS: u32 = 3;
    const WINDOW: Duration = Duration::from_secs(2);

    let modules: Modules = [MonitorModule::Cpu].into_iter().collect();
    let mut collector = NativeCollector::new().expect("native collector");
    let probe = collector.collect(&modules, false);
    let mut engine = Engine::new(collector.system(), probe);

    // A child that spins for a few seconds at the lowest priority, then ends.
    let spawn = || -> Option<Child> {
        Command::new("/usr/bin/nice")
            .args(["-n", "20", "/bin/sh", "-c"])
            .arg(format!(
                "end=$((SECONDS+{LIFETIME_SECONDS})); while [ $SECONDS -lt $end ]; do :; done"
            ))
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
            .ok()
    };
    let stop = Arc::new(AtomicBool::new(false));
    let launcher = {
        let stop = stop.clone();
        std::thread::spawn(move || {
            let mut children: Vec<Child> = Vec::new();
            let every =
                Duration::from_millis(u64::from(LIFETIME_SECONDS) * 1000 / u64::from(live.max(1)));
            let mut last_launch = Instant::now() - every;
            while !stop.load(Ordering::Acquire) {
                // Reap whatever has ended, promptly: that is when the parent's child totals grow.
                children.retain_mut(|child| child.try_wait().ok().flatten().is_none());
                if last_launch.elapsed() >= every {
                    children.extend(spawn());
                    last_launch = Instant::now();
                }
                std::thread::sleep(Duration::from_millis(30));
            }
            for mut child in children {
                let _ = child.kill();
                let _ = child.wait();
            }
        })
    };

    println!(
        "{live} busy children alive at a time, each for {LIFETIME_SECONDS} s, sampled every {} s on this machine",
        WINDOW.as_secs()
    );
    let started = Instant::now();
    for window in 0..=windows {
        let read = Instant::now();
        let frame = collector.collect(&modules, true);
        let read = read.elapsed();
        let at = chrono::Utc::now().timestamp_millis();
        let attribute = Instant::now();
        let sample = engine.sample(
            frame,
            &modules,
            true,
            started.elapsed().as_millis() as u64,
            at,
        );
        let attribute = attribute.elapsed();
        println!(
            "  cost: reading {} processes took {:.1} ms, attributing them {:.1} ms",
            sample.processes.as_ref().map_or(0, Vec::len),
            read.as_secs_f64() * 1000.0,
            attribute.as_secs_f64() * 1000.0
        );
        if let Some(b) = &sample.cpu_breakdown {
            let (usage, readable, finished, other) = (
                f64::from(b.usage),
                f64::from(b.readable),
                f64::from(b.finished),
                f64::from(b.other),
            );
            let from: Vec<String> = b
                .finished_from
                .iter()
                .map(|p| format!("{} {:.0}%", p.name, f64::from(p.cpu) * 100.0))
                .collect();
            println!(
                "window {window}: host busy {:5.1}%  = listed {:5.1}% ({} active) + finished children {:5.1}% [{}] + other {:5.1}%  | explained {:5.1}% of busy",
                usage * 100.0,
                readable * 100.0,
                b.active_count,
                finished * 100.0,
                from.join(", "),
                other * 100.0,
                if usage > 0.0 {
                    ((readable + finished) / usage).min(9.99) * 100.0
                } else {
                    0.0
                },
            );
        } else {
            println!("window {window}: no baseline yet");
        }
        std::thread::sleep(WINDOW);
    }
    stop.store(true, Ordering::Release);
    launcher.join().expect("launcher");
}
#[cfg(not(target_os = "macos"))]
fn main() {}
