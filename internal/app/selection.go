package app

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// selectionState owns the mouse drag-selection (left-drag to select, auto-copy
// on release). It is embedded in Model so existing m.selecting / m.selX … access
// keeps working via field promotion. Bounds are the column range of the panel the
// drag started in, so a selection never bleeds into the other panel's columns.
type selectionState struct {
	selecting       bool
	selActive       bool // a finished selection is still highlighted (until next press)
	selAnchorX      int
	selAnchorY      int
	selX            int
	selY            int
	selMinX         int
	selMaxX         int
	selMinY         int
	selMaxY         int
	overlaySel      *selRect // active overlay's inner content rect (clamps selection)
	overlayBoxWidth int      // content width recorded by markOverlayBox
	mouseDownX      int
	mouseDownY      int
	mouseMoved      bool
	lastFrameLines  []string // last rendered frame (clean), for selection extraction
}

// selSeg is one selected row's half-open display-column range [a, b).
type selSeg struct {
	row, a, b int
}

// selRect is an overlay box's inner content rectangle in the composed frame.
type selRect struct {
	x, y, w, h int
}

// detectOverlaySelRect recovers the active overlay box's inner rectangle from the
// composed frame using the boxMarker pair that modalBox injected, so the mouse
// selection can be clamped to the box interior (never the border). It strips the
// markers from lastFrameLines, and clears m.overlaySel when no box is present.
func (m *Model) detectOverlaySelRect() {
	m.overlaySel = nil
	minRow, maxRow, col := -1, -1, 0
	for r, line := range m.lastFrameLines {
		idx := strings.Index(line, boxMarker)
		if idx < 0 {
			continue
		}
		if minRow < 0 {
			minRow = r
			col = ansi.StringWidth(ansi.Strip(line[:idx])) // marker's display column = inner-left
		}
		maxRow = r
		m.lastFrameLines[r] = strings.ReplaceAll(line, boxMarker, "")
	}
	if minRow >= 0 && m.overlayBoxWidth > 0 {
		m.overlaySel = &selRect{x: col, y: minRow, w: m.overlayBoxWidth, h: maxRow - minRow + 1}
	}
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
	// Clamp the row span to the active panel/box so a selection never spills past
	// the current level's content rows (e.g. above/below a modal's interior).
	if m.selMaxY > m.selMinY {
		ay = clamp(ay, m.selMinY, m.selMaxY-1)
		by = clamp(by, m.selMinY, m.selMaxY-1)
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
// (clean, pre-highlight), one panel-clipped row per line, trailing spaces removed
// and any box-drawing frame (modal/panel border) trimmed from each edge.
func (m *Model) extractSelectionText() string {
	lines := m.lastFrameLines
	var out []string
	for _, s := range m.selectionSegments() {
		if s.row < 0 || s.row >= len(lines) {
			continue
		}
		seg := stripSelectionFrame(ansi.Strip(sliceColumns(lines[s.row], s.a, s.b)))
		out = append(out, strings.TrimRight(seg, " "))
	}
	return strings.Join(out, "\n")
}

// isBoxDrawing reports whether r is a Unicode box-drawing glyph (U+2500–U+257F:
// │ ─ ╭ ╮ ╰ ╯ ┃ …) — the runes lipgloss uses for panel/modal borders.
func isBoxDrawing(r rune) bool {
	return r >= 0x2500 && r <= 0x257F
}

// stripSelectionFrame removes a panel/modal border from the edges of a selected
// line so a drag across a bordered overlay copies the content, not the frame. It
// trims, on each edge, an optional run of spaces (centering), a run of box-drawing
// glyphs, and one adjacent padding space — but ONLY when a border is actually
// present, so plain content indentation and interior separators (e.g. a result
// grid's "│" columns) are preserved.
func stripSelectionFrame(s string) string {
	rs := []rune(s)
	left := leadingFrame(rs)
	right := trailingFrame(rs[left:])
	end := len(rs) - right
	if end < left {
		end = left
	}
	return string(rs[left:end])
}

// leadingFrame counts the leading "spaces* border+ space?" frame runes, or 0 when
// no border follows the optional leading spaces (so indentation is kept).
func leadingFrame(rs []rune) int {
	i := 0
	for i < len(rs) && rs[i] == ' ' {
		i++
	}
	if i >= len(rs) || !isBoxDrawing(rs[i]) {
		return 0
	}
	for i < len(rs) && isBoxDrawing(rs[i]) {
		i++
	}
	if i < len(rs) && rs[i] == ' ' {
		i++
	}
	return i
}

// trailingFrame mirrors leadingFrame on the right edge, returning the count of
// trailing frame runes to drop.
func trailingFrame(rs []rune) int {
	j := len(rs)
	for j > 0 && rs[j-1] == ' ' {
		j--
	}
	if j == 0 || !isBoxDrawing(rs[j-1]) {
		return 0
	}
	for j > 0 && isBoxDrawing(rs[j-1]) {
		j--
	}
	if j > 0 && rs[j-1] == ' ' {
		j--
	}
	return len(rs) - j
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
