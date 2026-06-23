//! Terminal lifecycle: enter/leave the alternate screen, enable mouse capture
//! and raw mode. Port of `internal/terminal` plus the bubbletea program options
//! (alt-screen + mouse) from `runner.go`.
//!
//! A [`TerminalGuard`] restores the terminal on drop, so a panic or early return
//! never leaves the user staring at a broken screen.

use std::io::{self, Stdout, Write};

use crossterm::{
    cursor,
    event::{DisableMouseCapture, EnableMouseCapture},
    execute,
    terminal::{disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen},
};
use ratatui::{backend::CrosstermBackend, Terminal};

pub type Tui = Terminal<CrosstermBackend<Stdout>>;

pub struct TerminalGuard {
    restored: bool,
}

impl TerminalGuard {
    /// Enter the alternate screen, enable raw mode and mouse capture, hide the
    /// cursor, and return a ratatui terminal plus a guard that restores state.
    pub fn enter() -> io::Result<(Tui, TerminalGuard)> {
        enable_raw_mode()?;
        let mut out = io::stdout();
        execute!(out, EnterAlternateScreen, EnableMouseCapture, cursor::Hide)?;
        let backend = CrosstermBackend::new(io::stdout());
        let mut terminal = Terminal::new(backend)?;
        terminal.clear()?;
        Ok((terminal, TerminalGuard { restored: false }))
    }

    pub fn restore(&mut self) {
        if self.restored {
            return;
        }
        self.restored = true;
        let mut out = io::stdout();
        let _ = execute!(out, cursor::Show, DisableMouseCapture, LeaveAlternateScreen);
        let _ = disable_raw_mode();
        let _ = out.flush();
    }
}

impl Drop for TerminalGuard {
    fn drop(&mut self) {
        self.restore();
    }
}
