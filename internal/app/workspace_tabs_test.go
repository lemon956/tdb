package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/history"
	"tdb/internal/result"
)

func TestOpenPreviewCreatesAndReusesDataTab(t *testing.T) {
	model := newWorkspaceTabModel(t)
	target := db.Target{Database: "app", Name: "users", Type: db.ObjectCollection}

	model.openPreview(context.Background(), target)
	model.openPreview(context.Background(), target)

	if len(model.workspaceTabs) != 1 {
		t.Fatalf("workspaceTabs = %d, want 1 reused tab", len(model.workspaceTabs))
	}
	tab := model.activeWorkspaceTab()
	if tab == nil || tab.Kind != workspaceTabData || tab.Target.Name != "users" || tab.Target.Database != "app" {
		t.Fatalf("active tab = %+v, want data app.users", tab)
	}
	if model.page != PageData {
		t.Fatalf("page = %s, want data for compatibility", model.page)
	}
}

func TestWorkspaceTabBarUsesEqualWidthAndCanHideDatabaseName(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.workspaceTabs = []workspaceTab{
		{Kind: workspaceTabData, Target: db.Target{Database: "analytics", Name: "events", Type: db.ObjectCollection}},
		{Kind: workspaceTabQuery, Title: "Query"},
	}
	model.activeTabIndex = 0

	full := stripANSI(model.workspaceTabBar(50))
	if !strings.Contains(full, "analytics.events") {
		t.Fatalf("wide tab bar should include database name:\n%s", full)
	}
	if !strings.Contains(full, "▸") {
		t.Fatalf("tab bar should show the database indicator on the same line:\n%s", full)
	}

	narrow := stripANSI(model.workspaceTabBar(24))
	if strings.Contains(narrow, "analytics.events") || !strings.Contains(narrow, "events") {
		t.Fatalf("narrow tab bar should hide database name but keep object:\n%s", narrow)
	}
	// The first two cells are the tabs (a trailing cell holds the db indicator).
	widths := visibleCellWidths(narrow)
	if len(widths) < 2 || widths[0] != widths[1] {
		t.Fatalf("tab widths = %+v, want two equal tab widths in %q", widths, narrow)
	}
}

func TestGlobalQueryCommandCreatesDistinctQueryTab(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.page = PageBrowser

	model.HandleLine(context.Background(), "query")

	tab := model.activeWorkspaceTab()
	if tab == nil || tab.Kind != workspaceTabQuery {
		t.Fatalf("active tab = %+v, want query tab", tab)
	}
	if strings.HasPrefix(model.commandLineText(), "Browser command:") || strings.HasPrefix(model.commandLineText(), "Query:") {
		t.Fatalf("command label = %q, want global Command label", model.commandLineText())
	}
	if !strings.Contains(model.workspaceTabBar(40), "Query") {
		t.Fatalf("tab bar missing query tab: %q", model.workspaceTabBar(40))
	}
}

func TestQueryExecutesInSameQueryTabWithResultScroll(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.adapter = &recordingAdapter{}
	model.HandleLine(context.Background(), "query")

	model.HandleLine(context.Background(), "SELECT 1")
	runCmd(model, model.takeCmd())

	tab := model.activeWorkspaceTab()
	if tab == nil || tab.Kind != workspaceTabQuery || tab.Result.Table == nil {
		t.Fatalf("active query tab = %+v, want result table", tab)
	}
	if tab.ResultView.RowOffset != 0 || tab.ResultView.ColumnOffset != 0 {
		t.Fatalf("query result view offsets = %+v, want reset", tab.ResultView)
	}
	if !strings.Contains(model.mainContent(), "SELECT") {
		t.Fatalf("query content should include highlighted statement/result:\n%s", model.mainContent())
	}
}

func TestQueryTabShowsEmptyResultMessageAfterSuccessfulMongoQuery(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.activeProfile = &config.Profile{ID: "mongo", Driver: config.DriverMongo}
	model.workspaceTabs = []workspaceTab{{
		Kind:           workspaceTabQuery,
		Title:          "Query",
		QueryText:      "db.users.find({missing: true})",
		Status:         "ok",
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
	}}
	model.activeTabIndex = 0
	model.focus = FocusMain

	content := stripANSI(model.workspaceQueryContent(*model.activeWorkspaceTab()))
	if !strings.Contains(content, "No documents returned.") {
		t.Fatalf("query content missing empty result message:\n%s", content)
	}
}

