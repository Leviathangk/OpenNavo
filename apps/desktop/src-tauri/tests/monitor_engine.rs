use opennavo_desktop_lib::monitor::{
    collect::*,
    engine::{Engine, reorder_cores, top_processes},
    popover::{Anchor, Rect, main_route, position},
    procs::{AppIdentity, may_quit},
    sampler::{PauseReason, Sampler, validate_view},
    types::*,
};
use std::sync::{
    Arc, Mutex,
    atomic::{AtomicUsize, Ordering},
};
fn system() -> SystemInfo {
    SystemInfo {
        model_name: "Fixture".into(),
        model_id: "Fixture1,1".into(),
        chip: "Apple Fixture".into(),
        performance_cores: Some(2),
        efficiency_cores: Some(4),
        logical_cores: 6,
        gpu_cores: None,
        memory_bytes: 100_000_000_000,
        os_version: "26.0".into(),
        booted_at: 0,
    }
}
fn memory() -> MemorySample {
    MemorySample {
        total_bytes: 100_000_000_000,
        used_bytes: 20_000_000_000,
        app_bytes: 10_000_000_000,
        wired_bytes: 5_000_000_000,
        compressed_bytes: 5_000_000_000,
        cached_bytes: 20_000_000_000,
        swap_used_bytes: 0,
        pressure: MemoryPressure::Normal,
    }
}
fn cpu(step: u32) -> RawCpu {
    RawCpu {
        ticks: (0..6)
            .map(|i| [i * 10 * step, 0, (100 - i * 10) * step, 0])
            .collect(),
        load: [1.0, 2.0, 3.0],
    }
}
fn frame(step: u32) -> Frame {
    Frame {
        cpu: Some(cpu(step)),
        memory: Some(memory()),
        disk: Some(RawDisk {
            volumes: vec![VolumeInfo {
                name: "FixtureDisk".into(),
                mount_point: "/".into(),
                total_bytes: 1_000_000_000_000,
                free_bytes: 500_000_000_000,
                internal: true,
            }],
            counters: Some((
                "device1".into(),
                u64::from(step) * 1000,
                u64::from(step) * 2000,
            )),
        }),
        network: Some(RawNetwork {
            interface: Some("en0".into()),
            kind: Some(NetworkKind::Wifi),
            local_ip: Some("192.0.2.1".into()),
            down: u64::from(step) * 10000,
            up: u64::from(step) * 20000,
        }),
        battery: None,
        processes: None,
        ..Frame::default()
    }
}
fn off() -> MonitorMenuBar {
    let mut menu = MonitorMenuBar::default();
    menu.cpu.enabled = false;
    menu.memory.enabled = false;
    menu
}
type Calls = Arc<Mutex<Vec<(Modules, bool)>>>;
struct Fake {
    calls: Calls,
    step: u32,
}
impl Collector for Fake {
    fn system(&self) -> SystemInfo {
        system()
    }
    fn collect(&mut self, modules: &Modules, processes: bool) -> Frame {
        self.calls
            .lock()
            .unwrap()
            .push((modules.clone(), processes));
        self.step += 1;
        let mut frame = frame(self.step);
        if !modules.contains(&MonitorModule::Cpu) {
            frame.cpu = None;
        }
        if !modules.contains(&MonitorModule::Memory) {
            frame.memory = None;
        }
        if !modules.contains(&MonitorModule::Disk) {
            frame.disk = None;
        }
        if !modules.contains(&MonitorModule::Network) {
            frame.network = None;
        }
        if processes {
            frame.processes = Some(vec![]);
        }
        frame
    }
}
fn sampler(menu: MonitorMenuBar) -> (Arc<Sampler>, Calls) {
    let calls = Arc::new(Mutex::new(vec![]));
    let sampler = Sampler::new(
        Box::new(Fake {
            calls: calls.clone(),
            step: 0,
        }),
        menu,
        MonitorIntervalMs::TwoSeconds,
    );
    (sampler, calls)
}
#[test]
fn apple_performance_cores_are_first_and_intel_is_not_reordered() {
    let mut ids = vec![0, 1, 2, 3, 4, 5];
    reorder_cores(&mut ids, &system());
    assert_eq!(ids, [4, 5, 0, 1, 2, 3]);
    let mut missing_efficiency = system();
    missing_efficiency.efficiency_cores = None;
    let mut ids = vec![0, 1, 2, 3, 4, 5];
    reorder_cores(&mut ids, &missing_efficiency);
    assert_eq!(ids, [4, 5, 0, 1, 2, 3]);
    let mut intel = system();
    intel.performance_cores = None;
    intel.efficiency_cores = None;
    let mut ids = vec![0, 1, 2, 3, 4, 5];
    reorder_cores(&mut ids, &intel);
    assert_eq!(ids, [0, 1, 2, 3, 4, 5]);
    let mut engine = Engine::new(system(), frame(0));
    let modules = m1_modules();
    engine.sample(frame(1), &modules, false, 1000, 1000);
    let s = engine.sample(frame(2), &modules, false, 2000, 2000);
    assert_eq!(
        s.cpu
            .cores
            .iter()
            .copied()
            .map(f64::from)
            .collect::<Vec<_>>(),
        vec![0.4, 0.5, 0.0, 0.1, 0.2, 0.3]
    );
    assert_eq!(f64::from(s.cpu.usage), 0.25);
}
#[test]
fn merge_replacement_intervals_idempotent_unsubscribe_and_empty_stop() {
    let (sampler, calls) = sampler(MonitorMenuBar::default());
    let base = sampler.plan().unwrap().unwrap();
    assert_eq!(base.interval_ms, 2000);
    assert!(!base.processes);
    sampler
        .subscribe(
            "main",
            MonitorView::Page,
            [MonitorModule::Disk].into_iter().collect(),
        )
        .unwrap();
    sampler
        .subscribe(
            "tray-monitor",
            MonitorView::Popover,
            [MonitorModule::Network].into_iter().collect(),
        )
        .unwrap();
    let plan = sampler.plan().unwrap().unwrap();
    assert_eq!(plan.interval_ms, 1000);
    assert!(plan.processes);
    assert_eq!(plan.modules.len(), 4);
    sampler
        .subscribe(
            "main",
            MonitorView::Page,
            [MonitorModule::Cpu].into_iter().collect(),
        )
        .unwrap();
    assert!(
        !sampler
            .plan()
            .unwrap()
            .unwrap()
            .modules
            .contains(&MonitorModule::Disk)
    );
    sampler.remove_window("tray-monitor");
    assert!(
        !sampler
            .plan()
            .unwrap()
            .unwrap()
            .modules
            .contains(&MonitorModule::Network)
    );
    sampler.unsubscribe("main", MonitorView::Page).unwrap();
    sampler.unsubscribe("main", MonitorView::Page).unwrap();
    sampler
        .configure(off(), MonitorIntervalMs::FiveSeconds)
        .unwrap();
    assert!(sampler.plan().unwrap().is_none());
    let count = calls.lock().unwrap().len();
    assert!(sampler.tick(5000, 5000).unwrap().is_none());
    assert_eq!(calls.lock().unwrap().len(), count);
    assert!(
        sampler
            .subscribe("tray-monitor", MonitorView::Page, m1_modules())
            .is_err()
    );
    assert!(validate_view("main", MonitorView::Popover).is_err());
}
#[test]
fn processes_are_visible_only_two_second_throttled_and_cleared_on_hide() {
    let (sampler, calls) = sampler(off());
    sampler
        .subscribe(
            "tray-monitor",
            MonitorView::Popover,
            [MonitorModule::Cpu].into_iter().collect(),
        )
        .unwrap();
    for now in [0, 1000, 2000, 3000] {
        sampler.tick(now, now as i64).unwrap();
    }
    let calls = calls.lock().unwrap();
    assert_eq!(
        calls.iter().skip(1).map(|(_, p)| *p).collect::<Vec<_>>(),
        [true, false, true, false]
    );
    drop(calls);
    sampler.remove_window("tray-monitor");
    assert!(sampler.plan().unwrap().is_none());
    sampler
        .configure(MonitorMenuBar::default(), MonitorIntervalMs::TwoSeconds)
        .unwrap();
    let p = sampler.tick(4000, 4000).unwrap().unwrap();
    assert!(p.sample.processes.is_none());
}
#[test]
fn shared_history_is_exactly_sixty_samples_at_two_second_intervals() {
    let (sampler, _) = sampler(MonitorMenuBar::default());
    for i in 0..75 {
        sampler.tick(i * 2000, i as i64 * 2000).unwrap();
    }
    let s = sampler
        .subscribe(
            "main",
            MonitorView::Page,
            [MonitorModule::Cpu].into_iter().collect(),
        )
        .unwrap();
    assert_eq!(s.history.len(), 60);
    assert_eq!(s.history[0].at, 30_000);
    assert_eq!(s.history[59].at, 148_000);
    assert_eq!(
        s.available,
        [
            MonitorModule::Cpu,
            MonitorModule::Memory,
            MonitorModule::Disk,
            MonitorModule::Network
        ]
    );
    sampler.unsubscribe("main", MonitorView::Page).unwrap();
    assert_eq!(sampler.snapshot().unwrap().history.len(), 60);
}
#[test]
fn overlapping_pause_reasons_prevent_reads_and_wake_resets_deltas() {
    let (sampler, calls) = sampler(MonitorMenuBar::default());
    sampler.tick(0, 0).unwrap();
    sampler.tick(2000, 2000).unwrap();
    sampler.pause(PauseReason::Sleep, true);
    sampler.pause(PauseReason::Lock, true);
    let reads = calls.lock().unwrap().len();
    sampler.pause(PauseReason::Sleep, false);
    assert!(sampler.tick(100_000, 100_000).unwrap().is_none());
    assert_eq!(calls.lock().unwrap().len(), reads);
    sampler.pause(PauseReason::Lock, false);
    let sample = sampler.tick(102_000, 102_000).unwrap().unwrap().sample;
    assert_eq!(f64::from(sample.cpu.usage), 0.0);
}
fn process(pid: u32, cpu: f64, memory: u64) -> ProcessInfo {
    ProcessInfo {
        pid,
        name: format!("Fixture{pid}"),
        bundle_id: Some(format!("org.example.fixture{pid}")),
        icon_path: None,
        cpu: ratio(cpu),
        memory_bytes: memory,
        disk_read_bytes_per_sec: None,
        disk_write_bytes_per_sec: None,
        net_down_bytes_per_sec: None,
        net_up_bytes_per_sec: None,
        kind: ProcessKind::App,
        quittable: true,
    }
}
#[test]
fn cpu_and_memory_top_twenty_union_is_deduplicated_and_keeps_quiet_memory_hogs() {
    let mut items: Vec<_> = (1..=50)
        .map(|pid| {
            process(
                pid,
                if pid <= 20 { 0.5 } else { 0.0 },
                if pid > 30 { 10_000 } else { 1 },
            )
        })
        .collect();
    items[0].memory_bytes = 100_000;
    let top = top_processes(items);
    assert_eq!(top.len(), 39);
    let ids: std::collections::BTreeSet<_> = top.iter().map(|p| p.pid).collect();
    assert_eq!(ids.len(), 39);
    assert!(ids.contains(&49));
    assert!(ids.contains(&1));
    assert!(!ids.contains(&30));
}
#[test]
fn io_counter_reset_interface_change_and_stop_never_spike() {
    let modules = m1_modules();
    let mut engine = Engine::new(system(), frame(0));
    engine.sample(frame(10), &modules, false, 1000, 1000);
    let s = engine.sample(frame(11), &modules, false, 2000, 2000);
    assert_eq!(f64::from(s.network.down_bytes_per_sec), 10000.0);
    assert_eq!(f64::from(s.disk.read_bytes_per_sec), 1000.0);
    let s = engine.sample(frame(1), &modules, false, 3000, 3000);
    assert_eq!(f64::from(s.network.down_bytes_per_sec), 0.0);
    let mut vpn = frame(30);
    vpn.network.as_mut().unwrap().interface = Some("utun7".into());
    vpn.network.as_mut().unwrap().kind = Some(NetworkKind::Vpn);
    let s = engine.sample(vpn, &modules, false, 4000, 4000);
    assert_eq!(f64::from(s.network.down_bytes_per_sec), 0.0);
    engine.reset();
    let s = engine.sample(frame(100), &modules, false, 120000, 120000);
    assert_eq!(f64::from(s.disk.read_bytes_per_sec), 0.0);
}
#[test]
fn availability_tracks_readable_modules_and_failures_are_isolated() {
    let modules = m1_modules();
    let mut engine = Engine::new(system(), frame(0));
    assert!(!engine.available.contains(&MonitorModule::Battery));
    let mut missing = frame(1);
    missing.cpu = None;
    engine.sample(missing, &modules, false, 1000, 1000);
    assert!(!engine.available.contains(&MonitorModule::Cpu));
    assert!(engine.available.contains(&MonitorModule::Memory));
    engine.sample(frame(2), &modules, false, 2000, 2000);
    assert!(engine.available.contains(&MonitorModule::Cpu));
    assert!(!engine.available.contains(&MonitorModule::Gpu));
}
#[test]
fn health_uses_sustained_monotonic_cpu_threshold_and_exact_disk_boundaries() {
    let mut tracker = opennavo_desktop_lib::monitor::health::HealthTracker::default();
    let mut sample = opennavo_desktop_lib::monitor::engine::empty_sample(&system());
    sample.cpu.usage = ratio(0.9);
    let modules = [MonitorModule::Cpu].into_iter().collect();
    assert_eq!(
        tracker.evaluate(&sample, &modules, 0, 0).level,
        HealthLevel::Good
    );
    assert_eq!(
        tracker.evaluate(&sample, &modules, 179999, 0).level,
        HealthLevel::Good
    );
    assert_eq!(
        tracker.evaluate(&sample, &modules, 180000, 0).level,
        HealthLevel::Attention
    );
    sample.cpu.usage = ratio(0.85);
    assert_eq!(
        tracker.evaluate(&sample, &modules, 181000, 0).level,
        HealthLevel::Good
    );
    sample.disk = frame(0)
        .disk
        .map(|d| DiskSample {
            devices: None,
            volumes: d.volumes,
            read_bytes_per_sec: number(0.0),
            write_bytes_per_sec: number(0.0),
        })
        .unwrap();
    let modules = [MonitorModule::Disk].into_iter().collect();
    sample.disk.volumes[0].free_bytes = 5_000_000_000;
    assert_eq!(
        tracker.evaluate(&sample, &modules, 0, 0).level,
        HealthLevel::Attention
    );
    sample.disk.volumes[0].free_bytes = 4_999_999_999;
    assert_eq!(
        tracker.evaluate(&sample, &modules, 0, 0).level,
        HealthLevel::Critical
    );
    tracker.reset();
    sample.at = 31 * 86_400_000;
    let health = tracker.evaluate(&sample, &Modules::new(), 0, 0);
    assert_eq!(health.level, HealthLevel::Good);
    assert_eq!(health.issues[0].key, HealthIssueKey::Uptime);
}
#[test]
fn process_cpu_is_whole_machine_and_pid_reuse_resets_baseline() {
    let mut engine = Engine::new(system(), frame(0));
    let modules = m1_modules();
    let raw = |start, total| RawProcess {
        info: process(7, 0.0, 1000),
        started: start,
        cpu_ns: total,
        disk_bytes: None,
        net_bytes: None,
        net_at: None,
    };
    let mut first = frame(1);
    first.processes = Some(vec![raw(1, 0)]);
    engine.sample(first, &modules, true, 0, 0);
    let mut next = frame(2);
    next.processes = Some(vec![raw(1, 2_000_000_000)]);
    let s = engine.sample(next, &modules, true, 2000, 2000);
    assert_eq!(f64::from(s.processes.unwrap()[0].cpu), 1.0 / 6.0);
    let mut reused = frame(3);
    reused.processes = Some(vec![raw(2, 100_000_000_000)]);
    let s = engine.sample(reused, &modules, true, 4000, 4000);
    assert_eq!(f64::from(s.processes.unwrap()[0].cpu), 0.0);
}
#[test]
fn in_flight_collection_is_discarded_if_hidden_or_suspended() {
    struct Blocking {
        calls: Arc<AtomicUsize>,
        entered: Arc<std::sync::Barrier>,
        release: Arc<std::sync::Barrier>,
    }
    impl Collector for Blocking {
        fn system(&self) -> SystemInfo {
            system()
        }
        fn collect(&mut self, _: &Modules, _: bool) -> Frame {
            if self.calls.fetch_add(1, Ordering::SeqCst) > 0 {
                self.entered.wait();
                self.release.wait();
            }
            frame(1)
        }
    }
    let entered = Arc::new(std::sync::Barrier::new(2));
    let release = Arc::new(std::sync::Barrier::new(2));
    let sampler = Sampler::new(
        Box::new(Blocking {
            calls: Arc::new(AtomicUsize::new(0)),
            entered: entered.clone(),
            release: release.clone(),
        }),
        MonitorMenuBar::default(),
        MonitorIntervalMs::TwoSeconds,
    );
    let worker = sampler.clone();
    let thread = std::thread::spawn(move || worker.tick(0, 0));
    entered.wait();
    sampler.pause(PauseReason::Sleep, true);
    release.wait();
    assert!(thread.join().unwrap().unwrap().is_none());
    assert!(sampler.snapshot().unwrap().history.is_empty());
}
#[test]
fn gui_quit_policy_rejects_self_other_users_system_and_background_apps() {
    let app = AppIdentity {
        pid: 123,
        own_user: true,
        regular: true,
        bundle_id: Some("org.example.fixture".into()),
        path: "/Applications/Fixture.app".into(),
    };
    assert!(may_quit(&app, 999));
    assert!(!may_quit(&app, 123));
    assert!(!may_quit(
        &AppIdentity {
            own_user: false,
            ..app.clone()
        },
        999
    ));
    assert!(!may_quit(
        &AppIdentity {
            regular: false,
            ..app.clone()
        },
        999
    ));
    assert!(!may_quit(
        &AppIdentity {
            path: "/System/Library/CoreServices/Fixture.app".into(),
            ..app.clone()
        },
        999
    ));
    assert!(!may_quit(
        &AppIdentity {
            bundle_id: Some("com.apple.finder".into()),
            ..app.clone()
        },
        999
    ));
    let (sampler, _) = sampler(off());
    assert!(sampler.quit_target(1).is_err());
}
#[test]
fn appkit_logical_position_constrains_on_clicked_screen_with_negative_origin() {
    let anchor = Anchor {
        item: Rect {
            x: -10.0,
            y: 1178.0,
            width: 48.0,
            height: 22.0,
        },
        visible: Rect {
            x: -1920.0,
            y: 0.0,
            width: 1920.0,
            height: 1178.0,
        },
    };
    let (x, y) = position(anchor, 360.0, 400.0);
    assert_eq!(x, -360.0);
    assert_eq!(y, 774.0);
    let (x, y) = position(anchor, 3000.0, 2000.0);
    assert_eq!((x, y), (-1920.0, 0.0));
    assert_eq!(
        main_route(MonitorMainTarget::Detail, Some(MonitorModule::Cpu)),
        "/monitor?detail=cpu"
    );
    assert_eq!(
        main_route(MonitorMainTarget::Settings, None),
        "/settings/monitor"
    );
}
#[test]
fn text_icon_width_is_fixed_and_unavailable_modules_are_never_enabled_by_capability() {
    use opennavo_desktop_lib::monitor::menubar::icon_text;
    let mut s = opennavo_desktop_lib::monitor::engine::empty_sample(&system());
    let before = icon_text(MonitorModule::Cpu, MenuBarStyle::Text, &s);
    s.cpu.usage = ratio(1.0);
    let after = icon_text(MonitorModule::Cpu, MenuBarStyle::Text, &s);
    assert_eq!(before.lines, ["CPU", "0%"]);
    assert_eq!(after.lines, ["CPU", "100%"]);
    assert_eq!(before.widest, after.widest);
    assert_eq!(after.widest, "100%");
    assert_eq!(
        icon_text(MonitorModule::Cpu, MenuBarStyle::Value, &s)
            .lines
            .len(),
        1
    );
    let (sampler, _) = sampler(off());
    sampler
        .subscribe(
            "main",
            MonitorView::Page,
            [MonitorModule::Gpu, MonitorModule::Fans]
                .into_iter()
                .collect(),
        )
        .unwrap();
    assert!(sampler.plan().unwrap().is_none());
}

