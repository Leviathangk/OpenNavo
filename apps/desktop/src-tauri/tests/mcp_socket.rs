//! Integration tests for the Local MCP Unix-socket broker.
//!
//! The broker tests run against the real public broker in a tempdir. The two
//! subprocess tests spawn the shipped binary with `mcp` and a temp data dir so
//! the `E_APP_NOT_RUNNING` and `E_MCP_DISABLED` fallbacks are exercised end to
//! end without touching the developer's real app data.

use std::{
    fs,
    io::{BufRead, BufReader, ErrorKind, Read, Write},
    os::unix::{
        fs::{FileTypeExt, MetadataExt, PermissionsExt},
        net::{UnixListener, UnixStream},
    },
    path::Path,
    process::{Child, Command, ExitStatus, Stdio},
    sync::{
        Arc,
        atomic::{AtomicBool, AtomicUsize, Ordering},
    },
    thread,
    time::{Duration, Instant},
};

use opennavo_desktop_lib::mcp::{
    broker::{Broker, MAX_REQUEST_BYTES},
    client::{CallError, Client},
    socket, state,
    tools::{ToolError, ToolHost},
};
use serde_json::{Value, json};

const INITIALIZE: &str = concat!(
    r#"{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"test","version":"1"}}}"#,
    "\n"
);

fn tempdir() -> tempfile::TempDir {
    tempfile::tempdir().expect("tempdir")
}

fn enable(dir: &Path) {
    state::save(dir, true).expect("save enabled state");
}

fn wait_running(broker: &Arc<Broker>) {
    let deadline = Instant::now() + Duration::from_secs(5);
    while Instant::now() < deadline {
        if broker.status().running {
            return;
        }
        thread::sleep(Duration::from_millis(5));
    }
    panic!("broker did not start: {:?}", broker.status());
}

fn start_broker(dir: &Path) -> Arc<Broker> {
    let broker = Arc::new(Broker::new(dir.to_path_buf()));
    broker.set_enabled(true).expect("enable broker");
    wait_running(&broker);
    broker
}

fn start_broker_with_host(dir: &Path, host: Arc<dyn ToolHost>) -> Arc<Broker> {
    let broker = Arc::new(Broker::with_host(dir.to_path_buf(), Some(host)));
    broker.set_enabled(true).expect("enable broker");
    wait_running(&broker);
    broker
}

fn request(path: &Path, body: Value) -> Value {
    let mut stream = UnixStream::connect(path).expect("connect to broker");
    stream
        .set_read_timeout(Some(Duration::from_secs(5)))
        .expect("read timeout");
    stream
        .set_write_timeout(Some(Duration::from_secs(5)))
        .expect("write timeout");
    serde_json::to_writer(&mut stream, &body).expect("write request");
    stream.write_all(b"\n").expect("write newline");
    let mut reader = BufReader::new(stream);
    let mut line = String::new();
    reader.read_line(&mut line).expect("read response");
    assert!(
        line.ends_with('\n'),
        "broker response must be newline terminated: {line:?}"
    );
    serde_json::from_str(&line).expect("response is JSON")
}

fn tool_call(name: &str, arguments: Value) -> Value {
    json!({
        "v": 1,
        "id": name,
        "method": "tools/call",
        "params": { "name": name, "arguments": arguments }
    })
}

#[test]
fn socket_is_created_with_0700_dir_and_0600_socket() {
    let dir = tempdir();
    let broker = start_broker(dir.path());
    let resolved = socket::path(dir.path());

    let directory = fs::metadata(&resolved.directory).expect("directory metadata");
    let socket_file = fs::symlink_metadata(&resolved.path).expect("socket metadata");
    assert_eq!(
        directory.permissions().mode() & 0o777,
        socket::DIRECTORY_MODE
    );
    assert_eq!(
        socket_file.permissions().mode() & 0o777,
        socket::SOCKET_MODE
    );
    assert!(socket_file.file_type().is_socket());
    assert_eq!(directory.uid(), socket::current_uid());
    assert_eq!(socket_file.uid(), socket::current_uid());
    broker.shutdown();
}

