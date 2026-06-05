package history

import (
	"regexp"
	"strings"
)

var (
	// fpStringLiteral matches single- or double-quoted string literals.
	fpStringLiteral = regexp.MustCompile(`'[^']*'|"[^"]*"`)
	// fpNumberLiteral matches standalone numeric literals; the word boundaries
	// keep it from touching digits embedded in identifiers (users2, table_1).
	fpNumberLiteral = regexp.MustCompile(`-?\b\d+(\.\d+)?\b`)
	// fpWhitespace collapses runs of whitespace.
	fpWhitespace = regexp.MustCompile(`\s+`)
)

// Fingerprint normalizes a statement so that queries differing only in literal
// values (condition variables) collapse to the same key. It lowercases, strips
// a trailing semicolon, replaces string/number literals with `?`, and collapses
// whitespace. It is used only as a merge key — the original statement text is
// kept for display.
func Fingerprint(statement string) string {
	s := strings.TrimSpace(statement)
	s = strings.TrimRight(s, ";")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = fpStringLiteral.ReplaceAllString(s, "?")
	s = fpNumberLiteral.ReplaceAllString(s, "?")
	s = fpWhitespace.ReplaceAllString(s, " ")
	return strings.ToLower(strings.TrimSpace(s))
}
