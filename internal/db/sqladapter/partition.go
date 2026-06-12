package sqladapter

import (
	"regexp"
	"strings"
)

// SHOW CREATE TABLE DDL clauses we care about for AI/query hints:
//
//	PARTITION BY RANGE(`event_date`)                 (Doris/MySQL)
//	PARTITION BY LIST(`region`)
//	PARTITION BY RANGE (to_days(`d`))                (MySQL expression)
//	DUPLICATE KEY(`id`, `event_date`)                (Doris)
//	UNIQUE KEY(`id`) / AGGREGATE KEY(`id`)           (Doris)
//	PRIMARY KEY (`id`)                               (MySQL/Doris)
var (
	partitionByRe = regexp.MustCompile(`(?is)PARTITION\s+BY\s+([A-Z]+)?(?:\s+COLUMNS)?\s*\(`)
	keyClauseRe   = regexp.MustCompile(`(?is)(DUPLICATE|UNIQUE|AGGREGATE|PRIMARY)\s+KEY\s*\(`)
)

// parseCreateTableShape extracts a table's partition and key columns from its
// SHOW CREATE TABLE DDL, as compact human/AI-readable strings. Either may be
// empty when the table has no partitioning or no declared key. Results are used
// both to enrich AI schema context and to display in the table metadata view.
//
// partition: e.g. "RANGE(event_date)" or "LIST(region)" or "(event_date)".
// key:       e.g. "DUPLICATE KEY(id, event_date)" or "PRIMARY KEY(id)".
func parseCreateTableShape(ddl string) (partition, key string) {
	return parsePartitionClause(ddl), parseKeyClause(ddl)
}

func parsePartitionClause(ddl string) string {
	loc := partitionByRe.FindStringSubmatchIndex(ddl)
	if loc == nil {
		return ""
	}
	// Partition method (RANGE/LIST/HASH/…) if present; loc[2:4] is group 1.
	method := ""
	if loc[2] >= 0 {
		method = strings.ToUpper(strings.TrimSpace(ddl[loc[2]:loc[3]]))
	}
	// The matched "(" is the last byte of the overall match (loc[1]-1).
	inner, ok := balancedParen(ddl, loc[1]-1)
	if !ok {
		return ""
	}
	cols := columnIdentifiers(inner)
	if len(cols) == 0 {
		return ""
	}
	joined := strings.Join(cols, ", ")
	if method == "" {
		return "(" + joined + ")"
	}
	return method + "(" + joined + ")"
}

func parseKeyClause(ddl string) string {
	loc := keyClauseRe.FindStringSubmatchIndex(ddl)
	if loc == nil {
		return ""
	}
	kind := strings.ToUpper(strings.TrimSpace(ddl[loc[2]:loc[3]]))
	inner, ok := balancedParen(ddl, loc[1]-1)
	if !ok {
		return ""
	}
	cols := columnIdentifiers(inner)
	if len(cols) == 0 {
		return ""
	}
	return kind + " KEY(" + strings.Join(cols, ", ") + ")"
}

// balancedParen returns the substring inside the parentheses that begins at
// openIdx (which must point at '('), honoring nesting so an expression like
// to_days(`d`) is captured whole. ok is false when the parenthesis is unbalanced.
func balancedParen(s string, openIdx int) (string, bool) {
	if openIdx < 0 || openIdx >= len(s) || s[openIdx] != '(' {
		return "", false
	}
	depth := 0
	for i := openIdx; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[openIdx+1 : i], true
			}
		}
	}
	return "", false
}

var identifierRe = regexp.MustCompile("[`\"]?([A-Za-z_][A-Za-z0-9_]*)[`\"]?")

// columnIdentifiers pulls the bare column identifiers out of a partition/key
// column list, dropping function names and SQL keywords (e.g. to_days(`d`) →
// "d"). It splits on commas at the top level so multi-column lists keep order.
func columnIdentifiers(inner string) []string {
	var cols []string
	seen := map[string]bool{}
	for _, part := range splitTopLevel(inner) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// For an expression like to_days(`d`) take the inner-most identifier; for
		// a plain `col` it is the identifier itself. Pick the last identifier match
		// (the column, not the function name).
		matches := identifierRe.FindAllStringSubmatch(part, -1)
		if len(matches) == 0 {
			continue
		}
		name := matches[len(matches)-1][1]
		if name == "" || isSQLNoise(name) || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		cols = append(cols, name)
	}
	return cols
}

// splitTopLevel splits on commas that are not inside nested parentheses.
func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

// isSQLNoise drops common function/keyword tokens that are not column names.
func isSQLNoise(name string) bool {
	switch strings.ToLower(name) {
	case "asc", "desc":
		return true
	}
	return false
}
