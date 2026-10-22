//! Fixed actions carried by the existing admin_batch request/askpass transport.
//! Caller-controlled paths are constrained again in the privileged process.
use super::{gate, service, swap};
use crate::{
    AppError,
    appstore::install::{PrivilegedInstall, validate_staging},
    brew::env::Reason,
    uninstall::{
        identity::command,
        rules::{self, Snapshot},
    },
};
use serde::{Deserialize, Serialize};
use std::{
    fs,
    io::{Read, Write},
    path::{Path, PathBuf},
    time::Duration,
};

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "action", rename_all = "snake_case", deny_unknown_fields)]
pub enum Action {
    MasInstallPkg { request: Box<PrivilegedInstall> },
    AppSwap { request: Box<swap::SwapRequest> },
    PkgInstall { request: Box<swap::SwapRequest> },
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Request {
    pub id: String,
    pub action: Action,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ResultItem {
    pub id: String,
    pub error: Option<AppError>,
    pub backup: Option<PathBuf>,
}
impl Request {
    fn app_path(&self) -> &Path {
        match &self.action {
            Action::MasInstallPkg { request } => &request.app_path,
            Action::AppSwap { request } | Action::PkgInstall { request } => &request.installed,
        }
    }
}
/// What the password dialog says about a batch: putting an App Store app on the Mac is an install, replacing
/// an app that is already there (App Store or not) is an update.
/// The app is named only when the whole batch is about one.
pub fn password_reason(requests: &[Request]) -> (Reason, Option<String>) {
    let reason = if requests.iter().all(|r| {
        matches!(&r.action, Action::MasInstallPkg { request } if request.app_snapshot.is_none())
    }) {
        Reason::Install
    } else {
        Reason::Update
    };
    let mut names = requests
        .iter()
        .filter_map(|r| r.app_path().file_stem())
        .map(|name| name.to_string_lossy().into_owned())
        .collect::<Vec<_>>();
    names.sort();
    names.dedup();
    (reason, (names.len() == 1).then(|| names.remove(0)))
}
pub trait Actions: Send + Sync {
    fn run(&self, requests: &[Request]) -> Result<Vec<ResultItem>, AppError>;
}
fn protected(message: &str) -> AppError {
    AppError::new("E_PROTECTED", message)
}
fn app_destination(path: &Path, home: &Path) -> bool {
    path.extension().is_some_and(|e| e == "app")
        && path
            .parent()
            .is_some_and(|p| p == Path::new("/Applications") || p == home.join("Applications"))
        && rules::parents_safe(path)
}
pub fn validate(request: &Request, home: &Path, data: &Path, uid: u32) -> Result<(), AppError> {
    if request.id.is_empty() || request.id.len() > 128 {
        return Err(protected("native_task_id"));
    }
    // The helper passes the folder its batch sits in, two levels up: OpenNavo's
    // `logs` folder, which also holds the native staging root.
    let app_data = data
        .parent()
        .filter(|_| data.file_name().is_some_and(|name| name == "logs"));
    if app_data.and_then(Path::parent) != Some(home.join("Library/Application Support").as_path())
        || app_data
            .and_then(Path::file_name)
            .and_then(|v| v.to_str())
            .is_none_or(|v| {
                v != "com.opennavo.desktop" && !v.starts_with("com.opennavo.desktop.dev")
            })
    {
        return Err(protected("native_data_root"));
    }
    let staging = data.join(crate::queue::NATIVE_STAGING);
    match &request.action {
        Action::MasInstallPkg { request: r } => {
            if r.staging.parent() != Some(staging.as_path()) || !app_destination(&r.app_path, home)
            {
                return Err(protected("mas_install_scope"));
            }
            r.validate(uid)?;
        }
        Action::AppSwap { request: r } | Action::PkgInstall { request: r } => {
            if !app_destination(&r.installed, home)
                || !r.prepared.starts_with(&staging)
                || r.prepared
                    .components()
                    .any(|c| matches!(c, std::path::Component::ParentDir))
            {
                return Err(protected("app_swap_scope"));
            }
            let task_stage = r
                .prepared
                .strip_prefix(&staging)
                .ok()
                .and_then(|r| r.components().next())
                .map(|c| staging.join(c.as_os_str()))
                .ok_or_else(|| protected("app_swap_staging"))?;
            validate_staging(&task_stage, uid)?;
            if matches!(&request.action, Action::PkgInstall { .. }) != r.package.is_some() {
                return Err(protected("native_action_mismatch"));
            }
            if let Some(package) = &r.package
                && (!package.path.starts_with(&task_stage)
                    || !rules::parents_safe(&package.path)
                    || !package.snapshot.matches(&package.path)
                    || package.snapshot.kind != 0
                    || package.snapshot.length > 1024 * 1024 * 1024)
            {
                return Err(protected("pkg_install_scope"));
            }
            if !r.prepared_snapshot.matches(&r.prepared)
                || !r.installed_snapshot.still(&r.installed)
            {
                return Err(protected("app_swap_changed"));
            }
        }
    }
    Ok(())
}
/// pkgutil has already validated the cryptographic signature and trust chain by the time
/// its output reaches here. App Store downloads are re-signed by Apple's own Mac App Store
/// installer certificate: pkgutil reports them as "signed by Apple for the App Store", with
/// "Apple Mac OS Installer Package Signing" as the leaf under the Worldwide Developer
/// Relations CA. Requiring those names keeps third-party Developer ID installers, which
/// also chain to an Apple root, and Apple's operating-system packages out.
pub fn apple_package_signature(output: &str) -> bool {
    let lines: Vec<_> = output.lines().map(str::trim).collect();
    lines.iter().any(|l| {
        matches!(
            *l,
            "Status: signed by Apple for the App Store"
                | "Status: signed by a certificate trusted by macOS"
        )
    }) && lines.contains(&"1. Apple Mac OS Installer Package Signing")
        && lines.contains(&"2. Apple Worldwide Developer Relations Certification Authority")
        && lines.contains(&"3. Apple Root CA")
}
/// App Store packages may install one app only. Reject distribution packages,
/// scripts and payload paths outside the expected bundle even with an Apple
/// signature; a signed operating-system package is not an App Store download.
pub fn store_payload_scope(
    info: &[u8],
    payload: &str,
    app: &Path,
    bundle: &str,
) -> Result<PathBuf, AppError> {
    use quick_xml::{Reader, XmlVersion, events::Event};
    if info.len() > 1024 * 1024
        || payload.len() > 16 * 1024 * 1024
        || !crate::appstore::valid_bundle_id(bundle)
    {
        return Err(protected("store_package_metadata"));
    }
    let mut reader = Reader::from_reader(info);
    let mut location = None;
    let mut matched = false;
    loop {
        match reader
            .read_event()
            .map_err(|_| protected("store_package_metadata"))?
        {
            Event::Start(e) | Event::Empty(e) => {
                if e.name().as_ref() == "scripts" || e.name().as_ref() == "installer-gui-script" {
                    return Err(protected("store_package_scripts"));
                }
                let mut attributes = std::collections::BTreeMap::new();
                for attribute in e.attributes() {
                    let attribute = attribute.map_err(|_| protected("store_package_metadata"))?;
                    attributes.insert(
                        attribute.key.as_ref().to_owned(),
                        attribute
                            .normalized_value(XmlVersion::Implicit1_0)
                            .map_err(|_| protected("store_package_metadata"))?
                            .into_owned(),
                    );
                }
                if e.name().as_ref() == "pkg-info" {
                    if location.is_some() {
                        return Err(protected("store_package_components"));
                    }
                    location = attributes.get("install-location").cloned();
                }
                if e.name().as_ref() == "bundle"
                    && attributes.get("id").is_some_and(|id| id == bundle)
                {
                    matched = true;
                }
            }
            Event::DocType(_) => return Err(protected("store_package_metadata")),
            Event::Eof => break,
            _ => {}
        }
    }
    let location = location
        .map(PathBuf::from)
        .filter(|p| {
            p.is_absolute()
                && !p
                    .components()
                    .any(|c| matches!(c, std::path::Component::ParentDir))
        })
        .ok_or_else(|| protected("store_package_location"))?;
    if !matched || !app.starts_with(&location) {
        return Err(protected("store_package_identity"));
    }
    let mut has_contents = false;
    for line in payload.lines().filter(|p| !p.is_empty()) {
        let relative = Path::new(line.trim_start_matches("./"));
        // Check the original path too: stripping './' must never hide traversal.
        if Path::new(line).components().any(|c| {
            matches!(
                c,
                std::path::Component::ParentDir | std::path::Component::RootDir
            )
        }) {
            return Err(protected("store_payload_path"));
        }
        let target = location.join(relative);
        // pkgbuild emits this one AppleDouble sibling for the bundle's resource
        // forks/xattrs. It belongs to that exact app, never an arbitrary sibling.
        let apple_double = app
            .file_name()
            .map(|name| app.with_file_name(format!("._{}", name.to_string_lossy())));
        if !target.starts_with(app)
            && !app.starts_with(&target)
            && apple_double.as_ref() != Some(&target)
        {
            return Err(protected("store_payload_scope"));
        }
        has_contents |= target.starts_with(app.join("Contents"));
    }
    if !has_contents {
        return Err(protected("store_payload_empty"));
    }
    Ok(location)
}
/// Elements of the `Distribution` file Apple's packager emits for an App Store download.
/// Nothing that can carry a script belongs here.
const STORE_DISTRIBUTION_ELEMENTS: &[&str] = &[
    "installer-gui-script",
    "pkg-ref",
    "bundle-version",
    "bundle",
    "product",
    "title",
    "options",
    "volume-check",
    "allowed-os-versions",
    "os-version",
    "choices-outline",
    "line",
    "choice",
    "must-close",
    "app",
];
fn store_distribution_element(
    element: &quick_xml::events::BytesStart<'_>,
    top_level: bool,
    location: &Path,
) -> Result<String, AppError> {
    use quick_xml::XmlVersion;
    let invalid = || protected("store_distribution");
    let name = element.name().as_ref().to_owned();
    if !STORE_DISTRIBUTION_ELEMENTS.contains(&name.as_str())
        || top_level != (name == "installer-gui-script")
    {
        return Err(invalid());
    }
    for attribute in element.attributes() {
        let attribute = attribute.map_err(|_| invalid())?;
        let key = attribute.key.as_ref().to_ascii_lowercase();
        let value = attribute
            .normalized_value(XmlVersion::Implicit1_0)
            .map_err(|_| invalid())?;
        // `require-scripts="false"` only states that the package has none.
        let no_scripts = key == "require-scripts" && value == "false";
        // Choice state attributes can hold JavaScript expressions; Apple's packager writes literals.
        let expression = matches!(
            key.as_str(),
            "start_selected"
                | "start_enabled"
                | "start_visible"
                | "selected"
                | "enabled"
                | "visible"
        ) && !matches!(&*value, "true" | "false");
        if (key.contains("script") && !no_scripts) || expression {
            return Err(invalid());
        }
        if name == "choice" && key == "customlocation" && Path::new(&*value) != location {
            return Err(invalid());
        }
    }
    Ok(name)
}
/// The `Distribution` wrapped around an App Store component package. Installer evaluates
/// JavaScript from `<script>` elements, `script` attributes and expression attributes, so
/// the file has to stay declarative. The package it names must be the component that was
/// checked, and an install location it overrides with must be that component's own.
pub fn store_distribution_scope(
    distribution: &[u8],
    component: &str,
    location: &Path,
) -> Result<(), AppError> {
    use quick_xml::{Reader, XmlVersion, events::Event};
    let invalid = || protected("store_distribution");
    if distribution.len() > 1024 * 1024 {
        return Err(invalid());
    }
    let mut reader = Reader::from_reader(distribution);
    let mut open: Vec<String> = Vec::new();
    let mut rooted = false;
    let mut references = Vec::new();
    loop {
        let in_reference = open.last().is_some_and(|name| name == "pkg-ref");
        match reader.read_event().map_err(|_| invalid())? {
            Event::Start(e) => {
                if open.is_empty() && std::mem::replace(&mut rooted, true) {
                    return Err(invalid());
                }
                open.push(store_distribution_element(&e, open.is_empty(), location)?);
            }
            Event::Empty(e) => {
                if open.is_empty() {
                    return Err(invalid());
                }
                store_distribution_element(&e, false, location)?;
            }
            Event::End(_) => {
                open.pop();
            }
            Event::Text(text) => {
                let text = text.xml_content(XmlVersion::Implicit1_0);
                if in_reference && !text.trim().is_empty() {
                    references.push(text.trim().to_owned());
                }
            }
            // The package it points at must be spelled out plainly.
            Event::CData(_) | Event::GeneralRef(_) if in_reference => return Err(invalid()),
            Event::DocType(_) | Event::PI(_) => return Err(invalid()),
            Event::CData(_) | Event::GeneralRef(_) | Event::Decl(_) | Event::Comment(_) => {}
            Event::Eof => break,
        }
    }
    if !rooted || !open.is_empty() || references != [format!("#{component}")] {
        return Err(invalid());
    }
    Ok(())
}
/// What `pkgutil --expand` leaves of an App Store download: a `Distribution` beside optional
/// `Resources` and exactly one component directory holding `Bom`, `PackageInfo` and
/// `Payload`. A bare component package is accepted too.
struct StoreLayout {
    /// The component's PackageInfo.
    info: PathBuf,
    /// For the distribution form: the file's text and the name of the component it points at.
    distribution: Option<(Vec<u8>, String)>,
}
fn store_layout(expanded: &Path) -> Result<StoreLayout, AppError> {
    let invalid = || protected("store_package_components");
    let mut entries = Vec::new();
    for entry in fs::read_dir(expanded)? {
        let entry = entry?;
        let name = entry.file_name().into_string().map_err(|_| invalid())?;
        let meta = fs::symlink_metadata(entry.path())?;
        if meta.is_symlink() {
            return Err(invalid());
        }
        entries.push((name, meta.is_dir()));
    }
    let named = |wanted: &str| entries.iter().find(|(name, _)| name == wanted);
    let Some((_, distribution_is_dir)) = named("Distribution") else {
        if named("Scripts").is_some() {
            return Err(invalid());
        }
        return Ok(StoreLayout {
            info: expanded.join("PackageInfo"),
            distribution: None,
        });
    };
    let mut components = entries.iter().filter(|(name, _)| name.ends_with(".pkg"));
    let (component, true) = components.next().ok_or_else(invalid)? else {
        return Err(invalid());
    };
    if components.next().is_some()
        || *distribution_is_dir
        || named("Resources").is_some_and(|(_, is_dir)| !is_dir)
        || entries.iter().any(|(name, _)| {
            !matches!(name.as_str(), "Distribution" | "Resources") && name != component
        })
    {
        return Err(invalid());
    }
    let directory = expanded.join(component);
    for entry in fs::read_dir(&directory)? {
        let entry = entry?;
        if !matches!(
            entry.file_name().to_str(),
            Some("Bom" | "PackageInfo" | "Payload")
        ) || !fs::symlink_metadata(entry.path())?.is_file()
        {
            return Err(invalid());
        }
    }
    let distribution = expanded.join("Distribution");
    if fs::symlink_metadata(&distribution)?.len() > 1024 * 1024 {
        return Err(protected("store_package_metadata"));
    }
    Ok(StoreLayout {
        info: directory.join("PackageInfo"),
        distribution: Some((fs::read(distribution)?, component.clone())),
    })
}
/// Confirms that an Apple-signed App Store package installs the requested app and nothing else.
/// Call it on a root-owned copy, after the signature check and before the installer runs.
pub fn validate_store_package(
    pkg: &Path,
    app: &Path,
    bundle: &str,
    work: &Path,
) -> Result<(), AppError> {
    let expanded = work.join("expanded");
    command(
        "/usr/sbin/pkgutil",
        &[
            "--expand",
            &pkg.to_string_lossy(),
            &expanded.to_string_lossy(),
        ],
        Duration::from_secs(120),
    )?;
    let StoreLayout { info, distribution } = store_layout(&expanded)?;
    let meta = fs::symlink_metadata(&info)?;
    if !meta.is_file() || meta.is_symlink() || meta.len() > 1024 * 1024 {
        return Err(protected("store_package_metadata"));
    }
    let payload = command(
        "/usr/sbin/pkgutil",
        &["--payload-files", &pkg.to_string_lossy()],
        Duration::from_secs(120),
    )?;
    let location = store_payload_scope(&fs::read(info)?, &payload, app, bundle)?;
    if let Some((distribution, component)) = distribution {
        store_distribution_scope(&distribution, &component, &location)?;
    }
    Ok(())
}
fn validate_store_payload(
    pkg: &Path,
    request: &PrivilegedInstall,
    work: &Path,
) -> Result<(), AppError> {
    validate_store_package(
        pkg,
        &request.app_path,
        request
            .bundle_id
            .as_deref()
            .ok_or_else(|| protected("store_bundle_id"))?,
        work,
    )
}
pub(crate) fn private_file_copy(
    source: &Path,
    destination: &Path,
    snapshot: &Snapshot,
    max: u64,
) -> Result<(), AppError> {
    use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
    if !snapshot.matches(source) {
        return Err(protected("admin_artifact_changed"));
    }
    let mut input = fs::OpenOptions::new()
        .read(true)
        .custom_flags(nix::libc::O_NOFOLLOW)
        .open(source)?;
    let meta = input.metadata()?;
    if !meta.is_file()
        || meta.dev() != snapshot.device
        || meta.ino() != snapshot.inode
        || meta.len() > max
    {
        return Err(protected("admin_artifact_identity"));
    }
    let mut output = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(destination)?;
    let copied = std::io::copy(&mut (&mut input).take(max + 1), &mut output)?;
    if copied != snapshot.length
        || !snapshot.matches(source)
        || input.metadata()?.modified()? != meta.modified()?
        || input.metadata()?.len() != meta.len()
    {
        return Err(protected("admin_artifact_length"));
    }
    output.sync_all()?;
    Ok(())
}
fn mas_install(request: &PrivilegedInstall, work: &Path) -> Result<(), AppError> {
    use std::os::unix::fs::{DirBuilderExt, OpenOptionsExt, PermissionsExt};
    let pkg = work.join("store.pkg");
    let receipt = work.join("receipt");
    private_file_copy(
        &request.pkg,
        &pkg,
        &request.pkg_snapshot,
        8 * 1024 * 1024 * 1024,
    )?;
    private_file_copy(
        &request.receipt,
        &receipt,
        &request.receipt_snapshot,
        4 * 1024 * 1024,
    )?;
    let signature = command(
        "/usr/sbin/pkgutil",
        &["--check-signature", &pkg.to_string_lossy()],
        Duration::from_secs(60),
    )
    .map_err(|_| AppError::new("E_MAS_ENGINE", "pkg_signature_invalid"))?;
    if !apple_package_signature(&signature) {
        return Err(AppError::new("E_MAS_ENGINE", "pkg_not_signed_by_apple"));
    }
    validate_store_payload(&pkg, request, work)?;
    request.validate_destination()?;
    // There is no arbitrary program, argument array, script or target volume in the
    // request. Only the root-owned verified copy reaches Apple's installer.
    command(
        "/usr/sbin/installer",
        &["-pkg", &pkg.to_string_lossy(), "-target", "/"],
        Duration::from_secs(900),
    )
    .map_err(|_| AppError::new("E_MAS_ENGINE", "administrator_install_failed"))?;
    if !rules::parents_safe(&request.app_path)
        || !request.app_path.is_dir()
        || fs::symlink_metadata(&request.app_path)?.is_symlink()
    {
        return Err(protected("installed_app_path"));
    }
    command(
        "/usr/bin/codesign",
        &[
            "--verify",
            "--deep",
            "--strict",
            "--all-architectures",
            &request.app_path.to_string_lossy(),
        ],
        Duration::from_secs(60),
    )
    .map_err(|_| AppError::new("E_MAS_ENGINE", "installed_app_signature"))?;
    let installed = plist::Value::from_file(request.app_path.join("Contents/Info.plist"))
        .map_err(|_| protected("installed_app_identity"))?;
    if request.bundle_id.as_deref().is_none_or(|bundle| {
        installed
            .as_dictionary()
            .and_then(|d| d.get("CFBundleIdentifier"))
            .and_then(|v| v.as_string())
            != Some(bundle)
    }) {
        return Err(protected("installed_app_identity"));
    }
    let directory = request.app_path.join("Contents/_MASReceipt");
    if !rules::parents_safe(&directory) {
        return Err(protected("receipt_parent"));
    }
    if !directory.exists() {
        fs::DirBuilder::new().mode(0o755).create(&directory)?;
    }
    if fs::symlink_metadata(&directory)?.is_symlink() || !directory.is_dir() {
        return Err(protected("receipt_directory"));
    }
    let output = directory.join("receipt");
    if let Ok(meta) = fs::symlink_metadata(&output)
        && (!meta.is_file() || meta.is_symlink())
    {
        return Err(protected("receipt_target"));
    }
    let temp = directory.join(format!("opennavo-{}.receipt", uuid::Uuid::now_v7()));
    let mut file = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(&temp)?;
    file.write_all(&fs::read(&receipt)?)?;
    file.sync_all()?;
    file.set_permissions(fs::Permissions::from_mode(0o644))?;
    fs::rename(&temp, &output)?; // New file was created by this root process: root:wheel, 0644.
    command(
        "/usr/bin/mdimport",
        &["-i", &request.app_path.to_string_lossy()],
        Duration::from_secs(60),
    )
    .map_err(|_| AppError::new("E_MAS_ENGINE", "installed_app_index"))?;
    swap::register(&request.app_path)
        .map_err(|_| AppError::new("E_MAS_ENGINE", "installed_app_register"))
}
pub fn execute(
    requests: &[Request],
    home: &Path,
    data: &Path,
    uid: u32,
) -> Result<Vec<ResultItem>, AppError> {
    use std::os::unix::fs::DirBuilderExt;
    if unsafe { nix::libc::geteuid() } != 0
        || uid == 0
        || requests.is_empty()
        || requests.len() > 100
    {
        return Err(protected("native_admin_context"));
    }
    // Validate the entire batch before the first privileged action.
    for request in requests {
        if let Err(error) = validate(request, home, data, uid) {
            return Ok(requests
                .iter()
                .map(|r| ResultItem {
                    id: r.id.clone(),
                    error: Some(error.clone()),
                    backup: None,
                })
                .collect());
        }
    }
    let mut results = vec![];
    for request in requests {
        let work =
            Path::new("/private/var/tmp").join(format!("opennavo-admin-{}", uuid::Uuid::now_v7()));
        fs::DirBuilder::new().mode(0o700).create(&work)?;
        let result = (|| {
            validate(request, home, data, uid)?;
            match &request.action {
                Action::MasInstallPkg { request: r } => mas_install(r, &work).map(|_| None),
                Action::AppSwap { request: r } => {
                    let (os, arch) = service::system()?;
                    swap::apply(
                        r,
                        &swap::Context {
                            inspector: &gate::NativeInspector,
                            files: &swap::NativeFiles,
                            os: &os,
                            arch: &arch,
                        },
                    )
                    .map(Some)
                }
                Action::PkgInstall { request: r } => super::package::execute(r, &work).map(Some),
            }
        })();
        let _ = fs::remove_dir_all(&work);
        results.push(match result {
            Ok(backup) => ResultItem {
                id: request.id.clone(),
                error: None,
                backup,
            },
            Err(error) => ResultItem {
                id: request.id.clone(),
                error: Some(error),
                backup: None,
            },
        });
    }
    Ok(results)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{uninstall::rules::Snapshot, updater::swap::SwapRequest};
    #[test]
    fn the_helper_root_is_the_logs_folder_that_holds_native_staging() {
        let tmp = tempfile::tempdir().unwrap();
        let root = tmp.path().canonicalize().unwrap();
        let home = root.join("home");
        let support = home.join("Library/Application Support");
        let data = support.join("com.opennavo.desktop/logs");
        std::fs::create_dir_all(&data).unwrap();
        // Outside /Applications, so a request past the data-root check fails on its own scope.
        let app = root.join("Fixture.app");
        std::fs::create_dir_all(&app).unwrap();
        let request = Request {
            id: "task".into(),
            action: Action::AppSwap {
                request: Box::new(SwapRequest {
                    source: Default::default(),
                    installed: app.clone(),
                    prepared: data
                        .join(crate::queue::NATIVE_STAGING)
                        .join("task/Fixture.app"),
                    installed_snapshot: Snapshot::capture(&app).unwrap(),
                    prepared_snapshot: Snapshot::capture(&app).unwrap(),
                    expected_team: None,
                    package: None,
                }),
            },
        };
        let message = |data: &Path| validate(&request, &home, data, 501).unwrap_err().message;
        assert_eq!(message(&data), "app_swap_scope");
        assert_eq!(
            message(&support.join("com.opennavo.desktop.dev.m1acceptance/logs")),
            "app_swap_scope"
        );
        for wrong in [
            support.join("com.opennavo.desktop"),
            support.join("com.example.other/logs"),
            support.join("com.opennavo.desktop/cache"),
            home.join("Library/Caches/com.opennavo.desktop/logs"),
        ] {
            assert_eq!(message(&wrong), "native_data_root", "{wrong:?}");
        }
    }
}
