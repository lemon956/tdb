// Package ai shells out to a local AI CLI (Claude Code's `claude` or OpenAI
// Codex's `codex`) to answer database questions. It is intentionally thin: the
// CLI handles its own authentication, and TDB just feeds it a prompt and reads
// the reply. Conversation history is maintained by the caller and re-sent each
// turn, so this layer is stateless and tool-agnostic.
package ai

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner executes the CLI and returns its stdout. It is a field on Provider so
// tests can inject a fake without a real binary.
type Runner func(ctx context.Context, bin string, args []string) (string, error)

// Provider is a resolved local AI CLI.
type Provider struct {
	Name string // "claude" or "codex"
	Bin  string // absolute path from LookPath
	Run  Runner
}

// knownProviders is the lookup order when no preference is set.
var knownProviders = []string{"claude", "codex"}

// Detect resolves an AI CLI: the preferred one if present, else the first of
// knownProviders found on PATH. look defaults to exec.LookPath (injectable for
// tests).
func Detect(preferred string, look func(string) (string, error)) (*Provider, error) {
	if look == nil {
		look = exec.LookPath
	}
	try := func(name string) *Provider {
		if name == "" {
			return nil
		}
		if bin, err := look(name); err == nil && bin != "" {
			return &Provider{Name: name, Bin: bin, Run: defaultRun}
		}
		return nil
	}
	if p := try(preferred); p != nil {
		return p, nil
	}
	for _, name := range knownProviders {
		if p := try(name); p != nil {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no AI CLI found on PATH (looked for claude, codex)")
}

// argsFor builds the non-interactive (headless) invocation for each CLI.
func (p *Provider) argsFor(prompt string) []string {
	switch p.Name {
	case "codex":
		return []string{"exec", prompt}
	default: // claude: print mode
		return []string{"-p", prompt}
	}
}

// Ask sends prompt to the CLI and returns the trimmed reply.
func (p *Provider) Ask(ctx context.Context, prompt string) (string, error) {
	run := p.Run
	if run == nil {
		run = defaultRun
	}
	out, err := run(ctx, p.Bin, p.argsFor(prompt))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func defaultRun(ctx context.Context, bin string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%s: %v: %s", bin, err, firstLine(msg))
		}
		return "", fmt.Errorf("%s: %w", bin, err)
	}
	return stdout.String(), nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ExtractSQL pulls the first SQL statement out of a reply: a ```sql fenced block
// if present, else the first bare ``` fenced block, else the whole reply.
func ExtractSQL(reply string) string {
	if sql := fencedBlock(reply, "sql"); sql != "" {
		return sql
	}
	if sql := fencedBlock(reply, ""); sql != "" {
		return sql
	}
	return strings.TrimSpace(reply)
}

// ExtractSQLBlocks returns every fenced SQL block in order (```sql preferred,
// else bare ``` blocks). When there are no fences it falls back to the whole
// trimmed reply as a single block.
func ExtractSQLBlocks(reply string) []string {
	blocks := allFencedBlocks(reply, "sql")
	if len(blocks) == 0 {
		blocks = allFencedBlocks(reply, "")
	}
	if len(blocks) == 0 {
		if s := strings.TrimSpace(reply); s != "" {
			return []string{s}
		}
		return nil
	}
	return blocks
}

// fencedBlock returns the body of the first ```<lang> ... ``` block. lang=="" matches a bare ``` fence.
func fencedBlock(text, lang string) string {
	if blocks := allFencedBlocks(text, lang); len(blocks) > 0 {
		return blocks[0]
	}
	return ""
}

// allFencedBlocks returns the bodies of every ```<lang> ... ``` block in order.
// lang=="" matches bare ``` fences.
func allFencedBlocks(text, lang string) []string {
	lines := strings.Split(text, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		opened := false
		if lang == "" {
			opened = trimmed == "```"
		} else {
			opened = strings.EqualFold(trimmed, "```"+lang)
		}
		if !opened {
			continue
		}
		var body []string
		j := i + 1
		for ; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "```" {
				break
			}
			body = append(body, lines[j])
		}
		if block := strings.TrimSpace(strings.Join(body, "\n")); block != "" {
			out = append(out, block)
		}
		i = j // resume after the closing fence
	}
	return out
}
