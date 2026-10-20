use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

/// JSON has no NaN or infinity. Keep required contract numbers non-null on every export.
#[derive(Debug, Clone, Copy, Default, Serialize, Deserialize, PartialEq)]
#[serde(try_from = "f64", into = "f64")]
pub struct MonitorNumber(f64);
impl TryFrom<f64> for MonitorNumber {
    type Error = &'static str;
    fn try_from(value: f64) -> Result<Self, Self::Error> {
        if value.is_finite() {
            Ok(Self(value))
        } else {
            Err("monitor numbers must be finite")
        }
    }
}
impl From<MonitorNumber> for f64 {
    fn from(value: MonitorNumber) -> Self {
        value.0
    }
}
impl specta::Type for MonitorNumber {
    fn definition(_: &mut specta::Types) -> specta::datatype::DataType {
        specta_typescript::define("number").into()
    }
}

#[derive(Debug, Clone, Copy, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(try_from = "u32", into = "u32")]
pub enum MonitorIntervalMs {
    OneSecond,
    #[default]
    TwoSeconds,
    FiveSeconds,
}
impl TryFrom<u32> for MonitorIntervalMs {
    type Error = &'static str;
    fn try_from(value: u32) -> Result<Self, Self::Error> {
        match value {
            1000 => Ok(Self::OneSecond),
            2000 => Ok(Self::TwoSeconds),
            5000 => Ok(Self::FiveSeconds),
            _ => Err("monitorIntervalMs must be 1000, 2000 or 5000"),
        }
    }
}
impl From<MonitorIntervalMs> for u32 {
    fn from(value: MonitorIntervalMs) -> Self {
        match value {
            MonitorIntervalMs::OneSecond => 1000,
            MonitorIntervalMs::TwoSeconds => 2000,
            MonitorIntervalMs::FiveSeconds => 5000,
        }
    }
}
impl specta::Type for MonitorIntervalMs {
    fn definition(_: &mut specta::Types) -> specta::datatype::DataType {
        specta_typescript::define("1000 | 2000 | 5000").into()
    }
}

#[derive(
    Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq, PartialOrd, Ord,
)]
#[serde(rename_all = "snake_case")]
pub enum MonitorModule {
    Cpu,
    Gpu,
    Memory,
    Disk,
    Network,
    Battery,
    Sensors,
    Fans,
}

