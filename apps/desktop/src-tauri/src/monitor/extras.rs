//! Native extras are lazy and demand-gated. No privileged interface is used.
use super::types::*;
use std::{
    collections::BTreeMap,
    ffi::{CStr, c_char, c_void},
    io::Read,
    process::{Command, Stdio},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};
unsafe extern "C" {
    fn on_monitor_power_create() -> *mut c_void;
    fn on_monitor_power_free(context: *mut c_void);
    fn on_monitor_power_reset(context: *mut c_void);
    fn on_monitor_power_read(context: *mut c_void, seconds: f64) -> *mut c_char;
    fn on_monitor_media_info(bsd: *const c_char) -> *mut c_char;
    fn on_monitor_power_probe() -> *mut c_char;
    fn on_monitor_smart_read() -> *mut c_char;
}
#[derive(Debug, serde::Deserialize)]
pub struct EnergyChannel {
    pub name: String,
    pub unit: String,
    pub energy: i64,
}
/// Prefer disjoint CPU cluster totals; never add aggregate/core/DTL counters to those totals.
pub fn energy_watts(channels: &[EnergyChannel], seconds: f64) -> (Option<f64>, Option<f64>) {
    if !seconds.is_finite() || !(0.0..=15.0).contains(&seconds) || seconds == 0.0 {
        return (None, None);
    }
    let watts = |c: &EnergyChannel| {
        let factor = match c.unit.as_str() {
            "J" => 1.0,
            "mJ" => 1e-3,
            "uJ" | "µJ" => 1e-6,
            "nJ" => 1e-9,
            _ => return None,
        };
        let v = c.energy as f64 * factor / seconds;
        (c.energy >= 0 && v.is_finite() && (0.0..1000.0).contains(&v)).then_some(v)
    };
    let cluster = |name: &str| {
        let n = name.to_ascii_lowercase();
        let n = n.strip_suffix(" energy").unwrap_or(&n);
        let digits = |s: &str| s.bytes().all(|b| b.is_ascii_digit());
        n == "eacc_cpu"
            || n.strip_prefix("pacc")
                .and_then(|s| s.strip_suffix("_cpu"))
                .is_some_and(digits)
            || n.strip_prefix("ecpu").is_some_and(digits)
            || n.strip_prefix("pcpu").is_some_and(digits)
    };
    let clusters: Vec<_> = channels.iter().filter(|c| cluster(&c.name)).collect();
    let aggregate = channels
        .iter()
        .find(|c| c.name.eq_ignore_ascii_case("CPU Energy"))
        .and_then(watts)
        .filter(|v| *v > 0.0);
    let cpu = if clusters.is_empty() {
        aggregate
    } else {
        clusters
            .into_iter()
            .map(watts)
            .collect::<Option<Vec<_>>>()
            .map(|v| v.into_iter().sum::<f64>())
            .filter(|v| *v > 0.0 && *v < 1000.0)
            .or(aggregate)
    };
    let gpu = channels
        .iter()
        .find(|c| c.name.eq_ignore_ascii_case("GPU Energy"))
        .and_then(watts);
    (cpu, gpu)
}

