//! Rendering, layout and hitboxes. The layout produces a set of [`Rect`]s that
//! are used both to draw widgets and to resolve mouse clicks, so a click never
//! drifts from what was drawn (this is the structural fix for the Go version's
//! hitbox/render divergence).

use ratatui::layout::{Constraint, Direction, Layout, Rect};
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, Borders, Clear, Paragraph, Wrap};
use ratatui::Frame;
use unicode_width::UnicodeWidthStr;

use crate::result::{cell_value_string, Set};

use super::nav::{self, NavKind};
use super::state::{App, Focus, Page, TabKind, VimMode, WorkspaceTab};
use super::theme;

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum HitId {
    PanelSidebar,
    PanelMain,
    ConnTab(usize),
    ConnAdd,
    NavNode(usize),
    ConnRow(usize),
    WorkspaceTab(usize),
    QueryRun(u64),
    FormField(usize),
    FormOk,
    FormCancel,
    ConfirmOk,
    ConfirmCancel,
    ErrorClose,
    HelpClose,
}

#[derive(Clone)]
pub struct Hitbox {
    pub rect: Rect,
    pub id: HitId,
}

fn point_in(rect: Rect, x: u16, y: u16) -> bool {
    x >= rect.x && x < rect.x + rect.width && y >= rect.y && y < rect.y + rect.height
}

/// Resolve the topmost hitbox containing the point (later-pushed wins, matching
/// draw order where overlays are drawn last).
pub fn hit_test(hits: &[Hitbox], x: u16, y: u16) -> Option<HitId> {
    hits.iter()
        .rev()
        .find(|h| point_in(h.rect, x, y))
        .map(|h| h.id.clone())
}

pub fn render_all(f: &mut Frame, app: &App, hits: &mut Vec<Hitbox>) {
    let area = f.area();
    // Guard against a zero/too-small terminal so widget rendering never indexes
    // outside the buffer.
    if area.width < 4 || area.height < 3 {
        return;
    }
    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(1),
            Constraint::Min(1),
            Constraint::Length(1),
        ])
        .split(area);
    let (header, body, footer) = (chunks[0], chunks[1], chunks[2]);

    render_header(f, app, header, hits);
    match app.page {
        Page::Unlock => render_unlock(f, app, body),
        Page::Connections => render_connections(f, app, body, hits),
        Page::Workspace => render_workspace(f, app, body, hits),
        Page::Help => render_help_page(f, body),
        Page::Game => render_game(f, app, body),
    }
    render_footer(f, app, footer);

    // Overlays draw last so their hitboxes win.
    if app.form.is_some() {
        render_form(f, app, area, hits);
    }
    if let Some(confirm) = &app.confirm {
        render_confirm(f, &confirm.message, area, hits);
    }
    if let Some(err) = &app.error_box {
        render_error_box(f, err, area, hits);
    }
    if app.help_open {
        render_help_overlay(f, area, hits);
    }
    if app.ai.open {
        render_ai_panel(f, app, area);
    }
    if app.history_open {
        render_history(f, app, area);
    }

    // Live drag-selection highlight (drawn over everything).
    if app.selection.active {
        let (a, b) = (app.selection.anchor, app.selection.cursor);
        let x = a.0.min(b.0);
        let y = a.1.min(b.1);
        let w = a.0.abs_diff(b.0) + 1;
        let h = a.1.abs_diff(b.1) + 1;
        let rect = Rect::new(x, y, w, h).intersection(area);
        f.buffer_mut()
            .set_style(rect, Style::default().bg(theme::CURSOR_DIM).fg(theme::INK_BRIGHT));
    }
}

fn render_history(f: &mut Frame, app: &App, area: Rect) {
    let box_rect = centered(area, (area.width as f32 * 0.7) as u16, (area.height as f32 * 0.7) as u16);
    f.render_widget(Clear, box_rect);
    let block = Block::default()
        .borders(Borders::ALL)
        .title(" Query History (Enter fill · Esc close) ")
        .border_style(Style::default().fg(theme::ACCENT))
        .style(Style::default().bg(theme::MODAL_BG));
    let inner = block.inner(box_rect);
    f.render_widget(block, box_rect);
    let h = inner.height as usize;
    let off = app.history_cursor.saturating_sub(h.saturating_sub(1));
    for (i, entry) in app.history_entries.iter().enumerate().skip(off).take(h) {
        let y = inner.y + (i - off) as u16;
        let mark = if entry.status == "ok" { "✓" } else { "✗" };
        let count = if entry.execution_count > 1 {
            format!(" ×{}", entry.execution_count)
        } else {
            String::new()
        };
        let line = format!("{mark} {}{count}", entry.statement.replace('\n', " "));
        let style = if i == app.history_cursor {
            theme::selection_style(true)
        } else {
            Style::default().fg(theme::INK)
        };
        f.render_widget(
            Paragraph::new(Span::styled(truncate(&line, inner.width as usize), style)),
            Rect::new(inner.x, y, inner.width, 1),
        );
    }
}

fn render_ai_panel(f: &mut Frame, app: &App, area: Rect) {
    let box_rect = centered(area, (area.width as f32 * 0.7) as u16, (area.height as f32 * 0.8) as u16);
    f.render_widget(Clear, box_rect);
    let title = format!(" AI · {} (Esc close · Ctrl+Y insert SQL) ", app.ai.provider_name);
    let block = Block::default()
        .borders(Borders::ALL)
        .title(title)
        .border_style(Style::default().fg(theme::VIOLET))
        .style(Style::default().bg(theme::MODAL_BG));
    let inner = block.inner(box_rect);
    f.render_widget(block, box_rect);

    let rows = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Min(1), Constraint::Length(1)])
        .split(inner);
    let (convo, input) = (rows[0], rows[1]);

    let mut lines: Vec<Line> = Vec::new();
    if app.ai.messages.is_empty() {
        lines.push(Line::from(Span::styled(
            "Ask a question. Slash: /new /provider <name> /help",
            theme::muted(),
        )));
    }
    for m in &app.ai.messages {
        let (tag, style) = match m.role.as_str() {
            "you" => ("you", theme::accent()),
            "err" => ("err", theme::danger()),
            _ => ("ai", Style::default().fg(theme::VIOLET)),
        };
        lines.push(Line::from(Span::styled(format!("{tag}:"), style)));
        for l in m.text.lines() {
            lines.push(Line::from(l.to_string()));
        }
        lines.push(Line::from(""));
    }
    if app.ai.pending {
        lines.push(Line::from(Span::styled(
            format!("{} thinking…", app.tasks.spinner_frame()),
            theme::muted(),
        )));
    }
    let total = lines.len();
    let h = convo.height as usize;
    let skip = total.saturating_sub(h);
    f.render_widget(
        Paragraph::new(lines).scroll((skip as u16, 0)).wrap(Wrap { trim: false }),
        convo,
    );

    f.render_widget(
        Paragraph::new(Span::styled(format!("> {}_", app.ai.input), Style::default().fg(theme::INK_BRIGHT))),
        input,
    );

    // @-mention table completion popup, drawn just above the input line.
    let items = &app.ai.mention_items;
    let n = items.len() as u16;
    if n > 0 && input.y > inner.y {
        let rows: Vec<String> = items.iter().map(|s| format!(" @{}  {} ", s.value, s.detail)).collect();
        let w = rows.iter().map(|r| r.width() as u16).max().unwrap_or(20).min(inner.width).max(1);
        let h = n.min(input.y - inner.y).min(8);
        let py = input.y - h;
        f.render_widget(Clear, Rect::new(input.x, py, w, h));
        for (i, row) in rows.iter().take(h as usize).enumerate() {
            let style = if i == app.ai.mention_index {
                theme::selection_style(true)
            } else {
                Style::default().bg(theme::MODAL_BG).fg(theme::INK)
            };
            f.render_widget(
                Paragraph::new(Span::styled(truncate(row, w as usize), style)).style(Style::default().bg(theme::MODAL_BG)),
                Rect::new(input.x, py + i as u16, w, 1),
            );
        }
    }
}

