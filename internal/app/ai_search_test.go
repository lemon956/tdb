package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"tdb/internal/db"
)

// leftClick simulates a full left click (press then release at the same cell),
// since the app resolves clicks on release (a press+move would be a drag-select).
func leftClick(m tea.Model, x, y int) tea.Model {
	m, _ = m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m, _ = m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	return m
}

// keyMsg builds a tea.KeyMsg from a key string for driving key handlers in tests.
func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// The AI panel body must hard-wrap every line to the content width (CJK-aware) so
// the modal's line-count based scroll/height accounting matches what lipgloss
// renders — otherwise long input/replies overflow and ghost across the panel.
func TestAIChatContentHardWrapsEveryLine(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.width = 100
	m.height = 40
	m.openAIChatModal()
	// A long spaceless CJK input (the case the old word-wrapper could not break).
	m.input.SetValue(strings.Repeat("查询最近七天修改的用户", 12))
	sess := m.currentAISession()
	sess.turns = append(sess.turns, aiTurn{role: "ai", text: strings.Repeat("SELECT very_long_column_name, ", 20)})

	width := max(8, clamp(m.workspaceContentWidth(), 32, 84)-6)
	for _, line := range strings.Split(m.aiChatContent(), "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Fatalf("line exceeds content width %d: %d %q", width, w, line)
		}
	}
}

func TestBuildSchemaContextMentionAttachesColumns(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.adapter = &metaStub{fields: []db.MetadataField{{Name: "id", Type: "int"}, {Name: "email", Type: "varchar"}}}
	m.selectedCatalog = ""
	m.selectedDB = "app"
	m.databaseObjects = map[string][]db.Object{
		m.scopeKey("", "app"): {{Name: "users", Type: db.ObjectTable}, {Name: "orders", Type: db.ObjectTable}},
	}

	// No mention → just the table list, no columns.
	if ctx := m.buildSchemaContext("hi"); strings.Contains(ctx, "Columns of") {
		t.Fatalf("no mention should attach no columns:\n%s", ctx)
	}
	// @users mention → its columns are fetched and attached.
	ctx := m.buildSchemaContext("count rows in @users please")
	for _, want := range []string{"app", "Tables: users, orders", "Columns of users", "id int", "email varchar"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("schema context missing %q:\n%s", want, ctx)
		}
	}
}

func TestInsertAISQLFillsQueryEditor(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.openQueryWorkspaceTab()
	m.currentAISession().lastSQLs = []string{"SELECT 1 FROM users"}

	m.insertAISQL()

	tab := m.activeWorkspaceTab()
	if tab == nil || tab.Kind != workspaceTabQuery {
		t.Fatal("expected an active query tab")
	}
	if tab.QueryBuffer != "SELECT 1 FROM users" {
		t.Fatalf("query buffer = %q, want the AI SQL", tab.QueryBuffer)
	}
	if tab.QueryCursor != len(tab.QueryBuffer) {
		t.Fatalf("cursor should be at end, got %d", tab.QueryCursor)
	}
}

// Multiple SQL blocks → ctrl+y opens a picker; choosing one inserts it.
func TestInsertAISQLMultiOpensPicker(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.openQueryWorkspaceTab()
	m.openAIChatModal()
	m.currentAISession().lastSQLs = []string{"SELECT 1", "SELECT 2", "SELECT 3"}

	m.insertAISQL()
	if m.modal == nil || m.modal.Kind != modalAISQLPick {
		t.Fatalf("multiple SQL should open the picker, modal=%v", m.modal)
	}
	if len(m.aiSQLChoices) != 3 {
		t.Fatalf("picker should hold 3 choices, got %d", len(m.aiSQLChoices))
	}
	m.handleAISQLPickKey(keyMsg("down")) // select "SELECT 2"
	m.handleAISQLPickKey(keyMsg("enter"))
	if tab := m.activeWorkspaceTab(); tab == nil || tab.QueryBuffer != "SELECT 2" {
		t.Fatalf("picker should insert the chosen SQL, got %q", m.activeWorkspaceTab().QueryBuffer)
	}
}

// positionRealCursor locates the zero-width marker, strips it, and appends an
// ANSI escape pointing the real terminal cursor at that row/column.
func TestPositionRealCursor(t *testing.T) {
	// Marker on row 2 (0-based 1), after "我" (width 2) + "x" (width 1) = col 3.
	frame := "line0\n我x" + cursorMarker + "rest\nline2"
	got := positionRealCursor(frame)
	if strings.Contains(got, cursorMarker) {
		t.Fatal("marker must be stripped from the output")
	}
	if !strings.Contains(got, "\x1b[?25h") {
		t.Fatal("the real cursor should be shown")
	}
	if !strings.HasSuffix(got, "\x1b[2;4H") { // row 2, col 4 (1-based)
		t.Fatalf("cursor position escape wrong, tail = %q", got[len(got)-12:])
	}
	// No marker → unchanged (fail-safe).
	if out := positionRealCursor("plain\nframe"); out != "plain\nframe" {
		t.Fatalf("no-marker frame should be unchanged, got %q", out)
	}
}

