//! AI assistant panel logic: open/close, slash commands, prompt building, and
//! routing replies. Mirrors the Go AI panel (Ctrl+K), driver-aware prompts, and
//! SQL extraction with Ctrl+Y insert.

use std::sync::Arc;

use crate::ai::{self, Provider};

use super::state::{scope_key, AiMsg, App, Focus, TabKind};
use super::task::AsyncMsg;
use super::theme::StatusKind;

impl App {
    pub fn toggle_ai(&mut self) {
        if self.ai.open {
            self.close_ai();
            return;
        }
        if self.active_session().is_none() {
            self.set_status(StatusKind::Warning, "open a connection first");
            return;
        }
        if self.ai.provider_name.is_empty() {
            let preferred = self.vault.ai_provider.clone();
            match Provider::detect(&preferred) {
                Ok(p) => self.ai.provider_name = p.name,
                Err(e) => {
                    self.set_status(StatusKind::Error, e.to_string());
                    return;
                }
            }
        }
        self.ai.open = true;
        self.previous_focus = self.focus;
        self.focus = Focus::Overlay;
        // Preload the current database's tables so `@`-mention has candidates even
        // if the user never expanded the database (mirrors Go's openAIChatModal).
        self.ensure_current_objects_loaded();
    }

    pub fn close_ai(&mut self) {
        self.ai.open = false;
        self.tasks.cancel_all();
        self.focus = if self.sessions.is_empty() {
            Focus::Command
        } else {
            self.previous_focus
        };
    }

    /// Handle the current AI input: a slash command or a question.
    pub fn ai_submit(&mut self) {
        let text = self.ai.input.trim().to_string();
        if text.is_empty() {
            return;
        }
        self.ai.input.clear();
        self.ai.mention_items.clear();
        self.ai.mention_index = 0;
        if let Some(cmd) = text.strip_prefix('/') {
            self.ai_slash(cmd);
            return;
        }
        self.ai.messages.push(AiMsg {
            role: "you".into(),
            text: text.clone(),
        });
        let prompt = self.build_ai_prompt(&text);
        let provider_name = self.ai.provider_name.clone();
        let preferred = self.vault.ai_provider.clone();
        self.ai.pending = true;
        self.tasks.spawn_ai("Asking AI", async move {
            let provider = match Provider::detect(if provider_name.is_empty() {
                &preferred
            } else {
                &provider_name
            }) {
                Ok(p) => p,
                Err(e) => return AsyncMsg::AiReply { result: Err(e.to_string()) },
            };
            let provider = Arc::new(provider);
            let result = provider.ask(&prompt).await.map_err(|e| e.to_string());
            AsyncMsg::AiReply { result }
        });
    }

    fn ai_slash(&mut self, cmd: &str) {
        let parts: Vec<&str> = cmd.split_whitespace().collect();
        match parts.first().copied() {
            Some("new") | Some("clear") => {
                self.ai.messages.clear();
                self.ai.last_sql.clear();
                self.set_status(StatusKind::Info, "new conversation");
            }
            Some("help") => {
                self.ai.messages.push(AiMsg {
                    role: "ai".into(),
                    text: "/new clear conversation · /provider <claude|codex> · /help · Ctrl+Y insert SQL · Esc close".into(),
                });
            }
            Some("provider") | Some("model") => {
                if let Some(name) = parts.get(1) {
                    self.ai.provider_name = name.to_string();
                    self.vault.ai_provider = name.to_string();
                    let _ = self.store.save(&self.master, &self.vault);
                    self.set_status(StatusKind::Success, format!("provider = {name}"));
                } else {
                    let avail = ai::available().join(", ");
                    self.ai.messages.push(AiMsg {
                        role: "ai".into(),
                        text: format!("available providers: {avail}"),
                    });
                }
            }
            _ => self.set_status(StatusKind::Warning, format!("unknown command: /{cmd}")),
        }
    }