fn render_header(f: &mut Frame, app: &App, area: Rect, hits: &mut Vec<Hitbox>) {
    f.render_widget(Block::default().style(Style::default().bg(theme::HEADER_BG)), area);
    let mut x = area.x;
    let badge = " TDB ";
    f.render_widget(
        Paragraph::new(Span::styled(badge, theme::header())),
        Rect::new(x, area.y, badge.width() as u16, 1),
    );
    x += badge.width() as u16 + 1;

    if !app.sessions.is_empty() {
        let labels: Vec<String> = app
            .sessions
            .iter()
            .map(|s| format!(" {} ", s.profile.name))
            .collect();
        let widths: Vec<u16> = labels.iter().map(|l| l.width() as u16 + 1).collect();
        let active = app.active_session.min(app.sessions.len() - 1);
        // Budget reserves the trailing " + " (3) and a possible leading "‹" (1).
        let budget = (area.x + area.width).saturating_sub(x + 3 + 1);
        // Smallest start index that keeps the active tab visible from the right.
        let mut start = 0usize;
        while start < active {
            let w: u16 = widths[start..=active].iter().sum();
            if w <= budget {
                break;
            }
            start += 1;
        }
        if start > 0 {
            f.render_widget(
                Paragraph::new(Span::styled("‹", Style::default().bg(theme::HEADER_BG).fg(theme::KEY))),
                Rect::new(x, area.y, 1, 1),
            );
            x += 1;
        }
        for (i, label) in labels.iter().enumerate().skip(start) {
            let w = label.width() as u16;
            if x + w >= area.x + area.width - 3 {
                break;
            }
            let is_active = i == active && app.page != Page::Connections;
            let style = if is_active {
                Style::default().bg(theme::CURSOR).fg(theme::INK_INVERSE).add_modifier(Modifier::BOLD)
            } else {
                Style::default().bg(theme::HEADER_BG).fg(theme::INK_BRIGHT)
            };
            let rect = Rect::new(x, area.y, w, 1);
            f.render_widget(Paragraph::new(Span::styled(label.clone(), style)), rect);
            hits.push(Hitbox { rect, id: HitId::ConnTab(i) });
            x += w + 1;
        }
    }

    let add = " + ";
    if x + 3 <= area.x + area.width {
        let rect = Rect::new(x, area.y, 3, 1);
        f.render_widget(
            Paragraph::new(Span::styled(add, Style::default().bg(theme::HEADER_BG).fg(theme::KEY))),
            rect,
        );
        hits.push(Hitbox { rect, id: HitId::ConnAdd });
    }
}

fn render_footer(f: &mut Frame, app: &App, area: Rect) {
    f.render_widget(Block::default().style(theme::footer()), area);

    // Command mode: show the line being typed (with a caret) instead of hints.
    if app.command_active {
        render_command_completion(f, app, area);
        f.render_widget(
            Paragraph::new(Span::styled(
                format!(":{}_", app.command),
                Style::default()
                    .bg(theme::FOOTER_BG)
                    .fg(theme::INK_BRIGHT)
                    .add_modifier(Modifier::BOLD),
            )),
            area,
        );
        return;
    }

    let left = if app.tasks.active {
        format!(" {} {}…", app.tasks.spinner_frame(), app.tasks.label)
    } else {
        format!(" {}", footer_hints(app))
    };
    let kind = match app.message_kind {
        theme::StatusKind::Success => "✓",
        theme::StatusKind::Error => "✗",
        theme::StatusKind::Warning => "!",
        theme::StatusKind::Info => "·",
    };
    let right = if app.message.is_empty() {
        String::new()
    } else {
        format!("{kind} {} ", app.message)
    };
    let right_w = right.width() as u16;
    f.render_widget(
        Paragraph::new(Span::styled(left, theme::footer())),
        Rect::new(area.x, area.y, area.width.saturating_sub(right_w), 1),
    );
    if right_w > 0 && right_w <= area.width {
        f.render_widget(
            Paragraph::new(Span::styled(
                right,
                Style::default().bg(theme::FOOTER_BG).fg(theme::status_color(app.message_kind)),
            )),
            Rect::new(area.x + area.width - right_w, area.y, right_w, 1),
        );
    }
}

/// Command-line (`:`) completion popup, drawn just above the footer line.
fn render_command_completion(f: &mut Frame, app: &App, footer: Rect) {
    let items = &app.command_completion;
    let n = items.len() as u16;
    if n == 0 || n > footer.y {
        return;
    }
    let rows: Vec<String> = items
        .iter()
        .map(|s| format!(" {}  {} ", s.value, s.detail))
        .collect();
    let w = rows.iter().map(|r| r.width() as u16).max().unwrap_or(20).clamp(20, 48);
    let px = footer.x.min(footer.x + footer.width.saturating_sub(w));
    let py = footer.y - n;
    let rect = Rect::new(px, py, w, n).intersection(f.area());
    f.render_widget(Clear, rect);
    for (i, row) in rows.iter().enumerate() {
        let style = if i == app.command_completion_index {
            theme::selection_style(true)
        } else {
            Style::default().bg(theme::MODAL_BG).fg(theme::INK)
        };
        f.render_widget(
            Paragraph::new(Span::styled(truncate(row, w as usize), style)).style(Style::default().bg(theme::MODAL_BG)),
            Rect::new(px, py + i as u16, w, 1),
        );
    }
}

fn footer_hints(app: &App) -> &'static str {
    match app.page {
        Page::Unlock => "enter password · Enter unlock",
        Page::Connections => "n new · e edit · d delete · t test · Enter open · q quit",
        Page::Workspace => match app.focus {
            Focus::Sidebar => "j/k move · Enter open/expand · Ctrl+L main · / search · : cmd",
            Focus::Main => "Ctrl+H sidebar · Ctrl+Enter run · n new tab · : cmd",
            _ => ": command",
        },
        Page::Help => "Esc close",
        Page::Game => "Space jump · Esc quit",
    }
}

fn render_unlock(f: &mut Frame, app: &App, area: Rect) {
    let title = if app.first_run {
        "Set a master password to create your encrypted vault"
    } else {
        "Enter master password to unlock"
    };
    let masked: String = "•".repeat(app.unlock_input.chars().count());
    let mut lines = vec![
        Line::from(""),
        Line::from(Span::styled(title, theme::accent())),
        Line::from(""),
        Line::from(format!("password: {masked}_")),
    ];
    if let Some(err) = &app.unlock_error {
        lines.push(Line::from(""));
        lines.push(Line::from(Span::styled(err.clone(), theme::danger())));
    }
    let block = Block::default()
        .borders(Borders::ALL)
        .title(" TDB ")
        .border_style(Style::default().fg(theme::ACCENT));
    f.render_widget(Paragraph::new(lines).block(block), centered(area, 60, 10));
}

