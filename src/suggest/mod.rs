//! SQL / Redis / Mongo completion engine and the shared syntax lookup sets.
//! Port of `internal/suggest`. The vendored syntax data is embedded with
//! `include_str!` so builds need no network.

use std::collections::HashSet;

use once_cell::sync::Lazy;
use serde::Deserialize;

use crate::config::Driver;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum Want {
    #[default]
    Any,
    Fields,
    Tables,
    None,
}

#[derive(Debug, Clone, Default)]
pub struct Field {
    pub name: String,
    pub type_: String,
}

#[derive(Debug, Clone, Default)]
pub struct Context {
    pub page: String,
    pub driver: Option<Driver>,
    pub input: String,
    pub cursor: usize,
    pub objects: Vec<String>,
    pub fields: Vec<Field>,
    pub want: Want,
    pub table: String,
}

#[derive(Debug, Clone, Default, Deserialize)]
pub struct Suggestion {
    pub value: String,
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub detail: String,
    /// When set, the string typed text is matched/ranked against (lets a
    /// qualified "table.col" filter by "col"). Not serialized.
    #[serde(skip)]
    pub match_: String,
}

impl Suggestion {
    fn new(value: &str, detail: &str) -> Suggestion {
        Suggestion {
            value: value.into(),
            label: String::new(),
            detail: detail.into(),
            match_: String::new(),
        }
    }
    fn match_key(&self) -> &str {
        if self.match_.is_empty() {
            &self.value
        } else {
            &self.match_
        }
    }
}

fn parse(raw: &str) -> Vec<Suggestion> {
    serde_json::from_str(raw).expect("suggest: invalid embedded syntax data")
}

static MONGO_QUERY: Lazy<Vec<Suggestion>> = Lazy::new(|| parse(include_str!("data/mongo_query.json")));
static MONGO_STAGE: Lazy<Vec<Suggestion>> = Lazy::new(|| parse(include_str!("data/mongo_stage.json")));
static MONGO_EXPR: Lazy<Vec<Suggestion>> = Lazy::new(|| parse(include_str!("data/mongo_expr.json")));
static MONGO_BSON_CONSTRUCTORS: Lazy<Vec<Suggestion>> = Lazy::new(|| {
    [
        ("ObjectId(", "BSON object id"),
        ("ISODate(", "BSON date"),
        ("NumberLong(", "BSON 64-bit integer"),
        ("NumberDecimal(", "BSON decimal"),
        ("NumberInt(", "BSON 32-bit integer"),
    ]
    .iter()
    .map(|(value, detail)| Suggestion::new(value, detail))
    .collect()
});
static REDIS_COMMANDS: Lazy<Vec<Suggestion>> = Lazy::new(|| parse(include_str!("data/redis.json")));
static MYSQL_KEYWORDS: Lazy<Vec<Suggestion>> = Lazy::new(|| parse(include_str!("data/mysql_keywords.json")));
static MYSQL_FUNCTIONS: Lazy<Vec<Suggestion>> = Lazy::new(|| parse(include_str!("data/mysql_functions.json")));
static DORIS_FUNCTIONS: Lazy<Vec<Suggestion>> = Lazy::new(|| parse(include_str!("data/doris_functions.json")));

static DORIS_KEYWORDS: Lazy<Vec<Suggestion>> = Lazy::new(|| {
    let extras = [
        "DISTRIBUTED", "BUCKETS", "ROLLUP", "AGGREGATE", "DUPLICATE", "PROPERTIES", "HLL",
        "BITMAP", "TABLET", "CATALOG",
    ];
    let mut all: Vec<Suggestion> = MYSQL_KEYWORDS.clone();
    for e in extras {
        all.push(Suggestion::new(e, "keyword"));
    }
    // dedup by lowercase value, sort by value (stable).
    let mut seen = HashSet::new();
    let mut out: Vec<Suggestion> = Vec::new();
    for it in all {
        let k = it.value.to_lowercase();
        if seen.insert(k) {
            out.push(it);
        }
    }
    out.sort_by(|a, b| a.value.cmp(&b.value));
    out
});

