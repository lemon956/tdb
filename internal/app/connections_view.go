package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"tdb/internal/config"
	"tdb/internal/result"
)

// connectionsUIState owns the connections-manager screen's cursor/selection and
// the detail popup. These are global UI state (not per-connection), so they live
// outside connSession. Embedded in Model so m.connectionIndex / m.connectionsView
// … keep working via field promotion.
type connectionsUIState struct {
	connectionIndex   int
	connectionsView   ResultView // VisiData-style connections table: column/row scroll
	connectionsDetail bool       // connection detail popup open (Ctrl+Enter)
	connectionsAnchor int        // row-visual anchor for v/y copy
	connectionsVisual bool       // row-visual ("copy mode") active in the table
	// Detail popup (Field/Value table) cursor state — mirrors the mysql result page.
	connectionsDetailIndex  int
	connectionsDetailView   ResultView
	connectionsDetailAnchor int
	connectionsDetailVisual bool
}

// connectionColumns is the fixed column set for the VisiData-style connections
// table — the single rule that maps every saved connection to one table row.
var connectionColumns = []result.Column{
	{Name: "ID"}, {Name: "Driver"}, {Name: "Host"}, {Name: "Port"},
	{Name: "Database"}, {Name: "User"}, {Name: "Access"},
}

// connectionsTable turns the saved profiles into a result.Table (one row per
// connection) so the connections popup can reuse the mysql result rendering and
// cursor machinery.
func (m *Model) connectionsTable() *result.Table {
	rows := make([]result.Row, len(m.vault.Profiles))
	for i, p := range m.vault.Profiles {
		host := p.Host
		if host == "" {
			host = connectionEndpoint(p)
		}
		port := "-"
		if p.Port != 0 {
			port = strconv.Itoa(p.Port)
		}
		database := valueOrDash(p.Database)
		if p.Driver == config.DriverRedis {
			database = strconv.Itoa(p.RedisDB)
		}
		access := "RW"
		if p.ReadOnly {
			access = "RO"
		}
		rows[i] = result.Row{Values: []any{
			p.ID, string(p.Driver), valueOrDash(host), port, database, valueOrDash(p.User), access,
		}}
	}
	return &result.Table{Columns: connectionColumns, Rows: rows}
}

// connectionsPopupActive reports whether the connections page should render as the
// centered table popup. Forms / confirms / modals fall back to the normal layout so
// n/e/d/t dialogs still appear.
func (m *Model) connectionsPopupActive() bool {
	return m.page == PageConnections && m.form == nil && m.pending == nil &&
		m.modal == nil && m.errBox == nil && !m.helpOpen
}

// connectionDetailRows returns the driver-aware Field/Value pairs for a single
// connection (the single source for both the plain detail text and the detail
// table). The password is never included; a URI's password is redacted.
func connectionDetailRows(p config.Profile) [][2]string {
	access := "RW"
	if p.ReadOnly {
		access = "RO"
	}
	rows := [][2]string{{"ID", p.ID}}
	if p.Name != "" {
		rows = append(rows, [2]string{"Name", p.Name})
	}
	rows = append(rows,
		[2]string{"Access", access},
		[2]string{"Driver", string(p.Driver)},
		[2]string{"Host", valueOrDash(connectionEndpoint(p))},
	)
	if p.User != "" {
		rows = append(rows, [2]string{"User", p.User})
	}
	switch p.Driver {
	case config.DriverRedis:
		rows = append(rows, [2]string{"DB", strconv.Itoa(p.RedisDB)})
	case config.DriverMongo:
		rows = append(rows, [2]string{"Database", valueOrDash(p.Database)})
		if p.AuthDB != "" {
			rows = append(rows, [2]string{"Auth DB", p.AuthDB})
		}
		if p.URIParams != "" {
			rows = append(rows, [2]string{"URI", redactedURI(p.URIParams)})
		}
	default: // mysql / doris
		rows = append(rows, [2]string{"Database", valueOrDash(p.Database)})
	}
	return rows
}

// connectionDetailTable renders the selected connection's fields as a Field/Value
// table so the detail popup reuses the mysql result cursor/copy machinery.
func (m *Model) connectionDetailTable() *result.Table {
	cols := []result.Column{{Name: "Field"}, {Name: "Value"}}
	p, ok := m.selectedProfile()
	if !ok {
		return &result.Table{Columns: cols}
	}
	pairs := connectionDetailRows(p)
	rows := make([]result.Row, len(pairs))
	for i, kv := range pairs {
		rows[i] = result.Row{Values: []any{kv[0], kv[1]}}
	}
	return &result.Table{Columns: cols, Rows: rows}
}

