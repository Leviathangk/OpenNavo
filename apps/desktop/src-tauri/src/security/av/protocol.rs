//! One bounded request per Unix connection; clamd never receives a pathname.
use crate::{AppError, security::files};
use std::{
    fs::File,
    io::{IoSlice, Read, Seek, SeekFrom, Write},
    os::fd::AsRawFd,
    os::unix::net::UnixStream,
    path::Path,
    sync::atomic::AtomicBool,
    time::{Duration, Instant},
};

fn error(e: impl ToString) -> AppError {
    AppError::new("E_AV_ENGINE", "clamd_protocol").with_detail(e.to_string())
}
fn transport(e: std::io::Error) -> AppError {
    if matches!(
        e.kind(),
        std::io::ErrorKind::UnexpectedEof
            | std::io::ErrorKind::BrokenPipe
            | std::io::ErrorKind::ConnectionReset
            | std::io::ErrorKind::ConnectionAborted
    ) {
        AppError::new("E_AV_ENGINE", "clamd_disconnected").with_detail(e.to_string())
    } else {
        error(e)
    }
}
pub fn connect(socket: &Path) -> Result<UnixStream, AppError> {
    let stream = UnixStream::connect(socket).map_err(error)?;
    stream
        .set_read_timeout(Some(Duration::from_millis(250)))
        .map_err(error)?;
    stream
        .set_write_timeout(Some(Duration::from_secs(5)))
        .map_err(error)?;
    Ok(stream)
}
pub fn reply(stream: &mut UnixStream, cancel: &AtomicBool) -> Result<String, AppError> {
    let mut bytes = Vec::new();
    let start = Instant::now();
    loop {
        files::canceled(cancel)?;
        if start.elapsed() > Duration::from_secs(300) {
            return Err(
                AppError::new("E_AV_ENGINE", "av_scan_too_large").with_detail("scan_time_budget")
            );
        }
        let mut byte = [0];
        match stream.read(&mut byte) {
            Ok(0) => {
                return Err(transport(std::io::Error::from(
                    std::io::ErrorKind::UnexpectedEof,
                )));
            }
            Ok(_) if byte[0] == 0 => return String::from_utf8(bytes).map_err(error),
            Ok(_) => bytes.push(byte[0]),
            Err(e)
                if matches!(
                    e.kind(),
                    std::io::ErrorKind::WouldBlock
                        | std::io::ErrorKind::TimedOut
                        | std::io::ErrorKind::Interrupted
                ) =>
            {
                continue;
            }
            Err(e) => return Err(transport(e)),
        }
        if bytes.len() > 16384 {
            return Err(error("response limit"));
        }
    }
}
pub fn ping(socket: &Path) -> bool {
    let Ok(mut stream) = connect(socket) else {
        return false;
    };
    if stream.write_all(b"zPING\0").is_err() {
        return false;
    }
    // A ping must never hold up cancellation/startup for the scan timeout.
    let mut buf = [0; 5];
    stream.read_exact(&mut buf).is_ok() && &buf == b"PONG\0"
}
pub fn shutdown(socket: &Path) {
    if let Ok(mut s) = connect(socket) {
        let _ = s.write_all(b"zSHUTDOWN\0");
    }
}
fn verdict(response: &str) -> Result<Option<String>, AppError> {
    if response.ends_with(": OK") {
        return Ok(None);
    }
    if let Some(text) = response.strip_suffix(" FOUND") {
        let (_, name) = text
            .split_once(": ")
            .ok_or_else(|| error("invalid finding"))?;
        // These are incomplete scans, not malware detections. Never quarantine
        // an encrypted archive or a file which merely exceeds an engine limit.
        if name.starts_with("Heuristics.Limits.Exceeded") {
            return Err(AppError::new("E_AV_SCAN_FAILED", "av_scan_too_large"));
        }
        if name.starts_with("Heuristics.Encrypted.") {
            return Err(AppError::new("E_AV_SCAN_FAILED", "av_scan_unreadable"));
        }
        return Ok(Some(name.to_owned()));
    }
    if response.ends_with(" ERROR") {
        // A completed file-level failure is not a broken daemon connection.
        return Err(AppError::new("E_AV_SCAN_FAILED", "av_scan_unreadable").with_detail(response));
    }
    Err(error(response))
}