fn upper_value_set(items: &[Suggestion]) -> HashSet<String> {
    items.iter().map(|s| s.value.to_uppercase()).collect()
}

fn upper_fn_set(items: &[Suggestion]) -> HashSet<String> {
    items
        .iter()
        .map(|s| s.value.trim_end_matches('(').to_uppercase())
        .collect()
}

static MYSQL_KW_SET: Lazy<HashSet<String>> = Lazy::new(|| upper_value_set(&MYSQL_KEYWORDS));
static DORIS_KW_SET: Lazy<HashSet<String>> = Lazy::new(|| upper_value_set(&DORIS_KEYWORDS));
static MYSQL_FN_SET: Lazy<HashSet<String>> = Lazy::new(|| upper_fn_set(&MYSQL_FUNCTIONS));
static DORIS_FN_SET: Lazy<HashSet<String>> = Lazy::new(|| upper_fn_set(&DORIS_FUNCTIONS));
static REDIS_CMD_SET: Lazy<HashSet<String>> = Lazy::new(|| upper_value_set(&REDIS_COMMANDS));
static MONGO_METHOD_SET: Lazy<HashSet<String>> = Lazy::new(|| upper_value_set(&mongo_methods()));
static MONGO_BSON_CONSTRUCTOR_SET: Lazy<HashSet<String>> =
    Lazy::new(|| upper_fn_set(&MONGO_BSON_CONSTRUCTORS));

/// Upper-case keyword set for an SQL driver (shared with the highlighter).
pub fn sql_keyword_set(driver: Driver) -> &'static HashSet<String> {
    if driver == Driver::Doris {
        &DORIS_KW_SET
    } else {
        &MYSQL_KW_SET
    }
}
pub fn sql_function_set(driver: Driver) -> &'static HashSet<String> {
    if driver == Driver::Doris {
        &DORIS_FN_SET
    } else {
        &MYSQL_FN_SET
    }
}
pub fn redis_command_set() -> &'static HashSet<String> {
    &REDIS_CMD_SET
}
pub fn mongo_method_set() -> &'static HashSet<String> {
    &MONGO_METHOD_SET
}
pub(crate) fn mongo_bson_constructor_set() -> &'static HashSet<String> {
    &MONGO_BSON_CONSTRUCTOR_SET
}

fn mongo_fragments() -> Vec<Suggestion> {
    [
        ("database", "database field"),
        ("collection", "collection field"),
        ("filter", "filter object"),
        ("limit", "result limit"),
        ("skip", "result offset"),
        ("$eq", "equals"),
        ("$in", "in array"),
        ("$gt", "greater than"),
        ("$lt", "less than"),
    ]
    .iter()
    .map(|(v, d)| Suggestion::new(v, d))
    .collect()
}

fn mongo_methods() -> Vec<Suggestion> {
    [
        ("find", "query documents"),
        ("findOne", "query one document"),
        ("countDocuments", "count documents"),
        ("aggregate", "aggregation pipeline"),
        ("insertOne", "insert one document"),
        ("insertMany", "insert many documents"),
        ("updateOne", "update one document"),
        ("updateMany", "update many documents"),
        ("deleteOne", "delete one document"),
        ("deleteMany", "delete many documents"),
        ("replaceOne", "replace one document"),
        ("distinct", "distinct values"),
        ("count", "count (legacy)"),
        ("estimatedDocumentCount", "fast count"),
        ("findOneAndUpdate", "find & update"),
        ("findOneAndDelete", "find & delete"),
        ("findOneAndReplace", "find & replace"),
        ("bulkWrite", "bulk operations"),
        ("createIndex", "create index"),
        ("dropIndex", "drop index"),
        ("drop", "drop collection"),
    ]
    .iter()
    .map(|(v, d)| Suggestion::new(v, d))
    .collect()
}

fn mongo_update_operators() -> Vec<Suggestion> {
    [
        ("$set", "field"), ("$unset", "field"), ("$inc", "field"), ("$push", "array"),
        ("$pull", "array"), ("$setOnInsert", "field"), ("$rename", "field"), ("$mul", "field"),
        ("$min", "field"), ("$max", "field"), ("$currentDate", "field"), ("$addToSet", "array"),
        ("$pop", "array"), ("$pullAll", "array"), ("$bit", "bitwise"), ("$each", "array modifier"),
        ("$position", "array modifier"), ("$slice", "array modifier"), ("$sort", "array modifier"),
    ]
    .iter()
    .map(|(v, d)| Suggestion::new(v, d))
    .collect()
}

