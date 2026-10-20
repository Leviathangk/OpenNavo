//! Read-only native collector + offscreen AppKit renderer benchmark. No Tauri client, menu items,
//! windows, privileged helpers or sampled payload files are created. stdout contains aggregates,
//! except the explicitly requested snapshot mode, which prints one complete sample to stdout only.
#[cfg(target_os = "macos")]
fn main() {
    use nix::libc;
    use opennavo_desktop_lib::monitor::{
        collect::{Collector, all_modules},
        menubar::{icon, render_template},
        native::NativeCollector,
        sampler::Sampler,
        types::*,
    };
    use std::{
        collections::BTreeMap,
        time::{Duration, Instant},
    };
    let mode = std::env::args().nth(1).unwrap_or_else(|| "default".into());
    if mode == "observe" {
        observe();
        return;
    }
    if mode == "probe" {
        probe();
        return;
    }
    if mode == "snapshot" {
        snapshot();
        return;
    }
    let seconds = std::env::args()
        .nth(2)
        .and_then(|s| s.parse::<u64>().ok())
        .unwrap_or(600);
    assert!(matches!(
        mode.as_str(),
        "default"
            | "all"
            | "page"
            | "off"
            | "profile"
            | "thermal"
            | "alerts"
            | "page-io"
            | "hour"
            | "charts"
            | "profile-extra"
    ));
    let mtm = objc2::MainThreadMarker::new().expect("benchmark must run on main thread");
    let app = objc2_app_kit::NSApplication::sharedApplication(mtm);
    app.setActivationPolicy(objc2_app_kit::NSApplicationActivationPolicy::Accessory);
    let usage = || {
        let mut r = unsafe { std::mem::zeroed::<libc::rusage_info_v2>() };
        assert_eq!(
            unsafe {
                libc::proc_pid_rusage(
                    std::process::id() as i32,
                    libc::RUSAGE_INFO_V2,
                    (&mut r as *mut libc::rusage_info_v2).cast(),
                )
            },
            0
        );
        // Independently measure CPU with getrusage timeval, not Darwin's Mach proc clocks.
        let mut cpu = unsafe { std::mem::zeroed::<libc::rusage>() };
        assert_eq!(unsafe { libc::getrusage(libc::RUSAGE_SELF, &mut cpu) }, 0);
        let micros = (cpu.ru_utime.tv_sec + cpu.ru_stime.tv_sec) as u64 * 1_000_000
            + (cpu.ru_utime.tv_usec + cpu.ru_stime.tv_usec) as u64;
        let mut child = unsafe { std::mem::zeroed::<libc::rusage>() };
        assert_eq!(
            unsafe { libc::getrusage(libc::RUSAGE_CHILDREN, &mut child) },
            0
        );
        let child_micros = (child.ru_utime.tv_sec + child.ru_stime.tv_sec) as u64 * 1_000_000
            + (child.ru_utime.tv_usec + child.ru_stime.tv_usec) as u64;
        (
            micros * 1000,
            r.ri_phys_footprint,
            child_micros * 1000,
            child.ru_maxrss as u64,
        )
    };
    let baseline = usage().1;
    if mode == "profile-extra" {
        use opennavo_desktop_lib::monitor::extras;
        use std::sync::{Arc, atomic::AtomicBool};
        for kind in ["smart", "ioreport", "nettop"] {
            let mut power = extras::Power::default();
            let active = Arc::new(AtomicBool::new(true));
            let count = if kind == "nettop" {
                3
            } else if kind == "ioreport" {
                5
            } else {
                20
            };
            let mut wall = 0.0;
            let mut cpu = 0.0;
            let mut host = 0.0;
            let mut readable = 0;
            for i in 0..count {
                if kind == "ioreport" && i > 0 {
                    std::thread::sleep(Duration::from_millis(2100));
                }
                let before = usage();
                let start = Instant::now();
                let valid = match kind {
                    "smart" => extras::smart().is_some_and(|v| !v.is_empty()),
                    "ioreport" => {
                        let (c, g) = power.read();
                        c.is_some() || g.is_some()
                    }
                    _ => extras::network(&active).is_some(),
                };
                let after = usage();
                wall += start.elapsed().as_secs_f64() * 1000.0;
                host += (after.0 - before.0) as f64 / 1e6;
                cpu += ((after.0 - before.0) + (after.2 - before.2)) as f64 / 1e6;
                readable += u32::from(valid);
            }
            println!(
                "{}",
                serde_json::json!({"extra":kind,"reads":count,"readable":readable,"wallMsPerRead":wall/f64::from(count),"hostCpuMsPerRead":host/f64::from(count),"cpuIncludingChildrenMsPerRead":cpu/f64::from(count)})
            );
        }
        return;
    }
    if mode == "profile" {
        let mut collector = NativeCollector::new().expect("native system info");
        for module in all_modules() {
            let before = usage().0;
            for _ in 0..20 {
                std::hint::black_box(collector.collect(&[module].into(), false));
            }
            let cost_ms = (usage().0 - before) as f64 / 1e6 / 20.0;
            println!(
                "{}",
                serde_json::json!({"module":module,"cpuMsPerRead":cost_ms})
            );
        }
        return;
    }
    let mut menu = MonitorMenuBar::default();
    if mode == "all" || mode == "charts" {
        menu.disk.enabled = true;
        menu.network.enabled = true;
        menu.battery.enabled = true;
        menu.gpu.enabled = true;
        menu.sensors.enabled = true;
    }
    if matches!(
        mode.as_str(),
        "page" | "off" | "thermal" | "alerts" | "page-io" | "hour"
    ) {
        menu.cpu.enabled = false;
        menu.memory.enabled = false;
    }
    if mode == "thermal" {
        menu.sensors.enabled = true;
    }
    if mode == "charts" {
        menu.cpu.style = MenuBarStyle::Line;
        menu.memory.style = MenuBarStyle::Line;
        menu.gpu.style = MenuBarStyle::Bars;
        menu.network.style = MenuBarStyle::Line;
        menu.sensors.style = MenuBarStyle::Bars;
    }
    let sampler = Sampler::new(
        Box::new(NativeCollector::new().expect("native system info")),
        menu,
        MonitorIntervalMs::TwoSeconds,
    );
    if mode == "alerts" {
        sampler.configure_alerts(true).unwrap();
    }
    if mode == "page-io" {
        sampler
            .subscribe(
                "main",
                MonitorView::Page,
                [
                    MonitorModule::Cpu,
                    MonitorModule::Memory,
                    MonitorModule::Disk,
                    MonitorModule::Network,
                ]
                .into(),
            )
            .unwrap();
    }
    if mode == "hour" {
        sampler
            .subscribe("main", MonitorView::Detail, [MonitorModule::Cpu].into())
            .unwrap();
    }
    if mode == "page" {
        sampler
            .subscribe("main", MonitorView::Page, all_modules())
            .unwrap();
    }
    let mut hour = opennavo_desktop_lib::monitor::trend::Trend::default();
    if mode == "hour" {
        let mut s = sampler.presentation().unwrap().sample;
        let at = chrono::Utc::now().timestamp_millis().div_euclid(5000) * 5000;
        for bucket in 0..720 {
            s.at = at - (719 - bucket) * 5000;
            hour.add(&s, &[MonitorModule::Cpu].into());
        }
    }
    let mut trend_at = None;
    let mut previous = BTreeMap::new();
    let mut images = BTreeMap::new();
    let mut step = |sampler: &Sampler,
                    now: u64,
                    previous: &mut BTreeMap<
        MonitorModule,
        opennavo_desktop_lib::monitor::menubar::TextIcon,
    >,
                    images: &mut BTreeMap<
        MonitorModule,
        objc2::rc::Retained<objc2_app_kit::NSImage>,
    >| {
        if let Some(p) = sampler
            .tick(now, chrono::Utc::now().timestamp_millis())
            .unwrap()
        {
            objc2::rc::autoreleasepool(|_| {
                for module in p
                    .menu
                    .enabled()
                    .intersection(&p.available)
                    .filter(|_| p.menu_due)
                {
                    let text = icon(*module, p.menu.item(*module).style, &p.sample, &p.history);
                    if previous.get(module) != Some(&text) {
                        images.insert(
                            *module,
                            render_template(&text, mtm).expect("template image"),
                        );
                        previous.insert(*module, text);
                    }
                }
                if mode == "hour" && trend_at.is_none_or(|at| now - at >= 5000) {
                    hour.add(&p.sample, &[MonitorModule::Cpu].into());
                    std::hint::black_box(serde_json::to_vec(&hour.points()).unwrap());
                    std::hint::black_box(sampler.trend().unwrap());
                    trend_at = Some(now);
                }
                for _ in &p.windows {
                    std::hint::black_box(serde_json::to_vec(&p.sample).unwrap());
                }
            });
        }
    };
    let origin = Instant::now();
    let warm = Duration::from_secs(10);
    while origin.elapsed() < warm {
        step(
            &sampler,
            origin.elapsed().as_millis() as u64,
            &mut previous,
            &mut images,
        );
        std::thread::sleep(Duration::from_secs(1));
    }
    let before = usage();
    let start = Instant::now();
    let duration = Duration::from_secs(seconds);
    let mut ticks = 0u64;
    let mut peak = before.1;
    while start.elapsed() < duration {
        let deadline = Instant::now()
            + Duration::from_millis(u64::from(
                sampler
                    .plan()
                    .unwrap()
                    .map(|p| p.interval_ms)
                    .unwrap_or(2000),
            ));
        step(
            &sampler,
            origin.elapsed().as_millis() as u64,
            &mut previous,
            &mut images,
        );
        ticks += 1;
        peak = peak.max(usage().1);
        if let Some(wait) = deadline.checked_duration_since(Instant::now()) {
            std::thread::sleep(wait);
        }
    }
    let after = usage();
    let elapsed = start.elapsed().as_secs_f64();
    let host_cpu = (after.0 - before.0) as f64 / 1e9 / elapsed * 100.0;
    let cpu = ((after.0 - before.0) + (after.2 - before.2)) as f64 / 1e9 / elapsed * 100.0;
    println!(
        "{}",
        serde_json::json!({"scenario":mode,"seconds":elapsed,"ticks":ticks,"available":sampler.snapshot().unwrap().available,"cpuPercent":cpu,"hostCpuPercent":host_cpu,"childMaxRssMiB":after.3 as f64/1048576.0,"footprintDeltaMiB":after.1.saturating_sub(baseline) as f64/1048576.0,"peakFootprintDeltaMiB":peak.saturating_sub(baseline) as f64/1048576.0,"scope":"native sampler, shared history/trend, process union/I-O, event serialization, nettop children CPU included, offscreen 1x/2x templates; excludes Tauri WebView/UI and actual status items"})
    );
}
#[cfg(target_os = "macos")]
fn snapshot_finished(
    sample: &opennavo_desktop_lib::monitor::types::MonitorSample,
    elapsed: std::time::Duration,
) -> bool {
    elapsed >= std::time::Duration::from_secs(15)
        || (sample.cpu.power_watts.is_some()
            && sample
                .gpu
                .as_ref()
                .is_none_or(|gpu| gpu.power_watts.is_some())
            && sample.disk.devices.is_some()
            && sample.processes.as_ref().is_some_and(|items| {
                items.iter().any(|p| {
                    p.disk_read_bytes_per_sec.is_some() && p.disk_write_bytes_per_sec.is_some()
                }) && items
                    .iter()
                    .any(|p| p.net_down_bytes_per_sec.is_some() && p.net_up_bytes_per_sec.is_some())
            }))
}

