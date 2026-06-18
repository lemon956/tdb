package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/history"
	"tdb/internal/result"
	"tdb/internal/suggest"
)

type workspaceTabKind string

const (
	workspaceTabData  workspaceTabKind = "data"
	workspaceTabQuery workspaceTabKind = "query"
)

type vimMode string

const (
	vimModeNormal vimMode = "normal"
	vimModeInsert vimMode = "insert"
	vimModeVisual vimMode = "visual"
)

type workspaceFocus string

const (
	workspaceFocusEditor workspaceFocus = "query_editor"
	workspaceFocusResult workspaceFocus = "result"
)

type workspaceTab struct {
	ID                      string
	Kind                    workspaceTabKind
	Title                   string
	Target                  db.Target
	Mode                    workspaceMode
	Result                  result.Set
	ResultView              ResultView
	QueryText               string
	QueryDatabase           string
	QueryCatalog            string // Doris catalog the query runs against (empty = internal)
	QueryDatabasePinned     bool   // true once the user picks a database explicitly (stops auto-follow)
	QueryWarning            string // non-blocking syntax/character lint message
	QueryBuffer             string
	QueryCursor             int
	QueryHistoryIndex       int
	QueryHistoryDraft       string
	QuerySuggestions        []suggest.Suggestion
	QuerySuggestionIdx      int
	QuerySuggestionsVisible bool
	QueryPendingOp          string
	QueryClipboard          string
	QueryUndo               []string
	SelectionAnchor         int
	VimMode                 vimMode
	WorkspaceFocus          workspaceFocus
	ResultRow               int
	ResultColumn            int
	ResultAnchorRow         int
	ResultAnchorColumn      int
	ResultCursorRow         int  // selected row in a query result list
	ResultCursorAnchor      int  // anchor row for the result list's row-visual selection
	ResultPendingG          bool // a leading "g" was pressed in the result/data grid (awaiting "gg")
	RowDetail               bool // single-row detail subpage open
	PreviewOffset           int  // data-preview pagination offset
	PreviewHasMore          bool // another preview page is available
	Status                  string
	Error                   string
	CreatedAtNano           int64
}

func (m *Model) activeWorkspaceTab() *workspaceTab {
	if len(m.workspaceTabs) == 0 {
		m.activeTabIndex = 0
		return nil
	}
	m.activeTabIndex = clamp(m.activeTabIndex, 0, len(m.workspaceTabs)-1)
	return &m.workspaceTabs[m.activeTabIndex]
}

func (m *Model) findWorkspaceTab(id string) int {
	for i, tab := range m.workspaceTabs {
		if tab.ID == id {
			return i
		}
	}
	return -1
}

func (m *Model) openDataWorkspaceTab(target db.Target, set result.Set, mode workspaceMode) {
	id := dataWorkspaceTabID(m.activeProfileID(), target)
	idx := m.findWorkspaceTab(id)
	if idx < 0 {
		m.workspaceTabs = append(m.workspaceTabs, workspaceTab{
			ID:            id,
			Kind:          workspaceTabData,
			CreatedAtNano: time.Now().UnixNano(),
		})
		idx = len(m.workspaceTabs) - 1
	}
	tab := &m.workspaceTabs[idx]
	tab.Kind = workspaceTabData
	tab.Target = target
	tab.Title = target.String()
	tab.Mode = mode
	tab.Result = set
	tab.ResultView.Reset()
	tab.QueryText = ""
	tab.QueryBuffer = ""
	tab.QueryCursor = 0
	tab.SelectionAnchor = 0
	tab.VimMode = vimModeNormal
	tab.WorkspaceFocus = workspaceFocusResult
	tab.PreviewOffset = 0
	tab.PreviewHasMore = false
	tab.Status = ""
	tab.Error = ""
	m.activeTabIndex = idx
	m.focus = FocusMain // focus follows to the opened data window
	if target.Database != "" {
		m.selectedDB = target.Database
	}
	m.syncActiveTabState()
	m.highlightSidebarForActiveTab()
}

func dataWorkspaceTabID(profileID string, target db.Target) string {
	return strings.Join([]string{
		"data",
		profileID,
		target.Database,
		target.Schema,
		target.Name,
		string(target.Type),
	}, ":")
}

func (m *Model) openQueryWorkspaceTab() {
	m.nextQueryTabID++
	title := "Query"
	if m.nextQueryTabID > 1 {
		title = fmt.Sprintf("Query %d", m.nextQueryTabID)
	}
	// Bind to the current sidebar selection (may be empty); the tab then
	// auto-follows the sidebar until the user pins a database via the picker.
	database := m.selectedDB
	tab := workspaceTab{
		ID:                fmt.Sprintf("query:%d", m.nextQueryTabID),
		Kind:              workspaceTabQuery,
		Title:             title,
		QueryDatabase:     database,
		QueryCatalog:      m.selectedCatalog,
		QueryHistoryIndex: -1,
		VimMode:           vimModeInsert,
		WorkspaceFocus:    workspaceFocusEditor,
		CreatedAtNano:     time.Now().UnixNano(),
	}
	m.workspaceTabs = append(m.workspaceTabs, tab)
	m.activeTabIndex = len(m.workspaceTabs) - 1
	m.focus = FocusMain
	m.message = "query tab opened"
	// Load the database's table/collection list (once) so completion can suggest
	// table names after FROM/JOIN instead of falling back to functions/keywords.
	m.ensureQueryObjectsLoaded(m.selectedCatalog, database)
	m.syncActiveTabState()
	m.highlightSidebarForActiveTab()
}

