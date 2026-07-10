//! High-level actions: each spawns an async DB op and routes the typed result
//! back through [`AsyncMsg`]. Kept separate from input dispatch so a key, a
//! mouse click and a command can all trigger the same action.

use std::sync::Arc;

use crate::config::Driver;
use crate::db::{Command, Page, Query, Scope, Target};
use crate::validate::{self, Issue, Severity};

use super::nav::{self, NavKind};
use super::state::WorkspaceTab as WsTab;
use super::state::{scope_key, App, Session, TabKind, WorkspaceTab};
use super::task::AsyncMsg;
use super::theme::StatusKind;

fn blocking_query_issue(driver: Driver, text: &str) -> Option<Issue> {
    validate::validate(driver, text)
        .into_iter()
        .find(|issue| issue.severity == Severity::Error)
}

impl App {
    /// Open (or focus) a connection by profile id.
    pub fn open_connection(&mut self, profile_id: &str) {
        if let Some(idx) = self.sessions.iter().position(|s| s.profile.id == profile_id) {
            self.active_session = idx;
            self.page = super::state::Page::Workspace;
            self.focus = super::state::Focus::Sidebar;
            return;
        }
        let Some(profile) = self.vault.get_profile(profile_id).cloned() else {
            return;
        };
        let name = profile.name.clone();
        self.tasks.spawn_db(format!("Connecting to {name}"), async move {
            match crate::db::connect(&profile).await {
                Ok(adapter) => AsyncMsg::Connected {
                    profile,
                    adapter: Arc::new(adapter),
                },
                Err(e) => AsyncMsg::ConnectFailed {
                    profile_name: profile.name.clone(),
                    message: e.to_string(),
                },
            }
        });
    }

    pub fn load_databases(&mut self, session_id: u64) {
        let Some(session) = self.session_by_id(session_id) else { return };
        let adapter = session.adapter.clone();
        self.tasks.spawn_db("Loading databases", async move {
            let catalogs = adapter.list_catalogs().await.unwrap_or_default();
            let databases = if catalogs.is_empty() {
                adapter.list_databases().await.unwrap_or_default()
            } else {
                Vec::new()
            };
            AsyncMsg::Databases {
                session_id,
                databases,
                catalogs,
            }
        });
    }

    /// Activate the node under the nav cursor: expand/collapse, or open a table.
    pub fn nav_activate(&mut self) {
        let info = {
            let Some(session) = self.active_session() else { return };
            let nodes = nav::visible_nodes(&session.nav, &session.conn_label());
            let Some(node) = nodes.get(session.nav.cursor.min(nodes.len().saturating_sub(1))) else {
                return;
            };
            (session.id, session.adapter.clone(), node.kind.clone())
        };
        let (sid, adapter, kind) = info;
        match kind {
            NavKind::Connection => {
                if let Some(s) = self.session_by_id(sid) {
                    s.nav.connection_collapsed = !s.nav.connection_collapsed;
                }
            }
            NavKind::Catalog { catalog } => {
                let need_load = {
                    let s = self.session_by_id(sid).unwrap();
                    let expanded = s.nav.expanded_catalogs.contains(&catalog);
                    if expanded {
                        s.nav.expanded_catalogs.remove(&catalog);
                        false
                    } else {
                        s.nav.expanded_catalogs.insert(catalog.clone());
                        !s.nav.catalog_databases.contains_key(&catalog)
                    }
                };
                if need_load {
                    let cat = catalog.clone();
                    self.tasks.spawn_db("Loading databases", async move {
                        let databases = adapter
                            .list_databases_in_catalog(&cat)
                            .await
                            .unwrap_or_default();
                        AsyncMsg::CatalogDatabases {
                            session_id: sid,
                            catalog: cat,
                            databases,
                        }
                    });
                }
            }
            NavKind::Database { catalog, database } => {
                let key = scope_key(&catalog, &database);
                let need_load = {
                    let s = self.session_by_id(sid).unwrap();
                    s.current_catalog = catalog.clone();
                    s.current_database = database.clone();
                    if s.nav.expanded_dbs.contains(&key) {
                        s.nav.expanded_dbs.remove(&key);
                        false
                    } else {
                        s.nav.expanded_dbs.insert(key.clone());
                        !s.nav.db_objects.contains_key(&key)
                    }
                };
                if need_load {
                    let (cat, db, k) = (catalog.clone(), database.clone(), key.clone());
                    self.tasks.spawn_db("Loading objects", async move {
                        let objects = adapter
                            .list_objects(Scope {
                                catalog: cat,
                                database: db,
                                schema: String::new(),
                            })
                            .await
                            .unwrap_or_default();
                        AsyncMsg::Objects {
                            session_id: sid,
                            key: k,
                            objects,
                        }
                    });
                }
            }
            NavKind::Object {
                catalog,
                database,
                object,
            } => {
                self.open_table(sid, catalog, database, object);
            }
        }
    }

    /// (expandable, expanded, is_object, depth) for the node under the cursor.
    fn cursor_node_info(&self) -> Option<(bool, bool, bool, u16)> {
        let session = self.active_session()?;
        let nodes = nav::visible_nodes(&session.nav, &session.conn_label());
        let node = nodes.get(session.nav.cursor.min(nodes.len().saturating_sub(1)))?;
        Some((
            node.expandable,
            node.expanded,
            matches!(node.kind, NavKind::Object { .. }),
            node.depth,
        ))
    }

    /// `l`/`→`: expand a collapsed node, or open a leaf object. Never collapses.
    pub fn nav_expand(&mut self) {
        match self.cursor_node_info() {
            Some((_, _, true, _)) => self.nav_activate(), // open object
            Some((true, false, _, _)) => self.nav_activate(), // expand
            _ => {}
        }
    }

