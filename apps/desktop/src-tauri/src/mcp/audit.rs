//! Local MCP write audit log.
//!
//! Every write tool call that reaches a user decision is recorded in
//! `mcp/audit.jsonl` (mode `0600`, inside the private MCP directory). Only the
//! tool name, the object (`kind/token` or an app path), what is known about
//! the caller, the user's decision and the outcome are stored: never the full
//! arguments or tool output. Entries older than [`RETENTION_DAYS`] are dropped
//! on the next write and are never listed.
//!
//! The log is the precondition for a write: the broker checks it is writable
//! before showing a dialog and records the approval before executing. If
//! either fails, the write is refused.

use std::{
    fs,
    io::{self, BufRead, BufReader, Write},
    path::{Path, PathBuf},
};

use serde::{Deserialize, Serialize};

use super::state;

/// Days an entry is kept.
pub const RETENTION_DAYS: i64 = 90;
const RETENTION_MS: i64 = RETENTION_DAYS * 24 * 60 * 60 * 1000;
/// Hard cap on the log; the oldest lines go first beyond it.
const MAX_BYTES: u64 = 4 * 1024 * 1024;
/// Largest line read back; longer lines are ignored.
const MAX_LINE: usize = 16 * 1024;

/// The process that asked; only the code signature of `path` is verified.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct AuditCaller {
    /// Executable of the `mcp` subprocess's direct parent.
    pub path: Option<String>,
    /// Signing authority (leaf certificate summary) when the parent is signed.
    pub signer: Option<String>,
    pub team_id: Option<String>,
    /// Whether the parent carries a valid signature.
    pub signed: bool,
    /// Nearest `.app` ancestor of the parent, unverified.
    pub may_come_from: Option<String>,
    /// Self-reported `clientInfo.name`.
    pub claimed_name: Option<String>,
    /// Parent pid, recorded for the audit only (never shown in the dialog).
    pub pid: Option<i32>,
}

/// The user's answer; `invalidated`: withdrawn because the connection or a switch went away; `rejected`: refused before asking.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub enum AuditDecision {
    Allowed,
    Denied,
    TimedOut,
    Invalidated,
    Rejected,
}

/// One write request as shown in Settings.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct AuditEntry {
    pub id: String,
    /// Milliseconds since the Unix epoch.
    pub at: i64,
    pub tool: String,
    pub object: String,
    pub caller: AuditCaller,
    pub decision: AuditDecision,
    /// `queued:<task id>` (`queued` if no task id came back) or an error code. `None` until known.
    pub result: Option<String>,
}

#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase", tag = "type")]
enum Line {
    Entry(AuditEntry),
    Result {
        id: String,
        #[serde(default)]
        at: i64,
        result: String,
    },
}

impl Line {
    fn at(&self) -> i64 {
        match self {
            Self::Entry(entry) => entry.at,
            Self::Result { at, .. } => *at,
        }
    }

    /// A refusal that never reached a dialog: cheap to regenerate, so rotation drops these before
    /// it drops the decisions a user actually made.
    fn rejected(&self) -> bool {
        matches!(
            self,
            Self::Entry(AuditEntry {
                decision: AuditDecision::Rejected,
                ..
            })
        )
    }
}

/// Append-only audit log under the MCP directory.
#[derive(Debug, Clone)]
pub struct Audit {
    dir: PathBuf,
}

impl Audit {
    pub fn new(data_dir: &Path) -> Self {
        Self {
            dir: state::mcp_dir(data_dir),
        }
    }

    pub fn path(&self) -> PathBuf {
        self.dir.join("audit.jsonl")
    }

    /// Fail unless a record could be written right now.
    pub fn check(&self) -> io::Result<()> {
        self.open_append().map(|_| ())
    }

    /// Record a decision. The caller must not run a write if this fails.
    pub fn record(&self, entry: &AuditEntry, now: i64) -> io::Result<()> {
        self.prune(now)?;
        self.append(&Line::Entry(entry.clone()))
    }

    /// Record the outcome of an earlier entry.
    pub fn record_result(&self, id: &str, result: &str, now: i64) -> io::Result<()> {
        self.append(&Line::Result {
            id: id.to_owned(),
            at: now,
            result: result.to_owned(),
        })
    }

    /// Entries within the retention window, newest first.
    pub fn list(&self, now: i64) -> io::Result<Vec<AuditEntry>> {
        let mut entries: Vec<AuditEntry> = Vec::new();
        for line in self.read_lines()? {
            match line {
                Line::Entry(entry) => entries.push(entry),
                Line::Result { id, result, .. } => {
                    if let Some(entry) = entries.iter_mut().rev().find(|entry| entry.id == id) {
                        entry.result = Some(result);
                    }
                }
            }
        }
        entries.retain(|entry| now - entry.at < RETENTION_MS);
        entries.reverse();
        Ok(entries)
    }

