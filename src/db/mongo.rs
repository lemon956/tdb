//! MongoDB adapter. Port of `internal/db/mongoadapter`.
//!
//! Preserves the mongosh command parser (`db.coll.method(...)`) and the
//! Extended-JSON normalization (unquoted keys quoted, single→double quotes,
//! `ObjectId()/ISODate()/NumberLong()/...` → `$oid/$date/$numberLong`), plus
//! collection field sampling for completion.

use anyhow::{anyhow, Result};
use bson::{doc, Bson, Document};
use futures::TryStreamExt;
use mongodb::{Client, Collection};
use once_cell::sync::Lazy;
use regex::Regex;
use serde_json::{Map, Value};

use crate::config::Profile;
use crate::db::{Command, Key, Object, ObjectMetadata, ObjectType, Page, Query, Scope, Target};
use crate::result::{self, Document as ResultDoc, MutationResult, Set};

const FIELD_SAMPLE_SIZE: i64 = 50;
const FIELD_MAX_COUNT: usize = 200;
const FIELD_MAX_DEPTH: usize = 3;

pub struct MongoAdapter {
    profile: Profile,
    client: Client,
}

impl MongoAdapter {
    pub async fn connect(profile: &Profile) -> Result<MongoAdapter> {
        let uri = build_mongo_uri(profile);
        let client = Client::with_uri_str(&uri).await?;
        Ok(MongoAdapter {
            profile: profile.clone(),
            client,
        })
    }

    pub async fn test(&self) -> Result<()> {
        self.client
            .database("admin")
            .run_command(doc! {"ping": 1})
            .await
            .map_err(mongo_err)?;
        Ok(())
    }

    pub async fn list_databases(&self) -> Result<Vec<String>> {
        self.client.list_database_names().await.map_err(mongo_err)
    }

    pub async fn list_objects(&self, scope: Scope) -> Result<Vec<Object>> {
        let names = self
            .client
            .database(&scope.database)
            .list_collection_names()
            .await
            .map_err(mongo_err)?;
        Ok(names
            .into_iter()
            .map(|name| Object {
                name,
                type_: ObjectType::Collection,
                sub_type: String::new(),
            })
            .collect())
    }

    fn coll(&self, database: &str, name: &str) -> Collection<Document> {
        self.client.database(database).collection::<Document>(name)
    }

    pub async fn preview(&self, target: Target, query: Query, page: Page) -> Result<Set> {
        let filter = build_filter(&query)?;
        let find_limit = if page.limit > 0 {
            (page.limit + 1) as i64
        } else {
            page.limit as i64
        };
        let cursor = self
            .coll(&target.database, &target.name)
            .find(filter)
            .limit(find_limit)
            .skip(page.offset.max(0) as u64)
            .await
            .map_err(mongo_err)?;
        let mut set = cursor_to_set(cursor, page.limit as usize).await?;
        set.has_more = set.truncated;
        set.truncated = false;
        Ok(set)
    }

    pub async fn metadata(&self, target: Target) -> Result<ObjectMetadata> {
        let coll = self.coll(&target.database, &target.name);
        let mut indexes = Vec::new();
        if let Ok(mut cursor) = coll.list_indexes().await {
            while let Ok(Some(model)) = cursor.try_next().await {
                let name = model.options.as_ref().and_then(|o| o.name.clone()).unwrap_or_default();
                let unique = model
                    .options
                    .as_ref()
                    .and_then(|o| o.unique)
                    .unwrap_or(false);
                let columns = model.keys.keys().map(|k| k.to_string()).collect();
                indexes.push(crate::db::MetadataIndex {
                    name,
                    columns,
                    unique,
                });
            }
        }
        let fields = self
            .sample_fields(&target.database, &target.name)
            .await
            .unwrap_or_default();
        let mut attributes = std::collections::BTreeMap::new();
        attributes.insert("database".into(), target.database.clone());
        attributes.insert("collection".into(), target.name.clone());
        Ok(ObjectMetadata {
            fields,
            indexes,
            attributes,
        })
    }

