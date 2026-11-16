//! Loopback proxy for panel audio and video.
//!
//! A `<video>` or `<audio>` element cannot send the panel session cookie, so the panel
//! mints a path-bound ticket. Putting that ticket URL in the webview would also require
//! `media-src https: http:` and an App Transport Security exception for every host.
//! The webview only ever loads `http://127.0.0.1`, and this process forwards Range
//! requests to the ticket URL.

use std::collections::{HashMap, VecDeque};
use std::io;
use std::net::Ipv4Addr;
use std::sync::{Arc, Mutex, OnceLock};
use std::time::{Duration, Instant};

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};

use super::AppError;

const GRANT_TTL: Duration = Duration::from_secs(60 * 60);
const MAX_GRANTS: usize = 128;
const HEADER_LIMIT: usize = 8 * 1024;

struct Grant {
    upstream: String,
    /// Session cookie for a dashboard that has no `/api/files/ticket`. Never copied into the
    /// loopback URL. `None` when the upstream URL already carries a path-bound ticket.
    cookie: Option<String>,
    expires: Instant,
}

/// Insertion order is the eviction order. `order` is touched on use so a stream that is still
/// playing is not the first grant dropped at the cap.
struct Grants {
    by_token: HashMap<String, Grant>,
    order: VecDeque<String>,
}

impl Grants {
    fn new() -> Self {
        Self {
            by_token: HashMap::new(),
            order: VecDeque::new(),
        }
    }
}

struct Proxy {
    port: u16,
    grants: Arc<Mutex<Grants>>,
}

fn proxy_error(detail: impl Into<String>) -> AppError {
    AppError::new(
        "E_HERMES_MEDIA",
        "Could not prepare this Hermes media file.",
    )
    .with_detail(detail.into())
}

fn proxy() -> Result<&'static Proxy, AppError> {
    static SLOT: OnceLock<Proxy> = OnceLock::new();
    // `get_or_init` only stores one Proxy, but it does not stop the other callers from binding
    // their own listeners first. One start at a time, and a failed start is not cached.
    static START: Mutex<()> = Mutex::new(());
    if let Some(proxy) = SLOT.get() {
        return Ok(proxy);
    }
    let _guard = START
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner());
    if let Some(proxy) = SLOT.get() {
        return Ok(proxy);
    }
    let started = start_proxy()?;
    Ok(SLOT.get_or_init(|| started))
}

fn start_proxy() -> Result<Proxy, AppError> {
    let listener = std::net::TcpListener::bind((Ipv4Addr::LOCALHOST, 0))
        .map_err(|error| proxy_error(error.to_string()))?;
    listener
        .set_nonblocking(true)
        .map_err(|error| proxy_error(error.to_string()))?;
    let port = listener
        .local_addr()
        .map_err(|error| proxy_error(error.to_string()))?
        .port();
    let grants = Arc::new(Mutex::new(Grants::new()));
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .map_err(|error| proxy_error(error.to_string()))?;
    let task_grants = Arc::clone(&grants);
    std::thread::Builder::new()
        .name("hermes-media-proxy".into())
        .spawn(move || {
            runtime.block_on(async move {
                let listener = match TcpListener::from_std(listener) {
                    Ok(listener) => listener,
                    Err(error) => {
                        log::error!("Hermes media proxy failed to start: {error}");
                        return;
                    }
                };
                let client = match stream_client() {
                    Ok(client) => client,
                    Err(error) => {
                        log::error!("Hermes media proxy client failed to start: {error}");
                        return;
                    }
                };
                loop {
                    match listener.accept().await {
                        Ok((socket, _)) => {
                            let grants = Arc::clone(&task_grants);
                            let client = client.clone();
                            tokio::spawn(async move {
                                if let Err(error) = serve_one(socket, grants, client).await {
                                    log::debug!("Hermes media proxy request ended: {error}");
                                }
                            });
                        }
                        Err(error) => {
                            log::error!("Hermes media proxy stopped accepting: {error}");
                            break;
                        }
                    }
                }
            });
        })
        .map_err(|error| proxy_error(error.to_string()))?;
    Ok(Proxy { port, grants })
}

