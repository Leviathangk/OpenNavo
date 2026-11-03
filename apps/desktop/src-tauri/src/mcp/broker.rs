//! App-side Unix socket broker for Local MCP.
//!
//! The broker is installed during Tauri setup and only binds when the persisted
//! master switch is on. Turning the switch off stops the accept loop, removes
//! the socket immediately, shuts down every live connection and clears the
//! per-connection session set. Broker failures are returned to Settings but
//! never block app startup.
//!
//! The internal protocol is newline-delimited JSON:
//! `{"v":1,"id":"...","method":"tools/call","params":{...}}` and
//! `{"v":1,"id":"...","ok":true,"result":...}` or `ok:false,error`.

use std::{
    collections::{HashMap, HashSet},
    io::{self, BufRead, BufReader, ErrorKind, Write},
    os::unix::net::{UnixListener, UnixStream},
    path::PathBuf,
    sync::{
        Arc, Mutex, Weak,
        atomic::{AtomicBool, AtomicU64, Ordering},
    },
    thread::{self, JoinHandle},
    time::Duration,
};

use serde::Serialize;
use serde_json::{Value, json};

use super::{
    socket, state,
    tools::{ToolError, ToolHost},
};

/// Maximum accepted internal request line.
pub const MAX_REQUEST_BYTES: usize = 1024 * 1024;
/// Maximum internal response line.
pub const MAX_RESPONSE_BYTES: usize = 8 * 1024 * 1024;
/// At most eight concurrent clients.
pub const MAX_CONNECTIONS: usize = 8;
/// Internal protocol version.
pub const RPC_VERSION: u64 = 1;
/// Accept polling interval; keeps shutdown responsive without a self-pipe.
const ACCEPT_POLL: Duration = Duration::from_millis(20);

/// Broker status surfaced by `mcp_status`.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct McpStatus {
    pub enabled: bool,
    /// Write-tool switch; always `false` while `enabled` is off.
    pub write_enabled: bool,
    pub running: bool,
    pub socket_path: Option<String>,
    pub error: Option<String>,
    /// Absolute path of the running app executable, for client config presets.
    pub command_path: Option<String>,
    /// Whether the volume holding `command_path` compares names case-sensitively. The settings page needs
    /// this to tell a real move from a case-only spelling of the same path.
    pub case_sensitive: bool,
}

/// Whether the volume that holds `path` compares names case-sensitively (`/Volumes` entries can be either
/// kind). A volume that cannot be asked falls back to the macOS default, case-insensitive.
#[cfg(target_os = "macos")]
fn volume_case_sensitive(path: &std::path::Path) -> bool {
    use std::os::unix::ffi::OsStrExt;
    let Ok(raw) = std::ffi::CString::new(path.as_os_str().as_bytes()) else {
        return false;
    };
    unsafe { nix::libc::pathconf(raw.as_ptr(), nix::libc::_PC_CASE_SENSITIVE) == 1 }
}
#[cfg(not(target_os = "macos"))]
fn volume_case_sensitive(_: &std::path::Path) -> bool {
    false
}

/// Broker operation failure with a stable code.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct BrokerError {
    pub code: &'static str,
    pub message: String,
}

impl BrokerError {
    fn new(code: &'static str, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
        }
    }

    fn io(error: io::Error) -> Self {
        Self::new("E_MCP_BROKER", error.to_string())
    }
}

impl std::fmt::Display for BrokerError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(formatter, "{}: {}", self.code, self.message)
    }
}

impl std::error::Error for BrokerError {}

#[derive(Default)]
struct Inner {
    enabled: bool,
    write_enabled: bool,
    running: bool,
    path: Option<PathBuf>,
    error: Option<String>,
    stop: Option<Arc<AtomicBool>>,
    thread: Option<JoinHandle<()>>,
    connections: HashMap<u64, UnixStream>,
    sessions: HashSet<u64>,
    /// Never reset, so ids stay unique across disable/enable cycles.
    next_connection: u64,
    /// Identity of the socket file this broker published.
    socket_id: Option<socket::SocketId>,
}

/// Shared broker handle. Tauri manages `Arc<Broker>`.
/// Longest a switch-off waits for an in-flight write to return.
const WRITE_DRAIN_TIMEOUT: Duration = Duration::from_secs(5);

pub struct Broker {
    data_dir: PathBuf,
    host: Option<Arc<dyn ToolHost>>,
    inner: Mutex<Inner>,
    /// Serializes whole switch transitions (bind/persist/stop), so runtime
    /// state and the persisted switch cannot diverge under concurrent toggles.
    lifecycle: Mutex<()>,
    /// Set while a write tool runs (including its dialog).
    writing: AtomicBool,
    /// Bumped whenever a switch turns off; an approval from an older
    /// generation is void even if the switches are back on.
    write_generation: AtomicU64,
    /// Executable a socket peer must run before it may call write tools.
    trusted_client: Option<PathBuf>,
}

impl Broker {
    /// Broker without tools, used by socket-only tests and diagnostics.
    pub fn new(data_dir: PathBuf) -> Self {
        Self::with_host(data_dir, None)
    }

    /// Broker with the app-side read-tool implementation.
    pub fn with_host(data_dir: PathBuf, host: Option<Arc<dyn ToolHost>>) -> Self {
        let persisted = state::load(&data_dir);
        Self {
            data_dir,
            host,
            inner: Mutex::new(Inner {
                enabled: persisted.enabled,
                write_enabled: persisted.write_enabled,
                ..Inner::default()
            }),
            lifecycle: Mutex::new(()),
            writing: AtomicBool::new(false),
            write_generation: AtomicU64::new(0),
            // B1: clients run this same executable as `<app binary> mcp`.
            trusted_client: std::env::current_exe().ok(),
        }
    }

    /// Replace the executable a write-capable peer must run (tests).
    pub fn with_trusted_client(mut self, path: Option<PathBuf>) -> Self {
        self.trusted_client = path;
        self
    }

    /// Whether a write on `connection` may proceed: running, both switches
    /// on, and the connection still registered.
    pub fn write_valid(&self, connection: u64) -> bool {
        let inner = self.lock();
        inner.running
            && inner.enabled
            && inner.write_enabled
            && inner.connections.contains_key(&connection)
    }

    /// Withdraw any write in flight (including one whose dialog is still
    /// open) and wait, bounded, until it has returned. Called after a switch
    /// is turned off, with no broker lock held.
    fn drain_writes(&self) {
        self.write_generation.fetch_add(1, Ordering::AcqRel);
        if let Some(host) = &self.host {
            host.cancel_writes();
        }
        let deadline = std::time::Instant::now() + WRITE_DRAIN_TIMEOUT;
        while self.writing.load(Ordering::Acquire) && std::time::Instant::now() < deadline {
            std::thread::sleep(Duration::from_millis(5));
        }
    }

    /// The write audit log next to the persisted state.
    pub fn audit(&self) -> super::audit::Audit {
        super::audit::Audit::new(&self.data_dir)
    }

    /// Current status for Settings and diagnostics.
    pub fn status(&self) -> McpStatus {
        let inner = self.lock();
        let exe = std::env::current_exe().ok();
        McpStatus {
            enabled: inner.enabled,
            write_enabled: inner.enabled && inner.write_enabled,
            running: inner.running,
            socket_path: inner
                .path
                .as_ref()
                .map(|path| path.to_string_lossy().into_owned()),
            error: inner.error.clone(),
            command_path: exe.as_ref().map(|path| path.to_string_lossy().into_owned()),
            case_sensitive: exe.as_deref().is_some_and(volume_case_sensitive),
        }
    }

    /// Bind at startup iff the persisted switch is on.
    pub fn start_if_enabled(self: &Arc<Self>) -> Result<(), BrokerError> {
        let _transition = self.transition();
        if self.status().enabled {
            self.enable()
        } else {
            // The switch is off: never create or accept on a socket. A socket
            // left by a crashed previous run is safe to clean only when stale,
            // owned by this user and still a socket.
            if let Err(error) = socket::remove_stale(&self.data_dir) {
                log::warn!("Local MCP stale socket cleanup failed: {error}");
            }
            Ok(())
        }
    }

    /// Apply the master switch immediately and persist it.
    pub fn set_enabled(self: &Arc<Self>, enabled: bool) -> Result<(), BrokerError> {
        let _transition = self.transition();
        if enabled {
            self.enable()
        } else {
            self.disable()?;
            self.drain_writes();
            Ok(())
        }
    }

