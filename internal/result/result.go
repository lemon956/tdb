package result

import "fmt"

type Column struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

type Row struct {
	Values []any `json:"values"`
}

type Table struct {
	Columns []Column `json:"columns"`
	Rows    []Row    `json:"rows"`
}

type Document struct {
	ID   string         `json:"id"`
	Data map[string]any `json:"data"`
}

type Set struct {
	Table      *Table     `json:"table,omitempty"`
	Documents  []Document `json:"documents,omitempty"`
	Value      any        `json:"value,omitempty"`
	HasMore    bool       `json:"has_more"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

type MutationResult struct {
	AffectedRows int64 `json:"affected_rows"`
}

func NewMutationResult(affectedRows int64) MutationResult {
	return MutationResult{AffectedRows: affectedRows}
}

func (t Table) CellString(rowIndex, columnIndex int) string {
	if rowIndex < 0 || rowIndex >= len(t.Rows) {
		return ""
	}
	values := t.Rows[rowIndex].Values
	if columnIndex < 0 || columnIndex >= len(values) {
		return ""
	}
	value := values[columnIndex]
	if value == nil {
		return "NULL"
	}
	if bytes, ok := value.([]byte); ok {
		return string(bytes)
	}
	return fmt.Sprint(value)
}
