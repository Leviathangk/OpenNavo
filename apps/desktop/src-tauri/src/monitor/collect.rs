//! Native boundaries are injected into the sampler; no collector writes files or makes requests.
use super::types::*;
use crate::AppError;
use std::collections::BTreeSet;

pub type Modules = BTreeSet<MonitorModule>;
#[derive(Clone, Debug)]
pub struct RawCpu {
    pub ticks: Vec<[u32; 4]>,
    pub load: [f64; 3],
}
#[derive(Clone, Debug)]
pub struct RawDisk {
    pub volumes: Vec<VolumeInfo>,
    pub counters: Option<(String, u64, u64)>,
}
#[derive(Clone, Debug)]
pub struct RawNetwork {
    pub interface: Option<String>,
    pub kind: Option<NetworkKind>,
    pub local_ip: Option<String>,
    pub down: u64,
    pub up: u64,
}
/// Which app or executable a process belongs to, for the merged view. Stable for the life of the process.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Group {
    /// `app:<outermost .app path>` or `exe:<executable name>`.
    pub key: std::sync::Arc<str>,
    /// The bundle's name, or the executable's.
    pub label: std::sync::Arc<str>,
}
/// When a process list was read, on the clocks that matter: monotonic for the interval and wall for process start times.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Capture {
    pub mono_ns: u64,
    pub wall_us: u64,
}
impl Capture {
    pub fn now() -> Self {
        use std::sync::OnceLock;
        static EPOCH: OnceLock<std::time::Instant> = OnceLock::new();
        let epoch = EPOCH.get_or_init(std::time::Instant::now);
        Self {
            mono_ns: u64::try_from(epoch.elapsed().as_nanos()).unwrap_or(u64::MAX),
            wall_us: std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .map_or(0, |d| u64::try_from(d.as_micros()).unwrap_or(u64::MAX)),
        }
    }
}
#[derive(Clone, Debug)]
pub struct RawProcess {
    pub info: ProcessInfo,
    pub started: u64,
    pub cpu_ns: u64,
    /// CPU time of children this process has collected (waited for), all of them ever, subtree included.
    /// Unknown when the kernel would not say, which must not read as "none" and then jump.
    pub child_cpu_ns: Option<u64>,
    pub ppid: u32,
    pub group: Group,
    pub disk_bytes: Option<(u64, u64)>,
    pub net_bytes: Option<(u64, u64)>,
    /// Monotonic time of the independently captured network snapshot; null for injected synchronous counters.
    pub net_at: Option<u64>,
}
#[derive(Default)]
pub struct Frame {
    pub cpu_power: Option<MonitorNumber>,
    pub disk_devices: Option<Vec<DiskDevice>>,
    pub cpu: Option<RawCpu>,
    pub memory: Option<MemorySample>,
    pub disk: Option<RawDisk>,
    pub network: Option<RawNetwork>,
    pub battery: Option<BatterySample>,
    pub gpu: Option<GpuSample>,
    pub sensors: Option<Vec<SensorReading>>,
    pub fans: Option<Vec<FanState>>,
    pub processes: Option<Vec<RawProcess>>,
    /// When `processes` began to be read. Collectors that cannot say leave it empty: the interval then comes from the
    /// sampler's clock, and processes that started in between are not credited for their first interval.
    pub processes_at: Option<Capture>,
}
impl Frame {
    pub fn available(&self) -> Modules {
        [
            (MonitorModule::Cpu, self.cpu.is_some()),
            (MonitorModule::Gpu, self.gpu.is_some()),
            (
                MonitorModule::Sensors,
                self.sensors.as_ref().is_some_and(|v| !v.is_empty()),
            ),
            (
                MonitorModule::Fans,
                self.fans.as_ref().is_some_and(|v| !v.is_empty()),
            ),
            (MonitorModule::Memory, self.memory.is_some()),
            (MonitorModule::Disk, self.disk.is_some()),
            (MonitorModule::Network, self.network.is_some()),
            (MonitorModule::Battery, self.battery.is_some()),
        ]
        .into_iter()
        .filter_map(|(m, yes)| yes.then_some(m))
        .collect()
    }
}
#[derive(Clone, Debug, Default)]
pub struct Extras {
    pub power: bool,
    pub power_visible: bool,
    pub smart: bool,
    pub process_disk: bool,
    pub process_network: bool,
}
pub trait Collector: Send {
    fn system(&self) -> SystemInfo;
    fn collect(&mut self, modules: &Modules, processes: bool) -> Frame;
    fn collect_requested(&mut self, modules: &Modules, processes: bool, _: &Extras) -> Frame {
        self.collect(modules, processes)
    }
    fn power_epoch(&self) -> Option<std::sync::Arc<std::sync::atomic::AtomicU64>> {
        None
    }
    fn network_active_flag(&self) -> Option<std::sync::Arc<std::sync::atomic::AtomicBool>> {
        None
    }
}
pub fn number(value: f64) -> MonitorNumber {
    MonitorNumber::try_from(if value.is_finite() { value } else { 0.0 }).unwrap_or_default()
}
pub fn ratio(value: f64) -> MonitorNumber {
    number(value.clamp(0.0, 1.0))
}
pub fn unavailable(key: &str) -> AppError {
    AppError::new("E_MONITOR_UNAVAILABLE", key)
}
pub fn m1_modules() -> Modules {
    [
        MonitorModule::Cpu,
        MonitorModule::Memory,
        MonitorModule::Disk,
        MonitorModule::Network,
        MonitorModule::Battery,
    ]
    .into_iter()
    .collect()
}

pub fn all_modules() -> Modules {
    let mut modules = m1_modules();
    modules.extend([
        MonitorModule::Gpu,
        MonitorModule::Sensors,
        MonitorModule::Fans,
    ]);
    modules
}
