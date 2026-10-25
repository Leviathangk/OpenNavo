// SPDX-License-Identifier: GPL-3.0-only
use opennavo_desktop_lib::{
    AppError,
    firewall::{bridge::Bridge, service::Update, *},
};
use opennavo_firewall_core::engine::{Engine, Flow};
use serde_json::{Value, json};
use std::sync::{Arc, Barrier, Mutex};

struct Fake {
    status: Mutex<FirewallStatus>,
    engine: Mutex<Engine>,
    disconnected: Mutex<bool>,
    disconnect_detail: Mutex<Option<String>>,
    fail_configure: Mutex<bool>,
    lose_configure_reply: Mutex<bool>,
    configure_count: Mutex<usize>,
    /// Every request after status, in order, for checks on what the service asked.
    requests: Mutex<Vec<Value>>,
    submission_pause: Mutex<Option<(Arc<Barrier>, Arc<Barrier>)>>,
    uninstall_result: Mutex<Option<FirewallExtensionState>>,
    fail_cleanup: Mutex<bool>,
    cleanup_confirmed: Mutex<bool>,
}
impl Fake {
    fn new() -> Arc<Self> {
        Arc::new(Self {
            status: Mutex::new(FirewallStatus {
                availability: FirewallAvailability::Available,
                ..FirewallStatus::not_bundled()
            }),
            engine: Mutex::new(Engine::default()),
            disconnected: Mutex::new(false),
            disconnect_detail: Mutex::new(None),
            fail_configure: Mutex::new(false),
            lose_configure_reply: Mutex::new(false),
            configure_count: Mutex::new(0),
            requests: Mutex::new(vec![]),
            submission_pause: Mutex::new(None),
            uninstall_result: Mutex::new(None),
            fail_cleanup: Mutex::new(false),
            cleanup_confirmed: Mutex::new(true),
        })
    }
    fn installed(&self) {
        let mut status = self.status.lock().unwrap();
        status.extension = FirewallExtensionState::Installed;
        status.filter = FirewallFilterState::On;
    }
    fn fixture_flow() -> Flow {
        serde_json::from_value(json!({"id":"alert","app":{"key":"path:/fixture/tool","name":"tool","path":"/fixture/tool","bundlePath":null,"bundleId":null,"signingId":null,"teamId":null,"teamName":null,"signer":"unsigned","iconPath":null},"pid":42,"processStart":1,"args":[],"ancestors":[],"remoteAddress":"192.0.2.1","remoteHost":null,"port":443,"protocol":"tcp","at":1000,"monotonicMs":1000})).unwrap()
    }
    fn pending(&self) {
        let mut e = self.engine.lock().unwrap();
        e.connect(1000);
        e.flow(Self::fixture_flow());
    }
}
impl Bridge for Fake {
    fn request(&self, request: Value) -> Result<Value, AppError> {
        let op = request["op"].as_str().unwrap();
        if op == "status" {
            return Ok(json!(*self.status.lock().unwrap()));
        }
        self.requests.lock().unwrap().push(request.clone());
        if op == "cleanup" {
            assert!(matches!(
                self.status.lock().unwrap().extension,
                FirewallExtensionState::NotInstalled | FirewallExtensionState::RemovalPending
            ));
            if *self.fail_cleanup.lock().unwrap() {
                return Err(failure(FirewallErrorCode::Removal));
            }
            if !*self.cleanup_confirmed.lock().unwrap() {
                return Ok(json!(false));
            }
            self.engine.lock().unwrap().reset();
            return Ok(json!(true));
        }
        if ["enable", "disable", "uninstall"].contains(&op) {
            let mut status = self.status.lock().unwrap();
            status.filter = if op == "enable" {
                FirewallFilterState::On
            } else {
                FirewallFilterState::Off
            };
            if op == "uninstall"
                && let Some(result) = *self.uninstall_result.lock().unwrap()
            {
                status.extension = result;
            }
            return Ok(json!(*status));
        }
        if *self.disconnected.lock().unwrap() {
            let mut error = failure(FirewallErrorCode::Disconnected);
            error.detail = self.disconnect_detail.lock().unwrap().clone();
            return Err(error);
        }
        if op == "configure" {
            *self.configure_count.lock().unwrap() += 1;
            if *self.fail_configure.lock().unwrap() {
                return Err(failure(FirewallErrorCode::Disconnected));
            }
            if *self.lose_configure_reply.lock().unwrap() {
                self.engine
                    .lock()
                    .unwrap()
                    .request(request.clone())
                    .unwrap();
                return Err(failure(FirewallErrorCode::Activation));
            }
        }
        let request = match op {
            "flush" => json!({"op":"suspend"}),
            _ => request,
        };
        self.engine
            .lock()
            .unwrap()
            .request(request)
            .map_err(failure)
    }
    fn disconnect(&self) {
        let pause = self.submission_pause.lock().unwrap().take();
        if let Some((entered, release)) = pause {
            entered.wait();
            release.wait();
        }
        self.engine.lock().unwrap().disconnect(2000);
    }
}
fn ops(fake: &Fake) -> Vec<String> {
    fake.requests
        .lock()
        .unwrap()
        .iter()
        .map(|r| r["op"].as_str().unwrap().to_owned())
        .collect()
}
fn configures(fake: &Fake) -> Vec<Value> {
    fake.requests
        .lock()
        .unwrap()
        .iter()
        .filter(|r| r["op"] == "configure")
        .cloned()
        .collect()
}
/// The extension's installed-app scan ends a configuration's preparation; this fake has no scanner.
fn finish_baseline(fake: &Fake) {
    fake.engine
        .lock()
        .unwrap()
        .request(json!({"op":"baseline_finish"}))
        .unwrap();
}
fn service(fake: Arc<Fake>) -> (Service, Arc<Mutex<Vec<Update>>>) {
    let updates = Arc::new(Mutex::new(vec![]));
    let sink = updates.clone();
    (
        Service::new(fake, Arc::new(move |u| sink.lock().unwrap().push(u)), None),
        updates,
    )
}
#[test]
fn uninstalled_returns_defaults_and_unbundled_returns_the_contract_error() {
    let fake = Fake::new();
    let (s, _) = service(fake.clone());
    assert_eq!(s.settings().unwrap(), FirewallSettings::default());
    fake.status.lock().unwrap().availability = FirewallAvailability::NotBundled;
    assert_eq!(s.settings().unwrap_err().code, "E_FW_NOT_BUNDLED");
    assert!(s.rules().unwrap().is_empty());
}
#[test]
fn removed_extension_awaiting_reboot_is_not_asked() {
    let fake = Fake::new();
    fake.status.lock().unwrap().extension = FirewallExtensionState::RemovalPending;
    // Any request past status would fail: the removed extension no longer runs.
    *fake.disconnected.lock().unwrap() = true;
    let (s, _) = service(fake);
    assert_eq!(s.settings().unwrap(), FirewallSettings::default());
    assert!(s.rules().unwrap().is_empty());
    assert!(s.alerts().unwrap().is_empty());
    let status = s.status().unwrap();
    assert_eq!(status.extension, FirewallExtensionState::RemovalPending);
    assert!(status.error.is_none());
}
#[test]
fn reply_immediately_emits_full_empty_alerts_and_stale_reply_replays_them() {
    let fake = Fake::new();
    fake.installed();
    fake.pending();
    let (s, updates) = service(fake);
    assert_eq!(s.status().unwrap().pending_alerts, 1);
    updates.lock().unwrap().clear();
    let request = json!({"op":"alert_reply","reply":{"alertId":"alert","action":"allow","scope":"app","until":"always"}});
    s.mutate::<()>(request.clone()).unwrap();
    assert!(
        updates
            .lock()
            .unwrap()
            .iter()
            .any(|u| matches!(u,Update::Alerts(a) if a.is_empty()))
    );
    updates.lock().unwrap().clear();
    assert_eq!(
        s.mutate::<()>(request).unwrap_err().code,
        "E_FW_UNKNOWN_ALERT"
    );
    assert!(
        updates
            .lock()
            .unwrap()
            .iter()
            .any(|u| matches!(u,Update::Alerts(a) if a.is_empty()))
    );
}
#[test]
fn timeout_emits_released_alert_and_late_reply_creates_rule_without_second_resume() {
    let fake = Fake::new();
    fake.installed();
    fake.pending();
    let (s, updates) = service(fake.clone());
    s.status().unwrap();
    updates.lock().unwrap().clear();
    {
        let mut e = fake.engine.lock().unwrap();
        e.connect(121_000);
        e.expire(121_000, 121_000);
        assert_eq!(e.resolutions.len(), 1);
        e.resolutions.clear();
    }
    assert_eq!(s.status().unwrap().pending_alerts, 1);
    assert!(updates.lock().unwrap().iter().any(|u| matches!(u,
        Update::Alerts(a) if a.len() == 1 && a[0].released && a[0].deadline.is_none())));
    s.mutate::<()>(json!({"op":"alert_reply","reply":{"alertId":"alert","action":"block","scope":"app","until":"always"}})).unwrap();
    assert_eq!(s.status().unwrap().pending_alerts, 0);
    let e = fake.engine.lock().unwrap();
    assert_eq!(e.state.rules.len(), 1);
    assert_eq!(e.state.rules[0].action, FirewallAction::Block);
    assert!(e.resolutions.is_empty());
    assert_eq!(e.state.events[0].reason, FirewallVerdictReason::Timeout);
}

