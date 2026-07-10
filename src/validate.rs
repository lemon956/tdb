//! Non-blocking, never-false-positive query checks. Port of `internal/validate`.
//! Pairs a universal balanced-delimiter scan with the official Redis/Mongo
//! command sets; issues are advisory (shown as warnings, not blocking).

use crate::config::Driver;
use crate::suggest;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Severity {
    Warning,
    Error,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Issue {
    pub offset: usize,
    pub length: usize,
    pub severity: Severity,
    pub message: String,
}

pub fn validate(driver: Driver, text: &str) -> Vec<Issue> {
    if text.trim().is_empty() {
        return Vec::new();
    }
    let mut issues = unbalanced_delimiters(text);
    match driver {
        Driver::Redis => issues.extend(validate_redis(text)),
        Driver::Mongo => issues.extend(validate_mongo(text)),
        _ => {}
    }
    issues
}

fn validate_redis(text: &str) -> Vec<Issue> {
    let commands = suggest::redis_command_set();
    let mut issues = Vec::new();
    let mut offset = 0usize;
    for line in text.split_inclusive('\n') {
        let trimmed = line.trim();
        if !trimmed.is_empty() && !trimmed.starts_with('#') {
            let first = trimmed.split_whitespace().next().unwrap_or("");
            let cmd = first.to_uppercase();
            if !commands.contains(&cmd) {
                let start = offset + line.find(first).unwrap_or(0);
                issues.push(Issue {
                    offset: start,
                    length: first.len(),
                    severity: Severity::Warning,
                    message: format!("redis: unknown command {cmd}"),
                });
            }
        }
        offset += line.len();
    }
    issues
}

fn validate_mongo(text: &str) -> Vec<Issue> {
    let methods = suggest::mongo_method_set();
    let trimmed = text.trim();
    if !trimmed.starts_with("db.") {
        return Vec::new();
    }
    let Some(open) = trimmed.find('(') else { return Vec::new() };
    let head = &trimmed[..open];
    let parts: Vec<&str> = head.split('.').collect();
    if parts.len() >= 3 {
        let method = *parts.last().unwrap();
        if !method.is_empty() && !methods.contains(&method.to_uppercase()) {
            let off = text.rfind(method).unwrap_or(0);
            return vec![Issue {
                offset: off,
                length: method.len(),
                severity: Severity::Warning,
                message: format!("mongo: unknown method {method}"),
            }];
        }
    }
    if let Err(err) = crate::db::mongo::parse_mongosh_command(trimmed) {
        return vec![Issue {
            offset: text.find(trimmed).unwrap_or(0),
            length: trimmed.len().max(1),
            severity: Severity::Error,
            message: format!("mongo: {err}"),
        }];
    }
    Vec::new()
}

fn unbalanced_delimiters(text: &str) -> Vec<Issue> {
    let bytes = text.as_bytes();
    let mut stack: Vec<(u8, usize)> = Vec::new();
    let mut quote = 0u8;
    let mut quote_start = 0usize;
    let opener = |c: u8| match c {
        b')' => b'(',
        b']' => b'[',
        b'}' => b'{',
        _ => 0,
    };
    for (i, &c) in bytes.iter().enumerate() {
        if quote != 0 {
            if c == quote {
                quote = 0;
            }
            continue;
        }
        match c {
            b'\'' | b'"' | b'`' => {
                quote = c;
                quote_start = i;
            }
            b'(' | b'[' | b'{' => stack.push((c, i)),
            b')' | b']' | b'}' => {
                if stack.last().map(|(ch, _)| *ch) != Some(opener(c)) {
                    return vec![Issue {
                        offset: i,
                        length: 1,
                        severity: Severity::Warning,
                        message: format!("unbalanced {}", c as char),
                    }];
                }
                stack.pop();
            }
            _ => {}
        }
    }
    if quote != 0 {
        return vec![Issue {
            offset: quote_start,
            length: 1,
            severity: Severity::Warning,
            message: "unterminated string".into(),
        }];
    }
    if let Some((ch, pos)) = stack.last() {
        return vec![Issue {
            offset: *pos,
            length: 1,
            severity: Severity::Warning,
            message: format!("unclosed {}", *ch as char),
        }];
    }
    Vec::new()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn balanced_ok() {
        assert!(validate(Driver::Mysql, "select * from t where (a=1)").is_empty());
    }

    #[test]
    fn detects_unbalanced() {
        let issues = validate(Driver::Mysql, "select * from t where (a=1");
        assert_eq!(issues.len(), 1);
        assert!(issues[0].message.contains("unclosed"));
    }

    #[test]
    fn detects_unterminated_string() {
        let issues = validate(Driver::Mysql, "select 'abc");
        assert_eq!(issues[0].message, "unterminated string");
    }

    #[test]
    fn redis_unknown_command_warns() {
        let issues = validate(Driver::Redis, "FOObar key");
        assert!(issues.iter().any(|i| i.message.contains("unknown command")));
    }

    #[test]
    fn redis_known_command_ok() {
        assert!(validate(Driver::Redis, "GET key").is_empty());
    }

    #[test]
    fn mongo_unknown_method_warns() {
        let issues = validate(Driver::Mongo, "db.users.frobnicate({})");
        assert!(issues.iter().any(|i| i.message.contains("unknown method")));
    }

    #[test]
    fn mongo_malformed_filter_is_error() {
        let issues = validate(Driver::Mongo, r#"db.users.find({profile.name: "Ada"})"#);

        assert_eq!(issues[0].severity, Severity::Error);
        assert!(issues[0].message.contains("profile.name"));
    }

    #[test]
    fn mongo_quoted_dotted_filter_is_ok() {
        let issues = validate(Driver::Mongo, r#"db.users.find({"profile.name": "Ada"})"#);

        assert!(issues.is_empty(), "{issues:?}");
    }
}
