//! Keyboard and mouse input dispatch. Routes events to the focused panel/page
//! and resolves mouse clicks against the same hitboxes that were rendered.

use crossterm::event::{
    KeyCode, KeyEvent, KeyEventKind, KeyModifiers, MouseButton, MouseEvent, MouseEventKind,
};

use super::nav;
use super::state::{App, Confirm, ConfirmAction, Focus, Page, TabKind};
use super::theme::StatusKind;
use super::ui::{hit_test, HitId};

pub fn handle_key(app: &mut App, key: KeyEvent) {
    if key.kind == KeyEventKind::Release {
        return;
    }

    // Overlays first.
    if app.error_box.is_some() {
        if matches!(key.code, KeyCode::Esc | KeyCode::Enter | KeyCode::Char('q')) {
            app.error_box = None;
        }
        return;
    }
    if app.help_open {
        if matches!(key.code, KeyCode::Esc | KeyCode::Char('q') | KeyCode::Char('?')) {
            app.help_open = false;
        }
        return;
    }
    if app.confirm.is_some() {
        match key.code {
            KeyCode::Char('y') | KeyCode::Enter => confirm_yes(app),
            KeyCode::Char('n') | KeyCode::Esc => app.confirm = None,
            _ => {}
        }
        return;
    }
    if app.ai.open {
        ai_key(app, key);
        return;
    }
    if app.history_open {
        match key.code {
            KeyCode::Esc | KeyCode::Char('q') => app.history_open = false,
            KeyCode::Char('j') | KeyCode::Down => app.history_move(1),
            KeyCode::Char('k') | KeyCode::Up => app.history_move(-1),
            KeyCode::Enter => app.history_fill(),
            _ => {}
        }
        return;
    }
    if app.form.is_some() {
        form_key(app, key);
        return;
    }

    // Ctrl+C: cancel an in-flight op, otherwise no-op (does not quit).
    if key.code == KeyCode::Char('c') && key.modifiers.contains(KeyModifiers::CONTROL) {
        if app.tasks.cancel_all() {
            app.set_status(StatusKind::Warning, "cancelled");
        }
        return;
    }

    if app.command_active {
        command_key(app, key);
        return;
    }

    match app.page {
        Page::Unlock => unlock_key(app, key),
        Page::Connections => connections_key(app, key),
        Page::Workspace => workspace_key(app, key),
        Page::Help => {
            if matches!(key.code, KeyCode::Esc | KeyCode::Char('q')) {
                app.page = if app.sessions.is_empty() { Page::Connections } else { Page::Workspace };
            }
        }
        Page::Game => match key.code {
            KeyCode::Esc | KeyCode::Char('q') => {
                app.game = None;
                app.page = if app.sessions.is_empty() { Page::Connections } else { Page::Workspace };
            }
            KeyCode::Char(' ') => {
                if let Some(g) = app.game.as_mut() {
                    g.press();
                }
            }
            _ => {}
        },
    }
}

fn confirm_yes(app: &mut App) {
    let Some(confirm) = app.confirm.take() else { return };
    match confirm.action {
        ConfirmAction::DeleteConnection(id) => {
            app.vault.delete_profile(&id);
            let _ = app.store.save(&app.master, &app.vault);
            if app.connections_cursor >= app.vault.profiles.len() {
                app.connections_cursor = app.vault.profiles.len().saturating_sub(1);
            }
            app.set_status(StatusKind::Success, "deleted");
        }
        ConfirmAction::CloseSession(id) => {
            if let Some(idx) = app.sessions.iter().position(|s| s.id == id) {
                app.sessions.remove(idx);
                if app.sessions.is_empty() {
                    app.page = Page::Connections;
                    app.active_session = 0;
                } else {
                    app.active_session = app.active_session.min(app.sessions.len() - 1);
                }
            }
        }
        ConfirmAction::Quit => app.should_quit = true,
    }
}

fn ai_key(app: &mut App, key: KeyEvent) {
    if key.code == KeyCode::Char('y') && key.modifiers.contains(KeyModifiers::CONTROL) {
        app.ai_insert_sql();
        return;
    }
    match key.code {
        KeyCode::Esc => {
            if app.tasks.cancel_all() {
                app.ai.pending = false;
            } else {
                app.close_ai();
            }
        }
        KeyCode::Enter => app.ai_submit(),
        KeyCode::Char(c) => app.ai.input.push(c),
        KeyCode::Backspace => {
            app.ai.input.pop();
        }
        _ => {}
    }
}

