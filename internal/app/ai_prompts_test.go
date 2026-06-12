package app

import (
	"strings"
	"testing"

	"tdb/internal/config"
)

// Each driver gets a dialect-appropriate system prompt with an extractable fence
// and safety guardrails.
func TestAISystemPromptPerDriver(t *testing.T) {
	cases := []struct {
		driver   config.Driver
		contains []string
	}{
		{config.DriverMySQL, []string{"MySQL", "LIMIT", "```sql"}},
		{config.DriverDoris, []string{"Doris", "catalog", "```sql"}},
		{config.DriverMongo, []string{"db.<collection>", "aggregate", "```js"}},
		{config.DriverRedis, []string{"Redis", "SCAN", "```redis"}},
	}
	for _, c := range cases {
		got := aiSystemPrompt(c.driver, false)
		for _, want := range c.contains {
			if !strings.Contains(got, want) {
				t.Fatalf("%s prompt missing %q:\n%s", c.driver, want, got)
			}
		}
	}
	// The Mongo prompt must steer away from unsupported method chaining.
	if !strings.Contains(aiSystemPrompt(config.DriverMongo, false), "chain") {
		t.Fatal("mongo prompt should warn against method chaining")
	}
	// SQL drivers must not be told to emit Redis/Mongo syntax.
	if strings.Contains(aiSystemPrompt(config.DriverMySQL, false), "```redis") {
		t.Fatal("mysql prompt should not mention a redis fence")
	}
}

// The read-only guardrail is appended only when the connection forbids writes.
func TestAISystemPromptReadOnly(t *testing.T) {
	if strings.Contains(aiSystemPrompt(config.DriverMySQL, false), "READ-ONLY") {
		t.Fatal("writable connection should not get the read-only guardrail")
	}
	ro := aiSystemPrompt(config.DriverMySQL, true)
	if !strings.Contains(ro, "READ-ONLY") {
		t.Fatalf("read-only connection should get the guardrail:\n%s", ro)
	}
}

// The extractor language order matches the driver so natural fences are pulled.
func TestAIFenceLangs(t *testing.T) {
	if got := aiFenceLangs(config.DriverMongo); got[0] != "js" {
		t.Fatalf("mongo should prefer js fences, got %v", got)
	}
	if got := aiFenceLangs(config.DriverRedis); got[0] != "redis" {
		t.Fatalf("redis should prefer redis fences, got %v", got)
	}
	for _, d := range []config.Driver{config.DriverMySQL, config.DriverDoris} {
		if got := aiFenceLangs(d); len(got) != 1 || got[0] != "sql" {
			t.Fatalf("%s should use sql fences only, got %v", d, got)
		}
	}
}

// buildAIPrompt embeds the supplied system prompt verbatim.
func TestBuildAIPromptIncludesSystem(t *testing.T) {
	system := aiSystemPrompt(config.DriverRedis, false)
	prompt := buildAIPrompt(system, nil, "", "how big is the keyspace?")
	if !strings.Contains(prompt, system) {
		t.Fatalf("prompt should embed the driver system prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "how big is the keyspace?") {
		t.Fatal("prompt should include the user question")
	}
}

// profileReadOnly reflects the active connection's flag.
func TestProfileReadOnly(t *testing.T) {
	m := newWorkspaceVimModel(t)
	if m.profileReadOnly() {
		t.Fatal("default test profile is writable")
	}
	m.activeProfile.ReadOnly = true
	if !m.profileReadOnly() {
		t.Fatal("should report read-only when the profile forbids writes")
	}
}
