package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"tdb/internal/config"
	"tdb/internal/history"
	"tdb/internal/result"
)

// TestCursorColorsUnified asserts the text caret, row selection, visual range
// and active-tab highlight all share one background color.
func TestCursorColorsUnified(t *testing.T) {
	theme := defaultTheme()
	wantBg := "48;2;34;211;238" // #22D3EE
	for name, style := range map[string]lipgloss.Style{
		"cursor":   theme.cursor,
		"selected": theme.selected,
		"visual":   theme.visual,
	} {
		if !strings.Contains(style.Render(" "), wantBg) {
			t.Fatalf("%s should use the unified cursor background %s", name, wantBg)
		}
	}
	if string(theme.tabActiveBg) != colCursor {
		t.Fatalf("tabActiveBg = %q, want unified %q", theme.tabActiveBg, colCursor)
	}
}

func TestModalListsHighlightSelectedRow(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.databases = []string{"app", "logs"}
	model.history = history.NewStore(t.TempDir() + "/h.json")
	_ = model.history.Append(history.Entry{ProfileID: model.activeProfileID(), Driver: "mysql", Action: history.ActionQuery, Statement: "SELECT 1"}, nil)
	model.historyIndex = 0
	strongBg := backgroundANSI(defaultTheme().selected)

	for name, content := range map[string]string{
		"databasePicker":     model.databasePickerContent(),
		"queryHistorySearch": model.queryHistorySearchContent(),
		"historyModal":       model.historyModalContent(),
	} {
		if !strings.Contains(content, strongBg) {
			t.Fatalf("%s should highlight the selected row with the unified color:\n%s", name, stripANSI(content))
		}
	}
}

func TestConnectionFormActiveFieldShowsCursor(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.cursorBlinkOn = true
	model.form = newConnectionForm()
	model.form.chooseDriver(config.DriverMySQL)
	model.form.fieldIndex = 0

	content := model.connectionFormContent()
	caret := defaultTheme().cursor.Render(" ")
	if !strings.Contains(content, caret) {
		t.Fatalf("active connection-form field should render an input cursor:\n%s", stripANSI(content))
	}
}

func TestResultViewMarksCursorRowWhenUnfocused(t *testing.T) {
	table := result.Table{
		Columns: []result.Column{{Name: "id"}},
		Rows:    []result.Row{{Values: []any{1}}, {Values: []any{2}}},
	}
	view := ResultView{Height: 5, Width: 4, MaxWidth: 40, MarkCursor: true, Selectable: false, SelectedRow: 1}
	out := view.Render(result.Set{Table: &table})
	if !strings.Contains(out, backgroundANSI(defaultTheme().selectedDim)) {
		t.Fatalf("unfocused result view should mark the cursor row with the dim color:\n%s", stripANSI(out))
	}
}