#[derive(
    Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq, PartialOrd, Ord,
)]
#[serde(rename_all = "snake_case")]
pub enum MonitorView {
    Page,
    Detail,
    Popover,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum MonitorMainTarget {
    Detail,
    Settings,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct MonitorSnapshot {
    pub system: SystemInfo,
    /// Modules that this machine can actually read; no temperature or FNum=0 excludes that module.
    pub available: Vec<MonitorModule>,
    /// Shared in-memory history, oldest first; the latest 60 samples, at Unix milliseconds.
    pub history: Vec<MonitorSample>,
}

/// In-memory five-second bucket averages, oldest first, at most 720 points (one hour).
/// Unsampled periods have no point; ratios are 0–1, rates B/s, temperature °C, fan RPM the mean.
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct MonitorTrendPoint {
    pub at: i64,
    pub cpu: Option<MonitorNumber>,
    pub gpu: Option<MonitorNumber>,
    pub memory: Option<MonitorNumber>,
    pub disk_read: Option<MonitorNumber>,
    pub disk_write: Option<MonitorNumber>,
    pub net_down: Option<MonitorNumber>,
    pub net_up: Option<MonitorNumber>,
    pub temperature: Option<MonitorNumber>,
    pub fan_rpm: Option<MonitorNumber>,
    pub battery: Option<MonitorNumber>,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct SystemInfo {
    pub model_name: String,
    pub model_id: String,
    pub chip: String,
    pub performance_cores: Option<u32>,
    pub efficiency_cores: Option<u32>,
    pub logical_cores: u32,
    pub gpu_cores: Option<u32>,
    pub memory_bytes: u64,
    pub os_version: String,
    pub booted_at: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct MonitorSample {
    pub at: i64,
    pub cpu: CpuSample,
    pub gpu: Option<GpuSample>,
    pub memory: MemorySample,
    pub disk: DiskSample,
    pub network: NetworkSample,
    pub battery: Option<BatterySample>,
    pub sensors: Option<Vec<SensorReading>>,
    pub fans: Option<Vec<FanState>>,
    /// Visible page/detail/popover only: CPU top 20 union memory top 20, deduplicated by PID.
    /// M3.1 adds disk top 10 / network top 10 when those modules have a visible subscription.
    pub processes: Option<Vec<ProcessInfo>>,
    /// The same processes merged by app bundle (helpers included) or executable name, selected like `processes`.
    /// Each entry stands for `count` processes: sums of CPU, memory and I/O, the main process's pid and name.
    pub process_groups: Option<Vec<ProcessInfo>>,
    /// Where the machine's CPU went in the last process window; null until two process captures exist.
    pub cpu_breakdown: Option<CpuBreakdown>,
    pub health: Health,
}

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CpuSample {
    pub usage: MonitorNumber,
    pub user: MonitorNumber,
    pub system: MonitorNumber,
    /// Apple: the first performanceCores entries are performance cores, followed by efficiency cores.
    /// Intel has performanceCores=null and preserves host order.
    pub cores: Vec<MonitorNumber>,
    pub load: (MonitorNumber, MonitorNumber, MonitorNumber),
    /// Watts; M3.1 visible CPU/GPU subscriptions only, at most once per two seconds; otherwise null.
    pub power_watts: Option<MonitorNumber>,
}
/// How the machine's busy share was spent over one process window (all ratios of the whole machine).
/// `readable + finished + other` equals `usage` unless the parts overshoot it by sampling skew, then `other` is 0.
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CpuBreakdown {
    /// The host's busy ratio over the same window as the process figures.
    pub usage: MonitorNumber,
    /// Used by every process whose CPU OpenNavo can read (its own user's), whether listed or not.
    pub readable: MonitorNumber,
    /// How many of those used at least 0.1% of the machine.
    pub active_count: u32,
    /// Used by child processes that ended and were collected by their parents since the last capture, so they can no
    /// longer be listed. Credited once: what they were already counted while alive is not repeated.
    pub finished: MonitorNumber,
    /// The parents that collected the most of `finished`, largest first (at most three).
    pub finished_from: Vec<FinishedParent>,
    /// What is left of the busy share: the kernel, root and other users' processes, and rounding.
    pub other: MonitorNumber,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct FinishedParent {
    pub pid: u32,
    pub name: String,
    /// Whole-machine ratio of the finished children's CPU this parent collected.
    pub cpu: MonitorNumber,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct GpuSample {
    pub usage: MonitorNumber,
    /// Watts; unsupported Intel / unavailable IOReport readings are null.
    pub power_watts: Option<MonitorNumber>,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum MemoryPressure {
    Normal,
    Warning,
    Critical,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct MemorySample {
    pub total_bytes: u64,
    pub used_bytes: u64,
    pub app_bytes: u64,
    pub wired_bytes: u64,
    pub compressed_bytes: u64,
    pub cached_bytes: u64,
    pub swap_used_bytes: u64,
    pub pressure: MemoryPressure,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct DiskSample {
    pub volumes: Vec<VolumeInfo>,
    pub read_bytes_per_sec: MonitorNumber,
    pub write_bytes_per_sec: MonitorNumber,
    /// M3.1 SMART device data, refreshed at most every ten minutes; unavailable data is null.
    pub devices: Option<Vec<DiskDevice>>,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum DiskSmartStatus {
    Verified,
    Failing,
    Unsupported,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct DiskDevice {
    pub id: String,
    pub name: String,
    pub internal: bool,
    pub smart: DiskSmartStatus,
    /// NVMe wear expressed as a 0–1 ratio, not free-space usage.
    pub percent_used: Option<MonitorNumber>,
    pub temperature_c: Option<MonitorNumber>,
    pub power_on_hours: Option<MonitorNumber>,
    pub data_written_bytes: Option<u64>,
    pub mount_points: Vec<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct VolumeInfo {
    pub name: String,
    pub mount_point: String,
    pub total_bytes: u64,
    pub free_bytes: u64,
    pub internal: bool,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum NetworkKind {
    Wifi,
    Ethernet,
    Vpn,
    Other,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct NetworkSample {
    pub interface: Option<String>,
    pub kind: Option<NetworkKind>,
    pub local_ip: Option<String>,
    pub down_bytes_per_sec: MonitorNumber,
    pub up_bytes_per_sec: MonitorNumber,
    pub total_down_bytes: u64,
    pub total_up_bytes: u64,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct BatterySample {
    pub percent: MonitorNumber,
    pub charging: bool,
    pub on_ac: bool,
    pub minutes_remaining: Option<MonitorNumber>,
    pub health_percent: Option<MonitorNumber>,
    pub cycle_count: Option<u32>,
    pub design_capacity_mah: Option<u32>,
    pub max_capacity_mah: Option<u32>,
    pub temperature_c: Option<MonitorNumber>,
    pub voltage_v: Option<MonitorNumber>,
    pub power_watts: Option<MonitorNumber>,
    pub adapter_watts: Option<MonitorNumber>,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum SensorGroup {
    Cpu,
    Gpu,
    Soc,
    Battery,
    Ssd,
    Other,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
/// Maximum per thermal zone, stable keys, at most seven localized English names; absent readings are null.
pub struct SensorReading {
    pub key: String,
    pub name: String,
    pub group: SensorGroup,
    pub celsius: MonitorNumber,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum ProcessKind {
    App,
    Background,
    System,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ProcessInfo {
    pub pid: u32,
    pub name: String,
    pub bundle_id: Option<String>,
    pub icon_path: Option<String>,
    /// Whole-machine ratio; multiply by logicalCores for Activity Monitor's convention.
    pub cpu: MonitorNumber,
    pub memory_bytes: u64,
    /// B/s; unreadable / other-user processes and the first delta are null (M3.1).
    pub disk_read_bytes_per_sec: Option<MonitorNumber>,
    pub disk_write_bytes_per_sec: Option<MonitorNumber>,
    pub net_down_bytes_per_sec: Option<MonitorNumber>,
    pub net_up_bytes_per_sec: Option<MonitorNumber>,
    pub kind: ProcessKind,
    pub quittable: bool,
    /// How many processes this entry stands for: 1, or the members of a merged app or executable.
    pub count: u32,
    /// OpenNavo itself (or the group holding it): listed, but never quittable.
    pub is_self: bool,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum HealthLevel {
    Good,
    Attention,
    Critical,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
pub struct Health {
    pub level: HealthLevel,
    pub issues: Vec<HealthIssue>,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum HealthIssueKey {
    CpuBusy,
    MemoryPressure,
    DiskLow,
    DiskSmart,
    BatteryHealth,
    BatteryHot,
    Temperature,
    Uptime,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum HealthIssueLevel {
    Info,
    Attention,
    Critical,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct HealthIssue {
    pub key: HealthIssueKey,
    pub level: HealthIssueLevel,
    pub value: MonitorNumber,
    pub top_pid: Option<u32>,
    /// Device name for disk_smart; null for every other issue.
    pub subject: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
pub struct MenuBarItemStatus {
    pub module: MonitorModule,
    pub installed: bool,
    pub visible: Option<bool>,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum FanHelperState {
    Unsupported,
    NotInstalled,
    RequiresApproval,
    Enabled,
    Error,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum FanControlSupport {
    Verified,
    Unverified,
    Unsupported,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct FanHelperStatus {
    pub status: FanHelperState,
    pub support: FanControlSupport,
    /// The chip gate alone, independent of the helper: false only for unknown chips and fanless Macs.
    pub manual_allowed: bool,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum FanMode {
    Auto,
    Manual,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
/// id is the zero-based SMC index; name prefers its location, otherwise Fan {id+1}.
pub struct FanState {
    pub id: u32,
    pub name: String,
    pub rpm: u32,
    pub min_rpm: u32,
    pub max_rpm: u32,
    pub target_rpm: Option<u32>,
    pub mode: FanMode,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
pub struct FanChange {
    pub fans: Vec<FanState>,
    pub reason: FanReason,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum FanReason {
    User,
    AppQuit,
    HeartbeatLost,
    Sleep,
    WakeReapplied,
    Overheat,
    WriteFailed,
    HelperStarted,
    HelperUninstalled,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
#[specta(type = MenuBarStyleContract)]
pub enum MenuBarStyle {
    Text,
    Value,
    #[serde(alias = "chart")]
    Line,
    Bars,
}

// The persisted legacy alias is accepted by serde, but must not widen the public IPC contract.
struct MenuBarStyleContract;
impl specta::Type for MenuBarStyleContract {
    fn definition(_: &mut specta::Types) -> specta::datatype::DataType {
        specta_typescript::define("\"text\" | \"value\" | \"line\" | \"bars\"").into()
    }
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, specta::Type, PartialEq, Eq)]
pub struct MenuBarItemSetting {
    pub enabled: bool,
    pub style: MenuBarStyle,
}

/// Every menu bar item on output, with partial legacy input support. Fans have no item of their own:
/// as in Stats, the sensors item shows the temperature and its popover the fans.
#[derive(Debug, Clone, Serialize, specta::Type, PartialEq, Eq)]
pub struct MonitorMenuBar {
    pub cpu: MenuBarItemSetting,
    pub gpu: MenuBarItemSetting,
    pub memory: MenuBarItemSetting,
    pub disk: MenuBarItemSetting,
    pub network: MenuBarItemSetting,
    pub battery: MenuBarItemSetting,
    pub sensors: MenuBarItemSetting,
}
impl Default for MonitorMenuBar {
    fn default() -> Self {
        let off = MenuBarItemSetting {
            enabled: false,
            style: MenuBarStyle::Text,
        };
        let on = MenuBarItemSetting {
            enabled: true,
            ..off
        };
        Self {
            cpu: on,
            memory: on,
            gpu: off,
            disk: off,
            network: off,
            battery: off,
            // A temperature needs no label, so its default is the value alone.
            sensors: MenuBarItemSetting {
                style: MenuBarStyle::Value,
                ..off
            },
        }
    }
}
impl<'de> Deserialize<'de> for MonitorMenuBar {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        let input = BTreeMap::<MonitorModule, MenuBarItemSetting>::deserialize(deserializer)?;
        let mut value = Self::default();
        for (module, setting) in input {
            *match module {
                MonitorModule::Cpu => &mut value.cpu,
                MonitorModule::Gpu => &mut value.gpu,
                MonitorModule::Memory => &mut value.memory,
                MonitorModule::Disk => &mut value.disk,
                MonitorModule::Network => &mut value.network,
                MonitorModule::Battery => &mut value.battery,
                MonitorModule::Sensors => &mut value.sensors,
                // Earlier builds had a fans item; fans are now part of the sensors item.
                MonitorModule::Fans => continue,
            } = setting;
        }
        Ok(value)
    }
}

impl MonitorMenuBar {
    pub fn item(&self, module: MonitorModule) -> MenuBarItemSetting {
        match module {
            MonitorModule::Cpu => self.cpu,
            MonitorModule::Gpu => self.gpu,
            MonitorModule::Memory => self.memory,
            MonitorModule::Disk => self.disk,
            MonitorModule::Network => self.network,
            MonitorModule::Battery => self.battery,
            MonitorModule::Sensors => self.sensors,
            MonitorModule::Fans => MenuBarItemSetting {
                enabled: false,
                style: MenuBarStyle::Value,
            },
        }
    }
    pub fn enabled(&self) -> super::collect::Modules {
        [
            MonitorModule::Cpu,
            MonitorModule::Gpu,
            MonitorModule::Memory,
            MonitorModule::Disk,
            MonitorModule::Network,
            MonitorModule::Battery,
            MonitorModule::Sensors,
        ]
        .into_iter()
        .filter(|m| self.item(*m).enabled)
        .collect()
    }
}
impl MonitorModule {
    pub fn key(self) -> &'static str {
        match self {
            Self::Cpu => "cpu",
            Self::Gpu => "gpu",
            Self::Memory => "memory",
            Self::Disk => "disk",
            Self::Network => "network",
            Self::Battery => "battery",
            Self::Sensors => "sensors",
            Self::Fans => "fans",
        }
    }
}
