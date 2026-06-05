package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