#[test]
fn stale_socket_is_replaced_only_when_owned_by_current_uid() {
    let dir = tempdir();
    let resolved = socket::path(dir.path());
    state::ensure_private_dir(&resolved.directory).expect("socket directory");

    // Dropping a listener leaves the socket path behind, like a crashed app.
    let stale = UnixListener::bind(&resolved.path).expect("bind stale socket");
    drop(stale);
    assert!(resolved.path.exists(), "stale socket should remain on disk");

    enable(dir.path());
    let broker = Arc::new(Broker::new(dir.path().to_path_buf()));
    broker
        .start_if_enabled()
        .expect("a stale socket owned by this uid must be replaced");
    wait_running(&broker);
    let response = request(
        &resolved.path,
        json!({"v": 1, "id": "ping", "method": "ping", "params": {}}),
    );
    assert_eq!(response["ok"], true);
    broker.shutdown();

    // A live socket is never stale, even when it belongs to this uid.
    let live_dir = tempdir();
    let live = start_broker(live_dir.path());
    let second = Arc::new(Broker::new(live_dir.path().to_path_buf()));
    assert!(
        second.start_if_enabled().is_err(),
        "a live socket must not be replaced"
    );
    assert!(!second.status().running);
    assert!(live.status().running);
    live.shutdown();

    // Ownership itself is checked in `socket::prepare` before any unlink. An
    // unprivileged test cannot create a socket owned by another uid; the
    // non-socket/refusal case is covered by the regular-file test below.
}

#[test]
fn regular_file_at_socket_path_is_not_deleted() {
    let dir = tempdir();
    let resolved = socket::path(dir.path());
    state::ensure_private_dir(&resolved.directory).expect("socket directory");
    fs::write(&resolved.path, b"do not delete").expect("write regular file");
    enable(dir.path());

    let broker = Arc::new(Broker::new(dir.path().to_path_buf()));
    broker
        .start_if_enabled()
        .expect_err("a regular file must never be treated as a stale socket");
    assert!(!broker.status().running);
    assert!(broker.status().error.is_some());
    assert_eq!(
        fs::read(&resolved.path).expect("regular file still exists"),
        b"do not delete"
    );
}

#[derive(Default)]
struct MockHost {
    searches: AtomicUsize,
    tasks: AtomicUsize,
}

impl ToolHost for MockHost {
    fn search_catalog(
        &self,
        query: &str,
        kind: Option<&str>,
        limit: u32,
    ) -> Result<Value, ToolError> {
        assert_eq!(query, "firefox");
        assert_eq!(kind, Some("cask"));
        assert_eq!(limit, 7);
        self.searches.fetch_add(1, Ordering::SeqCst);
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
        self.tasks.fetch_add(1, Ordering::SeqCst);
        Ok(json!({ "task": { "id": "task-1", "state": "running" } }))
    }
}

#[test]
fn broker_round_trips_search_and_get_task() {
    let dir = tempdir();
    let host = Arc::new(MockHost::default());
    let broker = start_broker_with_host(dir.path(), host.clone());
    let mut client = Client::new(dir.path().to_path_buf());

    let search = client
        .call(
            "tools/call",
            json!({
                "name": "search_catalog",
                "arguments": { "query": "firefox", "kind": "cask", "limit": 7 }
            }),
        )
        .expect("search result");
    assert_eq!(search["untrusted"], true);
    assert_eq!(search["items"][0]["untrusted"], true);
    assert_eq!(search["items"][0]["item"]["untrusted"], true);

    let task = client
        .call(
            "tools/call",
            json!({ "name": "get_task", "arguments": { "id": "task-1" } }),
        )
        .expect("task result");
    assert_eq!(task["task"]["state"], "running");

    assert_eq!(host.searches.load(Ordering::SeqCst), 1);
    assert_eq!(host.tasks.load(Ordering::SeqCst), 1);
    broker.shutdown();
}

#[test]
#[allow(non_snake_case)]
fn app_not_running_maps_to_E_APP_NOT_RUNNING() {
    let dir = tempdir();
    enable(dir.path());
    let mut client = Client::new(dir.path().to_path_buf());

    let error = client
        .call(
            "tools/call",
            json!({ "name": "search_catalog", "arguments": { "query": "firefox" } }),
        )
        .expect_err("no broker socket exists");
    assert_eq!(error, CallError::AppNotRunning);
    assert_eq!(error.data_code(), "E_APP_NOT_RUNNING");
    assert_eq!(
        error.message(),
        "OpenNavo is not running. Please open OpenNavo and try again."
    );
    assert_eq!(error.jsonrpc_code(), -32002);
}