fn stream_client() -> Result<reqwest::Client, AppError> {
    reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .no_proxy()
        .connect_timeout(Duration::from_secs(20))
        .timeout(GRANT_TTL)
        .build()
        .map_err(|error| proxy_error(error.to_string()))
}

/// Remember one panel stream URL and return the loopback URL the webview may load.
#[cfg(test)]
pub(super) fn publish(upstream: String) -> Result<String, AppError> {
    publish_with_cookie(upstream, None)
}

/// `cookie` is sent to the panel and is not placed in the loopback URL. Dashboard surfaces have
/// no file-ticket route, so the stream is fetched with the session cookie instead.
pub(super) fn publish_with_cookie(
    upstream: String,
    cookie: Option<String>,
) -> Result<String, AppError> {
    if !(upstream.starts_with("https://") || upstream.starts_with("http://")) {
        return Err(proxy_error("The panel stream URL is not http(s)."));
    }
    if cookie
        .as_deref()
        .is_some_and(|value| !header_value_is_safe(value))
    {
        return Err(proxy_error(
            "The panel session cookie is not a safe header.",
        ));
    }
    let proxy = proxy()?;
    let token = uuid::Uuid::new_v4().simple().to_string();
    {
        let mut grants = proxy
            .grants
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        admit(
            &mut grants,
            token.clone(),
            Grant {
                upstream,
                cookie,
                expires: Instant::now() + GRANT_TTL,
            },
        );
    }
    Ok(format!("http://127.0.0.1:{}/stream/{token}", proxy.port))
}

fn admit(grants: &mut Grants, token: String, grant: Grant) {
    let now = Instant::now();
    grants.order.retain(|token| {
        grants
            .by_token
            .get(token)
            .is_some_and(|grant| grant.expires > now)
    });
    grants.by_token.retain(|_, grant| grant.expires > now);
    while grants.by_token.len() >= MAX_GRANTS {
        let Some(oldest) = grants.order.pop_front() else {
            break;
        };
        grants.by_token.remove(&oldest);
    }
    grants.order.push_back(token.clone());
    grants.by_token.insert(token, grant);
}

fn lookup(grants: &Mutex<Grants>, token: &str) -> Option<(String, Option<String>)> {
    let mut grants = grants
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner());
    let expires = grants.by_token.get(token)?.expires;
    if expires <= Instant::now() {
        grants.by_token.remove(token);
        grants.order.retain(|item| item != token);
        return None;
    }
    if let Some(index) = grants.order.iter().position(|item| item == token) {
        grants.order.remove(index);
        grants.order.push_back(token.to_owned());
    }
    let grant = grants.by_token.get(token)?;
    Some((grant.upstream.clone(), grant.cookie.clone()))
}