fn page_commands(page: &str) -> Vec<Suggestion> {
    let set: &[(&str, &str)] = match page {
        "connections" => &[
            ("new", "create connection"), ("open", "open connection"), ("test", "test connection"),
            ("edit", "edit connection"), ("delete", "delete connection"), ("history", "show history"),
        ],
        "browser" => &[
            ("db", "select database"), ("open", "open object"), ("refresh", "reload metadata"),
            ("query", "open query console"), ("history", "show history"), ("next", "next Redis scan page"),
        ],
        "data" => &[
            ("insert", "insert document or row"), ("update", "update by key"), ("delete", "delete by key"),
            ("refresh", "reload data"), ("query", "open query console"), ("history", "show history"),
            ("back", "return"),
        ],
        "history" => &[("back", "return")],
        _ => &[],
    };
    set.iter().map(|(v, d)| Suggestion::new(v, d)).collect()
}

/// Produce ranked completions for the cursor position.
pub fn suggest(ctx: &Context) -> Vec<Suggestion> {
    let input = input_at_cursor(ctx);
    let candidates = candidates_for(ctx, input);
    let token = current_token(input);
    let mut filtered: Vec<Suggestion> = candidates
        .into_iter()
        .filter(|c| matches(c.match_key(), &token))
        .map(|mut c| {
            if c.label.is_empty() {
                c.label = c.value.clone();
            }
            c
        })
        .collect();

    // Prefix matches rank before mid-string matches (stable within group).
    let lower = token.to_lowercase();
    if !lower.is_empty() {
        filtered.sort_by(|a, b| {
            let pa = a.match_key().to_lowercase().starts_with(&lower);
            let pb = b.match_key().to_lowercase().starts_with(&lower);
            pb.cmp(&pa) // true (prefix) first
        });
    }
    filtered
}

fn candidates_for(ctx: &Context, input: &str) -> Vec<Suggestion> {
    let with_sql = |keywords: &[Suggestion], functions: &[Suggestion]| -> Vec<Suggestion> {
        let fields = field_suggestions(&ctx.fields, &ctx.table);
        let objects = object_suggestions(&ctx.objects, "table");
        match ctx.want {
            Want::None => Vec::new(),
            Want::Tables => concat(&[objects, keywords.to_vec(), functions.to_vec()]),
            Want::Fields => concat(&[fields, functions.to_vec(), keywords.to_vec(), objects]),
            Want::Any => concat(&[fields, objects, keywords.to_vec(), functions.to_vec()]),
        }
    };
    match ctx.driver {
        Some(Driver::Mysql) => with_sql(&MYSQL_KEYWORDS, &MYSQL_FUNCTIONS),
        Some(Driver::Doris) => with_sql(&DORIS_KEYWORDS, &DORIS_FUNCTIONS),
        Some(Driver::Redis) => {
            let mut out = object_suggestions(&ctx.objects, "key");
            out.extend(REDIS_COMMANDS.clone());
            out
        }
        Some(Driver::Mongo) => mongo_candidates(ctx, input),
        None => page_commands(&ctx.page),
    }
}

fn input_at_cursor(ctx: &Context) -> &str {
    if ctx.cursor > 0 && ctx.cursor < ctx.input.len() {
        &ctx.input[..ctx.cursor]
    } else {
        &ctx.input
    }
}

