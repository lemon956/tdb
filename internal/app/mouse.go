package app

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"tdb/internal/config"
)

func (m *Model) handleMouse(ctx context.Context, msg tea.MouseMsg) {
	event := tea.MouseEvent(msg)
	if event.Button == tea.MouseButtonWheelDown {
		m.handleWheel(1)
		return
	}
	if event.Button == tea.MouseButtonWheelUp {
		m.handleWheel(-1)
		return
	}
	// Dragging the divider between the sidebar and the main panel resizes their
	// widths only; nothing else about the layout changes.
	if m.resizingSidebar {
		switch event.Action {
		case tea.MouseActionMotion:
			m.setSidebarWidth(event.X + 1)
		case tea.MouseActionRelease:
			m.resizingSidebar = false
		}
		return
	}
	if event.Button == tea.MouseButtonLeft && event.Action == tea.MouseActionPress && m.nearSidebarDivider(event.X, event.Y) {
		m.resizingSidebar = true
		m.setSidebarWidth(event.X + 1)
		return
	}
	if event.Button != tea.MouseButtonLeft || event.Action != tea.MouseActionPress {
		return
	}
	hit, ok := m.hitboxes.Hit(event.X, event.Y)
	if !ok {
		return
	}
	if hit.Focus != "" {
		m.focus = hit.Focus
	}
	now := time.Now()
	repeatedClick := hit.ID == m.lastClickID && isOpenableHitbox(hit)
	doubleClick := repeatedClick && now.Sub(m.lastClickAt) <= 450*time.Millisecond
	m.lastClickID = hit.ID
	m.lastClickAt = now
	m.applyHitbox(ctx, hit, event.X, event.Y)
	if repeatedClick || doubleClick {
		m.runPageAction(ctx, actionOpen)
	}
}

// nearSidebarDivider reports whether (x, y) lands on or next to the vertical
// border between the sidebar and the main panel (the body rows below the header).
func (m *Model) nearSidebarDivider(x, y int) bool {
	height := m.height
	if height <= 0 {
		height = 30
	}
	bodyBottom := height - 1 // header row at y=0, footer at the bottom
	if y < 1 || y > bodyBottom {
		return false
	}
	divider := m.sidebarWidth()
	return x >= divider-1 && x <= divider
}

func (m *Model) handleWheel(delta int) {
	if m.modal != nil {
		m.scrollModal(delta)
		return
	}
	if m.focus == FocusMain && m.activeWorkspaceResultAvailable() {
		m.scrollActiveWorkspaceRows(delta)
		return
	}
	switch m.page {
	case PageData:
		m.resultView.ScrollRows(delta, m.resultRowCount())
		m.syncActiveTabFromModel()
	case PageConnections, PageBrowser, PageHistory:
		m.moveSelection(delta)
	}
}

