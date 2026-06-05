package app

import (
	"errors"
	"os/exec"
	"testing"
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

	if nerd.Connection == unicode.Connection || nerd.Database == unicode.Database || nerd.Collection == unicode.Collection {
		t.Fatalf("icon sets should differ: nerd=%+v unicode=%+v", nerd, unicode)
	}
}
