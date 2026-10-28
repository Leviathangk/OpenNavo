use opennavo_desktop_lib::{brew::env::construct, core::DesktopCore, mirror, model::*, settings};
use std::{path::Path, sync::Arc, time::Duration};
fn choice() -> MirrorChoice {
    MirrorChoice {
        key: "ustc".into(),
        api_domain: Some("https://mirrors.ustc.edu.cn/homebrew-bottles/api".into()),
        bottle_domain: Some("https://mirrors.ustc.edu.cn/homebrew-bottles".into()),
        brew_git_remote: Some("https://mirrors.ustc.edu.cn/brew.git".into()),
        core_git_remote: Some("https://mirrors.ustc.edu.cn/homebrew-core.git".into()),
    }
}
fn input(key: &str, url: String) -> MirrorInput {
    MirrorInput {
        key: key.into(),
        name: key.into(),
        probe_url: url,
        api_domain: None,
        bottle_domain: None,
        brew_git_remote: None,
        core_git_remote: None,
    }
}
#[test]
fn injection_terminal_quoting_and_official_reset() {
    let settings = Settings {
        mirror: choice(),
        homebrew_analytics: Some(false),
        ..Settings::default()
    };
    let env = construct(&settings, "/tmp/own_prefix", Path::new("/tmp/askpass"));
    for (key, value) in mirror::variables(&settings.mirror) {
        assert_eq!(env[key], value);
    }
    assert_eq!(env["HOMEBREW_NO_AUTO_UPDATE"], "1");
    assert_eq!(env["HOMEBREW_NO_ANALYTICS"], "1");
    let text = mirror::terminal_environment(&settings.mirror).unwrap();
    assert_eq!(text.lines().count(), 4);
    assert!(
        text.lines()
            .all(|line| line.starts_with("export HOMEBREW_"))
    );
    assert!(
        text.contains(
            "export HOMEBREW_API_DOMAIN='https://mirrors.ustc.edu.cn/homebrew-bottles/api'"
        )
    );
    let mut hostile = choice();
    hostile.api_domain = Some("https://mirror.example/api?q='value'`uname`".into());
    let text = mirror::terminal_environment(&hostile).unwrap();
    assert!(text.contains("'\\''value'\\''`uname`'"));
    let official = Settings {
        mirror: MirrorChoice {
            key: "official".into(),
            ..choice()
        },
        ..Settings::default()
    };
    let normalized = settings::validate(official).unwrap();
    assert!(normalized.mirror.api_domain.is_none());
    let env = construct(&normalized, "/tmp", Path::new("/tmp/askpass"));
    for (key, _) in mirror::VARIABLES {
        assert!(!env.contains_key(key));
    }
    assert_eq!(
        mirror::terminal_environment(&normalized.mirror).unwrap(),
        "unset HOMEBREW_API_DOMAIN\nunset HOMEBREW_BOTTLE_DOMAIN\nunset HOMEBREW_BREW_GIT_REMOTE\nunset HOMEBREW_CORE_GIT_REMOTE"
    );
    for url in [
        "file:///etc/passwd",
        "ftp://example.test",
        "https://user:password@example.test",
        "https://example.test/#fragment",
        "https://example.test/\n",
    ] {
        assert!(mirror::validate_url(url).is_err());
    }
}
#[tokio::test]
async fn four_head_results_status_timeout_and_concurrency_limit() {
    use std::sync::atomic::{AtomicUsize, Ordering};
    use tokio::{
        io::{AsyncReadExt, AsyncWriteExt},
        net::TcpListener,
    };
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let base = format!("http://{}", listener.local_addr().unwrap());
    let active = Arc::new(AtomicUsize::new(0));
    let max = Arc::new(AtomicUsize::new(0));
    let saw_head = Arc::new(AtomicUsize::new(0));
    let count = saw_head.clone();
    let maximum = max.clone();
    let server = tokio::spawn(async move {
        loop {
            let (mut stream, _) = listener.accept().await.unwrap();
            let active = active.clone();
            let maximum = maximum.clone();
            let count = count.clone();
            tokio::spawn(async move {
                let now = active.fetch_add(1, Ordering::SeqCst) + 1;
                maximum.fetch_max(now, Ordering::SeqCst);
                let mut bytes = [0u8; 4096];
                let size = stream.read(&mut bytes).await.unwrap_or(0);
                let request = String::from_utf8_lossy(&bytes[..size]);
                if request.starts_with("HEAD ") {
                    count.fetch_add(1, Ordering::SeqCst);
                }
                let timeout = request.contains("/slow ");
                let bad = request.contains("/bad ");
                tokio::time::sleep(Duration::from_millis(if timeout { 300 } else { 10 })).await;
                let code = if bad { 404 } else { 200 };
                let _ = stream
                    .write_all(
                        format!(
                            "HTTP/1.1 {code} Test\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
                        )
                        .as_bytes(),
                    )
                    .await;
                active.fetch_sub(1, Ordering::SeqCst);
            });
        }
    });
    let results = mirror::probe_with_timeout(
        vec![
            input("official", format!("{base}/ok")),
            input("tuna", format!("{base}/ok")),
            input("ustc", format!("{base}/bad")),
            input("aliyun", format!("{base}/slow")),
        ],
        Duration::from_millis(100),
    )
    .await
    .unwrap();
    assert_eq!(results.len(), 4);
    assert_eq!(
        results.iter().map(|i| i.key.as_str()).collect::<Vec<_>>(),
        ["official", "tuna", "ustc", "aliyun"]
    );
    assert!(results[0].ok && results[1].ok);
    assert_eq!(results[2].status, Some(404));
    assert_eq!(results[2].error.as_deref(), Some("http_404"));
    assert_eq!(results[3].error.as_deref(), Some("timeout"));
    assert!(results[3].latency_ms.is_none());
    tokio::time::sleep(Duration::from_millis(220)).await;
    max.store(0, Ordering::SeqCst);
    let values = (0..12)
        .map(|n| input(&format!("mirror{n}"), format!("{base}/ok")))
        .collect();
    assert!(mirror::probe(values).await.unwrap().iter().all(|i| i.ok));
    assert_eq!(max.load(Ordering::SeqCst), 4);
    assert_eq!(saw_head.load(Ordering::SeqCst), 16);
    assert!(
        mirror::probe(vec![
            input("duplicate", format!("{base}/ok")),
            input("duplicate", format!("{base}/ok"))
        ])
        .await
        .is_err()
    );
    server.abort();
    let _ = server.await;
}
#[test]
fn settings_defaults_file_schema_permissions_and_validation() {
    let tmp = tempfile::tempdir().unwrap();
    let path = tmp.path().join("settings.json");
    let first = settings::load(&path).unwrap();
    assert_eq!(
        first.locale,
        settings::locale(tauri_plugin_os::locale().as_deref())
    );
    std::fs::write(&path, r#"{"locale":"en-US","includeGreedy":false}"#).unwrap();
    let partial = settings::load(&path).unwrap();
    assert_eq!(partial.locale, Locale::EnUs);
    assert!(!partial.include_greedy);
    assert_eq!(partial.check_time, "09:00");
    assert_eq!(partial.mirror.key, "official");
    let file: serde_json::Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    assert!(file.get("settings").is_none());
    assert_eq!(file["locale"], "en-US");
    assert!(file.get("launchAtLogin").is_some());
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        assert_eq!(
            std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o600
        );
    }
    for time in ["9:00", "24:00", "12:60", "99:99", "00:0a", "", " 09:00"] {
        assert!(
            settings::validate(Settings {
                check_time: time.into(),
                ..Settings::default()
            })
            .is_err()
        );
    }
    for time in ["00:00", "09:00", "23:59"] {
        assert!(
            settings::validate(Settings {
                check_time: time.into(),
                ..Settings::default()
            })
            .is_ok()
        );
    }
    assert!(
        settings::validate(Settings {
            brew_path: Some("relative/brew".into()),
            ..Settings::default()
        })
        .is_err()
    );
    assert_eq!(settings::locale(Some("zh-Hans-CN")), Locale::ZhCn);
    assert_eq!(settings::locale(Some("en-US")), Locale::EnUs);
}
#[tokio::test]
async fn replacement_persists_offline_full_mirror_and_failed_save_preserves_previous() {
    let tmp = tempfile::tempdir().unwrap();
    let events = Arc::new(std::sync::Mutex::new(Vec::new()));
    let record = events.clone();
    let core = DesktopCore::open(
        tmp.path().join("data"),
        tmp.path().join("cache"),
        tmp.path().join("askpass"),
        Arc::new(move |e| record.lock().unwrap().push(e)),
    )
    .unwrap();
    let chosen = Settings {
        // Keep this persistence test independent of system-language onboarding migration.
        onboarding_completed: true,
        brew_path: Some(
            Path::new(env!("CARGO_MANIFEST_DIR"))
                .join("tests/fake-brew/brew")
                .to_string_lossy()
                .into(),
        ),
        mirror: choice(),
        locale: Locale::EnUs,
        locale_mode: opennavo_desktop_lib::model::LocaleMode::Manual,
        homebrew_analytics: Some(false),
        check_time: "10:05".into(),
        ..Settings::default()
    };
    core.replace_settings(chosen.clone()).await.unwrap();
    assert_eq!(core.current_settings().unwrap(), chosen);
    assert!(
        events
            .lock()
            .unwrap()
            .iter()
            .any(|e| matches!(e, opennavo_desktop_lib::events::CoreEvent::Env(_)))
    );
    tokio::time::timeout(Duration::from_millis(100), core.settings_changed.notified())
        .await
        .unwrap();
    let reloaded = DesktopCore::open(
        tmp.path().join("data"),
        tmp.path().join("cache"),
        tmp.path().join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    assert_eq!(reloaded.current_settings().unwrap(), chosen);
    core.install_settings_save(Arc::new(|_| {
        Err(opennavo_desktop_lib::AppError::new(
            "E_UNKNOWN",
            "save_failed",
        ))
    }))
    .unwrap();
    let mut updated = chosen.clone();
    updated.check_time = "11:00".into();
    assert!(core.replace_settings(updated).await.is_err());
    assert_eq!(core.current_settings().unwrap(), chosen);
}

#[tokio::test]
async fn login_state_rolls_back_when_settings_save_fails() {
    use std::sync::atomic::{AtomicBool, Ordering};
    let tmp = tempfile::tempdir().unwrap();
    let core = DesktopCore::open(
        tmp.path().join("data"),
        tmp.path().join("cache"),
        tmp.path().join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    let login = Arc::new(AtomicBool::new(false));
    let track = login.clone();
    let change: opennavo_desktop_lib::settings::Autostart = Arc::new(move |enabled| {
        track.store(enabled, Ordering::SeqCst);
        Ok(())
    });
    let mut settings = core.current_settings().unwrap();
    settings.launch_at_login = true;
    core.install_settings_save(Arc::new(|_| {
        Err(opennavo_desktop_lib::AppError::new(
            "E_UNKNOWN",
            "disk_full",
        ))
    }))
    .unwrap();
    assert!(
        core.replace_settings_with(settings, Some(change))
            .await
            .is_err()
    );
    assert!(!login.load(Ordering::SeqCst));
    assert!(!core.current_settings().unwrap().launch_at_login);
}

#[test]
fn settings_round_trip_in_every_language() {
    for (system, locale) in [
        ("zh_Hans_CN", Locale::ZhCn),
        ("ja-JP", Locale::JaJp),
        ("es-ES", Locale::EsEs),
        ("pt_BR", Locale::PtBr),
        ("ru-RU", Locale::RuRu),
        ("de-DE", Locale::DeDe),
        ("fr_CA", Locale::FrFr),
        ("it-IT", Locale::ItIt),
        ("zh_Hant_TW", Locale::ZhHant),
        ("nl-NL", Locale::EnUs),
    ] {
        assert_eq!(settings::locale(Some(system)), locale);
        let input = format!(r#"{{"locale":"{}"}}"#, locale.as_str());
        let parsed: Settings = serde_json::from_str(&input).unwrap();
        assert_eq!(parsed.locale, locale);
        assert_eq!(
            serde_json::to_value(&parsed).unwrap()["locale"],
            locale.as_str()
        );
        assert!(
            !opennavo_desktop_lib::native_i18n::text(locale, "settings")
                .unwrap()
                .is_empty()
        );
    }
}

#[test]
fn appearance_round_trips_and_unknown_values_default_to_dark() {
    assert_eq!(Settings::default().appearance, Appearance::Dark);
    for (appearance, code) in [
        (Appearance::System, "system"),
        (Appearance::Light, "light"),
        (Appearance::Dark, "dark"),
    ] {
        let settings = Settings {
            appearance,
            ..Settings::default()
        };
        let json = serde_json::to_value(&settings).unwrap();
        assert_eq!(json["appearance"], code);
        assert_eq!(serde_json::from_value::<Settings>(json).unwrap(), settings);
    }
    // Settings saved before the preference existed default to dark.
    let legacy: Settings = serde_json::from_str(r#"{"includeGreedy":false}"#).unwrap();
    assert_eq!(legacy.appearance, Appearance::Dark);
    assert!(!legacy.include_greedy);
    // A value from a newer build, or a malformed one, never stops the other settings from loading.
    for value in [
        serde_json::json!("auto"),
        serde_json::json!("Dark"),
        serde_json::json!(""),
        serde_json::json!(null),
        serde_json::json!(1),
        serde_json::json!(true),
        serde_json::json!(["dark"]),
        serde_json::json!({"mode": "dark"}),
    ] {
        let parsed: Settings = serde_json::from_value(serde_json::json!({
            "appearance": value, "includeGreedy": false, "checkTime": "10:30"
        }))
        .unwrap();
        assert_eq!(parsed.appearance, Appearance::Dark, "{value}");
        assert!(!parsed.include_greedy);
        assert_eq!(parsed.check_time, "10:30");
    }
    // The same holds for the startup load, which rewrites the file with the fallback.
    let tmp = tempfile::tempdir().unwrap();
    let path = tmp.path().join("settings.json");
    std::fs::write(
        &path,
        r#"{"appearance":"auto","includeGreedy":false,"checkTime":"10:30"}"#,
    )
    .unwrap();
    let loaded = settings::load(&path).unwrap();
    assert_eq!(loaded.appearance, Appearance::Dark);
    assert!(!loaded.include_greedy);
    assert_eq!(loaded.check_time, "10:30");
    let file: serde_json::Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    assert_eq!(file["appearance"], "dark");
    std::fs::write(&path, r#"{"appearance":"light"}"#).unwrap();
    assert_eq!(settings::load(&path).unwrap().appearance, Appearance::Light);
}

fn custom() -> MirrorInput {
    MirrorInput {
        key: "custom-local".into(),
        name: "Local mirror".into(),
        probe_url: "https://ignored.example".into(),
        api_domain: Some("https://mirror.example/api///".into()),
        bottle_domain: None,
        brew_git_remote: None,
        core_git_remote: None,
    }
}

#[test]
fn custom_mirrors_are_local_persistent_and_active_addresses_follow_edits() {
    let tmp = tempfile::tempdir().unwrap();
    let path = tmp.path().join("settings.json");
    let local = custom();
    let normalized = settings::validate(Settings {
        custom_mirrors: vec![local.clone()],
        mirror: mirror::choice(&local),
        ..Settings::default()
    })
    .unwrap();
    assert_eq!(
        normalized.custom_mirrors[0].probe_url,
        "https://mirror.example/api/cask.jws.json"
    );
    assert_eq!(
        normalized.mirror.api_domain.as_deref(),
        Some("https://mirror.example/api")
    );
    settings::save_file(&path, &normalized).unwrap();
    let mut reloaded = settings::load(&path).unwrap();
    assert_eq!(reloaded.custom_mirrors, normalized.custom_mirrors);
    assert_eq!(reloaded.mirror, normalized.mirror);
    reloaded.custom_mirrors[0].api_domain = Some("https://new.example/api".into());
    reloaded = settings::validate(reloaded).unwrap();
    assert_eq!(
        reloaded.mirror.api_domain.as_deref(),
        Some("https://new.example/api")
    );
    assert_eq!(mirror::variables(&reloaded.mirror).len(), 1);
    let env = construct(&reloaded, "/tmp", Path::new("/tmp/askpass"));
    assert!(!env.contains_key("HOMEBREW_BOTTLE_DOMAIN"));
    reloaded.custom_mirrors.clear();
    assert!(settings::validate(reloaded.clone()).is_err());
    reloaded.mirror = Settings::default().mirror;
    assert!(settings::validate(reloaded).is_ok());
    let legacy: Settings = serde_json::from_str(r#"{"mirror":{"key":"official"}}"#).unwrap();
    assert!(legacy.custom_mirrors.is_empty());
}

#[test]
fn invalid_custom_mirrors_cannot_be_persisted() {
    for address in [
        "file:///tmp/mirror",
        "https://user:password@example.test",
        "https://example.test/#fragment",
        "https://example.test/\n",
        "",
    ] {
        let mut local = custom();
        local.api_domain = Some(address.into());
        assert!(mirror::normalize_custom(local).is_err(), "{address:?}");
    }
    for name in ["", "   ", "Mirror\n"] {
        let mut local = custom();
        local.name = name.into();
        assert!(mirror::normalize_custom(local).is_err());
    }
    let mut local = custom();
    local.key = "official".into();
    assert!(mirror::normalize_custom(local).is_err());
    for custom_mirrors in [
        vec![custom(); 2],
        (0..21)
            .map(|n| MirrorInput {
                key: format!("custom-{n}"),
                ..custom()
            })
            .collect(),
    ] {
        assert!(
            settings::validate(Settings {
                custom_mirrors,
                ..Settings::default()
            })
            .is_err()
        );
    }
    let oversized = (0..20)
        .map(|n| MirrorInput {
            key: format!("custom-{n}"),
            bottle_domain: Some(format!("https://example.test/{}", "x".repeat(3900))),
            ..custom()
        })
        .collect();
    assert!(
        settings::validate(Settings {
            custom_mirrors: oversized,
            ..Settings::default()
        })
        .is_err()
    );
}

#[test]
fn custom_api_base_persistence_and_brew_environment_match_probe_requests() {
    let contract: serde_json::Value = serde_json::from_str(include_str!(
        "../../tests/fixtures/custom-mirror-probes.json"
    ))
    .unwrap();
    let tmp = tempfile::tempdir().unwrap();
    let path = tmp.path().join("settings.json");
    for example in contract["apiBases"].as_array().unwrap() {
        let source = MirrorInput {
            api_domain: Some(example["input"].as_str().unwrap().into()),
            ..custom()
        };
        let normalized = settings::validate(Settings {
            mirror: mirror::choice(&source),
            custom_mirrors: vec![source],
            ..Settings::default()
        })
        .unwrap();
        settings::save_file(&path, &normalized).unwrap();
        let reloaded = settings::load(&path).unwrap();
        let env = construct(&reloaded, "/tmp", Path::new("/tmp/askpass"));
        let base = &env["HOMEBREW_API_DOMAIN"];
        assert_eq!(base, example["normalized"].as_str().unwrap());
        assert_eq!(reloaded.custom_mirrors[0].api_domain.as_ref(), Some(base));
        assert_eq!(
            reloaded.custom_mirrors[0].probe_url,
            example["probeUrl"].as_str().unwrap()
        );
        // Homebrew::API.fetch builds this same string in Library/Homebrew/api.rb.
        assert_eq!(
            format!("{base}/cask.jws.json"),
            reloaded.custom_mirrors[0].probe_url
        );
    }
    for api in contract["invalidApiBases"].as_array().unwrap() {
        let source = MirrorInput {
            api_domain: Some(api.as_str().unwrap().into()),
            ..custom()
        };
        assert!(
            settings::validate(Settings {
                mirror: mirror::choice(&source),
                custom_mirrors: vec![source],
                ..Settings::default()
            })
            .is_err()
        );
    }
}
