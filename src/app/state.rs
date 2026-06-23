//! Application state. The 98-field Go `Model` is decomposed into focused
//! sub-states composed into [`App`]; render/input functions borrow only what
//! they need, which keeps any single struct from becoming a god object.

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

#[derive(Default)]
pub struct NavState {
    pub databases: Vec<String>,
    pub catalogs: Vec<String>,
    pub catalog_databases: HashMap<String, Vec<String>>,
    pub db_objects: HashMap<String, Vec<Object>>,
    pub expanded_dbs: HashSet<String>,
    pub expanded_catalogs: HashSet<String>,
    pub cursor: usize,
    pub v_offset: usize,
    pub search_active: bool,
    pub search_query: String,
    /// When true the connection root node is collapsed (its catalogs/databases
    /// are hidden).
    pub connection_collapsed: bool,
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
    pub result: Option<Set>,
    pub view: ResultViewState,
    pub error: Option<String>,
    pub preview_offset: i32,
    pub preview_has_more: bool,
    pub metadata_mode: bool,
    pub metadata: Option<crate::db::ObjectMetadata>,
    pub completion: Option<EditorCompletion>,
    pub result_search: String,
    pub result_search_active: bool,
    pub vim_mode: VimMode,
    pub vim_pending: Option<char>,
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
            result: None,
            view: ResultViewState::default(),
            error: None,
            preview_offset: 0,
            preview_has_more: false,
            metadata_mode: false,
            metadata: None,
            completion: None,
            result_search: String::new(),
            result_search_active: false,
            // Start in INSERT so users can type SQL immediately; Esc enters
            // Normal for vim motions.
            vim_mode: VimMode::Insert,
            vim_pending: None,
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
            result: None,
            view: ResultViewState::default(),
            error: None,
            preview_offset: 0,
            preview_has_more: false,
            metadata_mode: false,
            metadata: None,
            completion: None,
            result_search: String::new(),
            result_search_active: false,
            vim_mode: VimMode::Normal,
            vim_pending: None,
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
    pub row_offset: usize,
    pub col_offset: usize,
    pub selected_row: usize,
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
