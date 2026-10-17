//! Exercises only a freshly created temporary database, never a running app's database.
//! Compares cloned application handles with repeated system SQLite read-only processes.
use opennavo_desktop_lib::{AppError, db::Database};
use std::{
    ffi::{CStr, c_char},
    path::Path,
    process::Command,
    sync::atomic::{AtomicBool, Ordering},
};

fn vfs(connection: &rusqlite::Connection) -> rusqlite::Result<String> {
    let mut name: *mut c_char = std::ptr::null_mut();
    // SAFETY: the connection is locked by Database::with; VFSNAME writes an allocated
    // string to this valid pointer. SQLite owns its allocator, so use sqlite3_free.
    unsafe {
        let code = rusqlite::ffi::sqlite3_file_control(
            connection.handle(),
            c"main".as_ptr(),
            rusqlite::ffi::SQLITE_FCNTL_VFSNAME,
            (&mut name as *mut *mut c_char).cast(),
        );
        if code != rusqlite::ffi::SQLITE_OK {
            return Err(rusqlite::Error::SqliteFailure(
                rusqlite::ffi::Error::new(code),
                None,
            ));
        }
        if name.is_null() {
            return Ok("unknown".into());
        }
        let value = CStr::from_ptr(name).to_string_lossy().into_owned();
        rusqlite::ffi::sqlite3_free(name.cast());
        Ok(value)
    }
}

#[cfg(unix)]
fn shm_identity(path: &Path) -> std::io::Result<(u64, u64, u64)> {
    use std::os::unix::fs::MetadataExt;
    // stat only: opening and closing SQLite files directly could release our locks.
    let metadata = std::fs::metadata(path)?;
    Ok((metadata.dev(), metadata.ino(), metadata.len()))
}

fn system_read(path: &Path, sql: &str) -> Result<String, AppError> {
    let output = Command::new("/usr/bin/sqlite3")
        .args(["-batch", "-readonly", "-cmd", ".timeout 5000"])
        // Force the CLI to open the database before .vfsname (which otherwise
        // silently does nothing). This query produces no output.
        .args(["-cmd", "SELECT name FROM sqlite_schema LIMIT 0;"])
        .arg(path)
        .arg(sql)
        .output()?;
    if !output.status.success() {
        return Err(AppError::new("E_UNKNOWN", "system_sqlite")
            .with_detail(String::from_utf8_lossy(&output.stderr)));
    }
    Ok(String::from_utf8_lossy(&output.stdout).trim().into())
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    if std::env::args().skip(1).collect::<Vec<_>>() != ["--temp-only"] {
        return Err(
            "usage: sqlite_wal_probe --temp-only (no existing database paths accepted)".into(),
        );
    }
    let directory = tempfile::tempdir()?;
    let path = directory.path().join("probe.db");
    let shm = directory.path().join("probe.db-shm");
    let db = Database::open(&path)?;
    db.set_meta("probe", "initial")?;
    let library = db.with(|connection| {
        Ok(serde_json::json!({
            "version": rusqlite::version(), "vfs": vfs(connection)?,
            "journal": connection.pragma_query_value(None, "journal_mode", |row| row.get::<_, String>(0))?,
            "mmapSize": connection.pragma_query_value(None, "mmap_size", |row| row.get::<_, i64>(0))?,
        }))
    })?;
    let system = system_read(&path, "SELECT sqlite_version();")?;
    let system_vfs = system_read(&path, ".vfsname")?;
    let identity = shm_identity(&shm)?;
    println!(
        "{}",
        serde_json::json!({"bundled": library, "system": system, "systemVfs": system_vfs, "shmBefore": identity})
    );
    let finished = AtomicBool::new(false);
    // Stop and join the writer even if a reader fails. TempDir must never remove
    // sidecars while a worker still holds its connection.
    struct Stop<'a>(&'a AtomicBool);
    impl Drop for Stop<'_> {
        fn drop(&mut self) {
            self.0.store(true, Ordering::Release);
        }
    }
    // Declared outside the nested scope so the flag outlives every worker.
    let writes = std::thread::scope(|workers| -> Result<u64, AppError> {
        let writer_db = db.clone();
        let stop = &finished;
        let writer = workers.spawn(move || -> Result<u64, AppError> {
            let mut count = 0;
            while !stop.load(Ordering::Acquire) {
                writer_db.set_meta("probe", &format!("{count}:{}", "x".repeat(4096)))?;
                assert!(writer_db.meta("probe")?.is_some());
                count += 1;
                std::thread::yield_now();
            }
            Ok(count)
        });
        let stop_guard = Stop(&finished);
        let result = (|| -> Result<(), AppError> {
            // Separate bundled SQLite handles are also coordinated by SQLite's inode list.
            for _ in 0..250 {
                let reader = rusqlite::Connection::open_with_flags(
                    &path,
                    rusqlite::OpenFlags::SQLITE_OPEN_READ_ONLY,
                )?;
                reader.busy_timeout(std::time::Duration::from_secs(5))?;
                let value: String =
                    reader.query_row("SELECT value FROM meta WHERE key='probe'", [], |row| {
                        row.get(0)
                    })?;
                assert!(!value.is_empty());
            }
            let readers: Vec<_> = (0..4)
                .map(|_| {
                    let path = path.clone();
                    let shm = shm.clone();
                    let db = db.clone();
                    workers.spawn(move || -> Result<(), AppError> {
                        for _ in 0..250 {
                            assert_eq!(system_read(&path, "SELECT count(*) FROM tasks;")?, "0");
                            assert!(db.meta("probe")?.is_some());
                            let current = shm_identity(&shm)?;
                            assert_eq!((current.0, current.1), (identity.0, identity.1));
                            assert!(current.2 >= identity.2, "shared-memory file shrank");
                        }
                        Ok(())
                    })
                })
                .collect();
            let results: Vec<_> = readers
                .into_iter()
                .map(|reader| reader.join().expect("reader panic"))
                .collect();
            for result in results {
                result?;
            }
            Ok(())
        })();
        drop(stop_guard);
        let writes = writer.join().expect("writer panic")?;
        result?;
        Ok(writes)
    })?;
    let integrity = db.with(|connection| {
        Ok(connection.query_row("PRAGMA integrity_check", [], |row| row.get::<_, String>(0))?)
    })?;
    assert_eq!(integrity, "ok");
    println!(
        "{}",
        serde_json::json!({"bundledReaders": 250, "systemReaders": 1000, "writes": writes, "shmAfter": shm_identity(&shm)?, "integrity": integrity})
    );
    drop(db);
    // Drop every connection before TempDir deletes the database and sidecars.
    drop(directory);
    Ok(())
}
