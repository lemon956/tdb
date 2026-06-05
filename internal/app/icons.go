package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

type IconStyle string

const (
	IconStyleUnicode IconStyle = "unicode"
	IconStyleNerd    IconStyle = "nerd"
)

type IconSet struct {
	Connection string
	Database   string
	Collection string
	Metadata   string
	Expanded   string
	Collapsed  string
}

type IconDetectOptions struct {
	Env     map[string]string
	FontCmd func() ([]byte, error)
}

func ResolveIconStyle(options IconDetectOptions) IconStyle {
	if options.Env == nil {
		options.Env = currentEnvMap()
	}
	switch strings.ToLower(strings.TrimSpace(options.Env["TDB_ICON_STYLE"])) {
	case "nerd", "nerdfont", "nerd-font":
		return IconStyleNerd
	case "unicode", "emoji":
		return IconStyleUnicode
	}

	fontCmd := options.FontCmd
	if fontCmd == nil {
		fontCmd = listSystemFonts
	}
	out, err := fontCmd()
	if err != nil {
		return IconStyleUnicode
	}
	if hasNerdFontMarker(out) {
		return IconStyleNerd
	}
	return IconStyleUnicode
}

func IconSetForStyle(style IconStyle) IconSet {
	switch style {
	case IconStyleNerd:
		return IconSet{
			Connection: "\uf1c0",
			Database:   "\ue706",
			Collection: "\uf0ce",
			Metadata:   "\uf02b",
			Expanded:   "\uf078",
			Collapsed:  "\uf054",
		}
	default:
		return IconSet{
			Connection: "🔌",
			Database:   "◆",
			Collection: "◇",
			Metadata:   "▪",
			Expanded:   "▾",
			Collapsed:  "▸",
		}
	}
}

func currentEnvMap() map[string]string {
	env := map[string]string{}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			env[key] = value
		}
	}
	return env
}

func listSystemFonts() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	return exec.CommandContext(ctx, "fc-list").Output()
}

func hasNerdFontMarker(output []byte) bool {
	lower := bytes.ToLower(output)
	return bytes.Contains(lower, []byte("nerd font")) ||
		bytes.Contains(lower, []byte("symbols nerd font")) ||
		bytes.Contains(lower, []byte("fontawesome")) ||
		bytes.Contains(lower, []byte("powerline"))
}
