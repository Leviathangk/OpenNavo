use crate::{AppError, model::LocalAppArch};
use std::{
    collections::BTreeSet,
    io::Read,
    path::{Path, PathBuf},
    time::Duration,
};

#[derive(Debug, Clone, Default)]
pub struct Identity {
    pub name: Option<String>,
    pub bundle_id: Option<String>,
    pub version: Option<String>,
    pub team_id: Option<String>,
    pub vendor: Option<String>,
    pub arch: Option<LocalAppArch>,
    pub groups: BTreeSet<String>,
    pub component_ids: BTreeSet<String>,
    pub executables: BTreeSet<String>,
    pub last_used: Option<i64>,
    /// Spotlight's "Date Added": when the app arrived in its folder (a copied bundle keeps its build-time birth date).
    pub date_added: Option<i64>,
    pub managed: bool,
}
#[derive(Debug, Clone, Default)]
pub struct Receipt {
    pub id: String,
    pub files: Vec<PathBuf>,
}
/// Tests inject a probe; no test relies on signatures, receipts, Spotlight or the real launchd database.
pub trait MetadataProbe: Send + Sync {
    fn identity(&self, path: &Path) -> Identity;
    fn receipts(&self, path: &Path) -> Vec<Receipt>;
    fn loaded(&self, _label: &str, _system: bool) -> bool {
        false
    }
    /// Identity and receipts depend only on the bundle's files, so the engine may reuse them until the bundle changes;
    /// Spotlight dates are re-read through `dates`. Test probes answer from mutable fixtures and are never cached.
    fn cacheable(&self) -> bool {
        false
    }
    /// Spotlight's last-used and date-added times in Unix milliseconds.
    fn dates(&self, _path: &Path) -> (Option<i64>, Option<i64>) {
        (None, None)
    }
}
pub struct NativeProbe;
impl MetadataProbe for NativeProbe {
    fn identity(&self, path: &Path) -> Identity {
        let mut result = bundle(path);
        #[cfg(target_os = "macos")]
        {
            // Read in process: spawning codesign and mdls for every bundle and nested component cost about 700
            // processes and 10 s per enumeration on a Mac with 100 apps.
            if let Some(signing) = native::signing(path) {
                result.team_id = signing.team_id;
                result.vendor = signing.vendor;
                result.groups = signing.groups;
            }
            (result.last_used, result.date_added) = native::spotlight_dates(path);
            // Installer management marker; unknown management state never authorizes bypassing filesystem/TCC checks.
            result.managed = path.join("Contents/Resources/ManagedApp.plist").is_file();
        }
        for relative in [
            "Contents/Library/LoginItems",
            "Contents/XPCServices",
            "Contents/PlugIns",
            "Contents/Helpers",
            "Contents/Library/LaunchServices",
        ] {
            if let Ok(entries) = std::fs::read_dir(path.join(relative)) {
                for entry in entries.flatten() {
                    if entry
                        .file_type()
                        .is_ok_and(|t| t.is_dir() && !t.is_symlink())
                    {
                        let nested = bundle(&entry.path());
                        if let Some(id) = nested.bundle_id {
                            result.component_ids.insert(id);
                        }
                        result.executables.extend(nested.executables);
                        #[cfg(target_os = "macos")]
                        if let Some(signing) = native::signing(&entry.path()) {
                            result.groups.extend(signing.groups);
                        }
                    }
                }
            }
        }
        result
    }
    fn cacheable(&self) -> bool {
        true
    }
    fn dates(&self, path: &Path) -> (Option<i64>, Option<i64>) {
        #[cfg(target_os = "macos")]
        {
            native::spotlight_dates(path)
        }
        #[cfg(not(target_os = "macos"))]
        {
            let _ = path;
            (None, None)
        }
    }
    fn receipts(&self, path: &Path) -> Vec<Receipt> {
        #[cfg(target_os = "macos")]
        {
            let target = path.join("Contents/Info.plist");
            let Ok(output) = command(
                "/usr/sbin/pkgutil",
                &["--file-info", &target.to_string_lossy()],
                Duration::from_secs(3),
            ) else {
                return vec![];
            };
            output
                .stdout
                .lines()
                .filter_map(|line| line.trim().strip_prefix("pkgid: "))
                .filter(|id| valid_identifier(id))
                .filter_map(|id| {
                    let info = command(
                        "/usr/sbin/pkgutil",
                        &["--pkg-info-plist", id],
                        Duration::from_secs(3),
                    )
                    .ok()?;
                    let dict = plist::Value::from_reader_xml(info.stdout.as_bytes())
                        .ok()?
                        .into_dictionary()?;
                    let volume = dict
                        .get("volume")
                        .and_then(plist::Value::as_string)
                        .unwrap_or("/");
                    let location = dict
                        .get("install-location")
                        .and_then(plist::Value::as_string)
                        .unwrap_or("");
                    let base = Path::new(volume).join(location.trim_start_matches('/'));
                    let output = command(
                        "/usr/sbin/pkgutil",
                        &["--files", id],
                        Duration::from_secs(3),
                    )
                    .ok()?;
                    let files = receipt_files(&base, &output.stdout);
                    if !files.iter().any(|p| p.starts_with(path)) {
                        return None;
                    }
                    Some(Receipt {
                        id: id.to_owned(),
                        files,
                    })
                })
                .collect()
        }
        #[cfg(not(target_os = "macos"))]
        {
            let _ = path;
            vec![]
        }
    }
    fn loaded(&self, label: &str, system: bool) -> bool {
        #[cfg(target_os = "macos")]
        {
            if !valid_identifier(label) {
                return false;
            }
            let domain = if system {
                "system".into()
            } else {
                user_domain()
            };
            command(
                "/bin/launchctl",
                &["print", &format!("{domain}/{label}")],
                Duration::from_secs(2),
            )
            .is_ok()
        }
        #[cfg(not(target_os = "macos"))]
        {
            let _ = (label, system);
            false
        }
    }
}
fn receipt_files(base: &Path, output: &str) -> Vec<PathBuf> {
    output
        .lines()
        .filter(|line| !line.trim().is_empty())
        .filter(|line| {
            !Path::new(line).components().any(|c| {
                matches!(
                    c,
                    std::path::Component::ParentDir | std::path::Component::RootDir
                )
            })
        })
        .map(|line| base.join(line))
        .filter(|path| path != base)
        .collect()
}

