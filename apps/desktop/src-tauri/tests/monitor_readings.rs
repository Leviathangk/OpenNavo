use opennavo_desktop_lib::{
    AppError,
    monitor::{
        battery, sensors,
        smc::{Smc, Value},
        types::*,
    },
};
use std::collections::BTreeMap;

#[derive(Default)]
struct FakeSmc(BTreeMap<String, f32>);
impl Smc for FakeSmc {
    fn read(&mut self, key: &str) -> Option<Value> {
        self.0.get(key).map(|v| Value {
            kind: *b"flt ",
            bytes: v.to_le_bytes().to_vec(),
        })
    }
    fn write(&mut self, _: &str, _: &[u8]) -> Result<(), AppError> {
        panic!("thermal collectors must never write SMC");
    }
}

#[test]
fn m1_smc_cpu_zones_merge_with_hid_and_feed_health_and_helper_safety() {
    let mut smc = FakeSmc(BTreeMap::from([
        ("Tp01".into(), 97.0),
        ("Tp05".into(), 98.0),
        ("Tp09".into(), 74.0),
        ("Tp0T".into(), 75.0),
        ("TpZZ".into(), 149.0),
    ]));
    let zones = sensors::apple_readings(
        "Apple M1 Max",
        [
            ("PMU tdie0".into(), 57.6),
            ("gas gauge battery".into(), 37.1),
        ],
        Some(&mut smc),
    )
    .unwrap();
    assert_eq!(
        zones.iter().filter(|s| s.group == SensorGroup::Cpu).count(),
        2
    );
    assert_eq!(sensors::safety_temperature(&zones), Some(98.0));
    let mut sample: MonitorSample = serde_json::from_value(serde_json::json!({
        "at":0,"cpu":{"usage":0.44,"user":0.4,"system":0.04,"cores":[],"load":[0,0,0],"powerWatts":null},
        "gpu":null,"memory":{"totalBytes":100,"usedBytes":50,"appBytes":0,"wiredBytes":0,"compressedBytes":0,"cachedBytes":0,"swapUsedBytes":0,"pressure":"normal"},
        "disk":{"volumes":[],"readBytesPerSec":0,"writeBytesPerSec":0,"devices":null},
        "network":{"interface":null,"kind":null,"localIp":null,"downBytesPerSec":0,"upBytesPerSec":0,"totalDownBytes":0,"totalUpBytes":0},
        "battery":null,"sensors":zones,"fans":null,"processes":null,"health":{"level":"good","issues":[]}
    })).unwrap();
    let mut tracker = opennavo_desktop_lib::monitor::health::HealthTracker::default();
    let modules = [MonitorModule::Sensors].into_iter().collect();
    assert!(tracker.evaluate(&sample, &modules, 0, 0).issues.is_empty());
    sample.at = 10_000;
    let health = tracker.evaluate(&sample, &modules, 10_000, 0);
    assert!(
        health
            .issues
            .iter()
            .any(|i| i.key == HealthIssueKey::Temperature && f64::from(i.value) == 98.0)
    );
}

#[test]
fn hid_mtr_names_are_case_insensitive_and_unmapped_pmu_cannot_unlock_manual() {
    let zones = sensors::aggregate([
        ("pACC MTR Temp Sensor0".into(), 96.0),
        ("PACC MTR Temp Sensor1".into(), 98.0),
        ("eACC MTR Temp Sensor0".into(), 72.0),
        ("GPU MTR Temp Sensor0".into(), 67.0),
        ("SOC MTR Temp Sensor0".into(), 60.0),
        ("PMGR SOC Die Temp Sensor0".into(), 62.0),
    ])
    .unwrap();
    assert_eq!(zones.len(), 4);
    assert_eq!(sensors::safety_temperature(&zones), Some(98.0));
    let zones =
        sensors::aggregate([("PMU tdie0".into(), 57.6), ("PMU tdev0".into(), 70.0)]).unwrap();
    assert_eq!(sensors::safety_temperature(&zones), None);
    let mut smc = FakeSmc(BTreeMap::from([("Tp01".into(), 98.0)]));
    assert!(
        sensors::apple_readings("Apple M2", [], Some(&mut smc)).is_none(),
        "never apply M1 key meanings to another chip"
    );
    assert_eq!(
        sensors::safety_temperature(
            &sensors::apple_readings("Apple M1", [], Some(&mut smc)).unwrap()
        ),
        Some(98.0)
    );
}

