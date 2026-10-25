// SPDX-License-Identifier: GPL-3.0-only
use opennavo_firewall_core::{engine::*, matching::*, types::*};
use std::collections::HashSet;

fn app(name: &str) -> FirewallApp {
    let mut app = FirewallApp {
        key: String::new(),
        name: name.into(),
        path: format!("/fixtures/{name}"),
        bundle_path: None,
        bundle_id: None,
        signing_id: Some(format!("test.{name}")),
        team_id: Some("TESTTEAM".into()),
        team_name: None,
        signer: FirewallSigner::DeveloperId,
        icon_path: None,
    };
    app.key = identity_key(&app);
    app
}
fn flow(id: &str, host: &str) -> Flow {
    Flow {
        id: id.into(),
        app: app("tool"),
        baseline_owner: None,
        pid: 42,
        process_start: 10,
        args: vec![],
        ancestors: vec![],
        remote_address: "192.0.2.7".into(),
        remote_host: Some(host.into()),
        port: 443,
        protocol: FirewallProtocol::Tcp,
        at: 1_000,
        monotonic_ms: 1_000,
    }
}
fn engine() -> Engine {
    let mut engine = Engine::default();
    engine.connect(1_000);
    engine
}
fn rule(
    engine: &mut Engine,
    app: Option<FirewallApp>,
    remote: Option<&str>,
    port: Option<i64>,
    action: FirewallAction,
) -> FirewallRule {
    engine
        .save_rule(
            FirewallRuleInput {
                id: None,
                app_key: app.as_ref().map(|a| a.key.clone()),
                app_path: None,
                action,
                remote: remote.map(str::to_string),
                port,
            },
            app,
            1_000,
        )
        .unwrap()
}
#[test]
fn precedence_app_destination_then_global_destination_then_list_then_app_any() {
    let mut e = engine();
    let f = flow("one", "example.test");
    rule(
        &mut e,
        Some(f.app.clone()),
        None,
        None,
        FirewallAction::Allow,
    );
    e.blocklist_load(
        None,
        "/fixtures/list".into(),
        "list".into(),
        b"example.test",
        1_000,
    )
    .unwrap();
    assert_eq!(
        e.decide(&f).unwrap().reason,
        FirewallVerdictReason::Blocklist
    );
    rule(
        &mut e,
        None,
        Some("example.test"),
        None,
        FirewallAction::Allow,
    );
    assert_eq!(e.decide(&f).unwrap().action, FirewallAction::Allow);
    rule(
        &mut e,
        Some(f.app.clone()),
        None,
        Some(443),
        FirewallAction::Block,
    );
    assert_eq!(e.decide(&f).unwrap().action, FirewallAction::Block);
    rule(
        &mut e,
        Some(f.app.clone()),
        Some("example.test"),
        None,
        FirewallAction::Allow,
    );
    assert_eq!(e.decide(&f).unwrap().action, FirewallAction::Allow);
    rule(
        &mut e,
        Some(f.app.clone()),
        Some("example.test"),
        Some(443),
        FirewallAction::Block,
    );
    assert_eq!(e.decide(&f).unwrap().action, FirewallAction::Block);
}
#[test]
fn blocking_wins_ties_independent_of_insertion_order() {
    // Equally specific: an exact host and an exact IP, or a wildcard and a CIDR.
    for [allowed, blocked] in [
        ["example.test", "192.0.2.7"],
        ["192.0.2.7", "example.test"],
        ["*.test", "192.0.2.0/24"],
        ["192.0.2.0/24", "*.test"],
    ] {
        for block_first in [true, false] {
            let mut e = engine();
            let f = flow("one", "example.test");
            let mut save =
                |remote, action| rule(&mut e, Some(f.app.clone()), Some(remote), None, action);
            if block_first {
                save(blocked, FirewallAction::Block);
                save(allowed, FirewallAction::Allow);
            } else {
                save(allowed, FirewallAction::Allow);
                save(blocked, FirewallAction::Block);
            }
            assert_eq!(
                e.decide(&f).unwrap().action,
                FirewallAction::Block,
                "{allowed} / {blocked}"
            );
        }
    }
    // Entirely equal rules: the latest one decides.
    let mut e = engine();
    let f = flow("one", "example.test");
    rule(
        &mut e,
        Some(f.app.clone()),
        Some("example.test"),
        None,
        FirewallAction::Block,
    );
    let latest = rule(
        &mut e,
        Some(f.app.clone()),
        Some("192.0.2.7"),
        None,
        FirewallAction::Block,
    );
    assert_eq!(e.decide(&f).unwrap().rule, Some(latest.id));
}
#[test]
fn narrower_destinations_win_before_port_and_action() {
    use FirewallAction::{Allow, Block};
    type Rules = [(Option<&'static str>, Option<i64>, FirewallAction); 2];
    let cases: [(Rules, Option<&str>, &str, FirewallAction); 8] = [
        // An exact host outranks a wildcard even with a port.
        (
            [
                (Some("*.example.com"), Some(443), Allow),
                (Some("tracker.example.com"), None, Block),
            ],
            Some("tracker.example.com"),
            "192.0.2.7",
            Block,
        ),
        // An exact IP outranks any network.
        (
            [
                (Some("0.0.0.0/0"), Some(443), Allow),
                (Some("203.0.113.5"), None, Block),
            ],
            None,
            "203.0.113.5",
            Block,
        ),
        // A longer suffix or prefix is narrower.
        (
            [
                (Some("*.example.com"), None, Block),
                (Some("*.cdn.example.com"), None, Allow),
            ],
            Some("a.cdn.example.com"),
            "192.0.2.7",
            Allow,
        ),
        (
            [
                (Some("10.0.0.0/8"), None, Block),
                (Some("10.1.0.0/16"), None, Allow),
            ],
            None,
            "10.1.2.3",
            Allow,
        ),
        // Same destination: a port is narrower.
        (
            [
                (Some("example.com"), None, Allow),
                (Some("example.com"), Some(443), Block),
            ],
            Some("example.com"),
            "192.0.2.7",
            Block,
        ),
        // Any destination rule outranks a port-only rule.
        (
            [
                (None, Some(443), Block),
                (Some("*.example.com"), None, Allow),
            ],
            Some("a.example.com"),
            "192.0.2.7",
            Allow,
        ),
        // A wildcard and a CIDR don't outrank each other; the port decides.
        (
            [
                (Some("*.example.com"), Some(443), Allow),
                (Some("192.0.2.0/24"), None, Block),
            ],
            Some("a.example.com"),
            "192.0.2.7",
            Allow,
        ),
        // A full-length prefix names one address, like an exact IP.
        (
            [
                (Some("*.example.com"), None, Block),
                (Some("192.0.2.7/32"), None, Allow),
            ],
            Some("a.example.com"),
            "192.0.2.7",
            Allow,
        ),
    ];
    for (rules, host, address, expected) in cases {
        for global in [false, true] {
            for reversed in [false, true] {
                let mut e = engine();
                let mut f = flow("one", "unused.test");
                f.remote_host = host.map(str::to_string);
                f.remote_address = address.into();
                let mut ordered = rules.to_vec();
                if reversed {
                    ordered.reverse();
                }
                for (remote, port, action) in ordered {
                    rule(
                        &mut e,
                        (!global).then(|| f.app.clone()),
                        remote,
                        port,
                        action,
                    );
                }
                assert_eq!(
                    e.flow(f),
                    Some(expected),
                    "{rules:?} global={global} reversed={reversed}"
                );
            }
        }
    }
}
#[test]
fn rejects_global_any_and_invalid_ports_or_destinations() {
    for (remote, port) in [
        (None, None),
        (Some("https://example.test"), None),
        (Some("a..test"), None),
        (None, Some(0)),
        (None, Some(-1)),
        (None, Some(65536)),
    ] {
        let result = engine().save_rule(
            FirewallRuleInput {
                id: None,
                app_key: None,
                app_path: None,
                action: FirewallAction::Block,
                remote: remote.map(str::to_string),
                port,
            },
            None,
            0,
        );
        assert_eq!(result.unwrap_err(), FirewallErrorCode::RuleInvalid);
    }
}
#[test]
fn domains_cannot_match_an_ip_without_hostname() {
    assert!(!remote_matches("example.test", "192.0.2.7", None));
    assert!(remote_matches(
        "*.example.test",
        "192.0.2.7",
        Some("A.Example.Test.")
    ));
    assert!(!remote_matches(
        "*.example.test",
        "192.0.2.7",
        Some("example.test")
    ));
    assert!(!remote_matches(
        "*.example.test",
        "192.0.2.7",
        Some("notexample.test")
    ));
    assert!(remote_matches("192.0.2.0/24", "192.0.2.7", None));
    assert!(remote_matches("2001:db8::/32", "2001:db8::7", None));
    assert!(!remote_matches("192.0.2.0/24", "2001:db8::7", None));
    let list = BlockIndex::new(&["example.test".into(), "192.0.2.0/24".into()]);
    assert!(!list.matches("198.51.100.1", None));
    assert!(list.matches("192.0.2.7", None));
}
#[test]
fn graylisted_apple_tools_still_ask_even_when_in_baseline() {
    let mut e = engine();
    let mut f = flow("one", "example.test");
    f.app.signer = FirewallSigner::Apple;
    f.app.signing_id = Some("com.apple.curl".into());
    f.app.key = identity_key(&f.app);
    e.state.baseline.insert(f.app.key.clone());
    assert!(e.decide(&f).is_none());
    f.app.signing_id = Some("com.apple.Safari".into());
    f.app.key = identity_key(&f.app);
    assert_eq!(e.decide(&f).unwrap().reason, FirewallVerdictReason::Apple);
    assert_eq!(e.state.rules[0].source, FirewallRuleSource::Apple);
    e.set_settings(
        FirewallSettings {
            allow_apple: false,
            ..FirewallSettings::default()
        },
        1_001,
    );
    assert!(e.state.rules.is_empty());
    assert!(e.decide(&f).is_none());
}
#[test]
fn baseline_auto_allow_creates_one_rule_and_records_once() {
    let mut e = engine();
    let f = flow("one", "example.test");
    e.state.baseline.insert(f.app.key.clone());
    assert_eq!(e.flow(f.clone()), Some(FirewallAction::Allow));
    assert_eq!(e.state.rules[0].source, FirewallRuleSource::Installed);
    assert_eq!(e.state.events[0].reason, FirewallVerdictReason::Installed);
    assert_eq!(e.flow(f), Some(FirewallAction::Allow));
    assert_eq!(e.state.rules.len(), 1);
    assert_eq!(e.state.events[0].count, 1);
}

#[test]
fn unsigned_bundle_helpers_use_existing_baseline_but_keep_path_rule_keys() {
    for signer in [FirewallSigner::Adhoc, FirewallSigner::Unsigned] {
        let mut parent = app("proxy");
        parent.signer = signer;
        parent.team_id = None;
        parent.bundle_path = Some("/fixtures/Proxy.app".into());
        parent.path = "/fixtures/Proxy.app/Contents/MacOS/Proxy".into();
        parent.key = identity_key(&parent);
        let mut child = flow("core", "example.test");
        child.app = parent.clone();
        child.app.path = "/fixtures/Proxy.app/Contents/MacOS/ProxyCore".into();
        child.app.key = identity_key(&child.app);
        child.baseline_owner = Some(parent.clone());
        let mut e = engine();
        // A pre-upgrade baseline contains only the App's main executable.
        e.set_baseline(vec![parent.clone()]);
        e = Engine::restore(e.persistent()).unwrap();
        e.connect(1_000);
        assert_eq!(e.flow(child.clone()), Some(FirewallAction::Allow));
        assert_eq!(e.state.rules[0].source, FirewallRuleSource::Installed);
        assert_eq!(e.state.rules[0].app.as_ref().unwrap().key, child.app.key);
        assert_ne!(child.app.key, parent.key);
        assert_eq!(e.state.events[0].reason, FirewallVerdictReason::Installed);
        assert!(e.alerts().is_empty());
        e.set_settings(
            FirewallSettings {
                allow_installed: false,
                ..e.state.settings.clone()
            },
            1_000,
        );
        assert_eq!(e.flow(child.clone()), None);
        // Incremental baseline arrival also releases an already waiting helper.
        let mut e = engine();
        e.configure("first".into(), FirewallSettings::default(), false, 1_000);
        assert_eq!(e.flow(child), None);
        e.baseline_add(vec![parent], None, 1_001);
        assert!(e.alerts().is_empty());
        assert_eq!(e.resolutions[0].action, FirewallAction::Allow);
    }
}

#[test]
fn baseline_owner_cannot_cover_invalid_foreign_team_or_outside_executables() {
    let mut parent = app("proxy");
    parent.signer = FirewallSigner::Adhoc;
    parent.team_id = None;
    parent.bundle_path = Some("/fixtures/Proxy.app".into());
    parent.path = "/fixtures/Proxy.app/Contents/MacOS/Proxy".into();
    parent.key = identity_key(&parent);
    let mut original = flow("core", "example.test");
    original.app = parent.clone();
    original.app.path = "/fixtures/Proxy.app/Contents/MacOS/ProxyCore".into();
    original.baseline_owner = Some(parent.clone());
    for scenario in 0..7 {
        let mut child = original.clone();
        match scenario {
            0 => child.app.signer = FirewallSigner::Invalid,
            1 => {
                child.app.signer = FirewallSigner::DeveloperId;
                child.app.team_id = Some("OTHERTEAM".into());
            }
            2 => child.app.path = "/fixtures/Proxy.app.other/Core".into(),
            3 => child.baseline_owner.as_mut().unwrap().signer = FirewallSigner::Invalid,
            4 => child.baseline_owner.as_mut().unwrap().team_id = Some("OTHERTEAM".into()),
            5 => child.baseline_owner = None,
            _ => child.app.signer = FirewallSigner::Unsigned,
        }
        child.app.key = identity_key(&child.app);
        let mut e = engine();
        e.set_baseline(vec![parent.clone()]);
        assert_eq!(e.flow(child), None, "scenario {scenario}");
        assert!(e.state.rules.is_empty());
    }
}
#[test]
fn helper_identity_uses_outermost_same_team_only() {
    let child = app("helper");
    let mut outer = app("outer");
    outer.bundle_path = Some("/fixtures/Outer.app".into());
    assert_eq!(owner_app(&child, &[outer.clone()]).name, "outer");
    outer.team_id = Some("OTHERTEAM".into());
    assert_eq!(owner_app(&child, &[outer]).name, "helper");
    let mut unsigned = child.clone();
    unsigned.signer = FirewallSigner::Adhoc;
    assert_eq!(identity_key(&unsigned), "path:/fixtures/helper");
    assert_eq!(owner_app(&unsigned, &[app("outer")]).name, "helper");
    let mut renamed = child.clone();
    renamed.path = "/elsewhere/tool".into();
    assert_eq!(identity_key(&renamed), identity_key(&child));
}
#[test]
fn same_program_has_one_alert_and_destination_reply_requeues_nonmatches() {
    let mut e = engine();
    e.flow(flow("one", "a.test"));
    e.flow(flow("two", "b.test"));
    e.flow(flow("three", "a.test"));
    assert_eq!(e.alerts().len(), 1);
    assert_eq!(e.alerts()[0].waiting, 2);
    e.reply(
        FirewallReply {
            alert_id: "one".into(),
            action: FirewallAction::Allow,
            scope: FirewallReplyScope::Destination,
            until: FirewallRuleUntil::Always,
        },
        2_000,
    )
    .unwrap();
    assert_eq!(e.resolutions.len(), 2);
    assert_eq!(e.alerts()[0].id, "two");
    assert_eq!(e.alerts()[0].waiting, 0);
    assert_eq!(e.state.events[0].reason, FirewallVerdictReason::Answer);
    assert_eq!(e.state.events[0].count, 2);
    assert_eq!(
        e.reply(
            FirewallReply {
                alert_id: "one".into(),
                action: FirewallAction::Block,
                scope: FirewallReplyScope::App,
                until: FirewallRuleUntil::Always
            },
            2_001
        ),
        Err(FirewallErrorCode::UnknownAlert)
    );
}
#[test]
fn app_quits_rules_expire_and_do_not_match_reused_pid_or_survive_restart() {
    let mut e = engine();
    e.flow(flow("one", "a.test"));
    e.reply(
        FirewallReply {
            alert_id: "one".into(),
            action: FirewallAction::Allow,
            scope: FirewallReplyScope::App,
            until: FirewallRuleUntil::AppQuits,
        },
        2_000,
    )
    .unwrap();
    assert_eq!(e.state.rules.len(), 1);
    assert!(e.persistent().rules.is_empty());
    let mut reused = flow("two", "a.test");
    reused.process_start = 11;
    assert!(e.decide(&reused).is_none());
    e.connect(2_000);
    e.tick(2_000, 2_000, &HashSet::from([(42, 11)]));
    assert!(e.state.rules.is_empty());
}
#[test]
fn disconnected_client_releases_questions_and_bypasses_unmatched_block_mode() {
    let mut e = engine();
    let f = flow("one", "a.test");
    e.flow(f.clone());
    e.disconnect(2_000);
    assert!(e.alerts().is_empty());
    assert_eq!(e.resolutions[0].action, FirewallAction::Allow);
    assert!(e.state.rules.is_empty());
    e.state.settings.mode = FirewallMode::Block;
    assert_eq!(e.flow(f), Some(FirewallAction::Allow));
    assert_eq!(e.state.events[0].reason, FirewallVerdictReason::NoClient);
}

fn offline_flow(heartbeat_loss: bool) -> (Engine, Flow) {
    let mut e = engine();
    if !heartbeat_loss {
        e.disconnect(2_000);
    }
    let mut f = flow("offline", "example.test");
    // Exercise stale heartbeat detection without relying on a prior timer tick.
    f.monotonic_ms += HEARTBEAT_TIMEOUT_MS;
    f.at = f.monotonic_ms as i64;
    assert!(!e.connected(f.monotonic_ms));
    (e, f)
}

#[test]
fn offline_rules_keep_allow_and_block_actions_at_every_stage() {
    for heartbeat_loss in [false, true] {
        for stage in [1, 2, 4] {
            for action in [FirewallAction::Allow, FirewallAction::Block] {
                let (mut e, f) = offline_flow(heartbeat_loss);
                e.state.settings.mode = FirewallMode::Block;
                rule(
                    &mut e,
                    (stage != 2).then_some(f.app.clone()),
                    (stage != 4).then_some("example.test"),
                    None,
                    action,
                );
                assert_eq!(e.flow(f), Some(action));
                assert_eq!(e.state.rules[0].match_count, 1);
                assert!(e.alerts().is_empty());
                if action == FirewallAction::Block {
                    assert_eq!(e.state.events[0].reason, FirewallVerdictReason::Rule);
                } else {
                    assert!(e.state.events.is_empty()); // Ordinary rule allows aren't logged.
                }
            }
        }
    }
}

#[test]
fn offline_blocklists_preserve_precedence_over_app_any_and_automatic_allow() {
    for heartbeat_loss in [false, true] {
        let (mut e, mut f) = offline_flow(heartbeat_loss);
        f.app.signer = FirewallSigner::Apple;
        f.app.key = identity_key(&f.app);
        let list = e
            .blocklist_load(
                None,
                "/fixtures/list".into(),
                "list".into(),
                b"example.test",
                f.at,
            )
            .unwrap();
        assert_eq!(e.flow(f.clone()), Some(FirewallAction::Block));
        assert!(e.state.rules.is_empty()); // List wins over automatic Apple allow.
        assert_eq!(e.state.events[0].reason, FirewallVerdictReason::Blocklist);
        rule(
            &mut e,
            Some(f.app.clone()),
            None,
            None,
            FirewallAction::Allow,
        );
        assert_eq!(e.flow(f.clone()), Some(FirewallAction::Block));
        // A global destination allow wins over the list, and app-specific wins over global.
        rule(
            &mut e,
            None,
            Some("example.test"),
            None,
            FirewallAction::Allow,
        );
        assert_eq!(e.flow(f.clone()), Some(FirewallAction::Allow));
        rule(
            &mut e,
            Some(f.app.clone()),
            Some("example.test"),
            None,
            FirewallAction::Block,
        );
        assert_eq!(e.flow(f.clone()), Some(FirewallAction::Block));
        assert_eq!(e.state.events[0].reason, FirewallVerdictReason::Rule);
        // Disabled lists must not become active merely because the client is absent.
        e.state.rules.clear();
        e.blocklist_set(&list.id, false, f.at).unwrap();
        assert_eq!(e.flow(f), Some(FirewallAction::Allow));
        assert_eq!(e.state.events[0].reason, FirewallVerdictReason::Apple);
    }
}

#[test]
fn offline_unknown_connections_fail_open_in_all_modes_without_rules_or_questions() {
    for heartbeat_loss in [false, true] {
        for mode in [FirewallMode::Ask, FirewallMode::Allow, FirewallMode::Block] {
            let (mut e, f) = offline_flow(heartbeat_loss);
            e.state.settings.mode = mode;
            assert_eq!(e.flow(f), Some(FirewallAction::Allow));
            assert_eq!(e.state.events[0].reason, FirewallVerdictReason::NoClient);
            assert_eq!(e.state.events[0].action, FirewallAction::Allow);
            assert!(e.state.rules.is_empty() && e.alerts().is_empty());
        }
    }
}

#[test]
fn offline_automatic_rules_are_created_and_persist_without_a_client() {
    for heartbeat_loss in [false, true] {
        for source in [FirewallRuleSource::Apple, FirewallRuleSource::Installed] {
            let (mut e, mut f) = offline_flow(heartbeat_loss);
            e.state.settings.mode = FirewallMode::Block;
            let reason = if source == FirewallRuleSource::Apple {
                f.app.signer = FirewallSigner::Apple;
                f.app.key = identity_key(&f.app);
                FirewallVerdictReason::Apple
            } else {
                f.app.bundle_path = Some("/fixtures/Tool.app".into());
                f.app.path = "/fixtures/Tool.app/Contents/MacOS/tool".into();
                e.set_baseline(vec![f.app.clone()]);
                FirewallVerdictReason::Installed
            };
            assert_eq!(e.flow(f.clone()), Some(FirewallAction::Allow));
            assert_eq!(e.state.rules.len(), 1);
            assert_eq!(e.state.rules[0].source, source);
            assert_eq!(e.state.events[0].reason, reason);
            assert!(e.alerts().is_empty());
            let stored =
                serde_json::from_value(serde_json::to_value(e.persistent()).unwrap()).unwrap();
            let mut restored = Engine::restore(stored).unwrap();
            // A restarted extension has never had a client and still applies the saved rule.
            assert!(!restored.connected(f.monotonic_ms));
            assert_eq!(restored.flow(f), Some(FirewallAction::Allow));
            assert_eq!(restored.state.rules.len(), 1);
            assert_eq!(restored.state.rules[0].match_count, 2);
            assert_eq!(restored.state.events[0].count, 1);
        }
    }
}
#[test]
fn udp_and_tcp_timeouts_allow_without_rule_and_heartbeat_timeout_drains() {
    for (protocol, deadline) in [
        (FirewallProtocol::Udp, UDP_TIMEOUT_MS),
        (FirewallProtocol::Tcp, TCP_TIMEOUT_MS),
    ] {
        let mut e = engine();
        let mut f = flow("one", "a.test");
        f.protocol = protocol;
        e.flow(f);
        e.connect(1_000 + deadline);
        e.tick(
            1_000 + deadline,
            1_000 + deadline as i64,
            &HashSet::from([(42, 10)]),
        );
        assert_eq!(e.alerts().len(), 1);
        assert!(e.alerts()[0].released);
        assert_eq!(e.alerts()[0].deadline, None);
        assert_eq!(e.resolutions[0].action, FirewallAction::Allow);
        assert!(e.state.rules.is_empty());
        assert_eq!(e.state.events[0].reason, FirewallVerdictReason::Timeout);
    }
    let mut e = engine();
    e.flow(flow("one", "a.test"));
    e.tick(6_000, 6_000, &HashSet::from([(42, 10)]));
    assert!(e.alerts().is_empty());
}
#[test]
fn event_ring_merges_limits_age_and_preserves_daily_count_after_clear() {
    let mut e = engine();
    e.state.settings.mode = FirewallMode::Block;
    e.set_day(0);
    for _ in 0..3 {
        e.flow(flow("one", "a.test"));
    }
    assert_eq!(e.state.events.len(), 1);
    assert_eq!(e.state.events[0].count, 3);
    assert_eq!(e.state.blocked_today, 3);
    e.state.events.clear();
    assert_eq!(e.state.blocked_today, 3);
    for n in 0..1005 {
        let mut f = flow("one", &format!("{n}.test"));
        f.at = 10_000 + n;
        e.flow(f);
    }
    assert_eq!(e.state.events.len(), 1000);
    e.tick(20_000, 8 * 24 * 60 * 60 * 1000, &HashSet::new());
    assert!(e.state.events.is_empty());
    e.set_day(86_400_000);
    assert_eq!(e.state.blocked_today, 0);
}
#[test]
fn blocklist_parser_handles_hosts_domains_ip_cidr_comments_and_limits() {
    let parsed=parse_blocklist(b"# heading\n0.0.0.0 ads.test tracker.test # comment\n127.0.0.1 localhost\nADS.TEST\n192.0.2.0/24\n2001:db8::/32\n").unwrap();
    assert_eq!(parsed.len(), 4);
    assert!(parsed.contains(&"ads.test".into()));
    assert_eq!(
        parse_blocklist(b"https://example.test"),
        Err(FirewallErrorCode::BlocklistFormat)
    );
    assert_eq!(
        parse_blocklist(&[0xff]),
        Err(FirewallErrorCode::BlocklistFormat)
    );
    assert_eq!(
        parse_blocklist(&vec![b' '; MAX_LIST_BYTES + 1]),
        Err(FirewallErrorCode::BlocklistTooLarge)
    );
    assert_eq!(
        parse_blocklist("a.test\n".repeat(MAX_LIST_ENTRIES + 1).as_bytes()),
        Err(FirewallErrorCode::BlocklistTooLarge)
    );
}
#[test]
fn failed_blocklist_refresh_keeps_last_good_entries() {
    let mut e = engine();
    let list = e
        .blocklist_load(
            None,
            "https://example.test/list".into(),
            "example".into(),
            b"a.test",
            1000,
        )
        .unwrap();
    assert!(
        e.blocklist_load(
            Some(&list.id),
            list.source.clone(),
            list.name.clone(),
            b"<html>",
            2000
        )
        .is_err()
    );
    e.blocklist_error(&list.id, FirewallErrorCode::BlocklistFormat)
        .unwrap();
    assert_eq!(e.state.blocklists[0].entries, vec!["a.test"]);
    assert_eq!(
        e.decide(&flow("one", "a.test")).unwrap().reason,
        FirewallVerdictReason::Blocklist
    );
}
#[test]
fn argument_payload_is_bounded_and_mode_block_never_prompts() {
    let mut e = engine();
    let mut f = flow("one", "a.test");
    f.args = vec!["字".repeat(250); 25];
    e.flow(f);
    assert_eq!(e.alerts()[0].args.len(), 20);
    assert_eq!(e.alerts()[0].args[0].chars().count(), 200);
    e.set_settings(
        FirewallSettings {
            mode: FirewallMode::Block,
            ..FirewallSettings::default()
        },
        2000,
    );
    assert!(e.alerts().is_empty());
    assert_eq!(e.resolutions[0].action, FirewallAction::Block);
}

#[test]
fn identical_rule_is_overwritten_preserving_id() {
    let mut e = engine();
    let first = rule(
        &mut e,
        Some(app("tool")),
        Some("EXAMPLE.test."),
        None,
        FirewallAction::Block,
    );
    let second = rule(
        &mut e,
        Some(app("tool")),
        Some("example.test"),
        None,
        FirewallAction::Allow,
    );
    assert_eq!(first.id, second.id);
    assert_eq!(e.state.rules.len(), 1);
    assert_eq!(
        e.decide(&flow("f", "example.test")).unwrap().action,
        FirewallAction::Allow
    );
}
#[test]
fn expired_reply_creates_rule_without_reviving_or_relogging_a_flow() {
    let mut e = engine();
    let mut f = flow("one", "a.test");
    f.protocol = FirewallProtocol::Udp;
    e.flow(f);
    e.connect(5_999);
    let result = e.request(serde_json::json!({"op":"alert_reply","now":6000,"at":6000,"reply":{"alertId":"one","action":"block","scope":"app","until":"always"}}));
    assert!(result.is_ok());
    assert_eq!(e.state.rules.len(), 1);
    assert_eq!(e.state.rules[0].action, FirewallAction::Block);
    assert_eq!(e.resolutions.len(), 1);
    assert_eq!(e.resolutions[0].action, FirewallAction::Allow);
    assert_eq!(e.state.events[0].reason, FirewallVerdictReason::Timeout);
    assert_eq!(e.state.events[0].count, 1);
    assert!(e.alerts().is_empty());
}

#[test]
fn released_question_merges_new_flows_with_individual_deadlines() {
    let mut e = engine();
    let mut first = flow("one", "a.test");
    first.protocol = FirewallProtocol::Udp;
    first.at = 50_000; // UI deadline is epoch time, expiry uses monotonic time.
    e.flow(first);
    assert_eq!(e.alerts()[0].deadline, Some(55_000));
    e.connect(6_000);
    e.expire(6_000, 55_000);
    assert!(e.alerts()[0].released);
    e.resolutions.clear();
    let mut second = flow("two", "b.test");
    second.at = 55_001;
    second.monotonic_ms = 6_001;
    e.flow(second);
    let a = &e.alerts()[0];
    assert_eq!(a.id, "one");
    assert_eq!(a.waiting, 1);
    assert!(!a.released);
    assert_eq!(a.deadline, Some(175_001));
    // Reply to the released destination only; the other socket still needs a question.
    e.reply(
        FirewallReply {
            alert_id: "one".into(),
            action: FirewallAction::Block,
            scope: FirewallReplyScope::Destination,
            until: FirewallRuleUntil::Always,
        },
        55_002,
    )
    .unwrap();
    assert!(e.resolutions.is_empty());
    assert_eq!(e.alerts()[0].id, "two");
    assert_eq!(e.alerts()[0].waiting, 0);
    e.connect(126_001);
    e.expire(126_001, 175_001);
    assert!(e.alerts()[0].released);
    assert_eq!(e.resolutions.len(), 1);
    assert_eq!(e.state.events[0].reason, FirewallVerdictReason::Timeout);
}

#[test]
fn mixed_protocol_deadline_is_earliest_paused_connection() {
    let mut e = engine();
    e.flow(flow("tcp", "a.test"));
    let mut udp = flow("udp", "a.test");
    udp.protocol = FirewallProtocol::Udp;
    e.flow(udp);
    assert_eq!(e.alerts()[0].deadline, Some(6_000));
    e.connect(6_000);
    e.expire(6_000, 6_000);
    assert!(!e.alerts()[0].released);
    assert_eq!(e.alerts()[0].deadline, Some(121_000));
    assert_eq!(e.alerts()[0].id, "tcp");
    assert_eq!(e.resolutions[0].id, "udp");
}

#[test]
fn queue_evicts_oldest_released_question_but_never_discards_paused_flows() {
    let mut e = engine();
    for n in 0..MAX_ALERTS {
        let mut f = flow(&n.to_string(), "a.test");
        f.app = app(&n.to_string());
        f.at += n as i64;
        e.flow(f);
    }
    let mut extra = flow("extra", "a.test");
    extra.app = app("extra");
    assert_eq!(e.flow(extra.clone()), Some(FirewallAction::Allow));
    assert_eq!(e.alerts().len(), MAX_ALERTS);
    assert!(e.resolutions.is_empty());
    assert_eq!(e.state.events[0].reason, FirewallVerdictReason::NoClient);
    // Release out of order: eviction uses original question time.
    e.timeout("2", 2_000);
    e.timeout("1", 2_000);
    assert_eq!(e.flow(extra), None);
    assert_eq!(e.alerts().len(), MAX_ALERTS);
    assert!(!e.alerts().iter().any(|a| a.id == "1"));
    assert!(e.alerts().iter().any(|a| a.id == "0"));
    assert!(e.alerts().iter().any(|a| a.id == "2" && a.released));
    assert_eq!(
        e.reply(
            FirewallReply {
                alert_id: "1".into(),
                action: FirewallAction::Allow,
                scope: FirewallReplyScope::App,
                until: FirewallRuleUntil::Always
            },
            2_001
        ),
        Err(FirewallErrorCode::UnknownAlert)
    );
}

#[test]
fn disconnect_and_heartbeat_loss_clear_released_and_paused_questions() {
    for heartbeat_loss in [false, true] {
        let mut e = engine();
        e.flow(flow("released", "a.test"));
        e.timeout("released", 2_000);
        let mut other = flow("paused", "b.test");
        other.app = app("other");
        e.flow(other);
        e.resolutions.clear();
        if heartbeat_loss {
            e.expire(6_000, 6_000);
        } else {
            e.disconnect(6_000);
        }
        assert!(e.alerts().is_empty());
        assert!(e.state.rules.is_empty());
        assert_eq!(e.resolutions.len(), 1);
        assert_eq!(e.resolutions[0].id, "paused");
        assert_eq!(e.state.events[0].reason, FirewallVerdictReason::NoClient);
        e.connect(6_001);
        assert!(e.alerts().is_empty());
    }
}

#[test]
fn invalid_identity_asks_even_with_baseline_or_old_automatic_rule() {
    for automatic in [FirewallRuleSource::Apple, FirewallRuleSource::Installed] {
        let mut e = engine();
        let mut f = flow("invalid", "a.test");
        f.app.signer = FirewallSigner::Invalid;
        f.app.bundle_path = Some("/fixtures/Tool.app".into());
        f.app.key = identity_key(&f.app);
        assert_eq!(f.app.key, "path:/fixtures/tool");
        assert_eq!(
            owner_app(&f.app, &[app("outer")]).signer,
            FirewallSigner::Invalid
        );
        e.set_baseline(vec![f.app.clone()]);
        assert!(e.state.baseline.is_empty());
        // Even persisted baseline and automatic rules from an older build cannot grant trust.
        e.state.baseline.insert(f.app.key.clone());
        rule(
            &mut e,
            Some(f.app.clone()),
            None,
            None,
            FirewallAction::Allow,
        );
        e.state.rules[0].source = automatic;
        assert_eq!(e.flow(f.clone()), None);
        assert_eq!(e.alerts()[0].app.signer, FirewallSigner::Invalid);
        assert!(e.state.events.is_empty());
        // Explicit user decisions still apply.
        rule(
            &mut e,
            Some(f.app.clone()),
            None,
            None,
            FirewallAction::Block,
        );
        assert_eq!(e.flow(f), Some(FirewallAction::Block));
    }
}

#[test]
fn late_details_update_released_question_and_are_bounded() {
    let mut e = engine();
    e.flow(flow("one", "a.test"));
    e.timeout("one", 2_000);
    e.flow_details("one", vec!["字".repeat(250); 25], vec![]);
    assert_eq!(e.alerts()[0].args.len(), 20);
    assert_eq!(e.alerts()[0].args[0].chars().count(), 200);
    assert!(e.alerts()[0].released);
    e.disconnect(3_000);
    e.flow_details("one", vec!["ignored".into()], vec![]);
    assert!(e.alerts().is_empty());
}

#[test]
fn watchdog_timeout_is_idempotent_and_internal_failure_records_no_client() {
    let mut e = engine();
    let mut f = flow("one", "a.test");
    f.protocol = FirewallProtocol::Udp;
    e.flow(f);
    e.connect(6_000);
    e.request(serde_json::json!({"op":"timeout","id":"one","now":6000,"at":6000}))
        .unwrap();
    e.request(serde_json::json!({"op":"cancel","id":"one","now":6000,"at":6000}))
        .unwrap();
    assert_eq!(e.resolutions.len(), 1);
    assert_eq!(e.state.events[0].reason, FirewallVerdictReason::Timeout);
    assert_eq!(e.state.events[0].count, 1);
    assert!(e.alerts()[0].released);
    let mut e = engine();
    e.flow(flow("two", "a.test"));
    e.cancel("two", 1_500);
    e.bypass(flow("three", "a.test"), true, false);
    assert_eq!(e.state.events[0].reason, FirewallVerdictReason::NoClient);
    assert_eq!(e.state.events[0].count, 2);
    e.state.events.clear();
    e.flow(flow("four", "a.test"));
    e.suspend(2_000);
    e.connect(2_001);
    assert_eq!(e.flow(flow("five", "a.test")), Some(FirewallAction::Allow));
    e.bypass(flow("six", "a.test"), true, false);
    assert!(e.state.events.is_empty());
    assert!(e.alerts().is_empty());
}
#[test]
fn suspension_survives_heartbeat_and_unmatched_alert_keeps_arrival_time() {
    let mut e = engine();
    e.flow(flow("one", "a.test"));
    e.flow(flow("two", "b.test"));
    e.reply(
        FirewallReply {
            alert_id: "one".into(),
            action: FirewallAction::Allow,
            scope: FirewallReplyScope::Destination,
            until: FirewallRuleUntil::Always,
        },
        2000,
    )
    .unwrap();
    assert_eq!(e.alerts()[0].at, 1000);
    e.suspend(2000);
    e.connect(2001);
    assert_eq!(e.flow(flow("three", "b.test")), Some(FirewallAction::Allow));
    assert!(e.alerts().is_empty());
    e.resume();
    assert!(e.flow(flow("four", "b.test")).is_none());
}

#[test]
fn configure_is_single_flight_idempotent_and_never_suspends_policy() {
    let mut e = engine();
    let settings = FirewallSettings::default();
    assert!(e.configure("enable-1".into(), settings.clone(), false, 1_000));
    assert!(e.snapshot().preparing);
    for _ in 0..100 {
        assert!(!e.configure("enable-1".into(), settings.clone(), false, 1_001));
    }
    assert!(!e.configure("enable-2".into(), settings, false, 1_002));
    assert_eq!(e.flow(flow("unknown", "example.test")), None);
    assert_eq!(e.alerts().len(), 1);
    let blocked = flow("blocked", "blocked.test");
    rule(
        &mut e,
        Some(blocked.app.clone()),
        Some("blocked.test"),
        None,
        FirewallAction::Block,
    );
    assert_eq!(e.flow(blocked), Some(FirewallAction::Block));
    assert!(e.connected(1_002));
}

#[test]
fn baseline_progress_resolves_installed_apps_and_completion_survives_restart() {
    let mut e = engine();
    assert!(e.configure("enable".into(), FirewallSettings::default(), false, 1_000));
    let mut installed = flow("installed", "example.test");
    installed.app.bundle_path = Some("/fixtures/tool.app".into());
    assert_eq!(e.flow(installed.clone()), None);
    e.baseline_add(vec![installed.app.clone()], None, 1_001);
    assert_eq!(e.resolutions.len(), 1);
    assert_eq!(e.resolutions[0].action, FirewallAction::Allow);
    assert!(e.alerts().is_empty());
    let mut invalid = app("invalid");
    invalid.signer = FirewallSigner::Invalid;
    invalid.bundle_path = Some("/fixtures/invalid.app".into());
    e.baseline_add(vec![invalid.clone()], None, 1_002);
    assert!(!e.state.baseline.contains(&identity_key(&invalid)));
    e.baseline_finish(None);
    assert!(!e.snapshot().preparing);
    let mut restored = Engine::restore(e.persistent()).unwrap();
    assert!(!restored.configure(
        "reconnect".into(),
        FirewallSettings::default(),
        false,
        1_003
    ));
    assert!(
        restored
            .state
            .baseline
            .contains(&identity_key(&installed.app))
    );
}

#[test]
fn baseline_deadline_keeps_partial_results_and_late_work_cannot_resume_disabled_filter() {
    let mut e = engine();
    assert!(e.configure("enable".into(), FirewallSettings::default(), false, 1_000));
    let mut installed = app("completed");
    installed.bundle_path = Some("/fixtures/completed.app".into());
    e.baseline_add(vec![installed.clone()], None, 1_001);
    e.connect(60_999);
    assert!(!e.configure("retry".into(), FirewallSettings::default(), false, 60_999));
    e.expire(61_000, 61_000);
    assert!(!e.snapshot().preparing); // retry did not extend the original deadline
    assert!(e.state.baseline.contains(&identity_key(&installed)));
    let mut late = app("late");
    late.bundle_path = Some("/fixtures/late.app".into());
    e.baseline_add(vec![late.clone()], None, 61_001);
    assert!(!e.state.baseline.contains(&identity_key(&late)));
    assert!(!e.configure("later".into(), FirewallSettings::default(), false, 61_002));
    e.suspend(61_003);
    e.baseline_finish(None);
    e.connect(61_004);
    assert!(!e.connected(61_004));
}

#[test]
fn activity_composite_cursor_preserves_same_millisecond_rows_after_restart() {
    let mut e = engine();
    e.state.settings.mode = FirewallMode::Block;
    for n in 0..201 {
        e.flow(flow(&n.to_string(), &format!("{n}.test")));
    }
    let mut e = Engine::restore(e.persistent()).unwrap();
    let mut before = serde_json::Value::Null;
    let mut before_id = serde_json::Value::Null;
    let mut ids = HashSet::new();
    for expected in [100, 100, 1] {
        let page = e.request(serde_json::json!({
            "op": "events", "query": {"before": before, "beforeId": before_id, "limit": 100, "blockedOnly": true}
        })).unwrap();
        let rows = page.as_array().unwrap();
        assert_eq!(rows.len(), expected);
        for row in rows {
            assert!(ids.insert(row["id"].as_str().unwrap().to_owned()));
        }
        let last = rows.last().unwrap();
        before = last["at"].clone();
        before_id = last["id"].clone();
    }
    assert_eq!(ids.len(), 201);
}

fn answer(
    e: &mut Engine,
    alert: &str,
    action: FirewallAction,
    scope: FirewallReplyScope,
    until: FirewallRuleUntil,
) -> Result<(), FirewallErrorCode> {
    e.reply(
        FirewallReply {
            alert_id: alert.into(),
            action,
            scope,
            until,
        },
        2_000,
    )
}
fn addressed(id: &str, address: &str) -> Flow {
    let mut f = flow(id, "unused.test");
    f.remote_host = None;
    f.remote_address = address.into();
    f
}

#[test]
fn flow_addresses_are_canonical_for_rules_lists_and_alerts() {
    for (input, canonical) in [
        ("2001:0DB8::1", "2001:db8::1"),
        (" fe80::1%en0 ", "fe80::1"),
        ("::ffff:203.0.113.5", "203.0.113.5"),
        ("::ffff:203.0.113.0/120", "203.0.113.0/24"),
        ("[2001:db8::1]", "2001:db8::1"),
    ] {
        assert_eq!(normalize_remote(input).unwrap(), canonical);
    }
    assert_eq!(
        normalize_remote("192.0.2.1%en0"),
        Err(FirewallErrorCode::RuleInvalid)
    );
    assert!(remote_matches("203.0.113.0/24", "::ffff:203.0.113.5", None));
    assert!(remote_matches("2001:db8::1", "2001:0db8:0::1%en0", None));
    assert!(BlockIndex::new(&["203.0.113.0/24".into()]).matches("::ffff:203.0.113.5", None));
    for listed in [false, true] {
        let mut e = engine();
        if listed {
            e.blocklist_load(
                None,
                "/fixtures/list".into(),
                "list".into(),
                b"203.0.113.0/24",
                1_000,
            )
            .unwrap();
        } else {
            rule(
                &mut e,
                None,
                Some("203.0.113.0/24"),
                None,
                FirewallAction::Block,
            );
        }
        let mapped = addressed("mapped", "::ffff:203.0.113.5");
        assert_eq!(e.flow(mapped), Some(FirewallAction::Block));
        assert_eq!(e.state.events[0].remote_address, "203.0.113.5");
    }
    let mut e = engine();
    assert_eq!(e.flow(addressed("zoned", "fe80::1%en0")), None);
    assert_eq!(e.alerts()[0].remote_address, "fe80::1");
}

#[test]
fn destination_answers_need_a_matchable_destination_or_keep_the_question() {
    use FirewallReplyScope::{App, Destination};
    let mut e = engine();
    let mut zoned = addressed("zoned", "fe80::1%en0");
    zoned.port = 0;
    e.flow(zoned.clone());
    answer(
        &mut e,
        "zoned",
        FirewallAction::Block,
        Destination,
        FirewallRuleUntil::Always,
    )
    .unwrap();
    assert_eq!(e.state.rules[0].remote.as_deref(), Some("fe80::1"));
    assert_eq!(e.state.rules[0].port, None);
    assert_eq!(e.resolutions[0].action, FirewallAction::Block);
    // The rule survives a restart and matches the next connection there.
    let mut restored = Engine::restore(e.persistent()).unwrap();
    zoned.id = "again".into();
    assert_eq!(restored.flow(zoned), Some(FirewallAction::Block));
    // Nothing usable to name: no rule is stored and the question stays.
    let mut e = engine();
    e.flow(addressed("empty", " "));
    assert_eq!(
        answer(
            &mut e,
            "empty",
            FirewallAction::Block,
            Destination,
            FirewallRuleUntil::Always
        ),
        Err(FirewallErrorCode::RuleInvalid)
    );
    assert!(e.state.rules.is_empty());
    assert_eq!(e.alerts().len(), 1);
    answer(
        &mut e,
        "empty",
        FirewallAction::Block,
        App,
        FirewallRuleUntil::Always,
    )
    .unwrap();
    assert_eq!(e.state.rules.len(), 1);
    assert!(Engine::restore(e.persistent()).is_ok());
}

#[test]
fn restore_drops_rules_and_entries_that_can_never_match_and_keeps_the_rest() {
    let mut e = engine();
    let kept = rule(
        &mut e,
        Some(app("tool")),
        Some("example.test"),
        Some(443),
        FirewallAction::Block,
    );
    let template = e.state.rules[0].clone();
    for (id, remote, port) in [
        ("zoned", Some("fe80::1%en0"), None),
        ("mapped", Some("::ffff:203.0.113.5"), Some(80)),
        ("empty", Some(""), None),
        ("malformed", Some("a..test"), None),
        ("port", Some("example.test"), Some(0)),
    ] {
        e.state.rules.push(FirewallRule {
            id: id.into(),
            remote: remote.map(str::to_string),
            port,
            ..template.clone()
        });
    }
    // A global rule naming nothing, and an earlier build's installed allowance for an
    // Apple program. A third-party installed allowance stays.
    e.state.rules.push(FirewallRule {
        id: "global".into(),
        app: None,
        remote: None,
        port: None,
        ..template.clone()
    });
    let mut apple = app("Mail");
    apple.signer = FirewallSigner::Apple;
    apple.key = identity_key(&apple);
    for (id, owner) in [("apple", apple), ("installed", app("installed"))] {
        e.state.rules.push(FirewallRule {
            id: id.into(),
            app: Some(owner),
            remote: None,
            port: None,
            action: FirewallAction::Allow,
            source: FirewallRuleSource::Installed,
            ..template.clone()
        });
    }
    e.blocklist_load(
        None,
        "/fixtures/list".into(),
        "list".into(),
        b"a.test\nb.test",
        1_000,
    )
    .unwrap();
    e.state.blocklists[0].entries.extend([
        "under_score.test".into(),
        "2001:0DB8::/32".into(),
        "A.TEST".into(),
    ]);
    let restored = Engine::restore(e.persistent()).unwrap();
    let rules: Vec<_> = restored
        .state
        .rules
        .iter()
        .map(|r| (r.id.as_str(), r.remote.as_deref(), r.port))
        .collect();
    assert_eq!(
        rules,
        vec![
            (kept.id.as_str(), Some("example.test"), Some(443)),
            ("zoned", Some("fe80::1"), None),
            ("mapped", Some("203.0.113.5"), Some(80)),
            ("installed", None, None),
        ]
    );
    let list = &restored.state.blocklists[0];
    assert_eq!(list.entries, vec!["2001:db8::/32", "a.test", "b.test"]);
    assert_eq!(list.info.entries, 3);
    // Only an unknown version or the size limits still reject the whole file.
    let mut future = e.persistent();
    future.version = 2;
    assert!(Engine::restore(future).is_err());
}

#[test]
fn until_quit_answers_cover_every_answering_process_of_the_app() {
    use FirewallReplyScope::App;
    let mut e = engine();
    e.flow(flow("main", "a.test"));
    answer(
        &mut e,
        "main",
        FirewallAction::Allow,
        App,
        FirewallRuleUntil::AppQuits,
    )
    .unwrap();
    // A helper shares the app key but is another process: it gets its own question...
    let mut helper = flow("helper", "a.test");
    helper.pid = 43;
    helper.process_start = 20;
    assert_eq!(e.flow(helper), None);
    answer(
        &mut e,
        "helper",
        FirewallAction::Allow,
        App,
        FirewallRuleUntil::AppQuits,
    )
    .unwrap();
    // ...and then both share one temporary rule instead of taking turns asking.
    assert_eq!(e.state.rules.len(), 1);
    for (id, pid, start) in [("main-2", 42, 10), ("helper-2", 43, 20)] {
        let mut f = flow(id, "b.test");
        f.pid = pid;
        f.process_start = start;
        assert_eq!(e.flow(f), Some(FirewallAction::Allow));
    }
    assert!(e.alerts().is_empty());
    let mut processes = e.processes();
    processes.sort();
    assert_eq!(processes, vec![(42, 10), (43, 20)]);
    // The rule ends with the last of its processes.
    e.tick(2_000, 2_000, &HashSet::from([(43, 20)]));
    assert_eq!(e.state.rules.len(), 1);
    e.tick(2_000, 2_000, &HashSet::new());
    assert!(e.state.rules.is_empty());
}

#[test]
fn until_quit_answers_keep_permanent_rules_and_permanent_answers_replace_both() {
    use FirewallReplyScope::App;
    let mut e = engine();
    e.flow(flow("old", "a.test"));
    e.timeout("old", 1_500); // Released; the question stays answerable.
    let permanent = rule(&mut e, Some(app("tool")), None, None, FirewallAction::Block);
    answer(
        &mut e,
        "old",
        FirewallAction::Allow,
        App,
        FirewallRuleUntil::AppQuits,
    )
    .unwrap();
    assert_eq!(e.state.rules.len(), 2);
    assert_eq!(e.state.rules[0], permanent);
    assert_eq!(e.state.rules[1].until, FirewallRuleUntil::AppQuits);
    e.tick(2_000, 2_000, &HashSet::new());
    assert_eq!(e.state.rules, vec![permanent]);

    let mut e = engine();
    e.flow(flow("first", "a.test"));
    answer(
        &mut e,
        "first",
        FirewallAction::Allow,
        App,
        FirewallRuleUntil::AppQuits,
    )
    .unwrap();
    let mut other = flow("second", "a.test");
    other.pid = 43;
    other.process_start = 20;
    e.flow(other);
    answer(
        &mut e,
        "second",
        FirewallAction::Block,
        App,
        FirewallRuleUntil::Always,
    )
    .unwrap();
    assert_eq!(e.state.rules.len(), 1);
    assert_eq!(e.state.rules[0].until, FirewallRuleUntil::Always);
    assert!(e.processes().is_empty());
    assert_eq!(e.flow(flow("third", "a.test")), Some(FirewallAction::Block));
}

#[test]
fn match_statistics_bump_only_the_stats_revision() {
    let mut e = engine();
    let blocked = rule(&mut e, None, Some("a.test"), None, FirewallAction::Block);
    let before = e.snapshot();
    e.flow(flow("one", "a.test"));
    let after = e.snapshot();
    assert_eq!(after.rules_revision, before.rules_revision);
    assert!(after.rules_stats_revision > before.rules_stats_revision);
    assert_eq!(e.state.rules[0].match_count, 1);
    // Settings that prune nothing leave the list alone; creating or pruning changes it.
    e.set_settings(
        FirewallSettings {
            mode: FirewallMode::Block,
            ..FirewallSettings::default()
        },
        1_001,
    );
    assert_eq!(e.snapshot().rules_revision, before.rules_revision);
    let mut apple = flow("apple", "b.test");
    apple.app.signer = FirewallSigner::Apple;
    apple.app.key = identity_key(&apple.app);
    assert_eq!(e.flow(apple), Some(FirewallAction::Allow));
    let created = e.snapshot().rules_revision;
    assert!(created > before.rules_revision);
    e.set_settings(
        FirewallSettings {
            allow_apple: false,
            ..e.state.settings.clone()
        },
        1_002,
    );
    let pruned = e.snapshot().rules_revision;
    assert!(pruned > created);
    e.delete_rules(&[blocked.id], 1_003);
    assert!(e.snapshot().rules_revision > pruned);
    // Snapshots from an older extension lack the field.
    let mut old = serde_json::to_value(e.snapshot()).unwrap();
    old.as_object_mut().unwrap().remove("rulesStatsRevision");
    let old: Snapshot = serde_json::from_value(old).unwrap();
    assert_eq!(old.rules_stats_revision, 0);
}

#[test]
fn alert_waiting_counts_other_held_connections_and_falls_as_they_release() {
    let mut e = engine();
    for (id, host) in [("one", "a.test"), ("two", "b.test"), ("three", "c.test")] {
        e.flow(flow(id, host));
    }
    assert_eq!(e.alerts()[0].waiting, 2);
    e.timeout("two", 1_500);
    assert_eq!(e.alerts()[0].waiting, 1);
    // The question's own connection never counts.
    e.timeout("one", 1_500);
    assert_eq!(e.alerts()[0].waiting, 1);
    e.timeout("three", 1_500);
    assert_eq!(e.alerts()[0].waiting, 0);
    assert!(e.alerts()[0].released);
}

#[test]
fn apple_programs_follow_allow_apple_even_when_in_the_installed_baseline() {
    let mut mail = app("Mail");
    mail.signer = FirewallSigner::Apple;
    mail.signing_id = Some("com.apple.mail".into());
    mail.team_id = None;
    mail.bundle_path = Some("/System/Applications/Mail.app".into());
    mail.key = identity_key(&mail);
    let mut f = flow("mail", "example.test");
    f.app = mail.clone();
    let mut e = engine();
    e.set_settings(
        FirewallSettings {
            allow_apple: false,
            ..FirewallSettings::default()
        },
        1_000,
    );
    e.set_baseline(vec![mail.clone()]);
    assert!(e.state.baseline.contains(&mail.key));
    assert_eq!(e.flow(f.clone()), None);
    assert!(e.state.rules.is_empty());
    // Allowed as an Apple program when that setting is on, never as an installed app.
    let mut e = engine();
    e.set_baseline(vec![mail]);
    assert_eq!(e.flow(f), Some(FirewallAction::Allow));
    assert_eq!(e.state.rules[0].source, FirewallRuleSource::Apple);
}

#[test]
fn graylist_covers_apple_network_tools_but_not_other_signers_or_dns_tools() {
    for path in [
        "/usr/bin/nscurl",
        "/usr/bin/openssl",
        "/usr/bin/rsync",
        "/usr/bin/ftp",
        "/usr/bin/tftp",
        "/usr/bin/ldapsearch",
        "/usr/libexec/git-core/git-remote-https",
        "/Library/Developer/CommandLineTools/usr/libexec/git-core/git-remote-http",
        "/Applications/Xcode.app/Contents/Developer/usr/libexec/git-core/git-remote-https",
    ] {
        let mut tool = app("tool");
        tool.signer = FirewallSigner::Apple;
        tool.signing_id = None;
        tool.path = path.into();
        assert!(graylisted(&tool), "{path}");
        tool.signer = FirewallSigner::DeveloperId;
        assert!(!graylisted(&tool), "{path}");
    }
    for id in [
        "com.apple.nscurl",
        "com.apple.openssl",
        "com.apple.rsync",
        "com.apple.tftp",
        "com.apple.ldapsearch",
        "com.apple.git-remote-http",
    ] {
        let mut tool = app("renamed");
        tool.signer = FirewallSigner::Apple;
        tool.signing_id = Some(id.into());
        assert!(graylisted(&tool), "{id}");
    }
    let mut dig = app("dig");
    dig.signer = FirewallSigner::Apple;
    dig.signing_id = Some("com.apple.dig".into());
    dig.path = "/usr/bin/dig".into();
    assert!(!graylisted(&dig));
}

// The header of github.com/StevenBlack/hosts, verbatim apart from the dates.
const HOSTS_HEADER: &str = "# Title: StevenBlack/hosts
#
# This hosts file is a merged collection of hosts from reputable sources,
# with a dash of crowd sourcing via GitHub
#
# Date: 01 January 2026 00:00:00 (UTC)
# Number of unique domains: 2
#
# ===============================================================

127.0.0.1 localhost
127.0.0.1 localhost.localdomain
127.0.0.1 local
255.255.255.255 broadcasthost
::1 localhost
::1 ip6-localhost
::1 ip6-loopback
fe80::1%lo0 localhost
ff00::0 ip6-localnet
ff00::0 ip6-mcastprefix
ff02::1 ip6-allnodes
ff02::2 ip6-allrouters
ff02::3 ip6-allhosts
0.0.0.0 0.0.0.0

# Custom host records are listed here.

# End of custom host records.
# Start StevenBlack

#=====================================
# Title: Hosts contributed by Steven Black
# http://stevenblack.com

";

#[test]
fn blocklist_parser_reads_real_hosts_files_and_skips_unsupported_lines() {
    let text = format!(
        "{HOSTS_HEADER}0.0.0.0 ads.example.com\n0.0.0.0 tracker.example.net # inline\n\
         0.0.0.0 ad_server.example.org\n0.0.0.0 例え.example\n||adblock.example.org^\n\
         ! Adblock comment\n[Adblock Plus 2.0]\n"
    );
    assert_eq!(
        parse_blocklist(text.as_bytes()).unwrap(),
        vec!["ads.example.com", "tracker.example.net"]
    );
    // An HTML page, or a file without one usable entry, is not a list.
    for text in [
        "<!DOCTYPE html>\n<html><body>\nNotFound\n</body></html>\n",
        "# only comments\n\n",
        HOSTS_HEADER,
        "||ads.example.org^\n",
    ] {
        assert_eq!(
            parse_blocklist(text.as_bytes()),
            Err(FirewallErrorCode::BlocklistFormat),
            "{text}"
        );
    }
}

#[test]
fn blocklist_count_limit_has_its_own_code() {
    let mut e = engine();
    for n in 0..32 {
        e.blocklist_load(
            None,
            format!("/fixtures/{n}"),
            n.to_string(),
            b"a.test",
            1_000,
        )
        .unwrap();
    }
    assert_eq!(
        e.blocklist_load(
            None,
            "/fixtures/extra".into(),
            "extra".into(),
            b"a.test",
            1_000
        ),
        Err(FirewallErrorCode::BlocklistLimit)
    );
    // Refreshing a stored list is not another list.
    let first = e.state.blocklists[0].info.id.clone();
    assert!(
        e.blocklist_load(Some(&first), String::new(), String::new(), b"b.test", 1_001)
            .is_ok()
    );
}

#[test]
fn bypass_enforces_whatever_needs_no_question() {
    use FirewallAction::{Allow, Block};
    let mut e = engine();
    rule(&mut e, None, Some("blocked.test"), None, Block);
    rule(&mut e, Some(app("tool")), None, None, Block);
    e.blocklist_load(
        None,
        "/fixtures/list".into(),
        "list".into(),
        b"listed.test",
        1_000,
    )
    .unwrap();
    // Without identity: global destination rules and lists, not the app's rules.
    for (host, action, reason) in [
        ("blocked.test", Block, FirewallVerdictReason::Rule),
        ("listed.test", Block, FirewallVerdictReason::Blocklist),
        ("other.test", Allow, FirewallVerdictReason::NoClient),
    ] {
        assert_eq!(
            e.bypass(flow("unknown", host), false, false),
            action,
            "{host}"
        );
        assert_eq!(e.state.events[0].reason, reason, "{host}");
    }
    // Late but identified: the app's own rules apply as well.
    assert_eq!(e.bypass(flow("late", "other.test"), true, false), Block);
    // Already let through by the watchdog: recorded as what happened.
    assert_eq!(e.bypass(flow("gone", "blocked.test"), true, true), Allow);
    assert_eq!(e.state.events[0].reason, FirewallVerdictReason::NoClient);
    // Nothing decides it: allowed without a question, even in ask mode.
    let mut stranger = flow("stranger", "other.test");
    stranger.app = app("stranger");
    assert_eq!(e.bypass(stranger, true, false), Allow);
    assert!(e.alerts().is_empty());
    // Automatic allowances still apply when late.
    let mut apple = flow("apple", "other.test");
    apple.app.signer = FirewallSigner::Apple;
    apple.app.key = identity_key(&apple.app);
    assert_eq!(e.bypass(apple, true, false), Allow);
    assert_eq!(e.state.events[0].reason, FirewallVerdictReason::Apple);
    // The extension reads the verdict from the reply; addresses are normalized here too.
    let mut native = addressed("native", "::ffff:192.0.2.9");
    native.remote_host = Some("Blocked.Test.".into());
    let reply = e
        .request(serde_json::json!({"op":"bypass","identified":false,"flow":native}))
        .unwrap();
    assert_eq!(reply, serde_json::json!({"verdict":"block"}));
    assert_eq!(e.state.events[0].remote_address, "192.0.2.9");
}

#[test]
fn fresh_setup_and_reset_forget_everything_and_scan_again() {
    for via_reset in [false, true] {
        let mut e = engine();
        assert!(e.configure("first".into(), FirewallSettings::default(), false, 1_000));
        let mut installed = app("installed");
        installed.bundle_path = Some("/fixtures/Installed.app".into());
        e.baseline_add(vec![installed], Some("first"), 1_000);
        e.baseline_finish(Some("first"));
        rule(
            &mut e,
            None,
            Some("blocked.test"),
            None,
            FirewallAction::Block,
        );
        e.blocklist_load(
            None,
            "/fixtures/list".into(),
            "list".into(),
            b"listed.test",
            1_000,
        )
        .unwrap();
        assert_eq!(
            e.flow(flow("blocked", "blocked.test")),
            Some(FirewallAction::Block)
        );
        assert_eq!(e.flow(flow("asked", "a.test")), None);
        e.reply(
            FirewallReply {
                alert_id: "asked".into(),
                action: FirewallAction::Allow,
                scope: FirewallReplyScope::Destination,
                until: FirewallRuleUntil::AppQuits,
            },
            1_000,
        )
        .unwrap();
        assert_eq!(e.flow(flow("waiting", "b.test")), None);
        e.resolutions.clear();
        let reply = if via_reset {
            e.request(serde_json::json!({"op":"reset"})).unwrap()
        } else {
            e.request(serde_json::json!({
                "op":"configure","id":"second","fresh":true,"settings":FirewallSettings::default()
            }))
            .unwrap()
        };
        assert!(e.state.rules.is_empty() && e.state.blocklists.is_empty());
        assert!(e.state.events.is_empty() && e.state.blocked_today == 0);
        assert!(e.state.baseline.is_empty() && !e.state.baseline_initialized);
        assert!(e.alerts().is_empty() && e.processes().is_empty());
        assert_eq!(e.resolutions.len(), 1);
        assert_eq!(e.resolutions[0].action, FirewallAction::Allow);
        // A removal leaves nothing to scan for; a new setup scans again.
        assert_eq!(e.snapshot().preparing, !via_reset);
        if !via_reset {
            assert_eq!(reply, serde_json::json!({"accepted":true,"scan":true}));
        }
        // Old list indexes are gone too, and nothing returns after a restart.
        assert_eq!(e.flow(flow("listed", "listed.test")), None);
        let restored = Engine::restore(e.persistent()).unwrap();
        assert!(restored.state.rules.is_empty() && restored.state.blocklists.is_empty());
        assert!(restored.state.events.is_empty() && restored.state.baseline.is_empty());
    }
    // A retried fresh configuration (same ID) must not wipe what came after it,
    // and an ordinary one never wipes.
    let mut e = engine();
    assert!(e.configure("fresh".into(), FirewallSettings::default(), true, 1_000));
    rule(&mut e, None, Some("kept.test"), None, FirewallAction::Block);
    assert!(!e.configure("fresh".into(), FirewallSettings::default(), true, 1_001));
    assert!(!e.configure("other".into(), FirewallSettings::default(), false, 1_002));
    assert_eq!(e.state.rules.len(), 1);
}

#[test]
fn results_from_a_cancelled_baseline_scan_are_ignored() {
    let mut e = engine();
    assert!(e.configure("old".into(), FirewallSettings::default(), false, 1_000));
    e.reset();
    assert!(e.configure("new".into(), FirewallSettings::default(), true, 1_001));
    let mut stale = app("stale");
    stale.bundle_path = Some("/fixtures/Stale.app".into());
    let apps = serde_json::to_value(vec![stale.clone()]).unwrap();
    e.request(serde_json::json!({"op":"baseline_add","scan":"old","apps":apps}))
        .unwrap();
    e.request(serde_json::json!({"op":"baseline_finish","scan":"old"}))
        .unwrap();
    assert!(e.snapshot().preparing);
    assert!(!e.state.baseline.contains(&stale.key));
    e.request(serde_json::json!({"op":"baseline_add","scan":"new","apps":apps}))
        .unwrap();
    e.request(serde_json::json!({"op":"baseline_finish","scan":"new"}))
        .unwrap();
    assert!(!e.snapshot().preparing && e.state.baseline_initialized);
    assert!(e.state.baseline.contains(&stale.key));
}

#[test]
fn restore_revokes_legacy_apple_grants_for_graylisted_tools_only() {
    for (name, signing_id) in [
        ("nscurl", "com.apple.nscurl"),
        ("renamed", "com.apple.openssl"),
        ("rsync", "unknown"),
    ] {
        let mut e = engine();
        let mut f = flow("graylisted", "example.test");
        f.app.signer = FirewallSigner::Apple;
        f.app.path = format!("/usr/bin/{name}");
        f.app.signing_id = Some(signing_id.into());
        f.app.key = identity_key(&f.app);
        rule(
            &mut e,
            Some(f.app.clone()),
            None,
            None,
            FirewallAction::Allow,
        );
        e.state.rules.last_mut().unwrap().source = FirewallRuleSource::Apple;
        e.state.baseline.insert(f.app.key.clone());
        let mut restored = Engine::restore(e.persistent()).unwrap();
        assert!(restored.state.rules.is_empty());
        restored.connect(1_000);
        assert!(restored.decide(&f).is_none());
        rule(
            &mut restored,
            Some(f.app.clone()),
            None,
            None,
            FirewallAction::Allow,
        );
        let mut explicit = Engine::restore(restored.persistent()).unwrap();
        assert_eq!(explicit.decide(&f).unwrap().action, FirewallAction::Allow);
    }
    let mut e = engine();
    let mut f = flow("normal-apple", "example.test");
    f.app.signer = FirewallSigner::Apple;
    f.app.path = "/usr/bin/dig".into();
    f.app.signing_id = Some("com.apple.dig".into());
    f.app.key = identity_key(&f.app);
    rule(
        &mut e,
        Some(f.app.clone()),
        None,
        None,
        FirewallAction::Allow,
    );
    e.state.rules.last_mut().unwrap().source = FirewallRuleSource::Apple;
    assert_eq!(
        Engine::restore(e.persistent()).unwrap().state.rules.len(),
        1
    );
}

#[test]
fn activity_allow_reusing_exact_ip_target_replaces_the_block_even_with_a_hostname() {
    for remote in [
        "192.0.2.7",
        "192.0.2.7/32",
        "2001:db8::7",
        "2001:db8::7/128",
    ] {
        let mut e = engine();
        let mut f = flow("address", "example.test");
        f.remote_address = remote.split('/').next().unwrap().into();
        rule(
            &mut e,
            Some(f.app.clone()),
            Some(remote),
            Some(443),
            FirewallAction::Block,
        );
        assert_eq!(e.decide(&f).unwrap().action, FirewallAction::Block);
        rule(
            &mut e,
            Some(f.app.clone()),
            Some(remote),
            Some(443),
            FirewallAction::Allow,
        );
        assert_eq!(e.decide(&f).unwrap().action, FirewallAction::Allow);
        assert_eq!(e.state.rules.len(), 1);
    }
}