    /// `h`/`←`: collapse an expanded node, else jump to the parent node.
    pub fn nav_collapse(&mut self) {
        match self.cursor_node_info() {
            Some((true, true, false, _)) => self.nav_activate(), // collapse
            Some((_, _, _, depth)) if depth > 0 => {
                // Move the cursor up to the nearest shallower (parent) node.
                if let Some(s) = self.active_session() {
                    let nodes = nav::visible_nodes(&s.nav, &s.conn_label());
                    let cur = s.nav.cursor.min(nodes.len().saturating_sub(1));
                    let parent = (0..cur).rev().find(|&i| nodes[i].depth < depth);
                    if let Some(p) = parent {
                        if let Some(s) = self.active_session_mut() {
                            s.nav.cursor = p;
                        }
                    }
                }
            }
            _ => {}
        }
    }

    /// Jump-style navigation search: move the sidebar cursor to the next/prev
    /// (dir = ±1) or first (dir = 0) node whose label matches `nav.search_query`,
    /// auto-expanding the match's catalog/database. The tree is never filtered.
    pub fn nav_search_jump(&mut self, dir: i32) {
        let Some(s) = self.active_session() else { return };
        let query = s.nav.search_query.to_lowercase();
        if query.is_empty() {
            return;
        }
        let conn = s.conn_label();
        let matches = nav::search_matches(&s.nav, &conn, &query);
        if matches.is_empty() {
            self.set_status(StatusKind::Warning, "no match");
            return;
        }
        let n = matches.len();
        let idx = if dir == 0 {
            0
        } else {
            (s.nav.search_match_index as i32 + dir).rem_euclid(n as i32) as usize
        };
        let target = matches[idx].clone();
        let Some(s) = self.active_session_mut() else { return };
        s.nav.search_match_index = idx;
        // Auto-expand so the matched node is visible in the flattened tree.
        match &target {
            NavKind::Catalog { catalog } => {
                s.nav.expanded_catalogs.insert(catalog.clone());
            }
            NavKind::Database { catalog, database } => {
                if !catalog.is_empty() {
                    s.nav.expanded_catalogs.insert(catalog.clone());
                }
                let _ = database;
            }
            NavKind::Object { catalog, database, .. } => {
                if !catalog.is_empty() {
                    s.nav.expanded_catalogs.insert(catalog.clone());
                }
                s.nav.expanded_dbs.insert(scope_key(catalog, database));
            }
            NavKind::Connection => {}
        }
        let nodes = nav::visible_nodes(&s.nav, &conn);
        if let Some(pos) = nodes.iter().position(|nd| nd.kind == target) {
            s.nav.cursor = pos;
        }
    }

    /// Ensure the current database's objects are loaded so AI `@`-mention has
    /// candidates even when the user has not expanded the database (mirrors Go's
    /// `ensureQueryObjectsLoaded`). Loads asynchronously.
    pub(crate) fn ensure_current_objects_loaded(&mut self) {
        let info = {
            let Some(s) = self.active_session() else { return };
            if s.current_database.is_empty() {
                return;
            }
            let key = scope_key(&s.current_catalog, &s.current_database);
            if s.nav.db_objects.contains_key(&key) {
                return;
            }
            (s.id, s.adapter.clone(), s.current_catalog.clone(), s.current_database.clone(), key)
        };
        let (sid, adapter, catalog, database, key) = info;
        self.tasks.spawn_db("Loading objects", async move {
            let objects = adapter
                .list_objects(Scope { catalog, database, schema: String::new() })
                .await
                .unwrap_or_default();
            AsyncMsg::Objects { session_id: sid, key, objects }
        });
    }

    fn open_table(
        &mut self,
        session_id: u64,
        catalog: String,
        database: String,
        object: crate::db::Object,
    ) {
        let target = Target {
            catalog: catalog.clone(),
            database: database.clone(),
            schema: String::new(),
            name: object.name.clone(),
            type_: object.type_,
        };
        let (adapter, tab_id) = {
            let Some(s) = self.session_by_id(session_id) else { return };
            s.current_catalog = catalog;
            s.current_database = database;
            // Reuse an existing data tab for the same target, else open one.
            let existing = s.tabs.iter().position(|t| {
                t.kind == TabKind::Data && t.target.as_ref() == Some(&target)
            });
            let idx = match existing {
                Some(i) => i,
                None => {
                    let id = s.next_tab_id;
                    s.next_tab_id += 1;
                    s.tabs.push(WorkspaceTab::data(id, target.clone()));
                    s.tabs.len() - 1
                }
            };
            s.active_tab = idx;
            (s.adapter.clone(), s.tabs[idx].id)
        };
        self.focus = super::state::Focus::Main;
        self.spawn_preview(session_id, tab_id, adapter, target, 0);
    }

    fn spawn_preview(
        &mut self,
        session_id: u64,
        tab_id: u64,
        adapter: Arc<crate::db::Adapter>,
        target: Target,
        offset: i32,
    ) {
        self.tasks.spawn_db("Loading rows", async move {
            let result = adapter
                .preview(target, Query::default(), Page::new(100, offset))
                .await
                .map_err(|e| e.to_string());
            AsyncMsg::Preview {
                session_id,
                tab_id,
                offset,
                result,
            }
        });
    }

    /// Page the active data tab forward (+1) or backward (-1).
    pub fn page_data(&mut self, delta: i32) {
        let info = {
            let Some(s) = self.active_session() else { return };
            let Some(tab) = s.active_tab() else { return };
            if tab.kind != TabKind::Data {
                return;
            }
            if delta > 0 && !tab.preview_has_more {
                return;
            }
            if delta < 0 && tab.preview_offset == 0 {
                return;
            }
            let new_offset = (tab.preview_offset + delta * 100).max(0);
            (s.id, tab.id, s.adapter.clone(), tab.target.clone(), new_offset)
        };
        let (sid, tab_id, adapter, target, offset) = info;
        if let Some(target) = target {
            self.spawn_preview(sid, tab_id, adapter, target, offset);
        }
    }

