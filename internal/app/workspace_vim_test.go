package app

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/result"
)

func TestQueryTabInsertModeEditsBufferAndEscReturnsNormal(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.openQueryWorkspaceTab()

	// Query tabs open directly in insert mode, so typing works immediately.
	var updated tea.Model
	for _, r := range "SELECT 1" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(*Model)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(*Model)

	tab := model.activeWorkspaceTab()
	if tab == nil || tab.QueryBuffer != "SELECT 1" {
		t.Fatalf("query buffer = %+v, want SELECT 1", tab)
	}
	if tab.VimMode != vimModeNormal || tab.WorkspaceFocus != workspaceFocusEditor {
		t.Fatalf("mode/focus = %s/%s, want normal/editor", tab.VimMode, tab.WorkspaceFocus)
	}
}

func TestRowDetailLinesSplitMultilineValue(t *testing.T) {
	table := result.Table{
		Columns: []result.Column{{Name: "Table"}, {Name: "Create Table"}},
		Rows:    []result.Row{{Values: []any{"t", "CREATE TABLE `t` (\n  `a` int,\n  `b` int\n)"}}},
	}
	lines := rowDetailLines(table, 0)
	// Table(1 line) + Create Table value (4 lines) = 5 grid rows.
	if len(lines) != 5 {
		t.Fatalf("detail lines = %d, want 5 (multi-line value split into rows)", len(lines))
	}
	for i, l := range lines {
		if strings.ContainsAny(l, "\n\r") {
			t.Fatalf("detail line %d still contains a newline: %q", i, l)
		}
	}
	// Continuation lines map back to the Create Table column (index 1).
	if rowDetailColumnForLine(table, 0, 0) != 0 {
		t.Fatalf("line 0 should map to column 0 (Table)")
	}
	if rowDetailColumnForLine(table, 0, 3) != 1 {
		t.Fatalf("line 3 should map to column 1 (Create Table)")
	}
}

func TestDataCursorLineAndWordMotions(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.width = 120
	model.height = 30
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabData,
		Target:         db.Target{Name: "t"},
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Table: &result.Table{
			Columns: []result.Column{{Name: "ddl"}},
			Rows:    []result.Row{{Values: []any{"create table foo bar"}}},
		}},
	}}
	model.activeTabIndex = 0
	model.focus = FocusMain
	model.syncActiveTabState()
	tab := model.activeWorkspaceTab()
	tab.ResultRow = 1 // a data row
	tab.ResultColumn = 0
	model.syncActiveTabState()

	press := func(r rune) { up, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}); model = up.(*Model) }
	press('$')
	if tab = model.activeWorkspaceTab(); tab.ResultColumn == 0 {
		t.Fatalf("'$' should move to line end, col=%d", tab.ResultColumn)
	}
	end := tab.ResultColumn
	press('0')
	if tab = model.activeWorkspaceTab(); tab.ResultColumn != 0 {
		t.Fatalf("'0' should move to column 0, got %d", tab.ResultColumn)
	}
	press('w')
	afterW := model.activeWorkspaceTab().ResultColumn
	if afterW == 0 || afterW >= end {
		t.Fatalf("'w' should move to the next word, got %d", afterW)
	}
	press('b')
	if model.activeWorkspaceTab().ResultColumn != 0 {
		t.Fatalf("'b' should move back to the first word, got %d", model.activeWorkspaceTab().ResultColumn)
	}
}

func TestQueryInsertLineAndWordMotions(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.QueryBuffer = "select one two"
	tab.QueryCursor = len(tab.QueryBuffer)
	model.syncActiveTabState()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyHome})
	model = updated.(*Model)
	if model.activeWorkspaceTab().QueryCursor != 0 {
		t.Fatalf("Home should jump to line start, got %d", model.activeWorkspaceTab().QueryCursor)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}, Alt: true})
	model = updated.(*Model)
	if c := model.activeWorkspaceTab().QueryCursor; c == 0 {
		t.Fatalf("Alt+f should advance a word, got %d", c)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnd})
	model = updated.(*Model)
	if c := model.activeWorkspaceTab().QueryCursor; c != len(tab.QueryBuffer) {
		t.Fatalf("End should jump to line end, got %d want %d", c, len(tab.QueryBuffer))
	}
}

func TestQueryEditorPasteKeepsContentInBox(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.openQueryWorkspaceTab()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("SELECT *\r\nFROM t\r\nLIMIT 1;"), Paste: true})
	model = updated.(*Model)
	tab := model.activeWorkspaceTab()
	if strings.ContainsAny(tab.QueryBuffer, "\r") {
		t.Fatalf("query buffer should not contain CR: %q", tab.QueryBuffer)
	}
	if tab.QueryBuffer != "SELECT *\nFROM t\nLIMIT 1;" {
		t.Fatalf("query buffer = %q, want 3 LF-separated lines", tab.QueryBuffer)
	}
	if content := model.workspaceQueryContent(*tab); strings.ContainsAny(content, "\r") {
		t.Fatalf("rendered query content must not contain a raw CR")
	}
}

func TestOpenDataTabFocusesWorkspace(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.focus = FocusSidebar
	model.openDataWorkspaceTab(db.Target{Database: "app", Name: "users", Type: db.ObjectTable}, result.Set{}, workspacePreview)
	if model.focus != FocusMain {
		t.Fatalf("focus after opening a data tab = %s, want main (focus follows the opened window)", model.focus)
	}
}