// ensureQueryObjectsLoaded fetches the object list for database if it has not been
// loaded yet, so the completion engine has table/collection names to offer.
func (m *Model) ensureQueryObjectsLoaded(catalog, database string) {
	if database == "" || m.adapter == nil {
		return
	}
	m.ensureNavigationState()
	if _, ok := m.databaseObjects[m.scopeKey(catalog, database)]; ok {
		return
	}
	_ = m.loadObjectsForDatabase(context.Background(), catalog, database)
}

func (m *Model) moveWorkspaceTab(delta int) {
	if len(m.workspaceTabs) == 0 {
		m.message = "no workspace tab"
		return
	}
	m.activeTabIndex = (m.activeTabIndex + delta) % len(m.workspaceTabs)
	if m.activeTabIndex < 0 {
		m.activeTabIndex += len(m.workspaceTabs)
	}
	m.syncActiveTabState()
	m.highlightSidebarForActiveTab()
	m.message = "tab " + m.workspaceTabs[m.activeTabIndex].label(false)
}

func (m *Model) closeActiveWorkspaceTab() {
	if len(m.workspaceTabs) == 0 {
		m.message = "no workspace tab"
		return
	}
	m.activeTabIndex = clamp(m.activeTabIndex, 0, len(m.workspaceTabs)-1)
	closed := m.workspaceTabs[m.activeTabIndex].label(false)
	m.workspaceTabs = append(m.workspaceTabs[:m.activeTabIndex], m.workspaceTabs[m.activeTabIndex+1:]...)
	if m.activeTabIndex >= len(m.workspaceTabs) {
		m.activeTabIndex = len(m.workspaceTabs) - 1
	}
	if len(m.workspaceTabs) == 0 {
		m.activeTabIndex = 0
		m.target = db.Target{}
		m.result = result.Set{}
		m.resultView.Reset()
		m.workspaceMode = ""
		if m.activeProfile != nil {
			m.page = PageBrowser
			m.focus = FocusSidebar
		}
		m.message = "closed " + closed
		return
	}
	m.syncActiveTabState()
	m.highlightSidebarForActiveTab()
	m.message = "closed " + closed
}

func (m *Model) handleWorkspaceTabShortcut(msg tea.KeyMsg) bool {
	if m.isTextEntryMode() {
		return false
	}
	switch msg.String() {
	case "ctrl+right":
		// Ctrl+H/Ctrl+L now switch panels (sidebar ↔ workspace); workspace-tab
		// navigation stays on Ctrl+Left/Ctrl+Right.
		m.moveWorkspaceTab(1)
		return true
	case "ctrl+left":
		m.moveWorkspaceTab(-1)
		return true
	case "ctrl+w":
		// Cascade: close the active workspace tab; once a connection has no
		// workspace tabs left, Ctrl+W closes the connection session itself.
		if len(m.workspaceTabs) > 0 {
			m.closeActiveWorkspaceTab()
		} else if m.activeSession >= 0 {
			m.closeConnectionSession(m.activeSession)
		}
		return true
	default:
		return false
	}
}

func (m *Model) syncActiveTabState() {
	tab := m.activeWorkspaceTab()
	if tab == nil {
		return
	}
	m.result = tab.Result
	m.resultView = tab.ResultView
	switch tab.Kind {
	case workspaceTabData:
		m.target = tab.Target
		m.workspaceMode = tab.Mode
		m.page = PageData
	case workspaceTabQuery:
		m.workspaceMode = ""
		m.page = PageQuery
	}
}

func (m *Model) syncActiveTabFromModel() {
	tab := m.activeWorkspaceTab()
	if tab == nil {
		return
	}
	tab.ResultView = m.resultView
	switch tab.Kind {
	case workspaceTabData:
		tab.Target = m.target
		tab.Mode = m.workspaceMode
		tab.Result = m.result
	case workspaceTabQuery:
		tab.Result = m.result
	}
}

func (m *Model) workspaceContent() string {
	tab := m.activeWorkspaceTab()
	if tab == nil {
		return m.browserWorkspaceContent()
	}
	width := m.workspaceContentWidth()
	var b strings.Builder
	b.WriteString(m.workspaceTabBar(width))
	b.WriteString("\n")
	switch tab.Kind {
	case workspaceTabData:
		b.WriteString(m.workspaceDataContent(*tab))
	case workspaceTabQuery:
		b.WriteString(m.workspaceQueryContent(*tab))
	}
	return b.String()
}