func TestQueryTabEmptyResultMessageDoesNotOverrideErrorsOrRows(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.activeProfile = &config.Profile{ID: "mongo", Driver: config.DriverMongo}
	model.focus = FocusMain

	errorTab := workspaceTab{
		Kind:           workspaceTabQuery,
		QueryText:      "db.users.find({})",
		Status:         "ok",
		Error:          "bad query",
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
	}
	errorContent := stripANSI(model.workspaceQueryContent(errorTab))
	if !strings.Contains(errorContent, "Error: bad query") || strings.Contains(errorContent, "No documents returned.") {
		t.Fatalf("error content should show error only:\n%s", errorContent)
	}

	rowTab := workspaceTab{
		Kind:           workspaceTabQuery,
		QueryText:      "db.users.find({})",
		Status:         "ok",
		VimMode:        vimModeNormal,
		WorkspaceFocus: workspaceFocusResult,
		Result: result.Set{Documents: []result.Document{{
			ID:   "1",
			Data: map[string]any{"name": "Ada"},
		}}},
	}
	rowContent := stripANSI(model.workspaceQueryContent(rowTab))
	if strings.Contains(rowContent, "No documents returned.") || !strings.Contains(rowContent, "Ada") {
		t.Fatalf("row content should render result instead of empty state:\n%s", rowContent)
	}
}

func TestQueryPageOpenSelectedObjectCreatesDataTab(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.page = PageQuery
	model.focus = FocusSidebar
	model.databases = []string{"app"}
	model.expandedDBs = map[string]bool{"app": true}
	model.databaseObjects = map[string][]db.Object{"app": {{Name: "users", Type: db.ObjectCollection}}}
	model.workspaceTabs = []workspaceTab{{Kind: workspaceTabQuery, Title: "Query"}}
	model.activeTabIndex = 0
	model.setBrowserCursorByNodeID("object:0")

	model.runPageAction(context.Background(), actionOpen)

	tab := model.activeWorkspaceTab()
	if model.page != PageData || tab == nil || tab.Kind != workspaceTabData || tab.Target.Name != "users" {
		t.Fatalf("page/tab = %s/%+v, want Data tab for users", model.page, tab)
	}
}

func TestQueryPageMouseSecondClickObjectCreatesDataTab(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.page = PageQuery
	model.focus = FocusSidebar
	model.width = 100
	model.height = 24
	model.databases = []string{"app"}
	model.expandedDBs = map[string]bool{"app": true}
	model.databaseObjects = map[string][]db.Object{"app": {{Name: "users", Type: db.ObjectCollection}}}
	model.workspaceTabs = []workspaceTab{{Kind: workspaceTabQuery, Title: "Query"}}
	model.activeTabIndex = 0
	_ = model.View()

	model = leftClick(model, 2, 4).(*Model)
	model = leftClick(model, 2, 4).(*Model)

	tab := model.activeWorkspaceTab()
	if model.page != PageData || tab == nil || tab.Kind != workspaceTabData || tab.Target.Name != "users" {
		t.Fatalf("page/tab after second click = %s/%+v, want Data tab for users", model.page, tab)
	}
}

func TestQueryTabBindsDatabaseAndClearsBufferAfterExecute(t *testing.T) {
	model := newWorkspaceTabModel(t)
	adapter := &queryCommandAdapter{}
	model.adapter = adapter
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.selectedDB = "analytics"
	model.history = history.NewStore(filepath.Join(t.TempDir(), "history.json"))
	model.HandleLine(context.Background(), "query")
	tab := model.activeWorkspaceTab()
	if tab == nil || tab.QueryDatabase != "analytics" {
		t.Fatalf("QueryDatabase = %q, want analytics", tab.QueryDatabase)
	}

	tab.QueryBuffer = "SELECT * FROM events"
	tab.QueryCursor = len(tab.QueryBuffer)
	model.executeQueryInActiveTab(context.Background(), tab.QueryBuffer)
	runCmd(model, model.takeCmd())

	tab = model.activeWorkspaceTab()
	if adapter.last.Database != "analytics" {
		t.Fatalf("command database = %q, want analytics", adapter.last.Database)
	}
	if tab.QueryBuffer != "" || tab.QueryCursor != 0 {
		t.Fatalf("query buffer/cursor = %q/%d, want cleared", tab.QueryBuffer, tab.QueryCursor)
	}
	if tab.QueryText != "SELECT * FROM events" {
		t.Fatalf("QueryText = %q, want last executed statement", tab.QueryText)
	}
	entries, err := model.history.List(model.activeProfileID(), 1)
	if err != nil {
		t.Fatalf("List history: %v", err)
	}
	if len(entries) != 1 || entries[0].Database != "analytics" {
		t.Fatalf("history entries = %+v, want database analytics", entries)
	}
}

