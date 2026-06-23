//! Editor syntax highlighting. Port of `internal/app/highlight.go`. The keyword
//! / function / command sets come from the vendored `suggest` data so
//! highlighting and completion share one source. Classification is per byte; it
//! never alters the text (contract §15).

use ratatui::style::{Color, Modifier, Style};
use ratatui::text::Span;

use crate::config::Driver;
use crate::suggest;

use super::theme;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SynClass {
    Plain,
    Kw,
    Fn,
    Str,
    Num,
    Com,
    Var,
    Pun,
    Key,
    Bool,
}

fn is_ident_byte(b: u8) -> bool {
    b == b'_' || b.is_ascii_alphanumeric()
}
fn is_ident_start(b: u8) -> bool {
    b == b'_' || b.is_ascii_alphabetic()
}

/// Per-byte lexical class for `text` under `driver` (None = SQL drivers via the
/// shared keyword/function sets). Unknown drivers get all-plain.
pub fn highlight_classes(driver: Option<Driver>, text: &str) -> Vec<SynClass> {
    let mut cls = vec![SynClass::Plain; text.len()];
    match driver {
        Some(Driver::Mysql) | Some(Driver::Doris) => {
            let d = driver.unwrap();
            highlight_sql(text, suggest::sql_keyword_set(d), suggest::sql_function_set(d), &mut cls);
        }
        Some(Driver::Redis) => highlight_redis(text, suggest::redis_command_set(), &mut cls),
        Some(Driver::Mongo) => highlight_mongo(text, suggest::mongo_method_set(), &mut cls),
        None => {}
    }
    cls
}

pub fn highlight_json_classes(text: &str) -> Vec<SynClass> {
    let mut cls = vec![SynClass::Plain; text.len()];
    highlight_json(text, &mut cls);
    cls
}

fn fill(cls: &mut [SynClass], start: usize, end: usize, c: SynClass) {
    let len = cls.len();
    for slot in cls.iter_mut().take(end.min(len)).skip(start) {
        *slot = c;
    }
}

fn scan_string(text: &[u8], mut i: usize) -> usize {
    let quote = text[i];
    i += 1;
    while i < text.len() {
        if text[i] == b'\\' && i + 1 < text.len() {
            i += 2;
            continue;
        }
        if text[i] == quote {
            if i + 1 < text.len() && text[i + 1] == quote {
                i += 2;
                continue;
            }
            return i + 1;
        }
        i += 1;
    }
    text.len()
}

fn scan_number(text: &[u8], mut i: usize) -> usize {
    while i < text.len() {
        let b = text[i];
        let prev_e = i > 0 && (text[i - 1] == b'e' || text[i - 1] == b'E');
        if b.is_ascii_digit()
            || b == b'.'
            || matches!(b, b'x' | b'X' | b'e' | b'E')
            || ((b == b'+' || b == b'-') && prev_e)
            || (b as char).is_ascii_hexdigit()
        {
            i += 1;
        } else {
            break;
        }
    }
    i
}

fn next_non_space(text: &[u8], mut i: usize) -> u8 {
    while i < text.len() {
        if !matches!(text[i], b' ' | b'\t' | b'\n') {
            return text[i];
        }
        i += 1;
    }
    0
}

const SQL_OPERATOR: &[u8] = b"+-*/%=<>!&|^~";
const SQL_PUNCT: &[u8] = b"(),;.";

fn highlight_sql(text: &str, keywords: &std::collections::HashSet<String>, functions: &std::collections::HashSet<String>, cls: &mut [SynClass]) {
    let t = text.as_bytes();
    let mut i = 0;
    while i < t.len() {
        let b = t[i];
        if (b == b'-' && i + 1 < t.len() && t[i + 1] == b'-') || b == b'#' {
            let end = text[i..].find('\n').map(|e| i + e).unwrap_or(t.len());
            fill(cls, i, end, SynClass::Com);
            i = end;
        } else if b == b'/' && i + 1 < t.len() && t[i + 1] == b'*' {
            let end = text[i + 2..].find("*/").map(|e| i + 2 + e + 2).unwrap_or(t.len());
            fill(cls, i, end, SynClass::Com);
            i = end;
        } else if b == b'\'' || b == b'"' {
            let end = scan_string(t, i);
            fill(cls, i, end, SynClass::Str);
            i = end;
        } else if b == b'`' {
            i = scan_string(t, i);
        } else if b.is_ascii_digit() {
            let end = scan_number(t, i);
            fill(cls, i, end, SynClass::Num);
            i = end;
        } else if is_ident_start(b) {
            let mut j = i;
            while j < t.len() && is_ident_byte(t[j]) {
                j += 1;
            }
            let word = text[i..j].to_uppercase();
            if keywords.contains(&word) {
                fill(cls, i, j, SynClass::Kw);
            } else if functions.contains(&word) || next_non_space(t, j) == b'(' {
                fill(cls, i, j, SynClass::Fn);
            }
            i = j;
        } else if SQL_OPERATOR.contains(&b) || SQL_PUNCT.contains(&b) {
            fill(cls, i, i + 1, SynClass::Pun);
            i += 1;
        } else {
            i += 1;
        }
    }
}

