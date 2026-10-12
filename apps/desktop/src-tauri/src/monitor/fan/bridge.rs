#[cfg(target_os = "macos")]
unsafe extern "C" {
    pub fn on_helper_identity() -> i32;
    pub fn on_service_status() -> i32;
    pub fn on_service_change(install: i32) -> i32;
    pub fn on_service_approval();
    pub fn on_helper_bundled() -> i32;
    pub fn on_helper_log(message: *const std::ffi::c_char);
    pub fn on_thermal_serious() -> i32;
    pub fn on_xpc_exchange(json: *const std::ffi::c_char) -> *mut std::ffi::c_char;
    pub fn on_bridge_free(value: *mut std::ffi::c_char);
    pub fn on_xpc_serve(
        callback: unsafe extern "C" fn(*const std::ffi::c_char, i32) -> *mut std::ffi::c_char,
    );
    pub fn on_helper_sleep_observer(callback: extern "C" fn()) -> i32;
    pub fn on_run_loop();
}
