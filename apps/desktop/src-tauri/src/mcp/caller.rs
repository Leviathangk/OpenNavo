//! Who is on the other end of a Local MCP connection.
//!
//! The socket gives the broker the pid of the `mcp` subprocess. Its direct
//! parent is the process that launched it (an AI app, or a shell or helper
//! acting for one): that parent's executable is what the confirmation dialog
//! names, with its code signature checked by the app. The nearest `.app`
//! further up the process tree is shown separately and marked unverified,
//! because a parent can be re-parented and pids can be reused.
//!
//! A pid alone is not an identity: the kernel recycles numbers as soon as a
//! process exits. A pid is therefore named together with the start time it was
//! read with ([`PidStamp`]); a later read that returns a different start time is
//! a different process, and nothing that pid says is believed.
//!
//! Tauri-free so the ancestry walk can be tested with a fake process table.

use std::path::{Component, Path, PathBuf};

/// Longest ancestry walk; a real tree is far shallower.
const MAX_DEPTH: usize = 32;

/// A pid and a moment that process started. The scaling differs by platform
/// (microseconds since the Unix epoch on macOS, clock ticks since boot times
/// 10,000 on Linux), so the value is an opaque token: only `matches` compares
/// it, and nothing may treat it as a wall-clock time. Two reads are the same
/// process only when both halves match, so a recycled pid cannot inherit the
/// trust the previous holder had.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct PidStamp {
    pub pid: i32,
    /// `None` when the platform could not report one; see [`PidStamp::matches`].
    ///
    /// An opaque token, not a timestamp: the origin and the unit differ per
    /// platform (see the type's own docs). Deriving a duration or formatting a
    /// date from it gives a plausible-looking answer that means nothing, and
    /// comparing values taken on different platforms means less than nothing.
    pub started_at: Option<u64>,
}

impl PidStamp {
    pub fn new(pid: i32, started_at: Option<u64>) -> Self {
        Self { pid, started_at }
    }

    /// Whether `process` is still the process this stamp was taken from.
    ///
    /// When either read lacks a start time there is no proof either way, so the
    /// answer is `false`: a stamp that cannot be checked never vouches for
    /// anyone. Callers that only *describe* a process (the dialog, the audit
    /// log) may still use the pid; callers that *trust* it must not.
    pub fn matches(&self, process: &ProcessInfo) -> bool {
        self.pid == process.pid
            && match (self.started_at, process.started_at) {
                (Some(then), Some(now)) => then == now,
                _ => false,
            }
    }
}

/// One process as seen by the broker.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ProcessInfo {
    pub pid: i32,
    pub ppid: i32,
    pub path: Option<PathBuf>,
    /// Microseconds since the Unix epoch when this process started.
    pub started_at: Option<u64>,
}

impl ProcessInfo {
    pub fn stamp(&self) -> PidStamp {
        PidStamp::new(self.pid, self.started_at)
    }
}

/// Process lookups, injectable for tests.
pub trait ProcessTable {
    fn info(&self, pid: i32) -> Option<ProcessInfo>;
}

/// The caller as far as the process tree tells.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct CallerProcess {
    /// Direct parent of the `mcp` subprocess.
    pub parent_pid: Option<i32>,
    pub parent_path: Option<PathBuf>,
    /// Nearest `.app` bundle above the parent, when it is not the parent's
    /// own bundle. Unverified.
    pub may_come_from: Option<PathBuf>,
}

/// Whether the socket peer named by `peer` still runs exactly `expected` (both
/// paths canonicalised). Writes are only accepted from OpenNavo's own `mcp`
/// subcommand, so a same-user script talking to the socket directly cannot
/// borrow its parent's signature for the confirmation dialog.
///
/// The pid must still be the process the stamp was taken from: a recycled pid
/// fails here even when the new holder runs the same binary.
pub fn peer_runs(table: &dyn ProcessTable, peer: Option<PidStamp>, expected: &Path) -> bool {
    let Some(peer) = peer else {
        return false;
    };
    let Some(info) = table.info(peer.pid) else {
        return false;
    };
    if !peer.matches(&info) {
        return false;
    }
    let Some(path) = info.path else {
        return false;
    };
    match (std::fs::canonicalize(path), std::fs::canonicalize(expected)) {
        (Ok(actual), Ok(expected)) => actual == expected,
        _ => false,
    }
}

