package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/result"
)

// resultTabWithTable returns a focused query tab whose result is a 3-row table.
func resultTabWithTable(t *testing.T) (*Model, *workspaceTab) {
	t.Helper()
	m := newWorkspaceVimModel(t)
	m.openQueryWorkspaceTab()
	tab := m.activeWorkspaceTab()
	tab.Result = result.Set{Table: &result.Table{
		Columns: []result.Column{{Name: "id"}, {Name: "name"}},
		Rows: []result.Row{
			{Values: []any{1, "ada"}},
			{Values: []any{2, "grace"}},
			{Values: []any{3, "linus"}},
		},
	}}
	tab.WorkspaceFocus = workspaceFocusResult
	tab.VimMode = vimModeNormal
	return m, tab
}

func runes(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func TestQueryResultRowVisualCopy(t *testing.T) {
	m, tab := resultTabWithTable(t)

	// v enters row-visual ("copy mode") anchored on the current row.
	m.handleQueryResultKey(tab, runes('v'))
	if tab.VimMode != vimModeVisual {
		t.Fatalf("v should enter visual mode, got %q", tab.VimMode)
	}
	if tab.ResultCursorAnchor != 0 {
		t.Fatalf("anchor should be the current row 0, got %d", tab.ResultCursorAnchor)
	}

	// j extends the selection down one row; y copies rows [0,1] as TSV and exits.
	m.handleQueryResultKey(tab, runes('j'))
	if tab.ResultCursorRow != 1 {
		t.Fatalf("j should move the cursor to row 1, got %d", tab.ResultCursorRow)
	}
	m.handleQueryResultKey(tab, runes('y'))
	if tab.VimMode != vimModeNormal {
		t.Fatalf("y should leave visual mode, got %q", tab.VimMode)
	}
	want := "1\tada\n2\tgrace"
	if m.lastCopiedText != want {
		t.Fatalf("copied text = %q, want %q", m.lastCopiedText, want)
	}
}

func TestQueryResultNormalYCopiesSingleRow(t *testing.T) {
	m, tab := resultTabWithTable(t)
	m.handleQueryResultKey(tab, runes('j')) // move to row 1
	m.handleQueryResultKey(tab, runes('y')) // copy just the current row
	if tab.VimMode != vimModeNormal {
		t.Fatalf("normal-mode y should not enter visual, got %q", tab.VimMode)
	}
	if m.lastCopiedText != "2\tgrace" {
		t.Fatalf("copied text = %q, want %q", m.lastCopiedText, "2\tgrace")
	}
}

func TestQueryResultSelectionTextOrdersRows(t *testing.T) {
	m, tab := resultTabWithTable(t)
	// Anchor below the cursor: selection must still be ordered top-to-bottom.
	tab.ResultCursorRow = 0
	tab.ResultCursorAnchor = 2
	got := m.queryResultSelectionText(tab)
	want := "1\tada\n2\tgrace\n3\tlinus"
	if got != want {
		t.Fatalf("selection text = %q, want %q", got, want)
	}
}

func TestNonTableResultUsesDataGridVisualCopy(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.openQueryWorkspaceTab()
	tab := m.activeWorkspaceTab()
	// A Redis-style scalar/JSON value: no Table, so the result page falls back to
	// the data grid's char-level visual selection + copy.
	tab.Result = result.Set{Value: "hello"}
	tab.WorkspaceFocus = workspaceFocusResult
	tab.VimMode = vimModeNormal

	// v enters the data grid's visual mode; y copies the (char) selection.
	m.handleQueryResultKey(tab, runes('v'))
	if tab.VimMode != vimModeVisual {
		t.Fatalf("v should enter visual mode on a non-table result, got %q", tab.VimMode)
	}
	m.handleQueryResultKey(tab, runes('$')) // extend to end of line
	m.handleQueryResultKey(tab, runes('y'))
	if tab.VimMode != vimModeNormal {
		t.Fatalf("y should leave visual mode, got %q", tab.VimMode)
	}
	if !strings.Contains(m.lastCopiedText, "h") {
		t.Fatalf("y on a scalar value should copy its text, got %q", m.lastCopiedText)
	}
}
