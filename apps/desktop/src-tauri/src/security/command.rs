//! Read-only probes may inspect nonzero exits without losing either stream.
use crate::AppError;
pub use crate::native_command::Output;
pub fn output(program: &str, args: &[&str]) -> Result<Output, AppError> {
    crate::native_command::output(program, args, std::time::Duration::from_secs(10))
}
