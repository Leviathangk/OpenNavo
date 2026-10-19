// SPDX-License-Identifier: GPL-3.0-only
use crate::{engine::Engine, types::FirewallErrorCode};
use serde_json::json;
use std::{
    ffi::{CStr, CString, c_char},
    sync::{Mutex, OnceLock},
};

static ENGINE: OnceLock<Mutex<Engine>> = OnceLock::new();

/// # Safety
/// `request` must point to a NUL-terminated UTF-8 buffer for the duration of this call.
/// The returned buffer must be released exactly once with `on_fw_core_free`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn on_fw_core_call(request: *const c_char) -> *mut c_char {
    if request.is_null() {
        return std::ptr::null_mut();
    }
    std::panic::catch_unwind(|| {
        let request=unsafe{CStr::from_ptr(request)}.to_bytes();
        if request.len()>320*1024*1024 {return std::ptr::null_mut();}
        let Ok(mut engine)=ENGINE.get_or_init(||Mutex::new(Engine::default())).lock() else {return std::ptr::null_mut();};
        let result=serde_json::from_slice(request).map_err(|_|FirewallErrorCode::RuleInvalid).and_then(|request|engine.request(request));
        let resolutions=std::mem::take(&mut engine.resolutions);
        let response=match result {
            Ok(value)=>json!({"value":value,"error":null,"resolutions":resolutions}),
            Err(code)=>json!({"value":null,"error":{"code":code,"message":"firewall_request","detail":null},"resolutions":resolutions}),
        };
        CString::new(response.to_string()).map(CString::into_raw).unwrap_or(std::ptr::null_mut())
    }).unwrap_or(std::ptr::null_mut())
}

/// # Safety
/// `value` must be NULL or a live pointer returned by `on_fw_core_call`, used once.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn on_fw_core_free(value: *mut c_char) {
    if !value.is_null() {
        drop(unsafe { CString::from_raw(value) });
    }
}