fn fildes(socket: &Path, file: &File, cancel: &AtomicBool) -> Result<Option<String>, AppError> {
    let mut stream = connect(socket)?;
    // One request per connection needs no IDSESSION. A single FILDES enters
    // clamd's WAITREPLY state, instead of leaving a session awaiting commands
    // (and subject to ReadTimeout) while a large object is being scanned.
    stream.write_all(b"zFILDES\0").map_err(transport)?;
    // libc avoids an additional networking dependency. SCM_RIGHTS holds a duplicate
    // of our verified descriptor; neither path traversal nor a second open in clamd.
    let byte = [0u8];
    let iov = IoSlice::new(&byte);
    let mut io = nix::libc::iovec {
        iov_base: iov.as_ptr() as *mut _,
        iov_len: 1,
    };
    let mut control = [0_usize; 8];
    let mut msg: nix::libc::msghdr = unsafe { std::mem::zeroed() };
    msg.msg_iov = &mut io;
    msg.msg_iovlen = 1;
    msg.msg_control = control.as_mut_ptr().cast();
    msg.msg_controllen = unsafe { nix::libc::CMSG_SPACE(std::mem::size_of::<i32>() as _) } as _;
    unsafe {
        let header = nix::libc::CMSG_FIRSTHDR(&msg);
        (*header).cmsg_level = nix::libc::SOL_SOCKET;
        (*header).cmsg_type = nix::libc::SCM_RIGHTS;
        (*header).cmsg_len = nix::libc::CMSG_LEN(std::mem::size_of::<i32>() as _) as _;
        std::ptr::write_unaligned(nix::libc::CMSG_DATA(header).cast::<i32>(), file.as_raw_fd());
        if nix::libc::sendmsg(stream.as_raw_fd(), &msg, 0) != 1 {
            return Err(transport(std::io::Error::last_os_error()));
        }
    }
    let response = reply(&mut stream, cancel)?;
    verdict(&response)
}
pub fn scan(
    socket: &Path,
    file: &mut File,
    cancel: &AtomicBool,
) -> Result<Option<String>, AppError> {
    files::canceled(cancel)?;
    if files::dataless(&file.metadata()?) {
        return Err(error("dataless descriptor"));
    }
    file.seek(SeekFrom::Start(0))?;
    let mut result = fildes(socket, file, cancel);
    if result
        .as_ref()
        .is_err_and(|e| e.message == "clamd_disconnected")
    {
        files::canceled(cancel)?;
        log::warn!(
            "ClamAV connection closed before its verdict; retrying the same descriptor once"
        );
        // SCM_RIGHTS shares the open-file offset. Rewind the same verified fd;
        // never reopen its pathname, and never treat a missing reply as clean.
        file.seek(SeekFrom::Start(0))?;
        result = fildes(socket, file, cancel);
    }
    match result {
        Ok(v) => Ok(v),
        Err(e)
            if e.code != "E_SCAN_CANCELED"
                && e.detail.as_deref().is_some_and(|s| {
                    s.contains("UNKNOWN COMMAND") || s.contains("COMMAND UNAVAILABLE")
                }) =>
        {
            file.seek(SeekFrom::Start(0))?;
            let mut stream = connect(socket)?;
            stream.write_all(b"zINSTREAM\0").map_err(error)?;
            let mut buffer = [0u8; 65536];
            let mut bytes = 0_u64;
            loop {
                files::canceled(cancel)?;
                let count = file.read(&mut buffer)?;
                bytes += count as u64;
                if bytes > files::MAX_INSTALLER_FILE {
                    return Err(error("file grew"));
                }
                stream
                    .write_all(&(count as u32).to_be_bytes())
                    .map_err(error)?;
                if count == 0 {
                    break;
                }
                stream.write_all(&buffer[..count]).map_err(error)?;
            }
            verdict(&reply(&mut stream, cancel)?)
        }
        Err(e) => Err(e),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn incomplete_containers_are_neither_clean_nor_malware() {
        assert_eq!(verdict("fd[1]: OK").unwrap(), None);
        assert_eq!(
            verdict("fd[1]: Eicar-Test-Signature FOUND")
                .unwrap()
                .as_deref(),
            Some("Eicar-Test-Signature")
        );
        for limit in [
            "MaxFileSize",
            "MaxScanSize",
            "MaxRecursion",
            "MaxFiles",
            "MaxScanTime",
        ] {
            assert_eq!(
                verdict(&format!("fd[1]: Heuristics.Limits.Exceeded.{limit} FOUND"))
                    .unwrap_err()
                    .message,
                "av_scan_too_large"
            );
        }
        assert_eq!(
            verdict("fd[1]: Heuristics.Encrypted.Zip FOUND")
                .unwrap_err()
                .message,
            "av_scan_unreadable"
        );
    }
}
