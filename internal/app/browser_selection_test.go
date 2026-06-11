package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/result"
)

func TestMongoURIWithoutDatabaseKeepsDatabaseEmpty(t *testing.T) {
	form := newConnectionForm()
	form.chooseDriver(config.DriverMongo)
	form.setFieldValue("id", "mongo")
	form.setFieldValue("uri", "mongodb://user:secret@127.0.0.1:27017")

	profile, err := form.buildProfile()
	if err != nil {
		t.Fatalf("buildProfile returned error: %v", err)
	}
	if profile.Database != "" {
		t.Fatalf("Database = %q, want empty", profile.Database)
	}
}

func TestOpenMongoWithoutDefaultDatabaseLeavesDatabaseChoiceToUser(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.vault.Profiles = []config.Profile{{ID: "mongo", Driver: config.DriverMongo}}
	adapter := &browserSelectionAdapter{databases: []string{"app", "admin"}}
	model.openAdapter = func(config.Profile) (db.Adapter, error) { return adapter, nil }

	model.openProfile(context.Background(), "mongo")

	if model.page != PageBrowser {
		t.Fatalf("page = %s, want browser", model.page)
	}
	if model.selectedDB != "" {
		t.Fatalf("selectedDB = %q, want empty", model.selectedDB)
	}
	if len(model.objects) != 0 {
		t.Fatalf("objects = %+v, want empty until database is selected", model.objects)
	}
	if adapter.listObjectsCalls != 0 {
		t.Fatalf("ListObjects calls = %d, want 0", adapter.listObjectsCalls)
	}
}

