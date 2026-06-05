package app

import (
	"strings"
	"testing"

	"tdb/internal/suggest"
)

func TestSanitizeMultilineInputNormalizesCR(t *testing.T) {
	got := sanitizeMultilineInput("SELECT *\r\nFROM t\rLIMIT 1;\x00")
	if strings.ContainsAny(got, "\r\x00") {
		t.Fatalf("sanitized multi-line text still has CR/control: %q", got)
	}
	if got != "SELECT *\nFROM t\nLIMIT 1;" {
		t.Fatalf("got %q, want CRLF/CR folded to LF", got)
	}
}

func TestSanitizeSingleLineInputCollapsesBreaks(t *testing.T) {
	got := sanitizeSingleLineInput("a\r\nb\tc\x07")
	if strings.ContainsAny(got, "\r\n\t\x07") {
		t.Fatalf("single-line text still has breaks/control: %q", got)
	}
	if got != "a b c" {
		t.Fatalf("got %q, want 'a b c'", got)
	}
}

func TestCommandInputInsertStaysSingleLine(t *testing.T) {
	input := NewCommandInput()
	input.Insert("one\r\ntwo")
	if v := input.Value(); strings.ContainsAny(v, "\r\n") {
		t.Fatalf("command input value has line breaks: %q", v)
	}
}

func TestFlattenStatementIsSingleLine(t *testing.T) {
	got := flattenStatement("SELECT *\r\nFROM t\nLIMIT 1;")
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("flattened statement still multi-line: %q", got)
	}
	if !strings.Contains(got, "SELECT") || !strings.Contains(got, "LIMIT 1;") {
		t.Fatalf("flattened statement lost content: %q", got)
	}
}

func TestInputAcceptsSelectedSuggestionByReplacingCurrentToken(t *testing.T) {
	input := NewCommandInput()
	input.SetValue("sel")
	input.SetSuggestions([]suggest.Suggestion{{Value: "SELECT"}})

	input.AcceptSuggestion()

	if input.Value() != "SELECT" {
		t.Fatalf("Value() = %q, want SELECT", input.Value())
	}
	if input.SuggestionsVisible() {
		t.Fatal("suggestions still visible after accept")
	}
}

func TestInputCyclesSuggestionSelection(t *testing.T) {
	input := NewCommandInput()
	input.SetSuggestions([]suggest.Suggestion{{Value: "GET"}, {Value: "SET"}, {Value: "DEL"}})

	input.NextSuggestion()
	input.NextSuggestion()
	if got := input.SelectedSuggestion().Value; got != "DEL" {
		t.Fatalf("selected suggestion = %q, want DEL", got)
	}

	input.PreviousSuggestion()
	if got := input.SelectedSuggestion().Value; got != "SET" {
		t.Fatalf("selected suggestion = %q, want SET", got)
	}
}

func TestInputBackspaceClearsSuggestions(t *testing.T) {
	input := NewCommandInput()
	input.SetValue("GET")
	input.SetSuggestions([]suggest.Suggestion{{Value: "GET"}})

	input.Backspace()

	if input.Value() != "GE" {
		t.Fatalf("Value() = %q, want GE", input.Value())
	}
	if input.SuggestionsVisible() {
		t.Fatal("suggestions still visible after edit")
	}
}