#[test]
fn darwin_cpu_clocks_convert_mach_units_without_overflow() {
    use opennavo_desktop_lib::monitor::procs::cpu_ticks_to_ns;
    assert_eq!(cpu_ticks_to_ns(3_000_000, 125, 3), 125_000_000);
    assert_eq!(cpu_ticks_to_ns(123, 1, 1), 123);
    assert_eq!(cpu_ticks_to_ns(u64::MAX, 125, 3), u64::MAX);
    assert_eq!(cpu_ticks_to_ns(123, 1, 0), 0);
}

#[test]
fn visible_one_second_samples_preserve_configured_menu_refresh_interval() {
    let (sampler, _) = sampler(MonitorMenuBar::default());
    sampler
        .configure(MonitorMenuBar::default(), MonitorIntervalMs::FiveSeconds)
        .unwrap();
    sampler
        .subscribe("main", MonitorView::Detail, [MonitorModule::Cpu].into())
        .unwrap();
    assert_eq!(sampler.plan().unwrap().unwrap().interval_ms, 1000);
    for second in 0..=6 {
        let publication = sampler
            .tick(second * 1000, second as i64 * 1000)
            .unwrap()
            .unwrap();
        assert_eq!(publication.menu_due, second == 0 || second == 5);
        assert_eq!(publication.windows, vec!["main"]);
    }
}