#[test]
fn modern_battery_uses_nested_nominal_mah_and_never_percent_as_mah() {
    let top = |k: &str| (k == "MaxCapacity").then_some(100.0);
    let data = |k: &str| match k {
        "DesignCapacity" => Some(6075.0),
        "NominalChargeCapacity" => Some(4950.0),
        "FullChargeCapacity" => Some(4800.0),
        _ => None,
    };
    let c = battery::capacity(top, data);
    assert_eq!(c.design_mah, Some(6075));
    assert_eq!(c.max_mah, Some(4950));
    assert!((c.health.unwrap() - 4950.0 / 6075.0).abs() < 1e-12);
    let c = battery::capacity(|k| (k == "MaxCapacity").then_some(82.0), |_| None);
    assert_eq!(c.max_mah, None);
    assert_eq!(c.health, Some(0.82));
}

#[test]
fn legacy_battery_raw_capacity_time_sentinel_and_absent_temperature() {
    let c = battery::capacity(
        |k| match k {
            "DesignCapacity" => Some(6000.0),
            "AppleRawMaxCapacity" => Some(5000.0),
            "MaxCapacity" => Some(100.0),
            _ => None,
        },
        |_| None,
    );
    assert_eq!(c.max_mah, Some(5000));
    assert_eq!(c.health, Some(5.0 / 6.0));
    assert_eq!(
        battery::remaining_minutes([Some(65535.0), Some(-1.0), Some(0.0)]),
        None
    );
    assert_eq!(
        battery::remaining_minutes([Some(65535.0), Some(42.0)]),
        Some(42.0)
    );
    assert_eq!(battery::temperature(None), None);
    assert_eq!(battery::temperature(Some(65535.0)), None);
}

