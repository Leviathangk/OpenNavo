//! Integration tests for the `mcp` subcommand and the B1 stdio protocol.
//!
//! These tests exercise the public library surface only. They deliberately do
//! not start Tauri or spawn the application binary: argv classification is
//! checked directly and the protocol is driven over in-memory streams.

use std::ffi::OsString;
use std::io::Write;

use opennavo_desktop_lib::mcp::cli::{self, Mode};
use opennavo_desktop_lib::mcp::protocol::{
    self, Availability, MAX_LINE_BYTES, SERVER_NAME, Session,
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
    let input = concat!(
        r#"{"jsonrpc":"2.0","id":1,"method":"ping"}"#,
        "\n",
        r#"{"jsonrpc":"2.0","method":"notifications/initialized"}"#,
        "\n",
        "{not json}\n",
        r#"{"jsonrpc":"2.0","id":2,"method":"tools/list"}"#,
        "\n",
        r#"{"jsonrpc":"2.0","id":3,"method":"no/such/method"}"#,
        "\n",
    );
    let mut output = Vec::new();
    protocol::serve_with_availability(input.as_bytes(), &mut output, Availability::Enabled)
        .expect("serve should finish cleanly");
    output.flush().expect("flush in-memory stdout");

    let text = std::str::from_utf8(&output).expect("stdout is UTF-8");
    assert!(
        text.ends_with('\n'),
        "every protocol message is newline terminated"
    );
    let lines: Vec<&str> = text.lines().collect();
    assert_eq!(
        lines.len(),
        4,
        "notification and oversized-free input produce 4 responses"
    );
    for line in lines {
        let message: Value = serde_json::from_str(line).expect("stdout line must be protocol JSON");
        assert_eq!(message["jsonrpc"], "2.0");
        assert!(message.get("id").is_some());
        assert!(
            message.get("result").is_some() ^ message.get("error").is_some(),
            "each response has exactly one of result/error: {line}"
        );
        assert!(!line.contains("mcp:"), "diagnostics must not reach stdout");
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
