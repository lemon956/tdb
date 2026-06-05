package app

import (
	"errors"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHelpModalAutofocusSearchScrollAndCtrlW(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.focus = FocusSidebar
	model.width = 100
	model.height = 14

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	model = updated.(*Model)
	if model.modal == nil || model.modal.Kind != modalHelp || model.focus != FocusContext {
		t.Fatalf("modal/focus after ? = %+v/%s, want help/context", model.modal, model.focus)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	model = updated.(*Model)
	if model.helpSearch != "q" {
		t.Fatalf("helpSearch = %q, want q", model.helpSearch)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	model = updated.(*Model)
	if model.modal == nil || model.modal.ScrollOffset == 0 {
		t.Fatalf("modal scroll offset = %+v, want scrolled", model.modal)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	model = updated.(*Model)
	if model.modal != nil || model.helpOpen {
		t.Fatalf("modal/help after ctrl+w = %+v/%t, want closed", model.modal, model.helpOpen)
	}
	if model.focus != FocusSidebar {
		t.Fatalf("focus after ctrl+w = %s, want sidebar", model.focus)
	}
}

func TestMouseWheelScrollsFocusedModal(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.focus = FocusSidebar
	model.width = 100
	model.height = 14
	model.openHelpPanel()

	updated, _ := model.Update(tea.MouseMsg{
		Button: tea.MouseButtonWheelDown,
		Action: tea.MouseActionPress,
	})
	model = updated.(*Model)

	if model.modal == nil || model.modal.ScrollOffset == 0 {
		t.Fatalf("modal scroll offset = %+v, want wheel to scroll modal", model.modal)
	}
}

func TestCtrlWClosesErrorBoxAndRestoresFocus(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageQuery
	model.focus = FocusSidebar
	model.showErrorBox("Query failed", errors.New("syntax error"))

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	model = updated.(*Model)

	if model.errBox != nil {
		t.Fatalf("errBox after ctrl+w = %+v, want nil", model.errBox)
	}
	if model.focus != FocusSidebar {
		t.Fatalf("focus after error close = %s, want sidebar", model.focus)
	}
}
