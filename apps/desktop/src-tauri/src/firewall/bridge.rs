// SPDX-License-Identifier: GPL-3.0-only
use crate::AppError;
use serde_json::{Value, json};

pub trait Bridge: Send + Sync {
    fn request(&self, request: Value) -> Result<Value, AppError>;
    fn disconnect(&self) {}
}
pub struct Native;
impl Bridge for Native {
    fn request(&self, request: Value) -> Result<Value, AppError> {
        #[cfg(target_os = "macos")]
        {
            use std::ffi::{CStr, CString, c_char};
            unsafe extern "C" {
                fn on_fw_native_request(input: *const c_char) -> *mut c_char;
                fn on_fw_native_free(output: *mut c_char);
            }
            let input = CString::new(request.to_string())
                .map_err(|_| super::failure(super::FirewallErrorCode::RuleInvalid))?;
            let output = unsafe { on_fw_native_request(input.as_ptr()) };
            if output.is_null() {
                return Err(super::failure(super::FirewallErrorCode::Disconnected));
            }
            let decoded = serde_json::from_slice(unsafe { CStr::from_ptr(output) }.to_bytes());
            unsafe { on_fw_native_free(output) };
            let envelope: Value =
                decoded.map_err(|_| super::failure(super::FirewallErrorCode::Disconnected))?;
            if !envelope["error"].is_null() {
                return Err(serde_json::from_value(envelope["error"].clone())
                    .map_err(|_| super::failure(super::FirewallErrorCode::Disconnected))?);
            }
            Ok(envelope["value"].clone())
        }
        #[cfg(not(target_os = "macos"))]
        {
            if request["op"] == "status" {
                Ok(json!(super::FirewallStatus::not_bundled()))
            } else {
                Err(super::failure(super::FirewallErrorCode::NotBundled))
            }
        }
    }
    fn disconnect(&self) {
        #[cfg(target_os = "macos")]
        {
            unsafe extern "C" {
                fn on_fw_disconnect();
            }
            unsafe { on_fw_disconnect() };
        }
    }
}

pub fn status(bridge: &dyn Bridge) -> Result<super::FirewallStatus, AppError> {
    serde_json::from_value(bridge.request(json!({"op":"status"}))?)
        .map_err(|_| super::failure(super::FirewallErrorCode::Disconnected))
}
