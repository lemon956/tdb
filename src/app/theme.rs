//! Color palette and reusable styles. Ports the semantic colors from
//! `internal/app/theme.go`. The unified cursor/selection color (#22D3EE) is
//! shared by the text caret, row selection, visual range and active tab
//! (behavior contract §3).
#![allow(dead_code)]

use ratatui::style::{Color, Modifier, Style};

pub const BG: Color = Color::Rgb(0x0B, 0x12, 0x20);
pub const BG_ALT: Color = Color::Rgb(0x0F, 0x1A, 0x2E);
pub const INK: Color = Color::Rgb(0xD7, 0xDE, 0xE9);
pub const INK_BRIGHT: Color = Color::Rgb(0xF8, 0xFA, 0xFC);
pub const INK_INVERSE: Color = Color::Rgb(0x0F, 0x17, 0x2A);
pub const BORDER: Color = Color::Rgb(0x33, 0x41, 0x55);
pub const MUTED: Color = Color::Rgb(0x94, 0xA3, 0xB8);
pub const ACCENT: Color = Color::Rgb(0x38, 0xBD, 0xF8);
pub const INFO: Color = Color::Rgb(0x60, 0xA5, 0xFA);
pub const SUCCESS: Color = Color::Rgb(0x4A, 0xDE, 0x80);
pub const WARNING: Color = Color::Rgb(0xFB, 0xBF, 0x24);
pub const DANGER: Color = Color::Rgb(0xF8, 0x71, 0x71);
pub const VIOLET: Color = Color::Rgb(0xA7, 0x8B, 0xFA);
pub const HEADER_BG: Color = Color::Rgb(0x1D, 0x4E, 0xD8);
pub const FOOTER_BG: Color = Color::Rgb(0x1E, 0x29, 0x3B);
/// Unified cursor / selection highlight (bright cyan).
pub const CURSOR: Color = Color::Rgb(0x22, 0xD3, 0xEE);
/// Dimmed selection for an unfocused panel.
pub const CURSOR_DIM: Color = Color::Rgb(0x0E, 0x74, 0x90);
pub const KEY: Color = Color::Rgb(0xFD, 0xE6, 0x8A);
pub const SIDEBAR_EDGE: Color = Color::Rgb(0x0E, 0x74, 0x90);
pub const SIDEBAR_FOCUS_EDGE: Color = Color::Rgb(0x22, 0xD3, 0xEE);
pub const SIDEBAR_BG: Color = Color::Rgb(0x0A, 0x1F, 0x2E);
pub const MAIN_EDGE: Color = Color::Rgb(0x16, 0x65, 0x34);
pub const MAIN_FOCUS_EDGE: Color = Color::Rgb(0x86, 0xEF, 0xAC);
pub const MAIN_BG: Color = Color::Rgb(0x0B, 0x1F, 0x16);
pub const MODAL_BG: Color = Color::Rgb(0x11, 0x18, 0x27);
pub const TAB_QUERY: Color = Color::Rgb(0xFD, 0xE6, 0x8A);
pub const TAB_DATA: Color = Color::Rgb(0xA7, 0xF3, 0xD0);

/// Selected-row / caret style (focused). Dim variant when `focused` is false.
pub fn selection_style(focused: bool) -> Style {
    let bg = if focused { CURSOR } else { CURSOR_DIM };
    Style::default().bg(bg).fg(INK_INVERSE)
}

pub fn muted() -> Style {
    Style::default().fg(MUTED)
}

pub fn accent() -> Style {
    Style::default().fg(ACCENT).add_modifier(Modifier::BOLD)
}

pub fn header() -> Style {
    Style::default().bg(HEADER_BG).fg(INK_BRIGHT).add_modifier(Modifier::BOLD)
}

pub fn footer() -> Style {
    Style::default().bg(FOOTER_BG).fg(INK)
}

pub fn danger() -> Style {
    Style::default().fg(DANGER)
}

pub fn status_color(kind: StatusKind) -> Color {
    match kind {
        StatusKind::Success => SUCCESS,
        StatusKind::Error => DANGER,
        StatusKind::Warning => WARNING,
        StatusKind::Info => MUTED,
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StatusKind {
    Success,
    Error,
    Warning,
    Info,
}
