package app

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/suggest"
)

// queryCursorLeft / queryCursorRight move the byte-offset cursor by one full
// rune so it never lands inside a multi-byte UTF-8 character (which would
// otherwise make the rune-aware renderer drop the cursor cell).
func queryCursorLeft(buffer string, cursor int) int {
	cursor = clamp(cursor, 0, len(buffer))
	if cursor == 0 {
		return 0
	}
	_, size := utf8.DecodeLastRuneInString(buffer[:cursor])
	return cursor - size
}

func queryCursorRight(buffer string, cursor int) int {
	cursor = clamp(cursor, 0, len(buffer))
	if cursor >= len(buffer) {
		return len(buffer)
	}
	_, size := utf8.DecodeRuneInString(buffer[cursor:])
	return cursor + size
}

func (m *Model) handleWorkspaceVimKey(ctx context.Context, msg tea.KeyMsg) bool {
	if m.focus != FocusMain {
		return false
	}
	tab := m.activeWorkspaceTab()
	if tab == nil {
		return false
	}
	switch tab.Kind {
	case workspaceTabQuery:
		return m.handleQueryTabKey(ctx, tab, msg)
	case workspaceTabData:
		return m.handleDataTabKey(tab, msg)
	default:
		return false
	}
}

func (m *Model) handleQueryTabKey(ctx context.Context, tab *workspaceTab, msg tea.KeyMsg) bool {
	ensureQueryTabState(tab)
	// Ctrl+C clears the query input when it has content (it never quits — only
	// ":q" does). An empty buffer swallows the key so nothing else acts on it.
	if msg.String() == "ctrl+c" {
		if tab.QueryBuffer != "" {
			pushQueryUndo(tab)
			tab.QueryBuffer = ""
			tab.QueryCursor = 0
			tab.QuerySuggestionsVisible = false
			tab.QueryHistoryIndex = -1
			m.message = "query cleared"
		}
		return true
	}
	if tab.RowDetail {
		return m.handleRowDetailKey(tab, msg)
	}
	if tab.WorkspaceFocus == workspaceFocusResult {
		return m.handleQueryResultKey(tab, msg)
	}
	switch tab.VimMode {
	case vimModeInsert:
		return m.handleQueryInsertKey(ctx, tab, msg)
	case vimModeVisual:
		return m.handleQueryVisualKey(tab, msg)
	default:
		return m.handleQueryNormalKey(ctx, tab, msg)
	}
}

func ensureQueryTabState(tab *workspaceTab) {
	if tab.VimMode == "" {
		tab.VimMode = vimModeNormal
	}
	if tab.WorkspaceFocus == "" {
		tab.WorkspaceFocus = workspaceFocusEditor
	}
	tab.QueryCursor = clamp(tab.QueryCursor, 0, len(tab.QueryBuffer))
}

func (m *Model) handleQueryNormalKey(ctx context.Context, tab *workspaceTab, msg tea.KeyMsg) bool {
	if tab.QueryPendingOp != "" {
		return m.handleQueryPendingNormalKey(tab, msg)
	}
	switch msg.String() {
	case "ctrl+s":
		m.openQueryHistorySearchModal()
	case "ctrl+d":
		m.openDatabasePickerModal()
	case "i":
		tab.VimMode = vimModeInsert
		tab.WorkspaceFocus = workspaceFocusEditor
	case "v":
		tab.VimMode = vimModeVisual
		tab.SelectionAnchor = tab.QueryCursor
	case "w":
		tab.QueryCursor = queryNextWord(tab.QueryBuffer, tab.QueryCursor)
	case "b":
		tab.QueryCursor = queryPreviousWord(tab.QueryBuffer, tab.QueryCursor)
	case "0", "home":
		tab.QueryCursor = queryLineStart(tab.QueryBuffer, tab.QueryCursor)
	case "$", "end":
		tab.QueryCursor = queryLineEnd(tab.QueryBuffer, tab.QueryCursor)
	case "g", "d", "y":
		tab.QueryPendingOp = msg.String()
	case "G":
		tab.QueryCursor = max(0, len(tab.QueryBuffer)-1)
	case "u":
		m.undoQueryEdit(tab)
	case "p":
		m.pasteQueryClipboard(tab)
	case "o":
		m.openQueryLine(tab, false)
	case "O":
		m.openQueryLine(tab, true)
	case "enter":
		if tab.QueryBuffer == "" {
			m.message = "query buffer is empty"
			return true
		}
		m.executeQueryInActiveTab(ctx, tab.QueryBuffer)
	case "h", "left":
		tab.QueryCursor = queryCursorLeft(tab.QueryBuffer, tab.QueryCursor)
	case "l", "right":
		tab.QueryCursor = queryCursorRight(tab.QueryBuffer, tab.QueryCursor)
	case "x", "delete":
		if tab.QueryCursor < len(tab.QueryBuffer) {
			pushQueryUndo(tab)
			next := queryCursorRight(tab.QueryBuffer, tab.QueryCursor)
			tab.QueryBuffer = tab.QueryBuffer[:tab.QueryCursor] + tab.QueryBuffer[next:]
		}
	default:
		return false
	}
	return true
}

func (m *Model) handleQueryPendingNormalKey(tab *workspaceTab, msg tea.KeyMsg) bool {
	op := tab.QueryPendingOp
	tab.QueryPendingOp = ""
	switch op {
	case "g":
		if msg.String() == "g" {
			tab.QueryCursor = 0
			return true
		}
	case "d":
		if msg.String() == "d" {
			m.deleteQueryCurrentLine(tab)
			return true
		}
	case "y":
		if msg.String() == "y" {
			tab.QueryClipboard = queryCurrentLineText(tab.QueryBuffer, tab.QueryCursor)
			m.copyText(strings.TrimRight(tab.QueryClipboard, "\n"))
			return true
		}
	}
	m.message = "unsupported vim command: " + op + msg.String()
	return true
}