#[test]
fn disconnect_clears_pending_ui_and_reports_failure() {
    let fake = Fake::new();
    fake.installed();
    fake.pending();
    let (s, updates) = service(fake.clone());
    s.status().unwrap();
    *fake.disconnected.lock().unwrap() = true;
    let detail = "扩展文件权限异常，请重新安装";
    *fake.disconnect_detail.lock().unwrap() = Some(detail.into());
    let status = s.status().unwrap();
    assert!(!status.connected);
    assert_eq!(status.pending_alerts, 0);
    let error = status.error.unwrap();
    assert_eq!(error.code, FirewallErrorCode::Disconnected);
    assert_eq!(error.detail.as_deref(), Some(detail));
    assert!(
        updates
            .lock()
            .unwrap()
            .iter()
            .any(|u| matches!(u,Update::Alerts(a) if a.is_empty()))
    );
}

#[test]
fn shutdown_releases_questions_but_leaves_extension_rules_and_lists_active() {
    let fake = Fake::new();
    fake.installed();
    fake.pending();
    let (s, _) = service(fake.clone());
    s.mutate::<FirewallRule>(json!({"op":"rule_save","input":{
        "id":null,"appKey":null,"appPath":null,"action":"block","remote":"192.0.2.10","port":null
    }}))
    .unwrap();
    fake.engine
        .lock()
        .unwrap()
        .blocklist_load(
            None,
            "/fixture/list".into(),
            "list".into(),
            b"192.0.2.11",
            1000,
        )
        .unwrap();
    s.shutdown();
    assert!(s.is_stopped());
    let mut e = fake.engine.lock().unwrap();
    assert!(e.alerts().is_empty());
    assert_eq!(e.resolutions.len(), 1);
    assert_eq!(e.resolutions[0].action, FirewallAction::Allow);
    assert_eq!(e.state.rules.len(), 1);
    assert_eq!(e.state.blocklists.len(), 1);
    e.state.settings.mode = FirewallMode::Block;
    for (address, action, reason) in [
        (
            "192.0.2.10",
            FirewallAction::Block,
            FirewallVerdictReason::Rule,
        ),
        (
            "192.0.2.11",
            FirewallAction::Block,
            FirewallVerdictReason::Blocklist,
        ),
        (
            "192.0.2.12",
            FirewallAction::Allow,
            FirewallVerdictReason::NoClient,
        ),
    ] {
        let mut f = Fake::fixture_flow();
        f.id = address.into();
        f.remote_address = address.into();
        f.monotonic_ms = 3000;
        f.at = 3000;
        assert_eq!(e.flow(f), Some(action));
        assert_eq!(e.state.events[0].reason, reason);
    }
}
#[test]
fn failed_configure_retains_enable_options_for_retry() {
    let fake = Fake::new();
    fake.installed();
    *fake.fail_configure.lock().unwrap() = true;
    let (s, _) = service(fake.clone());
    s.enable(FirewallEnableOptions {
        allow_apple: false,
        allow_installed: false,
    })
    .unwrap();
    *fake.fail_configure.lock().unwrap() = false;
    s.refresh(false).unwrap();
    assert!(!s.settings().unwrap().allow_apple);
    assert!(!s.settings().unwrap().allow_installed);
    s.shutdown();
    assert!(s.is_stopped());
    assert!(s.refresh(false).is_err());
}
#[test]
fn replacement_properties_recovery_continues_pending_configuration_once() {
    let fake = Fake::new();
    fake.status.lock().unwrap().extension = FirewallExtensionState::NeedsReboot;
    let (service, _) = service(fake.clone());
    let pending = service
        .enable(FirewallEnableOptions {
            allow_apple: false,
            allow_installed: true,
        })
        .unwrap();
    assert_eq!(pending.extension, FirewallExtensionState::NeedsReboot);
    assert_eq!(*fake.configure_count.lock().unwrap(), 0);
    // Native properties discover the bundled replacement is already enabled
    // and finish enabling the filter, despite the old version awaiting removal.
    fake.installed();
    let recovered = service.refresh(false).unwrap();
    assert_eq!(recovered.extension, FirewallExtensionState::Installed);
    assert_eq!(recovered.filter, FirewallFilterState::On);
    assert!(recovered.connected && recovered.error.is_none());
    assert!(!service.settings().unwrap().allow_apple);
    assert!(service.settings().unwrap().allow_installed);
    service.refresh(false).unwrap();
    assert_eq!(*fake.configure_count.lock().unwrap(), 1);
}
#[test]
fn overview_maps_availability_and_filter_and_alert_acl_is_minimal() {
    use opennavo_desktop_lib::security::ProtectionState;
    let mut status = FirewallStatus::not_bundled();
    assert_eq!(protection_for(&status), ProtectionState::Unavailable);
    status.availability = FirewallAvailability::Available;
    assert_eq!(protection_for(&status), ProtectionState::Off);
    status.filter = FirewallFilterState::On;
    assert_eq!(protection_for(&status), ProtectionState::On);
    let capability: Value =
        serde_json::from_str(include_str!("../capabilities/firewall-alert.json")).unwrap();
    assert_eq!(capability["windows"], json!(["firewall-alert"]));
    assert_eq!(
        capability["permissions"],
        json!([
            "native-firewall-alert",
            "core:event:default",
            "core:window:allow-set-size"
        ])
    );
    let acl = include_str!("../permissions/native.toml")
        .split("[[permission]]")
        .find(|s| s.contains("identifier = \"native-firewall-alert\""))
        .unwrap();
    assert!(acl.contains("\"firewall_alerts\""));
    assert!(acl.contains("\"firewall_alert_reply\""));
    assert!(acl.contains("\"locale_get\""));
    assert!(!acl.contains("app_reveal"));
    assert!(!acl.contains("firewall_enable"));
}

