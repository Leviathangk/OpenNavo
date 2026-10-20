use super::{collect::*, health::HealthTracker, types::*};
use std::collections::{BTreeMap, BTreeSet, VecDeque};

/// One process as the previous capture saw it.
#[derive(Clone, Copy)]
struct Mark {
    started: u64,
    own_ns: u64,
    /// The last value the kernel gave for the CPU time of the children it collected.
    child_ns: Option<u64>,
    ppid: u32,
}
/// An ended child that was counted for while it was alive. Once its parent has waited for it, the parent's child totals
/// contain all of that, and it is taken off them so nothing is counted twice.
struct Pending {
    /// Its own CPU time plus its collected children's CPU when last seen, less deductions already spent.
    left: u64,
    /// Initially the full sampled CPU. Zero once a collection could contain it, even if other candidates
    /// spent all the room: the remaining balance may now stand for several smaller children.
    reach: u64,
    /// Processes that ended below it. Its totals contain them only if every parent on the way waited for its child before
    /// ending; one that did not left the child to launchd, which collects it where nobody can read it.
    below: Vec<Below>,
}
/// A process that ended below an ended child.
#[derive(Clone, Copy)]
struct Below {
    /// What was counted for it and is still to be taken off.
    left: u64,
    /// The least collection that can absorb this balance, including the ended ancestors' sampled totals.
    /// Already spent deductions do not increase this threshold. Zero once a collection could have contained it:
    /// any unspent balance then
    /// remains available because that collection may have spent another child's deduction instead.
    reach: u64,
}
/// The kernel adds the time of a collected child to its parent's totals in microseconds, so a count made from finer
/// readings can come out a little above what the parent was credited with. Differences this small are not told apart.
const COUNT_SLACK_NS: u64 = 1_000_000;
/// Takes what a parent collected off what was counted for its ended children and returns what is left.
///
/// Nothing in the process list says which children a collection held, or whether a child's own parent waited for it, so
/// what comes back is the least the new CPU can be: whatever could be inside is taken off, and what cannot be told stays
/// out of the result, in the share the other parts leave over. Before any collection could contain a candidate,
/// its size can rule it out. Afterwards its remaining balance no longer identifies a single process.
fn settle(owed: &mut Vec<Pending>, collected: u64) -> u64 {
    if collected == 0 {
        return 0;
    }
    let fits = collected.saturating_add(COUNT_SLACK_NS);
    let mut room = collected;
    for pending in owed.iter_mut() {
        // Size only rules out a collection. Spending this deduction does not prove that the child was collected,
        // so its unspent descendant deductions must survive even when its own balance reaches zero.
        if pending.reach <= fits {
            // Eligibility survives even if room is already zero. Otherwise the order in which we spent
            // interchangeable deductions would incorrectly exclude this balance from smaller collections.
            pending.reach = 0;
            let used = pending.left.min(room);
            pending.left -= used;
            room -= used;
        }
    }
    // A descendant can be inside only if every parent on the way waited. Once a collection could contain it,
    // keep its remaining balance eligible: deductions spent on other children may actually belong to this one.
    for pending in owed.iter_mut() {
        for below in &mut pending.below {
            if below.reach <= fits {
                below.reach = 0;
                let used = below.left.min(room);
                below.left -= used;
                room -= used;
            }
        }
        pending.below.retain(|below| below.left > 0);
    }
    owed.retain(|pending| pending.left > 0 || !pending.below.is_empty());
    room
}
/// The longest gap between two captures that still makes a CPU rate, the same limit disk and network rates have.
const MAX_WINDOW_NS: u64 = 15_000_000_000;
/// A process unseen before counts as born since the previous capture if it started no earlier than that capture began,
/// allowing for the wall clock and the kernel's start times disagreeing a little.
const START_SLACK_US: u64 = 100_000;
/// A process using less than this share of the machine is not counted as active.
const ACTIVE_SHARE: f64 = 0.001;
/// Parents named as the source of finished children's CPU, and the least share worth naming.
const FINISHED_PARENTS: usize = 3;
const FINISHED_PARENT_SHARE: f64 = 0.005;