func (m *Model) workspaceContentWidth() int {
	if m.width <= 0 {
		return 80
	}
	return max(20, m.width-m.sidebarWidth()-6)
}

// workspaceDatabaseHeader renders the active database name on the line above the
// workspace tab bar.
// workspaceDatabaseName returns the database the active workspace tab is bound
// to (used for the inline tab-bar indicator and the database picker).
func (m *Model) workspaceDatabaseName() string {
	database, catalog := "", ""
	if tab := m.activeWorkspaceTab(); tab != nil {
		database = tab.QueryDatabase
		catalog = tab.QueryCatalog
	}
	if database == "" {
		database = m.selectedDB
		catalog = m.selectedCatalog
	}
	if database == "" && m.activeProfile != nil {
		database = m.activeProfile.Database
		catalog = ""
	}
	if database == "" {
		return "-"
	}
	if cat := normalizedCatalog(catalog); cat != "" {
		return cat + "." + database
	}
	return database
}

// workspaceDatabaseLabel is the styled `▸ <db>` segment shown on the tab-bar row.
func (m *Model) workspaceDatabaseLabel() string {
	return defaultTheme().accent.Render("▸ " + m.workspaceDatabaseName())
}

// workspaceTabLayout computes the shared geometry of the tab strip so the
// renderer and the click hitboxes agree. cellWidth is the (equal) width of each
// tab cell; dbX is the column (relative to the workspace content start) where
// the `▸ <db>` label begins, placed directly after the tab strip.
func (m *Model) workspaceTabLayout(width int) (cellWidth, dbX int) {
	n := len(m.workspaceTabs)
	if n == 0 {
		return 0, 0
	}
	if width <= 0 {
		width = 80
	}
	maxLabel := 4
	for _, tab := range m.workspaceTabs {
		if w := lipgloss.Width(tab.label(false)); w > maxLabel {
			maxLabel = w
		}
	}
	dbSeg := lipgloss.Width("  ▸ " + m.workspaceDatabaseName())
	// Tab strip occupies n*cellWidth + n columns (cells + separators incl. the
	// trailing one). Cap the cell so the strip plus the db label still fit; +2
	// gives the longest label breathing room before it gets truncated.
	maxCell := (width - dbSeg - n) / n
	cellWidth = clamp(maxLabel+2, 4, max(4, maxCell))
	stripWidth := n*cellWidth + n
	dbX = stripWidth + 1 // one space before the db label
	return cellWidth, dbX
}

func (m *Model) workspaceTabBar(width int) string {
	if len(m.workspaceTabs) == 0 {
		return ""
	}
	if width <= 0 {
		width = 80
	}
	cellWidth, _ := m.workspaceTabLayout(width)
	theme := defaultTheme()
	cells := make([]string, 0, len(m.workspaceTabs))
	for i, tab := range m.workspaceTabs {
		hideDatabase := cellWidth < lipgloss.Width(tab.label(false))+2
		label := tab.label(hideDatabase)
		cell := fitTabCell(label, cellWidth)
		style := theme.muted
		if tab.Kind == workspaceTabQuery {
			style = style.Foreground(theme.tabQueryColor)
		} else {
			style = style.Foreground(theme.tabDataColor)
		}
		if i == m.activeTabIndex {
			style = style.Bold(true).Foreground(theme.tabActiveFg).Background(theme.tabActiveBg)
		}
		cells = append(cells, style.Render(cell))
	}
	// Compact tab strip, then the database label directly after it.
	bar := strings.Join(cells, "|") + "|" + " " + m.workspaceDatabaseLabel()
	if pad := width - lipgloss.Width(bar); pad > 0 {
		bar += strings.Repeat(" ", pad)
	}
	return bar
}

func (tab workspaceTab) label(hideDatabase bool) string {
	switch tab.Kind {
	case workspaceTabQuery:
		if tab.Title != "" {
			return tab.Title
		}
		return "Query"
	default:
		if hideDatabase && tab.Target.Name != "" {
			return tab.Target.Name
		}
		if tab.Target.String() != "" {
			return tab.Target.String()
		}
		if tab.Title != "" {
			return tab.Title
		}
		return "Data"
	}
}

func fitTabCell(label string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(label) > width {
		label = cellSlice(label, 0, width)
	}
	return padCells(label, width)
}

