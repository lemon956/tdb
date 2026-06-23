//! MySQL / Doris adapter. Port of `internal/db/sqladapter`.
//!
//! Doris specifics preserved: three-level catalog tree (`SHOW CATALOGS` +
//! per-catalog `SWITCH`), and the pipelineX fallback — when a legacy external
//! table fails with "Unsupported exec type in pipelineX: *_SCAN_NODE", the
//! query is retried once on a fresh connection with the pipelineX engine
//! disabled.

mod partition;

use std::str::FromStr;

use anyhow::{anyhow, Result};
use futures::TryStreamExt;
use serde_json::{Map, Value};
use sqlx::mysql::{MySqlConnectOptions, MySqlConnection, MySqlPool, MySqlRow};
use sqlx::{Column, Executor, Row, TypeInfo, ValueRef};

use crate::config::{Driver, Profile};
use crate::db::{dsn, Command, Key, Object, ObjectMetadata, ObjectType, Page, Query, Scope, Target};
use crate::result::{self, MutationResult, Set};

use partition::parse_create_table_shape;

pub struct SqlAdapter {
    profile: Profile,
    pool: MySqlPool,
    is_doris: bool,
}

impl SqlAdapter {
    pub async fn connect(profile: &Profile) -> Result<SqlAdapter> {
        let is_doris = profile.driver == Driver::Doris;
        let url = if is_doris {
            dsn::build_doris_url(profile)
        } else {
            dsn::build_mysql_url(profile)
        };
        let opts = MySqlConnectOptions::from_str(&url)?;
        // Lazy like Go's sql.Open: the first real query (Test) surfaces a
        // connection error.
        let pool = MySqlPool::connect_lazy_with(opts);
        Ok(SqlAdapter {
            profile: profile.clone(),
            pool,
            is_doris,
        })
    }

    pub async fn test(&self) -> Result<()> {
        sqlx::query("SELECT 1").execute(&self.pool).await?;
        Ok(())
    }

    pub async fn list_databases(&self) -> Result<Vec<String>> {
        let mut conn = self.pool.acquire().await?;
        list_databases_on(&mut conn).await
    }

    pub async fn list_catalogs(&self) -> Result<Vec<String>> {
        if !self.is_doris {
            return Ok(Vec::new());
        }
        let mut conn = self.pool.acquire().await?;
        let rows = sqlx::query("SHOW CATALOGS").fetch_all(conn.as_mut()).await?;
        let mut catalogs = Vec::new();
        for row in &rows {
            let map = row_to_string_map(row);
            // Find a column whose header looks like catalog name; else col index 1
            // (SHOW CATALOGS: CatalogId, CatalogName, ...); else col 0.
            let cols: Vec<&String> = map.keys().collect();
            let name = find_catalog_name(row, &map).unwrap_or_else(|| {
                if cols.len() > 1 {
                    map.values().nth(1).cloned().unwrap_or_default()
                } else {
                    map.values().next().cloned().unwrap_or_default()
                }
            });
            if !name.is_empty() {
                catalogs.push(name);
            }
        }
        Ok(catalogs)
    }

    pub async fn list_databases_in_catalog(&self, catalog: &str) -> Result<Vec<String>> {
        if is_internal_catalog(catalog) {
            return self.list_databases().await;
        }
        let mut conn = self.pool.acquire().await?;
        use_scope(&mut conn, catalog, "").await?;
        list_databases_on(&mut conn).await
    }

    pub async fn list_objects(&self, scope: Scope) -> Result<Vec<Object>> {
        let mut conn = self.pool.acquire().await?;
        if !is_internal_catalog(&scope.catalog) {
            use_scope(&mut conn, &scope.catalog, "").await?;
        }
        list_objects_on(&mut conn, &scope.database).await
    }

    pub async fn preview(&self, target: Target, query: Query, page: Page) -> Result<Set> {
        if !query.text.is_empty() {
            return self
                .execute(Command {
                    text: query.text,
                    catalog: target.catalog.clone(),
                    database: target.database.clone(),
                })
                .await;
        }
        // Fetch one extra row to detect a next page.
        let probe = Page::new(page.limit + 1, page.offset);
        let sql = build_preview_sql(&target, probe);
        let limit = page.limit as usize;
        let mut set = self
            .exec_scoped(&target.catalog, &target.database, &sql, limit)
            .await?;
        set.has_more = set.truncated; // the extra row means another page exists
        set.truncated = false;
        Ok(set)
    }