fn form_key(app: &mut App, key: KeyEvent) {
    let selecting = app.form.as_ref().map(|f| f.selecting_driver).unwrap_or(false);
    if selecting {
        match key.code {
            KeyCode::Left => {
                if let Some(f) = app.form.as_mut() {
                    f.driver_index = f.driver_index.saturating_sub(1);
                }
            }
            KeyCode::Right => {
                if let Some(f) = app.form.as_mut() {
                    f.driver_index = (f.driver_index + 1).min(3);
                }
            }
            KeyCode::Enter => app.confirm_form_driver(),
            KeyCode::Esc => app.close_form(),
            _ => {}
        }
        return;
    }

    match key.code {
        KeyCode::Esc => app.close_form(),
        KeyCode::Enter => app.submit_form(),
        KeyCode::Tab | KeyCode::Down => {
            if let Some(f) = app.form.as_mut() {
                f.active_field = (f.active_field + 1) % f.fields.len().max(1);
            }
        }
        KeyCode::BackTab | KeyCode::Up => {
            if let Some(f) = app.form.as_mut() {
                let n = f.fields.len().max(1);
                f.active_field = (f.active_field + n - 1) % n;
            }
        }
        KeyCode::Char(c) => {
            if let Some(f) = app.form.as_mut() {
                if let Some(field) = f.fields.get_mut(f.active_field) {
                    let byte = char_byte_index(&field.value, field.cursor);
                    field.value.insert(byte, c);
                    field.cursor += 1;
                }
            }
        }
        KeyCode::Backspace => {
            if let Some(f) = app.form.as_mut() {
                if let Some(field) = f.fields.get_mut(f.active_field) {
                    if field.cursor > 0 {
                        let b = char_byte_index(&field.value, field.cursor - 1);
                        let e = char_byte_index(&field.value, field.cursor);
                        field.value.replace_range(b..e, "");
                        field.cursor -= 1;
                    }
                }
            }
        }
        KeyCode::Left => {
            if let Some(f) = app.form.as_mut() {
                if let Some(field) = f.fields.get_mut(f.active_field) {
                    field.cursor = field.cursor.saturating_sub(1);
                }
            }
        }
        KeyCode::Right => {
            if let Some(f) = app.form.as_mut() {
                if let Some(field) = f.fields.get_mut(f.active_field) {
                    let len = field.value.chars().count();
                    field.cursor = (field.cursor + 1).min(len);
                }
            }
        }
        _ => {}
    }
}

fn unlock_key(app: &mut App, key: KeyEvent) {
    match key.code {
        KeyCode::Char(c) => app.unlock_input.push(c),
        KeyCode::Backspace => {
            app.unlock_input.pop();
        }
        KeyCode::Enter => unlock(app),
        KeyCode::Esc => app.should_quit = true,
        _ => {}
    }
}

fn unlock(app: &mut App) {
    let pwd = app.unlock_input.clone();
    if pwd.is_empty() {
        app.unlock_error = Some("password required".into());
        return;
    }
    if app.first_run {
        app.master = pwd;
        match app.store.save(&app.master, &app.vault) {
            Ok(()) => {
                app.first_run = false;
                app.page = Page::Connections;
                app.set_status(StatusKind::Success, "vault created");
            }
            Err(e) => app.unlock_error = Some(e.to_string()),
        }
        return;
    }
    match app.store.load(&pwd) {
        Ok(vault) => {
            app.master = pwd;
            app.vault = vault;
            app.page = Page::Connections;
            app.unlock_error = None;
            app.set_status(StatusKind::Success, "unlocked");
        }
        Err(e) => {
            app.unlock_error = Some(e.to_string());
            app.unlock_input.clear();
        }
    }
}

