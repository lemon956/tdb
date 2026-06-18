package app

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// runAsyncTimeout hands the work a context carrying the requested deadline (so an
// AI CLI call isn't bound by the short DB query timeout), and still wires Esc
// cancel.
func TestRunAsyncTimeoutCarriesDeadline(t *testing.T) {
	m := newWorkspaceVimModel(t)
	var deadline time.Time
	var ok bool
	cmd := m.runAIAsync("AI…", time.Minute, func(ctx context.Context) tea.Msg {
		deadline, ok = ctx.Deadline()
		return nil
	})
	if m.aiCancelOp == nil || !m.loading.active {
		t.Fatal("runAIAsync should arm Esc-cancel and the spinner")
	}
	cmd() // run synchronously
	if !ok {
		t.Fatal("AI work context should carry a deadline")
	}
	if time.Until(deadline) <= 0 {
		t.Fatal("deadline should be in the future")
	}
}

// The AI timeout must be far longer than the DB query timeout, else agentic CLIs
// get SIGKILLed mid-thought ("signal: killed").
func TestAIAskTimeoutLongerThanQuery(t *testing.T) {
	if aiAskTimeout <= defaultQueryTimeout {
		t.Fatalf("aiAskTimeout (%s) must exceed defaultQueryTimeout (%s)", aiAskTimeout, defaultQueryTimeout)
	}
}

// A concurrent DB runAsync must NOT touch the in-flight AI cancel slot —
// otherwise the CLI is SIGKILLed ("signal: killed"). The AI uses an independent
// slot; only Esc (cancelActiveOp) cancels both.
func TestDBAsyncDoesNotCancelAI(t *testing.T) {
	m := newWorkspaceVimModel(t)
	aiCancelled := false
	m.aiCancelOp = func() { aiCancelled = true } // stand in for an in-flight AI call

	// A normal DB op starts while the AI is "running".
	_ = m.runAsync("Loading…", func(context.Context) tea.Msg { return nil })
	if aiCancelled {
		t.Fatal("a DB runAsync must not cancel the AI slot")
	}
	if m.cancelOp == nil {
		t.Fatal("DB runAsync should arm the DB cancel slot")
	}

	// Esc cancels both slots.
	m.cancelActiveOp()
	if !aiCancelled {
		t.Fatal("cancelActiveOp should cancel the AI slot")
	}
}
