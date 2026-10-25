// SPDX-License-Identifier: GPL-3.0-only
use opennavo_desktop_lib::{events::*, firewall::*, specta_builder};
use serde::{Serialize, de::DeserializeOwned};
use serde_json::{Value, json};

#[test]
fn firewall_ipc_exports_match_command_permissions_and_event_names() {
    let temp = tempfile::tempdir().unwrap();
    let path = temp.path().join("bindings.ts");
    specta_builder()
        .export(specta_typescript::Typescript::default(), &path)
        .unwrap();
    let bindings = std::fs::read_to_string(path).unwrap();
    let acl = include_str!("../permissions/native.toml");
    let main = acl
        .split("[[permission]]")
        .find(|section| section.contains("identifier = \"native-main\""))
        .unwrap();
    for command in [
        "firewall_status",
        "firewall_enable",
        "firewall_disable",
        "firewall_uninstall",
        "firewall_open_approval",
        "firewall_app_identify",
        "firewall_rules",
        "firewall_rule_save",
        "firewall_rule_delete",
        "firewall_alerts",
        "firewall_alert_reply",
        "firewall_alert_show",
        "firewall_events",
        "firewall_events_clear",
        "firewall_blocklists",
        "firewall_blocklist_add",
        "firewall_blocklist_set",
        "firewall_blocklist_refresh",
        "firewall_blocklist_remove",
        "firewall_settings",
        "firewall_settings_set",
    ] {
        assert!(bindings.contains(&format!("__TAURI_INVOKE(\"{command}\"")));
        assert!(main.contains(&format!("\"{command}\"")));
    }
    for (client, wire, payload) in [
        ("firewallStatus", "firewall:status", "FirewallStatusEvent"),
        ("firewallAlerts", "firewall:alerts", "FirewallAlertsEvent"),
        (
            "firewallChanged",
            "firewall:changed",
            "FirewallChangedEvent",
        ),
        (
            "firewallActivity",
            "firewall:activity",
            "FirewallActivityEvent",
        ),
    ] {
        assert!(bindings.contains(&format!("{client}: makeEvent<{payload}>(\"{wire}\")")));
    }
    for name in [
        "FirewallAvailability",
        "FirewallExtensionState",
        "FirewallFilterState",
        "FirewallMode",
        "FirewallStatus",
        "FirewallFailure",
        "FirewallErrorCode",
        "FirewallSigner",
        "FirewallApp",
        "FirewallAction",
        "FirewallRuleSource",
        "FirewallRule",
        "FirewallRuleInput",
        "FirewallProcess",
        "FirewallAlert",
        "FirewallReply",
        "FirewallVerdictReason",
        "FirewallEvent",
        "FirewallBlocklist",
        "FirewallSettings",
    ] {
        assert!(bindings.contains(&format!("export type {name} =")));
    }
    for signature in [
        "firewallEnable: (options: {\n\tallowApple: boolean,\n\tallowInstalled: boolean,\n} | null)",
        "firewallRuleSave: (input: FirewallRuleInput)",
        "firewallAlertReply: (reply: FirewallReply)",
        "firewallEvents: (query: FirewallEventsQuery)",
        "firewallSettingsSet: (settings: FirewallSettings)",
    ] {
        assert!(bindings.contains(signature), "missing {signature}");
    }
}

fn round_trip<T: DeserializeOwned + Serialize>(value: Value) {
    let decoded: T = serde_json::from_value(value.clone()).unwrap();
    assert_eq!(serde_json::to_value(decoded).unwrap(), value);
}

