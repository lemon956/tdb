package sqlutil

import (
	"testing"

	"tdb/internal/config"
)

func TestBuildMySQLDSNUsesBasicDirectConnectionAndSortedParams(t *testing.T) {
	profile := config.Profile{
		Host:      "127.0.0.1",
		Port:      3306,
		User:      "root",
		Password:  "secret",
		Database:  "app",
		URIParams: "timeout=5s&charset=utf8mb4",
	}

	got := BuildMySQLDSN(profile)
	want := "root:secret@tcp(127.0.0.1:3306)/app?charset=utf8mb4&parseTime=true&timeout=5s"
	if got != want {
		t.Fatalf("BuildMySQLDSN() = %q, want %q", got, want)
	}
}

func TestBuildDorisDSNDefaultsDatabaseAndPort(t *testing.T) {
	profile := config.Profile{Host: "doris.local", User: "admin", Password: "pw"}

	got := BuildDorisDSN(profile)
	want := "admin:pw@tcp(doris.local:9030)/?parseTime=true"
	if got != want {
		t.Fatalf("BuildDorisDSN() = %q, want %q", got, want)
	}
}