#[test]
fn visible_subscribe_refreshes_stale_first_response_with_processes() {
    let calls = Arc::new(Mutex::new(vec![]));
    let sampler = Sampler::new(
        Box::new(Fake {
            calls: calls.clone(),
            step: 1,
        }),
        off(),
        MonitorIntervalMs::TwoSeconds,
    );
    sampler
        .subscribe_fresh_at(
            "main",
            MonitorView::Page,
            [MonitorModule::Cpu].into(),
            1000,
            1000,
        )
        .unwrap();
    assert!(calls.lock().unwrap().last().unwrap().1);
    let count = calls.lock().unwrap().len();
    let response = sampler
        .subscribe_fresh_at(
            "main",
            MonitorView::Detail,
            [MonitorModule::Cpu].into(),
            1300,
            1300,
        )
        .unwrap();
    assert_eq!(response.history.last().unwrap().at, 1000);
    assert!(response.history.last().unwrap().processes.is_some());
    assert_eq!(calls.lock().unwrap().len(), count);
    let response = sampler
        .subscribe_fresh_at(
            "main",
            MonitorView::Detail,
            [MonitorModule::Cpu].into(),
            1600,
            1600,
        )
        .unwrap();
    assert_eq!(response.history.last().unwrap().at, 1600);
    assert!(calls.lock().unwrap().last().unwrap().1);
}
#[test]
fn thermal_reads_are_display_only_and_at_most_once_per_five_seconds() {
    struct ThermalFake(Calls);
    impl Collector for ThermalFake {
        fn system(&self) -> SystemInfo {
            system()
        }
        fn collect(&mut self, modules: &Modules, processes: bool) -> Frame {
            self.0.lock().unwrap().push((modules.clone(), processes));
            let mut f = frame(1);
            if modules.contains(&MonitorModule::Sensors) {
                f.sensors = Some(vec![SensorReading {
                    key: "cpu".into(),
                    name: "CPU".into(),
                    group: SensorGroup::Cpu,
                    celsius: number(96.0),
                }]);
            }
            if modules.contains(&MonitorModule::Fans) {
                f.fans = Some(vec![FanState {
                    id: 0,
                    name: "Fan 1".into(),
                    rpm: 2000,
                    min_rpm: 1200,
                    max_rpm: 6000,
                    target_rpm: None,
                    mode: FanMode::Auto,
                }]);
            }
            f
        }
    }
    let calls = Arc::new(Mutex::new(vec![]));
    let sampler = Sampler::new(
        Box::new(ThermalFake(calls.clone())),
        MonitorMenuBar::default(),
        MonitorIntervalMs::TwoSeconds,
    );
    calls.lock().unwrap().clear(); // One-off capability probe is excluded from periodic sampling.
    sampler.tick(0, 0).unwrap();
    sampler.tick(2000, 2000).unwrap();
    assert!(calls.lock().unwrap().iter().all(|(m,_)|!m.contains(&MonitorModule::Sensors)&&!m.contains(&MonitorModule::Fans)));
    sampler
        .subscribe(
            "tray-monitor",
            MonitorView::Popover,
            [MonitorModule::Sensors, MonitorModule::Fans].into(),
        )
        .unwrap();
    for now in 2000..=12000 {
        if now % 1000 == 0 {
            sampler.tick(now, now as i64).unwrap();
        }
    }
    let reads = calls
        .lock()
        .unwrap()
        .iter()
        .filter(|(m, _)| m.contains(&MonitorModule::Sensors))
        .count();
    assert_eq!(reads, 3);
    let sample = sampler.snapshot().unwrap().history.pop().unwrap();
    assert!(sample.sensors.is_some());
    assert!(sample.fans.is_some());
    let issue = sample
        .health
        .issues
        .iter()
        .find(|i| i.key == HealthIssueKey::Temperature)
        .unwrap();
    assert_eq!(f64::from(issue.value), 96.0);
    sampler.remove_window("tray-monitor");
    let before = reads;
    sampler.tick(20000, 20000).unwrap();
    assert_eq!(
        calls
            .lock()
            .unwrap()
            .iter()
            .filter(|(m, _)| m.contains(&MonitorModule::Sensors))
            .count(),
        before
    );
}