// The multi-SQL picker shows each statement in full so near-identical ones are
// distinguishable.
func TestAISQLPickContentShowsFullSQL(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.width = 120
	m.height = 40
	m.aiSQLChoices = []string{"SELECT DISTINCT user_id FROM a", "SELECT DISTINCT user_id FROM b WHERE x = 1"}
	out := stripANSI(m.aiSQLPickContent())
	for _, want := range []string{"FROM a", "FROM b WHERE x = 1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("picker must show full SQL %q:\n%s", want, out)
		}
	}
}

// @-mention candidates are available right after opening the panel, even without
// expanding the database in the sidebar first.
func TestAIPanelAutoLoadsTablesForMention(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.adapter = &objLoadAdapter{objects: []db.Object{{Name: "users", Type: db.ObjectTable}}}
	m.selectedDB = "app"
	m.databaseObjects = map[string][]db.Object{} // nothing loaded yet

	m.openAIChatModal()
	m.handleAIChatModalKey(keyMsg("@"))
	if !m.input.SuggestionsVisible() {
		t.Fatal("@ should show suggestions immediately (tables auto-loaded on open)")
	}
}

// Typing @us shows table suggestions; Enter accepts the highlighted one as @users.
func TestAIMentionAutocomplete(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.selectedDB = "app"
	m.databaseObjects = map[string][]db.Object{
		m.scopeKey("", "app"): {{Name: "users", Type: db.ObjectTable}, {Name: "orders", Type: db.ObjectTable}},
	}
	m.openAIChatModal()
	for _, r := range "show @us" {
		m.handleAIChatModalKey(keyMsg(string(r)))
	}
	if !m.input.SuggestionsVisible() {
		t.Fatal("@us should show table suggestions")
	}
	m.handleAIChatModalKey(keyMsg("enter")) // accept "users"
	if m.input.Value() != "show @users " {
		t.Fatalf("mention accept should expand to @users, got %q", m.input.Value())
	}
}

func TestAISessionsScopedPerDatabase(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.selectedDB = "db1"
	m.currentAISession().turns = append(m.currentAISession().turns, aiTurn{role: "you", text: "hi from db1"})

	m.selectedDB = "db2"
	if len(m.currentAISession().turns) != 0 {
		t.Fatal("switching database should give a fresh AI session")
	}
	m.selectedDB = "db1"
	if len(m.currentAISession().turns) != 1 {
		t.Fatal("returning to db1 should restore its AI history")
	}
}

func TestViewSearchJumpAndCycle(t *testing.T) {
	m := newWorkspaceVimModel(t)
	moved := -1
	rows := []string{"alpha", "beta one", "gamma", "beta two", "delta"}
	m.startViewSearch(rows, 0, func(i int) { moved = i })

	m.input.SetValue("beta")
	m.updateViewSearch()
	if len(m.viewSearchMatches) != 2 {
		t.Fatalf("expected 2 matches, got %v", m.viewSearchMatches)
	}
	if moved != 1 {
		t.Fatalf("first match should jump to row 1, got %d", moved)
	}
	if !m.cycleViewSearch(1) || moved != 3 {
		t.Fatalf("n should advance to row 3, got %d", moved)
	}
	if !m.cycleViewSearch(1) || moved != 1 {
		t.Fatalf("n should wrap back to row 1, got %d", moved)
	}
	if !m.cycleViewSearch(-1) || moved != 3 {
		t.Fatalf("N should wrap to row 3, got %d", moved)
	}

	m.cancelViewSearch()
	if m.viewSearchInput || len(m.viewSearchMatches) != 0 {
		t.Fatal("cancel should clear the search state")
	}
}

// Search prefers the first match at or after the cursor's starting row.
func TestViewSearchStartsFromOrigin(t *testing.T) {
	m := newWorkspaceVimModel(t)
	moved := -1
	rows := []string{"hit", "miss", "hit", "miss", "hit"}
	m.startViewSearch(rows, 3, func(i int) { moved = i })
	m.input.SetValue("hit")
	m.updateViewSearch()
	if moved != 4 {
		t.Fatalf("from origin 3 the first match should be row 4, got %d", moved)
	}
}
