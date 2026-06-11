// Package validate performs per-driver syntax/character checks on a query before
// it runs. It pairs the officially-maintained command/method sets (Redis,
// MongoDB) with a universal balanced-delimiter scan. (A real SQL grammar parser
// was tried but rejected: the available parser false-positives on valid
// MySQL/Doris queries, so only the never-false-positive checks remain.) Issues
// are advisory: the UI surfaces them as a non-blocking warning and may colour the
// offending span.
package validate

import (
	"strings"

	"tdb/internal/config"
	"tdb/internal/suggest"
)

type Severity int

const (
	SevWarning Severity = iota
	SevError
)

// Issue is a single problem located in the query text by byte offset/length.
type Issue struct {
	Offset   int
	Length   int
	Severity Severity
	Message  string
}

// Validate returns the issues found in text for the given driver. An empty slice
// means no problems were detected.
func Validate(driver config.Driver, text string) []Issue {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	// A balanced-delimiter scan applies to every driver and never false-positives
	// on valid input.
	issues := unbalancedDelimiters(text)
	switch driver {
	case config.DriverRedis:
		issues = append(issues, validateRedis(text)...)
	case config.DriverMongo:
		issues = append(issues, validateMongo(text)...)
	}
	return issues
}

func validateRedis(text string) []Issue {
	commands := suggest.RedisCommandSet()
	var issues []Issue
	offset := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			fields := strings.Fields(trimmed)
			cmd := strings.ToUpper(fields[0])
			if !commands[cmd] {
				start := offset + strings.Index(line, fields[0])
				issues = append(issues, Issue{
					Offset:   start,
					Length:   len(fields[0]),
					Severity: SevWarning,
					Message:  "redis: unknown command " + cmd,
				})
			}
		}
		offset += len(line)
	}
	return issues
}

func validateMongo(text string) []Issue {
	methods := suggest.MongoMethodSet()
	trimmed := strings.TrimSpace(text)
	// Only validate the shell call shape db.<collection>.<method>(...).
	if !strings.HasPrefix(trimmed, "db.") {
		return nil
	}
	open := strings.IndexByte(trimmed, '(')
	if open < 0 {
		return nil // still being typed
	}
	head := trimmed[:open]
	parts := strings.Split(head, ".")
	if len(parts) >= 3 {
		method := parts[len(parts)-1]
		if method != "" && !methods[strings.ToUpper(method)] {
			off := strings.LastIndex(text, method)
			return []Issue{{Offset: max0(off), Length: len(method), Severity: SevWarning, Message: "mongo: unknown method " + method}}
		}
	}
	return nil
}

// unbalancedDelimiters reports the first unmatched (){}[] or unterminated string.
func unbalancedDelimiters(text string) []Issue {
	var stack []struct {
		ch  byte
		pos int
	}
	pairs := map[byte]byte{')': '(', ']': '[', '}': '{'}
	var quote byte
	quoteStart := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
			quoteStart = i
		case '(', '[', '{':
			stack = append(stack, struct {
				ch  byte
				pos int
			}{c, i})
		case ')', ']', '}':
			if len(stack) == 0 || stack[len(stack)-1].ch != pairs[c] {
				return []Issue{{Offset: i, Length: 1, Severity: SevWarning, Message: "unbalanced " + string(c)}}
			}
			stack = stack[:len(stack)-1]
		}
	}
	if quote != 0 {
		return []Issue{{Offset: quoteStart, Length: 1, Severity: SevWarning, Message: "unterminated string"}}
	}
	if len(stack) > 0 {
		top := stack[len(stack)-1]
		return []Issue{{Offset: top.pos, Length: 1, Severity: SevWarning, Message: "unclosed " + string(top.ch)}}
	}
	return nil
}

func max0(i int) int {
	if i < 0 {
		return 0
	}
	return i
}
