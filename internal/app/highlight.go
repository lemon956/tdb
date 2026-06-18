package app

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"tdb/internal/config"
	"tdb/internal/suggest"
)

// synClass is the lexical class of a byte in the query buffer, used to colorize
// the editor. The keyword/function/command sets come from the vendored syntax
// data (internal/suggest), so highlighting and completion share one source.
type synClass uint8

const (
	synPlain synClass = iota
	synKw
	synFn
	synStr
	synNum
	synCom
	synVar
	synPun
	synKey  // JSON object key ("name":)
	synBool // JSON true / false / null
)

func isIdentByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func isIdentStart(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// highlightClasses returns a per-byte lexical class for the buffer under the
// given driver. Unknown drivers get all-plain (no highlight).
func highlightClasses(driver config.Driver, text string) []synClass {
	cls := make([]synClass, len(text))
	switch driver {
	case config.DriverMySQL, config.DriverDoris:
		highlightSQL(text, suggest.SQLKeywordSet(driver), suggest.SQLFunctionSet(driver), cls)
	case config.DriverRedis:
		highlightRedis(text, suggest.RedisCommandSet(), cls)
	case config.DriverMongo:
		highlightMongo(text, suggest.MongoMethodSet(), cls)
	}
	return cls
}

func fill(cls []synClass, start, end int, c synClass) {
	for i := start; i < end && i < len(cls); i++ {
		cls[i] = c
	}
}

// scanString returns the index just past a quoted string starting at i (quote =
// text[i]). It tolerates doubled-quote and backslash escapes.
func scanString(text string, i int) int {
	quote := text[i]
	i++
	for i < len(text) {
		if text[i] == '\\' && i+1 < len(text) {
			i += 2
			continue
		}
		if text[i] == quote {
			if i+1 < len(text) && text[i+1] == quote { // doubled quote escape
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return len(text)
}

func scanNumber(text string, i int) int {
	for i < len(text) {
		b := text[i]
		if (b >= '0' && b <= '9') || b == '.' || b == 'x' || b == 'X' || b == 'e' || b == 'E' ||
			((b == '+' || b == '-') && i > 0 && (text[i-1] == 'e' || text[i-1] == 'E')) ||
			(b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F') {
			i++
			continue
		}
		break
	}
	return i
}

func nextNonSpace(text string, i int) byte {
	for i < len(text) {
		if text[i] != ' ' && text[i] != '\t' && text[i] != '\n' {
			return text[i]
		}
		i++
	}
	return 0
}

const sqlOperatorBytes = "+-*/%=<>!&|^~"
const sqlPunctBytes = "(),;."

func highlightSQL(text string, keywords, functions map[string]bool, cls []synClass) {
	i := 0
	for i < len(text) {
		b := text[i]
		switch {
		case b == '-' && i+1 < len(text) && text[i+1] == '-', b == '#':
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				end = len(text)
			} else {
				end = i + end
			}
			fill(cls, i, end, synCom)
			i = end
		case b == '/' && i+1 < len(text) && text[i+1] == '*':
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				end = len(text)
			} else {
				end = i + 2 + end + 2
			}
			fill(cls, i, end, synCom)
			i = end
		case b == '\'' || b == '"':
			end := scanString(text, i)
			fill(cls, i, end, synStr)
			i = end
		case b == '`':
			end := scanString(text, i)
			i = end // backtick identifiers stay plain
		case b >= '0' && b <= '9':
			end := scanNumber(text, i)
			fill(cls, i, end, synNum)
			i = end
		case isIdentStart(b):
			j := i
			for j < len(text) && isIdentByte(text[j]) {
				j++
			}
			word := strings.ToUpper(text[i:j])
			switch {
			case keywords[word]:
				fill(cls, i, j, synKw)
			case functions[word] || nextNonSpace(text, j) == '(':
				fill(cls, i, j, synFn)
			}
			i = j
		case strings.IndexByte(sqlOperatorBytes, b) >= 0:
			fill(cls, i, i+1, synPun)
			i++
		case strings.IndexByte(sqlPunctBytes, b) >= 0:
			fill(cls, i, i+1, synPun)
			i++
		default:
			i++
		}
	}
}

func highlightMongo(text string, methods map[string]bool, cls []synClass) {
	i := 0
	for i < len(text) {
		b := text[i]
		switch {
		case b == '/' && i+1 < len(text) && text[i+1] == '/':
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				end = len(text)
			} else {
				end = i + end
			}
			fill(cls, i, end, synCom)
			i = end
		case b == '/' && i+1 < len(text) && text[i+1] == '*':
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				end = len(text)
			} else {
				end = i + 2 + end + 2
			}
			fill(cls, i, end, synCom)
			i = end
		case b == '\'' || b == '"':
			end := scanString(text, i)
			fill(cls, i, end, synStr)
			i = end
		case b == '$':
			j := i + 1
			for j < len(text) && isIdentByte(text[j]) {
				j++
			}
			fill(cls, i, j, synVar)
			i = j
		case b >= '0' && b <= '9':
			end := scanNumber(text, i)
			fill(cls, i, end, synNum)
			i = end
		case isIdentStart(b):
			j := i
			for j < len(text) && isIdentByte(text[j]) {
				j++
			}
			if methods[strings.ToUpper(text[i:j])] {
				fill(cls, i, j, synFn)
			}
			i = j
		case strings.IndexByte("{}[]():,", b) >= 0:
			fill(cls, i, i+1, synPun)
			i++
		default:
			i++
		}
	}
}

