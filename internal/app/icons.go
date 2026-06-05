package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"tdb/internal/config"
	"tdb/internal/db"
)

type IconStyle string

const (
	IconStyleUnicode IconStyle = "unicode"
	IconStyleNerd    IconStyle = "nerd"
)

// Brand colors used to tint each driver's icon/label so connections read as the
// real product (mysql blue, mongodb green, redis red, doris teal-green).
const (
	brandMySQL = "#00758F"
	brandMongo = "#47A248"
	brandRedis = "#DC382D"
	brandDoris = "#0DBE85"
)

type IconSet struct {
	Database   string
	Collection string
	Metadata   string
	Expanded   string
	Collapsed  string

	// Object/type icons.
	Table string
	View  string
	Key   string
	// Redis key data-type icons.
	KeyString string
	KeyList   string
	KeySet    string
	KeyHash   string
	KeyZSet   string
	KeyStream string
	// Lock marks a read-only connection.
	Lock string

	// Driver brand glyphs. An empty glyph means "no official glyph in this style"
	// — the caller falls back to rendering the driver name as text.
	DriverMySQL string
	DriverMongo string
	DriverRedis string
	DriverDoris string
}

// DriverIcon returns the brand glyph (possibly "") and brand color for a driver.
// An empty glyph signals the caller to render the driver name as text instead.
func (s IconSet) DriverIcon(driver config.Driver) (glyph, color string) {
	switch driver {
	case config.DriverMySQL:
		return s.DriverMySQL, brandMySQL
	case config.DriverMongo:
		return s.DriverMongo, brandMongo
	case config.DriverRedis:
		return s.DriverRedis, brandRedis
	case config.DriverDoris:
		return s.DriverDoris, brandDoris
	default:
		return "", ""
	}
}

// ObjectIcon picks the icon for a database object by its type (and, for redis
// keys, by data sub-type).
func (s IconSet) ObjectIcon(object db.Object) string {
	switch object.Type {
	case db.ObjectView:
		return s.View
	case db.ObjectCollection:
		return s.Collection
	case db.ObjectKey:
		return s.keyIcon(object.SubType)
	default:
		return s.Table
	}
}

func (s IconSet) keyIcon(subType string) string {
	switch subType {
	case "string":
		return s.KeyString
	case "list":
		return s.KeyList
	case "set":
		return s.KeySet
	case "hash":
		return s.KeyHash
	case "zset":
		return s.KeyZSet
	case "stream":
		return s.KeyStream
	default:
		return s.Key
	}
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
	// Object/type and redis sub-type icons are geometric Unicode, shared by both
	// styles (they render fine in Nerd-Font terminals too, avoiding tofu).
	typeIcons := IconSet{
		Table:     "▦",
		View:      "◫",
		Key:       "\U0001F511",
		KeyString: "\"",
		KeyList:   "☰",
		KeySet:    "∈",
		KeyHash:   "#",
		KeyZSet:   "⇅",
		KeyStream: "≋",
		// Doris has no Nerd-Font glyph; its logo is a teal angular double-chevron,
		// so this double-angle is the closest shape in any terminal.
		DriverDoris: "»",
	}
	switch style {
	case IconStyleNerd:
		set := typeIcons
		set.Database = ""
		set.Collection = ""
		set.Metadata = ""
		set.Expanded = ""
		set.Collapsed = ""
		set.Lock = ""
		// Official devicon brand logos.
		set.DriverMySQL = ""
		set.DriverMongo = ""
		set.DriverRedis = ""
		return set
	default:
		set := typeIcons
		set.Database = "◆"
		set.Collection = "◇"
		set.Metadata = "▪"
		set.Expanded = "▾"
		set.Collapsed = "▸"
		set.Lock = "\U0001F512"
		// No Unicode brand logos for these — caller falls back to the driver name.
		return set
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