func (m *Model) handleQueryInsertKey(ctx context.Context, tab *workspaceTab, msg tea.KeyMsg) bool {
	switch msg.String() {
	case "esc":
		tab.VimMode = vimModeNormal
		tab.QueryHistoryIndex = -1
		tab.QuerySuggestionsVisible = false
	case "backspace", "ctrl+h":
		if tab.QueryCursor > 0 && len(tab.QueryBuffer) > 0 {
			prev := queryCursorLeft(tab.QueryBuffer, tab.QueryCursor)
			tab.QueryBuffer = tab.QueryBuffer[:prev] + tab.QueryBuffer[tab.QueryCursor:]
			tab.QueryCursor = prev
			tab.QueryHistoryIndex = -1
			m.refreshQuerySuggestions(tab)
		}
	case "delete":
		if tab.QueryCursor < len(tab.QueryBuffer) {
			next := queryCursorRight(tab.QueryBuffer, tab.QueryCursor)
			tab.QueryBuffer = tab.QueryBuffer[:tab.QueryCursor] + tab.QueryBuffer[next:]
			tab.QueryHistoryIndex = -1
			m.refreshQuerySuggestions(tab)
		}
	case "enter":
		if m.acceptQuerySuggestion(tab) {
			return true
		}
		if tab.QueryBuffer == "" {
			m.message = "query buffer is empty"
			return true
		}
		m.executeQueryInActiveTab(ctx, tab.QueryBuffer)
	case "ctrl+j", "alt+enter":
		insertQueryText(tab, "\n")
		tab.QueryHistoryIndex = -1
		tab.QuerySuggestionsVisible = false
	case "up":
		if tab.QuerySuggestionsVisible {
			m.moveQuerySuggestion(tab, -1)
		} else {
			m.moveQueryHistory(tab, 1)
		}
	case "down":
		if tab.QuerySuggestionsVisible {
			m.moveQuerySuggestion(tab, 1)
		} else {
			m.moveQueryHistory(tab, -1)
		}
	case "ctrl+s":
		m.openQueryHistorySearchModal()
	case "ctrl+d":
		m.openDatabasePickerModal()
	case "left":
		tab.QueryCursor = queryCursorLeft(tab.QueryBuffer, tab.QueryCursor)
		m.refreshQuerySuggestions(tab)
	case "right":
		tab.QueryCursor = queryCursorRight(tab.QueryBuffer, tab.QueryCursor)
		m.refreshQuerySuggestions(tab)
	case "home", "ctrl+a":
		tab.QueryCursor = queryLineStart(tab.QueryBuffer, tab.QueryCursor)
		m.refreshQuerySuggestions(tab)
	case "end", "ctrl+e":
		if i := strings.IndexByte(tab.QueryBuffer[clamp(tab.QueryCursor, 0, len(tab.QueryBuffer)):], '\n'); i >= 0 {
			tab.QueryCursor = clamp(tab.QueryCursor, 0, len(tab.QueryBuffer)) + i
		} else {
			tab.QueryCursor = len(tab.QueryBuffer)
		}
		m.refreshQuerySuggestions(tab)
	case "alt+b":
		tab.QueryCursor = queryPreviousWord(tab.QueryBuffer, tab.QueryCursor)
		m.refreshQuerySuggestions(tab)
	case "alt+f":
		tab.QueryCursor = queryNextWord(tab.QueryBuffer, tab.QueryCursor)
		m.refreshQuerySuggestions(tab)
	default:
		if len(msg.Runes) == 0 {
			return false
		}
		insertQueryText(tab, string(msg.Runes))
		tab.QueryHistoryIndex = -1
		m.refreshQuerySuggestions(tab)
	}
	return true
}

func (m *Model) handleQueryVisualKey(tab *workspaceTab, msg tea.KeyMsg) bool {
	switch msg.String() {
	case "esc":
		tab.VimMode = vimModeNormal
	case "h", "left":
		tab.QueryCursor = queryCursorLeft(tab.QueryBuffer, tab.QueryCursor)
	case "l", "right":
		tab.QueryCursor = queryCursorRight(tab.QueryBuffer, tab.QueryCursor)
	case "d":
		start, end := querySelectionRange(tab)
		if start <= end && start < len(tab.QueryBuffer) {
			tab.QueryBuffer = tab.QueryBuffer[:start] + tab.QueryBuffer[end+1:]
			tab.QueryCursor = clamp(start, 0, len(tab.QueryBuffer))
		}
		tab.VimMode = vimModeNormal
	case "y":
		start, end := querySelectionRange(tab)
		if start <= end && start < len(tab.QueryBuffer) {
			m.copyText(tab.QueryBuffer[start : end+1])
		}
		tab.VimMode = vimModeNormal
	default:
		return false
	}
	return true
}

func (m *Model) handleDataTabKey(tab *workspaceTab, msg tea.KeyMsg) bool {
	m.ensureDataTabState(tab)
	if isDataWriteKey(msg.String()) {
		m.message = "data tab is read-only"
		return true
	}
	switch tab.VimMode {
	case vimModeVisual:
		return m.handleDataVisualKey(tab, msg)
	default:
		return m.handleDataNormalKey(tab, msg)
	}
}