    fn enable(self: &Arc<Self>) -> Result<(), BrokerError> {
        let mut inner = self.lock();
        if inner.running {
            inner.enabled = true;
            inner.error = None;
            return state::save_state(&self.data_dir, self.persisted(&inner, true))
                .map_err(BrokerError::io);
        }
        let previous_enabled = inner.enabled;

        let resolved = socket::prepare(&self.data_dir).map_err(|error| {
            let failure = BrokerError::io(error);
            inner.error = Some(failure.to_string());
            failure
        })?;
        let (listener, socket_id) = socket::bind_owned(&resolved).map_err(|error| {
            let failure = BrokerError::io(error);
            inner.error = Some(failure.to_string());
            failure
        })?;

        if let Err(error) = state::save_state(&self.data_dir, self.persisted(&inner, true)) {
            drop(listener);
            socket::remove_if_same(&self.data_dir, socket_id);
            let failure = BrokerError::io(error);
            inner.error = Some(failure.to_string());
            return Err(failure);
        }

        let stop = Arc::new(AtomicBool::new(false));
        inner.enabled = true;
        inner.running = true;
        inner.path = Some(resolved.path);
        inner.error = None;
        inner.stop = Some(stop.clone());
        inner.socket_id = Some(socket_id);

        let weak = Arc::downgrade(self);
        let stop_thread = stop.clone();
        let host = self.host.clone();
        match thread::Builder::new()
            .name("opennavo-mcp-broker".into())
            .spawn(move || accept_loop(weak, listener, stop_thread, host))
        {
            Ok(handle) => {
                inner.thread = Some(handle);
                Ok(())
            }
            Err(error) => {
                inner.enabled = previous_enabled;
                inner.running = false;
                inner.path = None;
                inner.stop = None;
                inner.socket_id = None;
                let failure = BrokerError::io(error);
                inner.error = Some(failure.to_string());
                let _ = state::save_state(&self.data_dir, self.persisted(&inner, previous_enabled));
                socket::remove_if_same(&self.data_dir, socket_id);
                Err(failure)
            }
        }
    }

    fn disable(&self) -> Result<(), BrokerError> {
        // Persist "off" before stopping anything. If the write fails, the
        // broker keeps running and reports the error, so the runtime and the
        // persisted switch never disagree and the next launch cannot silently
        // turn Local MCP back on.
        if let Err(error) = state::save(&self.data_dir, false) {
            let failure = BrokerError::io(error);
            self.lock().error = Some(failure.to_string());
            return Err(failure);
        }
        let thread = {
            let mut inner = self.lock();
            inner.enabled = false;
            inner.write_enabled = false;
            inner.error = None;
            self.stop_runtime(&mut inner)
        };
        join(thread);
        Ok(())
    }

    /// Apply the write-tool switch and persist it. Requires the master switch.
    ///
    /// Like turning Local MCP off, the new value is persisted first; if that
    /// fails nothing changes and the error is reported. Turning writes off
    /// then drops every live connection and clears their sessions, so no
    /// client keeps a session (or anything issued on it) that was opened while
    /// writes were allowed. The listener keeps running for read tools.
    pub fn set_write_enabled(&self, write_enabled: bool) -> Result<(), BrokerError> {
        let _transition = self.transition();
        self.update_write_enabled(write_enabled)?;
        if !write_enabled {
            self.drain_writes();
        }
        Ok(())
    }

    fn update_write_enabled(&self, write_enabled: bool) -> Result<(), BrokerError> {
        let mut inner = self.lock();
        if !inner.enabled {
            return Err(BrokerError::new(
                "E_MCP_DISABLED",
                "Turn on Local MCP before allowing write tools",
            ));
        }
        let next = state::State {
            enabled: true,
            write_enabled,
        };
        if let Err(error) = state::save_state(&self.data_dir, next) {
            let failure = BrokerError::io(error);
            inner.error = Some(failure.to_string());
            return Err(failure);
        }
        inner.write_enabled = write_enabled;
        // Only a running listener has nothing left to report; a failed start (bind/prepare) keeps its
        // error visible until Local MCP actually runs.
        if inner.running {
            inner.error = None;
        }
        if !write_enabled {
            for connection in inner.connections.values() {
                let _ = connection.shutdown(std::net::Shutdown::Both);
            }
            inner.connections.clear();
            inner.sessions.clear();
        }
        Ok(())
    }

    fn persisted(&self, inner: &Inner, enabled: bool) -> state::State {
        state::State {
            enabled,
            write_enabled: enabled && inner.write_enabled,
        }
    }

    /// Stop accepting, drop connections, and clear sessions without changing
    /// the persisted switch (used on app exit).
    pub fn shutdown(&self) {
        let _transition = self.transition();
        let thread = {
            let mut inner = self.lock();
            self.stop_runtime(&mut inner)
        };
        join(thread);
        self.drain_writes();
    }

    /// Stop runtime resources and return the accept thread for joining after
    /// the mutex guard is dropped.
    fn stop_runtime(&self, inner: &mut Inner) -> Option<JoinHandle<()>> {
        if let Some(stop) = inner.stop.take() {
            stop.store(true, Ordering::Release);
        }
        inner.running = false;
        inner.path = None;
        for connection in inner.connections.values() {
            let _ = connection.shutdown(std::net::Shutdown::Both);
        }
        inner.connections.clear();
        inner.sessions.clear();
        if let Some(socket_id) = inner.socket_id.take() {
            socket::remove_if_same(&self.data_dir, socket_id);
        }
        inner.thread.take()
    }

    fn transition(&self) -> std::sync::MutexGuard<'_, ()> {
        self.lifecycle
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, Inner> {
        self.inner
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
    }
}

impl Drop for Broker {
    fn drop(&mut self) {
        let thread = {
            let mut inner = self.lock();
            self.stop_runtime(&mut inner)
        };
        join(thread);
    }
}

fn join(thread: Option<JoinHandle<()>>) {
    if let Some(thread) = thread {
        let _ = thread.join();
    }
}

fn accept_loop(
    weak: Weak<Broker>,
    listener: UnixListener,
    stop: Arc<AtomicBool>,
    host: Option<Arc<dyn ToolHost>>,
) {
    if let Err(error) = listener.set_nonblocking(true) {
        log::warn!("Local MCP accept loop unavailable: {error}");
        return;
    }
    while !stop.load(Ordering::Acquire) {
        match listener.accept() {
            Ok((stream, _)) => {
                if let Err(error) = stream.set_nonblocking(false) {
                    log::warn!("Local MCP connection setup failed: {error}");
                    continue;
                }
                let Some(broker) = weak.upgrade() else {
                    return;
                };
                let peer = match socket::verify_peer(&stream) {
                    Ok(peer) => peer,
                    Err(error) => {
                        log::warn!("Local MCP rejected peer: {error}");
                        continue;
                    }
                };
                let Ok(shutdown) = stream.try_clone() else {
                    continue;
                };
                let Some(id) = broker.register(shutdown) else {
                    let _ = stream.shutdown(std::net::Shutdown::Both);
                    continue;
                };
                let unregister_weak = weak.clone();
                let connection_host = host.clone();
                let spawned = thread::Builder::new()
                    .name(format!("opennavo-mcp-conn-{id}"))
                    .spawn(move || {
                        connection_loop(unregister_weak, id, peer.stamp(), stream, connection_host);
                    });
                if spawned.is_err()
                    && let Some(broker) = weak.upgrade()
                {
                    broker.unregister(id);
                }
            }
            Err(error) if error.kind() == ErrorKind::WouldBlock => {
                thread::sleep(ACCEPT_POLL);
            }
            Err(error) if error.kind() == ErrorKind::Interrupted => {}
            Err(error) => {
                log::warn!("Local MCP accept failed: {error}");
                thread::sleep(ACCEPT_POLL);
            }
        }
    }
    drop(listener);
}

impl Broker {
    fn register(&self, shutdown: UnixStream) -> Option<u64> {
        let mut inner = self.lock();
        if !inner.running || inner.connections.len() >= MAX_CONNECTIONS {
            return None;
        }
        inner.next_connection = inner.next_connection.wrapping_add(1);
        let id = inner.next_connection;
        inner.connections.insert(id, shutdown);
        inner.sessions.insert(id);
        Some(id)
    }

    /// Whether a connection still belongs to the running broker. A worker
    /// from a previous enable cycle (or a disabled broker) gets `false`.
    fn is_active(&self, id: u64) -> bool {
        let inner = self.lock();
        inner.running && inner.connections.contains_key(&id)
    }

    fn unregister(&self, id: u64) {
        let mut inner = self.lock();
        inner.connections.remove(&id);
        inner.sessions.remove(&id);
    }
}

/// Internal reply failure. Codes are owned because tool failures come from
/// `AppError`/`ToolError` rather than a fixed set of literals.
type RpcError = (String, String);

fn rpc_error(code: &str, message: &str) -> RpcError {
    (code.to_owned(), message.to_owned())
}

