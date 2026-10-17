// SPDX-License-Identifier: GPL-3.0-only
use super::*;
use chrono::{DateTime, Datelike, Days, Local, NaiveDate, NaiveTime, TimeZone};

const LAST: &str = "security_s2_schedule_run";
const PLAN: &str = "security_s2_schedule_plan";

#[derive(serde::Serialize, serde::Deserialize)]
struct Plan {
    fingerprint: String,
    effective_at: i64,
}

fn on_date<T: TimeZone>(date: NaiveDate, settings: &Settings, zone: &T) -> Option<DateTime<T>> {
    if settings.av_schedule == AvSchedule::Off
        || (settings.av_schedule == AvSchedule::Weekly
            && date.weekday().number_from_monday() != u32::from(settings.av_schedule_weekday))
    {
        return None;
    }
    let time = NaiveTime::parse_from_str(&settings.av_schedule_time, "%H:%M").ok()?;
    // Ambiguous autumn time runs once at the first occurrence. A nonexistent
    // spring time is skipped; there is no timer loop or repeated catch-up.
    zone.from_local_datetime(&date.and_time(time)).earliest()
}

fn next<T: TimeZone>(
    at: &DateTime<T>,
    settings: &Settings,
    last: Option<&ScheduledScanRun>,
    effective_at: i64,
) -> Option<i64> {
    (0..=7).find_map(|offset| {
        let date = at.date_naive().checked_add_days(Days::new(offset))?;
        let candidate = on_date(date, settings, &at.timezone())?;
        if candidate.timestamp_millis() < effective_at {
            return None;
        }
        let consumed = last.is_some_and(|last| {
            at.timezone()
                .timestamp_millis_opt(last.scheduled_at)
                .single()
                .is_some_and(|v| v.date_naive() == date)
        });
        (!consumed).then(|| candidate.timestamp_millis().max(at.timestamp_millis()))
    })
}

impl Service {
    // Caller holds scheduled before reading/publishing settings, so the timer
    // cannot observe a new plan with the old activation time (or vice versa).
    fn plan(&self, settings: &Settings, at: i64) -> Result<Plan, AppError> {
        let fingerprint = format!(
            "{:?}|{}|{}",
            settings.av_schedule, settings.av_schedule_time, settings.av_schedule_weekday
        );
        if let Some(plan) = self.s2_get::<Plan>(PLAN)?
            && plan.fingerprint == fingerprint
        {
            return Ok(plan);
        }
        let plan = Plan {
            fingerprint,
            effective_at: at,
        };
        self.s2_set(PLAN, &plan)?;
        Ok(plan)
    }

    pub(crate) fn record_schedule_settings(
        &self,
        settings: &Settings,
        at: i64,
    ) -> Result<(), AppError> {
        self.plan(settings, at).map(|_| ())
    }

