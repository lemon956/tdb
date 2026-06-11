package app

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// selSeg is one selected row's half-open display-column range [a, b).
type selSeg struct {
	row, a, b int
}

// selectionSegments yields the per-row column ranges of the current drag
// selection, normalized so the anchor/end order does not matter. Columns are
// clamped to the panel the drag started in (selMinX..selMaxX), so a selection in
// one panel never includes the other panel's columns.
func (m *Model) selectionSegments() []selSeg {
	ax, ay := m.selAnchorX, m.selAnchorY
	bx, by := m.selX, m.selY
	if ay > by || (ay == by && ax > bx) {
		ax, ay, bx, by = bx, by, ax, ay
	}
	segs := make([]selSeg, 0, by-ay+1)
	for r := ay; r <= by; r++ {
		a, b := m.selMinX, m.selMaxX
		switch {
		case ay == by:
			a, b = min(ax, bx), max(ax, bx)+1
		case r == ay:
			a, b = ax, m.selMaxX
		case r == by:
			a, b = m.selMinX, bx+1
		}
		a = clamp(a, m.selMinX, m.selMaxX)
		b = clamp(b, m.selMinX, m.selMaxX)
		if b < a {
			b = a
		}
		segs = append(segs, selSeg{row: r, a: a, b: b})
	}
	return segs
}

// sliceColumns returns the display columns [a, b) of an ANSI-styled line. Shared
// by selection extraction and highlighting so both agree on the boundaries.
func sliceColumns(line string, a, b int) string {
	if b <= a {
		return ""
	}
	return ansi.TruncateLeft(ansi.Truncate(line, b, ""), a, "")
}

// extractSelectionText pulls the selected text out of the last rendered frame
// (clean, pre-highlight), one panel-clipped row per line, trailing spaces removed.
func (m *Model) extractSelectionText() string {
	lines := m.lastFrameLines
	var out []string
	for _, s := range m.selectionSegments() {
		if s.row < 0 || s.row >= len(lines) {
			continue
		}
		out = append(out, strings.TrimRight(ansi.Strip(sliceColumns(lines[s.row], s.a, s.b)), " "))
	}
	return strings.Join(out, "\n")
}

// applySelectionHighlight reverses-video the selected column range on each
// selected row of frame, preserving the surrounding styled text (mirrors the
// prefix/suffix splicing in placeOverlay).
func (m *Model) applySelectionHighlight(frame string) string {
	lines := strings.Split(frame, "\n")
	theme := defaultTheme()
	for _, s := range m.selectionSegments() {
		if s.row < 0 || s.row >= len(lines) || s.b <= s.a {
			continue
		}
		line := lines[s.row]
		seg := ansi.Strip(sliceColumns(line, s.a, s.b))
		if seg == "" {
			continue
		}
		prefix := ansi.Truncate(line, s.a, "")
		suffix := ansi.TruncateLeft(line, s.b, "")
		lines[s.row] = prefix + theme.selected.Render(seg) + suffix
	}
	return strings.Join(lines, "\n")
}
