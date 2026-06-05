package app

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// mainInnerHeight is the number of content rows available inside the main panel,
// mirroring the geometry in renderLayout.
func (m *Model) mainInnerHeight() int {
	height := m.height
	if height <= 0 {
		height = 30
	}
	commandHeight := 0
	if m.commandVisible() {
		commandHeight = 1
	}
	bodyHeight := max(3, height-2-commandHeight)
	return max(1, bodyHeight-2)
}

// truncateLines clips each line of s to width display cells (ANSI-aware) so a
// boxed overlay stays rectangular instead of wrapping long lines.
func truncateLines(s string, width int) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "")
	}
	return strings.Join(lines, "\n")
}

// queryHistorySearchFloatingBox renders the query-history search popup as a
// compact bordered box, reusing the existing search/list content and scroll
// windowing.
func (m *Model) queryHistorySearchFloatingBox() string {
	// Track the available pane width: shrink in a split pane, grow on a big
	// screen (well past the old 72 cap) to show fuller statements.
	avail := max(20, m.workspaceContentWidth())
	inner := clamp(avail-6, 30, 110)
	theme := defaultTheme()
	header := theme.accent.Render("Query history")
	body := m.visibleModalBody(m.queryHistorySearchContent())
	// Clip to inner minus the horizontal padding (1 each side) so lipgloss does
	// not wrap long history statements onto a second line.
	content := truncateLines(header+"\n"+body, max(1, inner-2))
	return lipgloss.NewStyle().
		Width(inner).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.accentColor).
		Background(theme.historyBgColor).
		Padding(0, 1).
		Render(content)
}

// databasePickerFloatingBox renders the database picker popup.
func (m *Model) databasePickerFloatingBox() string {
	width := clamp(m.workspaceContentWidth()-8, 32, 60)
	inner := max(20, width-4)
	theme := defaultTheme()
	header := theme.accent.Render("Switch database")
	body := m.visibleModalBody(m.databasePickerContent())
	content := truncateLines(header+"\n"+body, max(1, inner-2))
	return lipgloss.NewStyle().
		Width(inner).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.accentColor).
		Background(theme.historyBgColor).
		Padding(0, 1).
		Render(content)
}

// querySuggestionOverlay floats the autocomplete popup anchored just below the
// cursor (flipping above when there is no room), composited over the live query
// view so it never pushes the result rows down. Returns ok=false when there is
// no query view or the box does not fit.
func (m *Model) querySuggestionOverlay() (string, bool) {
	tab := m.activeWorkspaceTab()
	if tab == nil || tab.Kind != workspaceTabQuery || !tab.QuerySuggestionsVisible || len(tab.QuerySuggestions) == 0 {
		return "", false
	}
	innerW := max(20, m.workspaceContentWidth())
	innerH := m.mainInnerHeight()

	body, _ := m.querySuggestionBoxBody(*tab, max(8, innerW-2))
	theme := defaultTheme()
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.accentColor).
		Background(theme.historyBgColor).
		Render(body)
	boxW := lipgloss.Width(box)
	boxH := lipgloss.Height(box)
	if boxW > innerW || boxH > innerH {
		return "", false
	}

	// Column anchor: line prefix width + display column of the cursor.
	runLines := queryRunButtonLines(tab.QueryBuffer)
	row, col := queryCursorPosition(tab.QueryBuffer, tab.QueryCursor)
	var prefixW int
	if row == 0 {
		prefixW = lipgloss.Width(queryEditorLinePrefix(m.workspaceCursor(workspaceFocusEditor, *tab), runLines[0]))
	} else {
		prefixW = lipgloss.Width(queryEditorLinePrefix("  ", runLines[row]))
	}
	left := clamp(prefixW+col, 0, max(0, innerW-boxW))

	// Row anchor inside workspaceContent: row 0 = tab bar, row 1 = status line,
	// row 2 = buffer line 0, so the cursor's buffer line is at row+2. Float on the
	// next line, flipping above the cursor when it would overflow the panel.
	cursorLine := row + 2
	top := cursorLine + 1
	if top+boxH > innerH {
		top = max(0, cursorLine-boxH)
	}

	base := padBlock(m.workspaceContent(), innerW, innerH)
	return placeOverlay(base, box, top, left), true
}

