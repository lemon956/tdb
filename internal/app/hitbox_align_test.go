package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"tdb/internal/config"
)

// screenRowOf returns the first screen row (Y) of the rendered view whose text
// (ANSI-stripped) contains marker, or -1.
func screenRowOf(view, marker string) int {
	for i, line := range strings.Split(view, "\n") {
		if strings.Contains(ansi.Strip(line), marker) {
			return i
		}
	}
	return -1
}

func newSizedModel(t *testing.T, w, h int) *Model {
	t.Helper()
	m := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	m.width = w
	m.height = h
	return m
}

// The connection form's fields and buttons resolve to the row where they actually
// render (was off by a row before because the modalBox wrapper wasn't accounted
// for).
func TestConnectionFormHitboxesAlign(t *testing.T) {
	m := newSizedModel(t, 120, 40)
	m.page = PageConnections
	m.form = newConnectionForm()
	m.form.chooseDriver(config.DriverMySQL)

	view := m.View() // registers hitboxes for this geometry

	checks := []struct {
		marker string
		id     string
	}{
		{"Host:", "form-field:host"},
		{"Port:", "form-field:port"},
		{"Database:", "form-field:database"},
		{"[ Save ]", "form:save"},
	}
	for _, c := range checks {
		row := screenRowOf(view, c.marker)
		if row < 0 {
			t.Fatalf("marker %q not found in view", c.marker)
		}
		hit, ok := m.hitboxes.Hit(m.sidebarWidth()+6, row)
		if !ok {
			t.Fatalf("no hitbox at the %q row (y=%d)", c.marker, row)
		}
		if hit.ID != c.id {
			t.Fatalf("%q row resolves to %q, want %q", c.marker, hit.ID, c.id)
		}
	}
}

// A confirm dialog's buttons resolve to the rendered button row.
func TestConfirmDialogHitboxAligns(t *testing.T) {
	m := newSizedModel(t, 120, 40)
	m.page = PageConnections
	m.pending = &pendingAction{Kind: "delete"}

	view := m.View()
	row := screenRowOf(view, "[ Confirm ]")
	if row < 0 {
		t.Fatal("confirm button not found in view")
	}
	hit, ok := m.hitboxes.Hit(m.sidebarWidth()+5, row)
	if !ok || hit.ID != "confirm-ok" {
		t.Fatalf("confirm row resolves to %+v, want confirm-ok", hit)
	}
}

// With many connection sessions on a narrow terminal the header stays one row and
// the active tab is rendered and clickable at its real position.
func TestHeaderTabsScrollKeepActiveClickable(t *testing.T) {
	m := newSizedModel(t, 50, 30)
	for i := 0; i < 8; i++ {
		m.sessions = append(m.sessions, connSession{profile: config.Profile{ID: "connection" + string(rune('a'+i)), Driver: config.DriverMySQL}})
	}
	m.activeSession = 6

	line, hits := m.headerTabLayout(50)
	if strings.Contains(line, "\n") {
		t.Fatal("header must be a single line")
	}
	// The active session must be among the visible, clickable tabs.
	var activeHit *headerTabHit
	for i := range hits {
		if hits[i].index == m.activeSession {
			activeHit = &hits[i]
		}
	}
	if activeHit == nil {
		t.Fatal("active session tab should be visible/clickable when scrolled")
	}
	// Its hitbox x must land within the rendered line width.
	if activeHit.x < 0 || activeHit.x >= ansi.StringWidth(line) {
		t.Fatalf("active tab x=%d is outside the header width", activeHit.x)
	}
}
