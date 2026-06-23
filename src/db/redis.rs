//! Redis adapter. Port of `internal/db/redisadapter`.
//!
//! Note: this module is named `redis` but the external driver crate is also
//! `redis`; the crate is referenced as `::redis` throughout.

use anyhow::{anyhow, Result};
use serde::Serialize;
use serde_json::{Map, Value};

use crate::config::Profile;
use crate::db::{Command, Key, Object, ObjectMetadata, ObjectType, Page, Query, Scope, Target};
use crate::result::{self, MutationResult, Set};

/// Cap on bytes fetched from a single string value.
const MAX_STRING_PREVIEW: i64 = 64 * 1024;
const PREVIEW_CAP: i64 = 100;

const READ_ONLY_COMMANDS: &[&str] = &[
    "GET", "MGET", "STRLEN", "GETRANGE", "SUBSTR", "EXISTS", "TYPE", "TTL", "PTTL", "OBJECT",
    "HGET", "HMGET", "HGETALL", "HKEYS", "HVALS", "HLEN", "HEXISTS", "HSCAN", "HSTRLEN", "LRANGE",
    "LINDEX", "LLEN", "LPOS", "SMEMBERS", "SISMEMBER", "SMISMEMBER", "SCARD", "SRANDMEMBER",
    "SSCAN", "SINTER", "SUNION", "SDIFF", "ZRANGE", "ZRANGEBYSCORE", "ZRANGEBYLEX", "ZREVRANGE",
    "ZSCORE", "ZMSCORE", "ZRANK", "ZREVRANK", "ZCARD", "ZCOUNT", "ZSCAN", "XRANGE", "XREVRANGE",
    "XLEN", "XREAD", "XINFO", "SCAN", "KEYS", "RANDOMKEY", "DBSIZE", "INFO", "PING", "ECHO",
    "TIME", "MEMORY", "DEBUG", "COMMAND", "CLIENT", "CONFIG", "BITCOUNT", "BITPOS", "GETBIT",
    "GEOPOS", "GEODIST", "GEOSEARCH", "PFCOUNT", "DUMP",
];

#[derive(Debug, Clone, Serialize)]
pub struct ZMember {
    pub member: String,
    pub score: f64,
}

#[derive(Debug, Clone, Default, Serialize)]
pub struct RedisValue {
    pub key: String,
    #[serde(rename = "type")]
    pub type_: String,
    pub ttl_seconds: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub string: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub hash: Option<std::collections::BTreeMap<String, String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub list: Option<Vec<String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub set: Option<Vec<String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sorted_set: Option<Vec<ZMember>>,
    pub preview_cap: i64,
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    pub truncated: bool,
}

#[derive(Debug, Clone, Default, Serialize)]
pub struct KeyScan {
    pub keys: Vec<String>,
    pub next_cursor: u64,
    pub has_more: bool,
    pub pattern: String,
    pub count: i64,
}

pub struct RedisAdapter {
    profile: Profile,
    conn: ::redis::aio::MultiplexedConnection,
}

impl RedisAdapter {
    pub async fn connect(profile: &Profile) -> Result<RedisAdapter> {
        let host = if profile.host.is_empty() {
            "127.0.0.1".to_string()
        } else {
            profile.host.clone()
        };
        let port = if profile.port == 0 { 6379 } else { profile.port } as u16;
        let info = ::redis::ConnectionInfo {
            addr: ::redis::ConnectionAddr::Tcp(host, port),
            redis: ::redis::RedisConnectionInfo {
                db: profile.redis_db as i64,
                username: if profile.user.is_empty() {
                    None
                } else {
                    Some(profile.user.clone())
                },
                password: if profile.password.is_empty() {
                    None
                } else {
                    Some(profile.password.clone())
                },
                protocol: Default::default(),
            },
        };
        let client = ::redis::Client::open(info)?;
        let conn = client.get_multiplexed_async_connection().await?;
        Ok(RedisAdapter {
            profile: profile.clone(),
            conn,
        })
    }

    fn c(&self) -> ::redis::aio::MultiplexedConnection {
        self.conn.clone()
    }

    pub async fn test(&self) -> Result<()> {
        let mut c = self.c();
        let _: String = ::redis::cmd("PING").query_async(&mut c).await?;
        Ok(())
    }

    pub async fn list_databases(&self) -> Result<Vec<String>> {
        Ok(vec![self.profile.redis_db.to_string()])
    }

    pub async fn list_objects(&self, _scope: Scope) -> Result<Vec<Object>> {
        let scan = self.scan_keys("*", 0, 100).await?;
        let types = self.key_types(&scan.keys).await?;
        Ok(scan
            .keys
            .into_iter()
            .enumerate()
            .map(|(i, key)| Object {
                name: key,
                type_: ObjectType::Key,
                sub_type: types.get(i).cloned().unwrap_or_default(),
            })
            .collect())
    }

