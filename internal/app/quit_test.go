package app

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func cmdQuits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestCtrlCDoesNotQuit(t *testing.T) {
	model := newWorkspaceVimModel(t)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmdQuits(cmd) {
		t.Fatal("ctrl+c must not quit the program")
	}
	if updated == nil {
		t.Fatal("model should survive ctrl+c")
	}
}

func TestColonQQuitsViaCommandLine(t *testing.T) {
	model := newWorkspaceVimModel(t)
	// Command mode strips the leading ":", so HandleLine receives "q".
	model.HandleLine(context.Background(), "q")
	if !cmdQuits(model.takeCmd()) {
		t.Fatal(":q (command \"q\") should quit")
	}

	for _, alias := range []string{"quit", "exit"} {
		m := newWorkspaceVimModel(t)
		m.HandleLine(context.Background(), alias)
		if !cmdQuits(m.takeCmd()) {
			t.Fatalf("command %q should quit", alias)
		}
	}
}

func TestColonQLiteralQuitsFromUnlockScreen(t *testing.T) {
	model := newWorkspaceVimModel(t)
	model.page = PageUnlock
	// On the unlock screen there is no command line, so the literal ":q" is typed.
	model.HandleLine(context.Background(), ":q")
	if !cmdQuits(model.takeCmd()) {
		t.Fatal(":q should quit even from the unlock screen")
	}
}

func TestQKeyGoesBackDoesNotQuit(t *testing.T) {
	model := newWorkspaceVimModel(t)
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmdQuits(cmd) {
		t.Fatal("the q key should go back, not quit")
	}
}

func pressRune(m *Model, r rune) (*Model, tea.Cmd) {
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return updated.(*Model), cmd
}

func TestUnlockScreenColonOpensQuitOnlyCommandMode(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.page = PageUnlock
	m.input.SetValue("secret") // a partially-typed password

	// ":" opens command mode and preserves the password.
	m, _ = pressRune(m, ':')
	if !m.unlockCommand {
		t.Fatal(": should open the unlock command mode")
	}
	if m.input.Value() != "" || m.unlockPasswordDraft != "secret" {
		t.Fatalf("password should be preserved as draft, input=%q draft=%q", m.input.Value(), m.unlockPasswordDraft)
	}

	// Typing "q" then Enter quits.
	m, _ = pressRune(m, 'q')
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !cmdQuits(cmd) {
		t.Fatal(":q should quit from the unlock command mode")
	}
}

func TestUnlockCommandModeDeniesNonQuitCommands(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.page = PageUnlock
	m.input.SetValue("pw")
	m, _ = pressRune(m, ':')
	for _, r := range "help" {
		m, _ = pressRune(m, r)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmdQuits(cmd) {
		t.Fatal("non-quit commands must not quit")
	}
	if m.unlockCommand {
		t.Fatal("a denied command should leave command mode")
	}
	if m.input.Value() != "pw" {
		t.Fatalf("password should be restored after a denied command, got %q", m.input.Value())
	}
	if !strings.Contains(m.message, "only :q") {
		t.Fatalf("should explain only :q is allowed, got %q", m.message)
	}
}

func TestUnlockCommandModeEscRestoresPassword(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.page = PageUnlock
	m.input.SetValue("pw")
	m, _ = pressRune(m, ':')
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(*Model)
	if m.unlockCommand || m.input.Value() != "pw" {
		t.Fatalf("Esc should restore the password, command=%v input=%q", m.unlockCommand, m.input.Value())
	}
}

func TestUnlockScreenPasswordTypingNotIntercepted(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.page = PageUnlock
	for _, r := range "abc" {
		m, _ = pressRune(m, r)
	}
	if m.unlockCommand {
		t.Fatal("plain typing should not enter command mode")
	}
	if m.input.Value() != "abc" {
		t.Fatalf("password input = %q, want abc", m.input.Value())
	}
}
