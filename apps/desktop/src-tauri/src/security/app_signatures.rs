// SPDX-License-Identifier: GPL-3.0-only
use super::*;
use crate::uninstall::types::LocalApp;
use std::collections::BTreeMap;

const REPORT: &str = "security_s2_signature_report";
const BASELINE: &str = "security_s2_signature_baseline";

impl Service {
    /// The last report, without the apps that are no longer on this Mac: one that was uninstalled since the check is not
    /// a problem to show, nor part of how many apps were checked.
    pub fn app_signature_get(&self) -> Result<Option<AppSignatureReport>, AppError> {
        Ok(self
            .s2_get::<AppSignatureReport>(REPORT)?
            .map(|mut report| {
                report.apps.retain(|app| Path::new(&app.path).exists());
                report
            }))
    }
    pub fn app_signature_check(&self, apps: &[LocalApp]) -> Result<AppSignatureReport, AppError> {
        let own_data = self.own_data()?;
        self.check_signatures_with(apps, |path| {
            signature::inspect_guarded(path, Some(&own_data))
        })
    }
    fn check_signatures_with(
        &self,
        apps: &[LocalApp],
        inspect: impl Fn(&Path) -> Result<SignatureInfo, AppError>,
    ) -> Result<AppSignatureReport, AppError> {
        let _guard = self.signature_audit.lock().map_err(poison)?;
        let previous: BTreeMap<String, SignatureInfo> = self.s2_get(BASELINE)?.unwrap_or_default();
        let mut baseline = BTreeMap::new();
        let mut report = AppSignatureReport {
            checked_at: now(),
            apps: vec![],
        };
        if apps.len() > 10_000 {
            return Err(AppError::new("E_INVALID_ARG", "signature_app_count"));
        }
        for app in apps {
            let old = previous.get(&app.path);
            let mut entry = AppSignatureEntry {
                path: app.path.clone(),
                name: app.name.clone(),
                bundle_id: app.bundle_id.clone(),
                signature: None,
                previous_team_id: old.and_then(|s| s.team_id.clone()),
                issues: vec![],
                changed: false,
                error_code: None,
            };
            match inspect(Path::new(&app.path)) {
                Ok(current) => {
                    match current.status {
                        SignatureStatus::Modified => entry.issues.push(AppSignatureIssue::Modified),
                        SignatureStatus::Unknown => {
                            entry.issues.push(AppSignatureIssue::Unreadable)
                        }
                        SignatureStatus::Invalid => entry.issues.push(AppSignatureIssue::Invalid),
                        SignatureStatus::Unsigned => entry.issues.push(AppSignatureIssue::Unsigned),
                        SignatureStatus::Adhoc => entry.issues.push(AppSignatureIssue::Adhoc),
                        _ => {}
                    }
                    if current.notarized == Some(false)
                        && current.status == SignatureStatus::DeveloperId
                    {
                        entry.issues.push(AppSignatureIssue::NotNotarized);
                    }
                    if current.status != SignatureStatus::Unknown
                        && old.is_some_and(|s| s.team_id != current.team_id)
                    {
                        entry.issues.push(AppSignatureIssue::TeamChanged);
                    }
                    entry.changed = current.status != SignatureStatus::Unknown
                        && old.is_some_and(|s| *s != current);
                    if current.status == SignatureStatus::Unknown {
                        if let Some(old) = old {
                            baseline.insert(app.path.clone(), old.clone());
                        }
                    } else {
                        baseline.insert(app.path.clone(), current.clone());
                    }
                    entry.signature = Some(current);
                }
                Err(e) => {
                    entry.error_code = Some(e.code);
                    entry.issues.push(AppSignatureIssue::Unreadable);
                    if let Some(old) = old {
                        baseline.insert(app.path.clone(), old.clone());
                    }
                }
            }
            report.apps.push(entry);
        }
        // Store both atomically: an interruption cannot advance a baseline without its report.
        self.database.with(|c| {
            let tx = c.unchecked_transaction()?;
            for (key, json) in [(REPORT, serde_json::to_string(&report)?), (BASELINE, serde_json::to_string(&baseline)?)] {
                tx.execute("INSERT INTO meta(key,value) VALUES(?1,?2) ON CONFLICT(key) DO UPDATE SET value=excluded.value", params![key,json])?;
            }
            tx.commit()?;
            Ok(())
        })?;
        let changed: Vec<_> = report.apps.iter().filter(|a| a.changed).cloned().collect();
        for app in &changed {
            self.record_event(
                ProtectionEventKind::SignatureChanged,
                &app.path,
                None,
                false,
            )?;
        }
        if !changed.is_empty() {
            (self.sink)(CoreEvent::AppSignaturesChanged(AppSignaturesChanged {
                checked_at: report.checked_at,
                apps: changed,
            }));
        }
        self.changed(SecurityChangeReason::Signatures);
        Ok(report)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn changed_team_and_failed_reads_preserve_the_last_successful_baseline() {
        let (_tmp, s, _) = super::super::tests::fixture();
        let app: LocalApp = serde_json::from_value(serde_json::json!({"id":"one","path":"/Applications/Fixture.app","name":"Fixture","source":"direct","running":false,"removable":true})).unwrap();
        let sig = |team: &str| SignatureInfo {
            status: SignatureStatus::DeveloperId,
            team_id: Some(team.into()),
            signer: None,
            notarized: Some(true),
        };
        let first = s
            .check_signatures_with(std::slice::from_ref(&app), |_| Ok(sig("FIRST")))
            .unwrap();
        assert!(!first.apps[0].changed);
        let second = s
            .check_signatures_with(std::slice::from_ref(&app), |_| Ok(sig("SECOND")))
            .unwrap();
        assert!(
            second.apps[0]
                .issues
                .contains(&AppSignatureIssue::TeamChanged)
        );
        assert_eq!(second.apps[0].previous_team_id.as_deref(), Some("FIRST"));
        s.check_signatures_with(std::slice::from_ref(&app), |_| {
            Err(AppError::new("E_PROTECTED", "fixture"))
        })
        .unwrap();
        let last = s
            .check_signatures_with(&[app], |_| Ok(sig("SECOND")))
            .unwrap();
        assert!(!last.apps[0].changed);
        assert!(s.app_signature_get().unwrap().is_some());
    }
    #[test]
    fn reading_the_report_leaves_out_apps_that_are_gone() {
        let (tmp, s, _) = super::super::tests::fixture();
        let kept = tmp.path().join("Kept.app");
        let removed = tmp.path().join("Removed.app");
        std::fs::create_dir(&kept).unwrap();
        std::fs::create_dir(&removed).unwrap();
        let app = |path: &Path, name: &str| -> LocalApp {
            serde_json::from_value(serde_json::json!({"id":name,"path":path,"name":name,"source":"direct","running":false,"removable":true})).unwrap()
        };
        let apps = [app(&kept, "Kept"), app(&removed, "Removed")];
        let checked = s
            .check_signatures_with(&apps, |_| Ok(signature::empty(SignatureStatus::Invalid)))
            .unwrap();
        assert_eq!(checked.apps.len(), 2);
        assert_eq!(s.app_signature_get().unwrap().unwrap().apps.len(), 2);
        // Uninstalled since the check.
        std::fs::remove_dir(&removed).unwrap();
        let read = s.app_signature_get().unwrap().unwrap();
        assert_eq!(read.checked_at, checked.checked_at);
        assert_eq!(
            read.apps
                .iter()
                .map(|a| a.name.as_str())
                .collect::<Vec<_>>(),
            ["Kept"]
        );
    }
    #[test]
    fn modified_has_its_own_issue_and_unknown_does_not_replace_identity() {
        let (_tmp, s, _) = super::super::tests::fixture();
        let app: LocalApp = serde_json::from_value(serde_json::json!({"id":"one","path":"/Applications/Fixture.app","name":"Fixture","source":"direct","running":false,"removable":true})).unwrap();
        let modified = SignatureInfo {
            status: SignatureStatus::Modified,
            team_id: Some("ABCDEFGHIJ".into()),
            signer: Some("Fixture".into()),
            notarized: Some(false),
        };
        let report = s
            .check_signatures_with(std::slice::from_ref(&app), |_| Ok(modified.clone()))
            .unwrap();
        assert_eq!(report.apps[0].issues, vec![AppSignatureIssue::Modified]);
        let unknown = s
            .check_signatures_with(std::slice::from_ref(&app), |_| {
                Ok(signature::empty(SignatureStatus::Unknown))
            })
            .unwrap();
        assert!(!unknown.apps[0].changed);
        assert_eq!(unknown.apps[0].issues, vec![AppSignatureIssue::Unreadable]);
        let again = s
            .check_signatures_with(&[app], |_| Ok(modified.clone()))
            .unwrap();
        assert!(!again.apps[0].changed);
        assert_eq!(
            again.apps[0].previous_team_id.as_deref(),
            Some("ABCDEFGHIJ")
        );
    }
}
