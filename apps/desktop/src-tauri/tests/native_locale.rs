use opennavo_desktop_lib::{model::*, native_i18n, settings};
#[test]
fn apple_language_mapping_matches_bundle_localizations() {
    let plist = plist::Value::from_file(
        std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("Info.plist"),
    )
    .unwrap();
    let declared = plist.as_dictionary().unwrap()["CFBundleLocalizations"]
        .as_array()
        .unwrap();
    assert_eq!(
        declared.len(),
        opennavo_desktop_lib::locales_gen::LOCALES.len()
    );
    for (locale, code) in [
        (Locale::EnUs, "en"),
        (Locale::ZhCn, "zh-Hans"),
        (Locale::JaJp, "ja"),
        (Locale::EsEs, "es"),
        (Locale::PtBr, "pt-BR"),
        (Locale::RuRu, "ru"),
        (Locale::DeDe, "de"),
        (Locale::FrFr, "fr"),
        (Locale::ItIt, "it"),
        (Locale::ZhHant, "zh-Hant"),
    ] {
        assert_eq!(settings::apple_language(locale), code);
        assert!(declared.iter().any(|v| v.as_string() == Some(code)));
    }
}
#[test]
fn legacy_manual_and_system_resolution() {
    assert_eq!(
        settings::legacy_mode(Locale::JaJp, Locale::JaJp),
        LocaleMode::System
    );
    assert_eq!(
        settings::legacy_mode(Locale::JaJp, Locale::EnUs),
        LocaleMode::Manual
    );
    let settings = Settings {
        locale: Locale::JaJp,
        locale_mode: LocaleMode::System,
        ..Settings::default()
    };
    assert_eq!(
        settings::resolve(settings, Locale::RuRu).locale,
        Locale::RuRu
    );
    let settings = Settings {
        locale: Locale::JaJp,
        locale_mode: LocaleMode::Manual,
        ..Settings::default()
    };
    assert_eq!(
        settings::resolve(settings, Locale::RuRu).locale,
        Locale::JaJp
    );
    let legacy: Settings = serde_json::from_str(r#"{"locale":"ja-JP"}"#).unwrap();
    assert_eq!(
        legacy.locale_mode,
        settings::legacy_mode(Locale::JaJp, settings::system_locale())
    );
    assert!(
        serde_json::to_value(legacy)
            .unwrap()
            .get("localeMode")
            .is_some()
    );
}
#[test]
fn integer_plural_zero_one_two_many_and_punctuation() {
    for locale in opennavo_desktop_lib::locales_gen::LOCALES {
        for count in [0, 1, 2, 5, 11, 21, 22, 25, 101, 111, 112, u32::MAX] {
            let text = native_i18n::plural(locale, "available", count).unwrap();
            assert!(text.contains(&count.to_string()));
            assert!(!text.contains('|'));
        }
        let separator = native_i18n::text(locale, "separator").unwrap();
        assert_eq!(
            separator,
            if [Locale::ZhCn, Locale::ZhHant, Locale::JaJp].contains(&locale) {
                "、"
            } else {
                ", "
            }
        );
    }
    assert_eq!(native_i18n::plural_index(Locale::EnUs, 0), 1);
    assert_eq!(native_i18n::plural_index(Locale::DeDe, 0), 1);
    assert_eq!(native_i18n::plural_index(Locale::ItIt, 1), 0);
    // French and Portuguese treat zero as singular.
    assert_eq!(native_i18n::plural_index(Locale::PtBr, 0), 0);
    assert_eq!(native_i18n::plural_index(Locale::FrFr, 0), 0);
    assert_eq!(native_i18n::plural_index(Locale::FrFr, 2), 1);
    assert_eq!(native_i18n::plural_index(Locale::ZhHant, 2), 0);
    for (count, index) in [
        (1, 0),
        (2, 1),
        (5, 2),
        (11, 2),
        (21, 0),
        (22, 1),
        (25, 2),
        (111, 2),
        (112, 2),
    ] {
        assert_eq!(native_i18n::plural_index(Locale::RuRu, count), index);
    }
}

#[test]
fn menu_labels_and_askpass_env_in_every_language() {
    use opennavo_desktop_lib::brew::env::construct;
    for locale in opennavo_desktop_lib::locales_gen::LOCALES {
        for key in [
            "menu.about",
            "menu.services",
            "menu.hide",
            "menu.hideOthers",
            "menu.quit",
            "menu.file",
            "menu.close",
            "menu.edit",
            "menu.undo",
            "menu.redo",
            "menu.cut",
            "menu.copy",
            "menu.paste",
            "menu.selectAll",
            "menu.view",
            "menu.fullscreen",
            "menu.window",
            "menu.minimize",
            "menu.maximize",
            "menu.help",
        ] {
            assert!(!native_i18n::text(locale, key).unwrap().trim().is_empty());
        }
        let env = construct(
            &Settings {
                locale,
                locale_mode: LocaleMode::Manual,
                ..Settings::default()
            },
            "/opt/homebrew",
            std::path::Path::new("/unused/askpass"),
        );
        for (suffix, key) in [
            ("MESSAGE", "askpass.message"),
            ("TITLE", "askpass.title"),
            ("OK", "askpass.ok"),
            ("CANCEL", "askpass.cancel"),
        ] {
            assert_eq!(
                env[&format!("HOMEBREW_OPENNAVO_ASKPASS_{suffix}")],
                native_i18n::text(locale, key).unwrap()
            );
        }
    }
    let script = std::fs::read_to_string(
        std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("resources/askpass/opennavo-askpass"),
    )
    .unwrap();
    assert!(!script.contains("Mac login password"));
    assert!(!script.contains("ja-JP"));
    assert!(script.contains("with title (item 2 of argv)"));
    assert!(script.contains("with hidden answer"));
    // OpenNavo's own icon from the bundle, and a dialog nobody answers must give up like Cancel.
    assert!(script.contains("icon.icns"));
    assert!(script.contains("giving up after"));
    assert!(script.contains("error \"User canceled.\" number -128"));
}

#[test]
fn askpass_sentences_exist_in_every_language_with_a_name_only_where_expected() {
    use opennavo_desktop_lib::brew::env::Reason;
    for locale in opennavo_desktop_lib::locales_gen::LOCALES {
        for reason in Reason::ALL {
            let text = native_i18n::text(locale, reason.key()).unwrap();
            assert!(!text.trim().is_empty(), "{locale:?}/{reason:?}");
            assert_eq!(
                text.matches("{name}").count(),
                usize::from(reason.names_app()),
                "{locale:?}/{reason:?}"
            );
        }
    }
}

#[test]
fn bundle_ships_a_localized_usage_description_for_every_declared_language() {
    let manifest = std::path::Path::new(env!("CARGO_MANIFEST_DIR"));
    let plist = plist::Value::from_file(manifest.join("Info.plist")).unwrap();
    let info = plist.as_dictionary().unwrap();
    let base = info["NSAppleEventsUsageDescription"].as_string().unwrap();
    let config: serde_json::Value =
        serde_json::from_str(&std::fs::read_to_string(manifest.join("tauri.conf.json")).unwrap())
            .unwrap();
    let files = config["bundle"]["macOS"]["files"].as_object().unwrap();
    let mut seen = std::collections::HashSet::new();
    for code in info["CFBundleLocalizations"].as_array().unwrap() {
        let code = code.as_string().unwrap();
        // Declared in Info.plist, so macOS looks for it: it must really be in the bundle.
        let folder = format!("{code}.lproj");
        assert_eq!(
            files[&format!("Resources/{folder}")],
            format!("resources/lproj/{folder}"),
            "{code} is not copied into the bundle"
        );
        let strings = std::fs::read_to_string(
            manifest
                .join("resources/lproj")
                .join(&folder)
                .join("InfoPlist.strings"),
        )
        .unwrap();
        let line = strings
            .lines()
            .find(|line| line.starts_with("\"NSAppleEventsUsageDescription\" = \""))
            .unwrap_or_else(|| panic!("{code} has no usage description"));
        let text = line
            .trim_end_matches(';')
            .trim_end_matches('"')
            .strip_prefix("\"NSAppleEventsUsageDescription\" = \"")
            .unwrap();
        assert!(!text.is_empty() && text.contains("System Events"), "{code}");
        assert!(
            seen.insert(text.to_owned()),
            "{code} repeats another language"
        );
        if code == "en" {
            assert_eq!(text, base);
        }
    }
    assert_eq!(seen.len(), opennavo_desktop_lib::locales_gen::LOCALES.len());
}
