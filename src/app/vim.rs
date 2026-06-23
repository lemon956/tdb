//! Vim-style editing for the query editor: Normal / Insert / Visual modes with
//! the common motions, operators, yank/paste, and undo. Port of the Go
//! `workspace_vim` behavior (contracts §4 §5 §1). Cursor positions are always on
//! rune boundaries.

use crossterm::event::{KeyCode, KeyEvent};

use super::state::{VimMode, WorkspaceTab};

fn char_count(s: &str) -> usize {
    s.chars().count()
}

fn byte_index(s: &str, col: usize) -> usize {
    s.char_indices().nth(col).map(|(b, _)| b).unwrap_or(s.len())
}

fn snapshot(tab: &mut WorkspaceTab) {
    tab.undo.push((tab.buffer.clone(), tab.cursor_row, tab.cursor_col));
    if tab.undo.len() > 200 {
        tab.undo.remove(0);
    }
}

fn clamp_normal(tab: &mut WorkspaceTab) {
    let len = char_count(&tab.buffer[tab.cursor_row]);
    let max = len.saturating_sub(1);
    if tab.cursor_col > max {
        tab.cursor_col = max;
    }
}

/// Dispatch a key to the active query tab's vim layer. Returns true if the key
/// was consumed.
pub fn handle(tab: &mut WorkspaceTab, register: &mut String, key: KeyEvent) {
    // Editing/movement dismisses an open completion popup (Tab is intercepted
    // earlier by the caller).
    tab.completion = None;
    match tab.vim_mode {
        VimMode::Insert => insert_key(tab, key),
        VimMode::Normal => normal_key(tab, register, key),
        VimMode::Visual => visual_key(tab, register, key),
    }
}

fn insert_key(tab: &mut WorkspaceTab, key: KeyEvent) {
    match key.code {
        KeyCode::Esc => {
            tab.vim_mode = VimMode::Normal;
            if tab.cursor_col > 0 {
                tab.cursor_col -= 1;
            }
        }
        KeyCode::Char(c) => {
            let line = &mut tab.buffer[tab.cursor_row];
            let b = byte_index(line, tab.cursor_col);
            line.insert(b, c);
            tab.cursor_col += 1;
        }
        KeyCode::Enter => {
            let line = tab.buffer[tab.cursor_row].clone();
            let b = byte_index(&line, tab.cursor_col);
            let (l, r) = line.split_at(b);
            tab.buffer[tab.cursor_row] = l.to_string();
            tab.buffer.insert(tab.cursor_row + 1, r.to_string());
            tab.cursor_row += 1;
            tab.cursor_col = 0;
        }
        KeyCode::Backspace => backspace(tab),
        KeyCode::Left => move_left(tab),
        KeyCode::Right => move_right(tab, true),
        KeyCode::Up => move_up(tab),
        KeyCode::Down => move_down(tab),
        _ => {}
    }
}

