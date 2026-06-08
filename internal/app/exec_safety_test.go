package app

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/result"
)

func TestDBContextHonorsConfigurableTimeout(t *testing.T) {
	m := &Model{queryTimeout: 5 * time.Second}
	ctx, cancel := m.dbContext(context.Background())
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("a positive timeout should set a deadline")
	}

	m.queryTimeout = 0
	ctx2, cancel2 := m.dbContext(context.Background())
	defer cancel2()
	if _, ok := ctx2.Deadline(); ok {
		t.Fatal("timeout 0 should have no deadline (cancel-only)")
	}
}

func TestRunAsyncIsCancellable(t *testing.T) {
	m := newWorkspaceVimModel(t)
	_ = m.runAsync("Running…", func(ctx context.Context) tea.Msg { return nil })
	// runAsync must stash a cancel func and mark loading.
	if m.cancelOp == nil || !m.loading.active {
		t.Fatalf("runAsync should set cancelOp and loading; cancelOp=%v active=%v", m.cancelOp != nil, m.loading.active)
	}

	if !m.cancelActiveOp() {
		t.Fatal("cancelActiveOp should report it cancelled the op")
	}
	if m.loading.active || m.cancelOp != nil {
		t.Fatal("after cancel, loading should be cleared")
	}
	if m.message != "cancelled" {
		t.Fatalf("message = %q, want cancelled", m.message)
	}
}

func TestTimeoutCommandSetsQueryTimeout(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.HandleLine(context.Background(), "timeout 5")
	if m.queryTimeout != 5*time.Second {
		t.Fatalf("queryTimeout = %s, want 5s", m.queryTimeout)
	}
	m.HandleLine(context.Background(), "timeout 0")
	if m.queryTimeout != 0 {
		t.Fatalf("queryTimeout = %s, want 0 (disabled)", m.queryTimeout)
	}
}

func TestResultBarShowsTruncationWarning(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.openQueryWorkspaceTab()
	tab := m.activeWorkspaceTab()
	tab.Result = result.Set{
		Table:     &result.Table{Columns: []result.Column{{Name: "id"}}, Rows: []result.Row{{Values: []any{1}}}},
		Truncated: true,
	}
	bar := stripANSI(m.queryResultBar(*tab))
	if !strings.Contains(bar, "capped") {
		t.Fatalf("result bar should warn about a capped result:\n%s", bar)
	}
}