    /// Infer field names (including nested dotted paths) by sampling documents.
    async fn sample_fields(&self, database: &str, collection: &str) -> Option<Vec<crate::db::MetadataField>> {
        let cursor = self
            .coll(database, collection)
            .find(doc! {})
            .limit(FIELD_SAMPLE_SIZE)
            .await
            .ok()?;
        let docs: Vec<Document> = cursor.try_collect().await.ok()?;
        let mut seen = std::collections::BTreeSet::new();
        for doc in &docs {
            collect_field_paths(doc, "", 0, &mut seen);
            if seen.len() >= FIELD_MAX_COUNT {
                break;
            }
        }
        Some(
            seen.into_iter()
                .map(|name| crate::db::MetadataField {
                    name,
                    ..Default::default()
                })
                .collect(),
        )
    }

    pub async fn execute(&self, command: Command) -> Result<Set> {
        match classify_execute_text(&command.text)? {
            MongoExecuteText::Mongosh(shell) => return self.execute_mongosh(&command, shell).await,
            MongoExecuteText::JsonRequest => {}
        }
        // JSON request form: {database, collection, filter, limit, skip}
        let req: Value = serde_json::from_str(&command.text)?;
        let database = req
            .get("database")
            .and_then(|v| v.as_str())
            .filter(|s| !s.is_empty())
            .map(|s| s.to_string())
            .unwrap_or_else(|| command.database.clone());
        let collection = req
            .get("collection")
            .and_then(|v| v.as_str())
            .unwrap_or("")
            .to_string();
        if database.is_empty() || collection.is_empty() {
            return Err(anyhow!("mongo execute requires database and collection"));
        }
        let filter = req
            .get("filter")
            .cloned()
            .and_then(|v| v.as_object().cloned());
        let limit = req.get("limit").and_then(|v| v.as_i64()).unwrap_or(0) as i32;
        let skip = req.get("skip").and_then(|v| v.as_i64()).unwrap_or(0) as i32;
        self.preview(
            Target {
                database,
                name: collection,
                type_: ObjectType::Collection,
                ..Default::default()
            },
            Query {
                text: String::new(),
                filter,
            },
            Page::new(limit, skip),
        )
        .await
    }

    async fn execute_mongosh(&self, command: &Command, shell: MongoshCommand) -> Result<Set> {
        if is_mongosh_mutation(&shell.method) {
            self.ensure_writable()?;
        }
        let database = if command.database.is_empty() {
            self.profile.database.clone()
        } else {
            command.database.clone()
        };
        if database.is_empty() || shell.collection.is_empty() {
            return Err(anyhow!("mongo execute requires database and collection"));
        }
        let coll = self.coll(&database, &shell.collection);
        match shell.method.as_str() {
            "find" | "findOne" => {
                self.preview(
                    Target {
                        database,
                        name: shell.collection.clone(),
                        type_: ObjectType::Collection,
                        ..Default::default()
                    },
                    Query {
                        text: String::new(),
                        filter: shell.filter.clone(),
                    },
                    Page::new(shell.limit, 0),
                )
                .await
            }
            "countDocuments" => {
                let count = coll
                    .count_documents(value_to_doc(&shell.filter)?)
                    .await
                    .map_err(mongo_err)?;
                Ok(Set {
                    table: Some(result::Table {
                        columns: vec![result::Column {
                            name: "count".into(),
                            type_: String::new(),
                        }],
                        rows: vec![result::Row {
                            values: vec![Value::Number(count.into())],
                        }],
                    }),
                    ..Default::default()
                })
            }
            "aggregate" => {
                let pipeline: Vec<Document> = shell
                    .pipeline
                    .iter()
                    .map(|v| value_to_doc(&Some(v.as_object().cloned().unwrap_or_default())))
                    .collect::<Result<Vec<_>>>()?;
                let cursor = coll.aggregate(pipeline).await.map_err(mongo_err)?;
                cursor_to_set(cursor, crate::db::MAX_RESULT_ROWS).await
            }
            _ => self.execute_mongosh_mutation(&coll, shell).await,
        }
    }

