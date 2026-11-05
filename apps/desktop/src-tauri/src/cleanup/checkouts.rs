use super::*;
use std::{
    io::Read,
    process::{Command, Stdio},
};

/// Git is quick; a hung network filesystem or credential prompt must not stall a category.
const GIT_TIMEOUT: Duration = Duration::from_secs(5);
/// A duplicate clone must have been idle this long before it is even listed.
const DUPLICATE_IDLE_DAYS: i64 = 30;

#[derive(Debug, PartialEq, Eq)]
enum GitError {
    Failed,
    Timeout,
}

#[derive(Debug)]
struct Repo {
    root: PathBuf,
    common: PathBuf,
    remote: Option<String>,
}
#[derive(Debug)]
struct Worktree {
    path: PathBuf,
    main: bool,
}
impl Collector<'_> {
    /// A failed probe can mean "not a repository/candidate"; a timed-out probe always leaves a gap in discovery.
    fn checkout_result<T>(&mut self, path: &Path, result: Result<T, GitError>) -> Option<T> {
        match result {
            Ok(value) => Some(value),
            Err(GitError::Timeout) => {
                self.result.incomplete(path, SkippedLocationReason::Timeout);
                None
            }
            Err(GitError::Failed) => None,
        }
    }
    fn checkout_expired(&mut self) -> bool {
        if Instant::now() < self.deadline {
            return false;
        }
        let home = self.engine.uninstall.roots.home.clone();
        self.result
            .incomplete(&home, SkippedLocationReason::Timeout);
        true
    }
    /// Read-only P2 discovery: linked worktrees and clones that share an origin. Nothing here is offered for
    /// removal; a later slice adds the gated `git worktree remove` / Trash actions.
    pub(super) fn checkouts(&mut self) -> Result<(), AppError> {
        let roots = self.project_roots.clone();
        let mut candidates = vec![];
        for root in roots {
            self.walk_checkouts(&root, 0, &mut candidates)?;
        }
        // A linked worktree and its main checkout share one common dir: keep a single repo, preferring the main
        // checkout so `worktree list` reports the linked ones instead of skipping itself.
        let mut found: BTreeMap<PathBuf, Vec<(PathBuf, Option<String>)>> = BTreeMap::new();
        for dir in candidates {
            self.check()?;
            if self.checkout_expired() {
                break;
            }
            let top = git_until(&dir, &["rev-parse", "--show-toplevel"], self.deadline);
            let Some(top) = self.checkout_result(&dir, top) else {
                continue;
            };
            let root = PathBuf::from(top.trim());
            let root = root.canonicalize().unwrap_or(root);
            let common = common_dir(&root, self.deadline);
            let Some(common) = self.checkout_result(&root, common) else {
                continue;
            };
            let remote = git_until(&root, &["remote", "get-url", "origin"], self.deadline);
            let remote = self
                .checkout_result(&root, remote)
                .map(|url| normalize_remote(url.trim()))
                .filter(|url| !url.is_empty());
            found.entry(common).or_default().push((root, remote));
        }
        let repos: Vec<Repo> = found
            .into_iter()
            .map(|(common, roots)| {
                let (root, _) = roots
                    .iter()
                    .find(|(root, _)| root.join(".git").is_dir())
                    .cloned()
                    .unwrap_or_else(|| roots[0].clone());
                // A linked worktree can carry the remote even when the main checkout's config is unusual.
                let remote = roots.iter().find_map(|(_, remote)| remote.clone());
                Repo {
                    root,
                    common,
                    remote,
                }
            })
            .collect();
        let mut listed = BTreeSet::new();
        for repo in &repos {
            if self.checkout_expired() {
                break;
            }
            self.worktrees(repo, &mut listed)?;
        }
        let mut by_remote: BTreeMap<String, Vec<&Repo>> = BTreeMap::new();
        for repo in &repos {
            if let Some(remote) = &repo.remote {
                by_remote.entry(remote.clone()).or_default().push(repo);
            }
        }
        for group in by_remote.values().filter(|group| group.len() > 1) {
            if self.checkout_expired() {
                break;
            }
            for repo in group {
                if self.checkout_expired() {
                    break;
                }
                let candidate = duplicate_candidate(&repo.root, self.deadline);
                if self.checkout_result(&repo.root, candidate) != Some(true) {
                    continue;
                }
                let owner = duplicate_owner(repo);
                let title = repo
                    .root
                    .file_name()
                    .map(|name| name.to_string_lossy().into_owned())
                    .unwrap_or_else(|| repo.root.display().to_string());
                self.add_checkout_item(
                    &repo.root,
                    owner,
                    title,
                    remote_name(&repo.remote.clone().unwrap_or_default()),
                    CleanupReason::DuplicateCheckout,
                )?;
            }
        }
        Ok(())
    }
    fn walk_checkouts(
        &mut self,
        dir: &Path,
        depth: usize,
        out: &mut Vec<PathBuf>,
    ) -> Result<(), AppError> {
        if depth > rules::PROJECT_MAX_DEPTH || self.checkout_expired() {
            return Ok(());
        }
        self.check()?;
        if dir.join(".git").exists() {
            out.push(dir.to_path_buf());
            return Ok(());
        }
        for child in self.children(dir)? {
            let name = child
                .file_name()
                .and_then(|name| name.to_str())
                .unwrap_or("");
            let generated = rules::PROJECT_ARTIFACTS
                .iter()
                .any(|(_, dirs)| dirs.contains(&name));
            if name.starts_with('.')
                || name.ends_with(".git")
                || name == "node_modules"
                || generated
            {
                continue;
            }
            self.walk_checkouts(&child, depth + 1, out)?;
        }
        Ok(())
    }
    fn worktrees(&mut self, repo: &Repo, listed: &mut BTreeSet<PathBuf>) -> Result<(), AppError> {
        let home = self.engine.uninstall.roots.home.clone();
        // The main checkout may sit outside the roots; derive the repository name from the common dir instead.
        let group_title = repo
            .common
            .parent()
            .and_then(|root| root.file_name())
            .map(|name| name.to_string_lossy().into_owned())
            .unwrap_or_else(|| repo.root.display().to_string());
        let output = git_until(
            &repo.root,
            &["worktree", "list", "--porcelain"],
            self.deadline,
        );
        let Some(output) = self.checkout_result(&repo.root, output) else {
            return Ok(());
        };
        for worktree in parse_worktrees(&output) {
            self.check()?;
            if self.checkout_expired() {
                break;
            }
            if worktree.main
                || !worktree.path.starts_with(&home)
                || !worktree.path.is_dir()
                || !belongs_to(&worktree.path, &repo.common)
                || !listed.insert(worktree.path.clone())
            {
                continue;
            }
            let title = worktree
                .path
                .file_name()
                .map(|name| name.to_string_lossy().into_owned())
                .unwrap_or_else(|| worktree.path.display().to_string());
            self.add_checkout_item(
                &worktree.path,
                checkout_owner(repo),
                title,
                group_title.clone(),
                CleanupReason::Worktree,
            )?;
        }
        Ok(())
    }
    fn add_checkout_item(
        &mut self,
        path: &Path,
        owner: Owner,
        title: String,
        group_title: String,
        reason: CleanupReason,
    ) -> Result<(), AppError> {
        self.progress(Some(path));
        let (bytes, incomplete) =
            tree::measure_filtered(path, self.flag, self.deadline, |_| false)?;
        if let Some(incomplete) = incomplete {
            self.result.incomplete(path, incomplete);
        }
        if bytes == 0 && incomplete.is_none() {
            return Ok(());
        }
        let modified_at = fs::symlink_metadata(path)
            .and_then(|meta| meta.modified())
            .ok()
            .and_then(|time| time.duration_since(std::time::UNIX_EPOCH).ok())
            .map(|time| time.as_millis().min(i64::MAX as u128) as i64);
        self.ensure_checkout_group(&owner, &group_title);
        let item = CleanupItem {
            id: uuid::Uuid::now_v7().to_string(),
            path: path.to_string_lossy().into_owned(),
            kind: CleanupItemKind::Dir,
            command: None,
            size_bytes: Some(bytes),
            size_complete: incomplete.is_none(),
            kept_open_files: 0,
            modified_at,
            reason,
            restore: CleanupRestore::Never,
            selected: false,
            delete_mode: DeleteMode::Trash,
            needs_admin: false,
            note: None,
        };
        self.insert(
            item,
            owner,
            title,
            None,
            ItemAction::Path {
                empty_only: false,
                bootout: None,
            },
        );
        Ok(())
    }
}
impl Collector<'_> {
    /// The first item in a group decides its title; checkouts want the repository name, not the first worktree's.
    fn ensure_checkout_group(&mut self, owner: &Owner, title: &str) {
        let key = owner
            .group_key
            .clone()
            .or_else(|| owner.bundle.clone())
            .unwrap_or_else(|| title.to_string());
        if self
            .result
            .category
            .groups
            .iter()
            .any(|group| group.id == key)
        {
            return;
        }
        self.result.category.groups.push(CleanupGroup {
            id: key,
            title: title.to_string(),
            subtitle: owner.group_subtitle.clone(),
            app_id: owner.app_id.clone(),
            bundle_id: owner.bundle.clone(),
            items: vec![],
        });
    }
}
fn checkout_owner(repo: &Repo) -> Owner {
    Owner {
        tool_key: Some("git".into()),
        group_key: Some(format!("checkout:{}", repo.common.display())),
        group_subtitle: repo
            .common
            .parent()
            .and_then(|root| root.parent())
            .map(|dir| dir.display().to_string()),
        ..Owner::default()
    }
}
fn duplicate_owner(repo: &Repo) -> Owner {
    Owner {
        tool_key: Some("git".into()),
        group_key: Some(format!(
            "checkout-remote:{}",
            repo.remote.clone().unwrap_or_default()
        )),
        group_subtitle: repo.remote.clone(),
        ..Owner::default()
    }
}
/// `git worktree list` can contain entries copied from another clone; only trust a `.git` file that points back at
/// this repository's common dir.
fn belongs_to(path: &Path, common: &Path) -> bool {
    let Ok(text) = fs::read_to_string(path.join(".git")) else {
        return false;
    };
    let Some(target) = text.trim().strip_prefix("gitdir: ") else {
        return false;
    };
    let target = PathBuf::from(target);
    let target = if target.is_absolute() {
        target
    } else {
        path.join(target)
    };
    target
        .canonicalize()
        .is_ok_and(|target| target.starts_with(common))
}
fn remote_name(remote: &str) -> String {
    remote
        .rsplit(['/', ':'])
        .next()
        .filter(|name| !name.is_empty())
        .unwrap_or(remote)
        .to_string()
}
/// A clean clone with nothing unpushed, no stash, and an idle index can be re-fetched if it is removed.
fn duplicate_candidate(root: &Path, deadline: Instant) -> Result<bool, GitError> {
    // Check the index first: `git status` can refresh its mtime, which would hide an otherwise idle clone.
    if !index_older_than(root, DUPLICATE_IDLE_DAYS) {
        return Ok(false);
    }
    // `status.showUntrackedFiles=no` must not make an untracked file look clean.
    let clean = git_until(
        root,
        &[
            "status",
            "--porcelain",
            "--untracked-files=all",
            "--ignore-submodules=none",
        ],
        deadline,
    )?
    .trim()
    .is_empty();
    if !clean {
        return Ok(false);
    }
    let no_unpushed = git_until(
        root,
        &["rev-list", "--count", "HEAD", "--not", "--remotes"],
        deadline,
    )?
    .trim()
        == "0";
    if !no_unpushed {
        return Ok(false);
    }
    Ok(git_until(root, &["stash", "list"], deadline)?
        .trim()
        .is_empty())
}
fn index_older_than(root: &Path, days: i64) -> bool {
    fs::symlink_metadata(root.join(".git/index"))
        .and_then(|meta| meta.modified())
        .ok()
        .and_then(|time| time.elapsed().ok())
        .is_some_and(|elapsed| elapsed.as_secs() >= days as u64 * 86_400)
}
fn common_dir(root: &Path, deadline: Instant) -> Result<PathBuf, GitError> {
    let raw = git_until(
        root,
        &["rev-parse", "--path-format=absolute", "--git-common-dir"],
        deadline,
    )
    .or_else(|error| match error {
        GitError::Failed => git_until(root, &["rev-parse", "--git-common-dir"], deadline),
        GitError::Timeout => Err(error),
    })?;
    let path = PathBuf::from(raw.trim());
    let path = if path.is_absolute() {
        path
    } else {
        root.join(path)
    };
    Ok(path.canonicalize().unwrap_or(path))
}
fn normalize_remote(url: &str) -> String {
    let url = redact_remote(url.trim()).trim_end_matches('/').to_string();
    let url = url.strip_suffix(".git").unwrap_or(&url);
    // Schemes and DNS hosts are case-insensitive; repository paths (including local paths) are not.
    if let Some((scheme, host, path)) = remote_url_parts(url) {
        return format!(
            "{}://{}{path}",
            scheme.to_ascii_lowercase(),
            host.to_ascii_lowercase()
        );
    }
    // SCP-style [user@]host:path. Preserve the SSH user and don't interpret local paths as authorities.
    let host_start = url
        .split(':')
        .next()
        .and_then(|authority| authority.rfind('@'))
        .map_or(0, |at| at + 1);
    let separator = if url[host_start..].starts_with('[') {
        url.find("]:").map(|end| end + 1)
    } else {
        url.find(':')
    };
    if let Some(separator) = separator {
        let (authority, path) = url.split_at(separator);
        let windows_drive = authority.len() == 1 && path[1..].starts_with(['/', '\\']);
        if !authority.contains(['/', '\\']) && !windows_drive && host_start <= authority.len() {
            return format!(
                "{}{}{path}",
                &authority[..host_start],
                authority[host_start..].to_ascii_lowercase()
            );
        }
    }
    url.to_string()
}
fn remote_url_parts(url: &str) -> Option<(&str, &str, &str)> {
    let (scheme, rest) = url.split_once("://")?;
    let mut bytes = scheme.bytes();
    if !bytes.next()?.is_ascii_alphabetic()
        || !bytes.all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'+' | b'-' | b'.'))
    {
        return None;
    }
    let (authority, path) = rest.split_at(rest.find(['/', '?', '#']).unwrap_or(rest.len()));
    Some((scheme, authority, path))
}
/// Remotes are shown in the UI; a token embedded in `https://user:token@host/...` must never reach it.
fn redact_remote(url: &str) -> String {
    let Some((scheme, authority, path)) = remote_url_parts(url) else {
        return url.to_string();
    };
    match authority.rsplit_once('@') {
        Some((_, host)) => format!("{scheme}://{host}{path}"),
        None => url.to_string(),
    }
}
fn parse_worktrees(output: &str) -> Vec<Worktree> {
    let mut worktrees = vec![];
    for (index, block) in output.split("\n\n").enumerate() {
        let mut path = None;
        for line in block.lines() {
            if let Some(value) = line.strip_prefix("worktree ") {
                path = Some(PathBuf::from(value));
            }
        }
        if let Some(path) = path {
            worktrees.push(Worktree {
                path,
                main: index == 0,
            });
        }
    }
    worktrees
}
/// One git call bounded by both the category deadline and the per-call cap.
fn git_until(dir: &Path, args: &[&str], deadline: Instant) -> Result<String, GitError> {
    let timeout = deadline.saturating_duration_since(Instant::now());
    if timeout.is_zero() {
        return Err(GitError::Timeout);
    }
    git(dir, args, timeout.min(GIT_TIMEOUT))
}
fn git(dir: &Path, args: &[&str], timeout: Duration) -> Result<String, GitError> {
    let mut child = Command::new("git")
        .current_dir(dir)
        .arg("--no-optional-locks")
        .args(args)
        .env("GIT_TERMINAL_PROMPT", "0")
        .env("GIT_ASKPASS", "/usr/bin/true")
        .env("SSH_ASKPASS", "/usr/bin/true")
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .map_err(|_| GitError::Failed)?;
    let stdout = child.stdout.take().ok_or(GitError::Failed)?;
    let reader = std::thread::spawn(move || {
        let mut bytes = vec![];
        stdout.take(1 << 20).read_to_end(&mut bytes).ok()?;
        Some(bytes)
    });
    let deadline = Instant::now() + timeout;
    let result = loop {
        match child.try_wait() {
            Ok(Some(status)) => {
                break if status.success() {
                    Ok(())
                } else {
                    Err(GitError::Failed)
                };
            }
            Ok(None) if Instant::now() < deadline => std::thread::sleep(Duration::from_millis(10)),
            state => {
                let _ = child.kill();
                let _ = child.wait();
                break Err(if state.is_ok() {
                    GitError::Timeout
                } else {
                    GitError::Failed
                });
            }
        }
    };
    let bytes = reader.join().ok().flatten();
    result?;
    String::from_utf8(bytes.ok_or(GitError::Failed)?).map_err(|_| GitError::Failed)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_linked_worktrees_and_marks_the_first_one_main() {
        let output = "worktree /src/app\nHEAD aaa\nbranch refs/heads/main\n\nworktree /src/app-feature\nHEAD bbb\ndetached\n";
        let worktrees = parse_worktrees(output);
        assert_eq!(worktrees.len(), 2);
        assert!(worktrees[0].main);
        assert!(!worktrees[1].main);
        assert_eq!(worktrees[1].path, PathBuf::from("/src/app-feature"));
    }
    #[test]
    fn remotes_compare_without_scheme_specific_suffixes() {
        assert_eq!(
            normalize_remote("HTTPS://GITHUB.COM/OpenNavo/OpenNavo.git/"),
            "https://github.com/OpenNavo/OpenNavo"
        );
        assert_eq!(
            normalize_remote("https://user:token@example.com/a.git"),
            "https://example.com/a"
        );
    }
    #[test]
    fn remote_paths_keep_their_case() {
        for (upper, lower) in [
            (
                "ssh://git@example.com/repos/App.git",
                "ssh://git@example.com/repos/app.git",
            ),
            (
                "git@example.com:repos/App.git",
                "git@example.com:repos/app.git",
            ),
            ("https://example.com/App.git", "https://example.com/app.git"),
            ("file:///repos/App.git", "file:///repos/app.git"),
            ("/repos/App.git", "/repos/app.git"),
            ("../App.git", "../app.git"),
        ] {
            assert_ne!(normalize_remote(upper), normalize_remote(lower), "{upper}");
        }
    }
    #[test]
    fn only_remote_schemes_and_hosts_are_case_insensitive() {
        for (remote, expected) in [
            (
                "SSH://git@EXAMPLE.COM/repos/App.git/",
                "ssh://example.com/repos/App",
            ),
            ("Git@EXAMPLE.COM:repos/App.git", "Git@example.com:repos/App"),
            (
                "Git@[FE80::ABCD]:repos/App.git",
                "Git@[fe80::abcd]:repos/App",
            ),
            ("C:/repos/App.git", "C:/repos/App"),
            ("./Repos:App.git", "./Repos:App"),
            ("/Repos/ssh://App.git", "/Repos/ssh://App"),
            (
                "EXAMPLE.COM:Repos/App@Work.git",
                "example.com:Repos/App@Work",
            ),
            (
                "https://user:token@EXAMPLE.COM/Repos/App@Work.git",
                "https://example.com/Repos/App@Work",
            ),
            (
                "https://EXAMPLE.COM/Repos/App@Work.git",
                "https://example.com/Repos/App@Work",
            ),
        ] {
            assert_eq!(normalize_remote(remote), expected, "{remote}");
        }
        assert_ne!(
            normalize_remote("Git@example.com:repos/App.git"),
            normalize_remote("git@example.com:repos/App.git")
        );
    }
    #[test]
    fn an_expired_budget_skips_git() {
        assert_eq!(
            git_until(Path::new("."), &["--version"], Instant::now()),
            Err(GitError::Timeout)
        );
    }
    #[test]
    fn a_recent_index_is_not_a_duplicate_yet() {
        let temp = tempfile::tempdir().unwrap();
        let repo = temp.path().join("repo");
        fs::create_dir_all(repo.join(".git")).unwrap();
        fs::write(repo.join(".git/index"), b"index").unwrap();
        assert!(!index_older_than(&repo, DUPLICATE_IDLE_DAYS));
    }
}
