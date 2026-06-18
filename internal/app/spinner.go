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
	return defaultTheme().spinner.Render(frame) + " " + m.loading.label + defaultTheme().muted.Render(" (Esc cancel)")
}

// runAsync starts a background operation: it marks the spinner active and
// returns a command that runs work() off the main loop and (alongside the
// spinner tick) feeds the resulting message back into Update. work performs
// only I/O and must not touch the model; the returned message is applied on the
// main goroutine.
func (m *Model) runAsync(label string, work func(context.Context) tea.Msg) tea.Cmd {
	ctx, cancel := m.dbContext(context.Background())
	return m.runAsyncContext(label, ctx, cancel)(work)
}

// runAIAsync runs a long, non-DB job (an agentic AI CLI that legitimately takes
// minutes) under its OWN cancel slot (m.aiCancelOp), independent of the shared
// m.cancelOp. This is critical: otherwise any concurrent DB operation's runAsync
// would cancel the AI context and SIGKILL the CLI ("signal: killed"). timeout
// <= 0 means no deadline (Esc-cancel only).
func (m *Model) runAIAsync(label string, timeout time.Duration, work func(context.Context) tea.Msg) tea.Cmd {
	if m.aiCancelOp != nil {
		m.aiCancelOp()
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout <= 0 {
		ctx, cancel = context.WithCancel(context.Background())
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	}
	m.aiCancelOp = cancel
	m.loading = loadingState{active: true, label: label}
	return func() tea.Msg {
		defer cancel()
		return runWork(ctx, work)
	}
}

// runWork executes a background work function under a recover boundary, so a
// panic in off-thread I/O becomes an error box (applied on the main loop)
// instead of crashing the Cmd goroutine and tearing down the program.
func runWork(ctx context.Context, work func(context.Context) tea.Msg) (msg tea.Msg) {
	defer func() {
		if r := recover(); r != nil {
			rec := r
			msg = asyncResultMsg{apply: func(m *Model) {
				m.logPanic("async", rec)
				m.showErrorBox("后台任务崩溃", recoverToError(rec))
			}}
		}
	}()
	return work(ctx)
}

// runAsyncContext wires the spinner + Esc-cancel around a caller-built context,
// returning a builder that takes the work function.
func (m *Model) runAsyncContext(label string, ctx context.Context, cancel context.CancelFunc) func(func(context.Context) tea.Msg) tea.Cmd {
	// Build the cancellable context on the main loop and stash its cancel func so a
	// keypress can abort the in-flight operation (Esc).
	if m.cancelOp != nil {
		m.cancelOp()
	}
	m.cancelOp = cancel
	m.loading = loadingState{active: true, label: label}
	// The spinner tick already runs continuously (started in Init), so we only
	// return the I/O job. Bubble Tea runs it on a goroutine; tests can invoke it
	// synchronously to apply the result.
	return func(work func(context.Context) tea.Msg) tea.Cmd {
		return func() tea.Msg {
			defer cancel()
			return runWork(ctx, work)
		}
	}
}

// cancelActiveOp aborts the in-flight async operation(s) — both the shared DB
// slot and the independent AI slot — so Esc cancels whichever is running.
func (m *Model) cancelActiveOp() bool {
	if m.cancelOp == nil && m.aiCancelOp == nil {
		return false
	}
	if m.cancelOp != nil {
		m.cancelOp()
		m.cancelOp = nil
	}
	if m.aiCancelOp != nil {
		m.aiCancelOp()
		m.aiCancelOp = nil
	}
	m.loading = loadingState{}
	m.message = "cancelled"
	return true
}

// finishLoading clears the spinner once an async result has been applied.
func (m *Model) finishLoading() {
	m.loading = loadingState{}
	m.cancelOp = nil
}