    pub async fn metadata(&self, target: Target) -> Result<ObjectMetadata> {
        let mut conn = self.pool.acquire().await?;
        if !is_internal_catalog(&target.catalog) {
            use_scope(&mut conn, &target.catalog, &target.database).await?;
        }
        metadata_on(&mut conn, &target).await
    }

    pub async fn execute(&self, command: Command) -> Result<Set> {
        let statement = command.text.trim().to_string();
        if self.profile.read_only && !is_row_statement(&statement) {
            return Err(anyhow!("connection is read-only"));
        }
        if is_row_statement(&statement) {
            self.exec_scoped(
                &command.catalog,
                &command.database,
                &statement,
                crate::db::MAX_RESULT_ROWS,
            )
            .await
        } else {
            // Non-row statement: run as a mutation (still honoring scope +
            // pipelineX fallback for DDL on external catalogs).
            let affected = self
                .exec_scoped_mutation(&command.catalog, &command.database, &statement)
                .await?;
            Ok(mutation_set(affected))
        }
    }

    pub async fn insert(&self, target: Target, values: Map<String, Value>) -> Result<MutationResult> {
        self.ensure_writable()?;
        let (sql, args) = build_insert_sql(&target, &values);
        self.exec_mutation_args(&sql, &args).await
    }

    pub async fn update(
        &self,
        target: Target,
        key: Key,
        values: Map<String, Value>,
    ) -> Result<MutationResult> {
        self.ensure_writable()?;
        let (sql, args) = build_update_sql(&target, &key, &values)?;
        self.exec_mutation_args(&sql, &args).await
    }

    pub async fn delete(&self, target: Target, key: Key) -> Result<MutationResult> {
        self.ensure_writable()?;
        let (sql, args) = build_delete_sql(&target, &key)?;
        self.exec_mutation_args(&sql, &args).await
    }

    fn ensure_writable(&self) -> Result<()> {
        if self.profile.read_only {
            return Err(anyhow!("connection is read-only"));
        }
        Ok(())
    }

    /// Run a row-returning statement with Doris catalog/database scope applied,
    /// retrying once with pipelineX disabled on the matching error.
    async fn exec_scoped(
        &self,
        catalog: &str,
        database: &str,
        sql: &str,
        limit: usize,
    ) -> Result<Set> {
        match self.run_scoped(catalog, database, false, sql, limit).await {
            Ok(set) => Ok(set),
            Err(e) if self.is_doris && is_pipelinex_unsupported(&e) => {
                self.run_scoped(catalog, database, true, sql, limit).await
            }
            Err(e) => Err(e),
        }
    }

    async fn run_scoped(
        &self,
        catalog: &str,
        database: &str,
        disable_pipelinex: bool,
        sql: &str,
        limit: usize,
    ) -> Result<Set> {
        let mut conn = self.pool.acquire().await?;
        if disable_pipelinex {
            disable_pipelinex_engine(&mut conn).await;
        }
        use_scope(&mut conn, catalog, database).await?;
        fetch_set(&mut conn, sql, limit).await
    }

    async fn exec_scoped_mutation(&self, catalog: &str, database: &str, sql: &str) -> Result<i64> {
        let run = |disable: bool| async move {
            let mut conn = self.pool.acquire().await?;
            if disable {
                disable_pipelinex_engine(&mut conn).await;
            }
            use_scope(&mut conn, catalog, database).await?;
            let r = sqlx::query(sql).execute(conn.as_mut()).await?;
            Ok::<i64, anyhow::Error>(r.rows_affected() as i64)
        };
        match run(false).await {
            Ok(n) => Ok(n),
            Err(e) if self.is_doris && is_pipelinex_unsupported(&e) => run(true).await,
            Err(e) => Err(e),
        }
    }

    async fn exec_mutation_args(&self, sql: &str, args: &[Value]) -> Result<MutationResult> {
        let mut conn = self.pool.acquire().await?;
        let mut q = sqlx::query(sql);
        for a in args {
            q = bind_value(q, a);
        }
        let r = q.execute(conn.as_mut()).await?;
        Ok(result::new_mutation_result(r.rows_affected() as i64))
    }
}