func TestQueryInsertModeNavigatesHistoryByDriver(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.history = history.NewStore(filepath.Join(t.TempDir(), "history.json"))
	now := time.Now().UTC()
	for _, entry := range []history.Entry{
		{ID: "mysql-old", ProfileID: "other-a", Driver: "mysql", Action: history.ActionQuery, Statement: "SELECT old", Status: history.StatusOK, StartedAt: now.Add(-time.Minute)},
		{ID: "mongo", ProfileID: "mongo-a", Driver: "mongo", Action: history.ActionQuery, Statement: "db.users.find()", Status: history.StatusOK, StartedAt: now},
		{ID: "mysql-new", ProfileID: "other-b", Driver: "mysql", Action: history.ActionQuery, Statement: "SELECT newest", Status: history.StatusOK, StartedAt: now.Add(time.Minute)},
	} {
		if err := model.history.Append(entry, nil); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.VimMode = vimModeInsert
	tab.QueryBuffer = "draft"
	tab.QueryCursor = len(tab.QueryBuffer)

	for _, want := range []struct {
		key   tea.KeyMsg
		value string
	}{
		{tea.KeyMsg{Type: tea.KeyUp}, "SELECT newest"},
		{tea.KeyMsg{Type: tea.KeyUp}, "SELECT old"},
		{tea.KeyMsg{Type: tea.KeyDown}, "SELECT newest"},
		{tea.KeyMsg{Type: tea.KeyDown}, "draft"},
	} {
		updated, _ := model.Update(want.key)
		model = updated.(*Model)
		if got := model.activeWorkspaceTab().QueryBuffer; got != want.value {
			t.Fatalf("QueryBuffer after %s = %q, want %q", want.key.String(), got, want.value)
		}
	}
}

func TestQueryHistorySearchRanksByHotnessThenRecency(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.history = history.NewStore(filepath.Join(t.TempDir(), "history.json"))
	now := time.Now().UTC()
	// "hot" query runs 3 times (literals differ -> merges); "cold" runs once but
	// more recently. Hotness must outrank recency.
	for i := 0; i < 3; i++ {
		if err := model.history.Append(history.Entry{
			ProfileID: "p", Driver: "mysql", Action: history.ActionQuery,
			Statement: fmt.Sprintf("SELECT * FROM hot WHERE id = %d", i),
			Status:    history.StatusOK, StartedAt: now.Add(time.Duration(i) * time.Minute),
		}, nil); err != nil {
			t.Fatalf("append hot: %v", err)
		}
	}
	if err := model.history.Append(history.Entry{
		ProfileID: "p", Driver: "mysql", Action: history.ActionQuery,
		Statement: "SELECT * FROM cold", Status: history.StatusOK, StartedAt: now.Add(time.Hour),
	}, nil); err != nil {
		t.Fatalf("append cold: %v", err)
	}

	model.openQueryHistorySearchModal()
	entries := model.filteredQueryHistoryEntries()
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (hot merged + cold)", len(entries))
	}
	if entries[0].ExecutionCount != 3 || !strings.Contains(entries[0].Statement, "hot") {
		t.Fatalf("hottest entry should rank first, got %+v", entries[0])
	}
	if entries[1].ExecutionCount != 1 || !strings.Contains(entries[1].Statement, "cold") {
		t.Fatalf("cold entry should rank last, got %+v", entries[1])
	}

	content := stripANSI(model.queryHistorySearchContent())
	if !strings.Contains(content, "×3") {
		t.Fatalf("history rows should show a hotness badge:\n%s", content)
	}
}

