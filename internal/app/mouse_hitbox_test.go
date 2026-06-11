package app

import (
	"path/filepath"
	"testing"

	"tdb/internal/config"
)

func TestConnectionRowsAreClickable(t *testing.T) {
	m := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	m.width = 120
	m.height = 40
	m.page = PageConnections
	m.focus = FocusSidebar
	m.vault.Profiles = []config.Profile{
		{ID: "alpha", Driver: config.DriverMySQL},
		{ID: "beta", Driver: config.DriverMongo},
		{ID: "gamma", Driver: config.DriverRedis},
	}
	// Render to register hitboxes for the current (centered popup) geometry.
	view := m.View()

	for i, p := range m.vault.Profiles {
		rowY := screenRowOf(view, p.ID)
		if rowY < 0 {
			t.Fatalf("connection %q not found in rendered popup", p.ID)
		}
		box, ok := findHitbox(m, "connection:"+itoa(i))
		if !ok {
			t.Fatalf("no hitbox registered for connection:%d", i)
		}
		if box.Y != rowY {
			t.Fatalf("connection:%d hitbox Y=%d, but it renders at row %d", i, box.Y, rowY)
		}
		if hit, ok := m.hitboxes.Hit(box.X+1, box.Y); !ok || hit.ID != "connection:"+itoa(i) {
			t.Fatalf("click on connection:%d resolves to %+v", i, hit)
		}
	}
}

func findHitbox(m *Model, id string) (Hitbox, bool) {
	for _, b := range m.hitboxes {
		if b.ID == id {
			return b, true
		}
	}
	return Hitbox{}, false
}

func itoa(i int) string {
	switch i {
	case 0:
		return "0"
	case 1:
		return "1"
	case 2:
		return "2"
	}
	return "?"
}
