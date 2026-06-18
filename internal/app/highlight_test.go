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

func TestHighlightJSONClasses(t *testing.T) {
	text := `{"name":"x","n":12,"ok":true,"z":null}`
	cls := jsonClasses(text)
	if classAt(cls, text, `"name"`) != synKey {
		t.Fatal(`"name" should be a JSON key`)
	}
	if classAt(cls, text, `"x"`) != synStr {
		t.Fatal(`"x" should be a string value`)
	}
	if classAt(cls, text, "12") != synNum {
		t.Fatal("12 should be a number")
	}
	if classAt(cls, text, "true") != synBool {
		t.Fatal("true should be a bool")
	}
	if classAt(cls, text, "null") != synBool {
		t.Fatal("null should be null/bool")
	}
	if classAt(cls, text, "{") != synPun || classAt(cls, text, ":") != synPun {
		t.Fatal("braces/colon should be punctuation")
	}
}

func TestHighlightJSONObjectIDAndCJK(t *testing.T) {
	text := `{"_id":ObjectId("507f1f77bcf86cd799439011"),"创建":ISODate("2026-01-01")}`
	cls := jsonClasses(text)
	if classAt(cls, text, `"_id"`) != synKey {
		t.Fatal(`"_id" should be a key`)
	}
	if classAt(cls, text, "ObjectId") != synFn {
		t.Fatal("ObjectId should be a function/constructor")
	}
	if classAt(cls, text, "ISODate") != synFn {
		t.Fatal("ISODate should be a function/constructor")
	}
	if classAt(cls, text, `"创建"`) != synKey {
		t.Fatal("CJK key should be a key")
	}
	// The hex argument is a normal string value.
	if classAt(cls, text, `"507f1f77bcf86cd799439011"`) != synStr {
		t.Fatal("ObjectId hex arg should be a string value")
	}
}

func TestHighlightJSONNested(t *testing.T) {
	text := `{"a":{"b":[1,2]},"s":"北京"}`
	cls := jsonClasses(text)
	if classAt(cls, text, `"a"`) != synKey || classAt(cls, text, `"b"`) != synKey {
		t.Fatal("nested keys should be keys")
	}
	if classAt(cls, text, `"s"`) != synKey {
		t.Fatal(`"s" should be a key`)
	}
	if classAt(cls, text, `"北京"`) != synStr {
		t.Fatal("CJK string value should be a string")
	}
}
