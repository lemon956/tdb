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
    let mentioning = !app.ai.mention_items.is_empty();
    match key.code {
        // @-mention completion owns Tab/Shift+Tab (cycle) and Enter (accept).
        KeyCode::Tab if mentioning => app.ai_mention_move(true),
        KeyCode::BackTab if mentioning => app.ai_mention_move(false),
        KeyCode::Esc => {
            if mentioning {
                app.ai.mention_items.clear();
            } else if app.tasks.cancel_all() {
                app.ai.pending = false;
            } else {
                app.close_ai();
            }
        }
        KeyCode::Enter => {
            if mentioning {
                app.accept_ai_mention();
            } else {
                app.ai_submit();
            }
        }
        KeyCode::Char(c) => {
            app.ai.input.push(c);
            app.ai_update_mention();
        }
        KeyCode::Backspace => {
            app.ai.input.pop();
            app.ai_update_mention();
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
                // Esc clears the nav search whether the input box is open or the
                // filter was already committed with Enter.
                if s.nav.search_active || !s.nav.search_query.is_empty() {
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
    // Active search input: jump-style — typing moves the cursor to the first
    // match (the tree is never filtered); Enter commits but keeps the query so
    // n/N keep cycling matches.
    if app.active_session().map(|s| s.nav.search_active).unwrap_or(false) {
        match key.code {
            KeyCode::Char(c) => {
                if let Some(s) = app.active_session_mut() {
                    s.nav.search_query.push(c);
                }
                app.nav_search_jump(0);
                return;
            }
            KeyCode::Backspace => {
                if let Some(s) = app.active_session_mut() {
                    s.nav.search_query.pop();
                }
                app.nav_search_jump(0);
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

    // After committing a search, n/N cycle through the matches.
    let has_query = app
        .active_session()
        .map(|s| !s.nav.search_query.is_empty())
        .unwrap_or(false);

    let node_count = app
        .active_session()
        .map(|s| nav::visible_nodes(&s.nav, &s.conn_label()).len())
        .unwrap_or(0);
    let g_action = app
        .active_session_mut()
        .map(|s| sidebar_g_action(&mut s.nav.pending_g, key.code, node_count))
        .unwrap_or(SidebarGAction::Fallthrough);
    match g_action {
        SidebarGAction::Pending => return,
        SidebarGAction::Jump(pos) => {
            if let Some(s) = app.active_session_mut() {
                s.nav.cursor = pos;
            }
            return;
        }
        SidebarGAction::Fallthrough => {}
    }
    match key.code {
        KeyCode::Char('n') if has_query => app.nav_search_jump(1),
        KeyCode::Char('N') if has_query => app.nav_search_jump(-1),
        KeyCode::Char('j') | KeyCode::Down => move_cursor(app, 1, node_count),
        KeyCode::Char('k') | KeyCode::Up => move_cursor(app, -1, node_count),
        KeyCode::Enter => app.nav_activate(),
        KeyCode::Char('l') | KeyCode::Right => app.nav_expand(),
        KeyCode::Char('h') | KeyCode::Left => app.nav_collapse(),
        KeyCode::Char('/') => {
            if let Some(s) = app.active_session_mut() {
                s.nav.search_active = true;
                s.nav.search_query.clear();
                s.nav.search_match_index = 0;
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

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum SidebarGAction {
    Pending,
    Jump(usize),
    Fallthrough,
}

fn sidebar_g_action(pending_g: &mut bool, key: KeyCode, node_count: usize) -> SidebarGAction {
    if *pending_g {
        *pending_g = false;
        return if matches!(key, KeyCode::Char('g')) {
            SidebarGAction::Jump(0)
        } else {
            SidebarGAction::Fallthrough
        };
    }

    match key {
        KeyCode::Char('g') => {
            *pending_g = true;
            SidebarGAction::Pending
        }
        KeyCode::Char('G') => SidebarGAction::Jump(node_count.saturating_sub(1)),
        _ => SidebarGAction::Fallthrough,
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
            // When the live completion popup is open, it owns Tab/arrows/Enter/Esc.
            // Tab cycles the highlight through the list; Enter accepts it.
            if app.completion_open() {
                let ctrl = key.modifiers.contains(KeyModifiers::CONTROL);
                match key.code {
                    KeyCode::Tab | KeyCode::Down => return app.completion_move(true),
                    KeyCode::BackTab | KeyCode::Up => return app.completion_move(false),
                    KeyCode::Char('n') if ctrl => return app.completion_move(true),
                    KeyCode::Char('p') if ctrl => return app.completion_move(false),
                    KeyCode::Enter => return app.completion_accept(),
                    KeyCode::Esc => return app.completion_dismiss(),
                    _ => {}
                }
            }
            // Sub-focus: while the result list is focused it owns navigation keys
            // (j/k scroll, `/` search, …); Tab/Esc returns focus to the editor.
            let result_focused = app
                .active_session()
                .and_then(|s| s.active_tab())
                .map(|t| t.result_focused)
                .unwrap_or(false);
            if result_focused {
                // While searching or in the row-detail view, those modes own
                // Esc/Tab (close popup / close detail); only return focus to the
                // editor when neither is active.
                let owns_keys = app
                    .active_session()
                    .and_then(|s| s.active_tab())
                    .map(|t| {
                        t.result_search_active
                            || t.view.detail
                            || t.view.text.search_active
                            || t.view.text.visual.is_some()
                    })
                    .unwrap_or(false);
                if !owns_keys && result_focus_return_key(key.code) {
                    if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
                        t.result_focused = false;
                    }
                    return;
                }
                return data_key(app, key);
            }
            // Editor-focused: Tab moves focus down to the result list (if any).
            if key.code == KeyCode::Tab
                && app
                    .active_session()
                    .and_then(|s| s.active_tab())
                    .map(|t| t.result.is_some())
                    .unwrap_or(false)
            {
                if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
                    t.result_focused = true;
                }
                return;
            }
            // NORMAL-mode Enter runs the query: a universal fallback for terminals
            // that cannot report Ctrl+Enter distinctly.
            let normal_mode = app
                .active_session()
                .and_then(|s| s.active_tab())
                .map(|t| t.vim_mode == super::state::VimMode::Normal)
                .unwrap_or(false);
            if key.code == KeyCode::Enter && normal_mode {
                app.run_active_query();
                return;
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

fn result_focus_return_key(key: KeyCode) -> bool {
    matches!(key, KeyCode::Tab | KeyCode::Esc | KeyCode::Char('q'))
}

/// Byte index of the `col`-th character (for rune-safe editing, contract §1).
fn char_byte_index(s: &str, col: usize) -> usize {
    s.char_indices().nth(col).map(|(b, _)| b).unwrap_or(s.len())
}

fn data_key(app: &mut App, key: KeyEvent) {
    // The read-only vim text pane owns keys for SQL row detail (exitable) and
    // Mongo documents (not exitable — the pane is the whole result view).
    if let Some(exitable) = pane_mode(app) {
        text_pane_key(app, key, exitable);
        return;
    }

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
                app.result_search_jump(0);
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

    // Resolve a pending `g` (gg → first row); only a second `g` continues it.
    let pending_g = app
        .active_session()
        .and_then(|s| s.active_tab())
        .map(|t| t.view.pending_g)
        .unwrap_or(false);
    if pending_g {
        if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
            t.view.pending_g = false;
        }
        if matches!(key.code, KeyCode::Char('g')) {
            cursor_goto_row(app, 0);
            return;
        }
        // Otherwise fall through and handle the key normally.
    }

    match key.code {
        KeyCode::Char('/') => {
            if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
                t.result_search_active = true;
                t.result_search.clear();
            }
        }
        KeyCode::Char('n') if has_query => app.result_search_jump(1),
        KeyCode::Char('N') if has_query => app.result_search_jump(-1),
        KeyCode::Char('j') | KeyCode::Down => move_row(app, 1),
        KeyCode::Char('k') | KeyCode::Up => move_row(app, -1),
        KeyCode::Char('l') | KeyCode::Right => scroll_col(app, 1),
        KeyCode::Char('h') | KeyCode::Left => scroll_col(app, -1),
        KeyCode::PageDown => move_row(app, 10),
        KeyCode::PageUp => move_row(app, -10),
        KeyCode::Char('g') => {
            if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
                t.view.pending_g = true;
            }
        }
        KeyCode::Char('G') => cursor_goto_row(app, usize::MAX),
        KeyCode::Char('0') | KeyCode::Home => goto_col(app, 0),
        KeyCode::Char('$') | KeyCode::End => goto_col(app, usize::MAX),
        KeyCode::Char('v') => toggle_visual(app),
        KeyCode::Char('y') => app.copy_result_rows(),
        KeyCode::Esc => clear_visual(app),
        KeyCode::Enter => open_row_detail(app),
        KeyCode::Char('[') => app.page_data(-1),
        KeyCode::Char(']') => app.page_data(1),
        KeyCode::Char(' ') => app.page_data(1),
        KeyCode::Char('p') => app.page_data(-1),
        KeyCode::Char('r') => app.page_data(0),
        KeyCode::Char('n') => app.page_data(1),
        KeyCode::Char('m') => app.toggle_metadata(),
        _ => {}
    }
}

/// (rows, cols) of the active result; documents count as `cols = 1`.
fn result_dims(app: &App) -> (usize, usize) {
    app.active_session()
        .and_then(|s| s.active_tab())
        .and_then(|t| t.result.as_ref())
        .map(|set| match &set.table {
            Some(tbl) => (tbl.rows.len(), tbl.columns.len()),
            None => (set.documents.len(), 1),
        })
        .unwrap_or((0, 0))
}

/// Move the row cursor by `delta`, clamped to the grid. Visual selection (if
/// active) extends because the renderer derives the range from anchor+cursor.
fn move_row(app: &mut App, delta: i32) {
    let (nrows, _) = result_dims(app);
    if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
        if nrows > 0 {
            t.view.selected_row = (t.view.selected_row as i32 + delta).clamp(0, nrows as i32 - 1) as usize;
        }
    }
}

fn cursor_goto_row(app: &mut App, row: usize) {
    let (nrows, _) = result_dims(app);
    if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
        if nrows > 0 {
            t.view.selected_row = row.min(nrows - 1);
        }
    }
}

/// Horizontal scroll: shift the leftmost visible column by `delta`.
fn scroll_col(app: &mut App, delta: i32) {
    let (_, ncols) = result_dims(app);
    if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
        if ncols > 0 {
            t.view.col_offset = (t.view.col_offset as i32 + delta).clamp(0, ncols as i32 - 1) as usize;
        }
    }
}

fn goto_col(app: &mut App, col: usize) {
    let (_, ncols) = result_dims(app);
    if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
        if ncols > 0 {
            t.view.col_offset = col.min(ncols - 1);
        }
    }
}

fn toggle_visual(app: &mut App) {
    if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
        t.view.visual = match t.view.visual {
            Some(_) => None,
            None => Some(t.view.selected_row),
        };
    }
}

fn clear_visual(app: &mut App) {
    if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
        t.view.visual = None;
    }
}

/// Enter the row-detail vim pane (table results only; documents are their own
/// pane). Resets the text cursor to the top.
fn open_row_detail(app: &mut App) {
    if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
        if t.result.as_ref().map(|s| s.table.is_some()).unwrap_or(false) {
            t.view.detail = true;
            t.view.text.reset();
        }
    }
}