#[test]
#[allow(non_snake_case)]
fn master_switch_off_creates_no_socket_and_returns_E_MCP_DISABLED() {
    let dir = tempdir();
    let broker = Arc::new(Broker::new(dir.path().to_path_buf()));
    broker
        .start_if_enabled()
        .expect("start with the switch off");
    assert!(!broker.status().running);
    assert!(!socket::path(dir.path()).path.exists());

    let mut client = Client::new(dir.path().to_path_buf());
    let error = client
        .call(
            "tools/call",
            json!({ "name": "search_catalog", "arguments": { "query": "firefox" } }),
        )
        .expect_err("the switch is off");
    assert_eq!(error, CallError::Disabled);
    assert_eq!(error.data_code(), "E_MCP_DISABLED");
    assert!(!socket::path(dir.path()).path.exists());
}

#[test]
fn turning_master_switch_off_disconnects_existing_connection() {
    let dir = tempdir();
    let broker = start_broker(dir.path());
    let path = socket::path(dir.path()).path;

    let stream = UnixStream::connect(&path).expect("connect");
    stream
        .set_read_timeout(Some(Duration::from_secs(5)))
        .expect("read timeout");
    let mut writer = stream.try_clone().expect("clone stream");
    let mut reader = BufReader::new(stream);
    serde_json::to_writer(
        &mut writer,
        &json!({"v": 1, "id": "ping", "method": "ping", "params": {}}),
    )
    .expect("write ping");
    writer.write_all(b"\n").expect("write newline");
    let mut line = String::new();
    reader.read_line(&mut line).expect("read ping response");
    assert_eq!(
        serde_json::from_str::<Value>(&line).expect("ping response")["ok"],
        true
    );

    broker.set_enabled(false).expect("disable broker");
    assert!(!broker.status().running);
    assert!(!path.exists(), "the socket file must disappear immediately");

    let mut byte = [0u8; 1];
    match reader.read(&mut byte) {
        Ok(0) => {}
        Ok(read) => panic!("existing connection still delivered {read} bytes"),
        Err(error)
            if matches!(
                error.kind(),
                ErrorKind::ConnectionReset
                    | ErrorKind::ConnectionAborted
                    | ErrorKind::NotConnected
                    | ErrorKind::UnexpectedEof
            ) => {}
        Err(error) => panic!("unexpected disconnect error: {error}"),
    }
}

#[test]
fn oversized_request_is_rejected_without_killing_broker() {
    let dir = tempdir();
    let broker = start_broker(dir.path());
    let path = socket::path(dir.path()).path;

    let stream = UnixStream::connect(&path).expect("connect");
    stream
        .set_read_timeout(Some(Duration::from_secs(10)))
        .expect("read timeout");
    let mut writer = stream.try_clone().expect("clone stream");
    let writer = thread::spawn(move || {
        let chunk = vec![b'x'; 64 * 1024];
        let mut remaining = MAX_REQUEST_BYTES + 1;
        while remaining > 0 {
            let take = remaining.min(chunk.len());
            if writer.write_all(&chunk[..take]).is_err() {
                return;
            }
            remaining -= take;
        }
        let _ = writer.write_all(b"\n");
    });

    let mut reader = BufReader::new(stream);
    let mut line = String::new();
    reader.read_line(&mut line).expect("error response");
    let response: Value = serde_json::from_str(&line).expect("error JSON");
    assert_eq!(response["error"]["code"], "E_REQUEST_TOO_LARGE");
    writer.join().expect("oversized writer thread");

    let ping = request(
        &path,
        json!({"v": 1, "id": "after", "method": "ping", "params": {}}),
    );
    assert_eq!(ping["ok"], true);
    broker.shutdown();
}

#[test]
fn unsupported_method_returns_jsonrpc_error() {
    let dir = tempdir();
    let broker = start_broker(dir.path());

    let response = request(
        &socket::path(dir.path()).path,
        json!({"v": 1, "id": "unsupported", "method": "no/such/method", "params": {}}),
    );
    assert_eq!(response["id"], "unsupported");
    assert_eq!(response["ok"], false);
    assert_eq!(response["error"]["code"], "E_METHOD_NOT_FOUND");
    assert!(response["error"]["message"].is_string());
    broker.shutdown();
}