    async fn execute_mongosh_mutation(
        &self,
        coll: &Collection<Document>,
        shell: MongoshCommand,
    ) -> Result<Set> {
        self.ensure_writable()?;
        let affected: i64 = match shell.method.as_str() {
            "insertOne" => {
                coll.insert_one(value_to_doc(&shell.document)?).await.map_err(mongo_err)?;
                1
            }
            "insertMany" => {
                let docs: Vec<Document> = shell
                    .documents
                    .iter()
                    .map(|v| value_to_doc(&v.as_object().cloned()))
                    .collect::<Result<Vec<_>>>()?;
                let res = coll.insert_many(docs).await.map_err(mongo_err)?;
                res.inserted_ids.len() as i64
            }
            "updateOne" => {
                let res = coll
                    .update_one(value_to_doc(&shell.filter)?, value_to_doc(&shell.update)?)
                    .await
                    .map_err(mongo_err)?;
                res.modified_count as i64
            }
            "updateMany" => {
                let res = coll
                    .update_many(value_to_doc(&shell.filter)?, value_to_doc(&shell.update)?)
                    .await
                    .map_err(mongo_err)?;
                res.modified_count as i64
            }
            "deleteOne" => coll
                .delete_one(value_to_doc(&shell.filter)?)
                .await
                .map_err(mongo_err)?
                .deleted_count as i64,
            "deleteMany" => {
                coll.delete_many(value_to_doc(&shell.filter)?)
                    .await
                    .map_err(mongo_err)?
                    .deleted_count as i64
            }
            _ => return Err(anyhow!("mongo execute requires database and collection")),
        };
        Ok(mongo_mutation_set(affected))
    }

    pub async fn insert(&self, target: Target, values: Map<String, Value>) -> Result<MutationResult> {
        self.ensure_writable()?;
        self.coll(&target.database, &target.name)
            .insert_one(value_to_doc(&Some(values))?)
            .await
            .map_err(mongo_err)?;
        Ok(result::new_mutation_result(1))
    }

    pub async fn update(
        &self,
        target: Target,
        key: Key,
        values: Map<String, Value>,
    ) -> Result<MutationResult> {
        self.ensure_writable()?;
        let filter = id_filter(&key)?;
        let update = doc! { "$set": value_to_doc(&Some(values))? };
        let res = self
            .coll(&target.database, &target.name)
            .update_one(filter, update)
            .await
            .map_err(mongo_err)?;
        Ok(result::new_mutation_result(res.modified_count as i64))
    }

    pub async fn delete(&self, target: Target, key: Key) -> Result<MutationResult> {
        self.ensure_writable()?;
        let filter = id_filter(&key)?;
        let res = self
            .coll(&target.database, &target.name)
            .delete_one(filter)
            .await
            .map_err(mongo_err)?;
        Ok(result::new_mutation_result(res.deleted_count as i64))
    }

    fn ensure_writable(&self) -> Result<()> {
        if self.profile.read_only {
            return Err(anyhow!("connection is read-only"));
        }
        Ok(())
    }
}

/// mongodb's `Error` Display appends the raw BSON server reply
/// (`server response: Some(RawDocumentBuf …)`); keep only the human-readable
/// `kind` so the UI error box shows a clean message instead of a hex dump.
fn mongo_err(e: mongodb::error::Error) -> anyhow::Error {
    anyhow!("{}", e.kind)
}

async fn cursor_to_set(
    mut cursor: mongodb::Cursor<Document>,
    limit: usize,
) -> Result<Set> {
    let limit = if limit == 0 {
        crate::db::MAX_RESULT_ROWS
    } else {
        limit
    };
    let mut docs = Vec::new();
    let mut truncated = false;
    while let Some(doc) = cursor.try_next().await.map_err(mongo_err)? {
        if docs.len() >= limit {
            truncated = true;
            break;
        }
        docs.push(doc);
    }
    let mut set = docs_to_set(&docs);
    set.truncated = truncated;
    Ok(set)
}

fn docs_to_set(docs: &[Document]) -> Set {
    let mut out = Vec::with_capacity(docs.len());
    for doc in docs {
        let id = doc
            .get("_id")
            .map(bson_to_display_string)
            .unwrap_or_default();
        let data = match bson_to_json(&Bson::Document(doc.clone())) {
            Value::Object(m) => m,
            _ => Map::new(),
        };
        out.push(ResultDoc { id, data });
    }
    Set {
        documents: out,
        document_result: true,
        ..Default::default()
    }
}