    /// Run the active query tab's buffer.
    pub fn run_active_query(&mut self) {
        let mut blocked = None;
        let info = {
            let Some(s) = self.active_session_mut() else { return };
            let sid = s.id;
            let adapter = s.adapter.clone();
            let catalog = s.current_catalog.clone();
            let database = s.current_database.clone();
            let profile_id = s.profile.id.clone();
            let driver_kind = s.profile.driver;
            let driver = driver_kind.as_str().to_string();
            let Some(tab) = s.active_tab_mut() else { return };
            if tab.kind != TabKind::Query {
                return;
            }
            let text = tab.buffer_text();
            if text.trim().is_empty() {
                return;
            }
            if let Some(issue) = blocking_query_issue(driver_kind, &text) {
                tab.query_run_id = tab.query_run_id.wrapping_add(1);
                tab.result = None;
                tab.error = Some(issue.message.clone());
                tab.view = Default::default();
                tab.clear_result_search();
                blocked = Some(issue.message);
                None
            } else {
                tab.query_run_id = tab.query_run_id.wrapping_add(1);
                let run_id = tab.query_run_id;
                tab.result = None;
                tab.error = None;
                tab.view = Default::default();
                tab.clear_result_search();
                Some((
                    sid,
                    tab.id,
                    run_id,
                    adapter,
                    catalog,
                    database,
                    profile_id,
                    driver,
                    text,
                ))
            }
        };
        if let Some(message) = blocked {
            self.set_status(StatusKind::Error, message);
            return;
        }
        let Some(info) = info else { return };
        let (sid, tab_id, run_id, adapter, catalog, database, profile_id, driver, text) = info;
        let stmt = text.clone();
        let db_for_msg = database.clone();
        self.tasks.spawn_db("Running query", async move {
            let result = adapter
                .execute(Command {
                    text,
                    catalog,
                    database,
                })
                .await
                .map_err(|e| e.to_string());
            AsyncMsg::QueryResult {
                session_id: sid,
                tab_id,
                run_id,
                statement: stmt,
                profile_id,
                driver,
                database: db_for_msg,
                result,
            }
        });
    }

    pub fn new_query_tab(&mut self) {
        let Some(s) = self.active_session_mut() else { return };
        let id = s.next_tab_id;
        s.next_tab_id += 1;
        s.tabs.push(WorkspaceTab::query(id));
        s.active_tab = s.tabs.len() - 1;
        self.focus = super::state::Focus::Main;
    }

    pub fn test_connection(&mut self, profile_id: &str) {
        let Some(profile) = self.vault.get_profile(profile_id).cloned() else { return };
        let name = profile.name.clone();
        self.tasks.spawn_db(format!("Testing {name}"), async move {
            let result = match crate::db::connect(&profile).await {
                Ok(a) => a.test().await.map_err(|e| e.to_string()),
                Err(e) => Err(e.to_string()),
            };
            AsyncMsg::TestResult {
                profile_name: name,
                result,
            }
        });
    }

    /// Tab/Shift+Tab completion in the query editor: compute candidates on first
    /// Recompute the live completion popup from the token before the cursor.
    /// Only runs for an INSERT-mode query tab; shows candidates without inserting
    /// (the user accepts with Tab). Clears the popup when there is no token/match.
    pub fn editor_update_completion(&mut self) {
        let info = {
            let Some(s) = self.active_session() else { return };
            let Some(tab) = s.active_tab() else { return };
            if tab.kind != TabKind::Query || tab.vim_mode != super::state::VimMode::Insert {
                None
            } else {
                let key = scope_key(&s.current_catalog, &s.current_database);
                let objects = s
                    .nav
                    .db_objects
                    .get(&key)
                    .map(|v| v.iter().map(|o| o.name.clone()).collect::<Vec<_>>())
                    .unwrap_or_default();
                let row = tab.cursor_row;
                let line = &tab.buffer[row];
                let prefix: String = line.chars().take(tab.cursor_col).collect();
                // Fields to offer: SQL columns of FROM/JOIN tables, or — for
                // Mongo — the fields of collections referenced as `db.<coll>.`.
                let mut tables = referenced_tables(&tab.buffer_text());
                if s.profile.driver == crate::config::Driver::Mongo {
                    tables.extend(referenced_collections(&tab.buffer_text()));
                }
                let mut fields = Vec::new();
                let mut missing = Vec::new();
                for t in &tables {
                    let tl = t.to_lowercase();
                    if let Some(fs) = s.field_cache.get(&tl) {
                        fields.extend(fs.iter().cloned());
                    } else if !s.fields_requested.contains(&tl) {
                        missing.push(t.clone());
                    }
                }
                Some((
                    s.profile.driver,
                    objects,
                    prefix,
                    row,
                    tab.cursor_col,
                    fields,
                    missing,
                    s.id,
                    s.adapter.clone(),
                    s.current_catalog.clone(),
                    s.current_database.clone(),
                ))
            }
        };
        let Some((driver, objects, prefix, row, col, fields, missing, sid, adapter, cat, db)) = info
        else {
            if let Some(tab) = self.active_session_mut().and_then(|s| s.active_tab_mut()) {
                tab.completion = None;
            }
            return;
        };
        let token = crate::suggest::current_token(&prefix);
        let items = live_completion_items(driver, &objects, &fields, &prefix);
        if let Some(tab) = self.active_session_mut().and_then(|s| s.active_tab_mut()) {
            if items.is_empty() {
                tab.completion = None;
            } else {
                tab.completion = Some(super::state::EditorCompletion {
                    items,
                    index: 0,
                    token_start: col - token.chars().count(),
                    row,
                });
            }
        }
        // Lazily fetch columns for referenced-but-uncached tables so the next
        // keystroke can suggest them.
        for table in missing {
            self.load_fields(sid, adapter.clone(), cat.clone(), db.clone(), table);
        }
    }

