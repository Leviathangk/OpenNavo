// Suppress terminal windows in release builds (Windows only; retained from the template).
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    if let Some(code) = opennavo_desktop_lib::security::signature::helper_entry() {
        std::process::exit(code);
    }
    if let Some(code) = opennavo_desktop_lib::uninstall::execute::admin_entry() {
        std::process::exit(code);
    }
    opennavo_desktop_lib::run();
}
