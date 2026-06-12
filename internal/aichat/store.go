// Package aichat persists AI assistant conversations to a plain JSON file so
// chats survive restarts. It mirrors internal/history's storage approach
// (mutex-guarded read-merge-write, 0o600), but keys sessions by a unique ID
// rather than collapsing them by fingerprint — conversations are not idempotent.
package aichat

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Message is one turn in a conversation. Role is "you", "ai", or "err".
type Message struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// Session is a single conversation, scoped to a connection + database.
type Session struct {
	ID        string    `json:"id"`
	ProfileID string    `json:"profile_id"`
	Scope     string    `json:"scope"`    // scopeKey(catalog, database)
	DBLabel   string    `json:"db_label"` // human label for the list, e.g. "catalog.db"
	Title     string    `json:"title"`
	Messages  []Message `json:"messages"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Store struct {
	mu   sync.Mutex
	path string
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

// NewID returns a short random hex id for a new session.
func NewID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should never fail; fall back to a time-based id.
		return hex.EncodeToString([]byte(time.Now().UTC().Format("150405.000000")))
	}
	return hex.EncodeToString(b[:])
}

// Load returns every stored session, newest UpdatedAt first.
func (s *Store) Load() ([]Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessions, err := s.read()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

// Upsert replaces the session with the same ID, or appends it, then writes the
// whole file back.
func (s *Store) Upsert(sess Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessions, err := s.read()
	if err != nil {
		return err
	}
	for i := range sessions {
		if sessions[i].ID == sess.ID {
			sessions[i] = sess
			return s.write(sessions)
		}
	}
	return s.write(append(sessions, sess))
}

// Delete drops the session with id (a no-op if absent).
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessions, err := s.read()
	if err != nil {
		return err
	}
	out := sessions[:0]
	for _, sess := range sessions {
		if sess.ID != id {
			out = append(out, sess)
		}
	}
	return s.write(out)
}

func (s *Store) read() ([]Session, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read ai chat history: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var sessions []Session
	if err := json.Unmarshal(raw, &sessions); err != nil {
		return nil, fmt.Errorf("parse ai chat history: %w", err)
	}
	return sessions, nil
}

func (s *Store) write(sessions []Session) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create ai chat directory: %w", err)
	}
	raw, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ai chat history: %w", err)
	}
	if err := os.WriteFile(s.path, raw, 0o600); err != nil {
		return fmt.Errorf("write ai chat history: %w", err)
	}
	return nil
}
