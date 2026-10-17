use super::{types::MasApp, valid_adam_id, valid_bundle_id};
use crate::{AppError, db::Database, model::Locale};
use reqwest::{Client, Url};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    collections::{BTreeMap, VecDeque},
    sync::Arc,
};

const LOOKUP_TTL: i64 = 6 * 60 * 60 * 1000;
const SEARCH_TTL: i64 = 10 * 60 * 1000;
const CACHE_KEY: &str = "mas_lookup_cache_v1";
const LIMIT_KEY: &str = "mas_request_limit_v1";

#[derive(Default, Serialize, Deserialize)]
pub(super) struct RateLimit {
    attempts: VecDeque<i64>,
    until: i64,
}
impl RateLimit {
    pub(super) fn take(&mut self, now: i64) -> Result<(), AppError> {
        // Keep a rolling window as well as the server cooldown: no 30-request
        // burst across a minute boundary, and every endpoint spends one token.
        self.attempts.retain(|at| *at > now.saturating_sub(60_000));
        let until = if self.attempts.len() >= 15 {
            self.until.max(self.attempts[0].saturating_add(60_000))
        } else {
            self.until
        };
        if until > now {
            return Err(limited((until - now + 999) / 1000));
        }
        self.attempts.push_back(now);
        Ok(())
    }
}
fn limited(seconds: i64) -> AppError {
    AppError::new("E_MAS_RATE_LIMITED", "app_store_rate_limited")
        .with_detail(json!({"retryAfterSecs": seconds.max(1)}).to_string())
}
#[derive(Clone, Serialize, Deserialize)]
struct Entry<T> {
    at: i64,
    value: T,
}
impl<T> Entry<T> {
    fn fresh(&self, now: i64, ttl: i64) -> bool {
        self.at <= now && now - self.at < ttl
    }
}
pub(super) struct Api {
    client: Client,
    clock: Arc<dyn Fn() -> i64 + Send + Sync>,
    base: Url,
    database: Database,
    rate: RateLimit,
    lookup: BTreeMap<String, Entry<Option<MasApp>>>,
    searches: BTreeMap<String, Entry<Vec<MasApp>>>,
}
impl Api {
    pub(super) fn new(
        database: Database,
        base: Url,
        clock: Arc<dyn Fn() -> i64 + Send + Sync>,
    ) -> Result<Self, AppError> {
        let client = Client::builder()
            .timeout(std::time::Duration::from_secs(20))
            // Fixed direct Apple endpoint. Do not forward terms/installed IDs to
            // arbitrary redirect targets or OpenNavo's server.
            .redirect(reqwest::redirect::Policy::none())
            .build()?;
        let lookup = database
            .meta(CACHE_KEY)?
            .filter(|s| s.len() <= 32 * 1024 * 1024)
            .and_then(|text| serde_json::from_str(&text).ok())
            .unwrap_or_default();
        let rate = database
            .meta(LIMIT_KEY)?
            .and_then(|text| serde_json::from_str(&text).ok())
            .unwrap_or_default();
        Ok(Self {
            client,
            clock,
            base,
            database,
            rate,
            lookup,
            searches: BTreeMap::new(),
        })
    }
    fn persist(&mut self, now: i64) -> Result<(), AppError> {
        self.lookup.retain(|_, entry| entry.fresh(now, LOOKUP_TTL));
        while self.lookup.len() > 2048 {
            let oldest = self
                .lookup
                .iter()
                .min_by_key(|(_, entry)| entry.at)
                .map(|(key, _)| key.clone());
            if let Some(key) = oldest {
                self.lookup.remove(&key);
            }
        }
        self.database
            .set_meta(CACHE_KEY, &serde_json::to_string(&self.lookup)?)
    }
    async fn request(
        &mut self,
        country: &str,
        locale: Locale,
        endpoint: &str,
        params: &[(&str, String)],
        now: i64,
    ) -> Result<Vec<MasApp>, AppError> {
        let now = (self.clock)().max(now);
        self.rate.take(now)?;
        self.database
            .set_meta(LIMIT_KEY, &serde_json::to_string(&self.rate)?)?;
        let mut url = self
            .base
            .join(endpoint)
            .map_err(|_| AppError::new("E_MAS_ENGINE", "itunes_endpoint"))?;
        url.query_pairs_mut()
            .extend_pairs([
                ("entity", "desktopSoftware"),
                ("country", country),
                ("lang", language(locale)),
            ])
            .extend_pairs(params.iter().map(|(key, value)| (*key, value.as_str())));
        let mut response = self.client.get(url).send().await?;
        if matches!(response.status().as_u16(), 403 | 429) {
            let now = (self.clock)();
            let seconds = retry_after(
                response
                    .headers()
                    .get("retry-after")
                    .and_then(|s| s.to_str().ok()),
                now,
            );
            self.rate.until = now.saturating_add(seconds.saturating_mul(1000));
            self.database
                .set_meta(LIMIT_KEY, &serde_json::to_string(&self.rate)?)?;
            return Err(limited(seconds));
        }
        if !response.status().is_success() {
            return Err(AppError::new("E_MAS_ENGINE", "itunes_http").with_detail(
                json!({"domain":"HTTP", "code": response.status().as_u16()}).to_string(),
            ));
        }
        let mut bytes = Vec::new();
        while let Some(chunk) = response.chunk().await? {
            if bytes.len() + chunk.len() > 8 * 1024 * 1024 {
                return Err(AppError::new("E_MAS_ENGINE", "itunes_response_size"));
            }
            bytes.extend_from_slice(&chunk);
        }
        let value: Value = serde_json::from_slice(&bytes)
            .map_err(|e| AppError::new("E_MAS_ENGINE", "itunes_json").with_detail(e.to_string()))?;
        let rows = value
            .get("results")
            .and_then(Value::as_array)
            .filter(|rows| value["resultCount"].as_u64() == Some(rows.len() as u64))
            .ok_or_else(|| AppError::new("E_MAS_ENGINE", "itunes_results"))?;
        Ok(rows
            .iter()
            .filter_map(|row| parse_app(row, country))
            .collect())
    }
    pub(super) async fn search(
        &mut self,
        country: &str,
        locale: Locale,
        term: &str,
        limit: u32,
        now: i64,
    ) -> Result<Vec<MasApp>, AppError> {
        let key = format!("{country}|{}|{limit}|{term}", language(locale));
        if let Some(hit) = self
            .searches
            .get(&key)
            .filter(|entry| entry.fresh(now, SEARCH_TTL))
        {
            return Ok(hit.value.clone());
        }
        let mut apps = self
            .request(
                country,
                locale,
                "search",
                &[("term", term.into()), ("limit", limit.to_string())],
                now,
            )
            .await?;
        apps.truncate(limit as usize);
        self.searches
            .retain(|_, entry| entry.fresh(now, SEARCH_TTL));
        if self.searches.len() >= 100
            && let Some(oldest) = self
                .searches
                .iter()
                .min_by_key(|(_, entry)| entry.at)
                .map(|(key, _)| key.clone())
        {
            self.searches.remove(&oldest);
        }
        self.searches.insert(
            key,
            Entry {
                at: now,
                value: apps.clone(),
            },
        );
        Ok(apps)
    }
    // Background inventory work waits for our rolling budget instead of failing
    // a whole check. Apple's explicit cooldown remains an error for the caller.
    async fn wait_for_budget(&mut self) -> Result<(), AppError> {
        loop {
            let now = (self.clock)();
            if self.rate.until > now {
                return Err(limited((self.rate.until - now + 999) / 1000));
            }
            self.rate
                .attempts
                .retain(|at| *at > now.saturating_sub(60_000));
            if self.rate.attempts.len() < 15 {
                return Ok(());
            }
            let wait = self.rate.attempts[0]
                .saturating_add(60_000)
                .saturating_sub(now);
            tokio::time::sleep(std::time::Duration::from_millis(wait.max(1) as u64)).await;
        }
    }

