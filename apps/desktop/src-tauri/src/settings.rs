use crate::{AppError, model::Settings};
use std::{io::Write, path::Path, sync::Arc};
pub type Autostart = Arc<dyn Fn(bool) -> Result<(), AppError> + Send + Sync>;
pub type Save = Arc<dyn Fn(&Settings) -> Result<(), AppError> + Send + Sync>;
pub fn validate(mut settings: Settings) -> Result<Settings, AppError> {
    let bytes = settings.check_time.as_bytes();
    if bytes.len() != 5
        || bytes[2] != b':'
        || ![0, 1, 3, 4].into_iter().all(|i| bytes[i].is_ascii_digit())
    {
        return Err(AppError::new("E_INVALID_ARG", "check_time"));
    }
    let hour = (bytes[0] - b'0') * 10 + bytes[1] - b'0';
    let minute = (bytes[3] - b'0') * 10 + bytes[4] - b'0';
    if hour > 23 || minute > 59 {
        return Err(AppError::new("E_INVALID_ARG", "check_time"));
    }
    if let Some(path) = &settings.brew_path
        && (!Path::new(path).is_absolute()
            || path.chars().any(char::is_control)
            || path.len() > 4096)
    {
        return Err(AppError::new("E_INVALID_ARG", "brew_path"));
    }
    if settings.cleanup_project_roots.len() > 32
        || settings.cleanup_project_roots.iter().any(|root| {
            root.is_empty()
                || root.len() > 4096
                || root.chars().any(char::is_control)
                || (!root.starts_with("~/") && !Path::new(root).is_absolute())
                || Path::new(root).components().any(|component| {
                    matches!(
                        component,
                        std::path::Component::ParentDir | std::path::Component::CurDir
                    )
                })
        })
    {
        return Err(AppError::new("E_INVALID_ARG", "cleanup_project_roots"));
    }
    settings.mirror = crate::mirror::normalize(settings.mirror)?;
    if settings.custom_mirrors.len() > 20 {
        return Err(AppError::new("E_INVALID_ARG", "custom_mirror_count"));
    }
    let mut keys = std::collections::HashSet::new();
    for input in &mut settings.custom_mirrors {
        *input = crate::mirror::normalize_custom(input.clone())?;
        if !keys.insert(input.key.clone()) {
            return Err(AppError::new("E_INVALID_ARG", "custom_mirror_duplicate"));
        }
    }
    if settings.mirror.key.starts_with("custom-") {
        let input = settings
            .custom_mirrors
            .iter()
            .find(|input| input.key == settings.mirror.key)
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "custom_mirror_missing"))?;
        settings.mirror = crate::mirror::choice(input);
    }
    // Match load's size limit so every saved configuration can be read on the next launch.
    if serde_json::to_vec_pretty(&settings)?.len() > 64 * 1024 {
        return Err(AppError::new("E_INVALID_ARG", "settings_size"));
    }
    Ok(settings)
}
pub fn locale(system: Option<&str>) -> crate::model::Locale {
    match_locale(&[system.unwrap_or("en")])
}
pub fn match_locale(languages: &[&str]) -> crate::model::Locale {
    for language in languages {
        let normalized = language.trim().to_ascii_lowercase().replace('_', "-");
        let prefix = normalized.split('-').next().unwrap_or("");
        for metadata in crate::locales_gen::LOCALE_METADATA {
            if metadata.hreflang.split('-').next() == Some(prefix) {
                return metadata.code;
            }
        }
    }
    crate::locales_gen::DEFAULT_LOCALE
}

pub fn load(path: &Path) -> Result<Settings, AppError> {
    let settings = if path.is_file() {
        if std::fs::metadata(path)?.len() > 64 * 1024 {
            return Err(AppError::new("E_INVALID_ARG", "settings_size"));
        }
        serde_json::from_slice(&std::fs::read(path)?)?
    } else {
        Settings {
            locale: system_locale(),
            ..Settings::default()
        }
    };
    let settings = resolve(validate(settings)?, system_locale());
    save_file(path, &settings)?;
    Ok(settings)
}
pub fn save_file(path: &Path, settings: &Settings) -> Result<(), AppError> {
    let parent = path
        .parent()
        .ok_or_else(|| AppError::new("E_INVALID_ARG", "settings_path"))?;
    std::fs::create_dir_all(parent)?;
    let temporary = parent.join(format!("settings_{}.tmp", uuid::Uuid::now_v7()));
    let result = (|| {
        let mut options = std::fs::OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let mut file = options.open(&temporary)?;
        file.write_all(&serde_json::to_vec_pretty(settings)?)?;
        file.sync_all()?;
        std::fs::rename(&temporary, path)?;
        Ok::<_, AppError>(())
    })();
    if result.is_err() {
        let _ = std::fs::remove_file(&temporary);
    }
    result
}
pub fn plugin_save(app: &tauri::AppHandle, path: &Path) -> Result<Save, AppError> {
    use tauri_plugin_store::StoreExt;
    let store = app
        .store_builder(path)
        .disable_auto_save()
        .build()
        .map_err(|e| AppError::new("E_UNKNOWN", "settings_store").with_detail(e.to_string()))?;
    Ok(Arc::new(move |settings| {
        let value = serde_json::to_value(settings)?;
        let entries = value
            .as_object()
            .ok_or_else(|| AppError::new("E_UNKNOWN", "settings_object"))?;
        let previous = store.entries();
        store.clear();
        for (key, value) in entries {
            store.set(key, value.clone());
        }
        if let Err(error) = store.save() {
            store.clear();
            for (key, value) in previous {
                store.set(key, value);
            }
            return Err(AppError::new("E_UNKNOWN", "settings_save").with_detail(error.to_string()));
        }
        Ok(())
    }))
}

