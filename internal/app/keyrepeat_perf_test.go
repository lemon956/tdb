package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/db"
	"tdb/internal/result"
)

func dataTabModel(t *testing.T) *Model {
	t.Helper()
	model := newWorkspaceVimModel(t)
	model.width = 64
	model.height = 16
	model.focus = FocusMain
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabData,
		Target:         db.Target{Database: "app", Name: "events", Type: db.ObjectCollection},
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Documents: []result.Document{
			{ID: "1", Data: map[string]any{"a": 1, "b": 2, "c": 3, "d": 4}},
			{ID: "2", Data: map[string]any{"a": 1, "b": 2, "c": 3, "d": 4}},
		}},
	}}
	model.activeTabIndex = 0
	return model
}

// Fix 1: a held navigation key arrives as one KeyMsg{Runes:['j','j','j']}. It must
// move the cursor multiple times and NOT raise "unsupported shortcut".
func TestRepeatedNavKeyMovesAndDoesNotError(t *testing.T) {
	model := dataTabModel(t)
	before := model.activeWorkspaceTab().ResultRow

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j', 'j', 'j'}})
	model = updated.(*Model)

	got := model.activeWorkspaceTab().ResultRow
	if got <= before {
		t.Fatalf("held 'jjj' should move the cursor down multiple rows, got row %d (was %d)", got, before)
	}
	if strings.Contains(model.message, "unsupported") {
		t.Fatalf("held nav key must not raise unsupported shortcut, message=%q", model.message)
	}
}

// Fix 1: in a text-input context a coalesced batch is inserted as text (not split
// into navigation), so a paste / fast typing lands intact.
func TestRepeatedRunesInCommandModeInsertAsText(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.focusCommand()
	if !model.inTextInputContext() {
		t.Fatal("command mode should be a text-input context")
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a', 'b', 'c'}})
	model = updated.(*Model)
	if got := model.input.Value(); got != "abc" {
		t.Fatalf("batch runes should insert as text, got %q", got)
	}
}

// Fix 2a: activeDataLines is memoized — a second call with the same result+width
// returns the cached slice; replacing the result invalidates it.
func TestActiveDataLinesCachedAndInvalidated(t *testing.T) {
	model := dataTabModel(t)
	tab := model.activeWorkspaceTab()

	a := model.activeDataLines(tab, 40)
	b := model.activeDataLines(tab, 40)
	if len(a) == 0 || len(b) == 0 {
		t.Fatal("expected non-empty data lines")
	}
	if &a[0] != &b[0] {
		t.Fatal("second activeDataLines call should return the cached slice")
	}

	tab.Result = result.Set{Documents: []result.Document{
		{ID: "9", Data: map[string]any{"z": 9}},
	}}
	c := model.activeDataLines(tab, 40)
	if len(c) == 0 {
		t.Fatal("expected lines after result change")
	}
	if &c[0] == &a[0] {
		t.Fatal("cache should invalidate when the result changes")
	}
}
