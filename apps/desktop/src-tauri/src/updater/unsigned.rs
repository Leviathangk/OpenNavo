//! Per-artifact consent for reviewable native update exceptions. Unsigned
//! replacements require publisher hashes; tree hashes bind every later copy.
use super::{archive, gate, service::Candidate, swap::SwapRequest, version};
use crate::{AppError, model::UpdateSource, uninstall::rules::Snapshot};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    fs,
    io::Read,
    os::unix::{
        ffi::OsStrExt,
        fs::{MetadataExt, OpenOptionsExt},
    },
    path::Path,
};

fn error(reason: &str) -> AppError {
    AppError::new("E_UPDATE_CHANGED", reason)
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Approval {
    bundle_id: String,
    version: String,
    version_key: String,
    source: UpdateSource,
    installed_digest: String,
    prepared_digest: String,
    authenticated_download: bool,
    warnings: Vec<super::UpdateWarning>,
}
impl Approval {
    pub fn prepare(candidate: &Candidate, prepared: &Path) -> Result<Self, AppError> {
        let (version, version_key) = if let Some(a) = &candidate.artifact {
            (a.version.clone(), a.version_key.clone())
        } else if let Some(f) = &candidate.feed_item {
            (f.version.clone(), "CFBundleVersion".into())
        } else {
            return Err(error("update_artifact_missing"));
        };
        let authenticated_download = candidate
            .artifact
            .as_ref()
            .is_some_and(|a| a.file.sha512.is_some() || a.file.sha256.is_some())
            || (candidate.item.source == UpdateSource::Sparkle && candidate.public_key.is_some());
        Ok(Self {
            bundle_id: candidate
                .item
                .bundle_id
                .clone()
                .filter(|s| !s.is_empty())
                .ok_or_else(|| AppError::new("E_UPDATE_IDENTITY", "bundle_id_missing"))?,
            version,
            version_key,
            source: candidate.item.source,
            installed_digest: tree_digest(&candidate.app_path)?,
            prepared_digest: tree_digest(prepared)?,
            authenticated_download,
            warnings: vec![super::UpdateWarning::UnsignedDeveloper],
        })
    }
    /// Called only after archive digest/EdDSA and the declared version were checked.
    /// The challenge includes both trees, artifact metadata, and the exact warnings.
    pub fn review(
        candidate: &Candidate,
        request: &mut SwapRequest,
        consent: Option<&super::UpdateConsent>,
        platform: gate::Platform<'_>,
    ) -> Result<(), AppError> {
        let mut approval = Self::prepare(candidate, &request.prepared)?;
        if request.unsigned.as_ref().is_some_and(|downloaded| {
            downloaded.installed_digest != approval.installed_digest
                || downloaded.prepared_digest != approval.prepared_digest
        }) {
            return Err(error("update_bundle_changed"));
        }
        let mut warnings = approval.assess(request, &request.prepared, platform)?;
        if let Some(package) = &request.package {
            warnings.extend(super::package::review_signature(
                &package.path,
                request.expected_team.as_deref(),
                approval.authenticated_download,
            )?);
        }
        warnings.sort();
        warnings.dedup();
        let key = format!(
            "{:x}",
            Sha256::digest(serde_json::to_vec(&(
                candidate.unsigned_key()?,
                &approval.installed_digest,
                &approval.prepared_digest,
                &warnings,
                request.package.as_ref().map(|p| &p.sha256),
            ))?)
        );
        let challenge = super::UpdateConsent {
            key,
            warnings: warnings.clone(),
        };
        // The early unsigned-app dialog covers only developer identity. Any other
        // exception still needs its own exact, post-download confirmation.
        let early_unsigned = request.unsigned.is_some()
            && warnings
                .iter()
                .all(|w| *w == super::UpdateWarning::UnsignedDeveloper);
        if !warnings.is_empty() && !early_unsigned && consent != Some(&challenge) {
            return Err(
                AppError::new("E_UPDATE_CONFIRMATION", "update_confirmation_required")
                    .with_detail(serde_json::to_string(&challenge)?),
            );
        }
        if warnings.is_empty() {
            request.unsigned = None;
        } else {
            approval.warnings = warnings;
            request.unsigned = Some(approval);
            // These packages have a single fixed, script-free app payload. Use
            // the atomic bundle swap; never run an unverified installer as root.
            request.package = None;
        }
        Ok(())
    }
    fn assess(
        &self,
        request: &SwapRequest,
        prepared: &Path,
        platform: gate::Platform<'_>,
    ) -> Result<Vec<super::UpdateWarning>, AppError> {
        use super::UpdateWarning as W;
        let old = gate::observe(&request.installed, false)?;
        let new = gate::observe(prepared, true)?;
        use gate::Inspector;
        let requirement_matches = old.unsigned
            || new.unsigned
            || gate::NativeInspector.satisfies(prepared, &old.identity.requirement)?;
        let mut warnings = identity_warnings(
            &old,
            &new,
            &self.bundle_id,
            request.expected_team.as_deref(),
            self.authenticated_download,
            requirement_matches,
            platform,
        )?;
        let current = super::check::disk_version(&request.installed, &self.version_key)
            .ok_or_else(|| AppError::new("E_UPDATE_VERSION", "installed_version_missing"))?;
        let actual = super::check::disk_version(prepared, &self.version_key)
            .ok_or_else(|| AppError::new("E_UPDATE_VERSION", "bundle_version_missing"))?;
        if !version::compare_source(self.source, &actual, &self.version)?.is_eq() {
            return Err(AppError::new(
                "E_UPDATE_VERSION",
                "feed_bundle_version_mismatch",
            ));
        }
        if !version::compare_source(self.source, &actual, &current)?.is_gt() {
            warnings.push(W::NonNewer);
        }
        // Match the ordinary gate's build/marketing comparison too. Otherwise a
        // feed with a newer build but older marketing version fails only after review.
        if let Err(error) = gate::versions_for_source(&old.identity, &new.identity, self.source) {
            if error.code == "E_UPDATE_NOT_NEWER" {
                warnings.push(W::NonNewer);
            } else {
                return Err(error);
            }
        }
        warnings.sort();
        warnings.dedup();
        Ok(warnings)
    }
    pub fn validate(
        &self,
        request: &SwapRequest,
        prepared: &Path,
        platform: gate::Platform<'_>,
    ) -> Result<(), AppError> {
        if request.source != self.source
            || request.package.is_some()
            || !request
                .installed_snapshot
                .same_object(&Snapshot::capture(&request.installed)?)
        {
            return Err(error("update_scope_changed"));
        }
        if tree_digest(&request.installed)? != self.installed_digest
            || tree_digest(prepared)? != self.prepared_digest
        {
            return Err(error("update_bundle_changed"));
        }
        let warnings = self.assess(request, prepared, platform)?;
        if warnings.iter().any(|w| !self.warnings.contains(w)) {
            return Err(error("update_verification_changed"));
        }
        Ok(())
    }
    pub fn verify_backup(&self, installed: &Path, backup: &Path) -> Result<(), AppError> {
        if tree_digest(installed)? != self.installed_digest
            || tree_digest(backup)? != self.installed_digest
        {
            return Err(error("backup_verification"));
        }
        Ok(())
    }
}

#[allow(clippy::too_many_arguments)]
fn identity_warnings(
    old: &gate::Observation,
    new: &gate::Observation,
    expected_id: &str,
    expected_team: Option<&str>,
    authenticated: bool,
    requirement_matches: bool,
    platform: gate::Platform<'_>,
) -> Result<Vec<super::UpdateWarning>, AppError> {
    use super::UpdateWarning as W;
    if old.bundle_id != expected_id || new.bundle_id != expected_id {
        return Err(AppError::new("E_UPDATE_IDENTITY", "bundle_id_changed"));
    }
    if !new.identity.valid && !new.unsigned {
        return Err(AppError::new(
            "E_UPDATE_SIGNATURE",
            "invalid_bundle_signature",
        ));
    }
    let mut warnings = Vec::new();
    if old.unsigned || new.unsigned {
        warnings.push(W::UnsignedDeveloper);
    }
    if !old.identity.valid && !old.unsigned {
        warnings.push(W::RepairInstalled);
    }
    if new.unsigned && !authenticated {
        return Err(AppError::new("E_UPDATE_DIGEST", "required_release_digest"));
    }
    if expected_team.is_some_and(|t| t != new.identity.team_id)
        || (!old.unsigned && (old.identity.team_id != new.identity.team_id || new.unsigned))
    {
        return Err(AppError::new(
            "E_UPDATE_TEAM_MISMATCH",
            "signing_identity_changed",
        ));
    }
    if !old.unsigned
        && !new.unsigned
        && (old.identity.identifier != new.identity.identifier || !requirement_matches)
    {
        return Err(AppError::new(
            "E_UPDATE_TEAM_MISMATCH",
            "signing_identity_changed",
        ));
    }
    if !new.unsigned {
        if new.identity.notarization_unavailable {
            return Err(AppError::new(
                "E_UPDATE_VERIFY_UNAVAILABLE",
                "notarization_check_unavailable",
            ));
        }
        if !new.identity.notarized {
            warnings.push(W::NotNotarized);
        }
    }
    if !new.identity.architectures.contains(platform.arch)
        || (old.identity.architectures.contains("arm64")
            && !new.identity.architectures.contains("arm64"))
    {
        return Err(AppError::new("E_UPDATE_ARCH", "incompatible_architecture"));
    }
    if [
        &new.identity.minimum_os,
        new.identity
            .minimum_os_by_arch
            .get(platform.arch)
            .unwrap_or(&new.identity.minimum_os),
    ]
    .iter()
    .any(|v| !v.is_empty() && version::compare(platform.os, v).is_lt())
    {
        return Err(AppError::new("E_UPDATE_MIN_OS", "minimum_system_version"));
    }
    Ok(warnings)
}

/// Deterministic content, names, link targets and mode bits. No timestamps or
/// ownership: ditto legitimately changes those when making a privileged copy.
pub fn tree_digest(root: &Path) -> Result<String, AppError> {
    archive::validate_bundle_links(root)?;
    fn field(hash: &mut Sha256, bytes: &[u8]) {
        hash.update((bytes.len() as u64).to_le_bytes());
        hash.update(bytes);
    }
    fn visit(
        root: &Path,
        path: &Path,
        hash: &mut Sha256,
        count: &mut usize,
        total: &mut u64,
    ) -> Result<(), AppError> {
        *count += 1;
        if *count > 200_000 || path.components().count() - root.components().count() > 128 {
            return Err(error("unsigned_bundle_limit"));
        }
        let meta = fs::symlink_metadata(path)?;
        field(
            hash,
            path.strip_prefix(root)
                .map_err(|_| error("unsigned_bundle_path"))?
                .as_os_str()
                .as_bytes(),
        );
        hash.update((meta.mode() & 0o177777).to_le_bytes());
        if meta.is_symlink() {
            field(hash, fs::read_link(path)?.as_os_str().as_bytes());
        } else if meta.is_dir() {
            let mut children = fs::read_dir(path)?
                .map(|e| e.map(|e| e.path()))
                .collect::<Result<Vec<_>, _>>()?;
            children.sort();
            for child in children {
                visit(root, &child, hash, count, total)?;
            }
        } else if meta.is_file() {
            *total = total.saturating_add(meta.len());
            if *total > 8 * 1024 * 1024 * 1024 {
                return Err(error("unsigned_bundle_limit"));
            }
            hash.update(meta.len().to_le_bytes());
            let mut file = fs::OpenOptions::new()
                .read(true)
                .custom_flags(nix::libc::O_NOFOLLOW | nix::libc::O_NONBLOCK)
                .open(path)?;
            if !file.metadata()?.is_file() {
                return Err(error("unsigned_bundle_file"));
            }
            let mut bytes = [0; 65536];
            let mut read = 0;
            loop {
                let n = file.read(&mut bytes)?;
                if n == 0 {
                    break;
                }
                read += n as u64;
                if read > meta.len() {
                    return Err(error("unsigned_bundle_changed"));
                }
                hash.update(&bytes[..n]);
            }
            if read != meta.len() {
                return Err(error("unsigned_bundle_changed"));
            }
        } else {
            return Err(error("unsigned_bundle_file"));
        }
        Ok(())
    }
    let mut hash = Sha256::new();
    visit(root, root, &mut hash, &mut 0, &mut 0)?;
    Ok(format!("{:x}", hash.finalize()))
}

#[cfg(test)]
mod tests {
    use super::super::UpdateWarning as W;
    use super::*;
    fn observed() -> gate::Observation {
        gate::Observation {
            unsigned: false,
            bundle_id: "test.example.App".into(),
            identity: gate::Identity {
                valid: true,
                team_id: "TESTTEAM01".into(),
                identifier: "test.example.App".into(),
                requirement: "developer requirement".into(),
                notarized: true,
                notarization_unavailable: false,
                architectures: ["arm64".into()].into(),
                minimum_os: "13.0".into(),
                minimum_os_by_arch: Default::default(),
                version: "2".into(),
                marketing_version: "2.0".into(),
            },
        }
    }
    fn check(
        old: &gate::Observation,
        new: &gate::Observation,
        authenticated: bool,
    ) -> Result<Vec<W>, AppError> {
        identity_warnings(
            old,
            new,
            "test.example.App",
            None,
            authenticated,
            true,
            gate::Platform {
                os: "15.0",
                arch: "arm64",
            },
        )
    }
    #[test]
    fn repairs_and_unsigned_to_signed_migration_require_specific_consent() {
        let mut old = observed();
        let new = observed();
        old.identity.valid = false;
        assert_eq!(check(&old, &new, true).unwrap(), vec![W::RepairInstalled]);
        old.unsigned = true;
        old.identity.team_id.clear();
        assert_eq!(check(&old, &new, true).unwrap(), vec![W::UnsignedDeveloper]);
        let mut new = observed();
        new.identity.notarized = false;
        assert_eq!(
            check(&old, &new, true).unwrap(),
            vec![W::UnsignedDeveloper, W::NotNotarized]
        );
        new.identity.notarization_unavailable = true;
        assert_eq!(
            check(&old, &new, true).unwrap_err().code,
            "E_UPDATE_VERIFY_UNAVAILABLE"
        );
    }
    #[test]
    fn consent_never_bypasses_corrupt_signed_code_identity_or_missing_digest() {
        let old = observed();
        let mut new = observed();
        new.identity.valid = false;
        assert_eq!(
            check(&old, &new, true).unwrap_err().code,
            "E_UPDATE_SIGNATURE"
        );
        new.identity.valid = true;
        new.bundle_id = "wrong.app".into();
        assert_eq!(
            check(&old, &new, true).unwrap_err().code,
            "E_UPDATE_IDENTITY"
        );
        let mut old = observed();
        old.unsigned = true;
        old.identity.team_id.clear();
        let mut new = observed();
        new.unsigned = true;
        new.identity.team_id.clear();
        assert_eq!(
            check(&old, &new, false).unwrap_err().code,
            "E_UPDATE_DIGEST"
        );
        assert_eq!(check(&old, &new, true).unwrap(), vec![W::UnsignedDeveloper]);
        new.unsigned = false;
        new.identity.team_id = "OTHERTEAM1".into();
        assert_eq!(
            check(&observed(), &new, true).unwrap_err().code,
            "E_UPDATE_TEAM_MISMATCH"
        );
    }
}