fn render_connections(f: &mut Frame, app: &App, area: Rect, hits: &mut Vec<Hitbox>) {
    let block = Block::default()
        .borders(Borders::ALL)
        .title(" Connections ")
        .border_style(Style::default().fg(theme::ACCENT));
    let inner = block.inner(area);
    f.render_widget(block, area);

    if app.vault.profiles.is_empty() {
        f.render_widget(
            Paragraph::new("No connections yet. Press n to create one.").style(theme::muted()),
            inner,
        );
        return;
    }

    let header = format!(
        "  {:<16} {:<7} {:<22} {:<10} {}",
        "ID", "DRIVER", "HOST", "DATABASE", "ACCESS"
    );
    f.render_widget(
        Paragraph::new(Span::styled(header, theme::muted())),
        Rect::new(inner.x, inner.y, inner.width, 1),
    );
    for (i, p) in app.vault.profiles.iter().enumerate() {
        let y = inner.y + 1 + i as u16;
        if y >= inner.y + inner.height {
            break;
        }
        let access = if p.read_only { "ro" } else { "rw" };
        let host = if p.driver == crate::config::Driver::Mongo && p.port == 0 {
            p.host.clone()
        } else {
            format!("{}:{}", p.host, p.port)
        };
        let text = format!(
            "  {:<16} {:<7} {:<22} {:<10} {}",
            truncate(&p.id, 16),
            p.driver.as_str(),
            truncate(&host, 22),
            truncate(&p.database, 10),
            access
        );
        let selected = i == app.connections_cursor;
        let style = if selected {
            theme::selection_style(true)
        } else {
            Style::default().fg(theme::INK)
        };
        let rect = Rect::new(inner.x, y, inner.width, 1);
        f.render_widget(Paragraph::new(Span::styled(text, style)), rect);
        hits.push(Hitbox { rect, id: HitId::ConnRow(i) });
    }
}

fn render_workspace(f: &mut Frame, app: &App, area: Rect, hits: &mut Vec<Hitbox>) {
    let sidebar_w = (area.width / 5).clamp(20, 48);
    let cols = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Length(sidebar_w), Constraint::Min(10)])
        .split(area);
    let (side, main) = (cols[0], cols[1]);
    hits.push(Hitbox { rect: side, id: HitId::PanelSidebar });
    hits.push(Hitbox { rect: main, id: HitId::PanelMain });

    render_sidebar(f, app, side, hits);
    render_main(f, app, main, hits);
}

fn render_sidebar(f: &mut Frame, app: &App, area: Rect, hits: &mut Vec<Hitbox>) {
    let focused = app.focus == Focus::Sidebar;
    let edge = if focused { theme::SIDEBAR_FOCUS_EDGE } else { theme::SIDEBAR_EDGE };
    let title = match app.active_session() {
        Some(s) if s.nav.search_active || !s.nav.search_query.is_empty() => {
            let caret = if s.nav.search_active { "_" } else { "" };
            format!(" /{}{caret} ", s.nav.search_query)
        }
        _ => " Navigation ".to_string(),
    };
    let block = Block::default()
        .borders(Borders::ALL)
        .title(title)
        .border_style(Style::default().fg(edge))
        .style(Style::default().bg(theme::SIDEBAR_BG));
    let inner = block.inner(area);
    f.render_widget(block, area);

    let Some(session) = app.active_session() else { return };
    let nodes = nav::visible_nodes(&session.nav, &session.conn_label());
    let cursor = session.nav.cursor.min(nodes.len().saturating_sub(1));
    let height = (inner.height as usize).max(1);
    // Cursor-follows-window scroll (mirrors `render_table`): persist the offset
    // and only shift it when the cursor leaves the viewport, so scrolling back up
    // does not keep the highlight stuck at the bottom.
    let offset = viewport_offset(session.nav.v_offset.get(), cursor, height, nodes.len());
    session.nav.v_offset.set(offset);

    for (row, node) in nodes.iter().enumerate().skip(offset).take(height) {
        let y = inner.y + (row - offset) as u16;
        let indent = "  ".repeat(node.depth as usize);
        let marker: String = if node.expandable {
            let twist = if node.expanded { app.icons.expanded() } else { app.icons.collapsed() };
            let kind_icon = match &node.kind {
                NavKind::Catalog { .. } => app.icons.catalog(),
                NavKind::Database { .. } => app.icons.database(),
                _ => "",
            };
            format!("{twist}{kind_icon}")
        } else {
            match &node.kind {
                NavKind::Object { object, .. } => app.icons.object(object.type_).to_string(),
                NavKind::Connection => app.icons.connection().to_string(),
                _ => "  ".to_string(),
            }
        };
        let text = format!("{indent}{marker}{}", node.label);
        let searching = !session.nav.search_query.is_empty();
        let style = if row == cursor {
            theme::selection_style(focused)
        } else if searching && matches!(node.kind, NavKind::Object { .. }) {
            // During search the visible object leaves are the matches — flag them.
            Style::default().fg(theme::WARNING).add_modifier(Modifier::BOLD)
        } else if matches!(node.kind, NavKind::Connection) {
            theme::accent()
        } else {
            Style::default().fg(theme::INK)
        };
        let rect = Rect::new(inner.x, y, inner.width, 1);
        f.render_widget(
            Paragraph::new(Span::styled(truncate(&text, inner.width as usize), style))
                .style(Style::default().bg(theme::SIDEBAR_BG)),
            rect,
        );
        hits.push(Hitbox { rect, id: HitId::NavNode(row) });
    }
}

fn render_main(f: &mut Frame, app: &App, area: Rect, hits: &mut Vec<Hitbox>) {
    let focused = app.focus == Focus::Main;
    let edge = if focused { theme::MAIN_FOCUS_EDGE } else { theme::MAIN_EDGE };

    // Single full-height bordered block (aligns with the sidebar border).
    let block = Block::default()
        .borders(Borders::ALL)
        .border_style(Style::default().fg(edge))
        .style(Style::default().bg(theme::MAIN_BG));
    let inner = block.inner(area);
    f.render_widget(block, area);

    let Some(session) = app.active_session() else { return };

    // Inside the border: a 1-row tab bar, then the tab content.
    let rows = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(1), Constraint::Min(1)])
        .split(inner);
    let (tabbar, content) = (rows[0], rows[1]);

    let mut x = tabbar.x;
    for (i, tab) in session.tabs.iter().enumerate() {
        let label = format!(" {} ", tab.title);
        let w = label.width() as u16;
        if x + w >= tabbar.x + tabbar.width {
            break;
        }
        let active = i == session.active_tab;
        let color = match tab.kind {
            TabKind::Query => theme::TAB_QUERY,
            TabKind::Data => theme::TAB_DATA,
        };
        let style = if active {
            Style::default().bg(theme::CURSOR).fg(theme::INK_INVERSE).add_modifier(Modifier::BOLD)
        } else {
            Style::default().bg(theme::MAIN_BG).fg(color)
        };
        let rect = Rect::new(x, tabbar.y, w, 1);
        f.render_widget(Paragraph::new(Span::styled(label, style)), rect);
        hits.push(Hitbox { rect, id: HitId::WorkspaceTab(i) });
        x += w;
    }

    let Some(tab) = session.active_tab() else {
        f.render_widget(
            Paragraph::new("Press n to open a query tab.")
                .style(Style::default().bg(theme::MAIN_BG).fg(theme::MUTED)),
            content,
        );
        return;
    };

    match tab.kind {
        TabKind::Query => render_query_tab(f, app, tab, content, hits),
        TabKind::Data => render_data_tab(f, tab, content),
    }
}