fn bson_to_display_string(b: &Bson) -> String {
    match b {
        Bson::ObjectId(oid) => oid.to_hex(),
        Bson::String(s) => s.clone(),
        other => result::cell_value_string(&bson_to_json(other)),
    }
}

/// Convert BSON to a display-oriented JSON value: ObjectId → hex string,
/// DateTime → RFC3339 string, numbers → numbers, nested docs/arrays recurse.
fn bson_to_json(b: &Bson) -> Value {
    match b {
        Bson::Double(d) => serde_json::Number::from_f64(*d)
            .map(Value::Number)
            .unwrap_or(Value::Null),
        Bson::String(s) => Value::String(s.clone()),
        Bson::Boolean(b) => Value::Bool(*b),
        Bson::Null | Bson::Undefined => Value::Null,
        Bson::Int32(i) => Value::Number((*i).into()),
        Bson::Int64(i) => Value::Number((*i).into()),
        Bson::ObjectId(oid) => Value::String(oid.to_hex()),
        Bson::DateTime(dt) => Value::String(dt.try_to_rfc3339_string().unwrap_or_else(|_| dt.to_string())),
        Bson::Array(arr) => Value::Array(arr.iter().map(bson_to_json).collect()),
        Bson::Document(doc) => {
            let mut m = Map::new();
            for (k, v) in doc {
                m.insert(k.clone(), bson_to_json(v));
            }
            Value::Object(m)
        }
        Bson::Decimal128(d) => Value::String(d.to_string()),
        Bson::Timestamp(ts) => Value::String(format!("Timestamp({}, {})", ts.time, ts.increment)),
        other => Value::String(format!("{other:?}")),
    }
}

fn collect_field_paths(
    doc: &Document,
    prefix: &str,
    depth: usize,
    seen: &mut std::collections::BTreeSet<String>,
) {
    for (key, value) in doc {
        if seen.len() >= FIELD_MAX_COUNT {
            return;
        }
        let path = if prefix.is_empty() {
            key.clone()
        } else {
            format!("{prefix}.{key}")
        };
        seen.insert(path.clone());
        if depth + 1 >= FIELD_MAX_DEPTH {
            continue;
        }
        if let Bson::Document(nested) = value {
            collect_field_paths(nested, &path, depth + 1, seen);
        }
    }
}

fn is_mongosh_mutation(method: &str) -> bool {
    matches!(
        method,
        "insertOne" | "insertMany" | "updateOne" | "updateMany" | "deleteOne" | "deleteMany"
    )
}

fn mongo_mutation_set(affected_rows: i64) -> Set {
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

// ---- URI + filter helpers (pure, tested) ----

/// Build a mongodb URI from a profile (or pass through an existing URI).
pub fn build_mongo_uri(profile: &Profile) -> String {
    if profile.uri_params.starts_with("mongodb://")
        || profile.uri_params.starts_with("mongodb+srv://")
    {
        return profile.uri_params.clone();
    }
    let host = if profile.host.is_empty() {
        "127.0.0.1"
    } else {
        &profile.host
    };
    let port = if profile.port == 0 { 27017 } else { profile.port };
    let database = profile.database.trim_start_matches('/');
    let mut auth = String::new();
    if !profile.user.is_empty() {
        auth = format!("{}:{}@", urlencode(&profile.user), urlencode(&profile.password));
    }
    let mut query = String::new();
    if !profile.auth_db.is_empty() && !profile.uri_params.contains("authSource=") {
        query = format!("?authSource={}", profile.auth_db);
    } else if !profile.uri_params.is_empty() {
        query = format!("?{}", profile.uri_params);
    }
    format!("mongodb://{auth}{host}:{port}/{database}{query}")
}

fn urlencode(s: &str) -> String {
    let mut out = String::new();
    for b in s.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => out.push(b as char),
            _ => out.push_str(&format!("%{b:02X}")),
        }
    }
    out
}

fn build_filter(query: &Query) -> Result<Document> {
    if let Some(filter) = &query.filter {
        return value_to_doc(&Some(filter.clone()));
    }
    if query.text.trim().is_empty() {
        return Ok(Document::new());
    }
    let v: Value = serde_json::from_str(&query.text)
        .map_err(|e| anyhow!("parse mongo filter json: {e}"))?;
    value_to_doc(&v.as_object().cloned())
}

