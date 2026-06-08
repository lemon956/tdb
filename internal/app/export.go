package app

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"tdb/internal/result"
)

// activeResult returns the result the user is currently looking at: the active
// workspace tab's result if it has one, otherwise the page-level result.
func (m *Model) activeResult() result.Set {
	if tab := m.activeWorkspaceTab(); tab != nil && !resultEmpty(tab.Result) {
		return tab.Result
	}
	return m.result
}

func resultEmpty(s result.Set) bool {
	return s.Table == nil && len(s.Documents) == 0 && s.Value == nil
}

func writeResult(set result.Set, format string, w *bytes.Buffer) error {
	switch format {
	case "csv":
		return set.WriteCSV(w)
	case "json":
		return set.WriteJSON(w)
	default:
		return fmt.Errorf("unknown format %q (use csv or json)", format)
	}
}

// exportResult handles ":export csv|json [path]" — writes the active result to a
// file (default tdb-export-<ts>.<ext>, mode 0600).
func (m *Model) exportResult(args []string) {
	if len(args) == 0 {
		m.message = "usage: export csv|json [path]"
		return
	}
	format := strings.ToLower(args[0])
	set := m.activeResult()
	if resultEmpty(set) {
		m.message = "no result to export"
		return
	}
	var buf bytes.Buffer
	if err := writeResult(set, format, &buf); err != nil {
		m.message = "export: " + err.Error()
		return
	}
	path := ""
	if len(args) >= 2 {
		path = args[1]
	} else {
		path = fmt.Sprintf("tdb-export-%d.%s", time.Now().UnixNano(), format)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		m.message = "export: " + err.Error()
		return
	}
	m.message = fmt.Sprintf("exported %s (%d bytes)", path, buf.Len())
}

// copyResultAs handles ":copy csv|json" — copies the active result to the
// clipboard in the chosen format.
func (m *Model) copyResultAs(args []string) {
	if len(args) == 0 {
		m.message = "usage: copy csv|json"
		return
	}
	format := strings.ToLower(args[0])
	set := m.activeResult()
	if resultEmpty(set) {
		m.message = "no result to copy"
		return
	}
	var buf bytes.Buffer
	if err := writeResult(set, format, &buf); err != nil {
		m.message = "copy: " + err.Error()
		return
	}
	m.copyText(buf.String())
	m.message = fmt.Sprintf("copied result as %s", format)
}
