//! Streaming Hermes gateway transport.
//!
//! `hermes chat -Q` only prints the final answer to a pipe, so the desktop uses
//! the local `hermes serve` JSON-RPC gateway (`/api/ws`) for token deltas. The
//! gateway also owns the same agent/tool/session implementation as the CLI.

use std::path::Path;
use std::sync::Arc;
use std::time::Duration;

use futures_util::{SinkExt, StreamExt};
use serde_json::{Value, json};
use tauri::{AppHandle, Emitter};
use tokio::net::TcpStream;
use tokio::process::{Child, Command};
use tokio::sync::mpsc::UnboundedSender;
use tokio::time::{Instant, sleep, timeout, timeout_at};
use tokio_tungstenite::tungstenite::Message;
use tokio_tungstenite::{MaybeTlsStream, WebSocketStream, connect_async};

use super::{
    AppError, AskAnswer, HermesAskReply, HermesChatInput, HermesChatReply, HermesChunk,
    HermesModelOptions, HermesRuntimeState, HermesSteerResult, HermesSteerStatus, HermesUsage,
    append_audit, clean_output, kill_pid, provider_id, reasoning_effort, toolsets,
    write_catalog_config, write_config,
};

type Socket = WebSocketStream<MaybeTlsStream<TcpStream>>;

const START_TIMEOUT: Duration = Duration::from_secs(25);
const MESSAGE_TIMEOUT: Duration = Duration::from_secs(180);

pub struct GatewayServer {
    pub port: u16,
    pub token: String,
    fingerprint: String,
    pid: u32,
    _child: Child,
}

impl Drop for GatewayServer {
    fn drop(&mut self) {
        kill_pid(self.pid);
    }
}

impl GatewayServer {
    fn matches(&self, fingerprint: &str) -> bool {
        self.fingerprint == fingerprint
    }

    pub fn stop(&self) {
        kill_pid(self.pid);
    }
}

fn server_fingerprint(input: &HermesChatInput, key: Option<&str>) -> String {
    format!(
        "{}|{}|{}|{}|{}|{}|{}",
        input.profile.as_deref().unwrap_or("default"),
        provider_id(&input.provider),
        input.endpoint.as_deref().unwrap_or(""),
        key.map(key_digest).unwrap_or_default(),
        toolsets(input),
        input
            .auxiliary
            .iter()
            .filter(|entry| entry.provider != "auto" && !entry.model.is_empty())
            .map(|entry| format!("{}:{}:{}", entry.task, entry.provider, entry.model))
            .collect::<Vec<_>>()
            .join(","),
        input.force_aux_vision
    )
}

/// A running gateway keeps whatever key it loaded at startup, so a changed key has to
/// restart it. Digest the key rather than holding the secret in the fingerprint.
fn key_digest(key: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(key.as_bytes());
    let digest = format!("{:x}", hasher.finalize());
    digest[..16].to_owned()
}

fn apply_runtime_env(command: &mut Command, bin: &Path) {
    if let Some(dir) = bin.parent() {
        let path = std::env::var_os("PATH").unwrap_or_default();
        let mut paths = vec![dir.to_path_buf()];
        paths.extend(std::env::split_paths(&path));
        if let Ok(joined) = std::env::join_paths(paths) {
            command.env("PATH", joined);
        }
        let cua = dir
            .parent()
            .map(|root| root.join("CuaDriver.app/Contents/MacOS/cua-driver"));
        if let Some(cua) = cua.filter(|path| path.is_file()) {
            command.env("HERMES_CUA_DRIVER_CMD", cua);
        }
    }
}

fn free_port() -> Result<u16, AppError> {
    let listener = std::net::TcpListener::bind(("127.0.0.1", 0)).map_err(|error| {
        AppError::new("E_HERMES_GATEWAY", "Could not reserve a local port.")
            .with_detail(error.to_string())
    })?;
    let port = listener
        .local_addr()
        .map_err(|error| {
            AppError::new("E_HERMES_GATEWAY", "Could not read the local port.")
                .with_detail(error.to_string())
        })?
        .port();
    Ok(port)
}

async fn port_open(port: u16) -> bool {
    timeout(
        Duration::from_millis(400),
        TcpStream::connect(("127.0.0.1", port)),
    )
    .await
    .map(|result| result.is_ok())
    .unwrap_or(false)
}

async fn ensure_server(
    bin: &Path,
    home: &Path,
    state: &HermesRuntimeState,
    input: &HermesChatInput,
    api_key: Option<&str>,
) -> Result<Arc<GatewayServer>, AppError> {
    let fingerprint = server_fingerprint(input, api_key);
    let mut guard = state.gateway.lock().await;
    if let Some(server) = guard.as_ref()
        && server.matches(&fingerprint)
        && port_open(server.port).await
    {
        return Ok(server.clone());
    }
    *guard = None;
    let port = free_port()?;
    let token = uuid::Uuid::new_v4().to_string();
    let mut command = super::runtime_command(bin)?;
    // Always name the profile explicitly: without the flag the runtime follows the sticky
    // `active_profile` file, so a profile picked in the CLI could silently replace the
    // default profile whose config/.env we just wrote.
    let profile = input
        .profile
        .as_deref()
        .map(str::trim)
        .filter(|profile| !profile.is_empty())
        .unwrap_or("default");
    if !super::profiles::name_ok(profile) {
        return Err(AppError::new(
            "E_HERMES_PROFILE_NAME",
            "Profile names must be lowercase letters, digits, dashes or underscores.",
        ));
    }
    command.arg("--profile").arg(profile);
    command.arg("serve");
    if profile != "default" {
        command.arg("--isolated");
    }
    command
        .arg("--skip-build")
        .arg("--port")
        .arg(port.to_string())
        .env("HERMES_HOME", home)
        .env("HERMES_DASHBOARD_SESSION_TOKEN", &token)
        .env("HERMES_TUI_TOOLSETS", toolsets(input))
        .env("PYTHONUNBUFFERED", "1")
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null());
    apply_runtime_env(&mut command, bin);
    let child = command.spawn().map_err(|error| {
        AppError::new("E_HERMES_GATEWAY", "Hermes gateway could not start.")
            .with_detail(error.to_string())
    })?;
    let pid = child.id().unwrap_or(0);
    let deadline = Instant::now() + START_TIMEOUT;
    while Instant::now() < deadline {
        if port_open(port).await {
            let server = Arc::new(GatewayServer {
                port,
                token,
                fingerprint,
                pid,
                _child: child,
            });
            *guard = Some(server.clone());
            return Ok(server);
        }
        sleep(Duration::from_millis(150)).await;
    }
    kill_pid(pid);
    Err(AppError::new(
        "E_HERMES_GATEWAY",
        "Hermes gateway did not become ready. Restart Hermes and try again.",
    ))
}

fn rpc_error(value: &Value) -> Option<String> {
    value.get("error").map(|error| {
        error
            .get("message")
            .and_then(Value::as_str)
            .unwrap_or("Hermes gateway error")
            .to_owned()
    })
}

async fn send_json(socket: &mut Socket, value: Value) -> Result<(), AppError> {
    socket
        .send(Message::Text(value.to_string().into()))
        .await
        .map_err(|error| {
            AppError::new("E_HERMES_GATEWAY_STREAM", "Hermes gateway write failed.")
                .with_detail(error.to_string())
        })
}

/// Read the next JSON frame, skipping WebSocket control frames.
///
/// `hermes serve` runs uvicorn with its default `ws_ping_interval=20s`, so the runtime pings the
/// socket every 20 seconds. The official dashboard client is a browser: ping/pong happens inside
/// the browser's WebSocket stack and the page never sees those frames. tungstenite also answers
/// them for us (it queues the pong and flushes it on the next read), so a control frame only has to
/// be skipped. Treating it as a protocol error aborted the turn ~20s into a stream with
/// "Hermes gateway sent an unexpected frame."
///
/// `limit: None` keeps reading without a stall budget: while an approval card is open the user owns
/// the clock, and the loop still has to drain pings (a reader that stops reading drops the socket).
///
/// The budget is one absolute deadline for the whole read, not a timeout per frame: a control frame
/// restarts nothing, or a gateway that only pings would keep a turn alive forever.
async fn next_frame(socket: &mut Socket, limit: Option<Duration>) -> Result<Value, AppError> {
    let deadline = limit.map(|limit| Instant::now() + limit);
    loop {
        let pending = socket.next();
        let message = match deadline {
            Some(deadline) => timeout_at(deadline, pending).await.map_err(|_| {
                AppError::new("E_HERMES_GATEWAY_TIMEOUT", "Hermes gateway timed out.")
            })?,
            None => pending.await,
        };
        let message = message
            .ok_or_else(|| {
                AppError::new(
                    "E_HERMES_GATEWAY_STREAM",
                    "Hermes gateway closed the connection.",
                )
            })?
            .map_err(|error| {
                AppError::new("E_HERMES_GATEWAY_STREAM", "Hermes gateway read failed.")
                    .with_detail(error.to_string())
            })?;
        match message {
            Message::Text(text) => {
                return serde_json::from_str(text.as_str()).map_err(|error| {
                    AppError::new(
                        "E_HERMES_GATEWAY_STREAM",
                        "Hermes gateway sent invalid JSON.",
                    )
                    .with_detail(error.to_string())
                });
            }
            Message::Binary(bytes) => {
                return serde_json::from_slice(&bytes).map_err(|error| {
                    AppError::new(
                        "E_HERMES_GATEWAY_STREAM",
                        "Hermes gateway sent invalid JSON.",
                    )
                    .with_detail(error.to_string())
                });
            }
            Message::Ping(_) | Message::Pong(_) | Message::Frame(_) => continue,
            Message::Close(frame) => {
                let error = AppError::new(
                    "E_HERMES_GATEWAY_STREAM",
                    "Hermes gateway closed the connection.",
                );
                return Err(match frame {
                    Some(frame) => error.with_detail(format!(
                        "code {}: {}",
                        u16::from(frame.code),
                        frame.reason
                    )),
                    None => error,
                });
            }
        }
    }
}

