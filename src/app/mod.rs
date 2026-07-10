//! TUI application layer (immediate-mode ratatui). The event loop recomputes
//! layout + hitboxes each frame, draws, then awaits the next crossterm event,
//! async DB/AI result, or spinner tick.

mod actions;
mod ai_panel;
mod clipboard;
mod forms;
mod game;
mod highlight;
mod icons;
mod input;
mod nav;
mod recover;
mod state;
mod task;
mod theme;
mod ui;
mod vim;

use std::time::Duration;

use anyhow::Result;
use crossterm::event::{Event, EventStream};
use futures::StreamExt;
use tokio::sync::mpsc;

pub use state::App;

use crate::cli::Options;
use crate::terminal::TerminalGuard;
use task::{AppMsg, TaskState};

/// Entry point: set up the terminal and run the event loop until quit.
pub async fn run(options: Options) -> Result<()> {
    let log_path = recover::panic_log_path(&options.config_path);
    recover::install_logging_hook(log_path);
    let (mut tui, mut guard) = TerminalGuard::enter()?;

    let (tx, mut rx) = mpsc::unbounded_channel::<AppMsg>();
    let mut app = App::new(options);
    app.tasks = TaskState::init(tx);

    let size = tui.size()?;
    app.width = size.width;
    app.height = size.height;

    let mut events = EventStream::new();
    let mut spinner = tokio::time::interval(Duration::from_millis(90));

    while !app.should_quit {
        // Render under a panic guard: a render panic becomes an error box rather
        // than crashing the program (contract: crash safety).
        let mut hits = Vec::new();
        let draw_result = recover::guard(|| {
            tui.draw(|f| ui::render_all(f, &app, &mut hits))
        });
        match draw_result {
            Ok(Ok(_)) => {
                app.hitboxes = hits;
                // Snapshot the rendered cells so a mouse drag can extract the
                // selected text (contract §11).
                let buf = tui.current_buffer_mut();
                let a = buf.area;
                let mut lines = Vec::with_capacity(a.height as usize);
                for y in 0..a.height {
                    let mut s = String::new();
                    for x in 0..a.width {
                        if let Some(cell) = buf.cell((a.x + x, a.y + y)) {
                            s.push_str(cell.symbol());
                        }
                    }
                    lines.push(s);
                }
                app.frame_lines = lines;
            }
            Ok(Err(e)) => return Err(e.into()),
            Err(msg) => app.error_box = Some(format!("render error: {msg}")),
        }

        tokio::select! {
            maybe_event = events.next() => {
                match maybe_event {
                    Some(Ok(event)) => {
                        if let Err(msg) = recover::guard(|| dispatch_event(&mut app, event)) {
                            app.error_box = Some(format!("input error: {msg}"));
                        }
                    }
                    Some(Err(_)) | None => break,
                }
            }
            Some(AppMsg::Async(msg)) = rx.recv() => {
                if let Err(m) = recover::guard(|| app.apply_async(msg)) {
                    app.error_box = Some(format!("error: {m}"));
                }
            }
            _ = spinner.tick() => {
                app.tasks.tick();
                if app.page == state::Page::Game {
                    app.game_tick();
                }
            }
        }
    }

    guard.restore();
    Ok(())
}

fn dispatch_event(app: &mut App, event: Event) {
    match event {
        Event::Key(key) => input::handle_key(app, key),
        Event::Mouse(mouse) => input::handle_mouse(app, mouse),
        Event::Resize(w, h) => {
            app.width = w;
            app.height = h;
        }
        _ => {}
    }
}

#[cfg(test)]
mod tests {
    use super::state::{App, Page};
    use super::ui::{self, hit_test, HitId};
    use crate::cli::Options;
    use crate::config::{Driver, Profile};
    use ratatui::backend::TestBackend;
    use ratatui::Terminal;
    use std::path::PathBuf;

    fn test_app() -> App {
        App::new(Options {
            config_path: PathBuf::from("/tmp/tdb-test.enc"),
        })
    }

    fn render(app: &App) -> Vec<ui::Hitbox> {
        let backend = TestBackend::new(100, 30);
        let mut term = Terminal::new(backend).unwrap();
        let mut hits = Vec::new();
        term.draw(|f| ui::render_all(f, app, &mut hits)).unwrap();
        hits
    }

    /// Render and return the screen as rows of symbols (for grid assertions).
    fn render_rows(app: &App) -> Vec<String> {
        let backend = TestBackend::new(100, 30);
        let mut term = Terminal::new(backend).unwrap();
        let mut hits = Vec::new();
        term.draw(|f| ui::render_all(f, app, &mut hits)).unwrap();
        let buf = term.backend().buffer();
        let area = *buf.area();
        (0..area.height)
            .map(|y| {
                (0..area.width)
                    .filter_map(|x| buf.cell((x, y)).map(|c| c.symbol().to_string()))
                    .collect::<String>()
            })
            .collect()
    }

