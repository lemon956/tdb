package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

// A1: crash safety. Bubble Tea recovers a panic only to restore the terminal and
// then exits the whole program. These helpers turn a panic in Update/View/async
// work into a recoverable error surfaced in the existing errorBox, so a single
// bad index no longer takes the app down.

// recoverToError converts a recovered panic value into an error carrying the
// panic message. The full stack is captured separately via logPanic.
func recoverToError(r any) error {
	switch v := r.(type) {
	case error:
		return v
	default:
		return fmt.Errorf("%v", v)
	}
}

// logPanic appends the panic and its stack trace to a panic log next to the
// config file, so users can attach it to a bug report. Best-effort: failures to
// write are ignored (we must not panic inside a recover path).
func (m *Model) logPanic(where string, r any) {
	path := m.panicLogPath()
	if path == "" {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n=== panic in %s @ %s ===\n", where, time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "%v\n", r)
	b.Write(debug.Stack())
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(b.String())
}

// panicLogPath returns the file panics are appended to, or "" when there is no
// config path to anchor it (e.g. some tests).
func (m *Model) panicLogPath() string {
	if m.options.ConfigPath == "" {
		return ""
	}
	dir := filepath.Dir(m.options.ConfigPath)
	return filepath.Join(dir, "tdb-panic.log")
}

// safeFrame is the fallback shown when View itself panics. View must not mutate
// errBox (it runs during render), so it only flags renderPanicked; the next
// Update converts that into a normal error box.
func safeFrame(r any) string {
	return "render error: " + fmt.Sprintf("%v", r) + "\n\n按任意键继续 (press any key to continue)"
}