func TestBrowserKeyboardSelectsDatabaseThenLoadsObjects(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	adapter := &browserSelectionAdapter{
		objectsByDB: map[string][]db.Object{
			"admin": {{Name: "audit", Type: db.ObjectCollection}},
		},
	}
	model.page = PageBrowser
	model.focus = FocusSidebar
	model.activeProfile = &config.Profile{ID: "mongo", Driver: config.DriverMongo}
	model.adapter = adapter
	model.databases = []string{"app", "admin"}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(*Model)
	if model.databaseIndex != 1 {
		t.Fatalf("databaseIndex after down = %d, want 1", model.databaseIndex)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	if model.selectedDB != "admin" {
		t.Fatalf("selectedDB = %q, want admin", model.selectedDB)
	}
	if len(model.objects) != 1 || model.objects[0].Name != "audit" {
		t.Fatalf("objects = %+v, want audit", model.objects)
	}
}

func TestBrowserKeyboardMovesFromDatabaseToFirstObject(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageBrowser
	model.focus = FocusSidebar
	model.activeProfile = &config.Profile{ID: "mysql", Driver: config.DriverMySQL}
	model.adapter = &browserSelectionAdapter{}
	model.databases = []string{"app"}
	model.selectedDB = "app"
	model.objects = []db.Object{{Name: "users", Type: db.ObjectTable}, {Name: "orders", Type: db.ObjectTable}}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(*Model)

	if got.page != PageData || got.target.Name != "users" {
		t.Fatalf("page/target = %s/%s, want data/users", got.page, got.target.Name)
	}
}

func TestWorkspaceTabClickSwitchesTab(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.width = 110
	model.height = 28
	model.focus = FocusSidebar
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.workspaceTabs = []workspaceTab{
		{Kind: workspaceTabData, Title: "users", Target: db.Target{Name: "users"}},
		{Kind: workspaceTabQuery, Title: "Query"},
	}
	model.activeTabIndex = 0
	_ = model.View() // registers hitboxes

	var hit Hitbox
	for _, h := range model.hitboxes {
		if h.ID == "workspace-tab:1" {
			hit = h
			break
		}
	}
	if hit.ID == "" {
		t.Fatalf("missing workspace-tab:1 hitbox: %+v", model.hitboxes)
	}
	model = leftClick(model, hit.X, hit.Y).(*Model)
	if model.activeTabIndex != 1 || model.focus != FocusMain {
		t.Fatalf("clicking a tab should activate it and focus main, got idx=%d focus=%s", model.activeTabIndex, model.focus)
	}
}

func TestMultipleConnectionSessions(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.width = 100
	model.height = 24
	model.vault.Profiles = []config.Profile{
		{ID: "mysql-a", Driver: config.DriverMySQL},
		{ID: "mongo-b", Driver: config.DriverMongo},
	}
	model.openAdapter = func(config.Profile) (db.Adapter, error) { return &browserSelectionAdapter{}, nil }
	model.enterConnectionsManager()

	model.openProfile(context.Background(), "mysql-a")
	runCmd(model, model.takeCmd())
	model.openProfile(context.Background(), "mongo-b")
	runCmd(model, model.takeCmd())

	if len(model.sessions) != 2 || model.activeSession != 1 {
		t.Fatalf("expected 2 sessions with the second active, got %d / active %d", len(model.sessions), model.activeSession)
	}
	if model.activeProfile == nil || model.activeProfile.ID != "mongo-b" {
		t.Fatalf("active profile = %v, want mongo-b", model.activeProfile)
	}

	// Alt+left wraps to the first session.
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	model = updated.(*Model)
	if model.activeSession != 0 || model.activeProfile.ID != "mysql-a" {
		t.Fatalf("alt+left should activate session 0 (mysql-a), got %d", model.activeSession)
	}
	// Alt+2 jumps to the second session.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}, Alt: true})
	model = updated.(*Model)
	if model.activeSession != 1 {
		t.Fatalf("alt+2 should activate session 1, got %d", model.activeSession)
	}

	// Re-opening an open connection deduplicates (switches, no new session).
	model.openProfile(context.Background(), "mysql-a")
	if len(model.sessions) != 2 || model.activeSession != 0 {
		t.Fatalf("re-opening should switch to the existing session, got %d sessions active %d", len(model.sessions), model.activeSession)
	}

	// Ctrl+W with no workspace tabs closes the connection session.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	model = updated.(*Model)
	if len(model.sessions) != 1 {
		t.Fatalf("Ctrl+W should close the connection session, got %d", len(model.sessions))
	}
}

func TestUnlockFocusesSidebar(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.focus = FocusMain
	model.unlock("master-password")
	if model.page != PageConnections || model.focus != FocusSidebar {
		t.Fatalf("after unlock page/focus = %s/%s, want connections/sidebar", model.page, model.focus)
	}
}

func TestSidebarWidthHonorsAndClampsOverride(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.width = 120
	auto := model.sidebarWidth()
	model.setSidebarWidth(48)
	if got := model.sidebarWidth(); got != 48 {
		t.Fatalf("sidebarWidth with override = %d, want 48 (auto was %d)", got, auto)
	}
	model.setSidebarWidth(5) // below minimum
	if got := model.sidebarWidth(); got < 16 {
		t.Fatalf("sidebarWidth = %d, want clamped to >= 16", got)
	}
	model.setSidebarWidth(1000) // above maximum
	if got := model.sidebarWidth(); got > model.width-30 {
		t.Fatalf("sidebarWidth = %d, want clamped to <= width-30", got)
	}
}

func TestDividerDragResizesWidthsOnly(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.width = 120
	model.height = 30
	start := model.sidebarWidth()
	before := model.workspaceContentWidth()

	// Press on the divider, drag right, release.
	updated, _ := model.Update(tea.MouseMsg{X: start, Y: 5, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = updated.(*Model)
	if !model.resizingSidebar {
		t.Fatalf("press on divider should start resizing")
	}
	updated, _ = model.Update(tea.MouseMsg{X: start + 12, Y: 5, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	model = updated.(*Model)
	updated, _ = model.Update(tea.MouseMsg{X: start + 12, Y: 5, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	model = updated.(*Model)

	if model.resizingSidebar {
		t.Fatalf("release should stop resizing")
	}
	if model.sidebarWidth() <= start {
		t.Fatalf("sidebar width = %d, want wider than %d after drag right", model.sidebarWidth(), start)
	}
	if model.workspaceContentWidth() >= before {
		t.Fatalf("main content width should shrink as sidebar grows (was %d, now %d)", before, model.workspaceContentWidth())
	}
}

func TestWorkspaceShowsDatabaseNameOnTabBarLine(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.width = 110
	model.height = 30
	model.focus = FocusMain
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.selectedDB = "shopdb"
	model.adapter = &browserSelectionAdapter{}
	model.openQueryWorkspaceTab()

	content := stripANSI(model.workspaceContent())
	lines := strings.Split(content, "\n")
	// The database name lives on the tab-bar line (line 0), not its own line.
	if len(lines) < 2 || !strings.Contains(lines[0], "shopdb") || !strings.Contains(lines[0], "Query") {
		t.Fatalf("tab-bar line should show both the tab and the database name, got:\n%s", content)
	}
}

func TestBrowserMovementIgnoredWhenWorkspaceFocused(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageBrowser
	model.focus = FocusMain
	model.activeProfile = &config.Profile{ID: "mongo", Driver: config.DriverMongo}
	model.adapter = &browserSelectionAdapter{}
	model.databases = []string{"app", "admin"}

	for _, key := range []tea.KeyMsg{{Type: tea.KeyDown}, {Type: tea.KeyRunes, Runes: []rune{'j'}}} {
		updated, _ := model.Update(key)
		model = updated.(*Model)
	}
	if model.databaseIndex != 0 {
		t.Fatalf("databaseIndex = %d, want 0: sidebar must not move while workspace is focused", model.databaseIndex)
	}
}

func TestQueryPageSidebarFocusMovesTree(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageQuery
	model.focus = FocusSidebar
	model.activeProfile = &config.Profile{ID: "mongo", Driver: config.DriverMongo}
	model.adapter = &browserSelectionAdapter{}
	model.databases = []string{"app", "admin"}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(*Model)
	if model.databaseIndex != 1 {
		t.Fatalf("databaseIndex = %d, want 1: sidebar must move on query page when focused", model.databaseIndex)
	}
}

func TestOpenProfileFocusesSidebar(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.focus = FocusMain
	model.vault.Profiles = []config.Profile{{ID: "mongo", Driver: config.DriverMongo}}
	model.openAdapter = func(config.Profile) (db.Adapter, error) {
		return &browserSelectionAdapter{databases: []string{"app"}}, nil
	}

	model.openProfile(context.Background(), "mongo")

	if model.focus != FocusSidebar {
		t.Fatalf("focus after openProfile = %s, want sidebar", model.focus)
	}
}

func TestBrowserViewRegistersDatabaseHitboxAndMouseDoubleClickLoadsObjects(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageBrowser
	model.width = 100
	model.height = 28
	model.focus = FocusSidebar
	model.activeProfile = &config.Profile{ID: "mongo", Driver: config.DriverMongo}
	model.adapter = &browserSelectionAdapter{
		objectsByDB: map[string][]db.Object{
			"app": {{Name: "users", Type: db.ObjectCollection}},
		},
	}
	model.databases = []string{"app"}

	_ = model.View()
	var dbHit Hitbox
	for _, hit := range model.hitboxes {
		if hit.ID == "database:0" {
			dbHit = hit
			break
		}
	}
	if dbHit.ID == "" {
		t.Fatalf("missing database hitbox: %+v", model.hitboxes)
	}

	model = leftClick(model, dbHit.X, dbHit.Y).(*Model)
	got := leftClick(model, dbHit.X, dbHit.Y).(*Model)
	if got.selectedDB != "app" || len(got.objects) != 1 || got.objects[0].Name != "users" {
		t.Fatalf("selectedDB/objects = %q/%+v, want app/users", got.selectedDB, got.objects)
	}
}

func TestCommandLineLabelMatchesCurrentPage(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageBrowser
	model.focus = FocusCommand
	if got := model.commandLineText(); !strings.HasPrefix(got, "Command:") {
		t.Fatalf("browser command line = %q", got)
	}

	model.page = PageQuery
	if got := model.commandLineText(); !strings.HasPrefix(got, "Command:") {
		t.Fatalf("query command line = %q", got)
	}
}

func TestBrowserUnknownCommandUsesGlobalError(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageBrowser

	model.HandleLine(context.Background(), "SELECT 1")

	if strings.Contains(model.message, "browser command") || !strings.Contains(model.message, "unknown command") {
		t.Fatalf("message = %q, want global unknown command error", model.message)
	}
}

type browserSelectionAdapter struct {
	databases        []string
	objectsByDB      map[string][]db.Object
	listObjectsCalls int
	lastPreview      db.Target
}

func (a *browserSelectionAdapter) Test(context.Context) error { return nil }
func (a *browserSelectionAdapter) ListDatabases(context.Context) ([]string, error) {
	if a.databases == nil {
		return []string{"app"}, nil
	}
	return a.databases, nil
}
func (a *browserSelectionAdapter) ListObjects(_ context.Context, scope db.Scope) ([]db.Object, error) {
	a.listObjectsCalls++
	return a.objectsByDB[scope.Database], nil
}
func (a *browserSelectionAdapter) Preview(_ context.Context, target db.Target, _ db.Query, _ db.Page) (result.Set, error) {
	a.lastPreview = target
	return result.Set{Table: &result.Table{
		Columns: []result.Column{{Name: "id"}},
		Rows:    []result.Row{{Values: []any{1}}},
	}}, nil
}
func (a *browserSelectionAdapter) Insert(context.Context, db.Target, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *browserSelectionAdapter) Update(context.Context, db.Target, db.Key, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *browserSelectionAdapter) Delete(context.Context, db.Target, db.Key) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *browserSelectionAdapter) Execute(context.Context, db.Command) (result.Set, error) {
	return result.Set{}, nil
}
func (a *browserSelectionAdapter) Close() error { return nil }
