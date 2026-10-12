use super::{poison, types::*};
use crate::{
    AppError,
    events::{CoreEvent, EventSink},
};
use std::{
    collections::BTreeMap,
    path::Path,
    sync::Mutex,
    time::{Duration, Instant},
};

/// One serialized, throttled stream per scan, shared by all collectors.
pub(super) struct Progress {
    request: String,
    sink: EventSink,
    state: Mutex<State>,
}

struct State {
    checked: u64,
    total: u64,
    bytes: BTreeMap<CleanupCategory, u64>,
    last: Option<Instant>,
}
impl Progress {
    pub fn new(request: String, total: usize, sink: EventSink) -> Self {
        Self {
            request,
            sink,
            state: Mutex::new(State {
                checked: 0,
                total: total as u64,
                bytes: BTreeMap::new(),
                last: None,
            }),
        }
    }
    pub fn update(
        &self,
        category: CleanupCategory,
        path: Option<&Path>,
        bytes: u64,
    ) -> Result<(), AppError> {
        self.send(category, path, bytes, false)
    }
    pub fn finish(&self, category: CleanupCategory, bytes: u64) -> Result<(), AppError> {
        self.send(category, None, bytes, true)
    }
    fn send(
        &self,
        category: CleanupCategory,
        path: Option<&Path>,
        bytes: u64,
        finished: bool,
    ) -> Result<(), AppError> {
        let mut state = self.state.lock().map_err(poison)?;
        let previous = state.bytes.entry(category).or_default();
        *previous = (*previous).max(bytes);
        if finished {
            state.checked += 1;
        }
        if state
            .last
            .is_none_or(|last| last.elapsed() >= Duration::from_millis(100))
            || (finished && state.checked == state.total)
        {
            (self.sink)(CoreEvent::CleanupScan(CleanupProgress {
                request_id: self.request.clone(),
                category,
                location: path.map(|p| p.to_string_lossy().into_owned()),
                checked: state.checked,
                total: state.total,
                found_bytes: state.bytes.values().copied().fold(0, u64::saturating_add),
            }));
            state.last = Some(Instant::now());
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;
    #[test]
    fn interleaved_collectors_accumulate_without_resetting_bytes_or_counts() {
        let events = Arc::new(Mutex::new(vec![]));
        let captured = events.clone();
        let progress = Progress::new(
            "request".into(),
            2,
            Arc::new(move |e| captured.lock().unwrap().push(e)),
        );
        progress
            .update(
                CleanupCategory::AppCaches,
                Some(Path::new("/synthetic/a")),
                10,
            )
            .unwrap();
        // Deterministically permit another throttled event without waiting in the test.
        progress.state.lock().unwrap().last = None;
        progress
            .update(CleanupCategory::Logs, Some(Path::new("/synthetic/b")), 20)
            .unwrap();
        progress.finish(CleanupCategory::Logs, 20).unwrap();
        progress.state.lock().unwrap().last = None;
        progress
            .update(CleanupCategory::AppCaches, None, 30)
            .unwrap();
        progress.update(CleanupCategory::Logs, None, 0).unwrap();
        progress.finish(CleanupCategory::AppCaches, 30).unwrap();
        let events = events.lock().unwrap();
        let values: Vec<_> = events
            .iter()
            .filter_map(|e| match e {
                CoreEvent::CleanupScan(p) => Some((p.checked, p.total, p.found_bytes)),
                _ => None,
            })
            .collect();
        assert_eq!(values, [(0, 2, 10), (0, 2, 30), (1, 2, 50), (2, 2, 50)]);
    }
}
