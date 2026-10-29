//! Installation checks are mandatory both before staging and at the final swap.
//! Tests substitute observations; production never accepts a validation override.
use super::version;
use crate::{AppError, uninstall::rules::Snapshot};
use base64::{Engine, engine::general_purpose::STANDARD};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    cmp::Ordering,
    collections::{BTreeMap, BTreeSet},
    path::Path,
    time::Duration,
};

fn fail(code: &str, message: &str) -> AppError {
    AppError::new(code, message)
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Identity {
    pub valid: bool,
    pub team_id: String,
    pub identifier: String,
    pub requirement: String,
    pub notarized: bool,
    /// The system assessment could not run (for example, a timeout). This is
    /// different from a successful assessment that explicitly rejected the app.
    #[serde(default)]
    pub notarization_unavailable: bool,
    pub architectures: BTreeSet<String>,
    pub minimum_os: String,
    #[serde(default)]
    pub minimum_os_by_arch: BTreeMap<String, String>,
    pub version: String,
    #[serde(default)]
    pub marketing_version: String,
}
pub trait Inspector: Send + Sync {
    fn inspect(&self, path: &Path) -> Result<Identity, AppError>;
    fn satisfies(&self, path: &Path, requirement: &str) -> Result<bool, AppError>;
}
pub struct NativeInspector;
pub struct Observation {
    pub identity: Identity,
    pub unsigned: bool,
    pub bundle_id: String,
}

/// Read the installed identity even when its resource seal is damaged. A caller
/// must still authenticate the replacement and explicitly confirm a repair.
pub fn observe(path: &Path, assess: bool) -> Result<Observation, AppError> {
    let path_text = path
        .to_str()
        .ok_or_else(|| fail("E_UPDATE_IDENTITY", "bundle_path"))?;
    let verification = crate::native_command::output(
        "/usr/bin/codesign",
        &[
            "--verify",
            "--deep",
            "--strict",
            "--all-architectures",
            path_text,
        ],
        Duration::from_secs(60),
    )
    .map_err(|e| e.context("E_UPDATE_VERIFY_UNAVAILABLE", "signature_check_unavailable"))?;
    if !matches!(verification.code, Some(0 | 1)) {
        return Err(fail(
            "E_UPDATE_VERIFY_UNAVAILABLE",
            "signature_check_unavailable",
        ));
    }
    let valid = verification.code == Some(0);
    let info = crate::native_command::output(
        "/usr/bin/codesign",
        &["-d", "-r-", "--verbose=4", path_text],
        Duration::from_secs(15),
    )
    .map_err(|e| e.context("E_UPDATE_VERIFY_UNAVAILABLE", "signature_information"))?;
    let unsigned =
        info.code == Some(1) && info.stderr.contains(": code object is not signed at all");
    if info.code != Some(0) && !unsigned {
        return Err(fail("E_UPDATE_SIGNATURE", "signature_information_invalid"));
    }
    let info = info.text();
    let field = |prefix: &str| {
        info.lines()
            .find_map(|l| l.strip_prefix(prefix))
            .unwrap_or("")
            .to_owned()
    };
    let unsigned = unsigned
        || (info.lines().any(|l| l == "Signature=adhoc")
            && !info.lines().any(|l| l.starts_with("Authority="))
            && matches!(field("TeamIdentifier=").as_str(), "" | "not set"));
    let dictionary = plist::Value::from_file(path.join("Contents/Info.plist"))
        .map_err(|_| fail("E_UPDATE_ARCHIVE", "bundle_info"))?;
    let dict = dictionary
        .as_dictionary()
        .ok_or_else(|| fail("E_UPDATE_ARCHIVE", "bundle_info"))?;
    let value = |key: &str| {
        dict.get(key)
            .and_then(|v| v.as_string())
            .unwrap_or("")
            .to_owned()
    };
    let executable = value("CFBundleExecutable");
    if executable.is_empty() || executable.contains('/') || executable == "." || executable == ".."
    {
        return Err(fail("E_UPDATE_IDENTITY", "bundle_executable"));
    }
    let binary = path.join("Contents/MacOS").join(&executable);
    if !binary.canonicalize()?.starts_with(path.canonicalize()?) {
        return Err(fail("E_UPDATE_IDENTITY", "bundle_executable_escape"));
    }
    let slices = super::macho::inspect(&binary)?;
    let architectures = slices.keys().cloned().collect();
    let (notarized, notarization_unavailable) = if assess && valid && !unsigned {
        match assess_notarization(path, "execute") {
            Ok(value) => (value, false),
            Err(e) if e.code == "E_UPDATE_VERIFY_UNAVAILABLE" => (false, true),
            Err(e) => return Err(e),
        }
    } else {
        (false, false)
    };
    let minimum_os = value("LSMinimumSystemVersion");
    let minimum_os_by_arch = architecture_minimums(&slices, dict);
    Ok(Observation {
        unsigned,
        bundle_id: value("CFBundleIdentifier"),
        identity: Identity {
            valid,
            team_id: field("TeamIdentifier="),
            identifier: field("Identifier="),
            requirement: field("designated => "),
            notarized,
            notarization_unavailable,
            architectures,
            minimum_os,
            minimum_os_by_arch,
            version: value("CFBundleVersion"),
            marketing_version: value("CFBundleShortVersionString"),
        },
    })
}

pub fn assess_notarization(path: &Path, kind: &str) -> Result<bool, AppError> {
    let output = crate::native_command::output(
        "/usr/sbin/spctl",
        &[
            "--assess",
            "--type",
            kind,
            "--verbose=4",
            &path.to_string_lossy(),
        ],
        Duration::from_secs(60),
    );
    notarization_result(output)
}
fn notarization_result(
    output: Result<crate::native_command::Output, AppError>,
) -> Result<bool, AppError> {
    let output = output.map_err(|e| {
        e.context(
            "E_UPDATE_VERIFY_UNAVAILABLE",
            "notarization_check_unavailable",
        )
    })?;
    let text = output.text();
    if output.code == Some(0)
        && text
            .lines()
            .any(|l| l.trim() == "source=Notarized Developer ID")
    {
        return Ok(true);
    }
    if text.to_ascii_lowercase().contains("revoked")
        || text.to_ascii_lowercase().contains("malware")
    {
        return Err(fail("E_UPDATE_SIGNATURE", "system_security_rejection"));
    }
    if output.code == Some(3)
        && text
            .lines()
            .any(|l| l.trim() == "source=Unnotarized Developer ID")
    {
        return Ok(false);
    }
    Err(fail(
        "E_UPDATE_VERIFY_UNAVAILABLE",
        "notarization_check_unavailable",
    ))
}

impl Inspector for NativeInspector {
    fn inspect(&self, path: &Path) -> Result<Identity, AppError> {
        let output = crate::native_command::output(
            "/usr/bin/codesign",
            &[
                "--verify",
                "--deep",
                "--strict",
                "--all-architectures",
                &path.to_string_lossy(),
            ],
            Duration::from_secs(60),
        )
        .map_err(|e| e.context("E_UPDATE_VERIFY_UNAVAILABLE", "signature_check_unavailable"))?;
        match output.code {
            Some(0) => {}
            Some(1) => {
                return Err(fail("E_UPDATE_SIGNATURE", "invalid_bundle_signature")
                    .with_detail(output.text()));
            }
            _ => {
                return Err(fail(
                    "E_UPDATE_VERIFY_UNAVAILABLE",
                    "signature_check_unavailable",
                ));
            }
        }
        let observed = observe(path, true)?;
        if !observed.identity.valid {
            return Err(fail("E_UPDATE_SIGNATURE", "invalid_bundle_signature"));
        }
        Ok(observed.identity)
    }
    fn satisfies(&self, path: &Path, requirement: &str) -> Result<bool, AppError> {
        if requirement.is_empty() {
            log::warn!("Update identity gate: check=installed_requirement_missing path={path:?}");
            return Ok(false);
        }
        // codesign treats a plain -R argument as a file name. The leading '='
        // selects literal requirement source, preserving its quotes/comments.
        let literal = format!("={requirement}");
        let result = crate::native_command::output(
            "/usr/bin/codesign",
            &[
                "--verify",
                "--deep",
                "--strict",
                "--all-architectures",
                "-R",
                &literal,
                path.to_str()
                    .ok_or_else(|| fail("E_UPDATE_IDENTITY", "bundle_path"))?,
            ],
            Duration::from_secs(60),
        );
        let output = result.map_err(|e| {
            e.context(
                "E_UPDATE_VERIFY_UNAVAILABLE",
                "requirement_check_unavailable",
            )
        })?;
        match output.code {
            Some(0) => Ok(true),
            Some(1) => Ok(false),
            Some(3)
                if output
                    .text()
                    .contains("code failed to satisfy specified code requirement(s)") =>
            {
                Ok(false)
            }
            _ => Err(fail(
                "E_UPDATE_VERIFY_UNAVAILABLE",
                "requirement_check_unavailable",
            )),
        }
    }
}

pub fn identities(
    old: &Identity,
    new: &Identity,
    requirement_satisfied: bool,
    os: &str,
    arch: &str,
    expected_team: Option<&str>,
) -> Result<(), AppError> {
    identities_for_source(
        old,
        new,
        requirement_satisfied,
        os,
        arch,
        expected_team,
        crate::model::UpdateSource::Sparkle,
    )
}

pub fn identities_for_source(
    old: &Identity,
    new: &Identity,
    requirement_satisfied: bool,
    os: &str,
    arch: &str,
    expected_team: Option<&str>,
    source: crate::model::UpdateSource,
) -> Result<(), AppError> {
    compatible_identities(old, new, requirement_satisfied, os, arch, expected_team)?;
    versions_for_source(old, new, source)
}

pub(crate) fn versions_for_source(
    old: &Identity,
    new: &Identity,
    source: crate::model::UpdateSource,
) -> Result<(), AppError> {
    if source == crate::model::UpdateSource::Electron {
        return electron_versions(old, new);
    }
    let marketing = if old.marketing_version.is_empty() || new.marketing_version.is_empty() {
        Ordering::Equal
    } else {
        version::compare(&new.marketing_version, &old.marketing_version)
    };
    if marketing == Ordering::Less
        || (marketing == Ordering::Equal
            && (new.version.is_empty()
                || version::compare(&new.version, &old.version) != Ordering::Greater))
    {
        return Err(fail("E_UPDATE_NOT_NEWER", "bundle_version_not_newer"));
    }
    Ok(())
}

/// Reopening an Electron app after its own updater ran may leave the same version.
/// Still verify its identity even when the bundle directory inode did not change.
pub(crate) fn compatible_identities(
    old: &Identity,
    new: &Identity,
    requirement_satisfied: bool,
    os: &str,
    arch: &str,
    expected_team: Option<&str>,
) -> Result<(), AppError> {
    if !old.valid || !new.valid {
        return Err(fail("E_UPDATE_SIGNATURE", "invalid_bundle_signature"));
    }
    let failed: Vec<_> = [
        (
            "installed_team_missing",
            old.team_id.is_empty() || old.team_id == "not set",
        ),
        ("team_id_changed", old.team_id != new.team_id),
        ("installed_identifier_missing", old.identifier.is_empty()),
        ("identifier_changed", old.identifier != new.identifier),
        ("installed_requirement_missing", old.requirement.is_empty()),
        ("requirement_not_satisfied", !requirement_satisfied),
        (
            "expected_team_mismatch",
            expected_team.is_some_and(|team| team != old.team_id),
        ),
    ]
    .into_iter()
    .filter_map(|(check, failed)| failed.then_some(check))
    .collect();
    if !failed.is_empty() {
        log::warn!(
            "Update identity gate rejected: checks={failed:?} installed_team={:?} candidate_team={:?} installed_identifier={:?} candidate_identifier={:?} expected_team={expected_team:?} requirement_satisfied={requirement_satisfied}",
            old.team_id,
            new.team_id,
            old.identifier,
            new.identifier,
        );
        return Err(fail("E_UPDATE_TEAM_MISMATCH", "signing_identity_changed"));
    }
    if !new.notarized {
        if new.notarization_unavailable {
            return Err(fail(
                "E_UPDATE_VERIFY_UNAVAILABLE",
                "notarization_check_unavailable",
            ));
        }
        return Err(fail("E_UPDATE_NOT_NOTARIZED", "notarization_required"));
    }
    if !new.architectures.contains(arch)
        || (old.architectures.contains("arm64") && !new.architectures.contains("arm64"))
    {
        return Err(fail("E_UPDATE_ARCH", "incompatible_architecture"));
    }
    if [
        &new.minimum_os,
        new.minimum_os_by_arch.get(arch).unwrap_or(&new.minimum_os),
    ]
    .into_iter()
    .any(|minimum| !minimum.is_empty() && version::compare(os, minimum).is_lt())
    {
        return Err(fail("E_UPDATE_MIN_OS", "minimum_system_version"));
    }
    Ok(())
}
/// Backwards-compatible Sparkle bundle gate for callers without an update source.
pub fn bundles(
    inspector: &dyn Inspector,
    old: &Path,
    new: &Path,
    snapshot: &Snapshot,
    os: &str,
    arch: &str,
    expected_team: Option<&str>,
) -> Result<(), AppError> {
    bundles_for_source(
        inspector,
        old,
        new,
        snapshot,
        Platform { os, arch },
        expected_team,
        crate::model::UpdateSource::Sparkle,
    )
}
pub struct Platform<'a> {
    pub os: &'a str,
    pub arch: &'a str,
}