/// Raw, read-only diagnostic names/units, with no AppKit/Tauri or settings initialization.
#[cfg(target_os = "macos")]
fn probe() {
    use opennavo_desktop_lib::monitor::{
        extras, sensors,
        smc::{NativeSmc, Smc},
    };
    let smc = NativeSmc::open().map(|mut smc| {
        let keys = smc.keys();
        keys.into_iter()
            .filter(|k| k.starts_with('T'))
            .filter_map(|k| {
                let v = smc.read(&k)?;
                if !matches!(&v.kind, b"flt " | b"sp78") {
                    return None;
                }
                let value = v.number()?;
                Some(serde_json::json!({"key":k,"celsius":value}))
            })
            .collect::<Vec<_>>()
    });
    println!("{}", serde_json::to_string_pretty(&serde_json::json!({
        "hid": sensors::apple_raw().map(|v| v.into_iter().map(|(product,celsius)| serde_json::json!({"product":product,"celsius":celsius})).collect::<Vec<_>>()),
        "ioreport": extras::power_probe(),
        "ioreportDelta": extras::power_delta_probe(),
        "smcTemperatures": smc,
    })).unwrap());
}

/// One stdout-only capture, using the same collector and sampler as the App but no runtime/UI sinks.
#[cfg(target_os = "macos")]
fn snapshot() {
    use opennavo_desktop_lib::monitor::{
        collect::{Collector, Extras, Frame, Modules, all_modules},
        native::NativeCollector,
        sampler::Sampler,
        types::*,
    };
    use std::{
        sync::{
            Arc, Mutex,
            atomic::{AtomicBool, AtomicU64},
        },
        time::{Duration, Instant},
    };
    // Retain access solely to wait for the native network worker after cancelling the subscription.
    struct Shared(Arc<Mutex<NativeCollector>>);
    impl Collector for Shared {
        fn system(&self) -> SystemInfo {
            self.0.lock().unwrap().system()
        }
        fn collect(&mut self, modules: &Modules, processes: bool) -> Frame {
            self.0.lock().unwrap().collect(modules, processes)
        }
        fn collect_requested(
            &mut self,
            modules: &Modules,
            processes: bool,
            extras: &Extras,
        ) -> Frame {
            self.0
                .lock()
                .unwrap()
                .collect_requested(modules, processes, extras)
        }
        fn network_active_flag(&self) -> Option<Arc<AtomicBool>> {
            self.0.lock().unwrap().network_active_flag()
        }
        fn power_epoch(&self) -> Option<Arc<AtomicU64>> {
            self.0.lock().unwrap().power_epoch()
        }
    }
    let native = Arc::new(Mutex::new(
        NativeCollector::new().expect("native system info"),
    ));
    let mut menu = MonitorMenuBar::default();
    menu.cpu.enabled = false;
    menu.memory.enabled = false;
    let sampler = Sampler::new(
        Box::new(Shared(native.clone())),
        menu,
        MonitorIntervalMs::TwoSeconds,
    );
    sampler
        .subscribe("main", MonitorView::Page, all_modules())
        .unwrap();
    let origin = Instant::now();
    let mut sample = sampler.presentation().unwrap().sample;
    while origin.elapsed() < Duration::from_secs(15) {
        if let Some(publication) = sampler
            .tick(
                origin.elapsed().as_millis() as u64,
                chrono::Utc::now().timestamp_millis(),
            )
            .unwrap()
        {
            sample = publication.sample;
        }
        if snapshot_finished(&sample, origin.elapsed()) {
            break;
        }
        std::thread::sleep(
            Duration::from_secs(1).min(Duration::from_secs(15).saturating_sub(origin.elapsed())),
        );
    }
    let snapshot = sampler.snapshot().unwrap();
    sampler.unsubscribe("main", MonitorView::Page).unwrap();
    drop(sampler);
    native
        .lock()
        .unwrap()
        .finish_network()
        .expect("nettop worker must finish and reap its child");
    println!(
        "{}",
        serde_json::to_string_pretty(&serde_json::json!({
            "system": snapshot.system,
            "available": snapshot.available,
            "sample": sample,
        }))
        .unwrap()
    );
}