func TestQueryHistorySearchModalFiltersByDriverAndFillsBuffer(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.history = history.NewStore(filepath.Join(t.TempDir(), "history.json"))
	now := time.Now().UTC()
	for _, entry := range []history.Entry{
		{ID: "mysql", ProfileID: "other-a", Driver: "mysql", Action: history.ActionQuery, Statement: "SELECT * FROM users", Status: history.StatusOK, StartedAt: now.Add(time.Minute)},
		{ID: "mongo", ProfileID: "mongo-a", Driver: "mongo", Action: history.ActionQuery, Statement: "db.users.find()", Status: history.StatusOK, StartedAt: now},
	} {
		if err := model.history.Append(entry, nil); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.VimMode = vimModeInsert

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(*Model)
	if model.modal == nil || model.modal.Kind != modalQueryHistorySearch {
		t.Fatalf("modal = %+v, want query history search", model.modal)
	}
	for _, r := range "user" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(*Model)
	}
	content := model.modalContent()
	if !strings.Contains(content, "SELECT * FROM users") || strings.Contains(content, "db.users.find") {
		t.Fatalf("query history search content not scoped/filtered:\n%s", content)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	tab = model.activeWorkspaceTab()
	if model.modal != nil {
		t.Fatalf("modal still open: %+v", model.modal)
	}
	if tab.QueryBuffer != "SELECT * FROM users" || tab.QueryCursor != len(tab.QueryBuffer) {
		t.Fatalf("query buffer/cursor = %q/%d, want selected history", tab.QueryBuffer, tab.QueryCursor)
	}
	if model.focus != FocusMain {
		t.Fatalf("focus = %s, want main after selecting history", model.focus)
	}
}

func TestQueryEditorSuggestsObjectsAndEnterAcceptsSelection(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.selectedDB = "app"
	model.databaseObjects = map[string][]db.Object{
		"app": {
			{Name: "users", Type: db.ObjectTable},
			{Name: "orders", Type: db.ObjectTable},
		},
	}
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.VimMode = vimModeInsert
	tab.QueryBuffer = "SELECT * FROM u"
	tab.QueryCursor = len(tab.QueryBuffer)
	model.refreshQuerySuggestions(tab)

	if !tab.QuerySuggestionsVisible || len(tab.QuerySuggestions) == 0 || tab.QuerySuggestions[0].Value != "users" {
		t.Fatalf("query suggestions = visible:%t %+v, want users", tab.QuerySuggestionsVisible, tab.QuerySuggestions)
	}
	content := stripANSI(model.mainContent())
	if !strings.Contains(content, "users") {
		t.Fatalf("floating suggestions missing from main content:\n%s", content)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	tab = model.activeWorkspaceTab()
	if tab.QueryBuffer != "SELECT * FROM users" {
		t.Fatalf("QueryBuffer = %q, want object suggestion accepted", tab.QueryBuffer)
	}
	if tab.QuerySuggestionsVisible {
		t.Fatal("query suggestions still visible after accepting")
	}
}

func TestQuerySuggestionsUseCursorTokenAndRefreshAfterCursorMove(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.selectedDB = "app"
	model.databaseObjects = map[string][]db.Object{
		"app": {
			{Name: "users", Type: db.ObjectTable},
			{Name: "orders", Type: db.ObjectTable},
		},
	}
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.VimMode = vimModeInsert
	tab.QueryBuffer = "SELECT * FROM u WHERE o = 1"
	tab.QueryCursor = strings.Index(tab.QueryBuffer, "u WHERE") + 1
	model.refreshQuerySuggestions(tab)
	if len(tab.QuerySuggestions) == 0 || tab.QuerySuggestions[0].Value != "users" {
		t.Fatalf("suggestions at cursor = %+v, want users from middle token", tab.QuerySuggestions)
	}

	tab.QueryCursor = strings.Index(tab.QueryBuffer, "o = 1") + 1
	model.refreshQuerySuggestions(tab)
	// After WHERE the token at the cursor ("o") drives filtering; ordering now
	// leads with fields/functions rather than tables, so just assert the cursor
	// token is what's being completed.
	if len(tab.QuerySuggestions) == 0 {
		t.Fatalf("expected suggestions for token 'o', got none")
	}
	first := tab.QuerySuggestions[0]
	key := first.Value
	if first.Match != "" {
		key = first.Match
	}
	if !strings.HasPrefix(strings.ToLower(key), "o") {
		t.Fatalf("suggestions after cursor move = %+v, want a match for token 'o'", first)
	}
}

func TestQuerySuggestionsLoadMongoMetadataFields(t *testing.T) {
	model := newWorkspaceTabModel(t)
	adapter := &queryMetadataAdapter{metadata: db.ObjectMetadata{Fields: []db.MetadataField{
		{Name: "room_id", Type: "int"},
		{Name: "msg_time", Type: "long"},
	}}}
	model.adapter = adapter
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMongo}
	model.selectedDB = "app"
	model.databaseObjects = map[string][]db.Object{"app": {{Name: "lp_pay_exposure", Type: db.ObjectCollection}}}
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.VimMode = vimModeInsert
	tab.QueryBuffer = "db.lp_pay_exposure.find({roo"
	tab.QueryCursor = len(tab.QueryBuffer)

	model.refreshQuerySuggestions(tab)
	model.refreshQuerySuggestions(tab)

	if adapter.calls != 1 {
		t.Fatalf("metadata calls = %d, want cached single call", adapter.calls)
	}
	if adapter.lastTarget.Database != "app" || adapter.lastTarget.Name != "lp_pay_exposure" {
		t.Fatalf("metadata target = %+v, want app.lp_pay_exposure", adapter.lastTarget)
	}
	if len(tab.QuerySuggestions) == 0 || tab.QuerySuggestions[0].Value != "room_id" {
		t.Fatalf("query suggestions = %+v, want room_id field", tab.QuerySuggestions)
	}
}

func TestQuerySuggestionsLoadSQLMetadataFields(t *testing.T) {
	model := newWorkspaceTabModel(t)
	adapter := &queryMetadataAdapter{metadata: db.ObjectMetadata{Fields: []db.MetadataField{
		{Name: "name", Type: "varchar"},
		{Name: "created_at", Type: "datetime"},
	}}}
	model.adapter = adapter
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.selectedDB = "app"
	model.databaseObjects = map[string][]db.Object{"app": {{Name: "users", Type: db.ObjectTable}}}
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.VimMode = vimModeInsert
	tab.QueryBuffer = "SELECT * FROM users WHERE na"
	tab.QueryCursor = len(tab.QueryBuffer)

	model.refreshQuerySuggestions(tab)

	if adapter.calls != 1 {
		t.Fatalf("metadata calls = %d, want one call", adapter.calls)
	}
	if adapter.lastTarget.Database != "app" || adapter.lastTarget.Name != "users" {
		t.Fatalf("metadata target = %+v, want app.users", adapter.lastTarget)
	}
	// After WHERE, the field leads and is qualified with the FROM table.
	if len(tab.QuerySuggestions) == 0 || tab.QuerySuggestions[0].Value != "users.name" {
		t.Fatalf("query suggestions = %+v, want users.name field", tab.QuerySuggestions)
	}
	if tab.QuerySuggestions[0].Match != "name" {
		t.Fatalf("qualified field should still match by bare name, got %q", tab.QuerySuggestions[0].Match)
	}
}

func TestQueryInsertModeLeftRightRefreshesSuggestions(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.selectedDB = "app"
	model.databaseObjects = map[string][]db.Object{
		"app": {
			{Name: "users", Type: db.ObjectTable},
			{Name: "orders", Type: db.ObjectTable},
		},
	}
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.VimMode = vimModeInsert
	tab.QueryBuffer = "SELECT * FROM u WHERE o"
	tab.QueryCursor = len(tab.QueryBuffer)
	model.refreshQuerySuggestions(tab)
	// After WHERE the token "o" is completed (fields/functions lead now, so the
	// exact head is no longer the "orders" table — just assert it matches "o").
	if len(tab.QuerySuggestions) == 0 || !strings.HasPrefix(strings.ToLower(tab.QuerySuggestions[0].Value), "o") {
		t.Fatalf("initial suggestions = %+v, want a match for 'o'", tab.QuerySuggestions)
	}

	// Move the cursor to just after `u` (followed by a space) so the new "only
	// complete when nothing is glued to the right" rule keeps the popup visible.
	for i := 0; i < len(" WHERE o"); i++ {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyLeft})
		model = updated.(*Model)
	}
	tab = model.activeWorkspaceTab()
	if len(tab.QuerySuggestions) == 0 || tab.QuerySuggestions[0].Value != "users" {
		t.Fatalf("suggestions after left movement = %+v, want users", tab.QuerySuggestions)
	}
}

