package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/db"
)

// Enter follows the focused window: on a data view focused in main, it must NOT
// leak into the sidebar (e.g. toggling a database node).
func TestEnterDoesNotLeakToSidebarFromMain(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.page = PageBrowser
	m.databases = []string{"app"}
	m.databaseObjects = map[string][]db.Object{m.scopeKey("", "app"): {{Name: "users", Type: db.ObjectTable}}}
	m.focus = FocusMain // looking at the right panel
	expandedBefore := len(m.expandedDBs)

	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if m.expandedDBs["app"] {
		t.Fatal("Enter in the main panel must not expand a sidebar database")
	}
	if len(m.expandedDBs) != expandedBefore {
		t.Fatal("Enter in the main panel must not touch sidebar state")
	}
}

// While an overlay is open, clicking the background panel must not change focus
// (the focus would be stuck on a panel hidden behind the modal).
func TestOverlayBackgroundClickIsNoOp(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.focus = FocusSidebar
	m.modal = &modalState{Kind: modalDatabasePicker, Title: "Switch database"}
	m.hitboxes = HitboxRegistry{{ID: "panel-main", X: 30, Y: 5, Width: 40, Height: 10, Focus: FocusMain}}

	leftClick(m, 40, 8)

	if m.focus != FocusSidebar {
		t.Fatalf("background click under an overlay must not change focus, got %s", m.focus)
	}
}

func TestSliceColumnsByDisplayWidth(t *testing.T) {
	// "我" is width 2, so columns [2,5) of "我abc" are "abc".
	if got := sliceColumns("我abc", 2, 5); got != "abc" {
		t.Fatalf("sliceColumns = %q, want abc", got)
	}
	if got := sliceColumns("hello world", 0, 5); got != "hello" {
		t.Fatalf("sliceColumns = %q, want hello", got)
	}
}

// A drag selection extracts only the columns of the panel it started in, clamped
// to that panel's bounds (so the other panel's columns are excluded).
func TestExtractSelectionClampedToPanel(t *testing.T) {
	m := newWorkspaceVimModel(t)
	// Two "panels": sidebar cols [0,10), main cols [10,…). Two rows.
	m.lastFrameLines = []string{
		"sidebarL0 MAIN-ROW-0-text",
		"sidebarL1 MAIN-ROW-1-text",
	}
	m.selMinX, m.selMaxX = 10, 30
	m.selAnchorX, m.selAnchorY = 10, 0
	m.selX, m.selY = 25, 1

	out := m.extractSelectionText()
	if strings.Contains(out, "sidebar") {
		t.Fatalf("selection must exclude the sidebar columns, got %q", out)
	}
	if !strings.Contains(out, "MAIN-ROW-0") || !strings.Contains(out, "MAIN-ROW-1") {
		t.Fatalf("selection should include both main rows, got %q", out)
	}
}

// A full left-drag (press → move → release) auto-copies the selection; a plain
// press+release (no motion) does not start a selection.
func TestDragSelectionAutoCopies(t *testing.T) {
	m := newWorkspaceVimModel(t)
	copier := &fakeClipboard{}
	m.options.ClipboardCopier = copier
	m.width, m.height = 100, 30
	sw := m.sidebarWidth()
	// Frame lines wide enough that the drag columns (in the main panel) hit text.
	m.lastFrameLines = []string{
		strings.Repeat(" ", sw) + "MAINROWZERO selectable text",
		strings.Repeat(" ", sw) + "MAINROWONE selectable text",
	}

	m.Update(tea.MouseMsg{X: sw + 1, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.Update(tea.MouseMsg{X: sw + 9, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m.Update(tea.MouseMsg{X: sw + 9, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})

	if copier.text == "" {
		t.Fatal("a drag-release should auto-copy the selection")
	}
	if !m.selActive {
		t.Fatal("the selection should stay highlighted after release")
	}
}

type fakeClipboard struct{ text string }

func (f *fakeClipboard) Copy(s string) error { f.text = s; return nil }