// ---- connection-level helpers ----

fn is_internal_catalog(catalog: &str) -> bool {
    catalog.is_empty() || catalog.eq_ignore_ascii_case("internal")
}

async fn use_scope(conn: &mut MySqlConnection, catalog: &str, database: &str) -> Result<()> {
    // `USE`/`SWITCH` are not allowed in the prepared-statement protocol (MySQL
    // error 1295), so run them via the simple/text protocol by passing a &str
    // (which sqlx executes unprepared).
    if !is_internal_catalog(catalog) {
        let stmt = format!("SWITCH {}", dsn::quote_identifier(catalog));
        conn.execute(stmt.as_str()).await?;
    }
    if !database.is_empty() {
        let stmt = format!("USE {}", dsn::quote_identifier(database));
        conn.execute(stmt.as_str()).await?;
    }
    Ok(())
}

async fn disable_pipelinex_engine(conn: &mut MySqlConnection) {
    for name in [
        "enable_pipeline_x_engine",
        "experimental_enable_pipeline_x_engine",
        "enable_pipeline_engine",
    ] {
        let _ = sqlx::query(&format!("SET {name} = false"))
            .execute(&mut *conn)
            .await;
    }
}

fn is_pipelinex_unsupported(err: &anyhow::Error) -> bool {
    let msg = err.to_string().to_lowercase();
    if msg.contains("unsupported exec type in pipelinex") {
        return true;
    }
    msg.contains("pipelinex") && msg.contains("scan_node")
}

async fn list_databases_on(conn: &mut MySqlConnection) -> Result<Vec<String>> {
    let rows = sqlx::query("SHOW DATABASES").fetch_all(&mut *conn).await?;
    let mut out = Vec::new();
    for row in &rows {
        out.push(decode_to_string(row, 0));
    }
    Ok(out)
}

async fn list_objects_on(conn: &mut MySqlConnection, database: &str) -> Result<Vec<Object>> {
    let mut sql = "SHOW FULL TABLES".to_string();
    if !database.is_empty() {
        sql += &format!(" FROM {}", dsn::quote_identifier(database));
    }
    let rows = sqlx::query(&sql).fetch_all(&mut *conn).await?;
    let mut objects = Vec::new();
    for row in &rows {
        let name = decode_to_string(row, 0);
        let mut type_ = ObjectType::Table;
        for (i, col) in row.columns().iter().enumerate() {
            if col.name().to_lowercase().contains("table_type") {
                if decode_to_string(row, i).eq_ignore_ascii_case("VIEW") {
                    type_ = ObjectType::View;
                }
                break;
            }
        }
        objects.push(Object {
            name,
            type_,
            sub_type: String::new(),
        });
    }
    Ok(objects)
}

async fn metadata_on(conn: &mut MySqlConnection, target: &Target) -> Result<ObjectMetadata> {
    let name = qualified_name(target);
    let columns_sql = format!("SHOW FULL COLUMNS FROM {name}");
    let indexes_sql = format!("SHOW INDEX FROM {name}");

    let mut fields = Vec::new();
    let rows = sqlx::query(&columns_sql).fetch_all(&mut *conn).await?;
    for row in &rows {
        let m = row_to_string_map(row);
        fields.push(crate::db::MetadataField {
            name: m.get("Field").cloned().unwrap_or_default(),
            type_: m.get("Type").cloned().unwrap_or_default(),
            nullable: m
                .get("Null")
                .map(|v| v.eq_ignore_ascii_case("YES"))
                .unwrap_or(false),
            default: m.get("Default").cloned().unwrap_or_default(),
            comment: m.get("Comment").cloned().unwrap_or_default(),
        });
    }

    let mut indexes: Vec<crate::db::MetadataIndex> = Vec::new();
    let rows = sqlx::query(&indexes_sql).fetch_all(&mut *conn).await?;
    for row in &rows {
        let m = row_to_string_map(row);
        let Some(key_name) = m.get("Key_name").filter(|s| !s.is_empty()) else {
            continue;
        };
        if let Some(idx) = indexes.iter_mut().find(|i| &i.name == key_name) {
            if let Some(col) = m.get("Column_name").filter(|s| !s.is_empty()) {
                idx.columns.push(col.clone());
            }
        } else {
            let mut idx = crate::db::MetadataIndex {
                name: key_name.clone(),
                columns: Vec::new(),
                unique: m.get("Non_unique").map(|v| v == "0").unwrap_or(false),
            };
            if let Some(col) = m.get("Column_name").filter(|s| !s.is_empty()) {
                idx.columns.push(col.clone());
            }
            indexes.push(idx);
        }
    }

    let mut meta = ObjectMetadata {
        fields,
        indexes,
        attributes: Default::default(),
    };
    if target.type_ == ObjectType::Table {
        if let Some(attrs) = table_shape_attributes(conn, target).await {
            meta.attributes = attrs;
        }
    }
    Ok(meta)
}

