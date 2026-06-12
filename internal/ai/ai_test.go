package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExtractSQL(t *testing.T) {
	cases := []struct {
		name, reply, want string
	}{
		{"sql fence", "Sure:\n```sql\nSELECT 1;\n```\nHope it helps", "SELECT 1;"},
		{"upper fence", "```SQL\nSELECT 2\n```", "SELECT 2"},
		{"bare fence", "here:\n```\nSELECT 3\n```", "SELECT 3"},
		{"no fence", "SELECT 4", "SELECT 4"},
		{"prefers sql over bare", "```\nnope\n```\n```sql\nSELECT 5\n```", "SELECT 5"},
	}
	for _, c := range cases {
		if got := ExtractSQL(c.reply); got != c.want {
			t.Fatalf("%s: ExtractSQL = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestExtractSQLBlocks(t *testing.T) {
	reply := "First:\n```sql\nSELECT 1;\n```\nThen:\n```sql\nSELECT 2;\n```\ndone"
	got := ExtractSQLBlocks(reply)
	if len(got) != 2 || got[0] != "SELECT 1;" || got[1] != "SELECT 2;" {
		t.Fatalf("expected two ordered blocks, got %#v", got)
	}
	// No fences → whole reply as one block.
	if got := ExtractSQLBlocks("SELECT 9"); len(got) != 1 || got[0] != "SELECT 9" {
		t.Fatalf("no-fence fallback = %#v", got)
	}
	if got := ExtractSQLBlocks("   "); len(got) != 0 {
		t.Fatalf("blank reply should yield no blocks, got %#v", got)
	}
}

func TestExtractBlocksByLanguage(t *testing.T) {
	// Mongo: a ```js fence is extracted when js is in the language list.
	mongo := "Run:\n```js\ndb.users.find({active: true})\n```"
	if got := ExtractBlocks(mongo, []string{"js", "javascript", "sql"}); len(got) != 1 || got[0] != "db.users.find({active: true})" {
		t.Fatalf("```js block = %#v", got)
	}
	// Redis: a ```redis fence is extracted when redis is listed.
	redis := "Try:\n```redis\nHGETALL user:1\n```"
	if got := ExtractBlocks(redis, []string{"redis", "sh", "sql"}); len(got) != 1 || got[0] != "HGETALL user:1" {
		t.Fatalf("```redis block = %#v", got)
	}
	// A ```js fence is NOT pulled when only sql is requested → falls through to
	// the whole reply (no bare fence present here).
	if got := ExtractBlocks(mongo, []string{"sql"}); len(got) != 1 || !strings.Contains(got[0], "```js") {
		t.Fatalf("sql-only over ```js should fall back to whole reply, got %#v", got)
	}
	// First matching language wins; later ones are ignored.
	mixed := "```redis\nGET k\n```\n```sql\nSELECT 1\n```"
	if got := ExtractBlocks(mixed, []string{"sql", "redis"}); len(got) != 1 || got[0] != "SELECT 1" {
		t.Fatalf("first listed language should win, got %#v", got)
	}
	// Bare fence fallback still works.
	if got := ExtractBlocks("here:\n```\nGET k\n```", []string{"redis"}); len(got) != 1 || got[0] != "GET k" {
		t.Fatalf("bare fence fallback = %#v", got)
	}
}

func TestDetectPrefersPreferred(t *testing.T) {
	look := func(name string) (string, error) {
		switch name {
		case "claude":
			return "/usr/bin/claude", nil
		case "codex":
			return "/usr/bin/codex", nil
		}
		return "", errors.New("not found")
	}
	p, err := Detect("codex", look)
	if err != nil || p.Name != "codex" {
		t.Fatalf("preferred codex should win, got %+v err=%v", p, err)
	}
	// No preference → first known (claude) wins.
	p, err = Detect("", look)
	if err != nil || p.Name != "claude" {
		t.Fatalf("default should pick claude, got %+v err=%v", p, err)
	}
	// Preferred missing → fall back to whatever exists.
	onlyCodex := func(name string) (string, error) {
		if name == "codex" {
			return "/usr/bin/codex", nil
		}
		return "", errors.New("not found")
	}
	p, err = Detect("claude", onlyCodex)
	if err != nil || p.Name != "codex" {
		t.Fatalf("missing preferred should fall back to codex, got %+v err=%v", p, err)
	}
	// None present → error.
	if _, err := Detect("", func(string) (string, error) { return "", errors.New("nope") }); err == nil {
		t.Fatal("Detect should error when no CLI is found")
	}
}

func TestAskBuildsArgsPerProvider(t *testing.T) {
	var gotArgs []string
	run := func(_ context.Context, _ string, args []string) (string, error) {
		gotArgs = args
		return "  reply  \n", nil
	}
	claude := &Provider{Name: "claude", Bin: "/usr/bin/claude", Run: run}
	out, err := claude.Ask(context.Background(), "hello")
	if err != nil || out != "reply" {
		t.Fatalf("Ask = %q err=%v", out, err)
	}
	if strings.Join(gotArgs, " ") != "-p hello" {
		t.Fatalf("claude args = %v, want [-p hello]", gotArgs)
	}
	codex := &Provider{Name: "codex", Bin: "/usr/bin/codex", Run: run}
	if _, err := codex.Ask(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(gotArgs, " ") != "exec hi" {
		t.Fatalf("codex args = %v, want [exec hi]", gotArgs)
	}
}
