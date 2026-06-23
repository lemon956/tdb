//! Database adapter layer. Port of `internal/db`.
//!
//! The Go code uses an `Adapter` interface with optional `MetadataProvider` /
//! `CatalogProvider` capabilities discovered by type assertion. Here we use an
//! enum [`Adapter`] that dispatches to the concrete driver adapters; optional
//! capabilities are methods that return `None`/empty when unsupported. This
//! avoids async-trait object-safety friction while keeping the same contract.

pub mod dsn;

#[cfg(feature = "drivers")]
pub mod mongo;
#[cfg(feature = "drivers")]
pub mod redis;
#[cfg(feature = "drivers")]
pub mod sql;

use serde::{Deserialize, Serialize};

#[cfg(feature = "drivers")]
use crate::result::{MutationResult, Set};

/// Upper bound on rows materialized from any single arbitrary query, so a huge
/// SELECT cannot freeze or OOM the TUI.
pub const MAX_RESULT_ROWS: usize = 10_000;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum ObjectType {
    #[default]
    Table,
    View,
    Collection,
    Key,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Object {
    pub name: String,
    #[serde(rename = "type")]
    pub type_: ObjectType,
    /// Finer data type when `type_` alone is not enough, e.g. the Redis key
    /// data type (string/list/set/hash/zset/stream).
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub sub_type: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct MetadataField {
    pub name: String,
    #[serde(default, skip_serializing_if = "String::is_empty", rename = "type")]
    pub type_: String,
    pub nullable: bool,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub default: String,
    /// Column comment (often documents enum-like values, e.g. "1=active
    /// 2=closed"); fed to the AI so it can use real domain values.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub comment: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct MetadataIndex {
    pub name: String,
    pub columns: Vec<String>,
    pub unique: bool,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct ObjectMetadata {
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub fields: Vec<MetadataField>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub indexes: Vec<MetadataIndex>,
    #[serde(default, skip_serializing_if = "std::collections::BTreeMap::is_empty")]
    pub attributes: std::collections::BTreeMap<String, String>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Scope {
    /// Doris external catalog (empty or "internal" = built-in catalog; other
    /// drivers leave it empty). Scopes `database`.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub catalog: String,
    pub database: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub schema: String,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Target {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub catalog: String,
    pub database: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub schema: String,
    pub name: String,
    #[serde(rename = "type")]
    pub type_: ObjectType,
}

impl Target {
    /// Dotted path: catalog.database.schema.name (omitting empties).
    pub fn to_path(&self) -> String {
        let mut parts: Vec<&str> = Vec::new();
        for p in [&self.catalog, &self.database, &self.schema, &self.name] {
            if !p.is_empty() {
                parts.push(p);
            }
        }
        parts.join(".")
    }
}

#[derive(Debug, Clone, Copy)]
pub struct Page {
    pub limit: i32,
    pub offset: i32,
}

impl Page {
    /// Clamp `limit` to [1, 500] (default 100) and `offset` to >= 0.
    pub fn new(limit: i32, offset: i32) -> Page {
        let mut limit = limit;
        if limit <= 0 {
            limit = 100;
        }
        if limit > 500 {
            limit = 500;
        }
        let offset = offset.max(0);
        Page { limit, offset }
    }
}

#[derive(Debug, Clone, Default)]
pub struct Query {
    pub text: String,
    pub filter: Option<serde_json::Map<String, serde_json::Value>>,
}

#[derive(Debug, Clone, Default)]
pub struct Command {
    pub text: String,
    pub catalog: String,
    pub database: String,
}

#[derive(Debug, Clone, Default)]
pub struct Key {
    pub columns: serde_json::Map<String, serde_json::Value>,
    pub id: String,
}

/// Concrete adapter, dispatched by driver. Construct via [`crate::db::connect`].
#[cfg(feature = "drivers")]
pub enum Adapter {
    Sql(sql::SqlAdapter),
    Redis(redis::RedisAdapter),
    Mongo(mongo::MongoAdapter),
}

#[cfg(feature = "drivers")]
impl Adapter {
    pub async fn test(&self) -> anyhow::Result<()> {
        match self {
            Adapter::Sql(a) => a.test().await,
            Adapter::Redis(a) => a.test().await,
            Adapter::Mongo(a) => a.test().await,
        }
    }

    pub async fn list_databases(&self) -> anyhow::Result<Vec<String>> {
        match self {
            Adapter::Sql(a) => a.list_databases().await,
            Adapter::Redis(a) => a.list_databases().await,
            Adapter::Mongo(a) => a.list_databases().await,
        }
    }

    pub async fn list_objects(&self, scope: Scope) -> anyhow::Result<Vec<Object>> {
        match self {
            Adapter::Sql(a) => a.list_objects(scope).await,
            Adapter::Redis(a) => a.list_objects(scope).await,
            Adapter::Mongo(a) => a.list_objects(scope).await,
        }
    }

    pub async fn preview(&self, target: Target, query: Query, page: Page) -> anyhow::Result<Set> {
        match self {
            Adapter::Sql(a) => a.preview(target, query, page).await,
            Adapter::Redis(a) => a.preview(target, query, page).await,
            Adapter::Mongo(a) => a.preview(target, query, page).await,
        }
    }

    pub async fn execute(&self, cmd: Command) -> anyhow::Result<Set> {
        match self {
            Adapter::Sql(a) => a.execute(cmd).await,
            Adapter::Redis(a) => a.execute(cmd).await,
            Adapter::Mongo(a) => a.execute(cmd).await,
        }
    }

    pub async fn metadata(&self, target: Target) -> anyhow::Result<ObjectMetadata> {
        match self {
            Adapter::Sql(a) => a.metadata(target).await,
            Adapter::Redis(a) => a.metadata(target).await,
            Adapter::Mongo(a) => a.metadata(target).await,
        }
    }

    /// Doris external catalogs. Empty vec = no catalog layer (UI falls back to
    /// the flat database list).
    pub async fn list_catalogs(&self) -> anyhow::Result<Vec<String>> {
        match self {
            Adapter::Sql(a) => a.list_catalogs().await,
            _ => Ok(Vec::new()),
        }
    }

    pub async fn list_databases_in_catalog(&self, catalog: &str) -> anyhow::Result<Vec<String>> {
        match self {
            Adapter::Sql(a) => a.list_databases_in_catalog(catalog).await,
            _ => self.list_databases().await,
        }
    }

    pub async fn insert(
        &self,
        target: Target,
        values: serde_json::Map<String, serde_json::Value>,
    ) -> anyhow::Result<MutationResult> {
        match self {
            Adapter::Sql(a) => a.insert(target, values).await,
            Adapter::Redis(a) => a.insert(target, values).await,
            Adapter::Mongo(a) => a.insert(target, values).await,
        }
    }

    pub async fn update(
        &self,
        target: Target,
        key: Key,
        values: serde_json::Map<String, serde_json::Value>,
    ) -> anyhow::Result<MutationResult> {
        match self {
            Adapter::Sql(a) => a.update(target, key, values).await,
            Adapter::Redis(a) => a.update(target, key, values).await,
            Adapter::Mongo(a) => a.update(target, key, values).await,
        }
    }

    pub async fn delete(&self, target: Target, key: Key) -> anyhow::Result<MutationResult> {
        match self {
            Adapter::Sql(a) => a.delete(target, key).await,
            Adapter::Redis(a) => a.delete(target, key).await,
            Adapter::Mongo(a) => a.delete(target, key).await,
        }
    }
}

/// Connect to the database described by a profile, returning a ready adapter.
#[cfg(feature = "drivers")]
pub async fn connect(profile: &crate::config::Profile) -> anyhow::Result<Adapter> {
    use crate::config::Driver;
    match profile.driver {
        Driver::Mysql | Driver::Doris => {
            Ok(Adapter::Sql(sql::SqlAdapter::connect(profile).await?))
        }
        Driver::Redis => Ok(Adapter::Redis(redis::RedisAdapter::connect(profile).await?)),
        Driver::Mongo => Ok(Adapter::Mongo(mongo::MongoAdapter::connect(profile).await?)),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn object_sub_type_round_trips() {
        let o = Object {
            name: "k".into(),
            type_: ObjectType::Key,
            sub_type: "hash".into(),
        };
        let s = serde_json::to_string(&o).unwrap();
        assert!(s.contains("\"sub_type\":\"hash\""));
        let back: Object = serde_json::from_str(&s).unwrap();
        assert_eq!(back, o);

        // Empty sub_type is omitted.
        let o2 = Object {
            name: "t".into(),
            type_: ObjectType::Table,
            sub_type: String::new(),
        };
        let s2 = serde_json::to_string(&o2).unwrap();
        assert!(!s2.contains("sub_type"), "{s2}");
    }

    #[test]
    fn page_sanitizes_limit_and_offset() {
        let p = Page::new(-5, -1);
        assert_eq!(p.limit, 100);
        assert_eq!(p.offset, 0);
        let p = Page::new(2000, 25);
        assert_eq!(p.limit, 500);
        assert_eq!(p.offset, 25);
    }

    #[test]
    fn target_string_includes_scope_and_object() {
        let t = Target {
            catalog: String::new(),
            database: "app".into(),
            schema: "public".into(),
            name: "users".into(),
            type_: ObjectType::Table,
        };
        assert_eq!(t.to_path(), "app.public.users");
    }
}