fn mongo_candidates(ctx: &Context, input: &str) -> Vec<Suggestion> {
    if mongo_collection_completion(input) {
        return object_suggestions(&ctx.objects, "collection");
    }
    if mongo_method_completion(input) {
        return mongo_methods();
    }
    if mongo_inside_document(input) {
        let token = current_token(input);
        if token.starts_with('$') {
            if mongo_inside_update_document(input) {
                return mongo_update_operators();
            }
            if input.contains("aggregate(") {
                let mut ops = MONGO_STAGE.clone();
                ops.extend(MONGO_EXPR.clone());
                ops.extend(MONGO_QUERY.clone());
                return ops;
            }
            return MONGO_QUERY.clone();
        }
        let mut candidates = field_suggestions(&ctx.fields, "");
        candidates.extend(mongo_fragments());
        candidates.extend(
            MONGO_BSON_CONSTRUCTORS
                .iter()
                .filter(|item| item.value == "ObjectId(")
                .cloned(),
        );
        candidates.extend(MONGO_QUERY.clone());
        candidates.extend(mongo_update_operators());
        return candidates;
    }
    let mut out = object_suggestions(&ctx.objects, "collection");
    out.extend(mongo_fragments());
    out
}

fn mongo_collection_completion(input: &str) -> bool {
    let trimmed = input.trim_end_matches([' ', '\t', '\n']);
    let Some(idx) = trimmed.rfind("db.") else { return false };
    let after = &trimmed[idx + 3..];
    !after.contains('.') && !after.contains(['(', '{', '[', ' '])
}

fn mongo_method_completion(input: &str) -> bool {
    let trimmed = input.trim_end_matches([' ', '\t', '\n']);
    let Some(idx) = trimmed.rfind("db.") else { return false };
    let after = &trimmed[idx + 3..];
    let parts: Vec<&str> = after.split('.').collect();
    parts.len() == 2 && !parts[0].is_empty() && !parts[1].contains(['(', '{', '[', ' '])
}

fn mongo_inside_document(input: &str) -> bool {
    let mut depth = 0i32;
    for ch in input.chars() {
        match ch {
            '{' => depth += 1,
            '}' => {
                if depth > 0 {
                    depth -= 1;
                }
            }
            _ => {}
        }
    }
    depth > 0
}

fn mongo_inside_update_document(input: &str) -> bool {
    let Some(open) = input.rfind('{') else { return false };
    let before = &input[..open];
    before.contains("updateOne(") || before.contains("updateMany(")
}

fn object_suggestions(objects: &[String], kind: &str) -> Vec<Suggestion> {
    objects.iter().map(|o| Suggestion::new(o, kind)).collect()
}

fn field_suggestions(fields: &[Field], table: &str) -> Vec<Suggestion> {
    fields
        .iter()
        .map(|f| {
            let detail = if f.type_.is_empty() { "field".to_string() } else { f.type_.clone() };
            let (value, match_) = if table.is_empty() {
                (f.name.clone(), String::new())
            } else {
                (format!("{table}.{}", f.name), f.name.clone())
            };
            Suggestion {
                value,
                label: String::new(),
                detail,
                match_,
            }
        })
        .collect()
}

fn concat(groups: &[Vec<Suggestion>]) -> Vec<Suggestion> {
    let mut out = Vec::new();
    for g in groups {
        out.extend(g.iter().cloned());
    }
    out
}

fn matches(value: &str, token: &str) -> bool {
    token.is_empty() || value.to_lowercase().contains(&token.to_lowercase())
}