async fn serve_one(
    mut socket: TcpStream,
    grants: Arc<Mutex<Grants>>,
    client: reqwest::Client,
) -> io::Result<()> {
    let _ = socket.set_nodelay(true);
    let head = read_head(&mut socket).await?;
    let Ok(text) = std::str::from_utf8(&head) else {
        return write_fixed(&mut socket, 400, "Bad Request", b"bad request").await;
    };
    let Some((prelude, _)) = text.split_once("\r\n\r\n") else {
        return write_fixed(&mut socket, 400, "Bad Request", b"bad request").await;
    };
    let mut lines = prelude.split("\r\n");
    let Some(request) = lines.next() else {
        return write_fixed(&mut socket, 400, "Bad Request", b"bad request").await;
    };
    let mut parts = request.split(' ');
    let (Some(method), Some(target), Some(version)) = (parts.next(), parts.next(), parts.next())
    else {
        return write_fixed(&mut socket, 400, "Bad Request", b"bad request").await;
    };
    if version != "HTTP/1.0" && version != "HTTP/1.1" {
        return write_fixed(&mut socket, 400, "Bad Request", b"bad request").await;
    }
    if method != "GET" && method != "HEAD" {
        return write_fixed(&mut socket, 405, "Method Not Allowed", b"method").await;
    }
    let Some(token) = stream_token(target) else {
        return write_fixed(&mut socket, 404, "Not Found", b"not found").await;
    };
    let Some((upstream, cookie)) = lookup(&grants, token) else {
        return write_fixed(&mut socket, 404, "Not Found", b"not found").await;
    };
    let range = lines.find_map(|line| {
        let (name, value) = line.split_once(':')?;
        name.eq_ignore_ascii_case("range")
            .then(|| value.trim().to_owned())
            .filter(|value| !value.is_empty() && header_value_is_safe(value))
    });
    let reqwest_method = if method == "HEAD" {
        reqwest::Method::HEAD
    } else {
        reqwest::Method::GET
    };
    let mut request = client.request(reqwest_method, &upstream);
    if let Some(cookie) = cookie.as_deref() {
        request = request.header(reqwest::header::COOKIE, cookie);
    }
    if let Some(range) = range.as_deref() {
        request = request.header(reqwest::header::RANGE, range);
    }
    let response = match request.send().await {
        Ok(response) => response,
        Err(error) => {
            log::debug!("Hermes media proxy upstream failed: {error}");
            return write_fixed(&mut socket, 502, "Bad Gateway", b"upstream").await;
        }
    };
    tunnel(socket, method, response).await
}

fn stream_token(target: &str) -> Option<&str> {
    let path = target
        .split_once('?')
        .map(|(path, _)| path)
        .unwrap_or(target);
    let token = path.strip_prefix("/stream/")?;
    if token.len() == 32 && token.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        Some(token)
    } else {
        None
    }
}

fn header_value_is_safe(value: &str) -> bool {
    value
        .bytes()
        .all(|byte| byte == b'\t' || (b' '..=b'~').contains(&byte))
}

async fn read_head(socket: &mut TcpStream) -> io::Result<Vec<u8>> {
    let mut buf = Vec::with_capacity(1024);
    let mut tmp = [0u8; 1024];
    let deadline = tokio::time::Instant::now() + Duration::from_secs(5);
    loop {
        if buf.windows(4).any(|window| window == b"\r\n\r\n") {
            return Ok(buf);
        }
        if buf.len() >= HEADER_LIMIT {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "media proxy header is too large",
            ));
        }
        let remaining = deadline.saturating_duration_since(tokio::time::Instant::now());
        if remaining.is_zero() {
            return Err(io::Error::new(
                io::ErrorKind::TimedOut,
                "media proxy header timed out",
            ));
        }
        let read = tokio::time::timeout(remaining, socket.read(&mut tmp))
            .await
            .map_err(|_| {
                io::Error::new(io::ErrorKind::TimedOut, "media proxy header timed out")
            })??;
        if read == 0 {
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "media proxy header ended early",
            ));
        }
        buf.extend_from_slice(&tmp[..read]);
    }
}

async fn write_fixed(
    socket: &mut TcpStream,
    status: u16,
    reason: &str,
    body: &[u8],
) -> io::Result<()> {
    let head = format!(
        "HTTP/1.1 {status} {reason}\r\nConnection: close\r\nContent-Type: text/plain\r\nContent-Length: {}\r\nCache-Control: no-store\r\n\r\n",
        body.len()
    );
    socket.write_all(head.as_bytes()).await?;
    socket.write_all(body).await?;
    Ok(())
}

