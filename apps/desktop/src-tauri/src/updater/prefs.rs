use super::UpdateChannel;
use crate::{AppError, db::Database};
use rusqlite::{OptionalExtension, params};
#[derive(Debug, Clone, Default)]
pub struct Preferences {
    pub skipped_version: Option<String>,
    pub channel: UpdateChannel,
    pub ignored: bool,
}
pub fn validate_key(key: &str) -> Result<(), AppError> {
    let app = key
        .strip_prefix("app:")
        .is_some_and(|v| !v.is_empty() && v.len() <= 512 && !v.chars().any(char::is_control));
    let brew = key
        .strip_prefix("homebrew:")
        .and_then(|v| v.split_once(':'))
        .is_some_and(|(kind, token)| {
            matches!(kind, "cask" | "formula") && crate::brew::args::validate_token(token).is_ok()
        });
    if app || brew {
        Ok(())
    } else {
        Err(AppError::new("E_INVALID_ARG", "update_app_key"))
    }
}
pub fn load(db: &Database, key: &str) -> Result<Preferences, AppError> {
    validate_key(key)?;
    db.with(|c| {
        Ok(c.query_row(
            "SELECT skipped_version,channel,ignored FROM update_prefs WHERE app_key=?1",
            [key],
            |r| {
                Ok(Preferences {
                    skipped_version: r.get(0)?,
                    channel: if r.get::<_, String>(1)? == "beta" {
                        UpdateChannel::Beta
                    } else {
                        UpdateChannel::Stable
                    },
                    ignored: r.get(2)?,
                })
            },
        )
        .optional()?
        .unwrap_or_default())
    })
}
pub fn skip(db: &Database, key: &str, version: Option<&str>) -> Result<(), AppError> {
    validate_key(key)?;
    if version.is_some_and(|v| v.is_empty() || v.len() > 256 || v.chars().any(char::is_control)) {
        return Err(AppError::new("E_INVALID_ARG", "update_skipped_version"));
    }
    db.with(|c|{c.execute("INSERT INTO update_prefs(app_key,skipped_version) VALUES(?1,?2) ON CONFLICT(app_key) DO UPDATE SET skipped_version=excluded.skipped_version",params![key,version])?;Ok(())})
}
pub fn channel(db: &Database, key: &str, channel: UpdateChannel) -> Result<(), AppError> {
    validate_key(key)?;
    db.with(|c|{c.execute("INSERT INTO update_prefs(app_key,channel) VALUES(?1,?2) ON CONFLICT(app_key) DO UPDATE SET channel=excluded.channel",params![key,if channel==UpdateChannel::Beta{"beta"}else{"stable"}])?;Ok(())})
}

pub fn ignore(db: &Database, key: &str, ignored: bool) -> Result<(), AppError> {
    validate_key(key)?;
    db.with(|c| { c.execute("INSERT INTO update_prefs(app_key,ignored) VALUES(?1,?2) ON CONFLICT(app_key) DO UPDATE SET ignored=excluded.ignored", params![key,ignored])?; Ok(()) })
}