/// Resolve the parent and the nearest `.app` ancestor of the stamped peer.
///
/// Returns nothing when the pid no longer belongs to the stamped process: the
/// parent it reports would be a stranger's parent, and the dialog would then
/// name an unrelated app.
pub fn resolve(table: &dyn ProcessTable, peer: Option<PidStamp>) -> CallerProcess {
    let Some(peer) = peer else {
        return CallerProcess::default();
    };
    let Some(peer_info) = table.info(peer.pid) else {
        return CallerProcess::default();
    };
    if !peer.matches(&peer_info) {
        return CallerProcess::default();
    }
    if peer_info.ppid <= 1 {
        return CallerProcess::default();
    }
    let Some(parent) = table.info(peer_info.ppid) else {
        return CallerProcess {
            parent_pid: Some(peer_info.ppid),
            ..CallerProcess::default()
        };
    };
    let own_bundle = parent.path.as_deref().and_then(app_bundle);
    let mut may_come_from = None;
    let mut current = parent.clone();
    for _ in 0..MAX_DEPTH {
        if current.ppid <= 1 || current.ppid == current.pid {
            break;
        }
        let Some(next) = table.info(current.ppid) else {
            break;
        };
        if let Some(bundle) = next.path.as_deref().and_then(app_bundle) {
            if own_bundle.as_ref() != Some(&bundle) {
                may_come_from = Some(bundle);
            }
            break;
        }
        current = next;
    }
    CallerProcess {
        parent_pid: Some(parent.pid),
        parent_path: parent.path,
        may_come_from,
    }
}

/// The outermost `*.app` directory in `path`, if any.
pub fn app_bundle(path: &Path) -> Option<PathBuf> {
    let mut bundle = PathBuf::new();
    for component in path.components() {
        bundle.push(component);
        if let Component::Normal(name) = component
            && Path::new(name)
                .extension()
                .is_some_and(|extension| extension.eq_ignore_ascii_case("app"))
        {
            return Some(bundle);
        }
    }
    None
}

/// Display name of a bundle path: `Claude` for `/Applications/Claude.app`.
pub fn bundle_name(bundle: &Path) -> String {
    bundle
        .file_stem()
        .map(|stem| stem.to_string_lossy().into_owned())
        .unwrap_or_else(|| bundle.to_string_lossy().into_owned())
}

#[cfg(target_os = "macos")]
fn started_at(info: &nix::libc::proc_bsdinfo) -> u64 {
    info.pbi_start_tvsec
        .saturating_mul(1_000_000)
        .saturating_add(info.pbi_start_tvusec)
}

/// When `pid` started, read at this moment. `None` when the process is gone or
/// the platform will not say; a peer stamped without a start time cannot be
/// trusted later, by design.
pub fn started_at_now(pid: i32) -> Option<u64> {
    SystemProcesses.info(pid).and_then(|info| info.started_at)
}

/// The real process table.
pub struct SystemProcesses;

impl ProcessTable for SystemProcesses {
    #[cfg(target_os = "macos")]
    fn info(&self, pid: i32) -> Option<ProcessInfo> {
        use nix::libc;
        if pid <= 0 {
            return None;
        }
        let mut info: libc::proc_bsdinfo = unsafe { std::mem::zeroed() };
        let size = std::mem::size_of::<libc::proc_bsdinfo>() as libc::c_int;
        let read = unsafe {
            libc::proc_pidinfo(
                pid,
                libc::PROC_PIDTBSDINFO,
                0,
                (&mut info as *mut libc::proc_bsdinfo).cast(),
                size,
            )
        };
        if read != size {
            return None;
        }
        let mut buffer = vec![0u8; libc::PROC_PIDPATHINFO_MAXSIZE as usize];
        let length =
            unsafe { libc::proc_pidpath(pid, buffer.as_mut_ptr().cast(), buffer.len() as u32) };
        let path = (length > 0).then(|| {
            use std::os::unix::ffi::OsStrExt;
            buffer.truncate(length as usize);
            PathBuf::from(std::ffi::OsStr::from_bytes(&buffer))
        });
        Some(ProcessInfo {
            pid,
            ppid: info.pbi_ppid as i32,
            path,
            started_at: Some(started_at(&info)),
        })
    }

