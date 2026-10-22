//! Small, read-only CoreFoundation / IOKit ownership boundary. Keys are fixed by native callers.
use std::{
    ffi::{CString, c_char, c_void},
    ptr,
};
pub type Ref = *const c_void;
#[link(name = "CoreFoundation", kind = "framework")]
unsafe extern "C" {
    fn CFRelease(value: Ref);
    fn CFGetTypeID(value: Ref) -> usize;
    fn CFDictionaryGetTypeID() -> usize;
    fn CFStringGetTypeID() -> usize;
    fn CFNumberGetTypeID() -> usize;
    fn CFBooleanGetTypeID() -> usize;
    fn CFDataGetTypeID() -> usize;
    fn CFStringCreateWithCString(allocator: Ref, value: *const c_char, encoding: u32) -> Ref;
    fn CFStringGetCString(value: Ref, buffer: *mut c_char, size: isize, encoding: u32) -> bool;
    fn CFNumberGetValue(value: Ref, kind: i32, output: *mut c_void) -> bool;
    fn CFBooleanGetValue(value: Ref) -> bool;
    fn CFDictionaryGetValue(value: Ref, key: Ref) -> Ref;
    pub fn CFArrayGetCount(value: Ref) -> isize;
    pub fn CFArrayGetValueAtIndex(value: Ref, index: isize) -> Ref;
    pub fn CFDataGetLength(value: Ref) -> isize;
    pub fn CFDataGetBytePtr(value: Ref) -> *const u8;
}
#[link(name = "IOKit", kind = "framework")]
unsafe extern "C" {
    fn IOServiceMatching(name: *const c_char) -> Ref;
    fn IOServiceNameMatching(name: *const c_char) -> Ref;
    fn IOServiceGetMatchingServices(port: u32, matching: Ref, iterator: *mut u32) -> i32;
    fn IOIteratorNext(iterator: u32) -> u32;
    fn IOObjectRelease(object: u32) -> i32;
    fn IORegistryEntryCreateCFProperties(
        entry: u32,
        properties: *mut Ref,
        allocator: Ref,
        options: u32,
    ) -> i32;
    fn IORegistryEntryGetRegistryEntryID(entry: u32, id: *mut u64) -> i32;
    fn IORegistryEntryFromPath(port: u32, path: *const c_char) -> u32;
    fn IORegistryEntryCreateCFProperty(entry: u32, key: Ref, allocator: Ref, options: u32) -> Ref;
}
pub struct Owned(pub Ref);
impl Owned {
    pub fn new(value: Ref) -> Option<Self> {
        (!value.is_null()).then(|| Self(value))
    }
}
impl Drop for Owned {
    fn drop(&mut self) {
        unsafe { CFRelease(self.0) }
    }
}
struct Io(u32);
impl Drop for Io {
    fn drop(&mut self) {
        unsafe {
            IOObjectRelease(self.0);
        }
    }
}
pub fn string(value: &str) -> Option<Owned> {
    let value = CString::new(value).ok()?;
    Owned::new(unsafe { CFStringCreateWithCString(ptr::null(), value.as_ptr(), 0x08000100) })
}
pub fn key(dict: Ref, name: &str) -> Ref {
    if dict.is_null() || unsafe { CFGetTypeID(dict) != CFDictionaryGetTypeID() } {
        return ptr::null();
    }
    string(name)
        .map(|s| unsafe { CFDictionaryGetValue(dict, s.0) })
        .unwrap_or(ptr::null())
}
pub fn text(value: Ref) -> Option<String> {
    if value.is_null() {
        return None;
    }
    unsafe {
        if CFGetTypeID(value) == CFStringGetTypeID() {
            let mut bytes = vec![0i8; 4096];
            if CFStringGetCString(value, bytes.as_mut_ptr(), bytes.len() as isize, 0x08000100) {
                return Some(
                    std::ffi::CStr::from_ptr(bytes.as_ptr())
                        .to_string_lossy()
                        .into_owned(),
                );
            }
        } else if CFGetTypeID(value) == CFDataGetTypeID() {
            let len = CFDataGetLength(value);
            if !(1..4096).contains(&len) {
                return None;
            }
            let bytes = std::slice::from_raw_parts(CFDataGetBytePtr(value), len as usize);
            return std::str::from_utf8(bytes)
                .ok()
                .map(|s| s.trim_end_matches('\0').to_owned());
        }
    }
    None
}
pub fn numeric(value: Ref) -> Option<f64> {
    if value.is_null() {
        return None;
    }
    let mut n: f64 = 0.0;
    unsafe {
        if CFGetTypeID(value) == CFNumberGetTypeID()
            && CFNumberGetValue(value, 13, (&mut n as *mut f64).cast())
            && n.is_finite()
        {
            Some(n)
        } else {
            None
        }
    }
}
pub fn n(dict: Ref, name: &str) -> Option<f64> {
    numeric(key(dict, name))
}
pub fn boolean(dict: Ref, name: &str) -> Option<bool> {
    let value = key(dict, name);
    if value.is_null() {
        return None;
    }
    unsafe { (CFGetTypeID(value) == CFBooleanGetTypeID()).then(|| CFBooleanGetValue(value)) }
}
pub fn services(class: &str) -> Vec<(u64, Owned)> {
    service_values(class, None, false)
}
/// Fetch only the requested registry property; avoid copying whole driver dictionaries each tick.
pub fn property_services(class: &str, property: &str) -> Vec<(u64, Owned)> {
    let Some(key) = string(property) else {
        return vec![];
    };
    service_values(class, Some(key.0), false)
}
/// Some registry nodes (including AppleSmartBatteryPack) are names, not driver classes.
pub fn named_property_services(name: &str, property: &str) -> Vec<(u64, Owned)> {
    let Some(key) = string(property) else {
        return vec![];
    };
    service_values(name, Some(key.0), true)
}
fn service_values(class: &str, key: Option<Ref>, by_name: bool) -> Vec<(u64, Owned)> {
    let Ok(name) = CString::new(class) else {
        return vec![];
    };
    let mut iterator = 0;
    // IOServiceGetMatchingServices consumes the matching dictionary, even on failure.
    let matching = unsafe {
        if by_name {
            IOServiceNameMatching(name.as_ptr())
        } else {
            IOServiceMatching(name.as_ptr())
        }
    };
    if matching.is_null()
        || unsafe { IOServiceGetMatchingServices(0, matching, &mut iterator) } != 0
    {
        return vec![];
    }
    let iterator = Io(iterator);
    let mut output = Vec::new();
    loop {
        let entry = unsafe { IOIteratorNext(iterator.0) };
        if entry == 0 {
            break;
        }
        let entry = Io(entry);
        let mut properties = ptr::null();
        let mut id = 0;
        if let Some(key) = key {
            properties = unsafe { IORegistryEntryCreateCFProperty(entry.0, key, ptr::null(), 0) };
        } else if unsafe {
            IORegistryEntryCreateCFProperties(entry.0, &mut properties, ptr::null(), 0)
        } != 0
        {
            continue;
        }
        if let Some(properties) = Owned::new(properties) {
            unsafe {
                IORegistryEntryGetRegistryEntryID(entry.0, &mut id);
            }
            output.push((id, properties));
        }
    }
    output
}
pub fn product_name() -> Option<String> {
    let entry = unsafe { IORegistryEntryFromPath(0, c"IODeviceTree:/".as_ptr()) };
    if entry == 0 {
        return None;
    }
    let entry = Io(entry);
    let key = string("product-name")?;
    let value =
        Owned::new(unsafe { IORegistryEntryCreateCFProperty(entry.0, key.0, ptr::null(), 0) })?;
    text(value.0).filter(|s| !s.is_empty())
}

/// IORegistry GPU counts may be CFNumber or a four-byte little-endian CFData property.
pub fn uint32(value: Ref) -> Option<u32> {
    if let Some(n) = numeric(value) {
        return (n.fract() == 0.0 && (0.0..=u32::MAX as f64).contains(&n)).then_some(n as u32);
    }
    if !value.is_null()
        && unsafe { CFGetTypeID(value) == CFDataGetTypeID() && CFDataGetLength(value) == 4 }
    {
        let bytes = unsafe { std::slice::from_raw_parts(CFDataGetBytePtr(value), 4) };
        Some(u32::from_le_bytes(bytes.try_into().ok()?))
    } else {
        None
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn null_create_results_have_no_release_obligation() {
        assert!(Owned::new(std::ptr::null()).is_none());
        assert_eq!(key(std::ptr::null(), "missing"), std::ptr::null());
        assert_eq!(text(std::ptr::null()), None);
        assert_eq!(numeric(std::ptr::null()), None);
    }
}