fn normal_key(tab: &mut WorkspaceTab, register: &mut String, key: KeyEvent) {
    // Pending operator (d, y, g).
    if let Some(op) = tab.vim_pending.take() {
        match (op, key.code) {
            ('g', KeyCode::Char('g')) => {
                tab.cursor_row = 0;
                tab.cursor_col = 0;
            }
            ('d', KeyCode::Char('d')) => delete_line(tab, register),
            ('y', KeyCode::Char('y')) => yank_line(tab, register),
            ('d', KeyCode::Char('w')) => {
                snapshot(tab);
                delete_to(tab, word_forward_pos(tab));
            }
            _ => {}
        }
        return;
    }

    match key.code {
        KeyCode::Char('i') => tab.vim_mode = VimMode::Insert,
        KeyCode::Char('a') => {
            move_right(tab, true);
            tab.vim_mode = VimMode::Insert;
        }
        KeyCode::Char('I') => {
            tab.cursor_col = 0;
            tab.vim_mode = VimMode::Insert;
        }
        KeyCode::Char('A') => {
            tab.cursor_col = char_count(&tab.buffer[tab.cursor_row]);
            tab.vim_mode = VimMode::Insert;
        }
        KeyCode::Char('o') => {
            snapshot(tab);
            tab.buffer.insert(tab.cursor_row + 1, String::new());
            tab.cursor_row += 1;
            tab.cursor_col = 0;
            tab.vim_mode = VimMode::Insert;
        }
        KeyCode::Char('O') => {
            snapshot(tab);
            tab.buffer.insert(tab.cursor_row, String::new());
            tab.cursor_col = 0;
            tab.vim_mode = VimMode::Insert;
        }
        KeyCode::Char('v') => {
            tab.visual_anchor = (tab.cursor_row, tab.cursor_col);
            tab.vim_mode = VimMode::Visual;
        }
        KeyCode::Char('h') | KeyCode::Left => move_left(tab),
        KeyCode::Char('l') | KeyCode::Right => {
            move_right(tab, false);
        }
        KeyCode::Char('j') | KeyCode::Down => move_down(tab),
        KeyCode::Char('k') | KeyCode::Up => move_up(tab),
        KeyCode::Char('0') | KeyCode::Home => tab.cursor_col = 0,
        KeyCode::Char('$') | KeyCode::End => {
            tab.cursor_col = char_count(&tab.buffer[tab.cursor_row]).saturating_sub(1);
        }
        KeyCode::Char('w') => tab.cursor_col = word_forward_pos(tab).1.min(line_len(tab)),
        KeyCode::Char('b') => {
            let (r, c) = word_back_pos(tab);
            tab.cursor_row = r;
            tab.cursor_col = c;
        }
        KeyCode::Char('e') => tab.cursor_col = word_end_pos(tab),
        KeyCode::Char('G') => {
            tab.cursor_row = tab.buffer.len() - 1;
            clamp_normal(tab);
        }
        KeyCode::Char('g') => tab.vim_pending = Some('g'),
        KeyCode::Char('d') => tab.vim_pending = Some('d'),
        KeyCode::Char('y') => tab.vim_pending = Some('y'),
        KeyCode::Char('x') => {
            snapshot(tab);
            let line = &mut tab.buffer[tab.cursor_row];
            let n = char_count(line);
            if tab.cursor_col < n {
                let b = byte_index(line, tab.cursor_col);
                let e = byte_index(line, tab.cursor_col + 1);
                line.replace_range(b..e, "");
                clamp_normal(tab);
            }
        }
        KeyCode::Char('J') => {
            if tab.cursor_row + 1 < tab.buffer.len() {
                snapshot(tab);
                let next = tab.buffer.remove(tab.cursor_row + 1);
                let line = &mut tab.buffer[tab.cursor_row];
                if !line.is_empty() && !next.is_empty() {
                    line.push(' ');
                }
                line.push_str(next.trim_start());
            }
        }
        KeyCode::Char('p') => paste(tab, register, true),
        KeyCode::Char('P') => paste(tab, register, false),
        KeyCode::Char('u') => {
            if let Some((buf, r, c)) = tab.undo.pop() {
                tab.buffer = buf;
                tab.cursor_row = r.min(tab.buffer.len().saturating_sub(1));
                tab.cursor_col = c;
            }
        }
        _ => {}
    }
}

fn visual_key(tab: &mut WorkspaceTab, register: &mut String, key: KeyEvent) {
    match key.code {
        KeyCode::Esc => tab.vim_mode = VimMode::Normal,
        KeyCode::Char('h') | KeyCode::Left => move_left(tab),
        KeyCode::Char('l') | KeyCode::Right => move_right(tab, true),
        KeyCode::Char('j') | KeyCode::Down => move_down(tab),
        KeyCode::Char('k') | KeyCode::Up => move_up(tab),
        KeyCode::Char('0') => tab.cursor_col = 0,
        KeyCode::Char('$') => tab.cursor_col = char_count(&tab.buffer[tab.cursor_row]),
        KeyCode::Char('y') => {
            *register = visual_text(tab);
            collapse_to_start(tab);
            tab.vim_mode = VimMode::Normal;
            clamp_normal(tab);
        }
        KeyCode::Char('d') | KeyCode::Char('x') => {
            snapshot(tab);
            *register = visual_text(tab);
            delete_visual(tab);
            tab.vim_mode = VimMode::Normal;
            clamp_normal(tab);
        }
        _ => {}
    }
}

// ---- motions ----

fn line_len(tab: &WorkspaceTab) -> usize {
    char_count(&tab.buffer[tab.cursor_row])
}

fn move_left(tab: &mut WorkspaceTab) {
    if tab.cursor_col > 0 {
        tab.cursor_col -= 1;
    }
}

fn move_right(tab: &mut WorkspaceTab, allow_eol: bool) {
    let len = line_len(tab);
    let max = if allow_eol { len } else { len.saturating_sub(1) };
    if tab.cursor_col < max {
        tab.cursor_col += 1;
    }
}

fn move_up(tab: &mut WorkspaceTab) {
    if tab.cursor_row > 0 {
        tab.cursor_row -= 1;
        tab.cursor_col = tab.cursor_col.min(line_len(tab));
    }
}

fn move_down(tab: &mut WorkspaceTab) {
    if tab.cursor_row + 1 < tab.buffer.len() {
        tab.cursor_row += 1;
        tab.cursor_col = tab.cursor_col.min(line_len(tab));
    }
}

