package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"tdb/internal/result"
)

func (m *Model) viewConnections(b *strings.Builder) {
	b.WriteString("Connections\n")
	if len(m.vault.Profiles) == 0 {
		b.WriteString("  no saved profiles\n")
	}
	for i, profile := range m.vault.Profiles {
		readOnly := ""
		if profile.ReadOnly {
			readOnly = " readonly"
		}
		b.WriteString(fmt.Sprintf("  %d. %s [%s] %s:%d%s\n", i+1, profile.ID, profile.Driver, profile.Host, profile.Port, readOnly))
	}
	b.WriteString("\nCommands:\n")
	b.WriteString("  new <driver> <id> <host> <port> <user> <password> <database|redis-db> [readonly]\n")
	b.WriteString("  edit <id> field=value ... | delete <id> | open <id> | test <id> | history\n")
}

func (m *Model) viewBrowser(b *strings.Builder) {
	profile := "<none>"
	if m.activeProfile != nil {
		profile = m.activeProfile.ID
	}
	b.WriteString("Browser: " + profile + "\n")
	b.WriteString("Databases:\n")
	for _, database := range m.databases {
		marker := " "
		if database == m.selectedDB {
			marker = "*"
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", marker, database))
	}
	b.WriteString("Objects:\n")
	for _, object := range m.objects {
		b.WriteString(fmt.Sprintf("  - %s [%s]\n", object.Name, object.Type))
	}
	b.WriteString("\nCommands: db <name> | open <object> | refresh | query | history | / <redis-pattern> | next | back\n")
}

func (m *Model) viewData(b *strings.Builder) {
	title := "Data"
	if m.workspaceMode == workspaceMetadata {
		title = "Metadata"
	}
	b.WriteString(title + ": " + m.target.String() + "\n")
	view := m.resultView
	if view.Height == 0 {
		view.Height = 12
	}
	if view.Width == 0 {
		view.Width = 8
	}
	view.MaxWidth = m.workspaceContentWidth()
	b.WriteString(view.Render(m.result))
	b.WriteString("\n")
}

func (m *Model) viewQuery(b *strings.Builder) {
	b.WriteString("Query Console\n")
	b.WriteString("SQL/Doris: enter SQL. Redis: enter command, quoted values allowed. Mongo: enter JSON {\"database\":\"app\",\"collection\":\"users\",\"filter\":{},\"limit\":100}.\n")
}

func (m *Model) viewHistory(b *strings.Builder) {
	b.WriteString("History\n")
	for i, entry := range historyEntries(m.history, m.activeProfileID()) {
		marker := " "
		if i == m.historyIndex {
			marker = ">"
		}
		b.WriteString(fmt.Sprintf("  %s %d. [%s] %s %s (%dms, affected=%d)\n", marker, i+1, entry.Status, entry.Action, entry.Statement, entry.DurationMillis, entry.AffectedRows))
		if entry.Error != "" {
			b.WriteString("     error: " + entry.Error + "\n")
		}
	}
}

func (m *Model) viewHelp(b *strings.Builder) {
	b.WriteString("Help\n")
	b.WriteString("TDB uses a command bar inside a full-screen TUI. Create a connection, open it, select a database/object, then query or mutate data.\n")
	b.WriteString("All writes create a pending action and require typing `yes`.\n")
}

func (m *Model) viewSuggestions(b *strings.Builder) {
	if !m.input.SuggestionsVisible() {
		return
	}
	b.WriteString("Suggestions:\n")
	suggestions := m.input.Suggestions()
	limit := len(suggestions)
	if limit > 6 {
		limit = 6
	}
	for idx := 0; idx < limit; idx++ {
		marker := " "
		if idx == m.input.SelectedIndex() {
			marker = ">"
		}
		b.WriteString(fmt.Sprintf("  %s %s", marker, suggestions[idx].Label))
		if suggestions[idx].Detail != "" {
			b.WriteString(" - " + suggestions[idx].Detail)
		}
		b.WriteString("\n")
	}
}

func renderResult(set result.Set) string {
	if set.Table != nil {
		return renderTable(*set.Table)
	}
	if len(set.Documents) > 0 {
		return renderDocuments(set.Documents)
	}
	raw, err := json.MarshalIndent(set.Value, "", "  ")
	if err != nil {
		return fmt.Sprint(set.Value)
	}
	return string(raw) + "\n"
}

func renderTable(table result.Table) string {
	var b strings.Builder
	for _, column := range table.Columns {
		b.WriteString(column.Name + "\t")
	}
	b.WriteString("\n")
	limit := len(table.Rows)
	if limit > 12 {
		limit = 12
	}
	for row := 0; row < limit; row++ {
		for column := range table.Columns {
			b.WriteString(table.CellString(row, column) + "\t")
		}
		b.WriteString("\n")
	}
	if len(table.Rows) > limit {
		b.WriteString(fmt.Sprintf("... %d more rows\n", len(table.Rows)-limit))
	}
	return b.String()
}

func renderDocuments(docs []result.Document) string {
	limit := len(docs)
	if limit > 8 {
		limit = 8
	}
	var b strings.Builder
	for i := 0; i < limit; i++ {
		raw, _ := json.MarshalIndent(docs[i].Data, "", "  ")
		b.Write(raw)
		b.WriteString("\n")
	}
	if len(docs) > limit {
		b.WriteString(fmt.Sprintf("... %d more documents\n", len(docs)-limit))
	}
	return b.String()
}