#[cfg(target_os = "macos")]
mod mac {
    use opennavo_desktop_lib::monitor::{
        extras::{EnergyChannel, MediaInfo, energy_watts, visible_volume},
        routes,
    };
    use std::ffi::{CStr, CString, c_char};
    unsafe extern "C" {
        fn on_monitor_media_fixture(json: *const c_char) -> *mut c_char;
        fn on_monitor_mount_fixture(path: *const c_char, roles: *const c_char) -> i32;
    }
    fn media(nodes: serde_json::Value) -> MediaInfo {
        let text = CString::new(nodes.to_string()).unwrap();
        let raw = unsafe { on_monitor_media_fixture(text.as_ptr()) };
        assert!(!raw.is_null());
        let result = serde_json::from_slice(unsafe { CStr::from_ptr(raw).to_bytes() }).unwrap();
        unsafe { nix::libc::free(raw.cast()) };
        result
    }
    fn energy(name: &str, unit: &str, amount: i64) -> EnergyChannel {
        EnergyChannel {
            name: name.into(),
            unit: unit.into(),
            energy: amount,
        }
    }
    #[test]
    fn cpu_power_sums_disjoint_cluster_totals_without_aggregate_core_or_dtl_double_counting() {
        let channels = [
            energy("EACC_CPU", "mJ", 2000),
            energy("PACC0_CPU", "mJ", 6000),
            energy("PACC1_CPU", "mJ", 10000),
            energy("CPU Energy", "mJ", 0),
            energy("PACC0_CPU0", "mJ", 8000),
            energy("PCPUDTL00", "mJ", 100000),
            energy("GPU Energy", "nJ", 540000000),
        ];
        assert_eq!(energy_watts(&channels, 2.0), (Some(9.0), Some(0.27)));
        assert_eq!(
            energy_watts(
                &[
                    energy("ECPU", "J", 2),
                    energy("PCPU0", "mJ", 6000),
                    energy("PCPU1", "uJ", 10000000)
                ],
                2.0
            )
            .0,
            Some(9.0)
        );
    }
    #[test]
    fn missing_invalid_and_first_power_deltas_are_null() {
        assert_eq!(
            energy_watts(
                &[
                    energy("EACC_CPU", "mJ", 0),
                    energy("PACC0_CPU", "mJ", 0),
                    energy("PACC1_CPU", "mJ", 0),
                    energy("CPU Energy", "mJ", 0)
                ],
                2.0
            )
            .0,
            None,
            "all-zero CPU model on this M1 Max is unavailable, not measured zero watts"
        );
        assert_eq!(energy_watts(&[energy("CPU Energy", "mJ", 0)], 2.0).0, None);
        assert_eq!(
            energy_watts(&[energy("EACC_CPU", "unknown", 2000)], 2.0).0,
            None
        );
        assert_eq!(energy_watts(&[energy("EACC_CPU", "mJ", -1)], 2.0).0, None);
        assert_eq!(
            energy_watts(&[energy("EACC_CPU", "mJ", 2000)], 0.0),
            (None, None)
        );
        assert_eq!(
            energy_watts(&[energy("CPU Energy", "mJ", 12000)], 2.0).0,
            Some(6.0)
        );
    }
    #[test]
    fn packed_iflist2_counters_keep_high_words_and_reject_truncated_messages() {
        use nix::libc;
        let size = std::mem::size_of::<libc::if_msghdr2>();
        let mut msg = vec![0; size];
        msg[..2].copy_from_slice(&(size as u16).to_ne_bytes());
        msg[3] = libc::RTM_IFINFO2 as u8;
        let i = std::mem::offset_of!(libc::if_msghdr2, ifm_index);
        msg[i..i + 2].copy_from_slice(&14u16.to_ne_bytes());
        let d = std::mem::offset_of!(libc::if_msghdr2, ifm_data);
        for (o, n) in [
            (
                std::mem::offset_of!(libc::if_data64, ifi_ibytes),
                327_330_000_000u64,
            ),
            (
                std::mem::offset_of!(libc::if_data64, ifi_obytes),
                164_840_000_000,
            ),
        ] {
            msg[d + o..d + o + 8].copy_from_slice(&n.to_ne_bytes());
        }
        let mut stream = vec![5, 0, 0, 0, 0];
        stream.extend(&msg);
        assert_eq!(
            routes::interface_counters(&stream, 14),
            Some((327_330_000_000, 164_840_000_000))
        );
        assert_eq!(routes::interface_counters(&stream, 15), None);
        assert_eq!(
            routes::interface_counters(&stream[..stream.len() - 1], 14),
            None
        );
    }
    #[test]
    fn physical_smart_policy_excludes_images_even_with_fake_storage_ancestors() {
        let disk = media(
            serde_json::json!([{"class":"IOMedia"},{"class":"IOBlockStorageDevice","storage":true,"NVMe SMART Capable":true}]),
        );
        assert!(disk.physical);
        let usb = media(
            serde_json::json!([{"class":"IOMedia"},{"class":"USBDevice","Protocol Characteristics":{"Physical Interconnect":"USB"}}]),
        );
        assert!(usb.physical);
        for nodes in [
            serde_json::json!([{"class":"IOHDIXHDDrive","storage":true}]),
            serde_json::json!([{"class":"DiskImageDevice","NVMe SMART Capable":true}]),
            serde_json::json!([{"class":"IOMedia","Virtual":true},{"storage":true}]),
            serde_json::json!([{"class":"AppleAPFSMedia"}]),
        ] {
            assert!(!media(nodes).physical);
        }
    }
    #[test]
    fn volume_roles_filter_system_disks_images_and_simulators_but_keep_physical_and_cached_network_volumes()
     {
        let physical = MediaInfo {
            physical: true,
            roles: vec![],
        };
        assert!(visible_volume("/", true, None));
        assert!(
            visible_volume("/Volumes/Recovery", true, Some(&physical)),
            "a user's same-named disk must remain"
        );
        assert!(visible_volume("/Volumes/Share", false, None));
        assert!(!visible_volume(
            "/Library/Developer/CoreSimulator/Volumes/runtime",
            true,
            Some(&physical)
        ));
        assert!(!visible_volume(
            "/System/Volumes/Preboot",
            true,
            Some(&physical)
        ));
        assert!(!visible_volume(
            "/Volumes/Installer",
            true,
            Some(&MediaInfo {
                physical: false,
                roles: vec![]
            })
        ));
        for role in ["Recovery", "Preboot", "VM", "Update", "xART", "Hardware"] {
            let info = MediaInfo {
                physical: true,
                roles: vec![role.into()],
            };
            assert!(!visible_volume("/Volumes/SystemHidden", true, Some(&info)));
            let path = CString::new("/Volumes/SystemHidden").unwrap();
            let roles = CString::new(serde_json::to_string(&info.roles).unwrap()).unwrap();
            assert_eq!(
                unsafe { on_monitor_mount_fixture(path.as_ptr(), roles.as_ptr()) },
                0,
                "SMART mount list uses the same roles policy"
            );
        }
    }
}
