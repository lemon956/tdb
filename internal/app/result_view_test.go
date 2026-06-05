package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"tdb/internal/result"
)

func TestResultViewRendersTableWindowWithOffsets(t *testing.T) {
	table := result.Table{
		Columns: []result.Column{{Name: "id"}, {Name: "name"}, {Name: "city"}},
		Rows: []result.Row{
			{Values: []any{1, "Ada", "London"}},
			{Values: []any{2, "Grace", "New York"}},
			{Values: []any{3, "Linus", "Helsinki"}},
		},
	}
	view := ResultView{RowOffset: 1, ColumnOffset: 1, Height: 1, Width: 2}

	got := view.Render(result.Set{Table: &table})
	if strings.Contains(got, "Ada") || strings.Contains(got, "id") {
		t.Fatalf("render included scrolled-out content:\n%s", got)
	}
	if !strings.Contains(got, "Grace") || !strings.Contains(got, "New York") {
		t.Fatalf("render missing visible window:\n%s", got)
	}
	if !strings.Contains(got, "row 2-2 of 3") {
		t.Fatalf("render missing row range:\n%s", got)
	}
}

func TestResultViewTableFitsWidthWithoutWrapping(t *testing.T) {
	table := result.Table{
		Columns: []result.Column{{Name: "url"}, {Name: "ts"}, {Name: "country"}, {Name: "tz"}},
		Rows: []result.Row{
			{Values: []any{"150309/thumbsize_100_100_7545_very_long_url_value", "2023-09-14 18:24:03 +0000 UTC", "CN", 8}},
			{Values: []any{"151012/thumbsize_100_100_9873_another_long_value", "2021-09-08 17:31:37 +0000 UTC", "TH", 7}},
		},
	}
	const maxWidth = 40
	view := ResultView{Height: 10, MaxWidth: maxWidth}
	got := view.Render(result.Set{Table: &table})

	rowCount := 0
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if lipgloss.Width(line) > maxWidth {
			t.Fatalf("line exceeds MaxWidth %d (would wrap): %q", maxWidth, line)
		}
		if strings.Contains(line, "150309") || strings.Contains(line, "151012") {
			rowCount++
		}
	}
	// Each data row must stay on a single line (not split across two).
	if rowCount != 2 {
		t.Fatalf("expected 2 single-line data rows, found %d:\n%s", rowCount, got)
	}
	if !strings.Contains(got, "of 4") {
		t.Fatalf("expected column summary 'of 4':\n%s", got)
	}
}

func TestResultViewHighlightsSelectedRow(t *testing.T) {
	table := result.Table{
		Columns: []result.Column{{Name: "id"}, {Name: "name"}},
		Rows: []result.Row{
			{Values: []any{1, "Ada"}},
			{Values: []any{2, "Grace"}},
		},
	}
	view := ResultView{Height: 10, Selectable: true, SelectedRow: 1}
	got := view.Render(result.Set{Table: &table})
	for _, line := range strings.Split(got, "\n") {
		plain := stripANSI(line)
		if strings.Contains(plain, "Grace") && line == plain {
			t.Fatalf("selected row should be styled (contain ANSI):\n%q", line)
		}
		if strings.Contains(plain, "Ada") && line != plain {
			t.Fatalf("non-selected data row should be plain:\n%q", line)
		}
	}
}

func TestResultViewRendersDocumentWindow(t *testing.T) {
	view := ResultView{RowOffset: 1, Height: 1}
	set := result.Set{Documents: []result.Document{
		{ID: "1", Data: map[string]any{"name": "Ada"}},
		{ID: "2", Data: map[string]any{"name": "Grace"}},
	}}

	got := view.Render(set)
	if strings.Contains(got, "Ada") {
		t.Fatalf("render included scrolled-out document:\n%s", got)
	}
	if !strings.Contains(got, "Grace") || !strings.Contains(got, "document 2-2 of 2") {
		t.Fatalf("render missing visible document:\n%s", got)
	}
}