func (m *Model) ensureDataTabState(tab *workspaceTab) {
	if tab.VimMode == "" || tab.VimMode == vimModeInsert {
		tab.VimMode = vimModeNormal
	}
	tab.WorkspaceFocus = workspaceFocusResult
	m.clampDataCursor(tab)
}

func (m *Model) handleDataNormalKey(tab *workspaceTab, msg tea.KeyMsg) bool {
	prevG := tab.ResultPendingG
	tab.ResultPendingG = false
	switch msg.String() {
	case "v":
		tab.VimMode = vimModeVisual
		tab.ResultAnchorRow = tab.ResultRow
		tab.ResultAnchorColumn = tab.ResultColumn
	case "y":
		tab.ResultAnchorRow = tab.ResultRow
		tab.ResultAnchorColumn = tab.ResultColumn
		m.copyText(m.dataSelectionText(tab))
	case "g":
		if prevG {
			m.moveDataCursor(tab, -1<<30, 0)
		} else {
			tab.ResultPendingG = true
		}
	case "G":
		m.moveDataCursor(tab, 1<<30, 0)
	case "j", "down":
		m.moveDataCursor(tab, 1, 0)
	case "k", "up":
		m.moveDataCursor(tab, -1, 0)
	case "h", "left":
		m.moveDataCursor(tab, 0, -1)
	case "l", "right":
		m.moveDataCursor(tab, 0, 1)
	case "pgdown":
		m.moveDataCursor(tab, m.dataViewportHeight(tab), 0)
	case "pgup":
		m.moveDataCursor(tab, -m.dataViewportHeight(tab), 0)
	case "]":
		m.pageData(1)
	case "[":
		m.pageData(-1)
	default:
		return m.handleDataMotionKey(tab, msg.String())
	}
	return true
}

func (m *Model) handleDataVisualKey(tab *workspaceTab, msg tea.KeyMsg) bool {
	prevG := tab.ResultPendingG
	tab.ResultPendingG = false
	switch msg.String() {
	case "esc":
		tab.VimMode = vimModeNormal
	case "y":
		m.copyText(m.dataSelectionText(tab))
		tab.VimMode = vimModeNormal
	case "g":
		if prevG {
			m.moveDataCursor(tab, -1<<30, 0)
		} else {
			tab.ResultPendingG = true
		}
	case "G":
		m.moveDataCursor(tab, 1<<30, 0)
	case "j", "down":
		m.moveDataCursor(tab, 1, 0)
	case "k", "up":
		m.moveDataCursor(tab, -1, 0)
	case "h", "left":
		m.moveDataCursor(tab, 0, -1)
	case "l", "right":
		m.moveDataCursor(tab, 0, 1)
	default:
		return m.handleDataMotionKey(tab, msg.String())
	}
	return true
}

func isDataWriteKey(key string) bool {
	switch key {
	case "i", "e", "d", "x", "delete":
		return true
	default:
		return false
	}
}

func (m *Model) moveDataCursor(tab *workspaceTab, rowDelta, colDelta int) {
	tab.ResultRow += rowDelta
	tab.ResultColumn += colDelta
	m.clampDataCursor(tab)
	m.ensureDataCursorVisible(tab)
	m.syncActiveTabState()
}

// currentDataLine returns the rendered text of the line the data cursor is on.
func (m *Model) currentDataLine(tab *workspaceTab) string {
	lines := m.activeDataLines(tab, m.workspaceContentWidth())
	if len(lines) == 0 {
		return ""
	}
	return lines[clamp(tab.ResultRow, 0, len(lines)-1)]
}

// setDataCursorColumn moves the data cursor to an absolute column on its line.
func (m *Model) setDataCursorColumn(tab *workspaceTab, col int) {
	tab.ResultColumn = col
	m.clampDataCursor(tab)
	m.ensureDataCursorVisible(tab)
	m.syncActiveTabState()
}

// handleDataMotionKey handles line-start/end and word motions shared by the data
// grid's normal and visual modes. Returns false for keys it does not handle.
func (m *Model) handleDataMotionKey(tab *workspaceTab, key string) bool {
	line := m.currentDataLine(tab)
	switch key {
	case "0", "home":
		m.setDataCursorColumn(tab, 0)
	case "$", "end":
		m.setDataCursorColumn(tab, max(0, lipgloss.Width(line)-1))
	case "w":
		m.setDataCursorColumn(tab, queryNextWord(line, tab.ResultColumn))
	case "b":
		m.setDataCursorColumn(tab, queryPreviousWord(line, tab.ResultColumn))
	default:
		return false
	}
	return true
}

func (m *Model) clampDataCursor(tab *workspaceTab) {
	rows := max(1, m.dataCursorRowCount(tab))
	tab.ResultRow = clamp(tab.ResultRow, 0, rows-1)
	cols := max(1, m.dataCursorColumnCountForRow(tab, tab.ResultRow))
	tab.ResultColumn = clamp(tab.ResultColumn, 0, cols-1)
	tab.ResultAnchorRow = clamp(tab.ResultAnchorRow, 0, rows-1)
	anchorCols := max(1, m.dataCursorColumnCountForRow(tab, tab.ResultAnchorRow))
	tab.ResultAnchorColumn = clamp(tab.ResultAnchorColumn, 0, anchorCols-1)
}