async fn recv_json(socket: &mut Socket) -> Result<Value, AppError> {
    next_frame(socket, Some(MESSAGE_TIMEOUT)).await
}

async fn rpc(socket: &mut Socket, id: u64, method: &str, params: Value) -> Result<Value, AppError> {
    send_json(
        socket,
        json!({"jsonrpc": "2.0", "id": id, "method": method, "params": params}),
    )
    .await?;
    loop {
        let value = recv_json(socket).await?;
        if value.get("id").and_then(Value::as_u64) != Some(id) {
            continue;
        }
        if let Some(error) = rpc_error(&value) {
            return Err(AppError::new("E_HERMES_GATEWAY_STREAM", error));
        }
        return Ok(value.get("result").cloned().unwrap_or(Value::Null));
    }
}

async fn open_session(
    socket: &mut Socket,
    input: &HermesChatInput,
    home: &Path,
    remote: bool,
) -> Result<(String, String, Vec<Value>, Option<i64>), AppError> {
    let provider = if remote {
        input.provider.clone()
    } else {
        provider_id(&input.provider).to_owned()
    };
    let model = input.model.clone();
    let effort = reasoning_effort(&input.effort);
    if let Some(stored) = input
        .session_id
        .as_deref()
        .map(str::trim)
        .filter(|value| !value.is_empty())
    {
        let resume = json!({"session_id": stored, "cols": 120, "source": "desktop"});
        let result = rpc(socket, 1, "session.resume", resume).await;
        if let Ok(result) = result
            && let Some(live) = result.get("session_id").and_then(Value::as_str)
        {
            let stored_id = result
                .get("stored_session_id")
                .and_then(Value::as_str)
                .unwrap_or(stored)
                .to_owned();
            // `open_requests` replays whoever was still waiting on a human (an approval card written
            // to the previous connection): without re-delivering it the agent stays parked until the
            // runtime's own deadline and the user never sees the question.
            let open = result
                .get("open_requests")
                .and_then(Value::as_array)
                .cloned()
                .unwrap_or_default();
            let generation = result.get("execution_generation").and_then(Value::as_i64);
            return Ok((live.to_owned(), stored_id, open, generation));
        }
    }
    let mut params = json!({
        "cols": 120,
        "title": input.prompt.chars().take(60).collect::<String>(),
        "source": "desktop",
        "model": model,
        "provider": provider,
        "fast": input.fast
    });
    // A remote host cannot see this Mac's paths, so never send `cwd` (or fall back to this
    // Mac's HOME) on a remote request: it only leaked the local working directory.
    if !remote {
        let cwd = input
            .cwd
            .as_deref()
            .map(str::trim)
            .filter(|dir| !dir.is_empty() && Path::new(dir).is_dir())
            .map(str::to_owned)
            .or_else(|| Some(std::env::var("HOME").unwrap_or_else(|_| home.display().to_string())));
        if let Some(cwd) = cwd {
            params["cwd"] = Value::String(cwd);
        }
    }
    if let Some(effort) = effort {
        params["reasoning_effort"] = Value::String(effort.to_owned());
    }
    if let Some(profile) = input
        .profile
        .as_deref()
        .map(str::trim)
        .filter(|profile| !profile.is_empty() && *profile != "default")
    {
        params["profile"] = Value::String(profile.to_owned());
    }
    let result = rpc(socket, 1, "session.create", params).await?;
    let live = result
        .get("session_id")
        .and_then(Value::as_str)
        .ok_or_else(|| {
            AppError::new(
                "E_HERMES_GATEWAY_STREAM",
                "Hermes gateway did not return a session id.",
            )
        })?
        .to_owned();
    let stored = result
        .get("stored_session_id")
        .and_then(Value::as_str)
        .unwrap_or(&live)
        .to_owned();
    let open = result
        .get("open_requests")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default();
    let generation = result.get("execution_generation").and_then(Value::as_i64);
    Ok((live, stored, open, generation))
}

/// Stream a turn through the local Hermes gateway. The desktop has no fallback
/// to the CLI when the gateway itself cannot start.
pub async fn chat(
    bin: &Path,
    home: &Path,
    state: &HermesRuntimeState,
    input: HermesChatInput,
    app: Option<&AppHandle>,
    sink: Option<UnboundedSender<String>>,
) -> Result<HermesChatReply, AppError> {
    // `--profile` makes the runtime re-home to `<root>/profiles/<name>` before it reads any
    // config, so the model and `.env` must be written there rather than to the shared root.
    let config_home = super::profiles::resolve_home(home, input.profile.as_deref())?;
    let api_key = write_config(&config_home, &input)?;
    let server = ensure_server(bin, &config_home, state, &input, api_key.as_deref()).await?;
    let url = format!(
        "ws://127.0.0.1:{}/api/ws?token={}",
        server.port, server.token
    );
    let (socket, _) = connect_async(url).await.map_err(|error| {
        AppError::new(
            "E_HERMES_GATEWAY_STREAM",
            "Could not connect to the Hermes gateway.",
        )
        .with_detail(error.to_string())
    })?;
    chat_over_socket(socket, home, state, input, app, sink, false).await
}

fn remote_ws_url_auth(url: &str, query: &str) -> Result<String, AppError> {
    let url = url.trim();
    let url = if let Some(rest) = url.strip_prefix("https://") {
        format!("wss://{rest}")
    } else if let Some(rest) = url.strip_prefix("http://") {
        format!("ws://{rest}")
    } else {
        url.to_owned()
    };
    if !(url.starts_with("ws://") || url.starts_with("wss://")) {
        return Err(AppError::new(
            "E_HERMES_REMOTE_URL",
            "Remote gateway URL must start with ws://, wss://, http:// or https://.",
        ));
    }
    let (mut base, existing) = match url.split_once('?') {
        Some((base, query)) => (base.to_owned(), Some(query)),
        None => (url, None),
    };
    // Keep any panel mount prefix (`https://host/hermes`) instead of treating it as the
    // WebSocket endpoint; only append `/api/ws` when the path is not already that endpoint.
    base = base.trim_end_matches('/').to_owned();
    if let Some(trimmed) = base.strip_suffix("/login") {
        base = trimmed.trim_end_matches('/').to_owned();
    }
    if !base.ends_with("/api/ws") {
        base.push_str("/api/ws");
    }
    let query = existing
        .map(|existing| format!("{existing}&{query}"))
        .unwrap_or_else(|| query.to_owned());
    Ok(format!("{base}?{query}"))
}

async fn connect_remote_auth(url: &str, query: &str) -> Result<Socket, AppError> {
    let ws_url = remote_ws_url_auth(url, query)?;
    let (socket, _) = timeout(START_TIMEOUT, connect_async(ws_url))
        .await
        .map_err(|_| {
            AppError::new(
                "E_HERMES_REMOTE_TIMEOUT",
                "Remote gateway connection timed out.",
            )
        })?
        .map_err(|error| {
            AppError::new(
                "E_HERMES_REMOTE_CONNECT",
                "Could not connect to the remote Hermes gateway.",
            )
            .with_detail(error.to_string())
        })?;
    Ok(socket)
}

pub async fn chat_remote_auth(
    url: &str,
    query: &str,
    home: &Path,
    state: &HermesRuntimeState,
    input: HermesChatInput,
    app: Option<&AppHandle>,
    sink: Option<UnboundedSender<String>>,
) -> Result<HermesChatReply, AppError> {
    let socket = connect_remote_auth(url, query).await?;
    chat_over_socket(socket, home, state, input, app, sink, true).await
}

/// Fallback window when a request carries no `timeout_s`. Matches the runtime's own
/// `approvals.timeout` default, so a card lives as long as the agent is willing to wait.
const APPROVAL_TIMEOUT: Duration = Duration::from_secs(300);
/// Longest a card may stay open, whatever the runtime asks for (a bad value must not park the turn).
const APPROVAL_TIMEOUT_MAX: Duration = Duration::from_secs(3600);
/// The refusal the official renderer sends when no window of its own serves a bridge tool
/// (`server_requests.py::NOT_SHOWN_CODE`). It is not a JSON-RPC code, so it is spelled out here.
const NOT_SHOWN_CODE: i64 = 4404;
/// Bridges driven by in-app windows this build does not have: a preview pane, a terminal, a tour.
const WINDOW_OWNED_REQUESTS: [&str; 5] = [
    "preview.read",
    "preview.act",
    "terminal.read",
    "window.read",
    "tour",
];

/// What a server→client request asks the renderer for. Each kind has its own card and result shape.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum AskKind {
    Approval,
    Clarify,
    Secret,
}

impl AskKind {
    fn of(method: &str) -> Option<Self> {
        match method {
            "approval" => Some(Self::Approval),
            "clarify" => Some(Self::Clarify),
            // One masked string each (`ValueResult { value }`): the sudo password, an env secret, a
            // vault unlock, a one-time code. `vault.save_login` wants a JSON blob and is refused.
            "sudo" | "secret" | "vault.unlock_prompt" | "vault.code" => Some(Self::Secret),
            _ => None,
        }
    }

    fn wire(self) -> &'static str {
        match self {
            Self::Approval => "approval",
            Self::Clarify => "clarify",
            Self::Secret => "secret",
        }
    }

    /// Fail-closed answer when the card is dismissed, the turn is stopped or the window expires.
    fn refusal(self) -> Value {
        match self {
            Self::Approval => json!({"choice": "deny"}),
            // An empty object is clarify's "cancelled": nobody answered, which is not an answer.
            Self::Clarify => json!({}),
            Self::Secret => json!({"value": ""}),
        }
    }
}