fn render_query_tab(
    f: &mut Frame,
    app: &App,
    tab: &super::state::WorkspaceTab,
    area: Rect,
    hits: &mut Vec<Hitbox>,
) {
    // Adaptive editor height: grow with the buffer up to half the panel, leaving
    // room for the run bar and result. The editor and result are each framed in a
    // focus-aware bordered box (the box border doubles as the input-box marker).
    let h = area.height as usize;
    let editor_text_h = tab.buffer.len().clamp(3, (h / 2).max(3)) as u16;
    let rows = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(editor_text_h + 2), // bordered editor box
            Constraint::Length(1),                 // run / status bar
            Constraint::Min(1),                    // bordered result box
        ])
        .split(area);
    let (editor_box, bar, result) = (rows[0], rows[1], rows[2]);

    let main_focused = app.focus == Focus::Main;
    let editor_focused = main_focused && !tab.result_focused;

    let (mode_label, mode_color) = match tab.vim_mode {
        super::state::VimMode::Normal => ("NORMAL", theme::MUTED),
        super::state::VimMode::Insert => ("INSERT", theme::SUCCESS),
        super::state::VimMode::Visual => ("VISUAL", theme::CURSOR),
    };
    // Scope indicator: which catalog/database ad-hoc queries run against. This
    // matters on Doris, where an external catalog can be the active scope and an
    // unqualified `FROM t` would otherwise silently target the wrong catalog.
    let (scope_text, external) = app
        .active_session()
        .map(|s| {
            let cat = s.current_catalog.as_str();
            let db = s.current_database.as_str();
            let external = !cat.is_empty() && !cat.eq_ignore_ascii_case("internal");
            let label = if external {
                format!("· {cat}.{db} ")
            } else if !db.is_empty() {
                format!("· {db} ")
            } else {
                String::new()
            };
            (label, external)
        })
        .unwrap_or_default();
    let scope_color = if external { theme::WARNING } else { theme::MUTED };
    let title = Line::from(vec![
        Span::styled(
            format!(" SQL · {mode_label} "),
            Style::default().fg(mode_color).add_modifier(Modifier::BOLD),
        ),
        Span::styled(scope_text, Style::default().fg(scope_color).add_modifier(Modifier::BOLD)),
    ]);
    let editor_edge = if editor_focused { theme::MAIN_FOCUS_EDGE } else { theme::MAIN_EDGE };
    let editor_block = Block::default()
        .borders(Borders::ALL)
        .border_style(Style::default().fg(editor_edge))
        .title(title)
        .style(Style::default().bg(theme::MAIN_BG));
    let editor = editor_block.inner(editor_box);
    f.render_widget(editor_block, editor_box);

    // Editor buffer: syntax-highlighted (including the caret line, so single-line
    // queries are colored too), with a block caret on the focused line.
    let focused = main_focused;
    let driver = app.active_session().map(|s| s.profile.driver);
    let mut lines = Vec::new();
    for (r, line) in tab.buffer.iter().enumerate() {
        let classes = super::highlight::highlight_classes(driver, line);
        if tab.vim_mode == VimMode::Visual {
            let base = editor_base_styles(line, &classes);
            let cursor = if focused && r == tab.cursor_row {
                Some(tab.cursor_col.min(line.chars().count().saturating_sub(1)))
            } else {
                None
            };
            lines.push(overlay_line(line, &base, editor_visual_span_on_line(tab, r), cursor, 0));
        } else if focused && r == tab.cursor_row {
            let col = tab.cursor_col.min(line.chars().count());
            let before: String = line.chars().take(col).collect();
            let at: String = line.chars().skip(col).take(1).collect();
            let after: String = line.chars().skip(col + 1).collect();
            let before_b = before.len();
            let after_b = (before_b + at.len()).min(classes.len());
            let mut spans = super::highlight::spans_for_line(&before, &classes[..before_b.min(classes.len())]);
            spans.push(Span::styled(
                if at.is_empty() { " ".into() } else { at },
                Style::default().bg(theme::CURSOR).fg(theme::INK_INVERSE),
            ));
            spans.extend(super::highlight::spans_for_line(&after, &classes[after_b..]));
            lines.push(Line::from(spans));
        } else {
            lines.push(Line::from(super::highlight::spans_for_line(line, &classes)));
        }
    }
    let scroll_off = tab
        .cursor_row
        .saturating_sub(editor.height.saturating_sub(1) as usize) as u16;
    f.render_widget(
        Paragraph::new(lines)
            .scroll((scroll_off, 0))
            .style(Style::default().bg(theme::MAIN_BG))
            .wrap(Wrap { trim: false }),
        editor,
    );

    f.render_widget(Paragraph::new("").style(Style::default().bg(theme::MAIN_BG)), bar);
    let run = " ▸ Run (Ctrl+Enter / Esc ⏎) ";
    let run_rect = Rect::new(bar.x, bar.y, (run.width() as u16).min(bar.width), 1);
    f.render_widget(
        Paragraph::new(Span::styled(run, theme::accent())).style(Style::default().bg(theme::MAIN_BG)),
        run_rect,
    );
    hits.push(Hitbox { rect: run_rect, id: HitId::QueryRun(tab.id) });

    // Non-blocking validation warning (contract: advisory only).
    if let Some(d) = driver {
        let text = tab.buffer.join("\n");
        if let Some(issue) = crate::validate::validate(d, &text).into_iter().next() {
            let msg = format!(" ⚠ {} ", issue.message);
            let x = run_rect.x + run_rect.width + 1;
            if x < bar.x + bar.width {
                f.render_widget(
                    Paragraph::new(Span::styled(msg, validation_issue_style(&issue)))
                        .style(Style::default().bg(theme::MAIN_BG)),
                    Rect::new(x, bar.y, bar.x + bar.width - x, 1),
                );
            }
        }
    }

    // Result framed in its own focus-aware box (Tab moves focus here to scroll).
    let result_edge = if main_focused && tab.result_focused {
        theme::MAIN_FOCUS_EDGE
    } else {
        theme::MAIN_EDGE
    };
    let result_title = if tab.result_search_active {
        format!(" Result   /{}_{} ", tab.result_search, result_search_stat(tab))
    } else if !tab.result_search.is_empty() {
        format!(" Result   /{} (n/N){} ", tab.result_search, result_search_stat(tab))
    } else {
        " Result ".to_string()
    };
    let result_block = Block::default()
        .borders(Borders::ALL)
        .border_style(Style::default().fg(result_edge))
        .title(result_title)
        .style(Style::default().bg(theme::MAIN_BG));
    let result_inner = result_block.inner(result);
    f.render_widget(result_block, result);
    render_result(f, tab, result_inner);

    // Completion popup, anchored just below the cursor.
    if let Some(comp) = &tab.completion {
        let n = comp.items.len().min(8) as u16;
        if n > 0 {
            let w = comp
                .items
                .iter()
                .take(8)
                .map(|s| s.width() as u16)
                .max()
                .unwrap_or(10)
                .clamp(10, 40)
                + 2;
            // Cursor screen position (display column of the prefix on its line).
            let cur_line = tab.buffer.get(tab.cursor_row).map(String::as_str).unwrap_or("");
            let prefix: String = cur_line.chars().take(tab.cursor_col).collect();
            let cur_x = editor.x + prefix.width() as u16;
            let cur_y = editor.y + (tab.cursor_row as u16).saturating_sub(scroll_off);
            let px = cur_x.min(area.x + area.width.saturating_sub(w));
            // Prefer below the cursor; flip above if it would overflow downward.
            let py = if cur_y + 1 + n <= area.y + area.height {
                cur_y + 1
            } else {
                cur_y.saturating_sub(n)
            };
            let rect = Rect::new(px, py, w, n).intersection(area);
            f.render_widget(Clear, rect);
            for (i, item) in comp.items.iter().take(n as usize).enumerate() {
                let style = if i == comp.index {
                    theme::selection_style(true)
                } else {
                    Style::default().bg(theme::MODAL_BG).fg(theme::INK)
                };
                f.render_widget(
                    Paragraph::new(Span::styled(format!(" {item} "), style)),
                    Rect::new(px, py + i as u16, w, 1),
                );
            }
        }
    }
}

/// Search-state suffix for result title bars: "  3/12", "  no match", or "".
fn result_search_stat(tab: &super::state::WorkspaceTab) -> String {
    if tab.result_search.is_empty() {
        String::new()
    } else if tab.result_search_total == 0 {
        "  no match".to_string()
    } else {
        format!("  {}/{}", tab.result_search_index, tab.result_search_total)
    }
}

