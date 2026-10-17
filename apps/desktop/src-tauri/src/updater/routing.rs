//! Update method for the installed-app inspector, using only local observations.
use super::{UpdateSource, feed, service};
use crate::{
    AppError,
    db::Database,
    model::{AppSource, Kind, LocalApp, Settings},
};
use base64::Engine;
use std::path::Path;

pub fn decorate(
    db: &Database,
    settings: &Settings,
    mut app: LocalApp,
) -> Result<LocalApp, AppError> {
    app.homebrew_cask = if app.cask.is_none() && app.protected_reason.is_none() {
        app.catalog_token
            .as_ref()
            .filter(|t| {
                crate::catalog::get(db, Kind::Cask, t)
                    .ok()
                    .flatten()
                    .is_some()
            })
            .cloned()
    } else {
        None
    };
    app.updater = None;
    if app.protected_reason.is_some() {
        return Ok(app);
    }
    app.updater = select(db, settings, &app, &service::cached_rules(db))?;
    Ok(app)
}
/// One method per app; disabled engines do not hide an available lower priority engine.
pub fn select(
    db: &Database,
    settings: &Settings,
    app: &LocalApp,
    rules: &[service::FeedRule],
) -> Result<Option<UpdateSource>, AppError> {
    if app.protected_reason.is_some() || app.path.starts_with("/System/") {
        return Ok(None);
    }
    let enabled = |s| settings.update_sources.enabled(s);
    if app.source == AppSource::AppStore {
        return Ok(enabled(UpdateSource::Mas).then_some(UpdateSource::Mas));
    }
    let cask = app
        .cask
        .as_ref()
        .map(|token| crate::catalog::get(db, Kind::Cask, token))
        .transpose()?
        .flatten();
    if app.cask.is_some()
        && !cask.as_ref().is_some_and(|c| c.auto_updates)
        && enabled(UpdateSource::Homebrew)
    {
        return Ok(Some(UpdateSource::Homebrew));
    }
    let info = plist::Value::from_file(Path::new(&app.path).join("Contents/Info.plist")).ok();
    let string = |key: &str| info.as_ref()?.as_dictionary()?.get(key)?.as_string();
    // The native installer requires a 32-byte Ed25519 key, including for curated feeds.
    let signed_feed = string("SUPublicEDKey")
        .and_then(|key| base64::engine::general_purpose::STANDARD.decode(key).ok())
        .is_some_and(|key| key.len() == 32);
    let owned_feed = string("SUFeedURL").is_some_and(|url| feed::https(url).is_ok());
    for source in [
        UpdateSource::Sparkle,
        UpdateSource::Electron,
        UpdateSource::Github,
        UpdateSource::Vendor,
    ] {
        if !enabled(source) || source == UpdateSource::Sparkle && !signed_feed {
            continue;
        }
        let curated = rules
            .iter()
            .any(|r| r.source == source && Some(&r.bundle_id) == app.bundle_id.as_ref());
        let local = source == UpdateSource::Sparkle && owned_feed
            || source == UpdateSource::Electron
                && super::electron::Config::local(Path::new(&app.path)).is_ok();
        if curated || local {
            return Ok(Some(source));
        }
    }
    Ok((app.cask.is_some() && enabled(UpdateSource::Homebrew)).then_some(UpdateSource::Homebrew))
}
