//! Internal aggregation for a completed check. No update candidates are discarded
//! just because a different app or source failed.
use super::UpdateSource;
use crate::AppError;

#[derive(Default)]
pub struct CheckOutcome {
    successes: usize,
    failures: Vec<(UpdateSource, AppError)>,
}

impl CheckOutcome {
    pub fn succeeded(&mut self) {
        self.successes += 1;
    }

    pub fn failed(&mut self, source: UpdateSource, context: &str, error: AppError) {
        log::warn!("Update check failed: source={source:?} context={context} error={error:?}");
        self.failures.push((source, error));
    }

    pub fn extend(&mut self, other: Self) {
        self.successes += other.successes;
        self.failures.extend(other.failures);
    }

    pub fn failed_sources(&self) -> Vec<UpdateSource> {
        UpdateSource::ALL
            .into_iter()
            .filter(|s| self.failures.iter().any(|(source, _)| source == s))
            .collect()
    }

    pub fn result(&self) -> Result<(), AppError> {
        if self.successes == 0
            && let Some((_, error)) = self.failures.first()
        {
            return Err(error.clone());
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn partial_app_and_source_failures_do_not_discard_success_or_empty_success() {
        let mut native = CheckOutcome::default();
        native.failed(
            UpdateSource::Electron,
            "one_app",
            AppError::new("E_UPDATE_FEED", "failed"),
        );
        assert_eq!(native.result().unwrap_err().code, "E_UPDATE_FEED");
        native.succeeded(); // Another app checked successfully, even with no newer version.
        assert!(native.result().is_ok());
        native.failed(
            UpdateSource::Electron,
            "another_app",
            AppError::new("E_UPDATE_FEED", "failed"),
        );
        assert_eq!(native.failed_sources(), [UpdateSource::Electron]);
        let mut overall = CheckOutcome::default();
        overall.failed(
            UpdateSource::Homebrew,
            "brew",
            AppError::new("E_BREW", "failed"),
        );
        overall.extend(native);
        assert!(overall.result().is_ok());
        assert_eq!(
            overall.failed_sources(),
            [UpdateSource::Homebrew, UpdateSource::Electron]
        );
    }
    #[test]
    fn no_work_does_not_hide_an_actual_failure_and_next_check_starts_clean() {
        let mut overall = CheckOutcome::default();
        assert!(overall.result().is_ok());
        overall.failed(
            UpdateSource::Github,
            "rules",
            AppError::new("E_UPDATE_FEED", "failed"),
        );
        overall.extend(CheckOutcome::default());
        assert!(overall.result().is_err());
        let fresh = CheckOutcome::default();
        assert!(fresh.failed_sources().is_empty());
    }
}