func (m *Model) ensureDataCursorVisible(tab *workspaceTab) {
	height := m.dataViewportHeight(tab)
	offset := clamp(tab.ResultView.RowOffset, 0, max(0, m.dataCursorRowCount(tab)-height))
	if tab.ResultRow < offset {
		offset = tab.ResultRow
	}
	if tab.ResultRow >= offset+height {
		offset = tab.ResultRow - height + 1
	}
	tab.ResultView.RowOffset = clamp(offset, 0, max(0, m.dataCursorRowCount(tab)-height))
	width := m.workspaceContentWidth()
	maxWidth := maxDataDisplayLineWidth(m.activeDataLines(tab, width))
	colOffset := clamp(tab.ResultView.ColumnOffset, 0, max(0, maxWidth-width))
	if tab.ResultColumn < colOffset {
		colOffset = tab.ResultColumn
	}
	if tab.ResultColumn >= colOffset+width {
		colOffset = tab.ResultColumn - width + 1
	}
	tab.ResultView.ColumnOffset = clamp(colOffset, 0, max(0, maxWidth-width))
}

func (m *Model) dataCursorRowCount(tab *workspaceTab) int {
	return len(m.activeDataLines(tab, m.workspaceContentWidth()))
}

func (m *Model) dataCursorColumnCountForRow(tab *workspaceTab, row int) int {
	lines := m.activeDataLines(tab, m.workspaceContentWidth())
	if len(lines) == 0 {
		return 1
	}
	row = clamp(row, 0, len(lines)-1)
	return max(1, lipgloss.Width(lines[row]))
}

func (m *Model) dataViewportHeight(tab *workspaceTab) int {
	headerLines := 3
	if tab.VimMode == vimModeVisual {
		headerLines = 4
	}
	return m.workspaceResultViewportHeight(headerLines)
}

func (m *Model) dataSelectionText(tab *workspaceTab) string {
	lines := m.activeDataLines(tab, m.workspaceContentWidth())
	if len(lines) == 0 {
		return ""
	}
	startRow, startCol, endRow, endCol := orderedDataSelection(*tab)
	return selectedDisplayText(lines, startRow, startCol, endRow, endCol)
}

func orderedDataSelection(tab workspaceTab) (int, int, int, int) {
	startRow := tab.ResultAnchorRow
	startCol := tab.ResultAnchorColumn
	endRow := tab.ResultRow
	endCol := tab.ResultColumn
	if startRow > endRow || (startRow == endRow && startCol > endCol) {
		return endRow, endCol, startRow, startCol
	}
	return startRow, startCol, endRow, endCol
}

func selectedDisplayText(lines []string, startRow, startCol, endRow, endCol int) string {
	if len(lines) == 0 {
		return ""
	}
	startRow = clamp(startRow, 0, len(lines)-1)
	endRow = clamp(endRow, 0, len(lines)-1)
	if startRow > endRow {
		startRow, endRow = endRow, startRow
		startCol, endCol = endCol, startCol
	}
	selected := make([]string, 0, endRow-startRow+1)
	for row := startRow; row <= endRow; row++ {
		line := lines[row]
		width := max(1, lipgloss.Width(line))
		from := 0
		to := width - 1
		if row == startRow {
			from = clamp(startCol, 0, width-1)
		}
		if row == endRow {
			to = clamp(endCol, 0, width-1)
		}
		if from > to {
			from, to = to, from
		}
		selected = append(selected, cellSlice(line, from, to-from+1))
	}
	return strings.Join(selected, "\n")
}

func (m *Model) copyText(text string) {
	m.lastCopiedText = text
	if text == "" {
		m.message = "nothing copied"
		return
	}
	var clipboardErr error
	if m.options.ClipboardCopier != nil {
		if err := m.options.ClipboardCopier.Copy(text); err == nil {
			m.message = "copied"
			return
		} else {
			clipboardErr = err
		}
	}
	if m.options.ClipboardWriter != nil {
		if _, err := osc52.New(text).WriteTo(m.options.ClipboardWriter); err != nil {
			if clipboardErr != nil {
				m.message = "copy failed: " + clipboardErr.Error() + "; OSC52 fallback failed: " + err.Error()
				return
			}
			m.message = "copy failed: OSC52 fallback failed: " + err.Error()
			return
		}
		m.message = "copied"
		return
	}
	if clipboardErr != nil {
		m.message = "copy failed: " + clipboardErr.Error()
		return
	}
	m.message = "copied"
}