#[test]
fn preparing_configure_is_sent_once_and_lost_ack_is_reconciled() {
    for lose_reply in [false, true] {
        let fake = Fake::new();
        fake.installed();
        *fake.lose_configure_reply.lock().unwrap() = lose_reply;
        let (s, _) = service(fake.clone());
        let first = s
            .enable(FirewallEnableOptions {
                allow_apple: false,
                allow_installed: true,
            })
            .unwrap();
        if lose_reply {
            assert_eq!(first.error.unwrap().code, FirewallErrorCode::Activation);
            assert!(first.connected);
        }
        for _ in 0..100 {
            let status = s.status().unwrap();
            assert!(status.preparing && status.connected);
            assert!(status.error.is_none());
        }
        assert_eq!(*fake.configure_count.lock().unwrap(), 1);
        fake.engine.lock().unwrap().baseline_finish(None);
        assert!(!s.status().unwrap().preparing);
        assert_eq!(*fake.configure_count.lock().unwrap(), 1);
    }
}

#[test]
fn recovered_snapshot_clears_stale_disconnected_error() {
    let fake = Fake::new();
    fake.installed();
    fake.status.lock().unwrap().error = Some(FirewallFailure {
        code: FirewallErrorCode::Disconnected,
        detail: None,
    });
    let (s, _) = service(fake.clone());
    let status = s.status().unwrap();
    assert!(status.connected);
    assert!(status.error.is_none());
    *fake.disconnected.lock().unwrap() = true;
    assert_eq!(
        s.status().unwrap().error.unwrap().code,
        FirewallErrorCode::Disconnected
    );
    *fake.disconnected.lock().unwrap() = false;
    assert!(s.status().unwrap().error.is_none());
}