    /// Refresh the `@`-mention completion popup from the current input. The
    /// candidates are the current database's objects whose name contains the
    /// trailing `@<prefix>`; no `@` token hides the popup.
    pub fn ai_update_mention(&mut self) {
        let Some(prefix) = ai_mention_prefix(&self.ai.input) else {
            self.ai.mention_items.clear();
            self.ai.mention_index = 0;
            return;
        };
        let items: Vec<crate::suggest::Suggestion> = self
            .active_session()
            .map(|s| {
                let key = scope_key(&s.current_catalog, &s.current_database);
                s.nav
                    .db_objects
                    .get(&key)
                    .map(|objs| {
                        objs.iter()
                            .filter(|o| prefix.is_empty() || o.name.to_lowercase().contains(&prefix))
                            .take(20)
                            .map(|o| crate::suggest::Suggestion {
                                value: o.name.clone(),
                                detail: object_type_label(o.type_).to_string(),
                                ..Default::default()
                            })
                            .collect()
                    })
                    .unwrap_or_default()
            })
            .unwrap_or_default();
        self.ai.mention_index = 0;
        self.ai.mention_items = items;
    }

    pub fn ai_mention_move(&mut self, forward: bool) {
        let n = self.ai.mention_items.len();
        if n == 0 {
            return;
        }
        self.ai.mention_index = if forward {
            (self.ai.mention_index + 1) % n
        } else {
            (self.ai.mention_index + n - 1) % n
        };
    }

    /// Accept the highlighted `@`-mention: replace the trailing `@<prefix>` with
    /// `@<table> ` and prefetch that table's columns for the prompt schema.
    pub fn accept_ai_mention(&mut self) {
        let Some(item) = self.ai.mention_items.get(self.ai.mention_index).cloned() else {
            return;
        };
        let at = mention_at_index(&self.ai.input);
        let mut new_input = self.ai.input[..at].to_string();
        new_input.push('@');
        new_input.push_str(&item.value);
        new_input.push(' ');
        self.ai.input = new_input;
        self.ai.mention_items.clear();
        self.ai.mention_index = 0;
        self.prefetch_table_fields(&item.value);
    }

    /// Kick off a background column fetch for `table` (cached in `field_cache`),
    /// so the next prompt can include its schema.
    fn prefetch_table_fields(&mut self, table: &str) {
        let info = self.active_session().map(|s| {
            (s.id, s.adapter.clone(), s.current_catalog.clone(), s.current_database.clone())
        });
        if let Some((sid, adapter, cat, db)) = info {
            self.load_fields(sid, adapter, cat, db, table.to_string());
        }
    }

    /// Build a driver-aware prompt with conversation history and DB context.
    fn build_ai_prompt(&self, question: &str) -> String {
        let mut p = String::new();
        if let Some(s) = self.active_session() {
            let driver = s.profile.driver.as_str();
            p.push_str(&format!(
                "You are a database assistant for a {driver} database. Answer with a single SQL (or shell) statement in a fenced ```sql code block. "
            ));
            if driver == "doris" || driver == "mysql" {
                // Partition guardrail (contract §8): steer toward filtered queries.
                p.push_str("If a table is partitioned, include a WHERE filter on the partition column to avoid full scans. ");
            }
            if !s.current_database.is_empty() {
                p.push_str(&format!("Current database: {}. ", s.current_database));
            }
            // Available tables in the current scope.
            let key = scope_key(&s.current_catalog, &s.current_database);
            if let Some(objs) = s.nav.db_objects.get(&key) {
                if !objs.is_empty() {
                    let names: Vec<&str> = objs.iter().take(200).map(|o| o.name.as_str()).collect();
                    p.push_str(&format!("Tables: {}. ", names.join(", ")));
                }
            }
            // Schema of any @-mentioned tables (from cached columns).
            let mut seen = std::collections::HashSet::new();
            for tok in self.ai.messages.iter().filter(|m| m.role == "you").flat_map(|m| mention_tokens(&m.text)) {
                let tl = tok.to_lowercase();
                if !seen.insert(tl.clone()) {
                    continue;
                }
                if let Some(fields) = s.field_cache.get(&tl) {
                    if !fields.is_empty() {
                        let cols: Vec<String> =
                            fields.iter().map(|f| format!("{} {}", f.name, f.type_)).collect();
                        p.push_str(&format!("Columns of {tok}: {}. ", cols.join(", ")));
                    }
                }
            }
        }
        p.push('\n');
        for m in &self.ai.messages {
            let who = match m.role.as_str() {
                "you" => "User",
                _ => "Assistant",
            };
            p.push_str(&format!("{who}: {}\n", m.text));
        }
        let _ = question; // already pushed as the last user message
        p.push_str("Assistant:");
        p
    }