fn highlight_mongo(text: &str, methods: &std::collections::HashSet<String>, cls: &mut [SynClass]) {
    let t = text.as_bytes();
    let mut i = 0;
    while i < t.len() {
        let b = t[i];
        if b == b'/' && i + 1 < t.len() && t[i + 1] == b'/' {
            let end = text[i..].find('\n').map(|e| i + e).unwrap_or(t.len());
            fill(cls, i, end, SynClass::Com);
            i = end;
        } else if b == b'/' && i + 1 < t.len() && t[i + 1] == b'*' {
            let end = text[i + 2..].find("*/").map(|e| i + 2 + e + 2).unwrap_or(t.len());
            fill(cls, i, end, SynClass::Com);
            i = end;
        } else if b == b'\'' || b == b'"' {
            let end = scan_string(t, i);
            fill(cls, i, end, SynClass::Str);
            i = end;
        } else if b == b'$' {
            let mut j = i + 1;
            while j < t.len() && is_ident_byte(t[j]) {
                j += 1;
            }
            fill(cls, i, j, SynClass::Var);
            i = j;
        } else if b.is_ascii_digit() {
            let end = scan_number(t, i);
            fill(cls, i, end, SynClass::Num);
            i = end;
        } else if is_ident_start(b) {
            let mut j = i;
            while j < t.len() && is_ident_byte(t[j]) {
                j += 1;
            }
            if methods.contains(&text[i..j].to_uppercase()) {
                fill(cls, i, j, SynClass::Fn);
            }
            i = j;
        } else if b"{}[]():,".contains(&b) {
            fill(cls, i, i + 1, SynClass::Pun);
            i += 1;
        } else {
            i += 1;
        }
    }
}

fn highlight_redis(text: &str, commands: &std::collections::HashSet<String>, cls: &mut [SynClass]) {
    let t = text.as_bytes();
    let mut i = 0;
    let mut first = true;
    while i < t.len() {
        let b = t[i];
        if matches!(b, b' ' | b'\t' | b'\n') {
            i += 1;
        } else if b == b'\'' || b == b'"' {
            let end = scan_string(t, i);
            fill(cls, i, end, SynClass::Str);
            i = end;
            first = false;
        } else if b.is_ascii_digit() {
            let end = scan_number(t, i);
            fill(cls, i, end, SynClass::Num);
            i = end;
            first = false;
        } else {
            let mut j = i;
            while j < t.len() && !matches!(t[j], b' ' | b'\t' | b'\n') {
                j += 1;
            }
            if first && commands.contains(&text[i..j].to_uppercase()) {
                fill(cls, i, j, SynClass::Kw);
            }
            first = false;
            i = j;
        }
    }
}

fn highlight_json(text: &str, cls: &mut [SynClass]) {
    let t = text.as_bytes();
    let mut i = 0;
    while i < t.len() {
        let b = t[i];
        if b == b'"' {
            let end = scan_string(t, i);
            if next_non_space(t, end) == b':' {
                fill(cls, i, end, SynClass::Key);
            } else {
                fill(cls, i, end, SynClass::Str);
            }
            i = end;
        } else if b == b'-' || b.is_ascii_digit() {
            let start = i;
            if b == b'-' {
                i += 1;
            }
            let mut end = scan_number(t, i);
            if end <= start {
                end = start + 1;
            }
            fill(cls, start, end, SynClass::Num);
            i = end;
        } else if is_ident_start(b) {
            let mut j = i;
            while j < t.len() && is_ident_byte(t[j]) {
                j += 1;
            }
            let word = &text[i..j];
            if next_non_space(t, j) == b'(' {
                fill(cls, i, j, SynClass::Fn); // ObjectId(...) / ISODate(...)
            } else if matches!(word, "true" | "false" | "null") {
                fill(cls, i, j, SynClass::Bool);
            }
            i = j;
        } else if b"{}[],:".contains(&b) {
            fill(cls, i, i + 1, SynClass::Pun);
            i += 1;
        } else {
            i += 1;
        }
    }
}

