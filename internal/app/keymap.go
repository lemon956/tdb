package app

import (
	"context"
	"fmt"

	"tdb/internal/config"
)

type appAction string

const (
	actionNone         appAction = ""
	actionFocusCommand appAction = "focus-command"
	actionHelp         appAction = "help"
	actionBack         appAction = "back"
	actionMoveLeft     appAction = "move-left"
	actionMoveRight    appAction = "move-right"
	actionMoveUp       appAction = "move-up"
	actionMoveDown     appAction = "move-down"
	actionRefresh      appAction = "refresh"
	actionOpen         appAction = "open"
	actionNew          appAction = "new"
	actionEdit         appAction = "edit"
	actionDelete       appAction = "delete"
	actionTest         appAction = "test"
	actionQuery        appAction = "query"
	actionHistory      appAction = "history"
	actionConfirm      appAction = "confirm"
	actionCancel       appAction = "cancel"
)

func focusOrder(width int) []Focus {
	return []Focus{FocusSidebar, FocusMain}
}

func panelFocusOrder() []Focus {
	return []Focus{FocusSidebar, FocusMain}
}

func (m *Model) focusCommand() {
	if m.focus != FocusCommand && m.focus != "" {
		m.previousFocus = m.focus
	}
	if m.previousFocus == "" || m.previousFocus == FocusCommand {
		m.previousFocus = FocusMain
	}
	m.focus = FocusCommand
}

func (m *Model) restorePreviousFocus() {
	if m.previousFocus == "" || m.previousFocus == FocusCommand {
		m.focus = FocusMain
		return
	}
	m.focus = m.previousFocus
}

func (m *Model) exitCommandMode() {
	m.input.Clear()
	m.restorePreviousFocus()
}

func (m *Model) focusModal() {
	restore := m.focus
	if restore == FocusCommand && m.previousFocus != "" && m.previousFocus != FocusCommand {
		restore = m.previousFocus
	}
	if restore == "" || restore == FocusCommand {
		restore = FocusMain
	}
	m.modalPreviousFocus = restore
	m.focus = FocusContext
}

func (m *Model) restoreModalFocus() {
	if m.modalPreviousFocus == "" {
		m.restorePreviousFocus()
		return
	}
	m.focus = m.modalPreviousFocus
	m.modalPreviousFocus = ""
}

func (m *Model) cycleFocus(delta int) {
	order := focusOrder(m.width)
	if m.focus == FocusCommand && delta != 0 {
		if m.previousFocus != "" && m.previousFocus != FocusCommand {
			m.focus = m.previousFocus
			return
		}
		m.focus = FocusSidebar
		return
	}
	idx := 0
	for i, focus := range order {
		if focus == m.focus {
			idx = i
			break
		}
	}
	idx = (idx + delta) % len(order)
	if idx < 0 {
		idx += len(order)
	}
	m.focus = order[idx]
}

func (m *Model) movePanelFocus(delta int) {
	order := panelFocusOrder()
	idx := 0
	for i, focus := range order {
		if focus == m.focus {
			idx = i
			break
		}
	}
	idx = clamp(idx+delta, 0, len(order)-1)
	m.focus = order[idx]
}

func (m *Model) isTextEntryMode() bool {
	return m.page == PageUnlock || m.focus == FocusCommand || m.pending != nil || m.form != nil
}

// inTextInputContext reports whether the focused surface is consuming typed text
// (command line, form, view-search, a text-entry modal, or the vim editor in
// insert mode). In these contexts a coalesced multi-rune KeyMsg must stay intact
// so a paste/fast-typing inserts all runes at once; elsewhere it is split so a
// held navigation key repeats.
func (m *Model) inTextInputContext() bool {
	if m.isTextEntryMode() || m.viewSearchInput || m.helpOpen {
		return true
	}
	if m.modal != nil {
		switch m.modal.Kind {
		case modalDatabasePicker, modalQueryHistorySearch, modalAIChat, modalHelp:
			return true
		}
	}
	if tab := m.activeWorkspaceTab(); tab != nil && tab.Kind == workspaceTabQuery &&
		tab.WorkspaceFocus == workspaceFocusEditor && tab.VimMode == vimModeInsert {
		return true
	}
	return false
}

