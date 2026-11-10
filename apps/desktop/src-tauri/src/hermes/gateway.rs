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
use tokio::time::{Instant, sleep, timeout};
use tokio_tungstenite::tungstenite::Message;
use tokio_tungstenite::{MaybeTlsStream, WebSocketStream, connect_async};

use super::{
    AppError, HermesChatInput, HermesChatReply, HermesChunk, HermesModelOptions,
    HermesRuntimeState, HermesUsage, append_audit, clean_output, kill_pid, provider_id,
    reasoning_effort, scrub_credential_env, toolsets, write_config,
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

fn server_fingerprint(input: &HermesChatInput, key: Option<&str>, env_cleaned: bool) -> String {
    format!(
        "{}|{}|{}|{}|{}|{}|{}",
        provider_id(&input.provider),
        input.endpoint.as_deref().unwrap_or(""),
        key.unwrap_or(""),
        toolsets(input),
        input
            .auxiliary
            .iter()
            .filter(|entry| entry.provider != "auto" && !entry.model.is_empty())
            .map(|entry| format!("{}:{}:{}", entry.task, entry.provider, entry.model))
            .collect::<Vec<_>>()
            .join(","),
        input.force_aux_vision,
        // A migrated `.env` must not keep its secret in an already-running gateway.
        if env_cleaned { "env-cleaned" } else { "" }
    )
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
    env_cleaned: bool,
) -> Result<Arc<GatewayServer>, AppError> {
    let fingerprint = server_fingerprint(input, api_key, env_cleaned);
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
    command
        .arg("serve")
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
    scrub_credential_env(&mut command);
    let secret = match (api_key, super::secret_env_var(&input.provider)) {
        (Some(key), Some(name)) => Some(super::attach_secret(&mut command, name, key)?),
        _ => None,
    };
    let child = command.spawn().map_err(|error| {
        AppError::new("E_HERMES_GATEWAY", "Hermes gateway could not start.")
            .with_detail(error.to_string())
    })?;
    drop(secret);
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

async fn recv_json(socket: &mut Socket) -> Result<Value, AppError> {
    let message = timeout(MESSAGE_TIMEOUT, socket.next())
        .await
        .map_err(|_| AppError::new("E_HERMES_GATEWAY_TIMEOUT", "Hermes gateway timed out."))?
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
        Message::Text(text) => serde_json::from_str(text.as_str()).map_err(|error| {
            AppError::new(
                "E_HERMES_GATEWAY_STREAM",
                "Hermes gateway sent invalid JSON.",
            )
            .with_detail(error.to_string())
        }),
        Message::Binary(bytes) => serde_json::from_slice(&bytes).map_err(|error| {
            AppError::new(
                "E_HERMES_GATEWAY_STREAM",
                "Hermes gateway sent invalid JSON.",
            )
            .with_detail(error.to_string())
        }),
        _ => Err(AppError::new(
            "E_HERMES_GATEWAY_STREAM",
            "Hermes gateway sent an unexpected frame.",
        )),
    }
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
) -> Result<(String, String), AppError> {
    let provider = provider_id(&input.provider);
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
            return Ok((live.to_owned(), stored_id));
        }
    }
    let cwd = input
        .cwd
        .as_deref()
        .map(str::trim)
        .filter(|dir| !dir.is_empty() && Path::new(dir).is_dir())
        .map(str::to_owned)
        .unwrap_or_else(|| std::env::var("HOME").unwrap_or_else(|_| home.display().to_string()));
    let mut params = json!({
        "cols": 120,
        "title": input.prompt.chars().take(60).collect::<String>(),
        "source": "desktop",
        "cwd": cwd,
        "model": model,
        "provider": provider,
        "fast": input.fast
    });
    if let Some(effort) = effort {
        params["reasoning_effort"] = Value::String(effort.to_owned());
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
    Ok((live, stored))
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
    let (api_key, env_cleaned) = write_config(home, &input)?;
    let server = ensure_server(bin, home, state, &input, api_key.as_deref(), env_cleaned).await?;
    let url = format!(
        "ws://127.0.0.1:{}/api/ws?token={}",
        server.port, server.token
    );
    let (mut socket, _) = connect_async(url).await.map_err(|error| {
        AppError::new(
            "E_HERMES_GATEWAY_STREAM",
            "Could not connect to the Hermes gateway.",
        )
        .with_detail(error.to_string())
    })?;
    let (live_sid, stored_sid) = open_session(&mut socket, &input, home).await?;
    if let Some(app) = app {
        let _ = app.emit(
            "hermes-session",
            json!({"requestId": input.request_id, "liveSessionId": live_sid, "sessionId": stored_sid}),
        );
    }
    let full_control = input.mac_enabled && input.control_level == "full" && !input.confirm_writes;
    if full_control {
        let _ = rpc(
            &mut socket,
            2,
            "config.set",
            json!({"session_id": live_sid, "key": "yolo", "value": true, "scope": "session"}),
        )
        .await;
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

    let mut deadline = Instant::now() + MESSAGE_TIMEOUT;
    let mut accumulated = String::new();
    let mut approval_id = 0_u64;
    loop {
        if Instant::now() >= deadline {
            let _ = send_json(
                &mut socket,
                json!({"jsonrpc": "2.0", "id": 99, "method": "session.interrupt", "params": {"session_id": live_sid}}),
            )
            .await;
            return Err(AppError::new(
                "E_HERMES_GATEWAY_TIMEOUT",
                "Hermes took too long to answer.",
            ));
        }
        let value = recv_json(&mut socket).await?;
        if let Some(error) = rpc_error(&value)
            && value.get("id").and_then(Value::as_u64) == Some(4)
        {
            append_audit(home, &input, "failed", Some(&stored_sid), Some(&error));
            return Err(AppError::new("E_HERMES_GATEWAY_STREAM", error));
        }
        let Some(params) = value.get("params") else {
            continue;
        };
        let event = params
            .get("type")
            .and_then(Value::as_str)
            .unwrap_or_default();
        let payload = params.get("payload").cloned().unwrap_or(Value::Null);
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
                if let Some(sink) = &sink {
                    let _ = sink.send(delta.to_owned());
                }
                if let Some(app) = app {
                    use tauri::Emitter;
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
                deadline = Instant::now() + MESSAGE_TIMEOUT;
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
                deadline = Instant::now() + MESSAGE_TIMEOUT;
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
                deadline = Instant::now() + MESSAGE_TIMEOUT;
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
                    append_audit(home, &input, "failed", Some(&stored_sid), Some(&text));
                    return Err(AppError::new("E_HERMES_GATEWAY_STREAM", text));
                }
                append_audit(home, &input, "succeeded", Some(&stored_sid), None);
                let usage = payload
                    .get("usage")
                    .cloned()
                    .and_then(|value| serde_json::from_value::<HermesUsage>(value).ok());
                return Ok(HermesChatReply {
                    text: clean_output(&text),
                    session_id: Some(stored_sid),
                    live_session_id: Some(live_sid),
                    usage,
                });
            }
            "error" => {
                let message = payload
                    .get("message")
                    .and_then(Value::as_str)
                    .unwrap_or("Hermes gateway returned an error.")
                    .to_owned();
                append_audit(home, &input, "failed", Some(&stored_sid), Some(&message));
                return Err(AppError::new("E_HERMES_GATEWAY_STREAM", message));
            }
            "approval.request" => {
                approval_id += 1;
                let request_id = payload
                    .get("request_id")
                    .and_then(Value::as_str)
                    .unwrap_or_default()
                    .to_owned();
                let choice = if input.confirm_writes {
                    if let Some(app) = app {
                        let _ = app.emit("hermes-approval", payload.clone());
                    }
                    let wait_started = Instant::now();
                    let (approval_tx, mut approval_rx) =
                        tokio::sync::mpsc::unbounded_channel::<String>();
                    *state.approval.lock().await = Some(approval_tx);
                    let selected = tokio::select! {
                        selected = approval_rx.recv() => selected.unwrap_or_else(|| "deny".to_owned()),
                        _ = state.approval_cancel.notified() => "deny".to_owned(),
                        _ = sleep(Duration::from_secs(120)) => "deny".to_owned(),
                    };
                    *state.approval.lock().await = None;
                    // Human approval does not consume the assistant's turn budget.
                    deadline += wait_started.elapsed();
                    selected
                } else if full_control {
                    "once".to_owned()
                } else {
                    "deny".to_owned()
                };
                let mut params = json!({"session_id": live_sid, "choice": choice});
                if !request_id.is_empty() {
                    params["request_id"] = Value::String(request_id);
                }
                let _ = send_json(
                    &mut socket,
                    json!({"jsonrpc": "2.0", "id": 100 + approval_id, "method": "approval.respond", "params": params}),
                )
                .await;
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
    state.approval_cancel.notify_waiters();
    let _ = state.approval.lock().await.take();
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

pub async fn steer(
    state: &HermesRuntimeState,
    live_session_id: &str,
    text: &str,
) -> Result<(), AppError> {
    if text.trim().is_empty() {
        return Err(AppError::new("E_HERMES_INPUT", "The message is empty."));
    }
    let mut socket = connect_running(state).await?;
    let result = rpc(
        &mut socket,
        1,
        "session.steer",
        json!({"session_id": live_session_id, "text": text}),
    )
    .await?;
    if result.get("status").and_then(Value::as_str) == Some("rejected") {
        return Err(AppError::new(
            "E_HERMES_STEER",
            "The running agent did not accept the steering message.",
        ));
    }
    Ok(())
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
    let (api_key, env_cleaned) = write_config(home, &input)?;
    let server = ensure_server(bin, home, state, &input, api_key.as_deref(), env_cleaned).await?;
    let url = format!(
        "ws://127.0.0.1:{}/api/ws?token={}",
        server.port, server.token
    );
    let (mut socket, _) = connect_async(url).await.map_err(|error| {
        AppError::new(
            "E_HERMES_GATEWAY_STREAM",
            "Could not connect to the Hermes gateway.",
        )
        .with_detail(error.to_string())
    })?;
    let result = tokio::time::timeout(
        std::time::Duration::from_secs(45),
        rpc(
            &mut socket,
            1,
            "model.options",
            json!({
                "include_unconfigured": include_unconfigured,
                "refresh": refresh
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
