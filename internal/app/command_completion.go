package app

import (
	"strings"

	"tdb/internal/suggest"
)

var globalCommandSuggestions = []suggest.Suggestion{
	{Value: "help", Detail: "open help"},
	{Value: "new", Detail: "create connection"},
	{Value: "edit", Detail: "edit connection"},
	{Value: "delete", Detail: "delete selected item"},
	{Value: "open", Detail: "open connection or object"},
	{Value: "test", Detail: "test connection"},
	{Value: "connections", Detail: "show connections"},
	{Value: "query", Detail: "create query tab"},
	{Value: "history", Detail: "show current connection history"},
	{Value: "refresh", Detail: "refresh active view"},
	{Value: "back", Detail: "go back"},
	{Value: "db", Detail: "expand database"},
	{Value: "next", Detail: "next Redis scan page"},
}

func (m *Model) openCommandSuggestions() {
	m.input.SetSuggestions(m.commandSuggestions())
}

func (m *Model) commandSuggestions() []suggest.Suggestion {
	token := strings.ToLower(currentCommandToken(m.input.Value()))
	out := make([]suggest.Suggestion, 0, 5)
	for _, candidate := range globalCommandSuggestions {
		if token != "" && !strings.HasPrefix(strings.ToLower(candidate.Value), token) {
			continue
		}
		item := candidate
		if item.Label == "" {
			item.Label = item.Value
		}
		out = append(out, item)
		if len(out) == 5 {
			break
		}
	}
	return out
}

func currentCommandToken(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	parts := splitLine(input)
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