fn connections_key(app: &mut App, key: KeyEvent) {
    let count = app.vault.profiles.len();
    match key.code {
        KeyCode::Char(':') => start_command(app),
        KeyCode::Char('?') => app.help_open = true,
        KeyCode::Char('q') => app.should_quit = true,
        KeyCode::Char('j') | KeyCode::Down => {
            if count > 0 {
                app.connections_cursor = (app.connections_cursor + 1).min(count - 1);
            }
        }
        KeyCode::Char('k') | KeyCode::Up => {
            app.connections_cursor = app.connections_cursor.saturating_sub(1);
        }
        KeyCode::Char('G') => {
            if count > 0 {
                app.connections_cursor = count - 1;
            }
        }
        KeyCode::Char('n') => app.open_new_form(),
        KeyCode::Char('e') => {
            if let Some(p) = app.vault.profiles.get(app.connections_cursor) {
                let id = p.id.clone();
                app.open_edit_form(&id);
            }
        }
        KeyCode::Char('d') => {
            if let Some(p) = app.vault.profiles.get(app.connections_cursor) {
                let id = p.id.clone();
                app.confirm = Some(Confirm {
                    message: format!("Delete connection '{id}'?"),
                    action: ConfirmAction::DeleteConnection(id),
                });
            }
        }
        KeyCode::Char('t') => {
            if let Some(p) = app.vault.profiles.get(app.connections_cursor) {
                let id = p.id.clone();
                app.test_connection(&id);
            }
        }
        KeyCode::Enter => {
            if let Some(p) = app.vault.profiles.get(app.connections_cursor) {
                let id = p.id.clone();
                app.open_connection(&id);
            }
        }
        _ => {}
    }
}

fn workspace_key(app: &mut App, key: KeyEvent) {
    // Connection switching.
    if key.modifiers.contains(KeyModifiers::ALT) {
        match key.code {
            KeyCode::Left => return switch_session(app, -1),
            KeyCode::Right => return switch_session(app, 1),
            KeyCode::Char(c) if c.is_ascii_digit() && c != '0' => {
                let idx = (c as u8 - b'1') as usize;
                if idx < app.sessions.len() {
                    app.active_session = idx;
                }
                return;
            }
            _ => {}
        }
    }

    // When the query editor is focused, it owns most plain keys (so Insert-mode
    // Esc exits to Normal, ':' types literally, etc.). Ctrl/Alt keys, Tab and
    // Ctrl+Enter are still handled globally below. In Normal mode, ':' and '?'
    // fall through to the command bar / help.
    if app.focus == Focus::Main
        && !key.modifiers.contains(KeyModifiers::CONTROL)
        && !key.modifiers.contains(KeyModifiers::ALT)
        && !matches!(key.code, KeyCode::Tab | KeyCode::BackTab)
    {
        let mode = app
            .active_session()
            .and_then(|s| s.active_tab())
            .filter(|t| t.kind == TabKind::Query)
            .map(|t| t.vim_mode);
        if let Some(mode) = mode {
            let allow_global = mode == super::state::VimMode::Normal
                && matches!(key.code, KeyCode::Char(':') | KeyCode::Char('?'));
            if !allow_global {
                main_key(app, key);
                return;
            }
        }
    }

    match key.code {
        KeyCode::Char(':') => return start_command(app),
        KeyCode::Char('?') if app.focus == Focus::Sidebar => {
            app.help_open = true;
            return;
        }
        KeyCode::Esc => {
            if app.tasks.cancel_all() {
                app.set_status(StatusKind::Warning, "cancelled");
            } else if let Some(s) = app.active_session_mut() {
                if s.nav.search_active {
                    s.nav.search_active = false;
                    s.nav.search_query.clear();
                }
            }
            return;
        }
        _ => {}
    }

    // Panel focus switching.
    if key.modifiers.contains(KeyModifiers::CONTROL) {
        match key.code {
            KeyCode::Char('k') => {
                app.toggle_ai();
                return;
            }
            KeyCode::Char('r') => {
                app.open_history();
                return;
            }
            KeyCode::Char('h') => {
                app.focus = Focus::Sidebar;
                return;
            }
            KeyCode::Char('l') => {
                app.focus = Focus::Main;
                return;
            }
            KeyCode::Char('w') => {
                close_active_tab(app);
                return;
            }
            _ => {}
        }
    }

    match app.focus {
        Focus::Sidebar => sidebar_key(app, key),
        Focus::Main => main_key(app, key),
        _ => {}
    }
}