async fn table_shape_attributes(
    conn: &mut MySqlConnection,
    target: &Target,
) -> Option<std::collections::BTreeMap<String, String>> {
    let sql = format!("SHOW CREATE TABLE {}", qualified_name(target));
    let rows = sqlx::query(&sql).fetch_all(&mut *conn).await.ok()?;
    let row = rows.first()?;
    let m = row_to_string_map(row);
    let ddl = m
        .iter()
        .find(|(k, _)| k.to_lowercase().contains("create"))
        .map(|(_, v)| v.clone())?;
    let (partition, key) = parse_create_table_shape(&ddl);
    let mut attrs = std::collections::BTreeMap::new();
    if !partition.is_empty() {
        attrs.insert("partition".to_string(), partition);
    }
    if !key.is_empty() {
        attrs.insert("key".to_string(), key);
    }
    if attrs.is_empty() {
        None
    } else {
        Some(attrs)
    }
}

/// Materialize a row-returning query, stopping at `limit` rows (0 = no cap). The
/// stream is consumed lazily so a huge result is capped without loading it all.
async fn fetch_set(conn: &mut MySqlConnection, sql: &str, limit: usize) -> Result<Set> {
    let mut stream = sqlx::query(sql).fetch(&mut *conn);
    let mut collected: Vec<MySqlRow> = Vec::new();
    let mut truncated = false;
    while let Some(row) = stream.try_next().await? {
        if limit > 0 && collected.len() >= limit {
            truncated = true;
            break;
        }
        collected.push(row);
    }
    drop(stream);

    let mut table = result::Table::default();
    if let Some(first) = collected.first() {
        for col in first.columns() {
            table.columns.push(result::Column {
                name: col.name().to_string(),
                type_: col.type_info().name().to_string(),
            });
        }
    }
    for row in &collected {
        let values: Vec<Value> = (0..table.columns.len()).map(|i| decode_cell(row, i)).collect();
        table.rows.push(result::Row { values });
    }
    Ok(Set {
        table: Some(table),
        truncated,
        ..Default::default()
    })
}

fn find_catalog_name(
    row: &MySqlRow,
    map: &std::collections::BTreeMap<String, String>,
) -> Option<String> {
    for col in row.columns() {
        let lower = col.name().to_lowercase();
        if lower.contains("catalogname") || lower.contains("catalog_name") {
            return map.get(col.name()).cloned();
        }
    }
    None
}

fn row_to_string_map(row: &MySqlRow) -> std::collections::BTreeMap<String, String> {
    let mut m = std::collections::BTreeMap::new();
    for (i, col) in row.columns().iter().enumerate() {
        m.insert(col.name().to_string(), decode_to_string(row, i));
    }
    m
}

fn decode_to_string(row: &MySqlRow, i: usize) -> String {
    result::cell_value_string(&decode_cell(row, i))
}

