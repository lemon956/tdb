package app

import (
	"context"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestCommandTabOpensLimitedSuggestionsAndEnterAcceptsOnly(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.focus = FocusSidebar

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	model = updated.(*Model)
	model.input.SetValue("he")

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(*Model)
	if !model.input.SuggestionsVisible() {
		t.Fatal("suggestions not visible after first Tab")
	}
	if got := len(model.input.Suggestions()); got == 0 || got > 5 {
		t.Fatalf("suggestion count = %d, want 1..5", got)
	}
	if got := model.input.SelectedSuggestion().Value; got != "help" {
		t.Fatalf("selected suggestion = %q, want help", got)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	if model.input.Value() != "help" {
		t.Fatalf("input after accept = %q, want help", model.input.Value())
	}
	if model.modal != nil {
		t.Fatalf("modal opened during suggestion accept: %+v", model.modal)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	if model.modal == nil || model.modal.Kind != modalHelp {
		t.Fatalf("modal after executing accepted command = %+v, want help modal", model.modal)
	}
}

func TestCommandTabAndArrowKeysCycleSuggestionMenu(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.focusCommand()
	model.input.SetValue("")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(*Model)
	first := model.input.SelectedSuggestion().Value
	if first == "" {
		t.Fatal("first suggestion is empty")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(*Model)
	second := model.input.SelectedSuggestion().Value
	if second == "" || second == first {
		t.Fatalf("second suggestion = %q, first = %q", second, first)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(*Model)
	third := model.input.SelectedSuggestion().Value
	if third == "" || third == second {
		t.Fatalf("third suggestion after down = %q, second = %q", third, second)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(*Model)
	if got := model.input.SelectedSuggestion().Value; got != second {
		t.Fatalf("selected after up = %q, want %q", got, second)
	}
}

func TestCommandExecutionRestoresPreviousFocus(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.focus = FocusSidebar

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	model = updated.(*Model)
	model.input.SetValue("connections")

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	if model.focus != FocusSidebar {
		t.Fatalf("focus after command execution = %s, want sidebar", model.focus)
	}
	if model.input.Value() != "" {
		t.Fatalf("input after command execution = %q, want cleared", model.input.Value())
	}
}

func TestHandleLineHelpStillOpensModal(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections

	model.HandleLine(context.Background(), "help")

	if model.modal == nil || model.modal.Kind != modalHelp {
		t.Fatalf("modal = %+v, want help", model.modal)
	}
}