func (m *Model) connectionsDetailRowCount() int {
	return len(m.connectionDetailTable().Rows)
}

func (m *Model) moveConnectionsDetail(delta int) {
	n := m.connectionsDetailRowCount()
	if n == 0 {
		return
	}
	m.connectionsDetailIndex = clamp(m.connectionsDetailIndex+delta, 0, n-1)
	h := max(1, m.connectionsViewportHeight())
	off := m.connectionsDetailView.RowOffset
	if m.connectionsDetailIndex < off {
		off = m.connectionsDetailIndex
	}
	if m.connectionsDetailIndex >= off+h {
		off = m.connectionsDetailIndex - h + 1
	}
	m.connectionsDetailView.RowOffset = clamp(off, 0, max(0, n-h))
}

// connectionsDetailSelectionText copies the selected Field/Value rows as TSV.
func (m *Model) connectionsDetailSelectionText() string {
	t := m.connectionDetailTable()
	if len(t.Rows) == 0 {
		return ""
	}
	start, end := m.connectionsDetailAnchor, m.connectionsDetailIndex
	if start > end {
		start, end = end, start
	}
	start = clamp(start, 0, len(t.Rows)-1)
	end = clamp(end, 0, len(t.Rows)-1)
	rows := make([]string, 0, end-start+1)
	for r := start; r <= end; r++ {
		rows = append(rows, t.CellString(r, 0)+"\t"+t.CellString(r, 1))
	}
	return strings.Join(rows, "\n")
}

// handleConnectionsDetailKey mirrors the mysql result page cursor controls for the
// detail Field/Value table. Returns true when it consumed the key.
func (m *Model) handleConnectionsDetailKey(msg tea.KeyMsg) bool {
	prevG := m.navPendingG
	m.navPendingG = false
	const cols = 2
	move := func() bool {
		switch msg.String() {
		case "j", "down":
			m.moveConnectionsDetail(1)
		case "k", "up":
			m.moveConnectionsDetail(-1)
		case "g":
			if prevG {
				m.moveConnectionsDetail(-1 << 30)
			} else {
				m.navPendingG = true
			}
		case "G":
			m.moveConnectionsDetail(1 << 30)
		case "pgdown":
			m.moveConnectionsDetail(m.connectionsViewportHeight())
		case "pgup":
			m.moveConnectionsDetail(-m.connectionsViewportHeight())
		case "h", "left":
			m.connectionsDetailView.ScrollColumns(-1, cols)
		case "l", "right":
			m.connectionsDetailView.ScrollColumns(1, cols)
		default:
			return false
		}
		return true
	}
	if m.connectionsDetailVisual {
		switch msg.String() {
		case "y":
			m.copyText(m.connectionsDetailSelectionText())
			m.connectionsDetailVisual = false
		default:
			move()
		}
		return true
	}
	switch msg.String() {
	case "v":
		m.connectionsDetailVisual = true
		m.connectionsDetailAnchor = m.connectionsDetailIndex
	case "y":
		m.connectionsDetailAnchor = m.connectionsDetailIndex
		m.copyText(m.connectionsDetailSelectionText())
	case "/":
		m.startConnectionsDetailSearch()
	case "n":
		m.cycleViewSearch(1)
	case "N":
		m.cycleViewSearch(-1)
	default:
		return move()
	}
	return true
}

func (m *Model) startConnectionsDetailSearch() {
	rows := tableRowStrings(m.connectionDetailTable())
	m.startViewSearch(rows, m.connectionsDetailIndex, func(i int) {
		m.moveConnectionsDetail(i - m.connectionsDetailIndex)
	})
}

// connectionsViewportHeight is how many connection rows are visible in the popup.
func (m *Model) connectionsViewportHeight() int {
	return max(3, m.mainInnerHeight()-4)
}

func (m *Model) connectionsSelectionRows() (int, int) {
	start, end := m.connectionsAnchor, m.connectionIndex
	if start > end {
		start, end = end, start
	}
	n := len(m.vault.Profiles)
	start = clamp(start, 0, max(0, n-1))
	end = clamp(end, 0, max(0, n-1))
	return start, end
}