fn id_filter(key: &Key) -> Result<Document> {
    let id = key.columns.get("_id").cloned();
    let id_str = match &id {
        Some(Value::String(s)) => Some(s.clone()),
        None if !key.id.is_empty() => Some(key.id.clone()),
        _ => None,
    };
    if let Some(text) = id_str {
        if text.is_empty() {
            return Err(anyhow!("mongo write operation requires _id"));
        }
        if let Ok(oid) = bson::oid::ObjectId::parse_str(&text) {
            return Ok(doc! {"_id": oid});
        }
        return Ok(doc! {"_id": text});
    }
    match id {
        Some(v) => Ok(doc! {"_id": json_to_bson(&v)?}),
        None => Err(anyhow!("mongo write operation requires _id")),
    }
}

/// Convert a JSON object (already Extended-JSON-normalized) into a BSON document,
/// interpreting `$oid`/`$date`/etc. Returns an empty document for `None`.
///
/// Errors are propagated (not swallowed): a filter/document that fails Extended
/// JSON conversion — e.g. an invalid `ObjectId` — must surface as an error, never
/// silently collapse to `{}` (which would make `find` match the whole collection).
fn value_to_doc(v: &Option<Map<String, Value>>) -> Result<Document> {
    match v {
        Some(m) => match json_to_bson(&Value::Object(m.clone()))? {
            Bson::Document(d) => Ok(d),
            _ => Err(anyhow!("mongo filter/document must be an object")),
        },
        None => Ok(Document::new()),
    }
}

fn json_to_bson(v: &Value) -> Result<Bson> {
    // bson's TryFrom<serde_json::Value> interprets MongoDB Extended JSON
    // ($oid, $date, $numberLong, ...) and recurses into nested objects/arrays.
    Bson::try_from(v.clone()).map_err(|e| anyhow!("invalid extended JSON: {e}"))
}

// ---- mongosh command parser (pure, tested) ----

#[derive(Debug, Clone, Default, PartialEq)]
pub struct MongoshCommand {
    pub collection: String,
    pub method: String,
    pub filter: Option<Map<String, Value>>,
    pub update: Option<Map<String, Value>>,
    pub document: Option<Map<String, Value>>,
    pub documents: Vec<Value>,
    pub pipeline: Vec<Value>,
    pub limit: i32,
}

#[derive(Debug, Clone, PartialEq)]
enum MongoExecuteText {
    Mongosh(MongoshCommand),
    JsonRequest,
}

fn classify_execute_text(text: &str) -> Result<MongoExecuteText> {
    if text.trim().starts_with("db.") {
        return parse_mongosh_command(text).map(MongoExecuteText::Mongosh);
    }
    Ok(MongoExecuteText::JsonRequest)
}

pub fn parse_mongosh_command(text: &str) -> Result<MongoshCommand> {
    let statement = text.trim().trim_end_matches(';').trim();
    let Some(rest) = statement.strip_prefix("db.") else {
        return Err(anyhow!("not a mongosh command"));
    };
    let Some(dot) = rest.find('.') else {
        return Err(anyhow!("not a mongosh command"));
    };
    if dot == 0 {
        return Err(anyhow!("not a mongosh command"));
    }
    let collection = rest[..dot].to_string();
    let rest = &rest[dot + 1..];
    let open = rest.find('(');
    let close = rest.rfind(')');
    let (Some(open), Some(close)) = (open, close) else {
        return Err(anyhow!("not a mongosh command"));
    };
    if open == 0 || close < open {
        return Err(anyhow!("not a mongosh command"));
    }
    let method = rest[..open].to_string();
    let args = split_mongosh_args(&rest[open + 1..close]);
    let mut command = MongoshCommand {
        collection,
        method: method.clone(),
        ..Default::default()
    };
    match method.as_str() {
        "find" => command.filter = Some(parse_optional_map_arg(&args, 0)?),
        "findOne" => {
            command.filter = Some(parse_optional_map_arg(&args, 0)?);
            command.limit = 1;
        }
        "countDocuments" => command.filter = Some(parse_optional_map_arg(&args, 0)?),
        "aggregate" => command.pipeline = parse_optional_array_arg(&args, 0)?,
        "insertOne" => command.document = Some(parse_required_map_arg(&args, 0)?),
        "insertMany" => command.documents = parse_required_array_arg(&args, 0)?,
        "updateOne" | "updateMany" => {
            command.filter = Some(parse_required_map_arg(&args, 0)?);
            command.update = Some(parse_required_map_arg(&args, 1)?);
        }
        "deleteOne" | "deleteMany" => command.filter = Some(parse_required_map_arg(&args, 0)?),
        _ => return Err(anyhow!("not a mongosh command")),
    }
    Ok(command)
}

