use opennavo_desktop_lib::db::Database;
use rusqlite::Connection;
use std::{sync::Arc, time::Duration};

#[test]
fn cloned_handles_share_one_connection_and_survive_concurrent_reads_and_writes() {
    let directory = tempfile::tempdir().unwrap();
    let db = Database::open(&directory.path().join("app.db")).unwrap();
    // Connection metadata only; never open SQLite files using ordinary file I/O.
    let original = db.with(|c| Ok(c as *const Connection as usize)).unwrap();
    let barrier = Arc::new(std::sync::Barrier::new(4));
    std::thread::scope(|scope| {
        for worker in 0..4 {
            let db = db.clone();
            let barrier = barrier.clone();
            scope.spawn(move || {
                assert_eq!(
                    db.with(|c| Ok(c as *const Connection as usize)).unwrap(),
                    original
                );
                barrier.wait();
                let key = format!("worker-{worker}");
                for n in 0..100 {
                    let value = n.to_string();
                    db.set_meta(&key, &value).unwrap();
                    assert_eq!(db.meta(&key).unwrap().as_deref(), Some(value.as_str()));
                    assert!(db.tasks().unwrap().is_empty());
                }
            });
        }
    });
    let integrity: String = db
        .with(|c| Ok(c.query_row("PRAGMA integrity_check", [], |r| r.get(0))?))
        .unwrap();
    assert_eq!(integrity, "ok");
}

#[test]
fn read_lock_contention_returns_an_error_and_the_same_handle_recovers() {
    let directory = tempfile::tempdir().unwrap();
    let path = directory.path().join("app.db");
    let db = Database::open(&path).unwrap();
    db.set_meta("probe", "still here").unwrap();
    db.with(|c| {
        let timeout: u32 = c.pragma_query_value(None, "busy_timeout", |r| r.get(0))?;
        assert_eq!(timeout, 5000);
        // WAL readers usually do not block on writers. Rollback mode lets this
        // test deterministically force a real SQLITE_BUSY on the read path.
        c.pragma_update(None, "journal_mode", "DELETE")?;
        c.busy_timeout(Duration::ZERO)?;
        Ok(())
    })
    .unwrap();
    let blocker = Connection::open(&path).unwrap();
    blocker.execute_batch("BEGIN EXCLUSIVE").unwrap();
    let error = db.meta("probe").unwrap_err();
    assert_eq!(
        (error.code.as_str(), error.message.as_str()),
        ("E_UNKNOWN", "database")
    );
    assert!(error.detail.unwrap().contains("locked"));
    assert!(db.tasks().is_err());
    blocker.execute_batch("ROLLBACK").unwrap();
    assert_eq!(db.meta("probe").unwrap().as_deref(), Some("still here"));
    assert!(db.tasks().unwrap().is_empty());
    // No connection recreation, mutex poisoning, or recovery file deletion.
    db.set_meta("probe", "updated").unwrap();
    assert_eq!(db.meta("probe").unwrap().as_deref(), Some("updated"));
}