func (m *Model) handleQueryResultKey(tab *workspaceTab, msg tea.KeyMsg) bool {
	selectable := tab.Result.Table != nil && len(tab.Result.Table.Rows) > 0
	// Non-table results (e.g. a Redis scalar / JSON value) have no rows to select
	// by, so they reuse the data grid's char-level cursor / visual / copy machinery
	// — exactly like the row-detail subpage. This gives every driver a working
	// v → select → y on its result page.
	if hasResultSet(tab.Result) && !selectable {
		m.clampDataCursor(tab)
		if tab.VimMode == vimModeVisual {
			return m.handleDataVisualKey(tab, msg)
		}
		switch msg.String() {
		case "ctrl+s":
			m.openQueryHistorySearchModal()
			return true
		case "ctrl+d":
			m.openDatabasePickerModal()
			return true
		case "i":
			tab.WorkspaceFocus = workspaceFocusEditor
			tab.VimMode = vimModeInsert
			return true
		}
		return m.handleDataNormalKey(tab, msg)
	}

	// Table result list: vim-like row-visual ("copy mode"). Press v to start a
	// row selection, j/k to extend it, y to copy the selected rows (TSV).
	if selectable && tab.VimMode == vimModeVisual {
		prevG := tab.ResultPendingG
		tab.ResultPendingG = false
		switch msg.String() {
		case "esc":
			tab.VimMode = vimModeNormal
		case "y":
			m.copyText(m.queryResultSelectionText(tab))
			tab.VimMode = vimModeNormal
		case "g":
			if prevG {
				m.moveQueryResultSelection(tab, -1<<30)
			} else {
				tab.ResultPendingG = true
			}
		case "G":
			m.moveQueryResultSelection(tab, 1<<30)
		case "j", "down":
			m.moveQueryResultSelection(tab, 1)
		case "k", "up":
			m.moveQueryResultSelection(tab, -1)
		case "pgdown":
			m.moveQueryResultSelection(tab, m.queryResultViewportHeight(tab))
		case "pgup":
			m.moveQueryResultSelection(tab, -m.queryResultViewportHeight(tab))
		case "h", "left":
			tab.ResultView.ScrollColumns(-1, resultSetColumnCount(tab.Result))
			m.syncActiveTabState()
		case "l", "right":
			tab.ResultView.ScrollColumns(1, resultSetColumnCount(tab.Result))
			m.syncActiveTabState()
		case "i":
			tab.VimMode = vimModeNormal
			tab.WorkspaceFocus = workspaceFocusEditor
			tab.VimMode = vimModeInsert
		default:
			return true // swallow other keys while in visual mode
		}
		return true
	}

	prevG := tab.ResultPendingG
	tab.ResultPendingG = false
	switch msg.String() {
	case "ctrl+s":
		m.openQueryHistorySearchModal()
	case "ctrl+d":
		m.openDatabasePickerModal()
	case "i":
		tab.WorkspaceFocus = workspaceFocusEditor
		tab.VimMode = vimModeInsert
	case "v":
		if selectable {
			tab.VimMode = vimModeVisual
			tab.ResultCursorAnchor = tab.ResultCursorRow
			m.syncActiveTabState()
		}
	case "y":
		if selectable {
			tab.ResultCursorAnchor = tab.ResultCursorRow
			m.copyText(m.queryResultSelectionText(tab))
		}
	case "g":
		if selectable {
			if prevG {
				m.moveQueryResultSelection(tab, -1<<30)
			} else {
				tab.ResultPendingG = true
			}
		}
	case "G":
		if selectable {
			m.moveQueryResultSelection(tab, 1<<30)
		}
	case "enter":
		if selectable {
			m.openRowDetail(tab)
		}
	case "j", "down":
		if selectable {
			m.moveQueryResultSelection(tab, 1)
		} else {
			tab.ResultView.ScrollRows(1, resultSetRowCount(tab.Result))
			m.syncActiveTabState()
		}
	case "k", "up":
		if selectable {
			m.moveQueryResultSelection(tab, -1)
		} else {
			tab.ResultView.ScrollRows(-1, resultSetRowCount(tab.Result))
			m.syncActiveTabState()
		}
	case "pgdown":
		if selectable {
			m.moveQueryResultSelection(tab, m.queryResultViewportHeight(tab))
		} else {
			tab.ResultView.ScrollRows(10, resultSetRowCount(tab.Result))
			m.syncActiveTabState()
		}
	case "pgup":
		if selectable {
			m.moveQueryResultSelection(tab, -m.queryResultViewportHeight(tab))
		} else {
			tab.ResultView.ScrollRows(-10, resultSetRowCount(tab.Result))
			m.syncActiveTabState()
		}
	case "h", "left":
		tab.ResultView.ScrollColumns(-1, resultSetColumnCount(tab.Result))
		m.syncActiveTabState()
	case "l", "right":
		tab.ResultView.ScrollColumns(1, resultSetColumnCount(tab.Result))
		m.syncActiveTabState()
	default:
		return false
	}
	return true
}

// queryResultSelectionRows returns the ordered [start, end] row range currently
// selected in the table result list's row-visual mode.
func (m *Model) queryResultSelectionRows(tab *workspaceTab) (int, int) {
	start, end := tab.ResultCursorAnchor, tab.ResultCursorRow
	if start > end {
		start, end = end, start
	}
	if tab.Result.Table != nil {
		n := len(tab.Result.Table.Rows)
		start = clamp(start, 0, max(0, n-1))
		end = clamp(end, 0, max(0, n-1))
	}
	return start, end
}

// queryResultSelectionText renders the selected rows as TSV (tab-separated
// columns, newline-separated rows) so it pastes cleanly into a spreadsheet.
func (m *Model) queryResultSelectionText(tab *workspaceTab) string {
	t := tab.Result.Table
	if t == nil || len(t.Rows) == 0 {
		return ""
	}
	start, end := m.queryResultSelectionRows(tab)
	rows := make([]string, 0, end-start+1)
	for row := start; row <= end; row++ {
		cells := make([]string, len(t.Columns))
		for col := range t.Columns {
			cells[col] = t.CellString(row, col)
		}
		rows = append(rows, strings.Join(cells, "\t"))
	}
	return strings.Join(rows, "\n")
}

// moveQueryResultSelection moves the selected row in the query result list and
// keeps it within the visible window.
func (m *Model) moveQueryResultSelection(tab *workspaceTab, delta int) {
	if tab.Result.Table == nil {
		return
	}
	n := len(tab.Result.Table.Rows)
	if n == 0 {
		return
	}
	tab.ResultCursorRow = clamp(tab.ResultCursorRow+delta, 0, n-1)
	height := max(1, m.queryResultViewportHeight(tab))
	offset := tab.ResultView.RowOffset
	if tab.ResultCursorRow < offset {
		offset = tab.ResultCursorRow
	}
	if tab.ResultCursorRow >= offset+height {
		offset = tab.ResultCursorRow - height + 1
	}
	tab.ResultView.RowOffset = clamp(offset, 0, max(0, n-height))
	m.syncActiveTabState()
}

