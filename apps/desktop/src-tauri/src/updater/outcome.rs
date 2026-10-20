//! Internal aggregation for a completed check. No update candidates are discarded
//! just because a different app or source failed.
use super::UpdateSource;
use crate::{
    AppError,
    model::{LocalApp, UpdateCheckFailure, UpdateCheckFailureReason},
};

#[derive(Default)]
pub struct CheckOutcome {
    successes: usize,
    pub details: Vec<UpdateCheckFailure>,
    rules_error: Option<AppError>,
    failures: Vec<(UpdateSource, AppError)>,
}

impl CheckOutcome {
    pub fn succeeded(&mut self) {
        self.successes += 1;
    }

    pub fn failed(&mut self, source: UpdateSource, context: &str, error: AppError) {
        log::warn!("Update check failed: source={source:?} context={context} error={error:?}");
        self.details.push(UpdateCheckFailure {
            source: Some(source),
            app_id: None,
            app_name: None,
            reason: match error.message.as_str() {
                "feed_rate_limited" => UpdateCheckFailureReason::RateLimited,
                "invalid_update_url" | "update_url_requires_https" => {
                    UpdateCheckFailureReason::InvalidUrl
                }
                "feed_request" => UpdateCheckFailureReason::Network,
                _ => UpdateCheckFailureReason::Failed,
            },
        });
        self.failures.push((source, error));
    }

    pub fn failed_app(&mut self, source: UpdateSource, app: &LocalApp, error: AppError) {
        self.failed(
            source,
            &format!("app_id={} name={:?}", app.id, app.name),
            error,
        );
        if let Some(detail) = self.details.last_mut() {
            detail.app_id = Some(app.id.clone());
            detail.app_name = Some(app.name.clone());
        }
    }

    pub fn rules_failed(&mut self, error: AppError) {
        log::warn!(
            "Update rules unavailable; using cached rules: code={}",
            error.code
        );
        self.details.push(UpdateCheckFailure {
            source: None,
            app_id: None,
            app_name: None,
            reason: UpdateCheckFailureReason::RulesUnavailable,
        });
        self.rules_error = Some(error);
    }

    pub fn extend(&mut self, other: Self) {
        self.details.extend(other.details);
        self.rules_error = self.rules_error.take().or(other.rules_error);
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
        if self.successes == 0
            && let Some(error) = &self.rules_error
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
    fn rules_failure_is_separate_and_survives_aggregation_without_hiding_success() {
        let mut native = CheckOutcome::default();
        native.rules_failed(AppError::new("E_UPDATE_FEED", "rules_unavailable"));
        assert!(native.failed_sources().is_empty());
        assert!(native.result().is_err());
        let mut overall = CheckOutcome::default();
        overall.succeeded();
        overall.extend(native);
        assert!(overall.result().is_ok());
        assert!(overall.failed_sources().is_empty());
        assert_eq!(overall.details.len(), 1);
        assert_eq!(
            overall.details[0].reason,
            UpdateCheckFailureReason::RulesUnavailable
        );
        assert!(CheckOutcome::default().details.is_empty());
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