#[test]
fn missing_service_reports_restart_without_configuring_or_changing_policy() {
    let fake = Fake::new();
    fake.installed();
    fake.pending();
    let rules = {
        let mut engine = fake.engine.lock().unwrap();
        engine.request(json!({"op":"alert_reply","reply":{"alertId":"alert","action":"block","scope":"app","until":"always"}})).unwrap();
        engine.state.settings.mode = FirewallMode::Block;
        engine.state.rules.clone()
    };
    let expected = FirewallFailure {
        code: FirewallErrorCode::Disconnected,
        detail: Some("firewall_service_missing".into()),
    };
    fake.status.lock().unwrap().error = Some(expected.clone());
    *fake.disconnected.lock().unwrap() = true;
    let (s, _) = service(fake.clone());
    for _ in 0..30 {
        let status = s.status().unwrap();
        assert_eq!(status.error, Some(expected.clone()));
        assert!(!status.connected);
        assert_eq!(status.extension, FirewallExtensionState::Installed);
        assert_eq!(status.filter, FirewallFilterState::On);
    }
    assert_eq!(*fake.configure_count.lock().unwrap(), 0);
    *fake.disconnected.lock().unwrap() = false;
    let ready = s.status().unwrap();
    assert!(ready.connected && ready.error.is_none());
    assert_eq!(ready.mode, FirewallMode::Block);
    assert_eq!(*fake.configure_count.lock().unwrap(), 0);
    assert_eq!(fake.engine.lock().unwrap().state.rules, rules);
}

