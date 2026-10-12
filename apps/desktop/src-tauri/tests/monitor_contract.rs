use opennavo_desktop_lib::{AppError, model::*, monitor, settings};
use std::collections::BTreeMap;

struct FakeSysctl(BTreeMap<&'static str, Vec<u8>>);
impl monitor::system::SystemSource for FakeSysctl {
    fn read(&self, key: &str) -> Result<Vec<u8>, AppError> {
        self.0
            .get(key)
            .cloned()
            .ok_or_else(|| AppError::new("E_MONITOR_UNAVAILABLE", key))
    }
}
fn source() -> FakeSysctl {
    let mut boot = 1_700_000_000i64.to_ne_bytes().to_vec();
    boot.extend(123_456i32.to_ne_bytes());
    boot.extend([0u8; 4]);
    FakeSysctl(BTreeMap::from([
        ("hw.model", b"MacTest,1\0".to_vec()),
        ("machdep.cpu.brand_string", b"Apple Test Chip\0".to_vec()),
        ("hw.logicalcpu", 12u32.to_ne_bytes().to_vec()),
        (
            "hw.memsize",
            (32u64 * 1024 * 1024 * 1024).to_ne_bytes().to_vec(),
        ),
        ("kern.osproductversion", b"26.0\0".to_vec()),
        ("kern.boottime", boot),
        ("hw.perflevel0.physicalcpu", 8u32.to_ne_bytes().to_vec()),
        ("hw.perflevel1.physicalcpu", 4u32.to_ne_bytes().to_vec()),
    ]))
}

#[test]
fn static_snapshot_uses_sysctl_and_emits_no_synthetic_metrics() {
    let snapshot = monitor::snapshot(&source()).unwrap();
    assert!(snapshot.available.is_empty());
    assert!(snapshot.history.is_empty());
    let info = snapshot.system;
    assert_eq!(info.model_name, "MacTest,1");
    assert_eq!(info.model_name, info.model_id);
    assert_eq!(info.chip, "Apple Test Chip");
    assert_eq!(info.performance_cores, Some(8));
    assert_eq!(info.efficiency_cores, Some(4));
    assert_eq!(info.logical_cores, 12);
    assert_eq!(info.gpu_cores, None);
    assert_eq!(info.memory_bytes, 34_359_738_368);
    assert_eq!(info.booted_at, 1_700_000_000_123);
    let json = serde_json::to_value(info).unwrap();
    assert_eq!(json["bootedAt"], 1_700_000_000_123i64);
    assert!(json["gpuCores"].is_null());
}

#[test]
fn optional_topology_degrades_and_intel_never_claims_efficiency_cores() {
    let mut source = source();
    source.0.remove("hw.perflevel1.physicalcpu");
    assert_eq!(
        monitor::snapshot(&source).unwrap().system.efficiency_cores,
        None
    );
    source
        .0
        .insert("machdep.cpu.brand_string", b"Intel Test CPU\0".to_vec());
    let info = monitor::snapshot(&source).unwrap().system;
    assert_eq!(info.performance_cores, None);
    assert_eq!(info.efficiency_cores, None);
}

#[test]
fn invalid_sysctl_lengths_strings_and_timevals_fail_without_panics() {
    for (key, bytes) in [
        ("hw.logicalcpu", vec![1]),
        ("hw.logicalcpu", 0u32.to_ne_bytes().to_vec()),
        ("hw.memsize", 0u64.to_ne_bytes().to_vec()),
        ("hw.model", vec![0xff]),
        ("hw.model", b"bad\0name".to_vec()),
        ("hw.model", vec![]),
        ("kern.boottime", vec![0; 8]),
        ("kern.boottime", vec![0xff; 16]),
    ] {
        let mut source = source();
        source.0.insert(key, bytes);
        assert_eq!(
            monitor::snapshot(&source).unwrap_err().code,
            "E_MONITOR_UNAVAILABLE"
        );
    }
    let mut source = source();
    source.0.remove("hw.model");
    assert_eq!(monitor::snapshot(&source).unwrap_err().message, "hw.model");
}

#[test]
fn legacy_settings_migrate_and_roundtrip_without_losing_existing_values() {
    let temp = tempfile::tempdir().unwrap();
    let path = temp.path().join("settings.json");
    // Explicit manual locale avoids depending on the test machine's locale.
    let old = serde_json::json!({
        "localeMode": "manual", "locale": "en-US", "autoCheck": false,
        "zapByDefault": false, "trayShowCount": false, "checkTime": "18:45",
        "cleanupReminder": "weekly", "cleanupProjectRoots": ["~/FixtureProjects"]
    });
    std::fs::write(&path, serde_json::to_vec(&old).unwrap()).unwrap();
    let settings = settings::load(&path).unwrap();
    assert!(!settings.auto_check);
    assert!(!settings.zap_by_default);
    assert!(!settings.tray_show_count);
    assert_eq!(settings.check_time, "18:45");
    assert_eq!(settings.cleanup_reminder, CleanupReminder::Weekly);
    assert_eq!(settings.cleanup_project_roots, ["~/FixtureProjects"]);
    assert_eq!(settings.monitor_menu_bar, MonitorMenuBar::default());
    assert_eq!(u32::from(settings.monitor_interval_ms), 2000);
    assert!(!settings.monitor_alerts);
    assert!(!settings.monitor_menu_bar_intro);
    assert!(settings.fan_last_manual.is_none());
    assert_eq!(settings::load(&path).unwrap(), settings);
    let json = serde_json::to_value(&settings).unwrap();
    let items = json["monitorMenuBar"].as_object().unwrap();
    // Fans have no item of their own: the sensors item shows the temperature, without a label.
    assert_eq!(items.len(), 7);
    for (module, item) in items {
        assert_eq!(item["enabled"], module == "cpu" || module == "memory");
        assert_eq!(
            item["style"],
            if module == "sensors" { "value" } else { "text" }
        );
    }
}

#[test]
fn partial_monitor_settings_preserve_explicit_choices_and_strict_intervals() {
    let input = serde_json::json!({
        "monitorMenuBar": {
            "cpu": {"enabled": false, "style": "value"},
            "sensors": {"enabled": true, "style": "chart"},
            "fans": {"enabled": true, "style": "bars"}
        },
        "monitorIntervalMs": 5000, "monitorAlerts": true, "monitorMenuBarIntro": true,
        "fanLastManual": {"0": 2000, "1": 2500}
    });
    let settings: Settings = serde_json::from_value(input).unwrap();
    assert!(!settings.monitor_menu_bar.cpu.enabled);
    assert_eq!(settings.monitor_menu_bar.cpu.style, MenuBarStyle::Value);
    assert!(settings.monitor_menu_bar.memory.enabled);
    assert!(settings.monitor_menu_bar.sensors.enabled);
    assert_eq!(settings.monitor_menu_bar.sensors.style, MenuBarStyle::Line);
    // A fans item saved by an earlier build is dropped: fans are part of the sensors item.
    assert!(!settings.monitor_menu_bar.item(MonitorModule::Fans).enabled);
    assert_eq!(u32::from(settings.monitor_interval_ms), 5000);
    assert!(settings.monitor_alerts && settings.monitor_menu_bar_intro);
    assert_eq!(settings.fan_last_manual.as_ref().unwrap()[&1], 2500);
    let json = serde_json::to_value(&settings).unwrap();
    assert!(json["monitorMenuBar"].get("fans").is_none());
    assert_eq!(serde_json::from_value::<Settings>(json).unwrap(), settings);
    for value in [0, 999, 3000, 5001, -1] {
        assert!(
            serde_json::from_value::<Settings>(serde_json::json!({"monitorIntervalMs": value}))
                .is_err()
        );
    }
    for value in [1000, 2000, 5000] {
        assert!(
            serde_json::from_value::<Settings>(serde_json::json!({"monitorIntervalMs": value}))
                .is_ok()
        );
    }
}

#[test]
fn manual_fan_control_is_open_on_known_chips_and_never_unlocks_unsupported_hardware() {
    assert!(monitor::manual_allowed(FanControlSupport::Verified));
    assert!(monitor::manual_allowed(FanControlSupport::Unverified));
    assert!(!monitor::manual_allowed(FanControlSupport::Unsupported));
}

#[test]
fn popover_permissions_are_minimal_and_main_has_every_monitor_command() {
    let acl = include_str!("../permissions/native.toml");
    let main = acl
        .split("[[permission]]")
        .find(|s| s.contains("identifier = \"native-main\""))
        .unwrap();
    let popover = acl
        .split("[[permission]]")
        .find(|s| s.contains("identifier = \"native-tray-monitor\""))
        .unwrap();
    let shared = [
        "monitor_subscribe",
        "monitor_unsubscribe",
        "monitor_open_in_main",
        "fan_helper_status",
        "fan_helper_open_approval",
        "fan_helper_install",
        "fan_helper_uninstall",
        "fan_set_mode",
        "fan_set_speed",
        "fan_restore_last",
    ];
    for command in shared {
        assert!(main.contains(&format!("\"{command}\"")));
        assert!(popover.contains(&format!("\"{command}\"")));
    }
    for command in [
        "monitor_trend",
        "monitor_quit_app",
        "monitor_open_activity_monitor",
        "monitor_menu_bar_status",
        "monitor_open_menu_bar_settings",
    ] {
        assert!(main.contains(&format!("\"{command}\"")));
        assert!(!popover.contains(&format!("\"{command}\"")));
    }
    // Check the exact command list, so unrelated commands cannot accidentally widen the popover ACL.
    let list = popover
        .split_once("commands.allow = [")
        .unwrap()
        .1
        .split_once(']')
        .unwrap()
        .0;
    let actual: Vec<_> = list
        .split(',')
        .map(|s| s.trim().trim_matches('"'))
        .filter(|s| !s.is_empty())
        .collect();
    assert_eq!(actual.len(), shared.len());
    assert!(actual.iter().all(|s| shared.contains(s)));
    let cap: serde_json::Value =
        serde_json::from_str(include_str!("../capabilities/tray-monitor.json")).unwrap();
    assert_eq!(cap["windows"], serde_json::json!(["tray-monitor"]));
    assert_eq!(
        cap["permissions"],
        serde_json::json!([
            "native-tray-monitor",
            "core:event:default",
            "core:window:allow-hide",
            "core:window:allow-set-size"
        ])
    );
    assert_eq!(monitor::POPOVER_LABEL, "tray-monitor");
    assert_eq!(monitor::POPOVER_ROUTE, "index.html#/tray-monitor");
}

#[test]
fn monitor_numbers_never_serialize_nonfinite_values_as_null() {
    for value in [f64::NAN, f64::INFINITY, f64::NEG_INFINITY] {
        assert!(MonitorNumber::try_from(value).is_err());
    }
    let value = MonitorNumber::try_from(0.125).unwrap();
    assert_eq!(
        serde_json::to_value(value).unwrap(),
        serde_json::json!(0.125)
    );
    assert!(serde_json::from_str::<MonitorNumber>("null").is_err());
}

#[test]
fn legacy_chart_settings_migrate_to_line_without_losing_other_preferences() {
    let mut legacy = serde_json::to_value(Settings::default()).unwrap();
    legacy["monitorMenuBar"]["cpu"]["style"] = serde_json::json!("chart");
    legacy["monitorMenuBar"]["sensors"]["style"] = serde_json::json!("bars");
    legacy["monitorAlerts"] = serde_json::json!(true);
    legacy["fanLastManual"] = serde_json::json!({"0": 2400});
    let migrated: Settings = serde_json::from_value(legacy.clone()).unwrap();
    assert_eq!(migrated.monitor_menu_bar.cpu.style, MenuBarStyle::Line);
    assert_eq!(migrated.monitor_menu_bar.sensors.style, MenuBarStyle::Bars);
    legacy["monitorMenuBar"]["cpu"]["style"] = serde_json::json!("line");
    assert_eq!(serde_json::to_value(migrated).unwrap(), legacy);
}

#[test]
fn m3_nullable_contract_round_trips_units_and_smart_subject() {
    let point: MonitorTrendPoint = serde_json::from_value(serde_json::json!({
        "at": 1700000000000_i64, "cpu": 0.25, "gpu": null, "memory": 0.5,
        "diskRead": 4096, "diskWrite": null, "netDown": null, "netUp": 1024,
        "temperature": 96, "fanRpm": 2400, "battery": null
    }))
    .unwrap();
    let encoded = serde_json::to_value(point).unwrap();
    assert_eq!(encoded["fanRpm"], 2400.0);
    assert!(encoded["gpu"].is_null());
    let device: DiskDevice = serde_json::from_value(serde_json::json!({
        "id": "disk-fixture", "name": "Fixture SSD", "internal": true,
        "smart": "failing", "percentUsed": 0.15, "temperatureC": null,
        "powerOnHours": null, "dataWrittenBytes": null, "mountPoints": ["/"]
    }))
    .unwrap();
    assert_eq!(device.smart, DiskSmartStatus::Failing);
    assert_eq!(f64::from(device.percent_used.unwrap()), 0.15);
    let issue = HealthIssue {
        key: HealthIssueKey::DiskSmart,
        level: HealthIssueLevel::Critical,
        value: 1.0.try_into().unwrap(),
        top_pid: None,
        subject: Some(device.name),
    };
    let encoded = serde_json::to_value(issue).unwrap();
    assert_eq!(encoded["key"], "disk_smart");
    assert_eq!(encoded["subject"], "Fixture SSD");
}