func TestQueryDatabasePickerSwitchesDatabase(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.databases = []string{"app", "analytics", "mysql"}
	model.openQueryWorkspaceTab()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	model = updated.(*Model)
	if model.modal == nil || model.modal.Kind != modalDatabasePicker {
		t.Fatalf("Ctrl+D should open the database picker")
	}
	for _, r := range "ana" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(*Model)
	}
	if got := model.filteredDatabases(); len(got) != 1 || got[0] != "analytics" {
		t.Fatalf("filtered databases = %+v, want [analytics]", got)
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	if model.modal != nil {
		t.Fatalf("Enter should close the picker")
	}
	if tab := model.activeWorkspaceTab(); tab.QueryDatabase != "analytics" {
		t.Fatalf("query tab database = %q, want analytics", tab.QueryDatabase)
	}
	// The async object load completes via the returned command.
	runCmd(model, cmd)
	if _, ok := model.databaseObjects["analytics"]; !ok {
		t.Fatalf("selecting a database should load its objects for completion")
	}
}

func TestQueryExecuteShowsSpinnerThenClears(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.adapter = &recordingAdapter{}
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.QueryBuffer = "SELECT 1"
	tab.QueryCursor = len(tab.QueryBuffer)

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	// Mid-flight: spinner active, input cleared, command pending.
	if !model.loading.active {
		t.Fatalf("spinner should be active while the query runs")
	}
	if cmd == nil {
		t.Fatalf("Enter should return an async command")
	}
	if got := model.activeWorkspaceTab(); got.QueryBuffer != "" {
		t.Fatalf("input should clear immediately, got %q", got.QueryBuffer)
	}
	// Completion: result applied, spinner cleared.
	runCmd(model, cmd)
	if model.loading.active {
		t.Fatalf("spinner should clear after the query completes")
	}
	if got := model.activeWorkspaceTab(); got.Result.Table == nil {
		t.Fatalf("result should be applied after completion")
	}
}

func TestQueryTabEnterExecutesBufferAndFocusesResult(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.adapter = &recordingAdapter{}
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.QueryBuffer = "SELECT 1"
	tab.QueryCursor = len(tab.QueryBuffer)

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	runCmd(model, cmd)
	tab = model.activeWorkspaceTab()

	if tab == nil || tab.Result.Table == nil || tab.Result.Table.CellString(0, 0) != "SELECT 1" {
		t.Fatalf("query result = %+v, want SELECT 1 table", tab)
	}
	if model.focus != FocusMain || tab.WorkspaceFocus != workspaceFocusResult {
		t.Fatalf("focus/result focus = %s/%s, want main/result", model.focus, tab.WorkspaceFocus)
	}
	if tab.QueryText != "SELECT 1" {
		t.Fatalf("QueryText = %q, want last executed statement", tab.QueryText)
	}
}

func TestQueryResultFocusScrollsAndIReturnsToInsertEditor(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabQuery,
		Title:          "Query",
		QueryBuffer:    "SELECT * FROM users",
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Table: &result.Table{
			Columns: []result.Column{{Name: "id"}},
			Rows:    []result.Row{{Values: []any{1}}, {Values: []any{2}}, {Values: []any{3}}},
		}},
	}}
	model.activeTabIndex = 0
	model.syncActiveTabState()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = updated.(*Model)
	tab := model.activeWorkspaceTab()
	if tab.ResultCursorRow != 1 {
		t.Fatalf("ResultCursorRow = %d, want 1", tab.ResultCursorRow)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	model = updated.(*Model)
	tab = model.activeWorkspaceTab()
	if tab.WorkspaceFocus != workspaceFocusEditor || tab.VimMode != vimModeInsert {
		t.Fatalf("focus/mode = %s/%s, want editor/insert", tab.WorkspaceFocus, tab.VimMode)
	}
}

func TestQueryVisualDeleteRemovesSelectedBufferText(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.QueryBuffer = "abcdef"
	tab.QueryCursor = 1
	tab.VimMode = vimModeNormal

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'v'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'d'}},
	} {
		updated, _ := model.Update(key)
		model = updated.(*Model)
	}
	tab = model.activeWorkspaceTab()
	if tab.QueryBuffer != "adef" {
		t.Fatalf("QueryBuffer after visual delete = %q, want adef", tab.QueryBuffer)
	}
	if tab.VimMode != vimModeNormal {
		t.Fatalf("VimMode = %s, want normal", tab.VimMode)
	}
}

func TestQueryBufferIsRenderedInWorkspace(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.QueryBuffer = "SELECT * FROM users"
	tab.VimMode = vimModeInsert
	tab.WorkspaceFocus = workspaceFocusEditor

	if content := stripANSI(model.mainContent()); !strings.Contains(content, "INSERT") || !strings.Contains(content, "SELECT * FROM users") {
		t.Fatalf("query editor content missing mode/buffer:\n%s", content)
	}
	if content := stripANSI(model.mainContent()); strings.Contains(content, "Buffer:") {
		t.Fatalf("query editor should not render Buffer label:\n%s", content)
	}
}