fn connection_loop(
    weak: Weak<Broker>,
    id: u64,
    peer: Option<super::caller::PidStamp>,
    stream: UnixStream,
    host: Option<Arc<dyn ToolHost>>,
) {
    let connection = id;
    let mut reader = BufReader::new(stream);
    loop {
        let line = match read_line(&mut reader, MAX_REQUEST_BYTES) {
            Ok(Some(line)) => line,
            Ok(None) => break,
            Err(error) if error.kind() == ErrorKind::InvalidData => {
                let _ = write_response(
                    reader.get_mut(),
                    Value::Null,
                    Err(rpc_error(
                        "E_REQUEST_TOO_LARGE",
                        "Request exceeds the size limit",
                    )),
                );
                break;
            }
            Err(_) => break,
        };
        // Re-check before every dispatch: once the switch is turned off (or
        // this connection was dropped), no further request is served.
        if !weak.upgrade().is_some_and(|broker| broker.is_active(id)) {
            break;
        }
        let request: Value = match serde_json::from_slice(&line) {
            Ok(request) => request,
            Err(_) => {
                if write_response(
                    reader.get_mut(),
                    Value::Null,
                    Err(rpc_error("E_INVALID_REQUEST", "Invalid JSON request")),
                )
                .is_err()
                {
                    break;
                }
                continue;
            }
        };
        let id = request.get("id").cloned().unwrap_or(Value::Null);
        let method = request.get("method").and_then(Value::as_str);
        let params = request.get("params").cloned().unwrap_or(Value::Null);
        let version = request.get("v").and_then(Value::as_u64);
        let result = if version != Some(RPC_VERSION) {
            Err(rpc_error("E_INVALID_REQUEST", "Unsupported RPC version"))
        } else {
            match method {
                Some("ping") => Ok(json!({})),
                Some("tools/list") => Ok(super::tools::list_result_for(
                    weak.upgrade()
                        .is_some_and(|broker| broker.status().write_enabled),
                )),
                Some("tools/call") => {
                    let name = params.get("name").and_then(Value::as_str).unwrap_or("");
                    if super::tools::is_write_tool(name) {
                        let stream = reader.get_ref().try_clone().ok();
                        call_write(&weak, connection, peer, stream, host.as_deref(), params)
                    } else {
                        call_tool(host.as_deref(), params)
                    }
                }
                Some(_) => Err(rpc_error("E_METHOD_NOT_FOUND", "Method not found")),
                None => Err(rpc_error("E_INVALID_REQUEST", "Missing method")),
            }
        };
        if write_response(reader.get_mut(), id, result).is_err() {
            break;
        }
    }
    if let Some(broker) = weak.upgrade() {
        broker.unregister(id);
    }
}

/// Run a write tool. Writes need the write switch, are never queued behind
/// another write (any connection) and get a validity check bound to this
/// connection, so an approval cannot outlive the connection or the switches.
fn call_write(
    weak: &Weak<Broker>,
    connection: u64,
    peer: Option<super::caller::PidStamp>,
    stream: Option<UnixStream>,
    host: Option<&dyn ToolHost>,
    params: Value,
) -> Result<Value, RpcError> {
    let Some(broker) = weak.upgrade() else {
        return Err(rpc_error("E_MCP_DISABLED", "Local MCP is off"));
    };
    let Some(host) = host else {
        return Err(rpc_error(
            "E_MCP_DISABLED",
            "Local MCP tools are unavailable",
        ));
    };
    if !broker.write_valid(connection) {
        return Err(rpc_error(
            "E_MCP_WRITE_DISABLED",
            super::tools::WRITE_DISABLED_MESSAGE,
        ));
    }
    // Only OpenNavo's own `mcp` subcommand may ask for writes; the dialog
    // names that process's parent, which means nothing for an arbitrary peer.
    let trusted = broker.trusted_client.as_deref().is_some_and(|expected| {
        super::caller::peer_runs(&super::caller::SystemProcesses, peer, expected)
    });
    if !trusted {
        return Err(rpc_error(
            "E_MCP_UNTRUSTED_CLIENT",
            "Install and uninstall are only accepted through OpenNavo's own mcp command. If OpenNavo was just moved, restart OpenNavo.",
        ));
    }
    let name = params.get("name").and_then(Value::as_str).unwrap_or("");
    let arguments = params.get("arguments").cloned().unwrap_or(Value::Null);
    let request = super::tools::parse_write(name, arguments)
        .map_err(|error: ToolError| (error.code, error.message))?;
    let client_name = params
        .get("client")
        .and_then(|client| client.get("name"))
        .and_then(Value::as_str)
        .map(super::protocol::sanitize_client_name)
        .filter(|name| !name.is_empty());
    let Some(_slot) = WriteSlot::acquire(&broker) else {
        return Err(rpc_error("E_BUSY", super::tools::BUSY_MESSAGE));
    };
    let generation = broker.write_generation.load(Ordering::Acquire);
    let check = weak.clone();
    let context = super::tools::WriteContext {
        connection,
        peer,
        client_name,
        // The connection stays registered while its thread waits on the
        // dialog, so also look at the socket itself: a client that hung up
        // must not leave an approval behind.
        still_valid: Arc::new(move || {
            stream.as_ref().is_some_and(peer_open)
                && check.upgrade().is_some_and(|broker| {
                    broker.write_generation.load(Ordering::Acquire) == generation
                        && broker.write_valid(connection)
                })
        }),
    };
    drop(broker);
    host.write(request, context)
        .map_err(|error: ToolError| (error.code, error.message))
}

/// Whether the client end of `stream` is still connected. Peeks without
/// consuming or changing the descriptor's blocking mode; pipelined bytes
/// count as connected, end of stream or an error as gone.
fn peer_open(stream: &UnixStream) -> bool {
    use nix::sys::socket::{MsgFlags, recv};
    use std::os::fd::AsRawFd;

    let mut byte = [0u8; 1];
    match recv(
        stream.as_raw_fd(),
        &mut byte,
        MsgFlags::MSG_PEEK | MsgFlags::MSG_DONTWAIT,
    ) {
        Ok(0) => false,
        Ok(_) => true,
        Err(nix::errno::Errno::EAGAIN) => true,
        Err(_) => false,
    }
}

/// One write at a time across all connections.
struct WriteSlot(Arc<Broker>);

impl WriteSlot {
    fn acquire(broker: &Arc<Broker>) -> Option<Self> {
        broker
            .writing
            .compare_exchange(false, true, Ordering::AcqRel, Ordering::Acquire)
            .ok()
            .map(|_| Self(Arc::clone(broker)))
    }
}

impl Drop for WriteSlot {
    fn drop(&mut self) {
        self.0.writing.store(false, Ordering::Release);
    }
}

fn call_tool(host: Option<&dyn ToolHost>, params: Value) -> Result<Value, RpcError> {
    // Availability is checked before any validation so an unavailable broker
    // answers every call the same way, whatever the name or arguments.
    let Some(host) = host else {
        return Err(rpc_error(
            "E_MCP_DISABLED",
            "Local MCP read tools are unavailable",
        ));
    };
    let Some(params) = params.as_object() else {
        return Err(rpc_error("E_INVALID_ARG", "Invalid tool call"));
    };
    let Some(name) = params.get("name").and_then(Value::as_str) else {
        return Err(rpc_error("E_INVALID_ARG", "Missing tool name"));
    };
    let arguments = params.get("arguments").cloned().unwrap_or(Value::Null);
    super::tools::call(host, name, arguments)
        .map_err(|error: ToolError| (error.code, error.message))
}

fn write_response(
    stream: &mut UnixStream,
    id: Value,
    result: Result<Value, RpcError>,
) -> io::Result<()> {
    let response = match result {
        Ok(result) => json!({
            "v": RPC_VERSION,
            "id": id,
            "ok": true,
            "result": result,
        }),
        Err((code, message)) => json!({
            "v": RPC_VERSION,
            "id": id,
            "ok": false,
            "error": { "code": code, "message": message },
        }),
    };
    let mut body = serde_json::to_vec(&response).map_err(io::Error::other)?;
    if body.len() > MAX_RESPONSE_BYTES {
        body = serde_json::to_vec(&json!({
            "v": RPC_VERSION,
            "id": id,
            "ok": false,
            "error": { "code": "E_RESPONSE_TOO_LARGE", "message": "Response exceeds the size limit" },
        }))
        .map_err(io::Error::other)?;
    }
    body.push(b'\n');
    stream.write_all(&body)?;
    stream.flush()
}

