package app

import (
	"strings"
	"testing"
	"unicode/utf8"

	"tdb/internal/config"
)

func classAt(cls []synClass, text, sub string) synClass {
	i := strings.Index(text, sub)
	if i < 0 {
		return synPlain
	}
	return cls[i]
}

func TestHighlightSQLClasses(t *testing.T) {
	text := "SELECT id FROM t WHERE x=1 -- note"
	cls := highlightClasses(config.DriverMySQL, text)
	if classAt(cls, text, "SELECT") != synKw {
		t.Fatal("SELECT should be a keyword")
	}
	if classAt(cls, text, "FROM") != synKw || classAt(cls, text, "WHERE") != synKw {
		t.Fatal("FROM/WHERE should be keywords")
	}
	if classAt(cls, text, "1") != synNum {
		t.Fatal("1 should be a number")
	}
	if classAt(cls, text, "=") != synPun {
		t.Fatal("= should be punctuation/operator")
	}
	if classAt(cls, text, "-- note") != synCom {
		t.Fatal("-- note should be a comment")
	}
	// COUNT( should highlight as a function.
	fn := "SELECT COUNT(*)"
	fcls := highlightClasses(config.DriverMySQL, fn)
	if classAt(fcls, fn, "COUNT") != synFn {
		t.Fatal("COUNT should be a function")
	}
}

func TestHighlightMongoAndRedisClasses(t *testing.T) {
	m := "db.c.find({ $gt: 5 })"
	mc := highlightClasses(config.DriverMongo, m)
	if classAt(mc, m, "$gt") != synVar {
		t.Fatal("$gt should be a variable/operator")
	}
	if classAt(mc, m, "5") != synNum {
		t.Fatal("5 should be a number")
	}
	if classAt(mc, m, "find") != synFn {
		t.Fatal("find should be a method/function")
	}

	r := "GET mykey"
	rc := highlightClasses(config.DriverRedis, r)
	if rc[0] != synKw {
		t.Fatal("GET should be a command keyword")
	}
	if classAt(rc, r, "mykey") != synPlain {
		t.Fatal("an argument should stay plain")
	}
}

func TestRenderQueryBufferHighlightsWithoutAlteringText(t *testing.T) {
	model := newWorkspaceVimModel(t) // driver mysql
	model.focus = FocusSidebar       // no cursor cell, so stripANSI is exactly the text
	tab := workspaceTab{QueryBuffer: "SELECT 真 FROM t", WorkspaceFocus: workspaceFocusEditor, VimMode: vimModeNormal}

	out := model.renderQueryBuffer(tab)
	if stripANSI(out) != tab.QueryBuffer {
		t.Fatalf("highlighting must not change characters: %q vs %q", stripANSI(out), tab.QueryBuffer)
	}
	if !utf8.ValidString(stripANSI(out)) {
		t.Fatal("rendered output must remain valid UTF-8 (CJK safe)")
	}
	// The keyword color (#C792EA => 199;146;234) should appear.
	if !strings.Contains(out, "199;146;234") {
		t.Fatalf("SELECT/FROM should be colored as keywords:\n%q", out)
	}
}