#[test]
fn firewall_payloads_keep_camel_case_nulls_and_event_envelopes() {
    let app = json!({
        "key":"unsigned:/fixture/tool", "name":"tool", "path":"/fixture/tool",
        "bundlePath":null, "bundleId":null, "signingId":null, "teamId":null,
        "teamName":null, "signer":"unsigned", "iconPath":null
    });
    round_trip::<FirewallApp>(app.clone());
    round_trip::<FirewallRule>(json!({
        "id":"rule", "app":null, "action":"block", "remote":null, "port":null,
        "until":"always", "pid":null, "source":"user", "createdAt":1,
        "lastMatchAt":null, "matchCount":0
    }));
    round_trip::<FirewallRuleInput>(json!({
        "id":null, "appKey":null, "appPath":null,
        "action":"allow", "remote":null, "port":null
    }));
    let alert = json!({
        "id":"alert", "app":app, "pid":42, "args":["--example"],
        "ancestors":[{"pid":40,"name":"shell","path":null}],
        "remoteAddress":"192.0.2.1", "remoteHost":null, "port":443,
        "protocol":"tcp", "at":1, "waiting":2, "released":false, "deadline":120001
    });
    round_trip::<FirewallAlert>(alert.clone());
    let mut released = alert.clone();
    released["released"] = json!(true);
    released["deadline"] = Value::Null;
    round_trip::<FirewallAlert>(released);
    round_trip::<FirewallAlertsEvent>(json!({"alerts":[alert]}));
    round_trip::<FirewallReply>(json!({
        "alertId":"alert", "action":"allow", "scope":"destination", "until":"app_quits"
    }));
    let event = json!({
        "id":"event", "at":1, "app":app, "remoteAddress":"192.0.2.1",
        "remoteHost":null, "port":53, "protocol":"udp", "action":"allow",
        "reason":"no_client", "ruleId":null, "blocklistId":null, "count":2
    });
    round_trip::<FirewallEvent>(event.clone());
    let mut timed_out = event.clone();
    timed_out["reason"] = json!("timeout");
    round_trip::<FirewallEvent>(timed_out);
    round_trip::<FirewallActivityEvent>(json!({"events":[event]}));
    round_trip::<FirewallEventsQuery>(
        json!({"before":null,"beforeId":null,"limit":50,"blockedOnly":true}),
    );
    round_trip::<FirewallBlocklist>(json!({
        "id":"blocklist", "name":"example", "source":"https://example.invalid/list.txt",
        "enabled":true, "entries":0, "updatedAt":null, "error":null
    }));
    round_trip::<FirewallEnableOptions>(json!({"allowApple":true,"allowInstalled":false}));
    round_trip::<FirewallSettings>(json!({"mode":"ask","allowApple":true,"allowInstalled":true}));
    for what in ["rules", "settings", "blocklists"] {
        round_trip::<FirewallChangedEvent>(json!({"what":what}));
    }
}

#[test]
fn unbundled_status_and_status_event_have_the_same_wire_shape() {
    let expected = json!({
        "availability":"not_bundled", "extension":"not_installed", "extensionVersion":null,
        "filter":"off", "connected":false, "preparing":false, "mode":"ask", "ruleCount":0,
        "pendingAlerts":0, "blockedToday":0, "error":null
    });
    let status = FirewallStatus::not_bundled();
    assert_eq!(serde_json::to_value(&status).unwrap(), expected);
    assert_eq!(
        serde_json::to_value(FirewallStatusEvent(status)).unwrap(),
        expected
    );
}

#[test]
fn failures_preserve_every_contract_error_code() {
    for code in [
        "E_FW_NOT_BUNDLED",
        "E_FW_NOT_IN_APPLICATIONS",
        "E_FW_ACTIVATION",
        "E_FW_REMOVAL",
        "E_FW_FILTER",
        "E_FW_CONSENT_DENIED",
        "E_FW_DISCONNECTED",
        "E_FW_RULE_INVALID",
        "E_FW_UNKNOWN_ALERT",
        "E_FW_BLOCKLIST_URL",
        "E_FW_BLOCKLIST_FETCH",
        "E_FW_BLOCKLIST_FORMAT",
        "E_FW_BLOCKLIST_TOO_LARGE",
        "E_FW_BLOCKLIST_LIMIT",
        "E_FW_BLOCKLIST_FILE",
    ] {
        round_trip::<FirewallFailure>(json!({"code":code,"detail":null}));
    }
}
