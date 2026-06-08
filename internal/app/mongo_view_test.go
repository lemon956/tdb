package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/result"
)

func sampleDocs() []result.Document {
	return []result.Document{
		{ID: "1", Data: map[string]any{"_id": "1", "name": "ada", "meta": map[string]any{"age": 30}}},
		{ID: "2", Data: map[string]any{"_id": "2", "name": "grace"}},
	}
}

// Mongo find/aggregate results stay as Documents and render as JSON (the same
// style as the sidebar data-browse page), not a column grid.
func TestMongoQueryResultRendersJSON(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.openQueryWorkspaceTab()
	tabID := m.activeWorkspaceTab().ID

	res := result.Set{Documents: sampleDocs()}
	m.applyQueryResult(tabID, "db.c.find({})", "app", "p", "mongo", res, nil, 0, time.Now())

	tab := m.activeWorkspaceTab()
	if tab.Result.Table != nil {
		t.Fatalf("mongo result must stay as Documents (no synthetic Table), got %+v", tab.Result.Table)
	}
	if len(tab.Result.Documents) != 2 {
		t.Fatalf("documents should be preserved, got %d", len(tab.Result.Documents))
	}
	if tab.WorkspaceFocus != workspaceFocusResult {
		t.Fatalf("focus should move to the result after a query, got %s", tab.WorkspaceFocus)
	}

	// The rendered result is pretty JSON, not the table footer.
	out := m.workspaceQueryContent(*tab)
	if !strings.Contains(out, `"name"`) || !strings.Contains(out, "{") {
		t.Fatalf("mongo result should render as JSON, got:\n%s", out)
	}
	if strings.Contains(out, "of 2, column") {
		t.Fatalf("mongo result should not render the table grid footer, got:\n%s", out)
	}
}

// A document result uses the data grid's char cursor + visual copy.
func TestMongoQueryResultUsesDataGridCopy(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.openQueryWorkspaceTab()
	tab := m.activeWorkspaceTab()
	tab.Result = result.Set{Documents: sampleDocs()}
	tab.WorkspaceFocus = workspaceFocusResult
	tab.VimMode = vimModeNormal

	// j moves the data-grid cursor down a JSON line.
	m.handleQueryResultKey(tab, runes('j'))
	if tab.ResultRow != 1 {
		t.Fatalf("j should move the data cursor to row 1, got %d", tab.ResultRow)
	}
	// v enters visual mode; y copies via the data grid.
	m.handleQueryResultKey(tab, runes('v'))
	if tab.VimMode != vimModeVisual {
		t.Fatalf("v should enter visual mode, got %q", tab.VimMode)
	}
	m.handleQueryResultKey(tab, runes('y'))
	if tab.VimMode != vimModeNormal {
		t.Fatalf("y should leave visual mode, got %q", tab.VimMode)
	}
}

func keys(s string) []tea.KeyMsg {
	out := make([]tea.KeyMsg, 0, len(s))
	for _, r := range s {
		out = append(out, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return out
}

func TestQueryResultGotoTopBottom(t *testing.T) {
	m, tab := resultTabWithTable(t) // 3-row table from result_copy_test.go
	// G jumps to the last loaded row.
	m.handleQueryResultKey(tab, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if tab.ResultCursorRow != 2 {
		t.Fatalf("G should jump to the last row 2, got %d", tab.ResultCursorRow)
	}
	// gg jumps back to the first row.
	for _, k := range keys("gg") {
		m.handleQueryResultKey(tab, k)
	}
	if tab.ResultCursorRow != 0 {
		t.Fatalf("gg should jump to row 0, got %d", tab.ResultCursorRow)
	}
	if tab.ResultPendingG {
		t.Fatal("pending-g should be cleared after gg")
	}
	// A lone g followed by a non-g key must not jump.
	m.handleQueryResultKey(tab, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}}) // to bottom
	m.handleQueryResultKey(tab, runes('g'))                                         // pending
	m.handleQueryResultKey(tab, runes('j'))                                         // cancels pending, moves down (clamped)
	if tab.ResultPendingG {
		t.Fatal("a non-g key must clear pending-g")
	}
	if tab.ResultCursorRow != 2 {
		t.Fatalf("g then j should not jump to top, got row %d", tab.ResultCursorRow)
	}
}

func TestDataGridGotoTopBottom(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.openQueryWorkspaceTab()
	tab := m.activeWorkspaceTab()
	tab.Result = result.Set{Documents: sampleDocs()} // multi-line JSON
	tab.WorkspaceFocus = workspaceFocusResult
	tab.VimMode = vimModeNormal

	last := m.dataCursorRowCount(tab) - 1
	if last < 1 {
		t.Fatalf("expected multi-line JSON, got %d lines", last+1)
	}
	m.handleQueryResultKey(tab, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if tab.ResultRow != last {
		t.Fatalf("G should jump to the last line %d, got %d", last, tab.ResultRow)
	}
	for _, k := range keys("gg") {
		m.handleQueryResultKey(tab, k)
	}
	if tab.ResultRow != 0 {
		t.Fatalf("gg should jump to line 0, got %d", tab.ResultRow)
	}
}