/// Decode an arbitrary MySQL/Doris cell into a JSON value, mirroring how Go's
/// database/sql surfaces int64/float64/[]byte/time.Time/nil.
fn decode_cell(row: &MySqlRow, i: usize) -> Value {
    if let Ok(raw) = row.try_get_raw(i) {
        if raw.is_null() {
            return Value::Null;
        }
    }
    let ty = row
        .columns()
        .get(i)
        .map(|c| c.type_info().name().to_uppercase())
        .unwrap_or_default();

    macro_rules! try_get {
        ($t:ty) => {
            row.try_get::<$t, _>(i).ok()
        };
    }

    if ty.contains("DECIMAL") {
        // rust_decimal preserves the column scale (a zero shows as "0.00");
        // BigDecimal is the fallback for values beyond rust_decimal's range.
        if let Some(d) = try_get!(sqlx::types::Decimal) {
            return Value::String(d.to_string());
        }
        if let Some(d) = try_get!(sqlx::types::BigDecimal) {
            return Value::String(d.to_string());
        }
    }
    if ty.contains("INT") {
        if let Some(v) = try_get!(i64) {
            return Value::Number(v.into());
        }
        if let Some(v) = try_get!(u64) {
            return Value::Number(v.into());
        }
    }
    if ty == "FLOAT" || ty == "DOUBLE" {
        if let Some(v) = try_get!(f64) {
            if let Some(n) = serde_json::Number::from_f64(v) {
                return Value::Number(n);
            }
        }
    }
    if ty.contains("DATETIME") || ty.contains("TIMESTAMP") {
        if let Some(v) = try_get!(sqlx::types::chrono::NaiveDateTime) {
            return Value::String(v.format("%Y-%m-%d %H:%M:%S").to_string());
        }
    }
    if ty == "DATE" {
        if let Some(v) = try_get!(sqlx::types::chrono::NaiveDate) {
            return Value::String(v.format("%Y-%m-%d").to_string());
        }
    }
    if ty == "TIME" {
        if let Some(v) = try_get!(sqlx::types::chrono::NaiveTime) {
            return Value::String(v.format("%H:%M:%S").to_string());
        }
    }
    // Strings, JSON, enums, sets, years, and anything else.
    if let Some(s) = try_get!(String) {
        return Value::String(s);
    }
    if let Some(b) = try_get!(Vec<u8>) {
        return Value::String(String::from_utf8_lossy(&b).into_owned());
    }
    Value::Null
}

fn bind_value<'q>(
    q: sqlx::query::Query<'q, sqlx::MySql, sqlx::mysql::MySqlArguments>,
    v: &'q Value,
) -> sqlx::query::Query<'q, sqlx::MySql, sqlx::mysql::MySqlArguments> {
    match v {
        Value::Null => q.bind(Option::<String>::None),
        Value::Bool(b) => q.bind(*b as i64),
        Value::Number(n) => {
            if let Some(i) = n.as_i64() {
                q.bind(i)
            } else if let Some(f) = n.as_f64() {
                q.bind(f)
            } else {
                q.bind(n.to_string())
            }
        }
        Value::String(s) => q.bind(s.clone()),
        other => q.bind(other.to_string()),
    }
}

// ---- pure SQL builders (unit-tested without a database) ----

fn qualified_name(target: &Target) -> String {
    let mut parts = Vec::new();
    for p in [&target.database, &target.schema, &target.name] {
        if !p.is_empty() {
            parts.push(dsn::quote_identifier(p));
        }
    }
    parts.join(".")
}

fn build_preview_sql(target: &Target, page: Page) -> String {
    format!(
        "SELECT * FROM {}{}",
        qualified_name(target),
        dsn::limit_offset_clause(page.limit, page.offset)
    )
}

fn sorted_keys(values: &Map<String, Value>) -> Vec<String> {
    let mut keys: Vec<String> = values.keys().cloned().collect();
    keys.sort();
    keys
}

fn build_insert_sql(target: &Target, values: &Map<String, Value>) -> (String, Vec<Value>) {
    let columns = sorted_keys(values);
    let quoted: Vec<String> = columns.iter().map(|c| dsn::quote_identifier(c)).collect();
    let placeholders: Vec<&str> = columns.iter().map(|_| "?").collect();
    let args: Vec<Value> = columns.iter().map(|c| values[c].clone()).collect();
    let sql = format!(
        "INSERT INTO {} ({}) VALUES ({})",
        qualified_name(target),
        quoted.join(", "),
        placeholders.join(", ")
    );
    (sql, args)
}