    pub fn apply_ai_reply(&mut self, result: Result<String, String>) {
        self.tasks.finish();
        self.ai.pending = false;
        match result {
            Ok(reply) => {
                self.ai.last_sql = ai::extract_blocks(&reply, &["sql"]);
                self.ai.messages.push(AiMsg {
                    role: "ai".into(),
                    text: reply,
                });
            }
            Err(e) => self.ai.messages.push(AiMsg {
                role: "err".into(),
                text: e,
            }),
        }
    }

    /// Ctrl+Y: insert the first extracted SQL block into the active query tab.
    pub fn ai_insert_sql(&mut self) {
        let Some(sql) = self.ai.last_sql.first().cloned() else {
            self.set_status(StatusKind::Warning, "no SQL to insert");
            return;
        };
        // Ensure a query tab exists.
        if self
            .active_session()
            .and_then(|s| s.active_tab())
            .map(|t| t.kind != TabKind::Query)
            .unwrap_or(true)
        {
            self.new_query_tab();
        }
        if let Some(tab) = self.active_session_mut().and_then(|s| s.active_tab_mut()) {
            tab.buffer = sql.split('\n').map(|l| l.to_string()).collect();
            if tab.buffer.is_empty() {
                tab.buffer.push(String::new());
            }
            tab.cursor_row = tab.buffer.len() - 1;
            tab.cursor_col = tab.buffer[tab.cursor_row].chars().count();
        }
        self.close_ai();
        self.set_status(StatusKind::Success, "SQL inserted");
    }
}

fn object_type_label(t: crate::db::ObjectType) -> &'static str {
    match t {
        crate::db::ObjectType::Table => "table",
        crate::db::ObjectType::View => "view",
        crate::db::ObjectType::Collection => "collection",
        crate::db::ObjectType::Key => "key",
    }
}

/// True when `b` continues an identifier (table-name) token.
fn is_ident_byte(b: u8) -> bool {
    b.is_ascii_alphanumeric() || b == b'_' || b == b'$'
}

/// Byte index where the trailing identifier run starts (the char after it would
/// be the `@`). Used to splice in an accepted mention.
fn mention_at_index(input: &str) -> usize {
    let bytes = input.as_bytes();
    let mut i = input.len();
    while i > 0 && is_ident_byte(bytes[i - 1]) {
        i -= 1;
    }
    i.saturating_sub(1) // step over the '@'
}

/// Lowercased prefix when the input ends in an `@<word>` mention, else `None`.
fn ai_mention_prefix(input: &str) -> Option<String> {
    let bytes = input.as_bytes();
    let mut i = input.len();
    while i > 0 && is_ident_byte(bytes[i - 1]) {
        i -= 1;
    }
    if i > 0 && bytes[i - 1] == b'@' {
        Some(input[i..].to_lowercase())
    } else {
        None
    }
}

/// All `@<word>` mention tokens in `text` (the `@` stripped).
fn mention_tokens(text: &str) -> Vec<String> {
    let bytes = text.as_bytes();
    let mut out = Vec::new();
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'@' {
            let start = i + 1;
            let mut j = start;
            while j < bytes.len() && is_ident_byte(bytes[j]) {
                j += 1;
            }
            if j > start {
                out.push(text[start..j].to_string());
            }
            i = j;
        } else {
            i += 1;
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::{ai_mention_prefix, mention_at_index, mention_tokens};

    #[test]
    fn mention_prefix_detects_trailing_at_token() {
        assert_eq!(ai_mention_prefix("show @Us"), Some("us".to_string()));
        assert_eq!(ai_mention_prefix("show @"), Some("".to_string()));
        assert_eq!(ai_mention_prefix("show users"), None);
        assert_eq!(ai_mention_prefix("@orders x"), None); // not trailing
    }

    #[test]
    fn mention_at_index_points_at_the_at_sign() {
        // "ask @us" → '@' at byte 4.
        assert_eq!(mention_at_index("ask @us"), 4);
        // Bare trailing '@'.
        assert_eq!(mention_at_index("ask @"), 4);
    }

    #[test]
    fn mention_tokens_extracts_all() {
        assert_eq!(mention_tokens("join @a and @b_2"), vec!["a".to_string(), "b_2".to_string()]);
        assert!(mention_tokens("no mentions here").is_empty());
    }
}
