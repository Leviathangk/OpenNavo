use super::{collect::*, health::HealthTracker, types::*};
use std::collections::{BTreeMap, VecDeque};

/// Monotonic baselines are cleared on stop, suspension, interface/device changes and module removal.
#[derive(Default)]
pub struct Deltas {
    cpu: Option<Vec<[u32; 4]>>,
    disk: Option<(u64, String, u64, u64)>,
    network: Option<(u64, String, u64, u64)>,
    processes: BTreeMap<u32, (u64, u64, u64)>,
    process_disk: BTreeMap<u32, (u64, u64, u64, u64)>,
    process_net: BTreeMap<u32, (u64, u64, u64, u64)>,
}
fn rates(
    previous: &mut Option<(u64, String, u64, u64)>,
    now: u64,
    id: String,
    a: u64,
    b: u64,
) -> (f64, f64) {
    let next = (now, id, a, b);
    let result = previous
        .as_ref()
        .and_then(|(at, id, a, b)| {
            let dt = now.checked_sub(*at).filter(|d| *d > 0 && *d <= 15_000)? as f64 / 1000.0;
            if id != &next.1 {
                return None;
            }
            Some((
                next.2.checked_sub(*a)? as f64 / dt,
                next.3.checked_sub(*b)? as f64 / dt,
            ))
        })
        .unwrap_or((0.0, 0.0));
    *previous = Some(next);
    result
}
pub fn reorder_cores<T>(cores: &mut [T], system: &SystemInfo) {
    // Apple host order is E then P. The P count alone suffices even if the optional E sysctl failed.
    if let Some(p) = system.performance_cores
        && p as usize <= cores.len()
    {
        cores.rotate_right(p as usize);
    }
}
pub fn top_processes(items: Vec<ProcessInfo>) -> Vec<ProcessInfo> {
    top_processes_io(items, false, false)
}
pub fn top_processes_io(
    mut items: Vec<ProcessInfo>,
    disk: bool,
    network: bool,
) -> Vec<ProcessInfo> {
    let mut cpu: Vec<_> = (0..items.len()).collect();
    cpu.sort_by(|&a, &b| {
        f64::from(items[b].cpu)
            .total_cmp(&f64::from(items[a].cpu))
            .then(items[a].pid.cmp(&items[b].pid))
    });
    let mut memory: Vec<_> = (0..items.len()).collect();
    memory.sort_by(|&a, &b| {
        items[b]
            .memory_bytes
            .cmp(&items[a].memory_bytes)
            .then(items[a].pid.cmp(&items[b].pid))
    });
    let mut wanted: std::collections::BTreeSet<_> = cpu
        .into_iter()
        .take(20)
        .chain(memory.into_iter().take(20))
        .collect();
    for (enabled, disk) in [(disk, true), (network, false)] {
        if enabled {
            let score = |p: &ProcessInfo| {
                let (a, b) = if disk {
                    (p.disk_read_bytes_per_sec, p.disk_write_bytes_per_sec)
                } else {
                    (p.net_down_bytes_per_sec, p.net_up_bytes_per_sec)
                };
                a.map(f64::from).unwrap_or(0.0) + b.map(f64::from).unwrap_or(0.0)
            };
            let mut ranked: Vec<_> = (0..items.len())
                .filter(|&i| {
                    if disk {
                        items[i].disk_read_bytes_per_sec.is_some()
                            || items[i].disk_write_bytes_per_sec.is_some()
                    } else {
                        items[i].net_down_bytes_per_sec.is_some()
                            || items[i].net_up_bytes_per_sec.is_some()
                    }
                })
                .collect();
            ranked.sort_by(|&a, &b| {
                score(&items[b])
                    .total_cmp(&score(&items[a]))
                    .then(items[a].pid.cmp(&items[b].pid))
            });
            wanted.extend(ranked.into_iter().take(10));
        }
    }
    items = items
        .into_iter()
        .enumerate()
        .filter_map(|(i, p)| wanted.contains(&i).then_some(p))
        .collect();
    items.sort_by(|a, b| {
        f64::from(b.cpu)
            .total_cmp(&f64::from(a.cpu))
            .then(a.pid.cmp(&b.pid))
    });
    items
}
impl Deltas {
    pub fn reset(&mut self) {
        *self = Self::default();
    }
    pub fn retain_process_io(&mut self, disk: bool, network: bool) {
        if !disk {
            self.process_disk.clear();
        }
        if !network {
            self.process_net.clear();
        }
    }
    pub fn retain(&mut self, modules: &Modules, processes: bool) {
        if !modules.contains(&MonitorModule::Cpu) {
            self.cpu = None;
        }
        if !modules.contains(&MonitorModule::Disk) {
            self.disk = None;
        }
        if !modules.contains(&MonitorModule::Network) {
            self.network = None;
        }
        if !processes {
            self.processes.clear();
            self.process_disk.clear();
            self.process_net.clear();
        }
    }
    fn cpu(&mut self, raw: RawCpu, system: &SystemInfo) -> CpuSample {
        let mut cores = Vec::with_capacity(raw.ticks.len());
        let mut totals = [0u64; 4];
        for (i, ticks) in raw.ticks.iter().enumerate() {
            let delta = self
                .cpu
                .as_ref()
                .and_then(|v| v.get(i))
                .map(|prev| {
                    let mut result = [0u64; 4];
                    for j in 0..4 {
                        result[j] = u64::from(ticks[j].wrapping_sub(prev[j]));
                    }
                    result
                })
                .unwrap_or([0; 4]);
            let sum = delta.iter().sum::<u64>();
            cores.push(ratio(if sum == 0 {
                0.0
            } else {
                (sum - delta[2]) as f64 / sum as f64
            }));
            for j in 0..4 {
                totals[j] += delta[j];
            }
        }
        self.cpu = Some(raw.ticks);
        reorder_cores(&mut cores, system);
        let sum = totals.iter().sum::<u64>().max(1) as f64;
        CpuSample {
            power_watts: None,
            usage: ratio((totals[0] + totals[1] + totals[3]) as f64 / sum),
            user: ratio((totals[0] + totals[3]) as f64 / sum),
            system: ratio(totals[1] as f64 / sum),
            cores,
            load: (
                number(raw.load[0]),
                number(raw.load[1]),
                number(raw.load[2]),
            ),
        }
    }
    pub fn apply(
        &mut self,
        frame: Frame,
        sample: &mut MonitorSample,
        system: &SystemInfo,
        now: u64,
    ) {
        if let Some(cpu) = frame.cpu {
            sample.cpu = self.cpu(cpu, system);
        }
        sample.cpu.power_watts = frame.cpu_power;
        if let Some(devices) = frame.disk_devices {
            sample.disk.devices = Some(devices);
        }
        if let Some(memory) = frame.memory {
            sample.memory = memory;
        }
        if let Some(disk) = frame.disk {
            let (r, w) = disk
                .counters
                .map(|(id, r, w)| rates(&mut self.disk, now, id, r, w))
                .unwrap_or_else(|| {
                    self.disk = None;
                    (0.0, 0.0)
                });
            sample.disk = DiskSample {
                devices: sample.disk.devices.take(),
                volumes: disk.volumes,
                read_bytes_per_sec: number(r),
                write_bytes_per_sec: number(w),
            };
        }
        if let Some(net) = frame.network {
            let (d, u) = rates(
                &mut self.network,
                now,
                net.interface.clone().unwrap_or_default(),
                net.down,
                net.up,
            );
            sample.network = NetworkSample {
                interface: net.interface,
                kind: net.kind,
                local_ip: net.local_ip,
                down_bytes_per_sec: number(d),
                up_bytes_per_sec: number(u),
                total_down_bytes: net.down,
                total_up_bytes: net.up,
            };
        }
        sample.battery = frame.battery.or(sample.battery.take());
        if let Some(gpu) = frame.gpu {
            sample.gpu = Some(gpu);
        }
        if let Some(sensors) = frame.sensors {
            sample.sensors = Some(sensors);
        }
        if let Some(fans) = frame.fans {
            sample.fans = Some(fans);
        }
        if let Some(procs) = frame.processes {
            let mut next = BTreeMap::new();
            let mut items = Vec::new();
            for mut raw in procs {
                let cpu = self
                    .processes
                    .get(&raw.info.pid)
                    .and_then(|&(start, at, total)| {
                        if start != raw.started {
                            return None;
                        }
                        let dt = now.checked_sub(at).filter(|d| *d > 0 && *d <= 15_000)?;
                        Some(
                            raw.cpu_ns.checked_sub(total)? as f64
                                / (dt as f64 * 1_000_000.0 * system.logical_cores as f64),
                        )
                    })
                    .unwrap_or(0.0);
                next.insert(raw.info.pid, (raw.started, now, raw.cpu_ns));
                raw.info.cpu = ratio(cpu);
                let io = |previous: &mut BTreeMap<u32, (u64, u64, u64, u64)>,
                          counters: Option<(u64, u64)>,
                          captured: u64,
                          retain_missing: bool| {
                    let Some((a, b)) = counters else {
                        if !retain_missing {
                            previous.remove(&raw.info.pid);
                        }
                        return (None, None);
                    };
                    let rates = previous
                        .get(&raw.info.pid)
                        .and_then(|&(start, at, pa, pb)| {
                            if start != raw.started {
                                return None;
                            }
                            let dt = captured.checked_sub(at).filter(|d| *d > 0 && *d <= 15000)?
                                as f64
                                / 1000.0;
                            Some((
                                Some(number(a.checked_sub(pa)? as f64 / dt)),
                                Some(number(b.checked_sub(pb)? as f64 / dt)),
                            ))
                        })
                        .unwrap_or((None, None));
                    previous.insert(raw.info.pid, (raw.started, captured, a, b));
                    rates
                };
                (
                    raw.info.disk_read_bytes_per_sec,
                    raw.info.disk_write_bytes_per_sec,
                ) = io(&mut self.process_disk, raw.disk_bytes, now, false);
                (
                    raw.info.net_down_bytes_per_sec,
                    raw.info.net_up_bytes_per_sec,
                ) = io(
                    &mut self.process_net,
                    raw.net_bytes,
                    raw.net_at.unwrap_or(now),
                    true,
                );
                items.push(raw.info);
            }
            self.process_disk.retain(|pid, _| next.contains_key(pid));
            self.process_net.retain(|pid, _| next.contains_key(pid));
            self.processes = next;
            sample.processes = Some(top_processes_io(items, true, true));
        }
    }
}
pub fn empty_sample(system: &SystemInfo) -> MonitorSample {
    MonitorSample {
        at: 0,
        cpu: CpuSample {
            power_watts: None,
            usage: number(0.0),
            user: number(0.0),
            system: number(0.0),
            cores: vec![number(0.0); system.logical_cores as usize],
            load: (number(0.0), number(0.0), number(0.0)),
        },
        gpu: None,
        memory: MemorySample {
            total_bytes: system.memory_bytes,
            used_bytes: 0,
            app_bytes: 0,
            wired_bytes: 0,
            compressed_bytes: 0,
            cached_bytes: 0,
            swap_used_bytes: 0,
            pressure: MemoryPressure::Normal,
        },
        disk: DiskSample {
            devices: None,
            volumes: vec![],
            read_bytes_per_sec: number(0.0),
            write_bytes_per_sec: number(0.0),
        },
        network: NetworkSample {
            interface: None,
            kind: None,
            local_ip: None,
            down_bytes_per_sec: number(0.0),
            up_bytes_per_sec: number(0.0),
            total_down_bytes: 0,
            total_up_bytes: 0,
        },
        battery: None,
        sensors: None,
        fans: None,
        processes: None,
        health: Health {
            level: HealthLevel::Good,
            issues: vec![],
        },
    }
}
pub struct Engine {
    pub system: SystemInfo,
    pub available: Modules,
    pub latest: MonitorSample,
    pub history: VecDeque<MonitorSample>,
    pub identities: BTreeMap<u32, RawProcess>,
    pub process_at: Option<u64>,
    pub deltas: Deltas,
    health: HealthTracker,
    pub trend: super::trend::Trend,
}
impl Engine {
    pub fn new(system: SystemInfo, probe: Frame) -> Self {
        let available = probe.available();
        let mut deltas = Deltas::default();
        let mut latest = empty_sample(&system);
        deltas.apply(probe, &mut latest, &system, 0);
        deltas.reset();
        Self {
            system,
            available,
            latest,
            history: VecDeque::with_capacity(60),
            identities: BTreeMap::new(),
            process_at: None,
            deltas,
            health: HealthTracker::default(),
            trend: super::trend::Trend::default(),
        }
    }
    pub fn fan_reason(&mut self, reason: FanReason) {
        self.health.fan_reason(reason);
    }
    pub fn reset(&mut self) {
        self.deltas.reset();
        self.health.reset();
        self.latest.processes = None;
        self.identities.clear();
        self.process_at = None;
    }
    pub fn sample(
        &mut self,
        frame: Frame,
        modules: &Modules,
        processes: bool,
        now: u64,
        at: i64,
    ) -> MonitorSample {
        self.sample_read(frame, modules, modules, processes, now, at)
    }
    pub fn sample_read(
        &mut self,
        frame: Frame,
        modules: &Modules,
        read_modules: &Modules,
        processes: bool,
        now: u64,
        at: i64,
    ) -> MonitorSample {
        if let Some(items) = &frame.processes {
            self.identities = items.iter().map(|p| (p.info.pid, p.clone())).collect();
        }
        if !processes {
            self.identities.clear();
        }
        let read = frame.available();
        for module in read_modules {
            if read.contains(module) {
                self.available.insert(*module);
            } else {
                self.available.remove(module);
            }
        }
        let readable = self.available.intersection(modules).copied().collect();
        self.deltas.retain(&readable, processes);
        self.deltas
            .apply(frame, &mut self.latest, &self.system, now);
        if !processes {
            self.latest.processes = None;
            self.identities.clear();
        }
        if !self.available.contains(&MonitorModule::Battery) {
            self.latest.battery = None;
        }
        if !self.available.contains(&MonitorModule::Gpu) {
            self.latest.gpu = None;
        }
        if !self.available.contains(&MonitorModule::Sensors) {
            self.latest.sensors = None;
        }
        if !self.available.contains(&MonitorModule::Fans) {
            self.latest.fans = None;
        }
        self.latest.at = at;
        let current: Modules = self.available.intersection(modules).copied().collect();
        self.latest.health =
            self.health
                .evaluate(&self.latest, &current, now, self.system.booted_at);
        if self.history.len() == 60 {
            self.history.pop_front();
        }
        self.trend.add(&self.latest, &current);
        self.history.push_back(self.latest.clone());
        self.latest.clone()
    }
    pub fn snapshot(&self) -> MonitorSnapshot {
        MonitorSnapshot {
            system: self.system.clone(),
            available: self.available.iter().copied().collect(),
            history: self.history.iter().cloned().collect(),
        }
    }
}
