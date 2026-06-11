package app

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

const cursorBlinkInterval = 500 * time.Millisecond

type blinkMsg struct{}

func cursorBlinkCmd() tea.Cmd {
	return tea.Tick(cursorBlinkInterval, func(time.Time) tea.Msg {
		return blinkMsg{}
	})
}

func (m *Model) renderCursorCell(cell string) string {
	if cell == "" {
		cell = " "
	}
	if !m.cursorBlinkOn {
		return cell
	}
	return defaultTheme().cursor.Render(cell)
}

// cursorMarker is a zero-width sentinel placed at the active text-input cursor so
// View can locate it in the composed frame and move the REAL terminal cursor
// there (so IME / pinyin candidate windows follow the cursor). Zero width means
// it never shifts layout, and if it gets stripped during composition the cursor
// positioning simply no-ops (fail-safe).
const cursorMarker = "​"

// rowMarker is a zero-width sentinel placed at the start of each clickable modal
// row (e.g. the database picker), so layout can locate the row's rendered line
// and register a hitbox there — same-source with rendering, immune to the
// floating box's centering/scroll geometry. Stripped before display.
const rowMarker = "‌"

// activeInputCursor is the cursor cell used at the one focused text-input surface
// (AI panel, command line, search popups). It carries cursorMarker so View can
// position the real terminal cursor at that column.
func (m *Model) activeInputCursor() string {
	return cursorMarker + m.renderCursorCell(" ")
}

// positionRealCursor finds cursorMarker in the composed frame, strips every
// marker, and appends an ANSI escape that shows + moves the real terminal cursor
// to that row/column (1-based). Best-effort: if no marker is present it returns
// the frame unchanged.
func positionRealCursor(frame string) string {
	idx := strings.Index(frame, cursorMarker)
	if idx < 0 {
		return frame
	}
	before := frame[:idx]
	row := strings.Count(before, "\n")
	lineStart := strings.LastIndexByte(before, '\n') + 1
	col := ansi.StringWidth(before[lineStart:])
	clean := strings.ReplaceAll(frame, cursorMarker, "")
	return clean + fmt.Sprintf("\x1b[?25h\x1b[%d;%dH", row+1, col+1)
}