#[derive(Default)]
pub struct Power {
    context: usize,
    attempted: bool,
    at: Option<Instant>,
    cached: (Option<MonitorNumber>, Option<MonitorNumber>),
}
impl Power {
    pub fn reset(&mut self) {
        if self.context != 0 {
            unsafe { on_monitor_power_reset(self.context as *mut c_void) };
        }
        self.at = None;
        self.cached = (None, None);
    }
    pub fn read(&mut self) -> (Option<MonitorNumber>, Option<MonitorNumber>) {
        if !self.attempted {
            self.context = unsafe { on_monitor_power_create() } as usize;
            self.attempted = true;
        }
        if self.context == 0 {
            return (None, None);
        }
        if self.at.is_some_and(|at| at.elapsed().as_millis() < 2000) {
            return self.cached;
        }
        let now = Instant::now();
        let elapsed = self
            .at
            .map(|at| now.duration_since(at).as_secs_f64())
            .unwrap_or(0.0);
        self.at = Some(now);
        let raw = unsafe { on_monitor_power_read(self.context as *mut c_void, elapsed) };
        let channels = if raw.is_null() {
            None
        } else {
            let result = serde_json::from_slice(unsafe { CStr::from_ptr(raw).to_bytes() }).ok();
            unsafe { nix::libc::free(raw.cast()) };
            result
        };
        let (cpu, gpu) = channels
            .map(|v: Vec<EnergyChannel>| energy_watts(&v, elapsed))
            .unwrap_or((None, None));
        self.cached = (
            cpu.and_then(|v| MonitorNumber::try_from(v).ok()),
            gpu.and_then(|v| MonitorNumber::try_from(v).ok()),
        );
        self.cached
    }
}
impl Drop for Power {
    fn drop(&mut self) {
        if self.context != 0 {
            unsafe { on_monitor_power_free(self.context as *mut c_void) };
        }
    }
}
pub fn smart() -> Option<Vec<DiskDevice>> {
    let raw = unsafe { on_monitor_smart_read() };
    if raw.is_null() {
        return None;
    }
    let result = serde_json::from_slice(unsafe { CStr::from_ptr(raw).to_bytes() }).ok();
    unsafe { nix::libc::free(raw.cast()) };
    result
}
pub fn power_probe() -> Option<serde_json::Value> {
    let raw = unsafe { on_monitor_power_probe() };
    if raw.is_null() {
        return None;
    }
    let result = serde_json::from_slice(unsafe { CStr::from_ptr(raw).to_bytes() }).ok();
    unsafe { nix::libc::free(raw.cast()) };
    result
}
/// Explicit diagnostic only: inspect a real delta as well as metadata to identify placeholder totals.
pub fn power_delta_probe() -> Option<serde_json::Value> {
    let mut power = Power::default();
    power.read();
    if power.context == 0 {
        return None;
    }
    std::thread::sleep(Duration::from_millis(2100));
    let seconds = power.at?.elapsed().as_secs_f64();
    let raw = unsafe { on_monitor_power_read(power.context as *mut c_void, seconds) };
    if raw.is_null() {
        return None;
    }
    let result = serde_json::from_slice(unsafe { CStr::from_ptr(raw).to_bytes() }).ok();
    unsafe { nix::libc::free(raw.cast()) };
    result
}
#[derive(Debug, serde::Deserialize)]
pub struct MediaInfo {
    pub physical: bool,
    pub roles: Vec<String>,
}
pub fn media_info(bsd: &str) -> Option<MediaInfo> {
    let bsd = std::ffi::CString::new(bsd.strip_prefix("/dev/").unwrap_or(bsd)).ok()?;
    let raw = unsafe { on_monitor_media_info(bsd.as_ptr()) };
    if raw.is_null() {
        return None;
    }
    let result = serde_json::from_slice(unsafe { CStr::from_ptr(raw).to_bytes() }).ok();
    unsafe { nix::libc::free(raw.cast()) };
    result
}
pub fn visible_volume(path: &str, local: bool, media: Option<&MediaInfo>) -> bool {
    if path == "/" {
        return true;
    }
    if !path.starts_with("/Volumes/") || path.starts_with("/Volumes/.") {
        return false;
    }
    if !local {
        return true;
    } // No metadata calls or network I/O for remote cached mount stats.
    media.is_some_and(|m| {
        m.physical
            && !m.roles.iter().any(|r| {
                ["Recovery", "Preboot", "VM", "Update", "xART", "Hardware"]
                    .iter()
                    .any(|v| r.eq_ignore_ascii_case(v))
            })
    })
}
/// nettop's CSV logging uses cumulative byte counters; process summary only, never addresses/argv.
pub const NETTOP_ARGS: &[&str] = &["-n", "-P", "-L", "1", "-x", "-J", "bytes_in,bytes_out"];
fn csv_row(row: &str) -> Option<Vec<String>> {
    let mut chars = row.chars().peekable();
    let mut cells = Vec::new();
    let mut cell = String::new();
    let mut quoted = false;
    while let Some(c) = chars.next() {
        match c {
            '"' if quoted && chars.peek() == Some(&'"') => {
                chars.next();
                cell.push('"');
            }
            '"' => quoted = !quoted,
            ',' if !quoted => {
                cells.push(cell.trim().to_owned());
                cell.clear();
            }
            _ => cell.push(c),
        }
    }
    if quoted {
        return None;
    }
    cells.push(cell.trim().to_owned());
    Some(cells)
}
pub fn parse_nettop(bytes: &[u8]) -> Option<BTreeMap<u32, (u64, u64)>> {
    let text = std::str::from_utf8(bytes).ok()?;
    let mut rows = text.lines();
    let header = rows.find(|line| line.contains("bytes_in") && line.contains("bytes_out"))?;
    let cols = csv_row(header)?;
    let incoming = cols.iter().position(|v| v == "bytes_in")?;
    let outgoing = cols.iter().position(|v| v == "bytes_out")?;
    let mut out = BTreeMap::new();
    for row in rows {
        let Some(cells) = csv_row(row) else {
            continue;
        };
        // The process field is name.pid, optionally after a timestamp column. No names leave this parser.
        let pid = cells.iter().take(incoming).rev().find_map(|v| {
            v.rsplit_once('.')
                .and_then(|(_, pid)| pid.parse::<u32>().ok())
        });
        if let (Some(pid), Some(Ok(down)), Some(Ok(up))) = (
            pid,
            cells.get(incoming).map(|v| v.parse::<u64>()),
            cells.get(outgoing).map(|v| v.parse::<u64>()),
        ) && pid > 0
        {
            out.insert(pid, (down, up));
        }
    }
    Some(out)
}
pub fn network(active: &Arc<AtomicBool>) -> Option<BTreeMap<u32, (u64, u64)>> {
    if !active.load(Ordering::Acquire) {
        return None;
    }
    let mut child = Command::new("/usr/bin/nettop")
        .args(NETTOP_ARGS)
        .env_clear()
        .env("LC_ALL", "C")
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .ok()?;
    let stdout = child.stdout.take()?;
    let reader = std::thread::Builder::new()
        .name("monitor-nettop-output".into())
        .spawn(move || {
            let mut bytes = Vec::new();
            stdout.take(1024 * 1024).read_to_end(&mut bytes).ok()?;
            Some(bytes)
        });
    let Ok(reader) = reader else {
        let _ = child.kill();
        let _ = child.wait();
        return None;
    };
    let started = Instant::now();
    let success = loop {
        if !active.load(Ordering::Acquire) || started.elapsed() > Duration::from_secs(3) {
            let _ = child.kill();
            let _ = child.wait();
            break false;
        }
        match child.try_wait() {
            Ok(Some(status)) => break status.success(),
            Ok(None) => std::thread::sleep(Duration::from_millis(10)),
            Err(_) => {
                let _ = child.kill();
                let _ = child.wait();
                break false;
            }
        }
    };
    let bytes = reader.join().ok().flatten()?;
    if !success || bytes.len() >= 1024 * 1024 || !active.load(Ordering::Acquire) {
        return None;
    }
    parse_nettop(&bytes)
}

