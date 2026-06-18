package app

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"tdb/internal/result"
)

// padCellsANSI right-pads an ANSI-styled string to width display cells (measuring
// with ansi.StringWidth so embedded color codes don't count toward the width).
func padCellsANSI(value string, width int) string {
	current := ansi.StringWidth(value)
	if current >= width {
		return value
	}
	return value + strings.Repeat(" ", width-current)
}

// mongoObjectIDPattern matches an _id (or *_id reference) whose value is a 24-char
// hex string, so mongo documents render the value as ObjectId("...") shell-style.
var mongoObjectIDPattern = regexp.MustCompile(`("(?:_id|[A-Za-z0-9_]*_id)"\s*:\s*)"([0-9a-fA-F]{24})"`)

// wrapMongoObjectIDs rewrites `"_id": "<hex>"` to `"_id": ObjectId("<hex>")` in a
// rendered JSON document. Non-24-hex ids (custom string keys) are left untouched.
func wrapMongoObjectIDs(jsonText string) string {
	return mongoObjectIDPattern.ReplaceAllString(jsonText, `${1}ObjectId("${2}")`)
}

const maxDataLineCells = 4096
const maxDataTableCellCells = 40
const minDataTableCellCells = 4

func (m *Model) renderDataResultViewport(tab workspaceTab, width, height int) string {
	width = max(8, width)
	height = max(1, height)
	lines := m.activeDataLines(&tab, width)
	return m.renderDataTextViewport(tab, lines, width, height)
}

