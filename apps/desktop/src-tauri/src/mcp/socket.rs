//! Unix-socket path, permission, stale-socket and peer-identity handling.
//!
//! Everything here is Tauri-free so the same rules can be exercised from a
//! scratch crate. The socket directory is `0700`, the socket `0600`, and the
//! server only accepts peers with the current effective uid.

use std::{
    fs,
    io::{self, ErrorKind},
    os::unix::{
        ffi::OsStrExt,
        fs::{FileTypeExt, MetadataExt, PermissionsExt},
        io::{FromRawFd, RawFd},
        net::{UnixListener, UnixStream},
    },
    path::{Path, PathBuf},
    time::{Duration, Instant},
};

use super::{caller, state};

/// Parent directory mode.
pub const DIRECTORY_MODE: u32 = 0o700;
/// Socket mode.
pub const SOCKET_MODE: u32 = 0o600;
/// Conservative `sun_path` payload limit (macOS allows 103 bytes plus NUL).
pub const MAX_SOCKET_PATH_BYTES: usize = 103;

/// Resolved socket locations.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SocketPath {
    pub directory: PathBuf,
    pub path: PathBuf,
}

/// Compute the socket path without creating directories or touching the
/// filesystem. Both the GUI broker and the CLI use this so they always meet at
/// the same location.
pub fn path(data_dir: &Path) -> SocketPath {
    let preferred = state::mcp_dir(data_dir);
    let socket = preferred.join("mcp.sock");
    if socket.as_os_str().as_bytes().len() <= MAX_SOCKET_PATH_BYTES {
        return SocketPath {
            directory: preferred,
            path: socket,
        };
    }

    let name = format!("opennavo-mcp-{}", current_uid());
    if let Some(temp) = user_temp_dir() {
        let directory = temp.join(&name);
        let socket = directory.join("mcp.sock");
        if socket.as_os_str().as_bytes().len() <= MAX_SOCKET_PATH_BYTES {
            return SocketPath {
                directory,
                path: socket,
            };
        }
    }

    // Last resort. The directory is shared and predictable, so both sides
    // refuse it unless it is a real `0700` directory owned by this uid, and
    // the CLI additionally verifies the broker's peer uid.
    let fallback = PathBuf::from("/tmp").join(name);
    SocketPath {
        directory: fallback.clone(),
        path: fallback.join("mcp.sock"),
    }
}

/// Per-user temporary directory. On macOS this is the private
/// `DARWIN_USER_TEMP_DIR` (`/var/folders/.../T/`), resolved through
/// `confstr` so the GUI and a CLI launched with a different environment
/// agree. Elsewhere `$TMPDIR` is used when it is an absolute path.
fn user_temp_dir() -> Option<PathBuf> {
    #[cfg(target_os = "macos")]
    {
        let mut buffer = vec![0u8; 1024];
        let length = unsafe {
            nix::libc::confstr(
                nix::libc::_CS_DARWIN_USER_TEMP_DIR,
                buffer.as_mut_ptr().cast(),
                buffer.len(),
            )
        };
        if length > 1 && length <= buffer.len() {
            buffer.truncate(length - 1);
            let path = PathBuf::from(std::ffi::OsStr::from_bytes(&buffer));
            if path.is_absolute() {
                return Some(path);
            }
        }
        None
    }
    #[cfg(not(target_os = "macos"))]
    {
        std::env::var_os("TMPDIR")
            .map(PathBuf::from)
            .filter(|path| path.is_absolute())
    }
}

/// Prepare the directory and remove a stale socket if it is safe to do so.
pub fn prepare(data_dir: &Path) -> io::Result<SocketPath> {
    let resolved = path(data_dir);
    state::ensure_private_dir(&resolved.directory)?;
    match fs::symlink_metadata(&resolved.path) {
        Ok(metadata) => {
            if !metadata.file_type().is_socket() || metadata.file_type().is_symlink() {
                return Err(io::Error::new(
                    ErrorKind::AlreadyExists,
                    "MCP socket path is not a socket",
                ));
            }
            if metadata.uid() != current_uid() {
                return Err(io::Error::new(
                    ErrorKind::PermissionDenied,
                    "MCP socket is owned by another user",
                ));
            }
            match UnixStream::connect(&resolved.path) {
                Ok(_live) => {
                    return Err(io::Error::new(
                        ErrorKind::AddrInUse,
                        "MCP socket is already in use",
                    ));
                }
                Err(error) if error.raw_os_error() == Some(nix::libc::ECONNREFUSED) => {
                    fs::remove_file(&resolved.path)?;
                }
                Err(error) if error.kind() == ErrorKind::NotFound => {}
                Err(error) => return Err(error),
            }
        }
        Err(error) if error.kind() == ErrorKind::NotFound => {}
        Err(error) => return Err(error),
    }
    Ok(resolved)
}