#[derive(Clone)]
pub struct NetworkSnapshot {
    pub at: u64,
    pub counters: BTreeMap<u32, (u64, u64)>,
}
pub struct Network {
    active: Arc<AtomicBool>,
    pending: Arc<AtomicBool>,
    cached: Arc<std::sync::Mutex<Option<NetworkSnapshot>>>,
    launched: Option<Instant>,
    origin: Instant,
    worker: Option<std::thread::JoinHandle<()>>,
}
impl Network {
    pub fn new(active: Arc<AtomicBool>) -> Self {
        Self {
            active,
            pending: Arc::new(AtomicBool::new(false)),
            cached: Arc::new(std::sync::Mutex::new(None)),
            launched: None,
            origin: Instant::now(),
            worker: None,
        }
    }
    pub fn request(&mut self) -> Option<NetworkSnapshot> {
        if !self.active.load(Ordering::Acquire) {
            if let Ok(mut cached) = self.cached.lock() {
                *cached = None;
            }
            return None;
        }
        if self
            .launched
            .is_none_or(|at| at.elapsed().as_millis() >= 2000)
            && !self.pending.swap(true, Ordering::AcqRel)
        {
            self.launched = Some(Instant::now());
            let active = self.active.clone();
            let pending = self.pending.clone();
            let cached = self.cached.clone();
            let origin = self.origin;
            let job = std::thread::Builder::new()
                .name("monitor-network".into())
                .spawn(move || {
                    let result = network(&active).map(|counters| NetworkSnapshot {
                        at: origin.elapsed().as_millis() as u64,
                        counters,
                    });
                    if let Ok(mut cache) = cached.lock() {
                        *cache = if active.load(Ordering::Acquire) {
                            result
                        } else {
                            None
                        };
                    }
                    pending.store(false, Ordering::Release);
                });
            match job {
                Ok(worker) => self.worker = Some(worker),
                Err(_) => self.pending.store(false, Ordering::Release),
            }
        }
        self.cached
            .lock()
            .ok()
            .and_then(|v| v.clone())
            .filter(|s| (self.origin.elapsed().as_millis() as u64).saturating_sub(s.at) <= 5000)
    }
    /// Stop demand and wait for our worker, which kills/waits only its own child before returning.
    /// Explicit diagnostic-tool shutdown; normal runtime visibility cancellation remains asynchronous.
    pub fn finish(&mut self) -> std::thread::Result<()> {
        self.active.store(false, Ordering::Release);
        if let Some(worker) = self.worker.take() {
            worker.join()?;
        }
        Ok(())
    }
}
impl Drop for Network {
    fn drop(&mut self) {
        self.active.store(false, Ordering::Release);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn finish_cancels_demand_and_joins_worker_before_returning() {
        let active = Arc::new(AtomicBool::new(true));
        let mut network = Network::new(active.clone());
        let finished = Arc::new(AtomicBool::new(false));
        let worker_finished = finished.clone();
        network.pending.store(true, Ordering::Release);
        let pending = network.pending.clone();
        network.worker = Some(std::thread::spawn(move || {
            while active.load(Ordering::Acquire) {
                std::thread::yield_now();
            }
            pending.store(false, Ordering::Release);
            worker_finished.store(true, Ordering::Release);
        }));
        network.finish().unwrap();
        assert!(finished.load(Ordering::Acquire));
        assert!(!network.pending.load(Ordering::Acquire));
        assert!(!network.active.load(Ordering::Acquire));
        network.finish().unwrap(); // Idempotent, including when there was no child.
    }
}