pub fn valid_identifier(id: &str) -> bool {
    !id.is_empty()
        && id.len() <= 256
        && id
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"._-".contains(&b))
        && !id.starts_with('-')
}
/// Framework defaults that unrelated apps keep when their developers never set an identifier (an unbranded Electron
/// app is `com.github.Electron`). Such an ID proves nothing about who owns a process or a file.
pub fn generic_bundle_id(id: &str) -> bool {
    [
        "com.github.electron",
        "com.github.electron.helper",
        "org.nwjs.nwjs",
        "com.tauri.dev",
    ]
    .contains(&id.to_ascii_lowercase().as_str())
}
pub fn user_domain() -> String {
    format!("gui/{}", unsafe { nix::libc::getuid() })
}
pub fn bundle(path: &Path) -> Identity {
    let mut result = Identity::default();
    if super::rules::parents_safe(&path.join("Contents/Info.plist"))
        && std::fs::symlink_metadata(path.join("Contents/Info.plist"))
            .is_ok_and(|m| m.is_file() && !m.is_symlink())
        && let Ok(dict) = crate::library::files::plist(path)
    {
        let value = |key: &str| {
            dict.get(key)
                .and_then(plist::Value::as_string)
                .filter(|v| !v.is_empty())
                .map(str::to_owned)
        };
        result.name = value("CFBundleDisplayName").or_else(|| value("CFBundleName"));
        #[cfg(target_os = "macos")]
        {
            use objc2_foundation::{NSBundle, NSString, NSURL};
            objc2::rc::autoreleasepool(|_| {
                let url = NSURL::fileURLWithPath(&NSString::from_str(&path.to_string_lossy()));
                if let Some(bundle) = NSBundle::bundleWithURL(&url)
                    && let Some(dict) = bundle.localizedInfoDictionary()
                {
                    for key in ["CFBundleDisplayName", "CFBundleName"] {
                        if let Some(value) = dict.objectForKey(&NSString::from_str(key))
                            && let Some(name) = value.downcast_ref::<NSString>()
                            && !name.is_empty()
                        {
                            result.name = Some(name.to_string());
                            break;
                        }
                    }
                }
            });
        }
        result.bundle_id = value("CFBundleIdentifier");
        result.version = value("CFBundleShortVersionString").or_else(|| value("CFBundleVersion"));
        if let Some(executable) = value("CFBundleExecutable") {
            result.executables.insert(executable.clone());
            result.arch = macho_arch(&path.join("Contents/MacOS").join(executable));
        }
    }
    result
}
fn macho_arch(path: &Path) -> Option<LocalAppArch> {
    let mut header = [0u8; 12];
    std::fs::File::open(path)
        .ok()?
        .read_exact(&mut header)
        .ok()?;
    if matches!(
        &header[..4],
        [0xca, 0xfe, 0xba, 0xbe]
            | [0xca, 0xfe, 0xba, 0xbf]
            | [0xbe, 0xba, 0xfe, 0xca]
            | [0xbf, 0xba, 0xfe, 0xca]
    ) {
        return Some(LocalAppArch::Universal);
    }
    let cpu = match &header[..4] {
        [0xcf, 0xfa, 0xed, 0xfe] | [0xce, 0xfa, 0xed, 0xfe] => {
            u32::from_le_bytes(header[4..8].try_into().ok()?)
        }
        [0xfe, 0xed, 0xfa, 0xcf] | [0xfe, 0xed, 0xfa, 0xce] => {
            u32::from_be_bytes(header[4..8].try_into().ok()?)
        }
        _ => return None,
    };
    match cpu {
        0x0100000c => Some(LocalAppArch::Arm64),
        0x01000007 => Some(LocalAppArch::X86_64),
        _ => None,
    }
}
/// Fixed program and argument arrays, bounded output and execution time. Never invoke a shell.
pub fn command(
    program: &str,
    args: &[&str],
    timeout: Duration,
) -> Result<crate::native_command::Output, AppError> {
    crate::native_command::output(program, args, timeout)?.success()
}