/// The run of identifier characters immediately before the cursor.
pub fn current_token(input: &str) -> String {
    let bytes = input.as_bytes();
    let mut start = bytes.len();
    while start > 0 {
        let r = bytes[start - 1];
        if r.is_ascii_alphanumeric() || r == b'_' || r == b'$' {
            start -= 1;
        } else {
            break;
        }
    }
    input[start..].to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ctx(driver: Driver, input: &str) -> Context {
        Context {
            driver: Some(driver),
            input: input.to_string(),
            cursor: input.len(),
            ..Default::default()
        }
    }

    #[test]
    fn sql_keywords_by_current_token() {
        let s = suggest(&ctx(Driver::Mysql, "sel"));
        assert!(s.iter().any(|x| x.value.eq_ignore_ascii_case("SELECT")));
    }

    #[test]
    fn redis_commands_by_token() {
        let s = suggest(&ctx(Driver::Redis, "hge"));
        let vals: Vec<&str> = s.iter().map(|x| x.value.as_str()).collect();
        assert!(vals.iter().any(|v| v.eq_ignore_ascii_case("HGET")));
        assert!(vals.iter().any(|v| v.eq_ignore_ascii_case("HGETALL")));
    }

    #[test]
    fn includes_database_objects() {
        let mut c = ctx(Driver::Mysql, "");
        c.objects = vec!["users".into(), "orders".into()];
        let s = suggest(&c);
        assert!(s.iter().any(|x| x.value == "users"));
    }

    #[test]
    fn mongo_collections_after_db_dot() {
        let mut c = ctx(Driver::Mongo, "db.");
        c.objects = vec!["users".into()];
        let s = suggest(&c);
        assert!(s.iter().any(|x| x.value == "users" && x.detail == "collection"));
    }

    #[test]
    fn mongo_methods_after_collection_dot() {
        let c = ctx(Driver::Mongo, "db.users.");
        let s = suggest(&c);
        let vals: Vec<&str> = s.iter().map(|x| x.value.as_str()).collect();
        assert!(vals.contains(&"find"));
        assert!(vals.contains(&"countDocuments"));
    }

    #[test]
    fn mongo_query_operators_inside_document() {
        let c = ctx(Driver::Mongo, "db.users.find({ $");
        let s = suggest(&c);
        assert!(s.iter().any(|x| x.value == "$in" || x.value == "$gt"));
    }

    #[test]
    fn mongo_update_operators_inside_update() {
        let c = ctx(Driver::Mongo, "db.t.updateOne({_id:1}, { $");
        let s = suggest(&c);
        assert!(s.iter().any(|x| x.value == "$set"));
    }

    #[test]
    fn mongo_only_suggests_object_id_constructor() {
        let object_id = suggest(&ctx(Driver::Mongo, "db.users.find({_id: Obj"));
        assert!(object_id.iter().any(|x| x.value == "ObjectId("));

        let iso_date = suggest(&ctx(Driver::Mongo, "db.users.find({created_at: ISO"));
        assert!(!iso_date.iter().any(|x| x.value == "ISODate("));
    }

    #[test]
    fn field_carries_type_or_falls_back() {
        let mut c = ctx(Driver::Mysql, "");
        c.fields = vec![
            Field { name: "id".into(), type_: "bigint".into() },
            Field { name: "n".into(), type_: String::new() },
        ];
        let s = suggest(&c);
        let id = s.iter().find(|x| x.value == "id").unwrap();
        assert_eq!(id.detail, "bigint");
        let n = s.iter().find(|x| x.value == "n").unwrap();
        assert_eq!(n.detail, "field");
    }

    #[test]
    fn qualified_field_matches_bare_name() {
        let mut c = Context {
            driver: Some(Driver::Mysql),
            table: "users".into(),
            ..Default::default()
        };
        c.fields = vec![Field { name: "email".into(), type_: "varchar".into() }];
        c.input = "email".into();
        c.cursor = 5;
        let s = suggest(&c);
        assert!(s.iter().any(|x| x.value == "users.email"));
    }

    #[test]
    fn prefix_ranks_before_substring() {
        let mut c = ctx(Driver::Mysql, "us");
        c.objects = vec!["campus".into(), "users".into()];
        let s = suggest(&c);
        let users = s.iter().position(|x| x.value == "users").unwrap();
        let campus = s.iter().position(|x| x.value == "campus").unwrap();
        assert!(users < campus, "prefix match should rank first");
    }

    #[test]
    fn doris_has_specific_function() {
        let s = suggest(&ctx(Driver::Doris, "approx"));
        assert!(s.iter().any(|x| x.value.to_uppercase().starts_with("APPROX_COUNT_DISTINCT")));
    }

    #[test]
    fn vendored_data_comprehensive() {
        assert!(!MYSQL_KEYWORDS.is_empty());
        assert!(!MYSQL_FUNCTIONS.is_empty());
        assert!(!DORIS_FUNCTIONS.is_empty());
        assert!(!REDIS_COMMANDS.is_empty());
        assert!(!MONGO_QUERY.is_empty());
        assert!(!MONGO_STAGE.is_empty());
    }

    #[test]
    fn current_token_excludes_trailing_space() {
        assert_eq!(current_token("select * from "), "");
        assert_eq!(current_token("sele"), "sele");
    }
}
