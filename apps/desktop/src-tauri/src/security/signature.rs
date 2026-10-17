//! Local signature validation; cache keys cover bundle members as well as the executable.
use super::{SignatureInfo, SignatureStatus, files};
use crate::AppError;
use sha2::{Digest, Sha256};
use std::{
    collections::BTreeMap,
    io::Read,
    os::unix::fs::MetadataExt,
    path::{Path, PathBuf},
    sync::{Mutex, OnceLock},
    time::{Duration, Instant},
};

pub(crate) fn empty(status: SignatureStatus) -> SignatureInfo {
    SignatureInfo {
        status,
        team_id: None,
        signer: None,
        notarized: None,
    }
}
struct Cached {
    fingerprint: [u8; 32],
    at: Instant,
    info: SignatureInfo,
}
static CACHE: OnceLock<Mutex<BTreeMap<PathBuf, Cached>>> = OnceLock::new();

/// Match the containing code bundle only for its declared main executable.
fn code_root(path: &Path, guard: Option<&super::own_data::OwnData>) -> Result<PathBuf, AppError> {
    let Some(macos) = path
        .parent()
        .filter(|p| p.file_name().is_some_and(|s| s == "MacOS"))
    else {
        return Ok(path.to_owned());
    };
    let Some(contents) = macos
        .parent()
        .filter(|p| p.file_name().is_some_and(|s| s == "Contents"))
    else {
        return Ok(path.to_owned());
    };
    if let Some(guard) = guard {
        guard.check(&contents.join("Info.plist"))?;
    }
    let info = files::read_guarded(&contents.join("Info.plist"), 4 * 1024 * 1024, guard)
        .ok()
        .and_then(|b| plist::from_bytes::<plist::Value>(&b).ok());
    if info
        .as_ref()
        .and_then(|v| v.as_dictionary()?.get("CFBundleExecutable")?.as_string())
        .is_some_and(|name| path.file_name().is_some_and(|s| s == name))
    {
        return Ok(contents.parent().unwrap_or(path).to_owned());
    }
    Ok(path.to_owned())
}
fn fingerprint(
    path: &Path,
    guard: Option<&super::own_data::OwnData>,
) -> Result<[u8; 32], AppError> {
    files::safe_metadata(path)?;
    let mut digest = Sha256::new();
    let mut stack = vec![path.to_owned()];
    let mut count = 0;
    while let Some(p) = stack.pop() {
        count += 1;
        if count > 200_000 {
            return Err(AppError::new("E_PROTECTED", "signature_bundle_limit"));
        }
        let m = std::fs::symlink_metadata(&p)?;
        if !m.is_symlink()
            && let Some(guard) = guard
        {
            guard.check(&p)?;
        }
        if files::dataless(&m) {
            return Err(AppError::new("E_PROTECTED", "security_dataless"));
        }
        digest.update(p.as_os_str().as_encoded_bytes());
        for v in [
            m.dev(),
            m.ino(),
            m.len(),
            m.mode() as u64,
            m.uid() as u64,
            m.gid() as u64,
            m.mtime() as u64,
            m.mtime_nsec() as u64,
            m.ctime() as u64,
            m.ctime_nsec() as u64,
        ] {
            digest.update(v.to_le_bytes());
        }
        if m.is_symlink() {
            let target = p.canonicalize()?;
            if !target.starts_with(path) {
                return Err(AppError::new("E_PROTECTED", "signature_external_link"));
            }
            if let Some(guard) = guard {
                guard.check(&target)?;
            }
            if files::dataless(&std::fs::symlink_metadata(&target)?) {
                return Err(AppError::new("E_PROTECTED", "security_dataless"));
            }
            digest.update(target.as_os_str().as_encoded_bytes());
        } else if m.is_dir() {
            let mut entries = std::fs::read_dir(&p)?
                .map(|e| e.map(|e| e.path()))
                .collect::<Result<Vec<_>, _>>()?;
            entries.sort();
            stack.extend(entries);
        }
    }
    Ok(digest.finalize().into())
}
fn carries_signature(
    path: &Path,
    guard: Option<&super::own_data::OwnData>,
) -> Result<bool, AppError> {
    let meta = files::safe_metadata(path)?;
    if meta.is_dir() {
        return Ok(path.join("Contents/Info.plist").is_file()
            || path.extension().is_some_and(|e| {
                ["app", "framework", "bundle", "kext", "appex", "xpc"]
                    .iter()
                    .any(|x| e.eq_ignore_ascii_case(x))
            }));
    }
    if !meta.is_file() {
        return Err(AppError::new("E_PROTECTED", "signature_file_type"));
    }
    let mut file = files::open_read_guarded(path, u64::MAX, guard)?;
    let mut magic = [0; 4];
    let n = file.read(&mut magic)?;
    let macho = n == 4
        && matches!(
            u32::from_be_bytes(magic),
            0xfeedface
                | 0xcefaedfe
                | 0xfeedfacf
                | 0xcffaedfe
                | 0xcafebabe
                | 0xbebafeca
                | 0xcafebabf
                | 0xbfbafeca
        );
    Ok(macho
        || &magic == b"xar!"
        || path
            .extension()
            .is_some_and(|e| e.eq_ignore_ascii_case("dmg") || e.eq_ignore_ascii_case("pkg")))
}
pub fn inspect(path: &Path) -> Result<SignatureInfo, AppError> {
    inspect_guarded(path, None)
}
pub(super) fn inspect_guarded(
    path: &Path,
    guard: Option<&super::own_data::OwnData>,
) -> Result<SignatureInfo, AppError> {
    inspect_recent(path, guard, Duration::from_secs(300))
}
/// As `inspect_guarded`, reusing a verdict on the unchanged file for up to `max_age`.
pub(super) fn inspect_recent(
    path: &Path,
    guard: Option<&super::own_data::OwnData>,
    max_age: Duration,
) -> Result<SignatureInfo, AppError> {
    match inspect_inner(path, guard, max_age) {
        Ok(info) => Ok(info),
        Err(error) => {
            log::warn!(
                "Signature unknown for {}: {} {} {:?}",
                path.display(),
                error.code,
                error.message,
                error.detail
            );
            Ok(empty(SignatureStatus::Unknown))
        }
    }
}
fn inspect_inner(
    path: &Path,
    guard: Option<&super::own_data::OwnData>,
    max_age: Duration,
) -> Result<SignatureInfo, AppError> {
    if let Some(guard) = guard {
        guard.check(path)?;
    }
    files::safe_metadata(path)?;
    let path = code_root(path, guard)?;
    if !carries_signature(&path, guard)? {
        return Ok(empty(SignatureStatus::NotCode));
    }
    let before = fingerprint(&path, guard)?;
    let cache = CACHE.get_or_init(Default::default);
    if let Some(entry) = cache.lock().map_err(super::poison)?.get(&path)
        && entry.fingerprint == before
        && entry.at.elapsed() < max_age
    {
        return Ok(entry.info.clone());
    }
    // Security.framework opens bundle members itself. An exec child owns no
    // SQLite locks, including if a member is replaced after the preflight walk.
    // A bundle too large to check in time (Xcode) is too large every time: remember
    // that for the unchanged bundle too, instead of waiting out the timeout again.
    let (info, timed_out) = match inspect_isolated(&path) {
        Err(error) if error.message == "command_timeout" => (empty(SignatureStatus::Unknown), true),
        result => (result?, false),
    };
    if before != fingerprint(&path, guard)? {
        return Err(AppError::new(
            "E_PROTECTED",
            "signature_changed_during_check",
        ));
    }
    if info.status != SignatureStatus::Unknown || timed_out {
        let mut cache = cache.lock().map_err(super::poison)?;
        if cache.len() >= 4096 {
            cache.clear();
        }
        cache.insert(
            path,
            Cached {
                fingerprint: before,
                at: Instant::now(),
                info: info.clone(),
            },
        );
    }
    Ok(info)
}