// databasePickerOverlay floats the picker over the live workspace view.
func (m *Model) databasePickerOverlay() (string, bool) {
	if len(m.workspaceTabs) == 0 {
		return "", false
	}
	innerW := max(20, m.workspaceContentWidth())
	innerH := m.mainInnerHeight()
	box := m.databasePickerFloatingBox()
	if lipgloss.Width(box) > innerW || lipgloss.Height(box) > innerH {
		return "", false
	}
	base := padBlock(m.workspaceContent(), innerW, innerH)
	return placeOverlayCenter(base, box), true
}

// queryHistorySearchOverlay floats the search popup over the live query view.
// It returns ok=false when there is no query view behind it or the terminal is
// too small, so the caller can fall back to a full-panel modal.
func (m *Model) queryHistorySearchOverlay() (string, bool) {
	if len(m.workspaceTabs) == 0 {
		return "", false
	}
	innerW := max(20, m.workspaceContentWidth())
	innerH := m.mainInnerHeight()
	box := m.queryHistorySearchFloatingBox()
	if lipgloss.Width(box) > innerW || lipgloss.Height(box) > innerH {
		return "", false
	}
	base := padBlock(m.workspaceContent(), innerW, innerH)
	return placeOverlayCenter(base, box), true
}

// padBlock normalizes every line of s to exactly the given display width
// (truncating over-wide lines and padding short ones) and ensures the block has
// at least height lines. A uniform-width base lets a floating box center
// predictably and keeps composited rows from exceeding the panel width (which
// would otherwise wrap and corrupt the overlay).
func padBlock(s string, width, height int) string {
	lines := strings.Split(s, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, line := range lines {
		w := ansi.StringWidth(line)
		if w > width {
			line = ansi.Truncate(line, width, "")
			w = ansi.StringWidth(line)
		}
		if pad := width - w; pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// placeOverlay composites box onto base starting at the given top row and left
// column (both in display cells). It is ANSI-aware: existing styled runs in base
// are preserved on either side of the box. Lines below base are appended.
func placeOverlay(base, box string, top, left int) string {
	if top < 0 {
		top = 0
	}
	if left < 0 {
		left = 0
	}
	baseLines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")

	boxWidth := 0
	for _, line := range boxLines {
		if w := ansi.StringWidth(line); w > boxWidth {
			boxWidth = w
		}
	}

	for i, boxLine := range boxLines {
		row := top + i
		for row >= len(baseLines) {
			baseLines = append(baseLines, "")
		}
		baseLine := baseLines[row]

		leftPart := ansi.Truncate(baseLine, left, "")
		if pad := left - ansi.StringWidth(leftPart); pad > 0 {
			leftPart += strings.Repeat(" ", pad)
		}
		if pad := boxWidth - ansi.StringWidth(boxLine); pad > 0 {
			boxLine += strings.Repeat(" ", pad)
		}
		rightPart := ansi.TruncateLeft(baseLine, left+boxWidth, "")

		baseLines[row] = leftPart + boxLine + rightPart
	}
	return strings.Join(baseLines, "\n")
}

// placeOverlayCenter composites box centered over base.
func placeOverlayCenter(base, box string) string {
	baseLines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")

	baseWidth := 0
	for _, line := range baseLines {
		if w := ansi.StringWidth(line); w > baseWidth {
			baseWidth = w
		}
	}
	boxWidth := 0
	for _, line := range boxLines {
		if w := ansi.StringWidth(line); w > boxWidth {
			boxWidth = w
		}
	}

	left := max(0, (baseWidth-boxWidth)/2)
	top := max(0, (len(baseLines)-len(boxLines))/2)
	return placeOverlay(base, box, top, left)
}