func highlightRedis(text string, commands map[string]bool, cls []synClass) {
	i := 0
	first := true
	for i < len(text) {
		b := text[i]
		switch {
		case b == ' ' || b == '\t' || b == '\n':
			i++
		case b == '\'' || b == '"':
			end := scanString(text, i)
			fill(cls, i, end, synStr)
			i = end
			first = false
		case b >= '0' && b <= '9':
			end := scanNumber(text, i)
			fill(cls, i, end, synNum)
			i = end
			first = false
		default:
			j := i
			for j < len(text) && text[j] != ' ' && text[j] != '\t' && text[j] != '\n' {
				j++
			}
			if first && commands[strings.ToUpper(text[i:j])] {
				fill(cls, i, j, synKw)
			}
			first = false
			i = j
		}
	}
}

// synStyle returns the style for a class and whether it should be applied.
func (m *Model) synStyle(theme appTheme, c synClass) (lipgloss.Style, bool) {
	return synStyleFor(theme, c)
}

// synStyleFor is the Model-free style lookup so renderers without a Model (e.g.
// applyHighlight on JSON output) can colorize too.
func synStyleFor(theme appTheme, c synClass) (lipgloss.Style, bool) {
	switch c {
	case synKw:
		return theme.synKeyword, true
	case synFn:
		return theme.synFunction, true
	case synStr:
		return theme.synString, true
	case synNum:
		return theme.synNumber, true
	case synCom:
		return theme.synComment, true
	case synVar:
		return theme.synVariable, true
	case synPun:
		return theme.synPunct, true
	case synKey:
		return theme.synKey, true
	case synBool:
		return theme.synBool, true
	default:
		return lipgloss.Style{}, false
	}
}

// highlightJSON classifies pretty-printed JSON (post wrapMongoObjectIDs) into
// lexical spans: object keys vs string values, numbers, true/false/null, the
// {}[],: punctuation, and mongo shell constructors like ObjectId("…") / ISODate(…)
// (identifier immediately followed by "(") which render as functions.
func highlightJSON(text string, cls []synClass) {
	i := 0
	for i < len(text) {
		b := text[i]
		switch {
		case b == '"':
			end := scanString(text, i)
			if nextNonSpace(text, end) == ':' {
				fill(cls, i, end, synKey)
			} else {
				fill(cls, i, end, synStr)
			}
			i = end
		case b == '-' || (b >= '0' && b <= '9'):
			start := i
			if b == '-' {
				i++
			}
			end := scanNumber(text, i)
			if end <= start {
				end = start + 1
			}
			fill(cls, start, end, synNum)
			i = end
		case isIdentStart(b):
			j := i
			for j < len(text) && isIdentByte(text[j]) {
				j++
			}
			word := text[i:j]
			switch {
			case nextNonSpace(text, j) == '(':
				fill(cls, i, j, synFn) // ObjectId( / ISODate( / NumberLong( …
			case word == "true" || word == "false" || word == "null":
				fill(cls, i, j, synBool)
			}
			i = j
		case strings.IndexByte("{}[]():,", b) >= 0:
			fill(cls, i, i+1, synPun)
			i++
		default:
			i++
		}
	}
}

// jsonClasses tokenizes a whole JSON document into a per-byte class slice.
func jsonClasses(text string) []synClass {
	cls := make([]synClass, len(text))
	highlightJSON(text, cls)
	return cls
}

// applyHighlight composes text with its per-byte classes into an ANSI-styled
// string. Consecutive bytes of the same class are coalesced into one Render call
// — class boundaries always fall on rune boundaries (fill() colors whole tokens),
// so each segment is valid UTF-8 and CJK is emitted whole. Coalescing keeps the
// ANSI volume low for large JSON and avoids per-rune SGR resets. Colored tokens
// never span a newline (JSON escapes them), so split-by-line stays self-contained.
func applyHighlight(text string, classes []synClass, theme appTheme) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		c := classes[i]
		j := i
		for j < len(text) && classes[j] == c {
			j++
		}
		seg := text[i:j]
		if style, ok := synStyleFor(theme, c); ok {
			b.WriteString(style.Render(seg))
		} else {
			b.WriteString(seg)
		}
		i = j
	}
	return b.String()
}

// highlightJSONText is the convenience one-shot: tokenize + colorize JSON.
func highlightJSONText(text string, theme appTheme) string {
	return applyHighlight(text, jsonClasses(text), theme)
}
