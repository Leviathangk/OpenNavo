use super::{
    collect::*,
    system::{self, NativeSystemSource, SystemSource},
    types::*,
};
use crate::AppError;
#[cfg(target_os = "macos")]
use {super::cf, nix::libc};

pub struct NativeCollector {
    system: SystemInfo,
    #[cfg(target_os = "macos")]
    smc: Option<super::smc::NativeSmc>,
    #[cfg(target_os = "macos")]
    temperature_keys: Vec<String>,
    #[cfg(target_os = "macos")]
    power: super::extras::Power,
    network_active: std::sync::Arc<std::sync::atomic::AtomicBool>,
    power_epoch: std::sync::Arc<std::sync::atomic::AtomicU64>,
    observed_power_epoch: u64,
    #[cfg(target_os = "macos")]
    process_network: super::extras::Network,
    smart_at: Option<std::time::Instant>,
    devices: Option<Vec<DiskDevice>>,
    volumes: super::volume_cache::VolumeCache,
    #[cfg(target_os = "macos")]
    mount_media: std::collections::BTreeMap<String, (u64, Option<super::extras::MediaInfo>)>,
    origin: std::time::Instant,
}
impl NativeCollector {
    /// Read-only tools can verify their asynchronous nettop worker has reaped its child before exit.
    pub fn finish_network(&mut self) -> Result<(), AppError> {
        #[cfg(target_os = "macos")]
        self.process_network
            .finish()
            .map_err(|_| unavailable("monitor_network_join"))?;
        Ok(())
    }
    pub fn new() -> Result<Self, AppError> {
        let mut system = system::read(&NativeSystemSource)?;
        #[cfg(target_os = "macos")]
        if let Some(name) = cf::product_name() {
            system.model_name = name;
        }
        #[cfg(target_os = "macos")]
        {
            system.gpu_cores = cf::property_services("AGXAccelerator", "gpu-core-count")
                .into_iter()
                .chain(cf::property_services("IOAccelerator", "gpu-core-count"))
                .find_map(|(_, v)| cf::uint32(v.0).filter(|n| *n > 0 && *n <= 1024));
        }
        #[cfg(target_os = "macos")]
        let mut smc = super::smc::NativeSmc::open();
        #[cfg(target_os = "macos")]
        let temperature_keys = if !system.chip.starts_with("Apple") {
            smc.as_mut()
                .map(|s| {
                    use super::smc::Smc;
                    s.keys()
                })
                .unwrap_or_default()
        } else {
            vec![]
        };
        let network_active = std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false));
        Ok(Self {
            system,
            #[cfg(target_os = "macos")]
            smc,
            #[cfg(target_os = "macos")]
            temperature_keys,
            #[cfg(target_os = "macos")]
            power: super::extras::Power::default(),
            network_active: network_active.clone(),
            power_epoch: std::sync::Arc::new(std::sync::atomic::AtomicU64::new(0)),
            observed_power_epoch: 0,
            #[cfg(target_os = "macos")]
            process_network: super::extras::Network::new(network_active),
            smart_at: None,
            devices: None,
            volumes: super::volume_cache::VolumeCache::default(),
            #[cfg(target_os = "macos")]
            mount_media: Default::default(),
            origin: std::time::Instant::now(),
        })
    }
}
impl Collector for NativeCollector {
    fn system(&self) -> SystemInfo {
        self.system.clone()
    }
    fn power_epoch(&self) -> Option<std::sync::Arc<std::sync::atomic::AtomicU64>> {
        Some(self.power_epoch.clone())
    }
    fn network_active_flag(&self) -> Option<std::sync::Arc<std::sync::atomic::AtomicBool>> {
        Some(self.network_active.clone())
    }
    fn collect_requested(&mut self, modules: &Modules, processes: bool, extras: &Extras) -> Frame {
        let mut frame = self.collect(modules, processes);
        #[cfg(target_os = "macos")]
        {
            let epoch = self.power_epoch.load(std::sync::atomic::Ordering::Acquire);
            if epoch != self.observed_power_epoch || !extras.power_visible {
                self.power.reset();
                self.observed_power_epoch = epoch;
            }
            if extras.power && epoch % 2 == 1 && self.system.chip.starts_with("Apple") {
                let (cpu, gpu) = self.power.read();
                frame.cpu_power = cpu;
                if let Some(sample) = &mut frame.gpu {
                    sample.power_watts = gpu;
                }
            }
            if extras.smart {
                if self.smart_at.is_none_or(|at| at.elapsed().as_secs() >= 600) {
                    self.smart_at = Some(std::time::Instant::now());
                    self.devices = super::extras::smart();
                }
                frame.disk_devices = self.devices.clone();
            }
            if let Some(items) = &mut frame.processes {
                let network = if extras.process_network {
                    self.process_network.request()
                } else {
                    None
                };
                for p in items {
                    if extras.process_network
                        && p.disk_bytes.is_some()
                        && super::procs::same_user_identity(p.info.pid, p.started)
                    {
                        p.net_bytes = network
                            .as_ref()
                            .and_then(|n| n.counters.get(&p.info.pid).copied());
                        p.net_at = network.as_ref().map(|n| n.at);
                    }
                    if !extras.process_disk {
                        p.disk_bytes = None;
                    }
                }
            }
        }
        frame
    }
    fn collect(&mut self, modules: &Modules, processes: bool) -> Frame {
        #[cfg(target_os = "macos")]
        {
            objc2::rc::autoreleasepool(|_| Frame {
                cpu: modules.contains(&MonitorModule::Cpu).then(cpu).flatten(),
                memory: modules
                    .contains(&MonitorModule::Memory)
                    .then(|| memory(self.system.memory_bytes))
                    .flatten(),
                disk: if modules.contains(&MonitorModule::Disk) {
                    disk(
                        &mut self.volumes,
                        &mut self.mount_media,
                        self.origin.elapsed().as_millis() as u64,
                    )
                } else {
                    None
                },
                network: modules
                    .contains(&MonitorModule::Network)
                    .then(network)
                    .flatten(),
                battery: modules
                    .contains(&MonitorModule::Battery)
                    .then(battery)
                    .flatten(),
                gpu: modules.contains(&MonitorModule::Gpu).then(gpu).flatten(),
                sensors: if modules.contains(&MonitorModule::Sensors) {
                    if self.system.chip.starts_with("Apple") {
                        super::sensors::apple_with_smc(
                            &self.system.chip,
                            self.smc.as_mut().map(|s| s as &mut dyn super::smc::Smc),
                        )
                    } else {
                        self.smc
                            .as_mut()
                            .and_then(|s| super::sensors::intel(s, &self.temperature_keys))
                    }
                } else {
                    None
                },
                fans: if modules.contains(&MonitorModule::Fans) {
                    self.smc
                        .as_mut()
                        .and_then(|s| super::fan::hardware::read_fans(s))
                } else {
                    None
                },
                processes: processes.then(super::procs::list).flatten(),
                ..Frame::default()
            })
        }
        #[cfg(not(target_os = "macos"))]
        {
            let _ = (modules, processes);
            Frame::default()
        }
    }
}
#[cfg(target_os = "macos")]
unsafe extern "C" {
    fn mach_host_self() -> u32;
    static mach_task_self_: u32;
    fn mach_port_deallocate(task: u32, port: u32) -> i32;
}
#[cfg(target_os = "macos")]
struct Host(u32);
#[cfg(target_os = "macos")]
impl Host {
    fn new() -> Self {
        Self(unsafe { mach_host_self() })
    }
}
#[cfg(target_os = "macos")]
impl Drop for Host {
    fn drop(&mut self) {
        unsafe {
            mach_port_deallocate(mach_task_self_, self.0);
        }
    }
}
#[cfg(target_os = "macos")]
fn cpu() -> Option<RawCpu> {
    let mut count = 0;
    let mut len = 0;
    let mut data = std::ptr::null_mut();
    let host = Host::new();
    if unsafe {
        libc::host_processor_info(
            host.0,
            libc::PROCESSOR_CPU_LOAD_INFO,
            &mut count,
            &mut data,
            &mut len,
        )
    } != 0
    {
        return None;
    }
    // Mach allocates this out-of-line buffer; always deallocate it, including malformed results.
    let result =
        if !data.is_null() && count > 0 && count <= 4096 && len as usize >= count as usize * 4 {
            let ticks = unsafe { std::slice::from_raw_parts(data, count as usize * 4) }
                .chunks_exact(4)
                .map(|v| [v[0] as u32, v[1] as u32, v[2] as u32, v[3] as u32])
                .collect();
            let mut load = [0.0; 3];
            unsafe {
                libc::getloadavg(load.as_mut_ptr(), 3);
            }
            Some(RawCpu { ticks, load })
        } else {
            None
        };
    if !data.is_null() {
        unsafe {
            libc::vm_deallocate(
                mach_task_self_,
                data as usize,
                len as usize * std::mem::size_of::<i32>(),
            );
        }
    }
    result
}
#[cfg(target_os = "macos")]
fn memory(total: u64) -> Option<MemorySample> {
    let host = Host::new();
    let mut stats: libc::vm_statistics64 = unsafe { std::mem::zeroed() };
    let mut count = libc::HOST_VM_INFO64_COUNT;
    if unsafe {
        libc::host_statistics64(
            host.0,
            libc::HOST_VM_INFO64,
            (&mut stats as *mut libc::vm_statistics64).cast(),
            &mut count,
        )
    } != 0
    {
        return None;
    }
    let page = unsafe { libc::sysconf(libc::_SC_PAGESIZE) };
    if page <= 0 {
        return None;
    }
    let page = page as u64;
    let compressed = u64::from(stats.compressor_page_count) * page;
    let wired = u64::from(stats.wire_count) * page;
    // Internal pages include purgeable pages; purgeable/file-backed pages belong to cached memory.
    let app = u64::from(
        stats
            .internal_page_count
            .saturating_sub(stats.purgeable_count),
    ) * page;
    let cached = u64::from(
        stats
            .external_page_count
            .saturating_add(stats.purgeable_count),
    ) * page;
    let swap = NativeSystemSource
        .read("vm.swapusage")
        .ok()
        .filter(|b| b.len() >= 24)
        .map(|b| u64::from_ne_bytes(b[16..24].try_into().unwrap_or_default()))
        .unwrap_or(0);
    let pressure = NativeSystemSource
        .read("kern.memorystatus_vm_pressure_level")
        .ok()
        .and_then(|b| <[u8; 4]>::try_from(b).ok())
        .map(u32::from_ne_bytes)
        .unwrap_or(1);
    Some(MemorySample {
        total_bytes: total,
        used_bytes: app
            .saturating_add(wired)
            .saturating_add(compressed)
            .min(total),
        app_bytes: app,
        wired_bytes: wired,
        compressed_bytes: compressed,
        cached_bytes: cached,
        swap_used_bytes: swap,
        pressure: if pressure & 4 != 0 {
            MemoryPressure::Critical
        } else if pressure & 2 != 0 {
            MemoryPressure::Warning
        } else {
            MemoryPressure::Normal
        },
    })
}
#[cfg(target_os = "macos")]
fn ctext(bytes: &[libc::c_char]) -> String {
    String::from_utf8_lossy(
        &bytes
            .iter()
            .take_while(|&&b| b != 0)
            .map(|&b| b as u8)
            .collect::<Vec<_>>(),
    )
    .into_owned()
}
#[cfg(target_os = "macos")]
fn disk(
    cache: &mut super::volume_cache::VolumeCache,
    media: &mut std::collections::BTreeMap<String, (u64, Option<super::extras::MediaInfo>)>,
    now: u64,
) -> Option<RawDisk> {
    let mut ptr = std::ptr::null_mut();
    let count = unsafe { libc::getmntinfo(&mut ptr, libc::MNT_NOWAIT) };
    if count <= 0 || ptr.is_null() {
        return None;
    }
    let mut volumes = Vec::new();
    let mut active_media = std::collections::BTreeSet::new();
    let mut active = std::collections::BTreeSet::new();
    let mounts = unsafe { std::slice::from_raw_parts(ptr, count as usize) }.to_vec();
    for original in &mounts {
        let path = ctext(&original.f_mntonname);
        if path != "/" && (!path.starts_with("/Volumes/") || path.starts_with("/Volumes/.")) {
            continue;
        }
        let local = original.f_flags & libc::MNT_LOCAL as u32 != 0;
        let visible = if local && path != "/" {
            let fsid: [i32; 2] = unsafe { std::mem::transmute(original.f_fsid) };
            let bsd = ctext(&original.f_mntfromname);
            let id = format!("{bsd}:{fsid:?}");
            active_media.insert(id.clone());
            let item = media
                .entry(id)
                .or_insert_with(|| (now, super::extras::media_info(&bsd)));
            if now.saturating_sub(item.0) >= 60_000 {
                *item = (now, super::extras::media_info(&bsd));
            }
            super::extras::visible_volume(&path, local, item.1.as_ref())
        } else {
            super::extras::visible_volume(&path, local, None)
        };
        if !visible {
            continue;
        }
        let mut mount = *original;
        if mount.f_flags & libc::MNT_LOCAL as u32 != 0 {
            let path = std::ffi::CString::new(ctext(&mount.f_mntonname)).ok()?;
            let mut fresh = unsafe { std::mem::zeroed::<libc::statfs>() };
            if unsafe { libc::statfs(path.as_ptr(), &mut fresh) } == 0 {
                mount = fresh;
            }
        }
        let path = ctext(&mount.f_mntonname);
        if path != "/" && (!path.starts_with("/Volumes/") || path.starts_with("/Volumes/.")) {
            continue;
        }
        let block = u64::from(mount.f_bsize);
        let total = mount.f_blocks.saturating_mul(block);
        if total == 0 {
            continue;
        }
        // Mounted network volumes use cached mount stats only: NSURL metadata could contact a server.
        let (important, display_name, internal) = if mount.f_flags & libc::MNT_LOCAL as u32 != 0 {
            // fsid is two initialized Darwin int32 values; the private libc fields have no accessor.
            let fsid: [i32; 2] = unsafe { std::mem::transmute(mount.f_fsid) };
            let id = format!("{path}:{}:{fsid:?}", ctext(&mount.f_mntfromname));
            active.insert(id.clone());
            let resources = cache.resolve(
                &id,
                now,
                total,
                mount.f_bavail.saturating_mul(block),
                || {
                    let (important, name, internal) = volume_resources(&path);
                    super::volume_cache::Resources {
                        important,
                        name,
                        internal,
                    }
                },
            );
            (resources.important, resources.name, resources.internal)
        } else {
            (None, None, Some(false))
        };
        let free = important
            .unwrap_or_else(|| mount.f_bavail.saturating_mul(block))
            .min(total);
        let name = display_name.unwrap_or_else(|| {
            std::path::Path::new(&path)
                .file_name()
                .and_then(|n| n.to_str())
                .unwrap_or(&path)
                .to_owned()
        });
        volumes.push(VolumeInfo {
            name,
            mount_point: path.clone(),
            total_bytes: total,
            free_bytes: free,
            internal: internal.unwrap_or(path == "/"),
        });
    }
    media.retain(|id, _| active_media.contains(id));
    cache.retain(&active);
    if volumes.is_empty() {
        return None;
    }
    let mut ids = Vec::new();
    let mut read = 0u64;
    let mut write = 0u64;
    for (id, statistics) in cf::property_services("IOBlockStorageDriver", "Statistics") {
        if let (Some(r), Some(w)) = (
            cf::n(statistics.0, "Bytes (Read)"),
            cf::n(statistics.0, "Bytes (Write)"),
        ) {
            ids.push(id);
            read = read.saturating_add(r.max(0.0) as u64);
            write = write.saturating_add(w.max(0.0) as u64);
        }
    }
    ids.sort_unstable();
    Some(RawDisk {
        volumes,
        counters: (!ids.is_empty()).then(|| (format!("{ids:?}"), read, write)),
    })
}
#[cfg(target_os = "macos")]
#[link(name = "SystemConfiguration", kind = "framework")]
unsafe extern "C" {
    fn SCDynamicStoreCopyValue(store: cf::Ref, key: cf::Ref) -> cf::Ref;
    fn SCNetworkInterfaceCopyAll() -> cf::Ref;
    fn SCNetworkInterfaceGetBSDName(interface: cf::Ref) -> cf::Ref;
    fn SCNetworkInterfaceGetInterfaceType(interface: cf::Ref) -> cf::Ref;
}
#[cfg(target_os = "macos")]
fn network() -> Option<RawNetwork> {
    let primary = |name: &str| -> Option<String> {
        let key = cf::string(name)?;
        let value = cf::Owned::new(unsafe { SCDynamicStoreCopyValue(std::ptr::null(), key.0) })?;
        cf::text(cf::key(value.0, "PrimaryInterface"))
    };
    let interface = default_interface(libc::AF_INET)
        .or_else(|| primary("State:/Network/Global/IPv4"))
        .or_else(|| default_interface(libc::AF_INET6))
        .or_else(|| primary("State:/Network/Global/IPv6"));
    let Some(name) = interface.clone() else {
        return Some(RawNetwork {
            interface: None,
            kind: None,
            local_ip: None,
            down: 0,
            up: 0,
        });
    };
    let mut mib = [libc::CTL_NET, libc::PF_ROUTE, 0, 0, libc::NET_RT_IFLIST2, 0];
    let mut len = 0;
    if unsafe {
        libc::sysctl(
            mib.as_mut_ptr(),
            6,
            std::ptr::null_mut(),
            &mut len,
            std::ptr::null_mut(),
            0,
        )
    } != 0
        || len > 16 * 1024 * 1024
    {
        return None;
    }
    let mut buffer = vec![0u8; len];
    if unsafe {
        libc::sysctl(
            mib.as_mut_ptr(),
            6,
            buffer.as_mut_ptr().cast(),
            &mut len,
            std::ptr::null_mut(),
            0,
        )
    } != 0
        || len > buffer.len()
    {
        return None;
    }
    let cname = std::ffi::CString::new(name.as_str()).ok()?;
    let index = unsafe { libc::if_nametoindex(cname.as_ptr()) };
    let route_counts = super::routes::interface_counters(&buffer[..len], index);
    // macOS 27's route table can zero the high words even in if_data64. The link MIB's
    // IFDATA_GENERAL table retains full counters (verified against netstat on M1 Max).
    let mut data = unsafe { std::mem::zeroed::<libc::ifmibdata>() };
    let mut data_len = std::mem::size_of_val(&data);
    let mut link_mib = [
        libc::CTL_NET,
        libc::PF_LINK,
        libc::NETLINK_GENERIC,
        libc::IFMIB_IFDATA,
        index as i32,
        libc::IFDATA_GENERAL,
    ];
    let link_counts = (unsafe {
        libc::sysctl(
            link_mib.as_mut_ptr(),
            6,
            (&mut data as *mut libc::ifmibdata).cast(),
            &mut data_len,
            std::ptr::null_mut(),
            0,
        )
    } == 0
        && data_len == std::mem::size_of_val(&data)
        && ctext(&data.ifmd_name) == name)
        .then_some((data.ifmd_data.ifi_ibytes, data.ifmd_data.ifi_obytes));
    let (down, up) = link_counts.or(route_counts)?;
    let kind = if name.starts_with("utun") || name.starts_with("ppp") || name.starts_with("ipsec") {
        NetworkKind::Vpn
    } else {
        let mut result = NetworkKind::Other;
        if let Some(all) = cf::Owned::new(unsafe { SCNetworkInterfaceCopyAll() }) {
            for i in 0..unsafe { cf::CFArrayGetCount(all.0) } {
                let iface = unsafe { cf::CFArrayGetValueAtIndex(all.0, i) };
                if cf::text(unsafe { SCNetworkInterfaceGetBSDName(iface) }).as_deref()
                    == Some(&name)
                {
                    result = match cf::text(unsafe { SCNetworkInterfaceGetInterfaceType(iface) })
                        .as_deref()
                    {
                        Some("IEEE80211") => NetworkKind::Wifi,
                        Some("Ethernet") => NetworkKind::Ethernet,
                        _ => NetworkKind::Other,
                    };
                    break;
                }
            }
        }
        result
    };
    Some(RawNetwork {
        interface,
        kind: Some(kind),
        local_ip: local_ip(&name),
        down,
        up,
    })
}
#[cfg(target_os = "macos")]
fn local_ip(name: &str) -> Option<String> {
    let mut list = std::ptr::null_mut();
    if unsafe { libc::getifaddrs(&mut list) } != 0 {
        return None;
    }
    let mut current = list;
    let mut found = None;
    while !current.is_null() {
        let iface = unsafe { &*current };
        if !iface.ifa_addr.is_null()
            && unsafe { std::ffi::CStr::from_ptr(iface.ifa_name) }.to_bytes() == name.as_bytes()
        {
            let family = unsafe { (*iface.ifa_addr).sa_family } as i32;
            if family == libc::AF_INET || (family == libc::AF_INET6 && found.is_none()) {
                let mut host = [0i8; libc::NI_MAXHOST as usize];
                if unsafe {
                    libc::getnameinfo(
                        iface.ifa_addr,
                        (*iface.ifa_addr).sa_len as u32,
                        host.as_mut_ptr(),
                        host.len() as u32,
                        std::ptr::null_mut(),
                        0,
                        libc::NI_NUMERICHOST,
                    )
                } == 0
                {
                    found = Some(
                        unsafe { std::ffi::CStr::from_ptr(host.as_ptr()) }
                            .to_string_lossy()
                            .into_owned(),
                    );
                    if family == libc::AF_INET {
                        break;
                    }
                }
            }
        }
        current = iface.ifa_next;
    }
    unsafe {
        libc::freeifaddrs(list);
    }
    found
}
#[cfg(target_os = "macos")]
#[link(name = "IOKit", kind = "framework")]
unsafe extern "C" {
    fn IOPSCopyPowerSourcesInfo() -> cf::Ref;
    fn IOPSCopyPowerSourcesList(info: cf::Ref) -> cf::Ref;
    fn IOPSGetPowerSourceDescription(info: cf::Ref, source: cf::Ref) -> cf::Ref;
}
#[cfg(target_os = "macos")]
fn battery() -> Option<BatterySample> {
    let info = cf::Owned::new(unsafe { IOPSCopyPowerSourcesInfo() })?;
    let list = cf::Owned::new(unsafe { IOPSCopyPowerSourcesList(info.0) })?;
    let mut description = std::ptr::null();
    for i in 0..unsafe { cf::CFArrayGetCount(list.0) } {
        let source = unsafe { cf::CFArrayGetValueAtIndex(list.0, i) };
        let dict = unsafe { IOPSGetPowerSourceDescription(info.0, source) };
        if cf::text(cf::key(dict, "Type")).as_deref() == Some("InternalBattery")
            && cf::boolean(dict, "Is Present").unwrap_or(true)
        {
            description = dict;
            break;
        }
    }
    if description.is_null() {
        return None;
    }
    let current = cf::n(description, "Current Capacity")?;
    let max = cf::n(description, "Max Capacity")?;
    if max <= 0.0 {
        return None;
    }
    let registry = cf::services("AppleSmartBattery");
    let props = registry
        .first()
        .map(|(_, d)| d.0)
        .unwrap_or(std::ptr::null());
    let data = cf::key(props, "BatteryData");
    let capacity = super::battery::capacity(|k| cf::n(props, k), |k| cf::n(data, k));
    let temp = super::battery::temperature(
        cf::n(data, "Temperature").or_else(|| cf::n(props, "Temperature")),
    );
    let voltage = cf::n(props, "Voltage").map(|v| v / 1000.0);
    let amperage = cf::n(props, "InstantAmperage")
        .or_else(|| cf::n(props, "Amperage"))
        .map(|v| {
            // Some firmware publishes signed current as an unsigned 16/32-bit value.
            if v > i32::MAX as f64 {
                (v as u32 as i32) as f64
            } else if v > i16::MAX as f64 && v <= u16::MAX as f64 {
                (v as u16 as i16) as f64
            } else {
                v
            }
        });
    let on_ac = cf::text(cf::key(description, "Power Source State")).as_deref() == Some("AC Power");
    let charging = cf::boolean(description, "Is Charging").unwrap_or(false);
    let time_key = if charging {
        "AvgTimeToFull"
    } else {
        "AvgTimeToEmpty"
    };
    let remaining = if on_ac && !charging {
        None
    } else {
        super::battery::remaining_minutes([
            cf::n(data, time_key),
            cf::n(props, time_key),
            cf::n(props, "TimeRemaining"),
            cf::n(
                description,
                if charging {
                    "Time to Full Charge"
                } else {
                    "Time to Empty"
                },
            ),
        ])
    };
    Some(BatterySample {
        percent: ratio(current / max),
        charging,
        on_ac,
        minutes_remaining: remaining.map(number),
        health_percent: capacity.health.map(ratio),
        cycle_count: cf::n(props, "CycleCount").map(|v| v.max(0.0) as u32),
        design_capacity_mah: capacity.design_mah,
        max_capacity_mah: capacity.max_mah,
        temperature_c: temp.map(number),
        voltage_v: voltage.map(number),
        power_watts: voltage
            .zip(amperage)
            .map(|(v, a)| number((v * a / 1000.0).abs())),
        adapter_watts: cf::n(cf::key(props, "AdapterDetails"), "Watts").map(number),
    })
}

