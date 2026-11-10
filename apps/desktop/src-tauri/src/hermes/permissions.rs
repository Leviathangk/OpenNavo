//! Real macOS privacy permission probes for the Mac-control settings sheet.
//!
//! TCC has no single public API for every pane. Accessibility and Screen Recording
//! expose preflight calls; Full Disk Access is probed by opening a TCC-protected
//! database. Anything we cannot prove is reported as unknown by the caller.

use serde::Serialize;
use tauri::AppHandle;

use super::AppError;

#[derive(Debug, Clone, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesPermissions {
    pub accessibility: bool,
    pub screen_recording: bool,
    pub full_disk: bool,
    pub computer_use: bool,
}

#[cfg(target_os = "macos")]
#[link(name = "ApplicationServices", kind = "framework")]
unsafe extern "C" {
    fn AXIsProcessTrusted() -> u8;
}

#[cfg(target_os = "macos")]
#[link(name = "CoreGraphics", kind = "framework")]
unsafe extern "C" {
    fn CGPreflightScreenCaptureAccess() -> u8;
    fn CGRequestScreenCaptureAccess() -> u8;
}

fn full_disk_access() -> bool {
    // TCC.db is intentionally unreadable without Full Disk Access. A successful
    // open is a reliable positive; denial is reported as not granted.
    std::fs::File::open("/Library/Application Support/com.apple.TCC/TCC.db").is_ok()
}

fn computer_use(app: &AppHandle) -> bool {
    super::runtime_root(app)
        .ok()
        .map(|root| {
            root.join("CuaDriver.app/Contents/MacOS/cua-driver")
                .is_file()
        })
        .unwrap_or(false)
}

pub fn request_screen_capture() -> bool {
    #[cfg(target_os = "macos")]
    unsafe {
        CGRequestScreenCaptureAccess() != 0
    }
    #[cfg(not(target_os = "macos"))]
    {
        false
    }
}

pub fn status(app: &AppHandle) -> Result<HermesPermissions, AppError> {
    #[cfg(target_os = "macos")]
    let (accessibility, screen_recording) = unsafe {
        (
            AXIsProcessTrusted() != 0,
            CGPreflightScreenCaptureAccess() != 0,
        )
    };
    #[cfg(not(target_os = "macos"))]
    let (accessibility, screen_recording) = (false, false);

    Ok(HermesPermissions {
        accessibility,
        screen_recording,
        full_disk: full_disk_access(),
        computer_use: computer_use(app),
    })
}