func TestWorkspaceFocusRendersDataTabCursor(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabData,
		Target:         db.Target{Database: "app", Name: "users", Type: db.ObjectTable},
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Table: &result.Table{
			Columns: []result.Column{{Name: "id"}},
			Rows:    []result.Row{{Values: []any{1}}},
		}},
	}}
	model.activeTabIndex = 0
	model.focus = FocusMain

	content := stripANSI(model.mainContent())
	for _, want := range []string{"> Data  NORMAL  result", "cursor row:1 col:1", "Data: app.users"} {
		if !strings.Contains(content, want) {
			t.Fatalf("data tab content missing %q:\n%s", want, content)
		}
	}
}

func TestWorkspaceFocusRendersQueryEditorAndResultCursors(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabQuery,
		Title:          "Query",
		QueryBuffer:    "SELECT * FROM users",
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusEditor,
	}}
	model.activeTabIndex = 0
	model.focus = FocusMain

	if content := stripANSI(model.mainContent()); strings.Contains(content, "Buffer:") || !strings.Contains(content, "> [>] SELECT * FROM users") {
		t.Fatalf("query editor content missing buffer cursor:\n%s", content)
	}

	tab := model.activeWorkspaceTab()
	tab.WorkspaceFocus = workspaceFocusResult
	tab.Result = result.Set{Table: &result.Table{
		Columns: []result.Column{{Name: "id"}},
		Rows:    []result.Row{{Values: []any{1}}},
	}}

	if content := stripANSI(model.mainContent()); !strings.Contains(content, "Results") {
		t.Fatalf("query result content missing result bar:\n%s", content)
	}
}

func TestQueryEditorEmptyInputRendersOnlyCursorWithoutBufferLabel(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.cursorBlinkOn = true
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabQuery,
		Title:          "Query",
		VimMode:        vimModeInsert,
		WorkspaceFocus: workspaceFocusEditor,
	}}
	model.activeTabIndex = 0
	model.focus = FocusMain

	content := model.workspaceQueryContent(*model.activeWorkspaceTab())
	plain := stripANSI(content)
	if strings.Contains(plain, "Buffer:") {
		t.Fatalf("empty query editor should not render Buffer label:\n%s", plain)
	}
	if !strings.Contains(content, defaultTheme().cursor.Render(" ")) {
		t.Fatalf("empty query editor should render cursor block:\n%q", content)
	}
}

func TestQueryEditorRendersCharacterCursorAndVisualSelection(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.cursorBlinkOn = true
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabQuery,
		Title:          "Query",
		QueryBuffer:    "SELECT * FROM users",
		QueryCursor:    7,
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusEditor,
	}}
	model.activeTabIndex = 0
	model.focus = FocusMain

	content := model.workspaceQueryContent(*model.activeWorkspaceTab())
	if !strings.Contains(content, defaultTheme().cursor.Render("*")) {
		t.Fatalf("query editor should render character cursor on *:\n%q", content)
	}

	tab := model.activeWorkspaceTab()
	tab.VimMode = vimModeVisual
	tab.SelectionAnchor = 0
	tab.QueryCursor = 5
	content = model.workspaceQueryContent(*tab)
	if !strings.Contains(content, defaultTheme().visual.Render("SELECT")) {
		t.Fatalf("query editor should render visual block for selected text:\n%q", content)
	}
}

func TestQueryNormalModeSupportsCoreVimLineEditing(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.QueryBuffer = "SELECT 1\nSELECT 2"
	tab.QueryCursor = 0
	tab.VimMode = vimModeNormal

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'w'}},
		{Type: tea.KeyRunes, Runes: []rune{'$'}},
		{Type: tea.KeyRunes, Runes: []rune{'0'}},
		{Type: tea.KeyRunes, Runes: []rune{'d'}},
		{Type: tea.KeyRunes, Runes: []rune{'d'}},
	} {
		updated, _ := model.Update(key)
		model = updated.(*Model)
	}
	tab = model.activeWorkspaceTab()
	if tab.QueryBuffer != "SELECT 2" {
		t.Fatalf("QueryBuffer after dd = %q, want second line only", tab.QueryBuffer)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	model = updated.(*Model)
	tab = model.activeWorkspaceTab()
	if tab.QueryBuffer != "SELECT 1\nSELECT 2" {
		t.Fatalf("QueryBuffer after undo = %q, want original", tab.QueryBuffer)
	}

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'y'}},
		{Type: tea.KeyRunes, Runes: []rune{'y'}},
		{Type: tea.KeyRunes, Runes: []rune{'G'}},
		{Type: tea.KeyRunes, Runes: []rune{'p'}},
	} {
		updated, _ = model.Update(key)
		model = updated.(*Model)
	}
	tab = model.activeWorkspaceTab()
	if !strings.Contains(tab.QueryBuffer, "SELECT 2\nSELECT 1") {
		t.Fatalf("QueryBuffer after yy/G/p = %q, want copied line pasted after last line", tab.QueryBuffer)
	}
}

