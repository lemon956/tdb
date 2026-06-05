package app

import (
	"strings"
	"testing"

	"tdb/internal/config"
)

func TestThemeRendersColoredPanel(t *testing.T) {
	got := renderPanel(defaultTheme(), "Connections", "profiles", FocusSidebar, true, 24, 5)

	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("panel did not include ANSI styling: %q", got)
	}
	if strings.Contains(got, "Connections") || !strings.Contains(got, "profiles") {
		t.Fatalf("panel should hide title and keep body: %q", got)
	}
}

func TestModelViewUsesMultiPanelLayout(t *testing.T) {
	model := NewModel(Options{})
	model.page = PageConnections
	model.width = 110
	model.height = 32

	got := model.View()

	for _, want := range []string{"TDB", "no saved profiles", "No connections.", "Ready", "help"} {
		if !strings.Contains(got, want) {
			t.Fatalf("view missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"NAVIGATION", "WORKSPACE", "CONTEXT", "SHORTCUTS", "ACTIONS", "module=", "page=", "focus="} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("view should not include %q:\n%s", unwanted, got)
		}
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("view did not include ANSI styling:\n%s", got)
	}
}

func TestRenderLayoutUsesFullWindowHeight(t *testing.T) {
	for _, tc := range []struct {
		name        string
		commandOpen bool
	}{
		{name: "command hidden", commandOpen: false},
		{name: "command visible", commandOpen: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := NewModel(Options{})
			model.page = PageConnections
			model.width = 110
			model.height = 24
			if tc.commandOpen {
				model.focusCommand()
			}

			got := model.View()
			lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
			if len(lines) != model.height {
				t.Fatalf("rendered line count = %d, want %d\n%s", len(lines), model.height, got)
			}
		})
	}
}

func TestHeaderShowsConnectionTabs(t *testing.T) {
	model := NewModel(Options{})
	model.width = 110
	model.height = 24
	model.sessions = []connSession{{
		profile: config.Profile{ID: "local", Driver: config.DriverMySQL, Database: "analytics"},
		page:    PageBrowser,
		focus:   FocusSidebar,
	}}
	model.loadSession(0)

	got := stripANSI(model.View())
	if !strings.Contains(got, "local") || !strings.Contains(got, "mysql") {
		t.Fatalf("header should show the connection tab (id + driver):\n%s", got)
	}
	if strings.Contains(got, "DATA") {
		t.Fatalf("header should not show page module DATA:\n%s", got)
	}
}
