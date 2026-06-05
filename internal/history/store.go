package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Action string

const (
	ActionQuery  Action = "query"
	ActionInsert Action = "insert"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
)

type Status string

const (
	StatusOK    Status = "ok"
	StatusError Status = "error"
)

type Entry struct {
	ID             string `json:"id"`
	ProfileID      string `json:"profile_id"`
	Driver         string `json:"driver"`
	Database       string `json:"database,omitempty"`
	Action         Action `json:"action"`
	Statement      string `json:"statement"`
	Status         Status `json:"status"`
	Error          string `json:"error,omitempty"`
	AffectedRows   int64  `json:"affected_rows"`
	DurationMillis int64  `json:"duration_millis"`
	// StartedAt is the most recent execution time (merged entries keep the latest).
	StartedAt time.Time `json:"started_at"`
	// ExecutionCount is the hotness: how many executions merged into this entry.
	ExecutionCount int64 `json:"execution_count,omitempty"`
}

type Store struct {
	mu   sync.Mutex
	path string
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Append(entry Entry, _ any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.readRaw()
	if err != nil {
		return err
	}
	if entry.StartedAt.IsZero() {
		entry.StartedAt = time.Now().UTC()
	}
	if entry.ExecutionCount <= 0 {
		entry.ExecutionCount = 1
	}
	entries = append(entries, entry)
	// Merge on write so identical queries (ignoring literal values) collapse into
	// a single hot entry and the file stays compact.
	return s.write(mergeEntries(entries))
}

func (s *Store) List(profileID string, limit int) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.read()
	if err != nil {
		return nil, err
	}
	filtered := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if profileID == "" || entry.ProfileID == profileID {
			filtered = append(filtered, entry)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].StartedAt.After(filtered[j].StartedAt)
	})
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

func (s *Store) ListByDriver(driver string, limit int) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.read()
	if err != nil {
		return nil, err
	}
	filtered := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if driver == "" || entry.Driver == driver {
			filtered = append(filtered, entry)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].StartedAt.After(filtered[j].StartedAt)
	})
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

// read returns the merged view (identical queries collapsed, hotness summed) so
// every reader sees the deduplicated history, including legacy files written
// before merge-on-write existed.
func (s *Store) read() ([]Entry, error) {
	entries, err := s.readRaw()
	if err != nil {
		return nil, err
	}
	return mergeEntries(entries), nil
}

// readRaw returns the entries exactly as stored on disk.
func (s *Store) readRaw() ([]Entry, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parse history: %w", err)
	}
	return entries, nil
}

// mergeEntries collapses entries that represent the same logical query — same
// driver, action and statement fingerprint (literal values ignored). The merged
// entry sums ExecutionCount (legacy entries count as 1) and carries the metadata
// of the most recent execution. Entries with an empty fingerprint (no statement)
// pass through untouched so unrelated records are never combined.
func mergeEntries(entries []Entry) []Entry {
	merged := make([]Entry, 0, len(entries))
	index := make(map[string]int, len(entries))
	for _, entry := range entries {
		count := entry.ExecutionCount
		if count <= 0 {
			count = 1
		}
		fp := Fingerprint(entry.Statement)
		if fp == "" {
			entry.ExecutionCount = count
			merged = append(merged, entry)
			continue
		}
		key := entry.Driver + "\x00" + string(entry.Action) + "\x00" + fp
		if i, ok := index[key]; ok {
			existing := merged[i]
			total := existing.ExecutionCount + count
			if !existing.StartedAt.After(entry.StartedAt) {
				// entry is the more recent execution: take its metadata.
				entry.ExecutionCount = total
				merged[i] = entry
			} else {
				existing.ExecutionCount = total
				merged[i] = existing
			}
			continue
		}
		entry.ExecutionCount = count
		index[key] = len(merged)
		merged = append(merged, entry)
	}
	return merged
}

func (s *Store) write(entries []Entry) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create history directory: %w", err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal history: %w", err)
	}
	if err := os.WriteFile(s.path, raw, 0o600); err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	return nil
}