func (m *Model) workspaceDataContent(tab workspaceTab) string {
	title := "Data"
	if tab.Mode == workspaceMetadata {
		title = "Metadata"
	}
	var b strings.Builder
	mode := tab.VimMode
	if mode == "" {
		mode = vimModeNormal
	}
	b.WriteString(m.workspaceCursor(workspaceFocusResult, tab) + title + "  " + strings.ToUpper(string(mode)) + "  " + string(workspaceFocusResult) + "\n")
	b.WriteString(fmt.Sprintf("cursor row:%d col:%d\n", tab.ResultRow+1, tab.ResultColumn+1))
	if mode == vimModeVisual {
		startRow, startCol, endRow, endCol := orderedDataSelection(tab)
		b.WriteString(fmt.Sprintf("selection %d:%d-%d:%d\n", startRow+1, startCol+1, endRow+1, endCol+1))
	}
	b.WriteString(title + ": " + tab.Target.String() + "\n")
	headerLines := 3
	if mode == vimModeVisual {
		headerLines = 4
	}
	if tab.Mode != workspaceMetadata {
		n := resultRowCount(tab.Result)
		more := ""
		if tab.PreviewHasMore {
			more = " +more"
		}
		status := fmt.Sprintf("rows %d-%d%s  ·  ]/[ page", tab.PreviewOffset+1, tab.PreviewOffset+n, more)
		// Clip to the content width so a narrow pane never overflows.
		status = cellSlice(status, 0, m.workspaceContentWidth())
		b.WriteString(defaultTheme().muted.Render(status) + "\n")
		headerLines++
	}
	b.WriteString(m.renderDataResultViewport(tab, m.workspaceContentWidth(), m.workspaceResultViewportHeight(headerLines)))
	return b.String()
}

func (m *Model) workspaceResultViewportHeight(headerLines int) int {
	height := m.height
	if height <= 0 {
		height = 30
	}
	commandHeight := 0
	if m.commandVisible() {
		commandHeight = 1
	}
	if m.commandVisible() && m.focus == FocusCommand && m.input.SuggestionsVisible() {
		commandHeight += commandSuggestionHeight(m)
	}
	bodyHeight := max(3, height-2-commandHeight)
	mainInnerHeight := max(1, bodyHeight-2)
	workspaceChromeHeight := 2 // tab bar + spacer
	return max(1, mainInnerHeight-workspaceChromeHeight-headerLines)
}

func (m *Model) workspaceQueryContent(tab workspaceTab) string {
	var b strings.Builder
	title := tab.Title
	if title == "" {
		title = "Query"
	}
	theme := defaultTheme()
	width := max(20, m.workspaceContentWidth())

	bufferPrefix := m.workspaceCursor(workspaceFocusEditor, tab)
	inner := cellSlice(m.queryStatusLine(title, tab), 0, width) + "\n" +
		m.queryBufferContent(bufferPrefix, tab)
	// Render the input region as a color-tinted full-width block (no border) so
	// it is visually distinct from the surrounding panel while preserving the
	// column anchoring of the inline suggestion popup.
	b.WriteString(theme.queryInput.Width(width).Render(strings.TrimRight(inner, "\n")) + "\n")

	if tab.QueryWarning != "" {
		b.WriteString(theme.statusWarn.Render("⚠ "+tab.QueryWarning) + "\n")
	}
	if tab.QueryText != "" {
		b.WriteString(theme.muted.Render("Statement: "+tab.QueryText) + "\n")
	}
	if tab.Error != "" {
		// Bound the width so the long backend error wraps inside the border
		// instead of overflowing and fragmenting it.
		b.WriteString(theme.danger.Width(clamp(width-4, 20, width)).Render("Error: "+tab.Error) + "\n")
		return b.String()
	}
	if !hasResultSet(tab.Result) {
		if tab.Status == "ok" && tab.QueryText != "" {
			b.WriteString(m.queryEmptyResultMessage(tab))
		}
		return b.String()
	}
	b.WriteString(m.queryResultBar(tab) + "\n")
	if tab.RowDetail {
		// Single-row detail: transposed field/value grid with cursor, visual
		// selection, copy and horizontal scrolling (via the data viewport).
		b.WriteString(m.renderDataResultViewport(tab, m.workspaceContentWidth(), m.queryResultViewportHeight(&tab)))
		return b.String()
	}
	if tab.Result.Table == nil {
		// Non-table results (Redis scalar / JSON value) reuse the data grid so the
		// char cursor and visual selection ("copy mode") render the same as the
		// sidebar data-browse page.
		b.WriteString(m.renderDataResultViewport(tab, m.workspaceContentWidth(), m.queryResultViewportHeight(&tab)))
		return b.String()
	}
	view := tab.ResultView
	if view.Height == 0 {
		view.Height = 12
	}
	if view.Width == 0 {
		view.Width = 8
	}
	view.MaxWidth = m.workspaceContentWidth()
	if len(tab.Result.Table.Rows) > 0 {
		view.Selectable = m.focus == FocusMain && tab.WorkspaceFocus == workspaceFocusResult
		// Always mark the current row so its position stays visible (dim when the
		// result pane is not focused, bright when it is).
		view.MarkCursor = true
		view.SelectedRow = tab.ResultCursorRow
		if view.Selectable && tab.VimMode == vimModeVisual {
			view.SelectionActive = true
			view.SelectionStart, view.SelectionEnd = m.queryResultSelectionRows(&tab)
		}
	}
	b.WriteString(view.Render(tab.Result))
	return b.String()
}