fn sidebar_key(app: &mut App, key: KeyEvent) {
    // Active search input.
    if app.active_session().map(|s| s.nav.search_active).unwrap_or(false) {
        match key.code {
            KeyCode::Char(c) => {
                if let Some(s) = app.active_session_mut() {
                    s.nav.search_query.push(c);
                }
                return;
            }
            KeyCode::Backspace => {
                if let Some(s) = app.active_session_mut() {
                    s.nav.search_query.pop();
                }
                return;
            }
            KeyCode::Enter => {
                if let Some(s) = app.active_session_mut() {
                    s.nav.search_active = false;
                }
                return;
            }
            KeyCode::Esc => {
                if let Some(s) = app.active_session_mut() {
                    s.nav.search_active = false;
                    s.nav.search_query.clear();
                }
                return;
            }
            _ => {}
        }
    }

    let node_count = app
        .active_session()
        .map(|s| nav::visible_nodes(&s.nav, &s.conn_label()).len())
        .unwrap_or(0);
    match key.code {
        KeyCode::Char('j') | KeyCode::Down => move_cursor(app, 1, node_count),
        KeyCode::Char('k') | KeyCode::Up => move_cursor(app, -1, node_count),
        KeyCode::Char('G') => {
            if let Some(s) = app.active_session_mut() {
                s.nav.cursor = node_count.saturating_sub(1);
            }
        }
        KeyCode::Char('g') => {
            if let Some(s) = app.active_session_mut() {
                s.nav.cursor = 0;
            }
        }
        KeyCode::Enter => app.nav_activate(),
        KeyCode::Char('l') | KeyCode::Right => app.nav_expand(),
        KeyCode::Char('h') | KeyCode::Left => app.nav_collapse(),
        KeyCode::Char('/') => {
            if let Some(s) = app.active_session_mut() {
                s.nav.search_active = true;
                s.nav.search_query.clear();
            }
        }
        KeyCode::Char('r') => {
            if let Some(id) = app.active_session().map(|s| s.id) {
                app.load_databases(id);
            }
        }
        _ => {}
    }
}

fn move_cursor(app: &mut App, delta: i32, count: usize) {
    if let Some(s) = app.active_session_mut() {
        let cur = s.nav.cursor as i32 + delta;
        s.nav.cursor = cur.clamp(0, count.saturating_sub(1) as i32) as usize;
    }
}

fn main_key(app: &mut App, key: KeyEvent) {
    let kind = app
        .active_session()
        .and_then(|s| s.active_tab())
        .map(|t| t.kind);
    // Ctrl+Enter runs a query regardless of tab kind context.
    if key.code == KeyCode::Enter && key.modifiers.contains(KeyModifiers::CONTROL) {
        app.run_active_query();
        return;
    }
    match kind {
        Some(TabKind::Query) => {
            // When the live completion popup is open, it owns Tab/arrows/Esc.
            if app.completion_open() {
                let ctrl = key.modifiers.contains(KeyModifiers::CONTROL);
                match key.code {
                    KeyCode::Tab => return app.completion_accept(),
                    KeyCode::Down => return app.completion_move(true),
                    KeyCode::Up | KeyCode::BackTab => return app.completion_move(false),
                    KeyCode::Char('n') if ctrl => return app.completion_move(true),
                    KeyCode::Char('p') if ctrl => return app.completion_move(false),
                    KeyCode::Esc => return app.completion_dismiss(),
                    _ => {}
                }
            }
            // Run the vim layer with the shared yank register (split borrow).
            let mut reg = std::mem::take(&mut app.vim_register);
            if let Some(tab) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
                super::vim::handle(tab, &mut reg, key);
            }
            app.vim_register = reg;
            // Recompute live suggestions after the edit/motion.
            app.editor_update_completion();
        }
        Some(TabKind::Data) => data_key(app, key),
        None => {
            if key.code == KeyCode::Char('n') {
                app.new_query_tab();
            }
        }
    }
}

/// Byte index of the `col`-th character (for rune-safe editing, contract §1).
fn char_byte_index(s: &str, col: usize) -> usize {
    s.char_indices().nth(col).map(|(b, _)| b).unwrap_or(s.len())
}

