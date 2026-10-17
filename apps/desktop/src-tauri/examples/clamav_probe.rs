//! Run only the explicitly supplied standalone clamd; no freshclam/CDN access.
//! Only temporary config/socket/EICAR files are written. The corpus is read only.
use serde_json::json;
use std::{
    fs::{File, OpenOptions},
    io::{Read, Write},
    os::unix::{fs::OpenOptionsExt, net::UnixStream},
    path::{Path, PathBuf},
    process::{Child, Command, Stdio},
    time::{Duration, Instant},
};

struct Daemon(Child);
impl Drop for Daemon {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}

fn reply(stream: &mut UnixStream) -> std::io::Result<String> {
    let mut result = Vec::new();
    loop {
        let mut byte = [0];
        stream.read_exact(&mut byte)?;
        if byte[0] == 0 {
            return Ok(String::from_utf8_lossy(&result).into_owned());
        }
        result.push(byte[0]);
        if result.len() > 16_384 {
            return Err(std::io::Error::other("clamd response too large"));
        }
    }
}

fn connect(socket: &Path) -> std::io::Result<UnixStream> {
    let stream = UnixStream::connect(socket)?;
    stream.set_read_timeout(Some(Duration::from_secs(120)))?;
    stream.set_write_timeout(Some(Duration::from_secs(120)))?;
    Ok(stream)
}

fn scan(socket: &Path, file: &mut File) -> std::io::Result<String> {
    let mut stream = connect(socket)?;
    // INSTREAM is the contract's fallback. Each connection has exactly one
    // command and response; no forbidden commands are sent inside IDSESSION.
    stream.write_all(b"zINSTREAM\0")?;
    let mut buffer = [0u8; 64 * 1024];
    loop {
        let count = file.read(&mut buffer)?;
        stream.write_all(&(count as u32).to_be_bytes())?;
        if count == 0 {
            break;
        }
        stream.write_all(&buffer[..count])?;
    }
    reply(&mut stream)
}

fn eligible(path: &Path) -> Option<File> {
    let meta = std::fs::symlink_metadata(path).ok()?;
    #[cfg(target_os = "macos")]
    {
        use std::os::macos::fs::MetadataExt;
        if meta.st_flags() & 0x4000_0000 != 0 {
            return None;
        }
    }
    if !meta.is_file() || meta.len() > 100 * 1024 * 1024 {
        return None;
    }
    // O_EVTONLY cannot be read. O_NOFOLLOW + O_NONBLOCK avoids symlink/FIFO
    // substitutions. Recheck dataless on the opened descriptor before reading.
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(nix::libc::O_NOFOLLOW | nix::libc::O_NONBLOCK)
        .open(path)
        .ok()?;
    let meta = file.metadata().ok()?;
    #[cfg(target_os = "macos")]
    {
        use std::os::macos::fs::MetadataExt;
        if meta.st_flags() & 0x4000_0000 != 0 {
            return None;
        }
    }
    (meta.is_file() && meta.len() <= 100 * 1024 * 1024).then_some(file)
}

fn collect(path: &Path, output: &mut Vec<PathBuf>, bytes: &mut u64) {
    if *bytes >= 64 * 1024 * 1024 || output.len() >= 1000 {
        return;
    }
    let Ok(meta) = std::fs::symlink_metadata(path) else {
        return;
    };
    if meta.file_type().is_symlink() {
        return;
    }
    #[cfg(target_os = "macos")]
    {
        use std::os::macos::fs::MetadataExt;
        if meta.st_flags() & 0x4000_0000 != 0 {
            return;
        }
    }
    if meta.is_file() {
        if meta.len() <= 100 * 1024 * 1024 {
            *bytes += meta.len();
            output.push(path.to_owned());
        }
    } else if meta.is_dir()
        && let Ok(entries) = std::fs::read_dir(path)
    {
        let mut paths: Vec<_> = entries.flatten().map(|entry| entry.path()).collect();
        paths.sort();
        for path in paths {
            collect(&path, output, bytes);
        }
    }
}

