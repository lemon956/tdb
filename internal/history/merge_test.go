package history

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFingerprintNormalizesLiterals(t *testing.T) {
	cases := [][2]string{
		{"SELECT * FROM users WHERE id = 1", "SELECT * FROM users WHERE id = 2"},
		{"select * from t where name = 'a'", "SELECT * FROM t WHERE name = 'b'"},
		{"SELECT 1", "select   1 ;"},
		{"db.c.find({room_id: 1})", "db.c.find({room_id: 9999})"},
	}
	for _, c := range cases {
		if Fingerprint(c[0]) != Fingerprint(c[1]) {
			t.Fatalf("fingerprints differ:\n  %q -> %q\n  %q -> %q", c[0], Fingerprint(c[0]), c[1], Fingerprint(c[1]))
		}
	}
	// Digits embedded in identifiers must NOT be normalized away.
	if Fingerprint("SELECT * FROM users2") == Fingerprint("SELECT * FROM users3") {
		t.Fatalf("identifier digits should not be treated as literals")
	}
	// Genuinely different queries keep different fingerprints.
	if Fingerprint("SELECT id FROM a") == Fingerprint("SELECT id FROM b") {
		t.Fatalf("different tables should not merge")
	}
}

func TestStoreMergesByFingerprintAndCountsHotness(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "history.json"))
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	for i, stmt := range []string{
		"SELECT * FROM users WHERE id = 1",
		"SELECT * FROM users WHERE id = 2",
		"SELECT * FROM users WHERE id = 3",
	} {
		entry := Entry{
			ProfileID: "p", Driver: "mysql", Action: ActionQuery,
			Statement: stmt, Status: StatusOK,
			StartedAt: base.Add(time.Duration(i) * time.Minute),
		}
		if err := store.Append(entry, nil); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	got, err := store.List("p", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("entries = %d, want 1 merged hot entry: %+v", len(got), got)
	}
	if got[0].ExecutionCount != 3 {
		t.Fatalf("ExecutionCount = %d, want 3", got[0].ExecutionCount)
	}
	// Metadata + statement come from the most recent execution.
	if got[0].Statement != "SELECT * FROM users WHERE id = 3" {
		t.Fatalf("Statement = %q, want the latest concrete query", got[0].Statement)
	}
	if !got[0].StartedAt.Equal(base.Add(2 * time.Minute)) {
		t.Fatalf("StartedAt = %v, want latest execution time", got[0].StartedAt)
	}
}

func TestStoreDoesNotMergeDifferentQueries(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "history.json"))
	for _, stmt := range []string{"SELECT id FROM a", "SELECT id FROM b"} {
		if err := store.Append(Entry{ProfileID: "p", Driver: "mysql", Action: ActionQuery, Statement: stmt}, nil); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	got, err := store.List("p", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2 distinct queries", len(got))
	}
}
