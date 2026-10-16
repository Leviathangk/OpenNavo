use crate::model::Settings;
use std::{collections::BTreeMap, path::Path};

pub fn construct(settings: &Settings, prefix: &str, askpass: &Path) -> BTreeMap<String, String> {
    let mut env = BTreeMap::new();
    for key in ["HOME", "USER", "LOGNAME", "SHELL", "TMPDIR"] {
        if let Ok(value) = std::env::var(key) {
            env.insert(key.into(), value);
        }
    }
    for (key, value) in [
        (
            "PATH",
            format!("{prefix}/bin:{prefix}/sbin:/usr/bin:/bin:/usr/sbin:/sbin"),
        ),
        ("LANG", "en_US.UTF-8".into()),
        ("HOMEBREW_NO_AUTO_UPDATE", "1".into()),
        ("HOMEBREW_NO_ENV_HINTS", "1".into()),
        ("HOMEBREW_NO_COLOR", "1".into()),
        ("HOMEBREW_NO_EMOJI", "1".into()),
        ("COLUMNS", "120".into()),
        ("LINES", "40".into()),
        ("SUDO_ASKPASS", askpass.to_string_lossy().into_owned()),
        ("OPENNAVO_LOCALE", settings.locale.as_str().into()),
    ] {
        env.insert(key.into(), value);
    }
    for (suffix, key) in [
        ("MESSAGE", "askpass.message"),
        ("TITLE", "askpass.title"),
        ("OK", "askpass.ok"),
        ("CANCEL", "askpass.cancel"),
    ] {
        // Build checks ensure embedded resources are complete; do not inherit caller dialog text.
        // bin/brew re-execs with only HOMEBREW_* plus a fixed allowlist, and brew's sudo hands the helper
        // what is left, so any other name leaves the helper without text and no dialog appears.
        if let Ok(text) = crate::native_i18n::text(settings.locale, key) {
            env.insert(format!("HOMEBREW_OPENNAVO_ASKPASS_{suffix}"), text);
        }
    }
    if settings.homebrew_analytics == Some(false) {
        env.insert("HOMEBREW_NO_ANALYTICS".into(), "1".into());
    }
    for (key, value) in crate::mirror::variables(&settings.mirror) {
        env.insert(key.into(), value.into());
    }
    env
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn whitelist_does_not_inherit_credentials() {
        let env = construct(
            &Settings::default(),
            "/opt/homebrew",
            Path::new("/tmp/helper"),
        );
        assert_eq!(env["HOMEBREW_NO_AUTO_UPDATE"], "1");
        assert_eq!(
            env["PATH"],
            "/opt/homebrew/bin:/opt/homebrew/sbin:/usr/bin:/bin:/usr/sbin:/sbin"
        );
        assert!(!env.contains_key("LLM_API_KEY"));
        assert!(!env.contains_key("GITHUB_TOKEN"));
        assert!(!env.contains_key("HOMEBREW_NO_ANALYTICS"));
        let env = construct(
            &Settings {
                homebrew_analytics: Some(false),
                ..Default::default()
            },
            "/tmp",
            Path::new("/tmp/helper"),
        );
        assert_eq!(env["HOMEBREW_NO_ANALYTICS"], "1");
    }

    #[test]
    fn askpass_text_survives_homebrew_env_filter() {
        // bin/brew keeps HOMEBREW_* plus an allowlist that includes SUDO_ASKPASS; brew's sudo runs the helper with that.
        let env = construct(
            &Settings::default(),
            "/opt/homebrew",
            Path::new("/tmp/helper"),
        );
        let script = std::fs::read_to_string(
            Path::new(env!("CARGO_MANIFEST_DIR")).join("resources/askpass/opennavo-askpass"),
        )
        .unwrap();
        let required: Vec<&str> = script
            .split("${")
            .skip(1)
            .filter_map(|rest| rest.split_once('}')?.0.strip_suffix(":?"))
            .collect();
        assert_eq!(required.len(), 4);
        for name in required {
            assert!(name.starts_with("HOMEBREW_"), "bin/brew drops {name}");
            assert!(!env[name].trim().is_empty());
        }
        assert_eq!(env["SUDO_ASKPASS"], "/tmp/helper");
    }
}
