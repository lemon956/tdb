package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func newRecoverModel(t *testing.T) *Model {
	t.Helper()
	return NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
}

// A panic inside asyncResultMsg.apply (or anywhere in the update tree) must be
// caught and turned into an error box, not crash the program.
func TestUpdateRecoversFromPanic(t *testing.T) {
	m := newRecoverModel(t)
	msg := asyncResultMsg{apply: func(*Model) { panic("boom") }}

	model, _ := m.Update(msg)

	mm := model.(*Model)
	if mm.errBox == nil {
		t.Fatal("expected errBox to be set after a panic in Update")
	}
	if !strings.Contains(mm.errBox.Message, "boom") {
		t.Fatalf("errBox message = %q, want it to contain %q", mm.errBox.Message, "boom")
	}
	if mm.loading.active {
		t.Fatal("loading spinner should be cleared after a recovered panic")
	}
}

// A panic recorded during View() is surfaced on the next Update as an error box.
func TestViewPanicSurfacedOnNextUpdate(t *testing.T) {
	m := newRecoverModel(t)
	m.renderPanicked = true
	m.renderPanicMsg = "render boom"

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	mm := model.(*Model)
	if mm.errBox == nil || !strings.Contains(mm.errBox.Message, "render boom") {
		t.Fatalf("expected render panic surfaced as errBox, got %+v", mm.errBox)
	}
	if mm.renderPanicked {
		t.Fatal("renderPanicked should be reset after surfacing")
	}
}

// Background work that panics is converted into an error-box asyncResultMsg
// instead of taking down the Cmd goroutine.
func TestRunWorkRecoversFromPanic(t *testing.T) {
	m := newRecoverModel(t)
	msg := runWork(context.Background(), func(context.Context) tea.Msg {
		panic("io boom")
	})

	res, ok := msg.(asyncResultMsg)
	if !ok {
		t.Fatalf("expected asyncResultMsg, got %T", msg)
	}
	res.apply(m)
	if m.errBox == nil || !strings.Contains(m.errBox.Message, "io boom") {
		t.Fatalf("expected errBox from recovered work, got %+v", m.errBox)
	}
}
