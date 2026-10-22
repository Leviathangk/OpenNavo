//! Firmware values and units are decoded independently of the live registry for fixture testing.
#[derive(Debug, PartialEq)]
pub struct Capacity {
    pub design_mah: Option<u32>,
    pub max_mah: Option<u32>,
    pub health: Option<f64>,
}
pub fn capacity(top: impl Fn(&str) -> Option<f64>, data: impl Fn(&str) -> Option<f64>) -> Capacity {
    let mah = |n: Option<f64>| {
        n.filter(|v| v.is_finite() && v.fract() == 0.0 && *v > 0.0 && *v <= u32::MAX as f64)
            .map(|v| v as u32)
    };
    let design = mah(data("DesignCapacity")).or_else(|| mah(top("DesignCapacity")));
    let max = mah(data("NominalChargeCapacity"))
        .or_else(|| mah(top("NominalChargeCapacity")))
        .or_else(|| mah(data("AppleRawMaxCapacity")))
        .or_else(|| mah(top("AppleRawMaxCapacity")))
        .or_else(|| mah(top("MaxCapacity").filter(|v| *v > 100.0)));
    let health = match max.zip(design) {
        Some((m, d)) => Some((f64::from(m) / f64::from(d)).clamp(0.0, 1.0)),
        None if max.is_none() => top("MaxCapacity")
            .filter(|v| v.is_finite() && (0.0..=100.0).contains(v))
            .map(|v| v / 100.0),
        None => None,
    };
    Capacity {
        design_mah: design,
        max_mah: max,
        health,
    }
}
pub fn remaining_minutes(values: impl IntoIterator<Item = Option<f64>>) -> Option<f64> {
    values
        .into_iter()
        .flatten()
        .find(|v| v.is_finite() && *v > 0.0 && *v < 65535.0)
}
pub fn temperature(raw: Option<f64>) -> Option<f64> {
    // IORegistry publishes hundredths of Celsius (not the SBS wire unit of 0.1 K).
    raw.filter(|v| v.is_finite() && *v > 0.0)
        .map(|v| v / 100.0)
        .filter(|v| *v < 100.0)
}
/// Older systems expose Temperature on AppleSmartBattery; newer ones may expose only
/// AppleSmartBatteryPack.BatteryData.Temperature. Do not query the pack if already readable.
pub fn temperature_from_sources(
    top: Option<f64>,
    data: Option<f64>,
    pack: impl FnOnce() -> Option<f64>,
) -> Option<f64> {
    temperature(top)
        .or_else(|| temperature(data))
        .or_else(|| temperature(pack()))
}