/// Fixed read-only helper mode; entered before Tauri or any database initialization.
pub fn helper_entry() -> Option<i32> {
    let mut args = std::env::args_os().skip(1);
    if args.next().as_deref() != Some(std::ffi::OsStr::new("--opennavo-signature-inspect")) {
        return None;
    }
    let result = (|| {
        let path = args
            .next()
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "signature_path"))?;
        if args.next().is_some() {
            return Err(AppError::new("E_INVALID_ARG", "signature_args"));
        }
        let info = inspect_uncached(Path::new(&path))?;
        println!("{}", serde_json::to_string(&info)?);
        Ok::<_, AppError>(())
    })();
    Some(if result.is_ok() { 0 } else { 1 })
}

#[cfg(not(test))]
fn inspect_isolated(path: &Path) -> Result<SignatureInfo, AppError> {
    let exe = std::env::current_exe()?;
    let output = super::command::output(
        exe.to_str()
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "signature_executable"))?,
        &[
            "--opennavo-signature-inspect",
            path.to_str()
                .ok_or_else(|| AppError::new("E_INVALID_ARG", "signature_path"))?,
        ],
    )?;
    if output.code != Some(0) {
        return Err(AppError::new("E_UNKNOWN", "signature_helper"));
    }
    Ok(serde_json::from_str(&output.stdout)?)
}