/// Coordinator-run complete-client measurement. Only explicitly provided current-user PIDs are read;
/// include the Tauri host and its dedicated WebContent process(es) for page/UI budget verification.
#[cfg(target_os = "macos")]
fn observe() {
    use nix::libc;
    use std::time::{Duration, Instant};
    let input = std::env::args()
        .nth(2)
        .expect("observe <pid[,pid...]> [seconds]");
    let pids: std::collections::BTreeSet<i32> = input
        .split(',')
        .map(|p| p.parse().expect("numeric PID"))
        .collect();
    assert!(!pids.is_empty());
    let seconds = std::env::args()
        .nth(3)
        .map(|s| s.parse::<u64>().expect("positive seconds"))
        .unwrap_or(600);
    assert!(seconds > 0);
    #[repr(C)]
    struct Timebase {
        numer: u32,
        denom: u32,
    }
    unsafe extern "C" {
        fn mach_timebase_info(info: *mut Timebase) -> i32;
    }
    let mut timebase = Timebase { numer: 0, denom: 0 };
    assert_eq!(unsafe { mach_timebase_info(&mut timebase) }, 0);
    for &pid in &pids {
        assert!(pid > 1 && pid as u32 != std::process::id());
        let mut bsd = unsafe { std::mem::zeroed::<libc::proc_bsdinfo>() };
        assert_eq!(
            unsafe {
                libc::proc_pidinfo(
                    pid,
                    libc::PROC_PIDTBSDINFO,
                    0,
                    (&mut bsd as *mut libc::proc_bsdinfo).cast(),
                    std::mem::size_of_val(&bsd) as i32,
                )
            },
            std::mem::size_of_val(&bsd) as i32
        );
        assert_eq!(
            bsd.pbi_uid,
            unsafe { libc::geteuid() },
            "only current-user processes may be observed"
        );
    }
    let usage = || {
        let mut cpu = 0u64;
        let mut footprint = 0u64;
        for &pid in &pids {
            let mut r = unsafe { std::mem::zeroed::<libc::rusage_info_v2>() };
            assert_eq!(
                unsafe {
                    libc::proc_pid_rusage(
                        pid,
                        libc::RUSAGE_INFO_V2,
                        (&mut r as *mut libc::rusage_info_v2).cast(),
                    )
                },
                0,
                "observed process exited or is unavailable; discard this run"
            );
            cpu = cpu.saturating_add(opennavo_desktop_lib::monitor::procs::cpu_ticks_to_ns(
                r.ri_user_time
                    .saturating_add(r.ri_system_time)
                    .saturating_add(r.ri_child_user_time)
                    .saturating_add(r.ri_child_system_time),
                timebase.numer,
                timebase.denom,
            ));
            footprint = footprint.saturating_add(r.ri_phys_footprint);
        }
        (cpu, footprint)
    };
    let baseline = usage().1;
    println!(
        "{}",
        serde_json::json!({"observerReady":true,"warmupSeconds":10,"processCount":pids.len(),"baselineFootprintMiB":baseline as f64/1048576.0})
    );
    // Enable the desired scenario during this warmup; baseline was taken with monitoring disabled.
    std::thread::sleep(Duration::from_secs(10));
    let before = usage();
    let start = Instant::now();
    let mut peak = before.1;
    while start.elapsed() < Duration::from_secs(seconds) {
        std::thread::sleep(Duration::from_secs(1));
        peak = peak.max(usage().1);
    }
    let after = usage();
    let elapsed = start.elapsed().as_secs_f64();
    println!(
        "{}",
        serde_json::json!({"scope":"explicit current-user client processes plus terminated children CPU; no argv or environment read","processCount":pids.len(),"seconds":elapsed,"cpuPercent":after.0.saturating_sub(before.0) as f64/1e9/elapsed*100.0,"footprintDeltaMiB":after.1.saturating_sub(baseline) as f64/1048576.0,"peakFootprintDeltaMiB":peak.saturating_sub(baseline) as f64/1048576.0})
    );
}
#[cfg(not(target_os = "macos"))]
fn main() {
    eprintln!("monitor_perf requires macOS");
}