fn read_line<R: BufRead>(reader: &mut R, max: usize) -> io::Result<Option<Vec<u8>>> {
    let mut line = Vec::new();
    loop {
        let buffer = reader.fill_buf()?;
        if buffer.is_empty() {
            return if line.is_empty() {
                Ok(None)
            } else {
                Ok(Some(line))
            };
        }
        let newline = buffer.iter().position(|byte| *byte == b'\n');
        let take = newline.map_or(buffer.len(), |position| position + 1);
        if line.len() + take > max {
            return Err(io::Error::new(
                ErrorKind::InvalidData,
                "request exceeds the size limit",
            ));
        }
        line.extend_from_slice(&buffer[..take]);
        reader.consume(take);
        if newline.is_some() {
            if line.last() == Some(&b'\n') {
                line.pop();
            }
            if line.last() == Some(&b'\r') {
                line.pop();
            }
            return Ok(Some(line));
        }
    }
}

/// App-side read tools backed by the live `DesktopCore`.
///
/// These methods run on broker connection threads and hand database/bundle
/// work to the app's blocking pool. The updatable-apps path reads only existing
/// cached update results; it never calls the update checker, starts a scan or
/// performs a network request.
///
/// Generic over the runtime for the same reason [`super::writes::Writes`] is:
/// the dispatch from `Broker` into a write is only reachable from a test if the
/// host it dispatches to can be built with a mock `AppHandle`. Nothing else in
/// here touches `app`.
struct LiveTools<R: tauri::Runtime = tauri::Wry> {
    core: Arc<crate::core::DesktopCore>,
    writes: super::writes::Writes<R>,
}

impl<R: tauri::Runtime> LiveTools<R> {
    fn new(
        core: Arc<crate::core::DesktopCore>,
        app: tauri::AppHandle<R>,
        data_dir: &std::path::Path,
    ) -> Self {
        let writes = super::writes::Writes {
            app,
            core: core.clone(),
            audit: super::audit::Audit::new(data_dir),
        };
        Self { core, writes }
    }
}

fn tool_error(error: crate::AppError) -> ToolError {
    ToolError::new(error.code, error.message)
}

fn json_error(error: serde_json::Error) -> ToolError {
    ToolError::internal(error.to_string())
}

/// One row `list_updatable_apps` offers, with everything the read tool prints
/// and everything `queue_updates` needs to queue it.
///
/// Both tools build their answer from this one function, which is what makes
/// the update tool's rule checkable: an id it accepts is by construction an id
/// the read tool returned. `updates_list` only merges cached results and never
/// calls `check_update_sources`, so neither tool can start a feed request.
///
/// Rows are whatever `updates_list` holds, exactly as this tool has always
/// answered. That includes Homebrew formulae, which the updates page does not
/// draw (`scheduler::visible`); making this list match the page is a change to
/// this tool's existing answer, not part of adding `queue_updates`, so it is
/// left as it is and reported as an open question instead.
pub struct UpdatableApp {
    pub id: String,
    pub path: String,
    pub name: String,
    pub installed_version: String,
    pub current_version: String,
    pub kind: crate::model::Kind,
    /// Which feed the row came from. Carried so the update tool can tell a
    /// Homebrew formula from a `mas` one: both are `Kind::Formula`, but only
    /// the Homebrew one is a token masquerading as an id.
    pub source: crate::model::UpdateSource,
    pub token: String,
}

pub fn updatable_rows(
    core: &std::sync::Arc<crate::core::DesktopCore>,
) -> Result<Vec<UpdatableApp>, crate::AppError> {
    let updates = core.updates_list()?;
    if updates.is_empty() {
        return Ok(Vec::new());
    }
    // Only the rows the updates page draws. `updates_list` is shared with the
    // tray (`tray.rs:97`, `tray.rs:250`) and the page, so it cannot be narrowed
    // itself; this is the MCP boundary, and here the list has to match what the
    // user can see or the tool offers to upgrade something the page never
    // showed. `scheduler::visible` is the same rule the page's `shown()` mirrors:
    // the catalog is cask-only (ADR-018), so a Homebrew formula or link is not
    // listed. This is deliberately not `scheduler::eligible`, which also drops
    // pinned, ignored, skipped and non-auto-upgraded rows: those are still on
    // the page and upgrading one on request is allowed.
    let updates: Vec<_> = updates
        .into_iter()
        .filter(crate::scheduler::visible)
        .collect();
    if updates.is_empty() {
        return Ok(Vec::new());
    }
    // Paths come from caches too: the in-memory app inventory for native
    // updates and the stored library snapshot for Homebrew entries.
    let installed: Vec<crate::model::InstalledItem> = core
        .database
        .meta("installed_cache")?
        .as_deref()
        .and_then(|body| serde_json::from_str(body).ok())
        .unwrap_or_default();
    Ok(updates
        .into_iter()
        .map(|update| {
            let id = update
                .app_id
                .clone()
                .unwrap_or_else(|| update.token.clone());
            let path = update
                .app_id
                .as_deref()
                .and_then(|id| core.queue.uninstall_engine.app(id).ok())
                .map(|cached| cached.app.path)
                .or_else(|| {
                    installed
                        .iter()
                        .find(|local| {
                            local.kind == update.kind && local.matches_token(&update.token)
                        })
                        .and_then(|local| local.app_paths.first().cloned())
                })
                .unwrap_or_default();
            UpdatableApp {
                id,
                path,
                name: update.name,
                installed_version: update.installed_version,
                current_version: update.current_version,
                kind: update.kind,
                source: update.source,
                token: update.token,
            }
        })
        .collect())
}

/// Run database or bundle work on the app's blocking pool. Broker connections
/// are already non-tokio threads, but this keeps the same rule as Tauri
/// commands: no filesystem or SQLite read runs on a tokio worker.
fn blocking_tool<T>(
    work: impl FnOnce() -> Result<T, crate::AppError> + Send + 'static,
) -> Result<T, ToolError>
where
    T: Send + 'static,
{
    tauri::async_runtime::block_on(crate::blocking(work)).map_err(tool_error)
}

fn parse_kind(kind: &str) -> Result<crate::model::Kind, ToolError> {
    match kind {
        "cask" => Ok(crate::model::Kind::Cask),
        "formula" => Ok(crate::model::Kind::Formula),
        "link" => Ok(crate::model::Kind::Link),
        _ => Err(ToolError::invalid("kind is not supported")),
    }
}

impl<R: tauri::Runtime> ToolHost for LiveTools<R> {
    fn search_catalog(
        &self,
        query: &str,
        kind: Option<&str>,
        limit: u32,
    ) -> Result<Value, ToolError> {
        let query = crate::model::SearchQuery {
            q: query.to_owned(),
            kind: kind.map(parse_kind).transpose()?,
            include_links: false,
            category: None,
            include_disabled: false,
            limit,
            offset: 0,
        };
        let database = self.core.database.clone();
        let page =
            blocking_tool(move || crate::catalog::search_without_history(&database, &query))?;
        serde_json::to_value(page).map_err(json_error)
    }

    fn get_catalog_item(&self, kind: &str, token: &str) -> Result<Value, ToolError> {
        let kind = parse_kind(kind)?;
        let token = token.to_owned();
        let database = self.core.database.clone();
        let item = blocking_tool(move || crate::catalog::get(&database, kind, &token))?;
        Ok(json!({ "item": item }))
    }

    fn list_installed_apps(&self) -> Result<Value, ToolError> {
        let engine = self.core.queue.uninstall_engine.clone();
        let apps = blocking_tool(move || engine.list())?;
        let rows: Vec<Value> = apps
            .iter()
            .map(|app| {
                json!({
                    "id": app.id,
                    "path": app.path,
                    "name": app.name,
                    "version": app.version,
                    "bundleId": app.bundle_id,
                    "source": app.source,
                })
            })
            .collect();
        Ok(json!({ "apps": rows }))
    }

    fn list_updatable_apps(&self) -> Result<Value, ToolError> {
        let core = self.core.clone();
        let rows: Vec<Value> = blocking_tool(move || updatable_rows(&core))?
            .into_iter()
            .map(|row| {
                json!({
                    "id": row.id,
                    "path": row.path,
                    "name": row.name,
                    "currentVersion": row.installed_version,
                    "latestVersion": row.current_version,
                })
            })
            .collect();
        Ok(json!({ "apps": rows }))
    }

    fn get_task(&self, id: &str) -> Result<Value, ToolError> {
        let database = self.core.database.clone();
        let id = id.to_owned();
        let task = blocking_tool(move || database.task(&id))?;
        Ok(json!({ "task": task }))
    }