/// Where an answer goes: current runtimes take a server→client request answered by JSON-RPC id
/// (`srq-…`), older ones an `approval.request` event answered by `approval.respond`.
enum AskTarget {
    Request(String),
    Event(String),
}

impl AskTarget {
    fn id(&self) -> &str {
        match self {
            AskTarget::Request(id) | AskTarget::Event(id) => id,
        }
    }

    fn matches(&self, id: &str) -> bool {
        match self {
            AskTarget::Request(own) | AskTarget::Event(own) => own == id,
        }
    }
}

/// Who owns the approval decision for this turn.
struct AskPolicy {
    /// Ask the user (writes are confirmed, or a remote host owns its tools) instead of deciding here.
    ask_user: bool,
    /// This Mac's full-control mode: approve without a prompt.
    full_control: bool,
}

/// One card the user still has to answer. The read loop keeps running while it is open: uvicorn pings
/// the socket every 20s, and a reader that parks on the answer drops the connection first.
struct PendingAsk {
    target: AskTarget,
    kind: AskKind,
    /// The chat turn that opened this card; the renderer uses it to ignore another window's events.
    turn_request_id: String,
    /// What the settled note in the transcript names (the command, the env var, the first question).
    summary: String,
    deadline: Instant,
}

enum Step {
    Frame(Value),
    Answer(Option<AskAnswer>),
    Cancelled,
    Expired,
}

/// Show a card for a server→client request, or answer it from the turn's own policy.
#[allow(clippy::too_many_arguments)]
async fn open_ask(
    socket: &mut Socket,
    app: Option<&AppHandle>,
    target: AskTarget,
    kind: AskKind,
    params: Value,
    policy: &AskPolicy,
    live_sid: &str,
    turn_request_id: &str,
) -> Result<Option<PendingAsk>, AppError> {
    let window = ask_window(&params);
    // Only approvals have a policy: a clarify or a password prompt is the agent asking something the
    // app cannot answer for the user, so those always reach the card.
    if kind == AskKind::Approval && !policy.ask_user {
        let choice = if policy.full_control { "once" } else { "deny" };
        send_response(socket, &target, json!({"choice": choice}), live_sid).await?;
        return Ok(None);
    }
    if let Some(app) = app {
        let _ = app.emit(
            "hermes-ask",
            json!({
                "requestId": target.id(),
                "turnRequestId": turn_request_id,
                "kind": kind.wire(),
                "deadline": unix_millis() + window.as_millis() as u64,
                "params": params,
            }),
        );
    }
    Ok(Some(PendingAsk {
        summary: ask_summary(kind, &params),
        target,
        kind,
        turn_request_id: turn_request_id.to_owned(),
        deadline: Instant::now() + window,
    }))
}

/// The window the runtime is willing to wait (`approvals.timeout`, forwarded as `timeout_s`), bounded
/// so a malformed value cannot park the turn forever.
fn ask_window(params: &Value) -> Duration {
    params
        .get("timeout_s")
        .and_then(Value::as_u64)
        .filter(|seconds| *seconds > 0)
        .map(Duration::from_secs)
        .map(|window| window.min(APPROVAL_TIMEOUT_MAX))
        .unwrap_or(APPROVAL_TIMEOUT)
}

fn unix_millis() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|elapsed| elapsed.as_millis() as u64)
        .unwrap_or_default()
}

/// One line naming what the user was asked, kept short enough for a transcript row.
fn ask_summary(kind: AskKind, params: &Value) -> String {
    let field = |name: &str| {
        params
            .get(name)
            .and_then(Value::as_str)
            .map(str::trim)
            .filter(|value| !value.is_empty())
    };
    let text = match kind {
        AskKind::Approval => field("command")
            .or_else(|| field("description"))
            .unwrap_or_default(),
        AskKind::Clarify => field("question")
            .or_else(|| {
                params
                    .get("questions")
                    .and_then(Value::as_array)
                    .and_then(|questions| questions.first())
                    .and_then(|question| question.get("question"))
                    .and_then(Value::as_str)
            })
            .unwrap_or_default(),
        AskKind::Secret => field("env_var")
            .or_else(|| field("display_name"))
            .or_else(|| field("site"))
            .or_else(|| field("command"))
            .unwrap_or_default(),
    };
    let mut summary = text.chars().take(200).collect::<String>();
    if text.chars().count() > 200 {
        summary.push('…');
    }
    summary
}

/// Answer a server→client request with the JSON-RPC `result` the runtime expects.
async fn send_response(
    socket: &mut Socket,
    target: &AskTarget,
    result: Value,
    live_sid: &str,
) -> Result<(), AppError> {
    match target {
        AskTarget::Request(id) => {
            send_json(
                socket,
                json!({"jsonrpc": "2.0", "id": id, "result": result}),
            )
            .await
        }
        // The event protocol only ever asks for approvals; the answer is its own RPC.
        AskTarget::Event(request_id) => {
            let choice = result
                .get("choice")
                .and_then(Value::as_str)
                .unwrap_or("deny")
                .to_owned();
            let mut params = json!({"session_id": live_sid, "choice": choice});
            if !request_id.is_empty() {
                params["request_id"] = Value::String(request_id.clone());
            }
            send_json(
                socket,
                json!({"jsonrpc": "2.0", "id": 100, "method": "approval.respond", "params": params}),
            )
            .await
        }
    }
}

/// Refuse a request this build cannot serve. A bridge tool gets the renderer's "no window shows this
/// chat" decline (the runtime turns it into a friendly refusal); anything else says "no handler",
/// which settles the request at once so the agent is never parked on a question nobody can see.
async fn refuse_request(socket: &mut Socket, id: &str, method: &str) -> Result<(), AppError> {
    let (code, message) = if WINDOW_OWNED_REQUESTS.contains(&method) {
        (
            NOT_SHOWN_CODE,
            "No Hermes Desktop window is showing this chat, so its preview, terminal and tour are out \
             of reach. Ask the user to open this chat in the Desktop app, then retry."
                .to_owned(),
        )
    } else {
        (
            -32601,
            format!("This Hermes Desktop build has no handler for {method}."),
        )
    };
    send_json(
        socket,
        json!({"jsonrpc": "2.0", "id": id, "error": {"code": code, "message": message}}),
    )
    .await
}

/// The transcript note for one settled request: what was asked, how it ended, and — for a clarify —
/// how many questions were answered. Never carries a secret value.
fn settled_note(
    ask: &PendingAsk,
    outcome: &str,
    scope: Option<&str>,
    count: Option<usize>,
) -> Value {
    json!({
        "requestId": ask.target.id(),
        "turnRequestId": ask.turn_request_id,
        "kind": ask.kind.wire(),
        "outcome": outcome,
        "scope": scope,
        "summary": ask.summary,
        "count": count,
    })
}

/// The runtime-side result for one answer, plus the note the transcript records for it.
fn answer_result(ask: &PendingAsk, reply: HermesAskReply) -> (Value, Value) {
    let settled = |outcome: &str, scope: Option<&str>, count: Option<usize>| {
        settled_note(ask, outcome, scope, count)
    };
    match (ask.kind, reply) {
        (AskKind::Approval, HermesAskReply::Approval { choice }) => {
            let scope =
                matches!(choice.as_str(), "once" | "session" | "always").then(|| choice.clone());
            let outcome = if choice == "deny" {
                "denied"
            } else {
                "allowed"
            };
            (
                json!({"choice": choice}),
                settled(outcome, scope.as_deref(), None),
            )
        }
        (AskKind::Clarify, HermesAskReply::Clarify { answers, cancelled }) => {
            if cancelled {
                return (AskKind::Clarify.refusal(), settled("cancelled", None, None));
            }
            let answered = answers
                .iter()
                .filter(|answer| answer.answer.is_some())
                .count();
            let answers = answers
                .into_iter()
                .map(|answer| {
                    (
                        answer.qid,
                        answer.answer.map(Value::String).unwrap_or(Value::Null),
                    )
                })
                .collect::<serde_json::Map<String, Value>>();
            (
                json!({"answers": answers}),
                settled("submitted", None, Some(answered)),
            )
        }
        (AskKind::Secret, HermesAskReply::Secret { value }) => {
            let outcome = if value.trim().is_empty() {
                "skipped"
            } else {
                "submitted"
            };
            (json!({"value": value}), settled(outcome, None, None))
        }
        // A reply that does not match its card is a renderer bug: fail closed rather than park the agent.
        (kind, _) => (kind.refusal(), settled("cancelled", None, None)),
    }
}

fn emit_settled(app: Option<&AppHandle>, settled: &Value) {
    if let Some(app) = app {
        let _ = app.emit("hermes-ask-settled", settled.clone());
    }
}