fn render_data_tab(f: &mut Frame, tab: &super::state::WorkspaceTab, area: Rect) {
    let rows = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(1), Constraint::Min(1)])
        .split(area);
    let title = tab
        .target
        .as_ref()
        .map(|t| t.to_path())
        .unwrap_or_default();
    let mode = if tab.metadata_mode { "  [metadata · m to toggle]" } else { "  [m: metadata]" };
    let bar = if tab.result_search_active {
        format!(" {title}   /{}_{}", tab.result_search, result_search_stat(tab))
    } else if !tab.result_search.is_empty() {
        format!(" {title}   /{} (n/N){}", tab.result_search, result_search_stat(tab))
    } else {
        format!(" {title}{mode}")
    };
    f.render_widget(
        Paragraph::new(Span::styled(bar, theme::accent())).style(Style::default().bg(theme::MAIN_BG)),
        rows[0],
    );
    if tab.metadata_mode {
        render_metadata(f, tab, rows[1]);
    } else {
        render_result(f, tab, rows[1]);
    }
}

fn validation_issue_style(issue: &crate::validate::Issue) -> Style {
    match issue.severity {
        crate::validate::Severity::Error => Style::default().fg(theme::DANGER),
        crate::validate::Severity::Warning => Style::default().fg(theme::WARNING),
    }
}

fn editor_visual_span_on_line(tab: &WorkspaceTab, line_idx: usize) -> Option<(usize, usize)> {
    if tab.vim_mode != VimMode::Visual {
        return None;
    }
    let len = tab.buffer.get(line_idx)?.chars().count();
    let anchor = tab.visual_anchor;
    let cursor = (tab.cursor_row, tab.cursor_col);
    let (start, end) = if anchor <= cursor { (anchor, cursor) } else { (cursor, anchor) };
    if line_idx < start.0 || line_idx > end.0 {
        return None;
    }
    let s = if line_idx == start.0 { start.1.min(len) } else { 0 };
    let e = if line_idx == end.0 { (end.1 + 1).min(len) } else { len };
    Some((s, e.max(s)))
}

fn editor_base_styles(line: &str, classes: &[super::highlight::SynClass]) -> Vec<Style> {
    line.char_indices()
        .map(|(b, _)| editor_style_for_class(classes.get(b).copied().unwrap_or(super::highlight::SynClass::Plain)))
        .collect()
}

fn editor_style_for_class(c: super::highlight::SynClass) -> Style {
    use super::highlight::SynClass;
    match c {
        SynClass::Kw => Style::default().fg(theme::ACCENT).add_modifier(Modifier::BOLD),
        SynClass::Fn => Style::default().fg(theme::VIOLET),
        SynClass::Str => Style::default().fg(theme::SUCCESS),
        SynClass::Num => Style::default().fg(theme::WARNING),
        SynClass::Com => Style::default().fg(theme::MUTED).add_modifier(Modifier::ITALIC),
        SynClass::Var => Style::default().fg(theme::KEY),
        SynClass::Pun => Style::default().fg(theme::MUTED),
        SynClass::Key => Style::default().fg(theme::INFO),
        SynClass::Bool => Style::default().fg(theme::VIOLET),
        SynClass::Plain => Style::default().fg(theme::INK),
    }
}

/// Render the table metadata view: columns (name/type/null/default/comment),
/// indexes, and extra attributes (e.g. partition/key for Doris).
fn render_metadata(f: &mut Frame, tab: &super::state::WorkspaceTab, area: Rect) {
    let bg = Style::default().bg(theme::MAIN_BG);
    let Some(meta) = &tab.metadata else {
        f.render_widget(Paragraph::new("Loading metadata…").style(bg.fg(theme::MUTED)), area);
        return;
    };
    let mut lines: Vec<Line> = Vec::new();
    lines.push(Line::from(Span::styled("COLUMNS", theme::accent())));
    lines.push(Line::from(Span::styled(
        format!("  {:<22} {:<18} {:<8} {:<10} {}", "name", "type", "null", "default", "comment"),
        theme::muted(),
    )));
    for fcol in &meta.fields {
        lines.push(Line::from(format!(
            "  {:<22} {:<18} {:<8} {:<10} {}",
            truncate(&fcol.name, 22),
            truncate(&fcol.type_, 18),
            if fcol.nullable { "YES" } else { "NO" },
            truncate(&fcol.default, 10),
            fcol.comment,
        )));
    }
    if !meta.indexes.is_empty() {
        lines.push(Line::from(""));
        lines.push(Line::from(Span::styled("INDEXES", theme::accent())));
        for idx in &meta.indexes {
            let uniq = if idx.unique { "UNIQUE " } else { "" };
            lines.push(Line::from(format!("  {uniq}{} ({})", idx.name, idx.columns.join(", "))));
        }
    }
    if !meta.attributes.is_empty() {
        lines.push(Line::from(""));
        lines.push(Line::from(Span::styled("ATTRIBUTES", theme::accent())));
        for (k, v) in &meta.attributes {
            lines.push(Line::from(format!("  {k}: {v}")));
        }
    }
    f.render_widget(Paragraph::new(lines).style(bg).wrap(Wrap { trim: false }), area);
}

/// Render a result set as a windowed table / document list, honoring row and
/// column offsets so large results scroll without re-rendering everything.
fn render_result(f: &mut Frame, tab: &super::state::WorkspaceTab, area: Rect) {
    if let Some(err) = &tab.error {
        f.render_widget(
            Paragraph::new(err.clone())
                .style(theme::danger())
                .wrap(Wrap { trim: false })
                .block(Block::default().borders(Borders::ALL).border_style(Style::default().fg(theme::DANGER)).title(" error ")),
            area,
        );
        return;
    }
    let Some(set) = &tab.result else {
        f.render_widget(
            Paragraph::new("No result. Run a query or open a table.")
                .style(Style::default().bg(theme::MAIN_BG).fg(theme::MUTED)),
            area,
        );
        return;
    };

    if let Some(table) = &set.table {
        if tab.view.detail {
            let lines = pane_lines(tab);
            let row = tab.view.selected_row.min(table.rows.len().saturating_sub(1));
            let header = format!(
                "row {}/{}  · Esc/q back · [ ] record · v/V select · y copy · / search",
                row + 1,
                table.rows.len()
            );
            render_text_pane(f, &lines, BaseStyle::Field { name_w: detail_name_w(table) }, &tab.view.text, area, &header);
        } else {
            render_table(f, set, table, &tab.view, area);
        }
    } else if set.document_result || !set.documents.is_empty() {
        if set.documents.is_empty() {
            f.render_widget(
                Paragraph::new("0 documents")
                    .style(Style::default().bg(theme::MAIN_BG).fg(theme::MUTED)),
                area,
            );
        } else {
            let lines = pane_lines(tab);
            let header = format!(
                "{} documents{}  · v/V select · y copy · / search · [ ] doc · space/p page",
                set.documents.len(),
                if set.has_more { " +more" } else { "" }
            );
            render_text_pane(f, &lines, BaseStyle::Json, &tab.view.text, area, &header);
        }
    } else if set.value.is_object() || set.value.is_array() {
        // Single JSON value: pretty-print and syntax-highlight, matching Go's
        // result view. Scalars fall through to the plain-text branch below.
        let json = serde_json::to_string_pretty(&set.value).unwrap_or_default();
        f.render_widget(
            Paragraph::new(json_lines(&json))
                .style(Style::default().bg(theme::MAIN_BG))
                .wrap(Wrap { trim: false }),
            area,
        );
    } else {
        let text = cell_value_string(&set.value);
        f.render_widget(
            Paragraph::new(text)
                .style(Style::default().bg(theme::MAIN_BG).fg(theme::INK))
                .wrap(Wrap { trim: false }),
            area,
        );
    }
}

/// Tokenize pretty-printed JSON into syntax-highlighted lines, reused by the
/// document view and the single-value result view.
fn json_lines(json: &str) -> Vec<Line<'static>> {
    json.lines()
        .map(|l| {
            let classes = super::highlight::highlight_json_classes(l);
            Line::from(super::highlight::spans_for_line(l, &classes))
        })
        .collect()
}

