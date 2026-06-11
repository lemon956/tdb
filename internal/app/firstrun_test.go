package app

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/config"
)

func typeRunes(m *Model, s string) {
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

func TestFirstRunSetsAndConfirmsPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tdb.enc")
	m := NewModel(Options{ConfigPath: path})
	m.page = PageUnlock
	if !m.firstRun() {
		t.Fatal("a fresh config path should be a first run")
	}

	// Step 1: enter a new password.
	typeRunes(m, "hunter2")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.unlockConfirm {
		t.Fatal("first Enter should advance to the confirm step")
	}
	if m.input.Value() != "" {
		t.Fatalf("input should be cleared for confirmation, got %q", m.input.Value())
	}

	// Step 2: confirm with the same password.
	typeRunes(m, "hunter2")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.page != PageConnections {
		t.Fatalf("matching confirmation should enter connections, page=%s", m.page)
	}
	if m.master != "hunter2" {
		t.Fatalf("master should be set, got %q", m.master)
	}
	if !config.NewStore(path).Exists() {
		t.Fatal("the encrypted config should be persisted to disk")
	}
}

func TestFirstRunMismatchRestarts(t *testing.T) {
	m := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	m.page = PageUnlock

	typeRunes(m, "abc")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	typeRunes(m, "xyz")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if m.page != PageUnlock {
		t.Fatalf("a mismatch must not unlock, page=%s", m.page)
	}
	if m.unlockConfirm || m.unlockFirstPassword != "" {
		t.Fatal("a mismatch should reset back to the first entry")
	}
	if m.master != "" {
		t.Fatalf("no master should be set on mismatch, got %q", m.master)
	}
}

func TestExistingConfigUnlocksInOneStep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tdb.enc")
	// Seed an existing encrypted vault.
	if err := config.NewStore(path).Save("master", config.Vault{}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	m := NewModel(Options{ConfigPath: path})
	m.page = PageUnlock
	if m.firstRun() {
		t.Fatal("an existing config should not be a first run")
	}

	typeRunes(m, "master")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.page != PageConnections {
		t.Fatalf("correct password should unlock in one step, page=%s", m.page)
	}
}