fn data_key(app: &mut App, key: KeyEvent) {
    // Result search input mode (contract: in-result `/` search).
    let searching = app
        .active_session()
        .and_then(|s| s.active_tab())
        .map(|t| t.result_search_active)
        .unwrap_or(false);
    if searching {
        match key.code {
            KeyCode::Char(c) => {
                if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
                    t.result_search.push(c);
                }
                app.result_search_jump(0);
            }
            KeyCode::Backspace => {
                if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
                    t.result_search.pop();
                }
            }
            KeyCode::Enter => {
                if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
                    t.result_search_active = false;
                }
            }
            KeyCode::Esc => {
                if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
                    t.result_search_active = false;
                    t.result_search.clear();
                }
            }
            _ => {}
        }
        return;
    }

    let has_query = app
        .active_session()
        .and_then(|s| s.active_tab())
        .map(|t| !t.result_search.is_empty())
        .unwrap_or(false);

    match key.code {
        KeyCode::Char('/') => {
            if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
                t.result_search_active = true;
                t.result_search.clear();
            }
        }
        KeyCode::Char('n') if has_query => app.result_search_jump(1),
        KeyCode::Char('N') if has_query => app.result_search_jump(-1),
        KeyCode::Char('n') => app.page_data(1),
        KeyCode::Char(' ') => app.page_data(1),
        KeyCode::Char('p') => app.page_data(-1),
        KeyCode::Char('r') => app.page_data(0),
        KeyCode::Char('j') | KeyCode::Down => scroll_rows(app, 1),
        KeyCode::Char('k') | KeyCode::Up => scroll_rows(app, -1),
        KeyCode::Char('l') | KeyCode::Right => scroll_cols(app, 1),
        KeyCode::Char('h') | KeyCode::Left => scroll_cols(app, -1),
        KeyCode::PageDown => scroll_rows(app, 10),
        KeyCode::PageUp => scroll_rows(app, -10),
        KeyCode::Char('m') => app.toggle_metadata(),
        _ => {}
    }
}

fn scroll_rows(app: &mut App, delta: i32) {
    if let Some(tab) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
        let max = tab
            .result
            .as_ref()
            .and_then(|s| s.table.as_ref().map(|t| t.rows.len()))
            .or_else(|| tab.result.as_ref().map(|s| s.documents.len()))
            .unwrap_or(0);
        // Move the selected row; the renderer scrolls to keep it visible.
        let cur = tab.view.selected_row as i32 + delta;
        tab.view.selected_row = cur.clamp(0, max.saturating_sub(1) as i32) as usize;
    }
}

fn scroll_cols(app: &mut App, delta: i32) {
    if let Some(tab) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
        let cur = tab.view.col_offset as i32 + delta;
        let max = tab
            .result
            .as_ref()
            .and_then(|s| s.table.as_ref().map(|t| t.columns.len()))
            .unwrap_or(0);
        tab.view.col_offset = cur.clamp(0, max.saturating_sub(1) as i32) as usize;
    }
}

fn close_active_tab(app: &mut App) {
    let close_session = {
        let Some(s) = app.active_session_mut() else { return };
        if !s.tabs.is_empty() {
            s.tabs.remove(s.active_tab);
            if s.active_tab >= s.tabs.len() {
                s.active_tab = s.tabs.len().saturating_sub(1);
            }
        }
        s.tabs.is_empty()
    };
    if close_session {
        if let Some(id) = app.active_session().map(|s| s.id) {
            app.confirm = Some(Confirm {
                message: "Close this connection?".into(),
                action: ConfirmAction::CloseSession(id),
            });
        }
    }
}

fn switch_session(app: &mut App, delta: i32) {
    if app.sessions.is_empty() {
        return;
    }
    let n = app.sessions.len() as i32;
    let cur = app.active_session as i32 + delta;
    app.active_session = ((cur % n + n) % n) as usize;
}

fn start_command(app: &mut App) {
    app.command_active = true;
    app.command.clear();
    app.previous_focus = app.focus;
    app.focus = Focus::Command;
}

