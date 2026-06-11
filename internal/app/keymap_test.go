package app

import (
	"context"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/result"
)

// Panel focus now switches with Ctrl+H/Ctrl+L (Tab is reserved for completion).
func TestCtrlHLCyclePanelFocus(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.focus = FocusSidebar

	// Tab no longer moves panel focus.
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := updated.(*Model).focus; got != FocusSidebar {
		t.Fatalf("Tab should not change focus, got %s", got)
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	if got := updated.(*Model).focus; got != FocusMain {
		t.Fatalf("focus after ctrl+l = %s, want main", got)
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	if got := updated.(*Model).focus; got != FocusSidebar {
		t.Fatalf("focus after ctrl+h = %s, want sidebar", got)
	}
}

func TestVimHorizontalKeysMovePanelFocus(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.focus = FocusSidebar

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	if got := updated.(*Model).focus; got != FocusMain {
		t.Fatalf("focus after l = %s, want main", got)
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if got := updated.(*Model).focus; got != FocusSidebar {
		t.Fatalf("focus after h = %s, want sidebar", got)
	}
}

func TestHelpAndCommandFocusShortcuts(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.focus = FocusSidebar

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	got := updated.(*Model)
	if got.page != PageConnections || !got.helpOpen {
		t.Fatalf("page/help after ? = %s/%v, want connections/true", got.page, got.helpOpen)
	}
	if got.focus != FocusContext {
		t.Fatalf("focus after ? = %s, want context", got.focus)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	got = updated.(*Model)
	if got.helpOpen || got.focus != FocusSidebar {
		t.Fatalf("help/focus after ctrl+w = %v/%s, want closed/sidebar", got.helpOpen, got.focus)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	if got := updated.(*Model).focus; got != FocusCommand {
		t.Fatalf("focus after : = %s, want command", got)
	}
}

func TestJAndKMoveConnectionSelection(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.vault.Profiles = []config.Profile{{ID: "one"}, {ID: "two"}}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if got := updated.(*Model).connectionIndex; got != 1 {
		t.Fatalf("connectionIndex after j = %d, want 1", got)
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if got := updated.(*Model).connectionIndex; got != 0 {
		t.Fatalf("connectionIndex after k = %d, want 0", got)
	}
}

func TestEnterOpensSelectedConnection(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.vault.Profiles = []config.Profile{
		{ID: "one", Driver: config.DriverMySQL},
		{ID: "two", Driver: config.DriverRedis},
	}
	model.connectionIndex = 1
	model.openAdapter = func(config.Profile) (db.Adapter, error) { return &selectionAdapter{}, nil }

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(*Model)
	if got.page != PageBrowser || got.activeProfile.ID != "two" {
		t.Fatalf("page/profile = %s/%+v, want browser/two", got.page, got.activeProfile)
	}
}

func TestBrowserEnterOpensSelectedObject(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageBrowser
	model.activeProfile = &config.Profile{ID: "one", Driver: config.DriverMySQL}
	model.adapter = &selectionAdapter{}
	model.selectedDB = "app"
	model.objects = []db.Object{{Name: "users", Type: db.ObjectTable}, {Name: "orders", Type: db.ObjectTable}}
	model.objectIndex = 1
	model.focus = FocusSidebar // Enter opens the sidebar selection only when it's focused

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(*Model)
	if got.page != PageData || got.target.Name != "orders" {
		t.Fatalf("page/target = %s/%s, want data/orders", got.page, got.target.Name)
	}
}

type selectionAdapter struct{}

func (a *selectionAdapter) Test(context.Context) error { return nil }
func (a *selectionAdapter) ListDatabases(context.Context) ([]string, error) {
	return []string{"0"}, nil
}
func (a *selectionAdapter) ListObjects(context.Context, db.Scope) ([]db.Object, error) {
	return []db.Object{{Name: "users", Type: db.ObjectTable}}, nil
}
func (a *selectionAdapter) Preview(context.Context, db.Target, db.Query, db.Page) (result.Set, error) {
	return result.Set{Table: &result.Table{
		Columns: []result.Column{{Name: "id"}},
		Rows:    []result.Row{{Values: []any{1}}},
	}}, nil
}
func (a *selectionAdapter) Insert(context.Context, db.Target, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *selectionAdapter) Update(context.Context, db.Target, db.Key, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *selectionAdapter) Delete(context.Context, db.Target, db.Key) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *selectionAdapter) Execute(context.Context, db.Command) (result.Set, error) {
	return result.Set{}, nil
}
func (a *selectionAdapter) Close() error { return nil }
