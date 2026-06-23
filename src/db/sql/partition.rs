//! Parse partition / key clauses out of `SHOW CREATE TABLE` DDL. Port of
//! `internal/db/sqladapter/partition.go`.
//!
//! Examples produced:
//!   partition: "RANGE(event_date)" / "LIST(region)" / "(event_date)"
//!   key:       "DUPLICATE KEY(id, event_date)" / "PRIMARY KEY(id)"

use once_cell::sync::Lazy;
use regex::Regex;

static PARTITION_BY_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"(?is)PARTITION\s+BY\s+([A-Z]+)?(?:\s+COLUMNS)?\s*\(").unwrap());
static KEY_CLAUSE_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"(?is)(DUPLICATE|UNIQUE|AGGREGATE|PRIMARY)\s+KEY\s*\(").unwrap());
static IDENTIFIER_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"[`"]?([A-Za-z_][A-Za-z0-9_]*)[`"]?"#).unwrap());

/// Extract `(partition, key)` from DDL; either may be empty.
pub fn parse_create_table_shape(ddl: &str) -> (String, String) {
    (parse_partition_clause(ddl), parse_key_clause(ddl))
}

fn parse_partition_clause(ddl: &str) -> String {
    let Some(caps) = PARTITION_BY_RE.captures(ddl) else {
        return String::new();
    };
    let whole = caps.get(0).unwrap();
    let method = caps
        .get(1)
        .map(|m| m.as_str().trim().to_uppercase())
        .unwrap_or_default();
    // The matched "(" is the last byte of the overall match.
    let open_idx = whole.end() - 1;
    let Some(inner) = balanced_paren(ddl, open_idx) else {
        return String::new();
    };
    let cols = column_identifiers(inner);
    if cols.is_empty() {
        return String::new();
    }
    let joined = cols.join(", ");
    if method.is_empty() {
        format!("({joined})")
    } else {
        format!("{method}({joined})")
    }
}

fn parse_key_clause(ddl: &str) -> String {
    let Some(caps) = KEY_CLAUSE_RE.captures(ddl) else {
        return String::new();
    };
    let kind = caps.get(1).unwrap().as_str().trim().to_uppercase();
    let open_idx = caps.get(0).unwrap().end() - 1;
    let Some(inner) = balanced_paren(ddl, open_idx) else {
        return String::new();
    };
    let cols = column_identifiers(inner);
    if cols.is_empty() {
        return String::new();
    }
    format!("{kind} KEY({})", cols.join(", "))
}

/// Substring inside the parentheses beginning at `open_idx` (must point at
/// '('), honoring nesting. Returns None on imbalance.
fn balanced_paren(s: &str, open_idx: usize) -> Option<&str> {
    let bytes = s.as_bytes();
    if open_idx >= bytes.len() || bytes[open_idx] != b'(' {
        return None;
    }
    let mut depth = 0i32;
    let mut i = open_idx;
    while i < bytes.len() {
        match bytes[i] {
            b'(' => depth += 1,
            b')' => {
                depth -= 1;
                if depth == 0 {
                    return Some(&s[open_idx + 1..i]);
                }
            }
            _ => {}
        }
        i += 1;
    }
    None
}

/// Bare column identifiers from a partition/key column list, dropping function
/// names and noise keywords (`to_days(\`d\`)` → "d"), preserving order, deduped.
fn column_identifiers(inner: &str) -> Vec<String> {
    let mut cols = Vec::new();
    let mut seen = std::collections::HashSet::new();
    for part in split_top_level(inner) {
        let part = part.trim();
        if part.is_empty() {
            continue;
        }
        let matches: Vec<_> = IDENTIFIER_RE.captures_iter(part).collect();
        let Some(last) = matches.last() else {
            continue;
        };
        let name = last.get(1).unwrap().as_str();
        let lower = name.to_lowercase();
        if name.is_empty() || is_sql_noise(&lower) || seen.contains(&lower) {
            continue;
        }
        seen.insert(lower);
        cols.push(name.to_string());
    }
    cols
}

/// Split on commas not inside nested parentheses.
fn split_top_level(s: &str) -> Vec<&str> {
    let bytes = s.as_bytes();
    let mut out = Vec::new();
    let mut depth = 0i32;
    let mut start = 0usize;
    for i in 0..bytes.len() {
        match bytes[i] {
            b'(' => depth += 1,
            b')' => depth -= 1,
            b',' if depth == 0 => {
                out.push(&s[start..i]);
                start = i + 1;
            }
            _ => {}
        }
    }
    out.push(&s[start..]);
    out
}

fn is_sql_noise(lower: &str) -> bool {
    matches!(lower, "asc" | "desc")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_range_partition_and_duplicate_key() {
        let ddl = "CREATE TABLE `t` (...) ENGINE=OLAP\nDUPLICATE KEY(`id`, `event_date`)\nPARTITION BY RANGE(`event_date`) ()";
        let (p, k) = parse_create_table_shape(ddl);
        assert_eq!(p, "RANGE(event_date)");
        assert_eq!(k, "DUPLICATE KEY(id, event_date)");
    }

    #[test]
    fn parses_expression_partition_to_inner_identifier() {
        let ddl = "CREATE TABLE x (...) PARTITION BY RANGE (to_days(`d`)) ()";
        let (p, _) = parse_create_table_shape(ddl);
        assert_eq!(p, "RANGE(d)");
    }

    #[test]
    fn parses_primary_key() {
        let (_, k) = parse_create_table_shape("CREATE TABLE x (id int, PRIMARY KEY (`id`))");
        assert_eq!(k, "PRIMARY KEY(id)");
    }

    #[test]
    fn no_partition_no_key() {
        let (p, k) = parse_create_table_shape("CREATE TABLE x (id int)");
        assert!(p.is_empty());
        assert!(k.is_empty());
    }
}
