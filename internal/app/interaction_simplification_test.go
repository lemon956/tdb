package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/result"
)

func TestCommandBarHiddenUntilCommandFocus(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.width = 100
	model.height = 28

	if view := model.View(); strings.Contains(view, "Command:") {
		t.Fatalf("command bar should be hidden before command focus:\n%s", view)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	got := updated.(*Model)
	if view := got.View(); !strings.Contains(view, "Command:") {
		t.Fatalf("command bar should be visible after ':' focus:\n%s", view)
	}
}

func TestHelpCommandShowsContextPanelWithoutChangingPage(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.width = 100
	model.height = 28

	model.HandleLine(context.Background(), "help")

	if model.page != PageConnections {
		t.Fatalf("page = %s, want connections", model.page)
	}
	if view := model.View(); !strings.Contains(view, "Command help") || !strings.Contains(view, "[ Close ]") {
		t.Fatalf("help panel not rendered:\n%s", view)
	}
}

func TestEditShortcutOpensConnectionFormWithSelectedProfile(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.vault.Profiles = []config.Profile{{
		ID:       "local",
		Driver:   config.DriverMySQL,
		Host:     "127.0.0.1",
		Port:     3306,
		User:     "root",
		Password: "secret",
		Database: "app",
		ReadOnly: true,
	}}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	got := updated.(*Model)

	if got.form == nil {
		t.Fatal("form = nil, want edit form")
	}
	if got.form.selectingDriver || got.form.driver != config.DriverMySQL {
		t.Fatalf("form driver state = %+v", got.form)
	}
	if got.form.fieldValue("id") != "local" || got.form.fieldValue("host") != "127.0.0.1" || got.form.fieldValue("database") != "app" {
		t.Fatalf("form fields not populated: %+v", got.form.fields)
	}
	if !got.form.readOnly {
		t.Fatal("form readonly = false, want true")
	}
}

func TestInvalidShortcutWritesBottomStatus(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	got := updated.(*Model)

	if !strings.Contains(got.message, "unsupported shortcut") {
		t.Fatalf("message = %q, want unsupported shortcut", got.message)
	}
	if view := stripANSI(got.View()); !strings.Contains(view, "unsupported shortcut") {
		t.Fatalf("bottom status missing:\n%s", view)
	}
}

func TestFooterOnlyShowsHelpAndStatus(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.width = 100
	model.height = 28
	model.message = "ready"

	view := stripANSI(model.View())
	if !strings.Contains(view, "help") || !strings.Contains(view, "ready") {
		t.Fatalf("footer missing help/status:\n%s", view)
	}
	for _, unwanted := range []string{"ACTIONS", "SHORTCUTS", "[New]", "[Open]"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("footer should not include %q:\n%s", unwanted, view)
		}
	}
}

func TestQueryErrorShowsClosableErrorPanel(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageQuery
	model.width = 100
	model.height = 28
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.adapter = &errorAdapter{err: errors.New("syntax error near FROM")}

	model.HandleLine(context.Background(), "SELECT FROM")
	runCmd(model, model.takeCmd())

	view := model.View()
	if !strings.Contains(view, "Query failed") || !strings.Contains(view, "syntax error near FROM") || !strings.Contains(view, "[ Close ]") {
		t.Fatalf("error panel not rendered:\n%s", view)
	}
}

