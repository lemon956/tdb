package app

import (
	"errors"
	"os/exec"
	"testing"

	"tdb/internal/config"
	"tdb/internal/db"
)

func TestResolveIconStyleHonorsEnvironmentOverride(t *testing.T) {
	tests := []struct {
		value string
		want  IconStyle
	}{
		{value: "nerd", want: IconStyleNerd},
		{value: "unicode", want: IconStyleUnicode},
	}

	for _, test := range tests {
		got := ResolveIconStyle(IconDetectOptions{
			Env:     map[string]string{"TDB_ICON_STYLE": test.value},
			FontCmd: func() ([]byte, error) { return nil, errors.New("should not run") },
		})

		if got != test.want {
			t.Fatalf("ResolveIconStyle(%q) = %s, want %s", test.value, got, test.want)
		}
	}
}

func TestResolveIconStyleDetectsNerdFontFromFontconfig(t *testing.T) {
	got := ResolveIconStyle(IconDetectOptions{
		Env: map[string]string{},
		FontCmd: func() ([]byte, error) {
			return []byte("JetBrainsMono Nerd Font\nSymbols Nerd Font Mono\n"), nil
		},
	})

	if got != IconStyleNerd {
		t.Fatalf("ResolveIconStyle = %s, want nerd", got)
	}
}

func TestResolveIconStyleFallsBackToUnicode(t *testing.T) {
	tests := []struct {
		name string
		cmd  func() ([]byte, error)
	}{
		{name: "no fontconfig", cmd: func() ([]byte, error) { return nil, exec.ErrNotFound }},
		{name: "no match", cmd: func() ([]byte, error) { return []byte("Arial\nMonaco\n"), nil }},
	}

	for _, test := range tests {
		got := ResolveIconStyle(IconDetectOptions{Env: map[string]string{}, FontCmd: test.cmd})

		if got != IconStyleUnicode {
			t.Fatalf("%s: ResolveIconStyle = %s, want unicode", test.name, got)
		}
	}
}

func TestIconSetForStyleUsesDifferentPrefixes(t *testing.T) {
	nerd := IconSetForStyle(IconStyleNerd)
	unicode := IconSetForStyle(IconStyleUnicode)

	if nerd.Database == unicode.Database || nerd.Collection == unicode.Collection || nerd.Lock == unicode.Lock {
		t.Fatalf("icon sets should differ: nerd=%+v unicode=%+v", nerd, unicode)
	}
}

func TestDriverIconUsesOfficialGlyphsWithTextFallback(t *testing.T) {
	nerd := IconSetForStyle(IconStyleNerd)
	unicode := IconSetForStyle(IconStyleUnicode)

	// Nerd style: official devicon brand glyphs + brand color.
	for _, drv := range []config.Driver{config.DriverMySQL, config.DriverMongo, config.DriverRedis} {
		glyph, color := nerd.DriverIcon(drv)
		if glyph == "" || color == "" {
			t.Fatalf("nerd %s should have a brand glyph and color, got %q/%q", drv, glyph, color)
		}
	}
	// Unicode style: no brand glyph for these → caller uses text; color still set.
	for _, drv := range []config.Driver{config.DriverMySQL, config.DriverMongo, config.DriverRedis} {
		glyph, color := unicode.DriverIcon(drv)
		if glyph != "" {
			t.Fatalf("unicode %s should have no brand glyph, got %q", drv, glyph)
		}
		if color == "" {
			t.Fatalf("unicode %s should still return a brand color", drv)
		}
	}
	// Doris has no official glyph but uses a close shape (») in both styles.
	for _, set := range []IconSet{nerd, unicode} {
		glyph, color := set.DriverIcon(config.DriverDoris)
		if glyph != "»" || color != brandDoris {
			t.Fatalf("doris should use » / %s, got %q / %q", brandDoris, glyph, color)
		}
	}
}

func TestObjectIconByTypeAndRedisSubType(t *testing.T) {
	set := IconSetForStyle(IconStyleUnicode)
	cases := map[string]string{
		set.Table:      set.ObjectIcon(db.Object{Type: db.ObjectTable}),
		set.View:       set.ObjectIcon(db.Object{Type: db.ObjectView}),
		set.Collection: set.ObjectIcon(db.Object{Type: db.ObjectCollection}),
		set.KeyList:    set.ObjectIcon(db.Object{Type: db.ObjectKey, SubType: "list"}),
		set.KeyHash:    set.ObjectIcon(db.Object{Type: db.ObjectKey, SubType: "hash"}),
		set.Key:        set.ObjectIcon(db.Object{Type: db.ObjectKey}),
	}
	for want, got := range cases {
		if got != want {
			t.Fatalf("ObjectIcon = %q, want %q", got, want)
		}
	}
}