async fn chat_over_socket(
    mut socket: Socket,
    home: &Path,
    state: &HermesRuntimeState,
    input: HermesChatInput,
    app: Option<&AppHandle>,
    sink: Option<UnboundedSender<String>>,
    remote: bool,
) -> Result<HermesChatReply, AppError> {
    // The runtime fails every server→client request fast for a connection that never advertised
    // handling them (`server_requests.py::advertise`). Without this the approval queue waits out its
    // own timeout and withdraws the tool while no card ever reaches the user.
    let _ = rpc(
        &mut socket,
        0,
        "client.capabilities",
        json!({"server_requests": true}),
    )
    .await;
    let (live_sid, stored_sid, open_requests, resumed_generation) =
        open_session(&mut socket, &input, home, remote).await?;
    if let Some(app) = app {
        let _ = app.emit(
            "hermes-session",
            json!({"requestId": input.request_id, "liveSessionId": live_sid, "sessionId": stored_sid}),
        );
    }
    // A remote host still owns its tools, so its approval must reach the user even though this Mac's
    // capabilities are forced off.
    let policy = AskPolicy {
        ask_user: input.confirm_writes || remote,
        full_control: !remote
            && input.mac_enabled
            && input.control_level == "full"
            && !input.confirm_writes,
    };
    if policy.full_control {
        let _ = rpc(
            &mut socket,
            2,
            "config.set",
            json!({"session_id": live_sid, "key": "yolo", "value": true, "scope": "session"}),
        )
        .await;
    }
    if remote && !input.attachments.is_empty() {
        return Err(AppError::new(
            "E_HERMES_REMOTE_ATTACHMENT",
            "Remote gateways do not accept local attachment paths yet.",
        ));
    }
    let mut prompt = input.prompt.clone();
    for (index, attachment) in input.attachments.iter().enumerate() {
        let path = Path::new(attachment);
        if !path.is_file() {
            return Err(AppError::new(
                "E_HERMES_ATTACHMENT",
                "The attached file no longer exists.",
            ));
        }
        let image = path
            .extension()
            .and_then(|extension| extension.to_str())
            .map(|extension| {
                matches!(
                    extension.to_ascii_lowercase().as_str(),
                    "png" | "jpg" | "jpeg" | "gif" | "webp" | "heic" | "heif" | "bmp" | "tiff"
                )
            })
            .unwrap_or(false);
        if image {
            rpc(
                &mut socket,
                10 + index as u64,
                "image.attach",
                json!({"session_id": live_sid, "path": attachment}),
            )
            .await?;
        } else {
            let attached = rpc(
                &mut socket,
                10 + index as u64,
                "file.attach",
                json!({"session_id": live_sid, "path": attachment}),
            )
            .await?;
            if let Some(reference) = attached.get("ref_text").and_then(Value::as_str) {
                prompt.push('\n');
                prompt.push_str(reference);
            }
        }
    }
    send_json(
        &mut socket,
        json!({"jsonrpc": "2.0", "id": 4, "method": "prompt.submit", "params": {"session_id": live_sid, "text": prompt}}),
    )
    .await?;

    let result = stream_turn(
        &mut socket,
        home,
        state,
        &input,
        app,
        sink.as_ref(),
        &live_sid,
        &stored_sid,
        policy,
        open_requests,
        resumed_generation,
    )
    .await;
    // The cards, and the sender a late answer would reach, die with the turn.
    *state.asks.lock().await = None;
    result
}

#[allow(clippy::too_many_arguments)]
async fn stream_turn(
    socket: &mut Socket,
    home: &Path,
    state: &HermesRuntimeState,
    input: &HermesChatInput,
    app: Option<&AppHandle>,
    sink: Option<&UnboundedSender<String>>,
    live_sid: &str,
    stored_sid: &str,
    policy: AskPolicy,
    open_requests: Vec<Value>,
    resumed_generation: Option<i64>,
) -> Result<HermesChatReply, AppError> {
    let mut asks: Vec<PendingAsk> = Vec::new();
    let (answers, mut answered) = tokio::sync::mpsc::unbounded_channel::<AskAnswer>();
    *state.asks.lock().await = Some(answers);
    // A question still waiting on the previous connection is re-delivered here, as the official
    // client does with `session.resume`'s `open_requests`.
    for entry in open_requests {
        let Some(id) = entry.get("id").and_then(Value::as_str) else {
            continue;
        };
        let method = entry
            .get("method")
            .and_then(Value::as_str)
            .unwrap_or_default();
        let params = entry.get("params").cloned().unwrap_or(Value::Null);
        match AskKind::of(method) {
            Some(kind) => {
                if let Some(ask) = open_ask(
                    socket,
                    app,
                    AskTarget::Request(id.to_owned()),
                    kind,
                    params,
                    &policy,
                    live_sid,
                    &input.request_id,
                )
                .await?
                {
                    asks.push(ask);
                }
            }
            None => refuse_request(socket, id, method).await?,
        }
    }
    let mut deadline = Instant::now() + MESSAGE_TIMEOUT;
    let mut accumulated = String::new();
    // Resume reports the last claimed generation. The turn this submit is about to claim is
    // strictly newer, so the snapshot is only a floor: publishing it would steer with a value
    // the gateway is about to retire.
    let mut execution_generation = resumed_generation;
    loop {
        // A parked question owns the clock: the turn's stall budget does not run while the user decides.
        if asks.is_empty() && Instant::now() >= deadline {
            let _ = send_json(
                socket,
                json!({"jsonrpc": "2.0", "id": 99, "method": "session.interrupt", "params": {"session_id": live_sid}}),
            )
            .await;
            return Err(AppError::new(
                "E_HERMES_GATEWAY_TIMEOUT",
                "Hermes took too long to answer.",
            ));
        }
        let value = if asks.is_empty() {
            // Bounded by the same stall budget as the check above, so a gateway that only pings
            // lands here and the session is interrupted instead of being left running.
            match next_frame(socket, Some(MESSAGE_TIMEOUT)).await {
                Ok(value) => value,
                Err(error) if error.code == "E_HERMES_GATEWAY_TIMEOUT" => {
                    let _ = send_json(
                        socket,
                        json!({"jsonrpc": "2.0", "id": 99, "method": "session.interrupt", "params": {"session_id": live_sid}}),
                    )
                    .await;
                    return Err(AppError::new(
                        "E_HERMES_GATEWAY_TIMEOUT",
                        "Hermes took too long to answer.",
                    ));
                }
                Err(error) => return Err(error),
            }
        } else {
            let soonest = asks
                .iter()
                .map(|ask| ask.deadline)
                .min()
                .unwrap_or(deadline);
            let wait = soonest.saturating_duration_since(Instant::now());
            let step = tokio::select! {
                frame = next_frame(socket, None) => Step::Frame(frame?),
                answer = answered.recv() => Step::Answer(answer),
                _ = state.approval_cancel.notified() => Step::Cancelled,
                _ = sleep(wait) => Step::Expired,
            };
            match step {
                Step::Frame(value) => value,
                Step::Answer(Some(answer)) => {
                    if let Some(index) = asks
                        .iter()
                        .position(|ask| ask.target.matches(&answer.request_id))
                    {
                        let ask = asks.remove(index);
                        let (result, settled) = answer_result(&ask, answer.reply);
                        send_response(socket, &ask.target, result, live_sid).await?;
                        emit_settled(app, &settled);
                    }
                    continue;
                }
                // The turn was stopped (⌘⇧.) or the channel died: fail closed on every open card.
                Step::Answer(None) | Step::Cancelled => {
                    for ask in asks.drain(..) {
                        send_response(socket, &ask.target, ask.kind.refusal(), live_sid).await?;
                        emit_settled(app, &settled_note(&ask, "cancelled", None, None));
                    }
                    continue;
                }
                Step::Expired => {
                    let now = Instant::now();
                    let (expired, live): (Vec<PendingAsk>, Vec<PendingAsk>) =
                        asks.drain(..).partition(|ask| ask.deadline <= now);
                    asks = live;
                    for ask in expired {
                        send_response(socket, &ask.target, ask.kind.refusal(), live_sid).await?;
                        emit_settled(app, &settled_note(&ask, "timeout", None, None));
                    }
                    continue;
                }
            }
        };
        // `MESSAGE_TIMEOUT` is a stall budget, not a cap on the turn: the official dashboard client
        // has no whole-turn deadline, so any frame the runtime sends (a delta, a tool event, a
        // compaction status) proves the turn is alive. Without this reset a long answer streaming
        // past three minutes was interrupted mid-sentence.
        deadline = Instant::now() + MESSAGE_TIMEOUT;
        if let Some(error) = rpc_error(&value)
            && value.get("id").and_then(Value::as_u64) == Some(4)
        {
            append_audit(home, input, "failed", Some(stored_sid), Some(&error));
            return Err(AppError::new("E_HERMES_GATEWAY_STREAM", error));
        }
        // A server→client request: the turn is parked on a human answer. Approvals, clarify questions
        // and password prompts become cards; a bridge this build has no window for is declined.
        if let Some(method) = value
            .get("method")
            .and_then(Value::as_str)
            .filter(|method| *method != "event")
        {
            let Some(Value::String(id)) = value.get("id") else {
                continue;
            };
            let params = value.get("params").cloned().unwrap_or(Value::Null);
            match AskKind::of(method) {
                Some(kind) => {
                    if let Some(ask) = open_ask(
                        socket,
                        app,
                        AskTarget::Request(id.clone()),
                        kind,
                        params,
                        &policy,
                        live_sid,
                        &input.request_id,
                    )
                    .await?
                    {
                        asks.push(ask);
                    }
                }
                None => refuse_request(socket, id, method).await?,
            }
            continue;
        }
        let Some(params) = value.get("params") else {
            continue;
        };
        let event = params
            .get("type")
            .and_then(Value::as_str)
            .unwrap_or_default();
        let payload = params.get("payload").cloned().unwrap_or(Value::Null);
        // `prompt.submit` publishes `session.info` before the claim, still carrying the previous
        // generation and `running: false`. A steer flushed with that value is refused as
        // `stale_generation`. Only the claim (`message.start`) or a frame that says the turn is
        // running may advance the stamp, and only to a newer value.
        if let Some(generation) = accepted_running_generation(execution_generation, event, params) {
            execution_generation = Some(generation);
            if let Some(app) = app {
                let _ = app.emit(
                    "hermes-session",
                    json!({
                        "requestId": input.request_id,
                        "liveSessionId": live_sid,
                        "sessionId": stored_sid,
                        "executionGeneration": generation
                    }),
                );
            }
        }
        match event {
            "message.delta" => {
                let delta = payload
                    .get("text")
                    .and_then(Value::as_str)
                    .unwrap_or_default();
                if delta.is_empty() {
                    continue;
                }
                accumulated.push_str(delta);
                if let Some(sink) = sink {
                    let _ = sink.send(delta.to_owned());
                }
                if let Some(app) = app {
                    let _ = app.emit(
                        "hermes-chunk",
                        HermesChunk {
                            request_id: input.request_id.clone(),
                            delta: delta.to_owned(),
                        },
                    );
                }
            }
            "tool.start" => {
                if let Some(app) = app {
                    let _ = app.emit(
                        "hermes-tool",
                        json!({
                            "requestId": input.request_id,
                            "phase": "start",
                            "toolId": payload.get("tool_id").and_then(Value::as_str).unwrap_or_default(),
                            "name": payload.get("name").and_then(Value::as_str).unwrap_or("tool"),
                            "label": payload.get("preview").and_then(Value::as_str).unwrap_or("")
                        }),
                    );
                }
            }
            "tool.complete" => {
                if let Some(app) = app {
                    let _ = app.emit(
                        "hermes-tool",
                        json!({
                            "requestId": input.request_id,
                            "phase": "complete",
                            "toolId": payload.get("tool_id").and_then(Value::as_str).unwrap_or_default(),
                            "name": payload.get("name").and_then(Value::as_str).unwrap_or("tool"),
                            "label": payload.get("summary").and_then(Value::as_str).unwrap_or(""),
                            "duration": payload.get("duration_s").and_then(Value::as_f64).unwrap_or(0.0)
                        }),
                    );
                }
            }
            "reasoning.delta" | "thinking.delta" => {
                if let Some(app) = app {
                    let delta = payload.get("text").and_then(Value::as_str).unwrap_or("");
                    if !delta.is_empty() {
                        let _ = app.emit(
                            "hermes-reasoning",
                            json!({"requestId": input.request_id, "delta": delta}),
                        );
                    }
                }
            }
            "message.complete" => {
                let text = payload
                    .get("text")
                    .and_then(Value::as_str)
                    .unwrap_or(&accumulated)
                    .to_owned();
                let status = payload
                    .get("status")
                    .and_then(Value::as_str)
                    .unwrap_or("complete");
                if status == "error" {
                    append_audit(home, input, "failed", Some(stored_sid), Some(&text));
                    return Err(AppError::new("E_HERMES_GATEWAY_STREAM", text));
                }
                append_audit(home, input, "succeeded", Some(stored_sid), None);
                let usage = payload
                    .get("usage")
                    .cloned()
                    .and_then(|value| serde_json::from_value::<HermesUsage>(value).ok());
                return Ok(HermesChatReply {
                    text: clean_output(&text),
                    session_id: Some(stored_sid.to_owned()),
                    live_session_id: Some(live_sid.to_owned()),
                    usage,
                });
            }
            "error" => {
                let message = payload
                    .get("message")
                    .and_then(Value::as_str)
                    .unwrap_or("Hermes gateway returned an error.")
                    .to_owned();
                append_audit(home, input, "failed", Some(stored_sid), Some(&message));
                return Err(AppError::new("E_HERMES_GATEWAY_STREAM", message));
            }
            "request.cancel" => {
                // The runtime withdrew the question (its own deadline, an interrupt, another surface
                // answered): drop the card and record how it ended.
                let id = payload
                    .get("id")
                    .and_then(Value::as_str)
                    .unwrap_or_default();
                if let Some(index) = asks.iter().position(|ask| ask.target.matches(id)) {
                    let ask = asks.remove(index);
                    let outcome =
                        if payload.get("reason").and_then(Value::as_str) == Some("timeout") {
                            "timeout"
                        } else {
                            "cancelled"
                        };
                    emit_settled(app, &settled_note(&ask, outcome, None, None));
                }
            }
            "approval.request" => {
                // Older runtimes ask over an event and take `approval.respond`; current ones ask over
                // the server→client request handled above.
                let request_id = payload
                    .get("request_id")
                    .and_then(Value::as_str)
                    .unwrap_or_default()
                    .to_owned();
                if let Some(ask) = open_ask(
                    socket,
                    app,
                    AskTarget::Event(request_id),
                    AskKind::Approval,
                    payload,
                    &policy,
                    live_sid,
                    &input.request_id,
                )
                .await?
                {
                    asks.push(ask);
                }
            }
            _ => {}
        }
    }
}

