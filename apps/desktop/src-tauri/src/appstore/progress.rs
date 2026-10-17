//! Sidecar stages use the existing task progress/event contract, without a new
//! event or phase enum. Percent is local to the step, not overall task progress.
use crate::{AppError, brew::pty::Progress, model::TaskPhase};

pub fn stage(phase: &str, percent: Option<f32>) -> Result<Progress, AppError> {
    let (phase, step, percent) = match phase {
        "purchasing" => (TaskPhase::Preparing, 0, None),
        "downloading" => (TaskPhase::Downloading, 1, percent),
        "downloaded" => (TaskPhase::Downloading, 1, Some(100.0)),
        "installing" => (TaskPhase::Installing, 2, percent),
        "done" => (TaskPhase::Finishing, 3, Some(100.0)),
        _ => return Err(AppError::new("E_MAS_ENGINE", "unknown_store_phase")),
    };
    if percent.is_some_and(|p| !p.is_finite() || !(0.0..=100.0).contains(&p)) {
        return Err(AppError::new("E_MAS_ENGINE", "invalid_store_percent"));
    }
    Ok(Progress {
        phase: Some(phase),
        percent,
        step_index: Some(step),
        from: None,
        to: None,
        bytes: None,
        total: None,
        speed: None,
    })
}