func TestQuerySuggestionsRenderAsCursorAnchoredPopup(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.selectedDB = "app"
	model.databaseObjects = map[string][]db.Object{"app": {{Name: "users", Type: db.ObjectTable}}}
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.VimMode = vimModeInsert
	tab.QueryBuffer = "SELECT * FROM u"
	tab.QueryCursor = len(tab.QueryBuffer)
	model.refreshQuerySuggestions(tab)

	content := stripANSI(model.mainContent())
	if strings.Contains(content, "Suggestions:") {
		t.Fatalf("query suggestions should render as popup without Suggestions heading:\n%s", content)
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	queryLine := -1
	popupLine := -1
	for i, line := range lines {
		if queryLine < 0 && strings.Contains(line, "SELECT * FROM u") {
			queryLine = i
		}
		if strings.Contains(line, "users") && !strings.Contains(line, "SELECT * FROM u") {
			popupLine = i
		}
	}
	// The floating box has a top border row, so "users" lands two rows below the
	// cursor line (cursor line + border + first item).
	if queryLine < 0 || popupLine != queryLine+2 {
		t.Fatalf("popup should float just below cursor line, query=%d popup=%d:\n%s", queryLine, popupLine, content)
	}
	if leadingSpaces(lines[popupLine]) < len("> SELECT * FROM ")-1 {
		t.Fatalf("popup should be anchored near cursor column, line=%q", lines[popupLine])
	}
}

func TestQuerySuggestionsPopupClampsToWorkspaceWidth(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.width = 54
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.selectedDB = "app"
	model.databaseObjects = map[string][]db.Object{"app": {{Name: "very_long_table_name_for_popup", Type: db.ObjectTable}}}
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.VimMode = vimModeInsert
	tab.QueryBuffer = "SELECT * FROM very"
	tab.QueryCursor = len(tab.QueryBuffer)
	model.refreshQuerySuggestions(tab)

	content := stripANSI(model.mainContent())
	width := model.workspaceContentWidth()
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		if len([]rune(line)) > width {
			t.Fatalf("popup line exceeds workspace width %d: %q\n%s", width, line, content)
		}
	}
}