/// Monotonic baselines are cleared on stop, suspension, interface/device changes and module removal.
#[derive(Default)]
pub struct Deltas {
    cpu: Option<Vec<[u32; 4]>>,
    /// Busy and total host ticks since the last process capture: what the machine did over the process window.
    cpu_window: (u64, u64),
    disk: Option<(u64, String, u64, u64)>,
    network: Option<(u64, String, u64, u64)>,
    /// Kept while the process views are hidden: a baseline older than the window limit is simply not used.
    processes: BTreeMap<u32, Mark>,
    process_capture: Option<Capture>,
    /// Per parent: the children that have ended and what they were counted for while alive. It is taken off the
    /// parent's child totals once the parent collects them, which can be a capture or several later: an ended child
    /// cannot be read, and a parent that is slow to wait for it has not yet been credited.
    owed: BTreeMap<u32, Vec<Pending>>,
    /// The last capture had nothing to measure against, so its CPU figures are all zero.
    process_baseline_missing: bool,
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
            // The CPU baseline stays: reopening within the window limit then shows figures at once, and an older
            // one is refused by the interval check. The I/O baselines depend on what is being read, so they go.
            self.process_disk.clear();
            self.process_net.clear();
        }
    }
    /// Whether the last process capture had no usable baseline, so a new one is worth taking soon.
    pub fn process_baseline_missing(&self) -> bool {
        self.process_baseline_missing
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
        self.cpu_window.0 += totals[0] + totals[1] + totals[3];
        self.cpu_window.1 += totals.iter().sum::<u64>();
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
            let capture = frame.processes_at.unwrap_or(Capture {
                mono_ns: now.saturating_mul(1_000_000),
                wall_us: 0,
            });
            self.processes(procs, capture, now, system, sample);
        }
    }
    /// Turns one process capture into rows: each process's CPU over the window since the previous capture, the CPU of
    /// children that ended in between, disk and network rates, and the same processes merged by app.
    fn processes(
        &mut self,
        procs: Vec<RawProcess>,
        capture: Capture,
        now: u64,
        system: &SystemInfo,
        sample: &mut MonitorSample,
    ) {
        let previous = self.process_capture.replace(capture);
        let window = previous.and_then(|earlier| {
            capture
                .mono_ns
                .checked_sub(earlier.mono_ns)
                .filter(|dt| *dt > 0 && *dt <= MAX_WINDOW_NS)
                .map(|dt| (earlier, dt))
        });
        self.process_baseline_missing = window.is_none();
        let (busy_ticks, all_ticks) = std::mem::take(&mut self.cpu_window);
        // What the whole machine can do in the window, in CPU nanoseconds.
        let machine_ns = window.map(|(_, dt)| dt as f64 * f64::from(system.logical_cores.max(1)));
        let before = std::mem::take(&mut self.processes);

        // Children that ended since the last capture are collected by their parents, whose child totals grow by all
        // that those children used. What was counted while they were alive is taken off, so nothing is counted twice;
        // what is left is CPU nobody could list because the process was gone before it could be read. An ended child
        // cannot be read before its parent has waited for it, so the deduction waits in `owed` until the parent does.
        // A parent that ended as well is collected in its turn, and when it waited for its children, what it collects
        // includes their totals: the whole ended subtree is handed to the first ancestor still here, the part below
        // the child as a maybe (see `settle`).
        let present: BTreeSet<(u32, u64)> = procs.iter().map(|p| (p.info.pid, p.started)).collect();
        if window.is_none() {
            self.owed.clear();
        } else {
            // Pending CPU belongs to the parent's lifetime, not the rate window. A parent can keep being
            // sampled while waiting arbitrarily long to collect a child. Transfer deductions when it ends;
            // clear them only with the baseline or when no readable ancestor remains.
            let ended: BTreeMap<u32, &Mark> = before
                .iter()
                .filter(|(pid, mark)| !present.contains(&(**pid, mark.started)))
                .map(|(pid, mark)| (*pid, mark))
                .collect();
            // How many ended ancestors a process has; the limit only guards against a loop in the recorded parents.
            let depth = |mut pid: u32| {
                let mut depth = 0;
                while let Some(mark) = ended.get(&pid) {
                    depth += 1;
                    pid = mark.ppid;
                    if depth > ended.len() {
                        break;
                    }
                }
                depth
            };
            // Deepest first: by the time a process hands its deduction up, everything owed to it by its own ended
            // children has arrived.
            let mut order: Vec<u32> = ended.keys().copied().collect();
            order.sort_by_key(|pid| std::cmp::Reverse(depth(*pid)));
            for pid in order {
                let mark = ended[&pid];
                let counted = mark.own_ns.saturating_add(mark.child_ns.unwrap_or(0));
                // Everything owed to this process by its own ended children is, for its parent, a maybe.
                let mut below = Vec::new();
                for child in self.owed.remove(&pid).unwrap_or_default() {
                    below.push(Below {
                        left: child.left,
                        // Earlier collections already contribute to `counted`. Preserve eligibility separately
                        // from the balance, which may now represent multiple smaller children.
                        reach: counted.saturating_add(child.reach),
                    });
                    below.extend(child.below.into_iter().map(|deeper| Below {
                        left: deeper.left,
                        reach: counted.saturating_add(deeper.reach),
                    }));
                }
                // Only a parent whose child totals can be read, or that ended itself, ever settles what is owed.
                let settles = ended.contains_key(&mark.ppid)
                    || before
                        .get(&mark.ppid)
                        .is_some_and(|parent| parent.child_ns.is_some());
                if settles {
                    self.owed.entry(mark.ppid).or_default().push(Pending {
                        left: counted,
                        reach: counted,
                        below,
                    });
                }
            }
        }

        let mut next = BTreeMap::new();
        let mut rows: Vec<(ProcessInfo, Group)> = Vec::with_capacity(procs.len());
        let (mut readable, mut active, mut finished_ns) = (0.0_f64, 0_u32, 0_u64);
        let mut parents: Vec<(u32, String, u64)> = Vec::new();
        for mut raw in procs {
            let base = before
                .get(&raw.info.pid)
                .filter(|mark| mark.started == raw.started);
            let (own, kids) = match (window, base) {
                (Some(_), Some(mark)) => (
                    raw.cpu_ns.saturating_sub(mark.own_ns),
                    match (raw.child_cpu_ns, mark.child_ns) {
                        (Some(current), Some(was)) => current.saturating_sub(was),
                        _ => 0,
                    },
                ),
                // Born since the previous capture began: everything it has used belongs to this window. A process
                // that merely was not read before is not told apart by age, so it gets nothing until measured twice.
                (Some((earlier, _)), None)
                    if earlier.wall_us != 0
                        && capture.wall_us != 0
                        && raw.started.saturating_add(START_SLACK_US) >= earlier.wall_us =>
                {
                    (raw.cpu_ns, raw.child_cpu_ns.unwrap_or(0))
                }
                _ => (0, 0),
            };
            let kids = match self.owed.get_mut(&raw.info.pid) {
                Some(owed) => settle(owed, kids),
                None => kids,
            };
            raw.info.cpu = ratio(machine_ns.map_or(0.0, |total| own as f64 / total));
            readable += f64::from(raw.info.cpu);
            if f64::from(raw.info.cpu) >= ACTIVE_SHARE {
                active += 1;
            }
            if kids > 0 {
                finished_ns = finished_ns.saturating_add(kids);
                parents.push((raw.info.pid, raw.info.name.clone(), kids));
            }
            next.insert(
                raw.info.pid,
                Mark {
                    started: raw.started,
                    // A reading of nothing from a process that had used CPU means the kernel would not say this
                    // time, not that it started over: keep the last figure, so the next reading is not its whole life.
                    own_ns: match (raw.cpu_ns, base) {
                        (0, Some(mark)) => mark.own_ns,
                        (current, _) => current,
                    },
                    child_ns: raw.child_cpu_ns.or(base.and_then(|mark| mark.child_ns)),
                    ppid: raw.ppid,
                },
            );
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
                        let dt = captured.checked_sub(at).filter(|d| *d > 0 && *d <= 15000)? as f64
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
            rows.push((raw.info, raw.group));
        }
        self.process_disk.retain(|pid, _| next.contains_key(pid));
        self.process_net.retain(|pid, _| next.contains_key(pid));
        // A deduction is only for a parent that is still there; children of one that ended go to the init process.
        self.owed
            .retain(|pid, owed| !owed.is_empty() && next.contains_key(pid));
        self.processes = next;

        sample.cpu_breakdown = machine_ns.and_then(|total| {
            let usage = (all_ticks > 0).then(|| busy_ticks as f64 / all_ticks as f64)?;
            let finished = finished_ns as f64 / total;
            parents.sort_by(|a, b| b.2.cmp(&a.2).then(a.0.cmp(&b.0)));
            let finished_from = parents
                .into_iter()
                .take(FINISHED_PARENTS)
                .map(|(pid, name, kids)| FinishedParent {
                    pid,
                    name,
                    cpu: ratio(kids as f64 / total),
                })
                .filter(|parent| f64::from(parent.cpu) >= FINISHED_PARENT_SHARE)
                .collect();
            Some(CpuBreakdown {
                usage: ratio(usage),
                readable: ratio(readable),
                active_count: active,
                finished: ratio(finished),
                finished_from,
                // The parts can overshoot the host's figure by sampling skew; then nothing is left over.
                other: ratio((usage - readable - finished).max(0.0)),
            })
        });
        sample.process_groups = Some(top_processes_io(merge_groups(&rows), true, true));
        sample.processes = Some(top_processes_io(
            rows.into_iter().map(|(info, _)| info).collect(),
            true,
            true,
        ));
    }
}

