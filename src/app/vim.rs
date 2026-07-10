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

fn enter_insert(tab: &mut WorkspaceTab, snap: bool) {
    if snap {
        snapshot(tab);
    }
    tab.vim_mode = VimMode::Insert;
}

fn take_count(tab: &mut WorkspaceTab) -> (usize, bool) {
    let had = tab.vim_count > 0;
    let count = if had { tab.vim_count } else { 1 };
    tab.vim_count = 0;
    (count, had)
}

fn push_count(tab: &mut WorkspaceTab, digit: usize) {
    tab.vim_count = tab.vim_count.saturating_mul(10).saturating_add(digit);
}

fn maybe_count_key(tab: &mut WorkspaceTab, key: KeyCode) -> bool {
    let KeyCode::Char(c) = key else { return false };
    let Some(digit) = c.to_digit(10).map(|d| d as usize) else { return false };
    if digit == 0 && tab.vim_count == 0 && tab.vim_pending.is_none() {
        return false;
    }
    push_count(tab, digit);
    true
}

fn record_change(tab: &mut WorkspaceTab, seq: impl Into<String>) {
    tab.vim_last_change = seq.into();
}

fn replay_last_change(tab: &mut WorkspaceTab, register: &mut String) {
    let seq = tab.vim_last_change.clone();
    if seq.is_empty() {
        return;
    }
    for c in seq.chars() {
        normal_key(tab, register, KeyEvent::from(KeyCode::Char(c)));
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
    if tab.vim_search_active {
        match key.code {
            KeyCode::Char(c) => tab.vim_search_query.push(c),
            KeyCode::Backspace => {
                tab.vim_search_query.pop();
            }
            KeyCode::Enter => {
                tab.vim_search_active = false;
                tab.vim_last_search = tab.vim_search_query.clone();
                tab.vim_last_search_reverse = tab.vim_search_reverse;
                search_buffer(tab, tab.vim_last_search_reverse);
            }
            KeyCode::Esc => {
                tab.vim_search_active = false;
                tab.vim_search_query.clear();
            }
            _ => {}
        }
        return;
    }

    if key.code == KeyCode::Char('.') {
        replay_last_change(tab, register);
        return;
    }

    if maybe_count_key(tab, key.code) {
        return;
    }

    // Pending operator / prefix (`d`, `y`, `c`, `g`, `r`).
    if let Some(op) = tab.vim_pending.take() {
        let op_count = tab.vim_pending_count.max(1);
        tab.vim_pending_count = 1;
        let (motion_count, motion_had_count) = take_count(tab);
        let total = op_count.saturating_mul(motion_count).max(1);
        match (op, key.code) {
            ('g', KeyCode::Char('g')) => {
                let target = if op_count > 1 {
                    op_count - 1
                } else if motion_had_count {
                    motion_count.saturating_sub(1)
                } else {
                    0
                };
                goto_line(tab, target);
            }
            ('d', KeyCode::Char('d')) => {
                delete_lines(tab, register, total);
                record_change(tab, format!("{}dd", if total > 1 { total.to_string() } else { String::new() }));
            }
            ('y', KeyCode::Char('y')) => yank_lines(tab, register, total),
            ('c', KeyCode::Char('c')) => {
                change_lines(tab, register, total);
                record_change(tab, format!("{}cc", if total > 1 { total.to_string() } else { String::new() }));
            }
            ('d', KeyCode::Char('w')) => {
                delete_words(tab, register, total);
                record_change(tab, format!("{}dw", if total > 1 { total.to_string() } else { String::new() }));
            }
            ('c', KeyCode::Char('w')) => {
                change_words(tab, register, total);
                record_change(tab, format!("{}cw", if total > 1 { total.to_string() } else { String::new() }));
            }
            ('d', KeyCode::Char('$')) => {
                delete_to_end(tab, register);
                record_change(tab, "d$");
            }
            ('c', KeyCode::Char('$')) => {
                change_to_end(tab, register);
                record_change(tab, "c$");
            }
            ('r', KeyCode::Char(c)) => {
                replace_chars(tab, register, c, op_count);
                record_change(tab, format!("{}r{c}", if op_count > 1 { op_count.to_string() } else { String::new() }));
            }
            _ => {}
        }
        return;
    }

    let (count, had_count) = take_count(tab);
    match key.code {
        KeyCode::Char('i') => enter_insert(tab, true),
        KeyCode::Char('a') => {
            move_right(tab, true);
            enter_insert(tab, true);
        }
        KeyCode::Char('I') => {
            tab.cursor_col = 0;
            enter_insert(tab, true);
        }
        KeyCode::Char('A') => {
            tab.cursor_col = char_count(&tab.buffer[tab.cursor_row]);
            enter_insert(tab, true);
        }
        KeyCode::Char('o') => {
            snapshot(tab);
            tab.buffer.insert(tab.cursor_row + 1, String::new());
            tab.cursor_row += 1;
            tab.cursor_col = 0;
            enter_insert(tab, false);
            record_change(tab, "o");
        }
        KeyCode::Char('O') => {
            snapshot(tab);
            tab.buffer.insert(tab.cursor_row, String::new());
            tab.cursor_col = 0;
            enter_insert(tab, false);
            record_change(tab, "O");
        }
        KeyCode::Char('v') => {
            tab.visual_anchor = (tab.cursor_row, tab.cursor_col);
            tab.vim_mode = VimMode::Visual;
        }
        KeyCode::Char('h') | KeyCode::Left => repeat(count, || move_left(tab)),
        KeyCode::Char('l') | KeyCode::Right => repeat(count, || move_right(tab, false)),
        KeyCode::Char('j') | KeyCode::Down => repeat(count, || move_down(tab)),
        KeyCode::Char('k') | KeyCode::Up => repeat(count, || move_up(tab)),
        KeyCode::Char('0') | KeyCode::Home => tab.cursor_col = 0,
        KeyCode::Char('^') => tab.cursor_col = first_nonblank_col(&tab.buffer[tab.cursor_row]),
        KeyCode::Char('$') | KeyCode::End => {
            tab.cursor_col = char_count(&tab.buffer[tab.cursor_row]).saturating_sub(1);
        }
        KeyCode::Char('w') => repeat(count, || move_word_forward(tab)),
        KeyCode::Char('b') => repeat(count, || move_word_back(tab)),
        KeyCode::Char('e') => repeat(count, || move_word_end(tab)),
        KeyCode::Char('G') => {
            if had_count {
                goto_line(tab, count.saturating_sub(1));
            } else {
                goto_line(tab, usize::MAX);
            }
        }
        KeyCode::Char('g') => {
            tab.vim_pending = Some('g');
            tab.vim_pending_count = count;
        }
        KeyCode::Char('d') | KeyCode::Char('y') | KeyCode::Char('c') | KeyCode::Char('r') => {
            let KeyCode::Char(c) = key.code else { return };
            tab.vim_pending = Some(c);
            tab.vim_pending_count = count;
        }
        KeyCode::Char('x') => {
            delete_chars(tab, register, count);
            record_change(tab, format!("{}x", if count > 1 { count.to_string() } else { String::new() }));
        }
        KeyCode::Char('X') => {
            delete_before(tab, register, count);
            record_change(tab, format!("{}X", if count > 1 { count.to_string() } else { String::new() }));
        }
        KeyCode::Char('s') => {
            substitute_chars(tab, register, count);
            record_change(tab, format!("{}s", if count > 1 { count.to_string() } else { String::new() }));
        }
        KeyCode::Char('S') => {
            change_lines(tab, register, count);
            record_change(tab, format!("{}S", if count > 1 { count.to_string() } else { String::new() }));
        }
        KeyCode::Char('D') => {
            delete_to_end(tab, register);
            record_change(tab, "D");
        }
        KeyCode::Char('C') => {
            change_to_end(tab, register);
            record_change(tab, "C");
        }
        KeyCode::Char('Y') => yank_lines(tab, register, count),
        KeyCode::Char('J') => {
            if tab.cursor_row + 1 < tab.buffer.len() {
                snapshot(tab);
                let next = tab.buffer.remove(tab.cursor_row + 1);
                let line = &mut tab.buffer[tab.cursor_row];
                if !line.is_empty() && !next.is_empty() {
                    line.push(' ');
                }
                line.push_str(next.trim_start());
                record_change(tab, "J");
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
        KeyCode::Char('/') | KeyCode::Char('?') => {
            tab.vim_search_active = true;
            tab.vim_search_reverse = key.code == KeyCode::Char('?');
            tab.vim_search_query.clear();
        }
        KeyCode::Char('n') => search_buffer(tab, tab.vim_last_search_reverse),
        KeyCode::Char('N') => search_buffer(tab, !tab.vim_last_search_reverse),
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
        KeyCode::Char('^') => tab.cursor_col = first_nonblank_col(&tab.buffer[tab.cursor_row]),
        KeyCode::Char('$') => tab.cursor_col = char_count(&tab.buffer[tab.cursor_row]),
        KeyCode::Char('w') => move_word_forward(tab),
        KeyCode::Char('b') => move_word_back(tab),
        KeyCode::Char('e') => move_word_end(tab),
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

fn first_nonblank_col(line: &str) -> usize {
    line.chars().position(|c| !c.is_whitespace()).unwrap_or(0)
}

fn repeat(mut count: usize, mut f: impl FnMut()) {
    while count > 0 {
        f();
        count -= 1;
    }
}

fn goto_line(tab: &mut WorkspaceTab, line: usize) {
    tab.cursor_row = line.min(tab.buffer.len().saturating_sub(1));
    clamp_normal(tab);
}

fn move_word_forward(tab: &mut WorkspaceTab) {
    tab.cursor_col = word_forward_pos(tab).1.min(line_len(tab));
}

fn move_word_back(tab: &mut WorkspaceTab) {
    let (r, c) = word_back_pos(tab);
    tab.cursor_row = r;
    tab.cursor_col = c;
}

fn move_word_end(tab: &mut WorkspaceTab) {
    tab.cursor_col = word_end_pos(tab);
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

fn next_word_col(line: &str, start: usize) -> usize {
    let chars: Vec<char> = line.chars().collect();
    let mut i = start.min(chars.len());
    while i < chars.len() && !chars[i].is_whitespace() {
        i += 1;
    }
    while i < chars.len() && chars[i].is_whitespace() {
        i += 1;
    }
    i
}

fn change_word_end_col(line: &str, start: usize, count: usize) -> usize {
    let chars: Vec<char> = line.chars().collect();
    let mut i = start.min(chars.len());
    let mut remaining = count.max(1);
    while remaining > 0 {
        while i < chars.len() && chars[i].is_whitespace() {
            i += 1;
        }
        while i < chars.len() && !chars[i].is_whitespace() {
            i += 1;
        }
        remaining -= 1;
        if remaining > 0 {
            while i < chars.len() && chars[i].is_whitespace() {
                i += 1;
            }
        }
    }
    i
}

fn line_slice(line: &str, start: usize, end: usize) -> String {
    let chars: Vec<char> = line.chars().collect();
    chars[start.min(chars.len())..end.min(chars.len())]
        .iter()
        .collect()
}

fn replace_line_range(line: &mut String, start: usize, end: usize, replacement: &str) {
    let a = byte_index(line, start);
    let b = byte_index(line, end);
    line.replace_range(a..b, replacement);
}

// ---- edits ----

fn yank_lines(tab: &WorkspaceTab, register: &mut String, count: usize) {
    let start = tab.cursor_row;
    let end = (start + count.max(1)).min(tab.buffer.len());
    *register = tab.buffer[start..end].join("\n");
    register.push('\n');
}

fn delete_lines(tab: &mut WorkspaceTab, register: &mut String, count: usize) {
    snapshot(tab);
    let start = tab.cursor_row;
    let end = (start + count.max(1)).min(tab.buffer.len());
    *register = tab.buffer[start..end].join("\n");
    register.push('\n');
    tab.buffer.drain(start..end);
    if tab.buffer.is_empty() {
        tab.buffer.push(String::new());
    }
    tab.cursor_row = start.min(tab.buffer.len() - 1);
    tab.cursor_col = 0;
}

fn change_lines(tab: &mut WorkspaceTab, register: &mut String, count: usize) {
    snapshot(tab);
    let start = tab.cursor_row;
    let end = (start + count.max(1)).min(tab.buffer.len());
    *register = tab.buffer[start..end].join("\n");
    register.push('\n');
    tab.buffer.drain(start..end);
    tab.buffer.insert(start.min(tab.buffer.len()), String::new());
    tab.cursor_row = start.min(tab.buffer.len() - 1);
    tab.cursor_col = 0;
    enter_insert(tab, false);
}

fn delete_words(tab: &mut WorkspaceTab, register: &mut String, count: usize) {
    snapshot(tab);
    let line = &mut tab.buffer[tab.cursor_row];
    let start = tab.cursor_col;
    let mut end = start;
    for _ in 0..count.max(1) {
        end = next_word_col(line, end);
    }
    *register = line_slice(line, start, end);
    replace_line_range(line, start, end, "");
    tab.cursor_col = start;
    clamp_normal(tab);
}

fn change_words(tab: &mut WorkspaceTab, register: &mut String, count: usize) {
    snapshot(tab);
    let line = &mut tab.buffer[tab.cursor_row];
    let start = tab.cursor_col;
    let end = change_word_end_col(line, start, count);
    *register = line_slice(line, start, end);
    replace_line_range(line, start, end, "");
    tab.cursor_col = start;
    enter_insert(tab, false);
}

fn delete_to_end(tab: &mut WorkspaceTab, register: &mut String) {
    snapshot(tab);
    let line = &mut tab.buffer[tab.cursor_row];
    let start = tab.cursor_col;
    let end = char_count(line);
    *register = line_slice(line, start, end);
    replace_line_range(line, start, end, "");
    clamp_normal(tab);
}

fn change_to_end(tab: &mut WorkspaceTab, register: &mut String) {
    snapshot(tab);
    let line = &mut tab.buffer[tab.cursor_row];
    let start = tab.cursor_col;
    let end = char_count(line);
    *register = line_slice(line, start, end);
    replace_line_range(line, start, end, "");
    enter_insert(tab, false);
}

fn delete_chars(tab: &mut WorkspaceTab, register: &mut String, count: usize) {
    snapshot(tab);
    let line = &mut tab.buffer[tab.cursor_row];
    let start = tab.cursor_col;
    let end = (start + count.max(1)).min(char_count(line));
    *register = line_slice(line, start, end);
    replace_line_range(line, start, end, "");
    clamp_normal(tab);
}

fn delete_before(tab: &mut WorkspaceTab, register: &mut String, count: usize) {
    snapshot(tab);
    let line = &mut tab.buffer[tab.cursor_row];
    let cur = tab.cursor_col.min(char_count(line));
    let start = cur.saturating_sub(count.max(1));
    *register = line_slice(line, start, cur);
    replace_line_range(line, start, cur, "");
    tab.cursor_col = start;
    clamp_normal(tab);
}

fn substitute_chars(tab: &mut WorkspaceTab, register: &mut String, count: usize) {
    delete_chars(tab, register, count);
    enter_insert(tab, false);
}

fn replace_chars(tab: &mut WorkspaceTab, register: &mut String, ch: char, count: usize) {
    snapshot(tab);
    let line = &mut tab.buffer[tab.cursor_row];
    let start = tab.cursor_col;
    let end = (start + count.max(1)).min(char_count(line));
    *register = line_slice(line, start, end);
    let replacement: String = std::iter::repeat(ch).take(end.saturating_sub(start)).collect();
    replace_line_range(line, start, end, &replacement);
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

fn search_buffer(tab: &mut WorkspaceTab, reverse: bool) {
    if tab.vim_last_search.is_empty() {
        return;
    }
    let mut matches = Vec::new();
    for (row, line) in tab.buffer.iter().enumerate() {
        let mut start_byte = 0;
        while let Some(pos) = line[start_byte..].find(&tab.vim_last_search) {
            let byte = start_byte + pos;
            let col = line[..byte].chars().count();
            matches.push((row, col));
            start_byte = byte + tab.vim_last_search.len();
            if start_byte >= line.len() {
                break;
            }
        }
    }
    if matches.is_empty() {
        return;
    }
    let cur = (tab.cursor_row, tab.cursor_col);
    let target = if reverse {
        matches
            .iter()
            .rev()
            .copied()
            .find(|m| *m < cur)
            .unwrap_or_else(|| *matches.last().unwrap())
    } else {
        matches
            .iter()
            .copied()
            .find(|m| *m > cur)
            .unwrap_or(matches[0])
    };
    tab.cursor_row = target.0;
    tab.cursor_col = target.1;
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::app::state::WorkspaceTab as Tab;
    use crossterm::event::{KeyCode, KeyEvent};

    fn k(c: char) -> KeyEvent {
        KeyEvent::from(KeyCode::Char(c))
    }

    fn esc() -> KeyEvent {
        KeyEvent::from(KeyCode::Esc)
    }

    fn enter() -> KeyEvent {
        KeyEvent::from(KeyCode::Enter)
    }

    fn keys(tab: &mut Tab, register: &mut String, input: &str) {
        for c in input.chars() {
            handle(tab, register, k(c));
        }
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
    fn gg_and_g_jump() {
        let mut t = tab_with("a\nb\nc");
        let mut reg = String::new();
        handle(&mut t, &mut reg, k('G'));
        assert_eq!(t.cursor_row, 2);
        handle(&mut t, &mut reg, k('g'));
        handle(&mut t, &mut reg, k('g'));
        assert_eq!(t.cursor_row, 0);
    }

    #[test]
    fn j_joins_lines_with_space() {
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

    #[test]
    fn normal_caret_jumps_to_first_nonblank() {
        let mut t = tab_with("  select 1");
        let mut reg = String::new();
        t.cursor_col = 8;

        handle(&mut t, &mut reg, k('^'));

        assert_eq!(t.cursor_col, 2);
    }

    #[test]
    fn visual_word_motions_extend_selection() {
        let mut t = tab_with("alpha beta");
        let mut reg = String::new();

        t.cursor_col = 6;
        handle(&mut t, &mut reg, k('v'));
        handle(&mut t, &mut reg, k('e'));
        handle(&mut t, &mut reg, k('y'));
        assert_eq!(reg, "beta");

        let mut t = tab_with("alpha beta");
        let mut reg = String::new();
        t.cursor_col = 8;
        handle(&mut t, &mut reg, k('v'));
        handle(&mut t, &mut reg, k('b'));
        handle(&mut t, &mut reg, k('y'));
        assert_eq!(reg, "bet");
    }

    #[test]
    fn visual_caret_extends_selection_to_first_nonblank() {
        let mut t = tab_with("  alpha beta");
        let mut reg = String::new();
        t.cursor_col = 9;

        handle(&mut t, &mut reg, k('v'));
        handle(&mut t, &mut reg, k('^'));
        handle(&mut t, &mut reg, k('y'));

        assert_eq!(reg, "alpha be");
    }

    #[test]
    fn counts_apply_to_motions_and_line_operators() {
        let mut t = tab_with("one two three four\nb\nc\nd\ne\nf");
        let mut reg = String::new();

        keys(&mut t, &mut reg, "2w");
        assert_eq!(t.cursor_col, 8, "2w should move to the third word");

        keys(&mut t, &mut reg, "3j");
        assert_eq!(t.cursor_row, 3, "3j should move down three rows");

        keys(&mut t, &mut reg, "2dd");
        assert_eq!(t.buffer, vec!["one two three four", "b", "c", "f"]);
        assert_eq!(reg, "d\ne\n");

        keys(&mut t, &mut reg, "1G");
        assert_eq!(t.cursor_row, 0);
        keys(&mut t, &mut reg, "3G");
        assert_eq!(t.cursor_row, 2);
        keys(&mut t, &mut reg, "2gg");
        assert_eq!(t.cursor_row, 1);
    }

    #[test]
    fn change_commands_enter_insert_mode() {
        let mut t = tab_with("alpha beta gamma\nsecond");
        let mut reg = String::new();

        keys(&mut t, &mut reg, "cw");
        assert_eq!(t.buffer[0], " beta gamma");
        assert_eq!(reg, "alpha");
        assert_eq!(t.vim_mode, VimMode::Insert);
        keys(&mut t, &mut reg, "ALPHA");
        handle(&mut t, &mut reg, esc());
        assert_eq!(t.buffer[0], "ALPHA beta gamma");

        keys(&mut t, &mut reg, "0C");
        assert_eq!(t.buffer[0], "");
        assert_eq!(reg, "ALPHA beta gamma");
        assert_eq!(t.vim_mode, VimMode::Insert);
        keys(&mut t, &mut reg, "replacement");
        handle(&mut t, &mut reg, esc());
        assert_eq!(t.buffer[0], "replacement");

        keys(&mut t, &mut reg, "jcc");
        assert_eq!(t.buffer[1], "");
        assert_eq!(reg, "second\n");
        assert_eq!(t.vim_mode, VimMode::Insert);
    }

    #[test]
    fn normal_substitute_replace_and_line_deletes_work() {
        let mut t = tab_with("abc\ndef");
        let mut reg = String::new();

        handle(&mut t, &mut reg, k('l'));
        keys(&mut t, &mut reg, "rx");
        assert_eq!(t.buffer[0], "axc");
        assert_eq!(t.vim_mode, VimMode::Normal);

        keys(&mut t, &mut reg, "X");
        assert_eq!(t.buffer[0], "xc");
        assert_eq!(reg, "a");

        keys(&mut t, &mut reg, "s");
        assert_eq!(t.buffer[0], "c");
        assert_eq!(reg, "x");
        assert_eq!(t.vim_mode, VimMode::Insert);
        handle(&mut t, &mut reg, k('Z'));
        handle(&mut t, &mut reg, esc());
        assert_eq!(t.buffer[0], "Zc");

        keys(&mut t, &mut reg, "jD");
        assert_eq!(t.buffer[1], "");
        assert_eq!(reg, "def");

        keys(&mut t, &mut reg, "kY");
        assert_eq!(reg, "Zc\n");
    }

    #[test]
    fn dot_repeats_last_normal_change() {
        let mut t = tab_with("abc");
        let mut reg = String::new();

        handle(&mut t, &mut reg, k('x'));
        assert_eq!(t.buffer[0], "bc");
        handle(&mut t, &mut reg, k('.'));
        assert_eq!(t.buffer[0], "c");
    }

    #[test]
    fn insert_undo_restores_pre_insert_buffer() {
        let mut t = tab_with("");
        let mut reg = String::new();

        keys(&mut t, &mut reg, "iabc");
        handle(&mut t, &mut reg, esc());
        assert_eq!(t.buffer[0], "abc");
        handle(&mut t, &mut reg, k('u'));
        assert_eq!(t.buffer[0], "");
    }

    #[test]
    fn normal_search_moves_with_n_and_shift_n() {
        let mut t = tab_with("select 1\nfrom users\nwhere user_id = 1\norder by user_id");
        let mut reg = String::new();

        keys(&mut t, &mut reg, "/user");
        handle(&mut t, &mut reg, enter());
        assert_eq!((t.cursor_row, t.cursor_col), (1, 5));

        handle(&mut t, &mut reg, k('n'));
        assert_eq!((t.cursor_row, t.cursor_col), (2, 6));

        handle(&mut t, &mut reg, k('N'));
        assert_eq!((t.cursor_row, t.cursor_col), (1, 5));

        keys(&mut t, &mut reg, "?select");
        handle(&mut t, &mut reg, enter());
        assert_eq!((t.cursor_row, t.cursor_col), (0, 0));
    }
}
