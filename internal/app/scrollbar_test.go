package app

import (
	"strings"
	"testing"
)

func thumbRows(bar []string) (first, count int) {
	first = -1
	for i, cell := range bar {
		if strings.Contains(cell, "┃") {
			if first < 0 {
				first = i
			}
			count++
		}
	}
	return first, count
}

func TestVerticalScrollbarNotScrollable(t *testing.T) {
	if bar := verticalScrollbar(5, 5, 5, 0); bar != nil {
		t.Fatalf("content that fits should have no scrollbar, got %v", bar)
	}
	if bar := verticalScrollbar(5, 3, 5, 0); bar != nil {
		t.Fatalf("content smaller than viewport should have no scrollbar, got %v", bar)
	}
}

func TestVerticalScrollbarProportionAndPosition(t *testing.T) {
	height, total, viewport := 10, 40, 10
	// At top: thumb starts at row 0, length proportional (~height*viewport/total).
	bar := verticalScrollbar(height, total, viewport, 0)
	if len(bar) != height {
		t.Fatalf("bar height = %d, want %d", len(bar), height)
	}
	first, count := thumbRows(bar)
	if first != 0 {
		t.Fatalf("at offset 0 thumb should start at top, got first=%d", first)
	}
	if count < 1 || count >= height {
		t.Fatalf("thumb length = %d, want proportional in [1,%d)", count, height)
	}
	// At bottom: thumb ends at the last row.
	maxOffset := total - viewport
	barBottom := verticalScrollbar(height, total, viewport, maxOffset)
	first, count = thumbRows(barBottom)
	if first+count != height {
		t.Fatalf("at max offset thumb should reach the bottom: first=%d count=%d height=%d", first, count, height)
	}
}

func TestScrollbarGlyphsStrippedFromSelectionCopy(t *testing.T) {
	bar := verticalScrollbar(4, 20, 4, 0)
	if bar == nil {
		t.Fatal("expected a scrollbar for overflowing content")
	}
	for _, cell := range bar {
		for _, r := range stripANSI(cell) {
			if !isBoxDrawing(r) {
				t.Fatalf("scrollbar glyph %q must be box-drawing so copy strips it", r)
			}
		}
	}
	// A selected row ending in the scrollbar copies clean (no bar glyph).
	got := stripSelectionFrame(`  "name": "Ada" ┃`)
	if strings.ContainsAny(got, "┃│") {
		t.Fatalf("copied selection should not contain scrollbar glyphs: %q", got)
	}
}
