package app

import (
	"strings"
	"testing"

	"tdb/internal/db"
)

func TestPendingSummaryRedactsSensitiveFieldsAndTruncatesPayload(t *testing.T) {
	pending := &pendingAction{
		Kind:   "insert",
		Target: db.Target{Database: "app", Name: "users", Type: db.ObjectTable},
		Values: map[string]any{
			"password": "secret",
			"token":    "private-token",
			"note":     strings.Repeat("x", 200),
		},
	}

	got := pendingSummary(pending)
	if strings.Contains(got, "secret") || strings.Contains(got, "private-token") {
		t.Fatalf("summary leaked sensitive value: %s", got)
	}
	if !strings.Contains(got, "<redacted>") {
		t.Fatalf("summary missing redaction marker: %s", got)
	}
	if len(got) > 220 {
		t.Fatalf("summary was not truncated: len=%d summary=%s", len(got), got)
	}
}
