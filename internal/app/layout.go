package app

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"tdb/internal/config"
)

type Focus string

const (
	FocusSidebar     Focus = "sidebar"
	FocusMain        Focus = "main"
	FocusContext     Focus = "context"
	FocusCommand     Focus = "command"
	FocusSuggestions Focus = "suggestions"
)

func (m *Model) renderLayout() string {
	theme := defaultTheme()
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height
	if height <= 0 {
		height = 30
	}

	commandVisible := m.commandVisible()
	commandSuggestionVisible := commandVisible && m.focus == FocusCommand && m.input.SuggestionsVisible()
	header := renderHeader(theme, m, width)
	commandHeight := 0
	if commandVisible {
		commandHeight = 1
	}
	if commandSuggestionVisible {
		commandHeight += commandSuggestionHeight(m)
	}
	bodyHeight := max(3, height-2-commandHeight)
	sidebarWidth := m.sidebarWidth()
	mainWidth := width - sidebarWidth
	if mainWidth < 34 {
		mainWidth = 34
	}

	sidebarContentHeight := max(1, bodyHeight-2)
	sidebarContentWidth := max(8, sidebarWidth-4)
	sidebar := renderPanel(theme, "", m.sidebarContent(sidebarContentHeight, sidebarContentWidth), FocusSidebar, m.focus == FocusSidebar, sidebarWidth, bodyHeight)
	main := renderPanel(theme, "", m.mainContent(), FocusMain, m.focus == FocusMain, mainWidth, bodyHeight)
	footerY := bodyHeight + 1 + commandHeight
	m.registerLayoutHitboxes(sidebarWidth, mainWidth, bodyHeight, footerY)
	panels := []string{sidebar, main}

	parts := []string{header, lipgloss.JoinHorizontal(lipgloss.Top, panels...)}
	if commandVisible {
		if commandSuggestionVisible {
			parts = append(parts, renderCommandSuggestions(theme, m, width))
		}
		parts = append(parts, theme.command.Width(width).Render(m.commandLineText()))
	}
	footer := renderFooter(theme, m, width)
	parts = append(parts, footer)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// sidebarWidth returns the current left-panel width in cells. When the user has
// dragged the divider (sidebarWidthOverride > 0) it honors that, clamped to a
// usable range; otherwise it falls back to the automatic 1/5 proportion.
func (m *Model) sidebarWidth() int {
	width := m.width
	if width <= 0 {
		width = 100
	}
	if m.sidebarWidthOverride > 0 {
		return clamp(m.sidebarWidthOverride, 16, max(16, width-30))
	}
	return clamp(width/5, 22, 32)
}

// setSidebarWidth records a manual divider position; sidebarWidth clamps it.
func (m *Model) setSidebarWidth(w int) {
	m.sidebarWidthOverride = w
}

func (m *Model) registerLayoutHitboxes(sidebarWidth, mainWidth, bodyHeight, footerY int) {
	boxes := HitboxRegistry{
		{ID: "panel-sidebar", X: 0, Y: 1, Width: sidebarWidth, Height: bodyHeight, Focus: FocusSidebar},
		{ID: "panel-main", X: sidebarWidth, Y: 1, Width: mainWidth, Height: bodyHeight, Focus: FocusMain},
	}
	// Header row (Y=0): connection tabs after "TDB", then a trailing "+".
	hx := lipgloss.Width(defaultTheme().header.Render("TDB"))
	for i := range m.sessions {
		w := lipgloss.Width(m.connTabText(i))
		boxes = append(boxes, Hitbox{ID: fmt.Sprintf("conn-tab:%d", i), X: hx, Y: 0, Width: w, Height: 1, Index: i})
		hx += w
	}
	boxes = append(boxes, Hitbox{ID: "conn-add", X: hx, Y: 0, Width: lipgloss.Width(connAddText), Height: 1})
	mainX := sidebarWidth
	if m.errBox != nil {
		boxes = append(boxes, Hitbox{ID: "error:close", X: mainX + 4, Y: 8, Width: 10, Height: 1, Focus: FocusContext, Action: actionCancel})
	}
	if m.helpOpen {
		boxes = append(boxes, Hitbox{ID: "help:close", X: mainX + 4, Y: 14, Width: 10, Height: 1, Focus: FocusContext, Action: actionCancel})
	}
	if m.form != nil {
		boxes = append(boxes, m.connectionFormHitboxes(mainX, mainWidth)...)
	}
	if m.pending != nil {
		boxes = append(boxes,
			Hitbox{ID: "confirm-ok", X: mainX + 4, Y: 8, Width: 10, Height: 1, Focus: FocusMain, Action: actionConfirm},
			Hitbox{ID: "confirm-cancel", X: mainX + 16, Y: 8, Width: 10, Height: 1, Focus: FocusMain, Action: actionCancel},
		)
	}
	if m.modal != nil && m.form == nil && m.pending == nil && m.errBox == nil {
		boxes = append(boxes, Hitbox{ID: "modal:close", X: mainX + 4, Y: 8, Width: 10, Height: 1, Focus: FocusContext, Action: actionCancel})
	}
	if tab := m.activeWorkspaceTab(); tab != nil {
		// Tab-bar row (content row 0). Cells are clickable to switch tabs and the
		// `▸ <db>` label is clickable to open the database picker.
		contentX := mainX + 2
		cellWidth, dbX := m.workspaceTabLayout(m.workspaceContentWidth())
		for i := range m.workspaceTabs {
			boxes = append(boxes, Hitbox{
				ID:     fmt.Sprintf("workspace-tab:%d", i),
				X:      contentX + i*(cellWidth+1),
				Y:      2,
				Width:  cellWidth,
				Height: 1,
				Focus:  FocusMain,
				Index:  i,
			})
		}
		dbLabel := "▸ " + m.workspaceDatabaseName()
		boxes = append(boxes, Hitbox{
			ID:     "workspace-db",
			X:      contentX + dbX,
			Y:      2,
			Width:  max(1, lipgloss.Width(dbLabel)),
			Height: 1,
			Focus:  FocusMain,
		})
		if tab.Kind == workspaceTabQuery {
			for idx, statement := range queryExecutableStatements(tab.QueryBuffer) {
				boxes = append(boxes, Hitbox{
					ID:     fmt.Sprintf("query-run:%d", idx),
					X:      mainX + 2,
					Y:      3 + statement.Line,
					Width:  4,
					Height: 1,
					Focus:  FocusMain,
				})
			}
		}
	}
	// Inner content starts after the border row; panel titles are hidden.
	y := 2
	switch m.page {
	case PageConnections:
		for i := range m.vault.Profiles {
			boxes = append(boxes, Hitbox{
				ID:     fmt.Sprintf("connection:%d", i),
				X:      1,
				Y:      y + i,
				Width:  max(1, sidebarWidth-2),
				Height: 1,
				Focus:  FocusSidebar,
				Index:  i,
			})
		}
	case PageBrowser, PageData, PageQuery:
		visible, _ := m.visibleBrowserNodes(max(1, bodyHeight-2))
		for row, node := range visible {
			index := node.DatabaseIdx
			if node.Kind == navNodeObject || node.Kind == navNodeMeta {
				index = node.ObjectIdx
			}
			boxes = append(boxes, Hitbox{
				ID:     node.ID,
				X:      1,
				Y:      y + row,
				Width:  max(1, sidebarWidth-2),
				Height: 1,
				Focus:  FocusSidebar,
				Index:  index,
			})
		}
	case PageHistory:
		for i := range historyEntries(m.history, m.activeProfileID()) {
			boxes = append(boxes, Hitbox{
				ID:     fmt.Sprintf("history:%d", i),
				X:      1,
				Y:      y + i,
				Width:  max(1, sidebarWidth-2),
				Height: 1,
				Focus:  FocusSidebar,
				Index:  i,
			})
		}
	}
	m.hitboxes = boxes
}

// connTabText is the plain text of a connection chip (id + driver), kept in
// sync between the renderer and the click hitboxes.
func (m *Model) connTabText(i int) string {
	s := m.sessions[i]
	return fmt.Sprintf(" %s · %s ", s.profile.ID, s.profile.Driver)
}

const connAddText = " + "

func renderHeader(theme appTheme, m *Model, width int) string {
	segs := []string{theme.header.Render("TDB")}
	if len(m.sessions) == 0 {
		segs = append(segs, theme.status.Render(" no open connection "))
	}
	for i := range m.sessions {
		style := theme.status
		if i == m.activeSession {
			style = lipgloss.NewStyle().Bold(true).Foreground(theme.tabActiveFg).Background(theme.tabActiveBg).Padding(0, 0)
		}
		segs = append(segs, style.Render(m.connTabText(i)))
	}
	addStyle := theme.status
	if m.activeSession < 0 {
		addStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.tabActiveFg).Background(theme.tabActiveBg)
	}
	segs = append(segs, addStyle.Render(connAddText))
	line := lipgloss.JoinHorizontal(lipgloss.Top, segs...)
	return lipgloss.NewStyle().Width(width).Render(line)
}