#[test]
fn read_tools_do_not_expose_write_names() {
    let dir = tempdir();
    // A tool host is required to exercise dispatch: `Broker::new` has no host
    // and deliberately answers every `tools/call` with E_MCP_DISABLED, which
    // says nothing about unknown or write-tool names. The switch itself is
    // already on via `start_broker_with_host`.
    let host = Arc::new(MockHost::default());
    let broker = start_broker_with_host(dir.path(), host);
    let path = socket::path(dir.path()).path;

    let response = request(&path, json!({"v": 1, "id": "list", "method": "tools/list"}));
    assert_eq!(response["ok"], true);
    let names: Vec<&str> = response["result"]["tools"]
        .as_array()
        .expect("tools array")
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
        "get_latest_scan_result",
    ] {
        assert!(
            !names.contains(&forbidden),
            "{forbidden} must not be listed"
        );
        let response = request(&path, tool_call(forbidden, json!({})));
        assert_eq!(response["ok"], false, "name {forbidden}");
        // Write tools exist but are refused while the write switch is off.
        let expected = if forbidden == "get_latest_scan_result" {
            "E_TOOL_NOT_FOUND"
        } else {
            "E_MCP_WRITE_DISABLED"
        };
        assert_eq!(response["error"]["code"], expected, "name {forbidden}");
    }
    broker.shutdown();
}

/// The app-side `LiveTools::list_updatable_apps` reads `DesktopCore::updates_list`
/// (cached results) and has no update-check/scan entry point. This guards the
/// dispatch layer: the call reaches only the cached-list method, never catalog
/// search or any other host operation that could start work.
#[derive(Default)]
struct CachedUpdatesHost {
    updatable_calls: AtomicUsize,
    unexpected_work: AtomicBool,
}

impl ToolHost for CachedUpdatesHost {
    fn search_catalog(
        &self,
        _query: &str,
        _kind: Option<&str>,
        _limit: u32,
    ) -> Result<Value, ToolError> {
        self.unexpected_work.store(true, Ordering::SeqCst);
        Err(ToolError::internal("catalog search must not run"))
    }

    fn get_catalog_item(&self, _kind: &str, _token: &str) -> Result<Value, ToolError> {
        self.unexpected_work.store(true, Ordering::SeqCst);
        Err(ToolError::internal("catalog read must not run"))
    }

    fn list_installed_apps(&self) -> Result<Value, ToolError> {
        self.unexpected_work.store(true, Ordering::SeqCst);
        Err(ToolError::internal("installed-app scan must not run"))
    }

    fn list_updatable_apps(&self) -> Result<Value, ToolError> {
        self.updatable_calls.fetch_add(1, Ordering::SeqCst);
        Ok(json!({
            "apps": [{
                "id": "cached",
                "path": "/Applications/Cached.app",
                "name": "Cached",
                "currentVersion": "1.0.0",
                "latestVersion": "2.0.0"
            }]
        }))
    }

    fn get_task(&self, _id: &str) -> Result<Value, ToolError> {
        self.unexpected_work.store(true, Ordering::SeqCst);
        Err(ToolError::internal("task read must not run"))
    }
}

#[test]
fn updatable_apps_tool_does_not_trigger_scan() {
    let dir = tempdir();
    let host = Arc::new(CachedUpdatesHost::default());
    let broker = start_broker_with_host(dir.path(), host.clone());
    let mut client = Client::new(dir.path().to_path_buf());

    let result = client
        .call(
            "tools/call",
            json!({ "name": "list_updatable_apps", "arguments": {} }),
        )
        .expect("cached updatable apps");
    assert_eq!(result["apps"][0]["id"], "cached");
    assert_eq!(result["apps"][0]["currentVersion"], "1.0.0");
    assert_eq!(result["apps"][0]["latestVersion"], "2.0.0");
    assert_eq!(host.updatable_calls.load(Ordering::SeqCst), 1);
    assert!(
        !host.unexpected_work.load(Ordering::SeqCst),
        "list_updatable_apps must not start a scan or other host work"
    );
    broker.shutdown();
}