// queryResultBar renders a full-width header bar that separates the result block
// from the input box above it.
func (m *Model) queryResultBar(tab workspaceTab) string {
	label := "Results"
	if tab.RowDetail && tab.Result.Table != nil {
		total := len(tab.Result.Table.Rows)
		label = fmt.Sprintf("Row %d/%d  ·  Esc back · h/l scroll · y copy", tab.ResultCursorRow+1, total)
	} else if m.focus == FocusMain && tab.WorkspaceFocus == workspaceFocusResult {
		selectable := tab.Result.Table != nil && len(tab.Result.Table.Rows) > 0
		switch {
		case selectable && tab.VimMode == vimModeVisual:
			start, end := m.queryResultSelectionRows(&tab)
			label = fmt.Sprintf("VISUAL  %d rows  ·  y copy · Esc cancel", end-start+1)
		case selectable:
			label = "Results ◀  Enter: view row · v select · y copy · gg/G top/bottom"
		default:
			label = "Results ◀  v select · y copy · gg/G top/bottom"
		}
	}
	if tab.Result.Truncated {
		label += fmt.Sprintf("  ⚠ capped at %d rows (refine your query)", resultRowCount(tab.Result))
	}
	width := max(8, m.workspaceContentWidth()-1)
	return defaultTheme().resultBar.Width(width).Render("▌ " + label)
}

// resultRowCount returns how many rows/documents a result set currently holds.
func resultRowCount(set result.Set) int {
	if set.Table != nil {
		return len(set.Table.Rows)
	}
	return len(set.Documents)
}

func (m *Model) queryEmptyResultMessage(tab workspaceTab) string {
	var msg string
	switch m.activeDriver() {
	case config.DriverMongo:
		msg = "No documents returned."
	case config.DriverRedis:
		msg = "No value returned."
	default:
		msg = "No rows returned."
	}
	return m.queryResultBar(tab) + "\n" + defaultTheme().muted.Render(msg) + "\n"
}

func (m *Model) queryStatusLine(title string, tab workspaceTab) string {
	mode := tab.VimMode
	if mode == "" {
		mode = vimModeNormal
	}
	database := tab.QueryDatabase
	if database == "" {
		database = m.selectedDB
	}
	driver := "-"
	instance := title
	if m.activeProfile != nil {
		driver = string(m.activeProfile.Driver)
		instance = m.activeProfile.ID
	}
	if database == "" {
		database = "-"
	}
	// Keep this plain text: the status line is truncated by cellSlice, which is
	// not ANSI-aware, so styled escape sequences would be corrupted.
	return fmt.Sprintf("%s  db:%s  %s/%s   Enter run · Ctrl+J newline · Ctrl+S history · Tab complete", strings.ToUpper(string(mode)), database, driver, instance)
}

// queryInputPlaceholder is the dim hint shown inside an empty query input row so
// it is visually obvious where to type.
func queryInputPlaceholder() string {
	return defaultTheme().muted.Render("Type a query…")
}

// queryBufferContent renders only the query text itself. The suggestion popup
// is composited separately as a floating overlay (querySuggestionOverlay) so it
// never pushes the rows below the cursor down.
func (m *Model) queryBufferContent(prefix string, tab workspaceTab) string {
	rendered := m.renderQueryBuffer(tab)
	runLines := queryRunButtonLines(tab.QueryBuffer)
	if rendered == "" {
		return prefix
	}
	lines := strings.Split(rendered, "\n")
	var b strings.Builder
	b.WriteString(queryEditorLinePrefix(prefix, runLines[0]) + lines[0])
	for idx, line := range lines[1:] {
		linePrefix := queryEditorLinePrefix("  ", runLines[idx+1])
		b.WriteString("\n" + linePrefix + line)
	}
	return b.String()
}

func queryEditorLinePrefix(base string, runnable bool) string {
	if runnable {
		return base + "[>] "
	}
	return base + "    "
}

