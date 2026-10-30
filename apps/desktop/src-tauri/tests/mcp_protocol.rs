//! Integration tests for the `mcp` subcommand and the B1 stdio protocol.
//!
//! Argv classification and protocol edges are checked against the public
//! library surface and in-memory streams. The stdout-isolation test also spawns
//! the real application binary with `mcp`, because that is the only way to
//! prove diagnostics cannot leak into the protocol stream.

use std::ffi::OsString;
use std::io::{Read, Write};
use std::process::{Child, Command, ExitStatus, Stdio};
use std::time::{Duration, Instant};

use opennavo_desktop_lib::mcp::cli::{self, Mode};
use opennavo_desktop_lib::mcp::protocol::{
    self, Availability, LATEST_PROTOCOL_VERSION, MAX_LINE_BYTES, SERVER_NAME,
    SUPPORTED_PROTOCOL_VERSIONS, Session,
};
use opennavo_desktop_lib::mcp::tools;
use serde_json::{Value, json};

const INITIALIZE: &str = concat!(
    r#"{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"test","version":"1"}}}"#,
    "\n"
);

fn argv(values: &[&str]) -> Vec<OsString> {
    values.iter().map(OsString::from).collect()
}

fn run(input: &str, availability: Availability) -> (Vec<Value>, Session) {
    let mut output = Vec::new();
    let mut session = Session::default();
    protocol::serve_with_session(input.as_bytes(), &mut output, availability, &mut session)
        .expect("serve should finish cleanly");
    let responses = String::from_utf8(output)
        .expect("protocol output is UTF-8")
        .lines()
        .map(|line| serde_json::from_str(line).expect("each stdout line is JSON"))
        .collect();
    (responses, session)
}

fn wait_with_timeout(child: &mut Child, timeout: Duration) -> ExitStatus {
    let deadline = Instant::now() + timeout;
    loop {
        if let Some(status) = child.try_wait().expect("poll mcp process") {
            return status;
        }
        if Instant::now() >= deadline {
            let _ = child.kill();
            let _ = child.wait();
            panic!("mcp process did not exit within {timeout:?}");
        }
        std::thread::sleep(Duration::from_millis(10));
    }
}

#[test]
fn mcp_subcommand_is_exact_and_does_not_capture_deep_links() {
    assert_eq!(
        cli::classify(&argv(&["opennavo-desktop", "mcp"])),
        Mode::Mcp
    );
    assert_eq!(
        cli::classify(&argv(&["opennavo-desktop", "mcp", "extra"])),
        Mode::InvalidMcp
    );

    for invocation in [
        &["opennavo-desktop"][..],
        &["opennavo-desktop", "--hidden"],
        &["opennavo-desktop", "opennavo://install?x=1"],
        &["opennavo-desktop", "--hidden", "mcp"],
        &["opennavo-desktop", "mcpfoo"],
    ] {
        assert_eq!(
            cli::classify(&argv(invocation)),
            Mode::Gui,
            "{invocation:?} must fall through to Tauri"
        );
    }
    assert_eq!(cli::classify(&[]), Mode::Gui);
}

#[test]
fn initialize_returns_tools_capability_and_server_info() {
    let (responses, _) = run(INITIALIZE, Availability::Enabled);
    assert_eq!(responses.len(), 1);
    let response = &responses[0];
    assert_eq!(response["jsonrpc"], "2.0");
    assert_eq!(response["id"], 1);
    let result = &response["result"];
    assert_eq!(result["protocolVersion"], "2025-06-18");
    assert_eq!(result["capabilities"], json!({ "tools": {} }));
    assert_eq!(result["serverInfo"]["name"], SERVER_NAME);
    assert!(result["serverInfo"]["version"].is_string());
    assert_eq!(tools::list_result()["tools"], json!([]));
}

#[test]
fn initialize_negotiates_protocol_version() {
    assert_eq!(
        LATEST_PROTOCOL_VERSION, SUPPORTED_PROTOCOL_VERSIONS[0],
        "latest must be the first supported version"
    );
    for version in SUPPORTED_PROTOCOL_VERSIONS {
        let input = format!(
            "{{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{{\"protocolVersion\":\"{version}\"}}}}\n"
        );
        let (responses, _) = run(&input, Availability::Enabled);
        assert_eq!(responses.len(), 1, "version {version}");
        assert_eq!(
            responses[0]["result"]["protocolVersion"], *version,
            "supported version {version} must be echoed"
        );
    }
}

#[test]
fn initialize_falls_back_to_latest_for_unusable_protocol_version() {
    for params in [
        "{}",
        r#"{"protocolVersion":20250618}"#,
        r#"{"protocolVersion":null}"#,
        r#"{"protocolVersion":["2025-06-18"]}"#,
        r#"{"protocolVersion":"2099-01-01"}"#,
        r#"{"protocolVersion":""}"#,
        r#"{"protocolVersion":"2025-06-18 "}"#,
    ] {
        let input = format!(
            "{{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{params}}}\n"
        );
        let (responses, _) = run(&input, Availability::Enabled);
        assert_eq!(responses.len(), 1, "params {params}");
        assert_eq!(
            responses[0]["result"]["protocolVersion"], LATEST_PROTOCOL_VERSION,
            "params {params} must fall back to latest"
        );
    }

    let without_params = "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n";
    let (responses, _) = run(without_params, Availability::Enabled);
    assert_eq!(responses.len(), 1);
    assert_eq!(
        responses[0]["result"]["protocolVersion"],
        LATEST_PROTOCOL_VERSION
    );
}