func TestQueryRunButtonExecutesOneStatementFromBuffer(t *testing.T) {
	model := newWorkspaceVimModel(t)
	adapter := &queryCommandAdapter{}
	model.adapter = adapter
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.selectedDB = "app"
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.QueryDatabase = "app"
	tab.QueryBuffer = "SELECT 1;\nSELECT 2;"
	tab.QueryCursor = len(tab.QueryBuffer)

	content := model.workspaceQueryContent(*tab)
	if !strings.Contains(stripANSI(content), "[>] SELECT 1") {
		t.Fatalf("query content missing run button:\n%s", stripANSI(content))
	}
	if strings.Contains(stripANSI(content), "Statements:") {
		t.Fatalf("query content should render run buttons in gutter, not a Statements block:\n%s", stripANSI(content))
	}

	model.applyHitbox(context.Background(), Hitbox{ID: "query-run:0", Focus: FocusMain}, 0, 0)
	tab = model.activeWorkspaceTab()
	if adapter.last.Text != "SELECT 1" || adapter.last.Database != "app" {
		t.Fatalf("executed command = %+v, want first statement in app", adapter.last)
	}
	if strings.Contains(tab.QueryBuffer, "SELECT 1") || !strings.Contains(tab.QueryBuffer, "SELECT 2") {
		t.Fatalf("QueryBuffer after run button = %q, want first statement removed", tab.QueryBuffer)
	}
}

func TestDataTabVisualSelectionCopiesTableDisplayCharacters(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabData,
		Target:         db.Target{Database: "app", Name: "users", Type: db.ObjectTable},
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Table: &result.Table{
			Columns: []result.Column{{Name: "id"}, {Name: "name"}},
			Rows: []result.Row{
				{Values: []any{1, "Ada"}},
				{Values: []any{2, "Linus"}},
			},
		}},
	}}
	model.activeTabIndex = 0
	model.syncActiveTabState()

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'v'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'j'}},
		{Type: tea.KeyRunes, Runes: []rune{'y'}},
	} {
		updated, _ := model.Update(key)
		model = updated.(*Model)
	}

	if model.lastCopiedText != "id    name \n1 " {
		t.Fatalf("lastCopiedText = %q, want display character selection", model.lastCopiedText)
	}
	tab := model.activeWorkspaceTab()
	if tab.VimMode != vimModeNormal {
		t.Fatalf("VimMode after y = %s, want normal", tab.VimMode)
	}
}

func TestDataTabVisualModeRendersCopySelection(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabData,
		Target:         db.Target{Database: "app", Name: "users", Type: db.ObjectTable},
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Table: &result.Table{
			Columns: []result.Column{{Name: "id"}, {Name: "name"}},
			Rows: []result.Row{
				{Values: []any{1, "Ada"}},
				{Values: []any{2, "Linus"}},
			},
		}},
	}}
	model.activeTabIndex = 0
	model.focus = FocusMain

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	model = updated.(*Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = updated.(*Model)

	content := stripANSI(model.mainContent())
	for _, want := range []string{"VISUAL", "selection 1:1-2:1"} {
		if !strings.Contains(content, want) {
			t.Fatalf("visual data tab content missing %q:\n%s", want, content)
		}
	}
}

func TestDataTableViewportRendersCursorAndVisualBlocks(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.width = 70
	model.height = 18
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabData,
		Target:         db.Target{Database: "app", Name: "users", Type: db.ObjectTable},
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Table: &result.Table{
			Columns: []result.Column{{Name: "id"}, {Name: "name"}},
			Rows: []result.Row{
				{Values: []any{1, "Ada"}},
				{Values: []any{2, "Linus"}},
			},
		}},
	}}
	model.activeTabIndex = 0

	normalContent := model.workspaceDataContent(*model.activeWorkspaceTab())
	if !strings.Contains(normalContent, "48;2;") {
		t.Fatalf("table data viewport should render a cursor background block:\n%q", normalContent)
	}

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'v'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'j'}},
	} {
		updated, _ := model.Update(key)
		model = updated.(*Model)
	}

	visualContent := model.workspaceDataContent(*model.activeWorkspaceTab())
	plain := stripANSI(visualContent)
	if !strings.Contains(plain, "VISUAL") || !strings.Contains(plain, "selection 1:1-2:2") || !strings.Contains(visualContent, "48;2;") {
		t.Fatalf("table visual viewport missing mode, selection range, or visual block:\n%q", visualContent)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(*Model)
	if model.lastCopiedText != "id    name \n1 " {
		t.Fatalf("lastCopiedText = %q, want display character selection", model.lastCopiedText)
	}
}

func TestDataTableCursorMovesByDisplayCharacterAndScrollsHorizontally(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.width = 58
	model.height = 14
	model.focus = FocusMain
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabData,
		Target:         db.Target{Database: "app", Name: "users", Type: db.ObjectTable},
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Table: &result.Table{
			Columns: []result.Column{{Name: "payload"}},
			Rows: []result.Row{
				{Values: []any{"abcdefghijklmnopqrstuvwxyz0123456789"}},
			},
		}},
	}}
	model.activeTabIndex = 0

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'j'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
	} {
		updated, _ := model.Update(key)
		model = updated.(*Model)
	}
	tab := model.activeWorkspaceTab()
	if tab.ResultRow != 1 || tab.ResultColumn != 3 {
		t.Fatalf("cursor = row:%d col:%d, want table display row:1 col:3", tab.ResultRow, tab.ResultColumn)
	}

	for i := 0; i < 40; i++ {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
		model = updated.(*Model)
	}
	tab = model.activeWorkspaceTab()
	if tab.ResultColumn <= model.workspaceContentWidth() || tab.ResultView.ColumnOffset == 0 {
		t.Fatalf("cursor col=%d column offset=%d width=%d, want horizontal viewport to follow character cursor", tab.ResultColumn, tab.ResultView.ColumnOffset, model.workspaceContentWidth())
	}
}

