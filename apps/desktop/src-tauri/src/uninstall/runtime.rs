//! Runtime fingerprints for installed application bundles.
//!
//! Electron applications are allowed to rename their main application and helper executables, so this module never
//! relies on a bundle name or bundle identifier. The framework path is the strongest macOS signal; `app.asar` alone
//! is retained as a useful but explicitly weak signal because other Chromium-based packagers can use ASAR too.

use super::{AppRuntime, RuntimeConfidence};
use std::{
    fs,
    path::{Path, PathBuf},
};

#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Detection {
    pub runtime: Option<AppRuntime>,
    pub confidence: Option<RuntimeConfidence>,
    pub evidence: Vec<String>,
}

impl Detection {
    fn electron(confidence: RuntimeConfidence, evidence: Vec<String>) -> Self {
        Self {
            runtime: Some(AppRuntime::Electron),
            confidence: Some(confidence),
            evidence,
        }
    }
}

/// Detect the runtime used to build one `.app` bundle.
pub fn detect(bundle: &Path) -> Detection {
    if !bundle.is_dir()
        || bundle
            .extension()
            .is_none_or(|extension| extension != "app")
    {
        return Detection::default();
    }

    let contents = bundle.join("Contents");
    let frameworks = contents.join("Frameworks");
    let resources = contents.join("Resources");
    let mut evidence = Vec::new();

    // Electron's default macOS distribution always embeds this framework. Check both the normal location and
    // nested framework directories because applications may wrap helpers or relocate the framework while signing.
    if let Some(path) = find_framework(&frameworks) {
        evidence.push(relative(bundle, &path));
        return Detection::electron(RuntimeConfidence::Confirmed, evidence);
    }

    let asar = resources.join("app.asar");
    let has_asar = asar.is_file();
    if has_asar {
        evidence.push(relative(bundle, &asar));
    }

    // A helper named by Electron is useful corroboration, but its presence alone is not enough: a third-party app
    // can ship a similarly named helper. Requiring an ASAR keeps this fallback conservative.
    let helpers = find_electron_helpers(&frameworks);
    evidence.extend(helpers.iter().map(|path| relative(bundle, path)));
    if has_asar && !helpers.is_empty() {
        return Detection::electron(RuntimeConfidence::High, evidence);
    }
    if has_asar {
        return Detection::electron(RuntimeConfidence::Possible, evidence);
    }

    Detection::default()
}

fn find_framework(frameworks: &Path) -> Option<PathBuf> {
    let direct = frameworks.join("Electron Framework.framework");
    if direct.is_dir() {
        return Some(direct);
    }
    let entries = fs::read_dir(frameworks).ok()?;
    for entry in entries.flatten() {
        let path = entry.path();
        if path.is_dir() {
            let nested = path.join("Contents/Frameworks/Electron Framework.framework");
            if nested.is_dir() {
                return Some(nested);
            }
            // Some wrappers place frameworks one level below a helper's Frameworks directory.
            let nested = path.join("Electron Framework.framework");
            if nested.is_dir() {
                return Some(nested);
            }
        }
    }
    None
}

fn find_electron_helpers(frameworks: &Path) -> Vec<PathBuf> {
    let Ok(entries) = fs::read_dir(frameworks) else {
        return Vec::new();
    };
    entries
        .flatten()
        .map(|entry| entry.path())
        .filter(|path| {
            path.extension().is_some_and(|extension| extension == "app")
                && path.file_stem().is_some_and(|name| {
                    name.to_string_lossy()
                        .to_ascii_lowercase()
                        .contains("electron helper")
                })
        })
        .collect()
}

fn relative(bundle: &Path, path: &Path) -> String {
    path.strip_prefix(bundle)
        .unwrap_or(path)
        .to_string_lossy()
        .replace(std::path::MAIN_SEPARATOR, "/")
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    fn bundle(root: &Path) -> PathBuf {
        root.join("Example.app")
    }

    #[test]
    fn confirms_standard_electron_framework() {
        let root = tempdir().unwrap();
        let app = bundle(root.path());
        fs::create_dir_all(app.join("Contents/Frameworks/Electron Framework.framework")).unwrap();
        fs::create_dir_all(app.join("Contents/Resources")).unwrap();
        fs::write(app.join("Contents/Resources/app.asar"), b"asar").unwrap();

        let result = detect(&app);
        assert_eq!(result.runtime, Some(AppRuntime::Electron));
        assert_eq!(result.confidence, Some(RuntimeConfidence::Confirmed));
        assert_eq!(
            result.evidence,
            vec!["Contents/Frameworks/Electron Framework.framework"]
        );
    }

    #[test]
    fn confirms_framework_nested_inside_an_electron_helper() {
        let root = tempdir().unwrap();
        let app = bundle(root.path());
        let framework = app.join(
            "Contents/Frameworks/Electron Helper.app/Contents/Frameworks/Electron Framework.framework",
        );
        fs::create_dir_all(framework).unwrap();

        let result = detect(&app);
        assert_eq!(result.confidence, Some(RuntimeConfidence::Confirmed));
        assert_eq!(
            result.evidence,
            vec![
                "Contents/Frameworks/Electron Helper.app/Contents/Frameworks/Electron Framework.framework"
            ]
        );
    }

    #[test]
    fn uses_helper_and_asar_as_high_confidence() {
        let root = tempdir().unwrap();
        let app = bundle(root.path());
        fs::create_dir_all(app.join("Contents/Frameworks/Electron Helper.app")).unwrap();
        fs::create_dir_all(app.join("Contents/Resources")).unwrap();
        fs::write(app.join("Contents/Resources/app.asar"), b"asar").unwrap();

        let result = detect(&app);
        assert_eq!(result.confidence, Some(RuntimeConfidence::High));
        assert!(
            result
                .evidence
                .iter()
                .any(|item| item.ends_with("app.asar"))
        );
        assert!(
            result
                .evidence
                .iter()
                .any(|item| item.ends_with("Electron Helper.app"))
        );
    }

    #[test]
    fn helper_without_an_asar_is_not_enough() {
        let root = tempdir().unwrap();
        let app = bundle(root.path());
        fs::create_dir_all(app.join("Contents/Frameworks/Electron Helper.app")).unwrap();

        assert_eq!(detect(&app), Detection::default());
    }

    #[test]
    fn app_asar_alone_is_only_possible() {
        let root = tempdir().unwrap();
        let app = bundle(root.path());
        fs::create_dir_all(app.join("Contents/Resources")).unwrap();
        fs::write(app.join("Contents/Resources/app.asar"), b"asar").unwrap();

        let result = detect(&app);
        assert_eq!(result.confidence, Some(RuntimeConfidence::Possible));
    }

    #[test]
    fn does_not_classify_chromium_without_electron_markers() {
        let root = tempdir().unwrap();
        let app = bundle(root.path());
        fs::create_dir_all(app.join("Contents/Resources")).unwrap();
        fs::write(app.join("Contents/Resources/resources.pak"), b"pak").unwrap();

        assert_eq!(detect(&app), Detection::default());
    }
}