func TestMouseClickClosesErrorPanel(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageQuery
	model.width = 100
	model.height = 28
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.adapter = &errorAdapter{err: errors.New("syntax error near FROM")}
	model.HandleLine(context.Background(), "SELECT FROM")
	runCmd(model, model.takeCmd())

	_ = model.View()
	var closeHit Hitbox
	for _, hit := range model.hitboxes {
		if hit.ID == "error:close" {
			closeHit = hit
			break
		}
	}
	if closeHit.ID == "" {
		t.Fatalf("missing error close hitbox: %+v", model.hitboxes)
	}

	updated, _ := model.Update(tea.MouseMsg{X: closeHit.X, Y: closeHit.Y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	got := updated.(*Model)
	if got.errBox != nil {
		t.Fatalf("errBox = %+v, want nil", got.errBox)
	}
}

func TestFocusedPanelUsesColorWithoutTitleText(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.width = 100
	model.height = 28
	model.focus = FocusSidebar

	view := model.View()
	for _, unwanted := range []string{"ACTIVE NAVIGATION", "ACTIVE WORKSPACE", "NAVIGATION", "WORKSPACE"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("panel title %q should be hidden:\n%s", unwanted, view)
		}
	}
	if !strings.Contains(view, "no saved profiles") {
		t.Fatalf("sidebar content missing after hiding title:\n%s", view)
	}
}

func TestCommandFocusClearsPanelHighlightAndRestoresPreviousFocus(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.width = 100
	model.height = 28
	model.focus = FocusSidebar

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	model = updated.(*Model)
	if model.focus != FocusCommand {
		t.Fatalf("focus after : = %s, want command", model.focus)
	}
	view := model.View()
	if strings.Contains(view, "ACTIVE") || strings.Contains(view, "NAVIGATION") || strings.Contains(view, "WORKSPACE") {
		t.Fatalf("command focus should not render active panel titles:\n%s", view)
	}

	model.HandleLine(context.Background(), "help")
	model.closeHelpPanel()
	if model.focus != FocusSidebar {
		t.Fatalf("focus after command/modal close = %s, want restored sidebar", model.focus)
	}
}

func TestEscExitsCommandModeWithInput(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.focus = FocusSidebar
	model.focusCommand()
	model.input.SetValue("help")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(*Model)

	if got.focus != FocusSidebar {
		t.Fatalf("focus after esc = %s, want sidebar", got.focus)
	}
	if got.input.Value() != "" {
		t.Fatalf("input after esc = %q, want empty", got.input.Value())
	}
}

func TestBackspaceOnlyExitsCommandModeWhenInputAlreadyEmpty(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.focus = FocusMain
	model.focusCommand()
	model.input.SetValue("x")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	model = updated.(*Model)
	if model.focus != FocusCommand {
		t.Fatalf("focus after first backspace = %s, want command", model.focus)
	}
	if model.input.Value() != "" {
		t.Fatalf("input after first backspace = %q, want empty", model.input.Value())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	got := updated.(*Model)
	if got.focus != FocusMain {
		t.Fatalf("focus after empty backspace = %s, want main", got.focus)
	}
}

func TestBrowserArrowKeysMoveObjectSelection(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageBrowser
	model.focus = FocusSidebar
	model.objects = []db.Object{{Name: "users"}, {Name: "orders"}}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := updated.(*Model)
	if got.objectIndex != 1 {
		t.Fatalf("objectIndex after down = %d, want 1", got.objectIndex)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyUp})
	got = updated.(*Model)
	if got.objectIndex != 0 {
		t.Fatalf("objectIndex after up = %d, want 0", got.objectIndex)
	}
}

type errorAdapter struct {
	err error
}

func (a *errorAdapter) Test(context.Context) error { return a.err }
func (a *errorAdapter) ListDatabases(context.Context) ([]string, error) {
	return nil, a.err
}
func (a *errorAdapter) ListObjects(context.Context, db.Scope) ([]db.Object, error) {
	return nil, a.err
}
func (a *errorAdapter) Preview(context.Context, db.Target, db.Query, db.Page) (result.Set, error) {
	return result.Set{}, a.err
}
func (a *errorAdapter) Insert(context.Context, db.Target, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, a.err
}
func (a *errorAdapter) Update(context.Context, db.Target, db.Key, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, a.err
}
func (a *errorAdapter) Delete(context.Context, db.Target, db.Key) (result.MutationResult, error) {
	return result.MutationResult{}, a.err
}
func (a *errorAdapter) Execute(context.Context, db.Command) (result.Set, error) {
	return result.Set{}, a.err
}
func (a *errorAdapter) Close() error { return nil }
