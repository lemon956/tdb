package app

import (
	"context"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/config"
)

func connModel(t *testing.T, n int) *Model {
	t.Helper()
	m := newWorkspaceVimModel(t)
	m.page = PageConnections
	m.width = 100
	m.height = 30
	m.vault.Profiles = nil
	for i := 0; i < n; i++ {
		m.vault.Profiles = append(m.vault.Profiles, config.Profile{
			ID: "db" + strconv.Itoa(i), Driver: config.DriverMySQL, Host: "h", Port: 3306, Database: "app",
		})
	}
	return m
}

func TestConnectionsTableMapsProfiles(t *testing.T) {
	m := connModel(t, 3)
	tbl := m.connectionsTable()
	if len(tbl.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(tbl.Rows))
	}
	names := make([]string, len(tbl.Columns))
	for i, c := range tbl.Columns {
		names[i] = c.Name
	}
	for _, want := range []string{"ID", "Driver", "Host", "Port", "Database", "Access"} {
		if !contains(names, want) {
			t.Fatalf("columns %v missing %q", names, want)
		}
	}
	if tbl.CellString(0, 0) != "db0" {
		t.Fatalf("row0 ID = %q", tbl.CellString(0, 0))
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func TestConnectionsKeyCursorMirrorsResult(t *testing.T) {
	m := connModel(t, 5)
	ctx := context.Background()
	// j/k move the selected row.
	m.handleConnectionsKey(ctx, key("j"))
	if m.connectionIndex != 1 {
		t.Fatalf("j → index %d, want 1", m.connectionIndex)
	}
	// G/gg jump to last/first.
	m.handleConnectionsKey(ctx, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if m.connectionIndex != 4 {
		t.Fatalf("G → index %d, want 4", m.connectionIndex)
	}
	m.handleConnectionsKey(ctx, key("g"))
	m.handleConnectionsKey(ctx, key("g"))
	if m.connectionIndex != 0 {
		t.Fatalf("gg → index %d, want 0", m.connectionIndex)
	}
	// h/l scroll columns.
	m.handleConnectionsKey(ctx, key("l"))
	if m.connectionsView.ColumnOffset != 1 {
		t.Fatalf("l → ColumnOffset %d, want 1", m.connectionsView.ColumnOffset)
	}
	m.handleConnectionsKey(ctx, key("h"))
	if m.connectionsView.ColumnOffset != 0 {
		t.Fatalf("h → ColumnOffset %d, want 0", m.connectionsView.ColumnOffset)
	}
}

func TestConnectionsDetailToggle(t *testing.T) {
	m := connModel(t, 2)
	ctx := context.Background()
	m.handleConnectionsKey(ctx, tea.KeyMsg{Type: tea.KeyCtrlJ})
	if !m.connectionsDetail {
		t.Fatal("ctrl+j should open the detail popup")
	}
	m.handleConnectionsKey(ctx, tea.KeyMsg{Type: tea.KeyEsc})
	if m.connectionsDetail {
		t.Fatal("esc should close the detail popup")
	}
}

func TestConnectionsVisualCopy(t *testing.T) {
	m := connModel(t, 3)
	ctx := context.Background()
	m.handleConnectionsKey(ctx, key("v"))
	m.handleConnectionsKey(ctx, key("j"))
	m.handleConnectionsKey(ctx, key("y"))
	if !strings.Contains(m.lastCopiedText, "db0") || !strings.Contains(m.lastCopiedText, "db1") {
		t.Fatalf("y should copy selected rows as TSV, got %q", m.lastCopiedText)
	}
	if !strings.Contains(m.lastCopiedText, "\t") || !strings.Contains(m.lastCopiedText, "\n") {
		t.Fatalf("copied text should be multi-row TSV, got %q", m.lastCopiedText)
	}
}

func TestConnectionDetailTableDriverAware(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.page = PageConnections
	m.vault.Profiles = []config.Profile{{
		ID: "test-mongo", Driver: config.DriverMongo,
		URIParams: "mongodb://u:secret@h.example:27017/db?authSource=admin",
		Database:  "pay_exposure", AuthDB: "admin", ReadOnly: true,
	}}
	m.connectionIndex = 0
	tbl := m.connectionDetailTable()
	var dump strings.Builder
	for i := range tbl.Rows {
		dump.WriteString(tbl.CellString(i, 0) + "=" + tbl.CellString(i, 1) + "\n")
	}
	s := dump.String()
	for _, want := range []string{"pay_exposure", "h.example:27017", "admin", "***"} {
		if !strings.Contains(s, want) {
			t.Fatalf("detail table missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, ":0") || strings.Contains(s, "secret") {
		t.Fatalf("detail table should not expose :0 host or password:\n%s", s)
	}
}

func TestConnectionsDetailVimCursor(t *testing.T) {
	m := connModel(t, 1)
	ctx := context.Background()
	// Open detail.
	m.handleConnectionsKey(ctx, tea.KeyMsg{Type: tea.KeyCtrlJ})
	if !m.connectionsDetail || m.connectionsDetailIndex != 0 {
		t.Fatalf("ctrl+j should open detail at row 0")
	}
	m.handleConnectionsKey(ctx, key("j"))
	if m.connectionsDetailIndex != 1 {
		t.Fatalf("j → detail index %d, want 1", m.connectionsDetailIndex)
	}
	m.handleConnectionsKey(ctx, key("l"))
	if m.connectionsDetailView.ColumnOffset != 1 {
		t.Fatalf("l → detail ColumnOffset %d, want 1", m.connectionsDetailView.ColumnOffset)
	}
	m.handleConnectionsKey(ctx, key("g"))
	m.handleConnectionsKey(ctx, key("g"))
	if m.connectionsDetailIndex != 0 {
		t.Fatalf("gg → detail index %d, want 0", m.connectionsDetailIndex)
	}
	// v + y copies a Field/Value row.
	m.handleConnectionsKey(ctx, key("v"))
	m.handleConnectionsKey(ctx, key("y"))
	if !strings.Contains(m.lastCopiedText, "\t") {
		t.Fatalf("detail y should copy Field\\tValue, got %q", m.lastCopiedText)
	}
}

// Colon passes through from the detail so command mode (and :q) can open.
func TestDetailColonOpensCommand(t *testing.T) {
	m := connModel(t, 1)
	m.connectionsDetail = true
	// ':' must NOT be consumed by the connections handler.
	if m.handleConnectionsKey(context.Background(), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}}) {
		t.Fatal("':' should pass through from the detail so command mode opens")
	}
	// Esc closes the detail.
	if !m.handleConnectionsKey(context.Background(), tea.KeyMsg{Type: tea.KeyEsc}) || m.connectionsDetail {
		t.Fatal("esc should close the detail popup")
	}
}

// :q quits even when the detail popup is open (reaches HandleLine via command mode).
func TestQuitCommandWorksFromDetail(t *testing.T) {
	m := connModel(t, 1)
	m.connectionsDetail = true
	m.focusCommand()
	m.input.SetValue(":q")
	m.HandleLine(context.Background(), ":q")
	if m.nextCmd == nil {
		t.Fatal(":q should set the quit command")
	}
}
