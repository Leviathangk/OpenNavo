use super::{
    countries::{ALIASES, STOREFRONTS},
    types::MasCountrySource,
};
use crate::AppError;

pub fn normalize(value: &str) -> Result<String, AppError> {
    let value = value.to_ascii_lowercase();
    let value = if value == "uk" { "gb" } else { &value };
    let value = ALIASES
        .iter()
        .find(|(from, _)| *from == value)
        .map_or(value, |(_, to)| *to);
    if STOREFRONTS.iter().any(|(_, country)| *country == value) {
        Ok(value.to_owned())
    } else {
        Err(AppError::new("E_MAS_REGION", "unsupported_store_region"))
    }
}

pub fn resolve(
    setting: &str,
    storefront: Option<&str>,
    locale: &str,
) -> Result<(String, MasCountrySource), AppError> {
    if setting != "auto" {
        return Ok((normalize(setting)?, MasCountrySource::Setting));
    }
    let account = storefront
        .and_then(|value| value.trim().split(['-', ',']).next()?.parse::<u32>().ok())
        .and_then(|id| STOREFRONTS.iter().find(|(candidate, _)| *candidate == id));
    if let Some((_, country)) = account {
        return Ok(((*country).into(), MasCountrySource::Account));
    }
    let country = locale
        .split('@')
        .next()
        .unwrap_or(locale)
        .split(['_', '-'])
        .skip(1)
        .find(|part| part.len() == 2 && part.bytes().all(|b| b.is_ascii_alphabetic()))
        .and_then(|value| normalize(value).ok())
        .unwrap_or_else(|| "us".into());
    Ok((country, MasCountrySource::System))
}

pub fn admin_install(version: &str) -> bool {
    let Some(parts) = super::updates::version_numbers(version) else {
        return false;
    };
    match parts.first().copied().unwrap_or_default() {
        14 => parts >= vec![14, 8, 2],
        15 => parts >= vec![15, 7, 2],
        26 => parts >= vec![26, 1],
        major => major > 26,
    }
}
