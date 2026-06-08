package result

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
)

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
	Table     *Table     `json:"table,omitempty"`
	Documents []Document `json:"documents,omitempty"`
	Value     any        `json:"value,omitempty"`
	// HasMore signals another page is available (pagination); NextCursor carries
	// the opaque cursor for it. Truncated signals the result was capped at
	// MaxResultRows to protect memory — distinct from page-has-more.
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
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

// WriteCSV writes the result as CSV. A Table becomes header + rows; Documents are
// emitted as a single "document" column of JSON objects; a scalar Value becomes a
// one-cell row. encoding/csv quotes fields containing commas/quotes/newlines.
func (s Set) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	switch {
	case s.Table != nil:
		header := make([]string, len(s.Table.Columns))
		for i, c := range s.Table.Columns {
			header[i] = c.Name
		}
		if err := cw.Write(header); err != nil {
			return err
		}
		for r := range s.Table.Rows {
			record := make([]string, len(s.Table.Columns))
			for c := range s.Table.Columns {
				record[c] = s.Table.CellString(r, c)
			}
			if err := cw.Write(record); err != nil {
				return err
			}
		}
	case len(s.Documents) > 0:
		if err := cw.Write([]string{"document"}); err != nil {
			return err
		}
		for _, doc := range s.Documents {
			raw, err := json.Marshal(doc.Data)
			if err != nil {
				return err
			}
			if err := cw.Write([]string{string(raw)}); err != nil {
				return err
			}
		}
	default:
		if err := cw.Write([]string{"value"}); err != nil {
			return err
		}
		if err := cw.Write([]string{fmt.Sprint(s.Value)}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// WriteJSON writes the result as indented JSON: a Table as an array of objects
// keyed by column name, Documents as an array of their data, otherwise the scalar
// Value.
func (s Set) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	switch {
	case s.Table != nil:
		rows := make([]map[string]any, len(s.Table.Rows))
		for r := range s.Table.Rows {
			obj := make(map[string]any, len(s.Table.Columns))
			for c, col := range s.Table.Columns {
				var v any
				if c < len(s.Table.Rows[r].Values) {
					v = s.Table.Rows[r].Values[c]
				}
				if b, ok := v.([]byte); ok {
					v = string(b)
				}
				obj[col.Name] = v
			}
			rows[r] = obj
		}
		return enc.Encode(rows)
	case len(s.Documents) > 0:
		docs := make([]map[string]any, len(s.Documents))
		for i, doc := range s.Documents {
			docs[i] = doc.Data
		}
		return enc.Encode(docs)
	default:
		return enc.Encode(s.Value)
	}
}
