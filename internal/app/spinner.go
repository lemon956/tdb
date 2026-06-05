package app

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const spinnerInterval = 90 * time.Millisecond

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type spinnerTickMsg struct{}

// asyncResultMsg carries the outcome of a background operation back to the main
// loop. apply runs on the main goroutine and mutates the model with the result
// computed off-thread, avoiding data races.
type asyncResultMsg struct {
	apply func(*Model)
}

// takeCmd returns and clears any command queued by a handler during this update.
func (m *Model) takeCmd() tea.Cmd {
	cmd := m.nextCmd
	m.nextCmd = nil
	return cmd
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

// loadingState tracks an in-flight async operation so a spinner can animate
// while the work runs on a background goroutine.
type loadingState struct {
	active bool
	label  string
	frame  int
}

// spinnerView renders the current spinner frame, or empty when idle.
func (m *Model) spinnerView() string {
	if !m.loading.active {
		return ""
	}
	frame := spinnerFrames[m.loading.frame%len(spinnerFrames)]
	return defaultTheme().spinner.Render(frame) + " " + m.loading.label
}

// runAsync starts a background operation: it marks the spinner active and
// returns a command that runs work() off the main loop and (alongside the
// spinner tick) feeds the resulting message back into Update. work performs
// only I/O and must not touch the model; the returned message is applied on the
// main goroutine.
func (m *Model) runAsync(label string, work func(context.Context) tea.Msg) tea.Cmd {
	m.loading = loadingState{active: true, label: label}
	// The spinner tick already runs continuously (started in Init), so we only
	// return the I/O job. Bubble Tea runs it on a goroutine; tests can invoke it
	// synchronously to apply the result.
	return func() tea.Msg {
		ctx, cancel := dbContext(context.Background())
		defer cancel()
		return work(ctx)
	}
}

// finishLoading clears the spinner once an async result has been applied.
func (m *Model) finishLoading() {
	m.loading = loadingState{}
}
