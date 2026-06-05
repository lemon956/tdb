package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"tdb/internal/db"
	"tdb/internal/history"
)

type modalKind string

const (
	modalForm               modalKind = "form"
	modalHelp               modalKind = "help"
	modalHistory            modalKind = "history"
	modalQueryHistorySearch modalKind = "query_history_search"
	modalDatabasePicker     modalKind = "database_picker"
	modalConfirm            modalKind = "confirm"
)

type modalState struct {
	Kind         modalKind
	Title        string
	Search       string
	ScrollOffset int
}

func (m *Model) handleOverlayKey(msg tea.KeyMsg) bool {
	if !m.overlayOpen() {
		return false
	}
	if msg.String() == "ctrl+w" {
		m.closeActiveOverlay()
		return true
	}
	if m.errBox != nil {
		switch msg.String() {
		case "esc", "enter":
			m.closeErrorBox()
		default:
			m.message = "unsupported shortcut: " + msg.String()
		}
		return true
	}
	if m.helpOpen || (m.modal != nil && m.modal.Kind == modalHelp) {
		return m.handleHelpModalKey(msg)
	}
	if m.form != nil {
		return false
	}
	if m.modal == nil {
		return false
	}
	switch m.modal.Kind {
	case modalHistory:
		return m.handleHistoryModalKey(msg)
	case modalQueryHistorySearch:
		return m.handleQueryHistorySearchModalKey(msg)
	case modalDatabasePicker:
		return m.handleDatabasePickerModalKey(msg)
	default:
		return m.handleGenericModalKey(msg)
	}
}

func (m *Model) openDatabasePickerModal() {
	m.historyIndex = 0
	m.input.Clear()
	m.modal = &modalState{Kind: modalDatabasePicker, Title: "Switch database"}
	m.focusModal()
	m.message = "switch database"
}

func (m *Model) filteredDatabases() []string {
	query := strings.ToLower(strings.TrimSpace(m.input.Value()))
	if query == "" {
		return m.databases
	}
	filtered := make([]string, 0, len(m.databases))
	for _, name := range m.databases {
		if strings.Contains(strings.ToLower(name), query) {
			filtered = append(filtered, name)
		}
	}
	return filtered
}

// selectedRow applies the unified selection highlight to the current list row so
// every movable-selection surface shows where the cursor is.
func selectedRow(theme appTheme, text string, selected bool) string {
	if selected {
		return theme.selected.Render(text)
	}
	return text
}

func (m *Model) databasePickerContent() string {
	var b strings.Builder
	theme := defaultTheme()
	b.WriteString("Search: " + m.input.Value() + m.renderCursorCell(" ") + "\n")
	names := m.filteredDatabases()
	if len(names) == 0 {
		if len(m.databases) == 0 {
			b.WriteString("No databases — refresh the connection first.\n")
		} else {
			b.WriteString("No match.\n")
		}
		b.WriteString("\n[ Close ]")
		return b.String()
	}
	current := m.workspaceDatabaseName()
	m.historyIndex = clamp(m.historyIndex, 0, len(names)-1)
	for i, name := range names {
		selected := i == m.historyIndex
		marker := " "
		if selected {
			marker = ">"
		}
		active := "  "
		if name == current {
			active = "● "
		}
		b.WriteString(selectedRow(theme, fmt.Sprintf("%s %s%s", marker, active, name), selected) + "\n")
	}
	b.WriteString("\n[ Close ]")
	return b.String()
}

func (m *Model) moveDatabasePickerSelection(delta int) {
	names := m.filteredDatabases()
	if len(names) == 0 {
		m.historyIndex = 0
		return
	}
	m.historyIndex = clamp(m.historyIndex+delta, 0, len(names)-1)
	m.ensureHistorySelectionVisible()
}

func (m *Model) acceptDatabasePickerSelection() {
	names := m.filteredDatabases()
	if len(names) == 0 {
		m.message = "no database match"
		return
	}
	m.historyIndex = clamp(m.historyIndex, 0, len(names)-1)
	chosen := names[m.historyIndex]
	m.modal = nil
	m.input.Clear()
	m.restoreModalFocus()
	m.applyDatabaseSelection(chosen)
}

func (m *Model) handleDatabasePickerModalKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "esc":
		m.closeModal()
	case "enter":
		m.acceptDatabasePickerSelection()
	case "up", "k":
		m.moveDatabasePickerSelection(-1)
	case "down", "j":
		m.moveDatabasePickerSelection(1)
	case "pgup":
		m.moveDatabasePickerSelection(-m.modalBodyHeight())
	case "pgdown":
		m.moveDatabasePickerSelection(m.modalBodyHeight())
	case "backspace", "ctrl+h":
		m.input.Backspace()
		m.historyIndex = 0
		m.setModalScroll(0)
	default:
		if len(msg.Runes) == 0 {
			m.message = "unsupported shortcut: " + msg.String()
			return true
		}
		m.input.Insert(string(msg.Runes))
		m.historyIndex = 0
		m.setModalScroll(0)
	}
	return true
}

// applyDatabaseSelection rebinds the active query tab to the chosen database and
// asynchronously loads that database's objects for completion.
func (m *Model) applyDatabaseSelection(database string) {
	tab := m.activeWorkspaceTab()
	if tab == nil || tab.Kind != workspaceTabQuery {
		m.message = "open a query tab to switch its database"
		return
	}
	tab.QueryDatabase = database
	m.syncActiveTabState()
	m.message = "database: " + database
	if m.adapter == nil {
		return
	}
	adapter := m.adapter
	m.nextCmd = m.runAsync("Loading "+database+"…", func(ctx context.Context) tea.Msg {
		objects, err := adapter.ListObjects(ctx, db.Scope{Database: database})
		return asyncResultMsg{apply: func(m *Model) {
			if err != nil {
				m.message = err.Error()
				return
			}
			if m.databaseObjects == nil {
				m.databaseObjects = map[string][]db.Object{}
			}
			m.databaseObjects[database] = objects
			m.message = "database: " + database
		}}
	})
}

func (m *Model) overlayOpen() bool {
	return m.errBox != nil || m.helpOpen || m.form != nil || m.pending != nil || m.modal != nil
}

func (m *Model) closeActiveOverlay() {
	switch {
	case m.errBox != nil:
		m.closeErrorBox()
	case m.form != nil:
		m.cancelConnectionForm()
	case m.helpOpen:
		m.closeHelpPanel()
	case m.modal != nil:
		m.closeModal()
	default:
		m.message = "no modal open"
	}
}

func (m *Model) handleHelpModalKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "esc", "enter":
		m.closeHelpPanel()
	case "up", "k":
		m.scrollModal(-1)
	case "down", "j":
		m.scrollModal(1)
	case "pgup":
		m.scrollModal(-m.modalBodyHeight())
	case "pgdown":
		m.scrollModal(m.modalBodyHeight())
	case "home":
		m.setModalScroll(0)
	case "end":
		m.setModalScroll(m.modalMaxOffset(m.currentModalBody()))
	case "backspace", "ctrl+h":
		m.input.Backspace()
		m.syncHelpSearch()
		m.setModalScroll(0)
	default:
		if len(msg.Runes) == 0 {
			m.message = "unsupported shortcut: " + msg.String()
			return true
		}
		m.input.Insert(string(msg.Runes))
		m.syncHelpSearch()
		m.setModalScroll(0)
	}
	return true
}

func (m *Model) handleHistoryModalKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "esc", "enter":
		m.closeModal()
	case "up", "k":
		m.moveHistorySelection(-1)
		m.ensureHistorySelectionVisible()
	case "down", "j":
		m.moveHistorySelection(1)
		m.ensureHistorySelectionVisible()
	case "pgup":
		m.moveHistorySelection(-m.modalBodyHeight())
		m.ensureHistorySelectionVisible()
	case "pgdown":
		m.moveHistorySelection(m.modalBodyHeight())
		m.ensureHistorySelectionVisible()
	case "home":
		m.historyIndex = 0
		m.ensureHistorySelectionVisible()
	case "end":
		entries := historyEntries(m.history, m.activeProfileID())
		m.historyIndex = max(0, len(entries)-1)
		m.ensureHistorySelectionVisible()
	default:
		m.message = "unsupported shortcut: " + msg.String()
	}
	return true
}

