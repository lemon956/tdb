package result

import "testing"

func TestTableCellStringFormatsNilBytesAndScalars(t *testing.T) {
	table := Table{
		Columns: []Column{{Name: "id"}, {Name: "payload"}, {Name: "missing"}},
		Rows: []Row{
			{Values: []any{int64(7), []byte("hello"), nil}},
		},
	}

	tests := []struct {
		column int
		want   string
	}{
		{column: 0, want: "7"},
		{column: 1, want: "hello"},
		{column: 2, want: "NULL"},
	}
	for _, test := range tests {
		if got := table.CellString(0, test.column); got != test.want {
			t.Fatalf("CellString(0, %d) = %q, want %q", test.column, got, test.want)
		}
	}
}

func TestNewMutationResultCapturesAffectedRows(t *testing.T) {
	got := NewMutationResult(3)
	if got.AffectedRows != 3 {
		t.Fatalf("AffectedRows = %d, want 3", got.AffectedRows)
	}
}