    pub(crate) fn s2_get<T: serde::de::DeserializeOwned>(
        &self,
        key: &str,
    ) -> Result<Option<T>, AppError> {
        self.database
            .meta(key)?
            .map(|v| serde_json::from_str(&v).map_err(Into::into))
            .transpose()
    }
    pub(crate) fn s2_set<T: serde::Serialize>(&self, key: &str, value: &T) -> Result<(), AppError> {
        self.database.set_meta(key, &serde_json::to_string(value)?)
    }
    pub fn schedule_status(&self) -> Result<AvScheduleStatus, AppError> {
        self.schedule_status_at(Local::now())
    }
    fn schedule_status_at<T: TimeZone>(
        &self,
        at: DateTime<T>,
    ) -> Result<AvScheduleStatus, AppError> {
        let _guard = self.scheduled.lock().map_err(poison)?;
        let last_run = self.s2_get(LAST)?;
        let settings = self.settings.read().map_err(poison)?;
        let plan = self.plan(&settings, at.timestamp_millis())?;
        Ok(AvScheduleStatus {
            next_run_at: next(&at, &settings, last_run.as_ref(), plan.effective_at),
            last_run,
        })
    }
    pub(crate) fn schedule_tick(
        self: &Arc<Self>,
        at: DateTime<Local>,
        on_battery: bool,
    ) -> Result<Option<ScheduledScanRun>, AppError> {
        self.schedule_tick_at(at, on_battery, |service| {
            service.scan_start(AvScanKind::Quick, None)
        })
    }
    fn schedule_tick_at<T: TimeZone>(
        self: &Arc<Self>,
        at: DateTime<T>,
        on_battery: bool,
        start: impl FnOnce(&Arc<Service>) -> Result<AvScanStarted, AppError>,
    ) -> Result<Option<ScheduledScanRun>, AppError> {
        let _guard = self.scheduled.lock().map_err(poison)?;
        let settings = self.settings.read().map_err(poison)?.clone();
        let plan = self.plan(&settings, at.timestamp_millis())?;
        let Some(due) = on_date(at.date_naive(), &settings, &at.timezone())
            .filter(|v| v <= &at && v.timestamp_millis() >= plan.effective_at)
        else {
            return Ok(None);
        };
        let last: Option<ScheduledScanRun> = self.s2_get(LAST)?;
        if last.as_ref().is_some_and(|r| {
            at.timezone()
                .timestamp_millis_opt(r.scheduled_at)
                .single()
                .is_some_and(|d| d.date_naive() >= at.date_naive())
        }) {
            return Ok(None);
        }
        let mut run = ScheduledScanRun {
            scheduled_at: due.timestamp_millis(),
            scan_id: None,
            skipped: None,
            error_code: None,
        };
        // Persist the claim before starting a thread: restart or clock rollback
        // must never run today's slot a second time.
        self.s2_set(LAST, &run)?;
        run.skipped = if !settings.av_consent {
            Some(ScheduleSkipReason::NoConsent)
        } else if self.av.ready().is_err() {
            Some(ScheduleSkipReason::NoDatabase)
        } else if on_battery {
            Some(ScheduleSkipReason::OnBattery)
        } else if self.active_scan.lock().map_err(poison)?.is_some() {
            Some(ScheduleSkipReason::Busy)
        } else {
            None
        };
        if run.skipped.is_none() {
            match start(self) {
                Ok(scan) => run.scan_id = Some(scan.scan_id),
                Err(e) => {
                    run.skipped = Some(ScheduleSkipReason::Error);
                    run.error_code = Some(e.code);
                }
            }
        }
        self.s2_set(LAST, &run)?;
        if let Some(reason) = run.skipped {
            self.record_event(
                ProtectionEventKind::ScheduledScanSkipped,
                &format!(
                    "scheduled_scan:{}",
                    serde_json::to_value(reason)?.as_str().unwrap_or("error")
                ),
                None,
                true,
            )?;
        }
        (self.sink)(CoreEvent::AvScheduleRun(run.clone()));
        self.changed(SecurityChangeReason::Schedule);
        Ok(Some(run))
    }
}