#[test]
fn initialize_captures_and_sanitizes_client_name() {
    let hostile = format!("\u{0007}\u{202E}\u{2066}{}", "a".repeat(80));
    let input = format!(
        "{{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{{\"clientInfo\":{{\"name\":{}}}}}}}\n",
        serde_json::to_string(&hostile).expect("encode client name")
    );
    let (responses, session) = run(&input, Availability::Enabled);
    assert_eq!(responses.len(), 1);
    let captured = session
        .client_name()
        .expect("client name should be captured");
    assert!(!captured.chars().any(char::is_control));
    assert!(!captured.contains('\u{202E}'));
    assert!(!captured.contains('\u{2066}'));
    assert_eq!(captured.chars().count(), 64);
    assert_eq!(captured, "a".repeat(64));

    // Truncation counts scalar values, not bytes, and drops bidi controls.
    let multibyte = format!("\u{061C}{}", "é".repeat(70));
    assert_eq!(protocol::sanitize_client_name(&multibyte), "é".repeat(64));
}

#[test]
fn ping_and_unknown_method() {
    let input = concat!(
        r#"{"jsonrpc":"2.0","id":2,"method":"ping"}"#,
        "\n",
        r#"{"jsonrpc":"2.0","id":3,"method":"no/such/method"}"#,
        "\n",
    );
    let (responses, _) = run(input, Availability::SwitchOff);
    assert_eq!(responses.len(), 2);
    assert_eq!(responses[0]["id"], 2);
    assert_eq!(responses[0]["result"], json!({}));
    assert_eq!(responses[1]["id"], 3);
    assert_eq!(responses[1]["error"]["code"], -32601);
    assert_eq!(responses[1]["error"]["message"], "Method not found");
}

#[test]
fn malformed_json_and_oversized_line_do_not_panic() {
    let oversized = format!("{}\n", "x".repeat(MAX_LINE_BYTES + 1));
    let input = format!(
        "{{not json}}\n{}\n{oversized}",
        r#"{"jsonrpc":"2.0","id":2,"method":"ping"}"#
    );
    let (responses, _) = run(&input, Availability::Enabled);
    assert_eq!(responses.len(), 2);
    assert_eq!(responses[0]["error"]["code"], -32700);
    assert_eq!(responses[1]["result"], json!({}));
}

#[test]
fn stdout_contains_only_protocol_json() {
    let input = format!(
        "{INITIALIZE}{}\n{}\n",
        r#"{"jsonrpc":"2.0","id":2,"method":"ping"}"#,
        r#"{"jsonrpc":"2.0","id":3,"method":"tools/list"}"#,
    );

    // Cargo sets CARGO_BIN_EXE_<name> for integration tests and builds the
    // package's binary targets first. `opennavo-desktop` is the autobin from
    // src/main.rs; the fan-helper [[bin]] is skipped unless its feature is on.
    let mut child = Command::new(env!("CARGO_BIN_EXE_opennavo-desktop"))
        .arg("mcp")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("spawn the app binary in mcp mode");

    let mut stdin = child.stdin.take().expect("child stdin is piped");
    stdin
        .write_all(input.as_bytes())
        .expect("write requests to child stdin");
    stdin.flush().expect("flush child stdin");
    drop(stdin);

    let status = wait_with_timeout(&mut child, Duration::from_secs(20));

    let mut stdout = String::new();
    child
        .stdout
        .take()
        .expect("child stdout is piped")
        .read_to_string(&mut stdout)
        .expect("read child stdout");
    let mut stderr = String::new();
    child
        .stderr
        .take()
        .expect("child stderr is piped")
        .read_to_string(&mut stderr)
        .expect("read child stderr");

    assert!(
        status.success(),
        "mcp mode must exit 0, got {status}; stderr: {stderr}"
    );
    assert!(
        stdout.ends_with('\n'),
        "stdout must be only newline-terminated protocol messages: {stdout:?}"
    );
    let lines: Vec<&str> = stdout.lines().collect();
    assert_eq!(
        lines.len(),
        3,
        "initialize, ping and tools/list each produce one response; the notification produces none: {stdout:?}"
    );
    assert!(
        !stdout.contains("mcp:"),
        "diagnostics must go to stderr, never stdout: {stdout:?}"
    );
    for (index, line) in lines.iter().enumerate() {
        let message: Value = serde_json::from_str(line).unwrap_or_else(|error| {
            panic!(
                "stdout line {} is not protocol JSON ({error}): {line:?}",
                index + 1
            )
        });
        assert_eq!(
            message["jsonrpc"],
            "2.0",
            "stdout line {}: {line}",
            index + 1
        );
        assert_eq!(
            message["id"],
            json!(index + 1),
            "stdout line {}: {line}",
            index + 1
        );
        assert!(
            message.get("result").is_some() ^ message.get("error").is_some(),
            "each response has exactly one of result/error: {line}"
        );
    }
}

#[test]
fn no_new_binary_target_is_added() {
    let manifest = include_str!("../Cargo.toml");
    let bins: Vec<&str> = manifest
        .split("[[bin]]")
        .skip(1)
        .map(|section| {
            section
                .split("\n[")
                .next()
                .expect("split always yields a section")
        })
        .collect();

    assert_eq!(
        bins.len(),
        1,
        "PR-B1 must not add a second [[bin]]; mcp is a mode of the default binary"
    );
    let bin_names: Vec<&str> = bins
        .iter()
        .filter_map(|section| {
            section.lines().find_map(|line| {
                let line = line.trim();
                line.strip_prefix("name = \"")
                    .and_then(|name| name.strip_suffix('"'))
            })
        })
        .collect();
    assert_eq!(
        bin_names,
        ["opennavo-fan-helper"],
        "the app binary comes from src/main.rs, not a [[bin]] target"
    );
    let fan_helper = bins[0];
    assert!(fan_helper.contains("path = \"fan-helper/main.rs\""));
    assert!(fan_helper.contains("required-features = [\"fan-helper\"]"));
}
