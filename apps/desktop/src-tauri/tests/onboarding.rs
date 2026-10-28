use opennavo_desktop_lib::{
    core::DesktopCore,
    model::{Locale, Settings},
    settings,
};
use std::sync::Arc;

#[test]
fn new_data_directory_starts_with_incomplete_onboarding() {
    let tmp = tempfile::tempdir().unwrap();
    let path = tmp.path().join("data/settings.json");
    assert!(!path.exists());
    assert!(
        !settings::load_with_locale(&path, Locale::ZhCn)
            .unwrap()
            .onboarding_completed
    );
    assert!(
        !settings::load_with_locale(&path, Locale::ZhCn)
            .unwrap()
            .onboarding_completed
    );
    let saved: serde_json::Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    assert_eq!(saved["onboardingCompleted"], false);
}

#[test]
fn other_languages_skip_welcome() {
    let tmp = tempfile::tempdir().unwrap();
    let path = tmp.path().join("data/settings.json");
    assert!(
        settings::load_with_locale(&path, Locale::EnUs)
            .unwrap()
            .onboarding_completed
    );
    let saved: serde_json::Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    assert_eq!(saved["onboardingCompleted"], true);
}

#[test]
fn legacy_file_missing_onboarding_is_migrated_to_completed() {
    let tmp = tempfile::tempdir().unwrap();
    let path = tmp.path().join("settings.json");
    let mut legacy = serde_json::to_value(Settings::default()).unwrap();
    legacy
        .as_object_mut()
        .unwrap()
        .remove("onboardingCompleted");
    std::fs::write(&path, serde_json::to_vec(&legacy).unwrap()).unwrap();
    assert!(
        settings::load_with_locale(&path, Locale::ZhCn)
            .unwrap()
            .onboarding_completed
    );
    assert!(
        settings::load_with_locale(&path, Locale::ZhCn)
            .unwrap()
            .onboarding_completed
    );
}

#[tokio::test]
async fn settings_set_preserves_explicit_onboarding_values_across_restart() {
    let tmp = tempfile::tempdir().unwrap();
    let open = || {
        DesktopCore::open(
            tmp.path().join("data"),
            tmp.path().join("cache"),
            tmp.path().join("askpass"),
            Arc::new(|_| {}),
        )
        .unwrap()
    };
    // DesktopCore reads the Mac's own language: only a Simplified Chinese Mac keeps Welcome pending across a restart.
    let welcome = settings::system_locale() == Locale::ZhCn;
    let core = open();
    assert_eq!(
        core.current_settings().unwrap().onboarding_completed,
        !welcome
    );
    for completed in [true, false] {
        let mut settings = core.current_settings().unwrap();
        settings.onboarding_completed = completed;
        // Use the shared settings_set save path without triggering any brew operation.
        let input: Settings =
            serde_json::from_value(serde_json::to_value(settings).unwrap()).unwrap();
        let saved = core.replace_settings(input).await.unwrap();
        assert_eq!(saved.onboarding_completed, completed);
        assert_eq!(
            core.current_settings().unwrap().onboarding_completed,
            completed
        );
        assert_eq!(
            open().current_settings().unwrap().onboarding_completed,
            completed || !welcome
        );
    }
}
