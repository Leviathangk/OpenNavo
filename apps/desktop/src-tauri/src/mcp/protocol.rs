//! Hand-written, newline-delimited JSON-RPC 2.0 transport for MCP stdio mode.
//!
//! B1 implements only the small surface needed by an MCP client: `initialize`,
//! `notifications/initialized`, `ping`, `tools/list` and `tools/call`. The
//! server is generic over `BufRead`/`Write` so tests can drive it in memory.

use std::io::{self, BufRead, Write};

use serde_json::{Map, Value, json};

use super::tools;

/// Maximum accepted input line length, excluding the trailing newline.
pub const MAX_LINE_BYTES: usize = 1024 * 1024;

/// MCP protocol versions this server can speak, newest first.
pub const SUPPORTED_PROTOCOL_VERSIONS: &[&str] = &["2025-06-18", "2025-03-26", "2024-11-05"];

/// Newest supported protocol version, used whenever the client's request
/// cannot be honored (missing, malformed, or unsupported).
pub const LATEST_PROTOCOL_VERSION: &str = SUPPORTED_PROTOCOL_VERSIONS[0];

/// Name advertised in the `initialize` response.
pub const SERVER_NAME: &str = "opennavo";

/// Whether local MCP may serve tool calls.
///
/// B1 only stubs this decision. B2/B5 replace it with the persisted master
/// switch and app/socket liveness checks.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Availability {
    /// The app is running and the Local MCP switch is on.
    Enabled,
    /// The Local MCP switch is off (B1 default).
    SwitchOff,
    /// The switch is on but the app-side broker is not reachable.
    AppNotRunning,
}

impl Availability {
    /// Whether tool calls may be served.
    pub fn is_enabled(self) -> bool {
        matches!(self, Self::Enabled)
    }

    /// Human-readable diagnostic written once when the server starts.
    pub fn startup_message(self) -> &'static str {
        match self {
            Self::Enabled => "local MCP is enabled",
            Self::SwitchOff => "local MCP is off; tools/call will return E_MCP_DISABLED",
            Self::AppNotRunning => {
                "OpenNavo is not running; tools/call will return E_APP_NOT_RUNNING"
            }
        }
    }

    /// The JSON-RPC error used by `tools/call` when tools are unavailable.
    fn disabled_error(self) -> Option<(i64, &'static str, &'static str)> {
        match self {
            Self::Enabled => None,
            Self::SwitchOff => Some((
                -32001,
                "E_MCP_DISABLED",
                "Local MCP is turned off in OpenNavo Settings. Ask the user to enable it.",
            )),
            Self::AppNotRunning => Some((
                -32002,
                "E_APP_NOT_RUNNING",
                "OpenNavo is not running. Ask the user to open OpenNavo, then retry.",
            )),
        }
    }
}

/// Resolve the current Local MCP availability.
///
/// B1 always reports `SwitchOff`; the real switch and socket checks arrive in
/// B2/B5. Tests inject the other variants through `serve_with_session`.
pub const fn local_mcp_availability() -> Availability {
    Availability::SwitchOff
}

/// Per-process MCP session state.
#[derive(Debug, Default)]
pub struct Session {
    client_name: Option<String>,
}

impl Session {
    /// Sanitized `params.clientInfo.name` captured by `initialize`.
    pub fn client_name(&self) -> Option<&str> {
        self.client_name.as_deref()
    }
}

/// Strip control and bidirectional formatting characters, then truncate to 64
/// Unicode scalar values. The client-supplied name is never echoed.
pub fn sanitize_client_name(name: &str) -> String {
    name.chars()
        .filter(|character| !character.is_control() && !is_bidi_control(*character))
        .take(64)
        .collect()
}

fn is_bidi_control(character: char) -> bool {
    matches!(
        character,
        '\u{061C}' | '\u{200E}' | '\u{200F}' | '\u{202A}'..='\u{202E}' | '\u{2066}'..='\u{2069}'
    )
}