#[test]
fn menu_bar_network_rates_stay_within_three_digits() {
    use opennavo_desktop_lib::monitor::menubar::icon_text;
    let mut sample = opennavo_desktop_lib::monitor::engine::empty_sample(&system());
    for (bytes, shown) in [
        (0.0, "↓0B"),
        (999.4, "↓999B"),
        (999.5, "↓1K"),
        (999_499.0, "↓999K"),
        (999_500.0, "↓1.0M"),
        (12_345_678.0, "↓12.3M"),
        (99_949_999.0, "↓99.9M"),
        (99_950_000.0, "↓100M"),
        (999_499_999.0, "↓999M"),
        (999_500_000.0, "↓1.0G"),
    ] {
        sample.network.down_bytes_per_sec = number(bytes);
        let icon = icon_text(MonitorModule::Network, MenuBarStyle::Value, &sample);
        assert_eq!(icon.lines, [shown]);
        assert_eq!(icon.widest, "↓99.9M");
    }
}

#[test]
fn temperature_menu_item_is_the_bare_hottest_reading() {
    use opennavo_desktop_lib::monitor::menubar::icon_text;
    let mut sample = opennavo_desktop_lib::monitor::engine::empty_sample(&system());
    sample.sensors = Some(vec![
        SensorReading {
            key: "a".into(),
            name: "CPU".into(),
            group: SensorGroup::Cpu,
            celsius: number(81.6),
        },
        SensorReading {
            key: "b".into(),
            name: "SSD".into(),
            group: SensorGroup::Ssd,
            celsius: number(44.0),
        },
    ]);
    // Like Stats, no "TEMP" label and no unit letter, in either text style.
    for style in [MenuBarStyle::Text, MenuBarStyle::Value] {
        let icon = icon_text(MonitorModule::Sensors, style, &sample);
        assert_eq!(icon.lines, ["82°"]);
        assert_eq!(icon.widest, "44°");
    }
    sample.sensors = None;
    assert_eq!(
        icon_text(MonitorModule::Sensors, MenuBarStyle::Text, &sample).lines,
        ["—"]
    );
}

#[test]
fn fan_menu_uses_mean_rpm_and_chart_styles_fall_back_to_text_in_m3_0() {
    use opennavo_desktop_lib::monitor::menubar::icon_text;
    let mut sample = opennavo_desktop_lib::monitor::engine::empty_sample(&system());
    assert!(sample.cpu.power_watts.is_none());
    assert!(sample.disk.devices.is_none());
    sample.fans = Some(vec![
        FanState {
            id: 0,
            name: "Left".into(),
            rpm: 2000,
            min_rpm: 1000,
            max_rpm: 6000,
            target_rpm: None,
            mode: FanMode::Auto,
        },
        FanState {
            id: 1,
            name: "Right".into(),
            rpm: 4000,
            min_rpm: 1000,
            max_rpm: 6000,
            target_rpm: None,
            mode: FanMode::Auto,
        },
    ]);
    assert_eq!(
        icon_text(MonitorModule::Fans, MenuBarStyle::Value, &sample).lines,
        ["3000"]
    );
    assert_eq!(
        icon_text(MonitorModule::Fans, MenuBarStyle::Text, &sample).lines,
        ["RPM", "3000"]
    );
    for module in [MonitorModule::Disk, MonitorModule::Battery] {
        for style in [MenuBarStyle::Line, MenuBarStyle::Bars] {
            assert_eq!(
                icon_text(module, style, &sample),
                icon_text(module, MenuBarStyle::Text, &sample)
            );
        }
    }
    sample.fans = Some(vec![]);
    assert_eq!(
        icon_text(MonitorModule::Fans, MenuBarStyle::Value, &sample).lines,
        ["—"]
    );
}