async fn tunnel(
    mut socket: TcpStream,
    method: &str,
    mut response: reqwest::Response,
) -> io::Result<()> {
    let status = response.status();
    let mut head = format!(
        "HTTP/1.1 {} {}\r\nConnection: close\r\nCache-Control: no-store\r\n",
        status.as_u16(),
        status.canonical_reason().unwrap_or("OK")
    );
    for name in ["content-type", "content-range", "accept-ranges"] {
        if let Some(value) = response
            .headers()
            .get(name)
            .and_then(|value| value.to_str().ok())
            .filter(|value| header_value_is_safe(value))
        {
            head.push_str(name);
            head.push_str(": ");
            head.push_str(value);
            head.push_str("\r\n");
        }
    }
    if method == "HEAD" || status.as_u16() == 204 || status.as_u16() == 304 {
        let length = response.content_length().unwrap_or(0);
        head.push_str(&format!("Content-Length: {length}\r\n\r\n"));
        socket.write_all(head.as_bytes()).await?;
        return Ok(());
    }
    if let Some(length) = response.content_length() {
        head.push_str(&format!("Content-Length: {length}\r\n\r\n"));
        socket.write_all(head.as_bytes()).await?;
        while let Some(chunk) = response.chunk().await.map_err(io::Error::other)? {
            socket.write_all(&chunk).await?;
        }
        return Ok(());
    }
    head.push_str("Transfer-Encoding: chunked\r\n\r\n");
    socket.write_all(head.as_bytes()).await?;
    while let Some(chunk) = response.chunk().await.map_err(io::Error::other)? {
        socket
            .write_all(format!("{:x}\r\n", chunk.len()).as_bytes())
            .await?;
        socket.write_all(&chunk).await?;
        socket.write_all(b"\r\n").await?;
    }
    socket.write_all(b"0\r\n\r\n").await?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    async fn read_response(stream: &mut TcpStream) -> String {
        let mut buf = Vec::new();
        let _ = tokio::time::timeout(Duration::from_secs(2), async {
            let mut tmp = [0u8; 1024];
            loop {
                match stream.read(&mut tmp).await {
                    Ok(0) | Err(_) => break,
                    Ok(n) => buf.extend_from_slice(&tmp[..n]),
                }
            }
        })
        .await;
        String::from_utf8_lossy(&buf).into_owned()
    }

    async fn mock_upstream() -> String {
        let listener = TcpListener::bind((Ipv4Addr::LOCALHOST, 0)).await.unwrap();
        let port = listener.local_addr().unwrap().port();
        tokio::spawn(async move {
            let (mut socket, _) = listener.accept().await.unwrap();
            let mut buf = vec![0u8; 2048];
            let n = socket.read(&mut buf).await.unwrap();
            let text = String::from_utf8_lossy(&buf[..n]).to_ascii_lowercase();
            let (status, extra, body) = if text.contains("range: bytes=0-3") {
                (
                    "206 Partial Content",
                    "Content-Range: bytes 0-3/11\r\n",
                    &b"helo"[..],
                )
            } else {
                ("200 OK", "", &b"hello-media"[..])
            };
            let head = format!(
                "HTTP/1.1 {status}\r\nContent-Type: video/mp4\r\nAccept-Ranges: bytes\r\n{extra}Content-Length: {}\r\nConnection: close\r\n\r\n",
                body.len()
            );
            socket.write_all(head.as_bytes()).await.unwrap();
            socket.write_all(body).await.unwrap();
        });
        format!("http://127.0.0.1:{port}/api/files/stream?path=/secret&ticket=sekret")
    }

    async fn request(url: &str, range: Option<&str>) -> String {
        let parsed = reqwest::Url::parse(url).unwrap();
        let mut stream = TcpStream::connect(format!(
            "{}:{}",
            parsed.host_str().unwrap(),
            parsed.port().unwrap()
        ))
        .await
        .unwrap();
        let range = range
            .map(|value| format!("Range: {value}\r\n"))
            .unwrap_or_default();
        let payload = format!(
            "GET {} HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n{range}\r\n",
            parsed.path()
        );
        stream.write_all(payload.as_bytes()).await.unwrap();
        read_response(&mut stream).await
    }

    #[tokio::test]
    async fn proxies_a_panel_stream_without_exposing_the_ticket() {
        let upstream = mock_upstream().await;
        let local = publish(upstream).unwrap();
        assert!(local.starts_with("http://127.0.0.1:"));
        assert!(!local.contains("sekret"));
        assert!(!local.contains("/api/files"));
        let response = request(&local, None).await;
        assert!(response.starts_with("HTTP/1.1 200 OK"), "{response}");
        assert!(response.contains("hello-media"), "{response}");
        assert!(
            response
                .to_ascii_lowercase()
                .contains("content-type: video/mp4")
        );
    }

    #[tokio::test]
    async fn forwards_range_requests_for_seeking() {
        let upstream = mock_upstream().await;
        let local = publish(upstream).unwrap();
        let response = request(&local, Some("bytes=0-3")).await;
        assert!(
            response.starts_with("HTTP/1.1 206 Partial Content"),
            "{response}"
        );
        assert!(response.contains("helo"), "{response}");
    }

    #[tokio::test]
    async fn unknown_tokens_do_not_reach_upstream() {
        let _ = publish("http://127.0.0.1:1/secret-ticket".into()).unwrap();
        let proxy = proxy().unwrap();
        let url = format!(
            "http://127.0.0.1:{}/stream/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            proxy.port
        );
        let response = request(&url, None).await;
        assert!(response.starts_with("HTTP/1.1 404"), "{response}");
    }

    #[tokio::test]
    async fn session_cookie_stays_out_of_the_loopback_url() {
        let listener = TcpListener::bind((Ipv4Addr::LOCALHOST, 0)).await.unwrap();
        let port = listener.local_addr().unwrap().port();
        tokio::spawn(async move {
            let (mut socket, _) = listener.accept().await.unwrap();
            let mut buf = vec![0u8; 2048];
            let n = socket.read(&mut buf).await.unwrap();
            let text = String::from_utf8_lossy(&buf[..n]).to_ascii_lowercase();
            let body: &[u8] = if text.contains("cookie: hermes_session_at=sekret") {
                b"cookie-seen"
            } else {
                b"no-cookie"
            };
            let head = format!(
                "HTTP/1.1 200 OK\r\nContent-Type: video/mp4\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                body.len()
            );
            socket.write_all(head.as_bytes()).await.unwrap();
            socket.write_all(body).await.unwrap();
        });
        let local = publish_with_cookie(
            format!("http://127.0.0.1:{port}/api/files/stream?path=/clip.mp4"),
            Some("hermes_session_at=sekret; hermes_session_rt=other".into()),
        )
        .unwrap();
        assert!(!local.contains("sekret"), "{local}");
        assert!(!local.contains("hermes_session"), "{local}");
        let response = request(&local, None).await;
        assert!(response.contains("cookie-seen"), "{response}");
    }

    #[test]
    fn full_grant_table_drops_the_least_recently_used() {
        let mut grants = Grants::new();
        for index in 0..MAX_GRANTS {
            admit(
                &mut grants,
                format!("{index:032x}"),
                Grant {
                    upstream: format!("http://127.0.0.1/{index}"),
                    cookie: None,
                    expires: Instant::now() + GRANT_TTL,
                },
            );
        }
        let oldest = "00000000000000000000000000000000";
        let grants = Mutex::new(grants);
        assert!(lookup(&grants, oldest).is_some());
        let mut grants = grants.into_inner().expect("grant lock");
        let fresh = "ffffffffffffffffffffffffffffffff";
        admit(
            &mut grants,
            fresh.to_owned(),
            Grant {
                upstream: "http://127.0.0.1/fresh".into(),
                cookie: None,
                expires: Instant::now() + GRANT_TTL,
            },
        );
        assert!(grants.by_token.contains_key(oldest));
        assert!(grants.by_token.contains_key(fresh));
        assert!(
            !grants
                .by_token
                .contains_key("00000000000000000000000000000001")
        );
        assert_eq!(grants.by_token.len(), MAX_GRANTS);
    }
}