    /// Remove every entry.
    pub fn clear(&self) -> io::Result<()> {
        match fs::remove_file(self.path()) {
            Err(error) if error.kind() != io::ErrorKind::NotFound => Err(error),
            _ => Ok(()),
        }
    }

    fn append(&self, line: &Line) -> io::Result<()> {
        let mut body = serde_json::to_vec(line).map_err(io::Error::other)?;
        body.push(b'\n');
        let mut file = self.open_append()?;
        file.write_all(&body)?;
        file.sync_data()
    }

    fn open_append(&self) -> io::Result<fs::File> {
        state::ensure_private_dir(&self.dir)?;
        let mut options = fs::OpenOptions::new();
        options.append(true).create(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600).custom_flags(nix::libc::O_NOFOLLOW);
        }
        let file = options.open(self.path())?;
        let metadata = file.metadata()?;
        if !metadata.is_file() {
            return Err(io::Error::new(
                io::ErrorKind::PermissionDenied,
                "MCP audit log is not a regular file",
            ));
        }
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            if metadata.permissions().mode() & 0o077 != 0 {
                return Err(io::Error::new(
                    io::ErrorKind::PermissionDenied,
                    "MCP audit log permissions are too broad",
                ));
            }
        }
        Ok(file)
    }

    fn read_lines(&self) -> io::Result<Vec<Line>> {
        let file = match fs::File::open(self.path()) {
            Ok(file) => file,
            Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(Vec::new()),
            Err(error) => return Err(error),
        };
        let mut lines = Vec::new();
        for line in BufReader::new(file).split(b'\n') {
            let line = line?;
            if line.len() > MAX_LINE {
                continue;
            }
            if let Ok(parsed) = serde_json::from_slice::<Line>(&line) {
                lines.push(parsed);
            }
        }
        Ok(lines)
    }

    /// Rewrite the log without expired lines (and within the size cap) when
    /// the oldest line has expired or the file is too large.
    fn prune(&self, now: i64) -> io::Result<()> {
        let size = match fs::symlink_metadata(self.path()) {
            Ok(metadata) => metadata.len(),
            Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(()),
            Err(error) => return Err(error),
        };
        let lines = self.read_lines()?;
        let expired = lines
            .first()
            .is_some_and(|line| now - line.at() >= RETENTION_MS);
        if !expired && size <= MAX_BYTES {
            return Ok(());
        }
        let size_of =
            |line: &Line| serde_json::to_vec(line).map_or(0, |body| body.len() as u64 + 1);
        let mut kept: Vec<&Line> = lines
            .iter()
            .filter(|line| now - line.at() < RETENTION_MS)
            .collect();
        let mut total: u64 = kept.iter().map(|line| size_of(line)).sum();
        // A client that keeps retrying is refused before a dialog and can append unlimited
        // Rejected entries. Drop the oldest of those first so they cannot push out Allowed or
        // Denied decisions, which are the evidence the log exists for.
        while total > MAX_BYTES / 2 {
            let Some(index) = kept.iter().position(|line| line.rejected()) else {
                break;
            };
            total = total.saturating_sub(size_of(kept.remove(index)));
        }
        while total > MAX_BYTES / 2 && !kept.is_empty() {
            total = total.saturating_sub(size_of(kept.remove(0)));
        }
        let kept: Vec<Vec<u8>> = kept
            .iter()
            .filter_map(|line| serde_json::to_vec(line).ok())
            .collect();
        let temporary = self.dir.join(format!(".audit-{}.tmp", std::process::id()));
        let result = (|| {
            let mut options = fs::OpenOptions::new();
            options.write(true).create_new(true);
            #[cfg(unix)]
            {
                use std::os::unix::fs::OpenOptionsExt;
                options.mode(0o600);
            }
            let mut file = options.open(&temporary)?;
            for line in &kept {
                file.write_all(line)?;
                file.write_all(b"\n")?;
            }
            file.sync_all()?;
            fs::rename(&temporary, self.path())
        })();
        if result.is_err() {
            let _ = fs::remove_file(&temporary);
        }
        result
    }
}