fn run_mcp_subprocess(data_dir: &Path, home: &Path, input: &str) -> ChildRun {
    let mut child = Command::new(env!("CARGO_BIN_EXE_opennavo-desktop"))
        .arg("mcp")
        .env("OPENNAVO_MCP_DATA_DIR", data_dir)
        .env("HOME", home)
        .env_remove("XDG_DATA_HOME")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("spawn the app binary in mcp mode");

    // Drain both pipes concurrently with the child's lifetime. Reading only
    // after `wait` would deadlock once either pipe's kernel buffer fills.
    let mut stdout_pipe = child.stdout.take().expect("child stdout is piped");
    let stdout_reader = thread::spawn(move || {
        let mut stdout = String::new();
        stdout_pipe
            .read_to_string(&mut stdout)
            .expect("read child stdout");
        stdout
    });
    let mut stderr_pipe = child.stderr.take().expect("child stderr is piped");
    let stderr_reader = thread::spawn(move || {
        let mut stderr = String::new();
        stderr_pipe
            .read_to_string(&mut stderr)
            .expect("read child stderr");
        stderr
    });

    let mut stdin = child.stdin.take().expect("child stdin is piped");
    let write_result = stdin
        .write_all(input.as_bytes())
        .and_then(|_| stdin.flush());
    drop(stdin);

    let status = wait_with_timeout(&mut child, Duration::from_secs(20));
    let stdout = stdout_reader.join().expect("stdout reader thread panicked");
    let stderr = stderr_reader.join().expect("stderr reader thread panicked");
    if let Err(error) = write_result {
        panic!("could not write to mcp stdin: {error}; stderr: {stderr}");
    }
    ChildRun {
        status,
        stdout,
        stderr,
    }
}

struct ChildRun {
    status: ExitStatus,
    stdout: String,
    stderr: String,
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
        thread::sleep(Duration::from_millis(10));
    }
}

fn subprocess_data_dir(
    enabled: bool,
) -> (tempfile::TempDir, std::path::PathBuf, std::path::PathBuf) {
    let root = tempdir();
    let data = root.path().join("data");
    state::save(&data, enabled).expect("save data-dir state");

    // The env override is debug-only; point HOME at the same tempdir so a
    // release-mode test also resolves the state file below instead of the
    // developer's real Application Support directory.
    let home = root.path().join("home");
    fs::create_dir_all(&home).expect("create fake home");
    #[cfg(target_os = "macos")]
    let fallback = home
        .join("Library/Application Support")
        .join(state::BUNDLE_ID);
    #[cfg(not(target_os = "macos"))]
    let fallback = home.join(".local/share").join(state::BUNDLE_ID);
    state::save(&fallback, enabled).expect("save fallback state");

    (root, data, home)
}

fn tools_call(name: &str, arguments: Value) -> String {
    format!(
        "{}\n",
        json!({
            "jsonrpc": "2.0",
            "id": 2,
            "method": "tools/call",
            "params": { "name": name, "arguments": arguments }
        })
    )
}

fn parse_stdout(run: &ChildRun) -> Vec<Value> {
    assert!(
        run.status.success(),
        "mcp mode must exit 0, got {:?}; stderr: {}",
        run.status,
        run.stderr
    );
    assert!(
        run.stdout.ends_with('\n'),
        "stdout must be newline-terminated protocol JSON: {:?}",
        run.stdout
    );
    run.stdout
        .lines()
        .map(|line| {
            serde_json::from_str(line)
                .unwrap_or_else(|error| panic!("stdout line is not JSON ({error}): {line:?}"))
        })
        .collect()
}

#[test]
#[allow(non_snake_case)]
fn subprocess_maps_app_not_running_to_E_APP_NOT_RUNNING() {
    let (_root, data, home) = subprocess_data_dir(true);
    assert!(
        !socket::path(&data).path.exists(),
        "the app is not running, so no socket may exist"
    );

    let input = format!(
        "{INITIALIZE}{}",
        tools_call("search_catalog", json!({ "query": "firefox" }))
    );
    let run = run_mcp_subprocess(&data, &home, &input);
    let responses = parse_stdout(&run);
    assert_eq!(
        responses.len(),
        2,
        "initialize and tools/call each produce one response"
    );
    assert_eq!(responses[0]["result"]["protocolVersion"], "2025-06-18");

    let error = &responses[1]["error"];
    assert_eq!(error["data"]["code"], "E_APP_NOT_RUNNING");
    assert_eq!(
        error["message"],
        "OpenNavo is not running. Please open OpenNavo and try again."
    );
}