pub fn bundles_for_source(
    inspector: &dyn Inspector,
    old: &Path,
    new: &Path,
    snapshot: &Snapshot,
    platform: Platform<'_>,
    expected_team: Option<&str>,
    source: crate::model::UpdateSource,
) -> Result<(), AppError> {
    let Platform { os, arch } = platform;
    if !snapshot.same_object(&Snapshot::capture(old)?) {
        return Err(fail("E_UPDATE_CHANGED", "installed_app_changed"));
    }
    super::archive::validate_bundle_links(new)?;
    let before = inspector.inspect(old)?;
    let after = inspector.inspect(new)?;
    identities_for_source(
        &before,
        &after,
        inspector.satisfies(new, &before.requirement)?,
        os,
        arch,
        expected_team,
        source,
    )
}
pub fn verify_archive(
    bytes: &[u8],
    public_key: &str,
    signature: &str,
    length: Option<u64>,
    sha256: Option<&str>,
) -> Result<String, AppError> {
    if length.is_some_and(|v| v != bytes.len() as u64) {
        return Err(fail("E_UPDATE_DIGEST", "download_length"));
    }
    let digest = format!("{:x}", Sha256::digest(bytes));
    if sha256.is_some_and(|expected| {
        expected.len() != 64
            || !expected.bytes().all(|b| b.is_ascii_hexdigit())
            || !expected.eq_ignore_ascii_case(&digest)
    }) {
        return Err(fail("E_UPDATE_DIGEST", "download_sha256"));
    }
    let key = STANDARD
        .decode(public_key)
        .map_err(|_| fail("E_UPDATE_FEED_SIGNATURE", "eddsa_key"))?;
    let signature = STANDARD
        .decode(signature)
        .map_err(|_| fail("E_UPDATE_FEED_SIGNATURE", "eddsa_signature"))?;
    ring::signature::UnparsedPublicKey::new(&ring::signature::ED25519, &key)
        .verify(bytes, &signature)
        .map_err(|_| fail("E_UPDATE_FEED_SIGNATURE", "eddsa_verification"))?;
    Ok(digest)
}

