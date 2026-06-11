package app

import (
	"context"
	"strings"
	"testing"

	"tdb/internal/db"
)

// Typing @ in the AI panel must keep rendering the AI panel inline — not hijack
// the whole main area into the standalone suggestions page.
func TestAIMentionDoesNotJumpToSuggestionsPage(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.width = 120
	m.height = 40
	m.selectedDB = "app"
	m.databaseObjects = map[string][]db.Object{m.scopeKey("", "app"): {{Name: "users", Type: db.ObjectTable}}}
	m.openAIChatModal()
	m.handleAIChatModalKey(keyMsg("@"))
	if !m.input.SuggestionsVisible() {
		t.Fatal("@ should show suggestions")
	}
	out := stripANSI(m.mainContent())
	if !strings.Contains(out, "context:") || !strings.Contains(out, "users") {
		t.Fatalf("main content should be the AI panel with the inline @ list, got:\n%s", out)
	}
}

// No matching table → silent: no suggestions popup, just a status message.
func TestAIMentionNoMatchIsSilent(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.selectedDB = "app"
	m.databaseObjects = map[string][]db.Object{m.scopeKey("", "app"): {{Name: "users", Type: db.ObjectTable}}}
	m.openAIChatModal()
	for _, r := range "@zzz" {
		m.handleAIChatModalKey(keyMsg(string(r)))
	}
	if m.input.SuggestionsVisible() {
		t.Fatal("no-match @ should not show a popup")
	}
	if m.message == "" {
		t.Fatal("no-match @ should set a bottom-right status hint")
	}
}

func newDBPickerModel(t *testing.T) *Model {
	t.Helper()
	m := newWorkspaceVimModel(t)
	m.width = 120
	m.height = 40
	m.databases = []string{"app", "sys", "logs"}
	m.openQueryWorkspaceTab() // workspaceTabs non-empty so the picker can float
	m.openDatabasePickerModal()
	return m
}

// The picker registers a click hitbox per row whose Y matches the row's actual
// rendered line (same-source), and clicks no longer leak to the page beneath.
func TestDBPickerRowHitboxesSameSource(t *testing.T) {
	m := newDBPickerModel(t)
	out := stripANSI(m.mainContent()) // populates m.modalRowHits, strips markers

	if len(m.modalRowHits) != 3 {
		t.Fatalf("expected a hitbox per database, got %d", len(m.modalRowHits))
	}
	known := map[string]bool{"app": true, "sys": true, "logs": true}
	lines := strings.Split(out, "\n")
	for _, h := range m.modalRowHits {
		name := strings.TrimPrefix(h.ID, "db-pick:")
		// The name must be an exact database name — no leading/trailing spaces or
		// box-drawing chars from the floating box's decoration.
		if !known[name] {
			t.Fatalf("hitbox name %q is not a clean database name", name)
		}
		row := h.Y - 2 // header + panel top border precede content
		if row < 0 || row >= len(lines) || !strings.Contains(lines[row], name) {
			t.Fatalf("hitbox %q at Y=%d does not match the rendered row %q", name, h.Y, safeLine(lines, row))
		}
	}

	// Full integration: while the picker is open, page-node hitboxes are gone and
	// db-pick hitboxes are present.
	m.renderLayout()
	var sawDBPick, sawNode bool
	for _, h := range m.hitboxes {
		if strings.HasPrefix(h.ID, "db-pick:") {
			sawDBPick = true
		}
		if strings.HasPrefix(h.ID, "database:") || strings.HasPrefix(h.ID, "object:") {
			sawNode = true
		}
	}
	if !sawDBPick {
		t.Fatal("db-pick hitboxes should be registered while the picker is open")
	}
	if sawNode {
		t.Fatal("page node hitboxes must be suppressed while a modal is open")
	}
}

// Clicking a picker row switches the active query tab to that database.
func TestDBPickerClickSelectsDatabase(t *testing.T) {
	m := newDBPickerModel(t)
	m.applyHitbox(context.Background(), Hitbox{ID: "db-pick:logs"}, 0, 0)
	if tab := m.activeWorkspaceTab(); tab == nil || tab.QueryDatabase != "logs" {
		t.Fatalf("clicking a row should switch the query tab's database, got %+v", m.activeWorkspaceTab())
	}
	if m.modal != nil {
		t.Fatal("the picker should close after a click selection")
	}
}

func TestDBPickerTabMovesSelection(t *testing.T) {
	m := newDBPickerModel(t)
	if m.historyIndex != 0 {
		t.Fatalf("picker should start at index 0, got %d", m.historyIndex)
	}
	m.handleDatabasePickerModalKey(keyMsg("tab"))
	if m.historyIndex != 1 {
		t.Fatalf("tab should advance selection, got %d", m.historyIndex)
	}
	m.handleDatabasePickerModalKey(keyMsg("shift+tab"))
	if m.historyIndex != 0 {
		t.Fatalf("shift+tab should move back, got %d", m.historyIndex)
	}
}

func safeLine(lines []string, i int) string {
	if i < 0 || i >= len(lines) {
		return "<out of range>"
	}
	return lines[i]
}