    /// Background fetch of a table's column names for editor completion and the
    /// AI `@`-mention schema context.
    pub(crate) fn load_fields(
        &mut self,
        session_id: u64,
        adapter: Arc<crate::db::Adapter>,
        catalog: String,
        database: String,
        table: String,
    ) {
        let tl = table.to_lowercase();
        if let Some(s) = self.session_by_id(session_id) {
            s.fields_requested.insert(tl.clone());
        }
        let Some(tx) = self.tasks.tx.clone() else { return };
        tokio::spawn(async move {
            let target = Target {
                catalog,
                database,
                schema: String::new(),
                name: table,
                type_: crate::db::ObjectType::Table,
            };
            let fields = match adapter.metadata(target).await {
                Ok(meta) => meta
                    .fields
                    .into_iter()
                    .map(|f| crate::suggest::Field { name: f.name, type_: f.type_ })
                    .collect(),
                Err(_) => Vec::new(),
            };
            let _ = tx.send(super::task::AppMsg::Async(AsyncMsg::Fields {
                session_id,
                table: tl,
                fields,
            }));
        });
    }

    /// Toggle the metadata view (columns / indexes / attributes) for a data tab.
    pub fn toggle_metadata(&mut self) {
        let info = {
            let Some(s) = self.active_session() else { return };
            let Some(tab) = s.active_tab() else { return };
            if tab.kind != TabKind::Data {
                return;
            }
            let Some(target) = tab.target.clone() else { return };
            (s.id, tab.id, s.adapter.clone(), target, tab.metadata.is_none())
        };
        let (sid, tab_id, adapter, target, need_load) = info;
        if let Some(tab) = self.active_session_mut().and_then(|s| s.active_tab_mut()) {
            tab.metadata_mode = !tab.metadata_mode;
        }
        if need_load {
            self.tasks.spawn_db("Loading metadata", async move {
                let result = adapter.metadata(target).await.map_err(|e| e.to_string());
                AsyncMsg::Metadata { session_id: sid, tab_id, result }
            });
        }
    }

    /// Move the completion highlight (Tab/↓ forward, Shift+Tab/↑ back).
    pub fn completion_move(&mut self, forward: bool) {
        if let Some(tab) = self.active_session_mut().and_then(|s| s.active_tab_mut()) {
            if let Some(comp) = tab.completion.as_mut() {
                let n = comp.items.len();
                if n > 0 {
                    comp.index = if forward {
                        (comp.index + 1) % n
                    } else {
                        (comp.index + n - 1) % n
                    };
                }
            }
        }
    }

    /// Accept the highlighted completion: replace the token and close the popup.
    pub fn completion_accept(&mut self) {
        if let Some(tab) = self.active_session_mut().and_then(|s| s.active_tab_mut()) {
            if let Some(comp) = tab.completion.take() {
                if let Some(item) = comp.items.get(comp.index).cloned() {
                    apply_completion(tab, comp.row, comp.token_start, &item);
                }
            }
        }
    }

    pub fn completion_dismiss(&mut self) {
        if let Some(tab) = self.active_session_mut().and_then(|s| s.active_tab_mut()) {
            tab.completion = None;
        }
    }

    pub fn completion_open(&self) -> bool {
        self.active_session()
            .and_then(|s| s.active_tab())
            .map(|t| t.completion.is_some())
            .unwrap_or(false)
    }

    /// Populate the command-line (`:`) completion popup with the candidates whose
    /// name prefixes the current token (Tab when the popup is hidden).
    pub fn open_command_suggestions(&mut self) {
        let token = crate::suggest::current_token(&self.command).to_lowercase();
        self.command_completion = command_suggestions(&token);
        self.command_completion_index = 0;
    }

    /// Cycle the command-completion highlight (Tab forward, Shift+Tab back).
    pub fn command_completion_move(&mut self, forward: bool) {
        let n = self.command_completion.len();
        if n == 0 {
            return;
        }
        self.command_completion_index = if forward {
            (self.command_completion_index + 1) % n
        } else {
            (self.command_completion_index + n - 1) % n
        };
    }

    /// Accept the highlighted command completion: replace the current token and
    /// close the popup (the line stays open so arguments can follow).
    pub fn accept_command_suggestion(&mut self) {
        if let Some(item) = self.command_completion.get(self.command_completion_index) {
            self.command = replace_last_token(&self.command, &item.value);
        }
        self.command_completion.clear();
        self.command_completion_index = 0;
    }

    fn record_history(
        &mut self,
        profile_id: String,
        driver: String,
        database: String,
        statement: String,
        ok: bool,
        error: String,
    ) {
        if statement.trim().is_empty() {
            return;
        }
        let entry = crate::history::Entry {
            id: crate::aichat::new_id(),
            profile_id,
            driver,
            database,
            action: "query".into(),
            statement,
            status: if ok { "ok".into() } else { "error".into() },
            error,
            started_at: chrono::Utc::now(),
            execution_count: 1,
            ..Default::default()
        };
        let _ = self.history.append(entry, chrono::Utc::now());
    }

    /// Open the history recall overlay for the active connection.
    pub fn open_history(&mut self) {
        let profile_id = self
            .active_session()
            .map(|s| s.profile.id.clone())
            .unwrap_or_default();
        self.history_entries = self.history.list(&profile_id, 100).unwrap_or_default();
        self.history_cursor = 0;
        self.history_open = !self.history_entries.is_empty();
        if !self.history_open {
            self.set_status(StatusKind::Info, "no history yet");
        }
    }

    pub fn history_move(&mut self, delta: i32) {
        if self.history_entries.is_empty() {
            return;
        }
        let cur = self.history_cursor as i32 + delta;
        self.history_cursor = cur.clamp(0, self.history_entries.len() as i32 - 1) as usize;
    }

