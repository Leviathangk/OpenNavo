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
    assert_eq!(declared.len(), 6);
    for (locale, code) in [
        (Locale::EnUs, "en"),
        (Locale::ZhCn, "zh-Hans"),
        (Locale::JaJp, "ja"),
        (Locale::EsEs, "es"),
        (Locale::PtBr, "pt-BR"),
        (Locale::RuRu, "ru"),
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
            if [Locale::ZhCn, Locale::JaJp].contains(&locale) {
                "、"
            } else {
                ", "
            }
        );
    }
    assert_eq!(native_i18n::plural_index(Locale::EnUs, 0), 1);
    assert_eq!(native_i18n::plural_index(Locale::PtBr, 0), 0);
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
fn six_language_menu_labels_and_askpass_env() {
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
}
