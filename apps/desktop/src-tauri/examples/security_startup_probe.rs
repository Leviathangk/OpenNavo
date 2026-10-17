//! Explicit read-only comparison probe. No database, watches or actions.
use opennavo_desktop_lib::security::{NativeCommands, files, signature, startup};
struct Logger;
impl log::Log for Logger {
    fn enabled(&self, _: &log::Metadata<'_>) -> bool {
        true
    }
    fn log(&self, record: &log::Record<'_>) {
        eprintln!("{}: {}", record.level(), record.args());
    }
    fn flush(&self) {}
}
static LOGGER: Logger = Logger;
fn main() -> Result<(), Box<dyn std::error::Error>> {
    if let Some(code) = opennavo_desktop_lib::security::signature::helper_entry() {
        std::process::exit(code);
    }
    let _ = log::set_logger(&LOGGER);
    log::set_max_level(log::LevelFilter::Info);
    let args = std::env::args().skip(1).collect::<Vec<_>>();
    if args.len() == 3 && args[0] == "--real-read-only" {
        let path = std::path::Path::new(&args[2]);
        if args[1] == "--btm-archive" {
            let (_, records) =
                startup::btm::Archive::parse(&files::read(path, 64 * 1024 * 1024)?)?.records()?;
            let rows = records.into_iter().map(|r| serde_json::json!({"name":r.name,"path":r.path,"disposition":r.disposition})).collect::<Vec<_>>();
            println!("{}", serde_json::to_string(&rows)?);
            return Ok(());
        }
        if args[1] == "--signature" {
            println!("{}", serde_json::to_string(&signature::inspect(path)?)?);
            return Ok(());
        }
    }
    if args == ["--real-read-only", "--posture"] {
        println!(
            "{}",
            serde_json::to_string(&opennavo_desktop_lib::security::posture::check(
                &NativeCommands,
                std::path::Path::new("/nonexistent")
            ))?
        );
        return Ok(());
    }
    if args != ["--real-read-only"] && args != ["--real-read-only", "--repeat"] {
        return Err("usage: security_startup_probe --real-read-only".into());
    }
    let home = std::path::PathBuf::from(std::env::var_os("HOME").ok_or("HOME missing")?);
    for _ in 0..if args.last().is_some_and(|a| a == "--repeat") {
        2
    } else {
        1
    } {
        let result = startup::scan(&startup::Roots::native(home.clone()), &NativeCommands);
        println!("{}", serde_json::to_string(&result)?);
    }
    Ok(())
}
