use opennavo_desktop_lib::{
    locales_gen::LOCALES,
    monitor::{
        alerts::{AlertTracker, content, route},
        types::*,
    },
};
fn health(key: HealthIssueKey, level: HealthIssueLevel) -> Health {
    Health {
        level: HealthLevel::Attention,
        issues: vec![HealthIssue {
            key,
            level,
            value: 0.75.try_into().unwrap(),
            top_pid: None,
            subject: None,
        }],
    }
}
fn clear() -> Health {
    Health {
        level: HealthLevel::Good,
        issues: vec![],
    }
}
#[test]
fn absent_attention_persistent_clear_reappearance_and_hour_cooldown_use_fake_clock() {
    let mut tracker = AlertTracker::default();
    tracker.set_enabled(true);
    let attention = health(HealthIssueKey::CpuBusy, HealthIssueLevel::Attention);
    assert!(tracker.evaluate(&clear(), 0).is_empty());
    assert_eq!(tracker.evaluate(&attention, 100).len(), 1);
    assert!(tracker.evaluate(&attention, 200).is_empty());
    assert!(tracker.evaluate(&attention, 3_600_100).is_empty());
    tracker.evaluate(&clear(), 3_600_101);
    assert_eq!(tracker.evaluate(&attention, 3_600_102).len(), 1);
    tracker.evaluate(&clear(), 3_600_103);
    assert!(tracker.evaluate(&attention, 3_600_104).is_empty());
    tracker.evaluate(&clear(), 7_200_101);
    assert!(tracker.evaluate(&attention, 7_200_101).is_empty());
    tracker.evaluate(&clear(), 7_200_102);
    assert_eq!(tracker.evaluate(&attention, 7_200_102).len(), 1);
}
#[test]
fn critical_transition_from_absent_or_attention_and_keys_have_independent_cooldowns() {
    let mut tracker = AlertTracker::default();
    tracker.set_enabled(true);
    let attention = health(HealthIssueKey::MemoryPressure, HealthIssueLevel::Attention);
    let critical = health(HealthIssueKey::MemoryPressure, HealthIssueLevel::Critical);
    assert_eq!(tracker.evaluate(&attention, 0).len(), 1);
    assert_eq!(tracker.evaluate(&critical, 3_600_000).len(), 1);
    assert!(tracker.evaluate(&critical, 7_200_000).is_empty());
    assert_eq!(
        tracker
            .evaluate(
                &health(HealthIssueKey::DiskSmart, HealthIssueLevel::Critical),
                7_200_001
            )
            .len(),
        1
    );
    tracker.evaluate(&clear(), 7_200_002);
    assert_eq!(tracker.evaluate(&critical, 7_200_003).len(), 1);
}
#[test]
fn info_disabled_and_upgrade_during_key_cooldown_never_notify() {
    let mut tracker = AlertTracker::default();
    let critical = health(HealthIssueKey::Temperature, HealthIssueLevel::Critical);
    assert!(tracker.evaluate(&critical, 0).is_empty());
    tracker.set_enabled(true);
    assert!(
        tracker
            .evaluate(&health(HealthIssueKey::Uptime, HealthIssueLevel::Info), 1)
            .is_empty()
    );
    assert_eq!(
        tracker
            .evaluate(
                &health(HealthIssueKey::Temperature, HealthIssueLevel::Attention),
                2
            )
            .len(),
        1
    );
    assert!(tracker.evaluate(&critical, 3).is_empty());
    tracker.set_enabled(false);
    assert!(tracker.evaluate(&critical, 4).is_empty());
    tracker.set_enabled(true);
    assert!(tracker.evaluate(&critical, 5).is_empty());
}
#[test]
fn notification_routes_and_placeholders_are_resolved_in_every_language() {
    assert_eq!(route(HealthIssueKey::DiskLow), "/cleanup");
    for key in [
        HealthIssueKey::CpuBusy,
        HealthIssueKey::MemoryPressure,
        HealthIssueKey::DiskSmart,
        HealthIssueKey::Temperature,
    ] {
        assert_eq!(route(key), "/monitor");
    }
    for locale in LOCALES {
        for key in [
            HealthIssueKey::CpuBusy,
            HealthIssueKey::MemoryPressure,
            HealthIssueKey::DiskLow,
            HealthIssueKey::BatteryHealth,
            HealthIssueKey::BatteryHot,
            HealthIssueKey::Temperature,
            HealthIssueKey::DiskSmart,
        ] {
            let mut issue = health(key, HealthIssueLevel::Critical).issues.remove(0);
            issue.subject = Some("Fixture SSD".into());
            if key == HealthIssueKey::Temperature {
                issue.value = 96.0.try_into().unwrap();
            }
            let (title, body) = content(&issue, locale).unwrap();
            assert!(!title.is_empty() && !body.contains('{'));
            if key == HealthIssueKey::Temperature {
                assert!(body.contains("96\u{a0}°C"));
            }
            if key == HealthIssueKey::DiskSmart {
                assert!(body.contains("Fixture SSD"));
            }
        }
    }
}
#[cfg(target_os = "macos")]
#[test]
fn nettop_parser_handles_header_order_first_totals_and_rejects_bad_counters() {
    use opennavo_desktop_lib::monitor::extras::{NETTOP_ARGS, parse_nettop};
    let values =
        parse_nettop(b",bytes_out,bytes_in,\nFixture.42,200,100,\nFixture.7,bad,0,\n").unwrap();
    assert_eq!(values[&42], (100, 200));
    assert_eq!(values.len(), 1);
    assert!(parse_nettop(b"permission denied").is_none());
    assert!(NETTOP_ARGS.contains(&"-n"));
    assert!(NETTOP_ARGS.contains(&"-P"));
}

#[cfg(target_os = "macos")]
#[test]
fn network_csv_timestamp_cannot_be_mistaken_for_pid_and_quoted_names_work() {
    use opennavo_desktop_lib::monitor::extras::parse_nettop;
    let values =
        parse_nettop(b"time,,bytes_in,bytes_out,\n12:34:56.123,\"Fixture, spaced.42\",100,200,\n")
            .unwrap();
    assert_eq!(values.len(), 1);
    assert_eq!(values[&42], (100, 200));
    assert!(!values.contains_key(&123));
}