func (m *Model) moveSelection(delta int) {
	switch m.page {
	case PageConnections:
		m.connectionIndex = clamp(m.connectionIndex+delta, 0, max(0, len(m.vault.Profiles)-1))
	case PageBrowser, PageData, PageQuery:
		// Tree pages have a navigable sidebar (left) and a workspace (right);
		// movement keys act on whichever panel is focused, never both.
		if m.focus == FocusSidebar {
			m.moveBrowserSelection(delta)
		} else if !m.scrollActiveWorkspaceRows(delta) {
			m.resultView.ScrollRows(delta, m.resultRowCount())
			m.syncActiveTabFromModel()
		}
	case PageHistory:
		m.moveHistorySelection(delta)
	}
}

func (m *Model) navigationSidebarFocused() bool {
	return m.focus == FocusSidebar && (m.page == PageBrowser || m.page == PageData || m.page == PageQuery)
}

func (m *Model) scrollNavigationHorizontal(delta int) {
	m.navHorizontalOffset = max(0, m.navHorizontalOffset+delta)
}

func (m *Model) runPageAction(ctx context.Context, action appAction) {
	switch action {
	case actionConfirm:
		if m.form != nil {
			m.submitConnectionForm(ctx)
			return
		}
		m.handleConfirmation(ctx, "yes")
	case actionCancel:
		if m.errBox != nil {
			m.closeErrorBox()
			return
		}
		if m.form != nil {
			m.cancelConnectionForm()
			return
		}
		if m.helpOpen {
			m.closeHelpPanel()
			return
		}
		if m.modal != nil {
			m.closeModal()
			return
		}
		m.handleConfirmation(ctx, "no")
	case actionOpen:
		m.openSelected(ctx)
	case actionRefresh:
		if m.page == PageBrowser {
			m.refreshBrowser(ctx)
		}
		if m.page == PageData {
			if m.workspaceMode == workspaceMetadata {
				m.openMetadata(ctx, m.target)
			} else {
				m.openPreview(ctx, m.target)
			}
		}
	case actionNew:
		switch m.page {
		case PageConnections:
			m.openConnectionForm()
		case PageBrowser:
			if m.activeDriver() == config.DriverRedis {
				m.nextRedisScan(ctx)
			}
		case PageData:
			m.focus = FocusCommand
			m.input.SetValue("insert ")
		}
	case actionEdit:
		if m.page == PageConnections {
			if profile, ok := m.selectedProfile(); ok {
				m.openEditConnectionForm(profile)
			}
			return
		}
		if m.page == PageData {
			m.focus = FocusCommand
			m.input.SetValue("update ")
		}
	case actionDelete:
		if m.page == PageConnections {
			if profile, ok := m.selectedProfile(); ok {
				m.pending = &pendingAction{Kind: "delete-profile", ProfileID: profile.ID}
				m.modal = &modalState{Kind: modalConfirm, Title: "Confirm"}
				m.focusModal()
				m.message = "delete requires confirmation"
			}
		}
		if m.page == PageData {
			m.pending = &pendingAction{Kind: "delete", Target: m.target}
			m.modal = &modalState{Kind: modalConfirm, Title: "Confirm"}
			m.focusModal()
			m.message = "delete requires confirmation"
		}
	case actionTest:
		if profile, ok := m.selectedProfile(); ok {
			m.testProfile(ctx, profile.ID)
		}
	case actionQuery:
		m.openQueryWorkspaceTab()
	case actionHistory:
		m.openHistoryModal()
	}
}

