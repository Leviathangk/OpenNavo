//! Protocol fixtures never invoke CommerceKit or the real store helper.
use opennavo_desktop_lib::appstore::{install::Event, progress};
use serde_json::Value;
use std::process::{Command, Stdio};

fn fake(scenario: &str) -> Command {
    let mut command = Command::new("python3");
    command
        .arg(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/tests/fake-store/store"
        ))
        .args([
            "install",
            "--adam-ids",
            "42,43",
            "--staging",
            "/fixture-only",
        ])
        .env("OPENNAVO_FAKE_STORE_SCENARIO", scenario);
    command
}

#[test]
fn privileged_install_is_a_request_not_a_done_event() {
    let output = fake("201").output().unwrap();
    assert!(output.status.success());
    let events: Vec<Value> = String::from_utf8(output.stdout)
        .unwrap()
        .lines()
        .map(|line| serde_json::from_str(line).unwrap())
        .collect();
    assert_eq!(
        events
            .iter()
            .filter(|e| e["event"] == "needsPrivilegedInstall")
            .count(),
        2
    );
    assert!(!events.iter().any(|e| e["phase"] == "done"));
    assert!(
        events
            .iter()
            .all(|e| e["adamId"] == 42 || e["adamId"] == 43)
    );
}

#[test]
fn closed_stdin_cancels_and_engine_error_is_nonzero() {
    let output = fake("cancel").stdin(Stdio::null()).output().unwrap();
    assert!(!output.status.success());
    let output = fake("error").output().unwrap();
    assert!(!output.status.success());
    let text = String::from_utf8(output.stdout).unwrap();
    let event: Value = serde_json::from_str(text.lines().last().unwrap()).unwrap();
    assert_eq!(event["code"], "E_MAS_ENGINE");
    assert_eq!(event["errorCode"], 201);
}

#[test]
fn phase_events_accept_json_number_forms_and_unknown_progress() {
    // Ensure this test is exercising the project's actual arbitrary_precision feature set.
    let maximum = u128::MAX.to_string();
    let value: Value = serde_json::from_slice(maximum.as_bytes()).unwrap();
    assert_eq!(value.to_string(), maximum);
    for (raw, expected) in [
        (Some("50"), Some(50.0)),
        (Some("50.0"), Some(50.0)),
        (Some("37.5"), Some(37.5)),
        (Some("3.75e1"), Some(37.5)),
        (Some("null"), None),
        (None, None),
        (Some("0"), Some(0.0)),
        (Some("1e2"), Some(100.0)),
    ] {
        for phase in [
            "purchasing",
            "downloading",
            "downloaded",
            "installing",
            "done",
        ] {
            let field = raw
                .map(|raw| format!(",\"percent\":{raw}"))
                .unwrap_or_default();
            // The tag comes last, as JSON object key order is not part of the protocol.
            let json = format!(r#"{{"adamId":42,"phase":"{phase}"{field},"event":"phase"}}"#);
            let Event::Phase {
                adam_id,
                phase: decoded_phase,
                percent,
            } = serde_json::from_slice(json.as_bytes()).unwrap()
            else {
                panic!("wrong event")
            };
            assert_eq!(adam_id, 42);
            assert_eq!(decoded_phase, phase);
            assert_eq!(percent, expected, "raw={raw:?}");
            assert!(progress::stage(phase, percent).is_ok());
        }
    }
}

#[test]
fn phase_events_reject_invalid_nonfinite_and_out_of_range_progress() {
    for raw in [
        "-1",
        "100.1",
        "100.000001",
        "1e39",
        "1e999",
        "-1e999",
        "NaN",
        "Infinity",
        "\"NaN\"",
        "\"37.5\"",
        "true",
        "[]",
        "{}",
    ] {
        for phase in [
            "purchasing",
            "downloading",
            "downloaded",
            "installing",
            "done",
        ] {
            let json =
                format!(r#"{{"event":"phase","adamId":42,"phase":"{phase}","percent":{raw}}}"#);
            assert!(
                serde_json::from_slice::<Event>(json.as_bytes()).is_err(),
                "accepted {raw} for {phase}"
            );
        }
    }
    // Non-JSON callers retain the existing stage guard as well.
    for percent in [f32::NAN, f32::INFINITY, f32::NEG_INFINITY, -1.0, 100.1] {
        assert_eq!(
            progress::stage("downloading", Some(percent))
                .unwrap_err()
                .message,
            "invalid_store_percent"
        );
    }
}

#[test]
fn non_phase_events_keep_their_payloads_and_required_fields() {
    let event: Event = serde_json::from_slice(br#"{"event":"needsPrivilegedInstall","adamId":42,"pkg":"/fixture/pkg","receipt":"/fixture/receipt","appPath":"/fixture/App.app"}"#).unwrap();
    let Event::NeedsPrivilegedInstall {
        adam_id,
        pkg,
        receipt,
        app_path,
    } = event
    else {
        panic!("wrong event")
    };
    assert_eq!(adam_id, 42);
    assert_eq!(pkg.to_str(), Some("/fixture/pkg"));
    assert_eq!(receipt.to_str(), Some("/fixture/receipt"));
    assert_eq!(app_path.to_str(), Some("/fixture/App.app"));
    let event: Event = serde_json::from_slice(br#"{"event":"error","adamId":42,"code":"E_MAS_ENGINE","domain":"Store","errorCode":201,"message":"failed"}"#).unwrap();
    let Event::Error {
        adam_id,
        code,
        domain,
        error_code,
        message,
    } = event
    else {
        panic!("wrong event")
    };
    assert_eq!(adam_id, Some(42));
    assert_eq!(code, "E_MAS_ENGINE");
    assert_eq!(domain.as_deref(), Some("Store"));
    assert_eq!(error_code, Some(201));
    assert_eq!(message.as_deref(), Some("failed"));
    assert!(matches!(
        serde_json::from_slice::<Event>(br#"{"event":"error","code":"E_MAS_ENGINE"}"#).unwrap(),
        Event::Error {
            adam_id: None,
            domain: None,
            error_code: None,
            message: None,
            ..
        }
    ));
    for bad in [
        br#"{"event":"unknown"}"#.as_slice(),
        br#"{"adamId":42,"phase":"downloading"}"#,
        br#"{"event":"phase","phase":"downloading"}"#,
        br#"{"event":"needsPrivilegedInstall","adamId":42}"#,
        br#"{"event":"error"}"#,
    ] {
        assert!(serde_json::from_slice::<Event>(bad).is_err());
    }
}
