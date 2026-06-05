package app

import (
	"strings"
	"testing"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/result"

	"tdb/internal/suggest"
)

// TestQuerySuggestionsSuppressedWhenNonSpaceFollowsCursor verifies the popup
// only opens when nothing non-whitespace is glued to the right of the cursor.
func TestQuerySuggestionsSuppressedWhenNonSpaceFollowsCursor(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.selectedDB = "app"
	model.databaseObjects = map[string][]db.Object{"app": {{Name: "users", Type: db.ObjectTable}}}
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.VimMode = vimModeInsert
	tab.QueryBuffer = "SELECT * FROM users"

	// Cursor in the middle of the word `user|s` -> next char is `s` -> suppressed.
	tab.QueryCursor = len("SELECT * FROM user")
	model.refreshQuerySuggestions(tab)
	if tab.QuerySuggestionsVisible {
		t.Fatalf("suggestions should be suppressed when a non-space follows the cursor")
	}

	// Cursor at end of buffer -> nothing follows -> visible.
	tab.QueryBuffer = "SELECT * FROM us"
	tab.QueryCursor = len(tab.QueryBuffer)
	model.refreshQuerySuggestions(tab)
	if !tab.QuerySuggestionsVisible {
		t.Fatalf("suggestions should open at end of buffer, got %+v", tab.QuerySuggestions)
	}

	// Cursor followed by a space -> visible.
	tab.QueryBuffer = "SELECT * FROM us WHERE 1"
	tab.QueryCursor = len("SELECT * FROM us")
	model.refreshQuerySuggestions(tab)
	if !tab.QuerySuggestionsVisible {
		t.Fatalf("suggestions should open when a space follows the cursor")
	}
}

// TestQuerySuggestionOverlayFloatsWithoutShiftingResults checks the popup is a
// floating overlay: enabling it must not move the result rows down.
func TestQuerySuggestionOverlayFloatsWithoutShiftingResults(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.width = 120
	model.height = 40
	model.focus = FocusMain
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.selectedDB = "app"
	model.databaseObjects = map[string][]db.Object{"app": {{Name: "users", Type: db.ObjectTable}}}
	model.openQueryWorkspaceTab()
	tab := model.activeWorkspaceTab()
	tab.WorkspaceFocus = workspaceFocusEditor
	tab.VimMode = vimModeInsert
	tab.QueryBuffer = "SELECT * FROM u"
	tab.QueryCursor = len(tab.QueryBuffer)
	tab.Status = "ok"
	tab.QueryText = "SELECT * FROM u"
	tab.Result = result.Set{Table: &result.Table{
		Columns: []result.Column{{Name: "MARKER_COL"}},
		Rows:    []result.Row{{Values: []any{"row_value"}}},
	}}

	const marker = "MARKER_COL"

	tab.QuerySuggestionsVisible = false
	tab.QuerySuggestions = nil
	rowWithout := lineIndexContaining(stripANSI(model.mainContent()), marker)

	tab.QuerySuggestions = []suggest.Suggestion{{Value: "users", Label: "users"}}
	tab.QuerySuggestionsVisible = true
	on := stripANSI(model.mainContent())
	rowWith := lineIndexContaining(on, marker)

	if rowWithout < 0 || rowWith < 0 {
		t.Fatalf("result marker missing: without=%d with=%d\n%s", rowWithout, rowWith, on)
	}
	if rowWithout != rowWith {
		t.Fatalf("floating popup shifted the result row: without=%d with=%d\n%s", rowWithout, rowWith, on)
	}
	if !strings.Contains(on, "users") {
		t.Fatalf("floating popup should render the suggestion:\n%s", on)
	}
}

func lineIndexContaining(content, needle string) int {
	for i, line := range strings.Split(content, "\n") {
		if strings.Contains(line, needle) {
			return i
		}
	}
	return -1
}
