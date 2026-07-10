//! Async task orchestration. Mirrors the Go `runAsync` / `runAIAsync` model:
//! a background tokio task runs a DB/AI operation and sends a typed result back
//! to the event loop, while the footer shows a spinner. A
//! [`CancellationToken`] makes the in-flight op cancellable with Esc, with a
//! separate slot for AI so a DB op never cancels an AI request (contract §9).

use std::future::Future;
use std::sync::Arc;

use tokio::sync::mpsc::UnboundedSender;
use tokio_util::sync::CancellationToken;

use crate::config::Profile;
use crate::db::{Adapter, Object};
use crate::result::Set;

/// Spinner animation frames (Braille), 90ms per frame.
pub const SPINNER_FRAMES: &[&str] = &["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];

/// Default query timeout in seconds (0 = no deadline, cancel-only).
pub const DEFAULT_QUERY_TIMEOUT: u64 = 30;
/// AI timeout must be far longer than the query timeout — agentic CLIs are slow.
pub const AI_TIMEOUT: u64 = 300;

#[derive(Default)]
pub struct TaskState {
    pub active: bool,
    pub label: String,
    pub frame: usize,
    pub cancel: Option<CancellationToken>,
    pub ai_cancel: Option<CancellationToken>,
    pub query_timeout: u64,
    pub tx: Option<UnboundedSender<AppMsg>>,
}

impl TaskState {
    pub fn init(tx: UnboundedSender<AppMsg>) -> TaskState {
        TaskState {
            query_timeout: DEFAULT_QUERY_TIMEOUT,
            tx: Some(tx),
            ..Default::default()
        }
    }

    pub fn tick(&mut self) {
        if self.active {
            self.frame = (self.frame + 1) % SPINNER_FRAMES.len();
        }
    }

    pub fn spinner_frame(&self) -> &'static str {
        SPINNER_FRAMES[self.frame % SPINNER_FRAMES.len()]
    }

    /// Spawn a DB operation. The future races against the cancel token and an
    /// optional query-timeout deadline; whichever finishes first wins.
    pub fn spawn_db<Fut>(&mut self, label: impl Into<String>, fut: Fut)
    where
        Fut: Future<Output = AsyncMsg> + Send + 'static,
    {
        let token = CancellationToken::new();
        self.cancel = Some(token.clone());
        self.active = true;
        self.label = label.into();
        let timeout = self.query_timeout;
        let Some(tx) = self.tx.clone() else { return };
        tokio::spawn(async move {
            let msg = race(fut, token, timeout).await;
            let _ = tx.send(AppMsg::Async(msg));
        });
    }

    /// Spawn an AI operation on the dedicated AI cancel slot with a long timeout.
    pub fn spawn_ai<Fut>(&mut self, label: impl Into<String>, fut: Fut)
    where
        Fut: Future<Output = AsyncMsg> + Send + 'static,
    {
        let token = CancellationToken::new();
        self.ai_cancel = Some(token.clone());
        self.active = true;
        self.label = label.into();
        let Some(tx) = self.tx.clone() else { return };
        tokio::spawn(async move {
            let msg = race(fut, token, AI_TIMEOUT).await;
            let _ = tx.send(AppMsg::Async(msg));
        });
    }

    /// Cancel both the DB and AI in-flight ops (Esc).
    pub fn cancel_all(&mut self) -> bool {
        let mut cancelled = false;
        if let Some(t) = self.cancel.take() {
            t.cancel();
            cancelled = true;
        }
        if let Some(t) = self.ai_cancel.take() {
            t.cancel();
            cancelled = true;
        }
        if cancelled {
            self.active = false;
        }
        cancelled
    }

    pub fn finish(&mut self) {
        self.active = false;
        self.cancel = None;
    }
}

async fn race<Fut>(fut: Fut, token: CancellationToken, timeout: u64) -> AsyncMsg
where
    Fut: Future<Output = AsyncMsg> + Send + 'static,
{
    if timeout > 0 {
        tokio::select! {
            m = fut => m,
            _ = token.cancelled() => AsyncMsg::Cancelled,
            _ = tokio::time::sleep(std::time::Duration::from_secs(timeout)) => {
                AsyncMsg::Error { context: "timeout".into(), message: "operation timed out".into() }
            }
        }
    } else {
        tokio::select! {
            m = fut => m,
            _ = token.cancelled() => AsyncMsg::Cancelled,
        }
    }
}

/// Top-level message delivered to the event loop over the async channel.
pub enum AppMsg {
    Async(AsyncMsg),
}

/// Typed async results. Each carries the identifiers needed to route it back to
/// the right session/tab (replacing Go's closure-apply pattern).
pub enum AsyncMsg {
    Connected {
        profile: Profile,
        adapter: Arc<Adapter>,
    },
    ConnectFailed {
        profile_name: String,
        message: String,
    },
    Databases {
        session_id: u64,
        databases: Vec<String>,
        catalogs: Vec<String>,
    },
    CatalogDatabases {
        session_id: u64,
        catalog: String,
        databases: Vec<String>,
    },
    Objects {
        session_id: u64,
        key: String,
        objects: Vec<Object>,
    },
    Preview {
        session_id: u64,
        tab_id: u64,
        offset: i32,
        result: Result<Set, String>,
    },
    QueryResult {
        session_id: u64,
        tab_id: u64,
        run_id: u64,
        statement: String,
        profile_id: String,
        driver: String,
        database: String,
        result: Result<Set, String>,
    },
    TestResult {
        profile_name: String,
        result: Result<(), String>,
    },
    AiReply {
        result: Result<String, String>,
    },
    Metadata {
        session_id: u64,
        tab_id: u64,
        result: Result<crate::db::ObjectMetadata, String>,
    },
    Fields {
        session_id: u64,
        table: String,
        fields: Vec<crate::suggest::Field>,
    },
    Cancelled,
    Error {
        context: String,
        message: String,
    },
}