// connectionsSelectionText renders the selected connection rows as TSV.
func (m *Model) connectionsSelectionText() string {
	t := m.connectionsTable()
	if len(t.Rows) == 0 {
		return ""
	}
	start, end := m.connectionsSelectionRows()
	rows := make([]string, 0, end-start+1)
	for r := start; r <= end; r++ {
		cells := make([]string, len(t.Columns))
		for c := range t.Columns {
			cells[c] = t.CellString(r, c)
		}
		rows = append(rows, strings.Join(cells, "\t"))
	}
	return strings.Join(rows, "\n")
}

// moveConnectionSelection moves the selected row and keeps it within the visible
// window (mirrors moveQueryResultSelection on the mysql result page).
func (m *Model) moveConnectionSelection(delta int) {
	n := len(m.vault.Profiles)
	if n == 0 {
		return
	}
	m.connectionIndex = clamp(m.connectionIndex+delta, 0, n-1)
	h := max(1, m.connectionsViewportHeight())
	off := m.connectionsView.RowOffset
	if m.connectionIndex < off {
		off = m.connectionIndex
	}
	if m.connectionIndex >= off+h {
		off = m.connectionIndex - h + 1
	}
	m.connectionsView.RowOffset = clamp(off, 0, max(0, n-h))
}

// handleConnectionsKey drives the connections table popup with mysql-result-style
// cursor controls. Returns true when it consumed the key; unhandled keys (n/e/d/t,
// ':', '?') fall through to the global handlers.
func (m *Model) handleConnectionsKey(ctx context.Context, msg tea.KeyMsg) bool {
	if m.connectionsDetail {
		switch msg.String() {
		case "esc", "q", "ctrl+j", "alt+enter":
			m.connectionsDetail = false
			return true
		case ":":
			// Let the global handler open command mode so ":q" can quit.
			return false
		}
		m.handleConnectionsDetailKey(msg)
		return true
	}
	if len(m.vault.Profiles) == 0 {
		return false
	}
	prevG := m.navPendingG
	m.navPendingG = false
	const cols = 7

	move := func() bool {
		switch msg.String() {
		case "j", "down":
			m.moveConnectionSelection(1)
		case "k", "up":
			m.moveConnectionSelection(-1)
		case "g":
			if prevG {
				m.moveConnectionSelection(-1 << 30)
			} else {
				m.navPendingG = true
			}
		case "G":
			m.moveConnectionSelection(1 << 30)
		case "pgdown":
			m.moveConnectionSelection(m.connectionsViewportHeight())
		case "pgup":
			m.moveConnectionSelection(-m.connectionsViewportHeight())
		case "h", "left":
			m.connectionsView.ScrollColumns(-1, cols)
		case "l", "right":
			m.connectionsView.ScrollColumns(1, cols)
		default:
			return false
		}
		return true
	}

	if m.connectionsVisual {
		switch msg.String() {
		case "esc":
			m.connectionsVisual = false
		case "y":
			m.copyText(m.connectionsSelectionText())
			m.connectionsVisual = false
		default:
			if !move() {
				return true // swallow other keys in visual mode
			}
		}
		return true
	}

	switch msg.String() {
	case "v":
		m.connectionsVisual = true
		m.connectionsAnchor = m.connectionIndex
	case "y":
		m.connectionsAnchor = m.connectionIndex
		m.copyText(m.connectionsSelectionText())
	case "q":
		// Leave the picker: resume an open connection if there is one, otherwise
		// drop into the fallback dino game.
		if len(m.sessions) > 0 {
			m.switchSession(len(m.sessions) - 1)
		} else {
			m.enterDinoGame()
		}
	case "enter":
		m.runPageAction(ctx, actionOpen)
	case "ctrl+j", "alt+enter":
		m.connectionsDetail = true
		m.connectionsDetailIndex = 0
		m.connectionsDetailView = ResultView{}
		m.connectionsDetailVisual = false
	case "/":
		m.startConnectionsSearch()
	case "n":
		m.cycleViewSearch(1)
	case "N":
		m.cycleViewSearch(-1)
	default:
		return move()
	}
	return true
}

func (m *Model) startConnectionsSearch() {
	rows := tableRowStrings(m.connectionsTable())
	m.startViewSearch(rows, m.connectionIndex, func(i int) {
		m.moveConnectionSelection(i - m.connectionIndex)
	})
}