fn split_mongosh_args(text: &str) -> Vec<String> {
    let mut args = Vec::new();
    let mut depth = 0i32;
    let mut quote = '\0';
    let mut seg_start = 0usize;
    for (b, ch) in text.char_indices() {
        if quote != '\0' {
            if ch == quote {
                quote = '\0';
            }
            continue;
        }
        match ch {
            '\'' | '"' => quote = ch,
            '{' | '[' => depth += 1,
            '}' | ']' => {
                if depth > 0 {
                    depth -= 1;
                }
            }
            ',' if depth == 0 => {
                args.push(text[seg_start..b].trim().to_string());
                seg_start = b + ch.len_utf8();
            }
            _ => {}
        }
    }
    let last = text[seg_start..].trim();
    if !last.is_empty() {
        args.push(last.to_string());
    }
    args
}

fn parse_optional_map_arg(args: &[String], index: usize) -> Result<Map<String, Value>> {
    match args.get(index) {
        Some(arg) if !arg.trim().is_empty() => parse_required_map_arg(args, index),
        _ => Ok(Map::new()),
    }
}

fn parse_required_map_arg(args: &[String], index: usize) -> Result<Map<String, Value>> {
    let arg = args
        .get(index)
        .filter(|arg| !arg.trim().is_empty())
        .ok_or_else(|| anyhow!("mongo argument {} must be an object", index + 1))?;
    match parse_mongosh_value(arg)? {
        Value::Object(m) => Ok(m),
        _ => Err(anyhow!("mongo argument {} must be an object", index + 1)),
    }
}

fn parse_optional_array_arg(args: &[String], index: usize) -> Result<Vec<Value>> {
    match args.get(index) {
        Some(arg) if !arg.trim().is_empty() => parse_required_array_arg(args, index),
        _ => Ok(Vec::new()),
    }
}

fn parse_required_array_arg(args: &[String], index: usize) -> Result<Vec<Value>> {
    let arg = args
        .get(index)
        .filter(|arg| !arg.trim().is_empty())
        .ok_or_else(|| anyhow!("mongo argument {} must be an array", index + 1))?;
    match parse_mongosh_value(arg)? {
        Value::Array(a) => Ok(a),
        _ => Err(anyhow!("mongo argument {} must be an array", index + 1)),
    }
}

