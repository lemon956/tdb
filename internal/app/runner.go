package app

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func Run(options Options) error {
	if options.HistoryPath == "" {
		options.HistoryPath = defaultHistoryPath(options.ConfigPath)
	}
	if options.ClipboardWriter == nil {
		options.ClipboardWriter = os.Stderr
	}
	if options.ClipboardCopier == nil {
		options.ClipboardCopier = NewSystemClipboardCopier()
	}
	program := tea.NewProgram(NewModel(options), programOptions()...)
	_, err := program.Run()
	return err
}

func programOptions() []tea.ProgramOption {
	return []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	}
}