/// Identity of the socket file this process bound (device and inode).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct SocketId {
    dev: u64,
    ino: u64,
}

/// Bind and harden the socket. The caller owns the listener and must remove the
/// path when it stops.
pub fn bind(resolved: &SocketPath) -> io::Result<UnixListener> {
    bind_owned(resolved).map(|(listener, _)| listener)
}

/// Bind the socket privately, then publish it at the final path.
///
/// The listener is bound at a unique temporary name inside the `0700`
/// directory, chmod-ed to `0600` and verified there, and only then hard-linked
/// to the well-known path. No client can reach the socket before its mode is
/// final, and `link` never replaces an existing entry, so a concurrent broker's
/// live socket is never overwritten. Returns the identity used by
/// [`remove_if_same`] so shutdown only ever removes this instance's socket.
pub fn bind_owned(resolved: &SocketPath) -> io::Result<(UnixListener, SocketId)> {
    // The temporary name is no longer than `mcp.sock`, so it always fits the
    // same `sun_path` budget as the final path.
    let mut attempt = 0;
    let (temporary, listener) = loop {
        let candidate = resolved
            .directory
            .join(format!(".{:07x}", unique_suffix() & 0x0fff_ffff));
        match UnixListener::bind(&candidate) {
            Ok(listener) => break (candidate, listener),
            Err(error) if error.kind() == ErrorKind::AddrInUse && attempt < 8 => {
                attempt += 1;
            }
            Err(error) => return Err(error),
        }
    };
    let published = (|| {
        fs::set_permissions(&temporary, fs::Permissions::from_mode(SOCKET_MODE))?;
        let metadata = fs::symlink_metadata(&temporary)?;
        if !metadata.file_type().is_socket()
            || metadata.file_type().is_symlink()
            || metadata.uid() != current_uid()
        {
            return Err(io::Error::new(
                ErrorKind::PermissionDenied,
                "MCP socket is not private to the current user",
            ));
        }
        if metadata.mode() & 0o777 != SOCKET_MODE {
            return Err(io::Error::new(
                ErrorKind::PermissionDenied,
                "MCP socket permissions are too broad",
            ));
        }
        match fs::hard_link(&temporary, &resolved.path) {
            Ok(()) => {}
            Err(error) if error.kind() == ErrorKind::AlreadyExists => {
                return Err(io::Error::new(
                    ErrorKind::AddrInUse,
                    "MCP socket is already in use",
                ));
            }
            Err(error) => return Err(error),
        }
        Ok(SocketId {
            dev: metadata.dev(),
            ino: metadata.ino(),
        })
    })();
    let _ = fs::remove_file(&temporary);
    match published {
        Ok(id) => Ok((listener, id)),
        Err(error) => {
            drop(listener);
            Err(error)
        }
    }
}

fn unique_suffix() -> u64 {
    use std::sync::atomic::{AtomicU64, Ordering};
    static COUNTER: AtomicU64 = AtomicU64::new(0);
    let nanos = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|duration| u64::from(duration.subsec_nanos()))
        .unwrap_or(0);
    let mixed = nanos ^ u64::from(std::process::id()).rotate_left(17);
    mixed.wrapping_add(
        COUNTER
            .fetch_add(1, Ordering::Relaxed)
            .wrapping_mul(0x9e37_79b9),
    )
}

/// Remove a stale socket owned by this user without ever touching a regular
/// file or another user's socket.
pub fn remove_stale(data_dir: &Path) -> io::Result<()> {
    let resolved = path(data_dir);
    let metadata = match fs::symlink_metadata(&resolved.path) {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == ErrorKind::NotFound => return Ok(()),
        Err(error) => return Err(error),
    };
    if !metadata.file_type().is_socket()
        || metadata.file_type().is_symlink()
        || metadata.uid() != current_uid()
    {
        return Ok(());
    }
    match UnixStream::connect(&resolved.path) {
        Ok(_live) => Ok(()),
        Err(error) if error.raw_os_error() == Some(nix::libc::ECONNREFUSED) => {
            match fs::remove_file(&resolved.path) {
                Ok(()) => Ok(()),
                Err(error) if error.kind() == ErrorKind::NotFound => Ok(()),
                Err(error) => Err(error),
            }
        }
        Err(error) if error.kind() == ErrorKind::NotFound => Ok(()),
        Err(error) => Err(error),
    }
}