func (m *Model) applyHitbox(ctx context.Context, hit Hitbox, x, y int) {
	if m.applyConnectionFormHitbox(ctx, hit) {
		return
	}
	if hit.ID == "conn-add" {
		m.enterConnectionsManager()
		return
	}
	if strings.HasPrefix(hit.ID, "conn-tab:") {
		if idx, err := strconv.Atoi(strings.TrimPrefix(hit.ID, "conn-tab:")); err == nil {
			m.switchSession(idx)
		}
		return
	}
	if hit.ID == "workspace-db" {
		m.focus = FocusMain
		m.openDatabasePickerModal()
		return
	}
	if strings.HasPrefix(hit.ID, "workspace-tab:") {
		if idx, err := strconv.Atoi(strings.TrimPrefix(hit.ID, "workspace-tab:")); err == nil && idx >= 0 && idx < len(m.workspaceTabs) {
			m.activeTabIndex = idx
			m.focus = FocusMain
			m.syncActiveTabState()
		}
		return
	}
	if strings.HasPrefix(hit.ID, "query-run:") {
		indexText := strings.TrimPrefix(hit.ID, "query-run:")
		index, err := strconv.Atoi(indexText)
		if err == nil {
			m.executeQueryStatementInActiveTab(ctx, index)
		}
		return
	}
	switch hit.ID {
	case "panel-sidebar":
		m.focus = FocusSidebar
	case "panel-main":
		m.focus = FocusMain
		m.applyDataViewportMouse(x, y)
	case "panel-context":
		m.focus = FocusContext
	}
	switch m.page {
	case PageConnections:
		if hit.Index >= 0 && hit.Index < len(m.vault.Profiles) {
			m.connectionIndex = hit.Index
		}
	case PageBrowser, PageData, PageQuery:
		if strings.HasPrefix(hit.ID, "database:") {
			m.setBrowserCursorByNodeID(hit.ID)
			return
		}
		if strings.HasPrefix(hit.ID, "object:") || strings.HasPrefix(hit.ID, "metadata:") || strings.HasPrefix(hit.ID, "fields:") || strings.HasPrefix(hit.ID, "indexes:") {
			m.setBrowserCursorByNodeID(hit.ID)
		}
	case PageHistory:
		if hit.Index >= 0 {
			m.historyIndex = hit.Index
		}
	}
	if hit.Action != actionNone {
		m.runPageAction(ctx, hit.Action)
	}
}

func (m *Model) applyDataViewportMouse(x, y int) bool {
	tab := m.activeWorkspaceTab()
	if tab == nil || tab.Kind != workspaceTabData {
		return false
	}
	contentX := m.sidebarWidth() + 2
	contentY := 2
	headerLines := 3
	if tab.VimMode == vimModeVisual {
		headerLines = 4
	}
	viewportRow := y - contentY - 2 - headerLines
	if viewportRow < 0 {
		return false
	}
	lines := m.activeDataLines(tab, m.workspaceContentWidth())
	if len(lines) == 0 {
		return false
	}
	row := tab.ResultView.RowOffset + viewportRow
	if row < 0 || row >= len(lines) {
		return false
	}
	column := tab.ResultView.ColumnOffset + max(0, x-contentX)
	lineWidth := max(1, lipgloss.Width(lines[row]))
	tab.ResultRow = row
	tab.ResultColumn = clamp(column, 0, lineWidth-1)
	tab.WorkspaceFocus = workspaceFocusResult
	m.clampDataCursor(tab)
	m.ensureDataCursorVisible(tab)
	m.syncActiveTabState()
	return true
}

func (m *Model) applyConnectionFormHitbox(ctx context.Context, hit Hitbox) bool {
	if m.form == nil {
		return false
	}
	if strings.HasPrefix(hit.ID, "form-driver:") {
		m.form.chooseDriver(config.Driver(hit.Payload))
		m.focus = FocusMain
		m.message = "creating " + hit.Payload + " connection"
		return true
	}
	if strings.HasPrefix(hit.ID, "form-field:") {
		m.form.fieldIndex = clamp(hit.Index, 0, len(m.form.fields))
		m.focus = FocusMain
		return true
	}
	switch hit.ID {
	case "form-readonly":
		m.form.readOnly = !m.form.readOnly
		m.focus = FocusMain
		m.message = "readonly toggled"
		return true
	case "form:save":
		m.submitConnectionForm(ctx)
		return true
	case "form:cancel":
		m.cancelConnectionForm()
		return true
	}
	return false
}

func isOpenableHitbox(hit Hitbox) bool {
	return strings.HasPrefix(hit.ID, "connection:") ||
		strings.HasPrefix(hit.ID, "database:") ||
		strings.HasPrefix(hit.ID, "object:") ||
		strings.HasPrefix(hit.ID, "metadata:") ||
		strings.HasPrefix(hit.ID, "fields:") ||
		strings.HasPrefix(hit.ID, "indexes:") ||
		strings.HasPrefix(hit.ID, "history:")
}
