use opennavo_desktop_lib::{
    AppError,
    monitor::{
        fan::{
            client::{Client, Transport, register},
            control::{Controller, Reply, Request},
            daemon::Executable,
            hardware::{Delay, Family, Hardware, SmcHardware, encoded, read_fans},
        },
        sensors::aggregate,
        smc::{Smc, Value},
        types::*,
    },
};
use std::{
    cell::Cell,
    collections::BTreeMap,
    sync::{Arc, Mutex},
};
fn fan(id: u32) -> FanState {
    FanState {
        id,
        name: format!("Fan {}", id + 1),
        rpm: 2000,
        min_rpm: 1200,
        max_rpm: 6000,
        target_rpm: Some(2000),
        mode: FanMode::Auto,
    }
}
#[derive(Default)]
struct FakeHardware {
    fans: Vec<FanState>,
    temperature: Option<f64>,
    serious: bool,
    fail_write: bool,
    fail_reset: bool,
    fail_read: bool,
    resets: u32,
    writes: Vec<(u32, u32)>,
}
impl FakeHardware {
    fn new() -> Self {
        Self {
            fans: vec![fan(0), fan(1)],
            temperature: Some(50.0),
            ..Default::default()
        }
    }
}
impl Hardware for FakeHardware {
    fn fans(&mut self) -> Result<Vec<FanState>, AppError> {
        if self.fail_read {
            Err(AppError::new("E_FAN_WRITE_FAILED", "fake"))
        } else {
            Ok(self.fans.clone())
        }
    }
    fn set_mode(&mut self, id: u32, mode: FanMode) -> Result<(), AppError> {
        if self.fail_write {
            return Err(AppError::new("E_FAN_WRITE_FAILED", "fake"));
        }
        self.fans[id as usize].mode = mode;
        Ok(())
    }
    fn set_speed(&mut self, id: u32, rpm: u32) -> Result<(), AppError> {
        self.writes.push((id, rpm));
        if self.fail_write {
            return Err(AppError::new("E_FAN_WRITE_FAILED", "fake"));
        }
        self.fans[id as usize].target_rpm = Some(rpm);
        Ok(())
    }
    fn reset(&mut self) -> Result<(), AppError> {
        self.resets += 1;
        if self.fail_reset {
            return Err(AppError::new("E_FAN_WRITE_FAILED", "fake"));
        }
        for f in &mut self.fans {
            f.mode = FanMode::Auto;
        }
        Ok(())
    }
    fn temperature(&mut self) -> Option<f64> {
        self.temperature
    }
    fn serious_pressure(&self) -> bool {
        self.serious
    }
}
fn control() -> Controller<FakeHardware> {
    Controller::new(FakeHardware::new(), FanControlSupport::Unverified, 0, 123)
}
fn speed(rpm: u32) -> Request {
    Request::SetSpeed { fan_id: None, rpm }
}
fn status(c: &mut Controller<FakeHardware>, at: u64) -> Reply {
    c.request(
        Request::Status {
            after: 0,
            epoch: None,
        },
        at,
        true,
    )
    .unwrap()
}
#[test]
fn startup_and_unauthorized_do_not_write_or_extend_heartbeat() {
    let mut c = control();
    assert_eq!(c.hardware.resets, 1);
    assert_eq!(
        status(&mut c, 0).changes[0].reason,
        FanReason::HelperStarted
    );
    c.request(speed(2000), 0, true).unwrap();
    let writes = c.hardware.writes.len();
    assert_eq!(
        c.request(speed(3000), 14999, false).unwrap_err().code,
        "E_FAN_UNAUTHORIZED"
    );
    assert_eq!(c.hardware.writes.len(), writes);
    c.watchdog(15000);
    assert_eq!(
        status(&mut c, 15001).changes.last().unwrap().reason,
        FanReason::HeartbeatLost
    );
}
#[test]
fn unsupported_gate_and_validate_every_target_before_writing() {
    let mut c = Controller::new(FakeHardware::new(), FanControlSupport::Unsupported, 0, 1);
    for request in [
        speed(2000),
        Request::SetMode {
            fan_id: None,
            mode: FanMode::Manual,
        },
    ] {
        assert_eq!(
            c.request(request, 0, true).unwrap_err().code,
            "E_FAN_UNSUPPORTED"
        );
    }
    assert!(c.hardware.writes.is_empty());
    // Every known chip is unverified, and that alone no longer blocks manual control.
    let mut c = control();
    assert_eq!(
        c.request(speed(0), 1, true).unwrap_err().code,
        "E_INVALID_ARG"
    );
    assert!(c.hardware.writes.is_empty());
    c.request(speed(1199), 2, true).unwrap();
    assert!(c.hardware.fans.iter().all(|f| f.target_rpm == Some(1200)));
    c.request(speed(6001), 3, true).unwrap();
    assert!(c.hardware.fans.iter().all(|f| f.target_rpm == Some(6000)));
    c.hardware.fans[1].min_rpm = 3000;
    c.request(speed(2000), 4, true).unwrap();
    assert_eq!(c.hardware.fans[1].target_rpm, Some(3000));
    assert_eq!(
        c.request(
            Request::SetMode {
                fan_id: Some(99),
                mode: FanMode::Auto,
            },
            3,
            true
        )
        .unwrap_err()
        .code,
        "E_INVALID_ARG"
    );
}
#[test]
fn heartbeat_exact_boundary_and_authenticated_status_keepalive() {
    let mut c = control();
    c.request(speed(3000), 0, true).unwrap();
    c.watchdog(14999);
    assert_eq!(c.hardware.fans[0].mode, FanMode::Manual);
    status(&mut c, 14999);
    c.watchdog(29998);
    assert_eq!(c.hardware.fans[0].mode, FanMode::Manual);
    c.watchdog(29999);
    assert_eq!(c.hardware.fans[0].mode, FanMode::Auto);
}
#[test]
fn overheat_after_ten_seconds_raises_manual_fans_to_maximum_and_keeps_them_manual() {
    let mut c = control();
    c.request(speed(2000), 0, true).unwrap();
    c.hardware.temperature = Some(95.0);
    c.watchdog(5000);
    status(&mut c, 10000);
    c.watchdog(14999);
    assert_eq!(c.hardware.fans[0].target_rpm, Some(2000));
    c.watchdog(15000);
    assert!(c.hardware.writes.ends_with(&[(0, 6000), (1, 6000)]));
    // Cooling only goes up: the fans stay manual at full speed instead of going back to a slower system curve.
    assert!(c.hardware.fans.iter().all(|f| f.mode == FanMode::Manual));
    let reply = status(&mut c, 15001);
    assert_eq!(reply.changes.last().unwrap().reason, FanReason::Overheat);
    // Already at full speed: nothing more to write or announce.
    let writes = c.hardware.writes.len();
    c.watchdog(16000);
    assert_eq!(c.hardware.writes.len(), writes);
    assert_eq!(status(&mut c, 16001).sequence, reply.sequence);
    // Lowered while it is still hot: back to full speed on the next check.
    c.request(speed(3000), 16500, true).unwrap();
    c.watchdog(17000);
    assert_eq!(c.hardware.fans[0].target_rpm, Some(6000));
    // Once it cools down, a lower manual speed stays.
    c.hardware.temperature = Some(80.0);
    c.watchdog(22000);
    c.request(speed(3000), 22500, true).unwrap();
    c.watchdog(23000);
    assert_eq!(c.hardware.fans[0].target_rpm, Some(3000));
}
#[test]
fn heat_never_blocks_entering_manual_or_boosting() {
    let mut c = control();
    c.hardware.temperature = Some(99.0);
    c.hardware.serious = true;
    c.request(
        Request::SetMode {
            fan_id: None,
            mode: FanMode::Manual,
        },
        0,
        true,
    )
    .unwrap();
    // Manual starts from the speed the system had chosen, then serious pressure raises it to full at once.
    assert_eq!(c.hardware.fans[0].target_rpm, Some(2000));
    c.watchdog(1000);
    assert!(
        c.hardware
            .fans
            .iter()
            .all(|f| f.mode == FanMode::Manual && f.target_rpm == Some(6000))
    );
    c.request(speed(6000), 1500, true).unwrap();
}
#[test]
fn cooling_resets_duration_serious_pressure_is_immediate() {
    let mut c = control();
    c.request(speed(2000), 0, true).unwrap();
    c.hardware.temperature = Some(96.0);
    c.watchdog(5000);
    c.hardware.temperature = Some(80.0);
    status(&mut c, 10000);
    c.watchdog(10000);
    c.hardware.temperature = Some(96.0);
    c.watchdog(15000);
    status(&mut c, 19000);
    c.watchdog(19999);
    assert_eq!(c.hardware.fans[0].mode, FanMode::Manual);
    c.hardware.serious = true;
    c.watchdog(20000);
    assert_eq!(c.hardware.fans[0].mode, FanMode::Manual);
    assert_eq!(c.hardware.fans[0].target_rpm, Some(6000));
}
#[test]
fn write_failure_returns_reason_and_failed_restore_blocks_manual_until_watchdog_recovers() {
    let mut c = control();
    c.hardware.fail_write = true;
    c.hardware.fail_reset = true;
    assert_eq!(
        c.request(speed(2000), 1, true).unwrap_err().code,
        "E_FAN_WRITE_FAILED"
    );
    assert_eq!(
        status(&mut c, 2).changes.last().unwrap().reason,
        FanReason::WriteFailed
    );
    let writes = c.hardware.writes.len();
    assert!(c.request(speed(2000), 3, true).is_err());
    assert_eq!(c.hardware.writes.len(), writes);
    c.watchdog(4);
    c.hardware.fail_reset = false;
    c.watchdog(5);
    c.hardware.fail_write = false;
    c.request(speed(2000), 6, true).unwrap();
}
#[test]
fn missing_temperature_refuses_manual_and_loss_during_manual_recovers() {
    let mut c = control();
    c.hardware.temperature = None;
    assert_eq!(
        c.request(speed(2000), 1, true).unwrap_err().code,
        "E_FAN_SENSOR_UNAVAILABLE"
    );
    assert!(c.hardware.writes.is_empty());
    c.hardware.temperature = Some(50.0);
    c.request(speed(2000), 2, true).unwrap();
    c.hardware.temperature = None;
    c.watchdog(5002);
    assert_eq!(c.hardware.fans[0].mode, FanMode::Auto);
}
#[test]
fn sleep_uninstall_and_user_reset_have_distinct_reasons() {
    let mut c = control();
    for reason in [FanReason::Sleep, FanReason::AppQuit, FanReason::User] {
        c.request(speed(2000), 1, true).unwrap();
        let r = c.request(Request::Reset { reason }, 2, true).unwrap();
        assert_eq!(r.changes.last().unwrap().reason, reason);
        assert!(r.fans.iter().all(|f| f.mode == FanMode::Auto));
    }
    assert_eq!(
        c.request(Request::Uninstall, 3, true)
            .unwrap()
            .changes
            .last()
            .unwrap()
            .reason,
        FanReason::HelperUninstalled
    );
    assert_eq!(
        c.request(
            Request::Reset {
                reason: FanReason::Overheat
            },
            4,
            true
        )
        .unwrap_err()
        .code,
        "E_INVALID_ARG"
    );
}
struct FakeTransport {
    control: Controller<FakeHardware>,
    state: FanHelperState,
    now: u64,
    /// The helper is registered but does not answer (crashed at start, killed, replaced).
    down: bool,
    exchanges: usize,
}
impl Transport for FakeTransport {
    fn state(&self) -> FanHelperState {
        self.state
    }
    fn install(&mut self) -> Result<(), AppError> {
        self.state = FanHelperState::RequiresApproval;
        Ok(())
    }
    fn uninstall(&mut self) -> Result<(), AppError> {
        self.state = FanHelperState::NotInstalled;
        Ok(())
    }
    fn exchange(&mut self, request: Request) -> Result<Reply, AppError> {
        self.exchanges += 1;
        if self.down {
            return Err(AppError::new("E_FAN_HELPER_UNAVAILABLE", "xpc"));
        }
        self.control.request(request, self.now, true)
    }
}
fn client(state: FanHelperState) -> Client<FakeTransport> {
    Client::new(
        FakeTransport {
            control: control(),
            state,
            now: 0,
            down: false,
            exchanges: 0,
        },
        FanControlSupport::Unverified,
        Arc::new(|_| {}),
    )
}
#[test]
fn allowed_is_independent_of_helper_and_approval() {
    let mut client = client(FanHelperState::NotInstalled);
    assert!(client.status().unwrap().manual_allowed);
    assert_eq!(
        client.install().unwrap().status,
        FanHelperState::RequiresApproval
    );
    assert!(client.allowed());
    assert_eq!(
        client.speed(None, 2000).unwrap_err().code,
        "E_FAN_HELPER_NOT_ENABLED"
    );
}
#[test]
fn established_control_uses_one_exchange_and_replays_safety_events() {
    let mut client = client(FanHelperState::Enabled);
    client.status().unwrap();
    let before = client.transport.exchanges;
    let fans = client.mode(None, FanMode::Manual).unwrap();
    assert!(fans.iter().all(|fan| fan.mode == FanMode::Manual));
    assert_eq!(client.transport.exchanges - before, 1);

    client.transport.control.watchdog(15000);
    client.transport.now = 15000;
    let before = client.transport.exchanges;
    let fans = client.speed(None, 3000).unwrap();
    assert!(fans.iter().all(|fan| fan.target_rpm == Some(3000)));
    assert_eq!(client.transport.exchanges - before, 1);
    // The command consumed the watchdog event and remembered the new target, not the old speed.
    client.sleep();
    client.wake();
    assert!(
        client
            .transport
            .control
            .hardware
            .fans
            .iter()
            .all(|fan| fan.target_rpm == Some(3000))
    );

    let before = client.transport.exchanges;
    client.mode(None, FanMode::Auto).unwrap();
    assert_eq!(client.transport.exchanges - before, 1);
}

