package app

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/suggest"
)

// Tab / Shift+Tab scroll the completion popup in query insert mode.
func TestTabScrollsSuggestions(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.openQueryWorkspaceTab()
	tab := m.activeWorkspaceTab()
	tab.VimMode = vimModeInsert
	tab.WorkspaceFocus = workspaceFocusEditor
	tab.QuerySuggestions = []suggest.Suggestion{{Value: "a"}, {Value: "b"}, {Value: "c"}}
	tab.QuerySuggestionsVisible = true
	tab.QuerySuggestionIdx = 0

	m.handleQueryInsertKey(context.Background(), tab, tea.KeyMsg{Type: tea.KeyTab})
	if tab.QuerySuggestionIdx != 1 {
		t.Fatalf("Tab should advance the suggestion index, got %d", tab.QuerySuggestionIdx)
	}
	m.handleQueryInsertKey(context.Background(), tab, tea.KeyMsg{Type: tea.KeyShiftTab})
	if tab.QuerySuggestionIdx != 0 {
		t.Fatalf("Shift+Tab should move back, got %d", tab.QuerySuggestionIdx)
	}
}

// Ctrl+H / Ctrl+L switch panels outside text entry.
func TestCtrlHLSwitchPanels(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.page = PageBrowser
	m.focus = FocusSidebar

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = updated.(*Model)
	if m.focus != FocusMain {
		t.Fatalf("Ctrl+L should focus the main panel, got %s", m.focus)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	m = updated.(*Model)
	if m.focus != FocusSidebar {
		t.Fatalf("Ctrl+H should focus the sidebar, got %s", m.focus)
	}
}

// In a text-entry context (command mode) Ctrl+H deletes a word (Ctrl+Backspace),
// not switch panels; plain Backspace deletes a single character.
func TestCtrlHWordDeleteInTextEntry(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.page = PageBrowser
	m.focusCommand()
	if m.focus != FocusCommand {
		t.Fatalf("expected command focus, got %s", m.focus)
	}
	m.input.SetValue("foo bar")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	m = updated.(*Model)
	if m.focus != FocusCommand {
		t.Fatalf("Ctrl+H in command mode must not switch panels, focus=%s", m.focus)
	}
	if got := m.input.Value(); got != "foo " {
		t.Fatalf("Ctrl+H should delete the previous word, got %q", got)
	}

	// Plain Backspace still deletes one character.
	m.input.SetValue("ab")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(*Model)
	if got := m.input.Value(); got != "a" {
		t.Fatalf("Backspace should delete one character, got %q", got)
	}
}
