package app

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/db"
	"tdb/internal/result"
)

// metaStub records Metadata calls so we can assert the suggestion code only
// probes known tables.
type metaStub struct {
	calls  int
	fields []db.MetadataField
	attrs  map[string]string
}

func (a *metaStub) Metadata(context.Context, db.Target) (db.ObjectMetadata, error) {
	a.calls++
	return db.ObjectMetadata{Fields: a.fields, Attributes: a.attrs}, nil
}
func (a *metaStub) Test(context.Context) error                                 { return nil }
func (a *metaStub) ListDatabases(context.Context) ([]string, error)            { return nil, nil }
func (a *metaStub) ListObjects(context.Context, db.Scope) ([]db.Object, error) { return nil, nil }
func (a *metaStub) Preview(context.Context, db.Target, db.Query, db.Page) (result.Set, error) {
	return result.Set{}, nil
}
func (a *metaStub) Insert(context.Context, db.Target, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *metaStub) Update(context.Context, db.Target, db.Key, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *metaStub) Delete(context.Context, db.Target, db.Key) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *metaStub) Execute(context.Context, db.Command) (result.Set, error) {
	return result.Set{}, nil
}
func (a *metaStub) Close() error { return nil }

func TestSuggestionMetadataOnlyForKnownTables(t *testing.T) {
	m := newWorkspaceVimModel(t)
	stub := &metaStub{fields: []db.MetadataField{{Name: "id", Type: "int"}}}
	m.adapter = stub
	m.selectedDB = "app"
	m.databaseObjects = map[string][]db.Object{"app": {{Name: "users", Type: db.ObjectTable}}}

	// An unknown / partial table name must not trigger a metadata probe.
	if fields := m.querySuggestionFields("", "app", "SELECT * FROM d"); fields != nil {
		t.Fatalf("unknown table should yield no fields, got %+v", fields)
	}
	if stub.calls != 0 {
		t.Fatalf("metadata must not be queried for an unknown table, calls=%d", stub.calls)
	}
	if strings.Contains(m.message, "metadata:") {
		t.Fatalf("a failed probe must not set a status message, got %q", m.message)
	}

	// A known table fetches once, then is served from cache.
	if fields := m.querySuggestionFields("", "app", "SELECT * FROM users"); len(fields) != 1 {
		t.Fatalf("known table should yield fields, got %+v", fields)
	}
	_ = m.querySuggestionFields("", "app", "SELECT * FROM users")
	if stub.calls != 1 {
		t.Fatalf("metadata should be fetched once and cached, calls=%d", stub.calls)
	}
}

func TestCtrlCClearsQueryBuffer(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.openQueryWorkspaceTab()
	tab := m.activeWorkspaceTab()
	tab.QueryBuffer = "SELECT 1"
	tab.QueryCursor = len(tab.QueryBuffer)

	if !m.handleQueryTabKey(context.Background(), tab, tea.KeyMsg{Type: tea.KeyCtrlC}) {
		t.Fatal("ctrl+c should be handled")
	}
	if tab.QueryBuffer != "" || tab.QueryCursor != 0 {
		t.Fatalf("ctrl+c should clear the buffer, got %q cursor=%d", tab.QueryBuffer, tab.QueryCursor)
	}
	// The clear is undoable.
	m.undoQueryEdit(tab)
	if tab.QueryBuffer != "SELECT 1" {
		t.Fatalf("u should restore the cleared buffer, got %q", tab.QueryBuffer)
	}

	// Ctrl+C on an empty buffer is swallowed but changes nothing (never quits).
	tab.QueryBuffer = ""
	if !m.handleQueryTabKey(context.Background(), tab, tea.KeyMsg{Type: tea.KeyCtrlC}) {
		t.Fatal("ctrl+c should still be handled (swallowed) when empty")
	}
	if tab.QueryBuffer != "" {
		t.Fatal("ctrl+c on empty buffer should not change it")
	}
}

func TestQueryCursorVisualPosAccountsForWrap(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.focus = FocusMain
	// A single logical line far wider than the wrap width must span several
	// visual rows.
	tab := workspaceTab{QueryBuffer: strings.Repeat("a", 100), WorkspaceFocus: workspaceFocusEditor}
	tab.QueryCursor = len(tab.QueryBuffer)
	vrow, _ := m.queryCursorVisualPos(tab, 40)
	if vrow < 2 {
		t.Fatalf("a 100-wide line at width 40 should wrap to vrow>=2, got %d", vrow)
	}

	// A short first line keeps the cursor on logical line 1 at visual row 1.
	tab2 := workspaceTab{QueryBuffer: "a\nb", WorkspaceFocus: workspaceFocusEditor}
	tab2.QueryCursor = len(tab2.QueryBuffer)
	if vr, _ := m.queryCursorVisualPos(tab2, 40); vr != 1 {
		t.Fatalf("second line cursor should be at visual row 1, got %d", vr)
	}
}