async fn connect_running(state: &HermesRuntimeState) -> Result<Socket, AppError> {
    let server = state
        .gateway
        .lock()
        .await
        .as_ref()
        .cloned()
        .ok_or_else(|| AppError::new("E_HERMES_GATEWAY", "No Hermes session is running."))?;
    let url = format!(
        "ws://127.0.0.1:{}/api/ws?token={}",
        server.port, server.token
    );
    connect_async(url)
        .await
        .map(|(socket, _)| socket)
        .map_err(|error| {
            AppError::new(
                "E_HERMES_GATEWAY_STREAM",
                "Could not connect to the Hermes gateway.",
            )
            .with_detail(error.to_string())
        })
}

pub async fn interrupt(state: &HermesRuntimeState, live_session_id: &str) -> Result<(), AppError> {
    // Wakes the running turn's card wait, which fails closed on every open ask itself.
    state.approval_cancel.notify_waiters();
    let mut socket = connect_running(state).await?;
    rpc(
        &mut socket,
        1,
        "session.interrupt",
        json!({"session_id": live_session_id}),
    )
    .await?;
    Ok(())
}

pub async fn interrupt_remote_auth(
    url: &str,
    query: &str,
    live_session_id: &str,
) -> Result<(), AppError> {
    let mut socket = connect_remote_auth(url, query).await?;
    rpc(
        &mut socket,
        1,
        "session.interrupt",
        json!({"session_id": live_session_id}),
    )
    .await?;
    Ok(())
}

pub async fn steer_remote_auth(
    url: &str,
    query: &str,
    live_session_id: &str,
    execution_generation: i64,
    text: &str,
) -> Result<HermesSteerResult, AppError> {
    if text.trim().is_empty() {
        return Err(AppError::new("E_HERMES_INPUT", "The message is empty."));
    }
    let mut socket = connect_remote_auth(url, query).await?;
    let result = rpc(
        &mut socket,
        1,
        "session.steer",
        steer_params(live_session_id, execution_generation, text),
    )
    .await?;
    steer_result(result)
}

pub async fn steer(
    state: &HermesRuntimeState,
    live_session_id: &str,
    execution_generation: i64,
    text: &str,
) -> Result<HermesSteerResult, AppError> {
    if text.trim().is_empty() {
        return Err(AppError::new("E_HERMES_INPUT", "The message is empty."));
    }
    let mut socket = connect_running(state).await?;
    let result = rpc(
        &mut socket,
        1,
        "session.steer",
        steer_params(live_session_id, execution_generation, text),
    )
    .await?;
    steer_result(result)
}

/// Generation a queued steer may echo, or `None` when this frame must not move the stamp.
///
/// Idle `session.info` (the frame `prompt.submit` publishes before the admission is claimed)
/// still carries the previous turn's generation. The official desktop client also refuses to
/// move a generation backwards.
fn accepted_running_generation(current: Option<i64>, event: &str, params: &Value) -> Option<i64> {
    let payload = params.get("payload").unwrap_or(&Value::Null);
    let running =
        event == "message.start" || payload.get("running").and_then(Value::as_bool) == Some(true);
    if !running {
        return None;
    }
    let generation = params
        .get("execution_generation")
        .and_then(Value::as_i64)
        .or_else(|| payload.get("execution_generation").and_then(Value::as_i64))?;
    if current.is_some_and(|current| generation <= current) {
        return None;
    }
    Some(generation)
}

/// `session.steer` rejects any other key and requires this exact generation, so keep the wire
/// shape in one place instead of letting the local and remote paths drift apart.
fn steer_params(live_session_id: &str, execution_generation: i64, text: &str) -> Value {
    json!({
        "session_id": live_session_id,
        "text": text,
        "execution_generation": execution_generation
    })
}

fn steer_result(result: Value) -> Result<HermesSteerResult, AppError> {
    let status = match result.get("status").and_then(Value::as_str) {
        Some("queued") => HermesSteerStatus::Queued,
        Some("redirected") => HermesSteerStatus::Redirected,
        Some("rejected") => HermesSteerStatus::Rejected,
        _ => Err(AppError::new(
            "E_HERMES_STEER",
            "Hermes gateway returned an unexpected steer response.",
        )
        .with_detail(result.to_string()))?,
    };
    let delivered = if status == HermesSteerStatus::Rejected {
        result.get("delivered").and_then(Value::as_bool)
    } else {
        None
    };
    Ok(HermesSteerResult { status, delivered })
}

/// Read the official Hermes model catalog through the local gateway.
pub async fn model_options(
    bin: &Path,
    home: &Path,
    state: &HermesRuntimeState,
    input: HermesChatInput,
    refresh: bool,
    include_unconfigured: bool,
) -> Result<HermesModelOptions, AppError> {
    let config_home = super::profiles::resolve_home(home, input.profile.as_deref())?;
    // A catalog read must not rewrite the profile's model, agent or auxiliary settings: the
    // settings sheet loads a profile's catalog just by selecting it, passing that profile's
    // saved draft (or the built-in default when this window never opened it), so a full write
    // here replaced the profile's own model with whatever the draft happened to hold.
    let api_key = write_catalog_config(&config_home, &input)?;
    let server = ensure_server(bin, &config_home, state, &input, api_key.as_deref()).await?;
    let url = format!(
        "ws://127.0.0.1:{}/api/ws?token={}",
        server.port, server.token
    );
    let (socket, _) = connect_async(url).await.map_err(|error| {
        AppError::new(
            "E_HERMES_GATEWAY_STREAM",
            "Could not connect to the Hermes gateway.",
        )
        .with_detail(error.to_string())
    })?;
    model_options_over_socket(
        socket,
        refresh,
        include_unconfigured,
        input.profile.as_deref(),
    )
    .await
}