func renderPanel(theme appTheme, title, body string, focus Focus, active bool, width, height int) string {
	style := theme.panel.Width(max(10, width-2)).Height(max(3, height-2))
	switch focus {
	case FocusSidebar:
		style = style.Inherit(theme.sidebar)
	case FocusMain:
		style = style.Inherit(theme.main)
	case FocusContext:
		style = style.Inherit(theme.context)
	}
	if active {
		style = style.Inherit(theme.focused)
		switch focus {
		case FocusSidebar:
			style = style.Background(theme.sidebarFocusBg).BorderForeground(theme.sidebarFocusEdge)
		case FocusMain:
			style = style.Background(theme.mainFocusBg).BorderForeground(theme.mainFocusEdge)
		}
	}
	content := trimToHeight(body, max(1, height-2))
	return style.Render(content)
}

func (m *Model) sidebarContent(height, width int) string {
	switch m.page {
	case PageConnections:
		return m.connectionsList()
	case PageBrowser, PageData, PageQuery:
		return m.browserListVisible(height, width)
	case PageHistory:
		return m.historyList()
	default:
		return "Connections\nBrowser\nData\nQuery\nHistory"
	}
}

func (m *Model) mainContent() string {
	var b strings.Builder
	if m.errBox != nil {
		return m.errorBoxContent()
	}
	if m.form != nil {
		return m.modalContent()
	}
	if m.pending != nil {
		return m.modalContent()
	}
	if m.input.SuggestionsVisible() && m.focus != FocusCommand {
		m.viewSuggestions(&b)
		return b.String()
	}
	if m.modal != nil && m.modal.Kind == modalQueryHistorySearch {
		if overlay, ok := m.queryHistorySearchOverlay(); ok {
			return overlay
		}
		return m.modalContent()
	}
	if m.modal != nil && m.modal.Kind == modalDatabasePicker {
		if overlay, ok := m.databasePickerOverlay(); ok {
			return overlay
		}
		return m.modalContent()
	}
	if m.modal != nil || m.helpOpen {
		return m.modalContent()
	}
	if len(m.workspaceTabs) > 0 {
		if tab := m.activeWorkspaceTab(); tab != nil && tab.Kind == workspaceTabQuery &&
			tab.QuerySuggestionsVisible && len(tab.QuerySuggestions) > 0 {
			if overlay, ok := m.querySuggestionOverlay(); ok {
				return overlay
			}
		}
		return m.workspaceContent()
	}
	switch m.page {
	case PageUnlock:
		theme := defaultTheme()
		b.WriteString(theme.sectionTitle.Render("Unlock config") + "\n")
		if m.unlockCommand {
			b.WriteString("Command: " + m.input.Value() + m.renderCursorCell(" ") + "\n")
			b.WriteString(theme.muted.Render("Only :q is available before unlocking. Esc to go back.") + "\n")
		} else {
			b.WriteString("Password: " + strings.Repeat("*", len(m.input.Value())) + m.renderCursorCell(" ") + "\n")
			b.WriteString(theme.muted.Render("Press : then type q to quit.") + "\n")
		}
	case PageConnections:
		b.WriteString(m.connectionWorkspaceContent())
	case PageBrowser:
		b.WriteString(m.browserWorkspaceContent())
	case PageData:
		m.viewData(&b)
	case PageQuery:
		b.WriteString(defaultTheme().sectionTitle.Render("Query editor") + "\n")
		b.WriteString(defaultTheme().muted.Render("Type : for a command, or open a query tab.") + "\n")
	case PageHistory:
		m.viewHistory(&b)
	default:
		m.viewHelp(&b)
	}
	return b.String()
}

