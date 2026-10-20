//! Only complete clean verdicts are reusable. Keys bind the entire file content
//! to the running engine/signature generation; pathname/mtime alone never suffice.
use crate::{AppError, db::Database};
use std::collections::BTreeMap;
const CAPACITY: usize = 200_000;
const TTL_MS: i64 = 7 * 86400 * 1000;
const KEY: &str = "av_clean_content_cache_v1";

pub(super) struct CleanCache {
    entries: BTreeMap<String, i64>,
    generation: Option<String>,
}
impl CleanCache {
    pub fn load(db: &Database, now: i64) -> Self {
        let stored = db
            .meta(KEY)
            .ok()
            .flatten()
            .filter(|value| value.len() <= 40 * 1024 * 1024)
            .and_then(|value| serde_json::from_str::<Vec<(String, i64)>>(&value).ok())
            .unwrap_or_default();
        let mut cache = Self {
            entries: BTreeMap::new(),
            generation: None,
        };
        for (key, at) in stored.into_iter().rev().take(CAPACITY).rev() {
            if key.len() == 129
                && key.as_bytes()[64] == b':'
                && key
                    .bytes()
                    .enumerate()
                    .all(|(i, b)| i == 64 || b.is_ascii_hexdigit())
                && at <= now
                && now.saturating_sub(at) < TTL_MS
            {
                cache.insert(key, at);
            }
        }
        cache
    }
    pub fn key(generation: &str, hash: &str) -> String {
        format!("{generation}:{hash}")
    }
    pub fn contains(&self, key: &str, now: i64) -> bool {
        self.entries
            .get(key)
            .is_some_and(|at| *at <= now && now.saturating_sub(*at) < TTL_MS)
    }
    pub fn remove(&mut self, key: &str) {
        self.entries.remove(key);
    }
    pub fn insert(&mut self, key: String, at: i64) {
        self.insert_bounded(key, at, CAPACITY);
    }
    fn insert_bounded(&mut self, key: String, at: i64, capacity: usize) {
        let Some((generation, _)) = key.split_once(':') else {
            return;
        };
        if self.generation.as_deref() != Some(generation) {
            self.entries.clear();
            self.generation = Some(generation.to_owned());
        }
        // Deterministic admission avoids an LRU scan-thrashing loop: walking a
        // tree larger than the cache must not evict every hit before reaching it.
        if self.entries.len() >= capacity && !self.entries.contains_key(&key) {
            if self
                .entries
                .last_key_value()
                .is_some_and(|(largest, _)| &key > largest)
            {
                return;
            }
            self.entries.pop_last();
        }
        self.entries.insert(key, at);
    }
    pub fn save(&self, db: &Database) -> Result<(), AppError> {
        let entries: Vec<_> = self.entries.iter().collect();
        db.set_meta(KEY, &serde_json::to_string(&entries)?)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn repeated_large_walks_preserve_hits_instead_of_evicting_them_in_order() {
        let dir = tempfile::tempdir().unwrap();
        let db = Database::open(&dir.path().join("cache.db")).unwrap();
        let mut cache = CleanCache::load(&db, 100);
        let keys: Vec<_> = ["c", "b", "a"]
            .map(|value| CleanCache::key(&"a".repeat(64), &value.repeat(64)))
            .into();
        for key in &keys {
            cache.insert_bounded(key.clone(), 100, 2);
        }
        let mut hits = 0;
        for key in &keys {
            if cache.contains(key, 101) {
                hits += 1;
            } else {
                cache.insert_bounded(key.clone(), 101, 2);
            }
        }
        assert_eq!(hits, 2);
        assert_eq!(cache.entries.len(), 2);
    }

    #[test]
    fn content_generation_expiry_and_invalidation_are_required() {
        let dir = tempfile::tempdir().unwrap();
        let db = Database::open(&dir.path().join("cache.db")).unwrap();
        let mut cache = CleanCache::load(&db, 100);
        let key = CleanCache::key(&"a".repeat(64), &"b".repeat(64));
        cache.insert(key.clone(), 100);
        assert!(cache.contains(&key, 101));
        assert!(!cache.contains(&CleanCache::key(&"c".repeat(64), &"b".repeat(64)), 101));
        assert!(!cache.contains(&CleanCache::key(&"a".repeat(64), &"c".repeat(64)), 101));
        assert!(!cache.contains(&key, 100 + TTL_MS));
        cache.save(&db).unwrap();
        let mut loaded = CleanCache::load(&db, 101);
        assert!(loaded.contains(&key, 101));
        loaded.remove(&key);
        loaded.save(&db).unwrap();
        assert!(!CleanCache::load(&db, 102).contains(&key, 102));
    }
}
