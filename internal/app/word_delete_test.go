package app

import (
	"context"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"tdb/internal/config"
)

func TestCommandInputBackspaceWord(t *testing.T) {
	var in CommandInput
	in.SetValue("select foo")
	in.BackspaceWord()
	if in.Value() != "select " {
		t.Fatalf("after word delete = %q, want %q", in.Value(), "select ")
	}
	in.BackspaceWord()
	if in.Value() != "" {
		t.Fatalf("second word delete should empty it, got %q", in.Value())
	}
}

func TestQueryInsertCtrlHDeletesWord(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.openQueryWorkspaceTab()
	tab := m.activeWorkspaceTab()
	tab.VimMode = vimModeInsert
	tab.WorkspaceFocus = workspaceFocusEditor
	tab.QueryBuffer = "select count"
	tab.QueryCursor = len(tab.QueryBuffer)

	// Ctrl+H (Ctrl+Backspace) deletes the previous word.
	m.handleQueryInsertKey(context.Background(), tab, tea.KeyMsg{Type: tea.KeyCtrlH})
	if tab.QueryBuffer != "select " {
		t.Fatalf("ctrl+h should delete a word, got %q", tab.QueryBuffer)
	}
	// Plain Backspace deletes one character.
	m.handleQueryInsertKey(context.Background(), tab, tea.KeyMsg{Type: tea.KeyBackspace})
	if tab.QueryBuffer != "select" {
		t.Fatalf("backspace should delete one char, got %q", tab.QueryBuffer)
	}
}

func TestConnectionFormBackspaceWord(t *testing.T) {
	f := newConnectionForm()
	f.chooseDriver(config.DriverMySQL)
	f.setFieldValue("id", "my conn")
	field, _ := f.currentField()
	field.Cursor = len(field.Value)
	f.backspaceWord()
	if got, _ := f.currentField(); got.Value != "my " {
		t.Fatalf("form word delete = %q, want %q", got.Value, "my ")
	}
}

// A long connection id is clipped to one line so per-row click hitboxes align.
func TestConnectionRowClipsToOneLine(t *testing.T) {
	m := NewModel(Options{ConfigPath: t.TempDir() + "/x.enc"})
	p := config.Profile{ID: strings.Repeat("verylong-connection-name-", 5), Driver: config.DriverMongo, Host: "10.40.4.9", Port: 27017}
	row := m.renderConnectionRow(p, "", 24, false, false)
	if h := lipgloss.Height(row); h != 1 {
		t.Fatalf("connection row should be one line, got height %d", h)
	}
	if w := lipgloss.Width(row); w != 24 {
		t.Fatalf("connection row width = %d, want 24", w)
	}
}

// With long ids in a narrow sidebar, each connection still resolves to its own
// row hitbox (no wrap drift).
func TestConnectionsPopupRowsClickAlign(t *testing.T) {
	m := NewModel(Options{ConfigPath: t.TempDir() + "/x.enc"})
	m.width = 90
	m.height = 30
	m.page = PageConnections
	m.focus = FocusSidebar
	for i := 0; i < 4; i++ {
		m.vault.Profiles = append(m.vault.Profiles, config.Profile{
			ID:     "connprofile" + strconv.Itoa(i),
			Driver: config.DriverMySQL, Host: "10.40.4.9", Port: 3306,
		})
	}
	view := m.View()
	for i, p := range m.vault.Profiles {
		rowY := screenRowOf(view, p.ID)
		box, ok := findHitbox(m, "connection:"+strconv.Itoa(i))
		if !ok || rowY < 0 || box.Y != rowY {
			t.Fatalf("connection:%d hitbox Y=%d does not match rendered row %d (ok=%v)", i, box.Y, rowY, ok)
		}
	}
}