func (m *Model) connectionWorkspaceContent() string {
	theme := defaultTheme()
	if len(m.vault.Profiles) == 0 {
		return theme.muted.Render("No connections.  Press n to create one.")
	}
	profile, ok := m.selectedProfile()
	if !ok {
		return theme.muted.Render("No connection selected.")
	}
	access := theme.badgeRW.Render(" RW ")
	if profile.ReadOnly {
		access = theme.badgeRO.Render(" RO ")
	}
	row := func(label, value string) string {
		return theme.muted.Render(fmt.Sprintf("%-10s", label)) + " " + value + "\n"
	}
	var b strings.Builder
	b.WriteString(theme.sectionTitle.Render(profile.ID) + "  " + access + "\n\n")
	b.WriteString(row("Driver", string(profile.Driver)))
	b.WriteString(row("Host", fmt.Sprintf("%s:%d", profile.Host, profile.Port)))
	b.WriteString(row("Database", valueOrDash(profile.Database)))
	return b.String()
}

func valueOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (m *Model) browserWorkspaceContent() string {
	profile := "none"
	if m.activeProfile != nil {
		profile = m.activeProfile.ID
	}
	if m.target.Name != "" {
		mode := "Data"
		if m.workspaceMode == workspaceMetadata {
			mode = "Metadata"
		}
		return fmt.Sprintf("%s: %s", mode, m.target.String())
	}
	theme := defaultTheme()
	return theme.muted.Render("Connection ") + theme.accent.Render(profile) + "\n\n" +
		theme.muted.Render("Open a database from the navigation tree.")
}

func (m *Model) confirmContent() string {
	if m.pending == nil {
		return ""
	}
	theme := defaultTheme()
	return theme.danger.Render("Confirm " + m.pending.Kind + "\n\n" + pendingSummary(m.pending) + "\n\n[ Confirm ]  [ Cancel ]")
}