fn smart_device(status: DiskSmartStatus) -> DiskDevice {
    DiskDevice {
        id: "fixture-device".into(),
        name: "Fixture SSD".into(),
        internal: true,
        smart: status,
        percent_used: Some(ratio(0.2)),
        temperature_c: None,
        power_on_hours: None,
        data_written_bytes: None,
        mount_points: vec!["/".into()],
    }
}
#[test]
fn smart_failure_is_critical_and_carries_subject_only_for_smart() {
    let mut sample = opennavo_desktop_lib::monitor::engine::empty_sample(&system());
    sample.disk.devices = Some(vec![smart_device(DiskSmartStatus::Failing)]);
    let mut tracker = opennavo_desktop_lib::monitor::health::HealthTracker::default();
    let health = tracker.evaluate(&sample, &[MonitorModule::Disk].into(), 0, 0);
    assert_eq!(health.level, HealthLevel::Critical);
    let issue = health
        .issues
        .iter()
        .find(|i| i.key == HealthIssueKey::DiskSmart)
        .unwrap();
    assert_eq!(issue.subject.as_deref(), Some("Fixture SSD"));
    assert!(
        health
            .issues
            .iter()
            .filter(|i| i.key != HealthIssueKey::DiskSmart)
            .all(|i| i.subject.is_none())
    );
    sample.disk.devices = Some(vec![smart_device(DiskSmartStatus::Unsupported)]);
    assert!(
        !tracker
            .evaluate(&sample, &[MonitorModule::Disk].into(), 1, 0)
            .issues
            .iter()
            .any(|i| i.key == HealthIssueKey::DiskSmart)
    );
}
#[test]
fn trend_averages_five_second_buckets_nulls_gaps_and_one_hour_bound() {
    let mut trend = opennavo_desktop_lib::monitor::trend::Trend::default();
    let mut sample = opennavo_desktop_lib::monitor::engine::empty_sample(&system());
    let modules = [MonitorModule::Cpu, MonitorModule::Disk, MonitorModule::Fans].into();
    sample.cpu.usage = ratio(0.2);
    sample.disk.read_bytes_per_sec = number(100.0);
    sample.at = 1000;
    trend.add(&sample, &modules);
    sample.cpu.usage = ratio(0.8);
    sample.disk.read_bytes_per_sec = number(300.0);
    sample.at = 4000;
    trend.add(&sample, &modules);
    let points = trend.points();
    assert_eq!(points.len(), 1);
    assert_eq!(points[0].at, 0);
    assert_eq!(f64::from(points[0].cpu.unwrap()), 0.5);
    assert_eq!(f64::from(points[0].disk_read.unwrap()), 200.0);
    assert!(points[0].gpu.is_none() && points[0].memory.is_none() && points[0].fan_rpm.is_none());
    sample.at = 15000;
    trend.add(&sample, &modules);
    assert_eq!(trend.points().len(), 2);
    assert_eq!(trend.points()[1].at, 15000); // No artificial buckets for 5/10 seconds.
    for bucket in 4..=724 {
        sample.at = bucket * 5000;
        trend.add(&sample, &modules);
    }
    let points = trend.points();
    assert_eq!(points.len(), 720);
    assert_eq!(points.first().unwrap().at, 25000);
    sample.at = 10_000_000;
    trend.add(&sample, &modules);
    assert_eq!(trend.points().len(), 1);
}
#[test]
fn graphs_use_last_thirty_core_order_normalization_and_fixed_width() {
    use opennavo_desktop_lib::monitor::menubar::{Graph, icon};
    let mut sample = opennavo_desktop_lib::monitor::engine::empty_sample(&system());
    sample.cpu.cores = vec![ratio(0.9), ratio(0.8), ratio(0.2)];
    let bars = icon(MonitorModule::Cpu, MenuBarStyle::Bars, &sample, &[]);
    assert_eq!(bars.graph, Some(Graph::Bars(vec![0.9, 0.8, 0.2])));
    let mut history = vec![];
    for i in 0..40 {
        sample.at = i * 1000;
        sample.cpu.usage = ratio(i as f64 / 100.0);
        history.push(sample.clone());
    }
    let line = icon(MonitorModule::Cpu, MenuBarStyle::Line, &sample, &history);
    let Some(Graph::Line(points)) = line.graph else {
        panic!("missing line");
    };
    assert_eq!(points.len(), 30);
    assert_eq!(points[0], Some(0.1));
    assert_eq!(points[29], Some(0.39));
    assert_eq!(bars.widest, line.widest);
    sample.network.down_bytes_per_sec = number(20.0);
    sample.network.up_bytes_per_sec = number(40.0);
    history.last_mut().unwrap().network.down_bytes_per_sec = number(80.0);
    assert_eq!(
        icon(
            MonitorModule::Network,
            MenuBarStyle::Bars,
            &sample,
            &history
        )
        .graph,
        Some(Graph::Bars(vec![0.25, 0.5]))
    );
    sample.sensors = Some(vec![SensorReading {
        key: "fixture".into(),
        name: "CPU".into(),
        group: SensorGroup::Cpu,
        celsius: number(140.0),
    }]);
    assert_eq!(
        icon(
            MonitorModule::Sensors,
            MenuBarStyle::Bars,
            &sample,
            &history
        )
        .graph,
        Some(Graph::Bars(vec![1.0]))
    );
    sample.at += 20000;
    history.push(sample.clone());
    let Some(Graph::Line(points)) =
        icon(MonitorModule::Cpu, MenuBarStyle::Line, &sample, &history).graph
    else {
        panic!()
    };
    assert!(points[28].is_none());
}
#[test]
fn process_io_first_delta_resets_pid_reuse_counter_rollbacks_and_missing_data() {
    let mut engine = Engine::new(system(), frame(0));
    let modules = m1_modules();
    let raw = |start, bytes| RawProcess {
        info: process(7, 0.0, 100),
        started: start,
        cpu_ns: 0,
        disk_bytes: bytes,
        net_bytes: bytes,
        net_at: None,
    };
    let step = |engine: &mut Engine, now, start, bytes| {
        let mut f = frame(1);
        f.processes = Some(vec![raw(start, bytes)]);
        engine
            .sample(f, &modules, true, now, now as i64)
            .processes
            .unwrap()
            .remove(0)
    };
    assert!(
        step(&mut engine, 0, 1, Some((100, 200)))
            .disk_read_bytes_per_sec
            .is_none()
    );
    let p = step(&mut engine, 2000, 1, Some((500, 800)));
    assert_eq!(f64::from(p.disk_read_bytes_per_sec.unwrap()), 200.0);
    assert_eq!(f64::from(p.net_up_bytes_per_sec.unwrap()), 300.0);
    assert!(
        step(&mut engine, 4000, 2, Some((700, 1000)))
            .net_down_bytes_per_sec
            .is_none()
    );
    assert!(
        step(&mut engine, 6000, 2, Some((0, 0)))
            .disk_read_bytes_per_sec
            .is_none()
    );
    assert!(
        step(&mut engine, 8000, 2, None)
            .net_up_bytes_per_sec
            .is_none()
    );
    assert!(
        step(&mut engine, 10000, 2, Some((100, 200)))
            .disk_read_bytes_per_sec
            .is_none()
    );
}
#[test]
fn process_union_includes_io_hogs_only_when_the_corresponding_view_is_visible() {
    use opennavo_desktop_lib::monitor::engine::top_processes_io;
    let mut items: Vec<_> = (1..=70)
        .map(|pid| {
            process(
                pid,
                if pid <= 20 { 0.5 } else { 0.0 },
                if (21..=40).contains(&pid) { 10000 } else { 1 },
            )
        })
        .collect();
    for p in &mut items {
        if (41..=50).contains(&p.pid) {
            p.disk_read_bytes_per_sec = Some(number(1000.0));
        }
        if (51..=60).contains(&p.pid) {
            p.net_down_bytes_per_sec = Some(number(1000.0));
        }
    }
    assert_eq!(top_processes_io(items.clone(), false, false).len(), 40);
    let all = top_processes_io(items.clone(), true, true);
    assert_eq!(all.len(), 60);
    assert!(all.iter().any(|p| p.pid == 60));
    assert_eq!(top_processes_io(items, false, true).len(), 50);
}

