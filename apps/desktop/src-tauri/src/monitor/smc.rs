//! AppleSMC user-client ABI. App collectors only call read; writes are owned by the helper backend.
use crate::AppError;
use std::collections::BTreeMap;
#[derive(Clone, Debug)]
pub struct Value {
    pub kind: [u8; 4],
    pub bytes: Vec<u8>,
}
impl Value {
    pub fn number(&self) -> Option<f64> {
        let b = &self.bytes;
        let n = match (&self.kind, b.as_slice()) {
            (b"flt ", [a, b, c, d]) => f32::from_le_bytes([*a, *b, *c, *d]) as f64,
            (b"fpe2", [a, b]) => u16::from_be_bytes([*a, *b]) as f64 / 4.0,
            (b"sp78", [a, b]) => i16::from_be_bytes([*a, *b]) as f64 / 256.0,
            (b"ui8 ", [a]) => f64::from(*a),
            (b"ui16", [a, b]) => u16::from_be_bytes([*a, *b]) as f64,
            (b"ui32", [a, b, c, d]) => u32::from_be_bytes([*a, *b, *c, *d]) as f64,
            _ => return None,
        };
        n.is_finite().then_some(n)
    }
    pub fn encode(&self, number: u32) -> Result<Vec<u8>, AppError> {
        let out = match &self.kind {
            b"flt " => (number as f32).to_le_bytes().to_vec(),
            b"fpe2" if number <= 16383 => ((number as u16) * 4).to_be_bytes().to_vec(),
            b"ui8 " if number <= 255 => vec![number as u8],
            b"ui16" if number <= 65535 => (number as u16).to_be_bytes().to_vec(),
            _ => return Err(error()),
        };
        if out.len() != self.bytes.len() {
            return Err(error());
        }
        Ok(out)
    }
}
pub fn error() -> AppError {
    AppError::new("E_FAN_WRITE_FAILED", "smc")
}
pub trait Smc: Send {
    fn read(&mut self, key: &str) -> Option<Value>;
    fn write(&mut self, key: &str, bytes: &[u8]) -> Result<(), AppError>;
    fn keys(&mut self) -> Vec<String> {
        vec![]
    }
    /// The last IOKit/SMC failure, for the helper's log.
    fn last_error(&self) -> Option<String> {
        None
    }
}
#[cfg(target_os = "macos")]
mod mac {
    use super::*;
    #[repr(C)]
    #[derive(Default, Clone, Copy)]
    struct Version {
        major: u8,
        minor: u8,
        build: u8,
        reserved: u8,
        release: u16,
    }
    #[repr(C)]
    #[derive(Default, Clone, Copy)]
    struct Limit {
        version: u16,
        length: u16,
        cpu: u32,
        gpu: u32,
        memory: u32,
    }
    #[repr(C)]
    #[derive(Default, Clone, Copy)]
    struct Info {
        size: u32,
        kind: u32,
        attributes: u8,
    }
    #[repr(C)]
    #[derive(Default, Clone, Copy)]
    struct Packet {
        key: u32,
        version: Version,
        limit: Limit,
        info: Info,
        result: u8,
        status: u8,
        command: u8,
        index: u32,
        bytes: [u8; 32],
    }
    #[link(name = "IOKit", kind = "framework")]
    unsafe extern "C" {
        fn IOServiceMatching(name: *const std::ffi::c_char) -> super::super::cf::Ref;
        fn IOServiceGetMatchingService(port: u32, dict: super::super::cf::Ref) -> u32;
        fn IOServiceOpen(service: u32, task: u32, kind: u32, connection: *mut u32) -> i32;
        fn IOServiceClose(connection: u32) -> i32;
        fn IOObjectRelease(object: u32) -> i32;
        fn IOConnectCallStructMethod(
            connection: u32,
            selector: u32,
            input: *const Packet,
            input_size: usize,
            output: *mut Packet,
            output_size: *mut usize,
        ) -> i32;
        static mach_task_self_: u32;
    }
    pub struct NativeSmc {
        connection: u32,
        infos: BTreeMap<String, Info>,
        failure: std::cell::Cell<Option<(i32, u8)>>,
    }
    impl NativeSmc {
        pub fn open() -> Option<Self> {
            let matching = unsafe { IOServiceMatching(c"AppleSMC".as_ptr()) };
            if matching.is_null() {
                return None;
            }
            let service = unsafe { IOServiceGetMatchingService(0, matching) };
            if service == 0 {
                return None;
            }
            let mut connection = 0;
            let result = unsafe { IOServiceOpen(service, mach_task_self_, 0, &mut connection) };
            unsafe {
                IOObjectRelease(service);
            }
            (result == 0 && connection != 0).then(|| Self {
                connection,
                infos: BTreeMap::new(),
                failure: std::cell::Cell::new(None),
            })
        }
        fn call(&self, p: Packet) -> Option<Packet> {
            let mut out = Packet::default();
            let mut size = std::mem::size_of::<Packet>();
            let kern = unsafe {
                IOConnectCallStructMethod(self.connection, 2, &p, size, &mut out, &mut size)
            };
            let ok = kern == 0 && size == std::mem::size_of::<Packet>() && out.result == 0;
            if !ok {
                self.failure.set(Some((kern, out.result)));
            }
            ok.then_some(out)
        }
        fn info(&mut self, key: &str) -> Option<Info> {
            if let Some(info) = self.infos.get(key) {
                return Some(*info);
            }
            let key_bytes: [u8; 4] = key.as_bytes().try_into().ok()?;
            let out = self.call(Packet {
                key: u32::from_be_bytes(key_bytes),
                command: 9,
                ..Default::default()
            })?;
            if !(1..=32).contains(&out.info.size) {
                return None;
            }
            self.infos.insert(key.into(), out.info);
            Some(out.info)
        }
    }
    impl Drop for NativeSmc {
        fn drop(&mut self) {
            unsafe {
                IOServiceClose(self.connection);
            }
        }
    }
    impl Smc for NativeSmc {
        fn read(&mut self, key: &str) -> Option<Value> {
            let info = self.info(key)?;
            let out = self.call(Packet {
                key: u32::from_be_bytes(key.as_bytes().try_into().ok()?),
                info,
                command: 5,
                ..Default::default()
            })?;
            Some(Value {
                kind: info.kind.to_be_bytes(),
                bytes: out.bytes[..info.size as usize].to_vec(),
            })
        }
        fn write(&mut self, key: &str, bytes: &[u8]) -> Result<(), AppError> {
            let info = self.info(key).ok_or_else(error)?;
            if bytes.len() != info.size as usize {
                return Err(error());
            }
            let mut packet = Packet {
                key: u32::from_be_bytes(key.as_bytes().try_into().map_err(|_| error())?),
                info,
                command: 6,
                ..Default::default()
            };
            packet.bytes[..bytes.len()].copy_from_slice(bytes);
            self.call(packet).ok_or_else(error).map(|_| ())
        }
        fn last_error(&self) -> Option<String> {
            self.failure
                .get()
                .map(|(kern, smc)| format!("kern {kern:#x}, smc {smc:#04x}"))
        }
        fn keys(&mut self) -> Vec<String> {
            let count = self.read("#KEY").and_then(|v| v.number()).unwrap_or(0.0) as u32;
            if count > 8192 {
                return vec![];
            }
            (0..count)
                .filter_map(|index| {
                    let p = self.call(Packet {
                        command: 8,
                        index,
                        ..Default::default()
                    })?;
                    String::from_utf8(p.key.to_be_bytes().to_vec()).ok()
                })
                .collect()
        }
    }
    #[test]
    fn smc_packet_matches_abi() {
        assert_eq!(std::mem::size_of::<Packet>(), 80);
        assert_eq!(std::mem::offset_of!(Packet, bytes), 48);
    }
}
#[cfg(target_os = "macos")]
pub use mac::NativeSmc;