/// Cursor-follows-window viewport scroll. Given the previously persisted top
/// `prev` offset, keep it unless `cursor` has scrolled outside the
/// `[off, off+height)` window, in which case move the window the minimum amount
/// to bring the cursor back into view. Clamps so the last page isn't
/// over-scrolled. This is what stops the selection from sticking to the bottom
/// row when scrolling back up after reaching the end.
fn viewport_offset(prev: usize, cursor: usize, height: usize, total: usize) -> usize {
    let height = height.max(1);
    let max_off = total.saturating_sub(height);
    let mut off = prev.min(max_off);
    if cursor < off {
        off = cursor;
    } else if cursor >= off + height {
        off = cursor + 1 - height;
    }
    off.min(max_off)
}

fn render_table(
    f: &mut Frame,
    set: &Set,
    table: &crate::result::Table,
    view: &super::state::ResultViewState,
    area: Rect,
) {
    let ncols = table.columns.len();
    if ncols == 0 {
        f.render_widget(Paragraph::new("(0 columns)").style(theme::muted()), area);
        return;
    }
    let body_h = (area.height.saturating_sub(2) as usize).max(1); // header + status lines
    // Cursor-follows-window scroll: keep the persisted offset and only move it
    // when the selected row leaves the viewport, so scrolling back up doesn't
    // pin the cursor to the bottom.
    let nrows = table.rows.len();
    let sel_row = view.selected_row.min(nrows.saturating_sub(1));
    let row_off = viewport_offset(view.row_offset.get(), sel_row, body_h, nrows);
    view.row_offset.set(row_off);

    // Per-column widths over all columns (cheap; needed for horizontal scroll).
    let mut widths: Vec<usize> = table.columns.iter().map(|col| col.name.width().min(32)).collect();
    for r in row_off..(row_off + body_h).min(nrows) {
        for (c, w) in widths.iter_mut().enumerate() {
            *w = (*w).max(table.cell_string(r, c).width().min(32));
        }
    }

    // Horizontal scroll: leftmost visible column, driven by h/l (col_offset).
    let col_off = view.col_offset.min(ncols.saturating_sub(1));
    let visible_cols: Vec<usize> = (col_off..ncols).collect();

    // Selected row range (visual selection, or the single cursor row).
    let (r0, r1) = view.selection_rows();

    // Header.
    let mut header = String::new();
    for &c in &visible_cols {
        header.push_str(&pad(&table.columns[c].name, widths[c] + 1));
    }
    f.render_widget(
        Paragraph::new(Span::styled(truncate(&header, area.width as usize), theme::muted()))
            .style(Style::default().bg(theme::MAIN_BG)),
        Rect::new(area.x, area.y, area.width, 1),
    );

    // Body: whole-row selection — the cursor row is highlighted strongly, other
    // rows in the visual range dimly.
    for (i, r) in (row_off..(row_off + body_h).min(nrows)).enumerate() {
        let mut line = String::new();
        for &c in &visible_cols {
            line.push_str(&pad(&table.cell_string(r, c), widths[c] + 1));
        }
        let style = if r == sel_row {
            theme::selection_style(true)
        } else if r >= r0 && r <= r1 {
            theme::selection_style(false)
        } else {
            Style::default().fg(theme::INK)
        };
        f.render_widget(
            Paragraph::new(Span::styled(truncate(&line, area.width as usize), style))
                .style(Style::default().bg(theme::MAIN_BG)),
            Rect::new(area.x, area.y + 1 + i as u16, area.width, 1),
        );
    }

    let last_col = visible_cols.last().map(|&c| c + 1).unwrap_or(ncols);
    let status = format!(
        "r{}/{}{}  · cols {}-{}/{}  · Enter detail · v select · y copy",
        if nrows == 0 { 0 } else { sel_row + 1 },
        nrows,
        if set.has_more { " +more" } else if set.truncated { " (capped)" } else { "" },
        col_off + 1,
        last_col,
        ncols,
    );
    if area.height >= 1 {
        f.render_widget(
            Paragraph::new(Span::styled(truncate(&status, area.width as usize), theme::muted()))
                .style(Style::default().bg(theme::MAIN_BG)),
            Rect::new(area.x, area.y + area.height - 1, area.width, 1),
        );
    }
}

/// Max column-name width used to align the SQL row-detail `name  value` lines.
/// Shared by `pane_lines` (content) and `render_result` (base styling) so the
/// accented name column matches exactly.
fn detail_name_w(table: &crate::result::Table) -> usize {
    table.columns.iter().map(|c| c.name.chars().count()).max().unwrap_or(4).clamp(4, 40)
}

/// Content lines for the read-only vim text pane: SQL row detail (`name  value`
/// for the selected row) or Mongo documents (all loaded docs' pretty JSON with
/// `─── i/total ───` separators). Rebuilt on demand; shared by render and input.
pub(super) fn pane_lines(tab: &super::state::WorkspaceTab) -> Vec<String> {
    let Some(set) = &tab.result else { return Vec::new() };
    if let Some(table) = &set.table {
        if !tab.view.detail || table.rows.is_empty() {
            return Vec::new();
        }
        let row = tab.view.selected_row.min(table.rows.len() - 1);
        let w = detail_name_w(table);
        return table
            .columns
            .iter()
            .enumerate()
            .map(|(c, col)| format!("{:>w$}  {}", col.name, table.cell_string(row, c), w = w))
            .collect();
    }
    if !set.documents.is_empty() {
        let total = set.documents.len();
        let mut lines = Vec::new();
        for (i, d) in set.documents.iter().enumerate() {
            lines.push(format!("─── {}/{} ───", i + 1, total));
            let json = serde_json::to_string_pretty(&d.data).unwrap_or_default();
            lines.extend(json.lines().map(str::to_string));
        }
        return lines;
    }
    Vec::new()
}

/// How to color a text-pane line before the cursor/selection overlay.
#[derive(Clone, Copy)]
enum BaseStyle {
    Json,
    Field { name_w: usize },
}

/// Read-only vim text pane: renders `lines` with a visible char cursor and
/// char/line-wise visual selection overlaid on the syntax/field colors. Vertical
/// and horizontal scroll follow the cursor.
fn render_text_pane(
    f: &mut Frame,
    lines: &[String],
    base_kind: BaseStyle,
    text: &super::state::TextCursor,
    area: Rect,
    header: &str,
) {
    let bg = Style::default().bg(theme::MAIN_BG);
    f.render_widget(
        Paragraph::new(Span::styled(truncate(header, area.width as usize), theme::muted())).style(bg),
        Rect::new(area.x, area.y, area.width, 1),
    );
    let body_h = area.height.saturating_sub(1) as usize;
    if body_h == 0 || lines.is_empty() {
        return;
    }
    let nlines = lines.len();
    let cline = text.line.min(nlines - 1);
    let cur_len = lines[cline].chars().count();
    let ccol = text.col.min(cur_len.saturating_sub(1));
    let v_off = viewport_offset(text.v_offset.get(), cline, body_h, nlines);
    text.v_offset.set(v_off);
    // Horizontal scroll follows the cursor column (char-based window).
    let width = area.width as usize;
    let mut h_off = text.h_offset.get();
    if ccol < h_off {
        h_off = ccol;
    } else if ccol >= h_off + width {
        h_off = ccol + 1 - width;
    }
    text.h_offset.set(h_off);

    for (i, li) in (v_off..(v_off + body_h).min(nlines)).enumerate() {
        let line = &lines[li];
        let len = line.chars().count();
        let base: Vec<Style> = match base_kind {
            BaseStyle::Json => {
                if line.starts_with('─') {
                    vec![theme::muted(); len]
                } else {
                    super::highlight::json_base_styles(line)
                }
            }
            BaseStyle::Field { name_w } => (0..len)
                .map(|c| if c < name_w { theme::accent() } else { Style::default().fg(theme::INK) })
                .collect(),
        };
        let sel = text.sel_span_on(li, len);
        let cursor = if li == cline { Some(ccol) } else { None };
        f.render_widget(
            Paragraph::new(overlay_line(line, &base, sel, cursor, h_off)).style(bg),
            Rect::new(area.x, area.y + 1 + i as u16, area.width, 1),
        );
    }
}