/// Serve MCP over the given streams using the B1 default availability.
pub fn serve<R: BufRead, W: Write>(input: R, output: W) -> io::Result<()> {
    serve_with_availability(input, output, local_mcp_availability())
}

/// Serve MCP with an explicitly injected availability decision.
pub fn serve_with_availability<R: BufRead, W: Write>(
    input: R,
    output: W,
    availability: Availability,
) -> io::Result<()> {
    eprintln!("mcp: {}", availability.startup_message());
    let mut session = Session::default();
    serve_with_session(input, output, availability, &mut session)
}

/// Serve MCP while exposing the session for tests.
pub fn serve_with_session<R: BufRead, W: Write>(
    mut input: R,
    mut output: W,
    availability: Availability,
    session: &mut Session,
) -> io::Result<()> {
    let mut line = Vec::new();
    loop {
        line.clear();
        // Take enough bytes to include the newline after a maximum-length
        // line, so an oversized line is rejected without buffering it all.
        let mut limited = std::io::Read::take(&mut input, (MAX_LINE_BYTES + 2) as u64);
        let read = limited.read_until(b'\n', &mut line)?;
        if read == 0 {
            return output.flush();
        }

        let had_newline = line.last() == Some(&b'\n');
        if had_newline {
            line.pop();
        }
        if had_newline && line.last() == Some(&b'\r') {
            line.pop();
        }
        if line.len() > MAX_LINE_BYTES {
            eprintln!("mcp: input line exceeds {MAX_LINE_BYTES} bytes; disconnecting");
            return Ok(());
        }

        handle_line(&line, &mut output, session, availability)?;
    }
}

fn handle_line<W: Write>(
    line: &[u8],
    output: &mut W,
    session: &mut Session,
    availability: Availability,
) -> io::Result<()> {
    let text = match std::str::from_utf8(line) {
        Ok(text) => text,
        Err(_) => return write_parse_error(output),
    };
    let message: Value = match serde_json::from_str(text) {
        Ok(message) => message,
        Err(_) => return write_parse_error(output),
    };
    let Some(object) = message.as_object() else {
        return write_error(output, Value::Null, -32600, "Invalid Request", None);
    };

    let id = object.get("id").cloned();
    let Some(method) = object.get("method").and_then(Value::as_str) else {
        return match id {
            Some(id) => write_error(output, id, -32600, "Invalid Request", None),
            None => Ok(()),
        };
    };

    // JSON-RPC notifications never receive a response. This covers
    // `notifications/initialized` as well as any other notification.
    let Some(id) = id else {
        return Ok(());
    };

    match method {
        "initialize" => {
            capture_client_name(object.get("params"), session);
            let protocol_version = negotiate_protocol_version(object.get("params"));
            let result = json!({
                "protocolVersion": protocol_version,
                "capabilities": { "tools": {} },
                "serverInfo": {
                    "name": SERVER_NAME,
                    "version": env!("CARGO_PKG_VERSION"),
                },
            });
            write_result(output, id, result)
        }
        "ping" => write_result(output, id, json!({})),
        "tools/list" => write_result(output, id, tools::list_result()),
        "tools/call" => write_tools_call(output, id, availability),
        _ => write_error(output, id, -32601, "Method not found", None),
    }
}

/// Echo a supported requested version, or fall back to the latest one when the
/// request is missing, malformed, or not in `SUPPORTED_PROTOCOL_VERSIONS`.
fn negotiate_protocol_version(params: Option<&Value>) -> &'static str {
    let Some(requested) = params
        .and_then(|params| params.get("protocolVersion"))
        .and_then(Value::as_str)
    else {
        return LATEST_PROTOCOL_VERSION;
    };
    SUPPORTED_PROTOCOL_VERSIONS
        .iter()
        .copied()
        .find(|version| *version == requested)
        .unwrap_or(LATEST_PROTOCOL_VERSION)
}