#[cfg(target_os = "macos")]
fn volume_resources(path: &str) -> (Option<u64>, Option<String>, Option<bool>) {
    use objc2_foundation::{
        NSArray, NSNumber, NSString, NSURL, NSURLVolumeAvailableCapacityForImportantUsageKey,
        NSURLVolumeIsInternalKey, NSURLVolumeLocalizedNameKey,
    };
    let url = NSURL::fileURLWithPath(&NSString::from_str(path));
    let keys = unsafe {
        [
            NSURLVolumeAvailableCapacityForImportantUsageKey,
            NSURLVolumeLocalizedNameKey,
            NSURLVolumeIsInternalKey,
        ]
    };
    let Ok(values) = url.resourceValuesForKeys_error(&NSArray::from_slice(&keys)) else {
        return (None, None, None);
    };
    let free = values
        .objectForKey(keys[0])
        .and_then(|v| v.downcast::<NSNumber>().ok())
        .map(|v| v.unsignedLongLongValue());
    let name = values
        .objectForKey(keys[1])
        .and_then(|v| v.downcast::<NSString>().ok())
        .map(|v| v.to_string());
    let internal = values
        .objectForKey(keys[2])
        .and_then(|v| v.downcast::<NSNumber>().ok())
        .map(|v| v.boolValue());
    (free, name, internal)
}