#[test]
fn activation_without_options_preserves_persisted_settings_after_reconnect() {
    let fake = Fake::new();
    fake.installed();
    let expected = FirewallSettings {
        mode: FirewallMode::Block,
        allow_apple: false,
        allow_installed: false,
    };
    fake.engine.lock().unwrap().state.settings = expected.clone();
    *fake.disconnected.lock().unwrap() = true;
    let service = Service::new(fake.clone(), Arc::new(|_| {}), None);
    service.disable(false).unwrap();
    service.enable(None).unwrap();
    *fake.disconnected.lock().unwrap() = false;
    service.status().unwrap();
    assert_eq!(service.settings().unwrap(), expected);
    assert_eq!(*fake.configure_count.lock().unwrap(), 0);
}
#[test]
fn pausing_does_not_suspend_policy_before_macos_stops_the_filter() {
    // If saving the disabled filter fails, the filter keeps running; a suspended engine would then allow everything.
    let fake = Fake::new();
    fake.installed();
    let (s, _) = service(fake.clone());
    s.disable(false).unwrap();
    assert!(
        !ops(&fake)
            .iter()
            .any(|op| op == "flush" || op == "suspend" || op == "reset")
    );
    // Removal also waits for macOS confirmation before any destructive reset.
    s.disable(true).unwrap();
    assert!(!ops(&fake).iter().any(|op| op == "reset"));
}
#[test]
fn cancelled_removal_preserves_all_persisted_policy_without_reconfiguration() {
    let fake = Fake::new();
    fake.installed();
    let (s, _) = service(fake.clone());
    let before = {
        let mut engine = fake.engine.lock().unwrap();
        let flow = Fake::fixture_flow();
        engine.remember(flow.app.clone());
        engine
            .request(json!({"op":"rule_save","input":{
                "id":null,"appKey":flow.app.key,"appPath":null,"action":"block",
                "remote":null,"port":null
            }}))
            .unwrap();
        engine
            .blocklist_load(
                None,
                "/fixture/list".into(),
                "fixture".into(),
                b"blocked.example",
                1000,
            )
            .unwrap();
        engine.state.baseline.insert("fixture-baseline".into());
        engine.state.baseline_initialized = true;
        engine.flow(flow);
        serde_json::to_value(engine.persistent()).unwrap()
    };
    s.refresh(false).unwrap();
    s.disable(true).unwrap();
    fake.installed(); // macOS password dialog cancelled / removal failed.
    s.refresh(false).unwrap();
    s.refresh(false).unwrap();
    assert_eq!(
        serde_json::to_value(fake.engine.lock().unwrap().persistent()).unwrap(),
        before
    );
    assert!(configures(&fake).is_empty());
    assert!(!ops(&fake).iter().any(|op| op == "reset"));
}