#[cfg(test)]
fn inspect_isolated(path: &Path) -> Result<SignatureInfo, AppError> {
    // The test harness also has an exec-only worker: no Service/DB is opened.
    let output = std::process::Command::new(std::env::current_exe()?)
        .args([
            "--exact",
            "security::signature::tests::native_worker",
            "--nocapture",
        ])
        .env("OPENNAVO_TEST_SIGNATURE_PATH", path)
        .output()?;
    let text = String::from_utf8_lossy(&output.stdout);
    let json = text
        .lines()
        .find_map(|line| line.strip_prefix("SIGNATURE_RESULT:"))
        .ok_or_else(|| AppError::new("E_UNKNOWN", "signature_worker"))?;
    Ok(serde_json::from_str(json)?)
}

fn inspect_uncached(path: &Path) -> Result<SignatureInfo, AppError> {
    #[cfg(target_os = "macos")]
    {
        if path.is_file()
            && path
                .extension()
                .is_some_and(|e| e.eq_ignore_ascii_case("pkg"))
        {
            use super::Commands;
            let output = super::NativeCommands.output(
                "/usr/sbin/pkgutil",
                &["--check-signature", &path.to_string_lossy()],
            )?;
            let info = package_signature(Some(&output.text()));
            if info.status == SignatureStatus::Unknown {
                log::warn!(
                    "Package signature unknown for {}: exit {:?}: {}",
                    path.display(),
                    output.code,
                    output.text()
                );
            }
            return Ok(info);
        }
        native::inspect(path)
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = path;
        Ok(empty(SignatureStatus::Unknown))
    }
}

