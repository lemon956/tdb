package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type ClipboardCopier interface {
	Copy(text string) error
}

type clipboardCopierFunc func(string) error

func (f clipboardCopierFunc) Copy(text string) error {
	return f(text)
}

type clipboardCommand struct {
	Name string
	Args []string
}

type systemClipboardCopier struct {
	goos    string
	timeout time.Duration
}

func NewSystemClipboardCopier() ClipboardCopier {
	return systemClipboardCopier{goos: runtime.GOOS, timeout: 2 * time.Second}
}

func (c systemClipboardCopier) Copy(text string) error {
	commands := clipboardCommandCandidates(c.goos)
	if len(commands) == 0 {
		return fmt.Errorf("unsupported clipboard OS %q", c.goos)
	}
	timeout := c.timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	failures := make([]string, 0, len(commands))
	for _, command := range commands {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := runClipboardCommand(ctx, command, text)
		cancel()
		if err == nil {
			return nil
		}
		failures = append(failures, command.String()+": "+err.Error())
	}
	return errors.New(strings.Join(failures, "; "))
}

func runClipboardCommand(ctx context.Context, command clipboardCommand, text string) error {
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func clipboardCommandCandidates(goos string) []clipboardCommand {
	switch goos {
	case "darwin":
		return []clipboardCommand{{Name: "pbcopy"}}
	case "linux", "freebsd", "openbsd", "netbsd":
		return []clipboardCommand{
			{Name: "wl-copy"},
			{Name: "xclip", Args: []string{"-selection", "clipboard"}},
			{Name: "xsel", Args: []string{"--clipboard", "--input"}},
		}
	case "windows":
		return []clipboardCommand{
			{Name: "clip.exe"},
			{Name: "powershell.exe", Args: []string{"-NoProfile", "-Command", "Set-Clipboard"}},
		}
	default:
		return nil
	}
}

func (c clipboardCommand) String() string {
	if len(c.Args) == 0 {
		return c.Name
	}
	return c.Name + " " + strings.Join(c.Args, " ")
}