#[test]
fn setting_up_after_a_completed_removal_wipes_once_and_a_cancelled_removal_keeps_rules() {
    let temp = tempfile::tempdir().unwrap();
    let database = opennavo_desktop_lib::db::Database::open(&temp.path().join("app.db")).unwrap();
    let options = FirewallEnableOptions {
        allow_apple: true,
        allow_installed: false,
    };
    let fake = Fake::new();
    fake.installed();
    let sink = Arc::new(|_| {}) as opennavo_desktop_lib::firewall::service::Sink;
    let s = Service::new(
        fake.clone(),
        sink.clone(),
        Some((database.clone(), temp.path().into())),
    );
    // A dismissed password prompt: the extension stays installed, so nothing is wiped later.
    s.disable(true).unwrap();
    fake.installed();
    s.refresh(false).unwrap();
    fake.status.lock().unwrap().extension = FirewallExtensionState::Failed;
    s.enable(options.clone()).unwrap();
    fake.installed();
    s.refresh(false).unwrap();
    assert_eq!(configures(&fake).last().unwrap()["fresh"], false);
    finish_baseline(&fake);
    // A completed removal cleans immediately and keeps a one-time reinstall fallback.
    *fake.uninstall_result.lock().unwrap() = Some(FirewallExtensionState::Uninstalling);
    s.disable(true).unwrap();
    fake.status.lock().unwrap().extension = FirewallExtensionState::RemovalPending;
    s.refresh(false).unwrap();
    s.enable(options.clone()).unwrap();
    fake.installed();
    s.refresh(false).unwrap();
    assert_eq!(configures(&fake).last().unwrap()["fresh"], true);
    finish_baseline(&fake);
    // The flag survives an OpenNavo restart until a setup used it, and isn't reused afterwards.
    let restarted = Service::new(fake.clone(), sink, Some((database, temp.path().into())));
    fake.status.lock().unwrap().extension = FirewallExtensionState::NotInstalled;
    restarted.enable(options).unwrap();
    fake.installed();
    restarted.refresh(false).unwrap();
    assert_eq!(configures(&fake).last().unwrap()["fresh"], false);
}
#[test]
fn setup_choices_survive_a_restart_before_the_extension_is_approved() {
    let temp = tempfile::tempdir().unwrap();
    let database = opennavo_desktop_lib::db::Database::open(&temp.path().join("app.db")).unwrap();
    let sink = Arc::new(|_| {}) as opennavo_desktop_lib::firewall::service::Sink;
    let fake = Fake::new();
    let s = Service::new(
        fake.clone(),
        sink.clone(),
        Some((database.clone(), temp.path().into())),
    );
    s.enable(FirewallEnableOptions {
        allow_apple: false,
        allow_installed: false,
    })
    .unwrap();
    assert!(configures(&fake).is_empty());
    // OpenNavo restarts; approval completes later and the user turns it on with the extension's default settings.
    let restarted = Service::new(fake.clone(), sink, Some((database, temp.path().into())));
    fake.installed();
    restarted
        .enable(FirewallEnableOptions {
            allow_apple: true,
            allow_installed: true,
        })
        .unwrap();
    let settings = configures(&fake).last().unwrap()["settings"].clone();
    assert_eq!(settings["allowApple"], false);
    assert_eq!(settings["allowInstalled"], false);
    assert!(!restarted.settings().unwrap().allow_installed);
}
#[test]
fn rule_match_statistics_do_not_reload_the_rule_list_every_poll() {
    let fake = Fake::new();
    fake.installed();
    let (s, updates) = service(fake.clone());
    let rule: FirewallRule = s
        .mutate(json!({"op":"rule_save","input":{
            "id":null,"appKey":null,"appPath":null,"action":"block","remote":"192.0.2.1","port":null
        }}))
        .unwrap();
    s.refresh(false).unwrap();
    updates.lock().unwrap().clear();
    let rules_changed = |updates: &Arc<Mutex<Vec<Update>>>| {
        updates
            .lock()
            .unwrap()
            .iter()
            .filter(|u| matches!(u, Update::Changed(FirewallChangeKind::Rules)))
            .count()
    };
    for _ in 0..3 {
        // Each connection the global rule blocks updates its match statistics.
        fake.engine.lock().unwrap().flow(Fake::fixture_flow());
        s.refresh(false).unwrap();
    }
    assert_eq!(rules_changed(&updates), 0);
    // Structural changes are announced at once.
    s.mutate::<Value>(json!({"op":"rule_delete","ids":[rule.id]}))
        .unwrap();
    assert_eq!(rules_changed(&updates), 1);
}