    /// Fill the active query tab with the selected history statement.
    pub fn history_fill(&mut self) {
        let Some(entry) = self.history_entries.get(self.history_cursor).cloned() else { return };
        self.history_open = false;
        if self
            .active_session()
            .and_then(|s| s.active_tab())
            .map(|t| t.kind != TabKind::Query)
            .unwrap_or(true)
        {
            self.new_query_tab();
        }
        if let Some(tab) = self.active_session_mut().and_then(|s| s.active_tab_mut()) {
            tab.buffer = entry.statement.split('\n').map(|l| l.to_string()).collect();
            if tab.buffer.is_empty() {
                tab.buffer.push(String::new());
            }
            tab.cursor_row = tab.buffer.len() - 1;
            tab.cursor_col = tab.buffer[tab.cursor_row].chars().count();
        }
        self.focus = super::state::Focus::Main;
    }

    /// Jump to the next/prev (dir=±1) result row matching the tab's search
    /// query; dir=0 searches from the top (incremental typing). Contract: result
    /// search with n/N.
    pub fn result_search_jump(&mut self, dir: i32) {
        let Some(tab) = self.active_session_mut().and_then(|s| s.active_tab_mut()) else { return };
        let q = tab.result_search.to_lowercase();
        if q.is_empty() {
            tab.result_search_total = 0;
            tab.result_search_index = 0;
            return;
        }
        let Some(set) = &tab.result else { return };
        let n = if let Some(t) = &set.table {
            t.rows.len()
        } else {
            set.documents.len()
        };
        let row_matches = |i: usize| -> bool {
            if let Some(t) = &set.table {
                (0..t.columns.len()).any(|c| t.cell_string(i, c).to_lowercase().contains(&q))
            } else if let Some(doc) = set.documents.get(i) {
                serde_json::to_string(&doc.data)
                    .unwrap_or_default()
                    .to_lowercase()
                    .contains(&q)
            } else {
                false
            }
        };
        // Match list (ascending row order) drives both navigation and the N/M
        // counter shown in the title bar.
        let matches: Vec<usize> = (0..n).filter(|&i| row_matches(i)).collect();
        tab.result_search_total = matches.len();
        if matches.is_empty() {
            tab.result_search_index = 0;
            return;
        }
        let cur = tab.view.selected_row;
        let target = if dir == 0 {
            // Incremental typing: jump to the first match at or after the cursor,
            // else wrap to the first match.
            matches.iter().copied().find(|&i| i >= cur).unwrap_or(matches[0])
        } else if dir > 0 {
            // n: first match strictly after the cursor, wrapping around.
            matches.iter().copied().find(|&i| i > cur).unwrap_or(matches[0])
        } else {
            // N: last match strictly before the cursor, wrapping around.
            matches.iter().rev().copied().find(|&i| i < cur).unwrap_or(matches[matches.len() - 1])
        };
        // Move the cursor; the renderer scrolls the matched row into view.
        tab.view.selected_row = target;
        tab.result_search_index = matches.iter().position(|&i| i == target).map_or(0, |p| p + 1);
    }

    /// Extract the rectangular text region between two frame cells and copy it
    /// to the system clipboard. Trailing spaces per row are trimmed; box-drawing
    /// frame glyphs are dropped so a selection over a modal copies clean text
    /// (contract §11).
    pub fn copy_selection(&mut self, a: (u16, u16), b: (u16, u16)) {
        let text = extract_selection(&self.frame_lines, a, b);
        if text.is_empty() {
            return;
        }
        match super::clipboard::copy(&text) {
            Ok(_) => self.set_status(StatusKind::Success, "selection copied"),
            Err(e) => self.set_status(StatusKind::Error, e),
        }
    }

    pub fn game_tick(&mut self) {
        let width = self.width.saturating_sub(4);
        if let Some(game) = self.game.as_mut() {
            game.advance(width);
        }
    }

    fn active_result(&self) -> Option<&crate::result::Set> {
        self.active_session()?.active_tab()?.result.as_ref()
    }