    async fn key_types(&self, keys: &[String]) -> Result<Vec<String>> {
        if keys.is_empty() {
            return Ok(Vec::new());
        }
        let mut c = self.c();
        let mut pipe = ::redis::pipe();
        for k in keys {
            pipe.cmd("TYPE").arg(k);
        }
        let raw: Vec<String> = pipe.query_async(&mut c).await?;
        Ok(raw
            .into_iter()
            .map(|t| if t == "none" { String::new() } else { t })
            .collect())
    }

    async fn scan_keys(&self, pattern: &str, cursor: u64, count: i64) -> Result<KeyScan> {
        let pattern = if pattern.is_empty() { "*" } else { pattern };
        let count = count.clamp(1, 500);
        let mut c = self.c();
        let (next, keys): (u64, Vec<String>) = ::redis::cmd("SCAN")
            .arg(cursor)
            .arg("MATCH")
            .arg(pattern)
            .arg("COUNT")
            .arg(count)
            .query_async(&mut c)
            .await?;
        Ok(KeyScan {
            keys,
            next_cursor: next,
            has_more: next != 0,
            pattern: pattern.to_string(),
            count,
        })
    }

    pub async fn preview(&self, target: Target, query: Query, page: Page) -> Result<Set> {
        if target.name.is_empty() {
            let scan = self.scan_keys(&query.text, 0, page.limit as i64).await?;
            let has_more = scan.has_more;
            let next = scan.next_cursor;
            return Ok(Set {
                value: serde_json::to_value(&scan).unwrap_or(Value::Null),
                has_more,
                next_cursor: next.to_string(),
                ..Default::default()
            });
        }
        let value = self.read_key(&target.name).await?;
        let truncated = value.truncated;
        Ok(Set {
            value: serde_json::to_value(&value).unwrap_or(Value::Null),
            truncated,
            ..Default::default()
        })
    }

    pub async fn metadata(&self, target: Target) -> Result<ObjectMetadata> {
        let value = self.read_key(&target.name).await?;
        let mut attributes = std::collections::BTreeMap::new();
        attributes.insert("key".into(), value.key.clone());
        attributes.insert("type".into(), value.type_.clone());
        attributes.insert("ttl".into(), format!("{}s", value.ttl_seconds));
        attributes.insert("length".into(), redis_value_length(&value).to_string());
        attributes.insert("previewCap".into(), value.preview_cap.to_string());
        Ok(ObjectMetadata {
            attributes,
            ..Default::default()
        })
    }

    async fn read_key(&self, key: &str) -> Result<RedisValue> {
        let mut c = self.c();
        let value_type: String = ::redis::cmd("TYPE").arg(key).query_async(&mut c).await?;
        let ttl: i64 = ::redis::cmd("TTL").arg(key).query_async(&mut c).await.unwrap_or(-1);
        let mut out = RedisValue {
            key: key.to_string(),
            type_: value_type.clone(),
            ttl_seconds: ttl,
            preview_cap: PREVIEW_CAP,
            ..Default::default()
        };
        match value_type.as_str() {
            "string" => {
                let n: i64 = ::redis::cmd("STRLEN").arg(key).query_async(&mut c).await.unwrap_or(0);
                if n > MAX_STRING_PREVIEW {
                    let s: String = ::redis::cmd("GETRANGE")
                        .arg(key)
                        .arg(0)
                        .arg(MAX_STRING_PREVIEW - 1)
                        .query_async(&mut c)
                        .await?;
                    out.string = s;
                    out.truncated = true;
                } else {
                    out.string = ::redis::cmd("GET").arg(key).query_async(&mut c).await.unwrap_or_default();
                }
            }
            "hash" => {
                let h: std::collections::BTreeMap<String, String> =
                    ::redis::cmd("HGETALL").arg(key).query_async(&mut c).await?;
                if h.len() as i64 > out.preview_cap {
                    out.truncated = true;
                }
                out.hash = Some(h);
            }
            "list" => {
                let l: Vec<String> = ::redis::cmd("LRANGE")
                    .arg(key)
                    .arg(0)
                    .arg(out.preview_cap - 1)
                    .query_async(&mut c)
                    .await?;
                let n: i64 = ::redis::cmd("LLEN").arg(key).query_async(&mut c).await.unwrap_or(0);
                if n > out.preview_cap {
                    out.truncated = true;
                }
                out.list = Some(l);
            }
            "set" => {
                let mut s: Vec<String> = ::redis::cmd("SMEMBERS").arg(key).query_async(&mut c).await?;
                if s.len() as i64 > out.preview_cap {
                    s.truncate(out.preview_cap as usize);
                    out.truncated = true;
                }
                out.set = Some(s);
            }
            "zset" => {
                let flat: Vec<String> = ::redis::cmd("ZRANGE")
                    .arg(key)
                    .arg(0)
                    .arg(out.preview_cap - 1)
                    .arg("WITHSCORES")
                    .query_async(&mut c)
                    .await?;
                let mut zs = Vec::new();
                let mut it = flat.into_iter();
                while let (Some(m), Some(sc)) = (it.next(), it.next()) {
                    zs.push(ZMember {
                        member: m,
                        score: sc.parse().unwrap_or(0.0),
                    });
                }
                let n: i64 = ::redis::cmd("ZCARD").arg(key).query_async(&mut c).await.unwrap_or(0);
                if n > out.preview_cap {
                    out.truncated = true;
                }
                out.sorted_set = Some(zs);
            }
            _ => {}
        }
        Ok(out)
    }

