package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"tdb/internal/result"
)

// Highlighted JSON must survive the data-grid's width-based wrapping and
// horizontal slicing without width drift or broken escapes, including CJK.
func TestHighlightedJSONSliceCJKSafe(t *testing.T) {
	set := result.Set{Documents: []result.Document{
		{ID: "1", Data: map[string]any{"城市": "北京市朝阳区", "n": 1}},
	}}
	const width = 12
	lines := wrappedDataLines(dataResultLines(set), width)
	if len(lines) == 0 {
		t.Fatal("expected wrapped lines")
	}
	for _, ln := range lines {
		if lipgloss.Width(ln) > width {
			t.Fatalf("wrapped line exceeds width %d: got %d %q", width, lipgloss.Width(ln), stripANSI(ln))
		}
		// A horizontal slice never exceeds the requested column window, and its
		// stripped form contains no escape bytes (no broken ANSI).
		seg := sliceColumns(ln, 0, 6)
		if lipgloss.Width(seg) > 6 {
			t.Fatalf("slice exceeds 6 cells: got %d %q", lipgloss.Width(seg), stripANSI(seg))
		}
		if strings.Contains(stripANSI(ln), "\x1b") {
			t.Fatalf("stripped line still contains escape bytes: %q", ln)
		}
	}
}
