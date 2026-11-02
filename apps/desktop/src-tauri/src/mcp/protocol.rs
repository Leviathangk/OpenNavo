//! Hand-written, newline-delimited JSON-RPC 2.0 transport for MCP stdio mode.
//!
//! B2 implements the small surface needed by an MCP client: `initialize`,
//! `notifications/initialized`, `ping`, `tools/list` and `tools/call`. Tool
//! calls are forwarded lazily to the app socket after checking the persisted
//! master switch; `initialize` and `tools/list` never need the app. The server
//! is generic over `BufRead`/`Write` so tests can drive it in memory.

use std::io::{self, BufRead, Write};

use serde_json::{Map, Value, json};

use super::{client, state, tools};

/// Maximum accepted input line length, excluding the trailing newline.
pub const MAX_LINE_BYTES: usize = 1024 * 1024;

/// MCP protocol versions this server can speak, newest first.
///
/// Only `2025-06-18` is advertised: earlier revisions (notably `2025-03-26`)
/// permit JSON-RPC batching, which this server deliberately does not support.
pub const SUPPORTED_PROTOCOL_VERSIONS: &[&str] = &["2025-06-18"];

/// Newest supported protocol version, used whenever the client's request
/// cannot be honored (missing, malformed, or unsupported).
pub const LATEST_PROTOCOL_VERSION: &str = SUPPORTED_PROTOCOL_VERSIONS[0];

/// Name advertised in the `initialize` response.
pub const SERVER_NAME: &str = "opennavo";

/// Whether local MCP may serve tool calls.
///
/// The static variants are used by in-memory tests; the live stdio server
/// resolves the persisted switch and socket for every `tools/call`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Availability {
    /// The app is running and the Local MCP switch is on.
    Enabled,
    /// The Local MCP switch is off (the default).
    SwitchOff,
    /// The switch is on but the app-side broker is not reachable.
    AppNotRunning,
    /// The broker disappeared while serving this session.
    AppDisconnected,
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
            Self::AppDisconnected => {
                "OpenNavo disconnected; tools/call will return E_APP_DISCONNECTED"
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
                "OpenNavo is not running. Please open OpenNavo and try again.",
            )),
            Self::AppDisconnected => Some((
                -32003,
                "E_APP_DISCONNECTED",
                "OpenNavo disconnected before answering. Please retry the call.",
            )),
        }
    }
}

