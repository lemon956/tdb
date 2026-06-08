package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tdb/internal/result"
)

func resultTab(m *Model) {
	m.openQueryWorkspaceTab()
	m.activeWorkspaceTab().Result = result.Set{Table: &result.Table{
		Columns: []result.Column{{Name: "id"}, {Name: "name"}},
		Rows:    []result.Row{{Values: []any{1, "ada"}}, {Values: []any{2, "grace"}}},
	}}
}

func TestExportResultWritesCSVFile(t *testing.T) {
	m := newWorkspaceVimModel(t)
	resultTab(m)
	path := filepath.Join(t.TempDir(), "out.csv")

	m.HandleLine(context.Background(), "export csv "+path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("export should create the file: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "id,name") || !strings.Contains(out, "1,ada") || !strings.Contains(out, "2,grace") {
		t.Fatalf("exported CSV missing expected content:\n%s", out)
	}
	if !strings.Contains(m.message, "exported") {
		t.Fatalf("message = %q, want export confirmation", m.message)
	}
}

func TestExportResultJSONAndCopy(t *testing.T) {
	m := newWorkspaceVimModel(t)
	resultTab(m)
	path := filepath.Join(t.TempDir(), "out.json")
	m.HandleLine(context.Background(), "export json "+path)
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"name": "ada"`) {
		t.Fatalf("exported JSON missing objects:\n%s", string(data))
	}

	m.HandleLine(context.Background(), "copy csv")
	if !strings.Contains(m.lastCopiedText, "id,name") {
		t.Fatalf("copy csv should put the CSV on the clipboard, got %q", m.lastCopiedText)
	}
}

func TestExportNoResultMessage(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.HandleLine(context.Background(), "export csv /tmp/none.csv")
	if !strings.Contains(m.message, "no result") {
		t.Fatalf("message = %q, want no-result notice", m.message)
	}
}