static KEY_PATTERN: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"([\{,]\s*)([A-Za-z_$][A-Za-z0-9_$]*)(\s*:)").unwrap());
static OBJECTID_PATTERN: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"[Oo]bject[Ii][dD]\(\s*"([^"]*)"\s*\)"#).unwrap());
static ISODATE_PATTERN: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"ISODate\(\s*"([^"]*)"\s*\)"#).unwrap());
static NUMBERLONG_PATTERN: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"NumberLong\(\s*"?([0-9-]+)"?\s*\)"#).unwrap());
static NUMBERDECIMAL_PATTERN: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"NumberDecimal\(\s*"([^"]*)"\s*\)"#).unwrap());
static NUMBERINT_PATTERN: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"NumberInt\(\s*"?([0-9-]+)"?\s*\)"#).unwrap());

/// Turn mongosh shell syntax into relaxed Extended JSON.
fn normalize_mongosh_extended(text: &str) -> String {
    let s = KEY_PATTERN.replace_all(text.trim(), r#"${1}"${2}"${3}"#).into_owned();
    let s = s.replace('\'', "\"");
    // `$$` emits a literal `$`; `${1}` is the capture group (a bare `$oid` would
    // be read as a group reference named "oid").
    let s = OBJECTID_PATTERN.replace_all(&s, r#"{"$$oid":"${1}"}"#).into_owned();
    let s = ISODATE_PATTERN.replace_all(&s, r#"{"$$date":"${1}"}"#).into_owned();
    let s = NUMBERLONG_PATTERN.replace_all(&s, r#"{"$$numberLong":"${1}"}"#).into_owned();
    let s = NUMBERDECIMAL_PATTERN.replace_all(&s, r#"{"$$numberDecimal":"${1}"}"#).into_owned();
    let s = NUMBERINT_PATTERN.replace_all(&s, r#"${1}"#).into_owned();
    s
}

fn parse_mongosh_value(text: &str) -> Result<Value> {
    if let Some(key) = find_unquoted_dotted_key(text) {
        return Err(anyhow!("mongo argument uses unquoted dotted field `{key}`; write it as \"{key}\""));
    }
    let normalized = normalize_mongosh_extended(text);
    let trimmed = normalized.trim();
    serde_json::from_str::<Value>(trimmed).map_err(|e| anyhow!("parse mongo argument: {e}"))
}

fn find_unquoted_dotted_key(text: &str) -> Option<String> {
    let bytes = text.as_bytes();
    let mut quote = 0u8;
    let mut escape = false;
    let mut i = 0usize;
    while i < bytes.len() {
        let b = bytes[i];
        if quote != 0 {
            if escape {
                escape = false;
            } else if b == b'\\' {
                escape = true;
            } else if b == quote {
                quote = 0;
            }
            i += 1;
            continue;
        }
        if b == b'\'' || b == b'"' {
            quote = b;
            i += 1;
            continue;
        }
        if b == b'{' || b == b',' {
            let mut j = i + 1;
            while j < bytes.len() && bytes[j].is_ascii_whitespace() {
                j += 1;
            }
            let start = j;
            if j >= bytes.len() || !is_mongo_key_start(bytes[j]) {
                i += 1;
                continue;
            }
            j += 1;
            let mut saw_dot = false;
            while j < bytes.len() {
                if is_mongo_key_continue(bytes[j]) {
                    j += 1;
                    continue;
                }
                if bytes[j] == b'.' && j + 1 < bytes.len() && is_mongo_key_start(bytes[j + 1]) {
                    saw_dot = true;
                    j += 2;
                    continue;
                }
                break;
            }
            let end = j;
            while j < bytes.len() && bytes[j].is_ascii_whitespace() {
                j += 1;
            }
            if saw_dot && j < bytes.len() && bytes[j] == b':' {
                return Some(text[start..end].to_string());
            }
        }
        i += 1;
    }
    None
}

fn is_mongo_key_start(b: u8) -> bool {
    b == b'_' || b == b'$' || b.is_ascii_alphabetic()
}

fn is_mongo_key_continue(b: u8) -> bool {
    is_mongo_key_start(b) || b.is_ascii_digit()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::Driver;

    fn mongo_profile() -> Profile {
        Profile {
            id: "m".into(),
            name: "m".into(),
            driver: Driver::Mongo,
            host: "h.example".into(),
            port: 27017,
            user: "u".into(),
            password: "p".into(),
            database: "app".into(),
            auth_db: "admin".into(),
            redis_db: 0,
            uri_params: String::new(),
            read_only: false,
        }
    }

    #[test]
    fn builds_uri_with_auth_source() {
        let uri = build_mongo_uri(&mongo_profile());
        assert!(uri.starts_with("mongodb://u:p@h.example:27017/app"), "{uri}");
        assert!(uri.contains("authSource=admin"), "{uri}");
    }

    #[test]
    fn passes_through_existing_uri() {
        let mut p = mongo_profile();
        p.uri_params = "mongodb+srv://x/y".into();
        assert_eq!(build_mongo_uri(&p), "mongodb+srv://x/y");
    }

    #[test]
    fn parses_find_with_filter() {
        let c = parse_mongosh_command("db.users.find({status: \"active\"})").unwrap();
        assert_eq!(c.collection, "users");
        assert_eq!(c.method, "find");
        assert_eq!(c.filter.unwrap().get("status").unwrap(), &Value::from("active"));
    }

    #[test]
    fn parses_quoted_dotted_field_filter() {
        let c = parse_mongosh_command(r#"db.users.find({"profile.name": "Ada"})"#).unwrap();
        let filter = c.filter.unwrap();
        assert_eq!(filter.get("profile.name"), Some(&Value::from("Ada")));
    }

    #[test]
    fn rejects_unquoted_dotted_field_filter() {
        let err = parse_mongosh_command(r#"db.users.find({profile.name: "Ada"})"#).unwrap_err();
        assert!(err.to_string().contains("profile.name"), "{err}");
    }

    #[test]
    fn rejects_malformed_find_filter_instead_of_empty_filter() {
        let err = parse_mongosh_command("db.users.find({status: })").unwrap_err();
        assert!(err.to_string().contains("mongo argument"), "{err}");
    }

    #[test]
    fn parses_find_one_sets_limit() {
        let c = parse_mongosh_command("db.users.findOne({})").unwrap();
        assert_eq!(c.method, "findOne");
        assert_eq!(c.limit, 1);
    }

    #[test]
    fn parses_update_with_two_args() {
        let c = parse_mongosh_command("db.t.updateOne({_id: 1}, {$set: {x: 2}})").unwrap();
        assert_eq!(c.method, "updateOne");
        assert!(c.filter.unwrap().contains_key("_id"));
        assert!(c.update.unwrap().contains_key("$set"));
    }

    #[test]
    fn rejects_update_missing_update_document() {
        let err = parse_mongosh_command("db.t.updateOne({_id: 1})").unwrap_err();
        assert!(err.to_string().contains("argument 2"), "{err}");
    }

    #[test]
    fn classify_execute_text_rejects_malformed_mongosh_before_json_request() {
        let err = classify_execute_text("db.users.find({status: })").unwrap_err();
        assert!(err.to_string().contains("mongo argument"), "{err}");

        assert!(matches!(
            classify_execute_text(r#"{"database":"app","collection":"users"}"#).unwrap(),
            MongoExecuteText::JsonRequest
        ));
    }

    #[test]
    fn rejects_non_mongosh() {
        assert!(parse_mongosh_command("select 1").is_err());
        assert!(parse_mongosh_command("db.find()").is_err());
    }

    #[test]
    fn normalizes_objectid_and_unquoted_keys() {
        let n = normalize_mongosh_extended("{_id: ObjectId(\"deadbeef\")}");
        assert!(n.contains("\"_id\""), "{n}");
        assert!(n.contains("\"$oid\":\"deadbeef\""), "{n}");
    }

    #[test]
    fn split_args_respects_nesting_and_quotes() {
        let a = split_mongosh_args("{a: 1, b: 2}, {c: 3}");
        assert_eq!(a.len(), 2);
        let a = split_mongosh_args("{a: \"x,y\"}");
        assert_eq!(a.len(), 1);
    }

    #[test]
    fn objectid_filter_becomes_real_object_id() {
        // The mongosh path normalizes ObjectId("..") → {"$oid":".."}; the filter
        // must convert to a real ObjectId, not a `{$oid: ..}` sub-document.
        let cmd = parse_mongosh_command("db.users.find({_id: ObjectId(\"64b7f0000000000000000001\")})").unwrap();
        let doc = value_to_doc(&cmd.filter).unwrap();
        match doc.get("_id") {
            Some(Bson::ObjectId(oid)) => assert_eq!(oid.to_hex(), "64b7f0000000000000000001"),
            other => panic!("_id should be a real ObjectId, got {other:?}"),
        }
    }

    #[test]
    fn invalid_objectid_filter_errors_not_empty() {
        // A bad ObjectId must surface as an error, never silently collapse to an
        // empty filter (which would make find() return the whole collection).
        let mut filter = Map::new();
        let mut oid = Map::new();
        oid.insert("$oid".into(), Value::from("not-a-valid-oid"));
        filter.insert("_id".into(), Value::Object(oid));
        assert!(value_to_doc(&Some(filter)).is_err());
    }

    #[test]
    fn docs_to_set_marks_empty_document_results() {
        let set = docs_to_set(&[]);
        assert!(set.document_result);
    }
}
