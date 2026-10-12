//! Compact, stable thermal zones. The M1 CPU fallback keys are attributed in third-party NOTICE.
use super::{collect::number, smc::Smc, types::*};
use std::collections::BTreeMap;
pub fn aggregate(readings: impl IntoIterator<Item = (String, f64)>) -> Option<Vec<SensorReading>> {
    let mut zones: BTreeMap<&str, (SensorGroup, &str, f64)> = BTreeMap::new();
    for (name, value) in readings {
        if !value.is_finite() || !(0.0..=150.0).contains(&value) || value == 0.0 {
            continue;
        }
        let n = name.to_ascii_lowercase();
        let zone = if n.contains("pacc") || n.contains("performance") {
            Some(("cpu-performance", SensorGroup::Cpu, "CPU performance cores"))
        } else if n.contains("eacc") || n.contains("efficiency") {
            Some(("cpu-efficiency", SensorGroup::Cpu, "CPU efficiency cores"))
        } else if n.contains("cpu") || n.starts_with("tc") {
            Some(("cpu", SensorGroup::Cpu, "CPU"))
        } else if n.contains("gpu") || n.contains("gacc") || n.starts_with("tg") {
            Some(("gpu", SensorGroup::Gpu, "GPU"))
        } else if n.contains("soc") || n.contains("die") {
            Some(("soc", SensorGroup::Soc, "SoC"))
        } else if n.contains("ssd") || n.contains("nand") {
            Some(("ssd", SensorGroup::Ssd, "SSD"))
        } else if n.contains("battery") || n.starts_with("tb") {
            Some(("battery", SensorGroup::Battery, "Battery"))
        } else {
            None
        };
        if let Some((key, group, label)) = zone {
            zones
                .entry(key)
                .and_modify(|v| v.2 = v.2.max(value))
                .or_insert((group, label, value));
        }
    }
    let values: Vec<_> = zones
        .into_iter()
        .map(|(key, (group, name, celsius))| SensorReading {
            key: key.into(),
            name: name.into(),
            group,
            celsius: number(celsius),
        })
        .collect();
    (!values.is_empty()).then_some(values)
}
pub fn intel(smc: &mut dyn Smc, keys: &[String]) -> Option<Vec<SensorReading>> {
    aggregate(keys.iter().filter(|k| k.starts_with('T')).filter_map(|k| {
        let v = smc.read(k)?;
        if &v.kind != b"sp78" && &v.kind != b"flt " {
            return None;
        }
        Some((k.clone(), v.number()?))
    }))
}

/// M1 CPU keys adapted from Stats 5df24006b2993228fdf23f72fec467bffb4d2106
/// Modules/Sensors/values.swift (MIT); full notice/license in resources/third-party/.
const M1_CPU: &[(&str, &str)] = &[
    ("Tp09", "CPU efficiency cores"),
    ("Tp0T", "CPU efficiency cores"),
    ("Tp01", "CPU performance cores"),
    ("Tp05", "CPU performance cores"),
    ("Tp0D", "CPU performance cores"),
    ("Tp0H", "CPU performance cores"),
    ("Tp0L", "CPU performance cores"),
    ("Tp0P", "CPU performance cores"),
    ("Tp0X", "CPU performance cores"),
    ("Tp0b", "CPU performance cores"),
];

/// Combine HID zones with chip-specific, read-only SMC CPU readings. PMU names never imply CPU.
pub fn apple_readings(
    chip: &str,
    readings: impl IntoIterator<Item = (String, f64)>,
    smc: Option<&mut dyn Smc>,
) -> Option<Vec<SensorReading>> {
    let mut readings: Vec<_> = readings.into_iter().collect();
    if chip.split_whitespace().any(|part| part == "M1")
        && let Some(smc) = smc
    {
        for &(key, name) in M1_CPU {
            if let Some(value) = smc.read(key)
                && matches!(&value.kind, b"flt " | b"sp78")
                && let Some(value) = value.number()
            {
                readings.push((name.into(), value));
            }
        }
    }
    aggregate(readings)
}

/// A cool PMU/SoC reading alone cannot establish that CPU thermal protection is safe.
pub fn safety_temperature(readings: &[SensorReading]) -> Option<f64> {
    readings
        .iter()
        .any(|s| s.group == SensorGroup::Cpu)
        .then(|| {
            readings
                .iter()
                .filter(|s| matches!(s.group, SensorGroup::Cpu | SensorGroup::Soc))
                .map(|s| f64::from(s.celsius))
                .reduce(f64::max)
        })
        .flatten()
}