func (m *Model) renderQueryBuffer(tab workspaceTab) string {
	if tab.QueryBuffer == "" {
		if m.focus == FocusMain && tab.WorkspaceFocus == workspaceFocusEditor {
			return m.renderCursorCell(" ") + queryInputPlaceholder()
		}
		return queryInputPlaceholder()
	}
	cursor := clamp(tab.QueryCursor, 0, len(tab.QueryBuffer))
	theme := defaultTheme()
	visualStart, visualEnd := -1, -1
	if tab.VimMode == vimModeVisual {
		visualStart, visualEnd = querySelectionRange(&tab)
	}
	var b strings.Builder
	buffer := tab.QueryBuffer
	showCursor := m.focus == FocusMain && tab.WorkspaceFocus == workspaceFocusEditor
	// Syntax classes drive coloring; precedence is visual selection > cursor cell
	// > token color > plain text.
	classes := highlightClasses(m.activeDriver(), buffer)
	// Walk by rune so multi-byte UTF-8 (e.g. CJK) is emitted whole; slicing
	// per-byte would write broken UTF-8 fragments and corrupt the terminal.
	for pos := 0; pos < len(buffer); {
		if visualStart >= 0 && pos >= visualStart && pos <= visualEnd {
			end := min(visualEnd+1, len(buffer))
			b.WriteString(theme.visual.Render(buffer[pos:end]))
			pos = end
			continue
		}
		_, size := utf8.DecodeRuneInString(buffer[pos:])
		ch := buffer[pos : pos+size]
		switch {
		case showCursor && pos == cursor:
			b.WriteString(m.renderCursorCell(ch))
		default:
			if style, ok := m.synStyle(theme, classes[pos]); ok {
				b.WriteString(style.Render(ch))
			} else {
				b.WriteString(ch)
			}
		}
		pos += size
	}
	if m.focus == FocusMain && tab.WorkspaceFocus == workspaceFocusEditor && cursor == len(tab.QueryBuffer) {
		b.WriteString(m.renderCursorCell(" "))
	}
	return b.String()
}

// querySuggestionBoxBody renders the suggestion list (up to 5 rows, selected row
// highlighted) as a uniform-width block, returning the block and its display
// width. It carries no anchor padding; placement is handled by the overlay.
func (m *Model) querySuggestionBoxBody(tab workspaceTab, maxWidth int) (string, int) {
	items := tab.QuerySuggestions
	const visible = 8
	total := len(items)
	// Window the list around the selected index so the highlight stays on screen
	// past the first page instead of "falling out" of the popup.
	offset := 0
	if total > visible {
		offset = clamp(tab.QuerySuggestionIdx-visible/2, 0, total-visible)
	}
	end := min(total, offset+visible)

	popupWidth := 0
	for idx := offset; idx < end; idx++ {
		popupWidth = max(popupWidth, lipgloss.Width(querySuggestionPopupLabel(items[idx])))
	}
	popupWidth = clamp(popupWidth+2, 8, max(8, maxWidth))

	theme := defaultTheme()
	var b strings.Builder
	writeLine := func(s string) {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(s)
	}
	if offset > 0 {
		writeLine(theme.muted.Render(fitDataTableCell(fmt.Sprintf(" ▲ %d", offset), popupWidth)))
	}
	for idx := offset; idx < end; idx++ {
		cell := renderSuggestionRow(theme, items[idx], popupWidth-2, idx == tab.QuerySuggestionIdx)
		writeLine(" " + cell + " ")
	}
	if end < total {
		writeLine(theme.muted.Render(fitDataTableCell(fmt.Sprintf(" ▼ %d", total-end), popupWidth)))
	}
	return b.String(), popupWidth
}

// renderSuggestionRow renders one completion cell of exactly width display cells:
// the value (default colour) followed by its type detail in a lighter (muted)
// colour. The selected row is highlighted uniformly. width is the inner cell width.
func renderSuggestionRow(theme appTheme, item suggest.Suggestion, width int, selected bool) string {
	if width <= 0 {
		return ""
	}
	value := item.Label
	if value == "" {
		value = item.Value
	}
	detail := item.Detail
	gap := ""
	if detail != "" {
		gap = "  "
	}
	suffixW := lipgloss.Width(gap) + lipgloss.Width(detail)
	maxVal := width - suffixW
	if maxVal < 1 {
		// Detail alone fills the cell; show a clipped value only.
		plain := fitDataTableCell(value, width)
		if selected {
			return theme.selected.Render(plain)
		}
		return plain
	}
	if lipgloss.Width(value) > maxVal {
		if maxVal <= 1 {
			value = cellSlice(value, 0, maxVal)
		} else {
			value = cellSlice(value, 0, maxVal-1) + "…"
		}
	}
	pad := max(0, width-lipgloss.Width(value)-suffixW)
	if selected {
		return theme.selected.Render(value + gap + detail + strings.Repeat(" ", pad))
	}
	return value + gap + theme.muted.Render(detail) + strings.Repeat(" ", pad)
}

func querySuggestionPopupLabel(item suggest.Suggestion) string {
	label := item.Label
	if label == "" {
		label = item.Value
	}
	if item.Detail != "" {
		label += "  " + item.Detail
	}
	return label
}

func queryCursorRow(buffer string, cursor int) int {
	row, _ := queryCursorPosition(buffer, cursor)
	return row
}