/// App Store metadata only; never opens an app or asks Spotlight to reindex it.
pub fn spotlight_store_metadata(path: &Path) -> (Option<u64>, Option<i64>) {
    #[cfg(target_os = "macos")]
    {
        native::spotlight_store_metadata(path)
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = path;
        (None, None)
    }
}

/// Code signing and Spotlight metadata read in process (Security / CoreServices); no tool is spawned.
#[cfg(target_os = "macos")]
pub(crate) mod native {
    use objc2_foundation::NSString;
    use std::{collections::BTreeSet, ffi::c_void, path::Path};

    type CFTypeRef = *const c_void;
    #[link(name = "CoreFoundation", kind = "framework")]
    unsafe extern "C" {
        fn CFRelease(cf: CFTypeRef);
        fn CFGetTypeID(cf: CFTypeRef) -> usize;
        fn CFStringGetTypeID() -> usize;
        fn CFArrayGetTypeID() -> usize;
        fn CFDictionaryGetTypeID() -> usize;
        fn CFDateGetTypeID() -> usize;
        fn CFNumberGetTypeID() -> usize;
        fn CFNumberGetValue(number: CFTypeRef, kind: i32, value: *mut c_void) -> bool;
        fn CFArrayGetCount(array: CFTypeRef) -> isize;
        fn CFArrayGetValueAtIndex(array: CFTypeRef, index: isize) -> CFTypeRef;
        fn CFDictionaryGetValue(dictionary: CFTypeRef, key: CFTypeRef) -> CFTypeRef;
        fn CFDateGetAbsoluteTime(date: CFTypeRef) -> f64;
        fn CFURLCreateFromFileSystemRepresentation(
            allocator: CFTypeRef,
            buffer: *const u8,
            length: isize,
            is_directory: u8,
        ) -> CFTypeRef;
    }
    #[link(name = "Security", kind = "framework")]
    unsafe extern "C" {
        fn SecStaticCodeCreateWithPath(path: CFTypeRef, flags: u32, code: *mut CFTypeRef) -> i32;
        fn SecCodeCopySigningInformation(
            code: CFTypeRef,
            flags: u32,
            information: *mut CFTypeRef,
        ) -> i32;
        fn SecCertificateCopySubjectSummary(certificate: CFTypeRef) -> CFTypeRef;
        static kSecCodeInfoTeamIdentifier: CFTypeRef;
        static kSecCodeInfoCertificates: CFTypeRef;
        static kSecCodeInfoEntitlementsDict: CFTypeRef;
    }
    #[link(name = "CoreServices", kind = "framework")]
    unsafe extern "C" {
        fn MDItemCreate(allocator: CFTypeRef, path: CFTypeRef) -> CFTypeRef;
        fn MDItemCopyAttribute(item: CFTypeRef, name: CFTypeRef) -> CFTypeRef;
    }
    /// kSecCSSigningInformation | kSecCSRequirementInformation: certificates, team and entitlements, no validation.
    const SIGNING_FLAGS: u32 = (1 << 1) | (1 << 2);
    /// Seconds between the Unix epoch and CFAbsoluteTime's reference date (2001-01-01).
    const CF_EPOCH: f64 = 978_307_200.0;