#[test]
fn removal_submission_excludes_polling_until_native_request_is_submitted() {
    let fake = Fake::new();
    fake.installed();
    *fake.uninstall_result.lock().unwrap() = Some(FirewallExtensionState::NotInstalled);
    let (service, _) = service(fake.clone());
    let service = Arc::new(service);
    let entered = Arc::new(Barrier::new(2));
    let release = Arc::new(Barrier::new(2));
    *fake.submission_pause.lock().unwrap() = Some((entered.clone(), release.clone()));
    let remove = {
        let service = service.clone();
        std::thread::spawn(move || service.disable(true).unwrap())
    };
    entered.wait(); // intent is set, but no native uninstall request has been made yet.
    let (ready_tx, ready_rx) = std::sync::mpsc::channel();
    let (done_tx, done_rx) = std::sync::mpsc::channel();
    let poll = {
        let service = service.clone();
        std::thread::spawn(move || {
            ready_tx.send(()).unwrap();
            let status = service.refresh(false).unwrap();
            done_tx.send(()).unwrap();
            status
        })
    };
    ready_rx.recv().unwrap();
    let premature = done_rx
        .recv_timeout(std::time::Duration::from_millis(100))
        .is_ok();
    release.wait();
    remove.join().unwrap();
    poll.join().unwrap();
    assert!(
        !premature,
        "poll must not clear the intent before native submission"
    );
    assert_eq!(ops(&fake).iter().filter(|op| *op == "cleanup").count(), 1);
    fake.status.lock().unwrap().extension = FirewallExtensionState::NotInstalled;
    service
        .enable(FirewallEnableOptions {
            allow_apple: true,
            allow_installed: true,
        })
        .unwrap();
    fake.installed();
    service.refresh(false).unwrap();
    assert_eq!(configures(&fake).last().unwrap()["fresh"], true);
}