/// Remove the socket on shutdown only when the path still refers to the exact
/// socket file this instance bound. A socket published later by another broker
/// (same user, different inode) is left alone.
pub fn remove_if_same(data_dir: &Path, id: SocketId) {
    let resolved = path(data_dir);
    if let Ok(metadata) = fs::symlink_metadata(&resolved.path)
        && metadata.file_type().is_socket()
        && !metadata.file_type().is_symlink()
        && metadata.uid() == current_uid()
        && metadata.dev() == id.dev
        && metadata.ino() == id.ino
    {
        let _ = fs::remove_file(&resolved.path);
    }
}

/// Peer credentials captured after `accept`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Peer {
    pub uid: u32,
    pub pid: Option<i32>,
    /// When the peer process started, read once at accept time. The kernel
    /// reuses pids, so a pid is only an identity together with this.
    pub started_at: Option<u64>,
}

impl Peer {
    /// The peer as a stamped pid, for code that must not trust a recycled one.
    pub fn stamp(&self) -> Option<caller::PidStamp> {
        self.pid
            .map(|pid| caller::PidStamp::new(pid, self.started_at))
    }
}

/// Verify that the peer belongs to the current user. On platforms where peer
/// credentials cannot be read, the connection is refused.
pub fn verify_peer(stream: &UnixStream) -> io::Result<Peer> {
    let peer = peer_credentials(stream)?;
    if peer.uid != current_uid() {
        return Err(io::Error::new(
            ErrorKind::PermissionDenied,
            "MCP peer belongs to another user",
        ));
    }
    Ok(peer)
}

/// Client side: verify that the socket's directory is private and that the
/// listening peer runs as the current user before any request is written.
pub fn verify_server(resolved: &SocketPath, stream: &UnixStream) -> io::Result<Peer> {
    state::verify_private_dir(&resolved.directory)?;
    verify_peer(stream).map_err(|error| {
        if error.kind() == ErrorKind::PermissionDenied {
            io::Error::new(
                ErrorKind::PermissionDenied,
                "MCP socket is served by another user",
            )
        } else {
            error
        }
    })
}

/// Current effective uid.
pub fn current_uid() -> u32 {
    unsafe { nix::libc::geteuid() }
}

#[cfg(target_os = "macos")]
fn peer_credentials(stream: &UnixStream) -> io::Result<Peer> {
    use std::os::fd::AsRawFd;

    let fd = stream.as_raw_fd();
    let mut uid: nix::libc::uid_t = 0;
    let mut gid: nix::libc::gid_t = 0;
    if unsafe { nix::libc::getpeereid(fd, &mut uid, &mut gid) } != 0 {
        return Err(io::Error::last_os_error());
    }

    let mut pid: nix::libc::pid_t = 0;
    let mut length = std::mem::size_of::<nix::libc::pid_t>() as nix::libc::socklen_t;
    let result = unsafe {
        nix::libc::getsockopt(
            fd,
            nix::libc::SOL_LOCAL,
            nix::libc::LOCAL_PEERPID,
            (&mut pid as *mut nix::libc::pid_t).cast(),
            &mut length,
        )
    };
    let observed = (result == 0 && pid > 0).then_some(pid);
    Ok(Peer {
        uid,
        pid: observed,
        // Read now, not later: a pid that is recycled between accept and the
        // dialog would otherwise describe a different process.
        started_at: observed.and_then(caller::started_at_now),
    })
}

#[cfg(any(target_os = "linux", target_os = "android"))]
fn peer_credentials(stream: &UnixStream) -> io::Result<Peer> {
    use std::os::fd::AsRawFd;

    let fd = stream.as_raw_fd();
    let mut credentials: nix::libc::ucred = unsafe { std::mem::zeroed() };
    let mut length = std::mem::size_of::<nix::libc::ucred>() as nix::libc::socklen_t;
    let result = unsafe {
        nix::libc::getsockopt(
            fd,
            nix::libc::SOL_SOCKET,
            nix::libc::SO_PEERCRED,
            (&mut credentials as *mut nix::libc::ucred).cast(),
            &mut length,
        )
    };
    if result != 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(Peer {
        uid: credentials.uid,
        pid: (credentials.pid > 0).then_some(credentials.pid),
        started_at: (credentials.pid > 0)
            .then(|| caller::started_at_now(credentials.pid))
            .flatten(),
    })
}