pub fn legacy_mode(
    saved: crate::model::Locale,
    system: crate::model::Locale,
) -> crate::model::LocaleMode {
    if saved == system {
        crate::model::LocaleMode::System
    } else {
        crate::model::LocaleMode::Manual
    }
}
pub fn resolve(mut settings: Settings, system: crate::model::Locale) -> Settings {
    if settings.locale_mode == crate::model::LocaleMode::System {
        settings.locale = system;
    }
    settings
}
pub fn apple_language(locale: crate::model::Locale) -> &'static str {
    use crate::locales_gen::Locale::*;
    match locale {
        EnUs => "en",
        ZhCn => "zh-Hans",
        JaJp => "ja",
        EsEs => "es",
        PtBr => "pt-BR",
        RuRu => "ru",
    }
}
#[cfg(target_os = "macos")]
pub fn system_locale() -> crate::model::Locale {
    use objc2_foundation::{NSString, NSUserDefaults};
    let defaults = NSUserDefaults::standardUserDefaults();
    // Use Foundation's global-domain constant; never read this app's AppleLanguages override.
    let global = defaults.persistentDomainForName(unsafe { objc2_foundation::NSGlobalDomain });
    let languages = global
        .and_then(|domain| domain.objectForKey(&NSString::from_str("AppleLanguages")))
        .and_then(|value| value.downcast::<objc2_foundation::NSArray>().ok());
    let languages = languages
        .map(|array| {
            array
                .iter()
                .filter_map(|s| s.downcast::<NSString>().ok().map(|s| s.to_string()))
                .collect::<Vec<_>>()
        })
        .unwrap_or_default();
    match_locale(&languages.iter().map(String::as_str).collect::<Vec<_>>())
}
#[cfg(not(target_os = "macos"))]
pub fn system_locale() -> crate::model::Locale {
    locale(tauri_plugin_os::locale().as_deref())
}
#[cfg(target_os = "macos")]
pub fn apply_apple_languages(settings: &Settings) {
    use objc2_foundation::{NSArray, NSString, NSUserDefaults};
    let defaults = NSUserDefaults::standardUserDefaults();
    let key = NSString::from_str("AppleLanguages");
    if settings.locale_mode == crate::model::LocaleMode::Manual {
        let value =
            NSArray::from_retained_slice(&[NSString::from_str(apple_language(settings.locale))]);
        // The array contains only NSString entries, matching the AppleLanguages system contract.
        unsafe {
            defaults.setObject_forKey(Some(&value), &key);
        }
    } else {
        defaults.removeObjectForKey(&key);
    }
}
#[cfg(not(target_os = "macos"))]
pub fn apply_apple_languages(_: &Settings) {}

/// App state owns the notification token and unregisters it on the main thread at exit; no system-preference polling.
#[cfg(target_os = "macos")]
pub fn install_locale_observer(app: &tauri::AppHandle) {
    use objc2::rc::Retained;
    use objc2_foundation::{NSNotificationCenter, NSOperationQueue, NSString};
    use tauri::Manager;
    struct Guard {
        app: tauri::AppHandle,
        tokens: Vec<usize>,
    }
    impl Drop for Guard {
        fn drop(&mut self) {
            let tokens = std::mem::take(&mut self.tokens);
            let _ = self.app.run_on_main_thread(move || {
                for pointer in tokens {
                    // Release into_raw's +1 ownership only after unregistering; never dereference the pointer across threads.
                    if let Some(token) = unsafe {
                        Retained::<objc2::runtime::AnyObject>::from_raw(pointer as *mut _)
                    } {
                        unsafe {
                            NSNotificationCenter::defaultCenter().removeObserver(&token);
                        }
                    }
                }
            });
        }
    }
    let mut tokens = Vec::new();
    for name in [
        "NSCurrentLocaleDidChangeNotification",
        "NSUserDefaultsDidChangeNotification",
    ] {
        let handle = app.clone();
        let block = block2::RcBlock::new(
            move |_: std::ptr::NonNull<objc2_foundation::NSNotification>| {
                crate::commands::settings::refresh_system(&handle);
            },
        );
        // The callback captures only a Send + Sync AppHandle; system notifications dispatch to the main queue.
        let token = unsafe {
            NSNotificationCenter::defaultCenter().addObserverForName_object_queue_usingBlock(
                Some(&NSString::from_str(name)),
                None,
                Some(&NSOperationQueue::mainQueue()),
                &block,
            )
        };
        tokens.push(Retained::into_raw(token) as usize);
    }
    app.manage(Guard {
        app: app.clone(),
        tokens,
    });
}
#[cfg(not(target_os = "macos"))]
pub fn install_locale_observer(_: &tauri::AppHandle) {}
