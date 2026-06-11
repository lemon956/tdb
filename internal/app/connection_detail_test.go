package app

import (
	"strings"
	"testing"

	"tdb/internal/config"
)

func detailFor(t *testing.T, p config.Profile) string {
	t.Helper()
	m := newWorkspaceVimModel(t)
	m.page = PageConnections
	m.vault.Profiles = []config.Profile{p}
	m.connectionIndex = 0
	return stripANSI(m.connectionWorkspaceContent())
}

func TestConnectionDetailMongoUsesURIHostNotZeroPort(t *testing.T) {
	out := detailFor(t, config.Profile{
		ID: "test-mongo", Driver: config.DriverMongo,
		URIParams: "mongodb://u:secret@h.example:27017/db?authSource=admin",
		Database:  "pay_exposure", AuthDB: "admin", ReadOnly: true,
	})
	if strings.Contains(out, ":0") {
		t.Fatalf("mongo detail should not show :0 host:\n%s", out)
	}
	for _, want := range []string{"pay_exposure", "h.example:27017", "admin", "***"} {
		if !strings.Contains(out, want) {
			t.Fatalf("mongo detail missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret") {
		t.Fatalf("mongo detail must redact the password:\n%s", out)
	}
}

func TestConnectionDetailMySQLShowsFields(t *testing.T) {
	out := detailFor(t, config.Profile{
		ID: "test-mysql", Driver: config.DriverMySQL,
		Host: "10.0.0.1", Port: 3306, User: "root", Database: "app",
	})
	for _, want := range []string{"10.0.0.1:3306", "root", "app", "Database"} {
		if !strings.Contains(out, want) {
			t.Fatalf("mysql detail missing %q:\n%s", want, out)
		}
	}
}

func TestConnectionDetailRedisShowsDBIndex(t *testing.T) {
	out := detailFor(t, config.Profile{
		ID: "test-redis", Driver: config.DriverRedis,
		Host: "10.0.0.2", Port: 6379, RedisDB: 3,
	})
	if !strings.Contains(out, "DB") || !strings.Contains(out, "3") {
		t.Fatalf("redis detail should show DB index:\n%s", out)
	}
}

func TestConnectionDetailNeverShowsPassword(t *testing.T) {
	out := detailFor(t, config.Profile{
		ID: "p", Driver: config.DriverMySQL, Host: "h", Port: 3306, User: "u", Password: "topsecret",
	})
	if strings.Contains(out, "topsecret") {
		t.Fatalf("detail must never show the plaintext password:\n%s", out)
	}
}