#[cfg(not(any(target_os = "macos", target_os = "linux", target_os = "android")))]
fn peer_credentials(_stream: &UnixStream) -> io::Result<Peer> {
    Err(io::Error::new(
        ErrorKind::Unsupported,
        "peer credentials are unavailable on this platform",
    ))
}

/// Connect to a Unix socket with an explicit timeout.
///
/// `UnixStream::connect` has no timeout API. A non-blocking connect plus
/// `poll` keeps the 500 ms CLI budget without spawning helper threads.
pub fn connect_timeout(path: &Path, timeout: Duration) -> io::Result<UnixStream> {
    let address = sockaddr(path)?;
    let fd = unsafe { nix::libc::socket(nix::libc::AF_UNIX, nix::libc::SOCK_STREAM, 0) };
    if fd < 0 {
        return Err(io::Error::last_os_error());
    }
    let _guard = FdGuard(fd);
    set_cloexec(fd)?;
    let flags = unsafe { nix::libc::fcntl(fd, nix::libc::F_GETFL) };
    if flags < 0 {
        return Err(io::Error::last_os_error());
    }
    if unsafe { nix::libc::fcntl(fd, nix::libc::F_SETFL, flags | nix::libc::O_NONBLOCK) } < 0 {
        return Err(io::Error::last_os_error());
    }

    let result = unsafe {
        nix::libc::connect(
            fd,
            (&address.0 as *const nix::libc::sockaddr_un).cast(),
            address.1,
        )
    };
    if result != 0 {
        let error = io::Error::last_os_error();
        let code = error.raw_os_error();
        if code != Some(nix::libc::EINPROGRESS) && code != Some(nix::libc::EAGAIN) {
            return Err(error);
        }
        wait_writable(fd, timeout)?;
        let mut socket_error: nix::libc::c_int = 0;
        let mut length = std::mem::size_of::<nix::libc::c_int>() as nix::libc::socklen_t;
        let result = unsafe {
            nix::libc::getsockopt(
                fd,
                nix::libc::SOL_SOCKET,
                nix::libc::SO_ERROR,
                (&mut socket_error as *mut nix::libc::c_int).cast(),
                &mut length,
            )
        };
        if result != 0 {
            return Err(io::Error::last_os_error());
        }
        if socket_error != 0 {
            return Err(io::Error::from_raw_os_error(socket_error));
        }
    }

    if unsafe { nix::libc::fcntl(fd, nix::libc::F_SETFL, flags) } < 0 {
        return Err(io::Error::last_os_error());
    }
    let stream = unsafe { UnixStream::from_raw_fd(fd) };
    std::mem::forget(_guard);
    Ok(stream)
}

fn wait_writable(fd: RawFd, timeout: Duration) -> io::Result<()> {
    let deadline = Instant::now() + timeout;
    loop {
        let remaining = deadline.saturating_duration_since(Instant::now());
        let millis = remaining.as_millis().min(i32::MAX as u128) as i32;
        let mut descriptor = nix::libc::pollfd {
            fd,
            events: nix::libc::POLLOUT,
            revents: 0,
        };
        let result = unsafe { nix::libc::poll(&mut descriptor, 1, millis) };
        if result > 0 {
            return Ok(());
        }
        if result == 0 {
            return Err(io::Error::new(ErrorKind::TimedOut, "MCP connect timed out"));
        }
        let error = io::Error::last_os_error();
        if error.kind() != ErrorKind::Interrupted {
            return Err(error);
        }
        if Instant::now() >= deadline {
            return Err(io::Error::new(ErrorKind::TimedOut, "MCP connect timed out"));
        }
    }
}

fn set_cloexec(fd: RawFd) -> io::Result<()> {
    if unsafe { nix::libc::fcntl(fd, nix::libc::F_SETFD, nix::libc::FD_CLOEXEC) } < 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(())
}

fn sockaddr(path: &Path) -> io::Result<(nix::libc::sockaddr_un, nix::libc::socklen_t)> {
    let bytes = path.as_os_str().as_bytes();
    let base = std::mem::offset_of!(nix::libc::sockaddr_un, sun_path);
    if bytes.len() + 1 > std::mem::size_of::<nix::libc::sockaddr_un>() - base {
        return Err(io::Error::new(
            ErrorKind::InvalidInput,
            "MCP socket path is too long",
        ));
    }
    let mut address: nix::libc::sockaddr_un = unsafe { std::mem::zeroed() };
    address.sun_family = nix::libc::AF_UNIX as nix::libc::sa_family_t;
    unsafe {
        std::ptr::copy_nonoverlapping(
            bytes.as_ptr(),
            address.sun_path.as_mut_ptr().cast::<u8>(),
            bytes.len(),
        );
    }
    Ok((address, (base + bytes.len() + 1) as nix::libc::socklen_t))
}