func (m *Model) contextContent() string {
	var b strings.Builder
	if m.errBox != nil {
		return m.errorBoxContent()
	}
	if m.form != nil {
		return m.connectionFormContent()
	}
	if m.pending != nil {
		b.WriteString(defaultTheme().danger.Render("Confirm "+m.pending.Kind) + "\n")
		b.WriteString(pendingSummary(m.pending) + "\n")
		b.WriteString("\n[ Confirm ]  [ Cancel ]\n")
		b.WriteString("Type yes/no or click Confirm/Cancel.\n")
		return b.String()
	}
	if m.input.SuggestionsVisible() {
		m.viewSuggestions(&b)
		return b.String()
	}
	if m.helpOpen {
		return m.helpPanelContent()
	}
	b.WriteString("Focus: " + string(m.focus) + "\n")
	b.WriteString("Press : then type help for commands.\n")
	if m.target.Name != "" {
		b.WriteString("\nTarget\n")
		b.WriteString(m.target.String() + "\n")
	}
	return b.String()
}

func (m *Model) commandVisible() bool {
	if m.page == PageUnlock {
		return false
	}
	// The query history search and database picker popups carry their own
	// Search: field, so keep the bottom command bar hidden to avoid echoing it.
	if m.modal != nil && (m.modal.Kind == modalQueryHistorySearch || m.modal.Kind == modalDatabasePicker) {
		return false
	}
	return m.navSearchActive || m.focus == FocusCommand || m.input.Value() != "" || m.input.SuggestionsVisible()
}

func (m *Model) commandLineText() string {
	if m.page == PageUnlock {
		return "Password: " + strings.Repeat("*", len(m.input.Value())) + m.renderCursorCell(" ")
	}
	if m.navSearchActive {
		return "Search: " + m.input.Value() + m.renderCursorCell(" ")
	}
	if m.modal != nil && m.modal.Kind == modalHelp {
		return "Help search: " + m.input.Value() + m.renderCursorCell(" ")
	}
	if m.form != nil {
		if m.form.selectingDriver {
			return "Form: choose driver with arrows, Enter to continue, Esc to cancel"
		}
		if field, ok := m.form.currentField(); ok {
			return "Form: " + field.Label + " = " + displayFieldValue(*field) + m.renderCursorCell(" ")
		}
		return "Form: readonly checkbox, Space toggles, Enter saves"
	}
	return "Command: " + m.input.Value() + m.renderCursorCell(" ")
}

