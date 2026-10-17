// SPDX-License-Identifier: GPL-3.0-only
//! Honest progress: FILDES has no intra-object byte counter.
use crate::{brew::pty::Progress, model::TaskPhase};
use std::{
    sync::Mutex,
    time::{Duration, Instant},
};

pub(crate) struct Meter {
    state: Mutex<State>,
}
#[derive(Default)]
struct State {
    weight_total: u64,
    weight_done: u64,
    weight: u64,
    volume: Option<(u64, u64, Instant)>,
    object_started: Option<Instant>,
}
impl Default for Meter {
    fn default() -> Self {
        Self {
            state: Mutex::new(State::default()),
        }
    }
}
impl Meter {
    pub fn plan(&self, bytes: u64) {
        self.state.lock().unwrap().weight_total = bytes;
    }
    pub fn begin(&self, weight: u64) {
        let mut s = self.state.lock().unwrap();
        s.weight = weight;
        s.volume = None;
        s.object_started = None;
    }
    pub fn volume(&self, total: u64) {
        self.state.lock().unwrap().volume = Some((0, total, Instant::now()));
    }
    pub fn file_started(&self) {
        self.state.lock().unwrap().object_started = Some(Instant::now());
    }
    pub fn file_finished(&self, scanned_bytes: u64) {
        let mut s = self.state.lock().unwrap();
        s.object_started = None;
        if let Some((done, _, _)) = &mut s.volume {
            *done += scanned_bytes;
        }
    }
    pub fn finish(&self) {
        let mut s = self.state.lock().unwrap();
        s.weight_done = s.weight_done.saturating_add(s.weight);
        s.weight = 0;
        s.volume = None;
        s.object_started = None;
    }
    pub fn progress(&self) -> Progress {
        self.progress_at(Instant::now())
    }
    fn progress_at(&self, now: Instant) -> Progress {
        let s = self.state.lock().unwrap();
        let mut p = Progress {
            phase: Some(TaskPhase::Scanning),
            percent: None,
            step_index: Some(2),
            from: None,
            to: None,
            bytes: None,
            total: None,
            speed: None,
        };
        // Keep the last completed-file count between short files. A slow single
        // object is indeterminate; elapsed time is never invented byte progress.
        if s.object_started
            .is_some_and(|t| now.saturating_duration_since(t) >= Duration::from_secs(1))
        {
            return p;
        }
        if let Some((done, total, started)) = s.volume.filter(|(_, t, _)| *t > 0) {
            let ratio = (done as f64 / total as f64).min(1.0);
            let weighted =
                (s.weight_done as f64 + s.weight as f64 * ratio) / s.weight_total.max(1) as f64;
            // Pre-fetch occupies 0..60, scan 60..70, existing install starts at 70.
            p.percent = Some(60.0 + 10.0 * weighted.min(1.0) as f32);
            p.bytes = Some(done);
            p.total = Some(total);
            p.speed = Some(
                (done as f64
                    / now
                        .saturating_duration_since(started)
                        .as_secs_f64()
                        .max(0.001)) as u64,
            );
        }
        p
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn opaque_objects_are_indeterminate_and_mounted_completion_never_regresses() {
        let m = Meter::default();
        m.plan(400);
        m.begin(100);
        let p = m.progress();
        assert!(p.percent.is_none() && p.bytes.is_none() && p.total.is_none() && p.speed.is_none());
        m.finish();
        m.begin(300);
        m.volume(200);
        m.file_started();
        m.file_finished(50);
        let before = m.progress();
        assert_eq!((before.bytes, before.total), (Some(50), Some(200)));
        m.file_started();
        let p = m.progress_at(Instant::now() + Duration::from_secs(2));
        assert!(p.percent.is_none() && p.bytes.is_none() && p.total.is_none() && p.speed.is_none());
        m.file_finished(100);
        let after = m.progress();
        assert_eq!(after.bytes, Some(150));
        assert!(after.percent > before.percent);
        assert!(after.percent.unwrap() < 70.0);
    }
}
