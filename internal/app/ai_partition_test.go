package app

import (
	"strings"
	"testing"

	"tdb/internal/config"
	"tdb/internal/db"
)

// The Doris and MySQL prompts must steer the AI toward partition/time filtering
// so generated queries don't scan every partition (which Doris rejects).
func TestAIPromptPartitionGuardrails(t *testing.T) {
	doris := aiSystemPrompt(config.DriverDoris, false)
	for _, want := range []string{"partition", "WHERE"} {
		if !strings.Contains(doris, want) {
			t.Fatalf("doris prompt missing %q:\n%s", want, doris)
		}
	}
	mysql := aiSystemPrompt(config.DriverMySQL, false)
	if !strings.Contains(mysql, "partition") {
		t.Fatalf("mysql prompt should mention partition filtering:\n%s", mysql)
	}
}

// A @mentioned table's partition and key columns are fed to the AI so it knows
// what to filter on.
func TestBuildSchemaContextIncludesPartition(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.adapter = &metaStub{
		fields: []db.MetadataField{{Name: "id", Type: "bigint"}, {Name: "event_date", Type: "date"}},
		attrs:  map[string]string{"partition": "RANGE(event_date)", "key": "DUPLICATE KEY(id, event_date)"},
	}
	m.selectedDB = "app"
	m.databaseObjects = map[string][]db.Object{
		m.scopeKey("", "app"): {{Name: "orders", Type: db.ObjectTable}},
	}

	ctx := m.buildSchemaContext("show recent @orders")
	if !strings.Contains(ctx, "Columns of orders:") {
		t.Fatalf("schema context should still include columns:\n%s", ctx)
	}
	if !strings.Contains(ctx, "Partitioning of orders:") {
		t.Fatalf("schema context should include partitioning:\n%s", ctx)
	}
	if !strings.Contains(ctx, "RANGE(event_date)") || !strings.Contains(ctx, "DUPLICATE KEY(id, event_date)") {
		t.Fatalf("schema context should carry partition + key columns:\n%s", ctx)
	}
}

// Column comments (which often document enum values) are surfaced to the AI so
// it uses real domain values instead of guessing.
func TestBuildSchemaContextIncludesFieldNotes(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.adapter = &metaStub{fields: []db.MetadataField{
		{Name: "id", Type: "bigint"},
		{Name: "status", Type: "tinyint", Comment: "1=active 2=closed 3=banned"},
	}}
	m.selectedDB = "app"
	m.databaseObjects = map[string][]db.Object{
		m.scopeKey("", "app"): {{Name: "users", Type: db.ObjectTable}},
	}

	ctx := m.buildSchemaContext("active @users")
	if !strings.Contains(ctx, "Field notes of users") {
		t.Fatalf("schema context should include field notes:\n%s", ctx)
	}
	if !strings.Contains(ctx, "status — 1=active 2=closed 3=banned") {
		t.Fatalf("field notes should carry the column comment:\n%s", ctx)
	}
}

// A table with no commented columns adds no Field notes line.
func TestBuildSchemaContextNoFieldNotes(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.adapter = &metaStub{fields: []db.MetadataField{{Name: "id", Type: "int"}}}
	m.selectedDB = "app"
	m.databaseObjects = map[string][]db.Object{
		m.scopeKey("", "app"): {{Name: "plain", Type: db.ObjectTable}},
	}
	if ctx := m.buildSchemaContext("@plain"); strings.Contains(ctx, "Field notes of") {
		t.Fatalf("uncommented table should not add field notes:\n%s", ctx)
	}
}

// An unpartitioned table (no Attributes) adds no Partitioning line.
func TestBuildSchemaContextNoPartition(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.adapter = &metaStub{fields: []db.MetadataField{{Name: "id", Type: "int"}}}
	m.selectedDB = "app"
	m.databaseObjects = map[string][]db.Object{
		m.scopeKey("", "app"): {{Name: "small", Type: db.ObjectTable}},
	}

	ctx := m.buildSchemaContext("count @small")
	if strings.Contains(ctx, "Partitioning of small:") {
		t.Fatalf("unpartitioned table should not add a partitioning line:\n%s", ctx)
	}
}
