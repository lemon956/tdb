package cli

import "testing"

func TestParseOptionsUsesExplicitConfigPath(t *testing.T) {
	opts, err := ParseOptions([]string{"--config", "/tmp/tdb.enc"})
	if err != nil {
		t.Fatalf("ParseOptions returned error: %v", err)
	}
	if opts.ConfigPath != "/tmp/tdb.enc" {
		t.Fatalf("ConfigPath = %q, want %q", opts.ConfigPath, "/tmp/tdb.enc")
	}
}

func TestParseOptionsRejectsUnknownFlag(t *testing.T) {
	_, err := ParseOptions([]string{"--unknown"})
	if err == nil {
		t.Fatal("ParseOptions returned nil error for unknown flag")
	}
}
