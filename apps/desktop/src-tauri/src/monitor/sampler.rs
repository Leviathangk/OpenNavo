use super::{collect::*, engine::Engine, types::*};
use crate::AppError;
use std::{
    collections::{BTreeMap, BTreeSet},
    sync::{Arc, Mutex},
    time::Instant,
};
use tokio::sync::Notify;

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum PauseReason {
    Sleep,
    Screen,
    Lock,
    Session,
}
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Plan {
    pub modules: Modules,
    pub interval_ms: u32,
    pub processes: bool,
}
#[derive(Clone)]
pub struct Publication {
    pub sample: MonitorSample,
    pub available: Modules,
    pub menu: MonitorMenuBar,
    pub windows: Vec<String>,
    pub menu_due: bool,
    pub history: Vec<MonitorSample>,
    pub alerts: Vec<HealthIssue>,
}
pub type Sink = Arc<dyn Fn(Publication) + Send + Sync>;
const PROBE_INTERVAL_MS: u64 = 30_000;
const PROBE_ATTEMPTS: u8 = 3;
struct Probe {
    remaining: u8,
    next_at: u64,
}
struct State {
    engine: Engine,
    capable: Modules,
    wanted: Modules,
    probes: BTreeMap<MonitorModule, Probe>,
    views: BTreeMap<(String, MonitorView), Modules>,
    hidden: BTreeSet<String>,
    destroyed: BTreeSet<String>,
    instances: BTreeMap<String, u64>,
    next_instance: u64,
    menu: MonitorMenuBar,
    interval_ms: u32,
    paused: BTreeSet<PauseReason>,
    generation: u64,
    menu_at: Option<u64>,
    sensors_at: Option<u64>,
    fans_at: Option<u64>,
    sample_at: Option<u64>,
    power_at: Option<u64>,
    smart_at: Option<u64>,
    core_at: BTreeMap<MonitorModule, u64>,
    alerts: super::alerts::AlertTracker,
}
pub struct Sampler {
    state: Mutex<State>,
    collector: Mutex<Box<dyn Collector>>,
    notify: Notify,
    started: std::sync::atomic::AtomicBool,
    origin: Instant,
    sink: Mutex<Option<Sink>>,
    network_active: Option<Arc<std::sync::atomic::AtomicBool>>,
    power_epoch: Option<Arc<std::sync::atomic::AtomicU64>>,
}
impl Sampler {
    pub fn new(
        mut collector: Box<dyn Collector>,
        menu: MonitorMenuBar,
        interval: MonitorIntervalMs,
    ) -> Arc<Self> {
        let network_active = collector.network_active_flag();
        let power_epoch = collector.power_epoch();
        let system = collector.system();
        let probe = collector.collect(&all_modules(), false);
        let engine = Engine::new(system, probe);
        let mut state = State {
            capable: engine.available.clone(),
            wanted: Modules::new(),
            probes: BTreeMap::new(),
            engine,
            views: BTreeMap::new(),
            hidden: BTreeSet::new(),
            destroyed: BTreeSet::new(),
            instances: BTreeMap::new(),
            next_instance: 0,
            menu,
            interval_ms: interval.into(),
            paused: BTreeSet::new(),
            generation: 0,
            menu_at: None,
            sensors_at: None,
            fans_at: None,
            sample_at: None,
            power_at: None,
            smart_at: None,
            core_at: BTreeMap::new(),
            alerts: super::alerts::AlertTracker::default(),
        };
        Self::changed(&mut state);
        Arc::new(Self {
            state: Mutex::new(state),
            collector: Mutex::new(collector),
            notify: Notify::new(),
            started: std::sync::atomic::AtomicBool::new(false),
            origin: Instant::now(),
            sink: Mutex::new(None),
            network_active,
            power_epoch,
        })
    }
    fn lock(&self) -> Result<std::sync::MutexGuard<'_, State>, AppError> {
        self.state
            .lock()
            .map_err(|_| AppError::new("E_UNKNOWN", "monitor_state"))
    }
    /// Only native window creation may register a new instance, never show/focus/IPC.
    pub fn register_window(&self, window: &str) -> Result<u64, AppError> {
        let mut state = self.lock()?;
        state.next_instance += 1;
        let instance = state.next_instance;
        state.instances.insert(window.into(), instance);
        state.destroyed.remove(window);
        state.hidden.remove(window);
        state.views.retain(|(label, _), _| label != window);
        Self::changed(&mut state);
        self.update_network_flag(&state);
        drop(state);
        self.notify.notify_one();
        Ok(instance)
    }
    pub fn window_instance_matches(&self, window: &str, instance: u64) -> bool {
        self.lock()
            .is_ok_and(|state| state.instances.get(window) == Some(&instance))
    }
    pub fn window_instance_active(&self, window: &str, instance: u64) -> bool {
        self.lock()
            .is_ok_and(|state| Self::validate_instance(&state, window, instance).is_ok())
    }
    fn validate_instance(state: &State, window: &str, instance: u64) -> Result<(), AppError> {
        if state.destroyed.contains(window) || state.instances.get(window) != Some(&instance) {
            return Err(AppError::new(
                "E_MONITOR_UNAVAILABLE",
                "monitor_window_closed",
            ));
        }
        Ok(())
    }
    pub fn destroy_window_instance(&self, window: &str, instance: u64) {
        if let Ok(mut state) = self.lock() {
            if state.instances.get(window) != Some(&instance) {
                return;
            }
            state.destroyed.insert(window.into());
            state.views.retain(|(label, _), _| label != window);
            state.hidden.remove(window);
            Self::changed(&mut state);
            self.update_network_flag(&state);
        }
        self.notify.notify_one();
    }
    fn visible_views(state: &State) -> impl Iterator<Item = &Modules> {
        state
            .views
            .iter()
            .filter(|((label, _), _)| !state.hidden.contains(label))
            .map(|(_, modules)| modules)
    }
    pub fn set_window_visible(&self, window: &str, visible: bool) {
        if let Ok(mut state) = self.lock() {
            let changed = if visible {
                state.hidden.remove(window)
            } else {
                state.hidden.insert(window.into())
            };
            if changed {
                Self::changed(&mut state);
                self.update_network_flag(&state);
            }
        }
        self.notify.notify_one();
    }
    fn plan_for(state: &State) -> Option<Plan> {
        if !state.paused.is_empty() {
            return None;
        }
        let mut modules: Modules = state
            .menu
            .enabled()
            .intersection(&state.capable)
            .copied()
            .collect();
        let mut visible = false;
        for wanted in Self::visible_views(state) {
            let added: Modules = wanted.intersection(&state.capable).copied().collect();
            if !added.is_empty() {
                visible = true;
                modules.extend(added);
            }
        }
        let menu_active = !modules.is_empty()
            && state
                .menu
                .enabled()
                .intersection(&state.capable)
                .next()
                .is_some();
        if state.alerts.enabled() {
            modules.extend(
                [
                    MonitorModule::Cpu,
                    MonitorModule::Memory,
                    MonitorModule::Disk,
                    MonitorModule::Battery,
                    MonitorModule::Sensors,
                ]
                .into_iter()
                .filter(|m| state.capable.contains(m)),
            );
        }
        (!modules.is_empty()).then_some(Plan {
            modules,
            interval_ms: if visible {
                1000
            } else if menu_active {
                state.interval_ms
            } else {
                state.interval_ms.max(5000)
            },
            processes: visible,
        })
    }
    fn wanted(state: &State) -> Modules {
        let mut wanted = state.menu.enabled();
        wanted.extend(Self::visible_views(state).flat_map(|modules| modules.iter().copied()));
        if state.alerts.enabled() {
            wanted.extend([
                MonitorModule::Cpu,
                MonitorModule::Memory,
                MonitorModule::Disk,
                MonitorModule::Battery,
                MonitorModule::Sensors,
            ]);
        }
        wanted
    }
    fn pending_probes(state: &State) -> impl Iterator<Item = (&MonitorModule, &Probe)> {
        state.probes.iter().filter(|(module, probe)| {
            state.paused.is_empty()
                && state.wanted.contains(module)
                && !state.capable.contains(module)
                && probe.remaining > 0
        })
    }
    /// Unknown hardware gets a bounded recovery path, separate from the regular plan.
    fn tick_plan(state: &State, now: u64) -> Option<Plan> {
        let retry: Modules = Self::pending_probes(state)
            .filter(|(_, probe)| now >= probe.next_at)
            .map(|(module, _)| *module)
            .collect();
        let mut plan = Self::plan_for(state);
        if !retry.is_empty() {
            plan.get_or_insert(Plan {
                modules: Modules::new(),
                interval_ms: PROBE_INTERVAL_MS as u32,
                processes: false,
            })
            .modules
            .extend(retry);
        }
        plan
    }
    fn next_delay(state: &State, now: u64) -> Option<u64> {
        let regular = Self::plan_for(state).map(|plan| u64::from(plan.interval_ms));
        let retry = Self::pending_probes(state)
            .map(|(_, probe)| probe.next_at.saturating_sub(now))
            .min();
        regular.into_iter().chain(retry).min()
    }
    pub fn plan(&self) -> Result<Option<Plan>, AppError> {
        let state = self.lock()?;
        Ok(Self::plan_for(&state))
    }
    fn changed(state: &mut State) {
        let wanted = Self::wanted(state);
        for module in wanted.difference(&state.wanted) {
            if !state.capable.contains(module) {
                // Preserve the cooldown across hide/show and unsubscribe/subscribe churn.
                state
                    .probes
                    .entry(*module)
                    .or_insert(Probe {
                        remaining: 0,
                        next_at: 0,
                    })
                    .remaining = PROBE_ATTEMPTS;
            }
        }
        state.wanted = wanted;
        if !state.paused.is_empty()
            || !Self::visible_views(state)
                .any(|v| v.contains(&MonitorModule::Cpu) || v.contains(&MonitorModule::Gpu))
        {
            state.power_at = None;
            state.engine.latest.cpu.power_watts = None;
            if let Some(gpu) = &mut state.engine.latest.gpu {
                gpu.power_watts = None;
            }
        }
        state.generation = state.generation.wrapping_add(1);
        if Self::plan_for(state).is_none() {
            state.engine.reset();
            state.core_at.clear();
            state.sensors_at = None;
            state.fans_at = None;
        }
    }
    pub fn subscribe(
        &self,
        window: &str,
        view: MonitorView,
        modules: Modules,
    ) -> Result<MonitorSnapshot, AppError> {
        self.subscribe_instance(window, None, view, modules)
    }
    fn subscribe_instance(
        &self,
        window: &str,
        instance: Option<u64>,
        view: MonitorView,
        modules: Modules,
    ) -> Result<MonitorSnapshot, AppError> {
        validate_view(window, view)?;
        let mut state = self.lock()?;
        if let Some(instance) = instance {
            Self::validate_instance(&state, window, instance)?;
        }
        if state.destroyed.contains(window) {
            return Ok(state.engine.snapshot());
        }
        state.views.insert((window.into(), view), modules);
        Self::changed(&mut state);
        self.update_network_flag(&state);
        let snapshot = state.engine.snapshot();
        drop(state);
        self.notify.notify_one();
        Ok(snapshot)
    }
    /// The first visible response includes fresh processes; native work stays off the IPC/UI thread.
    pub fn subscribe_fresh(
        &self,
        window: &str,
        view: MonitorView,
        modules: Modules,
    ) -> Result<MonitorSnapshot, AppError> {
        self.subscribe_fresh_at(
            window,
            view,
            modules,
            self.origin.elapsed().as_millis() as u64,
            chrono::Utc::now().timestamp_millis(),
        )
    }
    /// Commit subscription state before queuing native refresh work. The returned job only
    /// samples the current plan; it cannot restore a subscription removed while it waited.
    pub fn prepare_subscription(
        self: &Arc<Self>,
        window: &str,
        instance: u64,
        view: MonitorView,
        modules: Modules,
    ) -> Result<impl FnOnce() -> Result<MonitorSnapshot, AppError> + Send + 'static, AppError> {
        self.subscribe_instance(window, Some(instance), view, modules)?;
        let sampler = self.clone();
        Ok(move || {
            sampler.refresh_subscription(
                sampler.origin.elapsed().as_millis() as u64,
                chrono::Utc::now().timestamp_millis(),
            )
        })
    }
    pub fn subscribe_fresh_instance(
        &self,
        window: &str,
        instance: u64,
        view: MonitorView,
        modules: Modules,
    ) -> Result<MonitorSnapshot, AppError> {
        self.subscribe_instance(window, Some(instance), view, modules)?;
        self.refresh_subscription(
            self.origin.elapsed().as_millis() as u64,
            chrono::Utc::now().timestamp_millis(),
        )
    }
    pub fn subscribe_fresh_at(
        &self,
        window: &str,
        view: MonitorView,
        modules: Modules,
        now: u64,
        at: i64,
    ) -> Result<MonitorSnapshot, AppError> {
        self.subscribe(window, view, modules)?;
        self.refresh_subscription(now, at)
    }
    fn refresh_subscription(&self, now: u64, at: i64) -> Result<MonitorSnapshot, AppError> {
        let fresh = {
            let state = self.lock()?;
            state
                .sample_at
                .is_none_or(|last| now.saturating_sub(last) > 500)
                || state.engine.latest.processes.is_none()
                || Self::plan_for(&state).is_some_and(|plan| {
                    (plan.modules.contains(&MonitorModule::Sensors)
                        && state
                            .sensors_at
                            .is_none_or(|last| now.saturating_sub(last) >= 5000))
                        || (plan.modules.contains(&MonitorModule::Fans)
                            && state
                                .fans_at
                                .is_none_or(|last| now.saturating_sub(last) >= 5000))
                })
        };
        if fresh
            && let Some(publication) = self.tick_inner(now, at, true)?
            && let Some(sink) = self
                .sink
                .lock()
                .map_err(|_| AppError::new("E_UNKNOWN", "monitor_sink"))?
                .as_ref()
        {
            sink(publication);
        }
        self.snapshot()
    }
    pub fn unsubscribe(&self, window: &str, view: MonitorView) -> Result<(), AppError> {
        validate_view(window, view)?;
        let mut state = self.lock()?;
        state.views.remove(&(window.into(), view));
        Self::changed(&mut state);
        self.update_network_flag(&state);
        drop(state);
        self.notify.notify_one();
        Ok(())
    }
    pub fn unsubscribe_instance(
        &self,
        window: &str,
        instance: u64,
        view: MonitorView,
    ) -> Result<(), AppError> {
        validate_view(window, view)?;
        let mut state = self.lock()?;
        Self::validate_instance(&state, window, instance)?;
        state.views.remove(&(window.into(), view));
        Self::changed(&mut state);
        self.update_network_flag(&state);
        drop(state);
        self.notify.notify_one();
        Ok(())
    }
    pub fn remove_window(&self, window: &str) {
        if let Ok(mut state) = self.lock() {
            state.views.retain(|(label, _), _| label != window);
            state.hidden.remove(window);
            Self::changed(&mut state);
            self.update_network_flag(&state);
        }
        self.notify.notify_one();
    }
    pub fn destroy_window(&self, window: &str) {
        if let Ok(mut state) = self.lock() {
            state.destroyed.insert(window.into());
        }
        self.remove_window(window);
    }
    fn update_network_flag(&self, state: &State) {
        if let Some(epoch) = &self.power_epoch {
            let active = state.paused.is_empty()
                && Self::visible_views(state)
                    .any(|v| v.contains(&MonitorModule::Cpu) || v.contains(&MonitorModule::Gpu));
            let previous = epoch.load(std::sync::atomic::Ordering::Acquire);
            if (previous % 2 == 1) != active {
                epoch.store(
                    previous.wrapping_add(1),
                    std::sync::atomic::Ordering::Release,
                );
            }
        }
        if let Some(flag) = &self.network_active {
            flag.store(
                state.paused.is_empty()
                    && Self::visible_views(state).any(|v| v.contains(&MonitorModule::Network)),
                std::sync::atomic::Ordering::Release,
            );
        }
    }
    pub fn configure_alerts(&self, enabled: bool) -> Result<(), AppError> {
        let mut state = self.lock()?;
        if state.alerts.enabled() == enabled {
            return Ok(());
        }
        state.alerts.set_enabled(enabled);
        Self::changed(&mut state);
        self.update_network_flag(&state);
        drop(state);
        self.notify.notify_one();
        Ok(())
    }
    pub fn trend(&self) -> Result<Vec<MonitorTrendPoint>, AppError> {
        Ok(self.lock()?.engine.trend.points())
    }
    pub fn configure(
        &self,
        menu: MonitorMenuBar,
        interval: MonitorIntervalMs,
    ) -> Result<(), AppError> {
        let mut state = self.lock()?;
        if state.menu == menu && state.interval_ms == u32::from(interval) {
            return Ok(());
        }
        state.menu = menu;
        state.interval_ms = interval.into();
        state.menu_at = None;
        Self::changed(&mut state);
        self.update_network_flag(&state);
        drop(state);
        self.notify.notify_one();
        Ok(())
    }
    pub fn pause(&self, reason: PauseReason, paused: bool) {
        if let Ok(mut state) = self.lock() {
            let changed = if paused {
                state.paused.insert(reason)
            } else {
                state.paused.remove(&reason)
            };
            if changed {
                if reason == PauseReason::Sleep && !paused {
                    // A real wake is a new opportunity for temporarily unavailable drivers.
                    // Other pause transitions must not repeatedly probe absent hardware.
                    for probe in state.probes.values_mut() {
                        probe.remaining = PROBE_ATTEMPTS;
                        probe.next_at = 0;
                    }
                }
                state.engine.reset();
                state.sensors_at = None;
                state.fans_at = None;
                state.menu_at = None;
                Self::changed(&mut state);
                self.update_network_flag(&state);
            }
        }
        self.notify.notify_one();
    }
    pub fn snapshot(&self) -> Result<MonitorSnapshot, AppError> {
        Ok(self.lock()?.engine.snapshot())
    }
    pub fn presentation(&self) -> Result<Publication, AppError> {
        let state = self.lock()?;
        Ok(Publication {
            sample: state.engine.latest.clone(),
            available: state.engine.available.clone(),
            menu: state.menu.clone(),
            windows: vec![],
            menu_due: true,
            history: state.engine.history.iter().cloned().collect(),
            alerts: vec![],
        })
    }
    /// A fan command or a safety hand-back changed the fans. Samples reuse a fan reading for up to five seconds, so
    /// keep the helper's fresh reading, drop a tick still in flight with the old one, and read the fans again now.
    pub fn fan_changed(&self, change: &FanChange) {
        if let Ok(mut state) = self.lock() {
            state.engine.fan_reason(change.reason);
            if state.engine.latest.fans.is_some() {
                state.engine.latest.fans = Some(change.fans.clone());
            }
            state.fans_at = None;
            state.generation = state.generation.wrapping_add(1);
        }
        self.notify.notify_one();
    }
    pub fn quit_target(&self, pid: u32) -> Result<RawProcess, AppError> {
        self.lock()?
            .engine
            .identities
            .get(&pid)
            .filter(|p| p.info.quittable && p.info.kind == ProcessKind::App)
            .cloned()
            .ok_or_else(|| AppError::new("E_PROCESS_NOT_QUITTABLE", "monitor_quit_app"))
    }
    /// Shared with the read-only performance harness. No native work runs without an effective plan.
    pub fn tick(&self, now: u64, at: i64) -> Result<Option<Publication>, AppError> {
        self.tick_inner(now, at, false)
    }
    fn tick_inner(
        &self,
        now: u64,
        at: i64,
        force_processes: bool,
    ) -> Result<Option<Publication>, AppError> {
        // Serialize planning through commit, not only native collection: a queued refresh
        // must see the previous probe's cooldown and any changed demand before reading.
        // No caller acquires collector while holding state; both guards are local to this tick.
        let mut collector = self
            .collector
            .lock()
            .map_err(|_| AppError::new("E_UNKNOWN", "monitor_collector"))?;
        let (
            plan,
            generation,
            processes_due,
            read_modules,
            cached_sensors,
            cached_fans,
            extras,
            cached_power,
            cached_devices,
        ) = {
            let state = self.lock()?;
            let Some(plan) = Self::tick_plan(&state, now) else {
                return Ok(None);
            };
            let due = plan.processes
                && (force_processes
                    || state.engine.latest.processes.as_ref().is_none_or(|_| {
                        state.engine.process_at.is_none_or(|last| {
                            now.saturating_sub(last) >= state.engine.process_interval_ms()
                        })
                    }));
            let visible: Modules = Self::visible_views(&state)
                .flat_map(|v| v.iter().copied())
                .filter(|m| state.capable.contains(m))
                .collect();
            let extras = Extras {
                power_visible: visible.contains(&MonitorModule::Cpu)
                    || visible.contains(&MonitorModule::Gpu),
                power: (visible.contains(&MonitorModule::Cpu)
                    || visible.contains(&MonitorModule::Gpu))
                    && state
                        .power_at
                        .is_none_or(|last| now.saturating_sub(last) >= 2000),
                smart: (visible.contains(&MonitorModule::Disk) || state.alerts.enabled())
                    && state
                        .smart_at
                        .is_none_or(|last| now.saturating_sub(last) >= 600_000),
                process_disk: visible.contains(&MonitorModule::Disk),
                process_network: visible.contains(&MonitorModule::Network),
            };
            let power_visible =
                visible.contains(&MonitorModule::Cpu) || visible.contains(&MonitorModule::Gpu);
            let thermal_interval =
                if state.menu.sensors.enabled || visible.contains(&MonitorModule::Sensors) {
                    5000
                } else {
                    30000
                };
            let mut read_modules = plan.modules.clone();
            if state.alerts.enabled() {
                for module in [
                    MonitorModule::Cpu,
                    MonitorModule::Memory,
                    MonitorModule::Disk,
                    MonitorModule::Battery,
                ] {
                    let displayed = visible.contains(&module) || state.menu.item(module).enabled;
                    if !displayed
                        && state.core_at.get(&module).is_some_and(|last| {
                            now.saturating_sub(*last) < u64::from(state.interval_ms.max(5000))
                        })
                    {
                        read_modules.remove(&module);
                    }
                }
            }
            if state
                .sensors_at
                .is_some_and(|last| now.saturating_sub(last) < thermal_interval)
            {
                read_modules.remove(&MonitorModule::Sensors);
            }
            if state
                .fans_at
                .is_some_and(|last| now.saturating_sub(last) < 5000)
            {
                read_modules.remove(&MonitorModule::Fans);
            }
            (
                plan,
                state.generation,
                due,
                read_modules,
                state.engine.latest.sensors.clone(),
                state.engine.latest.fans.clone(),
                extras,
                if power_visible {
                    (
                        state.engine.latest.cpu.power_watts,
                        state.engine.latest.gpu.as_ref().and_then(|g| g.power_watts),
                    )
                } else {
                    (None, None)
                },
                state.engine.latest.disk.devices.clone(),
            )
        };
        let mut frame = collector.collect_requested(&read_modules, processes_due, &extras);
        if !read_modules.contains(&MonitorModule::Sensors) {
            frame.sensors = cached_sensors;
        }
        if !read_modules.contains(&MonitorModule::Fans) {
            frame.fans = cached_fans;
        }
        if !extras.power {
            frame.cpu_power = cached_power.0;
            if let Some(gpu) = &mut frame.gpu {
                gpu.power_watts = cached_power.1;
            }
        }
        if !extras.smart {
            frame.disk_devices = cached_devices;
        }
        let mut state = self.lock()?;
        if state.sample_at.is_some_and(|last| now < last)
            || state.generation != generation
            || Self::tick_plan(&state, now).as_ref() != Some(&plan)
        {
            return Ok(None);
        }
        let readable = frame.available();
        for module in &read_modules {
            if readable.contains(module) {
                state.capable.insert(*module);
                state.probes.remove(module);
            } else if let Some(probe) = state.probes.get_mut(module) {
                probe.remaining = probe.remaining.saturating_sub(1);
                probe.next_at = now.saturating_add(PROBE_INTERVAL_MS);
            }
        }
        if read_modules.contains(&MonitorModule::Sensors) {
            state.sensors_at = Some(now);
        }
        if read_modules.contains(&MonitorModule::Fans) {
            state.fans_at = Some(now);
        }
        state.sample_at = Some(now);
        for module in read_modules.intersection(
            &[
                MonitorModule::Cpu,
                MonitorModule::Memory,
                MonitorModule::Disk,
                MonitorModule::Battery,
            ]
            .into(),
        ) {
            state.core_at.insert(*module, now);
        }
        if extras.power {
            state.power_at = Some(now);
        }
        if extras.smart {
            state.smart_at = Some(now);
        }
        if processes_due {
            state.engine.process_at = Some(now);
        }
        if extras.smart && frame.disk_devices.is_none() {
            state.engine.latest.disk.devices = None;
        }
        if !extras.power_visible
            && let Some(gpu) = &mut state.engine.latest.gpu
        {
            gpu.power_watts = None;
        }
        state
            .engine
            .deltas
            .retain_process_io(extras.process_disk, extras.process_network);
        let previous_available = state.engine.available.clone();
        let sample =
            state
                .engine
                .sample_read(frame, &plan.modules, &read_modules, plan.processes, now, at);
        let latest = &mut state.engine.latest;
        for items in [&mut latest.processes, &mut latest.process_groups]
            .into_iter()
            .flatten()
        {
            for p in items.iter_mut() {
                if !extras.process_disk {
                    p.disk_read_bytes_per_sec = None;
                    p.disk_write_bytes_per_sec = None;
                }
                if !extras.process_network {
                    p.net_down_bytes_per_sec = None;
                    p.net_up_bytes_per_sec = None;
                }
            }
            *items = super::engine::top_processes_io(
                std::mem::take(items),
                extras.process_disk,
                extras.process_network,
            );
        }
        let mut sample = sample;
        sample.processes = state.engine.latest.processes.clone();
        sample.process_groups = state.engine.latest.process_groups.clone();
        sample.cpu_breakdown = state.engine.latest.cpu_breakdown.clone();
        if let Some(last) = state.engine.history.back_mut() {
            last.processes = sample.processes.clone();
            last.process_groups = sample.process_groups.clone();
            last.cpu_breakdown = sample.cpu_breakdown.clone();
        }
        let alerts = state.alerts.evaluate(&sample.health, now);
        let menu_due = previous_available != state.engine.available
            || state
                .menu_at
                .is_none_or(|last| now.saturating_sub(last) >= u64::from(state.interval_ms));
        if menu_due {
            state.menu_at = Some(now);
        }
        let windows: BTreeSet<_> = state
            .views
            .keys()
            .filter(|(label, _)| !state.hidden.contains(label))
            .map(|(label, _)| label.clone())
            .collect();
        Ok(Some(Publication {
            sample,
            available: state.engine.available.clone(),
            menu: state.menu.clone(),
            windows: windows.into_iter().collect(),
            menu_due,
            history: state.engine.history.iter().cloned().collect(),
            alerts,
        }))
    }
    pub fn start(self: &Arc<Self>, sink: Sink) {
        use std::sync::atomic::Ordering;
        if self.started.swap(true, Ordering::AcqRel) {
            return;
        }
        if let Ok(mut stored) = self.sink.lock() {
            *stored = Some(sink.clone());
        }
        let weak = Arc::downgrade(self);
        tauri::async_runtime::spawn(async move {
            loop {
                let Some(sampler) = weak.upgrade() else {
                    break;
                };
                let notified = sampler.notify.notified();
                tokio::pin!(notified);
                notified.as_mut().enable();
                let now = sampler.origin.elapsed().as_millis() as u64;
                let Ok(delay) = sampler.lock().map(|state| Self::next_delay(&state, now)) else {
                    break;
                };
                if delay.is_none() {
                    notified.await;
                    continue;
                }
                let collector = sampler.clone();
                let publication = crate::blocking(move || {
                    collector.tick(now, chrono::Utc::now().timestamp_millis())
                })
                .await;
                if let Ok(Some(publication)) = publication {
                    sink(publication);
                }
                let Ok(delay) = sampler.lock().map(|state| Self::next_delay(&state, now)) else {
                    break;
                };
                if let Some(delay) = delay {
                    let remaining = now
                        .saturating_add(delay)
                        .saturating_sub(sampler.origin.elapsed().as_millis() as u64);
                    tokio::select! {_=tokio::time::sleep(std::time::Duration::from_millis(remaining))=>{},_=notified=>{}}
                } else {
                    notified.await;
                }
            }
        });
    }
}
pub fn validate_view(window: &str, view: MonitorView) -> Result<(), AppError> {
    if matches!(
        (window, view),
        ("main", MonitorView::Page | MonitorView::Detail) | ("tray-monitor", MonitorView::Popover)
    ) {
        Ok(())
    } else {
        Err(AppError::new("E_INVALID_ARG", "monitor_view_window"))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    struct Empty;
    impl Collector for Empty {
        fn system(&self) -> SystemInfo {
            SystemInfo {
                model_name: "Fixture".into(),
                model_id: "Fixture".into(),
                chip: "Fixture".into(),
                performance_cores: None,
                efficiency_cores: None,
                logical_cores: 1,
                gpu_cores: None,
                memory_bytes: 100,
                os_version: "Fixture".into(),
                booted_at: 0,
            }
        }
        fn collect(&mut self, _: &Modules, _: bool) -> Frame {
            Frame::default()
        }
    }
    #[test]
    fn probe_deadlines_schedule_the_loop_without_a_regular_plan() {
        let sampler = Sampler::new(
            Box::new(Empty),
            MonitorMenuBar::default(),
            MonitorIntervalMs::TwoSeconds,
        );
        let delay = |now| Sampler::next_delay(&sampler.lock().unwrap(), now);
        assert!(sampler.plan().unwrap().is_none());
        assert_eq!(delay(0), Some(0));
        sampler.tick(0, 0).unwrap();
        assert_eq!(delay(1), Some(29_999));
        sampler.pause(PauseReason::Screen, true);
        assert_eq!(delay(30_000), None);
        sampler.pause(PauseReason::Screen, false);
        assert_eq!(delay(30_000), Some(0));
        sampler.tick(30_000, 30_000).unwrap();
        assert_eq!(delay(30_000), Some(30_000));
        sampler.tick(60_000, 60_000).unwrap();
        assert_eq!(
            delay(60_000),
            None,
            "exhausted hardware probes stop the loop"
        );
        sampler.pause(PauseReason::Sleep, true);
        sampler.pause(PauseReason::Sleep, false);
        assert_eq!(delay(60_001), Some(0));
        let mut menu = MonitorMenuBar::default();
        menu.cpu.enabled = false;
        menu.memory.enabled = false;
        sampler
            .configure(menu, MonitorIntervalMs::TwoSeconds)
            .unwrap();
        assert_eq!(delay(60_001), None, "no demand stops pending recovery too");
    }
}
