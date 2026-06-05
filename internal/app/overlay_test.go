package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestPlaceOverlaySplicesBoxPreservingWidth(t *testing.T) {
	base := strings.Join([]string{
		"0123456789",
		"0123456789",
		"0123456789",
		"0123456789",
	}, "\n")
	box := "XX\nXX"

	got := placeOverlay(base, box, 1, 3)
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d:\n%s", len(lines), got)
	}
	for i, line := range lines {
		if w := ansi.StringWidth(line); w != 10 {
			t.Fatalf("line %d width = %d, want 10: %q", i, w, line)
		}
	}
	// box at left=3 width=2 over "0123456789" -> "012" + "XX" + "56789"
	if lines[1] != "012XX56789" || lines[2] != "012XX56789" {
		t.Fatalf("spliced rows = %q / %q, want 012XX56789", lines[1], lines[2])
	}
	if lines[0] != "0123456789" || lines[3] != "0123456789" {
		t.Fatalf("untouched rows changed:\n%s", got)
	}
}

func TestPlaceOverlayKeepsANSIBaseWidth(t *testing.T) {
	styled := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")).Render("0123456789")
	base := styled + "\n" + styled
	box := "##"

	got := placeOverlay(base, box, 0, 4)
	for i, line := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(line); w != 10 {
			t.Fatalf("line %d ansi width = %d, want 10: %q", i, w, line)
		}
	}
	if !strings.Contains(stripANSI(got), "##") {
		t.Fatalf("overlay box content missing:\n%s", got)
	}
}

func TestPadBlockTruncatesOverWideLines(t *testing.T) {
	base := "short\n" + strings.Repeat("x", 200)
	got := padBlock(base, 40, 3)
	for i, line := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(line); w != 40 {
			t.Fatalf("line %d width = %d, want exactly 40", i, w)
		}
	}
}

func TestPlaceOverlayCenterStaysWithinWidthDespiteWideBase(t *testing.T) {
	// A base with a line far wider than the centering width must not push the
	// box out of bounds once padBlock normalizes it.
	width := 60
	base := padBlock("normal\n"+strings.Repeat("Z", 300), width, 12)
	box := "╭────╮\n│ hi │\n╰────╯"
	got := placeOverlayCenter(base, box)
	for i, line := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Fatalf("composited line %d width = %d, exceeds %d (would wrap):\n%s", i, w, width, line)
		}
	}
	if !strings.Contains(stripANSI(got), "hi") {
		t.Fatalf("overlay box content missing:\n%s", got)
	}
}

func TestPlaceOverlayCenterCentersBox(t *testing.T) {
	base := padBlock("", 20, 10)
	box := "AAAA\nAAAA"
	got := placeOverlayCenter(base, box)
	lines := strings.Split(got, "\n")
	if len(lines) != 10 {
		t.Fatalf("expected 10 lines, got %d", len(lines))
	}
	// box height 2 centered in 10 -> rows 4 and 5
	if !strings.Contains(lines[4], "AAAA") || !strings.Contains(lines[5], "AAAA") {
		t.Fatalf("box not vertically centered:\n%s", got)
	}
	// box width 4 centered in 20 -> left padding 8
	if leadingSpaces(lines[4]) != 8 {
		t.Fatalf("box not horizontally centered, leading=%d:\n%s", leadingSpaces(lines[4]), got)
	}
}