#[test]
fn first_control_resets_previous_session_and_failure_does_not_retry_writes() {
    let mut client = client(FanHelperState::Enabled);
    client.mode(None, FanMode::Manual).unwrap();
    // Initial status, mandatory session reset, then the requested control command.
    assert_eq!(client.transport.exchanges, 3);
    client.transport.down = true;
    let before = client.transport.exchanges;
    assert_eq!(
        client.mode(None, FanMode::Auto).unwrap_err().code,
        "E_FAN_HELPER_UNAVAILABLE"
    );
    assert_eq!(client.transport.exchanges - before, 1);
}

#[test]
fn silent_helper_reads_as_error_and_can_still_be_uninstalled() {
    let mut client = client(FanHelperState::Enabled);
    client.transport.down = true;
    assert_eq!(client.status().unwrap().status, FanHelperState::Error);
    // launchd stops it on unregister; the helper itself hands the fans back on SIGTERM.
    client.uninstall().unwrap();
    assert_eq!(client.transport.state, FanHelperState::NotInstalled);

    // One that answers but cannot restore automatic control stays registered, so its watchdog keeps trying.
    let mut client = self::client(FanHelperState::Enabled);
    client.status().unwrap();
    client.speed(None, 3000).unwrap();
    client.transport.control.hardware.fail_reset = true;
    assert_eq!(client.uninstall().unwrap_err().code, "E_FAN_WRITE_FAILED");
    assert_eq!(client.transport.state, FanHelperState::Enabled);
}
#[test]
fn reinstall_retries_registration_only_while_launchd_still_removes_the_old_job() {
    // Codes per attempt (the last one repeats), the state after a refusal, and how many waits remain.
    let run = |codes: &[i32], state: FanHelperState, waits: usize| {
        let (attempts, waited) = (Cell::new(0), Cell::new(0));
        let result = register(
            || {
                attempts.set(attempts.get() + 1);
                codes[(attempts.get() - 1).min(codes.len() - 1)]
            },
            || state,
            || {
                waited.set(waited.get() + 1);
                waited.get() <= waits
            },
        );
        (result, attempts.get(), waited.get())
    };
    // launchd refuses the new job until the old helper is gone, then accepts it.
    assert_eq!(
        run(&[1, 1, 0], FanHelperState::NotInstalled, 24),
        (Ok(()), 3, 2)
    );
    // A first registration waits for the user's approval instead.
    assert_eq!(
        run(&[1], FanHelperState::RequiresApproval, 24),
        (Ok(()), 1, 0)
    );
    // Still refused once the wait is over, or any other failure: report its code.
    assert_eq!(run(&[1], FanHelperState::NotInstalled, 3), (Err(1), 4, 4));
    assert_eq!(run(&[8], FanHelperState::Error, 24), (Err(8), 1, 0));
}
#[test]
fn helper_notices_an_update_swapping_its_bundle_but_not_a_missing_or_restored_file() {
    use std::{ffi::CString, os::unix::ffi::OsStrExt, path::Path};
    let helper = |app: &Path| app.join("Contents/Library/Helpers/opennavo-fan-helper");
    let bundle = |app: &Path, version: &str| {
        std::fs::create_dir_all(helper(app).parent().unwrap()).unwrap();
        std::fs::write(helper(app), version).unwrap();
    };
    let root = tempfile::tempdir().unwrap();
    let app = root.path().join("OpenNavo.app");
    let aside = root.path().join("current_app");
    let update = root.path().join("update");
    bundle(&app, "0.1.10");
    let running = Executable::at(helper(&app)).unwrap();
    assert!(!running.replaced());
    // Moved away (the updater's fallback, or by hand): there is nothing for launchd to start yet.
    std::fs::rename(&app, &aside).unwrap();
    assert!(!running.replaced());
    // Put back unchanged (the updater's rollback): still the same file.
    std::fs::rename(&aside, &app).unwrap();
    assert!(!running.replaced());
    // The updater swaps both bundles in one step, then deletes the old one.
    bundle(&update, "0.1.11");
    let from = CString::new(update.as_os_str().as_bytes()).unwrap();
    let to = CString::new(app.as_os_str().as_bytes()).unwrap();
    // SAFETY: both are valid NUL-terminated paths that outlive the call.
    let swapped =
        unsafe { nix::libc::renamex_np(from.as_ptr(), to.as_ptr(), nix::libc::RENAME_SWAP) };
    assert_eq!(swapped, 0);
    assert!(running.replaced());
    std::fs::remove_dir_all(&update).unwrap();
    assert!(running.replaced());
}
#[test]
fn reconnect_replays_each_safety_reason_once_and_helper_epoch_resets_cursor() {
    let events = Arc::new(Mutex::new(vec![]));
    let sink = events.clone();
    let mut client = Client::new(
        FakeTransport {
            control: control(),
            state: FanHelperState::Enabled,
            now: 0,
            down: false,
            exchanges: 0,
        },
        FanControlSupport::Unverified,
        Arc::new(move |c| sink.lock().unwrap().push(c.reason)),
    );
    client.status().unwrap();
    client.poll().unwrap();
    client.speed(None, 2000).unwrap();
    client.transport.control.watchdog(15000);
    client.transport.now = 15000;
    client.poll().unwrap();
    client.poll().unwrap();
    assert_eq!(
        events
            .lock()
            .unwrap()
            .iter()
            .filter(|r| **r == FanReason::HeartbeatLost)
            .count(),
        1
    );
    client.transport.control = Controller::new(
        FakeHardware::new(),
        FanControlSupport::Unverified,
        15000,
        456,
    );
    client.poll().unwrap();
    assert_eq!(
        events
            .lock()
            .unwrap()
            .iter()
            .filter(|r| **r == FanReason::HelperStarted)
            .count(),
        2
    );
}
#[test]
fn request_schema_has_only_five_commands_and_rejects_arbitrary_io() {
    for request in [
        r#"{"command":"write","key":"Ftst","value":1}"#,
        r#"{"command":"status","after":0,"epoch":null,"path":"/etc"}"#,
    ] {
        assert!(serde_json::from_str::<Request>(request).is_err());
    }
}
#[derive(Default)]
struct FakeSmc {
    values: BTreeMap<String, Value>,
    /// Every write attempt, refused or not.
    writes: Vec<(String, Vec<u8>)>,
    /// Older Apple firmware: fan mode writes are refused until Ftst is 1.
    needs_ftst: bool,
}
impl Smc for FakeSmc {
    fn read(&mut self, key: &str) -> Option<Value> {
        self.values.get(key).cloned()
    }
    fn write(&mut self, key: &str, bytes: &[u8]) -> Result<(), AppError> {
        self.writes.push((key.into(), bytes.to_vec()));
        let unlocked = self.read("Ftst").and_then(|v| v.number()) == Some(1.0);
        if self.needs_ftst && key.ends_with("Md") && !unlocked {
            return Err(AppError::new("E_FAN_WRITE_FAILED", "fake_locked"));
        }
        self.values.get_mut(key).unwrap().bytes = bytes.to_vec();
        Ok(())
    }
}
fn fake_smc() -> FakeSmc {
    let mut smc = FakeSmc::default();
    for (key, n) in [
        ("FNum", 1),
        ("F0Ac", 2000),
        ("F0Mn", 1200),
        ("F0Mx", 6000),
        ("F0Tg", 2000),
        ("F0Md", 0),
        ("FS! ", 0),
        ("Ftst", 0),
    ] {
        smc.values.insert(
            key.into(),
            Value {
                kind: *b"ui16",
                bytes: (n as u16).to_be_bytes().to_vec(),
            },
        );
    }
    smc.values.insert(
        "F0ID".into(),
        Value {
            kind: *b"{fds",
            bytes: [vec![0, 0, 0, 0], b"Left fan\0".to_vec()].concat(),
        },
    );
    smc
}
#[derive(Default)]
struct FakeDelay {
    waits: Vec<u64>,
}
impl Delay for FakeDelay {
    fn wait(&mut self, millis: u64) {
        self.waits.push(millis);
    }
}
#[test]
fn fan_ids_location_count_and_codecs_are_stable() {
    let mut smc = fake_smc();
    let fans = read_fans(&mut smc).unwrap();
    assert_eq!(fans[0].id, 0);
    assert_eq!(fans[0].name, "Left fan");
    smc.values.remove("F0ID");
    assert_eq!(read_fans(&mut smc).unwrap()[0].name, "Fan 1");
    smc.values.get_mut("FNum").unwrap().bytes = 0u16.to_be_bytes().to_vec();
    assert!(read_fans(&mut smc).is_none());
    for (kind, size) in [(*b"fpe2", 2), (*b"flt ", 4)] {
        let bytes = encoded(kind, size, 5000).unwrap();
        assert_eq!(Value { kind, bytes }.number(), Some(5000.0));
    }
    assert!(encoded(*b"fpe2", 2, 20000).is_err());
    assert!(
        Value {
            kind: *b"flt ",
            bytes: f32::NAN.to_le_bytes().to_vec()
        }
        .number()
        .is_none()
    );
}
#[test]
fn chip_strategies_only_write_their_allowed_keys_with_fake_delay() {
    let keys = |h: &SmcHardware<FakeSmc, FakeDelay>| {
        h.smc
            .writes
            .iter()
            .map(|(k, _)| k.clone())
            .collect::<Vec<_>>()
    };
    let run = |smc: FakeSmc, family: Family| {
        let mut h = SmcHardware::new(smc, family, FakeDelay::default());
        h.set_mode(0, FanMode::Manual).unwrap();
        h.set_speed(0, 3000).unwrap();
        assert_eq!(h.fans().unwrap()[0].mode, FanMode::Manual);
        h.reset().unwrap();
        assert_eq!(h.fans().unwrap()[0].mode, FanMode::Auto);
        h
    };
    // Intel: per-fan mode plus the FS! bitmask; no retries or waits.
    let mut smc = fake_smc();
    smc.values.remove("Ftst");
    let h = run(smc, Family::Intel);
    assert_eq!(keys(&h), ["F0Md", "FS! ", "F0Tg", "F0Md", "FS! "]);
    assert!(h.delay.waits.is_empty());
    // Apple firmware without Ftst (M5, M1 Max on macOS 27) takes the mode key directly; automatic also clears the
    // manual target.
    let mut smc = fake_smc();
    smc.values.remove("Ftst");
    smc.values.remove("FS! ");
    let h = run(smc, Family::Apple);
    assert_eq!(keys(&h), ["F0Md", "F0Tg", "F0Md", "F0Tg"]);
    assert_eq!(
        h.smc.values["F0Tg"].number(),
        Some(0.0),
        "automatic clears the manual target"
    );
    assert!(h.delay.waits.is_empty());
    // Older Apple firmware refuses the mode until Ftst unlocks it, then needs three seconds; reset locks it again.
    let mut smc = fake_smc();
    smc.values.remove("FS! ");
    smc.needs_ftst = true;
    let h = run(smc, Family::Apple);
    assert_eq!(
        keys(&h),
        ["F0Md", "Ftst", "F0Md", "F0Tg", "F0Md", "F0Tg", "Ftst"]
    );
    assert_eq!(h.delay.waits, [3000]);
    assert_eq!(h.smc.values["Ftst"].number(), Some(0.0));
}
#[test]
fn apple_without_ftst_or_mode_keys_fails_without_touching_other_keys() {
    let mut smc = fake_smc();
    smc.values.remove("Ftst");
    smc.values.remove("FS! ");
    smc.needs_ftst = true;
    let mut h = SmcHardware::new(smc, Family::Apple, FakeDelay::default());
    assert_eq!(
        h.set_mode(0, FanMode::Manual).unwrap_err().code,
        "E_FAN_WRITE_FAILED"
    );
    assert_eq!(h.smc.writes.len(), 1, "one direct attempt, no Ftst write");
    let mut smc = fake_smc();
    smc.values.remove("F0Md");
    let mut h = SmcHardware::new(smc, Family::Apple, FakeDelay::default());
    assert!(h.set_mode(0, FanMode::Manual).is_err());
    assert!(h.reset().is_err());
    assert!(h.smc.writes.is_empty());
}
#[test]
fn ftst_stays_unlocked_while_another_fan_is_manual() {
    let mut smc = fake_smc();
    smc.values.remove("FS! ");
    smc.needs_ftst = true;
    for (key, n) in [
        ("FNum", 2),
        ("F1Ac", 2000),
        ("F1Mn", 1200),
        ("F1Mx", 6000),
        ("F1Tg", 2000),
        ("F1Md", 0),
    ] {
        smc.values.insert(
            key.into(),
            Value {
                kind: *b"ui16",
                bytes: (n as u16).to_be_bytes().to_vec(),
            },
        );
    }
    let mut h = SmcHardware::new(smc, Family::Apple, FakeDelay::default());
    h.set_mode(0, FanMode::Manual).unwrap();
    h.set_mode(1, FanMode::Manual).unwrap();
    assert_eq!(h.delay.waits, [3000], "one unlock covers every fan");
    h.set_mode(0, FanMode::Auto).unwrap();
    assert_eq!(h.smc.values["Ftst"].number(), Some(1.0));
    assert_eq!(h.fans().unwrap()[1].mode, FanMode::Manual);
    h.set_mode(1, FanMode::Auto).unwrap();
    assert_eq!(h.smc.values["Ftst"].number(), Some(0.0));
}
#[test]
fn startup_reset_does_not_write_when_every_fan_is_already_automatic() {
    let mut smc = fake_smc();
    smc.values.remove("Ftst");
    smc.values.remove("FS! ");
    let mut h = SmcHardware::new(smc, Family::Apple, FakeDelay::default());
    h.reset().unwrap();
    assert!(h.smc.writes.is_empty());
}
#[test]
fn known_chips_are_allowed_and_unknown_never_unlocks() {
    for chip in ["Intel Core i7", "Apple M1 Max", "Apple M4", "Apple M5 Pro"] {
        let support = Family::from_chip(chip).support(1);
        assert_eq!(support, FanControlSupport::Unverified);
        assert!(opennavo_desktop_lib::monitor::manual_allowed(support));
    }
    for (chip, fans) in [("Apple Fixture", 1), ("Apple M1", 0)] {
        let support = Family::from_chip(chip).support(fans);
        assert_eq!(support, FanControlSupport::Unsupported);
        assert!(!opennavo_desktop_lib::monitor::manual_allowed(support));
    }
}
#[test]
fn sensors_aggregate_max_stable_keys_localization_vocabulary_and_invalid_values() {
    let rows = aggregate([
        (String::from("pACC 0"), 50.0),
        ("pACC 1".into(), 70.0),
        ("eACC".into(), 40.0),
        ("GPU".into(), 60.0),
        ("SoC die".into(), 55.0),
        ("SSD".into(), 45.0),
        ("Battery".into(), 35.0),
        ("CPU".into(), 65.0),
        ("CPU broken".into(), f64::NAN),
        ("SSD broken".into(), 999.0),
    ])
    .unwrap();
    assert_eq!(rows.len(), 7);
    assert_eq!(
        f64::from(
            rows.iter()
                .find(|s| s.name == "CPU performance cores")
                .unwrap()
                .celsius
        ),
        70.0
    );
    assert_eq!(
        rows.iter()
            .find(|s| s.group == SensorGroup::Battery)
            .unwrap()
            .key,
        "battery"
    );
    assert!(aggregate([("CPU".into(), f64::NAN)]).is_none());
}
#[test]
fn helper_sleep_can_precede_app_callback_without_losing_wake_targets() {
    let events = Arc::new(Mutex::new(vec![]));
    let sink = events.clone();
    let mut client = Client::new(
        FakeTransport {
            control: control(),
            state: FanHelperState::Enabled,
            now: 0,
            down: false,
            exchanges: 0,
        },
        FanControlSupport::Unverified,
        Arc::new(move |c| sink.lock().unwrap().push(c.reason)),
    );
    client.speed(Some(0), 3000).unwrap();
    client.transport.control.recover(FanReason::Sleep); // helper wins notification race
    client.sleep();
    client.wake();
    assert_eq!(
        client.transport.control.hardware.fans[0].mode,
        FanMode::Manual
    );
    assert_eq!(
        client.transport.control.hardware.fans[0].target_rpm,
        Some(3000)
    );
    assert!(events.lock().unwrap().contains(&FanReason::WakeReapplied));
    client.sleep();
    client.transport.control.recover(FanReason::Overheat);
    client.wake();
    assert!(
        client
            .transport
            .control
            .hardware
            .fans
            .iter()
            .all(|f| f.mode == FanMode::Auto)
    );
}
#[tokio::test]
async fn manual_speed_settings_merge_preserves_unrelated_choices_and_failed_save() {
    use opennavo_desktop_lib::core::DesktopCore;
    let root = tempfile::tempdir().unwrap();
    let core = DesktopCore::open(
        root.path().join("data"),
        root.path().join("cache"),
        root.path().join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    let before = core.current_settings().unwrap();
    core.save_fan_manual([(0, 2000)].into()).await.unwrap();
    core.save_fan_manual([(1, 3000)].into()).await.unwrap();
    let mut after = core.current_settings().unwrap();
    assert_eq!(after.fan_last_manual, Some([(0, 2000), (1, 3000)].into()));
    after.fan_last_manual = before.fan_last_manual.clone();
    assert_eq!(after, before);
    core.install_settings_save(Arc::new(|_| {
        Err(AppError::new("E_UNKNOWN", "fake_save_failure"))
    }))
    .unwrap();
    assert!(core.save_fan_manual([(0, 4000)].into()).await.is_err());
    assert_eq!(
        core.current_settings().unwrap().fan_last_manual,
        Some([(0, 2000), (1, 3000)].into())
    );
}
#[test]
fn automatic_restore_uses_cached_encoding_even_when_smc_reads_fail() {
    struct Toggle {
        inner: FakeSmc,
        fail: bool,
    }
    impl Smc for Toggle {
        fn read(&mut self, key: &str) -> Option<Value> {
            if self.fail {
                None
            } else {
                self.inner.read(key)
            }
        }
        fn write(&mut self, key: &str, bytes: &[u8]) -> Result<(), AppError> {
            self.inner.write(key, bytes)
        }
    }
    let mut h = SmcHardware::new(
        Toggle {
            inner: fake_smc(),
            fail: false,
        },
        Family::Intel,
        FakeDelay::default(),
    );
    h.set_mode(0, FanMode::Manual).unwrap();
    h.smc.fail = true;
    h.reset().unwrap();
    assert_eq!(
        h.smc.inner.writes.last().unwrap(),
        &("FS! ".into(), 0u16.to_be_bytes().to_vec())
    );
}
#[test]
fn bounded_journal_preserves_safety_categories_across_many_user_changes() {
    let mut c = control();
    c.request(speed(2000), 0, true).unwrap();
    c.watchdog(15000);
    for now in 15001..15100 {
        c.request(speed(2000), now, true).unwrap();
    }
    let reply = status(&mut c, 15100);
    assert!(reply.changes.len() <= 64);
    assert!(
        reply
            .changes
            .iter()
            .any(|e| e.reason == FanReason::HelperStarted)
    );
    assert!(
        reply
            .changes
            .iter()
            .any(|e| e.reason == FanReason::HeartbeatLost)
    );
    c.recover(FanReason::Sleep);
    let sequence = status(&mut c, 15101).sequence;
    c.recover(FanReason::Sleep);
    assert_eq!(status(&mut c, 15102).sequence, sequence);
}

#[test]
fn shared_cpu_zones_drive_helper_overheat_and_soc_only_never_authorizes_manual() {
    use opennavo_desktop_lib::monitor::sensors::safety_temperature;
    let zones = aggregate([
        ("pACC MTR Temp Sensor0".into(), 98.0),
        ("PMU tdie0".into(), 57.6),
    ])
    .unwrap();
    let mut c = control();
    c.request(speed(2000), 0, true).unwrap();
    c.hardware.temperature = safety_temperature(&zones);
    c.watchdog(5000);
    status(&mut c, 5000);
    c.watchdog(10000);
    status(&mut c, 10000);
    c.watchdog(15000);
    assert!(
        status(&mut c, 15000)
            .changes
            .iter()
            .any(|event| event.reason == FanReason::Overheat)
    );
    assert!(c.hardware.fans.iter().all(|f| f.target_rpm == Some(6000)));
    let mut c = control();
    let zones = aggregate([("PMU tdie0".into(), 57.6)]).unwrap();
    c.hardware.temperature = safety_temperature(&zones);
    assert_eq!(
        c.request(speed(2000), 0, true).unwrap_err().code,
        "E_FAN_SENSOR_UNAVAILABLE"
    );
    assert!(c.hardware.writes.is_empty());
}