struct ExtraCollector {
    calls: Arc<Mutex<Vec<(Modules, bool, Extras)>>>,
    active: Arc<std::sync::atomic::AtomicBool>,
}
impl Collector for ExtraCollector {
    fn system(&self) -> SystemInfo {
        system()
    }
    fn network_active_flag(&self) -> Option<Arc<std::sync::atomic::AtomicBool>> {
        Some(self.active.clone())
    }
    fn collect(&mut self, modules: &Modules, processes: bool) -> Frame {
        let mut f = frame(1);
        if !modules.contains(&MonitorModule::Cpu) {
            f.cpu = None;
        }
        if !modules.contains(&MonitorModule::Memory) {
            f.memory = None;
        }
        if !modules.contains(&MonitorModule::Disk) {
            f.disk = None;
        }
        if !modules.contains(&MonitorModule::Network) {
            f.network = None;
        }
        if modules.contains(&MonitorModule::Sensors) {
            f.sensors = Some(vec![SensorReading {
                key: "cpu".into(),
                name: "CPU".into(),
                group: SensorGroup::Cpu,
                celsius: number(80.0),
            }]);
        }
        if modules.contains(&MonitorModule::Gpu) {
            f.gpu = Some(GpuSample {
                usage: ratio(0.25),
                power_watts: None,
            });
        }
        if processes {
            f.processes = Some(vec![RawProcess {
                info: process(7, 0.0, 100),
                started: 1,
                cpu_ns: 0,
                disk_bytes: None,
                net_bytes: None,
                net_at: None,
            }]);
        }
        f
    }
    fn collect_requested(&mut self, modules: &Modules, processes: bool, extras: &Extras) -> Frame {
        self.calls
            .lock()
            .unwrap()
            .push((modules.clone(), processes, extras.clone()));
        let mut f = self.collect(modules, processes);
        if extras.power {
            f.cpu_power = Some(number(12.0));
            if let Some(g) = &mut f.gpu {
                g.power_watts = Some(number(8.0));
            }
        }
        if extras.smart {
            f.disk_devices = Some(vec![smart_device(DiskSmartStatus::Verified)]);
        }
        f
    }
}
type ExtraFixture = (
    Arc<Sampler>,
    Arc<Mutex<Vec<(Modules, bool, Extras)>>>,
    Arc<std::sync::atomic::AtomicBool>,
);
fn extra_sampler(menu: MonitorMenuBar) -> ExtraFixture {
    let calls = Arc::new(Mutex::new(vec![]));
    let active = Arc::new(std::sync::atomic::AtomicBool::new(false));
    (
        Sampler::new(
            Box::new(ExtraCollector {
                calls: calls.clone(),
                active: active.clone(),
            }),
            menu,
            MonitorIntervalMs::TwoSeconds,
        ),
        calls,
        active,
    )
}
#[test]
fn alerts_alone_use_minimum_modules_five_seconds_and_thirty_second_temperature() {
    let (sampler, calls, _) = extra_sampler(off());
    sampler.configure_alerts(true).unwrap();
    let plan = sampler.plan().unwrap().unwrap();
    assert_eq!(plan.interval_ms, 5000);
    assert!(!plan.processes);
    assert_eq!(
        plan.modules,
        [
            MonitorModule::Cpu,
            MonitorModule::Memory,
            MonitorModule::Disk,
            MonitorModule::Network
        ]
        .into_iter()
        .filter(|m| *m != MonitorModule::Network)
        .chain([MonitorModule::Sensors])
        .collect()
    );
    sampler.tick(0, 0).unwrap();
    sampler.tick(5000, 5000).unwrap();
    sampler.tick(30000, 30000).unwrap();
    let trace = calls.lock().unwrap();
    assert!(trace[0].0.contains(&MonitorModule::Sensors));
    assert!(!trace[1].0.contains(&MonitorModule::Sensors));
    assert!(trace[2].0.contains(&MonitorModule::Sensors));
    assert!(
        trace
            .iter()
            .all(|(_, processes, e)| !processes && !e.power && !e.process_network)
    );
    drop(trace);
    sampler.configure_alerts(false).unwrap();
    assert!(sampler.plan().unwrap().is_none());
    assert!(sampler.tick(35000, 35000).unwrap().is_none());
}
#[test]
fn visible_power_is_two_second_cached_smart_ten_minutes_and_network_cancels() {
    let (sampler, calls, active) = extra_sampler(MonitorMenuBar::default());
    sampler.tick(0, 0).unwrap();
    assert!(!calls.lock().unwrap()[0].2.power);
    sampler
        .subscribe(
            "main",
            MonitorView::Page,
            [
                MonitorModule::Cpu,
                MonitorModule::Gpu,
                MonitorModule::Disk,
                MonitorModule::Network,
            ]
            .into(),
        )
        .unwrap();
    assert!(active.load(Ordering::Acquire));
    let first = sampler.tick(1000, 1000).unwrap().unwrap();
    assert_eq!(f64::from(first.sample.cpu.power_watts.unwrap()), 12.0);
    let second = sampler.tick(2000, 2000).unwrap().unwrap();
    assert_eq!(second.sample.cpu.power_watts, first.sample.cpu.power_watts);
    assert!(!calls.lock().unwrap().last().unwrap().2.power);
    assert!(!calls.lock().unwrap().last().unwrap().2.smart);
    sampler.tick(3000, 3000).unwrap();
    assert!(calls.lock().unwrap().last().unwrap().2.power);
    sampler.tick(601000, 601000).unwrap();
    assert!(calls.lock().unwrap().last().unwrap().2.smart);
    sampler.unsubscribe("main", MonitorView::Page).unwrap();
    assert!(!active.load(Ordering::Acquire));
    let menu = sampler.tick(603000, 603000).unwrap().unwrap();
    assert!(menu.sample.cpu.power_watts.is_none());
    assert!(menu.sample.gpu.as_ref().unwrap().power_watts.is_none());
    let last = calls.lock().unwrap().last().unwrap().clone();
    assert!(!last.1 && !last.2.power && !last.2.process_network);
    assert!(!sampler.trend().unwrap().is_empty());
    assert_eq!(sampler.snapshot().unwrap().history.len(), 6);
}