    pub(super) async fn installed_bundle(
        &mut self,
        country: &str,
        locale: Locale,
        bundle: &str,
    ) -> Result<Option<MasApp>, AppError> {
        let key = format!("{country}|{}|bundle|{bundle}", language(locale));
        if !self
            .lookup
            .get(&key)
            .is_some_and(|entry| entry.fresh((self.clock)(), LOOKUP_TTL))
        {
            self.wait_for_budget().await?;
        }
        Ok(self
            .lookup_bundles(country, locale, &[bundle.to_owned()], (self.clock)())
            .await?
            .remove(bundle))
    }

    pub(super) async fn update_ids(
        &mut self,
        country: &str,
        locale: Locale,
        ids: &[u64],
    ) -> Result<Vec<MasApp>, AppError> {
        if ids.iter().any(|id| {
            let key = format!("{country}|{}|id|{id}", language(locale));
            !self
                .lookup
                .get(&key)
                .is_some_and(|entry| entry.fresh((self.clock)(), LOOKUP_TTL))
        }) {
            self.wait_for_budget().await?;
        }
        self.lookup_ids(country, locale, ids, (self.clock)()).await
    }

    pub(super) async fn lookup_ids(
        &mut self,
        country: &str,
        locale: Locale,
        ids: &[u64],
        now: i64,
    ) -> Result<Vec<MasApp>, AppError> {
        let key = |id: &u64| format!("{country}|{}|id|{id}", language(locale));
        let missing: Vec<_> = ids
            .iter()
            .filter(|id| {
                !self
                    .lookup
                    .get(&key(id))
                    .is_some_and(|entry| entry.fresh(now, LOOKUP_TTL))
            })
            .copied()
            .collect();
        if !missing.is_empty() {
            let apps = self
                .request(
                    country,
                    locale,
                    "lookup",
                    &[(
                        "id",
                        missing
                            .iter()
                            .map(u64::to_string)
                            .collect::<Vec<_>>()
                            .join(","),
                    )],
                    now,
                )
                .await?;
            for id in missing {
                self.lookup.insert(
                    key(&id),
                    Entry {
                        at: now,
                        value: apps.iter().find(|app| app.adam_id == id).cloned(),
                    },
                );
            }
            self.persist(now)?;
        }
        Ok(ids
            .iter()
            .filter_map(|id| {
                self.lookup
                    .get(&key(id))
                    .and_then(|entry| entry.value.clone())
            })
            .collect())
    }
    pub(super) async fn lookup_bundles(
        &mut self,
        country: &str,
        locale: Locale,
        ids: &[String],
        now: i64,
    ) -> Result<BTreeMap<String, MasApp>, AppError> {
        let mut result = BTreeMap::new();
        for id in ids {
            let key = format!("{country}|{}|bundle|{id}", language(locale));
            if !self
                .lookup
                .get(&key)
                .is_some_and(|entry| entry.fresh(now, LOOKUP_TTL))
            {
                // Apple's bundleId lookup is singular. Never assume a comma list
                // is supported and cache a silently incomplete response as misses.
                let apps = self
                    .request(country, locale, "lookup", &[("bundleId", id.clone())], now)
                    .await?;
                let app = apps.into_iter().find(|app| app.bundle_id == *id);
                if let Some(app) = &app {
                    self.lookup.insert(
                        format!("{country}|{}|id|{}", language(locale), app.adam_id),
                        Entry {
                            at: now,
                            value: Some(app.clone()),
                        },
                    );
                }
                self.lookup.insert(
                    key.clone(),
                    Entry {
                        at: now,
                        value: app,
                    },
                );
                // Persist each response even if a later item hits the shared rate limit.
                self.persist(now)?;
            }
            if let Some(app) = self.lookup.get(&key).and_then(|entry| entry.value.clone()) {
                result.insert(id.clone(), app);
            }
        }
        Ok(result)
    }
}
fn language(locale: Locale) -> &'static str {
    match locale {
        Locale::ZhCn => "zh_cn",
        Locale::JaJp => "ja_jp",
        Locale::EsEs => "es_es",
        Locale::PtBr => "pt_br",
        Locale::RuRu => "ru_ru",
        Locale::EnUs => "en_us",
    }
}
fn retry_after(value: Option<&str>, now: i64) -> i64 {
    value
        .and_then(|text| {
            text.parse::<i64>().ok().or_else(|| {
                chrono::DateTime::parse_from_rfc2822(text)
                    .ok()
                    .map(|date| (date.timestamp_millis() - now + 999) / 1000)
            })
        })
        .unwrap_or(60)
        .clamp(1, 86_400)
}
fn https(value: &str) -> Option<String> {
    Url::parse(value)
        .ok()
        .filter(|url| {
            url.scheme() == "https"
                && url.host_str().is_some()
                && url.username().is_empty()
                && url.password().is_none()
        })
        .map(|url| url.to_string())
}
fn parse_app(value: &Value, country: &str) -> Option<MasApp> {
    if !matches!(value["kind"].as_str()?, "software" | "mac-software") {
        return None;
    }
    // A misplaced mobile result must never become an install/update candidate.
    if value["supportedDevices"].as_array().is_some_and(|devices| {
        devices.iter().any(|device| {
            device.as_str().is_some_and(|s| {
                s.starts_with("iPhone") || s.starts_with("iPad") || s.starts_with("iPod")
            })
        })
    }) {
        return None;
    }
    let id = value["trackId"].as_u64().filter(|id| valid_adam_id(*id))?;
    let bundle = value["bundleId"]
        .as_str()
        .filter(|id| valid_bundle_id(id))?;
    let text = |key: &str| value[key].as_str().map(str::to_owned);
    let strings = |key: &str| {
        value[key]
            .as_array()
            .map(|items| {
                items
                    .iter()
                    .filter_map(Value::as_str)
                    .map(str::to_owned)
                    .collect::<Vec<_>>()
            })
            .unwrap_or_default()
    };
    let price = value["price"]
        .as_f64()
        .filter(|v| v.is_finite() && *v >= 0.0)?;
    Some(MasApp {
        adam_id: id,
        bundle_id: bundle.into(),
        name: text("trackName")?,
        seller: text("sellerName")
            .or_else(|| text("artistName"))
            .unwrap_or_default(),
        version: text("version")?,
        release_notes: text("releaseNotes"),
        released_at: text("currentVersionReleaseDate"),
        minimum_os: text("minimumOsVersion"),
        size_bytes: value["fileSizeBytes"]
            .as_u64()
            .or_else(|| value["fileSizeBytes"].as_str()?.parse::<u64>().ok())
            .filter(|n| *n <= 9_007_199_254_740_991),
        price: price.try_into().ok()?,
        formatted_price: text("formattedPrice").unwrap_or_default(),
        currency: text("currency").unwrap_or_default(),
        rating: value["averageUserRating"]
            .as_f64()
            .filter(|n| n.is_finite() && (0.0..=5.0).contains(n)),
        rating_count: value["userRatingCount"].as_u64(),
        icon_url: text("artworkUrl512")
            .or_else(|| text("artworkUrl100"))
            .and_then(|url| https(&url)),
        screenshots: strings("screenshotUrls")
            .iter()
            .filter_map(|url| https(url))
            .collect(),
        description: text("description").unwrap_or_default(),
        genres: strings("genres"),
        store_url: format!("https://apps.apple.com/{country}/app/id{id}?mt=12"),
        country: country.into(),
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn rolling_limit_and_retry_after_cover_boundaries_and_clock_rollback() {
        let mut limit = RateLimit::default();
        for i in 0..15 {
            limit.take(1_000_000 + i * 1_000).unwrap();
        }
        assert!(limit.take(1_059_999).is_err());
        limit.take(1_060_000).unwrap();
        assert!(limit.take(1_060_000).is_err());
        assert!(limit.take(999_999).is_err());
        assert_eq!(retry_after(Some("120"), 0), 120);
        assert_eq!(retry_after(Some("Thu, 01 Jan 1970 00:02:00 GMT"), 0), 120);
        assert_eq!(retry_after(None, 0), 60);
    }
}