func TestDataDocumentNormalCursorMovesByDisplayCharacter(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.width = 64
	model.height = 16
	model.focus = FocusMain
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabData,
		Target:         db.Target{Database: "app", Name: "events", Type: db.ObjectCollection},
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Documents: []result.Document{{
			ID: "doc-1",
			Data: map[string]any{
				"_id":  "doc-1",
				"name": "Ada",
			},
		}}},
	}}
	model.activeTabIndex = 0

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'j'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
	} {
		updated, _ := model.Update(key)
		model = updated.(*Model)
	}

	tab := model.activeWorkspaceTab()
	if tab.ResultRow != 1 || tab.ResultColumn != 3 {
		t.Fatalf("cursor = row:%d col:%d, want row:1 col:3 display character", tab.ResultRow, tab.ResultColumn)
	}
	lines := wrappedDataLines(dataResultLines(tab.Result), model.workspaceContentWidth())
	wantCursor := defaultTheme().cursor.Render(cellSlice(lines[1], 3, 1))
	content := model.workspaceDataContent(*tab)
	if !strings.Contains(content, wantCursor) {
		t.Fatalf("data viewport should render cursor on one display character %q:\n%q", cellSlice(lines[1], 3, 1), content)
	}
	if strings.Contains(content, defaultTheme().cursor.Render(padCells(lines[1], model.workspaceContentWidth()))) {
		t.Fatalf("data viewport should not render the whole row as cursor:\n%q", content)
	}
}

func TestDataDocumentViewportWrapsLongLinesAndRendersCursorBlock(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.width = 64
	model.height = 16
	model.focus = FocusMain
	longValue := strings.Repeat("x", 120)
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabData,
		Target:         db.Target{Database: "app", Name: "events", Type: db.ObjectCollection},
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Documents: []result.Document{{
			ID: "doc-1",
			Data: map[string]any{
				"_id":     "doc-1",
				"payload": longValue,
			},
		}}},
	}}
	model.activeTabIndex = 0

	content := model.workspaceDataContent(*model.activeWorkspaceTab())
	plain := stripANSI(content)
	if strings.Contains(plain, longValue) {
		t.Fatalf("data viewport should wrap long value instead of rendering it as one line:\n%s", plain)
	}
	for _, line := range strings.Split(strings.TrimRight(plain, "\n"), "\n") {
		if lipgloss.Width(line) > model.workspaceContentWidth() {
			t.Fatalf("line width = %d, want <= %d: %q\n%s", lipgloss.Width(line), model.workspaceContentWidth(), line, plain)
		}
	}
	if !strings.Contains(content, "48;2;") {
		t.Fatalf("data viewport should render a cursor background block:\n%q", content)
	}
}

func TestDataDocumentVisualModeHighlightsAndCopiesDisplayLines(t *testing.T) {
	var out bytes.Buffer
	model := NewModel(Options{
		ConfigPath:      filepath.Join(t.TempDir(), "tdb.enc"),
		IconStyle:       IconStyleUnicode,
		ClipboardWriter: &out,
	})
	model.page = PageData
	model.focus = FocusMain
	model.width = 72
	model.height = 18
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabData,
		Target:         db.Target{Database: "app", Name: "events", Type: db.ObjectCollection},
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Documents: []result.Document{{
			ID: "doc-1",
			Data: map[string]any{
				"_id":  "doc-1",
				"name": "Ada",
				"role": "engineer",
			},
		}}},
	}}
	model.activeTabIndex = 0

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'v'}},
		{Type: tea.KeyRunes, Runes: []rune{'j'}},
		{Type: tea.KeyRunes, Runes: []rune{'j'}},
	} {
		updated, _ := model.Update(key)
		model = updated.(*Model)
	}

	content := model.workspaceDataContent(*model.activeWorkspaceTab())
	plain := stripANSI(content)
	if !strings.Contains(plain, "VISUAL") || !strings.Contains(plain, "selection 1:1-3:1") || !strings.Contains(content, "48;2;") {
		t.Fatalf("visual document viewport missing mode, selection range, or visual block:\n%q", content)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(*Model)
	if model.lastCopiedText == "" || !strings.Contains(model.lastCopiedText, "\"_id\"") {
		t.Fatalf("lastCopiedText = %q, want selected display lines", model.lastCopiedText)
	}
	if model.activeWorkspaceTab().VimMode != vimModeNormal {
		t.Fatalf("VimMode after y = %s, want normal", model.activeWorkspaceTab().VimMode)
	}
}

func TestDataDocumentVisualSelectionCopiesDisplayCharacters(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.width = 72
	model.height = 18
	model.focus = FocusMain
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabData,
		Target:         db.Target{Database: "app", Name: "events", Type: db.ObjectCollection},
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Documents: []result.Document{{
			ID: "doc-1",
			Data: map[string]any{
				"_id":  "doc-1",
				"name": "Ada",
			},
		}}},
	}}
	model.activeTabIndex = 0

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'j'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'v'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
	} {
		updated, _ := model.Update(key)
		model = updated.(*Model)
	}

	content := model.workspaceDataContent(*model.activeWorkspaceTab())
	plain := stripANSI(content)
	if !strings.Contains(plain, "VISUAL") || !strings.Contains(plain, "selection 2:4-2:6") {
		t.Fatalf("visual character selection status missing:\n%s", plain)
	}
	if !strings.Contains(content, defaultTheme().visual.Render("_id")) {
		t.Fatalf("visual character selection should highlight only selected characters:\n%q", content)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(*Model)
	if model.lastCopiedText != "_id" {
		t.Fatalf("lastCopiedText = %q, want selected display characters", model.lastCopiedText)
	}
}