/// Resolve the persisted Local MCP switch without touching the socket.
///
/// A missing or unreadable state file means OFF. Tests inject static
/// availability through `serve_with_availability`.
pub fn local_mcp_availability() -> Availability {
    match state::data_dir() {
        Ok(data_dir) if state::load(&data_dir).enabled => Availability::Enabled,
        _ => Availability::SwitchOff,
    }
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

/// Strip control, line/paragraph separators, bidirectional formatting, and
/// other invisible formatting characters, then truncate to 64 Unicode scalar
/// values. The client-supplied name is never echoed.
pub fn sanitize_client_name(name: &str) -> String {
    name.chars()
        .filter(|character| !character.is_control() && !is_hidden_text(*character))
        .take(64)
        .collect()
}

/// Characters that must never reach a single-line display: line and paragraph
/// separators, bidirectional formatting, and invisible formatting. Shared by
/// the client name, the confirmation dialog and its displayed values.
pub fn is_hidden_text(character: char) -> bool {
    is_line_separator(character) || is_bidi_control(character) || is_invisible_format(character)
}

/// Unicode line separator (U+2028) and paragraph separator (U+2029).
///
/// These are not `char::is_control`, but they terminate lines in some
/// consumers, so they must not survive a value that is later written as a
/// single line.
fn is_line_separator(character: char) -> bool {
    matches!(character, '\u{2028}' | '\u{2029}')
}

fn is_bidi_control(character: char) -> bool {
    matches!(
        character,
        '\u{061C}' | '\u{200E}' | '\u{200F}' | '\u{202A}'..='\u{202E}' | '\u{2066}'..='\u{2069}'
    )
}

/// Zero-width characters, word joiner and invisible operators, byte-order
/// mark, and the Unicode tag block.
fn is_invisible_format(character: char) -> bool {
    matches!(
        character,
        '\u{200B}'..='\u{200D}' | '\u{2060}'..='\u{2064}' | '\u{FEFF}' | '\u{E0000}'..='\u{E007F}'
    )
}

/// Live backend used by the `mcp` subcommand.
enum Backend {
    Static(Availability),
    Live(client::Client),
}

impl Backend {
    fn availability(&self) -> Availability {
        match self {
            Self::Static(availability) => *availability,
            Self::Live(client) => {
                if state::load(client.data_dir()).enabled {
                    Availability::Enabled
                } else {
                    Availability::SwitchOff
                }
            }
        }
    }

    /// Whether the persisted write switch is on. In-memory tests never
    /// expose write tools.
    fn write_enabled(&self) -> bool {
        match self {
            Self::Static(_) => false,
            Self::Live(client) => state::load(client.data_dir()).write_enabled,
        }
    }

    fn call_tool(&mut self, params: Value) -> Result<Value, client::CallError> {
        match self {
            Self::Live(client) => client.call("tools/call", params),
            Self::Static(_) => Ok(Value::Null),
        }
    }
}

/// Serve MCP over the given streams with the live switch and socket.
pub fn serve<R: BufRead, W: Write>(input: R, output: W) -> io::Result<()> {
    let backend = match state::data_dir() {
        Ok(data_dir) => Backend::Live(client::Client::new(data_dir)),
        Err(_) => Backend::Static(Availability::SwitchOff),
    };
    eprintln!("mcp: {}", backend.availability().startup_message());
    let mut session = Session::default();
    serve_with_backend(input, output, backend, &mut session)
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

/// Serve MCP with a caller-supplied live client (tests use a temp data dir).
pub fn serve_with_client<R: BufRead, W: Write>(
    input: R,
    output: W,
    client: client::Client,
) -> io::Result<()> {
    let mut session = Session::default();
    serve_with_backend(input, output, Backend::Live(client), &mut session)
}

/// Serve MCP while exposing the session for tests.
pub fn serve_with_session<R: BufRead, W: Write>(
    input: R,
    output: W,
    availability: Availability,
    session: &mut Session,
) -> io::Result<()> {
    serve_with_backend(input, output, Backend::Static(availability), session)
}

fn serve_with_backend<R: BufRead, W: Write>(
    mut input: R,
    mut output: W,
    mut backend: Backend,
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

        handle_line(&line, &mut output, session, &mut backend)?;
    }
}

fn handle_line<W: Write>(
    line: &[u8],
    output: &mut W,
    session: &mut Session,
    backend: &mut Backend,
) -> io::Result<()> {
    let text = match std::str::from_utf8(line) {
        Ok(text) => text,
        Err(_) => return write_parse_error(output),
    };
    let message: Value = match serde_json::from_str(text) {
        Ok(message) => message,
        Err(_) => return write_parse_error(output),
    };
    if message.is_array() {
        return write_error(
            output,
            Value::Null,
            -32600,
            "Batch requests are not supported",
            None,
        );
    }
    let Some(object) = message.as_object() else {
        return write_error(output, Value::Null, -32600, "Invalid Request", None);
    };

    // JSON-RPC ids are strings, integer numbers, or null. Anything else is an
    // invalid request; a malformed id never gets echoed back.
    let id = match object.get("id") {
        None | Some(Value::Null) => None,
        Some(Value::String(_)) => object.get("id").cloned(),
        Some(Value::Number(number)) if number.is_i64() || number.is_u64() => {
            object.get("id").cloned()
        }
        Some(_) => {
            return write_error(output, Value::Null, -32600, "Invalid Request", None);
        }
    };
    if object.get("jsonrpc").and_then(Value::as_str) != Some("2.0") {
        return write_error(
            output,
            id.unwrap_or(Value::Null),
            -32600,
            "Invalid Request",
            None,
        );
    }

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
        "tools/list" => write_result(output, id, tools::list_result_for(backend.write_enabled())),
        "tools/call" => write_tools_call(output, id, object.get("params"), backend, session),
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
    let sanitized = sanitize_client_name(name);
    // A name made only of stripped characters carries no information; treat it
    // as if no name was supplied.
    session.client_name = if sanitized.is_empty() {
        None
    } else {
        Some(sanitized)
    };
}

fn write_tools_call<W: Write>(
    output: &mut W,
    id: Value,
    params: Option<&Value>,
    backend: &mut Backend,
    session: &Session,
) -> io::Result<()> {
    // The master switch is checked before any validation: while it is off,
    // every call (unknown names and malformed params included) gets the same
    // disabled error.
    if let Some((code, data_code, message)) = backend.availability().disabled_error() {
        return write_error(
            output,
            id,
            code,
            message,
            Some(json!({ "code": data_code })),
        );
    }

    let Some(params) = params.and_then(Value::as_object) else {
        return write_error(
            output,
            id,
            -32602,
            "Invalid tool call",
            Some(json!({ "code": "E_INVALID_ARG" })),
        );
    };
    let Some(name) = params.get("name").and_then(Value::as_str) else {
        return write_error(
            output,
            id,
            -32602,
            "Invalid tool call",
            Some(json!({ "code": "E_INVALID_ARG" })),
        );
    };

    // A write tool is forwarded only while the persisted write switch is on;
    // the broker checks again. Unknown names are never forwarded.
    if tools::is_write_tool(name) && !backend.write_enabled() {
        return write_error(
            output,
            id,
            -32004,
            tools::WRITE_DISABLED_MESSAGE,
            Some(json!({ "code": "E_MCP_WRITE_DISABLED" })),
        );
    }
    if !tools::is_read_tool(name) && !tools::is_write_tool(name) {
        return write_error(
            output,
            id,
            -32602,
            &format!("Unknown tool: {name}"),
            Some(json!({ "code": "E_TOOL_NOT_FOUND" })),
        );
    }

    let mut forwarded = Value::Object(params.clone());
    if let Some(client_name) = session.client_name()
        && let Some(object) = forwarded.as_object_mut()
    {
        object.insert("client".to_owned(), json!({ "name": client_name }));
    }

    match backend.call_tool(forwarded) {
        Ok(result) => write_result(output, id, tool_result(result)),
        Err(error) => write_error(
            output,
            id,
            error.jsonrpc_code(),
            &error.message(),
            Some(json!({ "code": error.data_code() })),
        ),
    }
}

/// Wrap a broker result as an MCP `CallToolResult`.
///
/// The broker returns structured JSON; clients expect a `content` array. Text
/// is the JSON encoding, and object results are also exposed through
/// `structuredContent` for clients that support it.
fn tool_result(result: Value) -> Value {
    if result.get("content").is_some() {
        return result;
    }
    let text = serde_json::to_string(&result).unwrap_or_else(|_| "null".to_owned());
    if result.is_object() {
        json!({
            "content": [{ "type": "text", "text": text }],
            "structuredContent": result,
        })
    } else {
        json!({ "content": [{ "type": "text", "text": text }] })
    }
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
        run_bytes(input.as_bytes(), availability)
    }

    fn run_bytes(input: &[u8], availability: Availability) -> (Vec<Value>, Session) {
        let mut output = Vec::new();
        let mut session = Session::default();
        serve_with_session(input, &mut output, availability, &mut session).expect("serve");
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
    fn sanitize_client_name_strips_invisible_and_tag_characters() {
        let hostile = "a\u{200B}b\u{FEFF}c\u{E0000}d\u{E007F}e\u{E0080}f";
        assert_eq!(sanitize_client_name(hostile), "abcde\u{E0080}f");

        let padded = format!("\u{200B}\u{FEFF}\u{E0041}{}", "z".repeat(70));
        assert_eq!(sanitize_client_name(&padded), "z".repeat(64));
    }

    #[test]
    fn sanitize_client_name_strips_line_and_paragraph_separators() {
        assert_eq!(
            sanitize_client_name("a\u{2028}b\u{2029}c"),
            "abc",
            "U+2028 and U+2029 must not survive into a single-line value"
        );

        let only_separators = "\u{2028}\u{2029}\u{200B}";
        assert_eq!(sanitize_client_name(only_separators), "");
        let input = format!(
            "{{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{{\"clientInfo\":{{\"name\":{}}}}}}}\n",
            serde_json::to_string(only_separators).expect("json")
        );
        let (responses, session) = run(&input, Availability::Enabled);
        assert_eq!(responses.len(), 1);
        assert_eq!(
            session.client_name(),
            None,
            "a name that is empty after sanitizing must become None"
        );
    }

    #[test]
    fn crlf_line_endings_are_accepted_and_carriage_return_is_stripped() {
        let input = concat!(
            "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"clientInfo\":{\"name\":\"crlf\"}}}\r\n",
            "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"ping\"}\r\n",
        );
        let (responses, session) = run(input, Availability::Enabled);
        assert_eq!(responses.len(), 2);
        assert_eq!(responses[0]["id"], 1);
        assert_eq!(responses[1]["id"], 2);
        assert_eq!(responses[1]["result"], json!({}));
        assert_eq!(session.client_name(), Some("crlf"));
    }

    #[test]
    fn final_line_without_newline_is_processed() {
        let input = r#"{"jsonrpc":"2.0","id":7,"method":"ping"}"#;
        let (responses, _) = run(input, Availability::Enabled);
        assert_eq!(responses.len(), 1);
        assert_eq!(responses[0]["id"], 7);
        assert_eq!(responses[0]["result"], json!({}));
    }

    #[test]
    fn invalid_utf8_returns_parse_error_and_keeps_serving() {
        let mut input = vec![0xFF, 0xFE, b'\n'];
        input.extend_from_slice(br#"{"jsonrpc":"2.0","id":2,"method":"ping"}"#);
        input.push(b'\n');
        let (responses, _) = run_bytes(&input, Availability::Enabled);
        assert_eq!(responses.len(), 2);
        assert_eq!(responses[0]["id"], Value::Null);
        assert_eq!(responses[0]["error"]["code"], -32700);
        assert_eq!(responses[1]["id"], 2);
        assert_eq!(responses[1]["result"], json!({}));
    }

    #[test]
    fn invalid_id_types_are_rejected_with_null_id() {
        for id in ["true", "1.5", "[]", "{}", r#"{"nested":1}"#] {
            let request = format!(r#"{{"jsonrpc":"2.0","id":{id},"method":"ping"}}"#);
            let (responses, _) = run(&format!("{request}\n"), Availability::Enabled);
            assert_eq!(responses.len(), 1, "id {id}");
            assert_eq!(responses[0]["id"], Value::Null, "id {id}");
            assert_eq!(responses[0]["error"]["code"], -32600, "id {id}");
            assert_eq!(
                responses[0]["error"]["message"], "Invalid Request",
                "id {id}"
            );
        }
    }

    #[test]
    fn string_and_integer_ids_are_echoed_and_null_id_is_a_notification() {
        for (id, expected) in [
            (r#""abc""#, json!("abc")),
            ("0", json!(0)),
            ("-5", json!(-5)),
            ("9007199254740993", json!(9007199254740993_i64)),
        ] {
            let request = format!(r#"{{"jsonrpc":"2.0","id":{id},"method":"ping"}}"#);
            let (responses, _) = run(&format!("{request}\n"), Availability::Enabled);
            assert_eq!(responses.len(), 1, "id {id}");
            assert_eq!(responses[0]["id"], expected, "id {id}");
            assert_eq!(responses[0]["result"], json!({}), "id {id}");
        }

        for request in [
            r#"{"jsonrpc":"2.0","method":"ping"}"#,
            r#"{"jsonrpc":"2.0","id":null,"method":"ping"}"#,
        ] {
            let (responses, _) = run(&format!("{request}\n"), Availability::Enabled);
            assert!(
                responses.is_empty(),
                "notification must not receive a response: {request}"
            );
        }
    }

    #[test]
    fn batch_requests_get_one_error_with_null_id_and_serving_continues() {
        for batch in [
            "[]",
            r#"[{"jsonrpc":"2.0","id":1,"method":"ping"}]"#,
            r#"[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","id":2,"method":"ping"}]"#,
        ] {
            let input = format!(
                "{batch}\n{}\n",
                r#"{"jsonrpc":"2.0","id":3,"method":"ping"}"#
            );
            let (responses, _) = run(&input, Availability::Enabled);
            assert_eq!(responses.len(), 2, "batch {batch}");
            assert_eq!(responses[0]["id"], Value::Null, "batch {batch}");
            assert_eq!(responses[0]["error"]["code"], -32600, "batch {batch}");
            assert_eq!(
                responses[0]["error"]["message"], "Batch requests are not supported",
                "batch {batch}"
            );
            assert_eq!(responses[1]["id"], 3, "batch {batch}");
            assert_eq!(responses[1]["result"], json!({}), "batch {batch}");
        }
    }

    #[test]
    fn only_non_batching_protocol_versions_are_supported() {
        assert_eq!(
            SUPPORTED_PROTOCOL_VERSIONS,
            &["2025-06-18"][..],
            "batching-capable revisions must not be advertised"
        );
        for requested in ["2025-03-26", "2024-11-05"] {
            let input = initialize_with_params(&format!(r#"{{"protocolVersion":"{requested}"}}"#));
            let (responses, _) = run(&input, Availability::Enabled);
            assert_eq!(responses.len(), 1, "version {requested}");
            assert_eq!(
                responses[0]["result"]["protocolVersion"], LATEST_PROTOCOL_VERSION,
                "version {requested} must fall back to latest"
            );
        }
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
    fn switch_off_keeps_initialize_and_lists_read_tools_but_rejects_calls() {
        let input = with_initialize(concat!(
            r#"{"jsonrpc":"2.0","id":2,"method":"tools/list"}"#,
            "\n",
            r#"{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_catalog","arguments":{}}}"#,
            "\n",
        ));
        let (responses, _) = run(&input, Availability::SwitchOff);
        assert_eq!(responses.len(), 3);
        assert_eq!(responses[0]["result"]["capabilities"]["tools"], json!({}));
        assert_eq!(
            responses[1]["result"]["tools"],
            tools::list_result()["tools"]
        );
        assert!(
            !responses[1]["result"]["tools"]
                .as_array()
                .expect("tools")
                .is_empty()
        );
        let error = &responses[2]["error"];
        assert_eq!(error["code"], -32001);
        assert_eq!(error["data"]["code"], "E_MCP_DISABLED");
        assert_eq!(
            error["message"],
            "Local MCP is turned off in OpenNavo Settings. Ask the user to enable it."
        );
    }

    #[test]
    fn read_tools_do_not_expose_write_names() {
        let input = with_initialize(concat!(
            r#"{"jsonrpc":"2.0","id":2,"method":"tools/list"}"#,
            "\n",
        ));
        let (responses, _) = run(&input, Availability::Enabled);
        let tools = responses[1]["result"]["tools"]
            .as_array()
            .expect("tools array");
        let names: Vec<&str> = tools
            .iter()
            .filter_map(|tool| tool["name"].as_str())
            .collect();
        assert_eq!(
            names,
            vec![
                "search_catalog",
                "get_catalog_item",
                "list_installed_apps",
                "list_updatable_apps",
                "get_task",
                "scan_cleanup"
            ]
        );
        for forbidden in [
            "install_app",
            "uninstall_app",
            "clean_cache",
            "queue_updates",
            "get_latest_scan_result",
        ] {
            assert!(
                !names.contains(&forbidden),
                "{forbidden} must not be listed"
            );
        }
    }

    #[test]
    fn write_tools_are_refused_while_writes_are_off() {
        for name in [
            "install_app",
            "uninstall_app",
            "clean_cache",
            "queue_updates",
        ] {
            let input = with_initialize(&format!(
                "{{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{{\"name\":\"{name}\",\"arguments\":{{}}}}}}\n"
            ));
            let (responses, _) = run(&input, Availability::Enabled);
            let error = &responses[1]["error"];
            assert_eq!(error["code"], -32004, "name {name}");
            assert_eq!(error["data"]["code"], "E_MCP_WRITE_DISABLED", "name {name}");
        }
    }

    #[test]
    fn unknown_tool_calls_are_rejected() {
        for name in ["get_latest_scan_result", "delete_everything"] {
            let input = with_initialize(&format!(
                "{{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{{\"name\":\"{name}\",\"arguments\":{{}}}}}}\n"
            ));
            let (responses, _) = run(&input, Availability::Enabled);
            let error = &responses[1]["error"];
            assert_eq!(error["code"], -32602, "name {name}");
            assert_eq!(error["data"]["code"], "E_TOOL_NOT_FOUND", "name {name}");
        }
    }

    #[test]
    fn switch_off_rejects_every_call_before_validation() {
        for params in [
            r#"{"name":"delete_everything","arguments":{}}"#,
            r#"{"name":"install_app"}"#,
            r#"{"arguments":{}}"#,
            r#""not an object""#,
        ] {
            let input = with_initialize(&format!(
                "{{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{params}}}\n"
            ));
            let (responses, _) = run(&input, Availability::SwitchOff);
            let error = &responses[1]["error"];
            assert_eq!(error["code"], -32001, "params {params}");
            assert_eq!(error["data"]["code"], "E_MCP_DISABLED", "params {params}");
        }
    }

    #[test]
    fn tool_results_are_wrapped_in_a_mcp_content_array() {
        let input = with_initialize(concat!(
            r#"{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_catalog","arguments":{"query":"firefox"}}}"#,
            "\n",
        ));
        let (responses, _) = run(&input, Availability::Enabled);
        let result = &responses[1]["result"];
        assert_eq!(result["content"][0]["type"], "text");
        assert!(result["content"][0]["text"].is_string());
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
            "OpenNavo is not running. Please open OpenNavo and try again."
        );
    }

    #[test]
    fn live_switch_off_returns_e_mcp_disabled_without_a_socket() {
        let dir = tempfile::tempdir().expect("tempdir");
        let input = with_initialize(concat!(
            r#"{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_catalog"}}"#,
            "\n",
        ));
        let mut output = Vec::new();
        serve_with_client(
            input.as_bytes(),
            &mut output,
            client::Client::new(dir.path().to_path_buf()),
        )
        .expect("serve");
        let responses: Vec<Value> = String::from_utf8(output)
            .expect("utf8")
            .lines()
            .map(|line| serde_json::from_str(line).expect("json"))
            .collect();
        assert_eq!(responses[1]["error"]["data"]["code"], "E_MCP_DISABLED");
    }

    #[test]
    fn live_switch_on_without_socket_returns_e_app_not_running() {
        let dir = tempfile::tempdir().expect("tempdir");
        state::prepare_enabled_for_test(dir.path());
        let input = with_initialize(concat!(
            r#"{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_catalog"}}"#,
            "\n",
        ));
        let mut output = Vec::new();
        serve_with_client(
            input.as_bytes(),
            &mut output,
            client::Client::new(dir.path().to_path_buf()),
        )
        .expect("serve");
        let responses: Vec<Value> = String::from_utf8(output)
            .expect("utf8")
            .lines()
            .map(|line| serde_json::from_str(line).expect("json"))
            .collect();
        let error = &responses[1]["error"];
        assert_eq!(error["data"]["code"], "E_APP_NOT_RUNNING");
        assert_eq!(
            error["message"],
            "OpenNavo is not running. Please open OpenNavo and try again."
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
    fn rejects_jsonrpc_that_is_not_exactly_2_0() {
        for (request, expected_id) in [
            (r#"{"id":1,"method":"ping"}"#, json!(1)),
            (r#"{"jsonrpc":"1.0","id":2,"method":"ping"}"#, json!(2)),
            (r#"{"jsonrpc":"2.0 ","id":3,"method":"ping"}"#, json!(3)),
            (r#"{"jsonrpc":2.0,"id":4,"method":"ping"}"#, json!(4)),
            (r#"{"jsonrpc":null,"id":5,"method":"initialize"}"#, json!(5)),
            (r#"{"jsonrpc":"1.0","method":"ping"}"#, Value::Null),
        ] {
            let input = format!("{request}\n");
            let (responses, _) = run(&input, Availability::Enabled);
            assert_eq!(responses.len(), 1, "request {request}");
            let error = &responses[0];
            assert_eq!(error["id"], expected_id, "request {request}");
            assert_eq!(error["error"]["code"], -32600, "request {request}");
            assert_eq!(
                error["error"]["message"], "Invalid Request",
                "request {request}"
            );
        }

        // A bad jsonrpc version is rejected even for a notification-shaped
        // message, which would normally produce no response.
        let notification = "{\"jsonrpc\":\"1.0\",\"method\":\"notifications/initialized\"}\n";
        let (responses, _) = run(notification, Availability::Enabled);
        assert_eq!(responses.len(), 1);
        assert_eq!(responses[0]["id"], Value::Null);
        assert_eq!(responses[0]["error"]["code"], -32600);
    }

    #[test]
    fn maximum_length_line_is_processed() {
        let input = format!(
            "{}\n{}\n",
            "x".repeat(MAX_LINE_BYTES),
            r#"{"jsonrpc":"2.0","id":2,"method":"ping"}"#
        );
        let (responses, _) = run(&input, Availability::Enabled);
        assert_eq!(responses.len(), 2);
        assert_eq!(responses[0]["error"]["code"], -32700);
        assert_eq!(responses[1]["result"], json!({}));
    }

    #[test]
    fn oversized_line_disconnects_without_response_or_panic() {
        for size in [MAX_LINE_BYTES + 1, MAX_LINE_BYTES + 2] {
            let oversized = format!("{}\n", "x".repeat(size));
            let (responses, _) = run(&oversized, Availability::Enabled);
            assert!(responses.is_empty(), "size {size}");
        }
    }
}