func (m *Model) openSelected(ctx context.Context) {
	switch m.page {
	case PageConnections:
		profile, ok := m.selectedProfile()
		if !ok {
			m.message = "no connection selected"
			return
		}
		m.openProfile(ctx, profile.ID)
	case PageBrowser, PageData, PageQuery:
		node, ok := m.selectedBrowserNode()
		if !ok {
			m.message = "nothing selected"
			return
		}
		switch node.Kind {
		case navNodeCatalog:
			m.toggleCatalog(ctx, node.Catalog)
		case navNodeDatabase:
			m.toggleDatabase(ctx, node.Catalog, node.Database)
		case navNodeObject:
			m.selectedCatalog = node.Catalog
			m.selectedDB = node.Database
			m.databaseIndex = node.DatabaseIdx
			m.objects = m.databaseObjects[m.scopeKey(node.Catalog, node.Database)]
			m.objectIndex = node.ObjectIdx
			m.bindActiveQueryTabToSelection()
			if sameTarget(m.target, node.Target) && m.workspaceMode == workspacePreview {
				m.openMetadata(ctx, node.Target)
				return
			}
			m.openPreview(ctx, node.Target)
		case navNodeMeta:
			m.openMetadata(ctx, node.Target)
		}
	case PageHistory:
		m.refillSelectedHistory()
	}
}

func (m *Model) moveBrowserSelection(delta int) {
	if len(m.databases) == 0 && len(m.externalCatalogs()) == 0 {
		m.objectIndex = clamp(m.objectIndex+delta, 0, max(0, len(m.objects)-1))
		rootOffset := 0
		if m.activeProfile != nil {
			rootOffset = 1
		}
		m.browserCursor = rootOffset + m.objectIndex
		return
	}
	nodes := m.browserNodes()
	if len(nodes) == 0 {
		m.objectIndex = clamp(m.objectIndex+delta, 0, max(0, len(m.objects)-1))
		m.browserCursor = m.objectIndex
		return
	}
	if m.activeProfile != nil && len(nodes) > 1 && m.browserCursor == 0 {
		m.browserCursor = 1
	}
	m.browserCursor = clamp(m.browserCursor+delta, 0, len(nodes)-1)
	m.syncBrowserSelectionFromCursor()
}

func (m *Model) syncBrowserSelectionFromCursor() {
	node, ok := m.selectedBrowserNode()
	if !ok {
		return
	}
	switch node.Kind {
	case navNodeCatalog:
		m.selectedCatalog = node.Catalog
	case navNodeDatabase:
		m.selectedCatalog = node.Catalog
		m.databaseIndex = node.DatabaseIdx
	case navNodeObject, navNodeMeta:
		m.selectedCatalog = node.Catalog
		m.databaseIndex = node.DatabaseIdx
		m.objectIndex = node.ObjectIdx
		if objects, ok := m.databaseObjects[m.scopeKey(node.Catalog, node.Database)]; ok {
			m.objects = objects
		}
	}
}

func (m *Model) browserSelectionIsDatabase() bool {
	node, ok := m.selectedBrowserNode()
	return ok && node.Kind == navNodeDatabase
}

func (m *Model) selectedObjectIndex() (int, bool) {
	node, ok := m.selectedBrowserNode()
	if ok && (node.Kind == navNodeObject || node.Kind == navNodeMeta) {
		return node.ObjectIdx, true
	}
	if len(m.objects) == 0 {
		return 0, false
	}
	return clamp(m.objectIndex, 0, len(m.objects)-1), true
}

func (m *Model) selectedProfile() (config.Profile, bool) {
	if len(m.vault.Profiles) == 0 {
		return config.Profile{}, false
	}
	m.connectionIndex = clamp(m.connectionIndex, 0, len(m.vault.Profiles)-1)
	return m.vault.Profiles[m.connectionIndex], true
}

func selectedLabel(index, total int) string {
	if total == 0 {
		return "0/0"
	}
	return fmt.Sprintf("%d/%d", index+1, total)
}