func TestDataVisualCopyWritesSystemClipboard(t *testing.T) {
	var out bytes.Buffer
	copied := ""
	model := NewModel(Options{
		ConfigPath:      filepath.Join(t.TempDir(), "tdb.enc"),
		IconStyle:       IconStyleUnicode,
		ClipboardWriter: &out,
		ClipboardCopier: clipboardCopierFunc(func(text string) error {
			copied = text
			return nil
		}),
	})
	model.page = PageData
	model.focus = FocusMain
	model.width = 72
	model.height = 18
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabData,
		Target:         db.Target{Database: "app", Name: "events", Type: db.ObjectCollection},
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Documents: []result.Document{{
			ID: "doc-1",
			Data: map[string]any{
				"_id":  "doc-1",
				"name": "Ada",
			},
		}}},
	}}
	model.activeTabIndex = 0

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'j'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'v'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRunes, Runes: []rune{'y'}},
	} {
		updated, _ := model.Update(key)
		model = updated.(*Model)
	}

	if copied != "_id" {
		t.Fatalf("system clipboard copied %q, want _id", copied)
	}
	if got := out.String(); got != "" {
		t.Fatalf("OSC52 output = %q, want no fallback when Data copy writes system clipboard", got)
	}
	if model.lastCopiedText != "_id" {
		t.Fatalf("lastCopiedText = %q, want _id", model.lastCopiedText)
	}
}

func TestMouseClickDataViewportMovesDisplayCharacterCursor(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.width = 90
	model.height = 22
	model.focus = FocusSidebar
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabData,
		Target:         db.Target{Database: "app", Name: "events", Type: db.ObjectCollection},
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Documents: []result.Document{{
			ID: "doc-1",
			Data: map[string]any{
				"_id":  "doc-1",
				"name": "Ada",
			},
		}}},
	}}
	model.activeTabIndex = 0
	_ = model.View()

	sidebarWidth := clamp(model.width/5, 22, 32)
	contentX := sidebarWidth + 2
	firstDataY := 7
	updated, _ := model.Update(tea.MouseMsg{
		X:      contentX + 3,
		Y:      firstDataY + 1,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	model = updated.(*Model)

	tab := model.activeWorkspaceTab()
	if model.focus != FocusMain || tab.ResultRow != 1 || tab.ResultColumn != 3 {
		t.Fatalf("focus=%s cursor=row:%d col:%d, want main row:1 col:3 from mouse click", model.focus, tab.ResultRow, tab.ResultColumn)
	}
}

func TestDataTabRejectsWriteVimKeysAsReadOnly(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabData,
		Target:         db.Target{Database: "app", Name: "users", Type: db.ObjectTable},
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Table: &result.Table{
			Columns: []result.Column{{Name: "id"}},
			Rows:    []result.Row{{Values: []any{1}}},
		}},
	}}
	model.activeTabIndex = 0
	model.syncActiveTabState()

	for _, r := range []rune{'i', 'd', 'x'} {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(*Model)
		if model.pending != nil {
			t.Fatalf("pending after %q = %+v, data tab should stay read-only", r, model.pending)
		}
		if !strings.Contains(model.message, "read-only") {
			t.Fatalf("message after %q = %q, want read-only", r, model.message)
		}
	}
}

func TestCopyTextWritesOSC52Sequence(t *testing.T) {
	var out bytes.Buffer
	model := NewModel(Options{
		ConfigPath:      filepath.Join(t.TempDir(), "tdb.enc"),
		IconStyle:       IconStyleUnicode,
		ClipboardWriter: &out,
	})

	model.copyText("alpha")

	if model.lastCopiedText != "alpha" {
		t.Fatalf("lastCopiedText = %q, want alpha", model.lastCopiedText)
	}
	if got, want := out.String(), "\x1b]52;c;YWxwaGE=\x07"; got != want {
		t.Fatalf("clipboard sequence = %q, want %q", got, want)
	}
}

func TestCopyTextUsesSystemClipboardBeforeOSC52(t *testing.T) {
	var out bytes.Buffer
	copied := ""
	model := NewModel(Options{
		ConfigPath:      filepath.Join(t.TempDir(), "tdb.enc"),
		IconStyle:       IconStyleUnicode,
		ClipboardWriter: &out,
		ClipboardCopier: clipboardCopierFunc(func(text string) error {
			copied = text
			return nil
		}),
	})

	model.copyText("alpha")

	if copied != "alpha" {
		t.Fatalf("system clipboard copied %q, want alpha", copied)
	}
	if got := out.String(); got != "" {
		t.Fatalf("OSC52 output = %q, want no fallback when system clipboard succeeds", got)
	}
	if model.message != "copied" {
		t.Fatalf("message = %q, want copied", model.message)
	}
}