fn capture_client_name(params: Option<&Value>, session: &mut Session) {
    let Some(name) = params
        .and_then(|params| params.get("clientInfo"))
        .and_then(|client_info| client_info.get("name"))
        .and_then(Value::as_str)
    else {
        return;
    };
    session.client_name = Some(sanitize_client_name(name));
}

fn write_tools_call<W: Write>(
    output: &mut W,
    id: Value,
    availability: Availability,
) -> io::Result<()> {
    if let Some((code, data_code, message)) = availability.disabled_error() {
        return write_error(
            output,
            id,
            code,
            message,
            Some(json!({ "code": data_code })),
        );
    }

    // B1 advertises no tools even when enabled; B2 adds the read tools.
    write_error(
        output,
        id,
        -32602,
        "Unknown tool",
        Some(json!({ "code": "E_TOOL_NOT_FOUND" })),
    )
}

fn write_parse_error<W: Write>(output: &mut W) -> io::Result<()> {
    write_error(output, Value::Null, -32700, "Parse error", None)
}

fn write_result<W: Write>(output: &mut W, id: Value, result: Value) -> io::Result<()> {
    write_response(
        output,
        json!({ "jsonrpc": "2.0", "id": id, "result": result }),
    )
}

fn write_error<W: Write>(
    output: &mut W,
    id: Value,
    code: i64,
    message: &str,
    data: Option<Value>,
) -> io::Result<()> {
    let mut error = Map::new();
    error.insert("code".to_owned(), json!(code));
    error.insert("message".to_owned(), json!(message));
    if let Some(data) = data {
        error.insert("data".to_owned(), data);
    }
    write_response(
        output,
        json!({
            "jsonrpc": "2.0",
            "id": id,
            "error": Value::Object(error),
        }),
    )
}