/// Each app bundle (helpers and all) or executable as one entry. The main process of an app leads it: its pid, name and
/// whether it can be quit; without one the heaviest member does and the bundle's name stands in.
fn merge_groups(rows: &[(ProcessInfo, Group)]) -> Vec<ProcessInfo> {
    let mut by_key: BTreeMap<std::sync::Arc<str>, (std::sync::Arc<str>, Vec<&ProcessInfo>)> =
        BTreeMap::new();
    for (info, group) in rows {
        by_key
            .entry(group.key.clone())
            .or_insert_with(|| (group.label.clone(), Vec::new()))
            .1
            .push(info);
    }
    // The busier process wins; of equals the one with the lower pid does.
    fn heavier(a: &ProcessInfo, b: &ProcessInfo) -> std::cmp::Ordering {
        f64::from(a.cpu)
            .total_cmp(&f64::from(b.cpu))
            .then(b.pid.cmp(&a.pid))
    }
    let total = |members: &[&ProcessInfo], pick: fn(&ProcessInfo) -> Option<MonitorNumber>| {
        let mut sum = None;
        for value in members.iter().filter_map(|p| pick(p)) {
            sum = Some(sum.unwrap_or(0.0) + f64::from(value));
        }
        sum.map(number)
    };
    by_key
        .into_values()
        .filter_map(|(label, members)| {
            let main = members
                .iter()
                .copied()
                .filter(|p| p.kind == ProcessKind::App && p.bundle_id.is_some())
                .max_by(|a, b| heavier(a, b));
            let lead = main.or_else(|| members.iter().copied().max_by(|a, b| heavier(a, b)))?;
            let mut merged = lead.clone();
            merged.count = members.len() as u32;
            if members.len() > 1 {
                if main.is_none() {
                    merged.name = label.to_string();
                }
                merged.bundle_id = main.and_then(|p| p.bundle_id.clone());
                merged.cpu = ratio(members.iter().map(|p| f64::from(p.cpu)).sum());
                merged.memory_bytes = members
                    .iter()
                    .fold(0, |sum, p| sum.saturating_add(p.memory_bytes));
                merged.disk_read_bytes_per_sec = total(&members, |p| p.disk_read_bytes_per_sec);
                merged.disk_write_bytes_per_sec = total(&members, |p| p.disk_write_bytes_per_sec);
                merged.net_down_bytes_per_sec = total(&members, |p| p.net_down_bytes_per_sec);
                merged.net_up_bytes_per_sec = total(&members, |p| p.net_up_bytes_per_sec);
                merged.kind = if members.iter().any(|p| p.kind == ProcessKind::App) {
                    ProcessKind::App
                } else if members.iter().all(|p| p.kind == ProcessKind::System) {
                    ProcessKind::System
                } else {
                    ProcessKind::Background
                };
                merged.quittable = main.is_some_and(|p| p.quittable);
                merged.is_self = members.iter().any(|p| p.is_self);
            }
            Some(merged)
        })
        .collect()
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
        process_groups: None,
        cpu_breakdown: None,
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
        self.latest.process_groups = None;
        self.latest.cpu_breakdown = None;
        self.identities.clear();
        self.process_at = None;
    }
    /// How soon the next process capture is due: the usual two seconds, or about one when the last had nothing to
    /// measure against (just opened), so the first figures do not wait out a full interval.
    pub fn process_interval_ms(&self) -> u64 {
        if self.deltas.process_baseline_missing() {
            900
        } else {
            2000
        }
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
            self.latest.process_groups = None;
            self.latest.cpu_breakdown = None;
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