/// Build one styled `Line` from `line`, starting at char `h_off`, layering a
/// selection background over `sel` chars and an inverted block cursor at
/// `cursor`, on top of the per-char `base` foreground styles.
fn overlay_line(
    line: &str,
    base: &[Style],
    sel: Option<(usize, usize)>,
    cursor: Option<usize>,
    h_off: usize,
) -> Line<'static> {
    let cursor_style = Style::default().bg(theme::CURSOR).fg(theme::INK_INVERSE);
    let chars: Vec<char> = line.chars().collect();
    if chars.is_empty() {
        return if cursor == Some(0) {
            Line::from(Span::styled(" ".to_string(), cursor_style))
        } else {
            Line::from(String::new())
        };
    }
    let mut spans: Vec<Span> = Vec::new();
    let mut buf = String::new();
    let mut cur: Option<Style> = None;
    for (i, ch) in chars.iter().enumerate().skip(h_off) {
        let mut st = base.get(i).copied().unwrap_or_else(|| Style::default().fg(theme::INK));
        if let Some((s, e)) = sel {
            if i >= s && i < e {
                st = st.bg(theme::CURSOR_DIM);
            }
        }
        if cursor == Some(i) {
            st = cursor_style;
        }
        if cur != Some(st) {
            if let Some(ps) = cur.take() {
                spans.push(Span::styled(std::mem::take(&mut buf), ps));
            }
            cur = Some(st);
        }
        buf.push(*ch);
    }
    if let Some(ps) = cur {
        spans.push(Span::styled(buf, ps));
    }
    Line::from(spans)
}

fn render_form(f: &mut Frame, app: &App, area: Rect, hits: &mut Vec<Hitbox>) {
    let Some(form) = &app.form else { return };
    let box_rect = centered(area, 60, 18);
    f.render_widget(Clear, box_rect);
    let title = if form.editing_id.is_some() { " Edit Connection " } else { " New Connection " };
    let block = Block::default().borders(Borders::ALL).title(title).border_style(Style::default().fg(theme::ACCENT)).style(Style::default().bg(theme::MODAL_BG));
    let inner = block.inner(box_rect);
    f.render_widget(block, box_rect);

    let mut y = inner.y;
    if form.selecting_driver {
        f.render_widget(Paragraph::new(Span::styled("Driver (←/→ to choose, Enter to confirm):", theme::muted())), Rect::new(inner.x, y, inner.width, 1));
        y += 1;
        let drivers = ["mysql", "doris", "mongo", "redis"];
        let mut x = inner.x;
        for (i, d) in drivers.iter().enumerate() {
            let label = format!(" {d} ");
            let style = if i == form.driver_index { theme::selection_style(true) } else { Style::default().fg(theme::INK) };
            f.render_widget(Paragraph::new(Span::styled(label.clone(), style)), Rect::new(x, y, label.width() as u16, 1));
            x += label.width() as u16 + 1;
        }
        return;
    }

    for (i, field) in form.fields.iter().enumerate() {
        if y >= inner.y + inner.height - 2 {
            break;
        }
        let shown = if field.secret {
            "•".repeat(field.value.chars().count())
        } else {
            field.value.clone()
        };
        let active = i == form.active_field;
        let caret = if active { "_" } else { "" };
        let label_style = if active { theme::accent() } else { theme::muted() };
        let line = format!("{:<10} {}{}", field.label, shown, caret);
        let rect = Rect::new(inner.x, y, inner.width, 1);
        f.render_widget(Paragraph::new(Span::styled(line, label_style)), rect);
        hits.push(Hitbox { rect, id: HitId::FormField(i) });
        y += 1;
    }

    let ok = "[ Save ]";
    let cancel = "[ Cancel ]";
    let oy = inner.y + inner.height - 1;
    let ok_rect = Rect::new(inner.x, oy, ok.width() as u16, 1);
    let cancel_rect = Rect::new(inner.x + ok.width() as u16 + 2, oy, cancel.width() as u16, 1);
    f.render_widget(Paragraph::new(Span::styled(ok, theme::accent())), ok_rect);
    f.render_widget(Paragraph::new(Span::styled(cancel, theme::muted())), cancel_rect);
    hits.push(Hitbox { rect: ok_rect, id: HitId::FormOk });
    hits.push(Hitbox { rect: cancel_rect, id: HitId::FormCancel });
}

fn render_confirm(f: &mut Frame, message: &str, area: Rect, hits: &mut Vec<Hitbox>) {
    let box_rect = centered(area, 50, 7);
    f.render_widget(Clear, box_rect);
    let block = Block::default().borders(Borders::ALL).title(" Confirm ").border_style(Style::default().fg(theme::WARNING)).style(Style::default().bg(theme::MODAL_BG));
    let inner = block.inner(box_rect);
    f.render_widget(block, box_rect);
    f.render_widget(Paragraph::new(message.to_string()).wrap(Wrap { trim: true }), Rect::new(inner.x, inner.y, inner.width, inner.height.saturating_sub(1)));
    let oy = inner.y + inner.height - 1;
    let ok = "[ Confirm ]";
    let cancel = "[ Cancel ]";
    let ok_rect = Rect::new(inner.x, oy, ok.width() as u16, 1);
    let cancel_rect = Rect::new(inner.x + ok.width() as u16 + 2, oy, cancel.width() as u16, 1);
    f.render_widget(Paragraph::new(Span::styled(ok, theme::danger())), ok_rect);
    f.render_widget(Paragraph::new(Span::styled(cancel, theme::muted())), cancel_rect);
    hits.push(Hitbox { rect: ok_rect, id: HitId::ConfirmOk });
    hits.push(Hitbox { rect: cancel_rect, id: HitId::ConfirmCancel });
}

fn render_error_box(f: &mut Frame, msg: &str, area: Rect, hits: &mut Vec<Hitbox>) {
    let box_rect = centered(area, (area.width as f32 * 0.7) as u16, 10);
    f.render_widget(Clear, box_rect);
    let block = Block::default().borders(Borders::ALL).title(" error (Esc to close) ").border_style(Style::default().fg(theme::DANGER)).style(Style::default().bg(theme::MODAL_BG));
    let inner = block.inner(box_rect);
    f.render_widget(block.clone(), box_rect);
    f.render_widget(Paragraph::new(msg.to_string()).style(theme::danger()).wrap(Wrap { trim: false }), inner);
    hits.push(Hitbox { rect: box_rect, id: HitId::ErrorClose });
}

fn render_help_overlay(f: &mut Frame, area: Rect, hits: &mut Vec<Hitbox>) {
    let box_rect = centered(area, 64, 18);
    f.render_widget(Clear, box_rect);
    let block = Block::default().borders(Borders::ALL).title(" Help (Esc to close) ").border_style(Style::default().fg(theme::ACCENT)).style(Style::default().bg(theme::MODAL_BG));
    let inner = block.inner(box_rect);
    f.render_widget(block, box_rect);
    f.render_widget(Paragraph::new(help_text()).wrap(Wrap { trim: false }), inner);
    hits.push(Hitbox { rect: box_rect, id: HitId::HelpClose });
}

fn render_help_page(f: &mut Frame, area: Rect) {
    let block = Block::default().borders(Borders::ALL).title(" Help ");
    let inner = block.inner(area);
    f.render_widget(block, area);
    f.render_widget(Paragraph::new(help_text()).wrap(Wrap { trim: false }), inner);
}

