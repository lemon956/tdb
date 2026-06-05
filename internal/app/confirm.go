package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const maxPendingSummary = 200

func pendingSummary(pending *pendingAction) string {
	if pending == nil {
		return ""
	}
	parts := []string{
		"action=" + pending.Kind,
		"target=" + pending.Target.String(),
	}
	if pending.ProfileID != "" {
		parts = append(parts, "profile="+pending.ProfileID)
	}
	if pending.Key.ID != "" || len(pending.Key.Columns) > 0 {
		parts = append(parts, "key="+safeJSON(map[string]any{
			"id":      pending.Key.ID,
			"columns": redactMap(pending.Key.Columns),
		}))
	}
	if len(pending.Values) > 0 {
		parts = append(parts, "values="+safeJSON(redactMap(pending.Values)))
	}
	return truncate(strings.Join(parts, " "))
}

func redactMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		if isSensitiveKey(key) {
			out[key] = "<redacted>"
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			out[key] = redactMap(nested)
			continue
		}
		if text, ok := value.(string); ok {
			out[key] = truncateValue(text)
			continue
		}
		out[key] = value
	}
	return out
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(key)
	return strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "credential")
}

func safeJSON(value any) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(stableMap(value)); err != nil {
		return fmt.Sprint(value)
	}
	return strings.TrimSpace(buffer.String())
}

func stableMap(value any) any {
	values, ok := value.(map[string]any)
	if !ok {
		return value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, key := range keys {
		out = append(out, map[string]any{"key": key, "value": stableMap(values[key])})
	}
	return out
}

func truncate(value string) string {
	if len(value) <= maxPendingSummary {
		return value
	}
	return value[:maxPendingSummary-3] + "..."
}

func truncateValue(value string) string {
	const maxValue = 60
	if len(value) <= maxValue {
		return value
	}
	return value[:maxValue-3] + "..."
}