    fn scan_cleanup(&self) -> Result<Value, ToolError> {
        // The scan is a real filesystem walk, so it runs on the blocking pool.
        // It never deletes: `scan_for_command` only measures and remembers the
        // report, and the tool filters what came back so a client never even
        // learns the id of an item it may not remove.
        let core = self.core.clone();
        let report = tauri::async_runtime::block_on(async {
            let permissions = core.permissions().await.map_err(tool_error)?;
            let request_id = format!("mcp-scan-{}", uuid::Uuid::new_v4());
            core.queue
                .cleanup_engine
                .scan_for_command(request_id, None, permissions, Some(core.clone()))
                .await
                .map_err(tool_error)
        })?;
        let allowed = super::cleanup::candidates(&report);
        let rows: Vec<Value> = allowed
            .iter()
            .filter(|(_, item)| super::cleanup::kind_allowed(item.kind))
            .map(|(category, item)| {
                let category_key = serde_json::to_value(category.key)
                    .ok()
                    .and_then(|value| value.as_str().map(str::to_owned))
                    .unwrap_or_default();
                let group = category
                    .groups
                    .iter()
                    .find(|group| group.items.iter().any(|candidate| candidate.id == item.id));
                json!({
                    "id": item.id,
                    "category": category_key,
                    "name": group.map(|group| group.title.clone()).unwrap_or_default(),
                    "path": item.path,
                    "kind": match item.kind {
                        crate::cleanup::types::CleanupItemKind::Dir => "dir",
                        crate::cleanup::types::CleanupItemKind::File => "file",
                        crate::cleanup::types::CleanupItemKind::Command => "command",
                    },
                    "bytes": item.size_bytes,
                    "deleteMode": match item.delete_mode {
                        crate::model::DeleteMode::Trash => "trash",
                        crate::model::DeleteMode::Delete => "delete",
                    },
                })
            })
            .collect();
        let bytes: u64 = allowed
            .iter()
            .filter(|(_, item)| super::cleanup::kind_allowed(item.kind))
            .filter_map(|(_, item)| item.size_bytes)
            .sum();
        Ok(json!({
            "reportId": report.report_id,
            "expiresAt": report.expires_at,
            "reclaimableBytes": bytes,
            "items": rows,
        }))
    }

    fn cancel_writes(&self) {
        self.writes.cancel();
    }

    fn write(
        &self,
        request: super::tools::WriteRequest,
        context: super::tools::WriteContext,
    ) -> Result<Value, ToolError> {
        self.writes.run(request, context)
    }
}

/// Install the broker during Tauri setup. Startup failures are logged and
/// surfaced through `mcp_status`; they never block the app.
pub fn install(app: tauri::AppHandle) {
    use tauri::Manager;

    let data_dir = match app.path().app_data_dir() {
        Ok(path) => path,
        Err(error) => {
            log::warn!("Local MCP data directory unavailable: {error}");
            return;
        }
    };
    let Some(core) = app
        .try_state::<Arc<crate::core::DesktopCore>>()
        .map(|state| state.inner().clone())
    else {
        log::warn!("Local MCP read tools unavailable: DesktopCore is not managed");
        return;
    };
    let host: Arc<dyn ToolHost> = Arc::new(LiveTools::new(core, app.clone(), &data_dir));
    let broker = Arc::new(Broker::with_host(data_dir, Some(host)));
    if let Err(error) = broker.start_if_enabled() {
        log::warn!("Local MCP broker unavailable: {error}");
    }
    app.manage(broker);
}