    /// `:export csv|json [path]` — write the active result to a file.
    pub fn export_result(&mut self, format: &str, path: Option<&str>) {
        let Some(set) = self.active_result() else {
            self.set_status(StatusKind::Warning, "no result to export");
            return;
        };
        let mut buf = Vec::new();
        let res = match format {
            "json" => set.write_json(&mut buf),
            _ => set.write_csv(&mut buf),
        };
        if let Err(e) = res {
            self.set_status(StatusKind::Error, format!("export failed: {e}"));
            return;
        }
        let default = format!("tdb-export.{}", if format == "json" { "json" } else { "csv" });
        let path = path.unwrap_or(&default).to_string();
        match std::fs::write(&path, &buf) {
            Ok(()) => {
                #[cfg(unix)]
                {
                    use std::os::unix::fs::PermissionsExt;
                    let _ = std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600));
                }
                self.set_status(StatusKind::Success, format!("exported to {path}"));
            }
            Err(e) => self.set_status(StatusKind::Error, format!("write failed: {e}")),
        }
    }

    /// `:copy csv|json` — copy the active result to the system clipboard.
    pub fn copy_result(&mut self, format: &str) {
        let Some(set) = self.active_result() else {
            self.set_status(StatusKind::Warning, "no result to copy");
            return;
        };
        let mut buf = Vec::new();
        let res = match format {
            "json" => set.write_json(&mut buf),
            _ => set.write_csv(&mut buf),
        };
        if res.is_err() {
            self.set_status(StatusKind::Error, "format failed");
            return;
        }
        let text = String::from_utf8_lossy(&buf).into_owned();
        match super::clipboard::copy(&text) {
            Ok(_) => self.set_status(StatusKind::Success, "copied to clipboard"),
            Err(e) => self.set_status(StatusKind::Error, e),
        }
    }

    /// Copy the selected row(s) to the clipboard as TSV (all columns). For
    /// document results, copies the selected documents' JSON.
    pub fn copy_result_rows(&mut self) {
        let text = {
            let Some(tab) = self.active_session().and_then(|s| s.active_tab()) else { return };
            let Some(set) = &tab.result else { return };
            let (r0, r1) = tab.view.selection_rows();
            if let Some(table) = &set.table {
                if table.rows.is_empty() || table.columns.is_empty() {
                    return;
                }
                let rmax = table.rows.len() - 1;
                let ncols = table.columns.len();
                let lines: Vec<String> = (r0..=r1.min(rmax))
                    .map(|r| {
                        (0..ncols)
                            .map(|c| table.cell_string(r, c))
                            .collect::<Vec<_>>()
                            .join("\t")
                    })
                    .collect();
                lines.join("\n")
            } else if !set.documents.is_empty() {
                let rmax = set.documents.len() - 1;
                (r0..=r1.min(rmax))
                    .filter_map(|r| set.documents.get(r))
                    .map(|d| serde_json::to_string_pretty(&d.data).unwrap_or_default())
                    .collect::<Vec<_>>()
                    .join("\n")
            } else {
                return;
            }
        };
        if text.is_empty() {
            return;
        }
        match super::clipboard::copy(&text) {
            Ok(_) => self.set_status(StatusKind::Success, "copied to clipboard"),
            Err(e) => self.set_status(StatusKind::Error, e),
        }
        if let Some(t) = self.active_session_mut().and_then(|s| s.active_tab_mut()) {
            t.view.visual = None;
        }
    }

    /// Copy the vim text pane's selection (char/line-wise) to the clipboard.
    /// With no active selection, copies the current line (yy-like).
    pub fn copy_text_selection(&mut self) {
        let text = {
            let Some(tab) = self.active_session().and_then(|s| s.active_tab()) else { return };
            let lines = super::ui::pane_lines(tab);
            if lines.is_empty() {
                return;
            }
            let tc = &tab.view.text;
            let (sl, el) = tc.selection().map(|(sl, _, el, _)| (sl, el)).unwrap_or((tc.line, tc.line));
            (sl..=el.min(lines.len() - 1))
                .map(|li| {
                    let chars: Vec<char> = lines[li].chars().collect();
                    let (s, e) = tc.sel_span_on(li, chars.len()).unwrap_or((0, chars.len()));
                    chars[s.min(chars.len())..e.min(chars.len())].iter().collect::<String>()
                })
                .collect::<Vec<_>>()
                .join("\n")
        };
        match super::clipboard::copy(&text) {
            Ok(_) => self.set_status(StatusKind::Success, "copied to clipboard"),
            Err(e) => self.set_status(StatusKind::Error, e),
        }
        if let Some(t) = self.active_session_mut().and_then(|s| s.active_tab_mut()) {
            t.view.text.visual = None;
        }
    }

    /// Apply a typed async result to state.
    pub fn apply_async(&mut self, msg: AsyncMsg) {
        match msg {
            AsyncMsg::Connected { profile, adapter } => {
                self.tasks.finish();
                let id = self.next_session_id;
                self.next_session_id += 1;
                // Start with one query tab so the workspace is usable immediately.
                self.sessions.push(Session {
                    id,
                    profile,
                    adapter,
                    nav: Default::default(),
                    tabs: vec![WorkspaceTab::query(1)],
                    active_tab: 0,
                    next_tab_id: 2,
                    current_catalog: String::new(),
                    current_database: String::new(),
                    field_cache: Default::default(),
                    fields_requested: Default::default(),
                });
                self.active_session = self.sessions.len() - 1;
                self.page = super::state::Page::Workspace;
                self.focus = super::state::Focus::Sidebar;
                self.set_status(StatusKind::Success, "connected");
                self.load_databases(id);
            }
            AsyncMsg::ConnectFailed { profile_name, message } => {
                self.tasks.finish();
                self.set_status(StatusKind::Error, format!("{profile_name}: {message}"));
            }
            AsyncMsg::Databases { session_id, databases, catalogs } => {
                self.tasks.finish();
                if let Some(s) = self.session_by_id(session_id) {
                    s.nav.databases = databases;
                    s.nav.catalogs = catalogs;
                    s.nav.cursor = s.nav.cursor.min(1);
                }
            }
            AsyncMsg::CatalogDatabases { session_id, catalog, databases } => {
                self.tasks.finish();
                if let Some(s) = self.session_by_id(session_id) {
                    s.nav.catalog_databases.insert(catalog, databases);
                }
            }
            AsyncMsg::Objects { session_id, key, objects } => {
                self.tasks.finish();
                if let Some(s) = self.session_by_id(session_id) {
                    s.nav.db_objects.insert(key, objects);
                }
            }
            AsyncMsg::Preview { session_id, tab_id, offset, result } => {
                self.tasks.finish();
                if let Some(s) = self.session_by_id(session_id) {
                    if let Some(tab) = s.tabs.iter_mut().find(|t| t.id == tab_id) {
                        match result {
                            Ok(set) => {
                                tab.preview_offset = offset;
                                tab.preview_has_more = set.has_more;
                                tab.error = None;
                                tab.view = Default::default();
                                tab.clear_result_search();
                                tab.result = Some(set);
                            }
                            Err(e) => tab.error = Some(e),
                        }
                    }
                }
            }
            AsyncMsg::QueryResult {
                session_id,
                tab_id,
                run_id,
                statement,
                profile_id,
                driver,
                database,
                result,
            } => {
                self.tasks.finish();
                let ok = result.is_ok();
                let err_text = result.as_ref().err().cloned().unwrap_or_default();
                let mut applied = false;
                if let Some(s) = self.session_by_id(session_id) {
                    if let Some(tab) = s.tabs.iter_mut().find(|t| t.id == tab_id) {
                        applied = apply_query_result(tab, run_id, result);
                    }
                }
                if applied {
                    self.record_history(profile_id, driver, database, statement, ok, err_text);
                    if ok {
                        self.set_status(StatusKind::Success, "query done");
                    } else {
                        self.set_status(StatusKind::Error, "query failed");
                    }
                }
            }
            AsyncMsg::TestResult { profile_name, result } => {
                self.tasks.finish();
                match result {
                    Ok(()) => self.set_status(StatusKind::Success, format!("{profile_name}: ok")),
                    Err(e) => self.set_status(StatusKind::Error, format!("{profile_name}: {e}")),
                }
            }
            AsyncMsg::AiReply { result } => {
                self.apply_ai_reply(result);
            }
            AsyncMsg::Metadata { session_id, tab_id, result } => {
                self.tasks.finish();
                if let Some(s) = self.session_by_id(session_id) {
                    if let Some(tab) = s.tabs.iter_mut().find(|t| t.id == tab_id) {
                        match result {
                            Ok(meta) => {
                                tab.error = None;
                                tab.metadata = Some(meta);
                            }
                            Err(e) => {
                                tab.metadata_mode = false;
                                tab.error = Some(e);
                            }
                        }
                    }
                }
            }
            AsyncMsg::Fields { session_id, table, fields } => {
                // Background column fetch for editor completion (no spinner noise).
                if let Some(s) = self.session_by_id(session_id) {
                    s.field_cache.insert(table, fields);
                }
            }
            AsyncMsg::Cancelled => {
                self.tasks.finish();
                self.ai.pending = false;
                self.set_status(StatusKind::Warning, "cancelled");
            }
            AsyncMsg::Error { context, message } => {
                self.tasks.finish();
                if context == "timeout" {
                    self.set_status(StatusKind::Error, message);
                } else {
                    self.error_box = Some(message);
                }
            }
        }
    }
}