fn render_game(f: &mut Frame, app: &App, area: Rect) {
    let Some(game) = &app.game else { return };
    let title = if game.game_over {
        format!(" Game Over — score {} · Space restart · Esc quit ", game.score)
    } else if !game.started {
        " Dino — Space to start · Esc quit ".to_string()
    } else {
        format!(" Dino — score {} ", game.score)
    };
    let block = Block::default().borders(Borders::ALL).title(title).border_style(Style::default().fg(theme::ACCENT));
    let inner = block.inner(area);
    f.render_widget(block, area);
    if inner.height < 3 {
        return;
    }

    let ground_y = inner.y + inner.height - 2;
    // Ground line.
    f.render_widget(
        Paragraph::new(Span::styled("─".repeat(inner.width as usize), theme::muted())),
        Rect::new(inner.x, ground_y + 1, inner.width, 1),
    );
    // Dino.
    let dino_row = ground_y.saturating_sub(game.dino_height.round() as u16);
    let dino_x = inner.x + game.dino_col();
    if dino_x < inner.x + inner.width {
        f.render_widget(
            Paragraph::new(Span::styled("🦖", Style::default().fg(theme::SUCCESS))),
            Rect::new(dino_x, dino_row, 2, 1),
        );
    }
    // Obstacles (cacti) on the ground.
    for &ox in &game.obstacles {
        let x = inner.x + ox;
        if x < inner.x + inner.width {
            f.render_widget(
                Paragraph::new(Span::styled("♠", Style::default().fg(theme::WARNING))),
                Rect::new(x, ground_y, 1, 1),
            );
        }
    }
}

fn help_text() -> String {
    [
        "Navigation:",
        "  Ctrl+H / Ctrl+L   switch panel focus",
        "  j/k or ↑/↓        move selection",
        "  Enter             open / expand",
        "  gg / G            jump first / last",
        "  /  n  N           search next/prev",
        "",
        "Connections:",
        "  n e d t           new / edit / delete / test",
        "  Alt+←/→ Alt+1..9  switch connection",
        "",
        "Query:",
        "  Ctrl+Enter        run statement",
        "  :export csv|json  export result",
        "",
        "Global:  :  command   ?  help   :q  quit",
    ]
    .join("\n")
}

// ---- text helpers (display-width aware, contract §1) ----

pub fn centered(area: Rect, w: u16, h: u16) -> Rect {
    let w = w.min(area.width);
    let h = h.min(area.height);
    let x = area.x + (area.width.saturating_sub(w)) / 2;
    let y = area.y + (area.height.saturating_sub(h)) / 2;
    Rect::new(x, y, w, h)
}

fn pad(s: &str, width: usize) -> String {
    let s = truncate(s, width);
    let w = s.width();
    if w < width {
        format!("{s}{}", " ".repeat(width - w))
    } else {
        s
    }
}

/// Truncate to a display-column budget without splitting a wide character.
fn truncate(s: &str, max_cols: usize) -> String {
    if s.width() <= max_cols {
        return s.to_string();
    }
    let mut out = String::new();
    let mut w = 0usize;
    for ch in s.chars() {
        let cw = unicode_width::UnicodeWidthChar::width(ch).unwrap_or(0);
        if w + cw > max_cols {
            break;
        }
        out.push(ch);
        w += cw;
    }
    out
}

#[cfg(test)]
mod tests {
    use super::{editor_visual_span_on_line, overlay_line, validation_issue_style, viewport_offset};
    use crate::app::state::{VimMode, WorkspaceTab};
    use crate::app::theme;
    use crate::result::Set;
    use crate::validate::{Issue, Severity};
    use ratatui::backend::TestBackend;
    use ratatui::style::Style;
    use ratatui::Terminal;

    #[test]
    fn overlay_line_preserves_text_and_marks_cursor() {
        let line = "hello 世界";
        let base = vec![Style::default(); line.chars().count()];
        // Cursor on char 2, a 3-char selection starting at 1.
        let l = overlay_line(line, &base, Some((1, 4)), Some(2), 0);
        let rebuilt: String = l.spans.iter().map(|s| s.content.as_ref()).collect();
        assert_eq!(rebuilt, line, "overlay must not alter the text");
        // Horizontal scroll drops leading chars.
        let l2 = overlay_line(line, &base, None, None, 6);
        let rebuilt2: String = l2.spans.iter().map(|s| s.content.as_ref()).collect();
        assert_eq!(rebuilt2, "世界");
        // Empty line still shows a cursor block.
        let l3 = overlay_line("", &[], None, Some(0), 0);
        let rebuilt3: String = l3.spans.iter().map(|s| s.content.as_ref()).collect();
        assert_eq!(rebuilt3, " ");
    }

    #[test]
    fn editor_visual_span_covers_same_and_cross_line_selection() {
        let mut tab = WorkspaceTab::query(1);
        tab.vim_mode = VimMode::Visual;
        tab.buffer = vec!["select abc".into(), "from table".into()];
        tab.visual_anchor = (0, 7);
        tab.cursor_row = 0;
        tab.cursor_col = 9;

        assert_eq!(editor_visual_span_on_line(&tab, 0), Some((7, 10)));
        assert_eq!(editor_visual_span_on_line(&tab, 1), None);

        tab.cursor_row = 1;
        tab.cursor_col = 3;

        assert_eq!(editor_visual_span_on_line(&tab, 0), Some((7, 10)));
        assert_eq!(editor_visual_span_on_line(&tab, 1), Some((0, 4)));
    }

    #[test]
    fn validation_issue_style_uses_error_and_warning_colors() {
        let issue = Issue {
            offset: 0,
            length: 1,
            severity: Severity::Error,
            message: "bad mongo".into(),
        };
        assert_eq!(validation_issue_style(&issue).fg, Some(theme::DANGER));

        let issue = Issue {
            severity: Severity::Warning,
            ..issue
        };
        assert_eq!(validation_issue_style(&issue).fg, Some(theme::WARNING));
    }

    #[test]
    fn viewport_scroll_follows_cursor_without_sticking_to_bottom() {
        let (h, total) = (10usize, 100usize);

        // Scroll down one row at a time: the cursor always stays inside the
        // window and the offset tracks it down to the last page.
        let mut off = 0;
        for c in 0..total {
            off = viewport_offset(off, c, h, total);
            assert!(c >= off && c < off + h, "cursor {c} out of window [{off},{})", off + h);
        }
        assert_eq!(off, total - h, "should be parked on the last page");

        // Scroll back up but stay within the current window: the offset must NOT
        // move, so the cursor rises off the bottom row instead of sticking to it.
        off = viewport_offset(off, 97, h, total);
        assert_eq!(off, 90, "window must not scroll while cursor is visible");
        assert!(97 < off + h - 1, "cursor must not be pinned to the bottom row");

        // Cross the top edge: the window pulls up to show the cursor at the top.
        off = viewport_offset(off, 88, h, total);
        assert_eq!(off, 88);
    }

    #[test]
    fn viewport_offset_handles_short_lists_and_zero_height() {
        // Fewer rows than the viewport: never scroll.
        assert_eq!(viewport_offset(5, 2, 10, 3), 0);
        // Degenerate height is treated as 1 without panicking.
        assert_eq!(viewport_offset(0, 4, 0, 10), 4);
    }

    #[test]
    fn empty_document_result_renders_zero_documents() {
        let backend = TestBackend::new(60, 8);
        let mut terminal = Terminal::new(backend).unwrap();
        let mut tab = WorkspaceTab::query(1);
        tab.result = Some(Set {
            document_result: true,
            ..Default::default()
        });

        terminal.draw(|f| super::render_result(f, &tab, f.area())).unwrap();
        let buf = terminal.backend().buffer();
        let area = *buf.area();
        let screen = (0..area.height)
            .map(|y| {
                (0..area.width)
                    .filter_map(|x| buf.cell((x, y)).map(|c| c.symbol().to_string()))
                    .collect::<String>()
            })
            .collect::<Vec<_>>()
            .join("\n");

        assert!(screen.contains("0 documents"), "screen = {screen:?}");
        assert!(!screen.contains("NULL"), "screen = {screen:?}");
    }
}
