package history

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStoreConcurrentAppendsDoNotLoseEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	store := NewStore(path)
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := store.Append(Entry{ID: fmt.Sprintf("h%d", i), ProfileID: "p", Driver: "mysql", Action: ActionQuery}, nil); err != nil {
				t.Errorf("Append %d returned error: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	entries, err := store.List("p", 0)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(entries) != n {
		t.Fatalf("entries length = %d, want %d (concurrent appends lost entries)", len(entries), n)
	}
}

func TestStoreAppendsMetadataWithoutPersistingResults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	store := NewStore(path)
	entry := Entry{
		ID:             "h1",
		ProfileID:      "mysql-local",
		Driver:         "mysql",
		Database:       "app",
		Action:         ActionQuery,
		Statement:      "select * from users",
		Status:         StatusOK,
		AffectedRows:   2,
		DurationMillis: 15,
		StartedAt:      time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}

	if err := store.Append(entry, "private-result-payload"); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if strings.Contains(string(raw), "private-result-payload") {
		t.Fatalf("history file persisted result payload: %s", string(raw))
	}

	entries, err := store.List("mysql-local", 10)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries length = %d, want 1", len(entries))
	}
	if entries[0].Statement != "select * from users" || entries[0].AffectedRows != 2 || entries[0].Database != "app" {
		t.Fatalf("unexpected entry: %+v", entries[0])
	}
}

func TestStoreListsNewestFirstWithProfileFilterAndLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	store := NewStore(path)
	entries := []Entry{
		{ID: "old", ProfileID: "redis-local", StartedAt: time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)},
		{ID: "other", ProfileID: "mongo-local", StartedAt: time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)},
		{ID: "new", ProfileID: "redis-local", StartedAt: time.Date(2026, 6, 1, 11, 0, 0, 0, time.UTC)},
	}
	for _, entry := range entries {
		if err := store.Append(entry, nil); err != nil {
			t.Fatalf("Append(%s) returned error: %v", entry.ID, err)
		}
	}

	got, err := store.List("redis-local", 1)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("filtered history = %+v, want newest redis entry only", got)
	}
}
