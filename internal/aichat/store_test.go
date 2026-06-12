package aichat

import (
	"path/filepath"
	"testing"
	"time"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "tdb.enc.aichat.json"))
}

func TestUpsertLoadRoundTrip(t *testing.T) {
	s := tempStore(t)
	now := time.Now().UTC()
	sess := Session{
		ID:        "a",
		ProfileID: "p1",
		Scope:     "app",
		Title:     "count users",
		Messages:  []Message{{Role: "you", Text: "how many users?", At: now}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.Upsert(sess); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a" || got[0].Title != "count users" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if len(got[0].Messages) != 1 || got[0].Messages[0].Text != "how many users?" {
		t.Fatalf("messages not persisted: %+v", got[0].Messages)
	}
}

func TestUpsertSameIDReplaces(t *testing.T) {
	s := tempStore(t)
	now := time.Now().UTC()
	_ = s.Upsert(Session{ID: "a", Title: "first", UpdatedAt: now})
	_ = s.Upsert(Session{ID: "a", Title: "second", UpdatedAt: now.Add(time.Minute)})
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("same ID should not duplicate, got %d", len(got))
	}
	if got[0].Title != "second" {
		t.Fatalf("Upsert should replace, got title %q", got[0].Title)
	}
}

func TestLoadSortsNewestFirst(t *testing.T) {
	s := tempStore(t)
	now := time.Now().UTC()
	_ = s.Upsert(Session{ID: "old", UpdatedAt: now.Add(-time.Hour)})
	_ = s.Upsert(Session{ID: "new", UpdatedAt: now})
	got, _ := s.Load()
	if len(got) != 2 || got[0].ID != "new" {
		t.Fatalf("expected newest first, got %+v", got)
	}
}

func TestDelete(t *testing.T) {
	s := tempStore(t)
	_ = s.Upsert(Session{ID: "a", UpdatedAt: time.Now().UTC()})
	_ = s.Upsert(Session{ID: "b", UpdatedAt: time.Now().UTC()})
	if err := s.Delete("a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := s.Load()
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("Delete should drop only a, got %+v", got)
	}
	// Deleting an absent id is a no-op, not an error.
	if err := s.Delete("missing"); err != nil {
		t.Fatalf("Delete absent: %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	s := tempStore(t) // file does not exist yet
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load missing should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("missing file should load empty, got %d", len(got))
	}
}

func TestNewIDUnique(t *testing.T) {
	a, b := NewID(), NewID()
	if a == b {
		t.Fatal("NewID should not collide")
	}
	if a == "" {
		t.Fatal("NewID should be non-empty")
	}
}
