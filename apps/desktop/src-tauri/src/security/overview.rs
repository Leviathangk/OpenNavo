use super::*;
use rusqlite::OptionalExtension;
impl Service {
    pub fn overview(&self) -> Result<SecurityOverview, AppError> {
        let posture = self.posture();
        let engine = self.av.status()?;
        let items = self.saved_startup()?;
        let quarantine = self.quarantine_list()?;
        let last = self.database.with(|c| {
            Ok(c.query_row(
                "SELECT id FROM security_scans ORDER BY started_at DESC LIMIT 1",
                [],
                |r| r.get::<_, String>(0),
            )
            .optional()?)
        })?;
        let last_scan = last
            .map(|id| self.scan_get(&id))
            .transpose()?
            .map(|r| AvScanSummary {
                scan_id: r.scan_id,
                kind: r.kind,
                state: r.state,
                finished_at: r.finished_at,
                files_scanned: r.files_scanned,
                threats: r.findings.len() as u64,
            });
        let startup = StartupOverview {
            total: items.len() as u64,
            third_party: items
                .iter()
                .filter(|i| i.trust != StartupTrust::Apple)
                .count() as u64,
            untrusted: items
                .iter()
                .filter(|i| i.trust == StartupTrust::Untrusted)
                .count() as u64,
            pending_review: items.iter().filter(|i| i.is_new).count() as u64,
            changed_at: self
                .database
                .meta("startup_baseline")?
                .and_then(|v| v.parse().ok()),
        };
        let settings = self.settings.read().map_err(poison)?.clone();
        let open_findings = self.database.with(|c| {
            let mut statement = c.prepare("SELECT value FROM security_findings")?;
            let rows = statement
                .query_map([], |r| r.get::<_, String>(0))?
                .collect::<rusqlite::Result<Vec<_>>>()?;
            let mut open = 0_u64;
            for row in rows {
                let finding: AvFinding = serde_json::from_str(&row)?;
                open += u64::from(finding.status == FindingStatus::Open);
            }
            Ok(open)
        })?;
        let attention = open_findings + startup.pending_review;
        Ok(SecurityOverview {
            posture,
            engine,
            last_scan,
            startup,
            protection: ProtectionOverview {
                startup_alerts: settings.startup_alerts,
                watch_downloads: settings.watch_downloads && settings.av_consent,
                endpoint_security: ProtectionState::Unavailable,
                firewall: ProtectionState::Unavailable,
            },
            quarantine: QuarantineOverview {
                count: quarantine.len() as u64,
                bytes: quarantine.iter().map(|q| q.size_bytes).sum(),
            },
            attention,
        })
    }
}