fn config_path(path: &Path) -> Result<String, Box<dyn std::error::Error>> {
    let path = path.canonicalize()?.to_string_lossy().into_owned();
    if path.contains(['\n', '\r', '"']) {
        return Err("unsupported config path".into());
    }
    Ok(format!("\"{path}\""))
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<_> = std::env::args().skip(1).collect();
    if args.len() != 5 || args[0] != "--real-read-only" {
        return Err(
            "Usage: clamav_probe --real-read-only <clamd> <cached-db-dir> <cert-dir> <corpus>"
                .into(),
        );
    }
    let temp = tempfile::Builder::new()
        .prefix("on-av-")
        .tempdir_in("/tmp")?;
    let socket = temp.path().join("clamd.sock");
    let config = temp.path().join("clamd.conf");
    std::fs::write(
        &config,
        format!(
            "Foreground yes\nLocalSocket {}\nLocalSocketMode 600\nDatabaseDirectory {}\nCVDCertsDirectory {}\nConcurrentDatabaseReload no\nSelfCheck 0\nMaxThreads 1\nMaxFileSize 100M\nMaxScanSize 100M\nStreamMaxLength 100M\nLogTime yes\n",
            socket.display(),
            config_path(Path::new(&args[2]))?,
            config_path(Path::new(&args[3]))?
        ),
    )?;
    let log = File::create(temp.path().join("clamd.log"))?;
    let start = Instant::now();
    let mut daemon = Daemon(
        Command::new(&args[1])
            .arg(format!("--config-file={}", config.display()))
            .env("CVD_CERTS_DIR", Path::new(&args[3]).canonicalize()?)
            .stdin(Stdio::null())
            .stdout(log.try_clone()?)
            .stderr(log)
            .spawn()?,
    );
    loop {
        if let Ok(mut stream) = connect(&socket)
            && stream.write_all(b"zPING\0").is_ok()
            && reply(&mut stream).is_ok_and(|value| value == "PONG")
        {
            break;
        }
        if daemon.0.try_wait()?.is_some() || start.elapsed() > Duration::from_secs(90) {
            return Err(std::fs::read_to_string(temp.path().join("clamd.log"))?.into());
        }
        std::thread::sleep(Duration::from_millis(100));
    }
    let load_secs = start.elapsed().as_secs_f64();
    let memory = Command::new("/bin/ps")
        .args(["-o", "rss=", "-p", &daemon.0.id().to_string()])
        .output()?;
    let resident_bytes = String::from_utf8_lossy(&memory.stdout)
        .trim()
        .parse::<u64>()?
        * 1024;
    use std::os::unix::fs::PermissionsExt;
    let mode = std::fs::metadata(&socket)?.permissions().mode() & 0o777;
    if mode != 0o600 {
        return Err("socket is not 0600".into());
    }
    let eicar = temp.path().join("eicar.txt");
    std::fs::write(
        &eicar,
        concat!(
            "X5O!P%@AP[4\\PZX54(P^)7CC)7}$",
            "EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*"
        ),
    )?;
    let eicar_reply = scan(&socket, &mut File::open(&eicar)?)?;
    if !eicar_reply.ends_with(" FOUND") {
        return Err("EICAR was not detected".into());
    }
    let mut paths = Vec::new();
    collect(Path::new(&args[4]), &mut paths, &mut 0);
    let start = Instant::now();
    let (mut files, mut bytes, mut found) = (0u64, 0u64, 0u64);
    for path in paths {
        if let Some(mut file) = eligible(&path) {
            let result = scan(&socket, &mut file)?;
            if !result.ends_with(" OK") && !result.ends_with(" FOUND") {
                return Err(result.into());
            }
            files += 1;
            bytes += file.metadata()?.len();
            found += u64::from(result.ends_with(" FOUND"));
        }
    }
    let elapsed = start.elapsed().as_secs_f64();
    println!(
        "{}",
        json!({"readOnly": true, "loadSeconds": load_secs, "residentBytes": resident_bytes,
        "socketMode": format!("{mode:o}"), "eicarDetected": true, "files": files, "bytes": bytes,
        "scanSeconds": elapsed, "mibPerSecond": bytes as f64 / 1_048_576.0 / elapsed, "findings": found})
    );
    // A separate connection, never SHUTDOWN inside IDSESSION.
    connect(&socket)?.write_all(b"zSHUTDOWN\0")?;
    Ok(())
}