    pub async fn execute(&self, command: Command) -> Result<Set> {
        let parts = split_command(&command.text);
        if parts.is_empty() {
            return Err(anyhow!("redis command is empty"));
        }
        if self.profile.read_only
            && !READ_ONLY_COMMANDS.contains(&parts[0].to_uppercase().as_str())
        {
            return Err(anyhow!("connection is read-only"));
        }
        let mut c = self.c();
        let mut cmd = ::redis::cmd(&parts[0]);
        for p in &parts[1..] {
            cmd.arg(p);
        }
        let v: ::redis::Value = cmd.query_async(&mut c).await?;
        Ok(Set {
            value: redis_to_json(&v),
            ..Default::default()
        })
    }

    pub async fn insert(&self, target: Target, values: Map<String, Value>) -> Result<MutationResult> {
        self.write(&target.name, &values).await
    }

    pub async fn update(
        &self,
        target: Target,
        _key: Key,
        values: Map<String, Value>,
    ) -> Result<MutationResult> {
        self.write(&target.name, &values).await
    }

    pub async fn delete(&self, target: Target, key: Key) -> Result<MutationResult> {
        self.ensure_writable()?;
        let mut c = self.c();
        if key.columns.is_empty() {
            let deleted: i64 = ::redis::cmd("DEL").arg(&target.name).query_async(&mut c).await?;
            return Ok(result::new_mutation_result(deleted));
        }
        let affected = self.delete_member(&target.name, &key.columns).await?;
        Ok(result::new_mutation_result(affected))
    }

    fn ensure_writable(&self) -> Result<()> {
        if self.profile.read_only {
            return Err(anyhow!("connection is read-only"));
        }
        Ok(())
    }

    async fn write(&self, key: &str, values: &Map<String, Value>) -> Result<MutationResult> {
        self.ensure_writable()?;
        let kind = as_string(values.get("type")).to_lowercase();
        let mut c = self.c();
        let affected: i64 = match kind.as_str() {
            "string" => {
                let mut cmd = ::redis::cmd("SET");
                cmd.arg(key).arg(as_string(values.get("value")));
                if let Some(secs) = as_i64(values.get("ttl_seconds")) {
                    if secs > 0 {
                        cmd.arg("EX").arg(secs);
                    }
                }
                let _: ::redis::Value = cmd.query_async(&mut c).await?;
                1
            }
            "hash" => {
                let field = as_string(values.get("field"));
                if field.is_empty() {
                    return Err(anyhow!("redis value is invalid"));
                }
                ::redis::cmd("HSET")
                    .arg(key)
                    .arg(&field)
                    .arg(as_string(values.get("value")))
                    .query_async(&mut c)
                    .await?
            }
            "list" => {
                if let Some(index) = as_i64(values.get("index")) {
                    let _: ::redis::Value = ::redis::cmd("LSET")
                        .arg(key)
                        .arg(index)
                        .arg(as_string(values.get("value")))
                        .query_async(&mut c)
                        .await?;
                    1
                } else {
                    ::redis::cmd("RPUSH")
                        .arg(key)
                        .arg(as_string(values.get("value")))
                        .query_async(&mut c)
                        .await?
                }
            }
            "set" => {
                ::redis::cmd("SADD")
                    .arg(key)
                    .arg(as_string(values.get("member")).max(as_string(values.get("value"))))
                    .query_async(&mut c)
                    .await?
            }
            "zset" => {
                let member = as_string(values.get("member"));
                let score = as_f64(values.get("score")).unwrap_or(0.0);
                ::redis::cmd("ZADD")
                    .arg(key)
                    .arg(score)
                    .arg(member)
                    .query_async(&mut c)
                    .await?
            }
            _ => return Err(anyhow!("redis value is invalid")),
        };
        Ok(result::new_mutation_result(affected))
    }