    #[cfg(not(target_os = "macos"))]
    fn info(&self, pid: i32) -> Option<ProcessInfo> {
        if pid <= 0 {
            return None;
        }
        let stat = std::fs::read_to_string(format!("/proc/{pid}/stat")).ok()?;
        let (ppid, started_ticks) = linux_stat(&stat)?;
        let path = std::fs::read_link(format!("/proc/{pid}/exe")).ok();
        Some(ProcessInfo {
            pid,
            ppid,
            path,
            // `starttime` counts clock ticks since boot, at whatever `CLK_TCK`
            // the kernel was built with (usually, but not guaranteed, 100). This
            // multiplies it into the microseconds macOS reports so the two arms
            // share a type, but the origin still differs: this value is an
            // opaque token, never a time (see [`PidStamp`]). At the usual
            // `CLK_TCK` of 100 the resolution here is 10 ms, so two starts
            // inside one tick are indistinguishable — closing that would mean
            // recycling a pid inside a single tick, which needs on the order of
            // 4e8 forks per second against a measured ~3.4e3.
            started_at: Some(started_ticks.saturating_mul(10_000)),
        })
    }
}

/// `ppid` and `starttime` from the text of `/proc/<pid>/stat`.
///
/// Field numbers follow `proc(5)`: 1 `pid`, 2 `comm` (parenthesised and possibly
/// containing spaces or `)`, hence the split on the *last* `)`), 3 `state`,
/// 4 `ppid`, 22 `starttime`. `starttime` is in clock ticks since boot; callers
/// scale it, and only equality of the scaled value is ever compared.
#[cfg(any(not(target_os = "macos"), test))]
fn linux_stat(stat: &str) -> Option<(i32, u64)> {
    let after = stat.rsplit_once(')')?.1;
    let fields: Vec<&str> = after.split_whitespace().collect();
    // Index 0 is field 3 (`state`), so field N sits at index N - 3.
    let ppid = fields.get(4 - 3)?.parse().ok()?;
    let started_ticks = fields.get(22 - 3)?.parse().ok()?;
    Some((ppid, started_ticks))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    struct Table(HashMap<i32, ProcessInfo>);

    impl Table {
        /// Rows are `(pid, ppid, path)`; every process gets a distinct start time
        /// derived from its pid, so a row built here is stable unless a test
        /// replaces it.
        fn new(rows: &[(i32, i32, &str)]) -> Self {
            Self(
                rows.iter()
                    .map(|(pid, ppid, path)| {
                        (
                            *pid,
                            ProcessInfo {
                                pid: *pid,
                                ppid: *ppid,
                                path: (!path.is_empty()).then(|| PathBuf::from(path)),
                                started_at: Some(started(*pid)),
                            },
                        )
                    })
                    .collect(),
            )
        }

        fn stamp(&self, pid: i32) -> PidStamp {
            self.0
                .get(&pid)
                .map(ProcessInfo::stamp)
                .unwrap_or(PidStamp::new(pid, None))
        }

        /// Replace `pid` with a different process that happens to reuse the number.
        fn recycled(mut self, pid: i32, path: &str) -> Self {
            if let Some(info) = self.0.get_mut(&pid) {
                info.path = Some(PathBuf::from(path));
                info.started_at = Some(started(pid) + 1_000_000);
            }
            self
        }

        /// The same pid, but the platform refused to report when it started.
        fn without_start_time(mut self, pid: i32) -> Self {
            if let Some(info) = self.0.get_mut(&pid) {
                info.started_at = None;
            }
            self
        }
    }

    fn started(pid: i32) -> u64 {
        1_700_000_000_000_000 + pid as u64 * 1_000_000
    }

    impl ProcessTable for Table {
        fn info(&self, pid: i32) -> Option<ProcessInfo> {
            self.0.get(&pid).cloned()
        }
    }

    #[test]
    fn app_parent_has_no_separate_origin() {
        let table = Table::new(&[
            (
                30,
                20,
                "/Applications/OpenNavo.app/Contents/MacOS/opennavo-desktop",
            ),
            (20, 1, "/Applications/Claude.app/Contents/MacOS/Claude"),
        ]);
        let caller = resolve(&table, Some(table.stamp(30)));
        assert_eq!(caller.parent_pid, Some(20));
        assert_eq!(
            caller.parent_path.as_deref(),
            Some(Path::new("/Applications/Claude.app/Contents/MacOS/Claude"))
        );
        assert_eq!(caller.may_come_from, None);
    }

    #[test]
    fn helper_parent_points_to_nearest_app_ancestor() {
        let table = Table::new(&[
            (
                40,
                30,
                "/Applications/OpenNavo.app/Contents/MacOS/opennavo-desktop",
            ),
            (30, 20, "/opt/homebrew/bin/node"),
            (20, 10, "/bin/zsh"),
            (10, 1, "/Applications/Cursor.app/Contents/MacOS/Cursor"),
        ]);
        let caller = resolve(&table, Some(table.stamp(40)));
        assert_eq!(
            caller.parent_path.as_deref(),
            Some(Path::new("/opt/homebrew/bin/node"))
        );
        assert_eq!(
            caller.may_come_from.as_deref(),
            Some(Path::new("/Applications/Cursor.app"))
        );
        assert_eq!(
            bundle_name(caller.may_come_from.as_deref().unwrap()),
            "Cursor"
        );
    }

    #[test]
    fn nested_helper_bundle_uses_outermost_app() {
        assert_eq!(
            app_bundle(Path::new(
                "/Applications/Claude.app/Contents/Frameworks/Claude Helper.app/Contents/MacOS/Claude Helper"
            )),
            Some(PathBuf::from("/Applications/Claude.app"))
        );
        assert_eq!(app_bundle(Path::new("/usr/bin/python3")), None);
    }

    #[test]
    fn unknown_or_orphaned_peers_resolve_to_nothing() {
        let table = Table::new(&[(30, 1, "/x/mcp")]);
        assert_eq!(
            resolve(&table, Some(PidStamp::new(99, None))),
            CallerProcess::default()
        );
        assert_eq!(
            resolve(&table, Some(table.stamp(30))),
            CallerProcess::default()
        );
    }

    #[test]
    fn cycles_and_deep_trees_terminate() {
        let table = Table::new(&[(3, 2, "/a"), (2, 4, "/b"), (4, 2, "/c")]);
        let caller = resolve(&table, Some(table.stamp(3)));
        assert_eq!(caller.parent_pid, Some(2));
        assert_eq!(caller.may_come_from, None);
    }

    #[test]
    fn system_table_reads_this_process() {
        let me = SystemProcesses
            .info(std::process::id() as i32)
            .expect("own process");
        assert!(me.ppid > 0);
        assert!(me.path.is_some_and(|path| path.is_absolute()));
        assert!(
            me.started_at.is_some_and(|at| at > 0),
            "the real table must report when this process started"
        );
    }

    #[test]
    fn peer_must_run_the_expected_binary() {
        let me = std::env::current_exe().expect("exe");
        let pid = std::process::id() as i32;
        let stamp = SystemProcesses.info(pid).expect("own process").stamp();
        assert!(peer_runs(&SystemProcesses, Some(stamp), &me));
        assert!(!peer_runs(
            &SystemProcesses,
            Some(stamp),
            Path::new("/bin/sh")
        ));
        assert!(!peer_runs(
            &SystemProcesses,
            Some(PidStamp::new(pid, None)),
            &me
        ));
        assert!(!peer_runs(
            &SystemProcesses,
            Some(PidStamp::new(pid, stamp.started_at.map(|at| at + 1))),
            &me
        ));
        assert!(!peer_runs(
            &SystemProcesses,
            Some(stamp),
            Path::new("/nonexistent/opennavo")
        ));
    }

    #[test]
    fn a_recycled_pid_is_not_the_process_the_stamp_named() {
        let table = Table::new(&[(
            30,
            20,
            "/Applications/OpenNavo.app/Contents/MacOS/opennavo-desktop",
        )]);
        let stamp = table.stamp(30);
        let recycled = table.recycled(30, "/tmp/evil");
        // Same number, same path even — a different start time means a different
        // process, so the ancestry walk must refuse to name its parent.
        assert_eq!(
            recycled.0[&30].path.as_deref(),
            Some(Path::new("/tmp/evil"))
        );
        assert_eq!(resolve(&recycled, Some(stamp)), CallerProcess::default());
        assert_eq!(resolve(&recycled, Some(stamp)).parent_pid, None);
    }

    #[test]
    fn a_process_that_reports_no_start_time_never_vouches_for_anyone() {
        // A path that really exists: `peer_runs` canonicalises both sides.
        let exe = std::env::current_exe().expect("exe");
        let exe = exe.to_str().expect("utf-8 path");
        let expected = Path::new(exe);
        let row = [(30, 20, exe)];
        let table = Table::new(&row);
        let stamped = table.stamp(30);
        let unknown = Table::new(&row).without_start_time(30);
        // A peer that cannot be checked proves nothing, and neither does a stamp
        // taken without a start time: both sides must be able to answer.
        assert!(!peer_runs(&unknown, Some(stamped), expected));
        assert!(!peer_runs(&table, Some(unknown.stamp(30)), expected));
        assert!(!peer_runs(&table, Some(PidStamp::new(30, None)), expected));
        assert_eq!(resolve(&unknown, Some(stamped)), CallerProcess::default());
        // With both sides present it is the same process and the check passes.
        assert!(peer_runs(&table, Some(stamped), expected));
    }

    /// A real `/proc/<pid>/stat` shape: `pid (comm) state ppid ...`. Field 22
    /// (`starttime`) is the last one written here.
    fn proc_stat(comm: &str, ppid: &str, starttime: &str) -> String {
        // Fields 5..=21 are placeholders; only their count matters, so that
        // `starttime` really is the twenty-second whitespace-separated value of
        // the line (index 19 after the closing paren).
        let middle = "0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0";
        format!("4242 ({comm}) S {ppid} {middle} {starttime}\n")
    }

    #[test]
    fn linux_stat_reads_field_4_and_field_22() {
        let stat = proc_stat("node", "1234", "987654");
        assert_eq!(linux_stat(&stat), Some((1234, 987_654)));
    }

    #[test]
    fn linux_stat_survives_a_command_name_with_spaces_and_parens() {
        // `comm` is capped at 15 bytes and can contain both; splitting on the
        // first `)` would read the wrong field for the rest of the line.
        let stat = proc_stat("my (weird) app", "77", "5");
        assert_eq!(linux_stat(&stat), Some((77, 5)));
    }

    #[test]
    fn linux_stat_refuses_a_truncated_line_rather_than_guessing() {
        assert_eq!(linux_stat("garbage"), None);
        assert_eq!(linux_stat("4242 (node) S 1 0 0"), None);
        assert_eq!(
            linux_stat("4242 (node) S notanumber 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0"),
            None
        );
    }

    #[test]
    fn a_stamp_matches_only_the_exact_pid_and_start_time() {
        let info = ProcessInfo {
            pid: 7,
            ppid: 1,
            path: None,
            started_at: Some(42),
        };
        assert!(PidStamp::new(7, Some(42)).matches(&info));
        assert!(!PidStamp::new(8, Some(42)).matches(&info));
        assert!(!PidStamp::new(7, Some(43)).matches(&info));
        assert!(!PidStamp::new(7, None).matches(&info));
        assert!(!PidStamp::new(7, Some(42)).matches(&ProcessInfo {
            started_at: None,
            ..info
        }));
    }

    /// `started_at` is an identity token, never a time.
    ///
    /// The two platforms fill it from different origins and units: microseconds
    /// since the Unix epoch on macOS, clock ticks since boot times 10,000 on
    /// Linux. Only equality across the value therefore means "same process".
    /// Nothing in this module orders the values, and this pins that: a stamp
    /// must not match a process whose start value is merely greater or merely
    /// less, because there is no ordering here to reason with in the first
    /// place — the numbers are not on the same scale.
    ///
    /// The practical hazard is a later reader deciding to compute a duration or
    /// format a date from this field. On one platform that yields a believable
    /// answer and on the other a value near 1.7e15; either way it is noise, and
    /// a comparison between the two is meaningless. If this test goes red,
    /// something started treating the token as a clock.
    #[test]
    fn a_stamp_is_an_opaque_token_not_a_time() {
        let at = |value: Option<u64>| ProcessInfo {
            pid: 7,
            ppid: 1,
            path: None,
            started_at: value,
        };

        // Identity: the same value is the same process.
        assert!(PidStamp::new(7, Some(41)).matches(&at(Some(41))));
        assert!(PidStamp::new(7, Some(42)).matches(&at(Some(42))));

        // A different value is a different process, in **either** direction.
        // The second assertion is what fails if the comparison ever loosens
        // from `==` to an ordering: under `then < now` a stamp taken at 41
        // would vouch for a process that started at 42.
        assert!(!PidStamp::new(7, Some(42)).matches(&at(Some(41))));
        assert!(!PidStamp::new(7, Some(41)).matches(&at(Some(42))));

        // The two platforms' numbers are not comparable at all: a Linux-shaped
        // value (small, ticks since boot) and a macOS-shaped one (large,
        // microseconds since the epoch) are just two different tokens. Neither
        // direction may be read as "older" or "newer".
        let from_linux = PidStamp::new(7, Some(123_450_000));
        let from_macos = PidStamp::new(7, Some(1_700_000_000_000_000));
        assert!(!from_linux.matches(&at(Some(1_700_000_000_000_000))));
        assert!(!from_macos.matches(&at(Some(123_450_000))));

        // And the value is `Option` precisely because a missing one proves
        // nothing: it can never be compared into a match.
        assert!(!from_linux.matches(&at(None)));
        assert!(!PidStamp::new(7, None).matches(&at(Some(123_450_000))));
        assert!(!PidStamp::new(7, None).matches(&at(None)));
    }
}
