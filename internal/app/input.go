package app

import (
	"strings"

	"tdb/internal/suggest"
)

type CommandInput struct {
	value       string
	suggestions []suggest.Suggestion
	selected    int
	visible     bool
}

func NewCommandInput() CommandInput {
	return CommandInput{}
}

func (i *CommandInput) Value() string {
	return i.value
}

func (i *CommandInput) SetValue(value string) {
	i.value = value
	i.HideSuggestions()
}

func (i *CommandInput) Insert(text string) {
	i.value += sanitizeSingleLineInput(text)
	i.HideSuggestions()
}

// sanitizeMultilineInput normalizes pasted/typed text for a multi-line editor:
// CR/CRLF become LF and other control characters (except newline and tab) are
// dropped, so a paste can never emit a raw CR that escapes the input box.
func sanitizeMultilineInput(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return stripControl(s, true)
}

// sanitizeSingleLineInput collapses any whitespace breaks (CR/LF/Tab) to spaces
// and drops other control characters, keeping pasted text on one line.
func sanitizeSingleLineInput(s string) string {
	repl := strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ")
	return stripControl(repl.Replace(s), false)
}

func stripControl(s string, keepBreaks bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if keepBreaks && (r == '\n' || r == '\t') {
			b.WriteRune(r)
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// flattenStatement renders a possibly multi-line statement on a single line for
// list displays, marking line breaks with a visible glyph.
func flattenStatement(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	return s
}

func (i *CommandInput) Backspace() {
	if len(i.value) == 0 {
		return
	}
	i.value = i.value[:len(i.value)-1]
	i.HideSuggestions()
}

func (i *CommandInput) Clear() {
	i.value = ""
	i.HideSuggestions()
}

func (i *CommandInput) SetSuggestions(suggestions []suggest.Suggestion) {
	i.suggestions = suggestions
	i.selected = 0
	i.visible = len(suggestions) > 0
}

func (i *CommandInput) HideSuggestions() {
	i.visible = false
}

func (i *CommandInput) ToggleSuggestions(suggestions []suggest.Suggestion) {
	if i.visible {
		i.HideSuggestions()
		return
	}
	i.SetSuggestions(suggestions)
}

func (i *CommandInput) SuggestionsVisible() bool {
	return i.visible && len(i.suggestions) > 0
}

func (i *CommandInput) Suggestions() []suggest.Suggestion {
	return i.suggestions
}

func (i *CommandInput) SelectedIndex() int {
	if len(i.suggestions) == 0 {
		return -1
	}
	return i.selected
}

func (i *CommandInput) SelectedSuggestion() suggest.Suggestion {
	if len(i.suggestions) == 0 {
		return suggest.Suggestion{}
	}
	if i.selected < 0 || i.selected >= len(i.suggestions) {
		i.selected = 0
	}
	return i.suggestions[i.selected]
}

func (i *CommandInput) NextSuggestion() {
	if len(i.suggestions) == 0 {
		return
	}
	i.visible = true
	i.selected = (i.selected + 1) % len(i.suggestions)
}

func (i *CommandInput) PreviousSuggestion() {
	if len(i.suggestions) == 0 {
		return
	}
	i.visible = true
	i.selected--
	if i.selected < 0 {
		i.selected = len(i.suggestions) - 1
	}
}

func (i *CommandInput) AcceptSuggestion() bool {
	if !i.SuggestionsVisible() {
		return false
	}
	selected := i.SelectedSuggestion()
	if selected.Value == "" {
		return false
	}
	i.value = replaceCurrentToken(i.value, selected.Value)
	i.HideSuggestions()
	return true
}

func replaceCurrentToken(input, replacement string) string {
	input = strings.TrimRight(input, " \t\n")
	start := len(input)
	for start > 0 {
		r := input[start-1]
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '$' {
			start--
			continue
		}
		break
	}
	return input[:start] + replacement
}