// queryCursorVisualPos returns the cursor's VISUAL row/column inside the rendered
// editor block, accounting for the soft-wrap the queryInput block applies at
// `width`. This keeps the suggestion popup anchored under the cursor even when a
// long query line wraps onto several screen rows.
func (m *Model) queryCursorVisualPos(tab workspaceTab, width int) (int, int) {
	if width < 1 {
		width = 1
	}
	buffer := tab.QueryBuffer
	row, col := queryCursorPosition(buffer, tab.QueryCursor)
	runLines := queryRunButtonLines(buffer)
	lines := strings.Split(buffer, "\n")
	prefixWidth := func(line int) int {
		base := "  "
		if line == 0 {
			base = m.workspaceCursor(workspaceFocusEditor, tab)
		}
		return lipgloss.Width(queryEditorLinePrefix(base, runLines[line]))
	}
	vrow := 0
	for l := 0; l < row && l < len(lines); l++ {
		lineW := prefixWidth(l) + lipgloss.Width(lines[l])
		vrow += max(1, (lineW+width-1)/width) // ceil
	}
	off := prefixWidth(row) + col
	vrow += off / width
	return vrow, off % width
}

func queryCursorPosition(buffer string, cursor int) (int, int) {
	cursor = clamp(cursor, 0, len(buffer))
	row := 0
	col := 0
	// Count display columns (CJK counts as 2), not bytes, so the suggestion
	// popup anchor lines up under the cursor.
	for _, r := range buffer[:cursor] {
		if r == '\n' {
			row++
			col = 0
			continue
		}
		col += lipgloss.Width(string(r))
	}
	return row, col
}

type queryStatement struct {
	Text  string
	Start int
	End   int
	Line  int
}

func queryExecutableStatements(buffer string) []queryStatement {
	statements := []queryStatement{}
	start := 0
	line := 0
	for idx := 0; idx <= len(buffer); idx++ {
		endOfStatement := idx == len(buffer)
		if idx < len(buffer) && buffer[idx] == ';' {
			endOfStatement = true
		}
		if !endOfStatement {
			if idx < len(buffer) && buffer[idx] == '\n' {
				line++
			}
			continue
		}
		text := strings.TrimSpace(buffer[start:idx])
		if text != "" {
			statements = append(statements, queryStatement{Text: text, Start: start, End: min(idx+1, len(buffer)), Line: line})
		}
		if idx < len(buffer) && buffer[idx] == ';' {
			idx++
			for idx < len(buffer) && (buffer[idx] == '\n' || buffer[idx] == ' ' || buffer[idx] == '\t') {
				if buffer[idx] == '\n' {
					line++
				}
				idx++
			}
			start = idx
			idx--
		}
	}
	return statements
}

func queryRunButtonLines(buffer string) map[int]bool {
	lines := map[int]bool{}
	for _, statement := range queryExecutableStatements(buffer) {
		lines[statement.Line] = true
	}
	return lines
}

func (m *Model) workspaceCursor(target workspaceFocus, tab workspaceTab) string {
	if m.focus == FocusMain && tab.WorkspaceFocus == target {
		return "> "
	}
	return ""
}

func (m *Model) executeQueryInActiveTab(ctx context.Context, line string) {
	tab := m.activeWorkspaceTab()
	if tab == nil || tab.Kind != workspaceTabQuery {
		m.handleQuery(ctx, line)
		return
	}
	if m.adapter == nil {
		m.message = "no active connection"
		return
	}
	database := tab.QueryDatabase
	if database == "" {
		database = m.selectedDB
	}
	catalog := tab.QueryCatalog
	if catalog == "" {
		catalog = m.selectedCatalog
	}
	// Clear the input immediately so the spinner animates over an empty editor
	// while the query runs in the background.
	tab.QueryText = line
	tab.QueryDatabase = database
	tab.QueryCatalog = catalog
	tab.QueryBuffer = ""
	tab.QueryCursor = 0
	tab.QuerySuggestionsVisible = false
	tab.QuerySuggestions = nil
	tab.Error = ""
	tab.Status = ""
	tabID := tab.ID
	adapter := m.adapter
	profileID := m.activeProfileID()
	driver := string(m.activeDriver())
	start := time.Now()
	m.syncActiveTabState()
	m.nextCmd = m.runAsync("Running query…", func(ctx context.Context) tea.Msg {
		res, err := adapter.Execute(ctx, db.Command{Text: line, Catalog: catalog, Database: database})
		dur := time.Since(start)
		return asyncResultMsg{apply: func(m *Model) {
			m.applyQueryResult(tabID, line, database, profileID, driver, res, err, dur, start)
		}}
	})
}