async fn model_options_over_socket(
    mut socket: Socket,
    refresh: bool,
    include_unconfigured: bool,
    profile: Option<&str>,
) -> Result<HermesModelOptions, AppError> {
    let result = tokio::time::timeout(
        std::time::Duration::from_secs(45),
        rpc(
            &mut socket,
            1,
            "model.options",
            json!({
                "include_unconfigured": include_unconfigured,
                "refresh": refresh,
                "profile": profile.filter(|value| !value.trim().is_empty() && *value != "default")
            }),
        ),
    )
    .await
    .map_err(|_| {
        AppError::new(
            "E_HERMES_MODEL_OPTIONS_TIMEOUT",
            "加载官方模型目录超时，请稍后重试。",
        )
    })??;
    serde_json::from_value(result).map_err(|error| {
        AppError::new(
            "E_HERMES_MODEL_OPTIONS",
            "Hermes returned an invalid model catalog.",
        )
        .with_detail(error.to_string())
    })
}

pub async fn model_options_remote_auth(
    url: &str,
    query: &str,
    profile: Option<&str>,
    refresh: bool,
    include_unconfigured: bool,
) -> Result<HermesModelOptions, AppError> {
    let socket = connect_remote_auth(url, query).await?;
    model_options_over_socket(socket, refresh, include_unconfigured, profile).await
}