    /// A +1 CoreFoundation reference, released on drop.
    struct Owned(CFTypeRef);
    impl Owned {
        fn new(value: CFTypeRef) -> Option<Self> {
            // Never build `Self(null)`: dropping it would call CFRelease(NULL), which traps.
            if value.is_null() {
                None
            } else {
                Some(Self(value))
            }
        }
    }
    impl Drop for Owned {
        fn drop(&mut self) {
            unsafe { CFRelease(self.0) }
        }
    }
    fn is(value: CFTypeRef, type_id: usize) -> bool {
        !value.is_null() && unsafe { CFGetTypeID(value) } == type_id
    }
    fn string(value: CFTypeRef) -> Option<String> {
        // CFString is toll-free bridged with NSString.
        is(value, unsafe { CFStringGetTypeID() })
            .then(|| unsafe { &*(value as *const NSString) }.to_string())
    }
    fn cf(text: &NSString) -> CFTypeRef {
        text as *const NSString as CFTypeRef
    }

    pub struct Signing {
        pub team_id: Option<String>,
        pub vendor: Option<String>,
        pub groups: BTreeSet<String>,
    }

    pub fn signing(path: &Path) -> Option<Signing> {
        use std::os::unix::ffi::OsStrExt;
        objc2::rc::autoreleasepool(|_| {
            let bytes = path.as_os_str().as_bytes();
            let url = Owned::new(unsafe {
                CFURLCreateFromFileSystemRepresentation(
                    std::ptr::null(),
                    bytes.as_ptr(),
                    isize::try_from(bytes.len()).ok()?,
                    1,
                )
            })?;
            let mut code: CFTypeRef = std::ptr::null();
            let status = unsafe { SecStaticCodeCreateWithPath(url.0, 0, &mut code) };
            let code = Owned::new(code)?;
            if status != 0 {
                return None;
            }
            let mut info: CFTypeRef = std::ptr::null();
            let status = unsafe { SecCodeCopySigningInformation(code.0, SIGNING_FLAGS, &mut info) };
            let info = Owned::new(info)?;
            if status != 0 || !is(info.0, unsafe { CFDictionaryGetTypeID() }) {
                return None;
            }
            let value = |key: CFTypeRef| unsafe { CFDictionaryGetValue(info.0, key) };
            let team_id = string(value(unsafe { kSecCodeInfoTeamIdentifier }))
                .filter(|team| !team.is_empty());
            let certificates = value(unsafe { kSecCodeInfoCertificates });
            let vendor = (is(certificates, unsafe { CFArrayGetTypeID() })
                && unsafe { CFArrayGetCount(certificates) } > 0)
                .then(|| {
                    let leaf = unsafe { CFArrayGetValueAtIndex(certificates, 0) };
                    Owned::new(unsafe { SecCertificateCopySubjectSummary(leaf) })
                })
                .flatten()
                .and_then(|summary| string(summary.0))
                .and_then(|summary| {
                    summary
                        .strip_prefix("Developer ID Application: ")
                        .or_else(|| summary.strip_prefix("Apple Distribution: "))
                        .map(str::to_owned)
                });
            let mut groups = BTreeSet::new();
            let entitlements = value(unsafe { kSecCodeInfoEntitlementsDict });
            if is(entitlements, unsafe { CFDictionaryGetTypeID() }) {
                let key = NSString::from_str("com.apple.security.application-groups");
                let list = unsafe { CFDictionaryGetValue(entitlements, cf(&key)) };
                if is(list, unsafe { CFArrayGetTypeID() }) {
                    for index in 0..unsafe { CFArrayGetCount(list) } {
                        if let Some(group) = string(unsafe { CFArrayGetValueAtIndex(list, index) })
                        {
                            groups.insert(group);
                        }
                    }
                }
            }
            Some(Signing {
                team_id,
                vendor,
                groups,
            })
        })
    }

