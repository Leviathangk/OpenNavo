use super::super::{
    smc::{Smc, Value, error},
    types::*,
};
use crate::AppError;
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Family {
    Intel,
    /// Apple M1 and later. Writes follow the keys this SMC exposes, not the chip generation: some firmware needs
    /// the global `Ftst` unlock first, newer firmware has no `Ftst` at all (M5, and M1 Max on macOS 27).
    Apple,
    Unsupported,
}
impl Family {
    pub fn from_chip(chip: &str) -> Self {
        if !chip.starts_with("Apple") {
            return if chip.contains("Intel") {
                Self::Intel
            } else {
                Self::Unsupported
            };
        }
        let generation = chip
            .split_whitespace()
            .find_map(|part| part.strip_prefix('M').and_then(|s| s.parse::<u32>().ok()));
        if generation.is_some_and(|g| g >= 1) {
            Self::Apple
        } else {
            Self::Unsupported
        }
    }
    pub fn support(self, fan_count: usize) -> FanControlSupport {
        if fan_count == 0 || self == Self::Unsupported {
            FanControlSupport::Unsupported
        } else {
            FanControlSupport::Unverified
        }
    }
}
pub fn read_fans(smc: &mut dyn Smc) -> Option<Vec<FanState>> {
    let count = smc.read("FNum")?.number()?;
    if count.fract() != 0.0 || !(1.0..=16.0).contains(&count) {
        return None;
    }
    // Apple Silicon reports a mode key per fan; older Intel Macs only the FS! bitmask (read as Stats does).
    let mask = smc.read("FS! ").and_then(|v| v.number()).unwrap_or(0.0) as u32;
    let mut fans = vec![];
    for id in 0..count as u32 {
        let n = |smc: &mut dyn Smc, suffix: &str| {
            smc.read(&format!("F{id:X}{suffix}"))
                .and_then(|v| v.number())
                .filter(|n| (0.0..=30000.0).contains(n))
                .map(|n| n.round() as u32)
        };
        let rpm = n(smc, "Ac")?;
        let min_rpm = n(smc, "Mn")?;
        let max_rpm = n(smc, "Mx")?;
        if max_rpm == 0 || min_rpm > max_rpm {
            return None;
        }
        let location = smc
            .read(&format!("F{id:X}ID"))
            .and_then(|v| {
                let bytes = if &v.kind == b"{fds" && v.bytes.len() > 4 {
                    &v.bytes[4..]
                } else {
                    &v.bytes[..]
                };
                std::str::from_utf8(bytes).ok().map(|s| {
                    s.trim_matches(|c: char| c == '\0' || c.is_whitespace())
                        .to_owned()
                })
            })
            .filter(|s| !s.is_empty());
        let manual =
            n(smc, "md").or_else(|| n(smc, "Md")).unwrap_or(0) == 1 || (mask >> id) & 1 == 1;
        fans.push(FanState {
            id,
            name: location.unwrap_or_else(|| format!("Fan {}", id + 1)),
            rpm,
            min_rpm,
            max_rpm,
            target_rpm: if manual { n(smc, "Tg") } else { None },
            mode: if manual {
                FanMode::Manual
            } else {
                FanMode::Auto
            },
        });
    }
    Some(fans)
}
pub trait Hardware: Send {
    fn fans(&mut self) -> Result<Vec<FanState>, AppError>;
    fn set_mode(&mut self, id: u32, mode: FanMode) -> Result<(), AppError>;
    fn set_speed(&mut self, id: u32, rpm: u32) -> Result<(), AppError>;
    fn reset(&mut self) -> Result<(), AppError>;
    fn temperature(&mut self) -> Option<f64>;
    fn serious_pressure(&self) -> bool;
}
pub trait Delay: Send {
    fn wait(&mut self, millis: u64);
}
pub struct ThreadDelay;
impl Delay for ThreadDelay {
    fn wait(&mut self, millis: u64) {
        std::thread::sleep(std::time::Duration::from_millis(millis));
    }
}
/// Apple firmware can be busy for a moment, so its writes retry briefly, as in Stats (MIT). A full `Ftst` unlock
/// (≤1 s, 3 s for thermalmonitord to let go, ≤2 s for the mode) stays inside the App's 8-second XPC deadline.
const APPLE_TRIES: u32 = 10;
const RETRY_MS: u64 = 50;
const UNLOCK_TRIES: u32 = 20;
const UNLOCK_RETRY_MS: u64 = 100;
const UNLOCK_SETTLE_MS: u64 = 3000;
pub struct SmcHardware<S: Smc, D: Delay> {
    pub smc: S,
    pub family: Family,
    pub delay: D,
    thermal_chip: String,
    temperature_keys: Vec<String>,
    known_ids: Vec<u32>,
    mode_keys: std::collections::BTreeMap<u32, String>,
    templates: std::collections::BTreeMap<String, Value>,
}
impl<S: Smc, D: Delay> SmcHardware<S, D> {
    pub fn new(mut smc: S, family: Family, delay: D) -> Self {
        let temperature_keys = if family == Family::Intel {
            smc.keys()
        } else {
            vec![]
        };
        let known_ids = read_fans(&mut smc)
            .unwrap_or_default()
            .iter()
            .map(|f| f.id)
            .collect();
        Self {
            smc,
            family,
            delay,
            thermal_chip: String::new(),
            temperature_keys,
            known_ids,
            mode_keys: Default::default(),
            templates: Default::default(),
        }
    }
    pub fn with_thermal_chip(mut self, chip: &str) -> Self {
        self.thermal_chip = chip.into();
        self
    }
    /// Reads a number and keeps the key's encoding, so a later restore still works when reads fail.
    fn read(&mut self, key: &str) -> Option<f64> {
        let value = self.smc.read(key)?;
        let number = value.number();
        self.templates.insert(key.into(), value);
        number
    }
    fn write(&mut self, key: &str, n: u32, tries: u32, gap: u64) -> Result<(), AppError> {
        if let Some(value) = self.smc.read(key) {
            self.templates.insert(key.into(), value);
        }
        let bytes = self.templates.get(key).ok_or_else(error)?.encode(n)?;
        for attempt in 0..tries.max(1) {
            if attempt > 0 {
                self.delay.wait(gap);
            }
            if self.smc.write(key, &bytes).is_ok() {
                return Ok(());
            }
        }
        log::warn!(
            "SMC write {key}={n} failed ({})",
            self.smc.last_error().unwrap_or_default()
        );
        Err(error())
    }
    fn put(&mut self, key: &str, n: u32) -> Result<(), AppError> {
        let tries = if self.family == Family::Apple {
            APPLE_TRIES
        } else {
            1
        };
        self.write(key, n, tries, RETRY_MS)
    }
    /// `F{n}md` or `F{n}Md` depending on the model; older Intel Macs have neither.
    fn mode_key(&mut self, id: u32) -> Option<String> {
        if let Some(key) = self.mode_keys.get(&id) {
            return Some(key.clone());
        }
        for suffix in ["md", "Md"] {
            let key = format!("F{id:X}{suffix}");
            if let Some(value) = self.smc.read(&key) {
                self.templates.insert(key.clone(), value);
                self.mode_keys.insert(id, key.clone());
                return Some(key);
            }
        }
        None
    }
    fn mask(&mut self, id: u32, manual: bool) -> Result<(), AppError> {
        let mask = self.read("FS! ").ok_or_else(error)? as u32;
        self.put(
            "FS! ",
            if manual {
                mask | (1 << id)
            } else {
                mask & !(1 << id)
            },
        )
    }
    /// Newer Apple firmware takes the mode key directly. Older firmware refuses it until the global `Ftst` unlock,
    /// then needs about three seconds before thermalmonitord lets go; without `Ftst` there is nothing else to try.
    fn unlock(&mut self, id: u32) -> Result<(), AppError> {
        let key = self.mode_key(id).ok_or_else(error)?;
        if self.write(&key, 1, 1, 0).is_ok() {
            return Ok(());
        }
        match self.read("Ftst") {
            None => return Err(error()),
            Some(state) if state != 1.0 => {
                self.write("Ftst", 1, UNLOCK_TRIES, RETRY_MS)?;
                self.delay.wait(UNLOCK_SETTLE_MS);
            }
            Some(_) => {}
        }
        self.write(&key, 1, UNLOCK_TRIES, UNLOCK_RETRY_MS)
    }
    /// Back to the system: the mode, then the manual target, so the system curve takes over at once (as Stats does).
    fn automatic(&mut self, id: u32, key: &str) -> Result<(), AppError> {
        self.put(key, 0)?;
        if self.family == Family::Apple {
            let _ = self.put(&format!("F{id:X}Tg"), 0);
        }
        Ok(())
    }
    /// The global key that hands every fan to the system: FS! on Intel, `Ftst` on older Apple firmware.
    fn release(&mut self) -> Result<(), AppError> {
        let key = if self.family == Family::Intel {
            "FS! "
        } else {
            "Ftst"
        };
        // Already released, or a key this Mac doesn't have; when a read fails, write from the cached encoding.
        if self.read(key) == Some(0.0) || !self.templates.contains_key(key) {
            return Ok(());
        }
        self.put(key, 0)
    }
}
impl<S: Smc, D: Delay> Hardware for SmcHardware<S, D> {
    fn fans(&mut self) -> Result<Vec<FanState>, AppError> {
        let fans = read_fans(&mut self.smc).ok_or_else(error)?;
        self.known_ids = fans.iter().map(|f| f.id).collect();
        Ok(fans)
    }
    fn set_mode(&mut self, id: u32, mode: FanMode) -> Result<(), AppError> {
        let manual = mode == FanMode::Manual;
        match self.family {
            Family::Unsupported => Err(AppError::new("E_FAN_UNSUPPORTED", "chip")),
            Family::Apple if manual => self.unlock(id),
            Family::Apple => {
                let key = self.mode_key(id).ok_or_else(error)?;
                self.automatic(id, &key)?;
                // Keep the Ftst unlock while another fan is still manual.
                if self.fans()?.iter().any(|f| f.mode == FanMode::Manual) {
                    Ok(())
                } else {
                    self.release()
                }
            }
            Family::Intel => {
                let key = self.mode_key(id);
                if let Some(key) = &key {
                    self.put(key, u32::from(manual))?;
                }
                if self.read("FS! ").is_some() {
                    self.mask(id, manual)
                } else if key.is_some() {
                    Ok(())
                } else {
                    Err(error())
                }
            }
        }
    }
    fn set_speed(&mut self, id: u32, rpm: u32) -> Result<(), AppError> {
        self.put(&format!("F{id:X}Tg"), rpm)
    }
    fn reset(&mut self) -> Result<(), AppError> {
        if self.family == Family::Unsupported {
            return Err(error());
        }
        let mut failed = false;
        for id in self.known_ids.clone() {
            let Some(key) = self.mode_key(id) else {
                // Older Intel Macs keep every fan's mode in FS! alone.
                failed |= self.family == Family::Apple;
                continue;
            };
            // Skip fans already automatic; when the read fails, write anyway from the cached encoding.
            if self.read(&key) != Some(0.0) && self.automatic(id, &key).is_err() {
                failed = true;
            }
        }
        if self.release().is_err() {
            failed = true;
        }
        if failed { Err(error()) } else { Ok(()) }
    }
    fn temperature(&mut self) -> Option<f64> {
        #[cfg(target_os = "macos")]
        let readings = if self.family != Family::Intel {
            super::super::sensors::apple_with_smc(&self.thermal_chip, Some(&mut self.smc))
        } else {
            super::super::sensors::intel(&mut self.smc, &self.temperature_keys)
        };
        #[cfg(not(target_os = "macos"))]
        let readings = super::super::sensors::intel(&mut self.smc, &self.temperature_keys);
        super::super::sensors::safety_temperature(&readings?)
    }
    fn serious_pressure(&self) -> bool {
        #[cfg(target_os = "macos")]
        {
            unsafe { super::bridge::on_thermal_serious() != 0 }
        }
        #[cfg(not(target_os = "macos"))]
        {
            false
        }
    }
}
// Keep codecs covered independently of native hardware.
pub fn encoded(kind: [u8; 4], size: usize, rpm: u32) -> Result<Vec<u8>, AppError> {
    Value {
        kind,
        bytes: vec![0; size],
    }
    .encode(rpm)
}