/// Test seam used by the ignored real-runtime tests.
#[cfg(test)]
pub async fn chat_for_test(
    bin: &Path,
    home: &Path,
    input: HermesChatInput,
    sink: UnboundedSender<String>,
) -> Result<HermesChatReply, AppError> {
    let state = HermesRuntimeState::default();
    chat(bin, home, &state, input, None, Some(sink)).await
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::hermes::HermesClarifyAnswer;

    /// Connect a client to an in-process WebSocket server and return both ends.
    async fn ws_pair() -> (
        Socket,
        tokio_tungstenite::WebSocketStream<tokio::net::TcpStream>,
    ) {
        let listener = tokio::net::TcpListener::bind(("127.0.0.1", 0))
            .await
            .expect("bind");
        let addr = listener.local_addr().expect("local addr");
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.expect("accept");
            tokio_tungstenite::accept_async(stream)
                .await
                .expect("handshake")
        });
        let (client, _) = connect_async(format!("ws://{addr}/api/ws"))
            .await
            .expect("connect");
        let server = server.await.expect("server task");
        (client, server)
    }

    async fn next_text(
        socket: &mut tokio_tungstenite::WebSocketStream<tokio::net::TcpStream>,
    ) -> Value {
        loop {
            match socket.next().await.expect("frame").expect("message") {
                Message::Text(text) => return serde_json::from_str(text.as_str()).expect("json"),
                _ => continue,
            }
        }
    }

    fn ask(target: AskTarget, kind: AskKind, params: Value) -> PendingAsk {
        PendingAsk {
            summary: ask_summary(kind, &params),
            target,
            kind,
            turn_request_id: "turn-1".into(),
            deadline: Instant::now() + APPROVAL_TIMEOUT,
        }
    }

    #[test]
    fn the_card_window_follows_the_runtime_deadline() {
        // The runtime forwards its own `approvals.timeout`, so the card lives exactly as long as the
        // agent is willing to wait instead of denying on a client-side guess.
        assert_eq!(
            ask_window(&json!({"timeout_s": 300})),
            Duration::from_secs(300)
        );
        assert_eq!(
            ask_window(&json!({"timeout_s": 45})),
            Duration::from_secs(45)
        );
        assert_eq!(ask_window(&json!({})), APPROVAL_TIMEOUT);
        assert_eq!(ask_window(&json!({"timeout_s": 0})), APPROVAL_TIMEOUT);
        assert_eq!(
            ask_window(&json!({"timeout_s": 999_999})),
            APPROVAL_TIMEOUT_MAX
        );
    }

    #[test]
    fn every_card_kind_maps_to_the_methods_the_runtime_uses() {
        assert_eq!(AskKind::of("approval"), Some(AskKind::Approval));
        assert_eq!(AskKind::of("clarify"), Some(AskKind::Clarify));
        for method in ["sudo", "secret", "vault.unlock_prompt", "vault.code"] {
            assert_eq!(AskKind::of(method), Some(AskKind::Secret), "{method}");
        }
        // `vault.save_login` wants a JSON credential blob, and bridges need windows we do not have.
        for method in ["vault.save_login", "preview.read", "tour", "setup_beat"] {
            assert_eq!(AskKind::of(method), None, "{method}");
        }
    }

    #[test]
    fn an_idle_session_info_is_not_the_running_generation() {
        let idle = json!({
            "type": "session.info",
            "execution_generation": 7,
            "payload": {"running": false, "execution_generation": 7}
        });
        assert_eq!(
            accepted_running_generation(None, "session.info", &idle),
            None
        );
        assert_eq!(
            accepted_running_generation(Some(7), "session.info", &idle),
            None
        );
        // A token frame is not a claim. The generation arrives on `message.start`.
        let delta = json!({
            "type": "message.delta",
            "execution_generation": 8,
            "payload": {"text": "x"}
        });
        assert_eq!(
            accepted_running_generation(Some(7), "message.delta", &delta),
            None
        );

        let start = json!({
            "type": "message.start",
            "execution_generation": 8,
            "payload": {"text": ""}
        });
        assert_eq!(
            accepted_running_generation(Some(7), "message.start", &start),
            Some(8)
        );
        assert_eq!(
            accepted_running_generation(Some(8), "message.start", &start),
            None,
            "the same stamp must not be published twice"
        );
        assert_eq!(
            accepted_running_generation(
                Some(8),
                "message.start",
                &json!({"execution_generation": 7, "payload": {}})
            ),
            None,
            "a replay must not rewind the floor"
        );

        let running = json!({
            "type": "session.info",
            "payload": {"running": true, "execution_generation": 9}
        });
        assert_eq!(
            accepted_running_generation(Some(8), "session.info", &running),
            Some(9)
        );
    }

    #[test]
    fn steer_carries_the_running_generation() {
        let params = steer_params("live-1", 7, "check the failing test");
        assert_eq!(
            params,
            json!({
                "session_id": "live-1",
                "text": "check the failing test",
                "execution_generation": 7
            })
        );
        assert_eq!(
            steer_result(json!({"status": "queued"})).expect("queued steer"),
            HermesSteerResult {
                status: HermesSteerStatus::Queued,
                delivered: None
            }
        );
        assert_eq!(
            steer_result(json!({"status": "redirected"})).expect("redirected steer"),
            HermesSteerResult {
                status: HermesSteerStatus::Redirected,
                delivered: None
            }
        );
        assert_eq!(
            steer_result(json!({"status": "rejected", "delivered": false}))
                .expect("an undelivered rejection reaches the UI"),
            HermesSteerResult {
                status: HermesSteerStatus::Rejected,
                delivered: Some(false)
            }
        );
        assert_eq!(
            steer_result(json!({"status": "rejected"})).expect("a delivered rejection is final"),
            HermesSteerResult {
                status: HermesSteerStatus::Rejected,
                delivered: None
            }
        );
        assert_eq!(
            steer_result(json!({"status": "unexpected"}))
                .expect_err("an unknown steer status must reach the UI")
                .code,
            "E_HERMES_STEER"
        );
    }

    #[tokio::test]
    async fn a_ping_only_gateway_still_runs_out_of_budget() {
        let (mut client, mut server) = ws_pair().await;
        // `hermes serve` pings every 20s. Skipping them keeps the socket alive, but each one used to
        // restart the per-frame timeout, so a gateway that only pinged could hold a turn open
        // forever; the stall budget has to be one deadline for the whole read.
        let pinger = tokio::spawn(async move {
            loop {
                if server.send(Message::Ping(Vec::new().into())).await.is_err() {
                    return;
                }
                sleep(Duration::from_millis(10)).await;
            }
        });
        let result = timeout(
            Duration::from_secs(5),
            next_frame(&mut client, Some(Duration::from_millis(120))),
        )
        .await
        .expect("the stall budget must expire despite the pings");
        pinger.abort();

        assert_eq!(
            result.expect_err("a stalled read has to time out").code,
            "E_HERMES_GATEWAY_TIMEOUT"
        );
    }

    #[tokio::test]
    async fn answers_a_server_request_by_its_srq_id() {
        let (mut client, mut server) = ws_pair().await;
        let open = ask(
            AskTarget::Request("srq-abc123".into()),
            AskKind::Approval,
            json!({"command": "rm -rf build"}),
        );
        let (result, settled) = answer_result(
            &open,
            HermesAskReply::Approval {
                choice: "session".into(),
            },
        );
        send_response(&mut client, &open.target, result, "live-1")
            .await
            .expect("answer");

        let frame = next_text(&mut server).await;
        assert_eq!(frame["id"], "srq-abc123");
        assert_eq!(frame["result"]["choice"], "session");
        assert!(frame.get("method").is_none());
        // The transcript note names the scope the user granted.
        assert_eq!(settled["outcome"], "allowed");
        assert_eq!(settled["scope"], "session");
        assert_eq!(settled["summary"], "rm -rf build");
    }

    #[tokio::test]
    async fn answers_an_event_approval_over_its_rpc() {
        let (mut client, mut server) = ws_pair().await;
        send_response(
            &mut client,
            &AskTarget::Event("approval-7".into()),
            json!({"choice": "deny"}),
            "live-1",
        )
        .await
        .expect("answer");
        let frame = next_text(&mut server).await;
        assert_eq!(frame["method"], "approval.respond");
        assert_eq!(frame["params"]["session_id"], "live-1");
        assert_eq!(frame["params"]["request_id"], "approval-7");
        assert_eq!(frame["params"]["choice"], "deny");
    }

    #[test]
    fn event_approvals_match_answers_and_cancellations() {
        let target = AskTarget::Event("approval-7".into());
        assert!(target.matches("approval-7"));
        assert!(!target.matches("approval-8"));
    }

    #[test]
    fn settled_notes_name_the_turn_that_asked() {
        let card = ask(
            AskTarget::Request("srq-abc123".into()),
            AskKind::Approval,
            json!({"command": "rm -rf build"}),
        );
        let settled = settled_note(&card, "allowed", Some("once"), None);
        assert_eq!(settled["requestId"], "srq-abc123");
        assert_eq!(settled["turnRequestId"], "turn-1");
    }

    #[tokio::test]
    async fn clarify_answers_travel_as_one_answers_map() {
        let (mut client, mut server) = ws_pair().await;
        let open = ask(
            AskTarget::Request("srq-clarify".into()),
            AskKind::Clarify,
            json!({"questions": [
                {"qid": "q1", "question": "Which database?"},
                {"qid": "q2", "question": "Which region?", "choices": ["eu", "us"]}
            ]}),
        );
        let (result, settled) = answer_result(
            &open,
            HermesAskReply::Clarify {
                answers: vec![
                    HermesClarifyAnswer {
                        qid: "q1".into(),
                        answer: Some("postgres".into()),
                    },
                    HermesClarifyAnswer {
                        qid: "q2".into(),
                        answer: None,
                    },
                ],
                cancelled: false,
            },
        );
        send_response(&mut client, &open.target, result, "live-1")
            .await
            .expect("answer");

        let frame = next_text(&mut server).await;
        assert_eq!(frame["result"]["answers"]["q1"], "postgres");
        // A skipped question is `null`, which is what the clarify tool reads as "not answered".
        assert!(frame["result"]["answers"]["q2"].is_null());
        assert_eq!(settled["outcome"], "submitted");
        assert_eq!(settled["count"], 1);
    }

    #[tokio::test]
    async fn a_dismissed_clarify_cancels_instead_of_answering() {
        let open = ask(
            AskTarget::Request("srq-clarify".into()),
            AskKind::Clarify,
            json!({"questions": [{"qid": "q1", "question": "Which database?"}]}),
        );
        let (result, settled) = answer_result(
            &open,
            HermesAskReply::Clarify {
                answers: Vec::new(),
                cancelled: true,
            },
        );
        assert_eq!(result, json!({}));
        assert_eq!(settled["outcome"], "cancelled");
    }

    #[tokio::test]
    async fn a_secret_prompt_answers_a_masked_value() {
        let (mut client, mut server) = ws_pair().await;
        let open = ask(
            AskTarget::Request("srq-secret".into()),
            AskKind::Secret,
            json!({"env_var": "OPENAI_API_KEY", "prompt": "Paste the key"}),
        );
        let (result, settled) = answer_result(
            &open,
            HermesAskReply::Secret {
                value: "sk-test".into(),
            },
        );
        send_response(&mut client, &open.target, result, "live-1")
            .await
            .expect("answer");
        assert_eq!(next_text(&mut server).await["result"]["value"], "sk-test");
        assert_eq!(settled["outcome"], "submitted");
        // The note names the variable, never the secret itself.
        assert_eq!(settled["summary"], "OPENAI_API_KEY");
        assert!(!settled.to_string().contains("sk-test"));

        let skipped = ask(
            AskTarget::Request("srq-secret-2".into()),
            AskKind::Secret,
            json!({"env_var": "OPENAI_API_KEY"}),
        );
        let (result, settled) = answer_result(
            &skipped,
            HermesAskReply::Secret {
                value: String::new(),
            },
        );
        assert_eq!(result, json!({"value": ""}));
        assert_eq!(settled["outcome"], "skipped");
    }

    /// A bridge tool gets the renderer's "no window shows this chat" decline; anything else says
    /// "no handler". Either way the request settles at once instead of parking the agent.
    #[tokio::test]
    async fn requests_this_build_cannot_serve_are_refused_fast() {
        let (mut client, mut server) = ws_pair().await;
        refuse_request(&mut client, "srq-preview", "preview.read")
            .await
            .expect("refuse");
        let frame = next_text(&mut server).await;
        assert_eq!(frame["error"]["code"], NOT_SHOWN_CODE);

        refuse_request(&mut client, "srq-setup", "setup_beat")
            .await
            .expect("refuse");
        let frame = next_text(&mut server).await;
        assert_eq!(frame["error"]["code"], -32601);
    }

    #[tokio::test]
    async fn the_turn_policy_decides_approvals_without_a_card() {
        let (mut client, mut server) = ws_pair().await;
        // Full control: approve silently, no card and no transcript note.
        let silent = open_ask(
            &mut client,
            None,
            AskTarget::Request("srq-a".into()),
            AskKind::Approval,
            json!({"command": "rm -rf build"}),
            &AskPolicy {
                ask_user: false,
                full_control: true,
            },
            "live-1",
            "turn-1",
        )
        .await
        .expect("open");
        assert!(silent.is_none());
        assert_eq!(next_text(&mut server).await["result"]["choice"], "once");

        // Confirmed writes reach the user instead of being decided here.
        let card = open_ask(
            &mut client,
            None,
            AskTarget::Request("srq-b".into()),
            AskKind::Approval,
            json!({"command": "rm -rf build"}),
            &AskPolicy {
                ask_user: true,
                full_control: false,
            },
            "live-1",
            "turn-1",
        )
        .await
        .expect("open");
        assert!(card.is_some());
    }

    /// A clarify question is not a policy decision: it reaches the user even in full-control mode.
    #[tokio::test]
    async fn clarify_and_secret_prompts_ignore_the_approval_policy() {
        let (mut client, _server) = ws_pair().await;
        for (kind, params) in [
            (
                AskKind::Clarify,
                json!({"questions": [{"qid": "q1", "question": "Which database?"}]}),
            ),
            (AskKind::Secret, json!({"env_var": "TOKEN"})),
        ] {
            let card = open_ask(
                &mut client,
                None,
                AskTarget::Request(format!("srq-{}", kind.wire())),
                kind,
                params,
                &AskPolicy {
                    ask_user: false,
                    full_control: true,
                },
                "live-1",
                "turn-1",
            )
            .await
            .expect("open");
            assert!(card.is_some(), "{} must reach the user", kind.wire());
        }
    }

    /// `hermes serve` pings every 20s (uvicorn's default `ws_ping_interval`); the runtime used to
    /// fail the whole turn with "unexpected frame" when that first ping landed mid-stream.
    #[tokio::test]
    async fn skips_transport_control_frames() {
        let listener = tokio::net::TcpListener::bind(("127.0.0.1", 0))
            .await
            .expect("bind");
        let addr = listener.local_addr().expect("local addr");
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.expect("accept");
            let mut socket = tokio_tungstenite::accept_async(stream)
                .await
                .expect("handshake");
            socket
                .send(Message::Ping(vec![1, 2].into()))
                .await
                .expect("ping");
            socket
                .send(Message::Pong(vec![3].into()))
                .await
                .expect("pong");
            socket
                .send(Message::Text(
                    json!({"jsonrpc": "2.0", "id": 1, "result": {"ok": true}})
                        .to_string()
                        .into(),
                ))
                .await
                .expect("result");
            // Drain the client's automatic pong so its read path flushes the reply.
            let _ = socket.next().await;
        });
        let (mut socket, _) = connect_async(format!("ws://{addr}/api/ws"))
            .await
            .expect("connect");
        let value = recv_json(&mut socket)
            .await
            .expect("control frames keep the stream alive");
        assert_eq!(value["result"]["ok"], true);
        server.abort();
    }

    #[test]
    fn login_path_normalizes_to_the_ws_endpoint() {
        assert_eq!(
            remote_ws_url_auth("http://panel.tail1234.ts.net:9119/login", "ticket=abc").unwrap(),
            "ws://panel.tail1234.ts.net:9119/api/ws?ticket=abc"
        );
    }

    #[test]
    fn keeps_a_panel_prefix_when_building_the_ws_endpoint() {
        assert_eq!(
            remote_ws_url_auth("https://host/hermes", "ticket=abc").unwrap(),
            "wss://host/hermes/api/ws?ticket=abc"
        );
        assert_eq!(
            remote_ws_url_auth("https://host/hermes/", "ticket=abc").unwrap(),
            "wss://host/hermes/api/ws?ticket=abc"
        );
        assert_eq!(
            remote_ws_url_auth("https://host/hermes/api/ws", "ticket=abc").unwrap(),
            "wss://host/hermes/api/ws?ticket=abc"
        );
        assert_eq!(
            remote_ws_url_auth("https://host/hermes/login", "ticket=abc").unwrap(),
            "wss://host/hermes/api/ws?ticket=abc"
        );
    }

    #[tokio::test]
    #[ignore = "requires HERMES_RUNTIME_BIN, HERMES_TEST_ENDPOINT, HERMES_TEST_API_KEY and HERMES_TEST_MODEL"]
    async fn gateway_resumes_a_stored_session() {
        let bin = std::env::var("HERMES_RUNTIME_BIN").expect("HERMES_RUNTIME_BIN");
        let endpoint = std::env::var("HERMES_TEST_ENDPOINT").expect("HERMES_TEST_ENDPOINT");
        let key = std::env::var("HERMES_TEST_API_KEY").expect("HERMES_TEST_API_KEY");
        let model = std::env::var("HERMES_TEST_MODEL").unwrap_or_else(|_| "gpt-4o-mini".into());
        let provider = std::env::var("HERMES_TEST_PROVIDER").unwrap_or_else(|_| "openai".into());
        let dir = tempfile::tempdir().unwrap();
        let input = HermesChatInput {
            prompt: "The secret word is ORBITAL. Remember it and reply with OK.".into(),
            request_id: "gateway-resume-1".into(),
            session_id: None,
            provider: provider.clone(),
            model: model.clone(),
            effort: "low".into(),
            fast: false,
            cwd: None,
            attachments: Vec::new(),
            auxiliary: Vec::new(),
            force_aux_vision: false,
            gateway_url: None,
            profile_id: None,
            profile: None,
            panel_auth: false,
            api_key: Some(key.clone()),
            endpoint: Some(endpoint.clone()),
            mac_enabled: false,
            control_level: "open".into(),
            mac_terminal: true,
            mac_screen: true,
            mac_files: true,
            mac_cron: true,
            confirm_writes: false,
        };
        let (tx, _rx) = tokio::sync::mpsc::unbounded_channel();
        let first = chat_for_test(Path::new(&bin), dir.path(), input, tx)
            .await
            .unwrap();
        let stored = first.session_id.expect("stored session id");
        let second = HermesChatInput {
            prompt: "What was the secret word? Reply with only the word.".into(),
            request_id: "gateway-resume-2".into(),
            session_id: Some(stored),
            provider: provider.clone(),
            model,
            effort: "low".into(),
            fast: false,
            cwd: None,
            attachments: Vec::new(),
            auxiliary: Vec::new(),
            force_aux_vision: false,
            gateway_url: None,
            profile_id: None,
            profile: None,
            panel_auth: false,
            api_key: Some(key),
            endpoint: Some(endpoint),
            mac_enabled: false,
            control_level: "open".into(),
            mac_terminal: true,
            mac_screen: true,
            mac_files: true,
            mac_cron: true,
            confirm_writes: false,
        };
        let (tx2, _rx2) = tokio::sync::mpsc::unbounded_channel();
        let reply = chat_for_test(Path::new(&bin), dir.path(), second, tx2)
            .await
            .unwrap();
        assert!(
            reply.text.to_uppercase().contains("ORBITAL"),
            "resume lost context: {}",
            reply.text
        );
    }

    #[tokio::test]
    #[ignore = "requires HERMES_RUNTIME_BIN, HERMES_TEST_ENDPOINT, HERMES_TEST_API_KEY and HERMES_TEST_MODEL"]
    async fn gateway_attaches_a_text_file() {
        let bin = std::env::var("HERMES_RUNTIME_BIN").expect("HERMES_RUNTIME_BIN");
        let endpoint = std::env::var("HERMES_TEST_ENDPOINT").expect("HERMES_TEST_ENDPOINT");
        let key = std::env::var("HERMES_TEST_API_KEY").expect("HERMES_TEST_API_KEY");
        let model = std::env::var("HERMES_TEST_MODEL").unwrap_or_else(|_| "gpt-4o-mini".into());
        let provider = std::env::var("HERMES_TEST_PROVIDER").unwrap_or_else(|_| "openai".into());
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("attachment.txt");
        std::fs::write(&file, "HERMES_ATTACHMENT_OK").unwrap();
        let input = HermesChatInput {
            prompt: "Read the attached file and reply with exactly its contents.".into(),
            request_id: "gateway-attachment-test".into(),
            session_id: None,
            provider,
            model,
            effort: "low".into(),
            fast: false,
            cwd: None,
            attachments: vec![file.display().to_string()],
            auxiliary: Vec::new(),
            force_aux_vision: false,
            gateway_url: None,
            profile_id: None,
            profile: None,
            panel_auth: false,
            api_key: Some(key),
            endpoint: Some(endpoint),
            mac_enabled: false,
            control_level: "open".into(),
            mac_terminal: true,
            mac_screen: true,
            mac_files: true,
            mac_cron: true,
            confirm_writes: false,
        };
        let (tx, _rx) = tokio::sync::mpsc::unbounded_channel();
        let reply = chat_for_test(Path::new(&bin), dir.path(), input, tx)
            .await
            .unwrap();
        assert!(
            reply.text.contains("HERMES_ATTACHMENT_OK"),
            "attachment not read: {}",
            reply.text
        );
    }

    #[tokio::test]
    #[ignore = "requires HERMES_RUNTIME_BIN, HERMES_TEST_ENDPOINT, HERMES_TEST_API_KEY and HERMES_TEST_MODEL"]
    async fn gateway_terminal_control_runs_on_the_mac() {
        let bin = std::env::var("HERMES_RUNTIME_BIN").expect("HERMES_RUNTIME_BIN");
        let endpoint = std::env::var("HERMES_TEST_ENDPOINT").expect("HERMES_TEST_ENDPOINT");
        let key = std::env::var("HERMES_TEST_API_KEY").expect("HERMES_TEST_API_KEY");
        let model = std::env::var("HERMES_TEST_MODEL").unwrap_or_else(|_| "gpt-4o-mini".into());
        let provider = std::env::var("HERMES_TEST_PROVIDER").unwrap_or_else(|_| "openai".into());
        let dir = tempfile::tempdir().unwrap();
        let input = HermesChatInput {
            prompt: "Use the terminal tool to run exactly: echo HERMES_TERMINAL_OK. Then tell me the output.".into(),
            request_id: "gateway-terminal-test".into(),
            session_id: None,
            provider: provider.clone(),
            model,
            effort: "low".into(),
            fast: false,
            cwd: None,
            attachments: Vec::new(),
            auxiliary: Vec::new(),
            force_aux_vision: false,
            gateway_url: None,
            profile_id: None,
        profile: None,
        panel_auth: false,
            api_key: Some(key),
            endpoint: Some(endpoint),
            mac_enabled: true,
            control_level: "full".into(),
            mac_terminal: true,
            mac_screen: true,
            mac_files: true,
            mac_cron: true,
            confirm_writes: false,
        };
        let (tx, _rx) = tokio::sync::mpsc::unbounded_channel();
        let reply = chat_for_test(Path::new(&bin), dir.path(), input, tx)
            .await
            .unwrap();
        assert!(
            reply.text.contains("HERMES_TERMINAL_OK"),
            "terminal output missing: {}",
            reply.text
        );
    }

    #[tokio::test]
    #[ignore = "requires HERMES_RUNTIME_BIN"]
    async fn loads_the_official_model_catalog() {
        let bin = std::env::var("HERMES_RUNTIME_BIN").expect("HERMES_RUNTIME_BIN");
        let dir = tempfile::tempdir().unwrap();
        let input = HermesChatInput {
            prompt: String::new(),
            request_id: "model-options-test".into(),
            session_id: None,
            provider: "copilot".into(),
            model: "gpt-5.4".into(),
            effort: "medium".into(),
            fast: false,
            cwd: None,
            attachments: Vec::new(),
            auxiliary: Vec::new(),
            force_aux_vision: false,
            gateway_url: None,
            profile_id: None,
            profile: None,
            panel_auth: false,
            api_key: None,
            endpoint: None,
            mac_enabled: false,
            control_level: "open".into(),
            mac_terminal: true,
            mac_screen: true,
            mac_files: true,
            mac_cron: true,
            confirm_writes: false,
        };
        let state = HermesRuntimeState::default();
        let options = model_options(Path::new(&bin), dir.path(), &state, input, true, true)
            .await
            .unwrap();
        assert!(!options.providers.is_empty());
        assert!(
            options
                .providers
                .iter()
                .any(|provider| !provider.slug.is_empty())
        );
    }

    #[tokio::test]
    #[ignore = "requires HERMES_RUNTIME_BIN, HERMES_TEST_ENDPOINT, HERMES_TEST_API_KEY and HERMES_TEST_MODEL"]
    async fn streams_real_hermes_gateway_chunks() {
        let bin = std::env::var("HERMES_RUNTIME_BIN").expect("HERMES_RUNTIME_BIN");
        let endpoint = std::env::var("HERMES_TEST_ENDPOINT").expect("HERMES_TEST_ENDPOINT");
        let key = std::env::var("HERMES_TEST_API_KEY").expect("HERMES_TEST_API_KEY");
        let model = std::env::var("HERMES_TEST_MODEL").unwrap_or_else(|_| "gpt-4o-mini".into());
        let provider = std::env::var("HERMES_TEST_PROVIDER").unwrap_or_else(|_| "openai".into());
        let dir = tempfile::tempdir().unwrap();
        let input = HermesChatInput {
            prompt: "Write a 60-word plain-text explanation of why the sky is blue. Do not use markdown.".into(),
            request_id: "gateway-stream-test".into(),
            session_id: None,
            provider: provider.clone(),
            model,
            effort: "low".into(),
            fast: false,
            cwd: None,
            attachments: Vec::new(),
            auxiliary: Vec::new(),
            force_aux_vision: false,
            gateway_url: None,
            profile_id: None,
        profile: None,
        panel_auth: false,
            api_key: Some(key),
            endpoint: Some(endpoint),
            mac_enabled: false,
            control_level: "open".into(),
            mac_terminal: true,
            mac_screen: true,
            mac_files: true,
            mac_cron: true,
            confirm_writes: false,
        };
        let (tx, mut rx) = tokio::sync::mpsc::unbounded_channel();
        let reply = chat_for_test(Path::new(&bin), dir.path(), input, tx)
            .await
            .unwrap();
        let mut chunks = Vec::new();
        while let Some(delta) = rx.recv().await {
            chunks.push(delta);
        }
        assert!(chunks.len() > 2, "expected gateway deltas, got {chunks:?}");
        assert!(!reply.text.trim().is_empty());
        assert!(reply.session_id.is_some());
    }
}