func TestCtrlTabShortcutsSwitchAndCloseWorkspaceTabs(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.workspaceTabs = []workspaceTab{
		{Kind: workspaceTabData, Target: db.Target{Database: "app", Name: "users", Type: db.ObjectCollection}},
		{Kind: workspaceTabQuery, Title: "Query"},
	}
	model.activeTabIndex = 0

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlRight})
	model = updated.(*Model)
	if model.activeTabIndex != 1 {
		t.Fatalf("activeTabIndex after ctrl+right = %d, want 1", model.activeTabIndex)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlLeft})
	model = updated.(*Model)
	if model.activeTabIndex != 0 {
		t.Fatalf("activeTabIndex after ctrl+left = %d, want 0", model.activeTabIndex)
	}

	// Ctrl+L is now a panel switch, not a tab switch: the active tab is unchanged.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	model = updated.(*Model)
	if model.activeTabIndex != 0 {
		t.Fatalf("ctrl+l should no longer switch tabs, activeTabIndex = %d, want 0", model.activeTabIndex)
	}

	// Move to the query tab via the arrow alias, then close it with Ctrl+W.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlRight})
	model = updated.(*Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	model = updated.(*Model)
	if len(model.workspaceTabs) != 1 || model.workspaceTabs[0].Kind != workspaceTabData {
		t.Fatalf("tabs after ctrl+w = %+v, want only data tab", model.workspaceTabs)
	}
}