func (m *Model) renderDataTextViewport(tab workspaceTab, lines []string, width, height int) string {
	if len(lines) == 0 {
		return ""
	}
	cursor := clamp(tab.ResultRow, 0, len(lines)-1)
	offset := clamp(tab.ResultView.RowOffset, 0, max(0, len(lines)-height))
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+height {
		offset = cursor - height + 1
	}
	offset = clamp(offset, 0, max(0, len(lines)-height))
	end := min(len(lines), offset+height)
	// Reserve one column for the scrollbar when the content overflows vertically.
	scrollable := len(lines) > height
	contentWidth := width
	if scrollable {
		contentWidth = max(1, width-1)
	}
	columnOffset := clamp(tab.ResultView.ColumnOffset, 0, max(0, maxDataDisplayLineWidth(lines)-contentWidth))
	var bar []string
	if scrollable {
		bar = verticalScrollbar(height, len(lines), height, offset)
	}
	theme := defaultTheme()
	startRow, startCol, endRow, endCol := orderedDataSelection(tab)
	startRow = clamp(startRow, 0, len(lines)-1)
	endRow = clamp(endRow, 0, len(lines)-1)
	var b strings.Builder
	for idx := offset; idx < end; idx++ {
		// ANSI-aware horizontal slice: lines may carry JSON highlight color codes.
		line := padCellsANSI(sliceColumns(lines[idx], columnOffset, columnOffset+contentWidth), contentWidth)
		switch {
		case tab.VimMode == vimModeVisual && idx >= startRow && idx <= endRow:
			line = renderDataVisualLine(line, contentWidth, idx, startRow, startCol-columnOffset, endRow, endCol-columnOffset, theme)
		case m.focus == FocusMain && idx == cursor:
			line = renderDataCursorLine(line, contentWidth, tab.ResultColumn-columnOffset, theme)
		}
		if bar != nil {
			line += barCell(bar, idx-offset)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func renderDataVisualLine(line string, width, row, startRow, startCol, endRow, endCol int, theme appTheme) string {
	line = padCellsANSI(line, width)
	visualStart := 0
	visualEnd := max(0, lipgloss.Width(strings.TrimRight(line, " "))-1)
	if row == startRow {
		visualStart = startCol
	}
	if row == endRow {
		visualEnd = endCol
	}
	visualStart = clamp(visualStart, 0, max(0, width-1))
	visualEnd = clamp(visualEnd, 0, max(0, width-1))
	if visualStart > visualEnd {
		visualStart, visualEnd = visualEnd, visualStart
	}
	selected := sliceColumns(line, visualStart, visualEnd+1)
	if selected == "" {
		selected = " "
	}
	selectedWidth := max(1, lipgloss.Width(selected))
	prefix := sliceColumns(line, 0, visualStart)
	suffix := sliceColumns(line, visualStart+selectedWidth, width)
	// Strip the selected cell's own color so the visual highlight reverses cleanly
	// (a nested SGR reset would otherwise cut the highlight short); prefix/suffix
	// keep their JSON colors.
	return prefix + theme.visual.Render(ansi.Strip(selected)) + suffix
}

func renderDataCursorLine(line string, width, column int, theme appTheme) string {
	line = padCellsANSI(line, width)
	column = clamp(column, 0, max(0, width-1))
	cell := sliceColumns(line, column, column+1)
	if cell == "" {
		cell = " "
	}
	cellWidth := max(1, lipgloss.Width(cell))
	prefix := sliceColumns(line, 0, column)
	suffix := sliceColumns(line, column+cellWidth, width)
	// Strip the cursor cell's own color so the cursor block renders cleanly over
	// it; prefix/suffix keep their JSON colors.
	return prefix + theme.cursor.Render(ansi.Strip(cell)) + suffix
}

func fitDataTableCell(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) > width {
		if width <= 3 {
			value = cellSlice(value, 0, width)
		} else {
			value = cellSlice(value, 0, width-3) + "..."
		}
	}
	return padCells(value, width)
}

func dataDisplayLines(set result.Set, width int) []string {
	if set.Table != nil {
		return dataTableLines(*set.Table)
	}
	return wrappedDataLines(dataResultLines(set), width)
}

// activeDataLines returns the display lines the grid cursor operates on, memoized
// because computing them (JSON marshal + highlight + wrap over ALL rows) is
// expensive and is hit 5–6x per keystroke plus once per render. The key captures
// everything the output depends on; a reloaded/ paged result gets a fresh backing
// slice (new pointer) so the cache invalidates automatically.
func (m *Model) activeDataLines(tab *workspaceTab, width int) []string {
	key := dataLinesCacheKey(m.activeTabIndex, tab, width)
	if key == m.dataLinesKey && m.dataLinesCache != nil {
		return m.dataLinesCache
	}
	lines := computeActiveDataLines(tab, width)
	m.dataLinesKey = key
	m.dataLinesCache = lines
	return lines
}

func computeActiveDataLines(tab *workspaceTab, width int) []string {
	if tab.RowDetail && tab.Result.Table != nil {
		return rowDetailLines(*tab.Result.Table, tab.ResultCursorRow)
	}
	return dataDisplayLines(tab.Result, width)
}

// dataLinesCacheKey fingerprints everything activeDataLines depends on. The
// result content is identified by its backing-slice pointers + lengths (results
// are immutable and replaced wholesale on load/paging, so a content change always
// yields a new pointer). RowDetail + ResultCursorRow matter only for the
// transposed single-row view but are cheap to always include.
func dataLinesCacheKey(tabIndex int, tab *workspaceTab, width int) string {
	rows := 0
	if tab.Result.Table != nil {
		rows = len(tab.Result.Table.Rows)
	}
	return fmt.Sprintf("%d|%d|%t|%d|%p/%d|%p/%d|%v",
		tabIndex, width, tab.RowDetail, tab.ResultCursorRow,
		tab.Result.Documents, len(tab.Result.Documents),
		tab.Result.Table, rows,
		tab.Result.Value)
}

// rowDetailLines transposes a single row into "FIELD  value" lines (one per
// column). The value is not width-capped so long values can be revealed by
// horizontal scrolling.
func rowDetailNameWidth(table result.Table) int {
	nameWidth := 0
	for _, column := range table.Columns {
		nameWidth = max(nameWidth, lipgloss.Width(column.Name))
	}
	return clamp(nameWidth, minDataTableCellCells, maxDataTableCellCells)
}

// rowDetailLines transposes one row into "FIELD  value" lines. A value that
// itself contains newlines is split across multiple grid rows (continuation
// rows leave the field column blank) so the cursor can traverse every line and
// long/multi-line values display fully.
func rowDetailLines(table result.Table, row int) []string {
	if len(table.Columns) == 0 {
		return []string{"(no columns)"}
	}
	row = clamp(row, 0, max(0, len(table.Rows)-1))
	nameWidth := rowDetailNameWidth(table)
	lines := make([]string, 0, len(table.Columns))
	for col, column := range table.Columns {
		value := ""
		if row < len(table.Rows) {
			value = strings.ReplaceAll(table.CellString(row, col), "\r", "")
		}
		valueLines := strings.Split(value, "\n")
		lines = append(lines, fitDataTableCell(column.Name, nameWidth)+"  "+valueLines[0])
		for _, vl := range valueLines[1:] {
			lines = append(lines, strings.Repeat(" ", nameWidth)+"  "+vl)
		}
	}
	return lines
}

// rowDetailColumnForLine maps a detail grid line back to its source column
// index (continuation lines map to the field they belong to).
func rowDetailColumnForLine(table result.Table, row, line int) int {
	if len(table.Columns) == 0 {
		return 0
	}
	row = clamp(row, 0, max(0, len(table.Rows)-1))
	idx := 0
	for col := range table.Columns {
		value := ""
		if row < len(table.Rows) {
			value = strings.ReplaceAll(table.CellString(row, col), "\r", "")
		}
		count := strings.Count(value, "\n") + 1
		if line < idx+count {
			return col
		}
		idx += count
	}
	return len(table.Columns) - 1
}

func dataTableLines(table result.Table) []string {
	if len(table.Columns) == 0 {
		return []string{"(no columns)"}
	}
	widths := dataTableColumnWidths(table)
	lines := make([]string, 0, len(table.Rows)+1)
	header := make([]string, 0, len(table.Columns))
	for col, column := range table.Columns {
		header = append(header, fitDataTableCell(column.Name, widths[col]))
	}
	lines = append(lines, strings.Join(header, "  "))
	for row := range table.Rows {
		cells := make([]string, 0, len(table.Columns))
		for col := range table.Columns {
			cells = append(cells, fitDataTableCell(flattenStatement(table.CellString(row, col)), widths[col]))
		}
		lines = append(lines, strings.Join(cells, "  "))
	}
	if len(table.Rows) == 0 {
		lines = append(lines, "(no rows)")
	}
	return lines
}

func dataTableColumnWidths(table result.Table) []int {
	widths := make([]int, len(table.Columns))
	for col, column := range table.Columns {
		widths[col] = clamp(lipgloss.Width(column.Name), minDataTableCellCells, maxDataTableCellCells)
	}
	for row := range table.Rows {
		for col := range table.Columns {
			widths[col] = clamp(max(widths[col], lipgloss.Width(flattenStatement(table.CellString(row, col)))), minDataTableCellCells, maxDataTableCellCells)
		}
	}
	return widths
}

func maxDataDisplayLineWidth(lines []string) int {
	width := 0
	for _, line := range lines {
		width = max(width, lipgloss.Width(line))
	}
	return width
}

func dataResultLines(set result.Set) []string {
	theme := defaultTheme()
	if len(set.Documents) > 0 {
		lines := make([]string, 0, len(set.Documents)*8)
		for _, doc := range set.Documents {
			raw, err := json.MarshalIndent(doc.Data, "", "  ")
			if err != nil {
				lines = append(lines, fmt.Sprint(doc.Data))
				continue
			}
			// Highlight AFTER wrapMongoObjectIDs so ObjectId(/ISODate( tokenize as
			// constructors. The colorized lines stay ANSI-safe through the viewport
			// because the data-grid slicing below is ANSI-aware (sliceColumns).
			colored := highlightJSONText(wrapMongoObjectIDs(string(raw)), theme)
			lines = append(lines, splitContentLines(colored)...)
		}
		return lines
	}
	raw, err := json.MarshalIndent(set.Value, "", "  ")
	if err != nil {
		return splitContentLines(fmt.Sprint(set.Value))
	}
	return splitContentLines(highlightJSONText(string(raw), theme))
}

func splitContentLines(content string) []string {
	trimmed := strings.TrimRight(content, "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func wrappedDataLines(lines []string, width int) []string {
	width = max(1, width)
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		line = limitCells(line, maxDataLineCells)
		if lipgloss.Width(line) <= width {
			wrapped = append(wrapped, line)
			continue
		}
		// ANSI-aware wrap: lines may carry JSON highlight color codes.
		for lipgloss.Width(line) > width {
			wrapped = append(wrapped, sliceColumns(line, 0, width))
			line = sliceColumns(line, width, lipgloss.Width(line))
		}
		if line != "" {
			wrapped = append(wrapped, line)
		}
	}
	return wrapped
}

func limitCells(line string, width int) string {
	if lipgloss.Width(line) <= width {
		return line
	}
	return sliceColumns(line, 0, max(1, width-3)) + "..."
}

func trimCellPrefix(value string, width int) string {
	if width <= 0 {
		return value
	}
	pos := 0
	for i, r := range value {
		partWidth := max(1, lipgloss.Width(string(r)))
		if pos+partWidth > width {
			return value[i:]
		}
		pos += partWidth
	}
	return ""
}