fn backspace(tab: &mut WorkspaceTab) {
    if tab.cursor_col > 0 {
        let line = &mut tab.buffer[tab.cursor_row];
        let b = byte_index(line, tab.cursor_col - 1);
        let e = byte_index(line, tab.cursor_col);
        line.replace_range(b..e, "");
        tab.cursor_col -= 1;
    } else if tab.cursor_row > 0 {
        let cur = tab.buffer.remove(tab.cursor_row);
        tab.cursor_row -= 1;
        tab.cursor_col = line_len(tab);
        tab.buffer[tab.cursor_row].push_str(&cur);
    }
}

fn word_forward_pos(tab: &WorkspaceTab) -> (usize, usize) {
    let chars: Vec<char> = tab.buffer[tab.cursor_row].chars().collect();
    let mut i = tab.cursor_col;
    // skip current word
    while i < chars.len() && !chars[i].is_whitespace() {
        i += 1;
    }
    while i < chars.len() && chars[i].is_whitespace() {
        i += 1;
    }
    (tab.cursor_row, i)
}

fn word_end_pos(tab: &WorkspaceTab) -> usize {
    let chars: Vec<char> = tab.buffer[tab.cursor_row].chars().collect();
    let mut i = tab.cursor_col + 1;
    while i < chars.len() && chars[i].is_whitespace() {
        i += 1;
    }
    while i + 1 < chars.len() && !chars[i + 1].is_whitespace() {
        i += 1;
    }
    i.min(chars.len().saturating_sub(1))
}

fn word_back_pos(tab: &WorkspaceTab) -> (usize, usize) {
    let chars: Vec<char> = tab.buffer[tab.cursor_row].chars().collect();
    let mut i = tab.cursor_col;
    if i == 0 {
        return (tab.cursor_row, 0);
    }
    i -= 1;
    while i > 0 && chars[i].is_whitespace() {
        i -= 1;
    }
    while i > 0 && !chars[i - 1].is_whitespace() {
        i -= 1;
    }
    (tab.cursor_row, i)
}

// ---- edits ----

fn delete_line(tab: &mut WorkspaceTab, register: &mut String) {
    snapshot(tab);
    *register = format!("{}\n", tab.buffer[tab.cursor_row]);
    if tab.buffer.len() == 1 {
        tab.buffer[0].clear();
    } else {
        tab.buffer.remove(tab.cursor_row);
        if tab.cursor_row >= tab.buffer.len() {
            tab.cursor_row = tab.buffer.len() - 1;
        }
    }
    tab.cursor_col = 0;
}

fn yank_line(tab: &mut WorkspaceTab, register: &mut String) {
    *register = format!("{}\n", tab.buffer[tab.cursor_row]);
}

fn delete_to(tab: &mut WorkspaceTab, to: (usize, usize)) {
    // Same-line delete from cursor to `to.1`.
    if to.0 != tab.cursor_row {
        return;
    }
    let line = &mut tab.buffer[tab.cursor_row];
    let from = tab.cursor_col;
    let (a, b) = (from.min(to.1), from.max(to.1));
    let ba = byte_index(line, a);
    let bb = byte_index(line, b);
    line.replace_range(ba..bb, "");
    tab.cursor_col = a;
    clamp_normal(tab);
}

fn paste(tab: &mut WorkspaceTab, register: &str, after: bool) {
    if register.is_empty() {
        return;
    }
    snapshot(tab);
    if let Some(line_text) = register.strip_suffix('\n') {
        // Line-wise paste.
        let at = if after { tab.cursor_row + 1 } else { tab.cursor_row };
        for (k, l) in line_text.split('\n').enumerate() {
            tab.buffer.insert(at + k, l.to_string());
        }
        tab.cursor_row = at;
        tab.cursor_col = 0;
    } else {
        let col = if after { tab.cursor_col + 1 } else { tab.cursor_col };
        let line = &mut tab.buffer[tab.cursor_row];
        let b = byte_index(line, col.min(char_count(line)));
        line.insert_str(b, register);
        tab.cursor_col = col + char_count(register) - 1;
    }
}

// ---- visual selection ----

fn ordered(tab: &WorkspaceTab) -> ((usize, usize), (usize, usize)) {
    let a = tab.visual_anchor;
    let b = (tab.cursor_row, tab.cursor_col);
    if a <= b {
        (a, b)
    } else {
        (b, a)
    }
}

