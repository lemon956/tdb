package terminal

import (
	"fmt"
	"io"
)

const (
	enterAlternateScreen = "\x1b[?1049h"
	exitAlternateScreen  = "\x1b[?1049l"
	hideCursor           = "\x1b[?25l"
	showCursor           = "\x1b[?25h"
	clearScreen          = "\x1b[2J"
	cursorHome           = "\x1b[H"
)

type Screen struct {
	out io.Writer
}

func NewScreen(out io.Writer) *Screen {
	return &Screen{out: out}
}

func (s *Screen) Enter() error {
	_, err := fmt.Fprint(s.out, enterAlternateScreen, hideCursor, clearScreen, cursorHome)
	return err
}

func (s *Screen) Exit() error {
	_, err := fmt.Fprint(s.out, showCursor, exitAlternateScreen)
	return err
}