func (m *Model) handleGenericModalKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "esc":
		m.closeModal()
	case "up", "k":
		m.scrollModal(-1)
	case "down", "j":
		m.scrollModal(1)
	case "pgup":
		m.scrollModal(-m.modalBodyHeight())
	case "pgdown":
		m.scrollModal(m.modalBodyHeight())
	default:
		return false
	}
	return true
}

func (m *Model) openHelpPanel() {
	m.helpOpen = true
	m.modal = &modalState{Kind: modalHelp, Title: "Command help"}
	m.focusModal()
	m.input.Clear()
	m.syncHelpSearch()
	m.message = "help opened"
}

func (m *Model) closeHelpPanel() {
	m.helpOpen = false
	if m.modal != nil && m.modal.Kind == modalHelp {
		m.modal = nil
	}
	m.input.Clear()
	m.helpSearch = ""
	m.restoreModalFocus()
	m.message = "help closed"
}

func (m *Model) closeModal() {
	if m.modal == nil {
		return
	}
	switch m.modal.Kind {
	case modalHelp:
		m.closeHelpPanel()
	case modalForm:
		m.form = nil
		m.modal = nil
		m.restoreModalFocus()
		m.message = "connection form cancelled"
	case modalConfirm:
		m.pending = nil
		m.modal = nil
		m.restoreModalFocus()
		m.message = "cancelled"
	default:
		m.modal = nil
		m.restoreModalFocus()
		m.message = "modal closed"
	}
}

func (m *Model) openHistoryModal() {
	m.historyIndex = 0
	m.modal = &modalState{Kind: modalHistory, Title: "History"}
	m.focusModal()
	m.message = "history opened"
}

func (m *Model) syncHelpSearch() {
	m.helpSearch = strings.ToLower(strings.TrimSpace(m.input.Value()))
	if m.modal != nil && m.modal.Kind == modalHelp {
		m.modal.Search = m.helpSearch
	}
}

func (m *Model) modalContent() string {
	// Non-destructive modals share the accent border; destructive ones use danger.
	if m.modal == nil {
		if m.form != nil {
			return m.modalBox("Connection", m.connectionFormContent(), colAccent)
		}
		if m.pending != nil {
			return m.modalBox("Confirm", m.confirmContent(), colDangerLine)
		}
		if m.helpOpen {
			return m.modalBox("Command help", m.helpPanelContent(), colAccent)
		}
		return ""
	}
	switch m.modal.Kind {
	case modalForm:
		return m.modalBox("Connection", m.connectionFormContent(), colAccent)
	case modalHelp:
		return m.modalBox("Command help", m.helpPanelContent(), colAccent)
	case modalHistory:
		return m.modalBox("History", m.historyModalContent(), colAccent)
	case modalQueryHistorySearch:
		return m.modalBox("Query history", m.queryHistorySearchContent(), colAccent)
	case modalDatabasePicker:
		return m.modalBox("Switch database", m.databasePickerContent(), colAccent)
	case modalConfirm:
		return m.modalBox("Confirm", m.confirmContent(), colDangerLine)
	default:
		return ""
	}
}

func (m *Model) modalBox(title, body, border string) string {
	width := clamp(m.workspaceContentWidth(), 32, 84)
	innerWidth := max(24, width-4)
	header := defaultTheme().sectionTitle.Render(title)
	if m.modal != nil {
		body = m.visibleModalBody(body)
	}
	content := header + "\n" + body
	return lipgloss.NewStyle().
		Width(innerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(border)).
		Padding(0, 1).
		Render(content)
}

func (m *Model) visibleModalBody(body string) string {
	if m.modal == nil {
		return body
	}
	height := m.modalBodyHeight()
	maxOffset := m.modalMaxOffset(body)
	m.modal.ScrollOffset = clamp(m.modal.ScrollOffset, 0, maxOffset)
	return sliceLines(body, m.modal.ScrollOffset, height)
}