    #[test]
    fn query_tab_starts_in_insert_mode() {
        // Fix A: users must be able to type SQL immediately after opening a tab.
        let tab = super::state::WorkspaceTab::query(1);
        assert_eq!(tab.vim_mode, super::state::VimMode::Insert);
    }

    #[test]
    fn connection_node_collapses_subtree() {
        // Fix B: collapsing the connection root hides its databases.
        let mut nav = super::state::NavState {
            databases: vec!["app".into(), "sys".into()],
            ..Default::default()
        };
        let expanded = super::nav::visible_nodes(&nav, "conn (mysql)");
        assert!(expanded.len() >= 3, "connection + 2 dbs expected");
        nav.connection_collapsed = true;
        let collapsed = super::nav::visible_nodes(&nav, "conn (mysql)");
        assert_eq!(collapsed.len(), 1, "only the connection node when collapsed");
    }

    #[test]
    fn live_completion_suggests_keywords() {
        // Fix C: typing a prefix yields suggestions; empty prefix yields none.
        let items = super::actions::live_completion_items(Driver::Mysql, &[], &[], "sel");
        assert!(items.iter().any(|s| s.eq_ignore_ascii_case("SELECT")), "{items:?}");
        assert!(super::actions::live_completion_items(Driver::Mysql, &[], &[], "").is_empty());
        // Column completion: a cached field should be suggested for its prefix.
        let fields = vec![crate::suggest::Field { name: "email".into(), type_: "varchar".into() }];
        let cols = super::actions::live_completion_items(Driver::Mysql, &["users".into()], &fields, "SELECT ema");
        assert!(cols.iter().any(|s| s == "email"), "{cols:?}");
    }

    #[test]
    fn live_completion_suggests_mongo_object_id_constructor() {
        let items = super::actions::live_completion_items(
            Driver::Mongo,
            &[],
            &[],
            "db.users.find({_id: Obj",
        );
        assert!(items.iter().any(|s| s == "ObjectId("), "{items:?}");
    }

    #[test]
    fn command_bar_shows_input() {
        // Reported bug #1: typing after `:` must be visible.
        let mut app = test_app();
        app.page = Page::Connections;
        app.command_active = true;
        app.command = "help".into();
        let rows = render_rows(&app);
        let footer = rows.last().unwrap();
        assert!(footer.contains(":help"), "footer = {footer:?}");
    }

    #[test]
    fn workspace_panel_borders_are_aligned() {
        // Reported bug #3: left (sidebar) and right (main) borders must span the
        // same rows. With no session both panels are full-height bordered blocks.
        let mut app = test_app();
        app.page = Page::Workspace;
        let rows = render_rows(&app);
        let is_box = |s: &str| s.chars().all(|c| ('\u{2500}'..='\u{257F}').contains(&c)) && !s.is_empty();
        // Body spans y=1..=28 (header y0, footer y29). Top/bottom border rows must
        // be box-drawing at both the far-left and far-right columns.
        let top = &rows[1];
        let bottom = &rows[28];
        let left_top = top.chars().next().unwrap().to_string();
        let right_top = top.chars().last().unwrap().to_string();
        let left_bot = bottom.chars().next().unwrap().to_string();
        let right_bot = bottom.chars().last().unwrap().to_string();
        assert!(is_box(&left_top) && is_box(&right_top), "top row not bordered both sides: {top:?}");
        assert!(is_box(&left_bot) && is_box(&right_bot), "bottom row not bordered both sides: {bottom:?}");
    }

    #[test]
    fn unlock_screen_renders() {
        let app = test_app();
        // Should not panic and registers no row hitboxes.
        let _ = render(&app);
    }

    #[test]
    fn connections_rows_are_clickable() {
        let mut app = test_app();
        app.page = Page::Connections;
        app.vault.profiles.push(Profile {
            id: "local".into(),
            name: "local".into(),
            driver: Driver::Mysql,
            host: "127.0.0.1".into(),
            port: 3306,
            user: "root".into(),
            password: "x".into(),
            database: "app".into(),
            auth_db: String::new(),
            redis_db: 0,
            uri_params: String::new(),
            read_only: false,
        });
        let hits = render(&app);
        // The connection row hitbox must resolve at its rendered position
        // (contract §2: hitbox/render same source).
        let row = hits.iter().find(|h| h.id == HitId::ConnRow(0)).expect("row hitbox");
        let got = hit_test(&hits, row.rect.x + 1, row.rect.y);
        assert_eq!(got, Some(HitId::ConnRow(0)));
    }

    #[test]
    fn form_overlay_registers_buttons() {
        let mut app = test_app();
        app.page = Page::Connections;
        app.open_new_form();
        app.confirm_form_driver(); // leave driver-select so fields render
        let hits = render(&app);
        assert!(hits.iter().any(|h| h.id == HitId::FormOk));
        assert!(hits.iter().any(|h| h.id == HitId::FormCancel));
    }

    #[test]
    fn help_overlay_renders() {
        let mut app = test_app();
        app.page = Page::Connections;
        app.help_open = true;
        let hits = render(&app);
        assert!(hits.iter().any(|h| h.id == HitId::HelpClose));
    }
}
