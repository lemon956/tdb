package redisadapter

import (
	"context"
	"errors"
	"testing"

	"tdb/internal/config"
	"tdb/internal/db"
)

func TestBuildRedisOptionsUsesBasicDirectConnection(t *testing.T) {
	opts := BuildRedisOptions(config.Profile{
		Host:     "127.0.0.1",
		Port:     6380,
		User:     "alice",
		Password: "secret",
		RedisDB:  2,
	})

	if opts.Addr != "127.0.0.1:6380" || opts.Username != "alice" || opts.Password != "secret" || opts.DB != 2 {
		t.Fatalf("redis options = %+v", opts)
	}
}

func TestNormalizeScanRequestCapsCountAndDefaultsPattern(t *testing.T) {
	got := NormalizeScanRequest(ScanRequest{Cursor: 9, Count: 5000})

	if got.Cursor != 9 || got.Pattern != "*" || got.Count != 500 {
		t.Fatalf("scan request = %+v, want cursor 9 pattern * count 500", got)
	}
}

func TestRedisValueMetadataSummarizesKey(t *testing.T) {
	metadata := RedisValueMetadata(RedisValue{
		Key:  "cache:user:1",
		Type: "hash",
		Hash: map[string]string{"id": "1", "name": "Ada"},
		TTL:  30,
	})

	if metadata.Attributes["key"] != "cache:user:1" {
		t.Fatalf("key attribute = %q", metadata.Attributes["key"])
	}
	if metadata.Attributes["type"] != "hash" {
		t.Fatalf("type attribute = %q", metadata.Attributes["type"])
	}
	if metadata.Attributes["length"] != "2" {
		t.Fatalf("length attribute = %q", metadata.Attributes["length"])
	}
	if metadata.Attributes["ttl"] != "30ns" {
		t.Fatalf("ttl attribute = %q", metadata.Attributes["ttl"])
	}
}

func TestAdapterRejectsWritesForReadOnlyProfile(t *testing.T) {
	adapter := NewWithClient(config.Profile{ReadOnly: true}, nil)

	_, err := adapter.Insert(context.Background(), db.Target{Name: "cache:user:1"}, map[string]any{
		"type":  "string",
		"value": "Ada",
	})
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Insert error = %v, want ErrReadOnly", err)
	}
}