// openRowDetail opens the single-row detail subpage for the selected row.
func (m *Model) openRowDetail(tab *workspaceTab) {
	tab.RowDetail = true
	tab.ResultRow = 0
	tab.ResultColumn = 0
	tab.ResultAnchorRow = 0
	tab.ResultAnchorColumn = 0
	tab.ResultView.Reset()
	tab.VimMode = vimModeNormal
	m.syncActiveTabState()
}

// handleRowDetailKey drives the single-row detail subpage, reusing the data grid
// cursor/visual/copy machinery (which now reads the transposed detail lines).
func (m *Model) handleRowDetailKey(tab *workspaceTab, msg tea.KeyMsg) bool {
	switch msg.String() {
	case "esc", "q":
		tab.RowDetail = false
		tab.VimMode = vimModeNormal
		tab.WorkspaceFocus = workspaceFocusResult
		m.syncActiveTabState()
	case "y":
		if tab.VimMode == vimModeVisual {
			m.copyText(m.dataSelectionText(tab))
			tab.VimMode = vimModeNormal
		} else if tab.Result.Table != nil {
			t := tab.Result.Table
			// Copy the full value of the field the cursor's line belongs to.
			col := rowDetailColumnForLine(*t, tab.ResultCursorRow, tab.ResultRow)
			m.copyText(t.CellString(tab.ResultCursorRow, col))
		}
		m.syncActiveTabState()
	default:
		// Delegate movement/visual to the data grid; swallow everything else so
		// keys never leak to global handlers (e.g. d/x deleting a connection).
		if tab.VimMode == vimModeVisual {
			m.handleDataVisualKey(tab, msg)
		} else {
			m.handleDataNormalKey(tab, msg)
		}
	}
	return true
}

// queryResultViewportHeight estimates how many result rows are visible, used to
// keep the selected row on screen.
func (m *Model) queryResultViewportHeight(tab *workspaceTab) int {
	bufferLines := len(strings.Split(tab.QueryBuffer, "\n"))
	if tab.QueryBuffer == "" {
		bufferLines = 1
	}
	chrome := 1 /*db header*/ + 1 /*tab bar*/ + 1 /*input status*/ + bufferLines + 1 /*result bar*/
	if tab.QueryText != "" {
		chrome++ // statement line
	}
	return max(3, m.mainInnerHeight()-chrome)
}

func insertQueryText(tab *workspaceTab, text string) {
	text = sanitizeMultilineInput(text)
	if text == "" {
		return
	}
	tab.QueryCursor = clamp(tab.QueryCursor, 0, len(tab.QueryBuffer))
	pushQueryUndo(tab)
	tab.QueryBuffer = tab.QueryBuffer[:tab.QueryCursor] + text + tab.QueryBuffer[tab.QueryCursor:]
	tab.QueryCursor += len(text)
}

func pushQueryUndo(tab *workspaceTab) {
	if len(tab.QueryUndo) > 0 && tab.QueryUndo[len(tab.QueryUndo)-1] == tab.QueryBuffer {
		return
	}
	tab.QueryUndo = append(tab.QueryUndo, tab.QueryBuffer)
	if len(tab.QueryUndo) > 50 {
		tab.QueryUndo = tab.QueryUndo[len(tab.QueryUndo)-50:]
	}
}

func (m *Model) undoQueryEdit(tab *workspaceTab) {
	if len(tab.QueryUndo) == 0 {
		m.message = "nothing to undo"
		return
	}
	last := tab.QueryUndo[len(tab.QueryUndo)-1]
	tab.QueryUndo = tab.QueryUndo[:len(tab.QueryUndo)-1]
	tab.QueryBuffer = last
	tab.QueryCursor = clamp(tab.QueryCursor, 0, len(tab.QueryBuffer))
	tab.QuerySuggestionsVisible = false
}

func (m *Model) deleteQueryCurrentLine(tab *workspaceTab) {
	if tab.QueryBuffer == "" {
		return
	}
	pushQueryUndo(tab)
	start, end := queryCurrentLineRange(tab.QueryBuffer, tab.QueryCursor)
	tab.QueryClipboard = tab.QueryBuffer[start:end]
	tab.QueryBuffer = tab.QueryBuffer[:start] + tab.QueryBuffer[end:]
	tab.QueryBuffer = strings.TrimPrefix(tab.QueryBuffer, "\n")
	tab.QueryCursor = clamp(start, 0, len(tab.QueryBuffer))
	tab.QuerySuggestionsVisible = false
}

func (m *Model) pasteQueryClipboard(tab *workspaceTab) {
	if tab.QueryClipboard == "" {
		m.message = "query clipboard is empty"
		return
	}
	pushQueryUndo(tab)
	_, end := queryCurrentLineRange(tab.QueryBuffer, tab.QueryCursor)
	insertAt := end
	if insertAt < len(tab.QueryBuffer) && tab.QueryBuffer[insertAt] == '\n' {
		insertAt++
	}
	text := tab.QueryClipboard
	if insertAt > 0 && insertAt <= len(tab.QueryBuffer) && !strings.HasPrefix(text, "\n") {
		text = "\n" + strings.TrimRight(text, "\n")
	}
	tab.QueryBuffer = tab.QueryBuffer[:insertAt] + text + tab.QueryBuffer[insertAt:]
	tab.QueryCursor = insertAt
	if strings.HasPrefix(text, "\n") {
		tab.QueryCursor++
	}
}

