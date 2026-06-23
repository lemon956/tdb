//! AI assistant panel logic: open/close, slash commands, prompt building, and
//! routing replies. Mirrors the Go AI panel (Ctrl+K), driver-aware prompts, and
//! SQL extraction with Ctrl+Y insert.

use std::sync::Arc;

use crate::ai::{self, Provider};

use super::state::{AiMsg, App, Focus, TabKind};
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
