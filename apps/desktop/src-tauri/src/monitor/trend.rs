//! Shared, bounded, memory-only bucket averages. Missing modules never become artificial zeroes.
use super::{
    collect::{Modules, number},
    types::*,
};
use std::collections::VecDeque;
#[derive(Default)]
struct Bucket {
    at: i64,
    sums: [f64; 10],
    counts: [u32; 10],
}
#[derive(Default)]
pub struct Trend {
    buckets: VecDeque<Bucket>,
}
impl Trend {
    pub fn add(&mut self, sample: &MonitorSample, modules: &Modules) {
        let at = sample.at.div_euclid(5000) * 5000;
        if self.buckets.back().is_some_and(|b| b.at > at) {
            return;
        }
        if self.buckets.back().is_none_or(|b| b.at != at) {
            self.buckets.push_back(Bucket {
                at,
                ..Bucket::default()
            });
        }
        while self.buckets.len() > 720
            || self.buckets.front().is_some_and(|b| b.at < at - 3_595_000)
        {
            self.buckets.pop_front();
        }
        let present = |m, value| modules.contains(&m).then_some(value).flatten();
        let values = [
            present(MonitorModule::Cpu, Some(f64::from(sample.cpu.usage))),
            present(
                MonitorModule::Gpu,
                sample.gpu.as_ref().map(|g| f64::from(g.usage)),
            ),
            present(
                MonitorModule::Memory,
                (sample.memory.total_bytes > 0)
                    .then(|| sample.memory.used_bytes as f64 / sample.memory.total_bytes as f64),
            ),
            present(
                MonitorModule::Disk,
                Some(f64::from(sample.disk.read_bytes_per_sec)),
            ),
            present(
                MonitorModule::Disk,
                Some(f64::from(sample.disk.write_bytes_per_sec)),
            ),
            present(
                MonitorModule::Network,
                Some(f64::from(sample.network.down_bytes_per_sec)),
            ),
            present(
                MonitorModule::Network,
                Some(f64::from(sample.network.up_bytes_per_sec)),
            ),
            present(
                MonitorModule::Sensors,
                sample
                    .sensors
                    .as_ref()
                    .and_then(|s| s.iter().map(|s| f64::from(s.celsius)).reduce(f64::max)),
            ),
            present(
                MonitorModule::Fans,
                sample
                    .fans
                    .as_ref()
                    .filter(|f| !f.is_empty())
                    .map(|f| f.iter().map(|f| f64::from(f.rpm)).sum::<f64>() / f.len() as f64),
            ),
            present(
                MonitorModule::Battery,
                sample.battery.as_ref().map(|b| f64::from(b.percent)),
            ),
        ];
        let b = self.buckets.back_mut().unwrap();
        for (i, value) in values.into_iter().enumerate() {
            if let Some(v) = value.filter(|v| v.is_finite()) {
                b.sums[i] += v;
                b.counts[i] += 1;
            }
        }
    }
    pub fn points(&self) -> Vec<MonitorTrendPoint> {
        self.buckets
            .iter()
            .map(|b| {
                let v = |i: usize| {
                    (b.counts[i] > 0).then(|| number(b.sums[i] / f64::from(b.counts[i])))
                };
                MonitorTrendPoint {
                    at: b.at,
                    cpu: v(0),
                    gpu: v(1),
                    memory: v(2),
                    disk_read: v(3),
                    disk_write: v(4),
                    net_down: v(5),
                    net_up: v(6),
                    temperature: v(7),
                    fan_rpm: v(8),
                    battery: v(9),
                }
            })
            .collect()
    }
}