fn visual_text(tab: &WorkspaceTab) -> String {
    let (start, end) = ordered(tab);
    let text = tab.buffer.join("\n");
    let so = offset_of(&tab.buffer, start.0, start.1);
    let eo = offset_of(&tab.buffer, end.0, end.1) + 1; // inclusive of char under cursor
    let chars: Vec<char> = text.chars().collect();
    let eo = eo.min(chars.len());
    chars[so.min(chars.len())..eo].iter().collect()
}

fn delete_visual(tab: &mut WorkspaceTab) {
    let (start, end) = ordered(tab);
    let text = tab.buffer.join("\n");
    let chars: Vec<char> = text.chars().collect();
    let so = offset_of(&tab.buffer, start.0, start.1).min(chars.len());
    let eo = (offset_of(&tab.buffer, end.0, end.1) + 1).min(chars.len());
    let mut new: String = chars[..so].iter().collect();
    new.extend(chars[eo..].iter());
    tab.buffer = new.split('\n').map(|s| s.to_string()).collect();
    if tab.buffer.is_empty() {
        tab.buffer.push(String::new());
    }
    tab.cursor_row = start.0.min(tab.buffer.len() - 1);
    tab.cursor_col = start.1;
}

fn collapse_to_start(tab: &mut WorkspaceTab) {
    let (start, _) = ordered(tab);
    tab.cursor_row = start.0;
    tab.cursor_col = start.1;
}

/// Absolute char offset of (row,col) within `buffer.join("\n")`.
fn offset_of(buffer: &[String], row: usize, col: usize) -> usize {
    let mut off = 0;
    for line in buffer.iter().take(row) {
        off += char_count(line) + 1; // +1 for the newline
    }
    off + col
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::app::state::WorkspaceTab as Tab;
    use crossterm::event::{KeyCode, KeyEvent};

    fn k(c: char) -> KeyEvent {
        KeyEvent::from(KeyCode::Char(c))
    }

    fn tab_with(text: &str) -> Tab {
        let mut t = Tab::query(1);
        // These tests exercise Normal/Visual commands; query tabs now default to
        // Insert, so start them in Normal explicitly.
        t.vim_mode = VimMode::Normal;
        t.buffer = text.split('\n').map(|s| s.to_string()).collect();
        t
    }

    #[test]
    fn insert_and_escape() {
        let mut t = tab_with("");
        let mut reg = String::new();
        handle(&mut t, &mut reg, k('i'));
        handle(&mut t, &mut reg, k('h'));
        handle(&mut t, &mut reg, k('i'));
        assert_eq!(t.buffer[0], "hi");
        handle(&mut t, &mut reg, KeyEvent::from(KeyCode::Esc));
        assert_eq!(t.vim_mode, VimMode::Normal);
    }

    #[test]
    fn dd_deletes_line_into_register_then_p_pastes() {
        let mut t = tab_with("one\ntwo");
        let mut reg = String::new();
        handle(&mut t, &mut reg, k('d'));
        handle(&mut t, &mut reg, k('d'));
        assert_eq!(t.buffer, vec!["two".to_string()]);
        assert_eq!(reg, "one\n");
        handle(&mut t, &mut reg, k('p'));
        assert_eq!(t.buffer, vec!["two".to_string(), "one".to_string()]);
    }

    #[test]
    fn x_deletes_char_and_u_undoes() {
        let mut t = tab_with("abc");
        let mut reg = String::new();
        handle(&mut t, &mut reg, k('x'));
        assert_eq!(t.buffer[0], "bc");
        handle(&mut t, &mut reg, k('u'));
        assert_eq!(t.buffer[0], "abc");
    }

    #[test]
    fn gg_and_G_jump() {
        let mut t = tab_with("a\nb\nc");
        let mut reg = String::new();
        handle(&mut t, &mut reg, k('G'));
        assert_eq!(t.cursor_row, 2);
        handle(&mut t, &mut reg, k('g'));
        handle(&mut t, &mut reg, k('g'));
        assert_eq!(t.cursor_row, 0);
    }

    #[test]
    fn J_joins_lines_with_space() {
        let mut t = tab_with("select 1\n  from t");
        let mut reg = String::new();
        handle(&mut t, &mut reg, k('J'));
        assert_eq!(t.buffer, vec!["select 1 from t".to_string()]);
    }

    #[test]
    fn visual_yank_charwise() {
        let mut t = tab_with("hello");
        let mut reg = String::new();
        handle(&mut t, &mut reg, k('v')); // anchor at 0
        handle(&mut t, &mut reg, k('l'));
        handle(&mut t, &mut reg, k('l')); // cursor at 2
        handle(&mut t, &mut reg, k('y'));
        assert_eq!(reg, "hel");
        assert_eq!(t.vim_mode, VimMode::Normal);
    }
}
