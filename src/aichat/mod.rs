//! AI conversation persistence. Port of `internal/aichat`. Sessions are keyed by
//! a unique id (conversations are not idempotent) and scoped to a connection +
//! database. Stored as plain JSON, 0o600.

use std::path::{Path, PathBuf};

use anyhow::{Context, Result};
use chrono::{DateTime, Utc};
use rand::RngCore;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Message {
    pub role: String, // "you" | "ai" | "err"
    pub text: String,
    pub at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Session {
    pub id: String,
    pub profile_id: String,
    pub scope: String,
    pub db_label: String,
    pub title: String,
    #[serde(default)]
    pub messages: Vec<Message>,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

pub struct Store {
    path: PathBuf,
}

/// Short random hex id for a new session.
pub fn new_id() -> String {
    let mut b = [0u8; 8];
    rand::thread_rng().fill_bytes(&mut b);
    b.iter().map(|x| format!("{x:02x}")).collect()
}

impl Store {
    pub fn new(path: impl Into<PathBuf>) -> Store {
        Store { path: path.into() }
    }

    /// All sessions, newest `updated_at` first.
    pub fn load(&self) -> Result<Vec<Session>> {
        let mut sessions = self.read()?;
        sessions.sort_by(|a, b| b.updated_at.cmp(&a.updated_at));
        Ok(sessions)
    }

    pub fn upsert(&self, session: Session) -> Result<()> {
        let mut sessions = self.read()?;
        if let Some(slot) = sessions.iter_mut().find(|s| s.id == session.id) {
            *slot = session;
        } else {
            sessions.push(session);
        }
        self.write(&sessions)
    }

    pub fn delete(&self, id: &str) -> Result<()> {
        let mut sessions = self.read()?;
        sessions.retain(|s| s.id != id);
        self.write(&sessions)
    }

    fn read(&self) -> Result<Vec<Session>> {
        match std::fs::read(&self.path) {
            Ok(raw) if raw.is_empty() => Ok(Vec::new()),
            Ok(raw) => serde_json::from_slice(&raw).context("parse ai chat history"),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(Vec::new()),
            Err(e) => Err(anyhow::Error::from(e).context("read ai chat history")),
        }
    }

    fn write(&self, sessions: &[Session]) -> Result<()> {
        if let Some(dir) = self.path.parent() {
            std::fs::create_dir_all(dir).ok();
        }
        let raw = serde_json::to_vec_pretty(sessions).context("marshal ai chat history")?;
        write_0600(&self.path, &raw).context("write ai chat history")
    }
}

#[cfg(unix)]
fn write_0600(path: &Path, data: &[u8]) -> std::io::Result<()> {
    use std::io::Write;
    use std::os::unix::fs::OpenOptionsExt;
    let mut f = std::fs::OpenOptions::new()
        .write(true)
        .create(true)
        .truncate(true)
        .mode(0o600)
        .open(path)?;
    f.write_all(data)
}

#[cfg(not(unix))]
fn write_0600(path: &Path, data: &[u8]) -> std::io::Result<()> {
    std::fs::write(path, data)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sess(id: &str, at: i64) -> Session {
        let t = DateTime::from_timestamp(at, 0).unwrap();
        Session {
            id: id.into(),
            profile_id: "p".into(),
            scope: "db".into(),
            db_label: "db".into(),
            title: "t".into(),
            messages: vec![],
            created_at: t,
            updated_at: t,
        }
    }

    #[test]
    fn upsert_load_delete() {
        let dir = tempfile::tempdir().unwrap();
        let store = Store::new(dir.path().join("ai.json"));
        store.upsert(sess("a", 100)).unwrap();
        store.upsert(sess("b", 200)).unwrap();
        let all = store.load().unwrap();
        assert_eq!(all.len(), 2);
        assert_eq!(all[0].id, "b"); // newest first
        store.delete("a").unwrap();
        assert_eq!(store.load().unwrap().len(), 1);
    }

    #[test]
    fn new_id_is_16_hex() {
        let id = new_id();
        assert_eq!(id.len(), 16);
        assert!(id.chars().all(|c| c.is_ascii_hexdigit()));
    }
}