    pub fn spotlight_store_metadata(path: &Path) -> (Option<u64>, Option<i64>) {
        let id = objc2::rc::autoreleasepool(|_| {
            let path = NSString::from_str(&path.to_string_lossy());
            let item = Owned::new(unsafe { MDItemCreate(std::ptr::null(), cf(&path)) })?;
            let name = NSString::from_str("kMDItemAppStoreAdamID");
            let value = Owned::new(unsafe { MDItemCopyAttribute(item.0, cf(&name)) })?;
            if is(value.0, unsafe { CFNumberGetTypeID() }) {
                let mut number = 0_i64;
                if unsafe { CFNumberGetValue(value.0, 4, (&mut number as *mut i64).cast()) } {
                    return u64::try_from(number).ok();
                }
            }
            string(value.0)?.parse().ok()
        });
        (id, spotlight_dates(path).1)
    }

    /// Spotlight's last-used and date-added times in Unix milliseconds; None when the item is not indexed.
    pub fn spotlight_dates(path: &Path) -> (Option<i64>, Option<i64>) {
        objc2::rc::autoreleasepool(|_| {
            let text = NSString::from_str(&path.to_string_lossy());
            let Some(item) = Owned::new(unsafe { MDItemCreate(std::ptr::null(), cf(&text)) })
            else {
                return (None, None);
            };
            let date = |name: &str| {
                let name = NSString::from_str(name);
                let value = Owned::new(unsafe { MDItemCopyAttribute(item.0, cf(&name)) })?;
                if !is(value.0, unsafe { CFDateGetTypeID() }) {
                    return None;
                }
                let seconds = unsafe { CFDateGetAbsoluteTime(value.0) } + CF_EPOCH;
                Some((seconds * 1000.0) as i64)
            };
            (date("kMDItemLastUsedDate"), date("kMDItemDateAdded"))
        })
    }
}

#[cfg(test)]
mod command_output_tests {
    use super::*;
    #[test]
    fn receipt_files_exclude_blank_root_and_escaping_entries_without_trimming_names() {
        let base = Path::new("/Applications");
        assert_eq!(
            receipt_files(
                base,
                "\n.\n./\n/other\n../escape\nExample.app/Contents/Info.plist\n Name.app/file \n\n"
            ),
            vec![
                base.join("Example.app/Contents/Info.plist"),
                base.join(" Name.app/file ")
            ]
        );
    }
}