#[test]
#[allow(non_snake_case)]
fn subprocess_switch_off_returns_E_MCP_DISABLED() {
    let (_root, data, home) = subprocess_data_dir(false);
    assert!(!socket::path(&data).path.exists());

    let input = format!(
        "{INITIALIZE}{}",
        tools_call("search_catalog", json!({ "query": "firefox" }))
    );
    let run = run_mcp_subprocess(&data, &home, &input);
    let responses = parse_stdout(&run);
    assert_eq!(responses.len(), 2);

    let error = &responses[1]["error"];
    assert_eq!(error["data"]["code"], "E_MCP_DISABLED");
    assert!(
        error["message"]
            .as_str()
            .is_some_and(|message| message.contains("Local MCP is turned off")),
        "unexpected disabled message: {error}"
    );
    assert!(!socket::path(&data).path.exists());
}

#[test]
fn failed_second_broker_does_not_remove_the_live_socket() {
    let dir = tempdir();
    let first = start_broker(dir.path());
    let path = socket::path(dir.path()).path;

    let second = Arc::new(Broker::new(dir.path().to_path_buf()));
    assert!(second.set_enabled(true).is_err(), "second bind must fail");
    second.shutdown();
    drop(second);

    assert!(
        fs::symlink_metadata(&path)
            .expect("socket still present")
            .file_type()
            .is_socket()
    );
    let response = request(&path, json!({"v":1,"id":"ping","method":"ping"}));
    assert_eq!(response["ok"], true);
    first.shutdown();
    assert!(
        fs::symlink_metadata(&path).is_err(),
        "owner removes its socket"
    );
}

#[test]
fn published_socket_never_has_broad_permissions_and_no_temp_left() {
    let dir = tempdir();
    let broker = start_broker(dir.path());
    let resolved = socket::path(dir.path());
    let metadata = fs::symlink_metadata(&resolved.path).expect("socket");
    assert_eq!(metadata.permissions().mode() & 0o777, socket::SOCKET_MODE);
    let entries: Vec<String> = fs::read_dir(&resolved.directory)
        .expect("dir")
        .filter_map(|entry| entry.ok())
        .map(|entry| entry.file_name().to_string_lossy().into_owned())
        .filter(|name| name.starts_with('.') && !name.starts_with(".state-"))
        .collect();
    assert!(entries.is_empty(), "temporary sockets left: {entries:?}");
    broker.shutdown();
}

#[test]
fn overlapping_toggles_keep_runtime_and_persisted_switch_in_sync() {
    let dir = tempdir();
    let broker = Arc::new(Broker::new(dir.path().to_path_buf()));
    let workers: Vec<_> = (0..8)
        .map(|index| {
            let broker = broker.clone();
            thread::spawn(move || {
                for round in 0..10 {
                    let _ = broker.set_enabled((index + round) % 2 == 0);
                }
            })
        })
        .collect();
    for worker in workers {
        worker.join().expect("toggle thread");
    }
    let status = broker.status();
    assert_eq!(status.enabled, state::load(dir.path()).enabled);
    assert_eq!(status.running, status.enabled);
    assert_eq!(
        socket::path(dir.path()).path.exists(),
        status.enabled,
        "socket presence follows the switch"
    );
    broker.shutdown();
}

#[test]
fn connection_from_previous_cycle_is_not_served_after_disable() {
    let dir = tempdir();
    let broker = start_broker(dir.path());
    let path = socket::path(dir.path()).path;
    let mut old = UnixStream::connect(&path).expect("connect");
    old.set_read_timeout(Some(Duration::from_secs(5)))
        .expect("timeout");
    old.write_all(b"{\"v\":1,\"id\":1,\"method\":\"ping\"}\n")
        .expect("write");
    let mut reader = BufReader::new(old.try_clone().expect("clone"));
    let mut line = String::new();
    reader.read_line(&mut line).expect("first reply");
    assert!(line.contains("\"ok\":true"));

    broker.set_enabled(false).expect("disable");
    broker.set_enabled(true).expect("re-enable");
    wait_running(&broker);

    let _ = old.write_all(b"{\"v\":1,\"id\":2,\"method\":\"ping\"}\n");
    line.clear();
    let read = reader.read_line(&mut line);
    assert!(
        matches!(read, Ok(0)) || read.is_err(),
        "old connection must not be served: {line:?}"
    );
    // New connections still work after re-enable.
    let response = request(&path, json!({"v":1,"id":"new","method":"ping"}));
    assert_eq!(response["ok"], true);
    broker.shutdown();
}
