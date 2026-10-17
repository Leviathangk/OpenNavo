//! Scope and reporting shared by the walker, without opening excluded files.
use super::{AvScanKind, files, own_data::OwnData};
use std::{
    collections::BTreeSet,
    fs,
    io::Read,
    os::unix::fs::MetadataExt,
    path::{Path, PathBuf},
};

#[derive(Default)]
pub(super) struct Policy<'a> {
    pub own_data: Option<&'a OwnData>,
    pub shallow: BTreeSet<PathBuf>,
    pub code_only: BTreeSet<PathBuf>,
    pub denied_roots: BTreeSet<String>,
}

impl<'a> Policy<'a> {
    pub fn for_scan(kind: AvScanKind, roots: &[PathBuf], own_data: &'a OwnData) -> Self {
        let mut policy = Self {
            own_data: Some(own_data),
            ..Default::default()
        };
        if kind == AvScanKind::Quick {
            policy.shallow.extend(temporary_roots());
            policy.code_only.extend(
                roots
                    .iter()
                    .filter(|p| p.extension().is_some_and(|e| e.eq_ignore_ascii_case("app")))
                    .cloned(),
            );
        }
        policy
    }

    pub fn allows(&self, path: &Path, root: &Path, meta: &fs::Metadata) -> bool {
        if self.shallow.contains(root) && path != root {
            // Do not descend into system services' TemporaryItems or other users'
            // directories. A quick scan only reads our ordinary top-level files.
            return meta.is_file() && meta.uid() == unsafe { nix::libc::geteuid() };
        }
        true
    }

    /// Called only after own-data, symlink, dataless and size guards. Executable
    /// permissions are not evidence of code: some Apps ship all resources 0755.
    pub fn allows_file(
        &self,
        path: &Path,
        root: &Path,
        limit: u64,
    ) -> Result<bool, crate::AppError> {
        if !self.code_only.contains(root)
            || limit == files::MAX_INSTALLER_FILE
            || path.extension().is_some_and(|e| {
                [
                    "dylib",
                    "so",
                    "sh",
                    "bash",
                    "zsh",
                    "command",
                    "py",
                    "pl",
                    "rb",
                    "js",
                    "mjs",
                    "cjs",
                    "scpt",
                    "applescript",
                    "workflow",
                    "wasm",
                ]
                .iter()
                .any(|s| e.eq_ignore_ascii_case(s))
            })
        {
            return Ok(true);
        }
        let mut file = files::open_read_guarded(path, limit, self.own_data)?;
        let mut head = [0; 4];
        let n = file.read(&mut head)?;
        let head = &head[..n];
        Ok(head.starts_with(b"#!")
            || head.starts_with(b"MZ")
            || [
                b"\xfe\xed\xfa\xce".as_slice(),
                b"\xce\xfa\xed\xfe",
                b"\xfe\xed\xfa\xcf",
                b"\xcf\xfa\xed\xfe",
                b"\xca\xfe\xba\xbe",
                b"\xbe\xba\xfe\xca",
                b"\xca\xfe\xba\xbf",
                b"\xbf\xba\xfe\xca",
                b"\x7fELF",
            ]
            .contains(&head))
    }

    pub fn denied(&mut self, path: &Path, root: &Path, error: &std::io::Error) {
        if path == root
            && matches!(
                error.raw_os_error(),
                Some(nix::libc::EACCES | nix::libc::EPERM)
            )
        {
            log::warn!("AV scan root denied: {}: {error}", path.display());
            self.denied_roots
                .insert(path.to_string_lossy().into_owned());
        }
    }
}

pub(super) fn temporary_roots() -> Vec<PathBuf> {
    let mut paths = std::env::var_os("TMPDIR")
        .and_then(|p| PathBuf::from(p).canonicalize().ok())
        .into_iter()
        .collect::<Vec<_>>();
    if let Ok(path) = Path::new("/tmp").canonicalize()
        && !paths.contains(&path)
    {
        paths.push(path);
    }
    paths
}