/// Best-effort cleanup on app exit. The persisted switch is left untouched.
pub fn shutdown(app: &tauri::AppHandle) {
    use tauri::Manager;

    if let Some(broker) = app.try_state::<Arc<Broker>>() {
        broker.shutdown();
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{io::Read, os::unix::net::UnixStream};

    fn tempdir() -> tempfile::TempDir {
        tempfile::tempdir().expect("tempdir")
    }

    fn request(path: &std::path::Path, body: Value) -> Value {
        let mut stream = UnixStream::connect(path).expect("connect");
        serde_json::to_writer(&mut stream, &body).expect("write");
        stream.write_all(b"\n").expect("newline");
        let mut reader = BufReader::new(stream);
        let mut line = String::new();
        reader.read_line(&mut line).expect("read");
        serde_json::from_str(&line).expect("json")
    }

    fn wait_running(broker: &Arc<Broker>) {
        for _ in 0..100 {
            if broker.status().running {
                return;
            }
            std::thread::sleep(Duration::from_millis(5));
        }
        panic!("broker did not start");
    }

    #[test]
    fn write_switch_keeps_start_error_while_not_running() {
        let dir = tempdir();
        let broker = Arc::new(Broker::new(dir.path().to_path_buf()));
        {
            let mut inner = broker.lock();
            inner.enabled = true;
            inner.error = Some("E_MCP_BROKER: bind failed".into());
        }
        broker
            .set_write_enabled(true)
            .expect("persist write switch");
        let status = broker.status();
        assert!(
            status.enabled && status.write_enabled && !status.running,
            "{status:?}"
        );
        assert_eq!(status.error.as_deref(), Some("E_MCP_BROKER: bind failed"));

        // Once running, a successful write change clears a stale error.
        broker.set_enabled(true).expect("enable");
        wait_running(&broker);
        broker.lock().error = Some("old".into());
        broker.set_write_enabled(false).expect("write off");
        assert!(broker.status().error.is_none());
        broker.shutdown();
    }

    #[test]
    fn switch_off_creates_no_socket() {
        let dir = tempdir();
        let broker = Arc::new(Broker::new(dir.path().to_path_buf()));
        broker.start_if_enabled().expect("start");
        assert!(!broker.status().running);
        assert!(!socket::path(dir.path()).path.exists());
    }

    #[test]
    fn failed_disable_persist_keeps_running_and_never_reenables_on_restart() {
        use std::os::unix::fs::PermissionsExt;

        let dir = tempdir();
        let broker = Arc::new(Broker::new(dir.path().to_path_buf()));
        broker.set_enabled(true).expect("enable");
        wait_running(&broker);
        let mcp = state::mcp_dir(dir.path());
        std::fs::set_permissions(&mcp, std::fs::Permissions::from_mode(0o500)).expect("chmod");

        broker.set_enabled(false).expect_err("persist must fail");
        let status = broker.status();
        // Runtime and persisted switch agree: still on, error reported.
        assert!(status.enabled && status.running, "{status:?}");
        assert!(status.error.is_some());
        assert!(state::load(dir.path()).enabled);

        std::fs::set_permissions(&mcp, std::fs::Permissions::from_mode(0o700)).expect("chmod");
        broker.set_enabled(false).expect("disable");
        assert!(!broker.status().running);
        assert!(!state::load(dir.path()).enabled);
        broker.shutdown();

        // A fresh broker (next launch) stays off.
        let restarted = Arc::new(Broker::new(dir.path().to_path_buf()));
        restarted.start_if_enabled().expect("start");
        assert!(!restarted.status().enabled);
        assert!(!restarted.status().running);
    }

    #[test]
    fn switch_on_binds_and_answers_ping() {
        let dir = tempdir();
        let broker = Arc::new(Broker::new(dir.path().to_path_buf()));
        broker.set_enabled(true).expect("enable");
        wait_running(&broker);
        let response = request(
            &socket::path(dir.path()).path,
            json!({"v":1,"id":"1","method":"ping","params":{}}),
        );
        assert_eq!(response["ok"], true);
        assert_eq!(response["result"], json!({}));
        broker.set_enabled(false).expect("disable");
        assert!(!socket::path(dir.path()).path.exists());
        assert!(!broker.status().running);
    }

    #[test]
    fn turning_off_disconnects_existing_connection_and_clears_sessions() {
        let dir = tempdir();
        let broker = Arc::new(Broker::new(dir.path().to_path_buf()));
        broker.set_enabled(true).expect("enable");
        wait_running(&broker);
        let mut stream = UnixStream::connect(socket::path(dir.path()).path).expect("connect");
        for _ in 0..100 {
            if !broker.lock().sessions.is_empty() {
                break;
            }
            std::thread::sleep(Duration::from_millis(5));
        }
        assert_eq!(broker.lock().sessions.len(), 1);
        broker.set_enabled(false).expect("disable");
        let mut byte = [0u8; 1];
        let _ = stream.read(&mut byte);
        assert!(broker.lock().sessions.is_empty());
    }

    #[test]
    fn write_switch_requires_master_and_resets_when_master_turns_off() {
        let dir = tempdir();
        let broker = Arc::new(Broker::new(dir.path().to_path_buf()));
        let error = broker.set_write_enabled(true).expect_err("master off");
        assert_eq!(error.code, "E_MCP_DISABLED");
        assert!(!state::load(dir.path()).write_enabled);

        broker.set_enabled(true).expect("enable");
        wait_running(&broker);
        broker.set_write_enabled(true).expect("allow writes");
        assert!(broker.status().write_enabled);
        assert!(state::load(dir.path()).write_enabled);
        // Re-enabling while running keeps the persisted write switch.
        broker.set_enabled(true).expect("enable again");
        assert!(state::load(dir.path()).write_enabled);

        broker.set_enabled(false).expect("disable");
        assert!(!broker.status().write_enabled);
        assert_eq!(state::load(dir.path()), state::State::default());
        broker.set_enabled(true).expect("enable");
        assert!(
            !broker.status().write_enabled,
            "writes stay off after re-enable"
        );
        broker.set_enabled(false).expect("disable");
    }

    #[test]
    fn turning_writes_off_disconnects_clients_but_keeps_listening() {
        let dir = tempdir();
        let broker = Arc::new(Broker::new(dir.path().to_path_buf()));
        broker.set_enabled(true).expect("enable");
        wait_running(&broker);
        broker.set_write_enabled(true).expect("allow writes");
        let mut stream = UnixStream::connect(socket::path(dir.path()).path).expect("connect");
        for _ in 0..100 {
            if !broker.lock().sessions.is_empty() {
                break;
            }
            std::thread::sleep(Duration::from_millis(5));
        }
        assert_eq!(broker.lock().sessions.len(), 1);
        broker.set_write_enabled(false).expect("forbid writes");
        let mut byte = [0u8; 1];
        assert_eq!(stream.read(&mut byte).unwrap_or(0), 0);
        assert!(broker.lock().sessions.is_empty());
        let status = broker.status();
        assert!(status.running && !status.write_enabled, "{status:?}");
        let response = request(
            &socket::path(dir.path()).path,
            json!({"v":1,"id":"1","method":"ping","params":{}}),
        );
        assert_eq!(response["ok"], true);
        broker.set_enabled(false).expect("disable");
    }

    #[test]
    fn failed_write_off_persist_keeps_writes_on_and_reports() {
        use std::os::unix::fs::PermissionsExt;

        if unsafe { nix::libc::geteuid() } == 0 {
            return;
        }
        let dir = tempdir();
        let broker = Arc::new(Broker::new(dir.path().to_path_buf()));
        broker.set_enabled(true).expect("enable");
        wait_running(&broker);
        broker.set_write_enabled(true).expect("allow writes");
        let mcp = state::mcp_dir(dir.path());
        std::fs::set_permissions(&mcp, std::fs::Permissions::from_mode(0o500)).expect("chmod");
        broker
            .set_write_enabled(false)
            .expect_err("persist must fail");
        let status = broker.status();
        assert!(status.write_enabled && status.error.is_some(), "{status:?}");
        assert!(state::load(dir.path()).write_enabled);
        std::fs::set_permissions(&mcp, std::fs::Permissions::from_mode(0o700)).expect("chmod");
        broker.set_enabled(false).expect("disable");
    }

    #[test]
    fn status_reports_absolute_command_path() {
        let dir = tempdir();
        let broker = Broker::new(dir.path().to_path_buf());
        let path = broker.status().command_path.expect("current exe");
        assert!(std::path::Path::new(&path).is_absolute());
    }

    #[test]
    fn status_reports_whether_the_volume_is_case_sensitive() {
        let dir = tempdir();
        let broker = Broker::new(dir.path().to_path_buf());
        let status = broker.status();
        // The test binary lives on the boot volume, which is case-insensitive unless it was formatted otherwise.
        let exe = status.command_path.clone().expect("current exe");
        assert_eq!(
            status.case_sensitive,
            volume_case_sensitive(std::path::Path::new(&exe))
        );
        // A path that cannot be asked falls back to the macOS default rather than panicking.
        assert!(!volume_case_sensitive(std::path::Path::new(
            "/nonexistent-opennavo-volume/x"
        )));
    }

    #[test]
    fn unsupported_method_returns_structured_error() {
        let dir = tempdir();
        let broker = Arc::new(Broker::new(dir.path().to_path_buf()));
        broker.set_enabled(true).expect("enable");
        wait_running(&broker);
        let response = request(
            &socket::path(dir.path()).path,
            json!({"v":1,"id":"1","method":"nope","params":{}}),
        );
        assert_eq!(response["ok"], false);
        assert_eq!(response["error"]["code"], "E_METHOD_NOT_FOUND");
        broker.shutdown();
    }

    #[test]
    fn oversized_request_does_not_kill_broker() {
        let dir = tempdir();
        let broker = Arc::new(Broker::new(dir.path().to_path_buf()));
        broker.set_enabled(true).expect("enable");
        wait_running(&broker);
        let path = socket::path(dir.path()).path;
        let mut stream = UnixStream::connect(&path).expect("connect");
        stream
            .set_read_timeout(Some(Duration::from_secs(5)))
            .expect("read timeout");
        let mut body = vec![b'x'; MAX_REQUEST_BYTES + 1];
        body.push(b'\n');
        // The broker may close the socket while this oversized write is still in flight, so a
        // reset here is the rejection working, not a failure. Only read a response if one arrived.
        match stream.write_all(&body) {
            Ok(()) => {}
            Err(error) => assert!(
                matches!(
                    error.kind(),
                    std::io::ErrorKind::BrokenPipe | std::io::ErrorKind::ConnectionReset
                ),
                "unexpected write error: {error}"
            ),
        }
        let mut reader = BufReader::new(stream);
        let mut line = String::new();
        match reader.read_line(&mut line) {
            Ok(0) => {}
            Ok(_) => {
                let response: Value = serde_json::from_str(&line).expect("json");
                assert_eq!(response["error"]["code"], "E_REQUEST_TOO_LARGE");
            }
            Err(error)
                if matches!(
                    error.kind(),
                    std::io::ErrorKind::WouldBlock | std::io::ErrorKind::TimedOut
                ) => {}
            Err(error) => panic!("unexpected read error: {error}"),
        }

        let response = request(&path, json!({"v":1,"id":"2","method":"ping"}));
        assert_eq!(response["ok"], true);
        broker.shutdown();
    }

    struct MockHost;

    impl ToolHost for MockHost {
        fn search_catalog(
            &self,
            query: &str,
            kind: Option<&str>,
            _limit: u32,
        ) -> Result<Value, ToolError> {
            assert_eq!(query, "firefox");
            assert_eq!(kind, Some("cask"));
            Ok(json!({
                "total": 1,
                "items": [{ "item": { "kind": "cask", "token": "firefox" } }]
            }))
        }

        fn get_catalog_item(&self, _kind: &str, _token: &str) -> Result<Value, ToolError> {
            Ok(json!({ "item": null }))
        }

        fn list_installed_apps(&self) -> Result<Value, ToolError> {
            Ok(json!({ "apps": [] }))
        }

        fn list_updatable_apps(&self) -> Result<Value, ToolError> {
            Ok(json!({ "apps": [] }))
        }

        fn get_task(&self, id: &str) -> Result<Value, ToolError> {
            assert_eq!(id, "task-1");
            Ok(json!({ "task": { "id": "task-1", "state": "running" } }))
        }
    }

    fn broker_with_mock(dir: &tempfile::TempDir) -> Arc<Broker> {
        let host: Arc<dyn ToolHost> = Arc::new(MockHost);
        Arc::new(Broker::with_host(dir.path().to_path_buf(), Some(host)))
    }

    #[test]
    fn broker_round_trips_search_and_get_task() {
        let dir = tempdir();
        let broker = broker_with_mock(&dir);
        broker.set_enabled(true).expect("enable");
        wait_running(&broker);
        let path = socket::path(dir.path()).path;

        let response = request(
            &path,
            json!({
                "v": 1,
                "id": "search",
                "method": "tools/call",
                "params": {
                    "name": "search_catalog",
                    "arguments": { "query": "firefox", "kind": "cask" }
                }
            }),
        );
        assert_eq!(response["ok"], true);
        assert_eq!(response["result"]["untrusted"], true);
        assert_eq!(response["result"]["items"][0]["item"]["untrusted"], true);

        let response = request(
            &path,
            json!({
                "v": 1,
                "id": "task",
                "method": "tools/call",
                "params": { "name": "get_task", "arguments": { "id": "task-1" } }
            }),
        );
        assert_eq!(response["ok"], true);
        assert_eq!(response["result"]["task"]["state"], "running");
        broker.shutdown();
    }

    #[test]
    fn tools_list_and_unknown_write_names_are_rejected() {
        let dir = tempdir();
        let broker = broker_with_mock(&dir);
        broker.set_enabled(true).expect("enable");
        wait_running(&broker);
        let path = socket::path(dir.path()).path;

        let response = request(&path, json!({"v":1,"id":"list","method":"tools/list"}));
        assert_eq!(response["ok"], true);
        let names: Vec<&str> = response["result"]["tools"]
            .as_array()
            .expect("tools")
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
        assert!(!names.contains(&"install_app"));
        assert!(!names.contains(&"uninstall_app"));
        assert!(!names.contains(&"clean_cache"));
        assert!(!names.contains(&"queue_updates"));

        for (name, code) in [
            ("install_app", "E_MCP_WRITE_DISABLED"),
            ("uninstall_app", "E_MCP_WRITE_DISABLED"),
            ("queue_updates", "E_MCP_WRITE_DISABLED"),
            ("get_latest_scan_result", "E_TOOL_NOT_FOUND"),
        ] {
            let response = request(
                &path,
                json!({
                    "v": 1,
                    "id": name,
                    "method": "tools/call",
                    "params": { "name": name, "arguments": { "kind": "cask", "token": "iina" } }
                }),
            );
            assert_eq!(response["ok"], false, "name {name}");
            assert_eq!(response["error"]["code"], code, "name {name}");
        }
        broker.shutdown();
    }

    /// Write host that blocks until released, standing in for the native dialog.
    use super::super::tools::{WriteContext, WriteRequest};

    struct WriteHost {
        entered: std::sync::mpsc::SyncSender<WriteContext>,
        release: Mutex<std::sync::mpsc::Receiver<()>>,
        cancel: std::sync::mpsc::Sender<()>,
    }

    impl ToolHost for WriteHost {
        fn search_catalog(&self, _: &str, _: Option<&str>, _: u32) -> Result<Value, ToolError> {
            Ok(json!({}))
        }
        fn get_catalog_item(&self, _: &str, _: &str) -> Result<Value, ToolError> {
            Ok(json!({}))
        }
        fn list_installed_apps(&self) -> Result<Value, ToolError> {
            Ok(json!({}))
        }
        fn list_updatable_apps(&self) -> Result<Value, ToolError> {
            Ok(json!({}))
        }
        fn get_task(&self, _: &str) -> Result<Value, ToolError> {
            Ok(json!({}))
        }
        fn cancel_writes(&self) {
            let _ = self.cancel.send(());
        }
        fn write(&self, request: WriteRequest, context: WriteContext) -> Result<Value, ToolError> {
            assert_eq!(request.tool(), "install_app");
            self.entered.send(context.clone()).expect("entered");
            self.release.lock().expect("lock").recv().expect("release");
            if (context.still_valid)() {
                Ok(json!({ "queued": true }))
            } else {
                Err(ToolError::new("E_CONFIRMATION_INVALIDATED", "invalidated"))
            }
        }
    }

    fn install_call(id: &str) -> Value {
        json!({
            "v": 1,
            "id": id,
            "method": "tools/call",
            "params": {
                "name": "install_app",
                "arguments": { "kind": "cask", "token": "iina" },
                "client": { "name": "Claude\u{7}" }
            }
        })
    }

    fn write_broker(
        dir: &tempfile::TempDir,
    ) -> (
        Arc<Broker>,
        std::sync::mpsc::Receiver<WriteContext>,
        std::sync::mpsc::Sender<()>,
    ) {
        let (entered, entered_rx) = std::sync::mpsc::sync_channel(4);
        let (release_tx, release) = std::sync::mpsc::channel();
        let host: Arc<dyn ToolHost> = Arc::new(WriteHost {
            entered,
            release: Mutex::new(release),
            cancel: release_tx.clone(),
        });
        let broker = Arc::new(Broker::with_host(dir.path().to_path_buf(), Some(host)));
        broker.set_enabled(true).expect("enable");
        broker.set_write_enabled(true).expect("write");
        wait_running(&broker);
        (broker, entered_rx, release_tx)
    }

    #[test]
    fn a_second_write_while_one_is_open_gets_busy_not_queued() {
        let dir = tempdir();
        let (broker, entered, release) = write_broker(&dir);
        let path = socket::path(dir.path()).path;
        let first_path = path.clone();
        let first = std::thread::spawn(move || request(&first_path, install_call("1")));
        let context = entered
            .recv_timeout(Duration::from_secs(5))
            .expect("first write");
        assert_eq!(context.client_name.as_deref(), Some("Claude"));
        assert!(context.peer.is_some());
        assert!(
            context.peer.is_some_and(|peer| peer.started_at.is_some()),
            "the connection must be stamped with the peer's start time, not just its pid"
        );

        let second = request(&path, install_call("2"));
        assert_eq!(second["ok"], false);
        assert_eq!(second["error"]["code"], "E_BUSY");
        assert!(
            entered.try_recv().is_err(),
            "busy call must not reach the host"
        );

        release.send(()).expect("release");
        let first = first.join().expect("join");
        assert_eq!(first["ok"], true, "{first}");

        // The slot is free again afterwards.
        let third_path = path.clone();
        let third = std::thread::spawn(move || request(&third_path, install_call("3")));
        entered
            .recv_timeout(Duration::from_secs(5))
            .expect("third write");
        release.send(()).expect("release");
        assert_eq!(third.join().expect("join")["ok"], true);
        broker.shutdown();
    }

    #[test]
    fn turning_writes_off_invalidates_an_open_approval() {
        let dir = tempdir();
        let (broker, entered, release) = write_broker(&dir);
        let path = socket::path(dir.path()).path;
        let call = std::thread::spawn(move || request(&path, install_call("1")));
        let context = entered.recv_timeout(Duration::from_secs(5)).expect("write");
        assert!((context.still_valid)());
        broker.set_write_enabled(false).expect("write off");
        assert!(!(context.still_valid)());
        // Turning writes back on does not revive an approval from before.
        broker.set_write_enabled(true).expect("write on");
        assert!(!(context.still_valid)());
        release.send(()).expect("release");
        let _ = call.join();
        broker.shutdown();
    }

    #[test]
    fn a_closed_connection_invalidates_its_approval() {
        let dir = tempdir();
        let (broker, entered, release) = write_broker(&dir);
        let path = socket::path(dir.path()).path;
        let mut stream = UnixStream::connect(&path).expect("connect");
        serde_json::to_writer(&mut stream, &install_call("1")).expect("write");
        stream.write_all(b"\n").expect("newline");
        let context = entered.recv_timeout(Duration::from_secs(5)).expect("write");
        assert!((context.still_valid)());
        stream.shutdown(std::net::Shutdown::Both).expect("hang up");
        drop(stream);
        let mut valid = true;
        for _ in 0..200 {
            valid = (context.still_valid)();
            if !valid {
                break;
            }
            std::thread::sleep(Duration::from_millis(5));
        }
        assert!(!valid, "approval must not outlive its connection");
        release.send(()).expect("release");
        broker.shutdown();
    }

    #[test]
    fn every_write_reaches_the_confirming_host_and_ignores_confirmed_flags() {
        let dir = tempdir();
        let (broker, entered, release) = write_broker(&dir);
        let path = socket::path(dir.path()).path;
        for id in ["1", "2", "3"] {
            let mut call = install_call(id);
            // Claims of prior approval anywhere in params change nothing.
            call["params"]["confirmed"] = json!(true);
            call["params"]["_meta"] = json!({ "approved": true });
            let call_path = path.clone();
            let thread = std::thread::spawn(move || request(&call_path, call));
            entered
                .recv_timeout(Duration::from_secs(5))
                .expect("each write asks the host (dialog) again");
            release.send(()).expect("release");
            assert_eq!(thread.join().expect("join")["ok"], true);
        }
        let mut forged = install_call("4");
        forged["params"]["arguments"]["confirmed"] = json!(true);
        let response = request(&path, forged);
        assert_eq!(response["error"]["code"], "E_INVALID_ARG");
        assert!(entered.try_recv().is_err());
        broker.shutdown();
    }

    #[test]
    fn switching_off_drains_an_open_write_and_cancels_its_dialog() {
        for master in [false, true] {
            let dir = tempdir();
            let (broker, entered, release) = write_broker(&dir);
            let path = socket::path(dir.path()).path;
            let call = std::thread::spawn(move || {
                let _ = std::panic::catch_unwind(|| request(&path, install_call("1")));
            });
            let context = entered.recv_timeout(Duration::from_secs(5)).expect("write");
            // Nothing releases the host except the broker's cancel_writes.
            drop(release);
            if master {
                broker.set_enabled(false).expect("off");
            } else {
                broker.set_write_enabled(false).expect("write off");
            }
            // Returning means the write slot was drained, and the approval is void.
            assert!(!broker.writing.load(Ordering::Acquire), "master {master}");
            assert!(!(context.still_valid)());
            let _ = call.join();
            broker.shutdown();
        }
    }

    #[test]
    fn writes_from_a_peer_that_is_not_the_opennavo_binary_are_refused() {
        let dir = tempdir();
        let (entered, entered_rx) = std::sync::mpsc::sync_channel(4);
        let (release_tx, release) = std::sync::mpsc::channel();
        let host: Arc<dyn ToolHost> = Arc::new(WriteHost {
            entered,
            release: Mutex::new(release),
            cancel: release_tx,
        });
        let broker = Arc::new(
            Broker::with_host(dir.path().to_path_buf(), Some(host))
                .with_trusted_client(Some(PathBuf::from("/bin/sh"))),
        );
        broker.set_enabled(true).expect("enable");
        broker.set_write_enabled(true).expect("write");
        wait_running(&broker);
        let path = socket::path(dir.path()).path;
        let response = request(&path, install_call("1"));
        assert_eq!(response["error"]["code"], "E_MCP_UNTRUSTED_CLIENT");
        assert!(
            entered_rx.try_recv().is_err(),
            "no dialog for an untrusted peer"
        );
        // Read tools stay available to any same-user peer.
        let list = request(&path, json!({"v":1,"id":"l","method":"tools/list"}));
        assert_eq!(list["ok"], true);
        broker.shutdown();
    }

    /// A cleanup report on disk, exactly the shape the scan leaves behind.
    fn report_for(engine: &crate::cleanup::Engine, id: &str, item: &str) {
        use crate::cleanup::types::{
            CategoryReport, CleanupCategory, CleanupGroup, CleanupItem, CleanupItemKind,
            CleanupReason, CleanupReport, CleanupRisk, CleanupVolume,
        };
        let one = CleanupItem {
            id: item.into(),
            path: format!("/Users/me/Library/Caches/{item}"),
            kind: CleanupItemKind::Dir,
            command: None,
            size_bytes: Some(2048),
            size_complete: true,
            kept_open_files: 0,
            modified_at: None,
            reason: CleanupReason::CacheDir,
            selected: true,
            delete_mode: crate::model::DeleteMode::Trash,
            needs_admin: false,
            note: None,
        };
        let report = CleanupReport {
            report_id: id.into(),
            scanned_at: crate::uninstall::now(),
            expires_at: crate::uninstall::now() + 600_000,
            volume: CleanupVolume {
                name: "synthetic".into(),
                total_bytes: 0,
                free_bytes: 0,
            },
            categories: vec![CategoryReport {
                key: CleanupCategory::AppCaches,
                risk: CleanupRisk::Rebuildable,
                bytes: 2048,
                complete: true,
                groups: vec![CleanupGroup {
                    id: "g".into(),
                    title: "g".into(),
                    subtitle: None,
                    app_id: None,
                    bundle_id: None,
                    items: vec![one.clone()],
                }],
                skipped: vec![],
            }],
            incomplete: vec![],
        };
        engine.reports.lock().expect("reports").insert(
            id.into(),
            crate::cleanup::Entry {
                category_millis: std::collections::BTreeMap::new(),
                report,
                items: std::collections::BTreeMap::from([(
                    item.into(),
                    crate::cleanup::EntryItem {
                        item: one,
                        owner: crate::cleanup::owner::Owner::default(),
                        title: "cache".into(),
                        tree: None,
                        action: crate::uninstall::ItemAction::Path {
                            empty_only: false,
                            bootout: None,
                        },
                        docker_endpoint: None,
                    },
                )]),
            },
        );
    }

    /// The whole production write path, one test deep.
    ///
    /// `Broker` dispatches a write to its `ToolHost`; in the app that host is
    /// `LiveTools`, whose `write` is a one-line call into `Writes::run`. Every
    /// other write test in this file uses a `MockHost`, so replacing that line
    /// with a refusal left the suite green: nothing entered it. This test goes
    /// through a real socket, the real `LiveTools`, and the real `Writes::run`,
    /// and asserts a cleanup is queued.
    ///
    /// `LiveTools` (and so `Writes`) is generic over the runtime for this: an
    /// `AppHandle<Wry>` cannot be built in a test.
    #[test]
    fn a_write_over_the_socket_reaches_writes_run_and_queues_a_cleanup() {
        use tauri::Manager;

        let dir = tempdir();
        let core = crate::core::DesktopCore::open(
            dir.path().join("data"),
            dir.path().join("cache"),
            dir.path().join("askpass"),
            Arc::new(|_| {}),
        )
        .expect("core");
        report_for(&core.queue.cleanup_engine, "report", "cache");
        let app = tauri::test::mock_app();
        let service = Arc::new(crate::confirmation::ConfirmationService::default());
        app.manage(service.clone());
        let host: Arc<dyn ToolHost> = Arc::new(LiveTools::new(
            core.clone(),
            app.handle().clone(),
            dir.path(),
        ));
        let broker = Arc::new(
            Broker::with_host(dir.path().to_path_buf(), Some(host))
                // The socket is created by this process, so its pid's executable
                // is the test binary. Name that, and the peer check passes for
                // the same reason it does in the app.
                .with_trusted_client(std::env::current_exe().ok()),
        );
        broker.set_enabled(true).expect("enable");
        broker.set_write_enabled(true).expect("write");
        wait_running(&broker);

        // Answer the window the way the webview would.
        let answerer = {
            let service = service.clone();
            std::thread::spawn(move || {
                let deadline = std::time::Instant::now() + Duration::from_secs(20);
                while std::time::Instant::now() < deadline {
                    if let Some(view) = service.window_view() {
                        service.test_arm();
                        service.answer_window(&view.id, true).expect("answer");
                        return true;
                    }
                    std::thread::sleep(Duration::from_millis(5));
                }
                false
            })
        };

        let response = request(
            &socket::path(dir.path()).path,
            json!({
                "v": 1,
                "id": "1",
                "method": "tools/call",
                "params": {
                    "name": "clean_cache",
                    "arguments": { "reportId": "report", "itemIds": ["cache"] },
                    "client": { "name": "Claude\u{7}" }
                }
            }),
        );

        assert_eq!(response["ok"], true, "{response}");
        assert!(
            answerer.join().expect("answer thread"),
            "the dialog must have been shown, so the request reached Writes::run"
        );
        assert_eq!(response["result"]["status"], "queued", "{response}");
        let queued = core.queue.active().expect("active");
        assert_eq!(queued.len(), 1, "{queued:?}");
        assert_eq!(queued[0].op, crate::model::TaskOp::Clean);
        assert_eq!(queued[0].trigger, crate::model::TaskTrigger::Manual);
        broker.shutdown();
    }

    /// The MCP read list is the page's list, not the internal one.
    ///
    /// `updates_list` keeps Homebrew formulae (`updater/service.rs` retains on
    /// source enablement and native-cask ownership only, never on kind) and the
    /// page drops them (`scheduler::visible`, mirrored by the web store's
    /// `shown()`). Both MCP tools go through `updatable_rows`, so this is the one
    /// place that narrowing has to happen — and it must be by `visible`, not by
    /// `eligible`, which would also drop pinned, ignored and skipped rows the
    /// page still draws.
    #[test]
    fn updatable_rows_drops_the_formula_the_page_never_draws() {
        let dir = tempdir();
        let core = crate::core::DesktopCore::open(
            dir.path().join("data"),
            dir.path().join("cache"),
            dir.path().join("askpass"),
            Arc::new(|_| {}),
        )
        .expect("core");
        let row = |kind: crate::model::Kind, token: &str| crate::model::OutdatedItem {
            source: crate::model::UpdateSource::Homebrew,
            app_id: None,
            bundle_id: None,
            channel: None,
            major: false,
            notes: None,
            integrity: None,
            requires_admin: false,
            running: false,
            relaunch_pending: false,
            skipped_version: None,
            critical_update: false,
            informational_url: None,
            download_host: None,
            expected_team_match: None,
            unsigned_update: None,
            kind,
            token: token.into(),
            name: token.into(),
            installed_version: "1.0.0".into(),
            current_version: "1.1.0".into(),
            auto_updates: false,
            pinned: false,
            ignored: false,
            dependents: 0,
            download_size: None,
        };
        *core.updates.write().unwrap() = vec![
            row(crate::model::Kind::Cask, "ghostty"),
            row(crate::model::Kind::Formula, "jq"),
        ];

        let rows = updatable_rows(&core).expect("rows");
        let tokens: Vec<&str> = rows.iter().map(|row| row.token.as_str()).collect();
        assert_eq!(
            tokens,
            vec!["ghostty"],
            "a formula the page never draws must not be offered by the read tool"
        );

        // And the write tool resolves ids against this same list, so a formula
        // id the read tool no longer prints is refused rather than queued.
        let offered: Vec<super::super::tools::UpdatableRow> = rows
            .into_iter()
            .map(|row| super::super::tools::UpdatableRow {
                id: row.id,
                token: row.token,
                name: row.name,
                kind: row.kind,
                source: row.source,
                current_version: row.current_version,
            })
            .collect();
        assert_eq!(
            super::super::tools::plan_update_selection(&offered, &["jq".into()])
                .expect_err("the formula id is not in the list any more")
                .code,
            "E_NOT_FOUND"
        );
    }
}
