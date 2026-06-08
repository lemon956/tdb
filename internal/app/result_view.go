package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"tdb/internal/result"
)

type ResultView struct {
	RowOffset    int
	ColumnOffset int
	Height       int
	Width        int
	// MaxWidth, when > 0, bounds the rendered width in display cells. Columns are
	// packed to fit it and every line is clipped to it so wide values never wrap.
	MaxWidth int
	// Selectable highlights SelectedRow as the current row (query result list).
	Selectable bool
	// MarkCursor shows SelectedRow with a dimmed highlight even when the view is
	// not Selectable (not focused), so the cursor position stays visible.
	MarkCursor  bool
	SelectedRow int
	// SelectionActive highlights every row in [SelectionStart, SelectionEnd] as a
	// row-visual ("copy mode") selection. SelectedRow stays the brightest within
	// the range so the cursor position is still distinguishable.
	SelectionActive bool
	SelectionStart  int
	SelectionEnd    int
}

func (v *ResultView) Reset() {
	v.RowOffset = 0
	v.ColumnOffset = 0
}

func (v *ResultView) ScrollRows(delta int, total int) {
	v.RowOffset = clamp(v.RowOffset+delta, 0, max(0, total-1))
}

func (v *ResultView) ScrollColumns(delta int, total int) {
	v.ColumnOffset = clamp(v.ColumnOffset+delta, 0, max(0, total-1))
}

func (v ResultView) Render(set result.Set) string {
	if set.Table != nil {
		return v.renderTable(*set.Table)
	}
	if len(set.Documents) > 0 {
		return v.renderDocuments(set.Documents)
	}
	raw, err := json.MarshalIndent(set.Value, "", "  ")
	if err != nil {
		return fmt.Sprint(set.Value)
	}
	return string(raw) + "\n"
}

func (v ResultView) renderTable(table result.Table) string {
	height := v.Height
	if height <= 0 {
		height = 12
	}
	width := v.Width
	if width <= 0 {
		width = len(table.Columns)
	}
	rowStart := clamp(v.RowOffset, 0, max(0, len(table.Rows)-1))
	rowEnd := min(len(table.Rows), rowStart+height)
	colStart := clamp(v.ColumnOffset, 0, max(0, len(table.Columns)-1))

	// Per-column display width from header + visible cells, capped so one wide
	// value cannot dominate. When a pixel budget is known, also cap each column
	// to it so a single column always fits.
	maxColWidth := 32
	if v.MaxWidth > 0 && v.MaxWidth < maxColWidth {
		maxColWidth = v.MaxWidth
	}
	colWidth := make([]int, len(table.Columns))
	for col := colStart; col < len(table.Columns); col++ {
		w := lipgloss.Width(table.Columns[col].Name)
		for row := rowStart; row < rowEnd; row++ {
			if cw := lipgloss.Width(flattenStatement(table.CellString(row, col))); cw > w {
				w = cw
			}
		}
		colWidth[col] = clamp(w, 1, maxColWidth)
	}

	const sep = "  "
	const sepWidth = 2
	// Decide how many columns fit. With a pixel budget, pack columns until the
	// budget is exhausted; otherwise fall back to the column-count window.
	colEnd := colStart
	if v.MaxWidth > 0 {
		used := 0
		for col := colStart; col < len(table.Columns); col++ {
			add := colWidth[col]
			if col > colStart {
				add += sepWidth
			}
			if used+add > v.MaxWidth && col > colStart {
				break
			}
			used += add
			colEnd = col + 1
		}
	} else {
		colEnd = min(len(table.Columns), colStart+width)
	}

	theme := defaultTheme()
	var b strings.Builder

	var header strings.Builder
	for col := colStart; col < colEnd; col++ {
		if col > colStart {
			header.WriteString(sep)
		}
		header.WriteString(fitDataTableCell(table.Columns[col].Name, colWidth[col]))
	}
	b.WriteString(theme.tableHead.Render(header.String()) + "\n")

	for row := rowStart; row < rowEnd; row++ {
		var line strings.Builder
		for col := colStart; col < colEnd; col++ {
			if col > colStart {
				line.WriteString(sep)
			}
			line.WriteString(fitDataTableCell(flattenStatement(table.CellString(row, col)), colWidth[col]))
		}
		rowText := line.String()
		inSelection := v.SelectionActive && row >= v.SelectionStart && row <= v.SelectionEnd
		switch {
		case v.Selectable && row == v.SelectedRow:
			rowText = theme.selected.Render(rowText)
		case inSelection:
			rowText = theme.visual.Render(rowText)
		case v.MarkCursor && row == v.SelectedRow:
			rowText = theme.selectedDim.Render(rowText)
		case row%2 == 1:
			rowText = theme.rowAlt.Render(rowText)
		}
		b.WriteString(rowText + "\n")
	}
	if len(table.Rows) > 0 {
		b.WriteString(theme.muted.Render(fmt.Sprintf("row %d-%d of %d, column %d-%d of %d", rowStart+1, rowEnd, len(table.Rows), colStart+1, colEnd, len(table.Columns))) + "\n")
	}
	out := b.String()
	if v.MaxWidth > 0 {
		// Defensive clip so no styled line can exceed the budget and wrap.
		out = truncateLines(out, v.MaxWidth)
	}
	return out
}

func (v ResultView) renderDocuments(docs []result.Document) string {
	height := v.Height
	if height <= 0 {
		height = 8
	}
	start := clamp(v.RowOffset, 0, max(0, len(docs)-1))
	end := min(len(docs), start+height)
	theme := defaultTheme()
	var b strings.Builder
	for idx := start; idx < end; idx++ {
		if idx > start {
			b.WriteString(theme.muted.Render(strings.Repeat("─", 24)) + "\n")
		}
		raw, _ := json.MarshalIndent(docs[idx].Data, "", "  ")
		b.Write(raw)
		b.WriteString("\n")
	}
	if len(docs) > 0 {
		b.WriteString(theme.muted.Render(fmt.Sprintf("document %d-%d of %d", start+1, end, len(docs))) + "\n")
	}
	out := b.String()
	if v.MaxWidth > 0 {
		out = truncateLines(out, v.MaxWidth)
	}
	return out
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