func TestCopyTextFallsBackToOSC52WhenSystemClipboardFails(t *testing.T) {
	var out bytes.Buffer
	model := NewModel(Options{
		ConfigPath:      filepath.Join(t.TempDir(), "tdb.enc"),
		IconStyle:       IconStyleUnicode,
		ClipboardWriter: &out,
		ClipboardCopier: clipboardCopierFunc(func(string) error {
			return errors.New("system clipboard unavailable")
		}),
	})

	model.copyText("alpha")

	if got, want := out.String(), "\x1b]52;c;YWxwaGE=\x07"; got != want {
		t.Fatalf("OSC52 fallback = %q, want %q", got, want)
	}
	if model.message != "copied" {
		t.Fatalf("message = %q, want copied after OSC52 fallback", model.message)
	}
}

func TestCopyTextReportsSystemAndOSC52Failures(t *testing.T) {
	model := NewModel(Options{
		ConfigPath:      filepath.Join(t.TempDir(), "tdb.enc"),
		IconStyle:       IconStyleUnicode,
		ClipboardWriter: failingWriter{err: errors.New("osc52 broken")},
		ClipboardCopier: clipboardCopierFunc(func(string) error {
			return errors.New("system clipboard unavailable")
		}),
	})

	model.copyText("alpha")

	for _, want := range []string{"copy failed", "system clipboard unavailable", "osc52 broken"} {
		if !strings.Contains(model.message, want) {
			t.Fatalf("message = %q, want to contain %q", model.message, want)
		}
	}
}

// runCmd synchronously drains an async command chain (the goroutine I/O job and
// its resulting apply message) so tests can assert on the post-completion state.
func runCmd(m *Model, cmd tea.Cmd) {
	for i := 0; cmd != nil && i < 16; i++ {
		msg := cmd()
		if msg == nil {
			return
		}
		_, cmd = m.Update(msg)
	}
}

func newWorkspaceVimModel(t *testing.T) *Model {
	t.Helper()
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc"), IconStyle: IconStyleUnicode})
	model.page = PageBrowser
	model.focus = FocusMain
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.adapter = &workspaceVimAdapter{}
	return model
}

type workspaceVimAdapter struct{}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func (a *workspaceVimAdapter) Test(context.Context) error { return nil }
func (a *workspaceVimAdapter) ListDatabases(context.Context) ([]string, error) {
	return []string{"app"}, nil
}
func (a *workspaceVimAdapter) ListObjects(context.Context, db.Scope) ([]db.Object, error) {
	return nil, nil
}
func (a *workspaceVimAdapter) Preview(context.Context, db.Target, db.Query, db.Page) (result.Set, error) {
	return result.Set{}, nil
}
func (a *workspaceVimAdapter) Insert(context.Context, db.Target, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *workspaceVimAdapter) Update(context.Context, db.Target, db.Key, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *workspaceVimAdapter) Delete(context.Context, db.Target, db.Key) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *workspaceVimAdapter) Execute(_ context.Context, command db.Command) (result.Set, error) {
	return result.Set{Table: &result.Table{
		Columns: []result.Column{{Name: "statement"}},
		Rows:    []result.Row{{Values: []any{command.Text}}},
	}}, nil
}
func (a *workspaceVimAdapter) Close() error { return nil }

func newQueryResultModel(t *testing.T) (*Model, *workspaceTab) {
	t.Helper()
	model := newWorkspaceVimModel(t)
	model.width = 100
	model.height = 30
	model.selectedDB = "app"
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabQuery,
		Title:          "Query",
		QueryText:      "SELECT * FROM users",
		QueryDatabase:  "app",
		Status:         "ok",
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Table: &result.Table{
			Columns: []result.Column{{Name: "id"}, {Name: "url"}},
			Rows: []result.Row{
				{Values: []any{1, "150309/thumbsize_100_100_7545_a_really_long_value_that_overflows_the_view"}},
				{Values: []any{2, "short"}},
			},
		}},
	}}
	model.activeTabIndex = 0
	model.focus = FocusMain
	model.syncActiveTabState()
	return model, model.activeWorkspaceTab()
}

func TestRowDetailLinesTransposeUncapped(t *testing.T) {
	table := result.Table{
		Columns: []result.Column{{Name: "id"}, {Name: "blob"}},
		Rows:    []result.Row{{Values: []any{1, strings.Repeat("x", 120)}}},
	}
	lines := rowDetailLines(table, 0)
	if len(lines) != 2 {
		t.Fatalf("detail lines = %d, want one per column (2)", len(lines))
	}
	if !strings.Contains(lines[1], strings.Repeat("x", 120)) {
		t.Fatalf("value column must not be width-capped: %q", lines[1])
	}
}