func (m *Model) connectionFormContent() string {
	var b strings.Builder
	theme := defaultTheme()
	title := "New connection"
	if m.form.mode == "edit" {
		title = "Edit connection"
	}
	b.WriteString(theme.accent.Render(title) + "\n")
	if m.form.selectingDriver {
		b.WriteString("Choose driver\n")
		for i, driver := range connectionFormDrivers {
			line := "  " + string(driver)
			if i == m.form.driverIndex {
				line = theme.selected.Render("> " + string(driver))
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n[ Cancel ]\n")
		return b.String()
	}
	b.WriteString("Driver: " + string(m.form.driver) + "\n")
	for i, field := range m.form.fields {
		if i == m.form.fieldIndex {
			// Active field: show an inline input cursor at the editing position so
			// it is obvious which field is being edited and where the caret is.
			b.WriteString("> " + field.Label + ": " + m.renderFormFieldValue(field, true) + "\n")
			continue
		}
		b.WriteString("  " + field.Label + ": " + m.renderFormFieldValue(field, false) + "\n")
	}
	readonly := "[ ] Readonly"
	if m.form.readOnly {
		readonly = "[x] Readonly"
	}
	if m.form.fieldIndex >= len(m.form.fields) {
		readonly = theme.selected.Render("> " + readonly)
	} else {
		readonly = "  " + readonly
	}
	b.WriteString(readonly + "\n")
	b.WriteString("\n[ Save ]  [ Cancel ]\n")
	return b.String()
}

func (m *Model) helpPanelContent() string {
	groups := []struct {
		Title string
		Items []string
	}{
		{"Global", []string{
			"?: open searchable help",
			":: open global command line",
			":q: quit the program (the only way to exit)",
			"Tab: switch Navigation and Workspace only",
			"help: open searchable help",
			"history: current connection history",
			"query: create a query tab",
			"q or Esc: back / close (does not quit)",
		}},
		{"Navigation", []string{
			"j/k or Up/Down: move selection",
			"Enter: expand database or open selected object",
			"/: search navigation tree",
			"Shift+h/l or Shift+Left/Right: horizontal scroll",
			"Mouse click: focus row, second click opens it",
		}},
		{"Workspace Tabs", []string{
			"Ctrl+Left / Ctrl+h: previous tab",
			"Ctrl+Right / Ctrl+l: next tab",
			"Ctrl+w: close current tab",
			"Data tabs show collection/table/key previews",
			"Query tabs keep their statement and result",
		}},
		{"Connections", []string{
			"new: create connection in a modal",
			"edit: edit selected connection in a modal",
			"delete: confirm deletion in a modal",
			"open <id>: open connection",
			"test <id>: test connection",
		}},
		{"Data", []string{
			"r or refresh: reload current object",
			"v: enter visual copy mode",
			"y: copy current cell or visual selection",
			"h/j/k/l or arrows: move result cursor",
			"i/d/x/delete: rejected in read-only data tabs",
		}},
		{"Query", []string{
			"Enter in command line: execute in current query tab",
			"Ctrl+Space: show statement suggestions",
			"Tab: accept selected suggestion",
			"SQL/Doris: SQL keywords",
			"Redis: Redis commands",
			"Mongo: JSON query fragments",
		}},
		{"Results & export", []string{
			"next / prev: page through data (offset)",
			"] / [: next / previous page in the data view",
			"export csv|json [path]: write result to a file",
			"copy csv|json: copy the whole result to the clipboard",
			"Esc: cancel a running query",
			"timeout <seconds>: query timeout (0 = none)",
		}},
	}
	query := strings.ToLower(strings.TrimSpace(m.helpSearch))
	var b strings.Builder
	b.WriteString("Command help\n")
	b.WriteString("[ Close ]\n")
	if query != "" {
		b.WriteString("Filter: " + query + "\n")
	}
	for _, group := range groups {
		if !helpGroupMatches(group.Title, group.Items, query) {
			continue
		}
		b.WriteString("\n" + defaultTheme().sectionTitle.Render(group.Title) + "\n")
		for _, item := range group.Items {
			if query != "" && !strings.Contains(strings.ToLower(group.Title+" "+item), query) {
				continue
			}
			b.WriteString("  " + item + "\n")
		}
	}
	return b.String()
}

func helpGroupMatches(title string, items []string, query string) bool {
	if query == "" {
		return true
	}
	if strings.Contains(strings.ToLower(title), query) {
		return true
	}
	for _, item := range items {
		if strings.Contains(strings.ToLower(item), query) {
			return true
		}
	}
	return false
}

func (m *Model) errorBoxContent() string {
	if m.errBox == nil {
		return ""
	}
	theme := defaultTheme()
	return theme.danger.Render(m.errBox.Title + "\n\n" + m.errBox.Message + "\n\n[ Close ]")
}

func displayFieldValue(field connectionFormField) string {
	if field.Secret && field.Value != "" {
		return strings.Repeat("*", len(field.Value))
	}
	return field.Value
}

// renderFormFieldValue renders a form field's value, masking secrets per-rune and
// (when active) drawing the editing caret at the cursor position.
func (m *Model) renderFormFieldValue(field connectionFormField, active bool) string {
	cursor := clamp(field.Cursor, 0, len(field.Value))
	var b strings.Builder
	for pos, r := range field.Value {
		ch := string(r)
		if field.Secret {
			ch = "*"
		}
		if active && pos == cursor {
			b.WriteString(m.renderCursorCell(ch))
		} else {
			b.WriteString(ch)
		}
	}
	if active && cursor == len(field.Value) {
		b.WriteString(m.renderCursorCell(" "))
	}
	return b.String()
}

func (m *Model) connectionFormHitboxes(contextX, contextWidth int) HitboxRegistry {
	x := contextX + 2
	width := max(8, contextWidth-4)
	boxes := HitboxRegistry{}
	if m.form.selectingDriver {
		for i, driver := range connectionFormDrivers {
			boxes = append(boxes, Hitbox{
				ID:      "form-driver:" + string(driver),
				X:       x,
				Y:       4 + i,
				Width:   width,
				Height:  1,
				Focus:   FocusContext,
				Payload: string(driver),
			})
		}
		boxes = append(boxes, Hitbox{ID: "form:cancel", X: x, Y: 6 + len(connectionFormDrivers), Width: 12, Height: 1, Focus: FocusContext, Action: actionCancel})
		return boxes
	}
	for i, field := range m.form.fields {
		boxes = append(boxes, Hitbox{
			ID:      "form-field:" + field.Name,
			X:       x,
			Y:       5 + i,
			Width:   width,
			Height:  1,
			Focus:   FocusContext,
			Index:   i,
			Payload: field.Name,
		})
	}
	controlsY := 6 + len(m.form.fields)
	boxes = append(boxes,
		Hitbox{ID: "form-readonly", X: x, Y: controlsY - 1, Width: width, Height: 1, Focus: FocusContext},
		Hitbox{ID: "form:save", X: x, Y: controlsY + 1, Width: 10, Height: 1, Focus: FocusContext, Action: actionConfirm},
		Hitbox{ID: "form:cancel", X: x + 12, Y: controlsY + 1, Width: 12, Height: 1, Focus: FocusContext, Action: actionCancel},
	)
	return boxes
}

// renderConnectionRow renders one connection uniformly for both the connections
// list and the navigation tree root: a brand-colored driver ICON followed by
// plain (uncolored) id/endpoint/lock text. Only the icon carries the brand color
// so the row text stays readable, including on the selection highlight. prefix is
// placed before the icon (the tree chevron for the nav root, "" for the list).
func (m *Model) renderConnectionRow(profile config.Profile, prefix string, width int, selected, focused bool) string {
	theme := defaultTheme()
	glyph, color := m.icons.DriverIcon(profile.Driver)
	indicator := glyph
	if indicator == "" {
		indicator = string(profile.Driver) // no brand glyph in this style → name
	}
	text := profile.ID
	if ep := connectionEndpoint(profile); ep != "" {
		text += " " + ep
	}
	if profile.ReadOnly {
		text += " " + m.icons.Lock
	}

	if selected {
		body := "> " + prefix + indicator + " " + text
		if width > 0 {
			body = padCells(body, width)
		}
		if focused {
			return theme.selected.Render(body)
		}
		return theme.selectedDim.Render(body)
	}

	lead := indicator
	if glyph != "" && color != "" {
		lead = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(glyph)
	}
	line := "  " + prefix + lead + " " + text
	if width > 0 {
		line = padCells(line, width)
	}
	return line
}

// connectionEndpoint returns a uniform "host:port" endpoint for a profile so every
// connection row reads the same way (mongo's host comes from its URI instead of
// the empty host:port that produced the inconsistent ":0").
func connectionEndpoint(profile config.Profile) string {
	if profile.Host != "" {
		return fmt.Sprintf("%s:%d", profile.Host, profile.Port)
	}
	if profile.URIParams != "" {
		if u, err := url.Parse(profile.URIParams); err == nil && u.Host != "" {
			return u.Host
		}
	}
	return ""
}

func (m *Model) connectionsList() string {
	if len(m.vault.Profiles) == 0 {
		return defaultTheme().muted.Render("no saved profiles")
	}
	var b strings.Builder
	for i, profile := range m.vault.Profiles {
		b.WriteString(m.renderConnectionRow(profile, "", 0, i == m.connectionIndex, m.focus == FocusSidebar) + "\n")
	}
	return b.String()
}

func (m *Model) browserList() string {
	return m.renderBrowserNodes(m.browserNodes(), 0, 0, false)
}

func (m *Model) browserListVisible(height, width int) string {
	nodes := m.browserNodes()
	if len(nodes) == 0 {
		return defaultTheme().muted.Render("no objects")
	}
	scrollWidth := 0
	if len(nodes) > height {
		scrollWidth = 1
	}
	rowWidth := max(1, width-scrollWidth-2)
	maxLineWidth := maxNodeLineWidth(nodes)
	m.navHorizontalOffset = clamp(m.navHorizontalOffset, 0, max(0, maxLineWidth-rowWidth))
	horizontal := m.navHorizontalOffset > 0 || maxLineWidth > rowWidth
	rowHeight := height
	if horizontal && rowHeight > 1 {
		rowHeight--
	}
	visible, offset := m.visibleBrowserNodes(rowHeight)
	body := m.renderBrowserNodes(visible, offset, rowWidth, len(nodes) > rowHeight)
	if horizontal {
		body = strings.TrimRight(body, "\n") + "\n" + horizontalScrollbar(width, m.navHorizontalOffset, maxLineWidth, rowWidth)
	}
	return body
}

func (m *Model) renderBrowserNodes(nodes []navNode, offset, width int, verticalScrollbar bool) string {
	var b strings.Builder
	theme := defaultTheme()
	if len(nodes) == 0 {
		return defaultTheme().muted.Render("no objects")
	}
	thumb := -1
	if verticalScrollbar && len(nodes) > 0 {
		thumb = clamp(m.browserCursor-offset, 0, len(nodes)-1)
	}
	for i, node := range nodes {
		selected := offset+i == m.browserCursor
		var line string
		if node.Kind == navNodeConnection && m.activeProfile != nil {
			// The connection root is rendered with the shared connection row so the
			// nav tree and the connections list look identical (colored icon, plain
			// text). The chevron is the prefix; selection is handled inside.
			line = m.renderConnectionRow(*m.activeProfile, m.icons.Expanded+" ", width, selected, m.focus == FocusSidebar)
		} else {
			line = strings.Repeat("  ", node.Depth) + node.Label
			if width > 0 {
				line = cellSlice(line, m.navHorizontalOffset, width)
				line = padCells(line, width)
			}
			line = sidebarCursorLine(theme, line, selected, m.focus == FocusSidebar)
		}
		if verticalScrollbar {
			bar := "│"
			if i == thumb {
				bar = "┃"
			}
			line += bar
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func maxNodeLineWidth(nodes []navNode) int {
	width := 0
	for _, node := range nodes {
		width = max(width, lipgloss.Width(strings.Repeat("  ", node.Depth)+node.Label))
	}
	return width
}

func cellSlice(value string, offset, width int) string {
	if width <= 0 {
		return ""
	}
	offset = max(0, offset)
	var b strings.Builder
	pos := 0
	written := 0
	for _, r := range value {
		part := string(r)
		partWidth := max(1, lipgloss.Width(part))
		if pos+partWidth <= offset {
			pos += partWidth
			continue
		}
		if written+partWidth > width {
			break
		}
		b.WriteRune(r)
		written += partWidth
		pos += partWidth
	}
	return b.String()
}

func padCells(value string, width int) string {
	if width <= 0 {
		return ""
	}
	current := lipgloss.Width(value)
	if current >= width {
		return value
	}
	return value + strings.Repeat(" ", width-current)
}

func horizontalScrollbar(width, offset, contentWidth, viewportWidth int) string {
	if width <= 2 {
		return strings.Repeat("─", max(0, width))
	}
	left := "◄"
	if offset <= 0 {
		left = " "
	}
	right := "►"
	if offset+viewportWidth >= contentWidth {
		right = " "
	}
	return left + strings.Repeat("─", max(0, width-2)) + right
}

func (m *Model) historyList() string {
	var b strings.Builder
	theme := defaultTheme()
	for i, entry := range historyEntries(m.history, m.activeProfileID()) {
		line := fmt.Sprintf("%s %s", entry.Action, entry.Statement)
		line = sidebarCursorLine(theme, line, i == m.historyIndex, m.focus == FocusSidebar)
		b.WriteString(line + "\n")
	}
	return b.String()
}

func sidebarCursorLine(theme appTheme, line string, selected, focused bool) string {
	if !selected {
		return "  " + line
	}
	if focused {
		return theme.selected.Render("> " + line)
	}
	// Keep the cursor position visible even when the panel is not focused, just
	// dimmer than the focused highlight.
	return theme.selectedDim.Render("> " + line)
}

type footerAction struct {
	id     string
	label  string
	action appAction
}

func renderFooter(theme appTheme, m *Model, width int) string {
	status := m.message
	if status == "" {
		status = "Ready"
	}
	var right string
	if m.loading.active {
		right = m.spinnerView()
	} else {
		icon, style := footerStatusStyle(theme, status)
		right = style.Render(icon + " " + status)
	}
	left := m.footerHints(theme)

	inner := max(1, width-2) // footer style adds 1 cell padding each side
	gap := 2
	rightW := ansi.StringWidth(right)
	avail := inner - rightW - gap
	if avail < 0 {
		avail = 0
	}
	if ansi.StringWidth(left) > avail {
		left = ansi.Truncate(left, max(0, avail-1), "…")
	}
	pad := inner - ansi.StringWidth(left) - rightW
	if pad < gap {
		pad = gap
	}
	line := left + strings.Repeat(" ", pad) + right
	return theme.footer.Width(width).Render(line)
}

// footerStatusStyle picks an icon and color for a status message based on its
// content, so successes, warnings and errors read at a glance.
func footerStatusStyle(theme appTheme, msg string) (string, lipgloss.Style) {
	lower := strings.ToLower(msg)
	containsAny := func(needles ...string) bool {
		for _, n := range needles {
			if strings.Contains(lower, n) {
				return true
			}
		}
		return false
	}
	switch {
	case containsAny("fail", "error", "invalid", "unsupported", "denied", "cannot", "could not", "no active connection"):
		return "✗", theme.statusErr
	case containsAny("ok", "executed", "copied", "opened", "closed", "unlocked", "saved", "updated", "deleted", "created", "expanded", "loaded", "refreshed", "connected"):
		return "✓", theme.statusOK
	case containsAny("requires confirmation", "read-only", "nothing", "already", "unavailable", "not found", "no "):
		return "!", theme.statusWarn
	default:
		return "·", theme.statusInfo
	}
}

// footerHints renders the most relevant keybindings for the current page/focus.
func (m *Model) footerHints(theme appTheme) string {
	hint := func(pairs ...[2]string) string {
		parts := make([]string, 0, len(pairs))
		for _, p := range pairs {
			parts = append(parts, theme.hintKey.Render(p[0])+" "+theme.hintLabel.Render(p[1]))
		}
		return strings.Join(parts, theme.hintLabel.Render(" · "))
	}
	if m.overlayOpen() {
		return hint([2]string{"↑↓", "navigate"}, [2]string{"enter", "select"}, [2]string{"esc", "close"})
	}
	switch m.page {
	case PageUnlock:
		return hint([2]string{"enter", "unlock"}, [2]string{":q", "quit"})
	case PageConnections:
		return hint([2]string{"↑↓", "select"}, [2]string{"enter", "open"}, [2]string{"n", "new"}, [2]string{"e", "edit"}, [2]string{"d", "delete"}, [2]string{"t", "test"}, [2]string{":", "cmd"}, [2]string{"?", "help"})
	}
	if m.focus == FocusSidebar {
		return hint([2]string{"↑↓", "select"}, [2]string{"enter", "open/expand"}, [2]string{"/", "search"}, [2]string{"tab", "workspace"}, [2]string{":", "cmd"}, [2]string{"?", "help"})
	}
	if tab := m.activeWorkspaceTab(); tab != nil {
		if tab.Kind == workspaceTabQuery {
			switch {
			case tab.RowDetail:
				return hint([2]string{"↑↓/hl", "move"}, [2]string{"v", "select"}, [2]string{"y", "copy"}, [2]string{"esc", "back"})
			case tab.WorkspaceFocus == workspaceFocusResult:
				return hint([2]string{"↑↓", "select"}, [2]string{"enter", "row"}, [2]string{"^s", "history"}, [2]string{"i", "edit"}, [2]string{"tab", "nav"})
			default:
				return hint([2]string{"enter", "run"}, [2]string{"^j", "newline"}, [2]string{"^s", "history"}, [2]string{"esc", "normal"}, [2]string{"tab", "nav"})
			}
		}
		return hint([2]string{"hjkl", "move"}, [2]string{"v", "select"}, [2]string{"y", "copy"}, [2]string{"tab", "nav"}, [2]string{"?", "help"})
	}
	return hint([2]string{"tab", "switch"}, [2]string{":", "cmd"}, [2]string{"?", "help"}, [2]string{":q", "quit"})
}

func commandSuggestionHeight(m *Model) int {
	if !m.input.SuggestionsVisible() {
		return 0
	}
	count := len(m.input.Suggestions())
	if count > 5 {
		count = 5
	}
	if count == 0 {
		return 0
	}
	return count + 1
}

func renderCommandSuggestions(theme appTheme, m *Model, width int) string {
	var b strings.Builder
	b.WriteString("Suggestions:\n")
	suggestions := m.input.Suggestions()
	limit := len(suggestions)
	if limit > 5 {
		limit = 5
	}
	for idx := 0; idx < limit; idx++ {
		selected := idx == m.input.SelectedIndex()
		marker := " "
		if selected {
			marker = ">"
		}
		row := marker + " " + suggestions[idx].Label
		if suggestions[idx].Detail != "" {
			row += " - " + suggestions[idx].Detail
		}
		b.WriteString(selectedRow(theme, row, selected))
		if idx < limit-1 {
			b.WriteString("\n")
		}
	}
	return theme.command.Width(width).Render(b.String())
}

func footerHitboxes(page Page, y int) HitboxRegistry {
	actions := footerActions(page)
	boxes := make(HitboxRegistry, 0, len(actions))
	x := 9
	for _, item := range actions {
		width := len(item.label) + 2
		boxes = append(boxes, Hitbox{
			ID:     "footer:" + item.id,
			X:      x,
			Y:      y,
			Width:  width,
			Height: 1,
			Focus:  FocusCommand,
			Action: item.action,
		})
		x += width + 1
	}
	return boxes
}

func footerActions(page Page) []footerAction {
	switch page {
	case PageConnections:
		return []footerAction{
			{id: "new", label: "New", action: actionNew},
			{id: "open", label: "Open", action: actionOpen},
			{id: "edit", label: "Edit", action: actionEdit},
			{id: "delete", label: "Delete", action: actionDelete},
			{id: "test", label: "Test", action: actionTest},
			{id: "history", label: "History", action: actionHistory},
		}
	case PageBrowser:
		return []footerAction{
			{id: "open", label: "Open", action: actionOpen},
			{id: "refresh", label: "Refresh", action: actionRefresh},
			{id: "query", label: "Query", action: actionQuery},
			{id: "history", label: "History", action: actionHistory},
		}
	case PageData:
		return []footerAction{
			{id: "refresh", label: "Refresh", action: actionRefresh},
			{id: "query", label: "Query", action: actionQuery},
			{id: "delete", label: "Delete", action: actionDelete},
		}
	case PageHistory:
		return []footerAction{
			{id: "open", label: "Refill", action: actionOpen},
			{id: "query", label: "Query", action: actionQuery},
		}
	default:
		return nil
	}
}

func moduleName(m *Model) string {
	if m.form != nil {
		return "connection-form"
	}
	if m.pending != nil {
		return "confirm"
	}
	if m.focus == FocusCommand {
		return "command"
	}
	switch m.page {
	case PageConnections:
		return "connections"
	case PageBrowser:
		return "browser"
	case PageData:
		return "data"
	case PageQuery:
		return "query"
	case PageHistory:
		return "history"
	case PageHelp:
		return "help"
	default:
		return "unlock"
	}
}

func shortcutText(page Page) string {
	switch page {
	case PageConnections:
		return "Tab panels | j/k select | Enter open | n new | e edit | d delete | t test | : command | ? help | q back"
	case PageBrowser:
		return "Tab panels | j/k select | Enter open | / search | Shift+Left/Right scroll | r refresh | : command | ? help | q back"
	case PageData:
		return "Tab panels | v visual | y copy | h/j/k/l move | r refresh | : command | ? help | q back"
	case PageQuery:
		return "Ctrl+Space suggest | Tab accept | Enter execute | : command | ? help | q back"
	case PageHistory:
		return "j/k select | Enter refill | r replay | : command | ? help | q back"
	default:
		return "Tab panels | : command | ? help | q back"
	}
}

func trimToHeight(content string, height int) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) <= height {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:height], "\n") + "\n…"
}