fn build_update_sql(
    target: &Target,
    key: &Key,
    values: &Map<String, Value>,
) -> Result<(String, Vec<Value>)> {
    if key.columns.is_empty() {
        return Err(anyhow!("write operation requires key columns"));
    }
    let set_cols = sorted_keys(values);
    let key_cols = sorted_keys(&key.columns);
    let mut args = Vec::new();
    let sets: Vec<String> = set_cols
        .iter()
        .map(|c| {
            args.push(values[c].clone());
            format!("{} = ?", dsn::quote_identifier(c))
        })
        .collect();
    let wheres: Vec<String> = key_cols
        .iter()
        .map(|c| {
            args.push(key.columns[c].clone());
            format!("{} = ?", dsn::quote_identifier(c))
        })
        .collect();
    let sql = format!(
        "UPDATE {} SET {} WHERE {}",
        qualified_name(target),
        sets.join(", "),
        wheres.join(" AND ")
    );
    Ok((sql, args))
}

fn build_delete_sql(target: &Target, key: &Key) -> Result<(String, Vec<Value>)> {
    if key.columns.is_empty() {
        return Err(anyhow!("write operation requires key columns"));
    }
    let key_cols = sorted_keys(&key.columns);
    let mut args = Vec::new();
    let wheres: Vec<String> = key_cols
        .iter()
        .map(|c| {
            args.push(key.columns[c].clone());
            format!("{} = ?", dsn::quote_identifier(c))
        })
        .collect();
    let sql = format!(
        "DELETE FROM {} WHERE {}",
        qualified_name(target),
        wheres.join(" AND ")
    );
    Ok((sql, args))
}

fn mutation_set(affected_rows: i64) -> Set {
    Set {
        table: Some(result::Table {
            columns: vec![result::Column {
                name: "affected_rows".into(),
                type_: "INTEGER".into(),
            }],
            rows: vec![result::Row {
                values: vec![Value::Number(affected_rows.into())],
            }],
        }),
        ..Default::default()
    }
}

/// Whether a statement returns rows (so it runs as a query, not a mutation).
pub fn is_row_statement(statement: &str) -> bool {
    let stripped = strip_leading_sql_comments(statement);
    let first = stripped
        .split_whitespace()
        .next()
        .map(|s| s.to_lowercase())
        .unwrap_or_default();
    matches!(
        first.as_str(),
        "select" | "show" | "describe" | "desc" | "explain" | "with"
    )
}

/// Strip leading whitespace and `-- line` / `/* block */` comments so the first
/// real keyword can be classified.
fn strip_leading_sql_comments(s: &str) -> String {
    let mut s = s;
    loop {
        s = s.trim_start_matches([' ', '\t', '\r', '\n']);
        if let Some(rest) = s.strip_prefix("--") {
            match rest.find('\n') {
                Some(i) => s = &rest[i + 1..],
                None => return String::new(),
            }
        } else if let Some(rest) = s.strip_prefix("/*") {
            match rest.find("*/") {
                Some(i) => s = &rest[i + 2..],
                None => return String::new(),
            }
        } else {
            return s.to_string();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn target() -> Target {
        Target {
            catalog: String::new(),
            database: "app".into(),
            schema: String::new(),
            name: "users".into(),
            type_: ObjectType::Table,
        }
    }

    #[test]
    fn preview_sql_quotes_and_paginates() {
        let sql = build_preview_sql(&target(), Page::new(10, 20));
        assert_eq!(sql, "SELECT * FROM `app`.`users` LIMIT 10 OFFSET 20");
    }

    #[test]
    fn insert_sql_sorts_columns() {
        let mut v = Map::new();
        v.insert("name".into(), json!("x"));
        v.insert("id".into(), json!(1));
        let (sql, args) = build_insert_sql(&target(), &v);
        assert_eq!(
            sql,
            "INSERT INTO `app`.`users` (`id`, `name`) VALUES (?, ?)"
        );
        assert_eq!(args, vec![json!(1), json!("x")]);
    }

    #[test]
    fn update_requires_key() {
        let v = Map::new();
        let err = build_update_sql(&target(), &Key::default(), &v).unwrap_err();
        assert!(err.to_string().contains("key columns"));
    }

    #[test]
    fn row_statement_classification() {
        assert!(is_row_statement("SELECT 1"));
        assert!(is_row_statement("  -- c\n SELECT 1"));
        assert!(is_row_statement("/* x */ with t as (select 1) select * from t"));
        assert!(is_row_statement("show tables"));
        assert!(!is_row_statement("INSERT INTO t VALUES (1)"));
        assert!(!is_row_statement("update t set x=1"));
        assert!(!is_row_statement(""));
    }
}