#[cfg(any(target_os = "macos", test))]
fn package_signature(output: Option<&str>) -> SignatureInfo {
    let output = output.unwrap_or_default();
    let status = output
        .lines()
        .filter_map(|l| l.trim().strip_prefix("Status: "))
        .next_back()
        .unwrap_or_default();
    let trusted = status == "signed by a developer certificate issued by Apple for distribution"
        || status == "signed by a certificate trusted by macOS"
        || status == "signed by a certificate trusted by Mac OS X";
    let signer = output
        .lines()
        .find_map(|l| l.trim().strip_prefix("1. Developer ID Installer: "));
    let team_id = signer
        .and_then(|s| s.rsplit_once(" (")?.1.strip_suffix(')'))
        .filter(|s| {
            s.len() == 10
                && s.bytes()
                    .all(|c| c.is_ascii_uppercase() || c.is_ascii_digit())
        })
        .map(str::to_owned);
    let notarized = output
        .lines()
        .find_map(|l| l.trim().strip_prefix("Notarization: "))
        .map(|v| v == "trusted by the Apple notary service");
    SignatureInfo {
        status: if trusted && team_id.is_some() {
            SignatureStatus::DeveloperId
        } else if status == "no signature" {
            SignatureStatus::Unsigned
        } else if status.to_ascii_lowercase().contains("revoked")
            || status.to_ascii_lowercase().contains("invalid")
        {
            SignatureStatus::Invalid
        } else {
            SignatureStatus::Unknown
        },
        team_id,
        signer: signer.map(str::to_owned),
        notarized,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn native_worker() {
        if let Some(path) = std::env::var_os("OPENNAVO_TEST_SIGNATURE_PATH") {
            let info = inspect_uncached(Path::new(&path)).unwrap();
            println!("SIGNATURE_RESULT:{}", serde_json::to_string(&info).unwrap());
        }
    }
    #[test]
    fn plain_files_are_not_code_and_missing_files_are_unknown() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        for name in [".zshrc", "job.sh", "download.zip", "rules.plist"] {
            let path = root.join(name);
            std::fs::write(&path, b"plain fixture").unwrap();
            assert_eq!(inspect(&path).unwrap(), empty(SignatureStatus::NotCode));
        }
        assert_eq!(
            inspect(&root.join("missing")).unwrap().status,
            SignatureStatus::Unknown
        );
    }
    #[test]
    #[cfg(target_os = "macos")]
    fn nested_modification_differs_from_invalid_main_and_invalidates_cache() {
        use std::process::Command;
        fn bundle(path: &Path, id: &str) {
            std::fs::create_dir_all(path.join("Contents/MacOS")).unwrap();
            std::fs::create_dir_all(path.join("Contents/Resources")).unwrap();
            std::fs::copy("/usr/bin/true", path.join("Contents/MacOS/main")).unwrap();
            std::fs::write(path.join("Contents/Info.plist"), format!("<plist version=\"1.0\"><dict><key>CFBundleIdentifier</key><string>{id}</string><key>CFBundleExecutable</key><string>main</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>")).unwrap();
        }
        fn sign(path: &Path) {
            let output = Command::new("/usr/bin/codesign")
                .args(["--force", "--sign", "-"])
                .arg(path)
                .output()
                .unwrap();
            assert!(
                output.status.success(),
                "{}",
                String::from_utf8_lossy(&output.stderr)
            );
        }
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap().join("Fixture.app");
        let nested = root.join("Contents/Helpers/Nested.app");
        bundle(&root, "test.opennavo.signature.root");
        bundle(&nested, "test.opennavo.signature.nested");
        let resource = nested.join("Contents/Resources/fixture.txt");
        std::fs::write(&resource, "original").unwrap();
        sign(&nested);
        sign(&root);
        assert_eq!(inspect(&root).unwrap().status, SignatureStatus::Adhoc);
        assert_eq!(
            inspect(&root.join("Contents/MacOS/main")).unwrap().status,
            SignatureStatus::Adhoc
        );
        std::fs::write(&resource, "modified").unwrap();
        assert_eq!(inspect(&root).unwrap().status, SignatureStatus::Modified);
        // A main resource failure is strict-invalid, regardless of a prior cached result.
        let plist_path = root.join("Contents/Info.plist");
        let plist = std::fs::read_to_string(&plist_path).unwrap();
        std::fs::write(
            plist_path,
            plist.replace(
                "test.opennavo.signature.root",
                "test.opennavo.signature.changed",
            ),
        )
        .unwrap();
        assert_eq!(inspect(&root).unwrap().status, SignatureStatus::Invalid);
    }
    #[test]
    fn package_chain_and_notary_result_are_distinct() {
        let info = package_signature(Some(
            "   Status: signed by a developer certificate issued by Apple for distribution\n   Notarization: trusted by the Apple notary service\n    1. Developer ID Installer: Fixture (ABCDEFGHIJ)",
        ));
        assert_eq!(info.status, SignatureStatus::DeveloperId);
        assert_eq!(info.team_id.as_deref(), Some("ABCDEFGHIJ"));
        assert_eq!(info.notarized, Some(true));
        assert_eq!(
            package_signature(Some("Status: no signature")).status,
            SignatureStatus::Unsigned
        );
        assert_eq!(package_signature(None).status, SignatureStatus::Unknown);
        assert_eq!(
            package_signature(Some(
                "Status: revoked signature\n1. Developer ID Installer: Fixture (ABCDEFGHIJ)"
            ))
            .status,
            SignatureStatus::Invalid
        );
    }
}
#[cfg(target_os = "macos")]
mod native {
    use super::*;
    use objc2_foundation::NSString;
    use std::{ffi::c_void, os::unix::ffi::OsStrExt};
    type Ref = *const c_void;
    #[link(name = "CoreFoundation", kind = "framework")]
    unsafe extern "C" {
        fn CFRelease(value: Ref);
        fn CFURLCreateFromFileSystemRepresentation(a: Ref, b: *const u8, n: isize, d: u8) -> Ref;
        fn CFErrorCopyDescription(error: Ref) -> Ref;
        fn CFErrorCopyUserInfo(error: Ref) -> Ref;
        fn CFCopyDescription(value: Ref) -> Ref;
    }
    #[link(name = "Security", kind = "framework")]
    unsafe extern "C" {
        fn SecStaticCodeCreateWithPath(path: Ref, flags: u32, out: *mut Ref) -> i32;
        fn SecStaticCodeCheckValidity(code: Ref, flags: u32, requirement: Ref) -> i32;
        fn SecStaticCodeCheckValidityWithErrors(
            code: Ref,
            flags: u32,
            requirement: Ref,
            error: *mut Ref,
        ) -> i32;
        fn SecRequirementCreateWithString(text: Ref, flags: u32, out: *mut Ref) -> i32;
    }
    struct Owned(Ref);
    impl Drop for Owned {
        fn drop(&mut self) {
            unsafe { CFRelease(self.0) }
        }
    }
    fn owned(value: Ref) -> Result<Owned, AppError> {
        if value.is_null() {
            Err(AppError::new("E_UNKNOWN", "signature_native"))
        } else {
            Ok(Owned(value))
        }
    }
    fn description(value: Ref) -> String {
        if value.is_null() {
            return String::new();
        }
        unsafe { (&*(value as *const NSString)).to_string() }
    }
    // API, I/O and policy database failures are inconclusive, not tampering.
    fn inconclusive(code: i32) -> bool {
        !matches!(code, -67062 | -67061 | -67058..=-67053 | -67050 | -67049 | -67045 | -67034 | -67030..=-67028 | -67023..=-67021 | -67016..=-67007 | -67005..=-67001 | -66999..=-66992 | -67818 | -67819 | -67820 | -67843)
    }
    fn validate(code: Ref, flags: u32, path: &Path, stage: &str) -> i32 {
        let mut error = std::ptr::null();
        let status = unsafe {
            SecStaticCodeCheckValidityWithErrors(code, flags, std::ptr::null(), &mut error)
        };
        if status != 0 {
            let detail = if let Ok(error) = owned(error) {
                let message = owned(unsafe { CFErrorCopyDescription(error.0) })
                    .map(|s| description(s.0))
                    .unwrap_or_default();
                // Includes the first failing nested path/resource, without a new IPC field.
                let info = owned(unsafe { CFErrorCopyUserInfo(error.0) }).ok();
                let fields = info
                    .and_then(|i| owned(unsafe { CFCopyDescription(i.0) }).ok())
                    .map(|s| description(s.0))
                    .unwrap_or_default();
                format!("{message}: {fields}")
            } else {
                String::new()
            };
            log::warn!(
                "Signature {stage} failed for {}: OSStatus={status} {detail}",
                path.display()
            );
        } else if let Ok(error) = owned(error) {
            drop(error);
        }
        status
    }
    pub fn inspect(path: &Path) -> Result<SignatureInfo, AppError> {
        objc2::rc::autoreleasepool(|_| {
            let bytes = path.as_os_str().as_bytes();
            let url = owned(unsafe {
                CFURLCreateFromFileSystemRepresentation(
                    std::ptr::null(),
                    bytes.as_ptr(),
                    bytes.len() as isize,
                    u8::from(path.is_dir()),
                )
            })?;
            let mut ptr = std::ptr::null();
            let result = unsafe { SecStaticCodeCreateWithPath(url.0, 0, &mut ptr) };
            if result != 0 {
                if let Ok(code) = owned(ptr) {
                    drop(code);
                }
                log::warn!(
                    "Signature creation failed for {}: OSStatus={result}",
                    path.display()
                );
                return Ok(empty(if result == -67062 {
                    SignatureStatus::Unsigned
                } else {
                    SignatureStatus::Unknown
                }));
            }
            let code = owned(ptr)?;
            // Strict, all architectures, explicitly offline. Check the main code
            // before recursively inspecting nested code, so severity stays distinct.
            const STRICT: u32 = (1 << 0) | (1 << 4) | (1 << 29);
            let validity = validate(code.0, STRICT, path, "strict");
            let metadata = crate::uninstall::identity::native::signing(path);
            let team_id = metadata.as_ref().and_then(|s| s.team_id.clone());
            let signer = metadata.and_then(|s| s.vendor);
            if validity != 0 {
                return Ok(SignatureInfo {
                    status: if validity == -67062 {
                        SignatureStatus::Unsigned
                    } else if inconclusive(validity) {
                        SignatureStatus::Unknown
                    } else {
                        SignatureStatus::Invalid
                    },
                    team_id,
                    signer,
                    notarized: None,
                });
            }
            let deep = validate(code.0, STRICT | (1 << 3), path, "deep");
            let requirement = |text: &str| -> bool {
                let text = NSString::from_str(text);
                let mut r = std::ptr::null();
                if unsafe { SecRequirementCreateWithString(&*text as *const _ as Ref, 0, &mut r) }
                    != 0
                {
                    return false;
                }
                let Ok(r) = owned(r) else {
                    return false;
                };
                // Content has already been checked. Identity predicates must not
                // repeat full resource/deep validation for every certificate type.
                unsafe {
                    SecStaticCodeCheckValidity(code.0, (1 << 1) | (1 << 2) | (1 << 29), r.0) == 0
                }
            };
            let identity = if requirement("anchor apple") {
                SignatureStatus::Apple
            } else if requirement(
                "anchor apple generic and certificate leaf[field.1.2.840.113635.100.6.1.9] exists",
            ) {
                SignatureStatus::AppStore
            } else if requirement(
                "anchor apple generic and certificate leaf[field.1.2.840.113635.100.6.1.13] exists",
            ) {
                SignatureStatus::DeveloperId
            } else {
                SignatureStatus::Adhoc
            };
            let status = if deep == 0 {
                identity
            } else if inconclusive(deep) {
                SignatureStatus::Unknown
            } else {
                SignatureStatus::Modified
            };
            let notarized = matches!(
                identity,
                SignatureStatus::Apple | SignatureStatus::AppStore | SignatureStatus::DeveloperId
            )
            .then(|| requirement("notarized"));
            Ok(SignatureInfo {
                status,
                team_id,
                signer,
                notarized,
            })
        })
    }
}
