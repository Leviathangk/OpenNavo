fn main() {
    if let Err(error) = opennavo_desktop_lib::monitor::fan::daemon::run() {
        eprintln!("OpenNavo fan helper failed: {}", error.code);
        std::process::exit(1);
    }
}