    async fn delete_member(&self, key: &str, columns: &Map<String, Value>) -> Result<i64> {
        let kind = as_string(columns.get("type")).to_lowercase();
        let mut c = self.c();
        let n: i64 = match kind.as_str() {
            "hash" => {
                ::redis::cmd("HDEL")
                    .arg(key)
                    .arg(as_string(columns.get("field")))
                    .query_async(&mut c)
                    .await?
            }
            "list" => {
                let Some(index) = as_i64(columns.get("index")) else {
                    return Err(anyhow!("redis value is invalid"));
                };
                let marker = format!("__tdb_deleted_{index}_marker");
                let _: ::redis::Value = ::redis::cmd("LSET")
                    .arg(key)
                    .arg(index)
                    .arg(&marker)
                    .query_async(&mut c)
                    .await?;
                ::redis::cmd("LREM").arg(key).arg(1).arg(&marker).query_async(&mut c).await?
            }
            "set" => {
                ::redis::cmd("SREM")
                    .arg(key)
                    .arg(as_string(columns.get("member")))
                    .query_async(&mut c)
                    .await?
            }
            "zset" => {
                ::redis::cmd("ZREM")
                    .arg(key)
                    .arg(as_string(columns.get("member")))
                    .query_async(&mut c)
                    .await?
            }
            _ => return Err(anyhow!("redis value is invalid")),
        };
        Ok(n)
    }
}

fn redis_value_length(value: &RedisValue) -> usize {
    match value.type_.as_str() {
        "string" => value.string.len(),
        "hash" => value.hash.as_ref().map(|h| h.len()).unwrap_or(0),
        "list" => value.list.as_ref().map(|l| l.len()).unwrap_or(0),
        "set" => value.set.as_ref().map(|s| s.len()).unwrap_or(0),
        "zset" => value.sorted_set.as_ref().map(|z| z.len()).unwrap_or(0),
        _ => 0,
    }
}

fn redis_to_json(v: &::redis::Value) -> Value {
    use ::redis::Value as RV;
    match v {
        RV::Nil => Value::Null,
        RV::Int(i) => Value::Number((*i).into()),
        RV::BulkString(b) => Value::String(String::from_utf8_lossy(b).into_owned()),
        RV::SimpleString(s) => Value::String(s.clone()),
        RV::Okay => Value::String("OK".into()),
        RV::Double(d) => serde_json::Number::from_f64(*d)
            .map(Value::Number)
            .unwrap_or(Value::Null),
        RV::Boolean(b) => Value::Bool(*b),
        RV::Array(items) | RV::Set(items) => {
            Value::Array(items.iter().map(redis_to_json).collect())
        }
        RV::Map(pairs) => {
            let mut m = Map::new();
            for (k, val) in pairs {
                m.insert(result::cell_value_string(&redis_to_json(k)), redis_to_json(val));
            }
            Value::Object(m)
        }
        other => Value::String(format!("{other:?}")),
    }
}

fn as_string(v: Option<&Value>) -> String {
    match v {
        Some(Value::String(s)) => s.clone(),
        Some(Value::Null) | None => String::new(),
        Some(other) => result::cell_value_string(other),
    }
}

fn as_i64(v: Option<&Value>) -> Option<i64> {
    match v {
        Some(Value::Number(n)) => n.as_i64().or_else(|| n.as_f64().map(|f| f as i64)),
        Some(Value::String(s)) => s.parse().ok(),
        _ => None,
    }
}

fn as_f64(v: Option<&Value>) -> Option<f64> {
    match v {
        Some(Value::Number(n)) => n.as_f64(),
        Some(Value::String(s)) => s.parse().ok(),
        _ => None,
    }
}

/// Split a command line into args, honoring double quotes.
fn split_command(command: &str) -> Vec<String> {
    let mut fields = Vec::new();
    let mut current = String::new();
    let mut in_quote = false;
    for ch in command.trim().chars() {
        match ch {
            '"' => in_quote = !in_quote,
            ' ' if !in_quote => {
                if !current.is_empty() {
                    fields.push(std::mem::take(&mut current));
                }
            }
            _ => current.push(ch),
        }
    }
    if !current.is_empty() {
        fields.push(current);
    }
    fields
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn split_command_handles_quotes() {
        assert_eq!(split_command("SET key \"hello world\""), vec!["SET", "key", "hello world"]);
        assert_eq!(split_command("  GET  k "), vec!["GET", "k"]);
        assert!(split_command("").is_empty());
    }

    #[test]
    fn redis_value_serializes_with_type() {
        let v = RedisValue {
            key: "k".into(),
            type_: "string".into(),
            string: "hi".into(),
            preview_cap: 100,
            ..Default::default()
        };
        let j = serde_json::to_value(&v).unwrap();
        assert_eq!(j["type"], "string");
        assert_eq!(j["string"], "hi");
    }
}
