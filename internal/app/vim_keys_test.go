package app

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/config"
	"tdb/internal/db"
)

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func editorTab(t *testing.T, buffer string) (*Model, *workspaceTab) {
	t.Helper()
	m := newWorkspaceVimModel(t)
	m.openQueryWorkspaceTab()
	tab := m.activeWorkspaceTab()
	tab.QueryBuffer = buffer
	tab.VimMode = vimModeNormal
	tab.WorkspaceFocus = workspaceFocusEditor
	return m, tab
}

func TestEditorJKMoveBetweenLines(t *testing.T) {
	m, tab := editorTab(t, "abc\ndefg")
	tab.QueryCursor = 1 // line 0, column 1

	m.handleQueryNormalKey(context.Background(), tab, key("j"))
	if tab.QueryCursor != 5 { // line 1, column 1 ('e')
		t.Fatalf("j should keep the column on the next line, cursor=%d", tab.QueryCursor)
	}
	m.handleQueryNormalKey(context.Background(), tab, key("k"))
	if tab.QueryCursor != 1 {
		t.Fatalf("k should return to the original column, cursor=%d", tab.QueryCursor)
	}
}

func TestEditorAppendAndInsertLine(t *testing.T) {
	m, tab := editorTab(t, "ab\ncd")
	tab.QueryCursor = 0
	m.handleQueryNormalKey(context.Background(), tab, key("A"))
	if tab.VimMode != vimModeInsert {
		t.Fatalf("A should enter insert mode, got %q", tab.VimMode)
	}
	if tab.QueryCursor != 2 { // end of "ab", before the newline
		t.Fatalf("A should move to line end, cursor=%d", tab.QueryCursor)
	}

	tab.VimMode = vimModeNormal
	tab.QueryCursor = 4 // somewhere on line 1
	m.handleQueryNormalKey(context.Background(), tab, key("I"))
	if tab.QueryCursor != 3 { // start of "cd"
		t.Fatalf("I should move to line start, cursor=%d", tab.QueryCursor)
	}
}

func TestEditorJoinLines(t *testing.T) {
	m, tab := editorTab(t, "select 1\n  from t")
	tab.QueryCursor = 0
	m.handleQueryNormalKey(context.Background(), tab, key("J"))
	if tab.QueryBuffer != "select 1 from t" {
		t.Fatalf("J should join lines with a single space, got %q", tab.QueryBuffer)
	}
}

func TestSidebarGotoTopBottom(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.page = PageConnections
	m.vault.Profiles = []config.Profile{
		{ID: "a", Driver: config.DriverMySQL},
		{ID: "b", Driver: config.DriverMySQL},
		{ID: "c", Driver: config.DriverMySQL},
	}
	m.connectionIndex = 1

	m.handleRuneKey(context.Background(), "G")
	if m.connectionIndex != 2 {
		t.Fatalf("G should jump to the last connection, got %d", m.connectionIndex)
	}
	m.handleRuneKey(context.Background(), "g")
	m.handleRuneKey(context.Background(), "g")
	if m.connectionIndex != 0 {
		t.Fatalf("gg should jump to the first connection, got %d", m.connectionIndex)
	}
	if m.navPendingG {
		t.Fatal("pending-g should be cleared after gg")
	}
}

// A lone g followed by another key must not jump.
func TestSidebarLoneGDoesNotJump(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.page = PageBrowser
	m.focus = FocusSidebar
	m.databaseObjects = map[string][]db.Object{}
	m.handleRuneKey(context.Background(), "g")
	if !m.navPendingG {
		t.Fatal("a single g should arm pending-g")
	}
	m.handleRuneKey(context.Background(), "j")
	if m.navPendingG {
		t.Fatal("a non-g key should clear pending-g")
	}
}