func (m *Model) openQueryLine(tab *workspaceTab, before bool) {
	pushQueryUndo(tab)
	start, end := queryCurrentLineRange(tab.QueryBuffer, tab.QueryCursor)
	insertAt := end
	if before {
		insertAt = start
	} else if insertAt < len(tab.QueryBuffer) && tab.QueryBuffer[insertAt] == '\n' {
		insertAt++
	}
	text := "\n"
	if before {
		tab.QueryBuffer = tab.QueryBuffer[:insertAt] + text + tab.QueryBuffer[insertAt:]
	} else {
		tab.QueryBuffer = tab.QueryBuffer[:insertAt] + text + tab.QueryBuffer[insertAt:]
		insertAt++
	}
	tab.QueryCursor = clamp(insertAt, 0, len(tab.QueryBuffer))
	tab.VimMode = vimModeInsert
}

func queryNextWord(buffer string, cursor int) int {
	cursor = clamp(cursor, 0, len(buffer))
	for cursor < len(buffer) && isQueryWordByte(buffer[cursor]) {
		cursor++
	}
	for cursor < len(buffer) && !isQueryWordByte(buffer[cursor]) {
		cursor++
	}
	return clamp(cursor, 0, len(buffer))
}

func queryPreviousWord(buffer string, cursor int) int {
	cursor = clamp(cursor-1, 0, len(buffer))
	for cursor > 0 && !isQueryWordByte(buffer[cursor]) {
		cursor--
	}
	for cursor > 0 && isQueryWordByte(buffer[cursor-1]) {
		cursor--
	}
	return clamp(cursor, 0, len(buffer))
}

func queryLineStart(buffer string, cursor int) int {
	cursor = clamp(cursor, 0, len(buffer))
	idx := strings.LastIndex(buffer[:cursor], "\n")
	if idx < 0 {
		return 0
	}
	return idx + 1
}

func queryLineEnd(buffer string, cursor int) int {
	cursor = clamp(cursor, 0, len(buffer))
	idx := strings.Index(buffer[cursor:], "\n")
	if idx < 0 {
		return max(0, len(buffer)-1)
	}
	return max(0, cursor+idx-1)
}

func queryCurrentLineRange(buffer string, cursor int) (int, int) {
	if buffer == "" {
		return 0, 0
	}
	cursor = clamp(cursor, 0, len(buffer))
	start := queryLineStart(buffer, cursor)
	end := len(buffer)
	if idx := strings.Index(buffer[start:], "\n"); idx >= 0 {
		end = start + idx + 1
	}
	return start, end
}

func queryCurrentLineText(buffer string, cursor int) string {
	start, end := queryCurrentLineRange(buffer, cursor)
	return buffer[start:end]
}

func isQueryWordByte(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '$'
}

func (m *Model) moveQueryHistory(tab *workspaceTab, delta int) {
	entries := queryHistoryEntries(m.history, string(m.activeDriver()), 100)
	if len(entries) == 0 {
		m.message = "no query history"
		return
	}
	if tab.QueryHistoryIndex < 0 {
		tab.QueryHistoryDraft = tab.QueryBuffer
		if delta < 0 {
			return
		}
		tab.QueryHistoryIndex = 0
	} else {
		tab.QueryHistoryIndex += delta
	}
	if tab.QueryHistoryIndex < 0 {
		tab.QueryHistoryIndex = -1
		tab.QueryBuffer = tab.QueryHistoryDraft
		tab.QueryCursor = len(tab.QueryBuffer)
		return
	}
	tab.QueryHistoryIndex = clamp(tab.QueryHistoryIndex, 0, len(entries)-1)
	tab.QueryBuffer = entries[tab.QueryHistoryIndex].Statement
	tab.QueryCursor = len(tab.QueryBuffer)
	tab.QuerySuggestionsVisible = false
}

func (m *Model) refreshQuerySuggestions(tab *workspaceTab) {
	if tab == nil {
		return
	}
	tab.QueryCursor = clamp(tab.QueryCursor, 0, len(tab.QueryBuffer))
	// Only complete when nothing is glued to the right of the cursor: suppress
	// when the next character is non-whitespace (i.e. the cursor sits in the
	// middle of a token or right before a bracket/comma).
	if tab.QueryCursor < len(tab.QueryBuffer) {
		if r, _ := utf8.DecodeRuneInString(tab.QueryBuffer[tab.QueryCursor:]); !unicode.IsSpace(r) {
			tab.QuerySuggestionsVisible = false
			tab.QuerySuggestions = nil
			tab.QuerySuggestionIdx = 0
			return
		}
	}
	input := tab.QueryBuffer[:tab.QueryCursor]
	if strings.TrimSpace(input) == "" {
		tab.QuerySuggestionsVisible = false
		tab.QuerySuggestions = nil
		tab.QuerySuggestionIdx = 0
		return
	}
	database := tab.QueryDatabase
	if database == "" {
		database = m.selectedDB
	}
	fields := m.querySuggestionFields(database, input)
	tab.QuerySuggestions = suggest.Suggest(suggest.Context{
		Page:    string(PageQuery),
		Driver:  m.activeDriver(),
		Input:   input,
		Objects: m.querySuggestionObjects(database),
		Fields:  fields,
	})
	tab.QuerySuggestionIdx = 0
	tab.QuerySuggestionsVisible = len(tab.QuerySuggestions) > 0
}

func (m *Model) querySuggestionObjects(database string) []string {
	objects := m.databaseObjects[database]
	if len(objects) == 0 && database == m.selectedDB {
		objects = m.objects
	}
	names := make([]string, 0, len(objects))
	for _, object := range objects {
		if object.Name != "" {
			names = append(names, object.Name)
		}
	}
	return names
}