/// Milliseconds since the Unix epoch.
pub fn now_ms() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|duration| duration.as_millis() as i64)
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;

    const DAY: i64 = 24 * 60 * 60 * 1000;

    fn entry(id: &str, at: i64) -> AuditEntry {
        AuditEntry {
            id: id.to_owned(),
            at,
            tool: "install_app".to_owned(),
            object: "cask/firefox".to_owned(),
            caller: AuditCaller {
                path: Some("/Applications/Claude.app/Contents/MacOS/Claude".to_owned()),
                signer: Some("Developer ID Application: Example".to_owned()),
                team_id: Some("ABCDE12345".to_owned()),
                signed: true,
                may_come_from: None,
                claimed_name: Some("claude-ai".to_owned()),
                pid: Some(42),
            },
            decision: AuditDecision::Allowed,
            result: None,
        }
    }

    #[test]
    fn records_merge_results_newest_first_and_stay_private() {
        use std::os::unix::fs::PermissionsExt;

        let dir = tempfile::tempdir().expect("tempdir");
        let audit = Audit::new(dir.path());
        assert!(audit.list(10).expect("empty").is_empty());
        audit.record(&entry("a", 1_000), 1_000).expect("a");
        audit.record(&entry("b", 2_000), 2_000).expect("b");
        audit
            .record_result("a", "queued:t1", 3_000)
            .expect("result");
        let listed = audit.list(4_000).expect("list");
        assert_eq!(
            listed.iter().map(|e| e.id.as_str()).collect::<Vec<_>>(),
            ["b", "a"]
        );
        assert_eq!(listed[1].result.as_deref(), Some("queued:t1"));
        assert_eq!(listed[0].result, None);
        let mode = fs::metadata(audit.path())
            .expect("meta")
            .permissions()
            .mode();
        assert_eq!(mode & 0o777, 0o600);
        let body = fs::read_to_string(audit.path()).expect("read");
        assert!(!body.contains("arguments"), "never stores raw arguments");
    }

    #[test]
    fn rejected_refusals_do_not_evict_user_decisions() {
        let dir = tempfile::tempdir().expect("tempdir");
        let audit = Audit::new(dir.path());
        let now = 1_000_000_000_000i64;
        for index in 0..5 {
            let mut allowed = entry(&format!("allowed-{index}"), now - 100 + index);
            allowed.decision = AuditDecision::Allowed;
            audit.record(&allowed, now).expect("allowed");
        }
        // A blocked client can retry without a dialog: each refusal is Rejected and large enough to
        // reach the size cap quickly, but none may evict the decisions above.
        let mut rejected = entry("rejected", now - 50);
        rejected.decision = AuditDecision::Rejected;
        rejected.caller.claimed_name = Some("x".repeat(11_000));
        for _ in 0..400 {
            audit.record(&rejected, now).expect("rejected");
        }
        let listed = audit.list(now).expect("list");
        let allowed = listed
            .iter()
            .filter(|entry| entry.decision == AuditDecision::Allowed)
            .count();
        assert_eq!(allowed, 5, "rotation protects user decisions");
        assert!(
            listed
                .iter()
                .any(|entry| entry.decision == AuditDecision::Rejected),
            "recent refusals stay visible"
        );
    }

    #[test]
    fn expired_entries_are_hidden_and_pruned_on_write() {
        let dir = tempfile::tempdir().expect("tempdir");
        let audit = Audit::new(dir.path());
        audit.record(&entry("old", 0), 0).expect("old");
        audit
            .record(&entry("new", 50 * DAY), 50 * DAY)
            .expect("new");
        let now = 91 * DAY;
        assert_eq!(audit.list(now).expect("list").len(), 1);
        audit.record(&entry("later", now), now).expect("later");
        let body = fs::read_to_string(audit.path()).expect("read");
        assert!(!body.contains("\"old\""), "expired line rewritten away");
        assert_eq!(audit.list(now).expect("list").len(), 2);
    }

    #[test]
    fn clear_removes_everything() {
        let dir = tempfile::tempdir().expect("tempdir");
        let audit = Audit::new(dir.path());
        audit.clear().expect("clear missing");
        audit.record(&entry("a", 1), 1).expect("a");
        audit.clear().expect("clear");
        assert!(audit.list(2).expect("list").is_empty());
    }

    #[test]
    fn unwritable_log_fails_check_and_record() {
        use std::os::unix::fs::PermissionsExt;

        if unsafe { nix::libc::geteuid() } == 0 {
            return;
        }
        let dir = tempfile::tempdir().expect("tempdir");
        let audit = Audit::new(dir.path());
        audit.check().expect("writable");
        fs::set_permissions(audit.path(), fs::Permissions::from_mode(0o400)).expect("chmod");
        assert!(audit.check().is_err());
        assert!(audit.record(&entry("a", 1), 1).is_err());
        fs::set_permissions(audit.path(), fs::Permissions::from_mode(0o600)).expect("chmod");
    }

    #[test]
    fn symlinked_log_is_refused() {
        let dir = tempfile::tempdir().expect("tempdir");
        let audit = Audit::new(dir.path());
        state::ensure_private_dir(&state::mcp_dir(dir.path())).expect("dir");
        let target = dir.path().join("elsewhere");
        fs::write(&target, b"").expect("target");
        std::os::unix::fs::symlink(&target, audit.path()).expect("symlink");
        assert!(audit.check().is_err());
        assert_eq!(fs::read(&target).expect("read"), b"");
    }
}
