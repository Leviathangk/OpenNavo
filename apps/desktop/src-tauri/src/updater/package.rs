//! Fixed package updates. Only a single, script-free application payload can be
//! rolled back completely; multi-product/distribution installers fail closed.
use super::{
    admin, archive,
    gate::{self, Inspector},
    service, swap,
};
use crate::{
    AppError,
    uninstall::{execute::move_exclusive, identity::command, rules::Snapshot},
};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    fs,
    io::Read,
    path::{Path, PathBuf},
    time::Duration,
};

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Artifact {
    pub path: PathBuf,
    pub snapshot: Snapshot,
    pub sha256: String,
}
fn error(message: &str) -> AppError {
    AppError::new("E_UPDATE_INTEGRITY", message)
}
pub fn installer_signature(text: &str, team: &str) -> bool {
    team.len() == 10
        && team
            .bytes()
            .all(|c| c.is_ascii_uppercase() || c.is_ascii_digit())
        && text.lines().any(|s| {
            s.trim().starts_with("Status:")
                && s.contains("signed by a certificate trusted by macOS")
        })
        && text.lines().any(|s| {
            s.trim().starts_with("1. Developer ID Installer: ")
                && s.trim().ends_with(&format!("({team})"))
        })
        && text
            .lines()
            .any(|s| s.trim().ends_with("Developer ID Certification Authority"))
        && text.lines().any(|s| s.trim().ends_with("Apple Root CA"))
}
pub fn verify_signature(path: &Path, team: &str) -> Result<(), AppError> {
    let signature = command(
        "/usr/sbin/pkgutil",
        &["--check-signature", &path.to_string_lossy()],
        Duration::from_secs(60),
    )
    .map_err(|_| error("pkg_signature_invalid"))?;
    if !installer_signature(&signature, team) {
        return Err(AppError::new(
            "E_UPDATE_TEAM_MISMATCH",
            "pkg_installer_team",
        ));
    }
    let result = command(
        "/usr/sbin/spctl",
        &[
            "--assess",
            "--type",
            "install",
            "--verbose=4",
            &path.to_string_lossy(),
        ],
        Duration::from_secs(60),
    );
    if !result.is_ok_and(|s| {
        s.lines()
            .any(|line| line.trim() == "source=Notarized Developer ID")
    }) {
        return Err(AppError::new(
            "E_UPDATE_NOT_NOTARIZED",
            "pkg_notarization_required",
        ));
    }
    Ok(())
}
pub fn fixed_location_metadata(info: &[u8]) -> Result<(), AppError> {
    use quick_xml::{Reader, XmlVersion, events::Event};
    let mut reader = Reader::from_reader(info);
    loop {
        match reader.read_event().map_err(|_| error("pkg_metadata"))? {
            Event::Start(e) if e.name().as_ref() == "relocate" => {
                // Empty relocate is emitted by pkgbuild; a bundle inside would
                // allow Installer to select another copy outside this target.
                if !reader
                    .read_text(e.name())
                    .map_err(|_| error("pkg_relocation"))?
                    .trim()
                    .is_empty()
                {
                    return Err(error("pkg_relocation"));
                }
            }
            Event::Start(e) | Event::Empty(e) if e.name().as_ref() == "pkg-info" => {
                let mut fixed = false;
                for attribute in e.attributes() {
                    let a = attribute.map_err(|_| error("pkg_metadata"))?;
                    let value = a
                        .normalized_value(XmlVersion::Implicit1_0)
                        .map_err(|_| error("pkg_metadata"))?;
                    if a.key.as_ref() == "relocatable" {
                        fixed = value == "false";
                    }
                    if a.key.as_ref() == "postinstall-action" && value != "none" {
                        return Err(error("pkg_postinstall_action"));
                    }
                }
                if !fixed {
                    return Err(error("pkg_relocation"));
                }
            }
            Event::DocType(_) => return Err(error("pkg_metadata")),
            Event::Eof => break,
            _ => {}
        }
    }
    Ok(())
}
fn regular(path: &Path, max: u64) -> Result<(), AppError> {
    let meta = fs::symlink_metadata(path)?;
    if !meta.is_file() || meta.is_symlink() || meta.len() > max {
        return Err(error("pkg_member_type"));
    }
    Ok(())
}
fn extract(path: &Path, destination: &Path) -> Result<(), AppError> {
    use std::os::unix::fs::DirBuilderExt;
    fs::DirBuilder::new().mode(0o700).create(destination)?;
    // BSD tar refuses '..' and traversing symlinks by default. Never pass -P/-U.
    command(
        "/usr/bin/tar",
        &[
            "--no-same-owner",
            "--no-same-permissions",
            "--safe-writes",
            "-xf",
            &path.to_string_lossy(),
            "-C",
            &destination.to_string_lossy(),
        ],
        Duration::from_secs(300),
    )?;
    Ok(())
}
/// Signature/Team validation is required BEFORE invoking this native extractor.
pub fn payload(
    path: &Path,
    destination: &Path,
    installed: &Path,
    bundle: &str,
) -> Result<PathBuf, AppError> {
    extract(path, destination)?;
    for entry in fs::read_dir(destination)? {
        let entry = entry?;
        if !matches!(
            entry.file_name().to_str(),
            Some("PackageInfo" | "Payload" | "Bom")
        ) {
            return Err(AppError::new(
                "E_UPDATE_FEED",
                "pkg_requires_single_script_free_app",
            ));
        }
        regular(&entry.path(), 1024 * 1024 * 1024)?;
    }
    let info = destination.join("PackageInfo");
    regular(&info, 1024 * 1024)?;
    let payload = destination.join("Payload");
    regular(&payload, 1024 * 1024 * 1024)?;
    let listing = command(
        "/usr/bin/tar",
        &["-tf", &payload.to_string_lossy()],
        Duration::from_secs(60),
    )?;
    let info = fs::read(info)?;
    fixed_location_metadata(&info)?;
    let location = admin::store_payload_scope(&info, listing.trim(), installed, bundle)?;
    let root = destination.join("files");
    extract(&payload, &root)?;
    let app = root.join(
        installed
            .strip_prefix(location)
            .map_err(|_| error("pkg_destination"))?,
    );
    // No symlinks are permitted in the path leading to the actual bundle.
    if !crate::uninstall::rules::parents_safe(&app) || fs::symlink_metadata(&app)?.is_symlink() {
        return Err(error("pkg_bundle_path"));
    }
    archive::validate_bundle_links(&app)?;
    Ok(app)
}
pub trait Installer: Send + Sync {
    fn install(&self, pkg: &Path) -> Result<(), AppError>;
}
struct NativeInstaller;
impl Installer for NativeInstaller {
    fn install(&self, pkg: &Path) -> Result<(), AppError> {
        command(
            "/usr/sbin/installer",
            &["-pkg", &pkg.to_string_lossy(), "-target", "/"],
            Duration::from_secs(900),
        )
        .map(|_| ())
    }
}
/// This common transaction is testable with temporary bundles and a fake
/// installer. Production calls it only with a root-owned, reverified package.
pub fn apply(
    request: &swap::SwapRequest,
    pkg: &Path,
    context: &swap::Context<'_>,
    installer: &dyn Installer,
) -> Result<PathBuf, AppError> {
    gate::bundles_for_source(
        context.inspector,
        &request.installed,
        &request.prepared,
        &request.installed_snapshot,
        gate::Platform {
            os: context.os,
            arch: context.arch,
        },
        request.expected_team.as_deref(),
        request.source,
    )?;
    let old = context.inspector.inspect(&request.installed)?;
    let expected = context.inspector.inspect(&request.prepared)?;
    let mut recovery = super::backup::Transaction::new(&request.installed)?;
    let work = recovery.work.clone();
    let backup = work.join("previous.app");
    context.files.copy_bundle(&request.installed, &backup)?;
    let saved = context.inspector.inspect(&backup)?;
    if !saved.valid
        || saved.team_id != old.team_id
        || saved.requirement != old.requirement
        || saved.version != old.version
    {
        return Err(error("backup_verification"));
    }
    if !request.installed_snapshot.still(&request.installed) {
        return Err(error("installed_app_changed"));
    }
    recovery.publish()?;
    let previous = recovery.previous.clone();
    let outcome = (|| {
        installer.install(pkg)?;
        if !crate::uninstall::rules::parents_safe(&request.installed)
            || fs::symlink_metadata(&request.installed)?.is_symlink()
        {
            return Err(error("pkg_installed_path"));
        }
        archive::validate_bundle_links(&request.installed)?;
        let installed = context.inspector.inspect(&request.installed)?;
        gate::identities_for_source(
            &old,
            &installed,
            context
                .inspector
                .satisfies(&request.installed, &old.requirement)?,
            context.os,
            context.arch,
            request.expected_team.as_deref(),
            request.source,
        )?;
        if installed.version != expected.version {
            return Err(error("pkg_installed_version"));
        }
        context.files.register(&request.installed)
    })();
    if let Err(failure) = outcome {
        let rollback = (|| {
            let restore = work.join("restore.app");
            context.files.copy_bundle(&previous, &restore)?;
            if fs::symlink_metadata(&request.installed).is_ok() {
                context.files.exchange(&request.installed, &restore)?;
            } else {
                move_exclusive(&restore, &request.installed)?;
            }
            let restored = context.inspector.inspect(&request.installed)?;
            if !restored.valid
                || restored.team_id != old.team_id
                || restored.version != old.version
                || restored.requirement != old.requirement
            {
                return Err(error("rollback_verification"));
            }
            Ok::<(), AppError>(())
        })();
        if let Err(rollback) = rollback {
            recovery.preserve_recovery();
            return Err(
                AppError::new("E_UNKNOWN", "update_rollback_failed").with_detail(format!(
                    "{}; {}; recovery={}",
                    failure.code,
                    rollback.code,
                    previous.display()
                )),
            );
        }
        let _ = context.files.register(&request.installed);
        return Err(AppError::new("E_UPDATE_SWAP", "update_reverted").with_detail(failure.code));
    }
    Ok(recovery.commit())
}
pub fn execute(request: &swap::SwapRequest, work: &Path) -> Result<PathBuf, AppError> {
    let artifact = request
        .package
        .as_ref()
        .ok_or_else(|| error("missing_package"))?;
    let copy = work.join("update.pkg");
    admin::private_file_copy(
        &artifact.path,
        &copy,
        &artifact.snapshot,
        1024 * 1024 * 1024,
    )?;
    let mut hash = Sha256::new();
    let mut file = fs::File::open(&copy)?;
    let mut chunk = [0u8; 65536];
    loop {
        let len = file.read(&mut chunk)?;
        if len == 0 {
            break;
        }
        hash.update(&chunk[..len]);
    }
    if format!("{:x}", hash.finalize()) != artifact.sha256 {
        return Err(error("download_sha256"));
    }
    let old = gate::NativeInspector.inspect(&request.installed)?;
    verify_signature(&copy, &old.team_id)?;
    let prepared = payload(
        &copy,
        &work.join("package"),
        &request.installed,
        &old.identifier,
    )?;
    let mut verified = request.clone();
    verified.prepared_snapshot = Snapshot::capture(&prepared)?;
    verified.prepared = prepared;
    let (os, arch) = service::system()?;
    apply(
        &verified,
        &copy,
        &swap::Context {
            inspector: &gate::NativeInspector,
            files: &swap::NativeFiles,
            os: &os,
            arch: &arch,
        },
        &NativeInstaller,
    )
}