fn write_response<W: Write>(output: &mut W, response: Value) -> io::Result<()> {
    serde_json::to_writer(&mut *output, &response).map_err(io::Error::other)?;
    output.write_all(b"\n")?;
    output.flush()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn run(input: &str, availability: Availability) -> (Vec<Value>, Session) {
        let mut output = Vec::new();
        let mut session = Session::default();
        serve_with_session(input.as_bytes(), &mut output, availability, &mut session)
            .expect("serve");
        let responses = String::from_utf8(output)
            .expect("utf-8")
            .lines()
            .map(|line| serde_json::from_str(line).expect("json response"))
            .collect();
        (responses, session)
    }

    const INITIALIZE: &str = concat!(
        r#"{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"test","version":"1"}}}"#,
        "\n"
    );

    fn with_initialize(rest: &str) -> String {
        format!("{INITIALIZE}{rest}")
    }

    fn initialize_with_params(params: &str) -> String {
        format!("{{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{params}}}\n")
    }

    #[test]
    fn initialize_returns_tools_capability_and_server_info() {
        let (responses, _) = run(INITIALIZE, Availability::Enabled);
        assert_eq!(responses.len(), 1);
        let result = &responses[0]["result"];
        assert_eq!(result["protocolVersion"], "2025-06-18");
        assert_eq!(result["capabilities"], json!({ "tools": {} }));
        assert_eq!(result["serverInfo"]["name"], SERVER_NAME);
        assert!(result["serverInfo"]["version"].is_string());
    }

    #[test]
    fn initialize_echoes_each_supported_protocol_version() {
        assert_eq!(
            LATEST_PROTOCOL_VERSION, SUPPORTED_PROTOCOL_VERSIONS[0],
            "latest must be the first supported version"
        );
        for version in SUPPORTED_PROTOCOL_VERSIONS {
            let input = initialize_with_params(&format!(r#"{{"protocolVersion":"{version}"}}"#));
            let (responses, _) = run(&input, Availability::Enabled);
            assert_eq!(responses.len(), 1, "version {version}");
            assert_eq!(
                responses[0]["result"]["protocolVersion"], *version,
                "supported version {version} must be echoed"
            );
        }
    }

    #[test]
    fn initialize_falls_back_to_latest_for_unusable_protocol_versions() {
        for params in [
            "{}",
            r#"{"protocolVersion":20250618}"#,
            r#"{"protocolVersion":null}"#,
            r#"{"protocolVersion":["2025-06-18"]}"#,
            r#"{"protocolVersion":"2099-01-01"}"#,
            r#"{"protocolVersion":""}"#,
            r#"{"protocolVersion":"2025-06-18 "}"#,
        ] {
            let input = initialize_with_params(params);
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
        let long_name = format!("\u{0007}\u{202E}{}", "a".repeat(80));
        let input = format!(
            "{{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{{\"clientInfo\":{{\"name\":{}}}}}}}\n",
            serde_json::to_string(&long_name).expect("json")
        );
        let (responses, session) = run(&input, Availability::Enabled);
        assert_eq!(responses.len(), 1);
        let captured = session.client_name().expect("client name");
        assert!(!captured.chars().any(char::is_control));
        assert!(!captured.contains('\u{202E}'));
        assert_eq!(captured.chars().count(), 64);
        assert_eq!(captured, "a".repeat(64));
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
        assert_eq!(responses[0]["result"], json!({}));
        assert_eq!(responses[1]["error"]["code"], -32601);
    }

    #[test]
    fn notifications_initialized_has_no_response() {
        let input = with_initialize(concat!(
            r#"{"jsonrpc":"2.0","method":"notifications/initialized"}"#,
            "\n",
        ));
        let (responses, _) = run(&input, Availability::Enabled);
        assert_eq!(responses.len(), 1);
        assert_eq!(responses[0]["id"], 1);
    }

    #[test]
    fn switch_off_keeps_initialize_and_lists_no_tools_but_rejects_calls() {
        let input = with_initialize(concat!(
            r#"{"jsonrpc":"2.0","id":2,"method":"tools/list"}"#,
            "\n",
            r#"{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_catalog","arguments":{}}}"#,
            "\n",
        ));
        let (responses, _) = run(&input, Availability::SwitchOff);
        assert_eq!(responses.len(), 3);
        assert_eq!(responses[0]["result"]["capabilities"]["tools"], json!({}));
        assert_eq!(responses[1]["result"]["tools"], json!([]));
        let error = &responses[2]["error"];
        assert_eq!(error["code"], -32001);
        assert_eq!(error["data"]["code"], "E_MCP_DISABLED");
        assert_eq!(
            error["message"],
            "Local MCP is turned off in OpenNavo Settings. Ask the user to enable it."
        );
    }

    #[test]
    fn app_not_running_rejects_calls_with_actionable_error() {
        let input = with_initialize(concat!(
            r#"{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_catalog"}}"#,
            "\n",
        ));
        let (responses, _) = run(&input, Availability::AppNotRunning);
        assert_eq!(responses.len(), 2);
        assert_eq!(responses[0]["result"]["capabilities"]["tools"], json!({}));
        let error = &responses[1]["error"];
        assert_eq!(error["code"], -32002);
        assert_eq!(error["data"]["code"], "E_APP_NOT_RUNNING");
        assert_eq!(
            error["message"],
            "OpenNavo is not running. Ask the user to open OpenNavo, then retry."
        );
    }

    #[test]
    fn malformed_json_returns_parse_error_and_keeps_serving() {
        let input = concat!(
            "{not json}\n",
            r#"{"jsonrpc":"2.0","id":2,"method":"ping"}"#,
            "\n",
        );
        let (responses, _) = run(input, Availability::Enabled);
        assert_eq!(responses.len(), 2);
        assert_eq!(responses[0]["error"]["code"], -32700);
        assert_eq!(responses[1]["result"], json!({}));
    }

    #[test]
    fn oversized_line_disconnects_without_response_or_panic() {
        let oversized = format!("{}\n", "x".repeat(MAX_LINE_BYTES + 1));
        let (responses, _) = run(&oversized, Availability::Enabled);
        assert!(responses.is_empty());
    }
}
