package result

import (
	"bytes"
	"strings"
	"testing"
)

func sampleSet() Set {
	return Set{Table: &Table{
		Columns: []Column{{Name: "id"}, {Name: "name"}},
		Rows: []Row{
			{Values: []any{1, "a,b\"c"}},
			{Values: []any{2, nil}},
		},
	}}
}

func TestWriteCSVQuotesAndNulls(t *testing.T) {
	var buf bytes.Buffer
	if err := sampleSet().WriteCSV(&buf); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "id,name\n") {
		t.Fatalf("CSV should start with the header, got:\n%s", out)
	}
	// A value with a comma and quote must be CSV-escaped.
	if !strings.Contains(out, `"a,b""c"`) {
		t.Fatalf("CSV did not escape special chars:\n%s", out)
	}
	if !strings.Contains(out, "2,NULL") {
		t.Fatalf("NULL cell not rendered:\n%s", out)
	}
}

func TestWriteJSONEmitsObjectsWithNulls(t *testing.T) {
	var buf bytes.Buffer
	if err := sampleSet().WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"id"`) || !strings.Contains(out, `"name"`) {
		t.Fatalf("JSON should be objects keyed by column:\n%s", out)
	}
	if !strings.Contains(out, "null") {
		t.Fatalf("nil cell should serialize as JSON null:\n%s", out)
	}
}