func (m *Model) scrollModal(delta int) {
	if m.modal == nil {
		return
	}
	m.setModalScroll(m.modal.ScrollOffset + delta)
}

func (m *Model) setModalScroll(offset int) {
	if m.modal == nil {
		return
	}
	m.modal.ScrollOffset = clamp(offset, 0, m.modalMaxOffset(m.currentModalBody()))
}

func (m *Model) modalMaxOffset(body string) int {
	lines := contentLines(body)
	return max(0, len(lines)-m.modalBodyHeight())
}

func (m *Model) modalBodyHeight() int {
	height := m.height
	if height <= 0 {
		height = 30
	}
	return max(3, height-10)
}

func (m *Model) currentModalBody() string {
	if m.modal == nil {
		return ""
	}
	switch m.modal.Kind {
	case modalForm:
		return m.connectionFormContent()
	case modalHelp:
		return m.helpPanelContent()
	case modalHistory:
		return m.historyModalContent()
	case modalQueryHistorySearch:
		return m.queryHistorySearchContent()
	case modalDatabasePicker:
		return m.databasePickerContent()
	case modalConfirm:
		return m.confirmContent()
	default:
		return ""
	}
}

func (m *Model) ensureHistorySelectionVisible() {
	if m.modal == nil {
		return
	}
	height := m.modalBodyHeight()
	if m.historyIndex < m.modal.ScrollOffset {
		m.setModalScroll(m.historyIndex)
		return
	}
	if m.historyIndex >= m.modal.ScrollOffset+height {
		m.setModalScroll(m.historyIndex - height + 1)
	}
}

func contentLines(content string) []string {
	trimmed := strings.TrimRight(content, "\n")
	if trimmed == "" {
		return []string{""}
	}
	return strings.Split(trimmed, "\n")
}

func sliceLines(content string, offset, height int) string {
	lines := contentLines(content)
	if len(lines) <= height {
		return strings.Join(lines, "\n")
	}
	offset = clamp(offset, 0, max(0, len(lines)-height))
	end := min(len(lines), offset+height)
	visible := append([]string{}, lines[offset:end]...)
	if offset > 0 && len(visible) > 0 {
		visible[0] = defaultTheme().muted.Render("↑") + " " + visible[0]
	}
	if end < len(lines) {
		visible = append(visible, defaultTheme().muted.Render("↓"))
	}
	return strings.Join(visible, "\n")
}

func (m *Model) historyModalContent() string {
	var b strings.Builder
	entries := historyEntries(m.history, m.activeProfileID())
	if len(entries) == 0 {
		b.WriteString("No history for current connection.\n")
		b.WriteString("\n[ Close ]")
		return b.String()
	}
	theme := defaultTheme()
	for i, entry := range entries {
		selected := i == m.historyIndex
		marker := " "
		if selected {
			marker = ">"
		}
		row := fmt.Sprintf("%s [%s] %s %s (%dms)", marker, entry.Status, entry.Action, flattenStatement(entry.Statement), entry.DurationMillis)
		b.WriteString(selectedRow(theme, row, selected) + "\n")
		if entry.Error != "" {
			b.WriteString(theme.statusErr.Render("  error: "+flattenStatement(entry.Error)) + "\n")
		}
	}
	b.WriteString("\n[ Close ]")
	return b.String()
}

func (m *Model) handleQueryHistorySearchModalKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "esc":
		m.closeModal()
	case "enter":
		m.acceptQueryHistorySearchSelection()
	case "up", "k":
		m.moveQueryHistorySearchSelection(-1)
	case "down", "j":
		m.moveQueryHistorySearchSelection(1)
	case "pgup":
		m.moveQueryHistorySearchSelection(-m.modalBodyHeight())
	case "pgdown":
		m.moveQueryHistorySearchSelection(m.modalBodyHeight())
	case "backspace", "ctrl+h":
		m.input.Backspace()
		m.historyIndex = 0
		m.setModalScroll(0)
	default:
		if len(msg.Runes) == 0 {
			m.message = "unsupported shortcut: " + msg.String()
			return true
		}
		m.input.Insert(string(msg.Runes))
		m.historyIndex = 0
		m.setModalScroll(0)
	}
	return true
}