func TestGlobalHistoryCommandOpensCurrentProfileModalNewestFirst(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.history = history.NewStore(filepath.Join(t.TempDir(), "history.json"))
	now := time.Now().UTC()
	for _, entry := range []history.Entry{
		{ID: "old", ProfileID: "mongo", Driver: "mongo", Action: history.ActionQuery, Statement: "old command", Status: history.StatusOK, StartedAt: now.Add(-time.Hour)},
		{ID: "other", ProfileID: "mysql", Driver: "mysql", Action: history.ActionQuery, Statement: "other command", Status: history.StatusOK, StartedAt: now},
		{ID: "new", ProfileID: "mongo", Driver: "mongo", Action: history.ActionQuery, Statement: "new command", Status: history.StatusOK, StartedAt: now.Add(time.Minute)},
	} {
		if err := model.history.Append(entry, nil); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}

	model.HandleLine(context.Background(), "history")

	if model.modal == nil || model.modal.Kind != modalHistory {
		t.Fatalf("modal = %+v, want history modal", model.modal)
	}
	content := model.modalContent()
	if !strings.Contains(content, "new command") || !strings.Contains(content, "old command") {
		t.Fatalf("history modal missing current profile commands:\n%s", content)
	}
	if strings.Contains(content, "other command") {
		t.Fatalf("history modal should exclude other profile:\n%s", content)
	}
	if strings.Index(content, "new command") > strings.Index(content, "old command") {
		t.Fatalf("history modal not newest first:\n%s", content)
	}
}

func TestNewAndHelpCommandsUseModal(t *testing.T) {
	model := newWorkspaceTabModel(t)

	model.HandleLine(context.Background(), "new")
	if model.modal == nil || model.modal.Kind != modalForm || model.form == nil {
		t.Fatalf("new modal/form = %+v/%+v, want form modal", model.modal, model.form)
	}

	model.form = nil
	model.modal = nil
	model.HandleLine(context.Background(), "help")
	if model.modal == nil || model.modal.Kind != modalHelp {
		t.Fatalf("help modal = %+v, want help modal", model.modal)
	}
	model.input.SetValue("query")
	model.syncHelpSearch()
	if !strings.Contains(model.modalContent(), "Query") {
		t.Fatalf("help search should keep query group:\n%s", model.modalContent())
	}
}

func newWorkspaceTabModel(t *testing.T) *Model {
	t.Helper()
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc"), IconStyle: IconStyleUnicode})
	model.page = PageBrowser
	model.focus = FocusMain
	model.activeProfile = &config.Profile{ID: "mongo", Driver: config.DriverMongo}
	model.adapter = &workspaceTabAdapter{}
	model.selectedDB = "app"
	return model
}

func visibleCellWidths(value string) []int {
	parts := strings.Split(strings.TrimSpace(value), "|")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		out = append(out, len([]rune(part)))
	}
	return out
}

func leadingSpaces(value string) int {
	count := 0
	for _, r := range value {
		if r != ' ' {
			return count
		}
		count++
	}
	return count
}

type workspaceTabAdapter struct{}

func (a *workspaceTabAdapter) Test(context.Context) error { return nil }
func (a *workspaceTabAdapter) ListDatabases(context.Context) ([]string, error) {
	return []string{"app"}, nil
}
func (a *workspaceTabAdapter) ListObjects(context.Context, db.Scope) ([]db.Object, error) {
	return []db.Object{{Name: "users", Type: db.ObjectCollection}}, nil
}
func (a *workspaceTabAdapter) Preview(context.Context, db.Target, db.Query, db.Page) (result.Set, error) {
	return result.Set{Documents: []result.Document{{ID: "1", Data: map[string]any{"name": "Ada"}}}}, nil
}
func (a *workspaceTabAdapter) Insert(context.Context, db.Target, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *workspaceTabAdapter) Update(context.Context, db.Target, db.Key, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *workspaceTabAdapter) Delete(context.Context, db.Target, db.Key) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *workspaceTabAdapter) Execute(context.Context, db.Command) (result.Set, error) {
	return result.Set{Table: &result.Table{
		Columns: []result.Column{{Name: "ok"}},
		Rows:    []result.Row{{Values: []any{1}}},
	}}, nil
}
func (a *workspaceTabAdapter) Close() error { return nil }

type queryCommandAdapter struct {
	workspaceTabAdapter
	last db.Command
}

func (a *queryCommandAdapter) Execute(_ context.Context, command db.Command) (result.Set, error) {
	a.last = command
	return result.Set{Table: &result.Table{
		Columns: []result.Column{{Name: "statement"}},
		Rows:    []result.Row{{Values: []any{command.Text}}},
	}}, nil
}

type queryMetadataAdapter struct {
	workspaceTabAdapter
	metadata   db.ObjectMetadata
	calls      int
	lastTarget db.Target
}

func (a *queryMetadataAdapter) Metadata(_ context.Context, target db.Target) (db.ObjectMetadata, error) {
	a.calls++
	a.lastTarget = target
	return a.metadata, nil
}