#[cfg(target_os = "macos")]
pub fn apple_with_smc(chip: &str, smc: Option<&mut dyn Smc>) -> Option<Vec<SensorReading>> {
    apple_readings(
        chip,
        apple_raw()
            .unwrap_or_default()
            .into_iter()
            .filter_map(|(name, value)| value.map(|v| (name, v))),
        smc,
    )
}
#[cfg(target_os = "macos")]
pub fn apple() -> Option<Vec<SensorReading>> {
    aggregate(
        apple_raw()?
            .into_iter()
            .filter_map(|(name, value)| value.map(|v| (name, v))),
    )
}
#[cfg(target_os = "macos")]
pub fn apple_raw() -> Option<Vec<(String, Option<f64>)>> {
    // The private API is resolved at runtime, so removal cannot prevent OpenNavo from launching.
    use super::cf;
    use std::{ffi::c_void, ptr};
    type Create = unsafe extern "C" fn(cf::Ref) -> cf::Ref;
    type Matching = unsafe extern "C" fn(cf::Ref, cf::Ref);
    type Services = unsafe extern "C" fn(cf::Ref) -> cf::Ref;
    type Property = unsafe extern "C" fn(cf::Ref, cf::Ref) -> cf::Ref;
    type Event = unsafe extern "C" fn(cf::Ref, i64, i32, i64) -> cf::Ref;
    type Float = unsafe extern "C" fn(cf::Ref, i32) -> f64;
    unsafe {
        let lib = nix::libc::dlopen(
            c"/System/Library/Frameworks/IOKit.framework/IOKit".as_ptr(),
            nix::libc::RTLD_LAZY | nix::libc::RTLD_LOCAL,
        );
        if lib.is_null() {
            return None;
        }
        struct Library(*mut c_void);
        impl Drop for Library {
            fn drop(&mut self) {
                unsafe {
                    nix::libc::dlclose(self.0);
                }
            }
        }
        let _library = Library(lib);
        macro_rules! symbol {
            ($name:literal,$ty:ty) => {{
                let p = nix::libc::dlsym(lib, concat!($name, "\0").as_ptr().cast());
                if p.is_null() {
                    return None;
                }
                std::mem::transmute::<*mut c_void, $ty>(p)
            }};
        }
        let create = symbol!("IOHIDEventSystemClientCreate", Create);
        let matching = symbol!("IOHIDEventSystemClientSetMatching", Matching);
        let services = symbol!("IOHIDEventSystemClientCopyServices", Services);
        let property = symbol!("IOHIDServiceClientCopyProperty", Property);
        let event = symbol!("IOHIDServiceClientCopyEvent", Event);
        let float = symbol!("IOHIDEventGetFloatValue", Float);
        let client = cf::Owned::new(create(ptr::null()))?;
        #[link(name = "CoreFoundation", kind = "framework")]
        unsafe extern "C" {
            fn CFNumberCreate(allocator: cf::Ref, kind: i32, value: *const i32) -> cf::Ref;
            fn CFDictionaryCreate(
                allocator: cf::Ref,
                keys: *const cf::Ref,
                values: *const cf::Ref,
                count: isize,
                key_callbacks: *const usize,
                value_callbacks: *const usize,
            ) -> cf::Ref;
            static kCFTypeDictionaryKeyCallBacks: [usize; 6];
            static kCFTypeDictionaryValueCallBacks: [usize; 5];
        }
        let page = cf::Owned::new(CFNumberCreate(ptr::null(), 3, &0xff00))?;
        let usage = cf::Owned::new(CFNumberCreate(ptr::null(), 3, &5))?;
        let page_key = cf::string("PrimaryUsagePage")?;
        let usage_key = cf::string("PrimaryUsage")?;
        let keys = [page_key.0, usage_key.0];
        let values = [page.0, usage.0];
        let filter = cf::Owned::new(CFDictionaryCreate(
            ptr::null(),
            keys.as_ptr(),
            values.as_ptr(),
            2,
            ptr::addr_of!(kCFTypeDictionaryKeyCallBacks).cast(),
            ptr::addr_of!(kCFTypeDictionaryValueCallBacks).cast(),
        ))?;
        matching(client.0, filter.0);
        let list = cf::Owned::new(services(client.0))?;
        let key = cf::string("Product")?;
        let count = cf::CFArrayGetCount(list.0);
        if !(0..=4096).contains(&count) {
            return None;
        }
        let mut readings = vec![];
        for i in 0..count {
            let service = cf::CFArrayGetValueAtIndex(list.0, i);
            let Some(name) = cf::Owned::new(property(service, key.0)).and_then(|v| cf::text(v.0))
            else {
                continue;
            };
            let value = cf::Owned::new(event(service, 15, 0, 0)).map(|ev| float(ev.0, 15 << 16));
            readings.push((name, value.filter(|v| v.is_finite())));
        }
        Some(readings)
    }
}