func (m *Model) queryHistorySearchContent() string {
	var b strings.Builder
	b.WriteString("Search: " + m.input.Value() + m.renderCursorCell(" ") + "\n")
	entries := m.filteredQueryHistoryEntries()
	if len(entries) == 0 {
		b.WriteString("No query history.\n")
		b.WriteString("\n[ Close ]")
		return b.String()
	}
	theme := defaultTheme()
	m.historyIndex = clamp(m.historyIndex, 0, len(entries)-1)
	for i, entry := range entries {
		selected := i == m.historyIndex
		marker := " "
		if selected {
			marker = ">"
		}
		database := entry.Database
		if database == "" {
			database = "-"
		}
		hits := entry.ExecutionCount
		if hits < 1 {
			hits = 1
		}
		row := fmt.Sprintf("%s [%s] %s  ×%d  %s", marker, database, entry.Driver, hits, flattenStatement(entry.Statement))
		b.WriteString(selectedRow(theme, row, selected) + "\n")
	}
	b.WriteString("\n[ Close ]")
	return b.String()
}

func (m *Model) openQueryHistorySearchModal() {
	m.historyIndex = 0
	m.input.Clear()
	m.modal = &modalState{Kind: modalQueryHistorySearch, Title: "Query history"}
	m.focusModal()
	m.message = "query history search"
}

func (m *Model) filteredQueryHistoryEntries() []history.Entry {
	entries := queryHistoryEntries(m.history, string(m.activeDriver()), 100)
	query := strings.ToLower(strings.TrimSpace(m.input.Value()))
	if query != "" {
		filtered := make([]history.Entry, 0, len(entries))
		for _, entry := range entries {
			if strings.Contains(strings.ToLower(entry.Statement), query) {
				filtered = append(filtered, entry)
			}
		}
		entries = filtered
	}
	// Surface the hottest queries first; break ties by most recent execution.
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].ExecutionCount != entries[j].ExecutionCount {
			return entries[i].ExecutionCount > entries[j].ExecutionCount
		}
		return entries[i].StartedAt.After(entries[j].StartedAt)
	})
	return entries
}

func (m *Model) moveQueryHistorySearchSelection(delta int) {
	entries := m.filteredQueryHistoryEntries()
	if len(entries) == 0 {
		m.historyIndex = 0
		return
	}
	m.historyIndex = clamp(m.historyIndex+delta, 0, len(entries)-1)
	m.ensureHistorySelectionVisible()
}

func (m *Model) acceptQueryHistorySearchSelection() {
	entries := m.filteredQueryHistoryEntries()
	if len(entries) == 0 {
		m.message = "no query history match"
		return
	}
	m.historyIndex = clamp(m.historyIndex, 0, len(entries)-1)
	entry := entries[m.historyIndex]
	if tab := m.activeWorkspaceTab(); tab != nil && tab.Kind == workspaceTabQuery {
		tab.QueryBuffer = sanitizeMultilineInput(entry.Statement)
		tab.QueryCursor = len(tab.QueryBuffer)
		tab.QueryHistoryIndex = -1
		tab.QueryHistoryDraft = ""
		tab.VimMode = vimModeInsert
		tab.WorkspaceFocus = workspaceFocusEditor
	}
	m.modal = nil
	m.input.Clear()
	m.restoreModalFocus()
	m.message = "query history loaded"
}

func (m *Model) showErrorBox(title string, err error) {
	if err == nil {
		return
	}
	m.errBox = &errorBox{Title: title, Message: err.Error()}
	m.focusModal()
	m.message = title + ": " + err.Error()
}

func (m *Model) closeErrorBox() {
	m.errBox = nil
	if m.modal != nil {
		m.modal = nil
	}
	m.restoreModalFocus()
	m.message = "error closed"
}
