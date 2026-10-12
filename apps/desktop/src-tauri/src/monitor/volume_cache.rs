//! Amortize NSURL purgeable-space queries; live statfs health-threshold crossings bypass the cache.
use std::collections::BTreeMap;
#[derive(Clone, Default)]
pub struct Resources {
    pub important: Option<u64>,
    pub name: Option<String>,
    pub internal: Option<bool>,
}
struct Entry {
    at: u64,
    total: u64,
    raw: u64,
    resources: Resources,
}
#[derive(Default)]
pub struct VolumeCache {
    entries: BTreeMap<String, Entry>,
}
impl VolumeCache {
    pub fn resolve(
        &mut self,
        id: &str,
        now: u64,
        total: u64,
        raw: u64,
        read: impl FnOnce() -> Resources,
    ) -> Resources {
        if let Some(entry) = self.entries.get(id)
            && entry.total == total
            && now.saturating_sub(entry.at) < 60_000
        {
            let mut r = entry.resources.clone();
            r.important = r
                .important
                .map(|v| {
                    if raw >= entry.raw {
                        v.saturating_add(raw - entry.raw)
                    } else {
                        v.saturating_sub(entry.raw - raw)
                    }
                })
                .map(|v| v.min(total));
            // Health uses important capacity, including purgeable space, rather than raw statfs.
            // If the live estimate crosses a health threshold, confirm it immediately with NSURL.
            let available = r.important.unwrap_or(raw);
            if space_level(available, total)
                == space_level(entry.resources.important.unwrap_or(entry.raw), total)
            {
                return r;
            }
        }
        let resources = read();
        self.entries.insert(
            id.into(),
            Entry {
                at: now,
                total,
                raw,
                resources: resources.clone(),
            },
        );
        resources
    }
    pub fn retain(&mut self, ids: &std::collections::BTreeSet<String>) {
        self.entries.retain(|id, _| ids.contains(id));
    }
}
fn space_level(free: u64, total: u64) -> u8 {
    if free < 5_000_000_000 {
        2
    } else if free < 20_000_000_000 || u128::from(free) * 10 < u128::from(total) {
        1
    } else {
        0
    }
}