#[cfg(all(test, target_os = "macos"))]
mod snapshot_tests {
    use super::snapshot_finished;
    use opennavo_desktop_lib::monitor::{engine::empty_sample, types::*};
    use std::time::Duration;

    fn sample() -> MonitorSample {
        let mut sample = empty_sample(&SystemInfo {
            model_name: "Fixture".into(),
            model_id: "Fixture1,1".into(),
            chip: "Fixture".into(),
            performance_cores: None,
            efficiency_cores: None,
            logical_cores: 1,
            gpu_cores: None,
            memory_bytes: 1024,
            os_version: "Fixture".into(),
            booted_at: 0,
        });
        sample.cpu.power_watts = Some(MonitorNumber::default());
        sample.disk.devices = Some(vec![]);
        sample.processes = Some(vec![ProcessInfo {
            pid: 42,
            name: "Fixture App".into(),
            bundle_id: None,
            icon_path: None,
            cpu: MonitorNumber::default(),
            memory_bytes: 100,
            disk_read_bytes_per_sec: Some(MonitorNumber::default()),
            disk_write_bytes_per_sec: Some(MonitorNumber::default()),
            net_down_bytes_per_sec: Some(MonitorNumber::default()),
            net_up_bytes_per_sec: Some(MonitorNumber::default()),
            kind: ProcessKind::Background,
            quittable: false,
            count: 1,
            is_self: false,
        }]);
        sample
    }
    #[test]
    fn snapshot_waits_for_both_io_deltas_power_and_smart_but_accepts_idle_zeroes() {
        let ready = sample();
        assert!(snapshot_finished(&ready, Duration::ZERO));
        for missing in 0..6 {
            let mut pending = ready.clone();
            match missing {
                0 => pending.cpu.power_watts = None,
                1 => pending.disk.devices = None,
                2 => pending.processes.as_mut().unwrap()[0].disk_read_bytes_per_sec = None,
                3 => pending.processes.as_mut().unwrap()[0].disk_write_bytes_per_sec = None,
                4 => pending.processes.as_mut().unwrap()[0].net_down_bytes_per_sec = None,
                _ => pending.processes.as_mut().unwrap()[0].net_up_bytes_per_sec = None,
            }
            assert!(!snapshot_finished(&pending, Duration::from_millis(14_999)));
            assert!(snapshot_finished(&pending, Duration::from_secs(15)));
        }
    }
    #[test]
    fn snapshot_unreadable_gpu_times_out_with_nullable_fields_and_keeps_process_names() {
        let mut pending = sample();
        pending.gpu = Some(GpuSample {
            usage: MonitorNumber::default(),
            power_watts: None,
        });
        assert!(!snapshot_finished(&pending, Duration::from_secs(2)));
        assert!(snapshot_finished(&pending, Duration::from_secs(15)));
        let json = serde_json::to_value(&pending).unwrap();
        assert!(json["gpu"]["powerWatts"].is_null());
        assert_eq!(json["processes"][0]["name"], "Fixture App");
        assert!(json.get("health").is_some());
        assert!(json["disk"].get("devices").is_some());
    }
}
