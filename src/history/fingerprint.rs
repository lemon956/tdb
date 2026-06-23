//! Statement fingerprint: normalize so queries differing only in literal values
//! collapse to one merge key. Port of `internal/history/fingerprint.go`.

use once_cell::sync::Lazy;
use regex::Regex;

static STRING_LITERAL: Lazy<Regex> = Lazy::new(|| Regex::new(r#"'[^']*'|"[^"]*""#).unwrap());
static NUMBER_LITERAL: Lazy<Regex> = Lazy::new(|| Regex::new(r"-?\b\d+(\.\d+)?\b").unwrap());
static WHITESPACE: Lazy<Regex> = Lazy::new(|| Regex::new(r"\s+").unwrap());

pub fn fingerprint(statement: &str) -> String {
    let s = statement.trim().trim_end_matches(';').trim();
    if s.is_empty() {
        return String::new();
    }
    let s = STRING_LITERAL.replace_all(s, "?");
    let s = NUMBER_LITERAL.replace_all(&s, "?");
    let s = WHITESPACE.replace_all(&s, " ");
    s.trim().to_lowercase()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn collapses_literals() {
        assert_eq!(
            fingerprint("SELECT * FROM t WHERE id = 1"),
            fingerprint("select * from t where id = 2;")
        );
    }

    #[test]
    fn keeps_identifier_digits() {
        // digits embedded in identifiers are not numeric literals... but the Go
        // regex uses \b so users2 -> "users?"? Verify parity with Go behavior:
        // \b\d+ matches the "2" in "users2" because there's a word boundary
        // between 's' and '2'? No: \b is between non-word/word; 's2' has no
        // boundary, so "2" is NOT matched. Confirm it stays.
        let fp = fingerprint("select * from users2");
        assert!(fp.contains("users2"), "{fp}");
    }

    #[test]
    fn empty_statement() {
        assert_eq!(fingerprint("  ;  "), "");
    }
}