fn command_key(app: &mut App, key: KeyEvent) {
    match key.code {
        KeyCode::Char(c) => app.command.push(c),
        KeyCode::Backspace => {
            app.command.pop();
        }
        KeyCode::Esc => {
            app.command_active = false;
            app.focus = app.previous_focus;
        }
        KeyCode::Enter => {
            let cmd = app.command.clone();
            app.command_active = false;
            app.focus = app.previous_focus;
            run_command(app, &cmd);
        }
        _ => {}
    }
}

fn run_command(app: &mut App, cmd: &str) {
    let cmd = cmd.trim();
    let parts: Vec<&str> = cmd.split_whitespace().collect();
    match parts.first().copied() {
        Some("q") | Some("quit") | Some("exit") => app.should_quit = true,
        Some("help") => app.help_open = true,
        Some("timeout") => {
            if let Some(n) = parts.get(1).and_then(|s| s.parse::<u64>().ok()) {
                app.tasks.query_timeout = n;
                app.set_status(StatusKind::Info, format!("query timeout = {n}s"));
            }
        }
        Some("new") => app.open_new_form(),
        Some("game") => {
            app.game = Some(super::game::Game::new());
            app.page = Page::Game;
        }
        Some("export") => {
            let format = parts.get(1).copied().unwrap_or("csv");
            app.export_result(format, parts.get(2).copied());
        }
        Some("copy") => {
            let format = parts.get(1).copied().unwrap_or("csv");
            app.copy_result(format);
        }
        Some(_) => app.set_status(StatusKind::Warning, format!("unknown command: {cmd}")),
        None => {}
    }
}

// ---- mouse ----

pub fn handle_mouse(app: &mut App, ev: MouseEvent) {
    match ev.kind {
        MouseEventKind::Down(MouseButton::Left) => {
            // Begin a potential drag-selection; the click action fires on release
            // only if the mouse did not move (contract §11).
            app.selection.active = false;
            app.selection.dragging = true;
            app.selection.moved = false;
            app.selection.anchor = (ev.column, ev.row);
            app.selection.cursor = (ev.column, ev.row);
        }
        MouseEventKind::Drag(MouseButton::Left) => {
            if app.selection.dragging {
                app.selection.cursor = (ev.column, ev.row);
                if app.selection.cursor != app.selection.anchor {
                    app.selection.moved = true;
                    app.selection.active = true;
                }
            }
        }
        MouseEventKind::Up(MouseButton::Left) => {
            let sel = std::mem::take(&mut app.selection);
            if sel.dragging && sel.moved {
                app.copy_selection(sel.anchor, sel.cursor);
            } else if let Some(id) = hit_test(&app.hitboxes, ev.column, ev.row) {
                handle_click(app, id);
            }
        }
        MouseEventKind::ScrollDown => scroll_rows(app, 1),
        MouseEventKind::ScrollUp => scroll_rows(app, -1),
        _ => {}
    }
}

fn handle_click(app: &mut App, id: HitId) {
    match id {
        HitId::ConnTab(i) => {
            if i < app.sessions.len() {
                app.active_session = i;
                app.page = Page::Workspace;
            }
        }
        HitId::ConnAdd => app.open_new_form(),
        HitId::PanelSidebar => app.focus = Focus::Sidebar,
        HitId::PanelMain => app.focus = Focus::Main,
        HitId::NavNode(i) => {
            app.focus = Focus::Sidebar;
            if let Some(s) = app.active_session_mut() {
                s.nav.cursor = i;
            }
            app.nav_activate();
        }
        HitId::ConnRow(i) => {
            app.connections_cursor = i;
        }
        HitId::WorkspaceTab(i) => {
            if let Some(s) = app.active_session_mut() {
                if i < s.tabs.len() {
                    s.active_tab = i;
                }
            }
            app.focus = Focus::Main;
        }
        HitId::QueryRun(_) => app.run_active_query(),
        HitId::FormField(i) => {
            if let Some(f) = app.form.as_mut() {
                if i < f.fields.len() {
                    f.active_field = i;
                }
            }
        }
        HitId::FormOk => app.submit_form(),
        HitId::FormCancel => app.close_form(),
        HitId::ConfirmOk => confirm_yes(app),
        HitId::ConfirmCancel => app.confirm = None,
        HitId::ErrorClose => app.error_box = None,
        HitId::HelpClose => app.help_open = false,
    }
}
