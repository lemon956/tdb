package app

import "testing"

func TestProgramOptionsEnableAltScreenAndMouse(t *testing.T) {
	options := programOptions()
	if len(options) != 2 {
		t.Fatalf("programOptions length = %d, want 2", len(options))
	}
}
