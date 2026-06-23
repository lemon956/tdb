//! Query history with literal-insensitive fingerprint de-duplication. Port of
//! `internal/history`. Identical queries (ignoring literal values) collapse to
//! one hot entry; the original statement text is kept for display.

mod fingerprint;

pub use fingerprint::fingerprint;

use std::collections::HashMap;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Entry {
    pub id: String,
    pub profile_id: String,
    pub driver: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub database: String,
    pub action: String,
    pub statement: String,
    pub status: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub error: String,
    #[serde(default)]
    pub affected_rows: i64,
    #[serde(default)]
    pub duration_millis: i64,
    pub started_at: DateTime<Utc>,
    #[serde(default, skip_serializing_if = "is_zero")]
    pub execution_count: i64,
}

fn is_zero(n: &i64) -> bool {
    *n == 0
}

pub struct Store {
    path: PathBuf,
}

impl Store {
    pub fn new(path: impl Into<PathBuf>) -> Store {
        Store { path: path.into() }
    }

    pub fn append(&self, mut entry: Entry, now: DateTime<Utc>) -> Result<()> {
        let mut entries = self.read_raw()?;
        if entry.execution_count <= 0 {
            entry.execution_count = 1;
        }
        if entry.started_at.timestamp() == 0 {
            entry.started_at = now;
        }
        entries.push(entry);
        self.write(&merge_entries(entries))
    }

    /// Merged, most-recent-first entries for a profile (empty = all).
    pub fn list(&self, profile_id: &str, limit: usize) -> Result<Vec<Entry>> {
        let mut entries: Vec<Entry> = self
            .read()?
            .into_iter()
            .filter(|e| profile_id.is_empty() || e.profile_id == profile_id)
            .collect();
        entries.sort_by(|a, b| b.started_at.cmp(&a.started_at));
        if limit > 0 && entries.len() > limit {
            entries.truncate(limit);
        }
        Ok(entries)
    }

    fn read(&self) -> Result<Vec<Entry>> {
        Ok(merge_entries(self.read_raw()?))
    }

    fn read_raw(&self) -> Result<Vec<Entry>> {
        match std::fs::read(&self.path) {
            Ok(raw) if raw.is_empty() => Ok(Vec::new()),
            Ok(raw) => serde_json::from_slice(&raw).context("parse history"),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(Vec::new()),
            Err(e) => Err(anyhow::Error::from(e).context("read history")),
        }
    }

    fn write(&self, entries: &[Entry]) -> Result<()> {
        if let Some(dir) = self.path.parent() {
            std::fs::create_dir_all(dir).ok();
        }
        let raw = serde_json::to_vec_pretty(entries).context("marshal history")?;
        write_0600(&self.path, &raw).context("write history")
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

/// Collapse entries with the same driver/action/fingerprint, summing
/// execution_count and keeping the most recent execution's metadata.
fn merge_entries(entries: Vec<Entry>) -> Vec<Entry> {
    let mut merged: Vec<Entry> = Vec::with_capacity(entries.len());
    let mut index: HashMap<String, usize> = HashMap::new();
    for mut entry in entries {
        let count = if entry.execution_count <= 0 { 1 } else { entry.execution_count };
        let fp = fingerprint(&entry.statement);
        if fp.is_empty() {
            entry.execution_count = count;
            merged.push(entry);
            continue;
        }
        let key = format!("{}\u{0}{}\u{0}{}", entry.driver, entry.action, fp);
        if let Some(&i) = index.get(&key) {
            let total = merged[i].execution_count + count;
            if merged[i].started_at <= entry.started_at {
                entry.execution_count = total;
                merged[i] = entry;
            } else {
                merged[i].execution_count = total;
            }
        } else {
            entry.execution_count = count;
            index.insert(key, merged.len());
            merged.push(entry);
        }
    }
    merged
}

#[cfg(test)]
mod tests {
    use super::*;

    fn entry(stmt: &str, at: i64) -> Entry {
        Entry {
            id: stmt.into(),
            profile_id: "p".into(),
            driver: "mysql".into(),
            action: "query".into(),
            statement: stmt.into(),
            status: "ok".into(),
            started_at: DateTime::from_timestamp(at, 0).unwrap(),
            ..Default::default()
        }
    }

    #[test]
    fn merges_by_fingerprint_summing_count() {
        let merged = merge_entries(vec![
            entry("select * from t where id = 1", 100),
            entry("select * from t where id = 2", 200),
        ]);
        assert_eq!(merged.len(), 1);
        assert_eq!(merged[0].execution_count, 2);
        // keeps most recent metadata
        assert!(merged[0].statement.contains("id = 2"));
    }

    #[test]
    fn append_and_list_roundtrip() {
        let dir = tempfile::tempdir().unwrap();
        let store = Store::new(dir.path().join("history.json"));
        let now = DateTime::from_timestamp(1000, 0).unwrap();
        store.append(entry("select 1", 1000), now).unwrap();
        store.append(entry("select 1", 1001), now).unwrap();
        let list = store.list("p", 0).unwrap();
        assert_eq!(list.len(), 1);
        assert_eq!(list[0].execution_count, 2);
    }
}