func (m *Model) applyQueryResult(tabID, line, database, profileID, driver string, res result.Set, err error, dur time.Duration, start time.Time) {
	// A user-cancelled query is not a failure: don't flash an error box or log it.
	if errors.Is(err, context.Canceled) {
		m.message = "query cancelled"
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("query timed out after %s — use `:timeout <seconds>` to extend (0 = none) or press Esc to cancel", m.queryTimeout)
	}
	status := history.StatusOK
	errText := ""
	if idx := m.findWorkspaceTab(tabID); idx >= 0 {
		tab := &m.workspaceTabs[idx]
		if err != nil {
			tab.Error = err.Error()
		} else {
			// Document results (mongo find/aggregate) stay as Documents so they
			// render as pretty JSON in the data viewport — the same style as the
			// sidebar data-browse page — with a char cursor and v/y copy.
			tab.Result = res
			tab.ResultView.Reset()
			tab.ResultCursorRow = 0
			tab.ResultCursorAnchor = 0
			tab.ResultRow = 0
			tab.ResultColumn = 0
			tab.ResultAnchorRow = 0
			tab.ResultAnchorColumn = 0
			tab.RowDetail = false
			tab.Error = ""
			tab.Status = "ok"
			tab.VimMode = vimModeNormal
			tab.WorkspaceFocus = workspaceFocusResult
			m.focus = FocusMain
		}
	}
	if err != nil {
		status = history.StatusError
		errText = err.Error()
		m.showErrorBox("Query failed", err)
	} else {
		m.message = "query executed"
	}
	m.recordHistory(history.Entry{
		ID:             strconv.FormatInt(start.UnixNano(), 10),
		ProfileID:      profileID,
		Driver:         driver,
		Database:       database,
		Action:         history.ActionQuery,
		Statement:      line,
		Status:         status,
		Error:          errText,
		DurationMillis: dur.Milliseconds(),
		StartedAt:      start.UTC(),
	})
	m.syncActiveTabState()
}

func (m *Model) executeQueryStatementInActiveTab(ctx context.Context, index int) {
	tab := m.activeWorkspaceTab()
	if tab == nil || tab.Kind != workspaceTabQuery {
		return
	}
	statements := queryExecutableStatements(tab.QueryBuffer)
	if index < 0 || index >= len(statements) {
		m.message = "query statement not found"
		return
	}
	statement := statements[index]
	original := tab.QueryBuffer
	m.executeQueryLine(ctx, tab, statement.Text, tab.QueryDatabase)
	tab.QueryBuffer = strings.TrimLeft(original[:statement.Start]+original[statement.End:], " \t\n")
	tab.QueryCursor = clamp(statement.Start, 0, len(tab.QueryBuffer))
	tab.QuerySuggestionsVisible = false
	tab.QuerySuggestions = nil
	m.syncActiveTabState()
}

func (m *Model) executeQueryLine(ctx context.Context, tab *workspaceTab, line, database string) {
	if m.adapter == nil {
		m.message = "no active connection"
		return
	}
	if database == "" {
		database = m.selectedDB
	}
	catalog := tab.QueryCatalog
	if catalog == "" {
		catalog = m.selectedCatalog
	}
	start := time.Now()
	opCtx, cancel := m.dbContext(ctx)
	res, err := m.adapter.Execute(opCtx, db.Command{Text: line, Catalog: catalog, Database: database})
	cancel()
	status := history.StatusOK
	errText := ""
	tab.QueryText = line
	tab.QueryDatabase = database
	tab.QueryCatalog = catalog
	if err != nil {
		status = history.StatusError
		errText = err.Error()
		tab.Error = errText
		m.showErrorBox("Query failed", err)
	} else {
		tab.Result = res
		tab.ResultView.Reset()
		tab.Error = ""
		tab.Status = "ok"
		tab.VimMode = vimModeNormal
		tab.WorkspaceFocus = workspaceFocusResult
		m.focus = FocusMain
		m.message = "query executed"
	}
	m.recordHistory(history.Entry{
		ID:             strconv.FormatInt(time.Now().UnixNano(), 10),
		ProfileID:      m.activeProfileID(),
		Driver:         string(m.activeDriver()),
		Database:       database,
		Action:         history.ActionQuery,
		Statement:      line,
		Status:         status,
		Error:          errText,
		DurationMillis: time.Since(start).Milliseconds(),
		StartedAt:      start.UTC(),
	})
}

func (m *Model) activeWorkspaceResultAvailable() bool {
	tab := m.activeWorkspaceTab()
	return tab != nil && hasResultSet(tab.Result)
}

func (m *Model) scrollActiveWorkspaceRows(delta int) bool {
	tab := m.activeWorkspaceTab()
	if tab == nil || !hasResultSet(tab.Result) {
		return false
	}
	tab.ResultView.ScrollRows(delta, resultSetRowCount(tab.Result))
	m.syncActiveTabState()
	return true
}

func (m *Model) scrollActiveWorkspaceColumns(delta int) bool {
	tab := m.activeWorkspaceTab()
	if tab == nil || !hasResultSet(tab.Result) {
		return false
	}
	tab.ResultView.ScrollColumns(delta, resultSetColumnCount(tab.Result))
	m.syncActiveTabState()
	return true
}

func hasResultSet(set result.Set) bool {
	return set.Table != nil || len(set.Documents) > 0 || set.Value != nil
}

func resultSetRowCount(set result.Set) int {
	if set.Table != nil {
		return len(set.Table.Rows)
	}
	if len(set.Documents) > 0 {
		return len(set.Documents)
	}
	if set.Value != nil {
		return 1
	}
	return 0
}

func resultSetColumnCount(set result.Set) int {
	if set.Table != nil {
		return len(set.Table.Columns)
	}
	return 0
}