fn architecture_minimums(
    slices: &BTreeMap<String, String>,
    dict: &plist::Dictionary,
) -> BTreeMap<String, String> {
    let declared = dict
        .get("LSMinimumSystemVersionByArchitecture")
        .and_then(|v| v.as_dictionary());
    slices
        .iter()
        .map(|(arch, binary)| {
            let plist = declared
                .and_then(|d| d.get(arch))
                .and_then(|v| v.as_string())
                .unwrap_or("");
            (
                arch.clone(),
                if version::compare(plist, binary).is_gt() {
                    plist.to_owned()
                } else {
                    binary.clone()
                },
            )
        })
        .collect()
}

pub(crate) fn electron_versions(old: &Identity, new: &Identity) -> Result<(), AppError> {
    let source = crate::model::UpdateSource::Electron;
    // Electron's semantic version lives in CFBundleShortVersionString; CFBundleVersion may be a build counter.
    let order = version::compare_source(source, &new.marketing_version, &old.marketing_version)?;
    if order.is_lt()
        || (order.is_eq()
            && (new.version.is_empty() || !version::compare(&new.version, &old.version).is_gt()))
    {
        return Err(fail("E_UPDATE_NOT_NEWER", "bundle_version_not_newer"));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    fn identity(build: &str) -> Identity {
        Identity {
            valid: true,
            team_id: "TESTTEAM01".into(),
            identifier: "test.app".into(),
            requirement: "test requirement".into(),
            notarization_unavailable: false,
            notarized: true,
            architectures: ["arm64".into(), "x86_64".into()].into(),
            minimum_os: "12.0".into(),
            minimum_os_by_arch: BTreeMap::new(),
            version: build.into(),
            marketing_version: String::new(),
        }
    }
    #[test]
    fn universal_minimum_os_is_scoped_to_target_slice_and_plist() {
        let slices = [
            ("x86_64".into(), "13.0".into()),
            ("arm64".into(), "14.0".into()),
        ]
        .into();
        let mut dict = plist::Dictionary::new();
        let mut declared = plist::Dictionary::new();
        declared.insert("arm64".into(), "14.2".into());
        declared.insert("x86_64".into(), "12.0".into());
        dict.insert(
            "LSMinimumSystemVersionByArchitecture".into(),
            declared.into(),
        );
        let old = identity("1");
        let mut new = identity("2");
        new.minimum_os_by_arch = architecture_minimums(&slices, &dict);
        let check = |n: &Identity, os, arch| identities(&old, n, true, os, arch, None);
        assert!(check(&new, "13.0", "x86_64").is_ok());
        assert_eq!(
            check(&new, "13.0", "arm64").unwrap_err().code,
            "E_UPDATE_MIN_OS"
        );
        assert_eq!(
            check(&new, "14.1", "arm64").unwrap_err().code,
            "E_UPDATE_MIN_OS"
        );
        assert!(check(&new, "14.2", "arm64").is_ok());
        new.minimum_os = "15.0".into();
        assert_eq!(
            check(&new, "14.2", "arm64").unwrap_err().code,
            "E_UPDATE_MIN_OS"
        );
        new.architectures.remove("arm64");
        assert_eq!(
            check(&new, "16.0", "x86_64").unwrap_err().code,
            "E_UPDATE_ARCH"
        );
    }
    #[test]
    fn electron_relaunch_allows_prerelease_progress_and_rejects_downgrades() {
        let mut old = identity("100");
        old.marketing_version = "2.0.0-beta.1".into();
        let mut new = identity("1");
        for v in ["2.0.0-beta.2", "2.0.0"] {
            new.marketing_version = v.into();
            assert!(electron_versions(&old, &new).is_ok());
            assert!(electron_versions(&new, &old).is_err());
        }
    }
}

#[cfg(test)]
mod assessment_tests {
    use super::*;
    use crate::native_command::Output;
    #[test]
    fn explicit_denial_is_distinct_from_tool_failure_and_revocation() {
        let output = |code, stderr: &str| {
            Ok(Output {
                code,
                stdout: String::new(),
                stderr: stderr.into(),
            })
        };
        assert!(
            notarization_result(output(
                Some(0),
                "App: accepted\nsource=Notarized Developer ID"
            ))
            .unwrap()
        );
        assert!(
            !notarization_result(output(
                Some(3),
                "App: rejected\nsource=Unnotarized Developer ID"
            ))
            .unwrap()
        );
        for result in [
            Err(AppError::new("E_TIMEOUT", "command_timeout")),
            output(None, ""),
            output(Some(1), "service unavailable"),
            output(Some(3), "internal assessment error"),
        ] {
            assert_eq!(
                notarization_result(result).unwrap_err().code,
                "E_UPDATE_VERIFY_UNAVAILABLE"
            );
        }
        assert_eq!(
            notarization_result(output(Some(3), "certificate revoked"))
                .unwrap_err()
                .code,
            "E_UPDATE_SIGNATURE"
        );
    }
}
