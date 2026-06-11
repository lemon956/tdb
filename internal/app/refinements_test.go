package app

import (
	"path/filepath"
	"strings"
	"testing"

	"tdb/internal/config"
	"tdb/internal/suggest"
)

// (#3) The SQL "database" field is optional: a profile can be built without it.
func TestSQLDatabaseIsOptional(t *testing.T) {
	form := newConnectionForm()
	form.chooseDriver(config.DriverMySQL)
	form.setFieldValue("id", "local")
	form.setFieldValue("host", "127.0.0.1")
	form.setFieldValue("port", "3306")
	form.setFieldValue("user", "root")
	// database intentionally left blank
	profile, err := form.buildProfile()
	if err != nil {
		t.Fatalf("building a profile without a database should succeed, got %v", err)
	}
	if profile.Database != "" {
		t.Fatalf("database should be empty, got %q", profile.Database)
	}
}

// (#2) The driver picker supports j/k/g/G.
func TestDriverPickerVimKeys(t *testing.T) {
	m := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	m.form = newConnectionForm()
	if !m.form.selectingDriver {
		t.Fatal("form should be selecting a driver")
	}
	start := m.form.driverIndex
	m.handleConnectionDriverKey(key("j"))
	if m.form.driverIndex != (start+1)%len(connectionFormDrivers) {
		t.Fatalf("j should advance the driver, got %d", m.form.driverIndex)
	}
	m.handleConnectionDriverKey(key("k"))
	if m.form.driverIndex != start {
		t.Fatalf("k should move back, got %d", m.form.driverIndex)
	}
	m.handleConnectionDriverKey(key("G"))
	if m.form.driverIndex != len(connectionFormDrivers)-1 {
		t.Fatalf("G should jump to the last driver, got %d", m.form.driverIndex)
	}
	m.handleConnectionDriverKey(key("g"))
	if m.form.driverIndex != 0 {
		t.Fatalf("g should jump to the first driver, got %d", m.form.driverIndex)
	}
}

// (#4) The header is always a single row so panel hitboxes never drift, even with
// many connection sessions on a narrow terminal.
func TestHeaderStaysSingleLine(t *testing.T) {
	m := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	m.width = 40
	for i := 0; i < 8; i++ {
		m.sessions = append(m.sessions, connSession{})
	}
	header := renderHeader(defaultTheme(), m, m.width)
	if n := strings.Count(header, "\n"); n != 0 {
		t.Fatalf("header should be a single line, found %d newlines", n)
	}
}

// (#6b) The suggestion popup windows around the selected index so a selection past
// the first page stays visible.
func TestSuggestionPopupWindowsAroundSelection(t *testing.T) {
	m := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	tab := workspaceTab{Kind: workspaceTabQuery}
	for i := 0; i < 20; i++ {
		tab.QuerySuggestions = append(tab.QuerySuggestions, suggest.Suggestion{Value: "item_" + string(rune('a'+i))})
	}
	tab.QuerySuggestionsVisible = true
	tab.QuerySuggestionIdx = 12 // well past the first 5/8

	body, _ := m.querySuggestionBoxBody(tab, 40)
	if !strings.Contains(body, "item_"+string(rune('a'+12))) {
		t.Fatalf("the selected item (index 12) should be within the window:\n%s", body)
	}
	if !strings.Contains(body, "▼") || !strings.Contains(body, "▲") {
		t.Fatalf("a windowed list should show more-items indicators:\n%s", body)
	}
}