#[cfg(target_os = "macos")]
fn default_interface(family: i32) -> Option<String> {
    let mut mib = [
        libc::CTL_NET,
        libc::PF_ROUTE,
        0,
        family,
        libc::NET_RT_DUMP2,
        0,
    ];
    let mut len = 0;
    if unsafe {
        libc::sysctl(
            mib.as_mut_ptr(),
            6,
            std::ptr::null_mut(),
            &mut len,
            std::ptr::null_mut(),
            0,
        )
    } != 0
        || len > 16 * 1024 * 1024
    {
        return None;
    }
    let mut bytes = vec![0u8; len];
    if unsafe {
        libc::sysctl(
            mib.as_mut_ptr(),
            6,
            bytes.as_mut_ptr().cast(),
            &mut len,
            std::ptr::null_mut(),
            0,
        )
    } != 0
        || len > bytes.len()
    {
        return None;
    }
    let mut offset = 0;
    while offset + std::mem::size_of::<libc::rt_msghdr2>() <= len {
        let header = unsafe {
            std::ptr::read_unaligned(bytes.as_ptr().add(offset).cast::<libc::rt_msghdr2>())
        };
        let size = header.rtm_msglen as usize;
        let start = offset + std::mem::size_of::<libc::rt_msghdr2>();
        if size < std::mem::size_of::<libc::rt_msghdr2>() || offset + size > len {
            return None;
        }
        if header.rtm_flags & libc::RTF_UP != 0
            && header.rtm_flags & (libc::RTF_IFSCOPE | libc::RTF_HOST) == 0
            && super::routes::is_default(
                &bytes[start..offset + size],
                header.rtm_addrs as u32,
                family as u8,
            )
        {
            let mut name = [0i8; libc::IF_NAMESIZE];
            if !unsafe { libc::if_indextoname(u32::from(header.rtm_index), name.as_mut_ptr()) }
                .is_null()
            {
                return Some(
                    unsafe { std::ffi::CStr::from_ptr(name.as_ptr()) }
                        .to_string_lossy()
                        .into_owned(),
                );
            }
        }
        offset += size;
    }
    None
}

#[cfg(target_os = "macos")]
fn gpu() -> Option<GpuSample> {
    let usage = cf::property_services("IOAccelerator", "PerformanceStatistics")
        .into_iter()
        .filter_map(|(_, v)| cf::n(v.0, "Device Utilization %"))
        .filter(|v| (0.0..=100.0).contains(v))
        .reduce(f64::max)?;
    Some(GpuSample {
        power_watts: None,
        usage: ratio(usage / 100.0),
    })
}