pub(crate) fn on_battery(commands: &dyn Commands) -> bool {
    commands
        .run("/usr/bin/pmset", &["-g", "batt"])
        .map(|s| s.contains("Battery Power"))
        .unwrap_or(true)
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::FixedOffset;
    #[test]
    fn schedule_defaults_weekdays_catchup_and_restart_claims() {
        let (_temp, s, _) = super::super::tests::fixture();
        let zone = FixedOffset::east_opt(8 * 3600).unwrap();
        let at = zone.with_ymd_and_hms(2026, 10, 16, 4, 0, 0).unwrap();
        assert!(
            s.schedule_tick_at(at, false, |_| panic!("off"))
                .unwrap()
                .is_none()
        );
        s.settings.write().unwrap().av_schedule = AvSchedule::Daily;
        s.record_schedule_settings(
            &s.settings.read().unwrap(),
            (at - chrono::Duration::hours(3)).timestamp_millis(),
        )
        .unwrap();
        let run = s
            .schedule_tick_at(at, false, |_| panic!("consent"))
            .unwrap()
            .unwrap();
        assert_eq!(run.skipped, Some(ScheduleSkipReason::NoConsent));
        assert!(
            s.schedule_tick_at(at, false, |_| panic!("repeat"))
                .unwrap()
                .is_none()
        );
        let settings = s.settings.read().unwrap().clone();
        let next_at = next(&at, &settings, Some(&run), 0).unwrap();
        assert_eq!(zone.timestamp_millis_opt(next_at).unwrap().day(), 17);
        s.settings.write().unwrap().av_consent = true;
        let next_day = at + chrono::Duration::days(1);
        assert_eq!(
            s.schedule_tick_at(next_day, false, |_| panic!("db"))
                .unwrap()
                .unwrap()
                .skipped,
            Some(ScheduleSkipReason::NoDatabase)
        );
        super::super::tests::fake_engine(&s);
        assert_eq!(
            s.schedule_tick_at(next_day + chrono::Duration::days(1), true, |_| panic!(
                "battery"
            ))
            .unwrap()
            .unwrap()
            .skipped,
            Some(ScheduleSkipReason::OnBattery)
        );
        let run = s
            .schedule_tick_at(next_day + chrono::Duration::days(2), false, |_| {
                Ok(AvScanStarted {
                    scan_id: "fixture".into(),
                })
            })
            .unwrap()
            .unwrap();
        assert_eq!(run.scan_id.as_deref(), Some("fixture"));
        let weekly = Settings {
            av_schedule: AvSchedule::Weekly,
            av_schedule_weekday: 1,
            ..settings
        };
        assert!(on_date(at.date_naive(), &weekly, &zone).is_none());
        assert_eq!(
            zone.timestamp_millis_opt(next(&at, &weekly, None, 0).unwrap())
                .unwrap()
                .weekday()
                .number_from_monday(),
            1
        );
        assert_eq!(
            s.s2_get::<ScheduledScanRun>(LAST).unwrap().unwrap().scan_id,
            run.scan_id
        );
    }

    #[test]
    fn enabling_or_changing_a_plan_does_not_backfill_but_restart_does() {
        let (_temp, s, commands) = super::super::tests::fixture();
        let zone = FixedOffset::east_opt(8 * 3600).unwrap();
        let evening = zone.with_ymd_and_hms(2026, 10, 16, 19, 22, 0).unwrap();
        s.settings.write().unwrap().av_schedule = AvSchedule::Daily;
        s.record_schedule_settings(&s.settings.read().unwrap(), evening.timestamp_millis())
            .unwrap();
        let tomorrow = zone.with_ymd_and_hms(2026, 10, 17, 3, 0, 0).unwrap();
        assert_eq!(
            s.schedule_status_at(evening).unwrap().next_run_at,
            Some(tomorrow.timestamp_millis())
        );
        assert!(
            s.schedule_tick_at(evening, false, |_| panic!("new plan must not catch up"))
                .unwrap()
                .is_none()
        );

        let path = s
            .database
            .with(|c| Ok(PathBuf::from(c.path().unwrap())))
            .unwrap();
        let restarted = Service::new(
            Database::open(&path).unwrap(),
            s.settings.clone(),
            Arc::new(|_| {}),
            s.home.clone(),
            s.data.clone(),
            s.av.config.clone(),
            commands,
        );
        let morning = tomorrow + chrono::Duration::hours(1);
        assert_eq!(
            restarted.schedule_status_at(morning).unwrap().next_run_at,
            Some(morning.timestamp_millis())
        );
        let run = restarted
            .schedule_tick_at(morning, false, |_| panic!("no consent"))
            .unwrap()
            .unwrap();
        assert_eq!(run.scheduled_at, tomorrow.timestamp_millis());
        assert_eq!(run.skipped, Some(ScheduleSkipReason::NoConsent));
        assert!(
            restarted
                .schedule_tick_at(morning, false, |_| panic!("duplicate"))
                .unwrap()
                .is_none()
        );

        let later = morning + chrono::Duration::days(1);
        restarted.settings.write().unwrap().av_schedule_time = "02:00".into();
        restarted
            .record_schedule_settings(
                &restarted.settings.read().unwrap(),
                later.timestamp_millis(),
            )
            .unwrap();
        assert_eq!(
            restarted.schedule_status_at(later).unwrap().next_run_at,
            Some(
                zone.with_ymd_and_hms(2026, 10, 19, 2, 0, 0)
                    .unwrap()
                    .timestamp_millis()
            )
        );
        assert!(
            restarted
                .schedule_tick_at(later, false, |_| panic!("changed time must not catch up"))
                .unwrap()
                .is_none()
        );
        restarted.settings.write().unwrap().av_schedule_weekday = 7;
        restarted
            .record_schedule_settings(
                &restarted.settings.read().unwrap(),
                later.timestamp_millis(),
            )
            .unwrap();
        assert!(
            restarted
                .schedule_tick_at(later, false, |_| panic!(
                    "changed weekday must not catch up"
                ))
                .unwrap()
                .is_none()
        );
    }
}
