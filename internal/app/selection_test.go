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

// A drag across a bordered overlay copies the content, not the box-drawing frame
// or its centering whitespace — while keeping plain indentation and interior
// separators.
func TestStripSelectionFrame(t *testing.T) {
	cases := []struct{ in, want string }{
		{"   │ SELECT 1 │  ", "SELECT 1"},              // centered modal row
		{"│ status — 1=active │", "status — 1=active"}, // tight border
		{"  indented", "  indented"},                   // no border → keep indentation
		{"a │ b", "a │ b"},                             // interior separator kept
		{"plain text", "plain text"},                   // untouched
		{"╭─────╮", ""},                                // a pure border row collapses to empty
	}
	for _, c := range cases {
		if got := strings.TrimRight(stripSelectionFrame(c.in), " "); got != c.want {
			t.Errorf("stripSelectionFrame(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// extractSelectionText drops the modal border from a full-width drag.
func TestExtractSelectionExcludesBorder(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.lastFrameLines = []string{"│ hello world │"}
	m.selMinX, m.selMaxX = 0, 15
	m.selAnchorX, m.selAnchorY = 0, 0
	m.selX, m.selY = 14, 0
	out := m.extractSelectionText()
	if strings.ContainsRune(out, '│') {
		t.Fatalf("copied selection must not contain the border: %q", out)
	}
	if !strings.Contains(out, "hello world") {
		t.Fatalf("copied selection should keep the content: %q", out)
	}
}

// detectOverlaySelRect recovers a box's inner rectangle from the boxMarker pair
// and strips the markers.
func TestDetectOverlaySelRect(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.overlayBoxWidth = 14
	m.lastFrameLines = []string{
		"      ╭─────────────╮",
		"      │ " + boxMarker + "AI assistant │",
		"      │ hello world │",
		"      │ " + boxMarker + "second line  │",
		"      ╰─────────────╯",
	}
	m.detectOverlaySelRect()
	if m.overlaySel == nil {
		t.Fatal("expected an overlay rect")
	}
	// "      │ " is 6 spaces + border + pad = display column 8.
	if m.overlaySel.x != 8 || m.overlaySel.y != 1 || m.overlaySel.h != 3 || m.overlaySel.w != 14 {
		t.Fatalf("rect = %+v, want {x:8 y:1 w:14 h:3}", *m.overlaySel)
	}
	for _, l := range m.lastFrameLines {
		if strings.Contains(l, boxMarker) {
			t.Fatalf("markers should be stripped: %q", l)
		}
	}
}

// A drag that starts and ends outside an overlay box is clamped to the box
// interior, so the copy holds only content — no border, no outside text.
func TestSelectionClampedToOverlayBox(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.overlaySel = &selRect{x: 8, y: 1, w: 14, h: 3}
	m.lastFrameLines = []string{
		"OUTSIDE TOP TEXT here",
		"      │ AI assistant │",
		"      │ hello world │",
		"      │ second line  │",
		"      ╰─────────────╯",
	}
	m.beginMouseDown(0, 0)      // clamps anchor into the box
	m.updateMouseDrag(200, 200) // drag far past the box → clamped
	out := m.extractSelectionText()

	if strings.ContainsAny(out, "│╭╮╰╯─") {
		t.Fatalf("clamped selection must not contain borders: %q", out)
	}
	if strings.Contains(out, "OUTSIDE") {
		t.Fatalf("clamped selection must not reach outside the box: %q", out)
	}
	if !strings.Contains(out, "AI assistant") || !strings.Contains(out, "hello world") {
		t.Fatalf("clamped selection should keep box content: %q", out)
	}
}

type fakeClipboard struct{ text string }

func (f *fakeClipboard) Copy(s string) error { f.text = s; return nil }
