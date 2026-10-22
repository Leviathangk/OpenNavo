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
    // The first capture has nothing to measure against, so the next one comes about a second later instead of two;
    // from then on the two-second throttle holds.
    assert_eq!(
        calls.iter().skip(1).map(|(_, p)| *p).collect::<Vec<_>>(),
        [true, true, false, true]
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
        count: 1,
        is_self: false,
    }
}
fn fixture_group() -> Group {
    Group {
        key: "exe:fixture".into(),
        label: "fixture".into(),
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

struct RecoveringCollector {
    calls: Calls,
    readable: Arc<std::sync::atomic::AtomicBool>,
}
impl Collector for RecoveringCollector {
    fn system(&self) -> SystemInfo {
        system()
    }
    fn collect(&mut self, modules: &Modules, processes: bool) -> Frame {
        self.calls
            .lock()
            .unwrap()
            .push((modules.clone(), processes));
        Frame {
            cpu: (self.readable.load(Ordering::SeqCst) && modules.contains(&MonitorModule::Cpu))
                .then(|| cpu(self.calls.lock().unwrap().len() as u32)),
            ..Frame::default()
        }
    }
}
fn recovering_sampler(
    menu: MonitorMenuBar,
) -> (Arc<Sampler>, Calls, Arc<std::sync::atomic::AtomicBool>) {
    let calls = Arc::new(Mutex::new(vec![]));
    let readable = Arc::new(std::sync::atomic::AtomicBool::new(false));
    let sampler = Sampler::new(
        Box::new(RecoveringCollector {
            calls: calls.clone(),
            readable: readable.clone(),
        }),
        menu,
        MonitorIntervalMs::TwoSeconds,
    );
    (sampler, calls, readable)
}
#[test]
fn startup_probe_failure_recovers_on_subscription_then_samples_normally() {
    let (sampler, calls, readable) = recovering_sampler(off());
    readable.store(true, Ordering::SeqCst);
    assert!(sampler.tick(0, 0).unwrap().is_none());
    assert_eq!(calls.lock().unwrap().len(), 1, "no demand must not probe");
    let snapshot = sampler
        .subscribe_fresh_at("main", MonitorView::Page, [MonitorModule::Cpu].into(), 1, 1)
        .unwrap();
    assert!(snapshot.available.contains(&MonitorModule::Cpu));
    for now in [1000, 2000, 3000] {
        assert!(
            sampler
                .tick(now, now as i64)
                .unwrap()
                .unwrap()
                .available
                .contains(&MonitorModule::Cpu)
        );
    }
    assert_eq!(calls.lock().unwrap().len(), 5);
    sampler.unsubscribe("main", MonitorView::Page).unwrap();
    assert!(sampler.tick(60_000, 60_000).unwrap().is_none());
    assert_eq!(calls.lock().unwrap().len(), 5);
}
#[test]
fn unsupported_modules_have_bounded_retries_and_sleep_wake_rearms_recovery() {
    let (sampler, calls, readable) = recovering_sampler(off());
    let wanted = [MonitorModule::Cpu, MonitorModule::Fans].into();
    sampler
        .subscribe("main", MonitorView::Page, wanted)
        .unwrap();
    for now in [0, 1000, 29_999, 30_000, 60_000, 90_000, 120_000] {
        // Repeated subscription refreshes must not replenish the retry budget.
        sampler
            .subscribe(
                "main",
                MonitorView::Page,
                [MonitorModule::Cpu, MonitorModule::Fans].into(),
            )
            .unwrap();
        sampler.tick(now, now as i64).unwrap();
    }
    assert_eq!(
        calls.lock().unwrap().len(),
        4,
        "startup plus three spaced attempts"
    );
    assert!(sampler.plan().unwrap().is_none());
    sampler.pause(PauseReason::Screen, true);
    sampler.pause(PauseReason::Screen, false);
    assert!(sampler.tick(120_500, 120_500).unwrap().is_none());
    assert_eq!(
        calls.lock().unwrap().len(),
        4,
        "screen changes do not replenish probes"
    );
    readable.store(true, Ordering::SeqCst);
    sampler.pause(PauseReason::Sleep, true);
    sampler.pause(PauseReason::Screen, true);
    sampler.pause(PauseReason::Sleep, false);
    assert!(sampler.tick(121_000, 121_000).unwrap().is_none());
    assert_eq!(calls.lock().unwrap().len(), 4);
    sampler.pause(PauseReason::Screen, false);
    let recovered = sampler.tick(122_000, 122_000).unwrap().unwrap();
    assert!(recovered.available.contains(&MonitorModule::Cpu));
    assert!(!recovered.available.contains(&MonitorModule::Fans));
    sampler.tick(123_000, 123_000).unwrap();
    assert_eq!(
        calls.lock().unwrap().last().unwrap().0,
        [MonitorModule::Cpu].into()
    );
}
#[test]
fn recovery_waits_for_demand_and_pause_release_and_keeps_cooldown_across_reopen() {
    let (sampler, calls, readable) = recovering_sampler(off());
    sampler
        .subscribe("main", MonitorView::Page, [MonitorModule::Cpu].into())
        .unwrap();
    sampler.tick(0, 0).unwrap();
    sampler.unsubscribe("main", MonitorView::Page).unwrap();
    sampler.tick(30_000, 30_000).unwrap();
    assert_eq!(
        calls.lock().unwrap().len(),
        2,
        "pending probes do no work without demand"
    );
    sampler
        .subscribe("main", MonitorView::Page, [MonitorModule::Cpu].into())
        .unwrap();
    sampler.pause(PauseReason::Lock, true);
    sampler.tick(30_001, 30_001).unwrap();
    assert_eq!(
        calls.lock().unwrap().len(),
        2,
        "pause also stops pending probes"
    );
    sampler.pause(PauseReason::Lock, false);
    sampler.tick(30_002, 30_002).unwrap();
    assert_eq!(calls.lock().unwrap().len(), 3);
    sampler.unsubscribe("main", MonitorView::Page).unwrap();
    sampler
        .subscribe("main", MonitorView::Page, [MonitorModule::Cpu].into())
        .unwrap();
    sampler.tick(30_003, 30_003).unwrap();
    assert_eq!(
        calls.lock().unwrap().len(),
        3,
        "reopen cannot bypass the cooldown"
    );
    readable.store(true, Ordering::SeqCst);
    assert!(
        sampler
            .tick(60_002, 60_002)
            .unwrap()
            .unwrap()
            .available
            .contains(&MonitorModule::Cpu)
    );
    readable.store(false, Ordering::SeqCst);
    assert!(
        !sampler
            .tick(61_002, 61_002)
            .unwrap()
            .unwrap()
            .available
            .contains(&MonitorModule::Cpu)
    );
    readable.store(true, Ordering::SeqCst);
    assert!(
        sampler
            .tick(62_002, 62_002)
            .unwrap()
            .unwrap()
            .available
            .contains(&MonitorModule::Cpu),
        "known hardware recovers on its regular cadence"
    );
}
#[test]
fn sampler_loop_runs_demanded_recovery_without_a_regular_plan() {
    let (sampler, _, readable) = recovering_sampler(MonitorMenuBar::default());
    readable.store(true, Ordering::SeqCst);
    assert!(sampler.plan().unwrap().is_none());
    let (tx, rx) = std::sync::mpsc::channel();
    sampler.start(Arc::new(move |p| {
        let _ = tx.send(p.available);
    }));
    let available = rx.recv_timeout(std::time::Duration::from_secs(2));
    sampler
        .configure(off(), MonitorIntervalMs::TwoSeconds)
        .unwrap();
    assert!(available.unwrap().contains(&MonitorModule::Cpu));
}
#[test]
fn concurrent_recovery_ticks_share_one_probe_budget_and_cooldown() {
    struct SlowUnavailable(Arc<AtomicUsize>);
    impl Collector for SlowUnavailable {
        fn system(&self) -> SystemInfo {
            system()
        }
        fn collect(&mut self, _: &Modules, _: bool) -> Frame {
            if self.0.fetch_add(1, Ordering::SeqCst) > 0 {
                // Model a slow native driver while the other refreshes queue behind it.
                std::thread::sleep(std::time::Duration::from_millis(30));
            }
            Frame::default()
        }
    }
    let calls = Arc::new(AtomicUsize::new(0));
    let sampler = Sampler::new(
        Box::new(SlowUnavailable(calls.clone())),
        off(),
        MonitorIntervalMs::TwoSeconds,
    );
    sampler
        .subscribe("main", MonitorView::Page, [MonitorModule::Fans].into())
        .unwrap();
    let start = Arc::new(std::sync::Barrier::new(9));
    let workers: Vec<_> = (0..8)
        .map(|_| {
            let sampler = sampler.clone();
            let start = start.clone();
            std::thread::spawn(move || {
                start.wait();
                sampler.tick(0, 0).unwrap()
            })
        })
        .collect();
    start.wait();
    let published = workers
        .into_iter()
        .filter_map(|worker| worker.join().unwrap())
        .count();
    assert_eq!(published, 1);
    assert_eq!(
        calls.load(Ordering::SeqCst),
        2,
        "startup and only one demanded probe"
    );
}
#[test]
fn protection_ends_on_each_fan_handback_but_not_sampling_reset() {
    use opennavo_desktop_lib::monitor::health::HealthTracker;
    let sample = opennavo_desktop_lib::monitor::engine::empty_sample(&system());
    for reason in [
        FanReason::User,
        FanReason::WakeReapplied,
        FanReason::HeartbeatLost,
        FanReason::WriteFailed,
        FanReason::HelperStarted,
        FanReason::HelperUninstalled,
        FanReason::AppQuit,
        FanReason::Sleep,
    ] {
        let mut tracker = HealthTracker::default();
        tracker.fan_reason(FanReason::Overheat);
        assert_eq!(
            tracker.evaluate(&sample, &Modules::new(), 0, 0).level,
            HealthLevel::Critical
        );
        // Engine reset on screen/sleep only restarts sustained-duration counters.
        tracker.reset();
        assert_eq!(
            tracker.evaluate(&sample, &Modules::new(), 1, 0).level,
            HealthLevel::Critical
        );
        tracker.fan_reason(reason);
        assert_eq!(
            tracker.evaluate(&sample, &Modules::new(), 2, 0).level,
            HealthLevel::Good,
            "{reason:?}"
        );
    }
}
#[test]
fn ending_fan_protection_still_reports_a_sustained_high_temperature() {
    use opennavo_desktop_lib::monitor::health::HealthTracker;
    let mut sample = opennavo_desktop_lib::monitor::engine::empty_sample(&system());
    sample.sensors = Some(vec![SensorReading {
        key: "cpu".into(),
        name: "CPU".into(),
        group: SensorGroup::Cpu,
        celsius: number(98.0),
    }]);
    let modules = [MonitorModule::Sensors].into();
    let mut tracker = HealthTracker::default();
    tracker.fan_reason(FanReason::Overheat);
    assert_eq!(
        tracker.evaluate(&sample, &modules, 0, 0).level,
        HealthLevel::Critical
    );
    tracker.fan_reason(FanReason::HeartbeatLost);
    assert_eq!(
        tracker.evaluate(&sample, &modules, 10_000, 0).level,
        HealthLevel::Attention
    );
    sample.sensors.as_mut().unwrap()[0].celsius = number(50.0);
    assert_eq!(
        tracker.evaluate(&sample, &modules, 11_000, 0).level,
        HealthLevel::Good
    );
}
#[test]
fn decoded_battery_temperature_warns_only_above_forty_celsius() {
    use opennavo_desktop_lib::monitor::{battery, health::HealthTracker};
    let mut sample = opennavo_desktop_lib::monitor::engine::empty_sample(&system());
    let modules = [MonitorModule::Battery].into();
    let mut tracker = HealthTracker::default();
    for (raw, hot) in [
        (2850.0, false),
        (3300.0, false),
        (3999.0, false),
        (4000.0, false),
        (4100.0, true),
        (65535.0, false),
    ] {
        sample.battery = Some(BatterySample {
            percent: ratio(0.8),
            charging: false,
            on_ac: false,
            minutes_remaining: None,
            health_percent: None,
            cycle_count: None,
            design_capacity_mah: None,
            max_capacity_mah: None,
            temperature_c: battery::temperature(Some(raw)).map(number),
            voltage_v: None,
            power_watts: None,
            adapter_watts: None,
        });
        let health = tracker.evaluate(&sample, &modules, 0, 0);
        assert_eq!(
            health
                .issues
                .iter()
                .any(|issue| issue.key == HealthIssueKey::BatteryHot),
            hot,
            "raw={raw}"
        );
    }
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
        child_cpu_ns: None,
        ppid: 1,
        group: fixture_group(),
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
        child_cpu_ns: None,
        ppid: 1,
        group: fixture_group(),
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
                child_cpu_ns: None,
                ppid: 1,
                group: fixture_group(),
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
            child_cpu_ns: None,
            ppid: 1,
            group: fixture_group(),
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

#[test]
fn fan_change_replaces_the_cached_reading_drops_in_flight_ticks_and_reads_again() {
    fn fan(mode: FanMode) -> FanState {
        FanState {
            id: 0,
            name: "Fan 1".into(),
            rpm: 2000,
            min_rpm: 1200,
            max_rpm: 6000,
            target_rpm: (mode == FanMode::Manual).then_some(3000),
            mode,
        }
    }
    struct FanFake {
        mode: Arc<Mutex<FanMode>>,
        reads: Arc<AtomicUsize>,
        hold: Arc<std::sync::atomic::AtomicBool>,
        entered: Arc<std::sync::Barrier>,
        release: Arc<std::sync::Barrier>,
    }
    impl Collector for FanFake {
        fn system(&self) -> SystemInfo {
            system()
        }
        fn collect(&mut self, modules: &Modules, _: bool) -> Frame {
            let mut f = frame(1);
            if modules.contains(&MonitorModule::Fans) {
                self.reads.fetch_add(1, Ordering::SeqCst);
                f.fans = Some(vec![fan(*self.mode.lock().unwrap())]);
            }
            if self.hold.swap(false, Ordering::SeqCst) {
                self.entered.wait();
                self.release.wait();
            }
            f
        }
    }
    let mode = Arc::new(Mutex::new(FanMode::Auto));
    let reads = Arc::new(AtomicUsize::new(0));
    let hold = Arc::new(std::sync::atomic::AtomicBool::new(false));
    let entered = Arc::new(std::sync::Barrier::new(2));
    let release = Arc::new(std::sync::Barrier::new(2));
    let sampler = Sampler::new(
        Box::new(FanFake {
            mode: mode.clone(),
            reads: reads.clone(),
            hold: hold.clone(),
            entered: entered.clone(),
            release: release.clone(),
        }),
        off(),
        MonitorIntervalMs::TwoSeconds,
    );
    sampler
        .subscribe(
            "tray-monitor",
            MonitorView::Popover,
            [MonitorModule::Sensors, MonitorModule::Fans].into(),
        )
        .unwrap();
    reads.store(0, Ordering::SeqCst); // The capability probe is not a periodic read.
    let mode_of = |sample: MonitorSample| sample.fans.unwrap()[0].mode;
    assert_eq!(
        mode_of(sampler.tick(0, 0).unwrap().unwrap().sample),
        FanMode::Auto
    );
    // The helper switches to manual while a one-second tick still carries the reading from 0 ms.
    hold.store(true, Ordering::SeqCst);
    let worker = sampler.clone();
    let thread = std::thread::spawn(move || worker.tick(1000, 1000));
    entered.wait();
    *mode.lock().unwrap() = FanMode::Manual;
    sampler.fan_changed(&FanChange {
        fans: vec![fan(FanMode::Manual)],
        reason: FanReason::User,
    });
    release.wait();
    assert!(thread.join().unwrap().unwrap().is_none());
    assert_eq!(
        mode_of(sampler.presentation().unwrap().sample),
        FanMode::Manual
    );
    // The next tick reads the fans again instead of waiting out the five seconds, then the cadence resumes.
    assert_eq!(
        mode_of(sampler.tick(1200, 1200).unwrap().unwrap().sample),
        FanMode::Manual
    );
    assert_eq!(reads.load(Ordering::SeqCst), 2);
    sampler.tick(2200, 2200).unwrap();
    assert_eq!(reads.load(Ordering::SeqCst), 2);
}

// ---- Process CPU attribution: processes born between captures, children that ended, and what the parts add up to. ----
fn host(step: u32, busy: u32) -> RawCpu {
    // Every core spends `busy` of each 100 ticks working.
    RawCpu {
        ticks: (0..6)
            .map(|_| [busy * step, 0, (100 - busy) * step, 0])
            .collect(),
        load: [0.0; 3],
    }
}
fn secs(value: f64) -> u64 {
    (value * 1e9) as u64
}
fn capture(mono: u64, wall: u64) -> Capture {
    Capture {
        mono_ns: mono * 1_000_000_000,
        wall_us: wall * 1_000_000,
    }
}
fn near(actual: f64, expected: f64) {
    assert!(
        (actual - expected).abs() < 1e-9,
        "{actual} is not {expected}"
    );
}
/// A process whose CPU totals and start (wall-clock seconds) are given directly.
fn live(pid: u32, ppid: u32, started: u64, cpu: f64, children: Option<f64>) -> RawProcess {
    RawProcess {
        info: process(pid, 0.0, 1000),
        started: started * 1_000_000,
        cpu_ns: secs(cpu),
        child_cpu_ns: children.map(secs),
        ppid,
        group: fixture_group(),
        disk_bytes: None,
        net_bytes: None,
        net_at: None,
    }
}
fn capture_step(
    engine: &mut Engine,
    step: u32,
    busy: u32,
    at: Capture,
    processes: Vec<RawProcess>,
) -> MonitorSample {
    let mut f = frame(step);
    f.cpu = Some(host(step, busy));
    f.processes = Some(processes);
    f.processes_at = Some(at);
    engine.sample(f, &m1_modules(), true, at.mono_ns / 1_000_000, 0)
}
fn cpu_of(sample: &MonitorSample, pid: u32) -> f64 {
    f64::from(
        sample
            .processes
            .as_ref()
            .unwrap()
            .iter()
            .find(|p| p.pid == pid)
            .unwrap()
            .cpu,
    )
}
#[test]
fn a_process_born_since_the_previous_capture_counts_its_cpu_since_start_and_others_wait_for_a_baseline()
 {
    let mut engine = Engine::new(system(), frame(0));
    let first = capture_step(
        &mut engine,
        1,
        50,
        capture(10, 1000),
        vec![live(7, 1, 500, 100.0, None)],
    );
    // Nothing to measure against yet.
    assert_eq!(cpu_of(&first, 7), 0.0);
    assert!(first.cpu_breakdown.is_none());
    assert_eq!(engine.process_interval_ms(), 900);
    let second = capture_step(
        &mut engine,
        2,
        50,
        capture(12, 1002),
        vec![
            live(7, 1, 500, 103.0, None),
            // Started after the previous capture began: all 1.5 s of CPU were used inside this window.
            live(8, 1, 1001, 1.5, None),
            // Older than the previous capture but not seen then: no way to say how much was this window's.
            live(9, 1, 900, 50.0, None),
        ],
    );
    // Six cores for two seconds offer 12 CPU-seconds.
    near(cpu_of(&second, 7), 3.0 / 12.0);
    near(cpu_of(&second, 8), 1.5 / 12.0);
    assert_eq!(cpu_of(&second, 9), 0.0);
    assert_eq!(engine.process_interval_ms(), 2000);
    // A baseline older than the window limit is refused, and a prompt second capture is asked for.
    let stale = capture_step(
        &mut engine,
        3,
        50,
        capture(40, 1030),
        vec![live(7, 1, 500, 110.0, None), live(10, 1, 1029, 4.0, None)],
    );
    assert_eq!(cpu_of(&stale, 7), 0.0);
    assert_eq!(cpu_of(&stale, 10), 0.0);
    assert!(stale.cpu_breakdown.is_none());
    assert_eq!(engine.process_interval_ms(), 900);
}
#[test]
fn a_reused_pid_is_a_new_process_and_gets_nothing_for_cpu_it_used_before() {
    // Same pid, different start: a new process, not the old one's continuation.
    let mut engine = Engine::new(system(), frame(0));
    capture_step(
        &mut engine,
        1,
        50,
        capture(10, 1000),
        vec![live(7, 1, 500, 100.0, None)],
    );
    let reused = capture_step(
        &mut engine,
        2,
        50,
        capture(12, 1002),
        // Started long before the previous capture, so its CPU is not this window's, whatever its total.
        vec![live(7, 1, 600, 900.0, None)],
    );
    assert_eq!(cpu_of(&reused, 7), 0.0);
}
#[test]
fn children_that_ended_are_credited_once_to_the_breakdown_and_their_parent_is_named() {
    let mut engine = Engine::new(system(), frame(0));
    let mut go = live(100, 1, 100, 10.0, Some(0.0));
    go.info.name = "go".into();
    capture_step(
        &mut engine,
        1,
        90,
        capture(10, 1000),
        vec![go.clone(), live(101, 100, 100, 5.0, Some(0.0))],
    );
    // The child works for two more seconds, all of it listed against the child.
    go.cpu_ns = secs(10.2);
    let second = capture_step(
        &mut engine,
        2,
        90,
        capture(12, 1002),
        vec![go.clone(), live(101, 100, 100, 7.0, Some(0.0))],
    );
    near(cpu_of(&second, 101), 2.0 / 12.0);
    let breakdown = second.cpu_breakdown.clone().unwrap();
    near(f64::from(breakdown.finished), 0.0);
    // Child 101 then ends having used 8.5 s in all, and child 102 lived and died between captures using 1 s: the
    // parent collected both. 101's 7 s were counted while it was alive; the other 1.5 s and 102's 1 s were not.
    go.cpu_ns = secs(10.4);
    go.child_cpu_ns = Some(secs(9.5));
    let third = capture_step(&mut engine, 3, 90, capture(14, 1004), vec![go.clone()]);
    let breakdown = third.cpu_breakdown.clone().unwrap();
    near(f64::from(breakdown.finished), 2.5 / 12.0);
    assert_eq!(breakdown.finished_from.len(), 1);
    assert_eq!(breakdown.finished_from[0].pid, 100);
    assert_eq!(breakdown.finished_from[0].name, "go");
    near(f64::from(breakdown.finished_from[0].cpu), 2.5 / 12.0);
    // Counted once: the next window has nothing more to credit.
    go.cpu_ns = secs(10.6);
    let fourth = capture_step(&mut engine, 4, 90, capture(16, 1006), vec![go]);
    near(f64::from(fourth.cpu_breakdown.unwrap().finished), 0.0);
}
#[test]
fn the_breakdown_splits_the_host_busy_share_and_never_overshoots_it() {
    let mut engine = Engine::new(system(), frame(0));
    let procs = |cpu_a: f64, cpu_b: f64, children: f64| {
        vec![
            live(1, 0, 100, cpu_a, Some(children)),
            live(2, 0, 100, cpu_b, None),
        ]
    };
    capture_step(&mut engine, 1, 90, capture(10, 1000), procs(0.0, 0.0, 0.0));
    // 90% busy; the two processes used 3.6 s and 1.8 s of 12, and 2.4 s were left by children that are gone.
    let sample = capture_step(&mut engine, 2, 90, capture(12, 1002), procs(3.6, 1.8, 2.4));
    let breakdown = sample.cpu_breakdown.unwrap();
    near(f64::from(breakdown.usage), 0.9);
    near(f64::from(breakdown.readable), 0.45);
    near(f64::from(breakdown.finished), 0.2);
    near(f64::from(breakdown.other), 0.25);
    assert_eq!(breakdown.active_count, 2);
    near(
        f64::from(breakdown.readable) + f64::from(breakdown.finished) + f64::from(breakdown.other),
        f64::from(breakdown.usage),
    );
    // The same kind of figures against a machine that was only 40% busy overshoot it (sampling skew): nothing is
    // left over, and nothing is shown above what the host measured as the busy share.
    let mut engine = Engine::new(system(), frame(0));
    capture_step(&mut engine, 1, 40, capture(10, 1000), procs(0.0, 0.0, 0.0));
    let skew = capture_step(&mut engine, 2, 40, capture(12, 1002), procs(7.2, 3.6, 4.8));
    let breakdown = skew.cpu_breakdown.unwrap();
    near(f64::from(breakdown.usage), 0.4);
    assert_eq!(f64::from(breakdown.other), 0.0);
}
#[test]
fn views_hiding_keeps_the_cpu_baseline_for_a_quick_reopen_but_not_past_the_window_limit() {
    let mut engine = Engine::new(system(), frame(0));
    capture_step(
        &mut engine,
        1,
        50,
        capture(10, 1000),
        vec![live(7, 1, 500, 10.0, None)],
    );
    capture_step(
        &mut engine,
        2,
        50,
        capture(12, 1002),
        vec![live(7, 1, 500, 11.0, None)],
    );
    // Hidden for a moment: a sample without processes.
    let mut hidden = frame(3);
    hidden.cpu = Some(host(3, 50));
    engine.sample(hidden, &m1_modules(), false, 14_000, 0);
    // Reopened five seconds after the last capture: measured against it at once.
    let back = capture_step(
        &mut engine,
        4,
        50,
        capture(17, 1007),
        vec![live(7, 1, 500, 14.0, None)],
    );
    near(cpu_of(&back, 7), 3.0 / (5.0 * 6.0));
    assert_eq!(engine.process_interval_ms(), 2000);
    let mut hidden = frame(5);
    hidden.cpu = Some(host(5, 50));
    engine.sample(hidden, &m1_modules(), false, 40_000, 0);
    let late = capture_step(
        &mut engine,
        6,
        50,
        capture(60, 1050),
        vec![live(7, 1, 500, 20.0, None)],
    );
    assert_eq!(cpu_of(&late, 7), 0.0);
    assert_eq!(engine.process_interval_ms(), 900);
}
fn grouped(pid: u32, name: &str, key: &str, label: &str, cpu_s: f64, memory: u64) -> RawProcess {
    let mut raw = live(pid, 1, 100, cpu_s, None);
    raw.info.name = name.into();
    raw.info.memory_bytes = memory;
    raw.group = Group {
        key: key.into(),
        label: label.into(),
    };
    raw
}
#[test]
fn processes_merge_by_app_bundle_or_executable_with_the_main_process_leading_an_app() {
    let mut engine = Engine::new(system(), frame(0));
    let chrome = "app:/Applications/Google Chrome.app";
    let all = |bump: f64| {
        let mut main = grouped(
            10,
            "Google Chrome",
            chrome,
            "Google Chrome",
            5.0 + bump,
            500,
        );
        main.info.kind = ProcessKind::App;
        main.info.bundle_id = Some("com.google.Chrome".into());
        main.info.quittable = true;
        let mut renderer = grouped(
            11,
            "Google Chrome Helper (Renderer)",
            chrome,
            "Google Chrome",
            8.0 + 3.0 * bump,
            700,
        );
        renderer.info.kind = ProcessKind::Background;
        renderer.info.bundle_id = None;
        renderer.info.quittable = false;
        let mut compile_a = grouped(20, "compile", "exe:compile", "compile", 1.0 + bump, 40);
        compile_a.info.kind = ProcessKind::Background;
        compile_a.info.quittable = false;
        let mut compile_b = grouped(
            21,
            "compile",
            "exe:compile",
            "compile",
            1.0 + 2.0 * bump,
            60,
        );
        compile_b.info.kind = ProcessKind::Background;
        compile_b.info.quittable = false;
        let mut mine = grouped(
            30,
            "OpenNavo",
            "app:/Applications/OpenNavo.app",
            "OpenNavo",
            2.0 + bump,
            90,
        );
        mine.info.is_self = true;
        mine.info.quittable = false;
        vec![main, renderer, compile_a, compile_b, mine]
    };
    capture_step(&mut engine, 1, 90, capture(10, 1000), all(0.0));
    let sample = capture_step(&mut engine, 2, 90, capture(12, 1002), all(1.2));
    // Every process is still listed on its own, OpenNavo's included.
    assert_eq!(sample.processes.as_ref().unwrap().len(), 5);
    let groups = sample.process_groups.unwrap();
    assert_eq!(groups.len(), 3);
    let chrome = groups.iter().find(|p| p.name == "Google Chrome").unwrap();
    assert_eq!(chrome.count, 2);
    // The main process leads: its pid, its name, and it alone decides whether the app can be quit.
    assert_eq!(chrome.pid, 10);
    assert!(chrome.quittable);
    assert_eq!(chrome.kind, ProcessKind::App);
    assert_eq!(chrome.memory_bytes, 1200);
    near(f64::from(chrome.cpu), (1.2 + 3.6) / 12.0);
    let compile = groups.iter().find(|p| p.name == "compile").unwrap();
    assert_eq!(compile.count, 2);
    assert_eq!(compile.kind, ProcessKind::Background);
    assert!(!compile.quittable);
    assert_eq!(compile.memory_bytes, 100);
    near(f64::from(compile.cpu), (1.2 + 2.4) / 12.0);
    let mine = groups.iter().find(|p| p.pid == 30).unwrap();
    assert!(mine.is_self && !mine.quittable && mine.count == 1);
    // Ranked by CPU like the plain list; an executable without a main process is led by its heaviest member.
    let order: Vec<_> = groups.iter().map(|p| p.pid).collect();
    assert_eq!(order, [10, 21, 30]);
}
#[test]
fn a_busy_machine_is_never_blamed_on_opennavo_itself() {
    let mut tracker = opennavo_desktop_lib::monitor::health::HealthTracker::default();
    let mut sample = opennavo_desktop_lib::monitor::engine::empty_sample(&system());
    sample.cpu.usage = ratio(0.95);
    let mut me = process(2, 0.7, 10);
    me.is_self = true;
    sample.processes = Some(vec![process(1, 0.2, 10), me]);
    let modules = [MonitorModule::Cpu].into_iter().collect();
    tracker.evaluate(&sample, &modules, 0, 0);
    let health = tracker.evaluate(&sample, &modules, 180_000, 0);
    let busy = health
        .issues
        .iter()
        .find(|issue| issue.key == HealthIssueKey::CpuBusy)
        .unwrap();
    assert_eq!(busy.top_pid, Some(1));
}
#[test]
fn a_parent_slow_to_wait_for_an_ended_child_is_credited_when_it_does_and_nothing_is_counted_twice()
{
    let mut engine = Engine::new(system(), frame(0));
    let mut go = live(100, 1, 100, 10.0, Some(0.0));
    go.info.name = "go".into();
    capture_step(
        &mut engine,
        1,
        90,
        capture(10, 1000),
        vec![go.clone(), live(101, 100, 100, 5.0, Some(0.0))],
    );
    go.cpu_ns = secs(10.2);
    let second = capture_step(
        &mut engine,
        2,
        90,
        capture(12, 1002),
        vec![go.clone(), live(101, 100, 100, 7.0, Some(0.0))],
    );
    near(cpu_of(&second, 101), 2.0 / 12.0);
    // The child ends, but its parent has not waited for it yet: it can no longer be listed, and it is not in the
    // parent's child totals either, so nothing is credited yet.
    let gap = capture_step(&mut engine, 3, 90, capture(14, 1004), vec![go.clone()]);
    near(f64::from(gap.cpu_breakdown.unwrap().finished), 0.0);
    // The parent waits for it a window later: 8.5 s in all, of which 7 s were counted while it was alive.
    go.child_cpu_ns = Some(secs(8.5));
    let later = capture_step(&mut engine, 4, 90, capture(16, 1006), vec![go.clone()]);
    near(f64::from(later.cpu_breakdown.unwrap().finished), 1.5 / 12.0);
    // Settled: the deduction is spent and a later child is credited in full.
    go.child_cpu_ns = Some(secs(9.5));
    let next = capture_step(&mut engine, 5, 90, capture(18, 1008), vec![go]);
    near(f64::from(next.cpu_breakdown.unwrap().finished), 1.0 / 12.0);
}
/// A tree that works for a while and then ends all at once, as a build tool and the processes it started do: the
/// grandparent is the one that finally collects everything, and what was counted while the tree was alive is not new.
#[test]
fn parent_and_child_ending_in_the_same_window_hand_the_whole_deduction_to_the_one_that_collects_them()
 {
    let mut engine = Engine::new(system(), frame(0));
    let mut tool = live(100, 1, 100, 10.0, Some(0.0));
    tool.info.name = "tool".into();
    let mut child = live(101, 100, 100, 1.0, Some(0.0));
    let mut grandchild = live(102, 101, 100, 1.0, None);
    capture_step(
        &mut engine,
        1,
        90,
        capture(10, 1000),
        vec![tool.clone(), child.clone(), grandchild.clone()],
    );
    // Both work for a while: 1 s and 2 s, listed against themselves.
    child.cpu_ns = secs(2.0);
    grandchild.cpu_ns = secs(3.0);
    let second = capture_step(
        &mut engine,
        2,
        90,
        capture(12, 1002),
        vec![tool.clone(), child.clone(), grandchild.clone()],
    );
    near(cpu_of(&second, 101), 1.0 / 12.0);
    near(cpu_of(&second, 102), 2.0 / 12.0);
    // Then both end without using anything more, the child having waited for the grandchild and the tool for the
    // child. The tool's child totals grow by what the two ever used, 2 s and 3 s, all of it counted before.
    tool.child_cpu_ns = Some(secs(5.0));
    let third = capture_step(&mut engine, 3, 90, capture(14, 1004), vec![tool.clone()]);
    let breakdown = third.cpu_breakdown.clone().unwrap();
    near(f64::from(breakdown.finished), 0.0);
    assert!(breakdown.finished_from.is_empty());
    // Nothing is left over to be taken off a later child either: a new one is credited in full.
    tool.child_cpu_ns = Some(secs(6.0));
    let fourth = capture_step(&mut engine, 4, 90, capture(16, 1006), vec![tool]);
    near(
        f64::from(fourth.cpu_breakdown.unwrap().finished),
        1.0 / 12.0,
    );
}

#[test]
fn a_whole_ended_chain_and_its_siblings_are_deducted_from_the_ancestor_that_is_still_there() {
    let mut engine = Engine::new(system(), frame(0));
    let mut root = live(100, 1, 100, 10.0, Some(0.0));
    root.info.name = "root".into();
    // Two chains below the root: 101 -> 102 -> 103, and 104 -> 105.
    let mut chain = vec![
        live(101, 100, 100, 1.0, Some(0.0)),
        live(102, 101, 100, 1.0, Some(0.0)),
        live(103, 102, 100, 1.0, None),
        live(104, 100, 100, 1.0, Some(0.0)),
        live(105, 104, 100, 1.0, None),
    ];
    let mut all = vec![root.clone()];
    all.extend(chain.clone());
    capture_step(&mut engine, 1, 90, capture(10, 1000), all);
    for process in &mut chain {
        process.cpu_ns += secs(1.0);
    }
    let mut all = vec![root.clone()];
    all.extend(chain.clone());
    capture_step(&mut engine, 2, 90, capture(12, 1002), all);
    // Everything ends. Each used 2 s in all, and 1.5 s more that no capture saw, in the last window, on 103 and 105.
    // The root's child totals: the two chains' 5 processes at 2 s each, plus the unseen 3 s.
    root.child_cpu_ns = Some(secs(10.0 + 3.0));
    let third = capture_step(&mut engine, 3, 90, capture(14, 1004), vec![root.clone()]);
    let breakdown = third.cpu_breakdown.unwrap();
    near(f64::from(breakdown.finished), 3.0 / 12.0);
    assert_eq!(breakdown.finished_from[0].pid, 100);
}

#[test]
fn a_child_that_ended_while_its_parent_was_busy_is_still_deducted_from_the_parent_that_collects_it_later()
 {
    let mut engine = Engine::new(system(), frame(0));
    let mut tool = live(100, 1, 100, 10.0, Some(0.0));
    tool.info.name = "tool".into();
    let mut child = live(101, 100, 100, 1.0, Some(0.0));
    let grandchild = live(102, 101, 100, 1.0, None);
    capture_step(
        &mut engine,
        1,
        90,
        capture(10, 1000),
        vec![tool.clone(), child.clone(), grandchild.clone()],
    );
    // The grandchild ends: its parent (the child) is slow to wait for it, so for now nothing is credited.
    child.cpu_ns = secs(2.0);
    let gap = capture_step(
        &mut engine,
        2,
        90,
        capture(12, 1002),
        vec![tool.clone(), child.clone()],
    );
    near(f64::from(gap.cpu_breakdown.unwrap().finished), 0.0);
    // The child waits for it and ends too; the tool collects the child. Both were counted for before (2 s and 1 s
    // as of the last capture, the grandchild's 1 s still owed to the child), so only the 1.5 s used since is new.
    tool.child_cpu_ns = Some(secs(2.0 + 1.0 + 1.5));
    let third = capture_step(&mut engine, 3, 90, capture(14, 1004), vec![tool]);
    near(f64::from(third.cpu_breakdown.unwrap().finished), 1.5 / 12.0);
}

/// A parent that ends without waiting for its child leaves the child to launchd, which collects it: what the child used
/// never reaches the grandparent, so it cannot be taken off what the grandparent did collect.
#[test]
fn a_child_orphaned_by_a_parent_that_did_not_wait_is_not_deducted_from_the_grandparent() {
    let mut engine = Engine::new(system(), frame(0));
    let mut tool = live(100, 1, 100, 10.0, Some(0.0));
    tool.info.name = "tool".into();
    let mut parent = live(101, 100, 100, 1.0, Some(0.0));
    let mut orphan = live(102, 101, 100, 1.0, None);
    capture_step(
        &mut engine,
        1,
        90,
        capture(10, 1000),
        vec![tool.clone(), parent.clone(), orphan.clone()],
    );
    parent.cpu_ns = secs(2.0);
    orphan.cpu_ns = secs(3.0);
    capture_step(
        &mut engine,
        2,
        90,
        capture(12, 1002),
        vec![tool.clone(), parent.clone(), orphan.clone()],
    );
    // Both end. The parent never waited, so the tool collects only the parent: 3 s in all, of which 2 s were counted
    // while it was alive. The orphan's 3 s (and what it used since) went to launchd, not to the tool.
    tool.child_cpu_ns = Some(secs(3.0));
    let third = capture_step(&mut engine, 3, 90, capture(14, 1004), vec![tool.clone()]);
    let breakdown = third.cpu_breakdown.clone().unwrap();
    near(f64::from(breakdown.finished), 1.0 / 12.0);
    assert_eq!(breakdown.finished_from[0].pid, 100);
    // And no deduction is left to eat into a later, unrelated child.
    tool.child_cpu_ns = Some(secs(4.0));
    let fourth = capture_step(&mut engine, 4, 90, capture(16, 1006), vec![tool]);
    near(
        f64::from(fourth.cpu_breakdown.unwrap().finished),
        1.0 / 12.0,
    );
}

#[test]
fn an_orphaned_chain_below_a_parent_that_did_not_wait_is_not_deducted_either() {
    let mut engine = Engine::new(system(), frame(0));
    let mut tool = live(100, 1, 100, 10.0, Some(0.0));
    tool.info.name = "tool".into();
    let mut parent = live(101, 100, 100, 1.0, Some(0.0));
    let mut child = live(102, 101, 100, 1.0, Some(0.0));
    let mut grandchild = live(103, 102, 100, 1.0, None);
    capture_step(
        &mut engine,
        1,
        90,
        capture(10, 1000),
        vec![
            tool.clone(),
            parent.clone(),
            child.clone(),
            grandchild.clone(),
        ],
    );
    parent.cpu_ns = secs(2.0);
    child.cpu_ns = secs(2.0);
    grandchild.cpu_ns = secs(2.0);
    capture_step(
        &mut engine,
        2,
        90,
        capture(12, 1002),
        vec![tool.clone(), parent, child, grandchild],
    );
    // The child waited for the grandchild, but the parent did not wait for the child: the tool collects the parent
    // alone, which used 0.5 s more than was counted.
    tool.child_cpu_ns = Some(secs(2.5));
    let third = capture_step(&mut engine, 3, 90, capture(14, 1004), vec![tool]);
    near(f64::from(third.cpu_breakdown.unwrap().finished), 0.5 / 12.0);
}

/// Nothing in the process list says whether a parent waited for its ended child or left it to launchd, so a collection
/// that is big enough to have held the child is never taken as proof that it did: what cannot be told stays out of the
/// finished share (it falls to "other"), and the share never claims CPU that may have been counted already.
#[test]
fn a_collection_big_enough_to_hold_a_child_does_not_prove_that_the_child_was_collected() {
    let mut engine = Engine::new(system(), frame(0));
    let mut tool = live(100, 1, 100, 10.0, Some(0.0));
    tool.info.name = "tool".into();
    let mut parent = live(101, 100, 100, 1.0, Some(0.0));
    let mut orphan = live(102, 101, 100, 0.5, None);
    capture_step(
        &mut engine,
        1,
        90,
        capture(10, 1000),
        vec![tool.clone(), parent.clone(), orphan.clone()],
    );
    parent.cpu_ns = secs(2.0);
    orphan.cpu_ns = secs(1.0);
    capture_step(
        &mut engine,
        2,
        90,
        capture(12, 1002),
        vec![tool.clone(), parent.clone(), orphan.clone()],
    );
    // Both end. The parent did not wait: the tool collects the parent alone, 3.5 s in all, 1.5 s of it used since the
    // last capture. The orphan's 1 s went to launchd. A collection of 3.5 s could also have held both (2 s + 1 s counted
    // and 0.5 s more), and nothing tells the two apart: only the 0.5 s that is certain is claimed, the rest is left alone.
    tool.child_cpu_ns = Some(secs(3.5));
    let third = capture_step(&mut engine, 3, 90, capture(14, 1004), vec![tool.clone()]);
    let breakdown = third.cpu_breakdown.clone().unwrap();
    near(f64::from(breakdown.finished), 0.5 / 12.0);
    // What was not claimed is not lost: it is part of what the other parts leave over.
    near(
        f64::from(breakdown.finished) + f64::from(breakdown.readable) + f64::from(breakdown.other),
        f64::from(breakdown.usage),
    );
    // And nothing is left to eat into a later, unrelated child.
    tool.child_cpu_ns = Some(secs(4.5));
    let fourth = capture_step(&mut engine, 4, 90, capture(16, 1006), vec![tool]);
    near(
        f64::from(fourth.cpu_breakdown.unwrap().finished),
        1.0 / 12.0,
    );
}

#[test]
fn of_two_ended_subtrees_one_waited_for_and_one_not_nothing_is_claimed_twice() {
    let mut engine = Engine::new(system(), frame(0));
    let mut tool = live(100, 1, 100, 10.0, Some(0.0));
    tool.info.name = "tool".into();
    let mut tree = vec![
        // 101 waited for 102; 103 did not wait for 104, which went to launchd.
        live(101, 100, 100, 1.0, Some(0.0)),
        live(102, 101, 100, 1.0, None),
        live(103, 100, 100, 1.0, Some(0.0)),
        live(104, 103, 100, 1.0, None),
    ];
    let mut all = vec![tool.clone()];
    all.extend(tree.clone());
    capture_step(&mut engine, 1, 90, capture(10, 1000), all);
    for process in &mut tree {
        process.cpu_ns += secs(1.0);
    }
    let mut all = vec![tool.clone()];
    all.extend(tree);
    capture_step(&mut engine, 2, 90, capture(12, 1002), all);
    // All four end without using anything more. The tool collects 101 with 102, and 103 alone: 2 s each, 6 s in all,
    // every second of it counted before. 104's 2 s were not collected by anyone readable.
    tool.child_cpu_ns = Some(secs(6.0));
    let third = capture_step(&mut engine, 3, 90, capture(14, 1004), vec![tool]);
    near(f64::from(third.cpu_breakdown.unwrap().finished), 0.0);
}

/// A count larger than everything the parent collected is the one thing that proves the child was not collected, so a
/// collection that cannot have held it leaves it for the collection that can.
#[test]
fn a_child_counted_for_more_than_the_parent_collected_is_not_taken_off_a_smaller_collection() {
    let mut engine = Engine::new(system(), frame(0));
    let mut tool = live(100, 1, 100, 10.0, Some(0.0));
    tool.info.name = "tool".into();
    let mut child = live(101, 100, 100, 1.0, Some(0.0));
    capture_step(
        &mut engine,
        1,
        90,
        capture(10, 1000),
        vec![tool.clone(), child.clone()],
    );
    child.cpu_ns = secs(3.0);
    capture_step(
        &mut engine,
        2,
        90,
        capture(12, 1002),
        vec![tool.clone(), child],
    );
    // The child ends but is not waited for. The tool collects 1 s from a process that started and ended within the
    // window: far less than the 3 s counted for the child, which cannot be inside it.
    tool.child_cpu_ns = Some(secs(1.0));
    let gap = capture_step(&mut engine, 3, 90, capture(14, 1004), vec![tool.clone()]);
    near(f64::from(gap.cpu_breakdown.unwrap().finished), 1.0 / 12.0);
    // When the tool does wait for the child, 3.5 s in all, only the 0.5 s since the last capture is new.
    tool.child_cpu_ns = Some(secs(4.5));
    let later = capture_step(&mut engine, 4, 90, capture(16, 1006), vec![tool]);
    near(f64::from(later.cpu_breakdown.unwrap().finished), 0.5 / 12.0);
}

/// The kernel adds a collected child to its parent's totals in microseconds, so the parent can be credited with a few
/// microseconds less than the nanosecond readings counted for the child.
#[test]
fn a_few_microseconds_of_rounding_in_the_parents_totals_do_not_make_a_collected_child_look_uncollected()
 {
    let mut engine = Engine::new(system(), frame(0));
    let mut tool = live(100, 1, 100, 10.0, Some(0.0));
    tool.info.name = "tool".into();
    let mut child = live(101, 100, 100, 1.0, Some(0.0));
    let mut grandchild = live(102, 101, 100, 1.0, None);
    capture_step(
        &mut engine,
        1,
        90,
        capture(10, 1000),
        vec![tool.clone(), child.clone(), grandchild.clone()],
    );
    child.cpu_ns = secs(2.0) + 999;
    grandchild.cpu_ns = secs(3.0) + 999;
    capture_step(
        &mut engine,
        2,
        90,
        capture(12, 1002),
        vec![tool.clone(), child, grandchild],
    );
    // Both ended without using anything more; the tool was credited with 5 s less 3 µs.
    tool.child_cpu_ns = Some(secs(5.0) - 1_000);
    let third = capture_step(&mut engine, 3, 90, capture(14, 1004), vec![tool]);
    near(f64::from(third.cpu_breakdown.unwrap().finished), 0.0);
}

/// A small deterministic generator, so a failing case can be replayed from its seed.
struct Dice(u64);
impl Dice {
    fn roll(&mut self, sides: u64) -> u64 {
        self.0 = self
            .0
            .wrapping_mul(6_364_136_223_846_793_005)
            .wrapping_add(1_442_695_040_888_963_407);
        (self.0 >> 33) % sides
    }
}
/// One process of a generated tree that ends within the window. CPU is in quarter seconds, so every sum is exact.
#[derive(Debug)]
struct Doomed {
    /// Its parent in the tree; `None` for a child of the collector.
    parent: Option<usize>,
    /// Counted while it was alive: its own CPU and that of the children it had collected by then.
    own: u64,
    kids: u64,
    /// Used after the last capture, before it ended.
    more: u64,
    /// Whether its parent waited for it (a child of the collector: whether the collector did).
    waited: bool,
}
fn quarters(count: u64) -> f64 {
    count as f64 / 4.0
}
fn random_tree(dice: &mut Dice, flat: bool) -> Vec<Doomed> {
    let size = 1 + dice.roll(6) as usize;
    (0..size)
        .map(|index| Doomed {
            parent: (!flat && index > 0 && dice.roll(3) > 0)
                .then(|| dice.roll(index as u64) as usize),
            own: dice.roll(5),
            kids: dice.roll(3),
            more: dice.roll(4),
            waited: dice.roll(10) < 7,
        })
        .collect()
}
/// Whether the collector's child totals gain the process: it was waited for, and so was every ended parent above it.
fn collected(tree: &[Doomed], index: usize) -> bool {
    tree[index].waited
        && tree[index]
            .parent
            .is_none_or(|parent| collected(tree, parent))
}
/// What a process takes with it when its parent waits for it: all it and the children it waited for used.
fn total(tree: &[Doomed], index: usize) -> u64 {
    let node = &tree[index];
    node.own
        + node.kids
        + node.more
        + (0..tree.len())
            .filter(|child| tree[*child].parent == Some(index) && tree[*child].waited)
            .map(|child| total(tree, child))
            .sum::<u64>()
}
/// Runs a tree through the engine: it is seen once alive below a tool, then everything in it ends and the tool's child
/// totals are credited with `credit` quarter seconds in each of the following windows. Returns the finished CPU in
/// seconds each window reports.
fn finished_after_the_tree_ends(tree: &[Doomed], credit: &[u64]) -> Vec<f64> {
    let mut engine = Engine::new(system(), frame(0));
    let mut tool = live(100, 1, 100, 10.0, Some(0.0));
    tool.info.name = "tool".into();
    let mut alive = vec![tool.clone()];
    alive.extend(tree.iter().enumerate().map(|(index, node)| {
        let ppid = node.parent.map_or(100, |parent| 101 + parent as u32);
        live(
            101 + index as u32,
            ppid,
            100,
            quarters(node.own),
            Some(quarters(node.kids)),
        )
    }));
    capture_step(&mut engine, 1, 90, capture(10, 1000), alive);
    let mut credited = 0;
    credit
        .iter()
        .enumerate()
        .map(|(window, gained)| {
            credited += gained;
            tool.child_cpu_ns = Some(secs(quarters(credited)));
            let step = 2 + window as u32;
            let at = 10 + 2 * (window as u64 + 1);
            let sample = capture_step(
                &mut engine,
                step,
                90,
                capture(at, 1000 + at - 10),
                vec![tool.clone()],
            );
            f64::from(sample.cpu_breakdown.unwrap().finished) * 12.0
        })
        .collect()
}
/// Nothing says which of the processes that ended were waited for, so the finished CPU may fall short of what really
/// is new, but it is never more than that: no CPU is counted twice. And it falls short by no more than was counted
/// for processes whose CPU never reached the collector.
#[test]
fn finished_cpu_is_never_more_than_the_new_cpu_however_the_tree_was_waited_for() {
    let mut dice = Dice(7);
    let (mut exact, mut short) = (0, 0);
    for case in 0..400 {
        let tree = random_tree(&mut dice, false);
        let unseen = dice.roll(5);
        let reached: u64 = (0..tree.len())
            .filter(|index| collected(&tree, *index))
            .map(|index| tree[index].own + tree[index].kids + tree[index].more)
            .sum();
        let new: u64 = (0..tree.len())
            .filter(|index| collected(&tree, *index))
            .map(|index| tree[index].more)
            .sum::<u64>()
            + unseen;
        let lost: u64 = (0..tree.len())
            .filter(|index| !collected(&tree, *index))
            .map(|index| tree[index].own + tree[index].kids)
            .sum();
        // The tool waits for the roots that were waited for, taking everything they waited for with them.
        let roots: u64 = (0..tree.len())
            .filter(|index| tree[*index].parent.is_none() && tree[*index].waited)
            .map(|index| total(&tree, index))
            .sum();
        assert_eq!(roots + unseen, reached + unseen, "case {case}");
        let found = finished_after_the_tree_ends(&tree, &[roots + unseen])[0];
        assert!(
            found <= quarters(new) + 1e-6,
            "case {case}: {found} s counted twice, only {} s new",
            quarters(new)
        );
        assert!(
            found + quarters(lost) + 1e-6 >= quarters(new),
            "case {case}: {found} s is short of {} s by more than the {} s that never reached the tool",
            quarters(new),
            quarters(lost)
        );
        if (found - quarters(new)).abs() < 1e-6 {
            exact += 1;
        } else {
            short += 1;
        }
    }
    // Not vacuous: a good share of the cases is told exactly, and some are not.
    assert!(exact > 100 && short > 20, "exact {exact}, short {short}");
}

/// Children a slow parent waits for in a later window are taken off whichever window collects them, so over the windows
/// nothing is counted twice and, when every child is waited for, nothing is missed.
#[test]
fn children_collected_in_later_windows_are_credited_exactly_over_the_windows() {
    let mut dice = Dice(11);
    for case in 0..300 {
        let mut tree = random_tree(&mut dice, true);
        for node in &mut tree {
            node.waited = true;
        }
        // Each child is collected in the first window or the second, never both.
        let later: Vec<bool> = tree.iter().map(|_| dice.roll(2) == 0).collect();
        let credit = |second: bool| -> u64 {
            (0..tree.len())
                .filter(|index| later[*index] == second)
                .map(|index| total(&tree, index))
                .sum()
        };
        let new: u64 = tree.iter().map(|node| node.more).sum();
        let found = finished_after_the_tree_ends(&tree, &[credit(false), credit(true)]);
        let sum: f64 = found.iter().sum();
        assert!(
            (sum - quarters(new)).abs() < 1e-6,
            "case {case}: {sum} s over the windows, {} s new ({found:?})",
            quarters(new)
        );
    }
}

#[test]
fn a_reading_the_kernel_would_not_give_does_not_turn_the_next_one_into_the_processs_whole_life() {
    let mut engine = Engine::new(system(), frame(0));
    capture_step(
        &mut engine,
        1,
        50,
        capture(10, 1000),
        vec![live(7, 1, 500, 100.0, None)],
    );
    // Unreadable this time: no CPU is claimed for it.
    let blind = capture_step(
        &mut engine,
        2,
        50,
        capture(12, 1002),
        vec![live(7, 1, 500, 0.0, None)],
    );
    assert_eq!(cpu_of(&blind, 7), 0.0);
    // Readable again: only what it used since the last good reading, not the hundred seconds before.
    let back = capture_step(
        &mut engine,
        3,
        50,
        capture(14, 1004),
        vec![live(7, 1, 500, 103.0, None)],
    );
    near(cpu_of(&back, 7), 3.0 / 12.0);
}

#[test]
fn ambiguous_collection_preserves_unspent_subtree_deductions() {
    let tree = vec![
        Doomed {
            parent: None,
            own: 2,
            kids: 0,
            more: 0,
            waited: true,
        },
        Doomed {
            parent: Some(0),
            own: 4,
            kids: 0,
            more: 0,
            waited: true,
        },
    ];
    // An unseen sibling contributes 0.75 s first. That fits the root's 0.5 s,
    // but does not prove it was collected. The actual 1.5 s tree arrives later.
    let found = finished_after_the_tree_ends(&tree, &[3, 6, 4]);
    near(found[0] + found[1], 0.75);
    // Every deduction has now been spent; subsequent new work counts in full.
    near(found[2], 1.0);
}

#[test]
fn delayed_collection_keeps_history_while_parent_is_sampled() {
    let tree = vec![Doomed {
        parent: None,
        own: 20,
        kids: 0,
        more: 0,
        waited: true,
    }];
    // Two-second captures continue for 18 seconds after the child disappears.
    let found = finished_after_the_tree_ends(&tree, &[0, 0, 0, 0, 0, 0, 0, 0, 0, 20, 4]);
    for value in &found[..10] {
        near(*value, 0.0);
    }
    near(found[10], 1.0);
}

#[test]
fn ambiguous_collections_across_windows_never_exceed_new_cpu() {
    let mut dice = Dice(29);
    for case in 0..400 {
        let tree = random_tree(&mut dice, false);
        let unseen = dice.roll(12);
        let mut credit = vec![unseen, 0, 0];
        for index in 0..tree.len() {
            if tree[index].parent.is_none() && tree[index].waited {
                credit[1 + dice.roll(2) as usize] += total(&tree, index);
            }
        }
        let new = unseen
            + (0..tree.len())
                .filter(|index| collected(&tree, *index))
                .map(|index| tree[index].more)
                .sum::<u64>();
        let lost: u64 = (0..tree.len())
            .filter(|index| !collected(&tree, *index))
            .map(|index| tree[index].own + tree[index].kids)
            .sum();
        let found = finished_after_the_tree_ends(&tree, &credit);
        let sum: f64 = found.iter().sum();
        assert!(
            sum <= quarters(new) + 1e-6,
            "case {case}: {tree:?}, credit {credit:?}, {found:?}, only {} s new",
            quarters(new)
        );
        assert!(
            sum + quarters(lost) + 1e-6 >= quarters(new),
            "case {case}: {found:?} deducted more than the uncollected history"
        );
    }
}

#[test]
fn partially_spent_deductions_remain_usable_after_transfer_to_an_ancestor() {
    for (small, large) in [(1.0, 2.0), (2.0, 3.0), (0.25, 4.0)] {
        let mut engine = Engine::new(system(), frame(0));
        let mut ancestor = live(100, 1, 100, 10.0, Some(0.0));
        let mut parent = live(101, 100, 100, 2.0, Some(0.0));
        capture_step(
            &mut engine,
            1,
            90,
            capture(10, 1000),
            vec![
                ancestor.clone(),
                parent.clone(),
                live(102, 101, 100, small, Some(0.0)),
                live(103, 101, 100, large, Some(0.0)),
            ],
        );
        // Both children disappear. The parent collects the larger child, but
        // settlement spends the smaller child's deduction first, leaving a
        // partial balance on the larger candidate. Neither child used new CPU.
        parent.child_cpu_ns = Some(secs(large));
        let first = capture_step(
            &mut engine,
            2,
            90,
            capture(12, 1002),
            vec![ancestor.clone(), parent],
        );
        near(f64::from(first.cpu_breakdown.unwrap().finished), 0.0);
        // The parent collects the other child and exits. Its sampled total
        // already includes the earlier collection, so only the unspent balance
        // may be added to the eligibility threshold when passing it upward.
        ancestor.child_cpu_ns = Some(secs(2.0 + small + large));
        let second = capture_step(
            &mut engine,
            3,
            90,
            capture(14, 1004),
            vec![ancestor.clone()],
        );
        near(f64::from(second.cpu_breakdown.unwrap().finished), 0.0);
        ancestor.child_cpu_ns = Some(secs(3.0 + small + large));
        let third = capture_step(&mut engine, 4, 90, capture(16, 1006), vec![ancestor]);
        near(f64::from(third.cpu_breakdown.unwrap().finished), 1.0 / 12.0);
    }
}

#[test]
fn ambiguous_pending_balance_can_cover_multiple_smaller_collections() {
    let tree: Vec<_> = [4, 4, 8]
        .into_iter()
        .map(|own| Doomed {
            parent: None,
            own,
            kids: 0,
            more: 0,
            waited: true,
        })
        .collect();
    let found = finished_after_the_tree_ends(&tree, &[8, 4, 4, 4]);
    for value in &found[..3] {
        near(*value, 0.0);
    }
    near(found[3], 1.0);
}

#[test]
fn ambiguous_pending_eligibility_survives_transfer_to_an_ancestor() {
    let mut engine = Engine::new(system(), frame(0));
    let mut ancestor = live(100, 1, 100, 10.0, Some(0.0));
    let mut parent = live(101, 100, 100, 2.0, Some(0.0));
    capture_step(
        &mut engine,
        1,
        90,
        capture(10, 1000),
        vec![
            ancestor.clone(),
            parent.clone(),
            live(102, 101, 100, 1.0, Some(0.0)),
            live(103, 101, 100, 1.0, Some(0.0)),
            live(104, 101, 100, 2.0, Some(0.0)),
        ],
    );
    // Collect the 2 s child. The engine spends the two 1 s candidates instead.
    parent.child_cpu_ns = Some(secs(2.0));
    let first = capture_step(
        &mut engine,
        2,
        90,
        capture(12, 1002),
        vec![ancestor.clone(), parent],
    );
    near(f64::from(first.cpu_breakdown.unwrap().finished), 0.0);
    // Collect one 1 s child and exit, leaving the other to launchd. The ancestor
    // receives 5 s of entirely historical CPU, including the parent's own 2 s.
    ancestor.child_cpu_ns = Some(secs(5.0));
    let second = capture_step(&mut engine, 3, 90, capture(14, 1004), vec![ancestor]);
    near(f64::from(second.cpu_breakdown.unwrap().finished), 0.0);
}

#[test]
fn ambiguous_pending_eligibility_is_independent_of_collection_order() {
    let orders = [
        [0, 1, 2],
        [0, 2, 1],
        [1, 0, 2],
        [1, 2, 0],
        [2, 0, 1],
        [2, 1, 0],
    ];
    for a in 1..=3 {
        for b in 1..=3 {
            for c in 1..=3 {
                let sizes = [a, b, c];
                for order in orders {
                    let tree: Vec<_> = sizes
                        .iter()
                        .map(|own| Doomed {
                            parent: None,
                            own: own * 4,
                            kids: 0,
                            more: 0,
                            waited: true,
                        })
                        .collect();
                    let credit = [
                        sizes[order[0]] * 4,
                        sizes[order[1]] * 4,
                        sizes[order[2]] * 4,
                        4,
                    ];
                    let found = finished_after_the_tree_ends(&tree, &credit);
                    assert!(
                        found[..3].iter().all(|value| value.abs() < 1e-6),
                        "sizes {sizes:?}, order {order:?}: {found:?}"
                    );
                    near(found[3], 1.0);
                    // After the first collection the parent exits, collecting
                    // zero, one, or both remaining children before doing so.
                    // Any uncollected children go to launchd, not the ancestor.
                    for remaining in 0..=2 {
                        let mut engine = Engine::new(system(), frame(0));
                        let mut ancestor = live(100, 1, 100, 10.0, Some(0.0));
                        let mut parent = live(101, 100, 100, 2.0, Some(0.0));
                        let mut alive = vec![ancestor.clone(), parent.clone()];
                        alive.extend(sizes.iter().enumerate().map(|(i, size)| {
                            live(102 + i as u32, 101, 100, *size as f64, Some(0.0))
                        }));
                        capture_step(&mut engine, 1, 90, capture(10, 1000), alive);
                        parent.child_cpu_ns = Some(secs(sizes[order[0]] as f64));
                        let first = capture_step(
                            &mut engine,
                            2,
                            90,
                            capture(12, 1002),
                            vec![ancestor.clone(), parent],
                        );
                        near(f64::from(first.cpu_breakdown.unwrap().finished), 0.0);
                        let collected: u64 = order[..=remaining].iter().map(|i| sizes[*i]).sum();
                        ancestor.child_cpu_ns = Some(secs(2.0 + collected as f64));
                        let last =
                            capture_step(&mut engine, 3, 90, capture(14, 1004), vec![ancestor]);
                        let finished = f64::from(last.cpu_breakdown.unwrap().finished);
                        assert!(
                            finished.abs() < 1e-6,
                            "sizes {sizes:?}, order {order:?}, remaining {remaining}: {finished}"
                        );
                    }
                }
            }
        }
    }
}