// ---- read-only vim text pane (SQL row detail + Mongo documents) ----

/// `Some(exitable)` when a text pane is active: `true` = SQL row detail (Esc/q
/// returns to the grid), `false` = Mongo documents (the pane is the whole view).
fn pane_mode(app: &App) -> Option<bool> {
    let tab = app.active_session().and_then(|s| s.active_tab())?;
    let set = tab.result.as_ref()?;
    if set.table.is_some() {
        tab.view.detail.then_some(true)
    } else if !set.documents.is_empty() {
        Some(false)
    } else {
        None
    }
}

/// Content lines of the active text pane (mirrors what `ui::pane_lines` renders).
fn active_text_lines(app: &App) -> Vec<String> {
    app.active_session()
        .and_then(|s| s.active_tab())
        .map(super::ui::pane_lines)
        .unwrap_or_default()
}

/// Char length of line `idx` in `lines` (0 if out of range).
fn line_chars(lines: &[String], idx: usize) -> usize {
    lines.get(idx).map(|l| l.chars().count()).unwrap_or(0)
}

fn text_first_nonblank_col(line: &str) -> usize {
    line.chars().position(|c| !c.is_whitespace()).unwrap_or(0)
}

fn text_pane_key(app: &mut App, key: KeyEvent, exitable: bool) {
    let lines = active_text_lines(app);
    let nlines = lines.len();

    // In-pane `/` search input mode.
    let searching = app
        .active_session()
        .and_then(|s| s.active_tab())
        .map(|t| t.view.text.search_active)
        .unwrap_or(false);
    if searching {
        match key.code {
            KeyCode::Char(c) => {
                text_mut(app, |t| t.search.push(c));
                text_search_jump(app, &lines, 0);
            }
            KeyCode::Backspace => {
                text_mut(app, |t| {
                    t.search.pop();
                });
                text_search_jump(app, &lines, 0);
            }
            KeyCode::Enter => text_mut(app, |t| t.search_active = false),
            KeyCode::Esc => text_mut(app, |t| {
                t.search_active = false;
                t.search.clear();
            }),
            _ => {}
        }
        return;
    }

    let has_search = app
        .active_session()
        .and_then(|s| s.active_tab())
        .map(|t| !t.view.text.search.is_empty())
        .unwrap_or(false);

    // Resolve a pending `g` (gg → first line).
    let pending_g = app
        .active_session()
        .and_then(|s| s.active_tab())
        .map(|t| t.view.text.pending_g)
        .unwrap_or(false);
    if pending_g {
        text_mut(app, |t| t.pending_g = false);
        if matches!(key.code, KeyCode::Char('g')) {
            text_goto_line(app, &lines, 0);
            return;
        }
    }

    if let Some(delta) = text_ctrl_motion_delta(key.code, key.modifiers) {
        text_move_line(app, &lines, delta);
        return;
    }

    match key.code {
        KeyCode::Char('/') => text_mut(app, |t| {
            t.search_active = true;
            t.search.clear();
        }),
        KeyCode::Char('n') if has_search => text_search_jump(app, &lines, 1),
        KeyCode::Char('N') if has_search => text_search_jump(app, &lines, -1),
        KeyCode::Char('h') | KeyCode::Left => text_move_col(app, &lines, -1),
        KeyCode::Char('l') | KeyCode::Right => text_move_col(app, &lines, 1),
        KeyCode::Char('j') | KeyCode::Down => text_move_line(app, &lines, 1),
        KeyCode::Char('k') | KeyCode::Up => text_move_line(app, &lines, -1),
        KeyCode::PageDown => text_move_line(app, &lines, 10),
        KeyCode::PageUp => text_move_line(app, &lines, -10),
        KeyCode::Char('w') => text_word(app, &lines, TextWordMotion::ForwardStartSmall),
        KeyCode::Char('b') => text_word(app, &lines, TextWordMotion::BackStartSmall),
        KeyCode::Char('e') => text_word(app, &lines, TextWordMotion::EndSmall),
        KeyCode::Char('W') => text_word(app, &lines, TextWordMotion::ForwardStartBig),
        KeyCode::Char('B') => text_word(app, &lines, TextWordMotion::BackStartBig),
        KeyCode::Char('E') => text_word(app, &lines, TextWordMotion::EndBig),
        KeyCode::Char('0') | KeyCode::Home => text_mut(app, |t| t.col = 0),
        KeyCode::Char('^') => {
            let target = lines.get(text_line(app)).map(|line| text_first_nonblank_col(line)).unwrap_or(0);
            text_mut(app, |t| t.col = target);
        }
        KeyCode::Char('$') | KeyCode::End => {
            let len = line_chars(&lines, text_line(app));
            text_mut(app, |t| t.col = len.saturating_sub(1));
        }
        KeyCode::Char('g') => text_mut(app, |t| t.pending_g = true),
        KeyCode::Char('G') => text_goto_line(app, &lines, nlines.saturating_sub(1)),
        KeyCode::Char('v') => text_mut(app, |t| {
            t.visual = if t.visual.is_some() && !t.linewise { None } else { Some((t.line, t.col)) };
            t.linewise = false;
        }),
        KeyCode::Char('V') => text_mut(app, |t| {
            t.visual = if t.visual.is_some() && t.linewise { None } else { Some((t.line, t.col)) };
            t.linewise = true;
        }),
        KeyCode::Char('y') => app.copy_text_selection(),
        KeyCode::Esc => {
            let had_visual = app
                .active_session()
                .and_then(|s| s.active_tab())
                .map(|t| t.view.text.visual.is_some())
                .unwrap_or(false);
            if had_visual {
                text_mut(app, |t| t.visual = None);
            } else if exitable {
                if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
                    t.view.detail = false;
                }
            }
        }
        KeyCode::Char('q') if exitable => {
            if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
                t.view.detail = false;
            }
        }
        KeyCode::Char('[') if exitable => switch_detail_row(app, -1),
        KeyCode::Char(']') if exitable => switch_detail_row(app, 1),
        KeyCode::Char('[') => text_doc_jump(app, &lines, -1),
        KeyCode::Char(']') => text_doc_jump(app, &lines, 1),
        KeyCode::Char(' ') => app.page_data(1),
        KeyCode::Char('p') => app.page_data(-1),
        KeyCode::Char('r') => app.page_data(0),
        _ => {}
    }
}

