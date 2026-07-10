//! Application state. The 98-field Go `Model` is decomposed into focused
//! sub-states composed into [`App`]; render/input functions borrow only what
//! they need, which keeps any single struct from becoming a god object.

use std::cell::Cell;
use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use crate::cli::Options;
use crate::config::{Profile, Store, Vault};
use crate::db::{Adapter, Object, Target};
use crate::result::Set;

use super::task::TaskState;
use super::theme::StatusKind;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Page {
    Unlock,
    Connections,
    Workspace,
    Help,
    Game,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Focus {
    Sidebar,
    Main,
    Command,
    Form,
    Overlay,
}

/// One open connection with its own navigation tree and workspace tabs (the
/// per-connection snapshot state from the Go multi-connection sessions design).
pub struct Session {
    pub id: u64,
    pub profile: Profile,
    pub adapter: Arc<Adapter>,
    pub nav: NavState,
    pub tabs: Vec<WorkspaceTab>,
    pub active_tab: usize,
    pub next_tab_id: u64,
    /// Current Doris catalog / database scope applied to ad-hoc queries.
    pub current_catalog: String,
    pub current_database: String,
    /// Cached columns per table (lowercased name) for editor completion, plus
    /// the set of tables whose columns are already being fetched.
    pub field_cache: HashMap<String, Vec<crate::suggest::Field>>,
    pub fields_requested: HashSet<String>,
}

impl Session {
    pub fn conn_label(&self) -> String {
        format!("{} ({})", self.profile.name, self.profile.driver.as_str())
    }
    pub fn active_tab(&self) -> Option<&WorkspaceTab> {
        self.tabs.get(self.active_tab)
    }
    pub fn active_tab_mut(&mut self) -> Option<&mut WorkspaceTab> {
        self.tabs.get_mut(self.active_tab)
    }
}

impl WorkspaceTab {
    /// Drop any in-result search so a freshly loaded/paged result set doesn't
    /// keep a stale query, match count, or open search box.
    pub fn clear_result_search(&mut self) {
        self.result_search.clear();
        self.result_search_active = false;
        self.result_search_total = 0;
        self.result_search_index = 0;
    }
}

#[derive(Default)]
pub struct NavState {
    pub databases: Vec<String>,
    pub catalogs: Vec<String>,
    pub catalog_databases: HashMap<String, Vec<String>>,
    pub db_objects: HashMap<String, Vec<Object>>,
    pub expanded_dbs: HashSet<String>,
    pub expanded_catalogs: HashSet<String>,
    pub cursor: usize,
    /// Persisted viewport top, adjusted at render time so the cursor stays
    /// visible without snapping to the bottom (see `render_sidebar`).
    pub v_offset: Cell<usize>,
    pub search_active: bool,
    pub search_query: String,
    /// Index into the jump-search match list for n/N cycling.
    pub search_match_index: usize,
    /// When true the connection root node is collapsed (its catalogs/databases
    /// are hidden).
    pub connection_collapsed: bool,
    /// `g` pressed, awaiting a second `g` (gg -> first visible nav node).
    pub pending_g: bool,
}

/// Scope key for the db_objects map: "catalog\0db" or just "db".
pub fn scope_key(catalog: &str, database: &str) -> String {
    if catalog.is_empty() || catalog.eq_ignore_ascii_case("internal") {
        database.to_string()
    } else {
        format!("{catalog}\u{0}{database}")
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TabKind {
    Data,
    Query,
}

pub struct WorkspaceTab {
    pub id: u64,
    pub kind: TabKind,
    pub title: String,
    pub target: Option<Target>,
    /// Query editor buffer (lines). A vim layer is added in phase 2.
    pub buffer: Vec<String>,
    pub cursor_row: usize,
    pub cursor_col: usize,
    /// Monotonic per-tab query run id. Async query results with an older id are
    /// ignored so slow previous runs cannot overwrite newer results.
    pub query_run_id: u64,
    pub result: Option<Set>,
    pub view: ResultViewState,
    /// Query tabs only: whether keyboard focus is on the result list (Tab
    /// toggles editor ⇄ result so the result can be scrolled/searched).
    pub result_focused: bool,
    pub error: Option<String>,
    pub preview_offset: i32,
    pub preview_has_more: bool,
    pub metadata_mode: bool,
    pub metadata: Option<crate::db::ObjectMetadata>,
    pub completion: Option<EditorCompletion>,
    pub result_search: String,
    pub result_search_active: bool,
    /// Number of rows matching the current result search (0 when none / no query).
    pub result_search_total: usize,
    /// 1-based ordinal of the selected match within the matches (0 when none).
    pub result_search_index: usize,
    pub vim_mode: VimMode,
    pub vim_pending: Option<char>,
    pub vim_count: usize,
    pub vim_pending_count: usize,
    pub vim_search_active: bool,
    pub vim_search_reverse: bool,
    pub vim_search_query: String,
    pub vim_last_search: String,
    pub vim_last_search_reverse: bool,
    pub vim_last_change: String,
    pub visual_anchor: (usize, usize),
    pub undo: Vec<(Vec<String>, usize, usize)>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum VimMode {
    Normal,
    Insert,
    Visual,
}

/// Active completion popup over the query editor (Tab cycles, contract §18).
pub struct EditorCompletion {
    pub items: Vec<String>,
    pub index: usize,
    /// Character column where the replaced token begins.
    pub token_start: usize,
    pub row: usize,
}

impl WorkspaceTab {
    pub fn query(id: u64) -> WorkspaceTab {
        WorkspaceTab {
            id,
            kind: TabKind::Query,
            title: format!("query {id}"),
            target: None,
            buffer: vec![String::new()],
            cursor_row: 0,
            cursor_col: 0,
            query_run_id: 0,
            result: None,
            view: ResultViewState::default(),
            result_focused: false,
            error: None,
            preview_offset: 0,
            preview_has_more: false,
            metadata_mode: false,
            metadata: None,
            completion: None,
            result_search: String::new(),
            result_search_active: false,
            result_search_total: 0,
            result_search_index: 0,
            // Start in INSERT so users can type SQL immediately; Esc enters
            // Normal for vim motions.
            vim_mode: VimMode::Insert,
            vim_pending: None,
            vim_count: 0,
            vim_pending_count: 1,
            vim_search_active: false,
            vim_search_reverse: false,
            vim_search_query: String::new(),
            vim_last_search: String::new(),
            vim_last_search_reverse: false,
            vim_last_change: String::new(),
            visual_anchor: (0, 0),
            undo: Vec::new(),
        }
    }

    pub fn data(id: u64, target: Target) -> WorkspaceTab {
        WorkspaceTab {
            id,
            kind: TabKind::Data,
            title: target.name.clone(),
            target: Some(target),
            buffer: vec![String::new()],
            cursor_row: 0,
            cursor_col: 0,
            query_run_id: 0,
            result: None,
            view: ResultViewState::default(),
            result_focused: false,
            error: None,
            preview_offset: 0,
            preview_has_more: false,
            metadata_mode: false,
            metadata: None,
            completion: None,
            result_search: String::new(),
            result_search_active: false,
            result_search_total: 0,
            result_search_index: 0,
            vim_mode: VimMode::Normal,
            vim_pending: None,
            vim_count: 0,
            vim_pending_count: 1,
            vim_search_active: false,
            vim_search_reverse: false,
            vim_search_query: String::new(),
            vim_last_search: String::new(),
            vim_last_search_reverse: false,
            vim_last_change: String::new(),
            visual_anchor: (0, 0),
            undo: Vec::new(),
        }
    }

    pub fn buffer_text(&self) -> String {
        self.buffer.join("\n")
    }
}

#[derive(Default)]
pub struct ResultViewState {
    /// Persisted viewport top row, adjusted at render time so the selected row
    /// stays visible without snapping to the bottom (see `render_table`).
    pub row_offset: Cell<usize>,
    /// Leftmost visible column — a horizontal scroll offset driven by h/l.
    pub col_offset: usize,
    pub selected_row: usize,
    /// Visual-mode anchor row; `None` when not selecting. The selection spans
    /// whole rows between the anchor and the current `selected_row`.
    pub visual: Option<usize>,
    /// Row-detail view: show the selected row in the shared vim text pane.
    pub detail: bool,
    /// `g` pressed, awaiting a second `g` (gg → first row).
    pub pending_g: bool,
    /// Cursor/selection state for the read-only vim text pane (SQL row detail
    /// and Mongo documents).
    pub text: TextCursor,
}

impl ResultViewState {
    /// Inclusive `(r0, r1)` selected row range: the visual range while selecting,
    /// otherwise just the row under the cursor.
    pub fn selection_rows(&self) -> (usize, usize) {
        match self.visual {
            Some(anchor) => (anchor.min(self.selected_row), anchor.max(self.selected_row)),
            None => (self.selected_row, self.selected_row),
        }
    }
}

/// Cursor + visual selection for the read-only vim text pane. Operates on a
/// `Vec<String>` of content lines rebuilt on demand (see `active_text_lines`).
#[derive(Default)]
pub struct TextCursor {
    pub line: usize,
    pub col: usize,
    /// Render-time scroll offsets that follow the cursor (vertical / horizontal).
    pub v_offset: Cell<usize>,
    pub h_offset: Cell<usize>,
    /// Visual anchor `(line, col)`; `None` when not selecting.
    pub visual: Option<(usize, usize)>,
    /// `true` = line-wise (`V`), `false` = char-wise (`v`).
    pub linewise: bool,
    pub pending_g: bool,
    pub search: String,
    pub search_active: bool,
}

impl TextCursor {
    /// Reset to the top with no selection (on entering a pane / switching record).
    pub fn reset(&mut self) {
        self.line = 0;
        self.col = 0;
        self.v_offset.set(0);
        self.h_offset.set(0);
        self.visual = None;
        self.linewise = false;
        self.search.clear();
        self.search_active = false;
    }

    /// Normalized selection as `(start_line, start_col, end_line, end_col)` with
    /// the cursor cell inclusive, or `None` when not selecting.
    pub fn selection(&self) -> Option<(usize, usize, usize, usize)> {
        let (al, ac) = self.visual?;
        let (cl, cc) = (self.line, self.col);
        Some(if (al, ac) <= (cl, cc) { (al, ac, cl, cc) } else { (cl, cc, al, ac) })
    }

    /// Char span `[start, end)` selected on `line_idx` (which has `len` chars),
    /// or `None`. Line-wise selects the whole line.
    pub fn sel_span_on(&self, line_idx: usize, len: usize) -> Option<(usize, usize)> {
        let (sl, sc, el, ec) = self.selection()?;
        if line_idx < sl || line_idx > el {
            return None;
        }
        if self.linewise {
            return Some((0, len));
        }
        let start = if line_idx == sl { sc.min(len) } else { 0 };
        let end = if line_idx == el { (ec + 1).min(len) } else { len };
        Some((start, end.max(start)))
    }
}

/// Connection create/edit form.
pub struct ConnectionForm {
    pub editing_id: Option<String>,
    pub selecting_driver: bool,
    pub driver_index: usize,
    pub fields: Vec<FormField>,
    pub active_field: usize,
}

pub struct FormField {
    pub key: String,
    pub label: String,
    pub value: String,
    pub secret: bool,
    pub cursor: usize,
}

pub struct App {
    pub options: Options,
    pub store: Store,
    pub vault: Vault,
    pub master: String,
    pub unlock_input: String,
    pub unlock_error: Option<String>,
    pub first_run: bool,

    pub page: Page,
    pub focus: Focus,
    pub previous_focus: Focus,

    pub sessions: Vec<Session>,
    pub active_session: usize,
    pub next_session_id: u64,

    pub connections_cursor: usize,
    pub form: Option<ConnectionForm>,

    pub command: String,
    pub command_active: bool,
    /// Command-line (`:`) completion candidates; empty means the popup is hidden.
    pub command_completion: Vec<crate::suggest::Suggestion>,
    pub command_completion_index: usize,

    pub message: String,
    pub message_kind: StatusKind,

    pub width: u16,
    pub height: u16,

    pub tasks: TaskState,
    pub hitboxes: Vec<super::ui::Hitbox>,

    pub confirm: Option<Confirm>,
    pub error_box: Option<String>,
    pub help_open: bool,
    pub game: Option<super::game::Game>,
    pub ai: AiState,

    pub history: crate::history::Store,
    pub history_open: bool,
    pub history_entries: Vec<crate::history::Entry>,
    pub history_cursor: usize,

    pub icons: super::icons::Icons,
    pub selection: Selection,
    pub frame_lines: Vec<String>,
    pub vim_register: String,

    pub should_quit: bool,
}

/// Mouse drag text selection over the rendered frame (contract §11).
#[derive(Default)]
pub struct Selection {
    pub active: bool,
    pub dragging: bool,
    pub moved: bool,
    pub anchor: (u16, u16),
    pub cursor: (u16, u16),
}

#[derive(Default)]
pub struct AiState {
    pub open: bool,
    pub provider_name: String,
    pub input: String,
    pub messages: Vec<AiMsg>,
    pub pending: bool,
    pub last_sql: Vec<String>,
    pub scroll: usize,
    /// `@`-mention table-name completion candidates; empty means popup hidden.
    pub mention_items: Vec<crate::suggest::Suggestion>,
    pub mention_index: usize,
}

pub struct AiMsg {
    pub role: String, // "you" | "ai" | "err"
    pub text: String,
}

pub struct Confirm {
    pub message: String,
    pub action: ConfirmAction,
}

#[derive(Clone)]
pub enum ConfirmAction {
    DeleteConnection(String),
    CloseSession(u64),
    Quit,
}

impl App {
    pub fn new(options: Options) -> App {
        let store = Store::new(options.config_path.clone());
        let first_run = !store.exists();
        let history_path = options
            .config_path
            .parent()
            .map(|d| d.join("tdb-history.json"))
            .unwrap_or_else(|| std::path::PathBuf::from("tdb-history.json"));
        App {
            options,
            store,
            vault: Vault::default(),
            master: String::new(),
            unlock_input: String::new(),
            unlock_error: None,
            first_run,
            page: Page::Unlock,
            focus: Focus::Command,
            previous_focus: Focus::Sidebar,
            sessions: Vec::new(),
            active_session: 0,
            next_session_id: 1,
            connections_cursor: 0,
            form: None,
            command: String::new(),
            command_active: false,
            command_completion: Vec::new(),
            command_completion_index: 0,
            message: String::new(),
            message_kind: StatusKind::Info,
            width: 80,
            height: 24,
            tasks: TaskState::default(),
            hitboxes: Vec::new(),
            confirm: None,
            error_box: None,
            help_open: false,
            game: None,
            ai: AiState::default(),
            history: crate::history::Store::new(history_path),
            history_open: false,
            history_entries: Vec::new(),
            history_cursor: 0,
            icons: super::icons::Icons::detect(),
            selection: Selection::default(),
            frame_lines: Vec::new(),
            vim_register: String::new(),
            should_quit: false,
        }
    }

    pub fn set_status(&mut self, kind: StatusKind, msg: impl Into<String>) {
        self.message_kind = kind;
        self.message = msg.into();
    }

    pub fn active_session(&self) -> Option<&Session> {
        self.sessions.get(self.active_session)
    }
    pub fn active_session_mut(&mut self) -> Option<&mut Session> {
        self.sessions.get_mut(self.active_session)
    }
    pub fn session_by_id(&mut self, id: u64) -> Option<&mut Session> {
        self.sessions.iter_mut().find(|s| s.id == id)
    }
}

#[cfg(test)]
mod tests {
    use super::ResultViewState;

    #[test]
    fn selection_rows_single_and_visual_range() {
        let mut v = ResultViewState::default();
        v.selected_row = 3;
        // No visual: just the cursor row.
        assert_eq!(v.selection_rows(), (3, 3));
        // Visual anchored above; range is normalized to min/max.
        v.visual = Some(1);
        assert_eq!(v.selection_rows(), (1, 3));
        // Anchor below the cursor also normalizes.
        v.visual = Some(6);
        assert_eq!(v.selection_rows(), (3, 6));
    }

    #[test]
    fn text_cursor_selection_spans() {
        let mut t = super::TextCursor::default();
        // No visual → nothing selected.
        assert_eq!(t.sel_span_on(0, 10), None);
        // Char-wise: anchor (line1,col2) → cursor (line1,col5); inclusive cursor.
        t.line = 1;
        t.col = 5;
        t.visual = Some((1, 2));
        assert_eq!(t.sel_span_on(1, 20), Some((2, 6)));
        assert_eq!(t.sel_span_on(0, 20), None);
        // Char-wise across lines: mid line fully covered.
        t.line = 3;
        t.col = 4;
        t.visual = Some((1, 2));
        assert_eq!(t.sel_span_on(1, 8), Some((2, 8))); // start line: from anchor col
        assert_eq!(t.sel_span_on(2, 6), Some((0, 6))); // middle: whole line
        assert_eq!(t.sel_span_on(3, 9), Some((0, 5))); // end line: through cursor
        // Line-wise selects whole lines.
        t.linewise = true;
        assert_eq!(t.sel_span_on(2, 6), Some((0, 6)));
    }
}