/// Replace the token at `[start, cursor)` on `row` with `item`, moving the cursor
/// to the end of the inserted text. Rune-safe (contract §1).
fn apply_completion(tab: &mut WsTab, row: usize, start: usize, item: &str) {
    let line = &mut tab.buffer[row];
    let start_b = line.char_indices().nth(start).map(|(b, _)| b).unwrap_or(line.len());
    let end_b = line
        .char_indices()
        .nth(tab.cursor_col)
        .map(|(b, _)| b)
        .unwrap_or(line.len());
    let (start_b, end_b) = if start_b <= end_b { (start_b, end_b) } else { (start_b, start_b) };
    line.replace_range(start_b..end_b, item);
    tab.cursor_col = start + item.chars().count();
}

fn apply_query_result(
    tab: &mut WorkspaceTab,
    run_id: u64,
    result: Result<crate::result::Set, String>,
) -> bool {
    if run_id != tab.query_run_id {
        return false;
    }
    match result {
        Ok(set) => {
            tab.error = None;
            tab.view = Default::default();
            tab.clear_result_search();
            tab.result = Some(set);
        }
        Err(e) => tab.error = Some(e),
    }
    true
}

/// Box-drawing / block glyphs (frames, scrollbars) that should be stripped from
/// a copied selection so only real content survives.
fn is_box_drawing(ch: char) -> bool {
    matches!(ch, '\u{2500}'..='\u{259F}')
}

/// Extract a rectangular region of `frame_lines` between cells `a` and `b`,
/// stripping box-drawing frame glyphs and trailing spaces per row (contract §11).
pub fn extract_selection(frame_lines: &[String], a: (u16, u16), b: (u16, u16)) -> String {
    let (x0, x1) = (a.0.min(b.0) as usize, a.0.max(b.0) as usize);
    let (y0, y1) = (a.1.min(b.1) as usize, a.1.max(b.1) as usize);
    let mut out = String::new();
    for y in y0..=y1 {
        let Some(line) = frame_lines.get(y) else { continue };
        let chars: Vec<char> = line.chars().collect();
        let row: String = chars
            .iter()
            .take(x1 + 1)
            .skip(x0)
            .filter(|c| !is_box_drawing(**c))
            .collect();
        out.push_str(row.trim_end());
        out.push('\n');
    }
    out.trim_end_matches('\n').to_string()
}

#[cfg(test)]
mod selection_tests {
    use super::extract_selection;

    #[test]
    fn strips_box_drawing_and_trailing_spaces() {
        let lines = vec![
            "│ hello     │".to_string(),
            "│ world     │".to_string(),
        ];
        // Select columns 0..=12 across both rows.
        let text = extract_selection(&lines, (0, 0), (12, 1));
        assert_eq!(text, " hello\n world");
    }

    #[test]
    fn single_row_partial_columns() {
        let lines = vec!["abcdef".to_string()];
        assert_eq!(extract_selection(&lines, (1, 0), (3, 0)), "bcd");
    }
}

#[cfg(test)]
mod query_result_tests {
    use super::{apply_query_result, blocking_query_issue};
    use crate::app::state::WorkspaceTab;
    use crate::config::Driver;
    use crate::result::{Column, Row, Set, Table};
    use crate::validate::Severity;
    use serde_json::json;

    fn one_cell_set(value: i64) -> Set {
        Set {
            table: Some(Table {
                columns: vec![Column {
                    name: "value".into(),
                    type_: String::new(),
                }],
                rows: vec![Row {
                    values: vec![json!(value)],
                }],
            }),
            ..Default::default()
        }
    }

    #[test]
    fn stale_query_result_does_not_replace_current_tab_result() {
        let mut tab = WorkspaceTab::query(1);
        tab.query_run_id = 2;
        tab.result = Some(one_cell_set(2));

        let applied = apply_query_result(&mut tab, 1, Ok(one_cell_set(1)));

        assert!(!applied, "stale query result should be ignored");
        let table = tab.result.as_ref().and_then(|s| s.table.as_ref()).unwrap();
        assert_eq!(table.cell_string(0, 0), "2");
    }