#[test]
fn alert_only_modules_keep_five_second_cadence_alongside_two_second_menu() {
    let (sampler, calls, _) = extra_sampler(MonitorMenuBar::default());
    sampler.configure_alerts(true).unwrap();
    sampler.tick(0, 0).unwrap();
    let second = sampler.tick(2000, 2000).unwrap().unwrap();
    assert!(second.available.contains(&MonitorModule::Disk));
    assert!(
        !calls
            .lock()
            .unwrap()
            .last()
            .unwrap()
            .0
            .contains(&MonitorModule::Disk)
    );
    sampler.tick(4000, 4000).unwrap();
    assert!(
        !calls
            .lock()
            .unwrap()
            .last()
            .unwrap()
            .0
            .contains(&MonitorModule::Disk)
    );
    sampler.tick(6000, 6000).unwrap();
    assert!(
        calls
            .lock()
            .unwrap()
            .last()
            .unwrap()
            .0
            .contains(&MonitorModule::Disk)
    );
}
#[test]
fn independently_captured_network_uses_its_clock_and_pending_snapshot_never_spikes() {
    let mut engine = Engine::new(system(), frame(0));
    let modules = m1_modules();
    let mut step = |now, captured, bytes| {
        let mut f = frame(1);
        f.processes = Some(vec![RawProcess {
            info: process(7, 0.0, 100),
            started: 1,
            cpu_ns: 0,
            disk_bytes: None,
            net_bytes: bytes,
            net_at: captured,
        }]);
        engine
            .sample(f, &modules, true, now, now as i64)
            .processes
            .unwrap()
            .remove(0)
    };
    assert!(
        step(2000, Some(100), Some((100, 100)))
            .net_down_bytes_per_sec
            .is_none()
    );
    let p = step(4000, Some(2100), Some((500, 700)));
    assert_eq!(f64::from(p.net_down_bytes_per_sec.unwrap()), 200.0);
    assert!(
        step(6000, Some(2100), Some((500, 700)))
            .net_down_bytes_per_sec
            .is_none()
    );
    assert!(step(6500, None, None).net_down_bytes_per_sec.is_none());
    let p = step(8000, Some(6100), Some((900, 1100)));
    assert_eq!(f64::from(p.net_down_bytes_per_sec.unwrap()), 100.0);
}
#[test]
fn skipped_background_core_read_keeps_cpu_delta_baseline_and_availability() {
    let mut engine = Engine::new(system(), frame(0));
    let modules = [MonitorModule::Cpu, MonitorModule::Network].into();
    engine.sample(frame(1), &modules, false, 0, 0);
    let mut skipped = frame(2);
    skipped.cpu = None;
    engine.sample_read(
        skipped,
        &modules,
        &[MonitorModule::Network].into(),
        false,
        1000,
        1000,
    );
    assert!(engine.available.contains(&MonitorModule::Cpu));
    let sample = engine.sample(frame(3), &modules, false, 2000, 2000);
    assert!(f64::from(sample.cpu.usage) > 0.0);
}
#[cfg(target_os = "macos")]
#[test]
fn disabled_native_network_gate_returns_without_a_subprocess() {
    use opennavo_desktop_lib::monitor::extras::{Network, network};
    let flag = Arc::new(std::sync::atomic::AtomicBool::new(false));
    let mut source = Network::new(flag.clone());
    assert!(source.request().is_none());
    assert!(network(&flag).is_none());
}

#[test]
fn volume_cache_keeps_live_space_deltas_and_refreshes_for_low_space_time_or_device_changes() {
    use opennavo_desktop_lib::monitor::volume_cache::{Resources, VolumeCache};
    let mut cache = VolumeCache::default();
    let reads = std::cell::Cell::new(0);
    let read = || {
        reads.set(reads.get() + 1);
        Resources {
            important: Some(300_000_000_000),
            name: Some("Fixture".into()),
            internal: Some(true),
        }
    };
    let total = 1_000_000_000_000;
    cache.resolve("disk-A", 0, total, 200_000_000_000, read);
    assert_eq!(
        cache
            .resolve("disk-A", 1000, total, 190_000_000_000, read)
            .important,
        Some(290_000_000_000)
    );
    assert_eq!(reads.get(), 1);
    assert_eq!(
        cache
            .resolve("disk-A", 2000, total, 210_000_000_000, read)
            .important,
        Some(310_000_000_000)
    );
    cache.resolve("disk-A", 60000, total, 210_000_000_000, read);
    assert_eq!(reads.get(), 2);
    cache.resolve("disk-B", 60001, total, 210_000_000_000, read);
    assert_eq!(reads.get(), 3);
    cache.resolve("disk-A", 60002, total, 20_000_000_000, read);
    assert_eq!(reads.get(), 3); // 110 GB including purgeable space: above the health threshold.
    cache.resolve("disk-A", 60003, total, 0, read);
    assert_eq!(reads.get(), 4); // Live important estimate is 90 GB: confirm below 10% immediately.
    let low = || {
        reads.set(reads.get() + 1);
        Resources {
            important: Some(15_000_000_000),
            ..Default::default()
        }
    };
    cache.resolve("disk-low", 0, total, 10_000_000_000, low);
    cache.resolve("disk-low", 1, total, 10_000_000_000, low);
    assert_eq!(reads.get(), 5); // An unchanged attention state reuses metadata.
    cache.resolve("disk-low", 2, total, 0, low);
    assert_eq!(reads.get(), 5); // Exactly 5 GB remains attention.
    cache.resolve("disk-critical", 0, total, 10, || {
        reads.set(reads.get() + 1);
        Resources {
            important: Some(5_000_000_000),
            ..Default::default()
        }
    });
    cache.resolve("disk-critical", 1, total, 9, low);
    assert_eq!(reads.get(), 7); // One byte below 5 GB confirms critical immediately.
    let ratio = || {
        reads.set(reads.get() + 1);
        Resources {
            important: Some(90_000_000_000),
            ..Default::default()
        }
    };
    cache.resolve("disk-ratio", 0, total, 80_000_000_000, ratio);
    cache.resolve("disk-ratio", 1, total, 80_000_000_000, ratio);
    assert_eq!(reads.get(), 8);
    cache.resolve("disk-ratio", 2, total, 90_000_000_000, ratio);
    assert_eq!(reads.get(), 9); // Estimate reaches 10%: confirm apparent recovery.
    cache.retain(&Default::default());
    cache.resolve("disk-A", 60004, total, 210_000_000_000, read);
    assert_eq!(reads.get(), 10);
    cache.resolve("disk-A", 60005, total * 2, 210_000_000_000, read);
    assert_eq!(reads.get(), 11); // Changed total invalidates even within one minute.
    let unavailable = || {
        reads.set(reads.get() + 1);
        Resources::default()
    };
    cache.resolve("disk-raw", 0, total, 10_000_000_000, unavailable);
    cache.resolve("disk-raw", 1, total, 4_000_000_000, unavailable);
    assert_eq!(reads.get(), 13); // Without NSURL capacity, raw statfs thresholds apply.
}

