package terminal

import (
	"bytes"
	"testing"
)

func TestScreenEnterAndExitWriteAlternateScreenSequences(t *testing.T) {
	var out bytes.Buffer
	screen := NewScreen(&out)

	if err := screen.Enter(); err != nil {
		t.Fatalf("Enter returned error: %v", err)
	}
	if err := screen.Exit(); err != nil {
		t.Fatalf("Exit returned error: %v", err)
	}

	got := out.String()
	want := "\x1b[?1049h\x1b[?25l\x1b[2J\x1b[H\x1b[?25h\x1b[?1049l"
	if got != want {
		t.Fatalf("screen output = %q, want %q", got, want)
	}
}