#[test]
fn successful_removal_cleans_immediately_and_failed_cleanup_can_be_retried() {
    for removed in [
        FirewallExtensionState::NotInstalled,
        FirewallExtensionState::RemovalPending,
    ] {
        let fake = Fake::new();
        fake.installed();
        fake.engine
            .lock()
            .unwrap()
            .state
            .baseline
            .insert("private-history".into());
        let (service, _) = service(fake.clone());
        *fake.uninstall_result.lock().unwrap() = Some(FirewallExtensionState::Uninstalling);
        service.disable(true).unwrap();
        fake.status.lock().unwrap().extension = removed;
        *fake.fail_cleanup.lock().unwrap() = true;
        assert_eq!(
            service.refresh(false).unwrap().error.unwrap().code,
            FirewallErrorCode::Removal
        );
        assert!(
            fake.engine
                .lock()
                .unwrap()
                .state
                .baseline
                .contains("private-history")
        );
        service.refresh(false).unwrap();
        assert_eq!(ops(&fake).iter().filter(|op| *op == "cleanup").count(), 1);
        *fake.fail_cleanup.lock().unwrap() = false;
        assert!(service.disable(true).unwrap().error.is_none());
        assert!(fake.engine.lock().unwrap().state.baseline.is_empty());
        service.refresh(false).unwrap();
        assert_eq!(ops(&fake).iter().filter(|op| *op == "cleanup").count(), 2);
    }
}

#[test]
fn unconfirmed_native_properties_do_not_count_as_successful_cleanup() {
    let fake = Fake::new();
    fake.installed();
    *fake.uninstall_result.lock().unwrap() = Some(FirewallExtensionState::NotInstalled);
    *fake.cleanup_confirmed.lock().unwrap() = false;
    let (service, _) = service(fake.clone());
    assert_eq!(
        service.disable(true).unwrap().extension,
        FirewallExtensionState::Uninstalling
    );
    *fake.cleanup_confirmed.lock().unwrap() = true;
    assert_eq!(
        service.refresh(false).unwrap().extension,
        FirewallExtensionState::NotInstalled
    );
    assert_eq!(ops(&fake).iter().filter(|op| *op == "cleanup").count(), 2);
}

#[test]
fn pending_removal_cleanup_survives_application_restart() {
    let temp = tempfile::tempdir().unwrap();
    let database = opennavo_desktop_lib::db::Database::open(&temp.path().join("app.db")).unwrap();
    let fake = Fake::new();
    fake.installed();
    fake.engine
        .lock()
        .unwrap()
        .state
        .baseline
        .insert("private-history".into());
    *fake.uninstall_result.lock().unwrap() = Some(FirewallExtensionState::Uninstalling);
    let sink = Arc::new(|_| {}) as opennavo_desktop_lib::firewall::service::Sink;
    let service = Service::new(
        fake.clone(),
        sink.clone(),
        Some((database.clone(), temp.path().into())),
    );
    service.disable(true).unwrap();
    drop(service);
    fake.status.lock().unwrap().extension = FirewallExtensionState::NotInstalled;
    let restarted = Service::new(fake.clone(), sink, Some((database, temp.path().into())));
    restarted.refresh(false).unwrap();
    assert!(fake.engine.lock().unwrap().state.baseline.is_empty());
    assert_eq!(ops(&fake).iter().filter(|op| *op == "cleanup").count(), 1);
    restarted.refresh(false).unwrap();
    assert_eq!(ops(&fake).iter().filter(|op| *op == "cleanup").count(), 1);
}
