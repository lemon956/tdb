//! Crash safety. Mirrors the Go `recover.go` goal: a panic in render or input
//! handling becomes a recoverable error surfaced in the error box instead of
//! taking the whole program down. The full panic + backtrace is appended to a
//! log next to the config file.

use std::panic::{catch_unwind, AssertUnwindSafe};
use std::path::PathBuf;
use std::sync::Mutex;

use once_cell::sync::Lazy;

static PANIC_LOG: Lazy<Mutex<Option<PathBuf>>> = Lazy::new(|| Mutex::new(None));

pub fn panic_log_path(config_path: &std::path::Path) -> Option<PathBuf> {
    config_path.parent().map(|d| d.join("tdb-panic.log"))
}

/// Install a panic hook that appends to the panic log (no stderr print, so the
/// alt-screen stays intact while [`guard`] recovers).
pub fn install_logging_hook(path: Option<PathBuf>) {
    *PANIC_LOG.lock().unwrap() = path;
    std::panic::set_hook(Box::new(|info| {
        if let Some(path) = PANIC_LOG.lock().unwrap().clone() {
            use std::io::Write;
            if let Ok(mut f) = std::fs::OpenOptions::new().append(true).create(true).open(path) {
                let _ = writeln!(f, "\n=== panic ===\n{info}");
                let bt = std::backtrace::Backtrace::force_capture();
                let _ = writeln!(f, "{bt}");
            }
        }
    }));
}

/// Run `body`, converting a panic into `Err(message)` instead of unwinding out.
pub fn guard<R>(body: impl FnOnce() -> R) -> Result<R, String> {
    catch_unwind(AssertUnwindSafe(body)).map_err(|e| {
        if let Some(s) = e.downcast_ref::<&str>() {
            (*s).to_string()
        } else if let Some(s) = e.downcast_ref::<String>() {
            s.clone()
        } else {
            "internal error".to_string()
        }
    })
}
