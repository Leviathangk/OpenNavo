use super::types::SystemInfo;
use crate::AppError;

/// Injectable sysctl source; tests never inspect the host or change process-wide state.
pub trait SystemSource {
    fn read(&self, key: &str) -> Result<Vec<u8>, AppError>;
}

pub struct NativeSystemSource;
#[cfg(target_os = "macos")]
impl SystemSource for NativeSystemSource {
    fn read(&self, key: &str) -> Result<Vec<u8>, AppError> {
        use nix::libc;
        let name = std::ffi::CString::new(key).map_err(|_| unavailable(key))?;
        let mut len = 0;
        // sysctlbyname receives a terminated, fixed key; null newp makes both calls read-only.
        if unsafe {
            libc::sysctlbyname(
                name.as_ptr(),
                std::ptr::null_mut(),
                &mut len,
                std::ptr::null_mut(),
                0,
            )
        } != 0
            || len == 0
            || len > 4096
        {
            return Err(unavailable(key));
        }
        let mut bytes = vec![0u8; len];
        let capacity = len;
        // The output buffer has the size returned by the first call; failure never exposes its contents.
        if unsafe {
            libc::sysctlbyname(
                name.as_ptr(),
                bytes.as_mut_ptr().cast(),
                &mut len,
                std::ptr::null_mut(),
                0,
            )
        } != 0
            || len > capacity
        {
            return Err(unavailable(key));
        }
        bytes.truncate(len);
        Ok(bytes)
    }
}
#[cfg(not(target_os = "macos"))]
impl SystemSource for NativeSystemSource {
    fn read(&self, key: &str) -> Result<Vec<u8>, AppError> {
        Err(unavailable(key))
    }
}

fn unavailable(key: &str) -> AppError {
    AppError::new("E_MONITOR_UNAVAILABLE", key)
}
pub(crate) fn string(source: &impl SystemSource, key: &str) -> Result<String, AppError> {
    let bytes = source.read(key)?;
    let value = String::from_utf8(bytes).map_err(|_| unavailable(key))?;
    let value = value.trim_end_matches('\0').trim();
    if value.is_empty() || value.chars().any(char::is_control) {
        return Err(unavailable(key));
    }
    Ok(value.into())
}
fn u32_value(source: &impl SystemSource, key: &str) -> Result<u32, AppError> {
    let bytes = source.read(key)?;
    Ok(u32::from_ne_bytes(
        bytes.try_into().map_err(|_| unavailable(key))?,
    ))
}
fn u64_value(source: &impl SystemSource, key: &str) -> Result<u64, AppError> {
    let bytes = source.read(key)?;
    Ok(u64::from_ne_bytes(
        bytes.try_into().map_err(|_| unavailable(key))?,
    ))
}

pub fn read(source: &impl SystemSource) -> Result<SystemInfo, AppError> {
    let model_id = string(source, "hw.model")?;
    let chip = string(source, "machdep.cpu.brand_string")?;
    let logical_cores = u32_value(source, "hw.logicalcpu")?;
    let memory_bytes = u64_value(source, "hw.memsize")?;
    let os_version = string(source, "kern.osproductversion")?;
    let boot = source.read("kern.boottime")?;
    // Darwin's 64-bit timeval has an i64 seconds field, i32 microseconds and trailing padding.
    if boot.len() != 16 || logical_cores == 0 || memory_bytes == 0 {
        return Err(unavailable("system_info"));
    }
    let seconds = i64::from_ne_bytes(
        boot[..8]
            .try_into()
            .map_err(|_| unavailable("kern.boottime"))?,
    );
    let micros = i32::from_ne_bytes(
        boot[8..12]
            .try_into()
            .map_err(|_| unavailable("kern.boottime"))?,
    );
    if seconds < 0 || !(0..1_000_000).contains(&micros) {
        return Err(unavailable("kern.boottime"));
    }
    let booted_at = seconds
        .checked_mul(1000)
        .and_then(|v| v.checked_add(i64::from(micros / 1000)))
        .ok_or_else(|| unavailable("kern.boottime"))?;
    let apple = chip.starts_with("Apple ");
    Ok(SystemInfo {
        model_name: model_id.clone(), // IORegistry's friendly product-name is deferred to M1.
        model_id,
        chip,
        performance_cores: apple
            .then(|| u32_value(source, "hw.perflevel0.physicalcpu").ok())
            .flatten(),
        efficiency_cores: apple
            .then(|| u32_value(source, "hw.perflevel1.physicalcpu").ok())
            .flatten(),
        logical_cores,
        gpu_cores: None, // M2: do not infer a GPU configuration from a chip name.
        memory_bytes,
        os_version,
        booted_at,
    })
}