// knownDatabaseObject reports whether name is a table/collection we have already
// loaded for the database (case-insensitive).
func (m *Model) knownDatabaseObject(database, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	for _, object := range m.querySuggestionObjects(database) {
		if strings.ToLower(object) == name {
			return true
		}
	}
	return false
}

func (m *Model) querySuggestionFields(database, input string) []suggest.Field {
	target, ok := m.querySuggestionTarget(database, input)
	if !ok {
		return nil
	}
	// Only fetch metadata for tables/collections we already know exist. This
	// avoids probing partial identifiers while typing/deleting (e.g. "d") which
	// would spam "Unknown table" errors and block the UI on every keystroke.
	if !m.knownDatabaseObject(database, target.Name) {
		return nil
	}
	key := strings.Join([]string{string(m.activeDriver()), target.Database, target.Name}, "\x00")
	if m.queryFieldCache == nil {
		m.queryFieldCache = map[string][]suggest.Field{}
	}
	if fields, ok := m.queryFieldCache[key]; ok {
		return fields
	}
	provider, ok := m.adapter.(db.MetadataProvider)
	if !ok {
		return nil
	}
	ctx, cancel := m.dbContext(context.Background())
	defer cancel()
	metadata, err := provider.Metadata(ctx, target)
	if err != nil {
		// Suggestion metadata is best-effort: cache the miss and stay silent so a
		// failed probe never overwrites the status line.
		m.queryFieldCache[key] = nil
		return nil
	}
	fields := make([]suggest.Field, 0, len(metadata.Fields))
	for _, field := range metadata.Fields {
		if field.Name != "" {
			fields = append(fields, suggest.Field{Name: field.Name, Type: field.Type})
		}
	}
	m.queryFieldCache[key] = fields
	return fields
}

func (m *Model) querySuggestionTarget(database, input string) (db.Target, bool) {
	switch m.activeDriver() {
	case config.DriverMongo:
		collection := mongoCollectionNameFromQuery(input)
		if collection == "" {
			return db.Target{}, false
		}
		return db.Target{Database: database, Name: collection, Type: db.ObjectCollection}, true
	case config.DriverMySQL, config.DriverDoris:
		table := sqlTableNameFromQuery(input)
		if table == "" {
			return db.Target{}, false
		}
		return db.Target{Database: database, Name: table, Type: db.ObjectTable}, true
	default:
		return db.Target{}, false
	}
}

func mongoCollectionNameFromQuery(input string) string {
	idx := strings.LastIndex(input, "db.")
	if idx < 0 {
		return ""
	}
	rest := input[idx+len("db."):]
	var b strings.Builder
	for i := 0; i < len(rest); i++ {
		ch := rest[i]
		if isQueryWordByte(ch) {
			b.WriteByte(ch)
			continue
		}
		break
	}
	return b.String()
}

func sqlTableNameFromQuery(input string) string {
	normalized := strings.NewReplacer(",", " ", ";", " ", "(", " ", ")", " ", "\n", " ", "\t", " ").Replace(input)
	tokens := strings.Fields(normalized)
	table := ""
	for i := 0; i < len(tokens)-1; i++ {
		switch strings.ToLower(tokens[i]) {
		case "from", "join", "update", "into":
			table = strings.Trim(tokens[i+1], "`\"")
		}
	}
	return table
}

func (m *Model) moveQuerySuggestion(tab *workspaceTab, delta int) {
	if len(tab.QuerySuggestions) == 0 {
		tab.QuerySuggestionsVisible = false
		return
	}
	tab.QuerySuggestionIdx = (tab.QuerySuggestionIdx + delta) % len(tab.QuerySuggestions)
	if tab.QuerySuggestionIdx < 0 {
		tab.QuerySuggestionIdx += len(tab.QuerySuggestions)
	}
}

func (m *Model) acceptQuerySuggestion(tab *workspaceTab) bool {
	if tab == nil || !tab.QuerySuggestionsVisible || len(tab.QuerySuggestions) == 0 {
		return false
	}
	tab.QuerySuggestionIdx = clamp(tab.QuerySuggestionIdx, 0, len(tab.QuerySuggestions)-1)
	selected := tab.QuerySuggestions[tab.QuerySuggestionIdx]
	if selected.Value == "" {
		return false
	}
	tab.QueryCursor = clamp(tab.QueryCursor, 0, len(tab.QueryBuffer))
	prefix := replaceCurrentToken(tab.QueryBuffer[:tab.QueryCursor], selected.Value)
	suffix := tab.QueryBuffer[tab.QueryCursor:]
	tab.QueryBuffer = prefix + suffix
	tab.QueryCursor = len(prefix)
	tab.QuerySuggestionsVisible = false
	tab.QuerySuggestions = nil
	return true
}

// querySelectionRange returns an inclusive [start, end] byte range snapped to
// rune boundaries: start is a rune's first byte, end is a rune's LAST byte, so
// slicing buffer[start:end+1] never splits a multi-byte UTF-8 character.
func querySelectionRange(tab *workspaceTab) (int, int) {
	buffer := tab.QueryBuffer
	start := tab.SelectionAnchor
	end := tab.QueryCursor
	if start > end {
		start, end = end, start
	}
	start = clamp(start, 0, max(0, len(buffer)-1))
	end = clamp(end, 0, max(0, len(buffer)-1))
	// Snap start back to the rune it falls inside.
	for start > 0 && !utf8.RuneStart(buffer[start]) {
		start--
	}
	// Snap end forward to the last byte of the rune it starts.
	for end < len(buffer) && !utf8.RuneStart(buffer[end]) {
		end--
	}
	if end < len(buffer) {
		_, size := utf8.DecodeRuneInString(buffer[end:])
		end += size - 1
	}
	return start, end
}