func TestQueryResultEnterOpensRowDetail(t *testing.T) {
	model, _ := newQueryResultModel(t)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = updated.(*Model)
	if tab := model.activeWorkspaceTab(); tab.ResultCursorRow != 1 {
		t.Fatalf("ResultCursorRow = %d, want 1", tab.ResultCursorRow)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	if tab := model.activeWorkspaceTab(); !tab.RowDetail {
		t.Fatalf("Enter should open the row detail subpage")
	}
}

func TestRowDetailScrollCopyAndClose(t *testing.T) {
	model, _ := newQueryResultModel(t)
	// Select row 0 (long url) and open detail.
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Move cursor down to the "url" field, then scroll right to reveal the value.
	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	model = updated.(*Model)
	tab := model.activeWorkspaceTab()
	if tab.ResultRow != 1 {
		t.Fatalf("detail cursor row = %d, want 1 (url field)", tab.ResultRow)
	}
	if tab.ResultColumn == 0 {
		t.Fatalf("'l' should scroll the cursor right to reveal long value")
	}

	// 'y' copies the full value of the current field.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(*Model)
	if !strings.Contains(model.lastCopiedText, "150309/thumbsize") {
		t.Fatalf("y should copy the full field value, got %q", model.lastCopiedText)
	}

	// Esc closes the detail subpage.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(*Model)
	if tab := model.activeWorkspaceTab(); tab.RowDetail {
		t.Fatalf("Esc should close the row detail subpage")
	}
}

func TestQueryTabOpensInInsertMode(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	if tab == nil || tab.VimMode != vimModeInsert || tab.WorkspaceFocus != workspaceFocusEditor {
		t.Fatalf("new query tab mode/focus = %v, want insert/editor", tab)
	}
}

func TestQueryInsertEnterExecutesAndClearsBuffer(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	// Tab opens in insert mode; set a buffer with suggestions hidden so Enter
	// executes rather than accepting a suggestion.
	tab.QueryBuffer = "db.users.find()"
	tab.QueryCursor = len(tab.QueryBuffer)
	tab.QuerySuggestionsVisible = false

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)

	tab = model.activeWorkspaceTab()
	if tab.QueryText != "db.users.find()" {
		t.Fatalf("QueryText = %q, want executed statement", tab.QueryText)
	}
	if tab.QueryBuffer != "" {
		t.Fatalf("QueryBuffer = %q, want cleared after execution", tab.QueryBuffer)
	}
}

func TestQueryCtrlJInsertsNewlineInInsertMode(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.openQueryWorkspaceTab()
	for _, r := range "a" {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(*Model)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	model = updated.(*Model)
	for _, r := range "b" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(*Model)
	}
	if got := model.activeWorkspaceTab().QueryBuffer; got != "a\nb" {
		t.Fatalf("QueryBuffer = %q, want \"a\\nb\"", got)
	}
}

func TestQueryCtrlSOpensHistorySearchInAllModes(t *testing.T) {
	for _, mode := range []struct {
		name  string
		vim   vimMode
		focus workspaceFocus
	}{
		{"insert", vimModeInsert, workspaceFocusEditor},
		{"normal", vimModeNormal, workspaceFocusEditor},
		{"result", vimModeNormal, workspaceFocusResult},
	} {
		t.Run(mode.name, func(t *testing.T) {
			model := newWorkspaceVimModel(t)
			model.openQueryWorkspaceTab()
			tab := model.activeWorkspaceTab()
			tab.VimMode = mode.vim
			tab.WorkspaceFocus = mode.focus
			model.syncActiveTabState()

			updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
			model = updated.(*Model)
			if model.modal == nil || model.modal.Kind != modalQueryHistorySearch {
				t.Fatalf("ctrl+s in %s mode did not open history search modal", mode.name)
			}
		})
	}
}

func TestQueryHistorySearchFloatsOverQueryView(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.width = 120
	model.height = 40
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.QueryBuffer = "db.users.find()"
	model.syncActiveTabState()

	model.openQueryHistorySearchModal()

	content := stripANSI(model.mainContent())
	if !strings.Contains(content, "Search:") {
		t.Fatalf("floating popup missing Search field:\n%s", content)
	}
	if !strings.Contains(content, "db:") {
		t.Fatalf("query view should remain visible behind the popup:\n%s", content)
	}
	if model.commandVisible() {
		t.Fatalf("command bar must be hidden while history search popup is open")
	}
}

func TestQueryHistorySearchBoxAdaptsToWidth(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.height = 40
	model.openQueryWorkspaceTab()
	model.openQueryHistorySearchModal()

	model.width = 60 // split-pane sized terminal
	narrow := lipgloss.Width(model.queryHistorySearchFloatingBox())

	model.width = 220 // large terminal
	wide := lipgloss.Width(model.queryHistorySearchFloatingBox())

	if wide <= narrow {
		t.Fatalf("search box should grow with the terminal: narrow=%d wide=%d", narrow, wide)
	}
	if wide <= 74 {
		t.Fatalf("on a big screen the box should exceed the old 72 cap, got %d", wide)
	}
	if narrow >= model.width {
		t.Fatalf("narrow box %d should fit within a split pane", narrow)
	}
}

func TestQueryEmptyInputShowsPlaceholder(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.openQueryWorkspaceTab()
	content := stripANSI(model.workspaceQueryContent(*model.activeWorkspaceTab()))
	if !strings.Contains(content, "Type a query") {
		t.Fatalf("empty query input should show placeholder hint:\n%s", content)
	}
}

func TestUnlockPasswordNotEchoedToCommandBar(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc"), IconStyle: IconStyleUnicode})
	if model.page != PageUnlock {
		t.Fatalf("model should start on unlock page, got %s", model.page)
	}
	for _, r := range "s3cret" {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(*Model)
	}
	if model.commandVisible() {
		t.Fatalf("command bar must stay hidden on unlock page")
	}
	if line := stripANSI(model.commandLineText()); strings.Contains(line, "s3cret") {
		t.Fatalf("password leaked into command line: %q", line)
	}
}