    #[test]
    fn mongo_validation_errors_are_blocking_query_issues() {
        let issue = blocking_query_issue(Driver::Mongo, r#"db.users.find({profile.name: "Ada"})"#).unwrap();

        assert_eq!(issue.severity, Severity::Error);
        assert!(issue.message.contains("profile.name"));
        assert!(blocking_query_issue(Driver::Mysql, "select 1").is_none());
    }
}

/// Command-line (`:`) completion candidates: only commands `run_command`
/// actually handles, so accepting one never yields "unknown command".
const COMMAND_SUGGESTIONS: &[(&str, &str)] = &[
    ("help", "open help"),
    ("new", "create connection"),
    ("edit", "edit connection"),
    ("delete", "delete selected item"),
    ("open", "open connection or object"),
    ("test", "test connection"),
    ("connections", "show connections"),
    ("query", "create query tab"),
    ("history", "show history"),
    ("refresh", "refresh active view"),
    ("back", "go back"),
    ("next", "next page"),
    ("prev", "previous page"),
    ("export", "export result: csv|json [path]"),
    ("copy", "copy result: csv|json"),
    ("timeout", "query timeout seconds"),
    ("q", "quit"),
];

/// Command candidates whose name prefixes `token` (lowercased), capped at 5.
/// Pure → unit-testable without an `App`.
fn command_suggestions(token: &str) -> Vec<crate::suggest::Suggestion> {
    COMMAND_SUGGESTIONS
        .iter()
        .filter(|(value, _)| token.is_empty() || value.starts_with(token))
        .take(5)
        .map(|(value, detail)| crate::suggest::Suggestion {
            value: value.to_string(),
            detail: detail.to_string(),
            ..Default::default()
        })
        .collect()
}

/// Replace the identifier run at the end of `s` with `value`. When `s` ends in
/// whitespace (empty token) the value is appended. Mirrors Go's
/// `replaceCurrentToken` for the single-line, cursor-at-end command input.
fn replace_last_token(s: &str, value: &str) -> String {
    let start = s
        .char_indices()
        .rev()
        .take_while(|(_, c)| c.is_ascii_alphanumeric() || *c == '_' || *c == '$')
        .last()
        .map(|(i, _)| i)
        .unwrap_or(s.len());
    format!("{}{}", &s[..start], value)
}

/// Compute live completion candidates for the text `prefix` before the cursor.
/// Returns empty when there is no trigger (no token and not just after a `.`) or
/// the only candidate equals what's already typed. Pure → unit-testable.
pub fn live_completion_items(
    driver: crate::config::Driver,
    objects: &[String],
    fields: &[crate::suggest::Field],
    prefix: &str,
) -> Vec<String> {
    let token = crate::suggest::current_token(prefix);
    let trigger = !token.is_empty() || prefix.trim_end().ends_with('.');
    if !trigger {
        return Vec::new();
    }
    let ctx = crate::suggest::Context {
        driver: Some(driver),
        input: prefix.to_string(),
        cursor: prefix.len(),
        objects: objects.to_vec(),
        fields: fields.to_vec(),
        ..Default::default()
    };
    let mut items: Vec<String> = crate::suggest::suggest(&ctx).into_iter().map(|s| s.value).collect();
    items.dedup();
    items.truncate(40);
    if items.len() == 1 && items[0] == token {
        return Vec::new();
    }
    items
}

use once_cell::sync::Lazy;
use regex::Regex;

static TABLE_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"(?i)\b(?:from|join)\s+([\w$.`"]+)"#).unwrap());

/// Table names referenced in FROM/JOIN clauses (last dotted segment, unquoted).
fn referenced_tables(sql: &str) -> Vec<String> {
    let mut out = Vec::new();
    for c in TABLE_RE.captures_iter(sql) {
        let raw = c[1].trim_matches(|ch| ch == '`' || ch == '"');
        let name = raw.rsplit('.').next().unwrap_or(raw).trim_matches(|ch| ch == '`' || ch == '"');
        if !name.is_empty() && !out.contains(&name.to_string()) {
            out.push(name.to_string());
        }
    }
    out
}

static MONGO_COLL_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"\bdb\.([A-Za-z_][\w$]*)\.").unwrap());

/// Collection names referenced as `db.<collection>.` in a mongosh command, so
/// their fields can be preloaded for `find({<field>` completion.
fn referenced_collections(text: &str) -> Vec<String> {
    let mut out = Vec::new();
    for c in MONGO_COLL_RE.captures_iter(text) {
        let name = c[1].to_string();
        if !out.contains(&name) {
            out.push(name);
        }
    }
    out
}

#[cfg(test)]
mod command_completion_tests {
    use super::{command_suggestions, replace_last_token};

    #[test]
    fn suggestions_filter_by_prefix() {
        let ex = command_suggestions("ex");
        assert!(ex.iter().any(|s| s.value == "export"));
        assert!(!ex.iter().any(|s| s.value == "edit"));

        // Newly wired commands are reachable by prefix.
        assert!(command_suggestions("con").iter().any(|s| s.value == "connections"));
        assert!(command_suggestions("q").iter().any(|s| s.value == "q"));

        // Empty token lists candidates (capped at 5).
        let all = command_suggestions("");
        assert!(!all.is_empty() && all.len() <= 5);

        // No match yields an empty list.
        assert!(command_suggestions("zzz").is_empty());
    }

    #[test]
    fn replace_last_token_swaps_or_appends() {
        assert_eq!(replace_last_token("exp", "export"), "export");
        assert_eq!(replace_last_token("", "help"), "help");
        // Cursor after whitespace: append a second word instead of replacing.
        assert_eq!(replace_last_token("export c", "copy"), "export copy");
    }
}