// connectionsHeaderHitboxes registers the top connection-tab strip (so it stays
// clickable behind the popup), reusing the shared header layout.
func (m *Model) connectionsHeaderHitboxes(width int) HitboxRegistry {
	_, hits := m.headerTabLayout(width)
	boxes := HitboxRegistry{}
	for _, h := range hits {
		if h.index < 0 {
			boxes = append(boxes, Hitbox{ID: "conn-add", X: h.x, Y: 0, Width: h.width, Height: 1})
			continue
		}
		boxes = append(boxes, Hitbox{ID: fmt.Sprintf("conn-tab:%d", h.index), X: h.x, Y: 0, Width: h.width, Height: 1, Index: h.index})
	}
	return boxes
}

// connectionsPopupBody renders the centered connections table (or the detail
// popup) over a blank body, and registers per-row click hitboxes aligned to the
// rendered rows. width/bodyHeight are the screen body dimensions.
func (m *Model) connectionsPopupBody(width, bodyHeight int) string {
	theme := defaultTheme()
	base := padBlock("", width, bodyHeight)
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.accentColor).
		Padding(0, 1)

	if m.connectionsDetail {
		innerW := clamp(width-8, 40, max(40, width-4))
		dview := ResultView{
			Height:       m.connectionsViewportHeight(),
			MaxWidth:     innerW,
			Selectable:   true,
			MarkCursor:   true,
			SelectedRow:  m.connectionsDetailIndex,
			RowOffset:    m.connectionsDetailView.RowOffset,
			ColumnOffset: m.connectionsDetailView.ColumnOffset,
		}
		if m.connectionsDetailVisual {
			dview.SelectionActive = true
			s, e := m.connectionsDetailAnchor, m.connectionsDetailIndex
			if s > e {
				s, e = e, s
			}
			dview.SelectionStart, dview.SelectionEnd = s, e
		}
		title := theme.accent.Render("Connection details") +
			theme.muted.Render("  Esc back · j/k move · h/l cols · v/y copy · :q quit")
		box := boxStyle.Render(title + "\n" + dview.Render(result.Set{Table: m.connectionDetailTable()}))
		m.hitboxes = m.connectionsHeaderHitboxes(width)
		return placeOverlayCenter(base, box)
	}

	if len(m.vault.Profiles) == 0 {
		box := boxStyle.Render(theme.accent.Render("Connections") + "\n\n" + theme.muted.Render("No connections.  Press n to create one."))
		m.hitboxes = m.connectionsHeaderHitboxes(width)
		return placeOverlayCenter(base, box)
	}

	table := m.connectionsTable()
	innerW := clamp(width-8, 40, max(40, width-4))
	view := ResultView{
		Height:       m.connectionsViewportHeight(),
		MaxWidth:     innerW,
		Selectable:   true,
		MarkCursor:   true,
		SelectedRow:  m.connectionIndex,
		RowOffset:    m.connectionsView.RowOffset,
		ColumnOffset: m.connectionsView.ColumnOffset,
	}
	if m.connectionsVisual {
		view.SelectionActive = true
		view.SelectionStart, view.SelectionEnd = m.connectionsSelectionRows()
	}
	tableStr := view.Render(result.Set{Table: table})
	title := theme.accent.Render("Connections") +
		theme.muted.Render("  Enter open · ^J details · n new · e edit · d del · t test · h/l cols")
	box := boxStyle.Render(title + "\n" + tableStr)

	boxW := lipgloss.Width(box)
	boxH := lipgloss.Height(box)
	boxTop := max(0, (bodyHeight-boxH)/2)
	boxLeft := max(0, (width-boxW)/2)

	// Register per-row hitboxes. Box rows: 0 border, 1 title, 2 table header, 3+k
	// data row k; the body sits one row below the app header (Y=1).
	hits := m.connectionsHeaderHitboxes(width)
	dataTopY := 1 + boxTop + 3
	rowStart := clamp(m.connectionsView.RowOffset, 0, max(0, len(table.Rows)-1))
	rowEnd := min(len(table.Rows), rowStart+m.connectionsViewportHeight())
	for k := 0; rowStart+k < rowEnd; k++ {
		idx := rowStart + k
		hits = append(hits, Hitbox{
			ID: fmt.Sprintf("connection:%d", idx), X: boxLeft + 2, Y: dataTopY + k,
			Width: max(1, boxW-4), Height: 1, Focus: FocusSidebar, Index: idx,
		})
	}
	m.hitboxes = hits
	return placeOverlay(base, box, boxTop, boxLeft)
}