#[test]
fn hidden_window_preserves_intent_without_process_power_or_network_demand() {
    let calls = Arc::new(Mutex::new(vec![]));
    let active = Arc::new(std::sync::atomic::AtomicBool::new(false));
    let sampler = Sampler::new(
        Box::new(ExtraCollector {
            calls: calls.clone(),
            active: active.clone(),
        }),
        off(),
        MonitorIntervalMs::TwoSeconds,
    );
    let modules = [
        MonitorModule::Cpu,
        MonitorModule::Network,
        MonitorModule::Disk,
    ]
    .into_iter()
    .collect();
    sampler
        .subscribe("main", MonitorView::Page, modules)
        .unwrap();
    sampler
        .subscribe(
            "main",
            MonitorView::Detail,
            [MonitorModule::Cpu].into_iter().collect(),
        )
        .unwrap();
    assert!(active.load(Ordering::Acquire));
    sampler.tick(0, 0).unwrap();
    for _ in 0..3 {
        sampler.set_window_visible("main", false);
        assert!(sampler.plan().unwrap().is_none());
        assert!(!active.load(Ordering::Acquire));
        assert!(sampler.tick(1000, 1000).unwrap().is_none());
        // A delayed IPC subscribe must not resume a hidden window.
        sampler
            .subscribe_fresh_at(
                "main",
                MonitorView::Detail,
                [MonitorModule::Cpu].into_iter().collect(),
                1000,
                1000,
            )
            .unwrap();
        assert!(sampler.plan().unwrap().is_none());
        sampler.set_window_visible("main", true);
        assert_eq!(sampler.plan().unwrap().unwrap().interval_ms, 1000);
        assert!(active.load(Ordering::Acquire));
    }
    sampler.set_window_visible("main", false);
    sampler
        .configure(MonitorMenuBar::default(), MonitorIntervalMs::TwoSeconds)
        .unwrap();
    let plan = sampler.plan().unwrap().unwrap();
    assert_eq!(plan.interval_ms, 2000);
    assert!(!plan.processes);
    let publication = sampler.tick(2000, 2000).unwrap().unwrap();
    assert!(publication.windows.is_empty());
    let reads = calls.lock().unwrap();
    let (_, processes, extras) = reads.last().unwrap();
    assert!(!processes);
    assert!(
        !extras.power && !extras.power_visible && !extras.process_network && !extras.process_disk
    );
    drop(reads);
    sampler
        .subscribe(
            "tray-monitor",
            MonitorView::Popover,
            [MonitorModule::Memory].into_iter().collect(),
        )
        .unwrap();
    assert!(sampler.plan().unwrap().unwrap().processes);
    sampler.remove_window("tray-monitor");
    sampler
        .configure(off(), MonitorIntervalMs::TwoSeconds)
        .unwrap();
    sampler.destroy_window("main");
    sampler.set_window_visible("main", true);
    sampler
        .subscribe(
            "main",
            MonitorView::Page,
            [MonitorModule::Cpu].into_iter().collect(),
        )
        .unwrap();
    assert!(sampler.plan().unwrap().is_none());
}

#[test]
fn in_flight_collection_is_discarded_when_main_window_hides() {
    struct Blocking {
        calls: Arc<AtomicUsize>,
        entered: Arc<std::sync::Barrier>,
        release: Arc<std::sync::Barrier>,
    }
    impl Collector for Blocking {
        fn system(&self) -> SystemInfo {
            system()
        }
        fn collect(&mut self, _: &Modules, _: bool) -> Frame {
            if self.calls.fetch_add(1, Ordering::SeqCst) > 0 {
                self.entered.wait();
                self.release.wait();
            }
            frame(1)
        }
    }
    let entered = Arc::new(std::sync::Barrier::new(2));
    let release = Arc::new(std::sync::Barrier::new(2));
    let sampler = Sampler::new(
        Box::new(Blocking {
            calls: Arc::new(AtomicUsize::new(0)),
            entered: entered.clone(),
            release: release.clone(),
        }),
        MonitorMenuBar::default(),
        MonitorIntervalMs::TwoSeconds,
    );
    sampler
        .subscribe(
            "main",
            MonitorView::Page,
            [MonitorModule::Cpu].into_iter().collect(),
        )
        .unwrap();
    let worker = sampler.clone();
    let thread = std::thread::spawn(move || worker.tick(0, 0));
    entered.wait();
    sampler.set_window_visible("main", false);
    release.wait();
    assert!(thread.join().unwrap().unwrap().is_none());
    assert!(sampler.snapshot().unwrap().history.is_empty());
}

#[test]
fn recreated_window_accepts_new_subscriptions_but_rejects_old_instance_work() {
    let (sampler, calls) = sampler(off());
    let label = "tray-monitor";
    let old = sampler.register_window(label).unwrap();
    sampler
        .subscribe_fresh_instance(
            label,
            old,
            MonitorView::Popover,
            [MonitorModule::Cpu].into_iter().collect(),
        )
        .unwrap();
    sampler.destroy_window_instance(label, old);
    assert!(!sampler.window_instance_active(label, old));
    assert!(sampler.plan().unwrap().is_none());
    let reads = calls.lock().unwrap().len();
    assert!(
        sampler
            .subscribe_fresh_instance(
                label,
                old,
                MonitorView::Popover,
                [MonitorModule::Cpu].into_iter().collect()
            )
            .is_err()
    );
    assert_eq!(calls.lock().unwrap().len(), reads);
    let new = sampler.register_window(label).unwrap();
    assert_ne!(old, new);
    assert!(sampler.window_instance_active(label, new));
    sampler
        .subscribe_fresh_instance(
            label,
            new,
            MonitorView::Popover,
            [MonitorModule::Memory].into_iter().collect(),
        )
        .unwrap();
    assert_eq!(
        sampler.plan().unwrap().unwrap().modules,
        [MonitorModule::Memory].into_iter().collect()
    );
    let reads = calls.lock().unwrap().len();
    // Work captured before destruction cannot overwrite or unsubscribe the replacement view.
    assert!(
        sampler
            .subscribe_fresh_instance(
                label,
                old,
                MonitorView::Popover,
                [MonitorModule::Cpu].into_iter().collect()
            )
            .is_err()
    );
    assert!(
        sampler
            .unsubscribe_instance(label, old, MonitorView::Popover)
            .is_err()
    );
    sampler.destroy_window_instance(label, old);
    assert_eq!(calls.lock().unwrap().len(), reads);
    assert_eq!(
        sampler.plan().unwrap().unwrap().modules,
        [MonitorModule::Memory].into_iter().collect()
    );
    sampler.set_window_visible(label, false);
    assert!(sampler.plan().unwrap().is_none());
    sampler.set_window_visible(label, true);
    assert!(
        sampler
            .subscribe_fresh_instance(
                label,
                old,
                MonitorView::Popover,
                [MonitorModule::Cpu].into_iter().collect()
            )
            .is_err()
    );
    assert_eq!(
        sampler.tick(10000, 10000).unwrap().unwrap().windows,
        [label]
    );
    sampler
        .unsubscribe_instance(label, new, MonitorView::Popover)
        .unwrap();
    assert!(sampler.plan().unwrap().is_none());
}

#[test]
fn queued_subscription_refresh_cannot_restore_an_unsubscribed_view() {
    let (sampler, calls) = sampler(off());
    let instance = sampler.register_window("main").unwrap();
    let queued = sampler
        .prepare_subscription(
            "main",
            instance,
            MonitorView::Page,
            [MonitorModule::Cpu].into(),
        )
        .unwrap();
    assert!(sampler.plan().unwrap().unwrap().processes);
    sampler
        .unsubscribe_instance("main", instance, MonitorView::Page)
        .unwrap();
    assert!(sampler.plan().unwrap().is_none());
    let reads = calls.lock().unwrap().len();
    // Execute the exact job used by IPC only after the synchronous unsubscribe.
    queued().unwrap();
    assert!(sampler.plan().unwrap().is_none());
    assert_eq!(calls.lock().unwrap().len(), reads);
}

#[test]
fn queued_subscription_refresh_does_not_overwrite_a_newer_subscription() {
    let (sampler, _) = sampler(off());
    let instance = sampler.register_window("main").unwrap();
    let old = sampler
        .prepare_subscription(
            "main",
            instance,
            MonitorView::Page,
            [MonitorModule::Cpu].into(),
        )
        .unwrap();
    sampler
        .unsubscribe_instance("main", instance, MonitorView::Page)
        .unwrap();
    let new = sampler
        .prepare_subscription(
            "main",
            instance,
            MonitorView::Page,
            [MonitorModule::Memory].into(),
        )
        .unwrap();
    new().unwrap();
    old().unwrap();
    assert_eq!(
        sampler.plan().unwrap().unwrap().modules,
        [MonitorModule::Memory].into()
    );
    sampler
        .unsubscribe_instance("main", instance, MonitorView::Page)
        .unwrap();
    assert!(sampler.plan().unwrap().is_none());
}
