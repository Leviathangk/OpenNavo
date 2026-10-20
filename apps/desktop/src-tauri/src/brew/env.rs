use crate::model::{Locale, Settings};
use std::{collections::BTreeMap, path::Path};

const MESSAGE: &str = "HOMEBREW_OPENNAVO_ASKPASS_MESSAGE";

/// Why OpenNavo asks for the password. The dialog says so, instead of one fixed sentence that claims a Homebrew
/// install while an app is uninstalled or a startup item changed.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Reason {
    Install,
    Update,
    Uninstall,
    /// Undoing an uninstall: files go back to system locations.
    Restore,
    /// Deleting the files an uninstall kept for Undo.
    Purge,
    /// The Homebrew installer itself.
    Homebrew,
    Startup,
    Quarantine,
}

impl Reason {
    pub const ALL: [Self; 8] = [
        Self::Install,
        Self::Update,
        Self::Uninstall,
        Self::Restore,
        Self::Purge,
        Self::Homebrew,
        Self::Startup,
        Self::Quarantine,
    ];

    pub const fn key(self) -> &'static str {
        match self {
            Self::Install => "askpass.install",
            Self::Update => "askpass.update",
            Self::Uninstall => "askpass.uninstall",
            Self::Restore => "askpass.restore",
            Self::Purge => "askpass.purge",
            Self::Homebrew => "askpass.homebrew",
            Self::Startup => "askpass.startup",
            Self::Quarantine => "askpass.quarantine",
        }
    }

    /// These sentences carry the app's name.
    pub const fn names_app(self) -> bool {
        matches!(self, Self::Install | Self::Update | Self::Uninstall)
    }
}

/// A reason, with the app it is about when there is exactly one.
#[derive(Debug, Clone, Copy)]
pub struct Ask<'a> {
    pub reason: Reason,
    pub name: Option<&'a str>,
}

impl<'a> Ask<'a> {
    pub const fn new(reason: Reason) -> Self {
        Self { reason, name: None }
    }

    pub const fn named(reason: Reason, name: &'a str) -> Self {
        Self {
            reason,
            name: Some(name),
        }
    }
}

/// Puts the sentence for `ask` into the dialog text of `env`. A sentence that needs an app name keeps the neutral
/// text `construct` set when there is none, which at least claims nothing wrong.
pub fn explain(env: &mut BTreeMap<String, String>, locale: Locale, ask: Ask<'_>) {
    let name = ask
        .name
        .filter(|_| ask.reason.names_app())
        .and_then(dialog_name);
    if ask.reason.names_app() && name.is_none() {
        return;
    }
    if let Ok(text) = crate::native_i18n::text(locale, ask.reason.key()) {
        let text = match name {
            Some(name) => text.replace("{name}", &name),
            None => text,
        };
        env.insert(MESSAGE.into(), text);
    }
}

/// A name goes into dialog text: one line, without control or direction-changing characters, at most 60 characters.
fn dialog_name(name: &str) -> Option<String> {
    let flat: String = name
        .chars()
        .map(|c| {
            let unsafe_char = c.is_control()
                || matches!(c, '\u{200e}' | '\u{200f}' | '\u{202a}'..='\u{202e}' | '\u{2066}'..='\u{2069}');
            if unsafe_char { ' ' } else { c }
        })
        .collect();
    let flat = flat.split_whitespace().collect::<Vec<_>>().join(" ");
    if flat.is_empty() {
        None
    } else if flat.chars().count() <= 60 {
        Some(flat)
    } else {
        Some(format!(
            "{}…",
            flat.chars().take(59).collect::<String>().trim_end()
        ))
    }
}

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

    fn message(locale: Locale, ask: Option<Ask<'_>>) -> String {
        let settings = Settings {
            locale,
            ..Default::default()
        };
        let mut env = construct(&settings, "/opt/homebrew", Path::new("/tmp/helper"));
        if let Some(ask) = ask {
            explain(&mut env, locale, ask);
        }
        env[MESSAGE].clone()
    }

    #[test]
    fn every_reason_has_its_own_sentence_in_every_language() {
        for locale in crate::locales_gen::LOCALES {
            let neutral = message(locale, None);
            assert!(!neutral.contains('{'));
            let mut seen = vec![neutral.clone()];
            for reason in Reason::ALL {
                let text = message(locale, Some(Ask::named(reason, "Docker")));
                // A named sentence carries the name; the others must not keep a placeholder.
                assert_eq!(
                    text.contains("Docker"),
                    reason.names_app(),
                    "{locale:?}/{reason:?}"
                );
                assert!(!text.contains('{'), "{locale:?}/{reason:?}");
                // Only the Homebrew installer may say Homebrew: the old fixed text claimed it for everything.
                assert_eq!(
                    text.contains("Homebrew"),
                    reason == Reason::Homebrew,
                    "{locale:?}/{reason:?}"
                );
                assert!(
                    !seen.contains(&text),
                    "{locale:?}/{reason:?} repeats a sentence"
                );
                seen.push(text);
            }
            assert!(!neutral.contains("Homebrew"), "{locale:?}");
        }
    }

    #[test]
    fn a_sentence_that_needs_a_name_stays_neutral_without_one() {
        for locale in crate::locales_gen::LOCALES {
            let neutral = message(locale, None);
            for reason in Reason::ALL.into_iter().filter(|r| r.names_app()) {
                assert_eq!(message(locale, Some(Ask::new(reason))), neutral);
                assert_eq!(message(locale, Some(Ask::named(reason, " \n\t "))), neutral);
            }
            // A name given to a sentence that has no place for it changes nothing.
            assert_eq!(
                message(locale, Some(Ask::named(Reason::Startup, "Docker"))),
                message(locale, Some(Ask::new(Reason::Startup)))
            );
        }
    }

    #[test]
    fn dialog_names_are_one_short_line() {
        assert_eq!(
            dialog_name("Visual Studio Code").as_deref(),
            Some("Visual Studio Code")
        );
        assert_eq!(
            dialog_name("  Evil\nApp\u{202e}gnp.exe\t").as_deref(),
            Some("Evil App gnp.exe")
        );
        assert_eq!(dialog_name("微信").as_deref(), Some("微信"));
        assert_eq!(dialog_name("\u{200e}\u{0}\n"), None);
        let long = "漢".repeat(80);
        let shortened = dialog_name(&long).unwrap();
        assert_eq!(shortened.chars().count(), 60);
        assert!(shortened.ends_with('…'));
        assert_eq!(dialog_name(&"a".repeat(60)).unwrap().chars().count(), 60);
        // A name cannot smuggle in a placeholder-like sentence that a later replace would expand.
        let mut env = BTreeMap::new();
        explain(
            &mut env,
            Locale::EnUs,
            Ask::named(Reason::Uninstall, "{name}"),
        );
        assert!(env[MESSAGE].contains("“{name}”"));
    }
}
