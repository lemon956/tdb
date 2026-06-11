package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// A long backend error must wrap inside the danger border instead of overflowing
// and fragmenting it: every rendered line stays within the workspace width.
func TestQueryErrorWrapsWithinWidth(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.width = 100
	m.height = 40
	m.openQueryWorkspaceTab()
	tab := m.activeWorkspaceTab()
	tab.Error = "Error 1105 (HY000): errCode = 2, detailMessage = (10.0.0.1)[INTERNAL_ERROR]" +
		"Unsupported exec type in pipelineX: ODBC_SCAN_NODE. Please check the data source configuration " +
		"and retry the statement after confirming the external table is reachable."

	out := m.workspaceQueryContent(*tab)
	limit := m.workspaceContentWidth()
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > limit {
			t.Fatalf("line exceeds workspace width %d: %d %q", limit, w, line)
		}
	}
	if !strings.Contains(stripANSI(out), "ODBC_SCAN_NODE") {
		t.Fatal("error text should still be present")
	}
}

// The full-panel error modal must likewise enclose its (long) message: bounded
// width keeps the rounded border intact.
func TestErrorBoxContentBounded(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.width = 100
	m.height = 40
	m.errBox = &errorBox{Title: "Query failed", Message: strings.Repeat("very long error detail ", 30)}

	out := m.errorBoxContent()
	limit := m.workspaceContentWidth()
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > limit {
			t.Fatalf("error box line exceeds %d: %d %q", limit, w, line)
		}
	}
	if !strings.Contains(stripANSI(out), "[ Close ]") {
		t.Fatal("close button should be present")
	}
}
