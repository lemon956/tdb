//! System clipboard integration. Port of `internal/app/clipboard.go`: try the
//! platform's clipboard commands in order, feeding the text on stdin.

use std::io::Write;
use std::process::{Command, Stdio};

/// Candidate (program, args) clipboard commands for the current OS.
pub fn candidates() -> Vec<(&'static str, &'static [&'static str])> {
    if cfg!(target_os = "macos") {
        vec![("pbcopy", &[][..])]
    } else if cfg!(target_os = "windows") {
        vec![("clip", &[][..])]
    } else {
        vec![
            ("wl-copy", &[][..]),
            ("xclip", &["-selection", "clipboard"][..]),
            ("xsel", &["--clipboard", "--input"][..]),
        ]
    }
}

/// Copy `text` to the system clipboard, returning the command that succeeded.
pub fn copy(text: &str) -> Result<&'static str, String> {
    let mut last_err = String::from("no clipboard command available");
    for (prog, args) in candidates() {
        match try_copy(prog, args, text) {
            Ok(()) => return Ok(prog),
            Err(e) => last_err = e,
        }
    }
    Err(last_err)
}

fn try_copy(prog: &str, args: &[&str], text: &str) -> Result<(), String> {
    let mut child = Command::new(prog)
        .args(args)
        .stdin(Stdio::piped())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .map_err(|e| format!("{prog}: {e}"))?;
    if let Some(stdin) = child.stdin.as_mut() {
        stdin.write_all(text.as_bytes()).map_err(|e| e.to_string())?;
    }
    let status = child.wait().map_err(|e| e.to_string())?;
    if status.success() {
        Ok(())
    } else {
        Err(format!("{prog} exited with {status}"))
    }
}
