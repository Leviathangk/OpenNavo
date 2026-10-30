//! Command-line dispatch for the `mcp` subcommand.
//!
//! This runs before Tauri initialization: MCP mode must not create an app
//! handle or any window, and stdout is reserved for protocol output.

use std::ffi::{OsStr, OsString};

use super::protocol;

/// How the process arguments should be dispatched.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Mode {
    /// No MCP subcommand: fall through to the Tauri application.
    Gui,
    /// Exactly `<exe> mcp`: run the stdio server.
    Mcp,
    /// `mcp` with extra arguments: invalid usage, never launch the GUI.
    InvalidMcp,
}

/// Classify the full argv vector, including the executable at index 0.
pub fn classify(args: &[OsString]) -> Mode {
    match args {
        [_, command, rest @ ..] if command.as_os_str() == OsStr::new("mcp") => {
            if rest.is_empty() {
                Mode::Mcp
            } else {
                Mode::InvalidMcp
            }
        }
        _ => Mode::Gui,
    }
}

/// Enter MCP mode when requested, returning the process exit code.
///
/// `None` means the caller should continue into Tauri. Only the exact
/// invocation `<exe> mcp` enters the protocol loop; deep links and `--hidden`
/// fall through unchanged.
pub fn entry() -> Option<i32> {
    let args: Vec<OsString> = std::env::args_os().collect();
    match classify(&args) {
        Mode::Gui => None,
        Mode::Mcp => Some(run_stdio()),
        Mode::InvalidMcp => {
            eprintln!("usage: opennavo-desktop mcp");
            Some(2)
        }
    }
}

fn run_stdio() -> i32 {
    let stdin = std::io::stdin();
    let stdout = std::io::stdout();
    match protocol::serve(stdin.lock(), stdout.lock()) {
        Ok(()) => 0,
        Err(error) => {
            eprintln!("mcp: {error}");
            1
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn argv(values: &[&str]) -> Vec<OsString> {
        values.iter().map(OsString::from).collect()
    }

    #[test]
    fn mcp_subcommand_is_exact_and_does_not_capture_deep_links() {
        assert_eq!(classify(&argv(&["opennavo-desktop", "mcp"])), Mode::Mcp);
        assert_eq!(
            classify(&argv(&["opennavo-desktop", "mcp", "extra"])),
            Mode::InvalidMcp
        );

        for invocation in [
            &["opennavo-desktop"][..],
            &["opennavo-desktop", "--hidden"],
            &["opennavo-desktop", "opennavo://install?x=1"],
            &["opennavo-desktop", "mcpfoo"],
        ] {
            assert_eq!(classify(&argv(invocation)), Mode::Gui, "{invocation:?}");
        }
        assert_eq!(classify(&[]), Mode::Gui);
    }
}
