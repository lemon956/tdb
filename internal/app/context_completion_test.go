package app

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"tdb/internal/db"
	"tdb/internal/suggest"
)

func TestSQLClauseWant(t *testing.T) {
	cases := []struct {
		seg  string
		want suggest.Want
	}{
		{"select ", suggest.WantFields},
		{"select id from ", suggest.WantTables},
		{"select * from t where ", suggest.WantFields},
		{"select * from t join ", suggest.WantTables},
		{"update ", suggest.WantTables},
		{"", suggest.WantAny},
	}
	for _, c := range cases {
		if got := sqlClauseWant(c.seg); got != c.want {
			t.Fatalf("sqlClauseWant(%q) = %v, want %v", c.seg, got, c.want)
		}
	}
}

func TestCursorAfterDot(t *testing.T) {
	if !cursorAfterDot("select t.ema") {
		t.Fatal("t.ema should be detected as qualified")
	}
	if cursorAfterDot("select ema") {
		t.Fatal("bare ema should not be qualified")
	}
}

// A bare ';' (or trailing whitespace after one) must not re-open the popup.
func TestSemicolonSuppressesSuggestions(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.openQueryWorkspaceTab()
	tab := m.activeWorkspaceTab()
	tab.QueryBuffer = "select 1;"
	tab.QueryCursor = len(tab.QueryBuffer)
	m.refreshQuerySuggestions(tab)
	if tab.QuerySuggestionsVisible {
		t.Fatal("suggestions should be suppressed right after ';'")
	}

	tab.QueryBuffer = "select 1; "
	tab.QueryCursor = len(tab.QueryBuffer)
	m.refreshQuerySuggestions(tab)
	if tab.QuerySuggestionsVisible {
		t.Fatal("suggestions should stay suppressed after '; '")
	}
}

func TestQueryValidationWarning(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.openQueryWorkspaceTab()
	tab := m.activeWorkspaceTab()

	tab.QueryBuffer = "SELECT (1 FROM t" // unbalanced paren
	m.refreshQuerySuggestions(tab)
	if tab.QueryWarning == "" {
		t.Fatal("invalid SQL should set a non-blocking warning")
	}

	tab.QueryBuffer = "SELECT 1 FROM t"
	m.refreshQuerySuggestions(tab)
	if tab.QueryWarning != "" {
		t.Fatalf("valid SQL should clear the warning, got %q", tab.QueryWarning)
	}
}

// (F) After FROM the completion leads with table names once the DB objects load.
func TestFromSuggestsTableNames(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.selectedDB = "app"
	m.databaseObjects = map[string][]db.Object{"app": {
		{Name: "users", Type: db.ObjectTable},
		{Name: "orders", Type: db.ObjectTable},
	}}
	m.openQueryWorkspaceTab()
	tab := m.activeWorkspaceTab()
	tab.QueryBuffer = "select * from "
	tab.QueryCursor = len(tab.QueryBuffer)
	m.refreshQuerySuggestions(tab)

	if len(tab.QuerySuggestions) == 0 {
		t.Fatal("expected suggestions after FROM")
	}
	first := tab.QuerySuggestions[0].Value
	if first != "users" && first != "orders" {
		t.Fatalf("FROM should lead with a table name, got %q", first)
	}
}

type objLoadAdapter struct {
	metaStub
	objects []db.Object
	calls   int
}

func (a *objLoadAdapter) ListObjects(context.Context, db.Scope) ([]db.Object, error) {
	a.calls++
	return a.objects, nil
}

// (F) Opening a query tab loads the database's object list once.
func TestOpenQueryTabLoadsObjects(t *testing.T) {
	m := newWorkspaceVimModel(t)
	stub := &objLoadAdapter{objects: []db.Object{{Name: "users", Type: db.ObjectTable}}}
	m.adapter = stub
	m.selectedDB = "app"
	m.databaseObjects = map[string][]db.Object{}

	m.openQueryWorkspaceTab()
	if stub.calls != 1 {
		t.Fatalf("opening a query tab should load objects once, calls=%d", stub.calls)
	}
	if len(m.databaseObjects["app"]) != 1 {
		t.Fatalf("objects should be cached for the database, got %+v", m.databaseObjects["app"])
	}
}

// (#1 regression) Fields show when the cursor is in SELECT and the FROM table
// appears after the cursor.
func TestSelectBeforeFromStillSuggestsFields(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.adapter = &metaStub{fields: []db.MetadataField{{Name: "id", Type: "int"}, {Name: "name", Type: "varchar"}}}
	m.selectedDB = "app"
	m.databaseObjects = map[string][]db.Object{"app": {{Name: "users", Type: db.ObjectTable}}}
	m.openQueryWorkspaceTab()
	tab := m.activeWorkspaceTab()
	tab.QueryBuffer = "select  from users"
	tab.QueryCursor = len("select ") // cursor right after "select "
	m.refreshQuerySuggestions(tab)

	if len(tab.QuerySuggestions) == 0 {
		t.Fatal("expected suggestions after SELECT with a FROM table present")
	}
	first := tab.QuerySuggestions[0]
	if first.Value != "users.id" || first.Match != "id" {
		t.Fatalf("SELECT should lead with the FROM table's fields, got %+v", first)
	}
}

func TestRenderSuggestionRowMutesDetail(t *testing.T) {
	theme := defaultTheme()
	row := renderSuggestionRow(theme, suggest.Suggestion{Value: "users", Detail: "table"}, 20, false)
	plain := stripANSI(row)
	if !strings.Contains(plain, "users") || !strings.Contains(plain, "table") {
		t.Fatalf("row should contain value + detail: %q", plain)
	}
	if lipgloss.Width(row) != 20 {
		t.Fatalf("row width = %d, want 20", lipgloss.Width(row))
	}
	if row == plain {
		t.Fatal("the detail should be colour-styled (muted), but row has no ANSI")
	}
	// Selected row is highlighted uniformly (different styling than unselected).
	sel := renderSuggestionRow(theme, suggest.Suggestion{Value: "users", Detail: "table"}, 20, true)
	if sel == row {
		t.Fatal("selected row should render differently than unselected")
	}
}