fn style_for(c: SynClass) -> Option<Style> {
    let s = match c {
        SynClass::Kw => Style::default().fg(theme::ACCENT).add_modifier(Modifier::BOLD),
        SynClass::Fn => Style::default().fg(theme::VIOLET),
        SynClass::Str => Style::default().fg(theme::SUCCESS),
        SynClass::Num => Style::default().fg(theme::WARNING),
        SynClass::Com => Style::default().fg(theme::MUTED).add_modifier(Modifier::ITALIC),
        SynClass::Var => Style::default().fg(theme::KEY),
        SynClass::Pun => Style::default().fg(theme::MUTED),
        SynClass::Key => Style::default().fg(theme::INFO),
        SynClass::Bool => Style::default().fg(theme::VIOLET),
        SynClass::Plain => return None,
    };
    Some(s)
}

/// Build styled spans for one line of `text` given its per-byte classes,
/// grouping consecutive same-class bytes and snapping to char boundaries.
pub fn spans_for_line(text: &str, classes: &[SynClass]) -> Vec<Span<'static>> {
    let mut spans = Vec::new();
    let t = text.as_bytes();
    let mut i = 0;
    while i < t.len() {
        let c = classes.get(i).copied().unwrap_or(SynClass::Plain);
        let mut j = i + 1;
        while j < t.len() && classes.get(j).copied().unwrap_or(SynClass::Plain) == c && text.is_char_boundary(j) {
            j += 1;
        }
        // snap to char boundary
        while j < t.len() && !text.is_char_boundary(j) {
            j += 1;
        }
        let seg = text[i..j].to_string();
        match style_for(c) {
            Some(style) => spans.push(Span::styled(seg, style)),
            None => spans.push(Span::styled(seg, Style::default().fg(Color::Reset))),
        }
        i = j;
    }
    spans
}

#[cfg(test)]
mod tests {
    use super::*;

    fn classes(driver: Driver, text: &str) -> Vec<SynClass> {
        highlight_classes(Some(driver), text)
    }

    #[test]
    fn sql_keyword_and_function() {
        let text = "SELECT COUNT(*) FROM t";
        let c = classes(Driver::Mysql, text);
        assert_eq!(c[0], SynClass::Kw); // SELECT
        let count_idx = text.find("COUNT").unwrap();
        assert_eq!(c[count_idx], SynClass::Fn);
        let from_idx = text.find("FROM").unwrap();
        assert_eq!(c[from_idx], SynClass::Kw);
    }

    #[test]
    fn sql_number_and_comment() {
        let text = "select 42 -- note";
        let c = classes(Driver::Mysql, text);
        assert_eq!(c[text.find("42").unwrap()], SynClass::Num);
        assert_eq!(c[text.find("--").unwrap()], SynClass::Com);
    }

    #[test]
    fn mongo_var_and_method() {
        let text = "db.t.find({ $gt: 5 })";
        let c = classes(Driver::Mongo, text);
        assert_eq!(c[text.find("$gt").unwrap()], SynClass::Var);
        assert_eq!(c[text.find("find").unwrap()], SynClass::Fn);
        assert_eq!(c[text.find('5').unwrap()], SynClass::Num);
    }

    #[test]
    fn json_key_string_bool_and_objectid() {
        let text = r#"{"_id": ObjectId("x"), "ok": true, "n": 1}"#;
        let c = highlight_json_classes(text);
        assert_eq!(c[text.find("\"_id\"").unwrap()], SynClass::Key);
        assert_eq!(c[text.find("ObjectId").unwrap()], SynClass::Fn);
        assert_eq!(c[text.find("true").unwrap()], SynClass::Bool);
    }

    #[test]
    fn highlight_preserves_text_length() {
        // CJK must not break classification or span reconstruction (contract §1).
        let text = "select '真人' from t";
        let c = classes(Driver::Mysql, text);
        assert_eq!(c.len(), text.len());
        let spans = spans_for_line(text, &c);
        let rebuilt: String = spans.iter().map(|s| s.content.as_ref()).collect();
        assert_eq!(rebuilt, text);
    }
}