/// Mutably access the active tab's text cursor.
fn text_mut(app: &mut App, f: impl FnOnce(&mut super::state::TextCursor)) {
    if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
        f(&mut t.view.text);
    }
}

fn text_line(app: &App) -> usize {
    app.active_session()
        .and_then(|s| s.active_tab())
        .map(|t| t.view.text.line)
        .unwrap_or(0)
}

fn text_move_col(app: &mut App, lines: &[String], delta: i32) {
    let len = line_chars(lines, text_line(app));
    text_mut(app, |t| {
        t.col = (t.col.min(len.saturating_sub(1)) as i32 + delta).clamp(0, len.saturating_sub(1) as i32) as usize;
    });
}

fn text_move_line(app: &mut App, lines: &[String], delta: i32) {
    let n = lines.len();
    if n == 0 {
        return;
    }
    text_mut(app, |t| {
        t.line = (t.line as i32 + delta).clamp(0, n as i32 - 1) as usize;
        let len = lines[t.line].chars().count();
        t.col = t.col.min(len.saturating_sub(1));
    });
}

fn text_goto_line(app: &mut App, lines: &[String], line: usize) {
    let n = lines.len();
    if n == 0 {
        return;
    }
    text_mut(app, |t| {
        t.line = line.min(n - 1);
        let len = lines[t.line].chars().count();
        t.col = t.col.min(len.saturating_sub(1));
    });
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum TextWordMotion {
    ForwardStartSmall,
    BackStartSmall,
    EndSmall,
    ForwardStartBig,
    BackStartBig,
    EndBig,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum TextWordClass {
    Space,
    Keyword,
    Punct,
}

fn text_ctrl_motion_delta(key: KeyCode, modifiers: KeyModifiers) -> Option<i32> {
    if !modifiers.contains(KeyModifiers::CONTROL) {
        return None;
    }
    match key {
        KeyCode::Char('d') => Some(5),
        KeyCode::Char('u') => Some(-5),
        KeyCode::Char('f') => Some(10),
        KeyCode::Char('b') => Some(-10),
        _ => None,
    }
}

/// Word motion within the current line. Lowercase motions use Vim-style
/// word/punctuation boundaries; uppercase motions use whitespace WORDs.
fn text_word(app: &mut App, lines: &[String], motion: TextWordMotion) {
    let li = text_line(app);
    let Some(line) = lines.get(li) else { return };
    if line.is_empty() {
        return;
    }
    let col = app.active_session().and_then(|s| s.active_tab()).map(|t| t.view.text.col).unwrap_or(0);
    let new_col = text_word_target(line, col, motion);
    text_mut(app, |t| t.col = new_col);
}

fn text_word_target(line: &str, col: usize, motion: TextWordMotion) -> usize {
    let chars: Vec<char> = line.chars().collect();
    if chars.is_empty() {
        return 0;
    }
    let big = matches!(
        motion,
        TextWordMotion::ForwardStartBig | TextWordMotion::BackStartBig | TextWordMotion::EndBig
    );
    let col = col.min(chars.len() - 1);
    match motion {
        TextWordMotion::ForwardStartSmall | TextWordMotion::ForwardStartBig => {
            let mut i = col;
            let class = text_word_class(chars[i], big);
            if class == TextWordClass::Space {
                while i < chars.len() && text_word_class(chars[i], big) == TextWordClass::Space {
                    i += 1;
                }
            } else {
                while i < chars.len() && text_word_class(chars[i], big) == class {
                    i += 1;
                }
                while i < chars.len() && text_word_class(chars[i], big) == TextWordClass::Space {
                    i += 1;
                }
            }
            i.min(chars.len() - 1)
        }
        TextWordMotion::BackStartSmall | TextWordMotion::BackStartBig => {
            let mut i = col.saturating_sub(1);
            while i > 0 && text_word_class(chars[i], big) == TextWordClass::Space {
                i -= 1;
            }
            let class = text_word_class(chars[i], big);
            while i > 0 && text_word_class(chars[i - 1], big) == class {
                i -= 1;
            }
            i
        }
        TextWordMotion::EndSmall | TextWordMotion::EndBig => {
            let mut i = col;
            if text_word_class(chars[i], big) == TextWordClass::Space {
                while i < chars.len() && text_word_class(chars[i], big) == TextWordClass::Space {
                    i += 1;
                }
                if i >= chars.len() {
                    return chars.len() - 1;
                }
            }
            let class = text_word_class(chars[i], big);
            while i + 1 < chars.len() && text_word_class(chars[i + 1], big) == class {
                i += 1;
            }
            if i > col {
                return i;
            }
            i += 1;
            while i < chars.len() && text_word_class(chars[i], big) == TextWordClass::Space {
                i += 1;
            }
            if i >= chars.len() {
                return chars.len() - 1;
            }
            let class = text_word_class(chars[i], big);
            while i + 1 < chars.len() && text_word_class(chars[i + 1], big) == class {
                i += 1;
            }
            i
        }
    }
}

fn text_word_class(c: char, big: bool) -> TextWordClass {
    if c.is_whitespace() {
        TextWordClass::Space
    } else if big || c.is_alphanumeric() || c == '_' {
        TextWordClass::Keyword
    } else {
        TextWordClass::Punct
    }
}

/// Jump the cursor to the next/prev document separator line (Mongo `[`/`]`).
fn text_doc_jump(app: &mut App, lines: &[String], dir: i32) {
    let seps: Vec<usize> = lines
        .iter()
        .enumerate()
        .filter(|(_, l)| l.starts_with('─'))
        .map(|(i, _)| i)
        .collect();
    if seps.is_empty() {
        return;
    }
    let cur = text_line(app);
    let target = if dir > 0 {
        seps.iter().copied().find(|&i| i > cur)
    } else {
        seps.iter().copied().rev().find(|&i| i < cur)
    };
    if let Some(line) = target {
        text_mut(app, |t| {
            t.line = line;
            t.col = 0;
        });
    }
}

/// Jump cursor to the next/prev (dir=±1) or first (dir=0) line containing the
/// pane search query.
fn text_search_jump(app: &mut App, lines: &[String], dir: i32) {
    let q = app
        .active_session()
        .and_then(|s| s.active_tab())
        .map(|t| t.view.text.search.to_lowercase())
        .unwrap_or_default();
    if q.is_empty() || lines.is_empty() {
        return;
    }
    let matches: Vec<usize> = lines
        .iter()
        .enumerate()
        .filter(|(_, l)| l.to_lowercase().contains(&q))
        .map(|(i, _)| i)
        .collect();
    if matches.is_empty() {
        return;
    }
    let cur = text_line(app);
    let target = if dir == 0 {
        matches.iter().copied().find(|&i| i >= cur).unwrap_or(matches[0])
    } else if dir > 0 {
        matches.iter().copied().find(|&i| i > cur).unwrap_or(matches[0])
    } else {
        matches.iter().rev().copied().find(|&i| i < cur).unwrap_or(matches[matches.len() - 1])
    };
    text_mut(app, |t| {
        t.line = target;
        t.col = 0;
    });
}

/// SQL detail `[`/`]`: switch to the previous/next record and reset the cursor.
fn switch_detail_row(app: &mut App, delta: i32) {
    let (nrows, _) = result_dims(app);
    if let Some(t) = app.active_session_mut().and_then(|s| s.active_tab_mut()) {
        if nrows > 0 {
            t.view.selected_row = (t.view.selected_row as i32 + delta).clamp(0, nrows as i32 - 1) as usize;
            t.view.text.reset();
        }
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
    app.command_completion.clear();
    app.command_completion_index = 0;
    app.previous_focus = app.focus;
    app.focus = Focus::Command;
}

fn command_key(app: &mut App, key: KeyEvent) {
    match key.code {
        // Tab drives command completion: open the popup when hidden, else cycle.
        KeyCode::Tab => {
            if app.command_completion.is_empty() {
                app.open_command_suggestions();
            } else {
                app.command_completion_move(true);
            }
        }
        KeyCode::BackTab => app.command_completion_move(false),
        KeyCode::Char(c) => {
            app.command.push(c);
            app.command_completion.clear();
        }
        KeyCode::Backspace => {
            app.command.pop();
            app.command_completion.clear();
        }
        KeyCode::Esc => {
            // Esc first dismisses the popup, then exits the command line.
            if !app.command_completion.is_empty() {
                app.command_completion.clear();
            } else {
                app.command_active = false;
                app.focus = app.previous_focus;
            }
        }
        KeyCode::Enter => {
            // Accept the highlighted suggestion (stay on the line for args),
            // otherwise run the command.
            if !app.command_completion.is_empty() {
                app.accept_command_suggestion();
            } else {
                let cmd = app.command.clone();
                app.command_active = false;
                app.focus = app.previous_focus;
                run_command(app, &cmd);
            }
        }
        _ => {}
    }
}

/// Id of the connection under the connections-page cursor. Most management
/// commands (open/edit/delete/test) act on the selected profile, mirroring
/// `connections_key`. Returns an owned id so the caller can then borrow `app`
/// mutably.
fn selected_profile_id(app: &App) -> Option<String> {
    app.vault.profiles.get(app.connections_cursor).map(|p| p.id.clone())
}

fn run_command(app: &mut App, cmd: &str) {
    let cmd = cmd.trim();
    let parts: Vec<&str> = cmd.split_whitespace().collect();
    match parts.first().copied() {
        Some("q") | Some("quit") | Some("exit") => app.should_quit = true,
        Some("help") | Some("?") => app.help_open = true,
        Some("timeout") => match parts.get(1) {
            Some(arg) => match arg.parse::<u64>() {
                Ok(n) => {
                    app.tasks.query_timeout = n;
                    app.set_status(StatusKind::Info, format!("query timeout = {n}s"));
                }
                Err(_) => app.set_status(StatusKind::Warning, "timeout: expected seconds"),
            },
            None => app.set_status(StatusKind::Info, format!("query timeout = {}s", app.tasks.query_timeout)),
        },
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
        Some("connections") | Some("profiles") => {
            app.page = Page::Connections;
            app.focus = Focus::Sidebar;
        }
        Some("back") => {
            if app.page == Page::Workspace {
                app.page = Page::Connections;
                app.focus = Focus::Sidebar;
            }
        }
        Some("query") => {
            if app.active_session().is_some() {
                app.page = Page::Workspace;
                app.new_query_tab();
            } else {
                app.set_status(StatusKind::Warning, "no active connection");
            }
        }
        Some("history") => {
            if app.active_session().is_some() {
                app.open_history();
            } else {
                app.set_status(StatusKind::Warning, "no active connection");
            }
        }
        Some("refresh") => {
            let is_data = app
                .active_session()
                .and_then(|s| s.active_tab())
                .map(|t| t.kind == TabKind::Data)
                .unwrap_or(false);
            if is_data {
                app.page_data(0);
            } else if let Some(id) = app.active_session().map(|s| s.id) {
                app.load_databases(id);
            } else {
                app.set_status(StatusKind::Warning, "no active connection");
            }
        }
        Some("next") => app.page_data(1),
        Some("prev") | Some("previous") => app.page_data(-1),
        Some("open") => {
            if app.page == Page::Connections {
                match selected_profile_id(app) {
                    Some(id) => app.open_connection(&id),
                    None => app.set_status(StatusKind::Warning, "no connection selected"),
                }
            } else {
                app.nav_activate();
            }
        }
        Some("edit") => match selected_profile_id(app) {
            Some(id) => app.open_edit_form(&id),
            None => app.set_status(StatusKind::Warning, "no connection selected"),
        },
        Some("delete") => match selected_profile_id(app) {
            Some(id) => {
                app.confirm = Some(Confirm {
                    message: format!("Delete connection '{id}'?"),
                    action: ConfirmAction::DeleteConnection(id),
                });
            }
            None => app.set_status(StatusKind::Warning, "no connection selected"),
        },
        Some("test") => match selected_profile_id(app) {
            Some(id) => app.test_connection(&id),
            None => app.set_status(StatusKind::Warning, "no connection selected"),
        },
        Some("ai") => app.toggle_ai(),
        Some(other) => app.set_status(StatusKind::Warning, format!("unknown command: {other}")),
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
        MouseEventKind::ScrollDown => move_row(app, 1),
        MouseEventKind::ScrollUp => move_row(app, -1),
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sidebar_g_requires_second_g_to_jump_top() {
        let mut pending = false;

        assert_eq!(sidebar_g_action(&mut pending, KeyCode::Char('g'), 5), SidebarGAction::Pending);
        assert!(pending);
        assert_eq!(sidebar_g_action(&mut pending, KeyCode::Char('g'), 5), SidebarGAction::Jump(0));
        assert!(!pending);
    }

    #[test]
    fn sidebar_shift_g_jumps_last_without_pending() {
        let mut pending = false;

        assert_eq!(sidebar_g_action(&mut pending, KeyCode::Char('G'), 5), SidebarGAction::Jump(4));
        assert!(!pending);
    }

    #[test]
    fn sidebar_pending_g_falls_through_on_other_key() {
        let mut pending = true;

        assert_eq!(sidebar_g_action(&mut pending, KeyCode::Char('j'), 5), SidebarGAction::Fallthrough);
        assert!(!pending);
    }

    #[test]
    fn result_focus_q_returns_to_editor_like_tab_and_escape() {
        assert!(result_focus_return_key(KeyCode::Tab));
        assert!(result_focus_return_key(KeyCode::Esc));
        assert!(result_focus_return_key(KeyCode::Char('q')));
        assert!(!result_focus_return_key(KeyCode::Char('j')));
    }

    #[test]
    fn text_word_motions_support_small_and_big_words() {
        assert_eq!(text_word_target("foo bar", 0, TextWordMotion::EndSmall), 2);
        assert_eq!(text_word_target("foo.bar baz", 0, TextWordMotion::ForwardStartSmall), 3);
        assert_eq!(text_word_target("foo.bar baz", 4, TextWordMotion::BackStartSmall), 3);
        assert_eq!(text_word_target("foo.bar baz", 0, TextWordMotion::ForwardStartBig), 8);
        assert_eq!(text_word_target("foo.bar baz", 8, TextWordMotion::BackStartBig), 0);
        assert_eq!(text_word_target("foo.bar baz", 0, TextWordMotion::EndBig), 6);
    }

    #[test]
    fn text_ctrl_reading_motions_use_half_and_full_pages() {
        let ctrl = KeyModifiers::CONTROL;

        assert_eq!(text_ctrl_motion_delta(KeyCode::Char('d'), ctrl), Some(5));
        assert_eq!(text_ctrl_motion_delta(KeyCode::Char('u'), ctrl), Some(-5));
        assert_eq!(text_ctrl_motion_delta(KeyCode::Char('f'), ctrl), Some(10));
        assert_eq!(text_ctrl_motion_delta(KeyCode::Char('b'), ctrl), Some(-10));
        assert_eq!(text_ctrl_motion_delta(KeyCode::Char('d'), KeyModifiers::empty()), None);
    }

    #[test]
    fn text_first_nonblank_col_finds_caret_target() {
        assert_eq!(text_first_nonblank_col("    user_id  42"), 4);
        assert_eq!(text_first_nonblank_col(""), 0);
        assert_eq!(text_first_nonblank_col("   "), 0);
    }
}
