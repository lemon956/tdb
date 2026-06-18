package app

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"tdb/internal/result"
)

// viewSearchState owns the `/` search over the active grid/detail view. It is
// embedded in Model so existing m.viewSearchQuery / m.viewSearchMatches … access
// keeps working via field promotion.
type viewSearchState struct {
	viewSearchInput   bool
	viewSearchQuery   string
	viewSearchRows    []string
	viewSearchMatches []int
	viewSearchIndex   int
	viewSearchOrigin  int
	viewSearchMoveTo  func(int)
}

// View search adds a `/` incremental search (with n/N navigation) to the
// read-only grid/detail views, which already have vim cursor movement + copy.
// Each view passes its own searchable rows + a cursor-setter, so the same logic
// serves the data grid, row detail, metadata, query results, and the
// connections table/detail.

// startViewSearch arms a `/` search over the given rows. cursor is the current
// row; moveTo jumps the view's cursor to a row index.
func (m *Model) startViewSearch(rows []string, cursor int, moveTo func(int)) {
	m.viewSearchInput = true
	m.viewSearchQuery = ""
	m.viewSearchRows = rows
	m.viewSearchMoveTo = moveTo
	m.viewSearchOrigin = cursor
	m.viewSearchMatches = nil
	m.viewSearchIndex = 0
	m.input.Clear()
	m.message = "/"
}

// updateViewSearch recomputes matches for the current query and jumps to the
// first one at/after the origin.
func (m *Model) updateViewSearch() {
	query := strings.ToLower(strings.TrimSpace(m.input.Value()))
	m.viewSearchMatches = nil
	if query != "" {
		for i, row := range m.viewSearchRows {
			if strings.Contains(strings.ToLower(ansi.Strip(row)), query) {
				m.viewSearchMatches = append(m.viewSearchMatches, i)
			}
		}
	}
	m.viewSearchIndex = 0
	// Prefer the first match at/after where the cursor started.
	for i, row := range m.viewSearchMatches {
		if row >= m.viewSearchOrigin {
			m.viewSearchIndex = i
			break
		}
	}
	m.jumpToCurrentMatch(query)
}

func (m *Model) jumpToCurrentMatch(query string) {
	if len(m.viewSearchMatches) == 0 {
		if query == "" {
			m.message = "/"
		} else {
			m.message = "no match: " + query
		}
		return
	}
	if m.viewSearchMoveTo != nil {
		m.viewSearchMoveTo(m.viewSearchMatches[m.viewSearchIndex])
	}
	m.message = matchStatus(m.viewSearchIndex, len(m.viewSearchMatches), query)
}

func matchStatus(index, total int, query string) string {
	return "match " + strconv.Itoa(index+1) + "/" + strconv.Itoa(total) + ": " + query
}

// confirmViewSearch ends the input phase but keeps the matches armed so n/N keep
// working in the view.
func (m *Model) confirmViewSearch() {
	m.viewSearchInput = false
	m.input.Clear()
	if len(m.viewSearchMatches) == 0 {
		m.message = "no match"
		return
	}
	m.message = matchStatus(m.viewSearchIndex, len(m.viewSearchMatches), m.viewSearchQueryText())
}

func (m *Model) cancelViewSearch() {
	m.viewSearchInput = false
	m.viewSearchMatches = nil
	m.viewSearchRows = nil
	m.viewSearchMoveTo = nil
	m.input.Clear()
	m.message = "search cancelled"
}

// cycleViewSearch moves to the next (dir=1) or previous (dir=-1) match.
func (m *Model) cycleViewSearch(dir int) bool {
	if len(m.viewSearchMatches) == 0 {
		return false
	}
	n := len(m.viewSearchMatches)
	m.viewSearchIndex = (m.viewSearchIndex + dir + n) % n
	if m.viewSearchMoveTo != nil {
		m.viewSearchMoveTo(m.viewSearchMatches[m.viewSearchIndex])
	}
	m.message = matchStatus(m.viewSearchIndex, n, m.viewSearchQueryText())
	return true
}

func (m *Model) viewSearchActive() bool { return len(m.viewSearchMatches) > 0 }

func (m *Model) viewSearchQueryText() string {
	return m.viewSearchQuery
}

// handleViewSearchInputKey consumes keys while the `/` query is being typed.
func (m *Model) handleViewSearchInputKey(key string, runes []rune) bool {
	switch key {
	case "enter":
		m.viewSearchQuery = strings.TrimSpace(m.input.Value())
		m.confirmViewSearch()
	case "esc":
		m.cancelViewSearch()
	case "backspace", "ctrl+h":
		m.input.Backspace()
		m.viewSearchQuery = strings.TrimSpace(m.input.Value())
		m.updateViewSearch()
	default:
		if len(runes) > 0 {
			m.input.Insert(string(runes))
			m.viewSearchQuery = strings.TrimSpace(m.input.Value())
			m.updateViewSearch()
		}
	}
	return true
}

// startDataViewSearch arms `/` search over the workspace data/metadata/row-detail
// grid (char-cell cursor on tab.ResultRow).
func (m *Model) startDataViewSearch(tab *workspaceTab) {
	lines := m.activeDataLines(tab, m.workspaceContentWidth())
	m.startViewSearch(lines, tab.ResultRow, func(i int) {
		tab.ResultRow = i
		m.clampDataCursor(tab)
	})
}

// startQueryResultSearch arms `/` search over a selectable query-result table
// (row-level cursor on tab.ResultCursorRow).
func (m *Model) startQueryResultSearch(tab *workspaceTab) {
	rows := tableRowStrings(tab.Result.Table)
	m.startViewSearch(rows, tab.ResultCursorRow, func(i int) {
		m.moveQueryResultSelection(tab, i-tab.ResultCursorRow)
	})
}

// tableRowStrings renders one searchable string per table row (tab-joined cells).
func tableRowStrings(t *result.Table) []string {
	if t == nil {
		return nil
	}
	rows := make([]string, len(t.Rows))
	for r := range t.Rows {
		cells := make([]string, len(t.Columns))
		for c := range t.Columns {
			cells[c] = t.CellString(r, c)
		}
		rows[r] = strings.Join(cells, "\t")
	}
	return rows
}