struct FdGuard(RawFd);

impl Drop for FdGuard {
    fn drop(&mut self) {
        unsafe {
            nix::libc::close(self.0);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;

    fn tempdir() -> tempfile::TempDir {
        tempfile::tempdir().expect("tempdir")
    }

    #[test]
    fn socket_is_created_with_0700_dir_and_0600_socket() {
        let dir = tempdir();
        let resolved = prepare(dir.path()).expect("prepare");
        let _listener = bind(&resolved).expect("bind");
        let directory = fs::metadata(&resolved.directory).expect("dir");
        let socket = fs::symlink_metadata(&resolved.path).expect("socket");
        assert_eq!(directory.permissions().mode() & 0o777, DIRECTORY_MODE);
        assert_eq!(socket.permissions().mode() & 0o777, SOCKET_MODE);
        assert!(socket.file_type().is_socket());
    }

    #[test]
    fn regular_file_at_socket_path_is_not_deleted() {
        let dir = tempdir();
        let resolved = path(dir.path());
        state::ensure_private_dir(&resolved.directory).expect("dir");
        fs::write(&resolved.path, b"do not delete").expect("regular file");
        let error = prepare(dir.path()).expect_err("must refuse");
        assert_eq!(error.kind(), ErrorKind::AlreadyExists);
        assert_eq!(
            fs::read(&resolved.path).expect("still there"),
            b"do not delete"
        );
    }

    #[test]
    fn stale_socket_is_replaced_only_when_owned_by_current_uid() {
        let dir = tempdir();
        let resolved = prepare(dir.path()).expect("prepare");
        let listener = bind(&resolved).expect("bind");
        drop(listener);
        // The path is left behind, like a crashed app.
        assert!(resolved.path.exists());
        let resolved = prepare(dir.path()).expect("stale socket replaced");
        let _listener = bind(&resolved).expect("rebind");
    }

    #[test]
    fn live_socket_is_not_replaced() {
        let dir = tempdir();
        let resolved = prepare(dir.path()).expect("prepare");
        let _listener = bind(&resolved).expect("bind");
        let error = prepare(dir.path()).expect_err("live socket");
        assert_eq!(error.kind(), ErrorKind::AddrInUse);
    }

    #[test]
    fn peer_uid_matches_current_user() {
        let dir = tempdir();
        let resolved = prepare(dir.path()).expect("prepare");
        let listener = bind(&resolved).expect("bind");
        let client = connect_timeout(&resolved.path, Duration::from_millis(500)).expect("connect");
        let (server, _) = listener.accept().expect("accept");
        let peer = verify_peer(&server).expect("peer");
        assert_eq!(peer.uid, current_uid());
        drop(server);
        drop(client);
    }

    #[test]
    fn long_data_dir_uses_the_user_temp_dir_first() {
        let dir = tempdir();
        let long = dir.path().join("x".repeat(120));
        let resolved = path(&long);
        let name = format!("opennavo-mcp-{}", current_uid());
        assert!(resolved.directory.ends_with(&name));
        if let Some(temp) = user_temp_dir() {
            let candidate = temp.join(&name).join("mcp.sock");
            if candidate.as_os_str().as_bytes().len() <= MAX_SOCKET_PATH_BYTES {
                assert_eq!(resolved.path, candidate);
            }
        }
    }

    #[test]
    fn client_refuses_a_socket_directory_with_broad_permissions() {
        let dir = tempdir();
        let resolved = prepare(dir.path()).expect("prepare");
        let listener = bind(&resolved).expect("bind");
        let client = connect_timeout(&resolved.path, Duration::from_millis(500)).expect("connect");
        verify_server(&resolved, &client).expect("private dir accepted");
        fs::set_permissions(&resolved.directory, fs::Permissions::from_mode(0o755)).expect("chmod");
        let error = verify_server(&resolved, &client).expect_err("broad dir");
        assert_eq!(error.kind(), ErrorKind::PermissionDenied);
        drop(listener);
    }

    #[test]
    fn connect_to_missing_socket_is_not_found() {
        let dir = tempdir();
        let resolved = path(dir.path());
        let error =
            connect_timeout(&resolved.path, Duration::from_millis(500)).expect_err("missing");
        assert_eq!(error.kind(), ErrorKind::NotFound);
    }
}
